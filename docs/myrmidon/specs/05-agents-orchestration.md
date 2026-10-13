# 05 · Agent 成员、主管 Agent 编排与频道

> **现行范围（2026-10-12，D125–D129）**：用户已批准需求审计 E1–E4；以 [00 共同契约](00-conventions.md) §16 与本文 §14 为准。原型全量展示，开发 MVP 仅单项目软件闭环；其余按现行分期延后。下文历史门户 / 跨项目菜单 / 独立拆解模板 / 旧 MVP 标记如冲突均已退休，不构成实现承诺。

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
| 主管 Agent 编排 | 派单候选预过滤 + 确定性评分、主管 Agent LLM 提案、提案生命周期、MANUAL / AUTO 护栏、人工改派、无候选、失效重派、饥饿与抖动上限 |
| Heavy 并行求解（v1.1） | 触发规则、N 枝选择（模型多样性）、预算上限、对比报告、择优 / 合成、落选枝归档 |
| 频道（collab） | 项目默认频道、自建频道、成员、消息四类、@mention、Agent 回复与「@Agent 请做 X」、事件折叠、未读、编辑 / 撤回、Agent 私信、成员私信与会话空间（D118）、钩子（v1.1）、桥接（v2）、保留、上限、实时 |
| Agent 运行时协议（D120） | 每个 Agent 的持久收件箱（唯一事实来源）、不带内容的唤醒提示（通道提示流 / 轮询 / 签名 webhook）、游标确认、过期上下文写入返回 `interrupted`；Agent 操作清单（schema、副作用、幂等、所需权限、可否委托、是否需人确认）与结果 `next`；五态活动状态与三种重置；工作目录与记忆查看范围；锚定线程的提醒（循环契约由 10 定义） |
| 团队工具目录（D120） | 团队级 MCP 服务器与技能目录：条目、不可变版本与内容哈希、推荐预设、授予（项目 / 角色 / Agent，默认无）、MCP 凭据托管（09 封装，经平台中继，凭据不下发机器）、按执行解析有效工具集（供 03 记录、04 分发）、本机自带项的识别与标记；技能内容与版本仍以 06 技能库为准 |

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
| US-05-08 | 作为交付负责人，我要为项目指定主管 Agent 与派单策略（MANUAL / AUTO），让它把工作项分给最合适的 Agent（可使用不同模型） | MVP |
| US-05-09 | 作为交付负责人，我要看到每条派单提案的候选评分与理由，一键确认或改派 | MVP |
| US-05-10 | 作为交付负责人，我要在没有合适 Agent、Agent 崩溃或机器离线时得到明确提示与重派 | MVP |
| US-05-11 | 作为交付负责人，我要对高风险 / 高不确定工作项启动 Heavy 并行求解，在预算内比较多枝结果后择优 | v1.1 |
| US-05-12 | 作为团队成员，我要在项目频道里与人和 Agent 协作，@成员，看到折叠的执行事件与不折叠的裁决卡片 | MVP |
| US-05-13 | 作为交付负责人，我要在频道里「@主管Agent 把 W 派给 Ada」，得到一张需要我确认的派单卡片 | MVP |
| US-05-14 | 作为团队所有者，我要只读查看 Agent 之间的私信 | MVP |
| US-05-15 | 作为团队所有者，我要为频道配置消息钩子（向外推送） | v1.1 |
| US-05-16 | 作为团队所有者，我要把频道桥接到外部 IM / MQTT / WebSocket / HTTP | v2 |
| US-05-17 | 作为主管 Agent，我要在团队所有者给定的权限、名额与预算内自行创建执行者 / 协调员 Agent，以便按工作量扩充人手（D110） | MVP |
| US-05-18 | 作为团队所有者，我要设置自建授权（名额、每日上限、预算、可用角色 / 机器 / 模型、有效期、是否需审批），并随时撤销 | MVP |
| US-05-19 | 作为交付负责人，我要在成员列表中一眼区分人工创建与 AI 创建的 Agent，并按来源、主管、孤儿状态筛选 | MVP |
| US-05-20 | 作为团队所有者，我要在主管失效后收编或退役它创建的 Agent，避免无人负责的 Agent 继续消耗预算 | MVP |

---


### 2.x 用户故事增补（D112 / D113，00 v2.0 §15）

| ID | 故事 | 版本 |
|---|---|---|
| US-05-21 | 作为交付负责人，我要交付执行通过自动检查后，系统自动派测试 Agent、再派产品验收 Agent，以便内部问题在内部解决、不打扰人 | MVP |
| US-05-22 | 作为团队所有者，我要内部验收 Agent 优先使用与执行者不同的模型家族，以便避免「自己审自己」 | MVP |
| US-05-23 | 作为纯人工团队的成员，我要 Agent 只做助理（分诊、拆解草稿、纪要转任务草稿、状态与报告草稿、风险与对账提示），以便人仍是唯一的执行者与验收者 | MVP |
| US-05-24 | 作为团队所有者，我要编制授权可以允许主管创建测试 Agent，但产品验收 Agent 只能由人创建，以便验收者的独立性有保证 | MVP |

### 2.y 用户故事增补（D118）

| ID | 故事 | 版本 |
|---|---|---|
| US-05-25 | 作为团队成员，我要在一个全宽的会话空间里看到提及与线程、按项目分组的频道、私信与运行中的执行会话，以便不在页面间来回跳转 | MVP |
| US-05-26 | 作为团队成员，我要和同事、和某个 Agent 私信，且人与人的私信只有我们两人能看 | MVP |
| US-05-27 | 作为团队所有者，我要能只读查看我负责的 Agent 与人之间的私信，以便对 Agent 的行为负责 | MVP |

### 2.z 用户故事增补（D120：Agent 运行时协议、操作清单、生命周期与提醒）

| ID | 故事 | 版本 |
|---|---|---|
| US-05-28 | 作为 Agent（宿主进程），我要从一个持久收件箱按游标读取提及、私信、派单、评审反馈、提醒与交互结果，断线或重启后从上次确认处继续，以便不漏事、不重复做 | MVP |
| US-05-29 | 作为交付负责人，我要 Agent 在会话或工作项已有更新事件时写入被拒为 `interrupted` 并带回更新内容，以便 Agent 不会基于过期上下文抢认领、回复或改状态 | MVP |
| US-05-30 | 作为 Agent，我要读取一份机器可读的操作清单（输入 schema、副作用、幂等键规则、所需权限、可否委托、是否需人确认），并在每次调用结果中拿到建议的下一步，以便正确调用平台 | MVP |
| US-05-31 | 作为团队所有者，我要在「治理 › Agent 操作清单」看到全部 Agent 可调用操作及每个 Agent 在某项目中的有效能力，以便知道它能做什么、为什么被拒 | MVP |
| US-05-32 | 作为 Agent 的负责人，我要看到它在线 / 思考中 / 工作中 / 出错 / 离线的实时状态，并能重启、重置会话或完全重置（完全重置需重新认证），以便处理卡住或被污染的 Agent | MVP |
| US-05-33 | 作为 Agent 的负责人或团队所有者，我要查看它的工作目录与记忆，而其他成员看不到，以便既能排查又不泄露 Agent 的内部材料 | MVP |
| US-05-34 | 作为 Agent，我要为自己设锚定在某个线程上的提醒（一次性或周期），到点进入收件箱，以便按约定回到那段对话继续工作；循环契约（10）也经提醒驱动 | MVP |
| US-05-35 | 作为团队所有者，我要为不经机器程序托管的 Agent 宿主配置签名 webhook 唤醒 | v1.1 |
| US-05-36 | 作为团队所有者，我要在「团队 › 工具与技能」维护团队的 MCP 服务器（可从推荐预设开始）与技能目录，每个条目有版本和内容哈希，以便所有 Agent 用同一套经过审核的工具 | MVP |
| US-05-37 | 作为团队所有者，我要把目录条目授予指定的项目、角色或 Agent（默认谁都不能用），以便工具按需开放、撤销即时生效 | MVP |
| US-05-38 | 作为团队所有者，我要由平台保管 MCP 服务器的凭据并能轮换，机器上永远拿不到明文，每次经平台的工具调用都有审计 | MVP |
| US-05-39 | 作为 Agent 的负责人，我要在 Agent 详情「技能」标签看到每个技能 / MCP 的来源（团队目录 vX / 本机自带）与是否可用，以便知道它实际带着什么工具在干活 | MVP |

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
| lead_agent_id | ID | Y | | 生成时的主管 Agent（空 = 纯系统评分） |
| result | ENUM | N | | `DispatchResult`：CANDIDATE / NO_ELIGIBLE |
| recommended_agent_id | ID | Y | | 最终推荐（LLM 选择或系统 top-1） |
| system_top_agent_id | ID | Y | | 评分第一 |
| rationale | STR(2000) | Y | | 理由文本（会进入 ASSIGN Decision 的 rationale） |
| rationale_source | ENUM | N | | `RationaleSource`：LEAD / SYSTEM |
| requires_human | BOOL | N | | 护栏判定结果 |
| guard_reasons | JSON | N | `[]` | 触发的护栏码（G1–G9） |
| scoring_version | STR(16) | N | `s1` | 公式版本，保证可复算 |
| inputs_digest | STR(64) | N | | 评分输入快照的 SHA-256 |
| planning_cost_micros | BIGINT | N | 0 | 主管 Agent 规划调用成本 |
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
| project_id | ID | Y | | `kind='DIRECT'` ⇔ 为空 | 项目实体（DataScoped）；DIRECT（D118）为团队级，恒为空 |
| kind | ENUM | N | | `ChannelKind`：PROJECT_DEFAULT / CUSTOM / AGENT_DIRECT / DIRECT（D118：人↔人、人↔Agent 成员私信，BR-05-114） | |
| name | STR(40) | N | | BR-05-80 | DEFAULT 名 = 项目名截断（随 07 改名同步）；AGENT_DIRECT 名 = `dm-<agentA>-<agentB>`；DIRECT 名固定为 `dm`（显示名由客户端按对方渲染） |
| topic | STR(200) | Y | | | |
| direct_pair_key | STR(48) | Y | | `<小id>:<大id>` | 部分唯一 `(project_id, direct_pair_key) WHERE kind='AGENT_DIRECT'`；DIRECT 取两名参与者的 principal_id，部分唯一 `(tenant_id, direct_pair_key) WHERE kind='DIRECT'` |
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
| project_id | ID | Y | | | 冗余，行过滤；DIRECT 频道的消息为空 |
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

### 3.11 `fleet_agents` 自建谱系列（D110）

在 §3.2 基础上新增（三方言按 §3.1 记号映射）：

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| origin | ENUM | N | `HUMAN_CREATED` | `AgentOrigin`：HUMAN_CREATED / AGENT_SPAWNED（模块共享枚举，见 §13） | 创建来源；**收编后不变**（谱系可追溯） |
| created_by_principal_id | ID | N | | | 创建者：人类 principal，或主管 Agent 的服务账号 principal |
| parent_agent_id | ID | Y | | AGENT_SPAWNED 时必填；所指 Agent 须含 LEAD 角色 | 主管 Agent |
| owner_principal_id | ID | N | | 必须是人类 principal | **负责人类**（D111）：人工创建 = 创建者；主管创建 = 编制授权授予人；收编后 = 收编人；`agent_manage` 可改。用于计划 / 报告确认人、通知与升级的人类落点 |
| spawn_grant_id | ID | Y | | AGENT_SPAWNED 时必填（收编后保留历史值） | 08 自建授权 |
| spawn_request_id | ID | Y | | | 对应 §3.12 请求 |
| spawn_reason | STR(2000) | Y | | AGENT_SPAWNED 时必填，≥10 字 | 主管给出的创建理由 |
| expires_at | TS | Y | | AGENT_SPAWNED 且未收编时 = 创建时间 + 授权 `agent_ttl` | 到期自动正常退役（BR-05-104） |
| idle_since | TS | Y | | | 最近一次无 ACTIVE 分派的起点；用于闲置退役 |
| adopted_by | ID | Y | | 仅 HUMAN | 收编人 |
| adopted_at | TS | Y | | | |
| orphaned_at | TS | Y | | | 成为孤儿的时间；收编 / 退役后保留历史值 |
| orphan_reason | ENUM | Y | | SUPERVISOR_RETIRED / SUPERVISOR_CRASHED / GRANT_REVOKED / GRANT_EXPIRED / TAKEOVER | |

派生显示状态 `lineage_status`（不落库）：`HUMAN_CREATED` → 人工创建；`AGENT_SPAWNED ∧ adopted_at IS NULL ∧ orphaned_at IS NULL` → 主管创建（SPAWNED）；`adopted_at IS NOT NULL` → 已收编（ADOPTED）；`orphaned_at IS NOT NULL ∧ adopted_at IS NULL ∧ state≠RETIRED` → 孤儿（ORPHANED）。

索引：`(tenant_id, origin, state)`；`(tenant_id, parent_agent_id, state)`；`(tenant_id, spawn_grant_id, state)`；`(expires_at)`；`(tenant_id, orphaned_at)`。

CHECK：`origin='HUMAN_CREATED' OR (parent_agent_id IS NOT NULL AND spawn_grant_id IS NOT NULL AND spawn_reason IS NOT NULL)`。

### 3.12 `fleet_agent_spawn_requests`（自建请求，D110）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | 雪花 |
| tenant_id | ID | N | | | |
| project_id | ID | Y | | 授权限定项目时必填且 ∈ 授权 `projects` | 新 Agent 加入的项目 |
| supervisor_agent_id | ID | N | | 须为 LEAD 且非未收编的 AGENT_SPAWNED | 发起的主管 |
| grant_id | ID | N | | 发起时 08 `SpawnGrantReader.Active` 返回的授权 | |
| requested_spec | JSON | N | | Schema 见下 | 新 Agent 规格 |
| reason | STR(2000) | N | | ≥10 字 | 为什么需要这个 Agent |
| approval_mode | ENUM | N | | REQUIRE_APPROVAL / AUTO（发起时的有效值快照，BR-05-99） | |
| state | ENUM | N | `PROPOSED` | `SpawnRequestState`：PROPOSED / APPROVED / REJECTED / CREATED / EXPIRED / FAILED | §4.6 |
| quota_reservation_id | ID | Y | | | 08 `SpawnGrantUsage.Reserve` 返回 |
| decided_by | ID | Y | | 仅 HUMAN | |
| decided_at | TS | Y | | | |
| decision_note | STR(2000) | Y | | 拒绝时必填 ≥10 字 | |
| created_agent_id | ID | Y | | | 成功创建的 Agent |
| failure_code | STR(32) | Y | | | FAILED 时的错误码 |
| source_command_id | STR(64) | Y | | UNIQUE (tenant_id, supervisor_agent_id, source_command_id)（可空列唯一，三方言允许多个 NULL） | 经机器通道发起时的命令 id，去重 |
| expires_at | TS | N | | 创建时间 + 24 h | PROPOSED 超时 |
| version | INT | N | 1 | | 乐观锁（ETag） |

另含通用列 `created_at`、`updated_at`、`created_by`、`updated_by`（§3.1）。索引：`(tenant_id, state, created_at)`；`(supervisor_agent_id, created_at)`；`(grant_id, state)`；`(state, expires_at)`。

`requested_spec` JSON Schema（要点）：`{"type":"object","required":["name","role","machine_id","runtime_kind","model_id","system_prompt"],"properties":{"name":{"type":"string","maxLength":64},"role":{"enum":["EXECUTOR","COORDINATOR"]},"machine_id":{"type":"string"},"runtime_kind":{"enum":["CLAUDE_CODE","CODEX"]},"model_id":{"type":"string","maxLength":160},"reasoning_effort":{"type":"string","maxLength":16},"system_prompt":{"type":"string","maxLength":32768},"persona":{"type":"string","maxLength":2000},"skill_ids":{"type":"array","maxItems":20},"mcp_allowlist":{"type":"array","maxItems":32},"capability_tags":{"type":"array","maxItems":20},"max_risk":{"enum":["LOW","MEDIUM"]}},"additionalProperties":false}`。不接受 `authorization_mode`（固定 ON_REQUEST），角色不得为 LEAD / REVIEWER。


### 3.x 数据增补（D112 / D113，00 v2.0 §15）

- `AgentRole` 增加 `TESTER`（测试 Agent）、`ACCEPTOR`（产品验收 Agent）、`ASSISTANT`（纯人工模式助理）；`fleet_agents.roles` 校验：`ACCEPTOR` 不可与 `EXECUTOR` 同时出现在同一 Agent 上；`ASSISTANT` 不可与 `EXECUTOR` / `TESTER` / `ACCEPTOR` 同时出现。
- `fleet_agents` 增加 `model_family STR(32) NOT NULL`：由绑定的 runtime × 模型经 04 模型目录解析（如 `anthropic`、`openai`、`google`、`local:<name>`），重新绑定机器 / 模型时更新；派单与内部验收的跨家族判定以执行时快照为准（03 `execution_executions.model_family`）。
- 编制授权（08）`allowed_roles` 可包含 `TESTER`；**`ACCEPTOR` 只能由人类创建**（主管创建请求中出现 ACCEPTOR → 422 **142040**）；`LEAD` / `REVIEWER` 仍不可由主管创建。
- 派单提案 `trigger` 增加 `INTERNAL_QA`（`role ∈ {TESTER, ACCEPTOR}`，`requires_human=false`，SYSTEM 自动应用）；提案新增字段 `subject_execution_id`、`family_fallback BOOL`。

### 3.z 数据增补（D120）

三方言按 §3.1 记号映射；通用列同 §3.1（下文不再重复）。

**`fleet_agent_inbox_events`**（Agent 收件箱，只追加；唯一事实来源，BR-05-120）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | 雪花 |
| agent_id | ID | N | | | |
| seq | BIGINT | N | | UNIQUE(agent_id, seq) | 每 Agent 单调，从 1 起；在 `fleet_agent_live_states` 行锁内分配 |
| kind | ENUM | N | | `InboxEventKind`（§3.z 枚举） | |
| project_id | ID | Y | | | 行过滤；团队级私信为空 |
| subject_kind / subject_id | STR(32) / ID | N | | | 事件所属对象：`channel` / `thread` / `work_item` / `execution` / `interaction` / `comment_thread` / `dispatch_proposal` / `reminder` / `loop_run` / `intake` / `delivery` / `report` / `plan` |
| stream_key | STR(64) | Y | | `^(channel\|thread\|work_item\|comment_thread):[0-9]{1,19}$` | 该事件所在的写入流（BR-05-127）；无写入流的事件为空 |
| stream_seq | BIGINT | Y | | | 事件发生时该流的序号；Agent 写入时作为 `seen_seq` 传回 |
| ref_kind / ref_id | STR(32) / ID | Y | | | 具体对象（消息、裁决、批注、交互、提醒触发等） |
| actor_kind / actor_id | ENUM / ID | Y | | 00 §4 | 引起事件的主体；SYSTEM 为空 |
| title | STR(200) | N | | | 一行说明（不含代码、消息正文与工具入参） |
| payload | JSON | N | `{}` | ≤8 KB，按 kind 的 Schema | 只含 ID、序号、截止时间等引用；正文经各模块读接口取得 |
| source_event_id | STR(64) | N | | UNIQUE(agent_id, source_event_id) | 投影幂等键（领域事件 `event_id`，或 `reminder:<id>:<fire_no>`、`command:<command_id>`） |
| expires_at | TS | N | | 创建 + 30 天 | 保留期（BR-05-125） |

索引：`(agent_id, seq)`；`(tenant_id, agent_id, kind, seq DESC)`；`(expires_at)`。不设 `updated_*` / `version`（只追加）。

**`fleet_agent_live_states`**（Agent 运行态，每 Agent 1 行；非聚合根，无乐观锁，避免高频上报与 `fleet_agents.version` 冲突）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| agent_id | ID | N | | PK | 与 `fleet_agents` 同时创建（BR-05-02 事务内） |
| inbox_head_seq | BIGINT | N | 0 | | 已分配的最大 seq |
| inbox_ack_seq | BIGINT | N | 0 | 0 ≤ ack ≤ head，只增不减 | 游标（BR-05-121） |
| inbox_acked_at | TS | Y | | | |
| last_wake_seq / last_wake_at | BIGINT / TS | Y | | | 最近一次发出的唤醒提示 |
| wake_mode | ENUM | N | `CHANNEL` | `WakeMode`：CHANNEL / POLL / WEBHOOK（v1.1） | BR-05-122 |
| wake_webhook_url | STR(512) | Y | | 仅 https，非内网地址 | v1.1 |
| wake_webhook_secret_sealed | STR(512) | Y | | | 09 `SecretSealer`；只在创建时返回一次 |
| activity_status | ENUM | N | `OFFLINE` | `AgentActivityStatus` | BR-05-134 |
| activity_changed_at | TS | Y | | | |
| activity_detail | JSON | Y | | `{execution_id?, error_code?≤64, since?}` | 不含提示词与输出 |
| session_epoch | INT | N | 1 | | 会话重置 / 完全重置 +1；宿主据此丢弃旧会话（BR-05-135） |
| workspace_epoch | INT | N | 1 | | 完全重置 +1 |

**`fleet_agent_resets`**（重置记录，只追加 + 状态列）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| agent_id | ID | N | | 部分唯一 `(agent_id) WHERE state='REQUESTED'` | 同时只有一个进行中的重置 |
| kind | ENUM | N | | `AgentResetKind`：SESSION / FULL | 重启（RESTART）沿用 F06 与 `fleet_agent_process_events`，不入本表 |
| state | ENUM | N | `REQUESTED` | `AgentResetState`：REQUESTED / DONE / FAILED / TIMED_OUT | §4.z R-状态 |
| reason | STR(2000) | N | | ≥10 字 | |
| requested_by | ID | N | | 仅 HUMAN | |
| reauth_at | TS | Y | | FULL 必填 | step-up `agent_full_reset` |
| command_id | STR(64) | N | | | `agent.reset` 命令 |
| removed_bytes | BIGINT | Y | | | FULL 时机器回报 |
| failure_code | STR(32) | Y | | | |
| completed_at | TS | Y | | | |

索引：`(tenant_id, agent_id, created_at DESC)`。

**`fleet_agent_reminders`**（Agent 提醒，`version` 乐观锁）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| agent_id | ID | N | | | 被提醒的 Agent |
| project_id | ID | Y | | | 锚点所属项目（团队级私信为空） |
| anchor_kind | ENUM | N | | `ReminderAnchorKind`：THREAD / WORK_ITEM / LOOP_CONTRACT | |
| channel_id / thread_root_message_id | ID / ID | Y | | THREAD 时必填；LOOP_CONTRACT 时取契约的锚定线程 | 「锚定在线程上」：到点事件指向该线程 |
| work_item_id | ID | Y | | WORK_ITEM 时必填 | |
| loop_contract_id | ID | Y | | LOOP_CONTRACT 时必填；部分唯一 `(loop_contract_id) WHERE state IN ('ACTIVE','PAUSED')` | 10 循环契约 |
| title | STR(200) | N | | | |
| note | STR(2000) | Y | | | Agent 写给未来自己的说明（经收件箱读取） |
| schedule_kind | ENUM | N | | `ReminderScheduleKind`：ONCE / INTERVAL / PERIOD（按 10 `PeriodKind` 的报告时刻）；CRON 待 00 F12 批准后启用 | |
| fire_at | TS | Y | | ONCE 必填 | |
| interval_minutes | INT | Y | | INTERVAL：15–10080 | |
| period_kind | ENUM | Y | | PERIOD 必填，`PeriodKind`（00 §5） | 时刻取 10 `planning_settings` 的 `*_report_at` 与团队时区 |
| next_fire_at | TS | Y | | | 终态为空 |
| last_fired_at / fire_count | TS / INT | Y / N | / 0 | | |
| max_fires | INT | Y | | 1–1000 | 达到后 DONE |
| state | ENUM | N | `ACTIVE` | `ReminderState`：ACTIVE / PAUSED / DONE / CANCELLED | §4.z |
| created_by_kind / created_by | ENUM / ID | N / Y | | AGENT（本人）/ HUMAN / SYSTEM（10 循环契约） | |
| version | INT | N | 1 | | |

索引：`(state, next_fire_at)`（触发扫描）；`(tenant_id, agent_id, state)`；`(channel_id, thread_root_message_id)`。

**`fleet_operation_manifest_versions`**（Agent 操作清单的发布版本；系统级只读数据，`tenant_id=0`，同 00 §3「基础设施表」中一方系统数据的处理方式，见 §13 CCR-05-11）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| manifest_version | STR(32) | N | | UNIQUE；语义化版本 `MAJOR.MINOR.PATCH` | BR-05-129 |
| sha256 | STR(64) | N | | UNIQUE | 规范化文档（JCS）的哈希，作为 ETag |
| document | JSON | N | | Schema S-M1（§6.z），≤2 MB | 完整清单 |
| operation_count | INT | N | | | |
| published_at | TS | N | | | |
| supersedes_version | STR(32) | Y | | | |

**`fleet_tool_entries`**（团队工具目录条目，`version` 乐观锁；BR-05-141）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| kind | ENUM | N | | `ToolEntryKind`：MCP_SERVER / SKILL | |
| entry_key | STR(64) | N | | `^[a-z0-9][a-z0-9-]{1,63}$`；UNIQUE(tenant_id, entry_key) | 运行时中的名称（MCP server 名 / 技能目录名）；SKILL 条目 = 06 `skill_key` |
| title / description | STR(200) / STR(1024) | N / Y | | | |
| skill_id | ID | Y | | SKILL 必填；UNIQUE(tenant_id, skill_id) | 06 `knowledge_skills.id`；版本与内容以 06 为准（06 BR-06-67） |
| transport | ENUM | Y | | MCP_SERVER 必填：`McpTransport` | |
| credential_mode | ENUM | N | `NONE` | `ToolCredentialMode`；STDIO_PACKAGE 在 MVP 只允许 NONE（BR-05-142） | |
| current_version_no | INT | Y | | MCP_SERVER：最新 ACTIVE 版本；SKILL 为空（取 06 当前版本） | |
| preset_key | STR(64) | Y | | | 由推荐预设创建时记录（BR-05-149） |
| state | ENUM | N | `DRAFT` | `ToolEntryState`；SKILL 条目创建即 ACTIVE | §4.z TE-状态 |
| upstream_auth_failures | INT | N | 0 | | 中继连续收到上游 401 / 403 的次数，成功即清零（BR-05-147） |
| version | INT | N | 1 | | |

索引：`(tenant_id, kind, state)`。

**`fleet_tool_entry_versions`**（MCP 条目的不可变版本，只追加 + 状态列；SKILL 条目的版本即 06 技能版本，不在本表）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| entry_id | ID | N | | UNIQUE(entry_id, version_no) | |
| version_no | INT | N | | 从 1 起 | |
| config | JSON | N | | Schema S-T1（§6.z），≤16 KB，不含任何机密 | |
| content_sha256 | STR(64) | N | | = sha256(JCS(config)) | 版本内容哈希；执行证据记录此值（03 BR-03-147） |
| change_note | STR(500) | Y | | | |
| state | ENUM | N | `ACTIVE` | `ToolVersionState`：ACTIVE / SUPERSEDED / REVOKED | 新版本发布 → 旧 ACTIVE 变 SUPERSEDED；REVOKED 由人工撤回（BR-05-142） |
| published_by / published_at | ID / TS | N | | | |

**`fleet_tool_credentials`**（每个 MCP 条目至多 1 行；明文永不出库、不下发机器）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| entry_id | ID | N | | PK | |
| sealed | STR(4096) | N | | 09 `SecretSealer` purpose `fleet.tool_credential`，关联数据 = (tenant_id, entry_id)（09 BR-09-85） | |
| header_name | STR(64) | Y | | CUSTOM_HEADER 必填；`^[A-Za-z0-9-]{1,64}$` | BEARER_TOKEN 固定为 `Authorization: Bearer` |
| last4 | STR(4) | N | | | 只用于显示 |
| rotated_by / rotated_at | ID / TS | N | | | |

**`fleet_tool_grants`**（授予，只追加 + 状态列；BR-05-143）

| 列 | 类型 | 空 | 默认 | 约束 | 说明 |
|---|---|---|---|---|---|
| id | ID | N | | PK | |
| entry_id | ID | N | | | |
| subject_kind | ENUM | N | | `ToolGrantSubjectKind`：PROJECT / ROLE / AGENT | |
| project_id | ID | Y | | PROJECT 必填；ROLE / AGENT 可选（为空 = 全部项目与团队级） | |
| role | ENUM | Y | | ROLE 必填，`AgentRole`（00 §5） | |
| agent_id | ID | Y | | AGENT 必填 | |
| state | ENUM | N | `ACTIVE` | ACTIVE / REVOKED；部分唯一 `(entry_id, subject_kind, coalesce(project_id,0), coalesce(role,''), coalesce(agent_id,0)) WHERE state='ACTIVE'` | |
| granted_by_kind / granted_by | ENUM / ID | N / Y | | HUMAN；迁移种子为 SYSTEM（BR-05-150） | |
| revoked_by / revoked_at / revoke_reason | ID / TS / STR(500) | Y | | | |

索引：`(tenant_id, entry_id, state)`；`(tenant_id, agent_id, state)`；`(tenant_id, project_id, state)`。

**`fleet_tool_presets`**（推荐预设；系统级只读 `tenant_id=0`，随版本发布，同 §13 CCR-05-11 的处理）：`preset_key`（UNIQUE）、`title`、`description`、`transport`、`config_template JSON`（S-T1，含 `{{placeholder}}`）、`credential_mode`、`credential_help STR(500)`（如何在服务方取得令牌）、`docs_url`、`phase`。
**模块内枚举增补**（00 §3：仅本模块使用，不进 00 §5）

