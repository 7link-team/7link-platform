# Task State

## Task / Objective
Deliver authorized public v0.1.0-rc.1 candidate and substantially optimize CI without weakening tests/coverage. Public source, public GHCR API/Web images, six Runner archives, Windows both-scope installer and portable ZIP required. Candidate publication does not complete the prototype.

## Current Phase
TESTING

## Current Step
Tagged source5968e701 CI40246546640 allgreen. Release40254999593: six Runner assets built; Windows build PASS but installer hash acceptance FAIL, API image still building and web queued. Actual Tauri binary marker patch proved sole difference; release workflow comparator fixed/reviewed/tested locally. Commit/push ONLY workflow+MEMORY, then dispatch updated main workflow with existing immutable v0.1.0-rc.1 input. Never force-move tag. Build jobs still checkout green taggedSHA5968e701.

## Overall Progress
- [x] Public source repository https://github.com/7link-team/7link-platform created and implementation published.
- [x] Desktop runtime multi-server chooser/native return; same-origin nginx API; Windows both-scope NSIS + actual EXE portable ZIP implemented and locally exercised.
- [x] Original coverage aggregation optimization:55module scans/~49min -> hosted4.2..6.4s; module/total semantics preserved.
- [x] Independent backend CI checks/coverage/integration runners/cache groups, all gates preserved.
- [x] Necessary cold-cache architecture JSON stderr fix and historical rollback test's existing migration18 support-index repair verified/published.
- [x] New Linux pipe collector avoids unmerged aggregate on disk; actual1.18GB replay exact SHA/counts, bounded memory.
- [x] New Windows compatibility collection compacts temporary raw before final output; original default raw replaced with9.69MB compact result.
- [x] Current tools tests/vet/build,55coverage floors/total replay, fmt/schema locks/actionlint/LF checks and independent reviews PASS.
- [x] Real MySQL/PostgreSQL isolated lifecycle/archive verification: all4original gates PASS; owned resources cleaned.
- [x] Commit/push latest optimization + fixture isolation as5968e701.
- [x] Exact-head full hosted CI40246546640 green:checks2.7min/frontend3.2min/Runner1.7min/coverage23.9min/integration26.2min.
- [x] Push authorized v0.1.0-rc.1 tag at green5968e701.
- [ ] Finish Release40254999593 workflow.
- [ ] Hosted Windows /CurrentUser and /AllUsers installation/uninstall plus asset checks.
- [ ] GHCR API/Web packages PUBLIC; anonymous manifests/architectures/signatures and Release assets/checksums verified.

## Active Work / Execution Mode
Main owns implementation and final acceptance. Coverage reviewer completed bounded read-only comparison of current23.9min versus previous14.3min coverage runtimes; no causal collector defect found. Current summed Go package times1258.57s vs703.43s, final statistical phase2.78s. Do not assert host load or collector regression without causal evidence.
 Independent read-only coverage reviewer finished: no remaining findings. Worker /root/real_database_verification completed unchanged original external tests against fresh real MySQL8.4.11/PostgreSQL16.15 in owned remote Docker network: lifecycle7.43s/2.55s,archive0.09s/0.09s, allPASS. All owned containers/network removed and cleanup filters empty. Source archive HEAD e177147d (production/test source unchanged).
Remote disk-backed attempt hit ENOSPC before tests; tmpfs attempt hit noexec before tests; both cleaned exactly owned resources. Successful rw,exec tmpfs run used Go1.26.6 (image GOTOOLCHAIN=local), -p=1, Go8Gi/no swap/4CPUs and each DB1Gi/no swap. Evidence .temp/delivery-external-real-databases-final.log. Do not run further disk-backed cold builds on full remote root, prune or alter others' resources.
No local heavy test currently active. Full Windows make test-coverage session4367 was intentionally stopped as redundant; partial execution is NOT PASS. Owned launcher5048 and descendants terminated, unrelated server go5348 untouched. Exact-head hosted Linux run is mandatory for fresh full coverage and full integration.

