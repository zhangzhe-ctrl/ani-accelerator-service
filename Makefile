SHELL := /bin/bash
.SHELLFLAGS := -Eeuo pipefail -c

GO ?= go
PYTHON ?= python3
TOOLS_DIR := $(CURDIR)/.tools/bin
BUF := $(TOOLS_DIR)/buf
GOVULNCHECK := $(TOOLS_DIR)/govulncheck
CYCLONEDX_GOMOD := $(TOOLS_DIR)/cyclonedx-gomod
GITLEAKS := $(TOOLS_DIR)/gitleaks
SQLC := $(TOOLS_DIR)/sqlc
SQLC_VERSION := v1.30.0

BUF_VERSION := v1.60.0
GOVULNCHECK_VERSION := v1.7.0
CYCLONEDX_GOMOD_VERSION := v1.12.0
GITLEAKS_VERSION := v8.30.1
BUF_MODULE := github.com/bufbuild/buf@$(BUF_VERSION)
GOVULNCHECK_MODULE := golang.org/x/vuln@$(GOVULNCHECK_VERSION)
CYCLONEDX_GOMOD_MODULE := github.com/CycloneDX/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)
GITLEAKS_MODULE := github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

SERVICE_NAME ?= ani-accelerator-service
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Name=$(SERVICE_NAME) -X main.Version=$(VERSION)

DOCKER ?= docker
IMAGE ?=
PLATFORM ?= linux/amd64
TARGET ?= service
BUILDER_IMAGE ?= golang:1.26.10-bookworm
RUNTIME_IMAGE ?= gcr.io/distroless/static-debian12:nonroot
IMAGE_GOPROXY ?= https://goproxy.cn,https://proxy.golang.org,direct
IMAGE_OUTPUT_DIR ?= .work/images
SOURCE_REVISION ?=

.PHONY: tools check-buf supply-chain-tools check-govulncheck check-cyclonedx check-gitleaks config api design-test generate build test verify vuln secrets sbom supply-chain-verify audit clean help

tools: check-buf check-sqlc

$(SQLC):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

.PHONY: check-sqlc sqlc verify-sqlc
check-sqlc: $(SQLC)
	test "$$($(SQLC) version)" = "$(SQLC_VERSION)"

sqlc: check-sqlc
	$(SQLC) generate

verify-sqlc: check-sqlc
	bash scripts/verify-sqlc $(SQLC)

$(BUF):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

check-buf: $(BUF)
	test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	test "$$(go version -m $(BUF) | awk '$$1 == "mod" {print $$2 "@" $$3; exit}')" = "$(BUF_MODULE)"

supply-chain-tools: check-govulncheck check-cyclonedx check-gitleaks

$(GOVULNCHECK):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

check-govulncheck: $(GOVULNCHECK)
	$(GOVULNCHECK) -version | grep --fixed-strings --line-regexp "Scanner: govulncheck@$(GOVULNCHECK_VERSION)"
	test "$$(go version -m $(GOVULNCHECK) | awk '$$1 == "mod" {print $$2 "@" $$3; exit}')" = "$(GOVULNCHECK_MODULE)"

$(CYCLONEDX_GOMOD):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$(CYCLONEDX_GOMOD_VERSION)

check-cyclonedx: $(CYCLONEDX_GOMOD)
	test "$$($(CYCLONEDX_GOMOD) version | awk -F '\t' '$$1 == "Version:" {print $$2}')" = "$(CYCLONEDX_GOMOD_VERSION)"
	test "$$(go version -m $(CYCLONEDX_GOMOD) | awk '$$1 == "mod" {print $$2 "@" $$3; exit}')" = "$(CYCLONEDX_GOMOD_MODULE)"

$(GITLEAKS):
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

check-gitleaks: $(GITLEAKS)
	test "$$(go version -m $(GITLEAKS) | awk '$$1 == "mod" {print $$2 "@" $$3; exit}')" = "$(GITLEAKS_MODULE)"

config: $(BUF)
	$(BUF) lint
	$(BUF) build
	$(BUF) generate --template buf.gen.yaml

