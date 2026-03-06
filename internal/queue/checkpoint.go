package queue

// FaceClusterInfo represents a cluster of detected faces.
type FaceClusterInfo struct {
	ClusterID  string   `json:"cluster_id"`
	FaceCount  int      `json:"face_count"`
	ImagePaths []string `json:"image_paths"`
	IsNew      bool     `json:"is_new"`
}

// ShouldCheckpoint returns true when the number of new clusters with
// sufficient faces meets or exceeds the threshold.
func ShouldCheckpoint(clusters []FaceClusterInfo, minFaces, threshold int) bool {
	if threshold <= 0 {
		return false
	}
	qualifying := 0
	for _, c := range clusters {
		if c.IsNew && c.FaceCount >= minFaces {
			qualifying++
		}
	}
	return qualifying >= threshold
}

// BuildCheckpointPayload constructs the event data emitted at a checkpoint.
func BuildCheckpointPayload(clusters []FaceClusterInfo, processed, total int) map[string]any {
	var newClusters []FaceClusterInfo
	for _, c := range clusters {
		if c.IsNew {
			newClusters = append(newClusters, c)
		}
	}
	return map[string]any{
		"new_clusters":     len(newClusters),
		"clusters":         newClusters,
		"images_processed": processed,
		"total_images":     total,
	}
}
