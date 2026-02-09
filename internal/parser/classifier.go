package parser

import (
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Classifier performs post-parse classification of nodes, enriching them
// with architectural roles, design patterns, and layer tags based on
// heuristics (annotations, decorators, naming conventions, base classes,
// package paths).
type Classifier struct{}

// NewClassifier creates a new Classifier instance.
func NewClassifier() *Classifier {
	return &Classifier{}
}

// Classify iterates all nodes in a ParseResult and enriches them with
// architectural metadata. It may reclassify node types (e.g., Class -> DBModel)
// and adds PropArchRole, PropDesignPattern, and PropLayerTag properties.
func (c *Classifier) Classify(result *ParseResult) *ParseResult {
	for _, node := range result.Nodes {
		c.ClassifyNode(node)
	}
	return result
}

// ClassifyNode classifies a single node, modifying it in place.
func (c *Classifier) ClassifyNode(node *graph.Node) *graph.Node {
	if node.Properties == nil {
		node.Properties = make(map[string]string)
	}

	annotations := commaSplit(node.Properties["annotations"])
	decorators := commaSplit(node.Properties["decorators"])
	bases := commaSplit(node.Properties["bases"])
	extends := commaSplit(node.Properties["extends"])
	implements := commaSplit(node.Properties["implements"])
	methods := commaSplit(node.Properties["methods"])

	pkg := extractPackageDirName(node.FilePath, node.Package)

	var patterns []string

	// --- Role detection (runs first, on original node type) ---

	// Controller detection
	if c.isController(node, annotations, decorators, bases) {
		node.Properties[graph.PropArchRole] = "controller"
	}

	// Service layer detection
	if c.isService(node, annotations, decorators, pkg) {
		node.Properties[graph.PropArchRole] = "service"
	}

	// Repository/DAO detection
	if c.isRepository(node, annotations, pkg, methods) {
		node.Properties[graph.PropArchRole] = "repository"
	}

	// Middleware detection
	if c.isMiddleware(node, decorators, implements, annotations) {
		node.Properties[graph.PropArchRole] = "middleware"
	}

	// --- Type reclassification (skips ViewModel/DTO if role is already set) ---

	if isClassLike(node.Type) {
		role := node.Properties[graph.PropArchRole]
		if c.isDBModel(node, annotations, decorators, bases) {
			node.Type = graph.NodeDBModel
		} else if role == "" && c.isViewModel(node) {
			node.Type = graph.NodeViewModel
		} else if role == "" && c.isDTO(node, annotations, pkg) {
			node.Type = graph.NodeDTO
		} else if role == "" && c.isDomainModel(node, annotations, pkg) {
			node.Type = graph.NodeDomainModel
			node.Properties[graph.PropArchRole] = "domain_model"
		}
	}

	// --- Design pattern detection ---

	// Factory detection
	if c.isFactory(node) {
		patterns = append(patterns, "factory")
	}

	// Singleton detection
	if c.isSingleton(node, methods) {
		patterns = append(patterns, "singleton")
	}

	// Observer detection
	if c.isObserver(node, implements, extends, methods) {
		patterns = append(patterns, "observer")
	}

	// Builder detection
	if c.isBuilder(node, methods) {
		patterns = append(patterns, "builder")
	}

	// Repository pattern (also a design pattern)
	if node.Properties[graph.PropArchRole] == "repository" {
		patterns = append(patterns, "repository")
	}

	if len(patterns) > 0 {
		node.Properties[graph.PropDesignPattern] = strings.Join(patterns, ",")
	}

	// --- Layer tag ---
	layer := c.detectLayer(node, pkg)
	if layer != "" {
		node.Properties[graph.PropLayerTag] = layer
	}

	return node
}

// isClassLike returns true if the node type is a class or struct.
func isClassLike(t graph.NodeType) bool {
	return t == graph.NodeClass || t == graph.NodeStruct
}

// isDBModel detects database model types.
func (c *Classifier) isDBModel(node *graph.Node, annotations, decorators, bases []string) bool {
	lang := node.Language

	switch lang {
	case "java":
		for _, a := range annotations {
			switch a {
			case "Entity", "Table", "Document", "MappedSuperclass":
				return true
			}
		}
	case "python":
		for _, b := range bases {
			if b == "Model" || b == "Base" || b == "Document" {
				return true
			}
		}
		// dataclass with Base base
		hasDataclass := containsAny(decorators, "dataclass")
		hasBase := containsAny(bases, "Base")
		if hasDataclass && hasBase {
			return true
		}
	case "go":
		name := node.Name
		if strings.HasSuffix(name, "Model") || strings.HasSuffix(name, "Entity") {
			// Check for json/db/gorm tags in fields
			fields := node.Properties["fields"]
			if fields != "" {
				return true
			}
		}
	case "typescript":
		for _, d := range decorators {
			if d == "Entity" || d == "Schema" {
				return true
			}
		}
	}

	return false
}

// isDomainModel detects DDD domain model types.
func (c *Classifier) isDomainModel(node *graph.Node, annotations []string, pkg string) bool {
	if !isClassLike(node.Type) {
		return false
	}

	// Check name for DDD terms
	name := node.Name
	dddTerms := []string{"Aggregate", "ValueObject", "DomainEvent"}
	for _, term := range dddTerms {
		if strings.Contains(name, term) {
			// But NOT if it has DB annotations
			if !hasDBAnnotation(annotations) {
				return true
			}
		}
	}

	// "Entity" in name but NOT a DB entity (no JPA etc)
	if strings.Contains(name, "Entity") && !hasDBAnnotation(annotations) {
		// Only if also in a domain-like package
		if isDomainPackage(pkg) {
			return true
		}
	}

	// Located in domain packages
	if isDomainPackage(pkg) {
		// Classify as domain model only if name doesn't suggest DTO/ViewModel
		if !c.isDTO(node, annotations, pkg) && !c.isViewModel(node) {
			return false // Don't over-classify; only DDD-named things in domain packages
		}
	}

	return false
}

// isViewModel detects view model types by name.
func (c *Classifier) isViewModel(node *graph.Node) bool {
	name := node.Name
	return strings.HasSuffix(name, "ViewModel") || strings.HasSuffix(name, "View")
}

// isDTO detects data transfer object types.
func (c *Classifier) isDTO(node *graph.Node, annotations []string, pkg string) bool {
	name := node.Name
	suffixes := []string{"DTO", "Request", "Response", "Payload", "Command", "Query"}
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}

	// Java @JsonProperty / @Serializable on class
	if node.Language == "java" {
		for _, a := range annotations {
			if a == "JsonProperty" || a == "Serializable" {
				return true
			}
		}
	}

	// Package-based
	dtoPkgs := []string{"dto", "viewmodel", "api", "request", "response"}
	for _, p := range dtoPkgs {
		if pkg == p {
			return true
		}
	}

	return false
}

