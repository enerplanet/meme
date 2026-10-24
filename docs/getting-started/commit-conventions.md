# Commit Message Conventions

MEME follows the [Conventional Commits](https://www.conventionalcommits.org/)
specification. Commit messages are validated on every pull request by
`.github/scripts/lint_commits.py`; a PR cannot be merged until all commit
messages pass the check.

## Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

Only the first line is required. The body explains why the change is made;
`Closes #N` in the footer links the issue.

## Types

| Type | When to use |
|---|---|
| `feat` | A new feature: a canonical field, a capability, a target, a flag |
| `fix` | A bug fix, including an emitter writing what a framework reads differently |
| `docs` | Documentation changes only |
| `style` | Formatting or whitespace — no logic changes |
| `refactor` | Code restructuring without new features or bug fixes |
| `perf` | Performance improvements |
| `test` | Adding or updating tests, scenarios and golden files |
| `chore` | Build, tooling, CI, dependencies, releases and other housekeeping |

## Rules

- **Use imperative mood** — write `add login endpoint`, not `added login endpoint`
- **No capital letter** at the start of the subject
- **No period** at the end of the subject
- **Max 100 characters** for the subject line
- Scope is optional but recommended — use the package or area the change
  affects (`api`, `cli`, `model`, `target`, `calliope`, `pypsa`, `adopt`,
  `scenarios`, `environment`, `ci`, `release`, `docs`)

## Examples

```
feat(target): support framework versions as separate targets
fix(calliope): charge annualized costs once per year
docs(contributing): describe how a release is cut
test(calliope): pin multi-node demand across frameworks
chore(ci): add the test, schema and vulnerability workflow
chore(release): prepare 0.1.0-alpha
```

## Breaking changes

Add a `BREAKING CHANGE:` footer when a change is not backwards compatible.
Before 1.0 this applies to any change that makes a previously valid payload
fail validation, alters a documented API response, or changes what a target
emits for an unchanged payload beyond a bug fix:

```
feat(model): require timeseries.source on every series

BREAKING CHANGE: payloads whose time series carry values without
source: inline no longer validate
```

## Common mistakes

| Wrong | Correct |
|---|---|
| `updated readme` | `docs: update readme` |
| `Fix bug` | `fix: correct null pointer in parser` |
| `feat: Add new endpoint.` | `feat: add new endpoint` |
| `WIP` | `chore: scaffold route handler` |

## Fixing a failed commit lint check

When the **Validate commit messages** check fails on your PR, follow the steps below depending on how many commits need fixing.

### Fix the most recent commit

```bash
git commit --amend -m "feat(scope): your corrected message"
git push --force-with-lease
```

### Fix multiple commits

Use an interactive rebase to reword each failing commit. The workflow log tells you exactly which commit SHAs need fixing.

```bash
# Replace N with the number of commits in your PR
git rebase -i HEAD~N
```

In the editor that opens, change `pick` to `reword` for each commit you want to fix, save and close. Git will pause at each one so you can enter the corrected message. Then push:

```bash
git push --force-with-lease
```

!!! warning "Force push requires branch to be up to date"
    After `--force-with-lease`, the PR will automatically re-run the commit lint check. If someone else pushed to the branch in the meantime, the force push will be rejected — pull first with `git pull --rebase` and then push again.
