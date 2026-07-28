# Repository Guidelines

## Releases

- Perform planned releases only through `./release.sh` from the `main` branch.
- Run `./release.sh status` before releasing, then use the appropriate command:
  `./release.sh app`, `./release.sh chart`, or `./release.sh both`.
- Manual releases, including manual tag creation or pushing, are allowed only in
  exceptional cases when the standard script is unsuitable. Document the reason
  for bypassing `release.sh` and verify the resulting versions and tags.
- The repository is mirrored to GitVerse (`gitverse` remote). Syncing the mirror
  is a separate manual step: `git push gitverse main --tags`. Pushing a release
  tag there triggers the GitVerse release workflow. See `docs/gitverse.md`.
