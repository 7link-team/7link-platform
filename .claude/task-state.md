# Task State

## Current Delivery Run (2026-09-07)

## Current Phase          IMPLEMENTATION
## Current Step           批 0 — 修复红线基线（gofmt/tests lint 已绿，migration fixture 待落地）

**范围锁定**：只关审计的 P0-1..P0-5 + 门禁转绿，不含 P1/P2。见 D22。
权威清单是 `apps/backend-api/docs/iam-production-readiness-audit-2026-09-07.md`。

### 批次计划（D22）
- 批 0：gofmt、tests 子模块 lint、integration migration 四处失败。
- 批 1：SSRF 出站 policy；SCIM 删除责任链（D23）；lifecycle API→OpenAPI→前端类型→UI；
  七 owner 填充数据与并发验收。
- 批 2：覆盖率行为测试补齐。
- 批 3（串行）：全量门禁、冻结 195 迁移的 schema.lock、真实 MySQL/race/generate、
  UAT 环境、备份恢复与 rollback 演练。

### 批 0 进度
- [x] `internal/iam/authn/recovery.go` gofmt 对齐。`gofmt -l ./cmd ./internal ./pkg ./tests` 空。
- [x] tests 子模块 lint 3 项：`testdb.go:101` 未检查 `engine.Close()`（改 `require.NoError`）、
      `testdb.go:178` 未检查类型断言（改 comma-ok + `require.True`）、
      `iam_core_model_test.go:23` 未使用的 `databaseColumn`（确认为真死代码后删除——
      同样的 name+nullable 信息由 `map[string]bool` 承载，`assert.Equal` 已同时比较列名集合
      与可空性，无断言丢失）。`cd tests && golangci-lint run ./...` 退出 0。
- [ ] migration fixture 三处过期断言：`iam_federation_test.go:107` 应为 `principal-erasure`
      （federation 顶点是 v5 不是 replay v4）；`milestone_test.go:42/43` 应为
      `lifecycle`/`principal-erasure`。已核实是擦除迁移合法新增后 fixture 未跟上，非回归。
- [ ] `milestone_test.go:67` 回滚错误文本断言：**不得**照实现输出反填，需先确认正确期望。
- [ ] `iam_core_test.go` 按 D25 组合 identity+core 依赖闭包。
- [x] migration fixture 三处过期断言已改（federation→`principal-erasure`、identity→`lifecycle`、
      authn→`principal-erasure`）。
- [x] `milestone_test.go` 回滚错误文本：按 `sortMilestoneKeys`/`formatMilestoneKeys` 推出正确
      格式（单数→复数、并列两个 identity milestone），并**新增** `"authn.principal-erasure"`
      断言——加强而非反填。
- [x] `iam_core_test.go` 按 D25 先迁 identity（74 行降到 11、190 行降到 10 都跨 v13）。
- [x] **真实缺陷**：`internal/app/migration_iam_status_integration_test.go:180`
      `iamMigrationReverseOrder()` 把 identity 排在 core 之前，而 core 的
      `principal-erasure`(v13) 依赖 `identity.lifecycle`，守卫正确拒绝孤儿依赖。
      该函数仅测试使用（定义 180、调用 57），**生产路径无此缺陷**。
      按完整依赖图核对，其余顺序全部正确，仅此一处错位。
      判定：这份硬编码顺序是**刻意的金丝雀**（独立于依赖图声明期望），改成拓扑排序会让
      测试自证并失去保护，故更新清单而非替换机制。
- [x] **真实缺陷**：`status_integration_test.go` 的 `require.Len(t, owners, 67)` 应为 68。
      `c7a02b1` 加了 4 张表（field_policies / principal_lifecycles / audit_archive_jobs /
      federation_replay_claims）却把冻结总数从 64 改成 67，少加 1。68 由 64+4 推出。
- [x] **自身回归已修**：`testdb.go:101` 改 `require.NoError` 过严——`t.Context()` 在 cleanup
      前已取消，`engine.Close()` 返回 `context canceled`，且 cleanup 内不应 FailNow。
      改为容忍 `context.Canceled`、其余 `t.Errorf`，与上方 `net.ErrClosed` 处理一致。

### 批 0 追加发现（审计低估）
审计称 integration 只有 3 处过期 fixture。实际展开为 **17 处失败点**，散布在
governance/organization/provisioning/oauth/core/module 多个文件。归为三个根因，全部源自
`c7a02b1` 加入 `principal-erasure` 后 fixture 未跟上：
1. 顶点 milestone 断言过期 8 处（`field_policies`/`constraints`/`consent`/`service_accounts`
   → `principal-erasure`），逐一对照各模块 `migrate.go` 声明的顶点后修改。
2. `governanceMigrationModules()` 缺 identity → `requires unknown milestone
   "identity.lifecycle"`。兄弟测试 organization/provisioning 本就包含 identity，补齐一致。
3. 回滚顺序 identity 排在 core 前，另发现一处 `verifyProvisioningRollbackGraph:235`。
另：`migration_test.go` 的三条路径（prefixed / maximumPrefix / 未加前缀的 verifyLifecycle）
都做全量 up 再降到 0，跨 core v13 需要 identity。为此给 `moduleMigration` 增加
`dependencies []goosex.Source` 字段并抽出 `migrateDependencies` helper 复用。
`iam-core` 的 `target: 12` → 13（core 最高迁移为 `00013_principal_erasure_guards`，
`status.Target` 必为 13；语义已用 identity=4、iam-audit=3 交叉验证）。

**自我纠错两处**：(a) 我新增的 `"authn.principal-erasure"` dependent 断言是错的——
`verifyAuthnQueryIndexLifecycle` 结束时 authn 停在 v11，v13 未应用，守卫正确未列出；已撤销，
未去改动正确的实现。(b) `require.NoError(t, engine.Close())` 在 cleanup 中过严，已改为容忍
`context.Canceled`、其余 `t.Errorf`。

**取证**：governance/organization/provisioning 那批失败，经 `git stash` 全部改动后重跑
同一子用例仍然失败，确认为既有问题而非本轮引入。

### 验证结果
`go test ./migration -count=1` → **ok 7link.com/platform/tests/migration 230.387s，零 FAIL**。

### 超时问题：已定论——当前 HEAD 不复现
实跑 `make test-integration`（原命令、原默认超时，未加 `-timeout` 覆盖）：6 个包全部 ok，
migration 包在多包并行下 **252.394s**，远低于 Go 默认 600s，零 timeout/panic 标记。
主 agent 先前倾向「超时为真」的判断**是错的**。无法断言审计当时有误（可能机器负载或冷缓存
不同），但当前代码在真实门禁命令下不触发超时。
**遗留风险**：252s 对 600s 仅约 2.4 倍余量，负载更重的 CI 机器仍可能逼近。记入批 3
发布流水线风险，本轮不处理。

## 批 0 —— 已完成（2026-09-07）
门禁实测：fmt-check PASS / 主模块 lint PASS / tests 子模块 lint PASS(exit 0) /
test-integration PASS(6 包全 ok) / build PASS / openapi-check PASS(matches docs/openapi.json)。
**覆盖率仍为 FAIL 89.9%**，按 D22 属批 2，批 0 未涉及——「门禁全绿」尚未达成。
改动 12 个源文件（+1 决策 +1 状态），仅 1 个非测试文件（`recovery.go` 纯格式化）；
生产行为零改动。`bin/` 为 gitignore 覆盖，工作区无多余产物。未提交。

## 批 1 进行中 —— P0-1 (SSRF) 已完成

新增 `pkg/extension/httpx`（97%+ 覆盖）：按 OWASP 通行做法，检查发生在 `net.Dialer.Control`
（socket 连接前的解析后地址），这是唯一能防 DNS rebinding 的位置；每个 redirect 跳都会重新
拨号因此同一守卫覆盖整条链；显式禁用 Proxy（含环境代理），否则拨号目标是代理、地址检查失效；
`Policy{}` 零值即严格（拒明文 + 封全部保留网段）。拦截清单为显式 CIDR 而非 Go 地址判定函数，
因后者不含 CGNAT/benchmarking/240-4，且安全控制须可被评审者直读。

已接入 federation（覆盖审计漏列的 `saml.go:1031`、`verifier.go:153`）与 provisioning
（漏列的 `target.go:347`），并接通部署配置 `outbound_allow_plaintext` /
`outbound_allow_networks`（per-module，理由见下）。补齐 metadata/私网/link-local/回环/
明文/重定向负向测试及启动期 CIDR 校验。

**行为变更**：`validTargetURL` 原硬编码放行 `http://localhost`/`127.0.0.1`/`::1`，已改为
配置驱动——本地目标靠 allow 网段放行，不再把例外编译进每个部署。受影响测试现显式声明允许
回环，反而证明守卫生效。

**配置为 per-module 而非全局**：最小权限——允许 OIDC IdP 访问某内网段不应自动让 SCIM 也能
到达；审计要求的「统一」指机制统一（都走 httpx），已满足。与既有 per-module `http_timeout`
一致。

### 附带完成（原属批 2）
- 七个 owner 的 `ErasePrincipalData` 原为 0% 单元覆盖且**全部缺 `lifecycle_test.go`**
  （违反测试文件一一对应约定）。已补 authn / core / organization 三个，含守卫分支、
  填充数据删除、无关主体保留、策略修订号 bump 与「不殃及无关租户」。余四个待补。
- `core/model` 的表名契约清单漏了 `FieldPolicy`（加字段权限时未同步），已补，13→14。
- 修复既有时间 flake `TestRewrapSecretsCommandRefusesToPrintADateThatReadsAsPermission`：
  断言用「今天」而 `clear_after` 是 now+90min，UTC 午夜前 90 分钟内必挂。改为让 seed 返回
  实际写入的过期时刻再断言，保持精确、不削弱。

## Verification（2026-09-07，实测）
`make ci` **EXIT=0**：fmt-check / lint / test-coverage / test-integration / build /
openapi-check 全绿。总覆盖率 90.1%，**每个模块均 ≥90%**（core 89.6→90.0，
organization 89.8→90.0+）。tests 子模块 lint 独立跑 EXIT=0。
审计所列四项红线门禁已全部关闭。

## Next Action
批 1 余项：P0-2 SCIM 删除责任链（D23：默认 fail closed + 显式 deprovision 任务）；
P0-3 platform-only lifecycle API→OpenAPI→前端类型→UI（内部串行）；补齐其余四个 owner 的
`lifecycle_test.go` 与七 owner 填充数据/并发验收（10.8）。

## Verification
Build: NOT_RUN（本轮改动后）/ 主模块 lint: NOT_RUN / tests lint: PASS /
gofmt: PASS / integration: FAIL（migration 包，待修）/ 覆盖率: FAIL 89.9%

## Next Action
等子 agent 的超时排查结论返回（其间不得改 `tests/migration/*`，会打断它的编译）。
返回后按序落地：三处过期 milestone 断言 → 确认回滚错误文本的正确期望 → 按 D25 改
`iam_core_test.go` → 重跑 `make test-integration` → 全绿后进批 1。

## Last Checkpoint        2026-09-07 批 0 lint/format 已绿，migration 待修


## Deep Audit Checkpoint (2026-09-07)

Current branch remains **NOT READY** for production. The authoritative audit is
`apps/backend-api/docs/iam-production-readiness-audit-2026-09-07.md`.

New production blockers found beyond the 2026-09-06 handoff:

1. Tenant-configurable OIDC issuer and SCIM target requests lack an SSRF-safe
   transport/address policy.
2. Deleting a SCIM target cascades its remote mappings without deprovisioning the
   remote account, allowing the principal-erasure remote-mapping check to be bypassed.
3. Current verification is red: committed `gofmt` drift, backend coverage 89.9%
   below 90%, integration migration rollback/version fixtures fail and time out,
   and the independent tests module has three lint findings.

Frontend typecheck/lint, 45 IAM + 85 shared UI tests, production build, backend
main-module lint/build/OpenAPI check and the 171-entry published schema-lock check
PASS. The source tree has 195 migration files, so 24 newer migrations remain
unfrozen. Race, real MySQL 8.4, generation stability, lifecycle browsers and
backup/restore/rollback acceptance were not rerun on current HEAD.

Next action order: close outbound SSRF and SCIM deletion/erasure accountability;
finish platform-only principal lifecycle HTTP/UI and populated/concurrency tests;
repair migration/format/coverage/tests-lint gates; then run the complete release
matrix, freeze migrations, and perform reproducible UAT plus recovery rehearsals.

## Remaining Work / Handoff (2026-09-06)

Current implementation snapshot: `c7a02b1`, pushed to `origin/IAM` at the user's
explicit request. This is a development checkpoint, NOT a production release.
The remote accepted the push with an author/committer email mismatch warning;
Git identity was left unchanged. Earlier no-commit/no-push notes are historical.
No full CI was rerun solely for this checkpoint commit.

### Remaining Work In Execution Order

1. [ ] Complete 10.8 populated-data acceptance across all seven PII owners.
   Seed real OAuth tokens/consents, federation links, SCIM mappings, accepted
   invitations and access-review profile copies. Prove target cleanup, unrelated
   principal/tenant preservation, immutable audit and late-failure rollback.
   Add live remote mapping, legacy NULL notification and disabled-owner cases.
2. [ ] Complete concurrent-writer coverage on PostgreSQL and real MySQL 8.4:
   both transaction orderings, in-flight notification delivery and SCIM/API
   attempts to restore a tombstone. Current old-snapshot notification rejection
   is verified, but is not exhaustive concurrency acceptance.
