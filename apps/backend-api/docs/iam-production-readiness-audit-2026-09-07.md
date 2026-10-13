# IAM 生产交付深度审计报告

> **历史记录（2026-09-07 的状态快照，不要据此判断当前状态）。** 本文的每一处数字都描述
> 审计当日那个提交，**不得改写**——改了就是伪造记录。当前状态与后续验收见
> [`iam-release-checkpoints.md`](iam-release-checkpoints.md)；已关闭项的存证见其第 7 节
> 与根目录 `ADR.md` D22–D37。本文给出的 `NOT READY` 结论已被后续工作取代。

审计日期：2026-09-07

审计分支：`IAM`，HEAD `66d4c49`

对比基线：`main`，merge-base `1a14c4b`

最终结论：**当前不具备生产交付条件（NOT READY）**

## 一、执行结论

IAM 已经不是原型。当前分支相对 `main`：

- ahead 93 个提交，behind 0；
- 变更 1,087 个文件，约 276,000 行新增；
- 新增完整 IAM 后端、管理控制台、迁移、OpenAPI、测试与审计工具；
- 当前 HEAD 与 `origin/IAM` 一致，审计开始时工作区干净。

现有架构和主要安全机制具备较好的工程基础：认证、租户自举、RBAC、行/字段权限、
OAuth、服务账号、SCIM、Federation、审计归档、密钥轮换和管理控制台均已有实现。

但项目仍不是“只剩测试修复”。生产交付同时被功能、安全、迁移和运维四类问题阻断：

1. Principal 保留期、法律保留和不可逆匿名化仅有内部 service，没有平台 HTTP API 和 UI。
2. 租户可配置的 OIDC issuer / SCIM target 缺少 SSRF 防护。
3. 删除 SCIM target 会丢失远端账号映射，却不会删除远端账号，可绕过匿名化前置检查。
4. 当前后端门禁为红：`gofmt`、覆盖率、integration migration、tests 子模块 lint 均失败。
5. 24 个新迁移尚未写入 `schema.lock`，当前 HEAD 未完成 race、真实 MySQL、恢复演练和最终 UAT。

综合判断：

- IAM 领域功能完成度：**约 80-85%**；
- 生产交付成熟度：**约 65-70%（建议采用 68% 作为当前基线）**；
- 剩余工作量：**约 20-35 工程日**；
- 后端、前端、QA/运维并行且环境就绪：约 **2-4 周**；
- 单人串行：约 **4-7 周**。

## 二、审计方法

本轮采用四个视角交叉审计：

- 主审：Git 状态、`main...IAM` 差异、跨模块契约、真实门禁执行与汇总；
- 后端安全 agent：认证、OAuth/OIDC、授权、租户隔离、Federation、SCIM、擦除；
- 前端/产品 agent：功能覆盖、API 契约、最小权限 UX、桌面/移动端、无障碍；
- 交付 agent：迁移、schema lock、CI、HA、恢复与 UAT。

前端审计参考 `ui-ux-pro-max` 的企业管理台、表单反馈、响应式、键盘与 React 检查项。
各 agent 仅做只读审计；最终问题均由主审对当前源码或真实命令结果复核，不以历史记录代替验证。

## 三、当前 IAM 任务状态

`MEMORY.md` 顶部 handoff 的主方向仍然可信：它明确标记当前是开发 checkpoint，
10.8 和 10.11 未完成，并列出了全 owner 数据验收、并发写入、迁移对齐、生命周期 API/UI、
浏览器验收、最终发布门禁和可复现 UAT 环境。

但该状态已经缺少本次发现的四项重要事实：

- 未记录 OIDC/SCIM 的 SSRF 风险；
- 未记录 SCIM target 删除造成的擦除绕过；
- 未记录当前 89.9% 覆盖率和已提交未格式化文件；
- 只记录 1 个 tests lint 问题，当前实测为 3 个。

Phase 10 逐项状态：

