.PHONY: help build test vet lint golden clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'

build: ## Build the monup binary
	go build -o bin/monup ./cmd/monup

test: ## Run all tests with race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

golden: ## Regenerate render golden files (review the diff!)
	go test ./internal/render -update

clean: ## Remove build artifacts
	rm -rf bin
