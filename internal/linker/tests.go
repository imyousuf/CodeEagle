package linker

import (
	"context"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// linkTests creates EdgeTests edges between test nodes and their source counterparts.
// It links:
//   - TestFile -> File (by filename convention)
//   - TestFunction -> Function/Method (by name heuristic)
func (l *Linker) linkTests(ctx context.Context) (int, error) {
	linked := 0

	// --- Link test files to source files ---
	fileLinked, err := l.linkTestFiles(ctx)
	if err != nil {
		return linked, err
	}
	linked += fileLinked

	// --- Link test functions to source functions ---
	funcLinked, err := l.linkTestFunctions(ctx)
	if err != nil {
		return linked, err
	}
	linked += funcLinked

	return linked, nil
}

// linkTestFiles creates EdgeTests from TestFile nodes to their corresponding File nodes.
func (l *Linker) linkTestFiles(ctx context.Context) (int, error) {
	testFiles, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeTestFile,
	})
	if err != nil {
		return 0, err
	}
	if len(testFiles) == 0 {
		return 0, nil
	}

	// Build file index: filePath -> node for regular files.
	allFiles, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeFile,
	})
	if err != nil {
		return 0, err
	}

	fileByPath := make(map[string]*graph.Node)
	for _, f := range allFiles {
		fileByPath[f.FilePath] = f
	}

	linked := 0
	for _, tf := range testFiles {
		sourceFiles := deriveSourceFilePaths(tf.FilePath, tf.Language)
		for _, sourcePath := range sourceFiles {
			target, ok := fileByPath[sourcePath]
			if !ok {
				continue
			}

			edge := &graph.Edge{
				ID:       graph.NewNodeID(string(graph.EdgeTests), tf.ID, target.ID),
				Type:     graph.EdgeTests,
				SourceID: tf.ID,
				TargetID: target.ID,
				Properties: map[string]string{
					"kind": "file_coverage",
				},
			}
			if err := l.store.AddEdge(ctx, edge); err != nil {
				continue
			}
			linked++

			if l.verbose {
				l.log("    Test file: %s -> %s", tf.FilePath, target.FilePath)
			}
			break // Only link to first match.
		}
	}

	return linked, nil
}

// linkTestFunctions creates EdgeTests from TestFunction nodes to source Function/Method nodes.
func (l *Linker) linkTestFunctions(ctx context.Context) (int, error) {
	testFuncs, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeTestFunction,
	})
	if err != nil {
		return 0, err
	}
	if len(testFuncs) == 0 {
		return 0, nil
	}

	// Build lookup maps for source functions and methods.
	functions, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeFunction,
	})
	if err != nil {
		return 0, err
	}
	methods, err := l.store.QueryNodes(ctx, graph.NodeFilter{
		Type: graph.NodeMethod,
	})
	if err != nil {
		return 0, err
	}

	// Index: dir -> name -> node  (for same-directory matching)
	type nameIndex struct {
		byDir map[string]map[string]*graph.Node // dir -> name -> node
		byPkg map[string]map[string]*graph.Node // pkg -> name -> node
	}
	buildIndex := func(nodes []*graph.Node) nameIndex {
		idx := nameIndex{
			byDir: make(map[string]map[string]*graph.Node),
			byPkg: make(map[string]map[string]*graph.Node),
		}
		for _, n := range nodes {
			dir := filepath.Dir(n.FilePath)
			if idx.byDir[dir] == nil {
				idx.byDir[dir] = make(map[string]*graph.Node)
			}
			idx.byDir[dir][n.Name] = n

			if n.Package != "" {
				if idx.byPkg[n.Package] == nil {
					idx.byPkg[n.Package] = make(map[string]*graph.Node)
				}
				idx.byPkg[n.Package][n.Name] = n
			}
		}
		return idx
	}

	funcIdx := buildIndex(functions)
	methodIdx := buildIndex(methods)

	linked := 0
	for _, tf := range testFuncs {
		// Derive candidate source names from the test function name.
		candidates := deriveSourceFuncNames(tf.Name, tf.Language)
		if len(candidates) == 0 {
			continue
		}

		dir := filepath.Dir(tf.FilePath)
		pkg := tf.Package

		var target *graph.Node
		for _, cand := range candidates {
			// Try function match in same directory.
			if dirFuncs, ok := funcIdx.byDir[dir]; ok {
				if n, ok := dirFuncs[cand]; ok {
					target = n
					break
				}
			}
			// Try method match in same directory.
			if dirMethods, ok := methodIdx.byDir[dir]; ok {
				if n, ok := dirMethods[cand]; ok {
					target = n
					break
				}
			}
			// Try function match in same package.
			if pkg != "" {
				if pkgFuncs, ok := funcIdx.byPkg[pkg]; ok {
					if n, ok := pkgFuncs[cand]; ok {
						target = n
						break
					}
				}
				if pkgMethods, ok := methodIdx.byPkg[pkg]; ok {
					if n, ok := pkgMethods[cand]; ok {
						target = n
						break
					}
				}
			}
		}

		if target == nil {
			continue
		}

		edge := &graph.Edge{
			ID:       graph.NewNodeID(string(graph.EdgeTests), tf.ID, target.ID),
			Type:     graph.EdgeTests,
			SourceID: tf.ID,
			TargetID: target.ID,
			Properties: map[string]string{
				"kind": "function_coverage",
			},
		}
		if err := l.store.AddEdge(ctx, edge); err != nil {
			continue
		}
		linked++

		if l.verbose {
			l.log("    Test func: %s -> %s", tf.Name, target.Name)
		}
	}

	return linked, nil
}

