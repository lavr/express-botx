#!/usr/bin/env bash
set -euo pipefail

CHART_FILE="charts/express-botx/Chart.yaml"
REMOTE="origin"

usage() {
    cat <<USAGE
Usage: $0 <command> [<spec>...] [options]

Commands:
  status                       Show current versions and latest tags
  app    [<spec>]              Tag app release
  chart  [<spec>]              Update Chart.yaml and tag chart release
  both   [<app>] [<chart>]     Tag app + update chart + tag chart

Spec:
  patch | minor | major        Bump relative to the latest tag
  X.Y.Z                        Explicit version, must be higher than the latest tag
  omitted                      Ask interactively (requires a terminal)

Options:
  --yes                        Skip the confirmation prompt
  --dry-run                    Print the plan and exit before any change
  -h, --help                   Show this help

Non-interactive use needs every spec supplied plus --yes; consent is never
inferred from the absence of a terminal. Relative specs are resolved against
the current tags, so they are not safe retry identifiers: prefer X.Y.Z when
retrying a release whose outcome is unknown.
USAGE
}

die() {
    echo "Error: $*" >&2
    exit 1
}

COMMAND=""
SPECS=()
ASSUME_YES=false
DRY_RUN=false

parse_args() {
    local arg
    for arg in "$@"; do
        case "$arg" in
            -h|--help) usage; exit 0 ;;
            --yes)     ASSUME_YES=true ;;
            --dry-run) DRY_RUN=true ;;
            -*)        die "unknown option: $arg" ;;
            *)
                if [[ -z "$COMMAND" ]]; then
                    COMMAND="$arg"
                else
                    SPECS+=("$arg")
                fi
                ;;
        esac
    done

    [[ -n "$COMMAND" ]] || { usage >&2; exit 1; }

    local max_specs
    case "$COMMAND" in
        status)      max_specs=0 ;;
        app|chart)   max_specs=1 ;;
        both)        max_specs=2 ;;
        *)           die "unknown command: $COMMAND" ;;
    esac

    if (( ${#SPECS[@]} > max_specs )); then
        die "$COMMAND takes at most $max_specs version spec(s), got ${#SPECS[@]}: ${SPECS[*]}"
    fi
    if [[ "$COMMAND" == "status" ]] && { $ASSUME_YES || $DRY_RUN; }; then
        die "status takes no options"
    fi

    local spec
    for spec in ${SPECS+"${SPECS[@]}"}; do
        [[ -n "$spec" ]] || die "empty version spec: pass patch, minor, major or X.Y.Z, or omit it"
    done
}

require_main() {
    local branch
    branch=$(git rev-parse --abbrev-ref HEAD)
    [[ "$branch" == "main" ]] || die "release.sh must be run from the main branch (current: $branch)"
}

require_clean_tree() {
    git diff --quiet --cached || die "index has staged changes; the chart bump commit would sweep them in"
    git diff --quiet || die "worktree has uncommitted changes; commit or stash them first"
}

current_app_tag() {
    local out
    out=$(git tag --sort=-v:refname | grep -vE '^chart-' | grep -E '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || true)
    printf '%s' "${out%%$'\n'*}"
}

current_chart_tag() {
    local out
    out=$(git tag --sort=-v:refname | grep -E '^chart-(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || true)
    out="${out%%$'\n'*}"
    printf '%s' "${out#chart-}"
}

chart_version() {
    grep '^version:' "$CHART_FILE" | awk '{print $2}'
}

chart_app_version() {
    grep '^appVersion:' "$CHART_FILE" | awk '{print $2}' | tr -d '"'
}

is_canonical_version() {
    [[ "$1" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

version_gt() {
    local a="$1" b="$2" a1 a2 a3 b1 b2 b3
    IFS='.' read -r a1 a2 a3 <<< "$a"
    IFS='.' read -r b1 b2 b3 <<< "$b"
    (( a1 != b1 )) && { (( a1 > b1 )); return; }
    (( a2 != b2 )) && { (( a2 > b2 )); return; }
    (( a3 > b3 ))
}

bump() {
    local version="$1" part="$2"
    local major minor patch
    IFS='.' read -r major minor patch <<< "$version"
    case "$part" in
        major) echo "$((major + 1)).0.0" ;;
        minor) echo "${major}.$((minor + 1)).0" ;;
        patch) echo "${major}.${minor}.$((patch + 1))" ;;
    esac
}

have_tty() {
    { exec 3<>/dev/tty; } 2>/dev/null || return 1
    exec 3>&-
    return 0
}

PICKED_VERSION=""

pick_version() {
    local current="$1" label="$2" spec="${3:-}"

    if [[ -z "$current" ]]; then
        [[ -n "$spec" ]] || die "$label: no previous tag to bump from; pass an explicit X.Y.Z"
        is_canonical_version "$spec" || die "$label: no previous tag to bump from; pass an explicit X.Y.Z, not $spec"
        PICKED_VERSION="$spec"
        return 0
    fi

    is_canonical_version "$current" || die "$label: latest tag $current is not a canonical X.Y.Z; release with an explicit version after fixing the tags"

    if [[ -n "$spec" ]]; then
        case "$spec" in
            patch|minor|major) PICKED_VERSION=$(bump "$current" "$spec") ;;
            *)
                is_canonical_version "$spec" || die "$label: invalid version $spec: expected patch, minor, major or X.Y.Z without leading zeros"
                PICKED_VERSION="$spec"
                ;;
        esac
        version_gt "$PICKED_VERSION" "$current" || die "$label: $PICKED_VERSION is not higher than the current $current"
        return 0
    fi

    have_tty || die "$label: no terminal to ask on; pass a version spec (patch, minor, major or X.Y.Z)"

    local v_patch v_minor v_major choice
    v_patch=$(bump "$current" patch)
    v_minor=$(bump "$current" minor)
    v_major=$(bump "$current" major)

    {
        echo ""
        echo "$label (current: $current):"
        echo "  1) patch  -> $v_patch"
        echo "  2) minor  -> $v_minor"
        echo "  3) major  -> $v_major"
        printf "Choose [1/2/3]: "
    } > /dev/tty
    read -r choice < /dev/tty || die "$label: no answer"
    case "$choice" in
        1) PICKED_VERSION="$v_patch" ;;
        2) PICKED_VERSION="$v_minor" ;;
        3) PICKED_VERSION="$v_major" ;;
        *) die "invalid choice: $choice" ;;
    esac
    version_gt "$PICKED_VERSION" "$current" || die "$label: $PICKED_VERSION is not higher than the current $current"
}

