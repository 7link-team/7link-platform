# 02 · 工作项与评审（需求基线 · 工作项 · 分派 · 完成声明 · 裁决 · 范围变更 · 拆解模板）

> 模块：`delivery`（第一部分；项目 / 交付包 / 验收见 `07`，同属 `delivery` 模块）｜上位文档：`PRD.md` §4、§5、§6.3、§6.6、§7、§8、§10.4、§11.3、§12；契约：`00-conventions.md`（本规格不重定义其中任何内容，变更见 §13）。
> 标注约定：「本规格提议」= 超出 PRD 的具体化，后附一句理由；未标注者均可追溯到 PRD / 00 / 立项基线（doc3）/ 评审报告（doc2）/ 旧 PRD v2 §13、§23.A、D.5、F.10。

---

## 1. 范围与目标

**解决**：

1. 需求（Requirement）的草稿 → 基线化 → 版本化；验收标准（AcceptanceCriterion）逐条可寻址，作为证据覆盖映射的锚点。
2. 工作项（WorkItem）：类型、风险、估算、父子（汇总项）、依赖 DAG（含环检测）、完整状态机（状态所有者、守卫、系统驱动迁移）。
3. 分派（Assignment）：认领 / 分派 / 租约 / 续约 / 过期回收 / 评审认领。
4. 完成声明（Claim，L1）与「验收标准 ↔ 证据」映射、覆盖率计算。
5. 裁决（Decision）：APPROVE / REJECT / REQUEST_CHANGES / PAUSE / RESUME / SCOPE_CHANGE / ASSIGN / CANCEL 的授权（经 08 `AuthorityChecker`）、理由、结构化反馈、职责分离、并发竞争、同事务审计。
6. 失败分类 → 返工分支请求（交 03 创建执行）；有界自治（质量返工 ≥3 或相邻产物变化比例 <8% → 暂停并升级）。
7. 变更记录（ChangeRecord）与需求版本 +1、受影响工作项「基线过期」。
8. 拆解模板（`software-dev-agile` 等）：模板 JSON Schema、版本化、实例化为工作项树。
9. 评审台人工关注时长计量。
10. 页面：工作项看板、工作项列表、工作项详情、评审台、需求页。

**不解决（边界）**：

| 内容 | 归属 |
|---|---|
| 来件、分诊、澄清线程、来件 → 需求转换的界面与流程 | 01（经 §6.9 `RequirementCreator` 调用本模块） |
| 执行、分支树数据、交互会话、证据行、验证器与验证结果、改动相似度计算 | 03（本规格只引用其 ID 与读接口） |
| 机器、执行槽 | 04 |
| Agent 档案与角色、主管 Agent 派单提案、Heavy 并行 | 05（经本规格 §6.9 `DispatchAssigner` **同步**落库分派与 ASSIGN 裁决；本规格不消费 `dispatch.applied`） |
| 知识注入与「使用的知识」数据 | 06（评审台只嵌入展示） |
| 项目阶段、团队模式、预算、交付包、甲方验收、DELIVER 裁决 | 07 |
| 权限目录维护、委托、升级规则、通知渠道、「我的裁决」聚合、审计页 | 08 |
| 后台任务队列、outbox、SSE、通知发送 | 09 |
| 内部验收的执行（测试 Agent / 产品验收 Agent 执行种类、视觉验证器）、平台重跑、冻结与隐藏验收集、测试 diff 检测、`unverified[]` | 03 |
| TESTER / ACCEPTOR 的派单与跨模型家族约束 | 05 |
| 云效对齐的流转矩阵配置、状态组、需求评审单、用例即验收标准、工时 | 12 |
| 领域模板中的就绪默认值、验证计划默认、`max_internal_rounds`、评分标准 | 11 |

> **D112 / D113（00 §15，v2.0）在本规格的落地**：就绪闸门、计划审批、内部质量门与不穿透路由、人类负责人、Andon 停线、容差与例外、穿透记录、云效缺陷字段——见 §3.20–§3.25、§4.5、§5.11、§6.10、§7.4、§8.6、§9（N11–N14）、§10.11、§11。**L2 的含义改为「内部验收通过」**（自动检查 + 测试 Agent + 产品验收 Agent）。

---

## 2. 用户故事

| ID | 故事 | 期 |
|---|---|---|
| US-02-01 | 作为交付负责人，我要把需求草稿基线化为 v1，并逐条维护验收标准，以便工作项与证据有稳定的对齐锚点 | MVP |
| US-02-02 | 作为交付负责人，我要用 `software-dev-agile` 模板一键把需求拆成工作项树与依赖，以便不用手工建十几个卡片 | MVP |
| US-02-03 | 作为交付负责人，我要把工作项分派给人或 Agent，并看到「为什么派给它」，以便责任清晰可追溯 | MVP |
| US-02-04 | 作为工程师，我要认领就绪的工作项并续约，以便别人不会重复认领、我离开后又能被回收 | MVP |
| US-02-05 | 作为执行者（人或 Agent），我要提交完成声明并按验收标准逐条挂证据，以便评审者知道哪些已被证据覆盖 | MVP |
| US-02-06 | 作为评审者，我要在评审台按队列认领、看证据来源 / 验证 / diff / 覆盖 / 使用的知识，再以明确的身份与权限来源裁决，以便高质量低成本地裁决 | MVP |
| US-02-07 | 作为评审者，我要拒绝时选择失败类别并填写结构化反馈，以便返工分支带着可执行的反馈启动 | MVP |
| US-02-08 | 作为团队所有者，我要系统在同一工作项质量返工第 3 次或原地打转时自动暂停并升级给我，以便 Agent 不会无限消耗预算 | MVP |
| US-02-09 | 作为交付负责人，我要发起范围变更并让需求升版本、受影响工作项标记基线过期，以便旧证据不会被误当作新需求的证明 | MVP |
| US-02-10 | 作为交付负责人，我要在看板上拖拽卡片，非法拖拽被拒并告诉我原因，以便看板不能绕过状态机 | MVP |
| US-02-11 | 作为团队所有者，我要每个裁决自动记录评审者的人工关注时长，以便计算「每个已接受交付物的人工关注分钟」 | MVP |
| US-02-12 | 作为 Agent 评审员（经委托），我要在委托范围内裁决低风险工作项，且永远不能裁决自己产出的工作项 | MVP |
| US-02-13 | 作为工程师，我要在执行故障时一键重试或转交，且不消耗质量返工次数 | MVP |
| US-02-14 | 作为交付负责人，我要维护依赖并被阻止形成环，以便调度顺序可靠 | MVP |
| US-02-15 | 作为团队所有者，我要维护团队自定义拆解模板并版本化，以便不同类型项目复用 | v1.1 |
| US-02-16 | 作为评审者，我要对比策略变更产生的两个并行分支的证据后择一，以便保留替代方案 | v1.1（MVP 仅创建分支 + 详情页并列查看） |
| US-02-17 | 作为交付负责人，我要工作项在派给 Agent 前必须写清目标、带编号的验收标准、每条标准的验证方式、预算与人类负责人，以便 Agent 不会在含糊的需求上开工 | MVP |
| US-02-18 | 作为交付负责人，我要中高风险工作项先出计划、经人或不同模型的评审者批准后才实现，以便方向错误在动手前被拦住 | MVP |
| US-02-19 | 作为交付负责人，我只想看到已通过内部验收（自动检查、测试 Agent、产品验收 Agent）的工作项；内部不通过的直接打回内部返工，不打扰我 | MVP |
| US-02-20 | 作为任何成员（含 Agent），我要能拉 Andon 停线，以便发现严重问题时下游立即停止 | MVP |
| US-02-21 | 作为交付负责人，我要工作项预测将超出时间 / 成本 / 范围容差时自动进入例外，并看到现状、选项与建议，由我决定继续 / 重新塑形 / 暂停 / 终止 | MVP |
| US-02-22 | 作为团队所有者，我要每次人或甲方发现内部已放行的问题都被记为「穿透」，并回流成测试与验收 Agent 的经验和模板评测用例 | MVP |

---

## 3. 领域模型与数据表

### 3.1 逻辑类型 → 三方言映射（本节所有字段表使用左列逻辑类型）

| 逻辑类型 | PostgreSQL | MySQL 8.0.16+ | SQLite | 备注 |
|---|---|---|---|---|
| `ID` | `BIGINT` | `BIGINT` | `INTEGER` | 雪花 ID；JSON 十进制字符串 |
| `TS` | `TIMESTAMPTZ(3)` | `DATETIME(3)` | `DATETIME` | UTC（00 §3） |
| `BOOL` | `BOOLEAN` | `TINYINT(1)` | `INTEGER CHECK (x IN (0,1))` | |
| `INT` / `SMALLINT` | `INTEGER` / `SMALLINT` | `INT` / `SMALLINT` | `INTEGER` | |
| `BIGINT` | `BIGINT` | `BIGINT` | `INTEGER` | 毫秒、计数 |
| `STR(n)` | `VARCHAR(n)` | `VARCHAR(n)`（utf8mb4） | `TEXT CHECK (length(x) <= n)` | |
| `ENUM` | `VARCHAR(32)` + `CHECK (x IN (...))` | 同左 | `TEXT CHECK (x IN (...))` | 共享枚举见 00 §5；模块内枚举（`RequirementStatus` 等，见 §13 CCR-02）在本规格定义，不出现在其他模块的表或事件载荷 |
| `LTEXT` | `TEXT` | `MEDIUMTEXT` | `TEXT` | ≤64KB 字符（代码校验 65536 字符；MySQL `TEXT` 按字节 64KB 不足以容纳中文，故用 MEDIUMTEXT） |
| `JSON` | `JSONB` | `JSON` | `TEXT CHECK (json_valid(x))` | Schema 在字段说明内给出 |
| `HASH` | `CHAR(64)` | `CHAR(64)` | `TEXT CHECK (length(x)=64)` | SHA-256 hex |

通用列（每张表都有，下文不再重复列出）：`id ID PK`、`tenant_id ID NOT NULL`、`created_at TS NOT NULL`、`created_by ID NULL`（NULL = 系统）。可变聚合根另有 `updated_at TS NOT NULL`、`updated_by ID NULL`、`version INT NOT NULL DEFAULT 1`。除 `tenant_id` 外，所有项目内资源有 `project_id ID NOT NULL`（= Entity id，type=`project`）。所有对象名带 `{{.Prefix}}`；迁移目录 `internal/delivery/sql/{postgres,mysql,sqlite}/`，版本表 `goose_delivery`（与 07 共用一个模块版本表）。跨模块 ID（execution、evidence、agent、intake、principal）不建外键。模块内外键：PG/MySQL 建 `FOREIGN KEY ... ON DELETE RESTRICT`；SQLite 同样声明（底座连接开启 `foreign_keys=ON`）。

### 3.2 `delivery_project_counters`（本规格提议：人类可读编号 `REQ-12` / `WI-42` 需项目内单调序号）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | UNIQUE(tenant_id, project_id) |
| next_requirement_no | INT | N | 1 | 分配时 `UPDATE ... SET n=n+1` 并读回（同事务） |
| next_work_item_no | INT | N | 1 | 同上 |
| updated_at | TS | N | | |

### 3.3 `delivery_requirements`（可变聚合根）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| number | INT | N | | 项目内序号，展示 `REQ-{number}`；UNIQUE(tenant_id, project_id, number) |
| title | STR(200) | N | | 非空白 |
| description | LTEXT | N | `''` | Markdown |
| priority | SMALLINT | N | 2 | 0–3（P0 最高）；CHECK |
| status | ENUM `RequirementStatus` | N | `DRAFT` | `DRAFT`/`BASELINED`/`ARCHIVED`（CCR-02） |
| current_version_no | INT | N | 0 | 0 = 未基线化；基线化后 ≥1 |
| source_intake_id | ID | Y | | 01 来件 id |
| archived_at | TS | Y | | 仅无非终态工作项时可归档 |
| version / updated_* | | | | 乐观锁 |

索引：`(tenant_id, project_id, status, priority, number)`；`(tenant_id, source_intake_id)`。

### 3.4 `delivery_acceptance_criteria`

基线化后**标准行不可改文本**：修改 = 退役旧行 + 新增行（`supersedes_id` 指向旧行），使任何「标准 ↔ 证据」映射永远指向它当时所证明的那段文字（本规格提议：避免证据被静默改义）。

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| requirement_id | ID | N | | FK → delivery_requirements |
| code | STR(16) | N | | 需求内序号 `C1`、`C2`…，退役不复用；UNIQUE(tenant_id, requirement_id, code) |
| text | STR(2000) | N | | |
| required | BOOL | N | true | false = 期望项，不计入覆盖率分母 |
| position | SMALLINT | N | | 展示顺序 0–49 |
| introduced_in_version | INT | N | 0 | 0 = 草稿期新增；基线化时回填 1 |
| retired_in_version | INT | Y | | 非空 = 自该版本起退役 |
| supersedes_id | ID | Y | | FK → 本表 |
| archived_at | TS | Y | | 仅 DRAFT 期删除使用 |

索引：`(tenant_id, requirement_id, retired_in_version, position)`。上限：每需求活跃标准 ≤50。

### 3.5 `delivery_requirement_versions`（不可变快照）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id / requirement_id | ID | N | | FK requirement |
| version_no | INT | N | | UNIQUE(tenant_id, requirement_id, version_no)；每需求 ≤200 版 |
| title / description / priority | 同 3.3 | N | | 快照 |
| criteria | JSON | N | | `[{id, code, text, required, position}]`，该版本活跃标准，按 position |
| change_record_id | ID | Y | | v1 为空；之后必填 |
| content_hash | HASH | N | | SHA-256(规范化 JSON：title, description, priority, criteria) |

### 3.6 `delivery_work_items`（可变聚合根）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| requirement_id | ID | N | | FK；同项目 |
| number | INT | N | | `WI-{number}`；UNIQUE(tenant_id, project_id, number) |
| parent_id | ID | Y | | FK → 本表；同项目；深度 ≤4 |
| is_container | BOOL | N | false | 有子项即 true（系统维护），见 BR-02-30 |
| title | STR(200) | N | | |
| description | LTEXT | N | `''` | |
| type | ENUM `WorkItemType` | N | | 00 §5 |
| risk | ENUM `RiskLevel` | N | `MEDIUM` | 00 §5 |
| estimate_minutes | INT | Y | | 15–4800（≤10 人日）；NULL 阻止进入 READY |
| state | ENUM `WorkItemState` | N | `BLOCKED` | 00 §5；新建初始状态由守卫计算（§4.2） |
| block_reasons | JSON | N | `[]` | `BlockReason[]` 去重有序；仅 state=BLOCKED 时非空 |
| block_note | STR(2000) | Y | | 人工阻塞说明 |
| paused_at | TS | Y | | 非空 = 暂停覆盖层生效（§4.4） |
| pause_reason | ENUM `PauseReason` | Y | | CCR-02  **状态：已处理（00 v1.1 §3：模块内枚举，不进 §5；`ExecutionFailureKind` 由 03 定义）** |
| pause_decision_id | ID | Y | | 引发暂停的裁决（系统暂停为空） |
| require_human_decision | BOOL | N | false | 模板评审门 / 升级后置 true：委托 Agent 不可裁决 |
| required_runtime | ENUM `RuntimeKind` | Y | | 派单硬过滤（05 BR-05-4x）；空 = 不限 |
| skill_tags / preferred_skill_tags | JSON | N | `[]` | `string[]` ≤20，每项 ≤40 字符；05 派单技能匹配（必需 / 加分） |
| uncertainty_flag | BOOL | N | false | 不确定性标记；05 Heavy 并行触发条件之一 |
| baseline_version_no | INT | N | | 工作项对齐的需求版本 |
| baseline_stale | BOOL | N | false | 需求版本 > baseline_version_no 且受影响 |
| baseline_stale_since | INT | Y | | 首次过期的需求版本号 |
| current_claim_id | ID | Y | | 最近一次有效声明 |
| current_execution_id | ID | Y | | 03 执行 id（最近一次分支） |
| quality_rework_count | SMALLINT | N | 0 | QUALITY 类返工次数（含自动验证失败） |
| infra_retry_count | SMALLINT | N | 0 | 当前执行链上的执行故障自动重试次数；新质量分支归零 |
| needs_judgement | BOOL | N | false | 必需验证存在 INCONCLUSIVE |
| first_claimed_at / accepted_at / cancelled_at | TS | Y | | 指标用 |
| first_pass | BOOL | Y | | **首次** ACCEPTED 时写入：quality_rework_count=0；T26 重开后不改写 |
| accepted_decision_id | ID | Y | | 最近一次 APPROVE 的 Decision（07 交付包与 L3 展示） |
| first_accepted_at | TS | Y | | 首次 ACCEPTED 时间（T26 重开不清除，07 指标用） |
| self_confidence | SMALLINT | Y | | 冗余自最近声明（0–100），供 08 `WorkItemFacts` |
| decomposition_id / template_node_key | ID / STR(64) | Y | | 模板来源 |
| template_stage_key | STR(64) | Y | | 领域模板阶段键（11 `TemplateResolver.WorkItemBlueprint` 产出，如 `A4`、`tech_spec`）；工作台与评审台按阶段聚合 / 过滤（W 系列列表 `template_stage_key`(eq,in)）；创建后只读，非模板来源为空 |
| state_changed_at | TS | N | | |
| archived_at | TS | Y | | 仅 CANCELLED 可归档 |
| accountable_principal_id | ID | N | | **人类负责人**（D112 A）：创建时必填，默认 = 需求负责人；必须是 HUMAN 成员；全自治项目 = 委托授予人（BR-02-49） |
| autonomy_level | ENUM(`LOW`,`MEDIUM`,`HIGH`) | N | `MEDIUM` | 就绪字段；决定是否需要计划审批（BR-02-51） |
| objective | STR(2000) | Y | | 就绪字段：本工作项要达成的结果 |
| out_of_scope | JSON | N | `[]` | 就绪字段：`string[]` ≤20 |
| context_refs | JSON | N | `[]` | 就绪字段：`[{kind: FILE\|DOC\|URL\|WORK_ITEM\|KNOWLEDGE, ref}]` ≤50 |
| verification_plan | JSON | N | `[]` | 就绪字段：`[{criterion_id, method: TEST\|COMMAND\|AGENT_JUDGE\|HUMAN\|VISUAL, detail}]`，须覆盖本工作项负责的每条验收标准（BR-02-48） |
| budget | JSON | Y | | 就绪字段：`{tokens?, usd_micros?, wall_clock_minutes?, max_internal_rounds}`；`max_internal_rounds` 缺省取模板（默认 5） |
| readiness_ok | BOOL | N | false | 最近一次就绪检查是否通过 |
| readiness_issues | JSON | N | `[]` | `[{code, field?, criterion_id?, message_key}]`；code ∈ `MISSING_FIELD` / `AC_WITHOUT_METHOD` / `NO_ACCOUNTABLE_HUMAN` / `REQUIREMENT_CONFLICT` / `REQUIREMENT_AMBIGUOUS` / `UNVERIFIABLE_AC` |
| readiness_override_by | ID | Y | | 人类在 DRAFT 上强行认领时记录（BR-02-47） |
| plan_required | BOOL | N | false | 由 BR-02-51 计算 |
| current_plan_id | ID | Y | | 最近一次计划（§3.21） |
| internal_qa_required | BOOL | N | true | 执行者为 AGENT 且团队模式 ≠ HUMAN_ONLY 时恒为 true；人类执行者按模板（默认 false，只做 AUTOMATED） |
| internal_round_count | SMALLINT | N | 0 | 当前声明链上内部不通过的轮次（D113，BR-02-54） |
| escalation_reason | ENUM `EscalationReason` | Y | | 00 §5；最近一次升级到人的原因 |
| appetite_minutes | INT | Y | | 时间预算（Shape Up）；与 tolerance 共同决定例外（BR-02-57） |
| tolerance | JSON | N | `{"time_pct":20,"cost_pct":20,"scope":"NO_NEW_CRITERIA"}` | 容差 |
| stopped_from_state | ENUM `WorkItemState` | Y | | Andon 前的状态（§3.22） |
| active_exception_id | ID | Y | | §3.23 |
| escape_count | SMALLINT | N | 0 | 本工作项累计穿透次数（§3.24） |
| severity / priority | ENUM(`FATAL`,`CRITICAL`,`NORMAL`,`MINOR`) / ENUM(`URGENT`,`HIGH`,`MEDIUM`,`LOW`) | Y | | 缺陷字段（云效：致命 / 严重 / 一般 / 轻微；紧急 / 高 / 中 / 低）；`type ∈ {BUG, INCIDENT}` 时必填 |
| reproducibility | ENUM(`ALWAYS`,`HIGH`,`MEDIUM`,`LOW`,`UNABLE`) | Y | | 缺陷：复现率 |
| found_version / fixed_version | STR(64) | Y | | 缺陷：发现版本 / 修复版本 |
| introduced_stage / introduced_reason / module | STR(64) / STR(200) / STR(100) | Y | | 缺陷：引入阶段 / 引入原因 / 所属模块（取值字典由 12 配置） |
| defect_stage | ENUM(`MERGED`,`DEPLOYED`,`VERIFIED`,`CLOSED`) | Y | | 缺陷生命周期（云效「已修复」拆为 已合入 → 已部署 → 已验证 → 已关闭）；推进条件由 12 的流转矩阵配置，`CLOSED` 等价于 ACCEPTED |
| version / updated_* | | | | 每次迁移 +1 |

索引：`(tenant_id, project_id, state, number)`；`(tenant_id, project_id, requirement_id)`；`(tenant_id, parent_id)`；`(tenant_id, project_id, baseline_stale)`；`(tenant_id, state, state_changed_at)`（SLA 扫描）。上限：每项目 ≤5000 个未归档工作项，每父 ≤200 子项。

### 3.7 `delivery_work_item_criteria`（工作项负责的验收标准）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id / work_item_id / criterion_id | ID | N | UNIQUE(tenant_id, work_item_id, criterion_id)；标准须属于同一需求且在工作项 baseline 版本中活跃 |

### 3.8 `delivery_work_item_dependencies`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id | ID | N | |
| work_item_id | ID | N | 依赖方（后继） |
| depends_on_id | ID | N | 被依赖方（前驱）；同项目；≠ work_item_id |

UNIQUE(tenant_id, work_item_id, depends_on_id)；索引 `(tenant_id, depends_on_id)`。语义只有完成→开始（FS）：前驱 `ACCEPTED` 即满足。每工作项前驱 ≤50。

### 3.9 `delivery_assignments`（可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id / work_item_id | ID | N | | FK work_item |
| role | ENUM `AssignmentRole` | N | | `EXECUTOR` / `REVIEWER`（REVIEWER = 评审认领） |
| assignee_kind | ENUM | N | | `HUMAN` / `AGENT`（00 §4 actor_kind 子集） |
| assignee_principal_id | ID | N | | |
| agent_id | ID | Y | | AGENT 必填 |
| status | ENUM `AssignmentStatus` | N | `ACTIVE` | `ACTIVE`/`RELEASED`/`EXPIRED`/`REVOKED`/`COMPLETED`（CCR-02） |
| lease_ttl_ms | BIGINT | N | | 见 BR-02-12 |
| lease_expires_at | TS | Y | | NULL = 租约挂起（BLOCKED / 暂停期间） |
| lease_renewed_at | TS | Y | | |
| decision_id | ID | Y | | ASSIGN 裁决（自认领为空） |
| ended_at | TS | Y | | |
| end_reason | STR(64) | Y | | `RELEASED_BY_ASSIGNEE`/`LEASE_EXPIRED`/`REASSIGNED`/`WORK_ITEM_TERMINAL`/`AGENT_RETIRED`/`DECIDED` |
| ensemble_plan_id | ID | Y | | 非空 = Heavy 并行分支的分派（05 `ensemble_plans`）；不参与下方唯一性（v1.1，C58） |
| active_executor_slot | ID | Y | | 仅 MySQL：生成列，见下 |
| version / updated_* | | | | |

唯一性（每工作项同一时刻最多一个 ACTIVE EXECUTOR、一个 ACTIVE REVIEWER）：

| 方言 | 写法 |
|---|---|
| PostgreSQL / SQLite | `CREATE UNIQUE INDEX {{.Prefix}}ux_assign_active ON {{.Prefix}}delivery_assignments (tenant_id, work_item_id, role) WHERE status = 'ACTIVE' AND ensemble_plan_id IS NULL` |
| MySQL | 生成列 `active_slot VARCHAR(48) AS (CASE WHEN status='ACTIVE' AND ensemble_plan_id IS NULL THEN CONCAT(work_item_id, ':', role) END) STORED` + `UNIQUE (tenant_id, active_slot)`（NULL 不参与唯一） |

索引：`(status, lease_expires_at)`（回收扫描）；`(tenant_id, assignee_principal_id, status)`。

### 3.10 `delivery_claims`（L1，不可变，仅 outcome 由系统更新）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id / work_item_id | ID | N | | |
| execution_id | ID | N | | 03 执行；须属于该工作项 |
| sequence | SMALLINT | N | | 工作项内第几次声明；UNIQUE(tenant_id, work_item_id, sequence) |
| summary | STR(2000) | N | | |
| self_confidence | SMALLINT | Y | | 0–100，Agent 自评；08 升级规则可读 |
| head_commit | STR(64) | Y | | 声明时工作目录 HEAD 提交（PG `VARCHAR(64)` / MySQL `VARCHAR(64)` / SQLite `TEXT`）；03 验证与合并守卫据此锁定被验证版本；`claim.submitted` 携带（复审 #14） |
| actor_kind / actor_id / agent_id | ENUM / ID / ID | N/N/Y | | 声明者 |
| baseline_version_no | INT | N | | 声明时工作项基线 |
| outcome | ENUM `ClaimOutcome` | N | `PENDING` | `PENDING`/`ACCEPTED`/`REWORK`/`SUPERSEDED`/`CANCELLED`（CCR-02） |
| coverage_required / coverage_covered | SMALLINT | N | | 声明时快照：必需标准数 / 已覆盖数 |
| similarity_ratio | INT | Y | | 相邻产物变化比例 ×10000（03 提供；首个分支为空） |
| outcome_decision_id | ID | Y | | |

### 3.11 `delivery_claim_evidence`（声明 ↔ 证据 ↔ 标准映射）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id / claim_id | ID | N | FK claim |
| evidence_id | ID | Y | 03 证据；`coverage=NOT_APPLICABLE` 时为空 |
| criterion_id | ID | Y | 空 = 仅作为声明的一般证据 |
| coverage | ENUM `CriterionCoverage` | Y | `COVERED`/`PARTIAL`/`NOT_APPLICABLE`；criterion_id 非空时必填 |
| note | STR(2000) | Y | NOT_APPLICABLE 必填理由 |

UNIQUE(tenant_id, claim_id, evidence_id, criterion_id)（PG 用 `NULLS NOT DISTINCT`；MySQL/SQLite 以 `COALESCE(...,0)` 生成列实现）。每声明 ≤500 行，证据 ≥1 条（BR-02-17）。

