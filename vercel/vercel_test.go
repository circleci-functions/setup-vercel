package vercel_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-vercel/internal/testing/fakevercel"
	"github.com/circleci-functions/setup-vercel/vercel"
)

func newWaiter(t *testing.T, timeout time.Duration) (*fakevercel.Server, vercel.Waiter, *bytes.Buffer) {
	t.Helper()
	srv := fakevercel.New(t, "prj_1")
	var log bytes.Buffer
	return srv, vercel.Waiter{
		Client:   vercel.New(srv.URL, fakevercel.Token, "team_1"),
		Timeout:  timeout,
		Interval: time.Millisecond,
		Log:      &log,
	}, &log
}

func TestReady_WaitsThroughTheBuild(t *testing.T) {
	srv, w, _ := newWaiter(t, time.Second)
	srv.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "my-app-1.vercel.app", States: []string{"QUEUED", "BUILDING", "READY"}})

	d, err := w.Ready(context.Background(), "https://my-app-1.vercel.app")
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d.DeploymentID(), "dpl_1"))
	assert.Check(t, cmp.Equal(d.HTTPSURL(), "https://my-app-1.vercel.app"))
}

func TestReady_PrintsTheBuildLogWhenTheBuildFails(t *testing.T) {
	srv, w, log := newWaiter(t, time.Second)
	srv.Add(&fakevercel.Deployment{
		ID: "dpl_1", URL: "my-app-1.vercel.app",
		States:       []string{"BUILDING", "ERROR"},
		ErrorMessage: "Command \"npm run build\" exited with 1",
		BuildLog:     []string{"> next build", "Type error: x is not a number"},
	})

	_, err := w.Ready(context.Background(), "dpl_1")
	assert.ErrorContains(t, err, "failed to build: Command \"npm run build\" exited with 1")
	assert.ErrorContains(t, err, "https://vercel.com/acme/my-app/1")
	assert.Check(t, cmp.Contains(log.String(), "Type error: x is not a number"))
}

func TestReady_TimesOutWithTheInspectURL(t *testing.T) {
	srv, w, _ := newWaiter(t, 50*time.Millisecond)
	srv.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "my-app-1.vercel.app", States: []string{"BUILDING"}})

	_, err := w.Ready(context.Background(), "dpl_1")
	assert.ErrorContains(t, err, "is still BUILDING after 50ms; the deployment keeps running on Vercel")
	assert.ErrorContains(t, err, "https://vercel.com/acme/my-app/1")
}

func TestForCommit_FindsThePreviewForTheCommit(t *testing.T) {
	srv, w, _ := newWaiter(t, time.Second)
	srv.Add(&fakevercel.Deployment{ID: "dpl_old", URL: "old.vercel.app", SHA: "aaa"})
	srv.Add(&fakevercel.Deployment{ID: "dpl_prod", URL: "prod.vercel.app", SHA: "bbb", Target: "production"})
	srv.Add(&fakevercel.Deployment{ID: "dpl_new", URL: "new.vercel.app", SHA: "bbb", States: []string{"BUILDING", "READY"}})

	d, err := w.ForCommit(context.Background(), vercel.Project{ID: "prj_1"}, "bbb", "preview")
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d.DeploymentID(), "dpl_new"))
}

func TestForCommit_SaysWhetherGitIsConnected(t *testing.T) {
	tests := []struct {
		name    string
		linked  bool
		message string
	}{
		{"connected", true, "Vercel's Git integration is on for the project"},
		{"not connected", false, "the project is not connected to a Git repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, w, _ := newWaiter(t, 30*time.Millisecond)
			srv.SetGitLinked(tt.linked)
			p, err := w.Client.Project(context.Background(), "prj_1")
			assert.NilError(t, err)

			_, err = w.ForCommit(context.Background(), p, "ccc", "preview")
			assert.ErrorContains(t, err, "no preview deployment of ccc appeared")
			assert.ErrorContains(t, err, tt.message)
		})
	}
}

