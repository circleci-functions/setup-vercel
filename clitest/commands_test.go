package clitest_test

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-vercel/internal/testing/fakevercel"
)

func TestCredentials_MissingFailsBeforeAnyRequest(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "VERCEL_TOKEN")
	delete(e.vars, "VERCEL_PROJECT_ID")

	out, err := e.run(t, "wait-for-preview")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "VERCEL_PROJECT_ID and VERCEL_TOKEN are not set"))
	assert.Check(t, cmp.Len(e.vercel.Requests(), 0))
}

func TestCredentials_RejectedToken(t *testing.T) {
	e := newEnv(t)
	e.vars["VERCEL_TOKEN"] = "expired"

	out, err := e.run(t, "promote")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "vercel rejected VERCEL_TOKEN"))
}

func TestCredentials_UnknownProjectNamesTheLinkCommand(t *testing.T) {
	e := newEnv(t)
	e.vars["VERCEL_PROJECT_ID"] = "prj_other"

	out, err := e.run(t, "rollback")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "vercel has no project prj_other in team_1: run `vercel link`"))
}

func TestWaitForPreview_ExportsTheDeployment(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "my-app-git-main.vercel.app", SHA: "abc123", States: []string{"BUILDING", "READY"}})

	out, err := e.run(t, "wait-for-preview")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(e.exported(t), map[string]string{
		"VERCEL_DEPLOYMENT_URL": "https://my-app-git-main.vercel.app",
		"VERCEL_DEPLOYMENT_ID":  "dpl_1",
		"VERCEL_INSPECT_URL":    "https://vercel.com/acme/my-app/1",
	}))
	stored := e.sandbox.Cache.StoredArtifacts()
	assert.Assert(t, cmp.Len(stored, 1))
	assert.Check(t, cmp.Equal(stored[0].Destination, "setup-vercel/wait-for-preview.json"))
}

func TestWaitForPreview_TimesOutWhenNothingIsBuilt(t *testing.T) {
	e := newEnv(t)
	e.vercel.SetGitLinked(true)

	out, err := e.run(t, "wait-for-preview", "--timeout=50ms")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "no preview deployment of abc123 appeared"))
	assert.Check(t, cmp.Contains(out, "Vercel's Git integration is on"))
}

func TestPromote_FindsTheStagedDeploymentForTheCommit(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_live", URL: "live.vercel.app", Target: "production", SHA: "old"})
	e.vercel.SetProduction("dpl_live")
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_staged", URL: "staged.vercel.app", Target: "production", SHA: "abc123"})

	out, err := e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.vercel.Production(), "dpl_staged"))
	assert.Check(t, cmp.Equal(e.exported(t)["VERCEL_DEPLOYMENT_URL"], "https://staged.vercel.app"))
}

func TestPromote_PrefersTheDeploymentFromAnEarlierStep(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_a", URL: "a.vercel.app", Target: "production", SHA: "abc123"})
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_b", URL: "b.vercel.app", Target: "production", SHA: "abc123"})
	e.vars["VERCEL_DEPLOYMENT_ID"] = "dpl_a"

	out, err := e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.vercel.Production(), "dpl_a"))
}

func TestPromote_RefusesAPreview(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_p", URL: "p.vercel.app", SHA: "abc123"})

	out, err := e.run(t, "promote", "--deployment=dpl_p")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "is a preview. Vercel rebuilds a preview to promote it"))
	assert.Check(t, cmp.Equal(e.vercel.Production(), ""))
}

func TestPromote_NothingStaged(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "promote")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "no staged production deployment of abc123 to promote"))
}

func TestRollback_ToThePreviousProductionDeployment(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "one.vercel.app", Target: "production", IsRollbackCandidate: ptr(true)})
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_staged", URL: "staged.vercel.app", Target: "production", IsRollbackCandidate: ptr(false)})
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_2", URL: "two.vercel.app", Target: "production", IsRollbackCandidate: ptr(true)})
	e.vercel.SetProduction("dpl_2")

	out, err := e.run(t, "rollback")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.vercel.Production(), "dpl_1"))
	assert.Check(t, cmp.Equal(e.exported(t)["VERCEL_DEPLOYMENT_ID"], "dpl_1"))
}

func TestRollback_NothingToRollBackTo(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "one.vercel.app", Target: "production", IsRollbackCandidate: ptr(true)})
	e.vercel.SetProduction("dpl_1")

	out, err := e.run(t, "rollback")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "found no earlier production deployment to roll back to"))
}

func TestDurations_AreValidated(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "promote", "--timeout=soon")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, `--timeout="soon": want a positive duration`))
}
