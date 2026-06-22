package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Graph wraps a pgxpool for Apache AGE knowledge-graph operations.
// Each tenant gets their own AGE graph named identically to their pg schema
// (e.g. mp_default), so isolation is at the graph level.
type Graph struct {
	pool      *pgxpool.Pool
	graphName string
}

// KGEntity is a graph node.
type KGEntity struct {
	Name        string `json:"name"`
	EntityType  string `json:"entity_type,omitempty"`
	Description string `json:"description,omitempty"`
}

// KGRelation is a directed edge.
type KGRelation struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

// kgGraphName returns the AGE graph name for a tenant.
// Uses a "kg_" prefix to avoid colliding with the pgvector schema (mp_{tenant}).
func kgGraphName(tenantID string) string {
	schema := SafeSchemaName(tenantID) // e.g. "mp_default"
	name := "kg_" + schema             // e.g. "kg_mp_default"
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// NewGraph returns a Graph for the given tenant.
func NewGraph(pool *pgxpool.Pool, tenantID string) *Graph {
	return &Graph{pool: pool, graphName: kgGraphName(tenantID)}
}

// ProvisionGraph creates the AGE extension and the tenant's graph (idempotent).
// graphName only contains [a-z0-9_] — safe to interpolate into SQL.
func ProvisionGraph(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	graphName := kgGraphName(tenantID)

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS age"); err != nil {
		log.Printf("graph: CREATE EXTENSION age: %v (must be pre-installed)", err)
	}

	// ag_catalog.create_graph errors when graph already exists — guard with existence check.
	// PL/pgSQL does not support $N parameters, so graphName is interpolated directly.
	// It is safe: SafeSchemaName guarantees [a-z0-9_] only.
	sql := `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM ag_catalog.ag_graph WHERE name = '` + graphName + `') THEN
			PERFORM ag_catalog.create_graph('` + graphName + `');
		END IF;
	END $$`

	if _, err := pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("provision graph %s: %w", graphName, err)
	}
	log.Printf("graph: %s ready", graphName)
	return nil
}

// ---------------------------------------------------------------------------
// Write operations
// ---------------------------------------------------------------------------

