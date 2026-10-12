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
| 执行级派发消息（start / cancel / 交互回应 / 证据采集 / 验证 / 合并）、事件流 `seq`、outbox 确认语义 | Agent 资料（系统提示、runtime×model、`max_context_tokens`、技能、授权模式）、主管 Agent 选择 ENSEMBLE 胜者（`ensemble.compared`）→ **05** |
| 注入运行时的**七层上下文包**、`maxContextTokens` 预算与裁剪顺序、"使用的知识"记录 | 检索与注入包生成（`Retriever`）→ **06** |
| 执行环境加固（不注入控制面令牌、环境变量白名单、每执行工作目录/worktree、同目录单写） | 预算阈值与升级规则（`budget.threshold_reached`、`escalation.triggered`）、委托条件判定、"我的裁决"聚合 → **08** |
| 交互会话（Preview 01 全量对齐）：轮次、消息、工具行、TOOL_PERMISSION / RUNTIME_INPUT、重试、换机器、授权模式、Skills、MCP、附件、限额 | 项目阶段、预算设定、交付包 → **07** |
| 产物、证据（类型、自动采集、人工观察、来源字段、局限、内容哈希、文件存储）、验证器定义与版本、验证结果、相似度信号、成本计量、`code_merge` 动作 | 后台任务队列、outbox、SSE、对象存储驱动、通知渠道 → **09**；文件与资源绑定、审计哈希链 → 底座 |
| **远程工作区**（D119）：每个执行的预览会话（服务预览、评审预览、产物预览）与产物查看器、文件浏览（只读）与人工接管后的编辑、终端会话（只读观看 / 交互 shell）、甲方分享链接；预览访问判定 | 隧道传输、端口归属与路径限定的机器端校验、配额、机器类型与远程访问开关、预览网关部署 → **04**；权限码、step-up、审批收件箱、审计目录 → **08** |
| **锚定批注**（D120）：产物查看器内对代码行区间、表格行、HTML 区域、视频时间点 / 区间、文本段落的批注线程，绑定产物版本，版本更新后的重新定位与「锚点版本已过期」；甲方在交付产物上的批注；批注与证据写入的过期上下文判定（`interrupted`） | 批注 → 结构化反馈（裁决）→ **02**；批注 → 缺陷 → **12**；批注 → 甲方拒收项与门户呈现 → **07**（门户接口 01）；Agent 收件箱投影 → **05**；HTML 区域辅助脚本的注入 → **04** 预览网关 |

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
| US-03-19 | 作为主管 Agent，我要对高风险工作项启动 ≤3 个 ENSEMBLE 并行分支并获得对比数据 | v1.1 |
| US-03-20 | 作为团队所有者，我要开启"全自动授权"时被要求重新认证，且该动作写入审计链 | MVP |
| US-03-21 | 作为评审者，我要以**人工验证器**身份记录 PASSED / FAILED / INCONCLUSIVE 与观察 | MVP |
| US-03-22 | 作为交付负责人，执行在 PAUSED（上下文不足 / Agent 放弃 / 成本上限）时我要能恢复或改为新分支 | MVP |

---


### 2.x 用户故事增补（D112 / D113，00 v2.0 §15）

| ID | 故事 | 版本 |
|---|---|---|
| US-03-23 | 作为交付负责人，我要在计划批准时冻结验收测试（可见部分 + 隐藏部分），以便执行者无法通过改测试「通过」 | MVP |
| US-03-24 | 作为团队，我要平台在隔离环境重跑所有确定性检查，以便不依赖执行者贴出的日志 | MVP |
| US-03-25 | 作为团队，我要测试 Agent 与产品验收 Agent 在内部先验收，以便只有内部验收通过的成果才到人面前 | MVP |
| US-03-26 | 作为产品验收 Agent，我要拿到真实交付形态（运行应用、截图、成品文件），以便按需求与评分标准判断而不只看代码 | MVP |
| US-03-27 | 作为团队，我要人或甲方发现内部放行的问题时自动沉淀为经验与评测用例，以便内部质量门越来越准 | MVP |

### 2.y 用户故事增补（D119 远程工作区）

| ID | 故事 | 版本 |
|---|---|---|
| US-03-28 | 作为交付负责人，我要在手机上打开 Agent 正在做的网页 / API 的实时预览，而不需要等它提交完成声明 | MVP |
| US-03-29 | 作为评审人，我要一键预览完成声明对应的那个提交（在隔离副本里重新启动），以便看到的就是要裁决的版本 | MVP |
| US-03-30 | 作为评审人，我要直接查看执行产出的文档、图片、PDF 与测试报告，不必下载 | MVP |
| US-03-31 | 作为评审人，我要观看视频产物的低清代理版本与小说章节（含与上一版的差异） | v1.1 |
| US-03-32 | 作为工程师，我要只读浏览执行工作区的文件与差异，密钥文件自动遮罩 | MVP |
| US-03-33 | 作为工程师，我要只读观看 Agent 正在运行的命令输出 | MVP |
| US-03-34 | 作为工程师，我要在暂停或接管执行后编辑文件并保存为一次署名为我的提交 | v1.1 |
| US-03-35 | 作为团队所有者，我要允许经过重新认证的人类打开交互式终端，全程录制进审计 | v1.1 |
| US-03-36 | 作为交付负责人，我要在人工批准后把某个预览以只读、有期限、可撤销的链接分享给甲方 | 分期待定（D119 未列入分期，见 §12 Q9） |

### 2.z 用户故事增补（D120 锚定批注与过期上下文写入）

| ID | 故事 | 版本 |
|---|---|---|
| US-03-37 | 作为评审人，我要在产物查看器里直接对代码行区间、表格行、网页上的某个区域、文档段落发起批注线程，以便精确指出问题而不是在别处描述位置 | MVP |
| US-03-38 | 作为评审人，我要对视频的某个时间点 / 区间与小说章节的段落批注 | v1.1（随视频与章节查看器） |
| US-03-39 | 作为评审人，我要把一条批注一键转为结构化反馈或缺陷，且保留锚点与它所针对的产物版本，以便返工的人能回到原处 | MVP |
| US-03-40 | 作为工程师，我要在产物更新后看到哪些批注仍对得上、哪些已重新定位、哪些「基于旧版本」，以便不被过期批注误导 | MVP |
| US-03-41 | 作为交付负责人，我要 Agent 在工作项已有更新事件时提交证据或发起批注被拒为 `interrupted`，以便它先看到新反馈再动手 | MVP |

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


### 3.19 执行表增补字段（D112 / D113，00 v2.0 §15）

`execution_executions` 增加：

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| model_family | STR(32) | N | | 执行所用模型家族（如 `anthropic` / `openai` / `google` / `local:<name>`），派发时由 04 模型目录解析并快照，跨模型家族规则据此判定 |
| verification_stage | ENUM `VerificationStage` | Y | NULL | 仅 `purpose ∈ {INTERNAL_TEST, INTERNAL_ACCEPTANCE}` 时为 `AGENT_TEST` / `AGENT_ACCEPTANCE` |
| subject_execution_id | ID | Y | NULL | 内部验收执行所验收的那次交付执行 |
| acceptance_suite_id | ID | Y | NULL | 本执行适用的冻结验收集 |

`ExecutionPurpose` 增加 `INTERNAL_TEST`（测试 Agent 执行）、`INTERNAL_ACCEPTANCE`（产品验收 Agent 执行）；二者不进分支树、不能提交完成声明、不得写入交付工作区（工作区以只读快照挂载，测试 Agent 新增的测试写入其自己的输出目录，经 BR-03-97 合入）。

### 3.20 `execution_acceptance_suites`（验收集，`version` 乐观锁）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id / work_item_id | ID | N | | 工作项级；需求级验收集可被多个工作项引用（`work_item_id` 可空时为需求级） |
| requirement_version_no | INT | N | | 冻结时的需求基线版本 |
| state | STR(16) | N | `DRAFT` | DRAFT / FROZEN / SUPERSEDED |
| visible_files | JSON | N | `[]` | `[{path, sha256, file_node_id}]`，复制进执行者工作区，执行者只读 |
| hidden_files | JSON | N | `[]` | `[{path, sha256, file_node_id}]`，**永不进入执行者工作区**，仅在平台副本中运行 |
| command | JSON | N | | 运行命令数组（不经 shell） |
| frozen_at / frozen_by | 时间 / ID | Y | | 计划批准时冻结（02 `plan.approved`）；冻结人须有 `acceptance_suite_manage` 或为计划批准动作本身 |
| superseded_by | ID | Y | | 范围变更生成新版本时指向新集 |

### 3.21 `execution_internal_qa_runs`

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id / work_item_id | ID | N | | |
| subject_execution_id | ID | N | | 被验收的交付执行 |
| stage | ENUM `VerificationStage` | N | | AUTOMATED / AGENT_TEST / AGENT_ACCEPTANCE |
| qa_execution_id | ID | Y | | AGENT_* 阶段对应的内部验收执行 |
| agent_id / model_family | ID / STR(32) | Y | | AGENT_* 阶段的执行主体与模型家族 |
| family_fallback | BOOL | N | false | 未能满足「不同模型家族」而退化为不同模型时为 true（05 BR-05-109） |
| state | STR(16) | N | `PENDING` | PENDING / RUNNING / PASSED / FAILED / INCONCLUSIVE / ERROR |
| waiting_reason | STR(32) | Y | NULL | PENDING 时的等待原因，目前仅 `TOOL_UNCALIBRATED`（BR-03-103） |
| findings | JSON | N | `[]` | `[{criterion_id?, severity: BLOCKER\|MAJOR\|MINOR, summary, evidence_ids[]}]` |
| screenshots | JSON | N | `[]` | `[{file_node_id, viewport, route, sha256}]` |
| rubric_scores | JSON | Y | | 产品验收 Agent 按模板评分标准的逐项得分（11） |
| started_at / ended_at | 时间 | Y | | |

索引：`(work_item_id, subject_execution_id, stage)`；`(tenant_id, state, stage)`。

### 3.22 证据包增补

`execution_evidence_collections` 增加 `unverified JSON NOT NULL DEFAULT '[]'`：`[{criterion_id?, what, reason}]`，**必填**（可为空数组，不可缺省）；Claim 提交（02 `ClaimSubmitter`）时一并写入；评审台与内部验收面板显著展示。

### 3.23 验证器类型增补

`execution_verifiers.kind` 增加：`VISUAL`（以无头浏览器打开交付物指定路由，按视口截图，检查布局约束：元素尺寸一致性、溢出、对齐、对比度；产出截图证据）与 `INTERACTION`（脚本化交互流程：按键 / 点击序列后断言 DOM 或截图）。二者属确定性检查（AUTOMATED 阶段），由平台在隔离副本执行；截图同时作为产品验收 Agent 的输入。截图必须按请求的视口真实排版：页面看到的视口宽高等于请求值，截图像素尺寸也等于请求值；浏览器最小窗口宽度以下的视口须以设备模拟或同尺寸框架渲染，不得以「更宽排版再裁切」代替（由 11 `probe_viewport_fidelity` 探针校验，BR-03-103）。

### 3.24 `execution_preview_sessions`（D119，`version` 乐观锁）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id | ID | N | | |
| execution_id | ID | N | | SERVICE / ARTIFACT：被预览的执行；REVIEW：声明所属执行 |
| claim_id | ID | Y | | 仅 REVIEW |
| kind | ENUM `PreviewSessionKind` | N | | SERVICE（执行工作区内的运行服务）/ REVIEW（声明提交的隔离副本，BR-03-107）/ ARTIFACT（只读文件流，不启动服务） |
| origin | ENUM `PreviewOrigin` | N | | PLATFORM（按预览配置由平台启动）/ AGENT_OFFERED（Agent 进程树内已监听的端口，BR-03-113）/ FILES（ARTIFACT） |
| config_key | STR(64) | Y | | 预览配置项 key（PLATFORM） |
| config_snapshot | JSON | Y | | `PreviewConfig` 快照（S8，§3.28） |
| port | INT | Y | | 机器回环端口（SERVICE / REVIEW） |
| commit | STR(64) | Y | | REVIEW：声明的 `head_commit` |
| machine_id | ID | N | | |
| holder_kind / holder_id | STR(16) / ID | N | | 隧道持有者：EXECUTION=execution_id；PREVIEW=本会话 id（REVIEW 占槽，04 `SlotHolderKind=PREVIEW`） |
| label | STR(26) | N | | 预览子域标签，26 位 base32 小写（≥128 bit 随机，BR-03-111）；UNIQUE |
| state | ENUM `PreviewSessionState` | N | `REQUESTED` | §4.y |
| stop_reason | ENUM | Y | | USER, IDLE, MAX_LIFETIME, EXECUTION_ENDED, CLAIM_DECIDED, ACCESS_DISABLED, MACHINE_OFFLINE, HEALTH_FAILED, START_TIMEOUT, PROCESS_EXITED, PORT_LOST |
| started_by | ID | N | | 发起主体（人或系统） |
| ready_at / last_access_at / expires_at / ended_at | 时间 | Y | | `expires_at` = 创建 + 最长存活（BR-03-108） |
| failure_detail | STR(500) | Y | | 启动日志末 20 行摘要（脱敏） |
| version | INT | N | 1 | |

索引：(`execution_id`,`state`)；(`state`,`last_access_at`)（空闲回收）；(`state`,`expires_at`)；UNIQUE(`label`)。

### 3.25 `execution_share_links`（D119）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id / execution_id / preview_session_id | ID | N | | |
| state | ENUM `ShareLinkState` | N | `PENDING_APPROVAL` | §4.y |
| requested_by / decided_by / revoked_by | ID | N / Y / Y | | 申请人、批准或驳回人（人类）、撤销人 |
| note | STR(500) | Y | | 给审批人与甲方看的说明（甲方页只显示这一项） |
| ttl_hours | INT | N | 24 | 1–168 |
| token_hash | HASH | Y | | 批准时生成 32 字节随机令牌，只存 SHA-256；UNIQUE |
| approved_at / expires_at / revoked_at / ended_at | 时间 | Y | | `expires_at` = 批准时刻 + `ttl_hours` |
| end_reason | ENUM | Y | | EXPIRED, REVOKED, REJECTED, SESSION_ENDED |
| access_count / last_access_at | INT / 时间 | N / Y | 0 | 访问计数（每 IP 每小时去重） |
| version | INT | N | 1 | |

### 3.26 `execution_terminal_sessions`（D119）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id / execution_id / machine_id | ID | N | | |
| mode | ENUM `TerminalMode` | N | | WATCH（只读观看，MVP）/ SHELL（交互，v1.1） |
| principal_id | ID | N | | 打开者（SHELL 必须为人类） |
| state | ENUM `TerminalSessionState` | N | `OPENING` | §4.y |
| sandboxed | BOOL | Y | | SHELL：机器端是否有目录沙箱（04 BR-04-96） |
| recording_file_id | ID | Y | | SHELL：asciicast v2 录制文件（files purpose `terminal_recording`） |
| recording_sha256 | HASH | Y | | 关闭时计算，写入审计 |
| opened_at / last_input_at / closed_at | 时间 | Y | | |
| close_reason | ENUM | Y | | USER, IDLE, MAX_DURATION, EXECUTION_ENDED, ACCESS_DISABLED, MACHINE_OFFLINE, STREAM_RESET |

### 3.27 执行表增补字段（D119）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| taken_over_by | ID | Y | | 人工接管者（人类，v1.1，BR-03-126）；非空时 Agent 不得继续该执行 |
| taken_over_at | 时间 | Y | | |

人工保存的编辑写入 `execution_artifacts`（`kind=COMMIT`，`producer_kind=HUMAN`，`producer_id=taken_over_by`），不新建表。

### 3.28 模块内枚举增补（D119，不进 00 §5）

| 枚举 | 值 |
|---|---|
| `PreviewSessionKind` / `PreviewOrigin` | SERVICE, REVIEW, ARTIFACT / PLATFORM, AGENT_OFFERED, FILES |
| `PreviewSessionState` | REQUESTED, STARTING, READY, STOPPED, FAILED |
| `PreviewKind`（预览配置的 kind） | WEB, API |
| `ViewerKind` | WEB_DEVICE（设备框网页）, OPENAPI（接口控制台）, DOCUMENT（Markdown / 纯文本 / HTML 报告）, IMAGE, PDF, REPORT（测试报告）, VIDEO（v1.1）, CHAPTER（v1.1） |
| `ShareLinkState` | PENDING_APPROVAL, ACTIVE, REJECTED, EXPIRED, REVOKED |
| `TerminalMode` / `TerminalSessionState` | WATCH, SHELL / OPENING, OPEN, CLOSED |

