# Legal Information Sync

The template includes an automated workflow that propagates copyright information across all repositories in the `THD-Spatial-AI` and `enerplanet` organizations. When the copyright holder is updated in this template, a pull request is automatically opened in every org repository with the change.

## What gets synced

The following fields are updated — everything else in each file is left untouched:

| File | Field synced |
|---|---|
| `LICENSE` | Copyright holder line (license type and year preserved) |
| `mkdocs.yml` | `copyright:` field link text |
| `CITATION.cff` | `affiliation:` field(s) |
| `README.md` | Any `Copyright (c) YEAR ...` lines |

The copyright holder string is read directly from this template's `LICENSE` file, so it is always the single source of truth.

## One-time setup

### 1. Create org-scoped fine-grained PATs

Create one token per organization. For each org (`THD-Spatial-AI` and `enerplanet`):

1. Go to **GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens → Generate new token**
2. Set **Resource owner** to the organization (not your personal account)
3. Set **Repository access** to **All repositories**
4. Under **Permissions → Repositories**, add:
    - **Contents** — Read and write
    - **Pull requests** — Read and write
5. Under **Permissions → Organizations**, add:
    - **Members** — Read-only (required to list org repositories)
6. Generate and copy the token

### 2. Add secrets to the template repo

In this template repo go to **Settings → Secrets and variables → Actions → New repository secret** and add:

| Secret name | Value |
|---|---|
| `SYNC_PAT_THD` | Token scoped to `THD-Spatial-AI` |
| `SYNC_PAT_ENERPLANET` | Token scoped to `enerplanet` |

## Triggering the sync

### Automatic

The workflow runs automatically on every push to `main` when any of these files change:

- `LICENSE`
- `mkdocs.yml`
- `CITATION.cff`

### Manual (with dry-run)

Before finalizing legal text, use the dry-run mode to preview changes without creating PRs:

1. Go to **Actions → Sync Legal Information → Run workflow**
2. Check **"Dry run — list changes without creating PRs"**
3. Click **Run workflow**

The log will show exactly which files would be updated in each repository. Once satisfied, run the workflow again without dry-run to open the PRs.

## Opting out

Individual repositories can opt out by adding `.github/.templatesync-ignore` to their root:

```
# Skip specific files
LICENSE
mkdocs.yml

# Or opt out entirely
*
```

Repositories that are archived or are forks are always skipped automatically.
