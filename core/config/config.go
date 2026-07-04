package config

import "github.com/caarlos0/env/v11"

// Config holds all server settings sourced from environment variables.
type Config struct {
	// PostgreSQL
	DatabaseURL string `env:"MEMPALACE_DB_URL"`
	PoolMin     int32  `env:"MEMPALACE_PG_POOL_MIN" envDefault:"2"`
	PoolMax     int32  `env:"MEMPALACE_PG_POOL_MAX" envDefault:"10"`

	// Multi-tenancy
	TenantID string `env:"MEMPALACE_TENANT_ID" envDefault:"default"`

	// Auth
	MCPAPIKey         string `env:"MCP_API_KEY"`          // full access (read + write); required
	MCPAPIKeyReadOnly string `env:"MCP_API_KEY_READONLY"` // optional read-only key; "" disables it

	// Embedding (OpenAI-compatible API — works with Ollama, LM Studio, etc.)
	EmbedAPIURL string `env:"EMBED_API_URL" envDefault:"http://localhost:11434/v1"` // e.g. http://ollama:11434/v1
	EmbedAPIKey string `env:"EMBED_API_KEY"`                                        // optional, e.g. for OpenAI
	EmbedModel  string `env:"EMBED_MODEL" envDefault:"embeddinggemma"`              // e.g. embeddinggemma (multilingual, 100+ langs), nomic-embed-text, text-embedding-3-small
	EmbedDim    int    `env:"EMBED_DIM" envDefault:"768"`                           // must equal model output, cannot exceed it; 768 for embeddinggemma (Matryoshka: also 512/256/128)

	// HNSW search quality (higher = better recall, slower)
	EFSearch int `env:"MEMPALACE_HNSW_EF_SEARCH" envDefault:"100"`

	// Optional plain REST/JSON API (off by default; MCP is always on)
	EnableRESTAPI bool `env:"ENABLE_REST_API" envDefault:"false"`

	// Room redirects (opt-in). When enabled, the redirect tools are exposed and
	// add_drawer / search / list_drawers transparently follow a merged/renamed
	// room to its canonical target. Default off — no redirect tools are
	// registered and add_drawer stays storage-only, preserving existing behavior.
	RoomRedirects bool `env:"MEMPALACE_ROOM_REDIRECTS" envDefault:"false"`

	// Dream consolidation job (cmd/dreamjob). Scans rooms once per run and writes
	// near-duplicate merge candidates for review — it never merges anything
	// itself. Consumed by the mempalace_*_merge_candidate MCP tools.
	DreamSemantic  bool    `env:"MEMPALACE_DREAM_SEMANTIC" envDefault:"true"`  // also cluster by embedding similarity, not just name normalization
	DreamThreshold float64 `env:"MEMPALACE_DREAM_THRESHOLD" envDefault:"0.88"` // cosine cutoff for the semantic tier (0..1)

	// Knowledge-graph auto-population (opt-in). When enabled, add_drawer also
	// writes to the AGE graph using the selected extractor strategy. Default
	// off — add_drawer stays storage-only, preserving existing behavior.
	GraphAutoPopulate bool   `env:"MEMPALACE_GRAPH_AUTO_POPULATE" envDefault:"false"`
	GraphExtractor    string `env:"MEMPALACE_GRAPH_EXTRACTOR" envDefault:"structural"` // "structural" (deterministic, no LLM) or "llm"

	// LLM extraction — only used when GraphExtractor == "llm".
	//
	// LLMProvider selects how the chat endpoint is called:
	//   "openai" (default) — OpenAI-compatible /v1/chat/completions. Works with
	//       OpenAI, LM Studio, LocalAI, and Ollama's compat layer. NOTE: you must
	//       use a NON-thinking model here — a reasoning model streams a long
	//       <think> block that this endpoint cannot suppress, blowing the timeout.
	//       Set LLM_API_URL to the base incl. /v1 (e.g. http://host:11434/v1).
	//   "ollama" — Ollama-native /api/chat with think=false, which disables
	//       reasoning, so thinking models (qwen3, …) work. Set LLM_API_URL to the
	//       Ollama root WITHOUT /v1 (e.g. http://host:11434).
	LLMProvider string `env:"LLM_PROVIDER" envDefault:"openai"` // "openai" | "ollama"
	LLMAPIURL   string `env:"LLM_API_URL"`                      // openai: http://host:11434/v1 · ollama: http://host:11434
	LLMAPIKey   string `env:"LLM_API_KEY"`                      // optional (empty for local servers)
	LLMModel    string `env:"LLM_MODEL"`                        // e.g. llama3.2 (openai), qwen3 (ollama)

	// HTTP
	Port string `env:"PORT" envDefault:"8000"`
}

// Load reads the configuration from the process environment. It returns an
// error if any variable cannot be parsed into its target type.
func Load() (Config, error) {
	return env.ParseAs[Config]()
}
