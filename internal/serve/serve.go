// Package serve implements `proofspan serve`: a local HTTP server with
// health, trajectory read API, and the feature-flagged SCIM provider.
// No orchestration UI — until 10 invoiced Cloud Pro teams, this stays a stub.
package serve

import (
	"encoding/json"
	"fmt"
	"net/http"

	"proofspan/internal/scim"
	"proofspan/internal/store"
)

// Server is the local proofspan server.
type Server struct {
	store    *store.Store
	provider *scim.Provider
	scimOn   bool
}

// NewServer builds the server. scimEnabled toggles the SCIM minimal provider.
func NewServer(st *store.Store, scimEnabled bool) *Server {
	s := &Server{store: st, scimOn: scimEnabled}
	if scimEnabled {
		s.provider = scim.NewProvider()
	}
	return s
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
