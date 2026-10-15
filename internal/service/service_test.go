package service

import (
	"encoding/xml"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		Binary: "/usr/local/bin/codeeagle",
		Args:   []string{"worker"},
		Home:   "/home/u",
		UID:    501,
	}
}

func TestSystemdUnitIsCorrectlyShaped(t *testing.T) {
	p := systemdPlan(testConfig())

	if want := "/home/u/.config/systemd/user/" + SystemdUnitName; p.Path != want {
		t.Errorf("Path = %q, want %q", p.Path, want)
	}

	for _, want := range []string{
		"ExecStart=/usr/local/bin/codeeagle worker",
		"WantedBy=default.target",
		"Restart=on-failure",
		"WorkingDirectory=/home/u",
	} {
		if !strings.Contains(p.Contents, want) {
			t.Errorf("unit is missing %q", want)
		}
	}

	// StartLimit* are [Unit] directives. In [Service] they are silently
	// ignored and a failing service spins.
	unitSection := p.Contents[strings.Index(p.Contents, "[Unit]"):strings.Index(p.Contents, "[Service]")]
	for _, want := range []string{"StartLimitIntervalSec", "StartLimitBurst"} {
		if !strings.Contains(unitSection, want) {
			t.Errorf("%s is not in the [Unit] section, where systemd reads it", want)
		}
	}
}

func TestSystemdActivationEnablesAndStarts(t *testing.T) {
	p := systemdPlan(testConfig())

	joined := make([]string, 0, len(p.Activate))
	for _, c := range p.Activate {
		joined = append(joined, strings.Join(c, " "))
	}
	all := strings.Join(joined, "; ")

	// daemon-reload must precede enable, or systemd acts on a stale unit.
	if !strings.Contains(all, "daemon-reload") {
		t.Error("no daemon-reload before enabling")
	}
	if !strings.Contains(all, "enable "+SystemdUnitName) {
		t.Error("the unit is not enabled for login")
	}
	// A reinstall must replace a running worker, or it keeps the binary and
	// PATH it started with; `enable --now` does not touch a running unit.
	if !strings.Contains(all, "restart "+SystemdUnitName) {
		t.Error("a running worker is not restarted onto the new unit")
	}
	if strings.Index(all, "restart") < strings.Index(all, "daemon-reload") {
		t.Error("restart precedes daemon-reload and would act on the stale unit")
	}
	if strings.Contains(all, "sudo") || strings.Contains(all, "--system") {
		t.Errorf("activation leaves user scope: %s", all)
	}
}

func TestLaunchdPlistIsValidXMLAndCorrectlyShaped(t *testing.T) {
	p := launchdPlan(testConfig())

	if want := "/home/u/Library/LaunchAgents/" + LaunchdLabel + ".plist"; p.Path != want {
		t.Errorf("Path = %q, want %q", p.Path, want)
	}

	// A malformed plist is rejected by launchd at load time with a message
	// that does not say what is wrong, so check it parses here.
	var anything any
	if err := xml.Unmarshal([]byte(p.Contents), &anything); err != nil {
		t.Fatalf("plist is not well-formed XML: %v", err)
	}

	for _, want := range []string{
		"<string>" + LaunchdLabel + "</string>",
		"<string>/usr/local/bin/codeeagle</string>",
		"<string>worker</string>",
		"<key>RunAtLoad</key>",
	} {
		if !strings.Contains(p.Contents, want) {
			t.Errorf("plist is missing %q", want)
		}
	}
}

func TestLaunchdUsesTheCallersGuiDomain(t *testing.T) {
	p := launchdPlan(testConfig())

	act := strings.Join(p.Activate[0], " ")
	if !strings.Contains(act, "gui/501") {
		t.Errorf("activation = %q, want the gui/<uid> domain for a LaunchAgent", act)
	}
	deact := strings.Join(p.Deactivate[0], " ")
	if !strings.Contains(deact, "gui/501/"+LaunchdLabel) {
		t.Errorf("deactivation = %q, want bootout of the labelled service", deact)
	}
}

// TestPathsWithSpacesSurvive covers a home directory or install path with a
// space, which systemd and XML each mishandle differently if unescaped.
func TestPathsWithSpacesSurvive(t *testing.T) {
	cfg := Config{
		Binary: "/Users/a b/bin/codeeagle",
		Args:   []string{"worker"},
		Home:   "/Users/a b",
		UID:    501,
	}

	unit := systemdPlan(cfg).Contents
	if !strings.Contains(unit, `ExecStart="/Users/a b/bin/codeeagle" worker`) {
		t.Errorf("a path with a space is not quoted for systemd:\n%s", unit)
	}

	plist := launchdPlan(cfg).Contents
	var anything any
	if err := xml.Unmarshal([]byte(plist), &anything); err != nil {
		t.Fatalf("plist with a spaced path is malformed: %v", err)
	}
}

