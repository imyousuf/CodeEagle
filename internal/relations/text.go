package relations

import (
	"regexp"
	"strings"
)

// fuzzyMinLen is the shortest word matched with one edit's tolerance.
// Transcription mangles names ("Markbench" heard as "Markbinch"), but on short
// words one edit turns one real word into another.
const fuzzyMinLen = 5

// fuzzyIndex finds the words of a vocabulary within one edit of a given word.
// It indexes every word under each of its one-letter deletions, so a lookup
// costs a handful of map reads instead of a comparison with every word.
type fuzzyIndex struct {
	words map[string]bool
	byDel map[string][]string
}

func newFuzzyIndex() *fuzzyIndex {
	return &fuzzyIndex{words: make(map[string]bool), byDel: make(map[string][]string)}
}

func (f *fuzzyIndex) add(w string) {
	if f.words[w] {
		return
	}
	f.words[w] = true
	if len(w) < fuzzyMinLen-1 {
		return
	}
	f.byDel[w] = append(f.byDel[w], w)
	for _, d := range deletions(w) {
		f.byDel[d] = append(f.byDel[d], w)
	}
}

// near returns the vocabulary words equal to w, or within one edit of it when
// w is long enough to tolerate one.
func (f *fuzzyIndex) near(w string) []string {
	var out []string
	if f.words[w] {
		out = append(out, w)
	}
	if len(w) < fuzzyMinLen {
		return out
	}
	seen := map[string]bool{w: true}
	try := func(key string) {
		for _, cand := range f.byDel[key] {
			if !seen[cand] && withinOneEdit(w, cand) {
				seen[cand] = true
				out = append(out, cand)
			}
		}
	}
	try(w)
	for _, d := range deletions(w) {
		try(d)
	}
	return out
}

func deletions(w string) []string {
	r := []rune(w)
	out := make([]string, 0, len(r))
	for i := range r {
		out = append(out, string(r[:i])+string(r[i+1:]))
	}
	return out
}

// withinOneEdit reports whether a and b differ by at most one insertion,
// deletion or substitution.
func withinOneEdit(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	if len(rb)-len(ra) > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		if len(ra) == len(rb) {
			i++
		}
		j++
	}
	return edits+(len(rb)-j)+(len(ra)-i) <= 1
}

// Identifier patterns. Spoken meetings rarely say these aloud, but summaries,
// keywords and mentions carry them when someone typed or read one out.
var (
	issueKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-\d{1,6}\b`)
	urlRe      = regexp.MustCompile(`https?://[^\s)>\]"']+`)
	prRe       = regexp.MustCompile(`(?i)\b(?:pr|pull request|issue)\s*#?\s*(\d{2,6})\b`)
	fileRe     = regexp.MustCompile(`\b[\w.-]+\.(?:go|py|ts|tsx|js|md|ya?ml|json|sql|proto|tf|java|rb|rs|sh)\b`)
	camelRe    = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]+)+\b`)
	snakeRe    = regexp.MustCompile(`\b[a-z][a-z0-9]+(?:_[a-z0-9]+)+\b`)
)

// identifiers extracts the identifiers in text, lower-cased.
func identifiers(text string) map[string]bool {
	out := make(map[string]bool)
	for _, re := range []*regexp.Regexp{issueKeyRe, urlRe, fileRe, camelRe, snakeRe} {
		for _, m := range re.FindAllString(text, -1) {
			out[strings.ToLower(strings.TrimRight(m, ".,;:"))] = true
		}
	}
	for _, m := range prRe.FindAllStringSubmatch(text, -1) {
		out["#"+m[1]] = true
	}
	return out
}
