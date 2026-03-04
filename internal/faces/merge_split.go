//go:build faces

package faces

import "fmt"

// Merge merges source cluster IDs into the target cluster.
// Returns the number of faces moved.
func Merge(store *Store, target int, sources []int) (int, error) {
	faces, err := store.AllFaces()
	if err != nil {
		return 0, fmt.Errorf("load faces: %w", err)
	}

	sourceSet := make(map[int]bool)
	for _, s := range sources {
		sourceSet[s] = true
	}

	moved := 0
	for _, f := range faces {
		if sourceSet[f.ClusterID] {
			if err := store.UpdateCluster(f.ImagePath, f.FaceIdx, target); err != nil {
				return moved, fmt.Errorf("update cluster: %w", err)
			}
			moved++
		}
	}

	// Migrate labels.
	targetLabel := store.GetLabel(target)
	if targetLabel == "" {
		for _, s := range sources {
			if label := store.GetLabel(s); label != "" {
				_ = store.SetLabel(target, label)
				break
			}
		}
	}

	for _, s := range sources {
		_ = store.DeleteLabel(s)
	}

	return moved, nil
}

// Split re-clusters a single cluster using DBSCAN at a tighter threshold.
// Returns a map of sub-cluster IDs → face counts.
func Split(store *Store, clusterID int, simThreshold float32) (map[int]int, error) {
	faces, err := store.AllFaces()
	if err != nil {
		return nil, fmt.Errorf("load faces: %w", err)
	}

	// Collect faces in the target cluster.
	var indices []int
	for i, f := range faces {
		if f.ClusterID == clusterID {
			indices = append(indices, i)
		}
	}

	if len(indices) < 2 {
		return nil, fmt.Errorf("cluster %d has fewer than 2 faces", clusterID)
	}

	embeddings := make([][]float32, len(indices))
	imgPaths := make([]string, len(indices))
	for i, idx := range indices {
		embeddings[i] = faces[idx].Embedding
		imgPaths[i] = faces[idx].ImagePath
	}

	subLabels := DBSCANClustering(embeddings, imgPaths, simThreshold, 2)

	// Find next available cluster ID.
	maxID := 0
	for _, f := range faces {
		if f.ClusterID > maxID {
			maxID = f.ClusterID
		}
	}

	// Map sub-labels to global IDs.
	subClusterMap := make(map[int]int)
	nextID := maxID + 1
	for _, sl := range subLabels {
		if sl <= 0 {
			continue
		}
		if _, exists := subClusterMap[sl]; !exists {
			if sl == 1 {
				subClusterMap[sl] = clusterID
			} else {
				subClusterMap[sl] = nextID
				nextID++
			}
		}
	}

	// Apply new cluster IDs.
	for i, idx := range indices {
		sl := subLabels[i]
		newCluster := -1
		if sl > 0 {
			newCluster = subClusterMap[sl]
		}
		if err := store.UpdateCluster(faces[idx].ImagePath, faces[idx].FaceIdx, newCluster); err != nil {
			return nil, fmt.Errorf("update cluster: %w", err)
		}
	}

	// Count results.
	result := make(map[int]int)
	for _, sl := range subLabels {
		if sl > 0 {
			result[subClusterMap[sl]]++
		} else {
			result[-1]++
		}
	}

	return result, nil
}
