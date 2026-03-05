package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const dataDir = ".facescan"

func dataPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, dataDir)
}

func modelsPath() string {
	return filepath.Join(dataPath(), "models")
}

func dbPath() string {
	return filepath.Join(dataPath(), "data.json")
}

func usage() {
	fmt.Fprintf(os.Stderr, `facescan — standalone face detection, clustering, and recognition

Usage:
  facescan scan <dir1> [dir2] ...   Detect faces in images, extract embeddings, cluster
  facescan clusters                  Show face clusters with sample images
  facescan label <cluster-id> <name> Assign a person name to a cluster
  facescan search <name>            Find images containing a named person
  facescan stats                     Show scan statistics
  facescan imageview <path>          Preview an image in the terminal

Options (for scan):
  --force             Re-scan all images (ignore previous results)
  --threshold <float> Face detection confidence threshold (default: 0.7)
  --sim <float>       Average-linkage similarity threshold for clustering (default: 0.30)
  --min-face <int>    Minimum face size in pixels (default: 40)
  --max-res <int>     Max image resolution for detection (default: 1024)

Examples:
  facescan scan ~/Pictures/Family_ImranBirthday_20210115
  facescan scan ~/Pictures/Family_ImranBirthday_20210115 ~/Pictures/Family_MahdiBirthday_20210313
  facescan clusters
  facescan extract                   Extract face thumbnails grouped by cluster
  facescan suggest                   Show merge candidates (similar clusters)
  facescan merge <id1> <id2> [id3..] Merge clusters (same person split apart)
  facescan split <id>                Re-cluster a single cluster at tighter threshold
  facescan label 1 "Dad"
  facescan search "Dad"
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "scan":
		cmdScan(os.Args[2:])
	case "clusters":
		cmdClusters()
	case "extract":
		cmdExtract()
	case "merge":
		cmdMerge(os.Args[2:])
	case "split":
		cmdSplit(os.Args[2:])
	case "label":
		cmdLabel(os.Args[2:])
	case "search":
		cmdSearch(os.Args[2:])
	case "suggest":
		cmdSuggest()
	case "stats":
		cmdStats()
	case "imageview":
		cmdImageView(os.Args[2:])
	case "knn-test":
		cmdKNNTest(os.Args[2:])
	case "bootstrap":
		cmdBootstrap(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func cmdScan(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: specify at least one directory to scan")
		os.Exit(1)
	}

	// Parse flags.
	var dirs []string
	threshold := float32(0.7)
	simThreshold := float32(0.30) // Average-linkage agglomerative clustering threshold
	minFace := 40
	maxRes := 1024
	force := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force":
			force = true
		case "--threshold":
			i++
			if v, err := strconv.ParseFloat(args[i], 32); err == nil {
				threshold = float32(v)
			}
		case "--sim":
			i++
			if v, err := strconv.ParseFloat(args[i], 32); err == nil {
				simThreshold = float32(v)
			}
		case "--min-face":
			i++
			if v, err := strconv.Atoi(args[i]); err == nil {
				minFace = v
			}
		case "--max-res":
			i++
			if v, err := strconv.Atoi(args[i]); err == nil {
				maxRes = v
			}
		default:
			dirs = append(dirs, args[i])
		}
	}

	// Ensure models are downloaded.
	fmt.Println("Checking models...")
	if err := ensureModels(modelsPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading models: %v\n", err)
		os.Exit(1)
	}

	// Load existing data (for incremental scan).
	db := &FaceDB{}
	if !force {
		if loaded, err := LoadDB(dbPath()); err == nil {
			db = loaded
		}
	} else {
		db = &FaceDB{Labels: make(map[int]string)}
	}

	// Collect image files.
	var images []string
	for _, dir := range dirs {
		expanded := expandPath(dir)
		files, err := collectImages(expanded)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: error scanning %s: %v\n", dir, err)
			continue
		}
		images = append(images, files...)
	}

	if len(images) == 0 {
		fmt.Println("No image files found in the specified directories.")
		return
	}

	fmt.Printf("Found %d images to process\n\n", len(images))

	// Create scanner.
	scanner, err := NewScanner(modelsPath(), threshold, minFace, maxRes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating scanner: %v\n", err)
		os.Exit(1)
	}
	defer scanner.Close()

	// Build set of already-scanned images (for incremental scan).
	scanned := make(map[string]bool)
	if !force {
		for _, f := range db.Faces {
			scanned[f.ImagePath] = true
		}
	}

	// Scan images.
	newFaces := 0
	processedImages := 0
	for i, imgPath := range images {
		if scanned[imgPath] {
			fmt.Printf("  [%d/%d] %s — already scanned, skipping\n", i+1, len(images), shortPath(imgPath))
			continue
		}

		faces, err := scanner.DetectFaces(imgPath)
		if err != nil {
			fmt.Printf("  [%d/%d] %s — error: %v\n", i+1, len(images), shortPath(imgPath), err)
			continue
		}

		for _, f := range faces {
			db.Faces = append(db.Faces, f)
		}
		newFaces += len(faces)
		processedImages++
		fmt.Printf("  [%d/%d] %s — %d faces detected\n", i+1, len(images), shortPath(imgPath), len(faces))
	}

	fmt.Printf("\nProcessed %d new images, detected %d new faces (total: %d faces)\n", processedImages, newFaces, len(db.Faces))

	if len(db.Faces) < 2 {
		fmt.Println("Need at least 2 faces for clustering. Saving results...")
		if err := db.Save(dbPath()); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		}
		return
	}

	// Run DBSCAN clustering.
	fmt.Printf("\nClustering %d face embeddings (similarity threshold: %.3f)...\n", len(db.Faces), simThreshold)
	embeddings := make([][]float32, len(db.Faces))
	imgPaths := make([]string, len(db.Faces))
	for i, f := range db.Faces {
		embeddings[i] = f.Embedding
		imgPaths[i] = f.ImagePath
	}

	// Agglomerative clustering with average linkage.
	// Average linkage considers ALL cross-cluster face pairs, preventing
	// chaining through a single high-similarity outlier pair (common in family photos).
	clusterLabels := agglomerativeClustering(embeddings, imgPaths, simThreshold, 2)

	// Absorb noise faces into their nearest cluster (single-pass, no chaining).
	absorbThreshold := simThreshold * 0.75 // slightly below clustering threshold
	noiseBefore := 0
	for _, cl := range clusterLabels {
		if cl == -1 {
			noiseBefore++
		}
	}
	if noiseBefore > 0 {
		clusterLabels = absorbNoise(embeddings, imgPaths, clusterLabels, absorbThreshold)
		noiseAfter := 0
		for _, cl := range clusterLabels {
			if cl == -1 {
				noiseAfter++
			}
		}
		if noiseBefore != noiseAfter {
			fmt.Printf("Absorbed %d noise faces into clusters\n", noiseBefore-noiseAfter)
		}
	}

	for i := range db.Faces {
		db.Faces[i].ClusterID = clusterLabels[i]
	}

	// Count clusters.
	clusterCounts := make(map[int]int)
	for _, cl := range clusterLabels {
		clusterCounts[cl]++
	}
	noise := clusterCounts[-1]
	delete(clusterCounts, -1)

	fmt.Printf("Found %d clusters (%d noise/single-occurrence faces)\n", len(clusterCounts), noise)

	// Save.
	if err := db.Save(dbPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nResults saved to %s\n", dbPath())
	fmt.Println("Run 'facescan clusters' to see results, 'facescan label <id> <name>' to name clusters.")
}

func cmdClusters() {
	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Group faces by cluster.
	clusters := make(map[int][]FaceRecord)
	for _, f := range db.Faces {
		clusters[f.ClusterID] = append(clusters[f.ClusterID], f)
	}

	fmt.Printf("%-10s %-20s %-7s %s\n", "Cluster", "Label", "Faces", "Sample Images")
	fmt.Println(strings.Repeat("-", 80))

	// Print non-noise clusters first.
	for clID := 1; clID <= maxClusterID(clusters); clID++ {
		faces, ok := clusters[clID]
		if !ok {
			continue
		}
		label := db.Labels[clID]
		if label == "" {
			label = "(unlabeled)"
		}
		samples := clusterSamples(faces, 3)
		fmt.Printf("%-10d %-20s %-7d %s\n", clID, label, len(faces), samples)
	}

	// Print noise.
	if noise, ok := clusters[-1]; ok {
		fmt.Printf("%-10s %-20s %-7d %s\n", "noise", "(single)", len(noise), clusterSamples(noise, 3))
	}
}

func cmdLabel(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: facescan label <cluster-id> <name>")
		os.Exit(1)
	}

	clusterID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid cluster ID: %s\n", args[0])
		os.Exit(1)
	}
	name := strings.Join(args[1:], " ")

	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Verify cluster exists.
	count := 0
	for _, f := range db.Faces {
		if f.ClusterID == clusterID {
			count++
		}
	}
	if count == 0 {
		fmt.Fprintf(os.Stderr, "Cluster %d not found\n", clusterID)
		os.Exit(1)
	}

	if db.Labels == nil {
		db.Labels = make(map[int]string)
	}
	db.Labels[clusterID] = name

	if err := db.Save(dbPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Labeled cluster %d as %q (%d images)\n", clusterID, name, count)
}

func cmdSearch(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: facescan search <name>")
		os.Exit(1)
	}
	name := strings.Join(args, " ")

	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Find cluster IDs for this person.
	var clusterIDs []int
	for clID, label := range db.Labels {
		if strings.EqualFold(label, name) {
			clusterIDs = append(clusterIDs, clID)
		}
	}

	if len(clusterIDs) == 0 {
		fmt.Printf("No person named %q found. Available labels:\n", name)
		for clID, label := range db.Labels {
			fmt.Printf("  Cluster %d: %s\n", clID, label)
		}
		return
	}

	// Find all images for these clusters.
	seen := make(map[string]bool)
	var results []FaceRecord
	for _, f := range db.Faces {
		for _, clID := range clusterIDs {
			if f.ClusterID == clID && !seen[f.ImagePath] {
				seen[f.ImagePath] = true
				results = append(results, f)
			}
		}
	}

	fmt.Printf("Found %d images containing %q:\n\n", len(results), name)
	for _, r := range results {
		fmt.Printf("  %s (confidence: %.2f)\n", shortPath(r.ImagePath), r.Confidence)
	}
}

func cmdSuggest() {
	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Group faces by cluster.
	clusterFaces := make(map[int][]int)
	for i, f := range db.Faces {
		if f.ClusterID > 0 {
			clusterFaces[f.ClusterID] = append(clusterFaces[f.ClusterID], i)
		}
	}

	if len(clusterFaces) < 2 {
		fmt.Println("Need at least 2 clusters to suggest merges.")
		return
	}

	// Compute average and max similarity between every cluster pair.
	type mergeSuggestion struct {
		clA, clB int
		avgSim   float32
		maxSim   float32
		overlap  bool // same-photo constraint violation
	}

	var suggestions []mergeSuggestion
	clIDs := make([]int, 0, len(clusterFaces))
	for id := range clusterFaces {
		clIDs = append(clIDs, id)
	}

	for i := 0; i < len(clIDs); i++ {
		for j := i + 1; j < len(clIDs); j++ {
			a, b := clIDs[i], clIDs[j]
			membersA := clusterFaces[a]
			membersB := clusterFaces[b]

			overlap := hasImageOverlap(membersA, membersB, extractPaths(db))

			var sumSim float32
			maxSim := float32(0)
			nPairs := 0
			for _, ai := range membersA {
				for _, bi := range membersB {
					s := cosineSimilarity(db.Faces[ai].Embedding, db.Faces[bi].Embedding)
					sumSim += s
					nPairs++
					if s > maxSim {
						maxSim = s
					}
				}
			}
			avgSim := sumSim / float32(nPairs)

			if avgSim >= 0.15 { // only show pairs with some similarity
				suggestions = append(suggestions, mergeSuggestion{
					clA: a, clB: b, avgSim: avgSim, maxSim: maxSim, overlap: overlap,
				})
			}
		}
	}

	// Sort by average similarity (descending).
	for i := 0; i < len(suggestions); i++ {
		for j := i + 1; j < len(suggestions); j++ {
			if suggestions[j].avgSim > suggestions[i].avgSim {
				suggestions[i], suggestions[j] = suggestions[j], suggestions[i]
			}
		}
	}

	fmt.Println("Merge candidates (sorted by average similarity):")
	fmt.Println()
	fmt.Printf("  %-12s %-12s %-8s %-8s %-8s %s\n", "Cluster A", "Cluster B", "Avg Sim", "Max Sim", "Overlap", "Suggestion")
	fmt.Println("  " + strings.Repeat("-", 72))

	for _, s := range suggestions {
		labelA := db.Labels[s.clA]
		if labelA == "" {
			labelA = fmt.Sprintf("cluster_%d", s.clA)
		}
		labelB := db.Labels[s.clB]
		if labelB == "" {
			labelB = fmt.Sprintf("cluster_%d", s.clB)
		}

		overlapStr := ""
		if s.overlap {
			overlapStr = "YES"
		}

		suggestion := ""
		if s.avgSim >= 0.30 && !s.overlap {
			suggestion = "LIKELY same person"
		} else if s.avgSim >= 0.25 && !s.overlap {
			suggestion = "possibly same person"
		} else if s.overlap {
			suggestion = "different people (shared photos)"
		}

		fmt.Printf("  %-12s %-12s %-8.3f %-8.3f %-8s %s\n",
			labelA, labelB, s.avgSim, s.maxSim, overlapStr, suggestion)
	}

	fmt.Println()
	fmt.Println("To merge clusters: facescan merge <id1> <id2>")
	fmt.Println("To split a cluster: facescan split <id>")
}

func extractPaths(db *FaceDB) []string {
	paths := make([]string, len(db.Faces))
	for i, f := range db.Faces {
		paths[i] = f.ImagePath
	}
	return paths
}

func cmdStats() {
	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Count unique images.
	images := make(map[string]bool)
	for _, f := range db.Faces {
		images[f.ImagePath] = true
	}

	// Count clusters.
	clusters := make(map[int]int)
	for _, f := range db.Faces {
		clusters[f.ClusterID]++
	}
	noise := clusters[-1]
	delete(clusters, -1)

	labeled := 0
	for clID := range clusters {
		if db.Labels[clID] != "" {
			labeled++
		}
	}

	fmt.Printf("Face Scan Statistics\n")
	fmt.Printf("====================\n")
	fmt.Printf("Images scanned:     %d\n", len(images))
	fmt.Printf("Faces detected:     %d\n", len(db.Faces))
	fmt.Printf("Clusters:           %d\n", len(clusters))
	fmt.Printf("  Labeled:          %d\n", labeled)
	fmt.Printf("  Unlabeled:        %d\n", len(clusters)-labeled)
	fmt.Printf("Noise faces:        %d\n", noise)
	fmt.Printf("Data file:          %s\n", dbPath())

	// Print per-person stats.
	if labeled > 0 {
		fmt.Printf("\nPeople:\n")
		for clID, label := range db.Labels {
			fmt.Printf("  %-20s %d images\n", label, clusters[clID])
		}
	}

	// Pretty-print as JSON for debugging.
	if os.Getenv("FACESCAN_DEBUG") != "" {
		fmt.Printf("\nRaw data:\n")
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(db)
	}
}

// --- Helpers ---

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func shortPath(path string) string {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func collectImages(dir string) ([]string, error) {
	var files []string
	imageExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true,
		".bmp": true, ".tiff": true, ".tif": true, ".webp": true,
	}

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if imageExts[ext] {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func maxClusterID(clusters map[int][]FaceRecord) int {
	maxID := 0
	for id := range clusters {
		if id > maxID {
			maxID = id
		}
	}
	return maxID
}

func clusterSamples(faces []FaceRecord, n int) string {
	seen := make(map[string]bool)
	var samples []string
	for _, f := range faces {
		short := shortPath(f.ImagePath)
		if !seen[short] {
			seen[short] = true
			samples = append(samples, short)
			if len(samples) >= n {
				break
			}
		}
	}
	result := strings.Join(samples, ", ")
	if len(faces) > n {
		result += ", ..."
	}
	return result
}
