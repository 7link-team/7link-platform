# Task State

## Task
IAM 域全量迁移：chaosplus `apps/server` → 7link-platform `apps/backend-api`

## Objective
按 `apps/backend-api/docs/superpowers/plans/2026-09-02-iam-domain-migration.md` 将 9 个 IAM 域模块与
63 张表全功能迁入 gorm/gorm-gen/goosex/pkg-module 架构，修复源 SQL 缺陷，并通过
PostgreSQL、MySQL、SQLite 真实迁移与 `make ci` 门禁。

## Current Phase
IMPLEMENTATION

## Current Step
Phase 1 / Task 1.3：建立 9 个自治 IAM 模块的三方言 migration source 与 63 表契约。

## Overall Progress
- [x] 深度分析与迁移方案 v8
- [x] 两轮独立评审问题回收并修订方案
- [x] 两机环境 / Docker / collation / entity type 决策闭环
- [x] Phase 0 前置修复、扩展包、端口图与依赖评审
- [ ] Phase 1 九个模块自治迁移源、里程碑 DAG 与三引擎验证
- [ ] Phase 2–8 Go 模块迁移
- [ ] Phase 9 集成与验收

## Active Work
Task 0.P/0.0/0.1/0.2/0.3/0.4/0.5、Task 1.0/1.1/1.2 已实现；迁移计划为 v8，
`internal/iam/` 作为域命名空间，9 个子模块各自拥有业务、model/query 与 SQL，取消集中 schema
和 feature 分包。Task 1.3 已完成 identity、authn、oauth、audit、core 五个自治迁移源（36/63 表）；
63 表所有权集合已闭合，当前语义重写 organization/governance/provisioning/federation。

## Completed Work
- 已从任务记录与实际仓库恢复断点；工作树在本轮开始时干净。
- 新增 `make iam-preflight`：自动解析 Mac/Windows 源路径、固定 source tree、核对三方言
  63 表集合、处理 Windows CRLF、生成机器本地 `.temp/iam-env.mk`。
- Windows 远端 Docker 已安装并启用：`root@10.0.0.100`，Server 26.1.5；专用 SSH key
  批处理登录通过，不在仓库保存密码。
- 固定 MySQL 8.4 image digest；真实启动 MySQL 8.4.11，固定 `sql_mode`，Windows 到随机远端
  端口 TCP 连通并执行查询成功，临时验证容器已删除。
- Windows `make build` 的目录创建已改为 PowerShell，Mac 继续使用 POSIX `mkdir -p`。
- R16 已确定：业务名称 `lower(trim)` 且不折叠重音；枚举、协议标识符、哈希/密钥、
  WebAuthn handle 使用 binary collation。
- R9'' 已由源代码确认：实体存在关系时本就不能修改 type；复合外键不新增产品限制。
- Task 0.0：一次迁移只捕获一个 writer；迁移状态预检查不再推进 writer 轮询。
- Task 0.1：SQLite DSN 默认注入 `_foreign_keys=on`；显式关闭、冲突或非法值在启动时拒绝。
- 回归测试覆盖双 writer 落点、迁移状态落点、三条池连接 PRAGMA、孤儿写入与 DSN 边界。
- Task 0.2：在 `pkg/extension/policyx` 移植 condition/member rule 纯逻辑；在表拥有方
  `internal/iam/core` 新增带部署前缀的 GORM revision store，保留 `Current`/`Lock`/`Bump`
  语义，原子 upsert 并发递增不丢更新，`updated_at` 使用 UTC 毫秒真实时间与 NULL 未发生语义。
- Task 0.2 聚焦测试通过 `-race`；policyx 覆盖率 91.2%，core revision store 97.4%；并发
  不丢更新与双事务锁测试均已通过反向变异验证。
- Task 0.2 deep-review 发现并修复真实时间列冲突、模块所有权、错误文案、canonical 字节契约
  和锁测试缺口；复审无剩余发现。缺失非资源认证事实的否定语义按源明确兼容契约保持不变。
- Task 0.3：在 `pkg/extension/authn` 与 `pkg/extension/authz` 移植 8 个源文件；Registrar 使用消费侧
  tenant/platform/entity 窄接口，不新增第十个 IAM 模块或中央 port 包；两包已登记覆盖率。
- Task 0.3 deep-review 按类修复 JWKS 刷新竞态/放大/轮换恢复、受限远端响应、认证错误与 CSRF
  语义漂移、OpenAPI 认证和 selector 契约；类级测试均完成反向变异验证与复审。
