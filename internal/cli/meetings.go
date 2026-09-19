package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/indexer"
	_ "github.com/imyousuf/CodeEagle/internal/llm" // register LLM providers
	"github.com/imyousuf/CodeEagle/internal/transcript"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// newMeetingsCmd builds the `codeeagle meetings` command tree.
func newMeetingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meetings",
		Short: "Index and query meeting transcripts",
		Long: `Index meeting transcripts into the knowledge graph.

Recordings are diarized but anonymous: voices are labelled "Person 1",
"Person 2", and so on, and those labels mean nothing outside a single
recording. Indexing works out who was actually speaking, then extracts the
topics, decisions, and follow-ups so agents can answer questions about what
was said, by whom, and what it committed anyone to.`,
	}
	cmd.AddCommand(
		newMeetingsSyncCmd(),
		newMeetingsListCmd(),
		newMeetingsShowCmd(),
		newMeetingsPeopleCmd(),
		newMeetingsIdentifyCmd(),
		newMeetingsLabelCmd(),
		newMeetingsTopicsCmd(),
		newMeetingsActionsCmd(),
	)
	return cmd
}

// --- sync ---

func newMeetingsSyncCmd() *cobra.Command {
	var (
		dir         string
		force       bool
		limit       int
		concurrency int
		model       string
		provider    string
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Enrich meeting transcripts and index them into the graph",
		Long: `Enrich transcripts and write them into the knowledge graph.

Recordings already indexed and unchanged are skipped, so re-running after new
meetings costs nothing for the ones already done.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			tc := cfg.Transcripts
			if dir != "" {
				tc.SessionsDir = dir
			}
			if model != "" {
				tc.Model = model
			}
			if provider != "" {
				tc.Provider = provider
			}
			if concurrency > 0 {
				tc.Concurrency = concurrency
			}
			if tc.SessionsDir == "" {
				return fmt.Errorf("no transcripts directory configured; set transcripts.sessions_dir or pass --dir")
			}
			tc.SessionsDir = expandPath(tc.SessionsDir)

			out := cmd.OutOrStdout()

			if dryRun {
				return meetingsDryRun(out, tc.SessionsDir, limit)
			}

			client, err := newTranscriptClient(tc)
			if err != nil {
				return err
			}
			defer client.Close()

			store, branch, err := openBranchStore(cfg)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()
			fmt.Fprintf(out, "Graph branch: %s\n", branch)
			fmt.Fprintf(out, "Provider: %s (%s)\n\n", client.Provider(), client.Model())

			ctx := cmd.Context()
			people, err := transcript.LoadPersonRegistry(ctx, store)
			if err != nil {
				return fmt.Errorf("load people: %w", err)
			}

			analyzer := transcript.NewAnalyzer(client, transcript.Options{
				Owner:         tc.Owner,
				OwnerAliases:  tc.OwnerAliases,
				Roster:        tc.Roster,
				ExcludeNames:  tc.ExcludeNames,
				MinConfidence: tc.MinConfidence,
			})
			writer := transcript.NewWriter(store, people, transcript.WriterOptions{
				MinConfidence: tc.MinConfidence,
				Owner:         tc.Owner,
			}).WithDateLinker(meetingDateLinker)

			ix := transcript.NewIndexer(store, analyzer, writer, transcript.IndexOptions{
				SessionsDir: tc.SessionsDir,
				Concurrency: tc.Concurrency,
				Force:       force,
				Limit:       limit,
				Log: func(format string, args ...any) {
					fmt.Fprintf(out, format+"\n", args...)
				},
			})

			report, err := ix.Run(ctx)
			if err != nil {
				return err
			}
			printRunReport(out, report)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory of recorded sessions (overrides config)")
	cmd.Flags().BoolVar(&force, "force", false, "re-enrich recordings that are already indexed")
	cmd.Flags().IntVar(&limit, "limit", 0, "process at most N recordings")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "recordings to analyse at once")
	cmd.Flags().StringVar(&model, "model", "", "model to use (overrides config)")
	cmd.Flags().StringVar(&provider, "provider", "", "LLM provider (overrides config)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be processed without calling a model")
	return cmd
}

// meetingsDryRun reports what a run would cover without spending anything.
func meetingsDryRun(out io.Writer, dir string, limit int) error {
	paths, err := transcript.DiscoverSessions(dir)
	if err != nil {
		return err
	}
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}

	var speech, chars float64
	var empty, participants int
	for _, p := range paths {
		s, err := transcript.Load(p)
		if err != nil {
			continue
		}
		if s.IsEmpty() {
			empty++
			continue
		}
		speech += s.SpeechSeconds()
		chars += float64(len(s.Transcript()))
		participants += len(s.SubstantiveSpeakers())
	}

	fmt.Fprintf(out, "Recordings:       %d (%d with no speech)\n", len(paths), empty)
	fmt.Fprintf(out, "Speech:           %.1f hours\n", speech/3600)
	fmt.Fprintf(out, "Participants:     %d across all recordings\n", participants)
	// Both enrichment passes send the transcript, so the prompt cost is
	// roughly twice its token count.
	promptTokens := chars / 4 * 2
	fmt.Fprintf(out, "Prompt tokens:    ~%.1fM (two passes per recording)\n", promptTokens/1e6)
	fmt.Fprintf(out, "\nRun without --dry-run to enrich and index.\n")
	return nil
}

// printRunReport summarizes a completed batch.
func printRunReport(out io.Writer, r *transcript.RunReport) {
	fmt.Fprintf(out, "\n── Indexed ──\n")
	fmt.Fprintf(out, "  meetings      %d\n", r.Stats.Meetings)
	fmt.Fprintf(out, "  participants  %d (%d identified, %d unresolved)\n",
		r.Stats.Speakers, r.Stats.Identified, r.Stats.Unidentified)
	fmt.Fprintf(out, "  new people    %d\n", r.Stats.People)
	fmt.Fprintf(out, "  topics        %d across %d segments\n", r.Stats.Topics, r.Stats.Segments)
	fmt.Fprintf(out, "  decisions     %d\n", r.Stats.Decisions)
	fmt.Fprintf(out, "  action items  %d\n", r.Stats.ActionItems)
	fmt.Fprintf(out, "  edges         %d\n", r.Stats.Edges)
	if r.Skipped > 0 || r.Empty > 0 {
		fmt.Fprintf(out, "  skipped       %d unchanged, %d with no speech\n", r.Skipped, r.Empty)
	}
	fmt.Fprintf(out, "\n── Cost ──\n")
	fmt.Fprintf(out, "  %d requests, %d in / %d out tokens, %s\n",
		r.Usage.Requests, r.Usage.InputTokens, r.Usage.OutputTokens, r.Duration.Round(time.Second))

	if len(r.Failures) > 0 {
		fmt.Fprintf(out, "\n── Failures (%d) ──\n", len(r.Failures))
		for i, f := range r.Failures {
			if i >= 10 {
				fmt.Fprintf(out, "  ... and %d more\n", len(r.Failures)-10)
				break
			}
			fmt.Fprintf(out, "  %s: %v\n", filepath.Base(filepath.Dir(f.Path)), f.Err)
		}
	}
}

// meetingDateLinker attaches a meeting to the Year/Month/Date hierarchy, so
// meetings answer temporal queries the same way modified files do.
func meetingDateLinker(ctx context.Context, store graph.Store, t interface{ Unix() int64 }, nodeID string) error {
	when, ok := t.(time.Time)
	if !ok {
		return nil
	}
	return indexer.EnsureDateNodes(ctx, store, when, nodeID)
}

// newTranscriptClient builds the LLM client used for enrichment.
func newTranscriptClient(tc config.TranscriptsConfig) (llm.Client, error) {
	apiKey, err := config.ResolveSecret(config.SecretSource{
		Literal: tc.APIKey,
		EnvVar:  tc.APIKeyEnv,
		Command: tc.APIKeyCommand,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve API key: %w", err)
	}

	provider := tc.Provider
	if provider == "" {
		provider = "baseten"
	}
	client, err := llm.NewClient(llm.Config{
		Provider:        provider,
		Model:           tc.Model,
		APIKey:          apiKey,
		BaseURL:         tc.BaseURL,
		MaxTokens:       tc.MaxTokens,
		ReasoningEffort: tc.ReasoningEffort,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s client: %w", provider, err)
	}
	return client, nil
}

// --- list ---

func newMeetingsListCmd() *cobra.Command {
	var (
		limit    int
		person   string
		asJSON   bool
		since    string
		showWith bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List indexed meetings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
				if err != nil {
					return err
				}
				sort.Slice(meetings, func(i, j int) bool {
					return meetings[i].UpdatedAt.After(meetings[j].UpdatedAt)
				})

				if since != "" {
					cutoff, err := time.Parse("2006-01-02", since)
					if err != nil {
						return fmt.Errorf("--since must be YYYY-MM-DD: %w", err)
					}
					var kept []*graph.Node
					for _, m := range meetings {
						if m.UpdatedAt.After(cutoff) {
							kept = append(kept, m)
						}
					}
					meetings = kept
				}

				if person != "" {
					filtered, err := meetingsWithPerson(ctx, store, meetings, person)
					if err != nil {
						return err
					}
					meetings = filtered
				}
				if limit > 0 && len(meetings) > limit {
					meetings = meetings[:limit]
				}

				out := cmd.OutOrStdout()
				if asJSON {
					return json.NewEncoder(out).Encode(meetingsToJSON(ctx, store, meetings))
				}
				if len(meetings) == 0 {
					fmt.Fprintln(out, "No meetings indexed. Run: codeeagle meetings sync")
					return nil
				}

				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "DATE\tDURATION\tPARTICIPANTS\tTITLE")
				for _, m := range meetings {
					who := "—"
					if showWith || person != "" {
						if names, err := attendeeNames(ctx, store, m.ID); err == nil && len(names) > 0 {
							who = strings.Join(names, ", ")
						}
					} else if names, err := attendeeNames(ctx, store, m.ID); err == nil {
						who = strconv.Itoa(len(names))
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
						m.UpdatedAt.Format("2006-01-02 15:04"),
						formatSeconds(m.Properties[graph.PropDuration]),
						truncateText(who, 40),
						truncateText(m.Name, 70))
				}
				return tw.Flush()
			})
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 30, "maximum meetings to show (0 for all)")
	cmd.Flags().StringVar(&person, "person", "", "only meetings this person attended")
	cmd.Flags().StringVar(&since, "since", "", "only meetings after this date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&showWith, "who", false, "list attendee names instead of a count")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// --- show ---

func newMeetingsShowCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "show <meeting-id-or-title>",
		Short: "Show a meeting's participants, topics, decisions, and follow-ups",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				m, err := findMeeting(ctx, store, args[0])
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if asJSON {
					return json.NewEncoder(out).Encode(meetingsToJSON(ctx, store, []*graph.Node{m})[0])
				}

				fmt.Fprintf(out, "%s\n", m.Name)
				fmt.Fprintf(out, "%s · %s · %s\n\n",
					m.UpdatedAt.Format("Monday, 2 January 2006 15:04"),
					formatSeconds(m.Properties[graph.PropDuration]),
					orNone(m.Properties[graph.PropPlatform]))

				if s := m.Properties[graph.PropSummary]; s != "" {
					fmt.Fprintf(out, "%s\n\n", s)
				}

				if err := showParticipants(ctx, store, out, m); err != nil {
					return err
				}
				if err := showChildren(ctx, store, out, m); err != nil {
					return err
				}
				if mentions := m.Properties["mentions"]; mentions != "" {
					fmt.Fprintf(out, "Mentioned: %s\n", mentions)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func showParticipants(ctx context.Context, store graph.Store, out io.Writer, m *graph.Node) error {
	speakers, err := store.GetNeighbors(ctx, m.ID, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return err
	}
	var rows []string
	for _, sp := range speakers {
		if sp.Type != graph.NodeSpeaker {
			continue
		}
		name := "unidentified"
		detail := ""
		people, err := store.GetNeighbors(ctx, sp.ID, graph.EdgeIdentifiedAs, graph.Outgoing)
		if err == nil && len(people) > 0 {
			name = people[0].Name
			if edges, err := store.GetEdges(ctx, sp.ID, graph.EdgeIdentifiedAs); err == nil {
				for _, e := range edges {
					if e.SourceID == sp.ID && e.Properties != nil {
						detail = fmt.Sprintf(" (%s, confidence %s)",
							e.Properties[graph.PropResolution], e.Properties[graph.PropConfidence])
					}
				}
			}
		}
		secs, _ := strconv.ParseFloat(sp.Properties[graph.PropSpeakingSeconds], 64)
		rows = append(rows, fmt.Sprintf("  %-22s %-10s spoke %s%s",
			name, sp.Name, transcript.FormatTimestamp(secs), detail))
	}
	if len(rows) > 0 {
		fmt.Fprintf(out, "Participants (%d)\n", len(rows))
		for _, r := range rows {
			fmt.Fprintln(out, r)
		}
		fmt.Fprintln(out)
	}
	return nil
}

func showChildren(ctx context.Context, store graph.Store, out io.Writer, m *graph.Node) error {
	children, err := store.GetNeighbors(ctx, m.ID, graph.EdgeContains, graph.Outgoing)
	if err != nil {
		return err
	}

	var segments, decisions, actions []*graph.Node
	for _, c := range children {
		switch c.Type {
		case graph.NodeTopicSegment:
			segments = append(segments, c)
		case graph.NodeDecision:
			decisions = append(decisions, c)
		case graph.NodeActionItem:
			actions = append(actions, c)
		}
	}

	sort.Slice(segments, func(i, j int) bool {
		a, _ := strconv.ParseFloat(segments[i].Properties[graph.PropStartTime], 64)
		b, _ := strconv.ParseFloat(segments[j].Properties[graph.PropStartTime], 64)
		return a < b
	})

	if len(segments) > 0 {
		fmt.Fprintf(out, "Topics (%d)\n", len(segments))
		for _, s := range segments {
			start, _ := strconv.ParseFloat(s.Properties[graph.PropStartTime], 64)
			end, _ := strconv.ParseFloat(s.Properties[graph.PropEndTime], 64)
			fmt.Fprintf(out, "  [%s–%s] %s\n",
				transcript.FormatTimestamp(start), transcript.FormatTimestamp(end), s.Name)
			if summary := s.Properties[graph.PropSummary]; summary != "" {
				fmt.Fprintf(out, "      %s\n", wrapText(summary, 76, "      "))
			}
		}
		fmt.Fprintln(out)
	}

	if len(decisions) > 0 {
		fmt.Fprintf(out, "Decisions (%d)\n", len(decisions))
		for _, d := range decisions {
			mark := " "
			if d.Properties["quote_verified"] != "true" {
				// Flag anything whose supporting quote is not in the
				// transcript: it is the one claim a reader should not take on
				// trust.
				mark = "?"
			}
			fmt.Fprintf(out, " %s %s\n", mark, d.Properties[graph.PropSummary])
			if q := d.Properties[graph.PropQuote]; q != "" {
				fmt.Fprintf(out, "      \"%s\"\n", truncateText(q, 100))
			}
		}
		fmt.Fprintln(out)
	}

	if len(actions) > 0 {
		fmt.Fprintf(out, "Follow-ups (%d)\n", len(actions))
		for _, a := range actions {
			who := a.Properties[graph.PropAssignee]
			if who == "" {
				who = "unassigned"
			}
			due := ""
			if d := a.Properties[graph.PropDueDate]; d != "" {
				due = " · due " + d
			}
			fmt.Fprintf(out, "  □ %s\n      %s%s\n", a.Properties[graph.PropSummary], who, due)
		}
		fmt.Fprintln(out)
	}
	return nil
}

// --- people ---

func newMeetingsPeopleCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "people",
		Short: "List people identified across meetings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				people, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodePerson})
				if err != nil {
					return err
				}

				type row struct {
					Name     string   `json:"name"`
					Aliases  []string `json:"aliases,omitempty"`
					Meetings int      `json:"meetings"`
					Seconds  float64  `json:"speaking_seconds"`
					Actions  int      `json:"action_items"`
					Owner    bool     `json:"owner,omitempty"`
				}
				var rows []row
				for _, p := range people {
					attended, err := store.GetEdges(ctx, p.ID, graph.EdgeAttended)
					if err != nil {
						return err
					}
					var secs float64
					count := 0
					for _, e := range attended {
						if e.SourceID != p.ID {
							continue
						}
						count++
						if e.Properties != nil {
							v, _ := strconv.ParseFloat(e.Properties[graph.PropSpeakingSeconds], 64)
							secs += v
						}
					}
					assigned, _ := store.GetEdges(ctx, p.ID, graph.EdgeAssignedTo)
					nActions := 0
					for _, e := range assigned {
						if e.TargetID == p.ID {
							nActions++
						}
					}
					if count == 0 && nActions == 0 {
						// Someone known only from photographs, not meetings.
						continue
					}
					var aliases []string
					if p.Properties != nil && p.Properties[graph.PropAliases] != "" {
						aliases = strings.Split(p.Properties[graph.PropAliases], ",")
					}
					rows = append(rows, row{
						Name: p.Name, Aliases: aliases, Meetings: count,
						Seconds: secs, Actions: nActions,
						Owner: p.Properties != nil && p.Properties[graph.PropIsOwner] == "true",
					})
				}
				sort.Slice(rows, func(i, j int) bool {
					if rows[i].Meetings != rows[j].Meetings {
						return rows[i].Meetings > rows[j].Meetings
					}
					return rows[i].Name < rows[j].Name
				})

				out := cmd.OutOrStdout()
				if asJSON {
					return json.NewEncoder(out).Encode(rows)
				}
				if len(rows) == 0 {
					fmt.Fprintln(out, "No people identified yet. Run: codeeagle meetings sync")
					return nil
				}
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "PERSON\tMEETINGS\tSPOKE\tFOLLOW-UPS\tALSO HEARD AS")
				for _, r := range rows {
					name := r.Name
					if r.Owner {
						name += " (you)"
					}
					fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%s\n",
						name, r.Meetings, transcript.FormatTimestamp(r.Seconds),
						r.Actions, strings.Join(r.Aliases, ", "))
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// --- identify ---

func newMeetingsIdentifyCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "identify",
		Short: "Review speakers that could not be identified automatically",
		Long: `List the speakers whose identity the transcript did not settle.

Each entry shows how much the speaker said and a sample of their words, so
they can be recognized and assigned with "codeeagle meetings label".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				speakers, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeSpeaker})
				if err != nil {
					return err
				}

				type pending struct {
					speaker *graph.Node
					meeting string
					secs    float64
				}
				var out []pending
				for _, sp := range speakers {
					people, err := store.GetNeighbors(ctx, sp.ID, graph.EdgeIdentifiedAs, graph.Outgoing)
					if err != nil || len(people) > 0 {
						continue
					}
					secs, _ := strconv.ParseFloat(sp.Properties[graph.PropSpeakingSeconds], 64)
					title := sp.Properties[graph.PropMeetingID]
					if m, err := findMeetingByID(ctx, store, sp.Properties[graph.PropMeetingID]); err == nil && m != nil {
						title = fmt.Sprintf("%s (%s)", m.Name, m.UpdatedAt.Format("2006-01-02"))
					}
					out = append(out, pending{speaker: sp, meeting: title, secs: secs})
				}
				// Most talkative first: identifying them recovers the most.
				sort.Slice(out, func(i, j int) bool { return out[i].secs > out[j].secs })
				if limit > 0 && len(out) > limit {
					out = out[:limit]
				}

				w := cmd.OutOrStdout()
				if len(out) == 0 {
					fmt.Fprintln(w, "Every speaker has been identified.")
					return nil
				}
				fmt.Fprintf(w, "%d speakers await identification.\n\n", len(out))
				for _, p := range out {
					fmt.Fprintf(w, "%s · %s · spoke %s\n",
						p.speaker.Name, p.meeting, transcript.FormatTimestamp(p.secs))
					if sample := speakerSample(p.speaker); sample != "" {
						fmt.Fprintf(w, "    %s\n", sample)
					}
					fmt.Fprintf(w, "    codeeagle meetings label %s \"<name>\" --meeting %s\n\n",
						p.speaker.Name, p.speaker.Properties[graph.PropMeetingID])
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum speakers to show (0 for all)")
	return cmd
}

