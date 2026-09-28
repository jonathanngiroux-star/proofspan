package scim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Minimal SCIM 2.0 provider surface (RFC 7643/7644 subset):
//   GET    /scim/v2/Users           list (paginated, startIndex/count)
//   GET    /scim/v2/Users/{id}     get one
//   POST   /scim/v2/Users           create
//   PUT   /scim/v2/Users/{id}     replace
//   DELETE /scim/v2/Users/{id}     delete (returns 204)
//   GET    /scim/v2/Groups        list groups
//   POST   /scim/v2/Groups          create group
// Schemas pinned: "urn:ietf:params:scim:schemas:core:2.0:User" / Group.

func newTestServer(t *testing.T) (*httptest.Server, *Provider) {
	t.Helper()
	p := NewProvider()
	mux := http.NewServeMux()
	p.Mount(mux, "/scim/v2")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, p
}

func TestServiceProviderConfig(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/scim/v2/ServiceProviderConfig")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var cfg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["authenticationSchemes"] == nil {
		t.Error("ServiceProviderConfig must advertise authentication schemes")
	}
}

func TestCreateAndGetUser(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"eng@example.com","name":{"givenName":"Ada","familyName":"Lovelace"},"emails":[{"value":"eng@example.com","primary":true}],"active":true}`
	resp, err := http.Post(srv.URL+"/scim/v2/Users", "application/scim+json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	id, _ := user["id"].(string)
	if id == "" {
		t.Fatal("created user has no id")
	}
	if user["userName"] != "eng@example.com" {
		t.Errorf("userName = %v", user["userName"])
	}
	resp2, err := http.Get(srv.URL + "/scim/v2/Users/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("get status = %d", resp2.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp2.Body).Decode(&got)
	if got["id"] != id || got["userName"] != "eng@example.com" {
		t.Errorf("get round-trip wrong: %v", got)
	}
}

func TestListUsersPaginated(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, n := range []string{"a@x", "b@x", "c@x"} {
		http.Post(srv.URL+"/scim/v2/Users", "application/scim+json",
			strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"`+n+`"}`))
	}
	resp, err := http.Get(srv.URL + "/scim/v2/Users?count=2&startIndex=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	resources, ok := list["Resources"].([]any)
	if !ok || len(resources) != 2 {
		t.Fatalf("want 2 resources with count=2, got %v", list["Resources"])
	}
	if list["totalResults"] != float64(3) {
		t.Errorf("totalResults = %v, want 3", list["totalResults"])
	}
}

func TestGetMissingUserIs404(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/scim/v2/Users/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteUserReturns204(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, _ := http.Post(srv.URL+"/scim/v2/Users", "application/scim+json",
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"del@x"}`))
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/scim/v2/Users/"+user["id"].(string), nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 204 {
		t.Fatalf("delete status = %d, want 204", resp2.StatusCode)
	}
	resp3, _ := http.Get(srv.URL + "/scim/v2/Users/" + user["id"].(string))
	resp3.Body.Close()
	if resp3.StatusCode != 404 {
		t.Errorf("deleted user still there: %d", resp3.StatusCode)
	}
}

func TestGroupsCRUD(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/scim/v2/Groups", "application/scim+json",
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"eng-leads"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("group create status = %d", resp.StatusCode)
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
		t.Fatalf("want 1 group, got %v", resources)
	}
	g := resources[0].(map[string]any)
	if g["displayName"] != "eng-leads" {
		t.Errorf("displayName = %v", g["displayName"])
	}
}

func TestListResponseSchemaPinned(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/scim/v2/Users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	schemas, ok := list["schemas"].([]any)
	if !ok || len(schemas) == 0 {
		t.Fatal("ListResponse must pin its schema")
	}
	if !strings.Contains(schemas[0].(string), "ListResponse") {
		t.Errorf("schema[0] = %v", schemas[0])
	}
}
