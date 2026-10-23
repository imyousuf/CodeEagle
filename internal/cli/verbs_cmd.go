package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/agents"
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/relations"
)

func newFindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "find <words>",
		Short: "Find documents and meetings, one line each, with IDs to brief or show",
		Long: `Find the documents and meetings a query is about. Hits fold up to what can
be acted on: a section to its document, a document to what is known about it,
a segment or decision to its meeting. At most eight lines, each starting with
an ID that brief and show accept.`,
		Example: `  codeeagle find retry policy
  codeeagle find "sticky board"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := loadVerbs()
			if err != nil {
				return err
			}
			defer v.Close()
			out, err := v.Find(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newBriefCmd() *cobra.Command {
	var (
		budget  int
		since   string
		jsonOut bool
		all     bool
	)
	cmd := &cobra.Command{
		Use:   "brief <id | path | words>",
		Short: "Where a document stands: what holds now, what changed, what is open, with citations",
		Long: `Print the brief of a document and the meetings that discussed it: a headline,
what holds now, where the document and a later meeting disagree, what is
unresolved, and freshness flags. Every line ends with the evidence IDs it
rests on; show expands one.

Give an ID from find, a document's path as find prints it, or words. Words
that clearly name one document are briefed; words that match several list
them instead.

--budget caps the output in estimated tokens (default 1500, at most 2000);
lines are dropped in a fixed order to fit. --since adds what changed after a
date, a time, a duration (7d) or a revision printed in the header (@r3), and
puts it first.

--json prints the whole brief for a program instead: every line with the IDs
of its evidence, each piece of evidence with its quote and where it is
anchored, the meetings judged to discuss the document, and freshness.
--budget and --since do not apply to it. With --all instead of an ID, every
brief is printed, one JSON object per line.`,
		Example: `  codeeagle brief 3fa9c2
  codeeagle brief retry policy --budget 250
  codeeagle brief 3fa9c2 --since @r4`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && !jsonOut {
				return fmt.Errorf("--all needs --json")
			}
			if !all && len(args) == 0 {
				return fmt.Errorf("brief needs an ID or words, or --all --json")
			}
			v, err := loadVerbs()
			if err != nil {
				return err
			}
			defer v.Close()
			if all {
				return v.AllBriefsJSON(cmd.Context(), cmd.OutOrStdout())
			}
			arg := strings.Join(args, " ")
			var out string
			if jsonOut {
				out, err = v.BriefJSON(cmd.Context(), arg)
			} else {
				out, err = v.Brief(cmd.Context(), arg, budget, since)
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().IntVar(&budget, "budget", relations.DefaultBriefBudget, "most estimated tokens to print (max 2000)")
	cmd.Flags().StringVar(&since, "since", "", "also show what changed after this: a date, RFC 3339 time, 7d/36h/2w, or @rN")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the whole brief and its evidence as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "with --json, print every brief, one per line")
	return cmd
}

func newShowCmd() *cobra.Command {
	var from int
	cmd := &cobra.Command{
		Use:   "show <evidence-id | node-id-prefix>",
		Short: "Expand a citation to its source text, bounded",
		Long: `Show what an evidence ID from a brief rests on: a document quote as its
section on disk now (at most 60 lines; --from continues a long one), noting
if the section changed since it was quoted; a meeting quote as the turns
around it, with speakers named. Given a node ID prefix instead, show prints
that node. Never a whole file.`,
		Example: `  codeeagle show 9c4d10
  codeeagle show e1a2b3 --from 60`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := loadVerbs()
			if err != nil {
				return err
			}
			defer v.Close()
			out, err := v.Show(cmd.Context(), args[0], from)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().IntVar(&from, "from", 0, "skip this many lines into a long section")
	return cmd
}

func loadVerbs() (*verbs, error) {
	cfg, err := config.LoadForReading()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return newVerbs(cfg), nil
}

// verbTools exposes find, brief and show as agent and MCP tools. Their
// descriptions are short on purpose: every tool definition is sent with
// every request.
func verbTools(v *verbs) []agents.Tool {
	return []agents.Tool{
		&verbTool{
			name: "find",
			desc: "Find documents and meetings about something: one line each with an ID for brief; meeting lines also name the matching topic or decision, with an ID for show. Start here.",
			params: map[string]any{
				"query": map[string]any{"type": "string", "description": "What to look for, in words."},
			},
			required: []string{"query"},
			run: func(ctx context.Context, a map[string]any) (string, error) {
				return v.Find(ctx, str(a, "query"))
			},
		},
		&verbTool{
			name: "brief",
			desc: "Where a document stands (headline, what holds now, conflicts, open items, freshness; lines cite evidence IDs), or a meeting's summary, decisions, topics and follow-ups.",
			params: map[string]any{
				"target": map[string]any{"type": "string", "description": "An ID from find, several IDs separated by commas to brief them in one call, a document's path, or words naming the document."},
				"budget": map[string]any{"type": "integer", "description": "Most tokens to return (default 1500, max 2000)."},
				"since":  map[string]any{"type": "string", "description": "Also what changed after: a date, 7d, or @rN from a previous brief."},
			},
			required: []string{"target"},
			run: func(ctx context.Context, a map[string]any) (string, error) {
				return v.Brief(ctx, str(a, "target"), num(a, "budget"), str(a, "since"))
			},
		},
		&verbTool{
			name: "show",
			desc: "Expand an ID to its source, bounded: evidence to the quoted section or turns; a topic to what was said; a document or relation to its sections, with IDs.",
			params: map[string]any{
				"id":   map[string]any{"type": "string", "description": "An evidence ID, a section, document or relation ID, or another node ID prefix."},
				"from": map[string]any{"type": "integer", "description": "Skip this many lines into a long section."},
			},
			required: []string{"id"},
			run: func(ctx context.Context, a map[string]any) (string, error) {
				return v.Show(ctx, str(a, "id"), num(a, "from"))
			},
		},
	}
}

type verbTool struct {
	name, desc string
	params     map[string]any
	required   []string
	run        func(context.Context, map[string]any) (string, error)
}

func (t *verbTool) Name() string        { return t.name }
func (t *verbTool) Description() string { return t.desc }
func (t *verbTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": t.params, "required": t.required}
}
func (t *verbTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
	out, err := t.run(ctx, args)
	if err != nil {
		return err.Error(), false
	}
	return out, true
}

func str(a map[string]any, key string) string {
	s, _ := a[key].(string)
	return s
}

func num(a map[string]any, key string) int {
	switch n := a[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// verbInstructions tell an agent how to answer from the verbs cheaply. Each
// turn resends the whole conversation, so an answer's cost is mostly its
// turns: agents that probed with show until they ran out of turns spent the
// most and answered worst.
const verbInstructions = `To answer a question about documents and the meetings that discussed them: call find with the subject, then brief on the best ID, or on several IDs at once separated by commas. A brief already gathers what the document says, every meeting confirmed to discuss it with dates, what changed, what is open, conflicts, and the closest unconfirmed meetings. Answer from it, citing its dates and IDs. Call show only to check one specific claim or read one section, not to page through transcripts. A meeting that is not confirmed may still be relevant: say so rather than claiming no meeting discussed something.`