**JSON Schema S8 `PreviewConfig`**：`{key: ^[a-z0-9-]{1,64}$, kind: "WEB"|"API", start: string[1..64]（argv，不经 shell）, cwd?: 相对工作区路径, port: 1024–65535, health_path: 以 / 开头 ≤256（默认 "/"）, env?: {名: 值}（名须在 04 `env_passthrough_names` 或本表声明，值不得含密钥，≤20 项）, openapi_path?: string（API 时 OpenAPI 文档路径）, start_timeout_s?: 10–600（默认 120）, devices?: ["DESKTOP"|"TABLET"|"PHONE"]}`。

### 3.29 `execution_comment_threads`（锚定批注线程，D120，`version` 乐观锁）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id | ID | N | | |
| execution_id | ID | N | | 产物所属执行（批注按执行的 `execution_read` 授权） |
| work_item_id | ID | Y | | 冗余（执行关联的工作项），用于工作项流与评审台汇总 |
| artifact_kind | ENUM `CommentArtifactKind` | N | | WORKSPACE_FILE（执行工作区文件，A63 / A64）/ EVIDENCE_FILE（证据文件，A30）/ ARTIFACT（`execution_artifacts` 行）/ PREVIEW_PAGE（服务预览或评审预览中的页面） |
| artifact_ref | JSON | N | | S10：`{path?, evidence_id?, artifact_id?, preview_session_id?, url_path?}`（按 kind 必填其一组；`path` 相对工作区，经规范化） |
| version_kind | ENUM `CommentVersionKind` | N | | COMMIT（已提交文件 / 评审预览）/ CONTENT_SHA256（证据、未提交文件、产物）/ HEAD_COMMIT（运行中服务预览：批注时执行的 `head_commit` + `uncommitted` 标记） |
| version_ref | STR(64) | N | | commit sha 或内容 sha256；**批注永远绑定它所针对的版本** |
| version_uncommitted | BOOL | N | false | HEAD_COMMIT 且工作区有未提交改动 |
| viewer_kind | ENUM `ViewerKind`（§3.28） | N | | 批注时使用的查看器；另含 CODE（文件 Tab 代码视图 / 差异视图） |
| anchor_kind | ENUM `AnchorKind`（00 §5） | N | | CODE_LINES / TABLE_ROW / HTML_REGION / MEDIA_TIME / TEXT_PARAGRAPH |
| anchor | JSON | N | | S9（§3.31） |
| anchor_excerpt | STR(2000) | Y | | 锚定内容的文本快照（代码行、表格行、段落原文、HTML 区域的可见文本）；遮罩文件不可批注（BR-03-135） |
| anchor_snapshot_file_id | ID | Y | | HTML_REGION 的区域截图（机器有 `browser_capture` 时生成，files purpose `comment_anchor`） |
| anchor_status | ENUM `AnchorStatus` | N | `CURRENT` | 相对 `anchor_checked_version` 的状态：CURRENT / RELOCATED / OUTDATED（BR-03-137） |
| anchor_checked_version | STR(64) | Y | | 最近一次判定所针对的版本 |
| relocated_anchor | JSON | Y | | RELOCATED 时在新版本中的锚点（S9） |
| visibility | ENUM `CommentVisibility`（00 §5） | N | `INTERNAL` | INTERNAL（团队）/ CLIENT（甲方在交付产物上创建，BR-03-141） |
| delivery_id / delivery_item_id | ID | Y | | CLIENT 必填：所在交付与交付项（07） |
| state | ENUM `CommentThreadState` | N | `OPEN` | OPEN / RESOLVED |
| resolved_by / resolved_at | ID / 时间 | Y | | |
| created_by_kind / created_by | ENUM / ID | N | | HUMAN / AGENT（principal）/ CLIENT（`intake_contact_id`） |
| comment_count / last_seq / last_comment_at | INT / BIGINT / 时间 | N / N / Y | 0 / 0 | `last_seq` = 线程内批注序号（`comment_thread` 写入流，BR-03-140） |
| version | INT | N | 1 | |

索引：(`execution_id`, `state`)；(`tenant_id`, `work_item_id`, `state`)；(`execution_id`, `artifact_kind`, `version_ref`)；(`delivery_id`) WHERE `visibility='CLIENT'`。

### 3.30 `execution_comments`（线程内批注，只追加 + 编辑 / 撤回列）与 `execution_comment_conversions`

`execution_comments`：

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| thread_id | ID | N | | UNIQUE(`thread_id`, `seq`) |
| seq | BIGINT | N | | 线程内单调，从 1 起；在线程行锁内分配 |
| author_kind / author_id | ENUM / ID | N | | HUMAN / AGENT / CLIENT |
| body | STR(8000) | N | | 纯文本 + `<@principal_id>` 提及令牌（规则同 05 BR-05-84；CLIENT 不可提及、≤2000） |
| mention_principal_ids | JSON | N | `[]` | ≤20 |
| edited_at / edit_count | 时间 / INT | Y / N | / 0 | 作者 15 min 内、≤10 次 |
| retracted_at | 时间 | Y | | 撤回不物理删除 |
| created_at | 时间 | N | | |

`execution_comment_conversions`（批注 → 下游对象，只追加）：`id`、`thread_id`、`target_kind ENUM CommentConversionTarget`（FEEDBACK（02 裁决的结构化反馈）/ DEFECT（12 缺陷）/ REJECTION_ITEM（07 甲方拒收项））、`target_id`（02 `delivery_decision_feedback` 行 / 工作项 id / 07 `delivery_acceptance_items` 行）、`anchor_snapshot JSON`（转换时的 `{anchor_kind, anchor, version_kind, version_ref, artifact_ref, anchor_excerpt}`，不随线程后续变化）、`converted_by_kind / converted_by`、`converted_at`；UNIQUE(`thread_id`, `target_kind`)。

### 3.31 S9 锚点 Schema 与模块内枚举增补（D120，不进 00 §5，`AnchorKind` / `CommentVisibility` 除外）

**S9 `Anchor`**（按 `anchor_kind`，`additionalProperties:false`）：

| anchor_kind | 字段 | 适用查看器 | 版本 |
|---|---|---|---|
| CODE_LINES | `{path, start_line ≥1, end_line ≥start_line（跨度 ≤500）, side?:"NEW"\|"OLD"（差异视图）, start_col?, end_col?}` | CODE（文件 / 差异）、DOCUMENT 纯文本 | MVP |
| TABLE_ROW | `{path, row_index ≥0（不含表头）, row_key?:{column, value≤200}（有主键列时）, row_sha256, columns?:[name]≤20}` | DOCUMENT 中的 CSV / TSV / JSON 数组表格、REPORT 中的表格行 | MVP |
| HTML_REGION | `{url_path ≤2000, selector?（CSS ≤512，辅助脚本可用时）, rect:{x, y, w, h}（CSS 像素，相对文档左上角）, viewport:{w, h, device:"DESKTOP"\|"TABLET"\|"PHONE", dpr}, scroll:{x, y}, text_quote?≤200, precision:"SELECTOR"\|"RECT_ONLY"}` | WEB_DEVICE、REPORT、PDF（rect 相对页，另含 `page`） | MVP |
| MEDIA_TIME | `{path, start_ms ≥0, end_ms?（区间，> start_ms）, duration_ms, proxy_profile?:"480p"}` | VIDEO | v1.1 |
| TEXT_PARAGRAPH | `{path, paragraph_index ≥0, paragraph_sha256, quote:{exact≤500, prefix?≤64, suffix?≤64}, chapter_key?}` | DOCUMENT（Markdown / 纯文本）为 MVP；CHAPTER（小说章节）为 v1.1 | MVP / v1.1 |

| 枚举 | 值 |
|---|---|
| `CommentArtifactKind` / `CommentVersionKind` | WORKSPACE_FILE, EVIDENCE_FILE, ARTIFACT, PREVIEW_PAGE / COMMIT, CONTENT_SHA256, HEAD_COMMIT |
| `AnchorStatus` | CURRENT, RELOCATED, OUTDATED |
| `CommentThreadState` / `CommentConversionTarget` | OPEN, RESOLVED / FEEDBACK, DEFECT, REJECTION_ITEM |
| `ViewerKind` 增补 | CODE（文件 Tab 代码视图与差异视图） |

### 3.32 执行表增补字段（D120 团队工具集）

`execution_executions` 增加列：`toolset JSON NULL`——S11 工具集快照，派发时由 05 `ToolsetResolver.ResolveOn` 解析后写入，**同一派发内不可变**（`{revision, entries:[{source:"CATALOG"|"MACHINE_LOCAL", kind:"MCP_SERVER"|"SKILL", entry_key, version_no?, content_sha256?, transport?}], warnings[]}`，≤64 KB，不含配置与凭据）；`toolset_revision STR(64) NULL`（= `toolset.revision`，便于按版本检索）。返工 / 重试分支是新的派发，各自记录。
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


### 4.x 内部验收与验收集状态（D112 / D113，00 v2.0 §15）

**验收集**：DRAFT →（02 `plan.approved` 或人工 `PUT state=FROZEN`）→ FROZEN →（02 范围变更产生新版本）→ SUPERSEDED。FROZEN 后 `visible_files` / `hidden_files` / `command` 不可改（409 **130451**）。

**内部验收运行**（每个交付执行的每个阶段一条）：

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| Q1 | — | 交付执行 SUCCEEDED 且已提交声明 | — | AUTOMATED: RUNNING | 平台在隔离副本重跑确定性检查（BR-03-95）与隐藏验收（BR-03-96） |
| Q2 | AUTOMATED RUNNING | 全部通过 且 无测试 diff 违规 | | PASSED | `internal_qa.passed{stage:AUTOMATED}` → 05 派测试 Agent |
| Q3 | AUTOMATED RUNNING | 任一失败 / 测试 diff 违规 | | FAILED | `internal_qa.failed`；违规另发 `cheating.suspected`；02 → REWORK（内部，不通知人） |
| Q4 | — | 05 派单生效 | | AGENT_TEST: RUNNING | Launch(purpose=INTERNAL_TEST) |
| Q5 | AGENT_TEST RUNNING | 无 BLOCKER/MAJOR 发现 | | PASSED | 测试 Agent 新增测试经 BR-03-97 合入；→ 05 派产品验收 Agent |
| Q6 | AGENT_TEST RUNNING | 有 BLOCKER/MAJOR | | FAILED | 发现作为结构化反馈 → 02 REWORK |
| Q7 | — | 05 派单生效 | | AGENT_ACCEPTANCE: RUNNING | Launch(purpose=INTERNAL_ACCEPTANCE)，输入含截图 |
| Q8 | AGENT_ACCEPTANCE RUNNING | 逐条验收标准通过 且 评分达模板阈值 | | PASSED | `internal_qa.passed{stage:AGENT_ACCEPTANCE}` → 02 L2 达成 → NEEDS_HUMAN_REVIEW |
| Q9 | AGENT_ACCEPTANCE RUNNING | 任一验收标准不通过 / 评分不足 | | FAILED | 反馈 → 02 REWORK |
| Q10 | 任一 RUNNING | 执行故障 / 超时 | | ERROR | 不计内部轮次；重派一次，仍 ERROR → 升级（`INTERNAL_ROUNDS_EXHAUSTED` 之外的执行故障按 02 T19） |
| Q11 | AGENT_* RUNNING | 判断无法得出结论（证据不足） | | INCONCLUSIVE | 视同 FAILED 返工一次；连续两次 INCONCLUSIVE → 升级人工（`MANDATORY_HUMAN_GATE`，detail=`INTERNAL_QA_INCONCLUSIVE`） |

非法：对 PASSED / FAILED 的运行再写结果 → 409 **130452**；INTERNAL_* 执行试图提交完成声明 → 409 **130453**。

### 4.y 预览、分享与终端（D119）

**预览会话**（`PreviewSessionState`）：

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| V1 | — | A56 / A57 / 系统（模板 `auto_preview`） | 执行非终态（SERVICE）或声明未裁决（REVIEW）；机器 ONLINE、WebSocket 可用、`allow_remote_preview`；配额（BR-03-110） | REQUESTED | 生成 `label`；PLATFORM：下发 `preview.start`；REVIEW：先 `ReserveSlot(holder=PREVIEW)` |
| V2 | REQUESTED | 机器 `preview.state{STARTING}` | | STARTING | |
| V3 | STARTING | 健康检查 `health_path` 返回 2xx/3xx（经隧道） | 在 `start_timeout_s` 内 | READY | `ready_at`；SSE |
| V4 | REQUESTED | AGENT_OFFERED：端口在 04 `ports.report` 中且开流成功 | | READY | 直接就绪（不启动进程） |
| V5 | REQUESTED | ARTIFACT | 工作区存在 | READY | 不启动进程 |
| V6 | STARTING | 超时 / 进程退出 | | FAILED（START_TIMEOUT / PROCESS_EXITED） | `failure_detail`；释放 REVIEW 槽 |
| V7 | READY | 连续 3 次健康检查失败（每 30 s）或进程退出 / 端口失去归属 | | FAILED（HEALTH_FAILED / PROCESS_EXITED / PORT_LOST） | 04 `ResetHolder`；下发 `preview.stop` |
| V8 | REQUESTED / STARTING / READY | 用户停止（A59）/ 空闲 30 min / 到 `expires_at` / 执行终态或 INTERRUPTED / 声明已裁决（REVIEW）/ `machine.access_changed{allow_remote_preview:false}` / 机器离线 | | STOPPED（对应 `stop_reason`） | 同 V7 副作用；关联 ACTIVE 分享链接 → EXPIRED（SESSION_ENDED） |

终态 STOPPED、FAILED；重新预览 = 新会话（新 `label`，旧链接失效）。

**分享链接**（`ShareLinkState`）：PENDING_APPROVAL →（A72 APPROVE，人类、`preview_share_approve`、非申请人）→ ACTIVE；PENDING_APPROVAL →（A72 REJECT / 会话结束 / 72 h 无人处理）→ REJECTED；ACTIVE →（到 `expires_at`）→ EXPIRED；ACTIVE →（A73）→ REVOKED；ACTIVE →（所属会话 STOPPED / FAILED）→ EXPIRED（SESSION_ENDED）。终态不可再迁移（409 **130465**）。

**终端会话**（`TerminalSessionState`）：OPENING →（04 `tunnel.opened`）→ OPEN →（用户关闭 / 空闲 / 最长时长 / 执行终态 / 开关关闭 / 机器离线 / 流 RESET）→ CLOSED。SHELL 关闭时封存录制文件并写审计（BR-03-125）。

**人工接管**（v1.1）：`taken_over_by` 为空 →（A65 PUT，执行 PAUSED 或 INTERRUPTED；RUNNING 时先在同一请求内暂停，`state_reason=USER_PAUSE`）→ 非空 →（A65 DELETE 释放 / 执行终态）→ 空。接管期间 A13 恢复（交还 Agent）须先释放（409 **130466**）。

### 4.z 批注线程与锚点状态（D120）

**线程 `CommentThreadState`**

| # | 当前 | 事件 / 命令 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| CT1 | — | A77 新建（或 07 经 `ArtifactComments.CreateClientOn` 建 CLIENT 线程） | BR-03-133…136、BR-03-141 | OPEN | 首条批注 seq=1；02 `WorkItemActivityRecorder.RecordOn(COMMENT_ADDED)`（有工作项时）；`comment.thread_created`；审计 `execution.comment_thread_created` |
| CT2 | OPEN / RESOLVED | A79 回复 | 同 BR-03-134；CLIENT 线程团队不可回复（130479） | 不变（RESOLVED 上回复 → 重新 OPEN） | seq+1；`comment.posted`；工作项活动 |
| CT3 | OPEN | A80 `{state:RESOLVED}` | 线程创建者、`work_item_write` 或执行者本人；If-Match | RESOLVED | `comment.thread_resolved`；审计 |
| CT4 | RESOLVED | A80 `{state:OPEN}` | 同上 | OPEN | 同上 |
| CT5 | OPEN / RESOLVED | 下游转换（02 D1 / 12 A31 / 07 回执）经 `ArtifactComments.MarkConvertedOn` | 同一 `target_kind` 未转换过；CLIENT 线程只能转为 REJECTION_ITEM，INTERNAL 线程不能转为 REJECTION_ITEM | 不变；FEEDBACK / DEFECT 转换后若线程 OPEN 则自动 RESOLVED | 写 `execution_comment_conversions`（锚点快照）；`comment.converted`；审计 |

