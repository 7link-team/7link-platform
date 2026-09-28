# Repository Guidelines

## Project Structure & Module Organization

This directory is the Go backend service. `cmd/7link-api/` contains the CLI and server entrypoint; `internal/app/` is the composition root for configuration, module registration, migrations, HTTP, and gRPC. Business and infrastructure modules live under `internal/` (use `internal/todo/` as the reference implementation). Shared, importable framework code belongs in `pkg/`. Integration tests use the separate Go module in `tests/`; pinned generators and linters use `tools/`. Architecture notes and implementation plans are under `docs/`.

Modules own their lifecycle, models, generated queries, localization, and migrations. A database-owning module keeps `migrate.go` and `sql/{postgres,mysql,sqlite}/` beside its code; do not create a centralized schema package. Construct multiple modules only in `internal/app/`.

## Build, Test, and Development Commands

- `make dev`: run API and Web with migrations, selectable demo accounts and local SQLite.
- `make dev-api`: run only the configurable API with debug mode and diagnostics.
- `make dev-status` / `make dev-stop`: inspect or stop the local development environment.
- `make build`: produce the version-stamped `bin/7link-api` binary.
- `make generate`: regenerate protobuf and module-owned GORM query code.
- `make test`: run main-module unit tests.
- `make test-integration`: run the separate integration-test module.
- `make test-coverage`: enforce overall and registered-module coverage.
- `make lint`: run the pinned `golangci-lint` configuration.
- `make ci`: run formatting, lint, the migration lock check, coverage, integration tests, build, the API document check and the web client contract check in gate order.

## Coding Style & Naming Conventions

Format Go with `gofmt`; use tabs as produced by the formatter. Package names are short, lowercase nouns. Exported identifiers require useful GoDoc. Follow existing optional `pkg/module` capability interfaces and `ProvideServices`/`InjectServices`; avoid parallel dependency-injection or port hierarchies. Generated files must be changed through their source and `make generate`.

## Testing Guidelines

Use Go's `testing` package with `testify` where established. Name files `*_test.go` and tests `TestBehavior`. Keep unit tests beside code and database-engine coverage in `tests/`. `make test-coverage` requires at least 90% overall and 90% for every package listed in `MODULE_COVERAGE_PACKAGES`.

## Commit & Pull Request Guidelines

History follows Conventional Commits with scope, for example `fix(backend-api): harden migration database handling`. Keep commits focused. Pull requests should explain behavior and architecture impact, list verification commands, link the issue or plan, and call out migration/configuration changes. Include screenshots only for user-visible documentation or UI changes. Read `../../CLAUDE.md` before substantial work and preserve unrelated working-tree changes.
