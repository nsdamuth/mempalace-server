package handler

import (
	"context"
	"fmt"
)

// registerRedirectTools wires the room-redirect surface: directional forwarding
// pointers left when a room is merged/renamed. These complement the write-time
// soft-warn — redirects consolidate what already fragmented, non-destructively.
func (s *Server) registerRedirectTools() {
	s.add("mempalace_redirect_room",
		"Merge/rename a room: forward an old room to a new one. By default also moves the old room's drawers to the target (pure metadata — no re-embedding). Old references keep resolving via the redirect; the old name stays visible, flagged as redirected.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"from_wing":    {Type: "string", Description: "Wing of the old room"},
				"from_room":    {Type: "string", Description: "Old room name to forward"},
				"to_wing":      {Type: "string", Description: "Wing of the target room"},
				"to_room":      {Type: "string", Description: "Canonical target room name"},
				"reason":       {Type: "string", Description: "Why the room was merged/renamed (optional)"},
				"move_drawers": {Type: "boolean", Description: "Move the old room's drawers to the target (default: true)", Default: true},
			},
			Required: []string{"from_wing", "from_room", "to_wing", "to_room"},
		},
		s.toolRedirectRoom)

	s.add("mempalace_list_redirects",
		"List all room redirects (old room → canonical room) with reasons.",
		inputSchema{Type: "object"},
		s.toolListRedirects)

	s.add("mempalace_resolve_room",
		"Resolve a room to its canonical target, following any chain of redirects. Returns the terminal room and the full chain.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"wing": {Type: "string", Description: "Wing of the room"},
				"room": {Type: "string", Description: "Room to resolve"},
			},
			Required: []string{"wing", "room"},
		},
		s.toolResolveRoom)

	s.add("mempalace_delete_redirect",
		"Remove a room redirect by its old (from) endpoint. Does not move drawers back.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"from_wing": {Type: "string", Description: "Wing of the redirected room"},
				"from_room": {Type: "string", Description: "Old room name whose redirect to remove"},
			},
			Required: []string{"from_wing", "from_room"},
		},
		s.toolDeleteRedirect)
}

func (s *Server) toolRedirectRoom(args map[string]any) (any, error) {
	if s.redirects == nil {
		return nil, fmt.Errorf("room redirects are unavailable")
	}
	ctx := reqCtx()

	fromWing, _ := args["from_wing"].(string)
	fromRoom, _ := args["from_room"].(string)
	toWing, _ := args["to_wing"].(string)
	toRoom, _ := args["to_room"].(string)
	reason, _ := args["reason"].(string)
	moveDrawers := true // default
	if p := boolArgPtr(args, "move_drawers"); p != nil {
		moveDrawers = *p
	}

	return s.applyRedirect(ctx, fromWing, fromRoom, toWing, toRoom, reason, moveDrawers)
}

// applyRedirect is the shared merge primitive behind both redirect_room and
// apply_merge_candidate: cycle-guarded creation of a from→to redirect, plus an
// optional move of the source room's drawers to the canonical terminal target.
func (s *Server) applyRedirect(ctx context.Context, fromWing, fromRoom, toWing, toRoom, reason string, moveDrawers bool) (map[string]any, error) {
	if fromWing == "" || fromRoom == "" || toWing == "" || toRoom == "" {
		return nil, fmt.Errorf("from_wing, from_room, to_wing, and to_room are required")
	}
	if fromWing == toWing && fromRoom == toRoom {
		return nil, fmt.Errorf("a room cannot redirect to itself")
	}

	// Cycle guard: if the target already resolves back to the source, adding
	// from→to would close a loop. Reject before writing.
	m, err := s.redirectMap(ctx)
	if err != nil {
		return nil, err
	}
	termWing, termRoom, _, _, _ := resolveChain(m, toWing, toRoom)
	if termWing == fromWing && termRoom == fromRoom {
		return nil, fmt.Errorf("redirect would create a cycle: %s/%s already resolves back to %s/%s",
			toWing, toRoom, fromWing, fromRoom)
	}

	redirect, err := s.redirects.Create(ctx, fromWing, fromRoom, toWing, toRoom, reason)
	if err != nil {
		return nil, err
	}

	resp := map[string]any{
		"success":  true,
		"redirect": redirect,
	}

	if moveDrawers {
		// Land drawers at the canonical terminal room in one shot.
		moved, err := s.col.MoveRoom(ctx, fromWing, fromRoom, termWing, termRoom)
		if err != nil {
			return nil, err
		}
		resp["drawers_moved"] = moved
		resp["moved_to"] = map[string]string{"wing": termWing, "room": termRoom}
	}

	return resp, nil
}

func (s *Server) toolListRedirects(_ map[string]any) (any, error) {
	if s.redirects == nil {
		return map[string]any{"redirects": []any{}, "count": 0}, nil
	}
	list, err := s.redirects.List(reqCtx())
	if err != nil {
		return nil, err
	}
	return map[string]any{"redirects": list, "count": len(list)}, nil
}

func (s *Server) toolResolveRoom(args map[string]any) (any, error) {
	wing, _ := args["wing"].(string)
	room, _ := args["room"].(string)
	if wing == "" || room == "" {
		return nil, fmt.Errorf("wing and room are required")
	}

	m, err := s.redirectMap(reqCtx())
	if err != nil {
		return nil, err
	}
	tw, tr, chain, reason, redirected := resolveChain(m, wing, room)
	return map[string]any{
		"wing":       tw,
		"room":       tr,
		"redirected": redirected,
		"chain":      chain,
		"reason":     reason,
	}, nil
}

func (s *Server) toolDeleteRedirect(args map[string]any) (any, error) {
	if s.redirects == nil {
		return nil, fmt.Errorf("room redirects are unavailable")
	}
	fromWing, _ := args["from_wing"].(string)
	fromRoom, _ := args["from_room"].(string)
	if fromWing == "" || fromRoom == "" {
		return nil, fmt.Errorf("from_wing and from_room are required")
	}
	found, err := s.redirects.Delete(reqCtx(), fromWing, fromRoom)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": found, "from_wing": fromWing, "from_room": fromRoom}, nil
}
