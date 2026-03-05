//go:build faces

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/faces"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

func init() {
	registerFacesCmd = func(rootCmd *cobra.Command) {
		rootCmd.AddCommand(newFacesCmd())
	}
}

func newFacesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "faces",
		Short: "Face detection, recognition, and clustering",
	}

	cmd.AddCommand(newFacesScanCmd())
	cmd.AddCommand(newFacesClustersCmd())
	cmd.AddCommand(newFacesLabelCmd())
	cmd.AddCommand(newFacesSearchCmd())
	cmd.AddCommand(newFacesMergeCmd())
	cmd.AddCommand(newFacesSplitCmd())
	cmd.AddCommand(newFacesUnlabeledCmd())
	cmd.AddCommand(newFacesSuggestCmd())

	return cmd
}

func openFaceStore(cfg *config.Config) (*faces.Store, error) {
	dbPath := cfg.ConfigDir + "/face.db"
	return faces.OpenStore(dbPath)
}

func modelDir(cfg *config.Config) string {
	dir := cfg.Docs.Faces.ModelDir
	if dir == "" {
		dir = cfg.ConfigDir + "/models"
	}
	return dir
}

func newFacesScanCmd() *cobra.Command {
	var force bool
	var threshold float32
	var sim float32
	var minFace int
	var maxRes int

	cmd := &cobra.Command{
		Use:   "scan [dirs...]",
		Short: "Detect faces in images and cluster them",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			out := cmd.OutOrStdout()
			logFn := func(format string, a ...any) {
				fmt.Fprintf(out, format+"\n", a...)
			}

			// Determine scan directories.
			dirs := args
			if len(dirs) == 0 {
				for _, repo := range cfg.Repositories {
					dirs = append(dirs, repo.Path)
				}
			}
			if len(dirs) == 0 {
				return fmt.Errorf("no directories specified and no repositories configured")
			}

			// Apply config defaults.
			if threshold == 0 {
				threshold = float32(cfg.Docs.Faces.ConfidenceThreshold)
				if threshold == 0 {
					threshold = 0.5
				}
			}
			if sim == 0 {
				sim = float32(cfg.Docs.Faces.SimilarityThreshold)
				if sim == 0 {
					sim = 0.30
				}
			}

			// Ensure models.
			mdir := modelDir(cfg)
			logFn("Ensuring face detection models...")
			if err := faces.EnsureModels(mdir, logFn); err != nil {
				return fmt.Errorf("ensure models: %w", err)
			}

			// Create detector.
			detector, err := faces.NewDetector(mdir, threshold, minFace, maxRes)
			if err != nil {
				return fmt.Errorf("create detector: %w", err)
			}
			defer detector.Close()

			// Open face store.
			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			// Walk directories and detect faces.
			totalFaces := 0
			totalImages := 0
			for _, dir := range dirs {
				err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil
					}
					if info.IsDir() || !faces.IsImageFile(path) {
						return nil
					}

					// Skip already scanned unless force.
					if !force && store.IsScanned(path) {
						return nil
					}

					logFn("Scanning %s...", path)
					result, err := detector.Detect(path)
					if err != nil {
						logFn("  Warning: %v", err)
						return nil
					}

					// Store faces.
					if force {
						_ = store.DeleteFacesForImage(path)
					}
					for i := range result.Faces {
						if err := store.StoreFace(&result.Faces[i]); err != nil {
							logFn("  Warning: store face: %v", err)
						}
					}
					_ = store.MarkScanned(path)

					totalImages++
					totalFaces += len(result.Faces)
					logFn("  Found %d faces", len(result.Faces))
					return nil
				})
				if err != nil {
					logFn("Warning: walk %s: %v", dir, err)
				}
			}

			logFn("\nDetection complete: %d faces in %d images", totalFaces, totalImages)

			// Run clustering.
			allFaces, err := store.AllFaces()
			if err != nil {
				return fmt.Errorf("load all faces: %w", err)
			}

			if len(allFaces) < 2 {
				logFn("Too few faces for clustering (%d)", len(allFaces))
				return nil
			}

			logFn("Clustering %d faces (threshold: %.3f)...", len(allFaces), sim)

			embeddings := make([][]float32, len(allFaces))
			imgPaths := make([]string, len(allFaces))
			for i, f := range allFaces {
				embeddings[i] = f.Embedding
				imgPaths[i] = f.ImagePath
			}

			labels := faces.AgglomerativeClustering(embeddings, imgPaths, sim, 2)
			absorbThr := sim * 0.75
			labels = faces.AbsorbNoise(embeddings, imgPaths, labels, absorbThr)

			for i, f := range allFaces {
				if err := store.UpdateCluster(f.ImagePath, f.FaceIdx, labels[i]); err != nil {
					logFn("Warning: update cluster: %v", err)
				}
			}

			// Count clusters.
			clusterCounts := make(map[int]int)
			for _, l := range labels {
				clusterCounts[l]++
			}
			noise := clusterCounts[-1]
			delete(clusterCounts, -1)
			logFn("Clustering complete: %d clusters, %d noise faces", len(clusterCounts), noise)

			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "re-scan all images")
	cmd.Flags().Float32Var(&threshold, "threshold", 0, "face detection confidence threshold")
	cmd.Flags().Float32Var(&sim, "sim", 0, "clustering similarity threshold")
	cmd.Flags().IntVar(&minFace, "min-face", 30, "minimum face size in pixels")
	cmd.Flags().IntVar(&maxRes, "max-res", 1600, "max image resolution for detection")

	return cmd
}

func newFacesClustersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "List face clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			allFaces, err := store.AllFaces()
			if err != nil {
				return fmt.Errorf("load faces: %w", err)
			}

			labels, _ := store.AllLabels()

			// Group by cluster.
			clusters := make(map[int][]faces.FaceRecord)
			for _, f := range allFaces {
				clusters[f.ClusterID] = append(clusters[f.ClusterID], f)
			}

			// Sort cluster IDs.
			ids := make([]int, 0, len(clusters))
			for id := range clusters {
				ids = append(ids, id)
			}
			sort.Ints(ids)

			for _, id := range ids {
				members := clusters[id]
				label := labels[id]
				name := "(unlabeled)"
				if label != "" {
					name = label
				}
				if id == -1 {
					name = "(noise)"
				}

				// Unique images.
				imgSet := make(map[string]bool)
				for _, f := range members {
					imgSet[f.ImagePath] = true
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Cluster %d: %d faces in %d images — %s\n",
					id, len(members), len(imgSet), name)
			}

			return nil
		},
	}
}

func newFacesLabelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "label <cluster-id> <name>",
		Short: "Label a cluster with a person name",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid cluster ID: %s", args[0])
			}
			name := args[1]

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			if err := store.SetLabel(clusterID, name); err != nil {
				return fmt.Errorf("set label: %w", err)
			}

			// Create NodePerson in graph if available.
			graphStore, _, gsErr := openReadOnlyBranchStore(cfg)
			if gsErr == nil {
				defer graphStore.Close()
				personID := graph.NewNodeID(string(graph.NodePerson), "", name)
				personNode := &graph.Node{
					ID:            personID,
					Type:          graph.NodePerson,
					Name:          name,
					QualifiedName: name,
				}
				_ = graphStore.AddNode(cmd.Context(), personNode)

				// Create EdgeAppearsIn for all images in this cluster.
				allFaces, _ := store.AllFaces()
				imgSet := make(map[string]bool)
				for _, f := range allFaces {
					if f.ClusterID == clusterID {
						imgSet[f.ImagePath] = true
					}
				}
				for imgPath := range imgSet {
					fileName := filepath.Base(imgPath)
					docNodeID := graph.NewNodeID(string(graph.NodeDocument), imgPath, fileName)
					edgeID := graph.NewNodeID("edge", personID, docNodeID+":AppearsIn")
					_ = graphStore.AddEdge(cmd.Context(), &graph.Edge{
						ID:       edgeID,
						Type:     graph.EdgeAppearsIn,
						SourceID: personID,
						TargetID: docNodeID,
					})
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Labeled cluster %d as %q\n", clusterID, name)
			return nil
		},
	}
}

func newFacesSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <name>",
		Short: "Find images containing a named person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.ToLower(args[0])

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			labels, _ := store.AllLabels()
			allFaces, _ := store.AllFaces()

			// Find matching clusters.
			var matchIDs []int
			for id, label := range labels {
				if strings.Contains(strings.ToLower(label), name) {
					matchIDs = append(matchIDs, id)
				}
			}

			if len(matchIDs) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No person found matching %q\n", args[0])
				return nil
			}

			matchSet := make(map[int]bool)
			for _, id := range matchIDs {
				matchSet[id] = true
			}

			imgSet := make(map[string]bool)
			for _, f := range allFaces {
				if matchSet[f.ClusterID] {
					imgSet[f.ImagePath] = true
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Found %d images containing %q:\n", len(imgSet), args[0])
			for img := range imgSet {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", img)
			}

			return nil
		},
	}
}

func newFacesMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge <id1> <id2> [id3...]",
		Short: "Merge clusters into the first cluster ID",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var ids []int
			for _, a := range args {
				id, err := strconv.Atoi(a)
				if err != nil {
					return fmt.Errorf("invalid cluster ID: %s", a)
				}
				ids = append(ids, id)
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			moved, err := faces.Merge(store, ids[0], ids[1:])
			if err != nil {
				return fmt.Errorf("merge: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Merged clusters %v into cluster %d (%d faces moved)\n",
				ids[1:], ids[0], moved)
			return nil
		},
	}
}

func newFacesSplitCmd() *cobra.Command {
	var sim float32

	cmd := &cobra.Command{
		Use:   "split <cluster-id>",
		Short: "Re-cluster a single cluster at a tighter threshold",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid cluster ID: %s", args[0])
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			result, err := faces.Split(store, clusterID, sim)
			if err != nil {
				return fmt.Errorf("split: %w", err)
			}

			for id, count := range result {
				if id == -1 {
					fmt.Fprintf(cmd.OutOrStdout(), "  Noise: %d faces\n", count)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  Cluster %d: %d faces\n", id, count)
				}
			}

			return nil
		},
	}

	cmd.Flags().Float32Var(&sim, "sim", 0.75, "similarity threshold for split")
	return cmd
}

func newFacesUnlabeledCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlabeled",
		Short: "List clusters without labels",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			allFaces, _ := store.AllFaces()
			labels, _ := store.AllLabels()

			clusterCounts := make(map[int]int)
			for _, f := range allFaces {
				if f.ClusterID > 0 {
					clusterCounts[f.ClusterID]++
				}
			}

			ids := make([]int, 0)
			for id := range clusterCounts {
				if labels[id] == "" {
					ids = append(ids, id)
				}
			}
			sort.Ints(ids)

			if len(ids) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "All clusters are labeled.")
				return nil
			}

			for _, id := range ids {
				fmt.Fprintf(cmd.OutOrStdout(), "Cluster %d: %d faces (unlabeled)\n", id, clusterCounts[id])
			}

			return nil
		},
	}
}

func newFacesSuggestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suggest",
		Short: "Suggest cluster merges based on similarity",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, err := openFaceStore(cfg)
			if err != nil {
				return fmt.Errorf("open face store: %w", err)
			}
			defer store.Close()

			allFaces, _ := store.AllFaces()
			labels, _ := store.AllLabels()

			// Group by cluster.
			clusters := make(map[int][]int) // cluster ID → face indices
			for i, f := range allFaces {
				if f.ClusterID > 0 {
					clusters[f.ClusterID] = append(clusters[f.ClusterID], i)
				}
			}

			ids := make([]int, 0, len(clusters))
			for id := range clusters {
				ids = append(ids, id)
			}
			sort.Ints(ids)

			type suggestion struct {
				a, b   int
				avgSim float32
				maxSim float32
			}
			var suggestions []suggestion

			for i := 0; i < len(ids); i++ {
				for j := i + 1; j < len(ids); j++ {
					a, b := ids[i], ids[j]
					membersA := clusters[a]
					membersB := clusters[b]

					// Check same-photo constraint.
					imgPaths := make([]string, len(allFaces))
					for k, f := range allFaces {
						imgPaths[k] = f.ImagePath
					}
					if faces.HasImageOverlap(membersA, membersB, imgPaths) {
						continue
					}

					var sumSim, maxS float32
					nPairs := 0
					for _, ai := range membersA {
						for _, bi := range membersB {
							s := faces.CosineSimilarity(allFaces[ai].Embedding, allFaces[bi].Embedding)
							sumSim += s
							if s > maxS {
								maxS = s
							}
							nPairs++
						}
					}

					if nPairs > 0 {
						avgSim := sumSim / float32(nPairs)
						if avgSim > 0.2 {
							suggestions = append(suggestions, suggestion{a, b, avgSim, maxS})
						}
					}
				}
			}

			sort.Slice(suggestions, func(i, j int) bool {
				return suggestions[i].avgSim > suggestions[j].avgSim
			})

			if len(suggestions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No merge suggestions.")
				return nil
			}

			for _, s := range suggestions {
				labelA := labels[s.a]
				if labelA == "" {
					labelA = "(unlabeled)"
				}
				labelB := labels[s.b]
				if labelB == "" {
					labelB = "(unlabeled)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Cluster %d (%s) + Cluster %d (%s): avg=%.3f max=%.3f\n",
					s.a, labelA, s.b, labelB, s.avgSim, s.maxSim)
			}

			return nil
		},
	}
}
