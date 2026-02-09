package config

import (
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const registryFileName = ".codeeagle.conf"

// ProjectEntry represents a registered project in the global registry.
type ProjectEntry struct {
	Name      string `yaml:"name"`
	Root      string `yaml:"root"`
	ConfigDir string `yaml:"config_dir"`
}

type registryFile struct {
	Projects []ProjectEntry `yaml:"projects"`
}

// RegistryPath returns the path to the global project registry file (~/.codeeagle.conf).
func RegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, registryFileName)
}

// RegisterProject adds or updates a project entry in the global registry.
// If name is empty, it defaults to filepath.Base(root).
func RegisterProject(name, root, configDir string) error {
	if name == "" {
		name = filepath.Base(root)
	}

	entries := ListProjects()

	// Update existing entry or append new one.
	found := false
	for i, entry := range entries {
		if entry.Root == root {
			entries[i].Name = name
			entries[i].ConfigDir = configDir
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, ProjectEntry{
			Name:      name,
			Root:      root,
			ConfigDir: configDir,
		})
	}

	return writeRegistry(entries)
}

// LookupProject finds a registry entry whose Root matches or is a parent of the given path.
func LookupProject(path string) (*ProjectEntry, bool) {
	entries := ListProjects()
	// Normalize the lookup path.
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	for _, entry := range entries {
		entryRoot, err := filepath.Abs(entry.Root)
		if err != nil {
			entryRoot = entry.Root
		}
		if absPath == entryRoot || strings.HasPrefix(absPath, entryRoot+string(filepath.Separator)) {
			return &entry, true
		}
	}
	return nil, false
}

// ListProjects returns all registered projects from the global registry.
func ListProjects() []ProjectEntry {
	regPath := RegistryPath()
	if regPath == "" {
		return nil
	}

	data, err := os.ReadFile(regPath)
	if err != nil {
		return nil
	}

	var reg registryFile
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil
	}

	return reg.Projects
}

func writeRegistry(entries []ProjectEntry) error {
	regPath := RegistryPath()
	if regPath == "" {
		return nil
	}

	reg := registryFile{Projects: entries}
	data, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}

	return os.WriteFile(regPath, data, 0644)
}
