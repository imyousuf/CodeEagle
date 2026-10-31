package relations

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Reference is a document naming another, resolved to it: a fact the author
// wrote, which needs no judge to be shown. What the reference means (a
// replacement, a companion, a record of an incident) is judged separately.
type Reference struct {
	Target  string `json:"target"`
	Section string `json:"section,omitempty"`
	Context string `json:"context,omitempty"`
	Via     string `json:"via"` // link | path
}

var (
	// Markdown links to another document, with any anchor.
	refLinkRe = regexp.MustCompile(`\]\(\s*<?([^)\s>#]+\.(?:md|markdown|txt|pdf|docx|pptx))(?:#[^)\s>]*)?>?\s*\)`)
	// A path to a document written in backticks or bare. Specs name each
	// other in backticks more often than they link: 1,506 backtick mentions
	// against 1,015 links in the experiment corpus.
	refPathRe = regexp.MustCompile("`?((?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_.-]+\\.(?:md|markdown))`?")
)

// maxRefContext bounds the sentence kept around a reference.
const maxRefContext = 240

// Resolver turns what a document wrote into the path of a document in the
// corpus.
type Resolver struct {
	paths  map[string]bool
	suffix map[string][]string // every trailing run of path components -> paths
}

// NewResolver indexes the corpus's paths by every trailing run of their
// components, so "agent-framework/stickyboard.md" and
// "docs/tech-spec/agent-framework/stickyboard.md" both find
// "specs/agent-framework/stickyboard.md".
func NewResolver(paths []string) *Resolver {
	r := &Resolver{paths: make(map[string]bool, len(paths)), suffix: make(map[string][]string)}
	for _, p := range paths {
		r.paths[p] = true
		parts := strings.Split(p, "/")
		for i := range parts {
			key := strings.ToLower(strings.Join(parts[i:], "/"))
			r.suffix[key] = append(r.suffix[key], p)
		}
	}
	return r
}

// Resolve finds the document a reference written in source names, or ""
// when none or more than one could be meant: a wrong link is worse than a
// missing one. It tries the path relative to the source's directory, then
// the longest trailing run of the written path that names exactly one
// document.
func (r *Resolver) Resolve(source, written string) string {
	w := strings.TrimSpace(written)
	if w == "" || strings.Contains(w, "://") {
		return ""
	}
	if rel := path.Clean(path.Join(path.Dir(source), w)); r.paths[rel] && rel != source {
		return rel
	}
	parts := strings.Split(strings.Trim(path.Clean(w), "/"), "/")
	for i := range parts {
		key := strings.ToLower(strings.Join(parts[i:], "/"))
		if key == "" || key == "." {
			continue
		}
		switch got := r.suffix[key]; len(got) {
		case 0:
			continue
		case 1:
			if got[0] == source {
				return ""
			}
			return got[0]
		default:
			return "" // several documents share this name
		}
	}
	return ""
}

// References finds the documents a subject names, once each, with the
// section and sentence of the first mention.
func References(subj *Subject, r *Resolver) []Reference {
	seen := make(map[string]bool)
	var out []Reference
	add := func(sec *Section, written, via string, at int, text string) {
		target := r.Resolve(subj.Path, written)
		if target == "" || seen[target] {
			return
		}
		seen[target] = true
		out = append(out, Reference{Target: target, Section: sec.HeadingPath, Context: around(text, at), Via: via})
	}
	for _, sec := range subj.Sections {
		for _, m := range refLinkRe.FindAllStringSubmatchIndex(sec.Text, -1) {
			add(sec, sec.Text[m[2]:m[3]], "link", m[0], sec.Text)
		}
		for _, m := range refPathRe.FindAllStringSubmatchIndex(sec.Text, -1) {
			add(sec, sec.Text[m[2]:m[3]], "path", m[0], sec.Text)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}

// around is the sentence-ish stretch of text around position at.
func around(text string, at int) string {
	start := max(0, at-maxRefContext/2)
	end := min(len(text), at+maxRefContext/2)
	if i := strings.LastIndexAny(text[start:at], ".\n"); i >= 0 {
		start += i + 1
	}
	if i := strings.IndexAny(text[at:end], "\n"); i >= 0 {
		end = at + i
	}
	return strings.Join(strings.Fields(text[start:end]), " ")
}

// sameRefs reports whether two reference lists name the same targets the
// same way.
func sameRefs(a, b []Reference) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Target != b[i].Target || a[i].Section != b[i].Section || a[i].Via != b[i].Via {
			return false
		}
	}
	return true
}