// speakerSample reads a few of a speaker's words from the source transcript,
// which is what actually lets someone recognize who was talking.
func speakerSample(sp *graph.Node) string {
	if sp.FilePath == "" {
		return ""
	}
	s, err := transcript.Load(sp.FilePath)
	if err != nil {
		return ""
	}
	for _, t := range s.Turns() {
		if t.Speaker == sp.Name && len(t.Text) > 60 {
			return "\"" + truncateText(t.Text, 140) + "\""
		}
	}
	return ""
}

// --- label ---

func newMeetingsLabelCmd() *cobra.Command {
	var meetingID string

	cmd := &cobra.Command{
		Use:   "label <speaker-label> <person-name>",
		Short: "Assign a speaker to a person by hand",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			label, name := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			store, _, err := openBranchStore(cfg)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			ctx := cmd.Context()
			speakers, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeSpeaker})
			if err != nil {
				return err
			}

			var matches []*graph.Node
			for _, sp := range speakers {
				if sp.Name != label {
					continue
				}
				if meetingID != "" && sp.Properties[graph.PropMeetingID] != meetingID {
					continue
				}
				matches = append(matches, sp)
			}
			switch {
			case len(matches) == 0:
				return fmt.Errorf("no speaker %q found%s", label, meetingSuffix(meetingID))
			case len(matches) > 1:
				// A label like "Person 1" exists in most recordings, so an
				// ambiguous assignment must be refused rather than guessed.
				return fmt.Errorf("%q appears in %d meetings; pass --meeting <id> to choose one",
					label, len(matches))
			}
			speaker := matches[0]

			people, err := transcript.LoadPersonRegistry(ctx, store)
			if err != nil {
				return err
			}
			person, err := people.Resolve(ctx, name)
			if err != nil {
				return err
			}

			if err := store.AddEdge(ctx, &graph.Edge{
				ID:       graph.NewNodeID("edge", speaker.ID, person.ID+":"+string(graph.EdgeIdentifiedAs)),
				Type:     graph.EdgeIdentifiedAs,
				SourceID: speaker.ID,
				TargetID: person.ID,
				Properties: map[string]string{
					graph.PropConfidence: "1.00",
					graph.PropResolution: transcript.MethodManual,
					graph.PropEvidence:   "assigned by hand",
				},
			}); err != nil {
				return err
			}

			meetingNodeID := graph.NewNodeID(string(graph.NodeMeeting), speaker.FilePath,
				speaker.Properties[graph.PropMeetingID])
			if err := store.AddEdge(ctx, &graph.Edge{
				ID:       graph.NewNodeID("edge", person.ID, meetingNodeID+":"+string(graph.EdgeAttended)),
				Type:     graph.EdgeAttended,
				SourceID: person.ID,
				TargetID: meetingNodeID,
				Properties: map[string]string{
					graph.PropSpeakerLabel:    speaker.Name,
					graph.PropSpeakingSeconds: speaker.Properties[graph.PropSpeakingSeconds],
				},
			}); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s in meeting %s is %s\n",
				label, speaker.Properties[graph.PropMeetingID], person.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&meetingID, "meeting", "", "meeting id, when the label appears in several")
	return cmd
}

