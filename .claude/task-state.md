# Task State

> 已交付并提交的前序任务:社交 connector(26 个,`bb89072` / `1c9b77d`)、
> webhook 模块(`b6d3b27`)。各自的决定见 `.claude/decisions.md` D94–D97。

## Task
用户批准的四项补齐,一项一交付一提交:
1. ~~**Webhook**~~ —— 已交付 `b6d3b27`(D97)
2. **Device flow(RFC 8628)+ Token exchange(RFC 8693)** —— 本次(D98)
3. 角色授予期限 —— 未开始,**最危险**:读取点跨 5 个模块,含 `core/query/authorization.go` 热路径
4. 短信 —— 未开始,实为 `iam_principals` 加手机号列 + 接生命周期,不是接厂商

## Objective
`oauth` 模块补两种 grant:没有浏览器的设备(电视/CLI)能登录;服务能换一张"代某人"的
令牌。两者都挂在 `service.go` 的 grant switch 上,复用既有的 client 认证、assurance
传递、refresh token 存储与审计路径。

## Current Phase
TESTING —— 实现与模块级门禁全过,等 `make ci` 第三轮结果。

## Current Step
`make ci` 运行中(前两轮分别被 schema-lock 表数量 75→77、oauth i18n 冻结 key 清单拦下,
两处都已修)。结束后跑 `tests/migration` 与 `make test-performance`,再提交推送。

## Overall Progress
- [x] 迁移 00011(三方言):`iam_oauth_device_authorizations` + `iam_oauth_exchange_grants`
- [x] `model/model.go` 两行 + dbgen + `query/table.go` BindNames
- [x] `device.go`:用户码字母表(剔除 0/O 1/I/L 2/Z 5/S 8/B)、双码哈希存储、
      服务端强制轮询间隔、批准者归属与 assurance 传递、消费与 refresh 存储同一事务
- [x] `exchange.go`:`subjectPermitted`(精确优先于前缀,空前缀谁都不覆盖,裸 `*` 入口拒绝)、
      scope 取 grant 的而非 client 的、不发 refresh token
- [x] `http_device.go`:6 个端点(device_authorization 公开;其余 tenant 守卫 + owned-resource 删除)
- [x] `http.go`:`supportedGrantTypes` 收口 + RFC 错误码映射
- [x] 测试:`device_test.go`(16)、`exchange_test.go`(15)、`http_device_test.go`(10)
- [x] oauth 包覆盖率 90.4%(门禁 ≥90%)
- [x] 变异验证 7 处守卫(public client 两端、grant scope 上限、租户成员、
      两处故障/拒绝分离、device 请求的租户归属)
- [x] 登记 7 处冻结清单:oauth 路由清单、里程碑计数、error mapper 清单、
      i18n declaredKeys、app 的 mappings/guards/count、schema-lock 表数量
- [x] `DeviceConfig` 导出并接进 `internal/app`(`oauth.device.*` 三个键)
- [x] 重新生成 `docs/openapi.json`
- [ ] `make ci` / `tests/migration` / `make test-performance`
- [ ] 提交推送

## 本次自查发现的七个真缺陷(都已修)
1. **`validateClient` 的 grant 白名单没加新 grant** —— 管理 API 根本建不出能用 device
   或 exchange 的 client,新功能从公开面无法到达。
2. **token exchange 对 public client 无防护** —— public client 不持有秘密,其标识等于
   公开值,一行 grant 就会让任何人代被覆盖的 principal 签发令牌。两端都加守卫
   (`validateClient` 创建侧 + `tokenExchange` 使用侧)。
3. **读失败被折叠成拒绝** —— 违反既有决定「A Failed Read Is Not A Refusal」。
   设备轮询读失败会回 `invalid_grant`,而符合规范的设备收到它会丢弃设备码重开流程;
   exchange 读失败会读作"委派已撤销"。改为只折叠 `gorm.ErrRecordNotFound`。
