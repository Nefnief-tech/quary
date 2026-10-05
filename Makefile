.PHONY: build test test-unit lint fmt tidy migrate proto dev down clean help

GO ?= go

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build panel and daemon binaries into ./bin
	$(GO) build -trimpath -o bin/panel ./panel/cmd/panel
	$(GO) build -trimpath -o bin/daemon ./daemon/cmd/daemon

test: ## Run unit and integration tests (integration needs Docker)
	$(GO) test -tags=integration -count=1 ./...

test-unit: ## Run unit tests only
	$(GO) test -count=1 ./...

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format Go code
	$(GO) fmt ./...

tidy: ## Tidy go module
	$(GO) mod tidy

migrate: ## Apply database migrations (uses QUARRY_DATABASE_URL)
	$(GO) run ./panel/cmd/panel migrate

proto: ## Regenerate protobuf/gRPC code
	cd proto && buf generate

dev: ## Bring up the dev stack (postgres + panel + daemon + web)
	@if [ ! -f deploy/.env ]; then \
		passwd=$$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n'); \
		printf 'POSTGRES_DB=quarry\nPOSTGRES_USER=quarry\nPOSTGRES_PASSWORD=%s\nQUARRY_ENROLLMENT_TOKEN=\nQUARRY_DOCKER_UID=%s\nQUARRY_DOCKER_GID=%s\n' "$$passwd" "$$(id -u)" "$$(id -g)" > deploy/.env; \
		echo "generated deploy/.env with a random database password"; \
	fi
	mkdir -p data/servers data/daemon
	docker compose --env-file deploy/.env -f deploy/docker-compose.dev.yml up --build

web-dev: ## Run the web UI dev server (proxies /api to the panel)
	pnpm --dir web run dev

web-build: ## Build the web UI
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web run check
	pnpm --dir web run build

down: ## Tear down the dev stack
	docker compose --env-file deploy/.env -f deploy/docker-compose.dev.yml down -v

clean: ## Remove build artifacts
	rm -rf bin
