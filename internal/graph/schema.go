package graph

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// NodeType represents the kind of entity in the knowledge graph.
type NodeType string

const (
	NodeRepository   NodeType = "Repository"
	NodeService      NodeType = "Service"
	NodeModule       NodeType = "Module"
	NodePackage      NodeType = "Package"
	NodeFile         NodeType = "File"
	NodeFunction     NodeType = "Function"
	NodeMethod       NodeType = "Method"
	NodeClass        NodeType = "Class"
	NodeStruct       NodeType = "Struct"
	NodeInterface    NodeType = "Interface"
	NodeEnum         NodeType = "Enum"
	NodeType_        NodeType = "Type"
	NodeConstant     NodeType = "Constant"
	NodeVariable     NodeType = "Variable"
	NodeAPIEndpoint  NodeType = "APIEndpoint"
	NodeDBModel      NodeType = "DBModel"
	NodeDomainModel  NodeType = "DomainModel"
	NodeViewModel    NodeType = "ViewModel"
	NodeDTO          NodeType = "DTO"
	NodeMigration    NodeType = "Migration"
	NodeDependency   NodeType = "Dependency"
	NodeDocument     NodeType = "Document"
	NodeAIGuideline  NodeType = "AIGuideline"
	NodeTestFunction NodeType = "TestFunction"
	NodeTestFile     NodeType = "TestFile"
	NodeDirectory    NodeType = "Directory"
	NodeTopic        NodeType = "Topic"
	NodePerson       NodeType = "Person"
	NodeYear         NodeType = "Year"
	NodeMonth        NodeType = "Month"
	NodeDate         NodeType = "Date"

	// Meeting transcript entities.

	// NodeMeeting is a single recorded meeting/session.
	NodeMeeting NodeType = "Meeting"
	// NodeSpeaker is a per-meeting diarization label (e.g. "Person 3").
	// It is an *unresolved* identity: it becomes meaningful only once an
	// IdentifiedAs edge links it to a Person.
	NodeSpeaker NodeType = "Speaker"
	// NodeTopicSegment is a contiguous span of a meeting about one topic.
	NodeTopicSegment NodeType = "TopicSegment"
	// NodeDecision is a decision reached during a meeting.
	NodeDecision NodeType = "Decision"
	// NodeActionItem is a follow-up/TODO arising from a meeting.
	NodeActionItem NodeType = "ActionItem"
)

// Well-known property keys used for architectural classification.
const (
	// PropArchRole is the architectural role of a node (e.g., "controller", "service",
	// "repository", "middleware", "factory", "observer", "gateway").
	PropArchRole = "architectural_role"

	// PropDesignPattern is a comma-separated list of design patterns detected
	// (e.g., "repository,singleton", "factory", "observer").
	PropDesignPattern = "design_pattern"

	// PropLayerTag classifies nodes into architectural layers
	// (e.g., "presentation", "business", "data_access", "infrastructure").
	PropLayerTag = "layer"

	// PropGraphSource indicates which branch a node or edge came from
	// when using BranchStore. Set to the branch name on reads, never persisted.
	PropGraphSource = "graph_source"

	// PropContentHash is the SHA-256 hash of a file's content ("sha256:<hex>").
	PropContentHash = "content_hash"

	// PropMimeType is the MIME type of a file (e.g., "image/jpeg", "text/x-go").
	PropMimeType = "mime_type"

	// PropSymlinkTarget is the relative path of a symlink's resolved target.
	PropSymlinkTarget = "symlink_target"
)

// Well-known property keys used by meeting transcript entities.
const (
	// PropSpeakerLabel is the raw diarization label for a speaker ("Person 3", "You").
	PropSpeakerLabel = "speaker_label"
	// PropSpeakerSource is the audio source for a speaker ("mic" = recording owner,
	// "monitor" = remote participant).
	PropSpeakerSource = "speaker_source"
	// PropConfidence is a 0..1 confidence score rendered as a decimal string.
	PropConfidence = "confidence"
	// PropEvidence is a verbatim transcript quote supporting an inference.
	PropEvidence = "evidence"
	// PropResolution records how an identity was resolved: "owner_anchor",
	// "llm", "alias", or "manual".
	PropResolution = "resolution_method"
	// PropStartTime is an offset in seconds from the start of the meeting.
	PropStartTime = "start_time"
	// PropEndTime is an offset in seconds from the start of the meeting.
	PropEndTime = "end_time"
	// PropMeetingID is the source session ID of the owning meeting.
	PropMeetingID = "meeting_id"
	// PropPlatform is the meeting platform ("Zoom", "Unknown", ...).
	PropPlatform = "platform"
	// PropDuration is the meeting duration in seconds.
	PropDuration = "duration_seconds"
	// PropSummary is a natural-language summary of a node's content.
	PropSummary = "summary"
	// PropStatus is the lifecycle state of an action item ("open", "done").
	PropStatus = "status"
	// PropDueDate is an ISO-8601 date an action item is due.
	PropDueDate = "due_date"
	// PropAssignee is the raw (unresolved) assignee name for an action item.
	PropAssignee = "assignee"
	// PropAliases is a comma-separated list of alternate spellings for a person,
	// including speech-recognition variants (e.g. "Imran,Imron").
	PropAliases = "aliases"
	// PropUtteranceCount is how many transcript segments a speaker contributed.
	PropUtteranceCount = "utterance_count"
	// PropSpeakingSeconds is the total seconds a speaker was talking.
	PropSpeakingSeconds = "speaking_seconds"
	// PropIsOwner marks the Person who owns the recordings (the "mic" speaker).
	PropIsOwner = "is_owner"
	// PropQuote is a representative verbatim quote.
	PropQuote = "quote"
	// PropIsTranscript marks a document that is also a meeting transcript.
	//
	// Such a file is both things at once: prose worth searching as a document,
	// and a record of who said what worth extracting as a meeting. It is
	// indexed as both, and this is what lets meeting indexing find the ones
	// that turned up during ordinary document indexing.
	PropIsTranscript = "is_transcript"
	// PropTranscriptFormat names the transcript layout recognized.
	PropTranscriptFormat = "transcript_format"
)

