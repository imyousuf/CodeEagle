//go:build app

package app

import "context"

// GetStatus returns the current status of all backend resources.
func (a *App) GetStatus() AppStatus {
	status := AppStatus{
		ProjectName: a.cfg.Project.Name,
		Branch:      a.branch,
	}

	// Graph store stats.
	if a.graphStore != nil {
		stats, err := a.graphStore.Stats(context.Background())
		if err == nil {
			status.GraphReady = stats.NodeCount > 0
			status.NodeCount = int(stats.NodeCount)
			status.EdgeCount = int(stats.EdgeCount)
		}
	}

	// Vector store status.
	if a.vectorStore != nil && a.vectorStore.Available() {
		status.VectorReady = true
		status.VectorCount = a.vectorStore.Len()
		if meta := a.vectorStore.Meta(); meta != nil {
			status.EmbedProvider = meta.Provider + "/" + meta.Model
		}
	}

	// LLM status.
	if a.llmClient != nil {
		status.LLMReady = true
		status.LLMProvider = a.llmClient.Provider()
	}

	return status
}