api: check-buf
	$(BUF) lint api/protos
	$(BUF) build api/protos
	$(BUF) generate --template buf.gen.api.yaml

design-test:
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m unittest discover -s docs/design/v1.2/tests -v

generate: config api
	$(GO) generate ./...
	find . -type f -name '*.go' -not -path './.git/*' -not -path './.tools/*' -print0 | xargs -0 --no-run-if-empty gofmt -w

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(SERVICE_NAME) ./cmd/...

.PHONY: image test-image
image:
	@$(PYTHON) scripts/build-image.py --image "$(IMAGE)" --version "$(VERSION)" \
		--platform "$(PLATFORM)" --target "$(TARGET)" --docker "$(DOCKER)" \
		--builder-image "$(BUILDER_IMAGE)" --runtime-image "$(RUNTIME_IMAGE)" \
		--goproxy "$(IMAGE_GOPROXY)" --source-revision "$(SOURCE_REVISION)" \
		--output-dir "$(IMAGE_OUTPUT_DIR)"

test-image:
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m unittest discover -s tests/image -v

test:
	$(GO) test -count=1 ./...

.PHONY: test-pg test-domain test-race test-contract test-provider
test-domain:
	$(GO) test -count=1 -v ./internal/biz

test-pg:
	test -n "$${ACC_TEST_DSN_FILE:-}"
	$(GO) test -count=1 -v ./internal/data -run '^TestPostgres'

test-contract:
	test -n "$${ACC_TEST_DSN_FILE:-}"
	$(GO) test -count=1 -v ./tests/contract ./cmd/ani-accelerator-service

test-provider:
	test -n "$${ACC_TEST_DSN_FILE:-}"
	$(GO) test -count=1 -v ./internal/data/provider

.PHONY: test-recovery
test-recovery:
	bash scripts/test-postgres-recovery

test-race:
	test -n "$${ACC_TEST_DSN_FILE:-}"
	$(GO) test -race -count=1 ./internal/biz ./internal/data/... ./internal/server ./internal/service ./tests/contract

verify: check-buf verify-sqlc
	test -n "$${ACC_TEST_DSN_FILE:-}"
	test -n "$${ACC_FORMAL_DSN_FILE:-}"
	./scripts/verify-source $(BUF)
	$(GO) mod tidy -diff
	$(GO) test -count=1 -v ./...
	$(GO) vet ./...
	$(GO) build -trimpath ./...
	$(GO) mod verify
	$(MAKE) design-test
	$(MAKE) test-race
	ACC_TEST_EVIDENCE_DIR="$${ACC_TEST_EVIDENCE_DIR:-$(CURDIR)/.tmp/tenant-mutations}" bash scripts/test-tenant-mutations
	$(MAKE) test-recovery
	$(MAKE) vuln secrets
	git diff --check

vuln: check-govulncheck
	$(GOVULNCHECK) -show version,verbose ./...

secrets: check-gitleaks
	$(GITLEAKS) git --no-banner --no-color --redact --log-opts="--all" .
	$(GITLEAKS) dir --no-banner --no-color --redact .

sbom: check-cyclonedx
	./scripts/generate-sbom $(CYCLONEDX_GOMOD)

supply-chain-verify: sbom
	./scripts/verify-supply-chain

audit: vuln secrets supply-chain-verify

clean:
	rm -rf bin .tools .work .tmp

help:
	@echo "make image IMAGE=<registry/project/name:tag> VERSION=<version> [PLATFORM=linux/amd64]"
	@echo "make test-image run the image orchestration tests (Docker boundary double)"
	@echo "make tools    install pinned config generator"
	@echo "make generate regenerate typed config and GPU API"
	@echo "make api      generate pinned GPU protobuf/gRPC bindings"
	@echo "make design-test run the imported v1.2 pure-model tests"
	@echo "make verify   run deterministic local quality gates"
	@echo "make vuln     scan the current dependency graph"
	@echo "make secrets  scan all Git history with pinned Gitleaks"
	@echo "make sbom     write a CycloneDX SBOM"
	@echo "make audit    run vulnerability, secret, SBOM, license, and notice gates"

.DEFAULT_GOAL := help