func meetingSuffix(id string) string {
	if id == "" {
		return ""
	}
	return " in meeting " + id
}

// --- topics ---

func newMeetingsTopicsCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "topics",
		Short: "List topics discussed across meetings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				topics, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeTopic})
				if err != nil {
					return err
				}

				type row struct {
					name  string
					count int
				}
				var rows []row
				for _, t := range topics {
					edges, err := store.GetEdges(ctx, t.ID, graph.EdgeHasTopic)
					if err != nil {
						continue
					}
					n := 0
					for _, e := range edges {
						if e.TargetID != t.ID {
							continue
						}
						src, err := store.GetNode(ctx, e.SourceID)
						if err == nil && src != nil && src.Type == graph.NodeMeeting {
							n++
						}
					}
					if n > 0 {
						rows = append(rows, row{t.Name, n})
					}
				}
				sort.Slice(rows, func(i, j int) bool {
					if rows[i].count != rows[j].count {
						return rows[i].count > rows[j].count
					}
					return rows[i].name < rows[j].name
				})
				if limit > 0 && len(rows) > limit {
					rows = rows[:limit]
				}

				out := cmd.OutOrStdout()
				if len(rows) == 0 {
					fmt.Fprintln(out, "No meeting topics indexed yet.")
					return nil
				}
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "MEETINGS\tTOPIC")
				for _, r := range rows {
					fmt.Fprintf(tw, "%d\t%s\n", r.count, r.name)
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 40, "maximum topics to show (0 for all)")
	return cmd
}