3. [ ] Reconcile remaining integration migration contract/version fixtures and
   verify migration-ledger checks through upgrade/down/up and disabled modules.
   Run the full real-MySQL migration gate, then regenerate and validate
   `apps/backend-api/tests/migration/schema.lock`. The current lock predates
   the seven owner-erasure migrations; do not claim generated schema consistency.
4. [ ] Implement platform lifecycle HTTP management and console UI: retention
   deadline, legal hold, lifecycle version, explicit irreversible confirmation,
   actionable blockers and tombstone state. Global platform authority only;
   no tenant-local permission may authorize global erasure. Regenerate OpenAPI
   and frontend types; add authorization and stale-version API tests.
5. [ ] Complete real desktop/mobile browser acceptance for retention, blockers,
   erasure and restore prevention using the actual composed backend. Reconcile
   the 10.8 plan checkbox only after all required behavior is accepted.
6. [ ] Complete 10.11 once the candidate is ready: resolve migration lint's
   existing unused `databaseColumn` at `tests/migration/iam_core_model_test.go:23`;
   run formatting/lint/coverage, race, three-engine initialization and permission
   matrix, real MySQL, generated-code/OpenAPI consistency, `make ci`, frontend
   checks, regression browsers and live readiness. Verify backup/restore and
   erasure reconciliation, archive recovery and rollback/roll-forward runbooks.
7. [ ] Provide a reproducible user acceptance environment with startup/stop/reset
   commands, runtime requirements, URLs, securely supplied test accounts and an
   acceptance checklist. Existing previews predate lifecycle and are NOT the
   final acceptance environment. Production deployment and mall stay out of scope.

### Verified Baseline / Next Action

Eight IAM module unit suites PASS; five composed erasure scenarios PASS;
owner migrations and downgrade guards PASS on SQLite/PostgreSQL/MySQL-compatible
and real MySQL 8.4; PostgreSQL/real MySQL reject pre-erasure snapshot notification
writes. Scoped app/owner lint PASS, migration-directory lint NOT PASS as above.
10.7 audit archive real-browser acceptance is complete. Detailed evidence follows.
Next implementation action: extend `internal/app/principallifecycle_test.go`
with populated owner records and negative cases, then proceed in the order above.

## Active Delivery Objective (2026-09-05, authoritative)

User requests completion of IAM for production delivery and user acceptance.
Mall is OUT OF SCOPE. Historical sections below are previous checkpoints, not
the current remaining-work list. No deployment to a live production system is
required to prove a release candidate; provide a reproducible acceptance environment.

### Current Phase / Execution

Current continuation checkpoint (2026-09-06, owner erasure integration):
- IMPLEMENTATION, NOT READY. Seven owner cleanup functions and three-dialect
  write-fencing migrations are implemented and wired through the real identity
  PrincipalAnonymizer. Authn notifications now persist principal ownership;
  NULL legacy ownership and in-flight delivery block erasure. Platform grants
  and live remote SCIM User mappings must be removed by their existing workflows.
- Latest owner migrations: core/authn13, oauth/organization9, federation/
  provisioning5, governance4; each erasure milestone depends on identity.lifecycle.
  Schema lock NOT updated: focused real-engine proof now passes, but the full
  migration gate and contract reconciliation still precede lock regeneration.
- Fixed core/OAuth fixtures to run actual identity migrations instead of minimal
  principal tables, completed required seed fields and migration contract checks.
  Scoped full suites PASS authn/OAuth/federation/governance/organization/
  provisioning/identity; core final full rerun PASS7.435s after correcting its last
  stale milestone-count assertion. Evidence: backend .temp/principal-erasure-unit-fixed.log
  and .temp/principal-erasure-core-final.log.
- New real-composition TestComposedPrincipalErasure PASS2.397s (four scenarios):
  successful cleanup, late governance failure rolling back credentials/membership,
  in-flight notification blocker and platform-access blocker. Tombstone writes
  rejected, old bearer denied, unrelated admin bearer works, erasure audit retained.
  Fifth unupgraded-owner negative included in final PASS2.132s. Scoped pinned
  app/eight-owner lint PASS; diff whitespace check PASS.
- TestIAMPrincipalErasureOwnerMigrations PASS8.824s on SQLite, PostgreSQL,
  in-process MySQL; same test real MySQL8.4 PASS1.945s. All seven owner migrations
  install, reject post-erasure notification writes, and refuse downgrade before
  removing guards. PostgreSQL/real MySQL additionally reject PII insertion from
  an already-open REPEATABLE READ pre-erasure snapshot. Compatibility driver has
  no non-default isolation support, so it does not claim concurrency coverage.
  Logs: .temp/principal-erasure-engines.log and -mysql8.log. Temporary MySQL
  instances and their volumes removed by label-validated helper; sessions terminal.
- README now documents cleanup boundaries, legacy notification remediation,
  remote deprovisioning prerequisite, restore reconciliation and no guard downgrade.
- No HTTP/UI for lifecycle yet. All-owner populated-data coverage, remote mapping
  negatives, both-order in-flight races, migration contracts/schema lock and final
  browser/release/recovery acceptance remain. No commits/pushes/agents/deployment.
- Migration-directory pinned lint FAIL only on pre-existing untouched unused
  databaseColumn in tests/migration/iam_core_model_test.go:23. New migration test
  has no reported finding; do not claim directory lint PASS. All sessions terminal.
- Next: extend populated owner/composed tests,
  update remaining migration contract fixtures and run real full migration gate
  before regenerating schema.lock and adding platform lifecycle API/UI. Existing
  preview/MinIO untouched; no new acceptance environment claimed.

Previous checkpoint:
Current continuation checkpoint (2026-09-06 03:44 America/Los_Angeles):
- IMPLEMENTATION, NOT READY. Reconciled10.7 plan checkbox with verified real
  browser evidence.10.8 identity-owned lifecycle foundation is implemented;
  cross-owner erasure is deliberately NOT wired or exposed through HTTP/UI yet.
  Goal remains incomplete; no commits/pushes/agents/production deployment.
- D21 records global platform authority, prior suspension, explicit retention
  deadline, legal hold, optimistic version and caller-owned atomic cleanup.
  New identity v3 lifecycle table and v4 immutable principal/lifecycle triggers;
  milestone identity.lifecycle=4, identity.base remains2. Principal row locking
  and current lifecycle reads avoid MySQL REPEATABLE READ stale decisions;
  retention uses existing shared database-clock expression.
- lifecycle.go implements GetPrincipalLifecycle, SetPrincipalRetention and
  AnonymizePrincipal orchestration. Missing PrincipalAnonymizer fails closed.
  The private service hook currently exists ONLY in unit tests, where test-owned
  credential deletion proves cleanup/audit rollback, not cross-module erasure.
  No application hook, management API, UI or new user-facing erasure claim.
- Database guards reject tombstone profile/status update, principal deletion,
  lifecycle reset/deletion. Migration down4 first performs an immutable-row
  guarded no-op UPDATE, refusing downgrade BEFORE any DDL if tombstones exist.
  Clean installations still downgrade/up. Generated query code regenerated,
  manual prefix table binding and ownership fixtures updated (68 IAM tables).
- PASS: identity full unit suite; scoped pinned lint; identity race90.3%,6.373s
  (.temp/identity-lifecycle-race.log, .temp/identity-lifecycle-cover.out).
  Focused three-engine/prefix upgrade,
  preservation, immutable writes and downgrade rejection18.741s; after PostgreSQL
  variable precision normalized to TIMESTAMPTZ(3), postgres-only17.955s PASS.
  Real MySQL8.4 full migration test PLUS lifecycle semantic test PASS29.301s
  (.temp/identity-lifecycle-mysql.log). Schema lock regenerated AFTER real
  migration evidence:171 files/68 tables/29 cross-module FKs. Architecture,
  autonomous ownership and composed tenant isolation tests PASS. No full CI,
  broad performance or browser rerun in this increment.
- Temporary MySQL fixture10e3bacfe72f07e200130e3bea992f40dabe66dcfc3669dd3c28e8e4cd7d2b24
  and its exclusive anonymous volume removed after label verification. Initial
  PowerShell helper failed on native stderr/SSH template quoting; reused same
  instance, corrected structured inspect parsing, no duplicate resource created.
  MinIO archive fixture and existing8083/8093 preview untouched. All test sessions
  terminal at this checkpoint; preview NOT upgraded to lifecycle source.
- Self-review: reused owned migration/query/transaction/audit/clock patterns,
  source outputs regenerated, scoped architecture/ownership/isolation verified,
  worktree preserved. Increment accepted only as an internal lifecycle foundation;
  original production-delivery acceptance remains NOT READY.
- Next: complete owner-scoped operational PII cleanup with in-flight writer
  fencing before composing PrincipalAnonymizer. In addition to prior inventory,
  governance AccessReviewItem has principal_name; authn notification_outbox has
  recipient/ciphertext but NO principal ID, so do not blindly delete by email or
  claim complete erasure. Platform administrative grant removal already exists
  in core/platform.go. SCIM live outbound mappings must not lose remote IDs
  without deprovision/reconciliation. Then platform HTTP/UI, composed browser
  acceptance, final combined10.11 release/recovery and reproducible user environment.

Previous checkpoint:
Current continuation checkpoint (2026-09-06 03:16 America/Los_Angeles):
- IMPLEMENTATION. Previous reply explained delays without implementation;
  this turn closes10.7 real browser acceptance and proceeds to10.8. Mall excluded;
  no commits, pushes, agents or production deployment. Avoid repeating full CI
  after every increment; run focused regressions until final candidate is ready.
- Real browser harness: TestArchiveBrowserSeed creates ONLY a new exclusive
  temporary SQLite file, seeds504 aged events across platform0/tenant91001/91002
  through actual Append with private test clock. No audit row mutation/triggers
  weakened. TestComposedArchiveBrowserAcceptance uses the existing full app
  fixture plus scoped config callback; actual worker15s, real MinIO behind a
  temporary outage proxy, dynamic isolated HTTP/Vite listeners.
- archive-live-browser-audit.mjs uses Node/Chrome, not API mocks. PASS35365,
  68.645s: outage then browser retry, manual second-segment queue, live polling,
  exact version/whole-file digest/NDJSON download, platform header omission,
  cross-tenant404 and ordinary-member tenant/platform403. Four completed jobs,
  all504 seed events preserved. Desktop/mobile tenant/platform screenshots
  inspected: .local/screenshots/archive-live/1788689685843/. Backend log
  .temp/archive-browser-live-current.log. Test backend/Vite/Chrome terminated
  and temporary DB removed by harness. Existing8083/8093 preview untouched.
- Initial harness attempts: Bun/Chrome pipe hung, terminated only confirmed
  child32932 tree; switched to existing Node convention and added child timeout.
  First Node run exposed test adapter assuming data.items; actual respx.List
  returns data array, fixed test decoding (production client already correct).
  Scoped app/audit lint PASS. No production runtime source edits in this increment.
- Prior true MySQL31055 handle was lost at interruption, but terminal process
  inspection and .temp/archive-multipart-mysql.log confirm all tests and final
  schema reconciliation completed (165 files/67 IAM tables/29 cross-module FKs).
  Earlier full CI77767 and full real audit race9992 remain current runtime evidence.
-10.7 now complete. Remaining10.8: irreversible principal anonymization/retention
  holds, credentials and linked PII cleanup, UI/API and restoration prevention;
  then10.11 final combined recovery/release gates and user acceptance.
-10.8 inspection: Principal is global, memberships hold copied profile fields;
  identity already owns cross-module credential/revocation workflows. Other PII
  is owned by authn (security state), federation (identity links), provisioning
  (SCIM identifiers), organization (invitations), core (member profiles). Do not
  implement name/email-only clearing or allow a tombstone to be reactivated.
  D17 authorizes mainstream lifecycle choices; preserve module ownership using
  existing narrow capability/composition conventions. Architecture not yet edited.
- Next concrete action: implement10.8 lifecycle state and transaction orchestration,
  owner-scoped PII cleanup and permanent restore/write guards, then targeted
  integration/browser proof. MinIO fixture from previous checkpoint remains for
  final combined acceptance; do not recreate it or touch other services.

Previous checkpoint:
Current continuation checkpoint (2026-09-06, multipart repair and verification):
- IMPLEMENTATION. Previous goal turn was a read-only status report, classified
  no progress. This continuation repairs the reproduced storage defect; overall
  production objective remains open, mall excluded. No commits, pushes, agents
  or production deployment.
- Reconciled uncheckpointed prior increment: archive_integration_test.go tests
  two independent pools, 16 concurrent requests/executors, durable recovery after
  object upload, stale completion fencing, tenant isolation and platform worker.
  Queue conflict fallback now clears the losing ID before winner lookup and uses
  FOR UPDATE for non-SQLite so MySQL REPEATABLE READ observes the winning insert.
  PostgreSQL/SQLite lifecycle PASS97.994s and real MySQL full gate PASS; logs
  .temp/archive-concurrency-engines-fixed.log and -mysql-fixed.log in backend.
- Real 70 MiB multipart replica test previously FAIL: two immutable versions
  published. Root cause confirmed in minio-go v7.2.1: high-level multipart paths
  reset PutObjectOptions and discard If-None-Match. archive.go now reuses SDK
  Core multipart APIs above16 MiB, streams parts without PII temp files, awaits
  producer digest/length before completion, and explicitly conditions completion.
  Retention belongs to initiation, not completion (real MinIO rejects it there).
  Canceled/failed uploads abort with independent10s cleanup; abrupt host/process
  loss requires bucket incomplete-multipart lifecycle cleanup documented in README.
