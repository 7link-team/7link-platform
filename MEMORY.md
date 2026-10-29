# Release v0.1.0-rc.5 - Final Delivery Receipt

## Objective / Authorization
User continued the proposed rc5 publication. Publish the verified in-place Windows installation and startup chooser-flash fixes; keep GitHub/GHCR public, preserve prior releases, verify actual updater delivery, and clean owned generated files.

## Phase / Current Step
ACCEPTANCE COMPLETE / PASS. Release: https://github.com/7link-team/7link-platform/releases/tag/v0.1.0-rc.5
Immutable annotated tag source2903f3aa1764776b3e6381d2aeb2064ac6fdbaa1; main receives a final state-only documentation commit after verification, without moving the tag.

## Completed Work / Verification
- Required behavior: NSIS installation upgrades/repairs in place without uninstall-first maintenance choices; both scopes and original location/data handling retained. Initial launcher hides chooser, shows startup feedback during restore, reveals controls only for first run/explicit switch/setup/failure. Existing preference format/data identity unchanged.
- Bun19tests/116assertions PASS. Earlier actual browser delayed-script comparison: old41visible chooser frames, new0. Interactive Windows current-user cross-version upgrade/reinstall preserved remote/local choices, installation/user-data sentinels and production local database. Two independent install/startup reviews PASS. Prior receipts .temp/install-startup-acceptance/ retained.
- Full exact-source CI40897293670:all5jobs PASS, total coverage90.6%/all module floors, real MySQL/PostgreSQL integration included. Logs .temp/rc5-ci-coverage.log and rc5-ci-integration.log.
- Release40898953799 SUCCESS:all21applicable jobs PASS, append-only job intentionally skipped. Windows both-scope installation/uninstallation, macOS Intel/AppleSilicon and Linux amd64/arm64 native desktop packaging/startup/service acceptance PASS;6Runner targets PASS. Final native logs .temp/rc5-final-*.log.
- Public23asset set complete; anonymous full downloads, exact GitHub/SHA256 checksums, formats, six signature/version bindings, complete portable resources and candidate feed equality PASS (.temp/rc5-public-release.json). Reviewed UTF-8 release notes published, including actual public upgrade receipt.
- Actual public rc4 portable client verified against published hash/signature/version, then discovered rc5 public feed, downloaded real signed ZIP, installed exact rc5 executable/full resources, restarted, retained saved remote connection and data sentinel, and reported rc5/current. PASS .temp/rc5-production-upgrade/receipt.json. No real account database initialized or user service contacted.
- Anonymous public API/Web GHCR dualarch indexes, matching attestations and both keyless cosign signatures PASS. Identity release.yml@refs/tags/v0.1.0-rc.5. Receipts .temp/rc5-public-images.json, rc5-cosign-api.json, rc5-cosign-web.json.
- Prior rc1/rc2/rc3/rc4 release IDs, asset IDs/sizes/digests and annotated tag objects unchanged (final baseline comparison PASS).
- Guarded cleanup21targets PASS:1,120,908,129logical bytes removed; measured D-volume free38,830,731,264to39,952,453,632bytes (1,121,722,368physical bytes reclaimed). Removed downloaded payloads/tools and owned native profiles/data; production signing backup, scripts/logs/receipts and unknown development data preserved. No owned desktop/service processes remain.

## Execution / Recovery Notes
Direct release execution; reused prior independent reviews, no new application edits. Native release ran in parallel with remaining source CI after observing prior coverage27.6min/packaging20min; both passed before publication completion. Whole-file prerequisite downloads were slow, switched owned tools to resumable512KiB ranges with final full hashes; no shipped downloader changes. Public checks and actual native upgrade ran in parallel after independently verifying the new ZIP. No source/tag recovery or artifact replacement required.

## Known Limits
Real UAC/all-users upgrade and full macOS/Linux upgrade UI flows NOT_TESTED; hosted initial install/startup checks passed. macOS ad-hoc signed/not Apple notarized; Windows lacks publisher code signing. Older pre-updater versions still need one manual installation. No claim of power-loss/helper-kill recovery or database rollback verification.

## Self-Review / Final Acceptance
Requirement/Scope/Reuse/Architecture/Regression/Complexity/DurableState/Testing/CommitHygiene PASS. Only state docs changed after executable verification; final docs commit uses [skip ci] to avoid rerunning executable CI. All cleanup paths owned/ignored/checked, no binaries/keys/reports staged. Historical checkpoints below are superseded by this receipt.

## Next Action
Nothing outstanding for approved rc5 release/public verification/cleanup. Final receipt committed/pushed to GitHub main, working tree clean. Installed rc4 clients can use native Help/Check for updates. Do not recreate deleted downloads/build caches or move published tags without a new task.

## Last Checkpoint
2026-10-29 rc5 publication, actual rc4-to-rc5 upgrade, public integrity checks and owned cleanup PASS.

---
# Previous Local Task - In-place installation and startup chooser flash

## Objective / Confirmed Requirements
User requests no uninstall-first choice in Windows installation, preservation of the last connection after installing a new version, and no connection-page flash on every startup. Prior rc4 release is immutable. No new publication authorized for this revision.

