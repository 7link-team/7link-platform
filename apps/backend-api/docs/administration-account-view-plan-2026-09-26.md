# Administration Layers and Account Views

## Goal and Requirements

### System Foundation Completion Review

User acceptance reopened missing system menu and audit-event management. Review
approved reuse of existing menu/audit engines with explicit global guards:

- System menu storage uses an internal generated-ID root with scope_kind=system
  and reserved slug __system__. Existing slug uniqueness makes creation atomic;
  public slug validation excludes this name. No membership or role is created.
- System menu CRUD and effective navigation share persistent records and atomic
  hierarchy placement. Canonical page permission floors survive menu edits.
- System audit list/detail/export/integrity always use partition0. They expose
  actual global events, not an implicit cross-tenant stream.
- Global system_menu_* and system_audit_event_* actions permit restricted grants;
  full system administrators acquire declared actions automatically.
- System access grants manage existing accounts' global authority; global account
  administration continues to manage identity/profile/status.
- Reuse existing shared pages and transport. Verify actual desktop/mobile system
  tree edits, query/export, owner isolation and actual operator attribution.

Completion verification (2026-09-27):

| Gate | Result | Evidence |
| --- | --- | --- |
| Backend core/organization/audit/app | PASS | Combined `go test` run; system ownership, bootstrap, route guards and operator attribution regressions |
| Composed API contract | PASS | 298 exact operations/guards; OpenAPI and frontend type drift checks |
| Frontend | PASS | 145 tests, typecheck, lint and build |
| Real MySQL8 | PASS | `make test-mysql8-run`; lifecycle, account views, migrations and unchanged enforced performance budgets |
| Published schema lock | PASS | 246 migration files unchanged |
| Retained demo Chrome | PASS | Chinese full-admin sidebar eight leaves, menu/audit/grant pages without alerts, zero uncaught errors |
| Final focused browser | PASS | 17/17 real flows, zero mocks/page errors; `.temp/sqlite-lifecycle-vvnTDp/report.json`, 40 desktop/mobile screenshots |
| Full browser lifecycle | PASS | 38/38 flows, zero mocks/page errors; `.temp/sqlite-lifecycle-n72jTp/report.json`, 47 screenshots |

Self-review repaired narrow menu columns and kept row commands visible during
horizontal scrolling; actual desktop/mobile screenshots inspected. Concurrent
bootstrap tests reproduced GORM's candidate-ID predicate and simulated MySQL
foundRows ambiguity; a fresh locked stored-root lookup now prevents duplicate
seeding. Full affected suites and rebuilt retained-demo startup passed afterward.
Final visual review removed the empty readonly Actions column. The final focused
browser run and frontend gates passed afterward; root inspected the corrected
readonly mobile screenshot. Readonly UI retains its existing export suppression;
ordinary restricted audit export and backend readonly admission were verified.
Self-review and original-requirement acceptance: PASS. No engineering work or
unresolved decision remains. Earlier evidence below belongs to the previous
delivery.

Manual acceptance: http://127.0.0.1:8091/login, select the system superadmin
admin@example.test. Verify System access grants, Menus, Audit events and Audit
governance in the system workspace. Menu manager and sidebar share stored
records; restart preserves the 11 default nodes and subsequent edits. Retained
demo remains running; test services and generated temporary artifacts are excluded
from the delivery commit.

Separate system administration, platform administration and tenant administration.
System administrators manage system subadministrators, platform administrators,
tenant administrators/members and global users. Preserve global accounts and
independent tenant memberships from D88. Business modules and creation quotas
are not part of this change.

An administrator can open a selected account's authenticated workspace through
a dedicated, recorded token. The default mode is read-only; writable mode is
an explicit selection. The target account's own session and credentials are
unchanged. The target receives no view-session login notification or entry in
its normal browser-session list. Actual writes still change business data.
Operator attribution must survive every write; records must not misrepresent
the target as the actual operator.

## Existing Implementation and Evidence

- `internal/iam/core/authorizer.go:CheckPlatform` recognizes only platform actions.
  Full platform administrators satisfy every platform action.
- `CheckBulk` also expands full platform administration to tenant permissions,
  after active membership is established. This is an authorization change to
  review explicitly when separating the system and platform layers.
