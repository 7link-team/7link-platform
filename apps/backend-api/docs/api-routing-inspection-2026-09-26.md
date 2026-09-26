# API routing inspection - 2026-09-26

## Requirement and measured baseline

The latest user instruction requires Swagger grouping by the first segment of
the final published path. It supersedes D75's workflow grouping. Explicit account
addressing was already confirmed in D74: discover the authenticated identity with
`GET /me`, then address account resources using `/principals/{principal_id}/...`.
The repository resource is Principal, rather than a new parallel `user` resource.

The generated OpenAPI at d087516 contains 206 paths and 296 operations. A
structured scan of `paths` (HTTP methods only) found 245 operations whose sole
tag differs from their first path segment. This affects tenant files, entities,
departments, roles, identity providers, account resources and platform operations.
It is a shared publication-rule problem, not three isolated file endpoints.

## Implicit subject addresses

There are 41 operations below `/me/`, plus these four tenant-contained operations:

- `GET /tenants/{tenant_id}/me/access-requests`
- `GET /tenants/{tenant_id}/me/menus`
- `GET /tenants/{tenant_id}/me/permissions`
- `GET /tenants/{tenant_id}/entities/{entity_id}/me/permissions`

| `/me/` family | Operations |
| --- | ---: |
| Avatar and avatar candidates | 4 |
| Closure request | 3 |
| Files and file operations | 3 |
| Identity links, providers and transactions | 4 |
| Memberships | 2 |
| MFA, enrollment, TOTP and recovery codes | 5 |
| OAuth consents | 2 |
| Passkeys and registration challenges | 5 |
| Password and permissions | 2 |
| Personal tokens, preparation and permissions | 5 |
| Reauthentication methods, challenges and verifications | 3 |
| Session collection | 2 |
| Tenants | 1 |
| **Total** | **41** |

## Concrete implementation boundaries

Prefix grouping is implemented through Huma's `OnAddOperation` hook in the shared
API builder. It runs against the final expanded path, including generated tenant
and entity mounts and transport-adapter operations. One prefix means one tag.
Runtime paths, authorization, methods and response shapes are unaffected by tags.
Regression checks cover shared registration, full exported OpenAPI and live Swagger.

The implemented subject-address correction changes all 45 implicit operations in
one coordinated contract change, preserving `GET /me` and `/sessions/current`.
Examples: `/me/files` becomes `/principals/{principal_id}/files`; tenant
projections replace `/me/` with `/principals/{principal_id}/` inside the same
tenant/entity mount. The old routes must disappear, and backend registration,
generated schema, typed client, fixture requests and E2E assertions must agree.

The path ID must be validated against the authenticated subject before any read
or mutation. These self-service operations must not become cross-account admin
operations merely because an ID is now supplied. Existing administration contracts
and per-resource owner checks remain authoritative. MFA enrollment also needs to
preserve its existing restricted-credential behavior; adding ordinary-session
middleware indiscriminately would break required enrollment.

## Implemented result and evidence

The user authorized the complete migration with "继续，搞到位". The final contract
retains all 296 operation IDs and methods. Exactly 45 implicit principal addresses
changed, and the authorized avatar image reader moved to
`GET /principals/{principal_id}/avatar/content` to avoid colliding with the
avatar settings GET. This is the necessary 46th changed operation. Historical
stored avatar references remain readable; no database or file migration is needed.

Personal routes explicitly declare their owner check through the existing
registrar. Authentication and tenant membership run first; canonical malformed
principal IDs return 422 and a foreign principal returns 404 before the handler.
The authorized avatar content reader retains its existing tenant permission and
field policy checks. Restricted MFA enrollment uses its existing verifier, with
the verified subject supplied in the password login result.

The generated contract has zero `/me/` segments, retains `GET /me` and
`/sessions/current`, has no duplicate operation IDs or principal selectors, and
uses exactly one first-prefix tag per operation. A structured comparison against
4830b04 confirms unchanged methods, request bodies and credential security schemes.
Backend and frontend generated artifacts and every personal API client call agree.
The `/me/*` Web page URLs remain personal navigation routes, not API aliases.

Verification on 2026-09-26:

- Real SQLite + Chrome E2E: **15/15 PASS**, including read/write foreign-ID denial,
  malformed IDs, retired paths, avatar settings/PNG content, account files,
  sessions, tenant permissions, live grant/revoke and CSRF. Evidence:
  `.temp/sqlite-lifecycle-vExue5/report.json`; no uncaught browser errors.
- Browser fixture regression: **74/74 PASS**; archive desktop/mobile: **PASS**.
- Live `make dev` demo selector: **5/5 PASS** for login, session and linked identity
  APIs/pages. Swagger: **309/309** first-prefix tags (includes diagnostics).
- `go test ./...`: **PASS**, including composed operation/guard inventory.
- Backend lint and OpenAPI drift: **PASS**.
- `make test-integration`: **PASS** (architecture, goose, testdb, lifecycle,
  migration and performance packages).
- Frontend workspace typecheck, lint, tests and production build: **PASS**;
  IAM **136** and UI **85** unit tests. Generated API type drift: **PASS**.

Additional environment-specific audit scripts received explicit-principal caller
updates and `node --check`; they were not executed against external UAT fixtures.
The real browser and fixture acceptance listed above are the executed evidence.

Direct execution; no agents. Self-review and requirement acceptance include the
operation inventory, authentication preservation and actual browser evidence.
This completes the requested address and grouping correction; no optional
resource redesign is queued by this report.
