package main

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gocv.io/x/gocv"
)

// exemplar is a labeled face embedding from the ideal clusters.
type exemplar struct {
	Label     string
	Embedding []float32
	FileName  string
}

// knnMatch is a single match in KNN results.
type knnMatch struct {
	Label      string
	Similarity float32
	FileName   string
}

// knnResult is the classification result for one face.
type knnResult struct {
	Label      string
	Confidence float32
	TopK       []knnMatch
}

// cmdKNNTest runs a KNN classifier test:
// 1. Loads face thumbnails from ideal_clusters/ as labeled exemplars
// 2. Scans test images, detects faces
// 3. Classifies each face using KNN
// 4. Reports results (filtered to specified labels)
//
// Usage: facescan knn-test --train <clusters-dir> --test <image-dir1> [dir2...] --labels hamza,mahdi --k 7 --threshold 0.35
func cmdKNNTest(args []string) {
	var trainDir string
	var testDirs []string
	var labelFilter []string
	k := 7
	threshold := float32(0.35)
	detThreshold := float32(0.5)
	minFace := 30
	maxRes := 1600

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--train":
			i++
			trainDir = expandPath(args[i])
		case "--test":
			i++
			for i < len(args) && !strings.HasPrefix(args[i], "--") {
				testDirs = append(testDirs, expandPath(args[i]))
				i++
			}
			i-- // back up since the for loop will increment
		case "--labels":
			i++
			labelFilter = strings.Split(strings.ToLower(args[i]), ",")
		case "--k":
			i++
			fmt.Sscanf(args[i], "%d", &k)
		case "--threshold":
			i++
			fmt.Sscanf(args[i], "%f", &threshold)
		case "--det-threshold":
			i++
			fmt.Sscanf(args[i], "%f", &detThreshold)
		default:
			testDirs = append(testDirs, expandPath(args[i]))
		}
	}

	if trainDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --train <clusters-dir> required")
		os.Exit(1)
	}
	if len(testDirs) == 0 {
		fmt.Fprintln(os.Stderr, "Error: --test <dir> required")
		os.Exit(1)
	}

	// Step 1: Load training exemplars from ideal_clusters/
	fmt.Println("=== Loading Training Exemplars ===")
	exemplars, err := loadExemplars(trainDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading exemplars: %v\n", err)
		os.Exit(1)
	}

	// Print per-label counts
	labelCounts := make(map[string]int)
	for _, ex := range exemplars {
		labelCounts[ex.Label]++
	}
	for label, count := range labelCounts {
		fmt.Printf("  %-15s %d exemplars\n", label, count)
	}
	fmt.Printf("  Total: %d exemplars across %d people\n\n", len(exemplars), len(labelCounts))

	// Step 2: Scan test images
	fmt.Println("=== Scanning Test Images ===")
	if err := ensureModels(modelsPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error ensuring models: %v\n", err)
		os.Exit(1)
	}

	scanner, err := NewScanner(modelsPath(), detThreshold, minFace, maxRes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating scanner: %v\n", err)
		os.Exit(1)
	}
	defer scanner.Close()

	type testFace struct {
		ImagePath string
		FaceIdx   int
		Embedding []float32
		Result    knnResult
	}

	var allTestFaces []testFace
	for _, dir := range testDirs {
		images, err := collectImages(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
		fmt.Printf("  Scanning %s (%d images)...\n", filepath.Base(dir), len(images))

		for _, imgPath := range images {
			faces, err := scanner.DetectFaces(imgPath)
			if err != nil {
				continue
			}
			for _, f := range faces {
				allTestFaces = append(allTestFaces, testFace{
					ImagePath: f.ImagePath,
					FaceIdx:   f.FaceIdx,
					Embedding: f.Embedding,
				})
			}
		}
	}
	fmt.Printf("  Total test faces detected: %d\n\n", len(allTestFaces))

	// Step 3: Classify each test face using KNN
	fmt.Println("=== KNN Classification ===")
	fmt.Printf("  K=%d, threshold=%.2f\n\n", k, threshold)

	// Build label filter set
	filterSet := make(map[string]bool)
	for _, l := range labelFilter {
		filterSet[l] = true
	}

	// Classify
	classifiedCounts := make(map[string]int) // label → count
	unknownCount := 0

	for i := range allTestFaces {
		result := classifyKNN(allTestFaces[i].Embedding, allTestFaces[i].ImagePath, exemplars, k, threshold)
		allTestFaces[i].Result = result
		if result.Label == "unknown" {
			unknownCount++
		} else {
			classifiedCounts[result.Label]++
		}
	}

	// Step 4: Report results
	fmt.Println("=== Results ===")
	fmt.Printf("\n  %-15s %-8s %-10s\n", "Person", "Count", "Avg Conf")
	fmt.Println("  " + strings.Repeat("-", 40))

	labels := make([]string, 0, len(classifiedCounts))
	for l := range classifiedCounts {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	for _, label := range labels {
		count := classifiedCounts[label]
		// Compute average confidence for this label
		var sumConf float32
		n := 0
		for _, tf := range allTestFaces {
			if tf.Result.Label == label {
				sumConf += tf.Result.Confidence
				n++
			}
		}
		avgConf := sumConf / float32(n)
		fmt.Printf("  %-15s %-8d %.3f\n", label, count, avgConf)
	}
	fmt.Printf("  %-15s %-8d\n", "unknown", unknownCount)
	fmt.Printf("\n  Total faces: %d, classified: %d, unknown: %d\n",
		len(allTestFaces), len(allTestFaces)-unknownCount, unknownCount)

	// Detailed per-image breakdown for filtered labels
	if len(filterSet) > 0 {
		fmt.Printf("\n=== Detailed Results (filtered: %s) ===\n\n", strings.Join(labelFilter, ", "))

		// Group by image
		type imageResult struct {
			path  string
			faces []testFace
		}
		imageMap := make(map[string]*imageResult)
		for _, tf := range allTestFaces {
			if _, ok := imageMap[tf.ImagePath]; !ok {
				imageMap[tf.ImagePath] = &imageResult{path: tf.ImagePath}
			}
			imageMap[tf.ImagePath].faces = append(imageMap[tf.ImagePath].faces, tf)
		}

		// Sort by path
		var imagePaths []string
		for p := range imageMap {
			imagePaths = append(imagePaths, p)
		}
		sort.Strings(imagePaths)

		for _, imgPath := range imagePaths {
			ir := imageMap[imgPath]
			hasFiltered := false
			for _, f := range ir.faces {
				if filterSet[strings.ToLower(f.Result.Label)] || f.Result.Label == "unknown" {
					hasFiltered = true
					break
				}
			}
			if !hasFiltered {
				continue
			}

			fmt.Printf("  %s\n", shortPath(imgPath))
			for _, f := range ir.faces {
				if !filterSet[strings.ToLower(f.Result.Label)] && f.Result.Label != "unknown" {
					// Show non-filtered labels briefly
					fmt.Printf("    face_%d: %s (%.3f)\n", f.FaceIdx, f.Result.Label, f.Result.Confidence)
					continue
				}
				fmt.Printf("    face_%d: %s (conf=%.3f)\n", f.FaceIdx, f.Result.Label, f.Result.Confidence)
				// Show top 3 matches
				topN := 3
				if len(f.Result.TopK) < topN {
					topN = len(f.Result.TopK)
				}
				for j := 0; j < topN; j++ {
					m := f.Result.TopK[j]
					fmt.Printf("      #%d: %s sim=%.3f (%s)\n", j+1, m.Label, m.Similarity, filepath.Base(m.FileName))
				}
			}
		}
	}
}

// loadExemplars reads face thumbnails from an ideal_clusters/ directory,
// extracts SFace embeddings from each thumbnail image.
func loadExemplars(clustersDir string) ([]exemplar, error) {
	// Ensure SFace model is available
	sfacePath := filepath.Join(modelsPath(), sfaceModel)
	sfaceNet := gocv.ReadNetFromONNX(sfacePath)
	if sfaceNet.Empty() {
		return nil, fmt.Errorf("failed to load SFace model from %s", sfacePath)
	}
	defer sfaceNet.Close()

	var exemplars []exemplar

	entries, err := os.ReadDir(clustersDir)
	if err != nil {
		return nil, fmt.Errorf("read clusters dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Parse label from directory name: "cluster_02_hamza" → "hamza"
		dirName := entry.Name()
		label := parseLabelFromDir(dirName)
		if label == "" {
			continue
		}

		clusterPath := filepath.Join(clustersDir, dirName)
		files, err := os.ReadDir(clusterPath)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(f.Name()))
			if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
				continue
			}

			imgPath := filepath.Join(clusterPath, f.Name())
			embedding, err := extractThumbnailEmbedding(sfaceNet, imgPath)
			if err != nil {
				fmt.Printf("  Warning: %s: %v\n", f.Name(), err)
				continue
			}

			exemplars = append(exemplars, exemplar{
				Label:     label,
				Embedding: embedding,
				FileName:  imgPath,
			})
		}
	}

	return exemplars, nil
}

