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
Phase 2 / Task 2：完整迁移 `internal/iam/core` Go 功能。

## Overall Progress
- [x] 深度分析与迁移方案 v8
- [x] 两轮独立评审问题回收并修订方案
- [x] 两机环境 / Docker / collation / entity type 决策闭环
- [x] Phase 0 前置修复、扩展包、端口图与依赖评审
- [x] Phase 1 九个模块自治迁移源、里程碑 DAG 与三引擎验证
- [ ] Phase 2–8 Go 模块迁移
- [ ] Phase 9 集成与验收

## Active Work
Task 0.P/0.0/0.1/0.2/0.3/0.4/0.5、Task 1.0/1.1/1.2 已实现；迁移计划为 v8，
`internal/iam/` 作为域命名空间，9 个子模块各自拥有业务、model/query 与 SQL，取消集中 schema
和 feature 分包。Task 1.3 已完成 identity、authn、oauth、audit、core、organization、governance、provisioning、
federation 九个自治迁移源（63/63 表）；Task 1.4 已完成真实 MySQL 8 门禁、静态对账与 baseline 冻结，
当前进入 core Go 功能迁移。

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
- Task 1.3 organization：三方言 14 表、8 个迁移版本完成；部门可空 parent/self-FK 与根/子唯一、
  岗位规范化 code、静态/动态组、成员 NULL 时间窗、邀请可空状态及 1024/4096 字节限制已对齐。
  7 条指向 core 的 FK 归入 organization constraints；OAuth tenant FK 与 identity/core/organization
  一并通过无序注册的里程碑 DAG、孤儿拒绝、CASCADE/RESTRICT 和有数据 down/up 生命周期验证。
- Task 1.3 governance：三方言 4 表、3 个迁移版本完成；请求原因保留 500 字节限制，状态、决策及
  7 类 grant type 使用 binary enum，缺省决策/撤销时间保持 NULL；tenant FK 与子表级联约束归入
  governance constraints，并通过普通/`sl_` 前缀的三引擎 up/幂等/down/up、依赖拒绝和最大标识符验证。
- Task 1.3 provisioning：三方言 5 表、3 个迁移版本完成；8 类时间列改为真实时间，credential
  过期/使用/撤销与资源删除保持 NULL 未发生语义；SCIM 枚举、哈希、密文和外部标识符使用 binary
  collation，名称/外部标识符落实源码字节上限。2 条 tenant FK 归入 provisioning constraints；
  普通/`sl_` 前缀的三引擎数据保留 down/up、依赖拒绝、级联、大小写和最大标识符验证通过。
- Task 1.3 federation：三方言 4 表、3 个迁移版本完成；OIDC/SAML 标识符、subject、密钥与枚举使用
  binary collation，`default_role_id`/`rotated_at` 保持 NULL 未发生语义，时间列改为真实时间并落实
  provider/link/SP/key 的源码字节上限。link 的 provider FK 加强为 tenant 复合约束，principal FK 归入
  federation constraints；普通/`sl_` 前缀的三引擎 `2→3→2→3` 数据保留、依赖拒绝、级联、DAG
  回滚和最大标识符验证通过。九个模块 migration source 现已达到 63/63 表。
- Task 1.4：新增 `make test-mysql8`，自动消费本机或 SSH Docker transport；固定 MySQL 8.4 digest、
  `sql_mode` 与非 root trigger 创建前提，使用随机凭据/端口、唯一容器标签及可验证清理。普通与 `sl_`
  前缀均完成 9 流 up/validate/down/up、R16、R9''、federation 与 append-only 契约验证。
- `schema.lock` 冻结 135 个已发布 migration 文件；静态补充门禁对账 63 张 IAM 表、27 条跨模块 FK、
  三方言文件集合、对象前缀、时间类型与禁止改写/插入历史版本规则。Task 1.4 deep-review 修复固定 root
  凭据和静默清理失败后无剩余发现；Swagger/OpenAPI 对本次纯迁移门禁改动不适用。

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
- `make ci` 默认仍使用内存 MySQL；真实 MySQL DDL 门禁由独立的 `make test-mysql8` 执行，最终验收必须两者都跑。
- 本任务约 6 万行，必须按 Phase 分批实现、验证和留档。

## Verification
Build: PASS（Task 1.2 Windows `make ci` build；Task 0.4 Linux CGO-disabled cross-compile）
Tests: PASS（Task 1.2 主模块全量 + 独立 tests module + 完整 `make ci`）
Lint: PASS（Task 1.4 固定版本 golangci-lint，0 findings）
Coverage: PASS（Task 1.2 总体 91.3%，全部受控包 ≥90%）
Integration: PASS（`tests/goose`、`tests/internal/testdb`、完整 `tests/migration`；Task 1.2 A↔B FK；
Task 1.3 identity→authn DAG、audit append-only/retention、core R5''/R9''/R16、
organization→core、oauth→organization、governance→organization 与 provisioning→organization
及 federation→identity 三引擎双前缀生命周期；Task 1.4 全套 `tests` module 133.453s）
Other verification: `git diff --check` PASS；source tree/三方言 63 表 PASS；REST baseline 与
源代码集合相等（200/200）PASS；远端 Docker 26.1.5 PASS；MySQL 8.4.11 启动、查询、
Windows TCP 连通及非 root 用户全量双前缀迁移 PASS；schema lock 135 文件、63 表、27 跨模块 FK PASS；
门禁成功/失败路径远端容器清理 PASS；Linux FIFO 真实文件系统测试 PASS。

## Next Action
执行 Task 2：先按 source manifest 建立 `internal/iam/core` 的 model/query/dbgen 与双向 schema 契约，
再按源文件职责迁移 repository、domain/service、HTTP/module 和负向隔离测试。

## Last Checkpoint
2026-09-02 · IAM 分支 Task 1.4 完成检查点：`make test-mysql8` 在 SSH Docker 的 MySQL 8.4.11 上以
非 root 用户完成 9 个自治 migration source、63/63 表、普通/`sl_` 前缀全生命周期及关键契约验证；
`schema.lock` 冻结 135 个已发布文件并对账 27 条跨模块 FK。工具测试、完整 tests module（133.453s）、
格式、lint、diff 检查及远端容器清理均通过。下一步进入 Task 2 core Go 功能迁移。
