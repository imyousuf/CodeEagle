// Package service installs the CodeEagle worker so it starts when the user
// logs in.
//
// Everything here is user-scoped: a systemd *user* unit on Linux, a launchd
// *LaunchAgent* on macOS. Nothing needs root, and nothing runs for other
// people on the machine.
//
// Starting at login rather than at boot is the point, not a limitation. The
// worker runs syncs that read credentials out of the login keyring -- config
// values like `$(keyring get baseten.co you@example.com)`. A service started
// before anyone logs in has no unlocked keyring to read, and CodeEagle
// deliberately stops rather than continue with an empty credential. Tying the
// worker to the login session means the keyring is already unlocked by the
// time it runs.
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Label is the service identifier: the unit name on Linux, the launchd label
// on macOS.
const (
	SystemdUnitName = "codeeagle-worker.service"
	LaunchdLabel    = "com.github.imyousuf.codeeagle.worker"
)

// Config describes the service to install.
type Config struct {
	// Binary is the codeeagle executable the service runs. Empty means this
	// executable, resolved through any symlink.
	Binary string
	// Args are the worker arguments, e.g. ["worker"].
	Args []string
	// Home overrides the user's home directory. For tests.
	Home string
	// UID is used to build launchd's domain target. Zero means the current
	// user. For tests.
	UID int
}

// Plan is what an install would do, so it can be shown before it is done.
type Plan struct {
	// Path is the unit or plist file that would be written.
	Path string
	// Contents is what would be written to it.
	Contents string
	// Activate are the commands run after writing, in order.
	Activate [][]string
	// Deactivate are the commands that undo it.
	Deactivate [][]string
	// Platform names the mechanism, for messages.
	Platform string
}

// Supported reports whether this operating system has an installer here.
func Supported() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

// Build returns the plan for this operating system.
func Build(cfg Config) (*Plan, error) {
	if err := cfg.resolve(); err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "linux":
		return systemdPlan(cfg), nil
	case "darwin":
		return launchdPlan(cfg), nil
	default:
		return nil, fmt.Errorf("no user-scope service installer for %s; "+
			"run `codeeagle worker` yourself, or supervise it however this system does", runtime.GOOS)
	}
}

func (c *Config) resolve() error {
	if c.Binary == "" {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate this executable: %w", err)
		}
		// Through the symlink: a service should point at the real file, not
		// at something that may be repointed later.
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		c.Binary = self
	}
	if c.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("locate the home directory: %w", err)
		}
		c.Home = home
	}
	if len(c.Args) == 0 {
		c.Args = []string{"worker"}
	}
	if c.UID == 0 {
		c.UID = os.Getuid()
	}
	return nil
}

// systemdPlan builds a systemd user unit.
func systemdPlan(cfg Config) *Plan {
	path := filepath.Join(cfg.Home, ".config", "systemd", "user", SystemdUnitName)

	// StartLimit* belong in [Unit], not [Service]. Put in the wrong section
	// they are silently ignored, and a service that cannot start spins.
	contents := fmt.Sprintf(`[Unit]
Description=CodeEagle worker: keep every registered project indexed
Documentation=https://github.com/imyousuf/CodeEagle
After=network-online.target
Wants=network-online.target
# A worker that cannot start must not respawn forever.
StartLimitIntervalSec=600
StartLimitBurst=5

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=60
# Indexing is background work: it should lose to anything interactive.
Nice=10
IOSchedulingClass=idle

[Install]
# default.target, not graphical-session.target: this should run for a
# console login too, and it stops when the user's last session ends.
WantedBy=default.target
`, shellJoin(append([]string{cfg.Binary}, cfg.Args...)), cfg.Home)

	return &Plan{
		Path:     path,
		Contents: contents,
		Platform: "systemd user unit",
		Activate: [][]string{
			{"systemctl", "--user", "daemon-reload"},
			{"systemctl", "--user", "enable", "--now", SystemdUnitName},
		},
		Deactivate: [][]string{
			{"systemctl", "--user", "disable", "--now", SystemdUnitName},
			{"systemctl", "--user", "daemon-reload"},
		},
	}
}

// launchdPlan builds a launchd LaunchAgent.
func launchdPlan(cfg Config) *Plan {
	path := filepath.Join(cfg.Home, "Library", "LaunchAgents", LaunchdLabel+".plist")

	var args strings.Builder
	for _, a := range append([]string{cfg.Binary}, cfg.Args...) {
		fmt.Fprintf(&args, "\n\t\t<string>%s</string>", xmlEscape(a))
	}

	// KeepAlive on SuccessfulExit=false restarts a crash but respects a
	// clean exit, so stopping the worker by hand does not fight launchd.
	contents := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>WorkingDirectory</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, LaunchdLabel, args.String(), xmlEscape(cfg.Home),
		xmlEscape(filepath.Join(cfg.Home, "Library", "Logs", "codeeagle-worker.log")),
		xmlEscape(filepath.Join(cfg.Home, "Library", "Logs", "codeeagle-worker.log")))

	domain := fmt.Sprintf("gui/%d", cfg.UID)
	return &Plan{
		Path:     path,
		Contents: contents,
		Platform: "launchd LaunchAgent",
		Activate: [][]string{
			{"launchctl", "bootstrap", domain, path},
		},
		Deactivate: [][]string{
			{"launchctl", "bootout", domain + "/" + LaunchdLabel},
		},
	}
}

// shellJoin quotes arguments for an ExecStart line, which systemd parses
// itself rather than handing to a shell.
func shellJoin(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"'\\") {
			a = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
	).Replace(s)
}
