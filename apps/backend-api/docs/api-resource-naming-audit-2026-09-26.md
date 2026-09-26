# API Resource Naming Audit - 2026-09-26

## Requirement And Decisions

Inspect the entire published path inventory, including nested resources. Swagger
groups belong to the first segment of the final URI. Resource names must identify
their actual meaning; issuance and consumption of one workflow must not create
unrelated top-level groups. This implements the user's explicit corrections.

Execution: direct, because route declarations, composed manifests, generated
schemas and frontend consumers share one contract. No business or database
migration, new authorization, compatibility aliases or navigation redesign.

## Corrected Families

| Previous Resource | Canonical Resource | Source Evidence / Meaning |
| --- | --- | --- |
| groups | principal-groups | organization/group.go and group_service.go: static or rule-derived groups of active tenant principals, used for collective role assignment |
| positions | job-positions | organization/position.go: tenant job code/name, principal membership windows and role assignments |
| invitations | membership-invitations | organization/invitation.go: invitation to create a principal and join a tenant |
| invitation-acceptances | membership-invitations/acceptances | The credential is supplied in the body; acceptance remains public and does not require a new path ID |
| email-verification-requests | email-verifications/requests | authn/verification.go: issue and consume credentials for the same primary email verification workflow |
| access-requests | role-access-requests | governance/governance.go: each request identifies one RoleID and bounded grant expiry |
| access-reviews | role-access-reviews | governance/review.go: snapshots role grants, including direct, temporary and directory/entity-derived grants |
| relationships | authorization-relationships | core/relationship_http.go: constrained subject/relation/resource authorization grants |
| scope-model | authorization-scope-model | core/service.go: platform, tenant, entity and business-resource authorization hierarchy |

The same names apply to tenant-contained collections and principal projections.
For example `/tenants/{tenant_id}/principal-groups` is grouped under `tenants`,
while `/principal-groups/{group_id}` is grouped under `principal-groups`.
`/menus/{menu_id}` belongs to `menus`; its tenant collection belongs to `tenants`.
Grouping never uses the suffix, module or authentication policy.

## Remaining Root Coverage

Every retained root was inspected together with its nested operations:

| Roots | Decision / Meaning |
| --- | --- |
| .well-known, oauth, saml, scim | Retain established discovery/federation/provisioning protocol addresses, including SCIM Groups and Users |
| healthz, livez, readyz | Retain operational health/readiness resources; a single operation is justified here |
| guid | Retain the existing globally unique ID generation utility |
| login | Retain local, MFA and passkey authentication entrypoints; one prefix already contains the workflow |
| me | Retain authenticated identity discovery before the client knows its principal ID |
| registrations | Retain account registration and its captcha issuance under the same root |
| password-recoveries | Retain existing request/consumption structure, used consistently for email verification |
| sessions | Retain current/item session discovery, revocation and current-session verification children |
| principals | Retain explicit account resources; identity links, MFA, passkeys, tokens, files and closure stay inside the principal tree |
| tenants | Retain owner collections, memberships, account projections, catalogs and tenant authorization/audit operations |
| platform | Retain platform administration, retention/erasure governance and platform audit scope |
| departments | Retain organizational department hierarchy; memberships are a separate established tenant resource |
| entities | Retain the polymorphic domain entity: company, enterprise, merchant or store; core/service.go declares this layer explicitly. It is not a database-record CRUD API |
| roles | Retain IAM roles, permissions, conditions, data scope and principal/directory bindings |
| menus | Retain navigation menu metadata and CRUD |
| files, todos | Retain file/directory and task identities with their content, hierarchy and lifecycle children |
| identity-providers | Retain OIDC/SAML external identity providers and their authorization/callback continuation endpoints |
| oauth-clients, saml-service-providers | Retain explicitly named application registrations and credential/configuration children |
| service-accounts | Retain non-interactive tenant accounts and credentials |
| scim-directories, scim-targets | Retain explicitly named inbound directories and outbound provisioning targets/jobs |

Nested resources were checked for their owner and flow: group/position members,
role bindings, invitation deliveries, principal security resources, tenant
authorization relationships, audit archives/attempts/verifications and provisioning
jobs all remain under their real parent. No new global singleton groups are
introduced for their actions. Generic internal discriminators such as `group`,
`position` and `entity` retain their established meaning in bodies and permissions.

## Compatibility And Review

This changes public addresses intentionally: older callers receive 404 and must
use the regenerated contract. Web page navigation, stored menus, domain IDs,
permission codes, payloads and authentication policies are preserved.

Structured comparison against c713c693: all 296 operation IDs and HTTP methods
retained; exactly 37 operation addresses changed. All component schemas and
every operation field except grouping tags compare equal.

Regression gates cover retired resource segments in every exported path and
first-prefix tags for every operation. Browser acceptance checks the actual
rendered Swagger sections and every rendered operation, rather than JSON alone.

## Verification

- Backend `go test ./...`: PASS after enabling browser routes in the export test
  fixture; the first run identified that fixture omission.
- Backend lint: PASS.
- Frontend workspace typecheck/lint/test/build: PASS; IAM 136 tests, UI 85 tests.
- Real SQLite/Chrome E2E: PASS, 18 flows, zero mocked business responses and zero
  uncaught page errors; `.temp/sqlite-lifecycle-vaXTfK/report.json`.
- E2E includes principal-group/job-position role assignments and dynamic rules;
  primary email request, real outbox webhook delivery, consumption and replay
  rejection; invitation rotation, anonymous acceptance, revoked credentials;
  role access request/approval/review decision/revocation; retired addresses 404.
- Fixture browser UI regression: PASS, 74/74. A run was interrupted by development
  server replacement; the complete rerun against the final server passed.
- Independent integration module: PASS, architecture/goose/testdb/lifecycle/
  migration/performance suites (`make test-integration`). This exercises the
  configured integration engines; it is not an external production UAT claim.
- OpenAPI and frontend generated-type drift checks: PASS.
- Rebuilt demo account selector, login, session and identity links: PASS, 5/5.
- Actual rendered Swagger UI: PASS, 309/309 operations in final-prefix groups;
  live methods/paths match all 296 operations in the checked-in public contract.
- Other affected environment-specific browser scripts were updated and syntax
  checked; real E2E evidence for this delivery is the SQLite suite above.

The old acceptance server had stale menu tags. Its owned processes were stopped;
the rebuilt canonical development runtime is Web8091/API8080 with five selectable
demo accounts. Unrelated services were not changed.

## Final Acceptance

Self-review PASS: scoped changes, existing registrars/services reused, no new
abstractions or behavior, UI navigation preserved, generated artifacts regenerated,
no protocol/DB changes, actual browser evidence, clean diff and LF source edits.
Requirement acceptance PASS: ambiguous names qualified, split invitation/email
workflow roots unified, complete disposition ledger, prefix grouping enforced on
the exported contract and actual UI, old addresses absent/404, real flows verified.
No remaining implementation or optional follow-up migration is queued.
