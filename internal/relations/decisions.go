package relations

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DecisionEntry is a dated decision about a document's subject, from the
// document itself, a meeting that discussed it, or a document that changes
// it. Entries are kept by ID across brief rewrites, so a decision recorded
// once stays in the history even when a later rewrite no longer reads the
// meeting it came from.
type DecisionEntry struct {
	ID string `json:"id"`
	// Date is YYYY-MM-DD, or empty when nothing dates it. DateBasis says
	// where it came from: stated (written beside the decision), meeting (the
	// day of the meeting that recorded it), or undated.
	Date      string `json:"date,omitempty"`
	DateBasis string `json:"date_basis"`
	Text      string `json:"text"`
	// Status is decided, or proposed when what was said was a suggestion
	// nobody was shown settling.
	Status string `json:"status"`
	// Source is doc, meeting, or writer (drawn from the brief's snippets).
	Source        string    `json:"source"`
	Cites         []string  `json:"cites"`
	FirstRecorded time.Time `json:"first_recorded"`
}

// Decision statuses and date bases.
const (
	StatusDecided  = "decided"
	StatusProposed = "proposed"

	DateStated  = "stated"
	DateMeeting = "meeting"
	DateNone    = "undated"
)

func decisionID(relID string, cites []string) string {
	sum := sha256.Sum256([]byte(relID + "|" + strings.Join(cites, ",")))
	return hex.EncodeToString(sum[:3])
}

// MeetingDecisions are the decisions a confirmed meeting recorded about the
// document: those whose topic is the topic of a stretch of the meeting the
// judge accepted. Deterministic, dated by the meeting, and cited by the
// decision's verbatim quote.
func MeetingDecisions(rel *Relation, meetings map[string]*Meeting) ([]DecisionEntry, []Evidence) {
	relID := RelationID(rel.SubjectPath)
	var entries []DecisionEntry
	var evidence []Evidence
	for _, v := range rel.Accepted() {
		m := meetings[v.SessionID]
		if m == nil {
			continue
		}
		topics := make(map[string]bool)
		for _, seg := range m.Segments {
			for _, rg := range v.Ranges {
				if seg.Start <= rg.End && seg.End >= rg.Start {
					topics[strings.ToLower(seg.Name)] = true
				}
			}
		}
		for _, d := range m.Decisions {
			if d.Quote == "" || !topics[strings.ToLower(d.Topic)] {
				continue
			}
			e := Evidence{Kind: EvidenceDecided, SessionID: m.SessionID, Quote: strings.Join(strings.Fields(d.Quote), " ")}
			e.ID = EvidenceID(relID, e)
			evidence = append(evidence, e)
			entries = append(entries, DecisionEntry{
				ID: decisionID(relID, []string{e.ID}), Date: m.Date.Format("2006-01-02"), DateBasis: DateMeeting,
				Text: d.Text, Status: StatusDecided, Source: "meeting", Cites: []string{e.ID},
			})
		}
	}
	return entries, evidence
}

var (
	decisionHeadingRe = regexp.MustCompile(`(?i)decision|changelog|change log|history|revision|log\b`)
	datedLineRe       = regexp.MustCompile(`(20\d\d-\d\d-\d\d)`)
)

// maxDocDecisions bounds the dated lines taken from one document.
const maxDocDecisions = 20

// DocDecisions are the dated lines in a document's decision, changelog,
// history or revision sections: "- 2026-09-12: drop the queue", a dated
// table row. The document states the date beside the decision, so it is
// taken as stated.
func DocDecisions(rel *Relation, subj *Subject) ([]DecisionEntry, []Evidence) {
	relID := RelationID(rel.SubjectPath)
	var entries []DecisionEntry
	var evidence []Evidence
	for _, sec := range subj.Sections {
		if !decisionHeadingRe.MatchString(sec.Heading) {
			continue
		}
		for _, line := range strings.Split(sec.Text, "\n") {
			m := datedLineRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			text := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "-*|"))
			text = strings.Join(strings.Fields(strings.ReplaceAll(text, "|", " · ")), " ")
			if len(text) < 12 {
				continue
			}
			e := Evidence{Kind: EvidenceDoc, Path: subj.Path, HeadingPath: sec.HeadingPath, Ordinal: sec.Ordinal,
				SectionHash: sec.Hash, Quote: strings.Join(strings.Fields(strings.TrimSpace(line)), " ")}
			e.ID = EvidenceID(relID, e)
			evidence = append(evidence, e)
			entries = append(entries, DecisionEntry{
				ID: decisionID(relID, []string{e.ID}), Date: m[1], DateBasis: DateStated, Text: clip(text, 300),
				Status: StatusDecided, Source: "doc", Cites: []string{e.ID},
			})
			if len(entries) >= maxDocDecisions {
				return entries, evidence
			}
		}
	}
	return entries, evidence
}

// MergeDecisions keeps every entry already recorded, adds the new ones, and
// orders them newest first, undated last. An entry seen again keeps the
// time it was first recorded but takes the latest wording and status.
func MergeDecisions(old, fresh []DecisionEntry, now time.Time) []DecisionEntry {
	byID := make(map[string]DecisionEntry, len(old)+len(fresh))
	for _, e := range old {
		byID[e.ID] = e
	}
	for _, e := range fresh {
		if prev, ok := byID[e.ID]; ok {
			e.FirstRecorded = prev.FirstRecorded
		} else {
			e.FirstRecorded = now
		}
		byID[e.ID] = e
	}
	out := make([]DecisionEntry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// mergeEvidence adds evidence not already held.
func mergeEvidence(have []Evidence, add []Evidence) []Evidence {
	seen := make(map[string]bool, len(have))
	for _, e := range have {
		seen[e.ID] = true
	}
	for _, e := range add {
		if !seen[e.ID] {
			seen[e.ID] = true
			have = append(have, e)
		}
	}
	return have
}

// decisionLine renders one entry.
func decisionLine(e DecisionEntry, via string) string {
	date := e.Date
	if date == "" {
		date = "undated"
	}
	status := ""
	if e.Status == StatusProposed {
		status = " (proposed, not settled)"
	}
	from := e.Source
	if via != "" {
		from = "via " + via
	}
	return "- " + date + " " + e.Text + status + " (" + from + ")" + cites(e.Cites)
}
