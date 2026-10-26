# Task State

## Task / Objective
Current authorized continuation: (1) substantially reduce cold ARM64 image build time using native runners; (2) user explicitly found missing macOS/Linux desktop assets, implement and publish them. Prior candidate/publication acceptance below remains valid and must not be reversed.

## Current Phase
TESTING

## Current Step
Implementation and local review complete, hosted acceptance NOT RUN. Native image4matrix+indexmerge/signing, Unix desktop4matrix+actualbundle/startup checks, and append-only publication implemented in existing release.yml. All original Windows build/acceptance body unchanged. Main staging/pushing ONLY5tracked files then dispatching images_only and append_desktop concurrently with existingimmutable v0.1.0-rc.1. Published10original assets/images/tag MUST remain unchanged; successful append yields15assets (4Unix binaries+newUnixchecksum added).

## Implementation Plan
- Goal/requirements: native amd64+arm64 API/Web cold builds, full multiarch indexes/SBOM/max provenance/keyless signatures unchanged; add real macOS Intel+AppleSilicon and Linux amd64+arm64 desktop packages with versioned names/checksums/public download. Preserve Windows two-scope installer+portableZIP and existing verified candidate assets/tag.
- Existing/reuse: existing pinned checkout/buildx/login/build-push/artifact/cosign actions, unchangedDockerfiles/CGO and locked Tauri, generic Rust desktop source, same release metadata and Windows job. No new dependency/action/product/API behavior.
- Decomposition/mode: Hybrid. Main implements native build4matrix +2merge jobs and Unix desktop4matrix + publication integration. Read-only desktop_platform_plan verifies platform prerequisites; independent deep-review before push. Shared workflow edits owned only by main.
- Native design: build API/Web each on matching ubuntu-24.04 amd64/ubuntu-24.04-arm, assert native uname; push platform results by digest; independent component+arch GHA caches. Native runtime smoke then upload each digest. After all builds PASS, collect exactly2digests per component, validate/merge full index retaining attestation references, publish normal tag and stable-latest policy, sign index bydigest. No QEMU. images_only diagnostic uses unique verify-RUN-ATTEMPT tag, skips release/Runner/desktop, cannot move publishedrc1/latest.
- Desktop design: retain Windows job; new Unix matrix macOS Intel+ARM DMG and Linux amd64+ARM AppImage on compatible native runners. Exact sourceTag checkout, lockeddeps, tagversion, architecture metadata/bundle checks; collect all desktop artifacts for normal releases. append_desktop mode builds all four Unix platforms and publishes ONLY new desktop binaries + a separate Unix checksum list into existing candidate. Verify exact existingrelease/tag and all original published desktop checksums/payloads before adding a separate Unix list; never replace any existing asset. Never clobber existingWindows/Runner assets, images or immutabletag.
- Files/change: existing .github/workflows/release.yml, docs/deployment.md, apps/frontend-app/README.md, MEMORY.md and ADR.md. No new tracked files planned; Dockerfiles, production app/Rust/Go/SQL/dependencies/frontend runtime remain untouched.
- Testing/acceptance: actionlint/diff/LF; meaningful local shell/index validation and failure mutations where practical; cold hosted native image-only run with real container smoke and anonymous perarch/source/version/SBOM/provenance/signature checks; measure cold ARM64 vs observed45..76minQEMU without claiming P95 from1run. Hosted all4Unix desktop builds, native package architecture/contents, checksum preservation and anonymous final appended assets. LatestmainCI green; final self-review/main acceptance.
- Risks/out-of-scope: unsigned/notarized macOS and unsignedWindows remain candidatelimitations; Linux glibc/WebKit runtime baseline must be documented. No mobile scaffolding, product gaps, usercode, deploymentaddress or existingartifact replacement. No newversion/tag required.

