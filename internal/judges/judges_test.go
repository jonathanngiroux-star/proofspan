package judges

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateAcceptsGoodManifest(t *testing.T) {
	path := writeManifest(t, `{
		"namespace": "namespace/llm-judge-registry",
		"version": "v1.0.0",
		"judges": [
			{"id": "factual-consistency", "model_fingerprint": "openai/gpt-4o-2024-08-06@sha256:abc123", "description": "grades summary vs docs"}
		]
	}`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Namespace != "namespace/llm-judge-registry" || m.Version != "v1.0.0" {
		t.Errorf("manifest fields wrong: %+v", m)
	}
	if len(m.Judges) != 1 || m.Judges[0].ID != "factual-consistency" {
		t.Errorf("judges wrong: %+v", m.Judges)
	}
	if m.Judges[0].ModelFingerprint != "openai/gpt-4o-2024-08-06@sha256:abc123" {
		t.Errorf("fingerprint not round-tripped: %q", m.Judges[0].ModelFingerprint)
	}
}

func TestValidateRejectsEmptyFingerprint(t *testing.T) {
	path := writeManifest(t, `{
		"namespace": "namespace/llm-judge-registry",
		"version": "v1.0.0",
		"judges": [
			{"id": "factual-consistency", "model_fingerprint": "", "description": "x"}
		]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("empty model_fingerprint must be rejected")
	}
	if !strings.Contains(err.Error(), "model_fingerprint") {
		t.Errorf("error should name the field: %v", err)
	}
}

func TestValidateRejectsNoJudges(t *testing.T) {
	path := writeManifest(t, `{"namespace":"n/x","version":"v1.0.0","judges":[]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("empty judge list must be rejected")
	}
}

func TestValidateRejectsMalformedJSON(t *testing.T) {
	path := writeManifest(t, `{oops`)
	if _, err := Load(path); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

func TestValidateRejectsDuplicateJudgeIDs(t *testing.T) {
	path := writeManifest(t, `{
		"namespace": "n/x", "version": "v1.0.0",
		"judges": [
			{"id": "same", "model_fingerprint": "m@sha256:1"},
			{"id": "same", "model_fingerprint": "m@sha256:2"}
		]
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("duplicate judge IDs must be rejected")
	}
}

func TestFingerprintLookup(t *testing.T) {
	path := writeManifest(t, `{
		"namespace": "n/x", "version": "v1.0.0",
		"judges": [
			{"id": "a", "model_fingerprint": "m1@sha256:1"},
			{"id": "b", "model_fingerprint": "m2@sha256:2"}
		]
	}`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if fp := m.Fingerprint("a"); fp != "m1@sha256:1" {
		t.Errorf("lookup a = %q", fp)
	}
	if fp := m.Fingerprint("missing"); fp != "" {
		t.Errorf("missing judge must return empty, got %q", fp)
	}
}

func TestRegistryPathMatchesBrief(t *testing.T) {
	// the brief pins judges/manifest.json with a pinned fingerprint per judge
	data, err := os.ReadFile("../../judges/manifest.json")
	if err != nil {
		t.Skip("repo manifest not present in test sandbox")
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("repo manifest invalid JSON: %v", err)
	}
	judges, ok := raw["judges"].([]any)
	if !ok || len(judges) == 0 {
		t.Fatalf("repo manifest needs at least one judge: %v", raw)
	}
	for _, j := range judges {
		jm := j.(map[string]any)
		if jm["model_fingerprint"] == "" || jm["model_fingerprint"] == nil {
			t.Errorf("repo judge %v missing model_fingerprint", jm["id"])
		}
	}
}
