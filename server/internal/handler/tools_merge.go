package handler

import "fmt"

// registerMergeTools exposes the dream job's output for review: list the
// proposed room merges, apply one (via the redirect machinery), or dismiss it.
// The job itself never mutates the palace — application is a human/LLM decision.
func (s *Server) registerMergeTools() {
	s.add("mempalace_list_merge_candidates",
		"List the dream job's proposed room merges (near-duplicate rooms). Review these, then apply or dismiss each. Highest-confidence first.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"status":    {Type: "string", Description: "Filter by status: pending (default), applied, dismissed. Empty for all.", Default: "pending"},
				"wing":      {Type: "string", Description: "Filter to a wing (optional)"},
				"min_score": {Type: "number", Description: "Minimum similarity score 0-1 (optional)"},
				"limit":     {Type: "integer", Description: "Max results (default 50)", Default: 50, Minimum: intPtr(1), Maximum: intPtr(500)},
			},
		},
		s.toolListMergeCandidates)

	s.add("mempalace_apply_merge_candidate",
		"Apply a proposed room merge by its candidate ID: forwards the old room to the canonical one and moves its drawers, then marks the candidate applied.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"id":           {Type: "string", Description: "Candidate ID from list_merge_candidates"},
				"move_drawers": {Type: "boolean", Description: "Move the old room's drawers to the target (default: true)", Default: true},
			},
			Required: []string{"id"},
		},
		s.toolApplyMergeCandidate)

	s.add("mempalace_dismiss_merge_candidate",
		"Dismiss a proposed room merge by its candidate ID — the two rooms are different and should stay separate. Keeps it out of future pending lists.",
		inputSchema{
			Type: "object",
			Properties: map[string]schemaProp{
				"id": {Type: "string", Description: "Candidate ID to dismiss"},
			},
			Required: []string{"id"},
		},
		s.toolDismissMergeCandidate)
}

func (s *Server) toolListMergeCandidates(args map[string]any) (any, error) {
	if s.mergeCandidates == nil {
		return map[string]any{"candidates": []any{}, "count": 0}, nil
	}
	status, ok := args["status"].(string)
	if !ok {
		status = "pending"
	}
	wing, _ := args["wing"].(string)
	minScore := floatArg(args, "min_score", 0)
	limit := intArg(args, "limit", 50)
	if limit < 1 || limit > 500 {
		limit = 50
	}

	list, err := s.mergeCandidates.List(reqCtx(), status, wing, minScore, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"candidates": list, "count": len(list)}, nil
}

func (s *Server) toolApplyMergeCandidate(args map[string]any) (any, error) {
	if s.mergeCandidates == nil {
		return nil, fmt.Errorf("merge candidates are unavailable")
	}
	ctx := reqCtx()

	id, _ := args["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	moveDrawers := true
	if p := boolArgPtr(args, "move_drawers"); p != nil {
		moveDrawers = *p
	}

	c, err := s.mergeCandidates.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return map[string]any{"success": false, "error": "merge candidate not found: " + id}, nil
	}
	if c.Status == "applied" {
		return map[string]any{"success": true, "reason": "already_applied", "id": id}, nil
	}

	reason := fmt.Sprintf("dream merge (%s, score %.2f)", c.Tier, c.Score)
	resp, err := s.applyRedirect(ctx, c.FromWing, c.FromRoom, c.ToWing, c.ToRoom, reason, moveDrawers)
	if err != nil {
		return nil, err
	}

	if _, err := s.mergeCandidates.SetStatus(ctx, id, "applied"); err != nil {
		return nil, err
	}
	resp["candidate_id"] = id
	return resp, nil
}

func (s *Server) toolDismissMergeCandidate(args map[string]any) (any, error) {
	if s.mergeCandidates == nil {
		return nil, fmt.Errorf("merge candidates are unavailable")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	found, err := s.mergeCandidates.SetStatus(reqCtx(), id, "dismissed")
	if err != nil {
		return nil, err
	}
	return map[string]any{"dismissed": found, "id": id}, nil
}
