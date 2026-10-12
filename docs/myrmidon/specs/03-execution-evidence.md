# 03 · 执行、交互会话、证据与验证（模块 `execution`）

> 上位文档：`PRD.md`（§5、§6.4、§6.5、§7、§10.3、§11）与 `00-conventions.md`（**约束性契约**，本文不重定义其中的枚举、权限码、事件名、错误码段；需要变更者列在 §13）。
> 语义移植来源（只读）：`MyrmidonAI` Preview 01（`docs/preview.md`、`docs/runtime-modes.md` §6–§9、`internal/platform/{server,types,daemon}.go`、`src/{bridge,chat}.ts`、`apps/web/app/*`）与旧 PRD §13、§18、附录 F.3/F.8/F.10。
> 标注「**本规格提议**」的条目是本文的新设计（旧实现与 PRD 未给出），附理由；其余条目均可追溯到上述来源。

---

## 1. 范围与目标

**解决**：把"一次具体运行"做成可追溯、可中断恢复、可分支返工的领域对象，并把它产出的东西变成**带来源与局限的证据**、经**版本化验证器**检查的**验证结果**。

| 本规格负责 | 交给其他规格 |
|---|---|
| Execution（尝试树：`parent_execution_id`、`BranchReason`、序号与"#3 · 质量返工 2"标签）；种类 AUTONOMOUS / INTERACTIVE / ADHOC | 工作项状态机、Claim 表与 `POST /work-items/{id}/claims`、Decision、结构化反馈表单 → **02** |
| 执行生命周期、执行租约与 `dispatch_epoch` 栅栏（移植 Preview 01：30 s 租约、服务重启/机器离线即中断、不自动重放） | 机器接入、心跳、通道传输（HTTP 轮询 / WebSocket）、执行槽调度、运行时与能力（Skills/MCP）检测、模型目录 → **04** |
| 执行级派发消息（start / cancel / 交互回应 / 证据采集 / 验证 / 合并）、事件流 `seq`、outbox 确认语义 | Agent 资料（系统提示、runtime×model、`max_context_tokens`、技能、授权模式）、主导 Agent 选择 ENSEMBLE 胜者（`ensemble.compared`）→ **05** |
| 注入运行时的**七层上下文包**、`maxContextTokens` 预算与裁剪顺序、"使用的知识"记录 | 检索与注入包生成（`Retriever`）→ **06** |
| 执行环境加固（不注入控制面令牌、环境变量白名单、每执行工作目录/worktree、同目录单写） | 预算阈值与升级规则（`budget.threshold_reached`、`escalation.triggered`）、委托条件判定、"我的裁决"聚合 → **08** |
| 交互会话（Preview 01 全量对齐）：轮次、消息、工具行、TOOL_PERMISSION / RUNTIME_INPUT、重试、换机器、授权模式、Skills、MCP、附件、限额 | 项目阶段、预算设定、交付包 → **07** |
| 产物、证据（类型、自动采集、人工观察、来源字段、局限、内容哈希、文件存储）、验证器定义与版本、验证结果、相似度信号、成本计量、`code_merge` 动作 | 后台任务队列、outbox、SSE、对象存储驱动、通知渠道 → **09**；文件与资源绑定、审计哈希链 → 底座 |

**不解决**：token 级 checkpoint；数据库分支；PR 平台连接器（v1.1，PULL_REQUEST 证据 MVP 仅支持人工登记链接）；跨机器自动迁移正在运行的进程。

---

## 2. 用户故事

| ID | 故事 | 期 |
|---|---|---|
| US-03-01 | 作为工作项分派到的 **Agent 执行者**，系统要在我绑定的机器上为我启动无人值守执行并注入完整上下文，以便我无需人工即可开工 | MVP |
| US-03-02 | 作为**工程师**，我要在任一在线机器上为分派给我的工作项开启交互会话，与 Claude Code / Codex 多轮对话，以便用熟悉的方式工作 | MVP |
| US-03-03 | 作为工程师，我要先开一个**临时执行（ADHOC）**做探索，确认方向后再关联到工作项并提交完成声明 | MVP |
| US-03-04 | 作为工程师，我要在手机上处理"允许一次 / 拒绝"的工具授权与输入请求，关掉页面执行也不中断 | MVP |
| US-03-05 | 作为工程师，机器离线时我要把会话切到另一台支持同一 runtime 的机器：同目录续接原生会话，不同目录带入最近对话 | MVP |
| US-03-06 | 作为执行者，我要提交完成声明时系统**自动收集** commit / 测试报告 / 日志，并让我逐条把验收标准映射到证据 | MVP |
| US-03-07 | 作为评审者，我要看到每条证据的**来源卡**：producer、时间、哈希前 8 位、验证器与版本、结果、局限 | MVP |
| US-03-08 | 作为团队所有者，我要定义**版本化验证器**（命令型、AI 辅助、人工），并按工作项类型 + 风险决定哪些是必需的 | MVP |
| US-03-09 | 作为交付负责人，我要在验证 FAILED / INCONCLUSIVE 时看到退出码、输出尾部与报告文件，以便判断是代码问题还是环境问题 | MVP |
| US-03-10 | 作为交付负责人，我要在工作项详情看到**执行分支树**（`#3 · 质量返工 2`），旧执行与证据永不被覆盖 | MVP |
| US-03-11 | 作为交付负责人，拒绝后系统要自动创建**质量返工子执行**并把结构化反馈注入其上下文 | MVP |
| US-03-12 | 作为交付负责人，当返工产物与上一次相比变化文件 < 8% 时我要收到"原地打转"信号 | MVP |
| US-03-13 | 作为团队所有者，我要看到每个执行的 token 与成本，并在单执行成本上限、项目预算触顶时自动暂停 | MVP |
| US-03-14 | 作为执行者，机器断网或服务重启后我要看到"已中断"而不是假装仍在运行，由我检查副作用后决定恢复或标记失败 | MVP |
| US-03-15 | 作为安全负责人，我要保证运行时进程环境里**没有**控制面令牌与机器凭证 | MVP |
| US-03-16 | 作为有 `code_merge` 权限的主体，我要在工作项被接受且验证全过后把执行分支合并到主分支 | MVP |
| US-03-17 | 作为工程师，我要在会话里用 `/`、`$` 插入 Skill、查看 MCP 列表、附加最多 8 个文本文件 | MVP |
| US-03-18 | 作为执行者，我要在会话里看到"使用的知识"：本次注入了哪些知识条目、得分与引用 | MVP |
| US-03-19 | 作为主导 Agent，我要对高风险工作项启动 ≤3 个 ENSEMBLE 并行分支并获得对比数据 | v1.1 |
| US-03-20 | 作为团队所有者，我要开启"全自动授权"时被要求重新认证，且该动作写入审计链 | MVP |
| US-03-21 | 作为评审者，我要以**人工验证器**身份记录 PASSED / FAILED / INCONCLUSIVE 与观察 | MVP |
| US-03-22 | 作为交付负责人，执行在 PAUSED（上下文不足 / Agent 放弃 / 成本上限）时我要能恢复或改为新分支 | MVP |

---

## 3. 领域模型与数据表

### 3.1 关系

```
WorkItem(02) 1─* Execution ─┬─ parent_execution_id → Execution（尝试树）
                            ├─ ensemble_group_id（ENSEMBLE 兄弟分支）
                            ├─ 1─* Turn ─ 1─* Message（含工具行）
                            ├─ 1─* Interaction（TOOL_PERMISSION / RUNTIME_INPUT）
                            ├─ 1─* ExecutionEvent（机器回传事件，按 dispatch_epoch+seq 去重）
                            ├─ 1─* Artifact（commit / branch / 文件 / continue.md …）
                            ├─ 1─* ContextItem（七层上下文条目与知识引用）
                            ├─ 1─* EvidenceItem ──*─1 Claim(02)
                            ├─ 1─* EvidenceCollection
                            └─ 1─* Merge
Claim(02) 1─* Verification *─1 VerifierVersion *─1 Verifier(团队级)
Machine(04) 1─* WorkdirLock ─1 Execution ；Machine 1─* Command（服务端 → 机器 outbox）
```

### 3.2 通用约定与三方言映射

每张表都含 `id BIGINT PK`（雪花）、`tenant_id`、`created_at`、`updated_at`、`created_by`、`updated_by`（00 §3）；项目内资源另含 `project_id`；可变聚合根另含 `version INT NOT NULL DEFAULT 1`。下表字段表省略这些通用列，仅在有特殊约束时列出。**不建跨模块外键**；模块内外键（如 `parent_execution_id`）在三方言中均建立。

| 逻辑类型 | PostgreSQL | MySQL 8.0.16+ | SQLite |
|---|---|---|---|
| ID | `BIGINT` | `BIGINT` | `INTEGER` |
| TS（UTC） | `TIMESTAMPTZ(3)` | `DATETIME(3)` | `DATETIME` |
| ENUM | `VARCHAR(32)` + `CHECK (col IN (...))` | 同左（CHECK 生效） | `TEXT` + `CHECK` |
| STR(n) | `VARCHAR(n)` | `VARCHAR(n)` utf8mb4 | `TEXT` + `CHECK(length(col)<=n)` |
| TEXT64K | `TEXT` + CHECK `octet_length<=65536` | `TEXT` | `TEXT` + CHECK |
| TEXT1M | `TEXT` + CHECK `octet_length<=1048576` | `MEDIUMTEXT` | `TEXT` + CHECK |
| JSON | `JSONB` | `JSON` | `TEXT` + `CHECK(json_valid(col))` |
| BOOL | `BOOLEAN` | `TINYINT(1)` | `INTEGER CHECK(col IN (0,1))` |
| HASH | `CHAR(64)`（小写十六进制 SHA-256） | `CHAR(64)` | `TEXT` + `CHECK(length(col)=64)` |
| INT / BIGINT | `INTEGER` / `BIGINT` | `INT` / `BIGINT` | `INTEGER` |

> 时间列类型按底座 `backend-module` 规约（MySQL 禁用 `TIMESTAMP`），与 00 v1.3 §3 一致。部分唯一索引（PG/SQLite 支持、MySQL 不支持）一律**不使用**，改用独立锁表或可空列唯一索引（三方言均允许多个 NULL）。

### 3.3 `execution_executions`（聚合根，`version` 乐观锁）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | 项目 entity id |
| work_item_id | ID | Y | | `kind=ADHOC` 时必为 NULL；其余必非空（CHECK）。ADHOC（含 HANDOVER）由调用方显式给出 `project_id` |
| kind | ENUM `ExecutionKind` | N | | AUTONOMOUS / INTERACTIVE / ADHOC |
| purpose | ENUM `ExecutionPurpose` | N | `DELIVERY` | DELIVERY / VERIFICATION（AI 辅助验证的执行；不进分支树）/ HANDOVER（05 退役交接文档的 ADHOC 执行，不进分支树，E27 豁免） |
| parent_execution_id | ID | Y | | FK → 本表；同 `work_item_id` |
| branch_reason | ENUM `BranchReason` | Y | | ADHOC / VERIFICATION 为 NULL；其余必非空 |
| seq_no | INT | Y | | 工作项内序号（1 起，按创建顺序，跨整棵树）；UNIQUE(`tenant_id`,`work_item_id`,`seq_no`) |
| reason_ordinal | INT | Y | | 同工作项同 `branch_reason` 内第几次（标签"质量返工 2"） |
| ensemble_group_id | ID | Y | | ENSEMBLE 兄弟共享（雪花，由发起方分配） |
| source_decision_id | ID | Y | | 触发本分支的 02 Decision；UNIQUE(`tenant_id`,`source_decision_id`) 保证事件重放不重复建枝 |
| source_change_record_id | ID | Y | | SCOPE 分支对应的 02 ChangeRecord |
| start_point | ENUM | N | | `BASELINE`（项目基线）/ `RESTART`（父的起点 `base_commit`）/ `CONTINUE`（父 `head_commit`）；对应 02 `workspace_mode` |
| autostart | BOOL | N | true | false 时停在 CREATED 不派发，等人显式启动（02 有界自治：返工 ≥3 次） |
| title | STR(200) | N | | 默认 = 工作项标题；ADHOC 默认"新会话"，首条消息前 60 字符自动命名（手动命名后不覆盖，`title_manual`） |
| title_manual | BOOL | N | false | |
| state | ENUM `ExecutionState` | N | `CREATED` | |
| state_reason | ENUM `ExecutionEndReason` | Y | | 见 §3.17 |
| state_detail | STR(2000) | Y | | 人读说明（已脱敏） |
| initiator_kind / initiator_id / authority_source | ENUM / ID / STR(96) | N/Y/N | | 发起者三元组（00 §4） |
| assignee_principal_id | ID | N | | 执行者：人类 principal 或 Agent 服务账号 principal |
| agent_id | ID | Y | | AUTONOMOUS 必非空 |
| assignment_id | ID | Y | | 02 Assignment |
| machine_id | ID | Y | | ADHOC/INTERACTIVE 创建时必选；AUTONOMOUS 取 Agent 绑定机器 |
| runtime | ENUM `RuntimeKind` | N | | 不自动回退（Preview：明确选择不存在的 runtime 即报错） |
| model | STR(160) | Y | | 原样传给 runtime；空 = runtime 默认；禁止 `\r\n\0` |
| effort | STR(32) | Y | | 取值受模型目录约束（04）；目录缺失时用 runtime 通用集 |
| authorization_mode | ENUM `AuthorizationMode` | N | `READ_ONLY` | READ_ONLY / ON_REQUEST / FULL_AUTO |
| tools_enabled | BOOL | N | false | Claude Code 原生工具、MCP、Skills 开关 |
| workdir_mode | ENUM | N | | `WORKTREE` / `SHARED_DIR` |
| workdir_path | STR(1024) | Y | | 机器上的绝对路径（不建索引） |
| workdir_key | HASH | Y | | `sha256(machine_id + ":" + 规范化路径)`；Windows 主机路径小写化 |
| base_ref / base_commit | STR(255) / STR(64) | Y | | 分支起点 |
| branch_name | STR(255) | Y | | WORKTREE 模式 `exec/<work_item_id>/<execution_id>` |
| head_commit | STR(64) | Y | | 最近一次回报的 HEAD |
| native_session_id | STR(255) | Y | | Codex thread id / Claude session id；**仅**与 machine+workdir 一起有效 |
| carryover_pending | BOOL | N | false | 下一轮附带最近对话（换机器不同目录、重试） |
| dispatch_epoch | INT | N | 0 | 每次派发（AUTONOMOUS 启动/恢复、INTERACTIVE 每轮）+1；栅栏令牌 |
| last_seq | INT | N | 0 | 当前 `dispatch_epoch` 已持久化的最大事件序号 |
| change_seq | BIGINT | N | 0 | 面向客户端的单调变更水位（跨派发），SSE 帧携带（与 09 全局 `stream_seq` 无关，故不同名） |
| lease_expires_at | TS | Y | | 仅 RUNNING 有值 |
| dispatched_at / accepted_at / cancel_requested_at | TS | Y | | |
| started_at / ended_at | TS | Y | | `ended_at` 仅终态 |
| exit_code | INT | Y | | 适配器退出码（§5.4） |
| timeout_ms | BIGINT | N | 14400000 | AUTONOMOUS 单次派发时长上限（4 h；可配 600000–86400000）；INTERACTIVE 为单轮上限 7200000 |
| max_cost_micros | BIGINT | Y | | 单执行成本上限（来自 02/07 工作项预算） |
| cost_micros / input_tokens / output_tokens / cache_read_tokens / cache_write_tokens | BIGINT | N | 0 | 累计 |
| cost_source | ENUM | N | `UNKNOWN` | RUNTIME_REPORTED / ESTIMATED / UNKNOWN |
| cost_published_micros | BIGINT | N | 0 | 上次 `execution.cost_updated` 发布值（合并发布用） |
| turn_count / message_count | INT | N | 0 | 限额 500 / 4000 |
| output_bytes | BIGINT | N | 0 | 消息正文 UTF-8 字节累计，限额 8 MiB |
| pending_interaction_count | INT | N | 0 | 列表"待你决定"徽标 |
| context_bundle_file_id / context_bundle_hash / context_tokens | ID / HASH / INT | Y | | 上下文包文件（§5.5） |
| retrieval_log_id | ID | Y | | 06 `BuildInjection` 输出的 `retrieval_log_id`（06 BR-06-25）；组包时写入，`RecordInclusion` / `RecordCitations` 以它为键（复审 #15） |
| similarity_ratio_ppm / similarity_flag | INT / BOOL | Y / N | NULL / false | 相似度（§5.10），ppm=比例×10⁶ |
| claim_id | ID | Y | | 本执行提交的 02 Claim |
| pending_claim | JSON | Y | | 仅 `branch_reason=ENSEMBLE`：退出码 0 时暂存的声明 `{summary, criteria_map, evidence_ids[], head_commit, confidence?}`，不调用 `ClaimSubmitter`；被 05 选中后经 `ClaimSubmitter`（`authority_source=POLICY:<plan_id>`）提交（BR-03-08） |
| log_file_id | ID | Y | | 完整日志文件 |
| infra_retry_depth | INT | N | 0 | 连续 RETRY_INFRA 祖先数 |
| archived_at | TS | Y | | 仅隐藏，不删除 |

索引：`(tenant_id, project_id, state, created_at DESC)`；`(tenant_id, work_item_id, seq_no)` UNIQUE；`(tenant_id, machine_id, state)`；`(state, lease_expires_at)`（看门狗）；`(tenant_id, parent_execution_id)`；`(tenant_id, ensemble_group_id)`；`(tenant_id, assignee_principal_id, state)`；`(tenant_id, source_decision_id)` UNIQUE。
方言差异：SQLite 外键需 `PRAGMA foreign_keys=ON`（底座已开）；MySQL `DESC` 索引 8.0 起生效；CHECK `kind='ADHOC' = (work_item_id IS NULL)` 在 MySQL 写作 `(kind='ADHOC' AND work_item_id IS NULL) OR (kind<>'ADHOC' AND work_item_id IS NOT NULL)`（VERIFICATION 用途除外的同构约束同理）。

### 3.4 `execution_turns`（INTERACTIVE / ADHOC 的轮次）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id, execution_id | ID | N | | |
| turn_no | INT | N | | 1..500；UNIQUE(`execution_id`,`turn_no`) |
| client_request_id | STR(64) | N | | 客户端生成；UNIQUE(`execution_id`,`client_request_id`)（Preview 请求 ID 去重） |
| retry_of_turn_id | ID | Y | | 重试来源 |
| state | ENUM `TurnState` | N | `QUEUED` | |
| prompt_message_id | ID | N | | 指向 USER 消息 |
| settings_snapshot | JSON | N | | 本轮生效设置（§3.16 S1）；轮中修改设置**不影响**本轮 |
| dispatch_epoch | INT | Y | | 被接受时的派发代次 |
| has_output | BOOL | N | false | 是否产生过 ASSISTANT/TOOL 消息（重试判定） |
| input_tokens / output_tokens / cost_micros | BIGINT | N | 0 | |
| end_reason | ENUM `ExecutionEndReason` | Y | | |
| started_at / ended_at / cancel_requested_at | TS | Y | | |

### 3.5 `execution_messages`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id, execution_id | ID | N | | |
| turn_id | ID | Y | | 系统消息可无 |
| msg_no | INT | N | | 执行内递增；UNIQUE(`execution_id`,`msg_no`)；分页游标 |
| role | ENUM | N | | USER / ASSISTANT / TOOL / SYSTEM |
| text | TEXT1M | N | '' | ASSISTANT 文本增量追加到同轮最后一条 ASSISTANT 消息（Preview） |
| tool_call_id | STR(128) | Y | | TOOL 行按 (`execution_id`,`tool_call_id`) upsert：running → completed/failed |
| tool_title / tool_state | STR(200) / STR(32) | Y | | |
| tool_details | TEXT64K | Y | | 超出部分截断并标注"完整内容见日志" |
| attachments | JSON | Y | | USER 消息附件元数据（§3.16 S2），正文已并入 `text` |

索引：`(execution_id, msg_no)` UNIQUE；`(execution_id, tool_call_id)`。

### 3.6 `execution_interactions`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id, execution_id | ID | N | | |
| turn_id | ID | Y | | AUTONOMOUS 为 NULL |
| dispatch_epoch | INT | N | | 只能回送给同一派发 |
| native_request_id | STR(128) | N | | 执行桥生成的关联 ID；UNIQUE(`execution_id`,`dispatch_epoch`,`native_request_id`) |
| kind | ENUM `InteractionKind` | N | | |
| state | ENUM `InteractionState` | N | `PENDING` | |
| title | STR(200) | N | | 例："Bash: npm test" |
| details | TEXT64K | Y | | 工具入参（已脱敏） |
| message | STR(2000) | Y | | RUNTIME_INPUT 提问 |
| url | STR(2048) | Y | | 仅 http/https 渲染为链接 |
| cwd | STR(1024) | Y | | |
| schema_json | JSON | Y | | ≤32 KiB；RUNTIME_INPUT 的 JSON Schema |
| response_action | ENUM | Y | | ACCEPT / DECLINE / CANCEL |
| response_content | JSON | Y | | ≤32 KiB，按 `schema_json` 校验 |
| responder_kind / responder_id / responder_authority | ENUM / ID / STR(96) | Y | | |
| expires_at | TS | N | | `created_at + 30 min`（runtime 给出更早期限时取更早） |
| decided_at / delivered_at / closed_at | TS | Y | | |
| closed_reason | STR(32) | Y | | TIMEOUT / TURN_ENDED / EXECUTION_ENDED / FENCED / DELIVERY_FAILED |

索引：`(tenant_id, state, expires_at)`；`(tenant_id, project_id, state)`；`(execution_id, state)`。

### 3.7 `execution_events`（机器回传原始事件，保留 30 天）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| execution_id | ID | N | |
| dispatch_epoch | INT | N | |
| seq | INT | N | 派发内从 1 连续；UNIQUE(`execution_id`,`dispatch_epoch`,`seq`) |
| event_type | STR(32) | N | §6.3 事件类型 |
| payload | JSON | N | ≤256 KiB（文本增量分批） |
| occurred_at | TS | N | 机器时间（仅展示） |
| received_at | TS | N | 服务端时间（权威） |

