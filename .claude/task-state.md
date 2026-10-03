# Task State

> 已交付并提交:社交 connector(`bb89072` / `1c9b77d`)、webhook 模块(`b6d3b27`、D97)、
> device flow + token exchange(`8187c19`、D98)。

## Task
用户批准的四项补齐,一项一交付一提交:
1. ~~Webhook~~ 已交付 `b6d3b27`(D97)
2. ~~Device flow + Token exchange~~ 已交付 `8187c19`(D98)
3. ~~角色授予期限~~ 已交付 `9bdddd6`(D99)
4. **短信** —— 本次

## Objective
让 principal 可以有手机号,并且这个号码从一开始就被当作 PII 对待:匿名化会清掉它,
tombstone 闸门禁止匿名化之后再写回,审计链里不留明文。

## 分析结论(开工前实地核对)
- `iam_principals` 现有 `email`(非唯一,仅索引)+ `email_verified`。`login_name` 才是唯一身份。
  → 手机号镜像 email:非唯一 + 索引。号码不是身份,家庭共用号或号码回收都不该挡住开户。
- 匿名化的写入是 `identity/lifecycle.go:275` 一张 map,加两个字段即可覆盖。
- `iam_principals` 上有 `trg_iam_principal_tombstone_update`:对已匿名化的 principal
  **任何 UPDATE 都 ABORT**。所以手机号与邮箱受同一道 schema 级闸门,不需要新守卫。
- 通知已有出站链路:`iam_notification_outbox`(kind / recipient / 加密载荷 / 重试),
  `postNotification` POST 给外部通知服务,`kind` 无 CHECK 约束 → 新增一种 kind 不动 schema。
- 邮箱验证流程(`authn/verification.go`,259 行 + 仓储 + HTTP + 配置)把地址快照进凭证行,
  改地址即失效 —— 手机号验证必须保留这个性质。

## 分两段交付(各自独立可验证)
**Stage 1(已完成,待提交):号码本身 + PII 链路**
1. 迁移 identity 00008(三方言):`phone` + `phone_verified` + phone 索引;里程碑 `phone@8`
2. model + dbgen
3. 匿名化写入加 `phone: ""`, `phone_verified: false`
4. 设置/清除手机号(管理侧 + 本人),E.164 校验
5. principal 投影暴露号码
6. 测试(含匿名化后清空、tombstone 闸门拒绝写回)、冻结清单、openapi、前端类型

**Stage 2(下一次):验证码**
把邮箱验证凭证改成分渠道(channel + 号码列),复用同一套尝试次数/消费一次/快照绑定,
新增 `phone_verification` 这种 outbox kind,端点限流。不复制一套流程。

## Confirmed Constraints
- 手机号是 PII:任何新增的日志、审计明细、webhook 投影都不得带它。
- 不接任何短信厂商:出站仍是既有的通知服务 POST。
- 邮箱的 CHECK/索引形状是模板,手机号照抄,不另发明。

## Known Issues / Risks
- SCIM 的属性映射不在本次范围(provisioning 另有自己的 resource 映射)。
- 手机号格式校验只做 E.164 形状,不做运营商/国家号段校验。

## Verification
Build: PASS / `internal/...` + `pkg/...` 全部测试: PASS
`tests/architecture`: PASS / `tests/migration`(三引擎 × 带/不带前缀): PASS
make ci: PASS(EXIT=0;总覆盖率 91.1%,identity 模块 90.8%,openapi 匹配,前端契约过)
变异验证: 匿名化清空号码、验证标志随字段可见性 —— 两处都可独立变红

## Next Action
Stage 1 已交付。Stage 2(验证码)未开工,第一步是把
`iam_email_verification_tokens` 改成分渠道:加 `channel` 与号码列,复用同一套
尝试次数/一次性消费/联系方式快照绑定,再加 `phone_verification` 这种 outbox kind
与端点限流。**硬约束**:号码一旦能用于找回或登录,改号必须同时吊销会话并提升
credential version(Stage 1 故意没做,因为那时号码还什么都换不到)。

另有一项等你定夺(不在四项范围内,我没擅自做):device flow 的验证页面。
后端已完整合规(未配置验证页时直接报配置故障),但页面本身属控制台工作线。
建议与「角色授予期限的编辑界面」一起做,控制台一次对接两处新能力。

## Last Checkpoint
2026-10-04 第 4 项 Stage 1 全量门禁通过,待提交
