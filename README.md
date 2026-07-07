
# THD-Spatial-AI Repository Template

[![MkDocs](https://github.com/THD-Spatial-AI/github-template/actions/workflows/docs.yml/badge.svg)](https://thd-spatial-ai.github.io/github-template)

This repository is a template for projects under the `THD-Spatial-AI` GitHub group. It provides a basic structure, documentation, and guidance to help you prepare repositories for internal collaboration and open-source release.

## What this template includes

- `README.md` (project overview and usage guidance)
- `CONTRIBUTING.md` (contribution workflow and expectations)
- `LICENSE` (required before making a repository public)
- `docs/` (documentation pages for naming conventions and open-source readiness)
- `mkdocs.yml` (MkDocs configuration for documentation site generation)
- `ATTRIBUTIONS.md` (third-party attribution, if applicable)
- `CITATION.cff` (citation metadata for research projects)

## Before making a repository public

Use the open-source readiness checklist before publishing a repository under `THD-Spatial-AI`.

- **Checklist:** [Open Source Checklist](docs/getting-started/open-source-checklist.md)

The checklist covers:

- essential repository files (license, README, contributing, code of conduct)
- Git LFS setup for large files
- optional but recommended files
- final review steps before publication

## Repository naming

All repositories under `THD-Spatial-AI` should follow a consistent naming convention.

- **Guidelines:** [Repository Naming Guidelines](docs/getting-started/repository-naming.md)

## Required files for public repositories

> [!IMPORTANT]
> Repositories under `THD-Spatial-AI` must include the following files before being made public. See the [Open Source Checklist](docs/getting-started/open-source-checklist.md) for the full list.

| File | Requirement | Purpose |
|------|-------------|---------|
| `LICENSE` | Required | Legal permission for use, modification, and distribution ([Choose a License](https://choosealicense.com/)) |
| `README.md` | Required | Project overview, setup, and usage instructions |
| `CONTRIBUTING.md` | Required for community repos | Issue reporting, PR process, coding standards |
| `CODE_OF_CONDUCT.md` | Required for community repos | Community expectations ([Contributor Covenant](https://www.contributor-covenant.org/)) |
| `ATTRIBUTIONS.md` | Required if applicable | Third-party credits when using assets that require attribution |
| `CITATION.cff` | Recommended for research | Machine-readable citation metadata ([Citation File Format](https://citation-file-format.github.io/)) |
| `.gitattributes` | Required if using LFS | Git LFS tracking for large files ([Git LFS docs](https://git-lfs.com/)) |

### Optional but useful files

- `CHANGELOG.md` — track notable changes
- `CODEOWNERS` — define review ownership
- `.github/ISSUE_TEMPLATE/` — issue templates
- `.github/pull_request_template.md` — PR template
- `SECURITY.md` — vulnerability reporting policy
- `SUPPORT.md` — support and contact guidance

## Documentation (MkDocs)

This template uses [MkDocs](https://www.mkdocs.org/) with [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) for project documentation. Source files are in `docs/`.

For setup instructions (local development, GitHub Pages deployment, and workflow configuration), see the [Documentation Setup Guide](docs/getting-started/documentation-setup.md).

> [!CAUTION]
> **TO ALL MAINTAINERS:** Please review the [open-source readiness checklist](docs/getting-started/open-source-checklist.md) and ensure all required files are included before making a repository public.
>
> This template is intended to be practical and easy to adapt. Keep it lightweight, take some time to remove sections you do not need **(Including this one)**, and update links/paths if you rename files in `docs/`.
