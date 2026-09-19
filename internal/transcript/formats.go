package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Meetings are recorded by whatever tool the participants happened to use, and
// each writes a different file. A local recorder produces diarized JSON; Teams
// exports a Word document or a caption file; Zoom writes WebVTT or SRT. They
// describe the same thing — who spoke, when, and what they said — so they are
// read through one interface and become the same Session.
//
// One difference matters enough to carry through the whole pipeline. A
// diarized recording knows only that it heard distinct voices and labels them
// "Person 1", "Person 2"; a conferencing platform knows who was in the call and
// writes their names. Where the names are already there, the identification
// machinery has nothing to work out, and spending a model call to rediscover
// what the file states would be both wasteful and less accurate.

// Format reads one transcript file layout.
type Format interface {
	// Name identifies the format in logs and errors.
	Name() string

	// Matches reports, from the path alone, whether this format might apply.
	// It is a cheap filter so that scanning a directory does not read every
	// file it contains.
	Matches(path string) bool

	// Detect confirms from the content that this format applies. It is only
	// called when Matches returned true.
	Detect(data []byte) bool

	// Parse builds a Session. Formats that carry real speaker names must set
	// Session.NamedSpeakers.
	Parse(path string, data []byte) (*Session, error)
}

var (
	formatMu sync.RWMutex
	formats  []Format
)

// RegisterFormat adds a transcript format. Formats are consulted in
// registration order, so a more specific one should register first.
func RegisterFormat(f Format) {
	formatMu.Lock()
	defer formatMu.Unlock()
	formats = append(formats, f)
}

// Formats returns the registered formats.
func Formats() []Format {
	formatMu.RLock()
	defer formatMu.RUnlock()
	out := make([]Format, len(formats))
	copy(out, formats)
	return out
}

// FormatNames lists the registered format names, for help text and errors.
func FormatNames() []string {
	out := make([]string, 0, len(formats))
	for _, f := range Formats() {
		out = append(out, f.Name())
	}
	sort.Strings(out)
	return out
}

// MayBeTranscript reports whether a path is worth reading, from its name alone.
func MayBeTranscript(path string) bool {
	for _, f := range Formats() {
		if f.Matches(path) {
			return true
		}
	}
	return false
}

// FormatFor returns the format that handles this file, or nil.
func FormatFor(path string, data []byte) Format {
	for _, f := range Formats() {
		if f.Matches(path) && f.Detect(data) {
			return f
		}
	}
	return nil
}

// ParseAny reads a transcript in whichever format it is written.
func ParseAny(path string, data []byte) (*Session, error) {
	f := FormatFor(path, data)
	if f == nil {
		return nil, fmt.Errorf("%s is not a transcript in any known format (%s)",
			filepath.Base(path), strings.Join(FormatNames(), ", "))
	}
	s, err := f.Parse(path, data)
	if err != nil {
		return nil, fmt.Errorf("%s: parse as %s: %w", filepath.Base(path), f.Name(), err)
	}
	s.Path = path
	s.Format = f.Name()
	if s.ID == "" {
		s.ID = idFromPath(path)
	}
	if s.CreatedAt.IsZero() {
		// A caption file carries no date. The filename is tried first because
		// tools stamp the meeting time into it; the file's own timestamp is the
		// last resort, since it records when the export was written rather than
		// when the meeting happened. Without either, the meeting would land at
		// year zero and disappear from every temporal query.
		if t, ok := timeFromName(path); ok {
			s.CreatedAt = t
		} else if info, err := os.Stat(path); err == nil {
			s.CreatedAt = info.ModTime()
		}
	}
	return s, nil
}

// idFromPath derives a stable session id for a format that carries none.
//
// The path is what identifies such a recording: moving or renaming the file
// makes it a different meeting as far as the graph is concerned, which is the
// same rule the rest of the indexer follows for files.
func idFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return slugify(base)
}

// slugify reduces a filename to a compact identifier.
func slugify(s string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// hasExt reports whether a path carries one of the given extensions.
func hasExt(path string, exts ...string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// parseClock reads a caption timestamp — "h:mm:ss.mmm", "mm:ss", "h:mm:ss" —
// and returns it as seconds. Formats differ in how many components they write
// and whether they separate fractions with a dot or a comma.
func parseClock(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.Replace(s, ",", ".", 1)

	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}

	var total float64
	for i, p := range parts {
		var v float64
		if _, err := fmt.Sscanf(p, "%g", &v); err != nil {
			return 0, false
		}
		if v < 0 {
			return 0, false
		}
		// The last component may carry a fraction; the others must be whole.
		if i < len(parts)-1 && v != float64(int(v)) {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}

// cleanRunTogether repairs text whose sentence boundaries lost their spacing.
//
// Word stores each sentence of an utterance as a separate run, and extracting
// the runs concatenates them directly: "Hello?What is this?Oh boy". Restoring
// the space keeps the text readable and keeps quote matching working, since a
// quote is compared against this text.
func cleanRunTogether(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		b.WriteRune(r)
		if i+1 >= len(runes) {
			continue
		}
		next := runes[i+1]
		isTerminator := r == '.' || r == '?' || r == '!'
		startsSentence := next >= 'A' && next <= 'Z'
		if isTerminator && startsSentence {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// timeFromName pulls a meeting time out of a filename.
//
// Conferencing tools stamp the time into the name — Teams writes
// "...-20260710_140714UTC-...", Zoom writes "GMT20260918-080717" — and that is
// more trustworthy than the file's modification time, which reflects when it
// was downloaded.
func timeFromName(path string) (time.Time, bool) {
	base := filepath.Base(path)

	layouts := []struct {
		re     string
		layout string
	}{
		{`(\d{8}_\d{6})UTC`, "20060102_150405"},
		{`GMT(\d{8}-\d{6})`, "20060102-150405"},
		{`(\d{4}-\d{2}-\d{2}[ _T]\d{2}[-.]\d{2}[-.]\d{2})`, "2006-01-02 15-04-05"},
	}
	for _, l := range layouts {
		m := findSubmatch(l.re, base)
		if m == "" {
			continue
		}
		normalized := strings.NewReplacer("_", "_", "T", " ", ".", "-").Replace(m)
		if t, err := time.Parse(l.layout, normalized); err == nil {
			return t.UTC(), true
		}
		if t, err := time.Parse(l.layout, m); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// submatchCache compiles each filename pattern once. The patterns are fixed,
// and discovery calls these for every candidate file.
var (
	submatchMu    sync.Mutex
	submatchCache = map[string]*regexp.Regexp{}
)

// findSubmatch returns the first capture group of pattern in s, or "".
func findSubmatch(pattern, s string) string {
	submatchMu.Lock()
	re, ok := submatchCache[pattern]
	if !ok {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			submatchCache[pattern] = nil
			submatchMu.Unlock()
			return ""
		}
		submatchCache[pattern] = re
	}
	submatchMu.Unlock()

	if re == nil {
		return ""
	}
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
