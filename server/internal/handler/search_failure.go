package handler

import (
	"context"
	"errors"
	"net"
	"net/http"

	"mempalace/core/embed"
)

var errSearchQueryRequired = errors.New("query is required")

type searchFailure struct {
	HTTPStatus int
	Code       string
	Message    string
}

// classifySearchFailure determines the caller-safe failure class. An embedding
// provider error is not a malformed player query. Only actual invalid input
// gets HTTP 400. Do not expose upstream URLs, credentials or provider bodies.
func classifySearchFailure(err error) searchFailure {
	if errors.Is(err, errSearchQueryRequired) {
		return searchFailure{http.StatusBadRequest, "INVALID_SEARCH_QUERY", "query is required"}
	}
	if errors.Is(err, context.Canceled) {
		return searchFailure{http.StatusRequestTimeout, "SEARCH_CANCELED", "search request canceled"}
	}
	var embedding *embed.Failure
	if errors.As(err, &embedding) {
		var netError net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netError) && netError.Timeout()) {
			return searchFailure{http.StatusGatewayTimeout, "EMBEDDING_TIMEOUT", "embedding provider timed out"}
		}
		switch embedding.Kind {
		case embed.FailureHTTPStatus:
			if embedding.HTTPStatus == http.StatusUnauthorized || embedding.HTTPStatus == http.StatusForbidden {
				return searchFailure{http.StatusBadGateway, "EMBEDDING_UPSTREAM_AUTH_FAILED", "embedding provider authentication failed"}
			}
			if embedding.HTTPStatus == http.StatusBadRequest {
				return searchFailure{http.StatusBadGateway, "EMBEDDING_UPSTREAM_REJECTED", "embedding provider rejected the embedding request"}
			}
			if embedding.HTTPStatus == http.StatusTooManyRequests {
				return searchFailure{http.StatusServiceUnavailable, "EMBEDDING_RATE_LIMITED", "embedding provider temporarily unavailable"}
			}
			if embedding.HTTPStatus == http.StatusGatewayTimeout {
				return searchFailure{http.StatusGatewayTimeout, "EMBEDDING_TIMEOUT", "embedding provider timed out"}
			}
			if embedding.HTTPStatus == http.StatusServiceUnavailable {
				return searchFailure{http.StatusServiceUnavailable, "EMBEDDING_UNAVAILABLE", "embedding provider temporarily unavailable"}
			}
			return searchFailure{http.StatusBadGateway, "EMBEDDING_UPSTREAM_FAILED", "embedding provider returned an unsuccessful response"}
		case embed.FailureTransport:
			return searchFailure{http.StatusBadGateway, "EMBEDDING_TRANSPORT_FAILED", "embedding provider connection failed"}
		case embed.FailureResponse:
			return searchFailure{http.StatusBadGateway, "EMBEDDING_RESPONSE_INVALID", "embedding provider returned an invalid response"}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return searchFailure{http.StatusGatewayTimeout, "SEARCH_TIMEOUT", "search exceeded its deadline"}
	}
	return searchFailure{http.StatusInternalServerError, "SEARCH_INTERNAL_FAILED", "search could not be completed"}
}
