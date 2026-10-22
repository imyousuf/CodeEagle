package relations

import (
	"strings"
	"sync"

	"github.com/imyousuf/CodeEagle/internal/transcript"
)

// Turn is one speaker's uninterrupted stretch of a recording.
type Turn struct {
	Speaker string
	Text    string
	Start   float64
	End     float64
}

// Transcripts supplies what was actually said in a meeting. Segment summaries
// are a model's words; the judge and the brief need the speakers' own.
type Transcripts interface {
	// Span returns the turns that overlap [start, end] seconds.
	Span(m *Meeting, start, end float64) ([]Turn, error)
}

// FileTranscripts reads recordings from their session files, keeping the last
// few parsed: a run touches the same meeting for several documents in a row.
type FileTranscripts struct {
	mu    sync.Mutex
	cache map[string][]Turn
	order []string
}

// transcriptCacheSize bounds how many parsed recordings are kept.
const transcriptCacheSize = 16

// NewFileTranscripts returns a transcript reader over session files.
func NewFileTranscripts() *FileTranscripts {
	return &FileTranscripts{cache: make(map[string][]Turn)}
}

// Span returns the turns of m overlapping [start, end].
func (f *FileTranscripts) Span(m *Meeting, start, end float64) ([]Turn, error) {
	turns, err := f.turns(m.FilePath)
	if err != nil {
		return nil, err
	}
	var out []Turn
	for _, t := range turns {
		if t.End >= start && t.Start <= end {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *FileTranscripts) turns(path string) ([]Turn, error) {
	f.mu.Lock()
	if t, ok := f.cache[path]; ok {
		f.mu.Unlock()
		return t, nil
	}
	f.mu.Unlock()

	s, err := transcript.Load(path)
	if err != nil {
		return nil, err
	}
	var turns []Turn
	for _, t := range s.Turns() {
		turns = append(turns, Turn{Speaker: t.Speaker, Text: t.Text, Start: t.StartTime, End: t.EndTime})
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.cache[path]; !ok {
		f.cache[path] = turns
		f.order = append(f.order, path)
		if len(f.order) > transcriptCacheSize {
			delete(f.cache, f.order[0])
			f.order = f.order[1:]
		}
	}
	return turns, nil
}

// spokenText joins turns into plain text, clipped to about n bytes.
func spokenText(turns []Turn, n int) string {
	var parts []string
	for _, t := range turns {
		parts = append(parts, strings.Join(strings.Fields(t.Text), " "))
	}
	return excerpt(strings.Join(parts, " "), n)
}
