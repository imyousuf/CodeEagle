package config

import (
	"os"

	"go.yaml.in/yaml/v3"
)

// WriteConfig serializes the given Config to YAML and writes it to path.
func WriteConfig(cfg *Config, path string) error {
	// Loading expanded every ${VAR} and $(command) in place, so the struct
	// now holds resolved secrets. Put the references back for the duration of
	// the write, or saving an unrelated setting would silently replace a
	// keyring lookup with the key it returned.
	restore := cfg.unexpandForWrite()
	defer restore()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	content := "# CodeEagle configuration\n" + string(data)
	return os.WriteFile(path, []byte(content), 0644)
}