- `internal/iam/core/platform.go` already implements full/restricted platform
  grants, transactional audit and the last-full-administrator invariant.
- `pkg/extension/authz/register.go:authenticate` is shared by tenant, entity,
  platform, owned-resource and authenticated registrations. Session-only routes
  reject Authorization headers before verification and need separate review.
- `internal/iam/authn/web.go:Authenticate` dispatches JWT, personal token and
  browser session authentication. Browser-session verification touches its
  last-seen/expiry fields. A target browser session cannot be reused for views.
- `internal/iam/audit/audit.go:NewDomainEvent` is reused by IAM and file writes.
- `apps/iam/src/lib/navigation.ts` supplies platform, tenant, entity and personal
  catalogs; there is no separate system workspace.

## Clarified Operating Model

The user clarified that one deployment can have independently operated platforms,
with different menus/views and department/role-based authorization. Platform is
therefore an explicit operating scope, not merely another label for global system
administration. Global accounts can hold separate grants in each scope.

Dedicated view tokens record the real operator and viewed account, selected
workspace and mode. Default is read-only; selected writable mode permits real
operations. In keeping with the requested account's logged-in view, target grants
and data scope determine that view's permissions; operator identity determines
audit attribution. System administration remains a separate authority.
No normal target login/session or notification is created. Business mutations
remain real and can naturally be seen in the resulting data.

Implementation resumes; the earlier unanswered-question blocker is superseded
by this clarification. No additional question or approval is required for the
dedicated record and identity-isolation work.

## Design and Reuse

Reuse global principals, tenant memberships, permission registry, registrar,
credential hashing, transaction coordinator, audit appender and existing pages.
Do not create separate password-bearing account types for each administrator.

Add an explicit system authority and workspace. Administration screens expose
accounts by their actual grants and memberships; the same account may appear
in several authorized contexts. Platform operators cannot grant system access.
Preserve the last-system-administrator invariant and review existing bootstrap
and administrator migration before changing any grant.

Authn owns view-token records: hashed secret, operator, target, selected scope,
mode, issue/expiry/revocation timestamps and operator-session binding. Return
the secret only on creation; never store it in URLs or audit details. Validate
operator authority, account status, record expiry/revocation and selected scope
on every request. Do not turn a view token into an OAuth authorization, personal
token, ordinary target login or nested account view.

Keep target identity for ownership and normal target permission evaluation;
keep operator identity separately for attribution. Re-evaluate target permissions
after grant/membership changes. Do not copy the operator's authentication strength
into the target's conditional grants as though the target authenticated.

Render the existing target workspace using an isolated administrator view session.
Show mode and exit controls to the operator. Backend enforcement remains decisive.
Session-only security pages, reauthentication, logout and credential issuance
require explicit treatment rather than a blanket session-only bypass.

## Review Findings and Implementation Gates

### Architecture and Security Review: 2026-09-26 Continuation

Architecture and security were independently reviewed against the current source.
The existing global `CheckPlatform` predicate and administrator tables represent
deployment-wide system authority. They must not authorize a selected operating
platform. The implementation contract is:

1. Add an immutable `scope_kind` (`tenant`, `platform`, `system`) to the existing
   organization root. Preserve physical owner IDs and existing `tenant_id` foreign
   keys so departments, groups, roles, memberships, menus and policy locks reuse
   the current engine. Existing roots remain tenants. Ordinary tenant APIs and
   tenant admission must reject administrative roots.
2. Introduce explicit system and selected-platform registrations/checkers and
   catalogs. Move current global management to system authority with an explicit
   legacy-grant migration. Never copy old global grants into every operating
   platform. Platform roles do not accept system or tenant business permissions.
3. Do not impose a one-platform-per-tenant ownership relationship. Independent
   operating platforms require their own organization and grants; associating
   business tenants with platforms is not required to establish that isolation.
   No association may silently confer tenant membership or entity data access.
4. Account views bind one immutable scope. Tenant views admit their own entity
   descendants; entity views do not admit parent tenant administration. System
   views admit system operations only. Personal views bind the target principal.
   Globally addressed resources compare their resolved owner, not their URL.
