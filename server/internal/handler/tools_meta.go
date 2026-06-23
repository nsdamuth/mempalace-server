package handler

import (
	"context"
	"fmt"
	"time"
)

// aaakSpec is the AAAK dialect specification (verbatim from upstream MemPalace).
const aaakSpec = `AAAK is a compressed memory dialect that MemPalace uses for efficient storage.
It is designed to be readable by both humans and LLMs without decoding.

FORMAT:
  ENTITIES: 3-letter uppercase codes. ALC=Alice, JOR=Jordan, RIL=Riley, MAX=Max, BEN=Ben.
  EMOTIONS: *action markers* before/during text. *warm*=joy, *fierce*=determined, *raw*=vulnerable, *bloom*=tenderness.
  STRUCTURE: Pipe-separated fields. FAM: family | PROJ: projects | ⚠: warnings/reminders.
  DATES: ISO format (2026-03-31). COUNTS: Nx = N mentions (e.g., 570x).
  IMPORTANCE: ★ to ★★★★★ (1-5 scale).
  HALLS: hall_facts, hall_events, hall_discoveries, hall_preferences, hall_advice.
  WINGS: wing_user, wing_agent, wing_team, wing_code, wing_myproject, wing_hardware, wing_ue5, wing_ai_research.
  ROOMS: Hyphenated slugs representing named ideas (e.g., chromadb-setup, gpu-pricing).

EXAMPLE:
  FAM: ALC→♡JOR | 2D(kids): RIL(18,sports) MAX(11,chess+swimming) | BEN(contributor)

Read AAAK naturally — expand codes mentally, treat *markers* as emotional context.
When WRITING AAAK: use entity codes, mark emotions, keep structure tight.`

// registerMetaTools wires the AAAK spec, hook settings, and filing-checkpoint tools.
func (s *Server) registerMetaTools() {
	s.add("mempalace_get_aaak_spec",
		"Get the AAAK dialect specification — the compressed memory format MemPalace uses. Call this if you need to read or write AAAK-compressed memories.",
		inputSchema{Type: "object"},
		s.toolGetAAAKSpec)

	// Split read/write tools so a read-only key can view settings.
	s.add("mempalace_get_hook_settings",
		"View current hook behavior (silent_save, desktop_toast). Read-only.",
		inputSchema{Type: "object"},
		s.toolGetHookSettings)

	hookSetSchema := inputSchema{
		Type: "object",
		Properties: map[string]schemaProp{
			"silent_save":   {Type: "boolean", Description: "True = silent direct save, False = blocking MCP calls"},
			"desktop_toast": {Type: "boolean", Description: "True = show desktop toast via notify-send"},
		},
	}
	s.add("mempalace_set_hook_settings",
		"Set hook behavior. silent_save: True = save directly (no MCP clutter), False = legacy blocking. desktop_toast: True = show desktop notification.",
		hookSetSchema,
		s.toolSetHookSettings)

	// Backward-compatible alias for the upstream combined get-or-set tool.
	// Classified as write (it can mutate); use the split tools for read-only access.
	s.add("mempalace_hook_settings",
		"Get or set hook behavior. silent_save: True = save directly (no MCP clutter), False = legacy blocking. desktop_toast: True = show desktop notification. Call with no args to view. (Alias — prefer get/set_hook_settings.)",
		hookSetSchema,
		s.toolSetHookSettings)

	s.add("mempalace_memories_filed_away",
		"Check recent palace filing activity. Returns how many drawers were filed today and the latest timestamp.",
		inputSchema{Type: "object"},
		s.toolMemoriesFiledAway)
}

func (s *Server) toolGetAAAKSpec(_ map[string]any) (any, error) {
	return map[string]any{"aaak_spec": aaakSpec}, nil
}

// toolGetHookSettings returns the current hook settings (read-only).
func (s *Server) toolGetHookSettings(_ map[string]any) (any, error) {
	ctx := reqCtx()
	settings, err := s.readHookSettings(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "settings": settings}, nil
}

// toolSetHookSettings updates hook settings and returns the new values (write).
func (s *Server) toolSetHookSettings(args map[string]any) (any, error) {
	ctx := reqCtx()

	var changed []string
	if v := boolArgPtr(args, "silent_save"); v != nil {
		if err := s.settings.Set(ctx, "silent_save", *v); err != nil {
			return nil, err
		}
		changed = append(changed, fmt.Sprintf("silent_save → %v", *v))
	}
	if v := boolArgPtr(args, "desktop_toast"); v != nil {
		if err := s.settings.Set(ctx, "desktop_toast", *v); err != nil {
			return nil, err
		}
		changed = append(changed, fmt.Sprintf("desktop_toast → %v", *v))
	}

	settings, err := s.readHookSettings(ctx)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"success": true, "settings": settings}
	if len(changed) > 0 {
		result["updated"] = changed
	}
	return result, nil
}

// readHookSettings loads the current hook settings with their defaults.
func (s *Server) readHookSettings(ctx context.Context) (map[string]any, error) {
	silentSave, err := s.settings.GetBool(ctx, "silent_save", true)
	if err != nil {
		return nil, err
	}
	desktopToast, err := s.settings.GetBool(ctx, "desktop_toast", false)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"silent_save":   silentSave,
		"desktop_toast": desktopToast,
	}, nil
}

func (s *Server) toolMemoriesFiledAway(_ map[string]any) (any, error) {
	ctx := reqCtx()
	today := time.Now().Format("2006-01-02")

	count, latest, err := s.col.RecentFilingStats(ctx, today)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return map[string]any{
			"status":    "quiet",
			"message":   "No drawers filed today",
			"count":     0,
			"timestamp": latest,
		}, nil
	}
	return map[string]any{
		"status":    "ok",
		"message":   fmt.Sprintf("✦ %d drawers filed today", count),
		"count":     count,
		"timestamp": latest,
	}, nil
}
