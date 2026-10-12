# 05 · Agent 成员、主导 Agent 编排与频道

> 后端模块：`fleet`（Agent 部分，与 04 同模块）+ `collab`（频道）。遵守 `00-conventions.md`；与 00 不一致或需扩展处集中在 §13。
> 标注「**本规格提议**」的是 PRD 未给出、由本规格补齐的具体值或机制，均附理由。

---

## 1. 范围与目标

**解决**：

| 范围 | 内容 |
|---|---|
| Agent 档案 | 人设、头像（像素人种子）、系统提示词、机器绑定、runtime × 模型 × 推理强度、授权模式、MCP 白名单、AgentRole 集合、默认频道、记忆策略引用、状态 |
| Agent 生命周期 | 创建（服务账号主体 + 角色绑定同事务）、启动 / 停止 / 重启、CRASHED 检测与自动重启上限、换绑机器、正常退役向导、强制退役 |
| Agent 绩效 | 首次验收率、单位已接受成本、返工率、当前负载（公式、刷新节奏） |
| 主导 Agent 编排 | 派单候选预过滤 + 确定性评分、主导 Agent LLM 提案、提案生命周期、MANUAL / AUTO 护栏、人工改派、无候选、失效重派、饥饿与抖动上限 |
| Heavy 并行求解（v1.1） | 触发规则、N 枝选择（模型多样性）、预算上限、对比报告、择优 / 合成、落选枝归档 |
| 频道（collab） | 项目默认频道、自建频道、成员、消息四类、@mention、Agent 回复与「@Agent 请做 X」、事件折叠、未读、编辑 / 撤回、Agent 私信、钩子（v1.1）、桥接（v2）、保留、上限、实时 |

**不解决（边界）**：

| 内容 | 归属 |
|---|---|
| 机器接入、心跳、通道传输、运行时 / 模型目录、执行槽 | 04（本规格经 CCR 在其通道上增加 Agent 进程消息，§6.6） |
| 工作项、分派与租约、Decision（含 ASSIGN 的落库）、职责分离判定 BR-02-21 | 02（派单经 02 接口生效，**本模块不直接写分派**） |
| 执行、ENSEMBLE 分支的创建与对比数据（03 A07/A08）、证据、验证、交接文档 Artifact 存储 | 03 |
| 技能库与挂载（`PUT /agents/{agent_id}/skills/{skill_id}`）、记忆、检索 | 06（`SkillCatalog`、`MemoryWriter`、`Retriever`） |
| 项目档案中的 `lead_agent_id`、`dispatch_policy`、`ensemble_max_branches`；项目成员角色绑定；项目创建 | 07（本模块经 07 #4 编辑，不存副本；07 在建项目事务内调用本模块 `ChannelProvisioner.CreateDefaultOn`） |
| 委托、AuthorityChecker、升级规则、通知渠道、我的裁决 | 08 / 09 |
| A2A / MCP 协议：MCP 只用于 Agent ↔ 工具（白名单在本规格），A2A 不进 MVP；派单、验收语义永远由本系统领域模型定义 | PRD Q9、评审 P1 |

---

## 2. 用户故事

| 编号 | 故事 | 期 |
|---|---|---|
| US-05-01 | 作为团队所有者，我要在一张表里同时看到人与 Agent 的角色、负载、当前工作项、状态，以便判断团队容量 | MVP |
| US-05-02 | 作为团队所有者，我要新建 Agent（人设、头像、提示词、机器、runtime×模型×强度、技能、MCP、角色、默认频道、记忆策略），创建失败不留残缺身份 | MVP |
| US-05-03 | 作为团队所有者，我要启动 / 停止 / 重启 Agent，并在崩溃时自动重启但不陷入无限循环 | MVP |
| US-05-04 | 作为团队所有者，我要把 Agent 换绑到另一台机器 | MVP |
| US-05-05 | 作为团队所有者，我要通过向导正常退役 Agent：生成交接文档、选择接手人、转交在途工作、清理机器、吊销凭证 | MVP |
| US-05-06 | 作为团队所有者，机器失联时我要强制退役 Agent，并清楚知道会丢失什么 | MVP |
| US-05-07 | 作为交付负责人，我要看到每个 Agent 的首次验收率、单位已接受成本、返工率、当前负载 | MVP |
| US-05-08 | 作为交付负责人，我要为项目指定主导 Agent 与派单策略（MANUAL / AUTO），让它把工作项分给最合适的 Agent（可使用不同模型） | MVP |
| US-05-09 | 作为交付负责人，我要看到每条派单提案的候选评分与理由，一键确认或改派 | MVP |
| US-05-10 | 作为交付负责人，我要在没有合适 Agent、Agent 崩溃或机器离线时得到明确提示与重派 | MVP |
| US-05-11 | 作为交付负责人，我要对高风险 / 高不确定工作项启动 Heavy 并行求解，在预算内比较多枝结果后择优 | v1.1 |
| US-05-12 | 作为团队成员，我要在项目频道里与人和 Agent 协作，@成员，看到折叠的执行事件与不折叠的裁决卡片 | MVP |
| US-05-13 | 作为交付负责人，我要在频道里「@主导Agent 把 W 派给 Ada」，得到一张需要我确认的派单卡片 | MVP |
| US-05-14 | 作为团队所有者，我要只读查看 Agent 之间的私信 | MVP |
| US-05-15 | 作为团队所有者，我要为频道配置消息钩子（向外推送） | v1.1 |
| US-05-16 | 作为团队所有者，我要把频道桥接到外部 IM / MQTT / WebSocket / HTTP | v2 |

---

## 3. 领域模型与数据表

### 3.1 类型记号与三方言映射

| 记号 | PostgreSQL | MySQL 8.0.16+ | SQLite |
|---|---|---|---|
| ID | `BIGINT` | `BIGINT` | `INTEGER` |
| STR(n) | `VARCHAR(n)` | `VARCHAR(n)`（utf8mb4） | `TEXT` + `CHECK(length(col)<=n)` |
| ENUM | `VARCHAR(32)` + `CHECK(col IN (...))` | 同左（CHECK 生效） | `TEXT` + CHECK |
| TEXT64 | `TEXT` + 应用层 ≤64 KB | `MEDIUMTEXT` | `TEXT` |
| JSON | `JSONB` | `JSON` | `TEXT` + `CHECK(json_valid(col))` |
| BOOL | `BOOLEAN` | `TINYINT(1)` | `INTEGER CHECK(col IN (0,1))` |
| TS | `TIMESTAMPTZ(3)` | `DATETIME(3)`（禁 `TIMESTAMP`） | `DATETIME` |
| INT / BIGINT | `INTEGER` / `BIGINT` | `INT` / `BIGINT` | `INTEGER` |

时间列按 `backend-module` 门禁书写（与 00 §3 文字冲突，同 CR-03-01 / CCR-07-01，见 §13 CCR-05-01）。所有表含 `tenant_id ID N`、`created_at TS N`、`updated_at TS N`、`created_by ID Y`、`updated_by ID Y`（下文不再重复）。对象名带 `{{.Prefix}}`；goose 版本表：fleet 与 04 共用 `goose_fleet`，collab 为 `goose_collab`。**部分唯一索引**：PG / SQLite 用 `CREATE UNIQUE INDEX … WHERE …`；MySQL 无部分索引，统一改为生成列 `<name>_key … GENERATED ALWAYS AS (CASE WHEN <cond> THEN <cols> END) STORED` + 普通 UNIQUE（NULL 不冲突）。下文写作「部分唯一（条件）」即指此对。

### 3.2 `fleet_agents`（Agent 聚合根）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | 雪花 |
| principal_id | ID | N | | UNIQUE | 底座服务账号 Principal（00 §4 AGENT） |
| name | STR(64) | N | | 见 BR-05-01 | 显示名，同租户非 RETIRED 内大小写不敏感唯一 |
| handle | STR(40) | N | | `^[a-z][a-z0-9-]{1,39}$` | @ 时的稳定短名；同租户唯一（含已退役，不复用）；服务账号 `login_name = agent-<handle>` |
| persona | STR(2000) | Y | | | 人设 / 简介 |
| avatar_style | ENUM | N | `PIXEL` | ROBOT / PIXEL / MONSTER / GEOMETRIC | 复用底座头像风格集 |
| avatar_seed | BIGINT | N | | 0 ≤ seed < 2^53 | 像素人生成种子；同种子同图（BR-05-03） |
| system_prompt | TEXT64 | N | | 1 B–32 KB（**本规格提议**） | 角色宪法，注入 03 上下文第 1 层 |
| machine_id | ID | Y | | | 绑定机器（04）；仅 RETIRED 后保留历史值，DRAFT 可空 |
| runtime_kind | ENUM | N | | `RuntimeKind`（00 §5） | |
| model_id | STR(160) | N | | 1–160 | 04 模型目录 id（含手填） |
| reasoning_effort | STR(16) | Y | | runtime 支持值 | 如 `low/medium/high/xhigh`；按 04 能力校验 |
| authorization_mode | ENUM | N | `ON_REQUEST` | 03 `AuthorizationMode` | FULL_AUTO 需 step-up 并审计（00 §7.2 YOLO） |
| max_context_tokens | INT | N | 24000 | 4000–200000 | 03 BR-03-28 注入预算 |
| mcp_allowlist | JSON | N | `[]` | ≤32 项，每项 STR(128) | MCP server 名；须 ∈ 绑定机器能力检测结果（BR-05-05） |
| capability_tags | JSON | N | `[]` | ≤20 项，`^[a-z0-9][a-z0-9-]{0,31}$` | 手工能力标签（如 `frontend`）；与技能标签合并参与评分 |
| default_channel_names | JSON | N | `[]` | ≤10 项 | Agent 加入项目时额外自动加入的自建频道名（默认频道必加） |
| memory_policy | JSON | N | `{"profile":"STANDARD"}` | Schema 由 06 定义 | 本模块只存储并经 06 校验，不解释 |
| max_parallel_items | INT | N | 1 | 1–4 | 同时作为 EXECUTOR 的工作项上限（**本规格提议**，避免单 Agent 上下文混杂） |
| max_risk | ENUM | N | `MEDIUM` | `RiskLevel` | 派单硬过滤 E6 |
| state | ENUM | N | `DRAFT` | `AgentState` | §4.1 |
| process_status | ENUM | N | `NONE` | `AgentProcessStatus`：NONE / STARTING / RUNNING / STOPPING / EXITED / UNREACHABLE | 进程观测子状态（CCR-05-02） |
| process_status_at | TS | Y | | | |
| last_seen_at | TS | Y | | | 最近一次机器上报该 Agent 进程存活 |
| config_revision | INT | N | 1 | | 影响进程的字段（提示词、模型、MCP、技能挂载、授权模式）变更 +1 |
| running_config_revision | INT | Y | | | 进程启动时的修订；≠ config_revision → UI「需重启生效」 |
| auto_restart | BOOL | N | true | | |
| restart_window_started_at | TS | Y | | | 自动重启滑动窗起点 |
| restart_count_in_window | INT | N | 0 | 0–3 | |
| restart_exhausted | BOOL | N | false | | 达上限后置 true，人工重启清零 |
| credential_id | ID | Y | | | 当前服务账号凭证（底座） |
| credential_delivery | ENUM | N | `PENDING` | PENDING / DELIVERED / FAILED | 凭证经 04 通道送达机器的状态 |
| pending_cleanup_machine_id | ID | Y | | | 强制退役 / 换绑时旧机离线，待其重连后清理 |
| retirement_id | ID | Y | | | 当前或最终退役记录 |
| retired_at | TS | Y | | | |
| version | INT | N | 1 | | 乐观锁（ETag） |

索引：`(tenant_id, state)`；`(tenant_id, machine_id, state)`；部分唯一 `(tenant_id, lower(name)) WHERE state<>'RETIRED'`（MySQL 生成列 `active_name_key`）；UNIQUE `(tenant_id, handle)`；`(state, process_status, last_seen_at)`（看门狗）。外部引用：`principal_id`→identity，`machine_id`→04，`credential_id`→identity（只存 ID）。

`memory_policy` 存储 Schema（外层，内层由 06 校验）：`{"type":"object","required":["profile"],"properties":{"profile":{"type":"string","maxLength":32},"overrides":{"type":"object"}},"additionalProperties":false}`。

### 3.3 `fleet_agent_roles`

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| agent_id | ID | N | | PK(agent_id, role) | |
| role | ENUM | N | | `AgentRole` | |
| iam_role_key | STR(64) | N | | | 对应 00 §6.2 角色键：EXECUTOR→`agent_executor`、REVIEWER→`agent_reviewer`、COORDINATOR→`agent_coordinator`、LEAD→`agent_lead` |
| revoked_at | TS | Y | | | 退役时置位（不删行） |

AgentRole 只决定「这个 Agent 能被安排成什么」；**权限**来自 07 / 底座在项目实体上写的角色绑定（按此映射）。项目外不授予任何 `agent_*` 角色（BR-05-02）。

### 3.4 `fleet_agent_process_events`（崩溃 / 重启账本，只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| agent_id | ID | N | |
| machine_id | ID | N | |
| kind | ENUM | N | `AgentProcessEventKind`：START_REQUESTED / STARTED / STOP_REQUESTED / STOPPED / CRASHED / AUTO_RESTART / MANUAL_RESTART / START_TIMEOUT / CLEANUP_DONE |
| reason | STR(64) | Y | `EXIT_NONZERO` / `HEARTBEAT_MISSING` / `START_TIMEOUT` / `MACHINE_REVOKED` / … |
| exit_code | INT | Y | |
| detail | STR(2000) | Y | 已脱敏 |
| conn_epoch | INT | Y | 04 连接代次（栅栏，丢弃旧代次上报） |
| occurred_at | TS | N | |

索引：`(tenant_id, agent_id, occurred_at DESC)`。

### 3.5 `fleet_agent_retirements`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| agent_id | ID | N | | 部分唯一 `(agent_id) WHERE state IN ('IN_PROGRESS')` |
| mode | ENUM | N | | `RetirementMode`：NORMAL / FORCED |
| state | ENUM | N | `IN_PROGRESS` | `RetirementState`：IN_PROGRESS / COMPLETED / CANCELLED / FAILED |
| step | ENUM | N | `PRECHECK` | `RetirementStep`：PRECHECK / HANDOVER / SUCCESSOR / REASSIGN / CLEANUP / REVOKE / DONE |
| step_started_at | TS | N | | 步骤超时依据 |
| precheck | JSON | Y | | `{open_items:[{work_item_id, project_id, state, role}], running_execution_ids[], lead_of_project_ids[], machine_state}` |
| handover_execution_id | ID | Y | | 03 ADHOC 执行（Agent 自写交接） |
| handover_artifact_id | ID | Y | | 03 Artifact（`ArtifactKind=HANDOVER_DOC`，CCR 给 03，§12） |
| handover_source | ENUM | Y | | AGENT / SYSTEM（Agent 未在时限内产出时的系统摘要） |
| successor_kind | ENUM | Y | | HUMAN / AGENT / LATER |
| successor_principal_id | ID | Y | | |
| reassign_result | JSON | Y | | `{reassigned:[work_item_id], released:[work_item_id], failed:[{work_item_id, code}]}` |
| cleanup_status | ENUM | Y | | PENDING / ACKED / TIMED_OUT / DEFERRED |
| reason | STR(2000) | N | | ≥10 字符 |
| confirm_name_matched | BOOL | N | false | FORCED 必须 true |
| reauth_at | TS | Y | | FORCED step-up 时间 |
| completed_at | TS | Y | | |
| version | INT | N | 1 | |

### 3.6 `fleet_agent_stats`（绩效投影，可重建）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| agent_id | ID | N | PK(agent_id, work_item_type, window_days) |
| work_item_type | STR(32) | N | `WorkItemType` 或 `*`（全部） |
| window_days | INT | N | 30 或 90 |
| first_decisions | INT | N | 窗口内「首次裁决」数（§5 BR-05-30 定义） |
| first_pass_accepts | INT | N | 其中首次即 APPROVE 数 |
| claims | INT | N | 窗口内本 Agent 提交的声明数 |
| rework_decisions | INT | N | 针对这些声明的 REJECT + REQUEST_CHANGES 数 |
| accepted_items | INT | N | 本 Agent 为最终执行者、窗口内 ACCEPTED 的工作项数 |
| cost_on_accepted_micros | BIGINT | N | 这些工作项上本 Agent 全部执行成本（含其返工、落选 ENSEMBLE 枝） |
| computed_at | TS | N | |
| source_watermark | TS | N | 已计入的最晚事件时间 |

`current_load` 不入表，实时计算（BR-05-33）。

### 3.7 `fleet_dispatch_proposals`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| project_id | ID | N | | 项目实体 |
| work_item_id | ID | N | | 部分唯一 `(work_item_id) WHERE state='PROPOSED'`（MySQL 生成列 `open_key`） |
| role | ENUM | N | `EXECUTOR` | `AssignmentRole`：EXECUTOR；REVIEWER 仅用于 AUTONOMOUS 项目的评审派单（BR-05-53） |
| trigger | ENUM | N | | `DispatchTrigger`：READY / MANUAL_REQUEST / CHANNEL_REQUEST / REDISPATCH / ENSEMBLE / REVIEW |
| trigger_ref | STR(64) | Y | | 如 `message:<id>`、`agent:<id>:CRASHED` |
| requested_by | ID | Y | | 发起的人（MANUAL / CHANNEL） |
| policy | ENUM | N | | 生成时的 `DispatchPolicy`（快照） |
| lead_agent_id | ID | Y | | 生成时的主导 Agent（空 = 纯系统评分） |
| result | ENUM | N | | `DispatchResult`：CANDIDATE / NO_ELIGIBLE |
| recommended_agent_id | ID | Y | | 最终推荐（LLM 选择或系统 top-1） |
| system_top_agent_id | ID | Y | | 评分第一 |
| rationale | STR(2000) | Y | | 理由文本（会进入 ASSIGN Decision 的 rationale） |
| rationale_source | ENUM | N | | `RationaleSource`：LEAD / SYSTEM |
| requires_human | BOOL | N | | 护栏判定结果 |
| guard_reasons | JSON | N | `[]` | 触发的护栏码（G1–G9） |
| scoring_version | STR(16) | N | `s1` | 公式版本，保证可复算 |
| inputs_digest | STR(64) | N | | 评分输入快照的 SHA-256 |
| planning_cost_micros | BIGINT | N | 0 | 主导 Agent 规划调用成本 |
| state | ENUM | N | `PROPOSED` | `DispatchProposalState` |
| resolution_kind | ENUM | Y | | `DispatchResolution`：AUTO_APPLIED / CONFIRMED / OVERRIDDEN / DISMISSED / EXPIRED_TTL / EXPIRED_STALE / EXPIRED_WORK_ITEM |
| resolved_by | ID | Y | | 人或 SYSTEM 空 |
| resolved_assignee_principal_id | ID | Y | | 实际受派者 |
| assignment_id / decision_id | ID | Y | | 02 返回 |
| override_reason | STR(2000) | Y | | 改派 / 驳回理由 |
| expires_at | TS | N | | 创建 + 30 min（BR-05-46） |
| resolved_at | TS | Y | | |
| version | INT | N | 1 | |

索引：`(tenant_id, project_id, state, created_at DESC)`；`(state, expires_at)`；`(tenant_id, work_item_id, created_at DESC)`。

### 3.8 `fleet_dispatch_candidates`（评分明细，只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| proposal_id | ID | N | PK(proposal_id, agent_id) |
| agent_id | ID | N | |
| eligible | BOOL | N | |
| ineligible_codes | JSON | N | `["E3_MACHINE_OFFLINE", …]` |
| skill_bp / fpa_bp / cost_bp / load_bp / diversity_bp | INT | Y | 0–10000 基点（仅 eligible） |
| score | INT | Y | 0–1,000,000 |
| rank | INT | Y | 1 起；仅 eligible |
| inputs | JSON | N | 原始输入（标签、n/a、成本、负载计数）用于复算 |

### 3.9 `fleet_ensemble_plans` 与 `fleet_ensemble_branches`（v1.1）

`fleet_ensemble_plans`：

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK；同时作为 03 的 `ensemble_group_id` |
| project_id / work_item_id | ID | N | | 部分唯一 `(work_item_id) WHERE state NOT IN ('SELECTED','CANCELLED')` |
| trigger | ENUM | N | | `EnsembleTrigger`：RISK_HIGH / UNCERTAINTY / MANUAL |
| branch_count | INT | N | | 2 – min(3, 07 `ensemble_max_branches`)；07 设 4–5 时上限随之（硬顶 5） |
| budget_cap_micros | BIGINT | N | | BR-05-62 |
| spent_micros | BIGINT | N | 0 | 03 `execution.cost_updated` 累计 |
| deadline_at | TS | N | | 启动 + 4 h（可设 1–24 h） |
| state | ENUM | N | | `EnsembleState`：PENDING_APPROVAL / RUNNING / COMPARING / SELECTION_PENDING / SELECTED / SYNTHESIZING / CANCELLED |
| report | JSON | Y | | 对比报告（§6.5 Schema） |
| system_pick_branch_id / lead_pick_branch_id | ID | Y | | |
| selected_branch_id | ID | Y | | 或合成枝 |
| selected_by | ID | Y | | |
| selection_reason | STR(2000) | Y | | |
| approved_by | ID | Y | | 预算 / 人工批准者 |
| version | INT | N | 1 | |

`fleet_ensemble_branches`：`id ID PK`、`plan_id ID N`、`agent_id ID N`、`runtime_kind ENUM N`、`model_id STR(160) N`、`execution_id ID Y`（03 返回）、`branch_cap_micros BIGINT N`、`outcome ENUM N`（`EnsembleBranchOutcome`：RUNNING / SUBMITTED / FAILED / STOPPED_BUDGET / STOPPED_DEADLINE / SELECTED / NOT_SELECTED / SYNTHESIS）、`synthesis_of JSON Y`（被合成的枝 id）、`archived_at TS Y`；UNIQUE(`plan_id`,`agent_id`)。

### 3.10 collab 表

**`collab_channels`**

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| project_id | ID | N | | | 项目实体（DataScoped） |
| kind | ENUM | N | | `ChannelKind`：PROJECT_DEFAULT / CUSTOM / AGENT_DIRECT | |
| name | STR(40) | N | | BR-05-80 | DEFAULT 名 = 项目名截断（随 07 改名同步）；AGENT_DIRECT 名 = `dm-<agentA>-<agentB>` |
| topic | STR(200) | Y | | | |
| direct_pair_key | STR(48) | Y | | `<小id>:<大id>` | 部分唯一 `(project_id, direct_pair_key) WHERE kind='AGENT_DIRECT'` |
| last_seq | BIGINT | N | 0 | | 行锁分配消息序号 |
| last_message_at | TS | Y | | | |
| archived_at | TS | Y | | | 归档 = 只读 |
| version | INT | N | 1 | | |

索引：部分唯一 `(project_id) WHERE kind='PROJECT_DEFAULT'`；部分唯一 `(project_id, lower(name)) WHERE archived_at IS NULL AND kind='CUSTOM'`；`(tenant_id, project_id, archived_at)`。

**`collab_channel_members`**：`channel_id ID N`、`principal_id ID N`（PK 二者）、`member_kind ENUM N`（HUMAN / AGENT，即 00 §4 actor_kind 子集）、`role ENUM N` 默认 `MEMBER`（`ChannelMemberRole`：OWNER / MEMBER）、`source ENUM N`（DERIVED：由项目角色绑定派生 / EXPLICIT）、`last_read_seq BIGINT N 0`、`muted BOOL N false`、`joined_at TS N`、`left_at TS Y`。索引：`(tenant_id, principal_id, left_at)`。

