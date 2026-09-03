SHELL := /bin/sh

GO ?= go
MODULES := ojs-chi ojs-gin ojs-echo ojs-fiber ojs-gorm ojs-serverless
EXAMPLE_MODULES := \
	ojs-chi/examples \
	ojs-gin/examples \
	ojs-echo/examples \
	ojs-fiber/examples \
	ojs-gorm/examples
ALL_MODULES := $(MODULES) $(EXAMPLE_MODULES)

TOOLS_BIN := $(CURDIR)/.tools/bin
STATICCHECK_VERSION := v0.6.1
GOLANGCI_LINT_VERSION := v2.7.0
STATICCHECK := $(TOOLS_BIN)/staticcheck
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint

.PHONY: test test-examples test-all build-all vet staticcheck golangci-lint \
	lint fmt-check tidy tidy-check tools clean-tools

test:
	@set -e; for module in $(MODULES); do \
		echo "==> Testing $$module"; \
		(cd "$$module" && GOWORK=off GOFLAGS=-mod=readonly $(GO) test ./... -race -count=1) || exit 1; \
	done

test-examples:
	@set -e; for module in $(EXAMPLE_MODULES); do \
		echo "==> Testing $$module"; \
		(cd "$$module" && GOWORK=off GOFLAGS=-mod=readonly $(GO) test ./... -race -count=1) || exit 1; \
	done

test-all: test test-examples

build-all:
	@set -e; for module in $(ALL_MODULES); do \
		echo "==> Building $$module"; \
		(cd "$$module" && GOWORK=off GOFLAGS=-mod=readonly $(GO) build ./...) || exit 1; \
	done

vet:
	@set -e; for module in $(ALL_MODULES); do \
		echo "==> Vetting $$module"; \
		(cd "$$module" && GOWORK=off GOFLAGS=-mod=readonly $(GO) vet ./...) || exit 1; \
	done

staticcheck: $(STATICCHECK)
	@set -e; for module in $(ALL_MODULES); do \
		echo "==> Staticchecking $$module"; \
		(cd "$$module" && GOWORK=off GOFLAGS=-mod=readonly "$(STATICCHECK)" ./...) || exit 1; \
	done

golangci-lint: $(GOLANGCI_LINT)
	@set -e; for module in $(ALL_MODULES); do \
		echo "==> golangci-lint $$module"; \
		(cd "$$module" && GOWORK=off GOFLAGS=-mod=readonly "$(GOLANGCI_LINT)" run --config "$(CURDIR)/.golangci.yml" ./...) || exit 1; \
	done

fmt-check:
	@files="$$(find $(MODULES) -name '*.go' -type f | sort)"; \
	unformatted="$$(gofmt -l $$files)"; \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt-formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint: fmt-check vet staticcheck golangci-lint

tidy:
	@set -e; for module in $(ALL_MODULES); do \
		echo "==> Tidying $$module"; \
		(cd "$$module" && GOWORK=off $(GO) mod tidy) || exit 1; \
	done

tidy-check:
	@set -e; for module in $(ALL_MODULES); do \
		echo "==> Checking $$module module files"; \
		(cd "$$module" && GOWORK=off $(GO) mod tidy -diff) || exit 1; \
	done

tools: $(STATICCHECK) $(GOLANGCI_LINT)

$(STATICCHECK):
	@mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

$(GOLANGCI_LINT):
	@mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

clean-tools:
	rm -rf "$(CURDIR)/.tools"