## Phase / Current Step
ACCEPTANCE COMPLETE / PASS for local implementation, NOT_PUBLISHED. NSIS maintenance page defaulted to uninstall-first; launcher rendered chooser before restoring browser-local connection. Fixed with Tauri custom template pinned to upstream2.12.2, changing only NSIS maintenance policy; WiX compatibility/both scopes retained. Initial HTML hides chooser; neutral progress during restore, chooser only on first run/explicit switch/setup/failure. Identity/data paths, preferences format and native permissions unchanged.

## Execution Mode / Scope
Direct implementation plus two independent read-only reviewers under deep-review. Existing launcher storage/startLocal and official NSIS lifecycle reused. No backend/business API/schema/dependency or published artifact changes. Template provenance/hash and synchronization instructions recorded in template and desktop README.

## Verification
- Bun19tests/116assertions PASS. Actual old/new Windows NSIS builds PASS with locked dependencies/production local resources. Only desktop affected; unrelated backend/Web business CI not repeated. No Rust source changes.
- Independent install_compat_review and startup_flash_review PASS: NSIS Abort skips leave callback, scope/location/data/IPC behavior preserved. Official NSIS callback source/local MultiUser implementation checked; no confirmed findings.
- Actual browser first-paint regression with800ms delayed modules: old chooser41visible frames, new0. Evidence .temp/install-startup-acceptance/first-paint-receipt.json PASS.
- Real interactive Windows upgrade old0.0.2-rc.1 to new0.0.2-rc.2 and same-version reinstall PASS: no uninstall maintenance choices; original install-directory/user-data sentinels preserved; new client restores old remote selection; local admin setup/production login succeeds; reinstall restores same local login/database; explicit native menu reveals chooser and hides progress. Three controlled main processes exit0. Evidence .temp/install-startup-acceptance/native-receipt.json, installer-pages.json.
- Windows fixture uses isolated neutral identity/current-user scope and process-local RunAsInvoker. Actual UAC/all-users and native macOS/Linux NOT_RERUN; unchanged both-scope paths reviewed and both-mode installer compiled. Do not claim new all-users/UAC acceptance.
- Tooling retries: absolute fixture frontendDist parsed as file URL (fixed relative directory); NSIS moved window to Default desktop (fixed owned-window enumeration/hiding). Aborted receipts marked ABORTED_TOOLING, not product failures/passes. Installer finish auto-launched owned fixture processes; both closed before final cleanup. No unrelated process stopped.
- Cleanup PASS: fixture uninstalled/registry removed; sentinel retained by uninstaller before owned cleanup. Five generated targets removed,5,092,073,114logical bytes. Build/resources, fixture installers/data removed; scripts/logs/receipts retained (.temp/install-startup-acceptance/cleanup-receipt.json). Production key/real data untouched. No owned app/service/installer processes remain.
- Self-review and original-requirement acceptance Scope/Reuse/Architecture/Regression/Complexity/DurableState/TestEvidence PASS. git diff --check PASS; no staged build artifacts. No unnecessary dependency/native privilege/storage system added.

## Next Action
Deliver local fix and verification; no implementation work outstanding. Source changes uncommitted, no new public release. Public rc4 remains immutable with previous installer/startup behavior. Future authorized publication must use a new version (recommend rc5), never replace rc4 assets/tag. No new publication/version decision requested for this revision.

## Last Checkpoint
2026-10-28 local installation/startup acceptance and owned cleanup PASS.

---
# Release v0.1.0-rc.4 - Delivery Receipt

## Objective / Authorization
User authorized publication of the verified product-flow repair and signed desktop updater, followed by disk cleanup. Repository and GHCR remain public. Prior releases are immutable.

## Phase / Current Step
ACCEPTANCE COMPLETE / PASS. Release: https://github.com/7link-team/7link-platform/releases/tag/v0.1.0-rc.4
Application tag remains dbbc936aae77513ee798b7cd98fd735e8b53714c. Workflow commit930d517f1b0cb50ec451a446f018148ed70775d3. Final Release40729416854 SUCCESS:21applicable jobs passed, one append-only job intentionally skipped.

## Completed Work / Verification
- Full hosted CI40717585189 and40723975366: all5jobs PASS; coverage90.6%, real database gates included.
- Windows installed/portable, macOS Intel/AppleSilicon, Linux amd64/arm64 desktop native packaging/startup/service acceptance PASS;6Runner targets PASS. Final Windows/macIntel/macARM logs: .temp/rc4-final-*.log.
- Public Release23assets: anonymous complete downloads, exact SHA256/GitHub digests, formats,6signed target signatures/version binding and candidate feed equality PASS (.temp/rc4-public-release.json). Reviewed UTF-8 release notes published.
- Actual public-feed Windows portable upgrade PASS: discovered rc4, downloaded actual signed public ZIP, replaced executable/full resources, restarted, preserved remote choice/data, reported rc4/current. Receipt .temp/rc4-production-upgrade/receipt.json. Existing Windows current-user installed upgrade/security/failure-path acceptance remains PASS.
- Public API/Web GHCR indexes both amd64/arm64 and matching attestations; anonymous cosign keyless signatures PASS (.temp/rc4-public-images.json, rc4-cosign-api.json, rc4-cosign-web.json).
- Final baseline compare: rc1/rc2/rc3 release IDs, asset IDs/sizes/digests and tag objects unchanged.
- Guarded cleanup31targets:11,919,891,520logical bytes removed (11.10GiB); D-volume free increased from37,008,351,232to46,084,018,176bytes (9,075,666,944physical bytes reclaimed). Generated build resources, task payloads/tools/profiles removed. Protected signing backup, logs/receipts and unknown development databases preserved. No frontend-app/local-service/product-flow-api processes remain.
- Earlier packaging recovery: DMG-only omitted updater archive, fixed app,dmg; busy read-only acceptance mount fixed owned-volume normal/force detach. Focused actionlint/Bash probes and independent reviews PASS. First ARM capacity cancellation resolved in final run; no application tag moved.
- Independent scoped release/security/recovery reviews and final main self-review/acceptance PASS. Only state documentation changed after executable verification; final state commit uses [skip ci] to avoid duplicate executable gates.

