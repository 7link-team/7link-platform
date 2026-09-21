# HTTP Resource Routing Design

Status: APPROVED - canonical resource-ID migration in progress

## Goal

Make every public application URL describe a business resource instead of a Go
package, internal database model, or command handler. Keep protocol endpoints
where their external specifications define the contract.

The generated OpenAPI document currently contains 202 paths. This proposal
covers every top-level family in that document.

## Reference Used

The archived Spring Boot project uses the useful core pattern consistently:

- plural resource collections such as `/files`, `/organizations`, and `/roles`;
- one resource at `/{id}`;
- owned resources at `/{id}/members`, `/{id}/roles`, or `/{id}/versions`;
- explicit representations such as `/{id}/raw`, `/{id}/info`, and
  `/{id}/thumb`.

This proposal adopts those resource and ownership rules. It does not copy the
archive's command endpoints such as `/batch` and `/recycler`, because those are
not resource-oriented and are not part of the current product contract.

## Mandatory Rules

1. Public paths use plural, lowercase, kebab-case resource nouns.
2. Package names and implementation domains are not path prefixes. In
   particular, `/iam`, `/authn`, `node`, `space`, and `workspace` are forbidden
   in application resource paths.
3. A resource is addressed as `/resources/{resource_id}`. Parameters use the
   precise resource name, not generic `id`, when more than one identifier can
   appear in a route.
4. Nesting expresses ownership or a relationship, for example
   `/groups/{group_id}/members/{principal_id}`. It does not mirror source-code
   packages.
5. HTTP methods carry CRUD semantics. Literal action segments such as
   `/approve`, `/reject`, `/retry`, `/rotate`, `/resend`, and `/restore` are
   replaced by a noun subresource or a state update.
6. A representation with materially different media semantics is a noun
   subresource, such as `/content`, `/thumbnail`, `/status`, or `/settings`.
7. Every first-class internal resource uses a globally unique snowflake ID.
   Collection creation and listing are nested under the direct owner; an
   existing resource is addressed by its own global ID. For example, entities
   are created at `/tenants/{tenant_id}/entities` and then addressed at
   `/entities/{entity_id}`. The server resolves and validates ownership; clients
   never select authorization scope through headers or query parameters.
8. Each application operation has exactly one lowercase kebab-case OpenAPI tag,
   equal to the first public path segment. The same public resource prefix is
   therefore always in the same Swagger group; Go package names such as `iam`,
   `authn`, `identity`, or `governance` are never public tags.
9. Old and new routes are not exposed together in Swagger. This is a breaking
   pre-production replacement; the Web client and generated client change in
   the same checkpoint.
10. Current-principal projections are explicit at every resource context:
    `/me/*`, `/tenants/{tenant_id}/me/*`, `/entities/{entity_id}/me/*`, and
    `/departments/{department_id}/me/*`. A projection reuses the same scoped
    authorization evaluator as the protected resource; menu visibility is not
    an authorization mechanism.

## Canonical Ownership And Addressing

The canonical path is selected by operation semantics, not by how many parent
IDs the database can reconstruct:

| Operation | Canonical shape | Example |
| --- | --- | --- |
| Create/list owned collection | nest below direct owner | `POST /tenants/{tenant_id}/entities` |
| Read/update/delete existing resource | global resource ID | `PATCH /entities/{entity_id}` |
| Manage direct child collection | nest below direct owner | `GET /entities/{entity_id}/departments` |
| Manage a relationship | name both related resources | `PUT /entities/{entity_id}/role-bindings/{role_id}/{principal_id}` |
| Project current user's view | resource context plus `/me` | `GET /entities/{entity_id}/me/menus` |

Snowflake IDs make direct addressing unambiguous. They do not replace isolation:
repositories and authorization middleware still verify the resolved tenant and
parent relationship, and cross-tenant references fail as not found.

Opaque credentials, protocol identifiers, external-provider IDs, permission
codes, and slugs are not internal resource primary keys and are not converted to
snowflake IDs.

