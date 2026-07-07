# Open Source Readiness Checklist

Use this checklist before making a repository public under the **THD-Spatial-AI** group.

This checklist is designed to be practical and easy to review. It separates **required**, **conditional**, and **recommended** items so you can focus on what matters first.

---

## Review Summary

| Section | Requirement Level | Status | Notes |
| ------- | ----------------- | ------ | ----- |
| [Essential Requirements](#essential-requirements) | Required | Pending | Must be completed before public release |
| [Data Management (Git LFS)](#data-management-required-if-applicable) | Required if applicable | Pending | Only if repository contains large files |
| [Attribution](#attribution-required-if-applicable) | Required if applicable | Pending | Only if project uses third-party assets requiring attribution |
| [Citation](#citation-recommended-for-research-projects) | Recommended | Pending | For research projects — enables proper academic citation |
| [Optional but Recommended Files](#optional-but-recommended-files-recommended) | Recommended | Pending | Strongly encouraged for public/community repos |
| [Quality Checks](#quality-checks-required) | Required | Pending | Final review before publishing |
| [Final Steps](#final-steps-required) | Required | Pending | Verify repository works from a fresh clone |

---

## Essential Requirements

Make sure the following essential requirements are completed before making your repository public.

Quick links: [LICENSE](#license) · [README](#readme) · [CONTRIBUTING](#contributing) · [CODE_OF_CONDUCT](#code_of_conduct)

### LICENSE

- [ ] [LICENSE](https://github.com/THD-Spatial-AI/github-template/blob/main/LICENSE) file present in repository root
- [ ] Appropriate license chosen (see [Choose a License](https://choosealicense.com/))
- [ ] Replace license content with correct license text for chosen license type
- [ ] License committed to repository

### README

- [ ] `README.md` file present in repository root

#### README content

- [ ] Project title and short description included
- [ ] Project purpose clearly explained
- [ ] Key features listed
- [ ] Installation/setup instructions provided (if applicable)
- [ ] Usage examples or usage steps included
- [ ] Contribution guidance referenced (e.g. `CONTRIBUTING.md`)
- [ ] README uses clear Markdown formatting

### CONTRIBUTING

- [ ] `CONTRIBUTING.md` file present in repository root
- [ ] Issue reporting process documented
- [ ] Pull request submission process documented
- [ ] Coding standards / best practices outlined (if applicable)
- [ ] Commit message guidance included (recommended)
- [ ] `CONTRIBUTING.md` linked from `README.md`

### CODE_OF_CONDUCT

- [ ] `CODE_OF_CONDUCT.md` file present in repository root
- [ ] Code of conduct chosen (e.g. [Contributor Covenant](https://www.contributor-covenant.org/))
- [ ] Contact method for reporting issues included
- [ ] `CODE_OF_CONDUCT.md` linked from `README.md` or documentation

!!! warning

    Without files listed above, your repository does not meet the minimum requirements for public release under the **THD-Spatial-AI** organization. Please complete all essential items before proceeding to make your repository public.
---

## Data Management (Required if applicable)

If your repository contains large files (e.g. datasets, binaries, media, generated assets), you must use Git LFS to manage them.

### Git LFS (Large File Storage)

Complete this section if your repository contains large files (e.g. datasets, binaries, media, generated assets).

- [ ] Git LFS installed locally
- [ ] Git LFS initialised in repository (`git lfs install`)
- [ ] Large file types tracked (e.g. data files, media, binaries)
- [ ] `.gitattributes` committed to repository
- [ ] Large files added and committed properly
- [ ] Release strategy for LFS files documented (if applicable)

> **Note:** Files tracked by Git LFS are not included in release assets by default.
> If needed, enable **Include Git LFS objects in archives** in repository settings.

---

## Attribution (Required if applicable)

If your project uses third-party components, assets, or generated code that require attribution (e.g. UI components from Figma, design systems, libraries under Apache 2.0, BSD, or CC BY licenses), include an `ATTRIBUTIONS.md` file.

### ATTRIBUTION

- [ ] `ATTRIBUTIONS.md` file created in repository root
- [ ] Third-party component or asset names listed
- [ ] Source platform or author identified for each entry
- [ ] License type specified for each entry
- [ ] Links to original sources included
- [ ] Any required attribution text preserved as-is from the original license/notice

!!! note

    If no third-party assets requiring attribution are used, this section can be skipped.

---

## Citation (Recommended for research projects)

If your project is related to research or uses open-source tools and data that should be cited, include a `CITATION.cff` file. GitHub automatically detects this file and shows a "Cite this repository" button on the repository page.

For full specification details, see [Citation File Format](https://citation-file-format.github.io/).

### CITATION.cff

- [ ] `CITATION.cff` file created in repository root
- [ ] `cff-version` set to `1.2.0`
- [ ] Project title specified
- [ ] At least one author listed (with name and affiliation)
- [ ] ORCID included for each author (if available)
- [ ] License field matches the repository `LICENSE` file
- [ ] Repository URL included
- [ ] Version and release date set
- [ ] File validates against the CFF schema (use [cff-validator](https://github.com/citation-file-format/cff-initializer-javascript))

!!! tip

    You can generate a `CITATION.cff` file interactively using the [CFF Initializer](https://citation-file-format.github.io/cff-initializer-javascript/).

---

## Optional but Recommended Files (Recommended)

These files are not always required, but they improve project quality, collaboration, and maintainability.

### CHANGELOG

- [ ] `CHANGELOG.md` file created (if using versioned releases)
- [ ] Version history documented
- [ ] Changes formatted consistently (e.g. [Keep a Changelog](https://keepachangelog.com/))

### Issue and PR Templates

- [ ] `.github/ISSUE_TEMPLATE/` directory created (if using issue templates)
- [ ] Issue template(s) added
- [ ] `.github/pull_request_template.md` file added
- [ ] PR template includes a contributor checklist (recommended)

### Code Owners

- [ ] `CODEOWNERS` file created (root or `.github/`)
- [ ] Owners assigned for relevant parts of the repository
- [ ] Teams/individuals correctly specified

### Security Policy

- [ ] `SECURITY.md` file created
- [ ] Vulnerability reporting process documented
- [ ] Contact method for security issues provided
- [ ] Supported versions documented (if applicable)

### Support Guidelines

- [ ] `SUPPORT.md` file created
- [ ] Support channels documented (e.g. email, discussions, issue tracker)
- [ ] Response expectations documented (if applicable)

---

## Quality Checks (Required)

### Documentation Quality

- [ ] Documentation reviewed for clarity
- [ ] Spelling and grammar checked
- [ ] Links tested and working
- [ ] Code examples/commands tested (if applicable)

### Repository Settings

- [ ] Repository description added on GitHub
- [ ] Topics/tags added for discoverability
- [ ] Repository visibility set to **Public**
- [ ] Default branch configured (typically `main`)
- [ ] Branch protection rules configured (if needed)

### Code Quality / Safety

- [ ] Code reviewed and cleaned up
- [ ] Sensitive information removed (API keys, passwords, credentials)
- [ ] `.gitignore` configured properly
- [ ] Dependencies documented
- [ ] Build/test instructions included (if applicable)

---

## Final Steps (Required)

- [ ] All essential requirements completed
- [ ] Repository tested by cloning a fresh copy
- [ ] Links and references verified
- [ ] Project builds/runs successfully from scratch (if applicable)
- [ ] Team members notified about repository availability (if applicable)

---

## Repository Ready for Open Source

Once all required items are completed, the repository meets the THD-Spatial-AI minimum readiness standard for public release.

- **Date completed:** `DD.MM.YYYY`
- **Reviewed by:** `Name(s)`

---

## Need Help?

If you have questions about any checklist item:

- Review the template `README.md`
- Check the documentation pages in `docs/`
- Ask the THD-Spatial-AI maintainers/reviewers