// --- actions ---

func newMeetingsActionsCmd() *cobra.Command {
	var (
		person     string
		unassigned bool
		asJSON     bool
	)

	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List follow-ups and TODOs captured from meetings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withGraph(cmd, func(ctx context.Context, store graph.Store) error {
				actions, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeActionItem})
				if err != nil {
					return err
				}

				type row struct {
					Text     string `json:"text"`
					Assignee string `json:"assignee,omitempty"`
					Due      string `json:"due_date,omitempty"`
					Meeting  string `json:"meeting"`
					Date     string `json:"date"`
					Quote    string `json:"quote,omitempty"`
					Verified bool   `json:"quote_verified"`
				}
				var rows []row
				for _, a := range actions {
					assignee := a.Properties[graph.PropAssignee]
					if people, err := store.GetNeighbors(ctx, a.ID, graph.EdgeAssignedTo, graph.Outgoing); err == nil && len(people) > 0 {
						assignee = people[0].Name
					}
					if person != "" && !transcript.SameName(assignee, person) {
						continue
					}
					if unassigned && assignee != "" {
						continue
					}
					title, date := "", ""
					if m, err := findMeetingByID(ctx, store, a.Properties[graph.PropMeetingID]); err == nil && m != nil {
						title = m.Name
						date = m.UpdatedAt.Format("2006-01-02")
					}
					rows = append(rows, row{
						Text:     a.Properties[graph.PropSummary],
						Assignee: assignee,
						Due:      a.Properties[graph.PropDueDate],
						Meeting:  title,
						Date:     date,
						Quote:    a.Properties[graph.PropQuote],
						Verified: a.Properties["quote_verified"] == "true",
					})
				}
				sort.Slice(rows, func(i, j int) bool { return rows[i].Date > rows[j].Date })

				out := cmd.OutOrStdout()
				if asJSON {
					return json.NewEncoder(out).Encode(rows)
				}
				if len(rows) == 0 {
					fmt.Fprintln(out, "No follow-ups found.")
					return nil
				}
				for _, r := range rows {
					who := r.Assignee
					if who == "" {
						who = "unassigned"
					}
					due := ""
					if r.Due != "" {
						due = " · due " + r.Due
					}
					fmt.Fprintf(out, "□ %s\n    %s%s · %s, %s\n", r.Text, who, due, r.Meeting, r.Date)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&person, "person", "", "only follow-ups assigned to this person")
	cmd.Flags().BoolVar(&unassigned, "unassigned", false, "only follow-ups with no owner")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// --- shared helpers ---

// withGraph opens the graph read-only and runs fn.
func withGraph(cmd *cobra.Command, fn func(ctx context.Context, store graph.Store) error) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, _, err := openReadOnlyBranchStore(cfg)
	if err != nil {
		return fmt.Errorf("open graph store: %w", err)
	}
	defer store.Close()
	return fn(cmd.Context(), store)
}

