# MCP Tools Design: Query Symbols, Interface Implementors, and Node Edges

## Overview

This document provides a detailed design for three new MCP tools that expose the same capabilities as the CLI's `query symbols`, `query interface`, and `query edges` subcommands.

## Tool Specifications

### 1. query_file_symbols

**Purpose:** List all symbols in a file with signatures, line numbers, and metadata.

**Parameters:**
- `file_path` (string, required): The file path to list symbols for
- `json_output` (boolean, optional, default: false): Whether to return structured JSON or formatted text

**Store Methods Called:**
```go
// 1. Query all nodes in the file
nodes, err := store.QueryNodes(ctx, graph.NodeFilter{FilePath: filePath})

// 2. Get individual nodes (if needed for additional details)
// Not needed - QueryNodes returns full node objects
```

**Data Flow:**
```
User Request (file_path)
    ↓
QueryNodes(NodeFilter{FilePath: file_path})
    ↓
Filter out NodeFile types (keep only symbols)
    ↓
Sort by Line number
    ↓
Extract metadata (language, package) from first non-File node
    ↓
Format output (JSON or text table)
    ↓
Return to user
```

**Output Structure (JSON mode):**
```json
[
  {
    "id": "abc123def456",
    "type": "Function",
    "name": "ParseFile",
    "qualified_name": "parser.ParseFile",
    "file_path": "internal/parser/parser.go",
    "line": 42,
    "end_line": 67,
    "package": "parser",
    "language": "go",
    "exported": true,
    "signature": "func ParseFile(path string) (*AST, error)",
    "doc_comment": "ParseFile reads and parses a source file...",
    "properties": {},
    "metrics": {"complexity": 5.0}
  }
]
```

**Output Structure (Text mode):**
```
Symbols in internal/parser/parser.go (go, package: parser)

  Function      func ParseFile(path string) (*AST, error)          line 42-67     exported
  Struct        type Parser                                         line 70-80     exported
  Method        func (p *Parser) Parse() error                      line 82-120    exported

3 symbols
```

**Potential Issues:**

1. **Large files with many symbols:** The CLI doesn't limit results, but for MCP we might want to impose a limit (e.g., 500 symbols) and paginate or warn the user.

2. **File not found:** QueryNodes will return an empty array. The tool should return a clear message: "No symbols found in {file_path}. File may not be indexed or may be empty."

3. **Relative vs absolute paths:** The Store indexes files with normalized paths. The tool should normalize the input path to match what's in the graph (handle `./`, `../`, etc.).

4. **Filtering logic:** The CLI filters out `NodeFile` types for display. This is important - file nodes are structural metadata, not symbols.

5. **Metadata extraction:** Language and package are extracted from the first non-File node. If the file only contains a File node (e.g., empty file or binary file), these will be empty strings.

---

### 2. query_interface_implementors

**Purpose:** Show interface definitions and all types that implement them.

**Parameters:**
- `name_pattern` (string, required): Interface name or glob pattern (e.g., "Store", "*Handler", "I*")
- `json_output` (boolean, optional, default: false): Whether to return structured JSON or formatted text

**Store Methods Called:**
```go
// 1. Find all interfaces matching the pattern
interfaces, err := store.QueryNodes(ctx, graph.NodeFilter{
    Type:        graph.NodeInterface,
    NamePattern: namePattern,
})

// 2. For each interface, get implementors (incoming EdgeImplements edges)
for _, iface := range interfaces {
    implementors, err := store.GetNeighbors(
        ctx,
        iface.ID,
        graph.EdgeImplements,
        graph.Incoming,
    )
}
```

**Data Flow:**
```
User Request (name_pattern)
    ↓
QueryNodes(NodeFilter{Type: Interface, NamePattern: pattern})
    ↓
For each interface:
    ↓
    GetNeighbors(interfaceID, EdgeImplements, Incoming)
    ↓
    Collect implementor nodes
    ↓
Build result structure
    ↓
Format output (JSON or text)
    ↓
Return to user
```