**`collab_messages`**

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| channel_id | ID | N | | UNIQUE(channel_id, seq) | |
| project_id | ID | N | | | 冗余，行过滤 |
| seq | BIGINT | N | | | 频道内单调，从 1 起 |
| kind | ENUM | N | | `MessageKind`：TEXT / EVENT / DECISION_CARD / DISPATCH_CARD | |
| author_kind | ENUM | N | | HUMAN / AGENT / SYSTEM | |
| author_id | ID | Y | | | SYSTEM 空 |
| body | STR(8000) | Y | | 仅 TEXT 必填 | 纯文本 + `<@principal_id>` 提及令牌（BR-05-84） |
| payload | JSON | Y | | 按 kind 的 Schema（§6.7） | |
| fold_key | STR(64) | Y | | | `execution:<id>` / `work_item:<id>` / `dispatch:<work_item_id>` / `ensemble:<id>` |
| attention | BOOL | N | false | | true 的 EVENT 不折叠 |
| ref_kind / ref_id | STR(32) / ID | Y | | | 关联对象（工作项、执行、裁决、提案） |
| source_event_id | STR(64) | Y | | 部分唯一 `(channel_id, source_event_id) WHERE source_event_id IS NOT NULL` | 事件投影幂等 |
| trigger_depth | INT | N | 0 | 0–3 | Agent 互相 @ 的链深（BR-05-88） |
| reply_to_message_id | ID | Y | | | |
| edited_at / edit_count | TS / INT | Y / N | / 0 | edit_count ≤10 | |
| retracted_at / retracted_by | TS / ID | Y | | | 撤回（不物理删除） |
| version | INT | N | 1 | | |

索引：`(channel_id, seq DESC)`；`(tenant_id, project_id, created_at DESC)`；`(ref_kind, ref_id)`。

**`collab_message_mentions`**：`message_id ID`、`principal_id ID`（PK 二者）、`channel_id ID N`、`mention_kind ENUM N`（HUMAN / AGENT）、`delivered_at TS Y`（Agent 投递到机器的时间）、`read BOOL N false`；索引 `(tenant_id, principal_id, read)`。

**`collab_message_revisions`**：`message_id ID`、`revision INT`（PK 二者）、`body STR(8000) N`、`edited_by ID N`、`edited_at TS N`。仅 `audit_read` 可见。

**`collab_channel_hooks`（v1.1）**：`id`、`channel_id`、`kind ENUM`（OUTGOING_WEBHOOK）、`target_url STR(512)`（仅 https）、`secret_sealed STR(512)`（09 `SecretSealer`）、`filter JSON`（`{kinds:[...], mentions_only:bool}`）、`enabled BOOL`、`failure_count INT`、`disabled_reason STR(200)`、`version`。

**幂等**：fleet / collab 不建模块幂等表，`Idempotency-Key` 统一由 09 C7 平台幂等辅助处理（00 §9）；领域事件消费去重统一用 09 `domain_event_receipts`，本模块只保留业务唯一键（如 `collab_messages(channel_id, source_event_id)`）。

---

## 4. 状态机

### 4.1 AgentState（`DRAFT, ACTIVE, STOPPED, CRASHED, RETIRING, RETIRED`）

| # | 当前 | 事件 / 命令 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| A01 | — | `POST /tenants/{t}/agents`（`start=false`） | BR-05-01–07 | DRAFT | 同事务：服务账号 + 角色行 + 审计 `fleet.agent_created` + `agent.created`；事务后：凭证经 04 投递 |
| A02 | — | 同上 `start=true` | 同上且机器 ONLINE | DRAFT → 投递凭证后自动 A03 | |
| A03 | DRAFT / STOPPED | `PUT /agents/{id}/run-state {desired:ACTIVE}` | `agent_manage`；机器 ONLINE 非 DRAINING；runtime AVAILABLE；`credential_delivery=DELIVERED`；非 restart_exhausted 或本次为人工 | ACTIVE（process STARTING） | 写 `agent.start` 机器命令；`agent.state_changed`；延迟任务 start-timeout 60 s |
| A04 | ACTIVE·STARTING | 机器上报 `agent.status{RUNNING}` | conn_epoch 当前 | ACTIVE·RUNNING | `running_config_revision=config_revision` |
| A05 | ACTIVE | `PUT run-state {desired:STOPPED}` | `agent_manage` | STOPPED（STOPPING→EXITED） | `agent.stop` 命令；03 E27 取消其 AUTONOMOUS 执行；02 BR-02-16 回收分派 |
| A06 | ACTIVE·STARTING | 60 s 无 RUNNING | — | CRASHED（START_TIMEOUT） | 进入自动重启判定（BR-05-12） |
| A07 | ACTIVE·RUNNING | `agent.status{EXITED, exit_code≠0}` 或连续 3 次心跳缺失该 Agent（机器 ONLINE） | — | CRASHED | 账本 CRASHED；`agent.state_changed`；频道 attention 事件；其 RUNNING / PAUSED 执行由 03 E19b 置 INTERRUPTED（`AGENT_CRASHED`），**不取消、不自动续跑** |
| A08 | ACTIVE | 机器 OFFLINE | — | ACTIVE·UNREACHABLE（状态不变） | 不计崩溃；派单不合格（E3）；5 min 后触发重派（BR-05-52） |
| A09 | ACTIVE·UNREACHABLE | 机器恢复且上报 RUNNING / 未上报该进程 | — | ACTIVE·RUNNING / 写 `agent.start`（视为恢复，不计重启次数） | |
| A10 | CRASHED | 自动重启任务 | `auto_restart`；窗口内次数 <3；机器 ONLINE | ACTIVE·STARTING | 计数 +1；账本 AUTO_RESTART |
| A11 | CRASHED | 自动重启判定不满足（次数已 3） | — | CRASHED（`restart_exhausted=true`） | 通知 N-05-01；触发重派（BR-05-52） |
| A12 | CRASHED | `POST /agents/{id}/restarts` | `agent_manage`；机器 ONLINE | ACTIVE·STARTING | 计数与 exhausted 清零；账本 MANUAL_RESTART |
| A13 | ACTIVE | `POST /agents/{id}/restarts` | `agent_manage` | ACTIVE（STOPPING→STARTING） | 不计入自动重启窗 |
| A14 | CRASHED | `PUT run-state {desired:STOPPED}` | | STOPPED | |
| A15 | DRAFT / STOPPED / CRASHED / ACTIVE | `POST /agents/{id}/retirements {mode:NORMAL}` | `agent_retire`；机器 ONLINE；无进行中退役 | RETIRING | 不再参与派单；审计 `fleet.agent_retirement_started` |
| A16 | RETIRING | 退役 DONE | §4.2 | RETIRED | 吊销凭证；`agent.retired`；审计 |
| A17 | RETIRING | 取消退役（step ∈ PRECHECK…SUCCESSOR） | `agent_retire` | STOPPED | 已生成的交接文档保留 |
| A18 | 除 RETIRED 外任意 | `POST /agents/{id}/retirements {mode:FORCED}` | `agent_retire` + HUMAN + step-up ≤5 min + `confirm_name` 完全相等 | RETIRED（单事务） | BR-05-25 |
| A19 | 任意非终态 | 04 `machine.state_changed{to:REVOKED}` | — | STOPPED（`machine_id` 保留，需换绑） | 与 04 A11 `stopped_agent_ids` 一致 |

**非法迁移**（409 `142010 agent.illegal_transition`，`data.state` 返回当前值）：RETIRED 上的任何命令；RETIRING 上的 run-state / restarts / machine-binding / PATCH（除 `persona`）；DRAFT 上的 restarts；STOPPED 上的 restarts（用 run-state）。守卫失败用专用码：机器非 ONLINE 142002；runtime 不可用 142003；凭证未送达 142008；正常退役但机器非 ONLINE 142024（UI 引导强制退役）。

### 4.2 退役步骤（`RetirementStep`）

| # | 步骤 | 进入动作 | 完成条件 | 超时 | 失败 / 超时处理 |
|---|---|---|---|---|---|
| R1 | PRECHECK | 读 02 在途分派、03 运行中执行、07 是否为主导、04 机器状态，写 `precheck` | 同步完成 | — | 机器非 ONLINE → 422 142024 |
| R2 | HANDOVER | 若进程未 RUNNING 先以「交接模式」启动（不接新派单）；经 03 `ExecutionLauncher.Launch(kind=ADHOC, purpose=HANDOVER)` 让 Agent 按模板写交接文档 → 03 Artifact | Artifact 写入 | 10 min | 系统按数据生成摘要 Artifact（`handover_source=SYSTEM`），向导提示「Agent 未完成交接，已使用系统摘要」，可〔重试〕一次 |
| R3 | SUCCESSOR | 人选择 HUMAN / AGENT / LATER | `PATCH /agent-retirements/{id}` 写入 | 无（可停留） | 接手人不合格 → 422 142018 |
| R4 | REASSIGN | 对 precheck 中每个 ACTIVE 分派调 02 `DispatchAssigner.Reassign`（HUMAN/AGENT）或 `Release`（LATER），每项独立 | 全部有结果 | — | 单项失败记入 `reassign_result.failed`，不阻断；UI 列出失败项（工作项将由 02 BR-02-16 在 RETIRED 时回收） |
| R5 | CLEANUP | 停止进程；`agent.cleanup` 命令（删除 Agent 私有目录，**不碰项目工作目录**） | 机器 ACK | 5 min | `cleanup_status=TIMED_OUT` → 置 `pending_cleanup_machine_id`，继续 R6 |
| R6 | REVOKE | 同一事务：底座吊销全部凭证并停用服务账号（token_version+1）；角色行 `revoked_at`；经 core 移除全部项目实体角色绑定；经 08 `DelegationRevoker` 撤销该主体全部委托；频道成员 `left_at` | 事务提交 | — | 失败整体回滚，可重试 |
| R7 | DONE | Agent → RETIRED；`agent.retired`；审计 `fleet.agent_retired` | — | — | — |

交接文档模板（Agent 与系统摘要共用标题）：`## 在途工作项`（状态、分支、阻塞）`## 进行中执行与未决交互` `## 近 30 天裁决与拒绝反馈摘要` `## 规则型记忆清单（引用 06）` `## 频道中未闭合的对话` `## 给接手人的注意事项`。系统摘要只填前 5 节（数据可得）。

退役状态非法：COMPLETED/CANCELLED/FAILED 上任何 PATCH → 409 142015；步骤乱序（如 PRECHECK 直接写 successor 以外字段）→ 409 142015；REASSIGN 之后取消 → 409 142015。

### 4.3 DispatchProposalState（`PROPOSED, APPLIED, OVERRIDDEN, EXPIRED`）

| # | 当前 | 事件 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| P01 | — | 生成（READY 事件 / 手动 / 频道 / 重派 / 评审） | 工作项状态按触发：READY（READY / MANUAL_REQUEST / CHANNEL_REQUEST / ENSEMBLE）、NEEDS_HUMAN_REVIEW（REVIEW）——此二者要求无同角色 ACTIVE 分派；**REDISPATCH** 允许 ∈ {CLAIMED, IN_PROGRESS, EXECUTION_FAILED} 且**保留现有 ACTIVE EXECUTOR 分派**（应用时经 02 `DispatchAssigner.Reassign`，即 02 T07）；同工作项无 PROPOSED；**07 项目阶段 ∈ {ACTIVE, ACCEPTANCE_PENDING}**（`MANUAL_REQUEST` 除外，见 BR-05-55） | PROPOSED | 评分明细；`dispatch.proposed`（含 `policy`、`requires_human`）；DISPATCH_CARD（`requires_human=true`）或折叠 EVENT（AUTO 将自动应用） |
| P02 | PROPOSED | AUTO 自动应用 | policy=AUTO；`requires_human=false`；07 `ProjectGate.CanAutoDispatch` = true；应用时复验 E1–E11 | APPLIED（AUTO_APPLIED） | 同事务调 02 `DispatchAssigner.Assign`（REDISPATCH 用 `Reassign`；actor=主导 Agent，`authority_source=POLICY:dispatch_auto`，`detail.dispatch_proposal_id`）；`dispatch.applied` |
| P03 | PROPOSED | 人确认 `PUT …/resolution {kind:CONFIRM}` | 人持 `work_item_assign`；复验 E1–E11 | APPLIED（CONFIRMED） | 02 `Assign`（actor=人，authority 由 AuthorityChecker 给出 ROLE / DELEGATION，`detail.dispatch_proposal_id`） |
| P04 | PROPOSED | 人改派 `{kind:OVERRIDE, assignee_principal_id, reason}` | 同上；受派者满足 02 BR-02-10 | OVERRIDDEN | 02 `Assign`（人）；被弃候选对该工作项 24 h 排除（BR-05-50）；审计 `fleet.dispatch_overridden` |
| P05 | PROPOSED | 人驳回 `{kind:DISMISS, reason}` | 同上 | OVERRIDDEN（DISMISSED） | `dispatch.rejected`；工作项 2 h 内不再自动生成提案（BR-05-50） |
| P06 | PROPOSED | `expires_at` 到期 | — | EXPIRED（TTL） | 若工作项仍 READY → 立即重新生成（计入抖动上限） |
| P07 | PROPOSED | 应用时复验失败，或 02 `DispatchAssigner` 返回错误 | — | 复验失败：EXPIRED（STALE）；02 错误：提案不变（同事务回滚） | 复验失败返回 409 143005 并重新生成；02 错误原样返回确认人（如 409 120426 并发认领、409 120420 阶段不允许） |
| P08 | PROPOSED | 按触发类型判定工作项已失去提案前提（复审 B4）：REDISPATCH → 工作项离开 {CLAIMED, IN_PROGRESS, EXECUTION_FAILED} 或其 ACTIVE EXECUTOR 分派已变更；REVIEW → 离开 NEEDS_HUMAN_REVIEW；其他 → 离开 READY；任一类型：被其他途径分派 / 取消 | — | EXPIRED（WORK_ITEM） | 卡片置灰「已由他人处理」 |

非法：非 PROPOSED 上任何 resolution → 409 143004（`data.state`）；版本不符 409 140409。

### 4.4 EnsembleState（v1.1）

| # | 当前 | 事件 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| H01 | — | 创建计划 | BR-05-60/61 | PENDING_APPROVAL（需人）或 RUNNING（AUTO 且预算内） | `ensemble.plan_created{requires_approval}`；PENDING_APPROVAL → 08 我的裁决 `ENSEMBLE_APPROVAL` + N-05-11 |
| H02 | PENDING_APPROVAL | 人批准 `PUT /ensemble-plans/{id}/approval` | HUMAN + `work_item_assign`（预算超默认还需 `project_manage`） | RUNNING | 同事务为每枝调 03 `ExecutionLauncher.Launch(branch_reason=ENSEMBLE, ensemble_group_id=plan.id, max_cost_micros=branch_cap)`；审计 `fleet.ensemble_started` |
| H03 | RUNNING | 全部枝终态（SUBMITTED/FAILED/STOPPED_*） 或 deadline | — | COMPARING | 拉 03 A08 对比数据 + 验证摘要；请求主导 Agent 写比较理由（120 s） |
| H04 | RUNNING | `spent_micros ≥ budget_cap` | — | COMPARING | 对每个仍运行的枝调 03 `CancelExecution(execution_id, ENSEMBLE_BUDGET)` → STOPPED_BUDGET；通知 N-05-07 |
| H05 | COMPARING | 报告生成 | ≥1 枝 SUBMITTED | SELECTION_PENDING | `ensemble.compared`；频道 DISPATCH_CARD（对比卡，不折叠） |
| H06 | COMPARING | 0 枝 SUBMITTED | — | CANCELLED（ALL_FAILED） | 调 02 `WorkItemTransitioner.EnsembleFailed(tx, work_item_id, plan_id)`；`ensemble.cancelled{reason:ALL_FAILED}`；通知 |
| H07 | SELECTION_PENDING | AUTO 且 lead_pick = system_pick 且风险 ≠ HIGH | — | SELECTED | 选中枝的声明提交给 02（§6.5 BR-05-66）；其余 NOT_SELECTED + `archived_at` |
| H08 | SELECTION_PENDING | 人选择 `PUT /ensemble-plans/{id}/selection {branch_id \| synthesize}` | HUMAN + `work_item_assign` | SELECTED / SYNTHESIZING | 审计 `fleet.ensemble_selected` |
| H09 | SYNTHESIZING | 合成枝执行终态且 SUBMITTED | — | SELECTED | 合成枝 = 新 ENSEMBLE 执行，父为被选主枝，`synthesis_of` 记录被合成枝 |
| H10 | 非终态 | `PATCH /ensemble-plans/{id} {state:CANCELLED, reason}` | HUMAN + `work_item_assign` | CANCELLED | 对运行中枝逐个调 03 `CancelExecution(execution_id, ENSEMBLE_CANCELLED)`；`ensemble.cancelled{reason:MANUAL}`；保留全部产物 |
| H11 | PENDING_APPROVAL | 创建后 24 h 无人批准（`fleet.ensemble_timers`） | — | CANCELLED（APPROVAL_EXPIRED） | `ensemble.cancelled{reason:APPROVAL_EXPIRED}`；关闭 `ENSEMBLE_APPROVAL` 待办；工作项回到单派（P01） |
| H12 | SELECTION_PENDING | 进入后每满 24 h 仍未选择 | — | SELECTION_PENDING（不变） | 再发 N-05-08 提醒，最多 3 次，之后只保留待办 |

非法：终态（SELECTED / CANCELLED）上的任何命令 → 409 143013；SELECTION_PENDING 以外选择 → 409 143012。

### 4.5 频道与消息

频道：`ACTIVE`（`archived_at IS NULL`）⇄ `ARCHIVED`。PROJECT_DEFAULT 只能随项目进入 CLOSED / CANCELLED 被系统归档（消费 07 `project.stage_changed`），人工归档 → 409 145011；AGENT_DIRECT 不可归档 / 改名（145011）。归档频道写消息 / 加成员 → 409 145008；项目重开（不存在此迁移）不适用。

消息：`POSTED → EDITED*（≤10 次，作者，15 min 内，仅 TEXT）→ RETRACTED`（终态）。EVENT / DECISION_CARD / DISPATCH_CARD 只由系统写，作者不可编辑 / 撤回（409 145006）；卡片的「已处理」状态由 `payload.status` 投影更新（`message.updated`，不计 edit_count）。RETRACTED 上编辑 / 撤回 → 409 145006。

---

## 5. 业务规则与边界情况

### 5.1 创建与档案

- **BR-05-01 名称与 handle**：`name` 1–64 字符、去首尾空白；`handle` 缺省由 name 转写（拼音 / 去重音 → 小写 → 非字母数字替换为 `-`），冲突追加 `-2…-99`；用户显式提供的 handle 冲突 → 422 142001。租户内非 RETIRED Agent ≤50（配置 `fleet.max_agents`，超出 422 142009，**本规格提议**：与 04 每租户 200 台机器、ICP 3–20 人团队量级匹配）。
- **BR-05-02 原子创建**：`POST /tenants/{tenant_id}/agents` 在**一个数据库事务**内：① identity `ServiceAccountProvisioner.CreateOn(tx)`（04 CR-04-08 已申请的事务内版本；`login_name=agent-<handle>`，`display_name=name`，无过期）→ ② 插 `fleet_agents`（DRAFT）与 `fleet_agent_roles` → ③ 06 `SkillMounter.MountOn(tx, agent_id, skill_ids)`（CCR-05-07）→ ④ identity 头像 `PrincipalAvatarSetter.SetOn(tx, principal, style, seed)`（底座新增，见 §12）→ ⑤ 审计 `fleet.agent_created`（detail 含 roles、machine、runtime、model、authorization_mode）→ ⑥ outbox `agent.created`。**任一步失败整体回滚**：无 Principal、无租户成员、无 Agent 行、无挂载、无审计（AC-05-02）。任一跨模块能力未注册 → 路由仍注册，请求 503 142030，启动 `slog.Warn` 指明缺失能力（禁止静默降级）。创建时**不写项目角色绑定**：Agent 入项目由 07 / 底座在项目实体上绑定 `agent_*` 角色（07 BR-07-02），本模块按 §3.3 映射校验（07 调 `AgentDirectory`）。
- **BR-05-03 事务后步骤（补做，不撤销）**：提交后：⑦ 底座签发服务账号凭证（`credential_id`）→ ⑧ 经 04 通道下发 `agent.provision{agent_id, client_id, client_secret}`（机器端以 09 `SecretSealer` 同等级本地加密存储），机器 ACK → `credential_delivery=DELIVERED` → 若 `start=true` 执行 A03。⑦⑧ 失败重试 5 次（10 s/30 s/90 s/5 min/15 min）后 `credential_delivery=FAILED`，Agent 保持 DRAFT，详情页显示〔重新投递〕（`POST /agents/{id}/credential-deliveries`）。机器离线时 ⑧ 挂起，重连后投递。凭证**只给机器上的 Agent 宿主进程**用于以 Agent 身份调用控制面（发频道消息、写记忆、提交提案），**绝不注入 runtime 子进程环境**（PRD §10.2、B §17.2）。
- **BR-05-04 运行时与模型校验**：经 04 `MachineDirectory.ListEligibleMachines({runtime, model, agent_machine_id})`：机器须未吊销、runtime AVAILABLE（否则 422 142003）；模型 ∉ 目录 ∪ 手填 → 422 142004；`reasoning_effort` 不在该 runtime 支持集合 → 422 142026。DRAFT 允许绑定 OFFLINE 机器（`start=false` 时）；`start=true` 要求 ONLINE（142002）。
- **BR-05-05 MCP 白名单**：每项须出现在绑定机器最近一次 04 能力检测（该 runtime）的 MCP 清单中，否则 422 142007（`data.unknown[]`）；检测结果过期 >24 h 时接受但标记 `stale=true` 并在详情页提示。白名单只约束 Agent 宿主为执行注入的 MCP 配置；MCP 不承载任何项目领域状态（评审 P1）。
- **BR-05-06 角色集合**：非空，≤4，元素唯一；`LEAD` 可单独存在；含 `REVIEWER` 时 `authorization_mode` 不得为 FULL_AUTO（422 142005，**本规格提议**：评审员不应具备无人值守写权限）。角色变更（PATCH）在项目实体上的既有绑定由本模块经 core `EntityRoleBindingStore.PutOn/RemoveOn(tx)` 同事务增删（同映射），使「角色集合」与权限一致。
- **BR-05-07 授权模式**：设为 FULL_AUTO 须 HUMAN + step-up（purpose=`agent_full_auto`）+ 审计 `fleet.agent_full_auto_enabled`（00 §7.2 YOLO）；Agent 主体修改自身任何档案字段 → 403 140403（`reason_code=SELF_MANAGE`）。
- **BR-05-08 修改与生效**：PATCH 需 If-Match（缺 428 140428，不符 409 140409）；影响进程的字段（§3.2 `config_revision` 所列）在 ACTIVE 时不重启进程，`running_config_revision` 落后即 UI 显示「需重启生效」；`machine_id / runtime_kind / model_id` 只能经 `PUT /agents/{id}/machine-binding`。
- **BR-05-09 头像**：`avatar_seed` 缺省 = `principal_id mod 2^53` 的 xorshift 结果；`GET /agents/{id}/avatar-candidates` 返回 6 个种子预览（同风格），选择即 PATCH `avatar_seed`。相同 `(style, seed)` 在所有客户端生成相同像素图（底座渲染器，`ActorAvatar` 圆角方形 + 芯片角标）。

