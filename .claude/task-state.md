# Task State

## Task

Latest-four-commit review optimization and Runtime lifecycle standardization.

## Objective

Leave the reviewed optimization uncommitted for manual acceptance. Every application Module receives and retains the App's single `*appenv.Runtime`; telemetry creates and publishes tracing state during lifecycle initialization rather than construction.

## Current Phase

ACCEPTANCE

## Overall Progress

- [x] Review latest four commits and agree the optimization scope.
- [x] Introduce `pkg/appenv.Runtime`, immutable application identity, and centralized infrastructure ownership.
- [x] Complete the previously agreed audit, registry, configuration, and coverage optimizations.
- [x] Standardize the remaining Module constructors and fields on Runtime.
- [x] Move telemetry provider/global initialization into lifecycle methods.
- [x] Update affected tests, call sites, and final lifecycle design docs.
- [x] Run full verification and final acceptance review.
- [x] Raise every application Module root package to at least 90% coverage.
- [x] Enforce both aggregate and per-Module coverage in the formal Makefile gate.
- [x] Remove production-only test constructor seams from dlock, wuid, and databasemonitor.

## Important Decisions

- App owns exactly one `*appenv.Runtime`; Modules retain the same pointer in a named private `runtime` field.
- Runtime contains App-level infrastructure managers and immutable App identity, not business/module services.
- `NewModule` stores dependencies/configuration and may build side-effect-free in-memory state; resource acquisition and global mutation belong to lifecycle methods.
- Keep the package name `audit`; keep generic deprecated-field support; do not restore unreleased legacy shutdown aliases.
- Only telemetry constructor behavior is being reorganized in this phase; unrelated constructor refactors are out of scope.

## Confirmed Constraints

- Do not create a Git commit.
- Do not add optional improvements without user confirmation.
- Preserve all existing worktree changes.
- Target formal project coverage remains at least 90%.
- Execution mode is Direct because the shared lifecycle contract is tightly coupled.

## Verification

Prior checkpoint before this phase: `go test ./...`, race tests, vet, external tests, coverage, and diff checks passed; formal total coverage was 90.1%.

Final verification:

- `go test -count=1 ./...`: PASS
- `go test -count=1 -race ./...`: PASS
- `go vet ./...`: PASS
- `go -C tests test -count=1 ./...`: PASS
- `make test-coverage`: PASS, total 92.1% (minimum 90.0%); all 13 application Module root packages are at least 90.0%
- `git diff --check`: PASS

Per-Module results: audit 91.8%, configinspector 92.3%, databasemonitor 94.1%,
dlock 94.8%, guid 93.2%, health 96.0%, metrics 98.3%, migrationstatus 92.6%,
pprof 90.5%, telemetry 92.5%, wuid 90.5%, respx 95.5%, i18n 98.5%.

## Next Action

User performs manual acceptance of the intentionally uncommitted worktree. No further implementation is authorized unless acceptance finds an issue or the user confirms additional scope.