**Output Structure (JSON mode):**
```json
[
  {
    "name": "Store",
    "file_path": "internal/graph/graph.go",
    "line": 28,
    "package": "graph",
    "signature": "type Store interface",
    "implementors": [
      {
        "type": "Struct",
        "name": "BadgerStore",
        "file_path": "internal/graph/embedded/store.go",
        "line": 45,
        "package": "embedded"
      },
      {
        "type": "Struct",
        "name": "Neo4jStore",
        "file_path": "internal/graph/neo4j/store.go",
        "line": 32,
        "package": "neo4j"
      }
    ]
  }
]
```

**Output Structure (Text mode):**
```
Interface: Store (internal/graph/graph.go:28, package: graph)
  Signature: type Store interface

  Implemented by:
    Struct     BadgerStore                   internal/graph/embedded/store.go:45  (package: embedded)
    Struct     Neo4jStore                    internal/graph/neo4j/store.go:32     (package: neo4j)
```

**Potential Issues:**

1. **No interfaces found:** QueryNodes returns empty array. Return clear message: "No interfaces matching '{pattern}' found."

2. **Interface with no implementors:** GetNeighbors returns empty array. This is valid - show "No implementors found." in the output.

3. **Multiple interfaces match pattern:** The tool handles this by returning results for all matches (same as CLI). Each interface gets its own result block.

4. **Glob pattern syntax:** The Store's QueryNodes uses glob patterns (e.g., `*`, `?`, `[abc]`). Document this in the tool description.

5. **Cross-language interfaces:**
   - **Go**: Interfaces are structural (method set matching). The `EdgeImplements` is created by the cross-file implements linker.
   - **Python**: Protocols (with `protocol=true` property) use structural matching; regular classes use nominal inheritance.
   - **TypeScript/Java**: Nominal inheritance (explicit `implements` keyword).

   The tool doesn't need to know about these differences - the graph already has the correct `EdgeImplements` edges created by the parsers and linker.

6. **Direction confusion:** We want **incoming** edges to the interface (types pointing TO the interface via Implements edges). Common mistake is to use Outgoing.

---

### 3. query_node_edges

**Purpose:** Show all edges (relationships) for a node, both outgoing and incoming.

**Parameters:**
- `node_identifier` (string, required): Node ID or name to query
- `edge_type` (string, optional): Filter by specific edge type (e.g., "Calls", "Implements", "Contains")
- `direction` (string, optional, default: "both"): Edge direction to show: "in", "out", or "both"
- `json_output` (boolean, optional, default: false): Whether to return structured JSON or formatted text

**Store Methods Called:**
```go
// 1. Try to find node by ID first
node, err := store.GetNode(ctx, nodeIdentifier)

// 2. If not found, search by name
if err != nil || node == nil {
    candidates, err := store.QueryNodes(ctx, graph.NodeFilter{
        NamePattern: nodeIdentifier,
    })
    if len(candidates) > 0 {
        node = candidates[0]
    }
}

// 3. Get all edges for the node
edges, err := store.GetEdges(ctx, node.ID, graph.EdgeType(edgeType))

// 4. For each edge, resolve the other node
for _, edge := range edges {
    otherID := edge.SourceID  // or edge.TargetID depending on direction
    other, _ := store.GetNode(ctx, otherID)
}
```

**Data Flow:**
```
User Request (node_identifier, edge_type?, direction?)
    ↓
GetNode(node_identifier)  [try as ID]
    ↓
If not found: QueryNodes(NodeFilter{NamePattern: identifier})
    ↓
GetEdges(nodeID, edgeType)
    ↓
For each edge:
    ↓
    Determine if outgoing (SourceID == nodeID) or incoming
    ↓
    Get other node: GetNode(otherID)
    ↓
    Build edge entry with node details
    ↓
Filter by direction (in/out/both)
    ↓
Format output (JSON or text)
    ↓
Return to user
```