## Confirmed Constraints / Important Decisions
- User already authorized public repository, pushes, tag/release, public packages and v0.1.0-rc.1. Do not ask again or require fixed API/server address.
- Preserve Codeup origin, local chaosplus and upstream. GitHub remote github; push git push --quiet github HEAD:main. No reset/stash/force/blind staging.
- Windows PowerShell, hidden children, UTF8/LF; one LOCAL heavy command at a time, Go GOFLAGS=-p=1. Do not touch live ports4303/8080/8091/8092/8087.
- Keep all tests/full atomic cross-package instrumentation,55floors/90%total, counts/deadlines/schema locks/real-engine gates. No published SQL changes or embedded emulators substituted for real MySQL concurrency.
- Desktop selects/saves/removes multiple HTTP(S) root origins at runtime, loads deployed Web directly. Web uses relative /api; nginx serves Web/proxies API through one origin. Native Connection>Servers(Ctrl+Shift+S) returns to chooser after refused connection too. No automatic failover/offline embedded Web.
- Web storage origin-separated; cookies obey Domain/Path/Secure and are NOT port-isolated. Switching/removing server does not log another out. Remote native invocation denied.
- Windows NSIS installMode=both; portable ZIP contains actual desktop EXE. WebView2 required, unsigned. Hosted scope/uninstall proof pending.
- Coverage collection uses Go's original final text merge, Linux dedicated pipe /dev/fd/3 + existing int64 aggregator. Preserve Go-owned auxiliary zero packages/subprocess processing. Other OS temporarily collect regular raw on OUTPUT volume, then compact. Per-test fragments/compiler/instrumentation cost remain.
- Rejected shared binary GOCOVERDIR/covdata approach: orphan zero blocks missing, early-os.Exit production subprocess count1->0 reproduced, official formatter silently saturates uint32 across pods while original int64 sums6billion exactly.
- Pipe parse errors must DRAIN remaining finite stream before close/Wait: os.Create(/dev/fd/3) opens O_RDWR, producer retains own read end, close-only can deadlock. Regression/reverse mutation prove this.
- CI fixtures now use separate lifecycle database from migration integration database; archive_ prefix separates archive tables. Existing tests/assertions and SQL modes unchanged.
- Automatic approval rejected recursive cleanup of six owned WebView profiles earlier; DO NOT retry by another mechanism. All owned native test processes closed.
- Automatic approval also rejected combined process-stop/recursive temporary-profile cleanup in this phase before execution. Separate verified process stop succeeded; DO NOT retry recursive cleanup. Retained C:/Users/Feather/AppData/Local/Temp/coverage-collect-1901935248/raw.out154,846,727bytes. No permission request needed for continuing authorized source/CI work.

## Completed Work / Verification Evidence
### Coverage and current change
- Original profile1,176,834,355bytes/15,795,297rows/129,126unique blocks/99.18%duplicate rows/max124copies. Compact unfiltered9,692,024bytes; filtered7,090,452bytes. Independent Python verified every statement/count,55module outcomes and filtered bytes equal original. Threshold rounding/generated-module-vs-filtered-total semantics preserved; total90.6%.
- Existing published converter local4.351s/make coverage-check9.507s, actual Windows original-input peak55.43MiB. Four earlier reverse mutations reject lost counts/generated exclusions/missing modules/unrounded thresholds. Evidence apps/backend-api/.temp/delivery-optimized-equivalence.json and delivery-coverage-mutations.json.
- NEW collect mode retains exact original go test timeout60m/count1/atomic/full-coverpkg and package list. Actual Linux1.26.6 and Windows1.26.9 real old/new fixture compare ALL block/statement/count values, requiring orphan zero block, cross-package sum and early-exit subprocess hit. Test/build failures reject and preserve previous output. Missing/malformed/nonzero producer, output/start errors and6billion count covered.
- Final Windows tests PASS10.229s; vet/build PASS. Expanded Linux tests/vet PASS1.607s. Reverse mutation removing malformed-stream drain reliably times out8s; .temp/delivery-coverage-pipe-mutation.log. Independent reviewer rechecked actual fix/source/pipes/processes/test strength: no remaining findings. A2 volume/A8 process lifecycle verified; business A1/A3/A4/A5/A6/A7 unaffected.
- Actual original1.18GB compressed replay gzip->dedicated pipe->collector PASS4.051s; MaxRSS53,460KiB(~52.2MiB); output9,692,024bytes SHA25692dfe8c6ae7b8b6e0c6bad2e09997ac0d6b5dfacfad71d313015d7b076ed3053 equals independently validated reference. No unmerged aggregate file created; disposable container removed. This is collection replay, NOT full suite timing. Evidence .temp/delivery-coverage-volume.log.
- Ignored default apps/backend-api/coverage.raw.out now compact9.69MB with same SHA; original exact data retained compressed .temp/delivery-coverage-original.raw.gz175,937,735bytes. Redundant owned transport tar removed.
- Current make coverage-check on previous verified full profile PASS/all55floors/total90.6%; this is replay/gate verification, NOT fresh full app-suite completion. Log apps/backend-api/.temp/delivery-stream-coverage-check.log.
- make fmt-check schema-lock-check PASS:279published migrations unchanged;285files/80IAMtables/37cross-module FKs reconciled. actionlint/diffcheck/tracked+working LF PASS. No production/SQL/dependency/API changes.

