package config

import (
	"os"

	"go.yaml.in/yaml/v3"
)

// WriteConfig serializes the given Config to YAML and writes it to path.
func WriteConfig(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	content := "# CodeEagle configuration\n" + string(data)
	return os.WriteFile(path, []byte(content), 0644)
}
