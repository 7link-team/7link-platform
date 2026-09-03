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
Phase 3 / Task 3.3：authn 数据、仓储与认证行为已完成，开始迁移 module/contracts 和 30 条 HTTP 路由。

## Overall Progress
- [x] 深度分析与迁移方案 v8
- [x] 两轮独立评审问题回收并修订方案
- [x] 两机环境 / Docker / collation / entity type 决策闭环
- [x] Phase 0 前置修复、扩展包、端口图与依赖评审
- [x] Phase 1 九个模块自治迁移源、里程碑 DAG 与三引擎验证
- [x] Phase 2 core Go 模块迁移
- [ ] Phase 3–8 其余 Go 模块迁移与装配
- [ ] Phase 9 集成与验收

## Active Work
Task 0.P/0.0/0.1/0.2/0.3/0.4/0.5、Task 1.0/1.1/1.2 已实现；迁移计划为 v8，
`internal/iam/` 作为域命名空间，9 个子模块各自拥有业务、model/query 与 SQL，取消集中 schema
和 feature 分包。Task 1.3 已完成 identity、authn、oauth、audit、core、organization、governance、provisioning、
federation 九个自治迁移源（63/63 表）；Task 1.4 已完成真实 MySQL 8 门禁、静态对账与 baseline 冻结。
Task 2.4–2.6 已完成 core HTTP/module、负例与阶段门禁；Task 3.1 已完成 authn 13 张自治表的
model/query/dbgen、三引擎列/NULL/时间契约及前缀安全 GORM 仓储，Task 3.2 已完成认证行为迁移，
当前进入 Task 3.3 module/contracts 与 HTTP 路由迁移。
identity 3 张自治表的 model/query/dbgen 也已提前完成，供 authn 读取由 identity 拥有的 principal 与
service-account 状态，避免跨模块重复定义持久化模型；identity 业务迁移仍留在 Task 3.5–3.7。

Task 2.1 已完成：13 张 core 表均已有 module-owned model、gorm-gen query 与统一前缀绑定；三引擎
model ↔ DDL 列集合和 NULL 契约已形成全称测试，MySQL `iam_entities.parent_key` 是唯一显式方言例外。

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
- Task 2.1：在 `internal/iam/core/{model,query,dbgen}` 建立 13 张自治表的 GORM model 和生成查询；
  `query.Bind` 一次性绑定完整表集，避免事务混用前缀。`RevisionStore` 已改用正式 model，并修复首次
  `Lock` 向 NOT NULL `updated_at` 插入 NULL 的模型/DDL 漂移。
- 三引擎 model ↔ DDL 契约逐表比较完整列集合与 NULL 性；`Menu.Route` 指针反向变异准确失败后已恢复。
  Task 2.1 deep-review、core 全包测试、生成、格式和 golangci-lint 均通过，无剩余发现。
- Task 2.2：core 的 role/permission/member/menu/entity/relationship/platform administrator/data scope/
  directory binding/dynamic group/temporary grant repositories 已按前缀安全 GORM 迁移；tenant 查询、更新、删除与
  grant 均携带 tenant ID，nullable time/route/parent 保持数据库 NULL 语义。
- Task 2.2：mutation、policy revision 与 audit append 固定同一 writer 和事务；启用 GORM 错误翻译，唯一键/FK
  冲突使用稳定 sentinel；最后 tenant 管理员使用 revision 行锁，最后 platform 管理员删除/降级先锁定完整管理员集合。
- Task 2.2 deep-review 发现并修复两个事务并发删除不同 full platform admin 后可能同时提交的竞态；重叠事务测试
  证明第二个删除必须等待且最终失败，移除锁的反向变异按预期失败。其余架构、tenant/NULL、安全和跨平台视角无发现。
- Task 2.3：core 根包已迁入授权声明/判定、角色与权限、成员/菜单、平台管理员、实体/关系、数据范围、
  目录绑定、动态组与临时授权领域服务；沿用 provider 根包窄契约，未新增 feature/port 中转包。