## Overall Progress (Current Continuation)
- [x] Existing architecture/platform prerequisites inspected; officialnativeRunner labels and locked Tauri nativebundles confirmed.
- [x] Native image pipeline and Unix desktop/append implementation, docs and ADR updated.
- [x] Local actionlint1.7.12/mode/index/publication invariants and reverse mutations PASS; independent reviews no remaining findings.
- [ ] Commit/push5delivery files and dispatch images_only+append_desktop without moving candidate/tag.
- [ ] Real native coldimage builds/runtime/index/attestation/signature/anonymous checks and measured optimization PASS.
- [ ] Real fourUnix desktop bundles/startup PASS; appendmissingassets+newUnixchecksum safely.
- [ ] Verify all15publicassets, original10assetIDs+bytes preserved and existingimage/tag digests unchanged; latestCI and finalacceptance.

## Prior Candidate Delivery (Verified)
- [x] Public source repository created: https://github.com/7link-team/7link-platform.
- [x] Runtime multi-server desktop chooser, native return, same-origin nginx /api; no fixed deployment URL.
- [x] Full Go coverage aggregation optimized,55module floors/90% total preserved; 1.18GB repeated aggregate compacted to9.69MB; Linux streaming avoids retained unmerged aggregate.
- [x] Real MySQL/PostgreSQL original gates PASS; migration/lifecycle fixture isolation verified.
- [x] Exact tagged-source full hosted CI PASS; immutable annotatedtag published.
- [x] Windows both-scope installer and real executable portable ZIP built; hosted install+uninstall proof PASS.
- [x] Both GHCR packages publicly readable and signed, both architectures verified.
- [x] Full automated Release SUCCESS and10assets published.
- [x] Anonymous checksums/download acceptance for all8binary assets+2checksum lists PASS.
- [x] Latest exact-main CI40330075270 all5jobs SUCCESS; final self-review and candidate acceptance PASS.

## Active Work / Execution Mode
Hybrid: main exclusively edited/integrates shared workflow and owns final acceptance. Three bounded read-only reviewers: desktop_platform_plan confirmed nativeSDK/bundles and Unixjob implementation; review_native_images checked native matrix/digest handoff/attestations/failure/permissions; review_desktop_append reproduced and reviewed append/publication invariants. All reviews complete, no remaining code findings. No local heavytests/services active. Hosted runs next.

## Confirmed Constraints / Important Decisions
- User already authorized public source/pushes/tag/release, public GHCR and v0.1.0-rc.1; do not ask again. GHCR visibility confirmed anonymous, no login/package permissions needed now.
- Preserve Codeup origin and local chaosplus; github remote push HEAD:main. No reset/stash/force/blind staging. Original tag MUST NOT move.
- Windows PowerShell/hidden native child processes/UTF8-LF; local Go GOFLAGS=-p=1 and one local heavy command at a time. Do not touch live ports4303/8080/8091/8092/8087 or unrelated server go5348.
- Keep ALL tests, full cross-package atomic instrumentation,55floors/90%total, counts/deadlines/schema locks/real-engine gates. No published SQL changes or emulator substituted for real concurrency.
- Desktop selects/saves/removes HTTP(S) root origins at runtime; navigate deployed Web, relative /api through nginx. Connection>Servers(Ctrl+Shift+S) returns even after refusal. No automatic failover/offline embedded Web; remote native invocation denied.
- Storage origin-separated; cookies follow Domain/Path/Secure and NOT port isolation. Switch/remove does not log another server out.
- Tauri NSIS installMode=both, actual desktop EXE ZIP, WebView2 required, unsigned. Both-mode framework requires administrator privileges; keep framework behavior.
- Coverage: Linux final go test text profile through /dev/fd/3 pipe to existing int64 aggregator; non-Linux temporarily collect regular raw on OUTPUT volume then compact. Go-owned fragments/compiler cost remain. Drain malformed producer stream before Wait (O_RDWR producer can otherwise deadlock).
- Rejected shared GOCOVERDIR/covdata alternative: lost orphan zero blocks and early-exit subprocess counts; official formatter saturates uint32 across pods vs existing6billion int64 sums.
- Migration DB stays integration; lifecycle uses fresh lifecycle DB in each engine, archive_ prefix for archive tables. All original assertions unchanged.
- Prior automatic approval rejected recursive temporary/WebView-profile cleanup; DO NOT retry through another deletion mechanism. Owned processes stopped. Retained C:/Users/Feather/AppData/Local/Temp/coverage-collect-1901935248/raw.out154,846,727bytes. Remote root filesystem full; no prune, other resources or further disk-backed cold builds. Successful owned tmpfs real-DB resources fully cleaned.

