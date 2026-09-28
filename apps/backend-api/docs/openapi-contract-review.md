# OpenAPI Public Contract Review

Status: HISTORICAL REVIEW - execution authority superseded

Current authority (2026-09-25): D77 supersedes the dual-address rule below;
Membership uses (tenant_id, principal_id), and Session discovery is
GET /sessions/current. The full proposal failed implementation review in
[api-migration-review-2026-09-25.md](api-migration-review-2026-09-25.md).
Execute only [api-optimization-implementation-2026-09-25.md](api-optimization-implementation-2026-09-25.md).
Historical unresolved questions below are not a work queue or required followups.

Date: 2026-09-23

## Scope

This review covers the generated `docs/openapi.json` in full: 206 paths, 295
operations, and 33 tags. It evaluates the public contract, not only path
spelling. The review checks resource identity, collection ownership, current
principal projections, state transitions, one-time credentials, protocol
exceptions, authorization boundaries, operation duplication, method semantics,
tag grouping, Swagger navigation, path readability, and compatibility impact.

No route change is authorized by this document. The proposed contract must be
confirmed before implementation. When implemented, the repository's approved
pre-production rule applies: old and new application routes are not published
together in OpenAPI.

## Mandatory Review Principles

These principles are ordered. A later concern cannot justify violating an
earlier one.

### 1. RESTful Resource Contract First

1. Before naming a URI, define the resource's meaning, direct owner, identity,
   lifecycle, canonical representation, and authorization boundary. Existing
   paths, handlers, tables, foreign keys, and screens are evidence, not the
   definition.
2. Every application URI names a resource, collection, representation,
   relationship, or durable state. It does not name a handler or a command.
3. Creation and listing of an owned collection stay below the proven direct
   owner. An optional foreign key or authorization context does not prove
   ownership.
4. Existing snowflake-ID resources have a global resource path. A direct-owner
   scoped item path may coexist when it identifies the exact same resource and
   behavior and validates the parent relationship. A parameter must identify
   the resource named by its collection; storage keys do not automatically
   become public resource identities.
5. HTTP methods carry their ordinary semantics: GET reads, POST creates, PUT
   replaces or creates an idempotent singleton, PATCH partially changes, and
   DELETE removes or cancels.
6. State changes use the resource method, a singleton subresource, or an
   auditable job resource. Literal action verbs are rejected.
7. `GET /me` is the sole authenticated identity-discovery projection for a
   client that does not yet know its principal ID. After discovery, profile and
   child resources use the same `/principals/{principal_id}/...` contract for
   self-service and administration. `/me` is not a second resource tree.
8. Paths carry trusted ownership context only where no existing resource can
   resolve it. Clients never assert owner type or owner ID in headers or query
   parameters.
9. OAuth/OIDC, SAML, and SCIM protocol paths follow their specifications when
   those specifications require a different contract.
10. Every conclusion is labeled `MODEL_CONFIRMED`, `CONTRACT_CONFIRMED`, or
    `PRODUCT_DECISION_REQUIRED`. Unknown semantics block a canonical route
    recommendation; they are not filled in by analogy.

### 2. Uniform And Reusable Module Interfaces

1. Equivalent modules use the same collection/item CRUD shape, ID naming,
   pagination, filtering, sorting, optimistic concurrency, error envelope, and
   authorization mechanism.
2. One resource has one canonical representation unless a second projection
   has materially different fields, media, authorization, or performance
   semantics. Convenience-only duplicate endpoints are rejected.
   Approved global/scoped dual addresses share that representation, service
   implementation, authorization, version, and audit behavior; they are two
   addresses, not two resource contracts.
3. Shared request parameters and response schemas are reused components, not
   near-duplicate module-local shapes with different names.
4. Existing-resource authorization reuses owner resolvers. Scoped collection
   authorization reuses the matching tenant/entity/department evaluator.
5. One-time challenges, deliveries, verifications, and acceptances may remain
   separate resources because issuance and consumption have different replay,
   abuse, expiry, and audit semantics.
6. Hyphenated compound segments are minimized by correct ownership nesting and
   reusable child resources. A compound noun remains only when splitting it
   would change meaning or invent a false parent resource.
7. Public abbreviations such as `authz`, `pwd`, `cfg`, and `ops` are rejected.
   Shorter is not better when it makes the resource ambiguous.

### 3. Clear Swagger Information Architecture

