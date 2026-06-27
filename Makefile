APP := agent-desk
MAIN := ./cmd/server
WEB_DIR := web
SPA_INDEX := $(WEB_DIR)/out/index.html

GO ?= go
PNPM ?= pnpm
GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)
DEV_SERVER_URL ?= http://127.0.0.1:8083

.DEFAULT_GOAL := help

.PHONY: all help build build-go build-linux release run run-go dev test check clean clean-web \
	web-install web-dev web-build-spa ensure-spa build-spa web-build-ssr web-typecheck web-lint \
	generator enums migration testdata

all: build

help:
	@echo "Available targets:"
	@echo "  make build                Build web SPA and Go binary"
	@echo "  make build-go             Build Go binary only, ensuring SPA exists"
	@echo "  make build-linux          Build linux amd64 binary"
	@echo "  make release              Build release binaries for common platforms"
	@echo "  make run                  Build web SPA then run server"
	@echo "  make run-go               Run server only, ensuring SPA exists"
	@echo "  make dev                  Run Go server with dev tag and web dev server"
	@echo "  make test                 Run Go tests, ensuring SPA exists"
	@echo "  make check                Run Go tests, web typecheck, and web lint"
	@echo "  make clean                Remove Go binaries"
	@echo "  make clean-web            Remove web build output"
	@echo "  make web-install          Install web dependencies"
	@echo "  make web-dev              Run web dev server"
	@echo "  make web-build-spa        Build static web SPA"
	@echo "  make web-typecheck        Run web typecheck"
	@echo "  make web-lint             Run web lint"
	@echo "  make generator            Run code generator"
	@echo "  make enums                Generate frontend enums"
	@echo "  make migration            Run migration command"
	@echo "  make testdata             Run testdata generator"

build: web-build-spa
	@$(MAKE) build-go

build-go: ensure-spa
	@echo "Building $(APP)..."
	@$(GO) build -v -o $(APP) $(MAIN)

build-linux: web-build-spa
	@echo "Building $(APP) for linux/amd64..."
	@GOOS=linux GOARCH=amd64 $(GO) build -v -o $(APP)-linux-amd64 $(MAIN)

release: web-build-spa
	@echo "Building release binaries..."
	@GOOS=linux GOARCH=amd64 $(GO) build -v -o $(APP)-linux-amd64 $(MAIN)
	@GOOS=linux GOARCH=arm64 $(GO) build -v -o $(APP)-linux-arm64 $(MAIN)
	@GOOS=darwin GOARCH=amd64 $(GO) build -v -o $(APP)-darwin-amd64 $(MAIN)
	@GOOS=darwin GOARCH=arm64 $(GO) build -v -o $(APP)-darwin-arm64 $(MAIN)
	@GOOS=windows GOARCH=amd64 $(GO) build -v -o $(APP)-windows-amd64.exe $(MAIN)

run: web-build-spa
	@$(GO) run $(MAIN)

run-go: ensure-spa
	@$(GO) run $(MAIN)

dev:
	@$(GO) run -tags dev $(MAIN) & \
	server_pid=$$!; \
	trap 'kill $$server_pid 2>/dev/null || true' EXIT INT TERM; \
	echo "Waiting for server at $(DEV_SERVER_URL)..."; \
	until curl -fsS "$(DEV_SERVER_URL)" >/dev/null 2>&1; do \
		if ! kill -0 $$server_pid 2>/dev/null; then \
			wait $$server_pid; \
			exit $$?; \
		fi; \
		sleep 1; \
	done; \
	echo "Server is ready; starting web dev server..."; \
	$(MAKE) web-dev

test: ensure-spa
	@$(GO) test ./...

check: test web-typecheck web-lint

clean:
	@rm -f $(APP) $(APP)-linux-amd64 $(APP)-linux-arm64 $(APP)-darwin-amd64 $(APP)-darwin-arm64 $(APP)-windows-amd64.exe

clean-web:
	@rm -rf $(WEB_DIR)/out

web-install:
	@cd $(WEB_DIR) && $(PNPM) install --frozen-lockfile

web-dev:
	@cd $(WEB_DIR) && $(PNPM) dev

web-build-spa:
	@cd $(WEB_DIR) && $(PNPM) build:sdk && $(PNPM) build

ensure-spa:
	@if [ ! -f "$(SPA_INDEX)" ]; then \
		echo "SPA build missing; running web-build-spa..."; \
		$(MAKE) web-build-spa; \
	fi

build-spa: web-build-spa

web-build-ssr:
	@cd $(WEB_DIR) && $(PNPM) build

web-typecheck:
	@cd $(WEB_DIR) && $(PNPM) typecheck

web-lint:
	@cd $(WEB_DIR) && $(PNPM) lint

generator:
	@$(GO) run ./cmd/generator/generator.go

enums:
	@$(GO) run ./cmd/enums/generator.go

migration:
	@$(GO) run ./cmd/migration

testdata:
	@$(GO) run ./cmd/testdata
