package judges

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proofspan/internal/schema"
)

// Providers make the judge engine provider-agnostic: a manifest declares
// one or more providers (OpenAI-compatible endpoint + the env var holding
// the API key), judges reference a provider by name, and the engine
// resolves endpoint+key at run time. Local endpoints (vLLM, ollama) may
// omit the key env entirely — no auth.

func writeProviderManifest(t *testing.T, body string) *Manifest {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func loadManifestErr(t *testing.T, body string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	return err
}

const providersManifest = `{
  "namespace": "n/x",
  "version": "v1.2.0",
  "providers": {
    "nvidia": {"endpoint": "https://integrate.api.nvidia.com/v1", "api_key_env": "NVIDIA_API_KEY"},
    "local":   {"endpoint": "http://localhost:8000/v1"}
  },
  "judges": [
    {"id": "j", "model_fingerprint": "m@sha256:1", "provider": "nvidia"}
  ]
}`

func TestManifestParsesProviders(t *testing.T) {
	m := writeProviderManifest(t, providersManifest)
	if m.Providers["nvidia"].Endpoint != "https://integrate.api.nvidia.com/v1" {
		t.Errorf("nvidia endpoint: %v", m.Providers["nvidia"])
	}
	if m.Providers["nvidia"].APIKeyEnv != "NVIDIA_API_KEY" {
		t.Errorf("nvidia api_key_env: %v", m.Providers["nvidia"])
	}
	if m.Providers["local"].APIKeyEnv != "" {
		t.Errorf("local provider must allow no-auth (empty api_key_env), got %q", m.Providers["local"].APIKeyEnv)
	}
}

func TestLoadRejectsUnknownProviderRef(t *testing.T) {
	err := loadManifestErr(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"nvidia":{"endpoint":"https://x/v1","api_key_env":"K"}},
	  "judges":[{"id":"j","model_fingerprint":"m@sha256:1","provider":"ghost"}]
	}`)
	if err == nil {
		t.Fatal("unknown provider reference must fail Load")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error must name the provider: %v", err)
	}
}

func TestLoadRejectsHTTPJudgeWithoutProvider(t *testing.T) {
	err := loadManifestErr(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"nvidia":{"endpoint":"https://x/v1","api_key_env":"K"}},
	  "judges":[{"id":"j","model_fingerprint":"m@sha256:1"}]
	}`)
	if err == nil {
		t.Fatal("HTTP judge without provider must fail Load")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("error must say what is missing: %v", err)
	}
}

func TestLoadRejectsProviderWithoutEndpoint(t *testing.T) {
	err := loadManifestErr(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"broken":{"api_key_env":"K"}},
	  "judges":[{"id":"j","model_fingerprint":"m@sha256:1","provider":"broken"}]
	}`)
	if err == nil {
		t.Fatal("provider without endpoint must fail Load")
	}
}

func TestLoadAcceptsBuiltinOnlyManifestWithoutProviders(t *testing.T) {
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "judges":[{"id":"b","model_fingerprint":"builtin:b@v1"}]
	}`)
	if len(m.Judges) != 1 {
		t.Fatalf("builtin-only manifest must load: %+v", m)
	}
}

// fakeJudgeServer simulates an OpenAI-compatible provider: /v1/models/<id>
// reports the served model, /v1/chat/completions grades PASS. It records
// the last Authorization header so tests can assert key propagation.
func fakeJudgeServer(t *testing.T, model string) (*httptest.Server, *string) {
	t.Helper()
	var lastAuth = "sentinel"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/models/"+model) {
			w.Write([]byte(`{"id":"` + model + `"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.Write([]byte(`{"choices":[{"message":{"content":"PASS: grounded"}}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &lastAuth
}

func TestEngineResolvesLocalProviderWithoutKey(t *testing.T) {
	const model = "local-model"
	srv, lastAuth := fakeJudgeServer(t, model)
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"local":{"endpoint":"`+srv.URL+`/v1"}},
	  "judges":[{"id":"lj","model_fingerprint":"`+model+`@sha256:abc","provider":"local"}]
	}`)
	eng := NewEngine(m) // no WithAPIKey: local provider declares no env var
	v, err := eng.Judge(context.Background(), "lj", "t1", []schema.Span{{SpanID: "s1", Kind: "llm", Output: `{"a":"b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "pass" {
		t.Errorf("local provider judge must run without any key, got %s: %s", v.Status, v.Detail)
	}
	if *lastAuth != "" {
		t.Errorf("no Authorization header expected for no-auth provider, got %q", *lastAuth)
	}
}

func TestEngineProviderKeyFromEnvVar(t *testing.T) {
	const model = "env-key-model"
	srv, lastAuth := fakeJudgeServer(t, model)
	t.Setenv("TEST_PROOFSPAN_PROVIDER_KEY", "env-secret-123")
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"custom":{"endpoint":"`+srv.URL+`/v1","api_key_env":"TEST_PROOFSPAN_PROVIDER_KEY"}},
	  "judges":[{"id":"cj","model_fingerprint":"`+model+`@sha256:abc","provider":"custom"}]
	}`)
	eng := NewEngine(m)
	v, err := eng.Judge(context.Background(), "cj", "t1", []schema.Span{{SpanID: "s1", Kind: "llm", Output: `{"a":"b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "pass" {
		t.Errorf("judge with env-var key must run, got %s: %s", v.Status, v.Detail)
	}
	if *lastAuth != "Bearer env-secret-123" {
		t.Errorf("Authorization must come from the provider's env var, got %q", *lastAuth)
	}
}