## Known Limits / Confirmed Constraints
Old clients lacking updater require one manual installation of rc4. Real Windows UAC/all-users upgrade and complete macOS/Linux upgrade UI paths remain NOT_TESTED; hosted installation/startup checks passed. macOS ad-hoc signed, not Apple notarized; Windows has no publisher code signature. Sudden power loss/helper termination and future database rollback not verified. No new behavior or unrelated cleanup added.

## Next Action
Nothing outstanding for approved release/public verification/cleanup. Final state documentation committed/pushed to GitHub main; working tree clean. Do not recreate deleted caches without a new task. Historical checkpoints below are superseded by this receipt.

## Last Checkpoint
2026-10-28 release/public update acceptance/cleanup COMPLETE.

---
# Active Desktop Updater

## Objective / Authorization
User explicitly asks why automatic updater still absent; implement it now, including real client check/download/signature/install/restart and release artifacts/feed. Prior repair batch remains local and verified below; do not discard it. Prior published releases immutable.

## Current Phase / Step
ACCEPTANCE: local implementation PASS with Windows portable and installed real upgrade verification. Public delivery NOT_PUBLISHED. No new release version selected/publication performed.

## Requirements / Design
- Startup background version check, clear native update UI when a newer version exists; manual native menu entry. User confirms installation/restart. Works in local and remote mode; remote Web cannot invoke updater commands.
- Use official Tauri2 updater for version/signature/download and native installed formats. Windows raw portable ZIP must stay portable: signed ZIP download, validate/extract stage in executable folder, hidden helper waits for app exit then swaps only executable/local resource tree, rolls back failure, restarts. Stop owned local service before any installation.
- Release build injects public key and update endpoint from configuration/repository metadata, never business server URL. Stable/candidate feed split because GitHub latest excludes prereleases. Signed native NSIS/AppImage/macOS app archive plus signed Windows portable ZIP; per-version immutable manifest and mutable channel feed after release.
- No backend/business/schema changes for updater. No keys in repo/logs. Existing source and bundles' identity/data paths preserved.

## Plan / Execution Mode
Direct implementation while client/release contract is tightly coupled; evaluate independent security/platform/release reviews under deep-review after implementation. Reuse native UI CSS and lifecycle, official updater crate, existing packaging. Files: frontend-app native updater/local helper+launcher updater UI, config/lock/deps, existing release workflow + focused release helper/tests/docs, MEMORY/ADR. Do not touch prior backend repair files.

## Verification / Known Risks
Bun desktop tests 16/106 assertions PASS; cargo test 3 PASS. Existing independent security review found no confirmed defect; release review found feed retry coupling, now code has separate desktop_update_feed job (needs root verification). Windows portable real acceptance PASS: .temp/updater-native-acceptance/receipt.json eight steps (startup, actual local service, remote IPC denied, bad signature rejected, signed-version mismatch rejected, 503 retry, exactly-one download/full swap/restart with data preserved, new version current). Earlier acceptance failures were tooling: corrupted untrusted comment rather than signature, and overlapping runs; rebuilt clean fixtures, no concurrent run, corrected actual signature mutation. NSIS signed debug fixture builds PASS; .temp/updater-native-acceptance/installed-receipt.json eight-step real installed upgrade PASS. Uses isolated neutral product, current-user scope and process-local RunAsInvoker; real UAC/all-users upgrade NOT_TESTED. Isolated installed application uninstalled, registry entry removed, owned app processes closed. actionlint PASS; final Bun16/106 PASS; cargo test --locked3 PASS; cargo clippy --locked --all-targets -- -D warnings PASS; cargo fmt --check and git diff --check PASS. Clippy initially flagged stop function after prior test module, moved function unchanged above tests, then all gates green. Release reviewer independently verified separate feed job recovery and idempotent partial stable/candidate repair. Protected production signing key backed up outside repository; GitHub private Secret/public variable configured successfully, no release published. Official documentation/source downloaded to ignored .temp/updater-official-*.txt. Native updater's Windows install calls process::exit: on_before_exit must explicitly stop local child. Portable official Windows path only handles NSIS/MSI, so safe signed ZIP installation needs bounded helper. GitHub CLI absent in Windows PATH; existing ignored delivery helper obtains credential via git credential fill without output. Do not print credentials.

## Next Action
Local source acceptance complete. Public delivery remains: select next immutable version (recommend v0.1.0-rc.4 after verifying latest tag), review/stage only intended product-repair/updater source, push and run native release workflow, verify six signed update targets/immutable manifest/public channel feeds, then perform macOS/Linux real upgrades and real Windows UAC/all-users acceptance on appropriate hosts. Never replace rc3 assets. No commit/push/release done this turn.