- Task 0.4：移植 `webauthnx`、`secretx`、`passwordx`，在 `internal/iam/audit` 根包冻结
  `DomainEvent`/`Appender`，并为三方言 `operation_logs` 增加 append-only 迁移。
- Task 0.4 deep-review 修复 Argon2 PHC 超长输入分配、Linux FIFO 替换阻塞和 MySQL 半回滚
  重试问题；聚焦竞态/变异、真实 Linux、MySQL 8.4 恢复矩阵及依赖漏洞复审均通过。
- Task 0.5：按 provider 根包冻结 13 项跨模块能力，完整记录 TOTP/SAML/SCIM/验证码/MinIO/Redis
  的源版本、职责与即时引入时机；未引入中央 port/schema 或第二套依赖注入模式。
- Task 0.5 deep-review 修复源方法遗漏、接口删除漏检、能力所有权/alias 漏检、DTO 字段与 JSON tag
  漂移、`MembershipRule` Base64 序列化及不准确 GoDoc；类级门禁完成反向变异验证，复审无新发现。
- Task 1.0：将 `OpenPostgres`/`OpenMySQL`/`OpenSQLite` 提取到测试模块的 `internal/testdb`；重型
  embedded 引擎未进入主模块，SQLite DSN 对三条并行连接启用外键，初始化失败路径立即登记 cleanup。
- Task 1.0 deep-review 修复 PostgreSQL 空闲端口 TOCTOU 的有限重试、过宽错误分类和测试失败时
  借出连接泄漏；SQLite DSN 与端口分类不变量完成反向变异验证，复审无新发现。
- Task 1.1：参数化验证 todo/dlock/wuid/audit 在 SQLite、go-mysql-server、嵌入式 PostgreSQL
  上的 up/幂等/down/up、`sl_` 与 21 字符最大前缀、无裸表/双前缀和标识符长度；README 已明确
  go-mysql-server 不替代真实 MySQL 8 门禁。聚焦三引擎测试通过，提交 `d50bca4` 已推送。
- Task 1.2：新增可选 `MigrationMilestoneProvider`，应用迁移器按稳定 DAG 拓扑执行 up、按反向
  拓扑执行 down，并拒绝仍有已开始依赖节点的指定模块回滚；普通 migration source 行为不变。
- Task 1.2 状态门禁同时校验节点唯一/依赖存在/无环/版本递增、里程碑与 embedded source 对齐、
  Goose history 未分叉，以及所有已开始节点的跨 source 依赖已完成；状态报告按模块聚合并显示
  最后完整 milestone。三引擎 A↔B FK 套件及三类反向变异通过。
- 计划 v5 已冻结 9 模块 source manifest、目标 model/query/dbgen、bun→gorm 注意项、端口、
  tenant 负例和逐模块门禁；不再把 `internal/iam` 拆成三个半成品阶段。
- 从固定 source tree 展开 200 条 REST 三元组，文档与源代码集合对账 200/200、无缺失、
  无额外、OperationID 全局唯一。
- 计划 v6 的局部 Ports-and-Adapters 与 v7 direct-feature 方案均已废弃；v8 按 `CLAUDE.md`
  和 `internal/todo` 固定 9 个 IAM 域自治模块，跨模块契约直接归 provider 根包。
- 源 `modules/iam/sql` 的 36 表按实际职责拆为 core 13、authn 13、identity 3、oauth 4、audit 3；
  加 organization 14、governance 4、provisioning 5、federation 4，共 63 表、9 个 migration source。
- Task 1.3 identity：三方言 3 表自治迁移完成，真实时间、NULL 缺省时间、binary collation、原始
  FK 语义与源行注释已落实；三引擎 up/幂等/down/up、普通/`sl_`/最大前缀验证通过。
- Task 1.3 authn：三方言 13 表、9 个迁移里程碑完成，11 个 principal FK 归入 constraints；
  identity→authn 三引擎双前缀 DAG、孤儿拒绝、级联删除、安全回滚与恢复验证通过。
- Task 1.3 oauth：三方言 4 表、5 个迁移里程碑完成；保留源 FK 契约并加强 consent 的 tenant/client
  一致性。完整跨模块 DAG 测试等待 organization 语义迁移完成后接入。
