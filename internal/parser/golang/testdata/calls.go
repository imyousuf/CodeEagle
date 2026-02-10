package calls

import (
	"encoding/json"
	"fmt"
	"os"
)

func helper() int {
	return 42
}

func formatOutput(data string) string {
	return fmt.Sprintf("output: %s", data)
}

func processData(input string) {
	result := helper()
	output := formatOutput(input)
	encoded, _ := json.Marshal(result)
	fmt.Println(output, string(encoded))
	os.Exit(0)
}

type Processor struct{}

func (p *Processor) validate(data string) bool {
	return len(data) > 0
}

func (p *Processor) Process(data string) string {
	if p.validate(data) {
		return formatOutput(data)
	}
	return ""
}
