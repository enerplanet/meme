# Makefile for the meme (energymodel) service — build & run only.
#
# Testing lives in test/Makefile, the container environment in
# environment/Makefile; the test aliases at the bottom keep the familiar
# `make test`, `make e2e` working from the root. The container environment
# is driven directly: `make -C environment <build|test|run|shell> ENV=dev`.
#
#   make            # build the server binary
#   make run        # build and run the API (dry-run mode)
#   make help       # list all targets (including the delegated ones)

# --- Config -----------------------------------------------------------------
BINARY      := meme
CMD_PKG     := ./cmd/meme
BIN_DIR     := bin
BIN         := $(BIN_DIR)/$(BINARY)

# ADDR, WORK, EXEC use ?= so the container (or your shell) can override them via
# the environment — that's how the compose env files feed ports/paths in.
ADDR        ?= :8080
WORK        ?= .work
EXEC        ?=
# Translate a truthy EXEC into the -exec flag for the `run` target.
EXEC_FLAG   := $(if $(filter 1 true yes on,$(EXEC)),-exec,)

GO          := go
GOFLAGS     :=
LDFLAGS     := -s -w

# --- Meta -------------------------------------------------------------------
.DEFAULT_GOAL := build
.PHONY: build run run-exec fmt vet tidy clean help

# --- Build ------------------------------------------------------------------
build: ## Compile the server binary into bin/
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN) $(CMD_PKG)
	@echo "built $(BIN)"

# --- Run --------------------------------------------------------------------
run: build ## Build and run the API (EXEC=1 enables real solvers; default dry-run)
	$(BIN) -addr $(ADDR) -work $(WORK) $(EXEC_FLAG)

run-exec: build ## Build and run the API with real solver execution
	$(BIN) -addr $(ADDR) -work $(WORK) -exec

# --- Source hygiene ----------------------------------------------------------
fmt: ## Format all Go sources
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

# --- Delegated: testing (test/Makefile) --------------------------------------
TEST_TARGETS := test test-race golden-update schema-check e2e-smoke e2e
.PHONY: $(TEST_TARGETS)
$(TEST_TARGETS): ## Test targets — defined in test/Makefile
	$(MAKE) -C test $@

# --- Housekeeping -----------------------------------------------------------
clean: ## Remove build artifacts and emitted work files
	$(GO) clean
	rm -rf $(BIN_DIR) $(WORK)

help: ## Show this help (root + delegated targets)
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo "  --- testing (make -C test help) ---"
	@$(MAKE) --no-print-directory -C test help
	@echo "  --- environment (make -C environment help) ---"
	@$(MAKE) --no-print-directory -C environment help