非法：同一 `target_kind` 重复转换 → 409 **130478**（`data.target_id` 为已有对象，便于调用方幂等处理）；对 CLIENT 线程回复或在团队侧解决 → 409 **130479**。

**锚点 `AnchorStatus`**（相对「正在查看的版本」，BR-03-137）：CURRENT（查看版本 = `version_ref`）→ RELOCATED（版本不同但锚点可映射，`relocated_anchor` 指向新位置）/ OUTDATED（无法映射，显示「基于旧版本」）。RELOCATED 在再下一个版本上重新判定；任何状态都可经〔查看原版本〕回到 `version_ref`。

## 5. 业务规则与边界情况

### 5.1 分支、序号与种类

- **BR-03-01 分支原因约束**：INITIAL：无父，且工作项尚无 DELIVERY 执行；RETRY_INFRA：父必填且为 FAILED / INTERRUPTED / CANCELLED / PAUSED —— 父为 INTERRUPTED 或 PAUSED 时，`Launch` 在同一事务内先把父置 FAILED（SUPERSEDED_BY_RETRY，栅栏其 epoch），再建子执行，避免与 BR-03-07 冲突；QUALITY：父必填，`source_decision_id` 必填且该 Decision 为 REJECT / REQUEST_CHANGES、指向父的 Claim；STRATEGY：父可选（被替代者），**起点为项目基线而非父 HEAD**；ENSEMBLE：无父，`ensemble_group_id` 必填；SCOPE：父必填，`source_change_record_id` 必填。违反 → 422 130440。**调用方约束**：重新派单（租约回收、Agent 崩溃、机器吊销、改派）时，若工作项已有 DELIVERY 执行，调用方（02 T03/T07）必须用 `branch_reason=RETRY_INFRA`、`parent=最近的 DELIVERY 执行`；仅工作项无任何 DELIVERY 执行时用 INITIAL。ADHOC（含 `purpose=HANDOVER`）无分支原因，须给出 `project_id`。
- **BR-03-02 起点**：INITIAL / STRATEGY / ENSEMBLE → `BASELINE`（项目基线分支，04 `ResolveWorkspace`）；QUALITY 按 02 传入的 `workspace_mode`：REJECT 默认 `RESTART`（父的 `base_commit`，重开工作区）、REQUEST_CHANGES 默认 `CONTINUE`（父 `head_commit`）；SCOPE / RETRY_INFRA → `CONTINUE`（父无提交时退化为父 `base_commit`）。父执行及其产物、证据**永不修改**（PRD I5）。
- **BR-03-03 序号**：创建事务内 `seq_no = max(seq_no)+1`（同工作项）、`reason_ordinal = count(同 reason)+1`；唯一冲突时整事务重试 ≤3 次，仍失败 → 409 130409。标签 `#{seq_no} · {原因}{ordinal}`；INITIAL 不带序数（"#1 · 初始"）；原因中文：初始 / 故障重试 / 质量返工 / 策略替代 / 并行求解 / 范围变更。
- **BR-03-04 ADHOC 关联**（PRD Q8）：`PUT /executions/{id}/work-item-link` 守卫：调用者是该执行发起者且为工作项当前 EXECUTOR 分派者；工作项 ∈ {CLAIMED, IN_PROGRESS, REWORK}；关联后 `kind` 改为 INTERACTIVE，`branch_reason` = 工作项无 DELIVERY 执行时 INITIAL，否则 STRATEGY；分配 `seq_no`；审计 `execution.linked`。已关联再关联 → 409 130426。
- **BR-03-05 ENSEMBLE**：同组 ≤3（PRD §10.6；项目可在 07 调为 1–5）；超出 → 409 130439。发起者：人须持 `work_item_assign` + `execution_start`；项目主管 Agent 不经角色码，经 08 `AuthorityChecker`（`POLICY:ensemble_branch`）判定。组内各分支可用不同 Agent / runtime / 模型；各分支对应 02 的 ENSEMBLE 分派行（02 `ensemble_plan_id`），工作项 `current_execution_id` 在选中前保持 NULL。
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


### 5.x 业务规则增补（D112 / D113，00 v2.0 §15）

- **BR-03-95 平台重跑**：AUTOMATED 阶段的一切确定性检查（`verify` 命令、`VISUAL` / `INTERACTION` 验证器、构建）由平台在**干净的隔离副本**（交付执行 `head_commit` 的检出 + 可见验收集）中执行；执行者在会话中贴出或写入的任何测试 / 构建日志**一律不作为证据**，只作参考附件。
- **BR-03-96 隐藏验收**：隐藏文件只在平台副本中复制并运行；任何执行（含测试 / 验收 Agent）的工作区与上下文包都不包含隐藏文件内容或路径列表（只告知「存在隐藏验收集」与数量）；隐藏集的 sha256 在每次运行时校验并写入证据。
- **BR-03-97 测试 diff 检测**：对比交付执行 diff 与 FROZEN 可见集的路径：任何对冻结测试文件的修改、删除、重命名、或通过配置跳过（如修改测试命令、`skip` / `only` 标记）→ 自动 FAILED、发布 `cheating.suspected{execution_id, files[]}`，并在反馈中写明「不得修改冻结的验收测试」；测试 Agent 新增的测试放在独立目录，不得覆盖冻结文件，合入后成为下一次执行的可见测试（不进入冻结集，除非人工冻结新版本）。
- **BR-03-98 诚实证据包**：Claim 必须带 `unverified[]`；为空数组时，系统仍检查验收标准覆盖映射，存在无证据的标准 → 自动补入 `unverified` 并标黄；「缺验证不算完成」：存在 `unverified` 项时 AGENT_ACCEPTANCE 不得给 PASSED，除非该项在验证计划中明确为 `HUMAN`。
- **BR-03-99 真实交付形态**：产品验收 Agent 的输入包 = 需求与验收标准 + 模板评分标准（11）+ 交付物的可运行副本（只读）+ AUTOMATED 阶段截图（`VISUAL` 验证器产出，按模板定义的路由与视口：默认 1440×900 与 390×844）+ 成品文件清单；Codex / Claude 以图片输入方式接收截图。产品验收 Agent 不得只看代码判定。
- **BR-03-100 跨模型家族**：执行开始时快照 `model_family`；AGENT_TEST / AGENT_ACCEPTANCE 的 `model_family` 与被验收交付执行相同时，必须 `family_fallback=true` 且模型不同、主体不同（05 BR-05-109 保证），评审台显示「同家族回退」标记。
- **BR-03-101 内部轮次计数**：每次 FAILED（Q3/Q6/Q9/Q11）计 1 次内部轮次，交由 02 判断是否达到 `max_internal_rounds`（00 §15.2）；内部失败**不发任何人工通知**，只写事件与执行记录。
- **BR-03-103 工具校准门槛**（D116）：执行 `VISUAL` / `INTERACTION` 等须工具校准的验证器（11 BR-11-33）前，按 04 `ListEligibleMachines(capabilities=[browser_capture])` 取候选，再以 11 `VerifierCatalog.ToolCalibration` 过滤，只在 CALIBRATED 机器上运行。没有 CALIBRATED 机器时，该 AUTOMATED 运行保持 PENDING（`waiting_reason=TOOL_UNCALIBRATED`，内部验收面板显示「等待已校准的截图机器」），**不在未校准机器上判定、不判 FAILED、不计内部轮次**（BR-03-101），也不通知人；消费 11 `verifier.calibration_changed{state:CALIBRATED}` 后自动开始。等待超过 24 小时 → 升级人工（`MANDATORY_HUMAN_GATE`，detail=`TOOL_UNCALIBRATED`，途径同 Q11），附各机器的 `last_failures`。运行开始后校准状态变化不影响本次结论；证据记录运行时的 `provider_ref` 与校准行 id。
- **BR-03-102 穿透学习（职责划分）**：`quality.escaped` 由 02 在拒绝裁决 / 甲方拒收的同一事务内发布（02 §4.5、BR-02-58），学习回流（06 `MemoryWriter.Propose`、11 `EvalSetWriter.ProposeCase(ctx, tx, {template_key, template_version?, source: ESCAPE, work_item_id, description, evidence_refs[], expected})`）只由 02 `delivery.escape_learning` 执行。本模块不发布 `quality.escaped`、不重复回流，只经 `InternalQAReader` 向 02 提供：该工作项最近一次 AGENT_TEST / AGENT_ACCEPTANCE 的结论、`internal_qa_run_ids[]`、参与的 TESTER / ACCEPTOR Agent 与截图证据引用（02 据此判断「内部已放行」并填入穿透记录）。

### 5.y 远程工作区（D119）

**预览**

- **BR-03-104 类别与分期**：服务预览（WEB / API）与评审预览为 MVP；产物预览中文档、图片、PDF、测试报告与设备框网页、OpenAPI 控制台为 MVP，视频（低清代理）与小说章节阅读器为 v1.1；分享链接分期待定（D119 未列）。所有预览都是**进度，不是交付**：页面常驻说明「预览只反映当前工作区，不代表已验收」，预览内容不作为证据（需要证据时由人另行截图登记为 OBSERVATION / SCREENSHOT）。
- **BR-03-105 预览配置来源**：按序取第一个有值者：① 项目 ACTIVE 宪法（07 `ConstitutionReader.Active`）正文中信息串为 `preview` 的围栏代码块（JSON 数组，元素为 S8 `PreviewConfig`）；② 项目模板解析参数 `params.preview`（11 `TemplateResolver.ForProject`）。解析失败 → 该来源视为无配置并在远程工作区页显示「预览配置无效：{字段}」；两者都没有 → 只能使用 Agent 提供的端口（BR-03-113）或产物预览（A56 `config_key` 请求 → 422 **130460**）。配置在会话创建时快照，之后宪法变更不影响已有会话。
- **BR-03-106 平台启动服务预览**：PLATFORM 会话由机器程序在执行工作区（`cwd` 相对工作区根）以 `start` argv 启动（不经 shell；环境 = BR-03-70 白名单 + 配置 `env`；不注入机器凭证与访问令牌），进程挂在该执行的进程树下（04 BR-04-91 据此判定归属），日志写入执行日志的独立段落 `preview/<key>`。同一执行同一 `config_key` 至多一个非终态会话（重复请求返回现有会话，200）。执行 PAUSED 时预览进程**继续运行**（便于人工查看），INTERRUPTED / 终态时停止。
- **BR-03-107 评审预览**：评审台对某声明〔预览此版本〕→ A57：在声明执行所在机器（离线或无工作区时，按 04 `ListEligibleMachines(require_workdir)` 选同项目仓库已登记的机器；K8S_POOL 为新 Pod）以 `git worktree add --detach <隔离目录> <claim.head_commit>` 建隔离副本（只读挂载可见验收集以外的内容不变），在副本内按配置启动；会话 `holder=PREVIEW` 占一个执行槽（04 `SlotHolderKind=PREVIEW`）。副本与执行工作区互不影响；会话结束删除副本并释放槽。声明被裁决（`decision.made` 对该 claim）后 REVIEW 会话停止（CLAIM_DECIDED）。
- **BR-03-108 生命周期**：空闲 = 30 min 内网关没有为该会话转发任何请求（WebSocket 帧计为活动）；最长存活默认 8 h（部署配置 `execution.preview.max_lifetime_hours`，1–24）；SERVICE 随执行进入终态或 INTERRUPTED 停止；全部停止都向机器下发 `preview.stop`，机器 4 s 后杀进程树。
- **BR-03-109 健康检查**：经隧道对 `health_path` 发 GET（超时 5 s）；STARTING 期间每 2 s，READY 后每 30 s；跟随至多 3 次同源重定向。
- **BR-03-110 并发**：每执行非终态 SERVICE 会话 ≤3、REVIEW 会话每声明 ≤1、每项目非终态会话 ≤20；超出 429 **130463**。
- **BR-03-111 标签**：26 位小写 base32（130 bit，CSPRNG），一次性，会话结束后不复用；URL 不含执行、项目或租户 ID。路径模式下为 `/p/<label>/`（04 BR-04-94）。
- **BR-03-112 产物查看器**：ARTIFACT 会话按扩展名与内容嗅探选择 `ViewerKind`：`.md/.txt/.log/.csv/.json` → DOCUMENT（纯文本或安全 Markdown 渲染，禁原始 HTML）；`.html`（测试报告目录内）→ REPORT（在预览域 iframe 中渲染，`sandbox="allow-scripts"`，不与控制台同源）；`.png/.jpg/.gif/.webp/.svg` → IMAGE（SVG 只在预览域渲染）；`.pdf` → PDF（预览域内置查看器）；`.mp4/.webm/.mov/.mkv` → VIDEO（v1.1）；小说模板章节目录（11 产物 schema）→ CHAPTER（v1.1）；SERVICE 会话的 WEB 配置 → WEB_DEVICE；API 配置且有 `openapi_path` → OPENAPI。其他类型只提供下载（BR-03-121）。所有文件字节只从预览域（`https://<label>.<preview-domain>/__files/<path>`）提供，控制台只嵌入，不在控制台源上渲染工作区内容。
- **BR-03-113 Agent 提供端口**：Agent 经运行时工具输出声明「预览端口 N」时（事件 `preview_offered{port, title?}`），或人在远程工作区从 04 `TunnelBroker.Ports(holder)`（只含执行进程树内的回环监听端口）中选择时，创建 `origin=AGENT_OFFERED` 会话；端口不在列表中 → 403 130403 `reason_code=PORT_NOT_OWNED`（不开流、不请求机器）；开流时机器再次判定（04 BR-04-91），拒绝同码。Agent 不能提供其他执行或机器上其他进程的端口。
- **BR-03-114 访问判定**（`PreviewAccessResolver.Authorize`，04 网关逐请求调用，结果按 Cookie 缓存 ≤30 s）：会话 READY；Cookie 中的预览访问会话属于该 `label`、未过期、未撤销；访问者（人类主体，Agent / 服务账号恒拒）对会话所属项目仍有 `execution_read`（成员被移出项目、权限被撤销后 ≤30 s 失效）；机器开关仍允许。任一不满足 → 网关 403 页「你没有访问这个预览的权限或预览已结束」（不暴露项目名）。分享链接访问另见 BR-03-130。
- **BR-03-115 访问换取**：控制台换取页（§8.8）调用 A60（Cookie 会话 + `execution_read`）→ 返回一次性 `code`（32 字节随机，60 s、单次）与 `url = https://<label>.<preview-domain>/__access?code=…`；网关以 `code` 换取 `__Host-preview` Cookie（15 min 滑动，最长到会话 `expires_at`）后 302 去掉查询串；`code` 只存哈希；重复使用 → 网关 403 并审计 `execution.preview_access_denied`。访问会话不扩大权限：每个请求仍经 BR-03-114。
- **BR-03-116 设备框**：WEB_DEVICE 查看器提供 桌面 1440×900 / 平板 820×1180 / 手机 390×844 三档（配置 `devices` 可收窄）、旋转、刷新、在新标签打开（同样经换取）；iframe 指向预览域，控制台对 iframe 不注入任何脚本。
- **BR-03-117 视频与章节**（v1.1）：VIDEO：浏览器可直接播放（mp4 H.264 / webm）且 ≤200 MiB → 直接以 Range 流播放；否则若机器有 `media_transcode`（04 BR-04-48）→ 经 `MEDIA_PROXY` 流按需转码为 480p H.264 低清代理（缓存于机器临时目录、不写工作区、执行结束清理），页面标「低清预览」；两者都不满足 → 只提供下载。CHAPTER：按模板章节顺序列出，Markdown 渲染，提供「与上一版对比」（上一版 = 同章节在 `base_commit` 或上一个声明提交中的内容，行级 diff）。
- **BR-03-118 刷新推送**：ARTIFACT / SERVICE 会话打开期间，服务端为其开 `FS_WATCH`（04 §6.4b，路径为当前查看的文件及其目录，去抖 500 ms），`FS_CHANGED` 经 SSE 推送 `preview_files_changed{session_id, paths[]}`；查看器自动刷新（DOCUMENT / IMAGE / PDF）或显示「文件已更新〔刷新〕」（VIDEO / REPORT / WEB_DEVICE 的 HMR 由应用自身处理）。
- **BR-03-119 开关与离线**：消费 04 `machine.access_changed`：`allow_remote_preview=false` → 该机全部非终态预览会话 STOPPED（ACCESS_DISABLED）；`allow_remote_terminal=false` → 全部 SHELL 会话 CLOSED（ACCESS_DISABLED）；`allow_file_edit=false` → 后续 A66 一律 403；`websocket_available=false` → 会话 STOPPED（MACHINE_OFFLINE）。`machine.state_changed{OFFLINE|REVOKED}` 同样停止全部会话。

**文件**