### 3.12 `delivery_decisions`（不可变）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id | ID | N | |
| target_kind | ENUM | N | `WORK_ITEM` / `REQUIREMENT` / `DELIVERY` / `PROJECT`（后两者由 07 写入：DELIVER、项目阶段 / 模式裁决） |
| target_id | ID | N | |
| work_item_id / claim_id | ID | Y | |
| action | ENUM `DecisionAction` | N | 00 §5 |
| actor_kind / actor_id / agent_id | | N/N/Y | |
| authority_source | STR(96) | N | 00 §6.3 格式，来自 AuthorityChecker |
| delegation_id | ID | Y | authority_source 为 DELEGATION 时 |
| reason | STR(2000) | Y | 必填规则 BR-02-22 |
| failure_category | ENUM `FailureCategory` | Y | REJECT/REQUEST_CHANGES/SCOPE_CHANGE 必填 |
| from_state / to_state | ENUM `WorkItemState` | Y | 工作项目标 |
| target_version | INT | N | 裁决时目标的 version（If-Match） |
| evidence_refs | JSON | N | `["<evidence_id>", ...]` ≤100 |
| verification_refs | JSON | N | `["<verification_id>", ...]` ≤100 |
| payload | JSON | N | 动作专属：ASSIGN `{assignment_id, assignee_principal_id, agent_id?, replaced_assignment_id?, rationale, dispatch_proposal_id?, dispatched_by_principal_id?}`；REQUEST_CHANGES(T26) `{source_acceptance_id}`；系统质量返工(T12) `{verification_batch_id, verification_ids[]}`；PAUSE `{pause_reason}`；SCOPE_CHANGE `{change_record_id}`；REJECT/REQUEST_CHANGES `{workspace_mode: RESTART|CONTINUE, branch_execution_id}` |
| attention_ms | BIGINT | Y | 人类裁决时汇总（§5.8） |
| idempotency_key | STR(64) | Y | |

索引：`(tenant_id, work_item_id, created_at)`；`(tenant_id, project_id, action, created_at)`；`(tenant_id, actor_id, created_at)`。

### 3.13 `delivery_decision_feedback`（结构化反馈，不可变）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id / decision_id | ID | N | FK decision |
| position | SMALLINT | N | 0–19；每裁决 ≤20 条 |
| category | ENUM `FeedbackCategory` | N | 00 §5 |
| location | STR(500) | Y | 文件:行、页面路径、组件 |
| expected | STR(2000) | Y | |
| detail | STR(2000) | N | ≥5 字符 |
| criterion_id | ID | Y | 关联验收标准 |
| evidence_id | ID | Y | 关联证据 |

### 3.14 `delivery_change_records` 与 `delivery_change_impacts`

`delivery_change_records`（不可变）：

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id / requirement_id | ID | N/Y | STRATEGY 类可无需求版本变化但仍记 requirement_id |
| kind | ENUM `ChangeRecordKind` | N | `SCOPE` / `STRATEGY`（CCR-02） |
| work_item_id | ID | Y | STRATEGY 必填 |
| from_version_no / to_version_no | INT | Y | SCOPE 必填，to = from+1 |
| summary | STR(2000) | N | |
| diff | JSON | N | `{title?:{from,to}, description_changed:bool, priority?:{from,to}, criteria:{added:[id], retired:[id], superseded:[{from,to}]}}` |
| decision_id | ID | N | 对应 SCOPE_CHANGE 或 REQUEST_CHANGES 裁决 |
| source_intake_id | ID | Y | 甲方新要求来源 |

`delivery_change_impacts`：`project_id, change_record_id, work_item_id, impact ENUM('STALE','CANCELLED','NONE')`，UNIQUE(tenant_id, change_record_id, work_item_id)。

### 3.15 `delivery_work_item_transitions`（迁移历史 + 系统迁移幂等）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id / work_item_id | ID | N | |
| from_state / to_state | ENUM | Y/N | 新建时 from 为空；覆盖层变化（暂停/恢复/过期标记）from=to |
| trigger | ENUM | N | `COMMAND` / `DECISION` / `SYSTEM` |
| actor_kind / actor_id / authority_source | | | 00 §4 三元组 |
| decision_id | ID | Y | |
| reason_code | STR(64) | N | 如 `CLAIM_SUBMITTED`、`VERIFICATION_PASSED`、`LEASE_EXPIRED`、`DEPENDENCY_SATISFIED` |
| source_ref | STR(128) | Y | 系统迁移去重键，如 `verification-batch:<id>`；UNIQUE(tenant_id, work_item_id, source_ref) |
| version_after | INT | N | |

### 3.16 拆解模板：`delivery_decomposition_templates`、`delivery_decomposition_template_versions`、`delivery_decompositions`

`delivery_decomposition_templates`（可变，团队级，无 project_id）：`key STR(64)`（`^[a-z][a-z0-9-]{2,63}$`，UNIQUE(tenant_id,key)）、`name STR(200)`、`status ENUM TemplateStatus('ACTIVE','DEPRECATED')`、`latest_version_no INT`、`builtin BOOL`（内置模板随租户初始化写入，`builtin=true` 不可编辑只可复制）、version/updated_*。

`delivery_decomposition_template_versions`（不可变）：`template_id ID FK`、`version_no INT`（UNIQUE(tenant_id, template_id, version_no)）、`definition JSON`（§3.17 Schema）、`schema_version SMALLINT`（=1）、`content_hash HASH`、`changelog STR(2000)`。

`delivery_decompositions`（一次实例化记录，不可变）：`project_id, requirement_id, template_version_id ID`、`parameters JSON`、`root_work_item_id ID`、`created_count SMALLINT`、`idempotency_key STR(64)`。

### 3.17 模板定义 JSON Schema（`definition` 列，schema_version=1）

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delivery/decomposition-template/v1",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "key", "name", "root", "nodes"],
  "properties": {
    "schema_version": { "const": 1 },
    "key": { "type": "string", "pattern": "^[a-z][a-z0-9-]{2,63}$" },
    "name": { "$ref": "#/$defs/i18nText" },
    "description": { "$ref": "#/$defs/i18nText" },
    "parameters": { "type": "array", "maxItems": 10, "items": {
      "type": "object", "additionalProperties": false, "required": ["key", "type", "default"],
      "properties": {
        "key": { "type": "string", "pattern": "^[a-z][a-z0-9_]{0,31}$" },
        "label": { "$ref": "#/$defs/i18nText" },
        "type": { "enum": ["integer"] },
        "minimum": { "type": "integer", "minimum": 1 }, "maximum": { "type": "integer", "maximum": 20 },
        "default": { "type": "integer" } } } },
    "root": { "type": "object", "additionalProperties": false, "required": ["title", "type"],
      "properties": { "title": { "$ref": "#/$defs/i18nText" }, "type": { "$ref": "#/$defs/type" } } },
    "nodes": { "type": "array", "minItems": 1, "maxItems": 100, "items": { "$ref": "#/$defs/node" } }
  },
  "$defs": {
    "i18nText": { "type": "object", "required": ["zh-CN", "en-US", "ms-MY"], "additionalProperties": false,
      "properties": { "zh-CN": { "type": "string", "maxLength": 200 }, "en-US": { "type": "string", "maxLength": 200 }, "ms-MY": { "type": "string", "maxLength": 200 } } },
    "type": { "enum": ["FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST"] },
    "node": { "type": "object", "additionalProperties": false, "required": ["key", "title", "type"],
      "properties": {
        "key": { "type": "string", "pattern": "^[a-z][a-z0-9-]{0,47}$" },
        "title": { "$ref": "#/$defs/i18nText" },
        "type": { "$ref": "#/$defs/type" },
        "risk": { "enum": ["LOW", "MEDIUM", "HIGH"], "default": "MEDIUM" },
        "estimate_minutes": { "type": "integer", "minimum": 15, "maximum": 4800 },
        "role_hint": { "type": "string", "maxLength": 32 },
        "require_human_decision": { "type": "boolean", "default": false },
        "criteria": { "enum": ["ALL", "NONE"], "default": "NONE" },
        "repeat": { "type": "object", "additionalProperties": false, "required": ["parameter"],
          "properties": { "parameter": { "type": "string" } } },
        "depends_on": { "type": "array", "maxItems": 20, "uniqueItems": true, "items": { "type": "string" } }
      } }
  }
}
```

模板语义校验（Schema 之外，保存与实例化时都执行）：`key` 唯一；`depends_on` 只引用已存在 node key；依赖图无环（Kahn 拓扑排序）；`repeat.parameter` 引用已声明参数；展开后工作项（含 root）≤300；被重复节点依赖的节点会依赖其**全部**展开副本（join 语义），重复节点依赖的上游对每个副本都生效（fork 语义）；重复节点标题自动追加 ` #{i}`（i 从 1 开始），`template_node_key` = `{key}#{i}`。

实例化结果：root → 汇总工作项（`is_container=true`）；每个 node → root 的子工作项；`criteria: ALL` 的节点关联需求当前版本全部活跃标准；`require_human_decision` 写入工作项；`role_hint` 写入 05 派单提案的输入（本模块只存于 `delivery_decompositions.parameters._role_hints`）。

**内置 `software-dev-agile` v1**（来源旧 PRD §23.A；`[prd-approval]`、`[arch-approval]`、`[sprint-delivery]` 三个人工门映射为对应节点 `require_human_decision=true`，`bug-fix → qa` 循环由运行期返工分支承担，不进模板）：

| node key | 类型 | 风险 | 估算(min) | 依赖 | 标准 | 人工门 | role_hint |
|---|---|---|---|---|---|---|---|
| requirements | DOC | LOW | 60 | — | NONE | 否 | pm |
| prd | DOC | MEDIUM | 120 | requirements | ALL | **是** | pm |
| design | DESIGN | MEDIUM | 180 | prd | NONE | **是** | arch |
| sprint-plan | CHORE | LOW | 60 | design | NONE | 否 | pm |
| coding（repeat `coder_count`，默认 2，1–8） | FEATURE | MEDIUM | 480 | sprint-plan | NONE | 否 | coder |
| qa | TEST | MEDIUM | 240 | coding | ALL | **是** | qa |

root：`{zh-CN: "交付：{requirement.title}", type: FEATURE}`。

### 3.18 `delivery_attention_sessions`（人工关注计量）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id / work_item_id / claim_id | ID | N/N/Y | |
| principal_id | ID | N | 仅 HUMAN |
| surface | ENUM | N | `REVIEW_DESK`（MVP 唯一值；08 的「我的裁决」卡片可复用为 `INBOX`，CCR-02） |
| started_at / last_heartbeat_at | TS | N | |
| ended_at | TS | Y | |
| active_ms | BIGINT | N | 默认 0；服务端累计 |
| last_sequence | INT | N | 心跳序号，防重放 |
| decision_id | ID | Y | 结束于裁决时回填 |

索引：`(tenant_id, claim_id, principal_id)`；`(ended_at, last_heartbeat_at)`。

### 3.19 幂等与事件消费去重（不建模块表）

- 请求幂等：使用 09 C7 平台幂等辅助（单表 `platform_idempotency_keys`，00 §9）；本模块不建 `delivery_idempotency_keys`。
- 领域事件消费去重：使用 09 C2 `domain_event_receipts`（订阅模式 `Transactional`）；本模块不建 `delivery_consumed_events`。
- 系统回调（03 `WorkItemTransitioner`）去重仍以 `delivery_work_item_transitions.source_ref` 唯一键实现（业务唯一键，非事件去重）。

---

### 3.20 就绪检查记录 `delivery_readiness_checks`（不可变）

| 字段 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| work_item_id | ID | N | | |
| trigger | ENUM(`EDIT`,`DISPATCH`,`REQUIREMENT_VERSIONED`,`MANUAL`) | N | | |
| ok | BOOL | N | | |
| issues | JSON | N | `[]` | 同 §3.6 `readiness_issues` |
| checked_by | ENUM(`SYSTEM`,`AGENT_JUDGE`) | N | `SYSTEM` | 歧义 / 冲突 / 不可验证由 AGENT_JUDGE（需求质量检查，BR-02-50）给出 |
| created_at | TS | N | | |

索引：`(tenant_id, work_item_id, created_at DESC)`。

### 3.21 计划 `delivery_plans`（每次提案一行；不可变，仅 `state` 更新）

| 字段 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| work_item_id / execution_id | ID | N | | 产出计划的只读执行（03 `purpose=PLANNING`） |
| round | SMALLINT | N | 1 | 同一工作项的计划轮次；≤3（BR-02-51） |
| content | JSON | N | | `{files[], criteria_coverage[{criterion_id, approach, verification}], risks[], out_of_scope_confirmed: bool}`（JSON Schema 由 03 定义产出格式，本模块校验覆盖） |
| coverage_ok | BOOL | N | | 计划是否覆盖本工作项全部验收标准 |
| state | ENUM(`PROPOSED`,`APPROVED`,`REJECTED`,`SUPERSEDED`) | N | `PROPOSED` | |
| decided_by / authority_source / decided_at | ID / STR(128) / TS | Y | | 批准者（人或不同模型家族的评审 Agent） |
| feedback | JSON | Y | | 拒绝时的结构化反馈（同 §3.13） |
| created_at | TS | N | | |

### 3.22 Andon 记录 `delivery_andon_events`

| 字段 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| scope | ENUM(`WORK_ITEM`,`PROJECT`) | N | | |
| work_item_id | ID | Y | | scope=WORK_ITEM 时必填 |
| pulled_by_kind / pulled_by | ENUM `ActorKind` / ID | N | | 任何成员，含 AGENT |
| reason | STR(2000) | N | | ≥10 字 |
| evidence_refs | JSON | N | `[]` | |
| state | ENUM(`ACTIVE`,`CLEARED`) | N | `ACTIVE` | |
| cleared_by / clearance_note / cleared_at | ID / STR(2000) / TS | Y | | 清除者必须是人类（`andon_clear`） |
| created_at | TS | N | | |

唯一约束：同一 `work_item_id` 同时最多一条 ACTIVE（MySQL 用生成列 `active_key`）。

### 3.23 例外 `delivery_exceptions`

| 字段 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| work_item_id | ID | Y | | 需求级例外时为空并填 `requirement_id` |
| requirement_id | ID | Y | | |
| dimension | ENUM(`TIME`,`COST`,`SCOPE`) | N | | |
| forecast | JSON | N | | `{appetite, consumed, predicted_total, tolerance, method}` |
| raised_by_kind | ENUM(`SYSTEM`,`HUMAN`,`AGENT`) | N | | 系统预测越界或成员手动提出 |
| report | JSON | N | | 例外报告：`{situation, options[{option, impact}], recommendation}`；系统 / 主管 Agent 起草，人可修改 |
| state | ENUM(`OPEN`,`DECIDED`) | N | `OPEN` | |
| decision | ENUM `ExceptionDecision` | Y | | 00 §5：CONTINUE / RESHAPE / HOLD / KILL |
| decided_by / decision_reason / new_appetite_minutes / decided_at | ID / STR(2000) / INT / TS | Y | | 仅人类；CONTINUE 必须给出新的 appetite 或新容差 |
| previous_state | ENUM `WorkItemState` | N | | 进入 EXCEPTION 前的状态 |
| created_at | TS | N | | |

### 3.24 穿透记录 `delivery_escapes`（不可变）

| 字段 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| work_item_id / claim_id | ID | N | | 内部验收已放行的那次声明 |
| escape_key | STR(64) | N | | `(work_item_id, claim_id 或 acceptance_id)`（00 §7.1，D114）；唯一，`delivery.escape_learning` 按此 upsert，07 / 12 Part A 发布的穿透亦落此表 |
| stage | ENUM(`HUMAN_REVIEW`,`CLIENT_ACCEPTANCE`) | N | | 在哪一层被发现 |
| decision_id / acceptance_id | ID | Y | | 发现它的裁决或甲方回执 |
| category | ENUM `FeedbackCategory` | N | | |
| internal_verdicts | JSON | N | | 当时 AGENT_TEST / AGENT_ACCEPTANCE 的结论摘要（03 `EvidenceReader`） |
| learning_state | ENUM(`PENDING`,`FORWARDED`,`FAILED`) | N | `PENDING` | 回流到 06 记忆与 11 评测集的状态（§7.4） |
| created_at | TS | N | | |

### 3.25 内部验收轮次 `delivery_internal_qa_rounds`（不可变）

| 字段 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| work_item_id / claim_id | ID | N | | |
| round | SMALLINT | N | | 1..max_internal_rounds |
| stage | ENUM `VerificationStage` | N | | `AUTOMATED` / `AGENT_TEST` / `AGENT_ACCEPTANCE` |
| result | ENUM(`PASSED`,`FAILED`,`CHEATING_SUSPECTED`) | N | | 03 回调结果 |
| verifier_agent_id | ID | Y | | TESTER / ACCEPTOR |
| feedback_decision_id | ID | Y | | 失败时生成的系统裁决（REQUEST_CHANGES，`authority_source=SYSTEM:internal_qa`） |
| visibility | ENUM(`INTERNAL`) | N | `INTERNAL` | **永不出现在甲方门户**；对团队成员可见 |
| created_at | TS | N | | |

## 4. 状态机

### 4.1 需求 RequirementStatus（CCR-02）

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | `POST .../requirements`、01 转换 | `requirement_write`；项目阶段 ∈ {INTAKE, DISCOVERY, PLANNING, ACTIVE, BLOCKED, ACCEPTANCE_PENDING}（ACCEPTANCE_PENDING：甲方部分验收后由返工来件转为新需求，复审 #10） | DRAFT | 审计 `delivery.requirement_created` |
| DRAFT | 编辑字段 / 增删改标准 | `requirement_write`；If-Match | DRAFT | — |
| DRAFT | `POST /requirements/{id}/versions`（基线化） | `requirement_write`；≥1 条 required 标准；title 非空 | BASELINED（v1） | 写 v1 快照；标准 `introduced_in_version=1`；事件 `requirement.versioned{version_no:1}`；审计 `delivery.requirement_baselined` |
| BASELINED | `POST /requirements/{id}/change-records`（SCOPE） | AuthorityChecker(`scope_change`，不可委托)；step-up；`base_version_no`=current | BASELINED（v+1） | §5.6 |
| BASELINED | `PATCH status=ARCHIVED` | `requirement_write`；无非终态工作项 | ARCHIVED | 审计 |
| DRAFT | `PATCH status=ARCHIVED` | `requirement_write`；无工作项 | ARCHIVED | 审计 |

非法：BASELINED 直接改 title/description/priority/标准 → 409 `120439`（须走变更记录）；DRAFT 发起变更记录 → 409 `120439`；ARCHIVED 任何写 → 409 `120420`。

### 4.2 工作项 WorkItemState

**状态所有者**（PRD §6.3）：READY / REWORK / BLOCKED = 交付负责人；CLAIMED / IN_PROGRESS / EXECUTION_FAILED = 执行者；AGENT_CLAIMED_DONE / VERIFYING = 系统；NEEDS_HUMAN_REVIEW = 有裁决权主体；ACCEPTED / CANCELLED = 终态。**硬规则**：执行者只能把工作项推进到 AGENT_CLAIMED_DONE；→ ACCEPTED 只能是 APPROVE 裁决。

**初始状态与 READY 守卫**（`ready_guard`）：非汇总项、`estimate_minutes` 非空、全部前驱 ACCEPTED、无人工阻塞原因、需求 BASELINED。新建时满足 → READY，否则 → BLOCKED 并写入全部未满足的系统原因（`ESTIMATE_MISSING`、`DEPENDENCY`、`DEPENDENCY_CANCELLED`、`CHILDREN_OPEN`、`REQUIREMENT_DRAFT`）。系统原因全部消除且无人工原因（`CLARIFICATION`、`EXTERNAL`）时，系统自动迁回（有 ACTIVE EXECUTOR 分派 → CLAIMED，否则 → READY）。

**迁移表**（「命令」= 非裁决 API；「裁决」= Decision；「系统」= 03 经 `WorkItemTransitioner` 或本模块任务。所有迁移：version+1、写 `delivery_work_item_transitions`、发布 `work_item.state_changed`、SSE `tenant/{t}/work-items/{id}`；裁决与人工阻塞额外写审计）：

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| T01 | — | 创建（命令 / 模板） | `work_item_write`；需求同项目且非 ARCHIVED；上限 | READY 或 BLOCKED | `work_item.created`；审计 `delivery.work_item_created` |
| T02 | READY | 自认领（命令 `POST assignments` 指向自己） | HUMAN；`work_item_execute`；非暂停；项目 ACTIVE | CLAIMED | 分派 ACTIVE + 租约；`work_item.assigned` |
| T03 | READY | ASSIGN（裁决；人经 A1，或 05 经 `DispatchAssigner.Assign` 同步调用） | AuthorityChecker(`work_item_assign`，主管 Agent AUTO 派单为 `POLICY:dispatch_auto`，提案 id 记 Decision `detail.dispatch_proposal_id`)；受派者校验 BR-02-10 | CLAIMED | 同上 + Decision；受派者为 Agent 时请求 03 `Launch(autostart)`，分支原因按 BR-02-44 |
| T04 | CLAIMED | 执行开始（系统 `ExecutionStarted`） | 执行属于本工作项，且执行的 `assignment_id` = 当前 ACTIVE EXECUTOR 分派（比较**受派人**，不比较执行发起者：autostart 执行的发起者为 SYSTEM，C17） | IN_PROGRESS | `current_execution_id`；`first_claimed_at` |
| T04e | CLAIMED / IN_PROGRESS | Heavy 并行分支开始（系统 `ExecutionStarted`，`branch_reason=ENSEMBLE`，v1.1） | 分支分派 `ensemble_plan_id` 非空 | IN_PROGRESS（已是则不变） | **不写** `current_execution_id`（保持为空直到择优，BR-02-45） |
| T05 | CLAIMED / IN_PROGRESS | 释放（命令 `PATCH assignment status=RELEASED`） | 分派人本人或 `work_item_assign`；无 RUNNING/PAUSED 执行 | READY（ready_guard 不满足 → BLOCKED） | 分派 RELEASED；通知负责人 |
| T06 | CLAIMED / IN_PROGRESS | 租约过期（系统 `delivery.lease_reclaim`） | 过期 +60s 宽限；03 报告该工作项**无非终态 DELIVERY 执行**（CREATED / RUNNING / PAUSED / INTERRUPTED，含等待执行槽的 CREATED，C6） | READY / BLOCKED | 分派 EXPIRED；N4 |
| T07 | CLAIMED / IN_PROGRESS / REWORK / EXECUTION_FAILED | ASSIGN 改派（裁决；A1 或 `DispatchAssigner.Reassign`） | AuthorityChecker(`work_item_assign`)；无 RUNNING 执行 | CLAIMED | 同事务：① 对旧受派人的非终态 DELIVERY 执行：CREATED（未启动的返工分支）调 03 `ExecutionLauncher.CancelExecution(tx, execution_id, REASSIGNED)`（C25）；**INTERRUPTED / PAUSED 且将作为本次 RETRY_INFRA 父执行的不取消**，由 ④ 的 `Launch(RETRY_INFRA, parent=该执行)` 按 03 BR-03-01 在同事务置其 FAILED(`SUPERSEDED_BY_RETRY`)（复审 #7）；不作为父执行的 INTERRUPTED / PAUSED 执行照常 `CancelExecution`；② 旧分派 REVOKED（`REASSIGNED`）；③ 新分派；④ 受派者为 Agent 时自动启动：REWORK 中未启动的返工分支 → 以**相同** branch_reason / parent 为新受派人重新 Launch（旧 CREATED 分支已在 ① 取消）；其他情况按 BR-02-44 |
| T08 | IN_PROGRESS | 提交声明（命令 `POST claims`） | 执行分派人本人；`work_item_execute`；BR-02-17 | AGENT_CLAIMED_DONE | Claim；`claim.submitted`；相似度检查 BR-02-27 |
| T09 | AGENT_CLAIMED_DONE | 验证开始（系统 `VerificationQueued`） | source_ref 去重 | VERIFYING | — |
| T10 | AGENT_CLAIMED_DONE / VERIFYING | 验证完成（系统 `VerificationCompleted`）：必需全 PASSED，或无必需验证 | BUG 部署守卫（BR-02-62） | **INTERNAL_QA**（D113；`internal_qa_required=false` 时 → NEEDS_HUMAN_REVIEW） | 03 排队内部验收；`needs_judgement=false`；**不发 N2**（N2 改在 T33） |
| T11 | VERIFYING | 验证完成：无 FAILED、≥1 INCONCLUSIVE | BUG 部署守卫（BR-02-62） | **INTERNAL_QA**（产品验收 Agent 先判；`internal_qa_required=false` 时 → NEEDS_HUMAN_REVIEW） | `needs_judgement=true`；AUTONOMOUS 项目 → 暂停 `ESCALATION_RULE` + `work_item.escalated`（PRD §2.3） |
| T12 | VERIFYING | 验证完成：≥1 必需 FAILED | | REWORK | 同事务写**系统裁决** Decision（`action=REQUEST_CHANGES`，`failure_category=QUALITY_REJECTION`，`actor_kind=SYSTEM`，`authority_source=SYSTEM:verification`，反馈 = 每个 FAILED 必需验证一条 `{category: FUNCTIONAL_DEFECT, location: 验证器名, expected: "验证通过", detail: 输出摘要 ≤500 字}`）；以该 decision_id 作 `feedback_ref` 调 03 `Launch(QUALITY, parent=claim.execution, CONTINUE)`（C5）；`quality_rework_count+1`；N3；`decision.made`（06 据此入库反馈）；BR-02-26 |
| T13 | NEEDS_HUMAN_REVIEW | 重新验证（系统 `VerificationQueued`；人经 03 `verification_run` 发起） | | VERIFYING | — |
| T14 | NEEDS_HUMAN_REVIEW | APPROVE（裁决） | BR-02-19 ~ 25 | ACCEPTED | Claim ACCEPTED；EXECUTOR / REVIEWER 分派 COMPLETED；`accepted_at`、`first_pass`；后继依赖重算；N9 |
| T15 | NEEDS_HUMAN_REVIEW | REJECT / REQUEST_CHANGES，`failure_category=QUALITY_REJECTION` | 反馈 ≥1；理由 | REWORK | `quality_rework_count+1`；03 `Launch(QUALITY, parent=claim.execution, workspace_mode=REJECT?RESTART:CONTINUE, feedback)`；BR-02-26 |
| T16 | NEEDS_HUMAN_REVIEW | REQUEST_CHANGES，`failure_category=STRATEGY_CHANGE` | 反馈 ≥1；`strategy_note` | REWORK | ChangeRecord(STRATEGY)；03 `Launch(STRATEGY, parent=claim.execution 的父, 并行替代)`；不计质量返工 |
| T17 | NEEDS_HUMAN_REVIEW | REQUEST_CHANGES，`failure_category=SCOPE_CHANGE` | `baseline_stale=true` | REWORK | 基线移到当前版本；03 `Launch(SCOPE, parent=claim.execution, CONTINUE)`；不计质量返工 |
| T18 | REWORK | 返工分支开始（系统 `ExecutionStarted`） | 分支原因 ∈ {QUALITY, STRATEGY, SCOPE} | IN_PROGRESS | — |
| T19 | CLAIMED / IN_PROGRESS / REWORK | 执行故障（系统 `ExecutionFailed{kind}`） | 执行为 `current_execution_id`（Heavy 并行分支不适用：单枝失败只记录，全部失败经 `EnsembleFailed`，BR-02-45） | EXECUTION_FAILED | `kind=INFRA` 且 `infra_retry_count` < 项目上限（默认 2，07 提供 0–5）→ 任务 `delivery.auto_retry`（退避 30s、120s）；超限或 `kind=RUNTIME` → N7 |
| T20 | EXECUTION_FAILED | 重试开始（系统 `ExecutionStarted`，分支 RETRY_INFRA；人工重试经 03 `execution_start`） | | IN_PROGRESS | 自动重试 `infra_retry_count+1`；人工重试不计 |
| T20b | EXECUTION_FAILED | 人工恢复同一执行（系统 `ExecutionStarted`，执行 = `current_execution_id`，自 INTERRUPTED 恢复，03 E26） | 执行属于当前分派 | IN_PROGRESS | 不计 `infra_retry_count`；租约恢复 now+ttl（03 Q8 已决） |
| T21 | READY / CLAIMED / IN_PROGRESS / REWORK / EXECUTION_FAILED | 人工阻塞（命令 `PUT blockage`） | 负责人（`work_item_write`），或执行分派人（仅 `CLARIFICATION`）；有 RUNNING 执行时同时请求 03 暂停 | BLOCKED | 租约挂起；`CLARIFICATION` 且需求有来源来件 → 01 `ClarificationRequester`，否则通知负责人；审计 `delivery.work_item_blocked` |
| T22 | BLOCKED | 解除（命令 `DELETE blockage`）或系统原因消除 | 人工原因须 `work_item_write`；ready_guard | READY / CLAIMED | 租约重置 now+ttl |
| T23 | 任意非终态（含汇总项） | CANCEL（裁决） | AuthorityChecker(`work_item_cancel`)；理由；BR-02-31 | CANCELLED | 分派 REVOKED；Claim CANCELLED；03 取消活跃执行；后继加 `DEPENDENCY_CANCELLED` |
| T24 | 汇总项 BLOCKED(`CHILDREN_OPEN`) | 子项全部终态且 ≥1 ACCEPTED（系统） | | NEEDS_HUMAN_REVIEW | 无必需验证；L2 显示「子项汇总 n/n」 |
| T25 | 任意非终态 | PAUSE / RESUME（裁决） | §4.4 | 状态不变 | 覆盖层；version+1；**PAUSE 同事务调 03 `ExecutionLauncher.Pause(tx, work_item_id, reason)`** 暂停该工作项的 RUNNING 执行（复审 #8）；RESUME 同事务调 03 `ExecutionLauncher.Resume(tx, work_item_id)`：恢复被暂停的执行，`AUTONOMY_*` / `ESCALATION_RULE` 时另启动 `autostart=false` 的待启动分支（C26） |
| T26 | ACCEPTED | REQUEST_CHANGES（裁决，`failure_category=QUALITY_REJECTION`，payload `source_acceptance_id`）——甲方拒收后的质量重开（C7） | HUMAN 且 AuthorityChecker(`decision_request_changes`)；07 报告该工作项在 `source_acceptance_id` 中逐项结果为 REJECTED；项目阶段 ∉ {DELIVERED, CLOSED, CANCELLED}；反馈 ≥1、理由必填；入口为 01 返工来件转换 `REOPEN_WORK_ITEMS`（经 `WorkItemReopener`，§6.9）或 D1 | REWORK | 清 `accepted_at`（保留 `first_accepted_at`、`first_pass`）；`quality_rework_count+1`；重新激活原执行者分派（新 ACTIVE EXECUTOR 行，`end_reason` 链接）；03 `Launch(QUALITY, parent=被接受声明的执行, CONTINUE, feedback_ref=decision)`；BR-02-26 照常计数；N3 |

