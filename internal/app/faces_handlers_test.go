//go:build app && faces

package app

import (
	"testing"
)

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
		TotalPersons: 3,
		TotalFaces:   15,
		TotalImages:  100,
		ScannedCount: 80,
		OldestDate:   "2023-01-01",
		NewestDate:   "2024-12-31",
	}
	if s.TotalPersons != 3 {
		t.Errorf("TotalPersons = %d, want 3", s.TotalPersons)
	}
	if s.ScannedCount != 80 {
		t.Errorf("ScannedCount = %d, want 80", s.ScannedCount)
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