### 5.2 运行与崩溃

- **BR-05-10 启动 / 停止语义**：命令经 04 通道（`ServerBatch.agent_commands[]`，CCR-05-06）下发到绑定机器；机器以 `agent.status` 上报（随每次 poll 的 `agents:[{agent_id, status, pid?, exit_code?, config_revision}]`，以及退出时立即上报）。STOP 先请求优雅退出，20 s 后机器强杀进程树。
- **BR-05-11 崩溃检测**：(a) 机器上报 `EXITED` 且非本系统发起的 STOP；(b) 机器 ONLINE 且连续 3 次心跳（≈30 s，按 04 心跳 10 s 节奏）未列出该 RUNNING Agent；(c) STARTING 超 60 s。机器 OFFLINE 时不判崩溃（A08）。旧 `conn_epoch` 的上报丢弃（04 BR-04-31 栅栏）。
- **BR-05-12 自动重启上限**（**本规格提议**，PRD 只要求 restart）：滑动窗 30 min 内最多 3 次自动重启，退避 10 s → 30 s → 90 s；进程连续 RUNNING ≥10 min 视为稳定，计数清零；第 4 次崩溃落在窗口内 → `restart_exhausted=true`、不再自动重启、通知 N-05-01、触发重派（BR-05-52）。`auto_restart=false` 时任何崩溃直接走 exhausted 分支。人工重启（A12）不受上限约束并清零。自动重启**只恢复 Agent 进程**：崩溃时其 RUNNING 执行已由 03 置 INTERRUPTED（`AGENT_CRASHED`），不续跑；工作项按 02 T19（`failure_kind=RUNTIME`，不自动重试）进入 EXECUTION_FAILED，由人恢复或经 BR-05-52(d)(e) 重派。
- **BR-05-13 并发命令**：同一 Agent 的 run-state / restarts / machine-binding / retirements 在 Agent 行 `SELECT … FOR UPDATE`（SQLite 单写者）下串行；第二个请求看到中间态（STARTING / STOPPING）→ 409 142011 `agent.transition_in_progress`（`Retry-After: 5`）。run-state 与当前一致 → 200 原样返回（天然幂等）。
- **BR-05-14 换绑机器**：`PUT /agents/{id}/machine-binding {machine_id, runtime_kind, model_id, reasoning_effort?}`（If-Match）：Agent 须 DRAFT / STOPPED / CRASHED（否则 409 142010）；03 报告该 Agent 无非终态执行（否则 409 142012）；目标 = 当前 → 422 142013；目标须满足 BR-05-04/05（白名单中目标机不存在的 MCP 项 → 422 142007，需先编辑白名单）。同事务：旧凭证吊销并签发新凭证（`credential_delivery=PENDING`）、旧机 ONLINE 则下发 `agent.cleanup`，否则写 `pending_cleanup_machine_id`；审计 `fleet.agent_rebound`。
- **BR-05-15 机器吊销 / 排空**：04 `machine.state_changed{REVOKED}` → 该机全部 Agent → STOPPED（A19），需人工换绑；DRAINING 时 Agent 保持 ACTIVE 但不合格派单（E3）。
- **BR-05-16 工作目录浏览**：`GET /agents/{id}/workspace-entries?path=` 经 04 新增通道请求 `agent.workspace_list`（CCR-05-06），根限定为 Agent 私有目录与其执行工作目录，路径规范化后不得越出（`..`、符号链接越界 → 422 142027）；单次 ≤1000 项；文件内容 `…/workspace-entries/content?path=` ≤1 MiB（超出 413 142028）；机器离线 → 409 142002；只读。权限：`agent_read` + `machine_read`。

### 5.3 退役

- **BR-05-20 正常退役前置**：机器 ONLINE（否则 422 142024）；无 IN_PROGRESS 退役（409 142014）；若为某项目主导 Agent：不阻断，但 PRECHECK 列出并提示——07 BR-07-07 在收到 RETIRING 时置 `lead_agent_id=NULL`、`dispatch_policy=MANUAL`。
- **BR-05-21 接手人**：HUMAN 须活跃租户成员且对每个在途工作项所在项目有 `work_item_execute`；AGENT 须 ACTIVE、含 EXECUTOR、非本 Agent、在对应项目有 `agent_executor` 绑定且 `max_risk ≥` 工作项风险；不满足的工作项不阻断整体，按项进入 `failed`（code 120444 来自 02）或在 SUCCESSOR 步骤以 422 142018 列出（`data.items[]`）由人改选 LATER。
- **BR-05-22 转交即裁决**：每个转交经 02 `DispatchAssigner.Reassign(tx, …)`，02 记 ASSIGN Decision（actor=操作的人，authority 由 AuthorityChecker，reason「Agent 退役交接：<retirement_id>」，`rationale` 引用交接 Artifact id）。LATER → 02 `Release`（工作项回 READY / BLOCKED，标 `needs_owner`），派单循环可再分派。
- **BR-05-23 运行中执行**：REASSIGN 前，对该 Agent 非终态 AUTONOMOUS 执行由 03 E27 在 `agent.state_changed{RETIRING}` 时取消（SUPERSEDED）；交接模式下的 HANDOVER 执行豁免（purpose=HANDOVER，03 CCR）。
- **BR-05-24 取消**：仅 PRECHECK / HANDOVER / SUCCESSOR 步可取消；Agent → STOPPED（非原状态，避免自动恢复派单），已生成交接 Artifact 保留。
- **BR-05-25 强制退役**：单事务完成：Agent → RETIRED；退役记录 `mode=FORCED, state=COMPLETED, step=DONE`；系统交接摘要 Artifact（同步生成，失败不阻断，记 `handover_source=NULL` 并在审计 detail 标注）；凭证吊销 / 服务账号停用 / 绑定移除 / 委托撤销同 R6；在途分派由 02 BR-02-16 在 `agent.retired` 后回收为 READY；机器在线则下发 stop + cleanup，否则写 `pending_cleanup_machine_id`（机器重连首个 poll 下发，最多保留 30 天）。确认：HUMAN、`agent_retire`、step-up（purpose=`agent_forced_retire`，≤5 min）、`confirm_name` 与当前 `name` 逐字相等（不符 422 142016）、reason ≥10。审计 `fleet.agent_retired`（`detail.mode=FORCED`、丢失清单：在途工作项数、运行中执行数）。
- **BR-05-26 退役后**：handle 不复用；历史消息、裁决、证据中的 Agent 显示「已退役」徽标；不可被 @（422 145016）；06 冻结其 AGENT scope（消费 `agent.retired`）。

### 5.4 绩效统计（公式）

- **BR-05-30 首次验收率** `FPA(agent, type, W)`：对窗口 W（30 / 90 天）内**首次裁决时间**落在窗口的工作项集合 S —— 工作项 w ∈ S 当且仅当 agent 提交过 w 的声明，且 w 的第一个 ∈ {APPROVE, REJECT, REQUEST_CHANGES} 的 Decision 发生在 agent 的首个声明之后、并落在 W 内。`first_decisions = |S|`；`first_pass_accepts = |{w ∈ S : 该首个裁决 = APPROVE}|`；`FPA = first_pass_accepts / first_decisions`；`first_decisions < 5` 时 UI 显示「样本不足（n）」，评分用平滑值（BR-05-41）。与 07 项目级「首次验收率」（AC-07 accepted_first_count）同口径：返工后的再次 APPROVE 不计为首次。
- **BR-05-31 单位已接受成本** `CPA(agent, type, W)` = `cost_on_accepted_micros / accepted_items`：分母 = W 内进入 ACCEPTED 且最终被接受声明的执行者 = agent 的工作项数；分子 = 这些工作项上 agent 名下全部执行的 `cost_micros` 之和（含其返工枝、RETRY_INFRA 枝、ENSEMBLE 落选枝）；不含主导 Agent 规划成本（计入项目，不计个人）。分母 0 → `null`（显示「—」）。
- **BR-05-32 返工率** `RR(agent, W)` = `rework_decisions / claims`：分子 = W 内针对 agent 声明作出的 REJECT + REQUEST_CHANGES 数；分母 = W 内 agent 提交的声明数；分母 0 → `null`。可 >1（一个声明理论上只有一次裁决，故通常 ≤1；保留原值不截断）。
- **BR-05-33 当前负载**（实时，不缓存）：`active_items` = 02 中该 Agent ACTIVE EXECUTOR 分派数；`running_executions` = 03 非终态执行数；`load_ratio = active_items / max_parallel_items`。成员表显示 `active_items/max_parallel_items` 与进度条。
- **BR-05-34 刷新节奏**：全量重算任务每 10 min（仅对 24 h 内有声明 / 裁决 / 成本事件的 Agent）；增量：消费 `decision.made`、`claim.submitted`、`execution.cost_updated` 后对受影响 Agent 去抖 60 s 重算；页面显示 `computed_at`（「1 分钟前更新」）。重算以源数据（02/03 读接口）为准，`fleet_agent_stats` 可整体删除重建。

### 5.5 主导 Agent 与派单

- **BR-05-40 预过滤（硬条件，全部满足才进入评分）**，失败码写入 `ineligible_codes`：
  - E1 `ROLE`：AgentRole 含 `EXECUTOR`；REVIEW 派单（BR-05-53）改为含 `REVIEWER`，且须为 08 `ListEligible` 返回的被委托主体。
  - E2 `STATE`：`state=ACTIVE` 且 `process_status=RUNNING`。
  - E3 `MACHINE`：绑定机器 ONLINE、非 DRAINING，且 04 空闲槽 ≥1 **或** 该机排队命令 <2（**本规格提议**：允许短队列，避免空槽瞬间抖动导致无人可派）。
  - E4 `CAPABILITY`：工作项 `required_runtime`（02 字段，可空）为空或 = Agent runtime；Agent 模型仍在机器目录（`model_listed=true`）。
  - E5 `SKILL_REQUIRED`：工作项必需技能标签 ⊆ Agent 标签（`capability_tags` ∪ 06 `SkillCatalog.ForAgent` 返回技能的 `tags`）。
  - E6 `RISK`：工作项 risk ≤ Agent `max_risk`（LOW<MEDIUM<HIGH）。
  - E7 `MEMBER`：Agent 在该项目实体上持 `agent_executor` 绑定。
  - E8 `CAPACITY`：`active_items < max_parallel_items`。
  - E9 `NOT_EXCLUDED`：不在该工作项排除表（BR-05-50）。
  - E10 `NOT_LEAD_AUTO`：policy=AUTO 时排除项目主导 Agent 自身（职责分离，BR-05-70）。
  - E11 `BUDGET`：Agent 在该类型的 CPA（无则团队中位数）≤ 07 `ProjectGate` 返回的项目剩余预算；剩余预算未设 → 通过。
- **BR-05-41 评分**（`scoring_version=s1`，整数运算保证确定性）：各分量取 0–10000 基点，四舍五入（half-up）到整数：
  - `skill_bp = round(10000 × |T ∩ A| / |T|)`，T = 工作项必需 ∪ 偏好技能标签，A = Agent 标签；`|T|=0` → 10000。
  - `fpa_bp = round(10000 × (a + 4·p₀) / (n + 4))`：n、a 为该 Agent 在**同 WorkItemType**、90 天窗口的 `first_decisions`、`first_pass_accepts`；p₀ = 团队同类型 90 天 FPA（团队无数据 → 0.5）。即贝叶斯平滑，先验权重 4。
  - `cost_bp`：c_i = Agent 同类型 90 天 CPA，`null` 用团队同类型 CPA 中位数，团队也无 → 用 04 `ModelPrice` × 团队该类型平均 token 数，再无 → 所有候选同值；`cost_bp = 10000` 若 c_max = c_min，否则 `round(10000 × (c_max − c_i) / (c_max − c_min))`（仅在合格候选内归一）。
  - `load_bp = round(10000 × (1 − min(1, (active_items + queued_on_machine) / max_parallel_items)))`。
  - `diversity_bp = round(10000 × (1 − min(1, r / 5)))`，r = 该 Agent 过去 24 h 在本项目被 APPLIED 的提案数（近因多样性）。
  - `score = 35·skill_bp + 30·fpa_bp + 15·cost_bp + 15·load_bp + 5·diversity_bp`（0–1,000,000，展示为 `score/10000` 保留两位）。权重理由：技能匹配决定能否做对 > 历史质量 > 经济性与负载 > 轮换（**本规格提议**）。
  - **并列打破**（依次）：fpa_bp 高 → active_items 少 → c_i 低 → 最近一次被派时间早（从未被派最先）→ agent_id 数值小。
  - 输入快照（标签、n、a、p₀、c、负载、r）写入 `fleet_dispatch_candidates.inputs`，`inputs_digest` = 规范化 JSON 的 SHA-256；同快照必得同排名（AC-05-20）。
- **BR-05-42 主导 Agent 的 LLM 用法**：系统把前 K=5 名合格候选（含分量、理由素材）、工作项与验收标准、需求摘录、06 `Retriever.Search(purpose=LEAD)` 结果，经 04 通道 `agent.lead_plan` 请求发给主导 Agent 宿主（CCR-05-06），120 s 超时。主导 Agent 只能在**合格集合**内选择并给出 ≤2000 字理由（须引用至少一项分量，如「前端技能 3/3、首次验收率 82%、当前空闲」）。结果非法（选了不合格 / 不存在的 Agent、JSON 不符 Schema、超时）→ 采用系统 top-1，`rationale_source=SYSTEM`，理由由模板生成，并在卡片上标注「主导 Agent 未给出有效建议」。LLM 永不能绕过预过滤，也不能改分。项目无主导 Agent 时一律系统评分（`lead_agent_id=NULL`）。
- **BR-05-43 策略来源**：`dispatch_policy`、`lead_agent_id`、`ensemble_max_branches` 只存于 07 项目档案，经 07 `ProjectReader` 读取、经 07 #4 `PATCH /entities/{entity_id}/project` 写入（「团队编排」卡片直接调用 07 #4，本模块不存副本）。AUTO 但 `lead_agent_id=NULL` → 视作 MANUAL（`guard_reasons` 含 G9）。
- **BR-05-44 AUTO 护栏**（任一命中 → `requires_human=true`，提案停在 PROPOSED 并发 DISPATCH_CARD 与通知 N-05-03）：G1 工作项 risk=HIGH；G2 估算成本（c_i）> 07 剩余预算或预算 EXHAUSTED / `ProjectGate.CanAutoDispatch=false`（项目暂停、升级生效、只读；阶段非 ACTIVE 时不生成提案，见 BR-05-55）；G3 主导 Agent 选择非 top-1 且 `score_top − score_chosen > 150000`（15 分）；G4 本项目过去 60 min 已 AUTO_APPLIED ≥20 次；G5 该工作项 24 h 内已被派 ≥2 次（抖动）；G6 工作项 `require_human_decision=true`（02 模板字段）；G7 选中者为主导 Agent 自身（MANUAL 下也须人确认）；G8 工作项 24 h 内曾被人改派（人工意图优先）；G9 无主导 Agent。
- **BR-05-45 生效路径**：APPLIED 必须经 02 接口 `DispatchAssigner.Assign(ctx, tx, {work_item_id, assignee_principal_id, role, expected_work_item_version, actor, authority_source, rationale, dispatch_proposal_id})`（CCR-05-04；02 在其内部执行 A1 逻辑：AuthorityChecker、ASSIGN Decision、分派与租约、`work_item.assigned`），与提案状态更新**同一事务**；02 返回错误 → 提案不变更，向调用者返回 02 错误码（如 120426 并发认领）。随后 outbox `dispatch.applied`（仅供 collab 投影、08 关闭待办与通知；**02 不消费此事件**，分派已由 `DispatchAssigner` 同步落库）。AUTO：actor=主导 Agent（AGENT），`authority_source=POLICY:dispatch_auto`，提案 id 记于 `detail.dispatch_proposal_id`（00 §6.3 v1.5）；人确认 / 改派：actor=人，authority 由 08 判定。`trigger=REDISPATCH` 的提案一律经 `DispatchAssigner.Reassign` 应用（02 T07 把工作项置为 CLAIMED、以 RETRY_INFRA 续跑并由 03 将中断的父执行置为 FAILED `SUPERSEDED_BY_RETRY`，见 BR-05-52）。
- **BR-05-46 提案 TTL**：30 min（**本规格提议**：短于 Agent 租约 10 min × 3，长于人类一次查看间隔）；到期 P06 立即重生成，但同工作项 24 h 内累计生成 >6 次 → 停止自动生成并通知（抖动）。
- **BR-05-47 并发**：派单循环按项目取 dlock `fleet:dispatch:<project_id>`（maxHold 60 s）；应用时在本事务内对受派 Agent 行 `SELECT … FOR UPDATE` 并复验 E2/E3/E8（跨项目防超额）；两人同时确认同一提案 → 一人成功，另一人 409 140409（版本）或 143004（已非 PROPOSED）。
- **BR-05-48 队列顺序与饥饿**：同项目待派工作项按 `(aging_flag DESC, priority DESC, risk DESC, ready_at ASC, work_item_id ASC)` 处理，`aging_flag = ready_at < now − 2h`；每轮 ≤10 项；READY 无分派超 4 h → 通知负责人 N-05-04（每项每 4 h 一次）。
- **BR-05-49 无合格候选**：生成 `result=NO_ELIGIBLE` 的 PROPOSED 提案（无推荐人），附原因直方图（如「机器离线 2、技能不符 1、满载 1」）与最接近的 3 名（不合格码最少者）；不自动应用；人可 OVERRIDE 指派任一满足 02 BR-02-10 的成员（含人类）或 DISMISS。合格集合可能因事件改变：`agent.state_changed`、`machine.state_changed`、分派释放后去抖 30 s 重新评估，若出现合格者 → 原提案 EXPIRED(STALE) 并生成新提案。
- **BR-05-50 抖动与排除**：被改派弃用 / 被移走的 Agent 对该工作项排除 24 h（E9）；同一工作项 24 h 内派单 ≥3 次 → 停止自动生成，需人手动生成；DISMISS 后 2 h 内不自动生成。
- **BR-05-51 频道请求**：「@主导Agent 把 W 派给 Ada」或「@Agent 请做 X」只能产生 `trigger=CHANNEL_REQUEST` 的提案或 02 工作项草稿（Agent 以自身凭证调用 02 草稿接口），**永不直接改变状态**；CHANNEL_REQUEST 提案一律 `requires_human=true`（G 外的硬规则，防止频道文本注入绕过策略），卡片「确认」按钮仅对持 `work_item_assign` 的人可用。
- **BR-05-52 重派触发**：(a) Agent CRASHED 且 `restart_exhausted`；(b) Agent STOPPED / RETIRING / RETIRED（02 BR-02-16 已回收分派 → 工作项 READY → 正常 P01）；(c) 机器 OFFLINE 持续 5 min（**本规格提议**宽限，覆盖短暂网络抖动与 03 执行续约 30 s × 多次）：对该机 Agent 的 CLAIMED（未开始执行）分派经 02 `Release` 后重派；(d) 02 `work_item.state_changed{to: EXECUTION_FAILED}` 且 02 自动重试已用尽（payload `infra_retries_exhausted=true`）。(e) 机器 OFFLINE 持续 ≥5 min，且该机 Agent 名下有工作项其当前执行为 INTERRUPTED（迁移，PRD §10.1）：生成 REDISPATCH 提案；应用时 02 `DispatchAssigner.Reassign` 按 02 T07 先终结旧执行，再以 `branch_reason=RETRY_INFRA`、parent = 该 INTERRUPTED 执行启动新执行（03 BR-03-07 在同事务把 parent 置 FAILED `SUPERSEDED_BY_RETRY`）；原工作目录不可达，故一律 CARRYOVER（04 `PlanMigration`）。重派提案 `trigger=REDISPATCH`，前任对该工作项排除 24 h。
- **BR-05-53 评审派单（AUTONOMOUS，MVP）**：消费 02 `work_item.state_changed{to:NEEDS_HUMAN_REVIEW}`，项目 `team_mode=AUTONOMOUS` 时：调 08 `AuthorityChecker.ListEligible(work_item, APPROVE)`，取持有效委托、含 REVIEWER 角色、非该工作项执行者且非派出者的 Agent；按 BR-05-41 评分（`fpa_bp` 改为评审一致率：其 APPROVE 后 30 天内未被甲方拒收或返工的比例，无数据取 0.5），取 top-1 生成 `trigger=REVIEW` 提案并立即应用：actor=SYSTEM（`SYSTEM:fleet.review_dispatch` 为系统动作身份），裁决记录的 `authority_source` 取 08 `Check` 返回的 `POLICY:review_dispatch`（本模块启动时 `SystemActions.Register("fleet.review_dispatch","work_item_assign")`，08 S1 据此放行；同时向 08 `PolicyRegistry` 贡献 `review_dispatch` 规则供审计说明），调 02 `DispatchAssigner.Assign(role=REVIEWER, lease_ttl_seconds=1800)`（02 对 NEEDS_HUMAN_REVIEW 上的 REVIEWER 分派为**非迁移**操作，租约 1800 s 覆盖评审时限，不被 `lease_reclaim` 提前回收），再经 04 通道发 `agent.review{command_id, agent_id, work_item_id, claim_id, deadline_at=now+30min}`，宿主回 `agent.review_ack`。30 min 内未收到裁决 → 同事务：02 `Release(REVIEWER)` + 02 `WorkItemEscalator.Escalate(tx, {work_item_id, kind:REVIEW_SLA, detail:{reviewer_kind:AGENT, agent_id, attempt}})`（由 02 发布 `work_item.escalated`），并重新评估；无合格评审 Agent，或同一工作项连续 2 次超时 → 停止派评审，由 08 规则 `NO_ELIGIBLE_AUTHORITY` 升级给人（N-05-12）。HYBRID 项目不派评审（由「我的裁决」承担）。评审派单本身不授予任何裁决权：评审 Agent 的裁决仍由 02 + 08 `AuthorityChecker` 按委托条件判定。
- **BR-05-54 唤醒协调员**：消费 01 `intake.received` / `intake.triage_requested`：若开口配置了 `triage_agent_id` 且该 Agent ACTIVE·RUNNING，经 04 通道发 `agent.triage{command_id, agent_id, intake_id, deadline_at=now+15min}`；Agent 以自身凭证调 01 分诊卡接口提交草案。Agent 未运行或超时不重试，由 01 的 30 min 看门狗与通知交给人。
- **BR-05-55 阶段规则**：只在 07 项目阶段 ∈ {ACTIVE, ACCEPTANCE_PENDING} 时自动生成提案（READY / REDISPATCH / REVIEW / ENSEMBLE；ACCEPTANCE_PENDING 覆盖甲方部分验收后的返工与评审，与 07 BR-07-12 一致）；DISCOVERY / PLANNING 阶段只允许人通过 D02 发起 `MANUAL_REQUEST`，其确认若被 02 以 120420（阶段不允许）拒绝则原样返回。`project.stage_changed{to:ACTIVE}` 立即触发一次派单循环。
- **BR-05-56 起草发布说明**：消费 07 `delivery.release_notes_requested{delivery_id, drafter_agent_id?, note?}`：若 `drafter_agent_id` 指定的起草 Agent 为 ACTIVE·RUNNING，经 04 通道发 `agent.release_notes{command_id, agent_id, delivery_id, note, deadline_at=now+30min}`；宿主让该 Agent 读取交付包内容并以**自身凭证**调用 07 现有发布说明接口（07 #36）提交草稿（07 校验起草人与交付包项目）。起草 Agent 不存在 / 未运行 → 立即调用 07 `ReleaseNotesDrafting.ReportUnavailable(ctx, tx, {tenant_id, delivery_id, reason: NO_DRAFTER / DRAFTER_NOT_RUNNING})`；`deadline_at` 到期仍未提交 → 调 `ReportUnavailable(reason:TIMEOUT)`。兜底（直接发出确认请求、要求人工编写）由 07 BR-07-30b 负责，本模块不重试。

