package transcript

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// WebVTT and SRT are the caption formats conferencing tools export. Both are a
// sequence of cues — a time range and a line of text — and both carry the
// speaker's real name, either in a "<v Name>" tag or as a "Name:" prefix.
//
// Cues are short by design, a few seconds each, so one turn of speech arrives
// as several consecutive cues from the same person. They are merged back into
// utterances here, because a summary drawn from four-second fragments reads as
// fragments.

func init() {
	RegisterFormat(&vttFormat{})
	RegisterFormat(&srtFormat{})
}

// --- WebVTT ---

type vttFormat struct{}

func (f *vttFormat) Name() string { return "webvtt" }

func (f *vttFormat) Matches(path string) bool { return hasExt(path, ".vtt") }

func (f *vttFormat) Detect(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(data, "\ufeff \t\r\n"), []byte("WEBVTT"))
}

func (f *vttFormat) Parse(path string, data []byte) (*Session, error) {
	cues, err := parseCues(string(data), vttTimeLine)
	if err != nil {
		return nil, err
	}
	return sessionFromCues(path, cues, f.Name())
}

// --- SRT ---

type srtFormat struct{}

func (f *srtFormat) Name() string { return "srt" }

func (f *srtFormat) Matches(path string) bool { return hasExt(path, ".srt") }

func (f *srtFormat) Detect(data []byte) bool {
	// An SRT opens with a cue number followed by a time range. There is no
	// magic header, so the shape of the first cue is the only signal.
	head := string(data)
	if len(head) > 512 {
		head = head[:512]
	}
	return srtOpening.MatchString(strings.TrimLeft(head, "\ufeff \t\r\n"))
}

func (f *srtFormat) Parse(path string, data []byte) (*Session, error) {
	cues, err := parseCues(string(data), srtTimeLine)
	if err != nil {
		return nil, err
	}
	return sessionFromCues(path, cues, f.Name())
}

var (
	// "00:00:01.000 --> 00:00:04.000" with optional cue settings after.
	vttTimeLine = regexp.MustCompile(`^\s*(\d{1,2}:\d{2}(?::\d{2})?(?:[.,]\d{1,3})?)\s*-->\s*(\d{1,2}:\d{2}(?::\d{2})?(?:[.,]\d{1,3})?)`)
	// SRT is the same shape; commas are the usual fraction separator.
	srtTimeLine = vttTimeLine
	srtOpening  = regexp.MustCompile(`^\d+\s*\r?\n\s*\d{1,2}:\d{2}`)

	// Teams tags the speaker: <v Kevin Smith>text</v>
	voiceTag = regexp.MustCompile(`<v\s+([^>]+?)\s*>(.*?)(?:</v>)?$`)
	// Zoom prefixes the line: "Kevin Smith: text"
	speakerPrefix = regexp.MustCompile(`^([^:]{1,60}):\s+(.*)$`)
	// Any other markup in a cue is presentation, not content.
	captionTag = regexp.MustCompile(`<[^>]*>`)
)

// cue is one caption entry.
type cue struct {
	start, end float64
	speaker    string
	text       string
}

// parseCues reads the cue stream shared by WebVTT and SRT.
func parseCues(body string, timeLine *regexp.Regexp) ([]cue, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	var cues []cue
	for i := 0; i < len(lines); i++ {
		m := timeLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		start, okStart := parseClock(m[1])
		end, okEnd := parseClock(m[2])
		if !okStart || !okEnd {
			continue
		}

		// The cue's text runs until the next blank line.
		var parts []string
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != ""; i++ {
			parts = append(parts, lines[i])
		}
		if len(parts) == 0 {
			continue
		}

		speaker, text := splitSpeaker(strings.Join(parts, " "))
		if text == "" {
			continue
		}
		cues = append(cues, cue{start: start, end: end, speaker: speaker, text: text})
	}

	if len(cues) == 0 {
		return nil, fmt.Errorf("no caption cues found")
	}
	return cues, nil
}