### Hosted CI / current blocker
- CI40203888389 on995b3bad: checks17.9min/coverage31.5min PASS; stats6.426s. Integration cold-cache stderr polluted JSON; corrected goList Output + separate stderr f5e80f3a, deterministic subprocess reverse mutation rejects old behavior; full architecture PASS210s.
- CI40206473266 onf5e80f3a: checks2.7min/coverage20.4min PASS; full nested suite PASS. Real MySQL rollback test omitted existing migration18 repair before historical14Down; corrected e177147d test cycle14->18->14->13. No SQL modified. Four-engine target PASS9.323s(realMySQL8.4.11 included); removing18 reproduces1553 on fresh real server.
- Latest published exact-head CI40209444796(e177147d) COMPLETED: frontend2.2min,Runner1.7min,checks2.7min,coverage14.3min PASS; full nested integration and realMySQL migration PASS. Stats4.195s/55floors/total90.6%.
- Its remaining external lifecycle step FAIL: bootstrap avatar principal101 append audit duplicated key. Migration tests seed principal101 in same integration DB. Current workflow creates fresh lifecycle DB in both services and changes only lifecycle/archive DSNs; all4real-engine gates independently PASS on clean lifecycle DBs; next full hosted run still required. Log .temp/delivery-optimized-integration-3.log.

### Prior delivery/runtime evidence
- Public source batches04ab3fd9/4ecbfe6f published. Four unexported Huma embedded path inputs fixed; real binding regressions/mutations and1343operation regenerated OpenAPI/client verified. nginx101m boundary requests verified.
- Earlier complete local backend gates90.6%/55floors; frontend UI169/Web825tests/98,702assertions; Runner build/tests/vet/Node3tests PASS. Full populated copied-runtime browser326s health200/API5xx0/locks0/ERROR0; workspace/project/work creation/detail/foreign-project404 PASS; source data unchanged.
- Desktop launcher4tests/21assertions and actual browser multi-origin save/persist/select/remove/storage denial/mobile/cookie rules PASS. Actual native EXE2hostnames/3same-origin API calls/4native returns including refusal; remote native command denied. .temp/delivery-desktop-chooser.png and delivery-desktop-native.py evidence.
- Local Windows release/NSIS build PASS~1.30MiB; EXE4,231,680bytes/ZIP1,689,083bytes with exact SHA match; ProductVersion0.1.0-rc.1. Both-mode scope selection generated. Official hosted scope installs/uninstalls NOT RUN.
- Real realtime MySQL8.4.11 P95=252ms/max1087ms; PG P95=3ms/max7ms; targetPASS62.334s. This compares MySQL polling(remote) with PG LISTEN/NOTIFY(local), not SQL speed. Embedded MySQL lost-snapshot probe established it cannot prove concurrency; real hosted gate required. No thresholds reduced.
- Reachable public history credential scan13,037blobs/424,618,159bytes:6benign fixture/vendor patterns, no credentials exposed/committed.

