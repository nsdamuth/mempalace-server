package config

import (
	"os"
	"strconv"
)

// Config holds all server settings sourced from environment variables.
type Config struct {
	// PostgreSQL
	DatabaseURL string
	PoolMin     int32
	PoolMax     int32

	// Multi-tenancy
	TenantID string

	// Auth
	MCPAPIKey string

	// Embedding (OpenAI-compatible API — works with Ollama, LM Studio, etc.)
	EmbedAPIURL string // e.g. http://ollama:11434/v1
	EmbedAPIKey string // optional, e.g. for OpenAI
	EmbedModel  string // e.g. nomic-embed-text, text-embedding-3-small
	EmbedDim    int    // must match model output; 384 for all-MiniLM-L6-v2

	// HNSW search quality (higher = better recall, slower)
	EFSearch int

	// HTTP
	Port string
}

func Load() Config {
	return Config{
		DatabaseURL: env("MEMPALACE_DB_URL", ""),
		PoolMin:     int32(envInt("MEMPALACE_PG_POOL_MIN", 2)),
		PoolMax:     int32(envInt("MEMPALACE_PG_POOL_MAX", 10)),
		TenantID:    env("MEMPALACE_TENANT_ID", "default"),
		MCPAPIKey:   env("MCP_API_KEY", ""),
		EmbedAPIURL: env("EMBED_API_URL", "http://localhost:11434/v1"),
		EmbedAPIKey: env("EMBED_API_KEY", ""),
		EmbedModel:  env("EMBED_MODEL", "nomic-embed-text"),
		EmbedDim:    envInt("EMBED_DIM", 384),
		EFSearch:    envInt("MEMPALACE_HNSW_EF_SEARCH", 100),
		Port:        env("PORT", "8000"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