confirm() {
    $ASSUME_YES && return 0
    have_tty || die "no terminal to confirm on; pass --yes to consent explicitly"
    local ans
    printf "%s [y/N] " "$1" > /dev/tty
    read -r ans < /dev/tty || die "no answer"
    [[ "$ans" =~ ^[Yy]$ ]] || exit 0
}

require_absent_tag() {
    git tag -l "$1" | grep -q . && die "tag $1 already exists"
    return 0
}

status() {
    echo "App:"
    echo "  latest tag:            $(current_app_tag)"
    echo "Chart:"
    echo "  latest tag:            chart-$(current_chart_tag)"
    echo "  Chart.yaml version:    $(chart_version)"
    echo "  Chart.yaml appVersion: $(chart_app_version)"
}

require_chart_fields() {
    local app_too="$1"
    [[ -f "$CHART_FILE" ]] || die "$CHART_FILE not found"
    [[ $(grep -cE '^version:[[:space:]]' "$CHART_FILE") -eq 1 ]] || die "$CHART_FILE must have exactly one version field"
    if [[ "$app_too" == "yes" ]]; then
        [[ $(grep -cE '^appVersion:[[:space:]]' "$CHART_FILE") -eq 1 ]] || die "$CHART_FILE must have exactly one appVersion field"
    fi
}