5. The service requires an injected authority/admission checker on issue and
   every use. Operator browser-session assurance may evaluate operator authority;
   target claims receive no copied ACR, AMR, client grant or authentication time.
   Principal status, credential versions, session expiry, revocation and current
   target admission are rechecked. Missing capabilities fail closed.
6. Issue and revoke records with audit in one transaction. Secrets use an `avt_`
   discriminator and 32 random bytes and are returned once. Maximum lifetime is
   30 minutes, clipped to the operator session's current and absolute expiries.
   Reject nested views. Token activation follows audited route admission.
7. OAuth userinfo and introspection explicitly reject view identity even if the
   claims otherwise match an OAuth client. Cookie logout rejects Authorization
   before reading a cookie; view exit revokes only the dedicated record.

Review result: the contract above resolves the structural design findings.
Implementation activation remains gated by personal-route inventory, operator
audit on every allowed mutation, exact-kind role/catalog checks, grant migration,
workspace UI and actual browser E2E. Intermediate test passes do not satisfy
these gates or the full delivery criteria.

Architecture: new system grants must be distinct from platform and tenant grants;
workspace catalogs must match management trees, including built-in entries.
Security: HTTP method rejection alone is insufficient. Inventory custom/public
authentication paths and any GET handlers with writes before enabling tokens.
Audit: creation, mode selection, use, revocation and resulting mutations need
operator/view/target correlation. Review existing direct event construction and
protocol telemetry as well as NewDomainEvent consumers.
Acceptance: no runtime token issuance exists yet. Foundation unit tests cannot
prove session isolation, real token revocation or browser correspondence.

### Authentication Path Inventory

| Path / source | Verified behavior | Required view treatment |
| --- | --- | --- |
| Authn `/me` | Handler formerly authenticated twice; now reuses verified context | Implemented and tested: exactly one authentication, target subject retained |
| `/sessions/current` | Browser-session-only registration | Represent the operator view session without inserting a target login |
| Personal session/MFA/passkey reads | Services accept raw cookie/authorization inputs | Ensure reads select the target; do not touch a target browser session |
| Personal token, avatar, closure, OAuth application reads | Several require browser-session-only registration | Explicitly allow verified dedicated view reads without allowing ordinary bearer substitution |
| Personal identity links/providers | Normal reads authenticate raw cookie; view reads now use trusted target context | Service implementation tested with operator/invalid/missing cookies and unchanged session rows; HTTP token admission still pending |
| GET identity-continuation | CompleteIdentityOperation can write bindings or finish reauthentication | View access explicitly denied by route metadata, tested in shared registrar for both modes |
| OAuth userinfo/introspection and federation protocol endpoints | Authentication outside the shared registrar also exists | Dedicated view tokens must not become protocol credentials |

The GET continuation write is verified in
`internal/iam/federation/identity_links.go:CompleteIdentityOperation`, including
binding updates. Personal identity read cookie authentication is verified in
`ListPersonalIdentityLinks` and `ListLinkableProviders`. Token activation remains
blocked on implementing these concrete treatments, not merely passing method tests.

## Implementation Sequence

1. Implement and test separate operator context and audit provenance.
2. Implement and test central read-only rejection; inventory bypass paths.
3. Apply the clarified independent-platform and target-authority model.
4. Implement system authority, account administration APIs and matching pages.
5. Implement persistent dedicated tokens, revocation and complete route guards.
6. Integrate target workspaces and modes without touching target sessions.
7. Run isolated real backend/browser E2E across all administrator and user views.

## Acceptance Criteria

- System/platform/tenant/account menus and APIs match their ownership and grants.
- System administrator can manage every requested account category; lower layers
  cannot promote themselves or grant authority outside their scope.
- Every target workspace opens with its actual account identity and navigation.
- Default read-only rejects actual mutations through UI and direct HTTP calls.
- Selected writable mode has the confirmed authority and records the operator.
- Expired/revoked tokens and disabled operators/targets fail; grant changes apply.
- Target browser sessions, credentials, login state and notifications are unchanged.
- Every view token has a durable record; tokens are absent from URLs and logs.
- Actual E2E proves all preceding criteria with isolated data; screenshots cover
  desktop/mobile, every administrator scope, ordinary users and exit restoration.