### 5.6 Heavy 并行求解（v1.1）

- **BR-05-60 触发**：(a) 工作项 risk=HIGH 且项目 `ensemble_max_branches ≥2` → 主导 Agent 在派单时改为生成 ensemble 计划；(b) 工作项 `uncertainty_flag=true`（02 字段，由人、协调员或主导 Agent 草拟设置）；(c) 人在工作项详情点〔并行求解〕。合格候选 <2 → 422 143009，回退单派。
- **BR-05-61 选枝**：N = min(请求值或 07 `ensemble_max_branches`, 合格数)，默认 3；从评分排序中贪心：先取第 1 名，之后依次取 `(runtime_kind, model_id)` 与已选**都不同**的最高分者；不足 N 时再按分数补入不同 Agent（模型可重复）；同一 Agent 不得占两枝；主导 Agent 不得为枝执行者（E10 恒生效）。
- **BR-05-62 预算**：`budget_cap_micros` 缺省 = N × 团队同类型 CPA 中位数 × 1.5，无数据时 N × 5,000,000（$5/枝，**本规格提议**）；不得超过 07 项目剩余预算（超出 422 143011）。需人批准的计划进入 PENDING_APPROVAL：08「我的裁决」生成 `ENSEMBLE_APPROVAL` 待办（由 `ensemble.plan_created{requires_approval:true}` 打开，`ensemble.started` / `ensemble.cancelled` 关闭），24 h 未批准自动取消（H11）。每枝上限 `branch_cap = floor(cap / N)`，作为 03 `max_cost_micros` 下发（03 COST_CAP 终止）。启动需人批准的情况：policy=MANUAL；或 cap > 默认值（还需 `project_manage`）；或风险 HIGH 且项目 HYBRID。累计 ≥80% → 通知 N-05-07；≥100% → H04。
- **BR-05-63 对比报告**（§6.5 Schema）：每枝列 Agent、runtime / 模型、终态与原因、证据覆盖（已映射验收标准数 / 总数）、必需验证 PASSED/FAILED/INCONCLUSIVE/PENDING、自动化测试 pass/fail/skip、diff 规模（文件数、+/− 行）、成本、时长、AI 辅助验证意见（标注「仅参考」）、主导 Agent 理由。**系统排序**：先要求必需 AUTOMATED 验证全 PASSED 的枝（其余排后），再按覆盖率高 → 失败测试少 → 成本低 → diff 行数少 → 枝 id 小。报告不宣称「正确」，只陈述证据。
- **BR-05-64 择优**：AUTO 且风险非 HIGH 且主导 Agent 选择 = 系统第一 → 自动 SELECTED（H07）；否则 SELECTION_PENDING 等人。合成（synthesize）只能由人发起，指定主枝 + 被合成枝（≤N−1），由主枝 Agent 执行新 ENSEMBLE 执行，预算从剩余 cap 扣，剩余 <10% → 422 143011。
- **BR-05-65 落选枝**：`outcome=NOT_SELECTED`、`archived_at=now`；执行、产物、证据全部保留（I5），在执行分支树以并列紫色枝灰显，可随时查看；不删除、不复用为声明。
- **BR-05-66 仍走正常裁决**：各枝执行结束时 03 不提交声明，只把声明草稿存为该执行的 `pending_claim`（03 E06 的 ENSEMBLE 分支）并对该枝排队验证（供对比报告）；选中后由本模块以选中枝 Agent 身份经 02 `ClaimSubmitter.SubmitOn(tx, {work_item_id, execution_id, claimed_by=枝 Agent, authority_source=POLICY:ensemble_branch, detail.ensemble_plan_id=<plan_id>, pending_claim（含 `head_commit`）})` 提交该枝的声明（枝内已采集的证据随 03 `EvidenceBinder` 绑定）→ AGENT_CLAIMED_DONE → 验证 → NEEDS_HUMAN_REVIEW → 有权主体裁决。择优本身不是 Decision，不改变 L2/L3。

### 5.7 职责分离与可观测

- **BR-05-70 主导 Agent 不裁决自己派出的工作**：本模块提供 `DispatchProvenance.DispatchedBy(work_item_id) []principal_id`（含 AUTO 应用时的主导 Agent、ensemble 计划发起者）；其唯一使用者是 02：02 构造 08 `AuthorityChecker` 请求时用它填入 `WorkItemFacts.DispatchedByActor`。08 对 APPROVE / REJECT / REQUEST_CHANGES / merge 判定时：主体 ∈ DispatchedBy 且无满足 PRD §8.3 条件（必需验证全 PASSED、非自产、返工 <3、风险在委托范围）的有效委托 → 拒绝（`reason_code=SEPARATION_OF_DUTIES`，`detail=LEAD_DISPATCHED`；02 返回 403 120403）。主导 Agent 在任何情况下不得作为自己所派工作项的执行者（AUTO 由 E10；MANUAL 由 G7 强制人确认）。
- **BR-05-71 可观测**：每次规划（含 LLM 输入摘要 hash、K 名单、耗时、成本）、提案、应用 / 改派 / 驳回 / 过期、ensemble 启动 / 对比 / 选择，都在项目默认频道写 EVENT（`fold_key=dispatch:<work_item_id>` 或 `ensemble:<plan_id>`）或卡片，并审计（§7.3）。「为什么派给它」= 提案 id → 候选明细 → 输入快照，永远可复算。

### 5.8 频道（collab）

- **BR-05-80 频道命名与上限**：CUSTOM 名 1–40，`^[\p{L}\p{N}][\p{L}\p{N}_-]{0,39}$`，同项目活跃频道唯一（422 145001）；每项目 CUSTOM ≤50（422 145002）；每频道成员 ≤200（422 145012）。
- **BR-05-81 默认频道**：07 在建项目事务内调 `ChannelProvisioner.CreateDefaultOn(ctx, tx, {tenant_id, project_id, name, member_principal_ids})`，失败使项目创建整体回滚（07 AC-07-02）。之后的成员变化由底座角色绑定决定：凡对项目有 `channel_read` 的人类与持 `agent_*` 绑定的 Agent 即默认频道成员；成员行 `source=DERIVED` 在首次读写时惰性补建，并由 reconcile 任务每 10 min 增删（失去项目访问 → `left_at`）。默认频道不可退出、不可移除成员（409 145011）。Agent 加入项目时同时加入其 `default_channel_names` 中存在的自建频道（`source=EXPLICIT`）。
- **BR-05-82 权限**：读频道 = `channel_read`（项目）且（DEFAULT 或为成员 或 持 `channel_manage`）；发消息 = `channel_post` 且为成员；建 / 改名 / 归档 / 管成员 = `channel_manage` 或该频道 OWNER（创建者）。不满足：缺权限码 403 145403（`reason_code=MISSING_PERMISSION`，`data.missing_permission`）；有权限但非成员 403 145403（`reason_code=NOT_MEMBER`）；他项目 / 他租户频道 404 145404。
- **BR-05-83 消息种类**：TEXT 由人或 Agent 发；EVENT / DECISION_CARD / DISPATCH_CARD 只由系统（collab 投影器）写，HTTP 发送 kind≠TEXT → 422 145017。DECISION_CARD 投影 02 的「待裁决」（NEEDS_HUMAN_REVIEW），按钮调用 02 D1（权限仍由 02 判定，频道只是入口）；拒绝必须填写结构化反馈（复用 `StructuredFeedbackForm`）。
- **BR-05-84 @mention**：客户端拾取器插入令牌 `<@principal_id>`；服务端解析令牌（不做显示名模糊匹配）：每个被提及者须为该频道当前成员（DEFAULT 频道按 BR-05-81 判定），否则 422 145003（`data.not_members[]`，UI 提示「X 不在本频道」，频道 OWNER / `channel_manage` 可〔邀请并发送〕）；已退役 Agent → 422 145016；每条 ≤20 个提及（422 145004）；不支持 `@all`/`@channel`（**本规格提议**：防通知风暴）。runtime 进程、机器、甲方联系人不是 Principal 或不是成员，无法被提及（PRD §5.3）。
- **BR-05-85 Agent 收到提及**：collab 写 `collab_message_mentions` → outbox `message.posted`；fleet 消费后经 04 通道 `agent.inbox{channel_id, message_id, seq, excerpt≤2000}` 投递给 ACTIVE·RUNNING 的 Agent（非运行 → 不投递，频道内系统 EVENT 提示「Ada 当前未运行，消息将在其启动后送达」，启动时补投最近 24 h 未投递项）。Agent 以自身凭证 `POST /channels/{id}/messages` 回复（`reply_to_message_id`）。
- **BR-05-86 「@Agent 请做 X」**：Agent 可做的唯一状态相关动作：(a) 以自身凭证创建 02 工作项草稿（需其角色允许，如 COORDINATOR）；(b) 若为主导 Agent，创建 `trigger=CHANNEL_REQUEST` 派单提案（`POST /entities/{p}/dispatch-proposals`，`requested_by`=原消息作者）。两者都产生 DISPATCH_CARD / 草稿卡片待人确认（BR-05-51）。执行者 Agent 被 @ 要求「开始做 W」→ 只能回复说明，不能自认领（02 BR-02-11）。
- **BR-05-87 事件折叠**（服务端提供 `fold_key`，客户端渲染）：同一频道中**连续**（中间无其他 kind 或其他 fold_key 消息）的 EVENT 且 fold_key 相同 → 折叠为一行（显示最新事件 + 「另 n 条」，可展开）；单组 ≤50 条，超出起新组；`attention=true` 的 EVENT（执行失败、Agent 崩溃 / 重启用尽、预算阈值、无合格候选）不折叠；DECISION_CARD **永不折叠**；DISPATCH_CARD 在 PROPOSED / SELECTION_PENDING 时不折叠，处理后可与同 fold_key 事件折叠。
- **BR-05-88 Agent 互 @ 防环**：由 Agent 消息中的提及触发投递时，新消息 `trigger_depth = 父 + 1`；`trigger_depth ≥3` 的消息中的 Agent 提及不再投递（消息照常保存，显示「已达 Agent 连锁上限」）；Agent 发消息速率 ≤10 条/min/频道，人 ≤30 条/min/频道，超出 429 145010（`Retry-After`）。
- **BR-05-89 编辑 / 撤回**：作者可在发送后 15 min 内编辑 TEXT（≤10 次，旧版本入 `collab_message_revisions`）；作者 24 h 内可撤回自己的 TEXT，`channel_manage` 可随时撤回任何 TEXT；撤回后 API 返回 `body=null, retracted=true`，原文仅 `audit_read` 可经修订表查看；编辑新增的提及会投递 / 通知，删除的提及不撤销已发通知。系统消息不可编辑 / 撤回（409 145006）。所有编辑 / 撤回审计 `collab.message_edited` / `collab.message_retracted`（不含正文）。
- **BR-05-90 未读**：`unread = count(seq > last_read_seq AND kind <> 'EVENT' AND author_id <> me AND retracted_at IS NULL)`，显示上限 99+；`mention_unread` 来自 `collab_message_mentions.read=false`。`PUT …/read-marker {seq}` 只增不减（小于当前值 → 200 不变，天然幂等），同时把 seq 及之前的提及置已读。
- **BR-05-91 Agent 私信（AGENT_DIRECT）**：只在同项目内两名 Agent 之间，由 Agent 以自身凭证 `POST /entities/{p}/channels {kind:AGENT_DIRECT, peer_agent_id}` 创建（已存在则 200 返回既有，幂等）；人类不能发送（403 145403，`reason_code=DIRECT_READ_ONLY`），持 `agent_read` + 该项目 `channel_read` 的人可只读查看；私信不产生派单 / 状态变更，同样受 BR-05-86 约束；任一方退役后频道只读。
- **BR-05-92 大小与附件**：正文 ≤8000 字符且 ≤32 KB UTF-8（413 145005）；附件 ≤5 个、单个 ≤25 MiB，经底座 `files` + `file_resource_bindings(purpose=channel_attachment, resource_kind=collab_message)`；payload ≤16 KB。
- **BR-05-93 保留**：MVP 消息不物理删除（00 §3）；项目 CLOSED / CANCELLED 后频道归档只读；保留期策略（如 365 天后清理正文、保留元数据）列 v2 待决（§12）。
- **BR-05-94 幂等与序号**：发消息支持 `Idempotency-Key`（09 C7：重放返回首条；同 key 不同请求 176425，进行中 176409）；序号在频道行锁内 `last_seq+1` 分配，与消息插入同事务；事件投影以 `(channel_id, source_event_id)` 唯一去重。
- **BR-05-95 钩子（v1.1）**：OUTGOING_WEBHOOK：消息写入后经 09 JobQueue 推送 `{channel_id, message_id, seq, kind, author{kind,id}, mentions[], body?}`，HMAC-SHA256 签名头；超时 5 s，重试 5 次指数退避；连续 20 次失败自动禁用并通知 OWNER。钩子不能触发状态变更（入站为 v2 桥接）。
- **BR-05-96 桥接（v2）**：`transport ∈ {SLACK, WECOM, TELEGRAM, MQTT, WEBSOCKET, HTTP}` 绑定到单一频道，内容为该频道日志投影（B §9.1）；不作为业务状态真相源（评审 P1）。MVP 不建表。

---

## 6. 接口契约

约定（与 02/03/04/07 一致）：huma v2 + `respx` 信封；ID 十进制字符串；OpenAPI tag = 最终路径首段（CR-03-07 / CR-04-02）；守卫记号 **T** = `authz.Register`（租户级，路径前缀 `/tenants/{tenant_id}`）、**E** = `authz.RegisterEntity`（`entity_id` = 项目实体）、**O** = `authz.RegisterOwned`（全局短地址，按资源反查所属项目 / 租户后授权；作用域地址为同一操作，归属不符 404）；乐观锁 = 响应头 `ETag: "<version>"`、写请求 `If-Match`（缺 428、不符 409，表中记「v」）；「✔」= 支持 `Idempotency-Key`（09 C7 平台辅助，24 h；同 key 不同请求 176425，进行中 176409）。权限拒绝统一 `<段>403` + `data.reason_code`（MISSING_PERMISSION / NOT_MEMBER / DIRECT_READ_ONLY / REAUTH_REQUIRED / SELF_MANAGE / HUMAN_ONLY 等）。列表一律 `pkg/query`（`field=op:value`，offset 分页，`capabilities` 声明）。fleet 通用码沿用 04：140400 / 140403 / 140404 / 140409 / 140428；collab：145400 / 145403 / 145404 / 145409 / 145428。

### 6.1 Agent（fleet）

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| F01 | GET `/tenants/{tenant_id}/agents` | tenants | `agent_read` · T | 过滤 `state`(eq,in) `role`(eq) `machine_id`(eq) `runtime_kind`(eq) `model_id`(eq) `project_id`(eq，经角色绑定) `name`(contains) `process_status`(eq)；排序 `name` `created_at` `state` `fpa_30d` `load_ratio` | `Agent[]`（摘要 + `stats_30d` + `load`） | — | — |
| F02 | POST `/tenants/{tenant_id}/agents` | tenants | `agent_manage` · T；HUMAN；FULL_AUTO 另需 step-up | `name*` `handle?` `persona?` `avatar_style?` `avatar_seed?` `system_prompt*` `machine_id*` `runtime_kind*` `model_id*` `reasoning_effort?` `authorization_mode?` `max_context_tokens?` `roles*[]` `skill_ids?[]≤20` `mcp_allowlist?[]` `capability_tags?[]` `default_channel_names?[]` `memory_policy?` `max_parallel_items?` `max_risk?` `auto_restart?` `start?`(默认 true) `reauth_proof?` | `Agent`（201，DRAFT，`credential_delivery=PENDING`） | 422 142001/03/04/05/07/09/22/26；409 142002；503 142030；403 140403（含 REAUTH_REQUIRED） | ✔ |
| F03 | GET `/agents/{agent_id}` | agents | `agent_read` · O | `?include=stats,load,roles,skills` | `Agent` 全量；ETag | 404 140404 | — |
| F04 | PATCH `/agents/{agent_id}` | agents | `agent_manage` · O；HUMAN | 可选 `name` `persona` `avatar_style` `avatar_seed` `system_prompt` `reasoning_effort` `authorization_mode` `max_context_tokens` `roles[]` `mcp_allowlist[]` `capability_tags[]` `default_channel_names[]` `memory_policy` `max_parallel_items` `max_risk` `auto_restart` `reauth_proof?` | `Agent` | 409 142010/140409；422 同 F02；428 140428 | v |
| F05 | PUT `/agents/{agent_id}/run-state` | agents | `agent_manage` · O | `{desired:"ACTIVE"\|"STOPPED"}` | `Agent`（202，含 process_status） | 409 142002/142008/142010/142011；422 142003 | 天然幂等 |
| F06 | POST `/agents/{agent_id}/restarts` | agents | `agent_manage` · O | `{reason?≤2000}` | `{agent, process_event_id}`（202） | 409 142002/142010/142011 | ✔ |
| F07 | PUT `/agents/{agent_id}/machine-binding` | agents | `agent_manage` · O；HUMAN | `{machine_id*, runtime_kind*, model_id*, reasoning_effort?}` | `Agent`（`credential_delivery=PENDING`） | 409 142010/142012；422 142003/04/07/13/26 | v |
| F08 | POST `/agents/{agent_id}/credential-deliveries` | agents | `agent_manage` · O | — | `{credential_delivery}`（202） | 409 142002/142010 | ✔；30 s 内合并 |
| F09 | GET `/agents/{agent_id}/process-events` | agents | `agent_read` · O | 过滤 `kind`(in) `occurred_at`(gte,lte)；排序 `occurred_at` | `ProcessEvent[]` | 404 | — |
| F10 | GET `/agents/{agent_id}/stats` | agents | `agent_read` · O | `?window_days=30\|90&work_item_type=` | `{fpa, fpa_n, cpa_micros, rework_rate, claims, accepted_items, load{active_items, max_parallel_items, running_executions}, computed_at, by_type[]}` | 404 | — |
| F11 | GET `/agents/{agent_id}/avatar-candidates` | agents | `agent_read` · O | `?style=` | `[{style, seed, url}] ×6` | 404 | — |
| F12 | GET `/agents/{agent_id}/workspace-entries` | agents | `agent_read`+`machine_read` · O | `?path=`（相对根） | `{root, path, entries:[{name, kind, size, modified_at}] ≤1000, truncated}` | 409 142002；422 142027 | — |
| F13 | GET `/agents/{agent_id}/workspace-entries/content` | agents | 同上 | `?path=` | 文本 / 二进制流（≤1 MiB，`Cache-Control: no-store`） | 413 142028；422 142027 | — |
| F14 | GET `/agents/{agent_id}/direct-channels` | agents | `agent_read` · O + 每频道按项目 `channel_read` 行过滤 | 过滤 `project_id`(eq) | `Channel[]`（AGENT_DIRECT） | — | — |
| F15 | GET `/tenants/{tenant_id}/roster-entries` | tenants | 租户成员 · T；Agent 行另需 `agent_read` | 过滤 `kind`(eq HUMAN\|AGENT) `role`(eq) `state`(in) `project_id`(eq) `q`(contains)；排序 `name` `load_ratio` `kind` | `RosterEntry[]`：`{kind, principal_id, agent_id?, name, avatar, roles[], state, load{active_items, max?}, current_work_items[≤3], last_active_at}` | — | — |
| F16 | GET `/agents/{agent_id}/runtime-config` | agents | 调用者 = 该 Agent 服务账号（宿主进程）· RegisterPrincipalAuthenticated + 自身校验 | — | `{config_revision, system_prompt, skills[{skill_key, version_no, content}], mcp_allowlist, …}` | 403 140403 | — |

F15 为人 + Agent 统一投影（**本规格提议**；资源语义卡：Meaning = 团队名册条目；Owner = 租户；Identity = principal_id；只读投影；人类数据经底座成员读能力、负载经 02 读接口）；无 `agent_read` 时只返回 HUMAN 行。

### 6.2 退役

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| R01 | POST `/agents/{agent_id}/retirements` | agents | `agent_retire` · O；HUMAN；FORCED 另需 step-up | `{mode*:"NORMAL"\|"FORCED", reason*≥10, confirm_name?(FORCED 必填), reauth_proof?(FORCED 必填)}` + If-Match（Agent 版本） | NORMAL：`Retirement`（201，step=HANDOVER，含 precheck）；FORCED：`{retirement, agent, lost:{open_items, running_executions}}`（201） | 409 142010/142014；422 142016/142024；403 140403（REAUTH_REQUIRED） | ✔；v |
| R02 | GET `/agent-retirements/{retirement_id}` | agent-retirements | `agent_read` · O | — | `Retirement`（含 handover 链接、reassign_result） | 404 | — |
| R03 | PATCH `/agent-retirements/{retirement_id}` | agent-retirements | `agent_retire` · O；HUMAN | 按步骤：`{successor_kind, successor_principal_id?}`（SUCCESSOR）；`{advance:true}`（推进 REASSIGN→…→DONE，异步）；`{handover_retry:true}`（仅一次）；`{state:"CANCELLED"}` | `Retirement`（推进时 202） | 409 142015/140409；422 142018/142021 | v |

### 6.3 派单提案与候选

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| D01 | GET `/entities/{entity_id}/dispatch-proposals` | entities | `work_item_read` · E | 过滤 `state`(eq,in) `result`(eq) `work_item_id`(eq) `trigger`(in) `requires_human`(eq) `created_at`(gte)；排序 `created_at` `expires_at` | `DispatchProposal[]`（含推荐人、top-3） | — | — |
| D02 | POST `/entities/{entity_id}/dispatch-proposals` | entities | 人：`work_item_assign` · E；Agent：须为本项目 `lead_agent_id` 且 `trigger=CHANNEL_REQUEST` | `{work_item_id*, trigger?:"MANUAL_REQUEST"\|"CHANNEL_REQUEST", source_message_id?, requested_by?}` | `DispatchProposal`（201；已有 PROPOSED → 200 返回既有） | 422 143007；409 143015；403 140403 | ✔ |
| D03 | GET `/dispatch-proposals/{proposal_id}` | dispatch-proposals | `work_item_read` · O | — | `DispatchProposal` + 全部 `candidates[]`（不合格码与分量）+ `inputs_digest`；ETag | 404 | — |
| D04 | PUT `/dispatch-proposals/{proposal_id}/resolution` | dispatch-proposals | `work_item_assign` · O；HUMAN | `{kind*:"CONFIRM"\|"OVERRIDE"\|"DISMISS", assignee_principal_id?(OVERRIDE 必填), reason?(OVERRIDE/DISMISS 必填 ≥5)}` | `{proposal, assignment?, decision?}` | 409 143004/143005/140409；409 120426/120420（02 `DispatchAssigner` 原样返回）；422 143006/120444；403 120403 | v；✔ |
| D05 | GET `/work-items/{work_item_id}/dispatch-candidates` | work-items | `work_item_read`+`agent_read` · O | `?role=EXECUTOR` | 即时评分预览（不落库）`{scoring_version, candidates[], inputs_digest}` | 404 | — |
| D06 | GET `/agents/{agent_id}/dispatch-history` | agents | `agent_read` · O + 项目行过滤 | 过滤 `state`(in) `created_at`(gte) | 该 Agent 作为候选 / 受派者的提案 | — | — |

