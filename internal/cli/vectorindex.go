package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/imyousuf/CodeEagle/internal/config"

	// Register embedding providers so their init() functions run.
	_ "github.com/imyousuf/CodeEagle/internal/embedding"
)

func newVectorIndexCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "vectorindex",
		Short: "Build vector index from existing graph",
		Long: `Build or rebuild the vector search index from an existing knowledge graph.

This command does not re-parse or re-sync source files. It reads all embeddable
nodes from the graph database and generates vector embeddings using the configured
(or auto-detected) embedding provider.

Requires either Ollama (with nomic-embed-text-v2-moe) or Vertex AI (with
gemini-embedding-001) to be available.

Use --force to rebuild even if an index already exists.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.ConfigDir == "" {
				return fmt.Errorf("no config directory found; run 'codeeagle init' first")
			}

			out := cmd.OutOrStdout()
			logFn := func(format string, args ...any) {
				fmt.Fprintf(out, format+"\n", args...)
			}

			// Open graph store.
			store, currentBranch, err := openBranchStore(cfg)
			if err != nil {
				return err
			}
			defer store.Close()

			// Open vector store.
			vs, err := openVectorStore(cfg, store, currentBranch, logFn)
			if err != nil {
				return err
			}
			if vs == nil {
				return fmt.Errorf("no embedding provider available; install Ollama with nomic-embed-text-v2-moe or configure Vertex AI")
			}
			defer vs.Close()

			// Try to load existing index.
			loaded, err := vs.Load()
			if err != nil {
				logFn("Warning: failed to load existing index: %v", err)
				loaded = false
			}

			if loaded && !force && !vs.NeedsReindex() {
				meta := vs.Meta()
				if meta != nil {
					fmt.Fprintf(out, "Vector index already up to date (%s/%s, %d vectors)\n",
						meta.Provider, meta.Model, meta.NodeCount)
				}
				return nil
			}

			if loaded && vs.NeedsReindex() {
				logFn("Embedding provider/model changed, rebuilding...")
			}

			logFn("Building vector index from graph (branch: %s)...", currentBranch)

			if err := vs.Rebuild(context.Background()); err != nil {
				return fmt.Errorf("rebuild vector index: %w", err)
			}

			if err := vs.Save(); err != nil {
				return fmt.Errorf("save vector index: %w", err)
			}

			meta := vs.Meta()
			fmt.Fprintf(out, "Vector index built: %d vectors (%s/%s, %d-dim)\n",
				vs.Len(), meta.Provider, meta.Model, meta.Dimensions)

			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "force full rebuild even if index exists")

	return cmd
}
