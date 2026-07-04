package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RedirectStore manages room redirects — directional, temporal forwarding
// pointers left behind when a room is merged/renamed. Unlike tunnels (symmetric,
// topical adjacency), a redirect is a one-way "this room BECAME that room" edge.
// A room forwards to at most one target, enforced by the (from_wing, from_room)
// primary key. Resolution to the terminal target is done in the handler layer
// (cycle-safe walk) so it stays a pure, unit-testable function.
type RedirectStore struct {
	pool   *pgxpool.Pool
	schema string
}

// NewRedirectStore returns a RedirectStore for the given tenant.
func NewRedirectStore(pool *pgxpool.Pool, tenantID string) *RedirectStore {
	return &RedirectStore{pool: pool, schema: SafeSchemaName(tenantID)}
}

// Redirect is a stored one-way room forwarding edge.
type Redirect struct {
	FromWing  string `json:"from_wing"`
	FromRoom  string `json:"from_room"`
	ToWing    string `json:"to_wing"`
	ToRoom    string `json:"to_room"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ProvisionRedirects creates the room_redirects table for a tenant (idempotent).
func ProvisionRedirects(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	schema := SafeSchemaName(tenantID)
	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.room_redirects (
				from_wing  TEXT NOT NULL,
				from_room  TEXT NOT NULL,
				to_wing    TEXT NOT NULL,
				to_room    TEXT NOT NULL,
				reason     TEXT DEFAULT '',
				created_at TIMESTAMPTZ DEFAULT now(),
				PRIMARY KEY (from_wing, from_room)
			)`, schema),
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("provision room_redirects %s: %w", schema, err)
		}
	}
	return nil
}

// Create inserts a redirect, or repoints an existing one (matched by the
// from-endpoint primary key). The caller is responsible for cycle/self checks.
func (s *RedirectStore) Create(ctx context.Context, fromWing, fromRoom, toWing, toRoom, reason string) (*Redirect, error) {
	if fromWing == "" || fromRoom == "" || toWing == "" || toRoom == "" {
		return nil, fmt.Errorf("from_wing, from_room, to_wing, and to_room are required")
	}
	sql := fmt.Sprintf(`
		INSERT INTO %s.room_redirects (from_wing, from_room, to_wing, to_room, reason, created_at)
		VALUES ($1,$2,$3,$4,$5, now())
		ON CONFLICT (from_wing, from_room) DO UPDATE SET
			to_wing = EXCLUDED.to_wing,
			to_room = EXCLUDED.to_room,
			reason  = EXCLUDED.reason
		RETURNING from_wing, from_room, to_wing, to_room, reason,
		          to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`, s.schema)

	row := s.pool.QueryRow(ctx, sql, fromWing, fromRoom, toWing, toRoom, reason)
	var r Redirect
	if err := row.Scan(&r.FromWing, &r.FromRoom, &r.ToWing, &r.ToRoom, &r.Reason, &r.CreatedAt); err != nil {
		return nil, fmt.Errorf("create redirect: %w", err)
	}
	return &r, nil
}

// Delete removes a redirect by its from-endpoint. Returns whether a row was removed.
func (s *RedirectStore) Delete(ctx context.Context, fromWing, fromRoom string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		fmt.Sprintf(`DELETE FROM %s.room_redirects WHERE from_wing = $1 AND from_room = $2`, s.schema),
		fromWing, fromRoom)
	if err != nil {
		return false, fmt.Errorf("delete redirect: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// List returns all redirects ordered by creation time.
func (s *RedirectStore) List(ctx context.Context) ([]Redirect, error) {
	sql := fmt.Sprintf(`
		SELECT from_wing, from_room, to_wing, to_room, reason,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM %s.room_redirects
		ORDER BY created_at`, s.schema)

	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list redirects: %w", err)
	}
	defer rows.Close()

	out := []Redirect{}
	for rows.Next() {
		var r Redirect
		if err := rows.Scan(&r.FromWing, &r.FromRoom, &r.ToWing, &r.ToRoom, &r.Reason, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
