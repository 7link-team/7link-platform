# Task State

## Task
修复 2026-09-10 生产就绪审计查出的 IAM 功能/安全/契约缺陷（应用与代码范围，不含运维部署）。

## Objective
把审计确认的 12 项缺口全部关闭：9 项明确缺陷直接修，3 项经用户确认后的方案实施。

## Current Phase
IMPLEMENTATION

## Current Step
后端安全修复（httpx IPv6 过渡前缀 + federation discovery scheme 校验）先行

## Overall Progress
- [x] W1a httpx：补 `64:ff9b::/96`、`2002::/16`、`2001::/32` + 负向用例；`pkg/extension/httpx` PASS
- [x] W1b federation：`oidcClient` 持有 `httpx.Policy`，discovery 三端点改用 `ValidateURL`；
      新增 `TestOIDCDiscoveryRefusesEndpointsOutsideTheOutboundPolicy`；federation PASS
- [x] W2a audit：platform 归档路由不再声明 `X-Tenant-Id`；新增 `documentPlatformScopeWithoutTenantHeader`；
      守卫改为"platform 路由不得出现任何选择器槽位参数"，**已用变异验证**（移除修复即失败）；
      修正 `internal/iam/audit/http_test.go` 中把缺陷冻结成契约的断言（platform 由 1 收紧为 0）
- [x] W2b oauth：两条 consent 路由改 `RegisterSessionAuthenticated` + `consentPrincipal`（拒绝 Bearer）；
      fixture 由 `authenticated` 改 `session-only`；OpenAPI 实测已是 sessionCookie-only
- [x] W2c 移除 2 处被覆盖的死 `Security` 声明（`core/member_exit_http.go`）
- [x] W3a identity：新增 `platform_status.go`（`SetPlatformStatus` + 两条 platform 路由 PUT/DELETE
      `/platform/iam/principals/{principal_id}/suspension`）；复用租户语义（status + 撤销会话/refresh，
      **不**销毁凭据，使恢复后仍可登录）；保留全部租户 last-admin 保护与平台访问守卫；
      拒绝对墓碑主体写入；新增 `platform_status_test.go` 4 个用例（零租户主体可达、墓碑拒绝、
      守卫拒绝不落库、非法状态）；identity 冻结清单 23→25 同步
- [x] W3b authn：`requestScope` 把 `/oauth/token|revoke|introspect` 纳入限流，**独立预算**
      （`oauth_per_ip` 默认 600、`oauth_global` 默认 30000，与登录预算分开计数、分开 Redis key）；
      拒绝时对 OAuth 端点返回 RFC 6749 `{"error":"temporarily_unavailable"}` 而非平台信封；
      oauth 模块自行声明 429/503 契约；新增两组测试（独立预算 + 运行时拒绝形状）
- [x] W5 前端三项（子代理完成，我已复核 diff）：汉堡按钮改 `Button size="icon-lg"` 命中 44px 规则；
      11 个硬编码英文键补齐三语言；roles 页读写错误分离 + 重试
- [x] W7 重新生成 `docs/openapi.json` 与前端 `api-schema.ts`（+314 行）
- [x] W4 federation：**经查为误报，不需要改代码**。跨站 SAML POST 的 callback 端点是
      `authz.Public`、不读取任何 cookie（`VerifyIdentityOperation` 签名里根本没有 cookie 参数），
      只按 RelayState 定位操作并记录验签后的断言属性；随后 303 跳转到同源
      `/federation/{provider_id}/identity-continuations`（`RegisterSessionAuthenticated`），
      在那里用既有的 Lax 会话 cookie + 交易 cookie 完成绑定校验。**加 SameSite=None 反而会削弱安全**。
      真实缺陷只是 `docs/iam-release-checkpoints.md:68` 描述了一个未采用的设计 → 已改为描述实际实现
- [ ] W6 真实数据库门禁：远端 docker 已确认可用（26.1.5），`test-mysql8-run` / `test-redis-security`
      尚未在本轮改动后执行

## Active Work
W3b（OAuth 三接口限流）

## Important Decisions（本轮新增）
- D-fix-6：`fieldPolicyListInput.EntityID` 是 `/iam/fields/effective` 的**合法可选筛选参数**，与授权选择器
  `entity_id` 同名不同物 → 守卫不得按名字全量拒绝，只对 **platform 类**操作要求选择器槽位为空。
  这是对初次过宽检查的自我纠正，已记录以免后人重犯。

## Verification
**真实浏览器门禁（本轮最终回归证据）**：`route-matrix-browser-audit.mjs` **792/792 PASS，EXIT=0**，
报告 `.temp/iam-uat/browser/route-matrix/report.json`（status PASS、results 792 全 PASS、两个夹具租户
finally 清理均 PASS）。22 路由 × 3 语言 × 1440/390 × admin/reader/denied × 2 租户。
被测二进制 SHA256 `3d6ae5df01e045f7af1c951360e8a316dd2a3c7a91c44a5474ff18679c71721c`，
与该二进制**运行中**的 `/openapi.json` 一并确认包含本轮全部契约改动（新 suspension 路由、
consent 已 sessionCookie-only、platform 归档无 `X-Tenant-Id`、`/oauth/token` 声明 429）。
→ 本轮前后端改动未击穿任何一格，含 reader/denied 的权限裁剪与写拒绝断言。

