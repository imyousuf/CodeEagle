//go:build app

package app

import "context"

// GetStatus returns the current status of all backend resources.
// Opens each resource, reads its status, and closes it. Never fails —
// reports whatever is available.
func (a *App) GetStatus() AppStatus {
	status := AppStatus{
		ProjectName: a.cfg.Project.Name,
		Branch:      a.branch,
	}

	// Try graph.
	gr, closeGraph, err := a.openGraph()
	if err == nil && gr != nil {
		defer closeGraph()
		stats, err := gr.store.Stats(context.Background())
		if err == nil {
			status.GraphReady = stats.NodeCount > 0
			status.NodeCount = int(stats.NodeCount)
			status.EdgeCount = int(stats.EdgeCount)
		}
		status.Branch = gr.branch

		// Try vector (requires graph).
		vs, closeVector := a.openVector(gr.store, gr.branch)
		if vs != nil {
			defer closeVector()
			if vs.Available() {
				status.VectorReady = true
				status.VectorCount = vs.Len()
				if meta := vs.Meta(); meta != nil {
					status.EmbedProvider = meta.Provider + "/" + meta.Model
				}
			}
		}
	}

	// Try LLM.
	client, closeLLM, err := a.openLLM()
	if err == nil && client != nil {
		defer closeLLM()
		status.LLMReady = true
		status.LLMProvider = client.Provider()
	}

	return status
}