### Resource Contexts

```text
/me/**
/tenants/{tenant_id}/**
/entities/{entity_id}/**
/departments/{department_id}/**
/platform/**
```

Menus and files follow the same context tree. Administrative menu collections
are `/platform/menus`, `/tenants/{tenant_id}/menus`,
`/entities/{entity_id}/menus`, and `/departments/{department_id}/menus`.
Effective navigation is read from the corresponding `/me/menus` projection.
Menu definitions, their resource-context placement, and effective navigation
are separate concerns; the projection delegates authorization to the existing
tenant/entity/department permission evaluator.

## Route Classes

### Standards and Operations: Preserve

These paths are protocols or operational probes rather than application REST
resources. Their established spelling takes precedence over local style.

| Current family | Decision |
| --- | --- |
| `/.well-known/*` | Preserve OIDC discovery and JWKS paths. |
| `/oauth/*` | Preserve OAuth/OIDC protocol endpoints and methods. |
| `/scim/v2/*` | Preserve SCIM paths, including specification-defined casing. |
| `/healthz`, `/livez`, `/readyz` | Preserve operational probe paths. |

Federated browser callbacks are also protocol endpoints, but their hierarchy is
made explicit as described below. Once published, callback URLs require an IdP
configuration migration.

### Infrastructure

| Current | Proposed | Reason |
| --- | --- | --- |
| `GET /guid` | `POST /identifiers` | Allocate one identifier resource. |
| `GET /guid/{count}` | `POST /identifier-batches` | Allocation is not a read; count belongs in the request body. |

Diagnostic routes that are disabled from the public production document remain
outside this application-resource contract.

## File Resources

The database may continue to use a unified node model. The HTTP contract exposes
one file-entry resource: a directory is a file with `kind=directory`. Internal
`node`, `space`, and `workspace` names never appear in a public file URL.

| Method | Public path | Meaning |
| --- | --- | --- |
| `GET` | `/files` | List authorized root file entries. Every item has `kind=directory`; `scope` identifies personal, department, entity, or tenant ownership. |
| `POST` | `/files` | Create one file entry from multipart form data. `parent_id + kind=directory + name` creates a directory; `parent_id + kind=file + file` uploads a file; `kind=directory + scope=personal` provisions the caller's personal root. |
| `GET` | `/files/{file_id}` | Read authorized metadata for a file or directory. |
| `GET` | `/files/{file_id}/items` | List a directory's authorized child file entries; a non-directory ID returns 404. |
| `GET` | `/files/{file_id}/raw` | Download original file bytes by file ID. |
| `GET` | `/files/{root_file_id}/-/{path}` | Resolve metadata by a slash-separated path relative to one explicitly authorized root. The runtime accepts multiple path segments. |
| `PATCH` | `/files/{file_id}` | Update file or directory metadata. |
| `DELETE` | `/files/{file_id}` | Move a file or directory subtree to trash. |

The root ID in the path route is mandatory because one caller can see multiple
personal, department, entity, and tenant roots. Resolution authorizes that root
and every traversed entry. Public schemas and operation names use `file` or
`entry`, never `node`. Copy/move remains internal until its public batch resource
is designed.

## Business Resource Migration Matrix

Every current `/iam` family is included below. A plain arrow means methods and
resource suffixes remain the same after removing the implementation prefix;
exceptions are listed explicitly.