**非法迁移**（全部 409，业务码 `120420`，`data={from, attempted, allowed_actions[], reason_key}`，审计 `delivery.work_item_transition_denied`）：

| 尝试 | 原因 key（i18n `delivery.transition.*`） |
|---|---|
| 任意 → ACCEPTED 非 APPROVE（含看板拖拽、执行者） | `accepted_requires_decision` |
| 执行者 / Agent 推进到 AGENT_CLAIMED_DONE 之后的任何状态 | `executor_ceiling` |
| 非 IN_PROGRESS 提交声明 | `claim_requires_in_progress` |
| AGENT_CLAIMED_DONE / VERIFYING 上的 APPROVE / REJECT / REQUEST_CHANGES | `verification_pending` |
| READY 直接 → IN_PROGRESS（未认领即开始） | `claim_before_start` |
| 汇总项认领 / 分派执行者 / 提交声明 | `container_not_executable` |
| BLOCKED 上的认领 / 分派 / 声明 | `blocked` |
| ACCEPTED / CANCELLED 上的任何迁移或裁决（含 PAUSE/RESUME），**T26 除外**（仅 ACCEPTED、仅 REQUEST_CHANGES + `source_acceptance_id`） | `terminal` |
| REJECT / REQUEST_CHANGES 在非 NEEDS_HUMAN_REVIEW | `review_only` |
| REQUEST_CHANGES(SCOPE_CHANGE) 而 `baseline_stale=false` | `scope_rework_requires_stale` |
| 项目阶段 DELIVERED / CLOSED / CANCELLED 时任何写；INTAKE / DISCOVERY / PLANNING 阶段的认领、分派、声明 | `project_stage` |
| 解除仍有系统原因的阻塞 | `blocked_by_system_reason`（`data.block_reasons`） |
| 未暂停时 RESUME；已暂停时 PAUSE | `pause_state` |

### 4.3 分派 AssignmentStatus（CCR-02）

| 当前 | 触发 | 目标 | 说明 |
|---|---|---|---|
| — | 自认领 / ASSIGN / 评审认领 | ACTIVE | 唯一索引保证每工作项每角色 ≤1 |
| ACTIVE | 续约 `PUT lease` / 03 心跳 `RenewLease` / 评审心跳 | ACTIVE | `lease_expires_at = now + ttl` |
| ACTIVE | 释放 | RELEASED | |
| ACTIVE | 过期回收 | EXPIRED | |
| ACTIVE | 改派、工作项 CANCELLED、Agent 退役、Heavy 未选分支（v1.1） | REVOKED | |
| ACTIVE | 工作项 ACCEPTED；REVIEWER 做出裁决 | COMPLETED | |
| 非 ACTIVE | 任何 | — | 409 `120420`（`assignment_not_active`） |

### 4.4 暂停覆盖层（PauseReason，CCR-02）

暂停不是状态（00 §5 无 PAUSED），而是工作项上的覆盖层 `paused_at`（本规格提议：不扩充 WorkItemState，PAUSE 可作用于任意非终态且恢复后回到原状态）。

| pause_reason | 触发 | 暂停期间允许 | RESUME 要求 |
|---|---|---|---|
| `MANUAL` | PAUSE 裁决（`decision_pause`，理由必填） | RESUME、CANCEL；03 的系统事实迁移（验证完成、执行故障）照常记录 | `decision_pause`，任意有权主体 |
| `BUDGET` | 消费 07 发布的 `budget.threshold_reached{threshold_pct:100}`（authority `SYSTEM:budget_guard`） | 同 MANUAL | `decision_pause` 且 HUMAN；07 `ProjectGate` 报告预算已 <100%，否则 409 `120421` |
| `AUTONOMY_REWORK_LIMIT` | 质量返工计数达到 3（BR-02-26） | 人类裁决（APPROVE / REJECT / REQUEST_CHANGES / ASSIGN / CANCEL / RESUME）；返工分支不自动启动；05 派单与 03 启动均须检查本覆盖层（03 BR-03-20 守卫「工作项未暂停」） | HUMAN + `decision_pause` + 理由；RESUME 后按 T25 启动待启动分支 |
| `AUTONOMY_SPINNING` | 相邻产物变化比例 <8%（BR-02-27） | 同上 | 同上 |
| `ESCALATION_RULE` | INCONCLUSIVE（AUTONOMOUS 模式）、08 升级规则 | 同上 | 同上 |

暂停时：执行者命令（认领、声明、续约外的推进）→ 409 `120421`；活跃执行由 T25 同事务调用 03 `ExecutionLauncher.Pause` 暂停（03 不消费 `work_item.state_changed` 来暂停）；租约挂起；升级类暂停置 `require_human_decision=true` 直到终态。

---

### 4.5 D112 / D113 新增状态与迁移

**状态所有者补充**：DRAFT = 交付负责人；PLAN_REVIEW = 计划批准者；INTERNAL_QA = 系统（内部质量门）；STOPPED = 人类负责人；EXCEPTION = 有 `exception_decide` 的人类。**完成阶梯 L2 = 内部验收通过**。

**状态优先级**：系统阻塞（BLOCKED）> 就绪未满足（DRAFT）> READY。新建工作项：`ready_guard` 不满足 → BLOCKED；满足但就绪检查（BR-02-47）不通过 → DRAFT；都满足 → READY。

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| T27 | DRAFT | 就绪检查通过（系统；编辑后自动重跑或 W15） | BR-02-47 全部通过；`ready_guard` | READY | `work_item.ready_checked{ok:true}` |
| T28 | READY | 就绪检查不通过（编辑删去验收标准 / 验证方式等、需求升版本） | — | DRAFT | `work_item.ready_checked{ok:false, issues}`；已有提案作废（05） |
| T29 | DRAFT | 人类自认领（命令 `POST assignments` 指向自己，带 `acknowledge_readiness_issues=true`） | HUMAN；`work_item_execute` | CLAIMED | `readiness_override_by`；审计 `delivery.readiness_overridden`；页面持续显示就绪告警 |
| T30 | CLAIMED / REWORK | 计划提交（系统：03 `PLANNING` 执行完成并回调 `PlanProposed`） | `plan_required=true` | PLAN_REVIEW | §3.21 新行；`plan.proposed`；按 BR-02-51 路由给批准者（Agent 批准者经 05 派单，不进入人类收件箱） |
| T31 | PLAN_REVIEW | 批准（P3 `APPROVE`） | BR-02-51 批准者资格；`coverage_ok=true` | CLAIMED | 计划 APPROVED；`plan.approved`；**同事务请求 03 冻结验收集**（`acceptance_suite.frozen`）并 `Launch(INITIAL / 当前分支原因, autostart)` 实现执行 |
| T32 | PLAN_REVIEW | 拒绝（P3 `REJECT`，反馈必填） | 同上 | CLAIMED | 计划 REJECTED；`plan.rejected`；03 以反馈重新出计划；第 3 次被拒 → T35 规则升级（`INTERNAL_ROUNDS_EXHAUSTED`） |
| T33 | INTERNAL_QA | 内部验收全部通过（系统 `InternalQaCompleted{passed}`：AGENT_TEST 与 AGENT_ACCEPTANCE 均 PASSED） | 两阶段结论都属于当前声明 | NEEDS_HUMAN_REVIEW | `internal_qa.passed`；**N2**（此时才通知人）；L2 = 内部验收通过 |
| T34 | INTERNAL_QA | 内部验收不通过（任一阶段 FAILED 或 CHEATING_SUSPECTED） | `internal_round_count + 1 < max_internal_rounds` | REWORK | `internal_round_count+1`；系统裁决 REQUEST_CHANGES（`authority_source=SYSTEM:internal_qa`，反馈 = 测试 / 验收 Agent 的结构化结论）；03 `Launch(QUALITY, CONTINUE, feedback)`；**不发任何人类通知、不建收件箱项、频道不发卡片**（BR-02-54） |
| T35 | INTERNAL_QA / PLAN_REVIEW | 内部轮次用尽（同 T34 触发但 `internal_round_count + 1 ≥ max_internal_rounds`，或计划第 3 次被拒） | — | NEEDS_HUMAN_REVIEW | `escalation_reason=INTERNAL_ROUNDS_EXHAUSTED`；`require_human_decision=true`；`work_item.escalated`；N11（附各轮内部结论） |
| T36 | 任意非终态（含 DRAFT） | Andon 拉绳（AN1，或项目级 AN2 对项目内全部非终态项） | `andon_pull`（全员含 Agent）；理由 ≥10 字 | STOPPED | `stopped_from_state`；同事务调 03 `Pause` 暂停活跃执行；下游后继加阻塞原因 `ANDON_UPSTREAM`；`andon.pulled`；N13 |
| T37 | STOPPED | 清除（AN3） | **仅人类**，`andon_clear`；处置说明必填 | `stopped_from_state` | 03 `Resume`；移除下游 `ANDON_UPSTREAM`；`andon.cleared` |
| T38 | READY / CLAIMED / PLAN_REVIEW / IN_PROGRESS / REWORK / INTERNAL_QA | 预测越界（系统 `delivery.tolerance_forecast`）或成员提出（E4） | BR-02-57 | EXCEPTION | §3.23 新行（报告草稿由系统 / 主管 Agent 生成）；03 `Pause`；`exception.raised`；N14 |
| T39 | EXCEPTION | 例外决定（E3） | **仅人类**，`exception_decide`（不可委托）；理由 | CONTINUE → `previous_state`（appetite / 容差更新）；RESHAPE → DRAFT（同事务建 ChangeRecord，需求按范围变更流程升版本）；HOLD → BLOCKED（原因 `HOLD`）；KILL → CANCELLED（同 T23 副作用） | `exception.decided`；CONTINUE 时 03 `Resume` |

**内部失败不穿透**：T12（自动检查失败）与 T34（内部验收失败）一律只走内部返工；T12 原有的 N3 通知**取消**（由本节覆盖），T12 同样累加 `internal_round_count`，到上限按 T35 升级。

**穿透**：T15（人工 REJECT / REQUEST_CHANGES，QUALITY 类）与 T26（甲方拒收重开）发生时，若被拒的声明曾经 T33 内部验收通过，则同事务按 `escape_key` 写 §3.24 并发布 `quality.escaped`（BR-02-58）；同一 `escape_key` 已有穿透（如 07 已就同一甲方回执发布）时只引用、不再发布（D114）。

**非法迁移补充**：

| 尝试 | 错误 |
|---|---|
| 把 DRAFT 工作项派给 Agent（T03 / `DispatchAssigner.Assign` / AUTO 派单） | 409 `120461` `readiness_unmet`（`data.readiness_issues`） |
| `plan_required=true` 且无 APPROVED 计划时开始实现执行 | 409 `120462` `plan_not_approved` |
| 执行者 / 派出该工作项的主管 Agent / 与执行者同模型家族的 Agent 批准计划 | 403 `120403` `reason_code=SEPARATION_OF_DUTIES`（detail `PLAN_SELF_APPROVAL` / `PLAN_SAME_MODEL_FAMILY`） |
| STOPPED 上的认领、分派、声明、裁决（Andon 清除除外） | 409 `120464` `andon_active` |
| EXCEPTION 上除例外决定、CANCEL 以外的任何迁移 | 409 `120465` `exception_pending` |
| INTERNAL_QA 上的 APPROVE / REJECT / REQUEST_CHANGES（人工裁决须等内部验收结束或升级） | 409 `120420` `internal_qa_pending` |
| Agent 清除 Andon；Agent 作出例外决定 | 403 `120403` `reason_code=HUMAN_ONLY` |
| 创建 / 修改工作项使 `accountable_principal_id` 为空或非人类 | 422 `120467` `accountable_human_required` |
| 例外决定 CONTINUE 未给出新 appetite / 容差 | 422 `120468` `exception_continue_requires_new_budget` |

## 5. 业务规则与边界情况

### 5.1 需求与标准

- **BR-02-01** 基线化后需求内容只能经 SCOPE 变更记录修改；DRAFT 期可自由编辑，标准可软删（`archived_at`）。
- **BR-02-02** 标准 `code` 在需求内单调递增不复用；修改基线标准 = 退役旧 + 新增并 `supersedes_id`。
- **BR-02-03** 上限：每项目 ≤500 个未归档需求；每需求活跃标准 ≤50、版本 ≤200；超限 422 `120449`（`data.limit`）。
- **BR-02-04** 版本差异由两份快照计算：title / description 行级 diff（description >2000 行只给统计）；标准按 id 匹配分为新增 / 退役 / 替换（supersedes 链）/ 未变。

### 5.2 工作项、父子与依赖

- **BR-02-05** 父子：同项目、同需求；深度 ≤4；不得以自身后代为父；违规 422 `120450`。父有子项后 `is_container=true`；若父当前有 ACTIVE EXECUTOR 分派或活跃执行 → 422 `120450`（须先释放）。
- **BR-02-06** 依赖新增：同项目（跨项目 422 `120451`）；前驱非 CANCELLED；每工作项前驱 ≤50（422 `120423`）。**环检测**：同事务内以 `depends_on_id` 为起点沿前驱边 BFS（读取该项目全部依赖边；5000 项 × 50 边上限内存可承受），到达 `work_item_id` → 422 `120422`，`data.cycle=["WI-3","WI-7","WI-3"]`。两条反向边并发：事务先 `SELECT ... FOR UPDATE` 锁项目的 `delivery_project_counters` 行（SQLite 写事务天然串行），保证不会双双通过检测。
- **BR-02-07** 依赖或前驱状态变化后同事务重算后继系统阻塞原因：前驱非 ACCEPTED → `DEPENDENCY`；前驱 CANCELLED → `DEPENDENCY_CANCELLED`（须人工删除该依赖才消除）。对已 CLAIMED / IN_PROGRESS 的工作项新增未满足依赖：只记录警示，不强制迁到 BLOCKED（本规格提议：不打断进行中的执行；卡片显示琥珀「依赖未满足」）。
- **BR-02-08** 字段修改须 `work_item_write` + If-Match；risk / type 在 AGENT_CLAIMED_DONE / VERIFYING / NEEDS_HUMAN_REVIEW 期间不可改（影响必需验证与委托范围）→ 409 `120420`。
- **BR-02-09** 工作项基线：创建时 = 需求当前版本；`PUT /work-items/{id}/baseline` 移到当前版本并重选关联标准、清除 `baseline_stale`；AGENT_CLAIMED_DONE / VERIFYING / NEEDS_HUMAN_REVIEW 期间禁止（须 REQUEST_CHANGES(SCOPE_CHANGE)）→ 409 `120420`。

### 5.3 分派与租约

- **BR-02-10** 受派者：团队 ACTIVE 成员；HUMAN 须具备项目 `work_item_execute`；AGENT 须 05 `AgentDirectory` 返回 `state=ACTIVE` 且角色含 `EXECUTOR`；否则 422 `120444`（`data.reason`: `not_member`/`lacks_execute`/`agent_inactive`/`agent_not_executor`）。
- **BR-02-11** **Agent 不可自认领**（PRD §3.2「执行被分配的工作项」）：AGENT 对自己创建 EXECUTOR 分派 → 403 `120403`（`reason_code=AGENT_SELF_CLAIM`）。Agent 只经 ASSIGN（人或主管 Agent 按 05 派单策略）获得工作。
- **BR-02-12** 租约 TTL（本规格提议的具体数，PRD 只要求「租约 + 过期回收」）：HUMAN 执行者默认 4h（15min–24h，认领 / 续约时可指定）；AGENT 执行者默认 10min（2–30min），由 03 执行心跳（≤每 60s）调用 `RenewLease` 续约；REVIEWER 评审认领默认 15min（5–60min），评审台心跳续约。回收宽限 60s。续约须分派人本人（或 03 代 Agent），否则 403 `120403`（`reason_code=NOT_LEASE_HOLDER`）。**执行者租约挂起**（`lease_expires_at=NULL`，C6）：工作项处于 BLOCKED、暂停覆盖层、AGENT_CLAIMED_DONE、VERIFYING、NEEDS_HUMAN_REVIEW、REWORK、EXECUTION_FAILED 期间；离开这些状态进入 CLAIMED / IN_PROGRESS（T18、T20、T20b、T07、T22）时重置为 now+ttl。
- **BR-02-13** 过期不打断活跃执行：03 报告该工作项有**非终态 DELIVERY 执行**（CREATED〔含等待执行槽，04 最长 60min〕/ RUNNING / PAUSED / INTERRUPTED）时，回收任务不迁移状态，记录 `lease_overdue` 并延长 1 个 TTL，连续最多 3 次，之后通知负责人且不再延长（执行存活以 03 心跳为准）。
- **BR-02-14** 抢占：两人同时自认领同一 READY 工作项，CAS（`state='READY' AND version=?`）+ 唯一索引保证仅一人成功，另一人 409 `120426`（`data.holder`）。
- **BR-02-15** 评审认领：仅 NEEDS_HUMAN_REVIEW；调用者须预检至少可做 APPROVE / REJECT / REQUEST_CHANGES 之一（AuthorityChecker + 职责分离），否则 403 `120403`；已被他人持有 → 409 `120446`（`data={holder, lease_expires_at}`）。不持认领也可裁决，但**他人持有有效认领时**裁决 → 409 `120446`。
- **BR-02-16** Agent 退役 / 停止（消费 `agent.retired`、`agent.state_changed{to: RETIRING|RETIRED|STOPPED}`）：其 ACTIVE 分派 REVOKED（`AGENT_RETIRED`），工作项按 T06 规则回到 READY / BLOCKED，通知负责人。

### 5.4 完成声明与证据覆盖

- **BR-02-17** 声明守卫：工作项 IN_PROGRESS；调用者 = ACTIVE EXECUTOR 分派人（Agent 以其服务账号；03 E06 代 Agent 经 `ClaimSubmitter.SubmitOn` 提交时 `claimed_by` = 该 Agent、`authority_source` = 其角色）；`execution_id` = `current_execution_id`，且同事务调用 03 `EvidenceBinder.ValidateClaimable(tx, execution_id, fromExit)` 通过（执行状态判定以 03 为唯一权威：交互会话主动声明时 RUNNING / PAUSED 可通过；E06 退出事务内 `fromExit=true` 时 RUNNING 可通过；SUCCEEDED 且尚无声明时可通过——本规则不再自列状态，C1）；**证据 ≥1 条**（PRD §6.3）且每条经 03 `EvidenceReader` 确认属于该执行或其祖先分支、同工作项；通过后同事务调用 03 `EvidenceBinder.Attach(tx, claim_id, evidence_ids)` 把证据置为 ATTACHED；否则 422 `120428`（无证据）/ `120429`（证据越界）；映射的标准须为工作项关联且活跃，否则 422 `120430`。Heavy 并行选中分支的声明见 BR-02-45。
- **BR-02-18** 覆盖率：分母 = 工作项关联、`required=true` 且在 `baseline_version_no` 中活跃的标准数；分子 = 有 ≥1 行 `COVERED` 或 `NOT_APPLICABLE`（附理由）的标准数；PARTIAL 不计入分子。允许带未覆盖标准提交（评审台琥珀标出），声明时写快照。项目「证据覆盖率」（PRD §15，目标 ≥90%）= 有 ≥1 条证据的声明占比。旧声明的映射永不修改；新分支须提交新声明（`sequence+1`），旧 PENDING 声明置 SUPERSEDED。

### 5.5 裁决

- **BR-02-19 授权**：每个裁决在业务事务内调用 08 `AuthorityChecker.Check(ctx, tx, AuthorityRequest)`，`Facts` 由本模块填充：`{type, risk, cost_micros（03 `EvidenceReader.WorkItemCost`：该工作项全部 DELIVERY 执行累计，不含 VERIFICATION，C28）, quality_rework_count, self_produced, required_verifications_passed, require_human_decision, paused（bool，不外泄模块内 `PauseReason`）, dispatched_by_actor（05 `DispatchProvenance.DispatchedBy(work_item_id)`，供 08 S3(c) 职责分离）}`，输出 `AuthorityDecision{allowed, authority_source, delegation_id?, reason_code}`。动作 → 权限码：APPROVE→`decision_approve`、REJECT→`decision_reject`、REQUEST_CHANGES→`decision_request_changes`、PAUSE/RESUME→`decision_pause`、ASSIGN→`work_item_assign`、CANCEL→`work_item_cancel`（CCR-03）、SCOPE_CHANGE→`scope_change`。拒绝 → 403 `120403`（`data.reason_code`），审计 `delivery.decision_denied`（outcome=denied，独立事务）。；ASSIGN 裁决另填 `assignment_role`（EXECUTOR / REVIEWER）与 `assignee_principal_id`（供 08 策略规则 `review_dispatch` / `dispatch_auto` 判定，复审 B3）
- **BR-02-20 不可委托**：`scope_change`（00 §6.4）的 authority 为 DELEGATION 一律拒绝（`reason_code=NON_DELEGABLE`）；本模块在 AuthorityChecker 之外再硬编码校验一次（纵深防御）。
- **BR-02-21 职责分离**（硬编码）：以下主体对该工作项的 APPROVE / REJECT / REQUEST_CHANGES 被拒（`reason_code=SELF_REVIEW`）：(a) 任一时刻持有该工作项 EXECUTOR 分派者；(b) 任一声明的声明者；(c) 03 报告的任一分支执行的**执行受派人**（不含仅确认派单、仅作为 SYSTEM autostart 发起的人，C17）；(d) 以 ASSIGN 分派该工作项的 AGENT（主管 Agent，PRD §10.6），除非其 authority 为 DELEGATION 且 (a)–(c) 不成立。另外，**05 中无 `REVIEWER` 角色的 AGENT，任何裁决（PAUSE 除外）均拒绝**（`reason_code=AGENT_EXECUTOR`），即使存在委托。人类同样受 (a)–(c) 约束（§12 Q1）。
- **BR-02-22 理由**：REJECT、REQUEST_CHANGES、SCOPE_CHANGE、PAUSE、RESUME、CANCEL、改派（替换已有分派的 ASSIGN）理由必填 5–2000 字符（PRD 只强制 REJECT / SCOPE_CHANGE；本规格提议扩展，依据 doc3「每个决定必须记录理由」）；APPROVE 理由可选，但存在未覆盖必需标准、`needs_judgement=true` 时必填；缺失 → 422 `120434`。
- **BR-02-23 结构化反馈**：REJECT / REQUEST_CHANGES 必须 ≥1 条反馈（`category`、`detail` 必填，PRD §7）且 `failure_category` ∈ {QUALITY_REJECTION, STRATEGY_CHANGE, SCOPE_CHANGE}；否则 422 `120435` / `120436`。其他动作携带反馈或 failure_category → 422 `120436`。EXECUTION_FAILURE、BLOCKED 只由系统 / 阻塞命令产生。
- **BR-02-24 验证门（L3 不能跳过 L2）**：APPROVE 要求 03 报告的必需验证全部 PASSED；有 INCONCLUSIVE（无 FAILED）→ 仅 HUMAN 可 APPROVE 且理由必填；AGENT（经委托）APPROVE 还要求 ≥1 条 AUTOMATED 层 PASSED 且无 INCONCLUSIVE；AI_ASSISTED 结果不能单独支撑 APPROVE（PRD §11.2）。违反 → 409 `120438`（`data.missing_verifications[]`）。
- **BR-02-25 基线过期与升级**：`baseline_stale=true` 时 APPROVE → 409 `120437`；`require_human_decision=true` 或升级类暂停时 AGENT 裁决 → 403 `120403`（`ESCALATED_HUMAN_ONLY`）。
- **BR-02-26 有界自治 · 返工计数**：T12 / T15 后 `quality_rework_count+1`；**计数 ≥3** 时同事务置暂停 `AUTONOMY_REWORK_LIMIT`、`require_human_decision=true`、发布 `work_item.escalated{trigger:REWORK_LIMIT, count}`（CCR-04），03 分支以 `autostart=false` 创建；此后每次返工都需人类放行（PRD §7、旧 PRD §13）。STRATEGY / SCOPE / RETRY_INFRA 不计数。
- **BR-02-27 有界自治 · 原地打转**：声明提交时，若当前执行分支原因为 QUALITY 且 03 `EvidenceReader.ChangeSimilarity(execution)` 返回 `changed_files / union_files < 0.08`（以父分支产物文件集为基线，逐文件 SHA-256，旧 PRD F.10），声明照常记录（L1 不丢）、验证照常排队，同事务置暂停 `AUTONOMY_SPINNING` 并发布 `work_item.escalated{trigger:SPINNING, ratio}`。首个分支不检测；03 无法计算（产物为空）→ ratio=NULL，不触发；**并集文件数 `union_count < 5` 时 03 返回 ratio=NULL，不触发**（小改动返工不误判打转；MVP 固定，03 Q7 已决）。
- **BR-02-28 并发**：裁决与命令携带 `If-Match: "<version>"`，写入 `WHERE id=? AND version=?`；0 行 → 409 `120409`，`data={current_version, current_state, last_decision:{action, actor, at}}`。系统迁移不带 version：读后 CAS，冲突重试 ≤3 次（每次重读并重判守卫），仍冲突返回可重试错误让 03 重投。
- **BR-02-29 幂等**：创建 / 命令型 POST 支持 `Idempotency-Key`（≤64），由 09 C7 平台幂等辅助处理（00 §9）：同 key + operation + 主体且请求哈希相同 → 返回首次结果；同 key 不同请求 → `176425`；首次请求仍在进行 → 409 `176409`。系统迁移以 `source_ref` 唯一键去重：重复回调返回成功且无重复副作用。
- **BR-02-30 汇总项**：不可分派 EXECUTOR、不可声明；状态由系统按 T24 维护；APPROVE 汇总项的职责分离检查覆盖其全部子项的执行者（本规格提议：防止经汇总项绕开职责分离）。
- **BR-02-31 取消**：CANCEL 汇总项须子项已全部终态，否则 409 `120420`（`children_open`）——不级联，避免一次误操作吞掉整棵树。