**Output Structure (JSON mode):**
```json
{
  "node": {
    "id": "abc123def456",
    "type": "Function",
    "name": "ParseFile",
    "file_path": "internal/parser/parser.go",
    "line": 42,
    "package": "parser",
    "language": "go"
  },
  "outgoing": [
    {
      "edge_type": "Calls",
      "node_id": "def456ghi789",
      "node_type": "Function",
      "node_name": "readSource",
      "file_path": "internal/parser/reader.go",
      "line": 28,
      "package": "parser",
      "properties": {"call_count": "3"}
    }
  ],
  "incoming": [
    {
      "edge_type": "Calls",
      "node_id": "ghi789jkl012",
      "node_type": "Function",
      "node_name": "IndexFile",
      "file_path": "internal/indexer/indexer.go",
      "line": 156,
      "package": "indexer",
      "properties": {}
    }
  ]
}
```

**Output Structure (Text mode):**
```
Edges for: ParseFile (Function, internal/parser/parser.go:42)

  Outgoing:
    Calls          -> readSource (Function, internal/parser/reader.go:28)
    Calls          -> validateAST (Function, internal/parser/validator.go:15)

  Incoming:
    Calls          <- IndexFile (Function, internal/indexer/indexer.go:156)
    Tests          <- TestParseFile (TestFunction, internal/parser/parser_test.go:25)

2 outgoing, 2 incoming
```

**Potential Issues:**

1. **Node not found:** Neither GetNode nor QueryNodes finds a match. Return clear error: "No node found matching '{identifier}'. Try using search_nodes to find the correct name or ID."

2. **Multiple candidates when searching by name:** QueryNodes may return multiple matches. The CLI uses the first match (`candidates[0]`). For MCP, we should either:
   - Follow CLI behavior (use first match) and warn if > 1 match
   - Return an error listing the candidates and ask user to be more specific
   - Recommended: Use first match but log a warning if there are multiple candidates

3. **Edge type validation:** The user might pass an invalid edge type (e.g., "Depends" instead of "DependsOn"). Options:
   - Accept any string and let the Store filter (no matches = empty result)
   - Validate against known EdgeType constants and return error for invalid types
   - Recommended: Accept any string (flexible), document valid types in tool description

4. **Direction parameter validation:** Only "in", "out", "both" are valid. Invalid values should default to "both" with a warning.

5. **Orphaned edges:** If an edge references a node ID that no longer exists in the graph, GetNode returns nil. The CLI handles this by showing the node ID instead of node details:
   ```go
   if other != nil {
       entry.NodeType = other.Type
       entry.NodeName = other.Name
       // ... other fields
   } else {
       entry.NodeName = otherID  // fallback to ID
   }
   ```
   This is good defensive programming - the MCP tool should do the same.

6. **Large number of edges:** Some nodes (e.g., a Package node) might have hundreds or thousands of edges. Consider:
   - Imposing a limit (e.g., 200 edges per direction) with a warning
   - Or: Return all edges but warn if count > threshold
   - Recommended: Return all edges (match CLI behavior) but warn in description that results may be large

7. **Edge properties:** Edges can have properties (e.g., `call_count` for Calls edges, `method` for HTTP calls). These should be included in the output.

8. **Empty edge type filter:** If `edge_type` is empty string, GetEdges returns ALL edge types. This is correct behavior.

---

## Implementation Plan

### Step 1: Create Tool Structs

Add three new tool structs in `internal/agents/planner_tools.go`:

```go
// queryFileSymbolsTool implements the query_file_symbols MCP tool.
type queryFileSymbolsTool struct {
    store graph.Store
}

// queryInterfaceImplementorsTool implements the query_interface_implementors MCP tool.
type queryInterfaceImplementorsTool struct {
    store graph.Store
}

// queryNodeEdgesTool implements the query_node_edges MCP tool.
type queryNodeEdgesTool struct {
    store graph.Store
}
```

### Step 2: Implement Tool Interface