### 3.8 `execution_commands`（服务端 → 机器 outbox）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| machine_id | ID | N | | |
| execution_id / verification_id / collection_id / merge_id | ID | Y | | 恰一非空 |
| command_type | ENUM | N | | EXECUTION_START / EXECUTION_CANCEL / INTERACTION_RESPONSE / EVIDENCE_COLLECT / VERIFICATION_START / VERIFICATION_CANCEL / MERGE_START |
| dispatch_epoch | INT | Y | | |
| payload | JSON | N | | ≤1 MiB（§6.3） |
| state | ENUM | N | `PENDING` | PENDING / FETCHED / ACKED / EXPIRED / SUPERSEDED |
| fetch_count | INT | N | 0 | 未 ACK 时每 10 s 可重新下发，最多 6 次 |
| fetched_at / acked_at / expires_at | TS | Y | | |

索引：`(machine_id, state, created_at)`；`(execution_id, command_type)`。

### 3.9 `execution_workdir_locks`（同目录单写）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| machine_id | ID | N | |
| workdir_key | HASH | N | UNIQUE(`machine_id`,`workdir_key`) |
| execution_id | ID | N | 持有者 |
| dispatch_epoch | INT | N | |
| acquired_at | TS | N | |

规则：写入型派发（`authorization_mode ≠ READ_ONLY` 或 AUTONOMOUS 或验证/合并）在**同一事务**内插入；唯一冲突 → 130415。RUNNING → 任一非 RUNNING 状态时删除；看门狗清理持有者已非 RUNNING 的残留行。

### 3.10 `execution_artifacts`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id, execution_id | ID | N | |
| kind | ENUM `ArtifactKind` | N | COMMIT / BRANCH / PATCH / FILE / DOCUMENT / BUILD / CONTINUATION / MERGE |
| name | STR(255) | N | |
| ref | STR(512) | N | commit sha、分支名、相对路径或 URL；UNIQUE(`execution_id`,`kind`,`ref`) |
| file_node_id / file_binding_id | ID | Y | 存于 files 时 |
| content_hash | HASH | Y | 服务端计算 |
| size_bytes | BIGINT | Y | |
| metadata | JSON | N | §3.16 S5（diffstat、`changed_files[]` ≤20000 条） |

### 3.11 `execution_evidence_items`（证据，永不删除）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id, execution_id | ID | N | | |
| work_item_id | ID | Y | | 关联后填 |
| claim_id | ID | Y | | 附到 Claim 后不可再改 |
| evidence_type | ENUM `EvidenceType` | N | | |
| state | ENUM | N | `DRAFT` | DRAFT / ATTACHED / WITHDRAWN |
| title | STR(200) | N | | |
| producer_kind / producer_id / producer_authority | ENUM / ID / STR(96) | N/Y/N | | 证据生产者三元组（AGENT / HUMAN / MACHINE / SYSTEM） |
| producer_machine_id | ID | Y | | |
| producer_tool | STR(128) | Y | | 例 `git 2.47.1`、`go test (go1.25.1)` |
| auto_collected | BOOL | N | false | |
| collection_id | ID | Y | | |
| produced_at | TS | N | | 源头时间（commit 时间、报告生成时间、观察时间） |
| content_hash | HASH | N | | 服务端计算（§5.8）；UNIQUE(`execution_id`,`evidence_type`,`content_hash`) 去重 |
| uri | STR(2048) | Y | | PR 链接等 |
| file_node_id / file_binding_id | ID | Y | | 经 files ResourceBinding（purpose=`execution_evidence`，pinned） |
| size_bytes / mime_type | BIGINT / STR(128) | Y | | |
| summary | JSON | N | | 类型专属（§3.16 S3） |
| scope_text | STR(2000) | Y | | 适用范围（如"仅 /api/orders 路径"） |
| verification_id / verifier_version_id | ID | Y | | 由验证器产出的证据 |
| result | ENUM `VerificationResult` | Y | | 验证器产出时填 |
| limitations | STR(2000) | N | | 默认按类型模板（§5.8），可编辑追加 |
| redaction_count | INT | N | 0 | 脱敏次数 |
| attached_at / withdrawn_at | TS | Y | | |

索引：`(tenant_id, claim_id)`；`(tenant_id, execution_id, state)`；`(execution_id, evidence_type, content_hash)` UNIQUE。

### 3.12 `execution_evidence_collections`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id, execution_id | ID | N | | |
| client_request_id | STR(64) | N | | UNIQUE(`execution_id`,`client_request_id`) |
| state | ENUM | N | `PENDING` | PENDING / RUNNING / SUCCEEDED / PARTIAL / FAILED / TIMED_OUT |
| include | JSON | N | | `["COMMITS","TEST_REPORTS","LOG"]` 子集 + `report_globs[]` |
| base_commit / head_commit | STR(64) | Y | | |
| item_count | INT | N | 0 | |
| error | STR(2000) | Y | | |
| requested_kind / requested_by | ENUM / ID | N/Y | | |
| started_at / finished_at / deadline_at | TS | Y | | `deadline_at = created_at + 120 s` |

### 3.13 `execution_context_items`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id, execution_id | ID | N | |
| layer | INT | N | 1–7（§5.5） |
| item_kind | STR(32) | N | AGENT_PROMPT / TEAM_RULE / WORK_ITEM / CRITERIA / PARENT_SUMMARY / CONTINUATION / FEEDBACK / KNOWLEDGE / SKILL / RUNTIME_GUIDE |
| source_kind | ENUM `KnowledgeSourceKind` | Y | |
| source_id / chunk_id | ID | Y | 出处 |
| title | STR(200) | N | |
| citation | STR(512) | N | 例 `[K3] 决策 #812 · 拒绝反馈：分页参数` |
| score_ppm | INT | Y | 检索得分×10⁶ |
| tokens | INT | N | 估算 token |
| inclusion | ENUM | N | FULL / REFERENCE / DROPPED |
| content_hash | HASH | N | |

索引：`(execution_id, layer)`。

### 3.14 验证器：`execution_verifiers`（团队级聚合根）与 `execution_verifier_versions`（不可变）

`execution_verifiers`：

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| key | STR(64) | N | | `^[a-z][a-z0-9-]{1,63}$`；UNIQUE(`tenant_id`,`key`) |
| name | STR(200) | N | | |
| layer | ENUM `VerificationLayer` | N | | 创建后不可改 |
| state | ENUM | N | `ACTIVE` | ACTIVE / ARCHIVED |
| current_version_no | INT | N | 1 | |
| applies_to | JSON | N | | §3.16 S6 |
| required | BOOL | N | false | 匹配时是否必需 |

`execution_verifier_versions`：`verifier_id ID N`、`version_no INT N`（UNIQUE(`verifier_id`,`version_no`)）、`spec JSON N`（§3.16 S7）、`timeout_ms BIGINT N`（1000–3600000，默认 600000）、`change_note STR(2000) Y`。只插入不更新。

### 3.15 `execution_verifications`（`version` 乐观锁，仅 HUMAN 层人工写结果时使用）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id, work_item_id, claim_id, execution_id | ID | N | | 被验证的声明与执行 |
| verifier_id / verifier_version_id / verifier_version_no | ID / ID / INT | N | | |
| layer | ENUM `VerificationLayer` | N | | 冗余快照 |
| required / advisory | BOOL | N | | `advisory = (layer = AI_ASSISTED)` |
| attempt | INT | N | 1 | UNIQUE(`claim_id`,`verifier_id`,`attempt`) |
| result | ENUM `VerificationResult` | N | `PENDING` | |
| run_state | ENUM | N | `QUEUED` | QUEUED / DISPATCHED / RUNNING / DONE |
| inputs | JSON | N | | `{commit, evidence_ids[]}` |
| output | JSON | Y | | `evidence_json`（§3.16 S8） |
| result_reason | STR(32) | Y | | TIMEOUT / COMMAND_NOT_RUNNABLE / REPORT_UNPARSEABLE / MACHINE_UNAVAILABLE / HUMAN_TIMEOUT / SELF_REVIEW / NO_REQUIRED_VERIFIER / CLAIM_SUPERSEDED |
| tool_version | STR(128) | Y | | 机器回报的工具版本 |
| machine_id | ID | Y | | |
| verifier_execution_id | ID | Y | | AI_ASSISTED 派发出的执行 |
| verifier_kind / verifier_actor_id / verifier_authority | ENUM / ID / STR(96) | Y | | HUMAN 层记录人 |
| note | TEXT64K | Y | | 人工观察 / AI 发现摘要 |
| queued_at / started_at / finished_at / deadline_at | TS | Y | | |

索引：`(tenant_id, claim_id)`；`(result, run_state, queued_at)`（调度）；`(tenant_id, project_id, result)`。

### 3.16 JSON Schema（摘要，实现时以 JSON Schema draft 2020-12 写入 `schema/` 并做单测）

| # | 列 | Schema |
|---|---|---|
| S1 | `turns.settings_snapshot` | `{machine_id:string, runtime, model?:string≤160, effort?:string≤32, authorization_mode, tools_enabled:bool, workdir_path:string, native_session_id?:string, carryover:bool}`，`additionalProperties:false` |
| S2 | `messages.attachments` | `[{name:string≤255, bytes:int≥0, sha256:hash}]`，≤8 项，`sum(bytes)≤49152` |
| S3 | `evidence.summary` | 按 `evidence_type` oneOf：COMMIT `{sha, parents[], author_name, authored_at, committed_at, title≤200, files_changed, insertions, deletions, signature:"NONE"\|"GOOD"\|"BAD"\|"UNKNOWN"}`；PULL_REQUEST `{provider, url, number?, head_sha?, ci_state?}`；TEST_REPORT `{format:"JUNIT_XML"\|"GO_TEST_JSON"\|"TAP", passed, failed, skipped, errored, duration_ms, coverage_pct?:0–100}`；LOG `{bytes, lines, truncated:bool, tail_bytes}`；SCREENSHOT `{width, height, criterion_ids?[]}`；OBSERVATION `{text≤65536, observed_at, environment?≤200}`；ARTIFACT `{artifact_id, path}` |
| S4 | `evidence_collections.include` | `{kinds:["COMMITS"\|"TEST_REPORTS"\|"LOG"], report_globs?: string[]≤10}` |
| S5 | `artifacts.metadata` | `{diffstat?:{files,insertions,deletions}, changed_files?:[{path≤1024, blob_sha:string(40\|64)\|null}]≤20000, truncated?:bool}`；`blob_sha=null` 表示删除 |
| S6 | `verifiers.applies_to` | `{work_item_types: WorkItemType[] (空=全部), risk_levels: RiskLevel[] (空=全部), project_ids: string[]\|null}` |
| S7 | `verifier_versions.spec` | AUTOMATED `{argv:string[1..32]（每项≤1024）, workdir?:相对路径≤255, env?:{[^A-Z_][A-Z0-9_]{0,63}: string≤1024}≤20, report_globs?:string[]≤10, report_format:"JUNIT_XML"\|"GO_TEST_JSON"\|"TAP"\|"NONE", stdout_tail_bytes:1024–65536（默认 8192）, tool_version_argv?:string[]}`；AI_ASSISTED `{agent_id, prompt_template≤16384, max_cost_micros≤50000000}`；HUMAN `{instructions≤4000, eligible_role_keys:string[]≥1}` |
| S8 | `verifications.output`（evidence_json） | `{exitCode:int\|null, signal?:string, durationMs:int, stdoutTail:string≤65536, stderrTail:string≤65536, files:[{path, evidence_id, sha256, bytes}]≤10, report?:S3.TEST_REPORT, findings?:[{severity:"INFO"\|"WARN"\|"BLOCKER", text≤2000, criterion_id?}]≤50}` |
| S9 | `commands.payload` / 事件 `payload` | 见 §6.3 每条消息 |

### 3.17 模块内枚举（00 v1.3 §3：仅本模块使用，不进 00 §5）

| 枚举 | 值 |
|---|---|
| `ExecutionPurpose` | DELIVERY, VERIFICATION, HANDOVER |
| `AuthorizationMode` | READ_ONLY, ON_REQUEST（逐项 TOOL_PERMISSION；MVP 仅 CLAUDE_CODE）, FULL_AUTO |
| `TurnState` | QUEUED, RUNNING, WAITING, COMPLETED, FAILED, CANCELLED, INTERRUPTED（Preview 01 会话状态） |
| `InteractionResponseAction` | ACCEPT, DECLINE, CANCEL |
| `ExecutionEndReason` | COMPLETED, CLAIM_SUBMITTED, CLAIM_INVALID, SESSION_ENDED, RUNTIME_ERROR, TIMEOUT, OUTPUT_LIMIT, COST_CAP, CONTEXT_EXHAUSTED, AGENT_GAVE_UP, TURN_IDLE, USER_PAUSE, DECISION_PAUSE, BUDGET_PAUSE, LEASE_EXPIRED, MACHINE_OFFLINE, SERVER_RESTART, HEARTBEAT_LOST, DISPATCH_UNCONFIRMED, DISPATCH_TIMEOUT, SPAWN_FAILED, RUNTIME_UNAVAILABLE, WORKDIR_UNAVAILABLE, BRIDGE_PROTOCOL_ERROR, USER_CANCEL, WORK_ITEM_CANCELLED, AGENT_STOPPED, AGENT_CRASHED, MACHINE_REVOKED, RULES_UNAVAILABLE, SUPERSEDED, SUPERSEDED_BY_RETRY, ENSEMBLE_STOPPED, MARKED_FAILED |
| `ArtifactKind` | COMMIT, BRANCH, PATCH, FILE, DOCUMENT, BUILD, CONTINUATION, MERGE |
| `EvidenceState` | DRAFT, ATTACHED, WITHDRAWN |
| `CollectionState` | PENDING, RUNNING, SUCCEEDED, PARTIAL, FAILED, TIMED_OUT |
| `VerificationRunState` | QUEUED, DISPATCHED, RUNNING, DONE |
| `MergeState` | PENDING, MERGED, CONFLICT, FAILED |

### 3.18 `execution_merges`

`execution_merges`：`project_id, execution_id, work_item_id ID N`；`attempt INT N`（UNIQUE(`execution_id`,`attempt`)）；`target_branch STR(255) N`；`source_commit STR(64) N`；`merge_commit STR(64) Y`；`state ENUM MergeState N DEFAULT PENDING`；`push BOOL N DEFAULT false`；`pushed BOOL N DEFAULT false`；`conflict_files JSON Y`（≤500 路径）；`requested_kind/requested_by/authority_source`；`reason STR(2000) N`；`deadline_at`（+10 min）/`finished_at TS`。
幂等不建模块表：统一使用 09 C7 平台幂等表（00 v1.3 §9）；同键不同请求 → 422 `176425`，同键进行中 → 409 `176409`。

---

## 4. 状态机

所有迁移在**单一数据库事务**内完成：条件更新（`version` 或 `state` CAS）→ 追加审计（如需）→ 写 outbox 领域事件 → `change_seq+1`。事件与审计在提交成功后才对外可见（09 outbox）。

### 4.1 Execution（`ExecutionState`）

```
           accepted            turn_ended / exit 2,3 / pause / cost cap
 CREATED ───────────▶ RUNNING ─────────────────────────────────▶ PAUSED
   │  │                 │ ▲ │                                     │  │
   │  │ rejected/timeout│ │ └── new turn / resumption (epoch+1) ──┘  │
   │  ▼                 │ │                                          │ claim / session end
   │ FAILED ◀── exit 1 ─┤ └─ resumption(ack) ── INTERRUPTED ◀─ lease / offline / restart
   │                    └── exit 0 + claim ──▶ SUCCEEDED
   └── cancel ─────────▶ CANCELLED ◀── termination（任一非终态）
```

终态：SUCCEEDED、FAILED、CANCELLED。INTERRUPTED 为"待处置"：**只能由人显式恢复或标记失败**，系统永不自动重放（Preview 01；PRD §16）。

| # | 当前 | 触发（命令/事件） | 守卫 | 目标（reason） | 副作用 |
|---|---|---|---|---|---|
| E00 | CREATED（`dispatch_epoch=0`） | `autostart=true` 创建完成 / 人工 `POST .../resumptions`（`autostart=false` 时）/ INTERACTIVE 首轮 | BR-03-20 | 保持 CREATED，`dispatch_epoch=1`，写 START 命令 | |
| E01 | CREATED | 机器 `execution.accepted`（epoch 一致） | 命令未 SUPERSEDED | RUNNING | `started_at`（首次）、`accepted_at`、`lease_expires_at=now+30s`；同事务调 02 `ExecutionStarted(tx)`；首次发 `execution.started`；`execution.state_changed` |
| E02 | CREATED | 机器 `execution.rejected` | — | FAILED（SPAWN_FAILED / RUNTIME_UNAVAILABLE / WORKDIR_UNAVAILABLE） | 释放锁与执行槽；BR-03-14 上报 `ExecutionFailed` |
| E02b | CREATED | 组包时 06 已批准 RULE 记忆无法读取（BR-03-30a） | — | FAILED（RULES_UNAVAILABLE），不写 START 命令 | 释放锁与执行槽；`ExecutionFailed{kind:INFRA}`（运行时未开始）；通知 N3 |
| E03 | CREATED | 命令 FETCHED 后 60 s 无 accepted/rejected | — | INTERRUPTED（DISPATCH_UNCONFIRMED） | 栅栏 epoch；通知 N2 |
| E04 | CREATED | 命令 PENDING 超 60 min 未被取走 | — | FAILED（DISPATCH_TIMEOUT） | 命令 → EXPIRED；`ExecutionFailed{INFRA}`（02 可自动重试） |
| E05 | CREATED | `PUT .../termination {outcome:CANCELLED}` | BR-03-40 | CANCELLED（USER_CANCEL） | 命令 → SUPERSEDED |
| E06 | RUNNING | `exited{code:0}` + 合法 `claim.json`（AUTONOMOUS） | — | SUCCEEDED（CLAIM_SUBMITTED / COMPLETED） | 同一事务：先落随事件到达的证据 → `EvidenceBinder.ValidateClaimable(tx, execution_id, fromExit=true)`（此时执行仍为 RUNNING，允许）→ 调 02 `ClaimSubmitter.SubmitOn（`from_exit=true`）(tx, …, head_commit)`；成功 CLAIM_SUBMITTED，失败 COMPLETED + `state_detail` + 通知 N3；释放执行槽（04 `ReleaseSlot`）与写锁。**ENSEMBLE 分支例外**：不调 `ClaimSubmitter`，写 `pending_claim`、`state_reason=CLAIM_SUBMITTED`，按 BR-03-60 直接排队验证（BR-03-08） |
| E07 | RUNNING | `exited{code:0}`，`claim.json` 缺失或不合 Schema | — | FAILED（CLAIM_INVALID） | |
| E08 | RUNNING | `exited{code:1 或 ∉{0,2,3}}` / `error` 事件 | — | FAILED（RUNTIME_ERROR / BRIDGE_PROTOCOL_ERROR） | 通知 N3 |
| E09 | RUNNING | `exited{code:2}` | — | PAUSED（CONTEXT_EXHAUSTED） | `continue.md` 存 CONTINUATION 产物，恢复时注入第 3 层 |
| E10 | RUNNING | `exited{code:3}` | — | PAUSED（AGENT_GAVE_UP） | 同上；通知 N4 |
| E11 | RUNNING | （INTERACTIVE/ADHOC）`turn_ended` | — | PAUSED（TURN_IDLE） | 轮次终态；释放 SHARED_DIR 锁；未决交互 → CANCELLED |
| E12 | RUNNING | `now > accepted_at + timeout_ms` | — | 下发 CANCEL；终止事件或 30 s 强制 → FAILED（TIMEOUT）；INTERACTIVE：轮次 FAILED、执行 PAUSED | |
| E13 | RUNNING | `output_bytes` 将超 8 MiB | — | 拒收该批并下发 CANCEL → AUTONOMOUS：FAILED（OUTPUT_LIMIT）；INTERACTIVE：轮次 FAILED、执行 PAUSED | |
| E14 | RUNNING | `cost_micros ≥ max_cost_micros` | — | 下发 CANCEL → PAUSED（COST_CAP） | 通知 N5 |
| E15 | RUNNING | `PUT .../suspension` / 02 `ExecutionLauncher.Pause`（PAUSE 裁决）/ 07 `budget.threshold_reached{threshold_pct≥100}` | 命令需权限 | 下发 CANCEL（保留 `native_session_id`）→ PAUSED（USER_PAUSE / DECISION_PAUSE / BUDGET_PAUSE） | 30 s 未确认则强制并栅栏 |
| E16 | RUNNING | 看门狗：`lease_expires_at < now` | — | INTERRUPTED（LEASE_EXPIRED） | 栅栏（该 epoch 后续事件 409）；交互 → CANCELLED；锁释放；追加 SYSTEM 消息"执行已中断，请先检查工具副作用再继续"；通知 N2 |
| E17 | RUNNING / PAUSED（有未完成派发） | 04 `machine.state_changed{OFFLINE\|REVOKED}` | — | INTERRUPTED（OFFLINE → MACHINE_OFFLINE；REVOKED → MACHINE_REVOKED） | 同 E16 |
| E18 | RUNNING | 进程启动栅栏（BR-03-12） | — | INTERRUPTED（SERVER_RESTART） | 同 E16 |
| E19 | RUNNING | 机器 `execution.lost`（daemon 重启无法重接 / 适配器心跳 45 s 丢失 / outbox 满 / outbox 缺口无法补发） | epoch 一致 | INTERRUPTED（HEARTBEAT_LOST） | 同 E16 |
| E19b | RUNNING / PAUSED | 05 `agent.state_changed{to:CRASHED}`（AUTONOMOUS，本 Agent 的执行） | — | INTERRUPTED（AGENT_CRASHED） | 同 E16；**不自动重放**：05 的自动重启只恢复 Agent 进程，不续跑被中断执行；BR-03-14 上报 `kind=RUNTIME` |
| E20 | RUNNING | `PUT .../termination {CANCELLED}` | BR-03-40 | 下发 CANCEL → CANCELLED（USER_CANCEL）（30 s 强制） | |
| E21 | PAUSED | 新轮次 `POST .../turns` / `POST .../resumptions` | BR-03-20 | 保持 PAUSED，`dispatch_epoch+1`，写 START 命令；accepted → RUNNING | 新轮次 QUEUED |
| E22 | PAUSED | 02 `claim.submitted`（本执行） | 已关联工作项 | SUCCEEDED（CLAIM_SUBMITTED） | 排队验证（§5.9）；相似度由 02 在声明事务内经 `ChangeSimilarity` 读取（§5.10） |
| E23 | PAUSED | `termination {outcome:SUCCEEDED}` | 最后一轮 COMPLETED | SUCCEEDED（SESSION_ENDED） | |
| E24 | PAUSED / INTERRUPTED | `termination {outcome:FAILED}` | BR-03-40 | FAILED（MARKED_FAILED） | |
| E25 | PAUSED / INTERRUPTED | `termination {outcome:CANCELLED}` | BR-03-40 | CANCELLED（USER_CANCEL） | |
| E26 | INTERRUPTED | `POST .../resumptions {acknowledge_side_effects:true}`，或 INTERACTIVE 新轮次带同字段 | BR-03-20 | 同 E21（同机同目录续接原生会话，否则 carryover） | 审计 `execution.resumed_after_interrupt` |
| E27 | 任一非终态 | 02 经 `ExecutionLauncher.CancelExecution`（工作项取消 / 改派 / 05 ENSEMBLE 落选或超时）/ 05 `agent.state_changed{STOPPED,RETIRING,RETIRED}`（AUTONOMOUS） | `purpose=HANDOVER` 的执行豁免 05 触发（退役期间仍须完成交接文档）；已因 MACHINE_OFFLINE / MACHINE_REVOKED / AGENT_CRASHED 处于 INTERRUPTED 的执行不因 `agent.state_changed` 改变（与事件到达顺序无关） | CANCELLED（WORK_ITEM_CANCELLED / SUPERSEDED / AGENT_STOPPED / ENSEMBLE_STOPPED） | RUNNING 时先下发 CANCEL |

