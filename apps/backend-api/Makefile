BUILD_PACKAGE := 7link.com/platform/cmd/7link-api/cmd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
OUTPUT ?= bin/7link-api
LDFLAGS := -X '$(BUILD_PACKAGE).buildVersion=$(VERSION)'

.PHONY: build test test-integration test-all

build:
	mkdir -p $(dir $(OUTPUT))
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUTPUT) ./cmd/7link-api

test:
	go test ./...

test-integration:
	go -C tests test ./...

test-all: test test-integration