- PASS: real replicas publish one version and leave no unfinished upload;
  cancellation after first part, producer failure/changed source publish nothing;
  lost completion response converges and retry reuses same version. The latter
  recovers in the same SDK call, so test expects success, not a forced error.
  Real HTTP download binds persisted version even after latest alias replacement.
  A1,003-event chain across3 objects (first2 multipart) verifies no missing or
  duplicate sequence, boundary hashes, completion counts and retained source rows.
- PASS: full audit race9992,27.105s,92.4% coverage including real storage tests;
  .temp/archive-multipart-race.log/.out. Pinned scoped lint PASS, diff-check PASS.
  Full make ci77767 PASS exit0, log .temp/archive-multipart-ci.log: overall90.2%,
  audit91.9% without real-store env, lifecycle80.544s, migration227.899s,
  performance48.397s, full lint/build/OpenAPI match. Runtime source unchanged
  after CI started. Current-source make test-mysql8 is running; inspect its
  returned tool handle and .temp/archive-multipart-mysql.log before claiming PASS.
  Schema/API contracts unchanged by this repair; no generated-output edits.
- Isolated MinIO reused and confirmed live: container
  cd9ec4f71f7401c7f2c5073e0b7c3dda07779a5870c6b0baab5f01b206a99a81,
  iam-archive-acceptance-20260906 at http://10.0.0.100:32820, private volume
  777662e9effe2cce2cce180eeab4675578f28d3558ac250a99f08ee0e6a6bd26.
  Transport .temp/iam-env.mk, credentials are existing audit test constants.
  Preview8083/8093 untouched and still previous CI binary, archive disabled.
- Next: finish current real MySQL gate, then reproducible isolated aged-audit fixture and
  REAL browser queue/worker/storage acceptance. Preserve append-only triggers and
  do not edit production data.10.7 remains open, then10.8 anonymization/tombstones,
  final recovery/release gates and user acceptance. Clean only validated isolated
  MinIO resources after storage/browser evidence is complete.

Previous checkpoint:
Current continuation checkpoint (2026-09-06 02:24 America/Los_Angeles):
- IMPLEMENTATION. Prior status-only goal turn classified as no progress. This
  turn implements archive management HTTP/UI and verifies actual platform archive
  execution. Overall production objective remains open; mall excluded; no commits,
  pushes, production deployment or agents.
- Recovered previous archive test handle28221: PASS8.254s. Added management tests
  for actual/pending counts, retry/database-clock leases, audit rollback, exact
  object verification/download, metadata corruption, tenancy/platform and guards.
  Worker includes tenant0; platform retention supported. Download supplies exact
  content length and verifies object retention against frozen job minimum.
- HTTP manifests expanded8->21; app frozen operations218->231. Read/export/manage
  guards are explicit, platform always platform/administer. I18n parity extended.
  Full audit race17189 PASS16.053s, coverage92.2%; pinned audit lint PASS. No live
  MinIO in this turn, so real storage tests are skipped, not newly verified.
- OpenAPI68969 regenerated; frontend schema regenerated. Client uses explicit
  tenant/platform scope; platform header omitted including download. API fixtures
  validate paths, pagination, conflicts and missing NDJSON completion. UI retains
  pinned tenant requests, keyed scope reset, polling, job details, retry/verify/
  download and platform navigation. Retention editor is not overwritten by polls.
- Frontend typecheck/lint/test/build PASS (IAM45 tests; shared UI tests cached).
  archive-ui-browser-audit.mjs runs MOCK API fixtures only: desktop/mobile twice
  PASS, final fixture run542ebf. Screenshots inspected (jobs and object dialog).
  Proves UI interaction/layout, NOT real storage/browser acceptance.
- First full CI10035 output reached all gates including migrations306.299s,
  build and OpenAPI match, but PowerShell redirection contaminated exit status.
  Correct final rerun34700 PASS exit0 via cmd /c: overall90.2%, audit92.2%,
  uncached migration307.188s, lifecycle86.631s, performance64.519s, lint/build/
  OpenAPI match. Log apps/backend-api/.temp/archive-management-ci-final.log.
  No backend runtime edits after final CI started. All tool sessions terminal.
- Preview8083/9093 upgraded using identical validated process args and --migrate;
  API PID27596, frontend8093 PID15524. Binary .local/scim-actor-acceptance/
  archive-preview.exe SHA256 matches final CI bin/7link-api. Health200.
  Inline launch twice hit OS access-denied before execution; after stopping old
  preview, .local/launch-archive-preview.ps1 successfully launched new process.
  backend-pid.json updated; other8082/8092 untouched. Secret file args unchanged,
  no passwords printed and ordinary preview still has archive execution disabled.
- Real authenticated browser smoke .local/archive-live-smoke.mjs PASS on both
  desktop/mobile (latest15287e): tenant and platform status/list200, disabled
  request503, no browser/API errors; screenshots inspected. This exposed platform
  guid0 serializing null (frontend types/fixtures corrected) and zero policy time
  showing year1 (now platform default). Navigation scope selection tested too.
  Preview http://127.0.0.1:8093/iam/audit-governance is NOT final user acceptance.
- Remaining10.7: multi-engine concurrent jobs/recovery, multipart/large-chain
  real storage checks and complete REAL browser acceptance. Then10.8 principal
  anonymization/tombstones, recovery/release/final user acceptance. Do not mark
  goal or10.7 complete on these management/UI-only results.
- Next concrete step: recreate an isolated MinIO fixture using validated transport
  in backend .temp/iam-env.mk; extend archive management to real immutable-version
  download and three-engine concurrent retry/lease recovery. Existing real-object
  tests are archive_test.go TestArchiveRealObjectLockAndRecovery and
  archive_worker_test.go real durable job test; IAM_ARCHIVE_MINIO_ENDPOINT opts in.
  Build a reproducible isolated aged-audit fixture for actual queue/worker/browser
  acceptance without editing production data or weakening append-only triggers.
- Self-review: export/S3/router/generation/UI patterns reused, original gates and
  assertions preserved, generated schema regenerated, docs/D20/plan aligned,
  git diff --check PASS. Original13 browser audits not rerun in this increment;
  scoped UI/live tests do not substitute for the final broad regression.

Previous checkpoint:
Current continuation checkpoint (2026-09-06 01:36 America/Los_Angeles):
- IMPLEMENTATION. Previous status-only turn was no implementation progress;
  this continuation implements actual 10.7 archive execution. Overall goal stays
  open; mall excluded; no commits, pushes, production deployment or agents.
- Added archive.go real streamed NDJSON object write/readback using existing
  PrepareExport/WriteExport and AnchorStore. Full-file SHA256/size, exact S3
  version and COMPLIANCE retention required before success; conditional creation,
  retry reuse, source revalidation, no PII temp files and no event deletion.
  AnchorStore now caches only successful initialization, recovering after outages.
- Added archive_jobs.go, model/query generation and three-engine v3 migration:
  durable queued/running/failed/complete, frozen incremental sequence segments
  (500 events), database-clock leases, fenced completion, bounded attempt duration,
  persisted exponential backoff and transactional lifecycle audit events.
  Added archive_worker.go module Start/Stop and iam_audit.archive app config;
  shares anchor S3 config without requiring anchor.enabled. HTTP/UI NOT added.
  Startup rejects missing v3 schema; persisted snapshots cannot change tenant,
  sequence range/generation time or add filters that silently omit segment rows.
- PASS: archive module full race including real MinIO tests (19235): 92.0%;
  real immutable-version readback, deletion refusal, restart and alias replacement;
  actual durable-job-to-MinIO completion. Focused three-engine/prefix migration
  and ownership tests 38570 PASS. Pinned audit lint PASS after assertion cleanup.
- Initial full CI 77148 failed architecture and the audit migration target fixture:
  authn/provisioning prior field-policy work omitted core.Name dependencies; the
  generic migration target still said v2. Declarations/tests and v3 target fixed.
  Integration tests now use -count=1 because source/subprocess architecture checks
  cannot rely on Go's test cache. No test assertions/thresholds were weakened.
- PASS: final make ci 69391, overall90.2%, audit91.8%, every module threshold;
  uncached architecture/lifecycle/migration/performance, migration242.859s,
  build and OpenAPI consistency. PASS: make generate82399, all71 generated Go
  hashes unchanged. Latest audit/authn/provisioning full race90781 PASS,
  14.187/42.328/10.671s, includes real MinIO tests on current runtime source.
- Real MySQL93639 initially passed execution but final lock check expected66;
  updated actual ownership/tool to67 and regenerated lock after that evidence.
  Normal no-update full gate31890 PASS. Explicit thresholds-enabled full gate
  71298 PASS: migration10.899s, performance31.072s, replay.265s, lifecycle5.589s;
  medians auth/session/SCIM/audit2.096/3.250/85.315/3.184ms below original limits.
  Final lock:165 migration files,67 IAM tables,29 cross-module foreign keys.
- Runtime preview 8083/8093 NOT rebuilt or migrated; previous 00:53 build remains.
  API PID27964/frontend PID15524 and both HTTP probes200 rechecked. Temporary
  dedicated MinIO container f518f6d992dcb0ab9f98fcebe68fe1fbff3a43cbea1e5bebcf49e464a4c85e08
  and its verified private anonymous data volume were removed after acceptance;
  Docker host10.0.0.100 has no running test containers. All tool sessions terminal.
  Recreate isolated MinIO and set IAM_ARCHIVE_MINIO_ENDPOINT for real archive tests.
- Self-review: reused export/S3/database-clock/module lifecycle; generated queries
  regenerated; append-only triggers untouched; scoped dependency declarations
  repaired; README/D20/backend plan updated; git diff --check PASS. No frontend
  runtime edits or new browser-gate claim. NOT READY for production acceptance.
- Next: add archive management HTTP/UI with correct view/export/manage guards,
  tenant reset, job status and actual archived/pending counts. Frontend surface:
  apps/iam/src/app/audit-governance/page.tsx. Complete platform tenant=0 archive
  handling (currently skipped), multi-engine concurrent job execution, multipart
  storage/recovery/large-chain acceptance and browser proof. 10.7 stays open.
  Then 10.8 anonymization/tombstones, final recovery/release and user acceptance.

Previous checkpoint:
Current continuation checkpoint (2026-09-06 00:53 America/Los_Angeles):
- IMPLEMENTATION, goal still open; mall excluded. 10.5 scoped implementation and
  acceptance now PASS: core policy/catalog/audit/revision, todo, identity/core
  profile APIs, authn session/JWT, OAuth userinfo, SCIM inbound and outbound, UI.
  Prior three-engine, true MySQL, race and performance evidence remains below;
  complete current browser regression now closes the previous acceptance gap.
- SCIM outbound UI complete: tenant-bound API/contracts, create/edit, actor
  preserve/rebind/clear, secret preservation/rotation, optimistic reload,
  User/Group push, remote removal, local configuration deletion. Real receiver
  audit repeatedly PASS, latest 49131 on CI backend: ordinary tenant admin, wire
  payloads/mapping reuse, actor projection, denied identifier -> 403/zero calls,
  disabled/unbound -> 409/zero calls, 502/retry, missing mapping/cross-tenant 404,
  tenant reset, desktop/mobile screenshots inspected.
- Original 13 browser audits ALL PASS in final full run 66849. Reused assertions;
  SCIM binds a real account, registration permits exact expected field-catalog
  GET 403 for a nonmember, invitation/review scripts wait for enabled buttons
  after async list refresh. audit:legacy isolates profiles/CDP and seeds the
  specified fixture tenant, leaves JS dialog handling to the legacy scripts.
- Two real release defects fixed with red-before/green-after regressions:
  pkg/timezone embeds time/tzdata so standalone -trimpath Windows binaries resolve
  IANA zones without Go installed; AppLayout memoizes Navigate state so lazy login
  loading does not cause a React update loop. audit:navigation PASS (84074) under
  250ms latency on desktop/mobile, including login return to protected deep link.
- PASS: full make ci 91728 (90.2% overall, all module thresholds, migrations
  212.738s, lint/build/OpenAPI); timezone/policyx race 100.0%/91.2%; make generate
  55596 preserves 70 generated Go hashes. Frontend typecheck/lint/test/build PASS,
  42 IAM tests (UI tests reused cache). Profile, SCIM actor, session tenant and
  field-policy browser audits also rerun PASS on final backend/frontend.
- Runtime restored to ordinary preview config: API 8083/9093 PID 27964 matches
  final CI binary hash, frontend 8093 PID 15524; both probes 200. Extended test
  registration/captcha bypass/notification/passkey options are OFF again; local
  notification receiver 8094 stopped, every test session terminal. Other services
  8082/8092 untouched. Preview http://127.0.0.1:8093/iam/scim-directories?view=targets
  is not final production acceptance. Password/tenant files unchanged.
- Next: actual 10.7 archive lifecycle. Read audit/governance.go, audit.go
  PrepareExport/WriteExport, anchor.go S3 object-lock client, module lifecycle.
  Reuse these and existing app jobs; design durable job/status, integrity-checked
  archive execution, retry/recovery and retention boundaries. D17 forbids hard
  deletion of audit events. Then 10.8 anonymization/tombstones, recovery/release
  and user acceptance. No commits, pushes, production deployment or agents.