所有 DELIVERY 执行进入 FAILED / INTERRUPTED 的迁移都在同一事务内调用 02 `ExecutionFailed(tx, ref, kind)`（kind 规则 BR-03-14）；从 INTERRUPTED/PAUSED 回到 RUNNING（E26、E21）时再次调用 `ExecutionStarted(tx)`：02 T20b 对工作项 `current_execution_id` 的人工恢复把 EXECUTION_FAILED → IN_PROGRESS，且不计入 `infra_retry_count`。执行进入终态、INTERRUPTED 或 PAUSED 的同一事务内释放执行槽（04 `ReleaseSlot`）与写锁（BR-03-74）。E15 的 DECISION_PAUSE 与 E21 的恢复由 02 经 `ExecutionLauncher.Pause/Resume` 在其裁决事务内同步触发（不经事件）。

**非法迁移**（服务层迁移表 = 上表补集，统一校验）：

| 当前 | 命令 | 错误码 |
|---|---|---|
| SUCCEEDED / FAILED / CANCELLED | 任何变更（turns、resumptions、suspension、termination、PATCH、work-item-link、新增证据） | 409 130411 `execution.error.terminal` |
| RUNNING | 新轮次 | 409 130412 `execution.error.turn_active` |
| RUNNING；CREATED 且 `dispatch_epoch>0` | resumptions | 409 130410 `execution.error.illegal_transition` |
| CREATED / INTERRUPTED | suspension | 409 130410（PAUSED 再 suspension → 幂等 200） |
| INTERRUPTED | 新轮次 / resumptions 未带 `acknowledge_side_effects:true` | 422 130450 `execution.error.side_effect_ack_required` |
| CREATED / RUNNING / INTERRUPTED | `termination {SUCCEEDED}` | 409 130410 |
| RUNNING | 绑定声明证据 / 换机器 / 切工具开关 | 409 130447 / 130424 |
| kind=ADHOC | 绑定声明证据 | 409 130425 `execution.error.adhoc_not_linked` |
| 机器回传 epoch ≠ 当前，或状态不接受事件 | 任何执行级消息 | 409 130427 `execution.error.fenced` |

### 4.2 Turn（`TurnState`，Preview 01）

| 当前 | 触发 | 目标 | 执行映射 |
|---|---|---|---|
| — | `POST .../turns` | QUEUED | 执行保持原态，UI"排队中" |
| QUEUED | accepted | RUNNING | 执行 → RUNNING |
| QUEUED | 取消（命令未被取走） | CANCELLED | 执行回原态 |
| QUEUED | rejected / E03 / E04 | FAILED / INTERRUPTED | 同 E02–E04 |
| RUNNING | `interaction_requested` | WAITING | 执行 RUNNING +"待你决定" |
| WAITING | 全部交互已送达或关闭 | RUNNING | |
| RUNNING / WAITING | `turn_ended{completed}` | COMPLETED | 执行 → PAUSED（TURN_IDLE） |
| RUNNING / WAITING | `error` / 超时 / 超限 | FAILED | 执行 → PAUSED |
| RUNNING / WAITING | `turn_ended{cancelled}` | CANCELLED | 执行 → PAUSED |
| RUNNING / WAITING | E16–E19 | INTERRUPTED | 执行 → INTERRUPTED |

非法：对终态轮次取消 → 幂等 200 返回当前态；取消非当前轮次 → 409 130410。

### 4.3 Interaction（`InteractionState`）

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | 事件 `interaction_requested` | 每派发 ≤100；`native_request_id` 未重复 | PENDING | `expires_at=+30 min`；`pending_interaction_count+1`；`interaction.requested`；通知 N1 |
| PENDING | `PUT /interactions/{id}/decision` | BR-03-31；CAS `state='PENDING'` | DECIDED | 写 INTERACTION_RESPONSE 命令；审计 `execution.interaction_responded`；`interaction.decided` |
| PENDING | 到期（看门狗 15 s） | — | EXPIRED | 写 INTERACTION_RESPONSE `{action:CANCEL}`（运行时视为拒绝）；`interaction.closed` |
| PENDING | 轮次结束 / 取消 / 中断 / 栅栏 | — | CANCELLED | `interaction.closed` |
| DECIDED | 事件 `interaction_delivered` | 同 epoch | DELIVERED | 轮次 WAITING→RUNNING（若无其他未决） |
| DECIDED | 派发结束而未送达 | — | CANCELLED（DELIVERY_FAILED） | UI"未送达（执行已结束）"；**禁止**套用到重试/新派发 |

非法：对 DECIDED 再提交**相同** `{action, content}`（规范化 JSON 相等）→ 200 原结果；不同 → 409 130421；对 EXPIRED / CANCELLED / DELIVERED → 409 130420。

### 4.4 Verification（`VerificationResult` × `run_state`）

| 当前 | 触发 | 目标 | 说明 |
|---|---|---|---|
| — | `claim.submitted` 选出验证器 / 手动重跑 | PENDING·QUEUED | |
| PENDING·QUEUED | 调度器写 VERIFICATION_START（AUTOMATED）/ 创建 VERIFICATION 执行（AI） | PENDING·DISPATCHED | HUMAN 层停在 QUEUED 等人 |
| PENDING·DISPATCHED | 机器 `verification.started` | PENDING·RUNNING | |
| PENDING·RUNNING | `verification.result` | PASSED / FAILED / INCONCLUSIVE ·DONE | 映射 BR-03-62 |
| PENDING·* | 超 `deadline_at`（排队 30 min；运行 = spec.timeout_ms + 60 s；HUMAN 72 h） | INCONCLUSIVE（MACHINE_UNAVAILABLE / TIMEOUT / HUMAN_TIMEOUT） | |
| PENDING·RUNNING | 机器离线（租约 30 s） | 首次重新 QUEUED；第二次 INCONCLUSIVE（MACHINE_UNAVAILABLE） | 验证视为无副作用，允许一次自动重排（**本规格提议**） |
| PENDING（HUMAN） | `PUT /verifications/{id}/result` | PASSED / FAILED / INCONCLUSIVE | BR-03-65 |
| PENDING·* | Claim 被取代 / 工作项取消 | INCONCLUSIVE（CLAIM_SUPERSEDED） | |

非法：对非 PENDING 写结果 → 409 130410；对非 HUMAN 层人工写结果 → 409 130434；结果不可改（重跑 = 新行 attempt+1）。

### 4.5 证据、采集与合并

| 对象 | 迁移 | 非法 → 错误码 |
|---|---|---|
| Evidence | DRAFT →（02 Claim 绑定）ATTACHED；DRAFT →（执行者排除）WITHDRAWN；WITHDRAWN → DRAFT（仅声明前） | 修改/撤回 ATTACHED → 409 130430 |
| Collection | PENDING → RUNNING → SUCCEEDED / PARTIAL / FAILED；非终态超 120 s → TIMED_OUT | 同执行已有进行中采集时再发起 → 返回该条（幂等 200） |
| Merge | PENDING → MERGED / CONFLICT / FAILED；超 10 min → FAILED | 已 MERGED 再请求 → 409 130436 |

---

## 5. 业务规则与边界情况

### 5.1 分支、序号与种类

- **BR-03-01 分支原因约束**：INITIAL：无父，且工作项尚无 DELIVERY 执行；RETRY_INFRA：父必填且为 FAILED / INTERRUPTED / CANCELLED / PAUSED —— 父为 INTERRUPTED 或 PAUSED 时，`Launch` 在同一事务内先把父置 FAILED（SUPERSEDED_BY_RETRY，栅栏其 epoch），再建子执行，避免与 BR-03-07 冲突；QUALITY：父必填，`source_decision_id` 必填且该 Decision 为 REJECT / REQUEST_CHANGES、指向父的 Claim；STRATEGY：父可选（被替代者），**起点为项目基线而非父 HEAD**；ENSEMBLE：无父，`ensemble_group_id` 必填；SCOPE：父必填，`source_change_record_id` 必填。违反 → 422 130440。**调用方约束**：重新派单（租约回收、Agent 崩溃、机器吊销、改派）时，若工作项已有 DELIVERY 执行，调用方（02 T03/T07）必须用 `branch_reason=RETRY_INFRA`、`parent=最近的 DELIVERY 执行`；仅工作项无任何 DELIVERY 执行时用 INITIAL。ADHOC（含 `purpose=HANDOVER`）无分支原因，须给出 `project_id`。
- **BR-03-02 起点**：INITIAL / STRATEGY / ENSEMBLE → `BASELINE`（项目基线分支，04 `ResolveWorkspace`）；QUALITY 按 02 传入的 `workspace_mode`：REJECT 默认 `RESTART`（父的 `base_commit`，重开工作区）、REQUEST_CHANGES 默认 `CONTINUE`（父 `head_commit`）；SCOPE / RETRY_INFRA → `CONTINUE`（父无提交时退化为父 `base_commit`）。父执行及其产物、证据**永不修改**（PRD I5）。
- **BR-03-03 序号**：创建事务内 `seq_no = max(seq_no)+1`（同工作项）、`reason_ordinal = count(同 reason)+1`；唯一冲突时整事务重试 ≤3 次，仍失败 → 409 130409。标签 `#{seq_no} · {原因}{ordinal}`；INITIAL 不带序数（"#1 · 初始"）；原因中文：初始 / 故障重试 / 质量返工 / 策略替代 / 并行求解 / 范围变更。
- **BR-03-04 ADHOC 关联**（PRD Q8）：`PUT /executions/{id}/work-item-link` 守卫：调用者是该执行发起者且为工作项当前 EXECUTOR 分派者；工作项 ∈ {CLAIMED, IN_PROGRESS, REWORK}；关联后 `kind` 改为 INTERACTIVE，`branch_reason` = 工作项无 DELIVERY 执行时 INITIAL，否则 STRATEGY；分配 `seq_no`；审计 `execution.linked`。已关联再关联 → 409 130426。
- **BR-03-05 ENSEMBLE**：同组 ≤3（PRD §10.6；项目可在 07 调为 1–5）；超出 → 409 130439。发起者：人须持 `work_item_assign` + `execution_start`；项目主导 Agent 不经角色码，经 08 `AuthorityChecker`（`POLICY:ensemble_branch`）判定。组内各分支可用不同 Agent / runtime / 模型；各分支对应 02 的 ENSEMBLE 分派行（02 `ensemble_plan_id`），工作项 `current_execution_id` 在选中前保持 NULL。
- **BR-03-06 返工建枝**（与 02 BR-02-26、§5.7 对齐）：由 02 在**裁决事务内**同步调用 `ExecutionLauncher.Launch(tx, …)`（委派模式：本模块在调用方事务里写自己的行），本模块**不**再依据 `decision.made` 事件建枝，避免双建。映射：REJECT/REQUEST_CHANGES（QUALITY_REJECTION）→ QUALITY；REQUEST_CHANGES（STRATEGY_CHANGE）→ STRATEGY；REQUEST_CHANGES（SCOPE_CHANGE）→ SCOPE；执行故障 → RETRY_INFRA（02 决定，见 BR-03-14）。执行者为 Agent 且 `autostart=true` → 立即派发 AUTONOMOUS；`autostart=false` → AUTONOMOUS 停在 CREATED 等人启动（A13）；执行者为人 → 建 INTERACTIVE（CREATED）等其打开会话。同一 `source_decision_id` 重复调用返回已建执行（UNIQUE 约束），不报错。
- **BR-03-07 同工作项并发**：同一工作项同时最多 1 个非终态 DELIVERY 执行（ENSEMBLE 组内除外，受 BR-03-05 约束）；违反 → 409 130426。改派时 02 T07 先对旧执行调 `CancelExecution`（或 BR-03-01 的 RETRY_INFRA 取代），再 `Launch`。
- **BR-03-08 ENSEMBLE 声明与收尾**：分支退出码 0 → 写 `pending_claim`（E06 例外），证据照常 DRAFT，验证按 BR-03-60 直接排队，**不**回调 02（`VerificationQueued/Completed` 仅在选中后对正式 Claim 执行）。05 选中某分支后在其事务内调 02 `ClaimSubmitter.SubmitOn(tx, {…pending_claim, execution_id, authority_source:POLICY:<plan_id>})`，本模块随之把该分支已完成的验证结果关联到新 Claim（不重跑）；落选或截止时间到 → 05 调 `CancelExecution(tx, execution_id, ENSEMBLE_STOPPED)`，已 SUCCEEDED 的落选分支只置 `archived_at`，证据与产物保留。全部分支失败 → 05 调 02 `WorkItemTransitioner.EnsembleFailed`。
- **BR-03-09 发起者**：经 `ExecutionLauncher.Launch` 且 `autostart=true` 建立的 DELIVERY 执行，`initiator = SYSTEM`、`authority_source` = 触发它的分派裁决（`DECISION:<decision_id>` 或 `POLICY:<proposal_id>`）；确认派单的人**不是**发起者（避免 02 T04 守卫与职责分离误判）。人在 A06 / A13 显式启动时发起者为该人。

### 5.2 生命周期、租约与栅栏（移植 Preview 01 `server.go expire()`、`daemon.go`、`runtime-modes §7.4`）

- **BR-03-10 租约**：执行租约 30 s；机器每 ≤10 s 经 `leases.renew` 续约（被接受的事件批次也续约）。看门狗每 5 s 扫描 `state='RUNNING' AND lease_expires_at < now`。最坏检测时延 35 s。DELIVERY 执行续约时，同一事务内调用 02 `WorkItemTransitioner.RenewLease(tx, work_item_id, assignee_principal_id)` 续工作项分派租约，每执行至多每 60 s 一次。
- **BR-03-11 机器自停**：机器连续 20 s 无法续约 → 对执行桥发 cancel，4 s 后杀进程树，保留 outbox。服务端 30 s 才判中断，留 10 s 余量，保证"判中断时进程已停"。
- **BR-03-12 进程启动栅栏**：配置 `execution.restart_fencing`：`all`（默认，单实例）→ `Start` 时持 dlock `execution:startup-fence` 把全部 RUNNING 标为 INTERRUPTED（SERVER_RESTART），与 Preview 一致；`lease`（多副本）→ 不做全量栅栏，依赖 30 s 看门狗。**本规格提议**：多副本时单副本重启不应中断其他副本上的健康派发。
- **BR-03-13 栅栏**：机器每条执行级消息带 `(execution_id, dispatch_epoch)` 与 04 连接代次；不一致或状态不接受 → 409 130427；机器收到后立即停止该派发并丢弃其 outbox 余量（Preview："迟到输出属于被栅栏的尝试，不得污染下一轮"）。栅栏不能撤销已发生的外部副作用 —— UI 必须提示"先检查副作用"。
- **BR-03-14 故障分类与上报**：DELIVERY 执行进入 FAILED 或 INTERRUPTED 时，在同一事务内调用 02 `WorkItemTransitioner.ExecutionFailed(tx, ExecutionRef, kind)`：`kind=INFRA` **仅当** reason ∈ {SPAWN_FAILED, RUNTIME_UNAVAILABLE, WORKDIR_UNAVAILABLE, DISPATCH_TIMEOUT}（可确定运行时未开始工作）且 `state_detail ≠ "env_leak"`（BR-03-71，配置错误重试无益）；RULES_UNAVAILABLE 亦为 INFRA（服务端组包失败，未派发）；其余一律 `kind=RUNTIME` —— **INTERRUPTED（含 AGENT_CRASHED、MACHINE_REVOKED）、TIMEOUT、HEARTBEAT_LOST 都报 RUNTIME**，从而 02 的自动 RETRY_INFRA（02 T19，退避 30 s / 120 s，上限 07 `infra_retry_limit` 默认 2）只会发生在确定无副作用的失败上，满足"不自动重放"。自动重试由 02 调 `Launch(RETRY_INFRA)` 发起，本模块不自行建枝。`execution.state_changed.data.failure_kind` 同值。
- **BR-03-15 取消宽限**：机器对执行桥发 cancel，4 s 后杀进程树（`daemon.go bridgeJob.stop`）；服务端 30 s 内未见终止事件 → 强制进入目标态并栅栏。

### 5.3 派发与事件流

- **BR-03-20 派发守卫**（新轮次、恢复、启动共用，按序检查）：① 机器 ONLINE 且非 DRAINING → 否则 409 130413；② 装有该 runtime → 否则 422 130414；③ 写入型派发取得 `execution_workdir_locks` → 否则 409 130415；④ 04 `ReserveSlot` 成功 → 否则命令保持 PENDING 排队（≤60 min，E04）；⑤ 07 `ProjectGate.CanStartExecution(tenant, project, kind)` 为 true（项目阶段、预算暂停、项目暂停）→ 否则 409 130438；⑥ INTERACTIVE 限额（BR-03-34）；⑦ 工作项未处于暂停叠加态（02 `paused_at` 为空；人工 RESUME 后由 02 调 `ExecutionLauncher.Resume` 再派发）→ 否则 409 130426。
- **BR-03-21 事件序号**：机器为每个派发维护从 1 起的连续 `seq`，先写本地 outbox 再发送；服务端事务内：`seq ≤ last_seq` → 不处理、返回 `ack_seq=last_seq`（重放幂等）；`seq = last_seq+1` → 应用；`seq > last_seq+1` → 409 130428，响应含 `expected_seq`，机器从该序号重发。批次按序应用，遇缺口停止并返回已确认水位。机器无法补发 `expected_seq`（本地 outbox 已丢失该段）→ 报 `execution.lost{reason:OUTBOX_GAP}` → E19，不得跳号续传。
- **BR-03-22 outbox 确认**：机器只删除 `≤ ack_seq` 的记录，重连后重发未确认记录；上限 10,000 条，达到即停止该执行并报 `execution.lost`（Preview 限额）。
- **BR-03-23 命令下发**：FETCHED 后 10 s 未 ACK 可重新下发，最多 6 次；机器按 `command_id` 去重（重复 START 只回报既有状态，不重复 spawn —— Preview A07）。写入任何新命令的事务提交后调 04 `MachineChannelNotifier.Wake(machine_id)`，唤醒该机器的长轮询（跨实例经 09 实时订阅）。
- **BR-03-24 原生会话一致性**：`session_bound`：若 `native_session_id` 非空且不同于回报值 → 409 130445，本派发失败（Preview "Native session changed unexpectedly"）；首次绑定写入并清 `carryover_pending`。

### 5.4 运行目录、退出语义与心跳（旧 PRD §18、F.8，去品牌化）

- **BR-03-25 运行目录**：`{machine_data_dir}/runs/{execution_id}/`：`context/`（`01_constitution.md`、`02_contract.json`、`03_inputs.md`、`04_feedback.json`、`05_knowledge.md`、`06_skills/`、`07_runtime.md`、`bundle.json` 清单含各文件 SHA-256）、`output/`、`logs/agent.log`、`continue.md`、`claim.json`、`exit_code`。运行目录**不在**工作区内。注入的执行变量只有 `EXECUTION_ID`、`EXECUTION_RUN_DIR`、`EXECUTION_WORKDIR`（按内容命名，不含品牌，CLAUDE.md §4）。
- **BR-03-26 退出码**：0 完成（必须写 `claim.json`：`{summary≤2000, criteria:[{criterion_id, evidence_hints[]}], confidence?:0–1}`）；1 失败；2 上下文不足（写 `continue.md`）；3 主动放弃（写 `continue.md` 说明原因）；其他 → 同 1。映射见 E06–E10。
- **BR-03-27 适配器心跳**：适配器每 15 s 向机器上报；3 个周期（45 s）缺失 → 机器杀进程并报 `execution.lost{reason:HEARTBEAT_LOST}`。

### 5.5 上下文包（七层，移植旧 PRD §18.1）

| 层 | 内容 | 来源 | 可裁剪 |
|---|---|---|---|
| 1 角色宪法 | Agent 系统提示 + forbidden actions + 已批准 RULE 记忆（该 Agent 自身 AGENT scope + TEAM scope，06 BR-06-23） | 05；06（`MemoryKind=RULE`） | 否 |
| 2 任务契约 | 工作项标题/描述、需求版本、验收标准逐条（含 `criterion_id`）、`claim.json` 写法、证据期望 | 02 | 否 |
| 3 输入 | 父执行摘要（claim 摘要、diffstat、变更文件清单）、`continue.md`、工作项附件路径 | 本模块、02 | 是（第 1 顺位） |
| 4 结构化反馈 | 祖先链全部 REJECT/REQUEST_CHANGES（含 02 T12 系统裁决）的 `{category, location, expected, detail}` + 失败验证的 `stdoutTail` 前 4 KiB，新在前；每条带 `[Fn]` 引用，写 `execution_context_items(item_kind=FEEDBACK, citation='[Fn]', source=decision_id)` | 02 `DecisionReader`、本模块 | 否 |
| 5 项目知识 | `Retriever.BuildInjection` 返回条目（agent → project → team），每条带 `[Kn]` 引用与出处 | 06 | 是（第 2 顺位） |
| 6 技能 | Agent 已挂载技能的 SKILL.md | 06 `SkillCatalog.ForAgent`（挂载关系由 05 管理） | 是（第 3 顺位） |
| 7 运行环境 | 运行目录、退出语义、心跳、`continue.md` 续跑、证据自动采集说明 | 本模块模板 | 否 |