## Remaining Work / Release Acceptance
1. Finish worker real MySQL/Postgres lifecycle+archive tests; inspect actual assertions and cleanup evidence. Fix only proven in-scope blocker.
2. Final review/stage only7tracked delivery files; commit/push authorized change and monitor new exact-head full CI. No state-only push while active CI (concurrency cancels main run).
3. CI green and tag published. Monitor Release40254999593 (.github/workflows/release.yml); existing authorization sufficient.
4. Verify hosted Windows current-user/all-users install/uninstall, actual EXE ZIP, six Runner archives+checksums, version injection; download/anonymous asset checks.
5. Ensure both GHCR packages PUBLIC (repo visibility separate); anonymous linux/amd64+arm64 manifests and cosign keyless signatures verified. Credential repo API works; package API403 so visibility may need UI. Never expose credentials.
6. Final self-review and acceptance; candidate/full-product distinction and actual limitations reported.

## Useful Existing Tools
- python .temp/delivery-github.py '/repos/7link-team/7link-platform/actions/runs?per_page=3' (stored credential helper, no secrets printed).
- python .temp/delivery-ci-log.py JOB_ID .temp/LOG downloads completed logs; live log blobs404 is not internal progress evidence.
- .temp/delivery-tools/actionlint.exe; .temp/delivery-public-verify.py images|release prepared for anonymous publication checks.
- .temp/delivery-tools/cosign.exe v2.5.0 checksum/version verified. Identity https://github.com/7link-team/7link-platform/.github/workflows/release.yml@refs/tags/v0.1.0-rc.1, issuer https://token.actions.githubusercontent.com ; empty Docker config for anonymous verification.
- Agent-browser skill previously applied; CLI C:/Users/Feather/.bun/install/cache/agent-browser@0.39.2@@@1/bin/agent-browser-win32-x64.exe ; own named sessions only, all owned sessions closed. Native helper uses TemporaryFile output to avoid daemon inherited capture pipes.
- Remote test scripts use SSH key C:/Users/Feather/.ssh/iam-docker-10.0.0.100 to10.0.0.100; exact owned resources only, no prune or live mutations, no passwords in output.

## Remaining Product Scope
Authoritative requirements docs/PRD_FINAL.md + specifications; .temp/lanes/final-gaps.md198rows DONE157/PARTIAL27/MISSING10/SUPERSEDED4. DONE is bounded evidence, not full live acceptance. Preserve37remaining IDs:
- MISSING: P-C10 inbound mail/thread routing; P-C22 interactive execution/ADHOC association; P-C44 remote file edit/upload; P09-09 browser-closed Web Push; P12-16 Agent Yunxiao MCP; P-R04 Centrifugo presence/recovery; P-R05 global search/index lifecycle; P-R08 health incidents/remediation; P-R10 mobile clients/background push; P-R11 actual cc-switch reuse/config release/apply/rollback.
- PARTIAL infrastructure/UI: P-C47 preissued SERVER enrollment/K8S pools; P-C48 consistent authorized query/page/export/import/refresh/context; P-C49 virtualization and measured100k-row/100-user performance.
- PARTIAL collaboration/agents: P05-08 manual dispatch; P05-09 automatic dispatch/fairness/recovery; P05-10 heavy branches/comparison/synthesis; P05-19 external channel projection; P05-20 hooks/rich media; P05-21 spawn/adoption/orphan lifecycle; P05-23 persistent Agent inbox/interruption; P05-30 default-deny grants; P06-15 accepted-work skill distillation.
- PARTIAL shell/notifications: P08-09 catalog/preferences/DND/channel completeness; P08-10 canonical scoped shell/contextual conversation; P09-08 complete producer-to-recipient in-app/mail delivery/live acceptance.
- PARTIAL integrations: P12-05 automation dry-run/logs; P12-07 acceptance cases/plans/runs/frozen suites; P12-14 remote state-map/import/sync; P12-15 signed Codeup ingress/trusted Flow redlines/manual gates.
- PARTIAL cross-cutting: P-R06 repository-event compensation; P-R07 node telemetry/probes; P-R09 native desktop/release proof; P-R12 cross-domain change coordination; P-R13 conversation provenance/exact-position actions; P-R15 organization/invitation/handover; P-R16 multi-Human/Agent teams/history; P-R17 archive/recovery/retention/export/backup.

## Known Issues / Risks
Full hosted CI40246546640 green; Release40254999593 queued; tag published; packages/assets not published or verified yet. Full prototype37gaps above remain. Rejected cleanup artifacts retained and must not be retried via alternate deletion. Remote root filesystem full: own bounded tmpfs tests only.

