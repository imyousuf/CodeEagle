package embedding_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/embedding"
)

// These tests capture the request the Vertex AI provider sends today, with no
// production hook. Two seams in the libraries make that possible:
//
//   - genai reads GOOGLE_VERTEX_BASE_URL and sends every Vertex request there
//     instead of https://<location>-aiplatform.googleapis.com/.
//   - Config.CredentialsFile becomes GOOGLE_APPLICATION_CREDENTIALS, and a
//     service-account key file names its own token endpoint (token_uri), so
//     the OAuth exchange can be answered by the same fake server. An
//     authorized_user file would not do: its token URL is fixed to Google's.
//
// The pinned request is what a later change (say, task instructions) must
// still send when it is given nothing new to say.

// vertexEnv lists every environment variable genai or the auth library reads
// that could steer the client away from the fake server.
var vertexEnv = []string{
	"GOOGLE_APPLICATION_CREDENTIALS",
	"GOOGLE_VERTEX_BASE_URL",
	"GOOGLE_GEMINI_BASE_URL",
	"GOOGLE_GENAI_USE_VERTEXAI",
	"GOOGLE_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_CLOUD_PROJECT",
	"GOOGLE_CLOUD_LOCATION",
	"GOOGLE_CLOUD_REGION",
	"GOOGLE_CLOUD_QUOTA_PROJECT",
	"GOOGLE_CLOUD_UNIVERSE_DOMAIN",
	"GOOGLE_API_USE_CLIENT_CERTIFICATE",
	"GOOGLE_API_USE_MTLS_ENDPOINT",
}

