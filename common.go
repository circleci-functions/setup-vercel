package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/vercel"
	"github.com/circleci-functions/setup-vercel/vercelcli"
)

// The variables setup-vercel reads its credentials from: the Vercel CLI's own.
const (
	envToken     = "VERCEL_TOKEN" //nolint:gosec // the variable's name, not a credential
	envOrgID     = "VERCEL_ORG_ID"
	envProjectID = "VERCEL_PROJECT_ID"
)

type credentials struct {
	Token     string
	OrgID     string
	ProjectID string
}

// readCredentials fails, naming every missing variable, before any work starts.
func readCredentials() (credentials, error) {
	c := credentials{
		Token:     os.Getenv(envToken),
		OrgID:     os.Getenv(envOrgID),
		ProjectID: os.Getenv(envProjectID),
	}
	var missing []string
	for name, v := range map[string]string{envToken: c.Token, envOrgID: c.OrgID, envProjectID: c.ProjectID} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return c, nil
	}
	slices.Sort(missing)
	return c, fmt.Errorf("%s not set: add %s, %s and %s to the job, for example from a context. "+
		"The IDs are orgId and projectId in .vercel/project.json once `vercel link` has run",
		joinNames(missing), envToken, envOrgID, envProjectID)
}

// apiFlags are the test hooks every command takes.
type apiFlags struct {
	APIURL       string `name:"api-url" hidden:"" default:"https://api.vercel.com" help:"Vercel API base URL."`
	PollInterval string `name:"poll-interval" hidden:"" default:"5s" help:"How often to poll Vercel while waiting."`
}

// installFlags choose the Vercel CLI the commands that need it install.
type installFlags struct {
	CLIVersion string `name:"cli-version" default:"" help:"Vercel CLI version to install. Empty installs ${vercel_version}."`
	InstallDir string `name:"install-dir" hidden:"" default:"~/.cache/setup-vercel" help:"Base directory for Vercel CLI installs."`
	BinDir     string `name:"bin-dir" hidden:"" default:"" help:"Link vercel into this directory instead of discovering one on PATH."`
	CacheNonce string `name:"cache-nonce" hidden:"" default:"" help:"Partition the Vercel CLI cache."`
}

func (f installFlags) install(ctx context.Context, client *sdk.Client, token string) (vercelcli.CLI, error) {
	version := f.CLIVersion
	if version == "" {
		version = vercelcli.DefaultVersion
	}
	cli, err := vercelcli.Installer{
		Dir:        expandHome(f.InstallDir),
		Version:    version,
		Client:     client,
		CacheNonce: f.CacheNonce,
		Log:        os.Stderr,
	}.Ensure(ctx)
	cli.Token = token
	return cli, err
}

// session is what every command starts from: credentials checked against the
// project they name.
type session struct {
	creds   credentials
	api     *vercel.Client
	project vercel.Project
	poll    time.Duration
}

func (f apiFlags) start(ctx context.Context) (session, error) {
	poll, err := parseDuration("poll-interval", f.PollInterval)
	if err != nil {
		return session{}, err
	}
	creds, err := readCredentials()
	if err != nil {
		return session{}, err
	}
	api := vercel.New(f.APIURL, creds.Token, creds.OrgID)
	project, err := api.Project(ctx, creds.ProjectID)
	if err != nil {
		return session{}, projectError(err, creds)
	}
	return session{creds: creds, api: api, project: project, poll: poll}, nil
}

func (s session) waiter(timeout time.Duration) vercel.Waiter {
	return vercel.Waiter{Client: s.api, Timeout: timeout, Interval: s.poll, Log: os.Stderr}
}

func projectError(err error, c credentials) error {
	var apiErr *vercel.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("look up the Vercel project: %w", err)
	}
	switch apiErr.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("vercel rejected %s: create a new token at https://vercel.com/account/tokens (%w)", envToken, err)
	case http.StatusForbidden:
		return fmt.Errorf("%s cannot reach the project %s in %s: check the token's scope includes that team (%w)", envToken, c.ProjectID, c.OrgID, err)
	case http.StatusNotFound:
		return fmt.Errorf("vercel has no project %s in %s: run `vercel link` in the project and copy orgId and projectId from .vercel/project.json into %s and %s (%w)",
			c.ProjectID, c.OrgID, envOrgID, envProjectID, err)
	}
	return fmt.Errorf("look up the Vercel project: %w", err)
}

// parseDuration reads a duration flag. Durations are strings in the config, so
// that the published flag type does not make a config check expect an integer.
func parseDuration(flag, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--%s=%q: want a positive duration such as 10m or 90s", flag, s)
	}
	return d, nil
}

// expandHome resolves a leading ~ against the home directory, which kong leaves
// alone. The tag holds ~/ so function.yaml publishes a readable default.
func expandHome(path string) string {
	if path == "" || path[0] != '~' || (len(path) > 1 && !os.IsPathSeparator(path[1])) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

func joinNames(names []string) string {
	switch len(names) {
	case 1:
		return names[0] + " is"
	case 2:
		return names[0] + " and " + names[1] + " are"
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " are"
}
