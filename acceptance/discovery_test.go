//go:build acceptance

package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/runner"

	"github.com/circleci-functions/setup-vercel/vercelcli"
)

// TestAcceptance_LinksOntoTheRealPath is the one test that lets setup-vercel
// choose where vercel goes, rather than telling it with --bin-dir. That choice
// depends on the executor, and a sandboxed suite never reaches it.
//
// It changes the machine it runs on, so it runs only where that is disposable:
// a CircleCI job, or a container the caller vouches for with
// $SETUP_VERCEL_DISPOSABLE.
func TestAcceptance_LinksOntoTheRealPath(t *testing.T) {
	if os.Getenv("SETUP_VERCEL_DISPOSABLE") == "" {
		sdktest.SkipUnlessInJob(t)
	}
	e := newEnv(t)

	// The job's own environment, which is what a step gets, with $BASH_ENV
	// redirected because CI writes to the job's own.
	bashEnv := filepath.Join(t.TempDir(), "bash_env")
	extra := map[string]string{"BASH_ENV": bashEnv}
	for k, v := range e.vars {
		extra[k] = v
	}
	r := runner.New(t, binaryPath, runner.WithUnsafeOSEnvVars(), runner.WithUnsafeAdditionalEnvVars(extra))
	_, err := r.Run(append([]string{"setup", "--api-url=" + e.vercel.URL}, e.cliFlags()...)...)
	out := r.StdoutString() + r.StderrString()
	assert.NilError(t, err, out)

	dir := reportedDir(t, out)
	t.Logf("setup-vercel put vercel on PATH from %s", dir)
	// The install goes away with the sandbox, so its link must too, or a
	// dangling vercel follows this test into the rest of the job.
	t.Cleanup(func() { removeLink(t, dir) })

	// A later bash step sources $BASH_ENV, which carries the PATH change where
	// a link was not possible (Windows, or no writable directory on PATH).
	cmd := exec.CommandContext(t.Context(), "bash", "-c", `[ -f "$BASH_ENV" ] && . "$BASH_ENV"; vercel --version`)
	cmd.Env = append(os.Environ(), "BASH_ENV="+bashEnv)
	got, err := cmd.CombinedOutput()
	assert.NilError(t, err, "a later step could not resolve vercel: %s", got)
	assert.Check(t, cmp.Contains(string(got), vercelcli.DefaultVersion))
}

var resolvesFrom = regexp.MustCompile(`vercel \S+ resolves from (.+), linked to project`)

func reportedDir(t *testing.T, out string) string {
	t.Helper()
	m := resolvesFrom.FindStringSubmatch(out)
	assert.Assert(t, m != nil, "setup-vercel did not say where vercel resolves from: %s", out)
	return strings.TrimSpace(m[1])
}

// removeLink removes the vercel link setup-vercel made in dir, if it made one.
func removeLink(t *testing.T, dir string) {
	t.Helper()
	link := filepath.Join(dir, "vercel")
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(link); err != nil {
			t.Logf("remove %s: %v", link, err)
		}
	}
}
