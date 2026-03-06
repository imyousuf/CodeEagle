//go:build faces

package faces

import (
	"math"
	"sort"
	"time"
)

// ExemplarData holds the data needed for KNN classification.
// Defined locally to avoid circular imports with the embedded package.
type ExemplarData struct {
	PersonID  string
	Embedding []float32
	ImagePath string
	DateTaken time.Time
}

// ClassifyResult holds the result of a KNN classification.
type ClassifyResult struct {
	PersonID   string
	PersonName string
	Confidence float64
	TopK       []KNNMatch
}

// KNNMatch represents one neighbor in the KNN result.
type KNNMatch struct {
	PersonID   string
	PersonName string
	Similarity float64
	ImagePath  string
	DateTaken  time.Time
}

// KNNClassifier classifies face embeddings using K-nearest neighbors.
type KNNClassifier struct {
	K             int
	Threshold     float64
	TemporalDecay float64 // per-year decay factor (0 = disabled)
}

// NewKNNClassifier creates a classifier with the given parameters.
func NewKNNClassifier(k int, threshold, temporalDecay float64) *KNNClassifier {
	return &KNNClassifier{
		K:             k,
		Threshold:     threshold,
		TemporalDecay: temporalDecay,
	}
}

// Classify determines the most likely person for a face embedding.
// Returns nil if no confident match is found.
func (c *KNNClassifier) Classify(embedding []float32, imagePath string, exemplars []ExemplarData) *ClassifyResult {
	if len(exemplars) == 0 {
		return nil
	}

	type scored struct {
		exemplar   ExemplarData
		similarity float64
	}

	var candidates []scored
	for _, ex := range exemplars {
		// Exclude exemplars from the same image to avoid self-match.
		if ex.ImagePath == imagePath {
			continue
		}
		sim := cosineSimilarity(embedding, ex.Embedding)
		if c.TemporalDecay > 0 && !ex.DateTaken.IsZero() {
			years := time.Since(ex.DateTaken).Hours() / (365.25 * 24)
			sim *= 1.0 / (1.0 + years*c.TemporalDecay)
		}
		candidates = append(candidates, scored{exemplar: ex, similarity: sim})
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort by similarity descending.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].similarity > candidates[j].similarity
	})

	// Take top K.
	k := c.K
	if k > len(candidates) {
		k = len(candidates)
	}
	topK := candidates[:k]

	// Majority vote.
	votes := make(map[string]int)
	simSum := make(map[string]float64)
	simCount := make(map[string]int)
	for _, s := range topK {
		votes[s.exemplar.PersonID]++
		simSum[s.exemplar.PersonID] += s.similarity
		simCount[s.exemplar.PersonID]++
	}

	// Find the winner: most votes, then highest avg similarity.
	var winnerID string
	var maxVotes int
	var bestAvgSim float64
	for pid, v := range votes {
		avg := simSum[pid] / float64(simCount[pid])
		if v > maxVotes || (v == maxVotes && avg > bestAvgSim) {
			winnerID = pid
			maxVotes = v
			bestAvgSim = avg
		}
	}

	// Require majority: >= ceil(K/2) votes.
	majority := (k + 1) / 2
	if maxVotes < majority {
		return nil
	}

	// Require average similarity above threshold.
	if bestAvgSim < c.Threshold {
		return nil
	}

	// Build result.
	var matches []KNNMatch
	for _, s := range topK {
		matches = append(matches, KNNMatch{
			PersonID:   s.exemplar.PersonID,
			Similarity: s.similarity,
			ImagePath:  s.exemplar.ImagePath,
			DateTaken:  s.exemplar.DateTaken,
		})
	}

	return &ClassifyResult{
		PersonID:   winnerID,
		Confidence: bestAvgSim,
		TopK:       matches,
	}
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
