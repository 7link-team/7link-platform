# 10 · 目标、计划与报告（模块 `planning`）

> 决议 D111：人与 Agent 协作按**目标从上往下拆解**推进；每个成员（人类或 Agent，含主管创建的 Agent）都有目标、计划与任务；**报告从下往上汇总**（日 → 周 → 月 → 季 → 年；成员 → 项目 → 团队），提交前**上下即时核对**，有阻断问题不允许提交，非阻断问题允许提交但标记「未核对 / 未确认 / 待佐证」。
>
> 本规格遵守 `00-conventions.md`（v1.8 起）；需要 00 登记的内容见 §13。

---

## 1. 范围与目标

**解决**

1. 目标体系：TEAM → PROJECT → MEMBER 三级目标（Objective + Key Results），子目标必须挂到上级 KR，计算对齐覆盖率与进度。
2. 计划：每个成员按周期（DAY / WEEK / MONTH / QUARTER / YEAR）制定计划，计划项必须关联目标 / KR，可关联工作项（02）或为非交付任务；下周期计划由系统 / Agent 起草、成员提交、上级确认。
3. 报告：日报、周报、月报、季报、年报，分成员 / 项目 / 团队三层；四个必填章节（完成、未完成、复盘、下周期计划）；定时生成草稿并由系统记录自动填充；时间维度与层级维度双向汇总。
4. 核对（Reconciliation）：提交前自动核对报告内容与系统事实、下级报告、目标对齐；阻断问题禁止提交，非阻断问题打标并向上传播。
5. 确认：上级（或 Agent 的负责人类）确认 / 退回；逾期提醒。
6. 知识回流：已确认报告中的改进项可提议为 06 记忆（EXPERIENCE）。

**不解决（边界）**

| 主题 | 归属 |
|---|---|
| 工作项、裁决、完成阶梯 L1–L3 的事实 | 02（本模块只读） |
| 交付与甲方验收（L4）、交付指标计算 | 07（本模块经 `DeliveryMetricsReader` 读取） |
| Agent 档案、负责人类、主管关系 | 05（`AgentDirectory`） |
| 权限判定、收件箱、通知路由 | 08 |
| 任务队列、outbox、SSE、通知渠道 | 09 |
| 记忆写入与治理 | 06（`MemoryWriter`） |
| 绩效考核与薪酬 | 非目标（报告不产生考评分数） |

---

## 2. 用户故事

| ID | 故事 | 版本 |
|---|---|---|
| US-10-01 | 作为团队所有者，我要设定团队年度 / 季度目标与关键结果，以便全队对齐方向 | MVP |
| US-10-02 | 作为交付负责人，我要把团队 KR 拆成项目目标，并看到每个团队 KR 被覆盖的程度 | MVP |
| US-10-03 | 作为成员（人或 Agent），我要基于项目 KR 设定自己的目标，以便知道自己为什么做这些工作 | MVP |
| US-10-04 | 作为成员，我要每周 / 每日有计划，计划项关联目标或 KR，可链接工作项 | MVP |
| US-10-05 | 作为上级，我要确认下属（含 Agent）的计划是否与目标对齐，以便及时纠偏 | MVP |
| US-10-06 | 作为成员，我要在周期结束时拿到自动填好的报告草稿（完成、未完成、复盘、下周期计划），只需补充与确认 | MVP |
| US-10-07 | 作为成员，我要在提交前看到核对结果，知道哪里与系统事实或下级报告不一致 | MVP |
| US-10-08 | 作为上级，我要报告里每个「已完成」都有系统证据，不能伪装完成 | MVP |
| US-10-09 | 作为交付负责人，我要项目周报由成员周报汇总而来，并看到「未核对 n/m」等标记 | MVP |
| US-10-10 | 作为团队所有者，我要月报 / 季报 / 年报由下层报告逐级汇总，并能下钻 | MVP |
| US-10-11 | 作为 Agent 的负责人类，我要确认 Agent 的报告，Agent 不能自己确认 | MVP |
| US-10-12 | 作为成员，我要收到逾期提醒；作为上级，我要看到谁逾期 | MVP |
| US-10-13 | 作为交付负责人，我要把复盘里的改进项沉淀为项目经验记忆 | v1.1 |
| US-10-14 | 作为团队所有者，我要在团队报告中心按成员 × 周期看报告状态，并按人类 / Agent / AI 创建 筛选 | MVP |
| US-10-15 | 作为成员，我要 KR 能自动从系统指标取值（如已接受工作项数），减少手填 | MVP |
| US-10-16 | 作为团队所有者，我要配置时区、日报截止时间、宽限期与哪些周期必须报告 | MVP |

---

### 2.x D112 / D113 增补用户故事（v2.0，00 §15）

| ID | 用户故事 | 版本 |
|---|---|---|
| US-10-17 | 作为上级，我要在报告里同时看到系统计算的进度和作者声明的状态（绿 / 黄 / 红），声明为绿但实际明显落后的报告不能直接提交，以便杜绝「外绿内红」 | MVP |
| US-10-18 | 作为作者，当我的状态信心比上一期下降时，我要写出改进计划，以便问题有人跟进 | MVP |
| US-10-19 | 作为团队所有者，项目与团队报告自动带上穿透率、内部一次通过率与平均内部轮次，以便在复盘中讨论内部质量门 | MVP |

## 3. 领域模型与数据表

### 3.0 类型映射（三方言）

| 逻辑类型 | PostgreSQL | MySQL | SQLite |
|---|---|---|---|
| ID | `BIGINT` | `BIGINT` | `INTEGER` |
| STR(n) | `VARCHAR(n)` | `VARCHAR(n)` | `TEXT` |
| TEXT | `TEXT` | `MEDIUMTEXT` | `TEXT` |
| JSON | `JSONB` | `JSON` | `TEXT` |
| TIME | `TIMESTAMPTZ(3)` | `DATETIME(3)` | `DATETIME` |
| DATE | `DATE` | `DATE` | `TEXT`（`YYYY-MM-DD`） |
| DEC | `NUMERIC(18,4)` | `DECIMAL(18,4)` | `REAL` |
| BOOL | `BOOLEAN` | `TINYINT(1)` | `INTEGER` |
| ENUM | `VARCHAR(32)` + CHECK | `VARCHAR(32)` + CHECK | `TEXT` + CHECK |

所有表：`id` 雪花 ID 主键；`tenant_id` 必填；审计列 `created_at/updated_at/created_by/updated_by`；可变聚合根带 `version`（ETag / If-Match）。MySQL 不支持部分唯一索引处，用生成列 `active_key`（满足条件时取业务键，否则 NULL）+ 普通唯一索引。

### 3.1 枚举（本模块；共享者见 §13 CR）

| 枚举 | 值 | 共享 |
|---|---|---|
| `GoalLevel` | `TEAM`, `PROJECT`, `MEMBER` | 是 |
| `GoalStatus` | `DRAFT`, `ACTIVE`, `AT_RISK`, `ACHIEVED`, `MISSED`, `CANCELLED` | 是 |
| `PeriodKind` | `DAY`, `WEEK`, `MONTH`, `QUARTER`, `YEAR` | 是 |
| `ReportLevel` | `MEMBER`, `PROJECT`, `TEAM` | 是 |
| `ReportState` | `DRAFT`, `CHECKING`, `BLOCKED`, `READY`, `SUBMITTED`, `CONFIRMED`, `RETURNED` | 是 |
| `CheckSeverity` | `BLOCKING`, `NON_BLOCKING` | 是 |
| `KRSource` | `AUTO`, `MANUAL` | 是 |
| `PlanState` | `DRAFT`, `SUBMITTED`, `CONFIRMED`, `RETURNED` | 模块内 |
| `PlanItemKind` | `WORK_ITEM`, `FREE_TASK` | 模块内 |
| `FreeTaskCategory` | `LEARNING`, `OPS`, `REVIEW`, `SUPPORT`, `OTHER` | 模块内 |
| `KRMetric` | `ACCEPTED_WORK_ITEMS`, `FIRST_PASS_RATE`, `COST_PER_ACCEPTED`, `EVIDENCE_COVERAGE`, `REWORK_CYCLE_P50`, `ON_TIME_REPORT_RATE`, `CLIENT_FIRST_ACCEPTANCE` | 模块内 |
| `KRDirection` | `INCREASE`, `DECREASE` | 模块内 |
| `UncompletedReason` | `BLOCKED_DEPENDENCY`, `SCOPE_CHANGED`, `UNDERESTIMATED`, `REPRIORITIZED`, `WAITING_CLIENT`, `QUALITY_REWORK`, `RESOURCE`, `OTHER` | 模块内 |
| `CarryOver` | `CARRY_TO_NEXT`, `DROP`, `ESCALATE` | 模块内 |
| `CheckCode` | `B1_UNBACKED_COMPLETION`, `B2_AGGREGATE_MISMATCH`, `B3_UNLINKED_PLAN_ITEM`, `B4_MISSING_SECTION`, `B5_FOREIGN_KR`, `N1_CHILD_UNSUBMITTED`, `N2_PLAN_UNCONFIRMED`, `N3_KR_NO_EVIDENCE`, `N4_STALE_CHECK` | 模块内 |
| `ReportFlag` | `UNCHECKED`（未核对）, `UNCONFIRMED`（未确认）, `NEEDS_EVIDENCE`（待佐证）, `OVERDUE`（逾期）, `NEEDS_RECHECK`（需重新核对） | 模块内 |
| `SubjectKind` | `HUMAN`, `AGENT`（报告 / 计划所属成员类型） | 沿用 00 §4 actor_kind 子集 |

### 3.2 `planning_settings`（团队设置，每租户 1 行）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id | ID | N | | 唯一 |
| timezone | STR(64) | N | `Asia/Shanghai` | IANA 时区；所有周期边界按此计算 |
| day_report_at | STR(5) | N | `18:00` | 日报草稿生成时刻（本地） |
| week_report_at | STR(16) | N | `MON 09:00` | 周报草稿生成（上周 Mon–Sun） |
| month_report_at | STR(16) | N | `D1 09:00` | 次月 1 日 |
| quarter_report_at | STR(16) | N | `D1 09:00` | 次季首日 |
| year_report_at | STR(16) | N | `D2 09:00` | 次年 1 月 2 日 |
| grace_day_minutes | INT | N | 960 | 日报宽限：至次日 10:00（18:00+16h） |
| grace_week_minutes | INT | N | 1620 | 周报宽限：至周二 12:00 |
| grace_month_minutes | INT | N | 4320 | 3 天 |
| grace_quarter_minutes | INT | N | 7200 | 5 天 |
| grace_year_minutes | INT | N | 14400 | 10 天 |
| required_member_kinds | JSON | N | `["WEEK","MONTH","QUARTER","YEAR"]` | 成员必须提交的报告周期；DAY 默认可选 |
| agent_day_report | BOOL | N | true | Agent 成员是否必须日报（Agent 默认日报） |
| workdays | JSON | N | `[1,2,3,4,5]` | ISO 周几；非工作日不生成日报 |
| at_risk_threshold | DEC | N | 0.15 | 进度落后于时间进度超过该值 → AT_RISK |
| version | INT | N | 1 | |

### 3.3 `planning_goals`

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id | ID | N | | |
| level | ENUM GoalLevel | N | | |
| project_id | ID | Y | NULL | PROJECT / MEMBER 级必填（MEMBER 级可为团队级成员目标时 NULL，见 BR-10-04） |
| owner_kind | ENUM SubjectKind | Y | NULL | MEMBER 级必填 |
| owner_principal_id | ID | Y | NULL | MEMBER 级必填；TEAM / PROJECT 级为负责人（人类） |
| parent_goal_id | ID | Y | NULL | TEAM 级必须 NULL，其余必填 |
| parent_kr_id | ID | Y | NULL | TEAM 级 NULL，其余必填（必须属于 parent_goal） |
| period_kind | ENUM PeriodKind | N | | 仅 YEAR / QUARTER / MONTH |
| period_start | DATE | N | | 周期首日（本地） |
| title | STR(200) | N | | Objective |
| description | TEXT | Y | NULL | ≤8000 |
| weight | DEC | N | 1 | 在父 KR 下的权重，(0, 10] |
| status | ENUM GoalStatus | N | `DRAFT` | |
| progress | DEC | N | 0 | 0–1，由 KR 计算（BR-10-08） |
| alignment_coverage | DEC | Y | NULL | 本目标 KR 被子目标覆盖的比例（BR-10-07） |
| at_risk_reason | STR(500) | Y | NULL | |
| closed_at | TIME | Y | NULL | ACHIEVED / MISSED / CANCELLED 时间 |
| version | INT | N | 1 | |

