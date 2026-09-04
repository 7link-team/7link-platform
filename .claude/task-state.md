# Task State

## Task
IAM 域全量迁移：chaosplus `apps/server` → 7link-platform `apps/backend-api`

## Objective
按 `apps/backend-api/docs/superpowers/plans/2026-09-02-iam-domain-migration.md` 将 9 个 IAM 域模块与
63 张表全功能迁入 gorm/gorm-gen/goosex/pkg-module 架构，修复源 SQL 缺陷，并通过
PostgreSQL、MySQL、SQLite 真实迁移与 `make ci` 门禁。

## Current Phase
BLOCKED

## Current Step
Phase 9 / Task 9.7：Windows 上全部最终门禁与文档收口已通过；等待原 Mac 执行计划 Task 0.P Step 4 后才能勾选 9.7 并宣布迁移完成。

## Overall Progress
- [x] 深度分析与迁移方案 v9
- [x] 两轮独立评审问题回收并修订方案
- [x] 两机环境 / Docker / collation / entity type 决策闭环
- [x] Phase 0 前置修复、扩展包、端口图与依赖评审
- [x] Phase 1 九个模块自治迁移源、里程碑 DAG 与三引擎验证
- [x] Phase 2 core Go 模块迁移
- [x] Phase 3 authn 与 identity Go 模块迁移
- [x] Phase 4 oauth Go 模块迁移
- [x] Phase 5 organization Go 模块迁移
- [x] Phase 6 audit Go 模块迁移
- [x] Phase 7 governance/provisioning/federation 迁移
- [x] Phase 8 应用装配、OpenAPI、跨模块负例与架构门禁
- [ ] Phase 9 集成与验收

## Active Work
Task 9.7 的 Windows 可执行项已完成：`make generate` 保持完整工作树指纹不变，最终 `make ci`、真 MySQL 8.4 migration/performance/schema-lock 门禁通过，`make dev` 的 `/healthz` 返回 HTTP 200 / `SERVING` 后干净关闭；README Testing 与 D13 性能门禁决策已更新。统一执行契约八项均有既有 source manifest、schema/behavior/tenant/OpenAPI/coverage 证据并已勾选。唯一剩余项是 Task 0.P Step 4：必须回到原 Mac 实跑 `make iam-preflight` 与 `make ci`；计划明确禁止用 Windows 结果替代，因此 9.7 与 Phase 9 暂不勾选。执行模式 Direct：当前上层约束禁止未授权子代理。

Phase 8 已完成：`internal/app` 已接入 authz registrar、IAM 配置映射、冻结的 9 模块运行时顺序与 6 模块声明顺序；core 提供 `ActiveMemberChecker`、`AdministratorGuard`、`RoleGrantStore`，identity 经 registry 解析 guard。缺失 provider 启动测试与顺序测试已加入。OAuth revoke/404 与 Federation SAML form/response OpenAPI 缺口已修复。组合应用测试实际执行 SQLite migration、wire/initialize/start，再经生产 Huma factory 注册所有模块，断言 208/208 target 集合、source 双射与全局 OperationID 唯一。真实组合 HTTP tenant A/B 矩阵使用 identity principal、JWT、持久化 platform admin 与 SCIM credential，验证 invitation、group provision、SCIM、federation callback、governance revoke、IAM authorization、audit trace 均不跨 tenant。`tests/architecture` 以 `go list -json` 和 Go AST 检查精确九模块目录、模块自有 model/query/dbgen/三方言 SQL、禁止中央 port/schema、禁止跨模块 persistence/concrete/constructor 使用、`module.Resolve` 依赖声明及根包导出持久化类型泄漏；违规 fixture 对五类规则均能触发。Task 8.7 首次 coverage 运行发现 core 89.5%/总计 89.9%，补充 `RoleGrantStore` provider late-binding 生命周期回归后官方 gate 达到 core 90.0%/总计 90.0%；tenant 集成 fixture 的 HTTP response 在 request 边界立即读取并关闭，bodyclose lint PASS。

