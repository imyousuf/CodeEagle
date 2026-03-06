package cli

import (
	"testing"
)

func TestNewQueueCmdStructure(t *testing.T) {
	cmd := newQueueCmd()

	if cmd.Use != "queue" {
		t.Errorf("Use = %q, want %q", cmd.Use, "queue")
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}

	// Should have status and purge subcommands.
	subs := cmd.Commands()
	if len(subs) != 2 {
		t.Fatalf("subcommands = %d, want 2", len(subs))
	}

	names := make(map[string]bool)
	for _, sub := range subs {
		names[sub.Use] = true
	}
	for _, want := range []string{"status", "purge"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestNewQueueStatusCmd(t *testing.T) {
	cmd := newQueueStatusCmd()
	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
}

func TestNewQueuePurgeCmd(t *testing.T) {
	cmd := newQueuePurgeCmd()
	if cmd.Use != "purge" {
		t.Errorf("Use = %q, want %q", cmd.Use, "purge")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
}