- **BR-03-120 浏览（只读，MVP）**：A62 列目录（深度 1，≤2000 项，按名称排序，目录在前）、A63 读文件（文本 ≤2 MiB 内联返回，超出或二进制只提供下载）；`.git/` 不列出（差异经 A64）；全部经 04 `FILE_READ` / `FS_LIST` 流，路径限定由机器端判定（04 BR-04-92），越界 → 403 130403 `reason_code=PATH_OUTSIDE_WORKSPACE`。执行终态后工作区仍在（SHARED_DIR / 未清理的 worktree）且机器在线时仍可浏览；K8S_POOL 的 Pod 已删除、机器离线或目录已清理 → 409 **130467**，页面引导到证据文件。
- **BR-03-121 下载**：单文件 ≤100 MiB，`Content-Disposition: attachment`、`X-Content-Type-Options: nosniff`、`Content-Type: application/octet-stream`（控制台源上从不以可执行类型返回工作区内容）；每次下载审计 `execution.workspace_file_downloaded`（路径 SHA-256 + 大小）。
- **BR-03-122 差异**：A64 返回相对执行 `base_commit` 的变更列表与单文件统一 diff（≤1 MiB，超出截断并标注）；未提交的工作区改动一并列出（标「未提交」）。
- **BR-03-123 密钥遮罩**：遮罩模式 = 内置（`.env`、`.env.*`、`*.pem`、`*.key`、`id_rsa*`、`id_ed25519*`、`*.p12`、`*.pfx`、`.npmrc`、`.netrc`、`.pypirc`、`credentials*.json`、`*secret*`（不区分大小写））∪ 项目宪法 `preview` 块同级的 `mask` 数组；随每个 `FILE_READ` / `FS_LIST` 流下发，机器端执行：命中文件在列表中显示但标「已遮罩」，读取时 `.env` 类按行返回 `KEY=••••`（只保留键名），其他类型不返回内容；下载、编辑、分享一律拒绝（422 **130469**）。遮罩不是授权边界的替代：有 `execution_read` 才能看到键名。

**终端**

- **BR-03-124 只读观看（MVP）**：WATCH 会话经 04 `TERMINAL_WATCH` 流镜像该执行内运行时所执行命令的 stdout / stderr（执行桥在启动工具子进程时复制其输出）与执行桥状态行；流单向，客户端发来的任何输入帧 → RST。需 `execution_read`；不受「允许远程终端」开关约束（同执行日志）；不录制（内容已在执行事件与日志中）。每执行 WATCH ≤8（04 配额）。
- **BR-03-125 交互 shell（v1.1）**：SHELL 会话要求：调用者为**人类**且对项目持 `terminal_interact`（不可委托、仅人类，08）；step-up purpose `terminal_interactive`、target `execution:<id>`；机器 `allow_remote_terminal=true`（否则 403 130403 `MACHINE_ACCESS_DISABLED`）；执行非终态且工作区存在。机器端以工作区根为初始 cwd、最小环境、可用时目录沙箱（04 BR-04-96；`sandboxed=false` 时页面常驻红字）。全部输入与输出以 asciicast v2 录制，实时写入 files（purpose `terminal_recording`，按 1 MiB 分块追加），关闭时计算 SHA-256 并写审计 `execution.terminal_closed{recording_sha256}`；录制保留 180 天（`execution.terminal.recording_retention_days`），与审计同等只读。空闲 15 min 无输入关闭，最长 2 h；每执行同时 ≤1 个 SHELL（04 配额）。执行 RUNNING 时允许打开，但终端顶部琥珀提示「Agent 正在运行，你的操作可能与它冲突；需要独占时请先接管」。

**人工接管与编辑（v1.1）**

- **BR-03-126 人工接管**：A65 PUT：人类，持 `execution_stop` 与 `workspace_file_edit`；执行 RUNNING → 同请求内暂停（BR-03-40 规则）；PAUSED / INTERRUPTED → 直接记录；其他状态 409 130410。接管后：Agent 不再收到该执行的新轮次与恢复；K8S_POOL 的 Pod 在接管与 PAUSED 期间保留（04 §4.6，≤24 h）。释放（A65 DELETE）后才能恢复交还 Agent；交还时上下文包增加一条 SYSTEM 说明「人工在 {时间} 编辑了 n 个文件，提交 {sha}」。
- **BR-03-127 编辑与上传**：A66 只在 `taken_over_by = 调用者` 且执行 PAUSED / INTERRUPTED、机器 `allow_file_edit=true` 时可用；否则：执行 RUNNING → 409 **130466**「执行正在运行，请先暂停或接管」；未接管或接管者非本人 → 409 130466；开关关闭 → 403 130403 `MACHINE_ACCESS_DISABLED`。每次保存为一批变更（≤50 个文件，单文件 ≤10 MiB，文本或上传文件 `file_id`），每项带 `base_sha256`（与机器上当前内容不一致 → 409 **130472**，返回当前 sha）；遮罩文件不可写（422 130469）；越界路径 403 `PATH_OUTSIDE_WORKSPACE`。机器端写入后在执行分支上提交，作者 = 接管人显示名与中性邮箱 `<principal_id>@users.noreply.invalid`（不含部署品牌），提交信息首行为用户填写的说明，追加 trailer `Edited-By-Human: <principal_id>`；结果登记为产物 COMMIT（`producer_kind=HUMAN`）；审计 `execution.workspace_files_edited{commit, paths_sha256[], count}`。人工提交与 Agent 提交在评审台中分别标注。

**分享链接（分期待定）**

- **BR-03-128 申请**：A70：对 READY 的 SERVICE / ARTIFACT 会话（REVIEW 不可分享），申请人为人类且对项目持 `execution_start`；`ttl_hours` 1–168；同一会话非终态链接 ≤1。发布 `share_link.requested` → 08 收件箱 `PREVIEW_SHARE_APPROVAL`。
- **BR-03-129 审批**：A72：人类、`preview_share_approve`（不可委托、仅人类）、**不得是申请人**（403 130403 `SELF_REVIEW`）；批准时生成令牌，响应一次性返回 `share_url = https://share.<preview-domain>/#t=<token>`（令牌在 fragment，不进日志与 Referer）；令牌只存哈希，申请人与批准人此后都无法再次取得，遗失须撤销后重新申请。
- **BR-03-130 甲方访问**：分享入口页（预览域、静态页）读取 fragment 后调用 A74 交换为该 `label` 主机的只读访问 Cookie（`__Host-share`，与 `expires_at` 同时失效）；之后网关对分享访问只放行 GET / HEAD 与 WebSocket 升级，其他方法 405「这是只读预览」；ARTIFACT 只读本就成立。分享页面只显示部署显示名（`GET /deployment`，缺失时中性文案）、申请时的 `note` 与预览本身，**不显示**执行、工作项、Agent、日志、文件浏览、终端或任何内部记录；不显示「下载」。
- **BR-03-131 失效**：到期、撤销、会话结束 → 网关返回 410 页「此预览链接已失效」，交换接口 A74 对失效令牌返回同一 410（不区分原因，不泄露是否存在）；撤销 ≤1 s 生效（网关缓存以 `share_link` 版本失效）。
- **BR-03-132 审计**：`execution.share_link_requested` / `_approved` / `_rejected` / `_revoked` / `_expired`、`execution.share_link_accessed`（每 IP 每小时一条，detail 含 IP 前缀 /24 与 UA 摘要）。

### 5.z 锚定批注与过期上下文写入（D120）

- **BR-03-133 锚点与查看器**：创建线程时 `anchor_kind` 必须与 `viewer_kind` 匹配（S9 适用查看器列），坐标必须落在该版本内容内（行号 ≤ 文件行数、`row_index` < 行数、`paragraph_sha256` 与该段一致、`end_ms ≤ duration_ms`、`rect` 在页面尺寸内），否则 422 **130476**（`data.field`）。分期随 D119 查看器：CODE_LINES、TABLE_ROW、HTML_REGION、文档中的 TEXT_PARAGRAPH 为 MVP；MEDIA_TIME 与章节中的 TEXT_PARAGRAPH 为 v1.1（MVP 构建中返回 422 **130480**，与 A75 的 130473 同理）。二进制且无查看器的文件不可批注（130476）。
- **BR-03-134 权限**：读线程 = 对项目 `execution_read`（CLIENT 线程同样可读，用于返工）；新建与回复 = `artifact_comment_write`（00 §6.1；默认 `team_owner`、`delivery_lead`、`engineer` 与全部 Agent 角色，纯人工项目中 Agent 仍被 08 S2(e) 拒绝）；解决 / 重新打开 = 线程创建者、执行者本人或 `work_item_write`；编辑 / 撤回 = 作者本人（撤回另允许 `work_item_write`）。不满足 → 403 130403（`MISSING_PERMISSION` / `NOT_OWNER`）。提及规则同 05 BR-05-84（被提及者须对该项目有 `execution_read`，否则 422 130476 `data.not_readable[]`），提及 Agent 经 05 投影到其收件箱（MENTION）。
- **BR-03-135 版本绑定**：每个线程在创建时固定 `version_kind` + `version_ref`：已提交文件与评审预览取 commit；证据文件、产物与未提交的工作区文件取内容 sha256（服务端经 04 `FILE_READ` 读取时计算，客户端提交的 sha 只作校验，不一致 → 409 **130477**「内容已变化，请刷新后再批注」）；运行中的服务预览取执行当时的 `head_commit` 并记 `version_uncommitted`。服务端在同一请求内保存 `anchor_excerpt`（≤2000 字符，按锚点截取；遮罩文件（BR-03-123）不可批注 → 422 130469）。原版本无法再取得（未提交内容已被覆盖、Pod 已删除）时线程仍可读，查看器以 `anchor_excerpt` 与截图替代原文并标「原版本已不可用」。
- **BR-03-136 HTML 区域的取点**：控制台不向 iframe 注入脚本（BR-03-116 不变）。批注模式下查看器请求预览网关在**预览域**对该会话的 `text/html` 响应注入辅助脚本 `/__annotate.js`（04 BR-04-104；同源于该预览标签，只在批注模式开启时注入）：脚本只读取用户点选元素的 CSS 选择器、包围盒与可见文本，经 `postMessage` 发给控制台；控制台只接受 `event.origin` 等于该会话预览源、且通过 S9 校验的消息，并把内容视为不可信数据（只作坐标与引文，不渲染为 HTML）。应用 CSP 阻止脚本或跨域 iframe 时，退回在覆盖层上框选矩形（`precision:"RECT_ONLY"`，`selector` 为空），页面提示「此页面不支持元素定位，已按位置标注」。PDF 与 REPORT 查看器同样经覆盖层取 `rect`（PDF 另含 `page`）。
- **BR-03-137 新版本上的定位与「锚点版本已过期」**：打开线程或查看器切换版本时，按「正在查看的版本」判定（结果缓存到 `anchor_checked_version` / `relocated_anchor`）：CODE_LINES —— 用 `git diff version_ref..查看版本` 映射行号，锚定行全部未改动则 RELOCATED 到新行号，否则 OUTDATED；TEXT_PARAGRAPH —— 先找 `paragraph_sha256` 相同的段落，再按 `quote`（前缀 + 原文 + 后缀）模糊匹配，均失败则 OUTDATED；TABLE_ROW —— 有 `row_key` 时按主键找行，行哈希相同为 RELOCATED、不同为 OUTDATED（仍高亮该行并标「此行已修改」），无主键时按 `row_sha256` 查找；HTML_REGION —— 辅助脚本可用时在新页面中解析 `selector` 且可见文本与 `text_quote` 一致为 RELOCATED，否则 OUTDATED（RECT_ONLY 在版本变化后恒为 OUTDATED）；MEDIA_TIME —— 文件内容 sha 变化即 OUTDATED（不尝试对齐时间轴）。OUTDATED 线程不消失：在查看器侧栏「基于旧版本」分组列出，显示原文快照与〔查看原版本〕（打开 `version_ref` 的只读视图）。判定只影响呈现，不改变线程状态、不自动解决。
- **BR-03-138 线程上限与编辑**：每线程 ≤200 条批注，每执行 ≤2000 个线程（超出 422 **130481**）；正文与提及规则同 05 BR-05-84 / 92（≤8000 字符）；作者 15 min 内可编辑（≤10 次），可撤回（不物理删除，撤回后 `body=null`）；系统不写批注（状态变化只体现在线程字段与事件中）。
- **BR-03-139 转为结构化反馈与缺陷**：转换只由下游所有者发起：02 D1 的 `feedback[].comment_thread_id`（02 BR-02-66）与 12 A31（12 BR-12-37），二者在自己的事务内调用 `ArtifactComments.MarkConvertedOn(ctx, tx, thread_id, {target_kind, target_id, actor})`，本模块写 `execution_comment_conversions`（锚点快照不可变）并发布 `comment.converted`。同一线程每种目标至多一次（130478，`data.target_id` 返回已存在对象）；FEEDBACK 与 DEFECT 可并存。转换后线程自动 RESOLVED（可重新打开，但转换记录保留）。被转换的反馈 / 缺陷始终带回链（`comment_thread_id`）与锚点快照，返工执行的上下文包第 3 层（裁决反馈）附带该快照（BR-03-28 预算内，超出时只保留路径与行号）。
- **BR-03-140 过期上下文写入**（05 BR-05-127 / 128，00 §9）：调用方为 AGENT 时，A27（提交证据）、A28（修改证据）、A77（新建线程）须带 `seen_seq`（执行关联工作项时对应 02 `work_item:<id>` 流；ADHOC 未关联工作项的执行不要求），A79（回复）须带 `comment_thread:<id>` 流的 `seen_seq`（线程中他人的批注或线程状态变化）；缺失 → 422 **130475**；有更新事件 → 409 **130474**（`data.state="interrupted"` + `newer_events`），不写任何数据，`interrupted` 不入幂等表，可用同一 `Idempotency-Key` 重试。判定在取得工作项行锁（02 `WorkItemActivityReader.HeadOn` 在调用方事务内加锁读取当前序号）或线程行锁后、其他守卫之前执行。停止 / 暂停执行（A12 / A14）不要求 `seen_seq`。证据提交、线程新建与回复在成功时调用 02 `WorkItemActivityRecorder.RecordOn`（EVIDENCE_ADDED / COMMENT_ADDED），使工作项流前进。
- **BR-03-141 甲方批注**：CLIENT 线程只能由 07 在甲方门户的验收会话中经 `ArtifactComments.CreateClientOn` 创建（01 门户会话 → 07 `AcceptanceRecorder`），只允许在**面向甲方的产物**上：证据被选入某个 SENT 状态交付、`include_mode=FILE` 且未被构建剔除（07 `delivery_evidence_selections`）；其他产物 → 07 返回 422（07 BR-07-63）。CLIENT 线程：作者为 `CLIENT:<intake_contact_id>`；不可提及；团队只读、不能回复（130479，团队与甲方的沟通仍走 01 澄清）；可被团队在内部另起 INTERNAL 线程引用。**INTERNAL 线程永不出现在门户**；甲方只看到自己所在交付上的 CLIENT 线程。
- **BR-03-142 事件不含正文**：`comment.*` 事件只含 ID、锚点种类、可见性、作者与提及主体，不含正文、引文或代码（00 §7.1 载荷约定）；05 据此投影 Agent 收件箱（MENTION、执行者的 MESSAGE），被提及的人类由本模块经 09 `Notifier` 通知（§9 N13 / N14，接收偏好按 08）。
- **BR-03-143 评审台汇总**：A81 按工作项汇总全部执行上的线程（含 OUTDATED 计数与已转换标记），供 02 评审台与工作项详情使用；当前声明执行之外的线程标「来自较早的执行 #n」。
- **BR-03-144 删除与保留**：线程与批注不物理删除（00 §3）；随执行归档只读；证据与产物的保留期不因批注延长或缩短。
- **BR-03-145 并发**：同一线程的回复在线程行锁内分配 `seq`；A80 带 If-Match（409 130409）；两人同时把同一线程转为 FEEDBACK → 唯一约束保证只有一条，后到者 409 130478 并得到已有 `target_id`。
- **BR-03-146 审计**：`execution.comment_thread_created`、`execution.comment_thread_resolved` / `_reopened`、`execution.comment_converted`（含 `target_kind`、`target_id`）、`execution.comment_retracted`；detail 不含正文与引文（只含线程 ID、锚点种类、版本引用）。
- **BR-03-147 工具集快照即证据**（D120 第 6 项，05 BR-05-144 / 145）：每次派发（`execution.start` 入队）在同一事务内调用 05 `ToolsetResolver.ResolveOn(tx, …)`，把结果写入 `toolset` / `toolset_revision`，并经 `IssueTokens` 取得工具令牌随 `execution.start` 下发（载荷见 §6.z 增补）。快照是该执行的**证据**：执行详情「工具与技能」区按来源列出（「团队目录 issue-tracker v2」「技能 deploy v4」「本机自带 pdf-tools」），声明（02 C1）的证据摘要与证据包 manifest 附带 `toolset_revision` 与条目列表，评审台据此判断某次产出用的是哪一版工具；之后目录发布的新版本不改写已有快照。上下文层 6（`06_skills/`，BR-03-25）只放快照中的目录技能（与运行时技能目录内容一致），不再另行读取 06 挂载。INTERACTIVE / ADHOC 会话的「Skills、MCP」选择只能在 05 判定为 PROJECT 授予覆盖的目录条目与本机自带项中进行。05 不可用 → 本次派发不入队（调用方得到 503，系统派发按既有退避重试），禁止不带快照启动。
- **BR-03-148 工具授权请求的发起权限**（`interaction_request`，08 §3.3d）：机器回传 `interaction_requested` 时，本模块以该执行的**执行者**（Agent 或人）为主体、执行所属项目为范围调 08 `AuthorityChecker.Check(interaction_request)`：允许 → 按既有 §4.3 创建 PENDING 交互；拒绝 → 不创建待人处理的交互，立即下发 `interaction.response{action:"DENY", content:"CAPABILITY_NOT_AUTHORIZED"}`，写一条 `state=CANCELLED`、`closed_reason=CAPABILITY_NOT_AUTHORIZED`、`reason_code` 为 08 原因码的交互记录（审计 `execution.interaction_denied`），不通知任何人。默认授予覆盖全部会运行执行的角色（08 §3.3d），因此默认行为不变；该码让团队可对某个 Agent 或自定义角色关闭「向人请求工具授权」。回应仍只能由人类以 `interaction_respond` 做出（BR-03-31 不变）。该操作在 05 操作清单中登记为 `interaction.request`。

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
| A07 | POST `/work-items/{work_item_id}/ensembles` | work-items | execution_start + work_item_assign（人）；主管 Agent 经 `POLICY:ensemble_branch`（BR-03-05） | O(work_item_id) | `{branches:[{agent_id, runtime?, model?, effort?}] 2..3, reason≤2000}` | `{ensemble_group_id, executions: Execution[]}` | 409 130439；403 | ✔ |
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


