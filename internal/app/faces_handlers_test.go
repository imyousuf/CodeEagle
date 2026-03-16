//go:build app && faces

package app

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/faces"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func imageRect(minX, minY, maxX, maxY int) image.Rectangle {
	return image.Rect(minX, minY, maxX, maxY)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestApp creates an App backed by real BadgerDB stores in temp directories.
// The graph store is pre-created so openGraphRW() works.
func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".CodeEagle")

	// Pre-create the graph store so both openGraph and openGraphRW succeed.
	graphDBPath := filepath.Join(cfgDir, "graph.db")
	gs, err := embedded.NewBranchStore(graphDBPath, "default", []string{"default"})
	if err != nil {
		t.Fatalf("create graph store: %v", err)
	}
	gs.Close()

	cfg := &config.Config{
		ConfigDir: cfgDir,
		Project:   config.ProjectConfig{Name: "test"},
		Graph:     config.GraphConfig{Storage: "embedded"},
	}

	a := NewApp(cfg, nil, "", nil, nil)
	// Wire a no-op emitter (default from NewApp is already no-op).
	t.Cleanup(func() { a.Shutdown(nil) })
	return a
}

// seedFaceStore creates a face store with test data and returns the store path.
func seedFaceStore(t *testing.T, cfgDir string) {
	t.Helper()
	fsp := filepath.Join(cfgDir, "faces.db")
	fs, err := faces.OpenStore(fsp)
	if err != nil {
		t.Fatalf("open face store for seeding: %v", err)
	}
	defer fs.Close()

	// Cluster 1: 3 faces
	for i := 0; i < 3; i++ {
		emb := make([]float32, 4)
		emb[0] = float32(i) * 0.1
		if err := fs.StoreFace(&faces.FaceRecord{
			ImagePath:  "/img" + string(rune('A'+i)) + ".jpg",
			FaceIdx:    0,
			BBox:       image.Rect(10, 10, 50, 50),
			Confidence: 0.95,
			Embedding:  emb,
			ClusterID:  1,
		}); err != nil {
			t.Fatalf("store face: %v", err)
		}
	}

	// Cluster 2: 2 faces
	for i := 0; i < 2; i++ {
		emb := make([]float32, 4)
		emb[1] = float32(i) * 0.1
		if err := fs.StoreFace(&faces.FaceRecord{
			ImagePath:  "/img" + string(rune('D'+i)) + ".jpg",
			FaceIdx:    0,
			BBox:       image.Rect(20, 20, 60, 60),
			Confidence: 0.90,
			Embedding:  emb,
			ClusterID:  2,
		}); err != nil {
			t.Fatalf("store face: %v", err)
		}
	}

	// Noise face (cluster -1)
	if err := fs.StoreFace(&faces.FaceRecord{
		ImagePath:  "/noise.jpg",
		FaceIdx:    0,
		BBox:       image.Rect(0, 0, 30, 30),
		Confidence: 0.50,
		Embedding:  []float32{0.9, 0.9, 0.9, 0.9},
		ClusterID:  -1,
	}); err != nil {
		t.Fatalf("store noise face: %v", err)
	}

	// Set a label for cluster 1.
	if err := fs.SetLabel(1, "Alice"); err != nil {
		t.Fatalf("set label: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Person CRUD tests (with real BadgerDB)
// ---------------------------------------------------------------------------

func TestCreatePerson(t *testing.T) {
	a := newTestApp(t)

	p, err := a.CreatePerson("Alice", []string{"friend"})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	if p.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", p.Name)
	}
	if len(p.Relationships) != 1 || p.Relationships[0] != "friend" {
		t.Errorf("Relationships = %v, want [friend]", p.Relationships)
	}
	if p.ID == "" {
		t.Error("ID should be set")
	}
	if p.CreatedAt == "" {
		t.Error("CreatedAt should be set")
	}
}

func TestCreatePersonDuplicate(t *testing.T) {
	a := newTestApp(t)

	if _, err := a.CreatePerson("Alice", nil); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := a.CreatePerson("Alice", nil)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestGetPersons(t *testing.T) {
	a := newTestApp(t)

	if _, err := a.CreatePerson("Alice", nil); err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	if _, err := a.CreatePerson("Bob", []string{"colleague"}); err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	persons, err := a.GetPersons()
	if err != nil {
		t.Fatalf("GetPersons: %v", err)
	}
	if len(persons) != 2 {
		t.Fatalf("len(persons) = %d, want 2", len(persons))
	}

	names := map[string]bool{}
	for _, p := range persons {
		names[p.Name] = true
	}
	if !names["Alice"] || !names["Bob"] {
		t.Errorf("expected Alice and Bob, got %v", names)
	}
}

func TestUpdatePerson(t *testing.T) {
	a := newTestApp(t)

	p, err := a.CreatePerson("Alice", nil)
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	if err := a.UpdatePerson(p.ID, "Alice Smith", []string{"wife"}); err != nil {
		t.Fatalf("UpdatePerson: %v", err)
	}

	persons, err := a.GetPersons()
	if err != nil {
		t.Fatalf("GetPersons: %v", err)
	}
	if len(persons) != 1 {
		t.Fatalf("len(persons) = %d, want 1", len(persons))
	}
	if persons[0].Name != "Alice Smith" {
		t.Errorf("Name = %q, want Alice Smith", persons[0].Name)
	}
	if len(persons[0].Relationships) != 1 || persons[0].Relationships[0] != "wife" {
		t.Errorf("Relationships = %v, want [wife]", persons[0].Relationships)
	}
}

func TestDeletePerson(t *testing.T) {
	a := newTestApp(t)

	p, err := a.CreatePerson("Alice", nil)
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	if err := a.DeletePerson(p.ID); err != nil {
		t.Fatalf("DeletePerson: %v", err)
	}

	persons, err := a.GetPersons()
	if err != nil {
		t.Fatalf("GetPersons: %v", err)
	}
	if len(persons) != 0 {
		t.Errorf("len(persons) = %d, want 0", len(persons))
	}
}

func TestDeletePersonNotFound(t *testing.T) {
	a := newTestApp(t)

	err := a.DeletePerson("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent person")
	}
}

// ---------------------------------------------------------------------------
// Face store singleton tests
// ---------------------------------------------------------------------------

func TestGetFaceStoreSingleton(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	fs1, err := a.getFaceStore()
	if err != nil {
		t.Fatalf("getFaceStore 1: %v", err)
	}
	fs2, err := a.getFaceStore()
	if err != nil {
		t.Fatalf("getFaceStore 2: %v", err)
	}
	if fs1 != fs2 {
		t.Error("getFaceStore should return the same instance")
	}
}

// ---------------------------------------------------------------------------
// Cluster handler tests (with real BadgerDB face store)
// ---------------------------------------------------------------------------

func TestGetClusters(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	clusters, err := a.GetClusters()
	if err != nil {
		t.Fatalf("GetClusters: %v", err)
	}

	if len(clusters) != 2 {
		t.Fatalf("len(clusters) = %d, want 2", len(clusters))
	}

	// Should be sorted by face count descending.
	if clusters[0].FaceCount < clusters[1].FaceCount {
		t.Error("clusters should be sorted by face count descending")
	}
	if clusters[0].FaceCount != 3 {
		t.Errorf("first cluster FaceCount = %d, want 3", clusters[0].FaceCount)
	}
	if clusters[1].FaceCount != 2 {
		t.Errorf("second cluster FaceCount = %d, want 2", clusters[1].FaceCount)
	}

	// Cluster 1 should have the label "Alice".
	var aliceCluster *ClusterDetail
	for i := range clusters {
		if clusters[i].ClusterID == 1 {
			aliceCluster = &clusters[i]
		}
	}
	if aliceCluster == nil {
		t.Fatal("cluster 1 not found")
	}
	if aliceCluster.Label != "Alice" {
		t.Errorf("cluster 1 Label = %q, want Alice", aliceCluster.Label)
	}
}

func TestGetNoiseFaces(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	noise, err := a.GetNoiseFaces()
	if err != nil {
		t.Fatalf("GetNoiseFaces: %v", err)
	}
	if len(noise) != 1 {
		t.Fatalf("len(noise) = %d, want 1", len(noise))
	}
	if noise[0].ImagePath != "/noise.jpg" {
		t.Errorf("noise ImagePath = %q, want /noise.jpg", noise[0].ImagePath)
	}
}

func TestRemoveFaceFromCluster(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	// Remove one face from cluster 1.
	if err := a.RemoveFaceFromCluster("/imgA.jpg", 0); err != nil {
		t.Fatalf("RemoveFaceFromCluster: %v", err)
	}

	clusters, err := a.GetClusters()
	if err != nil {
		t.Fatalf("GetClusters: %v", err)
	}

	for _, c := range clusters {
		if c.ClusterID == 1 && c.FaceCount != 2 {
			t.Errorf("cluster 1 FaceCount = %d, want 2 after removal", c.FaceCount)
		}
	}

	// The removed face should now be noise.
	noise, err := a.GetNoiseFaces()
	if err != nil {
		t.Fatalf("GetNoiseFaces: %v", err)
	}
	if len(noise) != 2 {
		t.Errorf("len(noise) = %d, want 2 (original + removed)", len(noise))
	}
}

func TestSetClusterLabel(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	if err := a.SetClusterLabel(2, "Bob"); err != nil {
		t.Fatalf("SetClusterLabel: %v", err)
	}

	clusters, err := a.GetClusters()
	if err != nil {
		t.Fatalf("GetClusters: %v", err)
	}

	for _, c := range clusters {
		if c.ClusterID == 2 && c.Label != "Bob" {
			t.Errorf("cluster 2 Label = %q, want Bob", c.Label)
		}
	}
}

func TestMergeClusters(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	moved, err := a.MergeClusters(1, []int{2})
	if err != nil {
		t.Fatalf("MergeClusters: %v", err)
	}
	if moved != 2 {
		t.Errorf("moved = %d, want 2", moved)
	}

	clusters, err := a.GetClusters()
	if err != nil {
		t.Fatalf("GetClusters: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("len(clusters) = %d, want 1 after merge", len(clusters))
	}
	if clusters[0].FaceCount != 5 {
		t.Errorf("merged cluster FaceCount = %d, want 5", clusters[0].FaceCount)
	}
}

func TestGetSuggestedMerges(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	suggestions, err := a.GetSuggestedMerges()
	if err != nil {
		t.Fatalf("GetSuggestedMerges: %v", err)
	}

	// With our test embeddings, the key check is that it doesn't error.
	// Suggestions may be nil/empty depending on similarity threshold.
	_ = suggestions
}

func TestIsClusteringRunning(t *testing.T) {
	a := newTestApp(t)

	if a.IsClusteringRunning() {
		t.Error("clustering should not be running initially")
	}
}

// ---------------------------------------------------------------------------
// AssignClusterToPerson (integrates graph + face stores)
// ---------------------------------------------------------------------------

func TestAssignClusterToPerson(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	// Create a person.
	p, err := a.CreatePerson("Alice", nil)
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	// Assign cluster 1 to Alice.
	if err := a.AssignClusterToPerson(1, p.ID); err != nil {
		t.Fatalf("AssignClusterToPerson: %v", err)
	}

	// Alice should now have 3 faces assigned.
	persons, err := a.GetPersons()
	if err != nil {
		t.Fatalf("GetPersons: %v", err)
	}
	if len(persons) != 1 {
		t.Fatalf("len(persons) = %d, want 1", len(persons))
	}
	if persons[0].FaceCount != 3 {
		t.Errorf("Alice FaceCount = %d, want 3", persons[0].FaceCount)
	}
}

func TestAssignClusterToPersonNotFound(t *testing.T) {
	a := newTestApp(t)
	seedFaceStore(t, a.cfg.ConfigDir)

	err := a.AssignClusterToPerson(1, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent person")
	}
}

// ---------------------------------------------------------------------------
// AssignFaceToPerson
// ---------------------------------------------------------------------------

func TestAssignFaceToPerson(t *testing.T) {
	a := newTestApp(t)

	p, err := a.CreatePerson("Bob", nil)
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	if err := a.AssignFaceToPerson(p.ID, "/photo.jpg", 0, 0.95); err != nil {
		t.Fatalf("AssignFaceToPerson: %v", err)
	}

	persons, err := a.GetPersons()
	if err != nil {
		t.Fatalf("GetPersons: %v", err)
	}
	if persons[0].FaceCount != 1 {
		t.Errorf("FaceCount = %d, want 1", persons[0].FaceCount)
	}
}

// ---------------------------------------------------------------------------
// padRect (pure function tests)
// ---------------------------------------------------------------------------

func TestPadRect(t *testing.T) {
	tests := []struct {
		name   string
		r      [4]int // minX, minY, maxX, maxY
		bounds [4]int
		frac   float64
		want   [4]int
	}{
		{
			name:   "normal padding",
			r:      [4]int{100, 100, 200, 200},
			bounds: [4]int{0, 0, 500, 500},
			frac:   0.2,
			want:   [4]int{80, 80, 220, 220},
		},
		{
			name:   "clamped to bounds",
			r:      [4]int{5, 5, 50, 50},
			bounds: [4]int{0, 0, 55, 55},
			frac:   0.5,
			want:   [4]int{0, 0, 55, 55},
		},
		{
			name:   "zero padding",
			r:      [4]int{10, 10, 30, 30},
			bounds: [4]int{0, 0, 100, 100},
			frac:   0.0,
			want:   [4]int{10, 10, 30, 30},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := padRect(
				imageRect(tc.r[0], tc.r[1], tc.r[2], tc.r[3]),
				imageRect(tc.bounds[0], tc.bounds[1], tc.bounds[2], tc.bounds[3]),
				tc.frac,
			)
			if got.Min.X != tc.want[0] || got.Min.Y != tc.want[1] ||
				got.Max.X != tc.want[2] || got.Max.Y != tc.want[3] {
				t.Errorf("padRect = (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
					got.Min.X, got.Min.Y, got.Max.X, got.Max.Y,
					tc.want[0], tc.want[1], tc.want[2], tc.want[3])
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectImageMIME (pure function tests)
// ---------------------------------------------------------------------------

func TestDetectImageMIME(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/photo.jpg", "image/jpeg"},
		{"/photo.jpeg", "image/jpeg"},
		{"/photo.png", "image/png"},
		{"/photo.webp", "image/webp"},
		{"/photo.bmp", "image/bmp"},
		{"/photo.tiff", "image/tiff"},
		{"/photo.tif", "image/tiff"},
		{"/noext", "image/jpeg"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := detectImageMIME(tc.path)
			if got != tc.want {
				t.Errorf("detectImageMIME(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