func TestEngineProviderMissingKeyNamesEnvVar(t *testing.T) {
	const model = "missing-key-model"
	srv, _ := fakeJudgeServer(t, model)
	t.Setenv("TEST_PROOFSPAN_MISSING_KEY", "") // explicitly empty
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"needskey":{"endpoint":"`+srv.URL+`/v1","api_key_env":"TEST_PROOFSPAN_MISSING_KEY"}},
	  "judges":[{"id":"nj","model_fingerprint":"`+model+`@sha256:abc","provider":"needskey"}]
	}`)
	eng := NewEngine(m)
	_, err := eng.Judge(context.Background(), "nj", "t1", nil)
	if err == nil {
		t.Fatal("missing key must refuse to run")
	}
	if !strings.Contains(err.Error(), "TEST_PROOFSPAN_MISSING_KEY") {
		t.Errorf("error must name the env var to set: %v", err)
	}
}

func TestEngineEndpointOverrideWins(t *testing.T) {
	const model = "override-model"
	srv, _ := fakeJudgeServer(t, model)
	// manifest declares a bogus endpoint; the CLI-style override must win
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"p":{"endpoint":"https://placeholder.invalid/v1"}},
	  "judges":[{"id":"oj","model_fingerprint":"`+model+`@sha256:abc","provider":"p"}]
	}`)
	eng := NewEngine(m).WithEndpoint(srv.URL + "/v1")
	v, err := eng.Judge(context.Background(), "oj", "t1", []schema.Span{{SpanID: "s1", Kind: "llm", Output: `{"a":"b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "pass" {
		t.Errorf("endpoint override must be honored, got %s: %s", v.Status, v.Detail)
	}
}

func TestEngineAPIKeyOverrideWins(t *testing.T) {
	const model = "key-override-model"
	srv, lastAuth := fakeJudgeServer(t, model)
	t.Setenv("TEST_PROOFSPAN_OVERRIDE_KEY", "from-env")
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"p":{"endpoint":"`+srv.URL+`/v1","api_key_env":"TEST_PROOFSPAN_OVERRIDE_KEY"}},
	  "judges":[{"id":"okj","model_fingerprint":"`+model+`@sha256:abc","provider":"p"}]
	}`)
	eng := NewEngine(m).WithAPIKey("from-flag")
	v, err := eng.Judge(context.Background(), "okj", "t1", []schema.Span{{SpanID: "s1", Kind: "llm", Output: `{"a":"b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "pass" {
		t.Fatalf("explicit key override must win, got %s", v.Status)
	}
	if *lastAuth != "Bearer from-flag" {
		t.Errorf("explicit override must beat provider env var, got %q", *lastAuth)
	}
}

// TestEngineEndpointOverrideKeepsEnvKey is a regression test: an endpoint
// override must NOT disable the provider's api_key_env lookup. Caught by
// e2e against a fake provider (env key silently not sent).
func TestEngineEndpointOverrideKeepsEnvKey(t *testing.T) {
	const model = "env-key-plus-endpoint-model"
	srv, lastAuth := fakeJudgeServer(t, model)
	t.Setenv("TEST_PROOFSPAN_ENV_KEY_2", "env-key-still-sent")
	m := writeProviderManifest(t, `{
	  "namespace":"n/x","version":"v1.2.0",
	  "providers":{"p":{"endpoint":"https://placeholder.invalid/v1","api_key_env":"TEST_PROOFSPAN_ENV_KEY_2"}},
	  "judges":[{"id":"ekj","model_fingerprint":"`+model+`@sha256:abc","provider":"p"}]
	}`)
	eng := NewEngine(m).WithEndpoint(srv.URL + "/v1")
	v, err := eng.Judge(context.Background(), "ekj", "t1", []schema.Span{{SpanID: "s1", Kind: "llm", Output: `{"a":"b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "pass" {
		t.Fatalf("endpoint override with env key must run, got %s: %s", v.Status, v.Detail)
	}
	if *lastAuth != "Bearer env-key-still-sent" {
		t.Errorf("env-var key must still propagate under endpoint override, got %q", *lastAuth)
	}
}