## Self-Review / Final Local Acceptance
- Scope/reuse/architecture/regression/complexity PASS: official updater used; portable helper only for unsupported raw ZIP format. No business/API/schema changes introduced by updater; prior repair batch preserved.
- Main inspected updater IPC/state/signature/install/service lifecycle, six-target workflow naming and separate feed retry. Independent security review and release recovery re-review PASS. A3 interruption/failure recovery, A4 remote IPC boundary, A6 duplicates, A7 existing local data preserved verified in real Windows tests; A1/A2 signed full bundle and complete extraction tested; A5 no business arithmetic; A8 mutex protects checks and installs. Sudden power loss/helper termination and future schema rollback not verified.
- Source implementation acceptance PASS. Published release acceptance NOT_RUN; macOS/Linux native runtime and actual UAC/all-users NOT_RUN. Do not describe these as delivered or tested.
- Generated artifacts/test credentials/keys remain ignored or outside repository; no build outputs staged. Runtime native test profiles/data are isolated from user data. Working tree intentionally includes prior unstaged product-flow repair.

---
# Completed Product Flow Repair

## Objective
Fix reported desktop restart chooser, login landing in personal center, unusable system admin entry, missing registration control, raw principal IDs in tenant owner/audit/export, unclear job selection and indistinguishable fanout tasks. Prior rc3 release is immutable and complete (receipt below).

## Current Phase
ACCEPTANCE - COMPLETE / PASS for this local repair batch

## Confirmed Decisions
- User confirmed account registration defaults OPEN for new deployments; system admin can enable/disable in UI. Preserve explicit existing policy/config. Tenant self-registration separate/unchanged. User further CONFIRMED account/password registration with OPTIONAL email verification: when email verification off, signup/login works without mail and email stays unverified; when on, keep existing pending/mail proof flow.
- Restart restores last chosen local/remote connection. Explicit native server chooser remains available without reconnect loop. First run still chooses/configures connection.
- Generic login should discover accessible work home; explicit deep links and scope-specific entrances must preserve intended targets and access control.
- Principal display uses readable nickname/account; internal IDs remain identifiers, never silently converted to numbers. Old audit chains must remain immutable; enrich read views/exports through authorized batched lookup.
- Job selection actually means retry failed jobs; fix affordances and expose distinguishing scheduled target context. Do not infer stuck jobs solely from pending screenshot or alter schedule cadence unasked.
- Automatic updater remains pending from previous turn; don't claim delivered. This stage prioritizes active reported flows; no new release/commit authorized yet.

## Plan / Execution Mode
Root directly implements desktop/home routing;3bounded read-only deep-review investigations registration_flow/readable_identity/jobs_usability determine minimal owned changes. Main owns contracts/integration/acceptance. Apply backend-module/ui-ux-pro-max/agent-browser for affected work.

## Findings
- Launcher currently intentionally always chooser, has no last connection persistence; native menu returns identical launcher URL.
- Generic login resolves user entrance workspace and user/files, bypassing workspaceHomeLoader's existing permission discovery.
- Tenant owner initialized from preferred_username-or-subject; missing tenant early return prevents system principal lookup, yielding ID(ID).
- Audit renders raw event/actor/target; job selection only DEAD+handler batch retry but unclear heading/action position.

## Implementation / Evidence
- Root launcher persists validated lastlocal/remote choice, restores on restart; native menu #choose suppresses restore and normalized chooser guard preserves native boundary. First run and invalidstate keep chooser. Desktop Bun11tests/49assertions PASS, cargofmtcheck PASS.
- Root generic user entrance workspace now redirects permission-discovery root, which checks system authority before tenant APIs; existing account-view/deep-link paths preserved. System home renders only effective-menu actions with purposes. Focused routing/home tests PASS including tenantdiscoveryfailure with systemauthority.
- registration_flow implementation: authnowned singleton policy table3dialects/milestone21; GET/PUT /system/settings/registration guarded platform_administer, desiredenabled/readinessavailable split; defaulttrue/explicitfalsepreserved; optionallogin_name/email, responseverification_required, publiccapabilityregistration_email_verification; real directidentitycreator with no verifiedmail/privileges. Full authn/identity/pkgconfig tests and realcomposition registration/login/policy test PASS. Root added settingsUI, typedAPI methods, registerform direct/verified flow+closedstate; contracts regenerated.
- readable_identity implementation: selfprofile/permittedpagedsystemcandidate ownerchooser; shared readablePrincipalLabel; auditauthorizedbatchedcurrent actor/target_principal/viewed_principal additions to reads/NDJSONexports, immutablechain/privacystream unchanged. Full audit/identitytests+build PASS; fieldpolicy/tenant/deleted/erased/SActests PASS; frontendlabel test/scopedlint PASS.
- jobs_usability implementation: retryselection label/help +toolbar action +separatedetails, nulltenant Systemwide, fanoutsnapshot schedule/targetlabel/code/firetime in desktop/mobile/detail. Full jobstests PASS47.8s; APIfive41assertionsPASS; scopedlintPASS. Actual agentbrowser DOM ID125 visible; permanent isolated API-double browser script PASS retry/readonly/mobile/context. Own8103Vite/browserclosed.
- Root shared3locale keys added for home/settings/jobs/identity/common audit actions/targets. OpenAPI + frontend API regeneration PASS; first integrated typecheck PASS.

