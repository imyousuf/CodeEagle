package relations

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// AssetType is what kind of document a subject is, which decides what its
// brief says: a spec has a status and decisions, an incident report an
// impact and a root cause, notes have entries and open loops.
type AssetType string

// Asset types.
const (
	AssetSpec         AssetType = "spec"
	AssetADR          AssetType = "adr"
	AssetIncident     AssetType = "incident"
	AssetMeetingNotes AssetType = "meeting_notes"
	AssetNotes        AssetType = "notes"
	AssetReport       AssetType = "report"
	AssetDeck         AssetType = "deck"
	AssetReference    AssetType = "reference"
	AssetOther        AssetType = "other"
)

// assetSignals are the words in a document's path and headings that point to
// each type. A path word counts double: authors name files for what they
// are, while a heading such as "Timeline" turns up in many kinds of
// document.
var assetSignals = map[AssetType]struct{ path, headings []string }{
	AssetIncident: {
		path:     []string{"postmortem", "rca", "incident", "outage", "triage", "sweep", "stuck", "regression", "investigation", "timeline"},
		headings: []string{"root cause", "impact", "what happened", "detection", "remediation", "resolution", "action items", "timeline"},
	},
	AssetADR: {
		path:     []string{"adr", "decision", "decisions"},
		headings: []string{"context", "decision", "consequences", "decisions", "decision log", "locked decisions"},
	},
	AssetMeetingNotes: {
		path:     []string{"meeting", "standup", "minutes", "recap"},
		headings: []string{"attendees", "agenda", "participants", "action items", "next steps"},
	},
	AssetNotes: {
		path:     []string{"notes", "journal", "memory", "memories", "diary"},
		headings: []string{"open loops", "today", "log"},
	},
	AssetReport: {
		path:     []string{"report", "analysis", "findings", "review", "research", "results", "benchmark", "sweep"},
		headings: []string{"findings", "recommendations", "results", "methodology", "summary of findings", "conclusion"},
	},
	AssetReference: {
		path:     []string{"readme", "runbook", "guide", "howto", "faq", "playbook"},
		headings: []string{"usage", "installation", "how to", "getting started", "steps", "troubleshooting"},
	},
	AssetSpec: {
		path:     []string{"spec", "specs", "design", "plan", "rfc", "proposal", "architecture", "requirements", "brief"},
		headings: []string{"goals", "non-goals", "design", "architecture", "requirements", "data model", "rollout", "implementation", "phases", "open questions", "scope", "api"},
	},
}

var wordRe = regexp.MustCompile(`[a-z0-9]+`)

// deckFormats are the slide formats.
var deckFormats = map[string]bool{".pptx": true, ".odp": true}

// ClassifyAsset decides a document's type from its path and headings, and
// says what decided it. It is deterministic and costs nothing; a document
// whose words point nowhere in particular is "other", and its brief still
// has every common part.
func ClassifyAsset(s *Subject) (AssetType, string) {
	// A slide deck is a deck whatever it is about: it argues for something
	// in a few claims, which is what its brief must say.
	if deckFormats[strings.ToLower(path.Ext(s.Path))] {
		return AssetDeck, "format"
	}
	pathWords := make(map[string]bool)
	for _, w := range wordRe.FindAllString(strings.ToLower(s.Path), -1) {
		pathWords[w] = true
	}
	var headings []string
	for _, sec := range s.Sections {
		headings = append(headings, strings.ToLower(sec.Heading))
	}
	type scored struct {
		t     AssetType
		score int
		basis string
	}
	var all []scored
	for t, sig := range assetSignals {
		sc, basis := 0, ""
		for _, w := range sig.path {
			if pathWords[w] {
				sc += 2
				basis = "path"
			}
		}
		for _, h := range sig.headings {
			for _, got := range headings {
				if got == h || strings.HasSuffix(got, " "+h) || strings.HasPrefix(got, h+" ") || strings.Contains(got, ". "+h) {
					sc++
					if basis == "" {
						basis = "headings"
					}
					break
				}
			}
		}
		all = append(all, scored{t, sc, basis})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].t < all[j].t
	})
	if all[0].score < 2 || all[0].score == all[1].score {
		return AssetOther, "none"
	}
	return all[0].t, all[0].basis
}

var (
	statusRe = regexp.MustCompile(`(?im)^\s*(?:>\s*)?(?:[-*]\s*)?(?:\*\*)?status(?:\*\*)?\s*:?\s*(?:\*\*)?\s*:?\s*([^\n|]{2,120})`)
	dateRe   = regexp.MustCompile(`\b(20\d\d-\d\d-\d\d)\b`)
	datedRe  = regexp.MustCompile(`(?im)^\s*(?:>\s*)?(?:[-*]\s*)?(?:\*\*)?(?:last updated|updated|date|created)(?:\*\*)?\s*:?\s*(?:\*\*)?\s*:?\s*[^\n]*?(20\d\d-\d\d-\d\d)`)
)

// DocStatus reads the status and date a document states about itself, from
// its opening: "**Status:** Draft, 2026-10-04", "Last updated: ...". 219 of
// the 460 specs in the experiment corpus state a status there. Either may
// be empty; neither is guessed.
func DocStatus(s *Subject) (status, date string) {
	text := s.Preamble
	if m := statusRe.FindStringSubmatch(text); m != nil {
		status = strings.TrimSpace(strings.Trim(strings.TrimSpace(m[1]), "*_`·"))
		if i := strings.Index(status, "**"); i > 0 {
			status = strings.TrimSpace(status[:i])
		}
		// The status is the first sentence; what follows is about
		// something else ("Companion to the README").
		if i := strings.Index(status, ". "); i > 0 {
			status = status[:i]
		}
		status = strings.TrimRight(status, " .,;·")
	}
	switch {
	case datedRe.MatchString(text):
		date = datedRe.FindStringSubmatch(text)[1]
	case dateRe.MatchString(status):
		date = dateRe.FindStringSubmatch(status)[1]
	}
	return status, date
}

// assetParts are the parts a brief of each type has besides the common ones.
var assetParts = map[AssetType][]string{
	AssetADR:          {"context", "decision", "consequences"},
	AssetIncident:     {"impact", "root_cause", "incident_timeline", "follow_ups"},
	AssetMeetingNotes: {"agreed", "follow_ups"},
	AssetNotes:        {"entries", "open_loops"},
	AssetReport:       {"findings", "recommendations"},
	AssetDeck:         {"purpose", "claims", "asks"},
	AssetReference:    {"purpose", "how_to"},
}

// assetGuide tells the writer what a brief of each type is for.
var assetGuide = map[AssetType]string{
	AssetSpec:         "",
	AssetADR:          "This is a decision record. In parts, give its context, the decision itself, and its consequences.",
	AssetIncident:     "This is an incident or investigation write-up. In parts, give the impact (who or what was affected, how long), the root cause as the document states it, the incident timeline with times or dates, and the follow-ups with owners if named.",
	AssetMeetingNotes: "These are notes of a meeting. In parts, give what was agreed and the follow-ups with owners if named.",
	AssetNotes:        "These are working notes or a journal. In parts, give the dated entries that matter and the open loops still unresolved.",
	AssetReport:       "This is a report or analysis. In parts, give its findings and its recommendations.",
	AssetDeck:         "This is a slide deck. In parts, give its purpose (who it is for and what it wants of them), its main claims with the figures it gives, and its asks. Cite slides by their snippet.",
	AssetReference:    "This is reference material. In parts, give its purpose and how it is used.",
	AssetOther:        "",
}
