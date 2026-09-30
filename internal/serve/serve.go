// Package serve implements `proofspan serve`: a local HTTP server with
// health, trajectory read API, and the feature-flagged SCIM provider.
// No orchestration UI — until 10 invoiced Cloud Pro teams, this stays a stub.
package serve

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jonathanngiroux-star/proofspan/internal/scim"
	"github.com/jonathanngiroux-star/proofspan/internal/store"
)

// Server is the local proofspan server.
type Server struct {
	store    *store.Store
	provider *scim.Provider
	scimOn   bool
}

// NewServer builds the server. scimEnabled toggles the SCIM minimal
// provider; users/groups persist to the same SQLite file as trajectories.
// scimToken, when non-empty, enforces bearer auth on SCIM endpoints;
// empty token = documented dev mode (bind 127.0.0.1).
func NewServer(st *store.Store, scimEnabled bool, scimToken string) (*Server, error) {
	s := &Server{store: st, scimOn: scimEnabled}
	if scimEnabled {
		p, err := scim.NewStoreBacked(st)
		if err != nil {
			return nil, err
		}
		if scimToken != "" {
			p.RequireBearer(scimToken)
		}
		s.provider = p
	}
	return s, nil
}

// Handler builds the HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/trajectories", func(w http.ResponseWriter, r *http.Request) {
		ids, err := s.store.ListTrajectoryIDs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"trajectories": ids})
	})
	mux.HandleFunc("/v1/trajectories/", func(w http.ResponseWriter, r *http.Request) {
		id := ""
		for i := len(r.URL.Path) - 1; i >= 0; i-- {
			if r.URL.Path[i] == '/' {
				id = r.URL.Path[i+1:]
				break
			}
		}
		tr, spans, err := s.store.GetTrajectory(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"trajectory": tr, "spans": spans})
	})
	if s.scimOn && s.provider != nil {
		s.provider.Mount(mux, "/scim/v2")
	}
	return mux
}
