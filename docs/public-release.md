# Public Release Provenance

This document defines the release-history boundary for Aphelion.

## Canonical Source

The canonical public source is:

```text
github.com/idolum-ai/aphelion
```

Public documentation, install snippets, issue templates, and contribution
workflows should point to that repository after the public cutover.

## Clean Public Cut

The first public/canonical cut should be made from a reviewed clean tree rather
than by publishing all private pre-public Git history. The clean tree should:

- exclude private runtime artifacts, local configs, logs, databases, transcripts,
  forensic artifacts, and credential-location details;
- avoid live account identifiers, private child-agent names, local absolute paths,
  and operational chat or channel identifiers in public docs/config examples;
- keep examples generic, using placeholder IDs, `example.test` accounts, and
  sample child-agent names;
- preserve attribution honestly through this provenance note and release notes
  instead of rewriting private history into a misleading public story.

## Before Publication

Before making `idolum-ai/aphelion` public or pushing a canonical release cut:

```bash
make public-readiness
make architecture
make design-principles
go test ./...
git diff --check
```

If Gitleaks is installed, also run:

```bash
make secrets
```

A Gitleaks history scan on the private working repository may report known
private-history findings. That is a blocker for publishing that full history, not
for creating a clean public cut from a reviewed tree.
