// Package handoff passes a deployment to the steps after setup-vercel: as
// environment variables, and as a summary stored with the job's artifacts.
package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/vercel"
)

// The variables later steps read the deployment from.
const (
	EnvURL        = "VERCEL_DEPLOYMENT_URL"
	EnvID         = "VERCEL_DEPLOYMENT_ID"
	EnvInspectURL = "VERCEL_INSPECT_URL"
)

// Summary is the deployment record stored as an artifact.
type Summary struct {
	Command              string  `json:"command"`
	URL                  string  `json:"url"`
	DeploymentID         string  `json:"deployment_id"`
	InspectURL           string  `json:"inspect_url,omitempty"`
	Target               string  `json:"target"`
	BuildDurationSeconds float64 `json:"build_duration_seconds,omitempty"`
}

// Publish exports d to later steps and stores its summary as an artifact. Only
// a failure to export fails it: outside a job there is nothing to export to, and
// a missing artifact is not worth failing a deploy over.
func Publish(ctx context.Context, client *sdk.Client, command string, d vercel.Deployment, log io.Writer) error {
	fmt.Fprintf(log, "setup-vercel: %s\n", d.HTTPSURL())

	err := bashenv.ExportEnvVars(map[string]string{
		EnvURL:        d.HTTPSURL(),
		EnvID:         d.DeploymentID(),
		EnvInspectURL: d.InspectorURL,
	})
	switch {
	case errors.Is(err, bashenv.ErrNoBashEnv):
		fmt.Fprintf(log, "setup-vercel: BASH_ENV is not set, so %s is not exported to later steps\n", EnvURL)
	case err != nil:
		return fmt.Errorf("export the deployment to later steps: %w", err)
	}

	storeSummary(ctx, client, Summary{
		Command:              command,
		URL:                  d.HTTPSURL(),
		DeploymentID:         d.DeploymentID(),
		InspectURL:           d.InspectorURL,
		Target:               target(d),
		BuildDurationSeconds: d.BuildDuration().Seconds(),
	}, log)
	return nil
}

func storeSummary(ctx context.Context, client *sdk.Client, s Summary, log io.Writer) {
	dir, err := os.MkdirTemp("", "setup-vercel-")
	if err != nil {
		fmt.Fprintf(log, "setup-vercel: could not write the deployment summary: %v\n", err)
		return
	}
	path := filepath.Join(dir, "deployment.json")
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		fmt.Fprintf(log, "setup-vercel: could not write the deployment summary: %v\n", err)
		return
	}
	err = client.StoreArtifacts(ctx, path, "setup-vercel/"+s.Command+".json")
	if err != nil && !errors.Is(err, sdk.ErrNoServer) {
		fmt.Fprintf(log, "setup-vercel: could not store the deployment summary: %v\n", err)
	}
}

func target(d vercel.Deployment) string {
	if d.Target == "" {
		return "preview"
	}
	return d.Target
}
