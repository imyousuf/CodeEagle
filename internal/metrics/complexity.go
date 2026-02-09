package metrics

import (
	"regexp"
	"strings"
)

// CyclomaticComplexityCalculator estimates cyclomatic complexity using regex-based
// branch counting. The baseline complexity is 1; each branch keyword adds 1.
type CyclomaticComplexityCalculator struct{}

// language-specific branch patterns
var branchPatterns = map[string][]*regexp.Regexp{
	"go": {
		regexp.MustCompile(`\bif\b`),
		regexp.MustCompile(`\belse\b`),
		regexp.MustCompile(`\bcase\b`),
		regexp.MustCompile(`\bfor\b`),
		regexp.MustCompile(`\brange\b`),
		regexp.MustCompile(`&&`),
		regexp.MustCompile(`\|\|`),
		regexp.MustCompile(`\bgo\b`),
		regexp.MustCompile(`\bselect\b`),
	},
	"python": {
		regexp.MustCompile(`\bif\b`),
		regexp.MustCompile(`\belif\b`),
		regexp.MustCompile(`\belse\b`),
		regexp.MustCompile(`\bfor\b`),
		regexp.MustCompile(`\bwhile\b`),
		regexp.MustCompile(`\bexcept\b`),
		regexp.MustCompile(`\band\b`),
		regexp.MustCompile(`\bor\b`),
		regexp.MustCompile(`\bwith\b`),
	},
	"typescript": {
		regexp.MustCompile(`\bif\b`),
		regexp.MustCompile(`\belse\b`),
		regexp.MustCompile(`\bcase\b`),
		regexp.MustCompile(`\bfor\b`),
		regexp.MustCompile(`\bwhile\b`),
		regexp.MustCompile(`\bdo\b`),
		regexp.MustCompile(`\bcatch\b`),
		regexp.MustCompile(`&&`),
		regexp.MustCompile(`\|\|`),
		regexp.MustCompile(`\?\?`),
	},
	"java": {
		regexp.MustCompile(`\bif\b`),
		regexp.MustCompile(`\belse\b`),
		regexp.MustCompile(`\bcase\b`),
		regexp.MustCompile(`\bfor\b`),
		regexp.MustCompile(`\bwhile\b`),
		regexp.MustCompile(`\bdo\b`),
		regexp.MustCompile(`\bcatch\b`),
		regexp.MustCompile(`&&`),
		regexp.MustCompile(`\|\|`),
	},
}

func init() {
	// JavaScript shares the same branch patterns as TypeScript.
	branchPatterns["javascript"] = branchPatterns["typescript"]
}

func (c *CyclomaticComplexityCalculator) Calculate(_ string, content []byte, language string) (map[MetricType]float64, error) {
	lang := strings.ToLower(language)
	patterns, ok := branchPatterns[lang]
	if !ok {
		// For unsupported languages, return baseline complexity of 1.
		return map[MetricType]float64{CyclomaticComplexity: 1}, nil
	}

	complexity := 1 // baseline
	text := string(content)
	for _, p := range patterns {
		complexity += len(p.FindAllStringIndex(text, -1))
	}

	return map[MetricType]float64{CyclomaticComplexity: float64(complexity)}, nil
}
