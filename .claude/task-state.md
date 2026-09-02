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
Phase 1 / Task 1.1：建立现有 4 个 migration module 的真实三引擎验证套件。

## Overall Progress
- [x] 深度分析与迁移方案 v8
- [x] 两轮独立评审问题回收并修订方案
- [x] 两机环境 / Docker / collation / entity type 决策闭环
- [x] Phase 0 前置修复、扩展包、端口图与依赖评审
- [ ] Phase 1 九个模块自治迁移源、里程碑 DAG 与三引擎验证
- [ ] Phase 2–8 Go 模块迁移
- [ ] Phase 9 集成与验收

## Active Work
Task 0.P/0.0/0.1/0.2/0.3/0.4/0.5 与 Task 1.0 已实现并通过 Windows 完整门禁；迁移计划为 v8，
`internal/iam/` 作为域命名空间，9 个子模块各自拥有业务、model/query 与 SQL，取消集中 schema
和 feature 分包。下一步扩展真实三引擎迁移验证套件。

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
- 计划 v5 已冻结 9 模块 source manifest、目标 model/query/dbgen、bun→gorm 注意项、端口、
  tenant 负例和逐模块门禁；不再把 `internal/iam` 拆成三个半成品阶段。
- 从固定 source tree 展开 200 条 REST 三元组，文档与源代码集合对账 200/200、无缺失、
  无额外、OperationID 全局唯一。
- 计划 v6 的局部 Ports-and-Adapters 与 v7 direct-feature 方案均已废弃；v8 按 `CLAUDE.md`
  和 `internal/todo` 固定 9 个 IAM 域自治模块，跨模块契约直接归 provider 根包。
- 源 `modules/iam/sql` 的 36 表按实际职责拆为 core 13、authn 13、identity 3、oauth 4、audit 3；
  加 organization 14、governance 4、provisioning 5、federation 4，共 63 表、9 个 migration source。

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
Build: PASS（Task 1.0 Windows `make ci` build；Task 0.4 Linux CGO-disabled cross-compile）
Tests: PASS（Task 1.0 三引擎 helper/Goose 冷跑 + 完整 `make ci`）
Lint: PASS（Task 1.0 固定版本 golangci-lint / `make ci`）
Coverage: PASS（总体 91.4%，organization 100.0%，24/24 受控包 ≥90%）
Integration: PASS（`tests/goose`、`tests/internal/testdb`）
Other verification: `git diff --check` PASS；source tree/三方言 63 表 PASS；REST baseline 与
源代码集合相等（200/200）PASS；远端 Docker 26.1.5 PASS；MySQL 8.4.11 启动、查询、
Windows TCP 连通及 append-only Up/Down 部分状态恢复矩阵 PASS；Linux FIFO 真实文件系统测试 PASS。

## Next Action
进入 Task 1.1：用共享 testdb helper 参数化验证现有 todo/dlock/wuid/audit migration source 在
SQLite、PostgreSQL、MySQL 上的 up、幂等、down 对称、前缀和标识符长度行为。

## Last Checkpoint
2026-09-02 · IAM 分支 Task 1.0 已落地；三引擎 helper 位于独立 tests module，资源失败路径、
PostgreSQL 端口竞争和 SQLite 多连接外键均完成复审与反向变异；固定 lint、完整 `make ci` PASS
（总覆盖率 91.4%，24/24 受控包达标）。下一步 Task 1.1。
