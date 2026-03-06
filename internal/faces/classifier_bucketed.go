//go:build faces

package faces

import (
	"math"
	"sort"
	"time"
)

const (
	// temporalDecayLambda controls exponential decay for year distance.
	// Weight = exp(-lambda * |yearDiff|)
	// At 0.15: 1yr=0.86, 2yr=0.74, 3yr=0.64, 5yr=0.47, 10yr=0.22
	temporalDecayLambda = 0.15

	// unknownDateWeight is applied when exemplar or query date is unknown.
	// Equivalent to ~5 years of decay.
	unknownDateWeight = 0.47
)

// BucketKey identifies a per-person-per-year bucket.
type BucketKey struct {
	PersonID string
	Year     int
}

// TimeBucket holds exemplars for one person in one year.
type TimeBucket struct {
	Key       BucketKey
	Exemplars []ExemplarData
}

// BucketedIndex organizes exemplars into per-person-per-year buckets
// for time-aware KNN classification.
type BucketedIndex struct {
	buckets       map[BucketKey]*TimeBucket
	personYears   map[string][]int // PersonID → sorted years
	unknownBucket *TimeBucket      // exemplars with no date
}

// WeightedMatch extends KNNMatch with temporal weighting.
type WeightedMatch struct {
	KNNMatch
	TemporalWeight float64
	WeightedSim    float64 // Similarity * TemporalWeight
}

// BuildBucketedIndex partitions exemplars into per-person-per-year buckets.
// Exemplars with zero DateTaken go to the unknown bucket.
func BuildBucketedIndex(exemplars []ExemplarData) *BucketedIndex {
	idx := &BucketedIndex{
		buckets:       make(map[BucketKey]*TimeBucket),
		personYears:   make(map[string][]int),
		unknownBucket: &TimeBucket{},
	}

	yearSet := make(map[string]map[int]bool) // PersonID → set of years

	for _, ex := range exemplars {
		if ex.DateTaken.IsZero() {
			idx.unknownBucket.Exemplars = append(idx.unknownBucket.Exemplars, ex)
			continue
		}

		year := ex.DateTaken.Year()
		key := BucketKey{PersonID: ex.PersonID, Year: year}

		bucket, ok := idx.buckets[key]
		if !ok {
			bucket = &TimeBucket{Key: key}
			idx.buckets[key] = bucket
		}
		bucket.Exemplars = append(bucket.Exemplars, ex)

		if yearSet[ex.PersonID] == nil {
			yearSet[ex.PersonID] = make(map[int]bool)
		}
		yearSet[ex.PersonID][year] = true
	}

	// Build sorted year lists per person.
	for pid, years := range yearSet {
		sorted := make([]int, 0, len(years))
		for y := range years {
			sorted = append(sorted, y)
		}
		sort.Ints(sorted)
		idx.personYears[pid] = sorted
	}

	return idx
}

// ClassifyBucketed determines the most likely person using time-bucketed cascade search.
// queryDate determines which year buckets to search first.
// Returns nil if no confident match is found.
func (c *KNNClassifier) ClassifyBucketed(embedding []float32, imagePath string, queryDate time.Time, idx *BucketedIndex) *ClassifyResult {
	queryYear := queryDate.Year()
	if queryDate.IsZero() {
		queryYear = idx.medianYear()
	}

	// Collect weighted candidates from all persons.
	var allCandidates []WeightedMatch

	// Search each person's buckets via cascade.
	for pid, years := range idx.personYears {
		candidates := idx.cascadeSearch(pid, years, embedding, imagePath, queryYear, c.K)
		allCandidates = append(allCandidates, candidates...)
	}

	// Search the unknown bucket (all persons mixed).
	if len(idx.unknownBucket.Exemplars) > 0 {
		unknownMatches := searchBucketExemplars(embedding, imagePath, idx.unknownBucket, c.K)
		for _, m := range unknownMatches {
			allCandidates = append(allCandidates, WeightedMatch{
				KNNMatch:       m,
				TemporalWeight: unknownDateWeight,
				WeightedSim:    m.Similarity * unknownDateWeight,
			})
		}
	}

	if len(allCandidates) == 0 {
		return nil
	}

	// Sort all candidates by weighted similarity (descending).
	sort.Slice(allCandidates, func(i, j int) bool {
		return allCandidates[i].WeightedSim > allCandidates[j].WeightedSim
	})

	// Take top K.
	k := c.K
	if k > len(allCandidates) {
		k = len(allCandidates)
	}
	topK := allCandidates[:k]

	// Weighted majority vote.
	weightSum := make(map[string]float64) // PersonID → sum of temporal weights
	simSum := make(map[string]float64)    // PersonID → sum of raw similarities
	voteCounts := make(map[string]int)    // PersonID → count
	totalWeight := 0.0

	for _, wm := range topK {
		weightSum[wm.PersonID] += wm.TemporalWeight
		simSum[wm.PersonID] += wm.Similarity
		voteCounts[wm.PersonID]++
		totalWeight += wm.TemporalWeight
	}

	// Find winner by weight sum.
	var winnerID string
	bestWeight := 0.0
	for pid, w := range weightSum {
		if w > bestWeight {
			bestWeight = w
			winnerID = pid
		}
	}

	avgSim := simSum[winnerID] / float64(voteCounts[winnerID])

	// Build topK matches for result.
	matches := make([]KNNMatch, len(topK))
	for i, wm := range topK {
		matches[i] = wm.KNNMatch
	}

	// Winner needs >50% of total weight AND avg similarity >= threshold.
	if totalWeight > 0 && bestWeight > totalWeight/2 && avgSim >= c.Threshold {
		return &ClassifyResult{
			PersonID:   winnerID,
			Confidence: avgSim,
			TopK:       matches,
		}
	}

	return nil
}

