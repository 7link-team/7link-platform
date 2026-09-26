# External Identity Remediation Plan

Date: 2026-09-26. Baseline: 563cea4. Status: V2 approved by security,
architecture and product/acceptance reviewers, adopted by the user, implemented.
Final integration verification and acceptance PASS. The proposal sections below record
the adopted scope and acceptance requirements.

## Goal And Requirements

Fix F1-F3 from external-identity-scope-review-2026-09-26.md, preserve tenant-owned
integration configuration, and explicitly define shared-principal effects.
Reuse global principals, tenant memberships, identity-link transactions, existing
authentication/proof gates, platform grants, SCIM jobs and owner module migrations.
GUID endpoints remain. No new account type, per-entity IdP or per-tenant signing key.

## Adopted Ownership

| Resource / operation | Authority |
| --- | --- |
| Tenant IdP, OAuth clients, SAML SP, SCIM directory/target | Existing tenant permissions |
| Global principal credentials and primary verified email | Principal self-service / existing platform authority |
| Link an external identity to an existing principal | Principal's authenticated linking flow and independent fresh proof |
| Global SAML signing key | Platform permission only |
| Tenant membership activation | Tenant/directory authority, with existing last-admin protection |
| Tenant-specific display name/contact projection | Tenant membership, not global credentials |

## W1: External Login And Historical Links

1. Existing provider+subject links continue only when their ownership is trusted.
2. Unlinked, auto-provisioned subjects may create a NEW principal and membership
   atomically. A matching global login/email must never select an existing account.
   Return a generic linking-required/conflict result, create no membership, role,
   link or session, and use the existing login/recovery/linking flow.
3. A legitimate existing principal joins a tenant through existing membership or
   invitation, then links its identity via the existing browser proof flow.
4. Concurrent duplicate creation must not fall back to global email reuse. On a
   unique collision, roll back; only provider+subject-owned state may be re-read.
5. Add federation-owned binding provenance: unknown (historical default),
   new_principal, principal_verified; add a security configuration fingerprint.
   No automatic trust backfill based on existence, successful login, or a consumed
   historical linking transaction. Preserve all source records and audit history.
6. Unknown links cannot issue sessions or verify proofs. Identify their principals
   before cutover. Since historical sessions have no link attribution, conservatively
   invalidate affected principals' sessions, refresh tokens, PATs, OAuth grants,
   pending proofs and continuation transactions; implement via owner capabilities.
   Do not claim the existing revokeAllSecurityState covers every category.
7. Relinking requires independently established local password/passkey proof.
   Unknown federated proof cannot bootstrap trust, including via another provider.
   For repair, extend the existing same-principal link transaction to upgrade a
   quarantined binding atomically; another principal's link remains a conflict.
   Repair creates no new membership or role and does not discard link history.
8. Existing link verification enforces active principal, tenant and membership;
   tenant suspension/removal never recreates membership automatically.

9. Bind login, reauthentication and linking completion to a security fingerprint
   covering provider type, OIDC issuer/client ID, and SAML entity/metadata source
   and configured trust material. Tenant replacement of an authentication source
   invalidates its links and pending proofs; no automatic fingerprint refresh.
   Name/default-role/timestamps are not security fields. Ordinary OIDC JWKS rollover
   under the same issuer remains supported. SAML trust-material replacement requires
   explicit rebind until a separately approved rollover policy exists.
10. Serialize/revalidate binding trust through session issuance. A security-source
    edit or quarantine racing a callback cannot issue a session from an obsolete
    snapshot after the federation transaction. Verify the issued session/credentials
    against current trust; use existing transaction/credential capabilities.

### Legacy Recovery And Deployment Gate

Inventory link/affected-principal counts, independent methods and their trustworthy
enrollment evidence, primary-email history, external-only users, active derived
credentials, and unresolved cases. Credentials enrolled in possibly compromised
sessions are not automatically independent evidence. Unknown ownership remains
quarantined; never automatically delete credentials or reset primary email.

The existing password recovery requires a credential, while JIT-only principals
have none. Extend the existing recovery completion to create the first credential
atomically after independently delivered mailbox proof, using existing credential
creation/password/history/audit capabilities. For legacy repair, the recovery
address must have independent ownership evidence; a provider's EmailVerified flag
alone is insufficient. A changed or unverifiable address is an unresolved case.
Normal new passwordless accounts must have a tested first-credential recovery path.

