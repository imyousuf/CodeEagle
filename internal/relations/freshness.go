package relations

import (
	"sort"
	"time"
)

// Freshness flags. They are computed when a brief is read, from the
// relation and the document as they are then, so they are never stale
// themselves.
const (
	// FlagDocChangedAfterDiscussion: the document changed after the last
	// meeting that discussed it; nobody has discussed the new version.
	FlagDocChangedAfterDiscussion = "doc_changed_after_discussion"
	// FlagDiscussedAfterDocChange: a meeting discussed it after its last
	// change, and the brief records something said then; the document may
	// not reflect it.
	FlagDiscussedAfterDocChange = "discussed_after_doc_change"
	// FlagBriefPending: what the brief is written from has changed and it
	// has not been rewritten yet.
	FlagBriefPending = "brief_pending"
	// FlagDocMissing: the document is no longer in the graph.
	FlagDocMissing = "doc_missing"
	// FlagQuoteGone: a section the brief quotes has changed since.
	FlagQuoteGone = "quote_gone"
	// FlagSourceMissing: a piece of evidence no longer resolves to its source.
	FlagSourceMissing = "source_missing"
)

// Freshness returns the flags that apply to the relation now. subj is the
// document as it is now, nil if it is gone; meetings supplies meeting dates.
func Freshness(rel *Relation, subj *Subject, meetings map[string]*Meeting) []string {
	var flags []string
	if subj == nil {
		return []string{FlagDocMissing}
	}

	var lastDiscussed time.Time
	for _, v := range rel.Accepted() {
		if m := meetings[v.SessionID]; m != nil && m.Date.After(lastDiscussed) {
			lastDiscussed = m.Date
		}
	}
	if !lastDiscussed.IsZero() && !subj.UpdatedAt.IsZero() {
		switch {
		case subj.UpdatedAt.After(lastDiscussed):
			flags = append(flags, FlagDocChangedAfterDiscussion)
		case rel.Brief != nil && briefHasNewsSince(rel.Brief, subj.UpdatedAt):
			flags = append(flags, FlagDiscussedAfterDocChange)
		}
	}

	if rel.Brief == nil || InputsHash(rel, subj, rel.BriefModel) != rel.BriefInputsHash {
		flags = append(flags, FlagBriefPending)
	}

	current := make(map[string]string, len(subj.Sections))
	for _, sec := range subj.Sections {
		current[sec.HeadingPath] = sec.Hash
	}
	cited := citedIDs(rel.Brief)
	for _, e := range rel.Evidence {
		if !cited[e.ID] {
			continue
		}
		if e.Kind == EvidenceDoc && current[e.HeadingPath] != e.SectionHash {
			flags = appendOnce(flags, FlagQuoteGone)
		}
		if e.SourceMissing {
			flags = appendOnce(flags, FlagSourceMissing)
		}
	}
	sort.Strings(flags)
	return flags
}

// briefHasNewsSince reports whether the brief records an event or an open
// question dated after t.
func briefHasNewsSince(b *Brief, t time.Time) bool {
	after := func(date string) bool {
		d, err := time.Parse("2006-01-02", date)
		return err == nil && d.After(t.Truncate(24*time.Hour))
	}
	for _, e := range b.Events {
		if after(e.Date) {
			return true
		}
	}
	for _, u := range b.Unresolved {
		if after(u.Raised) {
			return true
		}
	}
	for _, c := range b.Conflicts {
		if after(c.Date) {
			return true
		}
	}
	return false
}

func citedIDs(b *Brief) map[string]bool {
	out := make(map[string]bool)
	if b == nil {
		return out
	}
	mark := func(ids []string) {
		for _, id := range ids {
			out[id] = true
		}
	}
	mark(b.Headline.Cites)
	for _, l := range b.Current {
		mark(l.Cites)
	}
	for _, e := range b.Events {
		mark(e.Cites)
	}
	for _, u := range b.Unresolved {
		mark(u.Cites)
	}
	for _, c := range b.Conflicts {
		out[c.DocCite], out[c.SaidCite] = true, true
	}
	return out
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