- **BR-03-28 预算**：预算 = Agent `max_context_tokens`（05，默认 24,000）；关联工作项的 INTERACTIVE 会话仅在首轮把第 2、4、5 层作为前置说明注入（预算 12,000，UI 显示一条可折叠 SYSTEM 消息"已注入上下文"）；ADHOC 不注入。token 估算 = `ceil(UTF-8 字节 / 3)`（**本规格提议**：中文约 1 token/字、英文约 4 字符/token，偏保守，服务端无需 tokenizer）。
- **BR-03-29 裁剪顺序**：超预算时依次把第 3 层（与工作项相关度低、旧的在先）、第 5 层（得分低在先）、第 6 层条目由 FULL 降为 REFERENCE（只保留路径/ID 与一行标题，约 30 token），仍超则按同序 DROPPED；第 1/2/4/7 层永不裁剪。若 1+2+4+7 层本身超预算 → 创建失败 422 130441（系统自动建枝时改为通知 N3，不建执行）。每条的 `inclusion` 写 `execution_context_items`（"使用的知识"数据源）。
- **BR-03-30a 规则可用性**：组包前经 06 读取适用的已批准 RULE；读取失败或超时（5 s）→ 不派发，E02b（RULES_UNAVAILABLE）。规则是第 1 层不可裁剪内容，缺失即视为上下文不完整。
- **BR-03-30b 知识使用记录**：组包事务提交后以执行行上持久化的 `retrieval_log_id` 调 06 `Retriever.RecordInclusion(log_id, items[{chunk_id, inclusion}])`（签名见 06 §6.6）；执行结束（任一终态或 PAUSED）时从 `claim.json.citations[]` / 消息正文中的 `[Kn]` 标记提取实际引用，调 `RecordCitations(log_id, labels[])`（`labels` 为 `[Kn]` 标签，如 `K3`）。`retrieval_log_id` 为空（06 不可用时组包降级）则跳过两次调用并记 `knowledge_unavailable=true`。两次调用失败只记 `slog.Warn` 并由 `execution.knowledge_sync` 任务重试，不影响执行状态。
- **BR-03-30c Agent 记忆提议**：AUTONOMOUS 执行可在 `output/memory.json` 写 `[{kind:"EXPERIENCE"|"RULE", text≤2000, scope:"AGENT"|"PROJECT", evidence_refs?[]}]`（≤20 条）；机器在 `exited` 时随 `memory_file_id` 上传；服务端在 E06–E10 提交后经 06 `MemoryWriter.Propose(agent_id, project_id, execution_id, entries)` 转交（治理与限额由 06 执行）；文件缺失或 Schema 不合法 → 忽略并记 `state_detail` 附注，不影响执行结果。
- **BR-03-30 包存储**：上下文包按 RFC 8785 规范化 JSON 存 files（purpose=`execution_context`，pinned），`context_bundle_hash` 为其 SHA-256；START 命令内联（≤1 MiB，超出则携带下载引用）。

### 5.6 交互会话（Preview 01 全量对齐）

- **BR-03-31 回应权限**：调用者须为 HUMAN（**本规格提议**：工具授权是人的监督动作，Agent 不得批准工具调用）且持项目 `interaction_respond`；INTERACTIVE 发起者可回应自己的会话。审计 `execution.interaction_responded`（detail：interaction_id、kind、action、authority_source、content 的 SHA-256，不含原文）。
- **BR-03-32 超时**：30 min（runtime 给出更早期限取更早）；到期处理见 §4.3；送达失败不得套用到后续派发。
- **BR-03-33 设置修改**：`PATCH /executions/{id}` 必带 `If-Match`（`version`）：`model`、`effort`、`authorization_mode` 可在 RUNNING 时改，**下一轮生效**（当前轮用 `settings_snapshot`）；`machine_id`、`tools_enabled`、`workdir_mode` 仅非 RUNNING 可改（409 130424）；`version` 不符 → 409 130409（"会话设置已在其他客户端修改，请刷新后重试"）。模型 ≤160 字符、无换行；effort 须在 04 模型目录给该模型的集合内，目录缺失用 runtime 通用集（CLAUDE_CODE：low/medium/high/xhigh/max；CODEX：minimal/low/medium/high/xhigh/max/ultra/persistent）；切换模型时 effort 重置为默认；目录刷新不替换已保存的自定义模型。
- **BR-03-34 限额**：每执行轮次 ≤500、消息 ≤4000、消息正文累计 ≤8 MiB（达到后新轮次 409 130416；事件超限按 E13）；单条提示（含附件内容）≤64 KiB（413 130417）；每条消息附件 ≤8 个、合计 ≤48 KiB、UTF-8 文本且不含 `\x00-\x08\x0E-\x1F\x7F`（422 130418）。附件以 `### 文件名` + 自适应长度围栏并入正文（Preview `messageWithFiles`），**不是**写入远程工作目录。
- **BR-03-35 重试**：仅当最后一轮 FAILED 且其后只有 SYSTEM 消息（`has_output=false`）：`POST .../turns {retry_of_turn_id}` 复用原提示，清 `native_session_id`、置 `carryover_pending=true`（新原生会话并带入此前对话，避免在旧原生会话重复提交）；否则 409 130419。`tools_enabled` 或 FULL_AUTO 时前端必须二次确认"上次执行可能已调用工具或修改文件"。
- **BR-03-36 换机器**：仅 CREATED / PAUSED / INTERRUPTED 且 `workdir_mode=SHARED_DIR`；目标 ONLINE 且装有同一 runtime（422 130446）。目标工作目录（04 `ResolveWorkspace`）与当前相同（任一端 Windows 时大小写不敏感）→ 保留 `native_session_id` 尝试续接；不同 → 清空并 `carryover_pending=true`。carryover：只取 USER/ASSISTANT，自新到旧，每条取末尾 2000 字符，总 ≤24 KiB，前缀 `Previous conversation for context in a new native session:`、末尾 `Current user message:`（`daemon.go carryoverPrompt`）。WORKTREE 模式不可换机器 → UI 提供"在新机器上开故障重试分支"（RETRY_INFRA）。
- **BR-03-37 授权模式**：READ_ONLY（Codex `read-only` 沙箱；Claude 不启用写工具）；ON_REQUEST（Claude Code 原生工具，逐项 TOOL_PERMISSION；Codex MVP 不可选 —— Preview："CLI exec 不支持 Web 审批"）；FULL_AUTO（Codex `danger-full-access`；Claude 工具全部预授权）。切到 FULL_AUTO：需 `execution_start`、项目策略 `allow_full_auto`（07，默认 true）、底座 step-up（purpose `execution_full_auto`，新鲜认证 ≤5 min；否则 403 130403 `reason_code=REAUTH_REQUIRED`，前端据此弹出 ReauthenticationDialog）；项目策略为 false → 403 130403 `reason_code=FULL_AUTO_FORBIDDEN`；审计 `execution.full_auto_enabled`（00 §7.2）。AUTONOMOUS 的模式取自 Agent 配置（由 05 审计）。
- **BR-03-38 Skills 与 MCP**：候选来自 04 能力检测（不调用模型、不改聊天历史）；禁用 Skill 不进候选；插入语法 CLAUDE_CODE `/名称`、CODEX `$名称`，插入不自动发送；MCP 列表不代表实时连接状态（文案注明）。
- **BR-03-39 标题**：1–200 字符单行；手动命名后不再自动命名；并发改名按 `version` 冲突。

### 5.7 权限与"自己的"

- **BR-03-40**：`execution_read` 读项目内全部执行；发送轮次仅发起者（他人只读）；停止/暂停/终止：发起者或执行者本人持 `execution_stop` 即可，他人须同时持 `execution_stop` 与 `decision_pause`（交付负责人级）；否则 403 130403，审计 `execution.action_denied`（outcome=denied）。Agent（`agent_executor`）只能停止自己的执行。
- **BR-03-41**：AUTONOMOUS 只经 `ExecutionLauncher`（02/05/本模块系统动作）创建，HTTP 不开放；INTERACTIVE 须调用者为工作项当前 EXECUTOR 分派者并持 `work_item_execute` + `execution_start`；ADHOC 只需项目 `execution_start`。

### 5.8 证据

- **BR-03-50 自动采集**：`POST .../evidence-collections` 或 AUTONOMOUS 退出码 0 时机器自动执行：COMMITS（`base_commit..HEAD` 每提交一条 COMMIT，≤100 条，超出合并为 PATCH 产物并标截断）；TEST_REPORTS（`report_globs`，默认 `**/junit*.xml`、`**/test-results/**/*.xml`、`**/*.tap`；单文件 ≤20 MiB、≤10 个）；LOG（完整 `agent.log` 上传 files，摘要含末尾 64 KiB）。超时 120 s；部分失败 → PARTIAL。
- **BR-03-51 人工证据**：`POST .../evidence` 仅允许 OBSERVATION（正文 ≤64 KiB，必填 `scope_text`）、SCREENSHOT（先经 files 上传，PNG/JPEG/WebP ≤20 MiB）、ARTIFACT（引用本执行产物）、PULL_REQUEST（MVP 仅登记 URL，`limitations` 追加"未经 Git 平台连接器核验"）。COMMIT / TEST_REPORT / LOG 只能自动采集（防伪造），否则 422 130422。
- **BR-03-52 内容哈希**：服务端计算 SHA-256，不采信机器自报；机器附带哈希与服务端计算不符 → 422 130432。文件型 = 文件字节；COMMIT = RFC 8785 规范化 JSON `{sha, tree, parents, author, committer, message}`；OBSERVATION = `{text, observed_at, scope_text, environment}`；PULL_REQUEST = `{url, head_sha?}`。UI 显示前 8 位，可复制全值。
- **BR-03-53 局限默认模板**（可追加不可清空）：COMMIT"证明代码变更已提交且内容一致；不证明变更正确或满足需求"；PULL_REQUEST"证明合并请求存在及登记时状态；不证明评审通过"；TEST_REPORT"仅证明所列测试在该提交与环境下的结果；不证明测试覆盖了验收标准"；LOG"证明执行期间输出了这些内容；可能被截断，不证明结果正确"；SCREENSHOT"仅证明界面在截图时刻的渲染；不证明数据正确或交互可用"；OBSERVATION"人工观察，受观察者、时间与范围限制，未经工具复核"；ARTIFACT"证明产物存在且哈希一致；不证明其内容质量"。
- **BR-03-54 脱敏**：日志、stdout/stderr 尾部、工具详情入库前由机器与服务端各做一次：机器凭证值、`Bearer\s+\S+`、`sk-[A-Za-z0-9_-]{20,}`、`AKIA[0-9A-Z]{16}`、`-----BEGIN [A-Z ]*PRIVATE KEY-----` 块 → `[REDACTED]`，计数写 `redaction_count`。脱敏在哈希计算**之前**（哈希证明入库内容）。
- **BR-03-55 上限**：每 Claim 绑定证据 ≤50（409 130431）；每执行 DRAFT 证据 ≤200；文件型单个 ≤100 MiB（files 底座上限）。
- **BR-03-56 绑定**：02 提交 Claim 时在**其事务内**调用 `EvidenceBinder.Attach(tx, claim_id, work_item_id, execution_id, own_ids, cited_ids)`：`own_ids` 须属该执行且为 DRAFT → 置 ATTACHED、写 `claim_id`（= 首次绑定的声明，作为出处）与 `work_item_id`；`cited_ids` 须为同一工作项**祖先分支**上已 ATTACHED 的证据（02 BR-02 允许引用祖先证据），只读引用、不改状态，引用关系由 02 保存；两者合计 ≤50；违反 → 422 130422 / 409 130431；成功发 `evidence.added`。证据永不删除；擦除义务走底座 principallifecycle 匿名化（替换 producer_id，保留哈希）。

### 5.9 验证器与验证

- **BR-03-60 选择**：`claim.submitted` 时（ENSEMBLE 分支在写入 `pending_claim` 时，BR-03-08）选 ACTIVE 且 `applies_to` 匹配（类型、风险、项目）的验证器，取 `current_version_no`；若必需集为空：类型 ∈ {FEATURE, BUG, TEST} 或风险 = HIGH → 生成合成验证 `INCONCLUSIVE / NO_REQUIRED_VERIFIER`（**本规格提议**：高价值工作项不应静默跳过 L2）；其他 → 不进 VERIFYING，直接请求 02 迁到 NEEDS_HUMAN_REVIEW。
- **BR-03-61 AUTOMATED 执行**：在被验证执行的机器上、临时分离 worktree（`git worktree add --detach {data}/verify/{verification_id} {claim_commit}`）运行后删除；SHARED_DIR 非 git 目录则在原目录运行并取写锁。`argv` 直接执行（不经 shell），环境 = BR-03-70 白名单 + spec.env。排队等机器 ≤30 min。
- **BR-03-62 结果映射**：exit 0 且无报告或报告 failed+errored=0 → PASSED；exit 0 但报告有失败 → FAILED；exit 1–124 → FAILED；超时被杀 → INCONCLUSIVE（TIMEOUT）；126/127 → INCONCLUSIVE（COMMAND_NOT_RUNNABLE）；其他信号 → INCONCLUSIVE；报告存在但解析失败 → INCONCLUSIVE（REPORT_UNPARSEABLE）。`output` 写 evidence_json（S8）；报告文件同时成为 TEST_REPORT 证据（填 `verification_id`、`verifier_version_id`、`result`）。
- **BR-03-63 AI_ASSISTED**：派发 `purpose=VERIFICATION` 的 AUTONOMOUS 执行给 spec.agent_id（须为 REVIEWER 角色且不是被验证执行的执行者，否则 INCONCLUSIVE / SELF_REVIEW），`claim.json` 改为 `verdict.json {result, findings[]}`；`advisory=true`。AI 结果可推进到 NEEDS_HUMAN_REVIEW，但 `EvidenceReader.Summary.has_non_ai_pass=false` 时 02/08 不得据此单独接受（PRD §11.2）。
- **BR-03-64 汇总与回调**（方法集按 02 §6.9）：验证行建好的同一事务内调 02 `VerificationQueued(tx, work_item_id, batch_id=claim_id)`；必需验证全部非 PENDING 时，在写入最后一个结果的同一事务内调 `VerificationCompleted(tx, work_item_id, batch_id, Summary{required_total, passed, failed, inconclusive, automated_passed, has_non_ai_pass})`。HUMAN 层验证排队时发布 `verification.human_requested`（08 我的裁决）。目标状态由 02 决定（预期：全 PASSED / 仅 INCONCLUSIVE → NEEDS_HUMAN_REVIEW；任一必需 FAILED → REWORK）；本模块经 `EvidenceReader.FailedVerifications(claim_id)` 提供系统反馈素材（验证器名 + stdoutTail 前 2000 字符，category 建议 FUNCTIONAL_DEFECT）。两个回调都以 `source_ref` 幂等；delivery **不得**再依 `verification.completed` 事件重复迁移（事件仅供投影与通知）。若 BR-03-60 判定无需验证，则直接调 `VerificationCompleted` 且 `required_total=0`。
- **BR-03-65 HUMAN 层**：记录人须持 `verification_run`、角色 ∈ spec.eligible_role_keys、且**不是**被验证执行的发起者或执行者（403 130403 `reason_code=SELF_REVIEW`）；FAILED/INCONCLUSIVE 必填 `note`；乐观锁 `version`。
- **BR-03-66 版本化**：`PATCH /verifiers/{id}` 修改 spec/timeout 即插入新版本并 `current_version_no+1`；进行中的验证使用其记录的版本；改 `applies_to`/`required`/`name` 不产生新版本。归档后不再被选中；不可删除。

### 5.10 相似度信号（旧 PRD F.10）

- **BR-03-67**：`EvidenceReader.ChangeSimilarity(execution_id)` 供 02 在提交声明事务内同步调用（02 BR-02-27 负责暂停与 `work_item.escalated`）：仅对 `branch_reason=QUALITY` 且父有 Claim 的执行计算：U = 父、子各自（相对工作项根基线）变更路径的并集；Δ = U 中父、子最终 blob（逐文件 SHA-256 / git blob）不同的路径数；返回 `{ratio=|Δ|/|U|, union_count, changed_count}`。`|U| < 5`（与 02 BR-02-27 一致：小改动集比例噪声大）、`changed_files` 截断或首个执行 → 返回 `ratio=null`（不触发）。结果缓存于 `similarity_ratio_ppm`，`ratio < 0.08` 时置 `similarity_flag=true` 供分支树与评审台显示琥珀标记。本模块不发布相似度事件、不做暂停决定。

### 5.11 环境加固

- **BR-03-70 环境变量白名单**：执行桥与运行时子进程只继承：`PATH, HOME, USERPROFILE, APPDATA, LOCALAPPDATA, TEMP, TMP, TMPDIR, LANG, LANGUAGE, LC_*, TERM, SHELL, USER, USERNAME, LOGNAME, SystemRoot, ComSpec, PATHEXT, XDG_CONFIG_HOME, XDG_DATA_HOME, XDG_CACHE_HOME, HTTP_PROXY, HTTPS_PROXY, NO_PROXY, SSL_CERT_FILE, NODE_EXTRA_CA_CERTS`；按 runtime 的认证变量：CLAUDE_CODE `ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, ANTHROPIC_BASE_URL, CLAUDE_CONFIG_DIR`；CODEX `OPENAI_API_KEY, OPENAI_BASE_URL, CODEX_HOME`；机器管理员在 04 配置的附加名单（`^[A-Z_][A-Z0-9_]{0,63}$`）；BR-03-25 三个执行变量。（Preview `bridgeEnvironment()` 为黑名单，本规格改为白名单。）
- **BR-03-71 令牌隔离**：机器凭证、控制面令牌、任何 04 通道凭证**不得**出现在子进程环境（按名称**且**按值扫描：任一变量值包含凭证子串即拒绝派发，`execution.rejected{reason:WORKDIR_UNAVAILABLE, detail:"env_leak"}`）、运行目录文件、上下文包、命令参数、日志。附加名单含 04 凭证变量名时在 04 配置即被拒绝。
- **BR-03-72 工作目录**：AUTONOMOUS 一律 WORKTREE（`{machine_data_dir}/worktrees/{execution_id}`，分支 `exec/{work_item_id}/{execution_id}`）；项目工作区非 git → SHARED_DIR + 写锁。INTERACTIVE 由用户选（关联工作项时默认 WORKTREE；ADHOC 默认 SHARED_DIR）。worktree 在终态后保留 7 天由机器清理；已合并分支可立即清理。
- **BR-03-73 单写**：同一 `(machine_id, workdir_key)` 同时只允许一个写入型派发（锁表 + 机器 OS 文件锁双层，`runtime-modes §6.3`）；READ_ONLY 派发不取锁。
- **BR-03-74 执行槽与锁释放**：执行进入终态、INTERRUPTED 或 PAUSED（含 INTERACTIVE 轮次结束）的**同一事务**内调 04 `MachineDirectory.ReleaseSlot` 并删除写锁；派发（E00/E21/E26）时重新占用。验证派发（BR-03-61）同样占用 1 个执行槽。

### 5.12 成本

- **BR-03-80 计量**：执行桥 `usage` 事件携带 tokens 与（若有）成本；Claude SDK `total_cost_usd` → RUNTIME_REPORTED；仅 tokens 时按 04 模型目录单价估算 → ESTIMATED；均无 → UNKNOWN。`cost_micros = round(usd × 10⁶)`。
- **BR-03-81 发布**：`execution.cost_updated` 合并发布：距上次 ≥10 s 且有增量，或增量 ≥100,000 micros，或进入终态；载荷为累计值 `cumulative_cost_micros` + `breakdown{model, compute, platform}`（消费方 07 取最大值即幂等）。
- **BR-03-82 护栏**：单执行 `max_cost_micros`（E14）；消费 07 `budget.threshold_reached{threshold_pct:100}` → 该项目全部 RUNNING AUTONOMOUS 执行按 E15 暂停，新派发经 07 `ProjectGate.CanStartExecution` 拒绝（409 130438）；80% 仅通知（08 路由）。

### 5.13 合并（`code_merge`）

- **BR-03-85 条件**（全满足，否则 409 130436 并在 `meta.unmet[]` 列出）：执行 SUCCEEDED 且 WORKTREE；工作项 ACCEPTED；该 Claim 必需验证全部 PASSED（INCONCLUSIVE 不行）；`head_commit` = Claim 时提交；无进行中或已 MERGED 的合并；08 `AuthorityChecker.Check(code_merge)` 允许 —— 经委托时另需：非自产（调用 Agent 不是执行者）、工作项风险 = LOW（PRD Q6）、委托窗口内；这些委托条件不满足时由 08 返回拒绝 → 403 130403（`reason_code` 透传 08，如 `DELEGATION_MERGE_RISK` / `SELF_REVIEW`），不再作为 409 的 `meta.unmet` 项。
- **BR-03-86 执行**：机器在项目工作区 `git merge --no-ff {branch_name}` 到 `target_branch`（默认项目基线），`push=true` 时推送；冲突 → CONFLICT（`conflict_files` ≤500，`git merge --abort` 复位）→ 409 130437 语义由合并资源状态呈现。成功 → MERGE 产物 + 合并提交 COMMIT 证据（producer=MACHINE）；审计 `execution.code_merged`（detail：reason、authority_source、evidence_refs）；发 `execution.merged`。

### 5.14 幂等、并发、离线、不存在

- **BR-03-90**：所有创建/命令型 POST/PUT 接受 `Idempotency-Key`（≤64），由 09 C7 平台幂等辅助处理（同键不同请求 422 `176425`，进行中 409 `176409`），本模块不建幂等表；轮次另以 `client_request_id` 去重（同 ID 返回同一 turn，不重复调用模型 —— Preview A02）。
- **BR-03-91**：消费领域事件按 09 C2 `domain_event_receipts`（Subscriber Mode Transactional）去重，本模块不建消费记录表；另以业务唯一键（`source_decision_id`；`claim_id`+`verifier_id`+`attempt`）双保险。
- **BR-03-92**：跨租户、跨项目或不存在的 ID 一律 404 130404（先解析归属再鉴权，路由规范"全局与作用域地址"）。
- **BR-03-93**：UI 断开不影响执行（PRD I6）；重连从 `GET /executions/{id}`（含 `change_seq`）+ `GET .../messages?after_msg_no=` 恢复。
- **BR-03-94**：数据源不可用时路由仍注册并返回 503 130448，`slog.Warn` 说明原因（底座"禁止静默降级"）。

