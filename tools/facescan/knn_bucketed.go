package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	// temporalDecayLambda controls the exponential decay rate for temporal weighting.
	// Weight = exp(-lambda * |yearDiff|)
	// At lambda=0.15: 1yr=0.86, 2yr=0.74, 3yr=0.64, 5yr=0.47, 10yr=0.22
	temporalDecayLambda = 0.15

	// unknownDateWeight is applied to exemplars with no date or when query date is unknown.
	// Equivalent to ~5 years of decay.
	unknownDateWeight = float32(0.47)

	// defaultMaxPerQuarter is the default cap on exemplars per person per quarter.
	defaultMaxPerQuarter = 50
)

// bucketKey identifies a per-person-per-year bucket.
type bucketKey struct {
	Label string
	Year  int
}

// timeBucket holds exemplars for one person in one year.
type timeBucket struct {
	Key       bucketKey
	Exemplars []exemplar
}

// bucketedIndex organizes exemplars into per-person-per-year buckets
// for time-aware KNN classification.
type bucketedIndex struct {
	buckets       map[bucketKey]*timeBucket
	personYears   map[string][]int // label → sorted years with data
	unknownBucket *timeBucket      // exemplars with no date
}

// weightedMatch extends knnMatch with temporal weighting.
type weightedMatch struct {
	knnMatch
	TemporalWeight float32
	WeightedSim    float32 // Similarity * TemporalWeight
}

// testFaceRecord is used by benchmarks to hold a test face's data.
type testFaceRecord struct {
	ImagePath string
	FaceIdx   int
	Embedding []float32
	QueryDate time.Time
}

// buildBucketedIndex partitions exemplars into per-person-per-year buckets.
// Exemplars with zero DateTaken go to the unknown bucket.
func buildBucketedIndex(exemplars []exemplar) (*bucketedIndex, time.Duration) {
	start := time.Now()

	bi := &bucketedIndex{
		buckets:       make(map[bucketKey]*timeBucket),
		personYears:   make(map[string][]int),
		unknownBucket: &timeBucket{},
	}

	yearSet := make(map[string]map[int]bool) // label → set of years

	for _, ex := range exemplars {
		if ex.DateTaken.IsZero() {
			bi.unknownBucket.Exemplars = append(bi.unknownBucket.Exemplars, ex)
			continue
		}

		year := ex.DateTaken.Year()
		key := bucketKey{Label: ex.Label, Year: year}

		bucket, ok := bi.buckets[key]
		if !ok {
			bucket = &timeBucket{Key: key}
			bi.buckets[key] = bucket
		}
		bucket.Exemplars = append(bucket.Exemplars, ex)

		if yearSet[ex.Label] == nil {
			yearSet[ex.Label] = make(map[int]bool)
		}
		yearSet[ex.Label][year] = true
	}

	// Build sorted year lists per person.
	for label, years := range yearSet {
		sorted := make([]int, 0, len(years))
		for y := range years {
			sorted = append(sorted, y)
		}
		sort.Ints(sorted)
		bi.personYears[label] = sorted
	}

	return bi, time.Since(start)
}

