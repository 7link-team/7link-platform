BUILD_PACKAGE := 7link.com/platform/cmd/7link-api/cmd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
OUTPUT ?= bin/7link-api
LDFLAGS := -X '$(BUILD_PACKAGE).buildVersion=$(VERSION)'
DEV_ARGS ?= serve --debug --migrate --pprof.enabled \
	--api.metrics.enabled --api.metrics.advertise \
	--api.migration-status.enabled --api.migration-status.advertise \
	--api.config-inspector.enabled --api.config-inspector.advertise
COVERAGE_MIN ?= 90
COVERAGE_PROFILE ?= coverage.out
COVERAGE_RAW_PROFILE ?= coverage.raw.out

.PHONY: build dev test test-coverage test-integration test-all

build:
	mkdir -p $(dir $(OUTPUT))
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUTPUT) ./cmd/7link-api

dev:
	go -C cmd/7link-api run . $(DEV_ARGS)

test:
	go test ./...

test-coverage:
	@packages="$$(go list ./... | grep -Ev '/proto/gen/go/|/node_modules/')"; \
	go test -count=1 -covermode=atomic -coverprofile="$(COVERAGE_RAW_PROFILE)" $$packages
	@awk 'NR == 1 || ($$1 !~ /\/cmd\/7link-api\/main.go:/ && $$1 !~ /\/cmd\/infra-api\/main.go:/)' \
		"$(COVERAGE_RAW_PROFILE)" > "$(COVERAGE_PROFILE)"
	@coverage="$$(go tool cover -func="$(COVERAGE_PROFILE)" | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}')"; \
	awk -v coverage="$$coverage" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
		printf "total coverage: %.1f%% (minimum %.1f%%)\n", coverage, minimum; \
		if (coverage + 0 < minimum + 0) exit 1; \
	}'

test-integration:
	go -C tests test ./...

test-all: test test-integration
