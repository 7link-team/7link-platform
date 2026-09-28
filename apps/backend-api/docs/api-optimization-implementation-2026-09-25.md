# API optimization implementation plan

Date: 2026-09-25
Baseline: 8868a9f (D78 and migration review)
Status: IMPLEMENTED; design review and final acceptance passed

## Goal and requirements

Implement improvements justified by the review, preserve existing security and
transaction guarantees, and verify the actual backend and browser with E2E.
This plan is the execution authority for this change. The 295-row manifest is
an inventory of alternatives, not an approved work queue.

## Existing implementation and reuse

GET /me and GET /me/session currently return the same identity projection.
ListSessions already returns public hashed session IDs, active-session metadata
and a current marker. RevokeSession checks the authenticated owner and audits
inside a transaction. LogoutAll revokes sessions and refresh tokens atomically.
Reuse these services, authentication middleware, CSRF and respx; add no database
schema, credential, authorization privilege, dependency or authentication engine.

## Design and actor/target matrix

| Operation | Credential / target | Behavior |
| --- | --- | --- |
| GET /me | Cookie or supported Bearer; actor | Existing identity, tenant resolution and profile projection unchanged |
| GET /sessions/current | Cookie only; actor's current browser session | Existing BrowserSession representation; no credential or profile fields; Bearer rejected with 401 even with Cookie |
| GET /sessions/{session_id} | Cookie or supported Bearer; actor-owned active item | Same representation as listing/current; foreign or expired/missing item 404 |
| DELETE /sessions/current | Existing logout credentials and CSRF rules | Existing idempotent logout, Cookie clearing and logout_url |
| DELETE /sessions/{session_id} | Existing credentials, actor-owned item, CSRF | Existing owner-scoped revoke and audit; foreign/missing 404 |
| GET /me/sessions | Existing credentials; actor | Existing active-session collection and current marker |
| DELETE /me/sessions | Existing credentials; actor, CSRF | Existing atomic logout-all and Cookie clearing |
| POST /sessions/current/step-up-* | Existing step-up credentials, CSRF, actor | Existing challenge, one-time consumption and Cookie rotation |

Remove GET /me/session. Identity-reading frontend methods keep their identity
return type and call /me. Move current-session logout and step-up beneath the
real Session resource. Move session item revocation to its global opaque ID and
add item reading. No old/new aliases. Collection /me/sessions remains its
existing self-only contract: moving it under a supplied principal ID would add
target validation without enabling a needed new capability. That collection
rename is rejected here, not queued for later.

Read current/item by filtering ListSessions so ownership, active lifetime,
public IDs and metadata stay identical. Current discovery requires a nonempty
valid Cookie and no Authorization header. An invalid current credential is 401;
an authenticated owner with no matching active item gets 404. Authentication
must precede item lookup; never reveal another owner's row or raw Cookie token.

## Review disposition

| Finding | Decision and factual basis |
| --- | --- |
| R1 | Preserve current identity/admin boundaries. Session item services already enforce actor ownership; no new admin powers. Reject broad principal-route unification without a demonstrated workflow need. |
| R2 | Preserve atomic onboarding and current field synchronization. Splitting one transaction into two requests introduces recovery costs without a required capability. |
| R3 | Keep existing permission discovery and metadata; the token chooser uses it. No replacement catalog is needed for this optimization. |
| R4 | Implement real current/item Session discovery and move identity consumers to /me. Benefit: clients can discover an actual public session ID and lifetime. |
| R5 | Preserve authentication workflows. Only step-up's resource parent moves; its state, replay protection and rotation remain service-owned. Reject generic transaction redesign. |
| R6 | Preserve respx, pagination and version/revision concurrency. No concrete lost-update defect justifies an infrastructure rewrite. |
| R7 | Keep existing file-operation mounts and context authorization. Destination addressing reduces URI choices but does not add capability; reject its migration here. |
| R8 | Preserve exact tuple matching and confirmed ID design direction. No current deletion ambiguity was proven; reject implementation/backfill in this optimization. No migration is scheduled. |
| R9 | Restrict declared scope to account and remove unused invalid personal-root client method. Runtime already rejects personal roots with 422. Do not infer or delete historical data. |
| R10 | Mark historical normative sections superseded and clarify execution authority, single URI and natural membership key. Preserve decision history. |

## Task decomposition and execution mode