索引：`(tenant_id, level, period_kind, period_start)`、`(tenant_id, parent_goal_id)`、`(tenant_id, owner_principal_id, period_start)`、`(tenant_id, project_id, period_start)`。CHECK：`level='TEAM' ⇒ parent_goal_id IS NULL AND project_id IS NULL`；`level<>'TEAM' ⇒ parent_goal_id IS NOT NULL AND parent_kr_id IS NOT NULL`；`level='MEMBER' ⇒ owner_principal_id IS NOT NULL`。

### 3.4 `planning_key_results`

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id | ID | N | | |
| goal_id | ID | N | | |
| seq | INT | N | | 1..10，目标内排序 |
| title | STR(200) | N | | |
| source | ENUM KRSource | N | | |
| metric | ENUM KRMetric | Y | NULL | AUTO 必填 |
| metric_scope | JSON | Y | NULL | AUTO：`{project_ids?[], principal_id?, work_item_types?[]}`（不得超出目标的项目 / 成员范围） |
| direction | ENUM KRDirection | N | `INCREASE` | |
| baseline | DEC | N | 0 | |
| target | DEC | N | | `≠ baseline` |
| current | DEC | N | 0 | AUTO 由任务刷新；MANUAL 由负责人填写 |
| current_evidence | JSON | Y | NULL | MANUAL：`[{kind:"URL"\|"FILE"\|"WORK_ITEM"\|"DELIVERY", ref}]` |
| current_updated_at | TIME | Y | NULL | |
| weight | DEC | N | 1 | 目标内权重 (0, 10] |
| progress | DEC | N | 0 | BR-10-08 |
| version | INT | N | 1 | |

唯一：`(goal_id, seq)`。一个目标 1–5 个 KR（BR-10-02）。

### 3.5 `planning_goal_contributors`

成员对上级 KR 的贡献登记（用于 B5 判定「是否为该 KR 的贡献者」）。

| 列 | 类型 | 可空 | 说明 |
|---|---|---|---|
| id | ID | N | |
| tenant_id | ID | N | |
| kr_id | ID | N | |
| principal_id | ID | N | |
| source | STR(16) | N | `OWNER`（KR 所在目标负责人）/ `CHILD_GOAL`（成员有挂在该 KR 下的子目标）/ `ASSIGNED`（上级显式指派） |
| created_at | TIME | N | |

唯一：`(kr_id, principal_id)`。由 BR-10-06 维护（目标创建 / 删除时同步），可手动 `ASSIGNED`。

### 3.6 `planning_plans`

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id | ID | N | | |
| subject_kind | ENUM SubjectKind | N | | |
| principal_id | ID | N | | 计划所属成员 |
| period_kind | ENUM PeriodKind | N | | |
| period_start | DATE | N | | |
| state | ENUM PlanState | N | `DRAFT` | |
| drafted_by_kind | STR(16) | N | | `SYSTEM` / `AGENT` / `HUMAN` |
| submitted_at | TIME | Y | NULL | |
| confirmer_principal_id | ID | Y | NULL | 计算得出的确认人（BR-10-20） |
| confirmed_by | ID | Y | NULL | |
| confirmed_at | TIME | Y | NULL | |
| return_comment | STR(2000) | Y | NULL | |
| version | INT | N | 1 | |

唯一：`(tenant_id, principal_id, period_kind, period_start)`。

### 3.7 `planning_plan_items`

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id | ID | N | | |
| plan_id | ID | N | | |
| seq | INT | N | | |
| kind | ENUM PlanItemKind | N | | |
| goal_id | ID | N | | 必填（B3） |
| kr_id | ID | Y | NULL | 建议填；MEMBER 目标的 KR |
| work_item_id | ID | Y | NULL | kind=WORK_ITEM 必填 |
| free_category | ENUM FreeTaskCategory | Y | NULL | kind=FREE_TASK 必填 |
| title | STR(200) | N | | WORK_ITEM 时默认取工作项标题 |
| estimate_hours | DEC | Y | NULL | 人类成员可填；Agent 成员可填预算 `estimate_cost_micros` |
| estimate_cost_micros | BIGINT | Y | NULL | |
| carried_from_item_id | ID | Y | NULL | 由上周期未完成项结转 |
| outcome | STR(16) | Y | NULL | 周期结束后：`DONE` / `NOT_DONE` / `DROPPED`（由报告回写） |

### 3.8 `planning_reports`

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id | ID | N | | |
| level | ENUM ReportLevel | N | | |
| period_kind | ENUM PeriodKind | N | | |
| period_start | DATE | N | | |
| period_end | DATE | N | | 含 |
| subject_kind | ENUM SubjectKind | Y | NULL | MEMBER 级必填 |
| principal_id | ID | Y | NULL | MEMBER 级必填 |
| project_id | ID | Y | NULL | PROJECT 级必填 |
| state | ENUM ReportState | N | `DRAFT` | |
| flags | JSON | N | `[]` | `ReportFlag[]` |
| child_total | INT | N | 0 | 时间 / 层级下级报告应有数 |
| child_submitted | INT | N | 0 | 其中已提交（SUBMITTED / CONFIRMED） |
| child_confirmed | INT | N | 0 | |
| content | JSON | N | | 四章节，Schema 见 §3.12 |
| content_hash | STR(64) | N | | sha256(规范化 content)；核对针对该哈希 |
| draft_source | STR(16) | N | `SYSTEM` | `SYSTEM` / `AGENT`（Agent 成员自撰） |
| retrospective_ai_drafted | BOOL | N | false | 人类成员的复盘为 AI 草稿且尚未编辑时为 true（提交前必须编辑或明确接受，BR-10-29） |
| last_check_id | ID | Y | NULL | |
| last_check_hash | STR(64) | Y | NULL | 最近一次核对时的 content_hash |
| due_at | TIME | N | | 宽限截止 |
| submitted_at / submitted_by | TIME / ID | Y | NULL | |
| confirmer_principal_id | ID | Y | NULL | BR-10-20 |
| confirmed_at / confirmed_by | TIME / ID | Y | NULL | |
| return_comment | STR(2000) | Y | NULL | |
| version | INT | N | 1 | |

唯一（生成列 `report_key` = `level|period_kind|period_start|coalesce(principal_id,project_id,0)`）：`(tenant_id, report_key)`。索引：`(tenant_id, state, due_at)`、`(tenant_id, level, period_kind, period_start)`、`(tenant_id, principal_id, period_start)`。

### 3.9 `planning_report_links`（汇总关系）

| 列 | 类型 | 说明 |
|---|---|---|
| id | ID | |
| tenant_id | ID | |
| parent_report_id | ID | |
| child_report_id | ID | |
| axis | STR(8) | `TIME`（周 ← 日…）/ `HIER`（项目 ← 成员，团队 ← 项目） |

唯一：`(parent_report_id, child_report_id)`。

### 3.10 `planning_checks`（核对记录）

| 列 | 类型 | 可空 | 说明 |
|---|---|---|---|
| id | ID | N | |
| tenant_id | ID | N | |
| report_id | ID | Y | 报告核对 |
| plan_id | ID | Y | 计划核对（只跑 B3 / B5 / N2） |
| content_hash | STR(64) | N | 被核对内容哈希 |
| trigger | STR(16) | N | `ON_DEMAND` / `ON_SUBMIT` / `DATA_CHANGED` / `CHILD_CHANGED` |
| blocking_count | INT | N | |
| non_blocking_count | INT | N | |
| result | STR(16) | N | `PASS` / `BLOCKED` / `PASS_WITH_FLAGS` |
| started_at / finished_at | TIME | N | |
| duration_ms | BIGINT | N | |

### 3.11 `planning_check_issues`

| 列 | 类型 | 可空 | 说明 |
|---|---|---|---|
| id | ID | N | |
| tenant_id | ID | N | |
| check_id | ID | N | |
| code | ENUM CheckCode | N | |
| severity | ENUM CheckSeverity | N | |
| section | STR(16) | N | `COMPLETED` / `UNCOMPLETED` / `RETRO` / `NEXT_PLAN` / `SUMMARY` |
| item_key | STR(64) | Y | content 内条目 key（跳转锚点） |
| expected | STR(500) | Y | 例：「子报告合计 7」 |
| actual | STR(500) | Y | 例：「本报告 9」 |
| message_key | STR(128) | N | i18n key |
| fact_ref | JSON | Y | `{kind:"WORK_ITEM"\|"DECISION"\|"DELIVERY"\|"REPORT", id, state?}` |

### 3.12 报告 `content` JSON Schema（摘要）

```json
{
  "type": "object",
  "required": ["completed", "uncompleted", "retrospective", "next_plan", "summary"],
  "properties": {
    "summary": { "type": "object", "properties": {
      "completed_count": {"type":"integer"}, "uncompleted_count": {"type":"integer"},
      "accepted_work_items": {"type":"integer"}, "cost_micros": {"type":"integer"},
      "kr_progress": {"type":"array","items":{"type":"object","properties":{"kr_id":{},"from":{"type":"number"},"to":{"type":"number"}}}}
    }},
    "completed": { "type": "array", "maxItems": 500, "items": { "type": "object",
      "required": ["key","title","fact_ref"],
      "properties": { "key":{"type":"string"}, "title":{"type":"string","maxLength":200},
        "goal_id":{}, "kr_id":{}, "plan_item_id":{},
        "fact_ref": {"type":"object","required":["kind","id"],"properties":{"kind":{"enum":["WORK_ITEM","DECISION","DELIVERY","ACCEPTANCE","FREE_TASK_EVIDENCE","CHILD_REPORT"]},"id":{}}},
        "note":{"type":"string","maxLength":2000} } } },
    "uncompleted": { "type": "array", "items": { "type": "object",
      "required": ["key","title","reason","detail","carry_over"],
      "properties": { "key":{}, "title":{}, "plan_item_id":{}, "work_item_id":{},
        "reason":{"enum":["BLOCKED_DEPENDENCY","SCOPE_CHANGED","UNDERESTIMATED","REPRIORITIZED","WAITING_CLIENT","QUALITY_REWORK","RESOURCE","OTHER"]},
        "detail":{"type":"string","minLength":4,"maxLength":2000},
        "carry_over":{"enum":["CARRY_TO_NEXT","DROP","ESCALATE"]} } } },
    "retrospective": { "type": "object", "required": ["went_well","problems","root_causes","improvements"],
      "properties": { "went_well":{"type":"array","items":{"type":"string","maxLength":500}},
        "problems":{"type":"array","items":{"type":"string","maxLength":500}},
        "root_causes":{"type":"array","items":{"type":"string","maxLength":500}},
        "improvements":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string","maxLength":500},"propose_memory":{"type":"boolean"}}}} } },
    "next_plan": { "type": "array", "items": { "type": "object", "required": ["key","title","goal_id"],
      "properties": { "key":{}, "title":{}, "goal_id":{}, "kr_id":{}, "work_item_id":{}, "free_category":{} } } }
  }
}
```

层级 / 时间汇总报告的 `completed` 条目 `fact_ref.kind` 可为 `CHILD_REPORT`（引用下级报告条目，`id` 为 `report_id#key`）。

### 3.13 外部引用（只存 ID）

`principal_id`（底座 Principal，人或 Agent 服务账号）、`project_id`（Entity）、`work_item_id` / `decision_id`（02）、`delivery_id` / `acceptance_id`（07）、`agent_id`（05，经 principal 解析）。