Task 7.1–7.3 已验收：四表 `model/query/dbgen`、统一前缀绑定、request/review 状态机、14 条 HTTP、
provider 根契约解析、declaration-only、tenant/actor/终态并发/last-admin 负例均已完成。
所有 standalone mutation 只拥有一个 writer transaction，状态 CAS、core mutation、last-admin verifier、
policy revision 与 audit append 共用该 transaction。governance root coverage 90.2%，已登记 Makefile coverage gate；
focused tests/race/vet/lint、generator、三引擎 migration/model 与最终 `make ci` 均 PASS。
Tasks 7.4-7.6 已验收：provisioning 五表 `model/query/dbgen`、统一前缀绑定、directory credential、
SCIM users/groups/bulk/patch/filter/discovery、outbound target 与冻结 30 条 HTTP operation 已完成；
identity/organization/audit provider 契约通过 `module.Services` 解析并保留 declaration-only。
credential→directory→tenant 唯一绑定、A→B 不可见、bulk tenant 隔离、ETag 并发单胜者、事务回滚、
outbound 同资源串行与 lock registry 回收均有验证。最终 focused/full gates 与 deep-review/deep-audit PASS，
provisioning root coverage 90.9%；未修改 Phase 8 composition root。

Tasks 7.7-7.9 federation 已验收：四表 `model/query/dbgen`、prefix-safe repository、OIDC discovery/JWKS/PKCE/state/verifier/JIT link、SAML IdP/SP/SSO/SLO/metadata/key rotation、module/i18n 与批准的 17 条 operation 全部完成。模块只解析 `guid.Source`、`audit.Appender`、`identity.IdentityProvisioner`、`authn.WebService`，保留 declaration-only，未修改 composition root。tenant/provider/SP/path/state/assertion/logout replay、事务回滚、数据库失败、并发单胜者及 fail-closed capacity 均有负例；replay registry 仍为进程内状态，跨 replica 不共享是明确部署限制。

Task 0.P/0.0/0.1/0.2/0.3/0.4/0.5、Task 1.0/1.1/1.2 已实现；迁移计划为 v9，
`internal/iam/` 作为域命名空间，9 个子模块各自拥有业务、model/query 与 SQL，取消集中 schema
和 feature 分包。Task 1.3 已完成 identity、authn、oauth、audit、core、organization、governance、provisioning、
federation 九个自治迁移源（63/63 表）；Task 1.4 已完成真实 MySQL 8 门禁、静态对账与 baseline 冻结。
Task 2.4–2.6 已完成 core HTTP/module、负例与阶段门禁；Task 3.1 已完成 authn 13 张自治表的
model/query/dbgen、三引擎列/NULL/时间契约及前缀安全 GORM 仓储，Task 3.2 已完成认证行为迁移，
Task 3.3–3.4 已完成 module/contracts、30 条 RESTful HTTP operation、OpenAPI 与安全负例；
Task 3.5 已完成 identity principal/service-account/provisioning 业务与全部行为测试。
identity 3 张自治表的 model/query/dbgen 也已提前完成，供 authn 读取由 identity 拥有的 principal 与
service-account 状态，避免跨模块重复定义持久化模型；identity 业务迁移仍留在 Task 3.5–3.7。
Task 4.1 已完成 OAuth 四表 model/query/dbgen、前缀安全 repository、protocol/client administration
service、事务审计、授权码与 refresh token CAS、tenant/client 隔离和三引擎最终 schema 契约。
Task 4.2 已完成 OAuth module lifecycle/services 注入、三 locale namespaced i18n、12 条冻结 HTTP operation、
实际 OpenAPI 契约与 protocol/management 行为；无 declaration-only constructor，未提前修改 app composition。
Task 4.3 已补齐并独立验证 tenant/client/code/token 跨界负例、失败尝试不消费目标凭据、refresh family
撤销隔离，以及 authorization code/refresh token 并发重放与 CAS loser 撤销 successor family。
Task 4.4 已完成 generate 幂等、架构/模块检查、race、lint、覆盖率与两轮完整 `make ci` 门禁。
Task 5.1 已完成 organization 14 张表的 module-owned model/query/dbgen、统一前缀绑定，以及 department、
position、group、tenant、invitation 的输入归一化与稳定 DTO/错误契约；MySQL `parent_key` 保持显式方言例外。
Task 5.2 已完成 department hierarchy、position/group member、tenant lifecycle 与 invitation state machine 的
prefix-safe GORM repositories/services；所有 tenant-owned lookup/mutation 带 tenant ID，audit/revision/closure/member/
identity invitation acceptance 使用同一 transaction 或 caller-owned writer。
Task 5.3–5.4 已完成 caller-owned `GroupProvisioner`、module.Services 六项能力解析、declaration-only、三 locale
namespaced i18n 与冻结 32 条 HTTP operation；平台 tenant CRUD、tenant A/B directory/member/invitation/token
隔离以及 guard/audit/storage 失败回滚均有行为测试。
Task 5.5 已完成 generate 字节稳定、focused race/vet/lint、三引擎双前缀 migration/model parity 与完整
`make ci`；organization root coverage 1767/1963 = 90.015%。core repository test harness 改为直接读取真实
organization SQL，消除 organization 导入 core 后出现的 test-only import cycle。

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
- 从固定 source tree 展开 208 条 HTTP operation，文档与源代码集合对账 208/208、无缺失、
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
- Task 3.3：authn `module.go`/`http.go` 已接入真实 Registry 生命周期并稳定提供
  `WebService`/`InvitationCredentials`；30 条目标 operation 使用资源化路径和 HTTP method，OpenAPI
  逐条校验路径参数、typed WebAuthn schema、安全声明、动态 cookie 名、错误 envelope 与运行时路由。