## Source / Release Identity
- Annotatedtag v0.1.0-rc.1 object372a6b69cb63cfe91bbe403ba69b216c47c4387e peels to immutable app5968e701081de5f64e0a78f4daf33a445b5702fd; local+anonymous remote peel PASS. Never compare annotatedtag object SHA directly to commit.
- Main latest72242925fb83fc347591ad6a8664314bb386b56a (minimal harness fix); diagnostic d5a6719f (desktop_only option and retain artifact before acceptance); previousb67bf17f (bundle marker expected-hash comparison).
- Release https://github.com/7link-team/7link-platform/releases/tag/v0.1.0-rc.1, prerelease=true,draft=false,10assets.
- Successful automated Release40330098967. Windows job121597006878 log .temp/delivery-desktop-accepted.log explicitly PASS /CurrentUser and /AllUsers at13:29:40/41UTC.
- Final API digest sha256:36c063c603e3d410f7e0fde4e7c37ef237cc27b312472db36c09daa3ca9a0d7f.
- Final Web digest sha256:a61287546a76410137805a77fc85ecf38cbe25b326bb1125b22a3cff4888dc82.
- Both final cosign identities https://github.com/7link-team/7link-platform/.github/workflows/release.yml@refs/heads/main (workflow_dispatch), issuer https://token.actions.githubusercontent.com. Final signature receipts .temp/delivery-api-signature-final.json and delivery-web-signature-final.json; empty Docker config used. Image labels for each architecture prove actual app source5968e701,versionv0.1.0-rc.1,source repo; users API10001:10001/Web101:101. Anonymous manifests each linux/amd64+linux/arm64+2attestation manifests PASS.

## Completed Work / Verification Evidence
### Coverage / CI
- Original profile1,176,834,355bytes/15,795,297rows/129,126unique blocks,99.18%duplicate. Exact unfiltered compact9,692,024bytes SHA25692dfe8c6ae7b8b6e0c6bad2e09997ac0d6b5dfacfad71d313015d7b076ed3053; filtered7,090,452bytes. Independent all-block/statement/count+55module outcomes comparison PASS; total90.6%.
- Original module scans (~49min) replaced simultaneous floors+compact filtering. Full hosted statistics2.781s, tools fixture1.419s. Full real1.18GB pipe replay4.051s/~52.2MiBRSS/identical9.69MB output. Replay time is NOT full application suite runtime.
- Linux/Windows real old/new cross-package/orphan/subprocess fixtures preserve all counters; failure preserves previous output;6billion int64 and output/start/producer/malformed/nonzero cases tested. Reverse mutation removing drain reliably times out8s. Windows tool tests10.229s/vet/build PASS, Linux1.607s/vet PASS; all55floors replay PASS. Independent reviews no remaining findings.
- Full tagged CI40246546640 all5jobs SUCCESS: checks2.7min/frontend3.2min/Runner1.7min/coverage23.9min/integration26.2min. Fresh coverage122package summaries,55floorsPASS,total90.6%,stats2.781s. Full nested integration17m12s and realMySQL migration6m58s PASS; lifecycle/archive MySQL15.36s/0.21s,PG6.45s/0.24s PASS. Logs .temp/delivery-stream-hosted-coverage.log and delivery-stream-hosted-integration.log.
- Main b67bf17f full CI40256421617 SUCCESS. Current72242925 CI40330075270 running; do not claim latest completed until checked.
- Previous14.3min vs23.9min coverage difference not causally explained: summed Go package durations changed, source/cache/flags same, no CPU telemetry. Do not call replay4s whole-suite performance or assert host load/collector defect.
- fmt/schema locks/actionlint/diff/LF PASS;279published migrations unchanged. Actual real MySQL8.4.11/PostgreSQL16.15 lifecycle+archive original4gates PASS; owned remote resources cleaned. Evidence .temp/delivery-external-real-databases-final.log.