Previous checkpoint:
Current continuation checkpoint (2026-09-06 00:11 America/Los_Angeles):
- IMPLEMENTATION, not production acceptance. Profile default composition and
  SCIM enforcement are integrated; all details at the 23:47 checkpoint below.
- Full make ci 11708 PASS: 90.2% overall; identity 90.8%, all registered module
  thresholds met; migrations 219.441s; lint/build/OpenAPI consistency PASS.
  Initial CI 26327 failed identity coverage 89.8%; real HTTP policy projection,
  denied/null writes, unavailable evaluator and scope tests fixed that gap.
  Latest full identity race PASS 5.394s. make generate PASS, 70 *.gen.go hashes
  unchanged. No runtime source changes after this CI; later edits are frontend,
  performance fixture/plan checks and documentation, requiring final gate rerun.
- Frontend typecheck/lint/test/build PASS; profile browser audit repeatedly PASS
  on actual newest backend, last isolated tenant 35991337975152640. Desktop and
  mobile masked/omitted email, capability-aware disabled input, partial PATCH,
  raw email preservation, direct null rejection, hidden verification and tenant
  reset proven. Final mobile editor/list screenshots inspected after fixing
  local dialog grid/gaps and table minimum width. No shared UI component edits.
  Current field-policy, SCIM-actor and session-tenant browser audits rerun PASS.
  Original 13 browser audits still NOT fully rerun; old scim-browser-audit.mjs
  must create/bind an actual service account before performing SCIM operations.
- Preview rebuilt/restarted ONLY dedicated API 8083/9093: PID 5648, binary hash
  matches CI bin/7link-api. Frontend 8093 remains live; 8082/8092 untouched.
  Password file unchanged. Console http://127.0.0.1:8093/iam/users is a preview,
  not a release candidate or final user acceptance.
- MySQL normal gate initially identified new bound-account validation SQL not
  in saved plans. Plans-only regeneration PASS (48030): migration 10.881s,
  performance 30.719s, replay 0.250s, composed lifecycle 5.149s. PostgreSQL plan
  guard initially assumed service account scope was tenant_id; it is actually
  owner_tenant_id. Added accurate scope + MySQL alias and positive/negative
  regression tests; missing/wrong filters still rejected. Focused tests/lint PASS.
  PostgreSQL plans regeneration PASS 41.279s; normal updates-OFF enforced rerun
  PASS 40.852s: medians auth/session/SCIM/audit = .305/.454/7.433/.354ms, original
  thresholds .8/2/8/1ms. Structured HEAD comparison proves all baseline workload,
  samples, thresholds and non-plan metadata unchanged.
- PASS: MySQL updates-OFF enforced full gate 60212: migration 10.822s,
  performance 32.127s (SCIM median 78.517ms < original 154ms), replay 0.224s,
  composed lifecycle 5.147s. Final make ci 16033 PASS after the test-only plan
  calibration and baseline changes, 90.2% overall, all module thresholds met,
  integration/build/OpenAPI consistency PASS. Latest frontend gates rerun PASS.
  All test/tool sessions are terminal. Preview probes both 200.
- Self-review: default composition tested without fixture registration; raw and
  public identity boundaries retained; automation fails closed; schemas generated;
  no policy bypass or weakened thresholds. Reused core evaluator, service-account
  validator, existing API factory and module lifecycle. git diff --check PASS.
  Final production acceptance NOT READY: remaining features and regression scope
  below are still required; no goal-completion claim.
- Next: adapt legacy scim-browser-audit.mjs to create a real service account and
  bind it through the directory editor, then run all original 13 browser audits
  on the current preview. Add SCIM outbound target
  management UI, 10.7 real archive, 10.8 anonymization/tombstones, recovery/release
  and final user acceptance. No commits, pushes, production deployment or agents.

Previous checkpoint:
Current continuation checkpoint (2026-09-05 23:47 America/Los_Angeles):
- IMPLEMENTATION. Latest goal resumed after read-only status report. Mall remains
  excluded; no commits, pushes, deployment or agents. Prior status-only turn was
  no implementation progress; this turn has implemented and verified integration.
- SCIM now enforces profile policies on Get/List/Create/PUT/PATCH/Bulk, filter
  inference and outbound users. Explicit nulls and aliases checked; raw PATCH
  snapshots preserved. Unbound inbound/outbound automation fails closed; config
  repair remains possible. Outbound uses the bound actor, not initiating admin.
- ProfileFields is NOW REGISTERED in DefaultFields. Removed isolated registration
  from app profile test. Old fixtures now inject the evaluator and bind real
  service accounts; no production policy bypass or weaker test assertions.
- PASS: provisioning full race 9.092s; composed SCIM profile SQLite plain/prefixed
  2.217s; all-engine/prefixed lifecycle 103.389s, now includes real SCIM profile
  role denies, projection, filters, null/PATCH/PUT/Bulk rejection, raw preservation,
  remote payload attribution, zero remote calls on denial and role revocation.
- PASS: full core/authn/oauth/provisioning/authz/app race 75.359/88.963/16.949/
  21.813/1.137/89.718s. Identity originally failed on missing fixture dependency;
  after fixing that, full identity race PASS 5.113s.
- OpenAPI and TypeScript regenerated. Frontend profiles/session/member fields
  now optional; contract check also prevents requiring server-optional fields.
  Principal editor uses effective capabilities, only submits changed fields,
  does not persist redacted placeholders, pins requests and resets on tenant
  changes. Security page no longer labels an invisible email as unverified.
  Frontend typecheck/lint/test/build PASS (41 IAM tests, 85 UI cached).
- Full make ci running session 26327 (gofmt/lint passed; coverage in progress).
  Existing preview still API 8083 PID 31208, frontend 8093 listener PID 15524;
  it has NOT been rebuilt/restarted. Older 8082/8092 untouched. No current browser
  proof yet for profile UI; add audit, then rebuild preview only after gates.
- Next: finish CI, profile browser audit and latest real-MySQL/performance gates.
  Update D17/D19/README/plan to remove obsolete unregistered/unbound notes.
  Outbound target UI, actual 10.7 archive, 10.8 anonymization, recovery/release
  gates and user acceptance remain open. Not a release candidate.

Previous checkpoint (superseded by 23:47 for profile/SCIM integration):
Current continuation checkpoint (2026-09-05 23:18 America/Los_Angeles):
- IMPLEMENTATION, not a release candidate. The preceding user status request
  was handled read-only; the active production-delivery goal then resumed.
  No commits, pushes, production deployment, mall edits or agents this turn.
- ProfileFields and identity/core public DTO projection now exist. Identity
  create/update checks explicitly submitted fields including null; member
  handlers project display/email. Lists reject hidden search fields and their
  fixed ordering fields (login_name for principals, display_name for members).
- Authn ProjectProfile uses the core evaluator through module.Services.
  Sessions without a selected tenant omit profile attributes; scoped cookie
  responses apply policy, bearer responses additionally honor token scopes.
  Raw Authenticate identity remains internal and unchanged by projection.
  Bearer profile reads refresh principal values from the writer before projection.
- Principal access/ID tokens honor policy plus profile/email scopes. ID token
  contract now takes the consented scope; OAuth passes row.Scope. Userinfo calls
  the current policy projection and uses optional response fields. Local JWT
  verification previously discarded Raw/scope, making real userinfo reject
  authorized tokens; it now retains the verified payload. Regression proved
  this with real signed tokens, not only OAuth fixtures.
- SCIM PatchUser no longer builds replacement from public GetUser. It reads
  raw identity inside its transaction, retains that version even without
  If-Match and shares replaceUserOn with PUT. Tests cover concurrent disjoint
  PATCH without ETags, stale versions and audit rollback. SCIM field projection
  and write guards themselves are still NOT implemented.
- PASS: full authn/oauth/authz race tests (47.625/9.830/1.110s); provisioning
  full race (9.598s); composed SQLite plain/prefixed profile HTTP test under
  race (4.717s). Covers real database policies, admin/member/session/userinfo,
  old-token policy changes, scope separation, null rejection, fixed-order and
  search restrictions, tenant isolation and membership revocation.
- PASS: pinned lint for authn/oauth/provisioning/authz; gofmt and diff check.
  Whole main-module compile-only go test ./... -run '^$' 15487 terminated
  successfully. All test/tool sessions from this increment are terminal.
  No full CI/current coverage/browser gate claim. Self-review checked raw/public
  identity boundaries, actual scope propagation and non-bypass of policy denies;
  final production acceptance remains open.
- IMPORTANT: ProfileFields remains deliberately UNREGISTERED in DefaultFields.
  The composed profile test registers fields only in its isolated fixture.
  Current profile-protected admin/member/scoped-session HTTP cannot work in
  the ordinary default composition until remaining consumers and registration
  are finished. Do NOT rebuild/restart the existing preview from this tree.
  OpenAPI/TypeScript and frontend optional-profile contracts are NOT yet
  regenerated for this increment. Prior 22:48 full gates predate these edits.
- Next: finish SCIM Get/List/Create/Replace/PATCH/filter/outbound profile guards
  using the explicitly bound service account; fail closed on unbound automation.
  Reuse the filter-parser AST and transactional raw patch flow. Only then
  register ProfileFields, update fixtures and OpenAPI/frontend contracts, and
  verify all consumers across engines and browser. Outbound UI, 10.7 archive,
  10.8 anonymization, recovery/release gates and user acceptance remain open.
- SCIM source notes: normalizeUserInput aliases displayName/name.formatted and
  falls back to userName. PATCH alias handling needs real persisted-value tests;
  current name.formatted replacement can be shadowed by retained displayName.
  Do not infer write authorization merely from a full replacement after PATCH:
  check explicitly affected attributes, preserve other raw values, and reject
  filters/sorts on masked fields. UserResource currently uses scalar userName
  and struct name; omission must be represented honestly when adding projection.

Previous verified checkpoint (2026-09-05 22:48, predates profile edits):
- D18 session tenant implemented for /authn/me and current session: explicit
  membership checked on writer; only a sole active membership can auto-select;
  ambiguous/empty remains unset. Signed token tenant cannot be overwritten with
  a header; cookie context does not mutate the global login session. Core-owned
  MemberTenantResolver is wired via module.Services. D18/README document semantics.
- PASS: full make ci 92727 (90.2% overall; migration 213.803s; lint/build/OpenAPI
  consistency), core/authn/app full race 80463 (90.1/90.2/89.6%, 63.685/69.213/
  61.187s), uncached three-engine/prefixed lifecycle 71837 (64.383s), including
  ordinary-member cookie login, multiple memberships, explicit selection, tenant
  suspension, revocation and bearer tenant mismatch. Core fixture corrected its
  duplicate principal seed; production behavior was not weakened.
- PASS: make generate 89172; all 70 *.gen.go hashes unchanged. OpenAPI/TypeScript
  regenerated; frontend typecheck/lint/test/build PASS (41 IAM, 85 UI cached).
- PASS: new audit:session-tenant twice, starting with empty browser storage and
  no tenant prefill, actual ordinary tenant administrator login, derived menu
  requests, desktop/mobile, ambiguity, preserved manual choice and revocation.
  Final screenshots inspected. SCIM actor browser audit rerun PASS on same build.
  Original 13 audits still NOT rerun against this tree.
- Preview restart recovered safely, no other environment stopped. API 8083 PID
  31208 uses unique bin/7link-api-scim-*.exe; hash matches latest CI bin/7link-api.
  Frontend 8093 remains live, both HTTP probes 200. Password/tenant files unchanged.
  .local/start-scim-actor-acceptance.ps1 -BackendOnly and restart script avoid
  overwriting a locked executable; existing 8082/8092 preserved.
- PASS: current-source real MySQL 24444, updates OFF and thresholds enforced:
  migrations 12.559s, performance 29.327s, replay 0.238s, composed lifecycle
  including the new session scenario 4.595s; schema lock unchanged (162/66/29).
  Uncached enforced PostgreSQL performance 84795 PASS (39.102s); medians
  authorization/session/SCIM/audit = 0.276/0.398/3.927/0.314ms, all below original
  thresholds. No thresholds or baseline samples changed in this continuation.
- Self-review: scoped IAM fix, core owns membership reads, raw login/token
  identity behavior preserved; HTTP tenant projection tested independently and
  in composed apps; no frontend runtime changes, commits, pushes or mall edits.
  git diff --check PASS (existing decisions LF normalization warning only).
  All test/generation tool sessions are terminal. Preview services remain running.
- Next: implement actual 10.5 profile consumers using the source map below,
  not more prerequisite-only work. 10.7/10.8/outbound UI,
  complete browser/production/recovery/release gates and user acceptance remain.

Next 10.5 source map: identity.Principal is shared by raw internal provisioning
reads, so protect public views without mutating that contract blindly. Core
member HTTP also emits display_name/email. Authn sessionTenant now supplies a
validated cookie response tenant but raw Authenticate still intentionally does
not choose a tenant. JWT issueSubjectToken/IssueTenantIDTokenWithAssurance put
preferred_username/email/email_verified directly into payloads; oauth userinfo
echoes these claims after checking only openid, so scope and field restrictions
must be enforced there too. Provisioning Get/ListUsers serializes raw records;
PatchUser currently builds a replacement from GetUser, which must become a raw,
version-protected internal read before masking is introduced. Outbound
buildRemoteResource calls identity.Get then builds UserResource, so it must use
the validated bound actor's fields, not the initiating administrator. Do not
publish profile catalog fields until all these consumers enforce them; NULL
automation bindings remain an explicitly unfinished fail-closed requirement.

