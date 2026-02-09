package metrics

import (
	"regexp"
)

// TodoCounter scans file content for TODO, FIXME, HACK, and XXX markers.
type TodoCounter struct{}

var (
	todoPattern  = regexp.MustCompile(`(?i)\bTODO\b`)
	fixmePattern = regexp.MustCompile(`(?i)\bFIXME\b`)
	hackPattern  = regexp.MustCompile(`(?i)\bHACK\b`)
)

func (c *TodoCounter) Calculate(_ string, content []byte, _ string) (map[MetricType]float64, error) {
	text := string(content)
	return map[MetricType]float64{
		TodoCount:  float64(len(todoPattern.FindAllStringIndex(text, -1))),
		FixmeCount: float64(len(fixmePattern.FindAllStringIndex(text, -1))),
		HackCount:  float64(len(hackPattern.FindAllStringIndex(text, -1))),
	}, nil
}
