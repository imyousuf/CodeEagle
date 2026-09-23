// Package worker watches every CodeEagle project registered on this machine
// and re-syncs each one when its own files change.
//
// The unit of work is a project, not a directory: a sync must run with that
// project's configuration and from its root, because both decide which graph
// is written and how paths are recorded. The worker therefore discovers
// projects from the global registry, watches the directories each one actually
// indexes, and shells out to `codeeagle sync` in the right place.
//
// Shelling out rather than syncing in-process is deliberate. A sync opens a
// BadgerDB, may talk to a model for minutes, and can fail; running it in the
// supervisor would let one project's problem take down the watching of every
// other. A subprocess also inherits the ordinary configuration path, so the
// worker never has to reimplement config resolution.
package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/imyousuf/CodeEagle/internal/config"
)

// Project is one registered project, reduced to what the worker needs.
type Project struct {
	// Name is the registry name, used in logs and in --project.
	Name string
	// Root is the directory a sync must run from.
	Root string
	// ConfigDir is that project's .CodeEagle directory.
	ConfigDir string
	// Repos are the directories this project indexes, absolute and cleaned.
	// These, not Root, are what the worker watches: a project's root may hold
	// far more than it indexes, and the home project is exactly that case.
	Repos []string
	// TranscriptDirs are the directories holding meeting recordings, from
	// transcripts.sessions_dir. They are kept apart from Repos because a
	// change in one means `meetings sync` and a change in the other means
	// `sync`: running the wrong one is either useless or, for a large
	// project, an expensive walk for nothing.
	TranscriptDirs []string
	// Excludes are this project's watch.exclude patterns. They keep build
	// output and dependency trees from waking the worker, and -- because the
	// watcher skips excluded directories when adding watches -- keep a
	// node_modules from consuming thousands of inotify watches.
	Excludes []string
}

// Skipped records a registry entry the worker could not use, so that a silent
// registry problem does not look like a project that simply never changes.
type Skipped struct {
	Name   string
	Reason string
}

// narrowConfig is the slice of a project config the worker reads.
//
// The full loader is deliberately not used. It resolves ${VAR} and $(command)
// references, so loading every project's config merely to learn which
// directories to watch would run each project's credential commands at
// startup -- slow, and a locked keyring would turn a watchable project into a
// startup failure. The sync subprocess loads the real configuration itself,
// in its own working directory, where those commands belong.
type narrowConfig struct {
	Repositories []struct {
		Path string `yaml:"path"`
	} `yaml:"repositories"`
	Watch struct {
		Exclude []string `yaml:"exclude"`
	} `yaml:"watch"`
	Transcripts struct {
		Enabled bool `yaml:"enabled"`
		// sessions_dir is one path or several; a config may name either.
		SessionsDir any `yaml:"sessions_dir"`
	} `yaml:"transcripts"`
}

// sessionDirs reads transcripts.sessions_dir, which may be a single path or a
// list of them.
func (nc narrowConfig) sessionDirs() []string {
	switch v := nc.Transcripts.SessionsDir.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Discover reads the registry and returns the projects worth watching,
// alongside the entries that were skipped and why.
func Discover() ([]Project, []Skipped) {
	entries := config.ListProjects()
	projects := make([]Project, 0, len(entries))
	var skipped []Skipped

	for _, e := range entries {
		p, err := load(e)
		if err != nil {
			skipped = append(skipped, Skipped{Name: e.Name, Reason: err.Error()})
			continue
		}
		projects = append(projects, *p)
	}

	// Longest root first, so an attribution scan finds the most specific
	// project before a more general one that contains it.
	sort.Slice(projects, func(i, j int) bool {
		return len(projects[i].Root) > len(projects[j].Root)
	})
	return projects, skipped
}

func load(e config.ProjectEntry) (*Project, error) {
	if e.ConfigDir == "" {
		return nil, fmt.Errorf("no config_dir in the registry")
	}
	path := filepath.Join(e.ConfigDir, config.ProjectConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var nc narrowConfig
	if err := yaml.Unmarshal(data, &nc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	p := &Project{
		Name:      e.Name,
		Root:      filepath.Clean(e.Root),
		ConfigDir: e.ConfigDir,
		Excludes:  nc.Watch.Exclude,
	}

	for _, r := range nc.Repositories {
		dir := expandHome(strings.TrimSpace(r.Path))
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(p.Root, dir)
		}
		dir = filepath.Clean(dir)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			// A configured directory that is not there is not an error worth
			// refusing the whole project for -- an external drive may be
			// unmounted -- but it cannot be watched.
			continue
		}
		p.Repos = append(p.Repos, dir)
	}

	// Meeting recordings, when this project indexes any. Absent
	// transcripts.enabled the directory is not watched, so a recorder writing
	// there costs nothing.
	if nc.Transcripts.Enabled {
		for _, raw := range nc.sessionDirs() {
			dir := expandHome(strings.TrimSpace(raw))
			if dir == "" {
				continue
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(p.Root, dir)
			}
			dir = filepath.Clean(dir)
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			p.TranscriptDirs = append(p.TranscriptDirs, dir)
		}
		p.TranscriptDirs = dedupeNested(p.TranscriptDirs)
	}

	if len(p.Repos) == 0 && len(p.TranscriptDirs) == 0 {
		return nil, fmt.Errorf("no readable repositories configured")
	}

	// Watching a directory twice would deliver every event twice.
	p.Repos = dedupeNested(p.Repos)
	return p, nil
}

// dedupeNested drops any directory already covered by another in the list,
// since the watch is recursive.
func dedupeNested(dirs []string) []string {
	sort.Strings(dirs)
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		covered := false
		for _, kept := range out {
			if d == kept || strings.HasPrefix(d, kept+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
