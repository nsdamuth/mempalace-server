package storage

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"mempalace/server/internal/config"
)

// NewPool creates a pgxpool with pgvector types registered on every connection.
func NewPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}

	pcfg.MinConns = cfg.PoolMin
	pcfg.MaxConns = cfg.PoolMax

	// Register pgvector and AGE agtype codecs for every new connection.
	pcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// pgvector — required for vector columns
		if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
			return err
		}
		// AGE: load the shared library for this session.
		// No-op when 'age' is already in shared_preload_libraries.
		// Non-fatal: server still works without KG tools if AGE is absent.
		if _, err := conn.Exec(ctx, "LOAD 'age'"); err != nil {
			log.Printf("pool: LOAD age: %v (AGE not installed — KG tools unavailable)", err)
			return nil
		}
		// Include ag_catalog in search_path so AGE operators (@>, etc.) and
		// the agtype type are resolved without schema prefix.
		if _, err := conn.Exec(ctx, `SET search_path = ag_catalog, "$user", public`); err != nil {
			log.Printf("pool: SET search_path for AGE: %v", err)
		}
		// Register agtype as a text-decoded type so pgx can scan it into string.
		var agtypeOID uint32
		if err := conn.QueryRow(ctx,
			"SELECT oid FROM pg_type WHERE typname = 'agtype'",
		).Scan(&agtypeOID); err == nil {
			conn.TypeMap().RegisterType(&pgtype.Type{
				Name:  "agtype",
				OID:   agtypeOID,
				Codec: pgtype.TextCodec{},
			})
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// ---------------------------------------------------------------------------
// Identifier helpers
// ---------------------------------------------------------------------------

var (
	reUnsafe     = regexp.MustCompile(`[^a-zA-Z0-9]`)
	reMultiUnder = regexp.MustCompile(`_+`)
)

// SafeSchemaName converts an arbitrary tenant ID to a safe PostgreSQL schema
// name of the form mp_{sanitized} (max 63 chars, only [a-z0-9_]).
func SafeSchemaName(tenantID string) string {
	s := reUnsafe.ReplaceAllString(tenantID, "_")
	s = strings.ToLower(s)
	s = reMultiUnder.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "default"
	}
	name := "mp_" + s
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// safeTableName converts a collection name to a valid table identifier.
func safeTableName(name string) string {
	s := reUnsafe.ReplaceAllString(name, "_")
	s = strings.ToLower(s)
	s = reMultiUnder.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "drawers"
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}
