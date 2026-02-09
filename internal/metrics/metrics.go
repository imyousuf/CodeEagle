// Package metrics provides code quality metric calculators for source files.
package metrics

// MetricType identifies a specific code quality metric.
type MetricType string

const (
	CyclomaticComplexity MetricType = "cyclomatic_complexity"
	LinesOfCode          MetricType = "lines_of_code"
	BlankLines           MetricType = "blank_lines"
	CommentLines         MetricType = "comment_lines"
	CodeLines            MetricType = "code_lines"
	TodoCount            MetricType = "todo_count"
	FixmeCount           MetricType = "fixme_count"
	HackCount            MetricType = "hack_count"
)

// Calculator computes metrics for a given file.
type Calculator interface {
	// Calculate returns metric values for the given file content and language.
	Calculate(filePath string, content []byte, language string) (map[MetricType]float64, error)
}

// CompositeCalculator runs multiple calculators and merges their results.
type CompositeCalculator struct {
	calculators []Calculator
}

// NewCompositeCalculator creates a CompositeCalculator with all built-in calculators.
func NewCompositeCalculator() *CompositeCalculator {
	return &CompositeCalculator{
		calculators: []Calculator{
			&CyclomaticComplexityCalculator{},
			&LinesOfCodeCalculator{},
			&TodoCounter{},
		},
	}
}

// Calculate runs all calculators and merges results into a single map.
func (c *CompositeCalculator) Calculate(filePath string, content []byte, language string) (map[MetricType]float64, error) {
	result := make(map[MetricType]float64)
	for _, calc := range c.calculators {
		m, err := calc.Calculate(filePath, content, language)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			result[k] = v
		}
	}
	return result, nil
}
