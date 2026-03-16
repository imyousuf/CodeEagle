//go:build app && faces

package app

import (
	"image"
	"testing"
)

func imageRect(minX, minY, maxX, maxY int) image.Rectangle {
	return image.Rect(minX, minY, maxX, maxY)
}

func TestPersonInfoFields(t *testing.T) {
	p := PersonInfo{
		ID:            "abc",
		Name:          "Alice",
		Relationships: []string{"child"},
		FaceCount:     5,
		CreatedAt:     "2024-01-01T00:00:00Z",
	}
	if p.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", p.Name)
	}
	if p.FaceCount != 5 {
		t.Errorf("FaceCount = %d, want 5", p.FaceCount)
	}
}

func TestClusterInfoFields(t *testing.T) {
	c := ClusterInfo{
		ClusterID:  1,
		FaceCount:  3,
		ImagePaths: []string{"/a.jpg", "/b.jpg"},
		Label:      "alice",
	}
	if c.ClusterID != 1 {
		t.Errorf("ClusterID = %d, want 1", c.ClusterID)
	}
	if len(c.ImagePaths) != 2 {
		t.Errorf("ImagePaths len = %d, want 2", len(c.ImagePaths))
	}
}

func TestFaceStatsFields(t *testing.T) {
	s := FaceStats{
		TotalPersons:    3,
		TotalFaces:      15,
		DetectedFaces:   200,
		ImagesWithFaces: 80,
		TotalImages:     100,
		ScannedCount:    80,
		OldestDate:      "2023-01-01",
		NewestDate:      "2024-12-31",
	}
	if s.TotalPersons != 3 {
		t.Errorf("TotalPersons = %d, want 3", s.TotalPersons)
	}
	if s.ScannedCount != 80 {
		t.Errorf("ScannedCount = %d, want 80", s.ScannedCount)
	}
	if s.DetectedFaces != 200 {
		t.Errorf("DetectedFaces = %d, want 200", s.DetectedFaces)
	}
	if s.ImagesWithFaces != 80 {
		t.Errorf("ImagesWithFaces = %d, want 80", s.ImagesWithFaces)
	}
}

func TestFaceReviewItemFields(t *testing.T) {
	r := FaceReviewItem{
		ImagePath:  "/photo.jpg",
		FaceIndex:  0,
		ClusterID:  2,
		Confidence: 0.85,
	}
	if r.Confidence != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", r.Confidence)
	}
}

func TestClusterDetailFields(t *testing.T) {
	cd := ClusterDetail{
		ClusterID: 5,
		Label:     "alice",
		FaceCount: 10,
		Faces: []ClusterFace{
			{ImagePath: "/a.jpg", FaceIndex: 0},
			{ImagePath: "/b.jpg", FaceIndex: 1},
		},
		PersonID: "p-123",
	}
	if cd.ClusterID != 5 {
		t.Errorf("ClusterID = %d, want 5", cd.ClusterID)
	}
	if cd.Label != "alice" {
		t.Errorf("Label = %q, want alice", cd.Label)
	}
	if len(cd.Faces) != 2 {
		t.Errorf("Faces len = %d, want 2", len(cd.Faces))
	}
	if cd.Faces[0].ImagePath != "/a.jpg" {
		t.Errorf("Face[0].ImagePath = %q, want /a.jpg", cd.Faces[0].ImagePath)
	}
}

func TestClusterFaceFields(t *testing.T) {
	cf := ClusterFace{
		ImagePath: "/photo.jpg",
		FaceIndex: 2,
	}
	if cf.ImagePath != "/photo.jpg" {
		t.Errorf("ImagePath = %q, want /photo.jpg", cf.ImagePath)
	}
	if cf.FaceIndex != 2 {
		t.Errorf("FaceIndex = %d, want 2", cf.FaceIndex)
	}
}

func TestMergeSuggestionFields(t *testing.T) {
	ms := MergeSuggestion{
		ClusterA:   1,
		ClusterB:   2,
		LabelA:     "alice",
		LabelB:     "bob",
		Similarity: 0.78,
		FaceCountA: 5,
		FaceCountB: 3,
	}
	if ms.ClusterA != 1 {
		t.Errorf("ClusterA = %d, want 1", ms.ClusterA)
	}
	if ms.Similarity != 0.78 {
		t.Errorf("Similarity = %f, want 0.78", ms.Similarity)
	}
}

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
