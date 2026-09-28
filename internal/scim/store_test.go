package scim

import (
	"context"
	"encoding/json"
	"testing"

	"proofspan/internal/store"
)

// Store-backed SCIM: users and groups must survive restarts. The in-memory
// provider stays for tests/dev; NewStoreBacked wires SQLite persistence.

func newStoreBackedProvider(t *testing.T) *Provider {
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
	return p
}

func TestStoreBackedUserCRUDPersists(t *testing.T) {
	p := newStoreBackedProvider(t)
	ctx := context.Background()
	u, err := p.StorePutUser(ctx, map[string]any{"userName": "ada@example.com", "active": true})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := u["id"].(string)
	if id == "" {
		t.Fatal("created user has no id")
	}
	// read back through the store path
	got, err := p.StoreGetUser(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got["userName"] != "ada@example.com" {
		t.Errorf("round-trip: %v", got["userName"])
	}
	// update
	got["displayName"] = "Ada Lovelace"
	if _, err := p.StorePutUser(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := p.StoreGetUser(ctx, id)
	if again["displayName"] != "Ada Lovelace" {
		t.Errorf("update lost: %v", again["displayName"])
	}
	// delete
	if err := p.StoreDeleteUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := p.StoreGetUser(ctx, id); err == nil {
		t.Error("deleted user still readable")
	}
}

func TestStoreBackedUsersSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/scim.sqlite"
	st1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := NewStoreBacked(st1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := p1.StorePutUser(ctx, map[string]any{"userName": "restart@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	st1.Close() // simulate restart
	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	p2, err := NewStoreBacked(st2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p2.StoreGetUser(ctx, u["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got["userName"] != "restart@example.com" {
		t.Errorf("user lost across reopen: %v", got)
	}
}

func TestStoreBackedListPaginates(t *testing.T) {
	p := newStoreBackedProvider(t)
	ctx := context.Background()
	for _, n := range []string{"a@x", "b@x", "c@x", "d@x"} {
		if _, err := p.StorePutUser(ctx, map[string]any{"userName": n}); err != nil {
			t.Fatal(err)
		}
	}
	users, total, err := p.StoreListUsers(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("total = %d", total)
	}
	if len(users) != 2 {
		t.Errorf("page size = %d", len(users))
	}
	users2, _, err := p.StoreListUsers(ctx, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(users2) != 2 {
		t.Errorf("second page = %d", len(users2))
	}
}

func TestStoreBackedGroups(t *testing.T) {
	p := newStoreBackedProvider(t)
	ctx := context.Background()
	g, err := p.StorePutGroup(ctx, map[string]any{"displayName": "eng-leads"})
	if err != nil {
		t.Fatal(err)
	}
	if g["id"].(string) == "" {
		t.Fatal("group id empty")
	}
	got, err := p.StoreGetGroup(ctx, g["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got["displayName"] != "eng-leads" {
		t.Errorf("group round-trip: %v", got)
	}
	groups, total, err := p.StoreListGroups(ctx, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(groups) != 1 {
		t.Errorf("groups: total=%d len=%d", total, len(groups))
	}
}

func TestStoreBackedJSONRoundTrip(t *testing.T) {
	// arbitrary SCIM attributes (name, emails, enterprise extension) must
	// survive storage without schema loss
	p := newStoreBackedProvider(t)
	ctx := context.Background()
	in := map[string]any{
		"userName": "full@example.com",
		"name":     map[string]any{"givenName": "Grace", "familyName": "Hopper"},
		"emails":   []any{map[string]any{"value": "full@example.com", "primary": true}},
		"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User": map[string]any{
			"employeeNumber": "42", "department": "Eng",
		},
	}
	u, err := p.StorePutUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(u)
	var back map[string]any
	json.Unmarshal(raw, &back)
	name, _ := back["name"].(map[string]any)
	if name["givenName"] != "Grace" {
		t.Errorf("nested attribute lost: %v", back["name"])
	}
}
