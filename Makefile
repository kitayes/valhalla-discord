.PHONY: help build test test-race test-integration load-test db-test-up db-test-down vet lint fmt tidy check run docker-up docker-down clean

BINARY := blackwatch
BIN_DIR := bin

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Compile the bot
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/app

test: ## Run unit tests
	go test ./...

test-race: ## Run unit tests under the race detector (needs cgo + a C compiler)
	CGO_ENABLED=1 go test -race ./...

# The repository layer is SQL held in strings: a wrong column name, a Scan with
# the wrong arity or a join that does not resolve compiles and vets cleanly, and
# fails only when a user presses the button. These run it against a real
# Postgres. BW_TEST_DSN must point at a database with the migrations applied.
# -p 1: the packages share one database, and the bot's synthetic tournament
# breaks when the repository tests switch the active tournament under it.
test-integration: ## Run repository and synthetic bot tests against a real Postgres (needs BW_TEST_DSN)
	@test -n "$(BW_TEST_DSN)" || { echo "set BW_TEST_DSN first — see 'make db-test-up'"; exit 1; }
	go test -p 1 -tags integration ./internal/... -count=1

load-test: ## Load-test the match desk as a tournament drives it (needs BW_LOAD_DSN to a throwaway database: it is wiped)
	@test -n "$(BW_LOAD_DSN)" || { echo "set BW_LOAD_DSN to a throwaway database, e.g. dbname=bwload on 'make db-test-up'"; exit 1; }
	go test -tags loadtest -run TestDeskLoad -v -count=1 -timeout 10m ./internal/application/

db-test-up: ## Start a throwaway Postgres with pgvector for test-integration
	docker rm -f bw_testdb 2>/dev/null || true
	docker run -d --name bw_testdb -e POSTGRES_PASSWORD=bwtest -e POSTGRES_DB=bw \
		-p 55432:5432 quay.io/enterprisedb/postgresql:16
	@echo 'then: export BW_TEST_DSN="host=127.0.0.1 port=55432 user=postgres password=bwtest dbname=bw sslmode=disable"'

db-test-down: ## Remove the throwaway Postgres
	docker rm -f bw_testdb 2>/dev/null || true

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (install: https://golangci-lint.run/welcome/install/)
	golangci-lint run

fmt: ## Format the codebase
	gofmt -s -w .

tidy: ## Prune and verify module dependencies
	go mod tidy
	go mod verify

check: fmt vet test ## Format, vet and test — run before pushing

run: ## Run the bot locally
	go run ./cmd/app

docker-up: ## Start the stack
	docker compose up -d --build

docker-down: ## Stop the stack
	docker compose down

clean: ## Remove build artefacts
	rm -rf $(BIN_DIR)
	go clean -testcache
