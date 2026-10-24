# Documentation site (MkDocs + Material). Snippets in the pages pull
# CHANGELOG.md and CONTRIBUTING.md from the repository root, so mkdocs runs
# from the root; the root Makefile aliases these as `make docs` / `make docs-serve`.
.PHONY: install build serve

install: ## Install the documentation toolchain
	python -m pip install -r requirements.txt

build: install ## Build the site strictly (broken links and nav entries fail the build)
	cd .. && python -m mkdocs build --strict

serve: install ## Serve the site locally with live reload
	cd .. && python -m mkdocs serve