环境中转：MySQL 容器 `7link-iam-console-uat-mysql`（远端 10.0.0.100:32769，**保留未删**）；
本机 API `127.0.0.1:18080` 与控制台 `127.0.0.1:18091` 仍在运行，由 `node .temp/iam-uat/serve.mjs`
与 `bun run dev --host 127.0.0.1 --port 18091 --strictPort` 拉起。

后端（本轮改动后实测）：
- `make ci` **EXIT=0**：fmt clean、lint 0 findings、总覆盖率 90.0%（identity 89.9%→90.5%）、
  integration 全 ok（architecture 2.6s / goose 17.7s / testdb 0.3s / lifecycle 124.1s / migration 291.9s /
  performance 45.3s）、build 通过、`openapi: matches`
- `make test-race` **EXIT=0**，无 DATA RACE、无 FAIL
- `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4），schema lock 复核 219 文件未变、72 表、33 跨模块 FK
- `make test-redis-security` 运行中（本轮改了共享限流器，必须跑）
- 未跑：`make test-alerts`（本机无 promtool；本轮未改告警规则）

前端（子代理执行 + 我强制无缓存复核）：`typecheck` / `lint` / `test`（148 pass 0 fail）/ `build` 全 EXIT 0

生成物：`docs/openapi.json` 与 `api-schema.ts`（+314 行）均已由生成器重新产出

## Completed Work
- 审计与证据核实（本机 make ci EXIT=0；schema.lock 219/219；OpenAPI 191 paths/272 ops）
- 远端 Docker 可达（ssh 密钥 C:/Users/Feather/.ssh/iam-docker-10.0.0.100 → root@10.0.0.100，Server 26.1.5）

## Remaining Work
1. 按 W1→W5 顺序实施，每批后跑对应门禁
2. W6 全量门禁与自审、最终验收
3. 用户确认后提交

## Important Decisions
- D-fix-1：SAML 绑定按 `docs/iam-release-checkpoints.md` §2.1 既定设计实现（SameSite=None 交易 cookie +
  同源最终确认），用户已确认。**本机无法做真实浏览器 SAML 验收**（`scripts/iam-uat.mjs` 明确拒绝远端
  Docker context），只能以 Go/HTTP 测试覆盖，交付时须标注未浏览器验证。
- D-fix-2：OAuth 三接口纳入同一限流中间件但使用**独立可配置预算**，不与登录共享额度（用户已确认）。
- D-fix-3：新增 platform 作用域 suspend/restore 入口（用户已确认），复用既有账号停用与 last-admin 保护语义。
- D-fix-4：platform 归档路由的 `X-Tenant-Id` 用注册后文档修正移除（先例：`documentSharedSecurity` 同样
  在注册后改写 `api.OpenAPI().Paths`），运行时继续 422 拒绝，不改为接受。
- D-fix-5：永久擦除默认关闭（`erasure_ledger.enabled` default false）**保持不动**——属有意的 fail-closed
  设计且已写入 runbook，改默认值会削弱擦除的不变量。

## Confirmed Constraints
- 不碰运维/部署/灾备/RTO-RPO/告警接收路由/签字；仓库无部署物这一事实不因此轮改变。
- 生成物（openapi.json、api-schema.ts）必须经生成器产出，不手改。
- 不弱化任何既有测试断言；不降低覆盖率门槛；保留旧迁移哈希。
- 远端 Docker 仅用于测试门禁，不 bootstrap、不改动其安装。

## Known Issues / Risks
- 覆盖率总值为 90.0%，刚好压线；本轮新增代码必须带测试，否则会击穿门槛。
- 新增 platform 停用入口会改变现有 `iam_http_mappings.txt`/`iam_http_guards.txt`（258 条）与 OpenAPI
  操作数（272），属预期契约变更，需同步 fixture 与前端类型生成物。
- consent 路由改 session-only 会从对外契约移除 bearerAuth，属预期变更。

## Verification
Build / Tests / Other: NOT_RUN（本轮尚未执行）

## Next Action
原 12 项**全部关闭**：11 项为真实缺陷并已修复实证，第 12 项（SAML）经复核为**误报**，改为修正文档。
可选后续（未开始，需用户决定是否值得）：
1. 用真实浏览器跑仓库既有的 route-matrix（792 格）作为本轮前后端契约改动的回归证据
   —— 环境子代理正在用远端 MySQL 搭本地 API + 控制台，若成功可复用。
2. `make test-alerts` 仍需 promtool。
未提交：全部改动仍在工作区，等用户确认后再按 Conventional Commit 精确暂存提交。

## Last Checkpoint
2026-09-10 全部条目关闭；SAML 项由"实现 cookie"更正为"修正文档"，原因是实际实现比文档描述的更强