| 枚举 | 值 |
|---|---|
| `InboxEventKind` | MESSAGE（Agent 参与的私信、线程回复，或其执行产物上的新批注）, MENTION（频道或批注中被 @）, DISPATCH（派单生效与派给它的工作：评审、测试、验收、分诊、发布说明、主管规划、计划 / 复盘起草）, REVIEW_FEEDBACK（对其声明的裁决与结构化反馈、内部验收失败反馈、转为反馈的批注）, REMINDER（提醒到点，含循环契约运行）, INTERACTION（其执行的工具授权 / 输入请求已被人回应、到期或取消） |
| `WakeMode` | CHANNEL（经机器通道的提示流，默认）, POLL（宿主轮询）, WEBHOOK（签名 webhook，v1.1） |
| `AgentActivityStatus` | ONLINE（在线空闲）, THINKING（模型思考中）, WORKING（执行工具或有运行中的执行）, ERROR（出错）, OFFLINE（离线） |
| `AgentResetKind` / `AgentResetState` | SESSION, FULL / REQUESTED, DONE, FAILED, TIMED_OUT |
| `ReminderAnchorKind` / `ReminderScheduleKind` / `ReminderState` | THREAD, WORK_ITEM, LOOP_CONTRACT / ONCE, INTERVAL, PERIOD / ACTIVE, PAUSED, DONE, CANCELLED |
| `OperationSideEffect` | READ_ONLY, WRITES_STATE, POSTS_MESSAGE, STARTS_EXECUTION, SPENDS_BUDGET, NOTIFIES_HUMANS, CREATES_PRINCIPAL |
| `HumanConfirmation` | NONE（直接生效）, ALWAYS（只生成待人确认的卡片，如频道派单请求）, BY_POLICY（按授权或策略决定，如自建请求的审批方式） |
| `ToolEntryKind` / `ToolEntryState` | MCP_SERVER, SKILL / DRAFT, ACTIVE, DISABLED, ARCHIVED |
| `McpTransport` | STREAMABLE_HTTP（经平台中继，BR-05-147）, STDIO_PACKAGE（机器按完整性哈希安装并在执行内启动） |
| `ToolCredentialMode` | NONE, BEARER_TOKEN, CUSTOM_HEADER；OAUTH_CLIENT 为 v1.1 |
| `ToolVersionState` / `ToolGrantSubjectKind` | ACTIVE, SUPERSEDED, REVOKED / PROJECT, ROLE, AGENT |
| `ToolSource` | CATALOG（团队目录）, MACHINE_LOCAL（04 能力检测到、未纳入团队目录，「本机自带」） |

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
| R1 | PRECHECK | 读 02 在途分派、03 运行中执行、07 是否为主管、04 机器状态，写 `precheck` | 同步完成 | — | 机器非 ONLINE → 422 142024 |
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
| P02 | PROPOSED | AUTO 自动应用 | policy=AUTO；`requires_human=false`；07 `ProjectGate.CanAutoDispatch` = true；应用时复验 E1–E11 | APPLIED（AUTO_APPLIED） | 同事务调 02 `DispatchAssigner.Assign`（REDISPATCH 用 `Reassign`；actor=主管 Agent，`authority_source=POLICY:dispatch_auto`，`detail.dispatch_proposal_id`）；`dispatch.applied` |
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
| H03 | RUNNING | 全部枝终态（SUBMITTED/FAILED/STOPPED_*） 或 deadline | — | COMPARING | 拉 03 A08 对比数据 + 验证摘要；请求主管 Agent 写比较理由（120 s） |
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

频道：`ACTIVE`（`archived_at IS NULL`）⇄ `ARCHIVED`。PROJECT_DEFAULT 只能随项目进入 CLOSED / CANCELLED 被系统归档（消费 07 `project.stage_changed`），人工归档 → 409 145011；AGENT_DIRECT 与 DIRECT 不可归档 / 改名（145011）；DIRECT 不属项目，不受项目关闭影响（BR-05-114）。归档频道写消息 / 加成员 → 409 145008；项目重开（不存在此迁移）不适用。

消息：`POSTED → EDITED*（≤10 次，作者，15 min 内，仅 TEXT）→ RETRACTED`（终态）。EVENT / DECISION_CARD / DISPATCH_CARD 只由系统写，作者不可编辑 / 撤回（409 145006）；卡片的「已处理」状态由 `payload.status` 投影更新（`message.updated`，不计 edit_count）。RETRACTED 上编辑 / 撤回 → 409 145006。

---

### 4.6 SpawnRequestState（D110）

| # | 当前 | 事件 / 命令 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| S01 | — | 主管发起（G01，或机器通道 `agent.spawn_request`） | BR-05-97 / 98 全部通过；08 `SpawnGrantUsage.Reserve` 成功 | PROPOSED（REQUIRE_APPROVAL）/ APPROVED（AUTO） | `agent.spawn_requested`（含 `approval_mode`）；频道「自建请求」卡片；审计 `fleet.agent_spawn_requested` |
| S02 | PROPOSED | 人批准 G04 `{decision:APPROVE}` | HUMAN；对请求项目持 `agent_manage`；授权仍 ACTIVE；复验 BR-05-98 | APPROVED | `decided_by/at`；同事务继续 S04 |
| S03 | PROPOSED | 人拒绝 G04 `{decision:REJECT, note}` | HUMAN；`agent_manage` | REJECTED | `SpawnGrantUsage.Release`；`agent.spawn_rejected`；卡片置为已拒绝 |
| S04 | APPROVED | 系统执行创建（与 S01 / S02 同事务，BR-05-100） | 创建路径 BR-05-01–07 通过 | CREATED | `created_agent_id`；`agent.spawned`；审计 `fleet.agent_spawned` |
| S05 | APPROVED | 创建失败（名称冲突、机器不可用、底座 503 等） | — | FAILED | `SpawnGrantUsage.Release`；`failure_code`；`agent.spawn_failed`；通知主管与授权人 |
| S06 | PROPOSED | 24 h 到期；或授权 REVOKED / EXPIRED；或主管退役 | — | EXPIRED | `SpawnGrantUsage.Release`；卡片置灰 |

非法迁移：对非 PROPOSED 的请求裁决 → 409 `142035`（`data.state`）。

### 4.7 Agent 谱系迁移（D110；叠加在 §4.1 之上，不新增 AgentState）

| # | 当前 | 事件 | 守卫 | 结果 | 副作用 |
|---|---|---|---|---|---|
| L01 | — | S04 创建 | — | `origin=AGENT_SPAWNED`，DRAFT（随后按 A02 投递凭证并启动） | 写入 `parent_agent_id`、`spawn_grant_id`、`expires_at`；加入请求项目 |
| L02 | AGENT_SPAWNED·未收编 | 主管 RETIRED；或主管 CRASHED 且 `restart_exhausted` 持续 >24 h；或授权 REVOKED / EXPIRED（含 08 接管撤销授权） | — | 非 RETIRED 者 → STOPPED（经 A05 / A14）；`orphaned_at=now`、`orphan_reason` | 03 E27 中止其 AUTONOMOUS 执行；02 BR-02-16 回收分派；`agent.orphaned`；N-05-14 |
| L03 | 孤儿 | 人收编 G05 | HUMAN；`agent_manage`；可选新 `machine_id` | 保持 STOPPED（收编后由人启动） | `adopted_by/at`；清 `expires_at`；`SpawnGrantUsage.Release`；`agent.adopted`；审计 `fleet.agent_adopted` |
| L04 | AGENT_SPAWNED·未收编（非孤儿） | 人主动收编 G05 | 同 L03 | 状态不变 | 同 L03 |
| L05 | 孤儿 | 7 天未处理（`fleet.spawn_lifecycle`） | — | RETIRING → RETIRED（正常退役；机器离线时按系统强制退役，不需 step-up） | 交接文档由系统摘要生成；`agent.retired{reason:ORPHAN_TIMEOUT}` |
| L06 | AGENT_SPAWNED·未收编 | `expires_at` 到期，或 `now − idle_since ≥ idle_retire` | — | RETIRING（§4.2 正常退役） | 完成时 `SpawnGrantUsage.Release`；`agent.retired{reason:TTL / IDLE}` |


### 4.x 内部验收派单（D112 / D113，00 v2.0 §15）

| # | 触发 | 守卫 | 结果 |
|---|---|---|---|
| IQ1 | 03 `internal_qa.passed{stage:AUTOMATED}` | 项目非 HUMAN_ONLY | 生成 `trigger=INTERNAL_QA, role=TESTER` 提案并立即应用（SYSTEM `SYSTEM:fleet.internal_qa_dispatch`）→ 03 Launch(INTERNAL_TEST) |
| IQ2 | 03 `internal_qa.passed{stage:AGENT_TEST}` | 同上 | `role=ACCEPTOR` 提案并应用 → 03 Launch(INTERNAL_ACCEPTANCE) |
| IQ3 | 无合格候选（BR-05-109 全部回退仍为空） | | 不派单；02 `WorkItemEscalator.Escalate(kind=MANDATORY_HUMAN_GATE, detail{reason:NO_INTERNAL_QA_AGENT, role})` |
| IQ4 | 候选 Agent 拒绝 / 30 min 未开始（`agent.qa_ack{accepted:false}` 或超时） | 重派次数 < 2 | 换下一候选重派；≥2 次 → 同 IQ3 |
| IQ5 | HUMAN_ONLY 项目收到 IQ1 / IQ2 触发 | | 不派 Agent；内部验收由 02 直接进入人工（L2 只含 AUTOMATED，00 §15 对纯人工模式的例外） |

### 4.z 活动状态、重置、提醒与收件箱（D120；叠加在 §4.1 之上，不新增 AgentState）

**活动状态 `AgentActivityStatus`**（由机器上报 + 服务端派生，BR-05-134）

| # | 当前 | 事件 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| AS1 | OFFLINE | `agent.status{RUNNING}` 到达（A04 / A09） | conn_epoch 当前 | ONLINE（宿主同时报告 `activity` 时取其值） | SSE `tenant/{t}/agent/{id}` |
| AS2 | ONLINE / THINKING / WORKING | 宿主上报 `activity` 变化 | 同一 Agent 上报间隔 ≥2 s（更密的合并为最后一个值） | 上报值（ONLINE / THINKING / WORKING / ERROR） | SSE；不写领域事件、不审计 |
| AS3 | 任意非 OFFLINE | process_status 离开 RUNNING（STOPPING / EXITED / UNREACHABLE），或机器 OFFLINE，或 AgentState ∉ {ACTIVE} | — | OFFLINE；AgentState=CRASHED 时为 ERROR | SSE |
| AS4 | ERROR | 宿主上报 ONLINE / THINKING / WORKING，或人工重启后 RUNNING | — | 上报值 | 关闭 N-05-16 计时 |
| AS5 | ERROR | 持续 ≥10 min | — | ERROR（不变） | N-05-16（每次进入 ERROR 一次） |

**重置 `AgentResetState`**（BR-05-135 / 136）

| # | 当前 | 事件 / 命令 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| RS1 | — | F21 `{kind:SESSION}` | HUMAN；`agent_manage`；AgentState ∈ {ACTIVE, STOPPED, CRASHED}；无进行中的重置 | REQUESTED | `session_epoch+1`；ACTIVE·RUNNING 时下发 `agent.reset{SESSION}`，否则机器下次启动时按新 `session_epoch` 丢弃旧会话并直接 DONE；审计 `fleet.agent_session_reset` |
| RS2 | — | F21 `{kind:FULL}` | HUMAN；`agent_manage`；step-up `agent_full_reset`（≤5 min）；`confirm_name` 完全相等；03 报告该 Agent 无非终态执行；机器 ONLINE | REQUESTED | `session_epoch+1`、`workspace_epoch+1`；ACTIVE 时先 STOPPING；下发 `agent.reset{FULL}`；审计 `fleet.agent_full_reset` |
| RS3 | REQUESTED | 机器 `agent.command_ack{ok:true, removed_bytes?}` | conn_epoch 当前 | DONE | 原为 ACTIVE 的 FULL 重置 → 按 A03 自动启动；收件箱游标不变 |
| RS4 | REQUESTED | ACK `ok:false` | — | FAILED | `failure_code`；详情页显示原因与〔重试〕（新请求） |
| RS5 | REQUESTED | 5 min 无 ACK | — | TIMED_OUT | FULL：`pending_cleanup_machine_id` 置为该机，机器重连后按 `workspace_epoch` 补做清理 |

非法：RETIRING / RETIRED / DRAFT 上任何重置 → 409 142010；FULL 有非终态执行 → 409 142012；已有 REQUESTED → 409 142045。

**提醒 `ReminderState`**（BR-05-138 / 139）

| # | 当前 | 事件 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| RM1 | — | F23 创建（Agent 本人 / 人类 `agent_manage`）或 10 `AgentReminderScheduler.UpsertForLoopOn` | BR-05-138 | ACTIVE | 计算 `next_fire_at`；审计 `fleet.agent_reminder_changed` |
| RM2 | ACTIVE | `next_fire_at ≤ now`（`fleet.reminder_fire`） | Agent 非 RETIRED；锚点仍可读 | ACTIVE（ONCE / 达 `max_fires` → DONE） | 写收件箱 REMINDER + 唤醒；LOOP_CONTRACT 先调 10 `LoopRunRecorder.OpenOn` |
| RM3 | ACTIVE | 锚点不可读（频道归档、Agent 离开项目、工作项取消） | — | CANCELLED | 收件箱 REMINDER（`payload.cancelled_reason`） |
| RM4 | ACTIVE ⇄ PAUSED | F24 `{state}`；或 10 契约 PAUSED / ACTIVE | 创建者本人、Agent 负责人或 `agent_manage` | PAUSED / ACTIVE | 恢复时从 now 重算，不补发错过的触发 |
| RM5 | ACTIVE / PAUSED | F24 `{state:CANCELLED}`；Agent 退役；10 契约 ARCHIVED | — | CANCELLED | — |

**收件箱事件**：只追加，无状态迁移；`seq ≤ inbox_ack_seq` 视为已确认，`expires_at` 过后由 `fleet.inbox_retention` 物理清理（收件箱是派生投影，原始事实留在各模块；按 §13 CCR-05-11 登记为 00 §3 的保留期清理例外）。

**团队工具目录条目 `ToolEntryState`**（BR-05-141 / 142 / 152；SKILL 条目只有 ACTIVE ⇄ DISABLED 与随 06 归档的 ARCHIVED）

| # | 当前 | 事件 / 命令 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| TE1 | — | T02 新建 MCP 条目（或从预设 / 本机导入）；06 首个技能版本批准（SKILL 条目，`skill_version.approved`） | HUMAN；`tool_catalog_manage`；BR-05-142 | DRAFT（MCP）/ ACTIVE（SKILL） | 审计 `fleet.tool_entry_changed` |
| TE2 | DRAFT / DISABLED | T07 `{state:ACTIVE}` | 至少 1 个 ACTIVE 版本；`credential_mode≠NONE` 时已设置凭据；If-Match | ACTIVE | `tool_catalog.changed{STATE_CHANGED}` |
| TE3 | ACTIVE | T07 `{state:DISABLED, reason}` | HUMAN；`tool_catalog_manage` | DISABLED | 新执行不再解析该条目；中继立即拒绝持该条目令牌的调用（BR-05-152） |
| TE4 | DRAFT / DISABLED | T07 `{state:ARCHIVED, reason}`；SKILL：06 技能归档 | — | ARCHIVED（终态） | 授予保留记录但不再生效 |
| TE5 | ACTIVE | T05 发布新版本 | 配置哈希与当前版本不同（否则 409 142058） | ACTIVE（`current_version_no+1`） | 旧版本 SUPERSEDED；已开始的执行仍用旧版本（BR-05-145）；`tool_catalog.changed{VERSION_PUBLISHED}` |

非法：对 ARCHIVED 的任何迁移、DRAFT → DISABLED、ACTIVE → ARCHIVED（须先 DISABLED）→ 409 **142053**。

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
- **BR-05-16 工作目录浏览**：`GET /agents/{id}/workspace-entries?path=` 经 04 新增通道请求 `agent.workspace_list`（CCR-05-06），根限定为 Agent 私有目录与其执行工作目录，路径规范化后不得越出（`..`、符号链接越界 → 422 142027）；单次 ≤1000 项；文件内容 `…/workspace-entries/content?path=` ≤1 MiB（超出 413 142028）；机器离线 → 409 142002；只读。权限：`agent_read` + `machine_read`（**D120 收窄**：只限该 Agent 的负责人类与持 `agent_workspace_read` 者，见 BR-05-137）。

### 5.3 退役

- **BR-05-20 正常退役前置**：机器 ONLINE（否则 422 142024）；无 IN_PROGRESS 退役（409 142014）；若为某项目主管 Agent：不阻断，但 PRECHECK 列出并提示——07 BR-07-07 在收到 RETIRING 时置 `lead_agent_id=NULL`、`dispatch_policy=MANUAL`。
- **BR-05-21 接手人**：HUMAN 须活跃租户成员且对每个在途工作项所在项目有 `work_item_execute`；AGENT 须 ACTIVE、含 EXECUTOR、非本 Agent、在对应项目有 `agent_executor` 绑定且 `max_risk ≥` 工作项风险；不满足的工作项不阻断整体，按项进入 `failed`（code 120444 来自 02）或在 SUCCESSOR 步骤以 422 142018 列出（`data.items[]`）由人改选 LATER。
- **BR-05-22 转交即裁决**：每个转交经 02 `DispatchAssigner.Reassign(tx, …)`，02 记 ASSIGN Decision（actor=操作的人，authority 由 AuthorityChecker，reason「Agent 退役交接：<retirement_id>」，`rationale` 引用交接 Artifact id）。LATER → 02 `Release`（工作项回 READY / BLOCKED，标 `needs_owner`），派单循环可再分派。
- **BR-05-23 运行中执行**：REASSIGN 前，对该 Agent 非终态 AUTONOMOUS 执行由 03 E27 在 `agent.state_changed{RETIRING}` 时取消（SUPERSEDED）；交接模式下的 HANDOVER 执行豁免（purpose=HANDOVER，03 CCR）。
- **BR-05-24 取消**：仅 PRECHECK / HANDOVER / SUCCESSOR 步可取消；Agent → STOPPED（非原状态，避免自动恢复派单），已生成交接 Artifact 保留。
- **BR-05-25 强制退役**：单事务完成：Agent → RETIRED；退役记录 `mode=FORCED, state=COMPLETED, step=DONE`；系统交接摘要 Artifact（同步生成，失败不阻断，记 `handover_source=NULL` 并在审计 detail 标注）；凭证吊销 / 服务账号停用 / 绑定移除 / 委托撤销同 R6；在途分派由 02 BR-02-16 在 `agent.retired` 后回收为 READY；机器在线则下发 stop + cleanup，否则写 `pending_cleanup_machine_id`（机器重连首个 poll 下发，最多保留 30 天）。确认：HUMAN、`agent_retire`、step-up（purpose=`agent_forced_retire`，≤5 min）、`confirm_name` 与当前 `name` 逐字相等（不符 422 142016）、reason ≥10。审计 `fleet.agent_retired`（`detail.mode=FORCED`、丢失清单：在途工作项数、运行中执行数）。
- **BR-05-26 退役后**：handle 不复用；历史消息、裁决、证据中的 Agent 显示「已退役」徽标；不可被 @（422 145016）；06 冻结其 AGENT scope（消费 `agent.retired`）。

### 5.4 绩效统计（公式）

- **BR-05-30 首次验收率** `FPA(agent, type, W)`：对窗口 W（30 / 90 天）内**首次裁决时间**落在窗口的工作项集合 S —— 工作项 w ∈ S 当且仅当 agent 提交过 w 的声明，且 w 的第一个 ∈ {APPROVE, REJECT, REQUEST_CHANGES} 的 Decision 发生在 agent 的首个声明之后、并落在 W 内。`first_decisions = |S|`；`first_pass_accepts = |{w ∈ S : 该首个裁决 = APPROVE}|`；`FPA = first_pass_accepts / first_decisions`；`first_decisions < 5` 时 UI 显示「样本不足（n）」，评分用平滑值（BR-05-41）。与 07 项目级「首次验收率」（AC-07 accepted_first_count）同口径：返工后的再次 APPROVE 不计为首次。
- **BR-05-31 单位已接受成本** `CPA(agent, type, W)` = `cost_on_accepted_micros / accepted_items`：分母 = W 内进入 ACCEPTED 且最终被接受声明的执行者 = agent 的工作项数；分子 = 这些工作项上 agent 名下全部执行的 `cost_micros` 之和（含其返工枝、RETRY_INFRA 枝、ENSEMBLE 落选枝）；不含主管 Agent 规划成本（计入项目，不计个人）。分母 0 → `null`（显示「—」）。
- **BR-05-32 返工率** `RR(agent, W)` = `rework_decisions / claims`：分子 = W 内针对 agent 声明作出的 REJECT + REQUEST_CHANGES 数；分母 = W 内 agent 提交的声明数；分母 0 → `null`。可 >1（一个声明理论上只有一次裁决，故通常 ≤1；保留原值不截断）。
- **BR-05-33 当前负载**（实时，不缓存）：`active_items` = 02 中该 Agent ACTIVE EXECUTOR 分派数；`running_executions` = 03 非终态执行数；`load_ratio = active_items / max_parallel_items`。成员表显示 `active_items/max_parallel_items` 与进度条。
- **BR-05-34 刷新节奏**：全量重算任务每 10 min（仅对 24 h 内有声明 / 裁决 / 成本事件的 Agent）；增量：消费 `decision.made`、`claim.submitted`、`execution.cost_updated` 后对受影响 Agent 去抖 60 s 重算；页面显示 `computed_at`（「1 分钟前更新」）。重算以源数据（02/03 读接口）为准，`fleet_agent_stats` 可整体删除重建。

### 5.5 主管 Agent 与派单

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
  - E10 `NOT_LEAD_AUTO`：policy=AUTO 时排除项目主管 Agent 自身（职责分离，BR-05-70）。
  - E11 `BUDGET`：Agent 在该类型的 CPA（无则团队中位数）≤ 07 `ProjectGate` 返回的项目剩余预算；剩余预算未设 → 通过。
  - E12 `SPAWN_BUDGET`（D110）：`origin=AGENT_SPAWNED` 且未收编时，所属自建授权须 ACTIVE、未 EXHAUSTED，且该 Agent 未达 `per_agent_cap_micros`，且不是孤儿（BR-05-101 / 102 / 103）。
- **BR-05-41 评分**（`scoring_version=s1`，整数运算保证确定性）：各分量取 0–10000 基点，四舍五入（half-up）到整数：
  - `skill_bp = round(10000 × |T ∩ A| / |T|)`，T = 工作项必需 ∪ 偏好技能标签，A = Agent 标签；`|T|=0` → 10000。
  - `fpa_bp = round(10000 × (a + 4·p₀) / (n + 4))`：n、a 为该 Agent 在**同 WorkItemType**、90 天窗口的 `first_decisions`、`first_pass_accepts`；p₀ = 团队同类型 90 天 FPA（团队无数据 → 0.5）。即贝叶斯平滑，先验权重 4。
  - `cost_bp`：c_i = Agent 同类型 90 天 CPA，`null` 用团队同类型 CPA 中位数，团队也无 → 用 04 `ModelPrice` × 团队该类型平均 token 数，再无 → 所有候选同值；`cost_bp = 10000` 若 c_max = c_min，否则 `round(10000 × (c_max − c_i) / (c_max − c_min))`（仅在合格候选内归一）。
  - `load_bp = round(10000 × (1 − min(1, (active_items + queued_on_machine) / max_parallel_items)))`。
  - `diversity_bp = round(10000 × (1 − min(1, r / 5)))`，r = 该 Agent 过去 24 h 在本项目被 APPLIED 的提案数（近因多样性）。
  - `score = 35·skill_bp + 30·fpa_bp + 15·cost_bp + 15·load_bp + 5·diversity_bp`（0–1,000,000，展示为 `score/10000` 保留两位）。权重理由：技能匹配决定能否做对 > 历史质量 > 经济性与负载 > 轮换（**本规格提议**）。
  - **并列打破**（依次）：fpa_bp 高 → active_items 少 → c_i 低 → 最近一次被派时间早（从未被派最先）→ agent_id 数值小。
  - 输入快照（标签、n、a、p₀、c、负载、r）写入 `fleet_dispatch_candidates.inputs`，`inputs_digest` = 规范化 JSON 的 SHA-256；同快照必得同排名（AC-05-20）。
- **BR-05-42 主管 Agent 的 LLM 用法**：系统把前 K=5 名合格候选（含分量、理由素材）、工作项与验收标准、需求摘录、06 `Retriever.Search(purpose=LEAD)` 结果，经 04 通道 `agent.lead_plan` 请求发给主管 Agent 宿主（CCR-05-06），120 s 超时。主管 Agent 只能在**合格集合**内选择并给出 ≤2000 字理由（须引用至少一项分量，如「前端技能 3/3、首次验收率 82%、当前空闲」）。结果非法（选了不合格 / 不存在的 Agent、JSON 不符 Schema、超时）→ 采用系统 top-1，`rationale_source=SYSTEM`，理由由模板生成，并在卡片上标注「主管 Agent 未给出有效建议」。LLM 永不能绕过预过滤，也不能改分。项目无主管 Agent 时一律系统评分（`lead_agent_id=NULL`）。
- **BR-05-43 策略来源**：`dispatch_policy`、`lead_agent_id`、`ensemble_max_branches` 只存于 07 项目档案，经 07 `ProjectReader` 读取、经 07 #4 `PATCH /entities/{entity_id}/project` 写入（「团队编排」卡片直接调用 07 #4，本模块不存副本）。AUTO 但 `lead_agent_id=NULL` → 视作 MANUAL（`guard_reasons` 含 G9）。
- **BR-05-44 AUTO 护栏**（任一命中 → `requires_human=true`，提案停在 PROPOSED 并发 DISPATCH_CARD 与通知 N-05-03）：G1 工作项 risk=HIGH；G2 估算成本（c_i）> 07 剩余预算或预算 EXHAUSTED / `ProjectGate.CanAutoDispatch=false`（项目暂停、升级生效、只读；阶段非 ACTIVE 时不生成提案，见 BR-05-55）；G3 主管 Agent 选择非 top-1 且 `score_top − score_chosen > 150000`（15 分）；G4 本项目过去 60 min 已 AUTO_APPLIED ≥20 次；G5 该工作项 24 h 内已被派 ≥2 次（抖动）；G6 工作项 `require_human_decision=true`（02 模板字段）；G7 选中者为主管 Agent 自身（MANUAL 下也须人确认）；G8 工作项 24 h 内曾被人改派（人工意图优先）；G9 无主管 Agent。
- **BR-05-45 生效路径**：APPLIED 必须经 02 接口 `DispatchAssigner.Assign(ctx, tx, {work_item_id, assignee_principal_id, role, expected_work_item_version, actor, authority_source, rationale, dispatch_proposal_id})`（CCR-05-04；02 在其内部执行 A1 逻辑：AuthorityChecker、ASSIGN Decision、分派与租约、`work_item.assigned`），与提案状态更新**同一事务**；02 返回错误 → 提案不变更，向调用者返回 02 错误码（如 120426 并发认领）。随后 outbox `dispatch.applied`（仅供 collab 投影、08 关闭待办与通知；**02 不消费此事件**，分派已由 `DispatchAssigner` 同步落库）。AUTO：actor=主管 Agent（AGENT），`authority_source=POLICY:dispatch_auto`，提案 id 记于 `detail.dispatch_proposal_id`（00 §6.3 v1.5）；人确认 / 改派：actor=人，authority 由 08 判定。`trigger=REDISPATCH` 的提案一律经 `DispatchAssigner.Reassign` 应用（02 T07 把工作项置为 CLAIMED、以 RETRY_INFRA 续跑并由 03 将中断的父执行置为 FAILED `SUPERSEDED_BY_RETRY`，见 BR-05-52）。
- **BR-05-46 提案 TTL**：30 min（**本规格提议**：短于 Agent 租约 10 min × 3，长于人类一次查看间隔）；到期 P06 立即重生成，但同工作项 24 h 内累计生成 >6 次 → 停止自动生成并通知（抖动）。
- **BR-05-47 并发**：派单循环按项目取 dlock `fleet:dispatch:<project_id>`（maxHold 60 s）；应用时在本事务内对受派 Agent 行 `SELECT … FOR UPDATE` 并复验 E2/E3/E8（跨项目防超额）；两人同时确认同一提案 → 一人成功，另一人 409 140409（版本）或 143004（已非 PROPOSED）。
- **BR-05-48 队列顺序与饥饿**：同项目待派工作项按 `(aging_flag DESC, priority DESC, risk DESC, ready_at ASC, work_item_id ASC)` 处理，`aging_flag = ready_at < now − 2h`；每轮 ≤10 项；READY 无分派超 4 h → 通知负责人 N-05-04（每项每 4 h 一次）。
- **BR-05-49 无合格候选**：生成 `result=NO_ELIGIBLE` 的 PROPOSED 提案（无推荐人），附原因直方图（如「机器离线 2、技能不符 1、满载 1」）与最接近的 3 名（不合格码最少者）；不自动应用；人可 OVERRIDE 指派任一满足 02 BR-02-10 的成员（含人类）或 DISMISS。合格集合可能因事件改变：`agent.state_changed`、`machine.state_changed`、分派释放后去抖 30 s 重新评估，若出现合格者 → 原提案 EXPIRED(STALE) 并生成新提案。
- **BR-05-50 抖动与排除**：被改派弃用 / 被移走的 Agent 对该工作项排除 24 h（E9）；同一工作项 24 h 内派单 ≥3 次 → 停止自动生成，需人手动生成；DISMISS 后 2 h 内不自动生成。
- **BR-05-51 频道请求**：「@主管Agent 把 W 派给 Ada」或「@Agent 请做 X」只能产生 `trigger=CHANNEL_REQUEST` 的提案或 02 工作项草稿（Agent 以自身凭证调用 02 草稿接口），**永不直接改变状态**；CHANNEL_REQUEST 提案一律 `requires_human=true`（G 外的硬规则，防止频道文本注入绕过策略），卡片「确认」按钮仅对持 `work_item_assign` 的人可用。
- **BR-05-52 重派触发**：(a) Agent CRASHED 且 `restart_exhausted`；(b) Agent STOPPED / RETIRING / RETIRED（02 BR-02-16 已回收分派 → 工作项 READY → 正常 P01）；(c) 机器 OFFLINE 持续 5 min（**本规格提议**宽限，覆盖短暂网络抖动与 03 执行续约 30 s × 多次）：对该机 Agent 的 CLAIMED（未开始执行）分派经 02 `Release` 后重派；(d) 02 `work_item.state_changed{to: EXECUTION_FAILED}` 且 02 自动重试已用尽（payload `infra_retries_exhausted=true`）。(e) 机器 OFFLINE 持续 ≥5 min，且该机 Agent 名下有工作项其当前执行为 INTERRUPTED（迁移，PRD §10.1）：生成 REDISPATCH 提案；应用时 02 `DispatchAssigner.Reassign` 按 02 T07 先终结旧执行，再以 `branch_reason=RETRY_INFRA`、parent = 该 INTERRUPTED 执行启动新执行（03 BR-03-07 在同事务把 parent 置 FAILED `SUPERSEDED_BY_RETRY`）；原工作目录不可达，故一律 CARRYOVER（04 `PlanMigration`）。重派提案 `trigger=REDISPATCH`，前任对该工作项排除 24 h。
- **BR-05-53 评审派单（AUTONOMOUS，MVP）**：消费 02 `work_item.state_changed{to:NEEDS_HUMAN_REVIEW}`，项目 `team_mode=AUTONOMOUS` 时：调 08 `AuthorityChecker.ListEligible(work_item, APPROVE)`，取持有效委托、含 REVIEWER 角色、非该工作项执行者且非派出者的 Agent；按 BR-05-41 评分（`fpa_bp` 改为评审一致率：其 APPROVE 后 30 天内未被甲方拒收或返工的比例，无数据取 0.5），取 top-1 生成 `trigger=REVIEW` 提案并立即应用：actor=SYSTEM（`SYSTEM:fleet.review_dispatch` 为系统动作身份），裁决记录的 `authority_source` 取 08 `Check` 返回的 `POLICY:review_dispatch`（本模块启动时 `SystemActions.Register("fleet.review_dispatch","work_item_assign")`，08 S1 据此放行；同时向 08 `PolicyRegistry` 贡献 `review_dispatch` 规则供审计说明），调 02 `DispatchAssigner.Assign(role=REVIEWER, lease_ttl_seconds=1800)`（02 对 NEEDS_HUMAN_REVIEW 上的 REVIEWER 分派为**非迁移**操作，租约 1800 s 覆盖评审时限，不被 `lease_reclaim` 提前回收），再经 04 通道发 `agent.review{command_id, agent_id, work_item_id, claim_id, deadline_at=now+30min}`，宿主回 `agent.review_ack`。30 min 内未收到裁决 → 同事务：02 `Release(REVIEWER)` + 02 `WorkItemEscalator.Escalate(tx, {work_item_id, kind:REVIEW_SLA, detail:{reviewer_kind:AGENT, agent_id, attempt}})`（由 02 发布 `work_item.escalated`），并重新评估；无合格评审 Agent，或同一工作项连续 2 次超时 → 停止派评审，由 08 规则 `NO_ELIGIBLE_AUTHORITY` 升级给人（N-05-12）。HYBRID 项目不派评审（由「我的裁决」承担）。评审派单本身不授予任何裁决权：评审 Agent 的裁决仍由 02 + 08 `AuthorityChecker` 按委托条件判定。
- **BR-05-54 唤醒协调员**：消费 01 `intake.received` / `intake.triage_requested`：若开口配置了 `triage_agent_id` 且该 Agent ACTIVE·RUNNING，经 04 通道发 `agent.triage{command_id, agent_id, intake_id, deadline_at=now+15min}`；Agent 以自身凭证调 01 分诊卡接口提交草案。Agent 未运行或超时不重试，由 01 的 30 min 看门狗与通知交给人。
- **BR-05-55 阶段规则**：只在 07 项目阶段 ∈ {ACTIVE, ACCEPTANCE_PENDING} 时自动生成提案（READY / REDISPATCH / REVIEW / ENSEMBLE；ACCEPTANCE_PENDING 覆盖甲方部分验收后的返工与评审，与 07 BR-07-12 一致）；DISCOVERY / PLANNING 阶段只允许人通过 D02 发起 `MANUAL_REQUEST`，其确认若被 02 以 120420（阶段不允许）拒绝则原样返回。`project.stage_changed{to:ACTIVE}` 立即触发一次派单循环。
- **BR-05-56 起草发布说明**：消费 07 `delivery.release_notes_requested{delivery_id, drafter_agent_id?, note?}`：若 `drafter_agent_id` 指定的起草 Agent 为 ACTIVE·RUNNING，经 04 通道发 `agent.release_notes{command_id, agent_id, delivery_id, note, deadline_at=now+30min}`；宿主让该 Agent 读取交付包内容并以**自身凭证**调用 07 现有发布说明接口（07 #36）提交草稿（07 校验起草人与交付包项目）。起草 Agent 不存在 / 未运行 → 立即调用 07 `ReleaseNotesDrafting.ReportUnavailable(ctx, tx, {tenant_id, delivery_id, reason: NO_DRAFTER / DRAFTER_NOT_RUNNING})`；`deadline_at` 到期仍未提交 → 调 `ReportUnavailable(reason:TIMEOUT)`。兜底（直接发出确认请求、要求人工编写）由 07 BR-07-30b 负责，本模块不重试。