| 项目 | 状态 | 审计判断 |
|---|---|---|
| 10.1 平台管理员安全自举 | 已完成 | CLI 与 readiness 契约存在 |
| 10.2 租户 owner 自举 | 已完成 | 事务自举及存量修复路径存在 |
| 10.3 Principal 租户权限语义 | 已完成 | 已使用 tenant `user/update` 并有注册期 scope 检查 |
| 10.4 行/数据权限 | 已完成参照实现 | `todo` 已形成真实 entity/data-scope 样板 |
| 10.5 字段权限 | 基本完成 | 后端/UI 接入广，但最小权限前端行为仍有缺口 |
| 10.6 OAuth consent | 功能基本完成 | `prompt=none` 错误返回仍不符合 OIDC 协议 |
| 10.7 Audit archive | 已完成且有历史 live 证据 | 当前最成熟的生产工作流 |
| 10.8 Principal 生命周期 | 未完成 | 内部引擎已存在，产品管理面和验收缺失 |
| 10.9 密钥轮换/防重放 | 已完成 | 密钥环、rewrap、签名轮换、DB replay 已存在 |
| 10.10 IAM gRPC | 按决策关闭 | REST/OIDC/SCIM 是有意选择 |
| 10.11 最终产品门禁 | 未完成且当前失败 | 当前 checkout 无法通过已声明门禁 |

## 四、成熟度评分

| 维度 | 分数 | 判断 |
|---|---:|---|
| 登录、会话、MFA、Passkey | 85% | 机制较强；缺 HA captcha 和来源级节流 |
| 租户隔离与授权 | 85% | 服务端链路完整；前端最小权限体验未收口 |
| OAuth/OIDC 与 Federation | 75% | PKCE、consent、防重放存在；SSRF、error redirect、`azp` 待修 |
| SCIM 与远端生命周期 | 60% | 功能丰富；target 删除破坏远端擦除追踪 |
| 审计、归档、密钥运维 | 88% | archive 和 key rotation 完成度较高 |
| Principal 生命周期/擦除 | 45% | 事务与写围栏存在；管理面完全缺失 |
| 前端产品完整性 | 72% | 页面广；生命周期、权限 UX、移动端、a11y 待补 |
| 自动化验证 | 65% | 测试规模大，但当前关键门禁为红且 release gate 分散 |
| 运维、恢复、HA、UAT | 45% | 无最终可复现环境与完整恢复演练 |
| **综合生产交付成熟度** | **68%** | 仍处于功能收尾和 release hardening 阶段 |

## 五、P0：生产阻断问题

### P0-1：租户可控出站 URL 存在 SSRF

OIDC issuer 校验只要求 `http/https` 和 Host，随后使用默认 `http.Client` 请求 discovery、
token/JWKS URL；没有阻止 loopback、RFC1918、link-local、云 metadata、DNS rebinding 或跳转链：

- `internal/iam/federation/service.go:173`
- `internal/iam/federation/service.go:693`
- `internal/iam/federation/verifier.go:62`
- `internal/iam/federation/verifier.go:78`
- `internal/iam/federation/verifier.go:99`

SCIM 对 HTTPS 主机完全放行，同样使用默认出站 client：

- `internal/iam/provisioning/target.go:495`
- `internal/iam/provisioning/service.go:60`
- `internal/iam/provisioning/target.go:257`

影响：具有 identity provider 或 SCIM 管理权限的租户主体，可以让 API 节点探测/访问内网、
管理接口或云 metadata。OIDC 当前还允许任意非本地明文 HTTP issuer。

生产关闭条件：统一的出站 URL policy + transport；每次连接与 redirect 都校验解析后地址，
覆盖 IPv4/IPv6 特殊网段、DNS rebinding，并支持部署 allowlist；补私网/link-local/metadata/
redirect 负向测试。

### P0-2：删除 SCIM target 可绕过远端擦除责任

控制台明确提示“远端资源不会被删除”，后端直接硬删除 target，数据库级联删除 mapping：

- `apps/frontend-web-admin/apps/iam/src/app/scim-directories/targets.tsx:632`
- `internal/iam/provisioning/target.go:191`
- `internal/iam/provisioning/repository.go:222`
- `internal/iam/provisioning/sql/postgres/00002_scim_targets.sql:25`

Principal 擦除只检查仍然存在的 live mapping：

- `internal/iam/provisioning/lifecycle.go:26`

结果：删除 target 后 bearer token 和 remote ID 均丢失，远端账号/PII 仍存在，本地匿名化却可放行。
当前删除测试只覆盖空 target，没有 live mapping + erasure 绕过测试。

生产关闭条件：有 live mapping 时禁止删除；或实现持久化 deprovision job，远端删除成功或形成
受治理的例外记录前，不销毁凭据、mapping 和审计证据。

### P0-3：Principal 生命周期不是可运营的产品能力

保留期限、法律保留、版本并发和事务擦除存在于：

