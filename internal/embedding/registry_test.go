package embedding_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	appconfig "github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/embedding"
)

// These tests pin the registry and the explicit-provider half of
// DetectProvider. Auto-detection is left alone: its result depends on whether
// something answers on localhost:11434.

func TestIsProviderRegistered(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"ollama", true},
		{"vertex-ai", true},
		{"", false},
		{"Ollama", false}, // names are case-sensitive
		{"vertex", false},
		{"vertexai", false},
		{"openai", false},
		{"baseten", false},
	}
	for _, tc := range tests {
		if got := embedding.IsProviderRegistered(tc.name); got != tc.want {
			t.Errorf("IsProviderRegistered(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNewProviderErrors(t *testing.T) {
	tests := []struct {
		provider string
		wantErr  string
	}{
		{"", "embedding provider is required"},
		{"bogus", "unknown embedding provider: bogus"},
		{"Ollama", "unknown embedding provider: Ollama"},
	}
	for _, tc := range tests {
		p, err := embedding.NewProvider(embedding.Config{Provider: tc.provider})
		if err == nil || err.Error() != tc.wantErr {
			t.Errorf("NewProvider(%q) error = %v, want %q", tc.provider, err, tc.wantErr)
		}
		if p != nil {
			t.Errorf("NewProvider(%q) returned a provider alongside the error", tc.provider)
		}
	}
}

// stubProvider records the Config its factory was given.
type stubProvider struct{ cfg embedding.Config }

func (s *stubProvider) Embed(context.Context, []string) ([][]float32, error)  { return nil, nil }
func (s *stubProvider) EmbedQuery(context.Context, string) ([]float32, error) { return nil, nil }
func (s *stubProvider) Dimensions() int                                       { return s.cfg.Dimensions }
func (s *stubProvider) Name() string                                          { return s.cfg.Provider }
func (s *stubProvider) ModelName() string                                     { return s.cfg.Model }

func TestRegisterProviderPassesConfigThrough(t *testing.T) {
	// There is no way to unregister, so the stub stays for the rest of the
	// test binary; registering it again on -count=N replaces it.
	const name = "characterization-stub"
	embedding.RegisterProvider(name, func(cfg embedding.Config) (embedding.Provider, error) {
		return &stubProvider{cfg: cfg}, nil
	})
	if !embedding.IsProviderRegistered(name) {
		t.Fatalf("%q not registered after RegisterProvider", name)
	}

	cfg := embedding.Config{
		Provider:        name,
		Model:           "m",
		Dimensions:      42,
		OllamaBaseURL:   "http://ollama.invalid",
		Project:         "p",
		Location:        "l",
		CredentialsFile: "/c.json",
	}
	p, err := embedding.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	stub, ok := p.(*stubProvider)
	if !ok {
		t.Fatalf("NewProvider returned %T, want *stubProvider", p)
	}
	// The factory receives the Config untouched, Provider included.
	if stub.cfg != cfg {
		t.Errorf("factory got %+v, want %+v", stub.cfg, cfg)
	}
}

func TestDetectProviderExplicitOllama(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantModel string
	}{
		{name: "default model", wantModel: "nomic-embed-text-v2-moe"},
		{name: "configured model", model: "mxbai-embed-large", wantModel: "mxbai-embed-large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &appconfig.Config{}
			cfg.Agents.EmbeddingProvider = "ollama"
			cfg.Agents.EmbeddingModel = tc.model
			// Neither the LLM base URL nor the GCP settings reach the
			// embedding provider.
			cfg.Agents.BaseURL = "http://ollama.invalid:9999"
			cfg.Agents.Project = "p"
			cfg.Agents.Location = "us-central1"

			p, err := embedding.DetectProvider(cfg)
			if err != nil {
				t.Fatalf("DetectProvider: %v", err)
			}
			if p == nil {
				t.Fatal("DetectProvider returned nil for an explicit provider")
			}
			if got := p.Name(); got != "ollama" {
				t.Errorf("Name() = %q, want %q", got, "ollama")
			}
			if got := p.ModelName(); got != tc.wantModel {
				t.Errorf("ModelName() = %q, want %q", got, tc.wantModel)
			}
			if got := p.Dimensions(); got != 768 {
				t.Errorf("Dimensions() = %d, want 768", got)
			}

			// An explicit provider is created, not probed, and always targets
			// the default Ollama URL. The cancelled context fails the request
			// before anything is dialled; the error names the URL.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = p.EmbedQuery(ctx, "q")
			want := `embed request: Post "http://localhost:11434/api/embed": context canceled`
			if err == nil || err.Error() != want {
				t.Errorf("EmbedQuery error = %v, want %q", err, want)
			}
		})
	}
}