`DispatchProposal`：`id, project_id, work_item_id, role, trigger, policy, lead_agent_id, result, recommended_agent_id, system_top_agent_id, rationale, rationale_source, requires_human, guard_reasons[], scoring_version, state, resolution_kind, resolved_by, resolved_assignee_principal_id, assignment_id, decision_id, expires_at, created_at, version`。

### 6.4 Heavy 并行求解（v1.1）

分工：03 A07 `POST /work-items/{id}/ensembles` 只创建执行组；本模块的 **ensemble plan** 负责选枝、预算、对比报告与择优，内部调用 `ExecutionLauncher.Launch(ensemble_group_id = plan.id)`。UI 只用本节接口。

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| H01 | POST `/work-items/{work_item_id}/ensemble-plans` | work-items | 人：`work_item_assign`+`execution_start` · O；主导 Agent：经 08 `AuthorityChecker`（`POLICY:ensemble_branch`，不经角色码） | `{trigger*, branch_count?, agent_ids?[]（须合格）, budget_cap_micros?, deadline_hours?(1–24), reason*≤2000}` | `EnsemblePlan`（201；PENDING_APPROVAL 或 RUNNING） | 422 143009/143010/143011；409 143016/130426 | ✔ |
| H02 | GET `/ensemble-plans/{plan_id}` | ensemble-plans | `work_item_read` · O | — | `EnsemblePlan` + `branches[]`；ETag | 404 | — |
| H03 | PUT `/ensemble-plans/{plan_id}/approval` | ensemble-plans | `work_item_assign` · O；HUMAN（cap 超默认另需 `project_manage`） | `{note?}` | `EnsemblePlan`（RUNNING） | 409 143013/140409；403 140403 | v |
| H04 | GET `/ensemble-plans/{plan_id}/comparison-report` | ensemble-plans | `work_item_read`+`execution_read` · O | — | `ComparisonReport`（§6.5） | 409 143012 | — |
| H05 | PUT `/ensemble-plans/{plan_id}/selection` | ensemble-plans | `work_item_assign` · O；HUMAN | `{branch_id}` 或 `{synthesize:{primary_branch_id, merge_branch_ids[]}}`；`reason*≥10` | `EnsemblePlan` | 409 143012/143013；422 143014/143011 | v |
| H06 | PATCH `/ensemble-plans/{plan_id}` | ensemble-plans | `work_item_assign` · O；HUMAN | `{state:"CANCELLED", reason*}` | `EnsemblePlan` | 409 143013 | v |

### 6.5 ComparisonReport Schema

```json
{
  "plan_id": "…", "generated_at": "…", "scoring_version": "c1", "criteria_total": 8,
  "branches": [{
    "branch_id": "…", "execution_id": "…", "agent": {"id": "…", "name": "Ada"},
    "runtime_kind": "CLAUDE_CODE", "model_id": "…", "outcome": "SUBMITTED", "end_reason": "CLAIM_SUBMITTED",
    "evidence_coverage": {"covered": 7, "total": 8},
    "required_verifications": {"passed": 3, "failed": 0, "inconclusive": 0, "pending": 0, "required_total": 3},
    "tests": {"passed": 128, "failed": 0, "skipped": 2},
    "diff": {"files_changed": 9, "lines_added": 310, "lines_removed": 42},
    "cost_micros": 1840000, "duration_ms": 2520000,
    "ai_assisted_notes": [{"verifier": "…", "result": "PASSED", "summary": "…", "reference_only": true}],
    "system_rank": 1
  }],
  "system_pick_branch_id": "…",
  "lead": {"agent_id": "…", "pick_branch_id": "…", "rationale": "≤2000", "source": "LEAD|SYSTEM"},
  "limitations": ["AI 辅助意见仅供参考，不构成验收"]
}
```

### 6.6 机器通道上的 Agent 消息（**唯一定义**；04 只承载传输帧：C2 `ServerBatch.agent_commands[]`、poll 请求 `agents[]` 快照、C3 上行消息；未 ACK 每 10 s 重发 ≤6 次）

| 方向 | 类型 | 载荷 | 处理 |
|---|---|---|---|
| S→M | `agent.provision` | `{command_id, agent_id, principal_id, client_id, client_secret, config_revision}` | 机器加密落盘；ACK → `credential_delivery=DELIVERED`；秘密只出现在此命令，重投 = 签发新凭证 |
| S→M | `agent.start` / `agent.stop` | `{command_id, agent_id, config_revision}` / `{command_id, agent_id, grace_ms:20000}` | 宿主以 Agent 凭证调 F16 取完整配置 |
| S→M | `agent.cleanup` | `{command_id, agent_id, scope:"AGENT_HOME"}` | 只删 Agent 私有目录；ACK `{removed_bytes}` |
| S→M | `agent.inbox` | `{command_id, agent_id, items:[{channel_id, message_id, seq, excerpt≤2000, trigger_depth}]}` | 宿主转交 Agent |
| S→M | `agent.lead_plan` | `{command_id, agent_id, proposal_id, candidates[≤5], work_item, criteria[], retrieval_refs[], deadline_at}` | 宿主调用模型，经 `agent.lead_plan_result` 回传 |
| S→M | `agent.workspace_list` / `agent.workspace_read` | `{request_id, agent_id, path, max_entries\|max_bytes}` | 15 s 超时；结果经 `agent.workspace_result` 回传 |
| S→M | `agent.review` | `{command_id, agent_id, work_item_id, claim_id, deadline_at}` | 宿主让评审 Agent 读取声明 / 证据 / 验证，并以自身凭证调 02 裁决接口（BR-05-53） |
| S→M | `agent.triage` | `{command_id, agent_id, intake_id, deadline_at}` | 宿主让协调员读取来件并调 01 分诊卡接口（BR-05-54） |
| S→M | `agent.release_notes` | `{command_id, agent_id, delivery_id, note, deadline_at}` | 宿主让起草 Agent 读取交付包并调 07 #36 提交发布说明草稿（BR-05-56） |
| M→S | `agent.status`（亦随 poll `agents[]`） | `{agent_id, status:"STARTING"\|"RUNNING"\|"STOPPING"\|"EXITED", pid?, exit_code?, config_revision, at}`（**唯一**进程状态枚举，04 不另定义） | A04 / A07 |
| M→S | `agent.lead_plan_result` | `{proposal_id, pick_agent_id, rationale, cost_micros}` | BR-05-42 校验 |
| M→S | `agent.command_ack` | `{command_id, ok, code?, detail?}` | provision / start / stop / cleanup / inbox / triage / release_notes 的 ACK；未 ACK 的命令按 04 通道规则每 10 s 重发，≤6 次 |
| M→S | `agent.review_ack` | `{command_id, agent_id, work_item_id, accepted:bool, reason?}` | `agent.review` 专用 ACK；`accepted=false`（如 Agent 繁忙）→ 立即按 BR-05-53 超时路径处理 |
| M→S | `agent.workspace_result` | `{request_id, agent_id, ok, entries?[{name, kind, size, mtime}], content_base64?, truncated?, code?}` | 回应 workspace_list / workspace_read（F12 / F13 同步等待 ≤15 s） |

### 6.7 频道（collab）

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| C01 | GET `/entities/{entity_id}/channels` | entities | `channel_read` · E | 过滤 `kind`(in) `archived`(eq，默认 false) `name`(contains) `member`(eq me)；排序 `last_message_at` `name` | `Channel[]`（含 `unread`、`mention_unread`；AGENT_DIRECT 需 `kind` 显式过滤） | — | — |
| C02 | POST `/entities/{entity_id}/channels` | entities | CUSTOM：`channel_post`（**本规格提议**：成员可自建）· E；AGENT_DIRECT：调用者须为 AGENT | CUSTOM `{kind, name*, topic?, member_principal_ids?[]≤200}`；DIRECT `{kind:"AGENT_DIRECT", peer_agent_id*}` | `Channel`（201；DIRECT 已存在 → 200） | 422 145001/145002/145012/145014；403 145403（DIRECT_READ_ONLY） | ✔ |
| C03 | GET `/channels/{channel_id}` | channels | `channel_read` · O + BR-05-82 | — | `Channel`；ETag | 404 145404；403 145403（NOT_MEMBER） | — |
| C04 | PATCH `/channels/{channel_id}` | channels | `channel_manage` 或 OWNER · O | `{name?, topic?, archived?:bool}` | `Channel` | 409 145011/145008/145409；422 145001 | v |
| C05 | GET `/channels/{channel_id}/members` | channels | 同 C03 | 过滤 `member_kind`(eq) `q`(contains) | `ChannelMember[]` | — | — |
| C06 | PUT `/channels/{channel_id}/members/{principal_id}` | channels | `channel_manage` 或 OWNER · O | `{role?:"MEMBER"\|"OWNER"}` | `ChannelMember` | 409 145011/145008；422 145012/145016；404 | 天然幂等 |
| C07 | DELETE `/channels/{channel_id}/members/{principal_id}` | channels | 同 C06；本人可退出 CUSTOM | — | 204 | 409 145011/145018 | 幂等 |
| C08 | GET `/channels/{channel_id}/messages` | channels | 同 C03 | 过滤 `seq`(lt,gt,lte,gte) `kind`(in) `fold_key`(eq) `ref_id`(eq) `author_id`(eq)；排序 `seq`（默认 desc）；`limit ≤100` | `Message[]` + `meta.max_seq` | — | — |
| C09 | POST `/channels/{channel_id}/messages` | channels | `channel_post` · O + 成员 | `{body*≤8000, reply_to_message_id?, attachment_file_ids?[]≤5}`（kind 恒为 TEXT） | `Message`（201） | 422 145003/145004/145016/145017；413 145005；409 145008；403 145403（NOT_MEMBER / DIRECT_READ_ONLY）；429 145010 | ✔ |
| C10 | PATCH `/messages/{message_id}` | messages | 作者本人 · O | `{body*}` | `Message` | 409 145006/145409 | v |
| C11 | DELETE `/messages/{message_id}` | messages | 作者（24 h 内）或 `channel_manage` · O | — | 204（撤回，不物理删除） | 409 145006 | 幂等 |
| C12 | GET `/messages/{message_id}/revisions` | messages | `audit_read` · O | — | `Revision[]` | 403 | — |
| C13 | PUT `/channels/{channel_id}/members/{principal_id}/read-marker` | channels | 本人 · O | `{seq*}` | `{last_read_seq, unread, mention_unread}` | 404 | 天然幂等（只增） |
| C14 | GET `/principals/{principal_id}/channel-unreads` | principals | 本人 · RegisterPrincipalTenantMember | `?project_id=` | `[{channel_id, project_id, unread, mention_unread}]` + 合计 | 403 | — |
| C15 | GET `/channels/{channel_id}/mention-candidates` | channels | `channel_post` · O | `?q=&limit≤20` | `[{principal_id, kind, name, handle, avatar, state}]`（仅成员，不含退役） | — | — |
| C16 | GET/POST `/channels/{channel_id}/hooks`；PATCH `/channel-hooks/{hook_id}` | channels / channel-hooks | `channel_manage` · O（v1.1） | `{kind, target_url*, filter, enabled}` | `ChannelHook`（秘密仅创建时返回一次） | 422 145019 | ✔ / v |

`Message`：`id, channel_id, seq, kind, author{kind, id, name, avatar, retired?}, body?, mentions[{principal_id, kind}], payload?, fold_key?, attention, ref{kind,id}?, reply_to_message_id?, trigger_depth, edited_at?, edit_count, retracted, created_at, version`。

**payload Schema（按 kind）**：
- EVENT：`{event_type, title≤200, summary≤500, subject{kind,id}, actor{kind,id,authority_source}, links[{kind,id}], severity:"INFO"|"ATTENTION"}`。
- DECISION_CARD：`{work_item_id, title, completion_level:"L1"|"L2", verification{passed, required_total}, evidence_coverage{covered,total}, status:"PENDING"|"DECIDED", decision?{action, actor, at}}`。
- DISPATCH_CARD：`{card_kind:"PROPOSAL"|"ENSEMBLE_COMPARISON", proposal_id?, plan_id?, work_item_id, recommended{agent_id, name, score}?, top[{agent_id, score}]≤3, rationale_excerpt≤500, rationale_source, requires_human, guard_reasons[], status:"PROPOSED"|"APPLIED"|"OVERRIDDEN"|"EXPIRED"|"SELECTION_PENDING"|"SELECTED"}`。
- TEXT：payload 为空或 `{attachments:[{file_id, name, size, mime}]}`。

### 6.8 跨模块接口

**本模块提供**（Go 定义在 `internal/fleet`、`internal/collab` 根包）：

| 接口 | 方法 | 使用者 |
|---|---|---|
| `AgentDirectory`（00 §8） | `Get`；`ByPrincipal(principal_id)`；`IsAgent(principal_id)`；`Roles(agent_id)`；`Load(agent_id)`；`Stats(agent_id, type, window)`；`RuntimeProfile(agent_id) {system_prompt, runtime, model, effort, authorization_mode, max_context_tokens, mcp_allowlist}`；`ListByProject(project_id)` | 02、03、04、07、08、collab |
| `DispatchProvenance`（00 §8） | `DispatchedBy(ctx, tx, work_item_id) ([]principal_id, error)` | 02（填入 08 `WorkItemFacts.DispatchedByActor`） |
| `ChannelProvisioner`（collab，新增，CCR-05-05） | `CreateDefaultOn(ctx, tx, {tenant_id, project_id, name, member_principal_ids}) (channel_id, error)`；`RenameDefaultOn(ctx, tx, project_id, name)`；`ArchiveProjectChannelsOn(ctx, tx, project_id)` | 07 |
| `ChannelPoster`（collab，新增） | `PostSystemOn(ctx, tx, {project_id, channel_id?, kind, payload, fold_key, attention, ref, source_event_id})`（缺省为默认频道） | fleet（DISPATCH_CARD、ensemble 对比卡、Agent 崩溃 / 不可用提示，在 fleet 事务内同步写入）；其他模块的频道呈现经领域事件由 `collab.event_projector` 投影，不直接调用 |
| 向 08 `PolicyRegistry` 贡献的规则 | `dispatch_auto`（主导 Agent 在 AUTO 策略下 `work_item_assign`；条件：BR-05-44 护栏未命中且 P02 守卫通过）；`ensemble_branch`（主导 Agent 对 ENSEMBLE 枝 `execution_start`；条件：BR-05-60–62）；`review_dispatch`（系统在 AUTONOMOUS 下的 REVIEWER 分派；条件：BR-05-53） | 08 |
| 向 08 `SystemActions` 登记 | `fleet.review_dispatch` → `work_item_assign`（BR-05-53 的 SYSTEM 动作；未登记则 08 S1 返回 `SYSTEM_NOT_REGISTERED`） | 08 |

**本模块使用**：02 `WorkItemReader`、`DispatchAssigner{Assign, Reassign, Release}` 与 `ClaimSubmitter.SubmitOn`（CCR-05-04）；03 `ExecutionLauncher{Launch, CancelExecution}`、`EvidenceReader.VerificationSummary`、执行对比数据（A08 同源服务方法）、HANDOVER Artifact 写入；04 `MachineDirectory`、通道命令扩展；06 `SkillCatalog.ForAgent`、`SkillMounter.MountOn`、`Retriever.Search(purpose=LEAD)`、`MemoryPolicyValidator`（CCR-05-07）；07 `ProjectReader`、`ProjectGate.CanAutoDispatch`、项目档案 #4；08 `AuthorityChecker`（含 `ListEligible`）、`DelegationRevoker`、`PolicyRegistry`；02 `WorkItemTransitioner.EnsembleFailed`；底座 identity `ServiceAccountProvisioner`（CR-04-08）与 `PrincipalAvatarSetter`（新增），core `EntityRoleBindingStore{PutOn, RemoveOn, ListByEntity}`，authn `ReauthenticationConsumer`，`audit.Appender`，`dlock`；09 `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`、`Notifier`、`SecretSealer`。

### 6.9 错误码

fleet 子段（**本规格提议**，CCR-05-03）：04 用 140000–141999，本规格 142000–143999；i18n 命名空间 `fleet`。collab 145000–149999，命名空间 `collab`。三语 en-US / zh-CN / ms-MY 齐全。

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 142001 | 422 | fleet.agent.handle_taken | handle / 名称冲突 |
| 142002 | 409 | fleet.agent.machine_not_online | 绑定机器非 ONLINE（或 DRAINING） |
| 142003 | 422 | fleet.agent.runtime_unavailable | runtime 不存在或 MISSING |
| 142004 | 422 | fleet.agent.model_not_listed | 模型不在目录 |
| 142005 | 422 | fleet.agent.roles_invalid | 角色集合非法 |
| 142007 | 422 | fleet.agent.mcp_not_detected | MCP 不在机器能力中 |
| 142008 | 409 | fleet.agent.credential_not_delivered | 凭证未送达 |
| 142009 | 422 | fleet.agent.limit_reached | Agent 数量上限 |
| 142010 | 409 | fleet.agent.illegal_transition | 非法状态迁移 |
| 142011 | 409 | fleet.agent.transition_in_progress | 迁移进行中 |
| 142012 | 409 | fleet.agent.has_active_executions | 存在非终态执行 |
| 142013 | 422 | fleet.agent.rebind_same_target | 换绑目标相同 |
| 142014 | 409 | fleet.retirement.in_progress | 已有进行中的退役 |
| 142015 | 409 | fleet.retirement.illegal_step | 步骤顺序错误 / 已终态 |
| 142016 | 422 | fleet.retirement.name_mismatch | 强制退役名称不符 |
| 142018 | 422 | fleet.retirement.successor_invalid | 接手人不合格 |
| 142021 | 422 | fleet.retirement.handover_retry_exhausted | 交接已重试过 |
| 142022 | 413 | fleet.agent.prompt_too_large | 系统提示词超 32 KB |
| 142024 | 422 | fleet.retirement.machine_unhealthy | 机器不健康，只能强制退役 |
| 142026 | 422 | fleet.agent.effort_unsupported | 推理强度不支持 |
| 142027 | 422 | fleet.agent.workspace_path_invalid | 路径越界 |
| 142028 | 413 | fleet.agent.workspace_file_too_large | 文件超 1 MiB |
| 142030 | 503 | fleet.agent.provisioning_unavailable | 跨模块能力缺失 |
| 143004 | 409 | fleet.dispatch.proposal_not_open | 提案非 PROPOSED |
| 143005 | 409 | fleet.dispatch.proposal_stale | 应用时候选已不合格 |
| 143006 | 422 | fleet.dispatch.override_invalid | 改派对象不合格 |
| 143007 | 422 | fleet.dispatch.work_item_not_ready | 工作项不可派 |
| 143009 | 422 | fleet.ensemble.not_enough_candidates | 合格候选 <2 |
| 143010 | 422 | fleet.ensemble.branch_limit | 枝数越界 |
| 143011 | 422 | fleet.ensemble.budget_exceeded | 预算超出 / 剩余不足 |
| 143012 | 409 | fleet.ensemble.not_ready | 报告 / 选择时机不对 |
| 143013 | 409 | fleet.ensemble.terminal | 计划已终态 |
| 143014 | 422 | fleet.ensemble.selection_invalid | 选中枝无可提交声明 |
| 143015 | 409 | fleet.dispatch.thrash_limit | 派单次数达上限 |
| 143016 | 409 | fleet.ensemble.active_exists | 已有活动计划 |
| 145001 | 422 | collab.channel.name_taken | 频道名冲突 / 非法 |
| 145002 | 422 | collab.channel.limit | 自建频道上限 |
| 145003 | 422 | collab.mention.not_member | 被提及者不是成员 |
| 145004 | 422 | collab.mention.limit | 提及数超限 |
| 145005 | 413 | collab.message.too_large | 正文 / 附件超限 |
| 145006 | 409 | collab.message.not_editable | 不可编辑 / 撤回 |
| 145008 | 409 | collab.channel.archived | 频道已归档 |
| 145010 | 429 | collab.rate_limited | 发送过快 |
| 145011 | 409 | collab.channel.immutable | 默认 / 私信频道不可此操作 |
| 145012 | 422 | collab.channel.member_limit | 成员上限 |
| 145014 | 422 | collab.direct.peer_invalid | 私信对象非同项目 Agent |
| 145016 | 422 | collab.member.retired | 对象已退役 |
| 145017 | 422 | collab.message.kind_forbidden | 只能发送 TEXT |
| 145018 | 409 | collab.channel.last_owner | 不能移除最后一个 OWNER |
| 145019 | 422 | collab.hook.target_invalid | 钩子目标非 https 或为内网地址 |

权限拒绝不另设码：fleet 用 140403、collab 用 145403，原因放 `data.reason_code`（REAUTH_REQUIRED、NOT_MEMBER、DIRECT_READ_ONLY、MISSING_PERMISSION、SELF_MANAGE、HUMAN_ONLY）。幂等冲突用平台码 176425 / 176409（09 C7）。

---

## 7. 后台任务与事件

### 7.1 任务（经 09 `JobQueue`；周期互斥用 `internal/infra/dlock`，锁名 `fleet:<job>` / `collab:<job>`，`maxHold` = 周期 ×2）

