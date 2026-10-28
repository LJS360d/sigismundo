.PHONY: all build run dev local test lint clean

VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR    := bin
MAIN_PACKAGE := ./cmd/sigismundo
BINARY_NAME  := sigismundo
ENV          ?= production

BUILDPATH    := $(BUILD_DIR)/$(BINARY_NAME)

LDFLAGS     := -ldflags "-w -s -X main.Version=$(VERSION)"
LDFLAGS_DEV := -ldflags "-X main.Version=$(VERSION)"

RUNTIME_FLAGS = GO_ENV=$(ENV)

# ── Targets ───────────────────────────────────────────────────────────────────

all: dev

$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)

$(BUILDPATH): $(BUILD_DIR) FORCE
	@echo "[build] compiling $(BINARY_NAME) $(VERSION) (env=$(ENV))"
	go build $(LDFLAGS) -o $(BUILDPATH) $(MAIN_PACKAGE)
	@echo "[build] done -> $(BUILDPATH)"

FORCE:

build: $(BUILDPATH)

run: $(BUILDPATH)
	@echo "[run] starting $(BINARY_NAME)"
	set -a; [ ! -f .env ] || . ./.env; set +a; $(RUNTIME_FLAGS) ./$(BUILDPATH)

dev: ENV=development
dev: LDFLAGS=$(LDFLAGS_DEV)
dev: run

# API without Discord (dev auth), for the Activity in a plain browser.
local: ENV=development
local: LDFLAGS=$(LDFLAGS_DEV)
local: $(BUILDPATH)
	set -a; [ ! -f .env ] || . ./.env; set +a; $(RUNTIME_FLAGS) ./$(BUILDPATH) local

test:
	go test ./...

lint:
	@echo "[lint] formatting"
	@go fmt ./...
	@echo "[lint] static analysis"
	@staticcheck ./...

clean:
	@echo "[clean] removing build artifacts"
	@go clean
	@rm -rf $(BUILD_DIR)