// isController detects controller/handler types.
func (c *Classifier) isController(node *graph.Node, annotations, decorators, bases []string) bool {
	if !isClassLike(node.Type) {
		return false
	}

	lang := node.Language
	switch lang {
	case "java":
		for _, a := range annotations {
			switch a {
			case "Controller", "RestController", "RequestMapping":
				return true
			}
		}
	case "python":
		for _, d := range decorators {
			if strings.Contains(d, "app.route") || strings.Contains(d, "router") {
				return true
			}
		}
		for _, b := range bases {
			if b == "APIView" || b == "ViewSet" {
				return true
			}
		}
	case "typescript":
		for _, d := range decorators {
			if d == "Controller" {
				return true
			}
		}
	case "go":
		name := node.Name
		if strings.HasSuffix(name, "Handler") || strings.HasSuffix(name, "Controller") {
			return true
		}
	}

	return false
}

// isService detects service layer types.
func (c *Classifier) isService(node *graph.Node, annotations, decorators []string, pkg string) bool {
	if !isClassLike(node.Type) {
		return false
	}

	lang := node.Language
	switch lang {
	case "java":
		for _, a := range annotations {
			if a == "Service" || a == "Component" {
				return true
			}
		}
	case "python":
		svcPkgs := []string{"service", "services", "use_case", "usecase"}
		for _, p := range svcPkgs {
			if pkg == p {
				return true
			}
		}
	case "typescript":
		for _, d := range decorators {
			if d == "Injectable" {
				return true
			}
		}
	case "go":
		name := node.Name
		if strings.HasSuffix(name, "Service") || strings.HasSuffix(name, "UseCase") || strings.HasSuffix(name, "Interactor") {
			return true
		}
	}

	return false
}

