package vectorstore_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/dgraph-io/badger/v4"

	"github.com/imyousuf/CodeEagle/internal/vectorstore"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
)

// These helpers drive the store through its exported API only, and read or
// write its files directly after it is closed, so the tests that use them
// survive the store's internals moving.

// testBranch is the branch every store in these tests indexes.
const testBranch = "main"

// fakeEmbedder gives every distinct text its own direction, derived from a
// hash of the text, so the same text always embeds to the same vector and a
// query equal to an indexed text finds it at distance zero. Queries embed
// exactly like documents.
type fakeEmbedder struct {
	name, model string
	dims        int

	mu    sync.Mutex
	texts int // chunks embedded, queries excluded
}

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{name: "fake", model: "fake-embed-v1", dims: 8}
}

func (e *fakeEmbedder) vec(text string) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text))
	seed := h.Sum64()
	v := make([]float32, e.dims)
	var norm float64
	for i := range v {
		seed = seed*6364136223846793005 + 1442695040888963407
		v[i] = float32(int64(seed>>33)%2001-1000) / 1000
		norm += float64(v[i]) * float64(v[i])
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(norm))
	}
	return v
}

func (e *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.texts += len(texts)
	e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.vec(t)
	}
	return out, nil
}

func (e *fakeEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return e.vec(text), nil
}

func (e *fakeEmbedder) Dimensions() int   { return e.dims }
func (e *fakeEmbedder) Name() string      { return e.name }
func (e *fakeEmbedder) ModelName() string { return e.model }

func (e *fakeEmbedder) embedded() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.texts
}

// memGraph is a minimal graph.Store: GetNode fails for a missing node, and
// QueryNodes filters by type only and returns nodes in ID order.
type memGraph struct {
	mu    sync.Mutex
	nodes map[string]*graph.Node
}

func newMemGraph(nodes ...*graph.Node) *memGraph {
	g := &memGraph{nodes: make(map[string]*graph.Node)}
	for _, n := range nodes {
		g.nodes[n.ID] = n
	}
	return g
}

func (g *memGraph) AddNode(_ context.Context, n *graph.Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[n.ID] = n
	return nil
}

func (g *memGraph) UpdateNode(ctx context.Context, n *graph.Node) error { return g.AddNode(ctx, n) }

func (g *memGraph) DeleteNode(_ context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.nodes, id)
	return nil
}

func (g *memGraph) GetNode(_ context.Context, id string) (*graph.Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, fmt.Errorf("node %s not found", id)
	}
	return n, nil
}

