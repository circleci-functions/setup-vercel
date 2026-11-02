# Changelog

All notable changes to setup-vercel will be documented in this file. Each release's section is
added by its release PR (see [RELEASE.md](RELEASE.md)) and becomes its GitHub release notes.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-11-02

### What's Changed
* Smoke-test on a Linux VM too by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/9
* Fix the first deploy of a project, and wait for promotions to finish by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/11


**Full Changelog**: https://github.com/circleci-functions/setup-vercel/compare/v0.1.0...v0.2.0

## [0.1.0] - 2026-10-30

The first release: `setup`, `deploy`, `wait-for-preview`, `promote` and `rollback`.

### What's Changed
* Add a minimal CircleCI config and CODEOWNERS by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/2
* Scaffold setup-vercel from setup-go by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/3
* Deploy, wait for, promote and roll back Vercel deployments by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/4
* Test with the real Vercel CLI against a fake Vercel API by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/5
* Smoke-test the packaged binary on Linux, macOS and Windows by @stiyyagura0901 in https://github.com/circleci-functions/setup-vercel/pull/6

**Full Changelog**: https://github.com/circleci-functions/setup-vercel/commits/v0.1.0