func TestAlias_WaitsForProduction(t *testing.T) {
	srv, w, _ := newWaiter(t, time.Second)
	srv.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "a.vercel.app", Target: "production"})

	assert.NilError(t, w.Client.Promote(context.Background(), "prj_1", "dpl_1"))
	assert.NilError(t, w.Alias(context.Background(), "prj_1", "dpl_1"))
	assert.Check(t, cmp.Equal(srv.Production(), "dpl_1"))
}

func TestAlias_FailsWhenVercelDoes(t *testing.T) {
	srv, w, _ := newWaiter(t, time.Second)
	srv.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "a.vercel.app", Target: "production"})
	srv.FailAliasJobs()

	assert.NilError(t, w.Client.Promote(context.Background(), "prj_1", "dpl_1"))
	assert.ErrorContains(t, w.Alias(context.Background(), "prj_1", "dpl_1"), "vercel could not point production at dpl_1")
}

func TestClient_ReportsAPIErrors(t *testing.T) {
	srv := fakevercel.New(t, "prj_1")
	_, err := vercel.New(srv.URL, "wrong", "team_1").Project(context.Background(), "prj_1")
	assert.ErrorContains(t, err, "missing an authentication token (401 forbidden)")

	_, err = vercel.New(srv.URL, fakevercel.Token, "team_1").Deployment(context.Background(), "dpl_missing")
	assert.Check(t, vercel.IsNotFound(err))
}

func TestClient_SendsTheTeamOnlyForTeams(t *testing.T) {
	tests := map[string]string{
		"team_abc": "GET /v9/projects/prj_1?teamId=team_abc",
		"user_abc": "GET /v9/projects/prj_1",
	}
	for org, want := range tests {
		t.Run(org, func(t *testing.T) {
			srv := fakevercel.New(t, "prj_1")
			_, err := vercel.New(srv.URL, fakevercel.Token, org).Project(context.Background(), "prj_1")
			assert.NilError(t, err)
			assert.Check(t, cmp.DeepEqual(srv.Requests(), []string{want}))
		})
	}
}

func TestDeployment_BuildDuration(t *testing.T) {
	d := vercel.Deployment{BuildingAt: 1_000, Ready: 61_000}
	assert.Check(t, cmp.Equal(d.BuildDuration(), time.Minute))
	assert.Check(t, cmp.Equal(vercel.Deployment{}.BuildDuration(), time.Duration(0)))
}

// Vercel moves the production target before it registers the switch; returning
// then let a rollback straight after a promote hit "a promote is already
// in progress".
func TestAlias_WaitsForTheSwitchNotJustTheTarget(t *testing.T) {
	srv, w, _ := newWaiter(t, 50*time.Millisecond)
	srv.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "a.vercel.app", Target: "production"})
	srv.Add(&fakevercel.Deployment{ID: "dpl_2", URL: "b.vercel.app", Target: "production"})
	assert.NilError(t, w.Client.Promote(context.Background(), "prj_1", "dpl_1"))
	assert.NilError(t, w.Alias(context.Background(), "prj_1", "dpl_1"))

	// The target moves to dpl_2, but the latest switch is still the one to dpl_1.
	srv.SetProduction("dpl_2")
	assert.ErrorContains(t, w.Alias(context.Background(), "prj_1", "dpl_2"), "may still be switching over")
}

func TestIdle_WaitsForASwitchInProgress(t *testing.T) {
	srv, w, log := newWaiter(t, time.Second)
	srv.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "a.vercel.app", Target: "production"})
	srv.Add(&fakevercel.Deployment{ID: "dpl_2", URL: "b.vercel.app", Target: "production"})
	assert.NilError(t, w.Client.Promote(context.Background(), "prj_1", "dpl_1"))

	assert.NilError(t, w.Idle(context.Background(), "prj_1"))
	assert.Check(t, cmp.Contains(log.String(), "waiting for the promote to dpl_1 to finish"))
	assert.NilError(t, w.Client.Rollback(context.Background(), "prj_1", "dpl_2"))
}
