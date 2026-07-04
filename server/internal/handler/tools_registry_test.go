package handler

import (
	"testing"

	"mempalace/server/internal/config"
)

// upstreamTools is the full MCP tool surface of MemPalace (29 tools). The Go
// server must expose all of them by name. Additional Go-only tools (e.g. the
// AGE-backed entity graph) are allowed on top.
var upstreamTools = []string{
	// core palace
	"mempalace_status", "mempalace_list_wings", "mempalace_list_rooms",
	"mempalace_get_taxonomy", "mempalace_search", "mempalace_check_duplicate",
	"mempalace_add_drawer", "mempalace_delete_drawer", "mempalace_get_drawer",
	"mempalace_list_drawers", "mempalace_update_drawer", "mempalace_diary_write",
	"mempalace_diary_read", "mempalace_reconnect",
	// temporal knowledge graph
	"mempalace_kg_add", "mempalace_kg_query", "mempalace_kg_invalidate",
	"mempalace_kg_timeline", "mempalace_kg_stats",
	// palace graph + tunnels
	"mempalace_traverse", "mempalace_find_tunnels", "mempalace_graph_stats",
	"mempalace_create_tunnel", "mempalace_list_tunnels", "mempalace_delete_tunnel",
	"mempalace_follow_tunnels",
	// meta
	"mempalace_get_aaak_spec", "mempalace_hook_settings", "mempalace_memories_filed_away",
}

// newRegistry builds a Server for registry inspection only. Tool registration
// does not touch the storage/embed dependencies, so nil is safe here.
func newRegistry() *Server {
	return New(nil, nil, nil, nil, nil, nil, nil, nil, config.Config{})
}

func TestUpstreamToolsRegistered(t *testing.T) {
	s := newRegistry()
	for _, name := range upstreamTools {
		if _, ok := s.tools[name]; !ok {
			t.Errorf("missing upstream tool: %s", name)
		}
		if _, ok := s.router[name]; !ok {
			t.Errorf("upstream tool %s has no router entry", name)
		}
	}
}

func TestEveryToolHasRouterAndHandler(t *testing.T) {
	s := newRegistry()
	for name := range s.tools {
		if _, ok := s.router[name]; !ok {
			t.Errorf("tool %s registered without a router handler", name)
		}
	}
	if len(s.tools) != len(s.router) {
		t.Errorf("tools/router length mismatch: %d tools vs %d router entries", len(s.tools), len(s.router))
	}
}

func TestRequiredFieldsAreDeclaredProperties(t *testing.T) {
	s := newRegistry()
	for name, def := range s.tools {
		for _, req := range def.InputSchema.Required {
			if _, ok := def.InputSchema.Properties[req]; !ok {
				t.Errorf("tool %s requires %q but does not declare it as a property", name, req)
			}
		}
	}
}