// splitSpeaker pulls the speaker's name off a cue, however the tool wrote it.
func splitSpeaker(line string) (speaker, text string) {
	line = strings.TrimSpace(line)

	if m := voiceTag.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(captionTag.ReplaceAllString(m[2], ""))
	}

	line = strings.TrimSpace(captionTag.ReplaceAllString(line, ""))
	if m := speakerPrefix.FindStringSubmatch(line); m != nil {
		name := strings.TrimSpace(m[1])
		// A timestamp or a sentence containing a colon is not a name.
		if looksLikeSpeakerName(name) {
			return name, strings.TrimSpace(m[2])
		}
	}
	return "", line
}

// nameParticles are the lowercase words that legitimately appear inside a
// name, so requiring title case does not reject "Ludwig van Beethoven".
var nameParticles = map[string]bool{
	"van": true, "von": true, "de": true, "del": true, "della": true,
	"di": true, "da": true, "der": true, "den": true, "bin": true,
	"ibn": true, "al": true, "la": true, "le": true, "of": true,
}

// looksLikeSpeakerName reports whether the text before a colon is a name.
//
// Plenty of ordinary speech puts a clause before a colon — "So here's the
// thing: we ship Friday" — and reading that as a speaker would attribute the
// sentence to a person who does not exist. Names are written in title case and
// clauses are not, which separates the two reliably enough.
func looksLikeSpeakerName(s string) bool {
	if s == "" || len(s) > 60 {
		return false
	}
	if strings.ContainsAny(s, "?!,;") {
		return false
	}
	words := strings.Fields(s)
	if len(words) == 0 || len(words) > 5 {
		return false
	}

	for i, w := range words {
		// A period belongs to an initial or a short title — "J.", "Dr.",
		// "MD." — not to the end of a sentence.
		if strings.Contains(w, ".") && len([]rune(w)) > 4 {
			return false
		}
		lower := strings.ToLower(strings.Trim(w, "."))
		if i > 0 && nameParticles[lower] {
			continue
		}
		r := []rune(w)[0]
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// sessionFromCues merges consecutive cues from one speaker into utterances.
func sessionFromCues(path string, cues []cue, format string) (*Session, error) {
	s := &Session{
		Title:  titleFromPath(path),
		Format: format,
	}

	// Cues are a few seconds each, so one turn arrives as several. Merging
	// them restores utterances; a summary built from fragments reads as one.
	const maxCueGap = 2.0
	named := false
	for _, c := range cues {
		if c.speaker != "" {
			named = true
		}
		speaker := c.speaker
		if speaker == "" {
			speaker = unknownSpeaker
		}

		if n := len(s.Segments); n > 0 {
			prev := &s.Segments[n-1]
			if prev.Speaker == speaker && c.start-prev.EndTime <= maxCueGap {
				prev.Text += " " + c.text
				prev.EndTime = c.end
				continue
			}
		}
		s.Segments = append(s.Segments, Segment{
			ID:        fmt.Sprintf("cue-%d", len(s.Segments)+1),
			Speaker:   speaker,
			Text:      c.text,
			StartTime: c.start,
			EndTime:   c.end,
			Source:    SourceMonitor,
		})
	}

	s.NamedSpeakers = named
	if t, ok := timeFromName(path); ok {
		s.CreatedAt = t
	}
	if n := len(s.Segments); n > 0 {
		s.Duration = s.Segments[n-1].EndTime
		if !s.CreatedAt.IsZero() {
			s.EndedAt = s.CreatedAt.Add(time.Duration(s.Duration) * time.Second)
		}
	}
	return s, nil
}

// unknownSpeaker labels speech in a caption file that names nobody. It reads
// as a diarization label on purpose, because that is exactly what it is: an
// unattributed voice for identification to work on.
const unknownSpeaker = "Person 1"

// titleFromPath turns a filename into a readable title, which is all a caption
// file offers — it carries no metadata of its own.
func titleFromPath(path string) string {
	base := idFromPath(path)
	base = strings.ReplaceAll(base, "-", " ")
	// Tools append their own noise to the name.
	for _, suffix := range []string{" transcript", " recording", " meeting recording", " captions"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if base == "" {
		return "Meeting"
	}
	return strings.ToUpper(base[:1]) + base[1:]
}