### 5.6 范围变更与策略变更

- **BR-02-32 SCOPE 变更事务**（`POST /requirements/{id}/change-records`），全部同事务：① AuthorityChecker(`scope_change`) + 不可委托校验 + step-up（底座 `authn` 重新认证，purpose `scope_change`、target `requirement:<id>`，≤5 分钟，否则返回底座 step-up 要求，前端弹 ReauthenticationDialog；登记见 08 §6.9；经 01 `AppendFromIntake` 进入时，step-up 已由 01 以 purpose `intake_convert_privileged` 完成，此处不再重复要求，复审 #11）；② `base_version_no` ≠ 当前 → 409 `120440`；③ 应用标准新增 / 退役 / 替换与字段变更；④ 写 v+1 快照、ChangeRecord、Decision(SCOPE_CHANGE, failure_category=SCOPE_CHANGE, target=REQUIREMENT)；⑤ 影响分析：关联任一被退役 / 替换标准的工作项，或请求 `impacts[]` 显式列为 STALE 的工作项 → `baseline_stale=true`、`baseline_stale_since=v+1`，写 `delivery_change_impacts`；`impacts[]` 中标为 CANCEL 的按 T23 取消（权限源同一，理由取变更理由）；⑥ 审计 `delivery.scope_changed`；⑦ 发布 `requirement.versioned`、`decision.made`；⑧ N8。
- **BR-02-33** 空 diff → 422 `120400`。已 ACCEPTED 的工作项只标过期不改状态（新范围须新建工作项或走 07 返工来件）。
- **BR-02-34 策略变更**：仅经 REQUEST_CHANGES(STRATEGY_CHANGE)（T16）；ChangeRecord(kind=STRATEGY, 无版本变化)；03 创建并行替代分支；替代分支声明后评审者在详情页并列查看两枝证据再裁决（专用对比视图 v1.1）。

### 5.7 失败分类处理总表（PRD §7）

| FailureCategory | 产生方 | 工作项迁移 | 分支请求（03 `ExecutionLauncher`） | 计数 | UI |
|---|---|---|---|---|---|
| EXECUTION_FAILURE | 03 回调 `ExecutionFailed` | → EXECUTION_FAILED（T19） | 自动 RETRY_INFRA，parent = 失败执行，≤上限（默认 2），退避 30s / 120s；超限转人（N7） | `infra_retry_count`，不计质量 | 红色执行卡 +〔重试〕〔换机器〕〔转交〕 |
| QUALITY_REJECTION | 裁决 REJECT / REQUEST_CHANGES；系统验证 FAILED | → REWORK（T12 / T15） | QUALITY，parent = 声明执行；REJECT=`RESTART`（从声明执行的起点重开工作区）、REQUEST_CHANGES=`CONTINUE`；结构化反馈注入；计数 ≥3 → autostart=false | `quality_rework_count+1` | 分支树新增一枝 |
| SCOPE_CHANGE | 变更记录（需求级）；REQUEST_CHANGES(SCOPE_CHANGE)（工作项级） | 需求 v+1、工作项标过期；工作项级 → REWORK（T17） | SCOPE，CONTINUE | 不计 | 需求页版本对比、卡片「基线过期」 |
| STRATEGY_CHANGE | REQUEST_CHANGES(STRATEGY_CHANGE) | → REWORK（T16） | STRATEGY，并行替代（与旧分支同父） | 不计 | 分支并列 |
| BLOCKED | 阻塞命令 / 系统依赖 | → BLOCKED（T21） | 无；`CLARIFICATION` → 01 澄清 | 不计 | 阻塞原因 +〔发起澄清〕 |

### 5.8 人工关注计量（PRD §15「人工关注分钟」）

- **BR-02-35 客户端计时**：评审台对当前选中声明计时，仅当同时满足 `document.visibilityState==='visible'`、`document.hasFocus()`、最近一次用户活动（pointerdown / pointermove / keydown / wheel / scroll / touchstart）距今 ≤60s（空闲截断）。切换选中项即结束当前会话并开始新会话。
- **BR-02-36 心跳**：每 15s `PUT /attention-sessions/{id}` 发送 `{sequence, active_ms_delta}`；服务端校验 `sequence = last_sequence+1`（重复序号幂等忽略，跳号 422 `120454`），`active_ms_delta ≤ min(17000, now - last_heartbeat_at + 2000)`，超出截断；`last_heartbeat_at` 距今 >45s 的会话由任务关闭，此后心跳 409 `120454`，客户端新开会话。每次心跳同时续约该用户在该工作项上的 REVIEWER 认领。
- **BR-02-37 归属**：裁决请求可带 `attention_session_ids[]`；服务端结束这些会话，并把该主体在该声明上**全部**会话（跨设备）`active_ms` 之和写入 `delivery_decisions.attention_ms` 与埋点。未裁决会话仍计入工作项关注分钟（指标按 ACCEPTED 工作项聚合全部会话）。只有 HUMAN 可建会话（AGENT → 403 `120403`）。单会话上限 4h，超出自动关闭。

### 5.9 拆解模板

- **BR-02-38** 保存：Schema + 语义校验（§3.17），失败 422 `120441`，`data.errors=[{path, message_key}]`；每次保存产生新的不可变版本；`builtin` 模板不可新增版本（422 `120441`），可复制为新 key；DEPRECATED 不可实例化（422 `120442`），已有实例不受影响。
- **BR-02-39** 实例化：需求 BASELINED；`work_item_write`；参数按 Schema 校验；展开后 ≤300 项且项目总量 ≤5000（422 `120443`）；`dry_run=true` 只返回预览不写库；正式实例化单事务创建全部工作项、依赖、标准关联、`delivery_decompositions`；同一需求可多次实例化（各自独立 root）；`Idempotency-Key` 必填（缺失 422 `120400`），防双击生成两棵树。

### 5.10 其他边界

- **BR-02-40 不存在 / 跨租户**：全局地址先解析归属，跨租户或不存在一律 404 `120404`；作用域地址 owner 不匹配同样 404。
- **BR-02-41 Agent 可见范围**（00 §6.2「按分派」，实现为本规格提议）：调用者为 AGENT 且 05 角色仅含 `EXECUTOR` 时，工作项读取过滤为「曾分派给它的工作项 + 其需求与标准」，其他 404。
- **BR-02-42 依赖不可用**：数据库或 AuthorityChecker / EvidenceReader 不可用 → 503 `120460`，不降级放行（拒绝默认）。
- **BR-02-43 离线**：前端离线时禁用全部写按钮；离线期间的关注时长不补报（避免虚增）。
- **BR-02-44 分派时的分支原因**（C35，替代 T03 / T07 中原「否则 INITIAL」）：受派者为 Agent 且需要自动启动时，若工作项**尚无任何 DELIVERY 执行** → `INITIAL`；已有 DELIVERY 执行 → `RETRY_INFRA`，`parent` = 最近一个终态 DELIVERY 执行（若最近执行为 INTERRUPTED / PAUSED，03 BR-03-07 在同事务先把它置 FAILED `SUPERSEDED_BY_RETRY`）；REWORK 中尚未启动的返工分支按 T07 ④ 沿用。T03 / T07 / `DispatchAssigner` / BR-02-16 后的重新派单全部适用。RETRY_INFRA 由分派触发时不计 `infra_retry_count`。
- **BR-02-45 Heavy 并行（v1.1，C58 / C59）**：05 经 `DispatchAssigner.Assign(..., ensemble_plan_id)` 为每个分支创建 EXECUTOR 分派（不受唯一索引约束），03 以 `branch_reason=ENSEMBLE` 启动（T04e）；分支退出时 03 只保存 `pending_claim`，**不**调 `ClaimSubmitter`；单枝失败不触发 T19；全部分支失败时 05 调 `WorkItemTransitioner.EnsembleFailed(tx, work_item_id, plan_id)` → 工作项 EXECUTION_FAILED（`kind=RUNTIME`，不自动重试）。择优后 05 调 `ClaimSubmitter.SubmitOn(..., authority_source=POLICY:ensemble_branch（detail.ensemble_plan_id）, execution_id=选中分支)`：同事务把选中分支的分派保留为唯一 ACTIVE EXECUTOR、其余分派 REVOKED、写 `current_execution_id` = 选中执行，然后按 T08 提交声明；之后照常验证与裁决。
- **BR-02-46 甲方拒收重开**（C7 / C8）：T26 只能由人类经 01 返工来件 `REOPEN_WORK_ITEMS` 或 D1 发起；同一 `source_acceptance_id` × 工作项只能重开一次（唯一约束于 Decision payload 的 `(work_item_id, source_acceptance_id)`，重复 409 `120420` `reason_key=already_reopened`）；重开后的工作项再次 ACCEPTED 时 07 可把它放入新的交付包。被甲方拒收但经 01 判为「拒绝来件」或被变更记录取代的工作项，由 07 BR-07-08(b) 排除出 DELIVERED 判定（不需要在本模块迁移）。

---

### 5.11 D112 / D113 规则

- **BR-02-47 就绪检查（DoR）**：字段 `objective`、`acceptance_criteria`（本工作项负责的标准 ≥1，均有编号）、`out_of_scope`（可为空数组但须显式确认）、`context_refs`、`verification_plan`、`budget`、`autonomy_level`、`accountable_principal_id`。触发：创建、编辑就绪字段、需求升版本、派单前（DISPATCH）、W15 手动。**对 Agent 是硬闸门**（DRAFT 不可派发，120461）；**对人类是提示**：人类可在 DRAFT 上自认领（T29），记录 `readiness_override_by` 并在详情页持续告警。
- **BR-02-48 验证计划**：每条负责的验收标准恰好一个主方法（`TEST` / `COMMAND` / `AGENT_JUDGE` / `HUMAN` / `VISUAL`），可附辅方法；工作项类型 FEATURE / CONTENT 且涉及界面或成品观感时，模板默认追加 `VISUAL`；仅 `AGENT_JUDGE` 的标准在 risk=HIGH 时不允许（需 TEST / COMMAND / HUMAN 之一）。
- **BR-02-49 人类负责人**：每个工作项必须有人类负责人；默认取需求负责人，否则交付负责人；全自治项目默认委托授予人。负责人对结果负责、接收升级（N11–N14），可以不是执行者。Agent 永远不能成为负责人（422 120467）。负责人离开项目 → 工作项进入 BLOCKED（`NO_ACCOUNTABLE_HUMAN`）直到重新指定。
- **BR-02-50 需求质量检查**：需求基线化（R 系列）与来件转换时，由 AGENT_JUDGE 检查歧义、自相矛盾、不可验证的验收标准与交付形态冲突（例：「直接打开即可运行」与「模块化引入」）。发现问题 → 相关工作项 `readiness_issues` 加 `REQUIREMENT_CONFLICT` / `REQUIREMENT_AMBIGUOUS` / `UNVERIFIABLE_AC` 并保持 DRAFT；升级给**交付负责人**（N12，`escalation_reason=REQUIREMENT_CONFLICT`），由人决定修改需求或向甲方澄清（经 01 `ClarificationRequester`）——**不自动打扰甲方**。
- **BR-02-51 计划审批**：`plan_required = risk ≥ MEDIUM ∨ autonomy_level ≥ MEDIUM ∨ 模板要求`。计划由 03 只读执行产出（`purpose=PLANNING`）；`coverage_ok` 要求每条负责标准都有覆盖方式与验证方式。批准者：持 `plan_approve` 的人类，或经 08 策略 `plan_review` 的评审 Agent（必须与执行者**不同模型家族**，且不是执行者、不是派出该工作项的主管 Agent）。Agent 批准者的待办经 05 派单下发，不进入人类收件箱。计划轮次上限 3。
- **BR-02-52 冻结验收集**：T31 同事务请求 03 冻结验收集（可见部分 + 隐藏部分）；无需计划时在 T04（首次执行开始）冻结。冻结后执行者对测试只读；03 检测到冻结测试被改动 → 回调 `CHEATING_SUSPECTED`，按 T34 处理并累加轮次；同一工作项第 2 次作弊嫌疑直接 T35 升级。
- **BR-02-53 内部质量门**：`internal_qa_required=true` 时，T10 / T11 进入 INTERNAL_QA；03 依次执行 AGENT_TEST（测试 Agent）与 AGENT_ACCEPTANCE（产品验收 Agent，检查实际交付形态，含截图 / 运行 / 成品文件），05 负责派单与跨模型家族约束。两阶段均 PASSED → T33；任一 FAILED → T34 / T35。
- **BR-02-54 不穿透**：T12、T32（Agent 批准者拒绝时）、T34 的结果**只记录不打扰**：不发 N2 / N3、不建 08 收件箱项、不向频道发人类可见卡片（07 / 08 / 05 消费者按事件 `visibility=INTERNAL` 过滤），工作项卡片仅显示「内部第 n / N 轮」。`internal_round_count` 在新的声明链（T26 重开或人工 REJECT 后）清零。人工 REJECT 的质量返工上限沿用 BR-02-26（3 次）；内部轮次与人工返工分别计数。
- **BR-02-55 升级到人的唯一入口**：只有 `EscalationReason` 所列原因会产生人类待办：内部轮次用尽（T35）、需求冲突（BR-02-50）、Andon（T36）、容差例外（T38）、预算阈值（07）、模板必经人工关口（`MANDATORY_HUMAN_GATE`，例如视频脚本确认、小说大纲确认）。
- **BR-02-56 Andon**：任何成员（含 Agent）持 `andon_pull` 可停线，理由 ≥10 字；Agent 拉绳时同时附证据引用。项目级 Andon（AN2）停止项目内全部非终态工作项并暂停 05 派单。清除仅人类（`andon_clear`：人类负责人或交付负责人）。同一工作项 ACTIVE Andon 最多一条；重复拉绳返回现有记录（幂等）。
- **BR-02-57 容差与例外**：`delivery.tolerance_forecast` 每 15 分钟预测：时间 = 已用墙钟 + 同类工作项剩余中位数；成本 = 03 `WorkItemCost` + 剩余预测；范围 = 新增验收标准数。预测 > appetite × (1 + tolerance) → T38。例外报告由系统起草（主管 Agent 可补充），人可修改；决定仅人类、不可委托。禁止静默延期：appetite / 容差只能经 CONTINUE 决定修改。
- **BR-02-58 穿透记录与回流**（D114）：满足 §4.5「穿透」条件时写 §3.24。`quality.escaped` 的发布者为本模块（`HUMAN_REVIEW`，T15 / T26）、07（`CLIENT_ACCEPTANCE`）与 12 Part A（带 `escaped_from_stage` 的缺陷），统一幂等键 `escape_key`。`delivery.escape_learning` 消费**任一发布者**的 `quality.escaped`，按 `escape_key` upsert §3.24，把穿透摘要经 06 `MemoryWriter.Propose` 写为相关 TESTER / ACCEPTOR 的 EXPERIENCE 记忆**草稿**，并调用 11 `EvalSetWriter.ProposeCase(ctx, tx, {template_key, template_version, source: ESCAPE, work_item_id, description, evidence_refs[], expected})` 追加为模板评测用例（按 11 BR-11-22 自动入集，维护者可停用；幂等，重复调用不重复建用例）。本任务是穿透学习的**唯一**执行方：03 不发布 `quality.escaped`、不回流（03 BR-03-102 只提供内部验收记录），08 不调用 06 / 11（只做记录、通知与人工确认，BR-08-63），11 不直接消费 `quality.escaped`；本任务另消费 08 `quality.escape_learning_decided`：RECORDED → 使草稿生效，DISMISSED → 撤回草稿与用例；草稿与用例建成后发布 `quality.escape_learning_proposed{escape_key, memory_ids[], eval_case_ref}`，供 08 回填穿透记录的只读展示列；失败重试 5 次后 `learning_state=FAILED` 并通知团队所有者。
- **BR-02-59 缺陷（云效对齐）**：`type ∈ {BUG, INCIDENT}` 必填严重程度、优先级、复现率；`defect_stage` 推进：已合入（有合入提交证据）→ 已部署（交付 / 部署证据）→ 已验证（验证者通过）→ 已关闭（= ACCEPTED，仅验证角色可关闭）；状态组、流转矩阵与字典由 12 配置，本规格只保证字段与守卫钩子。
- **BR-02-61 模板系统阻塞原因**：`TOOL_UNAVAILABLE`（确定性验证器所需工具在所有在线机器都不可用，04 工具清单）与 `PROVIDER_UNAVAILABLE`（项目所选生成 provider 不可用）为**系统**阻塞原因：由 11 经 `WorkItemTransitioner.SetSystemBlock(tx, work_item_id, reason, on bool, source_ref)` 设置 / 清除（幂等，`source_ref` 去重），与其他系统原因同样参与 `ready_guard`；不可经 W12 人工设置或删除（422 120400）；原因全部消除后按 §4.2 自动回到 READY。
- **BR-02-60 L2 展示**：完成阶梯 L2 文案「内部验收通过」，悬停显示 AUTOMATED / AGENT_TEST / AGENT_ACCEPTANCE 三段结论与轮次；`internal_qa_required=false` 时 L2 只含 AUTOMATED 并标注「未经内部验收」。
- **BR-02-62 缺陷部署守卫（12 §4.2.1 F7）**：`type=BUG` 且 12 `WorkflowResolver` 解析出该工作项生效的缺陷流转矩阵含「merged → deployed」（F6）时，T10 / T11 额外要求 `defect_stage=DEPLOYED`；不满足时不迁移、不算失败，工作项停在（或经 T09 进入）VERIFYING，12 显示「已合入 · 等待部署」。`defect_stage` 推进为 `DEPLOYED` 时，以该声明最近一次验证结论重新评估 T10 / T11（source_ref 幂等）。矩阵不含 F6 或 `WorkflowResolver` 无该项目配置时按原守卫执行；`WorkflowResolver` 不可用 → 视为守卫不满足，保持 VERIFYING 并由 `delivery.stuck_check` 照常提醒（失败即拒）。
- **BR-02-63 甲方变更申请的影响预览与应用**（D117；01 kind=CHANGE）：① **预览**（`ChangeImpactPreviewer.Preview`，只读）：按需求**当前**版本计算——每条 RETIRE / REPLACE 标准所关联（`delivery_work_item_criteria`）的非 CANCELLED 工作项各给一条 `STALE`（已 ACCEPTED 的同样列出，按 BR-02-33 只标过期）；每条 ADD 给一条 `NEW`（`work_item_id` 为空，表示需新增工作项覆盖）；同一工作项只出现一次；`CANCELLED` 不由预览产生（是否取消由人在 01 分诊卡上决定）。② **应用**（`AppendFromIntake` 带 `Change`）：映射为 BR-02-32 的变更请求——ADD → `criteria.add{text, required:true}`、RETIRE → `criteria.retire`、REPLACE → `criteria.supersede{criterion_id, text, required:true}`；`Impacts` 中 STALE / CANCEL 同 R10 的 `impacts[]`（NEW 不传，新工作项由团队之后拆解）；ChangeRecord 的 `source_intake_id = SourceIntakeID`；按 BR-02-32 ②–⑧ 执行（`base_version_no` ≠ 当前 → 409 `120440`；空 diff → 422 `120400`，BR-02-33）。③ **容差调整**：`ApprovedAdjustment` 非空时，在 ④ 写入 v+1 之后、⑦ 发布事件之前，同事务调用 07 `ToleranceAdjuster.ApplyApprovedChange(ctx, tx, {requirement_id, source_intake_id, schedule_delta_days, cost_delta_micros, scope_delta_criteria})`；07 拒绝（`127055` 有未决例外）或任一步失败 → 整个事务回滚，原样返回 07 错误码（01 映射为 110435）。`ApprovedAdjustment` 为空（容差内变更）时不触碰容差。本规则不改变 BR-02-57「工作项 appetite 只能经 CONTINUE 修改」：甲方确认的调整只作用于 07 的需求 / 项目级容差基线。

## 6. 接口契约

### 6.1 通用

- huma v2 + `respx`；错误 `respx.ErrStatus(ctx, status, code, key, data)`；i18n namespace `delivery`。
- **守卫**：项目内集合用 `authz.RegisterOwned(..., "entity_id", projectOwner, guard, ...)`，路径 `/entities/{entity_id}/...`，`projectOwner` 解析 entity 归属并要求 `type='project'`（否则 404）；单个资源用 `authz.RegisterOwned(..., "<resource>_id", <resource>Owner, guard, ...)`，全局短地址 `/work-items/{work_item_id}` 等，resolver 从本模块表读 `(tenant_id, project_id)` 返回 `ResourceOwner{ScopeType:"entity", TenantID, EntityID: project_id}`；团队级用 `authz.Register`（自动加 `/tenants/{tenant_id}`）；团队成员只读用 `authz.RegisterTenantMember`。`FilterData=false`（00 的 DataScoped 指按项目实体授权，由 entity 角色绑定完成；Agent 分派范围在服务层过滤，BR-02-41）。
- **OpenAPI tag**：= 最终发布路径（含挂载前缀）第一段，由底座构建期 hook 设置（00 §9）：`entities`、`work-items`、`requirements`、`assignments`、`claims`、`decisions`、`change-records`、`acceptance-criteria`、`attention-sessions`、`tenants`、`decomposition-templates`。
- **乐观锁**：可变资源响应头 `ETag: "<version>"`，写请求 `If-Match: "<version>"`（与 `internal/todo` 一致，00 §3）；缺失 428 `120400`，不符 409 `120409`。
- **幂等**：标 ✔ 的操作支持 `Idempotency-Key`，由 09 C7 平台辅助处理（同 key 不同请求 `176425`，进行中 `176409`）。
- **权限拒绝**：一律 403 `120403` + `data.reason_code`（00 §9），不再为单个原因设独立 403 码。
- **路由注册顺序**：静态段（如 `/entities/{entity_id}/work-item-board`）先于参数段注册。

**业务错误码（02 子段 120000–125999，本规格使用 1204xx；12 Part A 使用 126000–126999；07 使用 127000–129999；00 §9，CR-12-01）**：

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 120400 | 400/422/428 | `delivery.error.invalid_input` | 输入不合法、缺 If-Match、空变更 |
| 120403 | 403 | `delivery.error.forbidden` | 授权拒绝；`data.reason_code` ∈ `MISSING_PERMISSION`/`SELF_REVIEW`/`AGENT_EXECUTOR`/`NON_DELEGABLE`/`DELEGATION_CONDITION`/`ESCALATED_HUMAN_ONLY`/`AGENT_SELF_CLAIM`/`HUMAN_ONLY`/`NOT_LEASE_HOLDER`（原因码以 08 `AuthorityReasonCode` 为准，本模块专属码为后两项之外的硬编码规则） |
| 120404 | 404 | `delivery.error.not_found` | 不存在 / 跨租户 / 不可见 |
| 120409 | 409 | `delivery.error.version_conflict` | 乐观锁冲突 |
| 120420 | 409 | `delivery.error.illegal_transition` | 非法迁移（§4.2） |
| 120421 | 409 | `delivery.error.paused` | 暂停中 / 预算未恢复 |
| 120422 | 422 | `delivery.error.dependency_cycle` | 依赖成环 |
| 120423 | 422 | `delivery.error.dependency_limit` | 前驱 >50 |
| 120426 | 409 | `delivery.error.lease_held` | 已被他人认领 |
| 120428 | 422 | `delivery.error.claim_requires_evidence` | 声明无证据 |
| 120429 | 422 | `delivery.error.evidence_out_of_scope` | 证据不属于该执行 / 工作项 |
| 120430 | 422 | `delivery.error.criterion_invalid` | 标准未关联 / 已退役 / 不同需求 |
| 120434 | 422 | `delivery.error.reason_required` | 缺理由 |
| 120435 | 422 | `delivery.error.feedback_invalid` | 结构化反馈缺失 / 不合法 |
| 120436 | 422 | `delivery.error.failure_category_invalid` | 失败类别与动作不匹配 |
| 120437 | 409 | `delivery.error.baseline_stale` | 基线过期不可 APPROVE |
| 120438 | 409 | `delivery.error.verification_required` | 必需验证未通过 |
| 120439 | 409 | `delivery.error.requirement_state` | 需求状态不允许该编辑 |
| 120440 | 409 | `delivery.error.requirement_version_conflict` | 变更基于旧版本 |
| 120441 | 422 | `delivery.error.template_invalid` | 模板校验失败 |
| 120442 | 422 | `delivery.error.template_unavailable` | 模板版本不存在 / 已弃用 |
| 120443 | 422 | `delivery.error.decomposition_limit` | 展开超限 |
| 120444 | 422 | `delivery.error.assignee_invalid` | 受派者不合法 |
| 120446 | 409 | `delivery.error.review_claim_held` | 评审认领被他人持有 |
| 120449 | 422 | `delivery.error.limit_exceeded` | 数量上限 |
| 120450 | 422 | `delivery.error.hierarchy_invalid` | 父子关系不合法 |
| 120451 | 422 | `delivery.error.dependency_cross_project` | 跨项目依赖 |
| 120454 | 409/422 | `delivery.error.attention_session_closed` | 关注会话已关闭 / 序号跳号 |
| 120460 | 503 | `delivery.error.unavailable` | 依赖不可用 |

### 6.2 资源表示（响应 `data` 字段）

- **WorkItem**：`id, number, key("WI-42"), project_id, requirement_id, parent_id, is_container, title, description, type, risk, estimate_minutes, state, block_reasons[], block_note, paused{at, reason, decision_id}|null, require_human_decision, baseline{version_no, stale, stale_since}, criteria_ids[], dependencies{depends_on[], dependents_count, unsatisfied_count}, executor{assignment_id, actor_kind, principal_id, agent_id, display_name, lease_expires_at}|null, reviewer_claim{...}|null, current_claim_id, current_execution_id, branch_count(03), quality_rework_count, infra_retry_count, needs_judgement, ladder{l1:{at, by}|null, l2:{passed, total, inconclusive, failed}|null, l3:{at, by, authority_source}|null, l4:{at}|null(07)}, coverage{required, covered}|null, accepted_at, first_pass, created_at, created_by, updated_at, version`。详情额外 `allowed_actions[]`（当前调用者可执行的动作与不可执行原因，见 6.4 #W8）。
- **Requirement**：`id, number, key("REQ-12"), title, description, priority, status, current_version_no, source_intake_id, criteria[{id, code, text, required, position, introduced_in_version, retired_in_version, supersedes_id}], rollup{work_items_total, accepted, criteria_required, criteria_accepted_covered}, version`。
- **Decision**：表 §3.12 全字段 + `feedback[]` + `actor_display{name, kind}`。
- **Claim**：表 §3.10 全字段 + `mappings[{evidence_id, criterion_id, coverage, note}]` + `coverage{required, covered, uncovered_criterion_ids[]}`。

