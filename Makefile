.DEFAULT_GOAL := check
.PHONY: check init lint vet test race benchmark help test_version

GO ?= go
GOLANGCI_LINT ?= golangci-lint
goversion ?= 1.27.4

check: lint vet race ## Run the contributor checks

init: ## Download module dependencies and install the configured linter
	$(GO) mod download
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

lint: ## Lint all three packages
	$(GOLANGCI_LINT) run ./...

vet: ## Vet all packages
	$(GO) vet ./...

test: ## Run short tests
	$(GO) test -short ./...

race: ## Run the full suite with the race detector
	$(GO) test -race ./...

benchmark: ## Run benchmarks and report allocations
	$(GO) test -run='^$$' -bench=. -benchmem ./...

test_version: ## Run vet and the race suite in the selected Go container
	docker run --rm -v "$(CURDIR):/project" -w /project golang:$(goversion) make vet race

help: ## Display available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_]+:.*?## / {printf "%-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
