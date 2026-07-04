package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MergeCandidateStore holds "dream" merge proposals: near-duplicate rooms the
// consolidation job suggests collapsing. The job only ever WRITES here; the
// palace is mutated later by a human/LLM applying a candidate through the
// redirect machinery. Rows carry a status so review decisions stick across runs.
type MergeCandidateStore struct {
	pool   *pgxpool.Pool
	schema string
}

// NewMergeCandidateStore returns a store for the given tenant.
func NewMergeCandidateStore(pool *pgxpool.Pool, tenantID string) *MergeCandidateStore {
	return &MergeCandidateStore{pool: pool, schema: SafeSchemaName(tenantID)}
}

// Candidate status values.
const (
	CandidatePending   = "pending"
	CandidateApplied   = "applied"
	CandidateDismissed = "dismissed"
)

// MergeCandidate is one proposed room merge (from → to).
type MergeCandidate struct {
	ID          string  `json:"id"`
	RunID       string  `json:"run_id"`
	FromWing    string  `json:"from_wing"`
	FromRoom    string  `json:"from_room"`
	ToWing      string  `json:"to_wing"`
	ToRoom      string  `json:"to_room"`
	Tier        string  `json:"tier"`
	Score       float64 `json:"score"`
	FromDrawers int     `json:"from_drawers"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
}

// candidateID is a stable, SYMMETRIC hash of the two endpoints: A→B and B→A map
// to the same id. This guarantees at most one candidate row per unordered room
// pair — re-running the dream (even if the canonical direction flips) upserts
// the same row instead of creating a reverse-direction duplicate. The actual
// direction lives in the from_*/to_* columns; a reviewer's decision is preserved.
func candidateID(fromWing, fromRoom, toWing, toRoom string) string {
	a := fromWing + "/" + fromRoom
	b := toWing + "/" + toRoom
	if a > b {
		a, b = b, a
	}
	h := sha256.Sum256([]byte(a + "↔" + b))
	return hex.EncodeToString(h[:])[:16]
}

// ProvisionMergeCandidates creates the room_merge_candidates table (idempotent).
func ProvisionMergeCandidates(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	schema := SafeSchemaName(tenantID)
	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.room_merge_candidates (
				id           TEXT PRIMARY KEY,
				run_id       TEXT NOT NULL,
				from_wing    TEXT NOT NULL,
				from_room    TEXT NOT NULL,
				to_wing      TEXT NOT NULL,
				to_room      TEXT NOT NULL,
				tier         TEXT NOT NULL,
				score        DOUBLE PRECISION NOT NULL DEFAULT 0,
				from_drawers INT NOT NULL DEFAULT 0,
				status       TEXT NOT NULL DEFAULT 'pending',
				created_at   TIMESTAMPTZ DEFAULT now()
			)`, schema),
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("provision room_merge_candidates %s: %w", schema, err)
		}
	}
	return nil
}

// Upsert writes a candidate. On conflict (same unordered room pair) it refreshes
// the latest proposal — including its direction — but PRESERVES an existing
// status, so a dismissed pair stays dismissed even if the next dream run
// proposes it again (in either direction).
func (s *MergeCandidateStore) Upsert(ctx context.Context, c MergeCandidate) error {
	id := candidateID(c.FromWing, c.FromRoom, c.ToWing, c.ToRoom)
	sql := fmt.Sprintf(`
		INSERT INTO %s.room_merge_candidates
			(id, run_id, from_wing, from_room, to_wing, to_room, tier, score, from_drawers, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending', now())
		ON CONFLICT (id) DO UPDATE SET
			run_id       = EXCLUDED.run_id,
			from_wing    = EXCLUDED.from_wing,
			from_room    = EXCLUDED.from_room,
			to_wing      = EXCLUDED.to_wing,
			to_room      = EXCLUDED.to_room,
			tier         = EXCLUDED.tier,
			score        = EXCLUDED.score,
			from_drawers = EXCLUDED.from_drawers`, s.schema)
	_, err := s.pool.Exec(ctx, sql, id, c.RunID, c.FromWing, c.FromRoom, c.ToWing, c.ToRoom,
		c.Tier, c.Score, c.FromDrawers)
	if err != nil {
		return fmt.Errorf("upsert merge candidate: %w", err)
	}
	return nil
}

