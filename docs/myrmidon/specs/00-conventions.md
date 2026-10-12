# 00 · 共同契约（所有模块规格必须遵守）

本文件是 `PRD.md` 之下、各模块规格（`01`–`09`）之上的**共享契约**。模块规格不得自行重新定义本文件中的枚举、权限码、事件名、表归属与错误码段；需要变更时，在本模块规格末尾「契约变更请求」一节提出，由主规格统一修订。

---

## 1. 规格文档体系

| 文件 | 范围 | 后端模块 | 主要页面 |
|---|---|---|---|
| `00-conventions.md` | 共享契约 | — | — |
| `01-intake-portal.md` | 需求开口、来件、分诊、澄清、甲方门户 | `intake` | 来件、甲方门户 |
| `02-work-review.md` | 需求基线、验收标准、工作项、分派、完成声明、裁决、范围变更、拆解模板 | `delivery` | 工作项看板/详情、评审台 |
| `03-execution-evidence.md` | 执行与分支、交互会话、产物、证据、验证器与验证 | `execution` | 执行列表、交互会话工作台、证据/验证面板、远程工作区（预览 / 产物 / 文件 / 终端，D119） |
| `04-machines-runtime.md` | 机器接入（设备码）、心跳与通道、运行时与能力检测、模型目录、执行槽调度、API 网关 | `fleet` | 机器、机器详情（含远程访问设置，D119）、接入服务器 / 集群池（D119）、API 网关 |
| `05-agents-orchestration.md` | Agent 成员、生命周期与退役、主管 Agent 派单、Heavy 并行求解、频道 | `fleet` + `collab` | 成员与 Agent、Agent 详情、会话 |
| `06-knowledge-memory.md` | 知识库、切块与检索、记忆与治理、上下文注入 | `knowledge` | 知识库、Agent 记忆 |
| `07-projects-delivery.md` | 项目（阶段、团队模式、预算）、交付包、甲方验收、返工来件 | `delivery` | 项目、交付 |
| `08-governance.md` | 权限目录与矩阵、授权委托、升级规则、审计、通知、总览与「我的裁决」 | `governance`（业务）+ 底座 IAM | 授权与策略、审计、总览、我的裁决 |
| `10-goals-reports.md` | 目标拆解、周期计划、日/周/月/季/年报、上下核对与确认（D111） | `planning` | 目标树、我的计划、报告编辑器、报告汇总、报告中心 |
| `11-domain-templates.md` | 领域模板包：软件（云效对齐）、视频、小说、团队任务；产物 schema、验证器插件、验收评分、评测集（D112） | `templates` | 模板库、模板详情、领域工作台 |
| `12-yunxiao-alignment.md` | 云效对齐与集成：工作项类型与字段、状态组、流转矩阵与条件、需求评审单、用例即验收标准、工时、导入、Codeup / Flow 证据、MCP（D112） | `delivery` + `integration` | 流转配置、评审单、用例、集成设置 |
| `09-platform-capabilities.md` | 平台通用能力：后台任务队列、领域事件 outbox、实时推送 SSE、对象存储驱动、通知渠道、模块菜单贡献 | 底座 `pkg/*` / `internal/infra/*` | — |

---

## 2. 每份模块规格的固定章节

每份 `01`–`09` 必须且仅按以下顺序组织（无内容的章节写「不适用」并说明原因）：

1. **范围与目标**：本规格解决什么、不解决什么（边界列给其他规格）。
2. **用户故事**：`US-<规格号>-<nn>`，格式「作为〈角色〉，我要〈动作〉，以便〈价值〉」，标注 MVP / v1.1 / v2。
3. **领域模型与数据表**：每张表给出完整字段表（列名、类型、可空、默认、约束、说明）、索引、唯一约束、外部引用；三方言差异（PostgreSQL / MySQL / SQLite）逐项写明。
4. **状态机**：状态 × 事件迁移表（当前状态、触发事件/命令、守卫条件、目标状态、副作用：事件/审计/通知），以及非法迁移的错误码。
5. **业务规则与边界情况**：编号 `BR-<规格号>-<nn>`；必须覆盖并发、幂等、超时、离线、权限不足、数据不存在、配额与上限。
6. **接口契约**：每个操作给出 方法、路径、OpenAPI tag、权限码、守卫函数（`authz.Register*`）、请求体字段、响应 `data` 字段、错误码、幂等与乐观锁要求；列表接口给出 `pkg/query` 可过滤/可排序字段。
7. **后台任务与事件**：任务名、触发、频率、互斥方式、重试；发布/消费的领域事件。
8. **页面规格**：每页按「目的 / 入口与路由 / 布局区块 / 字段与组件（含校验）/ 状态：加载·空·错误·无权限·离线 / 交互与按钮可用条件 / 实时更新 / 移动端差异 / 文案要点」。
9. **通知**：事件 → 接收人 → 渠道 → 文案模板 → 去重与合并规则。
10. **验收标准**：`AC-<规格号>-<nn>`，Given / When / Then，并标注测试层（unit / integration / e2e / manual）与关联的 US、BR。
11. **度量与埋点**：事件名与属性（不得包含代码与消息正文）。
12. **依赖、风险与待决**：依赖的其他规格与平台能力；待决问题附建议。
13. **契约变更请求**：对 `00` 的新增/修改请求（无则写「无」）。

---

## 3. 数据约定

| 项 | 约定 |
|---|---|
| 主键 | `id BIGINT`，底座雪花 ID（`pkg/guid.ID`），JSON 以十进制字符串传输；**禁止 UUID**作为业务主键 |
| 租户 | 每张业务表必有 `tenant_id BIGINT NOT NULL`，所有查询必须带租户条件 |
| 项目 | 项目 = 底座 Entity（`type='project'`）；项目内资源带 `project_id BIGINT NOT NULL`（即 entity id），用于 `authz.RegisterEntity` 行过滤 |
| 表名 | 模块前缀 + 复数（不得出现无前缀的表名；来件主表为 `intake_records`）：`intake_*`、`delivery_*`、`execution_*`、`fleet_*`、`collab_*`、`knowledge_*`、`governance_*`、`planning_*`、`integration_*`（12 Part B，CR-12-02）；经 `gormx.TableName` 加部署表前缀；迁移对象名带 `{{.Prefix}}` |
| 迁移 | 三方言目录 `sql/{postgres,mysql,sqlite}/`，goose 版本表 `goose_<module>`（如 `goose_integration`，CR-12-02） |
| 基础设施表 | 09 的平台表（任务、事件、幂等、通知）允许 `tenant_id=0` 表示系统级、允许 `*_at_ms BIGINT` 存租约/调度时间（与数据库时钟比较）、允许按保留期物理清理。另：11 的一方（内置）模板及其版本、`SEED` 评测用例使用 `tenant_id=0` 表示系统级只读数据（租户只能读与复制，不能改；v2.0，CR-11-06）。另：12 Part B 的 `integration_inbound_events` 作为**外部入站**去重表例外（云效 Webhook 等系统外来源按 `delivery_key` 去重）；09 `domain_event_receipts` 只用于内部领域事件（v2.0，CR-12-08）。另（D120，CCR-05-11 / 15）：05 的 Agent 操作清单版本 `fleet_operation_manifest_versions` 与工具目录推荐预设 `fleet_tool_presets` 以 `tenant_id=0` 存系统级只读数据；05 的 Agent 收件箱 `fleet_agent_inbox_events` 是可由各模块事实重建的派生投影，允许按 `expires_at` 物理清理 |
| 时间列 | PostgreSQL `TIMESTAMPTZ(3)`、MySQL `DATETIME(3)`、SQLite `DATETIME`，一律存 UTC（**禁止** MySQL `TIMESTAMP`，见 backend-module 规约与 mysql8 门禁）|
| 审计列 | `created_at`, `updated_at`（时间列类型同上），`created_by`, `updated_by`（principal id，可空=系统） |
| 乐观锁 | 可变聚合根带 `version INT NOT NULL DEFAULT 1`；读取响应返回 `ETag`，更新请求必须带 `If-Match`（与 `internal/todo` 一致）：缺失返回 428，不匹配返回 409 + 本模块 `<段>409` |
| 删除 | 业务记录**不物理删除**；需要隐藏的用 `archived_at`；证据、裁决、审计永不删除（擦除义务走底座 principallifecycle 匿名化） |
| 文本长度 | 标题 ≤200，短理由 ≤2000，长文本（描述、正文）≤64KB；超出存文件模块并以 ResourceBinding 引用 |
| JSON 列 | PostgreSQL `JSONB`、MySQL `JSON`、SQLite `TEXT`；必须定义 JSON Schema（写在规格内） |
| 枚举列 | `VARCHAR(32)` 存大写蛇形值；数据库 CHECK 约束（MySQL 8.0.16+ 支持）+ 代码校验双重。**跨模块共享**的枚举只能在本文件 §5 定义；**仅模块内部使用**的枚举（如 `RequirementStatus`、`AssignmentStatus`、`BlockReason`、`PauseReason`、`EnrollmentStatus`、`AcceptanceSource`）在所属模块规格中定义，不得出现在其他模块的表或事件载荷中 |
| 金额 | `cost_micros BIGINT`（百万分之一美元），展示时换算；币种固定 USD（MVP） |
| 时长 | 毫秒 `*_ms BIGINT` |
| 文件 | 附件统一存底座 `files`，业务表只存 `file_node_id` 或通过 `file_resource_bindings`（`purpose`、`resource_kind`、`resource_id`）绑定 |
| 外部引用 | 跨模块只存 ID，不建跨模块外键；读取通过 §8 的 `module.Resolve` 接口 |

---

## 4. 主体（Actor）模型

| actor_kind | 含义 | 身份来源 | 存储 |
|---|---|---|---|
| `HUMAN` | 乙方团队人类成员 | 底座 Principal（人类） | `principal_id` |
| `AGENT` | 乙方 Agent 成员 | 底座**服务账号** Principal（每个 Agent 一个） | `principal_id` + `agent_id` |
| `MACHINE` | 机器（执行面） | 底座服务账号（机器凭证） | `principal_id` + `machine_id` |
| `CLIENT` | 甲方联系人（系统外） | 来件联系人记录，**不是** Principal | `intake_contact_id` |
| `SYSTEM` | 系统自动动作（定时任务、规则） | — | 空 + `system_reason` |

所有"由谁做的"字段统一为三元组：`actor_kind`、`actor_id`（principal id 或 contact id，SYSTEM 为空）、`authority_source`（见 §6.3）。

---

## 5. 共享枚举（唯一定义）

