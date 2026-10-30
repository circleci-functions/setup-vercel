//go:build acceptance

package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-vercel/vercelcli"
)

func TestAcceptance_SetupInstallsLinksAndPulls(t *testing.T) {
	e := newEnv(t)

	args := append([]string{"setup", "--bin-dir=" + e.sandbox.BinDir}, e.cliFlags()...)
	out, err := e.run(t, args...)
	assert.NilError(t, err, out)

	if runtime.GOOS != "windows" {
		version, err := exec.Command(filepath.Join(e.sandbox.BinDir, "vercel"), "--version").CombinedOutput()
		assert.NilError(t, err, string(version))
		assert.Check(t, cmp.Contains(string(version), vercelcli.DefaultVersion))
	}

	project, err := os.ReadFile(filepath.Join(e.projectDir, ".vercel", "project.json"))
	assert.NilError(t, err)
	assert.Check(t, cmp.Contains(string(project), `"projectId": "prj_1"`))
	env, err := os.ReadFile(filepath.Join(e.projectDir, ".vercel", ".env.preview.local"))
	assert.NilError(t, err)
	assert.Check(t, cmp.Contains(string(env), "hello from preview"))
}

func TestAcceptance_DeployPreviewPrebuilt(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, append([]string{"deploy"}, e.cliFlags()...)...)
	assert.NilError(t, err, out)

	created := e.vercel.Created()
	assert.Assert(t, cmp.Len(created, 1))
	assert.Check(t, created[0]["target"] == nil, "a preview has no target: %v", created[0]["target"])
	assert.Check(t, cmp.DeepEqual(created[0]["meta"], map[string]any{"circleciCommitSha": "abc123"}))
	assert.Check(t, requested(e, "POST /v13/deployments", "prebuilt=1"), "not deployed prebuilt: %v", e.vercel.Requests())
	assert.Check(t, cmp.Equal(e.exported(t)["VERCEL_DEPLOYMENT_URL"], "https://my-app-cli1.vercel.app"))
}

func TestAcceptance_DeployBuildingOnVercel(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, append([]string{"deploy", "--no-prebuilt"}, e.cliFlags()...)...)
	assert.NilError(t, err, out)
	assert.Check(t, !requested(e, "POST /v13/deployments", "prebuilt=1"), "deployed prebuilt: %v", e.vercel.Requests())
	assert.Check(t, cmp.Equal(e.exported(t)["VERCEL_DEPLOYMENT_ID"], "dpl_cli1"))
}

// An AI agent running the job makes the CLI print JSON in place of the URL.
func TestAcceptance_DeployUnderAnAgent(t *testing.T) {
	e := newEnv(t)
	e.vars["AI_AGENT"] = "acceptance-test"

	out, err := e.run(t, append([]string{"deploy"}, e.cliFlags()...)...)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.exported(t)["VERCEL_DEPLOYMENT_ID"], "dpl_cli1"))
}

// The flow the README leads with, with promote in a later job: nothing passes
// the deployment between them but the commit.
func TestAcceptance_StagedProductionThenPromote(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, append([]string{"deploy", "--environment=production", "--staged"}, e.cliFlags()...)...)
	assert.NilError(t, err, out)
	created := e.vercel.Created()
	assert.Assert(t, cmp.Len(created, 1))
	assert.Check(t, cmp.Equal(created[0]["target"], "production"))
	assert.Check(t, cmp.Equal(created[0]["autoAssignCustomDomains"], false))
	assert.Check(t, cmp.Equal(e.vercel.Production(), ""), "a staged deployment took production")

	assert.NilError(t, os.Remove(e.sandbox.BashEnv))
	out, err = e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.vercel.Production(), "dpl_cli1"))
}

func requested(e *env, prefix, query string) bool {
	for _, r := range e.vercel.Requests() {
		if strings.HasPrefix(r, prefix) && strings.Contains(r, query) {
			return true
		}
	}
	return false
}