Unresolved legacy ownership is a deployment blocker requiring an operator's
documented ownership resolution before rollout, not an invented existing support
workflow or an exception that re-enables unsafe login. Verify a working independently
authenticated platform operator first. If inventory is empty, this blocker is empty.

Cutover: back up; inventory; stage additive schemas; stop old replicas/dispatchers;
apply quarantine and invalidation; start all enforcing replicas; test; reopen.
No old/new mixed authentication traffic. Do not roll back to the vulnerable binary.
Rollback may use only a build retaining trust enforcement and platform-only rotation;
retain quarantine/provenance and invalidation state. No old-schema down as an
operational rollback. Database-specific upgrade tests verify owner milestones.

## W2: Shared SAML Signing Key

Replace POST /tenants/{tenant_id}/saml-signing-keys with
POST /platform/saml-signing-keys, using RegisterPlatform and platform_administer.
Remove the tenant operation entirely; retain shared key persistence, restart
behavior and file-backed 409 refusal. Do not rotate a real key during upgrade.
Record platform-scoped audit events; tenant_id must not imply tenant-local impact.
Update composed route/guard manifests, OpenAPI, generated clients and any callers.
No unsafe tenant-compatible alias. Existing valid key data needs no rekey migration.
Operation ID remains federation-rotate-saml-signing-key unless the project generator
requires otherwise. Upgrade client/server together; inventory callers of the old
tenant route. No new Web rotation page is required. Audit failure rolls back key
mutation; return file-managed refusal without mutation or audit success claims.

## W3: SCIM Activation And Shared Profile

1. Extend the identity-owned capability to return a tenant provisioning projection:
   global account activation AND current tenant membership activation. Include
   disabled memberships so mapped remote accounts can receive active=false.
2. Both direct push and background sync consume that projection. Missing membership
   must reconcile an existing remote mapping through the deprovisioning workflow,
   not silently leave it active. Transient database/network errors must not be
   interpreted as removal or trigger irreversible remote deletion.
3. Repair the provisioning test fake to match production: disabling a membership
   preserves active global principal state. Verify with real composed identity too.
4. Adopted shared-profile policy: after SCIM creation, userName (global login) is
   immutable. PUT accepts an unchanged userName, requires the required attribute,
   and rejects changes with SCIM 400/mutability. Discovery advertises immutable.
   displayName/emails are tenant member fields; PUT omission removes contact email
   and resets displayName to userName, matching the existing nonempty-name invariant.
   PATCH changes only addressed attributes; remove uses the same local defaults.
   Global primary email verification/credential version never changes for local edits.
   Mixed allowed/disallowed PATCH operations fail atomically. POST restoration uses
   the same rules; password writes on existing users retain current refusal behavior.
   GET/list/filter/sort/PATCH seeds and outbound projection all use the same local
   representation. Preserve identity.Get's existing global Principal contract.
5. Membership deactivation blocks access to that tenant immediately and withdraws
   its grants/tokens. Avoid revoking unrelated tenant sessions solely for a local
   deactivation; global credential changes/suspension retain global revocation.
6. Preserve last-admin guards, directory mapping ownership, version/If-Match,
   field-policy masking/write checks and bound service-account validation.

Compatibility: profile restrictions and revocation semantics were reviewed and
adopted by the user. They use existing membership fields and owner capabilities.

### W3b: Apply Shared-Profile Authority To Human Tenant Administration Too

The existing tenant-qualified principal PATCH also changes global profile fields;
SCIM-only enforcement would not establish the proposed ownership table. Explicitly
include identity/http.go and Service.Update plus the Web account/member editor.
Tenant administrators edit contact/display via existing PATCH
/tenants/{tenant_id}/members/{subject}; this API already supports both fields.
Global principal edits require target==authenticated principal with existing
self-service checks, or platform_administer; retain tenant/member admission checks
for tenant-qualified routes. Do not silently reinterpret a global PATCH as local.
Web admin editing uses member projection/API, while self profile remains global.
Read responses and labels distinguish tenant contact from primary account email.
No new profile table, identity type or replacement principal route family.
Global self/platform profile updates do not mirror into tenant contact/display
fields. Preserve their independent values and test the old Service.Update mirroring
branch is removed for this policy; global and tenant edits remain separate commands.

This is a deliberate authorization/compatibility change proposed for approval.
All aliases/import/restore/SCIM paths must follow the same field ownership policy;
retain existing password/MFA/activation/suspension semantics outside this policy.

## W4: Web Scope And Menu Regression