// isolateVertexEnv unsets every variable in vertexEnv for the duration of the
// test. t.Setenv registers the restore, which also undoes the provider's own
// os.Setenv of GOOGLE_APPLICATION_CREDENTIALS.
func isolateVertexEnv(t *testing.T) {
	t.Helper()
	for _, k := range vertexEnv {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

const fakeAccessToken = "fake-access-token"

// fakeVertex answers the OAuth token exchange on /token and records every
// other request, replying with a fixed status and body.
type fakeVertex struct {
	srv *httptest.Server

	mu         sync.Mutex
	tokenForms []url.Values
	requests   []vertexRequest

	status int
	reply  string
}

// vertexRequest is one API request as the fake server saw it.
type vertexRequest struct {
	Method        string
	Path          string
	RawQuery      string
	Authorization string
	ContentType   string
	Body          string // canonical JSON: keys sorted, no whitespace
}

func newFakeVertex(t *testing.T, status int, reply string) *fakeVertex {
	t.Helper()
	f := &fakeVertex{status: status, reply: reply}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if r.URL.Path == "/token" {
			form, err := url.ParseQuery(string(raw))
			if err != nil {
				t.Errorf("parse token form: %v", err)
			}
			f.mu.Lock()
			f.tokenForms = append(f.tokenForms, form)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"`+fakeAccessToken+`","token_type":"Bearer","expires_in":3600}`)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, vertexRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			RawQuery:      r.URL.RawQuery,
			Authorization: r.Header.Get("Authorization"),
			ContentType:   r.Header.Get("Content-Type"),
			Body:          canonicalJSON(t, raw),
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.reply)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeVertex) captured() []vertexRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]vertexRequest(nil), f.requests...)
}

func (f *fakeVertex) tokenExchanges() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.tokenForms...)
}

// writeServiceAccountKey writes a service-account credentials file whose
// token endpoint is tokenURI, signed by a key generated for the test.
func writeServiceAccountKey(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	creds := map[string]string{
		"type":           "service_account",
		"project_id":     "creds-project",
		"private_key_id": "test-key-id",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "embedder@creds-project.iam.gserviceaccount.com",
		"client_id":      "1234567890",
		"token_uri":      tokenURI,
	}
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal credentials: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	return path
}

// newCapturedVertex returns a fake server and a Vertex provider pointed at it.
func newCapturedVertex(t *testing.T, cfg embedding.Config, status int, reply string) (*fakeVertex, embedding.Provider) {
	t.Helper()
	isolateVertexEnv(t)
	f := newFakeVertex(t, status, reply)
	t.Setenv("GOOGLE_VERTEX_BASE_URL", f.srv.URL+"/")

	cfg.Provider = "vertex-ai"
	cfg.CredentialsFile = writeServiceAccountKey(t, f.srv.URL+"/token")
	p, err := embedding.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider(vertex-ai): %v", err)
	}
	return f, p
}

func TestVertexNewProviderValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     embedding.Config
		wantErr string
	}{
		{
			name:    "nothing set",
			cfg:     embedding.Config{},
			wantErr: "vertex-ai embedding requires project",
		},
		{
			name:    "location only",
			cfg:     embedding.Config{Location: "us-central1"},
			wantErr: "vertex-ai embedding requires project",
		},
		{
			name:    "project only",
			cfg:     embedding.Config{Project: "p"},
			wantErr: "vertex-ai embedding requires location",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateVertexEnv(t)
			tc.cfg.Provider = "vertex-ai"
			tc.cfg.CredentialsFile = "/nonexistent/creds.json"

			p, err := embedding.NewProvider(tc.cfg)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("NewProvider error = %v, want %q", err, tc.wantErr)
			}
			if p != nil {
				t.Errorf("NewProvider returned a provider alongside the error")
			}
			// Validation runs before the credentials file is exported.
			if v, ok := os.LookupEnv("GOOGLE_APPLICATION_CREDENTIALS"); ok {
				t.Errorf("GOOGLE_APPLICATION_CREDENTIALS = %q after a validation error, want unset", v)
			}
		})
	}
}

func TestVertexNewProviderMissingCredentialsFile(t *testing.T) {
	isolateVertexEnv(t)
	missing := filepath.Join(t.TempDir(), "missing.json")

	_, err := embedding.NewProvider(embedding.Config{
		Provider:        "vertex-ai",
		Project:         "p",
		Location:        "us-central1",
		CredentialsFile: missing,
	})
	const wantPrefix = "create vertex-ai genai client: failed to find default credentials: "
	if err == nil || !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Fatalf("NewProvider error = %v, want prefix %q", err, wantPrefix)
	}
	// The file is exported process-wide even though it could not be used.
	if got := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); got != missing {
		t.Errorf("GOOGLE_APPLICATION_CREDENTIALS = %q, want %q", got, missing)
	}
}

func TestVertexProviderMetadata(t *testing.T) {
	tests := []struct {
		name      string
		cfg       embedding.Config
		wantModel string
		wantDims  int
	}{
		{
			name:      "defaults",
			cfg:       embedding.Config{Project: "p", Location: "us-central1"},
			wantModel: "gemini-embedding-001",
			wantDims:  768,
		},
		{
			name:      "overrides",
			cfg:       embedding.Config{Project: "p", Location: "us-central1", Model: "text-embedding-005", Dimensions: 256},
			wantModel: "text-embedding-005",
			wantDims:  256,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, p := newCapturedVertex(t, tc.cfg, http.StatusOK, `{}`)
			if got := p.Name(); got != "vertex-ai" {
				t.Errorf("Name() = %q, want %q", got, "vertex-ai")
			}
			if got := p.ModelName(); got != tc.wantModel {
				t.Errorf("ModelName() = %q, want %q", got, tc.wantModel)
			}
			if got := p.Dimensions(); got != tc.wantDims {
				t.Errorf("Dimensions() = %d, want %d", got, tc.wantDims)
			}
			// Creating the client talks to nobody.
			if n := len(f.captured()) + len(f.tokenExchanges()); n != 0 {
				t.Errorf("got %d requests while creating the provider, want 0", n)
			}
		})
	}
}

func TestVertexEmbedRequest(t *testing.T) {
	const reply = `{"predictions":[` +
		`{"embeddings":{"values":[0.5,-1.25],"statistics":{"truncated":false,"token_count":3}}},` +
		`{"embeddings":{"values":[2,0.125],"statistics":{"truncated":false,"token_count":4}}}]}`
	f, p := newCapturedVertex(t, embedding.Config{Project: "my-project", Location: "us-central1"}, http.StatusOK, reply)

	got, err := p.Embed(context.Background(), []string{"hello world", "  spaced\ttext  "})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if want := [][]float32{{0.5, -1.25}, {2, 0.125}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Embed() = %v, want %v", got, want)
	}

	// One predict call for the batch: each text verbatim as an instance's
	// content (no prefix, no task type, no title), and the configured
	// dimensionality as the only parameter.
	wantReqs := []vertexRequest{{
		Method:        http.MethodPost,
		Path:          "/v1beta1/projects/my-project/locations/us-central1/publishers/google/models/gemini-embedding-001:predict",
		Authorization: "Bearer " + fakeAccessToken,
		ContentType:   "application/json",
		Body:          `{"instances":[{"content":"hello world"},{"content":"  spaced\ttext  "}],"parameters":{"outputDimensionality":768}}`,
	}}
	if reqs := f.captured(); !reflect.DeepEqual(reqs, wantReqs) {
		t.Errorf("requests =\n%+v\nwant\n%+v", reqs, wantReqs)
	}

	// The bearer token came from the service-account file's token endpoint.
	forms := f.tokenExchanges()
	if len(forms) != 1 {
		t.Fatalf("got %d token exchanges, want 1", len(forms))
	}
	if got, want := forms[0].Get("grant_type"), "urn:ietf:params:oauth:grant-type:jwt-bearer"; got != want {
		t.Errorf("token grant_type = %q, want %q", got, want)
	}
}

func TestVertexEmbedQueryRequest(t *testing.T) {
	const reply = `{"predictions":[{"embeddings":{"values":[1,2,3]}}]}`
	f, p := newCapturedVertex(t, embedding.Config{
		Project:    "my-project",
		Location:   "europe-west4",
		Model:      "text-embedding-005",
		Dimensions: 256,
	}, http.StatusOK, reply)

	got, err := p.EmbedQuery(context.Background(), "what is auth")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if want := []float32{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("EmbedQuery() = %v, want %v", got, want)
	}

	// A query is sent exactly as a one-text document batch: Vertex is not
	// told it is a query.
	wantReqs := []vertexRequest{{
		Method:        http.MethodPost,
		Path:          "/v1beta1/projects/my-project/locations/europe-west4/publishers/google/models/text-embedding-005:predict",
		Authorization: "Bearer " + fakeAccessToken,
		ContentType:   "application/json",
		Body:          `{"instances":[{"content":"what is auth"}],"parameters":{"outputDimensionality":256}}`,
	}}
	if reqs := f.captured(); !reflect.DeepEqual(reqs, wantReqs) {
		t.Errorf("requests =\n%+v\nwant\n%+v", reqs, wantReqs)
	}
}

func TestVertexEmbedEmptyInputSendsNothing(t *testing.T) {
	f, p := newCapturedVertex(t, embedding.Config{Project: "p", Location: "us-central1"}, http.StatusOK, `{}`)

	for _, in := range [][]string{nil, {}} {
		got, err := p.Embed(context.Background(), in)
		if err != nil {
			t.Errorf("Embed(%#v): %v", in, err)
		}
		if got != nil {
			t.Errorf("Embed(%#v) = %#v, want nil", in, got)
		}
	}
	if n := len(f.captured()) + len(f.tokenExchanges()); n != 0 {
		t.Errorf("got %d requests for empty input, want 0", n)
	}
}

func TestVertexEmbedReplies(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		reply     string
		query     bool
		want      [][]float32
		wantErr   string
		errPrefix bool // wantErr is a prefix, the rest comes from genai
	}{
		{
			// Fewer vectors than texts is passed through unchecked.
			name:  "short batch",
			reply: `{"predictions":[{"embeddings":{"values":[1]}}]}`,
			want:  [][]float32{{1}},
		},
		{
			name:  "no predictions",
			reply: `{}`,
			want:  [][]float32{},
		},
		{
			name:    "query with no predictions",
			reply:   `{}`,
			query:   true,
			wantErr: "vertex-ai embed returned no results",
		},
		{
			name:      "server error",
			status:    http.StatusInternalServerError,
			reply:     `{"error":{"code":500,"message":"boom","status":"INTERNAL"}}`,
			wantErr:   "vertex-ai embed: ",
			errPrefix: true,
		},
		{
			name:      "server error on query",
			status:    http.StatusInternalServerError,
			reply:     `{"error":{"code":500,"message":"boom","status":"INTERNAL"}}`,
			query:     true,
			wantErr:   "vertex-ai embed: ",
			errPrefix: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			_, p := newCapturedVertex(t, embedding.Config{Project: "p", Location: "us-central1"}, status, tc.reply)

			var got [][]float32
			var err error
			if tc.query {
				var v []float32
				v, err = p.EmbedQuery(context.Background(), "q")
				if v != nil {
					got = [][]float32{v}
				}
			} else {
				got, err = p.Embed(context.Background(), []string{"a", "b"})
			}

			if tc.wantErr != "" {
				switch {
				case err == nil:
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				case tc.errPrefix && !strings.HasPrefix(err.Error(), tc.wantErr):
					t.Errorf("error = %q, want prefix %q", err, tc.wantErr)
				case !tc.errPrefix && err.Error() != tc.wantErr:
					t.Errorf("error = %q, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestVertexCredentialsFileIsExportedProcessWide(t *testing.T) {
	// The provider hands its credentials file to the auth library through the
	// environment, so it outlives NewProvider and applies to every later
	// Google client in the process.
	_, _ = newCapturedVertex(t, embedding.Config{Project: "p", Location: "us-central1"}, http.StatusOK, `{}`)

	got := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if filepath.Base(got) != "sa.json" {
		t.Errorf("GOOGLE_APPLICATION_CREDENTIALS = %q, want the configured sa.json", got)
	}
}