| Current family | Proposed family | Notes |
| --- | --- | --- |
| `/iam/tenants` | `/tenants` | Platform-owned tenant resources. |
| `/iam/entities` | `/entities` | Tenant-scoped resources; keep entity role-binding children. |
| `/iam/departments` | `/departments` | Entity selection remains explicit in the request scope. |
| `/iam/groups` | `/groups` | Keep `/groups/{group_id}/members/{principal_id}`. |
| `/iam/positions` | `/positions` | Keep `/positions/{position_id}/members/{principal_id}`. |
| `/iam/principals` | `/principals` | Principal resources, not an IAM implementation path. |
| `/iam/service-accounts` | `/service-accounts` | Keep credential child resources. |
| `/iam/roles` | `/roles` | Keep permission, member, data-scope, and binding children. |
| `/iam/platform-administrators` | `/platform/administrators` | Scope is part of the resource identity. |
| `/iam/relationships` | `/relationships` | Relationship collection. |
| `/iam/menus` | `/menus` | Menu resources. |
| `/iam/menu-catalog` | `/menu-catalog` | Read-only catalog. |
| `/iam/permission-catalog` | `/permission-catalog` | Tenant catalog. |
| `/iam/platform-permission-catalog` | `/platform/permission-catalog` | Platform catalog. |
| `/iam/field-catalog` | `/field-catalog` | Read-only catalog. |
| `/iam/field-policies` | `/field-policies` | Policy resources. |
| `/iam/fields/effective` | `/effective-fields` | Effective field projection. |
| `/iam/scope-model` | `/scope-model` | Read-only scope description. |
| `/iam/access-requests` | `/access-requests` | State commands are replaced below. |
| `/iam/requestable-roles` | `/requestable-roles` | Read-only projection. |
| `/iam/access-reviews` | `/access-reviews` | State commands are replaced below. |
| `/iam/audit*` | `/audit/*` | One coherent audit hierarchy, detailed below. |
| `/iam/authorization/*` | `/authorization-*` | Ephemeral evaluation resources, detailed below. |
| `/iam/identity-providers` | `/identity-providers` | Administrative provider resources. |
| `/iam/saml/service-providers` | `/saml-service-providers` | Resource name is globally unambiguous. |
| `/iam/saml/signing-key/rotate` | `/saml-signing-keys` | `POST` creates and activates a new key. |
| `/iam/scim/directories` | `/scim-directories` | Avoid collision with file directories. |
| `/iam/scim/targets` | `/scim-targets` | Provisioning commands become job resources. |
| `/iam/oauth-clients` | `/oauth-clients` | Administrative client resources. |
| `/iam/invitations` | `/invitations` | Delivery and acceptance become resources. |
| `/iam/avatars/{principal_id}/{file}` | `/principals/{principal_id}/avatar` | Read the current avatar without exposing a storage filename. |
| `/iam/members` | `/members` | Tenant member resources. |
| `/iam/me/*`, `/iam/my/*` | `/me/*` | Merge authenticated-principal singleton routes. |

### Authentication and Current Principal

`/authn` is also a package/domain prefix, so it is removed. Public transaction
collections stay top-level; resources owned by the current principal move under
the `/me` singleton.

| Current family | Proposed family |
| --- | --- |
| `/authn/me` | `/me` |
| `/authn/sessions` | `/sessions` |
| `/authn/sessions/current` | `/sessions/current` |
| `/authn/registrations` | `/registrations` |
| `/authn/captchas` | `/captchas` |
| `/authn/email-verification-requests` | `/email-verification-requests` |
| `/authn/email-verifications` | `/email-verifications` |
| `/authn/password-recovery-requests` | `/password-recovery-requests` |
| `/authn/password-recoveries` | `/password-recoveries` |
| `/authn/session-verifications` | `/session-verifications` |
| `/authn/reauthentication-*` | `/reauthentication-*` |
| `/authn/step-up-*` | `/step-up-*` |
| `/authn/passkey-session-*` | `/passkey-session-*` |
| `/authn/capabilities` | `/authentication-capabilities` |
| `/authn/password` | `/me/password` |
| `/authn/mfa*` | `/me/mfa*` |
| `/authn/passkeys*` | `/me/passkeys*` |
| `/authn/personal-tokens*` | `/me/personal-tokens*` |
| `/authn/identity-links*` | `/me/identity-links*` |

Challenge, verification, recovery, and enrollment records are nouns and may be
short-lived; they are still resources and therefore valid collection names.

