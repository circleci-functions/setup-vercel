package main

import (
	"context"
	"fmt"
	"os"

	"github.com/circleci-functions/toolkit/sdk"
)

type setupCmd struct {
	installFlags `embed:""`
	apiFlags     `embed:""`

	Environment string `enum:"preview,production" default:"preview" help:"Environment whose project settings and environment variables to pull: preview or production."`
	ProjectDir  string `name:"project-dir" default:"." help:"Directory to link to the Vercel project. In a monorepo, leave it at the repository root: the Vercel CLI applies the project's Root Directory setting." example:"apps/web"`
}

func (c *setupCmd) Run(ctx context.Context, client *sdk.Client) error {
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
	dir, err := cli.Link(c.BinDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "setup-vercel: vercel %s resolves from %s, linked to project %s\n", cli.Version, dir, s.project.Name)
	return nil
}
