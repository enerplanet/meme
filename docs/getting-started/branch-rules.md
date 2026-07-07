# Branch Protection Rules

The template includes a workflow that applies consistent branch protection rules across all repositories in the `THD-Spatial-AI` and `enerplanet` organizations.

## Branch strategy

| Branch | Purpose | Protection level |
|---|---|---|
| `main` | Production | Strict — PR + approval + CI required |
| `dev` | Testing / integration | Moderate — PR + approval + CI required |
| `feature/*` | New features | None — merge into `dev` via PR |
| `fix/*` | Bug fixes | None — merge into `dev` via PR |
| `docs/*` | Documentation | None — merge into `dev` via PR |
| `hotfix/*` | Urgent production fixes | None — merge directly into `main` via PR |
| `release/*` | Release preparation | None — merge into `main` via PR |

## Protection rules

### `main`

- Pull request required before merging (1 approval minimum)
- Stale reviews dismissed when new commits are pushed
- Branch must be up to date with `main` before merging
- CI status checks must pass
- Force pushes and deletions blocked
- **Admins can bypass all rules**

### `dev`

- Pull request required before merging (1 approval minimum)
- CI status checks must pass
- Force pushes and deletions blocked
- **Admins can bypass all rules**

## Triggering the sync

This workflow is **manual-only** — branch rules are intentionally not auto-applied on every push since they are less frequently changed than legal information.

1. Go to **Actions → Sync Branch Protection Rules → Run workflow**
2. Enter the comma-separated CI check names to require (e.g. `build,test`) — leave empty to skip CI enforcement
3. Optionally enable dry-run to preview without applying
4. Click **Run workflow**

!!! tip "Finding your CI check names"
    Check names come from the `jobs.<job-id>.name` field in your workflow files, or from the **Checks** tab on any PR. Common names are `build`, `test`, `ci`, `lint`.

## Opting out

Add `.github/.templatesync-ignore` to a repo with `branch-rules` to skip it:

```
branch-rules
```

Or use `*` to opt out of all template syncs entirely. Archived repos and forks are always skipped.

## Notes

- The `dev` branch rule is only applied if the `dev` branch already exists in the repository.
- These rules use the GitHub classic branch protection API. Admins retain full bypass capability and can override rules when needed.
- Re-running the workflow overwrites existing protection rules on `main` and `dev` — any manual customizations to those branches will be replaced.
