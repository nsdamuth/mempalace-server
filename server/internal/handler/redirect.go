package handler

import (
	"context"

	"mempalace/server/internal/consolidate"
	"mempalace/server/internal/storage"
)

// maxRedirectHops caps chain resolution so a corrupted cycle can never spin.
const maxRedirectHops = 32

// redirectInfo is the transparent-follow block attached to a tool response when
// the requested room was resolved through one or more redirects. Clients that
// ignore it still get correct data (the server already followed the chain);
// clients that read it learn the canonical room name and converge over time.
type redirectInfo struct {
	FromWing string   `json:"from_wing"`
	FromRoom string   `json:"from_room"`
	ToWing   string   `json:"to_wing"`
	ToRoom   string   `json:"to_room"`
	Chain    []string `json:"chain"` // ["wing/OldRoom", ..., "wing/CanonicalRoom"]
	Reason   string   `json:"reason,omitempty"`
	Hops     int      `json:"hops"`
}

// rkey is the map key for a (wing, room) pair. The unit-separator can't appear
// in a wing/room name, so "wing␟room" never collides even when names contain
// slashes.
func rkey(wing, room string) string { return wing + "\x1f" + room }

// resolveChain walks the redirect map from (wing, room) to its terminal target.
// It is a pure function: cycle-safe (visited set), bounded (maxRedirectHops),
// and DB-free, so the tricky part is unit-testable without a database.
//
// Returns the terminal (wing, room), the visited chain as "wing/room" strings,
// the reason of the FIRST hop (why the originally-requested room moved), and
// whether any redirect was followed.
func resolveChain(m map[string]storage.Redirect, wing, room string) (tw, tr string, chain []string, firstReason string, redirected bool) {
	tw, tr = wing, room
	chain = []string{wing + "/" + room}
	visited := map[string]bool{rkey(wing, room): true}

	for i := 0; i < maxRedirectHops; i++ {
		r, ok := m[rkey(tw, tr)]
		if !ok {
			break // terminal reached
		}
		if i == 0 {
			firstReason = r.Reason
		}
		if visited[rkey(r.ToWing, r.ToRoom)] {
			break // cycle — stop before re-entering
		}
		tw, tr = r.ToWing, r.ToRoom
		visited[rkey(tw, tr)] = true
		chain = append(chain, tw+"/"+tr)
	}
	redirected = len(chain) > 1
	return tw, tr, chain, firstReason, redirected
}

// redirectMap loads all redirects into a lookup keyed by from-endpoint. Returns
// an empty map (never nil error propagation) when the store is absent so tool
// handlers can call resolveRoom unconditionally.
func (s *Server) redirectMap(ctx context.Context) (map[string]storage.Redirect, error) {
	if s.redirects == nil {
		return map[string]storage.Redirect{}, nil
	}
	list, err := s.redirects.List(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]storage.Redirect, len(list))
	for _, r := range list {
		m[rkey(r.FromWing, r.FromRoom)] = r
	}
	return m, nil
}

// resolveRoom resolves (wing, room) to its terminal target following any
// redirects. It only resolves when BOTH wing and room are given — a room-only
// filter is ambiguous across wings and is left untouched (returns input as-is,
// nil info). The returned *redirectInfo is non-nil only when a redirect was
// actually followed, and is meant to be attached to the response as "redirected".
func (s *Server) resolveRoom(ctx context.Context, wing, room string) (string, string, *redirectInfo, error) {
	if wing == "" || room == "" {
		return wing, room, nil, nil
	}
	m, err := s.redirectMap(ctx)
	if err != nil {
		return wing, room, nil, err
	}
	tw, tr, chain, reason, redirected := resolveChain(m, wing, room)
	if !redirected {
		return wing, room, nil, nil
	}
	return tw, tr, &redirectInfo{
		FromWing: wing, FromRoom: room,
		ToWing: tw, ToRoom: tr,
		Chain: chain, Reason: reason, Hops: len(chain) - 1,
	}, nil
}

// canonicalInfo is attached to add_drawer's response when a would-be duplicate
// room was folded into an existing one at write time.
type canonicalInfo struct {
	FromRoom string `json:"from_room"`
	ToRoom   string `json:"to_room"`
	Wing     string `json:"wing"`
	Reason   string `json:"reason"`
}

// canonicalizeRoom prevents duplicate rooms from arising: if a room with an
// equivalent name (same after normalization — casing/separator only) already
// exists in the wing, the write is folded into that existing room instead of
// creating a new variant. The decision is remembered as a redirect (so later
// reads of the old spelling resolve too), and any drawers already under the old
// spelling are moved. A pair the user explicitly dismissed ("keep separate") is
// honored and left untouched. No-op when the feature is disabled.
func (s *Server) canonicalizeRoom(ctx context.Context, wing, room string) (string, string, *canonicalInfo, error) {
	if s.redirects == nil || wing == "" || room == "" {
		return wing, room, nil, nil
	}
	counts, err := s.col.RoomCountsInWing(ctx, wing)
	if err != nil {
		return wing, room, nil, err
	}

	// Find the best existing room whose name normalizes the same (but differs in
	// spelling); prefer the one with the most drawers as canonical.
	norm := consolidate.Normalize(room)
	canonical, bestCount := "", -1
	for r, c := range counts {
		if r == room {
			continue
		}
		if consolidate.Normalize(r) == norm && c > bestCount {
			canonical, bestCount = r, c
		}
	}
	if canonical == "" {
		return wing, room, nil, nil // no equivalent — nothing to prevent
	}

	// Respect a human's "these are different" decision.
	if s.mergeCandidates != nil {
		if dec, _ := s.mergeCandidates.Decision(ctx, wing, room, wing, canonical); dec == storage.CandidateDismissed {
			return wing, room, nil, nil
		}
	}

	// Record the fold as a redirect + move any pre-existing drawers.
	if _, err := s.applyRedirect(ctx, wing, room, wing, canonical,
		"duplicate room name (normalized match)", true); err != nil {
		return wing, room, nil, err
	}
	return wing, canonical, &canonicalInfo{
		FromRoom: room, ToRoom: canonical, Wing: wing,
		Reason: "folded into existing room with an equivalent name",
	}, nil
}