---

### 3.14 D112 / D113 增补字段（防西瓜与质量指标）

`planning_reports` 增列：

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| declared_confidence | ENUM `StatusConfidence` | Y | NULL | 作者声明的状态信心 GREEN / YELLOW / RED；提交时必填（B4 增补） |
| computed_progress | DEC(5,4) | Y | NULL | 系统计算进度 0–1（BR-10-48），核对时快照 |
| expected_progress | DEC(5,4) | Y | NULL | 期望进度 0–1（BR-10-48） |
| progress_lag | DEC(5,4) | Y | NULL | `expected_progress − computed_progress`（负数表示超前） |
| status_explanation | STR(2000) | Y | NULL | 声明 GREEN 但落后超阈值时的说明（≥ 20 字，B6） |
| improvement_plan | JSON | Y | NULL | `[{action, owner_principal_id, due_date, kr_id?}]`，信心下降时必填（B7） |
| quality_metrics | JSON | Y | NULL | PROJECT / TEAM 级：`{escape_rate, internal_first_pass_rate, avg_internal_rounds, l3_entered, escapes}`；Agent 执行者的 MEMBER 级：`{internal_first_pass_rate, avg_internal_rounds}` |

`planning_settings` 增列：`watermelon_lag_threshold DEC NOT NULL DEFAULT 0.30`（0.10–0.60）、`confidence_required_kinds JSON NOT NULL DEFAULT ["WEEK","MONTH","QUARTER","YEAR"]`（日报可不声明信心）。

报告 `content` Schema 增加 `status{declared_confidence, explanation?, computed_progress, expected_progress}`（computed / expected 由系统写入，客户端提交的值被忽略）与 `improvement_plan[]`；`ReportFlag` 增加 `STATUS_EXPLAINED`（「状态已说明」）与 `ABOVE_CHILDREN`（「状态高于下级」）。

## 4. 状态机

### 4.1 目标 `GoalStatus`

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| G1 | DRAFT | 激活 `PUT /goals/{id}/activation` | ≥1 KR；非 TEAM 级父目标 ACTIVE / AT_RISK；权限 | ACTIVE | `goal.status_changed`；维护贡献者（BR-10-06） |
| G2 | ACTIVE | 进度刷新（J4） | 时间进度 − 进度 > `at_risk_threshold` | AT_RISK | 通知负责人与上级；`goal.status_changed` |
| G3 | AT_RISK | 进度刷新 | 差值 ≤ 阈值 | ACTIVE | 同上 |
| G4 | ACTIVE / AT_RISK | 关闭 `PUT /goals/{id}/closure {result}` | 周期已结束或进度 = 1；人类有权限 | ACHIEVED / MISSED | `closed_at`；子目标若仍开放 → 通知其负责人 |
| G5 | DRAFT / ACTIVE / AT_RISK | 取消 | 人类有权限；理由必填 | CANCELLED | 子目标标记「父目标已取消」（不级联取消）；计划项保留但核对 N 级提示 |
| G6 | 周期结束 + 7 天 | J5 | 仍 ACTIVE / AT_RISK | 保持，发「待关闭」通知 | 不自动关闭（人类裁决） |

非法：已关闭（ACHIEVED / MISSED / CANCELLED）再改 → 409 **180410**；DRAFT 直接 ACHIEVED → 409 **180411**；Agent 关闭或取消目标 → 403 **180403**（`AGENT_PROHIBITED`）。

### 4.2 计划 `PlanState`

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| P1 | — | 期初生成（J2）或成员新建 | 唯一键不冲突 | DRAFT | 自动带入：上期 `CARRY_TO_NEXT` 项、负责的 READY / IN_PROGRESS 工作项、未完成 KR 建议 |
| P2 | DRAFT | 提交 `POST /plans/{id}/submissions` | 计划核对无 B3 / B5；≥1 项 | SUBMITTED | `plan.submitted`；收件箱 PLAN_CONFIRMATION 给确认人 |
| P3 | SUBMITTED | 确认 `POST /plans/{id}/decisions {action:CONFIRM}` | 人类；确认人或其上级 / 所有者；非本人 | CONFIRMED | `plan.confirmed`；关闭收件箱项 |
| P4 | SUBMITTED | 退回 `{action:RETURN, comment}` | 同上；comment ≥4 字 | RETURNED | `plan.returned`；通知作者 |
| P5 | RETURNED | 编辑 | 作者 | DRAFT | — |
| P6 | CONFIRMED | 编辑 | 作者 | SUBMITTED（重新确认） | 增删项后需再确认；`plan.submitted` |

非法：Agent 确认任何计划 → 403 `AGENT_PROHIBITED`；本人确认本人 → 403 `SELF_REVIEW`；对 DRAFT 确认 → 409 **180420**。

### 4.3 报告 `ReportState`

```
DRAFT ──提交/核对──▶ CHECKING ──有阻断──▶ BLOCKED ──编辑──▶ DRAFT
                        │无阻断
                        ▼
                      READY ──提交──▶ SUBMITTED ──确认──▶ CONFIRMED
                        ▲                 │退回
                        └──编辑─ DRAFT ◀── RETURNED
```

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| R1 | — | 周期生成（J1） | 成员在周期内为团队成员 ≥1 天；周期类型在 required 或成员已开启 | DRAFT | 自动填充 §5.4；`report.generated`；`due_at` |
| R2 | DRAFT / BLOCKED / READY / RETURNED | 核对 `POST /reports/{id}/checks` | — | CHECKING | 同步执行（≤3 s，超时转后台，见 BR-10-33） |
| R3 | CHECKING | 核对完成 | 阻断数 > 0 | BLOCKED | 写 check；`report.checked`；收件箱 REPORT_BLOCKED 给作者 |
| R4 | CHECKING | 核对完成 | 阻断数 = 0 | READY | 写 check；`report.checked`；flags 更新 |
| R5 | READY | 提交 `POST /reports/{id}/submissions {expected_content_hash}` | `last_check_hash = content_hash = expected`；核对未过期（数据未变，见 BR-10-35）；作者或代理作者 | SUBMITTED | `submitted_*`；`report.submitted`；收件箱 REPORT_CONFIRMATION；更新父报告 `child_submitted` |
| R6 | DRAFT / RETURNED | 直接提交 | — | 先 R2，再按 R3/R4，R4 时继续 R5 | 一次调用完成「核对 + 提交」 |
| R7 | SUBMITTED | 确认 `POST /reports/{id}/decisions {action:CONFIRM}` | 人类；确认人（BR-10-20）；非本人 | CONFIRMED | `report.confirmed`；父报告 `child_confirmed+1`；改进项进入知识回流（BR-10-41） |
| R8 | SUBMITTED | 退回 `{action:RETURN, comment}` | 同上 | RETURNED | `report.returned`；父报告 `child_submitted−1`（若父已提交 → 父 flags 加 `NEEDS_RECHECK`） |
| R9 | 任意非 CONFIRMED | 内容编辑 | 作者 | DRAFT | content_hash 变化；`last_check_*` 失效 |
| R10 | READY / SUBMITTED | 底层数据变化（事件，BR-10-35） | 影响已核对事实 | 保持状态，flags + `NEEDS_RECHECK` | READY 时提交前须重核；SUBMITTED 时通知作者与确认人 |
| R11 | 非 SUBMITTED / CONFIRMED | `due_at` 过（J3） | — | 保持状态，flags + `OVERDUE` | `report.overdue`；收件箱 REPORT_OVERDUE（作者 + 上级） |

非法：BLOCKED 直接提交 → 409 **180430**（`data.issues[]` 前 20 条）；content_hash 与 expected 不一致 → 409 **180431**（他人已改）；核对过期 → 409 **180432**；CONFIRMED 后编辑 → 409 **180433**（需 RETURN 后改，或在下周期更正）；Agent 确认 → 403 `AGENT_PROHIBITED`；自己确认 → 403 `SELF_REVIEW`。

---

## 5. 业务规则与边界情况

### 5.1 目标

- **BR-10-01 层级**：TEAM 级目标仅 YEAR / QUARTER；PROJECT 级 QUARTER / MONTH；MEMBER 级 QUARTER / MONTH。子目标周期必须落在父目标周期内（`period_start ≥ parent.period_start` 且结束不晚于父）。违反 → 422 **180401**。
- **BR-10-02 KR 数量**：每目标 1–5 个 KR；`target ≠ baseline`；AUTO KR 的 `metric_scope` 不得超出目标范围（PROJECT 目标只能取本项目；MEMBER 目标只能取本人）→ 422 **180402**。
- **BR-10-03 挂接**：非 TEAM 目标必须挂到父目标的某个 KR（`parent_kr_id`）；PROJECT 目标的父必须是 TEAM 目标；MEMBER 目标的父必须是其所在项目的 PROJECT 目标（或 TEAM 目标，当成员在团队级工作时，`project_id` 为 NULL）。
- **BR-10-04 成员目标**：人类成员与 Agent 成员（含 `AGENT_SPAWNED`）均可有目标；主管创建且未收编的 Agent 的目标由其主管 Agent 起草、负责人类确认（随 G1 激活前须人类）。Agent 退役 → 其开放目标转 CANCELLED（理由「成员退役」），由上级决定是否由接手人重建。
- **BR-10-05 权限**：TEAM / PROJECT 目标 CRUD 需 `goal_manage`（所有者、交付负责人；HUMAN）；MEMBER 目标：本人（人或 Agent）可起草与编辑 DRAFT，激活需 `goal_member_manage` 且由上级（人类）或本人（人类）执行；Agent 不能激活自己的目标（须负责人类激活）。
- **BR-10-06 贡献者**：目标激活时为目标负责人登记其所有 KR 的 `OWNER` 贡献；子目标激活时为子目标负责人登记 `parent_kr_id` 的 `CHILD_GOAL` 贡献；上级可显式 `ASSIGNED`。目标取消时移除 `CHILD_GOAL` 登记。
- **BR-10-07 对齐覆盖率**：`alignment_coverage(goal) = Σ_{kr ∈ goal.krs} kr.weight × [kr 至少有 1 个 ACTIVE/AT_RISK/ACHIEVED 子目标] / Σ kr.weight`；MEMBER 级为 NULL。目标树显示覆盖率 < 100% 的 KR 为「未拆解」。
- **BR-10-08 进度**：
  - KR：`INCREASE`：`clamp((current − baseline) / (target − baseline), 0, 1)`；`DECREASE`：`clamp((baseline − current) / (baseline − target), 0, 1)`。
  - 目标：`Σ kr.weight × kr.progress / Σ kr.weight`，四舍五入到 0.0001。
  - 子目标进度**不**自动累加到父 KR（避免重复计数）；父 KR 若为 AUTO 则由系统指标直接计算；MANUAL 父 KR 的建议值 = 子目标进度的加权平均（仅显示为参考，负责人确认后写入）。
- **BR-10-09 AUTO 取值**：J4 每 15 分钟刷新；指标定义与公式以 07 §5.8 为准（`ACCEPTED_WORK_ITEMS` = 范围内周期内首次 ACCEPTED 的工作项数，`ON_TIME_REPORT_RATE` = 本模块：按时提交报告数 / 应提交数）；读取失败保留旧值并显示「取值失败 · 上次 xx」，不清零。
- **BR-10-10 MANUAL 取值**：负责人更新 `current` 时可附证据；无证据时 KR 标记待佐证（N3）。Agent 可更新自己 MEMBER 目标的 MANUAL KR，但必须附证据（缺证据 → 422 **180405**）。
- **BR-10-11 时间进度**：`elapsed = (now − period_start) / (period_end − period_start)`，按团队时区，按工作日计（`workdays`）。

### 5.2 计划

