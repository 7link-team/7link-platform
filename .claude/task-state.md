# Task State

> 已交付并提交:社交 connector(`bb89072` / `1c9b77d`)、webhook 模块(`b6d3b27`、D97)、
> device flow + token exchange(`8187c19`、D98)。

## Task
用户批准的四项补齐,一项一交付一提交:
1. ~~Webhook~~ 已交付 `b6d3b27`
2. ~~Device flow + Token exchange~~ 已交付 `8187c19`
3. **角色授予期限** —— 本次
4. 短信 —— 未开始,实为 `iam_principals` 加手机号列 + 接生命周期,不是接厂商

## Objective
让管理员能把一个租户级角色授予**带上截止时间**,并让每一处"把授予读作权限"的判定都尊重它。

## 分析结论(开工前的实地核对,与开工前的假设有出入)
四种角色到达 principal 的方式(`core/query/authorization.go` 的 `effective_roles` CTE):

| 来源 | 表 | 期限列 | 判定是否已过滤 |
|------|-----|--------|----------------|
| 永久成员 | `iam_role_members` | **无** | — |
| 临时授予 | `iam_temporary_role_grants` | starts_at / ends_at(NOT NULL) | 已过滤 |
| 用户组 | `iam_group_role_bindings` + 组成员 | 绑定本身无;组成员有 starts_at/ends_at | 组成员已过滤 |
| 实体作用域 | `iam_role_bindings` | `expires_at`(可空) | 已过滤(authorization.go:309/345) |

所以**不是**"读取点都没过滤"(开工前的判断),而是:四条路里只有「永久成员」这一条没有期限可言。
`governance/review_grants.go:31` 给它写的是 `NULL AS grant_expires_at` —— 那一列早就给它留好了。

## 设计(D99,见 decisions.md)
- `iam_role_members` 加可空 `expires_at`。NULL = 无期限,等于现存每一行当下的含义。
- **不复用 `iam_temporary_role_grants`**:那张表是 governance 审批出来的授予,`source_type`/
  `source_id` 指向访问申请,两个时间列都 NOT NULL,且每条有自己的 id 以便单独撤销与按
  "temporary" 归类。带截止时间的直接授予仍是 (tenant, role, principal) 这同一个关系加了个界,
  用那张表会让同一个关系有两种身份,"谁持有这个角色"要变成 union。
- **读时过滤,不加清理器**:判定本来就按请求的 `now` 计算,四条路里三条已经这么做;清理器
  会让正确性取决于"任务跑过没有"。过期行留着,也正是"显示为已过期"的前提。
- **可见性走既有的 reviewable-grants 端点**(它已有 `grant_expires_at` 列和 `permanent` 类型),
  不改 `GET /roles/{id}/members` 的 `[]guid.ID` 契约 —— 那是行管理视角,过期行照样在。
- 作用域:只做租户级授予。`iam_group_role_bindings` 不加自己的期限 —— 组成员已有
  starts_at/ends_at 且已被过滤,那就是租户给"组带来的角色"设界的方式。

## 判定点清单(每处都有独立行为测试,且都做过变异验证:删掉谓词 → 对应用例立刻变红)
必须过滤(把授予读作权限)—— 全部完成:
- [x] `core/query/authorization.go` `effective_roles` 的 role_member 分支
- [x] `core/query/authorization.go` durable administrator 计数 —— 用 `IS NULL`
      而非「尚未过期」:守卫问的是"之后还有没有管理员",会过期的答案不算
- [x] `core/authorizer_system.go` 系统角色判定(独立语句,无 CTE,需自己的证明)
- [x] `core/platform.go` 平台管理员列表
- [x] `core/repository_admin.go` `ListMemberRoleIDs`
- [x] `core/review_grant.go` `roleGrantedOn`
- [x] `core/lifecycle.go` 关户前的平台权限拦截
- [x] `core/member_authorization.go` 缓存判定的到期(否则缓存比授予活得久)
- [x] `governance/review_grants.go` permanent 分支:吐出期限 + 过滤已失效

不过滤(行管理视角,有意为之):
- `core/repository.go` 增删与 `ListMembers`、`core/pagination.go` 分页列表
  —— 失效的行还在,撤销仍然有效
- `core/system_access_http.go` `systemMemberView` —— 控制台据此渲染开关,
  隐藏失效行会让开关变成空操作
- `core/entity.go` 删实体前清点绑定 —— 失效的行照样会被外键牵连
- `tenantbootstrap.go` / `federation` / `organization/invitation` 的写入 —— 不带期限 = 永久

## Completed Work
- 迁移 00018(三方言:可空 `expires_at` + 镜像 role_bindings 的 subject 索引)
- 里程碑 `role-member-expiry@18` + model + dbgen
- 授予 API 接受可选 `expires_at`(body 为指针 → 可缺省,老调用方行为不变)
- PUT 语义:整体置为请求所述;读后写而非 upsert(upsert 的影响行数各方言含义不同,
  而 `changed` 必须真的表示"授予变了")
- 期限进审计明细(`expires_at`),审查端点吐出它
- 9 处判定点 + 11 个新行为测试,逐一变异验证
- 冻结清单:core 里程碑清单与计数、三处 `milestones[iamcore.Name]`、openapi、前端类型

## Confirmed Constraints
- 不改既有四条授予路径的语义,只给第一条加界。
- MySQL 一律 `DATETIME(3)`;索引镜像 `iam_role_bindings` 的
  `(tenant_id, principal_id, ..., expires_at)` 形状。
- 热路径 CTE 每加一个谓词都要看性能基线。

## Known Issues / Risks
- **这是四项里最危险的一项**:`effective_roles` CTE 是每次鉴权都走的路径,
  判定点跨 core / governance / organization / federation 四个模块。
- 一旦某个判定点漏掉谓词,后果是"过期的角色仍然生效"——静默的权限泄漏,
  所以每个点都必须有自己的行为测试 + 变异验证,不能只靠 CTE 的 golden SQL。

## Verification
Build: PASS / `internal/iam/...` + `internal/app`: PASS
变异验证: 10 处谓词全部可独立变红
make ci: RUNNING / make test-performance: RUNNING

## Next Action
`make ci` 与 `make test-performance` 结束后:若 PASS 则以单个提交交付并推送,
然后开第 4 项(短信 —— `iam_principals` 加手机号列 + 接匿名化/撤回,属 PII)。
若性能基线退化,先查 `effective_roles` 的 role_member 分支新谓词是否吃到了
00018 建的 `(tenant_id, principal_id, expires_at)` 索引。

## Last Checkpoint
2026-10-04 第 3 项实现与变异验证完成,等全量 CI 与性能基线
