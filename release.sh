#!/usr/bin/env bash
set -euo pipefail

CHART_FILE="charts/express-botx/Chart.yaml"

usage() {
    echo "Usage: $0 <command>"
    echo ""
    echo "Commands:"
    echo "  app      Tag app release"
    echo "  chart    Update Chart.yaml and tag chart release"
    echo "  both     Tag app + update chart + tag chart"
    echo "  status   Show current versions and latest tags"
    exit 1
}

current_app_tag() {
    git tag --sort=-v:refname | grep -v chart | head -1
}

current_chart_tag() {
    git tag --sort=-v:refname | grep '^chart-' | head -1 | sed 's/^chart-//'
}

chart_version() {
    grep '^version:' "$CHART_FILE" | awk '{print $2}'
}

chart_app_version() {
    grep '^appVersion:' "$CHART_FILE" | awk '{print $2}' | tr -d '"'
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

pick_version() {
    local current="$1" label="$2"
    local v_patch v_minor v_major
    v_patch=$(bump "$current" patch)
    v_minor=$(bump "$current" minor)
    v_major=$(bump "$current" major)

    echo ""
    echo "$label (current: $current):"
    echo "  1) patch  -> $v_patch"
    echo "  2) minor  -> $v_minor"
    echo "  3) major  -> $v_major"
    printf "Choose [1/2/3]: "
    read -r choice
    case "$choice" in
        1) echo "$v_patch" ;;
        2) echo "$v_minor" ;;
        3) echo "$v_major" ;;
        *) echo "Invalid choice"; exit 1 ;;
    esac
}

confirm() {
    printf "%s [y/N] " "$1"
    read -r ans
    [[ "$ans" =~ ^[Yy]$ ]] || exit 0
}

status() {
    echo "App:"
    echo "  latest tag:            $(current_app_tag)"
    echo "Chart:"
    echo "  latest tag:            chart-$(current_chart_tag)"
    echo "  Chart.yaml version:    $(chart_version)"
    echo "  Chart.yaml appVersion: $(chart_app_version)"
}

release_app() {
    local version
    version=$(pick_version "$(current_app_tag)" "App version")

    if git tag -l "$version" | grep -q .; then
        echo "Error: tag $version already exists"
        exit 1
    fi

    echo ""
    echo "Commits since $(current_app_tag):"
    git log --oneline "$(current_app_tag)..HEAD"
    echo ""
    confirm "Create tag $version?"

    git tag "$version"
    echo "Created tag: $version"
    echo "Run 'git push origin $version' to trigger release"
}

release_chart() {
    local version
    version=$(pick_version "$(current_chart_tag)" "Chart version")
    local tag="chart-${version}"

    if git tag -l "$tag" | grep -q .; then
        echo "Error: tag $tag already exists"
        exit 1
    fi

    local old_version
    old_version=$(chart_version)

    echo ""
    echo "Will update Chart.yaml: version $old_version -> $version"
    confirm "Proceed?"

    sed -i '' "s/^version: .*/version: ${version}/" "$CHART_FILE"
    git add "$CHART_FILE"
    git commit -m "chart version bump"
    git tag "$tag"

    echo "Created commit and tag: $tag"
    echo "Run 'git push origin main $tag' to trigger chart release"
}

release_both() {
    local app_version chart_ver
    app_version=$(pick_version "$(current_app_tag)" "App version")
    chart_ver=$(pick_version "$(current_chart_tag)" "Chart version")
    local chart_tag="chart-${chart_ver}"

    if git tag -l "$app_version" | grep -q .; then
        echo "Error: tag $app_version already exists"
        exit 1
    fi
    if git tag -l "$chart_tag" | grep -q .; then
        echo "Error: tag $chart_tag already exists"
        exit 1
    fi

    local old_chart_version
    old_chart_version=$(chart_version)

    echo ""
    echo "Plan:"
    echo "  1. Tag app: $app_version"
    echo "  2. Update Chart.yaml: version $old_chart_version -> $chart_ver, appVersion -> $app_version"
    echo "  3. Tag chart: $chart_tag"
    echo ""
    echo "Commits since $(current_app_tag):"
    git log --oneline "$(current_app_tag)..HEAD"
    echo ""
    confirm "Proceed?"

    git tag "$app_version"
    echo "Created tag: $app_version"

    sed -i '' "s/^version: .*/version: ${chart_ver}/" "$CHART_FILE"
    sed -i '' "s/^appVersion: .*/appVersion: \"${app_version}\"/" "$CHART_FILE"
    git add "$CHART_FILE"
    git commit -m "chart version bump"
    git tag "$chart_tag"

    echo "Created commit and tags: $app_version, $chart_tag"
    echo "Run 'git push origin main $app_version $chart_tag' to trigger releases"
}

case "${1:-}" in
    app)    release_app ;;
    chart)  release_chart ;;
    both)   release_both ;;
    status) status ;;
    *)      usage ;;
esac