- **BR-10-12 周期**：计划周期取 DAY / WEEK / MONTH / QUARTER / YEAR；成员默认需要 WEEK 计划（Agent 默认 DAY + WEEK）；更长周期可选。
- **BR-10-13 计划项**：`goal_id` 必填，且必须是该成员的 MEMBER 目标、或该成员为贡献者（§3.5）的 PROJECT / TEAM 目标；`kr_id` 若填必须属于该目标；不满足 → B3（未关联）或 B5（他人 KR）。
- **BR-10-14 工作项关联**：`kind=WORK_ITEM` 时工作项须属于该成员（02 `WorkItemReader.LoadByPrincipal`：当前 EXECUTOR / REVIEWER 分派，或周期内曾分派）；否则 422 **180412**。
- **BR-10-15 起草**：J2 在周期开始前 1 小时（DAY：当日 08:00；WEEK：周一 08:00；其余：周期首日 08:00）为每个成员生成 DRAFT：① 上期报告 `uncompleted.carry_over=CARRY_TO_NEXT` 项（`carried_from_item_id`）；② 成员当前负责的 READY / CLAIMED / IN_PROGRESS / REWORK 工作项；③ 每个未达成 KR 一条建议项（标注「建议」）。Agent 成员由其自身执行一次「计划起草」指令（05 通道，`agent.plan_draft`，可选；未实现前使用系统起草）。
- **BR-10-16 上级确认**：计划提交后由确认人（BR-10-20）确认；72 小时未确认 → 计划标 `UNCONFIRMED`（报告核对 N2），提醒确认人一次。
- **BR-10-17 变更**：已确认计划被作者增删项 → 回到 SUBMITTED（P6），需重新确认；仅修改备注 / 估算不触发。
- **BR-10-18 上限**：每计划 ≤ 200 项；DAY 计划 ≤ 30 项（超出 422 **180413**）。

### 5.3 确认人与上下级

- **BR-10-19 上下级关系**：成员的上级按优先级：① 项目的交付负责人（成员只在一个 ACTIVE 项目时）；② 团队所有者；可在 `planning_settings` 外按成员覆盖（v1.1）。Agent 成员的「负责人类」= 05 `AgentDirectory` 的 `owner_principal_id`（创建 Agent 的人类；主管创建的 Agent = 授予其编制授权的人类，即 SpawnGrant.granted_by）；无则取项目交付负责人。
- **BR-10-20 确认人**：
  - MEMBER 报告 / 计划：人类成员 → 上级；Agent 成员 → 负责人类（主管创建且未收编的 Agent：其主管 Agent 可**预审**（添加评论，不改变状态），最终确认必须是人类）。
  - PROJECT 报告：团队所有者（或所有者指定的人）。
  - TEAM 报告：团队所有者自身提交即视为最终（无上级确认，状态到 SUBMITTED 后由系统直接 CONFIRMED，`confirmed_by=SYSTEM:team_top`）。
  - 确认人与作者相同 → 改为确认人的上级；仍相同（单人团队）→ 允许自确认需 owner 开启 `governance_sod_relax`（沿用 08）。
- **BR-10-21 确认权限**：`report_confirm` / `plan_confirm` 为**仅限人类**；Agent 主体一律 403 `AGENT_PROHIBITED`（含委托与策略来源）。

### 5.4 报告生成与填充

- **BR-10-22 周期边界**：按团队时区；DAY = 本地自然日（仅工作日）；WEEK = 周一 00:00 至周日 24:00；MONTH / QUARTER / YEAR = 自然月 / 季 / 年。
- **BR-10-23 生成时机**：J1 按 `*_report_at` 为每个应报告主体生成 DRAFT（幂等键 `report_key`）；DAY 报告在当天 `day_report_at` 生成（覆盖当天至生成时刻，之后的事实仍可补，核对以提交时刻为准）。
- **BR-10-24 应报告主体**：MEMBER 级 = 周期内曾为租户成员 ≥1 个工作日的人类成员，以及周期内状态曾为 ACTIVE 的 Agent（RETIRED 前的周期仍需报告，由负责人类代为提交，`submitted_by` 记人类）；PROJECT 级 = 周期内阶段 ∈ {DISCOVERY, PLANNING, ACTIVE, BLOCKED, ACCEPTANCE_PENDING} 的项目；TEAM 级 = 1。
- **BR-10-25 MEMBER 报告自动填充**：
  - `completed`：周期内该成员相关且达到「完成」的系统事实：作为执行者其工作项进入 ACCEPTED（`fact_ref` = 工作项 + 接受裁决 id）；作为执行者其声明验证通过且仍待裁决的项**不进入 completed**（进入 uncompleted，reason 预填 `OTHER`、detail 预填「已验证，待裁决」，carry_over 预填 `CARRY_TO_NEXT`）；作为裁决人做出的裁决（DECISION）；作为交付人发送的交付（DELIVERY）；计划中的 FREE_TASK 由成员勾选完成并附证据（`FREE_TASK_EVIDENCE`：URL / 文件 / 说明，至少一项）。
  - `uncompleted`：本期计划中未完成的项（工作项未 ACCEPTED / FREE_TASK 未勾选），reason 按系统事实预填（工作项 BLOCKED → `BLOCKED_DEPENDENCY`；REWORK → `QUALITY_REWORK`；范围变更标记过期 → `SCOPE_CHANGED`），`detail` 预填系统说明但须作者确认（BR-10-29）。
  - `summary`：计数与成本（成本：Agent 成员取其执行成本合计；人类成员取其主导执行的成本）、KR 进度起止。
  - `retrospective`：Agent 成员由自身起草（`agent.report_draft` 指令；不可用时系统模板）；人类成员由 AI 起草（协调员 Agent 或系统模板），`retrospective_ai_drafted=true`。
  - `next_plan`：取下周期 DRAFT 计划（若无则按 BR-10-15 生成）的项。
- **BR-10-26 时间汇总**：WEEK 报告的 `completed` 包含其 DAY 报告的 completed（去重，以 `fact_ref` 为键）+ 周内其它系统事实；`uncompleted` = 周计划未完成项（DAY 的未完成若后续完成则不再计入）；`summary` 数字以系统事实重新计算（不是简单相加），并与下级合计对比（B2，允许差额来源于「下级未覆盖的天数」，见 BR-10-31）。MONTH ← WEEK（跨月周按日期切分归属），QUARTER ← MONTH，YEAR ← QUARTER。
- **BR-10-27 层级汇总**：PROJECT 报告：`completed` = 项目内工作项 ACCEPTED、交付、甲方验收事实（`fact_ref` 直接指向事实，同时 `links` 指向成员报告）；`summary` 与成员报告合计对比（B2）。TEAM 报告 ← PROJECT 报告 + 团队级成员（未归属项目）报告。
- **BR-10-28 下级引用**：汇总报告记录 `planning_report_links`；`child_total` = 应有下级报告数；`child_submitted` / `child_confirmed` 实时维护。
- **BR-10-29 人工确认 AI 内容**：人类作者提交前，AI 起草的复盘与系统预填的未完成原因 `detail` 必须被编辑或逐条点「确认」（`retrospective_ai_drafted=false`）；否则 B4。

### 5.5 核对（Reconciliation）

- **BR-10-30 核对项**（对 content_hash 快照计算，事实读取同一时刻）：

| 码 | 级别 | 规则 | 示例提示 |
|---|---|---|---|
| B1_UNBACKED_COMPLETION | 阻断 | `completed` 条目的 `fact_ref` 不存在，或事实未达完成：WORK_ITEM 不是 ACCEPTED（VERIFIED 仅在报告条目显式标为「已验证待裁决」且放在 uncompleted 时允许），DECISION 不存在或非作者做出，DELIVERY 非 SENT/ACCEPTED，FREE_TASK 无证据，CHILD_REPORT 条目不存在 | 「BIL-142 标为已完成，但系统中仍为 待裁决」 |
| B2_AGGREGATE_MISMATCH | 阻断 | `summary` 中的计数 / 成本 ≠ 系统事实重算值；汇总报告中 `completed_count` < 下级已提交报告 completed 去重合计 | 「已接受工作项：本报告 9，系统 7」 |
| B3_UNLINKED_PLAN_ITEM | 阻断 | `next_plan` 条目或计划项缺 `goal_id`，或目标已 CANCELLED / 不存在 | 「下周计划第 3 项未关联目标」 |
| B4_MISSING_SECTION | 阻断 | 四章节任一为空（`completed` 可为空但须有 summary 说明且 uncompleted 非空或 retrospective.problems 说明原因）；uncompleted 条目缺 reason / detail / carry_over；AI 草稿未确认（BR-10-29） | 「复盘·根因 为空」 |
| B5_FOREIGN_KR | 阻断 | 条目声称推进的 `kr_id` 不在作者贡献者登记内（§3.5） | 「KR 'PROJ-Q4-KR2' 不属于你的目标链」 |
| N1_CHILD_UNSUBMITTED | 非阻断 | 汇总报告：`child_submitted < child_total` → flag `UNCHECKED`（UI「未核对 n/m」） | 「3 / 5 份成员周报未提交」 |
| N2_PLAN_UNCONFIRMED | 非阻断 | 本期计划或下期计划未被上级确认 → flag `UNCONFIRMED`（「未确认」） | |
| N3_KR_NO_EVIDENCE | 非阻断 | 引用的 MANUAL KR 最新值无证据 → flag `NEEDS_EVIDENCE`（「待佐证」） | |
| N4_STALE_CHECK | 非阻断 | 已提交报告的事实在提交后发生变化 → flag `NEEDS_RECHECK` | 「提交后 BIL-139 被重开」 |

- **BR-10-31 汇总容差**：B2 比较时，时间汇总只对「已有下级报告覆盖的日期」做严格相等；无下级报告的日期（如 DAY 报告可选未开启）以系统事实为准，不判不一致。
- **BR-10-32 flag 传播**：汇总报告的 flags = 自身 N 级问题 ∪ 下级 flags 的并集（`OVERDUE` 不传播，按数量显示「逾期 k」）；UI 显示核对覆盖率 `child_confirmed / child_total` 与 `child_submitted / child_total`。
- **BR-10-33 性能**：单次核对 P95 ≤ 1.5 s（MEMBER WEEK）、≤ 3 s（TEAM MONTH）；同步调用超过 3 s 返回 202 + `check_id`，结果经 SSE `tenant/{t}/report/{id}` 推送。
- **BR-10-34 并发**：同一报告同时只运行 1 个核对（行锁 + `CHECKING` 状态）；重复请求返回进行中的 `check_id`（幂等）。
- **BR-10-35 过期判定**：报告 READY 后，若被引用的事实（工作项状态、裁决、交付、下级报告 content_hash）发生变化，事件消费者（J6）把 `last_check_hash` 置空并加 `NEEDS_RECHECK`；提交时校验 `last_check_hash = content_hash` 且无 `NEEDS_RECHECK`，否则 409 **180432**（客户端自动重核）。
- **BR-10-36 证据口径**：B1 判定以提交时刻的事实为准；周期结束后才变为 ACCEPTED 的工作项不计入本周期 completed（应进入下一周期），作者可在 note 中说明。

- **BR-10-48 计算进度与期望进度（D112 §15.4 防西瓜）**：
  - MEMBER：`computed_progress` = 作者本周期计划项按权重的完成比例，完成以系统事实为准（工作项 ACCEPTED、交付 SENT 等，与 B1 同口径）；无计划项时取作者成员目标 KR 进度的加权平均。
  - PROJECT / TEAM：取下级已提交报告 `computed_progress` 按目标权重加权；无下级时取本级目标 KR 进度。
  - `expected_progress`：有计划基线时 = 截至报告时刻**按计划应完成**的权重占比；否则 = 周期已流逝工作日占比。
- **BR-10-49 核对项增补**（并入 BR-10-30 表）：