### 5.6 Heavy 并行求解（v1.1）

- **BR-05-60 触发**：(a) 工作项 risk=HIGH 且项目 `ensemble_max_branches ≥2` → 主管 Agent 在派单时改为生成 ensemble 计划；(b) 工作项 `uncertainty_flag=true`（02 字段，由人、协调员或主管 Agent 草拟设置）；(c) 人在工作项详情点〔并行求解〕。合格候选 <2 → 422 143009，回退单派。
- **BR-05-61 选枝**：N = min(请求值或 07 `ensemble_max_branches`, 合格数)，默认 3；从评分排序中贪心：先取第 1 名，之后依次取 `(runtime_kind, model_id)` 与已选**都不同**的最高分者；不足 N 时再按分数补入不同 Agent（模型可重复）；同一 Agent 不得占两枝；主管 Agent 不得为枝执行者（E10 恒生效）。
- **BR-05-62 预算**：`budget_cap_micros` 缺省 = N × 团队同类型 CPA 中位数 × 1.5，无数据时 N × 5,000,000（$5/枝，**本规格提议**）；不得超过 07 项目剩余预算（超出 422 143011）。需人批准的计划进入 PENDING_APPROVAL：08「我的裁决」生成 `ENSEMBLE_APPROVAL` 待办（由 `ensemble.plan_created{requires_approval:true}` 打开，`ensemble.started` / `ensemble.cancelled` 关闭），24 h 未批准自动取消（H11）。每枝上限 `branch_cap = floor(cap / N)`，作为 03 `max_cost_micros` 下发（03 COST_CAP 终止）。启动需人批准的情况：policy=MANUAL；或 cap > 默认值（还需 `project_manage`）；或风险 HIGH 且项目 HYBRID。累计 ≥80% → 通知 N-05-07；≥100% → H04。
- **BR-05-63 对比报告**（§6.5 Schema）：每枝列 Agent、runtime / 模型、终态与原因、证据覆盖（已映射验收标准数 / 总数）、必需验证 PASSED/FAILED/INCONCLUSIVE/PENDING、自动化测试 pass/fail/skip、diff 规模（文件数、+/− 行）、成本、时长、AI 辅助验证意见（标注「仅参考」）、主管 Agent 理由。**系统排序**：先要求必需 AUTOMATED 验证全 PASSED 的枝（其余排后），再按覆盖率高 → 失败测试少 → 成本低 → diff 行数少 → 枝 id 小。报告不宣称「正确」，只陈述证据。
- **BR-05-64 择优**：AUTO 且风险非 HIGH 且主管 Agent 选择 = 系统第一 → 自动 SELECTED（H07）；否则 SELECTION_PENDING 等人。合成（synthesize）只能由人发起，指定主枝 + 被合成枝（≤N−1），由主枝 Agent 执行新 ENSEMBLE 执行，预算从剩余 cap 扣，剩余 <10% → 422 143011。
- **BR-05-65 落选枝**：`outcome=NOT_SELECTED`、`archived_at=now`；执行、产物、证据全部保留（I5），在执行分支树以并列紫色枝灰显，可随时查看；不删除、不复用为声明。
- **BR-05-66 仍走正常裁决**：各枝执行结束时 03 不提交声明，只把声明草稿存为该执行的 `pending_claim`（03 E06 的 ENSEMBLE 分支）并对该枝排队验证（供对比报告）；选中后由本模块以选中枝 Agent 身份经 02 `ClaimSubmitter.SubmitOn(tx, {work_item_id, execution_id, claimed_by=枝 Agent, authority_source=POLICY:ensemble_branch, detail.ensemble_plan_id=<plan_id>, pending_claim（含 `head_commit`）})` 提交该枝的声明（枝内已采集的证据随 03 `EvidenceBinder` 绑定）→ AGENT_CLAIMED_DONE → 验证 → NEEDS_HUMAN_REVIEW → 有权主体裁决。择优本身不是 Decision，不改变 L2/L3。

### 5.7 职责分离与可观测

- **BR-05-70 主管 Agent 不裁决自己派出的工作**：本模块提供 `DispatchProvenance.DispatchedBy(work_item_id) []principal_id`（含 AUTO 应用时的主管 Agent、ensemble 计划发起者）；其唯一使用者是 02：02 构造 08 `AuthorityChecker` 请求时用它填入 `WorkItemFacts.DispatchedByActor`。08 对 APPROVE / REJECT / REQUEST_CHANGES / merge 判定时：主体 ∈ DispatchedBy 且无满足 PRD §8.3 条件（必需验证全 PASSED、非自产、返工 <3、风险在委托范围）的有效委托 → 拒绝（`reason_code=SEPARATION_OF_DUTIES`，`detail=LEAD_DISPATCHED`；02 返回 403 120403）。主管 Agent 在任何情况下不得作为自己所派工作项的执行者（AUTO 由 E10；MANUAL 由 G7 强制人确认）。
- **BR-05-71 可观测**：每次规划（含 LLM 输入摘要 hash、K 名单、耗时、成本）、提案、应用 / 改派 / 驳回 / 过期、ensemble 启动 / 对比 / 选择，都在项目默认频道写 EVENT（`fold_key=dispatch:<work_item_id>` 或 `ensemble:<plan_id>`）或卡片，并审计（§7.3）。「为什么派给它」= 提案 id → 候选明细 → 输入快照，永远可复算。

### 5.8 频道（collab）

- **BR-05-80 频道命名与上限**：CUSTOM 名 1–40，`^[\p{L}\p{N}][\p{L}\p{N}_-]{0,39}$`，同项目活跃频道唯一（422 145001）；每项目 CUSTOM ≤50（422 145002）；每频道成员 ≤200（422 145012）。
- **BR-05-81 默认频道**：07 在建项目事务内调 `ChannelProvisioner.CreateDefaultOn(ctx, tx, {tenant_id, project_id, name, member_principal_ids})`，失败使项目创建整体回滚（07 AC-07-02）。之后的成员变化由底座角色绑定决定：凡对项目有 `channel_read` 的人类与持 `agent_*` 绑定的 Agent 即默认频道成员；成员行 `source=DERIVED` 在首次读写时惰性补建，并由 reconcile 任务每 10 min 增删（失去项目访问 → `left_at`）。默认频道不可退出、不可移除成员（409 145011）。Agent 加入项目时同时加入其 `default_channel_names` 中存在的自建频道（`source=EXPLICIT`）。
- **BR-05-82 权限**：读频道 = `channel_read`（项目）且（DEFAULT 或为成员 或 持 `channel_manage`）；发消息 = `channel_post` 且为成员；建 / 改名 / 归档 / 管成员 = `channel_manage` 或该频道 OWNER（创建者）。不满足：缺权限码 403 145403（`reason_code=MISSING_PERMISSION`，`data.missing_permission`）；有权限但非成员 403 145403（`reason_code=NOT_MEMBER`）；他项目 / 他租户频道 404 145404。
- **BR-05-83 消息种类**：TEXT 由人或 Agent 发；EVENT / DECISION_CARD / DISPATCH_CARD 只由系统（collab 投影器）写，HTTP 发送 kind≠TEXT → 422 145017。DECISION_CARD 投影 02 的「待裁决」（NEEDS_HUMAN_REVIEW），按钮调用 02 D1（权限仍由 02 判定，频道只是入口）；拒绝必须填写结构化反馈（复用 `StructuredFeedbackForm`）。
- **BR-05-84 @mention**：客户端拾取器插入令牌 `<@principal_id>`；服务端解析令牌（不做显示名模糊匹配）：每个被提及者须为该频道当前成员（DEFAULT 频道按 BR-05-81 判定），否则 422 145003（`data.not_members[]`，UI 提示「X 不在本频道」，频道 OWNER / `channel_manage` 可〔邀请并发送〕）；已退役 Agent → 422 145016；每条 ≤20 个提及（422 145004）；不支持 `@all`/`@channel`（**本规格提议**：防通知风暴）。runtime 进程、机器、甲方联系人不是 Principal 或不是成员，无法被提及（PRD §5.3）。
- **BR-05-85 Agent 收到提及**：collab 写 `collab_message_mentions` → outbox `message.posted`；fleet 消费后经 04 通道 `agent.inbox{channel_id, message_id, seq, excerpt≤2000}` 投递给 ACTIVE·RUNNING 的 Agent（非运行 → 不投递，频道内系统 EVENT 提示「Ada 当前未运行，消息将在其启动后送达」，启动时补投最近 24 h 未投递项）。**D120 取代投递方式**：提及写入 Agent 收件箱（MENTION），通道只发不带内容的唤醒提示 `agent.wake`，Agent 按游标读取（BR-05-120…126）；「启动后送达」由收件箱保证，不再有 24 h 补投窗口。Agent 以自身凭证 `POST /channels/{id}/messages` 回复（`reply_to_message_id`）。
- **BR-05-86 「@Agent 请做 X」**：Agent 可做的唯一状态相关动作：(a) 以自身凭证创建 02 工作项草稿（需其角色允许，如 COORDINATOR）；(b) 若为主管 Agent，创建 `trigger=CHANNEL_REQUEST` 派单提案（`POST /entities/{p}/dispatch-proposals`，`requested_by`=原消息作者）。两者都产生 DISPATCH_CARD / 草稿卡片待人确认（BR-05-51）。执行者 Agent 被 @ 要求「开始做 W」→ 只能回复说明，不能自认领（02 BR-02-11）。
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

### 5.9 主管 Agent 自建 Agent（D110）

- **BR-05-97 谁能自建**：仅含 `LEAD` 角色、`origin=HUMAN_CREATED`（或已收编）、状态 ACTIVE·RUNNING 的 Agent，且 08 `SpawnGrantReader.Active(ctx, tx, tenant, supervisorAgentID)` 返回 ACTIVE 授权。权限码 `agent_spawn` **从不经角色授予**；08 `AuthorityChecker` 以策略规则 `spawn_grant` 判定（`authority_source=POLICY:spawn_grant`，`detail.grant_id`）。人类不走本流程（人用 F02 新建，`origin=HUMAN_CREATED`）。
- **BR-05-98 请求校验**（全部满足；失败码见 §6.9）：① 授权 ACTIVE 且在有效窗口内（142031）；② 项目 ∈ 授权 `projects`，且 07 `ProjectGate` 允许该项目执行；③ `role` ∈ 授权 `allowed_roles` ⊆ {EXECUTOR, COORDINATOR}（其他角色 142034；LEAD 或由自建 Agent 发起 142036）；④ `machine_id` ∈ `allowed_machine_ids`，`runtime_kind × model_id` ∈ 允许列表，`reasoning_effort` ≤ `max_effort`（142034）；⑤ `skill_ids` ⊆ 主管已挂载技能，`mcp_allowlist` ⊆ 主管 MCP 白名单（142034，`data.field`）；⑥ 该授权下未收编且非 RETIRED 的 Agent 数 + PROPOSED / APPROVED 请求数 < `max_active_agents`（142032）；⑦ 近 24 h 已创建 + 进行中请求 < `max_spawns_per_day`（142033）；⑧ 授权剩余预算 ≥ `per_agent_cap_micros`（142037）；⑨ 名称按 BR-05-01 校验。⑥–⑧ 由 08 `SpawnGrantUsage.Reserve` 在同一事务内锁定授权行判定，防并发超额。
- **BR-05-99 审批模式**：授权 `approval_mode=AUTO` 仅在项目 `team_mode=AUTONOMOUS` 时生效；HYBRID 项目一律按 REQUIRE_APPROVAL 处理（快照为 REQUIRE_APPROVAL，响应 `data.downgraded=true`）。REQUIRE_APPROVAL 时 08 生成收件箱项 `AGENT_SPAWN_APPROVAL`（接收人：对该项目持 `agent_manage` 的人类），批准**不需要** step-up。
- **BR-05-100 创建**：复用 BR-05-02 原子创建路径（服务账号 + 角色绑定 + 技能挂载 + 头像 + 审计 + outbox），差异：`origin=AGENT_SPAWNED`；`authorization_mode` 固定 `ON_REQUEST`（之后 F04 改为 FULL_AUTO 返回 142038）；角色绑定的权限 ⊆ 主管自身权限（底座防提权校验兜底）；`created_by_principal_id` = 主管服务账号；`expires_at = now + agent_ttl`；头像同风格，UI 加「✦」角标。创建后按 A02 自动投递凭证并启动。
- **BR-05-101 自建 Agent 的限制**（未收编期间）：不得持 LEAD / REVIEWER；不得成为 08 委托的受托方（08 授予时拒绝，`reason_code=SPAWNED_NOT_ADOPTED`）；不得自建；派单预过滤新增 **E12 `SPAWN_BUDGET`**（BR-05-40）。收编后上述限制解除，`origin` 不变。
- **BR-05-102 预算**：成本由 08 消费 03 `execution.cost_updated` 累加到授权；08 发布 `spawn_grant.exhausted` 后，本模块把该授权下自建 Agent 的 PROPOSED 派单提案置 EXPIRED，并在 E12 中排除（已在跑的执行继续完成）。单个 Agent 达 `per_agent_cap_micros` 时 08 发布 `spawn_grant.agent_cap_reached{agent_id}`，该 Agent 同样被 E12 排除。
- **BR-05-103 孤儿**：见 §4.7 L02，由 `fleet.spawn_lineage_watch` 判定。孤儿不参与派单、不可被 @（频道成员保留但标「孤儿」），只能收编或退役。
- **BR-05-104 到期与闲置**：`fleet.spawn_lifecycle` 每 10 min 扫描；`expires_at ≤ now` 或 `now − idle_since ≥ idle_retire` → 正常退役（交接文档由该 Agent 撰写，10 min 超时则系统摘要）。到期前 24 h 发 N-05-15 给授权人（可收编以保留）。`idle_since` 在 Agent 失去最后一个 ACTIVE 分派时写入，获得分派时清空。
- **BR-05-105 接管**：08 一键接管时同时撤销该项目的自建授权（08 侧），本模块按 L02 处理（`orphan_reason=TAKEOVER`）。
- **BR-05-106 可观测**：每个自建请求在项目频道发「自建请求」卡片（主管、规格摘要、理由、预计成本上限、授权用量）；批准 / 拒绝 / 创建 / 失败更新同一卡片。全部动作审计，可按 `spawn_grant_id` 过滤。


### 5.x 业务规则增补（D112 / D113，00 v2.0 §15）

- **BR-05-107 内部验收自动派单**：IQ1 / IQ2 由 `fleet.internal_qa_dispatch` 处理，同事务调 02 `DispatchAssigner.Assign(role=TESTER|ACCEPTOR, lease_ttl_seconds=3600)`（REVIEWER 类分派，不改工作项状态，02 已支持非迁移分派）并调 03 `ExecutionLauncher.Launch(purpose=INTERNAL_TEST|INTERNAL_ACCEPTANCE, subject_execution_id)`。内部验收的分派与执行**不进入任何人的收件箱**，频道只写折叠的系统事件。
- **BR-05-108 候选资格**：TESTER 候选 = 角色含 TESTER、ACTIVE·RUNNING、绑定项目、机器在线且有空槽；ACCEPTOR 候选另需机器具备 04 能力 `browser_capture`（模板要求 VISUAL 时）；均排除：被验收交付执行的执行者、该交付的派单者（主管）、同一工作项本轮已担任另一内部验收角色的 Agent（测试与验收不得同一主体）。
- **BR-05-109 跨模型家族优先**：在合格候选中按「`model_family` ≠ 被验收执行的家族」优先；若无，则回退到「模型不同（同家族）且主体不同」，提案标 `family_fallback=true`（03 记录并在评审台显示）；若仍无 → IQ3 升级。评分沿用 BR-05-41，`fpa_bp` 替换为该 Agent 作为内部验收者的「穿透率倒数」（其放行后被人或甲方发现问题的比例越低越好；无数据取 0.5）。
- **BR-05-110 计划审批者家族约束**：08 策略规则 `plan_approve` 对评审 Agent 的判定需本模块提供 `ModelFamilyOf(agent_id)`；评审 Agent 的家族必须与计划提出者（执行 Agent）不同，否则不得批准（返回 reason `SAME_MODEL_FAMILY`，由 08 登记）。主管 Agent 不得批准自己派出的计划（`SEPARATION_OF_DUTIES`）。
- **BR-05-111 纯人工模式（HUMAN_ONLY）**：项目 `team_mode=HUMAN_ONLY` 时：① 派单引擎对 Agent 关闭（P01 不为 Agent 生成 EXECUTOR / TESTER / ACCEPTOR / REVIEWER 提案；手工分派给 Agent → 422 **142041**）；② 只允许 `ASSISTANT` 角色的 Agent 绑定到该项目；③ 助理能力白名单：来件分诊草稿、工作项拆解草稿、会议纪要 → 行动项草稿（无负责人或截止日期的行动项不得转任务，回推给会议主持人）、状态 / 计划 / 报告草稿（10）、风险与对账提示；助理产出一律为草稿，需人确认后生效，且必须附来源链接；④ 助理不得提交完成声明、不得做任何裁决或验收（08 S2 硬拒，reason `HUMAN_ONLY_MODE`）。
- **BR-05-112 编制授权的角色边界**：主管创建请求 `role=TESTER` 受授权 `allowed_roles` 约束；`role=ACCEPTOR` 一律拒绝（142040）；已存在的 ACCEPTOR 被收编或退役不影响其他内部验收者资格。
- **BR-05-113 学习回流**：消费 `quality.escaped`（发布者 02 / 07 / 12 Part A，按 `escape_key` 去重；03 不发布，00 §7.1）：对参与该工作项内部验收的 TESTER / ACCEPTOR Agent 记一次「放行后穿透」，更新其内部验收绩效（BR-05-109 评分输入），并在 Agent 详情「记忆」标签显示经验条目（由 02 `delivery.escape_learning` 经 06 提出）。

### 5.y 业务规则增补（D118：会话空间与成员私信）

- **BR-05-114 成员私信（DIRECT）的范围**：DIRECT 是**团队级**会话，不属于任何项目（`project_id` 为空，不随项目关闭归档），恰好 2 名参与者：人↔人，或人↔Agent；Agent↔Agent 仍只用项目内 AGENT_DIRECT（BR-05-91）。只能由人类经 C18 创建（Agent 调用 → 403 145403，`reason_code=HUMAN_ONLY`）；对方须为本租户在职人类成员，或非 RETIRING / RETIRED 的 Agent（对方为 Agent 时调用者另需 `agent_read`，否则 403 145403 `MISSING_PERMISSION`）；对方为自己、非本租户主体、Agent 调用者想与 Agent 私信，或经 C02 传 `kind=DIRECT` → 422 **145020**。唯一键 `direct_pair_key = <小 principal_id>:<大 principal_id>`，部分唯一 `(tenant_id, direct_pair_key) WHERE kind='DIRECT'`；已存在 → 200 返回既有（幂等，并发创建只成功一行）。成员固定两行（`source=EXPLICIT`），不可改名、归档、加人、退出（409 145011）。
- **BR-05-115 私信可见性**：读、发、移动已读标记**只限两名参与者**。人↔人私信：任何第三方——包括团队所有者、`channel_manage`、`audit_read` 持有者——都不能读取消息、成员或修订（C03 / C05 / C08 / C12 → 403 145403 `NOT_MEMBER`，SSE 订阅拒绝），也不能撤回其中的消息；审计事件照常记录但不含正文（§7.3）。人↔Agent 私信：为对 Agent 行为可追责，另允许该 Agent 的负责人类（`fleet_agents.owner_principal_id`）与 `audit_read` 持有者**只读**（C03 / C05 / C08 / C12；不能发送、不能移动已读标记，发送 → 403 145403 `NOT_MEMBER`），入口为 Agent 详情「私信」标签（§8.3），不出现在其本人的会话左栏；人类参与者在详情栏与会话顶部看到「此私信对 Agent 的负责人与审计员可见」。他租户的私信 id → 404 145404。
- **BR-05-116 私信里的工作请求**：私信不是真相源，规则与 BR-05-86 相同：Agent 在私信中收到「请做 X」时，唯一可做的状态相关动作是 (a) 以自身凭证创建 02 工作项草稿（角色允许时），或 (b) 若为主管 Agent，在**已指明的项目**中创建 `trigger=CHANNEL_REQUEST` 派单提案（`requested_by` = 私信中的人类）；项目不明确时只回复询问。提案卡片（DISPATCH_CARD）照常写入该项目默认频道并待人确认（`requires_human=true`），私信中由系统写一条链接到提案的 EVENT（`ref_kind=dispatch_proposal`）。执行者 Agent 被要求「开始做 W」→ 只能回复说明，不能自认领、不能直接执行（02 BR-02-11）。Agent 在私信中可读取的数据不超出其自身授权。
- **BR-05-117 私信的提及与投递**：DIRECT 中每条 TEXT 自动对另一参与者写一行 `collab_message_mentions`（隐式提及）：对方为 Agent → 按 BR-05-85 投递（未运行时同样提示并在启动后补投）；对方为人 → N-05-09（私信变体）并计入 `mention_unread`。显式 `<@…>` 只能指向两名参与者，其他 → 422 145003（无〔邀请并发送〕）。速率与大小沿用 BR-05-88 / BR-05-92。
- **BR-05-118 参与者离开**：Agent 退役或人类成员被停用 / 移出租户后，DIRECT 保留历史、另一方仍可读，发送 → 422 145016；退役 Agent 的人↔Agent 私信仍按 BR-05-115 对其负责人与审计员只读。保留沿用 BR-05-93（不物理删除）。
- **BR-05-119 会话聚合与执行会话**：C17 只返回调用者自己的会话：可读的项目频道（BR-05-82）、本人参与的 DIRECT、按 BR-05-91 可只读的 AGENT_DIRECT（`include_agent_directs=true` 时）；不返回仅因审计而可读的人↔Agent 私信。「执行会话」区不由本模块存储：数据取自 03 A43（本人的执行，`kind=eq:INTERACTIVE` 且 `state=in:RUNNING,PAUSED`，或 `pending_interaction_count=gt:0`）与 08 我的裁决 I1 的「工具授权与输入」分类（本人可回应的交互），客户端合并去重并链到 03 交互会话工作台；回应仍走 03 / 08 接口与 `interaction_respond` 判定。03 目前没有「全租户内本人可回应的交互会话」读接口，超出这两个来源的会话不在此列（§12.1）。甲方已关联外部 Principal；C15 / C17 / C18 按共享频道、指定对接人和项目授权返回受限外部可见项，不返回内部 Agent / 执行会话。

### 5.z 业务规则增补（D120：Agent 运行时协议、操作清单、生命周期与提醒）

**收件箱与唤醒**

- **BR-05-120 收件箱是唯一事实来源**：Agent 需要处理的一切经 `fleet_agent_inbox_events` 到达，宿主不得依赖任何推送内容判断「有什么事」。投影来源（`fleet.inbox_projector`，按 `source_event_id` 幂等）：MENTION ← collab `message.posted` 的 AGENT 提及、03 `comment.thread_created` / `comment.posted` 的提及；MESSAGE ← DIRECT / AGENT_DIRECT 中对方的消息（BR-05-117 隐式提及只记一条，以 MENTION 为准）、Agent 发过言的线程中他人的回复、03 批注落在该 Agent 执行的产物上；DISPATCH ← 派单生效（`dispatch.applied` 受派者为 Agent）及本规格下发的工作命令（`agent.review` / `test` / `accept` / `triage` / `release_notes` / `lead_plan` / `plan_draft` / `report_draft`，与命令同事务写入，`source_event_id=command:<command_id>`）；REVIEW_FEEDBACK ← 02 `decision.made`（对象为该 Agent 的声明，含 `REQUEST_CHANGES` / `REJECT` 的结构化反馈）、03 `internal_qa.failed`（被验收执行的执行者）、03 `comment.converted{target_kind:FEEDBACK}`；REMINDER ← 本模块提醒触发（BR-05-139）；INTERACTION ← 03 `interaction.decided` / `interaction.closed`（交互所属执行的执行者为该 Agent）。不投影：EVENT 类频道消息、Agent 自己产生的事件、Agent 无读权限的对象（投影时按读权限复验，失去权限的事件不写入）。RETIRED Agent 不再写入。
- **BR-05-121 序号与游标**：`seq` 在 `fleet_agent_live_states` 行锁内 `inbox_head_seq+1` 分配，与事件插入同事务。宿主读取 F17 `?since=<seq>`（返回 `seq > since` 的事件，升序，`limit ≤200`）并在处理完成后 PUT F18 `{ack_seq}`：只增不减（小于当前值 → 200 不变，天然幂等），大于 `inbox_head_seq` → 422 **142044**。确认只表示「已读到这里」，不撤销任何业务对象；未确认的事件在下次唤醒或轮询时仍会返回（至少一次）。会话重置与完全重置**不改变**游标（BR-05-135）。
- **BR-05-122 唤醒提示不带内容**：每次写入收件箱后（同事务登记、提交后发送）向该 Agent 发唤醒提示，载荷只有 `{agent_id, inbox_seq}`（最新 head），不含标题、正文、对象 ID 或类型；提示可丢失、可重复、可合并（同一 Agent 1 s 内多次写入只发最后一个）。按 `wake_mode`：**CHANNEL**（默认）经 04 机器通道 `agent.wake`（WebSocket 即时推送；长轮询时立即返回本次 poll，04 BR-04-102）；**POLL** 不发提示，宿主每 ≤30 s 调 F17；**WEBHOOK**（v1.1）见 BR-05-124。Agent 不在 ACTIVE·RUNNING 时只写收件箱，不发提示（启动后宿主先按游标补读）。
- **BR-05-123 提示流心跳与兜底**：CHANNEL 模式下每个 `ServerBatch`（≤10 s 一次）都携带本机全部 Agent 的 `agent_inbox_heads[{agent_id, inbox_seq}]`（04 BR-04-102），作为提示流的心跳：宿主发现 `inbox_seq > 已确认 seq` 即读取，因此单个提示丢失最多延迟一个轮询周期。控制台与外部宿主可订阅 SSE 主题 `tenant/{t}/agent_inbox/{agent_id}`（F19），服务端每 15 s 发一次心跳帧 `{inbox_seq}`；30 s 未收到任何帧的订阅方须退回轮询。
- **BR-05-124 签名 webhook（v1.1）**：`wake_mode=WEBHOOK` 时向 `wake_webhook_url`（仅 https，拒绝回环 / 私网 / 链路本地地址，同 145019 规则）POST `{agent_id, inbox_seq, sent_at}`，头 `X-Wake-Timestamp`（Unix 秒）与 `X-Wake-Signature: sha256=<HMAC-SHA256(secret, timestamp + "." + body)>`；接收方须拒绝时间差 >300 s 的请求以防重放；超时 5 s，重试 5 次指数退避；连续 20 次失败 → 自动改为 POLL 并通知负责人（N-05-17）。秘密经 09 `SecretSealer` 存储，只在 F20 设置时返回一次；轮换即替换。webhook 不携带任何内容，接收方仍须以 Agent 凭证调 F17 读取。
- **BR-05-125 上限与保留**：每 Agent 未确认事件 ≤10,000；超出时最旧的未确认 DISPATCH / REVIEW_FEEDBACK / INTERACTION 保留，最旧的 MESSAGE / MENTION / REMINDER 被丢弃并写一条 `kind=MESSAGE, title="收件箱积压，已丢弃 n 条较早的消息与提醒"` 的系统事件（不静默丢失），同时 N-05-17；事件保留 30 天（`expires_at`），过期即使未确认也清理。ACTIVE·RUNNING 的 Agent 存在 >1 h 未确认事件 → N-05-17（每 Agent 每 24 h 一次）。
- **BR-05-126 与旧投递的关系**：`agent.inbox`（§6.6）废止；BR-05-85 中「启动后补投最近 24 h」由收件箱取代（启动后宿主按游标补读，保留期 30 天）。工作命令（§6.6 的 `agent.review` 等）保持原载荷（只含 ID 与截止时间，不含正文）与 ACK 语义，同时写 DISPATCH 事件；宿主以收件箱为准去重（`payload.command_id`）。

**过期上下文写入（`interrupted`）**

