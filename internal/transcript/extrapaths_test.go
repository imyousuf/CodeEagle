package transcript

import (
	"path/filepath"
	"testing"
)

// TestAppendUnseenPaths covers merging named transcripts into the discovered
// set. A transcript that is both in a configured directory and marked during
// document indexing arrives twice, and must still be indexed once.
func TestAppendUnseenPaths(t *testing.T) {
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	tests := []struct {
		name  string
		found []string
		extra []string
		want  []string
	}{
		{
			name:  "nothing to add",
			found: []string{"/a/one.json"},
			want:  []string{"/a/one.json"},
		},
		{
			name:  "adds a path discovery missed",
			found: []string{"/a/one.json"},
			extra: []string{"/b/two.vtt"},
			want:  []string{"/a/one.json", "/b/two.vtt"},
		},
		{
			name:  "skips one already discovered",
			found: []string{"/a/one.json"},
			extra: []string{"/a/one.json", "/b/two.vtt"},
			want:  []string{"/a/one.json", "/b/two.vtt"},
		},
		{
			name:  "skips a repeat among the extras",
			extra: []string{"/b/two.vtt", "/b/two.vtt"},
			want:  []string{"/b/two.vtt"},
		},
		{
			name:  "ignores blanks",
			extra: []string{"", "/b/two.vtt"},
			want:  []string{"/b/two.vtt"},
		},
		{
			name: "matches a discovered path written differently",
			// The same file, spelled two ways.
			found: []string{abs("testdata/x.vtt")},
			extra: []string{"testdata/x.vtt"},
			want:  []string{abs("testdata/x.vtt")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendUnseenPaths(tt.found, tt.extra)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
