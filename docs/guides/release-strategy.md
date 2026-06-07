# Release strategy

Aphelion releases should move through a reviewable release branch, not directly from
`main` to a tag. The release branch is the place where version-specific release
notes, release automation, and final review converge.

## Goals

- keep release work organized and auditable;
- make the release delta reviewable before publication;
- preserve a release PR as the human-readable release artifact;
- publish through guarded GitHub automation after the release PR is approved and merged;
- explain not only what changed, but what the change means for operators and users.

## Branch model

1. Identify the last published release tag, for example `v0.2.2`.
2. Create the next release branch from that tag, not from `main`:

   ```bash
   git checkout -b release/v0.2.3 v0.2.2
   ```

   Starting from the previous release tag makes the branch represent exactly the
   release line being advanced.

3. Open a pull request from `main` into the release branch:

   ```text
   base: release/v0.2.3
   compare: main
   ```

   This PR is the release review surface. It should show the complete delta from
   the last release to the proposed release.

4. Review and approve the release PR.
5. Merge the release PR into the release branch.
6. After the merge, `.github/workflows/release.yml` publishes the release from
   the release PR merge commit. It derives the release tag from the release branch
   name, refuses to overwrite an existing tag, builds release artifacts, creates
   the tag, and publishes the GitHub Release using the release PR body as the
   release-note source.

## Release branch as stabilization window

The release branch also creates a short stabilization window before publication.
If review finds a last-minute release blocker, documentation gap, validation issue,
or small polish change, the fix can still land before publication, but it should
remain visible to review and release-note generation. Prefer one of these paths:

- merge the fix to `main`, so the existing `main` -> release PR includes it;
- or open a separate reviewed PR directly into the release branch, then record how
  that release-only change will be back-merged or cherry-picked to `main`.

Avoid unreviewed direct pushes to the release branch. They can bypass the
`main` -> release PR diff and leave the release PR description out of sync with
what is actually published.

Those changes should remain narrow and release-scoped. Larger follow-up work should
return to `main` and wait for a later release. The release branch is a final review
and stabilization surface, not a second long-running development branch.

## Release PR content

The release PR description should become the draft release notes. It should include
both evidence and meaning.

### Evidence

- merged PRs included in the release;
- important commits or commit ranges;
- user-visible changes;
- operational changes;
- migrations or compatibility notes;
- validation results;
- known risks or follow-up work.

### Meaning

The release PR should also explain why the release matters:

- what value this gives an operator;
- what kind of failure mode it prevents;
- what becomes easier, safer, or more observable;
- what authority boundary, recovery loop, or diagnostic surface changed;
- how the release advances Aphelion's shape as a governed agent runtime.

This keeps release notes from becoming a mechanical commit list. The release PR is
where Aphelion translates implementation into operational significance.

## Automation contract

After a release PR is merged into a `release/v*` branch, GitHub Actions validates
and publishes the release. The release workflow also keeps `v*` tag pushes and
manual dispatch as maintainer recovery paths, but the normal path is the reviewed
release PR.

The automation is narrow and explicit:

- accept only release branches matching the chosen naming scheme, for example
  `release/v*`;
- derive or verify the release version from the branch/tag plan;
- build and test the release candidate from the release PR merge commit;
- create the git tag only after validation passes, and refuse to overwrite an existing tag;
- publish the GitHub release using the release PR body as the release-note
  source;
- fail closed if the branch name, version, tag, or release-note source is
  ambiguous.

Automation should not turn every merge to `main` into a release. `main` remains the
integration branch; the release branch remains the publication gate.

## Review checklist

Before merging a release PR:

- [ ] The release branch was created from the previous release tag.
- [ ] The PR base is the release branch and the compare branch is `main`.
- [ ] The PR description names the release version and scope.
- [ ] The PR description includes both evidence and meaning.
- [ ] Validation is current and cited.
- [ ] Known risks and operator-facing changes are explicit.
- [ ] The automation trigger and expected tag/release outcome are clear.

## Non-goals

- Do not publish a release directly from `main`.
- Do not tag before the release PR is reviewed and merged.
- Do not use release automation to bypass review.
- Do not collapse release notes into a raw commit list.
