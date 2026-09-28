# SQLite IAM lifecycle acceptance

Run from `apps/frontend-web-admin/apps/iam`:

```sh
bun run audit:sqlite-lifecycle
```

The runner builds the real backend, migrates a fresh isolated SQLite database,
bootstraps a platform administrator and starts owned loopback API/Vite services.
It uses real browser sessions and HTTP requests, without business API mocks or
direct database fixture inserts. Existing databases and services are untouched.

Coverage includes first-tenant creation and owner initialization; department and
entity hierarchies; child accounts and distinct roles; department data scopes;
entity inheritance and deny overrides; group and position authorization sources;
dynamic-group membership changes; custom menu creation, rename and enablement;
permission grant/revocation; cross-tenant denial; account suspension/restoration;
permission-read failure and retry; and audit events.

Tenant definitions remain tenant-owned. Entities constrain business grants; they
do not introduce independent department, role or menu catalogs. Menu visibility
is user-specific and does not replace API authorization. Unauthorized parent
menus become directories only. Built-in page gates cannot be weakened by a
custom menu's permission binding.

API checks enforce revocation immediately. The UI reloads authorization after
its IAM mutations and on focus, and polls every 15 seconds for other sessions'
changes. Unknown or failed permission reads never enable protected actions.

Each run retains `report.json`, SQLite data, logs and desktop/mobile screenshots
under `apps/backend-api/.temp/sqlite-lifecycle-*`. Credentials/configuration in
that private directory are local acceptance artifacts, not deliverable source.
Owned browser and service processes are stopped on completion or failure.