// isRepository detects repository/DAO types.
func (c *Classifier) isRepository(node *graph.Node, annotations []string, pkg string, methods []string) bool {
	if !isClassLike(node.Type) {
		return false
	}

	lang := node.Language
	switch lang {
	case "java":
		for _, a := range annotations {
			if a == "Repository" {
				return true
			}
		}
	case "go":
		name := node.Name
		if strings.HasSuffix(name, "Repository") || strings.HasSuffix(name, "Repo") ||
			strings.HasSuffix(name, "Store") || strings.HasSuffix(name, "DAO") {
			return true
		}
	case "python":
		repoPkgs := []string{"repository", "repositories"}
		for _, p := range repoPkgs {
			if pkg == p {
				return true
			}
		}
		name := node.Name
		if strings.HasSuffix(name, "Repository") || strings.HasSuffix(name, "Repo") {
			return true
		}
	}

	// Methods predominantly named with data access patterns
	if len(methods) >= 3 && c.hasDataAccessMethods(methods) {
		return true
	}

	return false
}

// isMiddleware detects middleware types.
func (c *Classifier) isMiddleware(node *graph.Node, decorators, implements, annotations []string) bool {
	lang := node.Language

	switch lang {
	case "go":
		// Function or struct with "Middleware" in name
		if strings.Contains(node.Name, "Middleware") {
			return true
		}
		// http.Handler pattern in signature
		if node.Type == graph.NodeFunction || node.Type == graph.NodeMethod {
			if strings.Contains(node.Signature, "http.Handler") {
				return true
			}
		}
	case "python":
		for _, d := range decorators {
			if strings.Contains(d, "middleware") {
				return true
			}
		}
	case "typescript":
		for _, impl := range implements {
			if impl == "NestMiddleware" {
				return true
			}
		}
		for _, d := range decorators {
			if d == "Middleware" {
				return true
			}
		}
	case "java":
		for _, a := range annotations {
			if a == "Filter" {
				return true
			}
		}
		for _, impl := range implements {
			if impl == "Filter" || impl == "HandlerInterceptor" {
				return true
			}
		}
	}

	return false
}

// isFactory detects factory functions/methods.
func (c *Classifier) isFactory(node *graph.Node) bool {
	if node.Type != graph.NodeFunction && node.Type != graph.NodeMethod {
		return false
	}

	name := node.Name
	prefixes := []string{"New", "Create", "Build", "Make"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			// For Go, check that it returns a pointer type
			if node.Language == "go" {
				if strings.Contains(node.Signature, "*") {
					return true
				}
				// Also match if it returns an interface (no pointer needed)
				continue
			}
			return true
		}
	}

	return false
}

// isSingleton detects singleton pattern: private constructor + getInstance-like.
func (c *Classifier) isSingleton(node *graph.Node, methods []string) bool {
	if !isClassLike(node.Type) {
		return false
	}

	hasGetInstance := false
	for _, m := range methods {
		ml := strings.ToLower(m)
		if ml == "getinstance" || ml == "instance" || ml == "shared" {
			hasGetInstance = true
			break
		}
	}

	return hasGetInstance
}

// isObserver detects observer pattern.
func (c *Classifier) isObserver(node *graph.Node, implements, extends, methods []string) bool {
	// Implements Listener/Observer interface
	for _, i := range implements {
		il := strings.ToLower(i)
		if strings.Contains(il, "listener") || strings.Contains(il, "observer") {
			return true
		}
	}
	for _, e := range extends {
		el := strings.ToLower(e)
		if strings.Contains(el, "listener") || strings.Contains(el, "observer") {
			return true
		}
	}

	// Has subscribe/notify methods
	hasSubscribe := false
	hasNotify := false
	for _, m := range methods {
		ml := strings.ToLower(m)
		if ml == "subscribe" || ml == "on" || ml == "addobserver" || ml == "addlistener" {
			hasSubscribe = true
		}
		if ml == "notify" || ml == "emit" || ml == "notifyobservers" || ml == "notifylisteners" {
			hasNotify = true
		}
	}
	return hasSubscribe && hasNotify
}