Each tool must implement the `Tool` interface from `internal/agents/tool.go`:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args map[string]any) (string, bool)
}
```

### Step 3: Add to Registry

Update `NewPlannerTools()` to include the new tools:

```go
func NewPlannerTools(ctxBuilder *ContextBuilder) []Tool {
    return []Tool{
        &graphOverviewTool{ctxBuilder: ctxBuilder},
        &architectureOverviewTool{ctxBuilder: ctxBuilder},
        &serviceInfoTool{ctxBuilder: ctxBuilder},
        &fileInfoTool{ctxBuilder: ctxBuilder},
        &impactAnalysisTool{ctxBuilder: ctxBuilder},
        &searchNodesTool{store: ctxBuilder.store},
        &modelInfoTool{ctxBuilder: ctxBuilder},
        &projectGuidelinesTool{ctxBuilder: ctxBuilder},

        // New query tools
        &queryFileSymbolsTool{store: ctxBuilder.store},
        &queryInterfaceImplementorsTool{store: ctxBuilder.store},
        &queryNodeEdgesTool{store: ctxBuilder.store},
    }
}
```

### Step 4: Implement Each Tool

See detailed implementation section below for each tool.

---

## Detailed Implementation

### query_file_symbols Implementation

```go
func (t *queryFileSymbolsTool) Name() string {
    return "query_file_symbols"
}

func (t *queryFileSymbolsTool) Description() string {
    return "List all symbols in a file with signatures and line numbers. " +
           "Symbols include functions, classes, structs, interfaces, methods, " +
           "constants, and variables. File nodes are excluded from the output."
}

func (t *queryFileSymbolsTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "file_path": map[string]any{
                "type":        "string",
                "description": "The file path to list symbols for (required). Can be relative or absolute.",
            },
            "json_output": map[string]any{
                "type":        "boolean",
                "description": "Whether to return structured JSON (true) or formatted text (false). Defaults to false.",
            },
        },
        "required": []string{"file_path"},
    }
}

func (t *queryFileSymbolsTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
    filePath, _ := args["file_path"].(string)
    if filePath == "" {
        return "Error: file_path is required", false
    }

    jsonOutput, _ := args["json_output"].(bool)

    // Query all nodes in the file
    nodes, err := t.store.QueryNodes(ctx, graph.NodeFilter{FilePath: filePath})
    if err != nil {
        return fmt.Sprintf("Error querying nodes: %v", err), false
    }

    // Sort by line number
    sort.Slice(nodes, func(i, j int) bool {
        return nodes[i].Line < nodes[j].Line
    })

    // Extract file metadata from first non-File node
    var lang, pkg string
    for _, n := range nodes {
        if n.Type == graph.NodeFile {
            continue
        }
        if lang == "" && n.Language != "" {
            lang = n.Language
        }
        if pkg == "" && n.Package != "" {
            pkg = n.Package
        }
        if lang != "" && pkg != "" {
            break
        }
    }

    // Filter out File nodes for display
    symbols := make([]*graph.Node, 0, len(nodes))
    for _, n := range nodes {
        if n.Type != graph.NodeFile {
            symbols = append(symbols, n)
        }
    }

    if len(symbols) == 0 {
        return fmt.Sprintf("No symbols found in %s. File may not be indexed or may be empty.", filePath), false
    }

    // JSON output
    if jsonOutput {
        data, err := json.MarshalIndent(symbols, "", "  ")
        if err != nil {
            return fmt.Sprintf("Error marshaling JSON: %v", err), false
        }
        return string(data), true
    }

    // Text output
    var b strings.Builder
    header := fmt.Sprintf("Symbols in %s", filePath)
    if lang != "" || pkg != "" {
        parts := []string{}
        if lang != "" {
            parts = append(parts, lang)
        }
        if pkg != "" {
            parts = append(parts, "package: "+pkg)
        }
        header += " (" + strings.Join(parts, ", ") + ")"
    }
    fmt.Fprintln(&b, header)
    fmt.Fprintln(&b)

    for _, n := range symbols {
        // Type column
        typeStr := fmt.Sprintf("%-12s", n.Type)

        // Name + signature
        nameStr := n.Name
        if n.Signature != "" {
            nameStr = n.Signature
        }

        // Line range
        lineStr := ""
        if n.Line > 0 {
            if n.EndLine > 0 && n.EndLine != n.Line {
                lineStr = fmt.Sprintf("line %d-%d", n.Line, n.EndLine)
            } else {
                lineStr = fmt.Sprintf("line %d", n.Line)
            }
        }

        // Exported status
        exportStr := ""
        if n.Exported {
            exportStr = "exported"
        }

        fmt.Fprintf(&b, "  %s  %-60s  %-14s  %s\n", typeStr, nameStr, lineStr, exportStr)
    }

    fmt.Fprintf(&b, "\n%d symbols\n", len(symbols))
    return b.String(), true
}
```

### query_interface_implementors Implementation

```go
func (t *queryInterfaceImplementorsTool) Name() string {
    return "query_interface_implementors"
}

