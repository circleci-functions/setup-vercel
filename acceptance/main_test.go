//go:build acceptance

// Package acceptance_test runs setup-vercel with the real Vercel CLI it pins,
// installed from npm, against a fake Vercel API: the CLI's global config points
// it at the fake, so no Vercel account is involved. Tagged out of `task test`
// because it needs Node.js and the npm registry. The CLI contract against a
// stand-in CLI is covered hermetically by clitest/.
package acceptance_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"

	"github.com/circleci-functions/setup-vercel/internal/testing/fakevercel"
)

// binaryPathEnvVar names a prebuilt setup-vercel to test, for an executor with
// no Go toolchain to compile one with. The smoke jobs set it, and pair it with a
// cross-compiled copy of this suite. A relative path is resolved here, since
// the suite runs the binary from a working directory of its own.
const binaryPathEnvVar = "SETUP_VERCEL_BINARY"

var binaryPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func runTests(m *testing.M) (int, error) {
	if bin := os.Getenv(binaryPathEnvVar); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		binaryPath = abs
		return m.Run(), nil
	}

	p := compiler.NewParallel(1)
	defer p.Cleanup()

	p.Add(compiler.Work{
		Result: &binaryPath,
		Name:   "setup-vercel",
		Target: "..",
		Source: ".",
	})

	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// env is a test's sandbox, fake Vercel API, project directory and the
// variables a job would set.
type env struct {
	sandbox    *sdktest.Sandbox
	vercel     *fakevercel.Server
	projectDir string
	vars       map[string]string
}

// newEnv returns an env with a one-page static site to deploy. The CLI cache is
// CircleCI's own when TOOLKIT_CACHE_MODE=live, as in CI, and in-memory otherwise.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hello</h1>\n"), 0o600))
	e := &env{
		sandbox:    sdktest.NewSandbox(t, sdktest.WithLiveCache()),
		vercel:     fakevercel.New(t, "prj_1"),
		projectDir: dir,
		vars: map[string]string{
			"VERCEL_TOKEN":      fakevercel.Token,
			"VERCEL_ORG_ID":     "team_1",
			"VERCEL_PROJECT_ID": "prj_1",
			"CIRCLE_SHA1":       "abc123",
			// Keep the CLI's own calls home off the network.
			"VERCEL_TELEMETRY_DISABLED": "1",
			"NO_UPDATE_NOTIFIER":        "1",
		},
	}
	t.Cleanup(func() {
		assert.Check(t, len(e.vercel.Unhandled()) == 0, "the CLI called routes the fake does not have: %v", e.vercel.Unhandled())
	})
	return e
}

// run runs the binary with the fake API and the sandbox's install directory,
// returning stdout and stderr together.
func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append(args,
		"--api-url="+e.vercel.URL,
		"--poll-interval=10ms",
	)
	vars := e.sandbox.Env()
	maps.Copy(vars, e.vars)
	r := runner.New(t, binaryPath, runner.WithSandboxEnv(vars))
	_, err := r.Run(full...)
	return r.StdoutString() + r.StderrString(), err
}

// cliFlags are the flags the commands that run the Vercel CLI take, keeping its
// install inside the sandbox.
func (e *env) cliFlags() []string {
	return []string{
		"--project-dir=" + e.projectDir,
		"--install-dir=" + e.sandbox.InstallDir,
		"--cache-nonce=" + e.sandbox.CacheKeyNonce,
	}
}

func (e *env) exported(t *testing.T) map[string]string {
	t.Helper()
	vars, err := bashenv.Vars(e.sandbox.BashEnv, map[string]string{})
	assert.NilError(t, err)
	return vars
}
