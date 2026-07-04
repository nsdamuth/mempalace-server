package graphextract

import (
	"context"
	"testing"

	"mempalace/core/storage"
	"mempalace/server/internal/llm"
)

func TestStructuralExtract(t *testing.T) {
	ents, rels, err := Structural{}.Extract(context.Background(), DrawerRef{
		Wing: "proj-x", Room: "auth", DrawerID: "a1b2c3d4", Content: "OAuth decision\nsecond line",
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	wantNames := map[string]string{
		"wing:proj-x":      TypeWing,
		"room:proj-x/auth": TypeRoom,
		"drawer:a1b2c3d4":  TypeDrawer,
	}
	if len(ents) != len(wantNames) {
		t.Fatalf("got %d entities, want %d: %+v", len(ents), len(wantNames), ents)
	}
	for _, e := range ents {
		wantType, ok := wantNames[e.Name]
		if !ok {
			t.Errorf("unexpected entity %q", e.Name)
			continue
		}
		if e.EntityType != wantType {
			t.Errorf("entity %q: type %q, want %q", e.Name, e.EntityType, wantType)
		}
		// Description is a single-line preview (no newline leaks through).
		if e.Name == "drawer:a1b2c3d4" && e.Description != "OAuth decision" {
			t.Errorf("drawer description = %q, want single first line", e.Description)
		}
	}

	if len(rels) != 2 {
		t.Fatalf("got %d relations, want 2: %+v", len(rels), rels)
	}
	assertRel(t, rels, "drawer:a1b2c3d4", RelInRoom, "room:proj-x/auth")
	assertRel(t, rels, "room:proj-x/auth", RelPartOf, "wing:proj-x")
}

func assertRel(t *testing.T, rels []storage.KGRelation, from, typ, to string) {
	t.Helper()
	for _, r := range rels {
		if r.From == from && r.Type == typ && r.To == to {
			return
		}
	}
	t.Errorf("missing relation %s-[%s]->%s in %+v", from, typ, to, rels)
}

func hasEntity(ents []storage.KGEntity, name string) bool {
	for _, e := range ents {
		if e.Name == name {
			return true
		}
	}
	return false
}

// --- LLM parsing ---------------------------------------------------------

func TestParseExtractionFencedJSON(t *testing.T) {
	raw := "Here you go:\n```json\n" +
		`{"entities":[{"name":"Alice","type":"Person","description":"eng"}],` +
		`"relations":[{"from":"Alice","type":"WORKS_ON","to":"Auth Service"}]}` +
		"\n```"
	ents, rels, err := parseExtraction(raw)
	if err != nil {
		t.Fatalf("parseExtraction: %v", err)
	}
	// Alice is listed; "Auth Service" is only a relation endpoint and must be
	// synthesized so AddRelation can MATCH it.
	if len(ents) != 2 {
		t.Fatalf("got %d entities, want 2 (endpoint auto-added): %+v", len(ents), ents)
	}
	if !hasEntity(ents, "Auth Service") {
		t.Errorf("relation endpoint 'Auth Service' was not added as an entity: %+v", ents)
	}
	if len(rels) != 1 || rels[0].Type != "WORKS_ON" {
		t.Fatalf("relations = %+v", rels)
	}
}

func TestParseExtractionEmpty(t *testing.T) {
	ents, rels, err := parseExtraction(`{"entities":[],"relations":[]}`)
	if err != nil {
		t.Fatalf("parseExtraction: %v", err)
	}
	if len(ents) != 0 || len(rels) != 0 {
		t.Errorf("expected empty result, got ents=%v rels=%v", ents, rels)
	}
}

func TestParseExtractionNoJSON(t *testing.T) {
	if _, _, err := parseExtraction("sorry, I cannot help with that"); err == nil {
		t.Error("expected error for reply with no JSON object")
	}
}

func TestParseExtractionDropsIncompleteRelations(t *testing.T) {
	raw := `{"entities":[{"name":"Bob","type":"Person"}],` +
		`"relations":[{"from":"Bob","type":"","to":"X"},{"from":"","type":"KNOWS","to":"Y"}]}`
	_, rels, err := parseExtraction(raw)
	if err != nil {
		t.Fatalf("parseExtraction: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected incomplete relations dropped, got %+v", rels)
	}
}

// fakeCompleter lets us exercise LLM.Extract without a network call.
type fakeCompleter struct{ reply string }

func (f fakeCompleter) Complete(_ context.Context, _ []llm.Message, _ bool) (string, error) {
	return f.reply, nil
}

func TestLLMExtractEndToEnd(t *testing.T) {
	e := &LLM{client: fakeCompleter{reply: `{"entities":[{"name":"Max","type":"Person"}],"relations":[]}`}}
	ents, _, err := e.Extract(context.Background(), DrawerRef{Content: "Max joined"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(ents) != 1 || ents[0].Name != "Max" {
		t.Fatalf("ents = %+v", ents)
	}
}