| 任务 | 触发 / 频率 | 互斥 | 处理 | 重试 / 失败 |
|---|---|---|---|---|
| `fleet.agent_crash_watchdog` | 每 15 s | dlock 全局 | 扫 `state='ACTIVE' AND process_status IN ('RUNNING','STARTING')`：机器 ONLINE 且 `last_seen_at < now−30s`（3 × 04 心跳 10 s）→ A07；STARTING 超 60 s → A06；每批 500，逐条 CAS（`version`） | 单条失败下轮重试；连续 5 轮失败告警 `fleet_watchdog_failures_total` |
| `fleet.agent_auto_restart` | A06/A07 后延迟任务（10 s / 30 s / 90 s） | 任务键 `agent:<id>:crash:<process_event_id>` 去重 | BR-05-12 判定 → A10 / A11 | 机器离线 → 推迟到 `machine.state_changed{ONLINE}` 再判（不消耗次数） |
| `fleet.agent_credential_delivery` | 创建 / 换绑 / F08 后 | 任务键 `agent:<id>:cred:<credential_id>` | BR-05-03 ⑦⑧ | 10 s/30 s/90 s/5 min/15 min，5 次后 FAILED + N-05-02 |
| `fleet.agent_pending_cleanup` | 消费 `machine.state_changed{ONLINE}` | 每机一次 | 对 `pending_cleanup_machine_id = 该机` 的 Agent 下发 `agent.cleanup` | 30 天未完成 → 清空字段，审计 `fleet.agent_cleanup_abandoned` |
| `fleet.retirement_runner` | R03 `advance` 后及每 30 s | 每退役 dlock `fleet:retirement:<id>` | 推进 R4→R7；R2 10 min、R5 5 min 超时处理 | R6 失败整体回滚，60 s 后重试，3 次失败 → state=FAILED，N-05-06 |
| `fleet.dispatch_cycle` | 消费 `work_item.created{state:READY}`、`work_item.state_changed{to:READY}`、`work_item.assigned`（释放）、`project.stage_changed{to:ACTIVE}`、`agent.state_changed`、`machine.state_changed`（去抖 30 s）；兜底每 5 min；只处理阶段 ACTIVE 的项目（BR-05-55） | dlock `fleet:dispatch:<project_id>`（maxHold 60 s） | BR-05-48 排序取 ≤10 项 → 生成提案 → AUTO 护栏 → 应用 | 主导 Agent 未回应走 BR-05-42 回退；单项失败不影响其他 |
| `fleet.lead_plan_timeout` | 每个 `agent.lead_plan` 发出后延迟 120 s | 任务键 `proposal:<id>` | 未收到结果 → 系统 top-1 | — |
| `fleet.dispatch_proposal_expiry` | 每 60 s | dlock 全局 | `state='PROPOSED' AND expires_at < now` → P06；工作项已失去提案前提（按 P08 的触发类型规则）→ P08 | — |
| `fleet.redispatch_machine_offline` | 消费 `machine.state_changed{to:OFFLINE}`，延迟 5 min | 任务键 `machine:<id>:offline:<state_changed_at>` | 仍 OFFLINE → 对其 Agent 的 CLAIMED 分派调 02 `Release`；当前执行 INTERRUPTED 的工作项按 BR-05-52(e) 生成 REDISPATCH 提案 | 机器已恢复 → 放弃 |
| `fleet.review_dispatch` | 消费 `work_item.state_changed{to:NEEDS_HUMAN_REVIEW}`（AUTONOMOUS 项目）；评审 30 min 超时延迟任务 | 任务键 `review:<work_item_id>:<attempt>` | BR-05-53 | 连续 2 次超时 → 停止，交 08 `NO_ELIGIBLE_AUTHORITY` |
| `fleet.triage_wakeup` | 消费 `intake.received`、`intake.triage_requested` | 任务键 `triage:<intake_id>:<event_id>` | BR-05-54 下发 `agent.triage` | Agent 未运行 → 放弃（01 看门狗兜底） |
| `fleet.release_notes_drafting` | 消费 `delivery.release_notes_requested`（07）；30 min 截止延迟任务 | 任务键 `release_notes:<delivery_id>:<request_seq>` | BR-05-56 | 无起草人 / 超时 → 07 `ReleaseNotesDrafting.ReportUnavailable`，不重试 |
| `fleet.starvation_alert` | 每 10 min | dlock 全局 | READY 无分派 >4 h → N-05-04（每项每 4 h 一次） | — |
| `fleet.agent_stats_refresh` | 每 10 min 全量（24 h 内有活动的 Agent）；消费 `decision.made` / `claim.submitted` / `execution.cost_updated` 去抖 60 s | dlock `fleet:stats:<tenant_id>` | 按 BR-05-30–32 重算并 upsert；`source_watermark` 前进 | 失败下轮重算（幂等） |
| `fleet.ensemble_timers`（v1.1） | 每 10 min | dlock 全局 | H11 审批 24 h 过期；H12 选择 24 h 提醒 | — |
| `fleet.ensemble_guard`（v1.1） | 消费 `execution.cost_updated`（取 `cumulative_cost_micros`）、`execution.state_changed`；兜底每 60 s | 每计划 dlock `fleet:ensemble:<plan_id>` | 累计成本、80% 通知、100% → H04；全部终态或 deadline → H03；报告生成 → H05 | — |
| `collab.event_projector` | 消费 §7.2 列出的领域事件 | 按 `(channel_id, source_event_id)` 唯一键幂等 | 写 EVENT / 卡片或更新卡片 `payload.status` | 至少一次；失败重试 8 次后记 `collab_projection_failures_total` |
| `collab.agent_mention_delivery` | 消费 `message.posted`（含 AGENT 提及）；Agent 启动时补投 | 任务键 `mention:<message_id>:<principal_id>` | 经 fleet 下发 `agent.inbox`，写 `delivered_at`；BR-05-88 深度限制 | 未运行不投递，保留 24 h |
| `collab.default_membership_reconcile` | 每 10 min；并**立即**消费 `project.created`、`project.members_changed`（07）、`agent.retired` | dlock `collab:membership:<tenant_id>` | 按 BR-05-81 增删 DERIVED 成员 | — |
| `collab.hook_delivery`（v1.1） | 消息写入后 | 任务键 `hook:<hook_id>:<message_id>` | BR-05-95 | 5 次退避；20 连败禁用 |

### 7.2 领域事件

**发布**（经 09 outbox；载荷外层按 00 §7.1）：

| 事件 | 时机 | `data` |
|---|---|---|
| `agent.created`（CCR-05-08） | F02 提交 | `{agent_id, principal_id, roles[], machine_id, runtime_kind, model_id}` |
| `agent.state_changed`（00） | 每次 AgentState 迁移，及 process_status 进入 UNREACHABLE / 恢复 | `{agent_id, principal_id, from, to, process_status, reason?, restart_exhausted}` |
| `agent.retired`（00） | R7 / 强制退役 | `{agent_id, principal_id, mode, retirement_id, successor_kind?, successor_principal_id?, handover_artifact_id?}` |
| `dispatch.proposed`（00） | P01 | `{proposal_id, work_item_id, role, trigger, policy, result, recommended_agent_id?, requires_human, guard_reasons[], planning_cost_micros}`（08 对 `requires_human=true` 生成「派单确认」待办） |
| `dispatch.applied`（00） | P02 / P03 / P04 提交后 | `{proposal_id, work_item_id, assignee_principal_id, resolution_kind, assignment_id, decision_id, authority_source}`（02 已在同事务落分派；此事件仅供 collab 投影、08 关闭待办与通知，**02 不消费**） |
| `dispatch.rejected`（00） | P05 人驳回 | `{proposal_id, work_item_id, resolution_kind:"DISMISSED", reason}` |
| `dispatch.expired`（00） | P06 / P07（STALE）/ P08 | `{proposal_id, work_item_id, resolution_kind}` |
| `ensemble.plan_created` | H01 | `{plan_id, work_item_id, branch_count, budget_cap_micros, requires_approval}` |
| `ensemble.cancelled` | H06 / H10 / H11 | `{plan_id, work_item_id, reason:"ALL_FAILED"\|"MANUAL"\|"APPROVAL_EXPIRED"}` |
| `ensemble.started`（00） | H02 / 自动 RUNNING | `{plan_id, work_item_id, branches:[{agent_id, model_id, execution_id}], budget_cap_micros}` |
| `ensemble.compared`（00） | H05 | `{plan_id, work_item_id, system_pick_branch_id, lead_pick_branch_id, requires_human}`（`requires_human=true` 时 08 建 ENSEMBLE_SELECTION 待办；AUTO 且系统与主导一致时为 false） |
| `ensemble.selected`（CCR-05-08） | H07 / H08 / H09 | `{plan_id, work_item_id, selected_branch_id, selected_by, synthesized:bool}` |
| `channel.created` / `channel.archived`（CCR-05-08） | C02 / 归档 | `{channel_id, project_id, kind}` |
| `message.posted`（CCR-05-08） | C09 与系统消息 | `{channel_id, message_id, seq, kind, author{kind,id}, mention_principal_ids[], trigger_depth}`（**不含正文**） |

**消费**：

| 事件 | 来源 | 处理 |
|---|---|---|
| `work_item.created` / `work_item.state_changed` / `work_item.assigned` | 02 | 派单循环；提案 P08；投影 EVENT（fold_key=`work_item:<id>`）；进入 NEEDS_HUMAN_REVIEW → DECISION_CARD，AUTONOMOUS 另触发 BR-05-53；`infra_retries_exhausted=true` → BR-05-52(d) |
| `intake.received` / `intake.triage_requested` | 01 | BR-05-54 唤醒协调员 |
| `decision.made` | 02 | DECISION_CARD 状态更新；统计刷新；ASSIGN 产生的分派变化触发 P08 |
| `claim.submitted` | 02 | 统计刷新；EVENT |
| `execution.started` / `execution.state_changed` / `execution.cost_updated` | 03 | EVENT（fold_key=`execution:<id>`，FAILED / INTERRUPTED → attention）；ensemble 守护；统计 |
| `machine.state_changed` / `machine.runtimes_changed` | 04 | A08 / A09 / A19；重派；运行时缺失 → Agent 详情告警 |
| `project.created` / `project.stage_changed` / `project.mode_changed` | 07 | 成员 reconcile；CLOSED / CANCELLED → 归档项目全部频道；HYBRID 切换时 07 已置 MANUAL，本模块把该项目 PROPOSED 且 AUTO 待生效的提案置 `requires_human=true` |
| `delegation.granted` / `delegation.revoked` / `delegation.expired`、`escalation.triggered` | 08 | EVENT（attention）；评审派单（BR-05-53）与派单循环重新评估 |
| `delivery.release_notes_requested` | 07 | BR-05-56：向起草 Agent 下发 `agent.release_notes` |
| `project.members_changed` | 07 | 立即触发 `collab.default_membership_reconcile`（新成员可被 @） |
| `budget.threshold_reached{threshold_pct}` | 07 | EVENT（attention）；`threshold_pct ≥ 100` → 派单循环 G2 命中、ensemble H04 |
| `memory.approved` 等 | 06 | 不消费 |

### 7.3 审计事件（`audit.Appender`，与业务写同事务；`detail` 含 `reason`、`evidence_refs[]`、`authority_source`）

`fleet.agent_created`、`fleet.agent_updated`（字段名 diff，不含提示词正文，仅 hash）、`fleet.agent_full_auto_enabled`、`fleet.agent_started` / `stopped` / `restarted`、`fleet.agent_crashed`、`fleet.agent_restart_exhausted`、`fleet.agent_rebound`、`fleet.agent_credential_issued` / `revoked`、`fleet.agent_retirement_started` / `cancelled`、`fleet.agent_retired`（`mode`）、`fleet.agent_cleanup_abandoned`、`fleet.dispatch_auto_applied`、`fleet.dispatch_confirmed`、`fleet.dispatch_overridden`、`fleet.dispatch_dismissed`、`fleet.ensemble_started`、`fleet.ensemble_budget_stopped`、`fleet.ensemble_selected`、`fleet.ensemble_cancelled`、`collab.channel_created` / `archived`、`collab.member_added` / `removed`、`collab.message_edited` / `retracted`（无正文）、`collab.hook_changed`。权限拒绝（403）写 `outcome=denied` 的同名事件。

### 7.4 实时（09 SSE）

主题（00 §9 语法：单项 = 单数 snake_case kind，集合 = 复数，项目扇出 = `tenant/{t}/project/{p}`）：`tenant/{t}/agent/{id}`（状态、进程、负载、统计更新时间）、`tenant/{t}/agents`（列表行）、`tenant/{t}/project/{p}`（`dispatch_proposal` 增改子流）、`tenant/{t}/ensemble_plan/{id}`、`tenant/{t}/channel/{id}`（`message.created` / `message.updated` / `message.retracted` / `member.changed`，载荷含完整 `Message` 表示，订阅时按 C03 授权过滤）、`tenant/{t}/inbox/{principal_id}`（未读与提及计数）。客户端断线重连以 `after_seq` 调 C08 补齐；目标 ≤1 s 呈现。本模块向 09 注册 `TopicAuthorizer`：`agent` / `agents` 需 `agent_read`；`project` 的 `dispatch_proposal` 子流与 `ensemble_plan` 需项目 `work_item_read`；`channel` 按 C03 授权。

---

## 8. 页面规格

所有页面在 `apps/delivery`；路由前缀 `/t/:tenantID`；通用五态（骨架、空、错误可重试、无权限说明缺失权限码、离线 / 重连横幅）；Agent 头像统一 `ActorAvatar`（圆角方形像素人 + 芯片角标）；状态用 `StateBadge`（图标 + 文字 + 语义色：ACTIVE 绿「运行中」、STOPPED 灰「已停止」、CRASHED 红「已崩溃」、RETIRING 琥珀「退役中」、RETIRED 虚线灰「已退役」、DRAFT 中性「草稿」、UNREACHABLE 虚线「机器离线」）。

### 8.1 成员与 Agent（`/t/:tenantID/members`）

- **目的**：一张表看人与 Agent 的角色、负载、当前工作、状态。
- **布局**：标题栏〔新建 Agent〕（`agent_manage`）；筛选条：类型（全部 / 人 / Agent）、角色、状态、项目、机器、搜索；表格（`@workspace/ui` auto-table，F15）。
- **列**：头像 + 名称（Agent 显示 handle）、类型标签、角色（人：项目角色汇总；Agent：AgentRole 芯片）、runtime × 模型（仅 Agent）、负载（`active/max` 进度条，满载琥珀）、当前工作项（≤3 链接）、首次验收率 30 天（样本 <5 显示「样本不足」）、状态、最近活跃。行操作（Agent）：启动 / 停止 / 重启（按 §4.1 可用性）、详情、退役（`agent_retire`）。
- **交互**：点击 Agent 行打开详情抽屉（宽屏）或跳转详情页（移动端）；状态列实时更新（SSE）。
- **空态**：无 Agent →「还没有 Agent。新建一个，让它接手第一个工作项」〔新建 Agent〕。
- **无权限**：无 `agent_read` 时只显示人类行并提示「缺少 agent_read，Agent 已隐藏」。
- **移动端**：卡片列表（头像、名称、状态、负载），筛选收入 Sheet。

### 8.2 新建 Agent（`/t/:tenantID/agents/new`）

- **分步表单**（auto-form，单页分区；移动端分步）：
  1. **身份**：名称*（1–64，实时查重 142001）、handle（自动生成可改，正则提示）、人设（≤2000）、头像（风格切换 + 6 个种子候选 + 〔换一批〕）。
  2. **运行环境**：机器*（下拉，显示状态；OFFLINE 可选但「创建后立即启动」自动关闭并提示）、runtime*（来自该机检测，MISSING 禁用）、模型*（该机目录 ∪ 手填，标注来源）、推理强度（按 runtime 能力）、授权模式（READ_ONLY / ON_REQUEST / FULL_AUTO；FULL_AUTO 红色语义 + 说明 + 提交时 step-up 对话框）、上下文预算（4000–200000）。
  3. **职责**：角色*（多选 EXECUTOR / REVIEWER / COORDINATOR / LEAD，含说明；REVIEWER + FULL_AUTO 冲突即时报错）、最大风险、并行上限（1–4）、能力标签（chip 输入，正则）。
  4. **能力**：技能（06 技能库多选，≤20，显示版本）、MCP 白名单（来自该机能力检测，检测超 24 h 显示「数据可能过期」〔重新检测〕跳 04）。
  5. **协作与记忆**：默认频道名（加入项目时自动加入）、记忆策略（06 提供的策略档位选择器，链接到 06 说明）、自动重启开关。
  6. **系统提示词***：等宽编辑器，字节计数（≤32 KB，超限禁用提交）。
- **按钮**：〔创建并启动〕（默认；机器非 ONLINE 时禁用并提示）、〔仅创建（草稿）〕、〔取消〕。提交中禁用、带 `Idempotency-Key`。
- **结果**：成功跳 Agent 详情，顶部进度：「身份已创建 → 正在把凭证送到 <机器> → 启动中」；凭证投递失败显示〔重新投递〕。服务端 422 映射回字段错误；503 142030 显示「团队暂时无法创建 Agent（<能力>未启用）」。

### 8.3 Agent 详情（`/t/:tenantID/agents/:agentID/:tab`）

- **头部**：头像、名称、handle、状态徽章（含 process_status）、机器（链接到 04 机器详情）、runtime × 模型 × 强度、「需重启生效」徽标（修订落后时）；按钮：〔启动〕（DRAFT / STOPPED，且机器 ONLINE、凭证已送达）、〔停止〕（ACTIVE / CRASHED）、〔重启〕（ACTIVE / CRASHED）、〔换绑机器〕（DRAFT / STOPPED / CRASHED 且无运行中执行）、〔编辑〕、〔退役〕（红色次要按钮，`agent_retire`）。不可用按钮悬浮说明原因（如「机器离线」）。CRASHED 且 `restart_exhausted` 时显示红色横幅：「30 分钟内崩溃 4 次，已停止自动重启」+ 最近 3 条崩溃原因 +〔查看进程记录〕。
- **Tab 概览**：绩效四卡（首次验收率、单位已接受成本、返工率、当前负载，含窗口切换 30 / 90 天与 `computed_at`）、按工作项类型的小表、最近进程事件、所在项目（主导的项目带 LEAD 标）。
- **Tab 工作项**：复用 02 列表（`assignee_principal_id=eq:<principal>`），进行中 / 历史切换；每行 `CompletionLadder`。
- **Tab 授权**：AgentRole 与项目角色绑定（只读，链接到 07 项目团队）、08 委托（剩余有效期 `CountdownRing`、使用次数）；FULL_AUTO 红色标签。
- **Tab 技能**：06 挂载列表（技能、版本、是否跟随最新、归档提示）；〔挂载技能〕调用 06 `PUT /agents/{id}/skills/{skill_id}`。
- **Tab 记忆**：06 提供的记忆组件（条目、写入审计、撤销）；本页只放容器与链接「在知识库中打开」。
- **Tab 私信**：左侧 AGENT_DIRECT 列表（对方头像、项目、最近消息时间），右侧只读消息流（与频道同组件，无输入框，顶部提示「Agent 之间的私信，仅可查看」）。
- **Tab 工作目录**：面包屑 + 文件列表（F12）+ 只读预览（代码高亮，≤1 MiB）；机器离线 → 离线态「机器离线，无法浏览」。
- **实时**：头部状态、负载、统计更新时间经 SSE；离线时顶部横幅并冻结按钮。
- **移动端**：Tab 变为横向滚动；头部按钮收入「更多」菜单，〔退役〕在菜单底部红色。

### 8.4 退役向导（`/t/:tenantID/agents/:agentID/retirement`）

- **入口**：详情〔退役〕→ 模式选择对话框：「正常退役（推荐，机器在线）」/「强制退役（机器异常时）」；机器非 ONLINE 时正常退役禁用并说明 142024。
- **正常退役步骤条**：① 检查（在途工作项 n、运行中执行 n、主导的项目列表「退役后这些项目的派单策略会切回人工确认」）→ ② 交接文档（进度、10 min 倒计时 `CountdownRing`；完成后内嵌预览 + 下载；超时显示系统摘要与〔让 Agent 重试一次〕）→ ③ 接手人（单选：人 / Agent / 稍后再选；成员选择器分组显示负载与首次验收率；不合格项逐条标红并说明）→ ④ 确认并执行（汇总将转交 / 释放的工作项、将清理的机器目录、将吊销的凭证；〔开始退役〕）→ 执行中逐步打勾（转交、清理、吊销、完成），失败项列表可〔重试〕。①–③ 可〔取消退役〕。
- **强制退役**：红色对话框：后果清单（「正在进行的执行会被取消」「未保存的工作状态会丢失」「操作无法撤销」「交接文档只能由系统根据记录生成」）、原因（≥10）、输入 Agent 名称（逐字一致才可提交）、step-up 重新认证；提交后显示结果页（丢失清单、待清理机器）。
- **移动端**：步骤条变为顶部「第 n / 4 步」；成员选择器全屏 Sheet。

### 8.5 项目「团队编排」卡片（项目总览右栏，`/t/:tenantID/p/:projectID`）

- **内容**：主导 Agent（头像、状态、runtime × 模型；空则「未设置 — 所有派单由系统评分并需人确认」）、派单策略（MANUAL「提案需人确认」 / AUTO「策略内自动生效」+ 护栏说明链接）、并行上限（1–5，默认 3）、近 24 h：提案数、自动生效数、被改派数、无合格候选数。
- **编辑**：〔编辑〕（`project_manage`）打开表单，提交调用 07 #4（If-Match = 项目版本）；主导 Agent 选择器只列 ACTIVE、含 LEAD、在本项目有 `agent_lead` 绑定的 Agent（07 校验 127042）；切 AUTO 且无主导 Agent 时提示「AUTO 需要主导 Agent，否则按人工确认处理」；项目只读（CLOSED / CANCELLED）时隐藏编辑。
- **链接**：〔查看派单提案〕→ 8.6。

### 8.6 派单提案面板（`/t/:tenantID/p/:projectID/dispatch`）

- **布局**：左列提案列表（筛选：待处理 / 已生效 / 已改派 / 已过期 / 无合格候选；待处理按 `expires_at` 升序，`CountdownRing` 显示剩余时间）；右侧提案详情。
- **详情**：工作项摘要（标题、类型、风险、必需技能）；推荐人卡（头像、总分、四舍五入两位）与理由（来源标签「主导 Agent」/「系统评分」）；护栏提示（如「风险 HIGH，需要人工确认」）；候选表：排名、Agent、总分、五个分量条（技能 / 首次验收 / 成本 / 负载 / 轮换，悬浮显示原始输入）、不合格候选折叠区（不合格原因中文化，如「机器离线」「技能不符：缺少 frontend」）；〔复算说明〕展示公式与 `inputs_digest`。
- **按钮**：〔确认派给 X〕（PROPOSED、有 `work_item_assign`）、〔改派〕（打开成员选择器，人与 Agent 分组，必须填理由）、〔驳回〕（理由必填）、〔重新生成〕（非 PROPOSED 时，D02）。并发冲突 409 → 刷新并提示「已被 Morgan 处理」。
- **无合格候选**：显示原因直方图与「最接近的 3 名」及各自差什么，按钮〔指派给…〕〔驳回〕。
- **移动端**：列表全屏，点击进入详情；候选分量以紧凑横条显示。

### 8.7 Heavy 对比报告视图（`/t/:tenantID/p/:projectID/work-items/:workItemID/ensembles/:planID`，v1.1）

- **头部**：计划状态、触发原因、预算环（已用 / 上限，80% 琥珀、100% 红）、截止倒计时；〔批准启动〕（PENDING_APPROVAL）、〔取消〕。
- **对比表**：每枝一列（≤5）：Agent + 模型、终态、证据覆盖、必需验证、测试、diff 规模、成本、时长、AI 辅助意见（灰色「仅参考」标签）、系统排名；每行最优值加粗；不宣称「胜者」，只在列头标「系统排序第 1」与「主导 Agent 推荐」。
- **主导 Agent 理由**：独立区块，来源标签。
- **选择**：〔选这一枝送审〕（每列底部）、〔合成…〕（选择主枝 + 被合成枝，显示剩余预算）；提交需理由；选择后提示「已提交声明，仍需正常验证与裁决」。
- **落选枝**：灰显 + 「已归档（保留证据）」，可展开查看执行详情。
- **移动端**：横向滑动的枝卡片，比较项纵向排列。

### 8.8 频道（`/t/:tenantID/p/:projectID/channels/:channelID`；团队级入口 `/t/:tenantID/channels` 按项目分组列出本人可读频道与未读，点击进入项目内路由）

