package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// expandCommandTimeout bounds one `$(...)` substitution. A keyring lookup can
// block on a locked keychain, and a hang with no explanation is worse than a
// clear failure.
const expandCommandTimeout = 30 * time.Second

// Expansion syntax recognized in configuration values.
//
//	${NAME}             the environment variable NAME, empty if unset
//	${NAME:-fallback}   the environment variable NAME, or fallback if unset
//	$(command)          the trimmed standard output of command
//	$$                  a literal dollar sign
//
// This keeps credentials out of the config file without giving every setting
// its own bespoke `_env` and `_command` companions. A key can be read from the
// environment a wrapper script set up, or straight out of the system keyring:
//
//	jev_api_key: ${JEV_API_KEY}
//	jev_api_key: $(keyring get typesafe.ai me@example.com)
//
// Running a command named in a config file is a real capability, and an
// intentional one: the file belongs to the person running the tool, and this
// is the same bargain git credential helpers and Docker's credential store
// make. Nothing runs unless they wrote a `$(...)` themselves.
var (
	// envPattern matches ${NAME} and ${NAME:-fallback}.
	envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)
	// cmdPattern matches $(command). Commands do not nest.
	cmdPattern = regexp.MustCompile(`\$\(([^)]+)\)`)
	// escapedDollar stands in for $$ while expanding, so that a literal
	// dollar cannot be re-read as the start of a reference.
	escapedDollar = "\x00codeeagle-dollar\x00"
)

// expandValue resolves the references in one configuration value.
//
// The expanded text is never included in an error, because the whole point of
// the syntax is to carry secrets.
func expandValue(raw, where string) (string, error) {
	if !strings.Contains(raw, "$") {
		return raw, nil
	}
	out := strings.ReplaceAll(raw, "$$", escapedDollar)

	var failure error
	out = cmdPattern.ReplaceAllStringFunc(out, func(match string) string {
		if failure != nil {
			return ""
		}
		command := cmdPattern.FindStringSubmatch(match)[1]
		value, err := runExpansion(command)
		if err != nil {
			failure = fmt.Errorf("%s: %w", where, err)
			return ""
		}
		return value
	})
	if failure != nil {
		return "", failure
	}

	out = envPattern.ReplaceAllStringFunc(out, func(match string) string {
		parts := envPattern.FindStringSubmatch(match)
		name, fallback := parts[1], parts[2]
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return value
		}
		return fallback
	})

	return strings.ReplaceAll(out, escapedDollar, "$"), nil
}

// runExpansion executes a command and returns its trimmed output.
func runExpansion(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), expandCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	// Discarded deliberately: a failing credential helper often prints the
	// thing it was asked to fetch, and an error message is the last place it
	// should end up.
	cmd.Stderr = nil

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after %s", expandCommandTimeout)
		}
		return "", fmt.Errorf("command failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// expandConfig walks a configuration and expands every string it holds.
//
// Applied after loading rather than to the file's text, so a value is expanded
// exactly once and wherever it came from — file, environment or flag.
func expandConfig(cfg *Config) error {
	cfg.resolved = make(map[string]resolvedValue)
	return walkStrings(reflect.ValueOf(cfg), "config", func(path, value string) (string, error) {
		out, err := expandValue(value, path)
		if err != nil {
			return "", err
		}
		if out != value {
			cfg.resolved[path] = resolvedValue{expression: value, resolved: out}
		}
		return out, nil
	})
}

// resolvedValue pairs a reference with what it resolved to.
type resolvedValue struct {
	expression string // what the file said, e.g. $(keyring get ...)
	resolved   string // what it produced, e.g. the key itself
}

// unexpandForWrite swaps resolved values back to the expressions that
// produced them, and returns a function restoring what it changed.
//
// A value the caller has since edited is left alone: the edit is what they
// asked to save, and it is no longer the secret that was fetched. Only a
// value still identical to what expansion produced is turned back into its
// reference.
func (c *Config) unexpandForWrite() func() {
	if len(c.resolved) == 0 {
		return func() {}
	}
	changed := make(map[string]string, len(c.resolved))
	_ = walkStrings(reflect.ValueOf(c), "config", func(path, value string) (string, error) {
		rv, ok := c.resolved[path]
		if !ok || value != rv.resolved {
			return value, nil
		}
		changed[path] = value
		return rv.expression, nil
	})
	return func() {
		_ = walkStrings(reflect.ValueOf(c), "config", func(path, value string) (string, error) {
			if restored, ok := changed[path]; ok {
				return restored, nil
			}
			return value, nil
		})
	}
}

// stringVisitor transforms one configuration value in the walk. Returning the
// value unchanged leaves it alone.
type stringVisitor func(path, value string) (string, error)

// walkStrings rewrites, in place, every string reachable from a value.
func walkStrings(v reflect.Value, path string, visit stringVisitor) error {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return walkStrings(v.Elem(), path, visit)

	case reflect.Struct:
		t := v.Type()
		for i := range v.NumField() {
			if !t.Field(i).IsExported() {
				continue
			}
			name := fieldName(t.Field(i))
			if err := walkStrings(v.Field(i), path+"."+name, visit); err != nil {
				return err
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := walkStrings(v.Index(i), fmt.Sprintf("%s[%d]", path, i), visit); err != nil {
				return err
			}
		}

	case reflect.Map:
		for _, key := range v.MapKeys() {
			entry := v.MapIndex(key)
			if entry.Kind() != reflect.String {
				// A map of structs cannot be addressed in place, so it is
				// copied, expanded and written back.
				copied := reflect.New(entry.Type()).Elem()
				copied.Set(entry)
				if err := walkStrings(copied, fmt.Sprintf("%s[%v]", path, key), visit); err != nil {
					return err
				}
				v.SetMapIndex(key, copied)
				continue
			}
			next, err := visit(fmt.Sprintf("%s[%v]", path, key), entry.String())
			if err != nil {
				return err
			}
			v.SetMapIndex(key, reflect.ValueOf(next))
		}

	case reflect.String:
		if !v.CanSet() {
			return nil
		}
		next, err := visit(path, v.String())
		if err != nil {
			return err
		}
		v.SetString(next)
	}
	return nil
}

// fieldName prefers the name the config file uses, so an error points at
// something the reader can find in their own file.
func fieldName(f reflect.StructField) string {
	if tag := f.Tag.Get("mapstructure"); tag != "" {
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			return name
		}
	}
	return f.Name
}
