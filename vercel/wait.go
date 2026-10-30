package vercel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Waiter polls the API until a deployment or a promotion settles.
type Waiter struct {
	Client   *Client
	Timeout  time.Duration
	Interval time.Duration
	// Log receives progress lines and, when a build fails, its build log.
	Log io.Writer
}

// Ready waits for the deployment to be READY, and fails if it errors, is
// cancelled, or is still building at the timeout.
func (w Waiter) Ready(ctx context.Context, idOrURL string) (Deployment, error) {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()

	var last Deployment
	for {
		d, err := w.Client.Deployment(ctx, idOrURL)
		if err != nil {
			if ctx.Err() != nil && last.DeploymentID() != "" {
				return last, w.timedOut(last)
			}
			return d, err
		}
		if d.Status() != last.Status() {
			fmt.Fprintf(w.Log, "setup-vercel: %s is %s\n", d.HTTPSURL(), d.Status())
		}
		last = d

		switch d.Status() {
		case StateReady:
			return d, nil
		case StateError:
			w.printBuildLog(ctx, d)
			return d, failed(d, "failed to build")
		case StateCanceled:
			return d, failed(d, "was cancelled")
		case StateDeleted:
			return d, failed(d, "was deleted")
		}

		if err := sleep(ctx, w.Interval); err != nil {
			return d, w.timedOut(d)
		}
	}
}

// ForCommit waits for a deployment of sha to appear, then for it to be READY.
// target is "preview" or "production". project explains a miss: whether the
// project is connected to a Git repository at all.
func (w Waiter) ForCommit(ctx context.Context, project Project, sha, target string) (Deployment, error) {
	deadline := time.Now().Add(w.Timeout)
	findCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	fmt.Fprintf(w.Log, "setup-vercel: looking for the %s deployment of %s\n", target, sha)
	for {
		ds, err := w.Client.ListDeployments(findCtx, ListOptions{ProjectID: project.ID, SHA: sha, Target: target})
		if err != nil && findCtx.Err() == nil {
			return Deployment{}, err
		}
		if len(ds) > 0 {
			w.Timeout = time.Until(deadline)
			return w.Ready(ctx, ds[0].DeploymentID())
		}
		if err := sleep(findCtx, w.Interval); err != nil {
			return Deployment{}, noDeployment(project, sha, target, w.Timeout)
		}
	}
}

// Alias waits for production to point at deploymentID after a promote or
// rollback.
func (w Waiter) Alias(ctx context.Context, projectID, deploymentID string) error {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()

	for {
		p, err := w.Client.Project(ctx, projectID)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("production does not point at %s after %s; it may still be switching over", deploymentID, w.Timeout)
			}
			return err
		}
		if req := p.LastAliasRequest; req != nil && req.ToDeploymentID == deploymentID {
			switch req.JobStatus {
			case "succeeded":
				return nil
			case "failed":
				return fmt.Errorf("vercel could not point production at %s", deploymentID)
			}
		} else if p.ProductionID() == deploymentID {
			return nil
		}
		if err := sleep(ctx, w.Interval); err != nil {
			return fmt.Errorf("production does not point at %s after %s; it may still be switching over", deploymentID, w.Timeout)
		}
	}
}

func (w Waiter) printBuildLog(ctx context.Context, d Deployment) {
	// The job's context may be nearly spent; the log is worth a few more seconds.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	logText, err := w.Client.BuildLog(ctx, d.DeploymentID())
	if err != nil {
		fmt.Fprintf(w.Log, "setup-vercel: could not fetch the build log: %v\n", err)
		return
	}
	fmt.Fprintf(w.Log, "----- Vercel build log -----\n%s----- end of build log -----\n", logText)
}

func (w Waiter) timedOut(d Deployment) error {
	msg := fmt.Sprintf("%s is still %s after %s; the deployment keeps running on Vercel", d.HTTPSURL(), d.Status(), w.Timeout)
	if d.InspectorURL != "" {
		msg += ": " + d.InspectorURL
	}
	return errors.New(msg)
}

func failed(d Deployment, what string) error {
	msg := fmt.Sprintf("deployment %s %s", d.HTTPSURL(), what)
	if d.ErrorMessage != "" {
		msg += ": " + d.ErrorMessage
	}
	if d.InspectorURL != "" {
		msg += "\ninspect it at " + d.InspectorURL
	}
	return errors.New(msg)
}

func noDeployment(p Project, sha, target string, timeout time.Duration) error {
	msg := fmt.Sprintf("no %s deployment of %s appeared in project %s within %s", target, sha, p.Name, timeout)
	if p.Link == nil {
		return errors.New(msg + "; the project is not connected to a Git repository, so Vercel only deploys it when asked: use deploy instead")
	}
	return errors.New(msg + "; Vercel's Git integration is on for the project, so check whether its Ignored Build Step skipped this commit")
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
