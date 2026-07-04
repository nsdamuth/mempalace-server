package handler

import (
	"fmt"
	"sort"
	"strings"

	"mempalace/core/storage"
)

// registerGraphTools wires the palace-graph navigation tools: passive room
// traversal / tunnel discovery (derived from drawer metadata) and explicit
// agent-created tunnels (stored in the tunnels table).
func (s *Server) registerGraphTools() {
	s.add("mempalace_traverse",
		"Walk the palace graph from a room. Shows connected ideas across wings — the tunnels. Start at a room and discover which other rooms share its wings.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"start_room": {Type: "string", Description: "Room to start from (e.g. 'chromadb-setup')"},
				"max_hops":   {Type: "integer", Description: "How many connections to follow (default: 2)", Default: 2, Minimum: intPtr(1), Maximum: intPtr(10)},
			},
			Required: []string{"start_room"},
		},
		s.toolTraverse)

	s.add("mempalace_find_tunnels",
		"Find rooms that bridge two wings — the hallways connecting different domains. Omit wings to list all tunnel rooms.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"wing_a": {Type: "string", Description: "First wing (optional)"},
				"wing_b": {Type: "string", Description: "Second wing (optional)"},
			},
		},
		s.toolFindTunnels)

	s.add("mempalace_graph_stats",
		"Palace graph overview: total rooms, tunnel connections, rooms per wing, top tunnels.",
		inputSchema{Type: "object"},
		s.toolGraphStats)

	s.add("mempalace_create_tunnel",
		"Create a cross-wing tunnel linking two palace locations. Use when content in one project relates to another.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"source_wing":      {Type: "string", Description: "Wing of the source"},
				"source_room":      {Type: "string", Description: "Room in the source wing"},
				"target_wing":      {Type: "string", Description: "Wing of the target"},
				"target_room":      {Type: "string", Description: "Room in the target wing"},
				"label":            {Type: "string", Description: "Description of the connection"},
				"source_drawer_id": {Type: "string", Description: "Optional specific drawer ID"},
				"target_drawer_id": {Type: "string", Description: "Optional specific drawer ID"},
			},
			Required: []string{"source_wing", "source_room", "target_wing", "target_room"},
		},
		s.toolCreateTunnel)

	s.add("mempalace_list_tunnels",
		"List all explicit cross-wing tunnels. Optionally filter by wing.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"wing": {Type: "string", Description: "Filter tunnels by wing (source or target)"},
			},
		},
		s.toolListTunnels)

	s.add("mempalace_delete_tunnel",
		"Delete an explicit tunnel by its ID.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"tunnel_id": {Type: "string", Description: "Tunnel ID to delete"},
			},
			Required: []string{"tunnel_id"},
		},
		s.toolDeleteTunnel)

	s.add("mempalace_follow_tunnels",
		"Follow tunnels from a room to see what it connects to in other wings. Returns connected rooms with drawer previews.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"wing": {Type: "string", Description: "Wing to start from"},
				"room": {Type: "string", Description: "Room to follow tunnels from"},
			},
			Required: []string{"wing", "room"},
		},
		s.toolFollowTunnels)
}

// ---------------------------------------------------------------------------
// Passive palace graph (derived from drawer metadata)
// ---------------------------------------------------------------------------

