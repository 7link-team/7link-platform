# Task State

## Task

Confine the HTTP router implementation to the application adapter.

## Objective

Keep application Modules portable across Huma HTTP adapters: Modules may
register typed Huma operations and contribute standard-library raw handlers,
but must not import or operate on chi. The current app adapter owns chi route
mounting and chi route-pattern extraction.

## Current Phase

ACCEPTANCE

## Current Step

Await user review of the verified, uncommitted worktree.

## Overall Progress

- [x] Recover and reconcile repository, Git, tests, and prior task state.
- [x] Confirm the HTTP adapter boundary with the user.
- [x] Remove chi from `pkg/module` and concrete Modules.
- [x] Preserve raw-route and metrics behavior through the app adapter.
- [x] Update tests and architecture documentation.
- [x] Run full verification, self-review, and acceptance review.

## Important Decisions

- Huma remains the typed HTTP API contract available to Modules.
- `RawRoute` remains a pure `net/http` description; mounting belongs to the
  selected HTTP adapter.
- metrics exposes a narrow observation service. It does not resolve router
  patterns and does not receive a router-specific callback.
- The chi adapter resolves low-cardinality route patterns after the handler
  runs. Empty patterns are recorded as `unmatched` by metrics.
- Do not add Gin or runtime-selectable HTTP engines in this change.

## Confirmed Constraints

- Do not commit or push.
- Do not add unrelated features, files, dependencies, or abstractions.
- Preserve middleware order and all existing raw-route behavior.
- Maintain aggregate and per-Module coverage at 90% or higher.
- Execution mode is Direct because the contract and adapter changes are tightly coupled.

## Known Issues / Risks

- No unresolved implementation issue. A future Gin or other adapter remains a
  separate change in the HTTP adapter assembly, not in Modules.

## Verification

Build: PASS

Tests:

- `go test -count=1 ./...`: PASS
- `go test -count=1 -race ./...`: PASS
- `go test -count=1 ./...` in `apps/backend-api/tests`: PASS

Other verification:

- `go vet ./...`: PASS
- `make test-coverage`: PASS; total 92.2%, every application Module root
  package at least 90% (lowest: pprof 90.5%).
- `git diff --check`: PASS
- Structural scan: no production chi reference in `internal/infra` or
  `pkg/module`; no `RawRoute.Mount` call remains.
- Post-test self-review and final acceptance review: PASS.

## Next Action

User reviews the uncommitted diff. Do not commit or push without an explicit request.

## Last Checkpoint

2026-08-31 implementation and full verification complete; worktree intentionally uncommitted.
