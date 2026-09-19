package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteKeepsCredentialReferences is the guarantee the expansion syntax
// exists to provide: a config that fetches its key from elsewhere must still
// fetch it after the file has been written back.
//
// Loading expands in place, so without care the first `config edit` replaces
// the reference with the secret and commits it to disk.
func TestWriteKeepsCredentialReferences(t *testing.T) {
	const secret = "apikey_not-a-real-key"
	t.Setenv("CODEEAGLE_TEST_SECRET", secret)

	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ProjectDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ProjectConfigFile)
	original := `project:
  name: test
transcripts:
  enabled: true
  provider: baseten
  model: some-model
  jev_api_key: ${CODEEAGLE_TEST_SECRET}
  baseten_api_key: $(printenv CODEEAGLE_TEST_SECRET)
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	chdir(t, tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Expansion must actually have happened, or the test proves nothing.
	if cfg.Transcripts.JevAPIKey != secret {
		t.Fatalf("jev_api_key not expanded: got %q", cfg.Transcripts.JevAPIKey)
	}
	if cfg.Transcripts.BasetenAPIKey != secret {
		t.Fatalf("baseten_api_key not expanded: got %q", cfg.Transcripts.BasetenAPIKey)
	}

	// Change something unrelated, exactly as `config edit` does.
	cfg.Transcripts.Model = "another-model"

	if err := WriteConfig(cfg, path); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(written)

	if strings.Contains(got, secret) {
		t.Errorf("the resolved credential was written to disk:\n%s", got)
	}
	if !strings.Contains(got, "${CODEEAGLE_TEST_SECRET}") {
		t.Errorf("the ${VAR} reference was lost:\n%s", got)
	}
	if !strings.Contains(got, "$(printenv CODEEAGLE_TEST_SECRET)") {
		t.Errorf("the $(command) reference was lost:\n%s", got)
	}
	if !strings.Contains(got, "another-model") {
		t.Errorf("the edit was not saved:\n%s", got)
	}

	// In memory the caller still holds usable values after the write.
	if cfg.Transcripts.JevAPIKey != secret {
		t.Errorf("config left unexpanded after write: %q", cfg.Transcripts.JevAPIKey)
	}
}

// TestWriteKeepsAnEditedCredential checks the other direction: a credential
// the caller deliberately replaced must be saved as they set it, not reverted
// to the reference it used to come from.
func TestWriteKeepsAnEditedCredential(t *testing.T) {
	t.Setenv("CODEEAGLE_TEST_SECRET", "old-secret")

	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ProjectDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ProjectConfigFile)
	if err := os.WriteFile(path, []byte(`project:
  name: test
transcripts:
  jev_api_key: ${CODEEAGLE_TEST_SECRET}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	chdir(t, tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Transcripts.JevAPIKey = "${A_DIFFERENT_VARIABLE}"

	if err := WriteConfig(cfg, path); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "${A_DIFFERENT_VARIABLE}") {
		t.Errorf("an edited credential was reverted:\n%s", got)
	}
}

// chdir moves into dir for the duration of the test, so Load() discovers the
// configuration written there.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
