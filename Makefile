# gfl — Makefile
#
# Version resolution order:
#   1. $VERSION env var
#   2. `git describe --tags --dirty --always` (what tagged releases use)
#   3. "dev"
#
# Release workflow:
#   1. make test
#   2. make tag VERSION=v0.1.0         # creates annotated tag, pushes
#   3. GitHub Actions picks up the tag, builds cross-platform binaries,
#      and attaches them to the release.

SHELL := /bin/sh

BINARY      := gfl
PKG         := github.com/swill/gfl
CMD_PATH    := .
DIST_DIR    := dist
BIN_DIR     := bin

GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
GIT_VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")
VERSION     ?= $(GIT_VERSION)
BUILD_DATE  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -s -w \
	-X '$(PKG)/cmd.version=$(VERSION)' \
	-X '$(PKG)/cmd.commit=$(GIT_COMMIT)' \
	-X '$(PKG)/cmd.buildDate=$(BUILD_DATE)'

# Target platforms for release builds.
PLATFORMS := \
	linux/amd64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: all
all: fmt vet test build

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD_PATH)

.PHONY: install
install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)

.PHONY: test
test:
	go test -race -count=1 ./...

.PHONY: test-cover
test-cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: test-cover
	go tool cover -html=coverage.out -o coverage.html

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: version
version:
	@echo "$(VERSION)"

# Create and push an annotated tag; CI builds release artifacts.
.PHONY: tag
tag:
ifndef VERSION
	$(error VERSION is required, e.g. `make tag VERSION=v0.1.0`)
endif
	@case "$(VERSION)" in v*.*.*) ;; *) echo "VERSION must be vX.Y.Z"; exit 1;; esac
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)

# Cross-platform build of release artifacts into $(DIST_DIR)/.
.PHONY: release-build
release-build:
	@mkdir -p $(DIST_DIR)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out=$(DIST_DIR)/$(BINARY)-$$os-$$arch$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build \
			-ldflags "$(LDFLAGS)" -o $$out $(CMD_PATH) || exit 1; \
	done

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) coverage.out coverage.html

.PHONY: help
help:
	@echo "gfl — make targets"
	@echo "  build          compile local binary into ./bin/$(BINARY)"
	@echo "  test           run unit tests (-race)"
	@echo "  test-cover     run tests and print total coverage"
	@echo "  cover-html     generate coverage.html"
	@echo "  fmt / vet      go fmt / go vet"
	@echo "  tidy           go mod tidy"
	@echo "  version        print resolved version string"
	@echo "  tag VERSION=vX.Y.Z   create and push an annotated release tag"
	@echo "  release-build  cross-compile artifacts into ./dist/ (used by CI)"
	@echo "  clean          remove build outputs"
