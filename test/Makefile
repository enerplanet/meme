# Test pyramid entry points for the meme (energymodel) service.
#
# The fast tiers run on the host (pure Go, no Python needed); the E2E tiers
# run real solvers inside the environment container (see ../environment/).
#
#   make -C test test           # vet + full host suite (ms–s)
#   make -C test test-race      # race detector over the concurrent packages
#   make -C test golden-update  # accept an intended emitter/validation change
#   make -C test e2e-smoke      # container: solver-backed smoke tier (~1 min)
#   make -C test e2e            # container: full corpus on real solvers
#
# The root Makefile aliases all of these (make test, make e2e, ...).

GO      ?= go
ROOT    := ..

# Environment selector for the containerized E2E tiers: ENV=dev (default) or
# prod. Picks ../environment/.env.$(ENV).
ENV     ?= dev
COMPOSE := docker compose --env-file $(ROOT)/environment/.env.$(ENV) \
           -f $(ROOT)/environment/docker-compose.yml

.DEFAULT_GOAL := test
.PHONY: test test-race golden-update schema-check e2e-smoke e2e help

test: ## Vet + full host test suite (E2E self-skips without MEME_E2E=1)
	cd $(ROOT) && $(GO) vet ./... && $(GO) test ./...

schema-check: ## Validate examples/ + the scenario corpus against schemas/revised_unified_schema.json (schema/code drift gate; needs python3 + jsonschema)
	python3 schema_check.py

test-race: ## Race-detector pass over the concurrent packages
	cd $(ROOT) && $(GO) test -race ./internal/...

golden-update: ## Regenerate the emitter/validation golden files after an intended change
	cd $(ROOT) && $(GO) test ./internal/target -run Golden -update
	cd $(ROOT) && $(GO) test ./internal/target -run Golden

e2e-smoke: ## Real-solver E2E smoke tier in the container (~1 min): 3 lifecycles + multi-target
	$(COMPOSE) run --rm -e MEME_E2E=1 shell \
		go test -short -parallel 3 -timeout 20m -v ./test/e2e

e2e: ## Full real-solver E2E suite in the container (all scenarios, parallel)
	$(COMPOSE) run --rm -e MEME_E2E=1 shell \
		go test -parallel 3 -timeout 60m -v ./test/e2e

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