- Task 3.4：HTTP 负例证明 Alice 的 session/passkey 列表、撤销、改名和删除均不可观察或改变 Bob；
  MFA/recovery/verification/passkey/step-up 的 CAS 单次消费已有 8 路并发 winner 测试。authn `-race`
  与 90.3% 覆盖率通过。邀请 tenant 绑定属于 organization acceptance，保留在 Task 5.4。

- Task 3.5：identity 根包已迁入 principal、service account、provisioning 与仓储；复用
  `core.AdministratorGuard` 与 `core.RevisionStore`，未复制最后管理员规则，未新增 feature/port 包。
  源 `service.go`/`service_account.go`/`provisioning.go` 的全部 type/function/method 已逐条映射，
  HTTP input 类型与 `RegisterServiceAccountREST` 按计划留到 Task 3.6。
- Task 3.5 测试：新增 `migrate_test.go`、`repository_test.go`、`service_test.go`、
  `service_account_test.go`、`provisioning_test.go`（与源文件一一对应）。覆盖租户隔离负例、
  最后管理员 guard 回滚、CAS 版本冲突、凭据上限与撤销幂等、跨模块表失败路径。
  identity 覆盖率 90.9%，`-race` 通过，全仓 `make ci` 总覆盖率 90.2% 全绿。
- Task 3.5 修复两个自查缺陷：`countActiveServiceAccountCredentials` 误用 `field.Expr.Or`（改用项目既有
  `field.Or`），`principalByLoginOrEmail` 复用同一 `*gorm.DB` 导致登录未命中错误串到邮箱查询
  （改为每次新建语句）；后者由新增测试直接抓到。`replaceServiceAccount` 的死参数 `displayName` 已删除。
- identity 生产代码 import core 后，`internal/iam/core` 的包内测试不能再 import identity（Go 禁止测试
  形成环）。core 测试改用原始 DDL 建 `iam_principals`，真实 schema 仍由 `tests/migration` 验证。

- Task 3.6–3.8：identity 根包新增 `http.go`/`module.go` 与 `i18n` 包；14 条 operation 与计划批准的
  目标契约集合相等，`identity-disable/restore-principal` 落为 `PUT`/`DELETE /iam/principals/{id}/suspension`
  子资源。principal 管理路由保留源语义的 `platform_administer` 租户内权限（非 RegisterPlatform），
  创建类 operation 与 core 一致返回 201，删除/撤销改用具名响应体而非源的裸 map。
- Task 3.7 负例：HTTP 层证明 tenant B 对 A 的 principal/service account/credential 的
  读、改、挂起、恢复、替换、删除、撤销全部 404 且 A 行不变；缺少 `platform_administer` 时
  PATCH/PUT/DELETE suspension 一律 403 且状态不变；版本冲突 409、非法 ID 422。
- Task 3.8 门禁：identity 90.6%、authn 90.3%、`-race` 通过、golangci-lint 0 findings、
  `go generate` 零 diff、全仓 `make ci` 总覆盖率 90.2% 全绿。
