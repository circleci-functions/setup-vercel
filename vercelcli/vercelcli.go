// Package vercelcli installs a pinned Vercel CLI with npm and runs it.
package vercelcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/pathlink"
	"github.com/circleci-functions/toolkit/sdk"
)

// DefaultVersion is the Vercel CLI installed when no version is given.
const DefaultVersion = "65.8.0"

// minNodeMajor is the oldest Node.js the Vercel CLI supports.
const minNodeMajor = 20

// Installer installs one version of the CLI under Dir, through the job cache.
type Installer struct {
	// Dir holds one directory per installed version.
	Dir     string
	Version string
	Client  *sdk.Client
	// CacheNonce partitions the cache, so a test cannot restore an earlier
	// run's install.
	CacheNonce string
	Log        io.Writer
}

// CLI is an installed Vercel CLI.
type CLI struct {
	// Prefix is the npm prefix it is installed under.
	Prefix  string
	Version string
	Token   string
	// GlobalConfig, when set, is a directory holding the CLI's global
	// config.json, passed with --global-config.
	GlobalConfig string
	Log          io.Writer
}

// WriteGlobalConfig writes a global config to dir that sends the CLI's API
// calls to apiURL instead of Vercel's, and returns dir.
func WriteGlobalConfig(dir, apiURL string) (string, error) {
	b, err := json.Marshal(map[string]string{"api": apiURL})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600)
}

// Ensure returns the CLI, installing it if neither an earlier run nor the job
// cache has it.
func (i Installer) Ensure(ctx context.Context) (CLI, error) {
	if err := checkNode(ctx); err != nil {
		return CLI{}, err
	}
	prefix := filepath.Join(i.Dir, i.Version)
	cli := CLI{Prefix: prefix, Version: i.Version, Log: i.Log}
	if installed(prefix) {
		return cli, nil
	}

	key := i.cacheKey()
	_, err := i.Client.RestoreCache(ctx, []string{key}, sdk.WithStepName("Restoring Vercel CLI "+i.Version))
	sdk.LogCacheErr("restore", err)
	if installed(prefix) {
		fmt.Fprintf(i.Log, "setup-vercel: restored Vercel CLI %s from cache\n", i.Version)
		return cli, nil
	}

	fmt.Fprintf(i.Log, "setup-vercel: installing Vercel CLI %s\n", i.Version)
	cmd := exec.CommandContext(ctx, "npm", "install", "--global", "--prefix", prefix,
		"--no-audit", "--no-fund", "--loglevel=error", "vercel@"+i.Version)
	cmd.Stdout = i.Log
	cmd.Stderr = i.Log
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(prefix)
		return CLI{}, fmt.Errorf("install Vercel CLI %s with npm: %w", i.Version, err)
	}
	if !installed(prefix) {
		return CLI{}, fmt.Errorf("npm installed vercel@%s, but %s is missing", i.Version, Bin(prefix))
	}
	sdk.LogCacheErr("save", i.Client.SaveCache(ctx, key, []string{prefix}, sdk.WithStepName("Saving Vercel CLI "+i.Version)))
	return cli, nil
}

func (i Installer) cacheKey() string {
	key := fmt.Sprintf("setup-vercel-cli-v1-%s-%s", sdk.OSArch(), i.Version)
	if i.CacheNonce != "" {
		key += "-" + i.CacheNonce
	}
	return key
}

// Bin returns the path of the vercel executable under an npm prefix.
func Bin(prefix string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(prefix, "vercel.cmd")
	}
	return filepath.Join(prefix, "bin", "vercel")
}

func installed(prefix string) bool {
	_, err := os.Stat(Bin(prefix))
	return err == nil
}

// Link makes vercel resolvable by name in later steps, and returns where it
// resolves from. binDir, when set, links into exactly that directory.
func (c CLI) Link(binDir string) (string, error) {
	if runtime.GOOS == "windows" {
		// npm's Windows shims find node_modules relative to themselves, so
		// they cannot be linked elsewhere; put the prefix on PATH instead.
		if err := bashenv.AddToPath(c.Prefix); err != nil {
			return "", err
		}
		return c.Prefix, nil
	}
	res, err := pathlink.Ensure(pathlink.Options{
		Names:     []string{"vercel"},
		FromDir:   filepath.Dir(Bin(c.Prefix)),
		Preferred: pathlink.UserBinDirs(),
		Dir:       binDir,
		AddToPath: bashenv.AddToPath,
		Verify: func(bin string) error {
			out, err := pathlink.Command(bin, "--version").CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed to run: %w", err)
			}
			if !strings.Contains(string(out), c.Version) {
				return fmt.Errorf("reports %q rather than %s", strings.TrimSpace(string(out)), c.Version)
			}
			return nil
		},
	})
	return res.Dir, err
}

// Run runs vercel in dir with args and the token, streaming its stderr to the
// log, and returns its stdout.
func (c CLI) Run(ctx context.Context, dir string, args ...string) (string, error) {
	args = append(args, "--token", c.Token)
	if c.GlobalConfig != "" {
		args = append(args, "--global-config", c.GlobalConfig)
	}
	cmd := exec.CommandContext(ctx, Bin(c.Prefix), args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, c.Log)
	cmd.Stderr = c.Log
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("vercel %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

// DeploymentRef reads the deployment vercel deploy made from its stdout: the
// URL on the last line, or, when the CLI detects it is run by an AI agent, the
// deployment's ID in the JSON it prints instead.
func DeploymentRef(stdout string) (string, error) {
	trimmed := strings.TrimSpace(stdout)
	if strings.HasPrefix(trimmed, "{") {
		var out struct {
			Deployment struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"deployment"`
		}
		if err := json.Unmarshal([]byte(trimmed), &out); err == nil {
			if out.Deployment.ID != "" {
				return out.Deployment.ID, nil
			}
			if out.Deployment.URL != "" {
				return out.Deployment.URL, nil
			}
		}
	}
	lines := strings.Split(trimmed, "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" && !strings.ContainsAny(last, "{} ") {
		return last, nil
	}
	return "", errors.New("vercel deploy printed no deployment URL")
}

// Pull links dir to the project and fetches its settings and environment
// variables for environment. The CLI reads the project from VERCEL_ORG_ID and
// VERCEL_PROJECT_ID.
func (c CLI) Pull(ctx context.Context, dir, environment string) error {
	_, err := c.Run(ctx, dir, "pull", "--yes", "--environment="+environment)
	return err
}

// checkNode fails unless a Node.js the CLI supports is on PATH.
func checkNode(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "node", "--version").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("the Vercel CLI needs Node.js %d or later, and node is not on PATH: run setup-node, or use a cimg/node image, before this step", minNodeMajor)
		}
		return fmt.Errorf("run node --version: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	major, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil {
		return fmt.Errorf("could not read the Node.js version from %q", strings.TrimSpace(string(out)))
	}
	if major < minNodeMajor {
		return fmt.Errorf("the Vercel CLI needs Node.js %d or later, and node on PATH is %s: run setup-node before this step", minNodeMajor, strings.TrimSpace(string(out)))
	}
	return nil
}
