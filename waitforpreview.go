package main

import (
	"context"
	"errors"
	"os"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/handoff"
)

type waitForPreviewCmd struct {
	apiFlags `embed:""`

	Commit  string `help:"Commit whose preview to wait for. Empty uses the job's commit, CIRCLE_SHA1."`
	Timeout string `default:"10m" help:"How long to wait for the preview to appear and be ready."`
}

func (c *waitForPreviewCmd) Run(ctx context.Context, client *sdk.Client) error {
	timeout, err := parseDuration("timeout", c.Timeout)
	if err != nil {
		return err
	}
	sha := c.Commit
	if sha == "" {
		sha = os.Getenv("CIRCLE_SHA1")
	}
	if sha == "" {
		return errors.New("no commit to look for: set --commit, or run in a CircleCI job, which sets CIRCLE_SHA1")
	}
	s, err := c.start(ctx)
	if err != nil {
		return err
	}
	d, err := s.waiter(timeout).ForCommit(ctx, s.project, sha, "preview")
	if err != nil {
		return err
	}
	return handoff.Publish(ctx, client, "wait-for-preview", d, os.Stderr)
}