- Task 4.1：OAuth 四表 GORM model、gorm-gen query/dbgen、统一前缀绑定与 repository/service 已完成；
  client mutation+audit、consent+code+audit、code consume+refresh insert、refresh CAS+successor、client
  delete+refresh revoke 均固定同一 writer/transaction。授权码与 refresh token 为 CAS 单次消费，CAS loser
  事务性撤销完整 family；client CRUD、token exchange/revoke/introspection 均绑定 client/tenant。
- Task 4.1 deep-review 修复并验证四类缺陷：MySQL `BIGINT` 对 `22junk` 的宽松强制转换（所有 client ID
  在 SQL 前解析为 `guid.ID`）、RFC 7636 verifier/challenge 长度与字符集、revoke 跨 client/存储错误测试缺口、
  SQLite v5 重建后 model parity 与有数据 `v4→v6` 保留缺口。五类反向变异均使目标测试失败后已恢复。
- Task 4.1 门禁：OAuth `-race`、focused lint/vet、generate 字节零变化、root coverage 91.0%、最终三引擎
  model↔DDL、SQLite 有数据升级、`make ci`（总覆盖率 90.2%、migration 152.478s）和真实 MySQL 8.4
  全量迁移全部通过；schema lock 仍为 147 files / 63 IAM tables / 27 cross-module FKs。
- Task 4.2：`Module` 经 `module.Services` 解析 `authn.WebService`、`guid.Source` 与 `audit.Appender`，依赖
  i18n/guid/audit/authn；initialize/health/RegisterHTTP 及缺失 wiring 均有测试。OAuth i18n 使用 `oauth.*`
  全限定 key 并完整覆盖 en-US/ms-MY/zh-CN；management 复用 authz/respx，protocol 保持 form body 与
  `{error,error_description}`。实际 Huma OpenAPI 与冻结 manifest 12/12 完全相等。
- Task 4.2 deep-review 修复三类行为缺陷：authorize 的 Huma 前置验证改为保留 schema、handler 内返回 OAuth
  error；repository outage 不再被 service 折叠为 invalid client/grant；introspection `sub` 使用 protocol
  subject 而非仅 principal ID。`prompt=consent` 不引用缺失路由，userinfo 强制 principal + openid。
- Task 4.2 门禁：focused race/vet/lint PASS，OAuth root coverage 91.4%（完整 CI 91.5%）；四项反向变异
  均使目标测试失败后恢复。`make ci` PASS（总覆盖率 90.2%、migration 152.818s、build PASS）；真实
  MySQL 8.4 PASS，schema lock 147 files / 63 IAM tables / 27 cross-module FKs。
- Task 4.3：service/HTTP acceptance matrix 完整覆盖 tenant A 不可管理 tenant B client、A client 不可兑换 B 的
  authorization code/refresh token、失败跨 client 尝试不消费 B 凭据、跨 client revoke 不改变 B token family、
  introspection 同时绑定 client ID 与 tenant claim；authorization code 与 refresh token 并发重放均仅一次成功，
  CAS loser 会撤销 winner 的 successor family。
- Task 4.3–4.4 deep-review：OAuth runtime/OpenAPI、命名注释、可测试性及 tenant/client/concurrency matrix
  独立复审均无剩余发现；client/code/token 绑定、两类 CAS、family revocation、introspection tenant 绑定及
  HTTP/OpenAPI 精确契约的反向变异均使目标测试失败，且变异已全部恢复、无备份文件残留。
- Task 4.4 门禁：`go generate ./internal/iam/oauth` 字节零变化，冻结 capability-owner、module/OpenAPI、
  focused vet/lint 与 `go test -count=1 -race ./internal/iam/oauth/...` PASS；focused OAuth coverage 91.6%。
  最终 `make ci` PASS（总覆盖率 90.2%、OAuth 91.6%、migration 与 production build PASS）。
- Task 5.1：14 个 GORM persisted model 与实际三引擎 DDL 的列、NULL、时间类型逐表相等；统一 query.Bind
  对 plain/prefixed 14 张表全覆盖。department/group `name_key`、root/child parent 唯一性和同 tenant parent FK
  继续由 PostgreSQL/MySQL/SQLite migration contract 验证；MySQL generated `parent_key` 在共享 parity helper
  中显式 allowlist。根包 normalization tests、`-race` 与 generate 字节稳定 PASS；当前 root coverage 77.6%，
  尚未达到 5.5 的 ≥90% 最终门禁，将由 5.2–5.4 repository/service/HTTP 测试补齐。