---

## 6. 接口契约

### 6.1 通用

- huma v2 + `respx` 信封；错误 `respx.Err(ctx, code, i18nKey)`；ID 以十进制字符串传输。
- **守卫**：`E` = `authz.RegisterEntity`（发布路径前缀 `/tenants/{tenant_id}/entities/{entity_id}`，entity = 项目）；`O(x)` = `authz.RegisterOwned`，资源参数 `x`，归属解析器由拥有者提供（execution 自有资源用本模块解析器，`work_item_id`/`claim_id` 用 02 `WorkItemReader.Owner`）；`T` = `authz.Register`（租户级）；`P` = `authz.RegisterPrincipalTenantMember`。
- **Tag**：tag = 最终发布路径（含挂载前缀）的第一段，由底座 `OnAddOperation` 设置（00 v1.3 §9）；下表 tag 列即该值（`E`/`T`/`P` 守卫挂载于 `/tenants/...`，故为 `tenants`）。
- **幂等**：标 ✔ 的接受 `Idempotency-Key`；**锁**：标 v 的请求必带请求头 `If-Match: "<version>"`（GET 以 `ETag` 返回），缺失 428，不符 409 130409（00 v1.3 §3）。幂等由 09 C7 平台辅助处理（同键不同请求 422 `176425`，进行中 409 `176409`）。
- 列表一律 `pkg/query`（`field=op:value`，操作符 eq/in/like/gte/lte/gt/lt；offset 分页 `limit≤100`）并返回 `capabilities`。

### 6.2 操作清单

| # | 方法 路径（发布形态） | tag | 权限码 | 守卫 | 请求体要点 | 响应 `data` | 主要错误 | 幂/锁 |
|---|---|---|---|---|---|---|---|---|
| A01 | GET `/tenants/{t}/entities/{e}/executions` | tenants | execution_read | E | query §6.6 | `ExecutionSummary[]` | 403 | — |
| A02 | POST `/tenants/{t}/entities/{e}/executions` | tenants | execution_start | E | `{kind:"ADHOC", machine_id, runtime, model?, effort?, authorization_mode?, tools_enabled?, workdir_mode?, title?}` | `Execution`（201，CREATED） | 409 130413；422 130414；403 130403（`FULL_AUTO_FORBIDDEN`） | ✔ |
| A03 | GET `/tenants/{t}/entities/{e}/evidence` | tenants | execution_read | E | query：`claim_id, execution_id, work_item_id, evidence_type, state` | `Evidence[]` | 403 | — |
| A04 | GET `/tenants/{t}/entities/{e}/verifications` | tenants | execution_read | E | query：`claim_id, work_item_id, result, layer, required` | `Verification[]` | 403 | — |
| A05 | GET `/work-items/{work_item_id}/executions` | work-items | execution_read | O(work_item_id) | `?include=tree` | `{nodes: ExecutionTreeNode[], groups: EnsembleGroup[]}` | 404 | — |
| A06 | POST `/work-items/{work_item_id}/executions` | work-items | execution_start + work_item_execute | O(work_item_id) | `{kind:"INTERACTIVE", machine_id, runtime, model?, effort?, authorization_mode?, tools_enabled?, workdir_mode?:"WORKTREE"}` | `Execution`（201） | 409 130426/130413；422 130414/130441 | ✔ |
| A07 | POST `/work-items/{work_item_id}/ensembles` | work-items | execution_start + work_item_assign（人）；主导 Agent 经 `POLICY:ensemble_branch`（BR-03-05） | O(work_item_id) | `{branches:[{agent_id, runtime?, model?, effort?}] 2..3, reason≤2000}` | `{ensemble_group_id, executions: Execution[]}` | 409 130439；403 | ✔ |
| A08 | GET `/work-items/{work_item_id}/execution-comparisons` | work-items | execution_read | O(work_item_id) | `?execution_ids=a,b[,c]`（2–5，同工作项） | `ComparisonRow[]` | 422 | — |
| A09 | GET `/executions/{execution_id}` | executions | execution_read | O(execution_id) | — | `Execution`（含 `change_seq`、`branch_label`、当前轮、未决交互数） | 404 | — |
| A10 | PATCH `/executions/{execution_id}` | executions | execution_start（设置）；FULL_AUTO 另需 step-up | O(execution_id) | `{title?, model?, effort?, authorization_mode?, tools_enabled?, machine_id?, workdir_mode?, archived?:bool}` | `Execution` | 409 130409/130424/130411；422 130446；403 130403（`FULL_AUTO_FORBIDDEN` / `REAUTH_REQUIRED`） | v |
| A11 | PUT `/executions/{execution_id}/work-item-link` | executions | execution_start + work_item_execute | O(execution_id) | `{work_item_id}` | `Execution` | 409 130426；403 | ✔ v |
| A12 | PUT `/executions/{execution_id}/suspension` | executions | execution_stop（BR-03-40） | O(execution_id) | `{reason≤2000}` | `Execution` | 409 130410/130411；403 | ✔ |
| A13 | POST `/executions/{execution_id}/resumptions` | executions | execution_start | O(execution_id) | `{acknowledge_side_effects?:bool, machine_id?}` | `{execution: Execution, dispatch_epoch}`（202） | 409 130410/130413/130415/130438；422 130450 | ✔ |
| A14 | PUT `/executions/{execution_id}/termination` | executions | execution_stop（BR-03-40） | O(execution_id) | `{outcome:"CANCELLED"\|"FAILED"\|"SUCCEEDED", reason≤2000}` | `Execution` | 409 130410/130411；403 | ✔ |
| A15 | POST `/executions/{execution_id}/branches` | executions | execution_start | O(execution_id) | `{branch_reason:"RETRY_INFRA"\|"STRATEGY", machine_id?, agent_id?, reason≤2000}`（QUALITY/SCOPE 仅由 02 在裁决事务内经 `ExecutionLauncher.Launch` 同步建） | `Execution`（201，子执行） | 422 130440；409 130426 | ✔ |
| A16 | GET `/executions/{execution_id}/turns` | executions | execution_read | O(execution_id) | query：`state, turn_no(gte/lte)` | `Turn[]` | 404 | — |
| A17 | POST `/executions/{execution_id}/turns` | executions | execution_start（且为发起者） | O(execution_id) | `{client_request_id≤64, prompt≤64KiB, attachments?:[{name, text}]≤8, retry_of_turn_id?, acknowledge_side_effects?}` | `{turn: Turn, execution: Execution}`（202） | 409 130412/130413/130415/130416/130419/130411；413 130417；422 130418/130450 | client_request_id |
| A18 | PUT `/executions/{execution_id}/turns/{turn_id}/cancellation` | executions | execution_stop（BR-03-40） | O(execution_id) | `{}` | `Turn` | 409 130410 | ✔ |
| A19 | GET `/executions/{execution_id}/messages` | executions | execution_read | O(execution_id) | `?after_msg_no=&limit≤500` | `Message[]` + `meta.change_seq` | 404 | — |
| A20 | GET `/executions/{execution_id}/interactions` | executions | execution_read | O(execution_id) | query：`state, kind` | `Interaction[]` | 404 | — |
| A21 | PUT `/interactions/{interaction_id}/decision` | interactions | interaction_respond（HUMAN） | O(interaction_id) | `{action:"ACCEPT"\|"DECLINE", content?:object}` | `Interaction` | 409 130420/130421；422 130423；403 | ✔（重复相同决定 200） |
| A22 | GET `/executions/{execution_id}/events` | executions | execution_read | O(execution_id) | `?dispatch_epoch=&after_seq=&limit≤500` | `ExecutionEvent[]` | 404；410（已过保留期） | — |
| A23 | GET `/executions/{execution_id}/log/content` | executions | execution_read | O(execution_id) | `?tail_bytes=1..1048576`（默认 65536） | `text/plain` 流 | 404 | — |
| A24 | GET `/executions/{execution_id}/artifacts` | executions | execution_read | O(execution_id) | query：`kind` | `Artifact[]` | 404 | — |
| A25 | GET `/executions/{execution_id}/context-items` | executions | execution_read | O(execution_id) | query：`layer, inclusion` | `ContextItem[]` + `meta{budget, used, bundle_hash}` | 404 | — |
| A26 | GET `/executions/{execution_id}/evidence` | executions | execution_read | O(execution_id) | query：`state, evidence_type` | `Evidence[]` | 404 | — |
| A27 | POST `/executions/{execution_id}/evidence` | executions | evidence_submit | O(execution_id) | `{evidence_type:"OBSERVATION"\|"SCREENSHOT"\|"ARTIFACT"\|"PULL_REQUEST", title, scope_text?, text?, observed_at?, file_id?, artifact_id?, url?, limitations_extra?≤1000}` | `Evidence`（201，DRAFT） | 422 130422/130432；409 130411/130431 | ✔ |
| A28 | PATCH `/evidence/{evidence_id}` | evidence | evidence_submit | O(evidence_id) | `{state?:"WITHDRAWN"\|"DRAFT", title?, limitations_extra?}` | `Evidence` | 409 130430 | — |
| A29 | GET `/evidence/{evidence_id}` | evidence | execution_read | O(evidence_id) | — | `Evidence` | 404 | — |
| A30 | GET `/evidence/{evidence_id}/content` | evidence | execution_read | O(evidence_id) | — | 文件流（files 授权下载） | 404 | — |
| A31 | POST `/executions/{execution_id}/evidence-collections` | executions | evidence_submit | O(execution_id) | `{client_request_id, kinds[], report_globs?[]}` | `EvidenceCollection`（202） | 409 130413/130411 | client_request_id |
| A32 | GET `/evidence-collections/{collection_id}` | evidence-collections | execution_read | O(collection_id) | — | `EvidenceCollection` + 生成的 `Evidence[]` | 404 | — |
| A33 | POST `/claims/{claim_id}/verifications` | claims | verification_run | O(claim_id) | `{verifier_id}`（重跑或补跑） | `Verification`（201） | 409 130433（验证器已归档）；403 | ✔ |
| A34 | GET `/verifications/{verification_id}` | verifications | execution_read | O(verification_id) | — | `Verification`（含 evidence_json） | 404 | — |
| A35 | PUT `/verifications/{verification_id}/result` | verifications | verification_run | O(verification_id) | `{result:"PASSED"\|"FAILED"\|"INCONCLUSIVE", note?≤65536, evidence_ids?[]}` | `Verification` | 409 130434/130410/130409；403 130403（`SELF_REVIEW`） | v |
| A36 | POST `/executions/{execution_id}/merges` | executions | code_merge（经 08 判定） | O(execution_id) | `{target_branch?, push?:bool, reason≤2000}` | `Merge`（202） | 409 130436；403 | ✔ |
| A37 | GET `/merges/{merge_id}` | merges | execution_read | O(merge_id) | — | `Merge` | 404 | — |
| A38 | GET `/tenants/{t}/verifiers` | tenants | policy_read | T | query：`layer, state, required` | `Verifier[]` | 403 | — |
| A39 | POST `/tenants/{t}/verifiers` | tenants | verifier_manage | T | `{key, name, layer, applies_to, required, spec, timeout_ms, change_note?}` | `Verifier`（201，v1） | 422 130433；409（key 重复）130409 | ✔ |
| A40 | GET `/verifiers/{verifier_id}` | verifiers | policy_read | O(verifier_id) | — | `Verifier` + 当前版本 spec | 404 | — |
| A41 | PATCH `/verifiers/{verifier_id}` | verifiers | verifier_manage | O(verifier_id) | `{name?, applies_to?, required?, spec?, timeout_ms?, state?:"ARCHIVED"\|"ACTIVE", change_note?}` | `Verifier` | 422 130433；409 130409 | v |
| A42 | GET `/verifiers/{verifier_id}/versions` | verifiers | policy_read | O(verifier_id) | — | `VerifierVersion[]` | 404 | — |
| A43 | GET `/tenants/{t}/principals/{principal_id}/executions` | tenants | （本人；行级按项目 `execution_read` 过滤） | P | query §6.6 | `ExecutionSummary[]`（跨项目"我的执行"） | 403 | — |

**表示（摘要）**：
- `Execution`：A02/A06 的全部设置字段 + `id, project_id, work_item_id, kind, purpose, parent_execution_id, branch_reason, seq_no, reason_ordinal, branch_label, ensemble_group_id, state, state_reason, state_detail, initiator{kind,id,authority_source}, assignee{kind,id,display}, agent_id, machine{id,name,online}, workdir_mode, workdir_path, branch_name, base_commit, head_commit, native_session_bound:bool, carryover_pending, dispatch_epoch, current_turn?: Turn, pending_interaction_count, cost{micros, source, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens}, limits{turns_used, messages_used, output_bytes, max_output_bytes}, timestamps, claim_id, similarity{ratio, flag}, change_seq, version`。**不返回** `native_session_id` 原值（仅布尔，减少跨机误用）。
- `ExecutionTreeNode`：`{id, parent_id, seq_no, branch_label, branch_reason, state, state_reason, assignee, machine, runtime, model, cost_micros, claim_id, verification{passed, failed, inconclusive, pending, required_total}, similarity_flag, ensemble_group_id, created_at, ended_at}`。
- `ComparisonRow`：`{execution_id, branch_label, assignee, runtime, model, state, claim_present, required_passed, required_total, criteria_covered, criteria_total（经 02 读取）, diffstat{files, insertions, deletions}, cost_micros, duration_ms}`。
- `Evidence`（来源卡）：`{id, evidence_type, state, title, producer{kind,id,display,machine?,tool?}, produced_at, collected_at, content_hash, verifier?{id, key, version_no}, result?, limitations, scope_text, summary, uri?, file?{id, size, mime}, redaction_count, claim_id?}`。
- `Verification`：`{id, claim_id, execution_id, verifier{id,key,name,layer}, verifier_version_no, required, advisory, attempt, result, run_state, result_reason, tool_version, machine?, output(evidence_json), note?, verified_by?, started_at, finished_at, deadline_at, version}`。

### 6.3 机器执行级消息（传输与认证属 04；本表定义语义与载荷）

信封（04 定义）至少含：协议版本、`message_id`、`machine_id`、连接代次。执行级载荷另含 `execution_id`、`dispatch_epoch`（验证/采集/合并用各自 ID）。

| 方向 | 消息 | 载荷要点 | 服务端处理 / 响应 |
|---|---|---|---|
| S→M | `execution.start` | `{command_id, execution_id, dispatch_epoch, kind, purpose, runtime, model?, effort?, authorization_mode, tools_enabled, workdir:{mode, repo_path, base_ref, base_commit?, branch_name?, path?}, native_session_id?, prompt（INTERACTIVE 为本轮提示，含 carryover 前缀；AUTONOMOUS 为引导语）, context:{inline_bundle? \| file_ref?, sha256}, env_extra_names[], limits:{timeout_ms, max_output_bytes:8388608, heartbeat_ms:15000, lease_renew_ms:10000, self_stop_ms:20000}, turn_id?}` | 机器 ACK 命令；按 `command_id` 去重 |
| S→M | `execution.cancel` | `{command_id, execution_id, dispatch_epoch, reason}` | 机器：bridge cancel → 4 s → kill tree |
| S→M | `interaction.response` | `{command_id, execution_id, dispatch_epoch, interaction_id, native_request_id, action, content?}` | 机器按原 schema 再校验后交执行桥；随后发 `interaction_delivered` |
| S→M | `evidence.collect` | `{command_id, collection_id, execution_id, base_commit, kinds[], report_globs[]}` | |
| S→M | `verification.start` / `verification.cancel` | `{command_id, verification_id, commit, workdir, spec, timeout_ms}` | |
| S→M | `merge.start` | `{command_id, merge_id, repo_path, source_branch, source_commit, target_branch, push}` | |
| M→S | `execution.accepted` | `{execution_id, dispatch_epoch, runtime_version, pid?}` | E01；200 `{lease_expires_at}` |
| M→S | `execution.rejected` | `{execution_id, dispatch_epoch, reason, detail≤2000}` | E02 |
| M→S | `execution.events` | `{execution_id, dispatch_epoch, events:[{seq, type, at, payload}] ≤200 条 / ≤1 MiB}` | BR-03-21；200 `{ack_seq, cancel_requested:bool}`；409 130428 `{expected_seq}`；409 130427 |
| M→S | `leases.renew` | `{items:[{execution_id, dispatch_epoch}] ≤64}` | 200 `{items:[{execution_id, status:"OK"\|"FENCED"\|"CANCEL_REQUESTED", lease_expires_at?}]}` |
| M→S | `execution.lost` | `{execution_id, dispatch_epoch, reason:"HEARTBEAT_LOST"\|"DAEMON_RESTARTED"\|"OUTBOX_FULL"\|"OUTBOX_GAP", detail}` | E19 |
| M→S | `blob.upload` | 流式；元数据 `{purpose:"evidence"\|"log"\|"artifact"\|"report", owner_id, name, mime, bytes, sha256}`，单个 ≤100 MiB | 服务端计算哈希、脱敏（文本类）、写 files 并绑定；返回 `{file_id, sha256}` |
| M→S | `evidence.collected` | `{collection_id, state, items:[{type, title, produced_at, summary, file_id?, producer_tool}], error?}` | 去重插入 DRAFT 证据 |
| M→S | `verification.started` / `verification.result` | `{verification_id, tool_version?, exit_code, signal?, duration_ms, stdout_tail, stderr_tail, files:[{path, file_id}], report?}` | BR-03-62 |
| M→S | `merge.result` | `{merge_id, state, merge_commit?, conflict_files?, pushed}` | BR-03-86 |

**事件类型**（`execution.events` 内，`type` → `payload`；源自 Preview `Event`）：`session_bound{native_session_id}`；`text{delta}`（≤64 KiB/条）；`tool{tool_call_id, title, state:"running"|"completed"|"failed", details}`；`status{text}`（SYSTEM 消息）；`interaction_requested{native_request_id, kind:"TOOL_PERMISSION"|"RUNTIME_INPUT", title, details?, message?, url?, cwd?, schema?, deadline_at?}`；`interaction_delivered{interaction_id}`；`usage{input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd?}`（增量）；`artifact{kind, name, ref, file_id?, metadata}`；`head{commit, changed_files?}`；`turn_ended{outcome:"completed"|"cancelled"}`；`error{message}`；`exited{exit_code, claim?:object（含 citations[]）, continue_file_id?, memory_file_id?（BR-03-30c）, head_commit?, evidence_collection?:{...同 evidence.collected}}`。未知 `type` → 400 130449，机器停止该派发。

### 6.4 跨模块接口

**本模块提供**（`module.Provide`，接口在 `internal/execution` 根包冻结）：

| 接口 | 方法（语义） | 使用者 |
|---|---|---|
| `ExecutionLauncher`（00 §8；方法集与 02 CCR-05 对齐） | `Launch(ctx, tx, LaunchRequest{work_item_id, assignment_id, kind, branch_reason, parent_execution_id?, workspace_mode?:RESTART\|CONTINUE, feedback_ref?(=source_decision_id), source_change_record_id?, ensemble_group_id?, assignee_principal_id, agent_id?, machine_id?, runtime?, model?, effort?, max_cost_micros?, autostart, actor, authority_source}) (execution_id, error)`（在调用方事务内写本模块行；同 `feedback_ref` 重复调用返回既有 ID）；`Pause(ctx, tx, work_item_id, reason)`；`Resume(ctx, tx, work_item_id)`（含派发因有界自治停在 CREATED、`autostart=false` 的分支，02 人工 RESUME 时调用）；`CancelExecution(ctx, tx, execution_id, reason)`（单个执行，05 ENSEMBLE 落选/截止与 02 改派用）；工作项取消时 02 对该工作项每个非终态 DELIVERY 执行逐一调用 `CancelExecution`（可经 `ExecutionReader` 列出），不再提供按工作项的 `Cancel`（复审 #17）。`LaunchRequest` 另含 `purpose?`（默认 DELIVERY；05 交接用 HANDOVER）、`project_id`（`kind=ADHOC` 时必填，此时 `work_item_id`/`assignment_id` 为空）、`ensemble_group_id?`、`max_cost_micros?`；`autostart=true` 时发起者为 SYSTEM（BR-03-09） | 02、05 |
| `EvidenceReader`（00 §8；按 02 §12.1 扩充） | `ListByClaim(claim_id)`；`Get(evidence_id)`；`BelongsToLineage(work_item_id, execution_id, evidence_ids) (own, cited, invalid)`；`VerificationSummary(claim_id) {required_total, passed, failed, inconclusive, pending, automated_passed, has_non_ai_pass}`；`FailedVerifications(claim_id)`；`ChangeSimilarity(execution_id) {ratio?, union_count, changed_count}`；`LiveExecution(work_item_id)`（当前非终态 DELIVERY 执行）；`Participants(work_item_id)`（全部分支的发起者与执行者，供 02 职责分离）；`ExecutionCost(execution_id)`；`WorkItemCost(work_item_id)`（该工作项全部 DELIVERY 执行累计成本，不含 VERIFICATION，08 委托成本条件用）；`DiffSummary(execution_id)`；`SecretFlags(evidence_ids) map[id]{secret_scanned, secret_flagged, redaction_count}`（07 交付包脱敏） | 02、06、07、08 |
| `ExecutionReader` | `Get(execution_id)`（出处：机器、runtime、模型、发起者、分支，06 知识溯源用）；`ActiveCount(tenant, project)`；`AttentionByDay(tenant, project_ids[], from, to)`（人工回应交互的时长按 UTC 日汇总，07 人工关注分钟用） | 06、07、08 |
| `EvidenceBinder` | `Attach(ctx, tx, claim_id, work_item_id, execution_id, own_ids, cited_ids) error`（BR-03-56）；`ValidateClaimable(ctx, tx, execution_id, fromExit bool) error`：ADHOC → 130425；`fromExit=true`（E06 事务内）允许 RUNNING；否则允许 PAUSED、或 SUCCEEDED 且尚无 Claim；其余 RUNNING → 130447，终态已有 Claim → 130411 | 02 |
| `ExecutionDispatchHandler` | `PendingCommands(machine, conn_epoch, max)`；`AckCommands`；`Accept`；`Reject`；`AppendEvents`；`RenewLeases`；`ReportLost`；`UploadBlob`；`ReportCollection`；`ReportVerification`；`ReportMerge` —— §6.3 的服务端处理 | 04（通道） |

