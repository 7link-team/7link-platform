# Task State

## Task / Objective
Selectable bundled local desktop service plus existing remote-server mode. User approved implementation and requires formal application name from CURRENT LOCAL branch `chaosplus` (not GitHub main). Product identity remains configuration. Automatic updater is outside scope; no updater currently exists.

## Current Phase
REVIEW

## Current Step
Implementation, real Windows native UI and release package acceptance PASS. NSIS chaosplus_0.1.0_x64-setup.exe built34.94MiB; extracted service/Web verification PASS. Actual EXE+local resource ZIP55,892,863bytes created/extracted and verified PASS (.temp/desktop-local-package). No actual system installation performed locally. Self-review scope/reuse/architecture/regression/complexity/durable state PASS; final acceptance remains NOT READY for cross-platform publication until hosted package gates run. Explicit source/docs/state staged; no artifacts or credentials staged. Next commit/push authorized main for full hosted CI. New version rc.2 question remains unanswered; do not tag/publish or overwrite rc.1.

## Overall Progress
- [x] Approved design persisted in ADR; existing composition/bootstrap/migrations/auth/static Web/Tauri mechanisms reused.
- [x] Go desktop subcommand serves production Web and /api on one loopback origin; per-user SQLite/keys; parent protocol controls first administrator setup and shutdown.
- [x] Rust owns hidden child, bounded readiness/shutdown and trusted chooser commands; launcher offers Local/Remote, explicit first administrator, error/retry states. Config productName=chaosplus; existing bundle identifier retained for compatibility.
- [x] Prepare script builds native Go+production Web. Release workflow builds shared Web once, packages local resources including Windows ZIP. Added actual service acceptance for extracted ZIP, both installed NSIS scopes, mounted DMG and extracted AppImage.
- [x] Read-only reviews completed; backend EOF deadlock and native setup-timeout stale state fixed with regression tests. Packaging review requested actual packaged service verification, now added.
- [x] Docs updated for local mode, resources, per-user data, application name and no automatic updater.
- [x] Local Windows release NSIS/portable contents and extracted native service verified.
- [ ] Current-source hosted CI/all-platform packaging; final acceptance.

## Verification
- Go entire command package: PASS 48.065s, repeated after latest fixes. Desktop tests cover setup rejection/success/restart, stable port/collision, Web/API same origin, Host/Origin rejection, missing assets, persistent keys, EOF after repeated setup.
- Bun launcher suite: PASS 8 tests /39 assertions (local first setup, password clearing/mismatch, busy/retry plus existing multi-server tests).
- cargo check/build/test: PASS; latest Rust timeout regression 1 PASS. Latest debug build Finished. PowerShell can report NativeCommandError on successful Cargo stderr progress; inspect actual log/exit.
- Fresh packaged Go service: PASS production HTML/API, administrator setup, persistent DB/admin/origin, extra command then parent EOF, listener shutdown. Windows PATH restricted to System32 proves no compiler DLL dependency. Script apps/frontend-app/scripts/verify-local.py.
- Real Windows native WebView: PASS explicit first admin, actual captcha/password login, deployment name chaosplus, native menu return, remote saved origin, denial of start_local from served local/remote Web, restart with persisted admin/session/origin, owned service stops on normal native close. Isolated debug data/profile under .temp/local-data-* and local-webview-*, never developer app-data. Harness .temp/accept-local-ui.py + finish-local-ui.py + close-local-ui.py; restart initial one-second assertion was premature, subsequent runtime polling/snapshot confirmed success. All owned native/service processes stopped.
- OpenAPI: PASS fresh `make openapi-check OPENAPI_WORK_DIR=.../desktop-openapi-fresh-20261026`; generated contract exactly matches docs/openapi.json; published OpenAPI tests PASS. First run reused old incomplete .temp/openapi DB and failed migration; fresh isolated DB resolved, no migration changes.
- Full architecture package: PASS `go -C tests test ./architecture -count=1 -timeout 240s`, 115.169s.
- actionlint current binary1.7.12: PASS; old bundled actionlint lacks already validated macos-15-intel label, use .temp/delivery-tools/actionlint-current/actionlint.exe. git diff --check PASS; LF verified.
- NOT RUN: current-source hosted full CI, actual new NSIS scope installs, macOS/Linux package/service acceptance. Do not claim all-platform delivery complete.

