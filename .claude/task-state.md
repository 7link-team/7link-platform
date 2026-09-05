# Task State

## Task
IAM Phase 10 后端推进。10.1（平台超管 bootstrap + reset-admin-password）与 10.2（新租户同事务 owner 自举）
已完成推送；品牌 cookie 清理已完成推送。当前：§2.5.2 要求的「存量零成员租户」修复命令。

## Objective
在 10.2 之前创建的租户可能既没有成员也没有角色，任何 HTTP 路由都要求 active membership，
所以它们在产品里是不可达的死租户。提供一个只在「零成员且零角色」时才允许运行的 create-only 修复命令，
把 owner 补上，让这些租户可管理。

## Current Phase
IN PROGRESS（10.1/10.2/§2.5.2 与 4 条外部报障均已推送；10.3 等 @gankcode 拍板，10.4 有待澄清的范围问题）

## Current Step
@ClaudeWEB 报的 4 条后端缺陷已全部修完推送。下一步只剩 10.3（阻塞）与 10.4（阻塞）。

## Overall Progress
- [x] Phase 10.2（提交 24f2483）
- [x] Phase 10.1（提交 d253c5e）：`iam bootstrap-platform-admin` + `iam reset-admin-password`
- [x] 品牌 cookie 清理（提交 348a37f）
- [x] §2.5.2 存量零成员租户修复命令（提交 20bae43）
- [x] CLI flag 下划线/中划线在所有子命令一致（提交 5d7e9d0，@ClaudeWEB 报的第 4 条）
- [x] `allowed_return_urls` 改为 origin 精确 + 路径前缀（提交 1ecc9cb，@ClaudeWEB 报的第 3 条）
- [x] create/update 可选外键 schema 对称 + invitation 空值线上形状（提交 69187ab）
- [ ] 10.3 principal 管理权限语义（等用户明确拍板）
- [ ] 10.4 数据权限端到端（缺可挂载的租户级业务资源，等用户定目标）
- [ ] 10.5–10.11

## 本轮交付（§2.5.2）
入口：`7link-api iam repair-tenant-owner --config <c> --tenant-slug <s> --owner-login <l>`
一个事务内按序：查租户（按 slug）→ 查 owner（按 login 锁行，必须 active）→ `IsUnclaimed` 必须为真
→ `Bootstrap` 写 membership / Administrator role / grant / 默认菜单 → bump policy revision
→ 审计 `tenant_bootstrapped` 且 `Detail["repaired"]=true`。跨进程锁 `iam/repair/tenant-owner`。

新增/改动文件：
- `internal/iam/organization/tenantlookup.go`(+test)：新窄能力 `TenantLookup`，
  `Repository.FindTenantBySlugOn`，`Module.tenantLookup()`；`module.go` 的 `ProvideServices` 发布它。
- `internal/iam/core/contracts.go` / `tenantbootstrap.go`(+test)：`TenantBootstrapper` 增加第三个字段
  `BumpRevision`，配套 `Repository.BumpPolicyRevisionOn`。
- `internal/app/tenantrepair.go`(+test)：`RunTenantOwnerRepair`、`TenantRepairConfig/Request/Report`、
  `ErrTenantRepairConflict` / `ErrTenantRepairNotFound`。
- `cmd/7link-api/cmd/iam.go`(+`iam_repair_test.go`)：新增 cmd 内 `iamConfig` 组结构体承载整组 flag
  （对齐 migrate 组的写法），新增 `repair-tenant-owner` 子命令与退出码映射。

## Important Decisions
- 跨模块只走窄事务能力（`func(ctx, *gorm.DB, ...)`，Provide 阶段发布、Inject 阶段解析）。
- 修复命令是 create-only：只要租户已有任何 member 或 role 就拒绝，绝不改动已有归属。
- 修复写的表与正常建租户完全一致，所以「修过的租户」在状态上不可区分；差异只留在审计事件的
  `repaired=true` 上，这是事后追责唯一能找到的地方。
- 密码永不作为命令行参数（10.1 已确立）。修复命令根本不碰凭据。
- 退出码按运维下一步动作区分：3 已 bootstrap / 4 冲突 / 5 未迁移 / 6 暂时不可用 / 7 尚未 bootstrap。
- 不在代码里硬编品牌相关字符串，标识符里带品牌也算（CLAUDE.md §4）。

## Confirmed Constraints
- 提交必须逐文件显式暂存；工作区有若干非本轮改动的文件等用户处置
  （`.gitignore`、`README.md`、`pkg/interpreter/interpreter.go`、4 个已暂存的文档改名、未跟踪的 `docs/README.md`）。
- 前端目录归 @ClaudeWEB，禁止碰。
- migration 不写固定账号或默认密码。
- `tools/iam-preflight` 与迁移方案文档里的 chaosplus 路径是迁移来源，保留。

## Known Issues / Risks
- 逐包 90% 覆盖率门禁余量极薄（authn 90.1%、organization 90.0%、core 90.1%），新增生产代码必须同批补测。
- 10.3 principal 归属未决：6 个全局账号接口挂在租户注册器上，其中 3 个要 `platform:administer`，
  平台管理员与租户管理员都不可达。方案 A = 整体挪到平台注册器。已两次说明，等用户一句明确同意。
- 10.4 无处落地：方案文档要求把 `DataConstraint` 接到「真实业务资源」上，但仓库里唯一的业务模块
  `internal/todo` 既没有 `tenant_id` 列，`internal/todo/http.go` 的 9 个接口也全走裸 `huma.Register`，
  没有任何 authz 守卫。要么给 todo 加租户维度当样板，要么等真实业务模块，需用户定。
- `organization` 模块的 ID 字段一律 `maxLength:"128"` 无 pattern，`core` 模块一律
  `pattern:"^[1-9][0-9]*$"`。两套写法并存但各自内部自洽，未擅自统一。

## Verification
Build: PASS  Tests: PASS  Lint: PASS  Integration: PASS
`make ci` exit 0，总覆盖率 90.1%（最低 90.0%），core 90.1%、organization 90.0%。

## Last Checkpoint
2026-09-05 · 已推送到 69187ab：§2.5.2（20bae43）、CLI flag 归一化（5d7e9d0）、
return_url 前缀校验（1ecc9cb）、create/update schema 对称 + invitation 空值（69187ab），
每一项都跑过完整 `make ci`。
下一步：10.3 与 10.4 都卡在 @gankcode 的决定上，没有可以自行推进的后端工作。