## Verification / Active Work
- Backend ALL applicable CI targets PASS, via initial invocation plus targeted fixes/resumption. Original make ci was not green: first inventory/mapping errors, then identity89.6% floor, then strict architecture contracts. All fixed: exact368operation manifests; meaningful identity input/no-writes/rollback/authorization/field/storage tests; coverage refresh identity90.0%/total90.5% with unchanged thresholds; moved composition test into bootstrap_test.go; narrow direct registration wiring classification with positive/negative tests. Three mutations rejected; originals restored and identity suite PASS. Final fmt/lint/build PASS; integration all packages PASS (architecture recheck171.529s); composition3.198s PASS; full generate-check EXIT0; authn21files extra byte fingerprints PASS. Final OpenAPI summary corrected via generators; openapi-check/frontend-contract-check/typecheck EXIT0. Evidence apps/backend-api/.temp/product-flow-{coverage-refresh,architecture-recheck,composed-registration-final,backend-contracts-recheck,generate-check,authn-generate-fingerprint}.log.
- Real MySQL8.4.11 full gate PASS, strict performance authorization3.979ms/session2.639ms/scim51.315ms/audit2.664ms all below enforced thresholds. Three-engine migrations PASS; PostgreSQL performance harness PASS but some optional reference latencies exceeded (not an enforced latency PASS). Logs apps/backend-api/.temp/product-flow-*.log. schema.lock extended after MySQL PASS; all279 prior entries unchanged,9 appended (3new policy +6previous unrecorded core/webhook). Static81IAMtables/37crossFK PASS.
- Final frontend-check via Python subprocess session87893 explicitly EXIT0 PASS: API contracts/lint/typecheck/171UI+836Web tests/production build. Log .temp/product-flow-frontend-final.log.
- Desktop prepare:local, cargo build, cargo test PASS (1native test); prior cargo check failed missing generated resources, corrected by existing prepare script. Windows native11step acceptance PASS .temp/product-flow-native-1793196638716088900-receipt.json: local first setup/login, restart same local origin, native #choose stable, remote persistence/restart, remote start_local denied;3owned processes exit0 and ports closed. Initial2tooling-only aborted receipts retained as ABORTED_TOOLING.
- Independent registration/security and desktop/login/frontend read-only reviews PASS, no confirmed defects. Final structured API review PASS .temp/product-flow-api-review.json:1343→1345operations,12affected/8schemas, no removed/changed existing parameters/security/status; new setting requests strictly required enabled bool; runtime exact368operation/path tests PASS.
- Actual browser against new compiled local service: admin root lands /system with permitted action links; registration setting off hides form, on restores; no-email account registered and logged in; tenant owner choices flow-owner and Registration acceptance(flow-registered); audit readable actors/targets and intact chain; actual NDJSON export fetch includes account/display names PASS. Jobs show retry-only selection/help and source schedule/target/fire time for distinct fanouts. Browser sessions product-flow-root/product-flow-registration; isolated service data/credentials only in ignored .temp.
- No commit/push/release this stage. Updater still unimplemented.

## Remaining Work / Next Action
Nothing outstanding for this local repair batch. Final root receipt .temp/product-flow-final-acceptance.json PASS; self-review and original current-batch requirement acceptance complete. Both root browsers, isolated local service and native acceptance processes closed. Source remains local/unstaged; no new release/commit. Separate pending user request: automatic updater is NOT implemented; published rc3 does NOT contain this batch. A future continuation must handle updater and a new immutable release, never replace rc3 artifacts or claim existing packages updated.

## Final Self-Review / Acceptance
- Main reviewed new identity tests and narrow stable-wiring extension: exact DirectRegistrationPrincipalCreator classification alongside existing registration creator; arbitrary/state providers still rejected. Bootstrap registration test moved into existing bootstrap_test.go to honor file-pair rule.
- Main reviewed irreversible-pattern relevance: A1 no staged bulk-write/cache flow introduced; A2 existing bounded retry unchanged; A3 native setup/restart/chooser recovery verified; A4 label read/export permissions/tenant/field/anonymization tests and independent probes PASS; A5 no numerical business calculations touched; A6 duplicate registration/credential rollback/policy rollback tests PASS; A7 existing279migration hashes preserved, explicit config disabled remains, nullable label additions; A8 scheduling cadence/worker execution unchanged, read-only fanout context and retry affordances verified.
- Integration packages goose/testdb/lifecycle/migration/performance PASS in original remaining run; architecture-only and moved app regression reruns PASS. Full generate-check EXIT0; authn21files extra fingerprint covers untracked policy query byte-for-byte PASS.
- Root actual-browser receipt .temp/product-flow-browser-receipt.json; both named browsers and isolated API service closed. No public artifact changed; no staged files.
- Self-review Scope/Reuse/Architecture/Regression/Complexity/DurableState/CommitHygiene PASS. Current-batch acceptance desktop reconnect/admin landing+actions/registration defaults+toggle/account-password+optional email/readable identities+audit export/job retry+fanout distinction PASS. Automatic updater explicitly outside completed batch and remains outstanding.

## Last Checkpoint
2026-10-28 all current-batch gates, real browser/native acceptance, independent reviews and final root reviews PASS; receipts persisted; no commit or new publication.