- Task 2.3：PostgreSQL revision upsert 改用表限定列；关系递归 CTE 按 SQLite/MySQL/PostgreSQL 显式固定
  text/integer 类型与 PostgreSQL `C` collation，真实 PostgreSQL 16 和 MySQL 8.4 双前缀均通过两级关系写入与闭环拒绝。
- Task 2.3：追加三方言 `00011_relationship_traversal.sql`，为反向遍历建立
  `(tenant_id, resource_id, relation)` 索引；`11→10→11` 精确验证新索引消失/恢复且旧索引不受影响，
  schema lock 从 135 扩展到 138 个文件。core 覆盖率 90.0%，race、lint、完整 migration 与真实 MySQL 门禁通过。
- Task 2.3 deep-review 修复迁移目标版本、PostgreSQL key/include 区分、单版本 down/up、跨真实引擎 revision
  增量与闭环失败无脏写门禁；对应反向变异均准确失败。当前批次无 HTTP/OpenAPI 改动，Swagger 评审不适用。
- Task 2.3 最终 `make ci` 暴露 audit/organization 已登记覆盖包的迁移 module 方法缺少包内契约测试；
  补齐 module name、embedded source、milestone 版本/依赖全称断言后两包均为 100.0%，反向变异准确失败。
- Task 2.4–2.6：core 根包已迁入 module 生命周期、i18n 与 51 条冻结 HTTP 路由；实际 OpenAPI 的
  `OperationID|METHOD|PATH` 与冻结基线 51/51 完全相等，且与固定源树的注册类型及 resource/verb guard
  逐条一致。tenant A/B 的 role/member/menu/entity/relationship 跨租户读写负例、platform guard、完整
  CRUD/授权流程、数据库关闭后的全路由错误 envelope、全部领域 sentinel 映射均有行为测试。
- Task 2.4–2.6 deep-review 修复一项 `testifylint` 断言形式；Swagger 门禁确认路径/参数/ID schema、
  PATCH 可清空 ID、relationship DELETE 必填 tuple、统一错误 schema 和兼容性均无剩余发现。
  `go generate` 幂等、core race、core 90.0%、全仓 `make ci`、真实 MySQL 8.4 与 schema lock 均通过。
- Task 3.1 model/query：authn 13 张表全部建立 module-owned `model/query/dbgen`；所有 nullable 时间与
  nullable passkey principal 使用指针，所有时间列使用 `time.Time`。三引擎 model ↔ DDL 全称契约、
  前缀绑定、生成幂等、包 race 及全仓 golangci-lint 已通过。
- Task 3.1 repositories：按认证职责保留 credential/session/MFA/passkey/recovery/verification/step-up
  仓储文件，迁入锁定、CAS 单次消费、session 轮换、跨 session/refresh-token 撤销与 outbox fencing；
  pinned writer 与 active transaction 显式区分，replacement/purge 失败可完整回滚。
- Task 3.1 deep-review：修复 outbox 领取后借用新 attempt、pinned writer 跳过事务、recovery code
  跨 principal 写入、service account 进入密码找回、歧义 identifier 任取一人及 binary collation 下
  大小写恢复查询失配；集合/并发测试覆盖旧 attempt 成功/失败回写与 claim 读取失败回滚，反向变异均能
  抓住对应缺陷。本批无 HTTP/OpenAPI 改动，Swagger 评审不适用。
- Task 3.1 索引门禁：authn v10 增加 verification code 前导索引，oauth v6 增加 principal-wide revoke
  索引；MySQL 用单条 ALTER 双向交换 FK 支持索引，plain/prefixed 连续两轮 v6↔v5 均通过真实 8.4.11。
  schema lock 从 138 扩展到 144 个文件，仍为 63 表、27 条跨模块 FK。
- Task 3.5 model/query 前置：identity 3 张表建立 module-owned `model/query/dbgen`，三引擎列/NULL/时间
  全称契约、前缀绑定、包 race、生成与全仓 golangci-lint 已通过；尚未迁移 identity 业务和 HTTP。