// AddEntity creates or updates an entity node (MERGE on name).
func (g *Graph) AddEntity(ctx context.Context, name, entityType, description string) (*KGEntity, error) {
	if name == "" {
		return nil, fmt.Errorf("entity name is required")
	}
	if entityType == "" {
		entityType = "entity"
	}

	params := map[string]any{
		"name":        name,
		"entity_type": entityType,
		"description": description,
	}
	cypher := `
		MERGE (n:Entity {name: $name})
		SET n.entity_type = $entity_type, n.description = $description
		RETURN n.name AS name, n.entity_type AS etype, n.description AS info`

	rows, err := g.queryCypher(ctx, cypher, params, 3)
	if err != nil {
		return nil, fmt.Errorf("add entity: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("add entity: no result")
	}
	return &KGEntity{
		Name:        agtypeStr(rows[0][0]),
		EntityType:  agtypeStr(rows[0][1]),
		Description: agtypeStr(rows[0][2]),
	}, nil
}

// AddRelation creates or updates a directed relationship between two entities.
// relType is sanitized to [A-Z0-9_] before being interpolated into Cypher
// (Cypher does not support parameterized relationship types).
func (g *Graph) AddRelation(ctx context.Context, fromEntity, relType, toEntity string) error {
	if fromEntity == "" || toEntity == "" || relType == "" {
		return fmt.Errorf("from_entity, relation_type, and to_entity are required")
	}
	safeType := safeCypherRelType(relType)

	params := map[string]any{"from": fromEntity, "to": toEntity}
	cypher := `
		MATCH (a:Entity {name: $from}), (b:Entity {name: $to})
		MERGE (a)-[r:` + safeType + `]->(b)
		RETURN 1 AS ok`

	rows, err := g.queryCypher(ctx, cypher, params, 1)
	if err != nil {
		return fmt.Errorf("add relation: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("one or both entities not found: %q, %q", fromEntity, toEntity)
	}
	return nil
}

// DeleteEntity removes an entity and all its relationships (DETACH DELETE).
func (g *Graph) DeleteEntity(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("name is required")
	}

	// Count before delete to return meaningful "found" boolean
	countRows, err := g.queryCypher(ctx,
		`MATCH (n:Entity {name: $name}) RETURN count(n) AS c`,
		map[string]any{"name": name}, 1)
	if err != nil {
		return false, err
	}
	if len(countRows) == 0 || agtypeStr(countRows[0][0]) == "0" {
		return false, nil
	}

	_, err = g.queryCypher(ctx,
		`MATCH (n:Entity {name: $name}) DETACH DELETE n RETURN 1 AS ok`,
		map[string]any{"name": name}, 1)
	if err != nil {
		return false, fmt.Errorf("delete entity: %w", err)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Read operations
// ---------------------------------------------------------------------------

// GetEntity returns an entity and all its direct relationships.
func (g *Graph) GetEntity(ctx context.Context, name string) (*KGEntity, []KGRelation, error) {
	if name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}

	// Fetch entity
	eRows, err := g.queryCypher(ctx,
		`MATCH (n:Entity {name: $name}) RETURN n.name AS name, n.entity_type AS etype, n.description AS info`,
		map[string]any{"name": name}, 3)
	if err != nil {
		return nil, nil, err
	}
	if len(eRows) == 0 {
		return nil, nil, nil // not found
	}
	entity := &KGEntity{
		Name:        agtypeStr(eRows[0][0]),
		EntityType:  agtypeStr(eRows[0][1]),
		Description: agtypeStr(eRows[0][2]),
	}

	// Outgoing relations
	outRows, err := g.queryCypher(ctx,
		`MATCH (n:Entity {name: $name})-[r]->(m:Entity) RETURN type(r) AS rtype, m.name AS to_name`,
		map[string]any{"name": name}, 2)
	if err != nil {
		return entity, nil, err
	}

	// Incoming relations
	inRows, err := g.queryCypher(ctx,
		`MATCH (n:Entity {name: $name})<-[r]-(m:Entity) RETURN type(r) AS rtype, m.name AS from_name`,
		map[string]any{"name": name}, 2)
	if err != nil {
		return entity, nil, err
	}

	var rels []KGRelation
	for _, r := range outRows {
		rels = append(rels, KGRelation{Type: agtypeStr(r[0]), From: name, To: agtypeStr(r[1])})
	}
	for _, r := range inRows {
		rels = append(rels, KGRelation{Type: agtypeStr(r[0]), From: agtypeStr(r[1]), To: name})
	}
	return entity, rels, nil
}

// SearchEntities finds entities whose name contains the query string.
func (g *Graph) SearchEntities(ctx context.Context, query, entityType string, limit int) ([]KGEntity, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	params := map[string]any{"query": query, "limit": limit}

	var cypher string
	if entityType != "" {
		params["etype"] = entityType
		cypher = `MATCH (n:Entity) WHERE n.name CONTAINS $query AND n.entity_type = $etype
			RETURN n.name AS name, n.entity_type AS etype, n.description AS info
			LIMIT $limit`
	} else {
		cypher = `MATCH (n:Entity) WHERE n.name CONTAINS $query
			RETURN n.name AS name, n.entity_type AS etype, n.description AS info
			LIMIT $limit`
	}

	rows, err := g.queryCypher(ctx, cypher, params, 3)
	if err != nil {
		return nil, fmt.Errorf("search entities: %w", err)
	}

	entities := make([]KGEntity, len(rows))
	for i, r := range rows {
		entities[i] = KGEntity{
			Name:        agtypeStr(r[0]),
			EntityType:  agtypeStr(r[1]),
			Description: agtypeStr(r[2]),
		}
	}
	return entities, nil
}

// Traverse returns all entities reachable from startName within maxDepth hops,
// along with the direct relationships between them.
func (g *Graph) Traverse(ctx context.Context, startName string, maxDepth int) ([]KGEntity, []KGRelation, error) {
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 3 {
		maxDepth = 3
	}

	// Collect reachable entity names first
	depthStr := fmt.Sprintf("1..%d", maxDepth)
	entityRows, err := g.queryCypher(ctx,
		`MATCH (start:Entity {name: $start})-[*`+depthStr+`]-(n:Entity)
		 WHERE start <> n
		 RETURN DISTINCT n.name AS name, n.entity_type AS etype, n.description AS info`,
		map[string]any{"start": startName}, 3)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse entities: %w", err)
	}

	// Include start entity
	startRows, err := g.queryCypher(ctx,
		`MATCH (n:Entity {name: $name}) RETURN n.name, n.entity_type, n.description`,
		map[string]any{"name": startName}, 3)
	if err != nil {
		return nil, nil, err
	}

	var entities []KGEntity
	if len(startRows) > 0 {
		entities = append(entities, KGEntity{
			Name:        agtypeStr(startRows[0][0]),
			EntityType:  agtypeStr(startRows[0][1]),
			Description: agtypeStr(startRows[0][2]),
		})
	}
	for _, r := range entityRows {
		entities = append(entities, KGEntity{
			Name:        agtypeStr(r[0]),
			EntityType:  agtypeStr(r[1]),
			Description: agtypeStr(r[2]),
		})
	}

	// Collect all edges where both endpoints are within the discovered entity set.
	// Build the name list in Go to avoid complex AGE aggregation patterns.
	names := make([]string, len(entities))
	for i, e := range entities {
		names[i] = e.Name
	}

	var rels []KGRelation
	if len(names) > 1 {
		relRows, err := g.queryEdgesBetween(ctx, names)
		if err != nil {
			return entities, nil, fmt.Errorf("traverse relations: %w", err)
		}
		for _, r := range relRows {
			rels = append(rels, KGRelation{
				Type: agtypeStr(r[0]),
				From: agtypeStr(r[1]),
				To:   agtypeStr(r[2]),
			})
		}
	}
	return entities, rels, nil
}

// queryEdgesBetween returns all directed edges (rtype, from_name, to_name) where
// both endpoints are in the given name slice.  Uses individual OR conditions to
// avoid AGE's limited IN-list support with parameters.
func (g *Graph) queryEdgesBetween(ctx context.Context, names []string) ([][]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	// Build parameterized Cypher: MATCH (a:Entity)-[r]->(b:Entity) WHERE a.name = $n0 OR ...
	aOrs := make([]string, len(names))
	bOrs := make([]string, len(names))
	params := map[string]any{}
	for i, n := range names {
		key := fmt.Sprintf("n%d", i)
		params[key] = n
		aOrs[i] = fmt.Sprintf("a.name = $%s", key)
		bOrs[i] = fmt.Sprintf("b.name = $%s", key)
	}
	cypher := fmt.Sprintf(
		`MATCH (a:Entity)-[r]->(b:Entity)
		 WHERE (%s) AND (%s)
		 RETURN DISTINCT type(r) AS rtype, a.name AS from_n, b.name AS to_n`,
		strings.Join(aOrs, " OR "),
		strings.Join(bOrs, " OR "),
	)
	return g.queryCypher(ctx, cypher, params, 3)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// queryCypher executes a Cypher query and returns each row as a slice of ag_catalog.agtype strings.
// ncols must match the number of columns in the Cypher RETURN clause.
func (g *Graph) queryCypher(ctx context.Context, cypher string, params map[string]any, ncols int) ([][]string, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	// Build column definitions: c0 ag_catalog.agtype, c1 ag_catalog.agtype, …
	colDefs := make([]string, ncols)
	colNames := make([]string, ncols)
	for i := range colDefs {
		colNames[i] = fmt.Sprintf("c%d", i)
		colDefs[i] = colNames[i] + " ag_catalog.agtype"
	}

	sql := fmt.Sprintf(
		`SELECT %s FROM ag_catalog.cypher('%s', $$%s$$, $1::ag_catalog.agtype) AS (%s)`,
		strings.Join(colNames, ", "),
		g.graphName,
		cypher,
		strings.Join(colDefs, ", "),
	)

	rows, err := g.pool.Query(ctx, sql, string(paramsJSON))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result [][]string
	for rows.Next() {
		ptrs := make([]*string, ncols)
		args := make([]any, ncols)
		for i := range ptrs {
			args[i] = &ptrs[i]
		}
		if err := rows.Scan(args...); err != nil {
			return nil, err
		}
		row := make([]string, ncols)
		for i, p := range ptrs {
			if p != nil {
				row[i] = *p
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// agtypeStr converts an ag_catalog.agtype-scanned string value to a plain Go string.
// Handles JSON-quoted strings ("Alice" → Alice) and bare values (42 → "42").
func agtypeStr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return ""
	}
	// Strip ::type suffix if present (e.g. "hello"::string)
	if i := strings.LastIndex(s, "::"); i > 0 && !strings.HasPrefix(s, "{") {
		s = s[:i]
	}
	// JSON-quoted string
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var out string
		if err := json.Unmarshal([]byte(s), &out); err == nil {
			return out
		}
	}
	return s
}

// safeCypherRelType sanitizes a relation type to uppercase [A-Z0-9_].
// Falls back to RELATED_TO for empty or invalid input.
func safeCypherRelType(s string) string {
	s = strings.ToUpper(s)
	var b strings.Builder
	for i, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c == '_':
			b.WriteRune(c)
		case c >= '0' && c <= '9' && i > 0:
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "RELATED_TO"
	}
	return b.String()
}