Keep existing platform/tenant/entity separation. Verify super-admin and tenant-admin
see tenant menus only in tenant context; ordinary/entity members cannot manage
integrations. Clearly identify shared-key rotation as platform-wide if exposed.
No new IdP/SAML management pages or menu regrouping required by these fixes.
Identity-link failures must offer the existing independent account proof path;
no perpetual retry, sensitive account-existence detail or new identity concept.

## Files And Execution

Workstreams W1/W3 modify shared identity capabilities and must be integrated in
sequence. W2 route/audit changes are separable. Implementation owner coordinates
contracts before parallel edits; independent roles review the plan only now.

Expected edits: identity contracts/service/provisioning and existing security-state
capabilities; federation login/linking/models/migrations and saml_http audit;
provisioning resource projection/target reconciliation and fakes; relevant authn/
oauth/core tenant-revocation capabilities only where needed; frontend error paths,
exact route manifests and generated contracts. Reuse owner migrations in sqlite,
postgres and mysql. No changes to GUID, unrelated APIs or organization concepts.
Authn recovery is required for first credentials; human member editor/API ownership
is included in W3b. Module receivers/connectors preserve current dependency order;
do not create a central identity coordinator or a historical session-source framework.

## Verification And Acceptance

- F1: unrelated existing account/email cannot create a link, membership or session;
  new account succeeds; legitimate independently verified linking succeeds; tenant
  membership removal, concurrent callbacks, replay and legacy unknown links fail safely.
  Assert persisted principal/link/member/role/session counts and revisions, not only
  HTTP denial. Test same-principal rehabilitation, source replacement, proof source
  independence, stale fingerprints, and passwordless first-credential recovery.
  Include provider/quarantine changes racing session issuance and ensure no usable
  stale-trust session/derived credential survives the completed invalidation.
- F2: tenant administrator receives denial and key remains unchanged; authorized
  platform operator rotates; old tenant route is absent; two tenants share unchanged
  metadata after denied rotation; file-managed keys still return conflict.
- F3: active principal + disabled membership exports active=false; reactivation
  respects global suspension; missing membership uses deprovisioning; unrelated
  tenants remain unchanged; transient errors cannot deactivate/delete remote users.
- Shared profile: disallowed global changes roll back; allowed local changes round
  trip and respect field policy; local disable withdraws only that tenant's access.
  Test immutable unchanged/changed/omitted userName PUT; multi-operation PATCH
  rollback; tenant email/display filters and response round trips; human tenant
  admin global PATCH denied but member PATCH succeeds, self/platform paths preserved.
  Existing browser/PAT/OAuth/refresh access for disabled tenant is denied immediately,
  other tenant remains usable, and reactivation cannot revive revoked grants.
- Real composed API tests plus Chrome E2E using local OIDC/SAML/SCIM fixtures;
  both positive and negative paths and desktop/mobile identity recovery/navigation.
  Existing browser federation fixture is OIDC-only: add signed upstream SAML browser
  fixture for this shared authentication path; downstream SAML metadata/signing uses
  composed API evidence. Do not claim browser SAML coverage from OIDC tests.
- Upgrade tests for legacy binding provenance on all supported database engines,
  both deployment prefixes. Unsafe rollback must not restore email auto-linking.
- Backend own CI/relevant release gates, frontend workspace checks/build, OpenAPI
  and generated contract drift, five demo personas. Store actual evidence; no
  acceptance based only on existing green unit fixtures.

## Independent Role Review

V1: all three roles requested revision. Accepted corrections: security fingerprint
and proof-source checks, conservative legacy invalidation, explicit rehabilitation
and passwordless recovery gate, concrete SCIM semantics/discovery, human tenant
profile authority, safe cutover/rollback and named E2E assertions.
V2 security, architecture and product/acceptance roles approve the design with
deployment/product gates. Security session-race and architecture non-mirroring
clarifications are incorporated. Implementation evidence is recorded below.

| Role | V1 | V2 final result | Main review corrections |
| --- | --- | --- | --- |
| Security | REVISE | APPROVE design | Source fingerprint, independent proof, complete invalidation, issuance race |
| Architecture | REVISE | APPROVE design | Same-principal repair, owner capabilities, human profile authority, no mirroring |
| Product / operations / acceptance | REVISE | APPROVE design | First-credential recovery, SCIM semantics, safe cutover, signed SAML browser evidence |

The root review reconciled these corrections against the existing membership API,
global-principal contract, recovery prerequisites and recorded F1-F3 evidence.
Design review, implementation and final verification are complete.
Before deployment, resolve any legacy ownership cases and demonstrate all
verification criteria with actual artifacts. An empty legacy inventory has no
legacy recovery blocker; unverifiable ownership must never be automatically trusted.

