package config

import (
	"strings"
	"testing"
)

// TestExpandValue covers the substitution syntax, which exists so a key can
// stay out of a file that gets committed.
func TestExpandValue(t *testing.T) {
	t.Setenv("CODEEAGLE_TEST_KEY", "apikey_from_env")
	t.Setenv("CODEEAGLE_TEST_EMPTY", "")

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"a plain value is untouched", "sk-literal-value", "sk-literal-value"},
		{"an environment variable", "${CODEEAGLE_TEST_KEY}", "apikey_from_env"},
		{"embedded in a longer value", "Bearer ${CODEEAGLE_TEST_KEY}!", "Bearer apikey_from_env!"},
		{"twice in one value", "${CODEEAGLE_TEST_KEY}/${CODEEAGLE_TEST_KEY}", "apikey_from_env/apikey_from_env"},
		{"an unset variable becomes empty", "${CODEEAGLE_TEST_MISSING}", ""},
		{"a set-but-empty variable takes the fallback", "${CODEEAGLE_TEST_EMPTY:-fallback}", "fallback"},
		{"an unset variable takes the fallback", "${CODEEAGLE_TEST_MISSING:-jev-1.13.0}", "jev-1.13.0"},
		{"a set variable ignores the fallback", "${CODEEAGLE_TEST_KEY:-unused}", "apikey_from_env"},
		{"a command", "$(printf hello)", "hello"},
		{"command output is trimmed", "$(printf 'spaced  \n')", "spaced"},
		{"a command inside a longer value", "prefix-$(printf mid)-suffix", "prefix-mid-suffix"},
		{"both kinds together", "$(printf one)/${CODEEAGLE_TEST_KEY}", "one/apikey_from_env"},
		// A dollar that is not a reference must survive: passwords contain them.
		{"a bare dollar", "pa$$word", "pa$word"},
		{"an escaped reference stays literal", "$${NOT_A_VAR}", "${NOT_A_VAR}"},
		{"a dollar with no braces", "cost is $5", "cost is $5"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandValue(tt.raw, "config.test")
			if err != nil {
				t.Fatalf("expandValue(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("expandValue(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestExpandValueReportsCommandFailure covers a command that does not work.
// Failing loudly beats yielding an empty credential that surfaces later as an
// authentication error.
func TestExpandValueReportsCommandFailure(t *testing.T) {
	_, err := expandValue("$(exit 3)", "config.transcripts.api_key")
	if err == nil {
		t.Fatal("a failing command expanded without error")
	}
	if !strings.Contains(err.Error(), "config.transcripts.api_key") {
		t.Errorf("error does not say which setting failed: %v", err)
	}
}

// TestExpandValueKeepsSecretsOutOfErrors covers the one thing this code must
// never do: a failure while resolving a credential must not quote it.
func TestExpandValueKeepsSecretsOutOfErrors(t *testing.T) {
	// The command prints a secret to stderr and then fails, which is exactly
	// what a confused credential helper does.
	_, err := expandValue("$(printf 'apikey_supersecret' >&2; exit 1)", "config.key")
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("the error leaked the command's output: %v", err)
	}
}

// TestExpandConfigWalksTheWholeConfig covers expansion reaching nested
// structs, slices and maps rather than a hand-picked list of fields.
func TestExpandConfigWalksTheWholeConfig(t *testing.T) {
	t.Setenv("CODEEAGLE_TEST_ROOT", "/srv/code")
	t.Setenv("CODEEAGLE_TEST_KEY", "apikey_from_env")
	t.Setenv("CODEEAGLE_TEST_MODEL", "jev-1.13.0")

	cfg := &Config{
		Project: ProjectConfig{Name: "${CODEEAGLE_TEST_MODEL}-project"},
		Repositories: []RepositoryConfig{
			{Path: "${CODEEAGLE_TEST_ROOT}/one", Type: "single"},
			{Path: "$(printf /srv/generated)", Type: "single"},
		},
		Watch: WatchConfig{
			Exclude: []string{"${CODEEAGLE_TEST_ROOT}/vendor", "**/node_modules/**"},
		},
		Transcripts: TranscriptsConfig{
			SessionsDir: []string{"${CODEEAGLE_TEST_ROOT}/sessions"},
			APIKey:      "${CODEEAGLE_TEST_KEY}",
		},
	}

	if err := expandConfig(cfg); err != nil {
		t.Fatalf("expandConfig: %v", err)
	}

	checks := []struct{ got, want, where string }{
		{cfg.Project.Name, "jev-1.13.0-project", "project name"},
		{cfg.Repositories[0].Path, "/srv/code/one", "a repository path"},
		{cfg.Repositories[1].Path, "/srv/generated", "a command-expanded path"},
		{cfg.Watch.Exclude[0], "/srv/code/vendor", "an exclude pattern"},
		{cfg.Watch.Exclude[1], "**/node_modules/**", "an untouched exclude pattern"},
		{cfg.Transcripts.SessionsDir[0], "/srv/code/sessions", "a sessions directory"},
		{cfg.Transcripts.APIKey, "apikey_from_env", "the api key"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.where, c.got, c.want)
		}
	}
}

// TestExpandConfigLeavesGlobsAlone covers the patterns a config is full of.
// Expansion must not mangle a value that merely contains punctuation.
func TestExpandConfigLeavesGlobsAlone(t *testing.T) {
	cfg := &Config{
		Watch: WatchConfig{Exclude: []string{
			"**/node_modules/**", "**/.git/**", "**/*.min.js", "$HOME/nope",
		}},
	}
	if err := expandConfig(cfg); err != nil {
		t.Fatalf("expandConfig: %v", err)
	}
	want := []string{"**/node_modules/**", "**/.git/**", "**/*.min.js", "$HOME/nope"}
	for i := range want {
		if cfg.Watch.Exclude[i] != want[i] {
			t.Errorf("exclude[%d] = %q, want %q", i, cfg.Watch.Exclude[i], want[i])
		}
	}
}
