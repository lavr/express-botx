#!/usr/bin/env bash
set -uo pipefail

SOURCE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/release-test.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT
git clone --quiet --local --no-hardlinks "$SOURCE_ROOT" "$ROOT" 2>/dev/null || {
    echo "FAIL could not clone the repository into $ROOT"
    exit 1
}
git -C "$ROOT" checkout --quiet main 2>/dev/null || true
cp "$SOURCE_ROOT/release.sh" "$SOURCE_ROOT/Makefile" "$ROOT/"
git -C "$ROOT" remote remove origin 2>/dev/null || true
BARE=$(mktemp -d "${TMPDIR:-/tmp}/release-test-remote.XXXXXX")
trap 'rm -rf "$ROOT" "$BARE"' EXIT
git init --quiet --bare "$BARE"
git -C "$ROOT" remote add origin "$BARE"
git -C "$ROOT" add release.sh Makefile
git -C "$ROOT" config user.email test@example.com
git -C "$ROOT" config user.name test
git -C "$ROOT" commit --quiet -m "fixture: release.sh and Makefile under test" 2>/dev/null || true
# The remote starts as a copy of the published history: main plus every tag.
git -C "$ROOT" push --quiet origin main --tags 2>/dev/null || {
    echo "FAIL could not seed the test remote"
    exit 1
}

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

fail() {
    echo "FAIL $1"
    failures=$((failures + 1))
}

check() {
    local label="$1"; shift
    if "$@"; then echo "ok   $label"; else fail "$label"; fi
}

commit_of() { git -C "$1" rev-parse -q --verify "refs/tags/$2^{commit}" 2>/dev/null; }

add_commit() {
    git -C "$ROOT" commit --quiet --allow-empty -m "fixture: $1"
    git -C "$ROOT" push --quiet origin main
}

echo "--- app tags ---"
latest=$(cd "$ROOT" && ./release.sh status | awk '/latest version/ {print $3}')
IFS=. read -r lmaj lmin _ <<< "$latest"
V1="${lmaj}.$((lmin + 1)).0"
V2="${lmaj}.$((lmin + 2)).0"
V3="${lmaj}.$((lmin + 3)).0"

expect_ok "dry run plans a v tag"       "tag app v$V1"          app minor --dry-run
expect_message "v2 needs a /v2 module"  "module path"           app 2.0.0 --dry-run
set_module() { sed -i '' "s#^module .*#module $1#" "$ROOT/go.mod"; }
set_module github.com/lavr/express-botx/v2
expect_ok "v2 with a /v2 module"        "tag app v2.0.0"        app 2.0.0 --dry-run
expect_message "v3 with a /v2 module"   "module path"           app 3.0.0 --dry-run
expect_message "0.x with a /v2 module"  "without /vN"           app minor --dry-run
git -C "$ROOT" checkout --quiet go.mod

add_commit "app release"
head=$(git -C "$ROOT" rev-parse HEAD)
expect_ok "app release"                 "Pushed tag: v$V1"      app "$V1" --yes
check "app tag on remote at HEAD"       test "$(commit_of "$BARE" "v$V1")" = "$head"
check "no bare app tag created"         test -z "$(commit_of "$BARE" "$V1")"
check "remote main is HEAD"             test "$(git -C "$BARE" rev-parse main)" = "$head"
check "status reads the v tag"          grep -q "latest tag: *v$V1" <<< "$(cd "$ROOT" && ./release.sh status)"
check "make version drops the v"        test "$(make -s -C "$ROOT" version)" = "$V1"
expect_ok "patch bumps from the v tag"  "tag app v${lmaj}.$((lmin + 1)).1" app patch --dry-run
expect_message "released version again" "not higher"           app "$V1" --yes

echo "--- both ---"
add_commit "both release"
app_commit=$(git -C "$ROOT" rev-parse HEAD)
chart_latest=$(cd "$ROOT" && ./release.sh status | awk '/latest tag: *chart-/ {sub("chart-", "", $3); print $3}')
IFS=. read -r cmaj cmin cpatch <<< "$chart_latest"
C1="${cmaj}.${cmin}.$((cpatch + 1))"
expect_ok "both release"                "Pushed tags: v$V2, chart-$C1" both "$V2" "$C1" --yes
check "both: app tag before the bump"   test "$(commit_of "$BARE" "v$V2")" = "$app_commit"
check "both: chart tag on the bump"     test "$(commit_of "$BARE" "chart-$C1")" = "$(git -C "$BARE" rev-parse main)"
check "both: bump is one commit"        test "$(git -C "$ROOT" rev-parse HEAD^)" = "$app_commit"
check "both: appVersion has no v"       grep -q "^appVersion: \"$V2\"$" "$ROOT/charts/express-botx/Chart.yaml"