func findMeeting(ctx context.Context, store graph.Store, ref string) (*graph.Node, error) {
	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return nil, err
	}
	for _, m := range meetings {
		if m.QualifiedName == ref || m.ID == ref {
			return m, nil
		}
	}
	var partial []*graph.Node
	for _, m := range meetings {
		if strings.Contains(strings.ToLower(m.Name), strings.ToLower(ref)) {
			partial = append(partial, m)
		}
	}
	switch len(partial) {
	case 0:
		return nil, fmt.Errorf("no meeting matching %q", ref)
	case 1:
		return partial[0], nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%d meetings match %q:\n", len(partial), ref)
		for i, m := range partial {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "  %s  %s\n", m.QualifiedName, m.Name)
		}
		return nil, fmt.Errorf("%s", b.String())
	}
}

func findMeetingByID(ctx context.Context, store graph.Store, sessionID string) (*graph.Node, error) {
	if sessionID == "" {
		return nil, nil
	}
	meetings, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeMeeting})
	if err != nil {
		return nil, err
	}
	for _, m := range meetings {
		if m.QualifiedName == sessionID {
			return m, nil
		}
	}
	return nil, nil
}

func attendeeNames(ctx context.Context, store graph.Store, meetingID string) ([]string, error) {
	people, err := store.GetNeighbors(ctx, meetingID, graph.EdgeAttended, graph.Incoming)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(people))
	for _, p := range people {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names, nil
}

