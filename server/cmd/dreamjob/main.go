// Command dreamjob is the "dream" consolidation microservice. It runs to
// completion (as a Kubernetes Job/CronJob), scans the palace's rooms once,
// proposes near-duplicate merges, and writes them to room_merge_candidates for
// review. It NEVER merges anything itself — a human or LLM applies the proposals
// later through the MCP redirect tools. Safe to run repeatedly: candidate rows
// are keyed by their merge endpoints, and a reviewer's decision is preserved.
package main

import (
	"context"
	"log"
	"time"

	"mempalace/server/internal/config"
	"mempalace/server/internal/consolidate"
	"mempalace/server/internal/embed"
	"mempalace/server/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("dreamjob: load config: %v", err)
	}
	if cfg.DatabaseURL == "" {
		log.Fatal("dreamjob: MEMPALACE_DB_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool, err := storage.NewPool(ctx, cfg)
	if err != nil {
		log.Fatalf("dreamjob: connect to database: %v", err)
	}
	defer pool.Close()

	if err := storage.ProvisionMergeCandidates(ctx, pool, cfg.TenantID); err != nil {
		log.Fatalf("dreamjob: provision candidates table: %v", err)
	}

	// --- gather rooms ---
	col := storage.NewCollection(pool, cfg.TenantID, cfg.EFSearch)
	tree, _, err := col.WingRoomCounts(ctx)
	if err != nil {
		log.Fatalf("dreamjob: read taxonomy: %v", err)
	}
	var rooms []consolidate.RoomInfo
	for wing, roomMap := range tree {
		for room, cnt := range roomMap {
			rooms = append(rooms, consolidate.RoomInfo{Wing: wing, Room: room, Drawers: cnt})
		}
	}
	log.Printf("dreamjob: tenant=%s rooms=%d semantic=%v threshold=%.2f",
		cfg.TenantID, len(rooms), cfg.DreamSemantic, cfg.DreamThreshold)

	// --- optional semantic tier: embed distinct room names ---
	vec := func(consolidate.RoomInfo) []float32 { return nil } // exact-only by default
	if cfg.DreamSemantic && len(rooms) > 1 {
		embeds, err := embedRoomNames(ctx, cfg, rooms)
		if err != nil {
			// Non-fatal: fall back to the exact tier so the job still produces
			// something useful without the embedding endpoint.
			log.Printf("dreamjob: semantic tier disabled (embed failed): %v", err)
		} else {
			vec = func(r consolidate.RoomInfo) []float32 { return embeds[r.Wing+"/"+r.Room] }
		}
	}

	// --- cluster + persist ---
	candidates := consolidate.Cluster(rooms, vec, cfg.DreamThreshold)
	store := storage.NewMergeCandidateStore(pool, cfg.TenantID)
	runID := time.Now().UTC().Format("20060102T150405Z")

	written := 0
	for _, c := range candidates {
		if err := store.Upsert(ctx, storage.MergeCandidate{
			RunID:    runID,
			FromWing: c.FromWing, FromRoom: c.FromRoom,
			ToWing: c.ToWing, ToRoom: c.ToRoom,
			Tier: c.Tier, Score: c.Score, FromDrawers: c.FromDrawers,
		}); err != nil {
			log.Printf("dreamjob: upsert candidate %s/%s→%s/%s: %v",
				c.FromWing, c.FromRoom, c.ToWing, c.ToRoom, err)
			continue
		}
		written++
	}

	log.Printf("dreamjob: run=%s proposed=%d written=%d — review via mempalace_list_merge_candidates",
		runID, len(candidates), written)
}

// embedRoomNames embeds each distinct room name once and returns a map keyed by
// "wing/room". Room names embed well enough to catch near-duplicates like
// "Auth" vs "Authentication"; content-centroid embeddings are a future upgrade.
func embedRoomNames(ctx context.Context, cfg config.Config, rooms []consolidate.RoomInfo) (map[string][]float32, error) {
	client := embed.NewClient(cfg.EmbedAPIURL, cfg.EmbedAPIKey, cfg.EmbedModel, cfg.EmbedDim)

	keys := make([]string, len(rooms))
	texts := make([]string, len(rooms))
	for i, r := range rooms {
		keys[i] = r.Wing + "/" + r.Room
		texts[i] = r.Room
	}
	vecs, err := client.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(rooms) {
		return nil, nil //nolint:nilnil // length mismatch → treat as no embeddings
	}
	out := make(map[string][]float32, len(rooms))
	for i := range rooms {
		out[keys[i]] = vecs[i]
	}
	return out, nil
}
