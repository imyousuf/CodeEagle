package transcript

import (
	"strings"
	"unicode"
)

// This file decides what counts as a person's name and when two spellings
// refer to the same person.
//
// Both questions are harder than they look on speech-recognized text. The
// transcriber capitalizes the first word of every sentence, so capitalization
// alone nominates "Wait", "Thanks" and "Right" as names. It also spells the
// same name differently between recordings — the corpus contains both "Imran"
// and "Imron" for one person. So plausibility filtering happens here, and
// matching tolerates the small edits a transcriber introduces.

// commonWords are words that appear capitalized in ordinary speech — usually
// because they start a sentence — and must never be mistaken for names.
var commonWords = map[string]bool{}

func init() {
	words := `a about above actually after again against all almost alone along already also although
always am an and another any anybody anyone anything anyway are around as ask at away back bad be
because been before begin behind being below best better between big both but by call can cannot
come could couple course cut day days did didn do does doing don done down during each early eight
either else end enough even ever every everybody everyone everything exactly example except far
fast few find first five for from front full get give go going gone good got great guess guy guys
had half happen has have he hear help her here hers herself hey hi high him himself his hold home
hope how however hundred i if in into is it its itself just keep kind know last late later least
left less let like listen little long look lot love made make many may maybe me mean means meeting
might mind mine minute minutes more morning most move much must my myself name near need never new
next nice night nine no nobody none nor not nothing now number of off often oh okay old on once one
only open or order other others ought our ours ourselves out over own part people perfect perhaps
person piece place please point possible probably problem put question quick quickly quite
rather read ready real really right room run said same saw say says second see seem seen sense
seven several shall she should show side since six so some somebody someone something sometimes
soon sorry sound speak start state still stop such sure take talk tell ten than thank thanks that
the their theirs them themselves then there these they thing things think third this those though
thought three through thus time times to today together told tomorrow too took top total toward
try turn twelve twenty two under understand until up upon us use used usually very wait want was
way we week well went were what whatever when where whether which while who whole whom whose why
will with within without won word work world would wow write wrong year years yes yet you your
yours yourself yourselves
monday tuesday wednesday thursday friday saturday sunday january february march april may june
july august september october november december
everybody everyone folks team teams hello howdy morning afternoon evening cheers bye goodbye
awesome cool nice amazing interesting basically literally honestly obviously definitely
absolutely totally certainly clearly essentially
yeah yep yup nope nah uh um hmm mhm ah oh ok
brief boss sir maam ma'am dude man buddy mate bro bruh oye arre
zoom teams slack google meet webex email calendar doc docs sheet sheets
alright anyways anyhow otherwise however therefore meanwhile besides moreover
regardless furthermore nevertheless nonetheless instead indeed
uh-huh mm-hmm uh-uh okay-so gotcha exactly correct agreed understood
fight point stuff thing done finished ready fine bad wrong true false
everything nothing anything something someone everyone anyone nobody
hold wait stop go come look listen see watch check
im ive ill id youre youve theyre weve thats whats lets dont cant wont isnt
sprint standup retro demo review release deploy staging prod production
api apis ui ux db sql json yaml http https url urls sdk cli mvp poc eta kpi
jira github gitlab figma notion confluence`
	for _, w := range strings.Fields(words) {
		commonWords[w] = true
	}
}

// IsCommonWord reports whether a token is ordinary vocabulary rather than a
// name. Punctuation is stripped first so that contractions ("I'm", "that's")
// are recognized as the ordinary words they are.
func IsCommonWord(s string) bool {
	return commonWords[NormalizeName(s)]
}