// EdgeType represents a relationship between two nodes.
type EdgeType string

const (
	EdgeContains    EdgeType = "Contains"
	EdgeImports     EdgeType = "Imports"
	EdgeDependsOn   EdgeType = "DependsOn"
	EdgeCalls       EdgeType = "Calls"
	EdgeImplements  EdgeType = "Implements"
	EdgeExposes     EdgeType = "Exposes"
	EdgeConsumes    EdgeType = "Consumes"
	EdgeDocuments   EdgeType = "Documents"
	EdgeTests       EdgeType = "Tests"
	EdgeMigrates    EdgeType = "Migrates"
	EdgeConfigures  EdgeType = "Configures"
	EdgeHasTopic    EdgeType = "HasTopic"
	EdgeAppearsIn   EdgeType = "AppearsIn"
	EdgeUpdatedOn   EdgeType = "UpdatedOn"
	EdgeDuplicateOf EdgeType = "DuplicateOf"
	EdgeSymLink     EdgeType = "SymLink"

	// Meeting transcript relationships.

	// EdgeAttended links a Person to a Meeting they participated in.
	EdgeAttended EdgeType = "Attended"
	// EdgeIdentifiedAs links a Speaker (diarization label) to the Person it
	// resolves to, carrying confidence, evidence, and resolution method.
	EdgeIdentifiedAs EdgeType = "IdentifiedAs"
	// EdgeAssignedTo links an ActionItem to the Person responsible for it.
	EdgeAssignedTo EdgeType = "AssignedTo"
	// EdgeRaisedBy links a Decision or ActionItem to the Person who raised it.
	EdgeRaisedBy EdgeType = "RaisedBy"
	// EdgeMentions links a meeting entity to something it refers to — a code
	// entity (Service, File, Function) or another Person.
	EdgeMentions EdgeType = "Mentions"
	// EdgeFollowsUp links an ActionItem to the Decision it implements, or a
	// Meeting to an earlier Meeting in the same series.
	EdgeFollowsUp EdgeType = "FollowsUp"
)

// MeetingNodeTypes lists the node types produced by meeting transcript indexing.
func MeetingNodeTypes() []NodeType {
	return []NodeType{
		NodeMeeting,
		NodeSpeaker,
		NodeTopicSegment,
		NodeDecision,
		NodeActionItem,
	}
}

// Node represents a source code or documentation entity in the knowledge graph.
type Node struct {
	ID            string             `json:"id"`
	Type          NodeType           `json:"type"`
	Name          string             `json:"name"`
	QualifiedName string             `json:"qualified_name"`
	FilePath      string             `json:"file_path"`
	Line          int                `json:"line"`
	EndLine       int                `json:"end_line"`
	Package       string             `json:"package"`
	Language      string             `json:"language"`
	Exported      bool               `json:"exported"`
	Signature     string             `json:"signature,omitempty"`
	DocComment    string             `json:"doc_comment,omitempty"`
	Properties    map[string]string  `json:"properties,omitempty"`
	Metrics       map[string]float64 `json:"metrics,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at,omitempty"`
}

// Edge represents a relationship between two nodes in the knowledge graph.
type Edge struct {
	ID         string            `json:"id"`
	Type       EdgeType          `json:"type"`
	SourceID   string            `json:"source_id"`
	TargetID   string            `json:"target_id"`
	Properties map[string]string `json:"properties,omitempty"`
}

// GraphStats holds aggregate statistics about the knowledge graph.
type GraphStats struct {
	NodeCount   int64              `json:"node_count"`
	EdgeCount   int64              `json:"edge_count"`
	NodesByType map[NodeType]int64 `json:"nodes_by_type"`
	EdgesByType map[EdgeType]int64 `json:"edges_by_type"`
}

// NewNodeID generates a deterministic node ID from the type, file path, and name.
// The ID is a hex-encoded SHA-256 hash prefix to keep keys compact and collision-resistant.
func NewNodeID(nodeType, filePath, name string) string {
	raw := fmt.Sprintf("%s:%s:%s", nodeType, filePath, name)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:12])
}