// extractThumbnailEmbedding extracts a SFace embedding from a face thumbnail.
// The thumbnail IS the face crop, so we resize the whole image to 112x112.
func extractThumbnailEmbedding(sfaceNet gocv.Net, imagePath string) ([]float32, error) {
	img := gocv.IMRead(imagePath, gocv.IMReadColor)
	if img.Empty() {
		return nil, fmt.Errorf("failed to read image")
	}
	defer img.Close()

	// Resize to SFace input size
	faceMat := gocv.NewMat()
	gocv.Resize(img, &faceMat, image.Pt(112, 112), 0, 0, gocv.InterpolationLinear)

	faceBlob := gocv.BlobFromImage(faceMat, 1.0, image.Pt(112, 112),
		gocv.NewScalar(0, 0, 0, 0), true, false)
	faceMat.Close()

	sfaceNet.SetInput(faceBlob, "")
	feature := sfaceNet.Forward("")
	faceBlob.Close()

	embLen := feature.Total()
	if embLen < 128 {
		feature.Close()
		return nil, fmt.Errorf("unexpected embedding length: %d", embLen)
	}

	embedding := make([]float32, 128)
	var norm float32
	for j := 0; j < 128; j++ {
		v := feature.GetFloatAt(0, j)
		embedding[j] = v
		norm += v * v
	}
	feature.Close()

	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for j := range embedding {
			embedding[j] /= norm
		}
	}

	return embedding, nil
}

