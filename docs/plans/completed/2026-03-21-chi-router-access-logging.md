# Migrate to chi router with access logging and X-Request-ID

## Overview

Replace the standard `http.ServeMux` with `go-chi/chi` router to get built-in access logging middleware and X-Request-ID propagation for all HTTP requests in serve mode.

## Context

- Files involved:
  - `internal/server/server.go` - main server setup, route registration, middleware chain
  - `internal/server/server_test.go` - test helpers (`newTestServer`, `doRequest`)
  - `go.mod` / `go.sum` - new dependency
  - `vendor/` - vendored chi modules
- Related patterns: middleware is applied as handler wrappers; APM/errtrack use `http.Handler` interface
- Dependencies: `github.com/go-chi/chi/v5` (router + middleware)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- chi is fully compatible with `http.Handler`/`http.HandlerFunc` - existing handlers need no changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add chi dependency

**Files:**
- Modify: `go.mod`

- [x] Run `go get github.com/go-chi/chi/v5` to add chi dependency
- [x] Run `go mod vendor` to vendor the dependency
- [x] Verify build compiles: `go build ./...`

### Task 2: Replace http.ServeMux with chi router and add middleware

**Files:**
- Modify: `internal/server/server.go`

- [x] Replace `http.NewServeMux()` with `chi.NewRouter()`
- [x] Add `middleware.RequestID` as the first middleware in the chi chain (generates X-Request-ID if not present in request, sets response header)
- [x] Add `middleware.Logger` as the next middleware for access logging (logs method, path, status, duration to stderr)
- [x] Convert route registrations from `mux.HandleFunc("GET /healthz", ...)` / `mux.Handle(pattern, ...)` to chi's `r.Get("/healthz", ...)` / `r.Post(...)` / `r.Route(base, ...)` syntax
- [x] Keep existing middleware wrappers (authMiddleware, apm.WrapHandler, errTracker.Middleware, callbackJWTMiddleware) - they return `http.Handler` and work with chi
- [x] Ensure errTracker.Middleware wraps the chi router as the outermost handler (same as before)
- [x] Update tests in `server_test.go` to work with the chi-based server (doRequest helper uses `srv.srv.Handler.ServeHTTP` which stays the same)
- [x] Run `go test ./internal/server/...` - must pass

### Task 3: Verify acceptance criteria

- [x] Run full test suite: `go test ./...`
- [x] Run linter if available: `go vet ./...`
- [x] Manual smoke test (skipped - not automatable)

### Task 4: Update documentation

- [x] Update README.md if user-facing changes (no user-facing changes - chi migration is internal)
- [x] Move this plan to `docs/plans/completed/`
