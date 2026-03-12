package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

type duplicateEntry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     graph.NodeType `json:"type"`
	FilePath string         `json:"file_path"`
}

type duplicateGroup struct {
	ContentHash string           `json:"content_hash"`
	MimeType    string           `json:"mime_type,omitempty"`
	Count       int              `json:"count"`
	Files       []duplicateEntry `json:"files"`
}

func newQueryDuplicatesCmd() *cobra.Command {
	var (
		nodeType string
		minCount int
		jsonOut  bool
	)

	cmd := &cobra.Command{
		Use:   "duplicates",
		Short: "Find duplicate files by content hash",
		Long: `Identify files with identical content across different paths.
Files are grouped by content hash and MIME type. Groups with fewer than
--min-count files are excluded (default 2).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, _, err := openReadOnlyBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			ctx := context.Background()

			fileTypes := []graph.NodeType{
				graph.NodeFile,
				graph.NodeTestFile,
				graph.NodeDocument,
				graph.NodeAIGuideline,
			}

			// Optionally filter to a single type.
			if nodeType != "" {
				fileTypes = []graph.NodeType{graph.NodeType(nodeType)}
			}

			// Group nodes by (content_hash, mime_type).
			type groupKey struct {
				hash     string
				mimeType string
			}
			groups := make(map[groupKey][]duplicateEntry)

			for _, nt := range fileTypes {
				nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nt})
				if err != nil {
					continue
				}
				for _, n := range nodes {
					if n.Properties == nil {
						continue
					}
					h := n.Properties[graph.PropContentHash]
					if h == "" {
						continue
					}
					key := groupKey{
						hash:     h,
						mimeType: n.Properties[graph.PropMimeType],
					}
					groups[key] = append(groups[key], duplicateEntry{
						ID:       n.ID,
						Name:     n.Name,
						Type:     n.Type,
						FilePath: n.FilePath,
					})
				}
			}

			// Filter to groups with at least minCount members.
			var result []duplicateGroup
			for key, entries := range groups {
				if len(entries) < minCount {
					continue
				}
				sort.Slice(entries, func(i, j int) bool {
					return entries[i].FilePath < entries[j].FilePath
				})
				result = append(result, duplicateGroup{
					ContentHash: key.hash,
					MimeType:    key.mimeType,
					Count:       len(entries),
					Files:       entries,
				})
			}

			// Sort by count descending, then by hash.
			sort.Slice(result, func(i, j int) bool {
				if result[i].Count != result[j].Count {
					return result[i].Count > result[j].Count
				}
				return result[i].ContentHash < result[j].ContentHash
			})

			out := cmd.OutOrStdout()

			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			if len(result) == 0 {
				fmt.Fprintln(out, "No duplicate files found.")
				return nil
			}

			totalDuplicates := 0
			for _, g := range result {
				totalDuplicates += g.Count - 1
				fmt.Fprintf(out, "\n[%d copies] %s (%s)\n", g.Count, g.ContentHash[:min(20, len(g.ContentHash))], g.MimeType)
				for _, f := range g.Files {
					fmt.Fprintf(out, "  %-14s  %s\n", f.Type, f.FilePath)
				}
			}

			fmt.Fprintf(out, "\n%d duplicate groups, %d duplicate files\n", len(result), totalDuplicates)

			return nil
		},
	}

	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type (File, Document, TestFile, AIGuideline)")
	cmd.Flags().IntVar(&minCount, "min-count", 2, "minimum number of copies to report")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")

	return cmd
}