edit_chart() {
    local chart_ver="$1" app_ver="${2:-}"
    sed -i '' "s/^version:[[:space:]].*/version: ${chart_ver}/" "$CHART_FILE"
    [[ "$(chart_version)" == "$chart_ver" ]] || die "chart version was not updated in $CHART_FILE"
    if [[ -n "$app_ver" ]]; then
        sed -i '' "s/^appVersion:[[:space:]].*/appVersion: \"${app_ver}\"/" "$CHART_FILE"
        [[ "$(chart_app_version)" == "$app_ver" ]] || die "chart appVersion was not updated in $CHART_FILE"
    fi
}

release_app() {
    local base; base=$(current_app_tag)
    pick_version "$base" "App version" "${SPECS[0]:-}"
    local version="$PICKED_VERSION"
    require_absent_tag "$version"

    echo ""
    echo "Plan:"
    echo "  tag app $version at $(git rev-parse --short HEAD) (current: $base)"
    echo "  push $REMOTE main $version"
    echo ""
    echo "Commits since $base:"
    git log --oneline "$base..HEAD"
    echo ""
    $DRY_RUN && { echo "dry run: nothing changed"; return 0; }
    confirm "Create tag $version?"

    git tag "$version"
    git push --atomic "$REMOTE" main "$version"
    echo "Pushed tag: $version"
}

release_chart() {
    local base; base=$(current_chart_tag)
    pick_version "$base" "Chart version" "${SPECS[0]:-}"
    local version="$PICKED_VERSION"
    local tag="chart-${version}"
    require_absent_tag "$tag"
    require_chart_fields no

    echo ""
    echo "Plan:"
    echo "  Chart.yaml version $(chart_version) -> $version"
    echo "  commit chart bump, tag $tag"
    echo "  push $REMOTE main $tag"
    echo ""
    $DRY_RUN && { echo "dry run: nothing changed"; return 0; }
    confirm "Proceed?"

    edit_chart "$version"
    git add "$CHART_FILE"
    git commit -m "chart version bump"
    git tag "$tag"
    git push --atomic "$REMOTE" main "$tag"
    echo "Pushed tag: $tag"
}

release_both() {
    local app_base chart_base
    app_base=$(current_app_tag)
    chart_base=$(current_chart_tag)

    pick_version "$app_base" "App version" "${SPECS[0]:-}"
    local app_version="$PICKED_VERSION"
    pick_version "$chart_base" "Chart version" "${SPECS[1]:-}"
    local chart_ver="$PICKED_VERSION"
    local chart_tag="chart-${chart_ver}"

    require_absent_tag "$app_version"
    require_absent_tag "$chart_tag"
    require_chart_fields yes

    local app_commit; app_commit=$(git rev-parse HEAD)

    echo ""
    echo "Plan:"
    echo "  1. tag app $app_version at ${app_commit:0:7}"
    echo "  2. Chart.yaml version $(chart_version) -> $chart_ver, appVersion -> $app_version"
    echo "  3. tag chart $chart_tag at the chart bump commit"
    echo "  4. push $REMOTE main $app_version $chart_tag"
    echo ""
    echo "Commits since $app_base:"
    git log --oneline "$app_base..HEAD"
    echo ""
    $DRY_RUN && { echo "dry run: nothing changed"; return 0; }
    confirm "Proceed?"

    edit_chart "$chart_ver" "$app_version"
    git add "$CHART_FILE"
    git commit -m "chart version bump"

    git tag "$app_version" "$app_commit"
    git tag "$chart_tag"

    git push --atomic "$REMOTE" main "$app_version" "$chart_tag"
    echo "Pushed tags: $app_version, $chart_tag"
}

parse_args "$@"
cd "$(git rev-parse --show-toplevel)"

if [[ "$COMMAND" == "status" ]]; then
    status
    exit 0
fi

require_main
$DRY_RUN || require_clean_tree

case "$COMMAND" in
    app)   release_app ;;
    chart) release_chart ;;
    both)  release_both ;;
esac
