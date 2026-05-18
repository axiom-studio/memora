# Memora Core — build & test toolchain.
# All Go builds default to CGo-free (CLI spec §7.3) so binaries cross-compile cleanly.

GO              ?= go
GOFLAGS         ?=
LDFLAGS         ?= -s -w
CGO_ENABLED     ?= 0
BIN_DIR         := bin
COVER_PROFILE   := coverage.out

VERSION         ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT          := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_LDFLAGS := -X 'main.version=$(VERSION)' \
                   -X 'main.commit=$(COMMIT)' \
                   -X 'main.buildDate=$(BUILD_DATE)'

.PHONY: help
help:
	@echo "Memora Core build targets:"
	@echo "  build        Build memora-core and memora-cli into ./bin/"
	@echo "  memora-oss   Alias for 'build' — used by the OSS CI pipeline"
	@echo "  test         Run unit tests with race detector + coverage"
	@echo "  cover        Generate HTML coverage report (coverage.html)"
	@echo "  lint         Run golangci-lint over the module"
	@echo "  vet          Run 'go vet ./...'"
	@echo "  tidy         Run 'go mod tidy'"
	@echo "  clean        Remove built binaries + coverage artifacts"

.PHONY: build memora-oss
build: $(BIN_DIR)/memora-core $(BIN_DIR)/memora-cli
memora-oss: build

$(BIN_DIR)/memora-core: $(shell find cmd/memora-core internal pkg -type f -name '*.go' 2>/dev/null)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) \
		-ldflags "$(LDFLAGS) $(VERSION_LDFLAGS)" \
		-o $@ ./cmd/memora-core

$(BIN_DIR)/memora-cli: $(shell find cmd/memora-cli internal pkg -type f -name '*.go' 2>/dev/null)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) \
		-ldflags "$(LDFLAGS) $(VERSION_LDFLAGS)" \
		-o $@ ./cmd/memora-cli

.PHONY: test
test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test ./... -race -cover -coverprofile=$(COVER_PROFILE)

.PHONY: cover
cover: test
	$(GO) tool cover -html=$(COVER_PROFILE) -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed — see https://golangci-lint.run/usage/install/"; \
		exit 1; \
	}
	golangci-lint run

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) $(COVER_PROFILE) coverage.html dist/
