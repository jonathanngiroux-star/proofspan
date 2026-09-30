package scim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/internal/store"
)

// SCIM endpoints enforce bearer auth when a token is configured.
// No token configured = dev mode, open (localhost-only by default).
// Wrong/missing token = 401 with the SCIM error schema, before any
// handler logic runs.

func newAuthedServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/scim.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := NewStoreBacked(st)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		p.RequireBearer(token)
	}
	mux := http.NewServeMux()
	p.Mount(mux, "/scim/v2")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthMissingTokenIs401(t *testing.T) {
	srv := newAuthedServer(t, "secret-token")
	resp, err := http.Post(srv.URL+"/scim/v2/Users", "application/scim+json",
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"x@y"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want 401", resp.StatusCode)
	}
}

func TestAuthWrongTokenIs401(t *testing.T) {
	srv := newAuthedServer(t, "secret-token")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/scim/v2/Users", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", resp.StatusCode)
	}
}

func TestAuthCorrectTokenIs200(t *testing.T) {
	srv := newAuthedServer(t, "secret-token")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/scim/v2/Users", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct token: got %d, want 200", resp.StatusCode)
	}
}

func TestAuthErrorUsesSCIMSchema(t *testing.T) {
	srv := newAuthedServer(t, "secret-token")
	resp, err := http.Get(srv.URL + "/scim/v2/Users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := jsonDecode(resp.Body, &body); err != nil {
		t.Fatal(err)
	}
	schemas, _ := body["schemas"].([]any)
	if len(schemas) == 0 || !strings.Contains(schemas[0].(string), "Error") {
		t.Errorf("401 body must use the SCIM error schema: %v", body)
	}
}

func TestNoTokenConfiguredIsOpenDevMode(t *testing.T) {
	// documented dev mode: no token → no enforcement (serve binds 127.0.0.1)
	srv := newAuthedServer(t, "")
	resp, err := http.Get(srv.URL + "/scim/v2/Users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dev mode: got %d, want 200", resp.StatusCode)
	}
}

func TestServiceProviderConfigAlwaysPublic(t *testing.T) {
	// RFC 7643: ServiceProviderConfig must be reachable for capability
	// discovery; it leaks nothing and IdPs probe it before configuring.
	srv := newAuthedServer(t, "secret-token")
	resp, err := http.Get(srv.URL + "/scim/v2/ServiceProviderConfig")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ServiceProviderConfig must not require auth: got %d", resp.StatusCode)
	}
}

func jsonDecode(r interface{ Read([]byte) (int, error) }, v any) error {
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return json.Unmarshal(buf[:n], v)
}
