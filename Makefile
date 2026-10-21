BINARY_NAME=ottermq
ADMIN_BINARY_NAME=ottermqadmin
BUILD_DIR=bin
MAIN_PATH=cmd/ottermq/main.go
ADMIN_MAIN_PATH=cmd/ottermqadmin/main.go

PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

build:
	@mkdir -p $(BUILD_DIR)
	@VERSION=$$(git describe --tags --always 2>/dev/null || echo dev); \
	go build -ldflags "-X main.VERSION=$$VERSION" -o ./$(BUILD_DIR)/$(BINARY_NAME) ./$(MAIN_PATH)

build-admin:
	@mkdir -p $(BUILD_DIR)
	@go build -o ./$(BUILD_DIR)/$(ADMIN_BINARY_NAME) ./$(ADMIN_MAIN_PATH)

docs:
	@GOPATH=$$(go env GOPATH); \
	$$GOPATH/bin/swag init -g ../../../$(MAIN_PATH) --pd -d web/handlers/api,web/handlers/api_admin -exclude web/handlers/webui/ -o ./web/docs --ot go

install: build
	@mkdir -p $(BINDIR)
	@install -m 0755 ./$(BUILD_DIR)/$(BINARY_NAME) $(BINDIR)/$(BINARY_NAME)

install-admin: build-admin
	@mkdir -p $(BINDIR)
	@install -m 0755 ./$(BUILD_DIR)/$(ADMIN_BINARY_NAME) $(BINDIR)/$(ADMIN_BINARY_NAME)

run: build
	@./$(BUILD_DIR)/$(BINARY_NAME)

test:
	@go test ./... -v

# Same packages and flags as the CI race jobs (.github/workflows/go.yml).
test-race:
	@go test -race ./internal/... ./pkg/... ./web/... ./config/... ./cmd/...
	@go test -race ./tests/e2e/

test-cli:
	@go test ./cmd/ottermqadmin ./internal/cli ./pkg/adminapi/client -v

lint:
	@golangci-lint run

UI_DIR=web/ui

ui-deps:
	@cd $(UI_DIR) && npm ci

ui-build: ui-deps
	@cd $(UI_DIR) && npm run build
	@rm -rf ui
	@cp -r $(UI_DIR)/build ui

build-all: ui-build build build-admin

run-dev: build
	@echo "Run the UI dev server separately: cd $(UI_DIR) && npm run dev (http://localhost:5173)"
	@echo "Starting broker..."
	@./$(BUILD_DIR)/$(BINARY_NAME)

clean:
	@rm -f $(BUILD_DIR)/$(BINARY_NAME)
	@rm -f $(BUILD_DIR)/$(ADMIN_BINARY_NAME)
	@rm -rf ./ui
	@rm -rf ./$(UI_DIR)/build

.PHONY: build build-admin install install-admin clean run docs test test-race test-cli lint ui-deps ui-build build-all run-dev
