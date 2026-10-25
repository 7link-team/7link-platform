# IAM Browser Tests

This directory contains browser regression scripts, real end-to-end acceptance
flows and their UI fixtures. It is not imported by the application entry point;
production builds use `src/main.tsx`. Unit tests remain alongside source files.

Run from `apps/frontend-web/apps/web`:

```sh
bun run audit:sqlite-lifecycle
node tests/sqlite-lifecycle-browser-audit.mjs
```

Stage A workspace scope acceptance uses an already-running isolated development
Web/API and the existing prefilled tenant demo login. It requires two admitted
instances, performs no fixture writes by default, and does not log credentials:

```sh
IAM_CONSOLE_URL=http://127.0.0.1:4317 node tests/workspace-scope-acceptance.mjs
IAM_CONSOLE_URL=http://127.0.0.1:4317 node tests/file-browser-ui-audit.mjs
```

The first script exercises real scope navigation, history, API refusal and mobile
controls. The second covers mounted file UI and mocked HTTP contract boundaries;
it also checks controlled filter restoration without stale updates.

Stage B creation acceptance runs against the same isolated services:

```sh
IAM_CONSOLE_URL=http://127.0.0.1:4317 node tests/project-create-acceptance.mjs
```

This writes one project per run through the real atomic API and retains it for
manual acceptance. It checks contract failure/retry, invalid input, duplicate
submission, lost successful response and idempotent replay, Entity/project identity,
creator bindings/channel/file access, reload/catalog refresh, duplicate-code conflict,
permission refusal, and mobile dialog fit. Only deliberate transport failures are
intercepted; successful responses and conflicts come from the isolated backend.
Demo credentials are selected through the existing UI and never logged.
On an isolated database only, `--seed-tenant` creates another tenant owned by the
tenant demo principal and two company instances through the existing system demo
login and IAM APIs. The fixture remains available for manual acceptance. It does
not change the deployment's self-service registration policy.

The lifecycle gate starts isolated SQLite, API and browser services and writes
reports and screenshots under backend `.temp/sqlite-lifecycle-*`. It exercises
real business responses. Scripts documenting mocked HTTP boundaries are UI
contract tests, not backend acceptance.

`scripts/` retains API type generation and the account/tenant/demo initialization
tools used by local development. `audit-password.mjs` is their shared credential
reader. These tools are also excluded from application imports.

`node tests/work-create-acceptance.mjs` uses the isolated API and the existing tenant-admin demo selector. It verifies real work creation/replay/detail/hash/isolation, Unicode validation, numbered pagination, sticky columns and both scroll axes. It leaves real work fixtures, including 71 pagination rows reused through fixed idempotency keys; never run it against a shared or production database.

`node tests/work-readiness-acceptance.mjs` uses the same isolated services and prefilled demo selector. It creates real work and verifies preparation, exact decimal budgets, omitted-budget clearing, immutable contracts, readiness checks, lost-response replay, stale-version input preservation, closing during a delayed save and mobile layout. It leaves acceptance fixtures and must only run against an isolated database.

`node tests/constitution-acceptance.mjs` exercises real immutable constitution proposals, proposer submission, Human approval, atomic superseding and the active project pointer. It checks exact-key retries after committed responses are lost, identity lookup failure/recovery, stale review input preservation, filter/page restoration, numbered server pagination, CSV export, compound auto-refresh and mobile sticky columns. Version comparison covers base selection/direction, line changes, identical content, detail-query failure/retry, escaped script text, result paging and mobile scrolling. It creates real proposal/review fixtures and must only run against the isolated acceptance database.
