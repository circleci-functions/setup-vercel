package clitest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-vercel/internal/testing/fakevercel"
	"github.com/circleci-functions/setup-vercel/vercelcli"
)

// fakeCLI puts a stand-in vercel where setup-vercel installs it, and a node on
// PATH, so deploy and setup run without npm or the network. The stand-in logs
// each invocation's arguments and prints url for deploy.
func fakeCLI(t *testing.T, e *env, url string) (installDir, argLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in CLI is a shell script")
	}
	installDir = t.TempDir()
	argLog = filepath.Join(t.TempDir(), "args")
	bin := vercelcli.Bin(filepath.Join(installDir, vercelcli.DefaultVersion))
	writeScript(t, bin, `echo "$*" >> `+argLog+`
case "$1" in
  --version) echo "Vercel CLI `+vercelcli.DefaultVersion+`" ;;
  deploy) echo "Uploading..." >&2; echo "`+url+`" ;;
esac
`)

	nodeDir := t.TempDir()
	writeScript(t, filepath.Join(nodeDir, "node"), `echo v22.20.0`)
	e.vars["PATH"] = nodeDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return installDir, argLog
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	assert.NilError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755)) //nolint:gosec // an executable
}

func invocations(t *testing.T, argLog string) []string {
	t.Helper()
	b, err := os.ReadFile(argLog)
	assert.NilError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, l := range lines {
		// Every call points the CLI at the fake API through a temporary
		// global config; which directory is not worth asserting.
		if j := strings.Index(l, " --global-config "); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return lines
}

func TestDeploy_PreviewBuildsInTheJob(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "my-app-abc.vercel.app", States: []string{"QUEUED", "READY"}})
	installDir, argLog := fakeCLI(t, e, "https://my-app-abc.vercel.app")

	out, err := e.run(t, "deploy", "--install-dir="+installDir)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(invocations(t, argLog), []string{
		"pull --yes --environment=preview --token testtoken",
		"build --token testtoken",
		"deploy --no-wait --prebuilt --meta circleciCommitSha=abc123 --token testtoken",
	}))
	assert.Check(t, cmp.Equal(e.exported(t)["VERCEL_DEPLOYMENT_URL"], "https://my-app-abc.vercel.app"))
	assert.Check(t, !strings.Contains(out, fakevercel.Token), "the token was printed:\n%s", out)
}

func TestDeploy_StagedProduction(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{ID: "dpl_1", URL: "my-app-prod.vercel.app", Target: "production"})
	installDir, argLog := fakeCLI(t, e, "https://my-app-prod.vercel.app")

	out, err := e.run(t, "deploy", "--install-dir="+installDir, "--environment=production", "--staged", "--no-prebuilt")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(invocations(t, argLog), []string{
		"pull --yes --environment=production --token testtoken",
		"deploy --no-wait --prod --skip-domain --meta circleciCommitSha=abc123 --token testtoken",
	}))
}

func TestDeploy_StagedNeedsProduction(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "deploy", "--staged")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "--staged needs --environment=production"))
	assert.Check(t, cmp.Len(e.vercel.Requests(), 0))
}

func TestDeploy_FailedBuildPrintsTheLog(t *testing.T) {
	e := newEnv(t)
	e.vercel.Add(&fakevercel.Deployment{
		ID: "dpl_1", URL: "my-app-abc.vercel.app",
		States:   []string{"BUILDING", "ERROR"},
		BuildLog: []string{"Error: Cannot find module 'next'"},
	})
	installDir, _ := fakeCLI(t, e, "https://my-app-abc.vercel.app")

	out, err := e.run(t, "deploy", "--install-dir="+installDir, "--no-prebuilt")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "Error: Cannot find module 'next'"))
	assert.Check(t, cmp.Contains(out, "https://vercel.com/acme/my-app/1"))
}

func TestSetup_LinksTheCLIOntoPath(t *testing.T) {
	e := newEnv(t)
	installDir, argLog := fakeCLI(t, e, "")

	out, err := e.run(t, "--install-dir="+installDir, "--bin-dir="+e.sandbox.BinDir)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(invocations(t, argLog), []string{"pull --yes --environment=preview --token testtoken"}))
	_, err = os.Lstat(filepath.Join(e.sandbox.BinDir, "vercel"))
	assert.Check(t, err)
}

func TestSetup_NeedsNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH handling differs on Windows")
	}
	e := newEnv(t)
	e.vars["PATH"] = t.TempDir()

	out, err := e.run(t)
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "the Vercel CLI needs Node.js 20 or later, and node is not on PATH"))
}
