package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TripleStore implements MemPalace's temporal entity-relationship graph as a
// plain PostgreSQL triple store (subject → predicate → object with validity
// windows). It mirrors the upstream SQLite knowledge_graph.py semantics but
// requires no Apache AGE extension — it is always available.
//
// This is distinct from the AGE-backed Graph (entity/relation property graph).
// The temporal triple store is the upstream-compatible KG surface
// (kg_add / kg_query / kg_invalidate / kg_timeline / kg_stats).
type TripleStore struct {
	pool   *pgxpool.Pool
	schema string
}

// NewTripleStore returns a TripleStore for the given tenant.
func NewTripleStore(pool *pgxpool.Pool, tenantID string) *TripleStore {
	return &TripleStore{pool: pool, schema: SafeSchemaName(tenantID)}
}

// KGFact is one relationship row, optionally direction-annotated.
type KGFact struct {
	Direction    string  `json:"direction,omitempty"`
	Subject      string  `json:"subject"`
	Predicate    string  `json:"predicate"`
	Object       string  `json:"object"`
	ValidFrom    *string `json:"valid_from"`
	ValidTo      *string `json:"valid_to"`
	Confidence   float64 `json:"confidence,omitempty"`
	SourceCloset *string `json:"source_closet,omitempty"`
	Current      bool    `json:"current"`
}

// KGStats summarises the triple store.
type KGStats struct {
	Entities          int      `json:"entities"`
	Triples           int      `json:"triples"`
	CurrentFacts      int      `json:"current_facts"`
	ExpiredFacts      int      `json:"expired_facts"`
	RelationshipTypes []string `json:"relationship_types"`
}

// ProvisionKG creates the temporal KG tables for a tenant (idempotent).
// schema is [a-z0-9_] (from SafeSchemaName) so it is safe to interpolate.
func ProvisionKG(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	schema := SafeSchemaName(tenantID)
	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.kg_entities (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL,
				type        TEXT DEFAULT 'unknown',
				properties  JSONB DEFAULT '{}',
				created_at  TIMESTAMPTZ DEFAULT now()
			)`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.kg_triples (
				id            TEXT PRIMARY KEY,
				subject       TEXT NOT NULL,
				predicate     TEXT NOT NULL,
				object        TEXT NOT NULL,
				valid_from    TEXT,
				valid_to      TEXT,
				confidence    DOUBLE PRECISION DEFAULT 1.0,
				source_closet TEXT,
				source_file   TEXT,
				extracted_at  TIMESTAMPTZ DEFAULT now()
			)`, schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS kg_triples_subject_%s ON %s.kg_triples(subject)`, schema, schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS kg_triples_object_%s ON %s.kg_triples(object)`, schema, schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS kg_triples_pred_%s ON %s.kg_triples(predicate)`, schema, schema),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS kg_triples_valid_%s ON %s.kg_triples(valid_from, valid_to)`, schema, schema),
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("provision kg %s: %w", schema, err)
		}
	}
	return nil
}

// entityID normalises a name to a stable id (mirrors upstream _entity_id).
func entityID(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "'", "")
	return s
}

func normPredicate(p string) string {
	return strings.ReplaceAll(strings.ToLower(p), " ", "_")
}

// AddEntity inserts or replaces an entity node.
func (t *TripleStore) AddEntity(ctx context.Context, name, entityType string) error {
	if entityType == "" {
		entityType = "unknown"
	}
	_, err := t.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.kg_entities (id, name, type) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, type = EXCLUDED.type`, t.schema),
		entityID(name), name, entityType)
	return err
}

// AddTriple adds subject → predicate → object with optional validity start.
// Auto-creates referenced entities. Returns the existing id if an identical
// still-valid triple already exists (idempotent).
func (t *TripleStore) AddTriple(ctx context.Context, subject, predicate, object, validFrom, sourceCloset string) (string, error) {
	if subject == "" || predicate == "" || object == "" {
		return "", fmt.Errorf("subject, predicate, and object are required")
	}
	subID := entityID(subject)
	objID := entityID(object)
	pred := normPredicate(predicate)

	// Auto-create entities (name-only; type defaults to 'unknown').
	for _, e := range []struct{ id, name string }{{subID, subject}, {objID, object}} {
		if _, err := t.pool.Exec(ctx, fmt.Sprintf(
			`INSERT INTO %s.kg_entities (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, t.schema),
			e.id, e.name); err != nil {
			return "", fmt.Errorf("ensure entity: %w", err)
		}
	}

	// Idempotency: identical still-valid triple already present?
	var existing string
	err := t.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT id FROM %s.kg_triples WHERE subject=$1 AND predicate=$2 AND object=$3 AND valid_to IS NULL`, t.schema),
		subID, pred, objID).Scan(&existing)
	if err == nil && existing != "" {
		return existing, nil
	}

	h := sha256.Sum256([]byte(validFrom + time.Now().Format(time.RFC3339Nano)))
	tripleID := fmt.Sprintf("t_%s_%s_%s_%s", subID, pred, objID, hex.EncodeToString(h[:])[:12])

	if _, err := t.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.kg_triples (id, subject, predicate, object, valid_from, source_closet)
		VALUES ($1, $2, $3, $4, $5, $6)`, t.schema),
		tripleID, subID, pred, objID, nullIfEmpty(validFrom), nullIfEmpty(sourceCloset)); err != nil {
		return "", fmt.Errorf("insert triple: %w", err)
	}
	return tripleID, nil
}