- **布局**：左栏频道列表（默认频道置顶、自建频道、未读数与 @ 标记；〔新建频道〕）；中栏消息流（虚拟滚动，>500 条流畅）；右栏（宽屏）成员列表 / 频道信息。
- **消息渲染**：TEXT（头像、名称、Agent 芯片、时间、已编辑标记、回复引用、附件）；EVENT 折叠行（最新事件 + 「另 n 条」，展开显示全部；attention 事件红 / 琥珀单独显示）；DECISION_CARD（完成阶梯、验证 n/n、证据覆盖、〔通过〕〔要求修改〕〔拒绝〕——按钮按 02 权限可用，拒绝弹 `StructuredFeedbackForm`；已裁决显示结果与 `AuthorityNote`）；DISPATCH_CARD（推荐人、分数、理由摘录、护栏、〔确认〕〔改派〕→ 跳 8.6 或内嵌对话框；处理后变为结果行）。撤回消息显示「消息已撤回」。
- **输入框**：多行；`@` 弹出候选（C15，人与 Agent 分组，显示状态；退役 Agent 不出现）；选中插入令牌并渲染为 chip；超 8000 字符禁用发送；附件 ≤5；发送失败保留草稿可重试；Enter 发送、Shift+Enter 换行。提及非成员（服务端 145003）时内联提示并提供〔邀请并发送〕（有权限时）。
- **未读**：进入频道定位到第一条未读，滚动到底部时 PUT read-marker（节流 2 s）；「n 条新消息」浮条。
- **状态**：归档频道只读横幅；非成员（CUSTOM）显示〔加入频道〕（有权限时）；Agent 私信在此页不出现（见 8.3）。
- **实时**：SSE 新消息 ≤1 s 呈现；断线横幅「正在重新连接…」，恢复后按 `after_seq` 补齐。
- **移动端**：频道列表与消息流分页切换；底部快捷栏「频道」显示总未读；长按消息出编辑 / 撤回菜单。

---

## 9. 通知

经 09 `Notifier`（站内 inbox + 用户配置的外部渠道）；文案三语 key 化，以下为 zh-CN。

| # | 事件 | 接收人 | 渠道 | 文案模板 | 去重与合并 |
|---|---|---|---|---|---|
| N-05-01 | 自动重启用尽（A11） | Agent 创建者 + 持 `agent_manage` 者 | 站内 + 邮件 | 「Agent {name} 在 30 分钟内崩溃 {n} 次，已停止自动重启。最近原因：{reason}」 | 每 Agent 每次 exhausted 一条 |
| N-05-02 | 凭证投递失败 | 创建者 | 站内 | 「无法把 {name} 的凭证送到机器 {machine}，Agent 仍为草稿」 | 每 Agent 每 24 h 一条 |
| N-05-03 | 提案需人确认（`requires_human=true`：MANUAL / 护栏 / 频道请求） | 项目内持 `work_item_assign` 的人；CHANNEL_REQUEST 另含发起人 | 站内（我的裁决分类「派单确认」） | 「{work_item} 建议派给 {agent}（{score} 分）：{rationale_excerpt}」 | 同项目 10 min 内合并为「{n} 条派单待确认」 |
| N-05-04 | 无合格候选 / 饥饿 >4 h | 项目交付负责人 | 站内 | 「{work_item} 没有可派的 Agent：{top_reason}」 | 每项每 4 h 一条 |
| N-05-05 | 被派 / 被转交（受派者为人） | 受派者 | 站内 + 移动推送 | 「你被分派了 {work_item}（来自 {source}）」 | 由 02 的 `work_item.assigned` 通知承担，本模块不重复发 |
| N-05-06 | 退役完成 / 失败 | 发起人；接手人（完成时） | 站内 | 「{name} 已退役，{n} 个工作项已转交给你，交接文档：{link}」 | 每退役一条 |
| N-05-07 | Ensemble 预算 80% / 100% | 计划批准者 + 项目交付负责人 | 站内 | 「并行求解 {work_item} 已用 {pct}% 预算」 | 每阈值一次 |
| N-05-08 | Ensemble 待选择 | 同上 | 站内（我的裁决 `ENSEMBLE_SELECTION`） | 「{work_item} 的 {n} 枝结果已可比较」 | 进入时一次；之后每 24 h 提醒，≤3 次（H12） |
| N-05-11 | Ensemble 待批准 | 项目内持 `work_item_assign` 的人（超默认预算另含 `project_manage` 持有者） | 站内（我的裁决 `ENSEMBLE_APPROVAL`） | 「{work_item} 的并行求解（{n} 枝，预算 ${cap}）等待批准，24 小时后自动取消」 | 每计划一次 |
| N-05-12 | 评审派单无合格 Agent / 连续超时（AUTONOMOUS） | 项目所有者 | 站内 | 「{work_item} 没有可用的评审 Agent，已转人工裁决」 | 每项一次（同时交 08 `NO_ELIGIBLE_AUTHORITY`） |
| N-05-09 | 被 @（人类） | 被提及者（静音频道仍通知提及） | 站内；用户离线 >10 min 且未读时发邮件摘要 | 「{author} 在 #{channel} 提到了你：{excerpt≤140}」 | 同频道 5 min 内合并；邮件每人每小时 ≤1 封 |
| N-05-10 | 主导 Agent 变为不可用（崩溃用尽 / 退役） | 其主导项目的交付负责人 | 站内 | 「{project} 的主导 Agent {name} 不可用，派单已切回人工确认」 | 每项目每事件一条 |

通知载荷不含消息正文以外的代码；外部渠道（邮件）中的摘录只含 TEXT 前 140 字。

---

## 10. 验收标准

公共夹具：租户 T；人 Sam（team_owner）、Morgan（项目 P 的 delivery_lead）、阿澈（engineer）；机器 M1（ONLINE，CLAUDE_CODE + CODEX，槽 2）、M2（ONLINE，CODEX，槽 1）、M3（OFFLINE）；项目 P（HYBRID，`dispatch_policy=MANUAL`）。

```
AC-05-01  [integration]  关联 US-05-02, BR-05-02, BR-05-03
Given Sam 持 agent_manage；M1 ONLINE 且目录含模型 opus-x
When  POST /tenants/T/agents {name:"Ada", machine_id:M1, runtime_kind:CLAUDE_CODE, model_id:"opus-x", roles:[EXECUTOR], skill_ids:[S1], system_prompt:"…", start:true}，带 Idempotency-Key K
Then  201，state=DRAFT、credential_delivery=PENDING；同一事务内存在：服务账号 Principal（login_name=agent-ada）、租户成员、fleet_agents、fleet_agent_roles(EXECUTOR→agent_executor)、06 挂载 S1、审计 fleet.agent_created、outbox agent.created
And   机器 ACK agent.provision 后 credential_delivery=DELIVERED，自动下发 agent.start；上报 RUNNING 后 state=ACTIVE、process_status=RUNNING
And   以同 K 重放 → 返回首次响应，Agent 数不变；同 K 不同请求体 → 422 176425（09 平台码）

AC-05-02  [integration]  关联 BR-05-02
Given 注入故障：06 SkillMounter.MountOn 返回错误
When  同 AC-05-01 请求
Then  5xx；iam principals / service_accounts / tenant_members、fleet_agents、fleet_agent_roles、审计链、outbox 逐表计数前后相等（无残留身份）

AC-05-03  [integration]  关联 BR-05-02
Given identity ServiceAccountProvisioner 未注册（模块禁用）
When  POST /tenants/T/agents
Then  503，业务码 142030；无行写入；启动日志含一次 slog.Warn 指明缺失能力

AC-05-04  [integration]  关联 BR-05-04, BR-05-05, BR-05-06, BR-05-07
When  分别提交：模型不在 M1 目录；MCP "jira" 不在 M1 检测结果；roles=[]；roles=[REVIEWER] 且 authorization_mode=FULL_AUTO；FULL_AUTO 但无 reauth_proof；start:true 且 machine_id=M3
Then  依次 422 142004；422 142007（data.unknown=["jira"]）；422 142005；422 142005；403 140403（REAUTH_REQUIRED）；409 142002；均无行写入

AC-05-05  [integration]  关联 BR-05-01
Given 已存在非 RETIRED 的 "Ada"
When  再建 name:"ada"（不显式 handle）；再建 handle:"ada"
Then  前者 422 142001；后者 422 142001；name 不同、handle 缺省时自动得到 "ada-2"

AC-05-06  [integration]  关联 US-05-03, BR-05-11, BR-05-12
Given Ada ACTIVE·RUNNING，auto_restart=true
When  t=0 机器上报 EXITED(exit_code=1)；10 s 后自动重启成功；t=5 min、t=12 min 再次崩溃并各自重启成功；t=20 min 第 4 次崩溃
Then  前三次各写 CRASHED + AUTO_RESTART，restart_count_in_window 依次 1,2,3，退避 10 s/30 s/90 s；第 4 次后 state=CRASHED、restart_exhausted=true、不再下发 start；N-05-01 一条；Ada 的 CLAIMED 分派产生 REDISPATCH 提案
And   Sam POST /agents/Ada/restarts → 202，计数清零，exhausted=false

AC-05-07  [unit]  关联 BR-05-12
Given 崩溃后进程连续 RUNNING 10 min
Then  restart_count_in_window 归零；随后再崩溃按第 1 次处理（退避 10 s）

AC-05-08  [integration]  关联 BR-05-11, A08
Given Ada ACTIVE·RUNNING 于 M1
When  M1 变 OFFLINE 持续 4 min 后恢复，并上报 Ada RUNNING
Then  期间 state=ACTIVE、process_status=UNREACHABLE，无 CRASHED 记录、不计重启次数；派单评估中 Ada 带 E3；恢复后 RUNNING，未触发重派（未满 5 min）

AC-05-09  [integration]  关联 BR-05-52
Given Ada 持有 W1 的 CLAIMED 分派（尚未开始执行），M1 OFFLINE
When  5 min 宽限到期 M1 仍 OFFLINE
Then  经 02 Release，W1 回 READY；生成 trigger=REDISPATCH 提案，Ada 在 candidates 中 ineligible_codes 含 E3 与 E9（排除 24 h）

AC-05-10  [integration]  关联 §4.1 非法迁移
Given Ada RETIRED
When  PUT run-state ACTIVE / POST restarts / PUT machine-binding / PATCH system_prompt
Then  均 409 142010，data.state="RETIRED"；DRAFT 上 POST restarts → 409 142010

AC-05-11  [integration]  关联 BR-05-13
When  对 Ada（STOPPED）并发两个 PUT run-state {ACTIVE}
Then  一个 202；另一个 202（看到同一目标，幂等返回）或 409 142011（Retry-After: 5），只下发一条 agent.start

AC-05-12  [integration]  关联 US-05-04, BR-05-14
Given Ada STOPPED 于 M1，无非终态执行
When  PUT /agents/Ada/machine-binding {machine_id:M2, runtime_kind:CODEX, model_id:"gpt-y"}（If-Match 正确）
Then  200；旧凭证吊销、新凭证 PENDING；M1 收到 agent.cleanup；审计 fleet.agent_rebound
And   ACTIVE 时调用 → 409 142010；有 RUNNING 执行时 → 409 142012；If-Match 过期 → 409 140409；缺 If-Match → 428 140428

AC-05-13  [e2e]  关联 US-05-05, BR-05-20–24, §4.2
Given Ada ACTIVE，持 W1（IN_PROGRESS，运行中执行 E1）与 W2（CLAIMED）；Bo（ACTIVE，EXECUTOR，P 中有 agent_executor）
When  Sam 发起 NORMAL 退役；Agent 在 3 min 内写出交接文档；选择接手人 Bo；推进
Then  RETIRING 时 03 取消 E1（SUPERSEDED）；交接 Artifact handover_source=AGENT；W1、W2 经 02 Reassign 转给 Bo，各一条 ASSIGN Decision（actor=Sam，reason 含 retirement_id）；M1 ACK cleanup；凭证吊销、服务账号停用、项目角色绑定移除、委托撤销；Ada → RETIRED；agent.retired；N-05-06 发给 Sam 与 Bo
And   Ada 的历史消息仍显示，作者带「已退役」徽标；@Ada → 422 145016

AC-05-14  [integration]  关联 §4.2 R2 超时
Given 退役进入 HANDOVER，Agent 10 min 未产出
Then  生成系统摘要 Artifact（handover_source=SYSTEM，含前 5 节）；向导可重试一次，第二次 handover_retry → 422 142021

AC-05-15  [integration]  关联 BR-05-24, §4.2 非法
Given 退役在 SUCCESSOR 步
When  PATCH {state:CANCELLED}
Then  Agent → STOPPED，交接 Artifact 保留；之后对该退役记录任何 PATCH → 409 142015；推进到 REASSIGN 后再取消 → 409 142015

AC-05-16  [integration]  关联 US-05-06, BR-05-25
Given Ada ACTIVE 于 M3（OFFLINE），持 W2
When  Sam POST retirements {mode:FORCED, confirm_name:"ada", reason:"机器已报废无法恢复"}（名称大小写不符）
Then  422 142016，无变化
When  confirm_name:"Ada" 但无 step-up
Then  403 140403（REAUTH_REQUIRED）
When  完整请求（step-up 3 min 内）
Then  单事务：Ada RETIRED、退役记录 FORCED/COMPLETED、凭证吊销、pending_cleanup_machine_id=M3；02 收到 agent.retired 后 W2 回 READY；审计 detail.mode=FORCED 且列出丢失清单
And   M3 重连后首个 poll 下发 agent.cleanup

AC-05-17  [integration]  关联 BR-05-20
Given M1 OFFLINE
When  POST retirements {mode:NORMAL}
Then  422 142024；UI 引导强制退役

AC-05-18  [integration]  关联 BR-05-30–34
Given Ada 30 天内：W1 首次裁决 APPROVE；W2 首次 REQUEST_CHANGES 后再 APPROVE；W3 首次 REJECT；共 4 个声明（W2 两次）；W1、W2 ACCEPTED，Ada 在 W1、W2 上执行成本合计 $3.10
Then  first_decisions=3、first_pass_accepts=1、FPA=33.33%（UI 显示「样本不足（3）」）；claims=4、rework_decisions=2、RR=50%；accepted_items=2、CPA=$1.55
And   新 decision.made 到达 60 s 内 computed_at 更新

AC-05-19  [unit]  关联 BR-05-40, BR-05-41（评分确定性夹具）
Given 工作项 W：FEATURE、risk MEDIUM、必需标签 {frontend}、偏好 {react, i18n}；团队 FEATURE 90 天 FPA p₀=0.70，团队 CPA 中位数 $1.50
  Ada：标签 {frontend,react,i18n,css}；n=10,a=8；CPA $1.20；active 1/max 2，队列 0；r=3
  Bo ：标签 {frontend,react}；n=4,a=4；CPA $2.00；active 0/max 1；r=0
  Cy ：标签 {frontend,i18n,go}；n=0；CPA null（用中位数 $1.50）；active 0/max 1；r=1
  Dev：满足其他条件但机器 M3 OFFLINE
Then  Dev ineligible（E3_MACHINE）
  Ada：skill 10000、fpa round(10000×10.8/14)=7714、cost 10000、load 5000、diversity 4000 → 350000+231420+150000+75000+20000 = 826420
  Cy ：skill 6667、fpa 7000、cost round(10000×0.50/0.80)=6250、load 10000、diversity 8000 → 233345+210000+93750+150000+40000 = 727095
  Bo ：skill 6667、fpa 8500、cost 0、load 10000、diversity 10000 → 233345+255000+0+150000+50000 = 688345
  排名 Ada(82.64) > Cy(72.71) > Bo(68.83)；同一输入快照重复计算 1000 次 inputs_digest 与排名完全一致

AC-05-20  [unit]  关联 BR-05-41 并列打破
Given X 与 Y score 相同；X fpa_bp 7500、Y 7600
Then  Y 在前；若 fpa 也相同、active 0 vs 1 → active 0 在前；依次至 agent_id 数值小者在前

AC-05-21  [integration]  关联 US-05-08, BR-05-43–45（MANUAL）
Given P MANUAL，主导 Agent L；W 进入 READY
When  派单循环运行，L 选择 Ada 并给出理由
Then  一条 PROPOSED 提案（requires_human=true，rationale_source=LEAD），默认频道出现 DISPATCH_CARD（不折叠），N-05-03 发给 Morgan；W 无分派
When  Morgan PUT resolution {kind:CONFIRM}（If-Match 正确）
Then  同事务：02 Assign 生成分派与 ASSIGN Decision（actor=Morgan，authority=ROLE:delivery_lead，detail.dispatch_proposal_id）；提案 APPLIED/CONFIRMED；dispatch.applied；卡片变为「已派给 Ada · Morgan 确认」

AC-05-22  [integration]  关联 BR-05-44, BR-05-45（AUTO）
Given P 切为 AUTONOMOUS（07 置 AUTO），主导 Agent L，W 风险 MEDIUM，预算充足
When  派单循环运行
Then  提案 AUTO_APPLIED；02 ASSIGN Decision actor=L（AGENT）、authority_source=POLICY:dispatch_auto、detail.dispatch_proposal_id=该提案；频道仅一条折叠 EVENT，无卡片
And   W' 风险 HIGH → requires_human=true（guard_reasons 含 G1）；L 选第 3 名且分差 >15 分 → G3；项目预算 EXHAUSTED → G2；均停在 PROPOSED

AC-05-23  [integration]  关联 BR-05-42
Given L 返回 pick=Dev（不合格）或 120 s 未回应
Then  推荐 = 系统 top-1，rationale_source=SYSTEM；卡片注明「主导 Agent 未给出有效建议」

AC-05-24  [integration]  关联 US-05-09, P04, BR-05-50
Given PROPOSED 推荐 Ada
When  Morgan OVERRIDE → 阿澈，reason 缺失
Then  422 140400
When  reason:"需要人做客户演示"
Then  02 分派给阿澈（ASSIGN Decision actor=Morgan）；提案 OVERRIDDEN；审计 fleet.dispatch_overridden；Ada 对 W 排除 24 h；W 24 h 内再派时 G8 生效

AC-05-25  [integration]  关联 BR-05-47 并发
When  Morgan 与 Sam 同时 CONFIRM 同一提案
Then  恰一个 200；另一个 409 143004 或 140409；02 中仅一条 ACTIVE EXECUTOR 分派
And   两个项目同时把 Bo（max 1）作为推荐应用 → Agent 行锁下复验 E8，后者 409 143005，提案 EXPIRED(STALE) 并重新生成

AC-05-26  [integration]  关联 US-05-10, BR-05-49
Given P 所有 Agent：2 名机器离线、1 名技能不符、1 名满载
Then  提案 result=NO_ELIGIBLE，直方图 {E3:2, E5:1, E8:1}，附最接近 3 名；不自动应用；4 h 后仍未处理 → N-05-04 一条
When  满载 Agent 释放分派（30 s 去抖后）
Then  原提案 EXPIRED(STALE)，新提案推荐该 Agent

AC-05-27  [integration]  关联 BR-05-46, BR-05-50 抖动
Given 提案 TTL 到期且 W 仍 READY
Then  P06 后立即重生成；同一 W 24 h 内派单第 3 次后不再自动生成，D02 返回 409 143015 直至人工生成

AC-05-28  [integration]  关联 BR-05-70 职责分离
Given L 以 AUTO 把 W 派给 Ada；Ada 声明完成，W 至 NEEDS_HUMAN_REVIEW；L 具 agent_reviewer
When  L POST /work-items/W/decisions {action:APPROVE}
Then  403 120403，reason_code=SEPARATION_OF_DUTIES（detail=LEAD_DISPATCHED）；审计 outcome=denied
Given 另有委托 D（授予 L，scope 覆盖 W 的风险，验证全 PASSED，返工 0）
Then  同请求 200，Decision authority_source=DELEGATION:D
And   AUTO 下 L 永不出现在合格候选中（E10）；MANUAL 下推荐 L 时 G7 要求人确认

AC-05-29  [integration]  关联 US-05-11, BR-05-60–62（v1.1）
Given W risk HIGH，ensemble_max_branches=3；合格：Ada(CLAUDE_CODE/opus-x)、Bo(CODEX/gpt-y)、Cy(CLAUDE_CODE/opus-x)、Eve(CODEX/gpt-z)，分数 Ada>Cy>Bo>Eve
When  创建 ensemble plan
Then  选枝 Ada、Bo、Eve（模型多样性优先于 Cy）；HYBRID + HIGH → PENDING_APPROVAL
When  Morgan 批准，cap=9,000,000
Then  3 个 ENSEMBLE 执行共享 ensemble_group_id=plan.id，每枝 max_cost_micros=3,000,000
When  累计成本 7,200,000
Then  N-05-07（80%）一条
When  累计 9,000,000，Bo 已 SUBMITTED、其他仍运行
Then  其余枝经 03 Cancel → STOPPED_BUDGET；进入 COMPARING；报告只含实际数据；审计 fleet.ensemble_budget_stopped
And   请求 4 枝 → 422 143010；合格 <2 → 422 143009；cap 超项目剩余预算 → 422 143011

AC-05-30  [integration]  关联 BR-05-63–66（v1.1）
Given 报告：系统第 1 = Bo，L 推荐 Ada，风险 HIGH
Then  SELECTION_PENDING，DISPATCH_CARD（ENSEMBLE_COMPARISON）不折叠
When  Morgan 选择 Bo 并给理由
Then  02 ClaimSubmitter 以 Bo 身份提交声明，W → AGENT_CLAIMED_DONE → 正常验证 → 仍需裁决；Ada、Eve 枝 NOT_SELECTED 且 archived_at 非空，其执行、证据仍可查询
And   SELECTED 后再 PUT selection → 409 143013

AC-05-31  [integration]  关联 US-05-12, BR-05-84
Given 自建频道 #design 成员 {Morgan, Ada}
When  Morgan 发送 "<@阿澈> 看下"（阿澈非成员）
Then  422 145003，data.not_members=[阿澈]；无消息写入
When  默认频道中提及阿澈（有项目 channel_read）
Then  201；惰性补建阿澈成员行；N-05-09 发给阿澈
And   21 个提及 → 422 145004；提及已退役 Agent → 422 145016；正文 8001 字符 → 413 145005

AC-05-32  [integration]  关联 BR-05-82, BR-05-91 权限拒绝
Then  无 channel_read 读 P 的频道 → 403 145403（data.missing_permission=channel_read）
And   有 channel_read 无 channel_post 发消息 → 403 145403
And   非成员读私有自建频道 → 403 145403（NOT_MEMBER）；他项目频道 id 用本项目作用域地址访问 → 404 145404
And   Morgan 在 Ada↔Bo 私信发消息 → 403 145403（DIRECT_READ_ONLY）；Morgan（agent_read + channel_read）读取该私信 → 200
And   人类 POST channels {kind:AGENT_DIRECT} → 403 145403（DIRECT_READ_ONLY）；HTTP 发送 kind=DISPATCH_CARD → 422 145017

AC-05-33  [integration]  关联 BR-05-51, BR-05-86
Given P 为 AUTONOMOUS（AUTO），主导 Agent L
When  阿澈（无 work_item_assign）在频道写 "<@L> 把 W 派给 Bo"，L 据此 D02 trigger=CHANNEL_REQUEST
Then  提案 requires_human=true（即使 AUTO）；卡片「确认」对阿澈不可用、对 Morgan 可用；W 状态不变
And   执行者 Agent Ada 被 @「开始做 W2」→ 只回复消息；Ada 尝试自建 EXECUTOR 分派 → 403 120403（AGENT_SELF_CLAIM，02）

AC-05-34  [integration]  关联 BR-05-88
Given Ada、Bo 互相 @ 形成链
Then  trigger_depth 0→1→2→3；depth=3 的消息保存但其中的 Agent 提及不投递；Agent 第 11 条/min → 429 145010

AC-05-35  [unit]  关联 BR-05-87 折叠
Given 默认频道序列：E(exec:1) E(exec:1) E(exec:1, attention) E(exec:1) DECISION_CARD E(exec:1) E(exec:2) TEXT E(exec:2)
Then  渲染为：[折叠 2 条] [attention 单行] [单行] [卡片] [单行 exec:1] [单行 exec:2] [TEXT] [单行 exec:2]；卡片永不折叠

AC-05-36  [integration]  关联 BR-05-89
Given Morgan 10:00 发送 TEXT
Then  10:14 PATCH → 200（edit_count=1，修订表 1 行）；10:16 PATCH → 409 145006；第 11 次编辑 → 409 145006
And   作者 DELETE → 204，读取返回 body=null、retracted=true；audit_read 可查修订；对 EVENT DELETE → 409 145006

AC-05-37  [integration]  关联 BR-05-90, BR-05-94
Given 频道 last_seq=40，Morgan last_read_seq=30，其间 3 条 EVENT、1 条自己的消息、1 条已撤回
Then  unread=5（40−30−3−1−1）
When  PUT read-marker {seq:35} 后再 {seq:20}
Then  last_read_seq=35（不回退，第二次 200 无变化）
And   并发 20 个请求向同一频道发消息 → seq 连续 41–60 无重复无空洞

AC-05-38  [integration]  关联 BR-05-81
Given 07 创建项目 P2 时 ChannelProvisioner.CreateDefaultOn 失败
Then  07 整体回滚（见 AC-07-02）；不存在孤立频道
And   P2 进入 CLOSED → 默认频道与自建频道 archived_at 非空，发消息 409 145008；人工归档默认频道 → 409 145011

AC-05-39  [e2e]  关联 US-05-01, 8.1
Given 2 人、3 Agent（1 CRASHED）
Then  成员页 5 行；Agent 行显示头像为圆角方形 + 芯片；CRASHED 行状态红色；Ada 崩溃→重启后 ≤1 s 行状态变化；无 agent_read 的阿澈只看到 2 行并见缺权限提示

AC-05-40  [manual]  关联 8.2–8.8
Given 375 / 768 / 1440 三档
Then  新建 Agent 分步可完成；退役向导步骤条变「第 n / 4 步」；频道输入框 @ 候选可用；派单候选分量条可读；对比报告横滑；所有危险操作为红色语义且有后果说明

AC-05-41  [integration]  关联 BR-05-45, P07（同步派单）
Given 提案 X 推荐 Ada 于 W；在 Morgan 确认前，阿澈经 02 自行认领 W（分派已存在）
When  Morgan PUT resolution {kind:CONFIRM}
Then  02 DispatchAssigner 返回 409 120426，原样返回给 Morgan；X 仍为 PROPOSED 后经 P08 置 EXPIRED(WORK_ITEM)；W 只有一条 ACTIVE EXECUTOR 分派
And   02 对随后的 dispatch.applied 无任何处理（无第二条分派、无第二条 ASSIGN Decision）

AC-05-42  [integration]  关联 P05, §7.2
Given 提案 X requires_human=true，08「派单确认」待办已打开
When  Morgan DISMISS（reason「先等设计稿」）
Then  发布 dispatch.rejected{resolution_kind:DISMISSED}；待办关闭；2 h 内 W 不再自动生成提案
When  另一提案 Y TTL 到期
Then  发布 dispatch.expired{EXPIRED_TTL}；其待办关闭

AC-05-43  [integration]  关联 BR-05-55, C13
Given 项目 P 阶段 PLANNING，W 为 READY
When  派单循环运行
Then  不生成提案
When  Morgan D02 {trigger:MANUAL_REQUEST} 并确认
Then  若 02 拒绝阶段 → 409 120420 原样返回；project.stage_changed{to:ACTIVE} 后 ≤30 s 内为 W 自动生成提案

AC-05-44  [integration]  关联 BR-05-53（AUTONOMOUS 评审派单）
Given P 为 AUTONOMOUS；评审 Agent Rex 持对 P 的有效委托（decision_approve，风险 ≤ MEDIUM）、含 REVIEWER；W（MEDIUM，执行者 Ada）进入 NEEDS_HUMAN_REVIEW
Then  ≤30 s 生成 trigger=REVIEW 提案并自动应用：02 REVIEWER 分派给 Rex；机器收到 agent.review{work_item_id=W}
When  30 min 内无裁决
Then  02 Release(REVIEWER) 后重新评估；第 2 次超时 → 停止派评审，08 NO_ELIGIBLE_AUTHORITY 升级，N-05-12 发给所有者
And   Ada（执行者）即使含 REVIEWER 也不入选；HYBRID 项目同一事件不产生 REVIEW 提案

AC-05-45  [integration]  关联 BR-05-54
Given 开口 PORTAL 配置 triage_agent_id=Cora（ACTIVE·RUNNING）
When  01 发布 intake.received
Then  机器收到 agent.triage{intake_id}；Cora 以自身凭证提交分诊卡
And   Cora 为 STOPPED 时不下发，不重试

AC-05-46  [integration]  关联 BR-05-12, A07（崩溃不取消执行）
Given Ada 执行 E1 RUNNING
When  Ada 进程崩溃（A07）并在 10 s 后自动重启成功
Then  E1 为 INTERRUPTED（AGENT_CRASHED），未被取消、未自动续跑；W 依 02 T19 进入 EXECUTION_FAILED；Ada 进程恢复为 RUNNING 后不会自行重启 E1

AC-05-47  [integration]  关联 BR-05-52(e)
Given M1 上 Ada 执行 W 的 E1；M1 OFFLINE（E1 → INTERRUPTED）
When  5 min 后 M1 仍 OFFLINE
Then  W 仍为 IN_PROGRESS 且保留 Ada 的 ACTIVE 分派时即生成 REDISPATCH 提案（Ada 排除，阶段 ACTIVE 或 ACCEPTANCE_PENDING 均可）；确认派给 Bo（M2）经 02 Reassign 后：E1 → FAILED（SUPERSEDED_BY_RETRY），新执行 branch_reason=RETRY_INFRA、parent=E1、CARRYOVER
And   M1 在 5 min 内恢复 → 放弃重派，不生成提案

AC-05-48  [integration]  关联 H11, H12, BR-05-62（v1.1）
Given 计划 PL 因预算超默认进入 PENDING_APPROVAL
Then  ensemble.plan_created{requires_approval:true}；08 ENSEMBLE_APPROVAL 待办；N-05-11
When  24 h 无人批准
Then  PL → CANCELLED(APPROVAL_EXPIRED)；ensemble.cancelled；待办关闭；W 回到单派
And   另一计划进入 SELECTION_PENDING 后 24 h、48 h、72 h 各提醒一次 N-05-08，第 4 天不再提醒

AC-05-49  [integration]  关联 H04, H10（按执行取消）
Given PL RUNNING 三枝 b1、b2（RUNNING）、b3（SUBMITTED）
When  spent_micros 达到 cap
Then  仅 b1、b2 被 03 CancelExecution 置 STOPPED_BUDGET；b3 的执行与 pending_claim 不受影响；W 的其他执行不受影响
```