### Windows harness correction / real evidence
- Prior release desktop EXE hash mismatch was framework UNK->NSS binary patch; comparator models ONLY that fixed-width change and compares full SHA256 of actual installed executable.
- Diagnostic40328344873 retained real hosted4,213,760byte portable with UNK1 at0x3bf56f and NSS1 at0x39b0fc (legitimate bundle-type constant table). Old global NSS-absence guard wrongly rejected it. Log .temp/delivery-desktop-diagnostic.log: UNK1/NSS1/PS7.6.8.
- Extracted actual NSIS payload exactly equals raw after its sole UNK->NSS replacement, every other byte identical. SHA25686faa41baf2bf3b08d3aceeb8d40bd2b2649828423a21461bd344918cdf69778. .temp/delivery-hosted-desktop-40328344873/ includes installer,ZIP,raw EXE and payload/.
- Minimal fix still requires exactly1UNK but permits independent NSS constants. All full-hash,scope/hive/location/version/uninstall/cleanup checks unchanged.
- .temp/delivery-marker-regression.ps1 extracts actual workflow block: PS5.1+PS7.4.13 PASS, both real local/hosted payload hashes exact; arbitrary payload mutation rejected; missing/duplicate/already-patched markers rejected; restoring old guard rejects valid hosted binary. Independent reviewer reproduced, no findings. Hosted final both-scope acceptance PASS (not merely local probes).
- Official isolated PS7.4.13 tooling SHA2568fb52d2172d285b230c2857a90ba4dd28ecf6477ba4a91f91b6854a647b33b65 verified against GitHub asset digest before execution; local .temp/delivery-tools/pwsh-7.4.13/. Default local PowerShell remains5.1.
- Workflow desktop_only input defaultsfalse, skips images/Runner and hence Release in diagnostictrue mode. Artifact upload precedes acceptance to retain evidence; publication still depends on desktop job success. actionlint/review PASS.

### Other accepted delivery evidence
- Six actual Runner artifacts from earlier same tagged-source run: one native binary/archive each; PE/ELF/Mach-O amd64+arm64 headers and Go build GOOS/GOARCH+candidate version bytes PASS. Windows amd64 actual --version printedv0.1.0-rc.1. Other five NOT executed. .temp/delivery-runner-acceptance.py and ignored binaries.
- Desktop local native EXE:2hostnames/3same-origin API calls/4native returns incl refusal, remote native denied. Browser multi-origin save/persist/select/remove/storage/mobile/cookie rules PASS. No fixed API/server required.
- Backend Huma real binding regressions and1343operation OpenAPI/client PASS; nginx101m request boundary PASS. Prior frontend UI169/Web825tests/98,702assertions, Runner/tests/vet/Node3tests PASS. Real copied-runtime326s health200/5xx0/locks0/ERROR0 and creation/detail/foreignproject404 PASS.
- Realtime real MySQL P95=252ms versusPG3ms compares remote MySQL polling with local PG LISTEN/NOTIFY, not SQL performance; original real gate PASS, thresholds unchanged.
- Public history credential scan13,037blobs/424,618,159bytes:6benign fixture/vendor patterns, no exposed credential found.
- Latest cached API release build job completed~1min with CACHED compile layers (.temp/delivery-release-api-cached.log). First tag cold API compile amd64306.2s/arm642697.9s; cold main retry arm644566.9s. QEMU cold cost remains; native ARM64 builds not implemented. No claim that warm cache fixes cold compilation.