// Invalidate sets valid_to on the matching still-valid triple(s).
func (t *TripleStore) Invalidate(ctx context.Context, subject, predicate, object, ended string) error {
	if ended == "" {
		ended = time.Now().Format("2006-01-02")
	}
	_, err := t.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.kg_triples SET valid_to=$1
		WHERE subject=$2 AND predicate=$3 AND object=$4 AND valid_to IS NULL`, t.schema),
		ended, entityID(subject), normPredicate(predicate), entityID(object))
	return err
}

// QueryEntity returns relationships for an entity, optionally as-of a date.
// direction: "outgoing", "incoming", or "both".
func (t *TripleStore) QueryEntity(ctx context.Context, name, asOf, direction string) ([]KGFact, error) {
	eid := entityID(name)
	facts := []KGFact{}

	if direction == "outgoing" || direction == "both" {
		rows, err := t.queryFacts(ctx, true, eid, asOf)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].Direction = "outgoing"
			rows[i].Subject = name
		}
		facts = append(facts, rows...)
	}
	if direction == "incoming" || direction == "both" {
		rows, err := t.queryFacts(ctx, false, eid, asOf)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].Direction = "incoming"
			rows[i].Object = name
		}
		facts = append(facts, rows...)
	}
	return facts, nil
}

// queryFacts fetches outgoing (subject=eid) or incoming (object=eid) facts.
// The joined entity name fills the opposite endpoint.
func (t *TripleStore) queryFacts(ctx context.Context, outgoing bool, eid, asOf string) ([]KGFact, error) {
	var join, where string
	if outgoing {
		join = fmt.Sprintf("JOIN %s.kg_entities e ON t.object = e.id", t.schema)
		where = "t.subject = $1"
	} else {
		join = fmt.Sprintf("JOIN %s.kg_entities e ON t.subject = e.id", t.schema)
		where = "t.object = $1"
	}
	args := []any{eid}
	if asOf != "" {
		where += " AND (t.valid_from IS NULL OR t.valid_from <= $2) AND (t.valid_to IS NULL OR t.valid_to >= $2)"
		args = append(args, asOf)
	}
	sql := fmt.Sprintf(`
		SELECT t.predicate, e.name, t.valid_from, t.valid_to, t.confidence, t.source_closet
		FROM %s.kg_triples t %s WHERE %s`, t.schema, join, where)

	rows, err := t.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query facts: %w", err)
	}
	defer rows.Close()

	var out []KGFact
	for rows.Next() {
		var f KGFact
		var endpoint string
		if err := rows.Scan(&f.Predicate, &endpoint, &f.ValidFrom, &f.ValidTo, &f.Confidence, &f.SourceCloset); err != nil {
			return nil, err
		}
		if outgoing {
			f.Object = endpoint
		} else {
			f.Subject = endpoint
		}
		f.Current = f.ValidTo == nil
		out = append(out, f)
	}
	return out, rows.Err()
}

// Timeline returns facts chronologically (by valid_from), optionally filtered
// to a single entity. Capped at 100 rows like upstream.
func (t *TripleStore) Timeline(ctx context.Context, entity string) ([]KGFact, error) {
	base := fmt.Sprintf(`
		SELECT t.predicate, s.name, o.name, t.valid_from, t.valid_to, t.confidence, t.source_closet
		FROM %s.kg_triples t
		JOIN %s.kg_entities s ON t.subject = s.id
		JOIN %s.kg_entities o ON t.object = o.id`, t.schema, t.schema, t.schema)

	var sql string
	var args []any
	if entity != "" {
		eid := entityID(entity)
		sql = base + ` WHERE (t.subject = $1 OR t.object = $1) ORDER BY t.valid_from ASC NULLS LAST LIMIT 100`
		args = []any{eid}
	} else {
		sql = base + ` ORDER BY t.valid_from ASC NULLS LAST LIMIT 100`
	}

	rows, err := t.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("timeline: %w", err)
	}
	defer rows.Close()

	out := []KGFact{}
	for rows.Next() {
		var f KGFact
		if err := rows.Scan(&f.Predicate, &f.Subject, &f.Object, &f.ValidFrom, &f.ValidTo, &f.Confidence, &f.SourceCloset); err != nil {
			return nil, err
		}
		f.Current = f.ValidTo == nil
		out = append(out, f)
	}
	return out, rows.Err()
}

// Stats returns the KG overview.
func (t *TripleStore) Stats(ctx context.Context) (*KGStats, error) {
	st := &KGStats{RelationshipTypes: []string{}}

	if err := t.pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s.kg_entities`, t.schema)).Scan(&st.Entities); err != nil {
		return nil, err
	}
	if err := t.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE valid_to IS NULL)
		FROM %s.kg_triples`, t.schema)).Scan(&st.Triples, &st.CurrentFacts); err != nil {
		return nil, err
	}
	st.ExpiredFacts = st.Triples - st.CurrentFacts

	rows, err := t.pool.Query(ctx, fmt.Sprintf(
		`SELECT DISTINCT predicate FROM %s.kg_triples ORDER BY predicate`, t.schema))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		st.RelationshipTypes = append(st.RelationshipTypes, p)
	}
	return st, rows.Err()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