- **BR-05-127 写入流与 `seen_seq`**：下列写入在调用方为 **AGENT** 时必须携带请求字段 `seen_seq`（调用方读到的该流最新序号；缺失 → 本模块段 422「seen_seq_required」：collab **145022**、02 **120470**、03 **130475**、12 **126062**）；人类调用方可选（传了即同样判定）。流的定义与「更新事件」的判定（00 §9）：
  - `channel:<id>`（会话）：C09 发顶层消息时，`collab_messages` 中 `seq > seen_seq`、作者 ≠ 调用者、`kind ∈ {TEXT, DECISION_CARD, DISPATCH_CARD}`、非线程回复、未撤回的消息；
  - `thread:<root_message_id>`（线程，序号仍用频道 `seq`）：C09 带 `reply_to_message_id` 时，根消息被编辑 / 撤回或该线程中 `seq > seen_seq` 的他人回复；
  - `work_item:<id>`（工作项）：02 `delivery_work_items.activity_seq`，判定为 02 `delivery_work_item_activities` 中 `seq > seen_seq` 且主体 ≠ 调用者的活动（02 BR-02-64）；适用 02 A1（认领 / 评审认领）、A4（释放）、C1（提交完成声明）、D1（经委托的裁决）、12 A13（流转）、03 A27 / A28（证据提交 / 修改，执行关联工作项时）、03 A77（新建批注线程，产物所属执行关联工作项时）；
  - `comment_thread:<id>`：03 A79 回复批注时，该线程中 `seq > seen_seq` 的他人批注或线程状态变化。
  **豁免**：Andon 停线、停止 / 暂停自己的执行、收件箱确认、编辑 / 撤回自己的消息——这些动作不能因上下文过期而被阻止。
- **BR-05-128 `interrupted` 响应与重试**：判定在写入事务内、取得流的行锁之后、其他业务守卫之前执行；存在更新事件 → **不写任何数据**，返回 HTTP 409，业务码为所属模块的 `write_interrupted`（collab **145021**、02 **120469**、03 **130474**、12 **126061**），`data = {state:"interrupted", stream, seen_seq, current_seq, newer_events:[…] ≤50, truncated}`（结构见 00 §9；会话流的 `newer_events` 为完整 `Message` 表示，其他流为 `{seq, kind, actor{kind,id}, ref{kind,id}, occurred_at, title}`），`meta.next = [{operation:"inbox_events.list"…}, {operation:<原操作>, reason:"RETRY_WITH_SAME_IDEMPOTENCY_KEY"}]`。`interrupted` 响应**不记入** 09 C7 幂等表，且 `seen_seq` 不参与请求哈希（00 §9）：Agent 读完更新事件后可用**同一个 `Idempotency-Key`**、新的 `seen_seq` 重试；若决定放弃，则不重试。重试仍可能再次 `interrupted`（又有新事件），宿主应在 3 次后改为回复或升级而不是无限重试。
- **BR-05-129 竞争认领**：两个 Agent 基于同一 `seen_seq` 竞争同一写入（如两个持委托的评审 Agent 对同一工作项评审认领）时，先提交者成功并使流前进；后到者得到 `interrupted`（`newer_events` 含前者的认领）；它若仍以同一幂等键重试，则落到原有冲突规则（02 BR-02-14 / BR-02-15：409 120426 / 120446，`data.holder`）。`interrupted` 不替代任何既有的唯一约束或 CAS，只是让 Agent 在冲突前先看到原因。

**操作清单与 `next`**

- **BR-05-130 清单内容与发布**：每个模块在启动时经本模块 `OperationRegistry.Register(op)`（00 §8）登记它允许 Agent 调用的每个操作；清单 = 全部登记项按名称排序后的文档（Schema S-M1，§6.z），每项含：`name`（`<资源>.<动词>`，如 `channel_message.post`）、`summary`、`transport`（HTTP `{method, path}` 或机器通道消息类型）、`input_schema` / `output_schema`（JSON Schema 2020-12，取自该接口的请求 / 响应定义）、`side_effects[]`（`OperationSideEffect`）、`idempotency{required, key_rule}`（如「每个逻辑动作一个新键，`interrupted` 后用原键重试」）、`stale_context{stream, seen_seq_required}`（BR-05-127）、`permission{code, via: ROLE|DELEGATION|POLICY|RELATION|SELF, policy_rule?, relation?}`（权限码取自 00 §6.1，与路由守卫登记的码一致，08 BR-08-67；`SELF` = 只要求调用者是该 Agent 本人的服务账号，`code` 为空——收件箱、提醒、能力视图、本人可用工具；`RELATION` = 关系授权，如项目主管 Agent、负责人类，`relation` 写明关系名）、`delegable`（码 ∉ 00 §6.4）、`human_only`（码 ∈ 00 §6.5 / 08 `HumanOnly` 时恒为 true，Agent 永远不可调用，仍列出以便解释）、`human_confirmation`（`HumanConfirmation`）、`quota`（NONE / SPAWN_GRANT / PROJECT_BUDGET / RATE_LIMIT）、`phase`、`deprecated_since?`。启动时 `fleet.manifest_publish` 计算规范化文档的 sha256，与最新版本不同则写入新版本：只增加操作或可选字段 → MINOR+1；删除操作、收紧输入 → MAJOR+1（旧版本保留可读）；文案变化 → PATCH+1。登记项缺 `input_schema` 或权限码未在 00 §6.1 登记 → 启动失败（禁止静默降级）。
- **BR-05-131 结果带 `next`**：清单内操作的成功与失败响应都在 `meta.next` 返回建议的下一步（数组，可为空，≤5 项）：`{operation, reason, args?}`，`reason` 取值：`READ_NEWER_EVENTS`、`RETRY_WITH_SAME_IDEMPOTENCY_KEY`、`ACK_INBOX`、`ATTACH_EVIDENCE`、`SUBMIT_CLAIM`、`WAIT_FOR_HUMAN`（结果需人确认时）、`REPLY_IN_THREAD`、`ESCALATE_TO_OWNER`、`NONE_AVAILABLE`。`next` 只是建议，平台不据此授权；`next` 中的操作仍按清单与权限判定。人类调用方同样收到（控制台忽略）。
- **BR-05-132 能力判定与 `CAPABILITY_NOT_AUTHORIZED`**：Agent 调用清单内操作被拒时，响应仍为所属模块段 `xx0403`，`data.reason_code` 为 08 §3.5 的原因码（不变），另加 `data.result = "CAPABILITY_NOT_AUTHORIZED"` 与 `data.operation`；原因码到该结果类别的映射由 08 §3.5c 唯一定义。M03 能力视图对每个操作给出 `status ∈ {AVAILABLE, CAPABILITY_NOT_AUTHORIZED, QUOTA_EXHAUSTED, NOT_APPLICABLE}` 与原因码，判定使用 08 `AuthorityChecker.Check` 的只读路径（不写库、不占配额）。调用清单外的接口不受影响（仍按各模块守卫），但 `meta.next` 为空且不保证向后兼容。
- **BR-05-133 按操作的配额与人确认**：`quota=SPAWN_GRANT` 的操作（`agent_spawn_request.create`，D110 G01）在处理前先做只读预检（08 `SpawnGrantReader.Active` + 用量），失败按原码返回（422 142031–142038），权威占用仍是同事务内的 `SpawnGrantUsage.Reserve`（BR-05-98，防并发超额）；`quota=PROJECT_BUDGET` 的操作（启动执行、并行求解）预检 07 `ProjectGate`；`quota=RATE_LIMIT` 的操作沿用各自限流（如 BR-05-88）。`human_confirmation=ALWAYS` 的操作（频道 / 私信派单请求 D02 `CHANNEL_REQUEST`）返回的对象处于待人确认状态，`meta.next=[{reason:"WAIT_FOR_HUMAN"}]`；`BY_POLICY`（自建请求、计划提交）在响应中标明实际生效方式（如 `downgraded=true`）。

**活动状态、重置与查看范围**

- **BR-05-134 五态上报与派生**：宿主在 `agent.status` 上附带 `activity`（ONLINE / THINKING / WORKING / ERROR）与 `activity_detail`，状态变化即报（同一 Agent ≥2 s 一次，合并），并随每次 poll 的 `agents[]` 快照上报（04 `AgentProcState`）。服务端派生规则（优先级从高到低）：AgentState=CRASHED → ERROR；process_status ≠ RUNNING、机器 OFFLINE、AgentState ≠ ACTIVE → OFFLINE；否则取宿主上报值；旧版宿主不报 `activity` → ONLINE，且有本 Agent 的 RUNNING 执行时为 WORKING。活动状态只经 SSE 推送与 F03 / F01 返回，不发领域事件、不审计；ERROR 持续 ≥10 min → N-05-16。
- **BR-05-135 三种重置**：**重启**（RESTART）= 既有 F06 / A12 / A13：重启宿主进程，保留会话与工作目录。**会话重置**（SESSION）：清空 Agent 宿主的常驻会话上下文（宿主丢弃 `session_epoch` 之前的会话与原生会话 ID），保留工作目录（Agent 私有目录中的笔记、仓库等）与 06 长期记忆；进行中的宿主轮次取消，Agent 的执行（03）不受影响。**完全重置**（FULL）：会话重置 + 清空 Agent 私有目录（`scope:"AGENT_HOME"`，与退役清理同范围，**不碰项目工作目录与执行工作区**）；06 长期记忆不在重置范围（撤销记忆走 06）。三种重置都不改变收件箱游标、角色、授权、技能挂载与凭证。
- **BR-05-136 完全重置仅限人类**：FULL 只能由人类发起（Agent / 服务账号 → 403 140403 `AGENT_PROHIBITED`），需 `agent_manage` + step-up `agent_full_reset`（target `agent:<id>`，08 §6.9）+ 输入 Agent 名称完全相等（不符 422 **142050**）；该 Agent 有非终态执行 → 409 142012（先停止或等待结束）；机器非 ONLINE → 409 142002。SESSION 需 `agent_manage` 与 HUMAN，不需 step-up。两者均审计（`reason` 必填 ≥10 字）。
- **BR-05-137 工作目录与记忆的查看范围**：F12 / F13（工作目录）与 Agent 详情「记忆」标签（数据来自 06 A30 `GET /agents/{agent_id}/memories`）只对两类人类开放：该 Agent 的负责人类（`owner_principal_id`，经 08 关系授权隐式持有）与持 `agent_workspace_read` 的成员（默认仅 `team_owner`，08 §3.3d）；其他人（含持 `agent_read` 的成员与其他 Agent）→ 403 140403（`MISSING_PERMISSION`，`data.missing_permission=agent_workspace_read`）。Agent 自身读自己的记忆不受影响（06 BR）。每次读取文件内容审计 `fleet.agent_workspace_read`（路径 SHA-256，不含路径原文与内容）。06 A30 的人类读者按同一规则收窄（06 BR-06-66，§13 CCR-05-14）。负责人关系由本模块 `AgentDirectory` 提供的 `owner_principal_id` 作为判定事实交给 08（08 BR-08-69）。

**提醒**

- **BR-05-138 创建与上限**：Agent 以自身凭证为**自己**创建提醒（为他人创建 → 403 140403 `NOT_OWNER`）；人类为某 Agent 创建需 `agent_manage`；锚点须当前可读（THREAD：频道成员且根消息未撤回；WORK_ITEM：`work_item_read`），否则 422 **142047**。每 Agent ACTIVE + PAUSED 提醒 ≤50（422 **142046**）；INTERVAL ≥15 min、ONCE 的 `fire_at` 在 now+1 min 至 now+365 天之间（422 142047）；LOOP_CONTRACT 提醒只由 10 经接口创建 / 修改 / 取消，HTTP 修改 → 409 **142048**。
- **BR-05-139 触发**：`fleet.reminder_fire` 每 30 s 扫描 `state='ACTIVE' AND next_fire_at ≤ now`（每批 ≤500，逐条 CAS）：写收件箱 REMINDER（`stream_key` = 锚定线程 `thread:<root>` 或 `work_item:<id>`，`stream_seq` = 触发时该流的最新序号，`payload{reminder_id, fire_no, loop_run_id?}`）并唤醒；Agent 离线时同样写入（错过的多次触发**合并为一条**，`payload.missed_fires=n`）。提醒不向线程发任何消息；Agent 收到后如需发言，按 BR-05-127 携带 `seen_seq` 发到该线程。
- **BR-05-140 循环契约提醒**：10 激活循环契约时在同一事务内调 `AgentReminderScheduler.UpsertForLoopOn(ctx, tx, {loop_contract_id, agent_id, anchor{channel_id, thread_root_message_id}, schedule})`，暂停 / 归档时调 `PauseForLoopOn` / `CancelForLoopOn`；触发时本模块在写收件箱的同一事务内调 10 `LoopRunRecorder.OpenOn(ctx, tx, loop_contract_id, fire_no, scheduled_for)` 取得 `loop_run_id`（10 返回 `skip=true` 时——如上一次运行仍未结束且契约不允许重叠——只写一条 `payload.skipped=true` 的 REMINDER）。运行的预算、自检、升级与报告核对全部由 10 判定（10 BR-10-52…58）。

**团队工具目录（MCP 服务器与技能）**

- **BR-05-141 目录与条目**：每个团队一个工具目录，条目两类：**MCP_SERVER**（本模块保存传输、配置版本与凭据）与 **SKILL**（06 技能库中的技能在目录中的镜像：06 首个版本批准时自动建条目、`entry_key = skill_key`，06 归档时条目 ARCHIVED；版本号、内容与 `content_hash` 一律取 06 已批准的技能版本，本模块不复制内容，06 BR-06-67）。管理（建、改、发布版本、启停、授予、凭据）只限 HUMAN 且持 `tool_catalog_manage`（08 §3.3d：仅限人类、不可委托，默认仅 `team_owner`）；Agent / 服务账号调用 → 403 140403（`AGENT_PROHIBITED`）；人缺码 → 403 140403（`MISSING_PERMISSION`）。读取目录沿用 `agent_read`。SKILL 条目经 T02 新建 → 422 142052（`data.field=kind`，「技能请在知识库中创建」）。
- **BR-05-142 MCP 条目、版本与传输**：`STREAMABLE_HTTP` 的 `config.url` 须为 https，拒绝回环 / 私网 / 链路本地地址（同 145019 规则）；`STDIO_PACKAGE` 的 `config.package` 须带完整性哈希（npm `integrity`、PyPI sha256 或 files 的 `file_id` + sha256），MVP 中 `credential_mode` 只能为 NONE（需要凭据的本地进程会把明文放到机器上，违反「凭据不下发机器」，v1.1 再议）——违反任一 → 422 **142052**（`data.field`）。T05 发布的版本不可变，`content_sha256 = sha256(JCS(config))`；与当前版本哈希相同 → 409 **142058**。版本可被人工 REVOKED（T07 版本子操作 `{version_no, state:"REVOKED"}`）：REVOKED 版本不再被新执行解析，中继拒绝持该版本令牌的调用；当前版本被撤回时 `current_version_no` 回退到最近一个未撤回版本，没有则条目自动 DISABLED。
- **BR-05-143 授予（默认无）**：条目只有被授予才可被使用；授予主体为 **PROJECT**（该项目内的全部执行）、**ROLE**（持该 `AgentRole` 的 Agent，可限定项目）、**AGENT**（单个 Agent，可限定项目）。覆盖判定 `covered(entry, agent, project)` = 存在 ACTIVE 授予满足以下任一：PROJECT 且 `project_id=project`；ROLE 且 `role ∈ agent.roles` 且（`project_id` 为空或 = project）；AGENT 且 `agent_id=agent` 且（`project_id` 为空或 = project）。授予与撤销只经 T09 / T10（HUMAN、`tool_catalog_manage`）；Agent 不能为自己或他人授予。同一主体重复授予 → 200 返回已有行（天然幂等）；主体不存在、角色非法、条目 ARCHIVED → 422 **142054**。撤销对新执行立即生效，对运行中的执行在 ≤60 s 内生效（中继按调用复验覆盖，缓存 ≤60 s）。
- **BR-05-144 有效工具集（按执行解析）**：03 派发执行时在派发事务内调 `ToolsetResolver.ResolveOn`（§6.z），按「授予是闸门、Agent 配置是选择」解析：**目录 MCP** = ACTIVE 条目中 `covered(…)` 为真且被选中的（Agent 执行：`catalog:<entry_key>` ∈ Agent `mcp_allowlist`；人类发起的 INTERACTIVE / ADHOC：会话配置中选中的、仅 PROJECT 授予覆盖的条目），版本取解析时的 `current_version_no`；**目录技能** = 06 `SkillCatalog.ForAgent` 返回的已挂载技能中 `covered(…)` 为真且条目未 DISABLED 的，版本取 06 解析出的版本；**本机自带**（`ToolSource=MACHINE_LOCAL`）= 绑定机器最近一次 04 能力检测中不属于目录的 MCP（仍按 BR-05-05 只在 `mcp_allowlist` 中时注入）与运行时自行发现的本机技能。结果 `Toolset{revision, entries[{source, kind, entry_key, version_no?, content_sha256?, transport?}], warnings[]}`（`revision` = 规范化条目列表的 sha256）返回 03 记录为执行证据（03 BR-03-147）。被覆盖但缺凭据、版本已撤回的条目不进入工具集，记入 `warnings[]`。单个执行 ≤30 个技能、≤20 个 MCP，超出按条目名排序截断并记 warning。
- **BR-05-145 分发与版本冻结**：工具集随 03 `execution.start` 下发（载荷见 03 §6.3 增补），由 04 机器程序落地（04 BR-04-105）：技能内容按版本写入该执行的运行时技能目录；目录 MCP 写入该执行的运行时 MCP 配置——STREAMABLE_HTTP 指向机器本地代理，经本模块中继 T13 访问上游；STDIO_PACKAGE 由机器按完整性哈希安装后在执行内启动。**版本在执行开始时冻结**：之后发布的新版本（含 06 批准的新技能版本）只影响之后开始的执行；同一执行的重试 / 返工分支是新的派发，重新解析。Agent 常驻宿主（F16）只取技能（AGENT / ROLE 授予且未限定项目者），目录 MCP 只注入执行。
- **BR-05-146 凭据与工具令牌**：凭据经 T08 设置或轮换（HUMAN、`tool_catalog_manage`、step-up `tool_credential_manage`，08 §6.9），用 09 `SecretSealer` 封装存储；任何接口只回显 `last4` 与 `rotated_at`；明文只在中继处理单次请求时于内存中解封，**永不**下发机器、写入执行载荷、日志、审计或事件。轮换即替换（旧密文不保留），下一次中继调用起使用新凭据，运行中的执行无需重启。机器只拿到**工具令牌**：由 `ToolsetResolver.IssueTokens` 为每个（执行 × 目录 MCP 条目）签发，HMAC 签名、不透明，绑定 `{tenant_id, execution_id, dispatch_epoch, entry_id, version_no}`，有效期 ≤15 min，随 03 `leases.renew` 续签；执行进入终态、被栅栏（FENCED）或租约过期后令牌立即失效。令牌只保存在机器程序内存中，不进入运行时进程环境、参数或配置文件（04 BR-04-107）。
- **BR-05-147 MCP 中继**：T13 为 Streamable HTTP 透传端点（JSON-RPC，不使用 `respx` 信封）。每个请求依次校验：令牌签名与有效期（否则 HTTP 401，`data.code=142056`）→ 执行非终态且 `dispatch_epoch` 当前（经 03 `ExecutionReader.Get`）→ 条目 ACTIVE、令牌版本未 REVOKED → `covered(…)` 仍为真；后三项不满足 → HTTP 403，JSON-RPC 错误 `{code:-32001, message:"capability not authorized", data:{reason_code:"TOOL_NOT_GRANTED"|"EXECUTION_NOT_ACTIVE"|"TOOL_DISABLED"}}`，审计 `fleet.tool_call_denied`。通过后按令牌中的版本配置连接上游、注入凭据头并透传；每个 `tools/call` 写审计 `fleet.tool_called`（`entry_key`、`version_no`、工具名、`execution_id`、`agent_id`、入参 SHA-256、结果 ok / error、耗时；**不含**入参与结果内容，08 BR-08-68）。上限：请求体 ≤4 MiB、响应 ≤16 MiB、每执行并发 ≤10（超出 429）、上游超时取版本配置（≤120 s，超时 502 `142057`）。上游连续 5 次返回 401 / 403 → N-05-18（每条目每 24 h 一次），条目保持 ACTIVE 并在目录中标「凭据可能失效」。STDIO_PACKAGE 与本机自带 MCP 的调用不经中继，只以 03 执行事件的 `tool` 行记录。
- **BR-05-148 本机自带项**：04 能力检测（S3）列出机器上运行时可见的技能与 MCP；本模块在 T12 与 Agent 详情中逐项标注：名称与目录条目相同且（技能）内容哈希等于某个目录版本 → 「团队目录 v{n}」；名称相同但哈希不同 →「与团队目录 v{n} 不一致」；其余 → 「本机自带，未纳入团队目录」。本机自带项保持既有可用性（MCP 仍按 BR-05-05 白名单注入；技能由运行时自行发现），但不受授予约束、不经中继、不托管凭据，执行证据中记为 `MACHINE_LOCAL`。持 `tool_catalog_manage` 者可对本机 MCP〔纳入团队目录〕（T02 `from_local`：按检测结果预填名称与传输，凭据须重新在平台设置）；本机技能须先导入 06 技能库（06 A51）。
- **BR-05-149 推荐预设**：T11 列出系统内置预设（如代码托管、项目管理、错误监控、浏览器自动化等常用 MCP 服务器）；T02 `{preset_key}` 按预设生成 DRAFT 条目与 v1 配置草稿，占位符未填或凭据未设置时不可激活（422 142052 / 142055）。预设只是模板，不自动授予任何主体。
- **BR-05-150 上线迁移与兼容**：本功能上线时（一次性任务 `fleet.tool_catalog_migration`）为每个已存在的 06 挂载（`knowledge_agent_skills`）生成 AGENT 授予（`granted_by_kind=SYSTEM`，`SYSTEM:fleet.tool_catalog_migration`），使既有 Agent 的技能不因「默认无」而丢失；既有 `mcp_allowlist` 中的名称视为本机自带项，行为不变。迁移幂等、审计一次汇总事件。
- **BR-05-151 上限**：每团队 ≤200 个 MCP 条目；每条目 ACTIVE 授予 ≤500；每条目版本不删除（列表分页）；凭据 ≤8 KB。超出 422 142052（`data.field=limit`）/ 142054。
- **BR-05-152 停用与撤回的效果**：DISABLED 是紧急止血：新执行不再解析，运行中的执行在下一次调用即被中继拒绝（`TOOL_DISABLED`）；已下发到机器的技能文件不追删（执行结束随运行目录清理），但 03 证据如实记录该执行使用过的版本。重新 ACTIVE 后只对之后开始的执行生效。

## 6. 接口契约

约定（与 02/03/04/07 一致）：huma v2 + `respx` 信封；ID 十进制字符串；OpenAPI tag = 最终路径首段（CR-03-07 / CR-04-02）；守卫记号 **T** = `authz.Register`（租户级，路径前缀 `/tenants/{tenant_id}`）、**E** = `authz.RegisterEntity`（`entity_id` = 项目实体）、**O** = `authz.RegisterOwned`（全局短地址，按资源反查所属项目 / 租户后授权；作用域地址为同一操作，归属不符 404）；乐观锁 = 响应头 `ETag: "<version>"`、写请求 `If-Match`（缺 428、不符 409，表中记「v」）；「✔」= 支持 `Idempotency-Key`（09 C7 平台辅助，24 h；同 key 不同请求 176425，进行中 176409）。权限拒绝统一 `<段>403` + `data.reason_code`（MISSING_PERMISSION / NOT_MEMBER / DIRECT_READ_ONLY / REAUTH_REQUIRED / SELF_MANAGE / HUMAN_ONLY 等）。列表一律 `pkg/query`（`field=op:value`，cursor 分页（00 §16.10；W1 确认底座复用 / 最小扩展），`capabilities` 声明）。fleet 通用码沿用 04：140400 / 140403 / 140404 / 140409 / 140428；collab：145400 / 145403 / 145404 / 145409 / 145428。

### 6.1 Agent（fleet）

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| F01 | GET `/tenants/{tenant_id}/agents` | tenants | `agent_read` · T | 过滤 `state`(eq,in) `role`(eq) `machine_id`(eq) `runtime_kind`(eq) `model_id`(eq) `project_id`(eq，经角色绑定) `name`(contains) `process_status`(eq)；排序 `name` `created_at` `state` `fpa_30d` `load_ratio`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `Agent[]`（摘要 + `stats_30d` + `load`） | — | — |
| F02 | POST `/tenants/{tenant_id}/agents` | tenants | `agent_manage` · T；HUMAN；FULL_AUTO 另需 step-up | `name*` `handle?` `persona?` `avatar_style?` `avatar_seed?` `system_prompt*` `machine_id*` `runtime_kind*` `model_id*` `reasoning_effort?` `authorization_mode?` `max_context_tokens?` `roles*[]` `skill_ids?[]≤20` `mcp_allowlist?[]` `capability_tags?[]` `default_channel_names?[]` `memory_policy?` `max_parallel_items?` `max_risk?` `auto_restart?` `start?`(默认 true) `reauth_proof?` | `Agent`（201，DRAFT，`credential_delivery=PENDING`） | 422 142001/03/04/05/07/09/22/26；409 142002；503 142030；403 140403（含 REAUTH_REQUIRED） | ✔ |
| F03 | GET `/agents/{agent_id}` | agents | `agent_read` · O | `?include=stats,load,roles,skills` | `Agent` 全量；ETag | 404 140404 | — |
| F04 | PATCH `/agents/{agent_id}` | agents | `agent_manage` · O；HUMAN | 可选 `name` `persona` `avatar_style` `avatar_seed` `system_prompt` `reasoning_effort` `authorization_mode` `max_context_tokens` `roles[]` `mcp_allowlist[]` `capability_tags[]` `default_channel_names[]` `memory_policy` `max_parallel_items` `max_risk` `auto_restart` `reauth_proof?` | `Agent` | 409 142010/140409；422 同 F02；428 140428 | v |
| F05 | PUT `/agents/{agent_id}/run-state` | agents | `agent_manage` · O | `{desired:"ACTIVE"\|"STOPPED"}` | `Agent`（202，含 process_status） | 409 142002/142008/142010/142011；422 142003 | 天然幂等 |
| F06 | POST `/agents/{agent_id}/restarts` | agents | `agent_manage` · O | `{reason?≤2000}` | `{agent, process_event_id}`（202） | 409 142002/142010/142011 | ✔ |
| F07 | PUT `/agents/{agent_id}/machine-binding` | agents | `agent_manage` · O；HUMAN | `{machine_id*, runtime_kind*, model_id*, reasoning_effort?}` | `Agent`（`credential_delivery=PENDING`） | 409 142010/142012；422 142003/04/07/13/26 | v |
| F08 | POST `/agents/{agent_id}/credential-deliveries` | agents | `agent_manage` · O | — | `{credential_delivery}`（202） | 409 142002/142010 | ✔；30 s 内合并 |
| F09 | GET `/agents/{agent_id}/process-events` | agents | `agent_read` · O | 过滤 `kind`(in) `occurred_at`(gte,lte)；排序 `occurred_at`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `ProcessEvent[]` | 404 | — |
| F10 | GET `/agents/{agent_id}/stats` | agents | `agent_read` · O | `?window_days=30\|90&work_item_type=` | `{fpa, fpa_n, cpa_micros, rework_rate, claims, accepted_items, load{active_items, max_parallel_items, running_executions}, computed_at, by_type[]}` | 404 | — |
| F11 | GET `/agents/{agent_id}/avatar-candidates` | agents | `agent_read` · O | `?style=` | `[{style, seed, url}] ×6` | 404 | — |
| F12 | GET `/agents/{agent_id}/workspace-entries` | agents | `agent_read`+`machine_read` · O（D120：另须为负责人类或持 `agent_workspace_read`，BR-05-137） | `?path=`（相对根） | `{root, path, entries:[{name, kind, size, modified_at}] ≤1000, truncated}` | 409 142002；422 142027 | — |
| F13 | GET `/agents/{agent_id}/workspace-entries/content` | agents | 同上 | `?path=` | 文本 / 二进制流（≤1 MiB，`Cache-Control: no-store`） | 413 142028；422 142027 | — |
| F14 | GET `/agents/{agent_id}/direct-channels` | agents | `agent_read` · O + 每频道按项目 `channel_read` 行过滤 | 过滤 `project_id`(eq)；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `Channel[]`（AGENT_DIRECT） | — | — |
| F15 | GET `/tenants/{tenant_id}/roster-entries` | tenants | 租户成员 · T；Agent 行另需 `agent_read` | 过滤 `kind`(eq HUMAN\；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） |AGENT) `role`(eq) `state`(in) `project_id`(eq) `q`(contains)；排序 `name` `load_ratio` `kind` | `RosterEntry[]`：`{kind, principal_id, agent_id?, name, avatar, roles[], state, load{active_items, max?}, current_work_items[≤3], last_active_at}` | — | — |
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
| D01 | GET `/entities/{entity_id}/dispatch-proposals` | entities | `work_item_read` · E | 过滤 `state`(eq,in) `result`(eq) `work_item_id`(eq) `trigger`(in) `requires_human`(eq) `created_at`(gte)；排序 `created_at` `expires_at`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `DispatchProposal[]`（含推荐人、top-3） | — | — |
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
| H01 | POST `/work-items/{work_item_id}/ensemble-plans` | work-items | 人：`work_item_assign`+`execution_start` · O；主管 Agent：经 08 `AuthorityChecker`（`POLICY:ensemble_branch`，不经角色码） | `{trigger*, branch_count?, agent_ids?[]（须合格）, budget_cap_micros?, deadline_hours?(1–24), reason*≤2000}` | `EnsemblePlan`（201；PENDING_APPROVAL 或 RUNNING） | 422 143009/143010/143011；409 143016/130426 | ✔ |
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
| S→M | `agent.inbox`（**D120 起废止**，由 `agent.wake` 取代，§6.z） | `{command_id, agent_id, items:[{channel_id, message_id, seq, excerpt≤2000, trigger_depth}]}` | 宿主转交 Agent |
| S→M | `agent.lead_plan` | `{command_id, agent_id, proposal_id, candidates[≤5], work_item, criteria[], retrieval_refs[], deadline_at}` | 宿主调用模型，经 `agent.lead_plan_result` 回传 |
| S→M | `agent.workspace_list` / `agent.workspace_read` | `{request_id, agent_id, path, max_entries\|max_bytes}` | 15 s 超时；结果经 `agent.workspace_result` 回传 |
| S→M | `agent.review` | `{command_id, agent_id, work_item_id, claim_id, deadline_at}` | 宿主让评审 Agent 读取声明 / 证据 / 验证，并以自身凭证调 02 裁决接口（BR-05-53） |
| S→M | `agent.plan_draft`（可选，D111） | `{command_id, agent_id, plan_id, period_kind, period_start, deadline_at}` | 宿主让 Agent 依据其目标（10 `GoalReader`）与待办起草下周期计划，并以自身凭证调 10 计划接口写入（只起草、不可确认） |
| S→M | `agent.report_draft`（可选，D111） | `{command_id, agent_id, report_id, sections:["retrospective"], deadline_at}` | 宿主让 Agent 撰写本周期复盘并以自身凭证调 10 报告接口写入；完成 / 未完成清单由系统自动填充，Agent 不得改动事实部分 |
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
| C01 | GET `/entities/{entity_id}/channels` | entities | `channel_read` · E | 过滤 `kind`(in) `archived`(eq，默认 false) `name`(contains) `member`(eq me)；排序 `last_message_at` `name` | `Channel[]`（含 `unread`、`mention_unread`；AGENT_DIRECT 需 `kind` 显式过滤）；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | — | — |
| C02 | POST `/entities/{entity_id}/channels` | entities | CUSTOM：`channel_post`（**本规格提议**：成员可自建）· E；AGENT_DIRECT：调用者须为 AGENT | CUSTOM `{kind, name*, topic?, member_principal_ids?[]≤200}`；DIRECT `{kind:"AGENT_DIRECT", peer_agent_id*}` | `Channel`（201；DIRECT 已存在 → 200） | 422 145001/145002/145012/145014；403 145403（DIRECT_READ_ONLY） | ✔ |
| C03 | GET `/channels/{channel_id}` | channels | `channel_read` · O + BR-05-82 | — | `Channel`；ETag | 404 145404；403 145403（NOT_MEMBER） | — |
| C04 | PATCH `/channels/{channel_id}` | channels | `channel_manage` 或 OWNER · O | `{name?, topic?, archived?:bool}` | `Channel` | 409 145011/145008/145409；422 145001 | v |
| C05 | GET `/channels/{channel_id}/members` | channels | 同 C03 | 过滤 `member_kind`(eq) `q`(contains)；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `ChannelMember[]` | — | — |
| C06 | PUT `/channels/{channel_id}/members/{principal_id}` | channels | `channel_manage` 或 OWNER · O | `{role?:"MEMBER"\|"OWNER"}` | `ChannelMember` | 409 145011/145008；422 145012/145016；404 | 天然幂等 |
| C07 | DELETE `/channels/{channel_id}/members/{principal_id}` | channels | 同 C06；本人可退出 CUSTOM | — | 204 | 409 145011/145018 | 幂等 |
| C08 | GET `/channels/{channel_id}/messages` | channels | 同 C03 | 过滤 `seq`(lt,gt,lte,gte) `kind`(in) `fold_key`(eq) `ref_id`(eq) `author_id`(eq) `reply_to_message_id`(eq，D118 线程)；排序 `seq`（默认 desc）；`limit ≤100`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `Message[]` + `meta.max_seq` | — | — |
| C09 | POST `/channels/{channel_id}/messages` | channels | `channel_post` · O + 成员 | `{body*≤8000, reply_to_message_id?, attachment_file_ids?[]≤5}`（kind 恒为 TEXT） | `Message`（201） | 422 145003/145004/145016/145017；413 145005；409 145008；403 145403（NOT_MEMBER / DIRECT_READ_ONLY）；429 145010 | ✔ |
| C10 | PATCH `/messages/{message_id}` | messages | 作者本人 · O | `{body*}` | `Message` | 409 145006/145409 | v |
| C11 | DELETE `/messages/{message_id}` | messages | 作者（24 h 内）或 `channel_manage` · O | — | 204（撤回，不物理删除） | 409 145006 | 幂等 |
| C12 | GET `/messages/{message_id}/revisions` | messages | `audit_read` · O | — | `Revision[]` | 403 | — |
| C13 | PUT `/channels/{channel_id}/members/{principal_id}/read-marker` | channels | 本人 · O | `{seq*}` | `{last_read_seq, unread, mention_unread}` | 404 | 天然幂等（只增） |
| C14 | GET `/principals/{principal_id}/channel-unreads` | principals | 本人 · RegisterPrincipalTenantMember | `?project_id=` | `[{channel_id, project_id, unread, mention_unread}]` + 合计（含 DIRECT，其 `project_id=null`；传 `project_id` 时不含 DIRECT） | 403 | — |
| C15 | GET `/channels/{channel_id}/mention-candidates` | channels | `channel_post` · O | `?q=&limit≤20` | `[{principal_id, kind, name, handle, avatar, state}]`（仅成员，不含退役） | — | — |
| C16 | GET/POST `/channels/{channel_id}/hooks`；PATCH `/channel-hooks/{hook_id}` | channels / channel-hooks | `channel_manage` · O（v1.1） | `{kind, target_url*, filter, enabled}` | `ChannelHook`（秘密仅创建时返回一次） | 422 145019 | ✔ / v |
| C17 | GET `/tenants/{tenant_id}/conversations` | tenants | RegisterTenantMember；服务内按 BR-05-119 行过滤（D118） | 过滤 `q`(name contains)；`include_agent_directs`(bool，默认 false)；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `{channels:[{project{id,name}, items: Channel[]}], directs: Channel[]（含 `peer{kind,id,name,avatar,state}`）, agent_directs?: Channel[], totals{unread, mention_unread}}`（每项含 `unread`、`mention_unread`） | — | — |
| C18 | POST `/tenants/{tenant_id}/direct-channels` | tenants | RegisterTenantMember · 调用者须为 HUMAN；对方为 Agent 时另需 `agent_read`（D118，BR-05-114） | `{peer_principal_id*}` | `Channel`（201；已存在 → 200） | 422 145020/145016；403 145403（HUMAN_ONLY / MISSING_PERMISSION） | ✔ |
| C19 | GET `/principals/{principal_id}/mentions` | principals | 本人 · RegisterPrincipalTenantMember（D118） | 过滤 `read`(eq) `reason`(in：MENTION / REPLY) `created_at`(lt)；`limit ≤50` | `[{message: Message, channel{id, kind, name, project_id?}, reason}]`（REPLY = 对本人消息的回复） | 403 | — |