Previous implementation checkpoints:

IMPLEMENTATION. Federation replay and 10.4 are verified at the 19:32 checkpoint
below. 10.5 now has policies, the todo consumer and a verified management UI;
IAM profile/alternate entrypoints remain. SCIM tenant/credential revalidation is
implemented and verified at 21:22 below, including full backend CI. Frontend
gates remain from 20:50; final production gates remain open.
Direct execution: policy contracts, persistence and consumers share
dependencies and are not suitable for concurrent edits.
Preserve the uncommitted changes; no commit or push has been made.

### Active Binding Checkpoint (2026-09-05 22:03, supersedes 21:22 for these changes)

- Implemented identity.ServiceAccountValidator via module.Services. It requires
  an active same-owner tenant, account/principal/member, no pending activation,
  no deletion and no expiry, using the caller's transaction.
- Provisioning directories/targets have optional versioned service_account_id;
  v4 additive migrations reference identity service accounts. Create/rebind
  validates the actor, retained SCIM contexts compare the current binding, and
  inbound/outbound operations reject invalid bound actors. Disabled/deleted old
  actors do not prevent configuration repair. Audit rollback and null clearing
  are tested. Outbound administrative rejection is 409, inbound SCIM is 401.
- IMPORTANT: nil bindings still preserve legacy operation as an intermediate
  prerequisite. No automatic actors/roles, no profile fields exposed in catalog.
  Full profile field enforcement and fail-closed unbound automation remain open.
- PASS: identity/provisioning/app race tests, coverage 90.5/90.0/89.6%; real composed
  HTTP create/rebind/stale-version/retained-context/spoofed-actor/status/expiry/
  activation/membership/deletion/repair/clear, no remote calls on rejection.
- PASS: three-engine plain/prefixed provisioning migration tests and identifier
  limits (16.390s), populated v3->v4->v3->v4, FK/CHECKs, dependency rollback.
  MySQL emulator driver fakeTransaction is no-op; transaction rollback assertions
  run on SQLite/PostgreSQL and actual MySQL, not this emulator.
- PASS: real MySQL gate (69283 exit 0): migrations 11.430s, performance under
  unchanged thresholds, replay concurrency, composed lifecycle including actors.
  Schema lock regenerated: 162 migrations, 66 IAM tables, 29 cross-module FKs.
- PASS: PostgreSQL plans-only performance regeneration (39.080s), SCIM 4.459ms
  < 8ms. Both engines preserve original benchmark samples and thresholds.
- PASS: OpenAPI export/types regenerated; frontend typecheck/lint/test/build
  (41 IAM tests, 85 UI tests cached). SCIM directory editor preserves/chooses/
  clears bindings, pins requests to tenant, and offers explicit 409 reload.
- PASS: new Node/Playwright scim-actor-browser-audit.mjs against fresh isolated
  SQLite service: binding create/preserve/rebind/clear, conflict reload, tenant
  switch, desktop/mobile. Final screenshots inspected after local dialog spacing
  fix. Original 13 browser audits not rerun. Frontend gates need final rerun after
  that last spacing edit.
- Full make ci (25589) FAILED in an existing databasemonitor timing test: probe
  call count became visible before snapshot publication. Focused 50x rerun passed;
  code inspection confirms the race. Test-only fix now waits for published second
  failure snapshot and stops the poller before asserting cached reads add no calls.
  That fix and full CI must be reverified; production monitor code is unchanged.
- Isolated preview: API http://127.0.0.1:8083 (PID 17804), frontend
  http://127.0.0.1:8093/iam/scim-directories (PID 33928). Tenant 35978959510306816,
  login scim-admin@example.test; password file .local/scim-actor-acceptance/audit-password.
  Startup .local/start-scim-actor-acceptance.ps1; binary bin/7link-api-scim-acceptance.exe.
  Existing 8082/8092 environment preserved. Preview is not final user acceptance.
- Next: verify monitor test fix and full CI; rerun lifecycle and normal performance
  serially, final frontend gates. Add any remaining actor boundary tests, then
  continue 10.5 actual profile consumers (admin/member/session/JWT/userinfo/SCIM/
  outbound) and outbound UI. 10.7, 10.8, recovery/release and final acceptance stay open.

Binding follow-up, 22:12:
- PASS: monitor test under race 100 times (7.165s); first complete CI after its
  fix passed (17958, 90.1% overall, migrations 211.380s). That run predates final
  actor audit changes and is not the final gate for the current tree.
- Added actual actor attribution to inbound SCIM audit; outbound audit retains
  initiating admin plus service_account_id, or uses actor for background work.
  New composed and service tests verify inbound create, outbound push/sync/remove,
  retained contexts, validator failure and zero remote calls on invalid actors.
- PASS: final provisioning/app race (91.1%/89.6%, 9.238s/43.874s); uncached complete
  three-engine/prefixed lifecycle including actor audit (71.724s); make generate
  preserved all 70 *.gen.go hashes; structured comparison confirms both engines'
  baseline workload/samples/thresholds/metadata match HEAD outside plans.
- PASS: frontend typecheck/lint/build rerun after final spacing changes; browser
  audit rerun and final desktop/mobile screenshots inspected, no overlap/overflow.
- Final make ci currently running as session 73219. After it terminates, run
  uncached PostgreSQL and real MySQL performance normally with updates OFF, serially.
- D19 records the binding prerequisite. Local 8083 preview binary predates the
  final audit-attribution-only change; do not describe that binary as the final
  verified backend or a release candidate. No commits, pushes or mall changes.

Next-stage source finding (22:17): browser-cookie Authenticate calls claims()
without a tenant, and authenticateBrowserSession never assigns OrganizationID
(authn/web.go). Both /authn/me and /authn/sessions/current echo that unset value
(authn/http.go). D18's frontend adoption only works if the session actually has
a tenant; the existing browser audits provide an explicit tenant. Resolve and
test the intended session tenant contract alongside profile field projection;
do not add a hardcoded tenant or treat current explicit-tenant audits as proof.

### Delivery Requirements and Progress

- [x] Reconcile HEAD f56cb43 with source, plans and existing dirty worktree.
- [x] D18 cookie session tenant projection and no-default-tenant browser flow,
  explicit membership/ambiguous choices/revocation, with three-engine and real
  MySQL composition plus ordinary tenant administrator desktop/mobile acceptance.
- [x] 10.9 shared federation replay claims: repository, all protocol callers,
  migrations, concurrent replicas, persistence, expiry and unavailable-store tests.
- [x] 10.4 tenant and entity data permissions enforced in todo repository for
  list/count/get/create/update/delete/export; tenant A/B and deny matrix.
- [x] 10.5 declarative field policy with read/write separation, deny precedence,
  server projection/masking and write rejection, management API/UI and revision.
- [ ] 10.7 audit archive execution, integrity checks, retries, retention boundaries
  and observable status, tested without deleting unverified audit data.
- [ ] 10.8 principal suspension -> anonymization -> retained tombstone, session
  and credential revocation, referential/audit integrity and authorized API/UI.
- [ ] Frontend workflows and contracts cover the resulting IAM capabilities;
  include tenant administrator and session-derived tenant browser acceptance.
- [ ] Final gates: generators stable, OpenAPI stable, make ci, race, PostgreSQL/
  MySQL/SQLite migrations and permissions, true MySQL, performance regression,
  frontend typecheck/lint/test/build and all real browser audits.
- [ ] Production configuration, install/upgrade/rollback and recovery procedures,
  key rotation and backup/restore validation, release/acceptance evidence and URL.
- [ ] Final independent review, requirement-by-requirement acceptance audit,
  then hand over the verified release candidate for user acceptance.

### Confirmed Decisions

10.5 implementation design (D17 refinement, 2026-09-05):
- Verified Keycloak user-profile.adoc (upstream docs) and RFC 7643 section 7.
  Keycloak distinguishes User/Admin contexts for view/edit; arbitrary local
  role allow/deny lists are this platform's RBAC adaptation, not a SCIM claim.
- Core owns one tenant/resource/field policy row with a versioned canonical
  JSON declaration: separate read/write defaults and role allow/deny lists,
  plus omit or constant text masking. Unknown/corrupt policy fails closed.
- Code-first field catalog fixes mutability/returned boundaries; policies cannot
  expose write-only credentials or mutate server-owned identifiers. Absent
  policies preserve existing declared-field access, never unknown fields.
- Reuse existing effective role sources (direct, temporary, static/dynamic groups,
  positions and active inherited entity bindings), transaction/audit/revision.
  No administrator bypass of explicit field denies. Policy administration has
  its own tenant permission and optimistic concurrency.
- Wire actual consumers incrementally: todo read/export projection and write
  rejection first, then IAM user-profile administrative/alternate entrypoints and
  management UI. Do not advertise fields before their consumers are enforced.
  Search/filter/sort on non-readable fields must reject to prevent inference.
- Sources: https://www.rfc-editor.org/rfc/rfc7643#section-7 and
  https://github.com/keycloak/keycloak/blob/main/docs/documentation/server_admin/topics/users/user-profile.adoc
  10.5 remains open until all intended consumers, UI and gates are verified.

D17/D18 remain authoritative. 10.3 is implemented; no longer waiting for approval.
10.10 IAM gRPC is closed by decision, not missing. Data permission reference is
todo. Use existing module ownership, query, authz, audit, jobs and storage APIs.
The user authorizes necessary IAM production fixes and tests across backend and
frontend; older line ownership notes do not prevent completing this request.
Do not commit/push or merge merely to record progress. Do not touch mall.

### Verification / Next Action

Checkpoint 2026-09-05 18:25 America/Los_Angeles, HEAD f56cb43 plus dirty tree:

- PASS: full `make ci` (coverage 90.1%; federation 91.4%; lifecycle 60.884s;
  migration 241.132s; build and OpenAPI consistency).
- PASS: federation unit tests and `go test -race ./internal/iam/federation
  -count=1`; clock failure after committed INSERT retains the claim, cleanup
  failure does not bypass uniqueness, GET/POST SLO validity regressions.
- PASS: `make test-mysql8-run`: true MySQL 8 migration, performance, and new
  32-request/two-connection-pool replay test. PostgreSQL/SQLite run that same
  replay test through lifecycle, plain and prefixed. The in-memory MySQL engine
  demonstrably allows multiple concurrent winners and explicitly documents no
  concurrency support in memory/session.go; its original lifecycle tests remain,
  but replay concurrency is required on real MySQL instead of that emulator.
- PASS: uncached `make test-performance` on PostgreSQL; all four medians below
  recorded thresholds (default gate enforces SQL fingerprints/plans).
- PASS: `make generate`; all 69 `*.gen.go` SHA-256 hashes unchanged; schema lock
  reconciles 156 migration files, 65 IAM tables, 27 cross-module foreign keys.
- PASS: frontend API types regenerated; workspace typecheck/lint/test/build.
  UI package used existing turbo cache; IAM tests reran: 39 pass, 0 fail.
- PASS: final deep-review perspectives (concurrency/security/distributed,
  SQL/version/reuse/performance, API/observability/naming), no remaining findings;
  parent reproduced SLO time defects before fixing. `git diff --check` clean.

10.9 uses database time after committed claim insertion and for cleanup, rounds
stored expiry upward to milliseconds, and scopes assertion/logout identifiers.
SLO rejects future IssueInstant beyond configured skew and honors NotOnOrAfter.
README limits rollback to v4-aware binaries and explicitly requires retiring
legacy SP entity IDs for old unbounded future SLO history; no false fixed-wait
promise. New-release backup recovery accounts for historical TTL/skew settings.

Deep-review A1/A3/A6: committed claim before side effects; interrupted flows
require fresh login, duplicates and storage failures cannot issue/revoke session.
A4: provider and tenant/issuer key scopes. A7: additive v4 migration and explicit
legacy/recovery restrictions. A2/A5/A8 not changed: no upload/batch entrypoint,
financial arithmetic or asynchronous job added; cleanup remains synchronous.
All frontend browser audits and complete production acceptance remain pending
until 10.4/10.5/10.7/10.8 are implemented. These green gates do not finish IAM.

10.4 prerequisite checkpoint, 2026-09-05 18:32 America/Los_Angeles:
core.AuthorizationEvaluator is now published through module.Services as a
stable Authorizer pointer, initialized during injection (not construction,
which must still work before the database is available/activation is decided).
Tests prove pre-injection fail-closed behavior, instance identity with the HTTP
service, actual self-scope constraints, immediate revocation and duplicate
provider rejection. Core/app full tests pass, core -race coverage 90.1%; the
architecture gate passed. The full CI above predates this small prerequisite;
do not describe it as a full CI run over later changes.

10.4 row-scoped gateway checkpoint, 2026-09-05 18:54 America/Los_Angeles:
- Implemented `Guard.FilterData` opt-in for typed `RegisterEntity` only, with
  startup rejection on tenant/platform/raw-adapter registrars, non-data actions
  and missing checker capability. OpenAPI declares `x-authz-data-filter: true`.
- `AuthorizationEvaluator` now embeds `authz.EntityDataPermissionChecker`.
  `Authorizer.EntityConstraint` reuses the existing revision-checked snapshot
  and constraint compiler, focused on one active tenant entity. Unknown/non-data/
  platform actions, missing/inactive entities and inactive memberships return
  empty scopes. Explicit entity denies clear all allow/owner/department scopes.
