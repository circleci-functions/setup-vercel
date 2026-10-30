package main

import (
	"context"
	"errors"
	"os"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/handoff"
	"github.com/circleci-functions/setup-vercel/vercelcli"
)

type deployCmd struct {
	installFlags `embed:""`
	apiFlags     `embed:""`

	Environment string `enum:"preview,production" default:"preview" help:"Environment to deploy to: preview or production." example:"production"`
	Staged      bool   `help:"With environment production, deploy without moving the production domains to it, so it can be tested and then promoted."`
	Prebuilt    bool   `default:"true" negatable:"" help:"Build in the job with vercel build and upload the output. Set to false to build on Vercel instead."`
	ProjectDir  string `name:"project-dir" default:"." help:"Directory to deploy. In a monorepo, leave it at the repository root: the Vercel CLI applies the project's Root Directory setting." example:"apps/web"`
	Timeout     string `default:"10m" help:"How long to wait for the deployment to be ready. It keeps running on Vercel after that."`
}

func (c *deployCmd) Run(ctx context.Context, client *sdk.Client) error {
	if c.Staged && c.Environment != "production" {
		return errors.New("--staged needs --environment=production: a preview never takes the production domains anyway")
	}
	timeout, err := parseDuration("timeout", c.Timeout)
	if err != nil {
		return err
	}
	s, err := c.start(ctx)
	if err != nil {
		return err
	}
	cli, err := c.install(ctx, client, s.creds.Token, c.APIURL)
	if err != nil {
		return err
	}
	if err := cli.Pull(ctx, c.ProjectDir, c.Environment); err != nil {
		return err
	}

	if c.Prebuilt {
		if _, err := cli.Run(ctx, c.ProjectDir, c.buildArgs()...); err != nil {
			return err
		}
	}
	out, err := cli.Run(ctx, c.ProjectDir, c.deployArgs()...)
	if err != nil {
		return err
	}
	ref, err := vercelcli.DeploymentRef(out)
	if err != nil {
		return err
	}

	d, err := s.waiter(timeout).Ready(ctx, ref)
	if err != nil {
		return err
	}
	return handoff.Publish(ctx, client, "deploy", d, os.Stderr)
}

func (c *deployCmd) buildArgs() []string {
	args := []string{"build"}
	if c.Environment == "production" {
		args = append(args, "--prod")
	}
	return args
}

func (c *deployCmd) deployArgs() []string {
	// --yes, or Vercel rejects the first deployment of a project that has
	// never been deployed, asking for settings the CLI would otherwise prompt for.
	args := []string{"deploy", "--yes", "--no-wait"}
	if c.Prebuilt {
		args = append(args, "--prebuilt")
	}
	if c.Environment == "production" {
		args = append(args, "--prod")
	}
	if c.Staged {
		args = append(args, "--skip-domain")
	}
	if sha := os.Getenv("CIRCLE_SHA1"); sha != "" {
		// So promote can find this deployment from a later job.
		args = append(args, "--meta", metaCommitSHA+"="+sha)
	}
	return args
}
