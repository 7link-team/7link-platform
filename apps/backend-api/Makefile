BUILD_PACKAGE := 7link.com/platform/cmd/7link-api/cmd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
OUTPUT ?= bin/7link-api
LDFLAGS := -X '$(BUILD_PACKAGE).buildVersion=$(VERSION)'
DEV_ARGS ?= serve --debug --migrate --pprof.enabled --shutdown.graceful=false \
	--api.metrics.enabled --api.metrics.advertise \
	--api.migration-status.enabled --api.migration-status.advertise \
	--api.config-inspector.enabled --api.config-inspector.advertise \
	--api.datasource-inspector.enabled --api.datasource-inspector.advertise
COVERAGE_MIN ?= 90
COVERAGE_PROFILE ?= coverage.out
COVERAGE_RAW_PROFILE ?= coverage.raw.out
MODULE_COVERAGE_PACKAGES := \
	7link.com/platform/internal/infra/audit \
	7link.com/platform/internal/infra/configinspector \
	7link.com/platform/internal/infra/databasemonitor \
	7link.com/platform/internal/infra/dlock \
	7link.com/platform/internal/infra/guid \
	7link.com/platform/internal/infra/health \
	7link.com/platform/internal/infra/metrics \
	7link.com/platform/internal/infra/migrationstatus \
	7link.com/platform/internal/infra/pprof \
	7link.com/platform/internal/infra/telemetry \
	7link.com/platform/internal/infra/wuid \
	7link.com/platform/pkg/extension/huma/respx \
	7link.com/platform/pkg/i18n

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
	@status=0; \
	for package in $(MODULE_COVERAGE_PACKAGES); do \
		coverage="$$(awk -v target="$$package" 'NR > 1 { \
			file = $$1; sub(/:.*/, "", file); directory = file; sub(/\/[^\/]+$$/, "", directory); \
			if (directory == target) { total += $$2; if ($$3 > 0) covered += $$2 } \
		} END { if (total == 0) print "missing"; else printf "%.1f", covered * 100 / total }' "$(COVERAGE_RAW_PROFILE)")"; \
		short="$${package#7link.com/platform/}"; \
		if [ "$$coverage" = "missing" ]; then \
			printf "module coverage: %-48s MISSING\n" "$$short"; status=1; continue; \
		fi; \
		if ! awk -v name="$$short" -v coverage="$$coverage" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
			printf "module coverage: %-48s %.1f%% (minimum %.1f%%)\n", name, coverage, minimum; \
			if (coverage + 0 < minimum + 0) exit 1; \
		}'; then status=1; fi; \
	done; \
	exit $$status

test-integration:
	go -C tests test ./...

test-all: test test-integration
