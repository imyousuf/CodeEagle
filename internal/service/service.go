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
//
// Being started by the service manager also means not inheriting the login
// shell's PATH, and a credential helper like `keyring` usually lives somewhere
// only that PATH reaches, such as ~/.local/bin. So the PATH of the shell that
// ran the install is written into the service. Relying on the session to
// export its PATH to the service manager is a race: systemd starts
// default.target units before a desktop session pushes its environment, a
// running process never sees that environment change, and a console login
// never pushes one at all.
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
	// Path is the PATH the worker, and every sync it starts, runs with.
	// Empty means this process's PATH: the shell where `codeeagle sync`
	// was shown to work is the one whose PATH the service should have.
	Path string
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
	if c.Path == "" {
		c.Path = os.Getenv("PATH")
	}
	c.Path = absolutePath(c.Path)
	return nil
}

// absolutePath drops the entries of a PATH that are not absolute, keeping the
// order and the first of any repeat. An empty or relative entry is searched
// relative to the working directory, which for a service is not anywhere a
// command should be found.
func absolutePath(path string) string {
	seen := make(map[string]bool)
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) || seen[dir] {
			continue
		}
		seen[dir] = true
		keep = append(keep, dir)
	}
	return strings.Join(keep, string(filepath.ListSeparator))
}

// systemdPlan builds a systemd user unit.
func systemdPlan(cfg Config) *Plan {
	path := filepath.Join(cfg.Home, ".config", "systemd", "user", SystemdUnitName)

	var env string
	if cfg.Path != "" {
		env = "# The PATH of the shell that installed this, which config values like\n" +
			"# $(keyring get ...) need. Re-run `codeeagle service install` after\n" +
			"# changing it.\n" +
			"Environment=" + systemdQuote("PATH="+cfg.Path) + "\n"
	}

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
%sRestart=on-failure
RestartSec=60
# Indexing is background work: it should lose to anything interactive.
Nice=10
IOSchedulingClass=idle

[Install]
# default.target, not graphical-session.target: this should run for a
# console login too, and it stops when the user's last session ends.
WantedBy=default.target
`, shellJoin(append([]string{cfg.Binary}, cfg.Args...)), cfg.Home, env)

	return &Plan{
		Path:     path,
		Contents: contents,
		Platform: "systemd user unit",
		// restart rather than `enable --now`, which leaves a running unit
		// alone: a reinstall would rewrite the file while the old process
		// carried on with the old binary and environment. restart also starts
		// a unit that is not running.
		Activate: [][]string{
			{"systemctl", "--user", "daemon-reload"},
			{"systemctl", "--user", "enable", SystemdUnitName},
			{"systemctl", "--user", "restart", SystemdUnitName},
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

	var env string
	if cfg.Path != "" {
		env = fmt.Sprintf("\n\t<key>EnvironmentVariables</key>\n\t<dict>\n"+
			"\t\t<key>PATH</key>\n\t\t<string>%s</string>\n\t</dict>", xmlEscape(cfg.Path))
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
	<string>%s</string>%s
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
`, LaunchdLabel, args.String(), xmlEscape(cfg.Home), env,
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

// systemdQuote quotes one Environment= assignment. Unlike ExecStart, an
// Environment= value is subject to specifier expansion, so a literal % must
// be doubled as well as quotes and backslashes escaped.
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s) + `"`
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
	).Replace(s)
}