**DIRECT 的守卫（D118）**：DIRECT 不属项目，C03 / C05 / C08 / C09 / C11 / C12 / C13 / C15 的 O 守卫反查到租户后按 BR-05-115 判定（参与者；人↔Agent 另含负责人与 `audit_read` 只读），不使用项目级 `channel_read` / `channel_post`；C04 / C06 / C07 / C16 对 DIRECT 一律 409 145011。

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
| `AgentDraftRequester`（00 §8，D111） | `Request(ctx, {tenant_id, agent_id, kind: PLAN\|REPORT, target_id, period_kind, period_start, deadline_at}) error`：Agent 为 ACTIVE·RUNNING 时下发 `agent.plan_draft` / `agent.report_draft`（幂等键 `kind:target_id`）；否则返回 `ErrDrafterUnavailable` | 10（planning J1 / J2） |
| `AgentDirectory`（00 §8） | `Get`；`ByPrincipal(principal_id)`；`IsAgent(principal_id)`；`Roles(agent_id)`；`Load(agent_id)`；`Stats(agent_id, type, window)`；`RuntimeProfile(agent_id) {system_prompt, runtime, model, effort, authorization_mode, max_context_tokens, mcp_allowlist}`；`ListByProject(project_id)`；视图含 `owner_principal_id`（负责人类）、`supervisor_agent_id`（= `parent_agent_id`）、`origin`（CR-10-10） | 02、03、04、07、08、collab |
| `DispatchProvenance`（00 §8） | `DispatchedBy(ctx, tx, work_item_id) ([]principal_id, error)` | 02（填入 08 `WorkItemFacts.DispatchedByActor`） |
| `ChannelProvisioner`（collab，新增，CCR-05-05） | `CreateDefaultOn(ctx, tx, {tenant_id, project_id, name, member_principal_ids}) (channel_id, error)`；`RenameDefaultOn(ctx, tx, project_id, name)`；`ArchiveProjectChannelsOn(ctx, tx, project_id)` | 07 |
| `ChannelPoster`（collab，新增） | `PostSystemOn(ctx, tx, {project_id, channel_id?, kind, payload, fold_key, attention, ref, source_event_id})`（缺省为默认频道） | fleet（DISPATCH_CARD、ensemble 对比卡、Agent 崩溃 / 不可用提示，在 fleet 事务内同步写入）；其他模块的频道呈现经领域事件由 `collab.event_projector` 投影，不直接调用 |
| 向 08 `PolicyRegistry` 贡献的规则 | `dispatch_auto`（主管 Agent 在 AUTO 策略下 `work_item_assign`；条件：BR-05-44 护栏未命中且 P02 守卫通过）；`ensemble_branch`（主管 Agent 对 ENSEMBLE 枝 `execution_start`；条件：BR-05-60–62）；`review_dispatch`（系统在 AUTONOMOUS 下的 REVIEWER 分派；条件：BR-05-53） | 08 |
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
| 142031 | 422 | fleet.spawn.grant_inactive | 无 ACTIVE 自建授权 / 不在有效窗口 |
| 142032 | 422 | fleet.spawn.quota_exceeded | 在册自建 Agent 达名额上限 |
| 142033 | 422 | fleet.spawn.daily_limit | 24 h 内自建次数达上限 |
| 142034 | 422 | fleet.spawn.not_allowed | 角色 / 机器 / 模型 / 强度 / 技能 / MCP 超出授权（`data.field`） |
| 142035 | 409 | fleet.spawn.request_not_pending | 请求不在 PROPOSED |
| 142036 | 422 | fleet.spawn.depth_exceeded | 自建 Agent 不能再自建 / 不能为 LEAD |
| 142037 | 422 | fleet.spawn.budget_exhausted | 授权剩余预算不足单 Agent 上限 |
| 142038 | 422 | fleet.spawn.full_auto_forbidden | 未收编的自建 Agent 不可启用 FULL_AUTO |
| 142039 | 409 | fleet.spawn.not_adoptable | 非自建 Agent 或已收编 |
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
| 145020 | 422 | collab.direct.member_peer_invalid | 成员私信对象无效：自己、非本租户成员、Agent 发起或 Agent↔Agent（应用 AGENT_DIRECT）、经 C02 创建 DIRECT（D118） |

权限拒绝不另设码：fleet 用 140403、collab 用 145403，原因放 `data.reason_code`（REAUTH_REQUIRED、NOT_MEMBER、DIRECT_READ_ONLY、MISSING_PERMISSION、SELF_MANAGE、HUMAN_ONLY、SPAWN_NOT_LEAD、SPAWNED_NOT_ADOPTED；均须在 08 §3.5 登记）。幂等冲突用平台码 176425 / 176409（09 C7）。

---

### 6.10 自建 Agent（D110）

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| G01 | POST `/tenants/{tenant_id}/agent-spawn-requests` | tenants | 调用者 = 主管 Agent 服务账号 · T；08 `Check(agent_spawn)` → `POLICY:spawn_grant` | `project_id?` `requested_spec*` `reason*` `source_command_id?` | `SpawnRequest`（201；AUTO 时 `state=CREATED` 并含 `created_agent_id`；HYBRID 降级时 `downgraded=true`） | 403 140403（`SPAWN_NOT_LEAD` / `NO_AUTHORITY`）；422 142031–142038；503 142030 | `Idempotency-Key` 必填（176425 / 176409）；`source_command_id` 唯一 |
| G02 | GET `/tenants/{tenant_id}/agent-spawn-requests` | tenants | `agent_read` · T | 过滤 `state`(in) `supervisor_agent_id`(eq) `grant_id`(eq) `project_id`(eq) `created_at`(gte,lte)；排序 `created_at`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `SpawnRequest[]` | — | — |
| G03 | GET `/agent-spawn-requests/{request_id}` | agent-spawn-requests | `agent_read` · O | — | `SpawnRequest`（含校验快照、授权用量）；ETag | 404 | — |
| G04 | PUT `/agent-spawn-requests/{request_id}/decision` | agent-spawn-requests | `agent_manage`（请求项目范围）· O；HUMAN | `{decision:"APPROVE" / "REJECT", note?}`（REJECT 时 note 必填 ≥10） | `SpawnRequest` | 409 142035；403 140403（`HUMAN_ONLY`）；422 142031–142037（批准时复验） | `If-Match` 必填（缺 428，冲突 409 142009） |
| G05 | POST `/agents/{agent_id}/adoption` | agents | `agent_manage` · O；HUMAN | `{machine_id?, note?}` | `Agent`（`lineage_status=ADOPTED`） | 409 142039；403 140403（`HUMAN_ONLY`） | `Idempotency-Key`；`If-Match` |
| G06 | GET `/agents/{agent_id}/lineage` | agents | `agent_read` · O | — | `{origin, created_by, supervisor, grant{id, name, usage}, spawn_request_id, reason, expires_at, adopted_by, adopted_at, orphaned_at, orphan_reason, children[]}` | 404 | — |

F01 / F15 增加过滤 `origin`(eq) `parent_agent_id`(eq) `lineage_status`(eq：HUMAN_CREATED / SPAWNED / ADOPTED / ORPHANED) `spawn_grant_id`(eq)，排序增加 `origin`；行增加 `origin`、`lineage_status`、`parent_agent{id, name}`、`expires_at`。

机器通道（§6.6 补充）：主管宿主可上行 `agent.spawn_request{command_id, project_id?, requested_spec, reason}`，服务端按 G01 处理（`source_command_id=command_id`）并回 `agent.command_ack{command_id, ok, request_id?, error_code?}`。

本模块使用的 08 接口：`SpawnGrantReader.Active(ctx, tx, tenantID, supervisorAgentID) (*SpawnGrantView, error)`；`SpawnGrantUsage.Reserve(ctx, tx, grantID, {request_id}) (reservationID, error)`、`SpawnGrantUsage.Release(ctx, tx, grantID, {reservation_id?, agent_id?}) error`。成本累计由 08 自行消费 `execution.cost_updated`。


### 6.x 接口增补（D112 / D113，00 v2.0 §15）

- 机器通道（§6.6 唯一定义）新增：S→M `agent.test{command_id, agent_id, work_item_id, subject_execution_id, qa_execution_id, deadline_at}`、`agent.accept{…同上, screenshots[]: file_node_id}`；M→S `agent.qa_ack{command_id, agent_id, accepted:bool, reason?}`。实际执行与结果回传走 03 的执行消息（`purpose=INTERNAL_*`）。
- 提供：`ModelFamilyOf(ctx, agent_id) (string, error)`（08 计划审批策略、03 记录使用）；`AgentDirectory` 视图增加 `model_family` 与 `roles` 中的新角色。
- 错误码：**142040** 主管不得创建产品验收 Agent；**142041** 纯人工项目不可把执行 / 内部验收分派给 Agent；**142042** 角色组合非法（ACCEPTOR+EXECUTOR / ASSISTANT+其他）；**142043** 无合格内部验收候选（仅用于接口查询与审计，派单路径走 IQ3 升级）。

### 6.z 接口增补（D120）

**收件箱、唤醒、重置与提醒**（fleet；记号同 §6 约定）

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| F17 | GET `/agents/{agent_id}/inbox-events` | agents | 调用者 = 该 Agent 服务账号；或负责人类 / `agent_workspace_read`（只读查看）· O | `?since=<seq>`（默认 = 当前 `inbox_ack_seq`）`&limit≤200&kind=`(in) | `{events: InboxEvent[], head_seq, ack_seq, truncated}`；`InboxEvent{seq, kind, project_id?, subject{kind,id}, stream{key, seq}?, ref{kind,id}?, actor{kind,id}?, title, payload, created_at}` | 403 140403（`NOT_OWNER`） | — |
| F18 | PUT `/agents/{agent_id}/inbox-cursor` | agents | 调用者 = 该 Agent 服务账号 · O | `{ack_seq*}` | `{ack_seq, head_seq, acked_at}` | 422 142044 | 天然幂等（只增） |
| F19 | SSE 主题 `tenant/{t}/agent_inbox/{agent_id}`（09 SSE） | — | 该 Agent 服务账号；负责人类 / `agent_workspace_read` | 订阅 | 帧 `{type:"wake", inbox_seq}`；每 15 s `{type:"heartbeat", inbox_seq}`（BR-05-123） | 订阅拒绝 | — |
| F20 | PUT `/agents/{agent_id}/wake-config` | agents | `agent_manage` · O；HUMAN | `{wake_mode*:"CHANNEL"\|"POLL"\|"WEBHOOK", webhook_url?, rotate_secret?:bool}`（WEBHOOK 为 v1.1） | `{wake_mode, webhook_url?, secret?（仅新设或轮换时返回一次）}` | 422 142049；409 140409 | v |
| F21 | POST `/agents/{agent_id}/resets` | agents | `agent_manage` · O；HUMAN；FULL 另需 step-up `agent_full_reset` | `{kind*:"SESSION"\|"FULL", reason*≥10, confirm_name?(FULL 必填), reauth_proof?(FULL 必填)}` | `Reset`（202，REQUESTED；未运行的 SESSION 直接 DONE） | 409 142002/142010/142012/142045；422 142050；403 140403（`REAUTH_REQUIRED` / `AGENT_PROHIBITED`） | ✔ |
| F22 | GET `/agents/{agent_id}/resets` | agents | 负责人类 / `agent_workspace_read` / `agent_manage` · O | 过滤 `kind`(in) `state`(in)；排序 `created_at`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `Reset[]` | — | — |
| F23 | GET · POST `/agents/{agent_id}/reminders` | agents | GET：该 Agent、负责人类、`agent_read`；POST：该 Agent 本人或 `agent_manage`（HUMAN）· O | POST `{anchor_kind*:"THREAD"\|"WORK_ITEM", channel_id?, thread_root_message_id?, work_item_id?, title*, note?, schedule_kind*:"ONCE"\|"INTERVAL"\|"PERIOD", fire_at?, interval_minutes?, period_kind?, max_fires?}`；GET 过滤 `state`(in) `anchor_kind`(eq) `loop_contract_id`(eq)；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `Reminder`（201）/ `Reminder[]` | 422 142046/142047；403 140403（`NOT_OWNER`） | ✔ |
| F24 | PATCH `/agent-reminders/{reminder_id}` | agent-reminders | 创建者本人、负责人类或 `agent_manage` · O | `{title?, note?, state?:"ACTIVE"\|"PAUSED"\|"CANCELLED", fire_at?, interval_minutes?, max_fires?}` | `Reminder` | 409 142048/140409；422 142047 | v |

F03 / F01 响应增加 `activity{status, changed_at, detail?}`、`inbox{head_seq, ack_seq, unacked}`、`session_epoch`；F01 增加过滤 `activity_status`(in)。`Reset{id, agent_id, kind, state, reason, requested_by, removed_bytes?, failure_code?, created_at, completed_at?}`；`Reminder` = §3.z 列（不含 `version` 之外的内部列）。

**操作清单与能力**

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| M01 | GET `/tenants/{tenant_id}/agent-operation-manifest` | tenants | 租户成员（人）或任一本租户 Agent 服务账号 · T | `?version=`（缺省 = 最新） | 清单文档 S-M1；`ETag: "<sha256>"`，`If-None-Match` 命中 → 304 | 404 140404（版本不存在） | — |
| M02 | GET `/tenants/{tenant_id}/agent-operation-manifest-versions` | tenants | 同 M01 · T | 排序 `published_at` | `[{manifest_version, sha256, operation_count, published_at, supersedes_version}]` | — | — |
| M03 | GET `/agents/{agent_id}/capabilities` | agents | 该 Agent 本人，或 `agent_read`（人）· O | `?project_id=`（缺省 = 团队级操作）`&manifest_version=` | `{manifest_version, project_id?, operations:[{name, status, reason_code?, via?, human_confirmation, quota_state?}]}`（BR-05-132；只读判定，不占配额） | 404 | — |

**S-M1 清单文档**：`{"manifest_version":"1.4.0","published_at":"…","conventions":{"envelope":"respx","next_field":"meta.next","seen_seq_field":"seen_seq","idempotency_header":"Idempotency-Key"},"operations":[{"name":"channel_message.post","summary":"…","transport":{"http":{"method":"POST","path":"/channels/{channel_id}/messages"}},"input_schema":{…},"output_schema":{…},"side_effects":["POSTS_MESSAGE"],"idempotency":{"required":true,"key_rule":"…"},"stale_context":{"stream":"channel|thread","seen_seq_required":true},"permission":{"code":"channel_post","via":"ROLE"},"delegable":true,"human_only":false,"human_confirmation":"NONE","quota":"RATE_LIMIT","phase":"MVP"}]}`（`additionalProperties:false`；每项 `name` 唯一，`^[a-z_]+\.[a-z_]+$`）。

**MVP 清单初版（节选；完整内容以各模块登记为准）**

| name | 接口 | 权限 · 来源 | 副作用 | `seen_seq` 流 | 人确认 | 配额 |
|---|---|---|---|---|---|---|
| `inbox_events.list` / `inbox_cursor.ack` | F17 / F18 | — · SELF | READ_ONLY / WRITES_STATE | — | NONE | — |
| `capabilities.list` / `agent_tools.list` | M03 / T12 | — · SELF | READ_ONLY | — | NONE | — |
| `channel_message.post` | 05 C09 | `channel_post` · ROLE | POSTS_MESSAGE | channel / thread | NONE | RATE_LIMIT |
| `dispatch_proposal.request` | 05 D02（`CHANNEL_REQUEST`） | `work_item_assign` · RELATION `project_lead_agent`（Agent 不持该码，只能以本项目主管身份生成待人确认的提案） | WRITES_STATE, NOTIFIES_HUMANS | — | ALWAYS | — |
| `agent_spawn_request.create` | 05 G01 | `agent_spawn` · POLICY `spawn_grant` | CREATES_PRINCIPAL, SPENDS_BUDGET | — | BY_POLICY | SPAWN_GRANT |
| `agent_reminder.create` | F23 | — · SELF | WRITES_STATE | — | NONE | — |
| `work_item_assignment.create`（评审认领） | 02 A1 | `work_item_read` + 委托 · DELEGATION | WRITES_STATE | work_item | NONE | — |
| `claim.submit` | 02 C1 | `work_item_execute` · ROLE | WRITES_STATE, NOTIFIES_HUMANS | work_item | NONE | — |
| `evidence.create` | 03 A27 | `evidence_submit` · ROLE | WRITES_STATE | work_item | NONE | — |
| `artifact_comment_thread.create` / `artifact_comment.reply` | 03 A77 / A79 | `artifact_comment_write` · ROLE | WRITES_STATE, NOTIFIES_HUMANS | work_item / comment_thread | NONE | — |
| `work_item_transition.apply` | 12 A13 | `work_item_write` · ROLE（再经流转条件） | WRITES_STATE | work_item | NONE | — |
| `report_content.update` / `report.submit` | 10 R4 / R7 | `report_submit` · ROLE（仅本人） | WRITES_STATE | — | BY_POLICY（确认仅人类） | — |
| `loop_run_result.record` | 10 L7 | — · SELF（契约的 Agent 本人） | WRITES_STATE | — | NONE | — |
| `memory.write` | 06 A31 | `knowledge_read` · ROLE（另按 06 BR-06-30 主体规则：只写自己的记忆、需执行出处） | WRITES_STATE | — | BY_POLICY（RULE 记忆待人审批） | — |
| `interaction.request`（工具授权 / 输入请求） | 03 机器事件 `interaction_requested`（运行中执行内） | `interaction_request` · ROLE（判定主体 = 执行的执行者，03 BR-03-148） | NOTIFIES_HUMANS | — | ALWAYS（回应需 `interaction_respond`，仅人类） | RATE_LIMIT（每派发 ≤100，03） |
| `defect_from_comment.create` | 12 A31 | `work_item_write` · ROLE | WRITES_STATE | — | NONE | — |
| `andon.pull` | 02 Andon | `andon_pull` · ROLE | WRITES_STATE, NOTIFIES_HUMANS | 豁免 | NONE | — |
| `interaction.respond` | 03 A21 | `interaction_respond`（仅人类） | — | — | — | —（`human_only=true`，列出以便解释拒绝） |

**机器通道（§6.6 唯一定义的增补）**

| 方向 | 类型 | 载荷 | 处理 |
|---|---|---|---|
| S→M | `agent.wake` | `{agent_id, inbox_seq}`（不带 `command_id`、不需 ACK、可合并；04 BR-04-102） | 宿主比较已确认 seq，必要时调 F17 读取；取代 `agent.inbox` |
| S→M | `agent.reset` | `{command_id, agent_id, kind:"SESSION"\|"FULL", session_epoch, workspace_epoch, scope:"AGENT_HOME"}` | SESSION：丢弃常驻会话；FULL：停止进程、清空 Agent 私有目录、丢弃会话；ACK 经 `agent.command_ack{command_id, ok, removed_bytes?, code?}`（RS3 / RS4） |
| M→S | `agent.status`（增补字段） | 原载荷 + `activity?:"ONLINE"\|"THINKING"\|"WORKING"\|"ERROR"`、`activity_detail?{execution_id?, error_code?≤64}`、`session_epoch?` | BR-05-134；`session_epoch` 小于服务端值 → 服务端回 `agent.reset{SESSION}` 补发 |

poll 请求 `agents[]` 快照（04 `AgentProcState`）同样增加 `activity`、`session_epoch`。`ServerBatch` 增加 `agent_inbox_heads[]`（04 BR-04-102）。

**collab 增补**：C09 请求增加 `seen_seq?`（AGENT 必填，BR-05-127）；409 **145021**（`interrupted`）、422 **145022**。C08 响应 `meta.max_seq` 即会话流当前序号；`Message` 增加 `stream_seq`（= `seq`）。

**跨模块接口增补**（本模块提供）

| 接口 | 方法 | 使用者 |
|---|---|---|
| `OperationRegistry`（00 §8） | `Register(op OperationSpec) error`（启动期调用；重复名称或缺必填字段返回错误并阻止启动）；`Lookup(name)` | 全部登记 Agent 可调用操作的模块（02、03、05、06、07、10、12） |
| `AgentReminderScheduler`（00 §8） | `UpsertForLoopOn(ctx, tx, LoopReminderSpec) (reminder_id, error)`；`PauseForLoopOn` / `ResumeForLoopOn` / `CancelForLoopOn(ctx, tx, loop_contract_id)` | 10 |

**本模块使用**：02 `WorkItemActivityReader.Since`（收件箱投影 `stream_seq`）；03 `comment.*` 事件；10 `LoopRunRecorder.OpenOn`；08 `AuthorityChecker.Check`（M03 只读判定）与 §3.5c 映射；09 `SecretSealer`（webhook 秘密，v1.1）。

**团队工具目录**（fleet；记号同 §6 约定）

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| T01 | GET `/tenants/{tenant_id}/tool-entries` | tenants | `agent_read` · T | 过滤 `kind`(eq) `state`(in) `q`(contains)；排序 `entry_key` `updated_at`；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `ToolEntry[]` | — | — |
| T02 | POST `/tenants/{tenant_id}/tool-entries` | tenants | `tool_catalog_manage` · T；HUMAN | `{kind*:"MCP_SERVER", entry_key*, title*, description?, transport*, credential_mode?, preset_key?, from_local?{machine_id, runtime_kind, name}}` | `ToolEntry`（201，DRAFT） | 409 142051；422 142052；403 140403（`AGENT_PROHIBITED` / `MISSING_PERMISSION`） | ✔ |
| T03 | GET `/tool-entries/{entry_id}` | tool-entries | `agent_read` · O | — | `ToolEntry` + `versions[≤20]` + `grants[]` + `credential{set, last4?, rotated_at?, upstream_auth_failing}`；ETag | 404 140404 | — |
| T04 | PATCH `/tool-entries/{entry_id}` | tool-entries | `tool_catalog_manage` · O；HUMAN | `{title?, description?, credential_mode?}` | `ToolEntry` | 422 142052；409 140409 | v |
| T05 | POST `/tool-entries/{entry_id}/versions` | tool-entries | `tool_catalog_manage` · O；HUMAN | `{config*（S-T1）, change_note?}`（仅 MCP_SERVER） | `ToolEntryVersion`（201，成为当前版本） | 422 142052；409 142053/142058 | ✔ |
| T06 | GET `/tool-entries/{entry_id}/versions` | tool-entries | `agent_read` · O | 排序 `version_no` | MCP：`ToolEntryVersion[]`；SKILL：06 已批准版本 `[{version_no, content_hash, approved_at}]`（经 06 `SkillCatalog` 读取） | — | — |
| T07 | PUT `/tool-entries/{entry_id}/state` | tool-entries | `tool_catalog_manage` · O；HUMAN | `{state*:"ACTIVE"\|"DISABLED"\|"ARCHIVED", reason?≥10（DISABLED / ARCHIVED 必填）}`；或版本撤回 `{version_no*, version_state*:"REVOKED", reason*}` | `ToolEntry` | 409 142053/140409；422 142055 | v |
| T08 | PUT `/tool-entries/{entry_id}/credential` | tool-entries | `tool_catalog_manage` · O；HUMAN；step-up `tool_credential_manage`（target `tool_entry:<id>`） | `{secret*≤8KB, header_name?, reauth_proof*}` | `{set:true, last4, rotated_at}`（明文永不回显） | 422 142055；403 140403（`REAUTH_REQUIRED`） | — |
| T09 | GET · POST `/tool-entries/{entry_id}/grants` | tool-entries | GET：`agent_read`；POST：`tool_catalog_manage`（HUMAN）· O | POST `{subject_kind*:"PROJECT"\|"ROLE"\|"AGENT", project_id?, role?, agent_id?}`；GET 过滤 `subject_kind`(eq) `state`(in)；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `ToolGrant`（201；同主体已有 ACTIVE → 200）/ `ToolGrant[]` | 422 142054 | 天然幂等 |
| T10 | DELETE `/tool-grants/{grant_id}` | tool-grants | `tool_catalog_manage` · O；HUMAN | `?reason=`（≥10 字） | 204（REVOKED） | 404 | 天然幂等 |
| T11 | GET `/tenants/{tenant_id}/tool-presets` | tenants | `agent_read` · T | 过滤 `transport`(eq)；`cursor?` / `limit?`（默认50、最大100）/ `sort?`，返回集合 `items,next_cursor?,has_more`（00 §16.10） | `ToolPreset[]` | — | — |
| T12 | GET `/agents/{agent_id}/tools` | agents | 该 Agent 本人，或 `agent_read`（人）· O | `?project_id=`（缺省 = 团队级） | `{toolset_preview{revision}, entries:[{source, kind, entry_key, title, version_no?, content_sha256?, status:"AVAILABLE"\|"NOT_GRANTED"\|"NOT_SELECTED"\|"NOT_MOUNTED"\|"DISABLED"\|"CREDENTIAL_MISSING"\|"LOCAL_MISMATCH"\|"LOCAL_ONLY", covered_by[{grant_id, subject_kind}], local?{machine_id, scope?, path?}}]}`（与 BR-05-144 同一解析，只读） | 404 | — |
| T13 | POST · GET · DELETE `/mcp-relay/{entry_key}` | mcp-relay | 工具令牌（`Authorization: Bearer`，BR-05-146）；不经 `authz.Register*` 的成员守卫 | MCP Streamable HTTP（JSON-RPC 透传，`Mcp-Session-Id` 原样转发） | 上游响应（流式） | 401（142056）；403 JSON-RPC `-32001`；429；502（142057） | — |

`ToolEntry{id, kind, entry_key, title, description?, skill_id?, transport?, credential_mode, current_version_no?, current_content_sha256?, preset_key?, state, grant_summary{projects, roles, agents}, credential{set, last4?}, version}`；`ToolEntryVersion{entry_id, version_no, config, content_sha256, change_note?, state, published_by, published_at}`；`ToolGrant{id, entry_id, subject_kind, project_id?, role?, agent_id?, state, granted_by_kind, granted_by?, granted_at, revoked_at?}`。