func TestXMLSpecialCharactersAreEscaped(t *testing.T) {
	cfg := testConfig()
	cfg.Home = `/home/a&b<c>`
	plist := launchdPlan(cfg).Contents

	if strings.Contains(plist, "a&b") && !strings.Contains(plist, "a&amp;b") {
		t.Error("an ampersand in a path was not escaped; the plist will not parse")
	}
	var anything any
	if err := xml.Unmarshal([]byte(plist), &anything); err != nil {
		t.Fatalf("plist is malformed with special characters: %v", err)
	}
}

func TestBuildFillsInDefaults(t *testing.T) {
	if !Supported() {
		t.Skip("no installer for this platform")
	}
	p, err := Build(Config{Home: "/home/u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Contents == "" || p.Path == "" {
		t.Error("Build returned an empty plan")
	}
	if !strings.Contains(p.Contents, "worker") {
		t.Error("the default arguments do not run the worker")
	}
}

// TestInstallTimePathReachesTheService covers the failure this exists for: a
// unit started at login got the service manager's bare PATH, every
// $(keyring get ...) in a config exited 127, and every sync failed while the
// same command worked in a terminal.
func TestInstallTimePathReachesTheService(t *testing.T) {
	cfg := testConfig()
	cfg.Path = "/home/u/.local/bin:/usr/bin"

	unit := systemdPlan(cfg).Contents
	if !strings.Contains(unit, `Environment="PATH=/home/u/.local/bin:/usr/bin"`) {
		t.Errorf("unit does not carry the install-time PATH:\n%s", unit)
	}
	// Environment= is a [Service] directive; elsewhere it is ignored.
	if strings.Index(unit, "Environment=") < strings.Index(unit, "[Service]") {
		t.Error("Environment= is not in the [Service] section")
	}

	plist := launchdPlan(cfg).Contents
	var anything any
	if err := xml.Unmarshal([]byte(plist), &anything); err != nil {
		t.Fatalf("plist with a PATH is malformed: %v", err)
	}
	want := "<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n" +
		"\t\t<string>/home/u/.local/bin:/usr/bin</string>"
	if !strings.Contains(plist, want) {
		t.Errorf("plist does not carry the install-time PATH:\n%s", plist)
	}
}

func TestEmptyPathIsLeftToTheServiceManager(t *testing.T) {
	cfg := testConfig()
	cfg.Path = ""

	if unit := systemdPlan(cfg).Contents; strings.Contains(unit, "Environment=") {
		t.Errorf("an empty PATH was written into the unit, hiding every command:\n%s", unit)
	}
	if plist := launchdPlan(cfg).Contents; strings.Contains(plist, "EnvironmentVariables") {
		t.Errorf("an empty PATH was written into the plist:\n%s", plist)
	}
}

// TestSystemdEnvironmentIsEscaped covers characters systemd treats specially
// in an Environment= value: % starts a specifier, and quotes and backslashes
// end or escape the quoted string.
func TestSystemdEnvironmentIsEscaped(t *testing.T) {
	cfg := testConfig()
	cfg.Path = `/opt/100%/bin:/opt/a "b"\c`

	unit := systemdPlan(cfg).Contents
	if want := `Environment="PATH=/opt/100%%/bin:/opt/a \"b\"\\c"`; !strings.Contains(unit, want) {
		t.Errorf("unit = \n%s\nwant it to contain %s", unit, want)
	}
}

func TestAbsolutePathDropsWhatAServiceCannotUse(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"unchanged", "/a:/b", "/a:/b"},
		{"empty entries are the working directory", "/a::/b:", "/a:/b"},
		{"relative entries", "/a:.:bin:/b", "/a:/b"},
		{"first of a repeat wins", "/a:/b:/a", "/a:/b"},
		{"nothing usable", ".:bin", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := absolutePath(tt.in); got != tt.want {
				t.Errorf("absolutePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildDefaultsToThisProcessPath(t *testing.T) {
	if !Supported() {
		t.Skip("no installer for this platform")
	}
	t.Setenv("PATH", "/opt/helpers/bin:/usr/bin")
	p, err := Build(Config{Home: "/home/u"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(p.Contents, "/opt/helpers/bin:/usr/bin") {
		t.Errorf("the service does not get the installing shell's PATH:\n%s", p.Contents)
	}
}
