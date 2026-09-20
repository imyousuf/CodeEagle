package queue

import (
	"testing"
)

func TestShouldCheckpoint_MeetsThreshold(t *testing.T) {
	clusters := []FaceClusterInfo{
		{ClusterID: "c1", FaceCount: 5, IsNew: true},
		{ClusterID: "c2", FaceCount: 3, IsNew: true},
		{ClusterID: "c3", FaceCount: 4, IsNew: true},
	}
	if !ShouldCheckpoint(clusters, 3, 3) {
		t.Error("should checkpoint when 3 new clusters with >= 3 faces")
	}
}

func TestShouldCheckpoint_BelowThreshold(t *testing.T) {
	clusters := []FaceClusterInfo{
		{ClusterID: "c1", FaceCount: 5, IsNew: true},
		{ClusterID: "c2", FaceCount: 1, IsNew: true}, // too few faces
	}
	if ShouldCheckpoint(clusters, 3, 2) {
		t.Error("should not checkpoint when only 1 qualifying cluster (need 2)")
	}
}

func TestShouldCheckpoint_ExcludesOldClusters(t *testing.T) {
	clusters := []FaceClusterInfo{
		{ClusterID: "c1", FaceCount: 10, IsNew: false},
		{ClusterID: "c2", FaceCount: 10, IsNew: false},
		{ClusterID: "c3", FaceCount: 5, IsNew: true},
	}
	if ShouldCheckpoint(clusters, 3, 2) {
		t.Error("should not checkpoint with only 1 new cluster (need 2)")
	}
}

func TestShouldCheckpoint_ZeroThreshold(t *testing.T) {
	clusters := []FaceClusterInfo{
		{ClusterID: "c1", FaceCount: 5, IsNew: true},
	}
	if ShouldCheckpoint(clusters, 3, 0) {
		t.Error("should not checkpoint with zero threshold")
	}
}

func TestBuildCheckpointPayload(t *testing.T) {
	clusters := []FaceClusterInfo{
		{ClusterID: "c1", FaceCount: 5, IsNew: true, ImagePaths: []string{"a.jpg"}},
		{ClusterID: "c2", FaceCount: 3, IsNew: false},
		{ClusterID: "c3", FaceCount: 4, IsNew: true, ImagePaths: []string{"b.jpg"}},
	}

	payload := BuildCheckpointPayload(clusters, 50, 200)

	if payload["new_clusters"] != 2 {
		t.Errorf("new_clusters = %v, want 2", payload["new_clusters"])
	}
	if payload["images_processed"] != 50 {
		t.Errorf("images_processed = %v, want 50", payload["images_processed"])
	}
	if payload["total_images"] != 200 {
		t.Errorf("total_images = %v, want 200", payload["total_images"])
	}

	newClusters, ok := payload["clusters"].([]FaceClusterInfo)
	if !ok {
		t.Fatal("clusters should be []FaceClusterInfo")
	}
	if len(newClusters) != 2 {
		t.Errorf("cluster count = %d, want 2", len(newClusters))
	}
}
