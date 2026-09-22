package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Install writes the unit or plist and activates it.
//
// Writing is idempotent: an existing file is replaced, and activation is safe
// to repeat, so re-running after an upgrade repoints the service at the new
// binary.
func Install(ctx context.Context, plan *Plan, out io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(plan.Path), err)
	}
	if err := os.WriteFile(plan.Path, []byte(plan.Contents), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", plan.Path, err)
	}
	fmt.Fprintf(out, "Wrote %s\n", plan.Path)

	// launchd refuses to bootstrap a label that is already loaded, which is
	// the normal case on a reinstall rather than an error. Unload first and
	// ignore the outcome.
	if runtime.GOOS == "darwin" {
		for _, cmd := range plan.Deactivate {
			_ = exec.CommandContext(ctx, cmd[0], cmd[1:]...).Run()
		}
	}

	for _, cmd := range plan.Activate {
		if err := run(ctx, cmd, out); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall deactivates the service and removes its file.
func Uninstall(ctx context.Context, plan *Plan, out io.Writer) error {
	// Deactivation failing is not fatal: the service may already be stopped,
	// or never have been installed, and the file should still go.
	for _, cmd := range plan.Deactivate {
		if err := run(ctx, cmd, out); err != nil {
			fmt.Fprintf(out, "  (ignored: %v)\n", err)
		}
	}

	if err := os.Remove(plan.Path); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "No %s to remove\n", plan.Path)
			return nil
		}
		return fmt.Errorf("remove %s: %w", plan.Path, err)
	}
	fmt.Fprintf(out, "Removed %s\n", plan.Path)
	return nil
}

// Status describes what is installed and whether it is running.
type Status struct {
	Installed bool
	Path      string
	Running   bool
	Detail    string
}

// Query inspects the installed service.
func Query(ctx context.Context, plan *Plan) Status {
	st := Status{Path: plan.Path}
	if _, err := os.Stat(plan.Path); err == nil {
		st.Installed = true
	}

	switch runtime.GOOS {
	case "linux":
		out, _ := exec.CommandContext(ctx,
			"systemctl", "--user", "is-active", SystemdUnitName).Output()
		st.Detail = strings.TrimSpace(string(out))
		st.Running = st.Detail == "active"
	case "darwin":
		out, _ := exec.CommandContext(ctx,
			"launchctl", "list", LaunchdLabel).Output()
		st.Detail = strings.TrimSpace(string(out))
		// `launchctl list <label>` prints a dictionary when loaded and fails
		// when it is not.
		st.Running = st.Detail != ""
	}
	return st
}

func run(ctx context.Context, cmd []string, out io.Writer) error {
	fmt.Fprintf(out, "  %s\n", strings.Join(cmd, " "))
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	combined, err := c.CombinedOutput()
	if len(combined) > 0 {
		fmt.Fprintf(out, "    %s\n", strings.TrimSpace(string(combined)))
	}
	if err != nil {
		return fmt.Errorf("%s: %w", strings.Join(cmd, " "), err)
	}
	return nil
}
