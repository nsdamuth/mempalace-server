package storage

import (
	"context"
	"fmt"
	"sort"
)

// RoomNode is the aggregated view of a single room across the whole palace:
// which wings it appears in, which halls, how many drawers, and recent dates.
// Rooms spanning ≥2 wings are "tunnels" — the same idea filed in multiple domains.
type RoomNode struct {
	Wings []string `json:"wings"`
	Halls []string `json:"halls"`
	Count int      `json:"count"`
	Dates []string `json:"dates"`
}

// BuildRoomGraph aggregates drawer metadata into a room → RoomNode map.
// Mirrors upstream palace_graph.build_graph: skips empty/"general" rooms and
// rows without a wing. Dates are capped to the 5 most recent per room.
func (c *Collection) BuildRoomGraph(ctx context.Context) (map[string]*RoomNode, error) {
	sql := fmt.Sprintf(`
		SELECT
			COALESCE(metadata->>'room', '') AS room,
			COALESCE(metadata->>'wing', '') AS wing,
			COALESCE(metadata->>'hall', '') AS hall,
			COALESCE(metadata->>'date', '') AS date
		FROM %s`, c.fqt)

	rows, err := c.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("build room graph: %w", err)
	}
	defer rows.Close()

	type agg struct {
		wings map[string]struct{}
		halls map[string]struct{}
		dates map[string]struct{}
		count int
	}
	tmp := map[string]*agg{}

	for rows.Next() {
		var room, wing, hall, date string
		if err := rows.Scan(&room, &wing, &hall, &date); err != nil {
			return nil, err
		}
		if room == "" || room == "general" || wing == "" {
			continue
		}
		a := tmp[room]
		if a == nil {
			a = &agg{wings: map[string]struct{}{}, halls: map[string]struct{}{}, dates: map[string]struct{}{}}
			tmp[room] = a
		}
		a.wings[wing] = struct{}{}
		if hall != "" {
			a.halls[hall] = struct{}{}
		}
		if date != "" {
			a.dates[date] = struct{}{}
		}
		a.count++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	graph := make(map[string]*RoomNode, len(tmp))
	for room, a := range tmp {
		dates := setToSorted(a.dates)
		if len(dates) > 5 {
			dates = dates[len(dates)-5:] // keep the 5 most recent
		}
		graph[room] = &RoomNode{
			Wings: setToSorted(a.wings),
			Halls: setToSorted(a.halls),
			Count: a.count,
			Dates: dates,
		}
	}
	return graph, nil
}

// RecentFilingStats returns the number of drawers filed on/after sinceDate
// (a YYYY-MM-DD prefix; filed_at is RFC3339 so lexical comparison is correct)
// and the latest filed_at timestamp in the palace.
func (c *Collection) RecentFilingStats(ctx context.Context, sinceDate string) (int, string, error) {
	sql := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE COALESCE(metadata->>'filed_at','') >= $1),
			COALESCE(MAX(metadata->>'filed_at'), '')
		FROM %s`, c.fqt)

	var count int
	var latest string
	if err := c.pool.QueryRow(ctx, sql, sinceDate).Scan(&count, &latest); err != nil {
		return 0, "", fmt.Errorf("recent filing stats: %w", err)
	}
	return count, latest, nil
}

func setToSorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
