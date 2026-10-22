# Repository Guidelines

## Releases

- Perform planned releases only through `./release.sh` from the `main` branch.
- Run `./release.sh status` before releasing, then use the appropriate command:
  `./release.sh app`, `./release.sh chart`, or `./release.sh both`.
- Without a terminal, pass every version spec plus `--yes`, for example
  `./release.sh both minor patch --yes`. A spec is `patch`, `minor`, `major` or
  a literal `X.Y.Z`. Consent is never inferred from the absence of a terminal.
- App releases are tagged `vX.Y.Z`, so Go resolves them as versions of the
  `github.com/lavr/express-botx` module. Tags up to `0.43.0` are bare `X.Y.Z`
  and stay as history; never add `v` tags to them retroactively. Only a
  `vX.Y.Z` tag triggers the release workflows. The version itself carries no
  `v`: binaries, Docker image tags, release names and the chart `appVersion`
  stay `X.Y.Z`. Chart tags remain `chart-X.Y.Z`.
- A published tag is never moved or recreated: the Go module proxy caches it
  for good. Fix a bad release with a new version.
- Versions 2.0.0 and later need the module path suffix `/vN` in `go.mod`
  first; `release.sh` refuses them until then.
- `--dry-run` prints the plan and exits before any tag, commit or push, and does
  not require `--yes`. Use it to check a release from a phone before cutting it.
- Relative specs resolve against the current tags, so they are not retry
  identifiers. Before picking a version `release.sh` compares tags with
  `origin` and stops when:
  - the latest app or chart tag is only local (an interrupted release): finish
    it with the `git push --atomic origin main <tags>` it prints, never by
    releasing the next version. The script does not undo a release; abandoning
    one means removing its local tags and any chart bump commit by hand;
  - such a tag points at another commit on `origin`: published tags are never
    moved, settle which one is right first;
  - `origin` has newer versions than the clone: `git fetch origin --tags`;
  - `origin` is unreachable.
- `scripts/tests/release_test.sh` covers the parser, version rules, consent,
  dry-run invariance, publishing to a local bare remote, rejected pushes and
  recovery; run it after touching `release.sh` or the `Makefile` version.
- Manual releases, including manual tag creation or pushing, are allowed only in
  exceptional cases when the standard script is unsuitable. Document the reason
  for bypassing `release.sh` and verify the resulting versions and tags.
- The repository is mirrored to GitVerse (`gitverse` remote). Syncing the mirror
  is a separate manual step: `git push gitverse main --tags`. Pushing a release
  tag there triggers the GitVerse release workflow. See `docs/gitverse.md`.
