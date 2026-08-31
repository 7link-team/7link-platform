# Task State

## Task

Unify service and standalone database migration with the Module lifecycle.

## Objective

Remove every standalone migration bootstrap path, including the reduced
AppConfig, manual datasource router, empty Identity, Runtime construction, and
custom cleanup. Both `serve` and `migrate` must use the same App configuration,
Runtime preparation, Module Registry, and lifecycle prefix; only migration action
and whether execution continues into Initialize/Start may differ.

## Current Phase

ACCEPTANCE

## Overall Progress

- [x] Confirm the lifecycle design with the user.
- [x] Inspect the service bootstrap, standalone migration, Registry, CLI, and shutdown paths.
- [x] Extract and reuse the single Module composition and preparation path.
- [x] Tighten the migration capability contract to apply only to Modules.
- [x] Add regression coverage for unified migration discovery and no-start behavior.
- [x] Identify the remaining custom Runtime/App construction during manual review.
- [x] Make migration configuration reuse the complete AppConfig with flat `squash` semantics.
- [x] Extract the common App configuration and prepare entry points.
- [x] Remove manual migration Router/Runtime/App construction and cleanup.
- [x] Remove module-level migration command APIs that bypass the App lifecycle.
- [x] Scan production construction and lifecycle paths for equivalent bypasses.
- [x] Run full verification and acceptance review again.

## Important Decisions

- Register the full application Module inventory exactly once per command invocation.
- `serve --migrate` executes automatic migration before Initialize/Start.
- `migrate <action>` executes the shared lifecycle only through migration, then exits.
- Modules without `MigrationSourceProvider` are skipped naturally.
- Modules own migration sources; the application retains the single migration executor and reporting policy.
- Do not introduce a second migration catalog, DTO, or dependency.
- Migration command options are only `action/module/to`; application and Module
  settings come from the same AppConfig used by serve.
- `mapstructure:",squash"` has one flat meaning across flags, strict/file
  decoding, defaults/trim, validation, generated YAML, and effective config.
- Every application Module retains the shared Runtime; derived narrow manager
  capabilities do not own or close Runtime resources.
- Application identity has no hard-coded brand default. CLI display names derive
  from the executable name; deployments may set `AppConfig.Name` explicitly.

## Confirmed Constraints

- Keep the change limited to Module composition, migration lifecycle integration, and necessary tests/docs.
- Preserve existing migration actions and CLI behavior.
- Maintain aggregate and per-Module coverage at 90% or higher.
- Do not commit or push unless the user explicitly requests it.
- Execution mode is Direct because the lifecycle changes are tightly coupled.

## Known Issues / Risks

- No unresolved implementation issue. User approved commit and push.

## Verification

Build: PASS

Tests:

- `go test -count=1 ./pkg/config ./cmd/7link-api/cmd ./internal/app
  ./internal/infra/dlock ./internal/infra/wuid ./internal/infra/audit`: PASS
- `go test -count=1 ./...`: PASS
- `go test -count=1 -race ./...`: PASS
- `go test -count=1 ./...` in `apps/backend-api/tests`: PASS

Other verification:

- `go vet ./...`: PASS
- `make test-coverage`: PASS; total 92.1%, every application Module root
  package at least 90% (lowest: pprof 90.5%)
- `git diff --check`: PASS
- Structural scan: exactly one production datasource Router construction,
  Runtime construction, Registry construction, and Module inventory; all are in
  `internal/app/bootstrap.go`. No production module-level migration commands remain.
- Post-test self-review: PASS (scope, reuse, architecture, rollback, regression,
  complexity, and durable state).
- Final acceptance review: PASS; user approved delivery.

## Next Action

Commit and push the verified worktree to the current branch.

## Last Checkpoint

2026-08-31 user approved the verified worktree for commit and push.
