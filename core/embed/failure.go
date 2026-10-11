package embed

import "fmt"

// Failure classifies embedding dependency errors without putting upstream
// URLs, credentials, or raw provider responses into public error messages.
// The underlying transport error remains available for errors.Is/As, allowing
// caller cancellation and deadline errors to retain their identities.
type FailureKind string

const (
	FailureTransport  FailureKind = "transport"
	FailureHTTPStatus FailureKind = "http_status"
	FailureResponse   FailureKind = "response"
)

type Failure struct {
	Kind       FailureKind
	HTTPStatus int
	Cause      error
}

func (e *Failure) Error() string {
	if e == nil {
		return "embedding failed"
	}
	switch e.Kind {
	case FailureHTTPStatus:
		return fmt.Sprintf("embedding upstream returned HTTP %d", e.HTTPStatus)
	case FailureResponse:
		return "embedding upstream returned an invalid response"
	default:
		return "embedding transport failed"
	}
}

func (e *Failure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