1. Swagger tags describe stable user work domains. They do not mechanically
   repeat the first URL segment; URL ownership and documentation grouping are
   separate concerns.
2. A tag with one operation is rejected unless the operation is an external
   protocol or operational standard that cannot honestly join another domain.
3. A tag must not become a catch-all merely because many paths begin with the
   same owner, such as `/tenants` or `/me`.
4. Every tag has a clear description, bounded responsibility, and predictable
   audience. Operations are ordered by resource lifecycle inside the group.
5. The generated Swagger is reviewed as a navigable product surface, not only
   validated as syntactically correct JSON.

## Contract Acceptance Checklist

Every changed operation must satisfy all of the following before acceptance:

- URI is a plural resource collection, a globally identified item, an honest
  singleton, a relationship, or a representation.
- Method semantics and success status match the resource transition.
- Path, query, header, and body parameters have one authoritative source; the
  same owner or destination is not submitted twice.
- IDs use precise names and the shared snowflake schema where applicable.
- List endpoints use the shared pagination/filter/sort contract unless an
  explicit protocol prevents it.
- Mutations use one consistent optimistic concurrency mechanism where lost
  updates are possible.
- Request and response schemas reuse shared components where semantics match.
- Authorization is derived from trusted resources and reuses the established
  resolver/evaluator instead of introducing route-specific permission logic.
- Operation belongs to one meaningful Swagger domain tag; the resulting tag is
  neither an unexplained singleton nor an owner-path catch-all.
- Generated OpenAPI, exact route manifests, clients, docs, tests, and real E2E
  change together.

## Resource Semantic Registry

The registry is the input to route design. It prevents an existing path or
database key from silently defining the public resource.

| Resource | Meaning / owner | Identity and lifecycle evidence | Review result |
| --- | --- | --- | --- |
| Principal | Global login identity; no tenant owns it | `iam_principals.id`; profile, credential, global status, and erasure lifecycle | `MODEL_CONFIRMED`: canonical global resource |
| Tenant membership | Relationship between one tenant and one principal; contains status and tenant-local display/email fields | Current model key is `(tenant_id, principal_id)` and has its own timestamps/status; no independent ID | `PRODUCT_DECISION_REQUIRED`: decide whether tenant-local fields are true overrides or denormalized snapshots, then decide composite versus stable membership identity |
| Department | Tenant directory resource with optional entity association | `tenant_id` required, `entity_id` optional, stable department ID | `MODEL_CONFIRMED`: tenant collection is valid; the earlier entity-owner proposal is rejected |
| Role member | Relationship between role and principal | Composite `(tenant_id, role_id, principal_id)` with no separate lifecycle beyond assignment | `MODEL_CONFIRMED`: principal ID can identify the relationship item under one role; parameter must be named `principal_id` |
| Group/position member | Relationship between group or position and principal, with optional validity interval | Composite parent/principal identity | `MODEL_CONFIRMED`: existing parent collection plus `{principal_id}` item shape is valid |
| Authorization relationship | Tuple-based grant between subject and resource | Seven-part composite identity; validity/condition fields; no stable ID | `PRODUCT_DECISION_REQUIRED`: item addressing cannot be settled by URI taste alone |
| Account closure | One workflow state per principal, not a history collection | `principal_id` is the persisted primary key; status/version/timestamps; completion derived from lifecycle | `MODEL_CONFIRMED`: principal-owned singleton; do not invent `closure_request_id` without adding history semantics |
| File | Globally identified logical entry with a verified owner scope | Stable file ID; owner resolver derives principal/tenant/entity/department context | `MODEL_CONFIRMED`: scoped root collections plus global item routes |
| File operation | Synchronous copy/move targeting an existing destination directory | Destination and sources are global file IDs; no durable operation record today | `MODEL_CONFIRMED`: destination-owned execution endpoint is reusable, but do not claim a durable job resource |
| Session | Opaque authentication session owned by a principal; one session is bound to the current credential | Opaque session ID plus current-session projection | `MODEL_CONFIRMED`; exact placement of the current-session alias is part of the authentication transaction design |
| Login ceremony | Multi-step password/MFA/passkey transaction that may precede known principal identity | Current routes expose stages but no common transaction resource | `PRODUCT_DECISION_REQUIRED`: redesign only as one transaction model |
| Verification/recovery challenge | Expiring one-time credential issuance | Separate issue and consume lifecycles with replay/abuse controls | `MODEL_CONFIRMED`: challenge and verification/recovery are distinct resources |