func meetingsWithPerson(ctx context.Context, store graph.Store, meetings []*graph.Node, person string) ([]*graph.Node, error) {
	var kept []*graph.Node
	for _, m := range meetings {
		names, err := attendeeNames(ctx, store, m.ID)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if transcript.SameName(n, person) {
				kept = append(kept, m)
				break
			}
		}
	}
	return kept, nil
}

// meetingJSON is the machine-readable form of a meeting.
type meetingJSON struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Date         string   `json:"date"`
	Duration     string   `json:"duration"`
	Summary      string   `json:"summary,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Path         string   `json:"path,omitempty"`
}

func meetingsToJSON(ctx context.Context, store graph.Store, meetings []*graph.Node) []meetingJSON {
	out := make([]meetingJSON, 0, len(meetings))
	for _, m := range meetings {
		names, _ := attendeeNames(ctx, store, m.ID)
		out = append(out, meetingJSON{
			ID:           m.QualifiedName,
			Title:        m.Name,
			Date:         m.UpdatedAt.Format(time.RFC3339),
			Duration:     formatSeconds(m.Properties[graph.PropDuration]),
			Summary:      m.Properties[graph.PropSummary],
			Participants: names,
			Path:         m.FilePath,
		})
	}
	return out
}

func formatSeconds(v string) string {
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return "—"
	}
	return transcript.FormatTimestamp(secs)
}

func orNone(s string) string {
	if s == "" || s == "Unknown" || s == "None" {
		return "no platform recorded"
	}
	return s
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// wrapText reflows text to a width, indenting continuation lines.
func wrapText(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n"+indent)
}

// expandPath resolves a leading ~ to the user's home directory.
func expandPath(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