| 枚举 | 值 | 所有者规格 |
|---|---|---|
| `TeamMode` | `HYBRID`, `AUTONOMOUS`, `HUMAN_ONLY`（纯人工 + AI 协助：Agent 只能做助理，不能做执行者或内部验收者） | 07 |
| `ProjectStage` | `INTAKE`, `DISCOVERY`, `PLANNING`, `ACTIVE`, `BLOCKED`, `ACCEPTANCE_PENDING`, `DELIVERED`, `CLOSED`, `CANCELLED` | 07 |
| `IntakeStatus` | `RECEIVED`, `TRIAGED`, `CLARIFYING`, `CLIENT_APPROVAL`（变更申请待甲方确认，仅模块内 kind=CHANGE，D117）, `CONVERTED`, `MERGED`, `DECLINED` | 01 |
| `IntakeChannelKind` | `PORTAL`, `EMAIL`, `API`, `WEBHOOK`, `IM` | 01 |
| `WorkItemState` | `DRAFT`（就绪闸门未通过，不可派发给 Agent）, `READY`, `CLAIMED`, `IN_PROGRESS`, `PLAN_REVIEW`（计划待批准）, `AGENT_CLAIMED_DONE`, `VERIFYING`, `INTERNAL_QA`（测试 Agent 与产品验收 Agent 内部验收中）, `NEEDS_HUMAN_REVIEW`, `ACCEPTED`, `REWORK`, `BLOCKED`, `EXECUTION_FAILED`, `STOPPED`（Andon 停线）, `EXCEPTION`（容差越界待决定）, `CANCELLED` | 02 |
| `WorkItemType` | `FEATURE`, `BUG`（= 云效「缺陷」）, `CHORE`, `DOC`, `RESEARCH`, `DESIGN`, `TEST`, `TASK`, `RISK`, `INCIDENT`（线上故障）, `CONTENT`（视频 / 小说等内容产物） | 02 |
| `StateGroup` | `TODO`, `IN_PROGRESS`, `VERIFYING`, `DONE`, `CANCELLED`（自定义状态必须归属其一；报表按组汇总） | 12 |
| `VerificationStage` | `AUTOMATED`（平台重跑确定性检查）, `AGENT_TEST`（测试 Agent）, `AGENT_ACCEPTANCE`（产品验收 Agent）, `HUMAN_REVIEW`（团队裁决 L3）, `CLIENT_ACCEPTANCE`（甲方 L4） | 03 |
| `ExceptionDecision` | `CONTINUE`, `RESHAPE`, `HOLD`, `KILL` | 02 |
| `EscalationReason` | `INTERNAL_ROUNDS_EXHAUSTED`, `REQUIREMENT_CONFLICT`, `ANDON`, `TOLERANCE_EXCEPTION`, `BUDGET`, `MANDATORY_HUMAN_GATE` | 02 |
| `TemplateDomain` | `SOFTWARE`, `VIDEO`, `NOVEL`, `TEAM_TASKS` | 11 |
| `StatusConfidence` | `GREEN`, `YELLOW`, `RED` | 10 |
| `RiskLevel` | `LOW`, `MEDIUM`, `HIGH` | 02 |
| `AssignmentRole` | `EXECUTOR`, `REVIEWER` | 02 |
| `DecisionAction` | `APPROVE`, `REJECT`, `REQUEST_CHANGES`, `PAUSE`, `RESUME`, `SCOPE_CHANGE`, `ASSIGN`, `CANCEL`, `DELIVER` | 02（DELIVER 由 07 使用） |
| `FailureCategory` | `EXECUTION_FAILURE`, `QUALITY_REJECTION`, `SCOPE_CHANGE`, `STRATEGY_CHANGE`, `BLOCKED` | 02 |
| `FeedbackCategory` | `FUNCTIONAL_DEFECT`, `REQUIREMENT_MISMATCH`, `STYLE`, `PERFORMANCE`, `SECURITY`, `OTHER` | 02 |
| `ExecutionState` | `CREATED`, `RUNNING`, `PAUSED`, `SUCCEEDED`, `FAILED`, `INTERRUPTED`, `CANCELLED` | 03 |
| `ExecutionKind` | `AUTONOMOUS`（Agent 无人值守）, `INTERACTIVE`（交互会话）, `ADHOC`（未挂工作项的临时执行） | 03 |
| `BranchReason` | `INITIAL`, `RETRY_INFRA`, `QUALITY`, `STRATEGY`, `ENSEMBLE`, `SCOPE` | 03 |
| `InteractionKind` / `InteractionState` | `TOOL_PERMISSION`, `RUNTIME_INPUT` / `PENDING`, `DECIDED`, `DELIVERED`, `EXPIRED`, `CANCELLED` | 03 |
| `EvidenceType` | `COMMIT`, `PULL_REQUEST`, `TEST_REPORT`, `LOG`, `SCREENSHOT`, `OBSERVATION`, `ARTIFACT` | 03 |
| `VerificationLayer` | `AUTOMATED`, `AI_ASSISTED`, `HUMAN` | 03 |
| `VerificationResult` | `PENDING`, `PASSED`, `FAILED`, `INCONCLUSIVE` | 03 |
| `MachineState` | `ONLINE`, `OFFLINE`, `DRAINING`, `REVOKED`（接入过程的待确认状态属于模块内枚举 `EnrollmentStatus`；确认后的机器初始为 `OFFLINE`，建立会话后变 `ONLINE`） | 04 |
| `RuntimeKind` | `CLAUDE_CODE`, `CODEX`（开放扩展，新增需契约变更） | 04 |
| `MachineType` | `WORKSTATION`（设备码接入的个人电脑）, `SERVER`（无头守护进程，预签发服务账号凭证）, `K8S_POOL`（集群池：每个执行一个临时 Pod，v1.1）（D119） | 04 |
| `AgentState` | `DRAFT`, `ACTIVE`, `STOPPED`, `CRASHED`, `RETIRING`, `RETIRED` | 05 |
| `AgentRole` | `EXECUTOR`, `REVIEWER`, `COORDINATOR`, `LEAD`（UI 称「主管 Agent」）, `TESTER`（测试 Agent）, `ACCEPTOR`（产品验收 Agent）, `ASSISTANT`（纯人工模式下的助理） | 05 |
| `GoalLevel` / `GoalStatus` | `TEAM`, `PROJECT`, `MEMBER` / `DRAFT`, `ACTIVE`, `AT_RISK`, `ACHIEVED`, `MISSED`, `CANCELLED` | 10 |
| `PeriodKind` | `DAY`, `WEEK`, `MONTH`, `QUARTER`, `YEAR` | 10 |
| `ReportLevel` / `ReportState` | `MEMBER`, `PROJECT`, `TEAM` / `DRAFT`, `CHECKING`, `BLOCKED`, `READY`, `SUBMITTED`, `CONFIRMED`, `RETURNED` | 10 |
| `KRSource` / `CheckSeverity` | `AUTO`, `MANUAL` / `BLOCKING`, `NON_BLOCKING` | 10 |
| `AgentOrigin` | `HUMAN_CREATED`, `AGENT_SPAWNED`（D110：主管 Agent 在编制授权内创建；收编后仍为 `AGENT_SPAWNED`，另记 `adopted_by`） | 05 |
| `SpawnGrantState` | `ACTIVE`, `EXHAUSTED`, `EXPIRED`, `REVOKED` | 08 |
| `DispatchPolicy` | `MANUAL`, `AUTO` | 05 |
| `KnowledgeScope` | `TEAM`, `PROJECT`, `AGENT` | 06 |
| `KnowledgeSourceKind` | `REQUIREMENT`, `DECISION`, `FEEDBACK`, `DELIVERY`, `DOCUMENT`, `SKILL`, `MEMORY` | 06 |
| `MemoryKind` | `EXPERIENCE`, `RULE` | 06 |
| `DeliveryState` | `DRAFT`, `READY`, `SENT`, `ACCEPTED`, `REJECTED`, `PARTIALLY_ACCEPTED` | 07 |
| `AcceptanceResult` | `ACCEPTED`, `REJECTED`, `PARTIALLY_ACCEPTED` | 07 |
| `DelegationState` | `ACTIVE`, `EXPIRED`, `REVOKED` | 08 |
| `AnchorKind` | `CODE_LINES`, `TABLE_ROW`, `HTML_REGION`, `MEDIA_TIME`（v1.1）, `TEXT_PARAGRAPH`（小说章节为 v1.1）（D120 锚定批注） | 03（02 / 07 / 12 读取） |
| `CommentVisibility` | `INTERNAL`（团队）, `CLIENT`（甲方在面向甲方的交付产物上创建）（D120） | 03（07 读取） |

---

## 6. 权限码（底座 `authz` 目录，`resource_verb`，Scope=`tenant`）

### 6.1 目录

