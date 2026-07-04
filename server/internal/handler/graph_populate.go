package handler

import (
	"context"
	"log"

	"mempalace/core/config"
	"mempalace/core/storage"
	"mempalace/server/internal/graphextract"
	"mempalace/server/internal/llm"
)

// buildExtractor selects the knowledge-graph extractor from config. It returns
// nil (auto-population disabled) when the feature is off, when AGE is not
// available, or when the chosen strategy is misconfigured — every "off" path
// is logged so the operator can see why nothing is being written.
func buildExtractor(cfg config.Config, graph *storage.Graph) graphextract.Extractor {
	if !cfg.GraphAutoPopulate {
		return nil
	}
	if graph == nil {
		log.Printf("graph auto-populate: enabled but AGE graph is not available — disabling")
		return nil
	}

	switch cfg.GraphExtractor {
	case "", "structural":
		log.Printf("graph auto-populate: enabled (structural extractor)")
		return graphextract.Structural{}
	case "llm":
		if cfg.LLMAPIURL == "" || cfg.LLMModel == "" {
			log.Printf("graph auto-populate: extractor=llm but LLM_API_URL/LLM_MODEL unset — disabling")
			return nil
		}
		client := llm.NewClient(cfg.LLMAPIURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMProvider)
		switch client.Provider() {
		case llm.ProviderOllama:
			log.Printf("graph auto-populate: enabled (llm extractor, provider=ollama /api/chat think=false, model=%s)", cfg.LLMModel)
		default:
			log.Printf("graph auto-populate: enabled (llm extractor, provider=openai /v1, model=%s) — NOTE: use a non-thinking model; a reasoning model's <think> output will exceed the timeout", cfg.LLMModel)
		}
		return graphextract.NewLLM(client)
	default:
		log.Printf("graph auto-populate: unknown extractor %q — disabling", cfg.GraphExtractor)
		return nil
	}
}

// populateGraph writes the extractor's nodes and edges into the AGE graph.
// It is best-effort: any failure is logged and swallowed so that graph
// population never fails the underlying add_drawer call (e.g. an LLM being
// unreachable must not block filing a memory).
func (s *Server) populateGraph(ctx context.Context, ref graphextract.DrawerRef) {
	if s.extractor == nil || s.graph == nil {
		return
	}

	ents, rels, err := s.extractor.Extract(ctx, ref)
	if err != nil {
		log.Printf("graph auto-populate: extract drawer %s: %v", ref.DrawerID, err)
		return
	}

	for _, e := range ents {
		if _, err := s.graph.AddEntity(ctx, e.Name, e.EntityType, e.Description); err != nil {
			log.Printf("graph auto-populate: add entity %q: %v", e.Name, err)
		}
	}
	// Relations MATCH both endpoints, so they must run after all entities exist.
	for _, r := range rels {
		if err := s.graph.AddRelation(ctx, r.From, r.Type, r.To); err != nil {
			log.Printf("graph auto-populate: add relation %s-[%s]->%s: %v", r.From, r.Type, r.To, err)
		}
	}
}