## Scan Evidence

The revised scan used generated contract SHA-256
`aaa36b0654814d3c1ee050848cfed86026da14b15a7f4d09f15959b9a439ebda`.
It enumerated every application method rather than sampling tags:

- 206 paths and 295 GET/POST/PUT/PATCH/DELETE operations reconciled;
- all 33 current tags reconciled;
- 51 operations use `/me` or a nested `/me` projection;
- 84 operations are grouped under `tenants`, 47 under `me`, while five tags
  contain one operation and five contain two;
- 46 distinct inline path-parameter definitions exist;
- ordinary offset pagination has four different `limit` schemas and two
  `offset` schemas; file cursor pagination is a legitimate separate contract;
- Snowflake-shaped path IDs use inconsistent 19, 64, or 128 character bounds;
- `subject` is documented and validated as an immutable principal ID;
- `GET /guid*` mutates allocation state;
- three file-operation paths publish the same handler contract;
- tenant principal operations mix principal, membership, onboarding, and global
  suspension semantics;
- protocol and probe routes were separated before applying application REST
  rules.

## Executive Decision Set

### Recommend Changing

| Priority | Current | Recommended | Reason |
| --- | --- | --- | --- |
| P0 | three scoped `POST .../file-operations` routes | `POST /files/{destination_file_id}/operations` | The destination is an existing globally identified directory and is the natural ownership and authorization boundary. Remove `destination_id` from the body. |
| P0 | 33 tags mechanically derived from the first path segment | 13 workflow-oriented tags defined below | The current Swagger is both fragmented (10 tags have at most two operations) and overloaded (`tenants` has 84 operations, `me` has 47). |
| P0 | `GET /guid`, `GET /guid/{count}` | remove from the public application API; if an external allocator is genuinely required, use `POST /identifiers` and `POST /identifier-batches` | GET must not allocate state. No Web client currently consumes these routes. |
| P1 | `POST /password-recoveries/requests` | `POST /password-recovery-challenges` | The created resource is an expiring recovery challenge; `request` only nominalizes the caller action. |
| P1 | `POST /email-verification-requests` | `POST /email-verification-challenges` | The created resource is an expiring verification challenge, not an HTTP request represented as application data. |
| P1 | both `GET /roles/{role_id}/permissions` and `/permission-grants` | keep only `GET /roles/{role_id}/permission-grants` | The detailed representation is a strict superset; the Web client already consumes it and can derive codes. |
| P1 | Todo `/export`, `/import`, `/import-template` routes | `GET /todo-exports`, `POST /todo-imports`, `GET /todo-import-template/content` | The example module currently teaches command-style routes that the approved routing design explicitly rejected. |
| P1 | platform account-closure collection and principal-keyed update use different names and hierarchies | choose one canonical principal-owned account-closure representation for both self-service and administrators | `MODEL_CONFIRMED`: the persisted closure is a one-per-principal singleton keyed by `principal_id`, not a historical request with its own ID. |
| P2 | `GET /scope-model` | `GET /authorization-scope-model` | The current name is too generic for a public top-level resource and its summary leaks deployment-specific scope names. |
| P2 | `POST /tenants/{tenant_id}/authorization-constraint-evaluations` | `POST /tenants/{tenant_id}/authorization-constraints` | The response is an authorization constraint; `evaluations` repeats what POST already expresses. |
| P1 | `GET /me/personal-token-permissions?tenant_id=...` | remove it with no token-specific replacement | Token creation validates submitted permissions against the selected principal's effective, delegable permissions. |
| P1 | every `/me/*` child plus `/tenants/{tenant_id}/me/*` and entity-scoped `/me/*` | keep only `GET /me` for identity discovery; move profile and child resources to `/principals/{principal_id}/...` | Self-service and administration must reuse one canonical resource contract. The principal ID selects the target; authorization decides whether the caller may act on self or another principal. |
| P1 | `/me/closure-request` | `/principals/{principal_id}/account-closure` with GET/PUT/DELETE | Account closure belongs to the principal resource. `request` exposes an internal workflow action. |
| P0 | `/tenants/{tenant_id}/principals*` | remove this incorrectly named family after resolving the membership identity decision below | `MODEL_CONFIRMED`: it lists principals through membership, creates principal plus membership, edits the global principal, and performs global suspension. One URI family hides several resources and scopes. |
| P1 | relationship DELETE selected by eight query parameters | define a canonical relationship item identity before replacing it | `MODEL_CONFIRMED`: the model currently has a seven-part composite key and no ID. `PRODUCT_DECISION_REQUIRED`: preserve that identity in an encoded item key or add a stable relationship ID. |
| P1 | role and tenant-member paths use `{subject}` for a value documented as a principal ID | rename it to `{principal_id}` if the item remains a principal-keyed relationship | `CONTRACT_CONFIRMED`: the schema accepts only a Snowflake-shaped local principal ID; `subject` is needlessly ambiguous. |
| P1 | 46 distinct inline path-parameter definitions and six pagination parameter variants | introduce shared Snowflake, opaque-ID, offset/limit, and cursor components | `CONTRACT_CONFIRMED`: equivalent parameters currently differ in length, pattern, defaults, and limits without documented semantic reasons. |

