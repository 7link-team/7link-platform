# Task State

> 前序任务「Storage Errors Must Not Read As Client Errors」已于 a842902 交付,
> 其提交↔修复映射见 `git show a842902:.claude/task-state.md`。本文件现承载下述任务。

## Task
把社交登录 connector(含 Casdoor 覆盖的国内 provider)迁移进 Go IAM 的 `internal/iam/federation`。

## Objective
federation 新增第三种 provider 类型 `oauth2`(plain OAuth 2.0 + profile 文档),
让一个社交 provider 的接入成本降到「一行表数据」,并复用既有的开户/绑定/审计/会话签发路径。

## Current Phase
ACCEPTANCE —— 实现与全部可执行门禁通过,等用户验收。

## Current Step
`make ci` EXIT=0 全绿,`make test-performance` PASS。待用户验收与两项决定(见下)。

## Overall Progress
- [x] 读通 federation 现有 OIDC/SAML 实现与接入点
- [x] 复用缝:`Service.authenticate(ctx, provider, idTokenClaims, method)` 一行未改
- [x] 设计:声明式 connector 表 + `profileFlow` 接口逃生口
- [x] `connectors.go` 引擎(claim 点号路径、三种 token 请求形状、三种 profile 鉴权、body 上限)
- [x] `connector_catalog.go` 24 行 connector
- [x] `connector_qq.go` bespoke flow(form 编码 token + JSONP + 第三次请求)
- [x] domain.go:`ProviderOAuth2` + `ErrNoUpstreamAuthTime`
- [x] service.go:normalizeProvider / StartLogin / exchangeUpstreamIdentity / 部署身份 User-Agent
- [x] identity_links.go:重认证双重拒绝、绑定放行
- [x] 三方言迁移 00008(SQLite 走表重建)+ migrate.go 里程碑 social-providers@8
- [x] http.go:enum 加 oauth2 + `/identity-provider-connectors` 目录端点 + 三个错误映射
- [x] i18n 三语各 +3 key
- [x] 测试:connectors_test / connector_catalog_test / connector_qq_test / service_social_test
- [x] 登记四处冻结清单(federation 路由清单、里程碑计数、app 的 mappings/guards/count)
- [x] 自查发现并修复两个真 bug:`tokenRequestQuery` 发 POST 空 body(微信是 GET)、
      微博/抖音的 secret 必须进 body 而非 Basic(建模为 `clientAuth`,用 OAuth 2.0 自己的命名)
- [x] 重新生成 `docs/openapi.json`(+151 行)与前端 `api-schema.ts` / `api-routes.ts`,
      `check:api-types` 与 `typecheck` 均通过
- [x] i18n 一致性自查:三语各 22 key,与 22 个常量一一对应,无空值
- [x] `tests/migration` 四个失败全部修复(见下「迁移发现」),全套 120s 通过
- [x] `make ci` EXIT=0(gofmt clean / lint 0 issues / schema lock / 总覆盖率 91.1% /
      openapi 匹配 / 前端 check:api-types + typecheck 全过)
- [x] `make test-performance` PASS(未触碰性能基线)

## 迁移发现(四个,都是真问题)
1. **SQLite 临时表名被当成模块拥有的表**:`tests/migration` 的 ownership 门禁正则扫
   `CREATE TABLE {{.Prefix}}iam_*`,我原来建的 `iam_identity_providers_v8` 被算进去。
   改成 authn 先例的形状:旧表 `RENAME TO ..._old`,新表用真名 `CREATE`。
2. **父表重命名会带走子表 FK**:authn 先例改的是子表,我这张是父表(三个子表指向它),
   现代 SQLite 的 RENAME 会把子表 FK 一起改写成指向 `_old`。加 `PRAGMA legacy_alter_table = ON`。
3. **两个测试替身假设「binding-trust 是最后一个里程碑」**
   (`tests/migration/iam_principal_erasure_test.go`、`internal/app/migration_status_integration_test.go`):
   它们只剥掉 `*_binding_trust.sql` 却保留我的 00008,导致源目标 8 > 里程碑 6/7。
   改成按**版本号**切(保留 < cutoff 的文件与里程碑),意图更明确且不会再被后续迁移打破。
4. **被拒绝的降级是部分执行的** —— 需要你定夺,见下。

## 已解决:不可逆降级改为事前拒绝(D-H)
里程碑新增 `Irreversible bool`(`pkg/module/module.go`),federation 在 binding-trust@7 上声明。
`internal/app/migration.go` 的 `downTarget` 在**任何回滚执行之前**拒绝跨越它,
错误含「nothing was rolled back」。SQL 里三方言的 RAISE 保留作最后兜底
(`tests/migration/iam_federation_test.go` 直接调 goosex 的那条用例现在是它的唯一覆盖)。
新增 `TestIrreversibleMilestoneRefusesBeforeRollingBack` 与
`TestIrreversibleMilestoneDoesNotBlockWhatWasNeverApplied`(合成模块,三引擎),
并做过变异验证:去掉守卫后用例立刻变红。
原来那条「失败后状态仍干净」的冻结断言因此重新成立,我撤回了为部分回滚加的临时断言。

## 旧记录(已解决,保留原因说明)
`MigrationDownTo federation To: 6` 会**先成功回滚我的第 8 版**,再撞上 binding-trust
不可逆守卫而中止,把库留在版本 7。以前 7 就是最后一版,守卫立刻拦下、什么都没回滚,
所以原测试断言「失败后状态仍干净」成立。