### 6.3 需求与标准

| # | 方法 路径 | tag | 守卫 / 权限码 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| R1 | `POST /entities/{entity_id}/requirements` | entities | RegisterOwned · `requirement_write` | `title*, description, priority(0–3), criteria[{text*, required}] ≤50, source_intake_id` | Requirement（201） | 400, 403, 404, 120420(project_stage), 120449 | ✔ |
| R2 | `GET /entities/{entity_id}/requirements` | entities | RegisterOwned · `requirement_read` | query：过滤 `status(eq,in)`、`priority(eq,in,lte)`、`source_intake_id(eq)`、`created_at(gte,lte)`；`search`(title)；排序 `number, priority, created_at, updated_at`（默认 `priority,number`）；`offset/limit(≤200)/meta=capabilities` | `{items: Requirement(无 description)[], total}` | 403, 404 | — |
| R3 | `GET /requirements/{requirement_id}` | requirements | RegisterOwned · `requirement_read` | — | Requirement + ETag | 404 | — |
| R4 | `PATCH /requirements/{requirement_id}` | requirements | RegisterOwned · `requirement_write` | `title, description, priority, status(ARCHIVED)` | Requirement | 120409, 120439, 120420 | If-Match |
| R5 | `POST /requirements/{requirement_id}/acceptance-criteria` | requirements | RegisterOwned · `requirement_write` | `text*, required, position` | Criterion（201） | 120439（非 DRAFT）, 120449 | ✔；If-Match（需求） |
| R6 | `PATCH /acceptance-criteria/{criterion_id}` | acceptance-criteria | RegisterOwned · `requirement_write` | `text, required, position, archived(bool)` | Criterion | 120439, 120409 | If-Match（需求 version） |
| R7 | `POST /requirements/{requirement_id}/versions` | requirements | RegisterOwned · `requirement_write` | `{}`（仅 DRAFT → v1） | `{requirement, version:{version_no, content_hash}}`（201） | 120439, 120400(无必需标准) | ✔；If-Match |
| R8 | `GET /requirements/{requirement_id}/versions` | requirements | RegisterOwned · `requirement_read` | 排序 `version_no` | `{items:[{version_no, change_record_id, created_at, created_by, content_hash}]}` | 404 | — |
| R9 | `GET /requirements/{requirement_id}/versions/{version_no}` | requirements | RegisterOwned · `requirement_read` | `against`（可选，另一版本号 → 返回 diff） | `{snapshot, diff?:{title, description_hunks[], criteria:{added[], retired[], superseded[], unchanged[]}}}` | 404 | — |
| R10 | `POST /requirements/{requirement_id}/change-records` | requirements | RegisterOwned · `requirement_read`（路由）+ 服务内 AuthorityChecker(`scope_change`) + step-up | `base_version_no*, summary*, reason*, title?, description?, priority?, criteria:{add[{text, required}], retire[criterion_id], supersede[{criterion_id, text, required}]}, impacts[{work_item_id, action: STALE|CANCEL}], source_intake_id?` | `{change_record, decision, requirement, stale_work_item_ids[], cancelled_work_item_ids[]}`（201） | 120403(NON_DELEGABLE…), 120440, 120439, 120400, step-up 要求 | ✔ |
| R11 | `GET /requirements/{requirement_id}/change-records` | requirements | RegisterOwned · `requirement_read` | 排序 `created_at` | `{items: ChangeRecord[]}` | — | — |
| R12 | `GET /change-records/{change_record_id}` | change-records | RegisterOwned · `requirement_read` | — | ChangeRecord + impacts[] | 404 | — |

### 6.4 工作项、依赖、基线

| # | 方法 路径 | tag | 守卫 / 权限码 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| W1 | `POST /entities/{entity_id}/work-items` | entities | RegisterOwned · `work_item_write` | `requirement_id*, title*, description, type*, risk, estimate_minutes, parent_id, criterion_ids[], depends_on_ids[] ≤50` | WorkItem（201） | 120449, 120450, 120422, 120430, 120420 | ✔ |
| W2 | `GET /entities/{entity_id}/work-items` | entities | RegisterOwned · `work_item_read` | 过滤：`state(eq,in,notIn)`、`type(eq,in)`、`risk(eq,in)`、`requirement_id(eq,in)`、`parent_id(eq,isNull)`、`executor_principal_id(eq,in)`、`executor_kind(eq)`、`baseline_stale(eq)`、`paused(isNull)`、`is_container(eq)`、`needs_judgement(eq)`、`created_at/updated_at/state_changed_at/accepted_at(gte,lte)`；`search`(title, key)；排序 `number, state_changed_at, created_at, updated_at, risk, estimate_minutes`（默认 `-state_changed_at`）；`limit ≤200` | `{items: WorkItem(摘要)[], total}` | 403 | — |
| W3 | `GET /entities/{entity_id}/work-item-board` | entities | RegisterOwned · `work_item_read` | `limit_per_column(1–200, 默认 100)`、W2 同名过滤（`state` 除外）、`include_cancelled(bool)` | `{columns:[{key, states[], total, items: WorkItem(卡片)[]}]}`（本规格提议：看板 1000 项单请求加载） | 403 | — |
| W4 | `GET /work-items/{work_item_id}` | work-items | RegisterOwned · `work_item_read` | — | WorkItem + `allowed_actions[]` + ETag | 404 | — |
| W5 | `PATCH /work-items/{work_item_id}` | work-items | RegisterOwned · `work_item_write` | `title, description, type, risk, estimate_minutes, parent_id(null=脱离), archived(bool，仅 CANCELLED)` | WorkItem | 120409, 120420, 120450 | If-Match |
| W6 | `PUT /work-items/{work_item_id}/baseline` | work-items | RegisterOwned · `work_item_write` | `requirement_version_no*（须=当前）, criterion_ids[]` | WorkItem | 120420, 120430, 120440 | If-Match |
| W7 | `GET /work-items/{work_item_id}/transitions` | work-items | RegisterOwned · `work_item_read` | — | `{items:[{at, from, to, trigger, actor, authority_source, decision_id, reason_code}]}` | 404 | — |
| W8 | `GET /work-items/{work_item_id}/available-actions` | work-items | RegisterOwned · `work_item_read` | — | `{items:[{action, target_state, allowed, reason_code, reason_key}]}`（看板拖拽开始时调用，p95 ≤150ms） | 404 | — |
| W9 | `GET /work-items/{work_item_id}/dependencies` | work-items | RegisterOwned · `work_item_read` | `direction=upstream|downstream` | `{items:[{work_item_id, key, title, state, satisfied}]}` | 404 | — |
| W10 | `PUT /work-items/{work_item_id}/dependencies/{depends_on_id}` | work-items | RegisterOwned · `work_item_write` | — | `{dependency, work_item}`（201 新建 / 200 已存在，天然幂等） | 120422, 120423, 120451, 120420 | 不需要 If-Match（关系资源；阻塞重算走 CAS 重试） |
| W11 | `DELETE /work-items/{work_item_id}/dependencies/{depends_on_id}` | work-items | RegisterOwned · `work_item_write` | — | 204 | 404 | 幂等 |
| W12 | `PUT /work-items/{work_item_id}/blockage` | work-items | RegisterOwned · `work_item_read`（服务内：`work_item_write`，或执行分派人 + `CLARIFICATION`） | `reasons*: [CLARIFICATION|EXTERNAL], note*（≤2000）, clarification_question?` | WorkItem | 120420, 120403 | If-Match |
| W13 | `DELETE /work-items/{work_item_id}/blockage` | work-items | RegisterOwned · `work_item_write` | `note` | WorkItem | 120420(blocked_by_system_reason) | If-Match |
| W14 | `GET /tenants/{tenant_id}/work-items` | tenants | RegisterTenantMember；服务内按调用者可读项目过滤（`work_item_read` 的项目集合） | W2 同名过滤 + `project_id(eq,in)`；排序同 W2；`limit ≤200` | `{items: WorkItem(摘要，含 project{id, name})[], total}` | — | — |

### 6.5 分派与租约

| # | 方法 路径 | tag | 守卫 / 权限码 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| A1 | `POST /work-items/{work_item_id}/assignments` | work-items | RegisterOwned · `work_item_read`；服务内：自认领 EXECUTOR → `work_item_execute`；评审认领 REVIEWER → BR-02-15 预检；指派他人 → AuthorityChecker(`work_item_assign`) 并记 ASSIGN 裁决 | `role*, assignee_principal_id*, lease_ttl_seconds?, reason(改派必填), rationale?(派单理由，≤2000), attention_session_ids?` | `{assignment, decision?, work_item}`（201） | 120403, 120426, 120446, 120444, 120420, 120421, 120434 | ✔；If-Match（工作项） |
| A2 | `GET /work-items/{work_item_id}/assignments` | work-items | RegisterOwned · `work_item_read` | 过滤 `status, role` | `{items: Assignment[]}` | — | — |
| A3 | `GET /assignments/{assignment_id}` | assignments | RegisterOwned · `work_item_read` | — | Assignment + ETag | 404 | — |
| A4 | `PATCH /assignments/{assignment_id}` | assignments | RegisterOwned · `work_item_read`；服务内：本人或 `work_item_assign` | `status*: RELEASED, reason` | `{assignment, work_item}` | 120403(NOT_LEASE_HOLDER), 120420, 120409 | If-Match（分派） |
| A5 | `PUT /assignments/{assignment_id}/lease` | assignments | RegisterOwned · `work_item_read`；服务内：本人 | `ttl_seconds?` | `{lease_expires_at, ttl_ms}` | 120403(NOT_LEASE_HOLDER), 120420, 120421 | 天然幂等 |

### 6.6 完成声明

| # | 方法 路径 | tag | 守卫 / 权限码 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| C1 | `POST /work-items/{work_item_id}/claims` | work-items | RegisterOwned · `work_item_execute` | `execution_id*, summary*(≤2000), self_confidence?, evidence_ids*[≥1], mappings[{criterion_id*, evidence_id?, coverage*, note?}] ≤500` | `{claim, work_item}`（201） | 120420, 120421, 120428, 120429, 120430, 120403 | ✔；If-Match |
| C2 | `GET /work-items/{work_item_id}/claims` | work-items | RegisterOwned · `work_item_read` | 排序 `sequence` | `{items: Claim[]}` | — | — |
| C3 | `GET /claims/{claim_id}` | claims | RegisterOwned · `work_item_read` | — | Claim（含覆盖与 03 证据摘要 `evidence[{id, type, producer, content_hash, limitations}]`） | 404 | — |

### 6.7 裁决

| # | 方法 路径 | tag | 守卫 / 权限码 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| D1 | `POST /work-items/{work_item_id}/decisions` | work-items | RegisterOwned · `work_item_read`（路由）+ 服务内 AuthorityChecker（BR-02-19 动作 → 权限码）；ASSIGN 与 SCOPE_CHANGE 不经此入口（422 `120436`，分别走 A1 / R10） | `action*: APPROVE|REJECT|REQUEST_CHANGES|PAUSE|RESUME|CANCEL, claim_id(评审类必填=current_claim_id), reason, failure_category, workspace_mode?(仅 REJECT 默认 RESTART / REQUEST_CHANGES 默认 CONTINUE，可覆盖), strategy_note?, feedback[{category*, detail*, location, expected, criterion_id, evidence_id}] ≤20, evidence_refs[] ≤100, verification_refs[] ≤100, attention_session_ids[]` | `{decision, work_item, branch_request?:{execution_id, branch_reason, autostart}, escalation?:{trigger}}`（201） | 120403(+reason_code), 120409, 120420, 120421, 120434, 120435, 120436, 120437, 120438, 120446 | ✔；If-Match（工作项） |
| D2 | `GET /work-items/{work_item_id}/decisions` | work-items | RegisterOwned · `work_item_read` | 排序 `created_at` | `{items: Decision[]}` | — | — |
| D3 | `GET /decisions/{decision_id}` | decisions | RegisterOwned · `work_item_read`（REQUIREMENT 目标 → `requirement_read`） | — | Decision | 404 | — |
| D4 | `GET /entities/{entity_id}/review-queue` | entities | RegisterOwned · `work_item_read` | 过滤 `risk, type, requirement_id, claimed(me|others|none), needs_judgement, baseline_stale`；排序 `waited(默认，按进入 NEEDS_HUMAN_REVIEW 时间升序), risk` | `{items:[{work_item(卡片), claim_id, coverage, verification{passed,total,inconclusive}, reviewer_claim, can_decide:bool, sod_blocked:bool}]}`（本规格提议：评审台专用投影，排除调用者因职责分离无法裁决的项，`include_blocked=true` 时返回并标记） | 403 | — |
| D5 | `GET /tenants/{tenant_id}/review-queue` | tenants | RegisterTenantMember；服务内按调用者可读项目过滤 | D4 同名过滤 + `project_id(eq,in)` | D4 同形，每项加 `project{id, name}`（团队级评审台，侧栏「评审台」默认入口，C63） | — | — |

**D1 事务顺序**（实现必须按此顺序，便于测试）：① 形状校验（理由、反馈、类别）→ ② 幂等查找 → ③ 开事务，读工作项 → ④ If-Match → ⑤ 项目阶段、终态、暂停规则 → ⑥ 状态守卫 → ⑦ 评审认领冲突 → ⑧ 职责分离与 Agent 规则 → ⑨ AuthorityChecker + 不可委托硬校验 → ⑩ 验证门 / 基线过期 → ⑪ 写 Decision + 反馈 + 迁移记录 + CAS 更新工作项 + 分派 / 声明结果 → ⑫ 失败处理：03 `ExecutionLauncher.Launch(tx, ...)`（拥有者在调用方事务内写自己的行，backend-module 委派模式）+ 有界自治 → ⑬ 关注会话结算 → ⑭ `audit.Appender(tx, delivery.decision_<action>)`，detail 含 `reason, evidence_refs, authority_source, delegation_id, failure_category, feedback_count, from_state, to_state` → ⑮ outbox `decision.made`、`work_item.state_changed`（09，同事务）→ ⑯ 幂等记录 → 提交。⑧⑨ 失败：回滚后以独立事务追加 `delivery.decision_denied`（outcome=denied）。

### 6.8 关注会话与拆解模板

| # | 方法 路径 | tag | 守卫 / 权限码 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| S1 | `POST /work-items/{work_item_id}/attention-sessions` | work-items | RegisterOwned · `work_item_read`；HUMAN | `claim_id*, surface(REVIEW_DESK)` | `{id, heartbeat_interval_ms:15000, idle_cutoff_ms:60000}`（201） | 120403(HUMAN_ONLY), 120420(非 NEEDS_HUMAN_REVIEW) | ✔ |
| S2 | `PUT /attention-sessions/{attention_session_id}` | attention-sessions | RegisterOwned · `work_item_read`；本人 | `sequence*, active_ms_delta*, ended(bool)` | `{active_ms, reviewer_lease_expires_at}` | 120454, 404 | 序号幂等 |
| T1 | `GET /tenants/{tenant_id}/decomposition-templates` | tenants | RegisterTenantMember | 过滤 `status, builtin`；排序 `key, updated_at` | `{items:[{id, key, name, status, latest_version_no, builtin}]}` | — | — |
| T2 | `POST /tenants/{tenant_id}/decomposition-templates` | tenants | Register · `decomposition_template_manage`（CCR-03） | `definition*`（§3.17）, `changelog`；`copy_from_template_id?` | `{template, version}`（201） | 120441, 120449(每团队 ≤100 模板) | ✔ |
| T3 | `GET /decomposition-templates/{template_id}` | decomposition-templates | RegisterOwned（tenant 归属）· 成员 | `version_no?` | `{template, version:{version_no, definition, content_hash, changelog}}` | 404 | — |
| T4 | `POST /decomposition-templates/{template_id}/versions` | decomposition-templates | RegisterOwned · `decomposition_template_manage` | `definition*, changelog*` | version（201） | 120441, 120409 | ✔；If-Match（模板） |
| T5 | `PATCH /decomposition-templates/{template_id}` | decomposition-templates | RegisterOwned · `decomposition_template_manage` | `name, status(ACTIVE|DEPRECATED)` | template | 120409 | If-Match |
| T6 | `POST /requirements/{requirement_id}/decompositions` | requirements | RegisterOwned · `work_item_write` | `template_id*, template_version_no*, parameters{}, dry_run(bool)` | dry_run：`{preview:{root, items[{node_key, title, type, risk, estimate_minutes, depends_on[], criteria_count}]}}`（200）；否则 `{decomposition, root_work_item, created_count}`（201） | 120439, 120441, 120442, 120443 | ✔（正式必填） |

### 6.9 跨模块接口（`module.Provide`，签名语义；Go 定义在 `internal/delivery` 根包）

| 接口 | 方法 | 使用者 | 说明 |
|---|---|---|---|
| `WorkItemReader`（00） | `Get(ctx, tenantID, workItemID) (WorkItemView, error)`；`Criteria(ctx, workItemID) ([]CriterionView, error)`；`ActiveAssignment(ctx, workItemID, role) (*AssignmentView, error)`；`ListByIDs(ctx, tenantID, ids[]) ([]WorkItemView, error)`；`Owner(ctx, kind WORK_ITEM\|CLAIM\|REQUIREMENT, id) (projectID, error)`；`RequirementBaseline(ctx, requirementID, versionNo) (RequirementSnapshot, error)`；`LoadByPrincipal(ctx, tenantID, principalIDs[]) (map[principalID]{active, queued}, error)`；`ListByState(ctx, tenantID, projectID, states[]) ([]WorkItemView{…, current_claim_id}, error)`（08 接管后重投待裁决项）；`CountTerminal(ctx, tenantID, projectIDs[], from, to) ({accepted, cancelled, with_audit}, error)`（08 闭环完整率）；`ListByAssigneeInPeriod(ctx, tenantID, principalID guid.ID, from, to time.Time) ([]WorkItemPeriodView{work_item_id, project_id, title, type, state, assignment_role, assigned_at, claimed_at?, verified_at?, accepted_at?, accepted_decision_id?, last_transition_at, requirement_id, criteria_covered, criteria_total}, error)`：返回该主体在 `[from, to)` 内持有过分派（EXECUTOR / REVIEWER）或有状态迁移的工作项，按 `last_transition_at` 排序，上限 2,000（D111，规格 10 报告自动填充与 B1 核对） | 03、05、06、07、08、collab、10（planning） | 只读视图。`WorkItemView` 含 `self_confidence`、`quality_rework_count`、`infra_retry_count`、`paused`（bool）、`require_human_decision`、`required_runtime`、`skill_tags[]`、`preferred_skill_tags[]`、`uncertainty_flag`、`accepted_decision_id`、`first_accepted_at` |
| `WorkItemTransitioner`（00） | `ExecutionStarted(ctx, tx, ExecutionRef)`；`ExecutionFailed(ctx, tx, ExecutionRef, kind INFRA\|RUNTIME)`；`VerificationQueued(ctx, tx, workItemID, batchID)`；`VerificationCompleted(ctx, tx, workItemID, batchID, Summary{required_total, passed, failed, inconclusive, pending, automated_passed, has_non_ai_pass, failed_verification_ids[]})`；`RenewLease(ctx, tx, workItemID, executorPrincipalID)`；`EnsembleFailed(ctx, tx, workItemID, planID)`（v1.1） | 03、05 | 全部以 `source_ref` 幂等；在调用方事务内执行（委派模式） |
| `DispatchAssigner`（00，**同步**） | `Assign(ctx, tx, AssignInput{work_item_id, assignee_principal_id, role EXECUTOR\|REVIEWER, expected_work_item_version, actor, authority_source, rationale, dispatch_proposal_id?, ensemble_plan_id?, lease_ttl_seconds?}) (assignmentID, decisionID, error)`；`Reassign(ctx, tx, {work_item_id, assignee_principal_id, actor, authority_source, reason, rationale, dispatch_proposal_id?}) (assignmentID, decisionID, error)`；`Release(ctx, tx, {work_item_id, assignment_id?, actor, reason}) error` | 05 | 在 fleet 事务内执行 T03 / T07 / T05（含 AuthorityChecker、BR-02-44 分支原因、ASSIGN Decision、审计、outbox）；**`role=REVIEWER`**：仅对 NEEDS_HUMAN_REVIEW 工作项、为**非迁移**（状态不变，不发 `work_item.state_changed`，记 ASSIGN Decision 与 `work_item.assigned{role:REVIEWER}`），按 BR-02-15 对受派人预检（职责分离、可裁决），`lease_ttl_seconds` 覆盖 BR-02-12 的评审认领 TTL（5–60min；05 为 Agent 评审员传 1800）（复审 #4）；错误原样返回本模块码（如 `120426` 并发认领、`120420` 阶段 / 状态、`120403`），由 05 同步反馈给确认人 |
| `WorkItemEscalator`（复审 #27） | `Escalate(ctx, tx, EscalateInput{work_item_id, kind REWORK_LIMIT\|SPINNING\|INCONCLUSIVE\|REVIEW_SLA, detail{reviewer_kind?(HUMAN\|AGENT), assignment_id?, waited_ms?}, actor}) error` | 05（Agent 评审超时：kind=REVIEW_SLA、reviewer_kind=AGENT） | 本模块是 `work_item.escalated` 的唯一发布者：同事务写升级标记（AUTONOMOUS 项目置暂停 `ESCALATION_RULE`、`require_human_decision=true`）、审计 `delivery.work_item_escalated`、outbox；同一工作项同一 kind 在未解除前幂等 |
| `ClaimSubmitter`（00） | `SubmitOn(ctx, tx, SubmitInput{work_item_id, execution_id, from_exit bool（03 E06 传 true，Heavy 择优传 false；原样传给 03 `ValidateClaimable`）, claimed_by_principal_id, summary, self_confidence?, head_commit?, evidence_ids[], criteria_map[{criterion_id, evidence_id?, coverage, note?}], cited_evidence_ids[], authority_source}) (claimID, error)` | 03（E06 代 Agent 提交）、05（Heavy 择优） | 在调用方事务内按 T08 / BR-02-17 执行（含 `EvidenceBinder`） |
| `DecisionReader`（00） | `Get(ctx, tenantID, decisionID) (DecisionView{action, reason, failure_category, feedback[{category, location, expected, detail, criterion_id?}], actor, authority_source, work_item_id?, created_at}, error)`；`FeedbackChain(ctx, workItemID) ([]DecisionView, error)`（该工作项全部 REJECT / REQUEST_CHANGES，按时间） | 06（反馈入库）、03（第 4 层上下文） | 只读 |
| `RequirementCreator`（00） | `CreateFromIntake(ctx, tx, CreateFromIntakeInput{TenantID, ProjectID, IntakeID, TriageCardID, Title, Description, Criteria[{text, required}], Priority, Actor}) (RequirementRef{ID, ProjectID, Version}, error)`；`AppendFromIntake(ctx, tx, AppendFromIntakeInput{TenantID, RequirementID, IntakeID, BaseVersionNo, Summary, Diff{criteria_added[], criteria_retired[], impacts[]}, Change *IntakeChange{CriteriaDiff[]{op ADD\|RETIRE\|REPLACE, criterion_id?, text}, Impacts[]{work_item_id, action STALE\|CANCEL}, SourceIntakeID, ApprovedAdjustment *{ScheduleDeltaDays, CostDeltaMicros, ScopeDeltaCriteria}}, Actor}) (changeRecordID, error)` | 01 | Create：DRAFT 需求（`source_intake_id`），同事务；Append：= BR-02-32 SCOPE 变更（不可委托）；`Diff` 与 `Change` 互斥（都给或都不给 → 120400），`Change` 为甲方变更申请载荷（01 kind=CHANGE，BR-02-63），有 `ApprovedAdjustment` 时同事务调用 07 `ToleranceAdjuster.ApplyApprovedChange`；**step-up 由 01 在调用前以 purpose `intake_convert_privileged` 校验**，本接口不再接收或校验重新认证证明（复审 #11；08 §6.9 登记该用途覆盖追加） |
| `ChangeImpactPreviewer`（00，D117） | `Preview(ctx, tenantID, requirementID, baseVersionNo, criteriaDiff[]{op ADD\|RETIRE\|REPLACE, criterion_id?, text}) (ChangeImpactPreview{AffectedWorkItems[]{work_item_id?, effect STALE\|NEW, criterion_id?}, CurrentVersionNo}, error)` | 01 | **只读，不写库**（可在调用方事务内或独立读取）；规则见 BR-02-63。`baseVersionNo ≠ 当前` 不报错，照常按当前版本计算并返回 `CurrentVersionNo`，由调用方判定过期（01 → 110453）；需求不存在 / 跨租户 → 120404；需求非 BASELINED → 120439；`criterion_id` 不属于该需求或已退役 → 120430 |
| `WorkItemReopener` | `ReopenFromAcceptance(ctx, tx, {tenant_id, work_item_ids[], source_acceptance_id, feedback[], reason, actor}) ([]decisionID, error)` | 01（返工来件 `REOPEN_WORK_ITEMS`） | 逐项执行 T26 |

**本模块使用的他方接口**（与 §12.1 一致）：03 `ExecutionLauncher{Launch, Pause, Resume, Cancel, CancelExecution}`、03 `EvidenceReader{… , ChangeSimilarity, LiveExecution, WorkItemCost}`、03 `EvidenceBinder{ValidateClaimable, Attach}`；05 `AgentDirectory`、`DispatchProvenance.DispatchedBy`；07 `ProjectGate`、`ProjectReader`、`AcceptanceReader`、`ToleranceAdjuster.ApplyApprovedChange`（BR-02-63）；08 `AuthorityChecker{Check, ListEligible}`；01 `ClarificationRequester.RequestFromWorkItem`；12 `WorkflowResolver`（BR-02-62 缺陷部署守卫）；09 `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`、`Notifier`。

---

### 6.10 D112 / D113 接口

