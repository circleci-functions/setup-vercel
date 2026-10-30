package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/handoff"
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
	cli, err := c.install(ctx, client, s.creds.Token)
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
	url := lastLine(out)
	if url == "" {
		return errors.New("vercel deploy printed no deployment URL")
	}

	d, err := s.waiter(timeout).Ready(ctx, url)
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
	args := []string{"deploy", "--no-wait"}
	if c.Prebuilt {
		args = append(args, "--prebuilt")
	}
	if c.Environment == "production" {
		args = append(args, "--prod")
	}
	if c.Staged {
		args = append(args, "--skip-domain")
	}
	return args
}

// lastLine returns the last non-empty line of the CLI's stdout, which is the
// deployment URL.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