- `internal/iam/identity/lifecycle.go:42`
- `internal/iam/identity/lifecycle.go:58`
- `internal/iam/identity/lifecycle.go:106`
- `internal/app/principallifecycle.go:20`

但 identity HTTP 在 suspension 后直接进入 service-account 路由，前端 client 也只有停用/恢复：

- `internal/iam/identity/http.go:261`
- `apps/frontend-web-admin/apps/iam/src/lib/iam-api.ts:1282`

当前 OpenAPI 没有 principal retention/legal-hold/anonymize/lifecycle operation。运维人员无法查看
deadline/hold/blocker、执行不可逆确认、触发匿名化或识别 tombstone 状态。

生产关闭条件：新增仅全局 platform authority 可调用的 API、stale-version 与 blocker 投影、
不可逆确认 UI、OpenAPI/type 更新及真实桌面/移动验收；tenant-local 权限不得授权该流程。

### P0-4：当前后端发布门禁为红

本轮对当前 HEAD 的真实结果：

- `make ci`：第一关即 FAIL，`internal/iam/authn/recovery.go` 未通过 `gofmt`；
- `make test-coverage`：FAIL，89.9%，门槛 90.0%；
- `make test-integration`：FAIL；
- tests 子模块 lint：FAIL 3 项：
  `tests/internal/testdb/testdb.go:101`、`:178`，
  `tests/migration/iam_core_model_test.go:23`。

Integration 失败不止一处：

1. `TestIAMCoreMigrationsAcrossEngines` 在 SQLite、MySQL-compatible、PostgreSQL 的 plain/prefix
   六种组合全部失败。独立回滚 core v13 时访问 identity-owned
   `iam_principal_lifecycles`，fixture 中该表不存在：
   `tests/migration/iam_core_test.go:74`、
   `internal/iam/core/sql/sqlite/00013_principal_erasure_guards.sql:78`、
   `internal/iam/core/sql/postgres/00013_principal_erasure_guards.sql:135`。
2. Federation migration fixture 仍期望 milestone `replay`，实际已是 `principal-erasure`：
   `tests/migration/iam_federation_test.go:107`。
3. Identity/authn fixture 仍期望 `base` / `verification-attempts`，实际是
   `lifecycle` / `principal-erasure`，错误文本断言也过期：
   `tests/migration/milestone_test.go:42`、`:43`、`:67`。
4. Migration package 最终达到 Go 默认 10 分钟超时，停在
   `TestIAMMigrationStatusAcrossEnginesAndPrefixes` 的 PostgreSQL/prefixed 场景。

生产关闭条件：明确 standalone-down 与协调 reverse-DAG rollback 契约，更新版本 fixtures，
重跑三引擎 upgrade/down/up 与真实 MySQL 8.4；覆盖率必须通过行为测试恢复，不能降门槛或加排除。

### P0-5：迁移发布、恢复和 UAT 不完整

`schema.lock` 有 171 项，IAM 源树已有 195 个 migration 文件。普通 verifier 只证明已发布的
171 项未被篡改，并有意允许更高版本的新文件（`tools/iam-schema-lock/main.go:396`）；只有
`--write` 会扩展 lock（`tools/iam-schema-lock/main.go:87`）。因此当前 24 个新迁移尚未冻结。

仓库也没有最终 UAT 的 start/stop/reset、依赖要求、URL、安全测试账号交付和 acceptance checklist。
计划要求的 `docs/runbook-migration-recovery.md` 不存在；现有擦除恢复说明没有可执行步骤、验证 SQL、
RTO/RPO 或演练记录。

生产关闭条件：真实 MySQL 门禁通过后写入并评审 lock；提供 UAT 环境；完成 backup/restore、
擦除 reconciliation、archive recovery、rollback/roll-forward 演练。

## 六、P1：高优先级问题

### P1-1：OIDC 协议兼容缺口

`prompt=none` 在 service 中能识别 consent 缺失，但 HTTP 层返回本地 400 JSON，没有向已验证的
redirect URI 回传 `consent_required` 与原始 `state`：

- `internal/iam/oauth/service.go:411`
- `internal/iam/oauth/http.go:396`
- `internal/iam/oauth/http_test.go:677`

Federation ID-token 验证只确认 `client_id` 在 `aud` 中，多 audience 时没有验证 `azp`：

- `internal/iam/federation/verifier.go:195`
- `internal/iam/federation/verifier.go:214`

