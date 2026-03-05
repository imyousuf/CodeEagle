// Types mirroring Go structs from internal/app/types.go

export interface SearchFilters {
  node_type: string;
  package: string;
  language: string;
  no_docs: boolean;
  min_score: number;
  limit: number;
}

export interface CodeResult {
  name: string;
  type: string;
  file_path: string;
  line: number;
  package: string;
  language: string;
  signature: string;
  snippet: string;
  relevance: number;
  score: number;
}

export interface DocResult {
  name: string;
  type: string;
  file_path: string;
  snippet: string;
  relevance: number;
  score: number;
}

export interface SearchResults {
  code: CodeResult[] | null;
  docs: DocResult[] | null;
  query: string;
  total: number;
  provider: string;
}

export interface AgentInfo {
  id: string;
  name: string;
  description: string;
}

export interface ChatMessage {
  role: 'user' | 'assistant';
  content: string;
  agent: string;
}

export interface AppStatus {
  project_name: string;
  graph_ready: boolean;
  vector_ready: boolean;
  llm_ready: boolean;
  node_count: number;
  edge_count: number;
  vector_count: number;
  llm_provider: string;
  embed_provider: string;
  branch: string;
}

// Global Wails runtime bindings — single declaration for all methods.
declare global {
  interface Window {
    runtime: {
      EventsOn: (event: string, callback: (...args: unknown[]) => void) => () => void;
    };
    go: {
      app: {
        App: {
          Search: (query: string, filters: SearchFilters) => Promise<SearchResults>;
          GetStatus: () => Promise<AppStatus>;
          GetAgentTypes: () => Promise<AgentInfo[]>;
          AskAgent: (agentType: string, query: string) => Promise<void>;
        };
      };
    };
  }
}

export const defaultFilters: SearchFilters = {
  node_type: '',
  package: '',
  language: '',
  no_docs: false,
  min_score: 0,
  limit: 15,
};