echo "--- chart only ---"
tags_before=$(git -C "$BARE" tag -l | grep -v '^chart-' | sort)
C2="${cmaj}.${cmin}.$((cpatch + 2))"
expect_ok "chart release"               "Pushed tag: chart-$C2" chart "$C2" --yes
check "chart: no new app tags"          test "$(git -C "$BARE" tag -l | grep -v '^chart-' | sort)" = "$tags_before"
check "make version skips chart tags"   test "$(make -s -C "$ROOT" version | cut -d- -f1)" = "$V2"

# run_printed runs the 'git push' line a failed release.sh printed, so the
# recovery advice itself is what gets tested.
run_printed() {
    local cmd
    cmd=$(grep -E '^  git push --atomic ' <<< "$1" | tail -n 1)
    [[ -n "$cmd" ]] || return 1
    (cd "$ROOT" && eval "$cmd" >/dev/null 2>&1)
}

reject_chart_tags() {
    cat > "$BARE/hooks/pre-receive" <<'HOOK'
#!/bin/sh
while read -r old new ref; do
    case "$ref" in refs/tags/chart-*) echo "rejected by test hook" >&2; exit 1 ;; esac
done
HOOK
    chmod +x "$BARE/hooks/pre-receive"
}

published() {
    local tag
    test "$(git -C "$BARE" rev-parse main)" = "$(git -C "$ROOT" rev-parse main)" || return 1
    for tag in "$@"; do
        test "$(commit_of "$BARE" "$tag")" = "$(commit_of "$ROOT" "$tag")" || return 1
    done
}

echo "--- interrupted chart ---"
reject_chart_tags
C3="${cmaj}.${cmin}.$((cpatch + 3))"
expect_message "chart push rejected"    "retry the same push"   chart "$C3" --yes
rm "$BARE/hooks/pre-receive"
out=$(cd "$ROOT" && setsid_free chart patch --yes)
check "rerun names main and the tag"    grep -q "git push --atomic origin main chart-$C3\$" <<< "$out"
check "rerun offers no tag -d"          test "$(grep -c 'tag -d' <<< "$out")" = 0
check "printed push finishes chart"     run_printed "$out"
check "chart release published"         published "chart-$C3"

echo "--- interrupted both ---"
add_commit "interrupted release"
reject_chart_tags
remote_before=$(git -C "$BARE" show-ref | sort)
C4="${cmaj}.${cmin}.$((cpatch + 4))"
expect_message "rejected push"          "retry the same push"   both "$V3" "$C4" --yes
check "atomic: remote unchanged"        test "$(git -C "$BARE" show-ref | sort)" = "$remote_before"
check "local tags kept"                 test -n "$(commit_of "$ROOT" "v$V3")"
out=$(cd "$ROOT" && setsid_free app patch --yes)
check "next app names both tags"        grep -q "git push --atomic origin main v$V3 chart-$C4\$" <<< "$out"
out=$(cd "$ROOT" && setsid_free both "$V3" "$C4" --yes)
check "same both names both tags"       grep -q "git push --atomic origin main v$V3 chart-$C4\$" <<< "$out"
rm "$BARE/hooks/pre-receive"
# Half of the advice taken by hand: only the app tag reaches the remote.
git -C "$ROOT" push --quiet --atomic origin main "v$V3" 2>/dev/null
out=$(cd "$ROOT" && setsid_free app patch --dry-run)
check "half-pushed: chart still named"  grep -q "git push --atomic origin main chart-$C4\$" <<< "$out"
check "printed push finishes both"      run_printed "$out"
check "both release published"          published "v$V3" "chart-$C4"
expect_ok "next release after recovery" "Plan:"                 app patch --dry-run

echo "--- tag moved on the remote ---"
git -C "$ROOT" tag -f "v$V3" "v$V3^" >/dev/null
expect_message "local and remote differ" "never moved"          app patch --dry-run
git -C "$ROOT" fetch --quiet --force origin "refs/tags/v$V3:refs/tags/v$V3"
check "fetch restores the tag"          published "v$V3"

echo "--- newer tags on the remote ---"
V4="${lmaj}.$((lmin + 4)).0"
V5="${lmaj}.$((lmin + 5)).0"
git -C "$ROOT" push --quiet origin "HEAD:refs/tags/v$V4" 2>/dev/null
expect_message "newer v tag on remote"  "git fetch origin --tags" app "$V5" --dry-run
git -C "$BARE" tag -d "v$V4" >/dev/null
git -C "$ROOT" push --quiet origin "HEAD:refs/tags/$V4" 2>/dev/null
expect_message "newer bare tag on remote" "git fetch origin --tags" app "$V5" --dry-run
git -C "$ROOT" fetch --quiet origin --tags
expect_ok "a newer bare tag is the base" "tag app v${lmaj}.$((lmin + 4)).1" app patch --dry-run

echo "--- unreachable remote ---"
git -C "$ROOT" remote set-url origin "$ROOT/no-such-remote"
expect_message "remote down is not absent" "cannot list tags"   app patch --dry-run
git -C "$ROOT" remote set-url origin "$BARE"

echo ""
if (( failures )); then
    echo "$failures failure(s)"
    exit 1
fi
echo "all release.sh checks passed"
