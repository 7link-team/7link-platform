# Task State

## Task
IAM Phase 10.2：新建租户时原子化完成 owner bootstrap（首个成员、Administrator 角色与授权、默认菜单）。

## Objective
按 `apps/backend-api/docs/iam-domain-migration-plan.md` §2.5.2，让 `POST /iam/tenants` 在同一个事务里
把租户交付给一个明确的 owner principal，消除「租户建成但没有任何成员、所有租户级接口因 membership
guard 全部不可达」的死锁；跨模块能力必须走窄事务能力边界，organization 不得直接写 core/identity 的表。

## Current Phase
VERIFICATION

## Current Step
功能与测试完成，`make ci` 第三轮运行中（前两轮均只卡覆盖率门禁，无功能失败）。

## Overall Progress
- [x] core 发布 `TenantBootstrapper`（`Bootstrap` + `IsUnclaimed`），事务由调用方持有
- [x] identity 发布 `TenantOwnerLookup`（事务内按 ID 解析 principal）
- [x] organization `CreateTenant` 增加 `OwnerPrincipalID`，在插入租户的同一事务内解析 owner 并 bootstrap
- [x] HTTP 请求体增加 `owner_principal_id`，新增两个 422 错误与三语言文案
- [x] core / identity / organization 三个包的直测补齐
- [ ] `make ci` 全绿
- [ ] 提交推送并通知 @ClaudeWEB 复核接口表
- [ ] §2.5.2 要求的「存量零成员租户」修复命令（`IsUnclaimed` 已实现，尚无调用方）

## Completed Work
- `internal/iam/core/tenantbootstrap.go`：`BootstrapTenantOn` 写入 owner membership、Administrator 角色、
  `tenant_administer` 授权、role member 和 15 条默认菜单；policy revision 与审计仍由调用方负责，
  保证一次建租户仍只有一次 revision bump。`TenantIsUnclaimedOn` 供修复命令判断租户是否无人认领。
- `internal/iam/identity/tenantowner.go`：`FindPrincipalOn` + `tenantOwnerLookup()` 能力方法
  （原本内联在 `ProvideServices` 里，提为方法以便直测，与 `bootstrapPrincipalStore()` 保持同一模式）。
- `internal/iam/organization/tenant.go`：owner 在事务内解析，refuse 语义为
  `ErrTenantOwnerNotFound` / `ErrTenantOwnerInactive`；成功时追加 `tenant_bootstrapped` 审计事件。
- 覆盖率：core 87.7% → 90.1%，identity 89.1% → 90.5%，organization 89.9% → 90.0%。

## Remaining Work
- CI 绿后按显式路径提交（暂存区里有他人的文档改名，禁止 `git add .` / `-A`）。
- §2.5.2 的存量租户修复命令。
- Phase 10.1 收尾：按用户 2026-09-05 决策，平台首个管理员走「启动读配置/环境变量，没有就建、有就跳过」。
- 10.3 待用户决策（见下）。

## Important Decisions
- 跨模块只走窄事务能力（`func(ctx, *gorm.DB, ...)`，Provide 阶段发布、Inject 阶段解析），
  organization 的单测因此用 stub 而非直接读 core/identity 的表。
- owner 在**事务内**解析：请求到提交之间被停用的 principal 不能仍然拿到租户。
- Administrator 只授 `tenant_administer` 一条权限；authorizer 已把它当租户级 override，逐条枚举只会漂移。
- 建成后该角色是普通租户角色，可改名、可扩权、可转授。
- 用户状态词汇是 `active` / `disabled` / `locked`（schema CHECK 约束），不存在 `suspended`。

## Confirmed Constraints
- 提交必须逐文件显式暂存；工作区有 7 个非本轮改动的文件等用户处置。
- migration 不写固定账号或默认密码（沿用上一轮结论）。
- 用户 2026-09-05 决策：Phase 10.1 采用配置/环境变量 bootstrap（方案 A），不采用 SQL 写死哈希。

## Known Issues / Risks
- 逐包 90% 覆盖率门禁余量极薄（organization 90.0%、core 90.1%），后续任何新增生产代码都要同批补测。
- `internal/iam/federation/state.go:70,95` 把品牌串 `"chaosplus:federation:state"` 当作 AES-GCM AAD，
  且同一字面量写了两处、没有常量。未上生产，现在改零代价；已向用户报备，待单独提交。
- 10.3 principal 归属未决：全局账号的 6 个接口（list/create/get/update/suspend/resume）挂在租户注册器上，
  其中 3 个却要 `platform:administer`，平台管理员与租户管理员**都不可达**。租户级的成员管理
  （`iam-create-tenant-member` / `iam-update-tenant-member`）已可用，不受此问题影响。

## Verification
Build: PASS（`go build ./internal/...`）
Tests: PASS（`go test ./internal/iam/{core,identity,organization}/...`）
Lint: 待 `make ci` 第三轮确认
Integration: 待 `make ci` 第三轮确认
Other: 冻结契约（`iam_http_mappings.txt` 211 条、`iam_http_guards.txt`、迁移表数）预期不受影响——
本轮只加了一个请求体字段，operation 集合未变；以 CI 结果为准。

## Last Checkpoint
2026-09-05 · PHASE 10.2 / VERIFICATION：租户 owner bootstrap 实现完成，三个包直测补齐并回到 90% 以上，
`make ci` 第三轮运行中；绿后提交推送并通知前端复核 `organization-create-tenant` 的新请求体字段。
