package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mempalace/core/config"
	"mempalace/core/embed"
	"mempalace/server/internal/auth"
)

func TestSearchFailureClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		http int
		code string
	}{
		{"invalid query", errSearchQueryRequired, 400, "INVALID_SEARCH_QUERY"},
		{"caller canceled", context.Canceled, 408, "SEARCH_CANCELED"},
		{"embed deadline", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureTransport, Cause: context.DeadlineExceeded}), 504, "EMBEDDING_TIMEOUT"},
		{"embed network timeout", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureTransport, Cause: &net.DNSError{IsTimeout: true}}), 504, "EMBEDDING_TIMEOUT"},
		{"embed 401", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureHTTPStatus, HTTPStatus: 401}), 502, "EMBEDDING_UPSTREAM_AUTH_FAILED"},
		{"embed 400", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureHTTPStatus, HTTPStatus: 400}), 502, "EMBEDDING_UPSTREAM_REJECTED"},
		{"embed 429", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureHTTPStatus, HTTPStatus: 429}), 503, "EMBEDDING_RATE_LIMITED"},
		{"embed 502", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureHTTPStatus, HTTPStatus: 502}), 502, "EMBEDDING_UPSTREAM_FAILED"},
		{"embed 503", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureHTTPStatus, HTTPStatus: 503}), 503, "EMBEDDING_UNAVAILABLE"},
		{"embed 504", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureHTTPStatus, HTTPStatus: 504}), 504, "EMBEDDING_TIMEOUT"},
		{"embed invalid response", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureResponse}), 502, "EMBEDDING_RESPONSE_INVALID"},
		{"embed connection", fmt.Errorf("embed query: %w", &embed.Failure{Kind: embed.FailureTransport, Cause: errors.New("upstream unavailable")}), 502, "EMBEDDING_TRANSPORT_FAILED"},
		{"storage deadline", context.DeadlineExceeded, 504, "SEARCH_TIMEOUT"},
		{"internal storage error", errors.New("secret: db connection string"), 500, "SEARCH_INTERNAL_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := classifySearchFailure(tc.err)
			if failure.HTTPStatus != tc.http || failure.Code != tc.code {
				t.Fatalf("got status %d code %s, want %d/%s", failure.HTTPStatus, failure.Code, tc.http, tc.code)
			}
			if strings.Contains(strings.ToLower(failure.Message), "secret") || strings.Contains(failure.Message, "http://") {
				t.Fatalf("unsanitized error message: %s", failure.Message)
			}
		})
	}
}

func TestSearchHTTPEmbeddingFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name         string
		providerHTTP int
		clientHTTP   int
		clientCode   string
	}{
		{"rate limited", 429, 503, "EMBEDDING_RATE_LIMITED"},
		{"provider authentication", 401, 502, "EMBEDDING_UPSTREAM_AUTH_FAILED"},
		{"upstream unavailable", 502, 502, "EMBEDDING_UPSTREAM_FAILED"},
		{"upstream timeout", 504, 504, "EMBEDDING_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tc.providerHTTP)
				_, _ = w.Write([]byte(`{"error":"PRIVATE_PROVIDER_DETAIL"}`))
			}))
			defer upstream.Close()
			s := New(nil, nil, nil, nil, nil, nil, nil, embed.NewClient(upstream.URL+"/v1", "", "fixture", 3), config.Config{})
			mux := http.NewServeMux()
			s.RegisterREST(mux)
			s.Register(mux)
			h := auth.Bearer("test-write-key", "test-read-key", mux)

			restReq := httptest.NewRequest(http.MethodPost, "/mp/api/v1/search", strings.NewReader(`{"query":"benign fixture","limit":1}`))
			restReq.Header.Set("Authorization", "Bearer test-read-key")
			restRec := httptest.NewRecorder()
			h.ServeHTTP(restRec, restReq)
			if restRec.Code != tc.clientHTTP {
				t.Fatalf("REST got status %d, want %d", restRec.Code, tc.clientHTTP)
			}
			var restPayload struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.Unmarshal(restRec.Body.Bytes(), &restPayload); err != nil {
				t.Fatal(err)
			}
			if restPayload.Code != tc.clientCode {
				t.Fatalf("REST code %s, want %s", restPayload.Code, tc.clientCode)
			}
			if bytes.Contains(restRec.Body.Bytes(), []byte("PRIVATE_PROVIDER_DETAIL")) {
				t.Fatal("REST leaked provider response")
			}

			mcpReq := httptest.NewRequest(http.MethodPost, "/mp/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mempalace_search","arguments":{"query":"benign fixture","limit":1}}}`))
			mcpReq.Header.Set("Authorization", "Bearer test-read-key")
			mcpRec := httptest.NewRecorder()
			h.ServeHTTP(mcpRec, mcpReq)
			if mcpRec.Code != http.StatusOK {
				t.Fatalf("MCP HTTP status %d", mcpRec.Code)
			}
			var mcpPayload rpcResponse
			if err := json.Unmarshal(mcpRec.Body.Bytes(), &mcpPayload); err != nil {
				t.Fatal(err)
			}
			if mcpPayload.Error == nil || !strings.Contains(mcpPayload.Error.Message, tc.clientCode) {
				t.Fatalf("MCP error %v, expected %s", mcpPayload.Error, tc.clientCode)
			}
			if bytes.Contains(mcpRec.Body.Bytes(), []byte("PRIVATE_PROVIDER_DETAIL")) {
				t.Fatal("MCP leaked provider response")
			}
		})
	}
}
