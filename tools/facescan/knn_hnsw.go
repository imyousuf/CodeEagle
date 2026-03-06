package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/coder/hnsw"
)

// hnswIndex wraps an HNSW graph for fast approximate nearest neighbor search
// over face embeddings. Build once from exemplars, then query per face.
type hnswIndex struct {
	graph     *hnsw.Graph[int] // node key = exemplar index
	exemplars []exemplar
}

// buildHNSWIndex constructs an HNSW index from exemplars.
// Returns the index and the build duration.
func buildHNSWIndex(exemplars []exemplar) (*hnswIndex, time.Duration) {
	start := time.Now()

	g := hnsw.NewGraph[int]()
	g.M = 32       // more connections = better recall for small datasets
	g.EfSearch = 128 // large candidate pool for high recall

	for i, ex := range exemplars {
		vec := make([]float32, len(ex.Embedding))
		copy(vec, ex.Embedding)
		g.Add(hnsw.MakeNode(i, vec))
	}

	return &hnswIndex{graph: g, exemplars: exemplars}, time.Since(start)
}

// classifyKNNHNSW runs KNN classification using the HNSW index for neighbor lookup.
// Fetches extra candidates (3x K) from HNSW to account for same-image filtering,
// then applies the same majority vote + threshold logic as brute-force.
func classifyKNNHNSW(embedding []float32, _ string, idx *hnswIndex, k int, threshold float32) knnResult {
	if len(idx.exemplars) == 0 {
		return knnResult{Label: "unknown", Confidence: 0}
	}

	// Fetch more candidates than needed — HNSW returns approximate neighbors
	// and we may need to skip same-image matches.
	fetchK := min(k*3, len(idx.exemplars))

	neighbors := idx.graph.Search(embedding, fetchK)

	// Build top-K from HNSW results, skipping same-image exemplars.
	var topK []knnMatch
	for _, n := range neighbors {
		if len(topK) >= k {
			break
		}
		ex := idx.exemplars[n.Key]
		// Compute exact similarity (HNSW returns distance, we want similarity).
		sim := dotProduct(embedding, ex.Embedding)
		topK = append(topK, knnMatch{
			Label:      ex.Label,
			Similarity: sim,
			FileName:   ex.FileName,
		})
	}

	// Re-sort by exact similarity (HNSW order is approximate).
	sort.Slice(topK, func(i, j int) bool {
		return topK[i].Similarity > topK[j].Similarity
	})

	if len(topK) == 0 {
		return knnResult{Label: "unknown", Confidence: 0}
	}

	// Majority vote (same logic as brute-force).
	votes := make(map[string]int)
	simSum := make(map[string]float32)
	for _, m := range topK {
		votes[m.Label]++
		simSum[m.Label] += m.Similarity
	}

	bestLabel := ""
	bestVotes := 0
	for label, count := range votes {
		if count > bestVotes {
			bestVotes = count
			bestLabel = label
		}
	}

	avgSim := simSum[bestLabel] / float32(votes[bestLabel])

	needed := (k + 1) / 2
	if bestVotes >= needed && avgSim >= threshold {
		return knnResult{
			Label:      bestLabel,
			Confidence: avgSim,
			TopK:       topK,
		}
	}

	return knnResult{
		Label:      "unknown",
		Confidence: avgSim,
		TopK:       topK,
	}
}