---
# Active Release Task

## Objective / Authorization
Publish approved v0.1.0-rc.3 administration feedback corrections; keep repository/GHCR public, include all existing desktop/Runner targets, preserve rc1/rc2 artifacts, then clean generated disk usage.

## Current Phase
ACCEPTANCE - COMPLETE / PASS

## Current Step
Release, anonymous public verification, disk cleanup, self-review and original-request acceptance complete. No execution work outstanding.

## Completed / Verification Evidence
- Source51ef4372011398dee1cca648f37a7b3503f9df9c: application commit678672b9 plus minimal test-fixture isolation51ef4372. Exact-source CI40643944023 SUCCESS/all5jobs; original CI failure was isolated stale SQLite schema fixture metadata, corrected without production change.
- Annotated immutable tagv0.1.0-rc.3 points to that source. Release40646265758 SUCCESS/all required jobs; append-only mode correctly skipped. Public prerelease426361859: https://github.com/7link-team/7link-platform/releases/tag/v0.1.0-rc.3 . Prepared notes applied and anonymously compared byte-for-byte.
- All6Runner native targets and Windows installer/portable, macOSIntel/AppleSilicon DMG, Linuxamd64/arm64 AppImage PASS. Hosted checks include both Windows installation scopes/uninstall cleanup, native startup and each packaged local service. macOSIntel build slow but completed successfully; no workflow/source workaround.
- All14public assets (12binaries+2checksum manifests) anonymously downloaded: full sizes/SHA256/GitHub digests/native formats PASS. Windows Runner version PASS. Actual downloaded portable local service firstadministrator/WebAPI/restartpersistence/stableorigin/parentEOF PASS. Receipt .temp/rc3-public-receipt.json.
- Anonymous GHCR API/Web indexes: amd64/arm64, exact source/version, nonroot, SBOM/max provenance PASS. Anonymous cosign identity release.yml@refs/tags/v0.1.0-rc.3 PASS. Receipts .temp/rc3-image-receipt.json, rc3-api-signature.json, rc3-web-signature.json.
- Priorrc1/rc2tags, release IDs/assets/full metadata and API/Web image digests unchanged against .temp/rc3-baseline.json; preservation receipts PASS. Source repository anonymously confirmed PUBLIC.
- Cleanup .temp/rc3-cleanup.ps1 -Apply removed7explicit own generated targets: public downloads/extracted payload, cosign tools, anonymous docker config, temporary OpenAPI export, coverage profiles and range probe. Removed logical902988992bytes; actual Dvolume reclaim903434240bytes (~903MB/0.84GiB); free afterward48691818496bytes. All target paths absent; all7snapshot project service/Vite processes retain IDs/names/executable paths. Receipts .temp/rc3-cleanup-receipt.json and rc3-processes-before-cleanup.json.
- Prior independent release/feature reviews PASS; final self-review Scope/Reuse/Architecture/Regression/Complexity/DurableState PASS. Original-request acceptance PASS. Final receipt .temp/rc3-final-acceptance.json. gitdiffcheck PASS; nothing staged; onlyMEMORY.md has local progress changes. No application deliverables outstanding.

## Confirmed Constraints / Limits
- Old releases remain immutable. Keep receipts/logs and existing live development services/data.
- Registration remains default-off via existing config; no runtime settings UI added.
- This candidate has no automatic updater. macOS is ad-hoc signed/not notarized; portable Windows needs WebView2.
- Additional operating-platform creation retired; legacy scopes/members/grants preserved. Full singleton authorization/data convergence remains separate, unapproved work.
- No extra product changes, migrations, dependency changes or workflow redesign in release orchestration. Followup51ef4372 changes only a legacy test fixture's connection isolation.

## Delegation
Release orchestration Direct; existing bounded read-only rc3_release_audit review PASS. Prior feedback reviewers' evidence preserved below. Main agent retained acceptance.

## Remaining Work / Next Action
Nothing outstanding for this authorized release/cleanup. Deliver release link and results. MEMORY.md remains a local handoff update; source commits/tag already public. Any future updater/full singleton convergence/runtime registration UI requires separate scoped work.

## Last Checkpoint
2026-10-28: published rc3, all public/native/compatibility checks PASS, bounded disk cleanup PASS, final reviews PASS.

---
# Verified Revision Task

## Objective
Correct rc2 feedback: readable identity; consistent account actions and typed filters; readable task/schedule/event origins and purposes; retire additional operating-platform creation while preserving legacy scopes. No new release published.

## Current Phase
ACCEPTANCE - COMPLETE / PASS for the approved bounded revision.

## Current Step
Implementation, real browser checks, all applicable gates, independent reviews, self-review and final acceptance completed. Changes remain local and unstaged; no commit/push/release.

## Confirmed Constraints / Decisions
- One operational administration; stop public creation; preserve existing scopes/members/grants. No automatic merge, deletion or full singleton authorization migration. ADR records the incremental scope superseding D89.
- Scheduler frequency/retry behavior, business events, authorization, SQL/schema, dependencies and public rc1/rc2 assets/tags unchanged.
- Registration already defaults false through authn.registration.enabled; tenant registration separately defaults false. No runtime registration settings UI approved or implemented.
- New publication/version remains undecided. Recommend a separate v0.1.0-rc.3 if user requests publication; never replace rc1/rc2 artifacts.