// List returns candidates filtered by status (empty = all) and wing (empty =
// all), highest score first. limit <= 0 means no limit.
func (s *MergeCandidateStore) List(ctx context.Context, status, wing string, minScore float64, limit int) ([]MergeCandidate, error) {
	q := newQB()
	where := "TRUE"
	if status != "" {
		where += " AND status = " + q.next(status)
	}
	if wing != "" {
		where += " AND (from_wing = " + q.next(wing) + " OR to_wing = " + q.next(wing) + ")"
	}
	if minScore > 0 {
		where += " AND score >= " + q.next(minScore)
	}
	sql := fmt.Sprintf(`
		SELECT id, run_id, from_wing, from_room, to_wing, to_room, tier, score, from_drawers, status,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM %s.room_merge_candidates
		WHERE %s
		ORDER BY score DESC, from_wing, from_room`, s.schema, where)
	if limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, fmt.Errorf("list merge candidates: %w", err)
	}
	defer rows.Close()

	out := []MergeCandidate{}
	for rows.Next() {
		var c MergeCandidate
		if err := rows.Scan(&c.ID, &c.RunID, &c.FromWing, &c.FromRoom, &c.ToWing, &c.ToRoom,
			&c.Tier, &c.Score, &c.FromDrawers, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns a single candidate by id, or nil if not found.
func (s *MergeCandidateStore) Get(ctx context.Context, id string) (*MergeCandidate, error) {
	sql := fmt.Sprintf(`
		SELECT id, run_id, from_wing, from_room, to_wing, to_room, tier, score, from_drawers, status,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM %s.room_merge_candidates WHERE id = $1`, s.schema)
	var c MergeCandidate
	err := s.pool.QueryRow(ctx, sql, id).Scan(&c.ID, &c.RunID, &c.FromWing, &c.FromRoom,
		&c.ToWing, &c.ToRoom, &c.Tier, &c.Score, &c.FromDrawers, &c.Status, &c.CreatedAt)
	if err != nil {
		return nil, nil //nolint:nilerr // not-found is nil,nil by contract
	}
	return &c, nil
}

// Decision returns the review decision recorded for a room pair, or "" if none.
// The candidate id is symmetric, so a single lookup covers both directions.
// Lets the write path honor a human's "keep separate" choice.
func (s *MergeCandidateStore) Decision(ctx context.Context, w1, r1, w2, r2 string) (string, error) {
	var st string
	err := s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT status FROM %s.room_merge_candidates WHERE id = $1`, s.schema),
		candidateID(w1, r1, w2, r2)).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("candidate decision: %w", err)
	}
	return st, nil
}

// MarkApplied flags the candidate matching a from→to merge as applied, if one
// exists (no-op otherwise). Called when a redirect is created so a manually
// performed merge clears its corresponding pending proposal.
func (s *MergeCandidateStore) MarkApplied(ctx context.Context, fromWing, fromRoom, toWing, toRoom string) error {
	id := candidateID(fromWing, fromRoom, toWing, toRoom)
	_, err := s.pool.Exec(ctx,
		fmt.Sprintf(`UPDATE %s.room_merge_candidates SET status = 'applied' WHERE id = $1`, s.schema),
		id)
	if err != nil {
		return fmt.Errorf("mark candidate applied: %w", err)
	}
	return nil
}

// SetStatus updates a candidate's review status. Returns whether a row matched.
func (s *MergeCandidateStore) SetStatus(ctx context.Context, id, status string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		fmt.Sprintf(`UPDATE %s.room_merge_candidates SET status = $1 WHERE id = $2`, s.schema),
		status, id)
	if err != nil {
		return false, fmt.Errorf("set candidate status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
