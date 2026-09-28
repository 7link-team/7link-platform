# External Identity Ownership And Authorization Review

Date: 2026-09-26. Baseline: 563cea4. Review only; no production changes.

Follow-up: the user subsequently adopted the reviewed remediation plan. Findings
below describe the baseline; implementation and current verification are recorded
in external-identity-remediation-plan-2026-09-26.md.

## Ownership

| Capability | Configuration owner | Subject / effect |
| --- | --- | --- |
| Upstream OIDC / SAML identity providers | Tenant | Authenticate a global principal through a provider-specific identity link |
| Personal identity links | Principal | Link/unlink external identity with browser verification |
| OAuth/OIDC clients, downstream SAML service providers | Tenant | External applications trust this platform for authentication |
| Inbound SCIM directory | Tenant | Synchronize accounts, tenant memberships and groups; not a login protocol |
| Outbound SCIM target | Tenant | Provision/deprovision users and groups in an external application |
| Service accounts | Tenant | Explicit automation identity |
| SAML IdP signing key | Platform-wide service | All downstream tenant SAML integrations |

Entities are business authorization/data boundaries. Current external identity
configuration is tenant-wide, not entity-owned. Merely moving configuration to
entities would not correct the findings below.

## Confirmed Findings

### F1: Tenant-controlled IdP assertions can select an existing global account

Trigger: a tenant provider with AutoProvision enabled authenticates a previously
unlinked subject and asserts the email of an existing active global principal.
federation/service.go:560 calls EnsureExternalPrincipal; identity/service.go:251
globally looks up login/email, adds a tenant membership and returns that same
principal ID. Federation persists the link and service.go:610 creates a global
session for that ID without proof from the existing local account.

OIDC's verified-email claim is made by the configured provider. A tenant can
control its own provider; that claim cannot establish ownership of an unrelated
global account. The common authenticate path is also used by upstream SAML.
Consequences include impersonating other tenant memberships and platform grants
held by the selected principal, subject to existing account activation checks.

Evidence: actual identity TestEnsureExternalPrincipalCreatesThenReusesIdentity
passed with the same principal ID across tenants 7 and 8. Source tracing confirms
the persisted identity link and global session. The delegated OIDC fixture probe
also demonstrated session selection; it used fake identity/session collaborators,
so this review does not claim a complete production HTTP takeover E2E.

Recommendation: never automatically link an existing global principal by email
from a tenant provider. Require the existing principal's authenticated linking
and fresh proof. For new identities retain provider+subject identity and explicit
tenant membership policy; duplicate global emails/login names must fail closed
until ownership is proved. Do not auto-attach a global/platform administrator.

### F2: Tenant permission can rotate a platform-wide SAML signing key

POST /tenants/{tenant_id}/saml-signing-keys uses identity_provider_update in
federation/saml_http.go:111. Its handler calls RotateSAMLSigningKey without a
tenant argument. saml.go:207 replaces the shared key/certificate; saml.go:337
uses the same pair for every tenant's metadata and assertions.

Trigger: SAML is enabled, uses its managed database key, and a tenant member
holds identity_provider_update (including tenant administrators). Rotation affects
other tenants' external SAML trust. Cached/pinned certificates may reject logins
until the external service providers consume the new metadata.

Evidence: TestSAMLSigningKeyRotationHTTP and file-backed conflict test passed;
the former confirms tenant-scoped rotation changes the service certificate.
File-managed keys reject API rotation and are outside this triggering condition.

Recommendation: keep the shared-key architecture, move rotation to a platform
operation guarded by platform permission. Per-tenant keys are not required for
this fix. This is downstream SAML signing, not upstream IdP verification keys.

### F3: Outbound SCIM exports a disabled membership as active

Production identity/provisioning.go:117 disables tenant membership while leaving
the global principal active. target.go:549 derives User.active exclusively from
principal.Status. identity/service.go:520 admits disabled memberships when loading
the profile. SyncTarget at target.go:691 pushes existing live mappings.

Root independently reproduced: create/push a user, disable tenant membership,
read inbound user active=false, then synchronize the target. Observed:
`inbound active=false, outbound active=true, synced=1`. The assertion requiring
outbound active=false failed. The temporary probe was removed.

Recommendation: compute exported active from both global account and current
tenant membership; propagate disabled status to existing mapped remote accounts.
Add a regression using production membership semantics. The unit fake currently
changes global status too, masking this condition.

## Menu Verification

Real Chrome and API inspection of all five selectable demo accounts:

- Super-admin: 14 tenant menu paths and 5 platform permission codes.
- Tenant-admin: 14 tenant menu paths, no platform permission codes.
- Restricted system-admin: one tenant menu path, platform tenant_view only.
- Entity-account and department-account: one tenant menu path, no platform codes.
- Rendered tenant links use /tenants/{id}; platform links are excluded from tenant
  navigation. Extra files/field-policy links are gated separately.

This explains why super-admin and tenant-admin see similar menus when both are
viewing the same tenant. No menu-driven platform privilege expansion was found.
SCIM has a tenant-wide configuration menu; upstream identity providers and SAML
service providers currently have API contracts without corresponding router pages.

## Additional Ownership Risk

SCIM profile writes update the global login/email/display name. Membership disable
and certain profile changes revoke global security state, affecting sessions in
other tenants (identity/provisioning.go:107-131). This is a demonstrated global
effect, but shared-profile ownership is not explicitly resolved by D17/D19.
Treat it as an ownership decision, not as a proven unauthorized data modification
or a reason to invent another account concept.

## Verification And Limits

- Existing federation/provisioning/oauth/identity/core package tests: PASS.
- Sidebar/navigation tests: 17 PASS.
- Actual identity reuse and SAML rotation tests rerun with count=1: PASS,
  demonstrating existing behavior rather than proving the security policy safe.
- Root SCIM negative probe: FAIL as expected, confirming F3.
- Real five-account Chrome menu inspection: PASS.
- Actual published OpenAPI inspected for configuration and protocol scopes.
- No live identity provider, signing key or SCIM target was changed.
- A1/A2/A3/A4/A6/A8 apply to callbacks/synchronization/isolation and were traced;
  no financial arithmetic (A5) or schema/version change (A7) in this review.
- Focus: authorization/security, API ownership, identity lifecycle and UI scope;
  no dependency, performance or database-dialect change review was required.

Local artifacts: backend .temp/external-identity-review-tests.log,
.temp/external-scope-identity-probe.log, .temp/external-scope-saml-probe.log,
.temp/external-scope-scim-probe.log; Web .temp/external-scope-menu-report.json.
Next: fix F1-F3 with the same ownership model and focused negative/E2E tests;
resolve shared-profile authority explicitly before changing that behavior.