## Completed Implementation
- UserMenu reuses selfProfileApi for display name/account/email, avoids raw IDs, stale responses and personal-account-view cookie leakage.
- Accounts/jobs/events reuse AutoTable/TableFilterBar: actions align with list tools; condition selects and typed values replace raw operator strings. Date filtering preserves exact instants, milliseconds and repeated DST hour until edited. DateTimePicker editableTime is opt-in.
- Public operating-platform creation UI removed. Existing guarded POST deprecated, localized409, malformed IDs still422, no2xx in final OpenAPI. Internal provisioning and legacy CRUD/memberships preserved.
- Owning modules declare metadata for55job kinds/41schedules/25subscribers/301unique event types in three languages. Budget/intake have module-owned i18n bundles and lifecycle registration.
- Admin DTOs add module/display_name/description; subscribers add event_types. Shared ActivityLabel displays readable names/purposes then stable codes/module. Schedules show actual interval/cron/timezone/scope; pages distinguish service-owned work from sign-in and events from follow-up handlers.
- OpenAPI/client types regenerated and verified identical to final source.
- Fixed9dynamic declaration loops whose metadata previously reused one static key; injection and all-declaration identity assertions catch this class. Corrected10frontend Chinese strings written as literal question marks; three-locale assertions and Chinese rendering regression added.

## Verification
- Backend fmt/lint/schema lock/unit tests/coverage/integration/build PASS: .temp/feedback-backend-ci-final.log. Total coverage90.5%; all module floors pass. Integration architecture/goose/testdb/lifecycle/migration/performance all PASS.
- The full makeci invocation failed only at OpenAPI export because my temporary OPENAPI_WORK_DIR override was relative and go-C changes cwd. Resumed remaining openapi-check/frontend-contract-check with absolute workspace path: PASS/exit0, .temp/feedback-backend-final-contracts.log. Do not rerun expensive passed tests for this command-path issue.
- Frontend makefrontend-check final PASS: API types/lint/typecheck/171UI+834Web tests/production build. .temp/feedback-frontend-gates-final.log. PowerShell redirection reports native stderr as error records; actual tools all passed. Focused regression rerun with explicit exitLASTEXITCODE returned0.
- Targeted six-package backend regressions PASS: .temp/feedback-backend-regressions.log. Production job catalog translation integration PASS: .temp/feedback-catalog-integration.log.
- All301events/55jobs/25subscribers have identity-aligned names and purposes in all three locales. Independent activity reviewer reran clientwork injection and full production binding tests, PASS.
- Mutation verification: wrong loop label/missing job name/missing Chinese purpose all rejected; original/restored PASS (.temp/feedback-activity-mutation-receipt.json and feedback-binding-*.log). Frontend profile-cookie guard and DST-instant mutations also rejected (.temp/feedback-mutation-receipt.json).
- Chinese regression fails before fix and passes after:3tests/67assertions, .temp/feedback-chinese-regression-before.log/after.log. Changed strings in54locale files scanned:0corrupt strings.
- Actual agent-browser checks with explicit API doubles: readable identity/activity labels; typed filters; unknown historical job; actual Every60seconds/UTC/Each active tenant. Two legacy platforms retained, no create control despite create permission, edit updates ID501/version1 without create calls. Chinese schedule descriptions/labels verified. Mobile390px document width390px, readable activity name/purpose. Screenshot .temp/feedback-mobile-jobs.png. Fixtures do not constitute production-data acceptance; backend real handler/storage tests cover persistence/guard behavior.
- Final OpenAPI structural comparison: only POST /operating-platforms operation changes (deprecated/summary/responses), bearer/session security unchanged. Four admin response schemas gain metadata; other operations unchanged. Evidence .temp/feedback-api-review.json; independent platform/runtime/schema review PASS.
- No SQL/query/schema/dependency changes: separate real-engine/performance release gates not repeated; repository integration performance suite did run.
- gitdiff--check PASS; no staged files/build artifacts. Self-review Scope/Reuse/Architecture/Regression/Complexity/DurableState PASS. Final original-request acceptance PASS for bounded revision, not a complete singleton migration.

## Delegation / Cleanup
Direct implementation, two read-only deep-review reviewers: review_platform_retirement and review_activity_metadata. Both final PASS; acceptance retained by main agent.
Closed only task browser feedback-rc3 and verified Vite PID42500 (--port4318 --strictPort); removed four task scratch browser fixtures. Logs/screenshot retained, pre-existing development services/data untouched.

## Remaining Work / Next Action
No implementation or verification outstanding for this approved revision. Deliver results; stop before commit/push/publication. If publication is requested, user must choose/confirm new version (recommend v0.1.0-rc.3). Full singleton authorization/data convergence, runtime registration settings and automatic updater remain outside this revision.

## Last Checkpoint
2026-10-27 final acceptance complete; local changes ready for review, not published.

---
# Prior Release and Cleanup Receipt

# Task State

## Objective
Publish approved v0.1.0-rc.2 with selectable Local/Remote desktop and configured application name chaosplus, then clean generated disk usage after release acceptance.

## Current Phase
ACCEPTANCE ? COMPLETE / PASS

## Current Step
Release, public verification, post-release disk cleanup, self-review and final requirement acceptance completed. No remaining execution work.