func (t *queryInterfaceImplementorsTool) Description() string {
    return "Show interface definitions and all types that implement them. " +
           "Supports glob patterns (e.g., 'Store', '*Handler', 'I*'). " +
           "Works across Go (structural), Python (Protocol + nominal), " +
           "TypeScript (nominal), and Java (nominal) interfaces."
}

func (t *queryInterfaceImplementorsTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "name_pattern": map[string]any{
                "type":        "string",
                "description": "Interface name or glob pattern (required). Examples: 'Store', '*Handler', 'I*'",
            },
            "json_output": map[string]any{
                "type":        "boolean",
                "description": "Whether to return structured JSON (true) or formatted text (false). Defaults to false.",
            },
        },
        "required": []string{"name_pattern"},
    }
}

// interfaceResult holds the structured output
type interfaceResult struct {
    Name         string               `json:"name"`
    FilePath     string               `json:"file_path"`
    Line         int                  `json:"line"`
    Package      string               `json:"package"`
    Signature    string               `json:"signature,omitempty"`
    Implementors []interfaceImplEntry `json:"implementors"`
}

type interfaceImplEntry struct {
    Type     graph.NodeType `json:"type"`
    Name     string         `json:"name"`
    FilePath string         `json:"file_path"`
    Line     int            `json:"line"`
    Package  string         `json:"package"`
}

func (t *queryInterfaceImplementorsTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
    namePattern, _ := args["name_pattern"].(string)
    if namePattern == "" {
        return "Error: name_pattern is required", false
    }

    jsonOutput, _ := args["json_output"].(bool)

    // Find all interfaces matching the pattern
    interfaces, err := t.store.QueryNodes(ctx, graph.NodeFilter{
        Type:        graph.NodeInterface,
        NamePattern: namePattern,
    })
    if err != nil {
        return fmt.Sprintf("Error querying interfaces: %v", err), false
    }

    if len(interfaces) == 0 {
        return fmt.Sprintf("No interfaces matching '%s' found.", namePattern), false
    }

    // Build results
    var results []interfaceResult
    for _, iface := range interfaces {
        result := interfaceResult{
            Name:      iface.Name,
            FilePath:  iface.FilePath,
            Line:      iface.Line,
            Package:   iface.Package,
            Signature: iface.Signature,
        }

        // Find implementors: nodes with incoming EdgeImplements edges
        implementors, err := t.store.GetNeighbors(
            ctx,
            iface.ID,
            graph.EdgeImplements,
            graph.Incoming,
        )
        if err != nil {
            return fmt.Sprintf("Error getting implementors for %s: %v", iface.Name, err), false
        }

        for _, impl := range implementors {
            result.Implementors = append(result.Implementors, interfaceImplEntry{
                Type:     impl.Type,
                Name:     impl.Name,
                FilePath: impl.FilePath,
                Line:     impl.Line,
                Package:  impl.Package,
            })
        }

        results = append(results, result)
    }

    // JSON output
    if jsonOutput {
        data, err := json.MarshalIndent(results, "", "  ")
        if err != nil {
            return fmt.Sprintf("Error marshaling JSON: %v", err), false
        }
        return string(data), true
    }

    // Text output
    var b strings.Builder
    for i, r := range results {
        if i > 0 {
            fmt.Fprintln(&b)
        }
        loc := r.FilePath
        if r.Line > 0 {
            loc = fmt.Sprintf("%s:%d", r.FilePath, r.Line)
        }
        fmt.Fprintf(&b, "Interface: %s (%s, package: %s)\n", r.Name, loc, r.Package)
        if r.Signature != "" {
            fmt.Fprintf(&b, "  Signature: %s\n", r.Signature)
        }

        if len(r.Implementors) == 0 {
            fmt.Fprintln(&b, "\n  No implementors found.")
        } else {
            fmt.Fprintln(&b, "\n  Implemented by:")
            for _, impl := range r.Implementors {
                implLoc := impl.FilePath
                if impl.Line > 0 {
                    implLoc = fmt.Sprintf("%s:%d", impl.FilePath, impl.Line)
                }
                fmt.Fprintf(&b, "    %-10s %-30s %s  (package: %s)\n",
                    impl.Type, impl.Name, implLoc, impl.Package)
            }
        }
    }

    return b.String(), true
}
```

### query_node_edges Implementation

```go
func (t *queryNodeEdgesTool) Name() string {
    return "query_node_edges"
}