- IAM 所有权契约已改为逐模块精确表名集合；identity/authn/oauth 的 20 表契约通过，完整 63 表门禁
  已在 audit/core 完成后转绿，9 个模块的三方言集合相等、互斥且并集恰为 63 表。
- Task 1.3 audit：三方言 3 表、2 个迁移版本完成；平台 tenant 0、NULL 可选主体/目标、真实时间、
  链序列/哈希唯一性、retention 范围和前缀化 append-only 触发器均通过双前缀三引擎验证。
- Task 1.3 core：三方言 13 表、10 个迁移版本完成；角色/实体 name_key、菜单 NULL route、实体根/子
  同级唯一、MySQL 安全 parent key、type 复合外键、RESTRICT、NULL 时间窗与 CHECK 通过三引擎验证。

## Important Decisions
- 目标为全量、全功能迁移；SAML/SCIM 不允许长期 Disabled。
- `internal/iam/` 只作命名空间；core/authn/identity/oauth/organization/audit/governance/
  provisioning/federation 是 9 个自治模块，各自拥有 SQL、版本表和跨模块 FK（D10）。
- 多写者是现有正式契约；Task 0.0 只修同一次迁移的 writer 句柄漂移，不禁用多写者。
- 动态菜单多语言不属于本次迁移；保持 63 表，不新增第 64 张表（D7）。
- 不局部引入 Ports-and-Adapters，不设中央 port/schema，不按 feature 机械拆包（D10 取代 D3/D9）。
- 只支持已确认的原 Mac 与当前 Windows 两机拓扑；源 tree 变化必须先复审。

## Confirmed Constraints
- 三方言 DDL 全部前缀化、真实时间类型、无 0 哨兵、FK/guid 使用 BIGINT。
- 每个新增受控模块覆盖率 ≥90%，阶段结束 `make ci` 必须通过。
- 不迁移存量数据；不缩减现有 IAM 域功能。
- 实现按依赖顺序推进；阶段评审按 deep-review 使用隔离只读 subagent，并由主执行者亲自证伪。
- 每次换机或恢复任务先运行 `make iam-preflight`；后续只消费生成的路径与 Docker transport。

## Known Issues / Risks
- 多写者 routing mode 与读副本接线仍是独立架构决策，不在 Task 0.0 中扩大范围。
- Mac 上的 `make iam-preflight` 与 `make ci` 尚未在本轮实跑；回到 Mac 后先完成该检查。
- MySQL append-only 半回滚恢复矩阵已在真实 MySQL 8.4 手工验证，但默认 `make ci` 仍使用
  内存 MySQL；真实引擎 DDL 隐式提交场景尚未形成持续集成门禁。
- 本任务约 6 万行，必须按 Phase 分批实现、验证和留档。

## Verification
Build: PASS（Task 1.2 Windows `make ci` build；Task 0.4 Linux CGO-disabled cross-compile）
Tests: PASS（Task 1.2 主模块全量 + 独立 tests module + 完整 `make ci`）
Lint: PASS（Task 1.2 固定版本 golangci-lint / `make ci`，0 findings）
Coverage: PASS（Task 1.2 总体 91.3%，全部受控包 ≥90%）
Integration: PASS（`tests/goose`、`tests/internal/testdb`、完整 `tests/migration`；Task 1.2 A↔B FK；
Task 1.3 identity→authn DAG、audit append-only/retention、core R5''/R9''/R16 三引擎双前缀生命周期）
Other verification: `git diff --check` PASS；source tree/三方言 63 表 PASS；REST baseline 与
源代码集合相等（200/200）PASS；远端 Docker 26.1.5 PASS；MySQL 8.4.11 启动、查询、
Windows TCP 连通及 append-only Up/Down 部分状态恢复矩阵 PASS；Linux FIFO 真实文件系统测试 PASS。

## Next Action
继续 Task 1.3：从 organization 开始语义重写四个已机械复制的模块，先把 14 表的真实时间、NULL、
name_key、CHECK/外键与 7 条 core 跨模块约束改写完成，再接入 core↔organization DAG 生命周期。

## Last Checkpoint
2026-09-02 · IAM 分支 Task 1.3 阶段检查点：identity/authn/oauth/audit/core 五个自治 migration
source 的 36/63 表已完成语义改写；完整 migration 测试与 63 表/9 source 所有权门禁通过。
下一步重写 organization，并验证 core↔organization 与 oauth→organization 里程碑 DAG。
