package main

import "sort"

// agglomerativeClustering performs hierarchical agglomerative clustering
// with average linkage. Unlike DBSCAN, average linkage considers ALL cross-cluster
// face pairs when deciding whether to merge, making it resistant to chaining
// through a single outlier pair (e.g., two similar-looking family members).
//
// Same-photo constraint: clusters are never merged if they share an image path
// (a person can only appear once per photo).
//
// Faces that end up in singleton clusters below minClusterSize are labeled noise (-1).
func agglomerativeClustering(embeddings [][]float32, imagePaths []string, simThreshold float32, minClusterSize int) []int {
	n := len(embeddings)

	// Precompute pairwise similarities (symmetric, O(n^2) space — fine for <1000 faces).
	simMatrix := make([][]float32, n)
	for i := range simMatrix {
		simMatrix[i] = make([]float32, n)
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			s := cosineSimilarity(embeddings[i], embeddings[j])
			simMatrix[i][j] = s
			simMatrix[j][i] = s
		}
	}

	// Initialize: each face is its own cluster.
	clusterOf := make([]int, n) // face index → cluster ID
	for i := range clusterOf {
		clusterOf[i] = i + 1 // cluster IDs start at 1
	}

	// Build cluster membership.
	clusterMembers := make(map[int][]int)
	for i := range clusterOf {
		clusterMembers[clusterOf[i]] = []int{i}
	}

	for {
		// Find the best merge candidate: highest average similarity between two clusters,
		// subject to same-photo constraint.
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

				// Same-photo constraint: reject merge if any image appears in both clusters.
				if hasImageOverlap(clusterMembers[a], clusterMembers[b], imagePaths) {
					continue
				}

				// Average linkage: compute mean similarity between all cross-cluster pairs.
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
			break // no more merges above threshold
		}

		// Merge bestB into bestA.
		for _, fi := range clusterMembers[bestB] {
			clusterOf[fi] = bestA
		}
		clusterMembers[bestA] = append(clusterMembers[bestA], clusterMembers[bestB]...)
		delete(clusterMembers, bestB)
	}

	// Renumber clusters contiguously; singletons (< minClusterSize) become noise (-1).
	remap := make(map[int]int)
	nextID := 1
	// Sort by cluster ID for deterministic output.
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

// absorbNoise assigns noise faces (label == -1) to their best-matching cluster.
// A noise face is absorbed if its best match to any cluster member exceeds
// absorbThreshold AND the face's image is not already in that cluster.
// Uses single-pass (no iteration) to prevent chaining through absorbed faces.
func absorbNoise(embeddings [][]float32, imagePaths []string, labels []int, absorbThreshold float32) []int {
	result := make([]int, len(labels))
	copy(result, labels)

	// Build cluster membership and per-cluster image sets.
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

	// Single-pass absorption — each noise face is independently assigned.
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
				s := cosineSimilarity(embeddings[i], embeddings[mi])
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

// DBSCANClustering runs DBSCAN on face embeddings using cosine similarity.
// Used by the split command for re-clustering a single cluster at tighter threshold.
// imagePaths enforces same-photo constraint.
func DBSCANClustering(embeddings [][]float32, imagePaths []string, simThreshold float32, minPts int) []int {
	n := len(embeddings)
	labels := make([]int, n)
	clusterID := 0
	clusterImages := make(map[int]map[string]bool)

	for i := 0; i < n; i++ {
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
		if cosineSimilarity(embeddings[idx], embeddings[i]) >= simThreshold {
			neighbors = append(neighbors, i)
		}
	}
	return neighbors
}

// hasImageOverlap checks if any image path appears in both clusters.
func hasImageOverlap(membersA, membersB []int, imagePaths []string) bool {
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

// cosineSimilarity computes the cosine similarity between two vectors.
// For L2-normalized vectors (SFace output), this is simply the dot product.
func cosineSimilarity(a, b []float32) float32 {
	var dot float32
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}
