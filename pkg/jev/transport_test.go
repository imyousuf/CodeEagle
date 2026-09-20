package jev

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// TestStalledAttemptIsRetriedNotReportedAsTheCallersDeadline covers the case
// the per-attempt timeout exists for.
//
// A connection that stalls should be abandoned and tried again. Previously the
// attempt's own deadline was returned as context.DeadlineExceeded, which told
// the caller their deadline had passed when it had not, and ended the retry
// loop because it was not a retryable error.
func TestStalledAttemptIsRetriedNotReportedAsTheCallersDeadline(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			// Stall past the per-attempt budget, then give up on this one.
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Typesafe-Request-Id", "req_second_attempt")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10}}`))
	}))
	defer srv.Close()

	c, err := New("apikey_test", WithBaseURL(srv.URL),
		WithTimeout(150*time.Millisecond), WithMaxRetries(2))
	if err != nil {
		t.Fatal(err)
	}

	// The caller's own context is generous; only the attempt budget is tight.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := c.Ask(ctx, map[string]string{"s": "x"}, Questions{"q": Noul("Is it so?")})
	if err != nil {
		t.Fatalf("a stalled attempt was not retried: %v", err)
	}
	if got := attempts.Load(); got < 2 {
		t.Errorf("made %d attempts, want the first to have been retried", got)
	}
	if resp.RequestID != "req_second_attempt" {
		t.Errorf("request id = %q, want it captured from the headers", resp.RequestID)
	}
	if ctx.Err() != nil {
		t.Error("the caller's context was consumed")
	}
}

// TestCallersCancellationIsReportedAsTheirs is the other half: when the
// caller's own context really has ended, that is what comes back.
func TestCallersCancellationIsReportedAsTheirs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bounded, so closing the server cannot wait on this handler forever.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	c, err := New("apikey_test", WithBaseURL(srv.URL), WithMaxRetries(3))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = c.Ask(ctx, map[string]string{"s": "x"}, Questions{"q": Noul("Is it so?")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the caller's own deadline", err)
	}
}

// TestTransportFailureIsNotOverload checks that an unreachable host is
// reported as what it is.
//
// Mapping every transport failure onto ErrOverloaded meant a typo in a base
// URL claimed the service was saturated, and hid the underlying *url.Error
// from errors.As.
func TestTransportFailureIsNotOverload(t *testing.T) {
	// Port 0 is never listening.
	c, err := New("apikey_test", WithBaseURL("http://127.0.0.1:0/v1/systemone"),
		WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Ask(context.Background(), map[string]string{"s": "x"},
		Questions{"q": Noul("Is it so?")})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if errors.Is(err, ErrOverloaded) {
		t.Error("an unreachable host was reported as the service being overloaded")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want it to match ErrUnavailable", err)
	}

	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Error("the underlying *url.Error is unreachable through errors.As")
	}
	var transport *TransportError
	if !errors.As(err, &transport) {
		t.Error("expected a *TransportError")
	}
}

// TestUnmarshalableStateIsAnInvalidRequest covers a caller branching on
// ErrInvalidRequest: a state that cannot be encoded is their mistake.
func TestUnmarshalableStateIsAnInvalidRequest(t *testing.T) {
	c, err := New("apikey_test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Ask(context.Background(), map[string]any{"ch": make(chan int)},
		Questions{"q": Noul("Is it so?")})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("err = %v, want ErrInvalidRequest", err)
	}
}

// TestReplyMustAnswerWhatWasAsked guards against a gateway reshaping the body.
func TestReplyMustAnswerWhatWasAsked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Answers a different question than the one asked.
		_, _ = w.Write([]byte(`{"model":"m","answers":{"other":{"type":"noul","noul":0.5}},"usage":{}}`))
	}))
	defer srv.Close()

	c, err := New("apikey_test", WithBaseURL(srv.URL), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Ask(context.Background(), map[string]string{"s": "x"},
		Questions{"q": Noul("Is it so?")})
	if !errors.Is(err, ErrMalformedResponse) {
		t.Errorf("err = %v, want ErrMalformedResponse", err)
	}
}
