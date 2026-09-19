package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Sentinel errors for the conditions a caller acts on differently.
var (
	// ErrUnauthorized means the key was missing, malformed or rejected.
	// Retrying will not help; the run should stop and say so.
	ErrUnauthorized = errors.New("jev: not authorized")

	// ErrInvalidRequest means the request was understood and refused. The
	// question set needs changing, so retrying it unchanged is pointless.
	ErrInvalidRequest = errors.New("jev: invalid request")

	// ErrTooLarge means the state plus the longest question exceeded what the
	// service accepts. A caller that trims its own input can catch this and
	// trim further rather than giving up.
	ErrTooLarge = errors.New("jev: request too large")

	// ErrRateLimited means the account's request or token allowance is spent.
	// Transient, and retried automatically unless retries are exhausted.
	ErrRateLimited = errors.New("jev: rate limited")

	// ErrOverloaded means the service is saturated. Transient.
	ErrOverloaded = errors.New("jev: service overloaded")
)

// APIError is a refusal from the service, carrying whatever detail came back.
type APIError struct {
	// StatusCode is the HTTP status.
	StatusCode int
	// Kind is the service's own error_type, when it sent one.
	Kind string
	// Message is the human-readable detail, when it sent one. Some refusals
	// carry a kind and nothing else.
	Message string
	// kind of failure this maps onto, for errors.Is.
	sentinel error
	// retryAfter is how long the service asked us to wait, if it said.
	retryAfter time.Duration
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("jev: ")
	switch {
	case e.Message != "":
		b.WriteString(e.Message)
	case e.Kind != "":
		b.WriteString(e.Kind)
	default:
		b.WriteString(http.StatusText(e.StatusCode))
	}
	fmt.Fprintf(&b, " (http %d", e.StatusCode)
	if e.Kind != "" && e.Message != "" {
		fmt.Fprintf(&b, ", %s", e.Kind)
	}
	b.WriteString(")")
	return b.String()
}

// Unwrap lets errors.Is match the sentinel for this kind of failure.
func (e *APIError) Unwrap() error { return e.sentinel }

// Retryable reports whether sending the same request again could succeed.
func (e *APIError) Retryable() bool {
	return errors.Is(e.sentinel, ErrRateLimited) || errors.Is(e.sentinel, ErrOverloaded)
}

// errorDetail is the reply body of a refusal.
//
// The detail field is a string for some refusals and an object for others, so
// it is decoded loosely and normalized here.
type errorDetail struct {
	Detail json.RawMessage `json:"detail"`
}

type errorObject struct {
	Kind    string `json:"error_type"`
	Message string `json:"message"`
}

// parseAPIError turns a refusal into an APIError, falling back to the status
// alone when the body is not the shape documented.
func parseAPIError(status int, body []byte) *APIError {
	e := &APIError{StatusCode: status}

	var envelope errorDetail
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Detail) > 0 {
		var obj errorObject
		var text string
		switch {
		case json.Unmarshal(envelope.Detail, &obj) == nil && (obj.Kind != "" || obj.Message != ""):
			e.Kind, e.Message = obj.Kind, obj.Message
		case json.Unmarshal(envelope.Detail, &text) == nil:
			e.Message = text
		}
	}
	if e.Kind == "" && e.Message == "" {
		e.Message = strings.TrimSpace(string(body))
	}

	e.sentinel = sentinelFor(status, e.Kind)
	return e
}

// sentinelFor maps a refusal onto the error a caller can test for.
//
// The size limit arrives as an ordinary bad request distinguished only by its
// error_type, but it is the one refusal a caller can do something about, so it
// gets its own sentinel.
func sentinelFor(status int, kind string) error {
	if kind == "max_tokens_exceeded" {
		return ErrTooLarge
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case 529:
		return ErrOverloaded
	}
	if status >= 500 {
		return ErrOverloaded
	}
	return ErrInvalidRequest
}
