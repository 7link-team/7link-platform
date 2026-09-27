# Unified Access Views

## Goal and Confirmed Requirements

Unify dedicated account, operating-platform and tenant views. Every view records
the actual visitor, the typed access object, immutable data scope and read/write
mode. Open each view in a separate tab with its own memory-only credential.
Organization entry must not impersonate an owner or create visitor membership.

## Existing Implementation and Reuse

Reuse /account-views issuance, server-side hashed token records, live original
session checks, revoke/expiry, scope guards, real-actor audit and verified tab
handoff. Preserve existing account requests and persisted tokens as account views.
Existing organization routes and RBAC engines already provide the needed pages.

## Design

- Add target_type: account (legacy default), platform or tenant. Organization
  requests address the matching scope_id and omit principal_id. Their effective
  authenticated principal remains the actual visitor; account requests retain
  the explicitly selected principal.
- Store the target type in the existing token record. Derive the access object
  ID from effective principal for account views and immutable scope for organization
  views. Never accept a browser-supplied visitor or borrow an organization owner.
- A visitor needs existing account_view_create authority to issue a view. Active
  organization members keep their existing RBAC permissions. The protected full
  system administrator may manage the exact selected organization without joining
  it. Merely holding account_view_create does not confer organization administration.
- Apply organization admission only to server-verified organization views with
  visitor/effective-principal agreement, matching owner kind and exact scope.
  Ordinary cookies and account views retain their current admission behavior.
- Recheck owner activity and current protected system authority on every use.
  Existing original-session and credential-version checks also remain active.
- Audit visitor, target_type/target_id, view ID, scope, mode and mutation object.
  Do not fabricate viewed_principal_id for an organization view.
- Extend the existing UI dialog/handoff for organization selection and explicit
  read-only/read-write choice. Enter links use dedicated views; ordinary workspace
  switchers continue switching the caller's admitted workspaces.

## Implementation and Boundaries

Direct implementation: authentication/model/migrations/generated query, verified
claims, scoped admission and audit, frontend entry/dialog, contract generation and
real browser acceptance. No new login/session, permanent account categories,
membership writes, owner impersonation, business modules or new token subsystem.

## Review Gates

Architecture: preserve account-view behavior, one token lifecycle, explicit target
semantics, migrations for SQLite/MySQL/PostgreSQL and exact owner resolution.
Security: reject spoofed visitor, mismatched target/scope, ordinary-cookie bypass,
cross-organization access, self-service credential conversion and readonly writes;
verify revocation and real actor attribution inside mutation transactions.
Acceptance: no denied entry for a protected superadministrator; organization
identity stays the visitor; member and owner data remain unchanged.

## Verification

Run affected backend authorization/authentication/audit tests, frontend tests,
typecheck/lint/build and API generation consistency. Real E2E must cover both
organization types, read/write, nonmember superadministrator, restricted visitor,
concurrent account/platform/tenant tabs, expiry/revocation and exact audit fields.
Inspect desktop/mobile screenshots and leave the retained demo ready for acceptance.

## Status

Architecture review PASS: reuse lifecycle and handoff; preserve legacy account
requests; effective identity stays a principal while the access object is typed.
Security review PASS with required corrections incorporated: creation authority
alone cannot grant nonmember administration; only the protected full system
administrator gets that admission. Scope/type and effective visitor must agree;
target type is storage-immutable. Ordinary/account-view admission remains unchanged.
Data constraints must use the same verified organization authority as route guards.
Implementation complete. Final architecture/security self-review PASS: one token
lifecycle, preserved account semantics, verified visitor identity, immutable exact
organization target, live protected authority, aligned route/data constraints and
no membership writes. Review performed directly; no independent agent review is
claimed. Requirement acceptance PASS against the confirmed visitor/object model.

## Executed Evidence

- Affected authn/core/audit and extension authn/authz Go suites PASS.
- Real MySQL8 and PostgreSQL16 TestAccountViewExternalDatabase PASS, including
  migrations, target immutability, scoped routes and full-authority revocation.
  Logs: .temp/access-view-mysql.log and .temp/access-view-postgres.log. Owned
  disposable containers removed; existing shared containers were not changed.
- Frontend workspace typecheck, lint, tests (IAM152, shared UI85), production build
  and check:api-types PASS. Published OpenAPI and generated schema agree.
- Full real browser acceptance: .temp/sqlite-lifecycle-iMLiIb/report.json,
  50/50 PASS, zero mocked business responses, zero page errors. Covers original
  account/system workflows plus restricted visitor denial, nonmember protected
  administrator entry, platform/tenant readonly and writable tabs, real role UI
  mutations, typed actual-visitor audits, unchanged memberships, exact scope,
  credential-conversion refusal, refresh failure and isolated exit/revocation.
  Existing shared lifecycle coverage verifies token expiry and original-session
  checks. New target coverage does not replace those existing assertions.
- Desktop platform-writable and mobile tenant-readonly screenshots inspected:
  target/mode banner visible, readonly mutation controls absent, content fits.
- Retained rebuilt demo: http://127.0.0.1:8091/login, API8080, demo selector enabled.
  Chinese tenant-entry browser smoke PASS with target_type=tenant and readonly
  memory-only view; screenshot .temp/dev/organization-view-desktop.png.
- A first new restricted-visitor fixture omitted required full_administrator and
  received422. Corrected the fixture; the final full gate above passed afterward.

No implementation remains for this plan. Refresh intentionally loses authority;
reopen the view from the operator tab. This follows the confirmed memory-only
credential requirement.