**S-T1 MCP 版本配置**（`additionalProperties:false`，不含机密）：STREAMABLE_HTTP `{"url":"https://…","timeout_ms":≤120000,"headers":{名称: 非机密值}?}`；STDIO_PACKAGE `{"package":{"manager":"NPM"|"PYPI"|"FILE","name","version","integrity","file_id"?},"command","args":[]≤32,"env":{名称: 非机密值}?}`。凭据只经 T08 设置，由中继注入，不得出现在配置中（检测到形如令牌的值 → 422 142052 `data.field=config`）。

**跨模块接口增补（团队工具目录）**

| 接口 | 方法 | 使用者 |
|---|---|---|
| `ToolsetResolver`（00 §8） | `ResolveOn(ctx, tx, ToolsetRequest{tenant_id, execution_id, dispatch_epoch, project_id, agent_id?, initiator_principal_id?, machine_id, runtime_kind, selected_entry_keys?}) (Toolset, error)`（BR-05-144；只读，在 03 派发事务内调用）；`IssueTokens(ctx, execution_id, dispatch_epoch) ([]ToolToken{entry_key, token, expires_at}, error)`（BR-05-146；执行非当前派发 → `ErrFenced`） | 03 |
| `AgentInboxHeads`（00 §8） | `AgentInboxHeads(ctx, machine_id) ([]{agent_id, inbox_seq}, error)`（本机绑定的非 RETIRED Agent 的收件箱头，04 BR-04-102 每批携带） | 04 |

本模块使用（工具目录）：06 `SkillCatalog`（技能版本与挂载）与 `skill_version.approved` / 技能归档事件；04 `MachineDirectory` 能力检测结果（S3，含 D120 增补的技能哈希，04 BR-04-106）；03 `ExecutionReader.Get`（中继校验执行状态与派发代次）；09 `SecretSealer`（purpose `fleet.tool_credential`）；08 step-up `tool_credential_manage`。
**错误码增补**（fleet 子段 142000–143999、collab 145000–149999）

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 142044 | 422 | fleet.inbox.cursor_ahead | `ack_seq` 大于收件箱最新序号 |
| 142045 | 409 | fleet.reset.in_progress | 已有进行中的重置 |
| 142046 | 422 | fleet.reminder.limit_reached | 提醒数量达上限 |
| 142047 | 422 | fleet.reminder.invalid | 锚点不可读、时间或间隔越界 |
| 142048 | 409 | fleet.reminder.managed_by_loop | 循环契约的提醒只能经 10 修改 |
| 142049 | 422 | fleet.wake.webhook_invalid | webhook 地址非 https / 内网地址（v1.1） |
| 142050 | 422 | fleet.reset.name_mismatch | 完全重置确认名称不符 |
| 145021 | 409 | collab.message.write_interrupted | 会话 / 线程已有更新消息，未写入（`data.state="interrupted"`） |
| 145022 | 422 | collab.message.seen_seq_required | Agent 发消息未携带 `seen_seq` |
| 142051 | 409 | fleet.tool.entry_key_taken | 目录中已有同名条目 |
| 142052 | 422 | fleet.tool.entry_invalid | 条目或版本配置非法（`data.field`：传输、地址、完整性哈希、STDIO 带凭据、配置中含疑似机密、经 T02 建技能、上限） |
| 142053 | 409 | fleet.tool.entry_state | 条目状态不允许该操作（§4.z TE-状态） |
| 142054 | 422 | fleet.tool.grant_invalid | 授予主体不存在、角色非法、条目已归档或授予数达上限 |
| 142055 | 422 | fleet.tool.credential_invalid | 激活时缺凭据、凭据格式不符或 `credential_mode=NONE` 时提交凭据 |
| 142056 | 401 | fleet.tool.token_invalid | 工具令牌无效或过期（中继） |
| 142057 | 502 | fleet.tool.upstream_failed | 上游 MCP 服务器不可达、超时或返回非协议响应（中继） |
| 142058 | 409 | fleet.tool.version_unchanged | 新版本配置与当前版本相同 |

## 7. 后台任务与事件

### 7.1 任务（经 09 `JobQueue`；周期互斥用 `internal/infra/dlock`，锁名 `fleet:<job>` / `collab:<job>`，`maxHold` = 周期 ×2）

| 任务 | 触发 / 频率 | 互斥 | 处理 | 重试 / 失败 |
|---|---|---|---|---|
| `fleet.agent_crash_watchdog` | 每 15 s | dlock 全局 | 扫 `state='ACTIVE' AND process_status IN ('RUNNING','STARTING')`：机器 ONLINE 且 `last_seen_at < now−30s`（3 × 04 心跳 10 s）→ A07；STARTING 超 60 s → A06；每批 500，逐条 CAS（`version`） | 单条失败下轮重试；连续 5 轮失败告警 `fleet_watchdog_failures_total` |
| `fleet.agent_auto_restart` | A06/A07 后延迟任务（10 s / 30 s / 90 s） | 任务键 `agent:<id>:crash:<process_event_id>` 去重 | BR-05-12 判定 → A10 / A11 | 机器离线 → 推迟到 `machine.state_changed{ONLINE}` 再判（不消耗次数） |
| `fleet.agent_credential_delivery` | 创建 / 换绑 / F08 后 | 任务键 `agent:<id>:cred:<credential_id>` | BR-05-03 ⑦⑧ | 10 s/30 s/90 s/5 min/15 min，5 次后 FAILED + N-05-02 |
| `fleet.agent_pending_cleanup` | 消费 `machine.state_changed{ONLINE}` | 每机一次 | 对 `pending_cleanup_machine_id = 该机` 的 Agent 下发 `agent.cleanup` | 30 天未完成 → 清空字段，审计 `fleet.agent_cleanup_abandoned` |
| `fleet.retirement_runner` | R03 `advance` 后及每 30 s | 每退役 dlock `fleet:retirement:<id>` | 推进 R4→R7；R2 10 min、R5 5 min 超时处理 | R6 失败整体回滚，60 s 后重试，3 次失败 → state=FAILED，N-05-06 |
| `fleet.dispatch_cycle` | 消费 `work_item.created{state:READY}`、`work_item.state_changed{to:READY}`、`work_item.assigned`（释放）、`project.stage_changed{to:ACTIVE}`、`agent.state_changed`、`machine.state_changed`（去抖 30 s）；兜底每 5 min；只处理阶段 ACTIVE 的项目（BR-05-55） | dlock `fleet:dispatch:<project_id>`（maxHold 60 s） | BR-05-48 排序取 ≤10 项 → 生成提案 → AUTO 护栏 → 应用 | 主管 Agent 未回应走 BR-05-42 回退；单项失败不影响其他 |
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
| `fleet.spawn_request_expiry` | 每 60 s | dlock `fleet:spawn-expiry` | PROPOSED 且 `expires_at<now`，或所属授权已失效 → S06 | — |
| `fleet.spawn_lineage_watch` | 每 60 s；并立即消费 `agent.retired`、`agent.state_changed{CRASHED}`、`spawn_grant.revoked` / `spawn_grant.expired`、`takeover.executed` | dlock `fleet:spawn-lineage` | 按 L02 标记孤儿（CRASHED 需 exhausted 且持续 >24 h） | — |
| `fleet.spawn_lifecycle` | 每 10 min | dlock `fleet:spawn-lifecycle` | BR-05-104 到期 / 闲置退役；到期前 24 h N-05-15；L05 孤儿 7 天自动退役 | 退役失败按 §4.2 重试 |

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
| `ensemble.compared`（00） | H05 | `{plan_id, work_item_id, system_pick_branch_id, lead_pick_branch_id, requires_human}`（`requires_human=true` 时 08 建 ENSEMBLE_SELECTION 待办；AUTO 且系统与主管一致时为 false） |
| `ensemble.selected`（CCR-05-08） | H07 / H08 / H09 | `{plan_id, work_item_id, selected_branch_id, selected_by, synthesized:bool}` |
| `channel.created` / `channel.archived`（CCR-05-08） | C02 / 归档 | `{channel_id, project_id, kind}`（DIRECT 经 C18 创建，`project_id=null`，D118） |
| `message.posted`（CCR-05-08） | C09 与系统消息 | `{channel_id, channel_kind, message_id, seq, kind, author{kind,id}, mention_principal_ids[], trigger_depth}`（`channel_kind` 为 D118 新增，供消费方识别并排除私信）（**不含正文**） |

| `agent.spawn_requested` | S01 | `{request_id, supervisor_agent_id, grant_id, project_id?, approval_mode, role, model_id, reason_excerpt≤140, estimated_cost_cap_micros}` |
| `agent.spawn_rejected` / `agent.spawn_failed` | S03 / S05 | `{request_id, supervisor_agent_id, grant_id, failure_code?}` |
| `agent.spawned` | S04 | `{agent_id, request_id, supervisor_agent_id, grant_id, project_id?, role, expires_at}` |
| `agent.orphaned` | L02 | `{agent_id, supervisor_agent_id, grant_id, orphan_reason}` |
| `agent.adopted` | L03 / L04 | `{agent_id, adopted_by, grant_id}` |

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
| `spawn_grant.revoked` / `spawn_grant.expired` | 08 | L02 孤儿判定；该授权的 PROPOSED 请求 → S06 |
| `spawn_grant.exhausted` / `spawn_grant.agent_cap_reached{agent_id}` | 08 | BR-05-102：E12 排除、相关派单提案 EXPIRED；EVENT（attention） |
| `takeover.executed` | 08 | 08 已撤销该项目自建授权；本模块按 L02（`orphan_reason=TAKEOVER`） |
| `memory.approved` 等 | 06 | 不消费 |

### 7.3 审计事件（`audit.Appender`，与业务写同事务；`detail` 含 `reason`、`evidence_refs[]`、`authority_source`）

自建（D110）：`fleet.agent_spawn_requested`、`fleet.agent_spawn_decided`（APPROVE / REJECT，含 `decided_by`）、`fleet.agent_spawned`（含 `grant_id`、`supervisor_agent_id`、`request_id`，`authority_source=POLICY:spawn_grant`）、`fleet.agent_spawn_failed`、`fleet.agent_orphaned`、`fleet.agent_adopted`。

`fleet.agent_created`、`fleet.agent_updated`（字段名 diff，不含提示词正文，仅 hash）、`fleet.agent_full_auto_enabled`、`fleet.agent_started` / `stopped` / `restarted`、`fleet.agent_crashed`、`fleet.agent_restart_exhausted`、`fleet.agent_rebound`、`fleet.agent_credential_issued` / `revoked`、`fleet.agent_retirement_started` / `cancelled`、`fleet.agent_retired`（`mode`）、`fleet.agent_cleanup_abandoned`、`fleet.dispatch_auto_applied`、`fleet.dispatch_confirmed`、`fleet.dispatch_overridden`、`fleet.dispatch_dismissed`、`fleet.ensemble_started`、`fleet.ensemble_budget_stopped`、`fleet.ensemble_selected`、`fleet.ensemble_cancelled`、`collab.channel_created` / `archived`、`collab.member_added` / `removed`、`collab.message_edited` / `retracted`（无正文）、`collab.hook_changed`。权限拒绝（403）写 `outcome=denied` 的同名事件。

### 7.4 实时（09 SSE）

主题（00 §9 语法：单项 = 单数 snake_case kind，集合 = 复数，项目扇出 = `tenant/{t}/project/{p}`）：`tenant/{t}/agent/{id}`（状态、进程、负载、统计更新时间）、`tenant/{t}/agents`（列表行）、`tenant/{t}/project/{p}`（`dispatch_proposal` 增改子流）、`tenant/{t}/ensemble_plan/{id}`、`tenant/{t}/channel/{id}`（`message.created` / `message.updated` / `message.retracted` / `member.changed`，载荷含完整 `Message` 表示，订阅时按 C03 授权过滤）、`tenant/{t}/inbox/{principal_id}`（未读与提及计数）。客户端断线重连以 `after_seq` 调 C08 补齐；目标 ≤1 s 呈现。本模块向 09 注册 `TopicAuthorizer`：`agent` / `agents` 需 `agent_read`；`project` 的 `dispatch_proposal` 子流与 `ensemble_plan` 需项目 `work_item_read`；`channel` 按 C03 授权（DIRECT 按 BR-05-115，D118）。

---


### 7.x 任务与事件增补（D112 / D113，00 v2.0 §15）

任务：`fleet.internal_qa_dispatch`（消费 `internal_qa.passed{stage ∈ AUTOMATED, AGENT_TEST}`；任务键 `iq:{subject_execution_id}:{role}`，幂等；30 min 未开始的延迟复查 → IQ4）。
发布：`dispatch.proposed` / `dispatch.applied`（`trigger=INTERNAL_QA`，`requires_human=false`，08 不为其建收件箱项）。
消费：03 `internal_qa.passed`、`quality.escaped`（02 / 07 / 12 Part A）；07 `project.mode_changed`（切到 HUMAN_ONLY 时：撤销该项目非 ASSISTANT Agent 的项目绑定与未开始分派，经 02 `DispatchAssigner.Release`；进行中的执行按 02 规则处理）。
登记 08 `SystemActions`：`fleet.internal_qa_dispatch` → `work_item_assign`。

### 7.z 任务与事件增补（D120）

| 任务 | 触发 / 频率 | 互斥 | 处理 | 重试 / 失败 |
|---|---|---|---|---|
| `fleet.inbox_projector` | 消费 BR-05-120 列出的领域事件（AtLeastOnce，09 `domain_event_receipts`） | 按 `(agent_id, source_event_id)` 唯一键幂等 | 解析受影响的 Agent → 复验读权限 → 写收件箱 → 登记唤醒 | 失败重试 8 次，之后 `fleet_inbox_projection_failures_total` 告警 |
| `fleet.agent_wake` | 收件箱写入提交后（同 Agent 1 s 去抖） | 任务键 `wake:<agent_id>` | 按 `wake_mode` 发 `agent.wake` / SSE 帧 / webhook（v1.1）；更新 `last_wake_*` | 提示可丢失，不重试（CHANNEL 由心跳兜底）；WEBHOOK 按 BR-05-124 |
| `fleet.inbox_retention` | 每小时 | dlock `fleet:inbox-retention` | 删除 `expires_at < now` 的事件（每批 5000）；BR-05-125 积压检查与 N-05-17 | 下轮继续 |
| `fleet.reminder_fire` | 每 30 s | dlock `fleet:reminder-fire` | BR-05-139 / 140 | 单条失败下轮重试；10 `LoopRunRecorder` 不可用 → 推迟 60 s，不丢触发 |
| `fleet.reset_timeout` | 每个 `agent.reset` 发出后延迟 5 min | 任务键 `reset:<id>` | 未 ACK → RS5 | — |
| `fleet.manifest_publish` | 服务启动；每次部署 | dlock `fleet:manifest`（全局） | BR-05-130 计算并发布新版本 | 失败阻止启动（只读模式不阻止，读最新已发布版本） |
| `fleet.activity_error_watch` | 每 60 s | dlock 全局 | ERROR 持续 ≥10 min → N-05-16 | — |
| `fleet.tool_catalog_sync` | 消费 06 `skill_version.approved` 与技能归档 | 按 `(tenant_id, skill_id)` 幂等 | 建 / 归档 SKILL 条目（BR-05-141）；`tool_catalog.changed{VERSION_PUBLISHED}` | 失败重试 8 次 |
| `fleet.tool_catalog_migration` | 上线时一次 | dlock `fleet:tool-catalog-migration` | BR-05-150 | 可重入（按挂载行幂等） |
| `fleet.tool_upstream_watch` | 中继每次上游 401 / 403 后去抖 60 s | 任务键 `tool-upstream:<entry_id>` | 连续 ≥5 次 → N-05-18 | — |

**发布**：收件箱、活动状态、唤醒均为本模块内部，重置与提醒只审计，不发领域事件；团队工具目录发布 `tool_catalog.changed{entry_id, entry_key, kind, change:"VERSION_PUBLISHED"|"STATE_CHANGED"|"GRANTS_CHANGED"|"CREDENTIAL_ROTATED", version_no?, state?}`（不含配置与凭据；消费者：本模块 agents（受影响 Agent 的 `config_revision+1`，常驻宿主重取 F16）、governance（通知与审计索引）；可经 SSE 推送完整载荷）。
**消费**（新增）：03 `comment.thread_created` / `comment.posted` / `comment.converted`、`internal_qa.failed`、`interaction.decided` / `interaction.closed`；02 `decision.made`（反馈投影）；10 `loop_contract.state_changed`（与提醒状态核对：若接口调用因故未同步，按契约状态修正提醒）。
**审计**（`audit.Appender`，与业务写同事务）：`fleet.agent_session_reset`、`fleet.agent_full_reset`（含 `reauth_purpose=agent_full_reset`、`removed_bytes`）、`fleet.agent_reset_failed`、`fleet.agent_workspace_read`（路径 SHA-256）、`fleet.agent_reminder_changed`（创建 / 暂停 / 取消，含 `created_by_kind`）、`fleet.agent_wake_config_changed`（不含秘密）、`fleet.operation_manifest_published`（版本、sha256、操作数）、`fleet.tool_entry_changed`、`fleet.tool_version_published`（含 `content_sha256`）、`fleet.tool_grant_changed`（主体、授予 / 撤销、理由）、`fleet.tool_credential_rotated`（只含 `last4` 与 `reauth_purpose=tool_credential_manage`）、`fleet.tool_called` / `fleet.tool_call_denied`（BR-05-147，不含入参与结果内容）。权限拒绝写 `outcome=denied` 的同名事件。
**实时**：`tenant/{t}/agent/{id}` 增加 `activity` 子流；新主题 `tenant/{t}/agent_inbox/{agent_id}`（F19；`TopicAuthorizer`：该 Agent 服务账号、负责人类或 `agent_workspace_read`）。

## 8. 页面规格

所有页面在 `apps/delivery`；路由前缀 `/t/:tenantID`；通用五态（骨架、空、错误可重试、无权限说明缺失权限码、离线 / 重连横幅）；Agent 头像统一 `ActorAvatar`（圆角方形像素人 + 芯片角标）；状态用 `StateBadge`（图标 + 文字 + 语义色：ACTIVE 绿「运行中」、STOPPED 灰「已停止」、CRASHED 红「已崩溃」、RETIRING 琥珀「退役中」、RETIRED 虚线灰「已退役」、DRAFT 中性「草稿」、UNREACHABLE 虚线「机器离线」）。

### 8.1 成员与 Agent（`/t/:tenantID/members`）

- **目的**：一张表看人与 Agent 的角色、负载、当前工作、状态。
- **布局**：标题栏〔新建 Agent〕（`agent_manage`）；筛选条：类型（全部 / 人 / Agent）、角色、状态、项目、机器、搜索；表格（`@workspace/ui` auto-table，F15）。
- **列**：头像 + 名称（Agent 显示 handle）、类型标签、角色（人：项目角色汇总；Agent：AgentRole 芯片）、runtime × 模型（仅 Agent）、负载（`active/max` 进度条，满载琥珀）、当前工作项（≤3 链接）、首次验收率 30 天（样本 <5 显示「样本不足」）、状态、最近活跃。行操作（Agent）：启动 / 停止 / 重启（按 §4.1 可用性）、详情、退役（`agent_retire`）。
- **来源列（D110）**：徽章「人工创建」（中性）、「主管创建 · {主管名}」（紫色，头像带 ✦ 角标）、「已收编」（绿）、「孤儿」（琥珀虚线，附〔收编〕〔退役〕快捷按钮）；未收编的自建 Agent 另显示到期倒计时（<24 h 琥珀）。
- **来源筛选（D110）**：chip 组「全部 / 人类 / 人工创建的 Agent / AI 创建的 Agent / 孤儿」（映射 `kind` 与 `lineage_status`），可与「主管」「授权」下拉组合；开关〔按主管分组〕以树展示：主管 → 其自建 Agent（已退役折叠），主管节点显示授权用量「2/3 名额 · $18.40 / $50」。
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
- **Tab 概览**：绩效四卡（首次验收率、单位已接受成本、返工率、当前负载，含窗口切换 30 / 90 天与 `computed_at`）、按工作项类型的小表、最近进程事件、所在项目（主管的项目带 LEAD 标）。
- **谱系卡（D110，概览顶部）**：来源徽章；创建者（人或主管，可点击）、主管、所属自建授权（名称与名额 / 预算计量条）、创建理由（主管原文）、到期时间（`CountdownRing`）、收编人与时间、孤儿原因。主管 Agent 的详情另有「它创建的 Agent」列表与〔调整授权〕（跳 08 授权与策略）。孤儿态顶部琥珀横幅〔收编〕〔退役〕。
- **Tab 工作项**：复用 02 列表（`assignee_principal_id=eq:<principal>`），进行中 / 历史切换；每行 `CompletionLadder`。
- **Tab 授权**：AgentRole 与项目角色绑定（只读，链接到 07 项目团队）、08 委托（剩余有效期 `CountdownRing`、使用次数）；FULL_AUTO 红色标签。
- **Tab 技能**：06 挂载列表（技能、版本、是否跟随最新、归档提示）；〔挂载技能〕调用 06 `PUT /agents/{id}/skills/{skill_id}`。
- **Tab 记忆**：06 提供的记忆组件（条目、写入审计、撤销）；本页只放容器与链接「在知识库中打开」。
- **Tab 私信**：左侧 AGENT_DIRECT 列表（对方头像、项目、最近消息时间），右侧只读消息流（与频道同组件，无输入框，顶部提示「Agent 之间的私信，仅可查看」）。另列该 Agent 与人类之间的 DIRECT（D118，BR-05-115）：仅对该 Agent 的负责人类与 `audit_read` 持有者显示，同为只读，顶部提示「人与 Agent 的私信，仅供负责人与审计查看」。
- **Tab 工作目录**：面包屑 + 文件列表（F12）+ 只读预览（代码高亮，≤1 MiB）；机器离线 → 离线态「机器离线，无法浏览」。
- **实时**：头部状态、负载、统计更新时间经 SSE；离线时顶部横幅并冻结按钮。
- **移动端**：Tab 变为横向滚动；头部按钮收入「更多」菜单，〔退役〕在菜单底部红色。

### 8.4 退役向导（`/t/:tenantID/agents/:agentID/retirement`）

- **入口**：详情〔退役〕→ 模式选择对话框：「正常退役（推荐，机器在线）」/「强制退役（机器异常时）」；机器非 ONLINE 时正常退役禁用并说明 142024。
- **正常退役步骤条**：① 检查（在途工作项 n、运行中执行 n、主管的项目列表「退役后这些项目的派单策略会切回人工确认」）→ ② 交接文档（进度、10 min 倒计时 `CountdownRing`；完成后内嵌预览 + 下载；超时显示系统摘要与〔让 Agent 重试一次〕）→ ③ 接手人（单选：人 / Agent / 稍后再选；成员选择器分组显示负载与首次验收率；不合格项逐条标红并说明）→ ④ 确认并执行（汇总将转交 / 释放的工作项、将清理的机器目录、将吊销的凭证；〔开始退役〕）→ 执行中逐步打勾（转交、清理、吊销、完成），失败项列表可〔重试〕。①–③ 可〔取消退役〕。
- **强制退役**：红色对话框：后果清单（「正在进行的执行会被取消」「未保存的工作状态会丢失」「操作无法撤销」「交接文档只能由系统根据记录生成」）、原因（≥10）、输入 Agent 名称（逐字一致才可提交）、step-up 重新认证；提交后显示结果页（丢失清单、待清理机器）。
- **移动端**：步骤条变为顶部「第 n / 4 步」；成员选择器全屏 Sheet。

### 8.5 项目「团队编排」卡片（项目总览右栏，`/t/:tenantID/p/:projectID`）

- **内容**：主管 Agent（头像、状态、runtime × 模型；空则「未设置 — 所有派单由系统评分并需人确认」）、派单策略（MANUAL「提案需人确认」 / AUTO「策略内自动生效」+ 护栏说明链接）、并行上限（1–5，默认 3）、近 24 h：提案数、自动生效数、被改派数、无合格候选数。
- **编辑**：〔编辑〕（`project_manage`）打开表单，提交调用 07 #4（If-Match = 项目版本）；主管 Agent 选择器只列 ACTIVE、含 LEAD、在本项目有 `agent_lead` 绑定的 Agent（07 校验 127042）；切 AUTO 且无主管 Agent 时提示「AUTO 需要主管 Agent，否则按人工确认处理」；项目只读（CLOSED / CANCELLED）时隐藏编辑。
- **链接**：〔查看派单提案〕→ 8.6。

### 8.6 派单提案面板（`/t/:tenantID/p/:projectID/dispatch`）

- **布局**：左列提案列表（筛选：待处理 / 已生效 / 已改派 / 已过期 / 无合格候选；待处理按 `expires_at` 升序，`CountdownRing` 显示剩余时间）；右侧提案详情。
- **详情**：工作项摘要（标题、类型、风险、必需技能）；推荐人卡（头像、总分、四舍五入两位）与理由（来源标签「主管 Agent」/「系统评分」）；护栏提示（如「风险 HIGH，需要人工确认」）；候选表：排名、Agent、总分、五个分量条（技能 / 首次验收 / 成本 / 负载 / 轮换，悬浮显示原始输入）、不合格候选折叠区（不合格原因中文化，如「机器离线」「技能不符：缺少 frontend」）；〔复算说明〕展示公式与 `inputs_digest`。
- **按钮**：〔确认派给 X〕（PROPOSED、有 `work_item_assign`）、〔改派〕（打开成员选择器，人与 Agent 分组，必须填理由）、〔驳回〕（理由必填）、〔重新生成〕（非 PROPOSED 时，D02）。并发冲突 409 → 刷新并提示「已被 Morgan 处理」。
- **无合格候选**：显示原因直方图与「最接近的 3 名」及各自差什么，按钮〔指派给…〕〔驳回〕。
- **移动端**：列表全屏，点击进入详情；候选分量以紧凑横条显示。

### 8.7 Heavy 对比报告视图（`/t/:tenantID/p/:projectID/work-items/:workItemID/ensembles/:planID`，v1.1）

- **头部**：计划状态、触发原因、预算环（已用 / 上限，80% 琥珀、100% 红）、截止倒计时；〔批准启动〕（PENDING_APPROVAL）、〔取消〕。
- **对比表**：每枝一列（≤5）：Agent + 模型、终态、证据覆盖、必需验证、测试、diff 规模、成本、时长、AI 辅助意见（灰色「仅参考」标签）、系统排名；每行最优值加粗；不宣称「胜者」，只在列头标「系统排序第 1」与「主管 Agent 推荐」。
- **主管 Agent 理由**：独立区块，来源标签。
- **选择**：〔选这一枝送审〕（每列底部）、〔合成…〕（选择主枝 + 被合成枝，显示剩余预算）；提交需理由；选择后提示「已提交声明，仍需正常验证与裁决」。
- **落选枝**：灰显 + 「已归档（保留证据）」，可展开查看执行详情。
- **移动端**：横向滑动的枝卡片，比较项纵向排列。

### 8.8 会话（D118；`/t/:tenantID/chat`）

- **路由（唯一方案）**：频道 `/t/:tenantID/chat/:channelID`（DIRECT / AGENT_DIRECT 同路由），默认 `/t/:tenantID/chat`；执行会话 `/t/:tenantID/chat?execution_id=:executionID`；线程 `?thread=:messageID`、详情 `?details=1`。旧 `/channels`、项目频道路径、独立 session UI 路径不注册，不留 alias / 重定向。所有通知和对象入口直接引用规范路由。项目归属由频道 / 执行对象提供，进入前校验权限。会话域全宽、无协作侧栏跨域项。
- **左栏**（数据 C17，未读 C14；≥1024 时宽 280px，可折叠）：① **提及与线程**（C19：@我的消息与对我消息的回复，未读加粗，点击定位到原消息并打开线程）；② **频道**：按项目分组（组头 = 项目名，可折叠；组内默认频道置顶，其后自建频道；未读数与 @ 标记；组头〔新建频道〕按 C02 权限显示）；③ **私信**：DIRECT（人↔人、人↔Agent，对方头像 + 状态点，Agent 带芯片角标）按最近消息排序，〔新建私信〕打开对话框（人员 / Agent 选择器，C18）；有权只读的 AGENT_DIRECT（BR-05-91）收在折叠子组「Agent 之间（只读）」；④ **执行会话**（BR-05-119）：本人运行中的交互会话与待我处理的工具授权 / 输入请求，显示执行标题、项目、待处理数与倒计时，点击打开同会话域执行会话（统一 UI，执行消息 / 授权 API 仍由 03 负责）。顶部搜索框按名称过滤（C17 `q`）。
- **中栏**（消息流，虚拟滚动，>500 条流畅）：头部 = 频道名 / 私信对方、所属项目 chip（DIRECT 无）、主题、〔详情〕。**消息渲染**：TEXT（头像、名称、Agent 芯片、时间、已编辑标记、回复引用与「n 条回复」→ 打开线程、附件）；EVENT 折叠行（最新事件 + 「另 n 条」，展开显示全部；attention 事件红 / 琥珀单独显示）；**结构化卡片永不折叠**，按钮直接作用于真实对象（权限仍由对象所属模块判定，会话只是入口）：DECISION_CARD（完成阶梯、验证 n/n、证据覆盖、〔通过〕〔要求修改〕〔拒绝〕——按 02 权限可用，拒绝弹 `StructuredFeedbackForm`；已裁决显示结果与 `AuthorityNote`）、完成声明与停线（来自 02 / 03 事件，以 `attention=true` 的 EVENT 呈现为卡片样式，含〔打开工作项〕〔清除停线〕等跳转，动作在对象页执行）、DISPATCH_CARD（推荐人、分数、理由摘录、护栏、〔确认〕〔改派〕→ 跳 8.6 或内嵌对话框；处理后变为结果行）。撤回消息显示「消息已撤回」。
- **右栏**（≥1280 常驻，较窄时为覆盖式抽屉）：**线程**（根消息 + C08 `reply_to_message_id=eq:<root>` 的回复，底部回复输入框）/ **详情**（频道主题、所属项目、成员列表 C05；私信时显示对方资料，人↔Agent 私信另显示「此私信对 Agent 的负责人与审计员可见」，BR-05-115）。
- **输入框**：多行；`@` 弹出候选（C15，人与 Agent 分组，显示状态；退役 Agent 不出现；DIRECT 中只有对方）；选中插入令牌并渲染为 chip；超 8000 字符禁用发送；附件 ≤5；发送失败保留草稿可重试；Enter 发送、Shift+Enter 换行。提及非成员（服务端 145003）时内联提示并提供〔邀请并发送〕（有权限时；DIRECT 不提供）。无发送权的可读会话（AGENT_DIRECT、审计只读的人↔Agent 私信）不显示输入框，顶部提示只读原因。
- **未读**：进入会话定位到第一条未读，滚动到底部时 PUT read-marker（节流 2 s）；「n 条新消息」浮条；会话域标签与移动端底栏「会话」显示 C14 合计。
- **状态**：加载骨架（三栏占位）；空态（没有任何频道与私信：「还没有会话」〔新建私信〕）；归档频道只读横幅；非成员（CUSTOM）显示〔加入频道〕（有权限时）；对方已退役 / 停用的私信只读横幅；错误态可重试；离线横幅「正在重新连接…」，恢复后按 `after_seq` 补齐。
- **实时**：SSE 新消息 ≤1 s 呈现（主题 `tenant/{t}/channel/{id}`；左栏计数经 `tenant/{t}/inbox/{principal_id}`）。
- **甲方**：甲方为真实外部 Principal，仅出现于有权人创建的共享频道和与指定对接人的私信；共享频道常驻「甲方在此频道」，内部旁注 / 执行事件不返回。需求确认、变更和验收卡引用项目对象，具体规则见 §14。
- **移动端**：单栏堆叠：左栏列表 → 消息流 → 线程（返回键逐级退回）；长按消息出编辑 / 撤回 / 回复菜单；底部快捷栏「会话」显示总未读。

