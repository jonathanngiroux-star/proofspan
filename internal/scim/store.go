// Store-backed SCIM persistence: scim_users / scim_groups tables in the
// main SQLite database. Resources are stored as canonical JSON documents
// so arbitrary SCIM attributes (name, emails, enterprise extensions)
// round-trip without schema loss.
package scim

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"proofspan/internal/store"
)

// storeSchemaSQL is executed against the main store on first use.
const storeSchemaSQL = `
CREATE TABLE IF NOT EXISTS scim_users (
	id         TEXT PRIMARY KEY,
	user_name  TEXT NOT NULL,
	document   TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scim_users_name ON scim_users(user_name);
CREATE TABLE IF NOT EXISTS scim_groups (
	id          TEXT PRIMARY KEY,
	display_name TEXT NOT NULL,
	document    TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
`

// NewStoreBacked returns a Provider whose Users/Groups persist to st.
// The HTTP handlers are shared with the in-memory provider; the storage
// layer underneath swaps to SQLite.
func NewStoreBacked(st *store.Store) (*Provider, error) {
	if _, err := st.Exec(storeSchemaSQL); err != nil {
		return nil, fmt.Errorf("scim schema: %w", err)
	}
	p := NewProvider()
	p.persist = &sqlPersist{st: st}
	return p, nil
}

type sqlPersist struct {
	st  *store.Store
	mu  sync.Mutex
	seq int
}

// StorePutUser upserts a user by id (generated if absent). Returns the
// stored document.
func (p *Provider) StorePutUser(ctx context.Context, user map[string]any) (map[string]any, error) {
	if p.persist == nil {
		return nil, fmt.Errorf("provider is not store-backed")
	}
	userName, _ := user["userName"].(string)
	if userName == "" {
		return nil, fmt.Errorf("userName required")
	}
	id, _ := user["id"].(string)
	now := time.Now().UTC()
	doc := cloneMap(user)
	if id == "" {
		var err error
		id, err = p.persist.nextID("usr")
		if err != nil {
			return nil, err
		}
	}
	doc["id"] = id
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	_, err = p.persist.st.Exec(`
		INSERT INTO scim_users (id, user_name, document, created_at, updated_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET user_name=excluded.user_name,
			document=excluded.document, updated_at=excluded.updated_at`,
		id, userName, string(raw), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("put user: %w", err)
	}
	return doc, nil
}

func (p *Provider) StoreGetUser(ctx context.Context, id string) (map[string]any, error) {
	if p.persist == nil {
		return nil, fmt.Errorf("provider is not store-backed")
	}
	var doc string
	err := p.persist.st.QueryRow(`SELECT document FROM scim_users WHERE id = ?`, id).Scan(&doc)
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", id, err)
	}
	return unmarshalDoc(doc)
}

func (p *Provider) StoreDeleteUser(ctx context.Context, id string) error {
	if p.persist == nil {
		return fmt.Errorf("provider is not store-backed")
	}
	res, err := p.persist.st.Exec(`DELETE FROM scim_users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %s not found", id)
	}
	return nil
}

// StoreListUsers returns one page (startIndex is 1-based) plus the total.
func (p *Provider) StoreListUsers(ctx context.Context, startIndex, count int) ([]map[string]any, int, error) {
	if p.persist == nil {
		return nil, 0, fmt.Errorf("provider is not store-backed")
	}
	rows, err := p.persist.st.Query(`SELECT document FROM scim_users ORDER BY user_name LIMIT ? OFFSET ?`, count, startIndex-1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var users []map[string]any
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, 0, err
		}
		u, err := unmarshalDoc(doc)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	var total int
	if err := p.persist.st.QueryRow(`SELECT COUNT(*) FROM scim_users`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return users, total, rows.Err()
}

func (p *Provider) StorePutGroup(ctx context.Context, group map[string]any) (map[string]any, error) {
	if p.persist == nil {
		return nil, fmt.Errorf("provider is not store-backed")
	}
	displayName, _ := group["displayName"].(string)
	if displayName == "" {
		return nil, fmt.Errorf("displayName required")
	}
	id, _ := group["id"].(string)
	now := time.Now().UTC()
	doc := cloneMap(group)
	if id == "" {
		var err error
		id, err = p.persist.nextID("grp")
		if err != nil {
			return nil, err
		}
	}
	doc["id"] = id
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	_, err = p.persist.st.Exec(`
		INSERT INTO scim_groups (id, display_name, document, created_at, updated_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,
			document=excluded.document, updated_at=excluded.updated_at`,
		id, displayName, string(raw), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("put group: %w", err)
	}
	return doc, nil
}

func (p *Provider) StoreGetGroup(ctx context.Context, id string) (map[string]any, error) {
	if p.persist == nil {
		return nil, fmt.Errorf("provider is not store-backed")
	}
	var doc string
	if err := p.persist.st.QueryRow(`SELECT document FROM scim_groups WHERE id = ?`, id).Scan(&doc); err != nil {
		return nil, fmt.Errorf("get group %s: %w", id, err)
	}
	return unmarshalDoc(doc)
}

func (p *Provider) StoreListGroups(ctx context.Context, startIndex, count int) ([]map[string]any, int, error) {
	if p.persist == nil {
		return nil, 0, fmt.Errorf("provider is not store-backed")
	}
	rows, err := p.persist.st.Query(`SELECT document FROM scim_groups ORDER BY display_name LIMIT ? OFFSET ?`, count, startIndex-1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var groups []map[string]any
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, 0, err
		}
		g, err := unmarshalDoc(doc)
		if err != nil {
			return nil, 0, err
		}
		groups = append(groups, g)
	}
	var total int
	if err := p.persist.st.QueryRow(`SELECT COUNT(*) FROM scim_groups`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return groups, total, rows.Err()
}

// nextID mints a sortable unique id: <prefix>_<unixms>_<counter>.
func (s *sqlPersist) nextID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("%s_%d_%04d", prefix, time.Now().UnixMilli(), s.seq), nil
}

func unmarshalDoc(doc string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		return nil, fmt.Errorf("stored document corrupt: %w", err)
	}
	return m, nil
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