## Important Decisions / Confirmed Constraints
- Source local branch chaosplus, preserve Codeup origin; authorized GitHub push `git push github HEAD:main`. No force/reset/stash/blind staging. Baseline commit cbd48068 before this uncommitted task.
- User already authorized public repository/GHCR/pushes and rc.1 publication. NEW release/version needs pending answer; recommended rc.2 due immutable rc.1.
- Reuse existing Go app lifecycle/SQLite/migrations/maintenance bootstrap, no new business module/API/schema or default/demo account. Credentials through parent stdio only, not HTTP/CLI args/logs. Per-user appdata; local service only lives with desktop, loopback only, persistent first port, collision explicitly fails. Multiple remote domains remain supported.
- ProductName chaosplus stored only in Tauri config; package metadata feeds window/service name. Keep com.example.frontendapp identifier for stored-server compatibility. Portable includes entire local/ resource tree; WebView2 still required. NSIS installMode=both uses framework scope/elevation semantics.
- One heavy local build/test at a time; Go GOFLAGS=-p=1; hidden Windows children. Do not disturb ports4303/8080/8091/8092/8087 or unrelated go5348.
- Prior automatic approval rejected recursive deletion of some temporary/WebView profiles; DO NOT retry cleanup via another mechanism. Existing retained temp files are not deliverables. No remote disk prune (previous remote root full).
- Review delegation already complete (review_local_backend/native/packaging); no new delegation needed, acceptance owned by main.

## Prior Delivery (Completed, Preserve)
- Public https://github.com/7link-team/7link-platform/releases/tag/v0.1.0-rc.1 has all15assets; anonymous checksums/downloads PASS; original10IDs/bytes retained. Unix append run40361485510 PASS.
- Immutable rc1 source5968e701081de5f64e0a78f4daf33a445b5702fd, annotated tag object372a6b69cb63cfe91bbe403ba69b216c47c4387e. Never move tag or replace existing images/binaries/checksums.
- API digest sha256:36c063c603e3d410f7e0fde4e7c37ef237cc27b312472db36c09daa3ca9a0d7f; Web digest sha256:a61287546a76410137805a77fc85ecf38cbe25b326bb1125b22a3cff4888dc82. Both public, native amd64/arm64, SBOM/provenance/cosign, source/version/nonroot verified. Native diagnostic40360560870 PASS; ARM compile279.4s vs QEMU2698–4567s.
- Previous main CI40361457804: all5jobs PASS after retry transient embedded PostgreSQL download. Earlier CI/release accepted both Windows install scopes/uninstall and remote chooser.
- Coverage raw1.18GB/99.18%duplicate compacted9.69MB with exact int64 counters/full cross-package instrumentation,55floors/90%total preserved. Linux stream avoids retained raw aggregate. Actual total90.6%; replay4.051s is aggregation ONLY, not full suite.
- Real MySQL8.4.11/PostgreSQL16.15 gates PASS, SQL unchanged. MySQL realtime P95 252ms vs PG3ms is remote polling vs local LISTEN/NOTIFY, not comparable SQL benchmarks. Migration DB integration separated from lifecycle fixtures.

## Known Product Gaps (Prior, Out of Scope)
No updater/mobile; prototype gaps include inbound mail routing, interactive execution/ADHOC association, remote file edit/upload, closed-browser Web Push, Yunxiao MCP, Centrifugo recovery, global search lifecycle, health remediation and actual cc-switch reuse. Do not call rc candidate production-complete.

## Next Action
Commit explicitly staged20source/docs/state files and push github HEAD:main; inspect current-source hosted CI and resolve any failures. Hosted all-platform package/new release requires pending version answer (recommend v0.1.0-rc.2). Never overwrite rc.1. Local Windows artifacts remain under .temp and ignored target directories, not committed.

## Last Checkpoint
2026-10-26: Windows release NSIS/portable extraction/runtime acceptance, OpenAPI/architecture/native UI tests and source self-review PASS; current-source hosted CI and new release pending.