func (t *queryNodeEdgesTool) Description() string {
    return "Show all edges (relationships) for a node. " +
           "Supports filtering by edge type and direction. " +
           "Valid edge types: Contains, Imports, DependsOn, Calls, Implements, " +
           "Exposes, Consumes, Documents, Tests, Migrates, Configures. " +
           "Valid directions: in (incoming), out (outgoing), both (default)."
}

func (t *queryNodeEdgesTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "node_identifier": map[string]any{
                "type":        "string",
                "description": "Node ID or name to query (required). Will try ID first, then name search.",
            },
            "edge_type": map[string]any{
                "type":        "string",
                "description": "Filter by specific edge type (optional). Examples: 'Calls', 'Implements', 'Contains'",
            },
            "direction": map[string]any{
                "type":        "string",
                "description": "Edge direction to show: 'in', 'out', or 'both' (default: 'both')",
            },
            "json_output": map[string]any{
                "type":        "boolean",
                "description": "Whether to return structured JSON (true) or formatted text (false). Defaults to false.",
            },
        },
        "required": []string{"node_identifier"},
    }
}

// edgeEntry holds a resolved edge for display
type edgeEntry struct {
    EdgeType   graph.EdgeType    `json:"edge_type"`
    NodeID     string            `json:"node_id"`
    NodeType   graph.NodeType    `json:"node_type"`
    NodeName   string            `json:"node_name"`
    FilePath   string            `json:"file_path,omitempty"`
    Line       int               `json:"line,omitempty"`
    Package    string            `json:"package,omitempty"`
    Properties map[string]string `json:"properties,omitempty"`
}

// edgesResult holds the structured output
type edgesResult struct {
    Node     *graph.Node `json:"node"`
    Outgoing []edgeEntry `json:"outgoing"`
    Incoming []edgeEntry `json:"incoming"`
}

