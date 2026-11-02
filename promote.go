package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-vercel/handoff"
	"github.com/circleci-functions/setup-vercel/vercel"
)

type promoteCmd struct {
	apiFlags `embed:""`

	Deployment string `help:"ID or URL of the staged production deployment to promote. Empty uses VERCEL_DEPLOYMENT_ID from an earlier step, else this commit's staged production deployment."`
	Timeout    string `default:"5m" help:"How long to wait for production to point at the deployment."`
}

func (c *promoteCmd) Run(ctx context.Context, client *sdk.Client) error {
	timeout, err := parseDuration("timeout", c.Timeout)
	if err != nil {
		return err
	}
	s, err := c.start(ctx)
	if err != nil {
		return err
	}
	d, err := c.resolve(ctx, s)
	if err != nil {
		return err
	}
	if d.Status() != vercel.StateReady {
		return fmt.Errorf("deployment %s is %s, not READY, so it cannot be promoted", d.HTTPSURL(), d.Status())
	}
	if d.Target != "production" {
		return fmt.Errorf("deployment %s is a preview. Vercel rebuilds a preview to promote it, so production would not run what was tested: "+
			"deploy with environment: production and staged: true, test that deployment, then promote it", d.HTTPSURL())
	}

	if s.project.ProductionID() == d.DeploymentID() {
		fmt.Fprintf(os.Stderr, "setup-vercel: production already points at %s\n", d.HTTPSURL())
		return handoff.Publish(ctx, client, "promote", d, os.Stderr)
	}
	fmt.Fprintf(os.Stderr, "setup-vercel: promoting %s to production\n", d.HTTPSURL())
	if err := s.waiter(timeout).Idle(ctx, s.project.ID); err != nil {
		return err
	}
	if err := s.waiter(timeout).Switch(ctx, func(ctx context.Context) error {
		return s.api.Promote(ctx, s.project.ID, d.DeploymentID())
	}); err != nil {
		return fmt.Errorf("promote %s: %w", d.HTTPSURL(), err)
	}
	if err := s.waiter(timeout).Alias(ctx, s.project.ID, d.DeploymentID()); err != nil {
		return err
	}
	return handoff.Publish(ctx, client, "promote", d, os.Stderr)
}

// resolve finds the deployment to promote: the flag, then the deployment an
// earlier step in this job exported, then this commit's staged production
// deployment, which works across jobs.
func (c *promoteCmd) resolve(ctx context.Context, s session) (vercel.Deployment, error) {
	ref := c.Deployment
	if ref == "" {
		ref = os.Getenv(handoff.EnvID)
	}
	if ref != "" {
		d, err := s.api.Deployment(ctx, ref)
		if vercel.IsNotFound(err) {
			return d, fmt.Errorf("vercel has no deployment %s in project %s", ref, s.project.Name)
		}
		return d, err
	}

	sha := os.Getenv("CIRCLE_SHA1")
	if sha == "" {
		return vercel.Deployment{}, errors.New("no deployment to promote: set --deployment, or run deploy earlier in the job")
	}
	// Not filtered on the commit by the API: that only matches the commit Vercel's
	// Git integration records, and deploy records its own.
	ds, err := s.api.ListDeployments(ctx, vercel.ListOptions{ProjectID: s.project.ID, Target: "production", State: vercel.StateReady, Limit: 100})
	if err != nil {
		return vercel.Deployment{}, err
	}
	for _, d := range ds {
		if d.DeploymentID() != s.project.ProductionID() && (d.Meta[metaCommitSHA] == sha || d.Meta["githubCommitSha"] == sha) {
			return s.api.Deployment(ctx, d.DeploymentID())
		}
	}
	return vercel.Deployment{}, fmt.Errorf("no staged production deployment of %s to promote: deploy with environment: production and staged: true first", sha)
}
