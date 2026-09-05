# Task State

## Task
IAM Phase 10 后端推进。10.1（平台超管 bootstrap + reset-admin-password）、10.2（新租户同事务 owner 自举）、
§2.5.2（存量零成员租户修复命令）均已推送。当前：10.6 补齐 OAuth 同意的自助撤销。

## Objective
用户授权过一个 OAuth 应用之后，`iam_oauth_consents` 里会留下一条常驻授权，但产品里没有任何地方
能看见它、更没有地方能收回它。补上标准的自助能力：列出「我授权过哪些应用」，以及撤销其中一条——
撤销要真正结束访问，而不只是停止续期。

## Current Phase
IN PROGRESS（10.1 / 10.2 / 10.6 / §2.5.2 与 4 条外部报障均已推送；10.3、10.4 等 @gankcode 拍板）

## Current Step
10.6 已完成。下一步是 10.9（密钥/密钥环轮换），这是当前唯一不需要用户拍板就能推进的大块后端工作。

## Overall Progress
- [x] Phase 10.2（提交 24f2483）
- [x] Phase 10.1（提交 d253c5e）：`iam bootstrap-platform-admin` + `iam reset-admin-password`
- [x] 品牌 cookie 清理（提交 348a37f）
- [x] §2.5.2 存量零成员租户修复命令（提交 20bae43）
- [x] CLI flag 下划线/中划线在所有子命令一致（提交 5d7e9d0，@ClaudeWEB 报的第 4 条）
- [x] `allowed_return_urls` 改为 origin 精确 + 路径前缀（提交 1ecc9cb，@ClaudeWEB 报的第 3 条）
- [x] create/update 可选外键 schema 对称 + invitation 空值线上形状（提交 69187ab）
- [x] Phase 10.6 OAuth 同意自助列出与撤销
- [ ] 10.3 principal 管理权限语义（等用户明确拍板）
- [ ] 10.4 数据权限端到端（缺可挂载的租户级业务资源，等用户定目标）
- [ ] 10.9 密钥轮换（下一项，纯技术，不阻塞）
- [ ] 10.5 / 10.7 / 10.8 / 10.10 / 10.11

## 本轮交付（10.6 OAuth 同意撤销）
两个自助接口，都挂 `RegisterAuthenticated`（认证即可，不带租户，不查成员资格）：
- `GET /iam/me/oauth-consents` — 列出本人授权过的应用（含应用名、scope、授权时间、最近使用时间）
- `DELETE /iam/me/oauth-consents/{client_id}` — 撤销其中一条

`Service.RevokeConsent` 在一个事务里做完三件事：吊销该授权已签发且未吊销的 refresh token、
作废该授权已签发且未兑换的 authorization code、删除 consent 行，然后写 `oauth_consent_revoked` 审计
（`Detail["scopes"]` 记下被撤走的 scope，因为行马上就没了）。

新增/改动文件：
- `internal/iam/oauth/repository.go`：`grantedConsent` 行结构、`consentsForPrincipal`（join client 取应用名）、
  `deleteConsent`、`revokeGrantedTokens`。
- `internal/iam/oauth/service.go`：`GrantedConsent`、`ListConsents`、`RevokeConsent`。
- `internal/iam/oauth/http.go`：`registerGrantHTTP`、`callingPrincipal`、`oauthConsentError`。
- `internal/iam/oauth/i18n/`：新增 `consent_not_found`，三个 locale 同步。
- 路由数三处硬编码同步：`oauth/http_test.go`(15→17)、`oauth/module_test.go`(15→17)、
  `internal/app/bootstrap_iam_openapi_test.go`(211→213)，外加两份冻结清单
  `internal/app/testdata/iam_http_mappings.txt` / `iam_http_guards.txt`。
- `docs/iam-domain-migration-plan.md`：oauth 路由清单 12→14，勾上 10.1 / 10.2 / 10.6。

## Important Decisions
- 跨模块只走窄事务能力（`func(ctx, *gorm.DB, ...)`，Provide 阶段发布、Inject 阶段解析）。
- 修复命令是 create-only：只要租户已有任何 member 或 role 就拒绝，绝不改动已有归属。
- 修复写的表与正常建租户完全一致，所以「修过的租户」在状态上不可区分；差异只留在审计事件的
  `repaired=true` 上，这是事后追责唯一能找到的地方。