| 码 | 级别 | 规则 | 示例提示 |
|---|---|---|---|
| B6_WATERMELON | 阻断 | `declared_confidence = GREEN` 且 `progress_lag ≥ watermelon_lag_threshold`（默认 0.30），且 `status_explanation` 为空或少于 20 字 | 「你声明为绿，但系统计算进度 45%，应达 80%（落后 35%）。请调整状态或说明原因。」 |
| B7_CONFIDENCE_DROP_NO_PLAN | 阻断 | 本期 `declared_confidence` 低于同主体上一期同周期报告（GREEN→YELLOW/RED、YELLOW→RED），且 `improvement_plan` 为空或缺负责人 / 期限 | 「状态由绿降为黄，请填写改进计划（至少 1 项，含负责人与期限）。」 |
| N5_STATUS_EXPLAINED | 非阻断 | B6 条件成立但已填写合格说明 → flag `STATUS_EXPLAINED`（上级视图高亮「外绿内红风险」） | |
| N6_ABOVE_CHILDREN | 非阻断 | 汇总报告声明信心高于下级已提交报告中最差者（如下级有 RED 而本级 GREEN） → flag `ABOVE_CHILDREN` | 「2 份下级周报为红，本报告为绿」 |

- **BR-10-50 信心必填**：`confidence_required_kinds` 内的周期，提交时 `declared_confidence` 为空 → 归入 B4（缺章节）；Agent 成员的信心由 Agent 起草、作者负责人（`owner_principal_id`）确认时可修改。
- **BR-10-51 质量指标填充**：PROJECT / TEAM 报告生成时从 07 `DeliveryMetricsReader` 读取本周期穿透率、内部一次通过率、平均内部轮次写入 `quality_metrics`，并在 `completed` 摘要中生成一行「质量」概述；穿透率超过 07 / 08 阈值时自动在 `retrospective.problems` 生成草稿条目（作者可编辑，不可删除事实数字）。

### 5.6 提交、确认与逾期

- **BR-10-37 提交者**：MEMBER 报告由本人提交（Agent 以自身凭证）；Agent 不可用（STOPPED / CRASHED / RETIRED）时负责人类可代为提交（`submitted_by` 记人类，`detail.on_behalf_of=agent`）。PROJECT 报告由交付负责人提交；TEAM 报告由所有者提交。
- **BR-10-38 宽限与逾期**：`due_at = period_end_local + grace_*_minutes`；J3 每 10 分钟扫描，未 SUBMITTED / CONFIRMED 且已过 → `OVERDUE`，通知作者与上级（每份报告最多 2 次：逾期时、逾期 24 h）。逾期报告仍可提交，`submitted_late=true`（`ON_TIME_REPORT_RATE` 指标据此）。
- **BR-10-39 退回**：退回必须有理由（≥4 字）；退回后作者编辑并重新核对提交；同一报告退回 ≥3 次 → 通知团队所有者。
- **BR-10-40 不可变**：CONFIRMED 报告不可编辑；发现错误 → 确认人可「撤销确认」（`POST /reports/{id}/decisions {action:REOPEN, reason}`，仅确认人或所有者，HUMAN，step-up 不需要）→ RETURNED，审计。
- **BR-10-41 知识回流（v1.1）**：CONFIRMED 后，`improvements[].propose_memory=true` 的条目经 06 `MemoryWriter.Propose`（kind=EXPERIENCE，scope = AGENT（Agent 成员）/ PROJECT（项目报告），来源 `report_id`）；人类成员的改进项默认不写个人记忆（隐私），可选写 PROJECT。RULE 型不得由此路径直接生成。

### 5.7 其他边界

- **BR-10-42 成员变更**：周期内退出团队的人类成员仍生成该周期报告（截止至退出日），由其上级代提交或标注「成员已离开」后跳过（`skipped=true`，不计入 child_total）。
- **BR-10-43 时区变更**：修改 `timezone` 仅对未生成的周期生效；已生成报告保持原边界。
- **BR-10-44 上限**：报告 content ≤ 1 MB；completed ≤ 500 条（超出时汇总报告按工作项类型聚合为一条并在 note 中给出明细链接）。
- **BR-10-45 幂等**：创建 / 提交 / 确认 / 核对类 POST 支持 `Idempotency-Key`（09 C7，`176425` / `176409`）。
- **BR-10-46 权限拒绝**：统一 403 **180403** + `data.reason_code`（08 `AuthorityReasonCode`：`MISSING_PERMISSION`、`AGENT_PROHIBITED`、`SELF_REVIEW`、`NOT_OWNER`）。
- **BR-10-47 可见性**：成员可读自己的报告与计划；上级可读下属；`report_read` 持有者可读项目 / 团队报告；Agent 只读自己的与其所在项目的 PROJECT 报告（不读其他成员的 MEMBER 报告）。

---

## 6. 接口契约

约定：响应 `respx` 信封；GET 返回 `ETag`，更新带 `If-Match`（缺失 428 **180428**，冲突 409 **180409**）；创建 / 命令型 POST 支持 `Idempotency-Key`；OpenAPI tag = 最终发布路径首段；守卫 `T` = `authz.Register`（租户级，路径 `/tenants/{tenant_id}/…`），`O` = `authz.RegisterOwned`（全局短地址，按所属租户 / 主体解析）。

### 6.1 设置

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| S1 | GET `/tenants/{tenant_id}/planning-settings` | tenants | `goal_read` · T | — | Settings | — |
| S2 | PATCH `/tenants/{tenant_id}/planning-settings` | tenants | `planning_settings_manage` · T；HUMAN；If-Match | 任意 §3.2 字段 | Settings | 422 180440 |

### 6.2 目标与 KR

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 / 幂等 |
|---|---|---|---|---|---|---|
| G1 | GET `/tenants/{tenant_id}/goals` | tenants | `goal_read` · T | 过滤 `level`(eq,in) `status`(in) `period_kind`(eq) `period_start`(gte,lte) `project_id`(eq) `owner_principal_id`(eq) `parent_goal_id`(eq)；排序 `period_start` `progress` | Goal[]（含 KR 摘要、覆盖率） | — |
| G2 | GET `/tenants/{tenant_id}/goal-trees` | tenants | `goal_read` · T | `?period_kind&period_start&depth=1..3` | 树 TEAM → PROJECT → MEMBER，含进度、覆盖率、风险 | — |
| G3 | POST `/tenants/{tenant_id}/goals` | tenants | TEAM/PROJECT：`goal_manage`；MEMBER：`goal_member_manage` 或本人 · T | `{level*, project_id?, owner_principal_id?, parent_goal_id?, parent_kr_id?, period_kind*, period_start*, title*, description?, weight?, key_results*[{title*, source*, metric?, metric_scope?, direction?, baseline?, target*, weight?}]}` | Goal（DRAFT） | 422 180401/180402；幂等 |
| G4 | GET `/goals/{goal_id}` | goals | `goal_read` · O | `?include=children,contributors,plan_items` | Goal | 404 180404 |
| G5 | PATCH `/goals/{goal_id}` | goals | 同 G3 · O；If-Match | 可选字段（ACTIVE 后不可改 level / parent / period） | Goal | 409 180410 |
| G6 | PUT `/goals/{goal_id}/activation` | goals | 同 G3 激活规则 · O；HUMAN（Agent 的 MEMBER 目标须人类激活） | — | Goal（ACTIVE） | 409 180411 |
| G7 | PUT `/goals/{goal_id}/closure` | goals | `goal_manage` 或上级 · O；HUMAN | `{result: ACHIEVED/MISSED/CANCELLED, reason?}` | Goal | 409 180410；403 `AGENT_PROHIBITED` |
| G8 | PUT `/key-results/{kr_id}/current` | key-results | KR 所属目标负责人或上级 · O；If-Match | `{current*, evidence?[]}`（仅 MANUAL） | KR | 409 180414；422 180405 |
| G9 | POST `/key-results/{kr_id}/contributors` | key-results | `goal_manage` · O | `{principal_id*}` | Contributor（ASSIGNED） | 409 180415 |
| G10 | DELETE `/key-results/{kr_id}/contributors/{principal_id}` | key-results | `goal_manage` · O | — | — | 409 180416 |

### 6.3 计划

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| P1 | GET `/tenants/{tenant_id}/plans` | tenants | 本人 / 上级 / `report_read` · T | 过滤 `principal_id` `period_kind` `period_start`(gte,lte) `state`(in) | Plan[] | — |
| P2 | GET `/plans/{plan_id}` | plans | 同上 · O | — | Plan + items + 最近核对 | 404 |
| P3 | POST `/tenants/{tenant_id}/plans` | tenants | `plan_submit`（为自己） · T | `{period_kind*, period_start*}`（已存在返回原计划） | Plan（DRAFT，含自动带入项） | 幂等 |
| P4 | PUT `/plans/{plan_id}/items` | plans | 作者 · O；If-Match | `{items*[{id?, kind*, goal_id*, kr_id?, work_item_id?, free_category?, title*, estimate_hours?, estimate_cost_micros?}]}`（整体替换） | Plan | 422 180412/180413 |
| P5 | POST `/plans/{plan_id}/submissions` | plans | 作者 · O | — | Plan（SUBMITTED）+ check | 409 180420；409 180430（B3/B5，`data.issues[]`） |
| P6 | POST `/plans/{plan_id}/decisions` | plans | `plan_confirm` · O；HUMAN；确认人或其上级 | `{action*: CONFIRM/RETURN, comment?}` | Plan | 403 `AGENT_PROHIBITED` / `SELF_REVIEW`；409 180420 |

### 6.4 报告

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| R1 | GET `/tenants/{tenant_id}/reports` | tenants | BR-10-47 · T | 过滤 `level` `period_kind` `period_start`(gte,lte) `principal_id` `project_id` `state`(in) `flag`(in) `subject_kind`(eq) `agent_origin`(eq，经 05 解析)；排序 `period_start` `due_at` | Report[]（不含正文，含 flags 与覆盖） | — |
| R2 | GET `/tenants/{tenant_id}/report-calendars` | tenants | `report_read` · T | `?period_kind&from&to&subject_kind?&agent_origin?` | 矩阵：成员 × 周期 → state、flags、due_at | — |
| R3 | GET `/reports/{report_id}` | reports | BR-10-47 · O | `?include=links,last_check` | Report（含 content、links、最近核对 issues） | 404 |
| R4 | PATCH `/reports/{report_id}/content` | reports | 作者 · O；If-Match | `{content*}`（按 §3.12 Schema 校验） | Report（state → DRAFT） | 422 180441；409 180433 |
| R5 | POST `/reports/{report_id}/checks` | reports | 作者 / 确认人 / `report_read` · O | — | Check + issues（≤3 s；否则 202 `{check_id}`） | 幂等（进行中返回同一 check） |
| R6 | GET `/report-checks/{check_id}` | report-checks | 同 R3 · O | — | Check + issues | 404 |
| R7 | POST `/reports/{report_id}/submissions` | reports | `report_submit`（作者；代提交见 BR-10-37） · O | `{expected_content_hash*}` | Report（SUBMITTED） | 409 180430/180431/180432；幂等 |
| R8 | POST `/reports/{report_id}/decisions` | reports | `report_confirm` · O；HUMAN；确认人或其上级 | `{action*: CONFIRM/RETURN/REOPEN, comment?, reason?}` | Report | 403 `AGENT_PROHIBITED` / `SELF_REVIEW`；409 180434 |
| R9 | POST `/reports/{report_id}/pre-reviews` | reports | 报告作者（子 Agent）的主管 Agent · O | `{comment*}` | PreReview（仅评论，不改状态） | 403 `MISSING_PERMISSION` |
| R10 | POST `/reports/{report_id}/regenerations` | reports | 作者 · O | `{sections?: ["completed","uncompleted","next_plan"]}`（按最新事实重填，保留人工编辑的复盘） | Report（DRAFT） | 409 180433 |
| R11 | GET `/reports/{report_id}/exports` | reports | BR-10-47 · O | `?format=md`（pdf v1.1） | 文件流 | — |

