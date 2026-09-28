// Package scim implements a minimal SCIM 2.0 provider (Users/Groups)
// for the v0.2.0-rc.1 cloud path — feature-flagged, in the self-host
// base. SCIM is never a cloud-only gate.
//
// RFC 7643/7644 subset: ServiceProviderConfig, Users CRUD + pagination,
// Groups list/create. Errors use the SCIM error schema. Storage is
// in-memory; the store-backed provider lands with v0.2.
package scim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	schemaUser  = "urn:ietf:params:scim:schemas:core:2.0:User"
	schemaGroup = "urn:ietf:params:scim:schemas:core:2.0:Group"
	schemaList  = "urn:ietf:params:scim:schemas:core:2.0:ListResponse"
	schemaError = "urn:ietf:params:scim:schemas:core:2.0:Error"
)

// Provider is a minimal in-memory SCIM provider.
type Provider struct {
	mu     sync.RWMutex
	users  map[string]map[string]any
	groups map[string]map[string]any
	next   int
}

// NewProvider creates an empty provider.
func NewProvider() *Provider {
	return &Provider{
		users:  map[string]map[string]any{},
		groups: map[string]map[string]any{},
	}
}

// Mount registers all SCIM routes under prefix (e.g. /scim/v2).
func (p *Provider) Mount(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/ServiceProviderConfig", p.serviceProviderConfig)
	mux.HandleFunc(prefix+"/Users", p.usersHandler)
	mux.HandleFunc(prefix+"/Users/", p.userHandler)
	mux.HandleFunc(prefix+"/Groups", p.groupsHandler)
}

func (p *Provider) serviceProviderConfig(w http.ResponseWriter, r *http.Request) {
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"documentationUri": "https://proofspan.dev/docs/scim",
		"patch": map[string]any{"supported": false},
		"bulk": map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter": map[string]any{"supported": false, "maxResults": 0},
		"changePassword": map[string]any{"supported": false},
		"sort": map[string]any{"supported": false},
		"etag": map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{{
			"name": "Bearer Token",
			"description": "Static bearer token (v0.1); OIDC in v0.2",
			"specUri": "https://datatracker.ietf.org/doc/html/rfc6750",
			"type": "oauthbearertoken",
			"primary": true,
		}},
	})
}

func (p *Provider) usersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		p.createUser(w, r)
	case http.MethodGet:
		p.listUsers(w, r)
	default:
		scimError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (p *Provider) userHandler(w http.ResponseWriter, r *http.Request) {
	id := lastSegment(r.URL.Path)
	if id == "" {
		scimError(w, http.StatusNotFound, "missing user id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p.getUser(w, r, id)
	case http.MethodPut:
		p.putUser(w, r, id)
	case http.MethodDelete:
		p.deleteUser(w, r, id)
	default:
		scimError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (p *Provider) groupsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		p.createGroup(w, r)
	case http.MethodGet:
		p.listGroups(w, r)
	default:
		scimError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (p *Provider) createUser(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		scimError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body["schemas"] == nil {
		body["schemas"] = []string{schemaUser}
	}
	if body["userName"] == nil || body["userName"].(string) == "" {
		scimError(w, http.StatusBadRequest, "userName required")
		return
	}
	p.mu.Lock()
	p.next++
	id := fmt.Sprintf("usr_%06d", p.next)
	body["id"] = id
	body["meta"] = map[string]any{
		"resourceType": "User",
		"created": time.Now().UTC().Format(time.RFC3339),
		"lastModified": time.Now().UTC().Format(time.RFC3339),
		"location": "/scim/v2/Users/" + id,
	}
	p.users[id] = body
	p.mu.Unlock()
	writeSCIM(w, http.StatusCreated, body)
}

func (p *Provider) listUsers(w http.ResponseWriter, r *http.Request) {
	startIndex, count := pageParams(r)
	p.mu.RLock()
	ids := make([]string, 0, len(p.users))
	for id := range p.users {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := len(ids)
	if startIndex > 0 && startIndex <= len(ids) {
		ids = ids[startIndex-1:]
	} else if startIndex > len(ids) {
		ids = nil
	}
	if count > 0 && len(ids) > count {
		ids = ids[:count]
	}
	resources := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		resources = append(resources, p.users[id])
	}
	p.mu.RUnlock()
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":      []string{schemaList},
		"totalResults": total,
		"startIndex":   startIndex,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	})
}

func (p *Provider) getUser(w http.ResponseWriter, r *http.Request, id string) {
	p.mu.RLock()
	user, ok := p.users[id]
	p.mu.RUnlock()
	if !ok {
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}
	writeSCIM(w, http.StatusOK, user)
}

func (p *Provider) putUser(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		scimError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	p.mu.Lock()
	old, ok := p.users[id]
	if !ok {
		p.mu.Unlock()
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}
	body["id"] = id
	body["meta"] = old["meta"]
	p.users[id] = body
	p.mu.Unlock()
	writeSCIM(w, http.StatusOK, body)
}

func (p *Provider) deleteUser(w http.ResponseWriter, r *http.Request, id string) {
	p.mu.Lock()
	_, ok := p.users[id]
	if ok {
		delete(p.users, id)
	}
	p.mu.Unlock()
	if !ok {
		scimError(w, http.StatusNotFound, fmt.Sprintf("user %q not found", id))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Provider) createGroup(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		scimError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body["schemas"] == nil {
		body["schemas"] = []string{schemaGroup}
	}
	if body["displayName"] == nil || body["displayName"].(string) == "" {
		scimError(w, http.StatusBadRequest, "displayName required")
		return
	}
	p.mu.Lock()
	gid := "grp_" + uuid.NewString()[:8]
	body["id"] = gid
	body["meta"] = map[string]any{
		"resourceType": "Group",
		"created": time.Now().UTC().Format(time.RFC3339),
		"lastModified": time.Now().UTC().Format(time.RFC3339),
		"location": "/scim/v2/Groups/" + gid,
	}
	p.groups[gid] = body
	p.mu.Unlock()
	writeSCIM(w, http.StatusCreated, body)
}

func (p *Provider) listGroups(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	ids := make([]string, 0, len(p.groups))
	for id := range p.groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	resources := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		resources = append(resources, p.groups[id])
	}
	p.mu.RUnlock()
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":      []string{schemaList},
		"totalResults": len(resources),
		"startIndex":   1,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	})
}

func pageParams(r *http.Request) (int, int) {
	q := r.URL.Query()
	start := 1
	if v, err := strconv.Atoi(q.Get("startIndex")); err == nil && v > 0 {
		start = v
	}
	count := 100
	if v, err := strconv.Atoi(q.Get("count")); err == nil && v >= 0 {
		count = v
	}
	return start, count
}

func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

func writeSCIM(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}

func scimError(w http.ResponseWriter, code int, detail string) {
	writeSCIM(w, code, map[string]any{
		"schemas": []string{schemaError},
		"status":  strconv.Itoa(code),
		"detail":  detail,
	})
}
