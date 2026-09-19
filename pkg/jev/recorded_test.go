package jev

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/jev/jevtest"
)

// TestRecordedErrorShapes checks every refusal the service actually produced.
//
// Refusals arrive in three shapes, and all three are real: the service's own
// checks answer with a sentence or with an object carrying an error_type,
// while the schema layer beneath answers with a list of exactly which fields
// were wrong. A hand-written fake would have encoded whichever one its author
// happened to hit.
//
// These are exercised against the parser rather than through Ask, because the
// client now refuses several of them locally -- a 256-option choice never
// reaches the wire -- and the recording is of what the service said when it
// did.
func TestRecordedErrorShapes(t *testing.T) {
	want := map[string]struct {
		sentinel error
		contains string
	}{
		"errors/400-object-unknown-model-01":       {ErrInvalidRequest, "jev-9.99.9"},
		"errors/400-object-unknown-type-01":        {ErrInvalidRequest, ""},
		"errors/400-string-empty-question-name-01": {ErrInvalidRequest, "empty"},
		"errors/400-string-too-many-levels-01":     {ErrInvalidRequest, "at most 10"},
		"errors/256-options-01":                    {ErrInvalidRequest, "at most 255"},
		"errors/401-invalid-key-01":                {ErrUnauthorized, ""},
		"errors/401-missing-key-01":                {ErrUnauthorized, ""},
		"errors/422-array-missing-state-01":        {ErrInvalidRequest, "state"},
		"errors/422-array-state-number-01":         {ErrInvalidRequest, ""},
		"errors/422-array-empty-questions-01":      {ErrInvalidRequest, ""},
	}

	checked := 0
	for _, f := range jevtest.Load(t, "errors") {
		expect, ok := want[f.Name]
		if !ok {
			continue
		}
		checked++
		t.Run(f.Name, func(t *testing.T) {
			e := parseAPIError(f.Response.Status, f.Response.Body)

			if !errors.Is(e, expect.sentinel) {
				t.Errorf("status %d: %v does not match %v", f.Response.Status, e, expect.sentinel)
			}
			if expect.contains != "" && !strings.Contains(e.Error(), expect.contains) {
				t.Errorf("message %q does not mention %q", e.Error(), expect.contains)
			}
			if f.Response.Status == 422 {
				if len(e.Violations) == 0 {
					t.Error("a schema refusal produced no violations")
				}
				if strings.Contains(e.Message, `"loc"`) {
					t.Errorf("raw JSON leaked into the message: %q", e.Message)
				}
			}
			if e.Message == "" && e.Kind == "" {
				t.Error("neither a message nor a kind was recovered")
			}
		})
	}
	if checked == 0 {
		t.Fatal("no recorded refusals were checked")
	}
}

// TestRecordedTooLargeCarriesNoMessage pins the one refusal a caller acts on.
//
// It is an ordinary bad request distinguished only by its error_type, and the
// service sends no message with it, so a parser that relies on the message
// would classify it as an unremarkable failure.
func TestRecordedTooLargeCarriesNoMessage(t *testing.T) {
	found := false
	for _, f := range jevtest.Load(t, "ceiling") {
		if f.Response.Status == http.StatusOK {
			continue
		}
		found = true
		e := parseAPIError(f.Response.Status, f.Response.Body)
		if !errors.Is(e, ErrTooLarge) {
			t.Errorf("%s: %v does not match ErrTooLarge", f.Name, e)
		}
		if e.Kind != "max_tokens_exceeded" {
			t.Errorf("%s: kind = %q", f.Name, e.Kind)
		}
	}
	if !found {
		t.Skip("no refusal recorded in the ceiling group")
	}
}

// TestRecorderServesRecordedExchanges exercises the replay mechanism itself:
// a request identical to a recorded one gets that recording back.
func TestRecorderServesRecordedExchanges(t *testing.T) {
	rec := jevtest.NewRecorder(t, "guardrail", jevtest.AllowUnused())

	for _, f := range jevtest.Load(t, "guardrail") {
		req, err := http.NewRequest(f.Request.Method,
			"https://api.typesafe.ai"+f.Request.Path,
			bytes.NewReader(f.Request.Body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rec.RoundTrip(req)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if resp.StatusCode != f.Response.Status {
			t.Errorf("%s: status %d, want %d", f.Name, resp.StatusCode, f.Response.Status)
		}
		if got := resp.Header.Get("X-Typesafe-Request-Id"); got == "" {
			t.Errorf("%s: the request id header was not replayed", f.Name)
		}
		_ = resp.Body.Close()
	}
}

// TestRecorderRefusesAnUnrecordedRequest is the property that keeps replay
// honest: no fallback to the network, and a loud failure instead.
func TestRecorderRefusesAnUnrecordedRequest(t *testing.T) {
	fake := &recordingT{TB: t}
	rec := jevtest.NewRecorder(fake, "guardrail", jevtest.AllowUnused())

	req, err := http.NewRequest(http.MethodPost,
		"https://api.typesafe.ai/v1/systemone",
		strings.NewReader(`{"model":"never-recorded","state":"x","questions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec.RoundTrip(req); err == nil {
		t.Fatal("an unrecorded request was served")
	}
	if !fake.failed {
		t.Error("an unrecorded request did not fail the test")
	}
}

// TestCorpusKeysAgree checks that this package computes the same key the
// recorder wrote, which is what lets a corpus recorded by one tool be
// replayed by another.
func TestCorpusKeysAgree(t *testing.T) {
	groups := []string{"errors", "guardrail", "jitter", "quirks", "speaker-identity"}
	for _, group := range groups {
		for _, f := range jevtest.Load(t, group) {
			if f.Key == "" {
				continue
			}
			got, err := jevtest.RequestKey(f.Request.Method, f.Request.Path, f.Request.Body)
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			if got != f.Key {
				t.Errorf("%s: key %s, recorded as %s", f.Name, got, f.Key)
			}
		}
	}
}

// TestCorpusProvenance fails when the corpus and the pinned model drift
// apart, so that bumping DefaultModel without re-recording is noisy rather
// than silent.
func TestCorpusProvenance(t *testing.T) {
	groups := []string{"errors", "guardrail", "jitter", "quirks", "speaker-identity"}
	for _, group := range groups {
		for _, f := range jevtest.Load(t, group) {
			if f.ModelAnswered == "" {
				continue // a refusal answers with no model
			}
			if f.ModelAnswered != DefaultModel {
				t.Errorf("%s: answered by %q, but DefaultModel is %q; re-record",
					f.Name, f.ModelAnswered, DefaultModel)
			}
		}
	}
}

// recordingT notes whether a failure was reported, without failing the real
// test.
type recordingT struct {
	testing.TB
	failed bool
}

func (r *recordingT) Error(...any)          { r.failed = true }
func (r *recordingT) Errorf(string, ...any) { r.failed = true }
func (r *recordingT) Helper()               {}
func (r *recordingT) Cleanup(func())        {}
