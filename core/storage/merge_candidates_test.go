package storage

import "testing"

// The candidate id must be symmetric so A→B and B→A never create two rows for
// the same room pair, while distinct pairs stay distinct.
func TestCandidateID_Symmetric(t *testing.T) {
	ab := candidateID("backend", "Auth", "backend", "Authentication")
	ba := candidateID("backend", "Authentication", "backend", "Auth")
	if ab != ba {
		t.Fatalf("candidateID not symmetric: %s vs %s", ab, ba)
	}

	// Distinct pairs must not collide.
	other := candidateID("backend", "Auth", "backend", "Billing")
	if other == ab {
		t.Errorf("distinct room pairs produced the same id: %s", other)
	}

	// Same room name in different wings is a different pair.
	crossWing := candidateID("frontend", "Auth", "backend", "Authentication")
	if crossWing == ab {
		t.Errorf("cross-wing pair collided with same-wing pair")
	}
}
