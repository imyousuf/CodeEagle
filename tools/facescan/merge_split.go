package main

import (
	"fmt"
	"os"
	"strconv"
)

// cmdMerge merges two or more clusters into the first cluster ID.
// Usage: facescan merge <id1> <id2> [id3...]
func cmdMerge(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: facescan merge <id1> <id2> [id3...]")
		os.Exit(1)
	}

	// Parse cluster IDs.
	var ids []int
	for _, a := range args {
		id, err := strconv.Atoi(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid cluster ID: %s\n", a)
			os.Exit(1)
		}
		ids = append(ids, id)
	}

	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Verify all clusters exist.
	clusterCounts := make(map[int]int)
	for _, f := range db.Faces {
		clusterCounts[f.ClusterID]++
	}
	for _, id := range ids {
		if clusterCounts[id] == 0 {
			fmt.Fprintf(os.Stderr, "Cluster %d not found\n", id)
			os.Exit(1)
		}
	}

	// Target is the first ID; all others merge into it.
	target := ids[0]
	sources := ids[1:]

	sourceSet := make(map[int]bool)
	for _, s := range sources {
		sourceSet[s] = true
	}

	// Reassign faces from source clusters to target.
	moved := 0
	for i := range db.Faces {
		if sourceSet[db.Faces[i].ClusterID] {
			db.Faces[i].ClusterID = target
			moved++
		}
	}

	// Migrate labels: if target has no label but a source does, use the source's label.
	if db.Labels[target] == "" {
		for _, s := range sources {
			if db.Labels[s] != "" {
				db.Labels[target] = db.Labels[s]
				break
			}
		}
	}

	// Remove source labels.
	for _, s := range sources {
		delete(db.Labels, s)
	}

	if err := db.Save(dbPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Merged clusters %v into cluster %d (%d faces moved, %d total in cluster)\n",
		sources, target, moved, clusterCounts[target]+moved)
	fmt.Println("Run 'facescan extract' to update face thumbnails.")
}

// cmdSplit re-clusters a single cluster at a tighter similarity threshold.
// Usage: facescan split <id> [--sim <float>]
func cmdSplit(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: facescan split <id> [--sim <float>]")
		os.Exit(1)
	}

	clusterID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid cluster ID: %s\n", args[0])
		os.Exit(1)
	}

	// Parse optional --sim flag (default: tighter than scan default).
	simThreshold := float32(0.75) // tighter than the 0.637 scan default
	for i := 1; i < len(args); i++ {
		if args[i] == "--sim" && i+1 < len(args) {
			i++
			if v, err := strconv.ParseFloat(args[i], 32); err == nil {
				simThreshold = float32(v)
			}
		}
	}

	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	// Collect faces belonging to the target cluster.
	var indices []int
	for i, f := range db.Faces {
		if f.ClusterID == clusterID {
			indices = append(indices, i)
		}
	}

	if len(indices) == 0 {
		fmt.Fprintf(os.Stderr, "Cluster %d not found\n", clusterID)
		os.Exit(1)
	}

	if len(indices) < 2 {
		fmt.Println("Cluster has fewer than 2 faces, nothing to split.")
		return
	}

	fmt.Printf("Re-clustering %d faces from cluster %d at similarity threshold %.3f...\n",
		len(indices), clusterID, simThreshold)

	// Extract embeddings and image paths for this cluster's faces.
	embeddings := make([][]float32, len(indices))
	imgPaths := make([]string, len(indices))
	for i, idx := range indices {
		embeddings[i] = db.Faces[idx].Embedding
		imgPaths[i] = db.Faces[idx].ImagePath
	}

	// Run DBSCAN with tighter threshold.
	subLabels := DBSCANClustering(embeddings, imgPaths, simThreshold, 2)

	// Find the next available cluster ID in the full database.
	maxID := 0
	for _, f := range db.Faces {
		if f.ClusterID > maxID {
			maxID = f.ClusterID
		}
	}

	// Map sub-cluster labels to new global cluster IDs.
	// Sub-cluster 1 keeps the original cluster ID; subsequent ones get new IDs.
	subClusterMap := make(map[int]int) // sub-label → global cluster ID
	nextID := maxID + 1

	for _, sl := range subLabels {
		if sl <= 0 {
			continue // noise handled below
		}
		if _, exists := subClusterMap[sl]; !exists {
			if sl == 1 {
				subClusterMap[sl] = clusterID // keep original ID for first sub-cluster
			} else {
				subClusterMap[sl] = nextID
				nextID++
			}
		}
	}

	// Apply new cluster IDs.
	for i, idx := range indices {
		sl := subLabels[i]
		if sl == -1 {
			db.Faces[idx].ClusterID = -1 // noise
		} else {
			db.Faces[idx].ClusterID = subClusterMap[sl]
		}
	}

	// Propagate label to the first sub-cluster (which kept the original ID).
	// New sub-clusters are unlabeled — user can label them after visual review.

	if err := db.Save(dbPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		os.Exit(1)
	}

	// Count results.
	newClusters := len(subClusterMap)
	noise := 0
	for _, sl := range subLabels {
		if sl == -1 {
			noise++
		}
	}

	fmt.Printf("Split into %d sub-clusters + %d noise faces\n", newClusters, noise)
	for sl, gid := range subClusterMap {
		count := 0
		for _, l := range subLabels {
			if l == sl {
				count++
			}
		}
		label := db.Labels[gid]
		if label == "" {
			label = "(unlabeled)"
		}
		fmt.Printf("  Cluster %d: %d faces — %s\n", gid, count, label)
	}

	fmt.Println("\nRun 'facescan extract' to update face thumbnails.")
}