## Current Status
### Implementation and Review

System account CRUD and named workspace discovery, independently operated platform
CRUD/bootstrap/local RBAC/directory/menus, dedicated token issuance/use/revocation
and separate frontend workspaces are implemented. Global administration uses
/system; independent platforms use /operating-platforms. Existing global grants
retain system meaning. No platform/tenant association or business quota is added.

Authority checks use the same transaction as principal/session validation.
Target grants determine view access; actual operator identity determines audit.
Tokens are memory-only in the browser and view transports omit cookies.

Architecture and security reviewers verified exact owner-kind admission,
transaction-bound authority and operator attribution. Findings repaired:

- Group additions, wider windows, reactivation and dynamic rule expansion check
  grantability of bound roles under the policy lock. Structured implication checks
  future matches; provable subsets and revokes remain supported.
- GET /account-views/workspace returns metadata from immutable token scope only.
  Entity self-permission discovery uses its actual addressed entity.
- Personal /me applies the target browser's default tenant field policy. Tests
  prove identical visible, omitted and redacted fields.
- Avatar metadata accepts verified personal views; ordinary JWT/PAT reads retain
  cookie checks and mutations remain session-only. Missing avatars use a neutral
  placeholder without writing during reads.
- Operating menus persist two branches/five children. Management and navigation
  read the same records. New client routes use generated address types.
- Scoped views omit unrelated personal controls/reads. Exit restores the operator.
  Authentication/security/protocol conversions reject both view modes.

Authn migration16 records hashed secret, actor/target, operator-session binding,
credential versions, immutable scope/mode/expiry and revocation. View records are
separate from normal sessions and PATs; principal lifecycle revokes actor and
target records. Revocation cannot be undone.

### Verification

| Gate | Result | Evidence |
| --- | --- | --- |
| Affected backend packages | PASS | `.temp/account-view-delivery-backend-20260926.log`; fixture failures repaired and rerun below |
| Core, audit and composed API contracts | PASS | `.temp/account-view-delivery-contracts-20260926.log` |
| Identity/avatar final regressions | PASS | `.temp/account-view-delivery-avatar-20260926.log` |
| Authn/personal profile final regressions | PASS | `.temp/account-view-delivery-profile-final-20260926.log` |
| Frontend | PASS | Typecheck, lint, build, 145 IAM tests, generated contract checks |
| Real MySQL8 full gate | PASS | `.temp/account-view-mysql-verified-20260926.log` |
| PostgreSQL16 runtime and migration | PASS | Actual issue/use/revoke, authority changes and three-dialect migration suite |
| PostgreSQL16 signatures and enforced budgets | PASS | `.temp/account-view-postgres-performance-signatures-20260926.log`; `.temp/account-view-postgres-performance-enforced-20260926.log` |
| Real full browser lifecycle | PASS35/35 | `.temp/sqlite-lifecycle-owjvLZ/report.json`, zero uncaught errors |
| Final desktop/mobile scoped views | PASS14/14 | `.temp/sqlite-lifecycle-aS3pWI/report.json`, 21 inspected screenshots |
| Retained demo | PASS | Six account-selector logins enter expected workspaces without error alerts |

One concurrent PostgreSQL plans-only run exceeded the frozen SCIM budget. The
final isolated enforced gate passed every sample: SCIM median7.47ms, max7.69ms
against8ms. No benchmark samples or thresholds changed. All required checks,
self-review and independent acceptance review PASS; ready for user acceptance.

### Acceptance Environment

Web: `http://127.0.0.1:8091/login`. Swagger: `http://127.0.0.1:8080/docs`.
The development selector has six accounts, including system full/restricted,
operating platform, tenant, entity and department roles. Select an account and
submit the prefilled credentials. `make dev` enables the selector by default.
`make dev-status` reports URLs; `make dev-stop` stops services and retains data.

Account views start from System administration / Global accounts. Select a target
and its named workspace. Read-only is the default; writable requires explicit
selection. Exit restores the operator. Writable business changes are real, while
target sessions, credentials, personal tokens and login notifications stay intact.
