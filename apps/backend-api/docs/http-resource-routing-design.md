# HTTP Resource Routing Design

Status: HISTORICAL DESIGN - superseded rules identified below

## Current Authority (2026-09-25)

Update 2026-09-26: the user requires Swagger grouping by the first segment of
the final published path. This supersedes this document's workflow-group rule
and D75's grouping exception. See
[api-routing-inspection-2026-09-26.md](api-routing-inspection-2026-09-26.md)
for the measured implicit-subject route inventory and proposed correction scope.

D74/D76/D77 confirm design principles, not whole-manifest execution. D77
supersedes rules 7/9/15 permitting dual item addresses: one canonical URI,
natural item keys, and GET /me plus GET /sessions/current discovery entries.
Membership identity is (tenant_id, principal_id), not an unresolved new ID.
Historical file sections describing creatable personal roots are obsolete:
the runtime already rejects that scope; account is the tenant-independent drive.

The sole execution specification for the approved optimization is
[api-optimization-implementation-2026-09-25.md](api-optimization-implementation-2026-09-25.md).
The sections below preserve design history and are not instructions to migrate
every runtime route. Existing collections retained by that plan are deliberate
scope decisions, not an implicit migration backlog.

## Goal

Make every public application URL describe a business resource instead of a Go
package, internal database model, or command handler. Keep protocol endpoints
where their external specifications define the contract.

The generated OpenAPI document currently contains 206 paths. This proposal
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
   Collection creation and listing are nested under the direct owner. An
   existing resource always has a short global-ID address and MAY also have a
   direct-owner scoped address, for example `/entities/{entity_id}` and
   `/tenants/{tenant_id}/entities/{entity_id}`. Both identify exactly the same
   resource, representation, version, and mutation; the scoped form validates
   the parent/resource relationship and returns not found on mismatch. It is a
   deliberate contextual address, not a compatibility alias or a second API.
   Clients never select authorization scope through headers or query parameters.
8. Each application operation has exactly one lowercase kebab-case OpenAPI tag
   from the approved workflow-domain vocabulary. Paths express resource identity
   and ownership; tags express where a human finds the operation in Swagger.
   Neither first path segments nor Go package names determine documentation
   groups.
9. Old and new routes are not exposed together in Swagger. This is a breaking
   pre-production replacement; the Web client and generated client change in
   the same checkpoint. This rule does not prohibit approved global/scoped dual
   addresses defined by rule 7.
10. `GET /me` is the only current-principal discovery projection. It returns the
    authenticated principal identifier needed after login. All profile and child
    resources use `/principals/{principal_id}/...` for both self-service and
    authorized administration; `/me` is not a parallel resource hierarchy. A
    tenant, entity, or department may be an authorization target but never owns
    `me`. Menu visibility is not an authorization mechanism.
11. A collection segment must name the resource actually returned or created.
    `/tenants/{tenant_id}/principals` cannot stand for tenant memberships:
    `principal` is global and `membership` is the tenant-owned relationship.
    Compound workflows may update both atomically, but their public resource
    contract must not conceal one resource behind the other resource's name.
12. Route design starts with a resource contract, not with an existing handler,
    table, foreign key, or screen. Before a URI is approved, record the
    resource's meaning, direct owner, identity, lifecycle, representation, and
    authorization boundary. An optional foreign key does not prove ownership.
13. Path parameters identify the resource named by their collection. A
    `{principal_id}` may identify a membership item only when the membership is
    explicitly defined as a relationship whose identity is the principal within
    that parent. If membership is a first-class resource, it requires its own
    stable identity. Storage primary keys do not decide the public contract.
14. A compound workflow may atomically create or update several resources, but
    its request and response must name every affected resource. Atomicity does
    not justify calling a membership a principal, a delivery an invitation, or
    a verification a request.
15. A scoped item address is allowed only below the resource's direct owner. It
    MUST reuse the same service operation, schemas, authorization policy,
    concurrency token, audit event, and canonical global identity as the short
    address. It MUST NOT introduce owner-specific fields or behavior. Do not
    generate every possible ancestor chain or transitive alias.

### Global And Scoped Item Resolution

Both addresses resolve identity from the resource ID first:

1. `/resources/{resource_id}` loads the globally identified resource, derives
   its owner from trusted data, and authorizes that owner/resource pair.
2. `/owners/{owner_id}/resources/{resource_id}` loads the same resource by
   `resource_id`, derives its actual owner, and compares it with `owner_id`.
3. An owner mismatch fails with the standard not-found response before business
   execution. The path owner never overrides or supplies missing ownership.
4. Both routes call the same service method and return the same representation,
   version/ETag, status semantics, side effects, and audit event.

This preserves one resource identity while allowing both context-rich and short
addresses. Caches and generated clients treat them as two addresses of the same
contract, never as independent mutable representations.

## Resource Semantic Gate

Every application resource MUST have a reviewed contract card before a route is
added or renamed:

| Field | Required evidence |
| --- | --- |
| Meaning | One business noun with a definition that does not depend on a page or handler. |
| Direct owner | Required ownership/lifecycle relation, distinguished from optional context and authorization scope. |
| Identity | Stable ID, documented composite identity, or justified singleton key. |
| Lifecycle | Creation, readable states, allowed transitions, deletion/cancellation, and history semantics. |
| Representation | Canonical fields, projections, embedded summaries, media type, and field filtering. |
| Authorization | Who may act on self or another target, contextual scope, fresh-authentication needs, and audit effects. |