### Current-Principal Resources

The existing `/iam/me` and `/iam/my` paths merge without changing ownership:

- `/me/avatar` and `/me/avatar-candidates`;
- `/me/closure-request`;
- `/me/memberships`;
- `/me/menus`;
- `/me/oauth-consents`;
- `/me/tenants` where it is a distinct projection from memberships.

Leaving a tenant becomes `DELETE /me/memberships/{tenant_id}`. It is deletion of
the caller-owned membership, not `POST .../leave`.

The current avatar is a binary subresource at
`GET /principals/{principal_id}/avatar`. Private generated candidates are
separate expiring resources at `GET /me/avatar-candidates/{candidate_id}`. The
candidate identifier is opaque and never a storage filename. `GET/PUT
/me/avatar` retains the current principal's avatar metadata/configuration
contract. Avatar bytes are not mixed into a principal JSON update because they
have different media, size, caching, and authorization semantics.

### Authorization Evaluations

These operations compute ephemeral results and do not mutate policy:

| Current | Proposed |
| --- | --- |
| `POST /iam/authorization/check` | `POST /authorization-checks` |
| `POST /iam/authorization/constraints` | `POST /authorization-constraint-evaluations` |
| `POST /iam/authorization/explain` | `POST /authorization-explanations` |

### Governance State Transitions

| Current | Proposed |
| --- | --- |
| `POST /iam/access-requests/{request_id}/approve` | `PUT /access-requests/{request_id}/decision` with `approved` |
| `POST /iam/access-requests/{request_id}/reject` | `PUT /access-requests/{request_id}/decision` with `rejected` |
| `POST /iam/access-requests/{request_id}/withdraw` | `PUT /access-requests/{request_id}/withdrawal` |
| `POST /iam/access-requests/{request_id}/revoke` | `PUT /access-requests/{request_id}/grant-revocation` |
| `POST /iam/access-reviews/{review_id}/cancel` | `PATCH /access-reviews/{review_id}` with status transition |
| `POST /iam/access-reviews/{review_id}/complete` | `PATCH /access-reviews/{review_id}` with status transition |
| `POST /iam/access-reviews/{review_id}/items/{item_id}/decide` | `PUT /access-reviews/{review_id}/items/{item_id}/decision` |

Mutually exclusive approval/rejection is one decision singleton, not two action
URLs. Withdrawal and revocation are distinct auditable resources.

### Audit Resources

Tenant paths live under `/audit`; platform paths use the same suffixes under
`/platform/audit`. The extra `/iam` segment is removed from both.

| Current suffix | Proposed suffix |
| --- | --- |
| `/audit-events` | `/audit/events` |
| `/audit-events/{event_id}` | `/audit/events/{event_id}` |
| `/audit-events/export` | `/audit/exports` |
| `/audit/archives` | `/audit/archives` |
| `/audit/archives/{archive_id}/download` | `/audit/archives/{archive_id}/content` |
| `/audit/archives/{archive_id}/retry` | `/audit/archives/{archive_id}/attempts` |
| `/audit/archives/{archive_id}/verify` | `/audit/archives/{archive_id}/verifications` |
| `/audit/archive-status` | `/audit/archive-status` |
| `/audit/governance` | `/audit/governance-policy` |
| `/audit/retention` | `/audit/retention-policy` |
| `/audit/roots/sign` | `/audit/root-signatures` |
| `/audit-anchor` | `/audit/anchors` |
| `/audit-integrity` | `/audit/integrity-verifications` |

### Federation and Provisioning

