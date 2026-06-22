package handler

import "fmt"

// registerTripleTools wires the temporal knowledge-graph tools (triple store).
// These mirror the upstream MemPalace KG surface and are always available
// (plain SQL — no Apache AGE dependency).
func (s *Server) registerTripleTools() {
	s.add("mempalace_kg_add",
		"Add a fact to the knowledge graph. Subject → predicate → object with optional time window. E.g. ('Max', 'started_school', 'Year 7', valid_from='2026-09-01').",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"subject":       {Type: "string", Description: "The entity doing/being something"},
				"predicate":     {Type: "string", Description: "The relationship type (e.g. 'loves', 'works_on', 'daughter_of')"},
				"object":        {Type: "string", Description: "The entity being connected to"},
				"valid_from":    {Type: "string", Description: "When this became true (YYYY-MM-DD, optional)"},
				"source_closet": {Type: "string", Description: "Closet/drawer ID where this fact appears (optional)"},
			},
			Required: []string{"subject", "predicate", "object"},
		},
		s.toolKGAdd)

	s.add("mempalace_kg_query",
		"Query the knowledge graph for an entity's relationships. Returns typed facts with temporal validity. Filter by date with as_of to see what was true at a point in time.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"entity":    {Type: "string", Description: "Entity to query (e.g. 'Max', 'MyProject', 'Alice')"},
				"as_of":     {Type: "string", Description: "Date filter — only facts valid at this date (YYYY-MM-DD, optional)"},
				"direction": {Type: "string", Description: "outgoing (entity→?), incoming (?→entity), or both (default: both)", Default: "both"},
			},
			Required: []string{"entity"},
		},
		s.toolKGQuery)

	s.add("mempalace_kg_invalidate",
		"Mark a fact as no longer true. E.g. an injury resolved, a job ended, moved house.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"subject":   {Type: "string", Description: "Entity"},
				"predicate": {Type: "string", Description: "Relationship"},
				"object":    {Type: "string", Description: "Connected entity"},
				"ended":     {Type: "string", Description: "When it stopped being true (YYYY-MM-DD, default: today)"},
			},
			Required: []string{"subject", "predicate", "object"},
		},
		s.toolKGInvalidate)

	s.add("mempalace_kg_timeline",
		"Chronological timeline of facts. Shows the story of an entity (or everything) in order.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"entity": {Type: "string", Description: "Entity to get timeline for (optional — omit for full timeline)"},
			},
		},
		s.toolKGTimeline)

	s.add("mempalace_kg_stats",
		"Knowledge graph overview: entities, triples, current vs expired facts, relationship types.",
		inputSchema{Type: "object"},
		s.toolKGStats)
}

func (s *Server) toolKGAdd(args map[string]any) (any, error) {
	subject, _ := args["subject"].(string)
	predicate, _ := args["predicate"].(string)
	object, _ := args["object"].(string)
	validFrom, _ := args["valid_from"].(string)
	sourceCloset, _ := args["source_closet"].(string)

	if subject == "" || predicate == "" || object == "" {
		return nil, fmt.Errorf("subject, predicate, and object are required")
	}

	tripleID, err := s.triples.AddTriple(reqCtx(), subject, predicate, object, validFrom, sourceCloset)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"success":   true,
		"triple_id": tripleID,
		"fact":      fmt.Sprintf("%s → %s → %s", subject, predicate, object),
	}, nil
}

func (s *Server) toolKGQuery(args map[string]any) (any, error) {
	entity, _ := args["entity"].(string)
	if entity == "" {
		return nil, fmt.Errorf("entity is required")
	}
	asOf, _ := args["as_of"].(string)
	direction, _ := args["direction"].(string)
	if direction == "" {
		direction = "both"
	}
	if direction != "outgoing" && direction != "incoming" && direction != "both" {
		return nil, fmt.Errorf("direction must be 'outgoing', 'incoming', or 'both'")
	}

	facts, err := s.triples.QueryEntity(reqCtx(), entity, asOf, direction)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"entity": entity,
		"as_of":  asOf,
		"facts":  facts,
		"count":  len(facts),
	}, nil
}

func (s *Server) toolKGInvalidate(args map[string]any) (any, error) {
	subject, _ := args["subject"].(string)
	predicate, _ := args["predicate"].(string)
	object, _ := args["object"].(string)
	ended, _ := args["ended"].(string)

	if subject == "" || predicate == "" || object == "" {
		return nil, fmt.Errorf("subject, predicate, and object are required")
	}

	if err := s.triples.Invalidate(reqCtx(), subject, predicate, object, ended); err != nil {
		return nil, err
	}
	endedMsg := ended
	if endedMsg == "" {
		endedMsg = "today"
	}
	return map[string]any{
		"success": true,
		"fact":    fmt.Sprintf("%s → %s → %s", subject, predicate, object),
		"ended":   endedMsg,
	}, nil
}

func (s *Server) toolKGTimeline(args map[string]any) (any, error) {
	entity, _ := args["entity"].(string)
	facts, err := s.triples.Timeline(reqCtx(), entity)
	if err != nil {
		return nil, err
	}
	label := entity
	if label == "" {
		label = "all"
	}
	return map[string]any{
		"entity":   label,
		"timeline": facts,
		"count":    len(facts),
	}, nil
}

func (s *Server) toolKGStats(_ map[string]any) (any, error) {
	stats, err := s.triples.Stats(reqCtx())
	if err != nil {
		return nil, err
	}
	return stats, nil
}
