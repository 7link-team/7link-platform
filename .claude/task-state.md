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