---

### 8.9 自建请求审批与卡片（D110）

- **入口**：08「我的裁决」`AGENT_SPAWN_APPROVAL` 项、项目频道「自建请求」卡片、`/t/:tenantID/agents/spawn-requests/:requestID`。
- **内容**：主管头像与名称、理由全文、规格（角色、机器、runtime × 模型 × 强度、技能、MCP、最大风险）、与授权的逐项比对（✓ / ✗）、单 Agent 成本上限、授权用量（名额 2/3、今日 1/5、预算 $18.40 / $50）、剩余审批时间（24 h 倒计时）。
- **按钮**：〔批准〕（`agent_manage`，无需 step-up；仅 PROPOSED）、〔拒绝〕（理由必填 ≥10）、〔调整授权〕（跳 08）。AUTO 模式下卡片只读，显示「已按授权自动创建」。
- **状态**：PROPOSED（琥珀）、CREATED（绿，链接新 Agent）、REJECTED / EXPIRED（灰）、FAILED（红，显示 `failure_code` 文案）。
- **移动端**：卡片全宽，按钮固定底部。


### 8.x 页面增补（D112 / D113，00 v2.0 §15）

- **成员与 Agent**：角色筛选增加「测试 Agent / 产品验收 Agent / 助理」；Agent 行显示模型家族徽章；产品验收 Agent 行显示「仅人类创建」。
- **Agent 详情**：概览显示模型家族；新增「内部验收」统计（担任测试 / 验收次数、一次通过率、放行后穿透次数与明细链接）。
- **项目「团队编排」卡片**：新增「内部质量门」区块：测试 Agent 池、产品验收 Agent 池（各自模型家族分布，单一家族时黄色提示「无法满足跨家族验收」）、`max_internal_rounds`（只读，来自模板参数 / 项目设置）；纯人工项目显示「内部验收由人完成，Agent 仅为助理」。
- **新建 Agent**：角色选择 ACCEPTOR 时提示「产品验收 Agent 需与执行者使用不同模型家族以保证独立性」，并按团队现有执行者家族推荐模型。

### 8.z 页面增补（D120）

- **Agent 详情头部**：状态徽章旁增加活动状态点（在线 绿实心「在线」、思考中 蓝色脉冲「思考中」、工作中 蓝实心「工作中 · {执行标题}」、出错 红「出错 · {error_code 文案}」、离线 灰空心「离线」；图标 + 文字，不只靠颜色）；〔重启〕旁增加下拉〔重置会话〕〔完全重置…〕（`agent_manage`；完全重置仅在无运行中执行时可用，否则悬浮说明「请先停止它的执行」）。
- **Tab 生命周期**（`/t/:tenantID/agents/:agentID/lifecycle`）：上部为状态机示意（AgentState 与活动状态两行，当前态高亮）；中部为三种重置的说明卡（「重启：保留会话与工作目录」「重置会话：清空上下文，保留工作目录与长期记忆」「完全重置：清空上下文与工作目录，保留长期记忆；需要重新认证」）与按钮；下部为时间线（F09 进程事件 + F22 重置记录，按时间合并，崩溃红、重置琥珀）。完全重置对话框：红色语义、后果清单（「Agent 私有目录将被清空（不影响项目代码与执行工作区）」「当前对话上下文丢失」「长期记忆不受影响，如需清除请到记忆标签撤销」）、理由（≥10 字）、输入 Agent 名称、step-up；提交后进度「已请求 → 机器确认 → 完成（释放 {size}）」，5 分钟未确认显示「机器未确认，将在其重连后完成清理」。
- **Tab 收件箱**（`/t/:tenantID/agents/:agentID/inbox`；负责人类与 `agent_workspace_read` 可见，只读）：顶部计量「已确认 {ack_seq} / 最新 {head_seq} · 未确认 {n}」与唤醒方式（通道 / 轮询 / webhook）；列表按 `seq` 倒序（类型图标与筛选：提及 / 消息 / 派单 / 评审反馈 / 提醒 / 交互），未确认行加粗，`seq ≤ ack_seq` 灰显；每行可跳转到对象（线程、工作项、批注、交互）；积压 >1 h 时琥珀横幅「{name} 有 {n} 条事件超过 1 小时未处理」。实时：订阅 `tenant/{t}/agent_inbox/{agent_id}`。人不能在此代 Agent 确认。
- **Tab 提醒与循环**（`/t/:tenantID/agents/:agentID/reminders`）：两段——「循环契约」（10 L1 `agent_id=eq:<id>`：名称、类型 报告 / 检查、节奏、预算用量条、最近 5 次运行结果点，链接到 10 循环契约详情；〔新建循环契约〕跳 10）；「提醒」（F23：标题、锚点（线程 / 工作项链接）、计划（一次 / 每 n 分钟 / 按周期）、下次触发、状态、创建者「Agent 自设 / {人} / 循环契约」；行操作〔暂停〕〔恢复〕〔取消〕；循环契约提醒只读并注明「由循环契约管理」）。
- **Tab 记忆 / 工作目录**：仅负责人类与 `agent_workspace_read` 持有者可见（BR-05-137）；其他人不显示这两个标签（直接访问 → 无权限态「仅 Agent 的负责人与团队所有者可查看」）。
- **治理 › Agent 操作清单**（`/t/:tenantID/governance/agent-operations`；菜单由本模块贡献，08 §8.6 登记；`agent_read`）：顶部版本选择（M02，显示发布时间与「与上一版差异」：新增 / 删除 / 变更的操作）与〔下载 JSON〕；表格列：操作名、说明、接口、副作用芯片、所需权限码与来源（角色 / 委托 / 策略）、可委托、仅人类、需人确认、配额、`seen_seq` 流、分期；行展开显示输入 / 输出 schema（只读代码块）。第二个标签「Agent 能力」：选择 Agent 与项目 → M03 结果，每行 `可用` 绿 / `未授权` 红（附原因码中文与「缺少 {权限码}」或「仅限人类」等说明，08 §3.5c）/ `配额不足` 琥珀 / `不适用` 灰。空态：清单未发布 →「服务尚未发布操作清单」。移动端：卡片列表，schema 折叠。
- **团队 › 工具与技能**（`/t/:tenantID/tools`，菜单项 `tool-catalog`，08 §8.6；`agent_read` 可读，`tool_catalog_manage` 可写，无该码时所有写按钮隐藏并显示「只有团队所有者可以管理工具目录」）：两个标签——
  - **MCP 服务器**：表格列 名称（`entry_key` + 标题）、传输（「远程 · 经平台中继」/「本地包」）、凭据（「已设置 ••••{last4}」/「未设置」琥珀 /「可能失效」红）、当前版本 v{n}、状态徽章、授予摘要「项目 2 · 角色 1 · Agent 3」；〔新建〕下拉：从推荐预设（T11 卡片网格）/ 自定义 / 从本机导入（列出 04 检测到的本机 MCP）。**条目抽屉**（右侧，手机全屏）：「概览」当前版本配置（只读代码块，无机密）、凭据卡〔设置凭据〕/〔轮换凭据〕（输入框只写不回显，提交前 step-up，完成后提示「新凭据从下一次调用起生效，机器上不会保存凭据」）、〔停用〕〔启用〕〔归档〕；「版本」列表（版本号、内容哈希前 8 位、说明、发布人、状态）与〔发布新版本〕（配置编辑器 + 与当前版本差异，提示「只影响之后开始的执行」）、〔撤回此版本〕；「授予」按 项目 / 角色 / Agent 分组列出，〔添加授予〕选择器，行操作〔撤销〕（填写理由，提示「运行中的执行将在 1 分钟内失去访问」）；「使用情况」选中该条目的 Agent 与最近 7 天经中继的调用数（来自审计索引）。
  - **技能**：数据为 06 技能库镜像：技能名、当前版本 v{n}、内容哈希前 8 位、授予摘要、已挂载的 Agent 数；**条目抽屉**：「版本」（06 已批准版本与哈希，链接到知识库技能页）、「授予」（同上）、「挂载」（06 挂载列表，标出「已挂载但未授予，不会加载」）。
  - 页底「在机器上检测到、未纳入团队目录」折叠区：按机器列出本机自带的 MCP 与技能（琥珀标签「本机自带，未纳入团队目录」），行操作〔纳入团队目录〕（MCP）/〔导入知识库〕（技能）。
  - 五态：骨架；空态「团队还没有纳入目录的 MCP 服务器，可以从推荐预设开始」〔浏览推荐预设〕；错误可重试；无权限说明 `agent_read`；离线横幅。移动端：表格改卡片列表，抽屉全屏，标签吸顶。
- **Agent 详情 Tab 技能（D120 增补）**：在既有 06 挂载列表之上改为两段「技能」「MCP 服务器」，数据为 T12（选择项目切换有效范围）；每行来源徽章：「团队目录 v{n}」（绿，链接到目录条目抽屉）/「本机自带」（琥珀，悬浮说明「未纳入团队目录：不受授予约束，凭据不由平台托管」）/「与团队目录 v{n} 不一致」（红）/「已挂载但未授予，不会加载」（灰）/「已选择但未授予」（灰）/「缺凭据」（琥珀）；〔挂载技能〕仍调用 06 A59，〔编辑 MCP 白名单〕仍走 F04（选择器分「团队目录」与「本机自带」两组）。

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
| N-05-09 | 被 @（人类） | 被提及者（静音频道仍通知提及） | 站内；用户离线 >10 min 且未读时发邮件摘要 | 「{author} 在 #{channel} 提到了你：{excerpt≤140}」；DIRECT 的隐式提及（BR-05-117，D118）用同一通知类型，文案为「{author} 在私信中给你发了消息：{excerpt≤140}」 | 同频道 5 min 内合并；邮件每人每小时 ≤1 封 |
| N-05-10 | 主管 Agent 变为不可用（崩溃用尽 / 退役） | 其主管项目的交付负责人 | 站内 | 「{project} 的主管 Agent {name} 不可用，派单已切回人工确认」 | 每项目每事件一条 |

| N-05-13 | 自建请求待审批（REQUIRE_APPROVAL） | 对请求项目持 `agent_manage` 的人 | 站内（08 收件箱） | 「主管 {supervisor} 申请新增 {role}「{name}」：{reason≤80}」 | 每请求一条 |
| N-05-14 | 自建 Agent 成为孤儿 | 授权人 + 项目交付负责人 | 站内 + 邮件 | 「{supervisor} 创建的 {n} 个 Agent 已停用（{orphan_reason}），7 天内未收编将自动退役」 | 同授权同原因 1 h 合并 |
| N-05-15 | 自建 Agent 24 h 内到期 | 授权人 | 站内 | 「{name} 将于 {time} 到期退役，如需保留请收编」 | 每 Agent 一条 |
| N-05-16 | Agent 活动状态 ERROR 持续 ≥10 min（D120，BR-05-134） | 负责人类 | 站内 + 邮件 | 「{name} 已出错 {minutes} 分钟：{error_code 文案}。可尝试重启或重置会话」 | 每次进入 ERROR 一条 |
| N-05-17 | 收件箱积压 >1 h、积压丢弃或 webhook 连续失败改为轮询（D120，BR-05-124 / 125） | 负责人类 | 站内 | 「{name} 有 {n} 条事件超过 1 小时未处理」/「{name} 的唤醒 webhook 连续失败，已改为轮询」 | 每 Agent 每 24 h 一条 |
| N-05-18 | 团队工具目录条目经中继连续 5 次收到上游 401 / 403（D120，BR-05-147） | `tool_catalog_manage` 持有者 | 站内 + 邮件 | 「MCP 服务器 {title} 的凭据可能已失效（上游连续 5 次拒绝），请轮换凭据」 | 每条目每 24 h 一条 |

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
Given P MANUAL，主管 Agent L；W 进入 READY
When  派单循环运行，L 选择 Ada 并给出理由
Then  一条 PROPOSED 提案（requires_human=true，rationale_source=LEAD），默认频道出现 DISPATCH_CARD（不折叠），N-05-03 发给 Morgan；W 无分派
When  Morgan PUT resolution {kind:CONFIRM}（If-Match 正确）
Then  同事务：02 Assign 生成分派与 ASSIGN Decision（actor=Morgan，authority=ROLE:delivery_lead，detail.dispatch_proposal_id）；提案 APPLIED/CONFIRMED；dispatch.applied；卡片变为「已派给 Ada · Morgan 确认」

AC-05-22  [integration]  关联 BR-05-44, BR-05-45（AUTO）
Given P 切为 AUTONOMOUS（07 置 AUTO），主管 Agent L，W 风险 MEDIUM，预算充足
When  派单循环运行
Then  提案 AUTO_APPLIED；02 ASSIGN Decision actor=L（AGENT）、authority_source=POLICY:dispatch_auto、detail.dispatch_proposal_id=该提案；频道仅一条折叠 EVENT，无卡片
And   W' 风险 HIGH → requires_human=true（guard_reasons 含 G1）；L 选第 3 名且分差 >15 分 → G3；项目预算 EXHAUSTED → G2；均停在 PROPOSED

AC-05-23  [integration]  关联 BR-05-42
Given L 返回 pick=Dev（不合格）或 120 s 未回应
Then  推荐 = 系统 top-1，rationale_source=SYSTEM；卡片注明「主管 Agent 未给出有效建议」

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
Given P 为 AUTONOMOUS（AUTO），主管 Agent L
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


### 10.x 自建 Agent（D110）

公共夹具：主管 Atlas（LEAD，HUMAN_CREATED，RUNNING）；Sam 授予 Atlas 自建授权 G（项目 P，`max_active_agents=3`，`max_spawns_per_day=5`，`allowed_roles=[EXECUTOR]`，机器 M1，CLAUDE_CODE×sonnet，`max_effort=high`，预算 $50，单 Agent $15，`agent_ttl=7d`，`idle_retire=48h`，`approval_mode=REQUIRE_APPROVAL`）。

AC-05-53  [integration]  US-05-17, BR-05-98/99, S01/S02/S04
Given P 为 HYBRID；G ACTIVE；Atlas 已自建 1 个在册 Agent
When  Atlas 以自身凭证 POST agent-spawn-requests {role:EXECUTOR, M1, sonnet, reason≥10}
Then  201，state=PROPOSED；08 生成 AGENT_SPAWN_APPROVAL；频道出现「自建请求」卡片；名额预占 2/3
When  Morgan（agent_manage）PUT decision {APPROVE}（If-Match）
Then  同事务创建 Agent：origin=AGENT_SPAWNED、parent_agent_id=Atlas、spawn_grant_id=G、authorization_mode=ON_REQUEST、expires_at=+7d；请求 CREATED；agent.spawned；审计 fleet.agent_spawned（authority_source=POLICY:spawn_grant）

AC-05-54  [integration]  BR-05-98 ⑥
Given G 下已有 3 个未收编在册 Agent
When  Atlas 再发起请求
Then  422 142032；无请求行；无预占

AC-05-55  [integration]  BR-05-98 ⑦
Given 近 24 h Atlas 已在 G 下创建 5 个（含已退役）
Then  第 6 次请求 422 142033

AC-05-56  [integration]  BR-05-98 ⑧, BR-05-102
Given G 剩余预算 $10 < 单 Agent 上限 $15
Then  请求 422 142037
Given 08 发布 spawn_grant.exhausted(G)
Then  G 下 Agent 在派单预过滤中带 E12 不合格；其 RUNNING 执行继续至结束；相关 PROPOSED 派单提案 EXPIRED

AC-05-57  [integration]  BR-05-98 ③④⑤
Given 请求分别为 model=opus（不在允许列表）/ role=COORDINATOR（不在 allowed_roles）/ role=LEAD / skill 不在 Atlas 挂载内
Then  分别 422 142034（data.field=model_id）/ 142034（role）/ 142036 / 142034（skill_ids）

AC-05-58  [integration]  BR-05-97, BR-05-101（深度）
Given A1 为 Atlas 自建的 EXECUTOR
When  A1 以自身凭证发起自建请求
Then  403 140403（reason_code=SPAWN_NOT_LEAD）
When  管理员尝试给未收编的 A1 加 LEAD 角色
Then  422 142036

AC-05-59  [integration]  BR-05-99（AUTO 与 HYBRID）
Given G.approval_mode=AUTO
When  P 为 AUTONOMOUS 时 Atlas 发起请求
Then  直接 CREATED（无收件箱项），卡片显示「已按授权自动创建」
When  P 为 HYBRID 时发起
Then  state=PROPOSED，data.downgraded=true，需人批准

AC-05-60  [integration]  BR-05-101（FULL_AUTO 与委托）
Given A1 未收编
When  PATCH A1 authorization_mode=FULL_AUTO
Then  422 142038
When  Sam 授予 A1 decision_approve 委托
Then  08 拒绝，reason_code=SPAWNED_NOT_ADOPTED

AC-05-61  [integration]  L02, L03, L05, BR-05-103
Given A1、A2 为 Atlas 在 G 下自建，均 ACTIVE
When  Atlas 完成正常退役（RETIRED）
Then  A1、A2 → STOPPED，orphaned_at 写入，orphan_reason=SUPERVISOR_RETIRED；agent.orphaned；N-05-14；成员列表「孤儿」筛选出 2 行
When  Morgan POST /agents/A1/adoption
Then  A1 lineage_status=ADOPTED，origin 仍为 AGENT_SPAWNED，expires_at 清空，G 名额释放 1；A1 此后可成为委托受托方
When  7 天后 A2 仍未处理
Then  A2 被正常退役，agent.retired{reason:ORPHAN_TIMEOUT}

AC-05-62  [e2e]  BR-05-105（接管）
Given P 为 AUTONOMOUS，G 限定 P，A1 正在执行
When  Sam 在 08 执行一键接管
Then  08 撤销 G；A1 成为孤儿（orphan_reason=TAKEOVER），其执行按 03 置 INTERRUPTED；P 切回 HYBRID

AC-05-63  [integration]  L06, BR-05-104
Given A1 expires_at 到达（或无 ACTIVE 分派超过 48 h）
Then  spawn_lifecycle 发起正常退役，交接文档由 A1 撰写（10 min 超时则系统摘要），完成后 G 名额释放；到期前 24 h 授权人收到 N-05-15

AC-05-64  [integration]  G04 并发
Given 请求 R PROPOSED
When  Morgan 与 Sam 携带同一 ETag 同时 APPROVE
Then  一个成功（CREATED），另一个 409 142009；只创建 1 个 Agent

AC-05-65  [e2e]  §8.1 来源列与筛选
Given 名册含 2 个人类、3 个人工创建 Agent、2 个自建 Agent（其一为孤儿）
When  选择「AI 创建的 Agent」
Then  显示 2 行，徽章分别为「主管创建 · Atlas」「孤儿」；切换〔按主管分组〕显示 Atlas → 2 个子节点与「名额 / 预算」用量


### 10.x 验收标准增补（D112 / D113，00 v2.0 §15）

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-05-66 | integration | IQ1, BR-05-107 | **G** 交付执行 AUTOMATED 通过 **W** `internal_qa.passed{AUTOMATED}` **T** ≤30 s 派 TESTER（SYSTEM 来源），03 启动 INTERNAL_TEST；无人收件箱新增 |
| AC-05-67 | integration | IQ2 | **G** 测试阶段通过 **W** 事件到达 **T** 派 ACCEPTOR，截图随 `agent.accept` 下发 |
| AC-05-68 | integration | BR-05-109 | **G** 执行者为 openai 家族；候选 A（anthropic）、B（openai，不同模型）**W** 派 ACCEPTOR **T** 选 A，`family_fallback=false` |
| AC-05-69 | integration | BR-05-109 | **G** 仅有 openai 家族候选 B（模型不同）**W** 派 ACCEPTOR **T** 选 B，`family_fallback=true` |
| AC-05-70 | integration | IQ3 | **G** 无任何合格 ACCEPTOR **W** 派单 **T** 02 升级（MANDATORY_HUMAN_GATE，reason NO_INTERNAL_QA_AGENT）|
| AC-05-71 | integration | BR-05-108 | **G** Agent X 已担任本轮 TESTER **W** 派 ACCEPTOR **T** X 不在候选中 |
| AC-05-72 | unit | BR-05-112 | **G** 主管创建请求 role=ACCEPTOR **W** 提交 **T** 422 142040 |
| AC-05-73 | integration | BR-05-112 | **G** 编制授权 allowed_roles 含 TESTER **W** 主管申请创建 TESTER **T** 按 D110 流程进入审批 |
| AC-05-74 | integration | BR-05-111 | **G** 项目 HUMAN_ONLY **W** 交付负责人手工把工作项分派给 Agent **T** 422 142041 |
| AC-05-75 | integration | BR-05-111 | **G** HUMAN_ONLY 项目的助理 Agent 起草周报 **W** 草稿生成 **T** 草稿状态，需人确认；助理调用裁决接口 → 403（HUMAN_ONLY_MODE） |
| AC-05-76 | integration | BR-05-110 | **G** 执行 Agent 与评审 Agent 同为 anthropic **W** 评审 Agent 批准计划 **T** 拒绝，reason SAME_MODEL_FAMILY |
| AC-05-77 | integration | BR-05-113 | **G** ACCEPTOR Y 放行的工作项被甲方拒收 **W** 07 发布 `quality.escaped{stage:CLIENT_ACCEPTANCE}`（同一 `escape_key` 重复投递只计一次）**T** Y 的穿透计数 +1，下次派单评分下降；记忆标签出现经验条目 |
| AC-05-78 | integration | 7.x | **G** 项目切换为 HUMAN_ONLY，有 2 个未开始的 Agent 执行分派 **W** `project.mode_changed` **T** 两个分派被释放；EXECUTOR Agent 的项目绑定被撤销 |

### 10.y 验收标准增补（D118：会话空间与成员私信）

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-05-79 | integration | BR-05-114, C18 | **G** 阿澈与 Morgan 为租户成员 **W** 阿澈 POST C18 `{peer_principal_id: Morgan}`（含 10 个并发）**T** 首次 201、其余 200 同一频道，`kind=DIRECT`、`project_id=null`、成员 2 行；Morgan 反向创建 → 200 同一频道；`peer=阿澈本人` → 422 145020；C02 传 `kind=DIRECT` → 422 145020；Agent Ada 调 C18 → 403 145403（HUMAN_ONLY）；PATCH 改名 / 归档、PUT 加成员 → 409 145011 |
| AC-05-80 | integration | BR-05-115 | **G** 阿澈↔Morgan 的 DIRECT 有 3 条消息；Sam 为 team_owner 且持 `audit_read`、`channel_manage` **W** Sam 调 C03 / C05 / C08 / C12、订阅 `tenant/T/channel/{id}`、DELETE 其中一条消息 **T** 全部 403 145403（NOT_MEMBER），SSE 订阅被拒，消息未撤回；审计有 `collab.channel_created` 但无正文；他租户用户用该 id → 404 145404 |
| AC-05-81 | integration | BR-05-115, BR-05-118 | **G** Ada 的 `owner_principal_id`=Sam；阿澈与 Ada 有 DIRECT；Lee 持 `audit_read` **W** Sam 读 C08；Lee 读 C08；Morgan（非参与者、非负责人、无 `audit_read`）读 C08；Sam 发消息 **T** 200 / 200 / 403 NOT_MEMBER / 403 NOT_MEMBER；Sam 的 C17 不含该私信；Ada 退役后阿澈仍可读，发送 → 422 145016 |
| AC-05-82 | integration | BR-05-116, BR-05-117 | **G** P 的主管 Agent L（RUNNING），W 为 READY **W** Morgan 在与 L 的 DIRECT 中写「把 P 的 W 派给 Bo」**T** L 经隐式提及收到投递；生成 `trigger=CHANNEL_REQUEST`、`requested_by=Morgan`、`requires_human=true` 的提案，DISPATCH_CARD 出现在 P 默认频道，私信中出现链接该提案的 EVENT；W 的状态与分派不变；执行者 Ada 在私信中被要求「开始做 W」→ 只回复，无分派、无执行；未指明项目时 L 只回复询问，无提案 |
| AC-05-83 | integration | BR-05-119, C17, C19 | **G** 阿澈可读 P1、P2 的频道，有 1 个 DIRECT，被 @ 2 次 **W** 调 C17、C19 **T** C17 频道按 P1 / P2 分组、私信 1 条、无 AGENT_DIRECT（未传 `include_agent_directs`）；C19 返回 2 条 `reason=MENTION`；结果中无任何甲方联系人；C14 合计含 DIRECT 未读 |
| AC-05-84 | e2e | §8.8 | **G** 视口 1440，阿澈有 1 个 RUNNING 交互会话（待处理 1）**W** 打开 `/t/T/chat` **T** 三栏呈现（左：提及与线程 / 频道 / 私信 / 执行会话；中：消息；右：线程 / 详情），执行会话区显示该会话，点击打开 03 交互会话工作台；DECISION_CARD 与 PROPOSED 的 DISPATCH_CARD 夹在 50 条同 fold_key 事件之间仍单独显示、不折叠；访问 `/t/T/p/P/channels/C` → 重定向 `/t/T/chat/C`；视口 375 → 列表 → 消息 → 线程单栏堆叠 |
| AC-05-85 | unit | §7.2, BR-05-114 | **W** 在 DIRECT 中发送 TEXT **T** `message.posted` 载荷含 `channel_kind=DIRECT`、不含正文；C18 创建时 `channel.created` 的 `project_id=null`；collab 事件投影器不向 DIRECT 写任何 EVENT / 卡片（BR-05-116 的提案链接 EVENT 除外） |