若产品声明标准 OIDC Provider，这两项必须在 conformance 前修复。

### P1-2：公共认证缺少来源级分布式限流

公共登录路由位于 `internal/iam/authn/http.go:452`。失败密码只更新账号锁定状态
（`internal/iam/authn/web.go:393`、`:415`），后端没有 IP/device/global limiter。

影响：可进行用户名喷洒，也可对已知账号制造锁定。应实现共享限流，或把经过验证的 ingress/
API gateway 策略设为强制生产前置条件，并覆盖多副本与故障切换。

### P1-3：注册 captcha 是进程内状态

- text：`internal/iam/authn/captcha.go:19` 使用 `DefaultMemStore`；
- slide/click：`internal/iam/authn/gocaptcha.go:81` 使用包级 map；
- verify：`internal/iam/authn/gocaptcha.go:272` 只消费本进程答案。

无粘性负载均衡时，issue 与 verify 落到不同副本会随机失败。应接共享 one-shot store；否则必须把
sticky session 及其 failover 行为固化为部署约束并验收。

### P1-4：最小权限角色会出现整页假失败

Principals 页面用一个 `Promise.all` 同时请求 principals、members、departments、field catalog：

- `apps/frontend-web-admin/apps/iam/src/app/principals/page.tsx:63`

仅有 `user/view` 而没有 `dept/view` 的用户能看到菜单，却会因一个预期 403 丢失整页数据。
Roles 页面在 `apps/frontend-web-admin/apps/iam/src/app/roles/page.tsx:117` 有相同全成全败模式。

应拆分必需/可选数据，仅降级受影响控件，并增加真实最小权限 persona 浏览器测试。

### P1-5：前端操作按钮没有按具体权限裁剪

导航按权限过滤，但 view-only 用户仍会看到 create/update/delete 操作：

- `apps/frontend-web-admin/apps/iam/src/app/oauth-clients/page.tsx:247`
- `apps/frontend-web-admin/apps/iam/src/app/principals/page.tsx:260`

服务端仍能拒绝越权，因此不是安全绕过；但这会制造大量可预期 403，并让权限配置难以理解。

### P1-6：移动端与无障碍验收未达到生产级

- 移动端选择路由后不会关闭导航 Sheet：
  `apps/frontend-web-admin/apps/iam/src/app/layout.tsx:108`、
  `apps/frontend-web-admin/apps/iam/src/components/app-sidebar.tsx:171`；
- 全局按钮高度仅 24-36 px，低于 44 px 移动触控目标：
  `apps/frontend-web-admin/packages/ui/src/components/button.tsx:24`；
- 租户输入移除 outline 且无等价 focus-visible：
  `apps/frontend-web-admin/apps/iam/src/app/layout.tsx:137`；
- 移动 Sheet 缺少 accessible title：
  `apps/frontend-web-admin/packages/ui/src/components/sheet.tsx:58`。

应加入键盘/焦点返回、accessible name、触控尺寸和真实移动导航门禁。

### P1-7：发布门禁是分散的

`apps/backend-api/Makefile:149` 的 `make ci` 只覆盖 format、主模块 lint/coverage、integration、
build、OpenAPI；未强制 tests 子模块 lint、race、generate 零差异、performance、真实 MySQL、
schema publication、前端/browser、live readiness、恢复演练。仓库也没有 tracked CI workflow
组合这些门禁。

应建立单一、可重复的 release target/pipeline，不能继续依赖 task-state 中的历史命令记录。

## 七、P2：优化项

- Principal 搜索缺 loading、成功后未清旧 error、无 debounce/abort/request ordering，旧响应可覆盖
  新查询：`apps/frontend-web-admin/apps/iam/src/app/principals/page.tsx:63`、`:167`。
- 平台审计侧栏和页面内 Platform tab 的权限表现不一致：
  `apps/frontend-web-admin/apps/iam/src/app/audit-governance/page.tsx:55`。
- 前端 production build 通过，但 Vite 警告
  `apps/frontend-web-admin/apps/iam/vite.config.ts:14` 的 `__dirname` 与计划中的 native config 不兼容。
- 迁移计划 `iam-domain-migration-plan.md:951` 仍称字段权限和 audit archive 缺失，详细条目
  `:959`、`:964` 又标记完成，摘要需要对齐。

## 八、值得保留的实现强项

