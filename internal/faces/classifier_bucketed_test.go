//go:build faces

package faces

import (
	"math"
	"testing"
	"time"
)

func makeExemplarDated(personID string, embedding []float32, imagePath string, date time.Time) ExemplarData {
	return ExemplarData{
		PersonID:  personID,
		Embedding: embedding,
		ImagePath: imagePath,
		DateTaken: date,
	}
}

func TestBuildBucketedIndex(t *testing.T) {
	now := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)
	old := time.Date(2020, 3, 10, 0, 0, 0, 0, time.UTC)

	exemplars := []ExemplarData{
		makeExemplarDated("alice", []float32{1, 0, 0}, "/a1.jpg", now),
		makeExemplarDated("alice", []float32{0.9, 0.1, 0}, "/a2.jpg", old),
		makeExemplarDated("bob", []float32{0, 1, 0}, "/b1.jpg", now),
		makeExemplarDated("bob", []float32{0, 0.9, 0.1}, "/b2.jpg", time.Time{}), // no date
	}

	idx := BuildBucketedIndex(exemplars)

	// Should have 3 dated buckets: alice:2023, alice:2020, bob:2023.
	if len(idx.buckets) != 3 {
		t.Errorf("buckets = %d, want 3", len(idx.buckets))
	}

	// 1 unknown exemplar (bob with no date).
	if len(idx.unknownBucket.Exemplars) != 1 {
		t.Errorf("unknown exemplars = %d, want 1", len(idx.unknownBucket.Exemplars))
	}
	if idx.unknownBucket.Exemplars[0].PersonID != "bob" {
		t.Errorf("unknown exemplar PersonID = %q, want bob", idx.unknownBucket.Exemplars[0].PersonID)
	}

	// Alice should have years [2020, 2023].
	aliceYears := idx.personYears["alice"]
	if len(aliceYears) != 2 || aliceYears[0] != 2020 || aliceYears[1] != 2023 {
		t.Errorf("alice years = %v, want [2020, 2023]", aliceYears)
	}

	// Bob should have years [2023] (the undated one is in unknown).
	bobYears := idx.personYears["bob"]
	if len(bobYears) != 1 || bobYears[0] != 2023 {
		t.Errorf("bob years = %v, want [2023]", bobYears)
	}
}

func TestClassifyBucketedSameYear(t *testing.T) {
	c := NewKNNClassifier(5, 0.3, 0)
	queryDate := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)

	exemplars := []ExemplarData{
		makeExemplarDated("alice", []float32{1, 0, 0}, "/a1.jpg", queryDate),
		makeExemplarDated("alice", []float32{0.99, 0.1, 0}, "/a2.jpg", queryDate),
		makeExemplarDated("alice", []float32{0.98, 0.05, 0.05}, "/a3.jpg", queryDate),
		makeExemplarDated("bob", []float32{0, 1, 0}, "/b1.jpg", queryDate),
		makeExemplarDated("bob", []float32{0, 0.99, 0.1}, "/b2.jpg", queryDate),
	}

	idx := BuildBucketedIndex(exemplars)
	result := c.ClassifyBucketed([]float32{1, 0, 0}, "/test.jpg", queryDate, idx)

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

func TestClassifyBucketedCrossYear(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0)
	queryDate := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)
	oldDate := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC) // 5 years away

	// Alice has exemplars in 2023 (near query), Bob in 2018 (far).
	// Both have similar raw cosine similarities, but Alice's should win
	// due to temporal weighting (1.0 vs 0.47).
	exemplars := []ExemplarData{
		makeExemplarDated("alice", []float32{0.9, 0.1, 0}, "/a1.jpg", queryDate),
		makeExemplarDated("alice", []float32{0.85, 0.15, 0}, "/a2.jpg", queryDate),
		makeExemplarDated("bob", []float32{0.95, 0.05, 0}, "/b1.jpg", oldDate),
		makeExemplarDated("bob", []float32{0.92, 0.08, 0}, "/b2.jpg", oldDate),
	}

	idx := BuildBucketedIndex(exemplars)
	result := c.ClassifyBucketed([]float32{1, 0, 0}, "/test.jpg", queryDate, idx)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Alice should win because temporal weighting boosts her 2023 exemplars.
	if result.PersonID != "alice" {
		t.Errorf("PersonID = %q, want alice (temporal weighting should prefer same-year)", result.PersonID)
	}
}

