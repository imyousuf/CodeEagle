package transcript

import (
	"path/filepath"
	"strings"
)

// The local recorder writes one session.json per recording: diarized audio with
// speakers labelled "Person 1", "Person 2", and microphone audio labelled "You".
// It is the only format here whose speakers are anonymous, which is why the
// identification machinery exists at all.

func init() {
	RegisterFormat(&tomoeFormat{})
}

type tomoeFormat struct{}

func (f *tomoeFormat) Name() string { return "tomoe" }

func (f *tomoeFormat) Matches(path string) bool {
	return strings.EqualFold(filepath.Base(path), SessionFileName) || hasExt(path, ".json")
}

func (f *tomoeFormat) Detect(data []byte) bool { return LooksLikeSession(data) }

func (f *tomoeFormat) Parse(path string, data []byte) (*Session, error) {
	s, err := Parse(data, path)
	if err != nil {
		return nil, err
	}
	// Diarization labels are not names: this is the format identification has
	// to work on.
	s.NamedSpeakers = false
	return s, nil
}
