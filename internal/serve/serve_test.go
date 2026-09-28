package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"proofspan/internal/schema"
	"proofspan/internal/store"
)

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
	srv := httptest.NewServer(NewServer(st, scimOn).Handler())
	t.Cleanup(srv.Close)
	return srv
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
