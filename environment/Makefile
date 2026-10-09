# Containerized environment lifecycle for the meme (energymodel) service:
# one Docker image carrying Go, the three frameworks (PyPSA, Calliope,
# AdOpT-NET0) and their solvers (HiGHS, CBC, GLPK).
#
#   make -C environment build   # build the image
#   make -C environment test    # run the Go suite inside the container
#   make -C environment run     # serve the API with real solvers
#   make -C environment shell   # interactive shell in the image
#
# ENV=dev (default) or prod selects .env.$(ENV).

ENV      ?= dev
ENV_FILE := .env.$(ENV)
COMPOSE  := docker compose --env-file $(ENV_FILE) -f docker-compose.yml

.DEFAULT_GOAL := help
.PHONY: build test run shell deploy down logs help

build: ## Build the environment image (Go + frameworks + solvers)
	$(COMPOSE) build

test: ## Build and run the Go test suite inside the container
	$(COMPOSE) run --rm test

run: ## Run the API in the container with real solvers
	$(COMPOSE) up api

shell: ## Interactive shell in the environment image
	$(COMPOSE) run --rm shell

deploy: ## Production: build the self-contained image and start detached (ENV=prod.local)
	docker compose --env-file $(ENV_FILE) -f docker-compose.prod.yml up -d --build

down: ## Production: stop the deployed stack
	docker compose --env-file $(ENV_FILE) -f docker-compose.prod.yml down

logs: ## Production: follow API logs
	docker compose --env-file $(ENV_FILE) -f docker-compose.prod.yml logs -f api

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
