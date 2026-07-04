// Package llm provides a minimal chat client for optional server-side
// extraction tasks (e.g. auto-populating the knowledge graph from drawer
// content). It speaks two dialects, selected by provider:
//
//   - "openai": the OpenAI /v1/chat/completions API. Compatible with OpenAI,
//     Ollama's compat layer, LM Studio and LocalAI. This endpoint cannot turn
//     off a model's reasoning, so a NON-thinking model must be used.
//   - "ollama": Ollama's native /api/chat API with think=false, which disables
//     reasoning — so thinking models (qwen3, …) can be used.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Provider dialects.
const (
	ProviderOpenAI = "openai"
	ProviderOllama = "ollama"
)

// Client calls a chat endpoint using the configured provider dialect.
type Client struct {
	provider string // ProviderOpenAI | ProviderOllama
	url      string // fully resolved endpoint
	apiKey   string
	model    string
	http     *http.Client
}

// NewClient builds a chat client. apiURL is the base URL: for the OpenAI
// provider it is the OpenAI-compatible base incl. /v1 (e.g.
// http://host:11434/v1); for the Ollama provider it is the Ollama root without
// /v1 (e.g. http://host:11434). apiKey may be empty for local servers. An empty
// or unknown provider defaults to OpenAI.
func NewClient(apiURL, apiKey, model, provider string) *Client {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != ProviderOllama {
		provider = ProviderOpenAI
	}

	base := strings.TrimRight(apiURL, "/")
	var endpoint string
	switch provider {
	case ProviderOllama:
		endpoint = base + "/api/chat"
	default:
		endpoint = base + "/chat/completions"
	}

	return &Client{
		provider: provider,
		url:      endpoint,
		apiKey:   apiKey,
		model:    model,
		// Extraction runs in the add_drawer write path; keep the ceiling high
		// enough for small local models but bounded so a hung server cannot
		// block a request forever.
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// Provider reports the configured dialect ("openai" | "ollama").
func (c *Client) Provider() string { return c.provider }

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete sends the messages and returns the assistant's reply text. When
// jsonMode is true it requests the provider's JSON output mode. It dispatches to
// the provider-specific implementation.
func (c *Client) Complete(ctx context.Context, messages []Message, jsonMode bool) (string, error) {
	if c.provider == ProviderOllama {
		return c.completeOllama(ctx, messages, jsonMode)
	}
	return c.completeOpenAI(ctx, messages, jsonMode)
}

// --- OpenAI /v1/chat/completions --------------------------------------------

type openAIReq struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	Stream         bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"` // "json_object"
}

type openAIResp struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) completeOpenAI(ctx context.Context, messages []Message, jsonMode bool) (string, error) {
	reqBody := openAIReq{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0, // deterministic extraction
		Stream:      false,
	}
	if jsonMode {
		reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	var result openAIResp
	if err := c.postJSON(ctx, reqBody, &result); err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("chat API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("chat API returned no choices")
	}
	return result.Choices[0].Message.Content, nil
}

// --- Ollama native /api/chat ------------------------------------------------

type ollamaReq struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Think    bool           `json:"think"`            // always false — disables reasoning
	Format   string         `json:"format,omitempty"` // "json" in JSON mode
	Options  map[string]any `json:"options,omitempty"`
}

type ollamaResp struct {
	Message Message `json:"message"`
	Error   string  `json:"error,omitempty"`
}

func (c *Client) completeOllama(ctx context.Context, messages []Message, jsonMode bool) (string, error) {
	reqBody := ollamaReq{
		Model:    c.model,
		Messages: messages,
		Stream:   false,
		Think:    false, // the whole point of this provider: no reasoning
		Options:  map[string]any{"temperature": 0},
	}
	if jsonMode {
		reqBody.Format = "json"
	}

	var result ollamaResp
	if err := c.postJSON(ctx, reqBody, &result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("chat API error: %s", result.Error)
	}
	return result.Message.Content, nil
}

// --- shared HTTP plumbing ---------------------------------------------------

// postJSON marshals reqBody, POSTs it to the configured endpoint, and decodes a
// 2xx JSON response into out.
func (c *Client) postJSON(ctx context.Context, reqBody any, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chat API returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("chat decode: %w", err)
	}
	return nil
}
