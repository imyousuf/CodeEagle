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
	if !strings.Contains(all, "enable --now") {
		t.Error("the unit is not enabled for login and started now")
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