| 权限码 | 含义 | DataScoped（按项目） | 注册模块 |
|---|---|---|---|
| `intake_read` / `intake_triage` / `intake_convert` / `intake_decline` | 查看 / 分诊与澄清 / 转为需求 / 拒绝来件 | 否（团队级） | intake |
| `intake_submit` | API 开口提交来件（仅见本主体提交的来件；默认角色 `intake_api_client`） | 否 | intake |
| `intake_channel_manage` | 管理开口（门户、邮箱、API） | 否 | intake |
| `project_read` / `project_manage` / `project_stage_change` / `project_mode_change` | 查看 / 编辑设置 / 推进阶段 / 切换团队模式 | 是 | delivery |
| `requirement_read` / `requirement_write` | 需求与验收标准 | 是 | delivery |
| `work_item_read` / `work_item_write` / `work_item_assign` | 工作项读写 / 分派 | 是 | delivery |
| `work_item_execute` | 执行已分派工作项、提交完成声明 | 是 | delivery |
| `work_item_cancel` / `decomposition_template_manage` | 取消工作项 / 管理拆解模板 | 是 / 否 | delivery |
| `decision_approve` / `decision_reject` / `decision_request_changes` | 裁决 | 是 | delivery |
| `decision_pause` | 暂停 / 恢复工作项或项目 | 是 | delivery |
| `scope_change` | 范围变更（**不可委托**） | 是 | delivery |
| `delivery_read` / `delivery_compose` / `delivery_send` | 交付包查看 / 编辑 / 正式交付（send **不可委托**） | 是 | delivery |
| `execution_read` / `execution_start` / `execution_stop` | 执行 | 是 | execution |
| `interaction_respond` | 回应工具授权 / 输入请求 | 是 | execution |
| `evidence_submit` / `verification_run` | 提交证据 / 运行验证 | 是 | execution |
| `code_merge` | 合并到主分支（可委托，有条件） | 是 | execution |
| `verifier_manage` | 管理验证器定义 | 否 | execution |
| `machine_read` / `machine_manage` / `machine_revoke` | 机器 | 否 | fleet |
| `gateway_manage` | API 网关（实验） | 否 | fleet |
| `agent_read` / `agent_manage` / `agent_retire` | Agent | 否 | fleet |
| `agent_spawn` | 主管 Agent 创建子 Agent（**不经角色授予**；仅对持 ACTIVE 编制授权的 LEAD Agent 经 08 策略规则 `spawn_grant` 生效） | 否 | fleet |
| `goal_read` / `goal_manage` / `goal_member_manage` | 目标查看 / 团队与项目级目标管理 / 成员目标管理（本人与上级） | 是（项目级）| planning |
| `plan_submit` / `plan_confirm` | 提交周期计划 / 确认下级计划（**仅限人类**） | 是 | planning |
| `report_read` / `report_submit` / `report_confirm` | 报告查看 / 提交 / 确认或退回（**仅限人类**：上级或 Agent 的负责人） | 是 | planning |
| `planning_settings_manage` | 报告时间、宽限期、模板 | 否 | planning |
| `spawn_grant_manage` | 授予 / 修改 / 撤销编制授权（名额、预算、可用角色/机器/模型、存活期、审批方式）；仅所有者、**不可委托**、需 step-up | 否 | governance |
| `channel_read` / `channel_post` / `channel_manage` | 频道 | 是 | collab |
| `knowledge_read` / `knowledge_write` / `memory_approve` / `knowledge_manage` | 知识库 / 记忆审批 / 知识库设置（检索参数、嵌入配置、重建索引） | 是（PROJECT scope）/ 否（TEAM） | knowledge |
| `policy_read` / `policy_manage` / `delegation_grant` / `takeover` | 策略 / 委托 / 一键接管 | 否 | governance |
| `audit_read` | 业务审计查看与导出（复用底座审计权限亦可） | 否 | governance |
| `system_job_view` / `system_job_manage` / `system_event_view` / `system_event_manage` | 平台任务队列与事件 outbox 的运维查看 / 重试与丢弃 / 重放（Scope=系统级，非租户） | 否 | 平台(09) |
| `production_access` | 生产环境访问（经底座访问申请临时授予，最长 8h，**不可委托**） | 是 | governance |
| `workflow_manage` | 流转配置（状态定义、流转矩阵与条件、自动化规则）、字典、迭代 / 版本 / 里程碑管理（12，CR-12-03） | 是（项目覆盖）/ 否（团队默认） | delivery |
| `review_initiate` / `review_participate` | 发起需求评审单 / 参与评审（`PASS` / `REJECT` 意见**仅限人类**，§6.5） | 是 | delivery |
| `test_case_manage` / `test_plan_manage` / `test_run_execute` | 用例管理 / 测试计划与报告 / 执行用例 | 是 | delivery |
| `worklog_submit` / `worklog_approve` | 填报工时（本人）/ 审批工时（**仅限人类，且非本人**） | 否 | delivery |
| `integration_manage` | 云效集成连接、映射、导入（已在 §15.5 登记；**仅限人类、需 step-up** `integration_credentials`，CR-12-03） | 否 | integration |
| `terminal_interact` / `workspace_file_edit` / `preview_share_approve` | 交互终端（**不可委托、仅限人类**、需 step-up `terminal_interactive`）/ 人工接管后编辑执行工作区文件（仅限人类）/ 批准甲方预览分享链接（**不可委托、仅限人类**，不得批准本人申请）（D119；查看预览、文件与只读终端沿用 `execution_read`） | 是 | execution |
| `machine_access_manage` | 每机远程访问开关：允许远程预览 / 远程终端 / 人工接管后编辑文件（**不可委托、仅限人类**；默认仅 `team_owner`；开启终端需 step-up `machine_terminal_enable`）（D119） | 否 | fleet |
| `agent_workspace_read` | 查看 Agent 的工作目录、收件箱、重置记录与记忆（**仅限人类**；默认仅 `team_owner`，Agent 的负责人类经关系授权隐式持有）（D120） | 否 | fleet |
| `artifact_comment_write` | 在产物查看器中新建与回复锚定批注（读取沿用 `execution_read`）（D120） | 是 | execution |
| `interaction_request` | 在自己运行中的执行内发起工具授权 / 输入请求（回应仍需 `interaction_respond`，仅限人类）；默认授予全部会运行执行的角色（D120） | 是 | execution |
| `tool_catalog_manage` | 管理团队工具目录（MCP 服务器与技能条目、版本、授予、凭据）（**不可委托、仅限人类**；默认仅 `team_owner`；设置 / 轮换凭据另需 step-up `tool_credential_manage`；读取目录沿用 `agent_read`）（D120） | 否 | fleet |

### 6.2 默认角色（随租户初始化，经底座 `core` 角色创建）

> **各角色的精确码表以 `08-governance.md` §3.3 为准**（本表为摘要）。种子角色另含：`production_operator`（仅 `production_access`，经访问申请获得）与 5 个 `delegated_*` 载体角色（委托的承载绑定，只有存在对应有效委托行时才生效）。

| 角色键 | 授予 |
|---|---|
| `team_owner` | 全部 |
| `delivery_lead` | 除 `machine_revoke`、`policy_manage`、`takeover`、`gateway_manage`、`project_mode_change`、`delegation_grant` 外全部（团队模式切换与授予委托仅限所有者，PRD §12.2） |
| `engineer` | `*_read`、`work_item_execute`、`execution_*`、`interaction_respond`、`evidence_submit`、`verification_run`、`decision_request_changes`、`channel_post`、`knowledge_write` |
| `agent_executor` | 项目内 `*_read`（按分派）、`work_item_execute`、`execution_start/stop`（自己的）、`evidence_submit`、`verification_run`、`channel_post`、`knowledge_read`、记忆写入（经 06 规则） |
| `agent_reviewer` | `*_read`、`verification_run`、`channel_post`、`knowledge_read`；裁决类**仅经委托** |
| `agent_coordinator` | `intake_read`、`intake_triage`、`*_read`、`channel_post`、`knowledge_read`（不授 `work_item_assign`） |
| `agent_lead` | `*_read`、`channel_post`、`knowledge_read`；分派与 ENSEMBLE 分支**不经角色码**，而经策略规则 `POLICY:dispatch_auto` / `POLICY:ensemble_branch`（05 贡献、08 判定） |

> **D111 默认授予（CR-10-04，精确码表见 08 §3.3）**：`team_owner` 全部 planning 码；`delivery_lead` 除 `planning_settings_manage` 外全部；`engineer` `goal_read`、`goal_member_manage`（本人）、`plan_submit`、`report_read`（本人与项目）、`report_submit`；各 Agent 角色仅 `goal_read`、`plan_submit`、`report_submit`（仅本人）。`plan_confirm`、`report_confirm` 仅限人类（§6.5）。

### 6.3 `authority_source` 取值

`ROLE:<role_key>`（种子角色键；底座管理员为 `tenant_administrator`；自定义角色为 `custom.<role_id>`）、`DELEGATION:<delegation_id>`、`POLICY:<rule_id>`（自动规则，如 `POLICY:dispatch_auto`、`POLICY:ensemble_branch`；具体提案 id 放 `detail.dispatch_proposal_id`）、`CLIENT:<intake_contact_id>`（甲方回执）、`SYSTEM:<job_name>`、`EXTERNAL:<provider>:<ref>`（外部系统裁决的只读镜像，如 `EXTERNAL:YUNXIAO_FLOW:<pipeline>`；仅作证据显示，**不能满足 L3**，CR-12-07）。

### 6.4 不可委托动作（硬编码，不可配置）

`scope_change`、`delivery_send`、`project_mode_change`、`policy_manage`、`delegation_grant`、`takeover`、`machine_revoke`、`intake_decline`、`production_access`、`spawn_grant_manage`、来件转换中的**新建项目**（伪码 `project_create`，`NEW_PROJECT`）与**追加到既有需求**（`APPEND`，等同范围变更）、`memory_approve`、生产环境访问、`terminal_interact`、`preview_share_approve`、`machine_access_manage`（D119）、`tool_catalog_manage`（D120）。

### 6.5 仅限人类的动作（即使有委托，Agent / 服务账号 / PAT 也不得执行）

回应工具授权与输入请求（`interaction_respond`）；项目阶段迁移到 `DELIVERED` / `CLOSED` / `CANCELLED`；修改预算上限；登记线下验收；正式交付（需 step-up，结构上排除 PAT 与服务账号）；确认机器接入；批准 RULE 记忆；批准主管 Agent 的创建请求；收编（adopt）AI 创建的 Agent；确认或退回下级的周期计划与报告（`plan_confirm`、`report_confirm`）；需求评审单的 `PASS` / `REJECT` 意见（Agent 意见只作 AI 建议，不计数）；工时审批（`worklog_approve`，且不得审批本人）；集成凭据管理（`integration_manage`，需 step-up）（CR-12-04）；执行映射到 `ACCEPTED` 的流转（含关闭缺陷；Agent 即使持 `TESTER` 角色也不可，`reason_code=AGENT_CANNOT_ACCEPT`，由 08 登记；「验证角色」= 持 `TESTER` 角色的**人类**成员或该工作项负责人，CR-12-09）；请甲方确认变更、登记甲方线下确认（01 T15 / T19，经 `scope_change` 判定，D117）；打开交互终端、人工接管后编辑工作区文件、批准甲方预览分享链接、修改机器远程访问开关、预签发服务器 / 集群机器凭证（D119）；Agent 完全重置（`agent_manage` + step-up `agent_full_reset`）、查看 Agent 的工作目录 / 收件箱 / 记忆（`agent_workspace_read`）、管理团队工具目录与设置 / 轮换其凭据（`tool_catalog_manage`）、创建与修改 Agent 的循环契约（10 BR-10-58）（D120）。甲方本人对变更的〔接受〕/〔不接受〕是**甲方行为**（`CLIENT:<intake_contact_id>`，只经门户会话），任何团队主体与 Agent 都不能代为做出。

> 12 增补（CR-12-07，由 08 登记）：收件箱类型 `REVIEW_SHEET`、`WORKLOG_APPROVAL`（08 §6.10）；step-up 用途 `integration_credentials`（08 §6.9 危险操作登记表）。

> D120 增补（由 08 登记，08 §3.3d / §3.5c / §6.9 / §8.6）：原因码 `TOOL_NOT_GRANTED`；操作清单结果类别 `CAPABILITY_NOT_AUTHORIZED` / `QUOTA_EXHAUSTED` 的映射（08 §3.5c）；step-up 用途 `agent_full_reset`、`tool_credential_manage`；菜单项 `tool-catalog`（团队 › 工具与技能）与 `agent-operations`（治理 › Agent 操作清单）。

---

## 7. 事件

### 7.1 领域事件（经 09 的 outbox 发布；命名 `<资源>.<过去式>`）