### 6.x 接口增补（D112 / D113，00 v2.0 §15）

| # | 方法 | 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 |
|---|---|---|---|---|---|---|---|
| A50 | GET | `/work-items/{work_item_id}/acceptance-suites` | work-items | `execution_read` · O | — | 列表（隐藏文件只返回数量与 sha256，不返回路径与内容） | 404 |
| A51 | POST | `/work-items/{work_item_id}/acceptance-suites` | work-items | `acceptance_suite_manage` · O；HUMAN | `{visible_files[], hidden_files[], command[]}`（文件为 files 节点 id） | 集（DRAFT） | 422 130454（命令为空 / 文件缺失） |
| A52 | PUT | `/acceptance-suites/{suite_id}/state` | acceptance-suites | `acceptance_suite_manage` · O；HUMAN；If-Match | `{state:"FROZEN"}` | 集 | 409 130451 |
| A53 | GET | `/work-items/{work_item_id}/internal-qa-runs` | work-items | `execution_read` · O | `?subject_execution_id=` | 运行列表（含发现、截图、评分、同家族回退标记） | 404 |
| A54 | GET | `/internal-qa-runs/{run_id}` | internal-qa-runs | `execution_read` · O | — | 单条 | 404 |

接口：`ExecutionLauncher.Launch` 接受 `purpose ∈ {INTERNAL_TEST, INTERNAL_ACCEPTANCE}` 与 `subject_execution_id`（05 调用）；新增 `InternalQAReader{Latest(work_item_id), PassedStages(execution_id)}`（02 / 08 读取）。使用：06 `MemoryWriter.Propose`、11 `EvalSetWriter.ProposeCase`、11 `VerifierCatalog.ToolCalibration`（BR-03-103）、04 `MachineDirectory`（`browser_capture` 能力）。

错误码：**130451** 验收集已冻结不可修改；**130452** 内部验收运行已终结；**130453** 内部验收执行不能提交完成声明；**130454** 验收集定义不完整；**130455** 交付执行修改了冻结测试（`data.files[]`，同时记 `cheating.suspected`）；**130456** 隐藏验收集校验和不符（平台副本被篡改，升级人工）；**130457** 机器缺少截图能力（`browser_capture`），VISUAL 验证器无法执行（按 BR-03-99 改由其他机器或升级）；**130458** 证据包缺 `unverified` 字段。

### 6.y 接口增补（D119 远程工作区）

| # | 方法 | 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 |
|---|---|---|---|---|---|---|---|
| A55 | GET | `/executions/{execution_id}/preview-sessions` | executions | `execution_read` · O | `?state=&kind=` | `PreviewSession[]` + `meta.configs[]`（可用预览配置，BR-03-105）+ `meta.gateway{enabled, mode}` | 404 |
| A56 | POST | `/executions/{execution_id}/preview-sessions` | executions | `execution_start` · O | `{kind:"SERVICE", config_key}` \| `{kind:"SERVICE", port}`（AGENT_OFFERED）\| `{kind:"ARTIFACT"}` | `PreviewSession`（201 / 200 已存在） | 422 130460；403 130403（`PORT_NOT_OWNED` / `MACHINE_ACCESS_DISABLED`）；409 140484 / 130467 / 130410（执行终态）；429 130463；503 140492 |
| A57 | POST | `/claims/{claim_id}/preview-sessions` | claims | `execution_read` · O（评审人；REVIEW 只读） | `{config_key?}` | `PreviewSession`（201） | 同 A56；409 130410（声明已裁决） |
| A58 | GET | `/preview-sessions/{session_id}` | preview-sessions | `execution_read` · O | — | `PreviewSession` | 404 |
| A59 | PUT | `/preview-sessions/{session_id}/termination` | preview-sessions | 发起者，或 `execution_stop` · O | `{}` | `PreviewSession`（STOPPED，USER） | 幂等 |
| A60 | POST | `/preview-sessions/{session_id}/access-grants` | preview-sessions | `execution_read` · O；HUMAN（Cookie 会话） | `{return_path? ≤2000}` | `{url, expires_in:60}`（`no-store`） | 409 130462（未就绪 / 已结束） |
| A61 | GET | `/executions/{execution_id}/offered-ports` | executions | `execution_read` · O | — | `[{port, process_name, offered_by_agent:bool}]`（04 `TunnelBroker.Ports`，只含执行进程树内端口） | 409 140484 |
| A62 | GET | `/executions/{execution_id}/workspace/entries` | executions | `execution_read` · O | `?path=`（相对，默认根） | `[{name, path, type:"FILE"\|"DIR"\|"LINK", size?, modified_at?, masked:bool, changed?:"A"\|"M"\|"D"}]` | 403 130403（`PATH_OUTSIDE_WORKSPACE` / `MACHINE_ACCESS_DISABLED`）；409 130467 / 140484 |
| A63 | GET | `/executions/{execution_id}/workspace/content` | executions | `execution_read` · O | `?path=&download=0\|1` | 文本 JSON `{text, sha256, truncated}` 或下载流 | 同 A62；413 130468；422 130469 |
| A64 | GET | `/executions/{execution_id}/workspace/diff` | executions | `execution_read` · O | `?path=`（缺省 = 变更列表） | `{base_commit, files:[{path, status, additions, deletions, uncommitted}]}` 或 `{path, unified_diff, truncated}` | 同 A62 |
| A65 | PUT / DELETE | `/executions/{execution_id}/human-takeover` | executions | `execution_stop` + `workspace_file_edit` · O；HUMAN（v1.1） | PUT `{reason ≤2000}` | `Execution`（含 `taken_over_by`） | 409 130410 / 130466；403 130403 |
| A66 | POST | `/executions/{execution_id}/workspace-commits` | executions | `workspace_file_edit` · O；HUMAN；接管者本人（v1.1） | `{message 1–200, changes:[{path, base_sha256?, text? \| file_id? \| delete?:true}] 1..50}` | `{artifact, commit}`（201） | 409 130466 / 130472；422 130469；403 130403（`PATH_OUTSIDE_WORKSPACE` / `MACHINE_ACCESS_DISABLED`） |
| A67 | POST | `/executions/{execution_id}/terminal-sessions` | executions | WATCH：`execution_read`；SHELL：`terminal_interact` + HUMAN + step-up `terminal_interactive`（v1.1） · O | `{mode:"WATCH"\|"SHELL", cols?, rows?, reauth_proof?}` | `TerminalSession`（201）+ `channel_url` | 403 130403（`MACHINE_ACCESS_DISABLED` / `REAUTH_REQUIRED` / `NON_DELEGABLE` / `AGENT_PROHIBITED`）；409 140484 / 130467；429 140486 |
| A68 | GET · PUT | `/terminal-sessions/{session_id}` · `/terminal-sessions/{session_id}/termination` | terminal-sessions | 打开者或 `execution_stop` · O | — | `TerminalSession` | 404 |
| A69 | GET（WebSocket 升级） | `/terminal-sessions/{session_id}/channel` | terminal-sessions | 打开者本人（同一 Cookie 会话） · O | 文本帧 `{type:"input", data}`（仅 SHELL）、`{type:"resize", cols, rows}`；服务端帧 `{type:"output", data}`、`{type:"closed", reason}` | — | 403；WATCH 收到 input → 关闭（1008） |
| A70 | POST | `/preview-sessions/{session_id}/share-links` | preview-sessions | `execution_start` · O；HUMAN（分期待定） | `{ttl_hours 1–168, note? ≤500}` | `ShareLink`（201，PENDING_APPROVAL） | 409 130462 / 130465 |
| A71 | GET | `/executions/{execution_id}/share-links` · `/share-links/{link_id}` | executions · share-links | `execution_read` · O | — | `ShareLink[]` / `ShareLink`（从不含令牌） | 404 |
| A72 | PUT | `/share-links/{link_id}/decision` | share-links | `preview_share_approve`（不可委托）· O；HUMAN；非申请人 | `{action:"APPROVE"\|"REJECT", reason? ≤2000}` + If-Match | APPROVE：`{share_link, share_url}`（`no-store`，只此一次）；REJECT：`ShareLink` | 403 130403（`SELF_REVIEW` / `NON_DELEGABLE`）；409 130465 / 130462 |
| A73 | PUT | `/share-links/{link_id}/revocation` | share-links | 申请人、批准人或 `preview_share_approve` · O；HUMAN | `{reason ≤2000}` | `ShareLink`（REVOKED） | 409 130465 |
| A74 | POST | `/share-access`（预览域，公开；`authz.RegisterPublic` + 限流 10 次/分钟/IP） | share-access | 令牌即凭据 | `{token}` | 204 + `Set-Cookie: __Host-share` + `Location` | 410 130464（失效 / 不存在，不区分） |
| A75 | GET | `/executions/{execution_id}/artifact-views` | executions | `execution_read` · O | `?path=` | `{viewer:ViewerKind, src_url（预览域）, proxy?:{available, profile}, previous?:{commit, src_url}}` | 409 130467；422 130473（v1.1 查看器在 MVP 构建中返回此码） |

**表示**：`PreviewSession{id, execution_id, claim_id?, kind, origin, config_key?, preview_kind?, viewers[], port?, commit?, machine{id, name, type}, state, stop_reason?, url_mode:"SUBDOMAIN"\|"PATH", path_mode_warning, ready_at?, last_access_at?, expires_at, failure_detail?, share_link_count, version}`（**不含** `label`，URL 只经 A60 换取取得）；`TerminalSession{id, execution_id, mode, state, sandboxed?, opened_by, opened_at, closed_at?, close_reason?, recording?:{file_id, sha256}}`（录制只对 `audit_read` 可见）；`ShareLink{id, preview_session_id, state, note, ttl_hours, requested_by, decided_by?, approved_at?, expires_at?, revoked_at?, end_reason?, access_count, last_access_at?, version}`。

**机器执行级消息增补**（§6.3 同表语义；传输属 04 §6.4 / §6.4b）：

| 方向 | 消息 | 载荷要点 | 服务端处理 |
|---|---|---|---|
| S→M | `preview.start` | `{command_id, session_id, holder:{kind, id}, config:S8, workdir:{mode:"EXECUTION"\|"ISOLATED_COPY", commit?}}` | 机器启动预览进程（REVIEW 先建隔离副本）；回 `preview.state` |
| S→M | `preview.stop` | `{command_id, session_id, reason}` | 杀进程树（4 s），REVIEW 删除副本 |
| M→S | `preview.state` | `{session_id, state:"STARTING"\|"RUNNING"\|"EXITED", exit_code?, log_tail? ≤4 KiB}` | §4.y V2 / V6 / V7 |
| M→S | `execution.events` 新事件类型 | `preview_offered{port, title?}` | BR-03-113 |
| S→M | `workspace.commit` | `{command_id, execution_id, dispatch_epoch, changes:[{path, base_sha256?, file_id? \| inline_b64? \| delete?}], author:{name, email}, message, trailer}`（v1.1） | BR-03-127 |
| M→S | `workspace.commit_result` | `{command_id, ok, commit?, conflicts?:[{path, current_sha256}], code?}` | 写产物、审计；冲突 → 130472 |

**跨模块接口增补**：本模块提供 `PreviewAccessResolver`（04 网关使用）：`Authorize(ctx, label, cookie, method) (Grant{session_id, tenant, project, machine_id, holder, port, read_only}, error)`、`ExchangeCode(ctx, label, code) (cookie, error)`、`ExchangeShare(ctx, token) (label, cookie, error)`、`Touch(session_id)`（空闲计时）、`HolderActive(ctx, holder) bool`（04 J10）。本模块使用 04 `TunnelBroker`（`Open` / `ResetHolder` / `Ports`）、`MachineDirectory.Type / RemoteAccess`、07 `ConstitutionReader.Active`、11 `TemplateResolver.ForProject`（`params.preview`）、08 `AuthorityChecker`（`terminal_interact`、`preview_share_approve`、`workspace_file_edit`）。

**错误码增补**：**130460** preview_config_missing（422，无可用预览配置或 `config_key` 不存在）；**130461** preview_start_failed（409，`data.failure_detail`）；**130462** preview_not_ready（409，会话未就绪或已结束）；**130463** preview_limit（429）；**130464** share_link_invalid（410，失效或不存在，不区分）；**130465** share_link_state（409，非法迁移）；**130466** workspace_not_editable（409，执行正在运行、未接管或接管者非本人；`data.reason`）；**130467** workspace_unavailable（409，机器离线、Pod 已删除或目录已清理）；**130468** file_too_large（413）；**130469** file_masked（422）；**130472** edit_conflict（409，`data.conflicts[]`）；**130473** viewer_unsupported（422）。权限类拒绝统一 403 130403，新 `reason_code`：`MACHINE_ACCESS_DISABLED`、`PORT_NOT_OWNED`、`PATH_OUTSIDE_WORKSPACE`（08 §3.5 登记）。

### 6.z 接口增补（D120 锚定批注与过期上下文写入）

| # | 方法 | 路径 | tag | 权限 · 守卫 | 请求 | 响应 | 错误 |
|---|---|---|---|---|---|---|---|
| A76 | GET | `/executions/{execution_id}/comment-threads` | executions | `execution_read` · O | `?artifact_kind=&path=&evidence_id=&state=&visibility=&view_version=`（`view_version` 给出时逐线程返回相对该版本的 `anchor_status` 与 `relocated_anchor`） | `CommentThread[]`（含首条批注摘要、计数、`anchor_status`、`conversions[]`） | 404 |
| A77 | POST | `/executions/{execution_id}/comment-threads` | executions | `artifact_comment_write` · O | `{artifact_kind*, artifact_ref*, version_ref*, viewer_kind*, anchor_kind*, anchor*（S9）, body*≤8000, seen_seq?（AGENT 且执行关联工作项时必填）}` | `CommentThread`（201，含 seq=1 的批注）；`meta.next` | 422 130476/130469/130475/130480/130481；409 130477/130474；403 130403 |
| A78 | GET | `/comment-threads/{thread_id}` | comment-threads | `execution_read` · O | `?view_version=` | `CommentThread` + `comments[]` + `conversions[]`；ETag | 404 |
| A79 | POST | `/comment-threads/{thread_id}/comments` | comment-threads | `artifact_comment_write` · O | `{body*≤8000, seen_seq?（AGENT 必填，`comment_thread` 流）}` | `Comment`（201） | 409 130474/130479；422 130475/130476/130481 |
| A80 | PUT | `/comment-threads/{thread_id}/resolution` | comment-threads | 线程创建者、执行者本人或 `work_item_write` · O；If-Match | `{state*:"RESOLVED"\|"OPEN", note?≤500}` | `CommentThread` | 409 130409/130479；403 130403 |
| A81 | GET | `/work-items/{work_item_id}/comment-threads` | work-items | `execution_read` · O | `?state=&visibility=&anchor_status=&execution_id=` | `CommentThread[]`（跨执行汇总，BR-03-143） | 404 |
| A82 | PATCH · DELETE | `/comments/{comment_id}` | comments | 作者本人（DELETE 另允许 `work_item_write`）· O | PATCH `{body*}`（If-Match） | `Comment` / 204（撤回） | 409 130409；422 130476 |

