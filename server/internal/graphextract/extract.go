// Package graphextract turns a filed drawer into knowledge-graph nodes and
// edges to MERGE into the Apache AGE graph. It exists so add_drawer can
// optionally auto-populate the graph (opt-in via config), and so the
// extraction strategy is swappable:
//
//   - Structural: deterministic, no LLM — mirrors the palace structure
//     (Drawer -> Room -> Wing).
//   - LLM: extracts real entities and relations from the content via an
//     OpenAI-compatible chat model.
//
// Both satisfy the Extractor interface, so add_drawer never has to know which
// one is active.
package graphextract

import (
	"context"

	"mempalace/server/internal/storage"
)

// DrawerRef is the input an extractor sees for one filed drawer.
type DrawerRef struct {
	Wing     string
	Room     string
	DrawerID string
	Content  string
}

// Extractor turns a filed drawer into graph nodes and edges to MERGE.
// Returned relations may only reference entities that are also returned
// (the AGE writer MATCHes both endpoints), so implementations must include
// every endpoint they relate.
type Extractor interface {
	Extract(ctx context.Context, d DrawerRef) ([]storage.KGEntity, []storage.KGRelation, error)
}

// Node types (stored as entity_type) and relation types used by the
// structural extractor. The prefixes on node names keep auto-generated
// structural nodes from colliding with user entities or with each other
// (all AGE nodes share a single :Entity label keyed by name), and the
// entity_type values keep them filterable via kg_search_entities.
const (
	TypeWing   = "Wing"
	TypeRoom   = "Room"
	TypeDrawer = "Drawer"

	RelInRoom = "IN_ROOM"
	RelPartOf = "PART_OF"
)

// Structural is the default, deterministic extractor. It needs no LLM and
// never calls out over the network.
type Structural struct{}

// Extract maps the drawer onto its palace location:
//
//	(Drawer:<id>) -[:IN_ROOM]-> (Room:<wing>/<room>) -[:PART_OF]-> (Wing:<wing>)
func (Structural) Extract(_ context.Context, d DrawerRef) ([]storage.KGEntity, []storage.KGRelation, error) {
	wingName := "wing:" + d.Wing
	roomName := "room:" + d.Wing + "/" + d.Room
	drawerName := "drawer:" + d.DrawerID

	ents := []storage.KGEntity{
		{Name: wingName, EntityType: TypeWing, Description: d.Wing},
		{Name: roomName, EntityType: TypeRoom, Description: d.Room},
		{Name: drawerName, EntityType: TypeDrawer, Description: preview(d.Content)},
	}
	rels := []storage.KGRelation{
		{Type: RelInRoom, From: drawerName, To: roomName},
		{Type: RelPartOf, From: roomName, To: wingName},
	}
	return ents, rels, nil
}

// preview returns a short, single-line summary of content for a node's
// description property.
func preview(s string) string {
	const max = 160
	// collapse to first line
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > max {
		s = s[:max]
	}
	return s
}
