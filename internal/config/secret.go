package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// secretCommandTimeout bounds a credential helper. A keyring lookup may block
// on a locked keychain, and an indefinite hang is worse than a clear failure.
const secretCommandTimeout = 30 * time.Second

// SecretSource describes the ways a credential may be supplied, in the order
// they are consulted.
type SecretSource struct {
	// Literal is a value written directly in the config file. It is checked
	// first but is the least advisable: config files get committed.
	Literal string
	// EnvVar names an environment variable to read.
	EnvVar string
	// Command is a shell command whose standard output is the secret, letting
	// the credential live in the system keyring rather than on disk. For
	// example: "keyring get baseten.co me@example.com".
	Command string
}

// ResolveSecret returns the first credential the source yields.
//
// Running a command from configuration is a real capability, so it is opt-in:
// nothing runs unless the user put a Command in their own config file. This
// follows the convention set by git credential helpers and Docker's
// credential store, and it is what lets a key stay in the OS keyring instead
// of being pasted into a YAML file.
func ResolveSecret(src SecretSource) (string, error) {
	if v := strings.TrimSpace(src.Literal); v != "" {
		return v, nil
	}
	if src.EnvVar != "" {
		if v := strings.TrimSpace(os.Getenv(src.EnvVar)); v != "" {
			return v, nil
		}
	}
	if src.Command == "" {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), secretCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", src.Command)
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("credential command timed out after %s", secretCommandTimeout)
		}
		// The command's own output may contain the secret, so report only
		// that it failed rather than echoing what it printed.
		return "", fmt.Errorf("credential command failed: %w", err)
	}

	secret := strings.TrimSpace(string(out))
	if secret == "" {
		return "", fmt.Errorf("credential command produced no output")
	}
	return secret, nil
}