| Current | Proposed |
| --- | --- |
| `GET /federation/{provider_id}/start` | `GET /identity-providers/{provider_id}/authorization` |
| `GET|POST /federation/{provider_id}/callback` | `GET|POST /identity-providers/{provider_id}/callback` |
| `GET /federation/{provider_id}/identity-continuations` | `GET /identity-providers/{provider_id}/identity-continuation` |
| `/federation/saml/{tenant_id}/metadata` | `/saml/{tenant_id}/metadata` |
| `/federation/saml/{tenant_id}/sso` | `/saml/{tenant_id}/sso` |
| `/federation/saml/{tenant_id}/slo` | `/saml/{tenant_id}/slo` |
| `POST /iam/scim/targets/{target_id}/push` | `POST /scim-targets/{target_id}/provisioning-jobs` |
| `POST /iam/scim/targets/{target_id}/deprovision` | `POST /scim-targets/{target_id}/deprovisioning-jobs` |
| `POST /iam/scim/targets/{target_id}/retire` | `PATCH /scim-targets/{target_id}` |

The SAML `metadata`, `sso`, and `slo` suffixes are protocol concepts and remain.

### Invitations and Credentials

| Current | Proposed |
| --- | --- |
| `POST /iam/invitations/{invitation_id}/resend` | `POST /invitations/{invitation_id}/deliveries` |
| `POST /iam/invitations/accept` | `POST /invitation-acceptances` |
| `POST /iam/oauth-clients/{client_id}/rotate-secret` | `POST /oauth-clients/{client_id}/credentials` |

### Platform Identity Lifecycle

Drop the embedded `/iam` prefix: `/platform/iam/principals/...` becomes
`/platform/principals/...`, and `/platform/iam/closure-requests` becomes
`/platform/closure-requests`.

Command suffixes become resource changes:

| Current suffix | Proposed suffix |
| --- | --- |
| `/closure-request/processing` | `PATCH /closure-request` with `processing` status |
| `/closure-request/rejection` | `PATCH /closure-request` with `rejected` status |
| `/lifecycle/anonymization` | `PUT /anonymization` |
| `/lifecycle/retention` | `PUT /retention-policy` |
| `/suspension` | Preserve as a singleton resource with `PUT` and `DELETE`. |

### Todo Example Module

The example module follows the same rules so it does not teach a conflicting
style:

| Current | Proposed |
| --- | --- |
| `/todos` and `/todos/{todo_id}` | Preserve. |
| `POST /todos/{todo_id}/restore` | `DELETE /todos/{todo_id}/deletion` |
| `GET /todos/export` | `GET /todo-exports` |
| `POST /todos/import` | `POST /todo-imports` |
| `GET /todos/import-template` | `GET /todo-import-template/content` |

## Enforcement

Implementation adds an OpenAPI contract test over every operation. It rejects:

- application paths beginning with `/iam` or `/authn`;
- internal segments `nodes`, `spaces`, or `workspaces`;
- uppercase application resource segments;
- generic path parameters where a precise resource parameter is required;
- unapproved literal action segments;
- tags containing uppercase characters;
- tags that differ from the first public path segment;
- implementation tags `iam`, `authn`, `identity`, `organization`,
  `governance`, `federation`, or `provisioning`;
- duplicate operation IDs or route/method pairs.

The allowlist contains only standards and probes: OIDC discovery, OAuth, SCIM,
SAML protocol suffixes, and health checks.

## Execution Order

1. Add the global OpenAPI path-convention test.
2. Correct files first, including backend kind validation, OpenAPI generation,
   generated TypeScript, Web calls, API lifecycle testing, and browser testing.
3. Migrate authentication and current-principal routes.
4. Migrate tenant, organization, identity, authorization, and governance routes.
5. Migrate audit, federation, provisioning, and platform lifecycle routes.
6. Migrate infrastructure and example routes.
7. Regenerate OpenAPI and clients; run all backend and frontend gates; verify the
   live Swagger has no legacy application routes.

## Approved Breaking-Change Policy

The user approved these decisions for this pre-production API:

1. Remove `/iam` and `/authn` entirely from application resource URLs.
2. Perform a clean pre-production replacement with no deprecated route aliases.
3. Keep tenant/entity selection in mandatory headers instead of redundantly
   nesting every globally identified resource under tenant/entity path segments.