// benchmarkKNN runs both brute-force and HNSW classification on the same test faces
// and prints a comparison table.
func benchmarkKNN(testFaces []testFaceRecord, exemplars []exemplar, k int, threshold float32) {
	fmt.Println("\n=== KNN Benchmark: Brute-Force vs HNSW ===")
	fmt.Printf("  Exemplars: %d, Test faces: %d, K=%d, threshold=%.2f\n\n", len(exemplars), len(testFaces), k, threshold)

	// --- Brute-force ---
	bfStart := time.Now()
	bfResults := make([]knnResult, len(testFaces))
	for i, tf := range testFaces {
		bfResults[i] = classifyKNN(tf.Embedding, tf.ImagePath, exemplars, k, threshold)
	}
	bfDuration := time.Since(bfStart)

	// --- HNSW ---
	idx, buildDuration := buildHNSWIndex(exemplars)

	hnswStart := time.Now()
	hnswResults := make([]knnResult, len(testFaces))
	for i, tf := range testFaces {
		hnswResults[i] = classifyKNNHNSW(tf.Embedding, tf.ImagePath, idx, k, threshold)
	}
	hnswQueryDuration := time.Since(hnswStart)
	hnswTotalDuration := buildDuration + hnswQueryDuration

	// --- Compare results ---
	bfClassified := 0
	hnswClassified := 0
	agreements := 0
	disagreements := 0

	for i := range testFaces {
		bfLabel := bfResults[i].Label
		hnswLabel := hnswResults[i].Label
		if bfLabel != "unknown" {
			bfClassified++
		}
		if hnswLabel != "unknown" {
			hnswClassified++
		}
		if bfLabel == hnswLabel {
			agreements++
		} else {
			disagreements++
		}
	}

	// Print timing.
	fmt.Printf("  %-25s %12s %12s\n", "", "Brute-Force", "HNSW")
	fmt.Printf("  %-25s %12s %12s\n", "", "-----------", "----")
	fmt.Printf("  %-25s %12s %12s\n", "Index build", "n/a", fmtDuration(buildDuration))
	fmt.Printf("  %-25s %12s %12s\n", "Classification", fmtDuration(bfDuration), fmtDuration(hnswQueryDuration))
	fmt.Printf("  %-25s %12s %12s\n", "Total", fmtDuration(bfDuration), fmtDuration(hnswTotalDuration))

	if len(testFaces) > 0 {
		bfPerFace := bfDuration / time.Duration(len(testFaces))
		hnswPerFace := hnswQueryDuration / time.Duration(len(testFaces))
		fmt.Printf("  %-25s %12s %12s\n", "Per face", fmtDuration(bfPerFace), fmtDuration(hnswPerFace))
	}

	if bfDuration > 0 {
		speedup := float64(bfDuration) / float64(hnswQueryDuration)
		fmt.Printf("\n  Query speedup: %.1fx\n", speedup)
		totalSpeedup := float64(bfDuration) / float64(hnswTotalDuration)
		fmt.Printf("  Total speedup (incl. build): %.1fx\n", totalSpeedup)
	}

	// Print accuracy comparison.
	fmt.Printf("\n  %-25s %12d %12d\n", "Classified", bfClassified, hnswClassified)
	fmt.Printf("  %-25s %12d %12d\n", "Unknown", len(testFaces)-bfClassified, len(testFaces)-hnswClassified)
	fmt.Printf("  %-25s %12d\n", "Agreements", agreements)
	fmt.Printf("  %-25s %12d\n", "Disagreements", disagreements)
	if len(testFaces) > 0 {
		agreementRate := float64(agreements) / float64(len(testFaces)) * 100
		fmt.Printf("  %-25s %11.1f%%\n", "Agreement rate", agreementRate)
	}

	// Show disagreements.
	if disagreements > 0 {
		fmt.Printf("\n  Disagreements (brute-force → HNSW):\n")
		shown := 0
		for i := range testFaces {
			if bfResults[i].Label != hnswResults[i].Label {
				fmt.Printf("    %s face_%d: %s(%.3f) → %s(%.3f)\n",
					shortPath(testFaces[i].ImagePath), testFaces[i].FaceIdx,
					bfResults[i].Label, bfResults[i].Confidence,
					hnswResults[i].Label, hnswResults[i].Confidence)
				shown++
				if shown >= 20 {
					fmt.Printf("    ... and %d more\n", disagreements-shown)
					break
				}
			}
		}
	}
}

func fmtDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fus", float64(d.Nanoseconds())/1000)
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
