package search

import (
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

func TestDeduplicateResults(t *testing.T) {
	nodeA := &graph.Node{ID: "a", Name: "funcA"}
	nodeB := &graph.Node{ID: "b", Name: "funcB"}

	tests := []struct {
		name     string
		input    []vectorstore.SearchResult
		wantLen  int
		wantIDs  []string
		wantText string // expected chunk text for first result
	}{
		{
			name:    "empty",
			input:   nil,
			wantLen: 0,
		},
		{
			name: "no duplicates",
			input: []vectorstore.SearchResult{
				{Node: nodeA, Score: 0.9, ChunkText: "chunk1"},
				{Node: nodeB, Score: 0.8, ChunkText: "chunk2"},
			},
			wantLen: 2,
			wantIDs: []string{"a", "b"},
		},
		{
			name: "duplicate keeps higher score",
			input: []vectorstore.SearchResult{
				{Node: nodeA, Score: 0.5, ChunkText: "low"},
				{Node: nodeA, Score: 0.9, ChunkText: "high"},
				{Node: nodeB, Score: 0.8, ChunkText: "b"},
			},
			wantLen:  2,
			wantIDs:  []string{"a", "b"},
			wantText: "high",
		},
		{
			name: "nil nodes filtered",
			input: []vectorstore.SearchResult{
				{Node: nil, Score: 1.0},
				{Node: nodeA, Score: 0.9, ChunkText: "a"},
			},
			wantLen: 1,
			wantIDs: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeduplicateResults(tt.input)
			if len(got) != tt.wantLen {
				t.Fatalf("got %d results, want %d", len(got), tt.wantLen)
			}
			for i, id := range tt.wantIDs {
				if got[i].Node.ID != id {
					t.Errorf("result[%d].Node.ID = %q, want %q", i, got[i].Node.ID, id)
				}
			}
			if tt.wantText != "" && got[0].ChunkText != tt.wantText {
				t.Errorf("result[0].ChunkText = %q, want %q", got[0].ChunkText, tt.wantText)
			}
		})
	}
}

func TestChunkSnippet(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLines int
		want     string
	}{
		{
			name: "empty",
			text: "",
			want: "",
		},
		{
			name:     "single line",
			text:     "func main() {}",
			maxLines: 2,
			want:     "func main() {}",
		},
		{
			name:     "skips blank lines and code fences",
			text:     "```\n\nfunc foo() {\n\nreturn nil\n}\n```",
			maxLines: 2,
			want:     "func foo() { | return nil",
		},
		{
			name:     "limits to maxLines",
			text:     "line1\nline2\nline3\nline4",
			maxLines: 2,
			want:     "line1 | line2",
		},
		{
			name:     "truncates long result",
			text:     "a very long line " + string(make([]byte, 250)),
			maxLines: 2,
			// result is >200 chars, should be truncated
		},
		{
			name:     "only blank lines",
			text:     "\n\n\n",
			maxLines: 2,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChunkSnippet(tt.text, tt.maxLines)
			if tt.name == "truncates long result" {
				if len(got) > 200 {
					t.Errorf("expected truncation, got len %d", len(got))
				}
				if len(got) > 3 && got[len(got)-3:] != "..." {
					t.Errorf("expected trailing '...', got %q", got[len(got)-3:])
				}
				return
			}
			if got != tt.want {
				t.Errorf("ChunkSnippet(%q, %d) = %q, want %q", tt.text, tt.maxLines, got, tt.want)
			}
		})
	}
}

func TestRelativePath(t *testing.T) {
	tests := []struct {
		name      string
		filePath  string
		repoPaths []string
		want      string
	}{
		{
			name: "empty",
			want: "",
		},
		{
			name:      "makes relative",
			filePath:  "/home/user/project/src/main.go",
			repoPaths: []string{"/home/user/project"},
			want:      "src/main.go",
		},
		{
			name:      "no match returns original",
			filePath:  "/other/path/main.go",
			repoPaths: []string{"/home/user/project"},
			want:      "/other/path/main.go",
		},
		{
			name:      "tries multiple roots",
			filePath:  "/repo2/lib/util.go",
			repoPaths: []string{"/repo1", "/repo2"},
			want:      "lib/util.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RelativePath(tt.filePath, tt.repoPaths)
			if got != tt.want {
				t.Errorf("RelativePath(%q, %v) = %q, want %q", tt.filePath, tt.repoPaths, got, tt.want)
			}
		})
	}
}