- Task 5.2：真实 prefixed SQLite migration 上完成 department closure create/move/delete、tenant A/B 不可见、
  group/position member active check 与 portable upsert、dynamic group rule、tenant member listing/soft-delete/bootstrap、
  invitation create/resend/revoke/accept、跨 tenant binding 拒绝、tamper 拒绝、accepted 幂等及 audit failure 全回滚。
  focused root/subpackage tests、`-race`、`go vet` 与 golangci-lint PASS；Task 5.3–5.5 的 HTTP/module、完整负例矩阵、
  覆盖率和最终 `make ci` 门禁也已完成。
- Task 6.1–6.2：audit 三表 model/query/dbgen、统一前缀绑定、caller-owned `audit.Appender`、append-only hash chain、
  head lock、snapshot export、retention/governance、S3-compatible Object Lock anchor 与 Ed25519 root signing 已完成；
  standalone mutation 固定同一 writer transaction，跨模块 appender 直接使用调用方传入的 `*gorm.DB`。
- Task 6.3–6.4：唯一生产 `audit.Appender` provider、declaration-only、namespaced i18n 与冻结 8 条 HTTP operation
  已完成；tenant A/B list/get/export/integrity/anchor/retention 隔离、并发 append 不分叉、历史篡改检测、
  三引擎 append-only trigger、依赖/存储失败和真实 MinIO COMPLIANCE retention/version/delete refusal 均有验证。
- Task 6.5 门禁：generator 字节稳定，focused tests/race/vet/lint、三引擎双前缀 migration/model parity、
  双模块 `go mod verify` 与最终 `make ci` PASS；audit root coverage 93.6%，全仓 coverage 90.1%。
- Task 7.1–7.2：governance 四表 model/query/dbgen、request/review 状态机、七类 grant snapshot 与全部可变
  revoke、统一前缀 repository、caller-owned transaction、provider/declaration-only、namespaced i18n 和冻结
  14 条 HTTP operation 已完成；tenant/actor/human-principal、rollback、CAS 并发与 nullable time/ID 均有验证。
- Task 7.3 门禁：generator 字节稳定，focused tests/race/vet/lint、三引擎 migration/model、双模块
  `go mod verify`、当前工作区 `git diff --check` 与最终 `make ci` PASS；governance root coverage 90.2%，
  全仓 coverage 90.0%。验收复审发现并修复 RoleGrantStore 对 group/position/entity review removal 缺少
  AdministratorGuard 的源行为差异；首次复跑 core coverage 89.9% 后补齐幂等回归路径，最终 core 90.0%。
- Tasks 7.4-7.6：provisioning 五表 model/query/dbgen、prefix-safe repository、directory credential、
  SCIM users/groups/bulk/patch/filter/discovery、outbound target、module/i18n 与 30 条 HTTP operation 已完成；
  tenant/directory/bulk/ETag/rollback/outbound concurrency 负例已覆盖。generator、focused tests/race/vet/lint、
  双模块 verify、完整 migration、coverage、`make ci` 与真实 MySQL 8.4 门禁全部 PASS；root coverage 90.9%。
- Task 9.1：新增可跨子进程使用的 MySQL TCP testdb helper、数据库/前缀可配置的生产 app 组合夹具与
  `tests/lifecycle` 六案例矩阵；完整业务链逐阶段断言持久化与 audit 行为。矩阵在最终 audit hash 修复后
  PostgreSQL/MySQL/SQLite 的 plain/`sl_` 全部 PASS（40.83s）。生命周期执行发现并修复 MySQL OAuth
  保留字 alias、MySQL unchanged-update `RowsAffected` 语义和 PostgreSQL `CHAR(64)` 空 hash padding；
  三项均有聚焦回归。完整 tests module、相关 root packages/race、双模块 vet/tidy、lint/fmt/diff 均 PASS。
- Task 9.2：migration-status 主路径改为 `/migration-status`，保留 deprecated `/debug/migrations`；两条路由
  共用 handler 并均登记 OpenAPI。生产 composition 白盒测试对精确 13 条 flow 执行 up/status/reverse
  down-to 0/up，并断言 clean/pending 版本集合；外层 PostgreSQL/MySQL/SQLite plain/`sl_` 六案例矩阵对账
  九个 IAM migration source 互斥 ownership、并集 63 及数据库实际 IAM 表精确相等。