// classifyKNNBucketed runs KNN classification using time-bucketed cascade search.
// queryDate determines which year buckets to search first.
func classifyKNNBucketed(embedding []float32, _ string, idx *bucketedIndex, queryDate time.Time, k int, threshold float32) knnResult {
	queryYear := queryDate.Year()
	if queryDate.IsZero() {
		queryYear = idx.medianYear()
	}

	// Collect weighted candidates from all persons.
	var allCandidates []weightedMatch

	// Search each person's buckets via cascade.
	for label, years := range idx.personYears {
		candidates := idx.cascadeSearch(label, years, embedding, queryYear, k)
		allCandidates = append(allCandidates, candidates...)
	}

	// Also search the unknown bucket (all persons mixed).
	if len(idx.unknownBucket.Exemplars) > 0 {
		unknownMatches := searchBucket(embedding, idx.unknownBucket, k)
		for _, m := range unknownMatches {
			allCandidates = append(allCandidates, weightedMatch{
				knnMatch:       m,
				TemporalWeight: unknownDateWeight,
				WeightedSim:    m.Similarity * unknownDateWeight,
			})
		}
	}

	if len(allCandidates) == 0 {
		return knnResult{Label: "unknown", Confidence: 0}
	}

	// Sort all candidates by weighted similarity (descending).
	sort.Slice(allCandidates, func(i, j int) bool {
		return allCandidates[i].WeightedSim > allCandidates[j].WeightedSim
	})

	// Take top K.
	topK := allCandidates
	if len(topK) > k {
		topK = topK[:k]
	}

	// Weighted majority vote.
	weightSum := make(map[string]float32) // label → sum of temporal weights
	simSum := make(map[string]float32)    // label → sum of raw similarities
	voteCounts := make(map[string]int)    // label → count
	totalWeight := float32(0)

	for _, wm := range topK {
		weightSum[wm.Label] += wm.TemporalWeight
		simSum[wm.Label] += wm.Similarity
		voteCounts[wm.Label]++
		totalWeight += wm.TemporalWeight
	}

	// Find winner by weight sum.
	bestLabel := ""
	bestWeight := float32(0)
	for label, w := range weightSum {
		if w > bestWeight {
			bestWeight = w
			bestLabel = label
		}
	}

	avgSim := simSum[bestLabel] / float32(voteCounts[bestLabel])

	// Build topK matches for result.
	topKMatches := make([]knnMatch, len(topK))
	for i, wm := range topK {
		topKMatches[i] = wm.knnMatch
	}

	// Winner needs >50% of total weight AND avg similarity >= threshold.
	if totalWeight > 0 && bestWeight > totalWeight/2 && avgSim >= threshold {
		return knnResult{
			Label:      bestLabel,
			Confidence: avgSim,
			TopK:       topKMatches,
		}
	}

	return knnResult{
		Label:      "unknown",
		Confidence: avgSim,
		TopK:       topKMatches,
	}
}

// cascadeSearch searches a person's year buckets starting from the nearest year,
// expanding outward. Returns up to k weighted candidates.
func (bi *bucketedIndex) cascadeSearch(label string, years []int, embedding []float32, queryYear int, k int) []weightedMatch {
	order := nearestYearOrder(years, queryYear)
	var candidates []weightedMatch

	for _, year := range order {
		if len(candidates) >= k {
			break
		}

		key := bucketKey{Label: label, Year: year}
		bucket, ok := bi.buckets[key]
		if !ok {
			continue
		}

		remaining := k - len(candidates)
		matches := searchBucket(embedding, bucket, remaining)
		tw := temporalWeight(queryYear, year)

		for _, m := range matches {
			candidates = append(candidates, weightedMatch{
				knnMatch:       m,
				TemporalWeight: tw,
				WeightedSim:    m.Similarity * tw,
			})
		}
	}

	return candidates
}

// nearestYearOrder returns years sorted by distance from queryYear.
// On tie (equidistant), prefers the earlier year (more conservative for children).
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
		return dists[i].year < dists[j].year // prefer earlier on tie
	})

	result := make([]int, len(dists))
	for i, d := range dists {
		result[i] = d.year
	}
	return result
}

// searchBucket runs brute-force KNN within a single bucket.
// Returns up to k matches sorted by similarity (descending).
func searchBucket(embedding []float32, bucket *timeBucket, k int) []knnMatch {
	type scored struct {
		idx int
		sim float32
	}

	scores := make([]scored, len(bucket.Exemplars))
	for i, ex := range bucket.Exemplars {
		scores[i] = scored{idx: i, sim: dotProduct(embedding, ex.Embedding)}
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].sim > scores[j].sim
	})

	n := min(k, len(scores))
	matches := make([]knnMatch, n)
	for i := range n {
		ex := bucket.Exemplars[scores[i].idx]
		matches[i] = knnMatch{
			Label:      ex.Label,
			Similarity: scores[i].sim,
			FileName:   ex.FileName,
		}
	}

	return matches
}

