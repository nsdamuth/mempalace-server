package consolidate

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Auth":       "auth",
		"auth":       "auth",
		"  auth  ":   "auth",
		"auth-flow":  "auth flow",
		"auth_flow":  "auth flow",
		"auth  flow": "auth flow",
		"Auth Flow":  "auth flow",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// no embeddings → only the exact tier fires.
func noVec(RoomInfo) []float32 { return nil }

func TestCluster_ExactTier_CanonicalIsBiggest(t *testing.T) {
	rooms := []RoomInfo{
		{Wing: "backend", Room: "Auth", Drawers: 3},
		{Wing: "backend", Room: "auth", Drawers: 10}, // biggest → canonical
		{Wing: "backend", Room: "AUTH", Drawers: 1},
	}
	got := Cluster(rooms, noVec, 0.9)
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d: %+v", len(got), got)
	}
	for _, c := range got {
		if c.ToRoom != "auth" {
			t.Errorf("canonical should be 'auth' (10 drawers), got %q", c.ToRoom)
		}
		if c.Tier != TierExact || c.Score != 1.0 {
			t.Errorf("want exact tier score 1.0, got %s %v", c.Tier, c.Score)
		}
	}
}

func TestCluster_WithinWingOnly(t *testing.T) {
	// Same room name in two wings must NOT be proposed for merge.
	rooms := []RoomInfo{
		{Wing: "backend", Room: "Auth", Drawers: 5},
		{Wing: "frontend", Room: "Auth", Drawers: 5},
	}
	if got := Cluster(rooms, noVec, 0.9); len(got) != 0 {
		t.Errorf("cross-wing merge proposed: %+v", got)
	}
}

func TestCluster_SemanticTier(t *testing.T) {
	// Two near-identical vectors (Auth ~ Authentication) and one orthogonal.
	vecs := map[string][]float32{
		"backend/Authentication": {1, 0, 0},
		"backend/Auth":           {0.99, 0.01, 0},
		"backend/Billing":        {0, 0, 1},
	}
	vec := func(r RoomInfo) []float32 { return vecs[r.Wing+"/"+r.Room] }
	rooms := []RoomInfo{
		{Wing: "backend", Room: "Authentication", Drawers: 20},
		{Wing: "backend", Room: "Auth", Drawers: 4},
		{Wing: "backend", Room: "Billing", Drawers: 8},
	}
	got := Cluster(rooms, vec, 0.9)
	if len(got) != 1 {
		t.Fatalf("want 1 semantic candidate, got %d: %+v", len(got), got)
	}
	c := got[0]
	if c.FromRoom != "Auth" || c.ToRoom != "Authentication" || c.Tier != TierSemantic {
		t.Errorf("want Auth→Authentication (semantic), got %s→%s (%s)", c.FromRoom, c.ToRoom, c.Tier)
	}
	if c.Score < 0.9 {
		t.Errorf("score below threshold surfaced: %v", c.Score)
	}
}

func TestCluster_SemanticBelowThreshold_NoMerge(t *testing.T) {
	vecs := map[string][]float32{
		"w/A": {1, 0},
		"w/B": {0, 1}, // orthogonal → cosine 0
	}
	vec := func(r RoomInfo) []float32 { return vecs[r.Wing+"/"+r.Room] }
	rooms := []RoomInfo{{Wing: "w", Room: "A", Drawers: 2}, {Wing: "w", Room: "B", Drawers: 2}}
	if got := Cluster(rooms, vec, 0.85); len(got) != 0 {
		t.Errorf("dissimilar rooms merged: %+v", got)
	}
}

func TestCluster_Deterministic(t *testing.T) {
	rooms := []RoomInfo{
		{Wing: "w", Room: "Foo", Drawers: 2},
		{Wing: "w", Room: "foo", Drawers: 2},
		{Wing: "w", Room: "FOO", Drawers: 2},
	}
	first := Cluster(rooms, noVec, 0.9)
	for i := 0; i < 5; i++ {
		again := Cluster(rooms, noVec, 0.9)
		if len(again) != len(first) {
			t.Fatalf("non-deterministic count: %d vs %d", len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("non-deterministic candidate at %d: %+v vs %+v", j, again[j], first[j])
			}
		}
	}
}
