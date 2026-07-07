# Commit Message Conventions

All repositories in the `THD-Spatial-AI` and `enerplanet` organizations follow the [Conventional Commits](https://www.conventionalcommits.org/) specification, originally adopted by the EU Commission component library.

Commit messages are automatically validated on every pull request. A PR cannot be merged until all commit messages pass the check.

## Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

Only the first line is required. Body and footer are optional.

## Types

| Type | When to use |
|---|---|
| `feat` | A new feature |
| `fix` | A bug fix |
| `docs` | Documentation changes only |
| `style` | Formatting or whitespace — no logic changes |
| `refactor` | Code restructuring without new features or bug fixes |
| `perf` | Performance improvements |
| `test` | Adding or updating tests |
| `chore` | Build process, tooling, or dependency updates |

## Rules

- **Use imperative mood** — write `add login endpoint`, not `added login endpoint`
- **No capital letter** at the start of the subject
- **No period** at the end of the subject
- **Max 100 characters** per line
- Scope is optional but recommended — use it to indicate the affected area (e.g. `api`, `auth`, `docs`)

## Examples

```
feat(api): add geospatial query endpoint
fix(auth): correct token expiry validation
docs: update installation instructions
chore(ci): upgrade actions/checkout to v4
refactor(parser): simplify coordinate transformation logic
test(export): add unit tests for GeoJSON output
```

## Breaking changes

Add a `BREAKING CHANGE:` footer when a change is not backwards compatible:

```
feat(api): replace coordinate system

BREAKING CHANGE: all endpoints now return WGS84 instead of ETRS89
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

### Using AI to rewrite messages

If you are unsure how to rewrite a message, paste it into an AI assistant:

> *"My commit message `update greeting message in main function` failed the conventional commits check. The allowed types are: feat, fix, docs, style, refactor, perf, test, chore. Please rewrite it in the correct format `type(scope): subject`."*

The AI will suggest something like `feat(main): update greeting message`.

!!! warning "Force push requires branch to be up to date"
    After `--force-with-lease`, the PR will automatically re-run the commit lint check. If someone else pushed to the branch in the meantime, the force push will be rejected — pull first with `git pull --rebase` and then push again.
