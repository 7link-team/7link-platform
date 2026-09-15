BUILD_PACKAGE := 7link.com/platform/cmd/7link-api/cmd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
OUTPUT ?= bin/7link-api
LDFLAGS := -X '$(BUILD_PACKAGE).buildVersion=$(VERSION)'
DEV_IAM_ISSUER ?= http://127.0.0.1:8080
DEV_SECRET_DIR ?= $(abspath .temp/dev-secrets)
DEV_AUTHN_SIGNING_KEY_FILE ?= $(DEV_SECRET_DIR)/authn-signing-key
DEV_AUTHN_MFA_KEY_FILE ?= $(DEV_SECRET_DIR)/authn-mfa-key
DEV_FEDERATION_KEY_FILE ?= $(DEV_SECRET_DIR)/federation-key
DEV_ARGS ?= serve --debug --migrate --pprof.enabled --shutdown.graceful=false \
	--api.metrics.enabled --api.metrics.advertise \
	--table_prefix sl_ \
	--api.migration-status.enabled --api.migration-status.advertise \
	--api.config-inspector.enabled --api.config-inspector.advertise \
	--api.datasource-inspector.enabled --api.datasource-inspector.advertise \
	--authn.enabled --authn.issuer $(DEV_IAM_ISSUER) \
	--authn.signing-key-file $(DEV_AUTHN_SIGNING_KEY_FILE) \
	--authn.web.enabled --authn.web.post-login-url $(DEV_IAM_ISSUER)/ \
	--authn.web.allowed-return-urls $(DEV_IAM_ISSUER)/ \
	--authn.web.allowed-origins $(DEV_IAM_ISSUER) --authn.web.cookie-secure=false \
	--authn.mfa.encryption-key-file $(DEV_AUTHN_MFA_KEY_FILE) \
	--federation.enabled --federation.encryption-key-file $(DEV_FEDERATION_KEY_FILE)

# The published API document, and the settings that decide what is in it.
#
# The document is generated rather than written, and committed rather than
# served, because everything that needs it runs before the service does: the
# client type generator, a compatibility check, the review of an API change.
# Regenerate with `make openapi` and commit the result; `make openapi-check`
# fails when the committed document no longer matches the code.
#
# The name is a setting rather than a constant, because the deployment identity
# belongs to whoever deploys. The default is neutral so the committed document
# carries no product name; the document's title is this name plus " API".
#
# Debug-only surfaces (pprof, the inspectors, metrics) are deliberately left
# off: publishing them would describe a deployment nobody actually runs.
#
# The keys exist because the modules refuse to compose without them. They are
# generated fresh into .temp on every run, they never leave this machine, and
# none of them appears in the document -- the document describes shapes, not
# state, which is also why two runs produce the same bytes.
OPENAPI_OUTPUT ?= docs/openapi.json
OPENAPI_NAME ?= IAM
OPENAPI_ISSUER ?= https://iam.invalid
OPENAPI_WORK_DIR ?= $(abspath .temp/openapi)
OPENAPI_SECRET_DIR ?= $(OPENAPI_WORK_DIR)/secrets
OPENAPI_SIGNING_KEY_FILE := $(OPENAPI_SECRET_DIR)/signing-key
OPENAPI_MFA_KEY_FILE := $(OPENAPI_SECRET_DIR)/mfa-key
OPENAPI_FEDERATION_KEY_FILE := $(OPENAPI_SECRET_DIR)/federation-key
OPENAPI_KEY_FILES := $(OPENAPI_SIGNING_KEY_FILE) $(OPENAPI_MFA_KEY_FILE) $(OPENAPI_FEDERATION_KEY_FILE)
# A throwaway schema. The export composes the real application, and composition
# expects its tables to be there; nothing is read from this database and nothing
# survives the run.
OPENAPI_ARGS = openapi --name "$(OPENAPI_NAME)" --migrate \
	--database.main.type sqlite \
	--database.main.dsn "$(OPENAPI_WORK_DIR)/openapi.sqlite" \
	--authn.enabled --authn.issuer $(OPENAPI_ISSUER) \
	--authn.signing-key-file $(OPENAPI_SIGNING_KEY_FILE) \
	--authn.mfa.encryption-key-file $(OPENAPI_MFA_KEY_FILE) \
	--authn.web.enabled --authn.web.post-login-url $(OPENAPI_ISSUER)/ \
	--authn.web.allowed-return-urls $(OPENAPI_ISSUER)/ \
	--authn.web.allowed-origins $(OPENAPI_ISSUER) \
	--federation.enabled \
	--federation.encryption-key-file $(OPENAPI_FEDERATION_KEY_FILE)