**本模块使用**：02 `WorkItemReader`（`Get`、`Criteria`、`ActiveAssignment`，以及路由归属解析 `Owner`）、`WorkItemTransitioner`（02 §6.9：`ExecutionStarted`、`ExecutionFailed`、`VerificationQueued`、`VerificationCompleted`、`RenewLease`，均在本模块事务内执行并以 `source_ref` 幂等）、`ClaimSubmitter.SubmitOn`（02 提供，AUTONOMOUS 代 Agent 提交 Claim）、`DecisionReader`（第 4 层反馈全文）；04 `MachineDirectory`（状态、runtime、`ResolveWorkspace(machine, project)`、`ReserveSlot/ReleaseSlot`、模型目录与单价、附加环境变量名单）、`MachineChannelNotifier.Wake`；05 `AgentDirectory`；06 `Retriever.BuildInjection(scope_chain, query, token_budget)`、`Retriever.RecordInclusion`、`Retriever.RecordCitations`、`SkillCatalog.ForAgent`、`MemoryWriter.Propose`；07 `ProjectGate.CanStartExecution`；08 `AuthorityChecker`；09 `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`、`Notifier`、平台幂等辅助；底座 `files.ResourceBindings`（注册 purpose：`execution_evidence`、`execution_log`、`execution_context`、`execution_artifact`，均 resource_kind=对应资源、OwnerScopes=`["entity"]`、VersionMode=`pinned`；系统身份写入经 09 C9）、`audit.Appender`、`guid`、`dlock`。

### 6.5 错误码（execution 段 130000–139999；i18n namespace `execution`，三语齐全）

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 130403 | 403 | execution.error.forbidden | 权限不足；`data.reason_code` ∈ {NOT_OWNER, SELF_REVIEW, FULL_AUTO_FORBIDDEN, REAUTH_REQUIRED, HUMAN_ONLY, AGENT_PROHIBITED, DELEGATION_MERGE_RISK, 及 08 AuthorityChecker 透传码} |
| 130404 | 404 | execution.error.not_found | 不存在或不属于该租户/项目 |
| 130409 | 409 | execution.error.version_conflict | 乐观锁 / 序号分配冲突 |
| 130410 | 409 | execution.error.illegal_transition | 非法状态迁移 |
| 130411 | 409 | execution.error.terminal | 执行已结束 |
| 130412 | 409 | execution.error.turn_active | 已有进行中的轮次 |
| 130413 | 409 | execution.error.machine_offline | 机器离线或排空中 |
| 130414 | 422 | execution.error.runtime_unsupported | 机器未安装该 runtime |
| 130415 | 409 | execution.error.workdir_busy | 工作目录已有写入型执行 |
| 130416 | 409 | execution.error.history_limit | 轮次/消息/输出达到上限，请新开会话 |
| 130417 | 413 | execution.error.prompt_too_large | 消息与附件合计超过 64 KB |
| 130418 | 422 | execution.error.attachment_invalid | 附件数量/大小/编码不合法 |
| 130419 | 409 | execution.error.retry_not_allowed | 只有无输出的失败轮次可重试 |
| 130420 | 409 | execution.error.interaction_closed | 交互已过期/已取消/已送达 |
| 130421 | 409 | execution.error.interaction_already_decided | 其他客户端已作出不同决定 |
| 130422 | 422 | execution.error.validation | 请求体校验失败（含手工提交仅限自动采集的证据类型） |
| 130423 | 422 | execution.error.interaction_schema | 回答不符合请求的 JSON Schema |
| 130424 | 409 | execution.error.settings_while_running | 执行中不可修改该设置 |
| 130425 | 409 | execution.error.adhoc_not_linked | 临时执行须先关联工作项 |
| 130426 | 409 | execution.error.work_item_state | 工作项状态或分派不允许此操作 |
| 130427 | 409 | execution.error.fenced | 派发已被栅栏（机器通道） |
| 130428 | 409 | execution.error.seq_gap | 事件序号缺口（机器通道） |
| 130430 | 409 | execution.error.evidence_locked | 证据已绑定声明，不可修改 |
| 130431 | 409 | execution.error.evidence_limit | 证据数量达到上限 |
| 130432 | 422 | execution.error.hash_mismatch | 内容哈希不一致 |
| 130433 | 422 | execution.error.verifier_invalid | 验证器定义不合法或已归档 |
| 130434 | 409 | execution.error.not_human_verification | 只有人工验证可手动记录结果 |
| 130436 | 409 | execution.error.merge_precondition | 合并条件不满足 |
| 130437 | 409 | execution.error.merge_conflict | 合并冲突 |
| 130438 | 409 | execution.error.project_gate_closed | 项目不允许启动执行（预算暂停 / 项目暂停 / 阶段不允许，07 `ProjectGate`） |
| 130439 | 409 | execution.error.ensemble_limit | 并行分支数达到上限 |
| 130440 | 422 | execution.error.branch_reason_invalid | 分支原因与父执行不匹配 |
| 130441 | 422 | execution.error.context_budget | 不可裁剪的上下文超过预算 |
| 130445 | 409 | execution.error.native_session_mismatch | 原生会话与记录不一致 |
| 130446 | 422 | execution.error.machine_runtime_mismatch | 目标机器不支持当前 runtime |
| 130447 | 409 | execution.error.claim_requires_idle | 请先等待本轮结束或停止执行 |
| 130448 | 503 | execution.error.unavailable | 执行服务暂不可用 |
| 130449 | 400 | execution.error.event_invalid | 机器事件不合法 |
| 130450 | 422 | execution.error.side_effect_ack_required | 恢复中断的执行前须确认已检查副作用 |

### 6.6 列表查询字段（`pkg/query`）

| 资源 | 可过滤（操作符） | 可排序 | 默认 |
|---|---|---|---|
| 执行（A01/A43） | `state`(eq,in)、`kind`(eq,in)、`purpose`(eq)、`branch_reason`(in)、`work_item_id`(eq)、`machine_id`(eq)、`runtime`(eq)、`agent_id`(eq)、`assignee_principal_id`(eq)、`initiator_id`(eq)、`pending_interaction_count`(gt)、`similarity_flag`(eq)、`cost_micros`(gte,lte)、`created_at`/`started_at`/`ended_at`(gte,lte)、`title`(like)、`archived`(eq) | `created_at`、`started_at`、`ended_at`、`cost_micros`、`seq_no`、`updated_at` | `-created_at`；`purpose=DELIVERY`、`archived=false` |
| 证据（A03/A26） | `claim_id`、`execution_id`、`work_item_id`(eq)；`evidence_type`、`state`(in)；`auto_collected`(eq)；`produced_at`(gte,lte) | `produced_at`、`created_at` | `-produced_at` |
| 验证（A04） | `claim_id`、`work_item_id`、`verifier_id`(eq)；`result`、`layer`(in)；`required`(eq)；`finished_at`(gte,lte) | `queued_at`、`finished_at` | `-queued_at` |
| 验证器（A38） | `layer`、`state`(in)；`required`(eq)；`key`、`name`(like) | `key`、`updated_at` | `key` |

---

## 7. 后台任务与事件

### 7.1 后台任务（经 09 `JobQueue`；互斥用底座 `dlock`，锁 TTL = 2×间隔；每批 ≤200 行，批内逐行独立事务，单行失败不影响其他行）

| 任务 | 触发 / 频率 | 互斥 | 动作 | 重试 |
|---|---|---|---|---|
| `execution.lease_watchdog` | 每 5 s | dlock `execution:lease-watchdog` | E16；清理持有者非 RUNNING 的 `workdir_locks` 残留 | 下个周期自然重试 |
| `execution.startup_fence` | 模块 `Start` 一次（`restart_fencing=all`） | dlock `execution:startup-fence` | E18；验证 RUNNING 中的重排（§4.4） | 失败则 `Start` 失败（不静默降级） |
| `execution.dispatch_timeout` | 每 30 s | dlock `execution:dispatch-timeout` | E03、E04；`cancel_requested_at+30s` 强制终态；命令重新下发（BR-03-23）；E12 超时检查 | 同上 |
| `execution.interaction_expiry` | 每 15 s | dlock `execution:interaction-expiry` | PENDING 到期 → EXPIRED（§4.3）；到期前 5 min 触发提醒 N1b | 同上 |
| `execution.verification_scheduler` | 消费 `claim.submitted` + 每 5 s 轮询 | dlock `execution:verification-scheduler` | 建验证行、派发 QUEUED、超时判 INCONCLUSIVE、离线重排、汇总迁移（BR-03-64） | 消费失败按 09 指数退避（1 s→5 min，最多 10 次）后进死信并告警 |
| `execution.collection_timeout` | 每 30 s | dlock `execution:collection-timeout` | 采集超 120 s → TIMED_OUT；合并超 10 min → FAILED | 同上 |
| `execution.knowledge_sync` | 每 60 s | dlock `execution:knowledge-sync` | 重试失败的 `RecordInclusion` / `RecordCitations` / `MemoryWriter.Propose`（BR-03-30b/c），每项最多 10 次后记 `slog.Warn` 放弃 | 同上 |
| `execution.cost_publisher` | 每 10 s | dlock `execution:cost-publisher` | 按 BR-03-81 合并发布 `execution.cost_updated`；检查 E14 | 同上 |
| `execution.retention` | 每日 03:00 UTC | dlock `execution:retention` | 删除终态 >30 天执行的 `execution_events`；删除 ACKED/EXPIRED/SUPERSEDED >7 天的 `execution_commands`；解绑并删除**未被证据引用**的日志文件（>90 天）与上下文包文件（>180 天）；证据、验证、消息、产物元数据永不删除 | 次日重试；`execution.retention.failed` 告警 |

### 7.2 发布的领域事件（`data` 字段；载荷外层按 00 §7.1）

| 事件 | 时机 | `data` |
|---|---|---|
| `execution.started` | 首次 E01 | `{execution_id, work_item_id?, kind, purpose, branch_reason?, seq_no?, parent_execution_id?, agent_id?, machine_id, runtime, model?}` |
| `execution.state_changed` | 每次 Execution 迁移 | `{execution_id, work_item_id?, kind, purpose, from, to, reason?, failure_kind?:"INFRA"\|"RUNTIME", dispatch_epoch, claim_id?}` —— 供投影、频道与通知；工作项迁移已经由同步回调完成（BR-03-14、E01），消费方不得据此再迁移 |
| `execution.cost_updated` | BR-03-81 | `{execution_id, work_item_id?, project_id, agent_id?（AGENT 执行时必填，供 08 编制预算按子 Agent 累计，D110）, cumulative_cost_micros, breakdown{model, compute, platform}, cost_source, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens}`（均为累计） |
| `verification.human_requested` | HUMAN 层验证进入 PENDING·QUEUED | `{verification_id, claim_id, work_item_id, verifier_key, eligible_role_keys[], deadline_at}`（08 我的裁决） |
| `interaction.requested` | §4.3 PENDING | `{interaction_id, execution_id, work_item_id?, kind, title, expires_at, initiator_id?, assignee_principal_id}`（不含 details 原文） |
| `interaction.decided` | DECIDED | `{interaction_id, execution_id, action, responder{kind,id}}` |
| `evidence.added` | 证据 ATTACHED | `{claim_id, work_item_id, execution_id, evidence_ids[], by_type{...}}` |
| `verification.completed` | 每条验证进入 DONE | `{verification_id, claim_id, work_item_id, layer, required, advisory, result, result_reason?, all_required_done, aggregate:"PASSED"\|"FAILED"\|"INCONCLUSIVE"\|null}` |
| `interaction.closed` | EXPIRED / CANCELLED | `{interaction_id, execution_id, state, closed_reason}` |
| `execution.merged` | 合并 MERGED | `{merge_id, execution_id, work_item_id, target_branch, merge_commit, pushed}` |

### 7.3 消费的领域事件

| 事件 | 处理 |
|---|---|
| `claim.submitted`（02） | E22（INTERACTIVE）/ 记录 `claim_id`；排队验证（BR-03-60、BR-03-64） |
| `decision.made`（02） | **不消费**：返工建枝、暂停、恢复、取消均由 02 在裁决事务内同步调用 `ExecutionLauncher`（BR-03-06；00 §7.1） |
| `work_item.state_changed`（02） | CANCELLED → E27（幂等兜底；主路径为 02 逐个调用 `ExecutionLauncher.CancelExecution`） |
| `work_item.assigned`（02） | 改派（EXECUTOR 变更）→ 原执行者的非终态 DELIVERY 执行 E27（SUPERSEDED）（幂等兜底） |
| `machine.state_changed`（04） | OFFLINE → E17（MACHINE_OFFLINE）；REVOKED → E17（MACHINE_REVOKED），已 INTERRUPTED 的不再变更；DRAINING → 不再派发新命令（已运行继续） |
| `agent.state_changed` / `agent.retired`（05） | STOPPED / RETIRING / RETIRED → E27（AGENT_STOPPED，HANDOVER 豁免）；CRASHED → E19b（AGENT_CRASHED） |
| `budget.threshold_reached`（07） | `threshold_pct≥100` → BR-03-82 |

### 7.4 实时推送（09 SSE）

主题 `tenant/{t}/execution/{id}`（`TopicAuthorizer`：`execution_read`，按归属项目判定）：帧 `{change_seq, kind:"state"|"message"|"message_delta"|"tool"|"interaction"|"cost"|"turn"|"artifact"|"evidence"|"verification", payload}`；文本增量在服务端按 250 ms 合并后推送。客户端发现 `change_seq` 跳号 → 重取 A09 + A19（`after_msg_no`）。主题 `tenant/{t}/project/{project_id}`（项目扇出，帧 `subject.kind=execution`）：列表行变化（状态、成本、未决交互数）。"我的裁决"由 08 订阅 `interaction.*` 聚合，≤1 s 移除已处理项。

---

## 8. 页面规格

> 应用 `apps/frontend-web-admin/apps/delivery`；组件只引用语义 Token；所有页面具备加载骨架、空态、错误态（可重试）、无权限态（写明缺少的权限码）、离线/重连态（顶部条"正在重连… 显示的可能不是最新状态"，保留已加载内容）。文案称"你"；不出现 Runner / Daemon / Computer（技术详情面板除外）。

### 8.1 执行列表

- **目的**：查看项目内全部执行与会话，快速进入待你决定的那一个。
- **入口与路由**：侧栏「交付 → 执行」；`/t/:tenantID/p/:projectID/executions`；"我的执行" `/t/:tenantID/executions?mine=1`（A43）。
- **布局**：顶部筛选条（状态多选、种类、机器、runtime、执行者、工作项、分支原因、"仅待你决定"开关、时间范围、"显示已归档"）；右上〔新建临时会话〕；主体 AutoTable。
- **列**：标题（ADHOC 显示"临时"徽标）/ 所属工作项链接；分支标签（`#3 · 质量返工 2`，紫色）；`StateBadge`（RUNNING 且有未决交互时显示琥珀"待你决定 n"）；执行者 `ActorAvatar`；机器（在线点）；runtime · 模型；成本（`$0.42` / 估算前缀 `≈` / 未知显示 `—` + token 数）；开始时间；时长。
- **交互**：行点击 → INTERACTIVE/ADHOC 进工作台，AUTONOMOUS 进详情；行菜单〔停止〕（BR-03-40 可用时）、〔归档〕（终态）。〔新建临时会话〕需 `execution_start`，弹窗字段：机器（离线/无 runtime 置灰并注明原因）、runtime（无"自动"降级：未安装即不可选）、模型（04 目录 + 手填 ≤160）、推理强度（随模型）、授权模式（FULL_AUTO 红色并触发重新认证）、工具开关（Codex 置灰并说明"CLI exec 不支持网页审批"）、工作目录模式；创建中按钮显示进度，失败保留表单。
- **实时**：订阅项目列表主题；≤1 s 更新状态与成本。
- **空态**："还没有执行。工作项分派给 Agent 后会自动开始；你也可以〔新建临时会话〕"。
- **无权限**："需要 execution_read 权限"。
- **移动端**：表格改卡片（标题、状态、分支、成本、机器），筛选收进底部 Sheet；底部快捷栏"执行"即此页。

### 8.2 交互会话工作台（Preview 01 全量对齐）

- **目的**：人与 runtime 多轮对话、处理授权、在同一处提交完成声明。
- **路由**：`/t/:tenantID/p/:projectID/executions/:executionID/session`。
- **头部（常驻）**：标题（可重命名，1–200 字符，`version` 冲突提示"名称已在其他客户端修改"）；`StateBadge`；工作项链接（ADHOC 显示"未关联工作项"+〔关联工作项〕）；分支标签；机器（在线点；离线时链接"执行节点离线，切换机器"）；runtime · 模型 · 强度（下一轮将生效的修改显示"下一轮生效"小字）；工具状态摘要（"Skills 12 · MCP 2" / "检测中" / "检测失败" / "工具未启用"）；成本（`$1.23 · 48.2k tokens`）；〔提交完成声明〕；〔⋯〕菜单（重命名、会话设置、结束会话、停止执行、归档；支持方向键/Enter/Esc，Shift+F10 打开）。
- **消息区**：USER / ASSISTANT 气泡（Markdown 安全渲染：禁用原始 HTML、不加载远程图片），复制按钮；TOOL 行为可折叠 `<details>`（首行摘要 + 状态图标，展开为等宽详情）；SYSTEM 消息居中灰条；最后一条失败系统消息满足 BR-03-35 时带〔重试〕（tools/FULL_AUTO 时二次确认）；跟随滚动：距底部 <100 px 时自动跟随；>500 条消息虚拟滚动（PRD §16）。活动条："排队中" / "生成中" / "等待你决定" / "正在停止执行"。
- **交互卡片**（`InteractionCard`，消息流内 + 移动端吸附在输入框上方）：标题、详情（等宽、可滚动）、说明、URL（仅 http/https 渲染为外链）、`CountdownRing`（30 min）、状态徽标（需要确认 / 已提交 / 已送达 / 已过期 / 已取消 / 未送达）。TOOL_PERMISSION：〔拒绝〕〔允许一次〕；RUNTIME_INPUT：可折叠"输入结构"、JSON 文本框（必须为对象，否则"请输入 JSON 对象"）、〔拒绝〕〔提交〕。执行 `cancel_requested` 时按钮禁用；他端已处理时卡片 ≤1 s 更新为"已提交 · 由 X"；409 130421 提示"其他客户端已作出不同决定"。
- **使用的知识**（可折叠，默认收起；仅关联工作项时出现）：按层分组，条目显示来源图标、标题（链接到知识库来源）、引用号 `[K3]`、得分、token、纳入方式（全文 / 引用 / 已裁剪）；顶部"预算 12,000 · 已用 9,870"。
- **输入区**（`SkillComposer`）：文本框 Enter 发送、Shift+Enter 换行、输入法组合期间不触发；输入 `/` 或 `$`（独立 token，非 URL/路径）弹出 Skills 候选：按名称/说明筛选、↑↓ 选择、Enter/Tab 插入、Esc 关闭；插入 CLAUDE_CODE `/名称`、CODEX `$名称` 并补空格，不自动发送；候选弹层状态行：未启用工具 / 正在检测 Skills / Skills 检测失败 / 没有匹配的 Skill / 没有可用的 Skill；禁用的 Skill 不出现。底栏图标：〔文件〕（8 个、合计 48 KB、UTF-8 文本、拒绝二进制，错误逐条提示）、〔Skill〕（在光标处插入触发符）、〔MCP〕（打开设置 MCP 页）；附件芯片可移除；工作目录路径；发送/停止按钮。发送按钮禁用条件：内容与附件皆空、发送中、读取附件中、机器离线、执行终态、RUNNING（此时显示〔停止生成〕）。草稿与未发送附件按会话保存在页面内存（刷新丢失，与 Preview 一致）；发送失败保留草稿与附件；客户端为相同提示复用同一 `client_request_id`。
- **会话设置**（右侧抽屉，移动端底部 Sheet）：授权模式（READ_ONLY / ON_REQUEST / FULL_AUTO，FULL_AUTO 红色语义 + 后果说明"允许完整访问该机器，不再逐项确认" + 底座 ReauthenticationDialog）；运行状态"执行中/空闲"；切换机器（仅在线且支持同 runtime 可选，说明"切换后尝试续接原生会话"或"将把最近的对话带入新机器"；RUNNING 时禁用）；执行方式说明（Codex CLI · exec / Claude Code · Agent SDK）；工具开关（RUNNING 禁用）；模型选择（目录搜索 + 手填）与推理强度（随模型；当前值不在目录时显示"(当前)"），〔保存〕仅在有变更时可用；能力清单：Skills / MCP 标签页、搜索、状态徽标（可用/已禁用/已连接/连接中/失败/未连接/未知）、来源路径、认证、MCP 工具折叠列表、〔刷新〕（离线/检测中禁用）、检测时间、错误逐条显示；空态"runtime 未返回条目"。焦点陷阱，Esc 关闭。
- **提交完成声明**（`ClaimSubmitDialog`）：
  - 可用条件：已关联工作项、执行非 RUNNING 且非终态、调用者为执行者、工作项状态 ∈ {IN_PROGRESS, REWORK}；否则按钮禁用并 tooltip 说明原因（如"请先关联工作项"、"请先等待本轮结束"）。
  - 打开即发起 A31 自动采集（进度列表：提交 n、测试报告 n、日志；PARTIAL/失败可〔重试采集〕）；证据列表为 `EvidenceCard` 精简版 + 勾选框；〔添加人工观察〕（正文、范围必填）、〔上传截图〕、〔登记 PR 链接〕。
  - 验收标准映射：每条标准一行，多选证据；未覆盖行琥珀色"未覆盖"；顶部覆盖率"4/5 条已覆盖"。
  - 摘要（必填 ≤2000）。提示语："完成声明不等于完成 —— 提交后将进入自动验证，再由有权限的人裁决。"
  - 〔提交〕禁用直到：摘要非空、≥1 条证据；存在未覆盖标准时提交前确认"有 n 条验收标准没有证据，评审时将标为琥珀"。提交调用 02 `POST /work-items/{id}/claims {execution_id, summary, evidence_ids[], criteria_map[]}`；成功后工作台转只读，头部显示"已声明完成（未验证）"（**绝不**显示"完成 ✓"）。
