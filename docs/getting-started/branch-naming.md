# Branch Naming Convention

MEME follows the [Conventional Branch](https://conventional-branch.github.io/)
specification, extended with additional types to mirror the commit convention.

Branch names are validated by `.github/scripts/lint_branch.py` when a pull
request is opened. A PR cannot be merged until the branch name passes the
check.

## Format

```
<type>/<description>
```

## Types

| Type | Purpose |
|---|---|
| `feat/` or `feature/` | New feature |
| `fix/` or `bugfix/` | Bug fix |
| `hotfix/` | Urgent production fix (branches off `main`) |
| `release/` | Release preparation |
| `chore/` | Tooling, dependencies, CI changes |
| `docs/` | Documentation only |
| `refactor/` | Code restructuring, no behaviour change |
| `test/` | Tests only |
| `style/` | Formatting, whitespace — no logic changes |
| `perf/` | Performance improvements |

## Rules

- **Lowercase only** — no uppercase letters
- **Hyphens as separators** — no underscores, no spaces
- **No consecutive hyphens or dots** — `--` and `..` are not allowed
- **No leading or trailing hyphens or dots**
- **Dots only for version numbers** — e.g. `release/0.1.0-alpha`
- **Ticket numbers are allowed** — e.g. `feat/issue-123-login-page`

## Exempt branches

The following branches are exempt from naming rules:

`main`, `master`, `dev`, `develop`, `staging`

## Examples

```
feat/calliope-0.6-target
fix/operate-fixed-trade-and-links
hotfix/solver-process-leak
docs/capability-reference
chore/remove-template-automation
release/0.1.0-alpha
refactor/split-data-table-writer
test/multi-node-objective-pins
feat/issue-42-stochastic-scenarios
```

## Invalid examples

| Branch name | Problem |
|---|---|
| `Feature/AddLogin` | Uppercase letters |
| `fix/header_bug` | Underscore — use `fix/header-bug` |
| `new-feature` | Missing type prefix |
| `feat/new--login` | Consecutive hyphens |
| `fix/` | Empty description |

## GitHub auto-generated branch names

When you create a branch directly from a GitHub issue using the **"Create a branch"** button, GitHub generates a name like `123-fix-login-bug`. This format has no type prefix and will fail the check.

Rename it before opening a PR:

```bash
git fetch origin
git checkout 123-fix-login-bug
git branch -m 123-fix-login-bug fix/123-login-bug
git push origin -u fix/123-login-bug
git push origin --delete 123-fix-login-bug
```

## Fixing a failed branch name check

The branch name check only runs when the PR is **opened** or **reopened**. To fix a failing branch name you need to rename the branch and update the PR.

```bash
# Rename the branch locally
git branch -m old-branch-name feat/correct-branch-name

# Push the renamed branch and update remote tracking
git push origin -u feat/correct-branch-name

# Delete the old branch from remote
git push origin --delete old-branch-name
```

Then update the PR base branch on GitHub if needed, or close and reopen the PR from the renamed branch.
