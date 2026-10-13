## Prototype deep audit — 2026-10-13 (ACTIVE)
- Goal: evidence-based PRD completeness, business closure, navigation/controls, design guidelines and E2E; two consecutive complete rounds without new findings. Previous delivery is historical evidence only.
- Phase: ANALYSIS / first audit; clean rounds 0. Scope docs/prototype and its requirement/evidence docs; no production backend implementation, no commit/push.
- Latest explicit requirement: task preview supports multiple tabs, including repeated kinds, retained state and task isolation. Existing implementation under verification.
- Execution: main owns browser reproduction/fixes/acceptance; deep-review mandated read-only product and UI review agents.
- Remaining: per-requirement evidence beyond route mapping; reproduce and repair defects; mutation verification; full scoped browser/build/tests; two clean review rounds.
- Next: verify preview state across switching tasks, reload and disconnect; independently assess product/UI findings. No blanket PASS yet.

## Final PRD and complete prototype — 2026-10-13
- Goal: full PRD/REQ integration with two reviews, plus independent high-quality Web/App interactive prototype under docs/prototype. Reuse original six themes/top nav, preserve production apps and unrelated changes. No commit/push or delegation.
- PRD COMPLETE: docs/PRD_FINAL.md v4.1 contains all original 291 requirements + 41 additions = 332; 70 scenarios (18 JNY,27 AC,25 EXT). T2 decisions, T3/T4 reviews, S8 unresolved production policies, S9 latest user confirmations. Original source snapshots/hashes unchanged; 578+719 reconciled source lines retained; anchors/local refs check PASS.
- Prototype DELIVERED (interactive mock, ready for user acceptance): React/Vite, shared mock commands, work/chat/artifact/governance modules, review studio. Latest user confirmations: tenant contains project instances with tenant-only rollup; Logo-right tenant create/join/switch; one project selector, no duplicate list; all-projects menu only tenant workbench, instance workbench is instance overview; sidebars/channels change local panels with unchanged URL/history/top area.
- UI: original six theme token sets + three original dark sets copied, other three dark sets explicitly extended; shared custom Select, Field/Form, Modal/confirmation, themed scrollbars/inputs. Calendar month/week/person/agent/milestone/unscheduled with validated edits. Workflows use pages-flow.jsx (node library, parameter/IO/debug inspector, duplicate/delete/connect/check/publish). Task previews use task-previews.jsx (per-task multiple Web/API/files/terminal/doc tabs, preserve state, disconnect/reconnect/close).
- Verified: 16 domain tests /59 assertions PASS; browser delivery workflow PASS; new-feature suite PASS; prior navigation/theme suite PASS. 63 routes/states x375/768/1024/1440: no runtime errors/broken pages/document overflow. Axe final checks of dashboard/chat/flow/settings/theme-modal/calendar/preview/Aurora-dark: 0 violations (some manual-review items remain). Source 9 theme blocks semantically match original. Screenshots reviewed and kept under docs/prototype/screenshots.
- Deliverables: README.md, DESIGN.md, COVERAGE.md (332 requirement entry mappings, NOT claims of production implementation), REVIEW.md (two prototype reviews), executable browser scripts under tests/browser-*.js.
- Final verification complete: final build +16 tests PASS; built review studio navigation/375-width smoke PASS; browser suites PASS; PRD coverage +git diff --check PASS. Demo state reset. Dev server remains http://127.0.0.1:4173 (review.html for device presets). No remaining work in this prototype-delivery scope; next user acceptance, then separately scoped production implementation.
- Boundaries: mock design only; no real APIs/model processes/FRP/PTY/native packaging/security/concurrency/performance proven. Historical media viewers use fixtures, not independent binary snapshots. Future implementation requires more than wiring HTTP APIs. No production implementation started for prototype.

## Workspace initialization — 2026-10-13
- User: rename Web to frontend-web; initialize frontend-app with Tauri and a runner project only. No feature implementation.
- Phase: DELIVERED (initialization only). Direct execution, no delegated subtasks.
- Web moved to apps/frontend-web; operational and documentation path references updated. Historical ADR entries retained.
- User refinement: frontend-app is packaging only; reuse Web UI. Removed duplicate React/Vite template and Rust greet command. Shared Web dev/build hooks configured. Retained monorepo and renamed apps/iam to apps/web; sys/ops/ai remain in-app module directions, not separate deployables.
- Verified: Web typecheck/lint/tests/build/API types PASS; runner build/start PASS (no test files); 375 moved Web files preserve content except requested path/name changes.
- Final verification: shared Web typecheck/lint/API contract/build PASS; 290 tests PASS (204 web + 86 shared UI); final Tauri beforeBuildCommand built shared Web and macOS debug binary PASS; final dev hook HTTP 200; cargo fmt and script syntax checks PASS; all 375 tracked Web files preserve contents except directory/package references; git diff --check PASS.
- Limits: no native UI/login E2E, no mobile platform initialization, no responsive redesign, no runner features; no full backend CI for path-only changes. Builds are ignored. No commit or push.
- Self-review / acceptance: initialization scope satisfied; shared UI reused; no separate frontend UI. Next: nothing outstanding in initialization scope.

