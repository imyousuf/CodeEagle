//go:build faces

package faces

import "sort"

// AgglomerativeClustering performs hierarchical agglomerative clustering
// with average linkage and same-photo constraint.
// Faces in singleton clusters below minClusterSize are labeled noise (-1).
func AgglomerativeClustering(embeddings [][]float32, imagePaths []string, simThreshold float32, minClusterSize int) []int {
	n := len(embeddings)

	// Precompute pairwise similarities.
	simMatrix := make([][]float32, n)
	for i := range simMatrix {
		simMatrix[i] = make([]float32, n)
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			s := CosineSimilarity(embeddings[i], embeddings[j])
			simMatrix[i][j] = s
			simMatrix[j][i] = s
		}
	}

	clusterOf := make([]int, n)
	for i := range clusterOf {
		clusterOf[i] = i + 1
	}

	clusterMembers := make(map[int][]int)
	for i := range clusterOf {
		clusterMembers[clusterOf[i]] = []int{i}
	}

	for {
		bestAvgSim := float32(0)
		bestA, bestB := 0, 0

		clIDs := make([]int, 0, len(clusterMembers))
		for id := range clusterMembers {
			clIDs = append(clIDs, id)
		}
		sort.Ints(clIDs)

		for i := 0; i < len(clIDs); i++ {
			for j := i + 1; j < len(clIDs); j++ {
				a, b := clIDs[i], clIDs[j]

				if HasImageOverlap(clusterMembers[a], clusterMembers[b], imagePaths) {
					continue
				}

				var sumSim float32
				nPairs := 0
				for _, ai := range clusterMembers[a] {
					for _, bi := range clusterMembers[b] {
						sumSim += simMatrix[ai][bi]
						nPairs++
					}
				}
				avgSim := sumSim / float32(nPairs)

				if avgSim > bestAvgSim {
					bestAvgSim = avgSim
					bestA = a
					bestB = b
				}
			}
		}

		if bestAvgSim < simThreshold || bestA == 0 {
			break
		}

		for _, fi := range clusterMembers[bestB] {
			clusterOf[fi] = bestA
		}
		clusterMembers[bestA] = append(clusterMembers[bestA], clusterMembers[bestB]...)
		delete(clusterMembers, bestB)
	}

	remap := make(map[int]int)
	nextID := 1
	finalIDs := make([]int, 0, len(clusterMembers))
	for id := range clusterMembers {
		finalIDs = append(finalIDs, id)
	}
	sort.Ints(finalIDs)

	for _, id := range finalIDs {
		if len(clusterMembers[id]) < minClusterSize {
			remap[id] = -1
		} else {
			remap[id] = nextID
			nextID++
		}
	}

	labels := make([]int, n)
	for i, cl := range clusterOf {
		labels[i] = remap[cl]
	}

	return labels
}

// AbsorbNoise assigns noise faces (label == -1) to their best-matching cluster.
// Single-pass to prevent chaining through absorbed faces.
func AbsorbNoise(embeddings [][]float32, imagePaths []string, labels []int, absorbThreshold float32) []int {
	result := make([]int, len(labels))
	copy(result, labels)

	clusterMembers := make(map[int][]int)
	clusterImages := make(map[int]map[string]bool)
	for i, l := range result {
		if l > 0 {
			clusterMembers[l] = append(clusterMembers[l], i)
			if clusterImages[l] == nil {
				clusterImages[l] = make(map[string]bool)
			}
			clusterImages[l][imagePaths[i]] = true
		}
	}

	if len(clusterMembers) == 0 {
		return result
	}

	type absorption struct {
		faceIdx int
		cluster int
	}
	var absorptions []absorption

	for i, l := range result {
		if l != -1 {
			continue
		}

		bestCluster := 0
		bestSim := float32(0)

		for clID, members := range clusterMembers {
			if clusterImages[clID][imagePaths[i]] {
				continue
			}
			for _, mi := range members {
				s := CosineSimilarity(embeddings[i], embeddings[mi])
				if s > bestSim {
					bestSim = s
					bestCluster = clID
				}
			}
		}

		if bestCluster > 0 && bestSim >= absorbThreshold {
			absorptions = append(absorptions, absorption{faceIdx: i, cluster: bestCluster})
		}
	}

	for _, a := range absorptions {
		result[a.faceIdx] = a.cluster
	}

	return result
}

// DBSCANClustering runs DBSCAN for re-clustering (used by split command).
func DBSCANClustering(embeddings [][]float32, imagePaths []string, simThreshold float32, minPts int) []int {
	n := len(embeddings)
	labels := make([]int, n)
	clusterID := 0
	clusterImages := make(map[int]map[string]bool)

	for i := range n {
		if labels[i] != 0 {
			continue
		}

		neighbors := regionQuery(embeddings, imagePaths, i, simThreshold)

		if len(neighbors) < minPts {
			labels[i] = -1
			continue
		}

		clusterID++
		labels[i] = clusterID
		clusterImages[clusterID] = map[string]bool{imagePaths[i]: true}

		seeds := make([]int, len(neighbors))
		copy(seeds, neighbors)

		for j := 0; j < len(seeds); j++ {
			q := seeds[j]
			if clusterImages[clusterID][imagePaths[q]] {
				continue
			}
			if labels[q] == -1 {
				labels[q] = clusterID
				clusterImages[clusterID][imagePaths[q]] = true
			}
			if labels[q] != 0 {
				continue
			}
			labels[q] = clusterID
			clusterImages[clusterID][imagePaths[q]] = true

			qNeighbors := regionQuery(embeddings, imagePaths, q, simThreshold)
			if len(qNeighbors) >= minPts {
				seeds = append(seeds, qNeighbors...)
			}
		}
	}

	return labels
}

func regionQuery(embeddings [][]float32, imagePaths []string, idx int, simThreshold float32) []int {
	var neighbors []int
	for i := range embeddings {
		if i == idx {
			continue
		}
		if imagePaths[i] == imagePaths[idx] {
			continue
		}
		if CosineSimilarity(embeddings[idx], embeddings[i]) >= simThreshold {
			neighbors = append(neighbors, i)
		}
	}
	return neighbors
}

// HasImageOverlap checks if any image path appears in both clusters.
func HasImageOverlap(membersA, membersB []int, imagePaths []string) bool {
	aImages := make(map[string]bool)
	for _, idx := range membersA {
		aImages[imagePaths[idx]] = true
	}
	for _, idx := range membersB {
		if aImages[imagePaths[idx]] {
			return true
		}
	}
	return false
}

// CosineSimilarity computes the dot product of two L2-normalized vectors.
func CosineSimilarity(a, b []float32) float32 {
	var dot float32
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}
