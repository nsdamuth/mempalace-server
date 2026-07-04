// Package embed provides an OpenAI-compatible HTTP embedding client.
// Compatible with: OpenAI API, Ollama (/v1/embeddings), LM Studio, LocalAI.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Client calls a /v1/embeddings endpoint and returns float32 vectors.
type Client struct {
	url    string
	apiKey string
	model  string
	dim    int
	http   *http.Client
}

func NewClient(apiURL, apiKey, model string, dim int) *Client {
	return &Client{
		url:    strings.TrimRight(apiURL, "/") + "/embeddings",
		apiKey: apiKey,
		model:  model,
		dim:    dim,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

type embedReq struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type embedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed returns one embedding vector per input text, preserving input order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedReq{Input: texts, Model: c.model})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed API returned HTTP %d", resp.StatusCode)
	}

	var result embedResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("embed API error: %s", result.Error.Message)
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Data))
	}

	// API may return results out of order — sort by index
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Index < result.Data[j].Index
	})

	out := make([][]float32, len(texts))
	for i, d := range result.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// EmbedOne embeds a single text.
func (c *Client) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// Dim returns the configured embedding dimension.
func (c *Client) Dim() int { return c.dim }