func (g *memGraph) QueryNodes(_ context.Context, f graph.NodeFilter) ([]*graph.Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []*graph.Node
	for _, n := range g.nodes {
		if f.Type == "" || n.Type == f.Type {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (g *memGraph) AddEdge(context.Context, *graph.Edge) error { return nil }
func (g *memGraph) DeleteEdge(context.Context, string) error   { return nil }
func (g *memGraph) GetEdges(context.Context, string, graph.EdgeType) ([]*graph.Edge, error) {
	return nil, nil
}
func (g *memGraph) GetNeighbors(context.Context, string, graph.EdgeType, graph.Direction) ([]*graph.Node, error) {
	return nil, nil
}
func (g *memGraph) DeleteByFile(context.Context, string) error { return nil }
func (g *memGraph) Stats(context.Context) (*graph.GraphStats, error) {
	return &graph.GraphStats{}, nil
}
func (g *memGraph) Close() error { return nil }

// storePaths is where one store keeps its two files.
type storePaths struct{ idx, db string }

func newPaths(t *testing.T) storePaths {
	t.Helper()
	dir := t.TempDir()
	return storePaths{idx: filepath.Join(dir, "vec.idx"), db: filepath.Join(dir, "vec.db")}
}

// openStore opens a store on the paths and closes it when the test ends;
// closing it earlier is harmless.
func openStore(t *testing.T, g graph.Store, emb *fakeEmbedder, p storePaths) *vectorstore.VectorStore {
	t.Helper()
	vs, err := vectorstore.New(g, emb, testBranch, p.idx, p.db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { vs.Close() })
	return vs
}

// reopen opens a store on the paths and loads it, failing unless an index
// was there to load.
func reopen(t *testing.T, g graph.Store, emb *fakeEmbedder, p storePaths) *vectorstore.VectorStore {
	t.Helper()
	vs := openStore(t, g, emb, p)
	loaded, err := vs.Load()
	if err != nil || !loaded {
		t.Fatalf("Load = %v, %v; want an index", loaded, err)
	}
	return vs
}

func mustSaveClose(t *testing.T, vs *vectorstore.VectorStore) {
	t.Helper()
	if err := vs.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := vs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// rawKV is one key and value read straight from the vector database.
type rawKV struct{ key, value string }

func openRaw(t *testing.T, dbPath string) *badger.DB {
	t.Helper()
	db, err := badger.Open(badger.DefaultOptions(dbPath).WithLogger(nil))
	if err != nil {
		t.Fatalf("open %s raw: %v", dbPath, err)
	}
	return db
}

// rawDump returns every key and value in the database, in key order.
func rawDump(t *testing.T, dbPath string) []rawKV {
	t.Helper()
	db := openRaw(t, dbPath)
	defer db.Close()
	var out []rawKV
	err := db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			v, err := it.Item().ValueCopy(nil)
			if err != nil {
				return err
			}
			out = append(out, rawKV{key: string(it.Item().KeyCopy(nil)), value: string(v)})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("dump %s: %v", dbPath, err)
	}
	return out
}

// rawMap is rawDump keyed by key.
func rawMap(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, kv := range rawDump(t, dbPath) {
		out[kv.key] = kv.value
	}
	return out
}

// rawWrite sets the given keys, and deletes those whose value is nil.
func rawWrite(t *testing.T, dbPath string, set map[string]*string) {
	t.Helper()
	db := openRaw(t, dbPath)
	defer db.Close()
	err := db.Update(func(txn *badger.Txn) error {
		for k, v := range set {
			if v == nil {
				if err := txn.Delete([]byte(k)); err != nil {
					return err
				}
				continue
			}
			if err := txn.Set([]byte(k), []byte(*v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("write %s: %v", dbPath, err)
	}
}

func strp(s string) *string { return &s }

// fnvHex is the hash the store records for a node's embedded text, computed
// here independently: FNV-64a, lowercase hex, unpadded.
func fnvHex(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return fmt.Sprintf("%x", h.Sum64())
}

// hnswHeader is the parameter block coder/hnsw v0.6.1 writes first.
type hnswHeader struct {
	version  int
	m        int
	ml       float64
	efSearch int
	distance string
}

// hnswNode is one node of one layer of an exported graph.
type hnswNode struct {
	vec       []float32
	neighbors []string
}

// readHNSW parses a vec.idx as coder/hnsw v0.6.1's Export writes it: signed
// varints for integers, a varint length before each string and vector,
// little-endian float32 vector elements and a little-endian float64 Ml. The
// first layer is the base layer, which holds every node.
func readHNSW(t *testing.T, path string) (hnswHeader, []map[string]hnswNode) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	r := bufio.NewReader(f)

	fail := func(what string, err error) {
		t.Helper()
		t.Fatalf("read %s from %s: %v", what, path, err)
	}
	varint := func(what string) int {
		t.Helper()
		v, err := binary.ReadVarint(r)
		if err != nil {
			fail(what, err)
		}
		return int(v)
	}
	str := func(what string) string {
		t.Helper()
		b := make([]byte, varint(what+" length"))
		if _, err := io.ReadFull(r, b); err != nil {
			fail(what, err)
		}
		return string(b)
	}

	var h hnswHeader
	h.version = varint("encoding version")
	h.m = varint("M")
	if err := binary.Read(r, binary.LittleEndian, &h.ml); err != nil {
		fail("Ml", err)
	}
	h.efSearch = varint("EfSearch")
	h.distance = str("distance name")

	layers := make([]map[string]hnswNode, varint("layer count"))
	for l := range layers {
		count := varint("node count")
		layers[l] = make(map[string]hnswNode, count)
		for range count {
			key := str("node key")
			vec := make([]float32, varint("vector length"))
			if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
				fail("vector", err)
			}
			neighbors := make([]string, varint("neighbour count"))
			for i := range neighbors {
				neighbors[i] = str("neighbour key")
			}
			layers[l][key] = hnswNode{vec: vec, neighbors: neighbors}
		}
	}
	if _, err := r.ReadByte(); err != io.EOF {
		t.Fatalf("%s has bytes after the last layer", path)
	}
	return h, layers
}

// writeHNSW encodes a one-layer graph the way coder/hnsw v0.6.1 does, with
// every node a neighbour of every other, for seeding an index by hand.
func writeHNSW(t *testing.T, path string, vectors map[string][]float32) {
	t.Helper()
	var buf []byte
	putInt := func(v int) { buf = binary.AppendVarint(buf, int64(v)) }
	putStr := func(s string) { putInt(len(s)); buf = append(buf, s...) }

	putInt(1)  // encoding version
	putInt(16) // M
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(0.25))
	putInt(40) // EfSearch
	putStr("cosine")
	putInt(1) // layers

	keys := make([]string, 0, len(vectors))
	for k := range vectors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	putInt(len(keys))
	for _, k := range keys {
		putStr(k)
		putInt(len(vectors[k]))
		for _, x := range vectors[k] {
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(x))
		}
		putInt(len(keys) - 1)
		for _, other := range keys {
			if other != k {
				putStr(other)
			}
		}
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// assertCleanLayers checks that every neighbour a layer names is a node of
// that layer, and that every upper layer's nodes are in the base layer.
func assertCleanLayers(t *testing.T, layers []map[string]hnswNode) {
	t.Helper()
	for l, layer := range layers {
		for key, n := range layer {
			if _, ok := layers[0][key]; !ok {
				t.Errorf("layer %d holds %s, which the base layer lacks", l, key)
			}
			for _, nb := range n.neighbors {
				if _, ok := layer[nb]; !ok {
					t.Errorf("layer %d: %s links to %s, which is not in the layer", l, key, nb)
				}
			}
		}
	}
}
