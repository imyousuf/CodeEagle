// Command dbcheck inspects a CodeEagle BadgerDB graph database.
// It lists branches, node/edge counts, type distributions, and text length
// statistics useful for planning vector embedding strategies.
//
// Usage:
//
//	go run ./cmd/dbcheck <path-to-graph.db>
package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/imyousuf/CodeEagle/internal/graph"
	embedded "github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: dbcheck <path-to-graph.db>\n")
		os.Exit(1)
	}
	dbPath := os.Args[1]

	store, err := embedded.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	branches, err := store.ListBranches()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list branches: %v\n", err)
		_ = store.Close()
		os.Exit(1)
	}
	_ = store.Close()

	if len(branches) == 0 {
		fmt.Println("No branches found in DB.")
		return
	}
	fmt.Printf("Branches in DB: %v\n\n", branches)

	ctx := context.Background()
	for _, branch := range branches {
		if err := inspectBranch(ctx, dbPath, branch); err != nil {
			fmt.Fprintf(os.Stderr, "branch %s: %v\n", branch, err)
		}
	}
}

func inspectBranch(ctx context.Context, dbPath, branch string) error {
	bs, err := embedded.NewBranchStore(dbPath, branch, []string{branch})
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = bs.Close() }()

	stats, err := bs.Stats(ctx)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}

	nodes, err := bs.QueryNodes(ctx, graph.NodeFilter{})
	if err != nil {
		return fmt.Errorf("query nodes: %w", err)
	}

	fmt.Printf("Branch: %s\n", branch)
	fmt.Printf("  Total Nodes: %d\n", stats.NodeCount)
	fmt.Printf("  Total Edges: %d\n", stats.EdgeCount)

	printNodeTypes(stats)
	printEdgeTypes(stats)
	printTextStats(nodes)
	printDocumentAnalysis(nodes)

	fmt.Println()
	return nil
}

func printNodeTypes(stats *graph.GraphStats) {
	fmt.Println("\n  Node types:")
	for t, c := range stats.NodesByType {
		fmt.Printf("    %-25s %d\n", t, c)
	}
}

func printEdgeTypes(stats *graph.GraphStats) {
	fmt.Println("\n  Edge types:")
	for t, c := range stats.EdgesByType {
		fmt.Printf("    %-25s %d\n", t, c)
	}
}

func printTextStats(nodes []*graph.Node) {
	// Per-type text length stats for embeddable types.
	typeLens := make(map[graph.NodeType][]int)
	totalWithDoc := 0
	totalDocLen := 0

	for _, n := range nodes {
		if n.DocComment == "" {
			continue
		}
		totalWithDoc++
		totalDocLen += len(n.DocComment)
		typeLens[n.Type] = append(typeLens[n.Type], len(n.DocComment))
	}

	fmt.Printf("\n  Nodes with DocComment: %d\n", totalWithDoc)
	if totalWithDoc > 0 {
		fmt.Printf("  Avg DocComment length: %d chars\n", totalDocLen/totalWithDoc)
	}

	embeddableTypes := []graph.NodeType{
		graph.NodeFunction, graph.NodeMethod, graph.NodeClass,
		graph.NodeDocument, graph.NodeInterface, graph.NodeStruct,
		graph.NodeAPIEndpoint, graph.NodeService, graph.NodeDBModel,
		graph.NodeAIGuideline,
	}

	fmt.Println("\n  Text length by embeddable node type:")
	for _, t := range embeddableTypes {
		lens := typeLens[t]
		if len(lens) == 0 {
			continue
		}
		sort.Ints(lens)
		total := 0
		for _, l := range lens {
			total += l
		}
		avg := total / len(lens)
		p50 := lens[len(lens)/2]
		p90 := lens[int(float64(len(lens))*0.9)]
		max := lens[len(lens)-1]
		fmt.Printf("    %-20s count=%-6d avg=%-5d p50=%-5d p90=%-5d max=%-6d total=%.1fMB\n",
			t, len(lens), avg, p50, p90, max, float64(total)/1024/1024)
	}
}

func printDocumentAnalysis(nodes []*graph.Node) {
	type docInfo struct {
		name    string
		textLen int
		kind    string
	}

	var docs []docInfo
	kindCounts := make(map[string]int)
	kindLens := make(map[string][]int)

	for _, n := range nodes {
		if n.Type != graph.NodeDocument || n.DocComment == "" {
			continue
		}
		kind := n.Properties["kind"]
		if kind == "" {
			kind = "(parsed)"
		}
		docs = append(docs, docInfo{name: n.Name, textLen: len(n.DocComment), kind: kind})
		kindCounts[kind]++
		kindLens[kind] = append(kindLens[kind], len(n.DocComment))
	}

	if len(docs) == 0 {
		return
	}

	fmt.Printf("\n  === Document Nodes with Text: %d ===\n", len(docs))
	fmt.Println("  By kind:")
	for kind, count := range kindCounts {
		lens := kindLens[kind]
		sort.Ints(lens)
		total := 0
		for _, l := range lens {
			total += l
		}
		avg := total / len(lens)
		p50 := lens[len(lens)/2]
		p90 := lens[int(float64(len(lens))*0.9)]
		max := lens[len(lens)-1]
		fmt.Printf("    %-25s count=%-6d avg=%-6d p50=%-6d p90=%-6d max=%-6d total=%.2fMB\n",
			kind, count, avg, p50, p90, max, float64(total)/1024/1024)
	}

	// Size distribution.
	thresholds := []int{256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	labels := []string{"< 256", "256-512", "512-1K", "1K-2K", "2K-4K", "4K-8K", "8K-16K", "16K-32K", "32K-64K", "64K+"}
	buckets := make([]int, len(labels))
	for _, d := range docs {
		placed := false
		for i, t := range thresholds {
			if d.textLen <= t {
				buckets[i]++
				placed = true
				break
			}
		}
		if !placed {
			buckets[len(labels)-1]++
		}
	}
	fmt.Println("\n  Document size distribution:")
	for i, label := range labels {
		if buckets[i] > 0 {
			fmt.Printf("    %-15s %d\n", label, buckets[i])
		}
	}

	// Top 20 largest.
	sort.Slice(docs, func(i, j int) bool { return docs[i].textLen > docs[j].textLen })
	fmt.Println("\n  Top 20 largest Document nodes:")
	limit := min(20, len(docs))
	for i := range limit {
		d := docs[i]
		name := d.name
		if len(name) > 60 {
			name = name[:57] + "..."
		}
		fmt.Printf("    %6d chars  %-25s  %s\n", d.textLen, d.kind, name)
	}
}