## Remaining Product Scope
Authoritative requirements docs/PRD_FINAL.md + specifications; .temp/lanes/final-gaps.md198rows DONE157/PARTIAL27/MISSING10/SUPERSEDED4. DONE is bounded evidence, not full live acceptance. Preserve37remaining IDs:
- MISSING: P-C10 inbound mail/thread routing; P-C22 interactive execution/ADHOC association; P-C44 remote file edit/upload; P09-09 browser-closed Web Push; P12-16 Agent Yunxiao MCP; P-R04 Centrifugo presence/recovery; P-R05 global search/index lifecycle; P-R08 health incidents/remediation; P-R10 mobile clients/background push; P-R11 actual cc-switch reuse/config release/apply/rollback.
- PARTIAL infrastructure/UI: P-C47 preissued SERVER enrollment/K8S pools; P-C48 consistent authorized query/page/export/import/refresh/context; P-C49 virtualization and measured100k-row/100-user performance.
- PARTIAL collaboration/agents: P05-08 manual dispatch; P05-09 automatic dispatch/fairness/recovery; P05-10 heavy branches/comparison/synthesis; P05-19 external channel projection; P05-20 hooks/rich media; P05-21 spawn/adoption/orphan lifecycle; P05-23 persistent Agent inbox/interruption; P05-30 default-deny grants; P06-15 accepted-work skill distillation.
- PARTIAL shell/notifications: P08-09 catalog/preferences/DND/channel completeness; P08-10 canonical scoped shell/contextual conversation; P09-08 complete producer-to-recipient in-app/mail delivery/live acceptance.
- PARTIAL integrations: P12-05 automation dry-run/logs; P12-07 acceptance cases/plans/runs/frozen suites; P12-14 remote state-map/import/sync; P12-15 signed Codeup ingress/trusted Flow redlines/manual gates.
- PARTIAL cross-cutting: P-R06 repository-event compensation; P-R07 node telemetry/probes; P-R09 native desktop/release proof; P-R12 cross-domain change coordination; P-R13 conversation provenance/exact-position actions; P-R15 organization/invitation/handover; P-R16 multi-Human/Agent teams/history; P-R17 archive/recovery/retention/export/backup.


## Known Issues / Risks
- Full prototype37remaining gaps above remain; this is a candidate publication, not full product completion.
- Windows unsigned and WebView2 required; other Runner platforms inspected, not all executed.
- Latest main CI40330075270, tagged application full CI and automated Release all SUCCESS.
- Only local MEMORY.md has final acceptance updates, deliberately not pushed as a state-only CI trigger. Production/workflow fixes through72242925 are committed/pushed; no staged artifacts.

## Useful Existing Tools
- python .temp/delivery-github.py '/repos/7link-team/7link-platform/actions/runs?per_page=3' (stored credential helper, never prints secret).
- python .temp/delivery-ci-log.py JOB_ID .temp/LOG; completed logs only (live404 is not progress proof).
- python .temp/delivery-public-verify.py images|release (ANONYMOUS; binary assets+checksum-list contents validated).
- python .temp/delivery-image-metadata.py (anonymous per-platform source/version/nonroot labels).
- .temp/delivery-tools/actionlint.exe; cosign.exe2.5.0; empty .temp/delivery-anonymous-docker/ for anonymous signature checks.
- .temp/delivery-desktop-artifact.py RUN downloads retained desktop workflow artifact; diagnosticmode artifacts are NOT releases.
- Agent-browser skill previously applied; CLI C:/Users/Feather/.bun/install/cache/agent-browser@0.39.2@@@1/bin/agent-browser-win32-x64.exe; own candidate-public-5968e701 session CLOSED. Browser session was unauthenticated; no need now both GHCR packages public.

