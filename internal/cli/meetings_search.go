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

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/transcript"
)

// --- search ---

func newMeetingsSearchCmd() *cobra.Command {
	var (
		person string
		since  string
		only   string
		limit  int
		asJSON bool
		full   bool
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

Nothing here calls a model; it reads the graph and is fast and free.

Examples:
  codeeagle meetings search AGI
  codeeagle meetings search "Okta CIAM" --full
  codeeagle meetings search pricing --person Kevin
  codeeagle meetings search --only decision --since 2026-08-19
  codeeagle meetings search --only follow-up --person Mona --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				q := transcript.Query{
					Text:   strings.Join(args, " "),
					Person: person,
					Only:   transcript.MatchKind(only),
					Limit:  limit,
				}
				if since != "" {
					cutoff, err := time.Parse("2006-01-02", since)
					if err != nil {
						return fmt.Errorf("--since must be YYYY-MM-DD: %w", err)
					}
					q.Since = cutoff
				}

				found, err := transcript.FindMeetings(ctx, store, q)
				if err != nil {
					return err
				}

				out := cmd.OutOrStdout()
				if asJSON {
					return json.NewEncoder(out).Encode(searchToJSON(ctx, store, q, found))
				}
				printSearch(ctx, store, out, q, found, full)
				return nil
			})
		},
	}

	cmd.Flags().StringVar(&person, "person", "", "only meetings this person attended")
	cmd.Flags().StringVar(&since, "since", "", "only meetings on or after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&only, "only", "",
		"only this kind of evidence: title, summary, topic, segment, decision, follow-up, mention, participant")
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum meetings to show (0 for all)")
	cmd.Flags().BoolVar(&full, "full", false, "print matched summaries and quotes in full")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// printSearch writes the hits with the evidence for each.
func printSearch(ctx context.Context, store graph.Store, out io.Writer, q transcript.Query, found *transcript.Found, full bool) {
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
		switch found.Complete {
		case found.Total:
			fmt.Fprint(out, ", all of them")
		case 0:
			fmt.Fprint(out, ", none with every word")
		default:
			fmt.Fprintf(out, "; %d with every word, shown first", found.Complete)
		}
	}
	fmt.Fprintln(out, ".")

	for _, h := range found.Hits {
		m := h.Meeting
		fmt.Fprintf(out, "\n%s  %s  %s\n",
			m.UpdatedAt.Format("2006-01-02 15:04"), transcript.ShortID(m), m.Name)

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
	for _, mt := range h.Matches {
		switch mt.Kind {
		case transcript.MatchTitle, transcript.MatchSummary:
			where = append(where, string(mt.Kind))
		case transcript.MatchMention:
			where = append(where, "mentions "+mt.Label)
		case transcript.MatchParticipant:
			where = append(where, "participant "+mt.Label)
		case transcript.MatchTopic:
			topics = append(topics, mt.Label)
		}
	}
	if len(where) > 0 {
		fmt.Fprintf(out, "  matched in %s\n", strings.Join(where, ", "))
	}
	if len(topics) > 0 {
		fmt.Fprintf(out, "  topic      %s\n", strings.Join(topics, "; "))
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
	Query      string          `json:"query"`
	Terms      []string        `json:"terms"`
	Total      int             `json:"total"`
	Considered int             `json:"considered"`
	Hits       []searchHitJSON `json:"hits"`
}

type searchHitJSON struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Date         string            `json:"date"`
	Duration     string            `json:"duration"`
	Participants []string          `json:"participants"`
	Unidentified int               `json:"unidentified,omitempty"`
	Score        float64           `json:"score"`
	Phrase       bool              `json:"phrase,omitempty"`
	Matches      []searchMatchJSON `json:"matches"`
	Path         string            `json:"path,omitempty"`
}

type searchMatchJSON struct {
	Kind          string   `json:"kind"`
	Label         string   `json:"label"`
	Terms         []string `json:"terms,omitempty"`
	Phrase        bool     `json:"phrase,omitempty"`
	Start         string   `json:"start,omitempty"`
	End           string   `json:"end,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	Quote         string   `json:"quote,omitempty"`
	QuoteVerified *bool    `json:"quote_verified,omitempty"`
	Assignee      string   `json:"assignee,omitempty"`
	Due           string   `json:"due_date,omitempty"`
}

func searchToJSON(ctx context.Context, store graph.Store, q transcript.Query, found *transcript.Found) searchJSON {
	out := searchJSON{Query: q.Text, Terms: found.Terms, Total: found.Total, Considered: found.Considered,
		Hits: make([]searchHitJSON, 0, len(found.Hits))}
	for _, h := range found.Hits {
		m := h.Meeting
		hit := searchHitJSON{
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
			j := searchMatchJSON{Kind: string(mt.Kind), Label: mt.Label, Terms: mt.Terms, Phrase: mt.Phrase}
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
