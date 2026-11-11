package topics

import (
	"strings"
	"unicode"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// NormalizeName reduces a name to a comparison key: lowercase, no
// punctuation, single-spaced.
func NormalizeName(s string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		case r == ' ' || r == '-' || r == '_':
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// JaroWinkler returns the Jaro-Winkler similarity of two strings in [0,1].
// It rewards a shared prefix, which suits both transcription variants of a
// name and rewordings of a label: they almost always begin the same way and
// drift in the middle ("imran" vs "imron").
func JaroWinkler(a, b string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)

	window := max(la, lb)/2 - 1
	if window < 0 {
		window = 0
	}

	matchedA := make([]bool, la)
	matchedB := make([]bool, lb)
	matches := 0
	for i := 0; i < la; i++ {
		lo := max(0, i-window)
		hi := min(lb-1, i+window)
		for j := lo; j <= hi; j++ {
			if matchedB[j] || ra[i] != rb[j] {
				continue
			}
			matchedA[i], matchedB[j] = true, true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}

	transpositions := 0
	k := 0
	for i := 0; i < la; i++ {
		if !matchedA[i] {
			continue
		}
		for !matchedB[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}
	t := float64(transpositions) / 2
	m := float64(matches)
	jaro := (m/float64(la) + m/float64(lb) + (m-t)/m) / 3

	// Winkler prefix bonus, capped at 4 characters.
	prefix := 0
	for prefix < 4 && prefix < la && prefix < lb && ra[prefix] == rb[prefix] {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

// Aliases returns the alternate wordings recorded on a node, in the
// comma-separated graph.PropAliases property.
func Aliases(n *graph.Node) []string {
	if n.Properties == nil {
		return nil
	}
	raw := n.Properties[graph.PropAliases]
	if raw == "" {
		return nil
	}
	var out []string
	for _, a := range strings.Split(raw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}
