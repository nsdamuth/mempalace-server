// Package consolidate holds the pure "dream" clustering logic: given the set of
// rooms (and optional per-room embeddings), it proposes near-duplicate merges.
// It contains no I/O — no DB, no embedding calls — so the tricky part is fully
// unit-testable. The dreamjob microservice supplies the data and persists the
// resulting candidates; nothing here mutates the palace.
package consolidate

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// RoomInfo is one room in the taxonomy.
type RoomInfo struct {
	Wing    string
	Room    string
	Drawers int
}

// Candidate is a proposed one-way merge (from → to). It maps 1:1 onto a
// redirect_room call, but is only ever a suggestion for a human/LLM to review.
type Candidate struct {
	FromWing    string  `json:"from_wing"`
	FromRoom    string  `json:"from_room"`
	ToWing      string  `json:"to_wing"`
	ToRoom      string  `json:"to_room"`
	Tier        string  `json:"tier"`  // "exact" | "semantic"
	Score       float64 `json:"score"` // 1.0 for exact; cosine for semantic
	FromDrawers int     `json:"from_drawers"`
}

const (
	TierExact    = "exact"
	TierSemantic = "semantic"
)

var sepRe = regexp.MustCompile(`[\s_\-]+`)

// Normalize folds a room name for the exact tier: lowercase, and collapse any
// run of whitespace / underscore / hyphen to a single space. Deliberately
// conservative — it must not merge distinct topics, so no stemming/plural
// stripping. "Auth", "auth" and "auth " collapse; "auth-flow" stays distinct
// from "Auth" (that difference is left to the semantic tier).
func Normalize(room string) string {
	s := strings.ToLower(strings.TrimSpace(room))
	s = sepRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// Cluster proposes merge candidates. Rooms are clustered only within their own
// wing (same room name in different wings is legitimately distinct). vec returns
// the embedding for a room, or nil when unavailable — a nil embedding simply
// skips the semantic tier for that room. threshold is the cosine cutoff for the
// semantic tier. The result is deterministic for a given input.
func Cluster(rooms []RoomInfo, vec func(RoomInfo) []float32, threshold float64) []Candidate {
	byWing := map[string][]RoomInfo{}
	for _, r := range rooms {
		byWing[r.Wing] = append(byWing[r.Wing], r)
	}
	wings := make([]string, 0, len(byWing))
	for w := range byWing {
		wings = append(wings, w)
	}
	sort.Strings(wings)

	var out []Candidate
	for _, w := range wings {
		out = append(out, clusterWing(byWing[w], vec, threshold)...)
	}
	return out
}

func clusterWing(rooms []RoomInfo, vec func(RoomInfo) []float32, threshold float64) []Candidate {
	// --- exact tier: group by normalized name ---
	groups := map[string][]RoomInfo{}
	for _, r := range rooms {
		n := Normalize(r.Room)
		groups[n] = append(groups[n], r)
	}
	norms := make([]string, 0, len(groups))
	for n := range groups {
		norms = append(norms, n)
	}
	sort.Strings(norms)

	var candidates []Candidate
	reps := make([]RoomInfo, 0, len(groups)) // one representative per exact group
	for _, n := range norms {
		g := groups[n]
		canon := pickCanonical(g)
		reps = append(reps, canon)
		for _, r := range g {
			if r.Room == canon.Room {
				continue
			}
			candidates = append(candidates, Candidate{
				FromWing: r.Wing, FromRoom: r.Room,
				ToWing: canon.Wing, ToRoom: canon.Room,
				Tier: TierExact, Score: 1.0, FromDrawers: r.Drawers,
			})
		}
	}

	// --- semantic tier: greedy single-link over the representatives ---
	// Process high-drawer rooms first so smaller rooms redirect into bigger ones.
	sort.SliceStable(reps, func(i, j int) bool {
		if reps[i].Drawers != reps[j].Drawers {
			return reps[i].Drawers > reps[j].Drawers
		}
		return reps[i].Room < reps[j].Room
	})

	var canonicals []RoomInfo
	for _, r := range reps {
		rv := vec(r)
		if rv == nil {
			canonicals = append(canonicals, r)
			continue
		}
		bestScore := -1.0
		var bestC RoomInfo
		for _, c := range canonicals {
			cv := vec(c)
			if cv == nil {
				continue
			}
			if s := cosine(rv, cv); s >= threshold && s > bestScore {
				bestScore, bestC = s, c
			}
		}
		if bestScore >= threshold {
			candidates = append(candidates, Candidate{
				FromWing: r.Wing, FromRoom: r.Room,
				ToWing: bestC.Wing, ToRoom: bestC.Room,
				Tier: TierSemantic, Score: bestScore, FromDrawers: r.Drawers,
			})
		} else {
			canonicals = append(canonicals, r)
		}
	}
	return candidates
}

// pickCanonical chooses the group's merge target: most drawers wins, ties broken
// by lexicographically smallest name (deterministic).
func pickCanonical(g []RoomInfo) RoomInfo {
	best := g[0]
	for _, r := range g[1:] {
		if r.Drawers > best.Drawers || (r.Drawers == best.Drawers && r.Room < best.Room) {
			best = r
		}
	}
	return best
}

// cosine returns the cosine similarity of two equal-length vectors, or 0 if
// either is empty / zero-magnitude / mismatched.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