**表示**：`CommentThread{id, execution_id, work_item_id?, artifact_kind, artifact_ref, version_kind, version_ref, version_uncommitted, viewer_kind, anchor_kind, anchor, anchor_excerpt, anchor_snapshot_url?（预览域）, anchor_status, relocated_anchor?, visibility, delivery_id?, state, created_by{kind, id, display}, comment_count, last_seq, last_comment_at, conversions[{target_kind, target_id, converted_by, converted_at}], version}`；`Comment{id, thread_id, seq, author{kind, id, display}, body?, mentions[], edited_at?, retracted, created_at}`。CLIENT 作者只显示联系人显示名，不显示联系方式。

**过期上下文写入增补**：A27 / A28 请求增加 `seen_seq?`（BR-03-140）；A27 / A28 / A77 / A79 在 409 130474 时 `data = {state:"interrupted", stream, seen_seq, current_seq, newer_events[], truncated}`（00 §9）。这些操作登记到 05 `OperationRegistry`（`evidence.create`、`evidence.update`、`artifact_comment_thread.create`、`artifact_comment.reply`），响应带 `meta.next`。

**跨模块接口增补**：本模块提供 `ArtifactComments`（00 §8）：`GetOn(ctx, tx, thread_id) (ThreadView, error)`（含锚点、版本、可见性、`work_item_id`、`delivery_id`）；`MarkConvertedOn(ctx, tx, thread_id, Conversion{target_kind, target_id, actor}) error`（同一 `target_kind` 已存在 → `ErrAlreadyConverted{target_id}`，调用方映射为自己的码或幂等返回；CLIENT / INTERNAL 与目标不匹配 → `ErrTargetNotAllowed`）；`CreateClientOn(ctx, tx, ClientComment{delivery_id, delivery_item_id, evidence_id, intake_contact_id, anchor_kind, anchor, version_ref, body}) (thread_id, error)`（只在面向甲方的产物上，BR-03-141）。本模块使用 02 `WorkItemActivityRecorder.RecordOn` / `WorkItemActivityReader.HeadOn`（BR-03-140）、05 `OperationRegistry`、07 `DeliveryEvidenceReader.ClientFacing(evidence_id)`（判定面向甲方，由 07 BR-07-63 提供）、04 预览网关的批注辅助脚本（04 BR-04-104）。

**错误码增补**：**130474** write_interrupted（409，工作项或批注线程已有更新事件，未写入；`data.state="interrupted"`）；**130475** seen_seq_required（422，Agent 写入未携带 `seen_seq`）；**130476** anchor_invalid（422，锚点与查看器不匹配、坐标越界、提及不可读主体、无查看器的二进制文件；`data.field`）；**130477** artifact_version_changed（409，提交的版本与当前内容不一致或该版本不可取得）；**130478** comment_already_converted（409，同一线程已转换为该类对象；`data.target_id`）；**130479** client_thread_read_only（409，甲方批注线程团队侧只读）；**130480** anchor_kind_unavailable（422，v1.1 锚点种类在 MVP 构建中不可用）；**130481** comment_limit（422，线程或执行的批注数量达上限）。i18n key `execution.error.<名>`，三语齐全。

**工具集与工具授权请求增补（D120 第 6 项）**：`execution.start`（§6.3）载荷增加 `toolset{revision, skills:[{entry_key, version_no, sha256, content?（总量 ≤256 KiB 时内联）| file_ref?}], mcp:[{entry_key, version_no, transport, package?{manager, name, version, integrity, file_id?}, command?, args?, env?}], tool_tokens:[{entry_key, token, expires_at}]}`（MCP 条目不含地址与凭据：STREAMABLE_HTTP 一律经机器本地代理访问 05 中继 T13，04 BR-04-105 / 106）；`leases.renew` 响应每项增加 `tool_tokens?[]`（剩余有效期 <5 min 时由 05 `IssueTokens` 续签；`status≠OK` 的项不签发）；`execution.rejected.reason` 增加 `TOOLSET_UNAVAILABLE`（机器校验技能哈希或安装包失败；E02 → FAILED，失败原因 `TOOLSET_UNAVAILABLE`，按基础设施失败上报 `ExecutionFailed`）。A03 / A43 的 `Execution` 表示增加 `toolset`、`toolset_revision`；列表过滤增加 `toolset_revision`(eq)。交互 `closed_reason` 增加 `CAPABILITY_NOT_AUTHORIZED`（BR-03-148）。本模块使用 05 `ToolsetResolver`（`ResolveOn` / `IssueTokens`），向 05 `OperationRegistry` 登记 `interaction.request`（机器事件 `interaction_requested`，权限码 `interaction_request`）。

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


### 7.x 任务与事件增补（D112 / D113，00 v2.0 §15）

任务：`execution.platform_rerun`（Q1，每个声明一次，隔离副本 + 可见验收集 + VISUAL / INTERACTION，超时按验证器）；`execution.hidden_acceptance`（Q1，同副本追加隐藏集）。（穿透学习任务归 02 `delivery.escape_learning`，见 BR-03-102。）
发布：`acceptance_suite.frozen{suite_id, work_item_id, visible_count, hidden_count}`；`internal_qa.passed{work_item_id, subject_execution_id, stage, run_id}`；`internal_qa.failed{work_item_id, subject_execution_id, stage, run_id, findings_summary, counts_as_round: bool}`；`cheating.suspected{execution_id, work_item_id, files[]}`。（`quality.escaped` 由 02 发布。）
消费：02 `plan.approved`（冻结验收集）；11 `verifier.calibration_changed`（`state=CALIBRATED` 时启动等待中的 AUTOMATED 运行，BR-03-103）。
SSE：`tenant/{t}/work_item/{id}` 推送内部验收阶段变化（仅对有 `execution_read` 的成员；甲方门户不订阅）。

### 7.y 任务与事件增补（D119）

任务：`execution.preview_reaper`（每 30 s，dlock `execution:preview-reaper`：空闲 30 min、到 `expires_at`、REVIEW 声明已裁决 → STOPPED；STARTING 超时 → FAILED；健康检查调度）；`execution.share_link_expirer`（每 60 s：ACTIVE 到期 → EXPIRED；PENDING_APPROVAL 超 72 h → REJECTED）；`execution.terminal_idle`（每 60 s：SHELL 空闲 15 min / 满 2 h → CLOSED；封存录制）；`execution.terminal_recording_retention`（每日：超保留期录制解绑删除，审计事件保留哈希）。
发布（outbox）：`share_link.requested{link_id, execution_id, preview_session_id, requested_by, ttl_hours}`、`share_link.decided{link_id, action, decided_by}`、`share_link.ended{link_id, end_reason}`（EXPIRED / REVOKED / SESSION_ENDED；08 据此关闭收件箱项）。预览与终端会话状态只经 SSE，不发领域事件。
消费：04 `machine.access_changed`（BR-03-119）；`decision.made`（REVIEW 会话 CLAIM_DECIDED）；`machine.state_changed`（OFFLINE / REVOKED → 停止会话）。
SSE：执行主题帧 `kind` 增加 `preview`（会话状态）、`preview_files_changed`、`terminal`（终端会话状态，不含内容）、`share_link`。
审计（00 §7.2，08 §7.5 登记）：`execution.preview_started` / `_stopped`（detail `session_id, kind, origin, port?, commit?`）、`execution.preview_access_denied`、`execution.workspace_file_downloaded`、`execution.workspace_path_denied`（越界尝试，同主体 10 min 合并）、`execution.terminal_opened` / `execution.terminal_closed`（SHELL 必含 `recording_sha256`、`sandboxed`；WATCH 只记 opened）、`execution.human_takeover_started` / `_released`、`execution.workspace_files_edited`、`execution.share_link_*`（BR-03-132）。

### 7.z 任务与事件增补（D120）

任务：无新增周期任务（锚点判定在读取时进行并缓存；原版本截图由 A77 后的单次任务 `execution.comment_anchor_snapshot` 生成，任务键 `comment-snapshot:<thread_id>`，机器无 `browser_capture` 或离线 → 放弃，不重试）。
发布（outbox，载荷不含正文，BR-03-142）：`comment.thread_created{thread_id, execution_id, work_item_id?, project_id, anchor_kind, visibility, created_by{kind,id}, mention_principal_ids[]}`、`comment.posted{thread_id, comment_id, seq, author{kind,id}, mention_principal_ids[]}`、`comment.thread_resolved{thread_id, state}`、`comment.converted{thread_id, target_kind, target_id, converted_by}`（可经 SSE 推送完整载荷）。
消费：无新增（转换由下游在其事务内同步调用 `ArtifactComments`）。
SSE：执行主题 `tenant/{t}/execution/{id}` 帧 `kind` 增加 `comment_thread`（线程增改，含 `anchor_status`）；工作项汇总由 02 工作项主题转发。
审计：BR-03-146 所列（08 §7.5 登记）。

## 8. 页面规格

> 应用 `apps/frontend-web-admin/apps/delivery`；组件只引用语义 Token；所有页面具备加载骨架、空态、错误态（可重试）、无权限态（写明缺少的权限码）、离线/重连态（顶部条"正在重连… 显示的可能不是最新状态"，保留已加载内容）。文案称"你"；不出现 Runner / Daemon / Computer（技术详情面板除外）。

### 8.1 执行列表

- **目的**：查看项目内全部执行与会话，快速进入待你决定的那一个。
- **入口与路由**：项目空间侧栏「流水线 › 执行」（08 §8.6）；`/t/:tenantID/p/:projectID/executions`；"我的执行" `/t/:tenantID/executions?mine=1`（A43）。
- **布局**：顶部筛选条（状态多选、种类、机器、runtime、执行者、工作项、分支原因、"仅待你决定"开关、时间范围、"显示已归档"）；右上〔新建临时会话〕；主体 AutoTable。
- **列**：标题（ADHOC 显示"临时"徽标）/ 所属工作项链接；分支标签（`#3 · 质量返工 2`，紫色）；`StateBadge`（RUNNING 且有未决交互时显示琥珀"待你决定 n"）；执行者 `ActorAvatar`；机器（在线点）；runtime · 模型；成本（`$0.42` / 估算前缀 `≈` / 未知显示 `—` + token 数）；开始时间；时长。
- **交互**：行点击 → INTERACTIVE/ADHOC 进工作台，AUTONOMOUS 进详情；行菜单〔停止〕（BR-03-40 可用时）、〔归档〕（终态）。〔新建临时会话〕需 `execution_start`，弹窗字段：机器（离线/无 runtime 置灰并注明原因）、runtime（无"自动"降级：未安装即不可选）、模型（04 目录 + 手填 ≤160）、推理强度（随模型）、授权模式（FULL_AUTO 红色并触发重新认证）、工具开关（Codex 置灰并说明"CLI exec 不支持网页审批"）、工作目录模式；创建中按钮显示进度，失败保留表单。
- **实时**：订阅项目列表主题；≤1 s 更新状态与成本。
- **空态**："还没有执行。工作项分派给 Agent 后会自动开始；你也可以〔新建临时会话〕"。
- **无权限**："需要 execution_read 权限"。
- **移动端**：表格改卡片（标题、状态、分支、成本、机器），筛选收进底部 Sheet；经底栏「项目」进入项目空间后由抽屉「流水线 › 执行」到达（底栏为 工作台 · 会话 · 项目 · 裁决，08 §8.6）。

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


### 8.x 页面增补（D112 / D113，00 v2.0 §15）

- **内部验收面板**（执行详情与评审台共用组件 `InternalQAPanel`）：三段进度 AUTOMATED → AGENT_TEST → AGENT_ACCEPTANCE，每段状态、执行主体（Agent 头像 + 模型家族，「同家族回退」黄标）、发现列表（严重度、关联验收标准、证据）、截图缩略图（点击放大，标注视口与路由）、评分（模板评分标准逐项）。内部失败记录只在执行详情可见；评审台只在全部通过后出现该工作项，并显示「内部验收已通过 · 第 n 轮」。
- **验收集卡片**（工作项详情）：状态 DRAFT / FROZEN、可见文件列表、隐藏文件数量与校验和（不显示路径）、冻结人与时间；冻结按钮仅人类且有 `acceptance_suite_manage`。
- **作弊嫌疑提示**：执行详情显示红色横幅「本次执行修改了冻结测试：files…」，提供 diff 链接。
- **证据卡**：新增「未验证项」区块（`unverified[]`），为空时显示「执行者声明：无未验证项」。

### 8.6 远程工作区 `/t/:tenantID/p/:projectID/executions/:executionID/workspace`（D119）

- **入口**：执行详情与交互会话工作台头部〔远程工作区〕；评审台声明卡〔预览此版本〕（直达预览 Tab 并创建 REVIEW 会话）；会话卡片中的 `preview_offered` 事件「Agent 提供了预览：端口 5173〔打开〕」。渲染在所属项目的项目空间内（08 §8.6，高亮「流水线 › 执行」）。
- **布局**：头部同 §8.2（标题、状态、机器 + 类型徽章、分支）；Tab（URL `?tab=`）：**预览** · **产物** · **文件** · **终端**；Tab 内各功能按机器开关与分期显示：v1.1 功能带「v1.1」标签（MVP 构建中禁用并说明「将在 v1.1 提供」）；分享相关按钮带「分期待定」标签。
- **预览 Tab**：会话列表（配置名 / Agent 提供端口、状态徽章 启动中 / 就绪 / 已停止 / 失败、剩余存活时间、空闲倒计时、分享链接数）；〔启动预览〕菜单列出可用配置（BR-03-105）与 Agent 提供的端口（A61；只列执行进程树内端口）；就绪会话展开为查看器：WEB_DEVICE（设备档切换 桌面 / 平板 / 手机、旋转、刷新、〔在新标签打开〕）、OPENAPI（接口列表 + 试调用面板，请求经预览域发送）；路径模式时查看器顶部常驻琥珀标签「路径模式：部分应用可能无法正常工作」；顶部常驻说明「预览是进度，不是交付」。失败会话显示 `failure_detail`（等宽）与〔重新启动〕。〔停止〕需发起者或 `execution_stop`。〔申请分享〕（`execution_start`）→ 对话框（有效期 1–168 h、说明）→ 提交后显示「等待批准」。
- **产物 Tab**：左侧工作区产物树（按查看器类型分组：文档、图片、PDF、报告、视频「v1.1」、章节「v1.1」），右侧查看器（A75 返回的预览域地址，iframe）；文件变化时 DOCUMENT / IMAGE / PDF 自动刷新，其余显示「文件已更新〔刷新〕」；CHAPTER 提供〔与上一版对比〕；VIDEO 低清代理标「低清预览」。
- **文件 Tab**：目录树 + 文件查看（只读代码视图，等宽、行号、语法高亮在客户端以纯文本安全渲染）；变更徽标 A / M / D；〔差异〕（A64）；〔下载〕（遮罩文件禁用并提示「含密钥，已遮罩」）；遮罩文件显示锁图标与 `KEY=••••`。**编辑**（v1.1）：〔接管并编辑〕→ 确认框（RUNNING 时「将暂停 Agent」）→ A65；接管后编辑器可写、〔上传文件〕、〔保存为提交〕（说明必填）→ A66；冲突 130472 时显示三方对比与〔用我的版本覆盖〕（重新提交带最新 `base_sha256`）；〔结束接管〕→ A65 DELETE。执行 RUNNING 且未接管时编辑按钮禁用并 tooltip「执行正在运行，请先暂停或接管」；开关关闭时「这台机器不允许远程编辑文件」。
- **终端 Tab**：默认 WATCH（xterm 只读，无光标，顶部「只读：Agent 运行的命令输出」）；〔打开交互终端〕（v1.1，需 `terminal_interact`）→ 底座 `ReauthenticationDialog`（`terminal_interactive`）→ A67{SHELL} → 终端顶部常驻红色条「本终端全程录制并写入审计」，`sandboxed=false` 时追加「这台机器无法限制目录」，执行 RUNNING 时琥珀提示（BR-03-125）；空闲 14 min 时提示「1 分钟后因空闲关闭」。开关关闭时按钮禁用并说明「这台机器未开启远程终端（由团队所有者在机器设置中开启）」。
- **状态**：加载骨架；无 `execution_read` → 无权限态；预览网关未配置 → 预览与产物 Tab 显示「部署未配置预览域名」；机器只用长轮询 → 「这台机器的网络不支持实时通道」；机器离线 / Pod 已删除 → 「工作区不可用，可在执行详情的证据中查看已采集文件」；连接中断 → 顶部琥珀条，终端自动重连 WATCH（SHELL 不自动重开，需再次认证）。
- **移动端**（<768px）：Tab 改为底部分段；预览默认「手机」设备档并占满宽度；文件树为抽屉；终端字号 12 px、横向滚动，交互终端在手机上显示软键盘工具条（Esc / Tab / Ctrl / 方向键）。

