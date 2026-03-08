# Makefile – tmuxai project

# Variables
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT  ?= $(shell git rev-parse --short HEAD)
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LD_FLAGS = -s -w \
    -X github.com/anhhung04/tmuxai/internal.Version=$(VERSION) \
    -X github.com/anhhung04/tmuxai/internal.Commit=$(COMMIT) \
    -X github.com/anhhung04/tmuxai/internal.Date=$(DATE)

# Default target
.PHONY: all
all: build

# Development build (no trimming)
.PHONY: dev
dev:
	go build ./...

# Optimized build for release
.PHONY: build
build:
	go build -trimpath -ldflags "$(LD_FLAGS)" -o tmuxai .

# Release binary (cross‑compile for common platforms)
.PHONY: release
release:
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64   go build -trimpath -ldflags "$(LD_FLAGS)" -o dist/tmuxai_linux_amd64 .
	GOOS=darwin  GOARCH=amd64   go build -trimpath -ldflags "$(LD_FLAGS)" -o dist/tmuxai_darwin_amd64 .
	GOOS=darwin  GOARCH=arm64   go build -trimpath -ldflags "$(LD_FLAGS)" -o dist/tmuxai_darwin_arm64 .
	GOOS=windows GOARCH=amd64   go build -trimpath -ldflags "$(LD_FLAGS)" -o dist/tmuxai_windows_amd64.exe .

# Run tests
.PHONY: test
test:
	go test ./...

# Clean generated files
.PHONY: clean
clean:
	rm -rf dist tmuxai

# Install binary to $HOME/.local/bin (or $GOPATH/bin)
.PHONY: install
install: build
	install -Dm755 tmuxai $(HOME)/.local/bin/tmuxai

# Run the program (development mode)
.PHONY: run
run: dev
	./tmuxai
