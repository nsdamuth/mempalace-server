package auth

import (
	"net/http"
	"strings"
)

// Bearer returns an HTTP middleware that enforces Authorization: Bearer <token>.
// The health endpoint is exempted so Kubernetes liveness probes work without auth.
func Bearer(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health check — no auth required
		if r.URL.Path == "/mp/mcp/health" {
			next.ServeHTTP(w, r)
			return
		}

		if apiKey == "" {
			jsonError(w, http.StatusServiceUnavailable, "server misconfigured: MCP_API_KEY not set")
			return
		}
		token, ok := bearerToken(r)
		if !ok || token != apiKey {
			w.Header().Set("WWW-Authenticate", "Bearer")
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	return strings.TrimPrefix(h, "Bearer "), true
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write([]byte(`{"error":"` + msg + `"}`)) //nolint:errcheck
}
