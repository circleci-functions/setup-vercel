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

type rollbackCmd struct {
	apiFlags `embed:""`

	Deployment string `help:"ID or URL of the earlier production deployment to roll back to. Empty uses the one production served before the current one."`
	Timeout    string `default:"5m" help:"How long to wait for production to point at the deployment."`
}

func (c *rollbackCmd) Run(ctx context.Context, client *sdk.Client) error {
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
	if d.Target != "production" {
		return fmt.Errorf("deployment %s is a preview; roll back to an earlier production deployment", d.HTTPSURL())
	}

	fmt.Fprintf(os.Stderr, "setup-vercel: rolling production back to %s\n", d.HTTPSURL())
	if err := s.api.Rollback(ctx, s.project.ID, d.DeploymentID()); err != nil {
		return fmt.Errorf("roll back to %s: %w", d.HTTPSURL(), err)
	}
	if err := s.waiter(timeout).Alias(ctx, s.project.ID, d.DeploymentID()); err != nil {
		return err
	}
	return handoff.Publish(ctx, client, "rollback", d, os.Stderr)
}

// resolve finds the deployment to roll back to: the flag, else the newest
// earlier deployment that served production.
func (c *rollbackCmd) resolve(ctx context.Context, s session) (vercel.Deployment, error) {
	if c.Deployment != "" {
		d, err := s.api.Deployment(ctx, c.Deployment)
		if vercel.IsNotFound(err) {
			return d, fmt.Errorf("vercel has no deployment %s in project %s", c.Deployment, s.project.Name)
		}
		return d, err
	}
	current := s.project.ProductionID()
	if current == "" {
		return vercel.Deployment{}, errors.New("the project has no production deployment to roll back from")
	}
	ds, err := s.api.ListDeployments(ctx, vercel.ListOptions{ProjectID: s.project.ID, Target: "production", State: vercel.StateReady, Limit: 50})
	if err != nil {
		return vercel.Deployment{}, err
	}
	for _, d := range ds {
		if d.DeploymentID() != current && d.IsRollbackCandidate != nil && *d.IsRollbackCandidate {
			return s.api.Deployment(ctx, d.DeploymentID())
		}
	}
	return vercel.Deployment{}, errors.New("found no earlier production deployment to roll back to: pass --deployment")
}