## Verification
Build: PASS (full automated Release40330098967).
Tests: tagged-source full CI40246546640 PASS; prior main full CI40256421617 PASS; latest exact-main40330075270/72242925 all5jobs PASS.
Other: hosted Windows both scopes/uninstall PASS; both anonymous images/platforms/source labels/signatures PASS; anonymous all8binary assets+2checksum-list scopes/checksums PASS; actual final6Runner archive formats/architectures/build stamps PASS, Windows amd64 --version PASS. Receipt .temp/delivery-published-runner-acceptance/receipt.json.
Self-review: scope/reuse/architecture/regression/complexity/durable state PASS; release acceptance PASS. Latest exact-main CI all5jobs SUCCESS; final candidate acceptance PASS.

## Next Action
Stage/review/commit ONLY release.yml, docs/deployment.md, apps/frontend-app/README.md, MEMORY.md, ADR.md; pushauthorized main. Dispatch release.yml refmain tagv0.1.0-rc.1 twice: images_only=true (unique verify-RUN-ATTEMPT tag) and append_desktop=true (4Unixbuilds+appendonly). Differentverification/publication concurrencygroups permitindependentwork; append and normalreleases stay serialized pertag. Inspect actual nativebuild/startup logs; fix only provenin-scope failures. Do not dispatchnormalfullrelease againstexistingcandidate or overwrite existingassets/images/tag.

## Last Checkpoint
Automated candidate Release40330098967 SUCCESS, immutable tag/source verified, actual hosted both-scope install+uninstall PASS, final anonymous images+signatures/source labels PASS. Anonymous final attachments verification active.

- Final publication acceptance2026-10-26:10expected assets exactly present; both product checksum lists name only their own binary assets; every8binary payload anonymous download+size+SHA256PASS. Actual final Runner archives each contain only machine-runner(.exe), PE/ELF/Mach-O amd64+arm64 headers and Go build GOOS/GOARCH+candidate stamp PASS; Windows amd64 native --version=v0.1.0-rc.1 exit0. Other5NOTexecuted. .temp/delivery-published-runner-acceptance/receipt.json. Final automated release runSUCCESS and immutable annotatedtag peeled correctly to5968e701; initial standalone tag probe incorrectly compared tag object to commit, corrected to peel annotation (no tag moved).

- Latest main coverage job121597110826 completedPASS: independently counted55module floor rows, every threshold met, total90.6%. .temp/delivery-final-coverage.log. Main CI40330075270 now waits ONLY integration121597111104.

## Final Acceptance (2026-10-26 13:50 UTC)
- Build/Tests/Other: PASS. Exact-main CI40330075270 ALL5jobs SUCCESS; actual full coverage55floors,total90.6%; full nested integration, realMySQL migration and both real-engine lifecycle/archive gates PASS. .temp/delivery-final-coverage.log and delivery-final-integration.log.
- Automated Release40330098967 ALLrequired jobs SUCCESS, public prerelease10assets, immutable tagged app5968e701. Anonymous final images/platforms/source/version/nonroot/signatures and every asset/checksum PASS. Actual hosted /CurrentUser+/AllUsers install+uninstall+fullhash/files+registry cleanup PASS.
- Self-Review: requirement/scope/reuse/architecture/complexity/regression/delegation/durable-state/testing/commit-hygiene PASS. Changed tracked code only existing release workflow; reused Tauri/NSIS, original hashes and original gates; no source/tag/API/SQL/dependency changes. Independent bounded reviewer reproduced true root cause and meaningful reverse mutations, no remaining findings. Final acceptance owned by main.
- Final Result: PASS for candidate delivery/coverage optimization, NOT full prototype. Build timings from successful Release: API62s(cached),Web76s,desktop364s,finalrelease16s. Cold QEMU45..76min compilation remains an optional future optimization; no claim of native ARM64 work.
- Working tree: ONLY final local MEMORY.md changed, no staged artifacts or unpushed source/workflow changes. All owned verifier/download sessions ended. Prior rejected cleanup retained, do not retry.