func (s *Server) toolTraverse(args map[string]any) (any, error) {
	startRoom, _ := args["start_room"].(string)
	if startRoom == "" {
		return nil, fmt.Errorf("start_room is required")
	}
	maxHops := intArg(args, "max_hops", 2)
	if maxHops < 1 {
		maxHops = 1
	}
	if maxHops > 10 {
		maxHops = 10
	}

	graph, err := s.col.BuildRoomGraph(reqCtx())
	if err != nil {
		return nil, err
	}

	start, ok := graph[startRoom]
	if !ok {
		return map[string]any{
			"error":       fmt.Sprintf("Room '%s' not found", startRoom),
			"suggestions": fuzzyRooms(startRoom, graph),
		}, nil
	}

	type result struct {
		Room         string   `json:"room"`
		Wings        []string `json:"wings"`
		Halls        []string `json:"halls"`
		Count        int      `json:"count"`
		Hop          int      `json:"hop"`
		ConnectedVia []string `json:"connected_via,omitempty"`
	}

	visited := map[string]bool{startRoom: true}
	results := []result{{Room: startRoom, Wings: start.Wings, Halls: start.Halls, Count: start.Count, Hop: 0}}

	type frontierItem struct {
		room  string
		depth int
	}
	frontier := []frontierItem{{startRoom, 0}}

	// Stable room order so traversal is deterministic across runs.
	roomNames := make([]string, 0, len(graph))
	for r := range graph {
		roomNames = append(roomNames, r)
	}
	sort.Strings(roomNames)

	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if cur.depth >= maxHops {
			continue
		}
		curWings := sliceToSet(graph[cur.room].Wings)

		for _, room := range roomNames {
			if visited[room] {
				continue
			}
			shared := intersect(curWings, graph[room].Wings)
			if len(shared) == 0 {
				continue
			}
			visited[room] = true
			results = append(results, result{
				Room: room, Wings: graph[room].Wings, Halls: graph[room].Halls,
				Count: graph[room].Count, Hop: cur.depth + 1, ConnectedVia: shared,
			})
			if cur.depth+1 < maxHops {
				frontier = append(frontier, frontierItem{room, cur.depth + 1})
			}
		}
	}

	// Sort by hop distance, then by descending drawer count.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Hop != results[j].Hop {
			return results[i].Hop < results[j].Hop
		}
		return results[i].Count > results[j].Count
	})
	if len(results) > 50 {
		results = results[:50]
	}

	return map[string]any{
		"start_room": startRoom,
		"max_hops":   maxHops,
		"results":    results,
		"count":      len(results),
	}, nil
}

func (s *Server) toolFindTunnels(args map[string]any) (any, error) {
	wingA, _ := args["wing_a"].(string)
	wingB, _ := args["wing_b"].(string)

	graph, err := s.col.BuildRoomGraph(reqCtx())
	if err != nil {
		return nil, err
	}

	type tunnel struct {
		Room   string   `json:"room"`
		Wings  []string `json:"wings"`
		Halls  []string `json:"halls"`
		Count  int      `json:"count"`
		Recent string   `json:"recent"`
	}
	tunnels := []tunnel{}
	for room, node := range graph {
		if len(node.Wings) < 2 {
			continue
		}
		if wingA != "" && !contains(node.Wings, wingA) {
			continue
		}
		if wingB != "" && !contains(node.Wings, wingB) {
			continue
		}
		recent := ""
		if len(node.Dates) > 0 {
			recent = node.Dates[len(node.Dates)-1]
		}
		tunnels = append(tunnels, tunnel{room, node.Wings, node.Halls, node.Count, recent})
	}
	sort.SliceStable(tunnels, func(i, j int) bool { return tunnels[i].Count > tunnels[j].Count })
	if len(tunnels) > 50 {
		tunnels = tunnels[:50]
	}
	return map[string]any{"tunnels": tunnels, "count": len(tunnels)}, nil
}

func (s *Server) toolGraphStats(_ map[string]any) (any, error) {
	graph, err := s.col.BuildRoomGraph(reqCtx())
	if err != nil {
		return nil, err
	}

	tunnelRooms := 0
	totalEdges := 0
	wingCounts := map[string]int{}
	for _, node := range graph {
		nw := len(node.Wings)
		if nw >= 2 {
			tunnelRooms++
			// edges = (wing pairs) × halls — upstream emits one edge per
			// wing-pair per hall, so rooms with no hall contribute none.
			pairs := nw * (nw - 1) / 2
			totalEdges += pairs * len(node.Halls)
		}
		for _, w := range node.Wings {
			wingCounts[w]++
		}
	}

	type topTunnel struct {
		Room  string   `json:"room"`
		Wings []string `json:"wings"`
		Count int      `json:"count"`
	}
	type roomEntry struct {
		room string
		node *storage.RoomNode
	}
	entries := make([]roomEntry, 0, len(graph))
	for r, n := range graph {
		entries = append(entries, roomEntry{r, n})
	}
	// Most wings first, then most drawers, then name for stability.
	sort.SliceStable(entries, func(i, j int) bool {
		if len(entries[i].node.Wings) != len(entries[j].node.Wings) {
			return len(entries[i].node.Wings) > len(entries[j].node.Wings)
		}
		if entries[i].node.Count != entries[j].node.Count {
			return entries[i].node.Count > entries[j].node.Count
		}
		return entries[i].room < entries[j].room
	})
	top := []topTunnel{}
	for _, e := range entries {
		if len(top) >= 10 {
			break
		}
		if len(e.node.Wings) >= 2 {
			top = append(top, topTunnel{e.room, e.node.Wings, e.node.Count})
		}
	}

	return map[string]any{
		"total_rooms":    len(graph),
		"tunnel_rooms":   tunnelRooms,
		"total_edges":    totalEdges,
		"rooms_per_wing": wingCounts,
		"top_tunnels":    top,
	}, nil
}