## Operator Cutover Commands

Stop old replicas and back up the database/configuration before migration and
quarantine. Use a maintenance configuration with the federation owner capability
enabled; maintenance initializes owner services without opening HTTP listeners or
starting business jobs. Diagnostic logs use stderr; inventory stdout is JSON.

```powershell
7link-api iam external-identity-inventory --config maintenance.json
7link-api iam quarantine-external-identities --config maintenance.json
7link-api iam record-external-identity-evidence --config maintenance.json --principal-id PRINCIPAL_ID --evidence-file ownership.txt
7link-api iam external-identity-inventory --config maintenance.json
```

Inventory exits 4 for unresolved ownership. Recording independently reviewed
evidence permits recovery/repair but never trusts a binding or issues credentials.
Users recover or authenticate independently, then relink using fresh local proof.
Disabling federation does not bypass retained-link quarantine or recovery evidence.
Migration 7 Down explicitly refuses: rollback must retain the enforcing schema and
security build. Historical migration 6 rollback tests use a separate v6 fixture.

## Implementation Cross-Review

Independent reviews repaired provider/principal lock inversion, stale SCIM remote
deletion after membership restoration, disabled-feature recovery evidence bypass,
and recovery delivery after a concurrent primary-mailbox change. Regression tests
cover these failure paths. Browser assertions compare both tenants' certificates
before/after denied and successful rotation; replay uses a distinct fresh password.
The mobile linked-identity selector now has a constrained width and browser layout
assertions. No menu authority expansion or new identity concept was introduced.

## Verification Evidence

- Actual Chrome lifecycle E2E: 24 flows PASS, zero mocked business responses and
  zero page errors. Local report: `.temp/sqlite-lifecycle-By3Pa0/report.json`.
  Signed upstream OIDC/SAML desktop/mobile, independent first-password recovery,
  credential replay denial, source invalidation, tenant SCIM projection and both
  tenants' signing metadata were exercised. Mobile screenshots inspected.
- Composed Cookie/PAT/OAuth/refresh isolation: both prefixes PASS. Tenant A
  deactivation denies A, leaves B usable, and restoration never revives old grants.
- Relevant six-package `go test -race`: PASS; `.temp/external-race-final.log`.
  Provider ordering/retry is tested deterministically and actual session issuance
  racing source invalidation is tested on SQLite. No claim of concurrent provider
  row-lock testing on PostgreSQL/MySQL is made.
- Frontend workspace typecheck, lint, tests, build and API type drift: PASS.
- `make generate-check`: PASS on frozen files; local log
  `.temp/external-generation-delivery.log`.
- All-engine migration7/both prefixes and schema-lock: PASS. Real MySQL8 full gate
  PASS/native exit0: `.temp/external-mysql-normal.log`; all 20 performance samples
  passed original thresholds (SCIM median113ms/max117ms, limit154ms).
- Corrected nested lifecycle suite across all six engine/prefix combinations PASS:
  `.temp/external-lifecycle-corrected.log`. PostgreSQL normal performance PASS:
  `.temp/external-postgres-normal.log`. Old package selectors now have execution
  guards; no-tests output cannot satisfy these gates. SQL plans regenerated for
  retired-position removal and local SCIM projection; benchmarks unchanged.
- Migration-status historical rollback/current downgrade refusal: all engines,
  both prefixes PASS. Final `make ci` PASS/native exit0:
  `.temp/external-ci-final-pass.log` (overall91.2%, federation90.1%, unchanged90%
  gates). Full nested migration321s/lifecycle112s/performance45s, build and OpenAPI
  consistency PASS. Final generate-check PASS:
  `.temp/external-generation-final-pass.log`.
- Persistent demo refreshed after SQLite backup; empty legacy inventory `[]`.
  Five selectable demo personas, real login and `/me/identities` load verified.
  All five persona menu audits PASS:
  `.temp/dev/browser/persona-menus/report.json`.
  Web `http://127.0.0.1:8091/login`, Swagger `http://127.0.0.1:8080/docs`.

Test logs, disposable fixture binaries/databases and backups remain local ignored
artifacts. Historical owners in a real deployment require the operator gate above;
demo inventory does not establish ownership for a different deployment.

Final self-review and requirement acceptance: PASS. Security, architecture and
product/acceptance cross-review findings are resolved. No outstanding implementation
or verification work remains. The user subsequently authorized committing and
pushing all reviewed deliverables together on IAM.
