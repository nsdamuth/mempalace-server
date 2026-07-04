package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Drawer is a single stored memory unit.
type Drawer struct {
	ID       string
	Document string
	Metadata map[string]any
	Distance float64 // populated by Query; 0 otherwise
}

// Collection wraps a pgxpool and a specific tenant schema + drawers table.
type Collection struct {
	pool     *pgxpool.Pool
	schema   string
	table    string
	fqt      string // fully qualified: schema.table
	efSearch int
}

// NewCollection returns a Collection for the given tenant.
func NewCollection(pool *pgxpool.Pool, tenantID string, efSearch int) *Collection {
	schema := SafeSchemaName(tenantID)
	table := safeTableName(DrawersTable)
	return &Collection{
		pool:     pool,
		schema:   schema,
		table:    table,
		fqt:      schema + "." + table,
		efSearch: efSearch,
	}
}

// ---------------------------------------------------------------------------
// Write operations
// ---------------------------------------------------------------------------

// Upsert inserts or replaces drawers.  Uses pgx.Batch for a single round trip.
func (c *Collection) Upsert(ctx context.Context, ids, docs []string, metas []map[string]any, embeddings [][]float32) error {
	return c.batchWrite(ctx, ids, docs, metas, embeddings, true)
}

// Add inserts drawers, silently ignoring duplicates.
func (c *Collection) Add(ctx context.Context, ids, docs []string, metas []map[string]any, embeddings [][]float32) error {
	return c.batchWrite(ctx, ids, docs, metas, embeddings, false)
}

func (c *Collection) batchWrite(ctx context.Context, ids, docs []string, metas []map[string]any, embeddings [][]float32, replace bool) error {
	if len(ids) == 0 {
		return nil
	}

	onConflict := "ON CONFLICT (id) DO NOTHING"
	if replace {
		onConflict = `ON CONFLICT (id) DO UPDATE SET
			document  = EXCLUDED.document,
			embedding = EXCLUDED.embedding,
			metadata  = EXCLUDED.metadata`
	}
	sql := fmt.Sprintf(`
		INSERT INTO %s (id, document, embedding, metadata)
		VALUES ($1, $2, $3, $4)
		%s`, c.fqt, onConflict)

	batch := &pgx.Batch{}
	for i, id := range ids {
		metaBytes, err := json.Marshal(metas[i])
		if err != nil {
			return fmt.Errorf("marshal metadata[%d]: %w", i, err)
		}
		batch.Queue(sql, id, docs[i], pgvector.NewVector(embeddings[i]), metaBytes)
	}

	res := c.pool.SendBatch(ctx, batch)
	defer res.Close()
	for range ids {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("batch write: %w", err)
		}
	}
	return nil
}

// Update patches an existing drawer.  Raises an error if the ID is not found.
// Re-embeds content only when newDoc is non-empty.
func (c *Collection) UpdateOne(ctx context.Context, id, newDoc string, newMeta map[string]any, newEmb []float32) error {
	setParts := []string{}
	args := []any{}
	idx := 1

	if newDoc != "" {
		setParts = append(setParts, fmt.Sprintf("document = $%d", idx))
		idx++
		args = append(args, newDoc)
		setParts = append(setParts, fmt.Sprintf("embedding = $%d", idx))
		idx++
		args = append(args, pgvector.NewVector(newEmb))
	}
	if newMeta != nil {
		b, err := json.Marshal(newMeta)
		if err != nil {
			return err
		}
		setParts = append(setParts, fmt.Sprintf("metadata = $%d", idx))
		idx++
		args = append(args, b)
	}
	if len(setParts) == 0 {
		return nil
	}

	args = append(args, id)
	sql := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d",
		c.fqt, joinComma(setParts), idx)

	tag, err := c.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("drawer not found: %s", id)
	}
	return nil
}

