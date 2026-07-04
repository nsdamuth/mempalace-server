package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TunnelStore manages explicit cross-wing tunnels — agent-created links between
// two palace locations that share a conceptual connection. Tunnels are
// undirected: create_tunnel(A, B) and create_tunnel(B, A) collapse to one
// canonical record (mirrors upstream palace_graph.py explicit tunnels).
type TunnelStore struct {
	pool   *pgxpool.Pool
	schema string
}

// NewTunnelStore returns a TunnelStore for the given tenant.
func NewTunnelStore(pool *pgxpool.Pool, tenantID string) *TunnelStore {
	return &TunnelStore{pool: pool, schema: SafeSchemaName(tenantID)}
}

// TunnelEndpoint is one side of a tunnel.
type TunnelEndpoint struct {
	Wing     string `json:"wing"`
	Room     string `json:"room"`
	DrawerID string `json:"drawer_id,omitempty"`
}

// Tunnel is a stored explicit cross-wing link.
type Tunnel struct {
	ID        string         `json:"id"`
	Source    TunnelEndpoint `json:"source"`
	Target    TunnelEndpoint `json:"target"`
	Label     string         `json:"label"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at,omitempty"`
}

// TunnelConnection is a tunnel as seen from one room (used by Follow).
type TunnelConnection struct {
	Direction     string `json:"direction"` // outgoing | incoming
	ConnectedWing string `json:"connected_wing"`
	ConnectedRoom string `json:"connected_room"`
	Label         string `json:"label"`
	DrawerID      string `json:"drawer_id,omitempty"`
	TunnelID      string `json:"tunnel_id"`
	DrawerPreview string `json:"drawer_preview,omitempty"`
}

// ProvisionTunnels creates the tunnels table for a tenant (idempotent).
func ProvisionTunnels(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	schema := SafeSchemaName(tenantID)
	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.tunnels (
				id               TEXT PRIMARY KEY,
				source_wing      TEXT NOT NULL,
				source_room      TEXT NOT NULL,
				target_wing      TEXT NOT NULL,
				target_room      TEXT NOT NULL,
				label            TEXT DEFAULT '',
				source_drawer_id TEXT,
				target_drawer_id TEXT,
				created_at       TIMESTAMPTZ DEFAULT now(),
				updated_at       TIMESTAMPTZ
			)`, schema),
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("provision tunnels %s: %w", schema, err)
		}
	}
	return nil
}

// canonicalTunnelID computes a symmetric id from the two endpoints.
func canonicalTunnelID(srcWing, srcRoom, tgtWing, tgtRoom string) string {
	a := srcWing + "/" + srcRoom
	b := tgtWing + "/" + tgtRoom
	pair := []string{a, b}
	sort.Strings(pair)
	h := sha256.Sum256([]byte(pair[0] + "↔" + pair[1]))
	return hex.EncodeToString(h[:])[:16]
}

// Create inserts a tunnel, or updates the label / drawer ids of an existing one
// (matched by canonical id). The original source/target orientation is kept.
func (s *TunnelStore) Create(ctx context.Context, srcWing, srcRoom, tgtWing, tgtRoom, label, srcDrawer, tgtDrawer string) (*Tunnel, error) {
	if srcWing == "" || srcRoom == "" || tgtWing == "" || tgtRoom == "" {
		return nil, fmt.Errorf("source_wing, source_room, target_wing, and target_room are required")
	}
	id := canonicalTunnelID(srcWing, srcRoom, tgtWing, tgtRoom)

	sql := fmt.Sprintf(`
		INSERT INTO %s.tunnels
			(id, source_wing, source_room, target_wing, target_room, label, source_drawer_id, target_drawer_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (id) DO UPDATE SET
			label            = EXCLUDED.label,
			source_drawer_id = COALESCE(EXCLUDED.source_drawer_id, tunnels.source_drawer_id),
			target_drawer_id = COALESCE(EXCLUDED.target_drawer_id, tunnels.target_drawer_id),
			updated_at       = now()
		RETURNING id, source_wing, source_room, target_wing, target_room, label,
		          source_drawer_id, target_drawer_id,
		          to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		          to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`,
		s.schema)

	row := s.pool.QueryRow(ctx, sql, id, srcWing, srcRoom, tgtWing, tgtRoom, label,
		nullIfEmpty(srcDrawer), nullIfEmpty(tgtDrawer))
	return scanTunnel(row)
}

// List returns all tunnels, optionally filtered to a wing (source or target).
func (s *TunnelStore) List(ctx context.Context, wing string) ([]Tunnel, error) {
	sql := fmt.Sprintf(`
		SELECT id, source_wing, source_room, target_wing, target_room, label,
		       source_drawer_id, target_drawer_id,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM %s.tunnels`, s.schema)
	var args []any
	if wing != "" {
		sql += ` WHERE source_wing = $1 OR target_wing = $1`
		args = append(args, wing)
	}
	sql += ` ORDER BY created_at`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list tunnels: %w", err)
	}
	defer rows.Close()

	out := []Tunnel{}
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Delete removes a tunnel by id. Returns whether a row was deleted.
func (s *TunnelStore) Delete(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.tunnels WHERE id = $1`, s.schema), id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Follow returns the tunnels touching a given room as directional connections.
func (s *TunnelStore) Follow(ctx context.Context, wing, room string) ([]TunnelConnection, error) {
	sql := fmt.Sprintf(`
		SELECT id, source_wing, source_room, target_wing, target_room, label,
		       source_drawer_id, target_drawer_id
		FROM %s.tunnels
		WHERE (source_wing = $1 AND source_room = $2) OR (target_wing = $1 AND target_room = $2)`, s.schema)

	rows, err := s.pool.Query(ctx, sql, wing, room)
	if err != nil {
		return nil, fmt.Errorf("follow tunnels: %w", err)
	}
	defer rows.Close()

	out := []TunnelConnection{}
	for rows.Next() {
		var id, sw, sr, tw, tr, label string
		var sd, td *string
		if err := rows.Scan(&id, &sw, &sr, &tw, &tr, &label, &sd, &td); err != nil {
			return nil, err
		}
		if sw == wing && sr == room {
			out = append(out, TunnelConnection{
				Direction: "outgoing", ConnectedWing: tw, ConnectedRoom: tr,
				Label: label, DrawerID: deref(td), TunnelID: id,
			})
		} else {
			out = append(out, TunnelConnection{
				Direction: "incoming", ConnectedWing: sw, ConnectedRoom: sr,
				Label: label, DrawerID: deref(sd), TunnelID: id,
			})
		}
	}
	return out, rows.Err()
}

// scanTunnel scans a row in the canonical column order into a Tunnel.
func scanTunnel(row interface{ Scan(...any) error }) (*Tunnel, error) {
	var t Tunnel
	var sd, td, updated *string
	if err := row.Scan(
		&t.ID, &t.Source.Wing, &t.Source.Room, &t.Target.Wing, &t.Target.Room,
		&t.Label, &sd, &td, &t.CreatedAt, &updated,
	); err != nil {
		return nil, err
	}
	t.Source.DrawerID = deref(sd)
	t.Target.DrawerID = deref(td)
	t.UpdatedAt = deref(updated)
	return &t, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
