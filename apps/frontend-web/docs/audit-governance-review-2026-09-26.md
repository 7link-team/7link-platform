# Audit Governance Usability Review

## Request and Evidence

The user questioned whether the governance screen is usable and complete.
This review inspected the frontend, IAM audit services and current development
configuration. No audit settings, events, object storage or credentials were
changed. No new E2E run was performed; source inspection is not live acceptance.

## Findings

1. The development configuration has no `iam-audit` anchor/archive configuration.
   Both capabilities default to disabled. Recording events and verifying the
   database hash chain are separate from external anchoring and archiving.
2. `governance.go:getRetentionPolicy` returns constant defaults (365/730) when
   the scope has no policy row. It does not inherit the platform policy row.
   The frontend nevertheless labels a missing update time `platformDefault`.
3. The root-signature button checks chain integrity and manage permission only.
   It does not check anchor or signer availability. Anchor status does not
   expose signer availability separately from whether an anchor is signed.
4. `anchor.go:SignRoot` calls `Anchor` before checking the returned signature.
   With anchoring enabled and no signer, it can write an unsigned external
   commitment and then report `ErrRootSigningDisabled`.
5. An unsigned anchoring API exists, guarded by `audit_event_anchor`, but the
   web screen has no corresponding action. Signing is guarded by manage.
6. The platform tab is shown without checking platform administration authority.
   Server authorization is present; an unauthorized tab can still lead to 403.
7. Archiving has durable jobs, a worker, retries, object verification and download
   APIs/UI. These require configured S3-compatible object-lock storage.
   Empty job tables in this demo are not evidence that the enabled workflow ran.
8. Archive eligibility is age based (730 days by default). `min_days` is used
   for the archive object's retention deadline. Archiving does not delete source
   events; the screen must not imply automatic deletion after 365 or 730 days.

## Repair Plan

- Correct the default-policy label without introducing platform inheritance.
- Expose signer capability independently from historical signature state; gate
  actions by capability, chain validity and their existing server permissions.
- Reject signing without a signer before any external write. Preserve immutable
  historical commitments; do not overwrite an existing unsigned commitment.
- Add the existing unsigned anchor action for the existing anchor permission.
- Gate the platform scope entry by platform administration permission.
- Make disabled capabilities and age-based empty states distinguishable using
  concrete status labels; retain existing job retry/verify/download workflows.
- Exercise enabled capabilities only in an isolated environment with object-lock
  storage and valid eligible fixture events. Do not change the demo's retention
  policy or historical timestamps just to populate this screen.

## Review

- Architecture: reuse existing APIs, worker and object store; no new audit
  subsystem, policy inheritance or deletion mechanism is necessary.
- Security: keep backend guards, immutable commitments and secret configuration
  outside tenant forms; failed signing must have no unsigned-write side effect.
- Acceptance: disabled-state UI alone cannot prove enabled archive/signing flows.
  Both configurations and authorization failures must be exercised.
- Verdict: the repair plan is suitable for implementation; the current screen
  is NOT READY for a claim that all governance operations are usable in the demo.
  This is a direct review, not an independent multi-agent approval.

## Required Verification

- Backend: signer missing has no storage writes; authorized anchoring/signing,
  invalid chain rejection and immutable existing anchor handling.
- Real browser: desktop/mobile and Chinese labels; disabled capabilities offer
  no failing actions; tenant operator sees no unauthorized platform tab.
- Enabled isolated E2E: create anchor/signature, verify anchor; archive eligible
  events through worker completion, verify/download exact object, retry a failed
  job, and reject unauthorized and cross-tenant requests.
- Final evidence is recorded below. Whole-site menu correspondence is a separate
  unfinished acceptance requirement and is not covered by this audit result.

## Implementation and Verification

- Implemented all listed audit UI/service corrections without enabling external
  storage in the user's demo or changing its retention settings/events.
- Root signing capability is a separate published `signing_enabled` field.
  Unconfigured signing returns before any object-store write; an existing
  unsigned commitment remains immutable even after a signer is configured.
- Clarified tenant role scope and `tenant_administer` label. Authentication level
  uses named choices; the condition dialog previews conjunction, token-client,
  trusted-zone and daily-time requirements and explicitly distinguishes other
  grants/platform authority. This does not change grant evaluation semantics.
- Backend: `go test ./internal/iam/audit -count=1` PASS;
  `go test -race ./internal/iam/audit ./internal/iam/core -count=1` PASS.
- Frontend: workspace typecheck, lint, tests (142 IAM), build and API type drift
  check PASS. `make openapi` regenerated the additive capability contract.
- Enabled real Chrome E2E: isolated SQLite, actual worker and isolated MinIO;
  desktop/mobile retries after storage outage, segmented archives, verification,
  exact-object download, signed anchoring and explicit signature operation PASS.
  Foreign-tenant object reads404 and ordinary-member reads403 verified.
  Log: `apps/backend-api/.temp/audit-governance-e2e-20260926.log`.
  Screenshots: `.local/screenshots/archive-live/1790477244077/`.
- Disabled real Chrome E2E: desktop/mobile actions disabled by actual capability
  replies; condition save/reload and platform authority/catalog boundary PASS.
  Ordinary tenant member does not match the conditioned administrator grant,
  cannot change roles (403), and sees no platform tab or anchor/sign actions.
  Log: `apps/backend-api/.temp/audit-governance-disabled-e2e-20260926.log`.
  Screenshots: `.local/screenshots/archive-live/1790477722464/`; mobile condition
  dialog and tenant-auditor screen visually inspected without text overlap.
- Harness route/package paths were corrected after the preceding API/package
  migrations. Absent avatar404 responses are expected initials fallbacks, not
  audit failures; every other page API failure remains a failure in enabled E2E.
- The last-administrator guard rejected the first test fixture's attempt to
  condition its only permanent administrator (409). The fixture now supplies a
  separate permanent administrator, matching tenant bootstrap; no guard weakened.
- Self-review: scope, reuse, architecture, regression, complexity and durable
  evidence PASS for these fixes. Audit/condition acceptance PASS; no independent
  reviewer approval or full menu acceptance is claimed.