func (t *queryNodeEdgesTool) Execute(ctx context.Context, args map[string]any) (string, bool) {
    nodeIdentifier, _ := args["node_identifier"].(string)
    if nodeIdentifier == "" {
        return "Error: node_identifier is required", false
    }

    edgeTypeStr, _ := args["edge_type"].(string)
    direction, _ := args["direction"].(string)
    if direction == "" {
        direction = "both"
    }
    jsonOutput, _ := args["json_output"].(bool)

    // Validate direction
    if direction != "in" && direction != "out" && direction != "both" {
        direction = "both"
        // Could log a warning here
    }

    // Try to find node by ID first
    node, err := t.store.GetNode(ctx, nodeIdentifier)
    if err != nil || node == nil {
        // Try name search
        candidates, qErr := t.store.QueryNodes(ctx, graph.NodeFilter{
            NamePattern: nodeIdentifier,
        })
        if qErr != nil {
            return fmt.Sprintf("Error querying nodes: %v", qErr), false
        }
        if len(candidates) == 0 {
            return fmt.Sprintf("No node found matching '%s'. Try using search_nodes to find the correct name or ID.", nodeIdentifier), false
        }
        node = candidates[0]
        // Could warn if len(candidates) > 1
    }

    // Get all edges for this node
    edges, err := t.store.GetEdges(ctx, node.ID, graph.EdgeType(edgeTypeStr))
    if err != nil {
        return fmt.Sprintf("Error getting edges: %v", err), false
    }

    // Resolve other nodes and split into outgoing/incoming
    var outgoing, incoming []edgeEntry
    for _, e := range edges {
        var otherID string
        var isOutgoing bool
        if e.SourceID == node.ID {
            otherID = e.TargetID
            isOutgoing = true
        } else {
            otherID = e.SourceID
            isOutgoing = false
        }

        other, _ := t.store.GetNode(ctx, otherID)
        entry := edgeEntry{
            EdgeType:   e.Type,
            NodeID:     otherID,
            Properties: e.Properties,
        }
        if other != nil {
            entry.NodeType = other.Type
            entry.NodeName = other.Name
            entry.FilePath = other.FilePath
            entry.Line = other.Line
            entry.Package = other.Package
        } else {
            // Orphaned edge - fallback to ID
            entry.NodeName = otherID
        }

        if isOutgoing {
            outgoing = append(outgoing, entry)
        } else {
            incoming = append(incoming, entry)
        }
    }

    // Apply direction filter
    showOutgoing := direction == "both" || direction == "out"
    showIncoming := direction == "both" || direction == "in"

    if !showOutgoing {
        outgoing = nil
    }
    if !showIncoming {
        incoming = nil
    }

    // JSON output
    if jsonOutput {
        result := edgesResult{
            Node:     node,
            Outgoing: outgoing,
            Incoming: incoming,
        }
        data, err := json.MarshalIndent(result, "", "  ")
        if err != nil {
            return fmt.Sprintf("Error marshaling JSON: %v", err), false
        }
        return string(data), true
    }

    // Text output
    var b strings.Builder
    loc := node.FilePath
    if node.Line > 0 {
        loc = fmt.Sprintf("%s:%d", node.FilePath, node.Line)
    }
    fmt.Fprintf(&b, "Edges for: %s (%s, %s)\n", node.Name, node.Type, loc)

    if showOutgoing {
        fmt.Fprintln(&b, "\n  Outgoing:")
        if len(outgoing) == 0 {
            fmt.Fprintln(&b, "    (none)")
        }
        for _, e := range outgoing {
            detail := formatEdgeNodeDetail(e)
            fmt.Fprintf(&b, "    %-14s -> %s\n", e.EdgeType, detail)
        }
    }

    if showIncoming {
        fmt.Fprintln(&b, "\n  Incoming:")
        if len(incoming) == 0 {
            fmt.Fprintln(&b, "    (none)")
        }
        for _, e := range incoming {
            detail := formatEdgeNodeDetail(e)
            fmt.Fprintf(&b, "    %-14s <- %s\n", e.EdgeType, detail)
        }
    }

    fmt.Fprintf(&b, "\n%d outgoing, %d incoming\n", len(outgoing), len(incoming))
    return b.String(), true
}