- Task 9.3：强化 `tests/architecture` 的全称门禁：九模块 root 必须有独立业务生产源码；所有导入 provider
  的 `module.Resolve` 必须由 root `Module.ModuleDependencies` 的 return expression 声明，覆盖 IAM 与 infra；
  同名 package function、方法体未返回引用、跨模块 model/query/dbgen、concrete type/constructor 与公开 storage
  type 泄漏均由实际 graph 或 fixture 检查。生产 IAM 模块与注册顺序无改动。
- Task 9.6：新增生产组合的外部数据库性能契约与 PostgreSQL coordinator，真实调用 authorization、session、
  SCIM bulk 25 和 audit append；冻结 PostgreSQL 16.9/MySQL 8.4.11 共 24 份 JSON `EXPLAIN`、规范化 SQL
  fingerprint、每路径 5 份 benchmark/alloc/bytes 与 3x 最大样本阈值。结构门禁拒绝无作用域全表扫描并对
  关键已填充表强制索引访问；同机时延由显式环境开关执行。`make test-performance` 与真 MySQL 门禁已接线。

## Important Decisions
- 目标为全量、全功能迁移；SAML/SCIM 不允许长期 Disabled。
- `internal/iam/` 只作命名空间；core/authn/identity/oauth/organization/audit/governance/
  provisioning/federation 是 9 个自治模块，各自拥有 SQL、版本表和跨模块 FK（D10）。
- 多写者是现有正式契约；Task 0.0 只修同一次迁移的 writer 句柄漂移，不禁用多写者。
- 动态菜单多语言不属于本次迁移；保持 63 表，不新增第 64 张表（D7）。
- 不局部引入 Ports-and-Adapters，不设中央 port/schema，不按 feature 机械拆包（D10 取代 D3/D9）。
- 只支持已确认的原 Mac 与当前 Windows 两机拓扑；源 tree 变化必须先复审。
- 传输文件统一 `http.go`/`RegisterHTTP`，接口按 RESTful 标准设计；源 208 条 operation 通过逐模块
  source→target 映射保证完整性，不再要求目标复制源 RPC 路径；源无 IAM gRPC，当前不虚构（D12）。
- Federation replay 在不增加表的约束下使用 bounded fail-closed TTL registry；不复用会按 nonce 无界增长的
  distributed-lock rows。跨 replica 的一次性消费不在现有能力图内，保留为明确部署限制。

## Confirmed Constraints
- 三方言 DDL 全部前缀化、真实时间类型、无 0 哨兵、FK/guid 使用 BIGINT。
- 每个新增受控模块覆盖率 ≥90%，阶段结束 `make ci` 必须通过。
- 不迁移存量数据；不缩减现有 IAM 域功能。
- 实现按依赖顺序推进；阶段评审按 deep-review 使用隔离只读 subagent，并由主执行者亲自证伪。
- 每次换机或恢复任务先运行 `make iam-preflight`；后续只消费生成的路径与 Docker transport。

## Known Issues / Risks
- `git diff --check` 对当前 OAuth 工作区 PASS，但 `git diff --check main...HEAD` 会因已提交的 identity 三个
  locale JSON 使用 CRLF 报 trailing whitespace；不得继续把分支级检查记录为 PASS。
- 多写者 routing mode 与读副本接线仍是独立架构决策，不在 Task 0.0 中扩大范围。
- Mac 上的 `make iam-preflight` 与 `make ci` 尚未在本轮实跑；回到 Mac 后先完成该检查。
- `make ci` 默认仍使用内存 MySQL；真实 MySQL DDL 门禁由独立的 `make test-mysql8` 执行，最终验收必须两者都跑。
- 验证码直接依赖的共同传递模块 `github.com/golang/freetype` 已归档；当前无 `govulncheck` 可达漏洞，
  且两项验证码能力均依赖它。等价迁移阶段保留，迁移完成后单独评估验证码/字体栈替换。
- 本任务约 6 万行，必须按 Phase 分批实现、验证和留档。
- Federation replay registry 为进程内状态；单进程和同一 replica 重放可拒绝，跨 replica 需要现有计划外的
  shared nonce capability 或 schema 变更，当前不得静默增加。

