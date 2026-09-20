package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/transcript"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// --- search ---

func newMeetingsSearchCmd() *cobra.Command {
	var (
		person   string
		since    string
		only     string
		breadth  string
		limit    int
		asJSON   bool
		full     bool
		noRerank bool
	)

	cmd := &cobra.Command{
		Use:   "search [words...]",
		Short: "Find what was said about something, by whom, and when",
		Long: `Search every meeting for some words and report, per meeting, where they
matched: the title, the topics it was filed under, what a topic segment said,
a decision and its quote, a follow-up and its owner, a participant's name.

One call answers "what did we discuss about X, with whom, and when". Words
match whole, without regard to case, and a word of four or more letters also
matches the start of a longer one — so "AGI" finds "AGI feasibility debate"
and not "messaging", and "auth" finds "authentication". An initialism and
its expansion count as one thing: "AGI artificial general intelligence"
matches a meeting that said either.

Meetings that contain the words as a phrase come first, then those covering
more of the query, rarer words counting for more, then the most recent.
Restricting to one kind of evidence with --only turns the search into a
listing: "--only decision --since 2026-08-01" with no words lists every
decision made since August.

A topic label matched by the words also reaches the topics judged to be
about the same thing (see "meetings relate"), so "AGI" finds the meeting
filed under "Recursive self improvement and model regression" as well as the
one filed under "AGI feasibility debate". --breadth sets how sure the
judgment must be and how many such steps to take: narrow follows one edge
at probability 0.8 or above, default one at 0.6, wide two at 0.5, none
follows none. A meeting reached this way is marked and ranks below one
whose own record matched the words.

Word matching cannot tell a meeting that discussed the subject from one that
mentioned it in passing. When transcripts.jev_api_key is configured, the
matched meetings are sent to the decision model in one request — the
question and each meeting's matching passages, one yes/no question per
meeting — and ordered by its probability that the meeting answers the
question, which is printed beside each. Measured on the real corpus this
moved the substantive discussion to the top for three queries in five and
never demoted a right answer; it costs about half a second and a fraction of
a cent. No generative model is ever called. --no-rerank keeps word-match
order, as does the absence of a key.

Examples:
  codeeagle meetings search AGI
  codeeagle meetings search "Okta CIAM" --full
  codeeagle meetings search pricing --person Kevin
  codeeagle meetings search --only decision --since 2026-08-19
  codeeagle meetings search --only follow-up --person Mona --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The decision model is optional: a key makes it available, and
			// its absence, or any failure, leaves word-match order in place.
			reranker, err := newSearchReranker(noRerank)
			if err != nil {
				return err
			}
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				q := transcript.Query{
					Text:    strings.Join(args, " "),
					Person:  person,
					Only:    transcript.MatchKind(only),
					Breadth: transcript.Breadth(breadth),
					Limit:   limit,
				}
				if since != "" {
					cutoff, err := time.Parse("2006-01-02", since)
					if err != nil {
						return fmt.Errorf("--since must be YYYY-MM-DD: %w", err)
					}
					q.Since = cutoff
				}
				// The model judges more than will be shown, so that a meeting
				// word matching ranked eleventh can still come first.
				rerank := reranker != nil && strings.TrimSpace(q.Text) != ""
				if rerank {
					q.Limit = 0
				}

				found, err := transcript.FindMeetings(ctx, store, q)
				if err != nil {
					return err
				}

				var ranking *transcript.Reranking
				if rerank {
					ranking, err = reranker.Rerank(ctx, q.Text, found.Hits)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Note: decision model unavailable, keeping word-match order: %v\n", err)
						ranking = nil
					}
					if limit > 0 && len(found.Hits) > limit {
						found.Hits = found.Hits[:limit]
					}
				}

				out := cmd.OutOrStdout()
				if asJSON {
					return json.NewEncoder(out).Encode(searchToJSON(ctx, store, q, found, ranking))
				}
				printSearch(ctx, store, out, q, found, ranking, full)
				return nil
			})
		},
	}

	cmd.Flags().StringVar(&person, "person", "", "only meetings this person attended")
	cmd.Flags().StringVar(&since, "since", "", "only meetings on or after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&only, "only", "",
		"only this kind of evidence: title, summary, topic, segment, decision, follow-up, mention, participant")
	cmd.Flags().StringVar(&breadth, "breadth", string(transcript.BreadthDefault),
		"how far a matched topic reaches through related topics: none, narrow, default, wide")
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum meetings to show (0 for all)")
	cmd.Flags().BoolVar(&full, "full", false, "print matched summaries and quotes in full")
	cmd.Flags().BoolVar(&noRerank, "no-rerank", false,
		"keep word-match order instead of asking the decision model (transcripts.jev_api_key) which meetings answer the question")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newSearchReranker builds the decision-model reranker when a key is
// configured and the caller has not opted out; nil otherwise.
func newSearchReranker(noRerank bool) (*transcript.Reranker, error) {
	if noRerank {
		return nil, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	tc := cfg.Transcripts
	key := strings.TrimSpace(tc.JevAPIKey)
	if key == "" {
		return nil, nil
	}
	var opts []jev.Option
	if tc.JevModel != "" {
		opts = append(opts, jev.WithModel(tc.JevModel))
	}
	client, err := jev.New(key, opts...)
	if err != nil {
		// A key that is present but unusable is a configuration mistake,
		// not a reason to pretend there is no key.
		return nil, fmt.Errorf("decision model: %w", err)
	}
	return transcript.NewReranker(client), nil
}

// printSearch writes the hits with the evidence for each.
func printSearch(ctx context.Context, store graph.Store, out io.Writer, q transcript.Query, found *transcript.Found, ranking *transcript.Reranking, full bool) {
	what := "match"
	if len(found.Terms) > 0 {
		what = fmt.Sprintf("mention %s", quoteAll(found.Terms))
	}
	if q.Only != "" {
		what += fmt.Sprintf(" in a %s", q.Only)
	}
	if found.Total == 0 {
		fmt.Fprintf(out, "None of %d meetings %s.\n", found.Considered, what)
		if len(found.Terms) > 0 {
			fmt.Fprintln(out, "Try fewer or shorter words, or `codeeagle rag` for a search by meaning.")
		}
		return
	}
	fmt.Fprintf(out, "%d of %d meetings %s", found.Total, found.Considered, what)
	if len(found.Terms) > 1 {
		// A hit needs only one of the words. Say how many have them all,
		// or "32 meetings mention X and Y" overstates what was found.
		switch {
		case found.Complete == found.Total:
			fmt.Fprint(out, ", all of them")
		case found.Complete == 0:
			fmt.Fprint(out, ", none with every word")
		case ranking != nil && ranking.Judged > 0:
			fmt.Fprintf(out, "; %d with every word", found.Complete)
		default:
			fmt.Fprintf(out, "; %d with every word, shown first", found.Complete)
		}
	}
	fmt.Fprintln(out, ".")
	if found.Expanded > 0 {
		// Say which hits the words never touched, so a reader can weigh
		// them, and how to see more or fewer.
		fmt.Fprintf(out, "%d reached only through related topics (--breadth %s; none disables, wide follows further).\n",
			found.Expanded, q.Breadth)
	}
	if ranking != nil && ranking.Judged > 0 {
		// Say what ordered the list and what it cost, so a reader knows
		// the probabilities are a model's judgment and not a word count.
		fmt.Fprintf(out, "Ordered by the decision model's probability that each answers the question "+
			"(%s judged %d of %d in %s, %d tokens; --no-rerank for word-match order).\n",
			ranking.Model, ranking.Judged, found.Total, ranking.Latency.Round(time.Millisecond), ranking.InputTokens)
	}

	for _, h := range found.Hits {
		m := h.Meeting
		p := ""
		if h.Judged {
			p = fmt.Sprintf("p=%.2f  ", h.Relevance)
		}
		// A meeting that contains every query word is worth marking when
		// the model has ranked it below ones that do not: the reader can
		// weigh a literal match against the model's judgment.
		every := ""
		if ranking != nil && ranking.Judged > 0 && len(found.Terms) > 1 && (h.Phrase || h.CoversAll(found.Terms)) {
			every = "  [every word]"
		}
		fmt.Fprintf(out, "\n%s  %s  %s%s%s\n",
			m.UpdatedAt.Format("2006-01-02 15:04"), transcript.ShortID(m), p, m.Name, every)

		with := strings.Join(h.Participants, ", ")
		if with == "" {
			with = "nobody identified"
		}
		if n := transcript.Unidentified(ctx, store, m.ID); n > 0 {
			with += fmt.Sprintf(" + %d unidentified", n)
		}
		fmt.Fprintf(out, "  with %s · %s\n", with, formatSeconds(m.Properties[graph.PropDuration]))

		printMatches(out, h, full)
	}

	fmt.Fprintln(out)
	noteTruncation(out, len(found.Hits), found.Total, "matching meetings", "--limit 0 for all")
	fmt.Fprintln(out, "Full record of any meeting: codeeagle meetings show <id>")
}

// printMatches lists where the query matched inside one meeting, with the
// matched text so the answer is in front of the reader, not a step away.
func printMatches(out io.Writer, h *transcript.Hit, full bool) {
	var where []string
	var topics []string
	var related []string
	for _, mt := range h.Matches {
		switch mt.Kind {
		case transcript.MatchTitle, transcript.MatchSummary:
			where = append(where, string(mt.Kind))
		case transcript.MatchMention:
			where = append(where, "mentions "+mt.Label)
		case transcript.MatchParticipant:
			where = append(where, "participant "+mt.Label)
		case transcript.MatchTopic:
			if mt.Via != "" {
				related = append(related, fmt.Sprintf("%s (p=%.2f)", mt.Via, mt.Probability))
			} else {
				topics = append(topics, mt.Label)
			}
		}
	}
	if len(where) > 0 {
		fmt.Fprintf(out, "  matched in %s\n", strings.Join(where, ", "))
	}
	if len(topics) > 0 {
		fmt.Fprintf(out, "  topic      %s\n", strings.Join(topics, "; "))
	}
	for _, r := range related {
		fmt.Fprintf(out, "  related    %s\n", r)
	}

	clip := func(s string, n int) string {
		if full {
			return s
		}
		return truncateText(strings.Join(strings.Fields(s), " "), n)
	}

	for _, mt := range h.Matches {
		n := mt.Node
		switch mt.Kind {
		case transcript.MatchSegment:
			start, _ := strconv.ParseFloat(n.Properties[graph.PropStartTime], 64)
			end, _ := strconv.ParseFloat(n.Properties[graph.PropEndTime], 64)
			fmt.Fprintf(out, "  segment    [%s–%s] %s\n",
				transcript.FormatTimestamp(start), transcript.FormatTimestamp(end), n.Name)
			if s := n.Properties[graph.PropSummary]; s != "" {
				fmt.Fprintf(out, "             %s\n", wrapText(clip(s, 300), 68, "             "))
			}
		case transcript.MatchDecision:
			mark := " "
			if n.Properties["quote_verified"] != "true" {
				// The quote is not in the transcript: say so where the
				// reader will see it.
				mark = "?"
			}
			fmt.Fprintf(out, "  decision %s %s\n", mark, wrapText(clip(n.Properties[graph.PropSummary], 300), 68, "             "))
			if qt := n.Properties[graph.PropQuote]; qt != "" {
				fmt.Fprintf(out, "             \"%s\"\n", clip(qt, 140))
			}
		case transcript.MatchFollowUp:
			who := n.Properties[graph.PropAssignee]
			if who == "" {
				who = "unassigned"
			}
			line := wrapText(clip(n.Properties[graph.PropSummary], 300), 68, "             ")
			fmt.Fprintf(out, "  follow-up  %s\n             — %s", line, who)
			if due := dueDate(n.Properties[graph.PropDueDate]); due != "" {
				fmt.Fprintf(out, ", due %s", due)
			}
			fmt.Fprintln(out)
		}
	}
}

// searchJSON is the machine-readable form of a search.
type searchJSON struct {
	Query      string   `json:"query"`
	Terms      []string `json:"terms"`
	Total      int      `json:"total"`
	Complete   int      `json:"complete"`
	Considered int      `json:"considered"`
	// Breadth is how far topic matches reached through related topics, and
	// Expanded how many hits were reached only that way.
	Breadth  string            `json:"breadth"`
	Expanded int               `json:"expanded,omitempty"`
	Reranked *searchRerankJSON `json:"reranked,omitempty"`
	Hits     []searchHitJSON   `json:"hits"`
}

// searchRerankJSON records that a decision model ordered the hits, and what
// that cost.
type searchRerankJSON struct {
	Model       string `json:"model"`
	Judged      int    `json:"judged"`
	InputTokens int    `json:"input_tokens"`
	LatencyMS   int64  `json:"latency_ms"`
}

type searchHitJSON struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Date         string   `json:"date"`
	Duration     string   `json:"duration"`
	Participants []string `json:"participants"`
	Unidentified int      `json:"unidentified,omitempty"`
	Score        float64  `json:"score"`
	Phrase       bool     `json:"phrase,omitempty"`
	// Relevance is the decision model's probability that the meeting
	// answers the question; present only when it was judged.
	Relevance *float64 `json:"relevance,omitempty"`
	// EveryWord reports that the meeting contains every query word.
	EveryWord bool              `json:"every_word,omitempty"`
	Matches   []searchMatchJSON `json:"matches"`
	Path      string            `json:"path,omitempty"`
}

type searchMatchJSON struct {
	Kind   string   `json:"kind"`
	Label  string   `json:"label"`
	Terms  []string `json:"terms,omitempty"`
	Phrase bool     `json:"phrase,omitempty"`
	// Via is the path of related topics from the label the words matched
	// to this one, and Probability the product of the edge probabilities
	// along it; both are present only for a match reached that way.
	Via           string  `json:"via,omitempty"`
	Probability   float64 `json:"probability,omitempty"`
	Start         string  `json:"start,omitempty"`
	End           string  `json:"end,omitempty"`
	Summary       string  `json:"summary,omitempty"`
	Quote         string  `json:"quote,omitempty"`
	QuoteVerified *bool   `json:"quote_verified,omitempty"`
	Assignee      string  `json:"assignee,omitempty"`
	Due           string  `json:"due_date,omitempty"`
}

func searchToJSON(ctx context.Context, store graph.Store, q transcript.Query, found *transcript.Found, ranking *transcript.Reranking) searchJSON {
	out := searchJSON{Query: q.Text, Terms: found.Terms, Total: found.Total, Complete: found.Complete,
		Considered: found.Considered, Breadth: string(q.Breadth), Expanded: found.Expanded,
		Hits: make([]searchHitJSON, 0, len(found.Hits))}
	if ranking != nil && ranking.Judged > 0 {
		out.Reranked = &searchRerankJSON{Model: ranking.Model, Judged: ranking.Judged,
			InputTokens: ranking.InputTokens, LatencyMS: ranking.Latency.Milliseconds()}
	}
	for _, h := range found.Hits {
		m := h.Meeting
		var relevance *float64
		if h.Judged {
			r := h.Relevance
			relevance = &r
		}
		hit := searchHitJSON{
			Relevance:    relevance,
			EveryWord:    len(found.Terms) > 0 && (h.Phrase || h.CoversAll(found.Terms)),
			ID:           m.QualifiedName,
			Title:        m.Name,
			Date:         m.UpdatedAt.Format(time.RFC3339),
			Duration:     formatSeconds(m.Properties[graph.PropDuration]),
			Participants: h.Participants,
			Unidentified: transcript.Unidentified(ctx, store, m.ID),
			Score:        h.Score,
			Phrase:       h.Phrase,
			Path:         m.FilePath,
			Matches:      make([]searchMatchJSON, 0, len(h.Matches)),
		}
		for _, mt := range h.Matches {
			j := searchMatchJSON{Kind: string(mt.Kind), Label: mt.Label, Terms: mt.Terms, Phrase: mt.Phrase,
				Via: mt.Via, Probability: mt.Probability}
			if n := mt.Node; n != nil {
				switch mt.Kind {
				case transcript.MatchSegment:
					start, _ := strconv.ParseFloat(n.Properties[graph.PropStartTime], 64)
					end, _ := strconv.ParseFloat(n.Properties[graph.PropEndTime], 64)
					j.Start, j.End = transcript.FormatTimestamp(start), transcript.FormatTimestamp(end)
					j.Summary = n.Properties[graph.PropSummary]
				case transcript.MatchDecision:
					j.Summary = n.Properties[graph.PropSummary]
					j.Quote = n.Properties[graph.PropQuote]
					verified := n.Properties["quote_verified"] == "true"
					j.QuoteVerified = &verified
				case transcript.MatchFollowUp:
					j.Summary = n.Properties[graph.PropSummary]
					j.Quote = n.Properties[graph.PropQuote]
					j.Assignee = n.Properties[graph.PropAssignee]
					j.Due = dueDate(n.Properties[graph.PropDueDate])
				}
			}
			hit.Matches = append(hit.Matches, j)
		}
		out.Hits = append(out.Hits, hit)
	}
	return out
}
