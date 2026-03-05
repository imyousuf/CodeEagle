package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/embedding"
	"github.com/imyousuf/CodeEagle/internal/gitutil"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show indexing status and graph stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, currentBranch, err := openReadOnlyBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			stats, err := store.Stats(context.Background())
			if err != nil {
				return fmt.Errorf("get stats: %w", err)
			}

			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Knowledge Graph Status\n")
			fmt.Fprintf(out, "======================\n\n")
			fmt.Fprintf(out, "  Active branch: %s\n", currentBranch)
			fmt.Fprintf(out, "  Total nodes:   %d\n", stats.NodeCount)
			fmt.Fprintf(out, "  Total edges:   %d\n\n", stats.EdgeCount)

			if len(stats.NodesByType) > 0 {
				fmt.Fprintf(out, "  Nodes by type:\n")
				nodeTypes := sortedNodeTypes(stats.NodesByType)
				for _, nt := range nodeTypes {
					fmt.Fprintf(out, "    %-20s %d\n", nt, stats.NodesByType[nt])
				}
				fmt.Fprintln(out)
			}

			if len(stats.EdgesByType) > 0 {
				fmt.Fprintf(out, "  Edges by type:\n")
				edgeTypes := sortedEdgeTypes(stats.EdgesByType)
				for _, et := range edgeTypes {
					fmt.Fprintf(out, "    %-20s %d\n", et, stats.EdgesByType[et])
				}
				fmt.Fprintln(out)
			}

			// Show vector search status.
			showVectorStatus(cfg, currentBranch, out)

			// Show git branch info for configured repositories.
			if len(cfg.Repositories) > 0 {
				fmt.Fprintf(out, "  Git Status:\n")
				for _, repo := range cfg.Repositories {
					info, err := gitutil.GetBranchInfo(repo.Path)
					if err != nil {
						continue
					}
					fmt.Fprintf(out, "    %s:\n", repo.Path)
					fmt.Fprintf(out, "      Branch: %s", info.CurrentBranch)
					if info.IsFeatureBranch {
						fmt.Fprintf(out, " (feature branch, %d ahead, %d behind %s)", info.Ahead, info.Behind, info.DefaultBranch)
					}
					fmt.Fprintln(out)

					if info.IsFeatureBranch {
						diff, err := gitutil.GetBranchDiff(repo.Path)
						if err == nil && len(diff.ChangedFiles) > 0 {
							fmt.Fprintf(out, "      Changed files: %d\n", len(diff.ChangedFiles))
							for _, cf := range diff.ChangedFiles {
								fmt.Fprintf(out, "        [%s] %s (+%d/-%d)\n", cf.Status, cf.Path, cf.Additions, cf.Deletions)
							}
						}
					}
				}
				fmt.Fprintln(out)
			}

			return nil
		},
	}

	return cmd
}

func showVectorStatus(cfg *config.Config, branch string, out io.Writer) {
	if cfg.ConfigDir == "" {
		return
	}

	idxPath := vectorIndexPath(cfg)
	dbPath := vectorDBPath(cfg)

	// Check if embedding provider is available.
	embedder, _ := embedding.DetectProvider(cfg)

	// Check if index file exists.
	info, err := os.Stat(idxPath)
	if err != nil {
		if embedder != nil {
			fmt.Fprintf(out, "  Vector Search: available (%s/%s) - run 'codeeagle vectorindex' to build\n\n",
				embedder.Name(), embedder.ModelName())
		} else {
			fmt.Fprintf(out, "  Vector Search: disabled (no embedding provider)\n\n")
		}
		return
	}

	// Index exists — try to read metadata.
	vs, vsErr := vectorstore.New(nil, nil, branch, idxPath, dbPath)
	if vsErr != nil {
		fmt.Fprintf(out, "  Vector Search: index exists but cannot open (%v)\n\n", vsErr)
		return
	}
	defer vs.Close()

	meta, _ := vs.LoadMetaOnly()

	if meta != nil {
		fmt.Fprintf(out, "  Vector Search: enabled (%s/%s, %d-dim)\n", meta.Provider, meta.Model, meta.Dimensions)
		fmt.Fprintf(out, "    Indexed nodes:  %d\n", meta.NodeCount)
		fmt.Fprintf(out, "    Index file:     %s (%.1fKB)\n", idxPath, float64(info.Size())/1024)
		fmt.Fprintf(out, "    Last updated:   %s\n", meta.UpdatedAt.Format("2006-01-02 15:04:05"))

		// Check if current provider matches.
		if embedder != nil && (meta.Provider != embedder.Name() || meta.Model != embedder.ModelName()) {
			fmt.Fprintf(out, "    WARNING: index built with %s/%s but current provider is %s/%s\n",
				meta.Provider, meta.Model, embedder.Name(), embedder.ModelName())
		} else if embedder == nil {
			fmt.Fprintf(out, "    WARNING: %s/%s no longer available, vector search disabled\n",
				meta.Provider, meta.Model)
		}
	} else {
		fmt.Fprintf(out, "  Vector Search: index exists (%s, %.1fKB)\n", idxPath, float64(info.Size())/1024)
	}
	fmt.Fprintln(out)
}

func sortedNodeTypes(m map[graph.NodeType]int64) []graph.NodeType {
	keys := make([]graph.NodeType, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedEdgeTypes(m map[graph.EdgeType]int64) []graph.EdgeType {
	keys := make([]graph.EdgeType, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