// MoveRoom rewrites the wing/room metadata of every drawer in (fromWing,
// fromRoom) to (toWing, toRoom) in a single statement. The room label is pure
// metadata, so this touches neither the document nor its embedding — no
// re-embedding is required. Drawer IDs are content/location-derived but are
// left unchanged (opaque after a move), matching update_drawer's behaviour.
// Returns the number of drawers moved.
func (c *Collection) MoveRoom(ctx context.Context, fromWing, fromRoom, toWing, toRoom string) (int64, error) {
	sql := fmt.Sprintf(`
		UPDATE %s
		SET metadata = jsonb_set(
			jsonb_set(metadata, '{wing}', to_jsonb($1::text)),
			'{room}', to_jsonb($2::text))
		WHERE metadata ->> 'wing' = $3 AND metadata ->> 'room' = $4`, c.fqt)
	tag, err := c.pool.Exec(ctx, sql, toWing, toRoom, fromWing, fromRoom)
	if err != nil {
		return 0, fmt.Errorf("move room: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Delete removes drawers by ID.
func (c *Collection) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	q := newQB()
	phs := make([]string, len(ids))
	for i, id := range ids {
		phs[i] = q.next(id)
	}
	sql := fmt.Sprintf("DELETE FROM %s WHERE id = ANY(ARRAY[%s]::text[])",
		c.fqt, joinComma(phs))
	_, err := c.pool.Exec(ctx, sql, q.args...)
	return err
}

// ---------------------------------------------------------------------------
// Read operations
// ---------------------------------------------------------------------------

// Query performs an HNSW approximate nearest-neighbour search.
// ef_search is applied via SET LOCAL inside a transaction.
func (c *Collection) Query(ctx context.Context, queryVec []float32, where map[string]any, nResults int) ([]Drawer, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// PostgreSQL SET does not accept $N parameters — interpolate directly.
	// c.efSearch comes from trusted config (int), not user input.
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", c.efSearch)); err != nil {
		return nil, fmt.Errorf("set ef_search: %w", err)
	}

	// $1 = query vector (pre-seeded); WHERE params start at $2
	q := newQB(pgvector.NewVector(queryVec))
	whereSQL, err := WhereToSQL(where, q)
	if err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}

	// Append nResults as final arg
	limitP := q.next(nResults)

	sql := fmt.Sprintf(`
		SELECT id, document, metadata, embedding <=> $1 AS distance
		FROM %s
		WHERE %s
		ORDER BY distance
		LIMIT %s`, c.fqt, whereSQL, limitP)

	rows, err := tx.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	drawers, err := scanDrawers(rows, true)
	if err != nil {
		return nil, err
	}

	return drawers, tx.Commit(ctx)
}

// QueryFTS performs a PostgreSQL full-text search using the ts_doc generated column.
// Uses the 'simple' dictionary (case-fold only) so it works for any language including German.
func (c *Collection) QueryFTS(ctx context.Context, queryText string, where map[string]any, nResults int) ([]Drawer, error) {
	q := newQB(queryText) // $1 = query text
	whereSQL, err := WhereToSQL(where, q)
	if err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}
	limitP := q.next(nResults)

	sql := fmt.Sprintf(`
		SELECT id, document, metadata
		FROM %s
		WHERE %s AND ts_doc @@ plainto_tsquery('simple', $1)
		ORDER BY ts_rank(ts_doc, plainto_tsquery('simple', $1)) DESC
		LIMIT %s`, c.fqt, whereSQL, limitP)

	rows, err := c.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()
	return scanDrawers(rows, false)
}

// QueryHybrid combines vector similarity search with full-text search using
// Reciprocal Rank Fusion (RRF). FTS failures (e.g. column not yet migrated)
// fall back silently to pure vector results.
func (c *Collection) QueryHybrid(ctx context.Context, queryText string, queryVec []float32, where map[string]any, nResults int) ([]Drawer, error) {
	fetch := nResults * 3
	if fetch < 10 {
		fetch = 10
	}

	vecResults, err := c.Query(ctx, queryVec, where, fetch)
	if err != nil {
		return nil, err
	}

	ftsResults, err := c.QueryFTS(ctx, queryText, where, fetch)
	if err != nil {
		// ts_doc column may not exist on old schemas — vector-only fallback
		if nResults < len(vecResults) {
			return vecResults[:nResults], nil
		}
		return vecResults, nil
	}

	return rrfMerge(vecResults, ftsResults, nResults), nil
}

// rrfMerge combines two ranked lists using Reciprocal Rank Fusion.
func rrfMerge(vecHits, ftsHits []Drawer, limit int) []Drawer {
	const k = 60.0
	scores := map[string]float64{}
	byID := map[string]Drawer{}

	for rank, d := range vecHits {
		scores[d.ID] += 1.0 / (k + float64(rank+1))
		byID[d.ID] = d
	}
	for rank, d := range ftsHits {
		scores[d.ID] += 1.0 / (k + float64(rank+1))
		if _, exists := byID[d.ID]; !exists {
			byID[d.ID] = d
		}
	}

	type entry struct {
		id    string
		score float64
	}
	ranked := make([]entry, 0, len(scores))
	for id, s := range scores {
		ranked = append(ranked, entry{id, s})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	out := make([]Drawer, 0, limit)
	for _, e := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, byID[e.id])
	}
	return out
}