## Swagger Information Architecture

### Current Problem

The current contract enforces `tag == first public path segment`. That rule
produces a technically deterministic but poor documentation structure:

- 33 top-level Swagger groups for 295 operations;
- five groups contain exactly one operation and five contain exactly two;
- `tenants` contains 84 operations from unrelated domains;
- `me` contains 47 operations spanning profile, security, tokens, files,
  memberships, consent, and lifecycle;
- resource-oriented paths such as `email-verification-requests` become isolated
  groups even though users naturally look for them under authentication.

The rule should be replaced. A path names the resource; a tag answers where a
human looks for that operation.

### Recommended Swagger Tags

| Tag | Includes |
| --- | --- |
| `system` | health/readiness and any approved identifier allocation API |
| `authentication` | registration, login, MFA, passkey ceremonies, verification, recovery, and session establishment |
| `account` | `GET /me` discovery plus principal profile, password, sessions, passkeys, identity links, personal tokens, consent, memberships, and account closure |
| `directory` | tenants, principals, members, entities, departments, groups, positions, invitations, and service accounts |
| `access-control` | roles, grants, relationships, permission catalogs, field policies, authorization checks/explanations/constraints, access requests, and reviews |
| `files` | every personal, tenant, entity, and department file operation |
| `applications` | OAuth clients, identity providers, SAML service providers, SCIM directories, and outbound SCIM targets |
| `audit` | tenant/platform audit events, archives, policies, anchors, signatures, exports, and verifications |
| `platform` | platform administrators and global principal lifecycle administration |
| `oauth` | externally specified OAuth/OIDC protocol endpoints only |
| `saml` | externally specified SAML protocol endpoints only |
| `scim` | externally specified inbound SCIM protocol endpoints only |
| `todos` | the optional example module, isolated from production domains |

This produces 13 predictable Swagger sections. Tags remain flat because Swagger
UI does not natively render nested tag groups consistently. A renderer that
supports `x-tagGroups` may additionally present these sections under Identity,
Resources, Governance, Protocols, and Examples, but correctness must not depend
on that vendor extension.

Each tag must have an OpenAPI top-level description. Operation IDs remain stable
unless the operation itself changes. The architecture test should validate the
approved tag vocabulary and operation-to-domain mapping instead of comparing a
tag to the first path segment.

### Path Readability

Hyphenated compound nouns are not inherently a defect. A path is changed only
when segments repeat the same concept or place a resource below the wrong
owner. For example:

- use `/email-verification-challenges`: it names the short-lived credential
  resource rather than nominalizing the action of requesting an email;
- keep `/service-accounts/{service_account_id}/credentials/{credential_id}`:
  its length expresses real ownership and avoids ambiguous credential IDs;
- shorten `authorization-constraint-evaluations` to
  `authorization-constraints`: POST already communicates evaluation;
- remove the personal-token-specific permission endpoint without inventing a
  replacement resource family;
- replace scoped file-operation paths with a destination-owned child resource.

Avoid cosmetic abbreviations such as `authz`, `pwd`, `cfg`, or `ops` in public
application paths. They save characters but reduce clarity and searchability.

### Recommend Keeping