| # | 方法 路径 | tag | 守卫 · 权限 | 请求 | 响应 `data` | 错误 |
|---|---|---|---|---|---|---|
| W15 | `POST /work-items/{work_item_id}/readiness-checks` | work-items | RegisterOwned · `work_item_read` | — | §3.20 记录 | 404 |
| W16 | `PATCH /work-items/{work_item_id}`（扩展 W 系列已有 PATCH） | work-items | RegisterOwned · `work_item_write`；If-Match | 新增就绪字段、缺陷字段、appetite / 容差（仅在 EXCEPTION 决定中修改，直接 PATCH 返回 409 120465） | WorkItem | 120467、120468 |
| P1 | `GET /work-items/{work_item_id}/plans` | work-items | RegisterOwned · `work_item_read` | — | `Plan[]` | — |
| P2 | `GET /plans/{plan_id}` | plans | RegisterOwned · `work_item_read` | — | Plan | 404 |
| P3 | `POST /plans/{plan_id}/decisions` | plans | RegisterOwned · `plan_approve`（人类）或 08 策略 `plan_review`（评审 Agent）| `{action: APPROVE\|REJECT, feedback?[]}`；REJECT 反馈必填 | Plan + WorkItem | 403 120403、409 120420（非 PLAN_REVIEW）、422 coverage |
| AN1 | `POST /work-items/{work_item_id}/andon-pulls` | work-items | RegisterOwned · `andon_pull` | `{reason≥10, evidence_refs?[]}`；Idempotency-Key | AndonEvent | 409 已 ACTIVE 时返回现有记录 200 |
| AN2 | `POST /entities/{entity_id}/andon-pulls` | entities | RegisterOwned · `andon_pull` | 同上（项目级） | AndonEvent | — |
| AN3 | `POST /andon-pulls/{andon_id}/clearance` | andon-pulls | RegisterOwned · `andon_clear`（仅人类） | `{clearance_note}` | AndonEvent + WorkItem[] | 403 HUMAN_ONLY |
| AN4 | `GET /entities/{entity_id}/andon-pulls?state=` | entities | RegisterOwned · `work_item_read` | pkg/query：`state`, `scope`, `created_at` | `AndonEvent[]` | — |
| E1 | `GET /work-items/{work_item_id}/exceptions` | work-items | RegisterOwned · `work_item_read` | — | `Exception[]` | — |
| E2 | `GET /exceptions/{exception_id}` | exceptions | RegisterOwned · `work_item_read` | — | Exception | 404 |
| E3 | `POST /exceptions/{exception_id}/decision` | exceptions | RegisterOwned · `exception_decide`（仅人类，不可委托） | `{decision, reason, new_appetite_minutes?, new_tolerance?}`；If-Match | Exception + WorkItem | 403 HUMAN_ONLY / NON_DELEGABLE、422 120468 |
| E4 | `POST /work-items/{work_item_id}/exceptions` | work-items | RegisterOwned · `work_item_write` 或负责人 | `{dimension, report}` | Exception | 409 120465（已有 OPEN） |
| Q1 | `GET /work-items/{work_item_id}/internal-qa-rounds` | work-items | RegisterOwned · `work_item_read`（团队成员；甲门户无此路由） | — | `InternalQaRound[]` | — |
| X1 | `GET /entities/{entity_id}/escapes` | entities | RegisterOwned · `work_item_read` | pkg/query：`stage`, `category`, `created_at` | `Escape[]` | — |

本模块新增提供接口（00 §8）：`WorkItemTransitioner.PlanProposed(tx, work_item_id, plan)`、`InternalQaCompleted(tx, work_item_id, claim_id, stage, result, verdict)`（03 调用，source_ref 幂等）；`SetSystemBlock(tx, work_item_id, reason ∈ {TOOL_UNAVAILABLE, PROVIDER_UNAVAILABLE}, on, source_ref)`（11 调用，BR-02-61）。新错误码：120461 `readiness_unmet`、120462 `plan_not_approved`、120464 `andon_active`、120465 `exception_pending`、120467 `accountable_human_required`、120468 `exception_continue_requires_new_budget`（i18n `delivery.error.*` 三语）。

## 7. 后台任务与事件

### 7.1 任务（经 09 `JobQueue`；周期任务互斥用 `internal/infra/dlock`，锁名 `delivery:<job>`，`maxHold` = 周期 ×2）

| 任务 | 触发 / 频率 | 互斥 | 处理 | 重试 |
|---|---|---|---|---|
| `delivery.lease_reclaim` | 每 30s | dlock | 扫描 `status='ACTIVE' AND lease_expires_at < now-60s`（挂起租约 `lease_expires_at IS NULL` 天然不在扫描范围，BR-02-12），每批 200，按租户分组；逐条事务 CAS：EXECUTOR → T06 / BR-02-13；REVIEWER → EXPIRED（无状态迁移，SSE 释放认领） | 单条失败跳过并在下一轮重试；连续 5 轮失败告警（metrics `delivery_lease_reclaim_failures_total`） |
| `delivery.auto_retry` | 事件驱动（T19 写入延迟任务）：第 1 次 +30s、第 2 次 +120s；上限读 07 项目策略 | 任务键 `retry:<work_item_id>:<attempt>` 去重 | 执行时重读工作项：仍 EXECUTION_FAILED、未暂停、机器可用由 03 判断 → 03 `Launch(RETRY_INFRA)`；否则放弃 | 03 不可用：指数退避 30s→15min，最多 6 次后 N7 |
| `delivery.review_sla` | 每 5min | dlock | NEEDS_HUMAN_REVIEW 超 24h 未裁决 → N6 提醒（每项每 24h 一次）；超 72h → 通知团队所有者并发布 `work_item.escalated{trigger:REVIEW_SLA}`（本规格提议：PRD 未定 SLA，取 1 / 3 个工作日量级） | 幂等键 `sla:<id>:<bucket>` |
| `delivery.stuck_check` | 每 5min | dlock | VERIFYING 超 60min 无完成回调 → 通知负责人 + 03 诊断事件；REWORK 且待启动分支 autostart=false 超 4h → 提醒负责人；EXECUTION_FAILED 超 24h → 提醒 | 同上 |
| `delivery.attention_close` | 每 60s | dlock | 关闭 `last_heartbeat_at < now-45s` 或已持续 4h 的会话 | — |
| `delivery.dependency_sweep` | 每日 03:00 UTC | dlock | 全量重算阻塞原因与汇总项状态（防事件遗漏）；差异写 `reason_code=RECONCILED` 迁移并告警 | — |

### 7.2 发布的领域事件（09 outbox，与业务同事务；载荷外壳见 00 §7.1，下列为 `data`）

| 事件 | 触发 | data |
|---|---|---|
| `requirement.versioned` | R7、R10 | `{requirement_id, version_no, change_record_id?, criteria_added[], criteria_retired[], stale_work_item_ids[]}` |
| `work_item.created` | T01 | `{work_item_id, requirement_id, parent_id, type, risk, state, decomposition_id?}` |
| `work_item.state_changed` | 每次迁移与覆盖层变化 | `{work_item_id, from, to, trigger, reason_code, decision_id?, claim_id?（to ∈ {AGENT_CLAIMED_DONE, VERIFYING, NEEDS_HUMAN_REVIEW} 时必带，供 08 收件箱 source_key，复审 #20）, paused, version, failure_kind?(INFRA\|RUNTIME，进入 EXECUTION_FAILED 时), infra_retries_exhausted?(bool)}`（不含模块内 `pause_reason`；消费者需原因时经 `WorkItemReader` 读取） |
| `work_item.assigned` | T02 / T03 / T07 / 评审认领 | `{work_item_id, assignment_id, role, assignee{kind, principal_id, agent_id}, lease_expires_at, decision_id?, replaced_assignment_id?}` |
| `work_item.escalated`（CCR-04） | BR-02-26 / 27、T11、review_sla、`WorkItemEscalator.Escalate`（05 Agent 评审超时） | `{work_item_id, trigger: REWORK_LIMIT|SPINNING|INCONCLUSIVE|REVIEW_SLA, reviewer_kind?(HUMAN|AGENT), count?, ratio?}`（本模块为唯一发布者） |
| `claim.submitted` | T08 | `{claim_id, work_item_id, execution_id, head_commit?, evidence_ids[], coverage{required, covered}, risk, type, self_confidence?}`（03 据此排队必需验证；08 据此评估 SENSITIVE_PATH / LOW_SELF_CONFIDENCE） |
| `decision.made` | 每个 Decision | `{decision_id, action, target{kind,id}, work_item_id?, from_state, to_state, failure_category?, feedback_categories[], authority_source, delegation_id?, attention_ms?, branch_request?}` |

### 7.3 消费的事件 / 回调

| 来源 | 处理 | 幂等 |
|---|---|---|
| 03 `WorkItemTransitioner` 回调 | T04、T09–T13、T18–T20、续约 | `source_ref` |
| `dispatch.applied`（05） | **不消费**：分派已由 05 经 `DispatchAssigner` 在 fleet 事务内同步落库 | — |
| `agent.retired` / `agent.state_changed`（05） | BR-02-16 | 09 `domain_event_receipts`（Transactional） |
| `budget.threshold_reached{threshold_pct:100}`（07） | 项目内全部非终态工作项置暂停 `BUDGET`（单事务每批 200）；`threshold_pct=80` 不处理 | 同上 |
| `delegation.revoked` / `delegation.expired`（08） | 释放该受托 Agent 持有的 REVIEWER 认领 | 同上 |
| `acceptance.received`（07，同模块直接调用） | 不改变工作项状态；仅刷新 L4 投影 | — |

---

### 7.4 D112 / D113 任务与事件

| 任务 | 触发 / 频率 | 互斥 | 说明 |
|---|---|---|---|
| `delivery.readiness_recheck` | 消费 `requirement.versioned`、就绪字段编辑 | 按工作项 | 重跑 BR-02-47，执行 T27 / T28 |
| `delivery.requirement_quality_check` | 需求基线化、来件转换 | 按需求 | BR-02-50；调用 AGENT_JUDGE（经 03 只读执行） |
| `delivery.tolerance_forecast` | 每 15 分钟 | dlock `delivery:tolerance-forecast` | BR-02-57，越界执行 T38 |
| `delivery.escape_learning` | 消费 `quality.escaped`（02 / 07 / 12 Part A）与 08 `quality.escape_learning_decided` | 按 `escape_key` | BR-02-58 回流 06 / 11（唯一执行方）；指数退避重试 5 次 |

发布：`work_item.ready_checked`、`plan.proposed`、`plan.approved`、`plan.rejected`、`andon.pulled`、`andon.cleared`、`exception.raised`、`exception.decided`、`quality.escaped`、`quality.escape_learning_proposed`，以及带 `escalation_reason` 的 `work_item.escalated`。内部失败相关事件（T12、T34、Agent 批准者的 T32）载荷带 `visibility: "INTERNAL"`，08 收件箱与 07 / 05 的人类可见投影**必须**过滤。
消费：03 `internal_qa.passed` / `internal_qa.failed` / `cheating.suspected` / `acceptance_suite.frozen`（经 `WorkItemTransitioner` 同步回调为主，事件作为审计与投影）。

## 8. 页面规格

共通：路由前缀 `/t/:tenantID/p/:projectID`；团队级入口 `/t/:tenantID/work-items`（W14，跨项目，带项目筛选）与 `/t/:tenantID/review`（D5，侧栏「评审台」默认入口），选中项后在同一布局内展示，跳转到项目内路由保持 deep link（C63）；外壳与工作区切换复用 IAM 应用；组件 `CompletionLadder`、`ActorAvatar`、`StateBadge`、`EvidenceCard`、`DecisionPanel`、`StructuredFeedbackForm`、`ExecutionBranchTree`（03 提供数据）、`AuthorityNote` 放 `packages/ui`（跨 delivery 页面与 08 总览复用）；本规格新增 `CoverageMatrix`、`BoardColumn`、`RequirementDiff` 放 delivery 应用 `components/`。所有页面：加载骨架；错误态（可重试，显示 `meta.request_id`）；无权限态（「缺少权限 `work_item_read`」+ 联系团队所有者）；离线态（顶部条「已离线，正在重连…」，写按钮禁用，SSE 重连后整页重取）；实时：订阅项目主题 `tenant/{t}/project/{p}`（工作项变化扇出，本模块向 09 注册 `TopicAuthorizer`：需 `work_item_read`）、单项主题 `tenant/{t}/work_item/{id}` 与 `tenant/{t}/inbox/{me}`，变化 ≤1s 呈现（00 §9 主题语法，09 不支持通配）。UI 永远不把 L1 显示为「完成 ✓」，而写「已声明完成（未验证）」。

### 8.1 工作项看板 `/work-items?view=board`

- **目的**：按状态一屏掌握工作项流动，拖拽只能做状态机允许的事。
- **布局**：顶部筛选条（需求、类型、风险、执行者〔人 / Agent / 具体成员〕、仅看基线过期、仅看暂停、搜索 key/标题）+ 视图切换〔看板｜列表〕+〔新建工作项〕〔从模板拆解〕；列（W3 列定义）：

| 列 key | 包含状态 | 列头语义色 |
|---|---|---|
| ready | READY | 中性 |
| claimed | CLAIMED | 中性 |
| in_progress | IN_PROGRESS | `--info` |
| claimed_done | AGENT_CLAIMED_DONE、VERIFYING | `--muted-foreground` / `--info` |
| review | NEEDS_HUMAN_REVIEW | `--warning` |
| rework | REWORK | `--primary` |
| accepted | ACCEPTED | `--success` |
| exceptions | BLOCKED、EXECUTION_FAILED | 虚线灰 / `--destructive` |
| cancelled（默认隐藏，筛选开启） | CANCELLED | 灰 |

- **卡片**：key、标题（2 行截断）、`StateBadge`（同列多状态时）、`CompletionLadder` 迷你版（L1–L4 四点）、执行者 `ActorAvatar`（人圆形 / Agent 圆角方形 + 芯片）、风险徽标、分支数「⑂3」、返工数「↻2」、徽标：暂停（⏸ + 原因 tooltip）、基线过期（琥珀「基线过期 v3→v4」）、依赖未满足、评审认领人头像；汇总项显示子项进度「5/8」。
- **拖拽**：开始拖拽时调用 W8，合法目标列高亮、非法列置灰并在悬停时显示原因文案；放下映射：

| 拖拽 | 行为 |
|---|---|
| ready → claimed | 若调用者为人且有 `work_item_execute`：确认气泡「认领 WI-42，租约 4 小时」→ A1 自认领；有 `work_item_assign`：弹分派对话框（成员选择器，人与 Agent 分组，显示负载与 05 首次验收率） |
| claimed / in_progress → ready | 本人：确认「释放认领」→ A4；负责人：改派对话框（理由必填）|
| 任意可阻塞列 → exceptions | 阻塞对话框（原因单选 CLARIFICATION / EXTERNAL、说明必填、澄清问题可选）→ W12 |
| exceptions(BLOCKED) → ready/claimed | 解除对话框 → W13 |
| exceptions(EXECUTION_FAILED) → in_progress | 确认「重试执行（不计入质量返工）」→ 03 创建 RETRY_INFRA 执行 |
| review → accepted | 不直接通过：跳转评审台并选中该项（本规格提议：通过必须看到证据与 AuthorityNote） |
| review → rework | 打开拒绝弹窗（§8.4 同组件） |
| 其他 | 卡片回弹 + toast（`--destructive`）：「不能把工作项直接移到「已接受」：必须由有裁决权的人在评审台通过」等，文案来自 `reason_key`；埋点 `board_drag_rejected` |

- **空态**：无工作项 →「这个项目还没有工作项」+〔从模板拆解〕〔新建工作项〕；筛选无结果 →〔清除筛选〕。
- **性能**：每列虚拟滚动；1000 卡片首屏 <2s，拖拽帧率 ≥50fps。
- **移动端**（<768px）：列变为顶部横向 Tab（显示计数），不支持拖拽；卡片长按 / 「⋯」打开「操作」底部 Sheet，列出 W8 合法动作与不可用原因。

### 8.2 工作项列表 `/work-items?view=list`

- `auto-table`：列 key、标题、状态、需求、类型、风险、估算、执行者、完成阶梯、覆盖率、返工数、基线、更新时间；排序 / 过滤与 W2 能力一致（`meta=capabilities` 驱动过滤器 UI）；多选仅支持「导出 CSV」与「批量设置风险 / 估算」（逐项 If-Match，失败项列出）；**不提供批量通过**（PRD §12.2）。分页 50 / 页。移动端：卡片列表，只显示 key、标题、状态、执行者。

### 8.3 工作项详情 `/work-items/:workItemID`

- **头部**：key + 标题（可编辑，`work_item_write`）、`StateBadge`、暂停横幅（原因、发起者、〔恢复〕需 `decision_pause`，升级类显示「需人类放行」）、基线过期横幅（「需求已升至 v4，本项基于 v3」+〔对比〕〔重设基线〕）、`CompletionLadder` 完整版：L1「已声明完成（未验证）· 时间 · 声明者」、L2「验证通过 n/n」（含 INCONCLUSIVE 琥珀数）、L3「已接受 · 名字 · 权限来源」、L4「甲方已验收」（07）。
- **主区 Tab**：
  1. **概览**：描述、类型 / 风险 / 估算（行内编辑）、父项与子项树、依赖（上游 / 下游，〔添加依赖〕成环时显示路径「WI-3 → WI-7 → WI-3」）、分派（执行者、租约倒计时 `CountdownRing`、〔续约〕〔释放〕〔改派〕）。
  2. **验收标准 ↔ 证据**：`CoverageMatrix`——行 = 关联标准（code + 文本，退役标准划线），列 = 最近声明（可切换历史声明）；单元格 COVERED（绿勾 + 证据数）/ PARTIAL（半圆）/ NOT_APPLICABLE（斜杠 + 理由 tooltip）/ 未覆盖（琥珀「未覆盖」）；点击单元格展开 `EvidenceCard` 列表（来源卡：producer、时间、hash 前 8 位可复制、验证器 / 版本、结果、局限）。
  3. **执行分支**：嵌入 03 `ExecutionBranchTree`（分支序号、branch_reason 色：QUALITY 紫、STRATEGY / ENSEMBLE 并列枝、RETRY_INFRA 灰）；勾选两枝 →〔对比证据〕并列显示两枝声明的覆盖矩阵与验证（MVP 并列，v1.1 专用对比视图）。
  4. **裁决与审计时间线**：合并迁移记录（W7）、裁决（D2，含反馈卡片：类别、位置、期望、详情）、变更记录；每条显示 `ActorAvatar` + 权限来源（`ROLE:delivery_lead` 显示为「交付负责人角色」，`DELEGATION:<id>` 显示「委托 #id，剩余 3 天」链接到 08）；〔查看审计哈希〕跳 08 审计页。
- **右栏动作**（按 W4 `allowed_actions` 渲染，不可用时置灰 + tooltip 原因）：〔认领〕〔分派〕〔提交完成声明〕（仅执行者，打开声明抽屉：摘要、证据选择〔03 自动收集的 commit / 测试报告 / 日志 + 人工观察〕、逐条标准映射、至少 1 条证据才可提交）〔在评审台打开〕〔阻塞 / 解除〕〔暂停 / 恢复〕〔取消〕（红色语义，理由必填）。
- **移动端**：Tab 变为分段控件；右栏动作收为底部固定操作条（最多 2 个主按钮 +「更多」Sheet）；覆盖矩阵改为按标准的折叠列表。

### 8.4 评审台 `/review`（核心）

- **目的**：高质量、低成本地裁决；计量人工关注时长。
- **布局（≥1024px 三栏）**：
  - **左：队列**（D4）：默认按等待时长；每项显示 key、标题、风险、等待时长、覆盖「7/8」、验证「3/3」或「2/3 · 1 待判」、评审认领人；顶部筛选（风险、类型、需求、「仅我可裁决」默认开）；被职责分离排除的项默认隐藏，打开「显示我不能裁决的」后灰显并标「你执行过此项」。
  - **中：证据区**：声明摘要（「已声明完成（未验证）」+ 声明者 + 自评置信度）；`EvidenceCard` 列表；验证结果表（层级 AUTOMATED / AI_ASSISTED「仅参考」/ HUMAN，结果，验证器版本，输出摘要展开）；diff 摘要（03 COMMIT 证据：文件数、+/- 行数、按目录聚合，前 20 个文件，可展开到 03 diff 视图）；验收标准覆盖（`CoverageMatrix` 单列版，未覆盖琥珀置顶）；「使用的知识」折叠区（06 提供：条目、来源、得分）；返工历史（上一次反馈卡片，便于确认是否修复）。
  - **右：`DecisionPanel`**：`AuthorityNote`「你以 交付负责人 身份裁决 · 来源：角色 delivery_lead」或「以 Agent 评审员 身份 · 委托 #123（剩余 2 天，条件：风险≤低）」；评审认领状态（〔认领评审〕/「你已认领 · 剩 12:30」/「Morgan 正在评审」）；按钮〔通过 A〕〔要求修改 X〕〔拒绝 R〕〔暂停〕；理由输入（必填规则按 BR-02-22 动态标星）；人工关注计时显示「本次关注 3 分 12 秒」。
- **按钮可用条件**：

| 按钮 | 可用 | 不可用文案 |
|---|---|---|
| 认领评审 | NEEDS_HUMAN_REVIEW、无他人有效认领、预检通过 | 「Morgan 正在评审（剩 9 分钟）」/「你执行过此工作项，不能评审」 |
| 通过 | 预检 APPROVE 允许；必需验证全 PASSED（或 INCONCLUSIVE 且人类 + 理由）；未过期基线；无他人认领 | 「还有 1 项必需验证未通过」「需求基线已过期，需先要求修改」「你的委托不覆盖高风险工作项」 |
| 要求修改 / 拒绝 | 预检允许 | 同上授权文案 |
| 暂停 | `decision_pause` | — |

- **拒绝 / 要求修改弹窗**（`StructuredFeedbackForm`）：失败类别单选（质量拒绝 / 策略变更 / 范围变更〔仅基线过期时可选〕），工作区（拒绝默认「从头重做」、要求修改默认「在原产出上修改」）；反馈条目 1–20：类别*（功能缺陷 / 需求偏差 / 样式 / 性能 / 安全 / 其他）、位置、期望、详情*（≥5 字）、关联标准下拉、关联证据下拉；理由*；第 3 次质量返工时显示警示「提交后该工作项将暂停并升级，返工分支需人工放行」。未填必填项提交按钮禁用。
- **快捷键**（焦点不在输入框时；`?` 显示帮助）：`J`/`K` 下一项 / 上一项；`C` 认领评审；`A` 通过（打开确认，`Enter` 确认）；`X` 要求修改；`R` 拒绝；`P` 暂停；`E` 展开 / 收起全部证据；`1`–`9` 跳到第 n 条标准；`Ctrl/⌘+Enter` 在弹窗内提交；`Esc` 关闭弹窗。不提供批量快捷键。
- **实时**：他端裁决或认领 ≤1s 更新；当前项被他人裁决 → 中栏覆盖提示「Morgan 已通过此项」+ 自动选中下一项；提交时遇 120409 → 弹窗「此工作项已被 Morgan 处理（通过 · 10:42）」〔查看〕〔下一项〕，不丢失已填写反馈（本地草稿保留至关闭）。
- **关注计量**：按 BR-02-35~37；页面标题旁「计时中 / 已暂停（离开或空闲）」状态点，计时规则在帮助中说明。
- **状态**：空队列「没有等你裁决的工作项」+ 链接到看板；无裁决权（预检全部拒绝）「你当前没有任何裁决权限」并列出所缺权限码。
- **移动端**：单栏：队列列表 → 点进详情（证据区折叠为分段：声明 / 证据 / 验证 / 覆盖 / 知识）→ 底部固定裁决条〔通过〕〔要求修改〕〔拒绝〕；无快捷键；结构化反馈为全屏 Sheet；关注计时规则相同（visibility + 触摸活动）。

### 8.5 需求页 `/requirements/:requirementID`（列表 `/requirements`）

- **列表**：key、标题、状态（草稿 / 已基线 v3 / 已归档）、优先级、来源来件、工作项进度「12/20 已接受」、标准覆盖「9/11」；〔新建需求〕。
- **详情布局**：头部（key、标题、状态、版本选择器「v3（当前）」、〔基线化〕仅 DRAFT、〔发起范围变更〕红色语义需 `scope_change`）；Tab：
  1. **基线**：描述（Markdown）、验收标准表（code、文本、必需、引入版本、关联工作项数、覆盖状态：已被 ACCEPTED 工作项覆盖 / 进行中 / 无工作项）；DRAFT 时可行内编辑、拖动排序、新增 / 删除；BASELINED 时只读并提示「修改需发起范围变更」。
  2. **版本**：版本列表 + `RequirementDiff`（任选两版：标题 / 描述行级 diff，标准新增绿、退役红划线、替换显示「C3 → C7」），每版链接其变更记录与裁决。
  3. **工作项**：按模板树展示关联工作项，基线过期项琥珀标出 +〔批量查看〕。
  4. **拆解**：〔从模板拆解〕向导：选模板与版本 → 填参数（如「并行编码数 2」）→ 预览树（dry_run）→ 确认创建（显示将创建 n 项）。
- **范围变更对话框**：基于版本 vN（显示）；改标题 / 描述 / 优先级；标准：新增、退役、改写（=替换）；影响预览（自动计算受影响工作项列表，每项可选「标记过期」/「取消」）；摘要*、理由*；来源来件（可选）；提交前 step-up（ReauthenticationDialog）；冲突 120440 →「需求已被他人更新到 v5，请刷新后重做」。
- **移动端**：Tab 保留；标准编辑与范围变更对话框改为全屏 Sheet；diff 改为上下排列。

---

### 8.6 D112 / D113 页面增补

- **看板列**：在「声明与验证」与「待裁决」之间新增「**内部验收**」列（INTERNAL_QA），卡片显示「内部第 n / N 轮」与当前阶段（测试 Agent / 产品验收 Agent）；新增「**草稿（就绪未满足）**」列（DRAFT，卡片列出缺项数）；STOPPED 卡片红色停线条，EXCEPTION 卡片琥珀色「例外待决定」。内部失败在看板上只表现为卡片回到「返工」并轮次 +1，**不弹任何通知**。
- **就绪检查面板**（工作项详情右栏顶部）：逐项打勾（目标、验收标准与编号、每条标准的验证方式、范围外、上下文、预算、自治级别、人类负责人）；缺项给出「补全」入口；需求冲突问题链接到需求页；人类自认领时弹出「就绪未满足，确认仍要开始？」并记录。
- **计划审批面板**（PLAN_REVIEW）：左为计划（改动文件、每条验收标准的实现与验证方式、风险），右为覆盖检查（未覆盖标准标红）与批准 / 拒绝（拒绝必填反馈）；显示批准者资格说明（「执行者与派单主管不可批准；评审 Agent 须为不同模型家族」）。
- **内部验收记录**（工作项详情标签）：按轮次列出 AUTOMATED / AGENT_TEST / AGENT_ACCEPTANCE 结论、截图与证据、作弊嫌疑标记；仅团队可见。
- **Andon 按钮**：工作项详情与卡片菜单「拉 Andon 停线」，对话框要求理由与证据；停线条显示拉绳人（人或 Agent）、时间、下游受影响数；清除按钮仅对人类负责人 / 交付负责人可用（Agent 视角隐藏并说明）。
- **例外报告对话框**：维度、预测（appetite / 已用 / 预测总量 / 容差图）、现状、选项与影响、建议；四个决定按钮（继续需填写新 appetite），仅人类可操作，显示「不可委托」。
- **穿透徽标**：工作项与评审台显示「穿透 ×n」，点开查看当时内部验收结论；团队指标卡显示穿透率趋势。
- **评审台**：进入评审台的只有 INTERNAL_QA 通过或已升级的工作项；顶部显示「内部验收：自动 ✓ 测试 Agent ✓ 产品验收 Agent ✓（第 2 轮通过）」与升级原因（如有）。

## 9. 通知（经 09 `Notifier`；渠道：站内 inbox + SSE 必发，邮件按 08 用户偏好；Agent 不接收通知，其工作经 03 下发）