COVERAGE_MIN ?= 90
COVERAGE_PROFILE ?= coverage.out
COVERAGE_RAW_PROFILE ?= coverage.raw.out
# The complete engine/prefix migration matrix alone takes about ten minutes on
# macOS. This is a package-wide budget; individual concurrency deadlines stay short.
INTEGRATION_TEST_TIMEOUT ?= 20m
-include .temp/iam-env.mk

IAM_SOURCE_ROOT ?=
DOCKER_CMD ?= docker
DOCKER_TEST_HOST ?= 127.0.0.1
MYSQL8_IMAGE ?= mysql:8.4@sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb
PROMTOOL ?= promtool
ifeq ($(OS),Windows_NT)
MKDIR_OUTPUT = powershell.exe -NoProfile -NonInteractive -Command "New-Item -ItemType Directory -Force -Path '$(dir $(OUTPUT))' | Out-Null"
else
MKDIR_OUTPUT = mkdir -p $(dir $(OUTPUT))
endif
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
	7link.com/platform/internal/iam/audit \
	7link.com/platform/internal/iam/authn \
	7link.com/platform/internal/iam/core \
	7link.com/platform/internal/iam/federation \
	7link.com/platform/internal/iam/governance \
	7link.com/platform/internal/iam/identity \
	7link.com/platform/internal/iam/oauth \
	7link.com/platform/internal/iam/organization \
	7link.com/platform/internal/iam/provisioning \
	7link.com/platform/internal/todo \
	7link.com/platform/pkg/extension/authn \
	7link.com/platform/pkg/extension/authz \
	7link.com/platform/pkg/extension/keyringx \
	7link.com/platform/pkg/extension/passwordx \
	7link.com/platform/pkg/extension/policyx \
	7link.com/platform/pkg/extension/secretx \
	7link.com/platform/pkg/extension/webauthnx \
	7link.com/platform/pkg/extension/huma/respx \
	7link.com/platform/pkg/i18n \
	7link.com/platform/pkg/query

.PHONY: build ci dev fmt-check generate generate-docs golangci iam-preflight lint openapi openapi-check test test-coverage test-integration test-performance test-load test-all test-mysql8 test-mysql8-run
.PHONY: release-check test-race generate-check frontend-check test-alerts test-redis-security
# These gates share coverage files and embedded database resources. Even when
# invoked with -j, execute each prerequisite to completion before starting the next.
.NOTPARALLEL: ci release-check

build:
	$(MKDIR_OUTPUT)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUTPUT) ./cmd/7link-api

# Regenerate everything derived from a declared source: protobuf from .proto,
# and each module's typed query code from its own model. `go generate ./...`
# reaches the per-module directives; a module is added by placing a directive in
# it, never by editing a list here.
generate:
	go tool -modfile=tools/go.mod buf generate
	go generate ./internal/...

# The docs site is generated by npm, needs network, and rewrites its own pinned
# dependencies, so it is kept out of `generate` and run deliberately.
generate-docs:
	go generate ./pkg/extension/huma/docs/...

# Resolve and verify the machine-local source checkout and Docker transport.
# The generated .temp/iam-env.mk contains paths only; it never stores credentials.
iam-preflight:
	go -C tools run ./iam-preflight --target .. --fix --bootstrap-docker

# Re-enter make after preflight so the generated machine-local transport is
# loaded even when .temp/iam-env.mk did not exist when this process started.
test-mysql8: iam-preflight
	@"$(MAKE)" --no-print-directory test-mysql8-run

test-mysql8-run:
	bash scripts/test-mysql8.sh "$(DOCKER_TEST_HOST)" "$(MYSQL8_IMAGE)" $(DOCKER_CMD)
	go -C tools run ./iam-schema-lock --target ..

# Single entry point for a pipeline: every gate, in cheapest-first order, so a
# formatting or lint failure reports before the slow database tests run. Fails
# on the first gate that fails.
ci: fmt-check lint test-coverage test-integration build openapi-check

# The production candidate adds the slow gates deliberately kept outside ci.
# Configure DOCKER_CMD / DOCKER_TEST_HOST for an already provisioned test daemon;
# release verification must not bootstrap or change a remote Docker installation.
# Environment deployment, browser UAT and service-owner sign-off remain separate.
test-redis-security:
	bash scripts/test-redis-security.sh
	IAM_TEST_REDIS_TLS=1 bash scripts/test-redis-security.sh

release-check: ci test-race generate-check frontend-check test-alerts test-mysql8-run test-redis-security

test-alerts:
	cd docs && $(PROMTOOL) test rules iam-alerts.test.yml

test-race:
	go test -race ./... -count=1