- **实时**：订阅执行主题；断线显示"正在重连"并保留内容；重连按 BR-03-93 补齐。
- **状态**：加载骨架（头部 + 3 条消息占位）；执行不存在 → 404 页"执行不存在或你无权查看"；无权限 → "需要 execution_read"；只读模式（非发起者）→ 输入区替换为"你正在查看 X 的会话"；终态 → 输入区替换为状态说明 + 〔查看详情〕。
- **移动端**：头部压缩为标题 + 状态 + 〔⋯〕，其余信息在〔⋯〕内"会话信息"；交互卡片吸附在输入框上方；声明弹窗全屏 Sheet；触控目标 ≥44 px；Skill 弹层出现在键盘上方。

### 8.3 执行详情

- **目的**：查看任一执行（尤其 AUTONOMOUS）的状态、日志、事件、产物、证据、验证与上下文。
- **路由**：`/t/:tenantID/p/:projectID/executions/:executionID`。
- **布局**：头部同 8.2（无输入区）；状态说明条（INTERRUPTED：虚线灰条"执行已中断（原因），系统不会自动重放。请先检查工具副作用"+〔恢复〕〔换机器开故障重试分支〕〔标记失败〕；PAUSED：原因 +〔恢复〕；FAILED 且可故障重试：〔重试（故障重试分支）〕）；标签页：概览（时间线、机器与工作目录、分支与基线提交、成本、退出码、相似度信号）/ 日志（尾部 64 KB，〔加载更多〕到 1 MB，〔下载完整日志〕，自动跟随开关）/ 事件（按派发分组，seq、类型、时间；过保留期显示"事件已按保留策略清理，消息与证据仍在"）/ 产物 / 证据（`EvidenceCard` 列表）/ 验证（`VerificationPanel`）/ 上下文（七层与"使用的知识"）/ 分支（`ExecutionBranchTree`，当前节点高亮）。
- **按钮条件**：〔停止〕RUNNING/CREATED 且 BR-03-40；〔暂停〕RUNNING 且 AUTONOMOUS；〔恢复〕PAUSED/INTERRUPTED 且 `execution_start`（INTERRUPTED 弹确认，勾选"我已检查工具副作用"）；〔合并到主分支〕满足 BR-03-85 时可用，不满足时 tooltip 列出未满足项。
- **移动端**：标签页改为分段选择器；日志字号 12 px、横向滚动。

### 8.4 组件：执行分支树 `ExecutionBranchTree`（delivery 应用 `components/`）

- 节点：`#seq · 原因序数`、`StateBadge`、`ActorAvatar`、机器、runtime·模型、成本、迷你完成阶梯（声明 / 验证 n/m）、相似度琥珀标记；INTERRUPTED 节点虚线边框；当前节点高亮。
- 布局：父子纵向缩进；ENSEMBLE 兄弟以紫色并列枝挂在同一组节点下，组节点显示"并行求解 · 3 枝"与〔对比〕；超过 6 层折叠；≤50 节点直接渲染，更多分页。
- 交互：点击节点进入工作台/详情；勾选任意 2–5 个同工作项节点 →〔对比〕打开对比抽屉（A08：验证、证据覆盖、diffstat、成本、时长逐列对比；最优值加粗，不自动"选胜者" —— 选择是 05/02 的裁决）。
- 无障碍：树使用 `role="tree"`，方向键导航；移动端改为可展开列表。

### 8.5 组件：证据来源卡 `EvidenceCard` 与验证面板 `VerificationPanel`（`packages/ui` 新增或 delivery `components/`，按 00 §10）

- **EvidenceCard**：类型图标 + 标题；producer（`ActorAvatar`/机器图标 + 工具版本）；produced_at（相对时间，悬停绝对 UTC）；哈希前 8 位 + 〔复制全值〕；验证器 `key@v3` 与结果徽标（若有）；**局限**（始终可见，灰色斜体，不可折叠）；范围；类型摘要（COMMIT：sha 前 7 位、标题、+/-；TEST_REPORT：通过/失败/跳过；LOG：大小、是否截断；SCREENSHOT：缩略图；OBSERVATION：正文前 3 行 + 展开）；脱敏计数提示"已隐藏 n 处敏感信息"；〔查看内容〕。DRAFT 卡可〔排除〕/〔恢复〕；ATTACHED 卡显示"已附于声明"。
- **VerificationPanel**：按必需/参考分组；每行：层级图标（自动化/AI 辅助"仅参考"/人工）、验证器名 + 版本、结果（PENDING 显示 QUEUED/DISPATCHED/RUNNING 子状态与已等待时长）、原因、工具版本、耗时；展开：exitCode、stdoutTail/stderrTail（等宽、可复制）、报告文件链接、AI findings；顶部汇总"必需 3/4 通过 · 1 不确定"。按钮：〔重跑〕（`verification_run`，非 PENDING 行）；HUMAN 行〔记录结果〕（BR-03-65，自验时禁用并说明"不能验证自己执行的产出"）。

---

## 9. 通知（经 09 `Notifier`；站内信必达，其余渠道按 08 用户偏好）

| # | 事件 | 接收人 | 渠道 | 文案模板（zh-CN） | 去重 / 合并 |
|---|---|---|---|---|---|
| N1 | `interaction.requested` | INTERACTIVE：发起者；AUTONOMOUS：项目内持 `interaction_respond` 的交付负责人 + 工作项 EXECUTOR 的人类负责人 | 站内（我的裁决）+ Web Push / 移动推送 | "{执行标题} 请求{允许工具：{title}/你的输入}，{n} 分钟后过期" | 每 interaction 一条；同一执行 60 s 内多条合并为"{执行} 有 {n} 个待你决定" |
| N1b | 交互到期前 5 min | 同 N1 | 站内 + 推送 | "还有 5 分钟：{title}" | 每 interaction 至多一次 |
| N2 | 执行 → INTERRUPTED | 发起者 / 执行者（人）；AUTONOMOUS 另通知交付负责人 | 站内 + 邮件（工作时间外仅站内） | "{执行} 已中断（{原因}），系统不会自动重放，请检查后恢复或标记失败" | 同一机器离线引发的多条 10 min 内合并为"{机器} 离线，{n} 个执行已中断" |
| N3 | 执行 FAILED（AUTONOMOUS）/ 自动声明失败 / 自动建枝失败（130441） | 交付负责人、工作项 EXECUTOR | 站内 | "{工作项} 的执行 {标签} 失败：{原因}" | 同工作项 10 min 内合并 |
| N4 | PAUSED（AGENT_GAVE_UP / CONTEXT_EXHAUSTED） | 交付负责人 | 站内 | "{Agent} 暂停了 {工作项}：{continue.md 首行}" | 每执行一次 |
| N5 | PAUSED（COST_CAP / BUDGET_PAUSE） | 交付负责人、团队所有者 | 站内 + 邮件 | "{执行} 因成本上限暂停（已用 {cost}）" | 预算暂停按项目合并 |
| N6 | `verification.completed`（必需项 FAILED / INCONCLUSIVE） | 执行者、工作项 REVIEWER | 站内 | "{工作项} 验证{未通过/不确定}：{验证器}（{原因}）" | 同 claim 汇总为一条，全部完成后发送 |
| N7 | HUMAN 验证进入 QUEUED | `eligible_role_keys` 中且非执行者的成员 | 站内（我的裁决） | "请为 {工作项} 记录人工验证：{验证器}" | 每验证一条 |
| N9 | 合并 CONFLICT / FAILED / MERGED | 发起合并者 | 站内 | "合并{成功/冲突/失败}：{分支} → {目标}" | 每合并一次 |

所有通知文案三语齐全；不包含消息正文、代码或工具入参原文。

---

## 10. 验收标准

> 测试层：unit（服务层，假时钟）/ integration（真实数据库三方言 + 夹具执行桥，不调用真实模型）/ e2e（Playwright，桌面 1440 与手机 390 视口）/ manual。时间相关用例一律用假时钟；夹具执行桥输出不得被报告为真实 runtime 结果。

### 10.1 主流程

```
AC-03-01  [integration]  US-03-01, BR-03-03, BR-03-30
Given 工作项 W（FEATURE/MEDIUM）分派给 Agent A，A 绑定机器 M（ONLINE，装有 CLAUDE_CODE）
When  02 调 ExecutionLauncher.Launch{branch_reason:INITIAL}
Then  执行 E 为 CREATED、seq_no=1、branch_label="#1 · 初始"；生成一条 EXECUTION_START 命令，其 context.sha256 = E.context_bundle_hash；
      M 回 execution.accepted 后 E=RUNNING、lease_expires_at=now+30s，execution.started 恰发布一次

AC-03-02  [integration]  US-03-01, US-03-06, BR-03-26, BR-03-50
Given E RUNNING，工作区有 2 个新提交与 1 个 junit.xml
When  夹具桥写入合法 claim.json 并以 exit 0 结束
Then  生成 2 条 COMMIT、1 条 TEST_REPORT、1 条 LOG 证据（服务端 SHA-256）；ClaimSubmitter 被调用一次；E=SUCCEEDED(CLAIM_SUBMITTED)，证据 ATTACHED

AC-03-03  [unit]  BR-03-26
Given E RUNNING   When exited{code:0} 且无 claim.json   Then E=FAILED(CLAIM_INVALID)，不调用 ClaimSubmitter
Given E RUNNING   When exited{code:2} 并上传 continue.md   Then E=PAUSED(CONTEXT_EXHAUSTED)，生成 CONTINUATION 产物；随后 resumptions 的上下文第 3 层含该文件
Given E RUNNING   When exited{code:3}   Then E=PAUSED(AGENT_GAVE_UP) 并发 N4；exit 7 → FAILED(RUNTIME_ERROR)

AC-03-04  [integration]  US-03-02, BR-03-33
Given 工程师 U 为 W 的 EXECUTOR，在 M 上经 A06 创建 INTERACTIVE 会话 S
When  U 发送轮次 {client_request_id:r1, prompt:"hi"}，夹具桥输出文本 3 段、工具 running→completed、turn_ended{completed}
Then  轮次 QUEUED→RUNNING→COMPLETED，S RUNNING→PAUSED(TURN_IDLE)；消息顺序 USER、ASSISTANT（3 段合并为 1 条）、TOOL（同 tool_call_id 只 1 行，状态 completed）

AC-03-05  [integration]  US-03-04, BR-03-31
Given S 的轮次因 TOOL_PERMISSION "Bash: npm test" 处于 WAITING
When  U 在手机端 PUT /interactions/{i}/decision {action:ACCEPT}
Then  交互 DECIDED，写 1 条 INTERACTION_RESPONSE 命令，审计链追加 execution.interaction_responded（不含 content 原文）；
      夹具桥回报 interaction_delivered 后交互 DELIVERED、轮次回到 RUNNING

AC-03-06  [e2e]  US-03-06, BR-03-56
Given S PAUSED、已关联 W（IN_PROGRESS）、W 有 3 条验收标准
When  U 打开〔提交完成声明〕，等待自动采集，映射 2 条标准，填写摘要并确认"有 1 条验收标准没有证据"后提交
Then  02 收到 {execution_id, evidence_ids, criteria_map}；页面头部显示"已声明完成（未验证）"，页面任何位置不出现"完成 ✓"；S=SUCCEEDED(CLAIM_SUBMITTED)

AC-03-07  [integration]  US-03-08, BR-03-60, BR-03-64
Given W 的 Claim C 匹配 2 个必需 AUTOMATED 验证器 V1@v1、V2@v3
When  claim.submitted 被消费，夹具机器回报两者 exit 0
Then  2 条验证 PASSED；建行事务内调用 VerificationQueued(batch_id=C) 一次，最后一个结果的同一事务内调用 VerificationCompleted{required_total:2, passed:2, automated_passed:2} 一次；
      生成的 TEST_REPORT 证据带 verifier_version_id 与 result=PASSED；verification.completed 发布 2 次，第二次 all_required_done=true

AC-03-08  [unit]  BR-03-62, BR-03-64
Given 必需验证器 V   When 机器回报 exit 1 / exit 0 但报告 failed=2 / 超时被杀 / exit 127 / 报告不可解析
Then  分别为 FAILED / FAILED / INCONCLUSIVE(TIMEOUT) / INCONCLUSIVE(COMMAND_NOT_RUNNABLE) / INCONCLUSIVE(REPORT_UNPARSEABLE)；
      VerificationCompleted 的 Summary 计数与之一致；FailedVerifications(C) 返回验证器名与 stdoutTail 前 2000 字符

AC-03-09  [unit]  BR-03-60
Given 无匹配验证器   When FEATURE/LOW 的声明提交   Then 生成 INCONCLUSIVE(NO_REQUIRED_VERIFIER)
When  DOC/LOW 的声明提交   Then 不建验证，直接调用 VerificationCompleted{required_total:0}

AC-03-10  [integration]  US-03-10, US-03-11, BR-03-01, BR-03-03, BR-03-06
Given W 已有 #1（INITIAL，Claim C1）
When  02 在 REJECT 裁决事务内调用 Launch(tx, {branch_reason:QUALITY, parent:#1, workspace_mode:RESTART, feedback_ref:D, autostart:true})，反馈 {FUNCTIONAL_DEFECT, "分页越界"}
Then  同一事务内创建子执行 #2，branch_label="#2 · 质量返工 1"，start_point=RESTART（起点 = #1.base_commit）；#1 及其证据字段无任何变化；
      #2 上下文第 4 层含该反馈且 inclusion=FULL；REQUEST_CHANGES(CONTINUE) 再建的分支为 "#3 · 质量返工 2"，起点 = #2.head_commit；
      裁决事务回滚时 #2 不存在；以同一 feedback_ref 重复调用返回 #2 的 ID
Given autostart=false   Then #2 停在 CREATED、无 START 命令；人工 POST /resumptions 后才派发

AC-03-11  [integration]  US-03-19, BR-03-05
Given 项目主导 Agent L
When  L 调 A07 创建 3 个 ENSEMBLE 分支（不同 Agent/模型），再追加第 4 个
Then  前 3 个共享 ensemble_group_id；第 4 个 409 130439；A08 返回 3 行对比数据，且不自动标记胜者

AC-03-12  [integration]  US-03-03, BR-03-04
Given U 创建 ADHOC 执行 X（无工作项）
When  U 经 02 提交 Claim 引用 X   Then 409 130425
When  U PUT /executions/{X}/work-item-link {work_item_id:W}（W 无执行、U 为 EXECUTOR）
Then  X.kind=INTERACTIVE、branch_reason=INITIAL、seq_no=1；审计 execution.linked；再次关联 → 409 130426

AC-03-13  [integration]  US-03-16, BR-03-85, BR-03-86
Given W ACCEPTED，E SUCCEEDED（WORKTREE），必需验证全 PASSED，head=Claim 提交
When  负责人 POST /executions/{E}/merges {reason}
Then  合并 MERGED；新增 MERGE 产物与 COMMIT 证据（producer=MACHINE）；审计 execution.code_merged；发 execution.merged
And   若某必需验证为 INCONCLUSIVE → 409 130436，meta.unmet 含 "required_verifications"

AC-03-14  [unit]  US-03-12, BR-03-67
Given QUALITY 子执行与父的变更路径并集 |U|=20，仅 1 个路径 blob 不同
When  02 调用 ChangeSimilarity(子)   Then 返回 {ratio:0.05, union_count:20, changed_count:1}，similarity_ratio_ppm=50000、flag=true；不发布任何相似度事件
Given |U|=4 / changed_files 截断 / INITIAL 执行   Then ratio=null，flag=false

AC-03-15  [unit]  US-03-13, BR-03-80, BR-03-81
Given E RUNNING，max_cost_micros=2,000,000
When  机器在 25 s 内回报 6 次 usage（累计 cost_usd 0.3→2.1）
Then  cost_updated 最多发布 3 次（10 s 合并），载荷为累计值；累计达 2,000,000 时下发 CANCEL，确认后 E=PAUSED(COST_CAP)
Given 项目收到 07 budget.threshold_reached{threshold_pct:100}   Then 项目内 RUNNING AUTONOMOUS 执行暂停(BUDGET_PAUSE)，07 ProjectGate 关闭期间新轮次 409 130438
Given 机器回报累计成本   Then execution.cost_updated.data 含 cumulative_cost_micros 与 breakdown{model,compute,platform}，重复发布同值时消费方结果不变

AC-03-16  [unit]  US-03-18, BR-03-28, BR-03-29
Given 预算 2,000 token；层 1+2+4+7 共 1,200；层 3 600、层 5 三条各 300、层 6 400
When  组装上下文包
Then  先把层 3 降为 REFERENCE，再按得分从低到高把层 5 降为 REFERENCE，直到 ≤2,000；层 1/2/4/7 均 FULL；每条 inclusion 写入 context_items
Given 层 1+2+4+7 合计 2,100   Then 创建失败 422 130441
```

### 10.2 崩溃、离线、中断与恢复

```
AC-03-17  [integration]  US-03-14, BR-03-10, BR-03-13
Given E RUNNING（dispatch_epoch=3），机器停止续约
When  假时钟推进 35 s
Then  E=INTERRUPTED(LEASE_EXPIRED)；其 PENDING 交互 CANCELLED；workdir 锁删除；追加 SYSTEM 消息；
      之后机器以 epoch 3 发来的任何事件 → 409 130427，messages 不变

AC-03-18  [integration]  BR-03-11, BR-03-27
Given 夹具机器与服务端网络断开   When 20 s 内无法续约   Then 机器向执行桥发 cancel，4 s 后进程树不存在；outbox 保留
Given 适配器心跳停止 45 s   Then 机器杀进程并报 execution.lost{HEARTBEAT_LOST}，E=INTERRUPTED(HEARTBEAT_LOST)

AC-03-19  [integration]  BR-03-12
Given 2 个 RUNNING 执行，restart_fencing=all   When 服务进程重启   Then 两者 INTERRUPTED(SERVER_RESTART)，各 1 条 N2（同机合并为 1 条）
Given restart_fencing=lease 且机器在 30 s 内持续续约   When 重启   Then 两者保持 RUNNING

AC-03-20  [integration]  US-03-14, BR-03-12, BR-03-36
Given E INTERRUPTED
When  假时钟推进 24 h   Then 不生成任何 EXECUTION_START 命令（不自动重放）
When  POST /resumptions 不带 acknowledge_side_effects   Then 422 130450
When  带 acknowledge_side_effects:true，机器与目录不变   Then dispatch_epoch+1，START 命令含原 native_session_id，审计 execution.resumed_after_interrupt
When  （SHARED_DIR 会话）先换到工作目录不同的机器再发新轮次   Then START 不含 native_session_id，prompt 以 carryover 前缀开头且 ≤24 KiB+本轮提示

AC-03-21  [integration]  BR-03-14, E03, E04
Given START 命令被取走后 60 s 无回应   Then E=INTERRUPTED(DISPATCH_UNCONFIRMED)，同事务调用 ExecutionFailed{kind:RUNTIME}（02 不会自动重试）
Given 命令 60 min 未被取走   Then E=FAILED(DISPATCH_TIMEOUT)，调用 ExecutionFailed{kind:INFRA}；本模块自身不建 RETRY_INFRA 子执行
Given E 因 LEASE_EXPIRED / TIMEOUT / HEARTBEAT_LOST 结束   Then 一律 kind=RUNTIME（参数化断言）

AC-03-22  [integration]  BR-03-15
Given E RUNNING   When 负责人 PUT termination{CANCELLED}，机器 30 s 内无回应   Then E=CANCELLED(USER_CANCEL)、epoch 栅栏，机器迟到事件 409 130427

AC-03-23  [e2e]  US-03-04, BR-03-93
Given U 在桌面发起长轮次   When 关闭浏览器 2 min 后在手机打开同一会话
Then  轮次已在后台完成或仍在进行；消息与待决交互完整显示，无重复消息（按 msg_no 去重）

AC-03-24  [integration]  BR-03-32, §4.3
Given 交互 I PENDING   When 假时钟推进 30 min   Then I=EXPIRED，写 INTERACTION_RESPONSE{CANCEL}，发 interaction.closed；之后对 I 的决定 → 409 130420
Given I DECIDED 未送达   When 执行被中断   Then I=CANCELLED(DELIVERY_FAILED)；随后重试轮次的 START 与新派发中不含 I 的决定

AC-03-25  [integration]  §4.4
Given 验证 V RUNNING 于机器 M   When M 离线 35 s   Then V 重新 QUEUED（attempt 不变）；再次离线 → INCONCLUSIVE(MACHINE_UNAVAILABLE)
Given V QUEUED 30 min 无机器   Then INCONCLUSIVE(MACHINE_UNAVAILABLE)
```

### 10.3 非法状态迁移

```
AC-03-26  [unit]  §4.1
Given 执行处于 SUCCEEDED / FAILED / CANCELLED 之一
When  依次调用 A10/A11/A12/A13/A14/A15/A17/A27/A31
Then  每次 409 130411，状态与 version 不变，不写 outbox 事件（参数化 3×9）

AC-03-27  [unit]  §4.1 非法表
Given RUNNING   When 新轮次 → 409 130412；resumptions → 409 130410；termination{SUCCEEDED} → 409 130410；PATCH machine_id/tools_enabled → 409 130424；EvidenceBinder.Attach → 409 130447
Given CREATED 且 dispatch_epoch>0   When resumptions / pause / termination{SUCCEEDED} → 409 130410（dispatch_epoch=0 且 autostart=false 时 resumptions → 202，E00）
Given PAUSED    When pause → 200 且无变化；termination{SUCCEEDED} 而最后一轮 FAILED → 409 130410
Given INTERRUPTED When pause → 409 130410；新轮次无确认 → 422 130450
Given WORKTREE 会话 PAUSED When PATCH machine_id → 409 130424

AC-03-28  [unit]  §4.1
For   全部 (状态 7 × 命令/事件 21) 组合中不在 E01–E27 的项
Then  返回表内错误码或 409 130410，状态不变（表驱动测试，组合覆盖率 100%）

AC-03-29  [unit]  §4.2, §4.3, §4.4, §4.5
Given 轮次 T2 非当前   When PUT T2 cancellation → 409 130410；当前轮次已 COMPLETED 再取消 → 200 返回 COMPLETED
Given 交互 DECIDED(ACCEPT)   When 再提交 ACCEPT → 200 原结果；提交 DECLINE → 409 130421；对 EXPIRED/CANCELLED/DELIVERED → 409 130420
Given 验证 PASSED   When PUT result → 409 130410；AUTOMATED 验证 PENDING 时 PUT result → 409 130434
Given 证据 ATTACHED  When PATCH → 409 130430；合并已 MERGED 再 POST merges → 409 130436
Given QUALITY 分支请求无 source_decision_id → 422 130440；STRATEGY 经 A15 起点为项目基线而非父 HEAD
```