- `CheckEntity` semantics remain unchanged: self/department roles do not grant
  whole-entity access. `DataConstraint.AllowsEntityRows` only admits a possible
  row scope; it is NOT a substitute for repository filtering. ResourceIDs and
  DeniedIDs are documented as entity IDs; Ancestors is provenance only.
- PASS: authz/core tests; authz/core/app `-race -cover -count=1` (authz 95.4%,
  core 90.2%, app 89.2%). Added real module-wiring HTTP assertions
  proving self-scope 200, whole-entity 403, foreign/missing entity 403 and immediate
  revocation 403; subsequently reran these module and EntityConstraint tests
  under `-race -count=1` successfully. Architecture test passed uncached.
- PASS: full `make ci` over this checkpoint, including the prior replay and
  evaluator-provider changes (overall coverage 90.1%, all registered module
  thresholds pass; migrations 238.842s, build, OpenAPI consistency). Lifecycle
  and performance were cached in CI; lifecycle rerun explicitly without cache
  passed in 58.785s. True MySQL and uncached performance evidence remain from
  the replay checkpoint above, not new executions in this checkpoint.
- Self-review: reused snapshot/compiler, preserved ordinary entity permission
  semantics and existing edits, no generated schema changes or new production
  route. No todo source changes yet; 10.4 and production acceptance remain open.

10.4 todo implementation checkpoint, 2026-09-05 19:10 America/Los_Angeles:
- All nine task routes now use RegisterEntity/FilterData. Default catalog has
  todo_create/view/update/delete/export/import. Restore uses update; template
  uses import. Every service operation enforces its own permission, including
  post-update reads, list/count, export and all-or-nothing import.
- Added nullable tenant/entity/department model columns and forward v2 migrations
  for three dialects. Historical rows remain unassigned/invisible. Create/import
  derive tenant/entity/creator from trusted claims and snapshot the creator's
  active department. Core owns MemberDepartmentReader on MembershipChecker,
  queries writer and publishes via module.Services; no cross-module model import.
- Todo config/Enabler now follows authn activation in the app composition. Without
  IAM no task module/routes are activated. Existing no-auth migration/health tests
  updated from four flows to three and explicitly assert todo is absent.
- New SQL negative tests caught a single-child field.Or being hoisted into the
  outer GORM WHERE, bypassing tenant ANDs. Fixed by using a direct predicate for
  one scope and grouped OR only for multiple scopes. Plain/prefixed tests cover
  owner, department, union, all/entity, count/pagination, cross-tenant/entity
  writes, exports, legacy rows, deny, attribution and failures.
- PASS: todo full tests (coverage 90.8%), core full tests (90.2%), architecture,
  TestTodoDataPermissionsWithIAM (real composed API, roles/members/departments,
  tokens, self->department, deny and revocation). Updated external lifecycle to
  run the same task permission scenario. All source changes remain uncommitted.
- This historical run finished. Session 43953 is no longer live. Its PostgreSQL
  duplicate updated_at failure was fixed by disabling automatic GORM timestamps
  on the todo model; the service owns the timestamp assignments.

10.4 verification checkpoint, 2026-09-05 19:32 America/Los_Angeles:
- Reproduced the remaining app failure: TestBootstrapAddsEnabledHTTPModules still
  expected todo with IAM disabled. Updated that exact module list; app/core/todo
  full tests now pass. Expanded IAM task scenario itself was already passing.
- Fixed migration test ordering without weakening the empty-database assertion.
  MySQL v2 now adds columns and named CHECK constraints separately and drops the
  checks before dropping columns. Populated legacy-table upgrade, positive-ID
  checks (0/-1 rejected), down/up and plain/prefixed tests pass on all engines.
- Expanded composed HTTP scenario: successful owner update/delete/restore with
  persisted versions and actor, department descendants, entity inheritance/deny,
  multipart import that cannot spoof creator, and immediate import revocation.
  Reused the existing fixture with raw io.Reader body support.
- PASS: uncached tests/lifecycle (64.368s), PostgreSQL/MySQL emulator/SQLite,
  plain and prefixed. Actual SQL writes include the expanded todo scenario.
- PASS: make test-mysql8-run: actual MySQL 8.4 populated-table upgrade/down/up,
  lifecycle with tasks_ prefix, replay concurrency and performance. Four medians
  remain below the recorded thresholds; temporary gate container was cleaned up.
- PASS: app/todo/core/authz -race -cover -count=1; todo 90.8%, core 90.2%,
  authz 95.4%, app 89.2%. make generate preserved all 69 *.gen.go hashes.
- PASS: OpenAPI and frontend API types regenerated; frontend workspace typecheck,
  lint, test and build. UI package reused cache; IAM tests reran (39 pass, 0 fail).
- README now documents task activation, trusted attribution, historical rows,
  and rollback limits. git diff --check clean; source changes remain uncommitted.
- PASS: full make ci (session 93975 exited 0): format, lint, coverage (90.1%),
  architecture, integration migrations (220.139s), build and OpenAPI consistency.
  Lifecycle/performance were cached inside CI; both were also rerun uncached in
  this checkpoint. PostgreSQL performance passed in 43.544s; four medians below
  recorded thresholds (default gate enforces fingerprints/plans).
- Scoped self-review/acceptance: 10.4 PASS. Kept ordinary CheckEntity semantics;
  opt-in gateway is not the row enforcement boundary; repository always filters.
  Trusted creation/import attribution, legacy NULL invisibility, owner/department/
  descendants/entity inheritance/deny, count/export and actual writes covered.
  No schema v1 rewrite, no unauthenticated fallback, no commits/pushes or mall work.

10.5 implementation checkpoint, 2026-09-05 20:19 America/Los_Angeles:
- Policy catalog, strict canonical read/write/role rules, omit/constant redaction,
  same-tenant management API, revision-aware evaluator and v12 DDL are present.
  Field versions use the locked tenant revision, preventing delete/recreate ABA;
  overflow is rejected. Tests cover audit rollback and concurrent CAS winners.
- Todo now enforces write policies in Create/Update/ImportAll, records actual
  submitted JSON keys (including null/default) and supplied spreadsheet headers,
  filters list/export SQL projection and query capabilities, rejects search/sort/
  filter inference, masks all single-item responses and exports, and restricts
  templates to create-writable fields. pkg/query.Readable copies the contract.
- Real composed IAM API tests prove entity role inheritance, deny precedence,
  immediate revision effect, tenant isolation, no administrator bypass and write
  rejection without side effects. API and core frozen routes now include five
  explicit Phase 10 additions (218 mappings, 56 core routes).
- Fixed tenant registrar passing token tenant instead of verified request tenant:
  copies claims, sets verified tenant, clears unverified entity; tests prove the
  original token claims remain unchanged. Entity/platform semantics preserved.
- Reproduced actual upgrade failure on existing OpenAPI DB. Keep iam.base at 11
  and add field_policies milestone at 12; raising base would invalidate already
  applied organization.constraints. Composed upgrade regression and OpenAPI export
  now pass on the existing populated environment without resetting it.
- Catalog refinement: id is always returned; version is nonconfigurable but uses
  default returned semantics to preserve existing sparse selection and ETag API.
- PASS: todo/query tests (91.3%/96.0%), composed app tests, uncached three-engine
  lifecycle (62.403s), field DDL/ownership tests (27.042s), frontend type generation
  and typecheck. First real MySQL run passed migrations/lifecycle/replay/performance
  but static gate caught missing PostgreSQL TIMESTAMPTZ(3); corrected and rerunning.
- Subsequent terminal results: race/full app/todo/core/authz PASS (89.6/91.3/
  90.1/95.1%); lint PASS; second true-MySQL gate PASS; three-engine DDL PASS
  (15.901s). Generated hashes stable (70 files), schema lock reconciled to
  159 migrations, 66 IAM tables, 27 cross-module FKs. These sessions are closed.
- Initial full CI over 10.5 failed stale core milestone assertions in governance,
  organization and provisioning migration tests; no production migration failure.

10.5 continuation checkpoint, 2026-09-05 20:36 America/Los_Angeles:
- Corrected only the three latest-core-milestone assertions to field_policies;
  identity base and all rollback dependency/constraint checks remain unchanged.
- PASS: uncached affected three-engine migration tests (44.602s).
- PASS: full make ci, session 88320 exited 0: format/lint, overall coverage
  90.2%, all registered module thresholds, architecture, migrations 207.909s,
  build and OpenAPI consistency. Lifecycle/performance reused cache; uncached
  10.5 lifecycle and actual MySQL evidence remain in the earlier checkpoint.
- The frontend work started here is verified at the next checkpoint.

10.5 management UI checkpoint, 2026-09-05 20:50 America/Los_Angeles:
- Added /iam/field-policies with catalog/resource search, separate read/write
  defaults, role allow/deny matrix, omit/redact modes, immutable/read-only bounds,
  versioned save/reset, explicit 409 reload, request errors and tenant-pinned
  editors. Sidebar access is proven by the protected field catalog, scoped to
  tenant and session subject; a tenant switch discards the previous editor.
- Real browser testing caught version-zero default policies being treated as
  stored overrides; fixed and covered by the reset-button regression. Screenshot
  inspection caught inline checkbox sizing and mobile column wrapping; fixed
  locally without changing shared components, and checked usable dimensions.
- PASS: frontend workspace typecheck/lint/test/build after final source changes.
  IAM 41 tests passed; ui 85 tests reused cache. api-contract now covers new
  field declarations, policies, constraints and policy write-body field names.
- PASS: scripts/field-policy-browser-audit.mjs, Node + pinned Playwright/Chrome,
  real HTTP and SQLite: UI policy persistence; allow+deny (no admin bypass);
  actual todo masking/write rejection; competing 409 and reset; 1440x1000 and
  390x844 screenshots; read-only fields; tenant switch; non-platform read-only
  viewer denied writes/cross-tenant reads; update grant without role_view permits
  role-ID editing; revoked catalog permission clears menu/data. No page errors.
  This is a new repeatable audit, not a rerun of all 13 older browser audits.
- Local isolated environment runs detached: API http://127.0.0.1:8082 (PID 11032),
  console http://127.0.0.1:8092 (bun parent 33968). API /healthz returned SERVING
  HTTP 200. Revalidate ports/PIDs before reuse; never assume these are still live.
  Tenant 35971203218800640, login field-admin@example.test; password file
  .local/field-policy-acceptance/audit-password (never printed). SQLite and generated
  keys/logs/PID records are in that ignored directory. Local start script:
  .local/start-field-policy-acceptance.ps1; it refuses occupied ports.
  Screenshots: .local/screenshots/field-policies/. No live tool command sessions.
- Scoped self-review PASS: no mall changes, commits, pushes or production writes;
  reused API/controls/bootstrap/secret-file helper and backend authorization;
  new dev-only browser dependency supports actual repeatable UI regression.
  Overall production acceptance remains NOT READY.

SCIM security verification checkpoint, 2026-09-05 21:22 America/Los_Angeles:
- Reproduced inactive-tenant bearer acceptance before the fix. Authentication
  now joins active tenant/directory and live, unexpired credential; all resource
  service methods revalidate the tenant/directory/credential tuple, with writes
  checked inside their transactions. Resolved IDs constrain SQL explicitly.
- Added composed HTTP tenant create -> SCIM user/group -> suspend -> resume ->
  delete regression: 13 resource/bulk operations reject suspended/deleted tenants
  with SCIM 401 and no audit/row-count/mapping-version changes. Reads recover
  unchanged on resumption. Shared scenario runs in external lifecycle engines.
- PASS: provisioning/app race+coverage (91.0%/89.6%, 9.539s/35.321s); focused
  composed and service tests. Final uncached three-engine/prefixed lifecycle PASS
  (52.877s), including the explicit SQL scope predicates and new SCIM scenario.
- Fixed performance fixture's fabricated credential ID: create/authenticate a
  real credential and assert all 25 missing-user operations return 404, not just
  the bulk response length. A real MySQL gate reproduced the old fixture failure.
- Reproduced plan checker accepting an unfiltered PostgreSQL scan because
  `Plan Width` contains `id`. It now reads actual Filter/attached_condition fields,
  matches whole column names, and resolves known SCIM aliases reported by MySQL.
- Performance generator has IAM_PERFORMANCE_UPDATE=plans: validate new plans and
  enforce original latency thresholds, regenerate plans only. Both engines now
  capture 13 queries instead of 12. Structured comparison against HEAD proves
  original benchmark samples AND thresholds are unchanged for both engines.
- PASS: PostgreSQL plan regeneration under enforced thresholds (SCIM median
  4.363ms < 8ms); true MySQL plan regeneration (SCIM 58.529ms < 154ms), migrations,
  replay concurrency, expanded HTTP lifecycle and schema lock (159/66/27).
- One PostgreSQL enforced run FAILED while race/CI were running; unchanged audit
  append also exceeded threshold. Final serial run with updates OFF and thresholds
  ON PASS (36.920s): medians auth 0.241ms < 0.8ms, session 0.374ms < 2ms,
  SCIM 3.762ms < 8ms, audit 0.302ms < 1ms; every sample passes original limits.
  Run same-machine latency gates alone, not concurrently with race/CI workloads.
