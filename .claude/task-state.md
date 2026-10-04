# Task State

> 已交付并提交:社交 connector(`bb89072` / `1c9b77d`)、webhook 模块(`b6d3b27`、D97)、
> device flow + token exchange(`8187c19`、D98)。

## Task
用户批准的四项补齐,全部交付:
1. ~~Webhook~~ `b6d3b27`(D97)
2. ~~Device flow + Token exchange~~ `8187c19`(D98)
3. ~~角色授予期限~~ `9bdddd6`(D99)
4. ~~短信~~ `931fdd3`(D100,号码+PII)+ 本次(D101,验证码)
外加本次:控制台三处接线(设备批准页、角色期限编辑、手机号验证入口)

## Objective
把四项的后端能力接到人能用的地方:设备批准页、角色期限编辑、手机号验证码与验证入口。

## Current Phase
TESTING —— 实现完成,等最后一轮全量 CI。

## Completed Work
**后端(第 4 项 Stage 2)**
- 迁移 authn 00019:`iam_phone_verification_codes`(principal_id 作主键 = 每人一条活码,
  号码与验证码同存),里程碑 `phone-verification@19`
- `phone_verification.go`:发码/验码;码只属于它被发往的号码,号码变了即失效
- **不复用邮箱凭证表**:邮箱凭证的主键是链接令牌,手机没有链接,塞进去要为
  「本来没有主键的东西」编一个主键。共享的是生成器/次数上限/出站 outbox/审计形状
- `PrincipalStateStore.VerifyPhone` 连接器(列属 identity,写入必须走它),谓词带号码
- 擦除路径带走这张表,`PrincipalErasureVersion` 16 → 19
- `/me` 报出 `phone`/`phone_verified`,与邮箱同受字段策略管辖
- `GET /roles/{role_id}/member-grants`:成员列表只有 ID,看不到到期时间,
  所以**新增**一个读取端点而不改既有响应形状

**控制台**
- `/admin/:tenantID/device` 设备批准页(带 `?user_code=` 免手输)
- 角色成员行内联到期时间控件(PUT 整体置换语义,留空即不限期)
- 安全页手机号区块:发码 → 填 6 位码

## 本轮抓到的真 bug
**尝试计数写在事务内,而拒绝以 error 退出事务 —— 计数被一起回滚**,
猜 5 次后正确的码依然可用。邮箱流程正是把计数放在事务外才避开这点,
现在这条理由写进代码注释与 D101。测试先红才发现。

## 变异验证的发现
手机验证的四个性质(次数上限、过期、号码快照、一次性)都是**双重强制**
(Go 判一次 + SQL 谓词判一次),单去一半测试不红,必须两半都去。
其中「一次性」原测试实际是靠 `phone_verified = false` 谓词挡住的,
并没真正证明一次性 —— 已改为直接观察 `consumed_at`。

## Confirmed Constraints
- 手机号是 PII:审计明细只记「变了」,不记号码;新表进擦除清单。
- **号码一旦能用于找回或登录,改号必须同时吊销会话并提升 credential version**。
  当下验证只是记录证明,号码还换不到任何东西,所以尚未吊销(D100/D101 都写明)。
- 不接任何短信厂商:出站仍是既有通知服务的 POST,只多一种 kind。

## Verification
Build / 后端全部测试 / 架构门禁 / 三引擎迁移: PASS
前端 typecheck + lint + 164 测试: PASS
性能基线:重新生成**并重新校验**,执行计划节点类型集合前后一致(无访问路径退化)
make ci: RUNNING(最后一轮)

## Next Action
CI 过后提交推送。四项范围内无遗留。
后续可做但不在四项内:手机号登录 / 短信 MFA(此时才需要按上述约束吊销会话)、
RFC 7591 动态客户端注册(当下每个 client 都属于必须授权它的租户,故未实现)。

## Last Checkpoint
2026-10-04 四项后端全部交付,控制台三处接线完成,等最后一轮 CI