### 6.5 错误码（本模块 180000–184999）

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 180401 | 422 | planning.error.goal_hierarchy | 层级 / 周期 / 挂接不合法 |
| 180402 | 422 | planning.error.kr_invalid | KR 数量 / 目标值 / 范围不合法 |
| 180403 | 403 | planning.error.forbidden | 权限拒绝（`data.reason_code`） |
| 180404 | 404 | planning.error.not_found | 不存在或不可见 |
| 180405 | 422 | planning.error.kr_evidence_required | Agent 更新 MANUAL KR 未附证据 |
| 180409 | 409 | planning.error.version_conflict | If-Match 不符 |
| 180410 | 409 | planning.error.goal_closed | 目标已关闭 |
| 180411 | 409 | planning.error.goal_not_activatable | 激活条件不满足 |
| 180412 | 422 | planning.error.work_item_not_owned | 工作项不属于该成员 |
| 180413 | 422 | planning.error.plan_too_large | 计划项超上限 |
| 180414 | 409 | planning.error.kr_auto | AUTO KR 不可手填 |
| 180415 | 409 | planning.error.contributor_exists | 贡献者已存在 |
| 180416 | 409 | planning.error.contributor_system | 系统登记的贡献者不可手删 |
| 180420 | 409 | planning.error.plan_state | 计划状态不允许该操作 |
| 180428 | 428 | planning.error.precondition_required | 缺 If-Match |
| 180430 | 409 | planning.error.blocking_issues | 存在阻断问题，禁止提交（`data.issues[]` 前 20 条） |
| 180431 | 409 | planning.error.report_changed | 内容哈希与预期不符 |
| 180432 | 409 | planning.error.check_stale | 核对已过期，需重新核对 |
| 180433 | 409 | planning.error.report_confirmed | 已确认报告不可编辑 |
| 180434 | 409 | planning.error.report_state | 报告状态不允许该操作 |
| 180440 | 422 | planning.error.settings_invalid | 设置不合法 |
| 180441 | 422 | planning.error.content_schema | 报告内容不符合 Schema |
| 180503 | 503 | planning.error.dependency_unavailable | 02 / 05 / 07 读取失败（核对 fail-closed，不给 PASS） |

- **D112 / D113 增补错误码**：`180460` invalid_confidence（422，非枚举值）、`180461` improvement_plan_invalid（422，缺负责人 / 期限 / 超过 10 项）、`180462` status_explanation_too_short（422，< 20 字时用于字段校验提示；提交时仍以 B6 阻断返回）。报告更新接口 `PUT /reports/{report_id}`（既有）接受 `status.declared_confidence`、`status.explanation`、`improvement_plan`；computed / expected 只读。

### 6.6 跨模块接口

**本模块提供**

| 接口 | 方法 | 使用者 |
|---|---|---|
| `GoalReader` | `ActiveForPrincipal(ctx, tenant, principal, date) ([]GoalView, error)`；`KRContributors(ctx, tenant, krID) ([]guid.ID, error)` | 05（Agent 计划 / 报告起草上下文）、06（注入「我的目标」） |
| `ReportReader` | `Get(ctx, tenant, reportID)`；`OnTimeRate(ctx, tenant, principalIDs[], from, to)`；`Coverage(ctx, tenant, periodKind, periodStart)` | 07（指标）、08（总览「报告覆盖」磁贴） |

**本模块使用**

| 提供者 | 方法 |
|---|---|
| 02 `WorkItemReader` | `LoadByPrincipal`、**新增** `ListByAssigneeInPeriod(ctx, tenant, principal, from, to)`（CR-10-07）、`ListByIDs`；`DecisionReader.Get` |
| 07 | `DeliveryMetricsReader`（BR-10-09）、`ProjectReader.HealthList`、交付与验收读取 |
| 05 `AgentDirectory` | 成员类型、`owner_principal_id`（负责人类）、`supervisor_agent_id`、`origin`、状态；机器通道可选指令 `agent.plan_draft` / `agent.report_draft`（CR-10-08） |
| 08 | `AuthorityChecker`、`Notifier` 路由、收件箱投影 |
| 09 | `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`、`Notifier` |
| 06 | `MemoryWriter.Propose`（BR-10-41） |

---

## 7. 后台任务与事件

### 7.1 任务（09 JobQueue；周期任务经 dlock 单实例）

| 任务 | 触发 / 频率 | 互斥 | 行为 | 重试 |
|---|---|---|---|---|
| J1 `planning.report_generate` | 每 5 分钟检查各租户本地时间是否到达 `*_report_at` | dlock `planning:report-generate` | 按 `report_key` 幂等生成 DRAFT：成员 → 项目 → 团队；汇总报告建立 links | 指数退避 5 次；死信并通知所有者 |
| J2 `planning.plan_draft` | 每 5 分钟检查周期开始前 1 小时 | dlock `planning:plan-draft` | 生成下周期 DRAFT 计划（BR-10-15） | 同上 |
| J3 `planning.overdue_scan` | 每 10 分钟 | dlock `planning:overdue` | 标 `OVERDUE`、通知（BR-10-38） | — |
| J4 `planning.kr_refresh` | 每 15 分钟；相关事件后 60 s 内合并触发 | dlock `planning:kr-refresh` | 刷新 AUTO KR、目标进度、AT_RISK | 读取失败保留旧值 |
| J5 `planning.goal_close_reminder` | 每日 09:00（本地） | dlock | 周期结束 7 天未关闭目标 → 通知 | — |
| J6 `planning.recheck_on_change` | 消费 §7.3 事件 | 按报告分区 | READY → 清 `last_check_hash` + `NEEDS_RECHECK`；SUBMITTED → `NEEDS_RECHECK` + 通知；维护父报告 `child_*` | Transactional 订阅 |
| J7 `planning.unconfirmed_plan_remind` | 每小时 | dlock | 计划 SUBMITTED > 72 h → 标 `UNCONFIRMED` + 提醒一次 | — |

### 7.2 发布事件

| 事件 | 时机 | data |
|---|---|---|
| `goal.created` / `goal.updated` | G3 / G5 | `{goal_id, level, project_id?, owner_principal_id?, period_kind, period_start}` |
| `goal.status_changed` | G1–G5 | `{goal_id, from, to, progress}` |
| `plan.submitted` / `plan.confirmed` / `plan.returned` | P2–P6 | `{plan_id, principal_id, subject_kind, period_kind, period_start, confirmer_principal_id}` |
| `report.generated` | R1 | `{report_id, level, period_kind, period_start, principal_id?, project_id?, due_at}` |
| `report.checked` | R3 / R4 | `{report_id, check_id, result, blocking_count, non_blocking_count}` |
| `report.submitted` | R5 | `{report_id, level, period_kind, period_start, principal_id?, project_id?, flags[], late}` |
| `report.confirmed` / `report.returned` | R7 / R8 | `{report_id, by, comment?}` |
| `report.overdue` | R11 | `{report_id, principal_id?, due_at}` |

均可经 SSE 推送完整载荷（不含报告正文）。

- **D112 / D113**：`report.checked` 载荷增加 `issues[]` 中的 B6 / B7 / N5 / N6；`report.submitted` 载荷增加 `declared_confidence`、`progress_lag`、`flags`；新增消费 07 指标快照完成事件（或按需读取 `DeliveryMetricsReader`）以填充 `quality_metrics`。

### 7.3 消费事件

`work_item.state_changed`、`decision.made`（02）；`delivery.sent`、`acceptance.received`、`project.stage_changed`（07）；`agent.state_changed`、`agent.retired`、`agent.spawned`、`agent.adopted`（05）；`execution.cost_updated`（03，只标记 NEEDS_RECHECK）；`report.*`（本模块父子汇总维护）。去重用 09 `domain_event_receipts`。

### 7.4 审计事件

`planning.goal_activated`、`planning.goal_closed`、`planning.plan_confirmed`、`planning.plan_returned`、`planning.report_submitted`（含 content_hash、check_id、flags）、`planning.report_submitted_on_behalf`、`planning.report_confirmed`、`planning.report_returned`、`planning.report_reopened`。detail 含 `actor_kind`、`authority_source`、`project_id?`。

### 7.5 SSE 主题

`tenant/{t}/report/{id}`、`tenant/{t}/plan/{id}`、`tenant/{t}/goal/{id}`；集合 `tenant/{t}/reports`、`tenant/{t}/goals`；`TopicAuthorizer`：`report_read`，或本人 / 上级。

---

## 8. 页面规格

路由前缀 `/t/:tenantID`；菜单经 09 C6 贡献（08 §8.6，D118 修订）：团队 › 目标树（`/goals`，`goal_read`）与报告中心（`/reports`，`report_read`）、工作台 › 我的计划（`/plans/me`，`plan_submit`）、项目空间 › 规划 › 目标（`/p/:projectID/goals`，同 §8.1 页面固定为本项目的 PROJECT 目标子树，`goal_read`）。

### 8.1 目标树 `/goals`

- 目的：看清团队 → 项目 → 成员的拆解、进度、未拆解 KR 与风险。
- 布局：顶部周期切换（年 / 季 / 月 + 期次）、筛选（项目、负责人、状态、成员类型：人类 / 人工创建 Agent / AI 创建 Agent）；主体为可折叠树：TEAM 目标 → KR → PROJECT 目标 → KR → MEMBER 目标。
- 节点：标题、负责人头像（Agent 带来源徽章）、目标进度条与时间进度刻度、状态徽章（AT_RISK 琥珀）、对齐覆盖率（「KR 拆解 2/3」）；未拆解 KR 显示虚线框与〔拆解为子目标〕（`goal_manage`）。
- 交互：点击节点进目标详情；从 KR 发起「新建子目标」（预填父目标与 KR）；不支持拖拽改挂接。
- 状态：加载骨架；空：「本期还没有团队目标」+〔新建团队目标〕（所有者）；错误：重试；无权限：说明缺 `goal_read`。
- 移动端：逐级下钻列表。

### 8.2 目标详情 `/goals/:goalID`

- 头部：标题、层级、周期、负责人、状态、进度环与时间进度；操作：编辑（DRAFT）、激活（无 KR 或父未激活时禁用并说明原因）、关闭（结果单选 + 理由）、取消（红色，理由必填）。
- 区块：① 关键结果表（来源、指标、基线 → 当前 → 目标、进度、更新时间、证据；MANUAL〔更新〕弹窗，Agent 必附证据）；② 子目标（按 KR 分组 + 覆盖率）；③ 贡献者（OWNER / CHILD_GOAL / ASSIGNED）；④ 本期关联计划项；⑤ 最近 6 期提到该目标的报告条目；⑥ 状态历史与审计。

### 8.3 我的计划 `/plans`、`/plans/:planID`

- 周期标签：日 / 周 / 月 / 季 / 年，左右切换期次；状态徽章（草稿 / 已提交 / 已确认 / 已退回）与确认人。
- 计划项表：类型（工作项 / 非交付任务）、标题、关联目标 · KR（必填；下拉只列可贡献目标）、工作项实时状态、估算、结转来源；底部「建议项」（未达成 KR，可一键加入）。
- 行内校验：未关联目标 → 红框「必须关联目标（B3）」；他人 KR → 「你不是该 KR 的贡献者（B5）」。
- 操作：保存；提交（存在 B3/B5 时禁用并列原因）；确认人视角〔确认〕〔退回（理由）〕；Agent 成员的计划由负责人类确认。
- 状态：空 →〔生成本期计划〕；错误；无权限。

### 8.4 报告编辑器 `/reports/:reportID`

- 头部：类型（日 / 周 / 月 / 季 / 年 · 成员 / 项目 / 团队）、周期、作者（人或 Agent + 来源徽章）、状态、flags（未核对 n/m、未确认、待佐证、逾期、需重新核对）、截止倒计时、确认人。
- 左侧四章节（固定顺序、全部必填）：
  1. 本期完成：条目（标题、证据 chip：工作项 / 裁决 / 交付 / 甲方验收 / 下级报告，可跳转；完成阶梯）；非交付任务须〔附证据〕。
  2. 未完成：标题、原因下拉、说明、结转（带入下期 / 放弃 / 升级）；系统预填项带「待确认」。
  3. 总结复盘：做得好 / 问题 / 根因 / 改进（可勾「提议为经验记忆」）；AI 草稿顶部提示「AI 起草，请编辑或逐条确认」。
  4. 下期计划：同 §8.3 组件，条目必须关联目标。
