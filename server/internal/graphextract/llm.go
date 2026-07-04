package graphextract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mempalace/server/internal/llm"
	"mempalace/server/internal/storage"
)

// completer is the slice of *llm.Client that the LLM extractor needs.
// Declaring it as an interface keeps the extractor unit-testable without a
// live model.
type completer interface {
	Complete(ctx context.Context, messages []llm.Message, jsonMode bool) (string, error)
}

// LLM extracts real entities and relations from drawer content using an
// OpenAI-compatible chat model.
type LLM struct {
	client completer
}

// NewLLM builds an LLM-backed extractor around a chat client.
func NewLLM(client *llm.Client) *LLM { return &LLM{client: client} }

const systemPrompt = `You extract a knowledge graph from a single memory note.
Return ONLY a JSON object, no prose, no code fences, with this exact shape:
{"entities":[{"name":"","type":"","description":""}],"relations":[{"from":"","type":"","to":""}]}
Rules:
- Include only entities explicitly present in the note (people, projects, organisations, concepts, places, artifacts).
- "type" for entities is a short noun label (e.g. Person, Project, Concept).
- "type" for relations is UPPER_SNAKE_CASE (e.g. WORKS_ON, KNOWS, PART_OF).
- Every relation "from"/"to" MUST match an entity "name" you list.
- If the note has no extractable entities, return {"entities":[],"relations":[]}.`

// Extract asks the model for entities/relations and normalises the result.
func (e *LLM) Extract(ctx context.Context, d DrawerRef) ([]storage.KGEntity, []storage.KGRelation, error) {
	raw, err := e.client.Complete(ctx, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: d.Content},
	}, true)
	if err != nil {
		return nil, nil, fmt.Errorf("llm extract: %w", err)
	}
	return parseExtraction(raw)
}

type llmOutput struct {
	Entities []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"entities"`
	Relations []struct {
		From string `json:"from"`
		Type string `json:"type"`
		To   string `json:"to"`
	} `json:"relations"`
}

// parseExtraction turns a model reply into graph nodes and edges. It tolerates
// code fences and surrounding prose, drops empty/incomplete records, and
// guarantees every relation endpoint is also present as an entity (the AGE
// writer MATCHes both ends, so a dangling endpoint would silently fail).
func parseExtraction(raw string) ([]storage.KGEntity, []storage.KGRelation, error) {
	js := extractJSONObject(raw)
	if js == "" {
		return nil, nil, fmt.Errorf("llm extract: no JSON object in reply")
	}

	var out llmOutput
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		return nil, nil, fmt.Errorf("llm extract: decode: %w", err)
	}

	seen := map[string]int{} // entity name -> index in ents
	var ents []storage.KGEntity
	addEntity := func(name, etype, desc string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		if etype == "" {
			etype = "entity"
		}
		seen[name] = len(ents)
		ents = append(ents, storage.KGEntity{Name: name, EntityType: etype, Description: desc})
	}

	for _, en := range out.Entities {
		addEntity(en.Name, en.Type, en.Description)
	}

	var rels []storage.KGRelation
	for _, r := range out.Relations {
		from := strings.TrimSpace(r.From)
		to := strings.TrimSpace(r.To)
		typ := strings.TrimSpace(r.Type)
		if from == "" || to == "" || typ == "" {
			continue
		}
		// Ensure both endpoints exist as entities so AddRelation can MATCH them.
		addEntity(from, "entity", "")
		addEntity(to, "entity", "")
		rels = append(rels, storage.KGRelation{Type: typ, From: from, To: to})
	}

	return ents, rels, nil
}

// extractJSONObject returns the outermost {...} object from a string, stripping
// Markdown code fences and any leading/trailing prose. Returns "" if none.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` or ``` ... ``` fences.
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}
