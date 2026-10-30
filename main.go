// Command setup-vercel deploys to Vercel from a CircleCI job.
//
// Supported platforms: linux, darwin, windows.
package main

import (
	"context"

	"github.com/alecthomas/kong"

	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/vercelcli"
)

type cli struct {
	Setup          setupCmd          `cmd:"" default:"withargs" help:"Install the Vercel CLI and link the project. Runs when no command is given."`
	Deploy         deployCmd         `cmd:"" help:"Build and deploy, then wait until the deployment is ready."`
	WaitForPreview waitForPreviewCmd `cmd:"" name:"wait-for-preview" help:"Wait for the preview Vercel's Git integration builds for this commit."`
	Promote        promoteCmd        `cmd:"" help:"Point production at a staged production deployment."`
	Rollback       rollbackCmd       `cmd:"" help:"Point production back at an earlier production deployment."`
}

func main() {
	sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
	var flags cli
	kCtx := kong.Parse(&flags,
		kong.Name("setup-vercel"),
		kong.Description("Deploy to Vercel, and promote or roll back production."),
		kong.UsageOnError(),
		kong.Vars{"vercel_version": vercelcli.DefaultVersion},
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}