// GetByIDs fetches specific drawers by ID.
func (c *Collection) GetByIDs(ctx context.Context, ids []string) ([]Drawer, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := newQB()
	phs := make([]string, len(ids))
	for i, id := range ids {
		phs[i] = q.next(id)
	}
	sql := fmt.Sprintf(
		`SELECT id, document, metadata FROM %s WHERE id = ANY(ARRAY[%s]::text[]) ORDER BY id`,
		c.fqt, joinComma(phs))

	rows, err := c.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDrawers(rows, false)
}

// GetWhere fetches drawers matching a filter with optional pagination.
func (c *Collection) GetWhere(ctx context.Context, where map[string]any, limit, offset int) ([]Drawer, error) {
	q := newQB()
	whereSQL, err := WhereToSQL(where, q)
	if err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}

	limitP := q.next(limit)
	offsetP := q.next(offset)

	sql := fmt.Sprintf(
		`SELECT id, document, metadata FROM %s WHERE %s ORDER BY id LIMIT %s OFFSET %s`,
		c.fqt, whereSQL, limitP, offsetP)

	rows, err := c.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDrawers(rows, false)
}

// MetaOnly returns all metadata rows (no document bodies) matching a filter.
// Used by status/list_wings/list_rooms/get_taxonomy which need to aggregate metadata.
func (c *Collection) MetaOnly(ctx context.Context, where map[string]any) ([]map[string]any, error) {
	q := newQB()
	whereSQL, err := WhereToSQL(where, q)
	if err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}

	sql := fmt.Sprintf(
		`SELECT metadata FROM %s WHERE %s ORDER BY id`, c.fqt, whereSQL)

	rows, err := c.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// WingRoomCounts returns {wing → {room → count}} + total via a single GROUP BY.
// Much faster than fetching all metadata and counting in Go.
func (c *Collection) WingRoomCounts(ctx context.Context) (map[string]map[string]int, int, error) {
	sql := fmt.Sprintf(`
		SELECT
			COALESCE(metadata->>'wing', 'unknown') AS wing,
			COALESCE(metadata->>'room', 'unknown') AS room,
			COUNT(*)::int AS cnt
		FROM %s
		GROUP BY wing, room
		ORDER BY wing, room`, c.fqt)

	rows, err := c.pool.Query(ctx, sql)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	tree := map[string]map[string]int{}
	total := 0
	for rows.Next() {
		var wing, room string
		var cnt int
		if err := rows.Scan(&wing, &room, &cnt); err != nil {
			return nil, 0, err
		}
		if tree[wing] == nil {
			tree[wing] = map[string]int{}
		}
		tree[wing][room] = cnt
		total += cnt
	}
	return tree, total, rows.Err()
}

// Count returns the approximate row count (O(1) via pg_class).
// Falls back to COUNT(*) for small / freshly-created tables.
func (c *Collection) Count(ctx context.Context) (int64, error) {
	var approx int64
	err := c.pool.QueryRow(ctx,
		`SELECT reltuples::bigint
		 FROM   pg_class c
		 JOIN   pg_namespace n ON n.oid = c.relnamespace
		 WHERE  n.nspname = $1 AND c.relname = $2`,
		c.schema, c.table,
	).Scan(&approx)
	if err != nil || approx < 1000 {
		var exact int64
		if e := c.pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", c.fqt)).Scan(&exact); e != nil {
			return 0, e
		}
		return exact, nil
	}
	return approx, nil
}

// Exists returns true if a drawer with the given ID exists.
func (c *Collection) Exists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := c.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1)", c.fqt), id,
	).Scan(&exists)
	return exists, err
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func scanDrawers(rows pgx.Rows, withDist bool) ([]Drawer, error) {
	var out []Drawer
	for rows.Next() {
		var d Drawer
		var raw []byte
		var err error
		if withDist {
			err = rows.Scan(&d.ID, &d.Document, &raw, &d.Distance)
		} else {
			err = rows.Scan(&d.ID, &d.Document, &raw)
		}
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &d.Metadata); err != nil {
			d.Metadata = map[string]any{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func joinComma(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}