### 8.7 分享查看页（预览域 `https://share.<preview-domain>/#t=…`，分期待定）

- 静态页读取 fragment → A74 → 跳转到该预览；页头只显示部署显示名（`GET /deployment`，缺失时「预览」）、`note`、到期时间与「只读预览」标签；无登录、无内部链接、无下载。失效（410）→「此预览链接已失效，请联系发送者」。语言按浏览器（en-US / zh-CN / ms-MY）。

### 8.8 预览换取页 `/t/:tenantID/preview-access?session=<id>&return=<path>`（控制台）

- 网关发现无有效预览 Cookie 时 302 到此页；页面以当前登录会话调用 A60 后跳转预览域；未登录 → 底座登录后回到此页；无权限 → 「你没有访问这个预览的权限或预览已结束」；会话已结束 → 同文案 +〔返回执行〕。页面不渲染任何预览内容。

### 8.9 锚定批注（D120；远程工作区产物 / 文件 / 预览 Tab 与评审台内嵌，不单独成页）

- **入口**：远程工作区（§8.6）各查看器工具栏〔批注〕开关（`artifact_comment_write` 时可用，否则只显示已有批注）；评审台与工作项详情的「批注」分组（A81）；Agent 收件箱与通知中的链接（`?thread=<id>` 定位并高亮锚点）。
- **取点**：CODE（文件 / 差异视图）—— 拖选行号槽选择行区间；DOCUMENT 表格 —— 点击行首；HTML（设备框 / 报告）—— 开启批注后鼠标悬停高亮元素、点击选中（辅助脚本可用）或拖框（RECT_ONLY，顶部提示「已按位置标注」），记录当前设备档、视口与滚动；PDF —— 拖框；文档段落 —— 选中文字（自动扩展到段落并保存引文）；视频（v1.1）—— 在进度条上点或拖出区间，播放器暂停在该点；章节（v1.1）—— 同文档段落。取点后右侧弹出新线程输入框（支持 @，Enter 发送）。
- **呈现**：锚点在内容上以编号标记（代码行槽圆点、表格行左侧色条、HTML 区域虚线框、视频进度条刻度、段落左侧竖条）；右侧线程栏分组：「当前版本」/「已重新定位」（显示「已从第 12 行移到第 15 行」）/「基于旧版本」（灰显，原文快照 + 截图，〔查看原版本〕）/「已解决」（折叠）；甲方批注带「甲方」徽章与只读说明「请通过澄清回复甲方」。线程卡：作者（人 / Agent / 甲方）、版本（commit 短哈希或「未提交」）、正文、回复、〔解决〕〔转为反馈〕（评审台裁决对话框中预填该线程，02）〔转为缺陷〕（12 A31，预填标题、重现位置与引文）、已转换徽章（链接到反馈或缺陷）。
- **状态**：加载骨架；无批注空态「在内容上选择位置即可批注」；版本不可取得时线程显示快照与「原版本已不可用」；MVP 中视频 / 章节查看器的批注按钮带「v1.1」并禁用；离线时批注栏只读并提示重连。
- **移动端**：线程栏改为底部抽屉；取点改为长按（代码行、段落、表格行）与双指框选（HTML / PDF）；锚点编号在手机上仍 ≥24 px 可点。

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
| N10 | 分享链接已批准 / 已驳回（分期待定） | 申请人 | 站内 | "{批准人}{批准/驳回}了 {执行} 的预览分享{：理由}" | 每链接一次 |
| N11 | 预览启动失败（PLATFORM / REVIEW） | 发起者 | 站内 | "{执行} 的预览 {配置名} 启动失败：{摘要}" | 同会话一次 |
| N12 | 交互终端被打开（v1.1） | 执行的人类执行者 / 发起者（非打开者本人）+ 工作项负责人 | 站内 | "{actor} 在 {执行} 上打开了交互终端（已录制）" | 每会话一次 |
| N13 | `comment.thread_created` / `comment.posted` 中提及人类（D120） | 被提及的人类 | 站内 + Web Push；离线 >10 min 邮件摘要 | "{author} 在 {执行} 的 {文件名} 上提到了你" | 同线程 5 min 内合并 |
| N14 | 新批注落在本人执行的产物上 / 甲方在交付产物上批注（D120） | 执行的人类执行者或发起者；CLIENT 线程另通知交付负责人 | 站内 | "{author} 批注了 {执行} 的 {文件名}"；甲方："甲方在交付 {编号} 的 {文件名} 上留下了批注" | 同执行 10 min 内合并；Agent 执行者不发通知（经 05 收件箱） |

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
Given 项目主管 Agent L
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


### 10.x 验收标准增补（D112 / D113，00 v2.0 §15）

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-03-72 | integration | BR-03-95 | **G** 执行者在会话中贴出「全部测试通过」日志，实际有 1 个失败 **W** 提交声明 **T** 平台副本重跑得 FAILED，内部返工，日志不作为证据 |
| AC-03-73 | integration | BR-03-96 | **G** 工作项有 3 个隐藏验收文件 **W** 交付执行、测试执行、验收执行运行 **T** 三者工作区与上下文包均不含隐藏文件内容或路径；平台副本运行并记录 sha256 |
| AC-03-74 | integration | BR-03-97 | **G** 冻结集含 `tests/a.test.mjs` **W** 交付 diff 修改该文件 **T** AUTOMATED FAILED（130455），发布 `cheating.suspected`，反馈含「不得修改冻结的验收测试」，无人收到通知 |
| AC-03-75 | integration | BR-03-97 | **G** 测试命令被改为跳过某文件 **W** 平台重跑 **T** 判定为跳过冻结测试，同 AC-03-74 |
| AC-03-76 | unit | BR-03-98 | **G** Claim 缺 `unverified` 字段 **W** 提交 **T** 422 130458 |
| AC-03-77 | integration | BR-03-98 | **G** 验收标准 AC2 无证据且 `unverified=[]` **W** 进入 AGENT_ACCEPTANCE **T** AC2 自动补入 unverified；验收 Agent 不得给 PASSED |
| AC-03-78 | e2e | BR-03-99 | **G** Web 交付物，模板定义路由 `/` **W** AUTOMATED **T** 产出 1440 与 390 两张截图，作为图片输入进入产品验收执行 |
| AC-03-79 | e2e | Q8, BR-03-99 | **G** 页面逻辑测试全过但棋盘行高不一致（2048 实测案例）**W** 产品验收 Agent 审查截图 **T** 给出 MAJOR 发现「棋盘行高不一致」并 FAILED，工作项内部返工；人未收到任何通知 |
| AC-03-80 | integration | BR-03-100 | **G** 交付执行与可选验收 Agent 均为 `openai` 家族 **W** 派验收 **T** 运行记录 `family_fallback=true`，模型不同、主体不同；评审台显示「同家族回退」 |
| AC-03-81 | integration | Q11 | **G** 验收 Agent 连续两次 INCONCLUSIVE **W** 第二次结束 **T** 升级人工（MANDATORY_HUMAN_GATE，detail INTERNAL_QA_INCONCLUSIVE） |
| AC-03-82 | integration | BR-03-102 | **G** 工作项 AGENT_TEST 与 AGENT_ACCEPTANCE 均 PASSED（含截图证据）**W** 02 处理人工拒绝时调用 `InternalQAReader.Latest(work_item_id)` **T** 返回两阶段结论、`internal_qa_run_ids[]`、TESTER / ACCEPTOR Agent 与截图证据引用；本模块不发布 `quality.escaped`、不调用 06 / 11 |
| AC-03-83 | integration | BR-03-102 | **G** 工作项无内部验收记录（`internal_qa_required=false`）**W** `InternalQAReader.Latest` **T** 返回空结论，02 据此不记穿透 |
| AC-03-84 | unit | 4.x | **G** 验收集 FROZEN **W** PATCH 文件 **T** 409 130451 |
| AC-03-85 | integration | Q3, BR-03-101 | **G** AUTOMATED 失败 **W** 内部返工 **T** 任何人的收件箱与通知均无新增；执行详情可见失败记录 |
| AC-03-86 | integration | BR-03-103 | **G** 机器 A 的 `ui_screenshot` CALIBRATED、B 为 DRIFTED，两台都有 `browser_capture` **W** 平台重跑 UI 工作项 **T** VISUAL 只在 A 执行；证据记录 A 的 `provider_ref` 与校准行 id |
| AC-03-87 | integration | BR-03-103, BR-03-101 | **G** 所有候选机器的 `ui_screenshot` 均未校准 **W** 平台重跑 **T** AUTOMATED 运行保持 PENDING（`TOOL_UNCALIBRATED`），无 FAILED、内部轮次不增加、无人工通知；任一机器校准通过（`verifier.calibration_changed`）后自动开始；假时钟推进 24 h 仍无 → 升级人工（detail `TOOL_UNCALIBRATED`） |
| AC-03-88 | e2e | §3.23 | **G** 浏览器最小窗口宽度约 500px 的机器 **W** 以 420×800 视口截图 **T** 截图 420×800，页面自报宽度 420，无裁切（2048 误报案例回归） |

### 10.y 验收标准增补（D119 远程工作区）

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-03-89 | integration | BR-03-105/106, V1–V3 | **G** 项目宪法含 `preview` 块 `{key:"web", kind:"WEB", start:["npm","run","dev"], port:5173, health_path:"/"}`，执行 E RUNNING，机器 WebSocket 可用 **W** A56{config_key:"web"} **T** 201 REQUESTED → 机器收到 `preview.start` → STARTING → 健康检查 200 后 READY；同请求重复 → 200 返回同一会话；会话 JSON 不含 `label` |
| AC-03-90 | e2e | BR-03-114/115, A60 | **G** 会话 READY **W** 成员 M（有 `execution_read`）点〔打开〕 **T** 经换取页 → 预览域 `https://<label>.<preview-domain>/` 渲染页面，HMR WebSocket 可用；地址栏不含 `code`；控制台 Cookie 未出现在发往预览域的请求中 |
| AC-03-91 | integration | BR-03-114（安全） | **G** 会话 READY，成员 N 对该项目无 `execution_read`，成员 M 有 **W** N 调 A60；N 直接访问预览 URL；M 的预览 Cookie 被 N 复制使用后 M 被移出项目 **T** A60 → 403 130403 `MISSING_PERMISSION`；N 直接访问 → 302 换取页 → 无权限页；M 被移出后 ≤30 s 内该 Cookie 的请求 → 403；Agent 主体调 A60 → 403 130403 `AGENT_PROHIBITED` |
| AC-03-92 | integration | BR-03-113（安全） | **G** E 的进程树监听 5173；同机另一项目执行 F 监听 3000；机器上其他进程监听 8080 **W** A61 **T** 只返回 5173 **W** A56{port:3000} 与 A56{port:8080} **T** 均 403 130403 `PORT_NOT_OWNED`，不向机器发 `tunnel.open`；伪造 `preview_offered{port:8080}` 事件 → 会话 FAILED（机器端 140485 拒绝），审计 `fleet.tunnel_stream_rejected` |
| AC-03-93 | integration | BR-03-107 | **G** 声明 C（`head_commit=abc`）之后执行又产生提交 def **W** 评审人 A57 **T** 机器在隔离副本检出 abc 并启动；预览内容来自 abc；执行工作区未被改动；会话占一个 PREVIEW 槽；02 对 C 裁决后会话 STOPPED（CLAIM_DECIDED）、副本删除、槽释放 |
| AC-03-94 | integration | BR-03-108 | **G** 会话 READY **W** 假时钟推进 30 min 无请求 **T** STOPPED（IDLE），机器收到 `preview.stop` **W** 另一会话持续访问至 8 h **T** STOPPED（MAX_LIFETIME） **W** 执行 SUCCEEDED **T** 其 SERVICE 会话 STOPPED（EXECUTION_ENDED），ARTIFACT 会话可新建 |
| AC-03-95 | integration | BR-03-119 | **G** 机器上 2 个 READY 会话、1 个 SHELL **W** 所有者关闭 `allow_remote_preview` **T** ≤1 s 两会话 STOPPED（ACCESS_DISABLED）、SHELL 保持；**W** 关闭 `allow_remote_terminal` **T** SHELL CLOSED（ACCESS_DISABLED），录制封存 |
| AC-03-96 | integration | BR-03-112/118 | **G** 工作区有 `docs/spec.md`、`out/chart.png`、`report/index.html`、`a.pdf` **W** A75 逐个 **T** viewer 分别为 DOCUMENT / IMAGE / REPORT / PDF，`src_url` 全在预览域；修改 `docs/spec.md` 后 ≤2 s SSE 推送 `preview_files_changed` |
| AC-03-97 | integration | BR-03-117（v1.1） | **G** `demo.mov`（ProRes）且机器有 `media_transcode` **W** 打开 **T** 经 MEDIA_PROXY 播放 480p 代理，工作区无新文件；无 ffmpeg 的机器 → 只提供下载；MVP 构建中 A75 对 VIDEO → 422 130473 |
| AC-03-98 | integration | BR-03-120/123（安全） | **G** 工作区有 `.env`（`DB_PASSWORD=x`）、`src/a.ts`、符号链接 `link → /etc` **W** A62 根目录 **T** 含 `.env`（masked）与 `src`，不含 `.git` **W** A63 `.env` **T** 返回 `DB_PASSWORD=••••` **W** A63 `.env&download=1` **T** 422 130469 **W** A63 `../../etc/passwd`、`link/hosts` **T** 403 130403 `PATH_OUTSIDE_WORKSPACE`，审计 `execution.workspace_path_denied` 一条（合并） |
| AC-03-99 | integration | BR-03-121 | **G** `bin/app.html`（含脚本）**W** A63 `download=1` **T** 响应 `Content-Type: application/octet-stream`、`Content-Disposition: attachment`、`nosniff`；审计 `execution.workspace_file_downloaded` 含路径 SHA-256 |
| AC-03-100 | integration | BR-03-124 | **G** E RUNNING，Agent 执行 `npm test` **W** M（只有 `execution_read`）A67{WATCH} 并通过 A69 发送 input 帧 **T** 收到 `npm test` 输出；input 帧导致通道以 1008 关闭；WORKSTATION 开关关闭时 WATCH 仍可打开 |
| AC-03-101 | integration | BR-03-125（v1.1，安全） | **G** WORKSTATION 机器 `allow_remote_terminal=false`，所有者 O 持 `terminal_interact` **W** A67{SHELL} 带有效 step-up **T** 403 130403 `MACHINE_ACCESS_DISABLED`，不开流 **W** 开关开启后不带 `reauth_proof` **T** 403 `REAUTH_REQUIRED` **W** 持委托的 Agent / 服务账号调用 **T** 403 `AGENT_PROHIBITED`（不可委托） **W** O 带 step-up **T** 201，审计 `execution.terminal_opened`；初始 cwd 为工作区根；15 min 无输入后 CLOSED（IDLE），审计 `execution.terminal_closed{recording_sha256}` 与录制文件 SHA-256 一致；N12 通知执行者 |
| AC-03-102 | integration | BR-03-126/127（v1.1） | **G** E RUNNING，工程师 X 持 `workspace_file_edit`，机器 `allow_file_edit=true` **W** X 直接 A66 **T** 409 130466（执行正在运行）**W** X A65 PUT **T** E PAUSED（USER_PAUSE），`taken_over_by=X` **W** X A66 改 `src/a.ts`（`base_sha256` 正确）**T** 201，机器提交作者为 X、trailer `Edited-By-Human`，产物 COMMIT `producer_kind=HUMAN`，审计 `execution.workspace_files_edited` **W** `base_sha256` 过期 **T** 409 130472 带当前 sha **W** 另一工程师 Y A66 **T** 409 130466 **W** A13 恢复 **T** 409 130466，A65 DELETE 后恢复成功，上下文含人工编辑说明 |
| AC-03-103 | integration | BR-03-127 | **G** WORKSTATION `allow_file_edit=false`（默认）**W** 接管者 A66 **T** 403 130403 `MACHINE_ACCESS_DISABLED`；编辑 `.env` → 422 130469 |
| AC-03-104 | integration | BR-03-128/129（分期待定） | **G** 会话 READY **W** 申请人 R A70{ttl_hours:24} **T** PENDING_APPROVAL，`share_link.requested` → 08 收件箱 `PREVIEW_SHARE_APPROVAL`（不含 R）**W** R 自己 A72 APPROVE **T** 403 130403 `SELF_REVIEW` **W** 审批人 Q APPROVE **T** 返回 `share_url`（fragment 令牌），库中只有哈希；再次 GET 不含令牌 |
| AC-03-105 | e2e | BR-03-130（安全） | **G** ACTIVE 链接 **W** 未登录浏览器打开 `share_url` **T** 显示预览与部署显示名、`note`、「只读预览」；页面与网络请求中不含执行 ID、Agent 名、日志、文件或终端入口；POST 表单 → 405「这是只读预览」 |
| AC-03-106 | integration | BR-03-131（安全） | **G** ACTIVE 链接 L **W** 假时钟越过 `expires_at` 后用同一 Cookie 访问、重新打开 `share_url` **T** 均 410「此预览链接已失效」，L EXPIRED，`share_link.ended` 关闭收件箱项 **W** 另一链接被 A73 撤销 **T** ≤1 s 后访问 410；不存在的令牌与失效令牌返回相同响应 |
| AC-03-107 | e2e | §8.6 | 桌面 1440 与手机 390：四个 Tab 的加载、空、错误、无权限、网关未配置、仅长轮询、工作区不可用状态；v1.1 项带「v1.1」并禁用；分享按钮带「分期待定」；路径模式琥珀标签；无横向滚动，触控 ≥44 px |

