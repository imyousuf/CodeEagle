package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/internal/parser"
	genericparser "github.com/imyousuf/CodeEagle/internal/parser/generic"
	"github.com/imyousuf/CodeEagle/internal/parser/golang"
	"github.com/imyousuf/CodeEagle/internal/watcher"
)

const sampleVTT = `WEBVTT

00:00:01.000 --> 00:00:04.000
<v Kevin Mitchell>Morning — shall we start with the deploy?

00:00:04.500 --> 00:00:09.000
<v Mona Patel>Yes. The staging rollout finished overnight.
`

// TestMarkTranscripts covers flagging a document that is also a meeting
// transcript. The flag is what lets meeting indexing find a transcript that
// lives among the code rather than in a configured directory, so the file ends
// up in both indexes instead of having to be claimed by one of them.
func TestMarkTranscripts(t *testing.T) {
	tests := []struct {
		name       string
		mark       bool
		file       string
		content    string
		wantMarked bool
		wantFormat string
	}{
		{
			name:       "a transcript is marked",
			mark:       true,
			file:       "standup.vtt",
			content:    sampleVTT,
			wantMarked: true,
			wantFormat: "webvtt",
		},
		{
			// Ordinary prose must not be dragged into meeting indexing.
			name:    "ordinary prose is not",
			mark:    true,
			file:    "NOTES.md",
			content: "# Notes\n\nNothing was said out loud.\n",
		},
		{
			// A .vtt extension alone is not enough; the content decides.
			name:    "a file that only looks like one is not",
			mark:    true,
			file:    "empty.vtt",
			content: "this is not a caption file\n",
		},
		{
			name:    "nothing is marked when marking is off",
			mark:    false,
			file:    "standup.vtt",
			content: sampleVTT,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, store := setupMarkingIndexer(t, tt.mark)
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			if err := idx.IndexFile(ctx, path); err != nil {
				t.Fatalf("IndexFile: %v", err)
			}

			node := findFileNode(ctx, t, store, tt.file)
			if node == nil {
				t.Fatalf("no node indexed for %s", tt.file)
			}
			marked := node.Properties[graph.PropIsTranscript] == "true"
			if marked != tt.wantMarked {
				t.Errorf("is_transcript = %v, want %v", marked, tt.wantMarked)
			}
			if got := node.Properties[graph.PropTranscriptFormat]; got != tt.wantFormat {
				t.Errorf("transcript_format = %q, want %q", got, tt.wantFormat)
			}
		})
	}
}

func setupMarkingIndexer(t *testing.T, mark bool) (*Indexer, graph.Store) {
	t.Helper()

	store, err := embedded.NewStore(filepath.Join(t.TempDir(), "testdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	registry := parser.NewRegistry()
	registry.Register(golang.NewParser())
	registry.SetFallback(genericparser.NewGenericParser(nil, nil, nil, 0))

	idx := NewIndexer(IndexerConfig{
		MarkTranscripts: mark,
		GraphStore:      store,
		ParserRegistry:  registry,
		WatcherConfig:   &watcher.WatcherConfig{},
	})
	return idx, store
}

// findFileNode returns the indexed node for a filename, whichever file-like
// type it was stored as.
func findFileNode(ctx context.Context, t *testing.T, store graph.Store, name string) *graph.Node {
	t.Helper()

	for _, typ := range []graph.NodeType{graph.NodeDocument, graph.NodeFile, graph.NodeTestFile} {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: typ})
		if err != nil {
			t.Fatalf("query %s: %v", typ, err)
		}
		for _, n := range nodes {
			if n.Name == name {
				return n
			}
		}
	}
	return nil
}