| 事件 | 发布者 | 主要消费者 |
|---|---|---|
| `intake.received`, `intake.triage_requested`, `intake.triaged`, `intake.clarification_requested`, `intake.clarification_answered`, `intake.clarification_expired`, `intake.converted`, `intake.merged`, `intake.declined`, `intake.client_approval_requested`, `intake.client_approval_decided`（D117） | intake | collab、governance(通知/我的裁决)、knowledge、fleet(唤醒协调员) |
| `project.created`, `project.stage_changed`, `project.mode_changed` | delivery(07) | collab、knowledge、governance |
| `requirement.versioned` | delivery | knowledge、delivery(工作项基线过期) |
| `work_item.created`, `work_item.state_changed`, `work_item.assigned`, `work_item.escalated` | delivery | execution、collab、governance、fleet(派单) |
| `claim.submitted` | delivery | execution(排队验证) |
| `decision.made` | delivery | governance(通知/我的裁决)、knowledge、collab（**返工分支不经此事件**：02 在裁决事务内直接调用 `ExecutionLauncher.Launch`，避免重复创建） |
| `execution.started`, `execution.state_changed`, `execution.cost_updated`（载荷必须含**累计**成本 `cumulative_cost_micros` 与执行者 `agent_id`）, `execution.merged` | execution | delivery(07 预算记账)、governance(08 编制预算累计)、collab |
| `interaction.requested`, `interaction.decided`, `interaction.closed` | execution | governance(我的裁决/通知) |
| `evidence.added`, `verification.completed` | execution | delivery(推进工作项) |
| `machine.state_changed`, `machine.runtimes_changed`（v2.0 载荷含工具变化 `tools_changed` 与能力标记 `capabilities`，04 BR-04-47/48） | fleet | execution（离线→INTERRUPTED）、fleet(agents)、governance、templates（工具恢复 → 清除 `TOOL_UNAVAILABLE`） |
| `agent.state_changed`, `agent.retired` | fleet | delivery(重新分派)、collab |
| `dispatch.proposed`（含 `policy`、`requires_human`）, `dispatch.applied`, `dispatch.rejected`（人工驳回提案）, `ensemble.compared` | fleet | collab、governance（派单收件箱项在 applied/expired/rejected/`work_item.assigned` 任一出现时关闭）；**delivery 不消费 `dispatch.applied`**（分派已经 `DispatchAssigner` 同步落库） |
| `delivery.sent`（含 `intake_ids[]`）, `acceptance.received`（含 `intake_ids[]`、`work_item_ids[]`、逐项结果与拒绝理由） | delivery(07) | intake(回执/创建返工来件)、governance、knowledge |
| `project.members_changed` | delivery(07) | collab（频道成员同步）、governance |
| `project.hold_changed`, `project.stage_ready`, `delivery.ready`, `delivery.release_notes_requested`, `delivery.withdrawn` | delivery(07) | governance、collab、knowledge；`delivery.release_notes_requested` 另由 fleet(05) 消费（向起草 Agent 下发 `agent.release_notes`） |
| `goal.created`, `goal.updated`, `goal.status_changed`, `plan.submitted`, `plan.confirmed`, `plan.returned`, `report.generated`, `report.checked`, `report.submitted`, `report.confirmed`, `report.returned`, `report.overdue` | planning(10) | governance（收件箱：计划确认、报告确认、报告被阻止、报告逾期）、knowledge（复盘改进沉淀）、collab |
| `agent.spawn_requested`, `agent.spawned`, `agent.orphaned`, `agent.adopted` | fleet(05) | governance（创建审批收件箱、编制用量、孤儿通知）、collab、knowledge |
| `spawn_grant.granted`, `spawn_grant.updated`, `spawn_grant.exhausted`, `spawn_grant.expired`, `spawn_grant.revoked` | governance(08) | fleet（停止子 Agent 接新执行 / 置孤儿） |
| `ensemble.plan_created`, `ensemble.cancelled` | fleet(05) | governance（Heavy 审批/选择收件箱项）、collab |
| `budget.threshold_reached`（载荷 `threshold_pct`） | delivery(07) | governance(升级规则/通知)、execution(暂停)、fleet(派单停止) |
| `memory.written`, `memory.approved`, `memory.rejected`, `memory.revoked`, `skill_version.approved` | knowledge | governance、fleet(agents) |
| `delegation.granted`, `delegation.revoked`, `delegation.expired`, `escalation.triggered`, `escalation.resolved`, `takeover.executed` | governance | delivery、fleet、collab |
| `verification.human_requested`（03）、`delivery.confirmation_requested`（07）、`dispatch.rejected`（05） | 各发布者 | governance（我的裁决） |
| `work_item.ready_checked`, `plan.proposed`, `plan.approved`, `plan.rejected`, `andon.pulled`, `andon.cleared`, `exception.raised`, `exception.decided`, `quality.escape_learning_proposed`（→ governance 回填展示列）, `quality.escaped`（发布者：02（`HUMAN_REVIEW`，T15 / T26）、07（`CLIENT_ACCEPTANCE`，甲方逐项拒收）、12 Part A（带 `escaped_from_stage` 的缺陷）；统一幂等键 `escape_key = (work_item_id, claim_id 或 acceptance_id)`，同一 `escape_key` 只发布一次，后到者引用已有穿透；03 不发布；穿透学习**只**由 02 `delivery.escape_learning` 执行，D114） | delivery(02，v2.0 §15；`quality.escaped` 另由 07 / 12 Part A 发布) | execution（`plan.approved` → 冻结验收集）、fleet（Agent 批准者派单、ACCEPTOR 经验）、governance（升级收件箱、Andon / 例外通知、穿透率）、collab；内部失败相关载荷 `visibility: "INTERNAL"`，人类可见投影必须过滤。注意与 10 的 `plan.submitted/confirmed/returned`（周期计划）区分 |
| `acceptance_suite.frozen`, `internal_qa.passed`, `internal_qa.failed`, `cheating.suspected` | execution(03，v2.0 §15) | delivery（经同步回调为主，事件作审计与投影）、fleet（`internal_qa.passed` → 下一阶段内部验收派单）、governance（作弊嫌疑通知与指标） |
| `template.version_published`, `template.version_deprecated`, `template.binding_changed`, `verifier.calibration_changed`, `eval.run_completed`, `provider.state_changed`, `rubric.divergence_detected` | templates(11) | governance（通知）、delivery（拆解默认值刷新；`provider.state_changed` → `PROVIDER_UNAVAILABLE`）、execution（`verifier.calibration_changed` → 启动等待已校准机器的 AUTOMATED 运行，D116） |
| `iteration.state_changed`, `version.locked`, `version.released`, `workflow.config_changed`, `review_sheet.opened`, `review_sheet.closed`, `test_run.recorded`, `test_report.generated`, `worklog.submitted`, `worklog.approved`, `worklog.returned`, `worklog.withdrawn`, `code_link.created`, `automation.run_failed`（CR-12-05） | delivery(12 Part A) | governance（通知；收件箱 `REVIEW_SHEET` / `WORKLOG_APPROVAL`）、integration（出站同步）、planning(10，工时与迭代)、collab |
| `integration.connection_state_changed`, `integration.import_finished`, `integration.sync_conflict`（CR-12-05） | integration(12 Part B) | governance（通知） |
| `machine.access_changed`（D119：远程访问开关或 WebSocket 可用性变化） | fleet(04) | execution（停止被关闭类别的预览 / 终端会话与编辑） |
| `share_link.requested`, `share_link.decided`, `share_link.ended`（D119，分期待定） | execution(03) | governance（收件箱 `PREVIEW_SHARE_APPROVAL` 的创建与关闭、通知） |
| `comment.thread_created`, `comment.posted`, `comment.thread_resolved`, `comment.converted`（D120 锚定批注；载荷不含正文与引文） | execution(03) | fleet（Agent 收件箱 MENTION / MESSAGE / REVIEW_FEEDBACK）、governance（通知） |
| `loop_contract.state_changed`, `loop_run.completed`, `loop_run.escalated`（D120 循环契约） | planning(10) | fleet（提醒与契约状态核对）、governance（通知） |
| `tool_catalog.changed`（D120 团队工具目录：版本发布、启停、授予变化、凭据轮换；不含配置与凭据） | fleet(05) | fleet(agents：`config_revision+1`)、governance（通知与审计索引） |

事件载荷统一：`{event_id, event_type, schema_version, stream_seq, tenant_id, project_id?, occurred_at, actor{kind,id,authority_source}, subject{kind,id,version}, topics[], correlation_id, causation_id, data{...}}`，`data` 字段在发布者规格中定义；同一聚合的事件保序（09 C2）。各发布者规格须声明每类事件可否经 SSE 推送完整载荷。

补充事件：

| 事件 | 发布者 | 主要消费者 |
|---|---|---|
| `agent.created`, `dispatch.expired`, `ensemble.started`, `ensemble.selected` | fleet(05) | delivery、collab、governance |
| `channel.created`, `channel.archived`, `message.posted` | collab | governance(通知/@我)、knowledge |
| `notification.delivery_state_changed`, `job.dead_lettered` | 平台(09) | intake(邮件投递状态)、运维 |

### 7.2 审计事件（底座 `iam/audit` 哈希链，`audit.Appender` 在业务事务内追加）

审计 `detail` 必含 `project_id`（如有）、`actor_kind`、`authority_source`（委托来源时含 `delegation_id`）。必须审计：所有 Decision、委托授予/撤销/使用、范围变更、交付发送、甲方回执、机器接入确认/吊销、Agent 创建/退役、记忆规则审批、团队模式切换、一键接管、工具授权回应、YOLO（全自动授权）开启。`event_type` 用 `<module>.<action>`，如 `delivery.decision_approved`；`detail` 至少含 `reason`、`evidence_refs[]`、`authority_source`。

---

## 8. 跨模块接口（`module.Provide` / `module.Resolve`）

