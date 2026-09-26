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

The proposed subject-address correction changes all 45 implicit operations in
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

This batch changes public API behavior. Its exact scope has been submitted for
confirmation under CLAUDE.md's substantial-change rule. At this checkpoint, no
subject-address route is claimed migrated. Prefix grouping and development startup
are independently verifiable and implemented.