- 右侧核对面板：〔立即核对〕；结果分「阻断（必须处理）」与「提示（可提交但会标记）」，每条显示期望 / 实际 / 事实链接，点击定位并高亮条目；顶部「已核对 · 内容版本 abc123 · 10:42」或「内容已变化，需重新核对」。
- 底部操作：〔保存〕〔重新从系统填充〕〔提交〕：存在阻断问题时禁用并显示「存在 n 个阻断问题，处理后才能提交」；只有非阻断问题时提交前确认框列出将带上的标记。
- 确认人视角：只读 + 核对面板 +〔确认〕〔退回（理由）〕；已确认后仅确认人可〔撤销确认〕。主管 Agent（子 Agent 报告）：〔预审评论〕。
- 汇总报告额外「下级报告」标签：全部下级状态与 flags、覆盖率条；未提交下级可〔催交〕。
- 状态：加载、错误、无权限、已确认只读、逾期红色横幅。移动端：核对面板为底部抽屉，章节可折叠。

### 8.5 报告汇总 `/reports?view=rollup`

- 层级视图：团队 → 项目 → 成员；节点显示状态、覆盖率（已提交 / 应提交、已确认 / 应提交）、flags 计数、关键数字（完成数、未完成数、成本、KR 进度变化）；可下钻。
- 时间视图：选成员或项目，按年 → 季 → 月 → 周 → 日展示状态与差异（B2 问题高亮）。

### 8.6 团队报告中心 `/reports?view=calendar`

- 矩阵：行 = 成员（筛选：人类 / 人工创建 Agent / AI 创建 Agent），列 = 周期（本周 7 天、近 8 周、近 12 个月）；单元格：已确认（绿）/ 已提交（青）/ 草稿（灰）/ 阻断（红）/ 逾期（琥珀边框）；悬停显示 flags；点击进入报告。
- 汇总行：按时提交率、核对通过率；〔批量催交〕（只发提醒）。

---

### 8.7 D112 / D113 页面增补

- **报告编辑器头部**新增「状态」区：绿 / 黄 / 红单选；旁边并列「计算进度 x% / 应达 y%」双条与落后差值；落后 ≥ 阈值且选择绿时，右侧核对面板出现 B6 阻断项并在状态区展开「说明」输入框；信心下降时展开「改进计划」表格（行动、负责人、期限、关联 KR）。
- **核对面板**：B6 / B7 归入阻断组，N5 / N6 归入标记组；提交按钮文案在仅剩 N 项时显示「提交（带标记：状态已说明）」。
- **报告汇总**：每行显示声明信心色点与计算进度；`STATUS_EXPLAINED` 与 `ABOVE_CHILDREN` 以琥珀标签展示（「外绿内红风险」「状态高于下级」）；项目 / 团队行显示穿透率、内部一次通过率。
- **团队报告中心**：日历格增加信心色点；可按「外绿内红风险」筛选。

## 9. 通知

| # | 事件 | 接收人 | 渠道 | 文案 | 去重 |
|---|---|---|---|---|---|
| N-10-01 | `report.generated` | 作者（人类；Agent 经指令） | 站内 | 「{period}{kind}报告草稿已生成，截止 {due}」 | 每报告 1 次 |
| N-10-02 | `report.checked{BLOCKED}` | 作者 | 站内 + 收件箱 REPORT_BLOCKED | 「报告存在 {n} 个阻断问题，处理后才能提交」 | 每次核对 1 次 |
| N-10-03 | `report.submitted` | 确认人 | 站内 + 收件箱 REPORT_CONFIRMATION | 「{author} 提交了{period}{kind}报告{flags}」 | 每次提交 |
| N-10-04 | `report.returned` | 作者 | 站内 + 邮件 | 「报告被退回：{comment}」 | |
| N-10-05 | `report.overdue` | 作者 + 上级 | 站内 + 收件箱 REPORT_OVERDUE；上级邮件摘要 | 「{period}{kind}报告已逾期」 | 每报告 ≤2 次 |
| N-10-06 | `plan.submitted` | 确认人 | 收件箱 PLAN_CONFIRMATION | 「{author} 提交了{period}计划，请确认对齐」 | |
| N-10-07 | 计划 72 h 未确认 | 确认人 | 站内 | 「{author} 的计划等待确认已超过 72 小时」 | 1 次 |
| N-10-08 | `goal.status_changed{AT_RISK}` | 负责人 + 上级 | 站内 | 「目标「{title}」进度落后时间 {gap}%」 | 每日 ≤1 |
| N-10-09 | 已提交报告 `NEEDS_RECHECK` | 作者 + 确认人 | 站内 | 「{period}{kind}报告提交后事实发生变化，请重新核对」 | 每报告每日 ≤1 |
| N-10-10 | 目标周期结束 7 天未关闭 | 负责人 | 站内 | 「目标「{title}」周期已结束，请标记达成或未达成」 | 1 次 |

收件箱新增（08 §6.10）：`PLAN_CONFIRMATION`（关闭：plan.confirmed / returned）、`REPORT_CONFIRMATION`（关闭：report.confirmed / returned）、`REPORT_BLOCKED`（关闭：后续核对 PASS 或已提交）、`REPORT_OVERDUE`（关闭：已提交）。确认类事项的接收人仅限人类。

---

## 10. 验收标准

### 10.1 目标

```
AC-10-01  [integration]  US-10-01/02, BR-10-01, BR-10-03
Given 团队目标 T（2026 Q4，ACTIVE，KR1/KR2）
When  交付负责人创建项目目标 P，parent_goal=T，parent_kr=KR1，周期 2026-11（月）
Then  201 DRAFT；激活后 T.alignment_coverage = KR1.weight/(KR1.weight+KR2.weight)；目标树 KR2 显示「未拆解」
```

```
AC-10-02  [integration]  BR-10-01
Given 团队目标 T 周期 2026 Q4
When  创建子目标周期 2027-01
Then  422 180401；未写入
```

```
AC-10-03  [integration]  BR-10-02
When  创建 MEMBER 目标 M（成员 Ada）含 AUTO KR，metric_scope.project_ids 含 Ada 不在的项目
Then  422 180402
```

```
AC-10-04  [unit]  BR-10-08
Given KR1 INCREASE baseline 0 target 20 current 5（w=2）；KR2 DECREASE baseline 40 target 20 current 30（w=1）
Then  KR1.progress=0.25；KR2.progress=0.5；目标 progress=(2×0.25+1×0.5)/3=0.3333
```

```
AC-10-05  [integration]  G2, BR-10-11
Given 月目标进度 0.20，时间进度（工作日）0.45，阈值 0.15
When  J4 运行
Then  status ACTIVE → AT_RISK；goal.status_changed；N-10-08 发给负责人与上级（当日不重复）
```

```
AC-10-06  [integration]  BR-10-05, BR-10-04
Given Agent Echo（主管创建）为自己起草 MEMBER 目标（DRAFT）
When  Echo 调 PUT /goals/{id}/activation
Then  403 180403 AGENT_PROHIBITED；其负责人类 Sam 激活 → ACTIVE
```

```
AC-10-07  [integration]  BR-10-10
When  Agent Ada 更新自己 MANUAL KR 的 current 且未附证据
Then  422 180405；附 URL 证据后 200；人类更新无证据 → 200 且报告核对出现 N3_KR_NO_EVIDENCE
```

```
AC-10-08  [integration]  BR-10-09
Given AUTO KR 指标 ACCEPTED_WORK_ITEMS；07 读取失败
When  J4 运行
Then  current 保持旧值；KR 显示「取值失败 · 上次 xx」；进度不清零
```

### 10.2 计划

```
AC-10-09  [integration]  BR-10-15
Given Ada 上周报告 uncompleted 中 BIL-139 carry_over=CARRY_TO_NEXT；Ada 当前负责 BIL-143（IN_PROGRESS）；Ada 的 KR「本月接受 8 项」进度 0.5
When  周一 08:00 J2 运行
Then  生成本周 DRAFT 计划，含 BIL-139（carried_from_item_id）、BIL-143、1 条 KR 建议项；全部带 goal_id
```

```
AC-10-10  [integration]  BR-10-13, P2
Given 计划含一项未填 goal_id，另一项 kr_id 属于 Bo 的目标
When  Ada 提交计划
Then  409 180430，issues=[B3_UNLINKED_PLAN_ITEM, B5_FOREIGN_KR]；状态仍 DRAFT
```

```
AC-10-11  [integration]  P3, BR-10-21
Given Ada（Agent）计划 SUBMITTED，确认人为 Sam
When  主管 Agent Atlas 调确认 → 403 AGENT_PROHIBITED
And   Sam 确认 → CONFIRMED；收件箱 PLAN_CONFIRMATION 关闭
```

```
AC-10-12  [integration]  P6, BR-10-17
Given 计划 CONFIRMED
When  作者新增一项
Then  状态 SUBMITTED；确认人收到 PLAN_CONFIRMATION；仅修改估算不改变状态
```

```
AC-10-13  [integration]  BR-10-14
When  Ada 把不属于自己的工作项 CON-91 加入计划
Then  422 180412
```

### 10.3 报告生成与填充

```
AC-10-14  [integration]  BR-10-22/23, J1
Given 团队时区 Asia/Shanghai，week_report_at=MON 09:00
When  2026-10-12（周一）09:00 J1 运行（两次）
Then  为每个应报告主体生成 1 份 2026-10-05~10-11 周报（第二次不重复）；项目周报 links 指向成员周报；团队周报 links 指向项目周报
```

```
AC-10-15  [integration]  BR-10-25, X3
Given 本周 Ada：BIL-137 ACCEPTED（裁决 D-1），BIL-142 NEEDS_HUMAN_REVIEW（验证 4/4 通过），BIL-139 REWORK
When  生成 Ada 周报
Then  completed 只含 BIL-137（fact_ref 指向工作项与 D-1）；BIL-142 在 uncompleted（detail「已验证，待裁决」）；BIL-139 在 uncompleted（reason QUALITY_REWORK）
```

```
AC-10-16  [integration]  BR-10-25, BR-10-29
Given 人类成员阿澈的周报复盘为 AI 草稿（retrospective_ai_drafted=true）
When  阿澈不编辑直接提交
Then  409 180430 含 B4_MISSING_SECTION（AI 草稿未确认）；逐条确认后可提交
```

```
AC-10-17  [integration]  BR-10-26
Given Ada 本周 5 份日报已提交，周内另有周六完成的 BIL-150（无日报）
When  生成周报
Then  completed = 日报 completed 去重并集 + BIL-150；summary 计数由系统事实重算
```

### 10.4 核对与提交

```
AC-10-18  [integration]  B1
Given Ada 周报 completed 手工添加 BIL-142（实际 NEEDS_HUMAN_REVIEW）
When  核对
Then  BLOCKED；issue B1_UNBACKED_COMPLETION，expected「ACCEPTED」，actual「待裁决」，fact_ref BIL-142；提交 409 180430
```

```
AC-10-19  [integration]  B2
Given 项目周报 summary.accepted_work_items=9，成员周报合计 7，系统事实 7
When  核对
Then  B2_AGGREGATE_MISMATCH（期望 7 实际 9）；禁止提交
```

```
AC-10-20  [integration]  B4
When  报告 uncompleted 条目缺 reason
Then  B4_MISSING_SECTION 定位到该条目 key
```

```
AC-10-21  [integration]  N1, BR-10-32
Given 项目周报 5 名成员中 3 名已提交
When  项目负责人核对并提交
Then  PASS_WITH_FLAGS；报告 SUBMITTED，flags 含 UNCHECKED，UI「未核对 2/5」；团队周报继承该 flag 并显示覆盖率
```

```
AC-10-22  [integration]  N2
Given Ada 本周计划 SUBMITTED 未确认
When  Ada 周报提交
Then  允许提交，flags 含 UNCONFIRMED（「未确认」）
```