// cascadeSearch searches a person's year buckets starting from the nearest year,
// expanding outward. Returns up to k weighted candidates.
func (idx *BucketedIndex) cascadeSearch(personID string, years []int, embedding []float32, imagePath string, queryYear, k int) []WeightedMatch {
	order := nearestYearOrder(years, queryYear)
	var candidates []WeightedMatch

	for _, year := range order {
		if len(candidates) >= k {
			break
		}

		key := BucketKey{PersonID: personID, Year: year}
		bucket, ok := idx.buckets[key]
		if !ok {
			continue
		}

		remaining := k - len(candidates)
		matches := searchBucketExemplars(embedding, imagePath, bucket, remaining)
		tw := temporalWeight(queryYear, year)

		for _, m := range matches {
			candidates = append(candidates, WeightedMatch{
				KNNMatch:       m,
				TemporalWeight: tw,
				WeightedSim:    m.Similarity * tw,
			})
		}
	}

	return candidates
}

// nearestYearOrder returns years sorted by distance from queryYear.
// On tie (equidistant), prefers the earlier year.
func nearestYearOrder(years []int, queryYear int) []int {
	type yearDist struct {
		year int
		dist int
	}

	dists := make([]yearDist, len(years))
	for i, y := range years {
		d := y - queryYear
		if d < 0 {
			d = -d
		}
		dists[i] = yearDist{year: y, dist: d}
	}

	sort.Slice(dists, func(i, j int) bool {
		if dists[i].dist != dists[j].dist {
			return dists[i].dist < dists[j].dist
		}
		return dists[i].year < dists[j].year
	})

	result := make([]int, len(dists))
	for i, d := range dists {
		result[i] = d.year
	}
	return result
}

// searchBucketExemplars runs brute-force KNN within a single bucket.
// Excludes exemplars from the same image (self-match prevention).
// Returns up to k matches sorted by similarity (descending).
func searchBucketExemplars(embedding []float32, imagePath string, bucket *TimeBucket, k int) []KNNMatch {
	type scored struct {
		idx int
		sim float64
	}

	var scores []scored
	for i, ex := range bucket.Exemplars {
		if ex.ImagePath == imagePath {
			continue
		}
		sim := cosineSimilarity(embedding, ex.Embedding)
		scores = append(scores, scored{idx: i, sim: sim})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].sim > scores[j].sim
	})

	n := k
	if n > len(scores) {
		n = len(scores)
	}
	matches := make([]KNNMatch, n)
	for i := range n {
		ex := bucket.Exemplars[scores[i].idx]
		matches[i] = KNNMatch{
			PersonID:   ex.PersonID,
			Similarity: scores[i].sim,
			ImagePath:  ex.ImagePath,
			DateTaken:  ex.DateTaken,
		}
	}

	return matches
}

// temporalWeight computes exponential decay based on year distance.
func temporalWeight(queryYear, exemplarYear int) float64 {
	diff := queryYear - exemplarYear
	if diff < 0 {
		diff = -diff
	}
	return math.Exp(-temporalDecayLambda * float64(diff))
}

// medianYear returns the median year across all buckets, used as fallback
// when query date is unknown.
func (idx *BucketedIndex) medianYear() int {
	var allYears []int
	for key := range idx.buckets {
		allYears = append(allYears, key.Year)
	}
	if len(allYears) == 0 {
		return time.Now().Year()
	}
	sort.Ints(allYears)
	return allYears[len(allYears)/2]
}