- 密码永不作为命令行参数（10.1 已确立）。修复命令根本不碰凭据。
- 退出码按运维下一步动作区分：3 已 bootstrap / 4 冲突 / 5 未迁移 / 6 暂时不可用 / 7 尚未 bootstrap。
- 不在代码里硬编品牌相关字符串，标识符里带品牌也算（CLAUDE.md §4）。
- 同意管理放 `/iam/me/*` 而不是 `/oauth/*`：`/oauth/*` 是 RFC 协议面（authorize/token/revoke/
  introspect/userinfo/同意页），管理授权是 IAM 管理 API，`/iam/me/*` 已经是既有的自助前缀
  （`organization-my-tenants` → `/iam/me/tenants`）。
- 同意属于人不属于租户，所以用 `RegisterAuthenticated` 而非 `Register`：要求 `X-Tenant-Id`
  恰好会藏起用户最需要看见的那些授权，而且任何租户权限都不该挡在人和自己的授权之间。
- 撤销必须连带作废未兑换的 authorization code：撤销前几秒签发的 code 一旦兑换就变成活的
  refresh token，只吊销 refresh token 会让这条授权在一分钟后自己复活。

## Confirmed Constraints
- 提交必须逐文件显式暂存；工作区有若干非本轮改动的文件等用户处置
  （`.gitignore`、`README.md`、`pkg/interpreter/interpreter.go`、4 个已暂存的文档改名、未跟踪的 `docs/README.md`）。
- 前端目录归 @ClaudeWEB，禁止碰。
- migration 不写固定账号或默认密码。
- `tools/iam-preflight` 与迁移方案文档里的 chaosplus 路径是迁移来源，保留。

## Known Issues / Risks
- 逐包 90% 覆盖率门禁余量极薄（authn 90.1%、organization 90.0%、core 90.1%），新增生产代码必须同批补测。
- 10.3 principal 归属未决：6 个全局账号接口挂在租户注册器上，其中 3 个要 `platform:administer`，
  平台管理员与租户管理员都不可达。方案 A = 整体挪到平台注册器。已三次说明，等用户一句明确同意。
- 10.4 无处落地：方案文档要求把 `DataConstraint` 接到「真实业务资源」上，但仓库里唯一的业务模块
  `internal/todo` 既没有 `tenant_id` 列，`internal/todo/http.go` 的 9 个接口也全走裸 `huma.Register`，
  没有任何 authz 守卫。要么给 todo 加租户维度当样板，要么等真实业务模块，需用户定。
- `organization` 模块的 ID 字段一律 `maxLength:"128"` 无 pattern，`core` 模块一律
  `pattern:"^[1-9][0-9]*$"`。两套写法并存但各自内部自洽，未擅自统一。
- 验证码方案等 @gankcode 拍板两件事：默认配置提供哪些验证码类型（建议只给文字），
  以及是否加一个配置选中的「测试用类型」。若同意，后端改动是把 `issueCaptcha()` 里写死的
  `secureIntn(3)` 换成从配置集合里选。
- `internal/iam/oauth/service.go` 的 `clientFromRow` 有与 69187ab 修掉的同一个空切片坑：
  `var redirects []string` 在 `RedirectURIs` 为空或解析失败时保持 nil，`redirect_uris` 会序列化成
  `null` 而不是 `[]`。本轮不在 10.6 范围内，未动。

## Verification
Build: PASS  Tests: PASS  Lint: PASS  Integration: PASS
`make ci` exit 0；`internal/iam/oauth` 91.5%（门槛 90.0），总覆盖率 90.1%（门槛 90.0）。

## Last Checkpoint
2026-09-05 · 10.6 OAuth 同意自助列出与撤销已完成并推送。
下一步：10.9 密钥/密钥环轮换（key id、双读单写窗口、回滚、旧密钥退役，
以及把 federation 的进程内 replay registry 换成多副本可用的共享 nonce 状态）。
10.3 与 10.4 仍卡在 @gankcode 的决定上。