| # | 事件 | 接收人 | 渠道 | 模板（zh-CN；en-US / ms-MY 同 key） | 去重 / 合并 |
|---|---|---|---|---|---|
| N1 | `work_item.assigned`（HUMAN 执行者） | 受派人 | inbox、邮件 | `delivery.notify.assigned`：「你被分派了 {key}「{title}」，租约至 {lease_expires_at}。」 | 键 `assigned:{assignment_id}` |
| N2 | 进入 NEEDS_HUMAN_REVIEW | 08 `AuthorityChecker.ListEligible(project, decision_approve)` 中非职责分离排除的人类 | inbox、SSE 徽标 | `delivery.notify.review_ready`：「{key}「{title}」待裁决：验证 {passed}/{total}，覆盖 {covered}/{required}。」 | 同项目 5 分钟内合并为「{n} 项待你裁决」 |
| N3 | → REWORK（裁决） | 执行者（人）、交付负责人 | inbox | `delivery.notify.rework`：「{key} 需返工（{failure_category}）：{reason 前 80 字}」 | 键 `rework:{decision_id}` |
| N4 | 租约过期回收 | 原受派人、交付负责人 | inbox | `delivery.notify.lease_expired`：「{key} 的认领已过期，已回到就绪。」 | 每项每小时 ≤1 |
| N5 | `work_item.escalated` | 交付负责人；AUTONOMOUS 项目加团队所有者 | inbox、邮件 | `delivery.notify.escalated.rework_limit`：「{key} 已第 {count} 次质量返工，已暂停，等待人工放行。」；`.spinning`：「{key} 相邻两次执行改动文件比例 {ratio}%（<8%），疑似原地打转，已暂停。」；`.inconclusive`；`.review_sla` | 键 `esc:{work_item_id}:{trigger}`，24h 内不重复 |
| N6 | 评审超 24h | N2 同一人群 | inbox | `delivery.notify.review_overdue`：「{key} 已等待裁决 {hours} 小时。」 | 每项每 24h ≤1 |
| N7 | 执行故障超自动重试上限 / RUNTIME 故障 | 执行者、交付负责人 | inbox、邮件 | `delivery.notify.execution_failed`：「{key} 执行失败（{kind}），自动重试已用尽，请重试、换机器或转交。」 | 键 `fail:{execution_id}` |
| N8 | `requirement.versioned`（v≥2） | 交付负责人、受影响工作项执行者 | inbox | `delivery.notify.requirement_versioned`：「{req_key} 升至 v{version}，{count} 个工作项基线过期。」 | 键 `ver:{requirement_id}:{version}` |
| N9 | APPROVE | 执行者（人）；执行者为 Agent 时通知分派人 | inbox | `delivery.notify.accepted`：「{key} 已被 {actor} 接受。」 | 同执行者 10 分钟内合并 |
| N11 | `work_item.escalated{INTERNAL_ROUNDS_EXHAUSTED}`（T35） | 人类负责人、交付负责人 | inbox（HIGH） | `delivery.notify.internal_rounds_exhausted`：「{wi_key} 内部 {n} 轮仍未通过验收，需要你判断。」 | 键 `esc:{wi}:{claim_chain}` |
| N12 | 需求质量检查发现冲突 / 歧义（BR-02-50） | 交付负责人（不通知甲方） | inbox | `delivery.notify.requirement_conflict`：「{req_key} 有 {n} 处需求冲突或歧义，相关工作项暂不可派给 Agent。」 | 键 `rq:{req}:{version}` |
| N13 | `andon.pulled` | 人类负责人、交付负责人；项目级另通知团队所有者 | inbox（CRITICAL）+ 浏览器推送 | `delivery.notify.andon_pulled`：「{actor} 对 {target} 拉了 Andon：{reason}」 | 键 `andon:{id}` |
| N14 | `exception.raised` | 人类负责人、持 `exception_decide` 的人 | inbox（HIGH） | `delivery.notify.exception_raised`：「{wi_key} 预测将超出{dimension}容差，需要你决定：继续 / 重新塑形 / 暂停 / 终止。」 | 键 `exc:{id}` |
| — | **内部失败（T12、T34、Agent 批准者的 T32）** | **无人类通知**（D113 不穿透） | — | — | — |
| N10 | 前驱 CANCELLED | 后继执行者、交付负责人 | inbox | `delivery.notify.dependency_cancelled`：「{key} 依赖的 {dep_key} 已取消，请调整依赖。」 | 键 `depc:{work_item_id}:{dep}` |

---

## 10. 验收标准

> 测试层：unit = 状态机 / 规则纯函数；integration = 真实 SQLite + PostgreSQL + MySQL 迁移下的服务与 HTTP（三方言矩阵）；e2e = 真实后端 + 浏览器；manual = 人工。所有 403 / 409 / 422 用例同时断言「目标状态与 version 未变、无 Decision 行、无 outbox 事件」。

**主流程**

```
AC-02-01  [e2e]  关联 US-02-01, US-02-02, US-02-03, US-02-05, US-02-06, BR-02-17, BR-02-24
Given 项目 P 处于 ACTIVE，交付负责人 Morgan 创建需求 R（3 条必需标准）并基线化为 v1
When  Morgan 用 software-dev-agile v1（coder_count=2）拆解 R，把 coding#1 分派给 Agent Ada，
      Ada 的执行提交声明（2 条证据覆盖 C1、C2，C3 映射 NOT_APPLICABLE 附理由），03 回调必需验证 3/3 PASSED，
      工程师 阿澈 在评审台认领并通过
Then  生成 1 个汇总项 + 7 个子项与依赖（coding#1、coding#2 依赖 sprint-plan，qa 依赖两者）；
      coding#1 依次经历 BLOCKED(DEPENDENCY)… → CLAIMED → IN_PROGRESS → AGENT_CLAIMED_DONE → VERIFYING → NEEDS_HUMAN_REVIEW → ACCEPTED；
      卡片在 AGENT_CLAIMED_DONE 显示「已声明完成（未验证）」而非「完成」；Decision 记录 actor=阿澈、authority_source=ROLE:engineer 或 POLICY:dispatch_auto（detail.dispatch_proposal_id）、attention_ms>0；
      审计链追加 delivery.decision_approved 且哈希链校验通过；分派状态 COMPLETED
```

```
AC-02-02  [integration]  关联 US-02-01, BR-02-01, BR-02-02
Given 需求 R 为 BASELINED v1
When  调用 PATCH /requirements/{R} 修改 description，或 PATCH /acceptance-criteria/{C1} 修改 text
Then  409 业务码 120439；R 与 C1 不变
```

```
AC-02-03  [integration]  关联 US-02-09, BR-02-32
Given R 为 v1，工作项 W1 关联 C1、W2 关联 C2，W1 处于 IN_PROGRESS
When  Morgan（delivery_lead，已 step-up）提交 change-record：supersede C1 为新文本、base_version_no=1
Then  201；R.current_version_no=2；新增 C4(supersedes_id=C1)，C1.retired_in_version=2；
      W1.baseline_stale=true、W2 不受影响；产生 ChangeRecord、Decision(SCOPE_CHANGE)、delivery_change_impacts(W1,STALE)；
      发布 requirement.versioned{version_no:2, stale_work_item_ids:[W1]}；N8 发送；以上同一事务（注入 outbox 写失败时全部回滚）
```

**状态机：每个非法迁移**

```
AC-02-04  [unit+integration]  关联 §4.2 非法迁移表, BR-02-28
Given 分别处于 §4.2 各状态的工作项（11 种状态 × 全部命令 / 裁决 / 系统事件的笛卡尔积）
When  施加迁移表未列出的触发
Then  均返回 409 业务码 120420，data.reason_key 等于表中对应 key（accepted_requires_decision、executor_ceiling、claim_requires_in_progress、
      verification_pending、claim_before_start、container_not_executable、blocked、terminal、review_only、scope_rework_requires_stale、
      project_stage、blocked_by_system_reason、pause_state）；审计 delivery.work_item_transition_denied；表驱动测试覆盖矩阵全部格子
```

```
AC-02-05  [integration]  关联 PRD §6.3 硬规则
Given W 处于 IN_PROGRESS，执行者为 Agent Ada
When  Ada 以自身凭证调用 POST /work-items/{W}/decisions {action: APPROVE}，或用任意接口试图使 W 进入 ACCEPTED
Then  前者 403 业务码 120403（reason_code=AGENT_EXECUTOR）；后者 409 120420（accepted_requires_decision）；W 不变
```

```
AC-02-06  [e2e]  关联 US-02-10
Given 看板上 W 处于 NEEDS_HUMAN_REVIEW、V 处于 IN_PROGRESS
When  用户把 V 拖到「已接受」列，把 W 拖到「已接受」列
Then  V 回弹并显示「不能把工作项直接移到「已接受」：必须由有裁决权的人在评审台通过」；W 跳转评审台并选中 W，未产生任何 Decision
```

```
AC-02-07  [integration]  关联 T10, T11, T12, BR-02-26
Given W 处于 VERIFYING
When  03 回调 VerificationCompleted 分别为 {3 PASSED}、{2 PASSED + 1 INCONCLUSIVE}、{1 FAILED}
Then  分别迁到 NEEDS_HUMAN_REVIEW(needs_judgement=false)、NEEDS_HUMAN_REVIEW(needs_judgement=true；AUTONOMOUS 项目另置暂停 ESCALATION_RULE)、
      REWORK（quality_rework_count+1；写 1 条系统 Decision{action:REQUEST_CHANGES, actor_kind:SYSTEM, authority_source:SYSTEM:verification}，
      其反馈 1 条 FUNCTIONAL_DEFECT；03 收到 Launch(QUALITY, feedback_ref=该 decision_id) 且未返回 130440；发布 decision.made）
```

```
AC-02-08  [integration]  关联 BR-02-29, T10
Given 03 以 source_ref=verification-batch:B1 回调 VerificationCompleted 成功一次
When  同一回调重复投递 3 次
Then  均返回成功；W 只迁移一次，transitions 表只有一行该 source_ref，事件只发布一次
```

**权限拒绝（含不可委托）**

```
AC-02-09  [integration]  关联 BR-02-19
Given 工程师 E 无 decision_approve，08 无适用的 POLICY
When  E 对 NEEDS_HUMAN_REVIEW 的 W 提交 APPROVE
Then  403 120403（MISSING_PERMISSION）；审计 delivery.decision_denied（outcome=denied，detail.permission=decision_approve）在独立事务中写入
```

```
AC-02-10  [integration]  关联 BR-02-20, US-02-12
Given Agent 评审员 Rev 持有有效委托 D（actions 含 approve，scope 风险≤LOW），08 被错误配置为对 scope_change 返回 allowed + DELEGATION:D
When  Rev 调用 POST /requirements/{R}/change-records
Then  403 120403（NON_DELEGABLE）——本模块硬校验生效；R 版本不变
```

```
AC-02-11  [integration]  关联 BR-02-19, PRD §8.3
Given Rev 的委托 D 范围为风险≤LOW，W 风险为 HIGH
When  Rev 对 W 提交 APPROVE
Then  403 120403（DELEGATION_CONDITION）；同一 W 改为 LOW 后（且其他条件满足）APPROVE 成功，decision.authority_source=DELEGATION:D、delegation_id=D
```

```
AC-02-12  [integration]  关联 BR-02-19, CCR-03
Given 工程师 E 无 work_item_cancel / work_item_assign
When  E 提交 CANCEL；E 用 A1 把 W 分派给他人
Then  均 403 120403；W 不变
```

```
AC-02-13  [integration]  关联 BR-02-41
Given Agent Ada 仅有 EXECUTOR 角色，被分派过 W1，从未接触 W2（同项目）
When  Ada GET /work-items/{W2}、GET 列表
Then  W2 返回 404 120404；列表只含 W1
```

```
AC-02-14  [integration]  关联 BR-02-11
Given Agent Ada 有 work_item_execute，W 为 READY
When  Ada 调用 A1 {role: EXECUTOR, assignee_principal_id: Ada}
Then  403 120403（AGENT_SELF_CLAIM）
```

**职责分离**

```
AC-02-15  [integration]  关联 BR-02-21
Given 工程师 E 曾是 W 的执行者（分派已 REVOKED），现由 Agent Ada 执行并声明，E 拥有 decision_approve 与 decision_request_changes
When  E 对 W 提交 APPROVE 或 REQUEST_CHANGES
Then  403 120403（SELF_REVIEW）；评审台队列默认不显示 W；N2 不发给 E
```

```
AC-02-16  [integration]  关联 BR-02-21(d), PRD §10.6
Given 主管 Agent Lead（角色 LEAD+REVIEWER）以 POLICY 规则把 W 分派给 Ada，Lead 无委托
When  Lead 对 W 提交 APPROVE
Then  403 120403（SELF_REVIEW）
```

```
AC-02-17  [integration]  关联 BR-02-30
Given 汇总项 P 的子项 C1 由 E 执行并已 ACCEPTED，P 进入 NEEDS_HUMAN_REVIEW
When  E 对 P 提交 APPROVE
Then  403 120403（SELF_REVIEW）
```

**并发**

```
AC-02-18  [integration]  关联 BR-02-28, BR-02-15
Given W 处于 NEEDS_HUMAN_REVIEW version=7，无评审认领；Morgan 与 阿澈 都读到 version=7
When  两人并发提交（Morgan APPROVE，阿澈 REJECT），If-Match 均为 "7"
Then  恰好一个 201，另一个 409 120409 且 data.last_decision 指向胜者；只存在 1 条 Decision；W.version=8；
      若胜者为 REJECT，只请求了 1 个返工分支
```

```
AC-02-19  [integration]  关联 BR-02-15
Given 阿澈 持有 W 的有效评审认领（剩 10 分钟）
When  Morgan 认领评审，或直接提交 APPROVE
Then  均 409 120446，data.holder=阿澈；认领过期并被回收后 Morgan 提交成功
```

```
AC-02-20  [integration]  关联 BR-02-14
Given W 为 READY
When  两名工程师并发自认领
Then  一个 201，另一个 409 120426；数据库中 W 的 ACTIVE EXECUTOR 分派恰好 1 条（三方言均由唯一约束兜底：绕过服务层直接插入第二条被数据库拒绝）
```

```
AC-02-21  [integration]  关联 BR-02-06
Given W1、W2 无依赖
When  并发提交「W1 依赖 W2」与「W2 依赖 W1」
Then  一个成功，另一个 422 120422 且 data.cycle 给出路径；数据库依赖图无环
```

**幂等**

```
AC-02-22  [integration]  关联 BR-02-29
Given 带 Idempotency-Key=k1 的 REJECT 请求已成功
When  同 key 同请求体重放；同 key 改变 reason 重放
Then  前者返回与首次相同的状态码与 decision.id，无新 Decision、无新分支；后者返回平台码 176425（09 C7）
```

```
AC-02-23  [integration]  关联 BR-02-39
Given 拆解请求带 Idempotency-Key=k2 已创建 8 个工作项
When  网络重试同一请求
Then  返回首次 decomposition 与 root，工作项总数仍为 8
```

**审计同事务**

```
AC-02-24  [integration]  关联 00 §7.2, D1 事务顺序
Given W 处于 NEEDS_HUMAN_REVIEW
When  APPROVE 过程中注入 audit.Appender 返回错误
Then  整个请求 503 120460，W 状态、Decision、分派、outbox 全部回滚；恢复后重试成功且审计链连续
```

**租约与超时**

```
AC-02-25  [integration]  关联 BR-02-12, T06
Given 工程师 E 认领 W（ttl 15 分钟），未开始执行
When  时钟推进 16 分 01 秒后运行 delivery.lease_reclaim
Then  分派 EXPIRED（end_reason=LEASE_EXPIRED），W → READY，N4 发给 E 与负责人；E 再续约返回 409 120420（assignment_not_active）
```

```
AC-02-26  [integration]  关联 BR-02-13
Given Agent Ada 的 W 租约已过期 2 分钟，但 03 报告其执行 RUNNING
When  运行 lease_reclaim 4 次（每次推进 1 个 TTL）
Then  前 3 次延长租约且 W 保持 IN_PROGRESS；第 4 次不再延长并通知负责人；W 仍不被回收
```

```
AC-02-27  [integration]  关联 BR-02-12
Given Ada 的执行心跳经 03 调用 RenewLease
When  每 60s 一次持续 30 分钟
Then  分派始终 ACTIVE，lease_expires_at 始终 > now
```

```
AC-02-28  [integration]  关联 BR-02-36
Given 评审会话 S 最后心跳在 46 秒前
When  运行 attention_close 后客户端发送下一次心跳
Then  409 120454；S.ended_at 非空；active_ms 不增加
```

**有界自治与失败分类**

```
AC-02-29  [integration]  关联 BR-02-26, US-02-08
Given W.quality_rework_count=2
When  评审者 REJECT（QUALITY_REJECTION，1 条反馈）
Then  W → REWORK，count=3，暂停 AUTONOMY_REWORK_LIMIT，require_human_decision=true，03 Launch(autostart=false)，
      发布 work_item.escalated{trigger:REWORK_LIMIT,count:3}，N5 发送；之后 Agent 评审员经委托裁决 → 403 120403（ESCALATED_HUMAN_ONLY）
```

```
AC-02-30  [integration]  关联 BR-02-27
Given W 的当前执行为 QUALITY 分支，03 ChangeSimilarity 返回 changed=1、union=20（5%）
When  执行者提交声明
Then  Claim 写入、W → AGENT_CLAIMED_DONE，同事务暂停 AUTONOMY_SPINNING 并发布 escalated{trigger:SPINNING, ratio:500}；
      ratio=8.00% 时不触发（边界：严格小于 0.08）
```

```
AC-02-31  [integration]  关联 T19, T20, §5.7
Given 项目重试上限 2，W 处于 IN_PROGRESS
When  03 连续回调 3 次 ExecutionFailed{kind:INFRA}
Then  前两次各在 30s、120s 后自动 Launch(RETRY_INFRA)，infra_retry_count=2；第 3 次停在 EXECUTION_FAILED 并发 N7；quality_rework_count 始终为 0
```

```
AC-02-32  [integration]  关联 T16, BR-02-34
Given W 处于 NEEDS_HUMAN_REVIEW
When  REQUEST_CHANGES {failure_category: STRATEGY_CHANGE, strategy_note, 1 条反馈}
Then  W → REWORK；ChangeRecord(kind=STRATEGY)；03 Launch(STRATEGY) 的 parent 为声明执行的父（并行替代）；quality_rework_count 不变；旧分支证据仍可读
```

```
AC-02-33  [integration]  关联 BR-02-23
Given W 处于 NEEDS_HUMAN_REVIEW
When  REJECT 无反馈；REJECT 反馈缺 detail；REJECT failure_category=EXECUTION_FAILURE；APPROVE 携带 failure_category
Then  依次 422 120435、120435、120436、120436
```

```
AC-02-34  [integration]  关联 BR-02-22
When  REJECT / PAUSE / CANCEL 理由为空或 4 个字符
Then  422 120434；APPROVE 无理由且覆盖完整、验证全通过 → 201
```

```
AC-02-35  [integration]  关联 BR-02-24
Given W 必需验证 {AUTOMATED PASSED, AI_ASSISTED INCONCLUSIVE}
When  Agent 评审员（委托满足其余条件）APPROVE；人类 Morgan 无理由 APPROVE；Morgan 带理由 APPROVE
Then  依次 409 120438、422 120434、201
```

```
AC-02-36  [integration]  关联 BR-02-25, T17
Given W 处于 NEEDS_HUMAN_REVIEW 且 baseline_stale=true
When  APPROVE；然后 REQUEST_CHANGES{SCOPE_CHANGE}
Then  前者 409 120437；后者 W → REWORK、baseline_version_no=当前版本、baseline_stale=false、03 Launch(SCOPE)
```

```
AC-02-37  [integration]  关联 BR-02-17
Given W 处于 IN_PROGRESS
When  声明不带证据；声明引用另一工作项执行的证据；声明映射已退役标准；非执行者声明
Then  依次 422 120428、422 120429、422 120430、403 120403
```

**范围变更与需求**

```
AC-02-38  [integration]  关联 BR-02-32
Given R 当前 v3
When  基于 base_version_no=2 提交变更；未 step-up 提交变更；空 diff
Then  依次 409 120440、返回底座 step-up 要求（无变更记录）、422 120400
```

**模板**

```
AC-02-39  [unit]  关联 BR-02-38, §3.17
Given 模板定义分别为：depends_on 引用不存在节点；依赖成环；repeat 引用未声明参数；展开后 301 项；缺 ms-MY 文案
When  保存
Then  均 422 120441，data.errors[].path 指向出错位置；内置 software-dev-agile v1 通过校验且展开（coder_count=2）为 1 汇总项 + 7 子项、8 条依赖边
```

```
AC-02-40  [integration]  关联 BR-02-39
Given R 为 DRAFT；模板 T 已 DEPRECATED
When  对 R 实例化；对已基线需求用 T 实例化；dry_run=true
Then  依次 409 120439、422 120442、200 预览且数据库无新行
```

**上限**

```
AC-02-41  [integration]  关联 BR-02-03, BR-02-05, BR-02-06
When  第 51 条活跃标准；第 51 个前驱；深度第 5 层子项；第 201 个子项；项目第 5001 个工作项
Then  依次 422 120449、120423、120450、120450、120449
```

**暂停与预算**

```
AC-02-42  [integration]  关联 §4.4
Given 项目预算达到 100%（07 发布 budget.threshold_reached{threshold_pct:100}）
When  消费事件后，执行者提交声明；Morgan 在预算仍 100% 时 RESUME；预算回落后 RESUME（理由）
Then  项目内全部非终态工作项暂停 BUDGET；声明 409 120421；第一次 RESUME 409 120421；第二次成功且 W 回到暂停前状态
```

**关注计量**

```
AC-02-43  [e2e]  关联 BR-02-35~37, US-02-11
Given 评审者在评审台选中 W，保持可见与活动 120 秒，切到其他标签页 300 秒，回来空闲 90 秒，再活动 30 秒后通过
Then  decision.attention_ms ∈ [145000, 155000]（120s + 30s；后台标签页 300s 与回来后无操作的 90s 均不计，容差 ±5s）；另一设备同时打开的会话时长也被汇总
```

**离线 / 不可用**

```
AC-02-44  [integration]  关联 BR-02-42
Given AuthorityChecker 不可用
When  任何裁决
Then  503 120460，无任何状态变化，不降级放行
```

```
AC-02-45  [e2e]  关联 BR-02-43, §8
Given 评审台已打开
When  断网 10 秒再恢复
Then  断网期间显示离线条且所有写按钮禁用；恢复后 ≤3 秒重连 SSE 并刷新队列，期间他端裁决的项已移除
```

**移动端**

```
AC-02-46  [e2e]  关联 §8.1, §8.4
Given 375px 视口
When  打开看板与评审台
Then  看板为状态 Tab 无拖拽，「⋯」Sheet 列出合法动作与不可用原因；评审台为单栏 + 底部裁决条；所有触控目标 ≥44px
```

**本轮补充（跨规格审校修订）**

```
AC-02-47  [integration]  关联 §6.9 DispatchAssigner, BR-02-14
Given 05 在 fleet 事务内为 W 调用 DispatchAssigner.Assign(Ada)，同时工程师 E 已抢先自认领 W
When  Assign 执行
Then  返回 409 120426（data.holder=E）给 05，fleet 事务回滚，提案保持 PROPOSED；W 只有 E 的一个 ACTIVE EXECUTOR；
      本模块不因随后任何 dispatch.applied 事件创建分派
```

```
AC-02-48  [integration]  关联 T04, BR-02-21(c), C17
Given 交付负责人 Morgan 经 05 确认派单把 W 分派给 Agent Ada；03 以 SYSTEM 发起者 autostart 执行
When  执行开始回调 ExecutionStarted；随后 Ada 声明、验证通过；Morgan 提交 APPROVE
Then  W → IN_PROGRESS（T04 以受派人比较，不因发起者为 SYSTEM 失败）；Morgan 的 APPROVE 成功（未被 SELF_REVIEW 拒绝）
```

```
AC-02-49  [integration]  关联 BR-02-44, T07, C35
Given W 已有 1 个 INTERRUPTED 的 INITIAL 执行（Ada），W 处于 EXECUTION_FAILED
When  Morgan 改派给 Agent Bo
Then  同事务：Ada 的 INTERRUPTED 执行由 03 置 FAILED(SUPERSEDED_BY_RETRY)；Launch 以 branch_reason=RETRY_INFRA、parent=该执行调用并成功；
      infra_retry_count 不变；不出现 03 错误 130426 / 422
```

```
AC-02-50  [integration]  关联 T20b
Given W 处于 EXECUTION_FAILED，current_execution_id=X（INTERRUPTED）
When  人工在 03 恢复 X，03 回调 ExecutionStarted(X)
Then  W → IN_PROGRESS；infra_retry_count 不变；执行者租约 lease_expires_at = now+ttl；随后声明正常提交
```

```
AC-02-51  [integration]  关联 T26, BR-02-46, C7
Given W 已 ACCEPTED（first_pass=true）并在交付 D 中，甲方 PARTIALLY_ACCEPTED 且 W 的逐项结果为 REJECTED（acceptance A）
When  01 返工来件以 REOPEN_WORK_ITEMS 调用 WorkItemReopener.ReopenFromAcceptance(W, A, feedback, reason)
Then  W → REWORK；accepted_at 清空，first_accepted_at、first_pass 保持；quality_rework_count+1；03 收到 Launch(QUALITY, feedback_ref)；
      对同一 (W, A) 再次重开 → 409 120420（already_reopened）；Agent 发起 → 403 120403（HUMAN_ONLY）；
      项目阶段为 DELIVERED 时 → 409 120420（project_stage）
```

```
AC-02-52  [integration]  关联 BR-02-12, BR-02-13, T06, C6
Given Ada（AGENT ttl 10min）的 W 进入 NEEDS_HUMAN_REVIEW 后 2 小时未被裁决；另一工作项 V 的 START 在 04 排队等执行槽 40 分钟（03 执行 CREATED）
When  lease_reclaim 运行
Then  W 的执行者分派仍 ACTIVE（lease_expires_at 为 NULL，不在扫描范围）；V 不被回收（存在非终态 DELIVERY 执行）；
      W 被 REJECT 进入 REWORK 后返工分支开始（T18）时租约重置为 now+10min
```

```
AC-02-53  [integration]  关联 T07, C25
Given W 处于 REWORK，Ada 的 QUALITY 分支为 CREATED（autostart=false，因返工上限暂停后已被 RESUME 前）
When  Morgan 改派给 Bo
Then  同事务 03 CancelExecution(该 CREATED 分支, REASSIGNED)；以相同 branch_reason=QUALITY、相同 parent 为 Bo 重新 Launch；
      W 只有一个非终态 DELIVERY 执行
```

```
AC-02-54  [integration]  关联 T25, §4.4, C26
Given W 因 AUTONOMY_REWORK_LIMIT 暂停，存在 autostart=false 的 CREATED 返工分支
When  人类 RESUME（理由）
Then  同事务调用 03 ExecutionLauncher.Resume(W)，分支开始；05 派单在暂停期间对 W 返回不合格（暂停覆盖层）
```

```
AC-02-55  [integration]  关联 BR-02-27
Given W 当前执行为 QUALITY 分支，03 ChangeSimilarity 因并集文件数 3 返回 ratio=NULL
When  执行者提交声明
Then  不暂停、不发布 escalated；埋点 similarity_bucket=null
```

```
AC-02-56  [integration]  关联 BR-02-17, §6.9 ClaimSubmitter, C1
Given Agent Ada 的 AUTONOMOUS 执行以退出码 0 结束，03 在 E06 退出事务内调用 ClaimSubmitter.SubmitOn（fromExit=true，执行仍为 RUNNING）
When  提交
Then  声明成功，W → AGENT_CLAIMED_DONE；03 EvidenceBinder.Attach 把所列证据置 ATTACHED；同一执行再次提交 → 409 120420（claim_requires_in_progress）
```