## Current Change Verification Checkpoint
- Files: existing release.yml+deployment.md+desktopREADME+MEMORY+ADR only. Production/Dockerfiles/Cargo/Bun/Go/dependencies/migrations unchanged. All5tracked+working LF confirmed.
- Native build4matrix api/web xamd64/arm64, matching ubuntu24/24-arm unameassert; digest-only push, percomponent/archcache, native api--help/nginx-t smoke before digestartifact. Mergeexact2digests requires2runtimearch+2attestationrefs, preservesSBOM/maxprovenance; signindexGitHubOIDC. No QEMU.
- Unixdesktop4matrix: macos-15-intel+x86_64-apple-darwin,macos-14+aarch64-apple-darwin DMGs withad-hocidentity-; Ubuntu22.04+22.04-arm nativeAppImages. Lockeddeps/tagconfig; mountedMacversion/arch/codesign/startup5s and LinuxELF/extraction/Xvfb+D-Bus startup10s. These arestartupchecks, NOT renderedUI/login acceptance.
- append mode verifies annotatedtag->expectedcommit, releasefields, exactincoming4names, alloldchecksums/payloads before anywrites. Adds onlymissingUnixbinaries plus separatedesktop_<tag>_unix_checksums.txt; never --clobber. Existingoriginal10assets MUST retain IDs/bytes/digests. Normalfuturefullrelease has combineddesktoplist6; existingcandidateappend hasWindowslist2+Unixlist4 (15totalassets).
- Review found initial draft clobber replacement coulddeleteoldmanifestbeforefailedupload. Main replaced entire approach with append-onlyseparatelist. Independent reviewer confirmedfix and reran meaningfultests nofindings.
- .temp/delivery-native-publication-tests.py PASS: validindex+5malformedindexes+lostattestationref reverse mutation; append/replaypreserveoldbytes evenfreshbuilddiffers;7badidentity/prerelease/checksum/path/build/conflict casesrejectbeforewrites; midbinaryandnewchecksumupload failures preserveALLoriginalassets, sameartifactfailed-jobretrycompletes; reversingtooldclobber reproduceschecksumloss detectedbyinvariant.
- .temp/delivery-native-mode-tests.py PASS all8booleanmode combinations, mutuallyexclusive invalidmodesrejectwithoutoutputs, verificationtagisunique, taggedappsource5968e701 unchanged. FouractualembeddedPythonblockscompile. Windows originalbuild+acceptancebody comparedbyteequivalent against72242925.
- actionlint1.7.12 PASS; oldlocal1.7.7 lacked officialmacos-15-intel label, no fakecustomrunnerconfig introduced. Official1.7.12 assetSHA2566e7241b51e6817ea6a047693d8e6fed13b31819c9a0dd6c5a726e1592d22f6e9verified beforeexecution; ignored .temp/delivery-tools/actionlint-current/.
- A-classreview: A1 immutable digestartifactstagingvalidatedbeforepublish; A2 largebundlehashesstreamed+actualhosted volumespending; A3 mode/dependencyguards tested; A4 noappauthchange, leastprivilege workflowpermissions; A5 nofinanciallogic; A6 appendinterruption/replay invariants+no oldclobber tested; A7 oldassets/appsource protected; A8 asyncmatrixfailures preventpublish and sharedperTagpublication lock. SQL/API/schema/budget/businessstatemachines unaffected.
- Desktopad-hocmacOS is NOT DeveloperID/notarization; Linuxbaseline22.04/runtimeGUI prerequisites documented. No mobile scaffolding/WindowsARM64 expansion.
