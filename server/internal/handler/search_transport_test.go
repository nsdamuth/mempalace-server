package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mempalace/core/config"
	"mempalace/core/embed"
	"mempalace/server/internal/auth"
)

// The embedding upstream blocks until its HTTP context is canceled. This test
// uses no actual database, credentials, provider, or persisted Memory Palace data.
func TestSearchHTTPTransportPropagatesCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
	}{
		{
			name: "REST",
			path: "/mp/api/v1/search",
			body: `{"query":"non-sensitive-cancel-fixture","limit":1}`,
		},
		{
			name: "MCP",
			path: "/mp/mcp",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mempalace_search","arguments":{"query":"non-sensitive-cancel-fixture","limit":1}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			upstreamCanceled := make(chan struct{})
			releaseUpstream := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" {
					t.Errorf("unexpected upstream path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				// For HTTP/1.1, consume the POST body before waiting for the
				// transport disconnect; otherwise net/http may not detect it.
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Errorf("read mock embedding request: %v", err)
					return
				}
				close(entered)
				select {
				case <-r.Context().Done():
					close(upstreamCanceled)
				case <-releaseUpstream:
					// Ensure test cleanup cannot hang if cancellation regresses.
				}
			}))
			t.Cleanup(func() {
				close(releaseUpstream)
				upstream.Close()
			})

			client := embed.NewClient(upstream.URL+"/v1", "", "mock", 3)
			s := New(nil, nil, nil, nil, nil, nil, nil, client, config.Config{})
			mux := http.NewServeMux()
			s.Register(mux)
			s.RegisterREST(mux)
			h := auth.Bearer("test-write-key", "test-read-key", mux)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer test-read-key")
			req.Header.Set("Content-Type", "application/json")
			complete := make(chan struct{})
			go func() {
				defer close(complete)
				h.ServeHTTP(httptest.NewRecorder(), req)
			}()

			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("search never reached the mock embedding upstream")
			}
			cancel()

			select {
			case <-upstreamCanceled:
			case <-time.After(3 * time.Second):
				t.Fatal("embedding request continued after incoming HTTP cancellation")
			}
			select {
			case <-complete:
			case <-time.After(3 * time.Second):
				t.Fatal("search handler did not finish after cancellation")
			}
		})
	}
}
