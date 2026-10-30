// Package vercel is a small client for the parts of the Vercel REST API that
// setup-vercel needs: deployments, promotion and rollback.
package vercel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIURL is the Vercel REST API.
const DefaultAPIURL = "https://api.vercel.com"

// Deployment states, as Vercel reports them in readyState.
const (
	StateQueued       = "QUEUED"
	StateInitializing = "INITIALIZING"
	StateBuilding     = "BUILDING"
	StateReady        = "READY"
	StateError        = "ERROR"
	StateCanceled     = "CANCELED"
	StateBlocked      = "BLOCKED"
	StateDeleted      = "DELETED"
)

// Deployment is the subset of a Vercel deployment setup-vercel reads.
type Deployment struct {
	ID           string            `json:"id"`
	UID          string            `json:"uid"`
	URL          string            `json:"url"`
	ReadyState   string            `json:"readyState"`
	State        string            `json:"state"`
	Target       string            `json:"target"`
	InspectorURL string            `json:"inspectorUrl"`
	ProjectID    string            `json:"projectId"`
	ErrorMessage string            `json:"errorMessage"`
	BuildingAt   int64             `json:"buildingAt"`
	Ready        int64             `json:"ready"`
	CreatedAt    int64             `json:"createdAt"`
	Meta         map[string]string `json:"meta"`
	// IsRollbackCandidate is nil when Vercel does not say.
	IsRollbackCandidate *bool `json:"isRollbackCandidate"`
}

// DeploymentID returns the deployment's ID: single-deployment responses call it
// id, list responses uid.
func (d Deployment) DeploymentID() string {
	if d.ID != "" {
		return d.ID
	}
	return d.UID
}

// Status returns the deployment's state, from readyState or, failing that, state.
func (d Deployment) Status() string {
	if d.ReadyState != "" {
		return d.ReadyState
	}
	return d.State
}

// HTTPSURL returns the deployment's URL with a scheme. Vercel reports the bare host.
func (d Deployment) HTTPSURL() string {
	if d.URL == "" || strings.Contains(d.URL, "://") {
		return d.URL
	}
	return "https://" + d.URL
}

// BuildDuration is how long the build took, or zero if it has not finished.
func (d Deployment) BuildDuration() time.Duration {
	if d.BuildingAt == 0 || d.Ready == 0 || d.Ready < d.BuildingAt {
		return 0
	}
	return time.Duration(d.Ready-d.BuildingAt) * time.Millisecond
}

// Project is the subset of a Vercel project setup-vercel reads.
type Project struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RootDirectory string `json:"rootDirectory"`
	Link          *struct {
		Type string `json:"type"`
	} `json:"link"`
	Targets map[string]*struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"targets"`
	LastAliasRequest *AliasRequest `json:"lastAliasRequest"`
}

// ProductionID returns the ID of the deployment production points at, or "".
func (p Project) ProductionID() string {
	if t := p.Targets["production"]; t != nil {
		return t.ID
	}
	return ""
}

// AliasRequest is the project's latest promote or rollback.
type AliasRequest struct {
	Type           string `json:"type"`
	ToDeploymentID string `json:"toDeploymentId"`
	JobStatus      string `json:"jobStatus"`
}

// APIError is a non-2xx answer from the Vercel API.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("vercel API: %s (%d %s)", msg, e.Status, e.Code)
	}
	return fmt.Sprintf("vercel API: %s (%d)", msg, e.Status)
}

// IsNotFound reports whether err is a 404 from the Vercel API.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Client calls the Vercel API with one token, on behalf of one team.
type Client struct {
	baseURL string
	token   string
	teamID  string
	http    *http.Client
}

// New returns a client for baseURL. orgID is the Vercel scope the project
// lives in; a team's ID is sent with every request, a personal account's is not
// needed.
func New(baseURL, token, orgID string) *Client {
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	if strings.HasPrefix(orgID, "team_") {
		c.teamID = orgID
	}
	return c
}

// Deployment fetches a deployment by its ID or URL.
func (c *Client) Deployment(ctx context.Context, idOrURL string) (Deployment, error) {
	var d Deployment
	err := c.do(ctx, http.MethodGet, "/v13/deployments/"+url.PathEscape(hostOf(idOrURL)), &d)
	return d, err
}

// ListOptions filters ListDeployments.
type ListOptions struct {
	ProjectID string
	SHA       string
	// Target is "production" or "preview". Vercel only filters on production,
	// so preview is applied here.
	Target string
	State  string
	Limit  int
}

// ListDeployments lists a project's deployments, newest first.
func (c *Client) ListDeployments(ctx context.Context, opts ListOptions) ([]Deployment, error) {
	q := url.Values{}
	q.Set("projectId", opts.ProjectID)
	if opts.SHA != "" {
		q.Set("sha", opts.SHA)
	}
	if opts.Target == "production" {
		q.Set("target", "production")
	}
	if opts.State != "" {
		q.Set("state", opts.State)
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 20
	}
	q.Set("limit", fmt.Sprint(limit))

	var resp struct {
		Deployments []Deployment `json:"deployments"`
	}
	if err := c.do(ctx, http.MethodGet, "/v7/deployments?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	if opts.Target != "preview" {
		return resp.Deployments, nil
	}
	var previews []Deployment
	for _, d := range resp.Deployments {
		if d.Target == "" || d.Target == "preview" {
			previews = append(previews, d)
		}
	}
	return previews, nil
}

// Project fetches a project by its ID or name.
func (c *Client) Project(ctx context.Context, idOrName string) (Project, error) {
	var p Project
	err := c.do(ctx, http.MethodGet, "/v9/projects/"+url.PathEscape(idOrName), &p)
	return p, err
}

// Promote points the project's production domains at deploymentID.
func (c *Client) Promote(ctx context.Context, projectID, deploymentID string) error {
	path := fmt.Sprintf("/v10/projects/%s/promote/%s", url.PathEscape(projectID), url.PathEscape(deploymentID))
	return c.do(ctx, http.MethodPost, path, nil)
}

// Rollback points the project's production domains back at deploymentID, an
// earlier production deployment.
func (c *Client) Rollback(ctx context.Context, projectID, deploymentID string) error {
	path := fmt.Sprintf("/v1/projects/%s/rollback/%s", url.PathEscape(projectID), url.PathEscape(deploymentID))
	return c.do(ctx, http.MethodPost, path, nil)
}

// BuildLog returns the text of a deployment's build output.
func (c *Client) BuildLog(ctx context.Context, idOrURL string) (string, error) {
	var events []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Payload struct {
			Text string `json:"text"`
		} `json:"payload"`
	}
	path := "/v3/deployments/" + url.PathEscape(hostOf(idOrURL)) + "/events?builds=1&direction=forward&limit=-1"
	if err := c.do(ctx, http.MethodGet, path, &events); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, e := range events {
		text := e.Text
		if text == "" {
			text = e.Payload.Text
		}
		if text == "" {
			continue
		}
		sb.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			sb.WriteByte('\n')
		}
	}
	return sb.String(), nil
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	u := c.baseURL + path
	if c.teamID != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		u += sep + "teamId=" + url.QueryEscape(c.teamID)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("vercel API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		return apiError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("vercel API: decode %s %s: %w", method, path, err)
	}
	return nil
}

func apiError(resp *http.Response) error {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	return &APIError{Status: resp.StatusCode, Code: body.Error.Code, Message: body.Error.Message}
}

// hostOf strips a scheme and path from a deployment URL, since the API takes
// the bare host. An ID passes through unchanged.
func hostOf(idOrURL string) string {
	s := idOrURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