// isBuilder detects builder pattern: method chaining (methods return self type).
func (c *Classifier) isBuilder(node *graph.Node, methods []string) bool {
	if !isClassLike(node.Type) {
		return false
	}

	name := node.Name
	if strings.HasSuffix(name, "Builder") {
		return true
	}

	// Check for "Build" method among methods
	for _, m := range methods {
		if m == "Build" || m == "build" {
			return true
		}
	}

	return false
}

// hasDataAccessMethods checks if the majority of methods match data access naming patterns.
// Excludes singleton-like methods (getInstance) to avoid false positives.
func (c *Classifier) hasDataAccessMethods(methods []string) bool {
	daPrefixes := []string{"find", "get", "save", "delete", "create", "update", "list"}
	// Exclude singleton/non-data methods
	excludeLower := map[string]bool{
		"getinstance": true, "instance": true, "getclass": true,
	}
	count := 0
	for _, m := range methods {
		ml := strings.ToLower(m)
		if excludeLower[ml] {
			continue
		}
		for _, p := range daPrefixes {
			if strings.HasPrefix(ml, p) {
				count++
				break
			}
		}
	}
	// Require at least 3 matching methods and majority
	return count >= 3 && count > len(methods)/2
}

// detectLayer assigns a layer tag based on role and package name.
func (c *Classifier) detectLayer(node *graph.Node, pkg string) string {
	// Check explicit role first
	role := node.Properties[graph.PropArchRole]
	switch role {
	case "controller":
		return "presentation"
	case "service", "domain_model":
		return "business"
	case "repository":
		return "data_access"
	case "middleware":
		return "infrastructure"
	}

	// Check node type
	switch node.Type {
	case graph.NodeDBModel:
		return "data_access"
	case graph.NodeDomainModel:
		return "business"
	case graph.NodeViewModel, graph.NodeDTO:
		return "presentation"
	}

	// Package-based fallback
	presentationPkgs := []string{"controller", "controllers", "handler", "handlers", "view", "views", "template", "templates", "api"}
	businessPkgs := []string{"service", "services", "domain", "model", "entity", "core", "use_case", "usecase"}
	dataAccessPkgs := []string{"repository", "repositories", "dao", "store", "persistence", "migration", "migrations"}
	infraPkgs := []string{"config", "middleware", "util", "utils", "adapter", "adapters", "infrastructure", "infra"}

	for _, p := range presentationPkgs {
		if pkg == p {
			return "presentation"
		}
	}
	for _, p := range businessPkgs {
		if pkg == p {
			return "business"
		}
	}
	for _, p := range dataAccessPkgs {
		if pkg == p {
			return "data_access"
		}
	}
	for _, p := range infraPkgs {
		if pkg == p {
			return "infrastructure"
		}
	}

	return ""
}

// --- Helpers ---

// commaSplit splits a comma-separated string into a slice, trimming spaces
// and filtering empty entries.
func commaSplit(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// containsAny checks whether any of the target values appear in the slice.
func containsAny(slice []string, targets ...string) bool {
	for _, s := range slice {
		for _, t := range targets {
			if s == t {
				return true
			}
		}
	}
	return false
}

// hasDBAnnotation checks for JPA/MongoDB/similar DB annotations.
func hasDBAnnotation(annotations []string) bool {
	dbAnns := []string{"Entity", "Table", "Document", "MappedSuperclass"}
	return containsAny(annotations, dbAnns...)
}

// isDomainPackage checks if the package name indicates a domain layer.
func isDomainPackage(pkg string) bool {
	domainPkgs := []string{"domain", "model", "entity", "core"}
	for _, p := range domainPkgs {
		if pkg == p {
			return true
		}
	}
	return false
}

// extractPackageDirName extracts the immediate directory name from the file path.
// For Go, uses the Package field; for others, derives from the file path.
func extractPackageDirName(filePath, pkg string) string {
	if pkg != "" {
		// For Go files, package name is already set. But we also want the directory
		// name for package-based heuristics in other languages.
		// Try to extract from file path as it's more reliable for directory-based checks.
	}

	// Extract last directory component from file path
	parts := strings.Split(filePath, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return pkg
}
