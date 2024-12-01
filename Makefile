VERSION := $(shell git describe --tags --always)
BINARY_NAME=ottermq
BUILD_DIR=bin


build:
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags "-X got_it/cmd.version=$(VERSION)" -o ./$(BUILD_DIR)/$(BINARY_NAME) ./cmd/ottermq/main.go

install:
	@mkdir -p $(shell go env GOPATH)/bin
	@mv ./$(BUILD_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/$(BINARY_NAME)

run: build
	@./$(BUILD_DIR)/$(BINARY_NAME)

clean:
	@rm -f $(BUILD_DIR)/$(BINARY_NAME)

.PHONY: build install clean run