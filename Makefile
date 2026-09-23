SHELL := /bin/bash

.DEFAULT_GOAL := build

.PHONY: build build-cli wpgo help fmt fmt-check lint test test-integration test-all ci tools install install-cli clean

BIN_DIR := $(CURDIR)/bin
BIN := $(BIN_DIR)/wpssh-mcp
CMD := ./cmd/wpssh-mcp
CLI_BIN := $(BIN_DIR)/wpgo
CLI_CMD := ./cmd/wpgo

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo "")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION)
CLI_LDFLAGS := $(LDFLAGS) -X main.commit=$(COMMIT) -X main.date=$(DATE)

TOOLS_DIR := $(CURDIR)/.tools
GOFUMPT := $(TOOLS_DIR)/gofumpt
GOIMPORTS := $(TOOLS_DIR)/goimports
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint

ifneq ($(filter wpgo,$(MAKECMDGOALS)),)
RUN_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
$(eval $(RUN_ARGS):;@:)
endif

build:
	@mkdir -p $(BIN_DIR)
	@go build -ldflags "$(LDFLAGS)" -o $(BIN) $(CMD)

build-cli:
	@mkdir -p $(BIN_DIR)
	@go build -ldflags "$(CLI_LDFLAGS)" -o $(CLI_BIN) $(CLI_CMD)

wpgo: build-cli
	@if [ -n "$(RUN_ARGS)" ]; then \
		$(CLI_BIN) $(RUN_ARGS); \
	elif [ -z "$(ARGS)" ]; then \
		$(CLI_BIN) --help; \
	else \
		$(CLI_BIN) $(ARGS); \
	fi

help: build-cli
	@$(CLI_BIN) --help

tools:
	@mkdir -p $(TOOLS_DIR)
	@GOBIN=$(TOOLS_DIR) go install mvdan.cc/gofumpt@v0.9.2
	@GOBIN=$(TOOLS_DIR) go install golang.org/x/tools/cmd/goimports@v0.41.0
	@GOBIN=$(TOOLS_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.9.0

fmt: tools
	@$(GOIMPORTS) -local github.com/builtbyrobben/wpssh -w .
	@$(GOFUMPT) -w .

fmt-check: tools
	@UNFMT=$$($(GOIMPORTS) -local github.com/builtbyrobben/wpssh -l .); \
	 UNFMT="$$UNFMT$$($(GOFUMPT) -l .)"; \
	 if [ -n "$$UNFMT" ]; then \
	   echo "Files need formatting:"; echo "$$UNFMT"; exit 1; \
	 fi

lint: tools
	@$(GOLANGCI_LINT) run

test:
	@go test -race -count=1 ./internal/... ./cmd/...

test-integration:
	@if [ -n "$(shell go list -tags=integration ./tests/integration/... 2>/dev/null)" ]; then \
		go test -race -tags=integration ./tests/integration/...; \
	else \
		echo "No integration test packages found under ./tests/integration/..."; \
	fi

test-all:
	@go test -race -tags=integration ./...

install:
	@go install -ldflags "$(LDFLAGS)" ./cmd/wpssh-mcp

install-cli:
	@go install -ldflags "$(CLI_LDFLAGS)" ./cmd/wpgo

clean:
	rm -rf bin/ dist/ .tools/

ci: fmt-check lint test