| 接口 | 提供者 | 方法（语义） | 使用者 |
|---|---|---|---|
| `WorkItemReader` | delivery | 读工作项、验收标准、分派、状态 | execution、fleet、knowledge、collab |
| `WorkItemTransitioner` | delivery | 系统驱动迁移（如验证完成 → NEEDS_HUMAN_REVIEW） | execution |
| `ExecutionLauncher` | execution | 为分派创建执行（含分支原因与父执行） | delivery、fleet |
| `EvidenceReader` | execution | 读证据与验证结果 | delivery、knowledge |
| `MachineDirectory` | fleet | 机器、运行时、能力、执行槽 | execution、fleet(agents) |
| `AgentDirectory` | fleet | Agent 资料、角色、负载、绩效统计 | delivery、collab、governance |
| `Retriever` | knowledge | 按 scope 检索、生成注入包 | execution、fleet(lead) |
| `AuthorityChecker` | governance | `Check(ctx, tx, req)`：按 08 §3.5 顺序判定（默认拒绝 → 主体有效 → 硬约束 → 职责分离 → 角色 → 不可委托闸门 → 委托 → 策略 → 拒绝），只读、在调用方事务内对委托行加共享锁；`ListEligible` 列出可裁决人。预算检查用 07 `ProjectGate`（不设 governance `BudgetGuard`） | delivery、execution、fleet、intake |
| `RequirementCreator`（CreateFromIntake / AppendFromIntake）, `WorkItemReopener.ReopenFromAcceptance`, `DecisionReader` | delivery(02) | 在调用方事务内由来件创建需求 / 追加（范围变更）；按甲方拒收重开工作项（T26）；读裁决与结构化反馈 | intake、knowledge |
| `ChangeImpactPreviewer`（Preview，只读，D117） | delivery(02) | 按验收标准差异预览受影响工作项（STALE / NEW）与需求当前版本；`RequirementCreator.AppendFromIntake` 另接受甲方变更载荷并在同事务调整容差基线 | intake |
| `ToleranceReader`（ForSubject / WouldBreach）, `ToleranceAdjuster`（ApplyApprovedChange，D117） | delivery(07) | 读需求 / 项目级容差；只读判定变更增量会使哪些维度越界；在调用方事务内按甲方确认的变更调整基线（未决例外时拒绝） | delivery(02)、governance、intake |
| `ClarificationRequester` | intake | 由工作项阻塞发起澄清 | delivery |
| `IntakeReader`（Get / Contacts / ChannelAvailable / OpenClarifications / AttentionByDay / RawText）, `IntakeReplySender`（SendDelivery / SendAcceptanceReminder，唯一的甲方通知方） | intake | 读来件与联系人；经原开口发送交付与验收提醒 | delivery(07)、knowledge |
| `ProjectCreator`, `ProjectReader`, `ProjectGate`, `ProjectModeSwitcher`, `ProjectHoldSetter`, `RepositoryReader`, `DeliveryMetricsReader` | delivery(07) | 建项目（事务内）、读项目档案与阶段/模式/预算、执行前闸门、接管切回混编、设置暂停、仓库路径、指标 | intake、execution、fleet、governance |
| `AcceptanceRecorder` | delivery(07) | 甲方查看交付包并提交验收 | intake |
| `ExecutionDispatchHandler`, `EvidenceBinder`, `ExecutionReader`（ActiveCount / AttentionByDay / Get）, `EvidenceReader.SecretFlags/WorkItemCost`, `ExecutionLauncher.CancelExecution` | execution | 机器通道上的执行消息处理、证据文件绑定、执行读取、按执行取消 | fleet、delivery |
| `ClaimSubmitter`（`SubmitOn(ctx, tx, …)`，在调用方事务内执行 T08） | delivery(02) | 代 Agent 提交完成声明 | execution、fleet |
| `MachineChannelNotifier`；执行槽经 `MachineDirectory.ReserveSlot/ReleaseSlot`；`MachineDirectory.Summary` | fleet | 唤醒机器长轮询；在调用方事务内占用/释放执行槽；总览机器摘要 | execution、governance |
| `ChannelProvisioner` | collab | 在建项目事务内创建默认频道 | delivery(07) |
| `DispatchAssigner`（**同步**，在 fleet 事务内调用：Assign / Reassign / Release） | delivery(02) | 应用派单提案；02 的拒绝（如并发认领）同步返回给确认人；02 不再异步消费 `dispatch.applied` 来分派 | fleet(05) |
| `DispatchProvenance`, `ChannelPoster` | fleet(05) / collab | 查询某分派是否源自主管 Agent（02 据此填 `DispatchedByActor` 供职责分离判定）；系统向频道发卡片 | delivery |
| `SkillMounter`, `MemoryPolicyValidator` | knowledge | 事务内挂载技能；校验 Agent 记忆策略 | fleet(05) |
| `DelegationRevoker`, `PreferenceResolver`, `ListEligible` | governance | 撤销全部委托；通知偏好；可裁决人列表 | fleet、delivery、09 |
| `RecipientDirectory` | 底座 identity | 收件人邮箱、语言、显示名 | 09 |
| `MemoryWriter`, `SkillCatalog`, `Retriever.RecordInclusion/RecordCitations` | knowledge | 记忆写入（经治理）、技能库（上下文第 6 层的唯一来源）、注入记录 | execution、fleet |
| `VectorConnector` | knowledge | K2 向量扩展点 | knowledgevector（K2 模块） |
| `IntakeProgressReader`, `AcceptanceReader` | delivery(07) | 门户里程碑进度；验收详情 | intake、knowledge |
| `GoalReader`（ActiveForPrincipal / KRContributors）, `ReportReader`（Get / OnTimeRate / Coverage） | planning(10) | 成员当前目标与 KR 贡献者；报告读取、准时率、覆盖率 | fleet(05 起草上下文)、knowledge(06)、delivery(07 指标)、governance(08 总览) |
| `WorkItemReader.ListByAssigneeInPeriod` | delivery(02) | 按成员与周期列出其参与的工作项及状态时间点（报告自动填充、B1 核对） | planning(10) |
| `AgentDraftRequester.Request` | fleet(05) | 请求 Agent 自撰计划 / 复盘（经 `agent.plan_draft` / `agent.report_draft`）；不可用时返回 `ErrDrafterUnavailable`，由 10 用系统模板 | planning(10) |
| `ReleaseNotesDrafting.ReportUnavailable` | delivery(07) | 起草 Agent 不可用 / 超时时立即触发交付确认兜底 | fleet(05) |
| `WorkItemEscalator.Escalate`, `DispatchAssigner.Assign(role=REVIEWER, lease_ttl_seconds)`（REVIEWER 分派不改工作项状态） | delivery(02) | 发布 `work_item.escalated`（唯一发布者）；评审员分派 | fleet(05) |
| `SpawnGrantReader.Active`, `SpawnGrantUsage.Reserve/Release`（事务内原子占用名额；成本由 08 消费 `execution.cost_updated` 累计） | governance | 主管 Agent 创建子 Agent 时校验与占用编制 | fleet(05) |
| `DelegationReader`, `EscalationRuleReader`, `PolicyRegistry`, `SystemActions` | governance | 读有效委托与升级规则；模块贡献策略规则；系统动作身份 | delivery、fleet |
| `WorkItemTransitioner.PlanProposed`, `WorkItemTransitioner.InternalQaCompleted`（source_ref 幂等）, `WorkItemTransitioner.SetSystemBlock`（`TOOL_UNAVAILABLE` / `PROVIDER_UNAVAILABLE`） | delivery(02，v2.0) | 计划提交回调（T30）；内部验收阶段结论回调（T33 / T34）；模板系统阻塞原因设置与清除 | execution、templates |
| `InternalQAReader`（Latest / PassedStages） | execution(03，v2.0) | 读工作项最近内部验收结论、运行 ID、参与 Agent 与截图证据（02 判定穿透） | delivery、governance |
| `ModelFamilyOf` | fleet(05，v2.0) | Agent 当前模型家族（跨模型家族约束、计划审批策略） | governance、execution |
| `MachineDirectory.Tools` / `HasCapability` / `ToolAvailability` / `ModelFamily`；`ListEligibleMachines(capabilities?)` | fleet(04，v2.0) | 工具清单与能力标记（`browser_capture`、`media_probe`、`media_transcode`、`speech_transcribe`、`scene_detect`）；模型家族唯一解析点 | execution（VISUAL 验证器、验收截图、执行快照）、fleet(05)、templates |
| `TemplateResolver`（ForProject / WorkItemBlueprint）, `VerifierCatalog`（含 `ToolCalibration`，D116）, `RubricScorer`（Record / Compare）, `ProviderRegistry`, `EvalSetWriter.ProposeCase` | templates(11，v2.0) | 项目模板解析与工作项蓝图；验证器定义与校准；评分记录与分歧；生成 provider；评测集提案（幂等，穿透入集只由 02 调用） | delivery(02 / 07)、execution、fleet(05)、planning(10)、governance |
| `WorklogReader` | delivery(12 Part A，v2.0) | 读工时与汇总（按人 / Agent、项目、迭代、类别，CR-12-06） | planning(10) |
| `WorkflowResolver` | delivery(12 Part A，v2.0) | 解析工作项的生效显示状态（含按 `defect_stage` 区分 `merged` / `deployed`）与流转矩阵、流转条件（CR-12-06） | delivery(02) |
| `TestCoverageReader` | delivery(12 Part A，v2.0) | 读验收标准的用例覆盖与最新执行结果（CR-12-06） | execution(03)、delivery(07) |
| `TunnelBroker`（Open / ResetHolder / Ports）；`MachineDirectory.Type` / `RemoteAccess`；`ReserveSlot(holder_kind=PREVIEW)` | fleet(04，D119) | 在机器出站 WebSocket 通道上打开预览 / 文件 / 终端流（端口归属、路径限定、开关与配额在 04 判定）；读机器类型与远程访问开关 | execution |
| `PreviewAccessResolver`（Authorize / ExchangeCode / ExchangeShare / Touch / HolderActive） | execution(03，D119) | 预览网关逐请求访问判定与访问换取 | fleet(04 预览网关) |
| `OperationRegistry`（Register / Lookup）, `AgentReminderScheduler`（UpsertForLoopOn / PauseForLoopOn / ResumeForLoopOn / CancelForLoopOn）, `AgentInboxHeads`, `ToolsetResolver`（ResolveOn / IssueTokens）（D120） | fleet(05) | 各模块启动期登记 Agent 可调用操作（操作清单）；10 循环契约驱动提醒；04 每批携带本机 Agent 收件箱头；03 派发时解析团队工具集并签发工具令牌 | delivery(02 / 07 / 12)、execution、knowledge、planning、fleet(04) |
| `WorkItemActivityRecorder.RecordOn`, `WorkItemActivityReader`（HeadOn / Since）（D120） | delivery(02) | 工作项活动流（`activity_seq`）：在调用方事务内追加活动、加锁读取当前序号、读取其后活动（过期上下文写入判定与收件箱投影） | execution、fleet(05)、delivery(12) |
| `ArtifactComments`（GetOn / MarkConvertedOn / CreateClientOn / ReplyClientOn）（D120） | execution(03) | 读批注线程；在调用方事务内记录批注转为结构化反馈 / 缺陷 / 甲方拒收项；创建甲方批注线程 | delivery(02 / 07 / 12) |
| `DeliveryEvidenceReader.ClientFacing` / `ListClientFacing`, `AcceptanceRecorder.CreateClientComment` / `ReplyClientComment`（D120） | delivery(07) | 判定证据是否为面向甲方的交付产物、列出交付的面向甲方文件；门户中甲方创建与回复批注（经 03 `CreateClientOn` / `ReplyClientOn`） | execution、intake |
| `LoopRunRecorder.OpenOn`（D120） | planning(10) | 提醒触发时在 05 事务内开启循环运行（或返回 `skip`） | fleet(05) |
| `EntityProvisioner`（事务内建实体与角色绑定）、`DeviceGrants`（设备码授权扩展）、`SecretSealer` | 底座 core / oauth / 09 | 见 §13 | delivery、fleet |