- Task 3.2 behavior：按 authn 根包职责迁入 captcha、口令、TOTP、WebAuthn、注册、登录、邮箱验证、
  密码找回、step-up、邀请凭据、通知 outbox worker 与 i18n；未引入 feature/port 子包。
- Task 3.2 hardening：邮箱验证码尝试次数持久化并绑定规范化邮箱；通知拒绝 3xx 凭据重放并上报轮询错误；
  passkey clone warning 与 counter 更新保持原子；TOTP enrollment 锁顺序固定；邮箱验证后续动作加入同一事务；
  AAGUID 固定为 16 字节，默认登录 URL 必须在 allowlist。对应类级测试均完成反向变异。
- Task 3.2 review/gate：架构视角无发现；重复验证码函数别名已删除；`ClaimEnricher` 保持包内消费侧接口，
  未扩大冻结能力图。authn race、90.1% 覆盖率、生成幂等、全仓 `make ci`、三引擎 v11 生命周期和真实
  MySQL 8.4.11 门禁均通过；schema lock 扩展为 147 个文件，仍为 63 表、27 条跨模块 FK。

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
- 验证码直接依赖的共同传递模块 `github.com/golang/freetype` 已归档；当前无 `govulncheck` 可达漏洞，
  且两项验证码能力均依赖它。等价迁移阶段保留，迁移完成后单独评估验证码/字体栈替换。
- 本任务约 6 万行，必须按 Phase 分批实现、验证和留档。

## Verification
Build: PASS（Task 3.2 Windows `make ci` build；Task 0.4 Linux CGO-disabled cross-compile）
Tests: PASS（Task 3.2 authn `-race`；完整 `make ci`；`tests/migration` 170.873s；真实 MySQL 8.4.11）
Lint: PASS（Task 3.2 `make lint` 与 `make ci` 全仓 golangci-lint，0 findings）
Coverage: PASS（总计 90.2%；authn 90.1%；core 90.0%；全部登记包 ≥90%）
Integration: PASS（`tests/goose`、`tests/internal/testdb`、完整 `tests/migration`；Task 1.2 A↔B FK；
Task 1.3 identity→authn DAG、audit append-only/retention、core R5''/R9''/R16、
organization→core、oauth→organization、governance→organization 与 provisioning→organization
及 federation→identity 三引擎双前缀生命周期；Task 2.3 core `11→10→11`、Task 3.2 authn
`11→10→11` 与 oauth 连续两轮 `6→5→6` 索引生命周期；PostgreSQL 16/MySQL 8.4 relationship/revision 行为）
Other verification: `git diff --check` PASS；source tree/三方言 63 表 PASS；core 实际 OpenAPI 与
冻结集合相等（51/51），且注册类型/resource/verb guard 与固定源逐条相等；全域 REST baseline 与
源代码集合相等（200/200）PASS；远端 Docker 26.1.5 PASS；MySQL 8.4.11 启动、查询、
Windows TCP 连通及非 root 用户全量双前缀迁移 PASS；schema lock 147 文件、63 表、27 跨模块 FK PASS；
门禁成功/失败路径远端容器清理 PASS；Linux FIFO 真实文件系统测试 PASS。

## Next Action
执行 Task 3.3：迁移 authn `module.go`、`rest.go` 与 30 条冻结 HTTP 路由；稳定提供
`InvitationCredentials`/`WebService`，并按 OpenAPI 门禁核对路径、参数、schema、安全与运行时路由。

## Last Checkpoint
2026-09-03 · IAM 分支 Task 3.2 完成检查点：authn 行为、v11 验证尝试迁移及评审修正已完成；
authn race/90.1% coverage、完整 `make ci`、真实 MySQL 8.4.11、生成幂等及 schema lock 147 文件
均通过。下一步执行 Task 3.3 authn module/contracts 与 HTTP/OpenAPI。