## Next Action
Commit/push release.yml+MEMORY after final staged review; dispatch main release.yml with inputs tag=v0.1.0-rc.1 (existing authorized immutabletag5968e701). Monitor old+new release runs; new same-tag run may queue until old finishes. Verify corrected hosted scope/uninstall, image publication/public status,10assets/checksums/platforms/cosign. No application/tag/SQL change or repeated release-version approval.

## Browser/public package preflight
agent-browser core skill loaded. Owned candidate-public-5968e701 session opened authorized GitHub organization packages page; page shows Sign in (owned session unauthenticated), existing unrelated public package visible. New candidate packages not yet published. Session closed; no settings/auth changes or messages sent. Do not claim package public until anonymous verification.

## Last Checkpoint
Coverage stream implementation, large-input equivalence, failure mutation, Windows/Linux tools and replay floors verified; local full Windows repeat deliberately stopped and recorded; all4real-engine gates and cleanup verified; 5968e701 CI allgreen, v0.1.0-rc.1 tag published; Release40254999593 queued, artifact/public package acceptance pending.

## Exact-head hosted acceptance checkpoint
CI40246546640/5968e701 all5jobs SUCCESS. Fresh full coverage122main test-package summaries;55floors PASS,total90.6%; final statistics00:14:18.826->00:14:21.608=2.781s including tooltests1.419s. .temp/delivery-stream-hosted-coverage.log. Full nested integration17m12s and realMySQL migration6m58s PASS; both lifecycle/archive tests PASS15.36s/0.21s(MySQL),6.45s/0.24s(PG). .temp/delivery-stream-hosted-integration.log. Total integration26.2min/coverage23.9min; no claim that pipe replay4.05s is full test runtime. Final source acceptance PASS, Release acceptance pending.

## Coverage timing comparison limitation
Independent reviewer: Go1.26.9 calculates reported package duration BEFORE mergeCoverProfile, so final aggregate pipe backpressure cannot directly explain reported package-time increase. Same image/cache867,418,397bytes/flags/dependencies/source, different workers/regions; no CPU telemetry, so host-load explanation unproven. Both runs55floorsPASS/total90.6;53module percentages identical,events90.3->90.5/audit93.2->93.0 differ between fresh runs. Exact statement/count preservation claim applies to the original-input replay and deterministic fixture, NOT to nondeterministic fresh-run counts. No additional gate or source change justified.

## Release installer acceptance correction
- First desktop job121354869612 logs .temp/delivery-release-desktop.log: build actual NSIS1.29MiB+ZIP PASS; /CurrentUser scope/hive/location/version/uninstall-path checks passed, then installed EXE hash differs fromportable.
- Reproduced using local matching4,231,680byte portable ZIP .temp/delivery-windows-packaging/desktop-runtime-servers_portable.zip and extracted real NSIS payload .temp/delivery-nsis-payload/frontend-app.exe: portable exactly1__TAURI_BUNDLE_TYPE_VAR_UNK/0NSS, installed0UNK/1NSS; only replacing that fixed-width marker yields byte-identical payload. Older9MB ZIP is unrelated and correctly fails comparator, do not use it.
- Tauri-utils2.10.1 platform.rs documents binary patch; pinned CLI2.12.2 changelog2.12.0 documents patching beforebundle/restoring raw after. Preserve framework/application behavior; do NOT disable patching or weaken arbitrary byte comparisons.
- Current release.yml derives installed expected SHA by exactly1UNK->NSS replacement using byte-reversible Latin1; rejects missing/duplicate/already-NSS marker; every other byte must match SHA256. Existing scope/registry/version/uninstaller/cleanup assertions unchanged. Pure actual workflow block extracted in .temp/delivery-installer-hash-probe.ps1 PASS, mutation beyond marker rejected; independent reviewer reproduced. actionlint/diffcheck PASS. Expected local installedSHA D738E9F697F46DC7C828B9FDE07A37D8EEB3ABD230E9F46C9B38FDDFBED15962. No local installation performed.
- This is harness-only correction on main; immutabletag remains5968e701. workflow_dispatch main + inputtag builds same approved tagged application; old failed-job rerun would reuse old workflow, so use new dispatch after main push. Release same-tag concurrency cancel-in-progress=false will queue until old image build finishes.