// ---------------------------------------------------------------------------
// Explicit tunnels (stored)
// ---------------------------------------------------------------------------

func (s *Server) toolCreateTunnel(args map[string]any) (any, error) {
	srcWing, _ := args["source_wing"].(string)
	srcRoom, _ := args["source_room"].(string)
	tgtWing, _ := args["target_wing"].(string)
	tgtRoom, _ := args["target_room"].(string)
	label, _ := args["label"].(string)
	srcDrawer, _ := args["source_drawer_id"].(string)
	tgtDrawer, _ := args["target_drawer_id"].(string)

	if srcWing == "" || srcRoom == "" || tgtWing == "" || tgtRoom == "" {
		return nil, fmt.Errorf("source_wing, source_room, target_wing, and target_room are required")
	}

	tunnel, err := s.tunnels.Create(reqCtx(), srcWing, srcRoom, tgtWing, tgtRoom, label, srcDrawer, tgtDrawer)
	if err != nil {
		return nil, err
	}
	return tunnel, nil
}

func (s *Server) toolListTunnels(args map[string]any) (any, error) {
	wing, _ := args["wing"].(string)
	tunnels, err := s.tunnels.List(reqCtx(), wing)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tunnels": tunnels, "count": len(tunnels)}, nil
}

func (s *Server) toolDeleteTunnel(args map[string]any) (any, error) {
	id, _ := args["tunnel_id"].(string)
	if id == "" {
		return nil, fmt.Errorf("tunnel_id is required")
	}
	deleted, err := s.tunnels.Delete(reqCtx(), id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": id, "found": deleted}, nil
}

func (s *Server) toolFollowTunnels(args map[string]any) (any, error) {
	wing, _ := args["wing"].(string)
	room, _ := args["room"].(string)
	if wing == "" || room == "" {
		return nil, fmt.Errorf("wing and room are required")
	}

	ctx := reqCtx()
	connections, err := s.tunnels.Follow(ctx, wing, room)
	if err != nil {
		return nil, err
	}

	// Enrich with drawer previews where a drawer_id is known.
	var ids []string
	for _, c := range connections {
		if c.DrawerID != "" {
			ids = append(ids, c.DrawerID)
		}
	}
	if len(ids) > 0 {
		drawers, err := s.col.GetByIDs(ctx, ids)
		if err == nil {
			byID := map[string]string{}
			for _, d := range drawers {
				preview := d.Document
				if len(preview) > 300 {
					preview = preview[:300]
				}
				byID[d.ID] = preview
			}
			for i := range connections {
				if p, ok := byID[connections[i].DrawerID]; ok {
					connections[i].DrawerPreview = p
				}
			}
		}
	}

	return map[string]any{"connections": connections, "count": len(connections)}, nil
}

// ---------------------------------------------------------------------------
// Graph helpers
// ---------------------------------------------------------------------------

func sliceToSet(s []string) map[string]struct{} {
	m := make(map[string]struct{}, len(s))
	for _, v := range s {
		m[v] = struct{}{}
	}
	return m
}

func intersect(set map[string]struct{}, list []string) []string {
	var out []string
	for _, v := range list {
		if _, ok := set[v]; ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// fuzzyRooms returns up to 5 room names approximately matching the query.
func fuzzyRooms(query string, graph map[string]*storage.RoomNode) []string {
	type scored struct {
		room  string
		score float64
	}
	q := strings.ToLower(query)
	var matches []scored
	for room := range graph {
		lr := strings.ToLower(room)
		switch {
		case strings.Contains(lr, q):
			matches = append(matches, scored{room, 1.0})
		case anyWordMatch(lr, q):
			matches = append(matches, scored{room, 0.5})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].room < matches[j].room
	})
	out := []string{}
	for _, m := range matches {
		if len(out) >= 5 {
			break
		}
		out = append(out, m.room)
	}
	return out
}

// anyWordMatch reports whether any hyphen-separated token of query appears in room.
func anyWordMatch(room, query string) bool {
	for _, w := range strings.Split(query, "-") {
		if w != "" && strings.Contains(room, w) {
			return true
		}
	}
	return false
}
