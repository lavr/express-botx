# Repository Guidelines

## Releases

- Perform planned releases only through `./release.sh` from the `main` branch.
- Run `./release.sh status` before releasing, then use the appropriate command:
  `./release.sh app`, `./release.sh chart`, or `./release.sh both`.
- Without a terminal, pass every version spec plus `--yes`, for example
  `./release.sh both minor patch --yes`. A spec is `patch`, `minor`, `major` or
  a literal `X.Y.Z`. Consent is never inferred from the absence of a terminal.
- `--dry-run` prints the plan and exits before any tag, commit or push, and does
  not require `--yes`. Use it to check a release from a phone before cutting it.
- Relative specs resolve against the current tags, so they are not retry
  identifiers: after an interrupted release whose outcome is unknown, re-run
  with explicit `X.Y.Z` and check `./release.sh status` first.
- `scripts/tests/release_test.sh` covers the parser, version rules, consent and
  dry-run invariance; run it after touching `release.sh`.
- Manual releases, including manual tag creation or pushing, are allowed only in
  exceptional cases when the standard script is unsuitable. Document the reason
  for bypassing `release.sh` and verify the resulting versions and tags.
- The repository is mirrored to GitVerse (`gitverse` remote). Syncing the mirror
  is a separate manual step: `git push gitverse main --tags`. Pushing a release
  tag there triggers the GitVerse release workflow. See `docs/gitverse.md`.
