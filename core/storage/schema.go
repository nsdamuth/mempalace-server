package storage

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

const DrawersTable = "mempalace_drawers"

// Provision creates the tenant schema, drawers table, HNSW vector index, and
// GIN metadata index.  All statements are idempotent (IF NOT EXISTS).
//
// dim must match the embedding model's output dimension (e.g. 768 for
// embeddinggemma / nomic-embed-text, 384 for all-MiniLM-L6-v2).
func Provision(ctx context.Context, pool *pgxpool.Pool, tenantID string, dim int) error {
	schema := SafeSchemaName(tenantID)
	table := safeTableName(DrawersTable)
	fqt := schema + "." + table

	idxPfx := schema + "_" + table
	if len(idxPfx) > 50 {
		idxPfx = idxPfx[:50]
	}

	// pgvector extension may already exist; ignore permission errors from
	// CREATE EXTENSION (superuser-only) — the extension must be pre-installed.
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		log.Printf("storage: CREATE EXTENSION vector: %v (ignored — must be pre-installed)", err)
	}

	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				id        TEXT  NOT NULL PRIMARY KEY,
				document  TEXT  NOT NULL DEFAULT '',
				embedding vector(%d),
				metadata  JSONB NOT NULL DEFAULT '{}'
			)`, fqt, dim),
		// HNSW index — m=16, ef_construction=64 are solid RAG defaults
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s_vec
			ON %s USING hnsw (embedding vector_cosine_ops)
			WITH (m = 16, ef_construction = 64)`, idxPfx, fqt),
		// GIN index for fast JSONB key/value filtering
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s_meta
			ON %s USING gin (metadata)`, idxPfx, fqt),
		// Generated tsvector column for full-text search (case-insensitive, language-agnostic)
		fmt.Sprintf(`
			ALTER TABLE %s ADD COLUMN IF NOT EXISTS ts_doc tsvector
			GENERATED ALWAYS AS (to_tsvector('simple', document)) STORED`, fqt),
		// GIN index for fast full-text search
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s_fts
			ON %s USING gin (ts_doc)`, idxPfx, fqt),
	}

	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			preview := stmt
			if len(preview) > 60 {
				preview = preview[:60] + "…"
			}
			return fmt.Errorf("provision %s: %w (stmt: %s)", schema, err, preview)
		}
	}

	log.Printf("storage: schema %s ready (dim=%d)", schema, dim)
	return nil
}
