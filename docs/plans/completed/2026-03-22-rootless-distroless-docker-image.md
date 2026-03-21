# Rootless/distroless Docker image

## Overview

Add a second Docker image variant based on `scratch` containing only the static binary and CA certificates. Update CI to build and push both variants. Switch Helm chart default to the rootless image.

## Context

- Files involved: `Dockerfile`, `.github/workflows/release.yml`, `charts/express-botx/values.yaml`
- The binary is already built with `CGO_ENABLED=0` (fully static) — no changes to the Go build needed
- Helm chart already enforces `runAsNonRoot: true`, `runAsUser: 65534`, `readOnlyRootFilesystem: true`
- CI builds multi-platform images (linux/amd64, linux/arm64) and pushes to Docker Hub as `lavr/express-botx`

## Development Approach

- Regular approach (code first)
- Changes are infrastructure-only (Dockerfile, CI, Helm) — no Go code or Go tests affected
- Validate with `helm template` and `docker build` commands

## Implementation Steps

### Task 1: Add rootless target to Dockerfile

**Files:**
- Modify: `Dockerfile`

Use multi-stage targets in the same Dockerfile: the existing alpine stage becomes a named target (`alpine`), and a new `rootless` target uses `scratch` with only the binary and CA certs. A `USER 65534` directive bakes the non-root user into the rootless image.

- [x] Rename the final `FROM alpine:3.21` stage to `FROM alpine:3.21 AS alpine`
- [x] Add a new stage `FROM scratch AS rootless` that copies CA certificates from the build stage (`/etc/ssl/certs/ca-certificates.crt`) and the binary
- [x] Add `USER 65534` to the rootless stage
- [x] Set `ENTRYPOINT ["express-botx"]` in the rootless stage
- [x] Verify both targets build: `docker build --target alpine .` and `docker build --target rootless .`

### Task 2: Update CI to build and push both image variants

**Files:**
- Modify: `.github/workflows/release.yml`

Add a second `docker/build-push-action` step for the rootless variant with `-rootless` tag suffix.

- [x] Add a second build-push step that uses `--target rootless` and tags `lavr/express-botx:<version>-rootless` and `lavr/express-botx:latest-rootless`
- [x] Add `--target alpine` to the existing build-push step to keep it explicit
- [x] Verify the workflow YAML is valid

### Task 3: Switch Helm chart default to rootless image

**Files:**
- Modify: `charts/express-botx/values.yaml`

- [x] Change `image.tag` default from `""` (appVersion) to append `-rootless` suffix by default
- [x] Add a comment explaining how to switch back to the alpine-based image (override `image.tag` without `-rootless` suffix)
- [x] Validate with `helm template charts/express-botx` — image reference should be `lavr/express-botx:<appVersion>-rootless`

### Task 4: Verify acceptance criteria

- [x] `docker build --target rootless -t test-rootless .` succeeds
- [x] `docker build --target alpine -t test-alpine .` succeeds
- [x] `helm template charts/express-botx` renders the rootless image tag by default
- [x] `helm template --set image.tag=0.30.1 charts/express-botx` can override to alpine variant
