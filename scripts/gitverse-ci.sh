#!/usr/bin/env bash
# GitVerse CI helper: list runs, jobs and read logs via public API.
#
# Auth: GITVERSE_TOKEN env var or ~/.gitverse_token file
# (token from https://gitverse.ru/settings/tokens with "Публичное API" scope).
#
# Usage:
#   scripts/gitverse-ci.sh runs            # list workflow runs
#   scripts/gitverse-ci.sh jobs <run_id>   # list jobs of a run
#   scripts/gitverse-ci.sh logs <job_id>   # print job log
#   scripts/gitverse-ci.sh last            # jobs of the latest run
#   scripts/gitverse-ci.sh secrets         # list CI secret names (values are not exposed)
set -euo pipefail

API="https://api.gitverse.ru"
REPO="${GITVERSE_REPO:-lavr/express-botx}"
TOKEN="${GITVERSE_TOKEN:-$(cat "$HOME/.gitverse_token" 2>/dev/null || true)}"

if [[ -z "$TOKEN" ]]; then
    echo "Error: set GITVERSE_TOKEN or put token into ~/.gitverse_token" >&2
    exit 1
fi

gv_api() {
    curl -sf \
        -H "Authorization: Bearer $TOKEN" \
        -H "Accept: application/vnd.gitverse.object+json;version=1" \
        "$API/repos/$REPO/$1"
}

runs() {
    gv_api "actions/runs" | python3 -c '
import json, sys
for r in json.load(sys.stdin)["workflow_runs"]:
    print(f'"'"'{r["id"]}  {r["status"]:<8}  {r["name"]:<16}  {r["ref"].removeprefix("refs/heads/"):<14}  {r["title"]}'"'"')
'
}

jobs() {
    gv_api "actions/runs/$1/jobs" | python3 -c '
import json, sys
for j in json.load(sys.stdin)["jobs"]:
    print(f'"'"'{j["id"]}  {j["status"]:<8}  {j["name"]:<14}  {j.get("started_at","")} .. {j.get("completed_at","")}'"'"')
'
}

case "${1:-}" in
    runs) runs ;;
    jobs) jobs "${2:?usage: $0 jobs <run_id>}" ;;
    logs) gv_api "actions/jobs/${2:?usage: $0 logs <job_id>}/logs" ;;
    secrets)
        gv_api "actions/secrets" | python3 -c '
import json, sys
for s in json.load(sys.stdin)["secrets"]:
    print(f'"'"'{s["name"]}  (created {s["created_at"]})'"'"')
'
        ;;
    last)
        run_id=$(gv_api "actions/runs" | python3 -c 'import json,sys; print(json.load(sys.stdin)["workflow_runs"][0]["id"])')
        echo "run $run_id:"
        jobs "$run_id"
        ;;
    *)
        grep '^#   ' "$0" | sed 's/^#   //'
        exit 1
        ;;
esac
