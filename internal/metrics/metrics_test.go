package metrics

import (
	"testing"
)

func TestCyclomaticComplexityGo(t *testing.T) {
	src := []byte(`package main

func compute(x int) int {
	if x > 0 && x < 100 {
		for i := range x {
			switch {
			case i%2 == 0:
				go handle(i)
			}
		}
	} else {
		return -1
	}
	return x
}
`)
	calc := &CyclomaticComplexityCalculator{}
	m, err := calc.Calculate("test.go", src, "go")
	if err != nil {
		t.Fatal(err)
	}
	cc := m[CyclomaticComplexity]
	// Branches: if, &&, for, range, case, go, else = 7  -> baseline 1 + 7 = 8
	if cc != 8 {
		t.Errorf("expected complexity 8, got %v", cc)
	}
}

func TestCyclomaticComplexityPython(t *testing.T) {
	src := []byte(`def process(items):
    if items and len(items) > 0:
        for item in items:
            if item.valid or item.override:
                pass
            elif item.skip:
                continue
    else:
        raise ValueError("empty")
`)
	calc := &CyclomaticComplexityCalculator{}
	m, err := calc.Calculate("test.py", src, "python")
	if err != nil {
		t.Fatal(err)
	}
	cc := m[CyclomaticComplexity]
	// if, and, for, if, or, elif, else = 7 -> baseline 1 + 7 = 8
	if cc != 8 {
		t.Errorf("expected complexity 8, got %v", cc)
	}
}

func TestCyclomaticComplexityJavaScript(t *testing.T) {
	src := []byte(`function handle(x) {
  if (x > 0 || x === -1) {
    for (let i = 0; i < x; i++) {
      try {
        process(i);
      } catch (e) {
        fallback(e);
      }
    }
  } else {
    return x ?? 0;
  }
}
`)
	calc := &CyclomaticComplexityCalculator{}
	m, err := calc.Calculate("test.js", src, "javascript")
	if err != nil {
		t.Fatal(err)
	}
	cc := m[CyclomaticComplexity]
	// if, ||, for, catch, else, ?? = 6 -> baseline 1 + 6 = 7
	if cc != 7 {
		t.Errorf("expected complexity 7, got %v", cc)
	}
}

func TestCyclomaticComplexityJava(t *testing.T) {
	src := []byte(`public class Example {
    public int run(int x) {
        if (x > 0 && x < 100) {
            for (int i = 0; i < x; i++) {
                while (check(i)) {
                    try {
                        process(i);
                    } catch (Exception e) {
                        handle(e);
                    }
                }
            }
        } else {
            return -1;
        }
        return x;
    }
}
`)
	calc := &CyclomaticComplexityCalculator{}
	m, err := calc.Calculate("Test.java", src, "java")
	if err != nil {
		t.Fatal(err)
	}
	cc := m[CyclomaticComplexity]
	// if, &&, for, while, catch, else = 6 -> baseline 1 + 6 = 7
	if cc != 7 {
		t.Errorf("expected complexity 7, got %v", cc)
	}
}

func TestCyclomaticComplexityUnsupportedLanguage(t *testing.T) {
	calc := &CyclomaticComplexityCalculator{}
	m, err := calc.Calculate("test.rb", []byte("if x > 0\n  puts x\nend"), "ruby")
	if err != nil {
		t.Fatal(err)
	}
	if m[CyclomaticComplexity] != 1 {
		t.Errorf("unsupported language should return baseline 1, got %v", m[CyclomaticComplexity])
	}
}

func TestLinesOfCodeGo(t *testing.T) {
	src := []byte(`package main

// main is the entry point.
func main() {
	/*
	  Multi-line comment
	*/
	fmt.Println("hello")
}
`)
	calc := &LinesOfCodeCalculator{}
	m, err := calc.Calculate("test.go", src, "go")
	if err != nil {
		t.Fatal(err)
	}

	if m[LinesOfCode] != 9 {
		t.Errorf("expected 9 total lines, got %v", m[LinesOfCode])
	}
	if m[BlankLines] != 1 {
		t.Errorf("expected 1 blank line, got %v", m[BlankLines])
	}
	if m[CommentLines] != 4 {
		t.Errorf("expected 4 comment lines, got %v", m[CommentLines])
	}
	if m[CodeLines] != 4 {
		t.Errorf("expected 4 code lines, got %v", m[CodeLines])
	}
}

func TestLinesOfCodePython(t *testing.T) {
	src := []byte(`# Module docstring
"""
This is a docstring.
"""

def hello():
    print("hello")
`)
	calc := &LinesOfCodeCalculator{}
	m, err := calc.Calculate("test.py", src, "python")
	if err != nil {
		t.Fatal(err)
	}

	if m[LinesOfCode] != 7 {
		t.Errorf("expected 7 total lines, got %v", m[LinesOfCode])
	}
	if m[BlankLines] != 1 {
		t.Errorf("expected 1 blank line, got %v", m[BlankLines])
	}
	// # comment + """ + docstring text + """ = 4
	if m[CommentLines] != 4 {
		t.Errorf("expected 4 comment lines, got %v", m[CommentLines])
	}
	if m[CodeLines] != 2 {
		t.Errorf("expected 2 code lines, got %v", m[CodeLines])
	}
}

func TestLinesOfCodeHTML(t *testing.T) {
	src := []byte(`<!-- Header -->
<html>
<body>

<!-- TODO: add content -->
</body>
</html>
`)
	calc := &LinesOfCodeCalculator{}
	m, err := calc.Calculate("test.html", src, "html")
	if err != nil {
		t.Fatal(err)
	}

	if m[LinesOfCode] != 7 {
		t.Errorf("expected 7 total lines, got %v", m[LinesOfCode])
	}
	if m[BlankLines] != 1 {
		t.Errorf("expected 1 blank line, got %v", m[BlankLines])
	}
	if m[CommentLines] != 2 {
		t.Errorf("expected 2 comment lines, got %v", m[CommentLines])
	}
}

func TestTodoCounter(t *testing.T) {
	src := []byte(`// TODO: implement this
// FIXME: broken logic
// HACK: workaround for bug #123
func hello() {
	// todo: another one
	// This is fine, no markers here
	// fixme and hack on same line
}
`)
	calc := &TodoCounter{}
	m, err := calc.Calculate("test.go", src, "go")
	if err != nil {
		t.Fatal(err)
	}
	if m[TodoCount] != 2 {
		t.Errorf("expected 2 TODOs, got %v", m[TodoCount])
	}
	if m[FixmeCount] != 2 {
		t.Errorf("expected 2 FIXMEs, got %v", m[FixmeCount])
	}
	if m[HackCount] != 2 {
		t.Errorf("expected 2 HACKs, got %v", m[HackCount])
	}
}

func TestCompositeCalculator(t *testing.T) {
	src := []byte(`package main

// TODO: refactor
func main() {
	if true {
		return
	}
}
`)
	calc := NewCompositeCalculator()
	m, err := calc.Calculate("test.go", src, "go")
	if err != nil {
		t.Fatal(err)
	}

	// Verify all metric types are present.
	expectedKeys := []MetricType{
		CyclomaticComplexity,
		LinesOfCode, BlankLines, CommentLines, CodeLines,
		TodoCount, FixmeCount, HackCount,
	}
	for _, k := range expectedKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing metric %s in composite result", k)
		}
	}

	if m[CyclomaticComplexity] < 1 {
		t.Error("complexity should be at least 1")
	}
	if m[TodoCount] != 1 {
		t.Errorf("expected 1 TODO, got %v", m[TodoCount])
	}
}
