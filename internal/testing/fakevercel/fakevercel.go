// Package fakevercel is an in-memory stand-in for the parts of the Vercel API
// setup-vercel calls.
package fakevercel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Token is the only bearer token the server accepts.
const Token = "test-token"

// Deployment is a deployment the server knows. States are reported in turn,
// one per lookup, and the last one sticks.
type Deployment struct {
	ID                  string
	URL                 string
	Target              string
	SHA                 string
	States              []string
	ErrorMessage        string
	IsRollbackCandidate *bool
	BuildLog            []string

	lookups int
}

// Server is a fake Vercel API for one project.
type Server struct {
	URL       string
	ProjectID string

	mu          sync.Mutex
	deployments []*Deployment
	production  string
	linked      bool
	aliasJob    string
	requests    []string
}

// New starts a server for projectID, registering cleanup with t.
func New(t *testing.T, projectID string) *Server {
	t.Helper()
	s := &Server{ProjectID: projectID, aliasJob: "succeeded"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v9/projects/{id}", s.project)
	mux.HandleFunc("GET /v7/deployments", s.list)
	mux.HandleFunc("GET /v13/deployments/{id}", s.deployment)
	mux.HandleFunc("GET /v3/deployments/{id}/events", s.events)
	mux.HandleFunc("POST /v10/projects/{project}/promote/{id}", s.alias("promote"))
	mux.HandleFunc("POST /v1/projects/{project}/rollback/{id}", s.alias("rollback"))
	srv := httptest.NewServer(s.auth(mux))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Add registers a deployment.
func (s *Server) Add(d *Deployment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deployments = append(s.deployments, d)
}

// SetProduction makes id the deployment production points at.
func (s *Server) SetProduction(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.production = id
}

// Production returns the deployment production points at.
func (s *Server) Production() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.production
}

// SetGitLinked reports the project as connected to a Git repository.
func (s *Server) SetGitLinked(linked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linked = linked
}

// FailAliasJobs makes every promote and rollback fail on Vercel's side.
func (s *Server) FailAliasJobs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aliasJob = "failed"
}

// Requests returns "METHOD path?query" for every request so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+Token {
			writeError(w, http.StatusUnauthorized, "forbidden", "The request is missing an authentication token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != s.ProjectID {
		writeError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := map[string]any{"id": s.ProjectID, "name": "my-app"}
	if s.linked {
		p["link"] = map[string]string{"type": "github"}
	}
	if s.production != "" {
		p["targets"] = map[string]any{"production": map[string]string{"id": s.production}}
		p["lastAliasRequest"] = map[string]string{"toDeploymentId": s.production, "jobStatus": s.aliasJob}
	}
	writeJSON(w, p)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, d := range slices.Backward(s.deployments) {
		if sha := q.Get("sha"); sha != "" && d.SHA != sha {
			continue
		}
		if q.Get("target") == "production" && d.Target != "production" {
			continue
		}
		if st := q.Get("state"); st != "" && d.state() != st {
			continue
		}
		out = append(out, d.json("uid"))
	}
	writeJSON(w, map[string]any{"deployments": out})
}

func (s *Server) deployment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.find(r.PathValue("id"))
	if d == nil {
		writeError(w, http.StatusNotFound, "not_found", "Deployment not found")
		return
	}
	body := d.json("id")
	if d.lookups < len(d.States)-1 {
		d.lookups++
	}
	writeJSON(w, body)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.find(r.PathValue("id"))
	if d == nil {
		writeError(w, http.StatusNotFound, "not_found", "Deployment not found")
		return
	}
	var events []map[string]string
	for _, line := range d.BuildLog {
		events = append(events, map[string]string{"type": "stdout", "text": line})
	}
	writeJSON(w, events)
}

func (s *Server) alias(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		d := s.find(r.PathValue("id"))
		if r.PathValue("project") != s.ProjectID || d == nil {
			writeError(w, http.StatusNotFound, "not_found", "Not found")
			return
		}
		if kind == "promote" && d.Target != "production" {
			writeError(w, http.StatusBadRequest, "bad_request", "Cannot promote a preview")
			return
		}
		s.production = d.ID
		w.WriteHeader(http.StatusCreated)
	}
}

// find matches a deployment by ID or host. Callers hold s.mu.
func (s *Server) find(idOrHost string) *Deployment {
	for _, d := range s.deployments {
		if d.ID == idOrHost || d.URL == idOrHost {
			return d
		}
	}
	return nil
}

func (d *Deployment) state() string {
	if len(d.States) == 0 {
		return "READY"
	}
	return d.States[d.lookups]
}

func (d *Deployment) json(idKey string) map[string]any {
	m := map[string]any{
		idKey:          d.ID,
		"url":          d.URL,
		"readyState":   d.state(),
		"inspectorUrl": "https://vercel.com/acme/my-app/" + strings.TrimPrefix(d.ID, "dpl_"),
		"meta":         map[string]string{"githubCommitSha": d.SHA},
	}
	if d.Target != "" {
		m["target"] = d.Target
	}
	if d.ErrorMessage != "" {
		m["errorMessage"] = d.ErrorMessage
	}
	if d.IsRollbackCandidate != nil {
		m["isRollbackCandidate"] = *d.IsRollbackCandidate
	}
	return m
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