## Verification
Build: PASS（Phase 8 Windows `make ci` production build）
Tests: PASS（9 个 IAM root 包与 `internal/app` focused/race PASS；Task 8.5 真实组合 tenant A/B PASS；Task 8.6 architecture 真实 graph/违规 fixture PASS；完整 tests module PASS）
Lint: PASS（`go vet ./...` 与 pinned golangci-lint/`make ci`，0 findings）
Coverage: PASS（完整 coverage 总计 90.0%；audit 93.6%、authn 90.3%、core 90.0%、federation 91.4%、governance 90.2%、identity 90.6%、oauth 91.6%、organization 90.0%、provisioning 90.9%）
Integration: PASS（`tests/goose`、`tests/internal/testdb`、完整 `tests/migration`；Task 1.2 A↔B FK；
Task 1.3 identity→authn DAG、audit append-only/retention、core R5''/R9''/R16、
organization→core、oauth→organization、governance→organization 与 provisioning→organization
及 federation→identity 三引擎双前缀生命周期；Task 2.3 core `11→10→11`、Task 3.2 authn
`11→10→11` 与 oauth 连续两轮 `6→5→6` 索引生命周期；PostgreSQL 16/MySQL 8.4 relationship/revision 行为）
Other verification: Phase 8 `make generate` 工作区指纹零变化、root/tests 两个 Go module `go mod verify` PASS、当前工作区
`git diff --check` PASS、`main...HEAD` 因 identity locale CRLF FAIL；audit 实际 OpenAPI 8/8、OAuth 实际
OpenAPI 12/12 且 schema/security/guard PASS；source tree/三方言 63 表 PASS；core 实际 OpenAPI 51/51；
authn 实际 OpenAPI 与批准目标契约相等（30/30），源 authn→目标映射 30/30；全域源 HTTP operation
baseline 已纠正为 208（旧提取漏扫 `identity/service_account.go` 的 8 条）；远端 Docker 26.1.5 PASS；MySQL 8.4.11 启动、查询、
Windows TCP 连通及非 root 用户全量双前缀迁移 PASS；schema lock 147 文件、63 表、27 跨模块 FK PASS；
门禁成功/失败路径远端容器清理 PASS；Linux FIFO 真实文件系统测试 PASS。
Provisioning acceptance verification: tests/race/vet/golangci-lint PASS；root coverage 90.9%；generator 字节稳定；双 Go module verify PASS；30/30 OpenAPI operation 与 SCIM security/parameters/request-response media/success status PASS；完整 migration、`make test-coverage`、`make ci` 与真实 MySQL 8.4 PASS。MySQL 首次 Docker Hub pull 遇到 transient EOF，未改代码重试后通过；schema lock 保持 147 files / 63 IAM tables / 27 cross-module FKs。最终 deep-review/deep-audit 与当前工作区 `git diff --check` PASS；确认未修改 composition root。
Federation acceptance verification: generator SHA-256 前后不变；focused tests、`-race`、vet、golangci-lint PASS；root coverage 91.4%，完整 coverage 90.0%；主模块 `go mod tidy -diff` clean，双 Go module verify/tidy、完整 migration、最终 `make ci` 与真实 MySQL 8.4 PASS；实际 OpenAPI 17/17；三 locale JSON/BOM 检查 PASS；schema lock 保持 147 files / 63 IAM tables / 27 cross-module FKs。补充 `AuthnRequestsSigned=true` 注册拒绝回归，错误类别与信息均验证；provider tenant boundary、SAML browser registration、replay duplicate claim 三项 mutation 均使目标测试失败，恢复后 focused tests/race/lint PASS；MySQL 首次 Docker Hub pull 遇到 transient EOF，未改代码重试后通过；未修改 composition root。
Task 9.1 verification: `go test ./lifecycle -run '^TestIAMLifecycleAcrossEnginesAndPrefixes$' -count=1` PASS，六案例均在最终 audit hash 修复后通过；完整 tests module `go test ./... -count=1` PASS；相关 root packages、audit/federation/oauth race、双模块 vet/tidy、`make lint`、`make fmt-check` 与工作区 `git diff --check` PASS。MySQL lifecycle 使用 go-mysql-server TCP dialect harness，不冒充真实 MySQL 8.4；真实 MySQL 终门禁仍属于 Task 9.7。
Task 9.1 final CI: `make ci` PASS；gofmt/lint PASS，总覆盖率 90.0%，完整 tests module PASS（lifecycle 43.725s、migration 184.562s），production build PASS。
Task 9.2 verification: focused root packages PASS；六案例 `TestIAMMigrationStatusAcrossEnginesAndPrefixes` PASS（45.41s）；完整 tests module PASS（lifecycle 43.289s、migration 226.907s）；双模块 `go vet ./...` 与 `go mod tidy -diff`、`make fmt-check`、工作区 `git diff --check` 均 PASS。额外 bare IAM table mutation 被精确识别为第 64 表；reverse order 删除 `dlock` mutation 因该 flow 保持 clean 而按预期失败，恢复后 prefixed SQLite PASS。
Task 9.2 final CI: `make ci` PASS；gofmt/lint PASS，总覆盖率 90.0%，migration-status 93.3%，完整 tests migration 210.155s，production build PASS。
Task 9.3 verification: `go test ./architecture -count=1 -v`、`go vet ./architecture`、tests `go mod tidy -diff` 与工作区 `git diff --check` PASS；实际 `go list` graph 精确九个 IAM root 且 namespace 无 Go 文件。business-source 恒真、Resolve 记录停用、同名函数冒充 receiver、方法体引用但未 return 四项 mutation 均使目标测试按预期失败，恢复后 focused architecture PASS（1.622s）。首次 CI 遇到一次 authn SQLite `database is locked`，目标 HTTP test 连续 10 次复跑全过；随后两次完整 `make ci` 均 PASS，最终 architecture 2.660s、总覆盖率 90.0%、production build PASS。
Task 9.6 verification: `go test ./internal/app -count=1` 与 `go -C tests test ./performance -count=1 -v` PASS；PostgreSQL fresh medians authorization 277527ns、session 424381ns、SCIM bulk 2690495ns、audit append 350425ns，均低于冻结阈值。双模块 vet、gofmt、静态 baseline/parser tests 与工作区 `git diff --check` PASS。tenant predicate 与 session PK 两项 mutation 均使真实性能契约按预期失败，恢复后 PostgreSQL enforced 与真 MySQL 8.4 enforced PASS。Task 9.6 最终 `make ci` PASS；总覆盖率 90.0%，完整 tests module/performance 与 production build PASS。
Task 9.7 Windows verification: `make generate` 前后完整工作树 fingerprint `3990f894c5ed42b2ca8f7166f5f86a225ecbeabc` 不变；随后最终 `make ci` PASS（lint、总覆盖率 90.0%、全部模块 ≥90%、完整 tests module、production build）。`make test-mysql8` PASS：source tree/preflight、真实 MySQL 8.4 migration、四路径 performance、schema lock 147 files / 63 tables / 27 cross-module FKs 全绿。`make dev` 监听 8080/9090，`GET http://127.0.0.1:8080/healthz` 返回 HTTP 200 且整体 `SERVING`，interrupt 后 shutdown complete 且 8080 无 listener。README Testing 与 D13 已更新；双模块 `go mod tidy -diff` 和工作区 `git diff --check` PASS。未验证项仅为计划明确要求的原 Mac `make iam-preflight` + `make ci`。

## Next Action
回到计划指定的原 Mac，执行 `make iam-preflight` 与 `make ci` 并记录结果；两者 PASS 后勾选 Task 0.P Step 4、Task 9.7 与 Phase 9，完成最终接受复审。

## Last Checkpoint
2026-09-03 · PRE-PUSH WINDOWS CHECKPOINT / BLOCKED：deep-review 类扫描已收口，删除 4 个已被真实三方言迁移替代的 SQL placeholder，修正 README 对 IAM 未实现的过期描述，并清理未跟踪 coverage、bin、本地数据库与 `.temp` 产物。修复后的完整 `make ci` 再次 PASS：gofmt/lint、总覆盖率 90.0%、全部受控模块 >=90%、完整 tests module（migration 215.745s）与 production build 均通过。此前 `make generate`、真实 MySQL 8.4、live health、README/decision/state 与 diff/tidy 门禁继续有效；因 Task 0.P Step 4 明确要求 Mac 实机结果且禁止 Windows 替代，9.7 与 Phase 9 保持未勾选，等待 Mac 两条命令。