func TestClassifyBucketedAllUnknown(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0)
	queryDate := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)

	// All exemplars have zero DateTaken — should degrade to uniform weighting.
	exemplars := []ExemplarData{
		makeExemplarDated("alice", []float32{1, 0, 0}, "/a1.jpg", time.Time{}),
		makeExemplarDated("alice", []float32{0.99, 0.1, 0}, "/a2.jpg", time.Time{}),
		makeExemplarDated("alice", []float32{0.98, 0.05, 0.05}, "/a3.jpg", time.Time{}),
		makeExemplarDated("bob", []float32{0, 1, 0}, "/b1.jpg", time.Time{}),
	}

	idx := BuildBucketedIndex(exemplars)
	result := c.ClassifyBucketed([]float32{1, 0, 0}, "/test.jpg", queryDate, idx)

	if result == nil {
		t.Fatal("expected non-nil result (all unknown should still classify)")
	}
	if result.PersonID != "alice" {
		t.Errorf("PersonID = %q, want alice", result.PersonID)
	}
}

func TestClassifyBucketedSameImageExclusion(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0)
	queryDate := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)

	// Only exemplar from the same image path — should be excluded.
	exemplars := []ExemplarData{
		makeExemplarDated("alice", []float32{1, 0, 0}, "/test.jpg", queryDate),
	}

	idx := BuildBucketedIndex(exemplars)
	result := c.ClassifyBucketed([]float32{1, 0, 0}, "/test.jpg", queryDate, idx)

	if result != nil {
		t.Error("expected nil result when all exemplars are from same image")
	}
}

func TestClassifyBucketedEmptyExemplars(t *testing.T) {
	c := NewKNNClassifier(3, 0.3, 0)
	idx := BuildBucketedIndex(nil)
	result := c.ClassifyBucketed([]float32{1, 0, 0}, "/test.jpg", time.Now(), idx)
	if result != nil {
		t.Error("expected nil result for empty exemplars")
	}
}

func TestNearestYearOrder(t *testing.T) {
	years := []int{2018, 2019, 2021, 2023}

	order := nearestYearOrder(years, 2020)
	// Expected: 2019 (dist=1), 2021 (dist=1, but 2019 is earlier), 2018 (dist=2), 2023 (dist=3)
	expected := []int{2019, 2021, 2018, 2023}
	if len(order) != len(expected) {
		t.Fatalf("order length = %d, want %d", len(order), len(expected))
	}
	for i, y := range expected {
		if order[i] != y {
			t.Errorf("order[%d] = %d, want %d (full order: %v)", i, order[i], y, order)
			break
		}
	}
}

func TestNearestYearOrderExactMatch(t *testing.T) {
	years := []int{2018, 2020, 2022}
	order := nearestYearOrder(years, 2020)
	if order[0] != 2020 {
		t.Errorf("first year = %d, want 2020 (exact match)", order[0])
	}
}

func TestTemporalWeight(t *testing.T) {
	tests := []struct {
		queryYear    int
		exemplarYear int
		wantMin      float64
		wantMax      float64
	}{
		{2023, 2023, 0.99, 1.01}, // same year → 1.0
		{2023, 2022, 0.85, 0.87}, // 1 year → ~0.86
		{2023, 2018, 0.46, 0.48}, // 5 years → ~0.47
		{2023, 2013, 0.21, 0.23}, // 10 years → ~0.22
	}

	for _, tt := range tests {
		w := temporalWeight(tt.queryYear, tt.exemplarYear)
		if w < tt.wantMin || w > tt.wantMax {
			t.Errorf("temporalWeight(%d, %d) = %f, want [%f, %f]",
				tt.queryYear, tt.exemplarYear, w, tt.wantMin, tt.wantMax)
		}
	}
}

func TestTemporalWeightSymmetric(t *testing.T) {
	w1 := temporalWeight(2023, 2020)
	w2 := temporalWeight(2020, 2023)
	if math.Abs(w1-w2) > 1e-9 {
		t.Errorf("temporal weight not symmetric: %f vs %f", w1, w2)
	}
}