---

AC-05-50  [integration]  关联 BR-05-53, #4/#5/#27（评审派单的身份、租约与超时升级）
Given P 为 AUTONOMOUS；Rex 合格（REVIEWER、有效委托）；W 进入 NEEDS_HUMAN_REVIEW
Then  02 REVIEWER 分派 actor=SYSTEM、authority_source=SYSTEM:fleet.review_dispatch、租约 1800 s；W 状态不变（非迁移）
And   本模块未登记 SystemActions("fleet.review_dispatch") 时（夹具）→ Assign 被 08 拒绝 SYSTEM_NOT_REGISTERED，提案不变更
When  25 min 时 lease_reclaim 运行
Then  REVIEWER 分派未被回收
When  30 min 无裁决
Then  同事务 02 Release(REVIEWER) 与 02 WorkItemEscalator.Escalate(kind=REVIEW_SLA, detail.reviewer_kind=AGENT)；02 发布 work_item.escalated{REVIEW_SLA}
And   宿主回 agent.review_ack{accepted:false} → 不等 30 min，立即走同一超时路径

AC-05-51  [integration]  关联 BR-05-56, #3（发布说明起草）
Given 07 发布 delivery.release_notes_requested{delivery_id=D}，起草 Agent Ada ACTIVE·RUNNING
Then  机器收到 agent.release_notes{delivery_id=D, deadline_at=now+30min}；Ada 以自身凭证调 07 #36 提交草稿；command_ack ok
When  起草 Agent 未配置或 STOPPED
Then  不下发命令，立即调 07 ReleaseNotesDrafting.ReportUnavailable(D, NO_DRAFTER / DRAFTER_NOT_RUNNING)
When  30 min 未提交
Then  调 ReportUnavailable(D, TIMEOUT)；本模块不重试（07 BR-07-30b 兜底）

AC-05-52  [integration]  关联 P01, BR-05-55, #2/#9（返工阶段的重派）
Given 项目阶段 ACCEPTANCE_PENDING；W（T26 后返工）IN_PROGRESS，Ada 的 ACTIVE EXECUTOR 分派保留；Ada 所在机器 OFFLINE ≥5 min
Then  生成 trigger=REDISPATCH 提案（不要求 READY、不要求无 ACTIVE 分派）
When  AUTO 应用
Then  经 02 DispatchAssigner.Reassign；ASSIGN Decision authority_source=POLICY:dispatch_auto、detail.dispatch_proposal_id=提案 id
And   阶段 PLANNING 时同一情形不生成提案

## 11. 度量与埋点（不含提示词、消息正文、代码）

| 事件 | 属性 |
|---|---|
| `agent_created` | `runtime_kind, roles[], authorization_mode, start_requested` |
| `agent_start_result` | `result(RUNNING\|START_TIMEOUT\|REJECTED), latency_ms` |
| `agent_crashed` | `reason, auto_restarted(bool), restart_count_in_window` |
| `agent_restart_exhausted` | `runtime_kind` |
| `agent_retired` | `mode, handover_source, successor_kind, items_reassigned, items_released, cleanup_status` |
| `lead_assignment`（PRD §15） | `mode(MANUAL\|AUTO), overridden(bool), rationale_source, rank_of_chosen, guard_reasons[]` |
| `dispatch_proposal_resolved` | `resolution_kind, result, time_to_resolve_ms, trigger` |
| `dispatch_no_eligible` | `top_reason, eligible_count=0, candidate_count` |
| `dispatch_scoring` | `candidate_count, eligible_count, scoring_version, duration_ms` |
| `ensemble_branches`（PRD §15） | `n, distinct_models, chosen(SYSTEM_PICK\|LEAD_PICK\|OTHER\|SYNTHESIS), budget_stopped(bool), cost_micros_bucket` |
| `channel_message_posted` | `kind, author_kind, mention_count, has_attachment, trigger_depth` |
| `mention_delivered` | `target_kind, delivered(bool), delay_ms` |
| `channel_unread_opened` | `unread_bucket` |

运维指标：`fleet_watchdog_failures_total`、`fleet_dispatch_cycle_duration_seconds`、`fleet_lead_plan_timeouts_total`、`collab_projection_failures_total`、`collab_messages_posted_total{kind}`。

---

## 12. 依赖、风险与待决

### 12.1 依赖

| 依赖方 | 需要什么 | 状态 |
|---|---|---|
| 02 | `WorkItemReader`；**同步** `DispatchAssigner{Assign, Reassign, Release}(ctx, tx, …)`（在调用方事务内执行 A1 逻辑并记 ASSIGN Decision，接受 `dispatch_proposal_id`）；`ClaimSubmitter.SubmitOn`（ensemble 选中枝）；字段 `required_runtime`、必需 / 偏好技能标签、`uncertainty_flag`、`require_human_decision`；`work_item.state_changed` 载荷 `infra_retries_exhausted`；02 调 `DispatchProvenance` 填 `DispatchedByActor`；`DispatchAssigner` 支持 `role=REVIEWER`；`ClaimSubmitter.SubmitOn` 携带 `pending_claim`；`WorkItemTransitioner.EnsembleFailed`；`work_item.state_changed` 载荷 `failure_kind` | 00 v1.2 已定为同步；02 不消费 `dispatch.applied`（02 修订落实）；REVIEWER `Assign` 在 NEEDS_HUMAN_REVIEW 上为非迁移并接受 `lease_ttl_seconds`（1800）；`WorkItemEscalator.Escalate(tx, {work_item_id, kind:REVIEW_SLA, detail})`（发布 `work_item.escalated`）；REDISPATCH 经 `Reassign`（T07，允许 CLAIMED / IN_PROGRESS / EXECUTION_FAILED）；`SubmitInput.head_commit` |
| 03 | `ExecutionLauncher.Launch/CancelExecution`（含 `ensemble_group_id`、`max_cost_micros`、`purpose`、ADHOC 时 `project_id`）；Agent CRASHED → 执行 INTERRUPTED（E19b，不取消）；ENSEMBLE 枝不提交声明而存 `pending_claim` 并排队验证；`purpose=HANDOVER` 的 ADHOC 执行豁免 E27；`ArtifactKind=HANDOVER_DOC` 与系统生成 Artifact（无执行来源）；A08 对比数据的服务方法；`EvidenceReader.VerificationSummary` | 需 03 对齐 |
| 04 | `MachineDirectory`；`ServerBatch.agent_commands[]` 与 poll `agents[]`、M→S `agent.*` 消息（§6.6）；`machine.state_changed`、`machine.runtimes_changed` | 需 04 加入（CCR-05-06）；04 只承载传输帧并引用本规格 §6.6 作为 Agent 消息唯一定义（含 `agent.release_notes`、`agent.review_ack`、`agent.workspace_result`） |
| 06 | `SkillCatalog.ForAgent`（返回技能 `tags`）；`SkillMounter.MountOn(tx)`；`MemoryPolicyValidator`；`Retriever.Search(purpose=LEAD)`；消费 `agent.retired` 冻结 AGENT scope；记忆 Tab 组件 | `ForAgent` 已有；`MountOn`、tags、Validator 需 06 增加（CCR-05-07） |
| 07 | `ProjectReader`（lead、policy、ensemble 上限、预算剩余）、`ProjectGate.CanAutoDispatch`、#4 编辑；建项目事务内调用 `ChannelProvisioner.CreateDefaultOn`；`project.stage_changed` / `project.mode_changed`；项目改名事件以同步默认频道名 | 已对齐（07 §6 / BR-07-07）；发布 `delivery.release_notes_requested{delivery_id, drafter_agent_id?, note?}` 与 `project.members_changed`；提供 `ReleaseNotesDrafting.ReportUnavailable(tx, {delivery_id, reason})` 并承担兜底（BR-07-30b）；#36 接受起草 Agent 自身凭证提交；BR-07-12 阶段闸门与本规格 BR-05-55 同为 {ACTIVE, ACCEPTANCE_PENDING} |
| 08 | `AuthorityChecker`（`Check` 含 `DispatchedByActor` 事实；`ListEligible`）、`PolicyRegistry`（本模块贡献 `dispatch_auto` / `ensemble_branch` / `review_dispatch`）、`DelegationRevoker.RevokeAllForOn(tx, principal)`、我的裁决分类「派单确认」「并行求解待批准」「并行求解待选择」 | 需 08 对齐；`SystemActions.Register("fleet.review_dispatch","work_item_assign")`；ENSEMBLE_APPROVAL 以 `ensemble.plan_created{requires_approval:true}` 触发、ENSEMBLE_SELECTION 以 `ensemble.compared{requires_human:true}` 触发；登记本模块 reason_code（NOT_MEMBER、DIRECT_READ_ONLY、SELF_MANAGE 等） |
| 09 | JobQueue、Outbox、SSE、Notifier、SecretSealer | 平台 |
| 底座 | identity `ServiceAccountProvisioner`（事务内创建 / 停用 / 签发 / 吊销凭证，CR-04-08）；`PrincipalAvatarSetter.SetOn(tx)`（服务账号头像）；core `EntityRoleBindingStore{PutOn, RemoveOn, ListByEntity}`（RoleGrantStore 已有 `RemoveEntityRoleBinding`，需补 Put / List）；authn step-up 目的 `agent_full_auto`、`agent_forced_retire` | 底座新增 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 主导 Agent 被频道文本注入诱导派单 | 频道请求一律需人确认（BR-05-51）；LLM 只能在预过滤集合中选；提案全量可复算 |
| AUTO 派单在预算 / 风险边界失控 | G1–G9 护栏 + 07 `ProjectGate` + 每小时 20 次上限；一键接管（08）即切 MANUAL |
| 崩溃循环刷屏与成本 | 30 min / 3 次上限 + 退避；attention 事件不折叠但通知去重 |
| 评分依赖的 FPA 冷启动偏差 | 贝叶斯先验（权重 4）+ 多样性分量；样本 <5 在 UI 标注 |
| 交接文档质量依赖 Agent | 10 min 超时回退系统摘要；文档为 Artifact 可审计 |
| 频道消息量增长 | 序号索引 + 虚拟滚动 + 分页 ≤100；保留策略 v2 |
| Agent 凭证在机器侧泄露 | 仅宿主进程持有、本地加密、换绑 / 退役即吊销；执行环境不注入 |

### 12.3 待决（附建议）

| # | 问题 | 建议 |
|---|---|---|
| Q-05-01 | 退役 Agent 的规则型记忆是否可转给接手 Agent | 默认冻结；向导提供「复制规则型记忆给接手 Agent」，复制项经 06 人审后生效（防投毒） |
| Q-05-02 | 主导 Agent 规划调用成本计入哪里 | 计入项目预算（07 成本事件），不计个人 CPA；需 07 接受 `source=LEAD_PLANNING` 的成本行 |
| Q-05-03 | REVIEWER 派单（评审认领）是否也由主导 Agent 提案 | **已决**：AUTONOMOUS 项目在 MVP 即派评审 Agent（BR-05-53，系统评分，复用同一公式，fpa 换为评审一致率）；HYBRID 不派评审，由「我的裁决」承担 |
| Q-05-04 | 频道消息保留期 | v2：项目关闭 365 天后清理正文、保留元数据与审计；需法务确认 |
| Q-05-05 | 评分权重是否允许团队调整 | MVP 固定（可复算、便于比较）；v1.1 允许项目级覆盖并在 `scoring_version` 中体现 |
| Q-05-06 | 是否开放人 ↔ Agent 私信 | 不开放；对 Agent 的请求应在项目频道中可见、可审计 |

---

## 13. 契约变更请求

> 状态：CCR-05-01、03、04、05、08、10 已并入 00 v1.2；CCR-05-09 并入 00 v1.3 §7.2；CCR-05-02 已处理（00 v1.3 §3：模块内枚举留在本规格，不进 §5）；CCR-05-06 由 04 通道扩展承接；CCR-05-07 由 06 承接。

| 编号 | 对 00 的变更 | 理由 |
|---|---|---|
| CCR-05-01 | §3 时间列改为 PG `TIMESTAMPTZ(3)` / MySQL `DATETIME(3)` / SQLite `DATETIME`；乐观锁经 `ETag` / `If-Match`（缺 428） | 与 `backend-module` 门禁一致；同 CR-03-01 / CR-03-08 / CCR-07-01 |
| CCR-05-02 | §5 新增枚举（所有者 05）：`AgentProcessStatus`（NONE, STARTING, RUNNING, STOPPING, EXITED, UNREACHABLE）、`AgentProcessEventKind`、`RetirementMode`（NORMAL, FORCED）、`RetirementState`（IN_PROGRESS, COMPLETED, CANCELLED, FAILED）、`RetirementStep`（PRECHECK, HANDOVER, SUCCESSOR, REASSIGN, CLEANUP, REVOKE, DONE）、`DispatchProposalState`（PROPOSED, APPLIED, OVERRIDDEN, EXPIRED）、`DispatchResolution`、`DispatchTrigger`、`DispatchResult`、`RationaleSource`、`EnsembleTrigger`、`EnsembleState`、`EnsembleBranchOutcome`、`ChannelKind`（PROJECT_DEFAULT, CUSTOM, AGENT_DIRECT）、`ChannelMemberRole`、`MessageKind`（TEXT, EVENT, DECISION_CARD, DISPATCH_CARD） | 00 §3 要求枚举列取自 §5 |
| CCR-05-03 | §9 错误码：fleet 段内划分 04 = 140000–141999、05 = 142000–143999、144000–144999 保留；通用码 1404xx 由 04 定义、05 共用 | 同一模块两份规格避免撞码 |
| CCR-05-04 | §8 新增 02 提供的 `DispatchAssigner{Assign, Reassign, Release}`（调用方事务内、同步、记 ASSIGN Decision、接受 `dispatch_proposal_id`）与 `ClaimSubmitter.SubmitOn`；§7.1 `dispatch.applied` 的 delivery 消费改为「已落库则幂等忽略」 | 提案状态与分派必须同事务一致；异步事件无法把 02 的拒绝（如 120426）返回给确认者 |
| CCR-05-05 | §8 新增 fleet `DispatchProvenance`（使用者 08）、collab `ChannelProvisioner`（使用者 07）、collab `ChannelPoster`（使用者 fleet） | 职责分离判定与原子建项目需要窄接口 |
| CCR-05-06 | 04 通道扩展：`ServerBatch.agent_commands[]`、poll 请求 `agents[]`、M→S `agent.status` / `agent.lead_plan_result` / `agent.command_ack`、同步请求 `agent.workspace_*`（§6.6） | Agent 进程托管于机器（B X-2 裁决），控制面只经通道控制 |
| CCR-05-07 | §8 06 增加 `SkillMounter.MountOn(tx)`、`MemoryPolicyValidator`，`SkillCatalog.ForAgent` 返回 `tags` | Agent 原子创建与技能标签评分 |
| CCR-05-08 | §7.1 新增事件：`agent.created`、`dispatch.expired`、`ensemble.started`、`ensemble.selected`（发布者 fleet）；`channel.created`、`channel.archived`、`message.posted`（发布者 collab，载荷不含正文） | 投影、通知与 Agent 投递需要；00 现表缺失 |
| CCR-05-09 | §7.2 必须审计增加：派单策略下的自动生效 / 人工改派 / 驳回、Ensemble 启动 / 预算停止 / 选择、Agent 换绑与凭证签发 / 吊销 | PRD §10.6「为什么派给它永远可追溯」 |
| CCR-05-10 | §6.3 明确：AUTO 派单的 `authority_source = POLICY:dispatch_auto`、提案 id 放 `detail.dispatch_proposal_id`（**已按 00 v1.5 定稿**；原提议 `POLICY:<proposal_id>` 撤回）；人确认 / 改派使用人自身的 ROLE / DELEGATION 来源 | 00 只给出 `POLICY:<rule_id>` 形式，需明确 rule_id 指向 |

**第二轮复审（00 v1.5）**：#2 REDISPATCH 允许非 READY 并经 `Reassign`；#3 BR-05-56 发布说明起草（`agent.release_notes`）；#4/#5/#27 评审派单 SYSTEM 身份、`lease_ttl_seconds=1800`、超时经 02 `WorkItemEscalator` 升级；#9 阶段 ∈ {ACTIVE, ACCEPTANCE_PENDING}；#14 `pending_claim.head_commit`；#18 §6.6 为 Agent 通道唯一定义；#22 `ensemble.compared.requires_human`；#24 立即消费 `project.members_changed`；#26 `POLICY:dispatch_auto` + `detail.dispatch_proposal_id`（CCR-05-10 按此定稿）；新增 AC-05-50..52。