// parseLabelFromDir extracts the person label from a directory name.
// "cluster_02_hamza" → "hamza", "cluster_08_Rocky" → "rocky"
func parseLabelFromDir(dirName string) string {
	parts := strings.Split(dirName, "_")
	if len(parts) < 3 {
		return ""
	}
	// Everything after the second underscore is the label
	label := strings.Join(parts[2:], "_")
	return strings.ToLower(label)
}

// classifyKNN runs KNN classification against exemplars.
func classifyKNN(embedding []float32, imagePath string, exemplars []exemplar, k int, threshold float32) knnResult {
	// Compute similarity to all exemplars
	type scored struct {
		idx int
		sim float32
	}
	var scores []scored
	for i, ex := range exemplars {
		sim := dotProduct(embedding, ex.Embedding)
		scores = append(scores, scored{idx: i, sim: sim})
	}

	// Sort by similarity (descending)
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].sim > scores[j].sim
	})

	// Take top K (skip same-image exemplars)
	var topK []knnMatch
	for _, s := range scores {
		if len(topK) >= k {
			break
		}
		ex := exemplars[s.idx]
		topK = append(topK, knnMatch{
			Label:      ex.Label,
			Similarity: s.sim,
			FileName:   ex.FileName,
		})
	}

	if len(topK) == 0 {
		return knnResult{Label: "unknown", Confidence: 0}
	}

	// Majority vote
	votes := make(map[string]int)
	simSum := make(map[string]float32)
	for _, m := range topK {
		votes[m.Label]++
		simSum[m.Label] += m.Similarity
	}

	// Find majority
	bestLabel := ""
	bestVotes := 0
	for label, count := range votes {
		if count > bestVotes {
			bestVotes = count
			bestLabel = label
		}
	}

	avgSim := simSum[bestLabel] / float32(votes[bestLabel])

	// Check confidence: majority must have >= ceil(K/2) votes AND avg sim >= threshold
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

// dotProduct computes dot product of two L2-normalized vectors (= cosine similarity).
func dotProduct(a, b []float32) float32 {
	var dot float32
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}