| Contract | Decision |
| --- | --- |
| `/principals/{principal_id}/files`, `/tenants/{tenant_id}/files`, `/entities/{entity_id}/files`, `/departments/{department_id}/files` | Keep as the four explicit owner-scoped collections; replace current `/me/files` with the principal form. |
| `/files/{file_id}` and its `items`, `content`, and root-relative path representations | Keep. Existing resources are correctly addressed by global file ID. |
| email verification challenge and verification collections | Keep separate after renaming. Issuing and consuming credentials have different abuse, replay, and audit semantics. |
| invitation collection, deliveries, and invitation acceptances | Keep separate. Administrator management, credential delivery, and unauthenticated acceptance are different security boundaries. |
| account closure lifecycle | Keep as `/principals/{principal_id}/account-closure`, one principal-owned singleton using GET/PUT/DELETE with self/admin authorization and clear impact text. |
| access-request decisions, withdrawals, revocations, and access-review item decisions | Keep. These are auditable singleton resources, not arbitrary action suffixes. |
| audit archives, attempts, verifications, exports, signatures, and anchors | Keep. They represent durable jobs or immutable evidence. |
| credentials below OAuth clients, service accounts, and SCIM directories | Keep. Credentials have distinct lifecycle and one-display secret semantics. |
| principal and SCIM-target suspension/retirement singleton resources | Keep. PUT/DELETE or resumable singleton semantics are explicit and idempotent. |
| all OAuth/OIDC, SAML, and `/scim/v2` protocol routes | Keep unchanged. External specifications and configured callback URLs outrank local naming preference. |
| health, liveness, and readiness probes | Keep unchanged. They are operational standards, not application resources. |

## Authentication Flow Assessment

The login family is inconsistent with the more resource-oriented reauthentication
family, but changing it now does not produce a clearly superior contract.

- `POST /login`, `POST /login/mfa`, `POST /login/passkey/options`, and
  `POST /login/passkey/verify` describe a ceremony understandably to UI clients.
- Replacing them with generic `/sessions` and `/verifications` would reduce
  verbs but hide which credential ceremony is being performed.
- WebAuthn options and assertions are two protocol stages and must remain
  separate even if renamed.

Recommendation: do not cosmetically rename individual login routes. Design one
`/authentication-transactions` state machine that covers password, MFA,
passkey, return URL binding, expiry, replay protection, and session creation.
Until that complete contract is approved, classify the current login family as
a known REST exception, not as a contract that passed this review.

## Current-Principal Migration Ledger

All 47 operations currently tagged `me`, plus the four nested tenant/entity
`me` operations, are accounted for by resource semantics:

| Current family | Resource result |
| --- | --- |
| `GET /me` | Keep as identity discovery. |
| `/me/avatar*` | Principal-owned avatar and candidate resources. |
| `/me/closure-request` | Principal-owned `account-closure` singleton; use resource methods, not request wording. |
| `/me/files`, `/me/file-operations` | Principal-owned file collection; operation execution consolidates by destination file. |
| `/me/identity-link-*`, `/me/identity-links*` | Principal-owned identity-link resources and transactions. |
| `/me/mfa*`, `/me/password`, `/me/passkey*` | Principal security resources; self/admin share URI while fresh-authentication policy remains operation-specific. |
| `/me/oauth-consents*` | Principal-owned application consent resources. |
| `/me/personal-token-preparations`, `/me/personal-tokens*` | Principal-owned token lifecycle; preparation remains a short-lived security resource. |
| `/me/personal-token-permissions` | Remove; token creation performs authoritative delegation validation. |
| `/me/reauthentication/*` | Principal-bound security transaction resources. |
| `/me/session*` | Session resources; current-session alias awaits the complete authentication transaction contract. |
| `/me/memberships`, `/me/memberships/{tenant_id}`, `/me/tenants` | Membership projections; exact canonical item shape waits for the membership semantic decision. Remove duplicate tenants projection if membership representation contains the required tenant summary. |
| `/me/permissions`, nested tenant/entity `/me/permissions` | One target-principal authorization projection with explicit evaluated context; no nested `me`. |
| `/tenants/{tenant_id}/me/menus` | Target-principal effective-navigation projection in tenant context; no nested `me`. |
| `/tenants/{tenant_id}/me/access-requests` | Target-principal access-request filter in tenant context; no nested `me`. |

This ledger defines ownership, not final spelling for unresolved projections.
The implementation plan must not replace `/me` mechanically with a principal
prefix until each request/response schema has passed the semantic gate.

## Detailed Findings

### File Operations

The three current routes are emitted from one handler by
`RegisterTenantEntityOrAuthenticated`. Their bodies and results are identical;
only the outer claims context differs. The operation already contains globally
unique source IDs and a globally unique destination ID.