### 10.4 权限拒绝

```
AC-03-30  [integration]  BR-03-40, BR-03-92
Given 主体无 execution_read   When A01 / A09   Then 403 130403
Given 执行属租户 T2   When T1 用户以全局地址 A09 读取   Then 404 130404

AC-03-31  [integration]  BR-03-40
Given 工程师 U2（engineer）非发起者亦非执行者   When PUT /executions/{S}/termination   Then 403 130403，审计 execution.action_denied(outcome=denied)，S 不变
Given 交付负责人（持 execution_stop + decision_pause）   When 同操作   Then 200
Given Agent B（agent_executor）停止 Agent A 的执行   Then 403 130403
Given U2 向 U 的会话发送轮次   Then 403 130403

AC-03-32  [integration]  BR-03-31
Given AUTONOMOUS 执行的 TOOL_PERMISSION 交互   When 任一 Agent principal（含持委托的 agent_reviewer）提交决定   Then 403 130403，交互保持 PENDING

AC-03-33  [integration]  BR-03-65
Given HUMAN 验证 V 针对 U 执行的 Claim   When U 记录结果   Then 403 130403，data.reason_code=SELF_REVIEW
Given 角色不在 eligible_role_keys 的成员   Then 403 130403

AC-03-34  [integration]  BR-03-37
Given U 持 execution_start   When PATCH authorization_mode=FULL_AUTO 且无 5 min 内 step-up（purpose execution_full_auto）   Then 403 130403，data.reason_code=REAUTH_REQUIRED，模式不变
Given 项目 allow_full_auto=false   Then 403 130403，data.reason_code=FULL_AUTO_FORBIDDEN
Given 完成 step-up   Then 200，审计链追加 execution.full_auto_enabled

AC-03-35  [integration]  BR-03-85
Given 工程师无 code_merge   When A36   Then 403 130403
Given agent_reviewer 持含 merge 的委托，工作项风险 MEDIUM   Then 403 130403，data.reason_code=DELEGATION_MERGE_RISK（08 透传），不创建合并
Given 该 Agent 同时是执行者   Then 403 130403，data.reason_code=SELF_REVIEW

AC-03-36  [integration]  BR-03-41, 00 §6.1
Given 主体无 verifier_manage   When POST /tenants/{t}/verifiers   Then 403 130403
Given 任意主体   When A06 传 kind:"AUTONOMOUS"   Then 422 130422（AUTONOMOUS 不经 HTTP 创建）
```

### 10.5 并发

```
AC-03-37  [integration]  BR-03-31, Preview A04
Given 交互 I PENDING   When 两个客户端并发提交 ACCEPT 与 DECLINE
Then  恰一个 200，另一个 409 130421；只写 1 条 INTERACTION_RESPONSE 命令；两端 ≤1 s 收到同一结果

AC-03-38  [integration]  BR-03-73, Preview A10
Given 机器 M 上 SHARED_DIR 会话 S1 RUNNING（ON_REQUEST）   When 同目录 S2（FULL_AUTO）发送轮次   Then 409 130415
When  S2 为 READ_ONLY   Then 202 正常派发

AC-03-39  [integration]  BR-03-33
Given S.version=7   When 两客户端各以 version=7 PATCH model   Then 一个 200（version=8），另一个 409 130409
Given S RUNNING   When PATCH model=X   Then 200，当前轮 settings_snapshot.model 不变，下一轮 START.model=X

AC-03-40  [integration]  BR-03-03, BR-03-07
Given 两个事务并发对 W 调用 Launch（一个 RETRY_INFRA，一个 A15 人工 STRATEGY）
Then  seq_no 唯一且连续（冲突事务重试 ≤3 次）；BR-03-07 只允许 1 个非终态 DELIVERY 执行，后到者 409 130426，调用方事务回滚

AC-03-41  [integration]  BR-03-90
Given S PAUSED   When 两个不同 client_request_id 的轮次并发提交   Then 一个 202，另一个 409 130412
```

### 10.6 幂等与事件重放

```
AC-03-42  [integration]  BR-03-90, 00 §9
Given 同一 Idempotency-Key 的 A02 请求重复 2 次（响应丢失）   Then 返回同一执行 ID，只建 1 行
Given 同键不同请求体   Then 422 176425（09 平台幂等）；同键首个请求仍在处理中   Then 409 176409
Given 相同 client_request_id 的轮次重复提交   Then 返回同一 turn_id，只生成 1 条 START 命令（Preview A02）

AC-03-43  [integration]  BR-03-21, BR-03-22
Given last_seq=10
When  机器发送 seq 9–11   Then 应用 11，ack_seq=11，seq 9–10 不重复写消息
When  机器发送 seq 14   Then 409 130428 {expected_seq:12}；机器重发 12–14 后全部应用，最终 ASSISTANT 文本与无缺口单次发送逐字节相同
When  ack 响应丢失、机器重发 12–14   Then 200 ack_seq=14，消息不重复

AC-03-44  [integration]  BR-03-23, Preview A07
Given 同一 EXECUTION_START 命令因未 ACK 被下发 2 次   Then 夹具机器只 spawn 1 次，第二次只回报既有状态

AC-03-45  [integration]  BR-03-91
Given decision.made 事件被投递 3 次   Then 本模块不建任何执行（建枝只经 Launch）；以同一 feedback_ref 调用 Launch 3 次 → 只有 1 个 QUALITY 子执行
Given 同一 claim.submitted 投递 2 次   Then 每个验证器只有 attempt=1 一行
Given 同一 execution.lost 重复 2 次   Then 只迁移一次、只发 1 条 N2
```

### 10.7 上限

```
AC-03-46  [integration]  BR-03-34
Given S turn_count=499   When 发送   Then 202；再发送 → 409 130416
Given message_count=4000 或 output_bytes ≥ 8 MiB   When 发送   Then 409 130416
Given 轮次中输出将超过 8 MiB   Then 该批被拒，下发 CANCEL，轮次 FAILED(OUTPUT_LIMIT)，S=PAUSED

AC-03-47  [unit+e2e]  BR-03-34
Given 9 个附件 / 合计 49,153 字节 / 含 \x00 的文件 / 非 UTF-8 文件 / 提示连同附件 65,537 字节
Then  分别 422 130418 ×4 与 413 130417；前端在发送前给出同样提示且保留草稿

AC-03-48  [integration]  §4.3, BR-03-55, BR-03-22
Given 同一派发已有 100 个交互   When 第 101 个 interaction_requested   Then 400 130449，轮次 FAILED
Given Claim 已绑定 50 条证据   When 再绑定 1 条   Then 409 130431
Given 机器 outbox 达 10,000 条   Then 机器报 execution.lost{OUTBOX_FULL}，E=INTERRUPTED

AC-03-49  [unit]  BR-03-05, BR-03-67
Given ENSEMBLE 上限 3 → 见 AC-03-11；changed_files 21,000 条 → truncated=true，ChangeSimilarity 返回 ratio=null
```

### 10.8 环境与令牌泄漏（负向）

```
AC-03-50  [integration]  US-03-15, BR-03-70, BR-03-71
Given 机器以凭证 K 运行，宿主环境另含 MACHINE_CREDENTIAL=K、FOO_TOKEN=x、ELECTRON_RUN_AS_NODE=1
When  派发一个夹具执行桥，其唯一动作是把完整环境与运行目录文件清单写入日志
Then  日志中变量名集合 ⊆ BR-03-70 白名单 ∪ 三个执行变量；任何变量值、运行目录文件、上下文包、START 命令载荷、上传日志中均不含 K 的子串（含 base64/URL 编码形式）

AC-03-51  [integration]  BR-03-71
Given 机器管理员在 04 附加名单加入变量 CI_HELPER，其值包含 K
When  派发   Then execution.rejected{detail:"env_leak"}，E=FAILED(WORKDIR_UNAVAILABLE)，ExecutionFailed 以 kind=RUNTIME 上报（env_leak 排除在 INFRA 外，02 不会自动重试）

AC-03-52  [integration]  BR-03-54, BR-03-52
Given 运行时输出 "Authorization: Bearer abc.def" 与 "sk-AAAAAAAAAAAAAAAAAAAAAAAA"
Then  入库日志与工具详情为 [REDACTED]，redaction_count=2；content_hash 等于脱敏后字节的 SHA-256
Given 机器上传附带哈希与服务端计算不符   Then 422 130432，不建证据
Given 用户经 A27 提交 evidence_type=COMMIT   Then 422 130422
```

### 10.9 验证器版本与 AI 辅助

```
AC-03-53  [integration]  BR-03-66
Given V@v1 的验证正在 RUNNING   When PATCH V.spec   Then V.current_version_no=2；RUNNING 那条仍记录 v1；之后新 Claim 的验证记录 v2；来源卡显示 "key@v2"
Given V ARCHIVED   When A33 重跑   Then 422 130433

AC-03-54  [integration]  BR-03-63
Given W 唯一必需验证器为 AI_ASSISTED，审阅 Agent R ≠ 执行者
When  R 输出 verdict PASSED   Then W 请求迁到 NEEDS_HUMAN_REVIEW；EvidenceReader.Summary.has_non_ai_pass=false
Given spec.agent_id 恰为执行者   Then 验证 INCONCLUSIVE(SELF_REVIEW)

AC-03-55  [manual]  §8.2
Given 真实 Claude Code 与 Codex 各一台机器   When 各跑两轮无工具对话与一次换机器续接
Then  第二轮能复述第一轮验证词；记录实际 runtime 版本与费用（真实模型冒烟单独标记，不计入 CI）
```

---

### 10.10 审计修订补充（契约 v1.3 对齐）

```
AC-03-56  [integration]  E06, BR-03-56, §6.4 EvidenceBinder
Given AUTONOMOUS 执行 E RUNNING，机器回报 exited{code:0} 与合法 claim.json
When  服务端处理退出事件
Then  同一事务内 ValidateClaimable(fromExit=true) 通过、ClaimSubmitter.SubmitOn 成功，E=SUCCEEDED(CLAIM_SUBMITTED)，执行槽与写锁已释放，工作项为 AGENT_CLAIMED_DONE
Given 同一 E 非退出路径（人工经 02 提交声明）且 E 仍 RUNNING   Then 409 130447

AC-03-57  [integration]  E19b, E27, BR-03-14
Given Agent A 的 AUTONOMOUS 执行 E RUNNING
When  05 发布 agent.state_changed{to:CRASHED}，随后 A 自动重启为 ACTIVE
Then  E=INTERRUPTED(AGENT_CRASHED)、ExecutionFailed{kind:RUNTIME}；不生成新的 START 命令；E 不变为 CANCELLED
Given A 被人工 STOPPED   Then A 的非终态执行 CANCELLED(AGENT_STOPPED)；purpose=HANDOVER 的执行除外

AC-03-58  [integration]  E17, E27, C62
Given 机器 M 上 E RUNNING，Agent 绑定 M
When  04 发布 machine.state_changed{REVOKED}，随后 05 发布 agent.state_changed{STOPPED}（两种到达顺序参数化）
Then  E 最终一律 INTERRUPTED(MACHINE_REVOKED)，不是 CANCELLED

AC-03-59  [integration]  BR-03-01, E24
Given 工作项 W 的执行 E INTERRUPTED
When  02 调 Launch(RETRY_INFRA, parent=E)
Then  同一事务内 E=FAILED(SUPERSEDED_BY_RETRY) 并栅栏，新子执行 CREATED；不返回 130426
Given W 已有 DELIVERY 执行而调用方传 INITIAL   Then 422 130440

AC-03-60  [unit]  BR-03-09
Given 交付负责人 L 确认派单，02 以 autostart=true 调 Launch
Then  执行 initiator_kind=SYSTEM，authority_source=DECISION:<assign_decision_id>；L 不出现在 Participants 的发起者中

AC-03-61  [integration]  E02b, BR-03-30a
Given 06 读取已批准 RULE 超时 5 s
When  派发 AUTONOMOUS 执行
Then  E=FAILED(RULES_UNAVAILABLE)，未写 START 命令，ExecutionFailed{kind:INFRA}，通知 N3

AC-03-62  [integration]  BR-03-21, E19
Given 机器 outbox 丢失 seq 12–15，服务端返回 409 130428{expected_seq:12}
When  机器无法补发
Then  机器报 execution.lost{OUTBOX_GAP}，E=INTERRUPTED(HEARTBEAT_LOST)；之后 seq 16 起的事件 409 130427

AC-03-63  [integration]  BR-03-08, BR-03-60, §6.4 CancelExecution
Given ENSEMBLE 组 3 个分支，分支 B1 退出码 0
Then  B1 写 pending_claim，未调用 ClaimSubmitter，必需验证已排队且不回调 02
When  05 选中 B2 并调 ClaimSubmitter(POLICY:<plan_id>)；对 B1、B3 调 CancelExecution(ENSEMBLE_STOPPED)
Then  B2 的验证结果关联到新 Claim（不重跑）；B3（RUNNING）→ CANCELLED(ENSEMBLE_STOPPED)；B1（SUCCEEDED）仅置 archived_at，证据保留；B2 不受取消影响

AC-03-64  [integration]  §5.5 第 4 层, BR-03-30b
Given 工作项 W 已被 REJECT 两次（结构化反馈 F1、F2）
When  组装第三次执行的上下文包
Then  第 4 层含 F2、F1（新在前），context_items 有两条 item_kind=FEEDBACK、citation=[F1]/[F2]；提交后 06 RecordInclusion 收到层 5 条目
Given claim.json.citations=[K3]   Then 退出后 RecordCitations 收到 K3；06 不可用时由 execution.knowledge_sync 重试，执行状态不受影响

AC-03-65  [integration]  BR-03-30c
Given Agent 执行写 output/memory.json 两条（1 条 RULE）
When  E06 提交
Then  06 MemoryWriter.Propose 收到 2 条，带 execution_id；Schema 非法的文件被忽略且执行仍 SUCCEEDED

AC-03-66  [integration]  BR-03-20 ⑤⑦
Given 07 ProjectGate.CanStartExecution=false（项目暂停）   When POST /resumptions   Then 409 130438
Given 工作项 paused_at 非空   When 派发其 CREATED 分支   Then 409 130426；02 人工 RESUME 后 ExecutionLauncher.Resume 派发该分支

AC-03-67  [integration]  BR-03-23
Given 机器 M 的长轮询挂在实例 I2   When 实例 I1 为 M 写入 START 命令并提交
Then  I1 调 MachineChannelNotifier.Wake(M)，M 在 ≤1 s 内取走命令

AC-03-68  [integration]  BR-03-60, §7.2
Given 验证器为 HUMAN 层   When claim.submitted
Then  发布 verification.human_requested{verification_id, eligible_role_keys}；08 我的裁决出现条目

AC-03-69  [integration]  §6.4 ExecutionLauncher, purpose=HANDOVER
Given 05 退役 Agent A，调 Launch{kind:ADHOC, purpose:HANDOVER, project_id:P, agent_id:A}
Then  执行建立且 work_item_id 为空；A 进入 RETIRING 时该执行不被 E27 取消
Given 缺 project_id   Then 422 130422
```


```
AC-03-70  [integration]  关联 BR-03-30b、复审 #15
Given 执行 E 组包时 06 BuildInjection 返回 retrieval_log_id=L
Then  E.retrieval_log_id=L；RecordInclusion(L, items) 被调用；退出后 claim.json.citations=[K3] → RecordCitations(L, ["K3"])
Given BuildInjection 不可用（降级组包）   Then retrieval_log_id 为空，不调用两方法，执行照常

AC-03-71  [integration]  关联 §6.4、复审 #17
Given 工作项 W 有两个非终态 DELIVERY 执行 E1、E2   When 02 取消 W
Then  02 对 E1、E2 各调用一次 CancelExecution，二者 CANCELLED；ExecutionLauncher 不存在按工作项的 Cancel 方法
Given 02 T25 PAUSE   Then 调用 ExecutionLauncher.Pause(tx, W, reason)，运行中执行 → PAUSED（E15）；RESUME 调 Resume(tx, W)
```

---

## 11. 度量与埋点（不得包含代码、消息正文、工具入参、提示词）

| 事件名 | 属性 |
|---|---|
| `execution_created` | kind, purpose, branch_reason, runtime, authorization_mode, workdir_mode, actor_kind |
| `execution_transition` | from, to, reason, kind, duration_in_state_ms |
| `execution_interrupted` | reason, lease_age_ms, resumed_within_ms?（恢复时补记） |
| `turn_completed` | state, duration_ms, tool_calls, input_tokens, output_tokens, retried:bool |
| `interaction_resolved` | kind, state, latency_ms, responder_device:"desktop"|"mobile" |
| `claim_evidence_mapped` | criteria_total, criteria_covered, evidence_count, auto_collected_count |
| `evidence_added` | type, auto:bool, size_bucket, redaction_count |
| `verification_result` | layer, result, result_reason, duration_ms, required（PRD §15） |
| `rework_branch_created` | category, branch_reason, reason_ordinal（PRD §15） |
| `similarity_signal` | ratio_bucket（0–2/2–5/5–8%）, union_bucket |
| `context_bundle_built` | budget, used, trimmed_layers[], knowledge_items_full, knowledge_items_reference |
| `cost_recorded` | cost_source, cost_bucket, tokens_bucket, runtime, model_family |
| `merge_result` | state, authority_kind（role/delegation） |
| `machine_switch` | same_dir:bool, carryover:bool |

---

## 12. 依赖、风险与待决

**依赖**：02（WorkItemReader、WorkItemTransitioner 方法集 §6.9、ClaimSubmitter、Claim 端点与 criteria_map / 祖先证据引用的存储、在裁决事务内调用 `ExecutionLauncher`、自动 RETRY_INFRA 与有界自治暂停；已按 `02-work-review.md` 当前稿对齐，`claim.submitted` 载荷须含 `claim_id, execution_id, head_commit`）；04（通道传输与 `ExecutionDispatchHandler` 接线、`ResolveWorkspace`、执行槽、模型目录与单价、能力检测、附加环境变量名单、连接代次）；05（Agent 资料、`max_context_tokens`、授权模式、主导 Agent 判定、ENSEMBLE 选择与 `CancelExecution` 调用）；06（`Retriever.BuildInjection` / `RecordInclusion` / `RecordCitations`、`SkillCatalog.ForAgent`（第 6 层技能）、`MemoryWriter.Propose`）；07（`allow_full_auto`、`infra_retry_limit`、ENSEMBLE 上限、单工作项预算、`ProjectGate.CanStartExecution`、预算事件 `budget.threshold_reached{threshold_pct}`）；08（AuthorityChecker 含 code_merge 委托条件与 `POLICY:ensemble_branch`、升级规则消费 INCONCLUSIVE 与相似度）；09（JobQueue、outbox、SSE、Notifier、对象存储）；底座 files（需提供"服务端以系统身份写入项目 entity 根并绑定"的能力 —— 见 Q2）、audit、dlock、authn step-up。

**风险**：① 运行时 SDK 事件形态变化 → 适配器契约测试（夹具桥）隔离；② 证据量大（日志）→ 对象存储驱动（09）与保留策略；③ 运行时副作用不可撤销 → 栅栏 + 中断不重放 + UI 强提示；④ token 估算偏差 → 偏保守，并在 `context_bundle_built` 埋点回看。

| # | 待决 | 建议 |
|---|---|---|
| Q1 | ADHOC 是否必须属于项目 | **MVP 必须选项目**（00 §3 `project_id NOT NULL` 与 `RegisterEntity` 行过滤要求）；团队可建一个"内部"项目承载探索 |
| Q2 | 机器上传的证据/日志如何写入 files | 由底座 files 提供系统身份写入 + ResourceBinding 的窄接口（委派模式），execution 不直接写 files 表 |
| Q3 | 多副本部署是否需要启动栅栏 | 默认 `all`；多副本部署配置 `lease`（BR-03-12） |
| Q4 | 团队级执行总览 | MVP 只做项目级列表 + "我的执行"（A43）；跨项目聚合待 08 提供按项目授权的行级过滤后再做 |
| Q5 | 验证失败是否自动建 QUALITY 子执行 | 由 02 决定（REWORK 状态所有者为交付负责人）；本模块只提供系统反馈与 `ExecutionLauncher` |
| Q6 | ON_REQUEST 对 Codex 的支持 | 待 Codex 提供可回传审批的通道后再开放，不绕过官方客户端限制（Preview 结论） |
| Q7 | 相似度最小并集 `\|U\| ≥ 5`（BR-03-67） | **已决**：02 BR-02-27 采纳，MVP 固定 |
| Q8 | INTERRUPTED 上报 `kind=RUNTIME` 后工作项进入 EXECUTION_FAILED，人工恢复同一执行时工作项如何回到 IN_PROGRESS | **已决**：02 T20b（`current_execution_id` 人工恢复，不计重试次数） |

---

## 13. 契约变更请求

全部已由 00 v1.3 合并或取代（2026-10-11 审校），本节保留为记录：

| ID | 对 00 的请求 | 处理 |
|---|---|---|
| CR-03-01 | 时间列类型 | 已并入 00 §3 |
| CR-03-02 | 事件 `interaction.closed`、`execution.merged` | 已并入 00 §7.1 |
| CR-03-03 | 本模块枚举 | 按 00 §3 作为模块内枚举，不进 §5（见 §3.17） |
| CR-03-04 | 权限码 `verifier_manage` | 已并入 00 §6.1 |
| CR-03-05 | 跨模块接口（ExecutionLauncher / EvidenceReader 扩充、EvidenceBinder、ExecutionDispatchHandler、ClaimSubmitter、`decision.made` 不建枝） | 已并入 00 §8；`BudgetGuard` 取消，改用 07 `ProjectGate`；`ClaimSubmitter` 由 02 提供；新增 `ExecutionReader`、`CancelExecution` |
| CR-03-06 | 模块幂等表附加列 | 取代：统一 09 C7 平台幂等表 |
| CR-03-07 | OpenAPI tag 规则 | 已并入 00 §9（路径首段） |
| CR-03-08 | ETag / If-Match / 428 | 已并入 00 §3 |
