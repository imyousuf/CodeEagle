package worker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// helperDirEnv marks a copy of this test binary that stands in for a running
// worker, so the test can replace its executable underneath it.
const helperDirEnv = "CODEEAGLE_WORKER_EXEC_HELPER"

// TestExecRunnerSurvivesUpgradeInPlace replays what happened to a running
// worker: a sync it started auto-updated codeeagle, and every later sync
// failed because the worker went looking for a codeeagle.old that the updater
// had already deleted. The worker has to be a real process here, because what
// went wrong was os.Executable following the process's own replaced binary.
func TestExecRunnerSurvivesUpgradeInPlace(t *testing.T) {
	if dir := os.Getenv(helperDirEnv); dir != "" {
		actAsWorker(dir)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("replaces a running executable, which Windows does not allow")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "codeeagle")
	copyExecutable(t, os.Args[0], bin)

	w := exec.Command(bin, "-test.run=^TestExecRunnerSurvivesUpgradeInPlace$")
	w.Env = append(os.Environ(), helperDirEnv+"="+dir)
	var stderr strings.Builder
	w.Stderr = &stderr
	stdin, err := w.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := w.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	out := bufio.NewReader(pipe)
	if line, err := out.ReadString('\n'); line != "ready\n" {
		t.Fatalf("worker did not start: %q %v\n%s", line, err, stderr.String())
	}

	// What the updater does: set the running binary aside, install the new
	// one in its place, delete the old. The new build records that it ran.
	if err := os.Rename(bin, bin+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch synced\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bin + ".old"); err != nil {
		t.Fatal(err)
	}

	_, _ = io.WriteString(stdin, "sync\n")
	_ = stdin.Close()
	rest, _ := io.ReadAll(out)
	if err := w.Wait(); err != nil {
		t.Fatalf("sync after the upgrade failed: %v\n%s%s", err, rest, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "synced")); err != nil {
		t.Fatalf("the upgraded binary never ran: %v", err)
	}
}

// actAsWorker locates itself the way the worker does at startup, waits while
// the test upgrades it, then runs a sync.
func actAsWorker(dir string) {
	r, err := NewExecRunner(nil, nil)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')

	if err := r.Sync(context.Background(), Project{Name: "upgraded", Root: dir, ConfigDir: dir}); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Exit(0)
}

func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
}