- JWT 每次访问校验当前 principal 状态与 credential version，停用/改密可即时撤权；
- session cookie 默认 HttpOnly、Secure、SameSite=Lax，cookie 写操作执行 Origin CSRF 校验；
- OAuth code flow 强制 S256 PKCE，code 单次消费，refresh-token family reuse 会整族撤销；
- Federation state 加密且 replay 已使用数据库持久化；
- active/previous signing/encryption key、rewrap 统计与退役流程已实现；
- tenant authorization 会检查 active membership，并由服务端覆写 trusted tenant context；
- 聚焦审计未发现直接跨租户 repository 越权；
- principal erasure 锁定 principal，并在一个事务中组合七个 owner；
- legal hold、retention、suspension、remote mapping 等前置条件 fail closed；
- tombstone 后有数据库 trigger 防止 PII 恢复；
- audit archive 保留源事件，验证精确对象版本、digest 和 retention；
- 前端类型来自提交的 OpenAPI，当前 OpenAPI 与代码一致。

这些基础足以支持增量完成，不需要重写 IAM 架构。

## 九、本轮真实验证结果

| 当前 HEAD 检查 | 结果 |
|---|---|
| `git status --short --branch` | PASS：审计前干净，`IAM...origin/IAM` |
| `git diff --check main...HEAD` | PASS |
| 后端 `make ci` | FAIL：第一关 `gofmt` |
| 后端主模块 lint | PASS |
| 后端 tests 子模块 lint | FAIL：3 项 |
| 后端覆盖率 | FAIL：89.9% < 90.0% |
| 后端 integration | FAIL：core rollback、过期 milestone fixtures、10 分钟超时 |
| 后端 build | PASS |
| 后端 OpenAPI consistency | PASS |
| schema-lock 只读验证 | 171 项 PASS；24 个新 migration 尚未冻结 |
| 前端 typecheck | PASS |
| 前端 lint | PASS |
| 前端 unit test | PASS：IAM 45，shared UI 85 |
| 前端 production build | PASS，有 Vite 未来兼容警告 |
| race | 当前 HEAD 未重跑 |
| 真实 MySQL 8.4 | 当前 HEAD 未重跑 |
| performance thresholds | 当前 HEAD 未重跑 |
| generated source 零差异 | 当前 HEAD 未重跑 |
| 真实桌面/移动 lifecycle | 未实现/未验收 |
| backup/restore、rollback/roll-forward | 未完成演练 |

说明：`make test-integration` 中 architecture、goose、lifecycle、performance 包通过；失败集中在
migration 包，但该包是发布核心门禁，不能降级为“只有测试断言问题”后忽略。

## 十、建议交付路线

### 阶段一：关闭安全与数据生命周期 P0（8-14 工程日）

1. 实现 OIDC/SCIM 出站 SSRF policy 与负向测试；
2. 修复 SCIM target 删除和远端擦除责任链；
3. 发布 platform-only lifecycle API 和控制台流程；
4. 完成七 owner 填充数据、legacy notification、disabled owner、双向并发时序验收。

退出条件：PostgreSQL/MySQL 均覆盖 P0；运维可通过公开产品面安全完成整个生命周期流程。

### 阶段二：修复并统一发布门禁（5-9 工程日）

1. 修复格式、行为覆盖率、integration rollback/version fixture、tests lint；
2. 修复 OIDC `prompt=none`/`azp`，确定分布式登录限流和 captcha HA；
3. 跑 race、generate、三引擎 upgrade/down/up、permission matrix、真实 MySQL 8.4、性能阈值；
4. 所有数据库门禁通过后，冻结并评审 195 个 migration。

退出条件：从 clean checkout 运行单一 release pipeline 全绿，不能跳过前端或 tests 子模块。

### 阶段三：产品与运维验收（5-12 工程日）

1. 修最小权限 UX、移动导航和关键 a11y；
2. 提供可复现 UAT start/stop/reset 与安全 persona；
3. 在真实组合后端上跑桌面/移动工作流，不使用 API mock；
4. 演练 backup/restore + erasure reconciliation、archive recovery、rollback/roll-forward，
   记录 RTO/RPO 与证据。

退出条件：产品、安全、QA、运维共同验收同一个不可变 release candidate；此后才能勾选 10.8/10.11。

## 十一、最终建议

当前不要合并并标记为生产可交付。应先继续补功能和修安全/契约，再做测试修复和 release hardening。
IAM 的主体架构已经足够好，剩余工作应聚焦收口，不应重写。