## Myrmidon product PRD + HTML prototype - 2026-10-11 (docs only, no code)
- Goal: design a brand-new product integrating old MyrmidonAI (D:\Projects\MyrmidonAI: PRD.md v2, chaosplus.md, docs/*, Preview 01 code)
  with docs/0[1-4]_Myrmidon_*.docx (2026-10-10 review drafts), built on THIS repo as base framework. Deep PRD + beautiful HTML prototype via .agents/skills.
- Constraints (user): old project code/docs read-only; outputs live in this repo (docs/myrmidon/). Do not touch backend/frontend code.
- Done: read old PRD/chaosplus/README/preview; subagent summary of old docs+code; read 4 docx; ui-ux-pro-max design system
  (AI-Native UI, violet #7C3AED + cyan #0891B2, Space Grotesk/DM Sans/JetBrains Mono); misplaced file in MyrmidonAI deleted.
- User direction: 乙方 team = agents+humans or all-agent; 甲方 = just a requirement intake. Added: lead/orchestrator agent assigning to
  multi-model agents (Grok-Heavy-like ensemble branches), built-in memory + DB-retrieval knowledge base (K1 full-text 3 dialects, K2 pgvector).
- DONE: docs/myrmidon/PRD.md (positioning, 4-layer completion ladder, domain model, state machines, failure/rework, permission matrix +
  delegation, intake/portal, fleet/execution, §10.6 lead agent, §10.7 knowledge, framework mapping §13, design system, metrics, 8-week plan, Q1-Q11).
- User: PRD (~700 lines) not enough; must be 完整 可落地 可执行. Plan approved: PRD.md stays master; specs/00-conventions.md (DONE,
  binding contract: modules/tables/enums/permission codes/events/interfaces/error ranges/AC format) + 9 module specs written in parallel by
  subagents: 01 intake-portal, 02 work-review, 03 execution-evidence, 04 machines-runtime, 05 agents-orchestration(+collab channels),
  06 knowledge-memory, 07 projects-delivery, 08 governance, 09 platform-capabilities. Added collab+governance modules to PRD §13.2.
- Returned DONE: 01(895) 02(1317) 03(1291) 04(1102) 06(1000) 07(1199). 05/08/09 hit gateway 503/timeouts, resumed with chunked writes
  (05 has §1-5). 00 bumped to v1.1 merging all CRs from 01-04,06,07 (+§13 framework change list F1-F10, §14 changelog); PRD runner name fixed.
- 2026-10-11 later: ALL 9 specs DONE (05:1197, 08:1404, 09:1124; total ~10.9k lines). 00 now v1.3 (merged CRs from all 9).
  Active: 3 read-only auditors (A: specs vs 00 mechanical; B: cross-module interfaces + known conflicts 1-9; C: end-to-end scenarios S1-S7).
  Audits DONE (~120 findings: A mechanical 56, B interfaces 45+9 conflicts, C scenarios 9 blockers/31 major). Resolutions R1-R20 decided
  (in conversation): platform idempotency table + 176425/176409; 09 receipts only; tag incl mount prefix; ETag/If-Match; 403+reason_code
  (08 owns, SEPARATION_OF_DUTIES); intake_records, /suspension, /draining; sync DispatchAssigner (+dispatch.rejected kept); 02 owns
  ClaimSubmitter/DecisionReader/RequirementCreator; RETRY_INFRA re-dispatch; crash/revoke -> INTERRUPTED; T20b, T26 ACCEPTED->REWORK, T12
  SYSTEM decision; REOPEN_WORK_ITEMS; ACTIVE-only autonomy; REVIEWER dispatch MVP + agent.review/agent.triage; new inbox kinds + shell §8.6;
  ProjectGate (no BudgetGuard); notifications storage=09; HOLD_DELEGATIONS on client rejection; ensemble fixes; step-up list = 08 §6.9.
  DONE by me: 00 -> v1.4; PRD fixes (C56 retry semantics, routes, refresh targets, §0.2 spec index).
  7 fork fix units DONE. Main verification: residual greps clean; aligned names (SELF_REVIEW + SEPARATION_OF_DUTIES both in 08 S3 & 00;
  dispatch.rejected restored in 08; 07 OpenClarifications; 01 WorkItemReopener.ReopenFromAcceptance; 07 BR-07-08③ uses 02 scope-change CANCEL).
  Totals: PRD+specs 12,242 lines, all LF/UTF-8. Round-2 re-audit found 6 BLOCKER/21 MAJOR -> fixed by 4 fork units + me (00 v1.5,
  07 ReleaseNotesDrafting). Residual greps clean. Round-3 blocker-only verification running.
  Prototype DONE: docs/myrmidon/prototype.html (684 lines, 19 routes, Aurora + 5 framework themes, light/dark, mobile tabbar, cmdk,
  machine device-code wizard, reject/structured feedback, takeover, delegation, retire). Verified in headless Edge: all routes 0 runtime
  errors; all modal functions OK; screenshots desktop/mobile(500px; headless min viewport 496, scrollWidth 481 = no h-scroll)/light/neo.
  Round-3 (blocker-only, S1-S4) found 4 BLOCKERs -> fixed by me: B1 SubmitInput.from_exit (02, 03 E06 passes true); B2 drafter_agent_id
  + ReportUnavailable reasons (05/07); B3 WorkItemFacts.AssignmentRole/AssigneePrincipalID, SYSTEM skips S3/S4/S6, POLICY:review_dispatch
  (02/05/08); B4 P08 expiry by trigger type (05); notes n1/n3/n4/n5 fixed; 00 v1.6. S2 had no blocker.
- Phase: DELIVERED (docs only). Final: PRD.md + specs 00-09 + prototype.html = 13,119 lines, all LF/UTF-8.
- Not done: no 4th re-audit after B1-B4 (fixes grep-verified only). docs/ ignored by user's whitelist .gitignore (not committed, not changed).
- User (2026-10-11): "docs 都进git" -> .gitignore whitelists /docs, docs committed locally (not pushed); "该有的就有" -> all PRD §18
  Q1-Q11 + spec §12 open questions adopt recommendations (decisions D109, PRD §18 note, 00 v1.7); "先不开工，等我过完原型再说".
- D110 (user: supervisor agents create agents; humans set permissions/quota/budget; UI marks human- vs AI-created): DONE in PRD §10.8,
  00 v1.8, 03 (cost event agent_id), 05 (origin/lineage, spawn requests, orphan/adopt), 08 (spawn grants, policy rule, inbox), prototype
  (members source filter + supervisor tree, lineage drawer, spawn approval, grant dialog, policy section, channel card); runtime-tested; commit f0a1fa8.
- Pushed chaosplus to origin; remote warns committer email != Codeup account (warning only). Spawn approval default
  stays REQUIRE_APPROVAL (user confirmed).
- ACTIVE (user: "二级页面补充，能用 codex cli 做的让他去做"): main prototype.html now loads prototype/pages-{project,fleet,exec,
  governance}.js after core; NAVPARENT + crumb parent; portal-* sub-routes; entry links wired (execution/, machine/, agent/, intake/,
  intake-channels, requirement/, delivery/, ksource/, retrieval-debug, audit-event/, delegation/, spawn-grant/, heavy/, settings,
  portal-submit|clarify|accept, newProjectWizard/newWorkItem/newIntake/newDelivery). 4 codex exec jobs running (briefs in %TEMP%/myr-codex).
  DONE: 4 codex jobs wrote only their own files; verified: node --check all, 36 routes 0 runtime errors, 9 entry dialogs OK,
  screenshots desktop+mobile; fixes by me: 主导→主管 naming, Heavy branch reviewer→executor (echo), BOM stripped. Committed + pushed.
- ACTIVE (user: "用 codex cli 在 .mvp 下写最简验证程序，再让它写一个可验收的小项目，如 2048"): .mvp/ (gitignored by whitelist,
  untracked). I wrote client inputs .mvp/tasks/2048/{requirement.md,task.json,acceptance/game.acceptance.test.mjs}; acceptance suite
  validated 11/11 against a throwaway reference impl (deleted). Codex is writing the orchestrator (.mvp/myr.mjs, lib/, test/, README)
  from brief %TEMP%/myr-mvp-brief.md; tasks/ checksums in %TEMP%/myr-tasks.sha256.
  Next: verify orchestrator (self-tests, tasks/ untouched, quick code read), then run `node myr.mjs run tasks/2048 --auto-approve-if-verified`
  (codex executors), then independent `verify`, play-check index.html screenshot, report to user; user does L4 `accept`.
- D111 (user: goals top-down, member plans, daily/weekly/monthly/quarterly/yearly reports bottom-up, reconcile before submit, block or
  flag 未核对/未确认): PRD §10.9 + nav group + 00 v1.9 DONE by me; spec 10-goals-reports.md being written by fork; then codex prototype page.
- RESEARCH (user: reference mature methodologies/platforms + new OSS agent-collab projects; closed loop, usable, RELIABLE, truly meets
  requirements; cover software / video gen / novel gen / human-only teams with AI assist): 3 read-only web research agents running
  (methodologies+platforms; OSS agent projects + spec-driven/eval practice; domain pipelines video/novel/human). Next: synthesize into
  docs/myrmidon/research/ report + design upgrade proposal (domain template packs + reliability mechanisms) -> show user BEFORE changing specs.
  + 4th research agent on Alibaba 云效 (Projex/Testhub/Flow/Codeup/Insight; user: "原来用阿里云效，多借鉴") -> adopt/adapt/integrate.
- 2026-10-11 user: (1) A-G all adopted = D112; (2) D113 internal QA gates, no leak to humans/client (tester agent + product acceptance
  agent must pass before human); (3) .mvp into git -> committed 94fe7b2 (runs/ and *.log ignored; NOT pushed yet).
  DONE by me: 00 v2.0 §15 (shared defs), PRD §10.10 + roles + HUMAN_ONLY + spec index 11/12, decisions D112/D113.
  RUNNING: forks upgrading 02 / 03+05 / 07+08+10, new spec 11 (domain templates) and 12 (Yunxiao); codex upgrading .mvp with internal QA
  (tester + acceptor w/ headless screenshots) -> then rerun 2048 fresh (run-id real-2048-qa) to see if internal QA catches row-height bug.
  After: prototype pages for D112/D113 (codex), consistency audit, commit + push.
  2026-10-12: 02 DONE, 03+05 DONE, 11 DONE (717 lines); 07/08/10 and 12 hit 503 -> resumed (07/08 mostly applied, 10 pending; 12 partial 75KB).
  .mvp QA upgrade DONE (lib/qa.mjs; 39/39 incl. real Edge screenshot test); stray .mvp/.claude removed. Real run real-2048-qa RUNNING.
  Pending cross-spec items for audit: 04 browser_capture tool + model_family in catalog (no fork owns 04); eval-set interface name
  mismatch (03 EvalSetWriter.ProposeCase vs 02 TemplateEvalSink vs 11 interfaces); 11 CRs (tenant_id=0 templates, 02 template_stage_key,
  BlockReason TOOL_/PROVIDER_UNAVAILABLE, 04 tool detection, 7 events, 4 interfaces).
  2026-10-12 later: leftovers unit DONE (11 provides EvalSetWriter.ProposeCase; 04 tools/capabilities/model_family; 11 CRs in 00;
  02 BR-02-61 SetSystemBlock). 12 DONE (858 lines). RUNNING: subagent merging CR-12-01..09 into 00/02/08 + D114 (quality.escaped
  publishers 02/07/12 with escape_key; learning only via 02 escape_learning; 08 keeps record+confirm) + D115 + residual grep sweep.
  Prototype: pages-quality.js + pages-templates.js DONE & verified (node --check, LF/no BOM, Edge probe 0 errors on 14 routes,
  screenshots); fixed full-width buttons in both helpers. pages-yunxiao.js codex job still RUNNING.
  real-2048-qa RUNNING: round1 internal QA rejected W2 (AC7 FAIL: no index.html, BLOCKER) -> internal rework, not leaked to human.
  DONE later 2026-10-12: CR-12 merged + D114/D115 (+ escape_learning_proposed event, worklog.returned/withdrawn, 主导→主管 in 03/06);
  pages-yunxiao.js verified (6 routes 0 errors, screenshots). real-2048-qa ended NEEDS_HUMAN: W2 rounds 1-3 (round 2 caught row
  heights!); found 2 MVP QA bugs -> fixed in .mvp (acceptor judges only item acceptanceRefs + engine guard; screenshots only for
  item owning the page; <500px viewports via same-size iframe). .mvp tests 41/41 incl real Edge. Research §5.1 written.
  Commits 9ba538c (docs) + d70b9f2 (.mvp) + 749f71b (state), pushed. real-2048-qa2 DONE: AWAITING_CLIENT, 4/4 internal pass,
  W3 round1 caught by tester (file:// 0 tiles) then fixed; hidden acceptance 11/11 re-run independently; screenshots OK; chain 68.
  Research §5.1 updated.
  D116 tool calibration (user approved) DONE in 11/03/00/PRD + prototype SOFTWARE verifier tab (probe 0 errors, screenshot).
  User did L4 accept on real-2048-qa2 (HUMAN:fan) -> COMPLETED, chain 74 ok; recorded in research §5.1.
  D117 DONE (client change requests): specs 00/01/02/07/08 + PRD by subagent (US-01-20..22, T15-T19, BR-01-76..87,
  110449-110455, AC-01-73..88, BR-02-63, BR-07-62, 127055); prototype pages-change.js (4 routes, ids IN-2028/2033/2042) +
  main entries; fixed: client disagree / team withdraw / decline need no step-up (only conversion), buttons, hidden note.
  Open for user: projects without tolerance rows never require client approval (BR-07-62 ①) - recommend default rule.
  Next: user reviews the change pages.
  D118 (project-first) + D119 (remote preview / terminal / files, server & k8s runners) DONE and committed:
  prototype shell + pages-chat/lists/remote + entries; docs in PRD/00-06/08-10 (subagent); Q12 share links v1.1,
  Q13 WebSocket to MVP, SERVER/K8S switches default on (decided under user's 「you call」, recorded in D119).
  Full probe: 71 routes + 9 parameterised routes 0 errors; all docs LF / no BOM.
  Later: goals moved to front (team + project nav); project discoverability (我的项目 list, project chip + switcher in top tab);
  machine remote access = machine detail tab 远程访问; crawler audit (357 pages, 0 broken after fixes, DETAIL_PARENT crumbs,
  spawn-request entry); chat unread badge + notification bell; Raft research (docs/myrmidon/research/2026-10-raft-comparison.md).
  Raft logged-in browsing: Chromium 136+ blocks remote debugging on default profiles; user to install Claude in Chrome in
  Chrome profile "Fan" (gankcode@gmail.com) and run /chrome; fallback = own minimal extension scoped to app.raft.build.
  D120 DONE (6 items incl. team tool catalog MCP + skills): specs 00/02-10/12 + PRD 10.12 (subagent reviewed the
  earlier partial edits); prototype pages-agentops.js (+ fleet tabs, remote comments), pages-tools.js (team tool catalog);
  operation names / permission codes aligned with the 05 manifest; crawl 359 pages 0 broken / 0 errors.
  Page Reader tool (general, C:/Users/Feather/tools/page-reader, no token, paired with Chrome profile Fan): Raft read
  logged in, findings in research section 6. Receiver not running now (start: python page_reader.py serve).
  01 portal client comments DONE (spec + portal-accept prototype, no internal info leaks verified).
  D124 prototype DONE + pushed (2b09160): client = external member (VIEWER client view, 以甲方身份预览), portal / intakes /
  engagements / portfolio / alias routes / redirects removed, task tabs, machine access tab, per-project lists and channels,
  project overview + real settings. Verified: crawl 325 pages 0 broken 0 errors, 0 pseudo-menus, 0 redirects, client view
  no leaks (14 routes), screenshots. Gap list: docs/myrmidon/research/2026-10-requirements-audit.md (A old design incl.
  visual workflow A1/A2, B docx incl. MVP re-scope, C specs/PRD vs prototype incl. D121-D124 spec rewrite, D user asks).
  E1-E4 ACCEPTED 2026-10-12; D125-D129 recorded. Active: docs/PRD/specs consistency + workflow React Flow prototype + missing pages. Main owns integration and browser acceptance.
  Do not start implementation.
  Proposal for user (not applied): spec 03 visual verifier needs tool calibration with known-good/bad sample pages.
- RESEARCH DONE: docs/myrmidon/research/2026-10-collaboration-research.md (4 tracks synthesized, gap analysis, P0/P1/P2 upgrade,
  decisions A-G awaiting user; §5 MVP results). Do NOT change specs for the upgrade until user confirms A-G.
- MVP DONE: .mvp/ (codex-built orchestrator, 17/17 self-tests); real run real-2048: 4 WIs, each 1 attempt, 9.2 min, hidden acceptance
  11/11 (independently re-run), chain ok, state AWAITING_CLIENT. Human look found: uneven board row heights (missing grid-template-rows);
  logic duplicated in index.html fallback (identical now, unverified). L4 accept/reject left to user. .mvp untracked (gitignored).
- spec 10 DONE (1030 lines); fork merging CR-10 into 00/02/05/08; prototype.html got 「目标」 nav group + pages-goals.js hook;
  codex writing prototype/pages-goals.js (goals tree/detail, plans, report editor w/ reconciliation panel, report center).
- Next: WAIT for user's prototype review feedback (docs/myrmidon/prototype.html). Do not start implementation until told.
  Next: when back -> verification grep pass (dispatch.applied consumers, BudgetGuard, idempotency tables, PENDING_CONFIRM, 176409/176425,
  stream_seq in 03, /pause, /drain, intakes table) + spot re-audit; then prototype.html.
- NOTE: repo .gitignore (user-modified, whitelist with `*`) ignores docs/ -> specs not tracked; tell user, don't change.
- Next: when all return -> consistency review (enums/permission codes/events/routes/error codes vs 00; cross-spec interface calls match;
  contract change requests merged into 00), fix, add spec index to PRD; THEN prototype.html based on specs; browser check; deliver.

## Fix all review + audit findings - 2026-10-11 (user: "全部修好")
- Phase: IMPLEMENTATION (Hybrid: 5 subagents on disjoint files, main agent integrates).
- Decisions: D105 erasure soft-deletes personal files; D106 PAT ceiling on file action; D107 /guid auth + max 100; D108 leaflet direct.
- Units: A files+PAT (internal/files except module.go, pkg/extension/authn, authn/personal_token.go, identity erasure call);
  B webhook reclaim/fences/fan-out cursor/metrics + files cleanup metric (files/module.go) + iam-alerts rules;
  C observability/security misc (500 root-cause logging shared layer, audit-write warn logs, redaction keys, /guid, SAML key startup race);
  D OpenAPI validity (headers, anonymous structs, webhook in OPENAPI_ARGS, spec lint in openapi-check) + Windows testdb teardown;
  E frontend (switcher F1/F2/F4, files page effect split, loginError, trashedFiles dedupe, leaflet, prettier, tests).
  Main afterwards: pkg->internal guid ID move + arch gate scope, toolchain go1.26.9 + x/net>=0.60, regenerate openapi/api-schema, full make ci + frontend checks.
- Out of scope (not in presented list): env prefix, implicit config parse error, login normalization dedupe, tiptap dup, CGO pin, files cleanup scan cost, permission catalog dual track, ui dead code.
- Progress: A-E DONE (all mutation-verified). D105 revised: logical delete only, files untouched (no code). D106 refinement + D107 mechanism recorded.
  Main: openapi.json + api-schema.ts/api-routes.ts regenerated (webhook 5 ops, /guid auth max 100); toolchain go1.26.9, x/net v0.61.0 (crypto/sys/text/tools bumped) in root/tests/tools modules;
  guid.ID moved to pkg/guid with alias in internal/infra/guid; new tests/architecture/layering_test.go (pkg !-> internal, cross-module storage ban for all internal modules); lint 0 issues.
- Phase: ACCEPTANCE. Gates (2026-10-11): make ci all targets PASS (fmt, lint 0 issues, schema-lock, coverage 91.0% all modules >=90 min authn 90.2,
  test-integration architecture/goose/testdb/lifecycle/migration/performance ok, build, openapi-check, frontend-contract-check); generate-check PASS; test-alerts SUCCESS;
  frontend-check (api-types, lint, typecheck, test iam 204 + ui 86, build) PASS. layering gate mutation-verified.
- test-mysql8-run PASS (2026-10-11, remote docker host 10.0.0.100, mysql 8.4): all gate tests incl. enforced performance thresholds; schema lock 255 files unchanged.
  Gate script now also runs TestSpaceInitializationConcurrencyExternalDatabase (files) and new TestSAMLConcurrentFirstStartExternalDatabase. Remote host keeps 2 pre-existing Sept gate containers (not ours) and a cached alpine:3 image.
- test-race PASS (2026-10-11): 84 packages ok, 0 data races (MSYS2 gcc present; earlier "needs cgo" claim was wrong).
- test-redis-security PASS (plain + TLS; standalone/cluster/sentinel) on remote docker. MySQL-side mutation of the new concurrency tests not run (PG mutation done).
- Committed locally on chaosplus (user: "继续"), 9 commits 9a4ac32..(test(architecture) commit), each commit verified to build + vet in a temp worktree; pushed 2026-10-11 and fast-forwarded origin/IAM and origin/main to the same commit. User-owned .agents/, .claude/skills deletions, .gitignore, SKILL.md left unstaged.
- Next: nothing outstanding for this task. Remote host 10.0.0.100 keeps a cached alpine:3 image and two Sept gate containers (user decides).

## Deep review of 006df17..cd2c103 + deep audit - 2026-10-10
- Phase: REVIEW reported, awaiting user confirmation before any fix (deep-review stage 5). Deep audit (whole repo) next.
- Verified TRUE: (1) architecture TestQueryGate FAILS: internal/files/basic.go:71 concatenated-table-name + naked-table-literal (pushed branch is red);
  (2) TestConcurrentOwnRootInitializationReturnsOneRoot passes with pre-fix read-then-insert code (SQLite _txlock=immediate serializes) - not a regression test;
  (3) tenantWorkspaceLoader asks memberEntities for the OLD tenant on switcher navigation (getTenant reads window.location before commit);
  (4) workspace-switcher groupEntitiesUnderTenant is global -> every tenant row becomes a heading for an entity-only account; no other way to reach other tenants;
  (5) FilesPage effect deps include selectedNodeID/pageAfter -> every directory click refetches 1+E+D collections (+D 403s);
  (6) layout.tsx myPermissions failure => standing:false withholds tenant, contradicting loader's "failed lookup moves nobody"; layout.tsx:166 comment stale.
- Same class unfixed: read-then-insert space init in account_resource.go:202, root.go:126 (tenant), system.go:92 (system).
- Latent: MySQL clientFoundRows=true makes DO NOTHING report RowsAffected=1 -> root insert for nonexistent space. Fix by deciding on space.ID==candidate ID via query package (also fixes gate).
- Deep audit DONE (7 perspectives, main-agent verified): PAT file_view can PATCH /files/{id} rename/restore (http.go:290 guard view);
  webhook delivering never reclaimed (repository.go:209); erasure leaves user-managed personal files (account_resource Release/PurgeOrphaned resource-only);
  OpenAPI omits 5 webhook ops (OPENAPI_ARGS lacks --webhook.enabled) + invalid 3.1 headers/component names; 500 fallbacks drop err in 6 IAM modules;
  authn/federation audit writes `_ =`; Go toolchain 1.26.6 has stdlib vulns (govulncheck, fix 1.26.9; os.Root.MkdirAll used files/local.go:55);
  react-leaflet Hippocratic-2.1 license; frontend phone-code catch ignores loginError.
- make ci (2026-10-11): fmt/lint/schema-lock/test-coverage PASS (91.0%); test-integration FAIL = architecture TestQueryGate (real) + zzprobe (agent artefact, removed); rerun pending.
- Next: user confirms fix scope; then fix by class, mutation-verify, run backend make ci + test-integration and frontend typecheck/lint/test.

## Commit and push delivery - 2026-10-09
- User authorized all current changes to be committed and pushed to origin/IAM.
- Source/test changes reviewed; generated reports, credentials and local runtime files excluded. Prior verification evidence is retained below.
- Remote changes integrated by rebase onto 2805771; preserved remote navigation/department fixes and current source/test changes. Conflicts resolved without restoring retired historical task state.
- Historical no-commit/no-push notes below describe earlier phases and are superseded by this authorization.
- Integration checks PASS: frontend typecheck, lint, tests (IAM 186 pass / 0 fail), build, diff whitespace and conflict-marker checks. Self-review and requested commit/push acceptance reviewed; normal push is the final delivery action.

## Authorized organization file visibility repair - 2026-10-09
- User explicitly requested fixing positive-visibility failures. Phase ACCEPTANCE COMPLETE for this repair; direct execution, no delegation.
- Frontend FilesPage loads its current tenant/entity collection plus authorized entity collections (existing memberEntities(file_view)) and department collections (existing dept_view catalog filtered to selected entity). Names remain owner labels; backend collection APIs remain final authorization filters. Personal/system branches retain their single collection.
- All root pages are walked so catalogs do not truncate root navigation. Trash owner pages merged in ascending snowflake-ID order, bounded to 100 and stopped at minimum unfinished scan cursor to avoid lost entries. Unit tests cover empty scan cursor, merged limit, IDs above JS integer precision and malformed response.
- New iamApi.departmentFiles uses published existing route; files gains optional state argument. No backend, schema, route, grant, authentication or navigation redesign.
- Positive UI E2E added to live matrix: tenant 4, entity 3, department Finance-only 1 root; inspect actual sidebar root IDs/owner labels, click each root and reload. Department pilot desktop/mobile PASS. Expanded pilot session 66868 adds real run-owned active+trashed marker files in each owner root; verifies rendered contents, exact download bytes, refresh and authorized-only recycle bin. Fixture cleanup moves only its active files to trash.
- Final PASS: .temp/dev/role-operations-1791564163462/report.json, all seven accounts ? desktop/mobile (14 groups, 234 page visits), authorized tenant 4 / entity 3 / Finance-only department 1 roots. Real active marker files render, exact downloads match bytes, reload preserves visibility, and per-owner trash markers render only in admitted scopes. Four active organization fixtures cleaned to trash; existing files untouched.
- Additional negative PASS: .temp/dev/role-operations-1791564524917/report.json, department desktop/mobile directly request foreign tenant/entity/Operations marker file IDs; all rejected (403/404). Sidebar and recycle bin exclude foreign markers; fixture cleanup PASS.
- Optional department collection 403 is omitted because permission to see a department name does not imply file access. Unexpected storage/transport errors still fail the page; primary owner access is not swallowed.
- Static verification PASS: frontend typecheck, lint, IAM unit tests 169 plus shared UI test, build, and git diff --check. Final catch-path build and targeted eslint also PASS. No debug/scratch files retained.
- Self-review PASS: existing owner APIs, permissions and file-browser validation reused; bounded trash cursor math tested; root navigation walks all owner pages; no backend/grant/authentication/routing changes for this repair. Original positive-visibility acceptance PASS on desktop/mobile plus direct-ID negatives. Prior E2E coverage limitations (Passkey/MFA/PAT) remain outside this repair, not claimed complete.
- Next: user refreshes 8091 organization file page to accept restored directories. No remaining implementation work for reported visibility bug; changes uncommitted.

## Positive visibility follow-up - 2026-10-09
- User asked whether every role can actually see what it should. Rechecked live UI, not only successful page/API requests.
- Original failure (now repaired above): department-account GET Finance department files returned one authorized root, but actual entity-files UI showed "No file root is available in this scope". Entity-account UI showed only entity root although both authorized department collection endpoints returned roots.
- Cause: FilesPage fetches only iamApi.files(fileEntityID) (entity collection). Current backend collection contract returns only roots of that owner/scope. Department collection roots are not added to the browser's navigation.
- Earlier seven-role PASS covers enumerated core operations/menu loads/collection API boundaries, NOT completeness of visible UI data. Do not claim all expected visibility passed. Readonly live browser check confirmed both accounts; scratch script removed, no product code changed in this follow-up.
- Follow-up authorization: user then said "??"; repair and positive/negative acceptance completed in the newest section above. This original investigation is retained as pre-fix evidence.

# Task State

> 已交付并提交:社交 connector(`bb89072` / `1c9b77d`)、webhook 模块(`b6d3b27`、D97)、
> device flow + token exchange(`8187c19`、D98)、角色授予期限(`9bdddd6`、D99)、
> 手机号 + PII(`931fdd3`、D100)、手机号验证码 + 控制台三屏(`a400ac8`、D101)。

## Task
用户批准的四项补齐,全部交付:
1. ~~Webhook~~ `b6d3b27`(D97)
2. ~~Device flow + Token exchange~~ `8187c19`(D98)
3. ~~角色授予期限~~ `9bdddd6`(D99)
4. ~~短信~~ `931fdd3`(D100,号码+PII)+ `a400ac8`(D101,验证码)
用户追加「把这个收掉」:
5. 手机号登录(D102)+ 短信第二因子(D103)—— 本轮,待提交

## Objective
让已证明的号码既能当第一因子(发码登录),也能当第二因子(应答 MFA 挑战),
并且第二种方式不能变成绕过第一种所要求的 MFA 的路子。

## Current Phase
ACCEPTANCE —— 已提交推送 `d47ee12`。

## Completed Work(本轮)
**手机号登录(D102)** `internal/iam/authn/phone_login.go`
- `BeginPhoneLogin` 对外只有一种回答:没人证明过的号码、两个账号都证明过的号码、
  真能登进去的号码,从外面看完全一样(发码的人不一定是号码的主人)
- 锁定账号不发码,用的是密码连错同一把锁 —— 两条路进不互相兜底
- `CompletePhoneLogin` 把码的读取与错码计数放在**签发会话的事务之外**
  (拒绝以 error 退出事务会把计数一起回滚 —— D101 踩过)
- 只有联邦登录过、没有 credential 行的 principal 也能用:缺行按零值读

**短信第二因子(D103)** `internal/iam/authn/sms_factor.go`
- 没有独立的 enrollment:principal 上那个已证明的号码就是 enrollment
- 一条规则让它是「第二」因子:用发到某号码的码开的登录,不能再用发到同号码的码完成。
  挑战表新增 `first_factor`(authn 迁移 00020,里程碑 `mfa-first-factor@20`)
- 用码时重读 principal:码只证明它被发往的那个号码,那号码必须还在账号上且仍已证明

**公共入口** `web.go` 的 `completeFirstFactor` 从 `BeginLogin` 抽出
—— 每一种第一因子都必须过同一个 MFA 门,复制尾部早晚会复制出一条绕过。

**前端** 登录页手机模式(发码 / 填码)、MFA 屏「发送短信验证码」、安全页报出
`sms_available`(已证明的号码即是 enrollment,否则会以为自己没有第二因子)

## 本轮抓到的真安全缺口
**只有短信因子的账号,错误的第二因子验证码原本完全不计数** —— 我写的早返回绕过了
既有的失败计数,六位数在验证码有效期内可以无限猜。现在错码走回和 TOTP 同一条失败
路径,计入挑战尝试上限。测试先红才暴露 —— 我原本的断言写的是自己实现的(错的)行为。

## MySQL 真库门禁(docker)抓到的两个缺陷
`oauth/sql/mysql/00011`(3 处)与 `webhook/sql/mysql/00001`(1 处):
**MySQL 不接受 TEXT 列上的字面量 DEFAULT**,必须写成括号表达式 `DEFAULT ('')`。
三引擎嵌入式测试与 sqlite/postgres 都看不见,只有真 MySQL 会拒。
`tests/migration/mysql8_test.go` 的模块清单原来是手写的、漏了 webhook ——
已改为从迁移契约派生,之后不会再漏。

## Confirmed Constraints
- 手机号是 PII:审计明细只记「变了」,不记号码;新表进擦除清单。
- **号码能登录,所以它是凭据**:换掉或删掉一个已证明的号码会提升 credential version
  并吊销全部安全状态(`identity/service.go` 的 `provenPhoneReplaced`,D100 起已实现)。
  第一次填一个未证明的号码不吊销任何东西 —— 它什么也换不到。
- 不接任何短信厂商:出站仍是既有通知服务的 POST,只多两种 kind。
- 两项都受配置开关管辖(`PhoneVerification.LoginEnabled` / `.SecondFactorEnabled`),
  默认关闭:短信是这里最弱的第二因子,开不开是部署的决定。

## Verification
Build / lint / fmt / schema-lock: PASS
覆盖率: 总体 91.0%,`internal/iam/authn` 90.1%(门禁 90.0%)
test-integration(architecture / goose / lifecycle / migration / performance): PASS
openapi-check(文档与生成一致)/ frontend-contract-check: PASS
前端 typecheck + lint + 164 测试: PASS
真 MySQL 8:**迁移与执行计划 PASS**(这是验证两处 TEXT 默认值修复与 00020 的那一半);
**延迟阈值 FAIL,原因是本机而非代码,阈值未动** —— 见下。

## 本机 MySQL 延迟门禁的判定依据
四个基准全部是基线中位数的 2.6~4.8 倍,只有 authorization 越过了自己的上限:
| 基准 | 语句数 | 基线 | 本次 | 倍数 |
|---|---|---|---|---|
| authorization | 7 | 2.15ms | 10.25ms | 4.8x |
| audit_append | 4 | 3.12ms | 9.63ms | 3.1x |
| session_lookup | 3 | 3.14ms | 8.17ms | 2.6x |
慢的倍数与**每次操作的语句条数单调相关**,折算下来是每条语句多出 1.2~1.7ms 的固定开销
—— 这是本机每次往返的代价被放大,不是某条查询变慢。若是角色期限谓词所致,多出的代价会
集中在一条语句上,而不是均匀摊在所有路径的每条语句上。另一个独立证据:authorization 那
7 条语句的优化器估算代价(含那条 CTE 的 0.66)在加谓词前后**逐条完全相同**。
参考机上的 CI 才是这半边门禁该出判定的地方。

## 追加交付:鉴权少 4 次往返(D104)
`CheckBulk` 原先无条件先问「是不是平台管理员」,而那个答案要查 4 张表,
且对绝大多数请求根本不可能改变结果(租户自己的授权已经允许了)。
改为只在它真能决定判定时才问:**一次鉴权 7 条语句 → 3 条**,这是全服务最热的一条路。
`platformAnswerDecides` 写成判定循环的精确反面,两边漂移是这件事唯一会变成安全缺陷的方式。
变异验证两边都红(强制总是问 / 强制永不问)。ci18 全绿(覆盖率 91.0%,core 90.4%)。

## 未完成项(刻意留下)
**性能基线的 `plans` 块需要在安静的参考机上重新生成。**
`authorization` 路径的冻结语句集合现在多记了 4 条代码已经不发的语句,
`TestIAMPerformanceExternalDatabase` 会报计划签名不匹配,直到在参考机上刷新。
**绝不能在负载高的机器上刷新**:阈值 = 最慢采样 × 3,今晚这台机器会把
authorization 的阈值写成 42ms(仓库里是 7ms),等于把门禁放宽 6 倍并冻结进仓库。
刷新时只取新的 `plans` 块,`benchmarks` 块保留仓库现有的(那是安静机器上录的)。

## 本机为什么慢(已查明)
`XprotectService` 连续 8 小时占满一核,在扫测试产生的临时数据库与 Go 构建缓存;
加上 Docker 的 MySQL 跑在虚拟化文件系统上。同一个 mysql8 门禁上一轮 15 分钟,
这一轮 3 小时仍未跑完,已主动停止并清理了它的容器。
根治需要你那边一个系统设置(隐私与安全性 → 开发者工具 → 加入 Terminal),我没有替你动。

## Next Action
参考机上跑一次 `make test-mysql8`,把 `authorization` 路径的 `plans` 块刷新
(只取 plans,保留 benchmarks)。其余无遗留。
可选(未获批准,未动手):测试套件加速 —— (1) 缓存 fixture 口令哈希,
(2) 每包迁移一次后按文件拷贝;实测 argon2id 单次 31.3ms、每测试建库 55ms、
覆盖率插桩 1.8 倍。

## Last Checkpoint
2026-10-04 `d47ee12` 已推送;手机号登录 + 短信第二因子 + 两处 MySQL 缺陷全部落盘

## 2026-10-12 原型全 UI 复审 checkpoint（进行中）
用户接受需求审计建议与 React Flow，并要求删除协作跨域频道菜单、全 UI 易用性/性能复审。D125–D130已记录；仅规划与静态原型，未提交推送，未开工产品。文档完成PRD/spec00–12及商业/试点评估；React Flow本地编辑器完成。420px探针遍历340路由，无运行错误/断链/鼠标独占操作；policy局部横溢出由pages修复。成员1000/消息500验证DOM上限20或50/100，非生产压测。交付守卫、WI/pjWork同步已修复回归。workflow代理修工时唯一集合和0.25输入、复验override，并修工作流概览投影与LOOP契约。docs代理继续独立复审。主线程待最终回归、UI复审报告、审计状态更新。服务8765/浏览器myrmidon-root；.temp/audit-setup.js有效。

### 复审补充 checkpoint
主线程完成1440×900与420×800各302路由第二轮巡检（errors/broken/unlabeled/clickOnly/overflow均0）；甲方13正常与越界路由无外项目名称/内部链接。个人工时15分钟→Morgan项目审批→周报75分钟已真实通过；任务看板、评审、流水线1000条分页边界通过，单页翻页控件隐藏，小屏默认任务列表。甲方通知偏好本地状态已接线，未实现语言选择撤下。23个JS/2内联脚本语法检查PASS，Markdown21文档26本地链接PASS，diff check PASS。报告research/2026-10-ui-usability-review.md已建。最后仍等待workflow共享定义/LOOP/gate/锁定实例修复完成、docs独立复验，随后最终build/全站回归并记录完成。未进行生产压测，未提交推送。

## 2026-10-12 当前任务完成 checkpoint
D125–D130已落实到规划与原型：React Flow本地bundle、共享WorkflowDef唯一真相/模板发布投影/项目锁定只读实例；循环完整约束、DAG/入口可达与关口必经；结构化补丁、撤销重做、JSON导入导出。协作频道侧栏已删除。全UI导航/操作/可访问性/小屏复审及相关缺陷修复完成：交付共用守卫、真实自动结果覆盖、WI/pjWork同步、工时共享集合、搜索过滤、消息锚点、看板/评审/流水线/成员分页。三条workflow原问题独立二次复验全PASS；额外undo/redo三态、导入保存重载/无历史变化、坏循环导入/2MiB拒绝、完整JSON导出及update_workflow metadata补丁实测PASS。最终build/23JS+2内联语法/diff check/21Markdown26链接/LF无BOM PASS，浏览器无错误。报告docs/myrmidon/research/2026-10-ui-usability-review.md；PRD/spec00–12与试点评估/商业验证完成，需求审计批准状态已更新。无产品代码变更，无提交推送；未做生产压测，生产负载门槛在00§16，下一步用户验收原型后才开工。8765静态预览保留（session62859），代理服务8766已停。

## 2026-10-12 提交推送 checkpoint
用户授权全部提交推送，随后要求两份状态文件移到根目录ADR.md/MEMORY.md，已移动并同步CLAUDE与文档引用。deep-review独立文档/工作流复审无运行阻塞；第三方notices遗漏use-sync-external-store已核实、补生成器并重建。A1本地草稿保存/发布与导入往返已验；A2 2MiB+1与200节点上限已验；A3评审/覆盖/交付守卫已验；A4甲方/项目隔离已验；A5只含分钟工时演示，无真实账务；A6覆盖重复与状态确认已验，无服务端副作用；A7锁定历史/缺失版本已验，无生产存量迁移；A8只读轮询为显示投影，无后台调度实现。SQL/事务/生产OpenAPI/数据库门禁未触发；根make ci为后端门禁，不跑无关产品测试。计划提交全部相关源文档、本地原型运行bundle与锁文件；不提交node_modules或.temp。

补充：.claude/.gitattributes仅Markdown LF规则，按用户根目录整理一并移动为根.gitattributes，不批量renormalize。提交前build与23JS/2内联语法再次通过。

## 2026-10-13 PRD 通用工作平台重整

- Task: 用户要求优化 `docs/PRD.md`，以通用 AI 工作平台为方向，补全 OPC / 多人、多模型 Agent、协作会话频道、文件知识记忆与整体架构。
- Phase: ACCEPTANCE / COMPLETE；执行模式 Direct，单文档语义紧耦合，不委派。
- Scope: 重写主 PRD，保留已有治理约束与章节引用兼容，记录 ADR；不实现产品、不改旧规格与原型、不提交推送。
- Findings: `docs/PRD.md` 当前未跟踪，旧 `docs/myrmidon/PRD.md` 与四份 docx 的既有删除保持不变。规格仍在 `docs/myrmidon/specs/`，主文档链接已修正。
- Design: 通用工作内核 + 专业工作包；协作频道 / 线程 / 执行会话分离；上下文四种来源分离；保留 L1–L4 但仅在实际外部交付时要求 L4；新增契约和分期明确待规格技术评审。
- Completed: PRD 2.0，24 个编号主章节、120 个编号章节/小节、6 张 Mermaid 图、18 个产品验收场景；§21 协作、§22 上下文、§23 专业工作包，§18.3 明列旧规格衔接；D132 记录规划与实施批准边界。
- Verification: Python 检查 30 个链接（含 8 个锚点）、全部章节引用、表格列数、围栏、LF / 无 BOM、验收编号均 PASS；Mermaid 实际解析 6/6 PASS；git diff --check PASS，未跟踪 PRD 另作 whitespace 检查无问题。自审与原请求覆盖检查 PASS。未运行产品代码/生产测试，本文场景为未来验收标准。
- Next Action: 本轮文档任务无剩余。后续若进入实施，先由产品负责人冻结 §18.2 的范围与政策建议，再按 §18.3 同步规格与原型；不视作已授权开工。未提交推送。

## 2026-10-13 PRD 产品主线重写（用户否定上一版）

- Feedback: 用户指出“你这是在堆 UI，不是在做产品”。上一版文档结构检查通过不代表产品设计获认可；其产品评审未通过。
- Phase: 文档修订完成 / 待用户产品评审；Direct，单一产品主线由主线程完成，不委派。
- Goal: 从真实工作、用户承担的协调成本、平台责任、成果交付与持续复用定义产品；协作会话、知识和 Agent 作为必需机制，界面与技术目录退回实现约束。
- Scope: 重写 docs/PRD.md 与连续性记录；保持通用方向、OPC / 多人 / 外部协作、不同专业与模型，保留已确认治理约束，不改规格、原型或产品代码。
- Steering: 用户明确要求不同人视角、通用流程与业务兼容、Agent + Human 内部协作、会话/执行/实时反馈预览、减少页面与路由冗余且不减功能；均纳入新版主线。
- Completed: PRD 3.0 按上述问题重写；用户责任与实际路径、完整发布资料案例、通用推进/专业步骤分离、分派交接与例外、运行反馈和三类成果版本、规范路由与冗余合并表、完整能力归属、价值验证及 18 个验收场景。D133 记录用户纠正与提案边界。
- Skill: ui-ux-pro-max；仅查询导航返回/深链接与反馈规则，保留适用条目，未生成新视觉系统；检索产生的 __pycache__ 已清理。
- Verification: 31 个链接/锚点、章节引用、表格、围栏、18 个验收编号、LF/无BOM与空白检查 PASS；Mermaid 实际解析最终 4 图全部 PASS；git diff --check PASS。已逐项自审用户七类要求与能力保留映射。未修改原型/规格/产品代码，未运行生产测试。
- Next: 本轮文档修订无剩余执行事项。用户评审产品方案；若进入实现，按 PRD 附录 B 冻结个人归属/可见政策/路由合并并同步工程规格与原型。未提交推送；不能以静态检查通过称为用户已接受。

## 2026-10-13 完整需求清单（24 组补充）

- Task: 合并原材料与用户编号 1–24 的要求，生成完整需求清单；第 23 项服务器 / 服务健康群告警、第 24 项环节小团队交付主流程均已纳入。
- Phase: 文档完成 / 待用户产品评审；Direct，主线程统一语义与覆盖核对，不委派。
- Completed: docs/REQUIREMENTS.md，26 个领域共 286 个稳定需求编号、24 个端到端验收场景、原编号 1–24 完整映射、两张 Mermaid 图；PRD 顶部加入最新入口与冲突提示；D134 记录本轮范围。
- Confirmed: 会话协商 → 计划任务确认 → SOP 确认 → AI 执行 / Human 负责的环节小团队 → 内部验收 → 下游交接或上游返工 → 最终 Human 负责交付。包含组织树、跨项目大盘、永久消息、分布式执行与预览、全平台、监控、预算和自动报告等完整范围。
- Design boundaries: 技术建议与明确需求分开；Matrix / Centrifugo / NATS 职责不同；领域事实 + 原件 + 审计事件 + 可重建搜索 + 有来源记忆。runtime、跨平台兼容与资源指标待真实验证，不宣称功能已实现。
- Verification: 286 需求编号唯一且分域连续、编号引用、0–28 章节、16 个本地链接 / 锚点、表格列数、围栏、LF / 无 BOM / 无行尾空格、1–24 来源映射、AC-01–24 均 PASS；Mermaid 实际解析 2/2 PASS；文档 diff check PASS。自审与原请求覆盖检查完成，非用户产品批准；未运行产品集成或性能测试。
- Scope: 本轮仅需求文档与连续性记录，不提交推送。并发发生的前端目录迁移、客户端 / runner 初始化和 AGENTS 更新属于其他工作，保持不动。
- Skill: full-output-enforcement 用于完整输出与来源映射。
- Next: 本轮文档任务无剩余执行事项；交付清单供用户审阅。实施选型与规格同步属于后续任务，不因文档完成而自动开工。


## Dynamic API field-rules design review — 2026-10-13
- Task / Objective: assess runtime OPTIONS field rules derived from backend declarations, frontend binding and structured errors; deliver reviewed implementation plan only.
- Phase: ACCEPTANCE / COMPLETE. Production code untouched; all existing workspace migration/scaffold changes preserved. No commit/push.
- Deliverable: apps/backend-api/docs/dynamic-field-rules-design-2026-10-13.md; recommendation appended to ADR.md.
- Execution: Hybrid, two read-only investigators (backend metadata/auth/CORS; frontend forms/rules) plus same agents independently reviewing final draft; main integrated and accepted.
- Findings: actual OpenAPI 3.1 = 275 paths / 398 operations, no OPTIONS; 33 Web TSX files / 48 form occurrences (includes search); AutoForm already supports rules but business pages do not use it; respx flattens field errors. Registration tag ≥8 characters vs service ≥12 bytes is a pre-existing divergence, not changed.
- Recommended decisions: final Huma schemas -> OPTIONS -> shared frontend adapter; preserve schema structure; controlled message templates; typed server issues; unknown tri-state composition; explicit server-validation fallback; follow OpenAPI exposure switch; per-open-form application cache; exact final-payload validation. No new general rules DSL or bulk UI rewrite.
- Verification: make openapi-check PASS (generated document matches); go test ./pkg/extension/huma/... PASS; subagent authz tests PASS; two frontend suites 69 PASS / 432 assertions; doc whitespace/link checks recorded in report at final delivery. New feature not implemented/tested.
- Remaining work in this review: none after final document checks. Implementation is future scope, not approved by this request.
- Next action: after user requests implementation, follow report §8 phase 1 contract/consistency baseline; do not silently change password policy.
- Last checkpoint: final review integrated, 2026-10-13.


## Dynamic field-rules review refinement — 2026-10-13
- Phase: ACCEPTANCE. User adds four hard requirements: unified UI automatic input limits/validation; one backend tag declaration source; three-language messages; resolve every affected path with no dangling items.
- Completed: design revision 2 and ADR addendum; shared form context/field adapters, service validation reuse, message retranslation, complete impact ledger and zero-unresolved gate specified.
- Scope: documentation only; Direct refinement based on earlier independent audits. No production edits or tests claimed.
- Supersedes prior state: password tag/service divergence and price/date conversion now MUST be fixed within future implementation, preserving actual accepted behavior. Unknown-rule compatibility is not permission to leave current supported scope unimplemented.
- Next: implement only on user request, starting full affected-path ledger and declaration baseline; pilot does not count as completion.


## Dynamic field-rules exhaustive impact closure — 2026-10-13
- Phase: ANALYSIS. User says continue after assistant clarified prior audit was sampled; current goal is complete structural impact matrix and bounded semantic review, not production implementation.
- Constraints: UI automatic binding and input limits; tag-owned single declaration; zh-CN/en-US/ms-MY; all identified affected issues assigned implementation and acceptance (no deferred known issues).
- Execution: Hybrid. Agents own backend module/service matrix, all Web forms/non-form mutations, shared UI/client/i18n matrix; each writes its own impact document under backend-api/docs. Main owns all-operation inventory, specification consistency, integration, cross-checks and final acceptance.
- State: existing 398 operations/275 paths and 48 form occurrences are previous verified baseline; recount against current filesystem before claims. Preserve unrelated changes.
- Next: generate operation/source/parameter inventory, inspect runtime-only exceptions and tests; integrate 3 matrices, resolve review gaps, update main plan/ADR and verification.


## Dynamic field-rules exhaustive closure — final checkpoint 2026-10-13
- Phase: ACCEPTANCE / COMPLETE (design + impact audit only). User withdrew accidental project-menu request; no UI/layout or production code changed.
- Deliverables: main dynamic-field-rules-design-2026-10-13.md v3 + backend-impact, frontend-impact, ui-impact, operation-inventory dated same; ADR v3 appended.
- Coverage: actual composition baseline398 ops matches committed spec exactly; all optional infra411 documented ops; adapter372 Huma +26 raw;13 optional raw docs;1 wildcard path rewrite.122 input schema refs.20 HTTP modules /357 enumerated production files.96 Web sources /54 UI entries /48 forms in33 files;262 API members resolved,264 unique routes within them;all iam-api routes292 +account-view2=294 valid spec pairs.31 FieldTypes exact-set checked.
- Integrated findings: typed/raw provenance with OperationDocumenter+Adapter.Handle; late OAuth form schema not executable proof; compile declaration before Huma precompute; preserve service profile/normalization; issue_messages multilingual catalog; direct account-view transport, synchronous popup/best-effort exit; preserve existing401 retry; no invented legacy shared-library endpoints. D-UI01–06 frozen in main§12.2.
- Verification: Go overlay actual composition/route/provenance tests PASS (temp files only); policyx/query PASS; files/todo/authn targeted4 test names across3packages PASS. UI6suites105PASS plus account-view/org-scope2suites9PASS.5doc links/examples/LF/whitespace,369 links,411op and31FieldType sets PASS;OpenAPI fingerprint unchanged. All are baseline/audit checks, not new feature runtime acceptance.
- Remaining audit work: none; peer corrections incorporated, primary scope/acceptance review complete. Every identified impact has an action/test ID; semantic implementation cases remain NOT IMPLEMENTED/NOT RUN.
- Next: on implementation instruction follow main§8 and4matrices to full completion; pilot is not delivery; no new permission needed for further read-only audit, but this requested audit is finished.
- Repro evidence: /tmp/dynamic-rules-audit holds overlay/runtime snapshots/generator; /tmp/recount-frontend-routes.cjs and /tmp/frontend-route-sets.json; durable docs preserve results if temp files expire.


## Commit and push all deliverables — 2026-10-13
- User authorization: commit and push all current workspace deliverables; supersedes earlier no-commit notes. Do not implement dynamic field rules.
- Scope: Web directory/package migration, Tauri and runner initialization, product requirements/prototype/reference documents, engineering instructions, dynamic field-rules design and exhaustive impact reports.
- Phase: ACCEPTANCE / ready to commit and push; branch chaosplus, origin/chaosplus fetched and initially synchronized.
- Review: independent read-only naming/reuse/architecture/cross-platform review plus main inspection. All 375 relocated files mapped; 367 byte-identical, eight path/name/doc changes. Corrected external historical source reference and current directory tree. Ignore Python caches and local editor settings; all build/dependency output excluded.
- Risk review: production A1 staging, A2 uploads, A3 workflow, A4 isolation, A5 money, A6 replay, A7 persisted schemas, A8 jobs unchanged by relocation; runner has no executor. Prototype is local demonstration, not production implementation. No HTTP/schema/SQL behavior or production dependency version changes; no database/release gates required for this scope.
- Verification: prototype 16 tests PASS and Vite build PASS; runner go test reports no test files, build and startup PASS; backend script syntax PASS and affected integration package compiles (browser cases require external fixtures and skip by default). make frontend-check PASS (API types, lint, typecheck, 290 tests, production build); cargo fmt --check and cargo check --locked PASS on macOS. Windows/Linux runtime and fixture-dependent browser integration not rerun. Prior dynamic-field audit validation retained above.
- Self-review / acceptance: requested deliverables staged explicitly, rename-aware diff reviewed, whitespace and staged LF checks PASS; no generated caches, binaries or local editor config staged. Only current-task hygiene and two migration documentation corrections added.
- Next action: commit the reviewed staged deliverables, push origin chaosplus, then verify remote HEAD and clean working tree; no product implementation authorized by this commit request.
