# IAM Browser Tests

This directory contains browser regression scripts, real end-to-end acceptance
flows and their UI fixtures. It is not imported by the application entry point;
production builds use `src/main.tsx`. Unit tests remain alongside source files.

Run from `apps/frontend-web/apps/web`:

```sh
bun run audit:sqlite-lifecycle
node tests/sqlite-lifecycle-browser-audit.mjs
```

The lifecycle gate starts isolated SQLite, API and browser services and writes
reports and screenshots under backend `.temp/sqlite-lifecycle-*`. It exercises
real business responses. Scripts documenting mocked HTTP boundaries are UI
contract tests, not backend acceptance.

`scripts/` retains API type generation and the account/tenant/demo initialization
tools used by local development. `audit-password.mjs` is their shared credential
reader. These tools are also excluded from application imports.