```
AC-02-57  [e2e]  关联 W14, D5, §8, C63
Given 用户可读项目 P1、P2，不可读 P3；三个项目各有 NEEDS_HUMAN_REVIEW 工作项
When  打开 /t/:tenantID/review
Then  队列含 P1、P2 的项（带项目名），不含 P3；点选后可 deep link 到 /t/:tenantID/p/:projectID/review
```

```
AC-02-58  [integration]  v1.1  关联 BR-02-45, T04e, C58/C59
Given 05 为 HIGH 风险 W 创建 3 个 ENSEMBLE 分派与分支，各分支以退出码 0 结束
When  分支退出；05 择优分支 #2 后调用 ClaimSubmitter.SubmitOn(authority_source=POLICY:ensemble_branch（detail.ensemble_plan_id）)
Then  分支退出时 W 无声明、current_execution_id 为空；择优后其余两个分派 REVOKED、current_execution_id=#2、W → AGENT_CLAIMED_DONE；
      若 3 枝全部失败，05 调 EnsembleFailed 后 W → EXECUTION_FAILED(kind=RUNTIME)，不自动重试

AC-02-59  [integration]  关联 §6.9 DispatchAssigner, BR-02-15（复审 #4）
Given W 处于 NEEDS_HUMAN_REVIEW，AUTONOMOUS 项目，Agent 评审员 Cy 持有效委托且非 W 产出者
When  05 调 DispatchAssigner.Assign(role=REVIEWER, assignee=Cy, lease_ttl_seconds=1800, authority_source=SYSTEM:fleet.review_dispatch)
Then  W 状态仍为 NEEDS_HUMAN_REVIEW，无 work_item.state_changed；生成 REVIEWER 分派（租约 30 分钟）与 ASSIGN Decision；work_item.assigned{role:REVIEWER}
And   若受派人为 W 的执行者 → 403 120403（SELF_REVIEW），无分派

AC-02-60  [integration]  关联 T07, BR-02-44（复审 #7）
Given W 处于 EXECUTION_FAILED，其 DELIVERY 执行 E1 为 INTERRUPTED（机器离线）
When  05 经 DispatchAssigner.Reassign 改派给 Bo
Then  同事务：E1 未被 CancelExecution，而由 03 Launch(RETRY_INFRA, parent=E1) 置 FAILED(SUPERSEDED_BY_RETRY)；新执行 E2 的 parent=E1；W → CLAIMED

AC-02-61  [integration]  关联 T25（复审 #8）
Given W 的执行 E 为 RUNNING
When  交付负责人裁决 PAUSE（理由必填）
Then  同事务调用 03 ExecutionLauncher.Pause(W)，E → PAUSED；W 状态不变、paused=true；RESUME 后同事务调用 Resume，E → RUNNING

AC-02-62  [integration]  关联 §4.1（复审 #10）
Given 项目阶段为 ACCEPTANCE_PENDING
When  01 以 NEW_REQUIREMENT 转换返工来件，调用 RequirementCreator.CreateFromIntake
Then  需求以 DRAFT 创建成功（不再返回 120420）

AC-02-63  [integration]  关联 RequirementCreator.AppendFromIntake, BR-02-32（复审 #11）
Given 01 已以 purpose intake_convert_privileged 完成 step-up
When  01 调用 AppendFromIntake（输入不含重新认证证明）
Then  本模块不再要求 step-up，按 BR-02-32 ②–⑧ 完成 SCOPE 变更；直接调用 R10 路由仍须 purpose scope_change 的 step-up

AC-02-64  [integration]  关联 ClaimSubmitter, §3.10（复审 #14）
Given 03 E06 代 Agent 调用 SubmitOn(head_commit="9f3c2a1…")
Then  delivery_claims.head_commit 落库，claim.submitted.head_commit 与之相同；人类经 C1 提交且未带 head_commit 时为 NULL

AC-02-65  [integration]  关联 WorkItemReader（复审 #16）
Given 项目 P 有 3 个 NEEDS_HUMAN_REVIEW 工作项
When  08 调 ListByState(P, [NEEDS_HUMAN_REVIEW])
Then  返回 3 项且各带 current_claim_id；CountTerminal 返回期间内 ACCEPTED / CANCELLED 数及具备审计记录的数量

AC-02-66  [integration]  关联 §7.2（复审 #20）
Given W 第二次进入 NEEDS_HUMAN_REVIEW（返工后新声明 K2）
Then  work_item.state_changed.claim_id=K2，与首次（K1）不同

AC-02-67  [integration]  关联 WorkItemEscalator（复审 #27）
Given Agent 评审员对 W 的评审分派 30 分钟未裁决
When  05 调用 WorkItemEscalator.Escalate(kind=REVIEW_SLA, reviewer_kind=AGENT)
Then  本模块发布 work_item.escalated{trigger:REVIEW_SLA, reviewer_kind:AGENT}；AUTONOMOUS 项目置暂停 ESCALATION_RULE；重复调用在解除前幂等（无第二条事件）

AC-02-68  [integration]  关联 T03（复审 #26）
Given MANUAL 之外的 AUTO 派单提案 P-8842 由 05 自动应用
Then  ASSIGN Decision.authority_source=POLICY:dispatch_auto，detail.dispatch_proposal_id=P-8842
```

---

### 10.11 D112 / D113 验收标准

```
AC-02-69  [integration]  关联 BR-02-47, US-02-17
Given 工作项 W 缺少验收标准 C2 的验证方式，Agent A 为候选执行者
When  主管 Agent 经 DispatchAssigner.Assign 把 W 派给 A
Then  409 120461，data.readiness_issues 含 AC_WITHOUT_METHOD(C2)；W 保持 DRAFT；无分派、无执行

AC-02-70  [integration]  关联 BR-02-47 T29
Given 同上的 DRAFT 工作项 W
When  工程师阿澈带 acknowledge_readiness_issues=true 自认领
Then  W → CLAIMED；readiness_override_by=阿澈；审计 delivery.readiness_overridden；详情页持续显示就绪告警

AC-02-71  [integration]  关联 BR-02-49
When  创建工作项时 accountable_principal_id 为空或为 Agent 的主体
Then  422 120467；全自治项目创建时默认填入委托授予人

AC-02-72  [integration]  关联 BR-02-50, US-02-17
Given 需求同时要求「直接打开即可运行」与「通过模块引入 ./src/game.mjs」
When  需求基线化触发需求质量检查
Then  相关工作项 readiness_issues 含 REQUIREMENT_CONFLICT 并保持 DRAFT；交付负责人收到 N12；甲方门户无任何通知

AC-02-73  [integration]  关联 BR-02-51 T30-T31, US-02-18
Given risk=MEDIUM 的工作项 W 由主管 Agent L 派给执行者 Ada（模型家族 Claude）
When  L 或 Ada 或同为 Claude 家族的评审 Agent 对计划 APPROVE
Then  均 403 120403 reason_code=SEPARATION_OF_DUTIES；由 Codex 家族评审 Agent Cy2 批准则成功，W → CLAIMED，03 收到冻结验收集请求

AC-02-74  [integration]  关联 BR-02-51
Given plan_required=true 且无 APPROVED 计划
When  03 尝试开始实现执行
Then  409 120462；W 不变

AC-02-75  [integration]  关联 BR-02-53/54 T34, US-02-19
Given W 在 INTERNAL_QA，产品验收 Agent 给出 FAILED（「棋盘行高不一致」）
When  03 回调 InternalQaCompleted(FAILED)
Then  W → REWORK，internal_round_count=1，系统裁决 authority_source=SYSTEM:internal_qa 携带结构化反馈并以 CONTINUE 启动 QUALITY 分支；08 收件箱、通知发件箱、频道人类可见消息中均无新增记录（事件 visibility=INTERNAL）

AC-02-76  [integration]  关联 BR-02-55 T35
Given max_internal_rounds=5，W 已内部失败 4 次
When  第 5 次内部验收 FAILED
Then  W → NEEDS_HUMAN_REVIEW，escalation_reason=INTERNAL_ROUNDS_EXHAUSTED，require_human_decision=true；负责人收到 N11（附 5 轮结论）；前 4 次失败期间无任何人类通知

AC-02-77  [integration]  关联 T33
Given W 在 INTERNAL_QA
When  AGENT_TEST 与 AGENT_ACCEPTANCE 均 PASSED
Then  W → NEEDS_HUMAN_REVIEW；此时才发 N2；L2 显示「内部验收通过（第 n 轮）」

AC-02-78  [integration]  关联 BR-02-52
Given 验收集已冻结
When  执行者的提交改动了冻结测试文件，03 回调 CHEATING_SUSPECTED
Then  按 T34 返工并累加轮次；同一工作项第 2 次作弊嫌疑直接 T35 升级，N11 注明作弊嫌疑

AC-02-79  [integration]  关联 BR-02-56 T36-T37, US-02-20
Given Agent Bo 对 W 拉 Andon（理由 12 字，附证据）
Then  W → STOPPED，活跃执行被暂停，后继加 ANDON_UPSTREAM；负责人收到 N13
When  Bo 调用 AN3 清除
Then  403 120403 reason_code=HUMAN_ONLY
When  负责人 Morgan 清除并填写处置说明
Then  W 回到 stopped_from_state，执行恢复，后继阻塞移除

AC-02-80  [integration]  关联 BR-02-57 T38-T39, US-02-21
Given W appetite=240 分钟、time_pct=20，预测总时长 320 分钟
When  delivery.tolerance_forecast 运行
Then  W → EXCEPTION，生成例外报告草稿，执行暂停，N14
When  Agent 或持委托的 Agent 调用 E3
Then  403 120403 HUMAN_ONLY / NON_DELEGABLE
When  Morgan 选择 CONTINUE 但不给新 appetite
Then  422 120468；给出 new_appetite_minutes=360 后 W 回到原状态并恢复执行

AC-02-81  [integration]  关联 T39
When  Morgan 选择 RESHAPE / HOLD / KILL
Then  分别 → DRAFT（同事务 ChangeRecord）/ BLOCKED(HOLD) / CANCELLED

AC-02-82  [integration]  关联 BR-02-58, US-02-22
Given W 的声明 K 曾在第 2 轮内部验收通过
When  Morgan 在评审台以 QUALITY_REJECTION REJECT（反馈：样式）
Then  同事务写 delivery_escapes(stage=HUMAN_REVIEW, claim=K)，发布 quality.escaped；escape_learning 把摘要写为 TESTER / ACCEPTOR 的 EXPERIENCE 记忆草稿（06 `MemoryWriter.Propose`）并提交 11 评测用例（`EvalSetWriter.ProposeCase`）；W.escape_count=1

AC-02-83  [integration]  关联 BR-02-59
When  创建 type=BUG 工作项缺少 severity 或 reproducibility
Then  422（字段校验）；defect_stage 只能按 MERGED → DEPLOYED → VERIFIED → CLOSED 推进，CLOSED 与 ACCEPTED 同时发生

AC-02-84  [integration]  关联 BR-02-53
Given 团队模式 HUMAN_ONLY 或执行者为人类且模板未要求内部验收
When  T10 发生
Then  直接 → NEEDS_HUMAN_REVIEW，L2 标注「未经内部验收」

AC-02-85  [integration]  关联 ChangeImpactPreviewer, BR-02-63（D117）
Given 需求 R 当前 v2，标准 c3 关联 W7（IN_PROGRESS）与 W8（ACCEPTED），W9（CANCELLED）也曾关联 c3
When  01 调用 Preview(R, 2, [ADD "导出 CSV", RETIRE c3])
Then  返回 CurrentVersionNo=2、AffectedWorkItems = [W7 STALE, W8 STALE, {work_item_id:null, NEW}]，不含 W9；数据库无任何写入（行数与 version 不变）
When  以 baseVersionNo=1 调用
Then  不报错，CurrentVersionNo=2
When  RETIRE 的 criterion_id 属于其他需求 / 需求为 DRAFT
Then  依次 422 120430 / 409 120439

AC-02-86  [integration]  关联 RequirementCreator.AppendFromIntake, BR-02-63
Given R 当前 v2，01 已完成 step-up
When  01 调用 AppendFromIntake(Change{CriteriaDiff=[ADD a, REPLACE c2→"…"], Impacts=[W7 CANCEL], SourceIntakeID=IN-9, ApprovedAdjustment=nil})
Then  同事务写 v3 快照、ChangeRecord(SCOPE, source_intake_id=IN-9)、Decision(SCOPE_CHANGE)；c2 被替换，关联工作项 baseline_stale；W7 按 T23 取消；发布 requirement.versioned；未调用 07 ToleranceAdjuster
When  同时给 Diff 与 Change，或都不给
Then  422 120400，无写入

AC-02-87  [integration]  关联 BR-02-63, 07 BR-07-62
Given R 当前 v2，R 的需求级容差 baseline_no=1
When  01 调用 AppendFromIntake(Change{…, ApprovedAdjustment{ScheduleDeltaDays:4, CostDeltaMicros:0, ScopeDeltaCriteria:1}})
Then  同事务：v3、ChangeRecord，以及 07 ApplyApprovedChange 使 baseline_no=2；外层事务回滚时三者都不存在
When  R 的容差有未决例外（07 返回 127055）
Then  整体回滚：R 仍为 v2、无 ChangeRecord、无 requirement.versioned 事件，错误码 127055 原样返回

AC-02-88  [integration]  关联 BR-02-63, BR-02-32 ②
Given 01 以 BaseVersionNo=2 调用，但 R 已被 R10 升到 v3
When  AppendFromIntake(Change{…})
Then  409 120440，无写入，未调用 07
```

## 11. 度量与埋点（不得包含代码、需求正文、反馈正文、理由正文）

| 事件 | 属性 |
|---|---|
| `work_item_transition` | `from, to, trigger, actor_kind, reason_code, project_mode` |
| `claim_submitted` | `actor_kind, evidence_count, coverage_required, coverage_covered, sequence, similarity_bucket(<8%/8–30%/>30%/null)` |
| `decision_made` | `action, authority_kind(ROLE/DELEGATION/POLICY/SYSTEM), actor_kind, attention_ms, failure_category?, feedback_count, feedback_categories[], risk, waited_ms(进入 NEEDS_HUMAN_REVIEW 至裁决)` |
| `decision_denied` | `action, reason_code, actor_kind` |
| `rework_branch_created` | `category, branch_reason, autostart, quality_rework_count` |
| `autonomy_paused` | `trigger(REWORK_LIMIT/SPINNING/INCONCLUSIVE/BUDGET/REVIEW_SLA), project_mode` |
| `lease_expired` | `role, assignee_kind, held_ms` |
| `review_claimed` | `contended(bool)` |
| `requirement_versioned` | `version_no, criteria_added, criteria_retired, stale_count, cancelled_count` |
| `decomposition_applied` | `template_key, template_version, items_created, dry_run` |
| `board_drag_rejected` | `from_column, to_column, reason_key` |

派生指标（PRD §15）：**以 07 §5.8 为唯一定义**（公式、时间窗与快照表均由 07 维护，C67）；本模块只提供原始事实：`first_pass`、`first_accepted_at`、`delivery_attention_sessions.active_ms`、迁移记录（REWORK 进入 / ACCEPTED 时间）、声明与证据计数。闭环完整率的原始核验（终态工作项具备迁移记录与审计）由 `dependency_sweep` 顺带完成。

---
| `internal_qa_round`（D113） | `work_item_type`, `round`, `stage`, `result`, `verifier_model_family` |
| `quality_escaped` | `stage`, `category`, `internal_rounds_before` |
| `andon_pulled` / `exception_raised` | `actor_kind`, `scope` / `dimension` |

派生指标（07 统一定义口径）：**穿透率**、内部一次通过率、平均内部轮次、就绪闸门拦截率、计划一次批准率。

## 12. 依赖、风险与待决

### 12.1 依赖

| 依赖 | 需要的能力 | 本规格的假设 |
|---|---|---|
| 03 execution | `ExecutionLauncher{Launch(tx, {work_item_id, assignment_id, parent_execution_id, branch_reason, workspace_mode, feedback_ref(decision_id), autostart}), Resume(tx, work_item_id), Cancel(tx, work_item_id, reason), CancelExecution(tx, execution_id, reason)}`；`EvidenceReader`：证据归属校验、必需验证汇总、`ChangeSimilarity`（并集 <5 返回 NULL）、`LiveExecution`、`WorkItemCost`、diff 摘要；`EvidenceBinder{ValidateClaimable, Attach}`；调用本模块 `WorkItemTransitioner` / `ClaimSubmitter`；`ExecutionBranchTree` 数据 | 方法签名以 03 §6.4 为准 |
| 05 agents | `AgentDirectory`：状态、角色（EXECUTOR/REVIEWER/LEAD）、负载、首次验收率；`DispatchProvenance.DispatchedBy`；调用本模块 `DispatchAssigner`（同步）与 `ClaimSubmitter`（Heavy 择优）；`agent.retired` / `agent.state_changed` | 派单理由文本来自 05 |
| 06 knowledge | 「使用的知识」按执行读取（经 03 执行记录或 06 `Retriever` 的引用记录） | 评审台只读展示 |
| 07 projects | 同模块：项目阶段、团队模式、执行故障重试上限（0–5，默认 2）、L4 投影、`ProjectGate`（预算 <100%）、`AcceptanceReader`（T26 守卫读取逐项结果）、`budget.threshold_reached`（07 发布）；指标公式 07 §5.8 | 07 在项目表提供 `infra_retry_limit` |
| 08 governance | `AuthorityChecker.Check(ctx, tx, req)`（含委托条件、不可委托约束）、`ListEligible`（通知人群）、`delegation.revoked` / `delegation.expired`、通知偏好 | 08 不在 Check 中写库；委托使用次数由 08 依据 `decision.made` 统计 |
| 09 platform | JobQueue、OutboxPublisher（同事务）、RealtimeBroadcaster（`TopicAuthorizer` 注册）、Notifier；`domain_event_receipts` 消费去重；C7 幂等辅助 | 已采用（本模块不建幂等 / 消费表） |
| 01 intake | `ClarificationRequester.RequestFromWorkItem`；调用本模块 `RequirementCreator`、`WorkItemReopener` | |
| 底座 | `authz.RegisterOwned`/`Register`/`RegisterTenantMember`、`audit.Appender`、`dlock`、`authn` step-up、`pkg/query`、`gormx.TableName`、`guid` | 已存在 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 状态机 × 覆盖层（暂停）× 汇总项组合爆炸 | 迁移表作为唯一真相、表驱动测试穷举（AC-02-04）；覆盖层规则集中在 §4.4 |
| 03 回调与本模块事务的一致性 | 回调在 03 事务内执行（委派模式）+ `source_ref` 幂等 + 每日 `dependency_sweep` 对账 |
| 评审瓶颈 | 评审认领避免重复劳动、SLA 提醒、委托覆盖低风险、关注时长量化 |
| 相似度依赖 03 产物文件集质量 | 无法计算时不触发（不误伤），埋点 `similarity_bucket=null` 比例监控 |
| 依赖环检测在大项目上的耗时 | 项目 5000 项 / 每项 50 边上限；BFS 内存实现，p95 目标 <50ms |

### 12.3 待决

| # | 议题 | 建议 | 理由 |
|---|---|---|---|
| Q1 | 人类执行者能否通过自己执行的工作项（单人团队） | 默认**不能**（硬职责分离对人也生效）；08 §12.3 Q3 允许团队所有者经 step-up 开启「单人团队」放宽（`governance_sod_relax`），开启后 BR-02-21(a)(b) 对人类不生效、审计标记 | 与 08 对齐 |
| Q2 | 汇总项是否需要裁决才能 ACCEPTED | **需要**（T24 → NEEDS_HUMAN_REVIEW → APPROVE） | 保持「ACCEPTED 必须是 Decision」无例外 |
| Q3 | 验证 FAILED 自动返工是否计入质量返工次数 | **计入** | 否则 Agent 可在自动验证失败上无限循环，违背有界自治本意 |
| Q4 | 租约与评审 SLA 的具体数值（4h / 10min / 15min / 24h / 72h） | 先用本规格默认值，W8 试点数据后再调 | PRD 未给数值 |
| Q5 | 返工阈值 3、8% 与相似度最小并集 5 是否允许项目配置 | MVP 固定；v1.1 由 08 升级规则允许**收紧**不允许放宽 | 防止全自治项目放宽护栏 |
| Q7 | 03 Q7「并集 <5 不计相似度」 | **已决：采纳**（BR-02-27） | 小改动返工不误判 |
| Q8 | 03 Q8「人工恢复中断执行 → IN_PROGRESS」 | **已决：T20b** | 恢复同一执行不应留在 EXECUTION_FAILED |
| Q6 | 新增依赖到进行中工作项时是否强制 BLOCKED | 不强制，仅警示（BR-02-07） | 避免中断已运行的执行与浪费成本 |

---

## 13. 契约变更请求

> 合并说明：以下请求均已在 00 v1.1–1.3 中处理（逐条标注状态）；本轮新增请求见 CCR-10。

| # | 类型 | 请求 | 理由 |
|---|---|---|---|
| CCR-01 | 修改 00 §3 | 时间列类型改为 PostgreSQL `TIMESTAMPTZ(3)` / MySQL `DATETIME(3)` / SQLite `DATETIME` | 00 写的 `TIMESTAMP`/`DATETIME(6)`/`TEXT` 与 backend-module 规约冲突；MySQL `TIMESTAMP` 有 2038 上限且被 `iam-schema-lock` 门禁禁止  **状态：已合并（00 v1.1）** |
| CCR-02 | 新增 00 §5 枚举（所有者 02） | `RequirementStatus`: DRAFT, BASELINED, ARCHIVED；`AssignmentStatus`: ACTIVE, RELEASED, EXPIRED, REVOKED, COMPLETED；`ClaimOutcome`: PENDING, ACCEPTED, REWORK, SUPERSEDED, CANCELLED；`BlockReason`: DEPENDENCY, DEPENDENCY_CANCELLED, ESTIMATE_MISSING, CHILDREN_OPEN, REQUIREMENT_DRAFT, CLARIFICATION, EXTERNAL, TOOL_UNAVAILABLE, PROVIDER_UNAVAILABLE（后两项为系统原因，由 11 BR-11-30 / BR-11-26 经 `WorkItemTransitioner` 写入与消除，见 BR-02-61）；`PauseReason`: MANUAL, BUDGET, AUTONOMY_REWORK_LIMIT, AUTONOMY_SPINNING, ESCALATION_RULE；`ChangeRecordKind`: SCOPE, STRATEGY；`CriterionCoverage`: COVERED, PARTIAL, NOT_APPLICABLE；`TemplateStatus`: ACTIVE, DEPRECATED；`AttentionSurface`: REVIEW_DESK, INBOX；`ExecutionFailureKind`: INFRA, RUNTIME（所有者 03） | 本规格的表与状态机需要；放入 00 防止 03/07/08 各自定义 |
| CCR-03 | 新增 00 §6.1 权限码 | `work_item_cancel`（DataScoped，delivery；默认 team_owner、delivery_lead）；`decomposition_template_manage`（团队级，delivery；默认 team_owner、delivery_lead） | DecisionAction 有 CANCEL 但目录无对应权限码；团队级模板管理无合适码（`project_manage` 是项目级）  **状态：已合并（00 v1.1）** |
| CCR-04 | 新增 00 §7.1 事件 | `work_item.escalated`（delivery → governance、collab、fleet） | 有界自治暂停 / 升级不是状态迁移，08 的「我的裁决」与通知需要独立信号  **状态：已合并（00 v1.1）** |
| CCR-05 | 补充 00 §8 接口 | `WorkItemTransitioner` 方法集（§6.9）；`ExecutionLauncher` 增加分支参数（parent、branch_reason、workspace_mode、feedback_ref、autostart）与暂停 / 恢复 / 取消；`EvidenceReader` 增加 `ChangeSimilarity`、`LiveExecution`、必需验证汇总、证据归属校验；`AuthorityChecker` 增加 `ListEligible`；新增 `RequirementCreator`（delivery 提供，01 使用）与 `ClarificationRequester`（intake 提供，delivery 使用） | 00 只有语义，本规格的守卫依赖这些具体能力；全部在调用方事务内执行（委派模式）  **状态：已合并（00 v1.2/1.3 §8；`ClaimSubmitter`、`DispatchAssigner`、`DecisionReader`、`WorkItemReopener` 由本模块提供）** |
| CCR-06 | 修改 00 §9 幂等表 | `<module>_idempotency_keys` 增加 `principal_id`、`request_hash`、`response_status`、`resource_kind`、`resource_id`；唯一键含 principal 与 operation | 只有 `response_hash` 无法「返回首次结果」，也无法区分「同 key 不同请求」  **状态：已被取代（00 v1.2：平台统一幂等表 09 C7）** |
| CCR-07 | 澄清 00 §3 乐观锁 | `version` 经 `ETag` 返回、经 `If-Match` 提交；缺失返回 428；冲突业务码 `<段>0409` 不变 | 与底座 `internal/todo` 先例一致，避免各模块分别用 body 字段  **状态：已合并（00 v1.1）** |
| CCR-08 | 修改 00 §9 tag 规则 | OpenAPI tag = 最终发布路径首段（2026-09-26 已实施规则），删除「工作流域词汇」表述 | `api-routing-inspection-2026-09-26.md` 已取代 workflow-group 规则，构建期 hook 会按首段重设 tag  **状态：已合并（00 v1.1）** |
| CCR-09 | 补充 00 §11 示例 | AC 示例中的 120403 保持为「授权拒绝」通用码，具体原因用 `data.reason_code`（MISSING_PERMISSION / SELF_REVIEW / AGENT_EXECUTOR / NON_DELEGABLE / DELEGATION_CONDITION / ESCALATED_HUMAN_ONLY / AGENT_SELF_CLAIM / HUMAN_ONLY） | 统一各模块拒绝码，前端一处翻译  **状态：已合并（00 v1.1）** |
| CCR-10 | 补充 00 §8 | 新增本模块提供的 `WorkItemReopener`（01 使用）；`WorkItemReader` 增 `Owner` / `RequirementBaseline` / `LoadByPrincipal`；`WorkItemTransitioner` 增 `EnsembleFailed`（v1.1） | 审计 C7 / C65 / C58 |

> **D117（2026-10-12，甲方变更申请）**：新增只读接口 `ChangeImpactPreviewer.Preview`、`AppendFromIntake` 的 `Change` 载荷、BR-02-63、AC-02-85…88；已登记入 00 §8（v2.0 D117 补充）。需要 07 提供 `ToleranceAdjuster.ApplyApprovedChange`（07 BR-07-62）。

> **D112 / D113（2026-10-11）**：本规格按 00 v2.0 §15 增补（§3.20–3.25、§4.5、§5.11、§6.10、§7.4、§8.6、N11–N14、§10.11、§11）。需要他规格配合：03 `purpose=PLANNING` 只读执行与计划产出格式、冻结 / 隐藏验收集、`InternalQaCompleted` 回调、`CHEATING_SUSPECTED`；05 TESTER / ACCEPTOR 与计划评审 Agent 的派单（跨模型家族）；06 `MemoryWriter`；07 穿透率口径与 `HUMAN_ONLY`；08 新权限码默认授予、`plan_review` 策略、收件箱按 `visibility=INTERNAL` 过滤、N11–N14 路由；11 `EvalSetWriter.ProposeCase` 与模板默认值；12 缺陷流转矩阵与字典。