// formatEdgeNodeDetail formats a resolved edge entry for text display
func formatEdgeNodeDetail(e edgeEntry) string {
    parts := []string{e.NodeName}
    if e.NodeType != "" {
        parts = append(parts, fmt.Sprintf("(%s", e.NodeType))
        if e.FilePath != "" {
            loc := e.FilePath
            if e.Line > 0 {
                loc = fmt.Sprintf("%s:%d", e.FilePath, e.Line)
            }
            parts[len(parts)-1] += ", " + loc + ")"
        } else {
            parts[len(parts)-1] += ")"
        }
    }
    return strings.Join(parts, " ")
}
```

---

## Testing Strategy

### Unit Tests

Create `internal/agents/query_tools_test.go`:

1. **query_file_symbols tests:**
   - File with multiple symbols (functions, structs, methods)
   - Empty file (only File node)
   - File not found (empty result)
   - JSON vs text output
   - Sorting by line number
   - Exported vs unexported symbols

2. **query_interface_implementors tests:**
   - Interface with multiple implementors
   - Interface with zero implementors
   - Pattern matching multiple interfaces
   - No interfaces found
   - JSON vs text output
   - Cross-language interfaces (Go, Python, Java, TypeScript)

3. **query_node_edges tests:**
   - Node with both incoming and outgoing edges
   - Node with zero edges
   - Filtering by edge type
   - Direction filtering (in/out/both)
   - Node lookup by ID vs name
   - Multiple name matches (use first)
   - JSON vs text output
   - Orphaned edges (other node deleted)

### Integration Tests

Use the test ground (`/home/imyousuf/projects/opal-app`):

1. **query_file_symbols:**
   - Query `internal/graph/graph.go` (interface with method signatures)
   - Query a test file with TestFunctions
   - Query a Python file with classes and methods

2. **query_interface_implementors:**
   - Query `Store` interface (should find BadgerStore, Neo4jStore)
   - Query Python Protocol (should find implementors)
   - Query with pattern `*Store` (should match multiple interfaces)

3. **query_node_edges:**
   - Query a Function node (should have Calls edges)
   - Query an Interface node (should have incoming Implements edges)
   - Query a Service node (should have Contains, Exposes edges)
   - Query with edge_type filter (e.g., only Calls edges)

### Manual Testing via MCP

After implementation, test via Claude CLI:

```bash
# Start MCP server
codeeagle mcp serve

# In Claude CLI session, test tools:
# - query_file_symbols with file_path="internal/graph/graph.go"
# - query_interface_implementors with name_pattern="Store"
# - query_node_edges with node_identifier="ParseFile", direction="both"
```

---

## Edge Cases & Defensive Programming

1. **Nil checks:** Always check if GetNode returns nil before accessing node fields.

2. **Empty results:** Provide clear, actionable messages (not just "no results").

3. **Large results:** Consider warning when returning > 100 symbols or > 200 edges.

4. **Invalid input:** Validate required parameters, provide defaults for optional ones.

5. **Error context:** Wrap errors with context about what operation failed.

6. **Pattern matching:** Document glob syntax in tool descriptions.

7. **Cross-language semantics:** Don't assume language-specific behavior - rely on what's in the graph.

8. **Performance:** All queries use indexed lookups (NodeFilter, GetEdges), should be fast even on large graphs.

---

## Documentation Updates

After implementation, update:

1. **README.md:** Add section on new MCP tools with examples.

2. **docs/mcp-tools.md:** (create if doesn't exist) Document all MCP tools with parameters, examples, and use cases.

3. **CLAUDE.md:** Update architecture section to mention new query tools.

4. **MEMORY.md:** Note completion of MCP query tools feature.

---

## Summary

The three new MCP tools map directly to existing CLI subcommands, using the same Store methods and data flow. Key points:

- **Store methods used:**
  - `QueryNodes(ctx, NodeFilter)` - all three tools
  - `GetNeighbors(ctx, nodeID, edgeType, direction)` - interface tool
  - `GetNode(ctx, id)` - edges tool
  - `GetEdges(ctx, nodeID, edgeType)` - edges tool

- **Data flow:** Simple and consistent across all tools:
  1. Validate input parameters
  2. Query the graph via Store methods
  3. Transform results into output structure
  4. Format as JSON or text
  5. Return to user

- **Potential issues:** Mostly defensive programming concerns (nil checks, empty results, large result sets, invalid input). The CLI implementations already handle most of these well - the MCP tools should follow the same patterns.

- **Implementation effort:** Low risk, straightforward port from CLI to MCP tool interface. Estimated ~200 lines of code per tool, plus ~300 lines of tests.