Direct execution: plan/review, backend contract, generated OpenAPI/client,
regression/E2E, final acceptance. These share the same contract and require
sequential integration; delegation was evaluated and is not used.

## Files and boundaries

Change authn HTTP/service tests, files multipart schema, generated OpenAPI and
TS types, IAM client and dependent browser audits, and review/design/state docs.
Add this plan and meaningful current/item acceptance tests. Do not change SQL,
Principal/Membership services, file-operation policy, token catalog, respx or
authentication state machines. No production data migration.

## Testing and acceptance

Backend authn and files suites; frontend typecheck/lint/test/build; generated
OpenAPI/type drift checks. Real SQLite backend + Playwright browser E2E must
cover identity/current/item consistency, public ID versus Cookie secret,
foreign/missing item denial, CSRF rejection, revocation, logout-all, old-address
absence and the existing lifecycle/browser regressions. Exercise account root
creation and rejection of personal roots against the real backend. Record exact
results and artifact location; clean owned services before delivery.

## Second design review

PASS for this scope: every changed route has a credential/owner/representation
rule; no actor-target privilege expansion; active item and current schemas reuse
BrowserSession; authentication and revocation reuse existing services; no data
backfill; old aliases removed in the same backend/client checkpoint; real E2E
criteria include failures and secrets. All ten findings have an explicit
disposition. Whole-manifest execution remains rejected, not implicitly deferred.

Material cost: breaking pre-production current/item addresses requires callers
to update together. Current discovery scans the caller's existing active list;
this is bounded by the same data already returned by the security page, and
avoids adding a second lookup/ownership implementation without measured need.

## Delivery Evidence and Final Review

- Generated contract: 206 paths / 296 operations. One additional operation is
  GET session item. Structured comparison against baseline finds only four
  changed operations at unchanged addresses: the four multipart file creation
  mounts, whose declared scope enum is corrected. /me and other existing
  operation definitions are unchanged. Old current/item/step-up addresses are
  absent; new current discovery publishes Cookie-only security.
- PASS: `go test ./internal/iam/authn ./internal/files`; latest full-suite log:
  `.temp/api-optimization-tests.log`. New HTTP test verifies current/item IDs,
  credential exclusion, valid Bearer rejection for current even with Cookie,
  Bearer item reads, foreign/missing/invalid/expired items, CSRF and revoke.
- PASS: targeted current/item, identity tenancy and step-up HTTP tests after
  final assertion review. Existing step-up test exercises actual service/SQLite
  enrollment, wrong-code rejection, recovery-factor verification, Cookie rotation
  and old-Cookie invalidation. This is backend integration, not browser MFA E2E.
- PASS: frontend `bun run typecheck`, `lint`, `test`, `build`;
  IAM 133/133 tests and shared UI 85/85. Build retains existing Vite/dependency
  warnings; no new build error. `make openapi-check` and `bun run check:api-types`
  PASS. `git diff --check` PASS.
- PASS: real SQLite + Chromium acceptance, 14/14 flows, no uncaught page errors.
  Artifact: `.temp/sqlite-lifecycle-0q9rOr/report.json`; business responses are
  real, with one deliberately injected permission-read outage for its recovery
  test. Covers desktop/mobile lifecycle, identity and Session separation, item
  ownership, Cookie-secret exclusion, CSRF, device revocation, account roots,
  rejected personal scope, logout-all via actual security UI, Cookie clearing,
  current logout and idempotent retry. Owned backend/Vite processes shut down.
- PASS: 74/74 browser regression scenarios (stubbed backend; reported separately
  from real E2E), archive UI fixtures desktop/mobile PASS.

Initial test failures were investigated: operation counts/security manifests
needed the new item and Cookie-only discovery; whole-envelope equality was
incorrect because each read updates idle lifetime and request metadata; a
low-permission user's entity-file request correctly failed authorization before
input validation. The final E2E checks invalid personal scope in the user's own
authorized file collection and receives 422. No failure assertion was skipped.

Final self-review: owner isolation remains in ListSessions/RevokeSession;
authentication precedes reading; no raw credential is returned; metadata and
expiry reuse the established projection; logout failure/retry and step-up
security regressions pass. Runtime, generated contract, typed client and audits
are consistent. All selected requirements are accepted. No deployment, external
caller inventory, PostgreSQL/MySQL runtime E2E or historical-data deletion is
claimed; this optimization changes no SQL/schema. Rejected migrations are not
delivery blockers or scheduled followups.
