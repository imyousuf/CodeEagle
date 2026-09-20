package jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/jev/jevtest"
)

// serveRecording answers every request with one exchange recorded from the
// live service, so the examples run offline and still show real answers.
//
// The figures printed below are bands rather than values for the same reason
// the tests assert bands: re-recording the corpus moves a probability by a few
// hundredths, and an example that printed two decimals would break each time.
func serveRecording(name string) *httptest.Server {
	raw, err := os.ReadFile("testdata/recordings/" + name + ".json")
	if err != nil {
		panic(err)
	}
	var f jevtest.Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		panic(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for name, value := range f.Response.Headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(f.Response.Status)
		_, _ = w.Write(f.Response.Body)
	}))
}

// ExampleClient_Ask asks three questions about one state in a single round
// trip, which is the shape most calls should take: every question is answered
// in the same pass, so the second and third cost almost nothing.
func ExampleClient_Ask() {
	srv := serveRecording("guardrail/force-push-fanout-01")
	defer srv.Close()

	client, err := jev.New("apikey_example", jev.WithBaseURL(srv.URL))
	if err != nil {
		panic(err)
	}

	state := map[string]string{
		"command":           "git push --force origin main",
		"working_directory": "/app/service-payments",
		"session_context":   "Refactoring migration files",
	}
	resp, err := client.Ask(context.Background(), state, jev.Questions{
		"is_destructive": jev.NoulWithCriteria(
			"Does this command irreversibly overwrite remote version control history or delete resources?",
			"Command forcibly rewrites history, drops data, or bypasses safety mechanisms",
			"Command performs safe local work or standard additive operations"),
		"execution_routing": jev.Choice("Which automated safety queue should process this command?",
			map[string]string{
				"auto_execute":          "Read-only inspection or standard non-destructive operations",
				"require_peer_approval": "Commands that alter shared environments or force-update remote history",
				"hard_block":            "Commands leaking credentials, writing to root, or destroying production state",
				"unrecognized":          "Syntactically ambiguous or obfuscated command",
			}),
		"blast_radius": jev.Score("Score the organizational and technical blast radius of this terminal command.",
			[]string{
				"Zero impact: Command is strictly read-only and local",
				"Moderate impact: Command alters local repository state or unmerged branch history",
				"Severe impact: Command mutates canonical production history or core system state",
			}),
	})
	if err != nil {
		panic(err)
	}

	destructive, _ := resp.Answers.Noul("is_destructive")
	route, confidence, _ := resp.Answers.Choice("execution_routing")
	blast, _, _ := resp.Answers.Score("blast_radius")

	fmt.Println("destructive:", destructive >= 0.8)
	fmt.Println("route:", route, "confident:", confidence >= 0.85)
	fmt.Println("blast radius at or above the middle level:", blast >= 1.0)
	// Output:
	// destructive: true
	// route: require_peer_approval confident: true
	// blast radius at or above the middle level: true
}

// ExampleChoice identifies a voice in a diarized transcript, and shows the
// answer that matters most: declining. The recording is of a speaker who only
// ever says "Mm-hmm", and the model correctly refuses to name them.
func ExampleChoice() {
	srv := serveRecording("speaker-identity/mmhmm-only-declined-01")
	defer srv.Close()

	client, err := jev.New("apikey_example", jev.WithBaseURL(srv.URL))
	if err != nil {
		panic(err)
	}

	// Recorded against the full transcript in testdata; abbreviated here.
	state := map[string]string{
		"meeting": "Gateway sync",
		"transcript": "Person 1 (00:06): Hi all, Kevin here.\n" +
			"Person 3 (01:11): Mm-hmm.\n" +
			"Person 3 (01:44): Yeah.\n",
	}
	// Always offer a way to decline. Without one the model must spread its
	// belief across names it has already rejected, and the confidence figure
	// stops meaning anything.
	options := map[string]string{
		"Kevin Mitchell": `The transcript establishes that "Person 3" is Kevin Mitchell`,
		"Priya Nair":     `The transcript establishes that "Person 3" is Priya Nair`,
		"Omar Haddad":    `The transcript establishes that "Person 3" is Omar Haddad`,
		"unresolved":     `The transcript does not establish who "Person 3" is`,
	}
	resp, err := client.Ask(context.Background(), state, jev.Questions{
		"speaker_2": jev.Choice(`Which person is the speaker labelled "Person 3"? `+
			`Answer "unresolved" unless the transcript itself establishes who they are.`, options),
	})
	if err != nil {
		panic(err)
	}

	who, confidence, _ := resp.Answers.Choice("speaker_2")
	fmt.Println("speaker:", who)
	fmt.Println("clears a 0.70 gate:", confidence >= 0.70)
	// Output:
	// speaker: unresolved
	// clears a 0.70 gate: true
}

