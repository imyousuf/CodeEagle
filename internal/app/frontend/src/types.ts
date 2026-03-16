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

// Config types mirroring Go structs from internal/app/config_types.go

export interface ConfigDTO {
  project: ProjectDTO;
  repositories: RepositoryDTO[];
  watch: WatchDTO;
  languages: string[];
  agents: AgentsDTO;
  docs: DocsDTO;
}

export interface ProjectDTO {
  name: string;
}

export interface RepositoryDTO {
  path: string;
  type: string;
}

export interface WatchDTO {
  exclude: string[];
}

export interface AgentsDTO {
  llm_provider: string;
  model: string;
  project: string;
  location: string;
  credentials_file: string;
  base_url: string;
  auto_summarize: boolean;
  auto_link: boolean;
  embedding_provider: string;
  embedding_model: string;
}

export interface DocsDTO {
  provider: string;
  model: string;
  project: string;
  location: string;
  credentials_file: string;
  base_url: string;
  max_image_resolution: number;
  context_window: number;
  disable_thinking: boolean;
  exclude_extensions: string[];
  faces: FacesDTO;
}

export interface FacesDTO {
  enabled: boolean;
  model_dir: string;
  min_face_size: number;
  similarity_threshold: number;
  confidence_threshold: number;
  object_detection: boolean;
  object_confidence: number;
}

export interface DetectionResult {
  llm_provider: string;
  llm_hint: string;
  languages: string[];
  ollama_status: string;
  claude_cli: boolean;
  vertex_ai: boolean;
  anthropic_key: boolean;
}

export interface ServiceStatus {
  available: boolean;
  message: string;
}

export interface ConfigDiff {
  section: string;
  field: string;
  old_value: string;
  new_value: string;
}

export interface SaveResult {
  success: boolean;
  message: string;
  path: string;
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
          // Config handlers
          GetConfig: () => Promise<ConfigDTO>;
          GetAllLanguages: () => Promise<string[]>;
          GetConfigPath: () => Promise<string>;
          DetectAll: () => Promise<DetectionResult>;
          DetectLanguages: (path: string) => Promise<string[]>;
          BrowseDirectory: (title: string) => Promise<string>;
          BrowseFile: (title: string, filter: string) => Promise<string>;
          TestLLMConnection: (provider: string, model: string, project: string, location: string, credFile: string, baseURL: string) => Promise<ServiceStatus>;
          TestOllamaConnection: (baseURL: string) => Promise<ServiceStatus>;
          ValidatePath: (path: string, expectDir: boolean) => Promise<ServiceStatus>;
          PreviewConfigChanges: (proposed: ConfigDTO) => Promise<ConfigDiff[]>;
          SaveConfig: (proposed: ConfigDTO) => Promise<SaveResult>;
          StartSync: (full: boolean) => Promise<void>;
          IsSyncing: () => Promise<boolean>;
          // Face handlers
          GetPersons: () => Promise<PersonInfo[]>;
          CreatePerson: (name: string, relationships: string[]) => Promise<PersonInfo>;
          UpdatePerson: (id: string, name: string, relationships: string[]) => Promise<void>;
          DeletePerson: (id: string) => Promise<void>;
          GetFaceStats: () => Promise<FaceStats>;
          ResumeSync: () => Promise<void>;
          AssignFaceToPerson: (personID: string, imagePath: string, faceIndex: number, confidence: number) => Promise<void>;
          // Cluster handlers
          GetClusters: () => Promise<ClusterDetail[]>;
          GetNoiseFaces: () => Promise<ClusterFace[]>;
          GetFaceThumbnail: (imagePath: string, faceIndex: number, size: number) => Promise<string>;
          GetImagePreview: (imagePath: string, maxRes: number) => Promise<string>;
          RunClustering: (simThreshold: number) => Promise<void>;
          IsClusteringRunning: () => Promise<boolean>;
          RemoveFaceFromCluster: (imagePath: string, faceIndex: number) => Promise<void>;
          MergeClusters: (targetID: number, sourceIDs: number[]) => Promise<number>;
          SplitCluster: (clusterID: number, simThreshold: number) => Promise<Record<number, number>>;
          SetClusterLabel: (clusterID: number, label: string) => Promise<void>;
          AssignClusterToPerson: (clusterID: number, personID: string) => Promise<void>;
          GetSuggestedMerges: () => Promise<MergeSuggestion[]>;
        };
      };
    };
  }
}

// Face types mirroring Go structs from internal/app/faces_handlers.go

export interface PersonInfo {
  id: string;
  name: string;
  relationships: string[];
  face_count: number;
  created_at: string;
}

export interface ClusterInfo {
  cluster_id: number;
  face_count: number;
  image_paths: string[];
  label: string;
}

export interface FaceStats {
  total_persons: number;
  total_faces: number;
  detected_faces: number;
  images_with_faces: number;
  total_images: number;
  scanned_count: number;
  oldest_date: string;
  newest_date: string;
}

export interface FaceReviewItem {
  image_path: string;
  face_index: number;
  cluster_id: number;
  confidence: number;
}

export interface CheckpointData {
  new_clusters: number;
  clusters: ClusterInfo[];
  images_processed: number;
  total_images: number;
}

// Cluster types for face clustering UI

export interface ClusterFace {
  image_path: string;
  face_index: number;
}

export interface ClusterDetail {
  cluster_id: number;
  label: string;
  face_count: number;
  faces: ClusterFace[];
  person_id: string;
}

export interface MergeSuggestion {
  cluster_a: number;
  cluster_b: number;
  label_a: string;
  label_b: string;
  similarity: number;
  face_count_a: number;
  face_count_b: number;
}

export interface ClusteringProgress {
  phase: string;
  current: number;
  total: number;
}

export const defaultFilters: SearchFilters = {
  node_type: '',
  package: '',
  language: '',
  no_docs: false,
  min_score: 0,
  limit: 15,
};
