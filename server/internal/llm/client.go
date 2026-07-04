// Package llm provides a minimal OpenAI-compatible chat-completions client.
// It is used for optional server-side extraction tasks (e.g. auto-populating
// the knowledge graph from drawer content). Compatible with the OpenAI API,
// Ollama (/v1/chat/completions), LM Studio and LocalAI.
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

// Client calls a /v1/chat/completions endpoint.
type Client struct {
	url    string
	apiKey string
	model  string
	http   *http.Client
}

// NewClient builds a chat client. apiURL is the OpenAI-compatible base
// (e.g. http://host.docker.internal:11434/v1); apiKey may be empty for local
// servers such as Ollama.
func NewClient(apiURL, apiKey, model string) *Client {
	return &Client{
		url:    strings.TrimRight(apiURL, "/") + "/chat/completions",
		apiKey: apiKey,
		model:  model,
		// Extraction runs in the add_drawer write path; keep the ceiling high
		// enough for small local models but bounded so a hung server cannot
		// block a request forever.
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	Stream         bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"` // "json_object"
}

type chatResp struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete sends the messages and returns the assistant's reply text.
// When jsonMode is true it requests the provider's JSON object mode where
// supported (harmless on providers that ignore it).
func (c *Client) Complete(ctx context.Context, messages []Message, jsonMode bool) (string, error) {
	reqBody := chatReq{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0, // deterministic extraction
		Stream:      false,
	}
	if jsonMode {
		reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat API returned HTTP %d", resp.StatusCode)
	}

	var result chatResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("chat decode: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("chat API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("chat API returned no choices")
	}
	return result.Choices[0].Message.Content, nil
}
