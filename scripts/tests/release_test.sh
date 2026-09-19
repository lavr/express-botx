#!/usr/bin/env bash
set -uo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/release-test.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT
git clone --quiet --local --no-hardlinks "$SOURCE_ROOT" "$ROOT" 2>/dev/null || {
    echo "FAIL could not clone the repository into $ROOT"
    exit 1
}
cp "$SOURCE_ROOT/release.sh" "$ROOT/release.sh"
git -C "$ROOT" checkout --quiet main 2>/dev/null || true
git -C "$ROOT" remote remove origin 2>/dev/null || true
BARE=$(mktemp -d "${TMPDIR:-/tmp}/release-test-remote.XXXXXX")
trap 'rm -rf "$ROOT" "$BARE"' EXIT
git init --quiet --bare "$BARE"
git -C "$ROOT" remote add origin "$BARE"
git -C "$ROOT" add release.sh
git -C "$ROOT" -c user.email=test@example.com -c user.name=test \
    commit --quiet -m "fixture: release.sh under test" 2>/dev/null || true

RELEASE="$ROOT/release.sh"
failures=0

setsid_free() {
    if command -v setsid >/dev/null 2>&1; then
        setsid "$RELEASE" "$@" </dev/null 2>&1
    else
        "$RELEASE" "$@" </dev/null 2>&1 &
        local pid=$!
        wait "$pid"
    fi
}

expect_message() {
    local label="$1" needle="$2"; shift 2
    local out rc
    out=$(cd "$ROOT" && setsid_free "$@"); rc=$?
    if (( rc == 0 )); then
        echo "FAIL $label: expected non-zero exit, got success"
        failures=$((failures + 1))
        return
    fi
    if [[ "$out" != *"$needle"* ]]; then
        echo "FAIL $label: expected %$needle%, got: $out"
        failures=$((failures + 1))
        return
    fi
    echo "ok   $label"
}

expect_ok() {
    local label="$1" needle="$2"; shift 2
    local out rc
    out=$(cd "$ROOT" && setsid_free "$@"); rc=$?
    if (( rc != 0 )); then
        echo "FAIL $label: expected success, got $rc: $out"
        failures=$((failures + 1))
        return
    fi
    if [[ "$out" != *"$needle"* ]]; then
        echo "FAIL $label: expected %$needle%, got: $out"
        failures=$((failures + 1))
        return
    fi
    echo "ok   $label"
}

echo "--- parser ---"
expect_message "unknown command"        "unknown command"       release --yes
expect_message "unknown option"         "unknown option"        both minor patch --force
expect_message "too many specs"         "at most 1"             app minor patch --yes
expect_message "status takes no opts"   "status takes no"       status --yes
expect_message "empty spec"             "empty version spec"    app "" --yes

echo "--- versions ---"
expect_message "leading zeros"          "invalid version"       app 01.02.03 --yes
expect_message "not an increase"        "not higher"            app 0.0.1 --yes

echo "--- consent and tty ---"
expect_message "no tty, no spec"        "no terminal to ask"    app
expect_message "no tty, no --yes"       "no terminal to confirm" both minor patch

echo "--- dry run ---"
expect_ok "dry run prints plan"         "dry run: nothing changed"  both minor patch --dry-run
expect_ok "dry run needs no --yes"      "Plan:"                     app minor --dry-run
expect_ok "status"                      "latest tag"                status

snapshot() {
    git -C "$ROOT" status --porcelain
    git -C "$ROOT" show-ref --head --dereference | sort
    git -C "$ROOT" rev-parse HEAD
    git -C "$BARE" show-ref 2>/dev/null | sort
    shasum "$ROOT/charts/express-botx/Chart.yaml" 2>/dev/null | awk '{print $1}'
}

before=$(snapshot)
(cd "$ROOT" && "$RELEASE" both minor patch --dry-run </dev/null >/dev/null 2>&1) || {
    echo "FAIL dry run exited non-zero"
    failures=$((failures + 1))
}
after=$(snapshot)
if [[ "$before" != "$after" ]]; then
    echo "FAIL dry run changed repository state"
    failures=$((failures + 1))
else
    echo "ok   dry run leaves refs, HEAD, chart and remote untouched"
fi

echo ""
if (( failures )); then
    echo "$failures failure(s)"
    exit 1
fi
echo "all release.sh checks passed"