我**没有**削弱那条守卫断言,而是让测试把真实的中间状态也断言出来
(federation 停在 7、8 待应用、其余模块不受影响),再 Up 恢复。

但这暴露一个运维风险:**被拒绝的降级不是原子的**。是否应该改成「请求范围内含不可逆里程碑
就整体拒绝、一个都不回滚」?那是迁移编排的语义变更,属你的决定,我没有擅自改。

## Active Work
等 `make ci` 结束。

## Completed Work
见 Overall Progress 勾选项。覆盖率 federation 包 90.4%(门禁 ≥90%)。

## Important Decisions
- D-A: connector key 存既有 `issuer` 列,不加列不加表。`securityProviderFingerprint` 已哈希
  issuer,换 key 自动让旧 identity link 失信;`UNIQUE(tenant_id, issuer)` 保证每租户一个同类。
- D-B: endpoint 编译进二进制,租户不可配。否则管理员改 profile endpoint 指向 + 保持 client_id
  即可冒用既有 identity link。
- D-C: 支持 OIDC discovery 的 provider(Google/Microsoft/Apple/Slack/LinkedIn/Line/Twitch/
  Zoom/Okta/Auth0/Keycloak/Casdoor/Logto…)不进本表,继续用 `oidc` 类型 —— ID token 验签 +
  auth_time 严格强于读 profile。有测试 `TestConnectorsDoNotShadowOIDCProviders` 冻结这条。
- D-D: 社交 provider **不得**用于重认证。plain OAuth2 无 `auth_time` 等价物,且社交方常静默
  复用已有会话。`StartIdentityOperation` 与 `VerifyIdentityOperation` 双重拒绝,
  绝不伪造 `AuthTime = now`。绑定(purpose=link)放行,新鲜度由单次授权码 + PKCE + replay claim 承担。
- D-E: 变化按「值 vs 形状」分层:值进 connector 表,形状进 `profileFlow` 实现。
  不把旋钮堆成配置语言。
- D-G: token 端点的客户端认证方式建模为 `clientAuth`(`clientSecretBasic` / `clientSecretPost`),
  用 OAuth 2.0 `token_endpoint_auth_method` 的原词;`tokenRequest` 只管传输形状。
  `tokenRequestQuery` 发 GET —— 把参数放进 URL 却 POST 空 body 会被 provider 以无法诊断的
  "invalid request" 拒绝。
- D-F: 出站 User-Agent 由 `runtime.Identity()` 生成,不写死产品名(架构门禁
  `TestSourceCarriesNoDeploymentIdentity` 曾抓到违规)。因此 `Headers` 字段无消费者,已删。

## Confirmed Constraints
- 不改既有 OIDC / SAML 行为(两者代码路径未动)。
- 三方言迁移语义等价;SQLite 不能 ALTER CHECK,走表重建 + `PRAGMA foreign_keys=OFF`。
- 没有邮箱的 connector 只能绑定不能自动开户(沿用既有 `ErrProvisioningUnverified`,未改)。

## Known Issues / Risks
- **connector 的 endpoint 与字段名未经真实 provider 验证**(本环境无外网)。结构与映射逻辑有
  测试保证,但某个 provider 的 URL 当下是否仍有效,需对照官方文档人工核对。
- `make generate` 在本环境跑不起来(buf 下载超时),**生成物漂移未验证**。未改任何 model,
  `git status` 也未显示 `*.gen.go` 变更,属间接证据。
- 桶 3 仍缺:alipay(RSA2 信封)、wecom、微信小程序、steam、dropbox、notion、vk。
- 会话中 `internal/iam/authn/captcha_turnstile.go:6` 曾被外部改动截断(`"errors"` → `"erro`),
  非本任务所为,已 `git checkout --` 恢复;原因未查明。

## Verification
Build: PASS / federation 包 Tests: PASS(覆盖率 90.4%)/ lint: PASS(0 issues)
fmt-check: PASS / schema-lock-check: PASS / architecture: PASS
internal/app: PASS / internal/integration/iam: PASS
tests/migration(三引擎 × 带/不带前缀): PASS
make ci: PASS(EXIT=0)
make test-performance: PASS
openapi-check 前置: PASS(已重新生成并提交到工作树)
frontend check:api-types + typecheck: PASS
make generate: 无法执行(无外网,buf 下载超时)
make test-mysql8: 无法执行(本机无 Docker)—— 静态的 schema-lock-check 已 PASS,
  且本次 MySQL 迁移只有 DROP CHECK + ADD CONSTRAINT,与已发布的 oauth/00007 同构
make test-performance: NOT_RUN(待 CI 结束)

## Next Action
本次交付收尾:全量验证通过后提交并推送到 IAM 分支。

推送后仍待办(不在本次范围):
1. 桶 3 的 alipay(RSA2 签名信封)/ wecom(应用 token 与人分开认证)—— 本环境无法对真实
   provider 验证(无外网、需商户私钥与企业凭证),故未纳入本次提交;扩展点已由 qq 证明可用。
2. 对照官方文档核对 24 行 connector 的 endpoint 与字段名,优先
   figma / coinbase / instagram / douyin(信心较低)。

## Last Checkpoint
2026-10-03 全部可执行门禁通过,等验收
