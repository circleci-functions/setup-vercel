# setup-vercel

A CircleCI function that deploys to [Vercel](https://vercel.com), waits until the deployment is
ready, and promotes or rolls back production. It works for projects that deploy from CircleCI and
for projects that keep Vercel's Git integration.

```yaml
functions:
  vercel: github.com/circleci-functions/setup-vercel@v0.1.0

jobs:
  preview:
    docker:
      - image: cimg/node:lts
    steps:
      - checkout
      - vercel/deploy
      - run: npx playwright test --base-url "$VERCEL_DEPLOYMENT_URL"

  ship:
    docker:
      - image: cimg/node:lts
    steps:
      - checkout
      # Build production without moving the production domains, test that exact build,
      # then point production at it.
      - vercel/deploy:
          with:
            environment: production
            staged: true
      - run: ./smoke.sh "$VERCEL_DEPLOYMENT_URL"
      - vercel/promote
```

The key under `functions:` names the steps, so `vercel:` gives `vercel/deploy`. Run
`circleci function add github.com/circleci-functions/setup-vercel` to write the entry.

## Credentials

Every command reads three environment variables, the same ones the Vercel CLI reads. Put them in
a context:

| Variable | Value |
|---|---|
| `VERCEL_TOKEN` | A token from https://vercel.com/account/tokens |
| `VERCEL_ORG_ID` | `orgId` from `.vercel/project.json` after `vercel link` |
| `VERCEL_PROJECT_ID` | `projectId` from the same file |

A missing variable fails the step before any work, naming it. The token is never printed.

## Commands

| Command | What it does |
|---|---|
| `setup` (a bare `- vercel` step) | Installs the Vercel CLI, puts `vercel` on PATH and links the project |
| `deploy` | Builds, deploys, and waits until the deployment is ready |
| `wait-for-preview` | Waits for the preview Vercel's Git integration builds for this commit. Deploys nothing |
| `promote` | Points production at a staged production deployment |
| `rollback` | Points production back at an earlier production deployment |

`deploy`, `wait-for-preview`, `promote` and `rollback` export the deployment to the steps after
them in the same job:

| Variable | Value |
|---|---|
| `VERCEL_DEPLOYMENT_URL` | `https://…` URL of the deployment |
| `VERCEL_DEPLOYMENT_ID` | Its ID, `dpl_…` |
| `VERCEL_INSPECT_URL` | Its page in the Vercel dashboard |

They also store a summary of the deployment (URL, ID, build duration) as the artifact
`setup-vercel/<command>.json`.

`setup` and `deploy` run the Vercel CLI, which needs Node.js 20 or later on PATH: use a
`cimg/node` image, or run `setup-node` first. The other commands call the Vercel API and need
no Node.js.

### setup

| Flag | Default | Description |
|---|---|---|
| `cli-version` | `65.8.0` | Vercel CLI version to install |
| `environment` | `preview` | Environment whose settings and environment variables to pull: `preview` or `production` |
| `project-dir` | `.` | Directory to link to the project |

### deploy

| Flag | Default | Description |
|---|---|---|
| `environment` | `preview` | `preview` or `production` |
| `staged` | `false` | With `environment: production`, deploy without moving the production domains, so the build can be tested and then promoted |
| `prebuilt` | `true` | Build in the job with `vercel build` and upload the output. `false` builds on Vercel |
| `project-dir` | `.` | Directory to deploy |
| `timeout` | `10m` | How long to wait for the deployment. It keeps running on Vercel after that |
| `cli-version` | `65.8.0` | Vercel CLI version to install |

In a monorepo, leave `project-dir` at the repository root: the Vercel CLI applies the project's
Root Directory setting.

A failed build prints Vercel's build log in the step output and links the deployment's page.

### wait-for-preview

| Flag | Default | Description |
|---|---|---|
| `commit` | `$CIRCLE_SHA1` | Commit whose preview to wait for |
| `timeout` | `10m` | How long to wait for the preview to appear and be ready |

If no preview appears, the error says whether the project is connected to a Git repository.

### promote

| Flag | Default | Description |
|---|---|---|
| `deployment` | | ID or URL to promote. Empty uses `VERCEL_DEPLOYMENT_ID` from an earlier step, else this commit's staged production deployment, so `promote` works in a later job too |
| `timeout` | `5m` | How long to wait for production to point at it |

`promote` only takes production deployments. Vercel rebuilds a preview to promote it, with
production's environment variables, so production would not run what was tested. Deploy with
`environment: production` and `staged: true` instead, as in the example above.

### rollback

| Flag | Default | Description |
|---|---|---|
| `deployment` | | ID or URL to roll back to. Empty uses the production deployment before the current one |
| `timeout` | `5m` | How long to wait for production to point at it |

## Platforms

Linux, macOS and Windows, on amd64 and arm64. On Windows, `vercel` goes on PATH through
`$BASH_ENV`, so it resolves in later `bash` steps.

## Releasing

See [RELEASE.md](RELEASE.md).