4. **`deviceError` 未登记进 error mapper 冻结清单** —— 该清单存在的理由就是新 mapper
   不能绕过"下层已选的状态码必须存活"这条规则。
5. **两处错误码不合规** —— `unsupported_token_type` 映射成了 `invalid_target`
   (RFC 8693 里 `invalid_target` 专指 resource/audience 不可接受),`unauthorized_client`
   用了 403(RFC 6749 §5.2:token 端点除 `invalid_client` 外一律 400)。
6. **device 查看/批准没按租户过滤** —— 端点有 `{tenant_id}` 守卫,服务层却只按用户码查。
   另一个租户的成员能把别人的设备请求批掉:令牌发不出去(`activeMember` 兜底),但请求
   已被消费,成了跨租户的拒绝服务,URL 里的租户段形同装饰。两处都加了 `tenant_id` 谓词。
7. **未配置验证页时返回空 `verification_uri`** —— RFC 8628 要求该字段必须有值。空值等于
   给设备一个"看起来能走、实际没人能完成"的流程。改为报配置故障(`server_error`),
   并顺手把 `verification_uri_complete` 的用户码做了 URL 转义。

## Important Decisions
- D98(见 decisions.md):委派是写下来的三元组(tenant, client, subject prefix)+ 它自己的
  scope,不是 client 上的开关;前缀匹配在 Go 里做而不是 SQL `LIKE`;不发 refresh token。
- 设备码与用户码都只存 SHA-256,和授权码同构 —— 读库不能兑换。
- 轮询间隔服务端强制(`last_polled_at` + `interval_seconds`),不信设备自报。
- 迁移 00011 **可逆**:只建两张表,回滚丢掉的是待批准请求(本就 10 分钟过期)
  与可重新写下的委派,不符合 `Irreversible` 的"迁移过的东西无法重建"门槛。
- 不实现 RFC 7591 动态客户端注册:此处每个 client 都属于某个必须授权它的租户。
- subject token 只接受本部署签发的 access token;SAML/外部 JWT 一律
  `unsupported_token_type` —— 接受验不了的断言等于信任来源不明的断言。

## Confirmed Constraints
- 不改既有 authorization_code / refresh_token / client_credentials 行为(路径未动,
  只在 grant switch 加两个 case、把硬编码白名单收口为 `supportedGrantTypes`)。
- MySQL 一律 `DATETIME(3)`,不用 `TIMESTAMP`。
- 表名只在 `query/table.go` 的 `BindNames` 拼接。

## Known Issues / Risks
- `make test-mysql8` 本机无 Docker 跑不了;静态 schema-lock 已过,00011 的 MySQL 侧
  只有 CREATE TABLE + CHECK,与已发布的 oauth/00008 同构。
- `make generate` 本环境 buf 下载超时;未改 proto,`*.gen.go` 变更仅来自 dbgen(已跑)。
- 设备验证页(`oauth.device.verification_uri` 指向的前端页面)**尚无实现** ——
  后端返回的 `verification_uri_complete` 当下指向一个不存在的页面。属前端工作,
  不在本次后端范围;留空则响应里不含该字段,流程仍可用(人工拿用户码调 API)。

## Verification
Build: PASS / `internal/iam/oauth`: PASS(覆盖率 90.4%)
`internal/app`: PASS / `tests/architecture`: PASS / i18n: PASS
lint: PASS(0 issues)/ fmt: clean / schema-lock: PASS(77 表)
make ci: RUNNING(第三轮)
tests/migration: NOT_RUN
make test-performance: NOT_RUN

## Next Action
`make ci` 结束后:若 PASS 则跑 `go -C tests test ./migration -count=1` 与
`make test-performance`,两者过后以单个提交交付(含 D98 与本文件),推送到 IAM 分支;
若 FAIL 则按失败门禁定位,不要绕过门禁。随后开始第 3 项(角色授予期限),
第一步是把 5 个模块的角色绑定读取点列成清单并逐一变异验证。

## Last Checkpoint
2026-10-04 第 2 项实现完成,模块级门禁全过,等全量 CI
