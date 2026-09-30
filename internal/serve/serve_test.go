package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
	"github.com/jonathanngiroux-star/proofspan/internal/store"
)

// The full HTTP surface runs against the store-backed SCIM provider:
// a user created over HTTP must be persisted in SQLite, not memory.

func newTestServer(t *testing.T, scimOn bool) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SaveTrajectory(schema.Trajectory{
		Type: "trajectory", Version: schema.ATFVersion, TrajectoryID: "t1",
		Source: "native", StartedAtUnixMs: 1,
	}, []schema.Span{{Type: "span", SpanID: "s1", TrajectoryID: "t1", Name: "x", Kind: "llm",
		StartedAtUnixMs: 1, EndedAtUnixMs: 2}}); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(st, scimOn, "")
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return hs
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, false)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestListTrajectories(t *testing.T) {
	srv := newTestServer(t, false)
	resp, err := http.Get(srv.URL + "/v1/trajectories")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string][]string
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body["trajectories"]) != 1 || body["trajectories"][0] != "t1" {
		t.Errorf("trajectories = %v", body)
	}
}

func TestGetTrajectory(t *testing.T) {
	srv := newTestServer(t, false)
	resp, err := http.Get(srv.URL + "/v1/trajectories/t1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Trajectory schema.Trajectory `json:"trajectory"`
		Spans      []schema.Span     `json:"spans"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Trajectory.TrajectoryID != "t1" || len(body.Spans) != 1 {
		t.Errorf("body wrong: %+v %+v", body.Trajectory, body.Spans)
	}
}

func TestSCIMFlagOff(t *testing.T) {
	srv := newTestServer(t, false)
	resp, err := http.Get(srv.URL + "/scim/v2/ServiceProviderConfig")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("SCIM must be 404 when flag off, got %d", resp.StatusCode)
	}
}

func TestSCIMFlagOn(t *testing.T) {
	srv := newTestServer(t, true)
	resp, err := http.Get(srv.URL + "/scim/v2/ServiceProviderConfig")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SCIM must mount when flag on, got %d", resp.StatusCode)
	}
}

func TestSCIMUserPersistsOverHTTP(t *testing.T) {
	srv := newTestServer(t, true)
	// create over HTTP
	resp, err := http.Post(srv.URL+"/scim/v2/Users", "application/scim+json",
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"persist@example.com","name":{"givenName":"Persist","familyName":"Ent"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	id, _ := user["id"].(string)
	if resp.StatusCode != 201 || id == "" {
		t.Fatalf("create: status=%d body=%v", resp.StatusCode, user)
	}
	// get by id over HTTP (store-backed read path)
	resp2, err := http.Get(srv.URL + "/scim/v2/Users/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("get: status=%d", resp2.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp2.Body).Decode(&got)
	if got["userName"] != "persist@example.com" {
		t.Errorf("stored user wrong: %v", got["userName"])
	}
	// list over HTTP must include it
	resp3, err := http.Get(srv.URL + "/scim/v2/Users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	var list map[string]any
	json.NewDecoder(resp3.Body).Decode(&list)
	resources, _ := list["Resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("want 1 persisted user, got %v", resources)
	}
}

func TestSCIMGroupPersistsOverHTTP(t *testing.T) {
	srv := newTestServer(t, true)
	resp, err := http.Post(srv.URL+"/scim/v2/Groups", "application/scim+json",
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"eng-leads"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("group create: status=%d", resp.StatusCode)
	}
	resp2, err := http.Get(srv.URL + "/scim/v2/Groups")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var list map[string]any
	json.NewDecoder(resp2.Body).Decode(&list)
	resources, _ := list["Resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("want 1 persisted group, got %v", resources)
	}
	g := resources[0].(map[string]any)
	if g["displayName"] != "eng-leads" {
		t.Errorf("displayName = %v", g["displayName"])
	}
}