If any field is unknown, the route stays in `SEMANTIC_DECISION_REQUIRED`; it is
not promoted into the canonical matrix. A proposal must label evidence as one
of: `MODEL_CONFIRMED`, `CONTRACT_CONFIRMED`, or `PRODUCT_DECISION_REQUIRED`.

## Canonical Ownership And Addressing

The canonical path is selected by operation semantics, not by how many parent
IDs the database can reconstruct:

| Operation | Canonical shape | Example |
| --- | --- | --- |
| Create/list owned collection | nest below direct owner | `POST /tenants/{tenant_id}/entities` |
| Read/update/delete existing resource | global ID, optionally plus direct-owner scoped address | `PATCH /entities/{entity_id}` and `PATCH /tenants/{tenant_id}/entities/{entity_id}` |
| Manage direct child collection | nest below direct owner | `GET /oauth-clients/{client_id}/credentials` |
| Manage a relationship | name both related resources | `PUT /entities/{entity_id}/role-bindings/{role_id}/{principal_id}` |
| Read a contextual projection | complete the semantic gate first | No canonical example until context identity and authorization semantics are defined. |

Snowflake IDs make direct addressing unambiguous. They do not replace isolation:
repositories and authorization middleware still verify the resolved tenant and
parent relationship, and cross-tenant references fail as not found.

Opaque credentials, protocol identifiers, external-provider IDs, permission
codes, and slugs are not internal resource primary keys and are not converted to
snowflake IDs.

### Resource Contexts

```text
/principals/{principal_id}/**
/tenants/{tenant_id}/**
/entities/{entity_id}/**
/departments/{department_id}/**
/platform/**
```

Menus and files follow the same context tree. Administrative menu collections
are `/platform/menus`, `/tenants/{tenant_id}/menus`,
`/entities/{entity_id}/menus`, and `/departments/{department_id}/menus`.
Effective navigation is read from a principal projection with an explicit
context selector. Menu definitions, their resource-context placement, and
effective navigation are separate concerns; the projection delegates
authorization to the existing tenant/entity/department permission evaluator.

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
| `/iam/me`, `/iam/my` | `/me` | Identity discovery only; child resources use the canonical principal tree. |

### Authentication and Current Principal

`/authn` is also a package/domain prefix, so it is removed. Public transaction
collections stay top-level; resources owned by a principal move under the
canonical `/principals/{principal_id}` tree.

| Current family | Proposed family |
| --- | --- |
| `/authn/me` | `/me` |
| `/authn/sessions` | `/sessions` |
| `/authn/sessions/current` | `/sessions/current` |
| `/authn/registrations` | `/registrations` |
| `/authn/captchas` | `/captchas` |
| `/authn/email-verification-requests` | `/email-verification-challenges` |
| `/authn/email-verifications` | `/email-verifications` |
| `/authn/password-recovery-requests` | `/password-recovery-challenges` |
| `/authn/password-recoveries` | `/password-recoveries` |
| `/authn/session-verifications` | `/session-verifications` |
| `/authn/reauthentication-*` | `/reauthentication-*` |
| `/authn/step-up-*` | `/step-up-*` |
| `/authn/passkey-session-*` | `/passkey-session-*` |
| `/authn/capabilities` | `/authentication-capabilities` |
| `/authn/password` | `/principals/{principal_id}/password` |
| `/authn/mfa*` | `/principals/{principal_id}/mfa*` |
| `/authn/passkeys*` | `/principals/{principal_id}/passkeys*` |
| `/authn/personal-tokens*` | `/principals/{principal_id}/personal-tokens*` |
| `/authn/identity-links*` | `/principals/{principal_id}/identity-links*` |

Challenge, verification, recovery, and enrollment records are nouns and may be
short-lived; they are still resources and therefore valid collection names.

### Principal Resources And Identity Discovery

`GET /me` is a bootstrap projection, not a second account API. It exists because
a newly authenticated client may not yet know the authenticated principal ID.
After discovery, both self-service and authorized administration use the same
principal resources:

- `/principals/{principal_id}/avatar` and `/avatar-candidates`;
- `/principals/{principal_id}/account-closure`;
- `/principals/{principal_id}/memberships`;
- `/principals/{principal_id}/menus` and `/permissions` with an explicit context;
- `/principals/{principal_id}/oauth-consents`;
- `/principals/{principal_id}/files`, credentials, sessions, and identity links.

Leaving a tenant becomes
`DELETE /principals/{principal_id}/memberships/{tenant_id}`. It is deletion of
the selected principal's membership, subject to authorization, not a special
self-only command.

The current avatar is a binary subresource at
`GET /principals/{principal_id}/avatar`. Private generated candidates are
separate expiring resources at
`GET /principals/{principal_id}/avatar-candidates/{candidate_id}`. The
candidate identifier is opaque and never a storage filename. `GET/PUT
`/principals/{principal_id}/avatar` retains the avatar metadata/configuration
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
- tags outside the approved workflow-domain vocabulary or mapped to the wrong
  workflow domain;
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
3. Create/list genuinely owned collections below their proven direct owner;
   address existing globally identified resources by their short item URI and,
   where approved, the equivalent direct-owner scoped item URI. Authorization
   resolves and verifies ownership instead of trusting a caller-selected owner
   header.
