// Command backpop-paths migrates file-type nodes in the CodeEagle graph store
// to use basename-prefixed paths (e.g., ".Gloria/F95A0315.JPG" becomes
// "Pictures/.Gloria/F95A0315.JPG"). This one-time migration ensures unique
// file identity across multiple non-git directories.
//
// Usage:
//
//	go run ./cmd/backpop-paths <config-dir>
//
// config-dir is the directory containing .CodeEagle/config.yaml (e.g., /home/user).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// fileTypeNodes lists the node types that represent files on disk.
var fileTypeNodes = []graph.NodeType{
	graph.NodeFile,
	graph.NodeTestFile,
	graph.NodeDocument,
	graph.NodeDirectory,
	graph.NodeAIGuideline,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: backpop-paths <config-dir>")
		fmt.Println("  config-dir: directory containing .CodeEagle/config.yaml")
		os.Exit(1)
	}

	configDir := os.Args[1]
	if err := os.Chdir(configDir); err != nil {
		fmt.Printf("chdir %s: %v\n", configDir, err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("config load error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ConfigDir: %s\n", cfg.ConfigDir)

	// Determine directories and their basenames.
	rootBasenames := make(map[string]string) // root path → basename
	basenames := make(map[string]bool)       // known basenames for prefix check
	for _, repo := range cfg.Repositories {
		base := filepath.Base(repo.Path)
		rootBasenames[repo.Path] = base
		basenames[base] = true
		fmt.Printf("  Repo root: %s -> basename: %s\n", repo.Path, base)
	}

	paths := make([]string, len(cfg.Repositories))
	for i, r := range cfg.Repositories {
		paths[i] = r.Path
	}

	store, branch, err := embedded.OpenReadWrite(cfg, paths, "")
	if err != nil {
		fmt.Printf("open store error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	fmt.Printf("Branch: %s\n", branch)

	ctx := context.Background()

	// Capture pre-migration counts.
	preStats, err := store.Stats(ctx)
	if err != nil {
		fmt.Printf("stats error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nPre-migration: %d nodes, %d edges\n", preStats.NodeCount, preStats.EdgeCount)

	// ── Phase 1: Build oldID → newID mapping ──────────────────────────

	type migration struct {
		node    *graph.Node
		newPath string
		newID   string
	}

	idMap := make(map[string]string)      // oldID → newID
	newIDToOld := make(map[string]string) // newID → old FilePath (for collision detection)
	var migrations []migration
	alreadyPrefixed := 0
	notFound := 0
	collisions := 0

	for _, nodeType := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			fmt.Printf("query %s error: %v\n", nodeType, err)
			continue
		}

		for _, node := range nodes {
			if node.FilePath == "" {
				continue
			}

			// Check if already migrated (path starts with a known basename/).
			parts := strings.SplitN(node.FilePath, "/", 2)
			if len(parts) > 1 && basenames[parts[0]] {
				alreadyPrefixed++
				continue
			}

			// Determine which root owns this file.
			var matchedBase string
			for root, base := range rootBasenames {
				candidate := filepath.Join(root, node.FilePath)
				if _, err := os.Stat(candidate); err == nil {
					matchedBase = base
					break
				}
			}
			if matchedBase == "" {
				notFound++
				continue
			}

			newPath := matchedBase + "/" + node.FilePath
			// Use the same ID generation as the parser would for new files.
			// For sub-file nodes (sections, code blocks) that share FilePath
			// but differ in Name, NewNodeID produces distinct IDs since Name differs.
			// However, if Name also collides (e.g., multiple "code:bash" sections),
			// we must disambiguate by including the old ID.
			newID := graph.NewNodeID(string(node.Type), newPath, node.Name)

			// Check for newID collision.
			if prevOldID, exists := idMap[node.ID]; exists {
				fmt.Printf("  WARN: oldID %s already mapped to %s\n", node.ID, prevOldID)
			}
			if _, collision := newIDToOld[newID]; collision {
				// Multiple sub-file nodes (e.g., markdown sections named "code:bash")
				// collide because they share type+path+name. This is a pre-existing
				// parser quirk. The first wins; duplicates get cleaned up.
				collisions++
				continue
			}
			newIDToOld[newID] = node.FilePath
			idMap[node.ID] = newID
			migrations = append(migrations, migration{
				node:    node,
				newPath: newPath,
				newID:   newID,
			})
		}
	}

	// Collect old IDs of collided nodes for cleanup in Phase 4.
	collidedOldIDs := make(map[string]bool)
	for _, nodeType := range fileTypeNodes {
		nodes, err := store.QueryNodes(ctx, graph.NodeFilter{Type: nodeType})
		if err != nil {
			continue
		}
		for _, node := range nodes {
			if node.FilePath == "" {
				continue
			}
			parts := strings.SplitN(node.FilePath, "/", 2)
			if len(parts) > 1 && basenames[parts[0]] {
				continue // already prefixed
			}
			if _, inMap := idMap[node.ID]; !inMap {
				// This node wasn't added to migrations — it was a collision victim.
				collidedOldIDs[node.ID] = true
			}
		}
	}

	fmt.Printf("Nodes to migrate: %d\n", len(migrations))
	fmt.Printf("Already prefixed (skipped): %d\n", alreadyPrefixed)
	fmt.Printf("Not found on disk (skipped): %d\n", notFound)
	fmt.Printf("Collisions (dedup, will delete): %d\n", len(collidedOldIDs))

	if len(migrations) == 0 {
		fmt.Println("Nothing to migrate.")
		return
	}

	// ── Phase 2: Create new nodes ─────────────────────────────────────

	created := 0
	for _, m := range migrations {
		newNode := *m.node
		newNode.ID = m.newID
		newNode.FilePath = m.newPath
		if err := store.AddNode(ctx, &newNode); err != nil {
			fmt.Printf("  ERROR add node %s (%s): %v\n", m.newID, m.newPath, err)
			continue
		}
		created++
		if created%1000 == 0 {
			fmt.Printf("  ... created %d/%d new nodes\n", created, len(migrations))
		}
	}
	fmt.Printf("Created %d new nodes\n", created)

	// ── Phase 3: Migrate edges ────────────────────────────────────────
	// For each migrated node, get all edges and re-create with translated
	// endpoints. Use idMap to translate BOTH endpoints (handles edges between
	// two migrated nodes). Track processed edge IDs to avoid duplicates.

	processedEdges := make(map[string]bool)
	edgesCreated := 0
	edgesDeleted := 0

	for _, m := range migrations {
		edges, err := store.GetEdges(ctx, m.node.ID, "")
		if err != nil {
			continue
		}

		for _, edge := range edges {
			if processedEdges[edge.ID] {
				continue
			}
			processedEdges[edge.ID] = true

			// Translate endpoints using the full mapping.
			newSource := edge.SourceID
			if mapped, ok := idMap[newSource]; ok {
				newSource = mapped
			}
			newTarget := edge.TargetID
			if mapped, ok := idMap[newTarget]; ok {
				newTarget = mapped
			}

			// If nothing changed, skip (edge connects to non-migrated nodes only).
			if newSource == edge.SourceID && newTarget == edge.TargetID {
				continue
			}

			// Create new edge with translated endpoints and a new deterministic ID.
			newEdge := &graph.Edge{
				ID:         graph.NewNodeID(string(edge.Type), newSource, newTarget),
				Type:       edge.Type,
				SourceID:   newSource,
				TargetID:   newTarget,
				Properties: edge.Properties,
			}
			if err := store.AddEdge(ctx, newEdge); err != nil {
				// May already exist from a different path through the graph.
				continue
			}
			edgesCreated++

			// Delete old edge.
			if err := store.DeleteEdge(ctx, edge.ID); err != nil {
				continue
			}
			edgesDeleted++
		}
	}
	fmt.Printf("Edges: %d created, %d deleted\n", edgesCreated, edgesDeleted)

	// ── Phase 4: Delete old nodes ─────────────────────────────────────
	// DeleteNode cascades edges, but we've already cleaned up the migrated ones.
	// Any remaining old edges (if the delete above missed some) get cleaned too.

	deleted := 0
	for _, m := range migrations {
		if err := store.DeleteNode(ctx, m.node.ID); err != nil {
			// May already be gone if it was cleaned up.
			continue
		}
		deleted++
		if deleted%1000 == 0 {
			fmt.Printf("  ... deleted %d/%d old nodes\n", deleted, len(migrations))
		}
	}
	fmt.Printf("Deleted %d old nodes\n", deleted)

	// ── Phase 4b: Delete collided duplicate nodes ─────────────────────
	collidedDeleted := 0
	for oldID := range collidedOldIDs {
		if err := store.DeleteNode(ctx, oldID); err != nil {
			continue
		}
		collidedDeleted++
	}
	fmt.Printf("Deleted %d collided duplicate nodes\n", collidedDeleted)

	// ── Validation ────────────────────────────────────────────────────

	postStats, err := store.Stats(ctx)
	if err != nil {
		fmt.Printf("post-stats error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nPost-migration: %d nodes, %d edges\n", postStats.NodeCount, postStats.EdgeCount)

	// Node count should decrease by exactly the number of collisions (deduped).
	expectedNodes := preStats.NodeCount - int64(collidedDeleted)
	nodeDiff := postStats.NodeCount - expectedNodes
	if nodeDiff != 0 {
		fmt.Printf("WARNING: unexpected node count! expected %d, got %d (diff: %+d)\n",
			expectedNodes, postStats.NodeCount, nodeDiff)
		os.Exit(1)
	}
	fmt.Printf("Validation passed: %d nodes (%d deduped collisions removed).\n",
		postStats.NodeCount, collidedDeleted)
}
