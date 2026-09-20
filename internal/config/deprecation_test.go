package config

import (
	"strings"
	"testing"
)

// TestCredentialWarning covers telling someone a setting has been superseded,
// without telling everyone.
//
// There were four ways to supply a key and expansion now does what two of them
// did, on every setting rather than only those given bespoke companions. The
// old settings still work — this is what says they need not be used, and it
// must stay quiet for a configuration that has already moved on.
func TestCredentialWarning(t *testing.T) {
	t.Setenv("CODEEAGLE_TEST_KEY", "apikey_from_env")

	tests := []struct {
		name    string
		cfg     TranscriptsConfig
		wantHas string
	}{
		{
			name: "a provider-named key says nothing",
			cfg:  TranscriptsConfig{Provider: "baseten", BasetenAPIKey: "apikey_named"},
		},
		{
			name: "a named key silences the old ones even when both are set",
			cfg: TranscriptsConfig{
				Provider:      "baseten",
				BasetenAPIKey: "apikey_named",
				APIKeyCommand: "keyring get baseten.co you",
			},
		},
		{
			name:    "a command is named as superseded",
			cfg:     TranscriptsConfig{Provider: "baseten", APIKeyCommand: "keyring get baseten.co you"},
			wantHas: "baseten_api_key: $(keyring get baseten.co you)",
		},
		{
			name:    "an environment variable that holds something",
			cfg:     TranscriptsConfig{Provider: "anthropic", APIKeyEnv: "CODEEAGLE_TEST_KEY"},
			wantHas: "anthropic_api_key: ${CODEEAGLE_TEST_KEY}",
		},
		{
			// The default names a variable nobody has set, so it supplies
			// nothing and is not worth mentioning.
			name: "an environment variable that holds nothing",
			cfg:  TranscriptsConfig{Provider: "baseten", APIKeyEnv: "CODEEAGLE_TEST_UNSET"},
		},
		{
			name:    "a bare literal is named after its provider",
			cfg:     TranscriptsConfig{Provider: "baseten", APIKey: "apikey_literal"},
			wantHas: "baseten_api_key",
		},
		{
			name: "nothing configured says nothing",
			cfg:  TranscriptsConfig{Provider: "ollama"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.CredentialWarning()
			if tt.wantHas == "" {
				if got != "" {
					t.Errorf("warned when it need not: %q", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantHas) {
				t.Errorf("warning = %q, want it to suggest %q", got, tt.wantHas)
			}
		})
	}
}