// deriveSourceFilePaths generates candidate source file paths from a test file path.
func deriveSourceFilePaths(testPath, language string) []string {
	dir := filepath.Dir(testPath)
	base := filepath.Base(testPath)

	var candidates []string

	switch language {
	case "go":
		// foo_test.go -> foo.go
		if strings.HasSuffix(base, "_test.go") {
			src := strings.TrimSuffix(base, "_test.go") + ".go"
			candidates = append(candidates, filepath.Join(dir, src))
		}
	case "python":
		// test_handlers.py -> handlers.py
		if strings.HasPrefix(base, "test_") {
			src := strings.TrimPrefix(base, "test_")
			candidates = append(candidates, filepath.Join(dir, src))
		}
		// handlers_test.py -> handlers.py
		if strings.HasSuffix(base, "_test.py") {
			src := strings.TrimSuffix(base, "_test.py") + ".py"
			candidates = append(candidates, filepath.Join(dir, src))
		}
	case "typescript":
		// utils.test.ts -> utils.ts, utils.spec.ts -> utils.ts
		for _, pattern := range []string{".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx"} {
			if strings.HasSuffix(base, pattern) {
				ext := ".ts"
				if strings.HasSuffix(pattern, "x") {
					ext = ".tsx"
				}
				src := strings.TrimSuffix(base, pattern) + ext
				candidates = append(candidates, filepath.Join(dir, src))
				break
			}
		}
	case "javascript":
		// utils.test.js -> utils.js, utils.spec.js -> utils.js
		for _, pattern := range []string{".test.js", ".spec.js", ".test.jsx", ".spec.jsx"} {
			if strings.HasSuffix(base, pattern) {
				ext := ".js"
				if strings.HasSuffix(pattern, "x") {
					ext = ".jsx"
				}
				src := strings.TrimSuffix(base, pattern) + ext
				candidates = append(candidates, filepath.Join(dir, src))
				break
			}
		}
	case "java":
		// FooTest.java -> Foo.java, FooTests.java -> Foo.java
		// TestFoo.java -> Foo.java, FooIT.java -> Foo.java
		name := strings.TrimSuffix(base, ".java")
		if strings.HasSuffix(name, "Tests") {
			src := strings.TrimSuffix(name, "Tests") + ".java"
			candidates = append(candidates, filepath.Join(dir, src))
		} else if strings.HasSuffix(name, "Test") {
			src := strings.TrimSuffix(name, "Test") + ".java"
			candidates = append(candidates, filepath.Join(dir, src))
		} else if strings.HasSuffix(name, "IT") {
			src := strings.TrimSuffix(name, "IT") + ".java"
			candidates = append(candidates, filepath.Join(dir, src))
		}
		if strings.HasPrefix(name, "Test") {
			src := strings.TrimPrefix(name, "Test") + ".java"
			candidates = append(candidates, filepath.Join(dir, src))
		}
	}

	return candidates
}

// deriveSourceFuncNames generates candidate source function names from a test function name.
func deriveSourceFuncNames(testName, language string) []string {
	var candidates []string

	switch language {
	case "go":
		// TestFoo -> Foo, TestFoo_Bar -> Foo, Foo.Bar
		stripped := ""
		for _, prefix := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
			if strings.HasPrefix(testName, prefix) {
				stripped = strings.TrimPrefix(testName, prefix)
				break
			}
		}
		if stripped == "" {
			return nil
		}
		// TestFoo_Bar -> try "Foo" and "Bar" separately, and "Foo.Bar" as method
		if idx := strings.Index(stripped, "_"); idx > 0 {
			receiver := stripped[:idx]
			method := stripped[idx+1:]
			candidates = append(candidates, receiver)
			if method != "" {
				candidates = append(candidates, method)
				candidates = append(candidates, receiver+"."+method)
			}
		} else {
			candidates = append(candidates, stripped)
		}
	case "python":
		// test_process_user -> process_user
		if strings.HasPrefix(testName, "test_") {
			candidates = append(candidates, strings.TrimPrefix(testName, "test_"))
		}
	case "typescript", "javascript":
		// describe("UserService") -> UserService
		// test("should process") -> less useful, skip
		candidates = append(candidates, testName)
	case "java":
		// testProcessUser -> processUser, testProcessUser -> ProcessUser
		if strings.HasPrefix(testName, "test") {
			rest := strings.TrimPrefix(testName, "test")
			if len(rest) > 0 {
				// Lowercase first char: testProcessUser -> processUser
				lower := string(unicode.ToLower(rune(rest[0]))) + rest[1:]
				candidates = append(candidates, lower)
				candidates = append(candidates, rest) // Also try unchanged
			}
		}
	}

	return candidates
}