Recommended contract:

```http
POST /files/{destination_file_id}/operations
Idempotency-Key: ...

{
  "action": "copy | move",
  "sources": ["..."],
  "file_policy": "...",
  "directory_policy": "...",
  "type_policy": "...",
  "observed_revision": 1,
  "decisions": []
}
```

The route must use the existing file-owner resolver before business execution.
The service must still authorize every source, require source and destination
to share the permitted internal space, and derive audit tenant/entity IDs from
trusted ownership. A route-only rename without these invariants is rejected.

### Identifier Allocation

The GUID endpoints allocate values and therefore are not reads. They are also
unused by the current Web client. The preferred outcome is to remove them from
the public application document and retain the generator as an internal module
capability. Only a demonstrated external consumer justifies the POST-based
replacement.

### Role Permission Representations

`/permissions` returns only strings. `/permission-grants` returns each
`permission_code` plus condition and creation metadata. Maintaining both creates
two representations with overlapping cache, authorization, test, and client
contracts. Keep the detailed resource and derive code lists at the caller.

### Principal And Tenant Membership

The current tenant principal family cannot survive as a canonical REST family:

- list filters global principals through tenant membership but names the result
  as tenant-owned principals;
- create atomically creates a global principal, credential, and membership;
- GET/PATCH return and modify the global principal profile;
- PATCH also copies display name and email into only the selected tenant member;
- suspension changes global principal state and revokes global sessions.

This is a semantic collision, not merely duplicated routing. The confirmed
target boundary is:

```http
GET /me                                      # identity discovery only
GET|PATCH /principals/{principal_id}         # global principal
PUT|DELETE /principals/{principal_id}/suspension
GET|POST /tenants/{tenant_id}/memberships    # tenant relationship collection
```

The membership item URI is deliberately not proposed yet. The current composite
key would support a principal-keyed relationship item, while its status and
tenant-local fields may justify a stable membership ID. That choice depends on
the unresolved meaning of those local fields. Direct tenant onboarding may stay
atomic, but its response must identify both the created principal and membership
instead of pretending they are one resource.

### Password Recovery

`/password-recoveries/requests` makes an action look like a child of a recovery
that does not exist yet. Name the created security resource:

- `POST /password-recovery-challenges` issues a credential without disclosing
  account existence.
- `POST /password-recoveries` consumes the credential and changes the password.

This matches the already-correct email verification model.

### Closure Lifecycle

The lifecycle should be named `/principals/{principal_id}/account-closure`;
closure is the singleton state and `request` is an internal workflow step. The
same route serves self and authorized administrators. The model proves there is
exactly one persisted record per principal and no request history, so adding a
`closure_request_id` would invent semantics the product does not have.

The remaining design choice is the administrative queue representation. It may
list principal-owned account-closure singletons, but it must not introduce a
second item URI or a second schema. If historical requests are wanted later,
that is a separate data-model change rather than a naming migration.

### Relationship Grants

`/tenants/{tenant_id}/relationships` has a seven-part composite business
identity rather than a stable relationship ID. POST upserts the relationship,
while DELETE reconstructs an item through eight query parameters. The current
contract does not provide an item URI and therefore fails the uniform CRUD
rule. The scan does not guess the replacement: product/data design must choose
either a canonical encoded composite item identity or a new stable relationship
ID. Until then this family is explicitly unresolved.

### Lifecycle And Long-Running Work

- Principal anonymization is an irreversible singleton transition guarded by
  retention, legal hold, suspension, version, and login-name proof. Its current
  `PUT .../anonymization` shape is explicit and acceptable.
- SCIM target retirement is resumable and blocks competing writes. Its current
  singleton shape is acceptable; converting it to a generic job would only be
  better if asynchronous execution and job inspection are introduced together.
- Audit retry attempts and verifications already use durable job resources and
  should not be collapsed into action endpoints.

## Exhaustive Tag Coverage Ledger

Every current tag and every operation assigned to it is covered below. `Keep`
means the current resource semantics passed; it does not preserve the current
Swagger tag because grouping is redesigned separately.

