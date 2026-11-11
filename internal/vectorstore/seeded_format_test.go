package vectorstore_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// TestOpensAnIndexSeededInTodaysFormat writes vec.db entry by entry in the
// documented layout, as a previous release would have left it, beside a
// vec.idx from a previous save or encoded by hand, and checks that a store
// opens it, reads what it holds, and repairs only what is out of date.
func TestOpensAnIndexSeededInTodaysFormat(t *testing.T) {
	n1 := &graph.Node{ID: "n1", Type: graph.NodeDocument, Name: "retry.md", DocComment: "retry policy with backoff and jitter"}
	n2 := &graph.Node{ID: "n2", Type: graph.NodeFunction, Name: "Backoff", Signature: "func Backoff(n int) time.Duration"}
	gone := &graph.Node{ID: "gone", Type: graph.NodeDocument, Name: "old.md", DocComment: "a document since deleted"}
	n4 := &graph.Node{ID: "n4", Type: graph.NodeDecision, Name: "cap retries", DocComment: "We cap retries at five."}

	sources := []struct {
		name  string
		write func(t *testing.T, idxPath string)
	}{
		{
			name: "vec.idx from a previous save",
			write: func(t *testing.T, idxPath string) {
				prior := newPaths(t)
				vs := openStore(t, newMemGraph(), newFakeEmbedder(), prior)
				if err := vs.IndexNodes(context.Background(), []*graph.Node{n1, n2, gone}); err != nil {
					t.Fatalf("IndexNodes: %v", err)
				}
				mustSaveClose(t, vs)
				data, err := os.ReadFile(prior.idx)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(idxPath, data, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "vec.idx encoded by hand",
			write: func(t *testing.T, idxPath string) {
				emb := newFakeEmbedder()
				writeHNSW(t, idxPath, map[string][]float32{
					"n1:0":   emb.vec(vectorstore.EmbeddableText(n1)),
					"n2:0":   emb.vec(vectorstore.EmbeddableText(n2)),
					"gone:0": emb.vec(vectorstore.EmbeddableText(gone)),
				})
			},
		},
	}

	for _, src := range sources {
		t.Run(src.name, func(t *testing.T) {
			ctx := context.Background()
			p := newPaths(t)
			src.write(t, p.idx)

			const seededMeta = `{"provider":"fake","model":"fake-embed-v1","dimensions":8,"chunk_size":1500,"overlap":200,` +
				`"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z","node_count":3,"version":7,"text_version":2}`
			rawWrite(t, p.db, map[string]*string{
				// The stored text differs from what n1 would embed today, to show
				// that a search returns the stored chunk, not a recomputed one.
				"chunk:n1:0":   strp(`{"node_id":"n1","chunk_index":0,"chunk_text":"seeded <chunk> for n1"}`),
				"chunk:n2:0":   strp(`{"node_id":"n2","chunk_index":0,"chunk_text":"seeded chunk for n2"}`),
				"chunk:gone:0": strp(`{"node_id":"gone","chunk_index":0,"chunk_text":"seeded chunk for gone"}`),
				// n1's hash matches its text; n2's does not, as if it changed.
				"text:n1":   strp(fnvHex(vectorstore.EmbeddableText(n1))),
				"text:n2":   strp("0"),
				"text:gone": strp(fnvHex(vectorstore.EmbeddableText(gone))),
				"meta:main": strp(seededMeta),
			})

			g := newMemGraph(n1, n2, n4)
			emb := newFakeEmbedder()
			vs := reopen(t, g, emb, p)

			want := vectorstore.VectorIndexMeta{
				Provider: "fake", Model: "fake-embed-v1", Dimensions: 8, ChunkSize: 1500, Overlap: 200,
				CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				NodeCount: 3, Version: 7, TextVersion: 2,
			}
			got := vs.Meta()
			if got == nil {
				t.Fatal("Meta is nil after Load")
			}
			if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
				t.Errorf("Meta times = %v, %v; want %v", got.CreatedAt, got.UpdatedAt, want.CreatedAt)
			}
			g2 := *got
			g2.CreatedAt, g2.UpdatedAt = want.CreatedAt, want.UpdatedAt
			if g2 != want {
				t.Errorf("Meta = %+v, want %+v", g2, want)
			}
			if vs.NeedsReindex() || vs.ReindexReason() != "" {
				t.Errorf("NeedsReindex = %v (%q), want false", vs.NeedsReindex(), vs.ReindexReason())
			}
			if vs.Len() != 3 {
				t.Errorf("Len = %d, want 3", vs.Len())
			}

			results, err := vs.Search(ctx, vectorstore.EmbeddableText(n1), 1)
			if err != nil || len(results) != 1 {
				t.Fatalf("Search = %d results, %v", len(results), err)
			}
			if r := results[0]; r.Node.ID != "n1" || r.ChunkIndex != 0 || r.ChunkText != "seeded <chunk> for n1" {
				t.Errorf("Search top = %s:%d %q, want n1:0 with the seeded chunk text", r.Node.ID, r.ChunkIndex, r.ChunkText)
			}
			// A vector whose node is gone is skipped and counted.
			results, err = vs.Search(ctx, vectorstore.EmbeddableText(gone), 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 || vs.StaleInLastSearch() != 1 {
				t.Errorf("Search past a stale vector = %d results, %d stale; want 2 and 1", len(results), vs.StaleInLastSearch())
			}

			// gone is stale; n2's text changed and n4 was never embedded.
			stale, missing, err := vs.Staleness(ctx)
			if err != nil || stale != 1 || missing != 2 {
				t.Errorf("Staleness = %d stale, %d missing, %v; want 1, 2", stale, missing, err)
			}
			before := emb.embedded()
			added, removed, err := vs.Reconcile(ctx, 0)
			if err != nil || added != 2 || removed != 1 {
				t.Fatalf("Reconcile = %d added, %d removed, %v; want 2, 1", added, removed, err)
			}
			if n := emb.embedded() - before; n != 2 {
				t.Errorf("Reconcile embedded %d chunks, want 2 (n1 was current)", n)
			}
			if stale, missing, _ := vs.Staleness(ctx); stale != 0 || missing != 0 {
				t.Errorf("Staleness after Reconcile = %d, %d; want 0, 0", stale, missing)
			}
			if _, ok := vs.Vector("gone"); ok {
				t.Error("a stale vector survived Reconcile")
			}
			mustSaveClose(t, vs)

			after := rawMap(t, p.db)
			var keys []string
			for k := range after {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			wantKeys := []string{"chunk:n1:0", "chunk:n2:0", "chunk:n4:0", "meta:main", "text:n1", "text:n2", "text:n4"}
			if !slices.Equal(keys, wantKeys) {
				t.Errorf("keys after Reconcile and Save = %v, want %v", keys, wantKeys)
			}
			if v := after["chunk:n1:0"]; v != `{"node_id":"n1","chunk_index":0,"chunk_text":"seeded <chunk> for n1"}` {
				t.Errorf("a current chunk was rewritten: %s", v)
			}
			if v := after["text:n2"]; v != fnvHex(vectorstore.EmbeddableText(n2)) {
				t.Errorf("text:n2 = %q after re-embedding, want its text's hash", v)
			}
			var meta vectorstore.VectorIndexMeta
			if err := json.Unmarshal([]byte(after["meta:main"]), &meta); err != nil {
				t.Fatal(err)
			}
			// Save keeps the record and moves only its update time; reconcile
			// does not count nodes or bump the version.
			if meta.Version != 7 || meta.NodeCount != 3 || !meta.CreatedAt.Equal(want.CreatedAt) || !meta.UpdatedAt.After(want.UpdatedAt) {
				t.Errorf("meta after Save = %+v", meta)
			}

			_, layers := readHNSW(t, p.idx)
			base := make([]string, 0, len(layers[0]))
			for k := range layers[0] {
				base = append(base, k)
			}
			sort.Strings(base)
			if want := []string{"n1:0", "n2:0", "n4:0"}; !slices.Equal(base, want) {
				t.Errorf("vec.idx base layer = %v, want %v", base, want)
			}
			assertCleanLayers(t, layers)
		})
	}
}