func TestDetectProviderExplicitVertexErrors(t *testing.T) {
	tests := []struct {
		name     string
		project  string
		location string
		wantErr  string
	}{
		{
			name:    "no project",
			wantErr: `configured embedding provider "vertex-ai": vertex-ai embedding requires project`,
		},
		{
			name:    "no location",
			project: "p",
			wantErr: `configured embedding provider "vertex-ai": vertex-ai embedding requires location`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateVertexEnv(t)
			cfg := &appconfig.Config{}
			cfg.Agents.EmbeddingProvider = "vertex-ai"
			cfg.Agents.Project = tc.project
			cfg.Agents.Location = tc.location

			p, err := embedding.DetectProvider(cfg)
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("DetectProvider error = %v, want %q", err, tc.wantErr)
			}
			if p != nil {
				t.Errorf("DetectProvider returned a provider alongside the error")
			}
		})
	}
}

func TestDetectProviderExplicitVertex(t *testing.T) {
	isolateVertexEnv(t)
	f := newFakeVertex(t, http.StatusOK, `{"predictions":[{"embeddings":{"values":[0.25]}}]}`)
	t.Setenv("GOOGLE_VERTEX_BASE_URL", f.srv.URL+"/")
	credsFile := writeServiceAccountKey(t, f.srv.URL+"/token")

	cfg := &appconfig.Config{}
	cfg.Agents.EmbeddingProvider = "vertex-ai"
	cfg.Agents.EmbeddingModel = "text-embedding-005"
	cfg.Agents.Project = "agents-project"
	cfg.Agents.Location = "asia-northeast1"
	cfg.Agents.CredentialsFile = credsFile

	p, err := embedding.DetectProvider(cfg)
	if err != nil {
		t.Fatalf("DetectProvider: %v", err)
	}
	if p == nil {
		t.Fatal("DetectProvider returned nil for an explicit provider")
	}
	if got := p.Name(); got != "vertex-ai" {
		t.Errorf("Name() = %q, want %q", got, "vertex-ai")
	}
	if got := p.ModelName(); got != "text-embedding-005" {
		t.Errorf("ModelName() = %q, want %q", got, "text-embedding-005")
	}
	// The app config carries no embedding dimensions, so the default holds.
	if got := p.Dimensions(); got != 768 {
		t.Errorf("Dimensions() = %d, want 768", got)
	}
	if got := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); got != credsFile {
		t.Errorf("GOOGLE_APPLICATION_CREDENTIALS = %q, want %q", got, credsFile)
	}

	// Project, location and model all come from the agents section.
	if _, err := p.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	reqs := f.captured()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	const wantPath = "/v1beta1/projects/agents-project/locations/asia-northeast1/publishers/google/models/text-embedding-005:predict"
	if reqs[0].Path != wantPath {
		t.Errorf("path = %q, want %q", reqs[0].Path, wantPath)
	}
	const wantBody = `{"instances":[{"content":"q"}],"parameters":{"outputDimensionality":768}}`
	if reqs[0].Body != wantBody {
		t.Errorf("body = %s, want %s", reqs[0].Body, wantBody)
	}
}

func TestDetectProviderExplicitUnknown(t *testing.T) {
	cfg := &appconfig.Config{}
	cfg.Agents.EmbeddingProvider = "bogus"

	p, err := embedding.DetectProvider(cfg)
	const want = `configured embedding provider "bogus": unknown embedding provider: bogus`
	if err == nil || err.Error() != want {
		t.Errorf("DetectProvider error = %v, want %q", err, want)
	}
	if p != nil {
		t.Errorf("DetectProvider returned a provider alongside the error")
	}
	// The registry's error is wrapped, not flattened.
	if inner := errors.Unwrap(err); inner == nil || inner.Error() != "unknown embedding provider: bogus" {
		t.Errorf("errors.Unwrap(err) = %v, want the registry's error", inner)
	}
}