- PASS: final true MySQL with updates OFF and thresholds ON (session 67202 exit 0):
  medians auth 2.080ms < 7ms, session 3.210ms < 10ms, SCIM 68.062ms < 154ms,
  audit 3.255ms < 11ms; migrations, replay concurrency and new lifecycle PASS.
  Generated plan review: PostgreSQL small-table scans all carry explicit ID
  predicates; MySQL credential/directory/tenant accesses are const primary keys.
- PASS: full make ci (65720 exit 0): formatting/lint, 90.2% overall coverage,
  all module thresholds, architecture, migrations 206.478s, build, OpenAPI check.
  Lifecycle/performance were cached in CI and independently rerun uncached above.
- All tool sessions in this checkpoint are terminal. git diff --check clean;
  benchmark samples/thresholds verified unchanged against HEAD for both engines.
- README documents SCIM lifecycle and plan-only regeneration. No frontend/schema
  production changes in this checkpoint; no commit, push, deployment or mall work.

- Scoped self-review: reused repository/transactions and composed lifecycle;
  explicit scoped SQL and no security/performance gate bypass. SCIM security
  regression acceptance PASS; overall IAM production acceptance remains NOT READY.

Next concrete action: wire IAM profile and alternate read/write consumers;
this SCIM fix does not complete field-policy task 10.5. Resolve the real automation
actor using existing service-account/ownership contracts before exposing profile
fields in the catalog; no actor binding or new field semantics were added here.
Identity Principal is shared with provisioning; do not mask authentication's
internal identity records. Cover identity admin, core member views, authn session/
me, OAuth userinfo, SCIM and outbound provisioning deliberately. SCIM authenticates
a directory credential, not a human principal; do not grant a blanket automation
bypass or fabricate a principal ID to satisfy FieldConstraint. Resolve its field
authorization context using the existing directory/tenant ownership contracts.
Do not mark 10.5 complete at the todo or management UI milestone.
10.7, 10.8, browser acceptance and production recovery/environment handoff remain
unimplemented or unverified; overall production acceptance is NOT READY.
Implementation constraints:
- DataConstraint.ResourceIDs and DeniedIDs are entity IDs, not todo IDs.
  Ancestors are descriptive; expanded ResourceIDs already reflect descendants
  and explicit denies. Never independently expand Ancestors to grant access.
- RegisterEntity calls CheckEntity, which requires whole-entity
  permission by default. The new FilterData opt-in calls EntityConstraint, so
  row scopes can pass only that explicit entrypoint. Preserve this distinction
  in the business repository; todo is now the production caller.
- Never retain an unauthenticated fallback when IAM is disabled. Attribution
  fields are not client-writable, including spreadsheet imports. A member moving
  department does not silently move existing task department attribution.

---

## Historical Backend Checkpoint (superseded by active delivery objective above)

## Task
IAM Phase 10 后端推进。10.1、10.2、10.6、§2.5.2 均已推送。当前：10.9 密钥轮换（第 ① 步已推送），
以及 @gankcode 拍板的注册验证码可配置化（频道 task #5，已推送）。

## Objective
10.9：现在 federation / provisioning / authn 各自拿一把密钥，密文信封里不记录是哪把密钥封的，
所以换密钥等于让库里所有密文永久打不开——「安全的动作」和「做得到的动作」是两个动作。
目标是让密钥可以被替换：一把在封，一圈都能开，旧密钥在没有东西依赖它之后才离开配置。

验证码：注册页以前在文字 / 滑块 / 点选里随机三选一，其中两种不是所有人都能完成；
另外自动化验收脚本没有任何办法通过注册流程。

## Current Phase
IN PROGRESS（10.9 第 ① 步与验证码均已推送；10.3、10.4 等 @gankcode 拍板）

## Current Step
10.3 已完成（三条 guard 改对 + `authz.Register` 补上反向 scope 检查）。
下一步：10.9 ⑤ federation replay registry 换跨副本共享状态；随后按 D17 依次做 10.4 → 10.5。

**推送被我主动挂起**：@ClaudeWEB 在同一条本地 `IAM` 分支上有 6 个提交（`32025c4`..`9ae8e45`）
压在我这条下面，他明说「都没推」。推我的就等于替他推他的，已在频道问过；未回话前不要 push。

## Overall Progress
- [x] Phase 10.2（提交 24f2483）
- [x] Phase 10.1（提交 d253c5e）：`iam bootstrap-platform-admin` + `iam reset-admin-password`
- [x] 品牌 cookie 清理（提交 348a37f）
- [x] §2.5.2 存量零成员租户修复命令（提交 20bae43）
- [x] CLI flag 下划线/中划线在所有子命令一致（提交 5d7e9d0，@ClaudeWEB 报的第 4 条）
- [x] `allowed_return_urls` 改为 origin 精确 + 路径前缀（提交 1ecc9cb，@ClaudeWEB 报的第 3 条）
- [x] create/update 可选外键 schema 对称 + invitation 空值线上形状（提交 69187ab）
- [x] Phase 10.6 OAuth 同意自助列出与撤销（提交 0b96fb9）
- [x] 10.9 ① 密钥环原语 `pkg/extension/keyringx`（提交 51ac8f4）
- [x] 10.9 ② federation / provisioning / authn 接入密钥环 + `previous_*_keys` 配置
- [x] 10.9 ③ `iam rewrap-secrets` + 「还有多少行挂在旧密钥上」报告
- [x] `7link-api openapi` 离线导出 + `make openapi` / `make openapi-check` + 落地 `docs/openapi.json`
- [x] 10.9 ④ authn 签名密钥环：JWKS 同时发布 active + previous，签用 active，验收任一把
- [ ] 10.9 ⑤ federation replay registry 换成跨副本共享状态
- [x] 注册验证码可配置（频道 task #5）
- [x] **10.10 IAM gRPC：决定不做，本项关闭（D17）**
- [ ] 10.3 principal 管理权限语义（**不再等待**，按 D17 双平面方案做）
- [ ] 10.4 数据权限端到端（**不再等待**，按 D17 以 `internal/todo` 为参照实现；用户已确认用 todo）
- [ ] 10.5 字段权限（**不再等待**，按 D17 对标 Keycloak User Profile + SCIM `mutability`/`returned`）
- [ ] 10.7 audit archive / 10.8 principal 生命周期（按 D17 SCIM+GDPR 停用→匿名化→墓碑）/ 10.11

## 本轮交付其一：10.9 ① 密钥环原语（已推送 51ac8f4）
新包 `pkg/extension/keyringx`。一把 active 负责封，整圈都能开；
`NeedsRewrap` 回答「这个值还挂在旧密钥上吗」，退役旧密钥不再靠猜。

- 新信封 `v2.<keyid>.<b64url>`；库里已有的 `v1.<b64url>` 靠逐把试解兼容——
  AES-GCM 会认证，错的密钥只会失败，不会解出错的明文，所以试解是明确的。
- 派生逐字节保持 `HMAC-SHA256(material, domain+purpose)` 不变，否则现存密文全部作废。
- 配置密钥接受四种 base64 拼法（RawStd / Std / RawURL / URL），与被它替换的三处解析器一致，
  不能让任何一个部署的密钥「因为软件变了」而突然读不出来。
- 密钥标识是 `HMAC(material, "keyringx:key-id\0"+domain)` 截断 8 字节，按 domain 分离，
  不是密钥本身的摘要——信封里明文携带的东西不该是那个秘密的短哈希。
- `ErrUnknownKey` 与 `ErrOpen` 分开：前者的修法是把密钥放回配置，不是承认数据没了。
- 配置报错只说密钥的位置（`previous key 2`），绝不说值；有测试断言错误串里不含密钥材料。
- 覆盖率 95.2%，未覆盖的都是 32 字节 HMAC 输出上不可能失败的防御分支。

## 本轮交付其二：注册验证码（频道 task #5，已推送）
承接 @gankcode 在 `#sevenlink-platform-win:e8731271` 的拍板：按市面通行做法，
再加一个 Turnstile 测试密钥式的机制。

配置（`authn.registration.captcha`）：
- `kinds: []` — 空 = 只发文字验证码（新默认）。可填 text / slide / click。
- `test_answer: ""` — 空 = 关闭。填了之后这个字符串可以作为任何验证码的答案，至少 8 字符。

改动文件：
- `pkg/extension/authn/verifier.go`：`RegistrationConfig.Captcha`、新增 `RegistrationCaptchaConfig`。
- `internal/iam/authn/captcha.go`：`captchaOffering`、`parseCaptchaOffering`、`(*Service).IssueCaptcha`、
  `(*Service).VerifyCaptcha`。
- `internal/iam/authn/gocaptcha.go`：`issueCaptcha()` → `issueCaptchaOf(kind)`，
  `VerifyCaptcha` → 不导出的 `verifyCaptchaOf(kind, id, answer)`。
- `internal/iam/authn/web.go`：`Service.captcha`，在 `NewWebService` 里解析（在 enabled 判断之前）。
- `internal/iam/authn/http.go`：两个调用点改成走 Service。

## 本轮交付其三：10.9 ② 三个模块接入密钥环
federation（client-secret / state / saml-key）、provisioning（scim-target-bearer）、
authn（mfa 加密 + 四个 HMAC 用途）全部改成走 `keyringx`，
并各自新增 `previous_encryption_keys` / `previous_encryption_key_files`。

- **纯增量**：派生逐字节没动，旧的 `v1.` 信封照常打开，旧的 HMAC 摘要照常对上。
  三个模块各有一个测试手写出「上一版代码会写成什么样」，派生一旦被改动就是红的，
  而不是等到某个部署发现库里的东西全打不开。
- **`secretx.ResolveList`**：`previous_*_keys` 与 `previous_*_key_files` 二选一，
  单个报错点名到 `name_files[2]`，`Resolve` 的老报错串一个字没变。
- **按摘要的用途分两类 API**，这是 authn 那边形状的由来：
  恢复码 / 找回密码 token / 邮箱验证 token 都是**按摘要查行**的，查询不可能对密钥不可知，
  所以用 `MACs` 出候选集 + SQL `IN`；邀请 token 是先按 ID 查到行再比对，用 `MACMatches`。
  为此跨模块契约 `InvitationCredentials` 从「给我摘要」改成「这个 token 对得上吗」。
- **五个 MFA 用途一起覆盖，不做半套**：半套更糟——运维换了密钥，看到登录正常，
  几周后才发现恢复码全废，而恢复码正是丢了验证器之后唯一的退路。
- **`ErrUnknownKey` 单独一条错误**，三个模块各自给了一句不同的话：
  修法是把退役密钥放回配置，不是让人承认数据没了，也不是叫用户重新录入他拿不出来的东西。
- **provisioning 只配 previous 不配 active 直接拒**：那是一次做了一半的轮换，
  静悄悄地把出站 SCIM 关掉不是那个人想干的事。完全不配密钥仍然合法（= 不用出站 SCIM）。
- **keyringx 的报错不再提 domain**：`"federation:key:"` 是派生标签，不是任何一个配置项的名字，
  最底层只说哪里不对（`active key must decode to 32 bytes`），由调用方补上是哪一项。

## 本轮交付其四：10.9 ③ `iam rewrap-secrets`
一条命令回答运维在轮换密钥时唯一关心的问题：旧密钥现在可以拿掉了吗。

- **七个密文列全覆盖**：authn 四（totp_secret / mfa_enrollments.secret_ciphertext /
  passkeys.credential_ciphertext / notification_outbox.payload_ciphertext）、
  federation 两（client_secret_ciphertext / saml_idp_keys.key_ciphertext）、
  provisioning 一（scim_targets.bearer_token_ciphertext）。
  每一列都得单独报名：一个静静没跑的列和一个真的没东西可换的列，在输出里长得一模一样。
- **`--dry-run` 是标志位而不是另一条命令**：两种跑法必须是同一条代码路径，
  否则预演的结果就不能当作真跑会发生什么的依据。报告首行始终写 `mode:`，
  免得一份预演输出贴进工单后被当成「已经换完了」的证据。
- **摆不平的那一类单独报**：恢复码 / 找回密码 token / 邮箱验证 token 存的是摘要不是密文，
  重封没用，只能等过期或让人重新生成。**恢复码永不过期**，
  所以 `clear_after` 在这种情况下写 `never` 而不是一个日期——
  一个看起来像「等到这天就能删密钥」的日期，比没有日期危险得多。
- **三个模块一个事务**：任何一处失败，所有行原样不动，不会出现跨模块半转。
  另取跨进程锁：两个进程同时跑不会弄坏数据（写入按读到的密文匹配），
  但会得出两份互相少算的报告，而这个决定只能有一份依据。
- **三个 `SecretRewrapper` 而不是一个共享接口**：`module.Services` 按类型为键，
  同名会让最后注册的那个静静覆盖前面的，报告少了一个模块也不会说一声。
  三个新接口已登记进 `internal/iam/core` 的三份冻结清单。
- **顺手修掉一个真实缺陷**：`countDerived` 用 `SELECT MAX(expires_at)` 统计到期时间，
  聚合表达式没有列类型，SQLite 按文本返回，扫描直接失败；PostgreSQL 上完全正常。
  改成取最晚一行而不是聚合值。这个报告偏偏只在轮换密钥的当口才会被跑到。

## 本轮交付其五：`7link-api openapi` 与落地的 API 文档

前端的 `generate-api-types.mjs` 默认读 `apps/backend-api/docs/openapi.json`，而这个文件从来没有存在过——
spec 以前只有跑起来的服务在 `/openapi.json` 上供，所以脚本里写着「后端还没发布，先指向一个跑着的实例」。
人可以这么干，CI 不行。