| Tags | Result |
| --- | --- |
| `access-requests`, `access-reviews` | Keep the persisted request/review, decision, withdrawal, revocation, campaign, and item-decision resources. |
| `departments` | Keep tenant collection and global-ID items. `MODEL_CONFIRMED`: entity association is optional, so do not move the whole collection under entities. |
| `entities`, `groups`, `positions`, `menus`, `service-accounts` | Keep owner-scoped collections and global-ID items; rename ambiguous principal parameters where present. |
| `files` | Keep item routes; change operation creation as specified |
| `roles` | Remove duplicate code-only permissions projection |
| `email-verification-requests`, `email-verifications` | Rename issuance to a challenge; keep consumption separate. |
| `registrations` | Keep registration and captcha resources; retain 202 for pending registration. |
| `invitation-acceptances`, `invitations` | Keep invitation administration, delivery, and acceptance separate. |
| `password-recoveries` | Rename issuance as a challenge resource |
| `login` | `PRODUCT_DECISION_REQUIRED`: replace only through one complete authentication-transaction design; current action paths do not pass the REST rule. |
| `me` | Keep only `GET /me`; relocate each child according to its proven principal, session, file, or authentication-transaction resource. Remove token-specific permission discovery. |
| `tenants` | Keep valid tenant-owned collections; remove file-operation duplicates, nested `me`, and the semantically mixed tenant-principal family. Relationship item identity remains unresolved. |
| `platform` | Keep administrator and audit resources; unify global principal lifecycle under canonical principal routes and treat account closure as a principal-keyed singleton. |
| `identity-providers` | Keep; callbacks are externally configured protocol boundaries |
| `oauth`, `oauth-clients` | Keep |
| `saml`, `saml-service-providers` | Keep |
| `scim`, `scim-directories`, `scim-targets` | Keep |
| `principals` | Keep avatar representation and expand this into the one canonical global principal tree after authorization review. |
| `scope-model` | Rename for domain clarity |
| `guid` | Remove publicly or replace with POST allocation resources |
| `health` | Keep |
| `todos` | Correct the example import/export contract |

## Proposed Optimization Plan

### Phase 1 - Enforce The Contract Rules

1. Add the resource semantic gate and evidence labels to the OpenAPI contract
   tests and review checklist.
2. Replace first-segment tags with the approved 13-domain Swagger vocabulary.
3. Add shared OpenAPI components for Snowflake IDs, opaque IDs, pagination,
   optimistic concurrency, and common errors.
4. Reject ambiguous `{subject}` where the value is a principal ID and reject
   inconsistent Snowflake bounds.

This phase changes documentation structure and parameter schemas, not resource
behavior.

### Phase 2 - Model-Confirmed Contract Corrections

1. Keep only `GET /me` as identity discovery and move model-confirmed principal
   children to the canonical principal tree.
2. Consolidate the three file-operation routes by destination file while
   preserving owner resolution and source authorization.
3. Rename verification and recovery issuance as challenge resources.
4. Model account closure as a principal-owned singleton; reuse it for self and
   authorized administrators.
5. Remove the personal-token permission catalog and duplicate role permission
   projection.
6. Remove public GET-based GUID allocation unless an external consumer is
   demonstrated.
7. Correct Todo import/export/template resources and the authorization scope
   naming issues.

### Phase 3 - Resolve Semantic Decisions Before Routing

1. Tenant membership: decide whether tenant-local display name/email are
   intentional overrides or snapshots; then select composite or stable
   membership identity and replace `/tenants/{tenant_id}/principals*`.
2. Authorization relationship: preserve a documented composite item identity
   or add a stable relationship ID; then replace query-selected DELETE.
3. Login/current session: design one authentication transaction state machine
   before replacing any individual login route.
4. Role directory bindings: decide whether group and position assignments are
   separate relationship collections or one first-class binding resource.

No Phase 3 URI is approved until its resource card is complete.

### Phase 4 - One Breaking Migration

After Phases 1-3 are confirmed, change server declarations, exact route
manifests, authorization and field-filtering tests, generated OpenAPI, generated
TypeScript, Web calls, documentation, and multi-account browser E2E together.
The pre-production contract publishes no legacy aliases beside canonical paths.

## Confirmation Requested

Recommended confirmation:

1. Approve the semantic gate and 13-domain Swagger grouping as permanent rules.
2. Approve Phases 1 and 2 as the confirmed optimization scope.
3. Confirm that Phase 3 must return with explicit resource cards before any of
   those routes are implemented.
4. Defer runtime API changes until the exact final route manifest is generated
   from the confirmed decisions and reviewed once more.
