package relations

import "testing"

func sections(headings ...string) []*Section {
	var out []*Section
	for _, h := range headings {
		out = append(out, &Section{Heading: h, HeadingPath: h})
	}
	return out
}

func TestClassifyAsset(t *testing.T) {
	for _, tt := range []struct {
		path     string
		headings []string
		want     AssetType
	}{
		{"specs/agent-framework/stickyboard.md", []string{"1. Product framing", "2. Goals and Non-Goals", "6. Data model"}, AssetSpec},
		{"specs/home-slice/artifacts-list-slow-rca-2026-10-08.md", []string{"Summary", "Root cause", "Timeline", "Action items"}, AssetIncident},
		{"specs/home-slice/opal-chat-timeout-prefill-postmortem-2026-09-09.md", []string{"Impact", "What happened"}, AssetIncident},
		{"specs/home-slice/primary-mark-jobs-decisions.md", []string{"Context", "Decisions"}, AssetADR},
		{"specs/home-slice/meeting-summary-2026-09-24-cos-bot-and-collaboration.md", []string{"Attendees", "Next steps"}, AssetMeetingNotes},
		{"specs/home-slice/bigquery_cost_spike_runbook_query_fixes.md", []string{"Steps", "Troubleshooting"}, AssetReference},
		{"specs/home-slice/im-ability-latency-report.md", []string{"Findings", "Recommendations"}, AssetReport},
		{"misc/ideas.md", []string{"Ideas"}, AssetOther},
	} {
		got, _ := ClassifyAsset(&Subject{Path: tt.path, Sections: sections(tt.headings...)})
		if got != tt.want {
			t.Errorf("%s: %s, want %s", tt.path, got, tt.want)
		}
	}
}

func TestDocStatus(t *testing.T) {
	for _, tt := range []struct{ preamble, status, date string }{
		{"**Status**: Design draft **Branch**: `x` **Owner**: Imran", "Design draft", ""},
		{"> **Status:** draft, 2026-10-04. Companion to the README.", "draft, 2026-10-04", "2026-10-04"},
		{"Status: implemented.\nLast updated: 2026-09-12", "implemented", "2026-09-12"},
		{"A document with nothing to say about itself.", "", ""},
	} {
		status, date := DocStatus(&Subject{Preamble: tt.preamble})
		if status != tt.status || date != tt.date {
			t.Errorf("%q: got %q, %q; want %q, %q", tt.preamble, status, date, tt.status, tt.date)
		}
	}
}
