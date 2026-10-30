// Package fakevercel is an in-memory stand-in for the parts of the Vercel API
// setup-vercel calls.
package fakevercel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Token is the only bearer token the server accepts.
const Token = "testtoken" // The Vercel CLI rejects a token with a dash in it.

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
	Meta                map[string]string

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
	created     []map[string]any
	unhandled   []string
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
	// What the Vercel CLI calls for pull and deploy.
	mux.HandleFunc("GET /v2/user", s.user)
	mux.HandleFunc("GET /teams/{id}", s.team)
	mux.HandleFunc("GET /v2/teams/{id}", s.team)
	mux.HandleFunc("GET /v3/env/pull/{project}/{target}", s.envPull)
	mux.HandleFunc("POST /v2/files", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]any{}) })
	mux.HandleFunc("POST /v13/deployments", s.create)
	mux.HandleFunc("/", s.notFound)
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

// Created returns the request bodies of the deployments created so far.
func (s *Server) Created() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.created)
}

// Unhandled returns the requests no route matched, which a test should fail on:
// the Vercel CLI calls something the fake does not implement.
func (s *Server) Unhandled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.unhandled)
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

func (s *Server) user(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"user": map[string]any{
		"id": "user_1", "uid": "user_1", "username": "tester", "email": "tester@example.com", "defaultTeamId": "team_1", "version": "northstar",
	}})
}

func (s *Server) team(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"id": r.PathValue("id"), "slug": "acme", "name": "Acme"})
}

func (s *Server) envPull(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("project") != s.ProjectID {
		writeError(w, http.StatusNotFound, "not_found", "Project not found")
		return
	}
	writeJSON(w, map[string]any{"env": map[string]string{"GREETING": "hello from " + r.PathValue("target")}})
}

// create makes a READY deployment from what the CLI sends.
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, body)
	n := len(s.created)
	d := &Deployment{ID: "dpl_cli" + strconv.Itoa(n), URL: "my-app-cli" + strconv.Itoa(n) + ".vercel.app"}
	if t, _ := body["target"].(string); t != "" {
		d.Target = t
	}
	if meta, ok := body["meta"].(map[string]any); ok {
		d.Meta = map[string]string{}
		for k, v := range meta {
			if s, ok := v.(string); ok {
				d.Meta[k] = s
			}
		}
	}
	s.deployments = append(s.deployments, d)
	resp := d.json("id")
	resp["name"] = "my-app"
	resp["projectId"] = s.ProjectID
	resp["ownerId"] = "team_1"
	resp["createdAt"] = 1
	resp["alias"] = []string{}
	resp["build"] = map[string]any{"env": []string{}}
	resp["version"] = 2
	resp["type"] = "LAMBDAS"
	resp["status"] = d.state()
	resp["creator"] = map[string]string{"uid": "user_1"}
	writeJSON(w, resp)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.unhandled = append(s.unhandled, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	writeError(w, http.StatusNotFound, "not_found", "The fake Vercel API has no route for this")
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
		"meta":         d.meta(),
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

func (d *Deployment) meta() map[string]string {
	m := map[string]string{}
	for k, v := range d.Meta {
		m[k] = v
	}
	if d.SHA != "" {
		m["githubCommitSha"] = d.SHA
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