| `Notifier` | 09 平台 | 发送通知 | 全部 |
| `JobQueue`, `OutboxPublisher`, `RealtimeBroadcaster` | 09 平台 | 任务、事件、实时 | 全部 |

方法签名以**提供者规格**为准；使用者规格中的调用描述必须与之一致（审校时逐一核对）。

---

## 9. 接口约定

- OpenAPI tag：**最终发布路径（含 `/tenants/{tenant_id}` 等挂载前缀）的第一段**，由底座 `OnAddOperation` 自动设置（底座 2026-09-26 规则）。规格中的 tag 列须按此填写。
- 框架：huma v2；响应信封 `respx`（`{code,message,meta,data}`）；错误 `respx.Err(ctx, code, i18nKey)`。
- 路由：遵循 `apps/backend-api/docs/http-resource-routing-design.md` 与 `api-optimization-implementation-2026-09-25.md`：复数 kebab-case 名词；**禁止** `/approve`、`/retry` 等动作段，用名词子资源（如 `POST /work-items/{work_item_id}/decisions`）；状态类子资源用名词（如 `/suspension`、`/draining`、`/termination`）；全局短地址 + 直接所有者下的作用域地址。
- 领域事件消费去重：统一使用 09 `domain_event_receipts`（订阅模式 Transactional / AtLeastOnce）；模块**不得**自建 consumed / receipt 表（外部入站去重例外见 §3「基础设施表」，CR-12-08）。业务唯一键（如 `origin_acceptance_id`、成本游标）不属于事件去重，保留在模块内。
- 权限拒绝原因码 `AuthorityReasonCode` 由 08 拥有（08 §3.5 表为唯一清单；模块专用码也须在该表登记）。职责分离两码：`SELF_REVIEW`（裁决 / 验证 / 审批**自己产出**的工作或记忆）与 `SEPARATION_OF_DUTIES`（其他职责分离，如主管 Agent 裁决其派出的工作；子原因放 `detail`）。
- 守卫：每个操作必须以 `authz.Register*` 声明（项目内资源用 `RegisterEntity`，团队级用 `Register`，甲方门户用 `RegisterPublic` + 访问码校验，机器通道用服务账号认证）。
- 列表：`pkg/query`（`field=op:value`，offset 分页，`capabilities`）。
- 幂等：所有创建/命令型 POST 支持请求头 `Idempotency-Key`（≤64 字符），由**平台统一幂等辅助**（09 C7，单表 `platform_idempotency_keys`：tenant_id、subject_kind、subject_id、key、operation、request_hash、status_code、response_body、created_at，保留 24h）处理，模块不再各建幂等表；同 key 同请求返回首次结果；同 key 不同请求返回 `176425`；并发进行中返回 `176409`。机器通道（04）不用此头，改用 `dispatch_epoch` + `seq` 去重。
- 乐观锁：见 §3；冲突返回 `<段>0409`。
- 业务错误码段：intake `110000–119999`；delivery `120000–125999`（02）、`126000–126999`（12 Part A）与 `127000–129999`（07）（CR-12-01）；execution `130000–139999`；fleet `140000–141999`（04 机器）与 `142000–143999`（05 Agent/派单），`144000–144999` 预留；collab `145000–149999`；knowledge `150000–159999`；governance `160000–169999`；platform `170000–179999`；planning `180000–184999`；templates `185000–189999`（11，v2.0）；integration `190000–194999`（12 Part B，v2.0）。权限拒绝统一用本段 `xx0403`，具体原因放 `data.reason_code`。每个规格列出本模块使用的码与 i18n key。
- i18n：每个模块 `i18n/locales/{en-US,zh-CN,ms-MY}.json`，三语齐全。
- 实时：SSE 主题文法——单项 `tenant/{tenant_id}/{subject_kind}/{id}`（`subject_kind` 为 snake_case 单数，与事件信封 `subject.kind` 一致，如 `work_item`、`intake`）；集合 `tenant/{tenant_id}/{collection}`（复数，如 `intakes`、`machines`、`agents`、`projects`）；项目扇出 `tenant/{tenant_id}/project/{project_id}`；个人收件箱 `tenant/{tenant_id}/inbox/{principal_id}`。不支持通配符；单连接 ≤50 个主题。每个模块在其规格 §7 列出主题并注册 `TopicAuthorizer`（所需权限码）。服务端内部订阅（如唤醒跨实例的机器长轮询）走同一机制（09 定义）。
- **过期上下文写入（D120，CCR-05-11）**：Agent 对会话（`channel:<id>` / `thread:<root_message_id>`）、工作项（`work_item:<id>`，02 `activity_seq`）与批注线程（`comment_thread:<id>`）的写入必须携带请求字段 `seen_seq`（读到的该流最新序号；人类可选），清单与豁免以 05 BR-05-127 为准。写入事务在取得流的行锁后、其他业务守卫之前判定：其间存在主体 ≠ 调用者的更新事件 → **不写任何数据**，返回 409 + 本模块 `write_interrupted` 码，`data = {state:"interrupted", stream, seen_seq, current_seq, newer_events:[…] ≤50, truncated}`；缺 `seen_seq` → 422 + 本模块 `seen_seq_required` 码（collab 145021 / 145022、02 120469 / 120470、03 130474 / 130475、12 126061 / 126062）。`interrupted` 响应不记入 09 C7 幂等表，`seen_seq` 不参与请求哈希（09 BR-09-84）：调用方读完更新事件后以**同一 `Idempotency-Key`**、新 `seen_seq` 重试。`interrupted` 不替代既有唯一约束与 CAS。
- **Agent 操作清单与 `next`（D120）**：Agent 可调用的操作由各模块经 05 `OperationRegistry` 在启动期登记（名称 `<资源>.<动词>`、输入 / 输出 Schema、副作用、幂等规则、`seen_seq` 流、权限码与来源 `ROLE` / `DELEGATION` / `POLICY` / `RELATION` / `SELF`、可否委托、仅人类、是否需人确认、配额），登记的权限码必须是 §6.1 中的码且与该接口守卫一致（08 BR-08-67）。清单内操作的成功与失败响应在 `meta.next` 返回建议的下一步（≤5 项，只是建议，不构成授权）；Agent 被拒时仍返回 `<段>403` + `data.reason_code`，另带 `data.result`（`CAPABILITY_NOT_AUTHORIZED` 等，映射由 08 §3.5c 唯一定义）与 `data.operation`（05 BR-05-130…133）。

---

## 10. 前端约定

- 应用：`apps/frontend-web-admin/apps/delivery`（Vite + React，依赖 `@workspace/ui`），复用 IAM 应用的认证、外壳、工作区切换；甲方门户为同应用内独立布局，入口 `/portal#t=<token>`（令牌放在 fragment，不进日志与 Referer；兼容旧 `/portal/:token` 跳转）。
- 路由前缀：`/t/:tenantID/...`；项目内 `/t/:tenantID/p/:projectID/...`。
- 预览域（D119）：远程预览只在部署配置给出的**独立可注册域名**上提供（每会话一个随机子域；无通配 DNS 时为路径模式），从不在控制台源上渲染执行工作区内容；控制台只以 iframe 嵌入（04 BR-04-94、03 BR-03-112）。
- 共享组件（在 `packages/ui` 新增或在 delivery 应用 `components/` 内，规格中标明）：`CompletionLadder`（L1–L4）、`ActorAvatar`（人圆形 / Agent 圆角方形 + 芯片角标 / 机器图标 / 甲方）、`StateBadge`（图标 + 文字 + 语义色）、`EvidenceCard`（来源卡）、`DecisionPanel`、`StructuredFeedbackForm`、`ExecutionBranchTree`、`CountdownRing`、`AuthorityNote`（"你以 X 身份 · 来源 Y"）。
- 状态语义色 Token：见 PRD §14.2，只引用语义 Token。
- 每页必须定义：加载骨架、空态（含下一步按钮）、错误态（可重试）、无权限态（说明缺哪个权限码）、离线/重连态。
- 文案：称呼用户「你」；agent 声明完成写「已声明完成（未验证）」；不使用 Runner / Daemon / Computer 等技术词（技术详情面板除外）。

---

## 11. 验收标准格式

```
AC-02-07  [integration]  关联 US-02-03, BR-02-11
Given 工作项 W 处于 NEEDS_HUMAN_REVIEW，执行者为 Agent A，A 的角色为 agent_executor
When  A 以自身凭证调用 POST /work-items/{W}/decisions {action: APPROVE}
Then  返回 403，业务码 120403，W 状态不变，审计链追加 delivery.decision_denied（outcome=denied）
```

每份规格的验收标准必须覆盖：主流程、每个非法状态迁移、每个权限拒绝（含不可委托）、并发冲突、幂等重放、超时/离线、上限。

---

## 12. 引用资料（只读）

- 本仓：`docs/myrmidon/PRD.md`；`docs/0[1-4]_Myrmidon_*.docx`；`apps/backend-api/{README.md,docs/*,internal/todo,internal/files,internal/iam/*,pkg/*}`；`.agents/skills/backend-module/SKILL.md`；`apps/frontend-web-admin/{apps/iam,packages/ui}`。
- 旧项目（**只读，禁止修改**）：`D:\Projects\MyrmidonAI\{PRD.md,chaosplus.md,README.md,docs/*,internal/platform/*,src/*,apps/web/app/*}`。

---

## 13. 底座框架需要的改动（由各模块规格提出，作为平台工作项排期）