// PlausibleName reports whether a candidate token could be a person's given
// name. It is deliberately permissive about unusual names and strict about
// ordinary words, because a false name that reaches the graph becomes a
// spurious person while a missed one is merely re-found in another meeting.
func PlausibleName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	parts := strings.Fields(s)
	if len(parts) == 0 || len(parts) > 3 {
		return false
	}
	for _, p := range parts {
		p = strings.Trim(p, ".,!?;:'\"")
		if len(p) < 2 || len(p) > 20 {
			return false
		}
		r := []rune(p)
		if !unicode.IsUpper(r[0]) {
			return false
		}
		for _, c := range r[1:] {
			// Allow McDonald, O'Brien, Jean-Luc.
			if !unicode.IsLetter(c) && c != '\'' && c != '-' {
				return false
			}
		}
		if IsCommonWord(p) {
			return false
		}
	}
	return true
}

// honorificPrefixes and honorificSuffixes are titles of respect that attach to
// a name without being part of it. The suffix list includes South Asian forms
// ("bhai", "da", "ji") because they appear throughout this corpus — a
// transcriber renders "Rupak bhai" as "Rupad Bai", and treating that as a
// two-word surname would invent a person.
var honorificPrefixes = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "miss": true, "dr": true,
	"prof": true, "professor": true, "sir": true, "madam": true,
}

var honorificSuffixes = map[string]bool{
	"bhai": true, "bai": true, "vai": true, "bhaiya": true,
	"da": true, "dada": true, "di": true, "didi": true,
	"ji": true, "jee": true, "san": true, "sama": true, "sensei": true,
	"sir": true, "jr": true, "sr": true, "ii": true, "iii": true,
}

