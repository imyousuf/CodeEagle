package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestQuestionValidation covers the shapes the service accepts but should not.
//
// A one-level rubric is the important case: the service answers it with score
// 0.0 at confidence 1.0, which a caller gating on confidence would read as
// certainty rather than as a malformed question.
func TestQuestionValidation(t *testing.T) {
	tests := []struct {
		name      string
		questions Questions
		wantErr   string
	}{
		{
			name:      "no questions",
			questions: Questions{},
			wantErr:   "no questions asked",
		},
		{
			name:      "noul with nothing to go on",
			questions: Questions{"q": {Type: TypeNoul}},
			wantErr:   "needs instructions or criteria",
		},
		{
			name:      "choice with no options",
			questions: Questions{"q": Choice("Which?", nil)},
			wantErr:   "needs options",
		},
		{
			name:      "choice option with no description",
			questions: Questions{"q": Choice("Which?", map[string]string{"a": ""})},
			wantErr:   `option "a" has no description`,
		},
		{
			name:      "score with one level",
			questions: Questions{"q": Score("How bad?", []string{"only one"})},
			wantErr:   "1 rubric levels, want 2 to 10",
		},
		{
			name: "score with eleven levels",
			questions: Questions{"q": Score("How bad?", []string{
				"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
			})},
			wantErr: "11 rubric levels, want 2 to 10",
		},
		{
			name:      "score with an empty level",
			questions: Questions{"q": Score("How bad?", []string{"fine", ""})},
			wantErr:   "rubric level 1 is empty",
		},
		{
			name:      "unknown type",
			questions: Questions{"q": {Type: "guess", Instructions: "?"}},
			wantErr:   `unknown type "guess"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.questions.validate()
			if err == nil {
				t.Fatalf("validate() accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestQuestionValidationAcceptsGoodQuestions guards against a validator so
// strict it rejects the shapes the service documents.
func TestQuestionValidationAcceptsGoodQuestions(t *testing.T) {
	questions := Questions{
		"plain":     Noul("Is the build broken?"),
		"explained": NoulWithCriteria("Is it transient?", "a timeout", "a failed assertion"),
		"choice":    Choice("Which subsystem?", map[string]string{"db": "schema", "ui": "rendering"}),
		"score":     Score("How risky?", []string{"safe", "needs review", "dangerous"}),
	}
	if err := questions.validate(); err != nil {
		t.Fatalf("validate() rejected valid questions: %v", err)
	}
}

// TestErrorParsing covers both refusal bodies the service actually sends.
func TestErrorParsing(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantIs      error
		wantMessage string
		retryable   bool
	}{
		{
			name:        "authentication, detail as an object",
			status:      401,
			body:        `{"detail":{"error_type":"authentication_error","message":"Cannot authenticate with the server."}}`,
			wantIs:      ErrUnauthorized,
			wantMessage: "Cannot authenticate",
		},
		{
			name:   "size limit, an object with no message",
			status: 400,
			body:   `{"detail":{"error_type":"max_tokens_exceeded"}}`,
			wantIs: ErrTooLarge,
		},
		{
			name:        "detail as a bare string",
			status:      400,
			body:        `{"detail":"Noul question must have criteria or instructions: q"}`,
			wantIs:      ErrInvalidRequest,
			wantMessage: "Noul question must have criteria",
		},
		{
			name:        "unknown model",
			status:      400,
			body:        `{"detail":{"error_type":"api_usage_error","message":"Unknown model: jev-9.99.9"}}`,
			wantIs:      ErrInvalidRequest,
			wantMessage: "Unknown model",
		},
		{
			name:      "rate limited",
			status:    429,
			body:      `{"detail":"slow down"}`,
			wantIs:    ErrRateLimited,
			retryable: true,
		},
		{
			name:      "overloaded",
			status:    529,
			body:      `{}`,
			wantIs:    ErrOverloaded,
			retryable: true,
		},
		{
			name:      "a gateway failure with no JSON at all",
			status:    502,
			body:      `<html>bad gateway</html>`,
			wantIs:    ErrOverloaded,
			retryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseAPIError(tt.status, []byte(tt.body))
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("error is %v, want %v", err, tt.wantIs)
			}
			if tt.wantMessage != "" && !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMessage)
			}
			if err.Retryable() != tt.retryable {
				t.Errorf("Retryable() = %v, want %v", err.Retryable(), tt.retryable)
			}
		})
	}
}

// TestAskRetriesTransientFailures covers the retry policy: transient failures
// are retried, refusals are not.
func TestAskRetriesTransientFailures(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantAttempts int
	}{
		{"overload is retried", 529, `{}`, 3},
		{"rate limit is retried", 429, `{"detail":"slow down"}`, 3},
		{"a bad request is not", 400, `{"detail":"bad question"}`, 1},
		{"a rejected key is not", 401, `{"detail":{"error_type":"authentication_error"}}`, 1},
		{"too large is not", 400, `{"detail":{"error_type":"max_tokens_exceeded"}}`, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				// Ask to come back immediately, so the test does not sleep out
				// the real backoff.
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c, err := New("apikey_test", WithBaseURL(srv.URL), WithMaxRetries(2))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			if _, err := c.Ask(ctx, "state", Questions{"q": Noul("Is it?")}); err == nil {
				t.Fatal("Ask succeeded against a failing server")
			}
			if attempts != tt.wantAttempts {
				t.Errorf("made %d attempts, want %d", attempts, tt.wantAttempts)
			}
		})
	}
}

// TestAskDecodesAnswers covers reading each primitive back out.
func TestAskDecodesAnswers(t *testing.T) {
	const body = `{
	  "model": "jev-1.13.0",
	  "answers": {
	    "destructive": {"type": "noul", "noul": 0.87},
	    "routing": {"type": "choice", "choice": "review", "confidence": 0.9,
	                "probabilities": {"review": 0.9, "auto": 0.07, "block": 0.03}},
	    "blast": {"type": "score", "score": 1.99, "confidence": 0.99,
	              "probabilities": {"0": 0.0, "1": 0.01, "2": 0.99}}
	  },
	  "usage": {"input_tokens": 561, "output_tokens": 95}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer apikey_test" {
			t.Errorf("Authorization = %q", got)
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model != DefaultModel {
			t.Errorf("model = %q, want the pinned %q", req.Model, DefaultModel)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, err := New("apikey_test", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Ask(context.Background(), map[string]string{"cmd": "rm -rf /"}, Questions{
		"destructive": Noul("Is it destructive?"),
		"routing":     Choice("Where?", map[string]string{"review": "a", "auto": "b", "block": "c"}),
		"blast":       Score("How bad?", []string{"none", "some", "severe"}),
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if noul, err := resp.Answers.Noul("destructive"); err != nil || noul != 0.87 {
		t.Errorf("Noul = %v, %v; want 0.87", noul, err)
	}
	choice, confidence, err := resp.Answers.Choice("routing")
	if err != nil || choice != "review" || confidence != 0.9 {
		t.Errorf("Choice = %q/%v, %v; want review/0.9", choice, confidence, err)
	}
	score, _, err := resp.Answers.Score("blast")
	if err != nil || score != 1.99 {
		t.Errorf("Score = %v, %v; want 1.99", score, err)
	}
	if resp.Usage.InputTokens != 561 {
		t.Errorf("InputTokens = %d, want 561", resp.Usage.InputTokens)
	}

	// Asking for the wrong type must say so rather than return a zero value,
	// which would read as a confident "no".
	if _, err := resp.Answers.Noul("routing"); err == nil {
		t.Error("reading a choice as a noul succeeded; want an error")
	}
	if _, err := resp.Answers.Noul("nonexistent"); err == nil {
		t.Error("reading a missing answer succeeded; want an error")
	}

	ranked, err := resp.Answers.Ranked("routing")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 3 || ranked[0].Option != "review" || ranked[2].Option != "block" {
		t.Errorf("Ranked = %v, want review, auto, block", ranked)
	}
}

// TestAskValidatesBeforeSending covers not spending a round trip on a question
// the caller can be told about immediately.
func TestAskValidatesBeforeSending(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	c, err := New("apikey_test", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Ask(context.Background(), "state", Questions{
		"q": Score("How bad?", []string{"only one level"}),
	})
	if err == nil {
		t.Fatal("Ask accepted a one-level rubric")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("error is %v, want ErrInvalidRequest", err)
	}
	if called {
		t.Error("Ask sent an invalid request instead of refusing it locally")
	}
}

// TestAskHonoursContextCancellation covers giving up when the caller does.
func TestAskHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(529)
	}))
	defer srv.Close()

	c, err := New("apikey_test", WithBaseURL(srv.URL), WithMaxRetries(10))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, askErr := c.Ask(ctx, "state", Questions{"q": Noul("Is it?")})
		done <- askErr
	}()

	select {
	case askErr := <-done:
		if !errors.Is(askErr, context.DeadlineExceeded) {
			t.Errorf("error = %v, want the deadline", askErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ask ignored the cancelled context")
	}
}

// TestNewRequiresKey covers the one argument with no sensible default.
func TestNewRequiresKey(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New succeeded with no API key")
	}
}

// TestOptionsComposeInAnyOrder covers WithHTTPClient and WithTimeout together.
//
// An option that silently undid another depending on where it sat in the
// argument list would be a trap, and a lost timeout is the kind that only
// shows up as a hung request in production.
func TestOptionsComposeInAnyOrder(t *testing.T) {
	const want = 3 * time.Second

	tests := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{
			name: "timeout before client",
			opts: []Option{WithTimeout(want), WithHTTPClient(&http.Client{})},
			want: want,
		},
		{
			name: "client before timeout",
			opts: []Option{WithHTTPClient(&http.Client{}), WithTimeout(want)},
			want: want,
		},
		{
			name: "a client's own timeout is kept when none is given",
			opts: []Option{WithHTTPClient(&http.Client{Timeout: 7 * time.Second})},
			want: 7 * time.Second,
		},
		{
			name: "an explicit timeout overrides the client's",
			opts: []Option{WithHTTPClient(&http.Client{Timeout: 7 * time.Second}), WithTimeout(want)},
			want: want,
		},
		{
			name: "neither given falls back to the default",
			opts: nil,
			want: DefaultTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New("apikey_test", tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.httpClient.Timeout; got != tt.want {
				t.Errorf("timeout = %s, want %s", got, tt.want)
			}
		})
	}
}