### 10.z 验收标准增补（D120）

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-05-86 | integration | US-05-28, BR-05-120, BR-05-121 | **G** Ada（ACTIVE·RUNNING，`ack_seq=7`）**W** Morgan 在 P 默认频道 @Ada、02 对 Ada 的声明 `REQUEST_CHANGES`、Ada 的执行的工具授权被 Sam 批准 **T** Ada 收件箱依次新增 MENTION、REVIEW_FEEDBACK、INTERACTION（seq 8–10），每条 `payload` 只含 ID，`title` 不含正文；同一领域事件重复投递 → 仍为 3 条；F17 `since=7` 返回 3 条，PUT F18 `{ack_seq:10}` 后 `since` 缺省返回空；PUT `{ack_seq:9}` → 200 仍为 10；PUT `{ack_seq:11}` → 422 142044 |
| AC-05-87 | integration | BR-05-122, BR-05-123, 04 BR-04-102 | **G** Ada 在 M1（WebSocket），`wake_mode=CHANNEL` **W** 1 s 内写入 3 条事件 **T** M1 只收到 1 个 `agent.wake{agent_id, inbox_seq:10}`，帧中无任何标题、ID 或正文字段；断开 WebSocket 丢弃该帧后，M1 下一次 `ServerBatch` 的 `agent_inbox_heads` 含 `{Ada, 10}`，宿主据此读取（≤10 s） |
| AC-05-88 | integration | BR-05-122, BR-05-126 | **G** Ada 为 STOPPED **W** 被 @ 两次后启动 **T** 期间无唤醒提示发出；启动后宿主按游标读到 2 条 MENTION；旧命令 `agent.inbox` 不再下发；频道 EVENT 仍提示「Ada 当前未运行」 |
| AC-05-89 | integration | US-05-29, BR-05-127, BR-05-128 | **G** 频道 C 最新 seq=100，Ada 读到 100 后起草回复 **W** Morgan（人）发消息 seq=101 改变了需求；Ada 以 `seen_seq=100`、键 K 调 C09 **T** 409 145021，`data.state="interrupted"`、`current_seq=101`、`newer_events[0]` 为 Morgan 的完整消息；C 中无 Ada 新消息；`meta.next` 含 `inbox_events.list` 与 `RETRY_WITH_SAME_IDEMPOTENCY_KEY`；Ada 以同一键 K、`seen_seq=101` 重试 → 201（无 176425）；再次以 K 重放 → 返回同一条消息 |
| AC-05-90 | integration | BR-05-127 | **G** 线程 T（根 seq=50）与频道其他顶层消息 seq=51…60 **W** Ada 以 `seen_seq=50`、`reply_to_message_id=T` 回复 **T** 201（顶层消息不打断线程回复）；线程中他人回复 seq=61 后 Ada 再以 `seen_seq=60` 回复 → 409 145021；Ada 发顶层消息不带 `seen_seq` → 422 145022；人类不带 `seen_seq` → 201 |
| AC-05-91 | integration | BR-05-129, 02 BR-02-64 | **G** AUTONOMOUS 项目工作项 W 处于 NEEDS_HUMAN_REVIEW，`activity_seq=40`；评审 Agent R1、R2 均持有效委托 **W** 二者以 `seen_seq=40` 并发调 02 A1 `{role:REVIEWER}` **T** 恰好一个 201；另一个 409 120469 `interrupted`，`newer_events` 含对方的认领，无新分派行；后者以同一键、`seen_seq=41` 重试 → 409 120446（`data.holder`=先到者）；审计无重复认领 |
| AC-05-92 | integration | BR-05-127 豁免 | **G** W 有更新事件，Ada 的 `seen_seq` 已过期 **W** Ada 拉 Andon；停止自己的执行；确认收件箱 **T** 三者均成功，不要求 `seen_seq`、不返回 `interrupted` |
| AC-05-93 | integration | US-05-30, BR-05-130 | **G** 新部署增加操作 `agent_reminder.create` **W** 服务启动 **T** 发布新版本（MINOR+1），sha256 变化，M01 返回 ETag 新值，`If-None-Match` 旧值 → 200、新值 → 304；M02 列出新旧两版且旧版仍可按 `?version=` 读取；某模块登记缺 `input_schema` 的操作 → 启动失败并在日志指明 |
| AC-05-94 | integration | BR-05-131, BR-05-132 | **G** Ada（agent_executor）在 P **W** 调 `channel_message.post` 成功；调 02 D1 `APPROVE`（无委托）**T** 前者 `meta.next` 为数组；后者 403 120403，`data.reason_code=NO_AUTHORITY`（原码不变），`data.result="CAPABILITY_NOT_AUTHORIZED"`、`data.operation="work_item_decision.create"`；M03 中同一操作 `status=CAPABILITY_NOT_AUTHORIZED`、`reason_code=NO_AUTHORITY`；调 03 A21 → `reason_code=AGENT_PROHIBITED` 且清单中该项 `human_only=true` |
| AC-05-95 | integration | BR-05-133 | **G** 主管 Atlas 的编制授权名额已满 **W** M03 查询后再调 G01 **T** M03 中 `agent_spawn_request.create` 为 `QUOTA_EXHAUSTED`（未占用名额）；G01 返回 422 142032（原码），授权用量不变；名额释放后两个并发 G01 只有一个成功（`Reserve` 行锁） |
| AC-05-96 | integration | US-05-32, BR-05-134 | **G** Ada RUNNING **W** 宿主上报 THINKING → WORKING（`execution_id`）→ 连续 5 次 1 s 内的变化 → 进程 EXITED **T** SSE 依次推送 THINKING、WORKING、合并后的最后值、OFFLINE；崩溃（A07）后为 ERROR，持续 10 min 发 N-05-16 一次；旧版宿主不报 `activity` 且有运行中执行 → WORKING；无领域事件、无审计 |
| AC-05-97 | integration | BR-05-135 | **G** Ada RUNNING，`session_epoch=3`，私有目录有 notes.md **W** Sam POST F21 `{kind:SESSION, reason}` **T** 202；下发 `agent.reset{SESSION, session_epoch:4}`；ACK 后 DONE；notes.md 仍在；收件箱游标不变；Ada 的运行中执行不受影响；机器离线期间请求 → 记录 DONE，机器重连后上报 `session_epoch:3` 时服务端补发 `agent.reset{SESSION}` |
| AC-05-98 | integration | BR-05-136 | **G** Ada STOPPED，无非终态执行 **W** Sam 带 step-up `agent_full_reset` 与正确名称 POST F21 `{kind:FULL}` **T** 202，ACK `removed_bytes` 后 DONE，审计 `fleet.agent_full_reset`（含 `reauth_purpose`）；Ada 的 06 记忆条数不变，项目工作目录未被触碰；无 step-up → 403 140403 `REAUTH_REQUIRED`；名称不符 → 422 142050；Ada 有 RUNNING 执行 → 409 142012；主管 Agent 以自身凭证调用 → 403 140403 `AGENT_PROHIBITED`；已有 REQUESTED → 409 142045 |
| AC-05-99 | integration | US-05-33, BR-05-137 | **G** Ada 的负责人为 Sam；Morgan 持 `agent_read`、`machine_read`，不是负责人也无 `agent_workspace_read`；Lee 被授予 `agent_workspace_read` **W** 三人分别调 F12 / F13 与记忆标签 **T** Sam、Lee 200；Morgan 403 140403（`MISSING_PERMISSION`，`missing_permission=agent_workspace_read`），详情页不显示「工作目录」「记忆」「收件箱」标签；Sam 读文件内容 → 审计 `fleet.agent_workspace_read` 只含路径哈希 |
| AC-05-100 | integration | US-05-34, BR-05-138, BR-05-139 | **G** Ada 是线程 T 所在频道成员 **W** Ada 为自己建 `INTERVAL 15 min` 提醒锚定 T；为 Bo 建提醒；建 `interval_minutes=5`；第 51 个提醒 **T** 201；403 `NOT_OWNER`；422 142047；422 142046；到点写入 REMINDER（`stream_key=thread:<T>`、`stream_seq` = 当时最新 seq）并唤醒；Ada 离线 1 h → 上线后只有 1 条 REMINDER，`missed_fires=3`；频道归档 → 提醒 CANCELLED 并有一条说明事件 |
| AC-05-101 | integration | BR-05-140, 10 BR-10-53 | **G** 10 激活 Ada 的日报循环契约 L **W** 激活事务提交、到点触发 **T** 存在 `anchor_kind=LOOP_CONTRACT` 的提醒；触发时 REMINDER `payload.loop_run_id` 与 10 新建的运行一致；用 F24 修改该提醒 → 409 142048；10 暂停 L → 提醒 PAUSED |
| AC-05-102 | e2e | §8.z | **G** Sam 为 Ada 负责人，视口 1440 / 390 **W** 打开 Agent 详情「生命周期」「收件箱」「提醒与循环」与「治理 › Agent 操作清单」 **T** 活动状态点带文字；完全重置对话框含后果清单、名称输入与重新认证；收件箱未确认行加粗、实时增加；操作清单可切换版本并显示差异，「Agent 能力」标签对 Ada × P 显示可用 / 未授权（附原因）/ 配额不足；Morgan（非负责人）看不到收件箱、记忆、工作目录标签；手机为卡片列表 |
| AC-05-103 | integration | BR-05-124（v1.1） | **G** Ada `wake_mode=WEBHOOK`，接收方校验签名 **W** 写入事件 **T** 收到 POST，body 只有 `{agent_id, inbox_seq, sent_at}`，签名 = HMAC-SHA256(secret, timestamp.body)；重放 6 min 前的请求被接收方拒绝；webhook 地址为 `http://10.0.0.5` → 422 142049；连续 20 次失败 → `wake_mode=POLL` 且 N-05-17 |
| AC-05-104 | integration | US-05-36, BR-05-141, BR-05-142, BR-05-149 | **G** Sam 为 `team_owner`；Morgan 为 `delivery_lead`；主管 Agent Atlas **W** Sam 以预设 `issue-tracker` 调 T02；未设凭据即 T07 `{ACTIVE}`；T08 带 step-up 设置凭据后再激活；Morgan 调 T02；Atlas 以自身凭证调 T09 **T** DRAFT 条目含 v1 配置草稿；422 142055；T08 返回 `{set:true, last4}`，T03 / T01 / 审计 `fleet.tool_credential_rotated` 中均无明文；激活成功；Morgan 403 140403（`MISSING_PERMISSION`）；Atlas 403 140403（`AGENT_PROHIBITED`） |
| AC-05-105 | integration | US-05-37, BR-05-143, BR-05-144, BR-05-147 | **G** 条目 `issue-tracker` ACTIVE（v2），Ada 的 `mcp_allowlist` 含 `catalog:issue-tracker`，无任何授予 **W** 03 为 Ada 在 P 派发执行 E1；Ada 的宿主用伪造或属于其他条目的令牌调 T13 **T** E1 工具集不含该条目，`warnings` 为空，T12 中状态 `NOT_GRANTED`；T13 → 401 142056 / 403 `-32001`（`TOOL_NOT_GRANTED`）并审计 `fleet.tool_call_denied`；Sam 授予 PROJECT=P 后派发 E2 → 工具集含 `issue-tracker` v2（`content_sha256` 一致），经中继的 `tools/call` 成功且审计 `fleet.tool_called` 只含入参 SHA-256；Ada 在项目 Q 的执行 E3 不含该条目 |
| AC-05-106 | integration | BR-05-143, BR-05-152 | **G** E2 运行中且已成功调用 **W** Sam T10 撤销 PROJECT 授予；另一条目由 Sam T07 `{DISABLED}` **T** 撤销后 ≤60 s 内 E2 的调用得到 `TOOL_NOT_GRANTED`；停用后下一次调用立即得到 `TOOL_DISABLED`；之后新派发的执行工具集均不含二者；授予行状态 REVOKED 且保留 |
| AC-05-107 | integration | US-05-38, BR-05-146 | **G** `issue-tracker` 凭据 last4=`a1b2`，E2 运行中，上游桩记录收到的凭据 **W** Sam 带 step-up 轮换为新凭据；不带 step-up 再轮换 **T** 下一次中继调用上游收到新凭据，E2 无需重启；M1 收到的 `execution.start` 载荷、`leases.renew` 响应、机器日志与运行时环境变量中均不含新旧凭据（只有工具令牌与本地代理地址）；第二次 403 140403（`REAUTH_REQUIRED`）；审计只含 last4 |
| AC-05-108 | integration | BR-05-145, 03 BR-03-147 | **G** E4 以 `issue-tracker` v2、技能 `deploy` v4 开始运行 **W** Sam 发布 `issue-tracker` v3（url 变更）；06 批准 `deploy` v5；之后派发 E5 **T** E4 后续中继调用仍连向 v2 的地址，E4 证据中的工具集仍为 v2 / v4；E5 工具集为 v3 / v5；E4 的返工分支是新派发，解析为 v3 / v5；以相同配置再次 T05 → 409 142058 |
| AC-05-109 | integration | US-05-39, BR-05-148 | **G** M1 的 04 能力检测含技能 `pdf-tools`（目录中无）、技能 `deploy`（哈希等于 06 v4）与本机 MCP `local-db`；Ada 白名单含 `local-db` **W** 调 T12 并派发执行 E6 **T** `pdf-tools` 为 `LOCAL_ONLY`（「本机自带，未纳入团队目录」）、`deploy` 为团队目录 v4、`local-db` 为 `LOCAL_ONLY`；E6 工具集中三者来源分别为 MACHINE_LOCAL / CATALOG / MACHINE_LOCAL，`local-db` 调用不经中继、无 `fleet.tool_called`；Sam 对 `local-db`〔纳入团队目录〕→ DRAFT 条目，凭据须重新设置 |
| AC-05-110 | integration | BR-05-142, BR-05-151 | **W** 新建条目：`entry_key` 重复；STDIO_PACKAGE 且 `credential_mode=BEARER_TOKEN`；url `https://10.0.0.5/mcp`；STDIO 包缺 `integrity`；配置 `headers` 中含 `Bearer sk-…`；kind=SKILL **T** 依次 409 142051；422 142052（`data.field` 分别为 credential_mode / url / package.integrity / config / kind）；ACTIVE → ARCHIVED 直接迁移 → 409 142053 |
| AC-05-111 | integration | BR-05-150 | **G** 上线前 Ada 挂载 06 技能 S1、S2，Bo 挂载 S1 **W** 运行 `fleet.tool_catalog_migration` 两次 **T** 恰好生成 3 条 AGENT 授予（`granted_by_kind=SYSTEM`）；Ada、Bo 之后的执行工具集仍含原技能；一条汇总审计 |
| AC-05-112 | integration | BR-05-146, 04 BR-04-107 | **G** E7 运行中持有令牌 K（剩余 2 min）**W** 机器 `leases.renew`；E7 进入 SUCCEEDED 后机器再用新令牌调用；以旧 `dispatch_epoch` 的令牌调用 **T** 续签响应含新令牌（有效期 ≤15 min）；终态后 403 `-32001`（`EXECUTION_NOT_ACTIVE`）；旧代次令牌同样被拒；运行时进程环境与 MCP 配置文件中只有 `http://127.0.0.1:<port>/…` 本地代理地址 |
| AC-05-113 | integration | BR-05-147 | **G** 上游连续返回 401 **W** 第 5 次 **T** N-05-18 发给 `tool_catalog_manage` 持有者（24 h 内不重复），T03 `credential.upstream_auth_failing=true`；之后一次成功调用清零 |
| AC-05-114 | e2e | §8.z | **G** 视口 1440 / 390，Sam 为所有者，Morgan 只有 `agent_read` **W** 打开「团队 › 工具与技能」两个标签、条目抽屉与 Agent 详情「技能」标签 **T** MCP 与技能列表、凭据只显示 last4、发布新版本显示差异与「只影响之后开始的执行」、授予撤销提示、页底本机自带折叠区；Agent 技能标签来源徽章正确；Morgan 看不到任何写按钮；手机为卡片与全屏抽屉 |
| AC-05-115 | unit | BR-05-130, 08 BR-08-67 | **G** 某模块登记 `agent_reminder.create`（`via:SELF`，`code` 空）、`interaction.request`（`interaction_request` · ROLE）与一个 `via:ROLE` 但码未在 00 §6.1 登记的操作 **W** 启动 **T** 前两者通过；第三个导致启动失败并指明操作名；M01 文档中 SELF 操作的 `permission.code` 为 null |

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
| `agent_spawn_requested` | `approval_mode, role, runtime_kind, downgraded(bool)` |
| `agent_spawn_decided` | `decision, wait_ms` |
| `agent_spawn_lifecycle` | `event(CREATED / ORPHANED / ADOPTED / TTL_RETIRED / IDLE_RETIRED / ORPHAN_RETIRED), lifetime_hours_bucket` |

运维指标：`fleet_watchdog_failures_total`、`fleet_dispatch_cycle_duration_seconds`、`fleet_lead_plan_timeouts_total`、`collab_projection_failures_total`、`collab_messages_posted_total{kind}`。

---


### 11.x 度量增补（D112 / D113，00 v2.0 §15）

`internal_qa_dispatch_total{role, family_fallback}`、`internal_qa_no_candidate_total{role}`、`acceptor_escape_rate`（按 Agent）、`human_only_assistant_drafts_total{kind, confirmed}`。

### 11.z 度量增补（D120）

| 事件 | 属性 |
|---|---|
| `agent_inbox_event_written` | `kind, wake_mode, agent_running(bool)` |
| `agent_inbox_ack_latency` | `kind, latency_ms_bucket`（写入到确认） |
| `agent_write_interrupted` | `stream_kind(channel/thread/work_item/comment_thread), operation, newer_count_bucket, retried(bool), retry_outcome` |
| `agent_capability_denied` | `operation, reason_code, via` |
| `agent_reset` | `kind, result, removed_bytes_bucket` |
| `agent_activity_error` | `error_code, duration_bucket` |
| `agent_reminder_fired` | `anchor_kind, schedule_kind, missed_fires_bucket` |
| `tool_entry_changed` | `kind, change, transport` |
| `tool_relay_call` | `entry_key_hash, outcome, duration_ms_bucket`（不含工具入参） |
| `toolset_resolved` | `catalog_mcp_count, catalog_skill_count, local_count, warnings_count` |

运维指标：`fleet_inbox_projection_failures_total`、`fleet_inbox_unacked_events{bucket}`、`fleet_wake_sent_total{mode}`、`fleet_wake_webhook_failures_total`（v1.1）、`fleet_manifest_version_info{version}`、`fleet_tool_relay_requests_total{outcome}`、`fleet_tool_relay_upstream_seconds`、`fleet_tool_token_rejections_total{reason}`。

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

| 08（D110） | 自建授权 `SpawnGrant` 模型与授予 / 撤销界面、`SpawnGrantReader.Active`、`SpawnGrantUsage.Reserve / Release`、成本累计与 `spawn_grant.exhausted` / `agent_cap_reached` / `revoked` / `expired` 事件、策略规则 `spawn_grant`、收件箱 `AGENT_SPAWN_APPROVAL`、接管时撤销自建授权、授予委托时拒绝未收编自建 Agent（`SPAWNED_NOT_ADOPTED`）、登记 reason_code `SPAWN_NOT_LEAD` / `SPAWNED_NOT_ADOPTED` | 需 08 同步修订 |
| 03 / 08（D118） | 会话「执行会话」区复用 03 A43 与 08 I1「工具授权与输入」分类（BR-05-119），本模块只列出与链接，不代答；03 尚无「全租户内本人可回应（`interaction_respond`）的交互会话」读接口——若要覆盖非本人发起的会话，需 03 新增 | 03 待定 |
| 08（D118） | §8.6 菜单项 `chat`（Domain=CHAT，`channel_read`）替代原 `channels`；项目空间菜单项 `p-channel`（Domain=PROJECTS，Space=PROJECT，D118 修订）重定向到项目默认频道；会话域全宽无侧栏；旧频道路由重定向（§8.8） | 已在 08 §8.6 登记 |
| 02 / 03 / 12（D120） | 02 `delivery_work_items.activity_seq` 与活动日志、`WorkItemActivityRecorder` / `WorkItemActivityReader`、写入的 `seen_seq` 与 `interrupted`（120469 / 120470）；03 锚定批注、`comment.*` 事件、证据与批注写入的 `interrupted`（130474 / 130475）；12 A13 的 `interrupted`（126061 / 126062） | 已在 02 BR-02-64…66、03 BR-03-133…146、12 BR-12-37 / 38 定义 |
| 04（D120） | `ServerBatch.agent_inbox_heads[]`、`agent.wake` 推送与长轮询唤醒、`AgentProcState.activity` / `session_epoch` 转交 | 已在 04 BR-04-102 / 103 定义 |
| 08（D120） | 权限码 `agent_workspace_read`（负责人类关系授权）、`tool_catalog_manage`、`interaction_request`、`artifact_comment_write`；§3.5c `CAPABILITY_NOT_AUTHORIZED` 映射与 `via` 取值；原因码 `TOOL_NOT_GRANTED`；step-up `agent_full_reset` / `tool_credential_manage`；菜单项 `agent-operations` / `tool-catalog`；审计目录与通知目录；BR-08-67…69 | 已在 08 §3.3d / §3.5c / §5.y / §6.9 / §8.6 / §7.5 / §9.2 登记 |
| 10（D120） | 循环契约、`LoopRunRecorder.OpenOn`；使用本模块 `AgentReminderScheduler` | 已在 10 §3.15–3.16、BR-10-52…58 定义 |
| 06（D120） | A30 人类读者按 BR-05-137 收窄（负责人类 + `agent_workspace_read`）；技能库作为团队工具目录 SKILL 条目的内容与版本来源（`SkillCatalog`、`skill_version.approved`、技能归档） | 已在 06 BR-06-66 / 67 定义 |
| 04（D120 工具目录） | 工具集落地：技能写入执行的运行时技能目录、目录 MCP 写入运行时配置并经本地代理访问中继、STDIO 包按完整性哈希安装、令牌只在机器程序内存；能力检测 S3 增加技能内容哈希 | 已在 04 BR-04-105…107 定义 |
| 03（D120 工具目录） | 派发时调用 `ToolsetResolver`、`execution.start` 携带工具集、`leases.renew` 续签工具令牌、执行记录工具集快照作为证据、`interaction_request` 判定 | 已在 03 BR-03-147 / 148 定义 |
| 09（D120） | C7 幂等辅助：`interrupted`（`data.state="interrupted"` 的 409）不入幂等表、`seen_seq` 不参与请求哈希（00 §9）；C8 purpose `fleet.tool_credential`、`fleet.wake_webhook_secret` | 已在 09 BR-09-84 / 85 定义 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 主管 Agent 被频道文本注入诱导派单 | 频道请求一律需人确认（BR-05-51）；LLM 只能在预过滤集合中选；提案全量可复算 |
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
| Q-05-02 | 主管 Agent 规划调用成本计入哪里 | 计入项目预算（07 成本事件），不计个人 CPA；需 07 接受 `source=LEAD_PLANNING` 的成本行 |
| Q-05-03 | REVIEWER 派单（评审认领）是否也由主管 Agent 提案 | **已决**：AUTONOMOUS 项目在 MVP 即派评审 Agent（BR-05-53，系统评分，复用同一公式，fpa 换为评审一致率）；HYBRID 不派评审，由「我的裁决」承担 |
| Q-05-04 | 频道消息保留期 | v2：项目关闭 365 天后清理正文、保留元数据与审计；需法务确认 |
| Q-05-05 | 评分权重是否允许团队调整 | MVP 固定（可复算、便于比较）；v1.1 允许项目级覆盖并在 `scoring_version` 中体现 |
| Q-05-06 | 是否开放人 ↔ Agent 私信 | 已决 D118：内部成员允许，按双方 / Agent 负责人权限限制；D124 外部仅与对接人私信 |
| Q-05-07 | 不经机器程序托管的「外部 Agent 宿主」如何取得 Agent 凭证（D120 webhook 唤醒的前提） | v1.1 与 webhook 同期：负责人在 F20 旁〔签发外部宿主凭证〕（step-up，凭证只显示一次，复用 BR-05-03 的服务账号凭证），外部宿主只能调用清单内操作；MVP 所有 Agent 仍由机器程序托管 |
| Q-05-08 | 收件箱事件保留 30 天是否足够 | MVP 30 天；未确认事件到期前 24 h 在 N-05-17 中提示；若有审计需要，原始事实仍在各模块中可查 |
| Q-05-09 | `tool_catalog_manage` 是否默认授予交付负责人 | MVP 仅团队所有者（目录托管团队凭据，与 `machine_access_manage` 同级）；如交付负责人需要为本项目开放工具，v1.1 增加「项目级授予」委派 |
| Q-05-10 | 是否允许团队禁止本机自带的 MCP / 技能 | MVP 保持既有行为（白名单内的本机 MCP 可用，本机技能由运行时发现），只标记与记录；v1.1 提供团队设置「只允许团队目录中的工具」，开启后本机自带项不注入、并要求机器程序隔离用户技能目录 |
| Q-05-11 | 需要凭据的本地（STDIO）MCP 服务器 | MVP 不支持（凭据会落到机器上）；v1.1 评估由中继提供令牌交换或改为远程部署 |

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
| CCR-05-11 | §3（D120）：`fleet_operation_manifest_versions` 以 `tenant_id=0` 存系统级只读清单（同 11 一方模板的处理）；`fleet_agent_inbox_events` 作为派生投影允许按 `expires_at` 物理清理（原始事实留在各模块）。§9（D120）：登记「过期上下文写入」约定（`seen_seq`、`interrupted` 响应结构、幂等例外）与清单操作的 `meta.next` | 清单与部署版本绑定、不属任何租户；收件箱量大且可由事实重建；`interrupted` 是跨 02 / 03 / 05 / 12 的统一语义 |
| CCR-05-12 | §8（D120）：新增本模块提供的 `OperationRegistry`（全部模块登记 Agent 可调用操作）与 `AgentReminderScheduler`（10 使用）；使用 02 `WorkItemActivityReader`、10 `LoopRunRecorder` | 清单须由各模块在启动期登记，循环契约由提醒驱动 |
| CCR-05-13 | §6.1 / §6.5（D120）：新权限码 `agent_workspace_read`（fleet，团队级，仅限人类；默认 `team_owner`，Agent 负责人类经关系授权隐式持有）；仅限人类动作增加「Agent 完全重置」（`agent_manage` + step-up `agent_full_reset`） | 工作目录与记忆只给负责人与管理员；完全重置不可由 Agent 自行触发 |
| CCR-05-14 | 06（非 00）：A30 `GET /agents/{agent_id}/memories` 的人类读者从「`knowledge_read` + `agent_read`」收窄为「负责人类或 `agent_workspace_read`」（BR-05-137） | D120「工作区与记忆查看只限负责人与管理员」；06 不在 D120 修订范围，待其修订 |
| CCR-05-15 | §6.1 / §6.4 / §6.5（D120）：权限码 `tool_catalog_manage`（fleet，团队级，仅限人类、不可委托，默认仅 `team_owner`）；§7.1 事件 `tool_catalog.changed`；§8 接口 `ToolsetResolver`（fleet → execution）与 `AgentInboxHeads`（fleet → 04）；§3 基础设施表例外增加 `fleet_tool_presets`（`tenant_id=0`，同 CCR-05-11） | 团队工具目录（D120 第 6 项）：授予、凭据托管与按执行解析工具集 |

**第二轮复审（00 v1.5）**：#2 REDISPATCH 允许非 READY 并经 `Reassign`；#3 BR-05-56 发布说明起草（`agent.release_notes`）；#4/#5/#27 评审派单 SYSTEM 身份、`lease_ttl_seconds=1800`、超时经 02 `WorkItemEscalator` 升级；#9 阶段 ∈ {ACTIVE, ACCEPTANCE_PENDING}；#14 `pending_claim.head_commit`；#18 §6.6 为 Agent 通道唯一定义；#22 `ensemble.compared.requires_human`；#24 立即消费 `project.members_changed`；#26 `POLICY:dispatch_auto` + `detail.dispatch_proposal_id`（CCR-05-10 按此定稿）；新增 AC-05-50..52。

**D110（主管 Agent 自建 Agent）已并入 00 v1.8**：新增模块共享枚举 `AgentOrigin`（HUMAN_CREATED / AGENT_SPAWNED）、权限码 `agent_spawn`（不经角色授予，仅策略规则 `spawn_grant`）、事件 `agent.spawn_requested` / `agent.spawned` / `agent.spawn_rejected` / `agent.spawn_failed` / `agent.orphaned` / `agent.adopted`（发布者 fleet）与 `spawn_grant.*`（发布者 governance）、08 接口 `SpawnGrantReader` / `SpawnGrantUsage`。本规格新增 US-05-17–20、§3.11–3.12、§4.6–4.7、§5.9（BR-05-97–106）、E12、§6.10（G01–G06）、错误码 142031–142039、§7 三个任务与事件、§8.9、N-05-13–15、AC-05-53–65。

> **D112 / D113 已并入（00 v2.0 §15）**：依赖 02 `DispatchAssigner.Assign(role ∈ TESTER / ACCEPTOR)` 非迁移分派与 `WorkItemEscalator`；03 `internal_qa.*`、`ExecutionLauncher.Launch(purpose=INTERNAL_*)`；02 / 07 / 12 Part A `quality.escaped`（D114）；04 能力标记 `browser_capture` 与模型目录的 `model_family`；08 登记 reason `SAME_MODEL_FAMILY`、`HUMAN_ONLY_MODE` 与策略 `plan_approve` 使用 `ModelFamilyOf`；10 助理草稿。对 00 无新增变更请求。

> **D118（会话空间与成员私信，2026-10-12）已并入本规格**：US-05-25…27；`ChannelKind` 增 `DIRECT`（团队级，`project_id` 为空；模块内枚举，按 00 §3 留在本规格）；BR-05-114…119；C08 线程过滤、C14 含私信、C17 / C18 / C19；错误码 145020；`message.posted` 载荷增 `channel_kind`；§8.8 改为会话页 `/t/:tenantID/chat`（旧频道路由重定向）；AC-05-79…85。沿用既有权限码、事件与通知类型，对 00 无新增变更请求。

> **D120（Agent 运行时协议、操作清单、生命周期与提醒，2026-10-12）已并入本规格**：§1 范围、US-05-28…35、§3.z（收件箱、运行态、重置、提醒、清单版本与模块内枚举）、§4.z 状态机、BR-05-120…140、§6.z（F17–F24、M01–M03、S-M1 清单文档、`agent.wake` / `agent.reset` / `agent.status` 增补、`OperationRegistry` / `AgentReminderScheduler`、错误码 142044–142050 与 145021 / 145022）、§7.z、§8.z（Agent 详情「生命周期」「收件箱」「提醒与循环」标签与「治理 › Agent 操作清单」）、N-05-16 / 17、AC-05-86…103、§11.z。BR-05-16（工作目录权限）与 BR-05-85、§6.6 `agent.inbox`（投递方式）按 D120 收窄 / 取代。对 00 的请求为 CCR-05-11…13、CCR-05-15（已登记入 00 v2.0 D120 补充）；CCR-05-14 已由 06 BR-06-66 落实。

> **D120 第 6 项（团队工具目录：MCP 服务器与技能，2026-10-12）已并入本规格**：§1 范围、US-05-36…39、§3.z（`fleet_tool_entries`、`fleet_tool_entry_versions`、`fleet_tool_credentials`、`fleet_tool_grants`、`fleet_tool_presets` 与枚举 `ToolEntryKind` / `ToolEntryState` / `McpTransport` / `ToolCredentialMode` / `ToolVersionState` / `ToolGrantSubjectKind` / `ToolSource`）、§4.z TE-状态、BR-05-141…152、§6.z（T01–T13、S-T1、`ToolsetResolver`、`AgentInboxHeads`）、错误码 142051–142058、§7.z（`fleet.tool_catalog_sync` / `_migration`、`fleet.tool_upstream_watch`、事件 `tool_catalog.changed` 与审计）、§8.z（「团队 › 工具与技能」与 Agent 详情「技能」标签来源徽章）、N-05-18、AC-05-104…115、§11.z、Q-05-09…11、CCR-05-15。操作清单的权限码按 08 §3.5c 落实：`via` 增加 RELATION / SELF，`interaction.request` 使用 `interaction_request`（08 §3.3d）。


## 14. 共享频道、流程起草与原型完整性

### 14.1 D124 共享频道替代旧甲方排除规则

§8.8 中「甲方不出现在会话」类限制已废止。频道 `visibility=INTERNAL|CLIENT_SHARED` 默认 INTERNAL；共享频道只由有权人创建 / 邀请外部成员，并显示常驻「甲方在此频道」与成员名单。服务端针对外部成员过滤消息、结构化卡片、附件、搜索、未读数量、线程与 SSE；内部旁注使用独立内部线程，不能通过展开卡片或引用跨入外部响应。甲方私信只允许自己的对接人，不能任意找内部成员 / Agent。协作侧栏不放任何频道链接，顶部会话是唯一沟通入口。

确认单草稿 / 签认 / 变更 / 交付 / 验收卡片引用 01 / 02 / 07 真实对象，不能从聊天直接完成签认 / 改范围。Agent 对外消息默认进入人审草稿；可经策略允许事实 / 进度答复，但日期、价格、范围、承诺由人。外部身份不继承 `channel_read` 或租户普通成员的内部可见性。D122 富媒体 / 可编辑语音转录 / 联网搜索引文卡开发 v1.1、原型全做；搜索默认未授予，查询包含客户机密需人批准且审计。

协调员 / 主管 Agent 可从已签认确认单或共享频道请求提出 WorkflowPatch，图与 JSON 展示差异、依据与影响，11 / 02 人审批后发布 / 应用；草稿不触发工作项执行。运行期重拆要新基线补丁裁决。循环、无进展、连续依赖缺失和副作用确认按 00 §16.1 / §16.4，AI 不自行延长预算或上限。

### 14.2 分期与页面验收

D110 编制授权 / 自建、Heavy、完整目录与生命周期增强开发 v1.1；MVP 固定人员 / Agent 队伍、简单派单与 D113 内部质量门。D109 接受旧规格的决定不恢复已由 E2 收窄的 MVP。Agent 新建与退役原型为完整分步向导（配置校验 / 预览 / 确认 / 失败回退；退役交接 / 转交 / 清理 / 吊销），审批 / 孤儿治理等延后场景加 v1.1 徽标。

验收：甲方在共享频道正常交流，但直达内部频道 / 线程 / 附件 / SSE 被拒且不泄露；对外 Agent 承诺即便授予事实答复也必须人审；流程草稿不自动发布；外部搜索卡只包含已批准对外来源；共享频道内不展示内部 QA 失败卡。截图演示不能替代产品权限测试。

### 14.P 本域易用性与大数据验收

本域适用 00 §16.8：服务端权限过滤 / 稳定游标分页，不全量拉取前端后筛；长列表虚拟化、聚合服务端取，精确总数不阻塞首屏；涉及看板按列分页、频道历史锚点与增量、流程图可见渲染 / 子流程惰性加载、文件范围读。切项目 / 筛选取消过期请求，保留阅读 / 草稿上下文，明示 loading / 空 / 超时 / 限流 / 断流 / 重试。协作二级菜单不跳会话域。规模与 API / UI 性能预算是开发后待实测门槛，原型不宣称已通过。


交互会话 UI 复审统一：重复独立 session 页面退休；执行资源 / API 不变，执行列表与工具授权待办的对象链接进入会话域 `/t/:tenantID/chat?execution_id=:executionID`（原型 `chat/exec-tax`），新建交互会话复用同一列表创建。旧 `/executions/:executionID/session` UI 路径不注册 / 无 alias 或重定向，所有引用直接使用规范会话路径；消息、工具授权、上下文共用同一真实状态，禁止假操作按钮。此为对象上下文链接，协作侧栏仍不跨域，见 00 §16.9。

### 14.Q 集合读取 API 分页参数同步

本规格 §6 全部集合 GET operation（包括现有列表和本次补的读模型）采用 00 §16.10 的唯一产品分页合同：`cursor?` / `limit?`（默认50、最多100）/ 白名单 `sort?`，保留各 operation 的业务 filter / q / project_id；返回 `items,next_cursor?,has_more`，旧 offset / page / page_size 参数退休，不并存两套 API。消息历史 anchor_seq / 增量 after_seq 单独按语义使用；聚合 / 有限枚举不分页。服务端稳定排序与权限 / 筛选绑定 cursor，不依赖精确 COUNT。公共底座既有查询不改，本产品开发前 W1 检查 pkg/query 能力及最小复用 / 扩展 ADR，不能假称已具备游标。旧接口表分页字段由本条原地替代，最终 OpenAPI 只生成这一合同。