### 10.z 验收标准增补（D120 锚定批注与过期上下文写入）

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-03-108 | integration | US-03-37, BR-03-133, BR-03-135 | **G** 执行 E 已提交 `src/app.ts`（commit c1，120 行）**W** 评审人 X A77 `{WORKSPACE_FILE, path, version_ref:c1, viewer_kind:CODE, CODE_LINES{start_line:40, end_line:44}}` **T** 201，`anchor_excerpt` 为第 40–44 行原文，`version_kind=COMMIT`；`end_line:130` → 422 130476（`data.field=end_line`）；对 `.env` 批注 → 422 130469；`viewer_kind=VIDEO` + CODE_LINES → 422 130476 |
| AC-03-109 | integration | BR-03-133, BR-03-136 | **G** 服务预览 S READY，设备档 PHONE **W** 辅助脚本可用时选中按钮；应用 CSP 禁止脚本时拖框 **T** 前者 `HTML_REGION{selector, rect, viewport{390,844,PHONE}, precision:SELECTOR}`、`version_kind=HEAD_COMMIT`；后者 `precision:RECT_ONLY`、`selector` 为空；来自其他源的 `postMessage` 被忽略；控制台源上无任何注入脚本 |
| AC-03-110 | integration | BR-03-133 | **G** DOCUMENT 中的 CSV（主键列 `id`）与 Markdown 文档 **W** 对 `id=42` 的行、对第 3 段批注 **T** TABLE_ROW 含 `row_key{column:"id", value:"42"}` 与 `row_sha256`；TEXT_PARAGRAPH 含 `paragraph_sha256` 与引文；MVP 构建中 MEDIA_TIME → 422 130480 |
| AC-03-111 | integration | US-03-40, BR-03-137 | **G** 线程 T1 锚定 c1 第 40–44 行、T2 锚定第 80 行；c2 只在第 10 行前插入 3 行并改写第 80 行 **W** 以 `view_version=c2` 调 A76 **T** T1 `RELOCATED`、`relocated_anchor` 为 43–47；T2 `OUTDATED`，仍返回原文快照；以 `view_version=c1` → 两者 CURRENT；线程状态均未改变 |
| AC-03-112 | integration | BR-03-137 | **G** TABLE_ROW 线程 `row_key id=42`；新版本中该行金额被改 **W** 判定 **T** OUTDATED 且仍定位到 id=42 的行并标「此行已修改」；该行被删除 → OUTDATED 无定位；段落线程在段落前插入新段后 → 按 sha 找到，RELOCATED |
| AC-03-113 | integration | US-03-39, BR-03-139, 02 BR-02-66 | **G** INTERNAL 线程 T（OPEN）**W** 评审人在 02 D1 `REQUEST_CHANGES` 的 `feedback[0].comment_thread_id=T` 提交 **T** 同一事务内写入反馈行（`location` 由锚点生成）、`execution_comment_conversions(FEEDBACK)` 含锚点快照、T 变为 RESOLVED、发布 `comment.converted`；再次以 T 转 FEEDBACK → 02 返回 422 120471（`data.target_id` = 已有反馈）；同一 T 经 12 A31 转缺陷成功（两种转换并存） |
| AC-03-114 | integration | US-03-41, BR-03-140 | **G** E 关联工作项 W（`activity_seq=12`），执行者 Agent Ada 读到 12 **W** 评审人在 E 的产物上新建批注（W 流到 13）；Ada 以 `seen_seq=12` 调 A27 提交证据 **T** 409 130474，`newer_events` 含 COMMENT_ADDED（线程 ID）；无证据行；Ada 以同一 `Idempotency-Key`、`seen_seq=13` 重试 → 201 且 W 流到 14（EVIDENCE_ADDED）；Ada 不带 `seen_seq` → 422 130475；人类不带 → 201；ADHOC 未关联工作项的执行不要求 `seen_seq` |
| AC-03-115 | integration | BR-03-140, BR-03-145 | **G** 线程 T 最新 seq=4 **W** Agent 以 `seen_seq=3` 回复；两名人类并发回复 **T** Agent 得 409 130474（`stream="comment_thread:<T>"`）；两条人类回复得到 seq 5、6，无重复 |
| AC-03-116 | integration | BR-03-134 | **G** Morgan 只有 `execution_read`；纯人工项目中的助理 Agent 持 `artifact_comment_write` **W** 二者调 A77 **T** Morgan 403 130403（`MISSING_PERMISSION`）；助理 403 130403（`HUMAN_ONLY_MODE`，08 S2(e)）；Morgan 仍可 A76 读取 |
| AC-03-117 | integration | BR-03-141 | **G** 交付 D（SENT）选入证据 Ev（`include_mode=FILE`）；另一证据 Ev2 只入 SUMMARY **W** 07 经门户验收为 Ev 创建 CLIENT 线程、为 Ev2 创建 **T** Ev 成功（`visibility=CLIENT`、作者 `CLIENT:<contact>`）；Ev2 被拒（07 422 127056）；团队成员对该 CLIENT 线程 A79 → 409 130479；门户读取只返回 D 上的 CLIENT 线程，INTERNAL 线程不出现 |
| AC-03-118 | unit | BR-03-142 | **W** 新建含提及与 2000 字正文的线程 **T** `comment.thread_created` 载荷不含正文、引文与路径原文以外的内容（只含 ID、锚点种类、可见性、作者与提及主体）；Agent 被提及时 05 收件箱新增 MENTION，人类被提及时 N13 |
| AC-03-119 | e2e | §8.9 | **G** 桌面 1440 与手机 390 **W** 在代码视图、CSV、Markdown、设备框网页、PDF 上依次批注并切换到新版本 **T** 锚点标记、线程栏四个分组（当前 / 已重新定位 / 基于旧版本 / 已解决）、〔查看原版本〕、〔转为反馈〕〔转为缺陷〕可用；甲方线程带徽章且无回复框；视频与章节批注按钮带「v1.1」并禁用；手机上为底部抽屉、长按取点 |
| AC-03-120 | integration | BR-03-147, 05 BR-05-145 | **G** Ada 在 P 的执行 E1 派发时工具集为 issue-tracker v2、技能 deploy v4、本机 pdf-tools **W** E1 运行中团队发布 issue-tracker v3；E1 返工生成分支 E2；05 在派发时不可用 **T** E1 的 `toolset` 与 `toolset_revision` 不变，执行详情与声明证据摘要列出三项及来源；E2 的快照为 v3；上下文 `06_skills/` 与运行时技能目录内容一致；05 不可用时派发不入队（503），无不带快照的 `execution.start` |
| AC-03-121 | integration | BR-03-148 | **G** 自定义角色移除了 Agent Bo 的 `interaction_request`；Ada 保留默认授予 **W** 二者的执行各回传一个 TOOL_PERMISSION `interaction_requested` **T** Ada：PENDING 交互、N1 通知；Bo：立即下发 `interaction.response{DENY}`，交互记录 CANCELLED（`closed_reason=CAPABILITY_NOT_AUTHORIZED`），无通知、无收件箱项，审计 `execution.interaction_denied`；任一 Agent 回应 Ada 的交互仍 403 130403 |

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


### 11.x 度量增补（D112 / D113，00 v2.0 §15）

`internal_qa_stage_result{stage, result, family_fallback}`、`internal_rounds_per_item`、`cheating_suspected_total`、`platform_rerun_mismatch_total`（执行者声称通过但平台重跑失败的次数）。

**D119**：`preview_session{kind, origin, result, start_ms, stop_reason}`、`preview_access{device:"desktop"|"mobile", mode:"SUBDOMAIN"|"PATH"}`、`artifact_view{viewer}`、`workspace_file{action:"list"|"read"|"download"|"diff", masked:bool}`、`terminal_session{mode, duration_bucket, sandboxed}`、`human_edit_commit{files_bucket}`、`share_link{state_transition}`（不含路径、内容与令牌）。

## 12. 依赖、风险与待决

**依赖**：02（WorkItemReader、WorkItemTransitioner 方法集 §6.9、ClaimSubmitter、Claim 端点与 criteria_map / 祖先证据引用的存储、在裁决事务内调用 `ExecutionLauncher`、自动 RETRY_INFRA 与有界自治暂停；已按 `02-work-review.md` 当前稿对齐，`claim.submitted` 载荷须含 `claim_id, execution_id, head_commit`）；04（通道传输与 `ExecutionDispatchHandler` 接线、`ResolveWorkspace`、执行槽、模型目录与单价、能力检测、附加环境变量名单、连接代次）；05（Agent 资料、`max_context_tokens`、授权模式、主管 Agent 判定、ENSEMBLE 选择与 `CancelExecution` 调用）；06（`Retriever.BuildInjection` / `RecordInclusion` / `RecordCitations`、`SkillCatalog.ForAgent`（第 6 层技能）、`MemoryWriter.Propose`）；07（`allow_full_auto`、`infra_retry_limit`、ENSEMBLE 上限、单工作项预算、`ProjectGate.CanStartExecution`、预算事件 `budget.threshold_reached{threshold_pct}`）；08（AuthorityChecker 含 code_merge 委托条件与 `POLICY:ensemble_branch`、升级规则消费 INCONCLUSIVE 与相似度）；09（JobQueue、outbox、SSE、Notifier、对象存储）；底座 files（需提供"服务端以系统身份写入项目 entity 根并绑定"的能力 —— 见 Q2）、audit、dlock、authn step-up。

**风险**：① 运行时 SDK 事件形态变化 → 适配器契约测试（夹具桥）隔离；⑤ 预览中运行的是 Agent 写的不可信代码 → 独立预览域 + 每会话子域 + 访问判定逐请求（BR-03-114，04 BR-04-94）；⑥ 交互终端在无沙箱的工作站上可越出工作区 → 开关默认关闭、仅所有者开启、step-up、全程录制（BR-03-125）；② 证据量大（日志）→ 对象存储驱动（09）与保留策略；③ 运行时副作用不可撤销 → 栅栏 + 中断不重放 + UI 强提示；④ token 估算偏差 → 偏保守，并在 `context_bundle_built` 埋点回看。

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
| Q9 | 甲方分享链接的分期（D119 未列） | **已决（2026-10-12）：v1.1**：MVP 先完成内部预览闭环；分享涉及对外暴露与审批收件箱，按「预览是进度不是交付」放在交付流程成熟之后 |
| Q10 | 预览配置的结构化来源 | 本规格约定宪法内 `preview` 围栏块与模板 `params.preview`（BR-03-105）；若 07 / 11 希望改为独立字段，由其规格提出，03 只依赖解析结果 |

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

> **D112 / D113 已并入（00 v2.0 §15）**：新增依赖 06 `MemoryWriter.Propose`（穿透学习）、11 `EvalSetWriter.ProposeCase`（已在 11 §6.8 定义，由 02 调用）、04 `browser_capture` 能力标记、02 `plan.approved` 事件与 `max_internal_rounds` 判断；对 00 无新增变更请求（§15 已定义所用枚举、事件与权限码）。

> **D119（远程工作区，2026-10-12）已并入**：§1 范围、US-03-28..36、表 §3.24–3.28（预览会话、分享链接、终端会话、接管字段、模块内枚举与 S8 `PreviewConfig`）、§4.y 状态机、BR-03-104..132、接口 A55–A75 与机器消息、`PreviewAccessResolver`、错误码 130460–130469 / 130472 / 130473、§7.y 任务与事件、页面 §8.6–8.8、通知 N10–N12、AC-03-89..107。**CR-03-09**（对 00 / 08，已并入 00 v2.0 D119 补充）：权限码 `terminal_interact`、`workspace_file_edit`、`preview_share_approve`（execution 注册，DataScoped；前两者与第三者均仅人类，`terminal_interact`、`preview_share_approve` 不可委托）；事件 `share_link.requested` / `share_link.decided` / `share_link.ended`；接口 `PreviewAccessResolver`（execution → fleet）；08 登记 step-up `terminal_interactive`、收件箱 `PREVIEW_SHARE_APPROVAL`、原因码 `MACHINE_ACCESS_DISABLED` / `PORT_NOT_OWNED` / `PATH_OUTSIDE_WORKSPACE` 与审计事件。分期：服务 / 评审预览、文档图片 PDF 报告查看器、只读文件、只读终端为 MVP；交互终端、文件编辑与接管、视频与章节查看器为 v1.1；分享链接分期待定（Q9）。

> **D120（锚定批注与过期上下文写入，2026-10-12）已并入**：§1 范围、US-03-37…41、表 §3.29–3.31（批注线程、批注、转换记录、S9 锚点与模块内枚举）、§4.z、BR-03-133…146、接口 A76–A82、`ArtifactComments`、错误码 130474–130481、事件 `comment.*`、§8.9、N13 / N14、AC-03-108…119。**CR-03-10**（对 00，已登记入 00 v2.0 D120 补充）：§5 共享枚举 `AnchorKind`（CODE_LINES, TABLE_ROW, HTML_REGION, MEDIA_TIME, TEXT_PARAGRAPH）与 `CommentVisibility`（INTERNAL, CLIENT），所有者 03，02 / 07 / 12 读取；§6.1 权限码 `artifact_comment_write`；§7.1 事件 `comment.thread_created`、`comment.posted`、`comment.thread_resolved`、`comment.converted`；§8 接口 `ArtifactComments`；§9 过期上下文写入约定（与 05 CCR-05-11 同）。依赖：02 `WorkItemActivityRecorder` / `WorkItemActivityReader`（BR-02-64）、07 `DeliveryEvidenceReader.ClientFacing`（BR-07-63）、04 批注辅助脚本注入（BR-04-104）。分期按 D119 查看器：视频时间点与章节段落批注为 v1.1，其余 MVP。

> **D120 第 6 项（团队工具目录）已并入**：§3.32 执行表 `toolset` / `toolset_revision`（S11 快照）、BR-03-147（工具集快照即证据）、BR-03-148（`interaction_request` 判定）、§6.z 增补（`execution.start.toolset`、`leases.renew.tool_tokens`、`TOOLSET_UNAVAILABLE`、交互 `closed_reason` 增加 `CAPABILITY_NOT_AUTHORIZED`）、AC-03-120 / 121。**CR-03-11**（对 00，已登记入 00 v2.0 D120 补充）：§6.1 权限码 `interaction_request`（execution 注册，DataScoped，可委托，默认授予全部会运行执行的角色，08 §3.3d）；依赖 05 `ToolsetResolver`（fleet → execution）；不新增共享枚举与事件。
