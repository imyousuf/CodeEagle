package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func main() {
	// Change to home dir so config.Load finds the home CodeEagle config.
	if len(os.Args) > 1 {
		if err := os.Chdir(os.Args[1]); err != nil {
			fmt.Printf("chdir error: %v\n", err)
			return
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("config load error: %v\n", err)
		return
	}
	fmt.Printf("ConfigDir: %s\n", cfg.ConfigDir)

	paths := make([]string, len(cfg.Repositories))
	for i, r := range cfg.Repositories {
		paths[i] = r.Path
	}

	store, branch, err := embedded.OpenReadOnly(cfg, paths, "")
	if err != nil {
		fmt.Printf("open store error: %v\n", err)
		return
	}
	defer store.Close()
	fmt.Printf("Branch: %s\n", branch)

	ctx := context.Background()
	fileTypeNodes := []graph.NodeType{
		graph.NodeFile, graph.NodeTestFile, graph.NodeDocument,
		graph.NodeDirectory, graph.NodeAIGuideline,
	}

	var maxTime time.Time
	totalNodes := 0
	zeroCount := 0
	for _, nt := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nt})
		if err != nil {
			fmt.Printf("query %s error: %v\n", nt, err)
			continue
		}
		for _, n := range nodes {
			totalNodes++
			if n.UpdatedAt.IsZero() {
				zeroCount++
			} else if n.UpdatedAt.After(maxTime) {
				maxTime = n.UpdatedAt
			}
		}
		fmt.Printf("  %s: %d nodes\n", nt, len(nodes))
	}
	fmt.Printf("Total file-type nodes: %d\n", totalNodes)
	fmt.Printf("Nodes with zero UpdatedAt: %d\n", zeroCount)
	if !maxTime.IsZero() {
		fmt.Printf("Max UpdatedAt (watermark): %s\n", maxTime.Format(time.RFC3339))
	} else {
		fmt.Printf("Max UpdatedAt: ZERO (no timestamps!)\n")
	}

	// Check a specific Pictures file to see if it has UpdatedAt.
	samplePaths := []string{
		".Gloria/F95A0315.JPG",
		"167_Robbinsville_Allentown_LotDimensions.png",
		"Screenshots/Screenshot_20200101.png",
	}
	fmt.Println("\n--- Sample node lookups ---")
	for _, sp := range samplePaths {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: sp})
		if err != nil {
			fmt.Printf("  %s: query error: %v\n", sp, err)
			continue
		}
		if len(nodes) == 0 {
			fmt.Printf("  %s: NOT FOUND\n", sp)
		} else {
			for _, n := range nodes {
				fmt.Printf("  %s: type=%s UpdatedAt=%s\n", sp, n.Type, n.UpdatedAt.Format(time.RFC3339))
			}
		}
	}
}
