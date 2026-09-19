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

	// ErrUnavailable means the service could not be reached, or failed before
	// producing an answer. Transient, and retried automatically.
	//
	// Every transport failure matches this: a name that does not resolve, a
	// refused connection, a TLS failure, a reset, or an attempt that ran out
	// of time. So does every 5xx.
	ErrUnavailable = errors.New("jev: service unavailable")

	// ErrMalformedResponse means the reply did not answer what was asked.
	// The service itself cannot produce this, so it means something between
	// here and there rewrote the body.
	ErrMalformedResponse = errors.New("jev: malformed response")
)

// TransportError is a failure that happened before any reply arrived.
//
// It is kept distinct from a refusal because the two call for different
// things: a refusal is the service telling you something about your request,
// while this is the request never having been answered. Conflating them means
// a typo in a base URL reports itself as the service being overloaded, and the
// underlying *url.Error is unreachable through errors.As.
type TransportError struct {
	// Err is the failure as the HTTP client reported it.
	Err error
	// Timeout says the attempt exceeded the client's per-attempt budget
	// rather than failing outright. The caller's own context was still live.
	Timeout bool
}

func (e *TransportError) Error() string {
	if e.Timeout {
		return "jev: transport: attempt timed out: " + e.Err.Error()
	}
	return "jev: transport: " + e.Err.Error()
}

// Unwrap exposes the underlying failure, so errors.As reaches *url.Error and
// the net package's own types.
func (e *TransportError) Unwrap() error { return e.Err }

// Is reports a transport failure as ErrUnavailable.
func (e *TransportError) Is(target error) bool { return target == ErrUnavailable }

// Retryable is always true: nothing was answered, and the service has no side
// effects, so asking again is safe.
func (e *TransportError) Retryable() bool { return true }

// retryable is what Ask tests to decide whether to try again.
type retryable interface{ Retryable() bool }

// Violation is one schema failure from a validation refusal.
//
// The service validates in two places: its own checks refuse with a message,
// while the schema layer beneath refuses with a list of exactly which fields
// were wrong. Without this the second kind arrives as raw JSON in Message.
type Violation struct {
	// Loc is the path to the offending field, e.g. ["body", "state"].
	Loc []string
	// Msg is the explanation, e.g. "Field required".
	Msg string
	// Type is the machine-readable kind, e.g. "missing".
	Type string
}

func (v Violation) String() string {
	if len(v.Loc) == 0 {
		return v.Msg
	}
	return strings.Join(v.Loc, ".") + ": " + v.Msg
}

// APIError is a refusal from the service, carrying whatever detail came back.
type APIError struct {
	// StatusCode is the HTTP status.
	StatusCode int
	// Kind is the service's own error_type, when it sent one.
	Kind string
	// Message is the human-readable detail, when it sent one. Some refusals
	// carry a kind and nothing else.
	Message string
	// Violations lists the individual schema failures, for a refusal that
	// came from the schema layer rather than the service's own checks.
	Violations []Violation
	// RequestID is the service's identifier for this exchange. It is the one
	// thing the vendor can look up, so it belongs on every failure.
	RequestID string
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
//
// Derived from the status and kind when it was not set, so an APIError built
// by a caller — in a test, or a fake — behaves like one this package parsed.
// A public type that only works when this package constructed it is a trap.
func (e *APIError) Unwrap() error {
	if e.sentinel != nil {
		return e.sentinel
	}
	return sentinelFor(e.StatusCode, e.Kind)
}

// Retryable reports whether sending the same request again could succeed.
func (e *APIError) Retryable() bool {
	kind := e.Unwrap()
	return errors.Is(kind, ErrRateLimited) || errors.Is(kind, ErrOverloaded)
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
		default:
			// The schema layer refuses with a list of what was wrong, rather
			// than a sentence. Without this the raw JSON became the message.
			var raw []struct {
				Loc  []any  `json:"loc"`
				Msg  string `json:"msg"`
				Type string `json:"type"`
			}
			if json.Unmarshal(envelope.Detail, &raw) == nil && len(raw) > 0 {
				parts := make([]string, 0, len(raw))
				for _, item := range raw {
					v := Violation{Msg: item.Msg, Type: item.Type}
					for _, segment := range item.Loc {
						v.Loc = append(v.Loc, fmt.Sprint(segment))
					}
					e.Violations = append(e.Violations, v)
					parts = append(parts, v.String())
				}
				e.Message = strings.Join(parts, "; ")
			}
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
