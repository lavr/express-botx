# Add Makefile targets and Zed tasks

## Overview

Extend the Makefile with standard Go development targets (test, lint, fmt, race, docker-build) and create .zed/tasks.json with corresponding tasks for quick execution from Zed editor, following the ralphex project conventions.

## Context

- Files involved: `Makefile`, `.zed/tasks.json` (new)
- Related patterns: ralphex project Makefile and .zed/tasks.json conventions
- Dependencies: golangci-lint (for lint target)

## Development Approach

- **Testing approach**: Regular (manual verification of Makefile targets)
- Complete each task fully before moving to the next

## Implementation Steps

### Task 1: Extend Makefile with new targets

**Files:**
- Modify: `Makefile`

- [x] Add `test` target: run `go test -race -coverprofile=coverage.out -tags "sentry newrelic kafka rabbitmq" ./...` with coverage summary, excluding vendor
- [x] Add `lint` target: run `golangci-lint run`
- [x] Add `fmt` target: run gofmt and goimports on project .go files (excluding vendor)
- [x] Add `race` target: run `go test -race -timeout=60s -tags "sentry newrelic kafka rabbitmq" ./...`
- [x] Add `docker-build` target: build docker image with appropriate tag
- [x] Add `version` target: print current version info
- [x] Update `.PHONY` declaration with all new targets
- [x] Verify Makefile syntax by running `make -n test` and `make -n build`

### Task 2: Create .zed/tasks.json

**Files:**
- Create: `.zed/tasks.json`

- [x] Add "build" task running `make build`
- [x] Add "test: all" task running `make test`
- [x] Add "test: race" task running `make race`
- [x] Add "lint" task running `make lint`
- [x] Add "fmt" task running `make fmt`
- [x] Add "docker: build" task running `make docker-build`
- [x] Add "run: serve" task running `go run . serve` for local development
- [x] All tasks use `$ZED_WORKTREE_ROOT` as cwd, `use_new_terminal: true`, `allow_concurrent_runs: false`
- [x] Verify JSON syntax is valid

### Task 3: Verify acceptance criteria

- [x] Run `make -n test` and `make -n build` to verify Makefile syntax
- [x] Run `make -n lint` to verify lint target
- [x] Validate `.zed/tasks.json` is valid JSON
