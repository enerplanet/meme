# Contributing

Thank you for taking the time to contribute to **[PROJECT_NAME]**.

This project welcomes contributions such as bug reports, feature requests, documentation improvements, code changes, and general feedback.

Please read this guide before opening an issue or submitting a pull request.

## Code of Conduct

By participating in this project, you agree to follow the rules and expectations described in the [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to Contribute

You can contribute in different ways, including:

- Reporting bugs
- Requesting features or improvements
- Improving documentation
- Fixing issues
- Reviewing pull requests
- Asking and answering questions

## Before You Start

Before creating a new issue or pull request, please:

- Read the `README.md` to understand the project purpose and setup
- Check existing issues and pull requests to avoid duplicates
- Make sure your idea/request is relevant to the project scope
- Use the issue templates (if available)

## Reporting Bugs and Requesting Changes

Use the project issue tracker for bug reports, feature requests, and documentation issues.

- **Issue tracker:** [INSERT_ISSUE_TRACKER_URL]
- **Discussions / Questions (optional):** [INSERT_DISCUSSION_URL_OR_REMOVE]

When reporting an issue, please include:

- What you expected to happen
- What actually happened
- Steps to reproduce the issue
- Screenshots/logs/error messages (if applicable)
- Environment details (OS, browser, version, etc., if relevant)

## Development Workflow

The exact setup steps may differ by project. Please check the `README.md` and project documentation for installation and development instructions.

### 1) Fork and clone the repository (if applicable)

If you do not have direct write access, fork the repository first, then clone your fork:

```bash
git clone [REPOSITORY_URL]
cd [REPOSITORY_DIRECTORY]
```

If you have direct write access, clone the main repository instead.

### 2) Create a branch for your change

Create a dedicated branch for your bugfix, feature, or documentation update:

```bash
git checkout -b type/short-description
```

Examples:

- `fix/login-validation`
- `feat/export-yaml`
- `docs/readme-setup`

### 3) Make your changes

Keep changes focused and small where possible. If your change is large, consider splitting it into multiple pull requests.

### 4) Test your changes (if applicable)

Before submitting a pull request:

- Run relevant tests
- Check linting/formatting tools (if used)
- Verify the project still builds/runs locally
- Update documentation if your change affects usage or behaviour

### 5) Commit your changes

Use clear commit messages that explain what changed.

```bash
git add .
git commit -m "Short summary of the change"
```

For larger changes, include a more descriptive commit message when needed.

### 6) Push your branch

```bash
git push -u origin <your-branch-name>
```

### 7) Open a pull request

Create a pull request against the appropriate branch (usually `main` unless the project uses a different workflow).

In your pull request description, include:

- What changed
- Why it changed
- Any screenshots (for UI changes)
- Testing notes
- Related issue(s), if applicable (e.g. `Closes #123`)

## Pull Request Checklist

Before submitting a pull request, check:

- [ ] The change is relevant and scoped appropriately
- [ ] I tested my changes (if applicable)
- [ ] I updated documentation (if applicable)
- [ ] I followed the project coding/style conventions (if applicable)
- [ ] I checked for sensitive information (keys, credentials, private data)
- [ ] I linked related issues (if applicable)

## Commit Message Guidance (Recommended)

Keep commit messages clear and specific.

Good examples:

- `Fix CSV upload validation for empty headers`
- `Add YAML export button to model builder`
- `Update installation steps in README`

Avoid vague messages such as:

- `fix`
- `changes`
- `update stuff`

## Documentation Contributions

Documentation improvements are welcome and valuable.

If you are updating docs:

- Keep wording clear and practical
- Prefer short examples where useful
- Check links and commands
- Match the style used in existing documentation

## Project-Specific Notes (Template Placeholder)

Replace or remove this section in project repositories.

Examples of what may go here:

- Setup links (Windows/Linux/Docker)
- Testing commands (`npm test`, `pytest`, `go test ./...`)
- Branching strategy
- Review/approval rules
- CI requirements
- Changelog policy

## Licensing of Contributions

By contributing to this project, you confirm that:

- your contribution is your own work (or you have the right to submit it), and
- you agree that your contribution will be licensed under the same license as this repository.

## Need Help?

If you are unsure where to start, open an issue or discussion and ask. Maintainers can help point you in the right direction.

---

## Maintainer Note (Template)

> [!CAUTION]
> This file is a template. Replace placeholders such as:

- `[PROJECT_NAME]`
- `[INSERT_ISSUE_TRACKER_URL]`
- `[INSERT_DISCUSSION_URL_OR_REMOVE]`
- `[REPOSITORY_URL]`
- `[REPOSITORY_DIRECTORY]`

Remove sections that do not apply to your project.