// temporalWeight computes exponential decay based on year distance.
func temporalWeight(queryYear, exemplarYear int) float32 {
	diff := queryYear - exemplarYear
	if diff < 0 {
		diff = -diff
	}
	return float32(math.Exp(-temporalDecayLambda * float64(diff)))
}

// medianYear returns the median year across all buckets, used as fallback
// when query date is unknown.
func (bi *bucketedIndex) medianYear() int {
	var allYears []int
	for key := range bi.buckets {
		allYears = append(allYears, key.Year)
	}
	if len(allYears) == 0 {
		return time.Now().Year()
	}
	sort.Ints(allYears)
	return allYears[len(allYears)/2]
}

// quarterKey returns a string key for per-person-per-quarter cap tracking.
// Format: "label:YYYY-QN" where N is 1-4.
func quarterKey(label string, date time.Time) string {
	q := (int(date.Month())-1)/3 + 1
	return fmt.Sprintf("%s:%d-Q%d", label, date.Year(), q)
}

// benchmarkKNN3Way runs brute-force, HNSW, and bucketed classification on the same
// test faces and prints a 3-way comparison table.
func benchmarkKNN3Way(testFaces []testFaceRecord, exemplars []exemplar, queryDate time.Time, k int, threshold float32) {
	fmt.Println("\n=== KNN Benchmark: Brute-Force vs HNSW vs Bucketed ===")
	fmt.Printf("  Exemplars: %d, Test faces: %d, K=%d, threshold=%.2f\n\n", len(exemplars), len(testFaces), k, threshold)

	// --- Brute-force ---
	bfStart := time.Now()
	bfResults := make([]knnResult, len(testFaces))
	for i, tf := range testFaces {
		bfResults[i] = classifyKNN(tf.Embedding, tf.ImagePath, exemplars, k, threshold)
	}
	bfDuration := time.Since(bfStart)

	// --- HNSW ---
	idx, hnswBuildDuration := buildHNSWIndex(exemplars)
	hnswStart := time.Now()
	hnswResults := make([]knnResult, len(testFaces))
	for i, tf := range testFaces {
		hnswResults[i] = classifyKNNHNSW(tf.Embedding, tf.ImagePath, idx, k, threshold)
	}
	hnswQueryDuration := time.Since(hnswStart)
	hnswTotalDuration := hnswBuildDuration + hnswQueryDuration

	// --- Bucketed ---
	bIdx, bucketBuildDuration := buildBucketedIndex(exemplars)
	bucketedStart := time.Now()
	bucketedResults := make([]knnResult, len(testFaces))
	for i, tf := range testFaces {
		qd := tf.QueryDate
		if qd.IsZero() {
			qd = queryDate
		}
		bucketedResults[i] = classifyKNNBucketed(tf.Embedding, tf.ImagePath, bIdx, qd, k, threshold)
	}
	bucketedQueryDuration := time.Since(bucketedStart)
	bucketedTotalDuration := bucketBuildDuration + bucketedQueryDuration

	// Print bucket distribution stats.
	fmt.Printf("  Bucket stats: %d dated buckets, %d unknown exemplars\n",
		len(bIdx.buckets), len(bIdx.unknownBucket.Exemplars))
	for label, years := range bIdx.personYears {
		sizes := make([]string, 0, len(years))
		for _, y := range years {
			key := bucketKey{Label: label, Year: y}
			if b, ok := bIdx.buckets[key]; ok {
				sizes = append(sizes, fmt.Sprintf("%d:%d", y, len(b.Exemplars)))
			}
		}
		fmt.Printf("    %-15s %s\n", label, strings.Join(sizes, " "))
	}
	fmt.Println()

	// Print timing table.
	fmt.Printf("  %-25s %12s %12s %12s\n", "", "Brute-Force", "HNSW", "Bucketed")
	fmt.Printf("  %-25s %12s %12s %12s\n", "", "-----------", "----", "--------")
	fmt.Printf("  %-25s %12s %12s %12s\n", "Index build", "n/a", fmtDuration(hnswBuildDuration), fmtDuration(bucketBuildDuration))
	fmt.Printf("  %-25s %12s %12s %12s\n", "Classification", fmtDuration(bfDuration), fmtDuration(hnswQueryDuration), fmtDuration(bucketedQueryDuration))
	fmt.Printf("  %-25s %12s %12s %12s\n", "Total", fmtDuration(bfDuration), fmtDuration(hnswTotalDuration), fmtDuration(bucketedTotalDuration))

	if len(testFaces) > 0 {
		bfPerFace := bfDuration / time.Duration(len(testFaces))
		hnswPerFace := hnswQueryDuration / time.Duration(len(testFaces))
		bucketedPerFace := bucketedQueryDuration / time.Duration(len(testFaces))
		fmt.Printf("  %-25s %12s %12s %12s\n", "Per face", fmtDuration(bfPerFace), fmtDuration(hnswPerFace), fmtDuration(bucketedPerFace))
	}

	if bfDuration > 0 {
		hnswSpeedup := float64(bfDuration) / float64(hnswQueryDuration)
		bucketedSpeedup := float64(bfDuration) / float64(bucketedQueryDuration)
		fmt.Printf("\n  Query speedup vs brute-force: HNSW=%.1fx, Bucketed=%.1fx\n", hnswSpeedup, bucketedSpeedup)
	}

	// Agreement matrix.
	bfHnswAgree, bfBucketAgree, hnswBucketAgree := 0, 0, 0
	bfClassified, hnswClassified, bucketedClassified := 0, 0, 0

	for i := range testFaces {
		bf := bfResults[i].Label
		hw := hnswResults[i].Label
		bu := bucketedResults[i].Label

		if bf != "unknown" {
			bfClassified++
		}
		if hw != "unknown" {
			hnswClassified++
		}
		if bu != "unknown" {
			bucketedClassified++
		}
		if bf == hw {
			bfHnswAgree++
		}
		if bf == bu {
			bfBucketAgree++
		}
		if hw == bu {
			hnswBucketAgree++
		}
	}

	n := len(testFaces)
	fmt.Printf("\n  %-25s %12d %12d %12d\n", "Classified", bfClassified, hnswClassified, bucketedClassified)
	fmt.Printf("  %-25s %12d %12d %12d\n", "Unknown", n-bfClassified, n-hnswClassified, n-bucketedClassified)

	if n > 0 {
		fmt.Printf("\n  Agreement rates:\n")
		fmt.Printf("    BF↔HNSW:     %d/%d (%.1f%%)\n", bfHnswAgree, n, float64(bfHnswAgree)/float64(n)*100)
		fmt.Printf("    BF↔Bucketed: %d/%d (%.1f%%)\n", bfBucketAgree, n, float64(bfBucketAgree)/float64(n)*100)
		fmt.Printf("    HNSW↔Bucket: %d/%d (%.1f%%)\n", hnswBucketAgree, n, float64(hnswBucketAgree)/float64(n)*100)
	}

	// Show BF↔Bucketed disagreements.
	bfBucketDisagree := n - bfBucketAgree
	if bfBucketDisagree > 0 {
		fmt.Printf("\n  BF↔Bucketed disagreements:\n")
		shown := 0
		for i := range testFaces {
			if bfResults[i].Label != bucketedResults[i].Label {
				fmt.Printf("    %s face_%d: BF=%s(%.3f) Buck=%s(%.3f)\n",
					shortPath(testFaces[i].ImagePath), testFaces[i].FaceIdx,
					bfResults[i].Label, bfResults[i].Confidence,
					bucketedResults[i].Label, bucketedResults[i].Confidence)
				shown++
				if shown >= 20 {
					fmt.Printf("    ... and %d more\n", bfBucketDisagree-shown)
					break
				}
			}
		}
	}
}