// ExampleScore rates a diff against a rubric whose levels describe concrete
// situations. The answer is continuous — 3.91 on this recording — so the
// nearest level is read off by rounding.
func ExampleScore() {
	srv := serveRecording("code-review/sql-injection-fanout-01")
	defer srv.Close()

	client, err := jev.New("apikey_example", jev.WithBaseURL(srv.URL))
	if err != nil {
		panic(err)
	}

	// Recorded against the full diff in testdata; abbreviated here.
	review := map[string]any{
		"file_path": "internal/api/users.go",
		"diff": `+	q := r.URL.Query().Get("q")
+	query := fmt.Sprintf("SELECT id, name, email FROM users WHERE name LIKE '%%%s%%'", q)
+	rows, err := s.db.QueryContext(r.Context(), query)`,
		"author":         "d.alvarez",
		"lines_modified": 24,
	}
	levels := []string{
		"Trivial: a rename or comment change with no behavioural effect",
		"Small: a local logic change covered by existing tests",
		"Moderate: new behaviour that needs new tests and one reviewer familiar with the area",
		"Large: touches a shared boundary such as a database or public API and needs cross-team review",
		"Critical: changes security-sensitive or data-integrity code and needs a security review before merge",
	}
	resp, err := client.Ask(context.Background(), review, jev.Questions{
		"review_complexity": jev.Score("Rate the review effort this change needs.", levels),
	})
	if err != nil {
		panic(err)
	}

	score, confidence, _ := resp.Answers.Score("review_complexity")
	nearest := int(math.Round(score))
	fmt.Println("nearest level:", levels[nearest])
	fmt.Println("top of the rubric:", nearest == len(levels)-1, "confident:", confidence >= 0.85)
	// Output:
	// nearest level: Critical: changes security-sensitive or data-integrity code and needs a security review before merge
	// top of the rubric: true confident: true
}

// Example_tooLarge shows the one refusal a caller can do something about. The
// service never truncates: a state past its ceiling is refused outright, with
// an error_type and no message, and the recording is of exactly that.
func Example_tooLarge() {
	srv := serveRecording("ceiling/just-over-refused-01")
	defer srv.Close()

	client, err := jev.New("apikey_example", jev.WithBaseURL(srv.URL))
	if err != nil {
		panic(err)
	}

	// Recorded against 108,593 characters of transcript; abbreviated here.
	state := map[string]string{"transcript": "You (00:00): Okay, I think we're recording. ..."}
	_, err = client.Ask(context.Background(), state, jev.Questions{
		"speaker_0": jev.Noul("Is the first speaker the host?"),
	})

	var apiErr *jev.APIError
	fmt.Println("too large:", errors.Is(err, jev.ErrTooLarge))
	fmt.Println("worth retrying unchanged:", errors.As(err, &apiErr) && apiErr.Retryable())
	fmt.Println("kind:", apiErr.Kind)
	fmt.Println("request id recorded:", apiErr.RequestID != "")
	// Output:
	// too large: true
	// worth retrying unchanged: false
	// kind: max_tokens_exceeded
	// request id recorded: true
}