- `internal/app/openapi.go`：`RunOpenAPIExport` 按配置组合出真正的应用（不是「所有模块全开」的超集，
  否则文档会描述这个部署实际返回 404 的接口），注册 HTTP 之后取 `api.OpenAPI()`，全程不监听端口。
- `info.version` 取配置里的版本，取不到就退到常量 `0.0.0`，**不取构建号**：构建号每次提交都变，
  落地的文档就永远对不上新导出的，真正的 API 变更会被淹在噪音里。
- 日志改指 stderr（新增 `processConsoleErrorWriter`）。原来 `configureProcessRuntime` 把 slog 指向 stdout，
  `openapi > spec.json` 会把日志写进文档中间，产出一个谁都解析不了的文件——这是真 bug，不是洁癖。
- `--format json|yaml` 显式指定，不按扩展名猜；认不出的格式直接拒绝，不默默当成 JSON。
- `--out` 走临时文件 + rename：中断的导出不能留下半个文档，否则下一步 diff 出来的是「命令被打断」而不是「API 变了」。
- `make openapi` 重新生成，`make openapi-check` 在文档与代码不一致时失败并给出修法，已接进 `make ci`。
  应用名默认中性的 `IAM`（文档标题是名字 + " API"），可用 `OPENAPI_NAME` 覆盖；导出用的密钥每次现生成在
  `.temp/` 里，不进仓库，也不出现在文档里。
- 落地文档：152 条路径 / 227 个操作 / 294 个 schema，约 1.0 MB；全文搜不到任何品牌字样。

## Important Decisions
- 跨模块只走窄事务能力（`func(ctx, *gorm.DB, ...)`，Provide 阶段发布、Inject 阶段解析）。
- 修复命令是 create-only：只要租户已有任何 member 或 role 就拒绝，绝不改动已有归属。
- 密码永不作为命令行参数（10.1 已确立）。
- 退出码按运维下一步动作区分：3 已 bootstrap / 4 冲突 / 5 未迁移 / 6 暂时不可用 / 7 尚未 bootstrap。
- 不在代码里硬编品牌相关字符串，标识符里带品牌也算（CLAUDE.md §4）。
- 同意管理放 `/iam/me/*` 而不是 `/oauth/*`；用 `RegisterAuthenticated` 而非 `Register`。
- 撤销必须连带作废未兑换的 authorization code，否则这条授权一分钟后自己复活。
- **验证码不新增 `test` 类型**（@ClaudeWEB 提的，采纳）：发新 type 会逼前端认识「测试模式」这件事。
  改成万能答案之后，验证码照常发、照常是真图，只有结果是定的，前端一行都不用改。
- **万能答案由部署方配置，不是源码里的常量**：常量意味着「在哪个部署忘了关就是同一个后门」，
  而且读代码的人会把它当成仓库在保守的秘密。
- **验证与发放两侧都受配置集合约束**：类型是请求方在 body 里报的，只把某类型从「发放」里去掉，
  它仍然可以被点名到达。没开的类型现在直接拒，答案看都不看。
- 万能答案不改变一次性：命中时挑战照样被消费。
- 「生产环境启动即拒」做不到并且不做：这个代码库里没有环境（prod/dev）这个概念，
  为一个小功能引入一个全局概念不划算。防线是默认关 + 必须显式配 8 字符以上 + 每次启动 warn。

## Confirmed Constraints
- 提交必须逐文件显式暂存；工作区有若干非本轮改动的文件等用户处置
  （`.gitignore`、`README.md`、`pkg/interpreter/interpreter.go`、3 个已暂存的文档改名、未跟踪的 `docs/README.md`）。
- 前端目录归 @ClaudeWEB，禁止碰。
- migration 不写固定账号或默认密码。
- `tools/iam-preflight` 与迁移方案文档里的 chaosplus 路径是迁移来源，保留。
- `apps/backend-api/.temp/` 下的草稿目录（backup-10.1、backup-10.2、10.6、10.9、captcha）不得提交。

## Known Issues / Risks
- 逐包 90% 覆盖率门禁余量极薄（authn、organization、core 都贴着 90.0–90.2），新增生产代码必须同批补测。
- 10.3 principal 归属未决：6 个全局账号接口挂在租户注册器上，其中 3 个要 `platform:administer`，
  平台管理员与租户管理员都不可达。方案 A = 整体挪到平台注册器。已三次说明，等用户一句明确同意。
- 10.4 无处落地：唯一的业务模块 `internal/todo` 既没有 `tenant_id` 列，9 个接口也全走裸 `huma.Register`。
  要么给 todo 加租户维度当样板，要么等真实业务模块，需用户定。
- `organization` 模块的 ID 字段一律 `maxLength:"128"` 无 pattern，`core` 模块一律
  `pattern:"^[1-9][0-9]*$"`。两套写法并存但各自内部自洽，未擅自统一。
- `internal/iam/oauth/service.go:1028` 的 `clientFromRow` 有与 69187ab 修掉的同一个空切片坑：
  `var redirects []string` 在 `RedirectURIs` 为空或解析失败时保持 nil，`redirect_uris` 序列化成 `null`。
- 10.9 剩下四步里，④（签名密钥轮换）会短暂影响所有在途 token 的验签路径，需要一个 TTL 长度的双读窗口；
  ⑤（replay registry）目前是进程内 map，多副本部署下 nonce 每副本各花一次，重放保护实际是关的。

## Verification
Build: PASS  Tests: PASS  Lint: PASS  Integration: PASS
OpenAPI 导出：`make ci` exit 0（已含新增的 `openapi-check`），总体 90.1%。
落地文档 152 路径 / 227 操作 / 294 schema，`make openapi-check` 在人为改坏文档时如实失败并给出修法。
（首轮 CLI 测试因 cobra 本地 flag 跨 `Execute()` 粘连而串味——`--format toml` 漏给了下一个用例，
已按 rewrap 的同一套路加 `keepOpenAPIFlagsFromLeaking` 复位。）
10.9 ③：`make ci` exit 0，总体 90.1%，authn 90.1 / federation 91.2 / provisioning 90.8 /
keyringx 94.6 / core 90.1。（首轮 fmt-check 报 CRLF，lint 报 misspell `enrol`，
`internal/iam/core` 三份冻结接口清单需同步登记新增的三个 `SecretRewrapper`，均已处理。）
10.9 ②：`make ci` exit 0，总体 90.2%，authn 90.2 / federation 91.4 / provisioning 91.0 / secretx 96.1。
（首轮 lint 报 misspell 与两个轮换后不再使用的 `*CipherVersion` 常量，已改。）
10.9 ①：`make ci` exit 0，`pkg/extension/keyringx` 95.2%（门槛 90.0）。
验证码：`make ci` exit 0，`internal/iam/authn` 90.2%，总体 90.1%（首轮 lint 报 `real` 遮蔽内建标识符，已改名为 `solved`）。

## 本轮交付其六：10.9 ④ authn 签名密钥环

`internal/iam/authn/signingkeys.go` 新增 `signingKeyring`：一把私钥签名，环里任一把公钥验签。
`Service` 上原来的 `privateKey`/`publicKey`/`kid` 三元组被这一个 ring 取代（web.go 15 处调用点）。

三个关键取舍，都是「换早了会怎样」推出来的：

1. **验签按 `kid` 选 key，不是逐把试。**逐把试也能通过所有测试，但那样一来「把旧 key 从配置里
   拿掉」这个动作就不再有任何效果——旧 token 仍会被剩下的某把接住。选 key 才让退场是真的退场。
2. **retired key 只保留公钥。**私钥留着没有任何用途，只会让「不小心用旧 key 签名」变成可能。
3. **previous key 解析失败是启动错误，不是跳过。**跳过会得到一个「启动正常、健康检查绿、
   但恰好把该 key 对应的在途 token 全判无效」的部署——正是轮换窗口本身要防的那个故障。

新增配置 `authn.previous_signing_keys` / `authn.previous_signing_key_files`
（命名沿用既有的 `previous_encryption_keys` 先例）。`/.well-known/jwks.json` 现在发布整个环，
active 在前；`oauth/http.go` 的 `jwksDocument.Keys` 本来就是 slice，不需要改。

轮换步骤写进 `README.md` 新增的「Rotating keys」一节，四种密钥（签名 / MFA / federation /
provisioning）一张表列全，并写清签名密钥和存储密钥的差别：存储密钥换完跑 `rewrap-secrets`
按剩余行数决定何时退场；签名密钥没有 rewrap，token 不落库、只会过期，所以旧 key 必须在配置里
留满一个 `access_token_ttl`（外加 relying party 缓存 JWKS 的时间，如果更长）才能拿掉。

测试 `signingkeys_test.go` 六个，含一个端到端：用旧 key 签一个 token，再用
「新 key active + 旧 key previous」的 Service 去验，必须通过；反过来只持新 key 的 Service
必须拒绝旧环签的 token。

## 本轮交付其七：10.3 principal 管理权限语义

`identity-update-principal`、suspend、resume 三条挂的是 `platform_administer`，但它们本身
是租户内操作（路由带 tenantID，service 方法也带 tenantID）。

**我上一轮在频道里把这条说错过**（说成「六条全局 operation 移到 platform 路由」），
读代码后更正：是三条，而且方向相反。挪去 platform 平面等于删掉「租户管理员管自己租户的人」，
正是 Keycloak realm admin 最标准的能力。已在频道公开更正（msg 8b41bef3）。

坏在哪里：`core/authorizer.go` 的 `CheckBulk` 里，platform scope 的 action 走
`allowed[permission] = platformAdministrator` 这一支，`tenant_administer` 的通配
（`grantSet["tenant_administer"]`）**不覆盖它**，所以租户管理员一律 403。

**我第一次报的时候把范围说大了**，说成「谁都调不通」，@ClaudeWEB 拿浏览器审计的反例纠正了：
`CheckBulk` 是先过 `memberActive` 闸再走 platform 分支，所以「既是平台管理员、又是该租户
活跃成员」的账号两个条件同时满足，是能调通的。bootstrap 出来的管理员恰好是这一种——
这也正是浏览器审计一直绿、没能早点把这个 bug 顶出来的原因。口子比我说的小，但对
普通租户管理员依然是全封的。**教训：断言"谁都不能"之前，先找一个能的反例。**

修法两半：

1. 三条 guard 改成权限表里现成的 `user/update`（scope=tenant），与同文件 list/create/get
   用 `user/view`、`user/create` 一致。路由和请求体不变，@ClaudeWEB 确认前端零改动。
2. `authz.Register` 的 `prepare` 补上 scope 检查：platform scope 的 action 注册到
   tenant registrar 直接 panic。这是 `RegisterPlatform` 早就有的检查的反向一半——
   **缺的正是这一半，这个 bug 才漏得出去**。`Register`/`RegisterEntity`/
   `RegisterEntityAdapter` 三个入口共用 `prepare`，一处补齐三处生效。

全仓扫过一遍，除这三条外没有第二处违规：organization 的五条 tenant scope guard 和
core 的 `iam-scope-model` 走的都是 `RegisterPlatform`，`provisioning` 的
`tenant/administer` 在权限表里是 scope=tenant。

测试：`register_test.go` 新增一条覆盖三个注册入口都拒绝 + 租户 action 仍能注册；
`identity/http_test.go` 的 guard 表改为 `user_update`，授权 fake 改成扣 `user_update`
才能试出 403，`CheckPlatform` 改成恒 false（identity 模块本就没有 platform 路由，
恒 false 才能保证测试不是靠它蒙过）。`docs/openapi.json` 差异恰好三行 `x-authz-permission`。


## Last Checkpoint
2026-09-05 · 10.3 完成，`make ci` 绿。上一轮 10.9 ④ authn 签名密钥环已推送（`a12c9fc`）。

**本轮最重要的一件事不是代码，是 D17。**@gankcode 明确指出：10.3/10.4/10.5/10.8/10.10 这五项
不该拿去问他，IAM 领域都有成熟答案，要「按标准的、通用的、大家共识的做法」自己定。
这个批评成立——这五项被标为「待产品确认」已阻塞多轮，而它们并不是本项目特有的产品分歧。
五条方案已写入 `.claude/decisions.md` D17 并在频道公示（msg 4c5cc55c），
用户可否决任意一条，未否决即执行。10.4 用 `internal/todo` 当参照实现已获用户明确确认。
**后续不要再把这五项列为「等用户拍板」。**

上一轮（10.9 ③ + OpenAPI 离线导出，已推送）
本轮还按 @gankcode「做完了文档要么更新一致、要么删掉清理」把文档对齐了实际：
方案 10.9 条目改成「五步已完成三步」并写清剩下的 ④⑤ 各是什么问题；
`docs/README.md` 的「端点去哪看」改指落地的 `openapi.json` 并声明它是唯一机器生成、不得手改的文件；
`README.md` 新增 API document 一节。
下一步：10.9 ④——authn 签名密钥环（JWKS 同时发布 active + previous，旧 key 一个 token TTL 后退场）。
@gankcode 问到能否整体换成 go-captcha：已答——它本来就在用（滑块/点选），
但 v2 只有 click/slide/rotate，没有打字那一种，全换等于去掉唯一键盘可完成的验证码；
可选项是加 `rotate` 作第四种。等回话。
10.3 与 10.4 仍卡在 @gankcode 的决定上。
CLAUDE.md §12「Line endings」那段与仓库实际不符（曾提过一次，未获回应），未擅自改。
