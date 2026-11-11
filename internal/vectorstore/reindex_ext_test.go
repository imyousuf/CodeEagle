package vectorstore_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// TestNeedsReindexFollowsTheStoredMeta drives NeedsReindex and ReindexReason
// from a real rebuild's metadata, reopened under a different embedder or
// with its text version rewritten on disk, rather than by assigning it.
func TestNeedsReindexFollowsTheStoredMeta(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph(&graph.Node{ID: "d1", Type: graph.NodeDocument, Name: "doc", DocComment: "retry policy"})

	// A store that has loaded nothing has no metadata, whatever is on disk.
	p := newPaths(t)
	vs := openStore(t, g, newFakeEmbedder(), p)
	if !vs.NeedsReindex() || vs.ReindexReason() != "no index metadata" {
		t.Errorf("new store: NeedsReindex = %v, reason %q; want true, %q", vs.NeedsReindex(), vs.ReindexReason(), "no index metadata")
	}
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if vs.NeedsReindex() {
		t.Errorf("NeedsReindex right after a rebuild: %s", vs.ReindexReason())
	}
	mustSaveClose(t, vs)
	unloaded := openStore(t, g, newFakeEmbedder(), p)
	if !unloaded.NeedsReindex() || unloaded.ReindexReason() != "no index metadata" {
		t.Errorf("reopened, not loaded: NeedsReindex = %v, reason %q", unloaded.NeedsReindex(), unloaded.ReindexReason())
	}
	unloaded.Close()

	tests := []struct {
		name        string
		emb         *fakeEmbedder
		textVersion *string // rewritten in the stored meta when set
		want        bool
		reason      string
	}{
		{name: "same embedder", emb: newFakeEmbedder(), want: false, reason: ""},
		{
			name: "different provider", emb: &fakeEmbedder{name: "other", model: "fake-embed-v1", dims: 8},
			want: true, reason: "index built with fake/fake-embed-v1, current provider is other/fake-embed-v1",
		},
		{
			name: "different model", emb: &fakeEmbedder{name: "fake", model: "fake-embed-v2", dims: 8},
			want: true, reason: "index built with fake/fake-embed-v1, current provider is fake/fake-embed-v2",
		},
		// Only the provider and model are compared; a change of size alone
		// is not noticed.
		{name: "different dimensions", emb: &fakeEmbedder{name: "fake", model: "fake-embed-v1", dims: 16}, want: false, reason: ""},
		{
			name: "earlier text version", emb: newFakeEmbedder(), textVersion: strp(`"text_version":1`),
			want: true, reason: "index built from embeddable text version 1, current is 2",
		},
		{
			name: "no text version recorded", emb: newFakeEmbedder(), textVersion: strp(""),
			want: true, reason: "index built from embeddable text version 0, current is 2",
		},
		{
			name: "later text version", emb: newFakeEmbedder(), textVersion: strp(`"text_version":3`),
			want: true, reason: "index built from embeddable text version 3, current is 2",
		},
		// A change of model is reported ahead of a change of text.
		{
			name: "different model and text version", emb: &fakeEmbedder{name: "fake", model: "fake-embed-v2", dims: 8},
			textVersion: strp(`"text_version":1`),
			want:        true, reason: "index built with fake/fake-embed-v1, current provider is fake/fake-embed-v2",
		},
	}
	textVersion := regexp.MustCompile(`,?"text_version":\d+`)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if vectorstore.EmbeddableTextVersion != 2 {
				t.Fatalf("EmbeddableTextVersion = %d; the reasons below are written for 2", vectorstore.EmbeddableTextVersion)
			}
			if tc.textVersion != nil {
				stored := rawMap(t, p.db)["meta:main"]
				rewritten := textVersion.ReplaceAllString(stored, "")
				if *tc.textVersion != "" {
					rewritten = rewritten[:len(rewritten)-1] + "," + *tc.textVersion + "}"
				}
				rawWrite(t, p.db, map[string]*string{"meta:main": &rewritten})
				t.Cleanup(func() { rawWrite(t, p.db, map[string]*string{"meta:main": &stored}) })
			}
			vs := openStore(t, g, tc.emb, p)
			defer vs.Close()
			if loaded, err := vs.Load(); err != nil || !loaded {
				t.Fatalf("Load = %v, %v", loaded, err)
			}
			if got := vs.NeedsReindex(); got != tc.want {
				t.Errorf("NeedsReindex = %v, want %v", got, tc.want)
			}
			if got := vs.ReindexReason(); got != tc.reason {
				t.Errorf("ReindexReason = %q, want %q", got, tc.reason)
			}
		})
	}
}

// TestRebuildUnderANewEmbedderRecordsIt: once a store reopened under a new
// model is rebuilt, its metadata names the new model, its version counts the
// rebuild, and it no longer needs one.
func TestRebuildUnderANewEmbedderRecordsIt(t *testing.T) {
	ctx := context.Background()
	g := newMemGraph(&graph.Node{ID: "d1", Type: graph.NodeDocument, Name: "doc", DocComment: "retry policy"})
	p := newPaths(t)
	vs := openStore(t, g, newFakeEmbedder(), p)
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	mustSaveClose(t, vs)
	created := vs.Meta().CreatedAt

	next := &fakeEmbedder{name: "fake", model: "fake-embed-v2", dims: 12}
	vs = openStore(t, g, next, p)
	if loaded, err := vs.Load(); err != nil || !loaded {
		t.Fatalf("Load = %v, %v", loaded, err)
	}
	if !vs.NeedsReindex() {
		t.Fatal("NeedsReindex = false under a new model")
	}
	if err := vs.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	m := vs.Meta()
	if m.Model != "fake-embed-v2" || m.Dimensions != 12 || m.Version != 3 || !m.CreatedAt.Equal(created) || m.Rebuilding {
		t.Errorf("Meta after rebuilding under a new model = %+v; want model v2, 12 dims, version 3, the original creation time", m)
	}
	if vs.NeedsReindex() {
		t.Errorf("NeedsReindex after the rebuild: %s", vs.ReindexReason())
	}
	if v, ok := vs.Vector("d1"); !ok || len(v) != 12 {
		t.Errorf("Vector(d1) = %d dims, %v; want 12", len(v), ok)
	}
}
