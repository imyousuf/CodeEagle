package config

import "testing"

// TestProviderSecretPrefersTheNamedKey covers naming a credential after the
// service it belongs to, so several can sit in one config and changing
// `provider` does not mean moving a key.
func TestProviderSecretPrefersTheNamedKey(t *testing.T) {
	cfg := TranscriptsConfig{
		Provider:        "baseten",
		BasetenAPIKey:   "from-baseten",
		AnthropicAPIKey: "from-anthropic",
		APIKey:          "from-the-generic-fallback",
	}

	tests := []struct {
		provider string
		want     string
	}{
		{"baseten", "from-baseten"},
		{"anthropic", "from-anthropic"},
		// Nothing named for it, so the fallback answers.
		{"ollama", "from-the-generic-fallback"},
		{"vertex-ai", "from-the-generic-fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			cfg.Provider = tt.provider
			got, err := ResolveSecret(cfg.ProviderSecret())
			if err != nil {
				t.Fatalf("ResolveSecret: %v", err)
			}
			if got != tt.want {
				t.Errorf("provider %q resolved %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

// TestProviderSecretFallsBackForOlderConfigs covers a config written before
// the provider-named settings existed, which must keep working untouched.
func TestProviderSecretFallsBackForOlderConfigs(t *testing.T) {
	t.Setenv("CODEEAGLE_TEST_PROVIDER_KEY", "from-the-environment")

	tests := []struct {
		name string
		cfg  TranscriptsConfig
		want string
	}{
		{
			name: "a literal under the old name",
			cfg:  TranscriptsConfig{Provider: "baseten", APIKey: "literal"},
			want: "literal",
		},
		{
			name: "an environment variable under the old name",
			cfg:  TranscriptsConfig{Provider: "baseten", APIKeyEnv: "CODEEAGLE_TEST_PROVIDER_KEY"},
			want: "from-the-environment",
		},
		{
			name: "a command under the old name",
			cfg:  TranscriptsConfig{Provider: "baseten", APIKeyCommand: "printf from-a-command"},
			want: "from-a-command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSecret(tt.cfg.ProviderSecret())
			if err != nil {
				t.Fatalf("ResolveSecret: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolved %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTranscriptProviderDefaults covers the provider being left unset.
func TestTranscriptProviderDefaults(t *testing.T) {
	var cfg TranscriptsConfig
	if got := cfg.TranscriptProvider(); got != "baseten" {
		t.Errorf("TranscriptProvider() = %q, want baseten", got)
	}
	cfg.Provider = "  ollama  "
	if got := cfg.TranscriptProvider(); got != "ollama" {
		t.Errorf("TranscriptProvider() = %q, want ollama with the padding removed", got)
	}

	// An unset provider still finds a Baseten key named for it.
	var unset TranscriptsConfig
	unset.BasetenAPIKey = "named"
	got, err := ResolveSecret(unset.ProviderSecret())
	if err != nil || got != "named" {
		t.Errorf("resolved %q, %v; want the Baseten key by default", got, err)
	}
}

// TestProviderKeyLoadsAndExpandsEndToEnd covers the whole path a real config
// takes: viper reading the provider-named key, expansion resolving the command
// behind it, and the resolver picking it for the configured provider.
//
// Each link is tested on its own elsewhere; this is the composition, which is
// what actually has to work when someone writes the key into a file.
func TestProviderKeyLoadsAndExpandsEndToEnd(t *testing.T) {
	t.Setenv("CODEEAGLE_TEST_JEV", "apikey_jev_from_env")

	cfg := loadTranscriptsConfig(t, "  enabled: true\n"+
		"  provider: baseten\n"+
		"  baseten_api_key: $(printf apikey_baseten_from_command)\n"+
		"  jev_api_key: ${CODEEAGLE_TEST_JEV}\n")

	if got := cfg.Transcripts.BasetenAPIKey; got != "apikey_baseten_from_command" {
		t.Errorf("baseten_api_key = %q, want the command's output", got)
	}
	if got := cfg.Transcripts.JevAPIKey; got != "apikey_jev_from_env" {
		t.Errorf("jev_api_key = %q, want the environment variable", got)
	}

	resolved, err := ResolveSecret(cfg.Transcripts.ProviderSecret())
	if err != nil {
		t.Fatalf("ResolveSecret: %v", err)
	}
	if resolved != "apikey_baseten_from_command" {
		t.Errorf("the provider's credential resolved to %q", resolved)
	}
}
