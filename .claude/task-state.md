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
TESTING —— 实现与前端完成,等最后一轮全量 CI(authn 覆盖率是唯一悬着的门禁)。

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
Build / 架构门禁 / 三引擎迁移 / 真 MySQL 8 迁移+计划: PASS(MySQL 延迟阈值超标是本机,
执行计划节点类型集合前后一致,阈值未动)
前端 typecheck + lint + 164 测试: PASS
make ci: RUNNING(第 16 轮)—— `internal/iam/authn` 覆盖率门禁(≥90%)是唯一悬着的项

## Next Action
读 ci16 的 `module coverage: internal/iam/authn`;不足就用能到达的性质补测试
(故障注入、审计原子性、入参守卫),不靠放宽断言。然后跑 tests/migration +
tests/architecture 与 make test-mysql8(都在 00020 迁移之后),通过即提交推送。

## Last Checkpoint
2026-10-04 手机号登录 + 短信第二因子实现完成,两个 MySQL 缺陷已修,等 ci16