| # | 底座位置 | 改动 | 提出规格 |
|---|---|---|---|
| F1 | `iam/core` | `EntityProvisioner`：在调用方事务内创建实体与角色绑定；`project` 设为**保留实体类型**（通用 `/entities` 接口不得创建/删除/改名/改类型/移动项目实体） | 07 |
| F2 | `iam/oauth` | 设备码客户端属性 `device_subject`（签发给机器服务账号而非批准人）与 `device_code_ttl`；`/oauth/token` 与通用 `/admin/:tenantID/device` 页拒绝机器客户端；`DeviceGrants` 接口 | 04 |
| F3 | `iam/identity` | 服务账号创建、禁用、凭证轮换可在调用方事务内执行 | 04、05 |
| F4 | `iam/authn` | step-up 用途改为**模块登记**（`RegisterReauthenticationPurpose`）；**完整用途清单以 08 §6.9 危险操作登记表为唯一来源**；`ReauthenticationDialog` 迁入 `packages/ui` | 01、02、03、04、05、07、08、09 |
| F4b | `iam/core`、`iam/governance` | 事务绑定的 `EntityRoleBindingStore`、`RoleSeeder`、`TenantMemberLister`；访问申请按角色设最长授权时长 | 05、08 |
| F5 | `iam/webhook` | 订阅按目标/开口过滤（否则 API 回执会暴露其他来件的 ID 与时间） | 01 |
| F6 | 共享安全 | 新增 `portal` 限流作用域 | 01 |
| F7 | `files` | 服务端（系统身份）向租户/项目根写入业务文件的能力 | 01、03、06 |
| F8 | 构建 | 所有 build/test 目标加 `-tags sqlite_fts5`；新增 `make test-pgvector` | 06 |
| F9 | `pkg/extension/gorm` | 三个白名单辅助（读取 MySQL ngram 设置、pgvector 版本、HNSW `SET LOCAL`），保持查询门禁预算不变 | 06 |
| F10 | 平台能力 | 任务队列、outbox、SSE、对象存储、通知、菜单贡献（`iam_menus.app` 列 + 贡献台账）、幂等辅助、`SecretSealer`、租户业务文件写入；新权限 `system_job_*` / `system_event_*`（系统级） | 09 |
| F11 | `pkg/extension/authz`、`gormx` | `RegisterTenantMemberStream`（SSE 守卫）；`gormx.Notify/Listen`；SQLite 默认 `_busy_timeout=5000` | 09 |
| F12 | 依赖 | `robfig/cron/v3`（仅解析器，待批准；未批准时 MVP 仅支持间隔调度） | 09 |

---

## 14. 契约变更记录

| 版本 | 日期 | 变更 | 来源 |
|---|---|---|---|
| 1.0 | 2026-10-11 | 初版 | 主规格 |
| 1.1 | 2026-10-11 | 时间列类型；ETag/If-Match（428/409）；模块内枚举规则；`MachineState` 去掉 `PENDING_CONFIRM`；新权限 `intake_submit`/`work_item_cancel`/`decomposition_template_manage`/`verifier_manage`/`knowledge_manage`；不可委托清单扩充 + 新增 §6.5 仅限人类动作；新事件（intake.*、project.*、work_item.escalated、interaction.closed、execution.merged、machine.runtimes_changed、memory.*、skill_version.approved）；`budget.threshold_reached` 发布者改为 07；`decision.made` 不再触发返工分支；幂等表列；OpenAPI tag=路径首段；错误码段细分与 `xx0403 + reason_code`；SSE 集合主题；门户入口 fragment；跨模块接口扩充；§13 底座改动清单 | CR-01-01..10、CCR-02-01..09、CR-03-01..08、CR-04-01..11、CR-06-01..11、CCR-07-01..09 |
| 1.2 | 2026-10-11 | fleet 错误码段拆分；平台统一幂等表；事件信封扩充 + 补充事件；`DispatchAssigner` 改为同步；新接口（DispatchProvenance、ChannelPoster、SkillMounter、MemoryPolicyValidator、DelegationRevoker、PreferenceResolver、RecipientDirectory）；基础设施表例外；F11/F12 | CCR-05-01..10、CR-09-01..11 |
| 1.3 | 2026-10-11 | `production_access`；角色码表以 08 §3.3 为准、载体角色；`delivery_lead` 去掉模式切换与授予委托；`agent_lead` 改经策略规则；ROLE 键格式；治理事件；审计 detail 必含字段；AuthorityChecker 签名与顺序；F4/F4b | CR-08-01..12 |
| 1.4 | 2026-10-11 | 一致性审校（三路审计 ~120 项，决议 R1–R20）：tag 含挂载前缀；状态子资源名词；事件消费去重统一 09；`AuthorityReasonCode` 归 08、`SEPARATION_OF_DUTIES`；SSE 主题文法；`ClaimSubmitter` 归 02；删 `BudgetGuard`/`SlotReserver`/`AgentLifecycle`；新接口 ExecutionReader/IntakeProgressReader/AcceptanceReader；新事件（project.hold_changed/stage_ready、delivery.ready/release_notes_requested/withdrawn、ensemble.plan_created/cancelled、dispatch.rejected 语义）；delivery 不消费 dispatch.applied；系统级运维权限码；F4 以 08 §6.9 为准 | 审计 A/B/C |
| 1.5 | 2026-10-11 | 第二轮复审（6 BLOCKER / 21 MAJOR）：`POLICY:<rule_id>` + `detail.dispatch_proposal_id`；登记 `project.members_changed`；`delivery.release_notes_requested` 增 fleet 消费；`AuthorityReasonCode` 规范清单见 08 §3.5（模块改名 NOT_DELEGABLE→NON_DELEGABLE、PERMISSION_DENIED→MISSING_PERMISSION、STEP_UP_REQUIRED→REAUTH_REQUIRED） | 复审 #1–#27 |
| 1.6 | 2026-10-11 | 第三轮复核（4 BLOCKER）：`SubmitInput.from_exit`；`delivery.release_notes_requested.drafter_agent_id` 与 `ReportUnavailable` 原因值对齐；`WorkItemFacts.AssignmentRole/AssigneePrincipalID`，SYSTEM 主体跳过 S3/S4/S6 由策略判定，评审派单记 `POLICY:review_dispatch`；派单提案过期按触发类型判定 | 复核 B1–B4 |
| 1.7 | 2026-10-11 | PRD §18 Q1–Q11 与各规格 §12 待决项全部按建议采纳（产品负责人「该有的就有」，见 decisions D109） | 产品负责人 |
| 1.8 | 2026-10-11 | D110 主管 Agent 自建 Agent：`AgentOrigin`、`SpawnGrantState`；权限 `agent_spawn`（仅经策略）、`spawn_grant_manage`（不可委托、仅所有者）；仅限人类动作增加批准创建请求与收编；事件 `agent.spawn_requested/spawned/orphaned/adopted`、`spawn_grant.*`；接口 `SpawnGrantReader`/`SpawnGrantUsage` | 产品负责人 |
| 1.9 | 2026-10-11 | D111 目标、计划与周期报告：新模块 `planning`（规格 10，错误码段 180000–184999）；枚举 GoalLevel/GoalStatus/PeriodKind/ReportLevel/ReportState/KRSource/CheckSeverity；权限 goal_*、plan_*、report_*、planning_settings_manage（确认类仅限人类）；事件 goal.*、plan.*、report.*；合并 CR-10-01..10：表前缀 `planning_*`、接口 `GoalReader`/`ReportReader`/`WorkItemReader.ListByAssigneeInPeriod`/`AgentDraftRequester`、默认授予、05 `owner_principal_id` 与 `agent.plan_draft`/`agent.report_draft`、08 收件箱 PLAN_CONFIRMATION/REPORT_CONFIRMATION/REPORT_BLOCKED/REPORT_OVERDUE 与「目标」菜单分组 | 产品负责人 |
| 2.0 | 2026-10-11 | D112（研究方案 A–G 采纳：可靠性 P0、云效对齐、领域模板、纯人工模式、集成排期）与 D113（内部质量门、不穿透）：新状态 DRAFT / PLAN_REVIEW / INTERNAL_QA / STOPPED / EXCEPTION；新角色 TESTER / ACCEPTOR / ASSISTANT；TeamMode HUMAN_ONLY；VerificationStage、StateGroup、ExceptionDecision、EscalationReason、TemplateDomain、StatusConfidence；§15；规格 11、12。**合并（2026-10-12）**：CR-11-01/04/05/06/07/08/09——templates 错误码段 185000–189999、templates 事件、接口 TemplateResolver / VerifierCatalog / RubricScorer / ProviderRegistry / EvalSetWriter（替代 02 原写法 `TemplateEvalSink`）、一方模板 `tenant_id=0` 基础设施表例外、02 `template_stage_key` 与模块内 `BlockReason` 新值 TOOL_UNAVAILABLE / PROVIDER_UNAVAILABLE、04 工具检测与模型家族；D112/D113 接口 `WorkItemTransitioner.PlanProposed/InternalQaCompleted/SetSystemBlock`、`InternalQAReader`、`ModelFamilyOf`、`MachineDirectory.Tools/HasCapability/ToolAvailability/ModelFamily` 与事件 plan.* / andon.* / exception.* / internal_qa.* / acceptance_suite.frozen / cheating.suspected / quality.escaped 登记入 §7.1 / §8；穿透学习唯一执行方为 02。**合并（2026-10-12）**：CR-12-01..09——delivery 错误码段细分（02 120000–125999、12 Part A 126000–126999、07 127000–129999）与 integration 段 190000–194999；模块 `integration`（`integration_*`、`goose_integration`）；12 权限码与仅人类动作；12 事件；接口 WorklogReader / WorkflowResolver / TestCoverageReader；收件箱 `REVIEW_SHEET` / `WORKLOG_APPROVAL`、step-up `integration_credentials`、`EXTERNAL:<provider>:<ref>`；外部入站去重例外；`AGENT_CANNOT_ACCEPT`。D114：`quality.escaped` 发布者为 02 / 07 / 12 Part A，统一 `escape_key`，学习只经 02 `delivery.escape_learning` | 产品负责人；CR-11-01..09；CR-12-01..09；D114 补充（D116，2026-10-12）：工具校准——11 `VerifierCatalog.ToolCalibration`、03 BR-03-103 只在已校准机器上判定视觉 / 交互验证器。补充（D117，2026-10-12）：甲方变更申请——`IntakeStatus` 增 `CLIENT_APPROVAL`；事件 `intake.client_approval_requested` / `intake.client_approval_decided`；接口 02 `ChangeImpactPreviewer`、07 `ToleranceReader.WouldBreach` / `ToleranceAdjuster.ApplyApprovedChange`；§6.5 增请甲方确认与登记线下确认（甲方决定为 `CLIENT:` 行为）（01 CR-12、07 CR-07-12）。补充（D118，2026-10-12）：控制台信息架构——顶栏域标签（工作台 · 会话 · 协作 · 项目 · 团队 · 治理）+ 当前域侧栏（08 §8.6）、09 C6 `MenuPage.Domain` 与底座 `iam_menus.domain` 列（09 CR-09-12）、05 会话页 `/t/:tenantID/chat` 与成员私信 `ChannelKind.DIRECT`；不新增共享枚举、权限码、事件或跨模块接口（`MenuDomain`、`ChannelKind` 为模块内枚举，留在 09 / 05）。补充（D118 修订，2026-10-12）：项目为主容器——删除协作域，域标签为 工作台 · 会话 · 项目 · 团队 · 治理；项目域分项目外（项目组合 · 来件 · 交付总览 / 跨项目 需求 · 任务 · 缺陷 · 流水线）与项目空间（路由 `/t/:tenantID/p/:projectID/...`，详情页在所属项目空间渲染），团队域增目标树 · 报告中心，移动端底栏 工作台 · 会话 · 项目 · 裁决（08 §8.6）；09 `MenuDomain` 去掉 `COLLAB`，`MenuPage` 增 `Space`（TEAM / PROJECT）与 `iam_menus.space` 列（09 BR-09-80、CR-09-12）；同样不新增共享枚举、权限码、事件或接口。补充（D119，2026-10-12）：远程预览、终端与文件经机器隧道；服务器与集群执行机器——共享枚举 `MachineType`（04）；权限码 `terminal_interact`、`workspace_file_edit`、`preview_share_approve`（03）与 `machine_access_manage`（04），§6.4 / §6.5 同步；事件 `machine.access_changed`（04）与 `share_link.requested` / `decided` / `ended`（03）；接口 `TunnelBroker`、`MachineDirectory.Type` / `RemoteAccess`（04）与 `PreviewAccessResolver`（03）；错误码在既有段内使用（03：130460–130473；04：140482–140492）；08 登记 step-up `terminal_interactive` / `machine_terminal_enable` / `machine_credential_issue`、收件箱 `PREVIEW_SHARE_APPROVAL`、原因码 `MACHINE_ACCESS_DISABLED` / `PORT_NOT_OWNED` / `PATH_OUTSIDE_WORKSPACE` 与审计事件；§10 预览域约定（03 CR-03-09、04 CR-04-12）。分期按 D119：服务 / API 预览、文档图片报告预览、只读终端、只读文件、SERVER 为 MVP（WebSocket 通道随之提前到 MVP）；交互终端、文件编辑、视频与小说预览、K8S_POOL 为 v1.1；甲方分享链接 D119 未列分期，待定。补充（D120，2026-10-12）：Agent 运行时协议、操作清单、锚定批注、Agent 生命周期、循环契约与团队工具目录——共享枚举 `AnchorKind`、`CommentVisibility`（03）；权限码 `agent_workspace_read`、`tool_catalog_manage`（05，均仅限人类，后者不可委托）与 `artifact_comment_write`、`interaction_request`（03），§6.4 / §6.5 同步；事件 `comment.*`（03）、`loop_contract.state_changed` / `loop_run.completed` / `loop_run.escalated`（10）、`tool_catalog.changed`（05）；接口 `OperationRegistry` / `AgentReminderScheduler` / `AgentInboxHeads` / `ToolsetResolver`（05）、`WorkItemActivityRecorder` / `WorkItemActivityReader`（02）、`ArtifactComments`（03）、`DeliveryEvidenceReader` / `AcceptanceRecorder.CreateClientComment`（07）、`LoopRunRecorder`（10）；§9 过期上下文写入（`seen_seq`、`interrupted`、幂等例外）与操作清单 `meta.next` 约定；§3 基础设施表例外（系统级清单与工具预设、收件箱按保留期清理）；错误码在既有段内使用（02：120469–120471；12：126061–126063；07：127056 / 127057；03：130474–130481；05：142044–142058；collab：145021 / 145022；10：180510–180514）；08 登记原因码 `TOOL_NOT_GRANTED`、§3.5c 结果映射、step-up `agent_full_reset` / `tool_credential_manage` 与菜单项 `tool-catalog` / `agent-operations`（05 CCR-05-11…13 / 15、02 CCR-11、03 CR-03-10 / 11、04 CR-04-13 / 14、07 CR-07-13、08 CR-08-14、10 CR-10-11、12 CR-12-10、06 CR-06-13、09 CR-09-13）。分期按 D119 查看器：视频时间点与小说章节批注为 v1.1；唤醒 webhook 为 v1.1；需要凭据的本地（STDIO）MCP 为 v1.1，其余 MVP。 |
---

