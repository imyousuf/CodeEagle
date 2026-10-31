package relations

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// snapshotVersion is the layout of a library snapshot. Bump it when a type
// the snapshot holds changes in a way an older snapshot would decode wrongly
// rather than merely miss.
const snapshotVersion = 1

// librarySnapshot is what a library reads from the graph: the documents
// with their sections, the meeting headers and the relations.
type librarySnapshot struct {
	Version   int
	Stamp     time.Time
	Subjects  map[string]*Subject
	Meetings  map[string]*Meeting
	Relations map[string]*Relation
}

// SaveSnapshot writes what the library read from the graph, marked with the
// database state it was read under, so a later process can start from it
// instead of reading every document again. It writes a temporary file and
// renames it, so a reader never sees half a snapshot.
func (l *Library) SaveSnapshot(path string, stamp time.Time) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	snap := librarySnapshot{Version: snapshotVersion, Stamp: stamp, Subjects: l.Subjects, Meetings: l.Meetings, Relations: l.Relations}
	if err := gob.NewEncoder(w).Encode(&snap); err != nil {
		tmp.Close()
		return fmt.Errorf("encode library snapshot: %w", err)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadSnapshot reads a library snapshot taken under stamp. It reports false
// when there is none, it was taken under another database state, or it
// cannot be read; the caller then reads the graph.
func LoadSnapshot(path string, stamp time.Time) (*Library, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var snap librarySnapshot
	if err := gob.NewDecoder(bufio.NewReader(f)).Decode(&snap); err != nil {
		return nil, false
	}
	if snap.Version != snapshotVersion || !snap.Stamp.Equal(stamp) || stamp.IsZero() {
		return nil, false
	}
	return &Library{
		Subjects:    orEmpty(snap.Subjects),
		Meetings:    orEmpty(snap.Meetings),
		Relations:   orEmpty(snap.Relations),
		Transcripts: NewFileTranscripts(),
		Now:         time.Now,
	}, true
}

func orEmpty[V any](m map[string]V) map[string]V {
	if m == nil {
		return map[string]V{}
	}
	return m
}