// CleanName strips honorifics and trailing noise from a candidate name,
// returning the cleaned name or "" if nothing plausible remains.
//
// Speech recognition routinely appends an unrelated word to a name — "Evan
// Fight", "Yamara Amra" — so a trailing token that is ordinary vocabulary is
// dropped rather than allowed to make a two-word name.
func CleanName(s string) string {
	s = uninvertName(strings.TrimSpace(s))
	// A comma surviving un-inverting means several people were listed, which
	// is not one person's name.
	if strings.Contains(s, ",") {
		return ""
	}
	parts := strings.Fields(s)
	// Drop leading titles.
	for len(parts) > 0 {
		head := strings.ToLower(strings.Trim(parts[0], ".,"))
		if !honorificPrefixes[head] {
			break
		}
		parts = parts[1:]
	}
	// Drop trailing honorifics and trailing ordinary words.
	for len(parts) > 1 {
		tail := strings.ToLower(strings.Trim(parts[len(parts)-1], ".,"))
		if honorificSuffixes[tail] || commonWords[tail] {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	// A lone honorific is not a name.
	if len(parts) == 1 {
		only := strings.ToLower(strings.Trim(parts[0], ".,"))
		if honorificSuffixes[only] || honorificPrefixes[only] {
			return ""
		}
	}
	cleaned := strings.Join(parts, " ")
	if !PlausibleName(cleaned) {
		return ""
	}
	return cleaned
}

// uninvertName turns "Bonaiuto, Jenna" into "Jenna Bonaiuto".
//
// Directories list people surname-first, and a meeting platform writes whatever
// the directory gave it. Left alone, the same colleague appears as "Jenna
// Bonaiuto" from one source and "Bonaiuto, Jenna" from another, and the two
// never resolve to one person.
func uninvertName(s string) string {
	// Exactly one comma, with something on each side, is the inverted form.
	// More than one is a list of people, which is not a name at all.
	first := strings.Index(s, ",")
	if first < 0 || strings.Count(s, ",") != 1 {
		return s
	}
	surname := strings.TrimSpace(s[:first])
	given := strings.TrimSpace(s[first+1:])
	if surname == "" || given == "" {
		return s
	}
	// Both sides must be short enough to be name parts rather than a clause.
	if len(strings.Fields(surname)) > 2 || len(strings.Fields(given)) > 2 {
		return s
	}
	return given + " " + surname
}

// NormalizeName reduces a name to a comparison key: lowercase, no punctuation,
// single-spaced.
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

// FirstName returns the leading token of a normalized name.
func FirstName(s string) string {
	n := NormalizeName(s)
	if i := strings.IndexByte(n, ' '); i > 0 {
		return n[:i]
	}
	return n
}

// Surname returns the last token of a normalized name, or "" when the name has
// only one part.
func Surname(s string) string {
	n := NormalizeName(s)
	if i := strings.LastIndexByte(n, ' '); i > 0 {
		return n[i+1:]
	}
	return ""
}

// nicknames maps familiar forms to the formal name they abbreviate. Entries are
// one-directional keys; matching consults the table in both directions.
var nicknames = map[string]string{
	"abby": "abigail", "al": "albert", "alex": "alexander", "andy": "andrew",
	"bea": "beatrice", "ben": "benjamin", "beth": "elizabeth", "bill": "william",
	"bob": "robert", "cathy": "catherine", "charlie": "charles", "chris": "christopher",
	"dan": "daniel", "dave": "david", "deb": "deborah", "dick": "richard",
	"don": "donald", "ed": "edward", "fred": "frederick", "gabe": "gabriel",
	"greg": "gregory", "jack": "john", "jake": "jacob", "jen": "jennifer",
	"jim": "james", "joe": "joseph", "josh": "joshua", "ken": "kenneth",
	"kate": "katherine", "kathy": "katherine", "larry": "lawrence", "liz": "elizabeth",
	"matt": "matthew", "meg": "margaret", "mike": "michael", "nate": "nathaniel",
	"nick": "nicholas", "pat": "patrick", "pete": "peter", "phil": "philip",
	"rob": "robert", "ron": "ronald", "sam": "samuel", "steve": "stephen",
	"sue": "susan", "tom": "thomas", "tony": "anthony", "vicky": "victoria",
	"will": "william", "zach": "zachary",
}

// sameNickname reports whether two normalized first names are the familiar and
// formal forms of one name.
func sameNickname(a, b string) bool {
	if nicknames[a] == b || nicknames[b] == a {
		return true
	}
	// Both may be nicknames of one formal name (bill/will → william).
	if fa, ok := nicknames[a]; ok {
		if fb, ok2 := nicknames[b]; ok2 && fa == fb {
			return true
		}
	}
	return false
}

// jaroWinkler returns the Jaro-Winkler similarity of two strings in [0,1].
// It rewards a shared prefix, which suits transcription variants: they almost
// always begin correctly and drift in the middle ("imran" vs "imron").
func jaroWinkler(a, b string) float64 {
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

// asrVariantThreshold is the Jaro-Winkler score above which two first names are
// treated as the same name misheard. It is set high: merging two distinct
// people is far worse than leaving one person recorded under two spellings,
// since the latter is visible and correctable while the former silently
// attributes one person's words to another.
const asrVariantThreshold = 0.90

// SameName reports whether two names refer to the same person, tolerating
// transcription variants and familiar forms.
func SameName(a, b string) bool {
	na, nb := NormalizeName(a), NormalizeName(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}

	// When both names carry a surname, the surname decides. Comparing given
	// names alone was right while transcripts offered nothing else, but a
	// conferencing platform writes full names, and "Chris Banner" and
	// "Christopher Stookey" are two colleagues rather than one spelled two
	// ways. Merging them would attribute one person's words to the other,
	// which is the failure this whole area is built to avoid.
	sa, sb := Surname(na), Surname(nb)
	if sa != "" && sb != "" && sa != sb {
		return false
	}

	fa, fb := FirstName(na), FirstName(nb)
	if fa == fb {
		return true
	}
	if sameNickname(fa, fb) {
		return true
	}

	// A misheard name keeps its first letter and its rough length; requiring
	// both keeps "jon"/"ron" and "dan"/"dana" apart.
	if fa[0] != fb[0] {
		return false
	}
	if abs(len([]rune(fa))-len([]rune(fb))) > 2 {
		return false
	}
	// Very short names have too little signal for edit-distance matching.
	if len([]rune(fa)) < 4 || len([]rune(fb)) < 4 {
		return false
	}
	return jaroWinkler(fa, fb) >= asrVariantThreshold
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
