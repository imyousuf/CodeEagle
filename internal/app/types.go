//go:build app

package app

import "github.com/imyousuf/CodeEagle/internal/graph"

// SearchFilters holds optional filters for the Search method.
type SearchFilters struct {
	NodeType string  `json:"node_type"` // comma-separated node types
	Package  string  `json:"package"`
	Language string  `json:"language"`
	NoDocs   bool    `json:"no_docs"`
	MinScore float64 `json:"min_score"`
	Limit    int     `json:"limit"`
}

// CodeResult represents a code entity search result.
type CodeResult struct {
	Name      string         `json:"name"`
	Type      graph.NodeType `json:"type"`
	FilePath  string         `json:"file_path"`
	Line      int            `json:"line"`
	Package   string         `json:"package"`
	Language  string         `json:"language"`
	Signature string         `json:"signature"`
	Snippet   string         `json:"snippet"`
	Relevance int            `json:"relevance"` // 0-100
	Score     float64        `json:"score"`
}

// DocResult represents a documentation search result.
type DocResult struct {
	Name      string         `json:"name"`
	Type      graph.NodeType `json:"type"`
	FilePath  string         `json:"file_path"`
	Snippet   string         `json:"snippet"`
	Relevance int            `json:"relevance"` // 0-100
	Score     float64        `json:"score"`
}

// SearchResults holds the split results from a search query.
type SearchResults struct {
	Code     []CodeResult `json:"code"`
	Docs     []DocResult  `json:"docs"`
	Query    string       `json:"query"`
	Total    int          `json:"total"`
	Provider string       `json:"provider"` // embedding provider info
}

// AgentInfo describes an available AI agent.
type AgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ChatMessage represents a message in the ask conversation.
type ChatMessage struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
	Agent   string `json:"agent"` // agent ID that generated the response
}

// AppStatus holds the current status of the app's backend resources.
type AppStatus struct {
	ProjectName   string `json:"project_name"`
	GraphReady    bool   `json:"graph_ready"`
	VectorReady   bool   `json:"vector_ready"`
	LLMReady      bool   `json:"llm_ready"`
	NodeCount     int    `json:"node_count"`
	EdgeCount     int    `json:"edge_count"`
	VectorCount   int    `json:"vector_count"`
	LLMProvider   string `json:"llm_provider"`
	EmbedProvider string `json:"embed_provider"`
	Branch        string `json:"branch"`
}