## 15. 可靠性闭环与内部质量门（D112 / D113，v2.0）

> 来源：`research/2026-10-collaboration-research.md` 的 P0 方案（产品负责人 2026-10-11 确认 A–G 全部采纳）与 D113「内部能处理的不穿透到人，更不穿透到甲方」。本节是各规格的共同定义，规格细节不得与之冲突。

### 15.1 完成阶梯（替换 PRD §4.2 的 L2 定义）

| 层级 | 名称 | 由谁产生 |
|---|---|---|
| L1 | 已声明完成（未验证） | 执行者 |
| L2 | **内部验收通过** = 平台重跑的确定性检查（`AUTOMATED`）+ 测试 Agent（`AGENT_TEST`）+ 产品验收 Agent（`AGENT_ACCEPTANCE`）全部通过 | 平台 + 内部 Agent |
| L3 | 团队已接受 | 有裁决权的人（或经委托的评审 Agent，仍须有人类负责人） |
| L4 | 甲方已验收 | 需求开口回执 |

### 15.2 内部质量门与不穿透路由（D113）

- 顺序：`VERIFYING`（AUTOMATED）→ `INTERNAL_QA`（AGENT_TEST → AGENT_ACCEPTANCE）→ `NEEDS_HUMAN_REVIEW`。任一阶段不通过 → `REWORK`（内部返工，结构化反馈注入下一次执行），**不进入任何人的收件箱，不通知人**。
- 内部返工上限 `max_internal_rounds`（模板参数，默认 5；质量返工计数沿用 02，二者取先到者）。
- **只有以下情况升级到人**（`EscalationReason`）：内部轮次用尽；发现需求冲突 / 歧义（先给交付负责人，是否向甲方澄清由人决定）；Andon 停线；容差例外；预算阈值；模板规定的必经人工关口。
- 甲方只看见由 L3 已接受工作项组成的交付包；内部返工、内部验收记录不出现在甲方门户。
- **测试 Agent**：补充 / 扩展测试（不得修改冻结的验收测试），做探索式测试与视觉 / 交互检查（截图、可访问性），输出缺陷与证据。
- **产品验收 Agent**：以需求、验收标准、模板评分标准为准，检查实际交付形态（运行、截图、成品文件），给出逐条验收结论与证据；必须与执行者**不同模型家族**（不可用时与执行者不同的模型 + 不同主体），且与测试 Agent 不是同一主体。
- **穿透**：人（L3）或甲方（L4）发现了内部质量门已放行的问题 → 记录穿透事件（`quality.escaped`；发布者 02 / 07 / 12 Part A，统一幂等键 `escape_key = (work_item_id, claim_id 或 acceptance_id)`，见 §7.1），生成学习条目：**只**由 02 `delivery.escape_learning` 写入测试 / 验收 Agent 的经验记忆草稿（06）并提交模板评测集用例（11），08 负责人工确认 / 驳回；穿透率是团队核心质量指标。
- **验收权**：Agent（含持 `TESTER` 角色者）不得执行映射到 `ACCEPTED` 的流转（`AGENT_CANNOT_ACCEPT`，CR-12-09，见 §6.5）。

### 15.3 就绪闸门（DoR）

派发给 Agent 前必须满足（缺项则 `DRAFT`）：`objective`、`acceptance_criteria[]`（每条有 ID，推荐 EARS 写法「当……时，系统应……」）、`out_of_scope[]`、`context_refs[]`、`verification_plan`（每条验收标准 → `TEST` / `COMMAND` / `AGENT_JUDGE` / `HUMAN` / `VISUAL`）、`budget{tokens, usd, wall_clock, max_internal_rounds}`、`autonomy_level`、`accountable_principal_id`。对人类执行者只做提示不拦截。需求质量检查（歧义、冲突、不可验证）在转为工作项时运行，发现冲突 → `REQUIREMENT_CONFLICT` 升级给交付负责人。

### 15.4 其他 P0 机制

| 机制 | 定义 |
|---|---|
| 人类负责人 | 每个工作项必须有 `accountable_principal_id`（人类）；Agent 只是执行者 / 被委托者，不对结果负责；全自治下为委托授予人 |
| 计划审批 | 中高风险或自治级别 ≥ MEDIUM：执行前进入 `PLAN_REVIEW`，只读产出计划（文件、每条验收标准的覆盖方式、风险）；批准者为人或不同模型家族的评审者；主管 Agent 不得批准自己派出的计划 |
| 冻结验收测试 | 计划批准时冻结 `acceptance_suite`（可见部分 + 隐藏部分 `HIDDEN`）；执行者对测试只读；验证时检测测试 diff，改动冻结测试 → 自动拒绝并记作弊嫌疑 |
| 平台重跑 | 确定性检查一律由平台在隔离环境重跑，不采信执行者贴出的日志 |
| 诚实证据包 | 证据包必填 `unverified[]`（可为空）；「缺验证不算完成」 |
| Andon 停线 | 任何成员（含 Agent）可停线：工作项 / 项目 → `STOPPED`，下游依赖暂停，通知负责人；负责人确认处置后恢复 |
| 容差与例外 | 需求 / 工作项带 `appetite` 与 `tolerance{time, cost, scope}`；按**预测**越界 → `EXCEPTION`，提交例外报告（现状 / 选项 / 建议），授权人决定 `ExceptionDecision`；禁止静默延期 |
| 报告防西瓜 | 报告含系统计算进度与人声明状态 `StatusConfidence`；声明 GREEN 而计算进度落后阈值（默认 30%）→ 阻止提交并要求说明 |
| 项目宪法 | 每个项目一份宪法（构建 / 测试命令、约束、禁区、风格），作为上下文第 5 层每次必注入；变更需评审 |

### 15.5 新增权限码、事件与指标

- 权限码：`andon_pull`（全员，含 Agent）、`andon_clear`（负责人 / 交付负责人，仅人类）、`exception_decide`（仅人类，不可委托）、`plan_approve`（人或不同模型家族的评审 Agent，经策略）、`acceptance_suite_manage`（冻结与隐藏验收集，仅人类）、`template_manage`（模板库）、`integration_manage`（云效集成）。
- 事件：`work_item.ready_checked`, `plan.proposed`, `plan.approved`, `plan.rejected`, `acceptance_suite.frozen`, `internal_qa.passed`, `internal_qa.failed`, `andon.pulled`, `andon.cleared`, `exception.raised`, `exception.decided`, `quality.escaped`, `cheating.suspected`。
- 指标：**穿透率**（L3 / L4 阶段发现的、内部已放行问题数 ÷ 进入 L3 的工作项数）、内部一次通过率、平均内部轮次、人工关注分钟（只统计 L3 及升级）。

### 15.6 规格归属

02：DoR、计划审批状态、负责人、Andon、容差例外、内部质量门路由与升级；03：平台重跑、冻结与隐藏验收、测试 diff、`unverified[]`、内部验收执行（TESTER / ACCEPTOR 执行种类、视觉验证器）；05：TESTER / ACCEPTOR / ASSISTANT 角色与内部质量门派单（跨模型家族约束）；07：`HUMAN_ONLY` 模式、交付只含 L3；08：新权限码、升级事件路由、穿透指标与学习的人工确认（学习执行归 02 `delivery.escape_learning`）；10：声明状态与防西瓜；11：领域模板；12：云效对齐与集成。