generate-check:
	@before="$$(mktemp)"; after="$$(mktemp)"; \
	trap 'rm -f "$$before" "$$after"' EXIT; \
	git ls-files --full-name | git hash-object --stdin-paths > "$$before" && \
	$(MAKE) --no-print-directory generate && \
	git ls-files --full-name | git hash-object --stdin-paths > "$$after" && \
	cmp "$$before" "$$after" || { echo "generate changed tracked content; review and regenerate the candidate"; exit 1; }

frontend-check:
	cd ../frontend-web-admin && bun run lint --force
	cd ../frontend-web-admin && bun run typecheck --force
	cd ../frontend-web-admin && bun run test --force
	cd ../frontend-web-admin && bun run build --force

# gofmt has no check mode; -l prints files that need formatting and exits 0, so
# the emptiness of that list is the assertion.
fmt-check:
	@unformatted="$$(gofmt -l ./cmd ./internal ./pkg)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt required for:"; echo "$$unformatted"; exit 1; \
	fi; \
	echo "gofmt: clean"

$(DEV_AUTHN_SIGNING_KEY_FILE) $(DEV_AUTHN_MFA_KEY_FILE) $(DEV_FEDERATION_KEY_FILE):
	@umask 077; mkdir -p "$(DEV_SECRET_DIR)"; openssl rand -base64 32 > "$@"

dev: $(DEV_AUTHN_SIGNING_KEY_FILE) $(DEV_AUTHN_MFA_KEY_FILE) $(DEV_FEDERATION_KEY_FILE)
	go -C cmd/7link-api run . $(DEV_ARGS)

# Regenerate the published API document. Commit the result: the frontend
# type generator reads the committed file, so a change to the API is only
# visible to a client once this has run.
openapi: $(OPENAPI_KEY_FILES)
	go -C cmd/7link-api run . $(OPENAPI_ARGS) --out "$(abspath $(OPENAPI_OUTPUT))"

# Fail when the committed document no longer describes the code. Without
# this, an API change lands green and the mismatch is found later by whoever
# generated a client from the stale document -- a long way from the change
# that caused it. The fix is always the same, so the failure says so.
openapi-check: $(OPENAPI_KEY_FILES)
	@go -C cmd/7link-api run . $(OPENAPI_ARGS) \
		--out "$(OPENAPI_WORK_DIR)/openapi.expected.json" >/dev/null
	@if ! diff -q "$(OPENAPI_OUTPUT)" "$(OPENAPI_WORK_DIR)/openapi.expected.json" >/dev/null 2>&1; then \
		echo "$(OPENAPI_OUTPUT) does not match the code; run 'make openapi' and commit the result"; \
		diff -u "$(OPENAPI_OUTPUT)" "$(OPENAPI_WORK_DIR)/openapi.expected.json" | head -40; \
		exit 1; \
	fi; \
	echo "openapi: matches $(OPENAPI_OUTPUT)"

$(OPENAPI_KEY_FILES):
	@umask 077; mkdir -p "$(OPENAPI_SECRET_DIR)"; openssl rand -base64 32 > "$@"

# Config lives in .golangci.yml. The linter is pinned in tools/go.mod so every
# developer resolves the same findings without a local install.
lint:
	go tool -modfile=tools/go.mod golangci-lint run ./...

golangci: lint

test:
	go test ./...

# gorm.io/gen writes one file per table as <table>.gen.go and the package entry
# point as query/gen.go. Only the first spelling matches the .gen.go pattern, so
# the entry point -- transaction and session boilerplate that nothing here should
# hand-test -- was counted as uncovered project code. It is excluded by its full
# path rather than by name, because pkg/extension/gorm/gen is hand-written build
# tooling that happens to share it.
test-coverage:
	@packages="$$(go list ./... | grep -Ev '/proto/gen/go/|/node_modules/|/dbgen$$')"; \
	go test -count=1 -covermode=atomic -coverprofile="$(COVERAGE_RAW_PROFILE)" $$packages
	@awk 'NR == 1 || ($$1 !~ /\/cmd\/7link-api\/main.go:/ && $$1 !~ /\/cmd\/infra-api\/main.go:/ && $$1 !~ /\.gen\.go:/ && $$1 !~ /\/query\/gen\.go:/)' \
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
	go -C tests test ./... -count=1 -timeout="$(INTEGRATION_TEST_TIMEOUT)"

test-performance:
	go -C tests test ./performance -count=1 -v

# The HTTP load harness against a real MySQL 8.4. Deliberately outside ci and
# release-check: it measures throughput and latency rather than asserting a
# pass, and what it reports depends on the machine it ran on. Tune with
# IAM_LOAD_CONCURRENCY and IAM_LOAD_REQUESTS.
test-load:
	bash scripts/load-mysql8.sh "$(DOCKER_TEST_HOST)" "$(MYSQL8_IMAGE)" $(DOCKER_CMD)

test-all: test test-integration
