//go:build faces

package faces

import (
	"math"
	"testing"
	"time"
)

func makeExemplar(personID string, embedding []float32, imagePath string) ExemplarData {
	return ExemplarData{
		PersonID:  personID,
		Embedding: embedding,
		ImagePath: imagePath,
		DateTaken: time.Now(),
	}
}

func TestClassifyMajority(t *testing.T) {
	c := NewKNNClassifier(5, 0.3, 0)
	query := []float32{1, 0, 0}
	exemplars := []ExemplarData{
		makeExemplar("alice", []float32{1, 0, 0}, "/a1.jpg"),
		makeExemplar("alice", []float32{0.99, 0.1, 0}, "/a2.jpg"),
		makeExemplar("alice", []float32{0.98, 0.05, 0.05}, "/a3.jpg"),
		makeExemplar("bob", []float32{0, 1, 0}, "/b1.jpg"),
		makeExemplar("bob", []float32{0, 0.99, 0.1}, "/b2.jpg"),
	}

	result := c.Classify(query, "/test.jpg", exemplars)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.PersonID != "alice" {
		t.Errorf("PersonID = %q, want alice", result.PersonID)
	}
	if result.Confidence < 0.9 {
		t.Errorf("Confidence = %f, expected >= 0.9", result.Confidence)
	}
}

func TestClassifyTiedVote(t *testing.T) {
	c := NewKNNClassifier(4, 0.3, 0)
	query := []float32{1, 1, 0}
	exemplars := []ExemplarData{
		makeExemplar("alice", []float32{1, 0, 0}, "/a1.jpg"),
		makeExemplar("alice", []float32{0.9, 0.1, 0}, "/a2.jpg"),
		makeExemplar("bob", []float32{0, 1, 0}, "/b1.jpg"),
		makeExemplar("bob", []float32{0.1, 0.9, 0}, "/b2.jpg"),
	}

	result := c.Classify(query, "/test.jpg", exemplars)
	// With 2 vs 2, neither has majority (ceil(4/2)=2 needed but tied).
	// Both should have 2 votes which equals majority — result depends on avg similarity.
	// This test just verifies no panic and the result is sensible.
	if result != nil && result.PersonID != "alice" && result.PersonID != "bob" {
		t.Errorf("unexpected person: %q", result.PersonID)
	}
}

func TestClassifyBelowThreshold(t *testing.T) {
	c := NewKNNClassifier(3, 0.95, 0)
	query := []float32{1, 0, 0}
	exemplars := []ExemplarData{
		makeExemplar("alice", []float32{0.7, 0.7, 0}, "/a1.jpg"),
		makeExemplar("alice", []float32{0.6, 0.8, 0}, "/a2.jpg"),
		makeExemplar("alice", []float32{0.5, 0.5, 0.7}, "/a3.jpg"),
	}

	result := c.Classify(query, "/test.jpg", exemplars)
	if result != nil {
		t.Errorf("expected nil result for low similarity, got confidence %f", result.Confidence)
	}
}

func TestClassifySamePhotoExclusion(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0)
	query := []float32{1, 0, 0}
	// Only exemplar from the same image path — should be excluded.
	exemplars := []ExemplarData{
		makeExemplar("alice", []float32{1, 0, 0}, "/test.jpg"),
	}

	result := c.Classify(query, "/test.jpg", exemplars)
	if result != nil {
		t.Error("expected nil result when all exemplars are from same image")
	}
}

func TestClassifyEmptyExemplars(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0)
	result := c.Classify([]float32{1, 0, 0}, "/test.jpg", nil)
	if result != nil {
		t.Error("expected nil result for empty exemplars")
	}
}

func TestClassifyInsufficientExemplars(t *testing.T) {
	c := NewKNNClassifier(7, 0.3, 0)
	query := []float32{1, 0, 0}
	// Only 1 exemplar but K=7 — should adapt K to available count.
	exemplars := []ExemplarData{
		makeExemplar("alice", []float32{1, 0, 0}, "/a1.jpg"),
	}

	result := c.Classify(query, "/test.jpg", exemplars)
	if result == nil {
		t.Fatal("expected non-nil result with single matching exemplar")
	}
	if result.PersonID != "alice" {
		t.Errorf("PersonID = %q, want alice", result.PersonID)
	}
}

func TestClassifyWithTemporalDecay(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0.1)
	query := []float32{1, 0, 0}

	recent := ExemplarData{
		PersonID:  "alice",
		Embedding: []float32{0.95, 0.1, 0},
		ImagePath: "/recent.jpg",
		DateTaken: time.Now(),
	}
	old := ExemplarData{
		PersonID:  "alice",
		Embedding: []float32{1, 0, 0},
		ImagePath: "/old.jpg",
		DateTaken: time.Now().Add(-10 * 365 * 24 * time.Hour), // 10 years ago
	}

	result := c.Classify(query, "/test.jpg", []ExemplarData{recent, old})
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// The recent exemplar should have higher weighted similarity despite lower raw similarity.
	if len(result.TopK) < 2 {
		t.Fatal("expected at least 2 TopK matches")
	}
	// Old exemplar gets decayed: sim * 1/(1 + 10*0.1) = sim * 0.5.
	// For old: raw=1.0, weighted=0.5. For recent: raw~0.95, weighted~0.95.
	// So recent should rank first.
	if result.TopK[0].ImagePath != "/recent.jpg" {
		t.Errorf("expected recent exemplar to rank first, got %s", result.TopK[0].ImagePath)
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors.
	sim := cosineSimilarity([]float32{1, 0, 0}, []float32{1, 0, 0})
	if math.Abs(sim-1.0) > 1e-6 {
		t.Errorf("identical vectors: sim = %f, want 1.0", sim)
	}

	// Orthogonal vectors.
	sim = cosineSimilarity([]float32{1, 0, 0}, []float32{0, 1, 0})
	if math.Abs(sim) > 1e-6 {
		t.Errorf("orthogonal vectors: sim = %f, want 0.0", sim)
	}

	// Different length vectors.
	sim = cosineSimilarity([]float32{1, 0}, []float32{1, 0, 0})
	if sim != 0 {
		t.Errorf("different length: sim = %f, want 0.0", sim)
	}

	// Empty vectors.
	sim = cosineSimilarity(nil, nil)
	if sim != 0 {
		t.Errorf("empty: sim = %f, want 0.0", sim)
	}
}