```
AC-10-23  [integration]  R5, BR-10-35
Given Ada 周报 READY（核对通过）
When  BIL-137 被 02 T26 重开（ACCEPTED → REWORK），随后 Ada 提交
Then  J6 已清 last_check_hash 并加 NEEDS_RECHECK；提交 409 180432；重核后 BIL-137 出现 B1，需移到 uncompleted
```

```
AC-10-24  [integration]  R5
Given 两个标签页同时编辑同一报告
When  A 保存后 B 以旧 expected_content_hash 提交
Then  409 180431
```

```
AC-10-25  [integration]  BR-10-34
When  同一报告并发触发 3 次核对
Then  仅运行 1 次；三个请求得到同一 check_id
```

```
AC-10-26  [integration]  180503
Given 02 WorkItemReader 不可用
When  核对
Then  503 180503；报告不变为 READY（fail-closed）
```

### 10.5 确认、退回、逾期

```
AC-10-27  [integration]  BR-10-20, BR-10-21
Given Echo（主管 Atlas 创建，负责人类 Sam）周报 SUBMITTED
When  Atlas 预审评论 → 200，状态仍 SUBMITTED；Atlas 确认 → 403 AGENT_PROHIBITED；Sam 确认 → CONFIRMED
```

```
AC-10-28  [integration]  BR-10-20
Given Morgan 既是作者又是计算出的确认人
Then  确认人改为 Morgan 的上级（所有者 Sam）；单人团队且未开启 governance_sod_relax → Morgan 确认 403 SELF_REVIEW
```

```
AC-10-29  [integration]  R8, BR-10-28
Given 项目周报已 SUBMITTED，其下 Ada 周报被退回
Then  项目周报 child_submitted−1，flags + NEEDS_RECHECK，作者与确认人收到 N-10-09
```

```
AC-10-30  [integration]  BR-10-38, J3
Given 周报 due_at=周二 12:00 未提交
When  12:10 J3 运行；次日 12:10 再运行
Then  flags + OVERDUE，report.overdue，作者与上级收到 N-10-05；共 2 次通知；之后提交 late=true，ON_TIME_REPORT_RATE 计为迟交
```

```
AC-10-31  [integration]  BR-10-40
Given 报告 CONFIRMED
When  作者编辑 → 409 180433；确认人 REOPEN（理由）→ RETURNED，审计 planning.report_reopened
```

```
AC-10-32  [integration]  BR-10-37
Given Agent Gale 已 STOPPED（孤儿），本周报告未提交
When  负责人类 Sam 代为提交
Then  submitted_by=Sam，审计 planning.report_submitted_on_behalf，detail.on_behalf_of=gale
```

### 10.6 汇总与时区

```
AC-10-33  [integration]  BR-10-26
Given 一周跨 10 月 / 11 月（10-29 ~ 11-04）
When  生成 10 月月报
Then  只包含 10-29 ~ 10-31 的事实；该周周报同时 link 到 10 月与 11 月月报
```

```
AC-10-34  [integration]  BR-10-43
When  在周三把时区从 Asia/Shanghai 改为 Europe/Berlin
Then  已生成的本周日报不变；下一周期边界按新时区计算
```

### 10.7 知识回流（v1.1）

```
AC-10-35  [integration]  BR-10-41
Given Ada 周报 CONFIRMED，improvements 一条 propose_memory=true
Then  06 MemoryWriter.Propose(kind=EXPERIENCE, scope=AGENT, source=report_id) 被调用一次；无 RULE 生成
```

---

### 10.8 D112 / D113 增补验收标准

**AC-10-36**（unit）B6 阻断 — Given 周报计划 5 项（等权），截至提交应完成 4 项（expected 0.80），系统事实完成 2 项（computed 0.40，lag 0.40），声明 GREEN、无说明 When 核对 Then 出现 B6，提交返回阻断；补写 ≥20 字说明后再核对 → 无 B6、出现 N5，提交成功且 flags 含 `STATUS_EXPLAINED`。
**AC-10-37**（unit）阈值边界 — lag 0.25、GREEN → 无 B6；lag 0.30、GREEN → B6；lag 0.40、YELLOW → 无 B6。
**AC-10-38**（unit）B7 — 上期 GREEN，本期 RED、改进计划为空 → B7；补 1 项含负责人与期限 → 通过；改进计划某项缺期限 → 422 `180461`。
**AC-10-39**（integration）N6 — 项目周报声明 GREEN，下级 3 份已提交中 1 份 RED → flag `ABOVE_CHILDREN`，可提交；上级视图显示「状态高于下级」。
**AC-10-40**（integration）质量指标 — 07 返回本周穿透率 10.0%、内部一次通过率 70.0%、平均内部轮次 1.55 → 团队周报 `quality_metrics` 与 `completed` 摘要一致；作者删除摘要中的事实数字后再核对 → B2 阻断。
**AC-10-41**（unit）客户端伪造进度 — 提交请求携带 `computed_progress=0.95` → 被忽略，核对使用系统重算值。
**AC-10-42**（integration）Agent 成员 — Agent A 起草周报声明 GREEN 且 lag 0.35 → B6 阻断；A 不能自行提交越过阻断；其负责人修改为 YELLOW 后可提交。

## 11. 度量与埋点

| 指标 | 定义 |
|---|---|
| 目标对齐覆盖率 | 团队 KR 中至少有一个激活子目标的比例 |
| 计划确认率 | 周期内已确认计划 / 应有计划 |
| 按时提交率 | 截止前提交的报告 / 应提交报告（`ON_TIME_REPORT_RATE`，可被 KR 引用） |
| 核对一次通过率 | 首次核对 PASS 或 PASS_WITH_FLAGS 的报告比例 |
| 阻断问题分布 | 按 B1–B5 计数（B1 高 → 伪装完成倾向） |
| 汇总覆盖率 | 汇总报告提交时 `child_submitted / child_total` 的分布 |
| 人工编辑率 | 人类成员提交前修改 AI 草稿复盘的比例 |

埋点：`goal_activated{level}`、`plan_submitted{subject_kind,items}`、`plan_confirmed{latency_h}`、`report_generated{level,period_kind}`、`report_checked{result,blocking,non_blocking,duration_ms}`、`report_submitted{late,flags}`、`report_confirmed{latency_h}`、`report_returned`。不采集报告正文。

---

- **D112 / D113 埋点**：`report_confidence_declared{level, kind, confidence}`、`watermelon_blocked{lag_bucket}`、`watermelon_explained`、`confidence_drop{from, to, plan_items}`。

## 12. 依赖、风险与待决

### 12.1 依赖

| 依赖 | 内容 |
|---|---|
| 02 | `WorkItemReader.LoadByPrincipal`、新增 `ListByAssigneeInPeriod`；`DecisionReader.Get`；`work_item.state_changed`、`decision.made` |
| 05 | `AgentDirectory` 返回 `owner_principal_id`、`supervisor_agent_id`、`origin`；可选指令 `agent.plan_draft` / `agent.report_draft` |
| 07 | `DeliveryMetricsReader`（指标口径以 07 §5.8 为准）、`ProjectReader.HealthList`、交付 / 验收读取 |
| 08 | `AuthorityChecker`；收件箱新增 4 类事项；通知目录新增 N-10-01..10；确认类动作加入「仅限人类」 |
| 09 | JobQueue（定时任务）、outbox、SSE、Notifier、菜单贡献 |
| 06 | `MemoryWriter.Propose`（v1.1） |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 报告变成形式主义、负担过重 | 自动填充 + AI 起草；日报对人类默认可选；只要求确认与补充 |
| 伪装完成 | B1 以系统事实为准；完成必须有证据 |
| 核对过严导致无法提交 | 只有 B1–B5 阻断；其他问题仅标记 |
| 上下级关系不清 | BR-10-19 有确定的回退顺序；v1.1 支持按成员覆盖 |
| 指标读取不稳定 | AUTO KR 保留旧值；核对依赖不可用时 fail-closed 且明确提示 |

### 12.3 待决（附建议）

| # | 问题 | 建议 |
|---|---|---|
| Q-10-1 | 人类成员是否强制日报 | 默认不强制（`required_member_kinds` 不含 DAY），Agent 默认强制 |
| Q-10-2 | 报告是否参与绩效 | 不参与（非目标），仅用于对齐与复盘 |
| Q-10-3 | 上下级是否需要独立的组织架构 | MVP 用 BR-10-19 推导；v1.1 复用底座部门树 |
| Q-10-4 | 目标是否允许跨项目的成员目标 | 允许挂到 TEAM 目标（`project_id` 为空） |

---

## 13. 契约变更请求（D111，供 00 v1.9）

> 状态：CR-10-01..10 **已合并**（00 v1.9；02 `WorkItemReader.ListByAssigneeInPeriod`；05 `owner_principal_id`、`AgentDraftRequester`、`agent.plan_draft` / `agent.report_draft`；08 §3.2 / §3.3 / §6.10 / §8.6）。J1 / J2 起草 Agent 成员计划与复盘时调用 05 `AgentDraftRequester.Request`，返回 `ErrDrafterUnavailable` 时用系统模板。

| # | 位置 | 变更 |
|---|---|---|
| CR-10-01 | §1 | 新增规格 `10-goals-reports.md`，后端模块 `planning`，页面：目标树、目标详情、我的计划、报告编辑器、报告汇总、团队报告中心 |
| CR-10-02 | §3 / §9 | 表前缀 `planning_`；错误码段 `180000–184999`（platform 段之后） |
| CR-10-03 | §5 | 共享枚举：`GoalLevel`、`GoalStatus`、`PeriodKind`、`ReportLevel`、`ReportState`、`CheckSeverity`、`KRSource` |
| CR-10-04 | §6.1 | 权限码：`goal_read`、`goal_manage`、`goal_member_manage`、`plan_submit`、`plan_confirm`、`report_read`、`report_submit`、`report_confirm`、`planning_settings_manage`；§6.2 默认角色：所有者全部；交付负责人除 `planning_settings_manage` 外全部；工程师 `goal_read`、`goal_member_manage`（本人）、`plan_submit`、`report_read`（本人与项目）、`report_submit`；各 Agent 角色 `goal_read`、`plan_submit`、`report_submit`（仅本人） |
| CR-10-05 | §6.5 | 仅限人类：`plan_confirm`、`report_confirm`、目标激活（Agent 的 MEMBER 目标）与关闭 / 取消 |
| CR-10-06 | §7.1 | 事件：`goal.created`、`goal.updated`、`goal.status_changed`、`plan.submitted`、`plan.confirmed`、`plan.returned`、`report.generated`、`report.checked`、`report.submitted`、`report.confirmed`、`report.returned`、`report.overdue`（发布者 planning；消费者 governance、collab、knowledge） |
| CR-10-07 | §8 | 02 新增 `WorkItemReader.ListByAssigneeInPeriod`；本模块提供 `GoalReader`、`ReportReader` |
| CR-10-08 | §8 / 05 | 05 机器通道可选指令 `agent.plan_draft`、`agent.report_draft`（Agent 自撰计划与复盘） |
| CR-10-09 | 08 | 收件箱类型 `PLAN_CONFIRMATION`、`REPORT_CONFIRMATION`、`REPORT_BLOCKED`、`REPORT_OVERDUE`；通知目录 N-10-01..10；控制台外壳新增「目标与报告」分组 |
| CR-10-10 | 05 | `AgentDirectory` 视图增加 `owner_principal_id`（主管创建的 Agent = 编制授权授予人） |

> **v2.0（D112 / D113，00 §15）已并入本规格**：声明状态信心与计算进度（§3.14、BR-10-48）、核对项 B6 / B7 / N5 / N6（BR-10-49）、信心必填（BR-10-50）、质量指标填充（BR-10-51）、错误码 180460–180462、页面增补（§8.7）、AC-10-36…42。依赖：07 `DeliveryMetricsReader` 需提供穿透率等三项指标（07 BR-07-61 已定义）。