## Completed Work
- Application source3891020c940027132fa3a2087c6bf51ca79580cd is immutable annotated tagv0.1.0-rc.2. Keep tag unchanged. Main2776dd8a64e3d3dab9e609d350b770232253181b contains only subsequent release-workflow fixes.
- Public release: https://github.com/7link-team/7link-platform/releases/tag/v0.1.0-rc.2
- Exact-source CI40438325988 PASS,90.6% coverage/all55module floors and real database gates. Latest main CI40512840215 all5jobs PASS.
- Final Release40512867916 SUCCESS. All6Runner targets; Windows installer/portable; macOSIntel/AppleSilicon; Linuxamd64/arm64 desktop PASS.
- Windows actual CurrentUser/AllUsers installation, packaged service, executable hashes, uninstall/files/registry cleanup PASS. macOS DMG/native architecture/ad-hoc signature/startup PASS; Linux AppImage extraction/architecture/native startup PASS. Every desktop packaged local service first administrator/WebAPI/restart persistence/stable origin/parentEOF PASS.
- All14public assets (12binaries +2checksum manifests) anonymously downloaded; exact sizes/fullSHA256/GitHub digests/formats PASS. Downloaded Windows portable service actually exercised with setup/WebAPI/restart/parentEOF PASS.
- Final public GHCR API and Web: linux/amd64 +linux/arm64, exact source/version/nonroot/SBOM/max provenance PASS. Anonymous cosign identity release.yml@refs/heads/main PASS.
- Oldrc1 release424286548/15assets IDs/sizes/digests and annotated tag372a6b69cb63cfe91bbe403ba69b216c47c4387e unchanged; old API/Web index digests unchanged.
- Postrelease .temp/rc2-cleanup.ps1 -Apply deleted79targets; all reported removed and paths absent. Actual D-volume reclaimed17,550,471,168bytes (17.55GB/16.35GiB); free immediately after53,171,687,424bytes (~53.17GB). Logical removed19,264,000,261bytes differs from physical reclaim due compressed/sparse files.
- All5original project processes and9related listening endpoints preserved, including protected backend services/Vite and existing Go process listeners. Source diff unchanged except this handoff. git diff --check PASS.
- Self-review scope/reuse/architecture/regression/complexity/durable state and final original-requirement acceptance PASS. No new delegation in final delivery; previous backend/native/packaging reviews already completed.

## Important Decisions / Confirmed Constraints
- App name chaosplus is packaging configuration from user-selected local branch. Keep bundle identifier for compatibility. Local mode reuses production Go/Web/SQLite, explicit first admin/no demo credentials, per-user persistent data, loopback stable same-origin hosting and child shutdown with desktop. Remote mode retains multiple runtime-configured deployment domains, same-origin /api through nginx, no fixed server address.
- Preserve Codeup origin, GitHub public repository and GHCR packages; never move existing release tags or replace published rc1 assets/images. Keep rc2 tag at3891020c.
- Workflow-only fixes521c9bba use native find instead of unavailable rg for Linux resources;2776dd8a waits for macOS process exit before DMG detach. Actionlint and focused regression fixtures PASS; final hosted workflow PASS.
- Cleanup was strictly workspace-local ignored build outputs/resources, acceptance downloads/tools, inactive test executables/coverage and closed old logs. Preserved reports/screenshots/configuration/databases, node_modules, app data, global caches, live process directories, and excluded WebView profiles. No services stopped.
- Automatic updater remains unimplemented/outside approved scope. macOS bundles use ad-hoc signatures, without Apple notarization; Windows portable requires WebView2.

## Final Image Digests
- rc2 API: sha256:85119f524750746346a7d0d33bc3762101fd2bd8f6400398acb6810d2f3db7ad
- rc2 Web: sha256:d0e7594f20895815055377edc00db474fc576766ffaec3f4f1de400a85f52d2e
- preserved rc1 API: sha256:36c063c603e3d410f7e0fde4e7c37ef237cc27b312472db36c09daa3ca9a0d7f
- preserved rc1 Web: sha256:a61287546a76410137805a77fc85ecf38cbe25b326bb1125b22a3cff4888dc82

## Verification Evidence
- .temp/rc2-public-receipt.json; .temp/rc2-image-receipt.json; .temp/rc2-api-signature.json; .temp/rc2-web-signature.json
- .temp/rc2-final-windows.log; .temp/rc2-final-macos-intel.log; .temp/rc2-final-macos-arm64.log; .temp/rc2-final-linux-amd64.log; .temp/rc2-final-linux-arm64.log
- .temp/rc2-cleanup-plan.json; .temp/rc2-cleanup-receipt.json; .temp/rc2-cleanup-live-before.json; .temp/rc2-cleanup-acceptance.json
- Anonymous full CDN downloads stalled on local route; bounded512KiB curl range downloads/resume completed all files with full hash verification. No product/published changes. Downloaded payloads and delivery tools/cosign were deleted only after successful verification; receipts and logs remain.

## Remaining Work / Next Action
Nothing outstanding for approved release and cleanup. MEMORY.md alone is locally modified as durable final handoff; do not push progress-only state and trigger redundant CI. Do not rebuild/re-download removed caches without a new task. Future feature decisions (including updater) require their own scope.

## Last Checkpoint
2026-10-27 final release/public verification/cleanup/live-service preservation PASS.
