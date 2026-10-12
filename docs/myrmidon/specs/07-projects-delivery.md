# 07 · 项目、交付包与甲方验收（`delivery` 模块 · 第二部分）

> 上位文档：`PRD.md` §2.2、§2.3、§4、§6.2、§6.7、§8.4、§9.3、§12（项目 / 交付）、§15；共同契约 `00-conventions.md`（本文不重定义其中任何枚举、权限码、事件、错误码段，所需变更见 §13）。
> 标记约定：「本规格提议」= PRD 与 00 未规定、由本规格补齐的设计，后附理由；评审可推翻。

---

## 1. 范围与目标

**解决**：
1. 项目 = 底座 Entity（`type='project'`，可选父实体 `type='client'` 做分组）；`delivery` 模块持有项目档案（阶段、团队模式、预算、主管 Agent 引用、派单策略、甲方联系人引用、代码仓库、门户可见性、验收策略）。
2. 项目创建：实体 + 档案 + 默认频道（经 collab）+ 默认角色绑定，**单事务原子**；事务外的后续步骤幂等可补偿。
3. `ProjectStage` 状态机：每次迁移是授权决策，含进入 / 退出条件；BLOCKED / CANCELLED 规则；未经甲方验收不得进入 DELIVERED。
4. 团队模式 HYBRID ↔ AUTONOMOUS：前置条件、接管语义。
5. 预算：执行成本聚合（幂等记账）、80% 通知 / 100% 暂停、上限调整权限。
6. 交付包：只收 ACCEPTED 工作项；证据包（manifest JSON + 选中证据文件 + 哈希 + 脱敏）；发布说明（Agent 起草、人类定稿）；`DeliveryState` 状态机；DELIVER 裁决（不可委托 + step-up）；经来源开口发送（01 接口）。
7. 甲方验收：逐项 ACCEPTED / REJECTED（汇总为 ACCEPTED / REJECTED / PARTIALLY_ACCEPTED）、回执签名、超时提醒；拒收 → 返工来件（01）与工作项重开规则（02）。
8. 指标：每个已接受交付物成本、首次验收率、人工关注分钟、返工周期、证据覆盖率、甲方一次验收率 —— 精确公式、时间窗、日快照表；为 08 总览供数。
9. 页面：项目列表、新建项目向导、项目总览、项目设置、交付列表、交付包编辑器、交付详情（验收时间线）、指标页。

**不解决（边界）**：
| 内容 | 归属 |
|---|---|
| 来件、澄清、门户外壳与访问码、邮件 / API / Webhook 发送与接收实现、返工来件的创建与分诊 | 01 |
| 需求基线、验收标准、工作项状态机、Decision 表与裁决流程、范围变更 | 02（本规格只调用其内部函数与字段） |
| 执行、成本产生、证据内容与秘密扫描、验证 | 03 |
| 机器上的工作目录路径（按机器 × 项目仓库） | 04 |
| 团队编排卡片的 UI 与主管 Agent / 派单语义、频道 | 05 |
| 知识库项目 scope 初始化 | 06 |
| 委托、升级规则、接管、审计视图、通知收件箱、总览页布局 | 08 |
| 任务队列、outbox、SSE、对象存储、通知渠道 | 09 |

---

## 2. 用户故事

| 编号 | 故事 | 期 |
|---|---|---|
| US-07-01 | 作为交付负责人，我要用向导新建项目（名称、代号、甲方分组、联系人、成员、仓库、预算），以便一次得到可用的项目、频道与权限 | MVP |
| US-07-02 | 作为交付负责人，我要在项目卡片上看到阶段、模式、预算环与健康度，以便早上一眼看出哪个项目不健康 | MVP |
| US-07-03 | 作为交付负责人，我要按阶段推进项目，并在不满足条件时看到缺什么，以便阶段反映真实进展而不是任务计数 | MVP |
| US-07-04 | 作为交付负责人，我要把项目标记为阻塞并写明原因，以便门户与团队都知道在等什么 | MVP |
| US-07-05 | 作为团队所有者，我要在满足委托、升级规则、预算前置条件后把项目切换为全自治，以便周末无人值守推进 | MVP |
| US-07-06 | 作为团队所有者，我要一键接管全自治项目，以便立刻收回所有 Agent 裁决权 | MVP |
| US-07-07 | 作为团队所有者，我要设置预算上限，在 80% 收到通知、100% 自动暂停，以便成本不失控 | MVP |
| US-07-08 | 作为交付负责人，我要从已接受工作项组装交付包、挑选证据并生成带哈希的证据包，以便甲方可追溯验收 | MVP |
| US-07-09 | 作为交付负责人，我要让 Agent 起草发布说明、我来定稿，以便省时又保证对外措辞由人负责 | MVP |
| US-07-10 | 作为交付负责人，我要在重新认证后正式交付，以便对外交付永远是有权限的人做出的决定 | MVP |
| US-07-11 | 作为甲方联系人，我要逐项接受或拒绝并写原因，以便只对我确认的部分签收 | MVP |
| US-07-12 | 作为交付负责人，我要在甲方拒收后自动得到关联的返工来件，以便返工有据可查 | MVP |
| US-07-13 | 作为交付负责人，我要在甲方逾期未回执时自动提醒并在第 14 天升级给我，以便验收不被遗忘 | MVP |
| US-07-14 | 作为交付负责人，我要登记甲方线下确认的验收（附凭证），以便覆盖甲方不使用门户的情况 | MVP |
| US-07-15 | 作为团队所有者，我要看到每个已接受交付物成本、首次验收率、人工关注分钟等指标及其公式，以便评估交付经济性 | MVP |
| US-07-16 | 作为交付负责人，我要关闭已交付项目，系统自动收回委托、归档频道，以便项目干净收尾 | MVP |
| US-07-17 | 作为团队所有者，我要配置门户可见性（是否展示 AI 协作标签、证据摘要），以便控制对外透明度 | MVP |
| US-07-18 | 作为团队所有者，我要在合同约定"逾期视同验收"时启用视同验收 | v1.1 |
| US-07-19 | 作为团队所有者，我要按租户时区切日计算指标 | v1.1 |

---

### 2.x D112 / D113 增补用户故事（v2.0，00 §15）

| ID | 用户故事 | 版本 |
|---|---|---|
| US-07-20 | 作为团队所有者，我要把项目切到「纯人工 + AI 协助」（`HUMAN_ONLY`），以便纯人类团队也能用同一套交付闭环，Agent 只起草不执行 | MVP |
| US-07-21 | 作为交付负责人，我要为项目维护一份评审过的「项目宪法」，每次派发都自动注入，以便所有执行者遵守同一套命令、约束与禁区 | MVP |
| US-07-22 | 作为交付负责人，我要给项目与需求设定时间 / 成本 / 范围预算与容差，按预测越界时收到例外报告并做出决定，以便杜绝静默延期 | MVP |
| US-07-23 | 作为团队所有者，我要看到穿透率、内部一次通过率与平均内部轮次，以便判断内部质量门是否可靠 | MVP |

## 3. 领域模型与数据表

### 3.1 实体关系

```
iam_entities(type=client) 0..1 ─┐ parent
iam_entities(type=project) 1 ─── 1 delivery_projects ─┬─ * delivery_project_contacts ──> intake_contact_id (01)
                                                      ├─ * delivery_project_repositories ─(04 按机器存工作目录)
                                                      ├─ * delivery_project_stage_transitions
                                                      ├─ * delivery_project_mode_changes
                                                      ├─ * delivery_project_cost_entries ── delivery_execution_cost_cursors
                                                      ├─ * delivery_project_budget_events
                                                      ├─ * delivery_metric_daily
                                                      └─ * delivery_deliveries ─┬─ * delivery_delivery_items ──> work_item (02)
                                                                                ├─ * delivery_evidence_selections ──> evidence (03)
                                                                                ├─ * delivery_release_note_drafts
                                                                                ├─ * delivery_delivery_sends
                                                                                ├─ 0..1 delivery_acceptances ─ * delivery_acceptance_items
                                                                                └─ * delivery_acceptance_reminders
```

- 项目名称存于 `iam_entities.name`（唯一性由底座 `uq_iam_entities_root_name / child_name` 保证：同父同类型不重名）；`iam_entities.metadata` 只写 `{"module":"delivery"}`，**不复制档案字段**（档案是唯一真相源，避免双写漂移）。
- 实体 `status` 保持 `active`，项目关闭 / 取消也不改为 `disabled`：底座对 disabled 实体的数据约束会让历史只读访问失效；只读由本模块在写路径强制（BR-07-05）。
- 底座需补齐（见 §12.1 依赖与 §13）：`project` 列为**保留实体类型**，通用 `/entities` 接口不得创建、改类型、改父、改名或删除该类型实体；`client` 不保留（可在 IAM 实体页管理）。
- client 实体上的实体级角色绑定按底座 `Ancestors` 语义对其下项目生效（以 `ExplainEntity` 实测为准，列入 AC-07-30）。

### 3.2 方言映射（全表通用）

| 逻辑类型 | PostgreSQL | MySQL 8.0.16+ | SQLite |
|---|---|---|---|
| ID | `BIGINT` | `BIGINT` | `INTEGER` |
| 枚举 / 代码 | `VARCHAR(n) COLLATE "C"` + `CHECK` | `VARCHAR(n) CHARACTER SET ascii COLLATE ascii_bin` + `CHECK` | `TEXT` + `CHECK` |
| 短文本 ≤2000 | `VARCHAR(2000)` | `VARCHAR(2000)` utf8mb4 | `TEXT` + `CHECK(length(x)<=2000)` |
| 长文本 ≤64KB | `TEXT` + `CHECK(octet_length(x)<=65536)` | `MEDIUMTEXT`（`TEXT` 上限 65535 字节，含多字节字符时不足）+ 代码校验 | `TEXT` |
| JSON | `JSONB` | `JSON` | `TEXT` + `CHECK(json_valid(x))` |
| 时间 | `TIMESTAMPTZ(3)` | `DATETIME(3)`（禁止 `TIMESTAMP`） | `DATETIME` |
| 日期 | `DATE` | `DATE` | `TEXT`（`YYYY-MM-DD`，`CHECK(length(x)=10)`） |
| 布尔 | `BOOLEAN` | `TINYINT(1)` | `INTEGER CHECK(x IN (0,1))` |
| SHA-256 | `CHAR(64) COLLATE "C"` | `CHAR(64) CHARACTER SET ascii` | `TEXT CHECK(length(x)=64)` |
| 金额 | `BIGINT`（`*_micros`） | `BIGINT` | `INTEGER` |
| 部分唯一索引 | `CREATE UNIQUE INDEX … WHERE …` | 不支持：用可空"锁列"+普通唯一索引（多 NULL 不冲突） | 支持 `WHERE` |

所有表：`tenant_id BIGINT NOT NULL`；审计列 `created_at, updated_at`（时间）、`created_by, updated_by`（BIGINT 可空 = 系统）；对象名带 `{{.Prefix}}`；goose 版本表 `goose_delivery`（与 02 共用，迁移编号顺延）。下表省略这些公共列，仅在有特殊约束时列出。

### 3.3 `delivery_projects`（项目档案，主键 = 实体 id）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| tenant_id | ID | N | — | PK(tenant_id, project_id) |
| project_id | ID | N | — | = `iam_entities.id`（type=project）；不建跨模块外键 |
| code | VARCHAR(16) | N | — | `^[A-Z][A-Z0-9-]{1,15}$`；租户内唯一 `uq_delivery_projects_code(tenant_id, code)`；用于交付编号与返工来件标题；创建后不可改（本规格提议：编号被外部引用） |
| description | 短文本 | Y | NULL | 项目简介 ≤2000 |
| client_entity_id | ID | Y | NULL | 冗余父实体 id（type=client），便于列表分组；与实体 parent_id 同事务维护 |
| stage | VARCHAR(32) | N | `INTAKE` | `ProjectStage` CHECK |
| stage_changed_at | 时间 | N | — | |
| blocked_kind | VARCHAR(32) | Y | NULL | 仅 stage=BLOCKED：`CLIENT_CLARIFICATION`/`DEPENDENCY`/`EXTERNAL`/`OTHER`（本规格局部枚举，见 CCR-07-04） |
| blocked_reason | 短文本 | Y | NULL | stage=BLOCKED 时必填（CHECK：stage<>'BLOCKED' OR blocked_reason IS NOT NULL） |
| team_mode | VARCHAR(32) | N | `HYBRID` | `TeamMode` |
| team_mode_changed_at | 时间 | Y | NULL | |
| lead_agent_id | ID | Y | NULL | fleet Agent id（05 语义）；须为 ACTIVE 且具 LEAD 角色 |
| dispatch_policy | VARCHAR(32) | N | `MANUAL` | `DispatchPolicy`；切 AUTONOMOUS 时若 `dispatch_policy_explicit=false` 自动置 `AUTO`（PRD Q10） |
| dispatch_policy_explicit | 布尔 | N | false | 用户是否显式设置过 |
| ensemble_max_branches | INT | N | 3 | 1–5（PRD §10.6 默认 3） |
| origin_intake_id | ID | Y | NULL | 创建来源来件（01） |
| default_send_channel_id | ID | Y | NULL | 默认发送开口（01 `intake_channels.id`）；空 = 用主联系人的来源开口 |
| budget_cap_micros | BIGINT | Y | NULL | NULL = 不限；>0；AUTONOMOUS 下不得为 NULL |
| budget_cap_version | INT | N | 1 | 每次改上限 +1（阈值去重键的一部分） |
| budget_spent_micros | BIGINT | N | 0 | ≥0；只经记账函数修改 |
| budget_notify_pct | INT | N | 80 | 50–95（本规格提议可调；100% 暂停不可调） |
| budget_state | VARCHAR(32) | N | `NORMAL` | `NORMAL`/`NOTIFIED`/`EXHAUSTED`（局部枚举，CCR-07-04） |
| holds | JSON | N | `[]` | 暂停原因集合，Schema 见下 |
| health | VARCHAR(16) | N | `GREEN` | `GREEN`/`AMBER`/`RED`/`NONE`（终态） |
| health_reasons | JSON | N | `[]` | 风险 kind 数组（§5 BR-07-40） |
| health_refreshed_at | 时间 | Y | NULL | |
| portal_settings | JSON | N | 见 Schema | 门户可见性 |
| acceptance_policy | JSON | N | 见 Schema | 验收提醒与升级 |
| next_delivery_seq | INT | N | 1 | 交付编号分配（行锁内自增） |
| target_date | 日期 | Y | NULL | 目标交付日 |
| started_at / delivered_at / closed_at / cancelled_at | 时间 | Y | NULL | 首次进入 ACTIVE / DELIVERED / CLOSED / CANCELLED |
| cancel_reason | 短文本 | Y | NULL | |
| provisioning_issues | JSON | N | `[]` | 事务外步骤失败记录：`[{step, event_id, error_code, at}]` |
| archived_at | 时间 | Y | NULL | 列表隐藏；只允许终态项目归档 |
| version | INT | N | 1 | 乐观锁 |

索引：`idx_delivery_projects_list(tenant_id, archived_at, stage, health)`、`idx_delivery_projects_client(tenant_id, client_entity_id)`、`idx_delivery_projects_lead(tenant_id, lead_agent_id)`。

JSON Schema：
```json
// holds
{"type":"array","maxItems":4,"items":{"type":"object","required":["kind","since"],
 "properties":{"kind":{"enum":["BUDGET","MANUAL","ESCALATION","AUTONOMY_PRECONDITION"]},
  "since":{"type":"string","format":"date-time"},"ref":{"type":"string","maxLength":64},
  "reason":{"type":"string","maxLength":2000}},"additionalProperties":false},"uniqueItems":true}
// portal_settings（默认值即示例）
{"show_ai_label":true,"show_work_item_titles":true,"show_evidence_summary":true,
 "show_release_notes":true,"contributor_display":"TEAM_ONLY","milestones_visible":true}
// contributor_display ∈ TEAM_ONLY（只显示团队品牌）| ROLES（显示"工程师 / AI 成员"角色，不显示姓名与 Agent 名）
// acceptance_policy
{"due_days":14,"reminder_days":[3,7,12],"escalate_day":14,"deemed_acceptance_days":null}
// due_days 1–60；reminder_days 升序、≤5 项、每项 < escalate_day；deemed_acceptance_days MVP 必须为 null（v1.1）
```

### 3.4 `delivery_project_contacts`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| tenant_id, project_id | ID | N | — | |
| intake_contact_id | ID | N | — | 01 来件联系人；PK(tenant_id, project_id, intake_contact_id) |
| role | VARCHAR(32) | N | `CC` | `PRIMARY`/`CC`（局部枚举）；每项目恰好 0 或 1 个 PRIMARY：PG/SQLite 部分唯一 `WHERE role='PRIMARY'`，MySQL 用 `primary_lock TINYINT NULL`（PRIMARY 时 =1）+ 唯一(tenant_id, project_id, primary_lock) |
| receives_deliveries | 布尔 | N | true | 交付通知收件人 |
| can_accept | 布尔 | N | true | 是否有权签收（PRIMARY 强制 true） |

### 3.5 `delivery_project_repositories`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK(tenant_id, id) |
| project_id | ID | N | — | |
| name | VARCHAR(100) | N | — | 项目内唯一 |
| remote_url | VARCHAR(2000) | N | — | `https://`、`ssh://`、`git@host:path` 三种形态；**不得含凭证**（含 `user:pass@` 拒绝 127041） |
| default_branch | VARCHAR(200) | N | `main` | |
| protected_branches | JSON | N | `["main"]` | ≤20 项；03 的 `code_merge` 用 |
| is_primary | 布尔 | N | false | 每项目 ≤1 个（同 3.4 的部分唯一 / 锁列写法） |
| archived_at | 时间 | Y | NULL | |
| version | INT | N | 1 | |
> 每台机器上的本地路径归 04（`(machine_id, project_id, repository_id) → local_path`），本表不存路径。

### 3.6 `delivery_project_stage_transitions`（只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK(tenant_id, id) |
| project_id | ID | N | |
| from_stage, to_stage | VARCHAR(32) | N | `ProjectStage` |
| actor_kind | VARCHAR(16) | N | `HUMAN`/`AGENT`/`SYSTEM` |
| actor_id | ID | Y | |
| authority_source | VARCHAR(128) | N | 00 §6.3 |
| reason | 短文本 | Y | BLOCKED / 回退 / CANCELLED 必填（≥10 字符） |
| blocked_kind | VARCHAR(32) | Y | |
| criteria_snapshot | JSON | N | 迁移时的条件评估结果 `[{key, passed, detail}]` |
| scope_change_id | ID | Y | DELIVERED→ACTIVE 必填（02 ChangeRecord） |
| decision_id | ID | Y | CANCELLED 时对应 02 Decision(action=CANCEL) |
| occurred_at | 时间 | N | |
索引：`idx_delivery_stage_transitions_project(tenant_id, project_id, occurred_at)`。无 updated_* / version（不可变）。

### 3.7 `delivery_project_mode_changes`（只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id | ID | N | |
| from_mode, to_mode | VARCHAR(32) | N | `TeamMode` |
| trigger | VARCHAR(32) | N | `MANUAL`/`TAKEOVER`（局部枚举） |
| actor_kind / actor_id / authority_source | — | N/Y/N | 同 3.6 |
| reason | 短文本 | N | ≥10 字符 |
| preconditions_snapshot | JSON | N | `[{key, passed, detail}]`（切 AUTONOMOUS 时全部 passed） |
| revoked_delegation_ids | JSON | N | 切回 HYBRID 时由 08 回填（接管） |
| occurred_at | 时间 | N | |

### 3.8 预算记账：`delivery_execution_cost_cursors` 与 `delivery_project_cost_entries`

`delivery_execution_cost_cursors`（每个执行一行，记账幂等的锚）：
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| tenant_id, execution_id | ID | N | PK |
| project_id | ID | N | |
| work_item_id | ID | Y | ADHOC 执行为空 |
| cumulative_cost_micros | BIGINT | N | 已入账的最大累计值 |
| last_event_id | VARCHAR(64) | N | |
| updated_at | 时间 | N | |

`delivery_project_cost_entries`（增量流水，只追加）：
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK(tenant_id, id) |
| project_id, execution_id | ID | N | |
| work_item_id | ID | Y | |
| event_id | VARCHAR(64) | N | 唯一 `uq_delivery_cost_entries_event(tenant_id, event_id)` |
| delta_micros | BIGINT | N | >0 |
| model_micros, compute_micros, platform_micros | BIGINT | N | 增量分解，三者之和 = delta |
| cumulative_after | BIGINT | N | |
| occurred_at | 时间 | N | 成本发生时间（取事件 `occurred_at`），指标按此切日 |
索引：`idx_delivery_cost_entries_day(tenant_id, project_id, occurred_at)`。

### 3.9 `delivery_project_budget_events`（只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id | ID | N | |
| kind | VARCHAR(32) | N | `CAP_CHANGED`/`THRESHOLD_NOTIFY`/`THRESHOLD_EXHAUSTED`/`HOLD_RELEASED` |
| cap_version | INT | N | |
| cap_before_micros, cap_after_micros | BIGINT | Y | CAP_CHANGED 用 |
| spent_micros | BIGINT | N | 事件时的已花费 |
| threshold_pct | INT | Y | 80（或配置值）/ 100 |
| reason | 短文本 | Y | CAP_CHANGED 必填 |
| actor_kind / actor_id / authority_source | — | — | |
| occurred_at | 时间 | N | |
唯一：`uq_delivery_budget_threshold(tenant_id, project_id, cap_version, kind)`，仅约束 THRESHOLD_* 两种 kind —— PG/SQLite 部分唯一 `WHERE kind LIKE 'THRESHOLD_%'`；MySQL 用 `threshold_lock VARCHAR(32) NULL`（THRESHOLD_* 时 = kind）+ 唯一(tenant_id, project_id, cap_version, threshold_lock)。**这是阈值通知 / 暂停幂等的物理保证**。

### 3.10 `delivery_deliveries`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK(tenant_id, id) |
| project_id | ID | N | — | |
| sequence_no | INT | N | — | 唯一(tenant_id, project_id, sequence_no) |
| number | VARCHAR(32) | N | — | `D-<code>-<三位序号>`，如 `D-ACME-003` |
| title | VARCHAR(200) | N | — | |
| summary | 短文本 | Y | NULL | 门户展示的"说明" |
| demo_url | VARCHAR(2000) | Y | NULL | `https://` 才允许 |
| state | VARCHAR(32) | N | `DRAFT` | `DeliveryState` |
| release_notes_md | 长文本 | Y | NULL | ≤64KB |
| release_notes_sha256 | SHA-256 | Y | NULL | |
| release_notes_actor_kind | VARCHAR(16) | Y | NULL | 最后编辑者类型；DELIVER 要求 `HUMAN` |
| release_notes_actor_id | ID | Y | NULL | |
| release_notes_draft_id | ID | Y | NULL | 采纳的 Agent 草稿 |
| bundle_state | VARCHAR(16) | N | `NONE` | `NONE`/`BUILDING`/`BUILT`/`FAILED`/`STALE`（局部枚举） |
| bundle_fingerprint | SHA-256 | Y | NULL | 构建输入指纹（BR-07-26） |
| bundle_file_id | ID | Y | NULL | files 文件 id（zip） |
| manifest_file_id | ID | Y | NULL | files 文件 id（manifest.json） |
| bundle_sha256 / manifest_sha256 | SHA-256 | Y | NULL | |
| bundle_size_bytes | BIGINT | Y | NULL | ≤ 512 MiB |
| bundle_built_at | 时间 | Y | NULL | |
| bundle_error_code | VARCHAR(64) | Y | NULL | |
| send_channel_kind | VARCHAR(32) | Y | NULL | `IntakeChannelKind`，DELIVER 时冻结 |
| send_channel_id | ID | Y | NULL | |
| send_contact_ids | JSON | N | `[]` | 收件联系人 id 数组（≤20），DELIVER 时冻结 |
| deliver_decision_id | ID | Y | NULL | 02 Decision(action=DELIVER) |
| decided_at | 时间 | Y | NULL | |
| send_attempts | INT | N | 0 | |
| send_failed | 布尔 | N | false | 重试耗尽 |
| sent_at | 时间 | Y | NULL | |
| acceptance_due_at | 时间 | Y | NULL | sent_at + due_days |
| acceptance_id | ID | Y | NULL | |
| result_at | 时间 | Y | NULL | |
| rework_of_delivery_id | ID | Y | NULL | 返工后再次交付时指向被拒交付 |
| source_intake_ids | JSON | N | `[]` | 交付项追溯到的来件 id（经 02 需求的 `source_intake_id` 去重，≤50），DELIVER 时计算并冻结；用于 `delivery.sent` / `acceptance.received` 载荷与 01 门户投影（C18） |
| confirmation_requested_at / confirmation_requested_by | 时间 / ID | Y | NULL | 最近一次「请求正式交付」（BR-07-30b）；D3 / D5 / 归档时清空 |
| archived_at | 时间 | Y | NULL | 只允许 DRAFT 归档（丢弃草稿） |
| version | INT | N | 1 | |
索引：`idx_delivery_deliveries_project(tenant_id, project_id, state, created_at)`、`idx_delivery_deliveries_due(tenant_id, state, acceptance_due_at)`（提醒任务扫描）。

### 3.11 `delivery_delivery_items`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id, delivery_id, work_item_id | ID | N | 唯一(tenant_id, delivery_id, work_item_id) |
| work_item_key / title_snapshot | VARCHAR(32) / VARCHAR(200) | N | 加入时快照，门户展示用 |
| accepted_decision_id | ID | N | 加入时该工作项最近一次 APPROVE 裁决 id（02） |
| requirement_id | ID | N | |
| requirement_version | INT | N | |
| position | INT | N | 1..200 |
| active_lock | INT | Y | 交付处于 READY/SENT 时 = 1，否则 NULL；唯一(tenant_id, work_item_id, active_lock) —— **同一工作项同时只能在一个在途交付里**（三方言统一用锁列） |
| client_result | VARCHAR(32) | Y | 验收后回填 `ACCEPTED`/`REJECTED` |

### 3.12 `delivery_evidence_selections`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id, delivery_id, delivery_item_id, evidence_id | ID | N | 唯一(tenant_id, delivery_id, evidence_id) |
| evidence_type | VARCHAR(32) | N | `EvidenceType` 快照 |
| include_mode | VARCHAR(16) | N | `SUMMARY`（只入 manifest 元数据与摘要）/`FILE`（附原文件）/`FULL_LOG`（仅 LOG，过滤后全文）（局部枚举） |
| original_sha256 | SHA-256 | N | 03 证据 content_hash |
| bundled_sha256 | SHA-256 | Y | 构建后实际入包字节的哈希 |
| redaction_count | INT | N | 默认 0 |
| excluded_reason | VARCHAR(64) | Y | 构建时被剔除的原因（`DENY_LISTED_FILE` / `SECRET_FLAGGED` / `TOO_LARGE` / `UNSUPPORTED_TYPE`） |
| path_in_bundle | VARCHAR(500) | Y | |

### 3.13 `delivery_release_note_drafts`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id, delivery_id | ID | N | |
| author_agent_id | ID | N | fleet Agent id |
| author_principal_id | ID | N | Agent 服务账号 |
| content_md | 长文本 | N | ≤64KB |
| content_sha256 | SHA-256 | N | |
| source_fingerprint | SHA-256 | N | 起草时的交付项集合指纹 |
| adopted_at | 时间 | Y | |
每交付最多保留 20 份，超出最早的标记 `archived_at`（列同 3.10）。

### 3.14 `delivery_delivery_sends`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id, delivery_id | ID | N | |
| attempt_no | INT | N | 唯一(tenant_id, delivery_id, attempt_no, contact_id) |
| contact_id | ID | N | |
| channel_kind | VARCHAR(32) | N | `IntakeChannelKind` |
| channel_id | ID | N | |
| state | VARCHAR(16) | N | `PENDING`/`SUCCEEDED`/`FAILED` |
| external_ref | VARCHAR(200) | Y | 邮件 Message-ID / webhook 投递 id / 门户通知 id |
| error_code | VARCHAR(64) | Y | 01 返回的错误码 |
| started_at / finished_at | 时间 | N/Y | |

### 3.15 `delivery_acceptances` 与 `delivery_acceptance_items`

`delivery_acceptances`（每个交付至多一份，**不可修改**）：
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| project_id, delivery_id | ID | N | 唯一(tenant_id, delivery_id) |
| result | VARCHAR(32) | N | `AcceptanceResult`，由逐项结果计算（BR-07-31） |
| source | VARCHAR(32) | N | `PORTAL`/`EMAIL`/`API`/`WEBHOOK`/`RECORDED`（CCR-07-03） |
| intake_contact_id | ID | N | 签收人（甲方联系人） |
| actor_kind | VARCHAR(16) | N | `CLIENT`（RECORDED 时仍为 CLIENT，另记登记人） |
| authority_source | VARCHAR(128) | N | `CLIENT:<intake_contact_id>` |
| signer_name | VARCHAR(200) | N | 门户须手输姓名；API 取请求字段；RECORDED 取凭证上的姓名 |
| manifest_sha256_ack | SHA-256 | N | 甲方确认时看到的 manifest 哈希，须等于交付的 manifest_sha256 |
| overall_comment | 短文本 | Y | |
| receipt_sha256 | SHA-256 | N | 回执签名（BR-07-33） |
| client_request_id | VARCHAR(64) | Y | 外部系统的幂等键；唯一(tenant_id, delivery_id, client_request_id) |
| recorded_by | ID | Y | RECORDED：登记人 principal |
| record_evidence_file_id | ID | Y | RECORDED：线下凭证文件（files，ResourceBinding purpose=`acceptance_record`） |
| rework_intake_id | ID | Y | 01 生成返工来件后回填 |
| received_at | 时间 | N | |
无 updated_* / version。

`delivery_acceptance_items`：
| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| acceptance_id, delivery_item_id, work_item_id | ID | N | 唯一(tenant_id, acceptance_id, delivery_item_id) |
| result | VARCHAR(32) | N | `ACCEPTED`/`REJECTED`（`AcceptanceResult` 子集） |
| feedback_category | VARCHAR(32) | Y | `FeedbackCategory`；REJECTED 必填（CHECK） |
| comment | 短文本 | Y | REJECTED 必填，≥10 字符 |

### 3.16 `delivery_acceptance_reminders`

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| tenant_id, delivery_id | ID | N | |
| seq | INT | N | PK(tenant_id, delivery_id, seq)；按 `reminder_days` 顺序 1..n，升级为 n+1 |
| kind | VARCHAR(16) | N | `REMINDER`/`ESCALATION` |
| due_at | 时间 | N | |
| sent_at | 时间 | Y | |

### 3.17 `delivery_metric_daily`（日快照；存分子分母，不存比率）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| tenant_id, project_id | ID | N | — | |
| day | 日期 | N | — | UTC 日；PK(tenant_id, project_id, day) |
| is_final | 布尔 | N | false | day+3 天后定稿，不再重算 |
| cost_micros | BIGINT | N | 0 | Σ cost_entries.delta（occurred_at ∈ day） |
| accepted_first_count | INT | N | 0 | 当日**首次**进入 ACCEPTED 的工作项数 |
| first_pass_count | INT | N | 0 | 其中首次验收前质量返工次数 = 0 的数量 |
| attention_ms | BIGINT | N | 0 | 当日 HUMAN 关注时长合计 |
| attention_breakdown | JSON | N | `{}` | `{"review":ms,"interaction":ms,"clarification":ms}` |
| rework_cycles | INT | N | 0 | 当日闭合的返工周期数 |
| rework_ms_sum | BIGINT | N | 0 | |
| rework_hist | JSON | N | `[0,0,0,0,0,0,0,0,0]` | 桶上界（小时）：1,4,8,24,48,72,168,336,∞ |
| claimed_items | INT | N | 0 | 当日有新完成声明的去重工作项数 |
| evidenced_items | INT | N | 0 | 其中至少 1 条可审查证据的数量 |
| criteria_total / criteria_covered | INT | N | 0 | 声明时验收标准条数 / 已映射证据的条数 |
| first_receipts | INT | N | 0 | 当日收到回执的交付数（每交付仅一份回执；口径见 BR-07-47） |
| first_receipts_accepted | INT | N | 0 | 其中 result=ACCEPTED |
| computed_at | 时间 | N | — | |
索引：`idx_delivery_metric_daily_day(tenant_id, day)`（团队汇总）。

### 3.18 幂等键

使用平台统一幂等表（09 C7 `platform_idempotency_keys`，00 §9）；本模块不建幂等表。同 key 不同请求 → 422 `176425`，进行中 → 409 `176409`。本规格的 `operation` 取值：`project.create`、`project.stage_transition`、`project.mode_change`、`project.budget_put`、`project.decision`、`delivery.create`、`delivery.decision`、`delivery.bundle`、`delivery.send`、`delivery.acceptance_record`、`delivery.release_note_draft`、`delivery.confirmation_request`、`project.create_for_intake`。

---

### 3.19 `delivery_project_constitutions`（项目宪法，D112 §15.4）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | |
| tenant_id / project_id | ID | N | | |
| version_no | INT | N | | 项目内自增，从 1 起 |
| body | TEXT | N | | Markdown，≤ 64 KB（超出 422 `127050`）；固定小节：构建与测试命令、约束、禁区、风格、目录约定 |
| body_sha256 | STR(64) | N | | 规范化后哈希；执行记录引用该值 |
| state | STR(16) | N | `DRAFT` | `DRAFT` / `IN_REVIEW` / `ACTIVE` / `SUPERSEDED` / `REJECTED`（模块内枚举 `ConstitutionState`） |
| change_summary | STR(2000) | N | | 提议说明（必填） |
| proposed_by_kind / proposed_by | STR(16) / ID | N | | 人或 Agent 均可提议 |
| reviewed_by / reviewed_at | ID / 时间 | Y | NULL | 仅人类（`project_manage`） |
| review_comment | STR(2000) | Y | NULL | 驳回必填 |
| active_key | ID | Y | NULL | `state=ACTIVE` 时 = project_id，否则 NULL；唯一索引保证每项目仅一个 ACTIVE（三方言均允许多个 NULL） |
| created_at / updated_at / version | 时间 / INT | N | | ETag |

唯一：`(project_id, version_no)`、`(active_key)`。

### 3.20 `delivery_tolerances`（项目 / 需求级预算与容差，D112 §15.4；工作项级由 02 拥有）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| subject_kind | STR(16) | N | | `PROJECT` / `REQUIREMENT` |
| subject_id | ID | N | | 项目 = entity id；需求 = 02 requirement id |
| appetite_end_at | 时间 | N | | 时间预算（承诺完成时刻） |
| appetite_cost_micros | BIGINT | Y | NULL | 成本预算（项目级缺省取预算上限） |
| appetite_scope_criteria | INT | Y | NULL | 承诺的验收标准条数（范围基线） |
| tolerance_time_pct / tolerance_cost_pct / tolerance_scope_pct | INT | N | 20 / 15 / 10 | 容差百分比，0–100 |
| forecast_end_at / forecast_cost_micros / forecast_scope_criteria | 时间 / BIGINT / INT | Y | NULL | 最近一次预测（BR-07-58） |
| forecast_at | 时间 | Y | NULL | |
| state | STR(16) | N | `OK` | `OK` / `AT_RISK` / `EXCEPTION`（模块内枚举 `ToleranceState`） |
| open_exception_id | ID | Y | NULL | 指向 §3.21 |
| baseline_no | INT | N | 1 | 每次 CONTINUE / RESHAPE 决定后 +1；甲方确认的变更经 `ToleranceAdjuster.ApplyApprovedChange` 调整后亦 +1（BR-07-62，D117） |
| version | INT | N | 1 | ETag |

唯一：`(subject_kind, subject_id)`。

### 3.21 `delivery_exceptions`（例外报告与决定，只追加；项目 / 需求级）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| tolerance_id | ID | N | | |
| breached_dimensions | JSON | N | | `["TIME","COST","SCOPE"]` 子集 |
| forecast_snapshot | JSON | N | | 越界时的预测值与基线 |
| report | JSON | Y | NULL | `{situation, options[{decision, impact, cost_delta, time_delta}], recommendation}`；由系统起草（Agent 可补充），人确认后提交 |
| state | STR(16) | N | `RAISED` | `RAISED` / `REPORTED` / `DECIDED`（模块内枚举 `ExceptionState`） |
| decision | ENUM `ExceptionDecision` | Y | NULL | `CONTINUE` / `RESHAPE` / `HOLD` / `KILL` |
| decision_payload | JSON | Y | NULL | CONTINUE：新基线；RESHAPE：02 范围变更记录 id；HOLD：恢复条件；KILL：取消理由 |
| decided_by / decided_at / decision_reason | ID / 时间 / STR(2000) | Y | NULL | 仅人类，`exception_decide`（不可委托） |
| created_at | 时间 | N | | |

### 3.17b `delivery_metric_daily` 新增列（D113 质量指标）

| 列 | 类型 | 说明 |
|---|---|---|
| l3_entered | INT | 当日首次进入 `NEEDS_HUMAN_REVIEW` 的工作项数（每项只计一次） |
| escapes_human / escapes_client | INT | 当日记录的穿透数（`quality.escaped.stage` = HUMAN_REVIEW / CLIENT_ACCEPTANCE） |
| internal_qa_completed | INT | 当日完成内部质量门（`internal_qa.passed` 首次）的工作项数 |
| internal_first_pass | INT | 其中第一次内部质量门即通过的数量 |
| internal_rounds_sum | INT | 这些工作项累计的内部轮次（AUTOMATED / AGENT_TEST / AGENT_ACCEPTANCE 任一失败 = 1 轮） |

`delivery_projects` 增列：`constitution_version_no INT NULL`（当前 ACTIVE 版本）；`team_mode` 取值增加 `HUMAN_ONLY`。

## 4. 状态机

### 4.1 ProjectStage

只有下表列出的迁移合法；所有迁移经 `POST /entities/{entity_id}/project/stage-transitions`，均为授权决策（权限 `project_stage_change`，数据范围 = 该项目），**不由工作项状态自动推导**。系统在条件满足时只产生"可推进"建议（08 收件箱 + 总览横幅），不自动迁移。

| # | 当前 | 目标 | 守卫（进入 / 退出条件，全部满足） | 主体限制 | 副作用 |
|---|---|---|---|---|---|
| S1 | INTAKE | DISCOVERY | ≥1 来件已关联本项目（origin_intake 或 01 中 project_id=本项目的 CONVERTED 来件）**或** ≥1 需求草稿（02） | 可委托规则同权限 | 事件 `project.stage_changed`；审计；门户里程碑"澄清中" |
| S2 | DISCOVERY | PLANNING | 当前需求基线版本 ≥1，且每条在册需求 ≥1 条验收标准；01 `IntakeReader.OpenClarifications(tenant, project)` = 0（本项目关联来件无 OPEN 澄清轮，C42） | 同上 | 同上 |
| S3 | PLANNING | DISCOVERY | reason 必填 | 同上 | 同上 |
| S4 | PLANNING | ACTIVE | ≥1 工作项 READY；项目内 ≥1 名具 `work_item_execute` 的成员（人或 ACTIVE Agent）；若 AUTONOMOUS 则 budget_cap 非空 | 同上 | `started_at` 首次写入；门户"进行中" |
| S5 | ACTIVE | BLOCKED | reason + blocked_kind 必填 | 同上 | 新 AUTONOMOUS 执行被闸门拒绝（BR-07-12）；Lead 自动派单暂停；门户显示"等待你的回复"（仅 blocked_kind=CLIENT_CLARIFICATION） |
| S6 | BLOCKED | ACTIVE | reason（解除说明）必填 | 同上 | 清 blocked_*；恢复派单 |
| S7 | ACTIVE | ACCEPTANCE_PENDING | ≥1 交付处于 SENT | 同上 | 门户"待验收"；可与 DELIVER 裁决同事务（BR-07-29） |
| S8 | ACCEPTANCE_PENDING | ACTIVE | reason 必填（返工） | 同上 | 门户"进行中" |
| S9 | ACCEPTANCE_PENDING | DELIVERED | 见 BR-07-08（全部可交付工作项已获甲方 ACCEPTED，无 READY/SENT 交付，无未处理返工来件） | **仅 HUMAN** | `delivered_at`；门户"已交付" |
| S10 | DELIVERED | ACTIVE | `scope_change_id` 必填（02 范围变更已生效，Requirement 版本 +1） | 同上 | 清 delivered_at 不清（保留首次） |
| S11 | DELIVERED | CLOSED | 无 CREATED/RUNNING/PAUSED 执行；所有工作项 ∈ {ACCEPTED, CANCELLED}；无 READY/SENT 交付 | **仅 HUMAN** | DRAFT 交付自动归档；清空 holds；事件触发 08 撤销项目委托、05 归档频道；health=NONE；项目只读 |
| S12 | 任意非终态（INTAKE…DELIVERED） | CANCELLED | reason ≥10 字符；step-up（purpose=`project_cancel`）；同事务写 02 Decision(action=CANCEL, target=PROJECT) | **仅 HUMAN**（不可委托） | 非终态工作项 → CANCELLED（02，authority 同本次）；DRAFT 交付归档、READY 交付撤回为 DRAFT 后归档；SENT 交付保持 SENT（回执仍可登记，停止提醒）；事件触发 03 取消执行、08 撤销委托、05 归档频道；health=NONE；项目只读 |

终态：CLOSED、CANCELLED（不可迁出）。

**非法迁移**（返回 409 `127010 delivery.errors.stage_transition_illegal`，`meta.allowed_targets` 列出当前合法目标）：表外任意组合，典型：INTAKE→PLANNING / INTAKE→ACTIVE / DISCOVERY→ACTIVE（跳级）、ACTIVE→DELIVERED、BLOCKED→ACCEPTANCE_PENDING、BLOCKED→DELIVERED、DELIVERED→ACCEPTANCE_PENDING、CLOSED→*、CANCELLED→*、X→X（同态）。
**守卫不满足**：422 `127011 stage_criteria_unmet`（`meta.criteria=[{key,passed,detail}]`）；S9 专用 422 `127013 stage_delivered_requires_acceptance`；S11 专用 422 `127014 stage_close_open_work`；理由缺失 422 `127012`；AGENT 请求 S9/S11/S12 → 403 `120403`（`data.reason_code=HUMAN_ONLY`）并审计 `delivery.project_stage_denied`。

### 4.2 TeamMode

| 当前 | 命令 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| HYBRID | 切换（`POST …/mode-changes {to_mode:AUTONOMOUS}`） | HUMAN；`project_mode_change`（不可委托）；step-up `team_mode_change`；前置 P1–P5 全过（BR-07-14） | AUTONOMOUS | 若 `dispatch_policy_explicit=false` → `AUTO`；清 AUTONOMY_PRECONDITION hold；审计 `delivery.project_mode_changed`；事件 `project.mode_changed`；通知所有者 |
| AUTONOMOUS | 切换（`to_mode:HYBRID`） | HUMAN；`project_mode_change`；**无前置条件**（收回权力永远允许）；无需 step-up | HYBRID | `dispatch_policy` → MANUAL（显式设置过也覆盖，本规格提议：自治结束后无人确认的自动派单不应保留）；事件让 08 撤销该项目全部委托；进行中执行继续，待裁决项回到人类队列 |
| AUTONOMOUS | 接管（08 `takeover` 调用 `ProjectModeSwitcher.TakeOverOn`） | 调用方已通过 08 的 `takeover` 权限与 step-up | HYBRID | 同上，trigger=TAKEOVER，`revoked_delegation_ids` 由 08 在同事务回填 |
| 任意 | 同态切换 | — | — | 409 `127020 mode_same` |
| CLOSED / CANCELLED 项目 | 任意 | — | — | 409 `127005 project_read_only` |

前置条件在 AUTONOMOUS 期间失效（委托到期、升级规则被停用）时**不自动切模式**，而是加 hold `AUTONOMY_PRECONDITION`（自动派单与委托裁决暂停）并通知所有者（BR-07-16）。

### 4.3 预算状态 `budget_state`

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| NORMAL | 记账后 spent ≥ cap × notify_pct/100 | cap 非空 | NOTIFIED | 插入 THRESHOLD_NOTIFY（唯一键去重）；事件 `budget.threshold_reached{threshold_pct:80}`；通知 |
| NORMAL/NOTIFIED | 记账后 spent ≥ cap | cap 非空 | EXHAUSTED | 插入 THRESHOLD_EXHAUSTED；加 hold BUDGET；事件 `{threshold_pct:100}`；03 暂停本项目 RUNNING 的 AUTONOMOUS 执行 |
| 任意 | 改上限（cap_version+1） | HUMAN；`project_manage` | 按新 cap 重算：<notify→NORMAL；≥notify 且 <cap→NOTIFIED（新 cap_version 再发一次通知）；≥cap→EXHAUSTED | 离开 EXHAUSTED 时移除 BUDGET hold 并写 HOLD_RELEASED；不自动恢复已暂停执行（由人逐个恢复，本规格提议：避免一次性放量） |
| 任意 | cap 置 NULL | HYBRID 才允许 | NORMAL | 同上 |

### 4.4 DeliveryState

| # | 当前 | 命令 / 事件 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| D1 | — | 创建 | `delivery_compose`；项目 stage ∈ {ACTIVE, BLOCKED, ACCEPTANCE_PENDING} | DRAFT | 分配 sequence_no |
| D2 | DRAFT | 编辑项 / 证据 / 说明 | `delivery_compose`；`If-Match` 匹配 | DRAFT | bundle_state BUILT→STALE |
| D3 | DRAFT | DELIVER 裁决 | BR-07-27 全部校验；`delivery_send`；HUMAN；step-up `delivery_send` | READY | 02 Decision(action=DELIVER)；items.active_lock=1；冻结发送渠道与联系人；入队 `delivery.send`；审计 `delivery.delivery_decided`；可选同事务 S7 |
| D4 | READY | 发送成功（全部联系人 SUCCEEDED 或 ≥1 个 PRIMARY 成功） | — | SENT | sent_at、acceptance_due_at；生成提醒行；事件 `delivery.sent`；审计 `delivery.delivery_sent` |
| D5 | READY | 撤回（`PUT …/withdrawal`） | `delivery_send`；HUMAN；尚无 SUCCEEDED 发送记录 | DRAFT | active_lock=NULL；bundle 保留（指纹仍有效）；事件 `delivery.withdrawn`（08 关闭交付确认收件箱项） |
| D6 | SENT | 甲方回执（01 `AcceptanceRecorder` 或 RECORDED） | BR-07-30..34 | ACCEPTED / REJECTED / PARTIALLY_ACCEPTED | items.client_result；active_lock=NULL；停止提醒；事件 `acceptance.received`（含 `intake_ids[]`、`accepted_work_item_ids[]`、`rejected_items[]`）；审计 `delivery.acceptance_received`；REJECTED/PARTIAL → 01 建返工来件（幂等键 `origin_acceptance_id`） |
| D7 | DRAFT | 归档 | `delivery_compose` | DRAFT（archived_at） | 列表隐藏 |

终态：ACCEPTED、REJECTED、PARTIALLY_ACCEPTED。
**非法迁移**：409 `128010 delivery_transition_illegal`。典型：DRAFT→SENT（未经裁决）、READY→ACCEPTED（未发出）、SENT→DRAFT（已发出不可撤回，专用 409 `128012 delivery_already_sent`）、终态→任意、对非 DRAFT 编辑（409 `128001 delivery_not_draft`）、对非 SENT 登记回执（409 `128030 acceptance_not_awaited`）。

---

### 4.5 TeamMode 增补（`HUMAN_ONLY`）

| 当前 | 目标 | 守卫 | 副作用 |
|---|---|---|---|
| HYBRID | HUMAN_ONLY | HUMAN；`project_mode_change`（仅所有者） | 停止向 Agent 派发执行 / 测试 / 验收 / 评审；Agent 持有的未开始分派释放回 READY；运行中的 Agent 执行允许跑完（结果仍走内部质量门，由人工核验替代 Agent 关口，BR-07-56） |
| AUTONOMOUS | HUMAN_ONLY | 同上 + step-up `team_mode_change` | 先执行接管语义（撤销全部委托、派单策略置 MANUAL），再按上一行处理 |
| HUMAN_ONLY | HYBRID | 同 HYBRID→AUTONOMOUS 以外的普通切换 | 恢复 Agent 派发 |
| HUMAN_ONLY | AUTONOMOUS | — | **非法** → 409 `127051`（须先切回 HYBRID） |

### 4.6 项目宪法 `ConstitutionState`

`DRAFT → IN_REVIEW`（提议人提交）→ `ACTIVE`（人类评审通过；原 ACTIVE → `SUPERSEDED`，同事务）/ `REJECTED`（驳回意见必填）。非法：对非 IN_REVIEW 评审 → 409 `127052`；Agent 评审 → 403 `120403`（reason `HUMAN_ONLY`）。

### 4.7 容差 `ToleranceState` 与例外 `ExceptionState`

`OK → AT_RISK`（预测超出预算但在容差内）`→ EXCEPTION`（预测超出 预算 ×（1 + 容差））；`AT_RISK → OK`（预测回落）。进入 EXCEPTION 同事务创建 `delivery_exceptions(RAISED)` 并发布 `exception.raised`。`RAISED → REPORTED`（例外报告提交）`→ DECIDED`：CONTINUE（新基线，`baseline_no+1`，容差回 OK）、RESHAPE（调 02 范围变更，基线按新范围重算）、HOLD（项目 hold `TOLERANCE_EXCEPTION`，暂停新派发，恢复条件写入 payload）、KILL（项目阶段 → CANCELLED 的既有人工流程，需 step-up）。EXCEPTION 期间修改 `appetite_end_at` / 交付截止时间 → 409 `127053`（禁止静默延期）。甲方确认的变更（D117）是 CONTINUE / RESHAPE 之外唯一的基线调整来源：`ToleranceAdjuster.ApplyApprovedChange` 只在主体无未决例外（`open_exception_id IS NULL`）时执行，调整后 `baseline_no+1` 并按新基线重算 `OK` / `AT_RISK`；有未决例外 → 409 `127055`，不改任何值（BR-07-62）。

## 5. 业务规则与边界情况

### 5.1 项目创建与档案

- **BR-07-01 原子创建**：`POST /tenants/{tenant_id}/projects` 在**一个数据库事务**内完成：①（可选）新建 client 实体 → ② 经 core `EntityProvisioner.CreateOn(tx)` 建 project 实体（底座同事务内开通实体文件根、递增策略版本、写 IAM 审计 `entity_created`）→ ③ 插 `delivery_projects` → ④ 插联系人、仓库 → ⑤ 经 core `PutRoleBindingOn(tx)` 写默认角色绑定（BR-07-02）→ ⑥ 经 collab `ChannelProvisioner.CreateDefaultOn(tx)` 建 `# <项目名>` 频道并加入绑定成员 → ⑦ 审计 `delivery.project_created` + outbox `project.created`。任一步失败整体回滚，无任何残留（AC-07-02）。三个跨模块能力任一未解析到（模块被禁用）→ 路由仍注册，请求返回 503 `127004 provisioning_unavailable`，启动时 `slog.Warn` 说明缺哪个能力（禁止静默降级）。
- **BR-07-01b 来件转换建项目**（C11）：01 在其转换事务内调 `ProjectCreator.CreateForIntakeOn(ctx, tx, {tenant_id, name, code?, origin_intake_id, primary_contact_id, actor})`，本模块在**调用方事务**内执行 BR-07-01 的 ②③⑤⑥⑦（不建 client 实体、不建仓库）：`code` 缺省时由名称自动生成（取拉丁字母 / 拼音首字母大写，冲突追加 `-2`…`-9`，仍冲突 → 422 `127001`）；`primary_contact_id` 写入 `delivery_project_contacts`（role=PRIMARY，can_accept=true）；`origin_intake_id` 回写档案；`team_mode=HYBRID`、`dispatch_policy=MANUAL`；转换主体获得项目范围 `delivery_lead` 绑定；`ChannelProvisioner.CreateDefaultOn(tx)` 建默认频道。返回 `ProjectRef{id, code, channel_id}`。新建项目不可委托（00 §6.4 `project_create`），主体须为 HUMAN，否则 403 `120403`（`reason_code=NON_DELEGABLE`）。
- **BR-07-02 默认绑定**：创建者自动获得项目范围 `delivery_lead`（已有租户级角色时仍写，保证租户级角色被收回后仍可管理该项目）；`members[]` 中人类仅可绑 `delivery_lead`/`engineer`，Agent 仅可绑 `agent_*`（按 `AgentDirectory` 判别主体类型），否则 422 `127043`；成员须为活跃租户成员。≤50 个。
- **BR-07-02b 项目成员维护**（C16）：创建后经 #46–#48 增删项目成员：`PUT /entities/{entity_id}/project/members/{principal_id} {role_key}` 经 core `EntityRoleBindingStore.PutOn(tx)` 写项目范围绑定（角色与主体类型匹配规则同 BR-07-02；`role_key` 不得为 `delegated_*` 载体角色 —— 委托只能经 08 授予，422 `127043`）；DELETE 移除绑定：若该主体是 `lead_agent_id` → 同事务置空并 `dispatch_policy=MANUAL`；若其有 ACTIVE 分派 → 422 `127048 member_has_active_assignment`（先改派）；不得移除项目最后一个 `delivery_lead`（422 `127049`）。变更发 `project.members_changed{added[], removed[]}`，05 据此更新派单候选与频道成员。
- **BR-07-03 事务外步骤（补偿）**：`project.created` 的消费者 —— 06 初始化项目知识 scope、08 套用租户默认升级规则 ——（来件与项目的关联由 01 在转换事务内经 BR-07-01b 同步写入，不经事件） 至少一次、按 `event_id` 幂等。消费者重试 8 次仍失败 → 调 `ProjectProvisioningIssueRecorder` 写 `provisioning_issues`，项目总览显示"初始化未完成：〈步骤〉〔重试〕"（重试 = 重新投递该事件，接口 #25）。这些步骤失败**不回滚项目**：项目已可用，缺的是附属数据；补偿方向是"补做"，不是"撤销"。
- **BR-07-04 保留类型**：`project` 实体只能经本模块创建、改名、改父；底座通用 `/entities` 对 type=project 的创建 / 改类型 / 改父 / 改名 / 删除返回 409 `entity_type_reserved`（底座新增，CCR-07-08）。项目永不物理删除。
- **BR-07-05 只读**：stage ∈ {CLOSED, CANCELLED} 时，本模块对该项目的一切写操作返回 409 `127005`；例外：SENT 交付的回执登记、终态项目 `archived_at` 归档。
- **BR-07-06 代号与名称**：code 租户唯一（409 `127001`）；名称同父同类型唯一（底座冲突 → 409 `127002`）；client 必须 type=client 且 active（422 `127003`）。
- **BR-07-07 主管 Agent**：`lead_agent_id` 须 ACTIVE、具 LEAD 角色、在本项目有 `agent_lead` 绑定（422 `127042`）；收到 `agent.retired` 或 `agent.state_changed`→RETIRING 时置 NULL、`dispatch_policy`→MANUAL，加风险 `LEAD_AGENT_LOST`。

### 5.2 阶段

- **BR-07-08 DELIVERED 守卫**（S9，全部满足）：(a) 无 READY / SENT 交付；(b) 当前需求基线下每个**可交付工作项**（type ∈ {FEATURE, BUG, DOC, DESIGN} 且 state≠CANCELLED）在某交付中的**最新** `client_result=ACCEPTED`；(c) ≥1 交付结果为 ACCEPTED 或 PARTIALLY_ACCEPTED；(d) 本项目交付产生的返工来件全部处于 01 终态（CONVERTED/MERGED/DECLINED；以 `REOPEN_WORK_ITEMS` 转换的返工来件在 02 T26 迁移完成时即为 CONVERTED）。CHORE/RESEARCH/TEST 不要求甲方验收（本规格提议：不是对外交付物）。不需甲方验收的可交付工作项仅限以下三种（均有授权裁决或甲方意思表示作依据，不设「豁免」开关）：① 经 02 CANCEL 裁决；② 其最新甲方 REJECTED 关联的返工来件已被 01 DECLINE（团队不返工、并已回复甲方）；③ 在 02 范围变更（SCOPE_CHANGE）对话框中被选择「取消」处置的受影响工作项（02 随该裁决记录 CANCEL，本质同 ①，单列以便界面说明来源）。不满足 → 422 `127013`，`meta.missing_work_item_ids[]`（≤100）。
- **BR-07-09 阶段建议**：健康度任务评估 S1/S2/S4/S7/S9 守卫；满足且 24h 内未提示过 → 发事件 `project.stage_ready{target}`，由 08 以**站内通知**（非收件箱）提示项目交付负责人「项目 X 可推进到 Y」。不自动迁移。
- **BR-07-10 阶段并发**：迁移带 `version`；服务端锁项目行（PG/MySQL `SELECT … FOR UPDATE`；SQLite 单写者事务）后重评守卫，杜绝"评估时满足、提交时已不满足"。
- **BR-07-11 委托与阶段**：`project_stage_change` 不在委托动作集合（PRD §8.3 仅 approve/reject/request_changes/merge）；Agent 只能经自定义角色获得 S1–S8、S10；S9/S11/S12 硬编码仅 HUMAN（CCR-07-06）。
- **BR-07-12 执行闸门**（`ProjectGate`，03/05/08 调用）。AUTONOMOUS 执行与 Lead 自动派单只在 ACTIVE 与 ACCEPTANCE_PENDING 放行（后者使部分验收后的返工能继续派单与评审，复审 #9）；PLANNING 拒绝：

| 条件 | AUTONOMOUS 执行 | INTERACTIVE 执行 / 新回合 | Lead 自动派单 | 委托裁决 |
|---|---|---|---|---|
| stage ∈ {ACTIVE, ACCEPTANCE_PENDING} 且无 hold | 允许 | 允许 | 允许（AUTO 时） | 允许 |
| stage = PLANNING | 拒绝 | 允许 | 拒绝（05 只在 ACTIVE / ACCEPTANCE_PENDING 生成提案，MANUAL_REQUEST 除外） | 允许 |
| stage ∈ {INTAKE, DISCOVERY, BLOCKED, DELIVERED} | 拒绝 | 允许 | 拒绝 | 允许 |
| hold BUDGET / MANUAL / ESCALATION | 拒绝 | 拒绝（409 `127032`） | 拒绝 | 允许（裁决不产生成本） |
| hold AUTONOMY_PRECONDITION | 允许 | 允许 | 拒绝 | **拒绝** |
| CLOSED / CANCELLED | 拒绝 | 拒绝 | 拒绝 | 拒绝 |

- **BR-07-13 项目暂停 / 恢复**：`POST /entities/{entity_id}/project/decisions {action: PAUSE|RESUME}`，权限 `decision_pause`，写 02 Decision(target=PROJECT)；PAUSE 加 MANUAL hold（reason 必填）；08 升级规则以 `POLICY:<rule_id>` 经 `ProjectHoldSetter` 加 ESCALATION hold；RESUME 只移除 MANUAL/ESCALATION，ESCALATION 的 RESUME 仅 HUMAN；BUDGET hold 只能经调整预算移除（422 `127034`）。

### 5.3 团队模式

- **BR-07-14 AUTONOMOUS 前置条件**（同一事务评估，写 `preconditions_snapshot`）：
  - P1 ≥1 条 ACTIVE 委托（08 `DelegationReader`）：scope 覆盖本项目、grantee 为本项目具 `agent_reviewer` 绑定的 ACTIVE Agent、动作含 `decision_approve`、`ends_at ≥ now+24h`。缺 → 422 `127021`。
  - P2 本项目生效的升级规则（08 `EscalationRuleReader`）覆盖且启用 4 类：`BUDGET`、`VERIFICATION_INCONCLUSIVE`、`CLIENT_REJECTION`、`REWORK_LIMIT`。缺 → 422 `127022`（`meta.missing_kinds`）。
  - P3 `budget_cap_micros` 非空且 `budget_state≠EXHAUSTED`。缺 → 422 `127023`。
  - P4 ≥1 名具 `agent_executor` 绑定的 ACTIVE Agent。缺 → 422 `127024`。
  - P5 stage ∈ {PLANNING, ACTIVE, ACCEPTANCE_PENDING}。否则 422 `127025`。
  多项失败返回第一个失败码，`meta.preconditions` 给全部结果。
- **BR-07-15 不可委托**：Agent 调用模式切换 → 403 `120403`（`reason_code=NON_DELEGABLE`），审计 `delivery.project_mode_change_denied`（outcome=denied）；`AuthorityChecker` 对 `project_mode_change` 恒判"不可委托"（00 §6.4）。
- **BR-07-16 前置条件失效**：消费 `delegation.revoked`、`delegation.expired`、升级规则变更、`agent.state_changed`，另每小时兜底扫描 AUTONOMOUS 项目；P1–P4 任一失败 → 加 hold `AUTONOMY_PRECONDITION`（已存在不重复加、不重复通知）；全部恢复 → 移除并通知。委托到期前 72h 通知一次。
- **BR-07-17 切回 HYBRID**：永远允许（无前置、无 step-up）；同事务发 `project.mode_changed{to:HYBRID}`，08 消费后撤销本项目全部委托；接管由 08 在其事务内调 `ProjectModeSwitcher.TakeOverOn(tx, …)`，"撤销委托 + 切模式"原子。

### 5.4 预算

- **BR-07-18 记账幂等**：消费 `execution.cost_updated{execution_id, project_id, work_item_id?, cumulative_cost_micros, breakdown{model,compute,platform}（均为累计）}`。事务内：锁 cursor 行（无则插入）→ `cumulative ≤ cursor.cumulative` 则丢弃（重复或乱序旧事件）→ `delta = cumulative − cursor.cumulative`，分解取各累计分量之差 → 插 cost_entry（`event_id` 唯一冲突 = 已处理）→ 锁项目行 `budget_spent_micros += delta` → 评估阈值（4.3）。同一事件重放 N 次只入账一次（AC-07-12）。
- **BR-07-19 对账**：任务每 15 分钟对最近 24h 有成本的执行，经 03 `EvidenceReader.ExecutionCost(execution_id)` 取权威累计；大于 cursor 则按 BR-07-18 补记（event_id=`reconcile:<execution_id>:<cumulative>`）；小于只记指标 `delivery_budget_drift_total`，不回减（本规格提议：已发生成本不撤销，避免预算状态回跳）。
- **BR-07-20 归属**：无 project_id 的 ADHOC 执行不计入项目预算；`platform_micros` MVP 恒为 0（无计费来源），字段保留。
- **BR-07-21 上限调整**：权限 `project_manage`，**仅 HUMAN**（Agent → 403 `120403`，`reason_code=HUMAN_ONLY`，00 §6.5），reason 必填；cap >0 且 ≤10^15 micros，否则 422 `127030`；AUTONOMOUS 下置 NULL → 422 `127033`；低于已花费须 `confirm_below_spent=true`（否则 422 `127035`），生效即 EXHAUSTED。
- **BR-07-22 超支**：100% 后在途成本照常入账，spent 可超过 cap，UI 显示"> 100%"；不终止执行，只暂停（继承旧项目 budget_cap："超限转暂停并通知，不自动终止"）。
- **BR-07-23 两种模式都生效**：cap 非空时 HYBRID 同样执行 80%/100%（本规格提议：不阻止花费的上限不是上限；不想被暂停的混编项目可不设上限）。

### 5.5 交付包组装

- **BR-07-24 只收 ACCEPTED**：加入项须 (a) 属本项目（422 `128003`）；(b) state=ACCEPTED（422 `128002`）；(c) 不在另一 READY/SENT 交付中（409 `128004`）；(d) 最新 APPROVE 裁决未被甲方 ACCEPTED 过（409 `128005`；返工后新的 APPROVE 产生新裁决 id，可再交付）。≤200 项（422 `128007`）。DRAFT 交付不占用 `active_lock`，同一工作项可出现在多个草稿中，DELIVER 时才排他。
- **BR-07-25 证据选择**：只能选所属工作项的证据（422 `128022`）；新加项默认将其 Claim 证据全部以 SUMMARY 选入；LOG 只允许 SUMMARY / FULL_LOG；FILE 单文件 ≤100 MiB，整包文件 ≤500 个。
- **BR-07-26 构建指纹**：`fingerprint = SHA-256(JCS({items: 排序[(work_item_id, accepted_decision_id)], selections: 排序[(evidence_id, include_mode, original_sha256)], release_notes_sha256, portal_settings, rules_version}))`。任一输入变化 → bundle_state=STALE。
- **BR-07-27 DELIVER 校验**（锁交付行与各项工作项行 `FOR SHARE` 后按序执行，任一失败整体不生效）：①状态 DRAFT（409 `128010`）；②≥1 项（422 `128006`）；③每项仍 ACCEPTED 且 `accepted_decision_id` = 当前最新 APPROVE 裁决（422 `128002`，`meta.work_item_ids`）；③b 无项被 02 标记 `baseline_stale=true`（需求基线已变更，须先重新评审，422 `128016`，C50）；④无项在其他在途交付（409 `128004`）；⑤`bundle_state=BUILT` 且指纹 = 现算值（422 `128008`）；⑥release_notes ≥50 字符且最后编辑者 `HUMAN`（422 `128009`）；⑦≥1 个 `receives_deliveries=true` 且 `can_accept=true` 的联系人，其开口在 01 中可用（422 `128011`）；⑧项目 stage ∈ {ACTIVE, BLOCKED, ACCEPTANCE_PENDING}（422 `128013`）；⑨主体 HUMAN（AGENT → 403 `120403`，`reason_code=HUMAN_ONLY`，审计 denied）；⑩step-up 证明有效（403 `127027`）；⑪`If-Match` 匹配（缺失 428 `120400`，不符 409 `120409`）；⑫计算并冻结 `source_intake_ids`。由于底座 step-up 只接受浏览器会话 Cookie，**PAT / 服务账号在结构上无法 DELIVER**。
- **BR-07-28 证据包生成与脱敏**（任务 `delivery.bundle_build`，fail-closed）：
  - 包结构（zip，条目按路径字典序、mtime 固定 `1980-01-01T00:00:00Z`、权限 0644、deflate level 6 —— 同输入同字节）：`manifest.json`、`RELEASE_NOTES.md`、`SHA256SUMS.txt`、`evidence/<work_item_key>/<evidence_id>-<安全文件名>`。底座 files 无打包能力，本模块用标准库 `archive/zip` 生成后写入 files。
  - R1 LOG：从不放原始日志；SUMMARY 放 03 的日志摘要；FULL_LOG 放经 R2 过滤后的全文，单份 ≤5 MiB，超出截断并标 `truncated:true`。
  - R2 秘密过滤（规则集版本 `rules_version` 随代码发版）：私钥块 `-----BEGIN [A-Z ]*PRIVATE KEY-----`…`END`、`AKIA[0-9A-Z]{16}`、`gh[pousr]_[A-Za-z0-9]{36,}`、`sk-[A-Za-z0-9_-]{20,}`、`xox[baprs]-[A-Za-z0-9-]{10,}`、JWT `eyJ[\w-]+\.[\w-]+\.[\w-]+`、`(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+`、URL 内凭证 `://[^/\s:@]+:[^/\s@]+@`、`Authorization: (Bearer|Basic) \S+`、`key=value` 中长度 ≥32 且香农熵 ≥4.5 bit/char 的值。命中替换为 `[REDACTED:<rule_id>]`，计数写 `redaction_count`。只对文本类 MIME（`text/*`、json、xml、yaml、markdown、日志）执行。
  - R3 拒绝文件（整文件剔除，不尝试脱敏）：`.env*`、`*.pem`、`*.key`、`*.p12`、`*.pfx`、`id_rsa*`、`id_ed25519*`、`*.kdbx`、`.npmrc`、`.pypirc`、`kubeconfig`、`credentials*`、`*.tfstate`；以及 03 `EvidenceReader.SecretFlags(evidence_ids)` 报告 `secret_flagged=true` 的证据。写 `excluded_reason`，manifest 列出条目但无文件。
  - R4 类型白名单：图片（png/jpeg/webp/gif）、pdf、文本类；`application/zip` 仅当 03 `EvidenceReader.SecretFlags` 报告 `secret_scanned=true` 才原样入包（不递归），否则剔除 `UNSUPPORTED_TYPE`；其余二进制剔除。
  - R5 内部身份：principal id、Agent 名、机器名、主机名、本地路径（`/Users/…`、`/home/…`、`C:\Users\…`）在 manifest 与文本中替换为门户标签（`contributor_display`：TEAM_ONLY →「团队」；ROLES →「工程师」/「AI 成员」）。
  - R6 永不入包：系统提示词、频道消息、知识块、Agent 记忆、交互会话原文、成本明细、内部裁决理由。
  - R7 任一扫描失败 / 超时（单文件 30s）→ FAILED，`bundle_error_code=REDACTION_FAILED`，不产生部分包。
  - 产物写入项目实体文件根 `/deliveries/<number>/`，ResourceBinding（purpose=`delivery_bundle`/`delivery_manifest`，resource_kind=`delivery`，VersionMode=`pinned`，owner scope=`entity`）；总大小 ≤512 MiB（超出 FAILED `TOO_LARGE`）。
  - `manifest_sha256 = SHA-256(JCS(manifest))`；`bundle_sha256` = zip 字节哈希。manifest Schema（v1）：

```json
{"schema_version":"1","delivery":{"id":"…","number":"D-ACME-003","title":"…","project":{"id":"…","name":"…","code":"ACME"}},
 "generated_at":"…","generator":{"name":"delivery-bundle","rules_version":"r1"},
 "items":[{"work_item_id":"…","key":"ACME-42","title":"…","type":"FEATURE","requirement":{"id":"…","version":3},
   "accepted_at":"…","accepted_by":"团队",
   "criteria":[{"id":"…","text":"…","evidence_ids":["…"]}],
   "evidence":[{"id":"…","type":"TEST_REPORT","producer":"AI 成员","produced_at":"…","original_sha256":"…",
     "verification":{"verifier":"go-test","version":"1.23","layer":"AUTOMATED","result":"PASSED"},
     "limitations":"…","include_mode":"FILE",
     "file":{"path":"evidence/ACME-42/…-report.xml","sha256":"…","size":1234,"redacted":true,"redaction_count":2,"truncated":false},
     "excluded_reason":null}]}],
 "release_notes":{"path":"RELEASE_NOTES.md","sha256":"…"},
 "totals":{"items":3,"evidence":9,"files":5,"bytes":482113}}
```

- **BR-07-29 DELIVER 同事务推进阶段**：`advance_stage_to_acceptance_pending=true` 且 stage=ACTIVE 时同事务写 S7（守卫"≥1 SENT"放宽为"本交付即将发送"，本规格提议：同一授权主体的显式选择，省一次操作）。发送最终失败不回退阶段，以风险 `DELIVERY_SEND_FAILED` 提示人处理。
- **BR-07-30 发送**：任务 `delivery.send` 对冻结的每个联系人**同步**调用 01 `IntakeReplySender.SendDelivery(ctx, {delivery_id, attempt_no, intake_ids[], contact_id, channel_id, manifest_sha256, bundle_file_id, manifest_file_id, title, summary, demo_url, portal_view})`（`intake_ids[]` = 交付的 `source_intake_ids`，供 01 写 DELIVERY_NOTICE；`attempt_no` 为幂等键组成，复审 #12） → `{status, message_ref}`，按 (delivery_id, contact_id, attempt_no) 幂等。**对甲方的通知只由 SendDelivery 发出**；01 消费 `delivery.sent` 时只写来件线程的 DELIVERY_NOTICE 记录，不再二次通知（C2 / #6）。01 负责：PORTAL → 门户出现该交付并邮件通知联系人；EMAIL → 邮件（说明 + 清单 + 门户链接；manifest ≤10 MiB 才作附件；zip 不作附件）；API/WEBHOOK → 回调甲方系统，载荷含 manifest 与 72h 有效的签名下载地址。重试 5 次（1m、5m、15m、1h、6h），耗尽 → `send_failed=true`、风险 + 通知；人可 `POST …/sends` 重发（新 attempt_no）。PRIMARY 联系人送达（或无 PRIMARY 时任一联系人送达）即 SENT（D4）。

### 5.6 甲方验收

- **BR-07-30b 请求正式交付**（C3）：`POST /deliveries/{id}/confirmation-requests`（`delivery_compose`，交付须 DRAFT 且 BR-07-27 ①–⑧ 全过，否则 422 返回首个失败码）写 `confirmation_requested_*` 并发 `delivery.confirmation_requested{delivery_id, number, requested_by}`；08 为 `HOLDERS(delivery_send)` 中的人类生成「交付确认」收件箱项，D3（READY）、D5、归档或 `delivery.withdrawn` 时关闭。**AUTONOMOUS 自动编排**：任务 `delivery.autonomous_compose`（每 30 分钟）对 AUTONOMOUS 且 stage ∈ {ACTIVE, ACCEPTANCE_PENDING} 的项目，若存在 ACCEPTED 且不在任何 DRAFT/READY/SENT 交付中、停留 >24h 的可交付工作项：以 `SYSTEM:delivery.autonomous_compose`（经 08 `SystemActions` 登记，仅授 `delivery_compose`）创建或补充一个 DRAFT 交付、默认选入 Claim 证据、触发证据包构建与发布说明起草请求（#37）；证据包 BUILT 且已有 Agent 草稿时，系统采纳最新草稿（`release_notes_actor_kind=AGENT`）并自动发确认请求（此时 BR-07-27 ⑥ 不作前置：发布说明须由确认的人类定稿后才能 DELIVER），通知**所有者与 `delivery_send` 持有者**。**无起草兜底**（复审 #3）：若 #37 返回 `128041`（无可起草 Agent）、或起草请求发出后 30 分钟仍无草稿（05 未回传 / 超时），系统仍发 `delivery.confirmation_requested{…, release_notes_status:"NEEDS_HUMAN"}`，发布说明留空并在交付详情与收件箱项上标「需人工撰写」；DELIVER 仍以 BR-07-27 ⑥（人类最后编辑）为前置。起草由 05 消费 `delivery.release_notes_requested` 向起草 Agent 下发 `agent.release_notes`，草稿经 #36 回写。DELIVER 仍仅限人类（00 §6.5）。风险 `ACCEPTED_UNDELIVERED`（BR-07-40）在此期间提示。
- **BR-07-31 结果计算**：回执必须**覆盖交付全部项**（集合不等 → 422 `128031`）；全 ACCEPTED → ACCEPTED；全 REJECTED → REJECTED；混合 → PARTIALLY_ACCEPTED。门户〔全部接受〕仍逐项落库。签收人必须是 `can_accept=true` 的项目联系人（否则 01 返回 403）。**统一输入**（门户 / API / RECORDED 共用，`AcceptanceRecorder.Record`）：`{delivery_id*, intake_contact_id*, source*, manifest_sha256_ack*, signer_name*≤200, confirmed*:true, items[]*{delivery_item_id*, result*, feedback_category?, comment?}, overall_comment?, client_request_id?}`；`confirmed≠true` → 422 `128036`（C4）。
- **BR-07-32 拒绝必填**：REJECTED 项必须有 `feedback_category` 与 ≥10 字符 `comment`（422 `128032`）。
- **BR-07-33 回执签名**：`receipt_sha256 = SHA-256(JCS({delivery_id, manifest_sha256_ack, items: 排序[(delivery_item_id, result, feedback_category, comment)], overall_comment, intake_contact_id, signer_name, source, received_at}))`；`manifest_sha256_ack` 必须等于交付 manifest_sha256（422 `128034`，防止签收旧包）；随审计 `delivery.acceptance_received`（detail 含 receipt_sha256）写入底座哈希链；回执 JSON 可下载（#45）。门户签收：手输签署人姓名 + 勾选"我确认以上验收结果"；API 来源由 01 校验渠道 HMAC 签名后传入。
- **BR-07-34 幂等与唯一**：每个交付只接受一份回执。`client_request_id` 相同或 receipt_sha256 相同 → 200 返回首份；内容不同 → 409 `128033`；交付非 SENT → 409 `128030`。
- **BR-07-35 线下登记**（source=RECORDED）：#43，`delivery_send` + HUMAN + step-up `acceptance_record` + 必附线下凭证（甲方签字件 / 邮件截图；缺 → 422 `128035`）；actor 仍为 CLIENT（甲方是验收主体），`recorded_by` 记登记人，UI 标"由〈登记人〉代为登记 · 附凭证"。
- **BR-07-36 不自动验收（决定）**：MVP **不提供**逾期自动验收。理由：①PRD I2 —— L4 只能来自甲方，系统不得代位；②混入系统判定会让"甲方一次验收率"失真；③合同"逾期视同验收"条款的效力与举证是商务判断，系统无法核实。替代：提醒 + 第 14 天升级 + 线下登记。v1.1 提议：`acceptance_policy.deemed_acceptance_days`（团队所有者 + step-up + 必填合同条款引用），到期生成 actor=SYSTEM、`authority_source=SYSTEM:delivery.deemed_acceptance` 的回执，UI 标"视同验收（合同条款）"，不计入一次验收率；需 CCR 扩展 `AcceptanceResult` 或加标志列，届时再议。MVP 中该字段非 null → 422 `127045`。
- **BR-07-37 提醒与升级**：SENT 时按 `reminder_days`（默认第 3、7、12 天）生成 REMINDER 行、第 `escalate_day`（默认 14）天生成 ESCALATION 行；到点经 01 向未回执的可签收联系人发提醒（同一联系人 24h 内至多 1 次）；ESCALATION 通知交付负责人并加风险 `ACCEPTANCE_OVERDUE`（RED）。回执到达或项目 CANCELLED 后任务跳过剩余行。
- **BR-07-38 拒收 → 返工来件**：回执事务内发 `acceptance.received{result, intake_ids[], accepted_work_item_ids[], rejected_items[…]}`；01 消费并创建「返工来件」（rework 标记，关联 delivery_id、acceptance_id、被拒工作项与反馈，标题 `返工：<交付编号>`，联系人 = 签收人），幂等键 = `origin_acceptance_id`；01 发 `intake.received{origin_acceptance_id}` 后本模块回填 `rework_intake_id`。AUTONOMOUS 项目另由 08 `CLIENT_REJECTION` 规则加 ESCALATION hold 并通知所有者（PRD §2.3）。
- **BR-07-39 工作项重开（对 02 的要求）**：被拒工作项**不自动**改状态；`client_result=REJECTED` 让其 L4 显示"甲方拒收"。返工来件分诊时由有权主体在 01 转换模式中选（与 01 BR-01-27 一致）：(a) `REOPEN_WORK_ITEMS` 质量返工 —— 01 调 02 T26（ACCEPTED→REWORK，Decision action=REQUEST_CHANGES，failure_category=QUALITY_REJECTION，`source_acceptance_id`，结构化反馈取自回执项），产生子执行；(b) `APPEND_TO_REQUIREMENT` 范围变更 —— 02 SCOPE_CHANGE（不可委托，step-up），Requirement 版本 +1；(c) `NEW_REQUIREMENT` 作为新需求；(d) 不返工 —— 01 DECLINE 返工来件（理由必填，回复甲方；该项按 BR-07-08 ② 不再要求甲方验收）。理由：甲方意见可能超出范围，外部输入不应绕过授权裁决改变内部状态。分诊卡默认建议：FUNCTIONAL_DEFECT/STYLE/PERFORMANCE/SECURITY → (a)，REQUIREMENT_MISMATCH → (b)，OTHER → 无默认。

### 5.7 健康度与风险

- **BR-07-40 风险与健康度**（任务每 5 分钟 + 相关事件触发）：

| kind | 条件 | 级别 |
|---|---|---|
| BUDGET_NEAR / BUDGET_EXHAUSTED | budget_state=NOTIFIED / EXHAUSTED | AMBER / RED |
| DELEGATION_EXPIRING | AUTONOMOUS 且某 P1 委托 72h 内到期 | AMBER |
| AUTONOMY_PRECONDITION_LOST | hold AUTONOMY_PRECONDITION | RED |
| ACCEPTANCE_WAITING / ACCEPTANCE_OVERDUE | SENT 交付已过首个 reminder_day / escalate_day | AMBER / RED |
| DELIVERY_SEND_FAILED | 有 send_failed=true 的 READY 交付 | RED |
| ACCEPTED_UNDELIVERED | 有 ACCEPTED 可交付工作项停留 >24h 且不在任何 READY/SENT 交付中 | AMBER |
| REVIEW_QUEUE_AGED | 有 NEEDS_HUMAN_REVIEW 停留 >24h 的工作项 | AMBER |
| REWORK_LIMIT | 有工作项质量返工 ≥3 | AMBER |
| HIGH_RISK_OPEN | 有 risk=HIGH 的非终态工作项 | AMBER |
| STAGE_STALE | BLOCKED 超过 3 天 | RED |
| TARGET_DATE_PASSED | target_date < 今天 且 stage ∉ {DELIVERED, CLOSED, CANCELLED} | RED |
| LEAD_AGENT_LOST / PROVISIONING_ISSUE | BR-07-07 / provisioning_issues 非空 | AMBER |
| HOLD_MANUAL / HOLD_ESCALATION | 对应 hold | AMBER / RED |

  health：有 RED → RED；否则有 AMBER → AMBER；否则 GREEN；终态 → NONE。

### 5.8 指标（公式、窗口、存储）

- **BR-07-41 时间与窗口**：日 = UTC 日（v1.1 支持租户时区）。窗口 `7d`/`30d`/`90d` = 截至今天（含当日部分数据）的最近 N 个 UTC 日；`custom` = `[from, to]` 闭区间，跨度 ≤366 天（422 `129002`）；`from>to` 或格式错 → 422 `129001`。窗口值 = **分子、分母分别求和后相除**；分母 0 → `null`（UI 显示"—"）。团队汇总 = 调用者可读（`project_read` 数据范围）项目的日快照求和。比率保留 1 位小数（四舍五入），金额四舍五入到 $0.01。
- **BR-07-42 每个已接受交付物成本（CPAD）** = Σ`cost_micros` / Σ`accepted_first_count`。只计工作项**首次** ACCEPTED；返工后再次 ACCEPTED 不再计数 —— 返工成本因此推高 CPAD，这正是指标意图。
- **BR-07-43 首次验收率** = Σ`first_pass_count` / Σ`accepted_first_count`；first_pass = 首次 ACCEPTED 之前无 REJECT / REQUEST_CHANGES 裁决、且无 branch_reason=QUALITY 的执行。
- **BR-07-44 人工关注分钟 / 已接受交付物** = Σ`attention_ms` / 60000 / Σ`accepted_first_count`。来源（仅 actor_kind=HUMAN）：02 `decision.made.data.attention_ms`（评审台计时）、03 `interaction.decided.data.attention_ms`、01 澄清发出 / 回复的 `attention_ms`；按事件 `occurred_at` 切日；单条上限 2h，超出截断（本规格提议：防止忘关页面污染数据）。
- **BR-07-45 返工周期**：周期起点 = 上次 ACCEPTED（或创建）之后首条 REJECT/REQUEST_CHANGES 裁决时间，或甲方回执中该项 REJECTED 的 received_at（取较早者）；终点 = 下一次 ACCEPTED。闭合当日计入 `rework_cycles+1`、`rework_ms_sum+=时长`、`rework_hist[桶]+1`（桶上界小时：1,4,8,24,48,72,168,336,∞）。窗口展示均值 = Σms/Σcycles，P50/P90 = 累计频率首次 ≥50%/≥90% 的桶上界（最后一桶显示"> 14 天"）。
- **BR-07-46 证据覆盖率** = Σ`evidenced_items` / Σ`claimed_items`。claimed = 当日提交过完成声明的去重工作项；evidenced = 该日声明关联 ≥1 条**可审查证据**（有 content_hash，且 type≠OBSERVATION 或 OBSERVATION 的 producer 为 HUMAN）。辅助"验收标准覆盖率" = Σ`criteria_covered`/Σ`criteria_total`。目标 ≥90%（PRD §15）。
- **BR-07-47 甲方一次验收率** = Σ`first_receipts_accepted` / Σ`first_receipts`。每个交付只有一份回执，按 received_at 当日计入分母；result=ACCEPTED 计入分子；PARTIALLY_ACCEPTED、REJECTED 只计分母；`source=RECORDED` 计入（有凭证的甲方意思表示）；v1.1 视同验收不计入。返工后的再次交付是新交付，单独计数。
- **BR-07-48 快照计算**：每 15 分钟重算"今天"（is_final=false）；每日 00:20 UTC 重算 D-1、D-2、D-3（迟到事件），D-3 写 is_final=true，定稿行不再改写；按主键 upsert 幂等。数据来源（全部为"按日从源头重算"，不在消费事件时累加，因此重算天然幂等、无需额外去重表）：成本 ← 本模块 `cost_entries`；ACCEPTED / 返工 / 声明 / 注意力（裁决）← 02 表（同模块直接聚合）；证据可审查性 ← 03 `EvidenceReader` 批量读；交互与澄清注意力 ← 03 `ExecutionReader.AttentionByDay`、01 `IntakeReader.AttentionByDay`；回执 ← 本模块 `acceptances`。
- **BR-07-49 闭环完整率**（PRD §15）由 08 基于审计计算，不在本规格。

### 5.9 通用边界

- **BR-07-50 权限不足**：所有权限拒绝统一 403 `120403`，原因放 `data.reason_code`（`MISSING_PERMISSION` + `meta.missing_permission`、`HUMAN_ONLY`、`NON_DELEGABLE`、`AGENT_NOT_DRAFTER` 等，00 §9）；数据范围外的项目 / 交付一律 404 `120404`（不泄露存在性）。
- **BR-07-51 引用不存在**：联系人、Agent、开口、证据解析失败 → 422 对应码，不 500。
- **BR-07-52 离线**：甲方开口不可用只影响发送任务，交付停在 READY 重试；控制台离线时前端禁用所有写按钮并显示重连条，恢复后重拉当前资源（以 `ETag` 判断是否需提示冲突）。
- **BR-07-53 上限**：初始成员 ≤50；联系人 ≤20；仓库 ≤10；交付项 ≤200；证据文件 ≤500；包 ≤512 MiB；草稿 ≤20/交付；提醒天数 ≤5；租户非归档项目 ≤500（422 `127044`，本规格提议：ICP 为 3–20 人团队，留足余量又防误用）。
- **BR-07-54 幂等**：所有 POST 命令接受 `Idempotency-Key`；同 key 同 operation 24h 内返回首次响应（含首次错误）；同 key 不同请求体 → 422 `176425`、进行中 → 409 `176409`（09 C7 平台幂等）。PUT 天然幂等（相同内容不写新审计 / 事件）。
- **BR-07-55 乐观锁**（00 §3）：档案、预算、交付、仓库的 GET 返回 `ETag: "<version>"`；修改请求必须带 `If-Match`，缺失 428 `120400`，不符 409 `120409`（`meta.current_version`）；请求体不再携带 `version`。

---

### 5.10 D112 / D113 增补

- **BR-07-56 纯人工模式**：`HUMAN_ONLY` 下 Agent 只能是 `ASSISTANT`：起草分诊卡、发布说明、计划与报告、风险与对账提示；不得成为执行者、评审者、测试 / 验收 Agent 或委托受托方（05、08 判定）。内部质量门的 `AGENT_TEST` / `AGENT_ACCEPTANCE` 由「人工核验清单」替代：非执行者的人类按模板清单逐项勾选并附交付物链接（非代码工作的完成证据 = 交付物链接（版本晚于开工）+ 清单 100% + 指定角色签收）。
- **BR-07-57 项目宪法**：每项目最多一个 ACTIVE 版本；人或 Agent 可提议，**仅人类**（`project_manage`）评审；激活后对**之后**开始的执行生效（03 在派发时经 `ConstitutionReader.Active` 读取并在执行记录写入 `constitution_version_no` 与 `body_sha256`），已开始的执行保持原版本；新建项目时由模板（11）生成 v1 草稿，项目进入 ACTIVE 阶段前必须有 ACTIVE 宪法（S3 守卫追加条件）。
- **BR-07-58 预测**：任务 `delivery.tolerance_forecast`（每小时 + 相关事件触发，去抖 60 s）计算：`forecast_end = now + 剩余未接受工作项数 × 近 30 天同项目工作项中位周期时间 / 有效并行度`（并行度 = 近 7 天日均活跃执行数，至少 1）；`forecast_cost = 已花费 + 剩余工作项数 × 近 30 天每接受工作项中位成本`；`forecast_scope = 当前基线在册验收标准数`；样本不足 5 条时以模板缺省值代替并标记「低置信」。
- **BR-07-59 例外不可绕过**：EXCEPTION 期间不得修改预算或截止时间；决定只能由人类以 `exception_decide` 做出（不可委托；Agent 调用 → 403 `120403` reason `NON_DELEGABLE`）；例外报告草稿由系统按预测与选项生成，提交前须人确认。
- **BR-07-60 交付只含 L3，内部记录不外泄**：交付包只能包含已 ACCEPTED（L3）工作项（既有规则）；证据包清单与甲方门户视图**排除**内部质量门记录：`VerificationStage ∈ {AGENT_TEST, AGENT_ACCEPTANCE}` 的结果、内部返工轮次、内部反馈、穿透学习记录；脱敏规则追加 R8（上述内容一律不写入 manifest）。甲方门户接口返回体经白名单字段序列化，回归测试断言不含上述字段。
- **BR-07-61 质量指标**（窗口与存储同 §5.8，日快照存分子分母）：
  - 穿透率 = Σ(escapes_human + escapes_client) ÷ Σ l3_entered；
  - 内部一次通过率 = Σ internal_first_pass ÷ Σ internal_qa_completed；
  - 平均内部轮次 = Σ internal_rounds_sum ÷ Σ internal_qa_completed；
  - 来源事件：02 / 03 的 `internal_qa.passed` / `internal_qa.failed`、`work_item.state_changed{to:NEEDS_HUMAN_REVIEW}`、`quality.escaped`（HUMAN_REVIEW 阶段由 02 发布，CLIENT_ACCEPTANCE 阶段由本模块在甲方逐项拒收时发布，判定：被拒工作项曾通过内部质量门）。
- **BR-07-62 甲方变更的容差检查与基线调整**（D117；01 kind=CHANGE）：① **检查**（`ToleranceReader.WouldBreach`，只读）：取需求 R 的 `REQUIREMENT` 容差行与 R 所属项目的 `PROJECT` 容差行（无行的主体跳过；两者都没有 → 返回空集，即不需要甲方确认）；对每个主体以最近一次预测（尚无预测时以 appetite 本身）加上变更增量——时间：`forecast_end_at + schedule_delta_days`；成本：`forecast_cost_micros + cost_delta_micros`；范围：`forecast_scope_criteria + scope_delta_criteria`——按 §4.7 同一判定式（超出 appetite ×（1 + 容差））得出越界维度；只计算增量造成或加剧的越界，增量为 0 或负的维度不报越界；返回 `breached[]{dimension, subject_kind}` 与快照 `{subjects[{kind, id, appetite, tolerance_pct, forecast, forecast_after}], baseline_no, low_confidence}`。② **调整**（`ToleranceAdjuster.ApplyApprovedChange`，只在调用方事务内、由 02 `AppendFromIntake` 调用）：对 R 的 `REQUIREMENT` 行按增量调整 appetite（`appetite_end_at += schedule_delta_days`；`appetite_cost_micros`、`appetite_scope_criteria` 非空时加上增量）；对 `PROJECT` 行只调整按 ① 会越界的维度；被调整的每行 `baseline_no+1`、按新基线重算状态、`version+1`；审计 `delivery.tolerance_changed`（detail：`source=CLIENT_APPROVED_CHANGE`、`source_intake_id`、各维增量、新旧 `baseline_no`）。任一被调整主体 `open_exception_id` 非空 → 409 `127055 tolerance_exception_open`，不改任何值（与 BR-07-59「例外期间不得修改预算或截止时间」一致：例外须先由人决定）。该调整代表甲方已同意的新承诺，不是静默延期；未经甲方确认（含容差内变更）不得调用。幂等由调用方保证（01 BR-01-26 转化幂等，同一 `source_intake_id` 只转化一次）。

## 6. 接口契约

约定：响应信封 `respx`；ID 为十进制字符串；`P` = `authz.RegisterEntity`（`entity_id` = 项目实体）；`O` = `authz.RegisterOwned`（按资源反查所属项目实体后授权；短地址与 `/entities/{entity_id}/…` 作用域地址同一操作，归属不符 404）；`T` = `authz.Register`（租户级，DataScoped 权限以 `FilterData` 过滤行）。step-up：请求带 `Cookie` + `Origin`（CSRF 校验）+ body `reauth_proof`，底座 `ReauthenticationConsumer` 在业务事务内消费（purpose 见行内）。OpenAPI tag = 最终发布路径第一段（00 §9；`tenants` / `entities` / `deliveries` / `project-repositories`）。乐观锁：GET 返回 `ETag`，标「If-Match」的写操作必须带 `If-Match`（缺失 428 `120400`，不符 409 `120409`），请求体不含 `version`。权限拒绝统一 403 `120403` + `data.reason_code`。幂等：平台 `176425`（同 key 不同请求）/ `176409`（进行中）。通用码 `120400/120403/120404/120409` 不逐行重复。

### 6.1 项目

| # | 方法 · 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 特有错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| 1 | POST `/tenants/{tenant_id}/projects` | tenants | `project_manage` · T；HUMAN | `name`*≤200、`code`*、`description`、`client`{`entity_id`｜`new_name`}、`target_date`、`origin_intake_id`、`contacts[]`{`intake_contact_id`,`role`,`receives_deliveries`,`can_accept`}、`members[]`{`principal_id`,`role_key`}、`repositories[]`、`budget_cap_micros`、`budget_notify_pct`、`portal_settings` | `project`、`entity`{`id`,`parent_id`}、`client_entity_id`、`channel_id`、`role_bindings[]`（路由规则 14：列出全部受影响资源） | 127001–04/40–44；403 120403（NON_DELEGABLE / HUMAN_ONLY） | Idempotency-Key；201 |
| 2 | GET `/tenants/{tenant_id}/projects` | tenants | `project_read` · T+FilterData | 过滤 `stage`(eq,in) `team_mode`(eq) `health`(eq,in) `client_entity_id`(eq) `code`(eq) `name`(contains) `archived`(eq，默认 false) `lead_agent_id`(eq)；排序 `name` `stage_changed_at` `health` `target_date` `created_at`；limit ≤100 | `project_id,code,name,client{id,name},stage,team_mode,health,health_reasons,budget{cap,spent,state,pct},target_date,counts{open_work_items,needs_review,waiting_deliveries},lead_agent_id,version` | — | — |
| 3 | GET `/entities/{entity_id}/project` | entities | `project_read` · P | — | 档案全字段 + `name`、`client` | — | 返回 `ETag` |
| 4 | PATCH `/entities/{entity_id}/project` | entities | `project_manage` · P | 可选 `name`,`description`,`client_entity_id`(可置空),`target_date`,`lead_agent_id`,`dispatch_policy`,`ensemble_max_branches`,`default_send_channel_id`,`portal_settings`,`acceptance_policy` | 档案 | 127002/03/05/42/45 | If-Match；改名 / 改父经 core 同事务 |
| 5 | GET `/entities/{entity_id}/project/overview` | entities | `project_read` · P | — | `project`、`stage_readiness[]`{`target`,`allowed`,`criteria[]`}、`baseline`{`requirement_count`,`version`,`criteria_count`,`work_items_by_state`}、`team`{`humans`,`agents`,`lead_agent`}、`mode`{`team_mode`,`dispatch_policy`,`delegations`{`active`,`min_ends_at`},`escalation_kinds_enabled[]`}、`budget`、`risks[]`{`kind`,`level`,`detail`,`ref`}、`deliveries`{`draft`,`ready`,`sent`,`last_result`}、`provisioning_issues` | — | — |
| 6 | GET `/entities/{entity_id}/project/stage-readiness` | entities | `project_read` · P | — | 每个合法目标的 `allowed`、`criteria[]`、`human_only` | — | — |
| 7 | POST `/entities/{entity_id}/project/stage-transitions` | entities | `project_stage_change` · P；S9/S11/S12 HUMAN；S12 step-up `project_cancel` | `to_stage`*、`reason`、`blocked_kind`、`scope_change_id`、`reauth_proof` | `transition`、`project` | 127005/10–14/27 | Idempotency-Key；If-Match + 行锁 |
| 8 | GET `/entities/{entity_id}/project/stage-transitions` | entities | `project_read` · P | 排序 `occurred_at` | 列表 | — | — |
| 9 | POST `/entities/{entity_id}/project/mode-changes` | entities | `project_mode_change` · P；HUMAN；→AUTONOMOUS step-up `team_mode_change` | `to_mode`*、`reason`*≥10、`reauth_proof` | `mode_change`（含 `preconditions_snapshot`）、`project` | 127005/20–25/27 | Idempotency-Key；If-Match |
| 10 | GET `/entities/{entity_id}/project/mode-preconditions` | entities | `project_read` · P | — | `[{key,passed,detail}]`（P1–P5） | — | — |
| 11 | GET `/entities/{entity_id}/project/mode-changes` | entities | `project_read` · P | — | 列表 | — | — |
| 12 | GET `/entities/{entity_id}/project/budget` | entities | `project_read` · P | — | `cap_micros,cap_version,spent_micros,notify_pct,state,pct_used,holds,breakdown{model,compute,platform},top_work_items[10]` | — | 返回 `ETag` |
| 13 | PUT `/entities/{entity_id}/project/budget` | entities | `project_manage` · P；HUMAN | `cap_micros`(可 null)、`notify_pct`、`reason`*、`confirm_below_spent` | 预算视图 + `budget_event` | 127005/30/33/35 | 同值不写事件；If-Match |
| 14 | GET `/entities/{entity_id}/project/budget/events` | entities | `project_read` · P | 过滤 `kind`；排序 `occurred_at` | 列表 | — | — |
| 15 | POST `/entities/{entity_id}/project/decisions` | entities | `decision_pause` · P；RESUME(ESCALATION) HUMAN | `action`*∈{PAUSE,RESUME}、`hold_kind`（RESUME 必填）、`reason`* | `decision`（02 形状）、`holds` | 127034；422 `127036` | Idempotency-Key；If-Match |
| 16 | PUT `/entities/{entity_id}/project/contacts/{intake_contact_id}` | entities | `project_manage` · P | `role`,`receives_deliveries`,`can_accept` | 联系人 | 127040 | 幂等 |
| 17 | DELETE `/entities/{entity_id}/project/contacts/{intake_contact_id}` | entities | `project_manage` · P | — | 204 | 422 `127046`（有 SENT 交付时不得删最后一个可签收联系人） | 幂等 |
| 18 | GET `/entities/{entity_id}/project/contacts` | entities | `project_read` · P | — | 列表（含 01 解析的姓名、开口） | — | — |
| 19 | POST `/entities/{entity_id}/project/repositories` | entities | `project_manage` · P | `name`*,`remote_url`*,`default_branch`,`protected_branches`,`is_primary` | 仓库 | 127041 | Idempotency-Key；201 |
| 20 | GET `/entities/{entity_id}/project/repositories` | entities | `project_read` · P | — | 列表 | — | — |
| 21 | PATCH `/project-repositories/{repository_id}` | project-repositories | `project_manage` · O | 字段同 19，`archived` | 仓库 | 127041 | If-Match |
| 22 | GET `/entities/{entity_id}/project/metrics` | entities | `project_read` · P | `window`=7d｜30d｜90d｜custom、`from`、`to` | `kpis`{`cpad_micros`,`first_pass_rate`,`attention_min_per_accepted`,`rework_mean_ms`,`rework_p50_ms`,`rework_p90_ms`,`evidence_coverage`,`criteria_coverage`,`client_first_acceptance_rate`}，每项含 `{value,numerator,denominator}`；`includes_partial_today` | 129001/02 | — |
| 23 | GET `/entities/{entity_id}/project/metrics/daily` | entities | `project_read` · P | `from`,`to`（≤366 天） | 日快照数组 | 129001/02 | — |
| 24 | GET `/tenants/{tenant_id}/delivery-metrics` | tenants | `project_read` · T+FilterData | 同 22 + `project_id`(in) | 团队 `kpis` + `by_project[]`（≤50，CPAD 降序） | 129001/02 | — |
| 46 | PUT `/entities/{entity_id}/project/members/{principal_id}` | entities | `project_manage` · P | `role_key`*（BR-07-02 / BR-07-02b） | `member`{`principal_id`,`actor_kind`,`role_key`,`binding_id`} | 127043/127005 | 幂等（同角色不写新审计） |
| 47 | DELETE `/entities/{entity_id}/project/members/{principal_id}` | entities | `project_manage` · P | — | 204；若清空主管 Agent 返回 `project` | 422 `127048`/`127049`；127005 | 幂等 |
| 48 | GET `/entities/{entity_id}/project/members` | entities | `project_read` · P | 过滤 `actor_kind`(eq) `role_key`(eq,in) | `principal_id,actor_kind,display_name,role_key,binding_id,active_assignments` | — | — |
| 25 | PUT `/entities/{entity_id}/project/provisioning-retries/{step}` | entities | `project_manage` · P | — | `provisioning_issues` | 422 `127047` | 幂等 |

### 6.2 交付

| # | 方法 · 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 特有错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| 26 | POST `/entities/{entity_id}/deliveries` | entities | `delivery_compose` · P | `title`*、`summary`、`demo_url`、`work_item_ids[]`≤200、`rework_of_delivery_id` | 交付（含 items、`bundle_state`） | 127005；128002–07/13 | Idempotency-Key；201；项目行锁分配序号 |
| 27 | GET `/entities/{entity_id}/deliveries` | entities | `delivery_read` · P | 过滤 `state`(eq,in) `archived` `number`(eq) `created_at`(gte,lte)；排序 `created_at` `sent_at` `sequence_no` | `id,number,title,state,item_count,bundle_state,sent_at,acceptance_due_at,result,version` | — | — |
| 28 | GET `/tenants/{tenant_id}/deliveries` | tenants | `delivery_read` · T+FilterData | 同 27 + `project_id`(in) | 同 27 + `project{id,code,name}` | — | — |
| 29 | GET `/deliveries/{delivery_id}`（同 `/entities/{entity_id}/deliveries/{delivery_id}`） | deliveries | `delivery_read` · O | — | 全字段 + `items[]`、`selections[]`、`sends[]`、`acceptance`、`reminders[]`、`drafts_count` | — | — |
| 30 | PATCH `/deliveries/{delivery_id}` | deliveries | `delivery_compose` · O | `title`,`summary`,`demo_url`,`release_notes_md`,`adopt_draft_id`,`archived` | 交付 | 128001/14 | If-Match；记录 release_notes 编辑者 |
| 31 | PUT `/deliveries/{delivery_id}/items/{work_item_id}` | deliveries | `delivery_compose` · O | `position` | 交付 | 128001–05/07 | 幂等；If-Match |
| 32 | DELETE `/deliveries/{delivery_id}/items/{work_item_id}` | deliveries | `delivery_compose` · O | — | 204 | 128001 | 幂等；连带删该项证据选择 |
| 33 | PUT `/deliveries/{delivery_id}/evidence-selections` | deliveries | `delivery_compose` · O | `selections[]`{`evidence_id`,`include_mode`}（整集替换，≤500） | `selections[]` | 128001/07/22 | 幂等；If-Match |
| 34 | POST `/deliveries/{delivery_id}/bundles` | deliveries | `delivery_compose` · O | — | 202 `bundle_state=BUILDING`,`job_id`；指纹未变且已 BUILT → 200 原结果 | 128001/06/20 | Idempotency-Key |
| 35 | GET `/deliveries/{delivery_id}/bundle` | deliveries | `delivery_read` · O | — | `state,fingerprint,current_fingerprint,manifest,bundle_file_id,manifest_file_id,bundle_sha256,manifest_sha256,size,error_code` | — | — |
| 36 | POST `/deliveries/{delivery_id}/release-note-drafts` | deliveries | `delivery_read` · O；主体须为本项目 lead_agent 或具 `agent_coordinator` 绑定的 Agent | `content_md`*≤64KB、`source_fingerprint`* | 草稿 | 403 `120403`（`reason_code=AGENT_NOT_DRAFTER`）；128001 | Idempotency-Key；201 |
| 37 | POST `/deliveries/{delivery_id}/release-note-draft-requests` | deliveries | `delivery_compose` · O | `note`≤2000 | 202 `requested_agent_id` | 422 `128041` | Idempotency-Key；发 `delivery.release_notes_requested` |
| 38 | GET `/deliveries/{delivery_id}/release-note-drafts` | deliveries | `delivery_read` · O | — | 列表 | — | — |
| 39 | POST `/deliveries/{delivery_id}/decisions` | deliveries | `delivery_send` · O；HUMAN；step-up `delivery_send` | `action`*=DELIVER、`reason`≤2000、`reauth_proof`*、`send_contact_ids[]`（缺省 = 全部可收件）、`send_channel_id`、`advance_stage_to_acceptance_pending` | `decision`、交付（READY）、可选 `stage_transition` | BR-07-27 各码；422 `128015` | Idempotency-Key；If-Match + 行锁 |
| 40 | PUT `/deliveries/{delivery_id}/withdrawal` | deliveries | `delivery_send` · O；HUMAN | `reason`* | 交付（DRAFT） | 128010/12 | 幂等 |
| 41 | GET `/deliveries/{delivery_id}/sends` | deliveries | `delivery_read` · O | — | 列表 | — | — |
| 42 | POST `/deliveries/{delivery_id}/sends` | deliveries | `delivery_send` · O；HUMAN | `contact_ids[]`（缺省 = 失败的联系人） | 202 新 attempt | 128010/11 | Idempotency-Key；SENT 后重发只重新通知 |
| 43 | PUT `/deliveries/{delivery_id}/acceptance` | deliveries | `delivery_send` · O；HUMAN；step-up `acceptance_record` | `intake_contact_id`*、`signer_name`*、`manifest_sha256_ack`*、`items[]`*{`delivery_item_id`,`result`,`feedback_category`,`comment`}、`overall_comment`、`record_evidence_file_id`*、`reauth_proof`* | `acceptance`（含 `receipt_sha256`）、交付 | 128030–35 | 一次性（BR-07-34） |
| 44 | GET `/deliveries/{delivery_id}/acceptance` | deliveries | `delivery_read` · O | — | 回执 + items | 404（尚无） | — |
| 49 | POST `/deliveries/{delivery_id}/confirmation-requests` | deliveries | `delivery_compose` · O | `note`≤2000 | `delivery`（含 `confirmation_requested_at`） | 128001；BR-07-27 ①–⑧ 各码 | Idempotency-Key；同交付未变化重复请求返回首次结果（BR-07-30b） |
| 45 | GET `/deliveries/{delivery_id}/acceptance/receipt` | deliveries | `delivery_read` · O | — | 回执规范化 JSON（签名输入 + `receipt_sha256`），`Content-Disposition: attachment` | 404 | — |

甲方侧（01 门户会话接口 `/portal-*`、API 开口）的查看与签收由 01 定义并注册（`RegisterPublic` + 门户会话 / 渠道签名），内部统一调 `AcceptanceRecorder.ListForIntakes / PortalView / Record`，输入见 BR-07-31，校验与 #43 相同（RECORDED 专属字段除外）。

### 6.3 跨模块接口（本模块提供；写方法带 `On` 后缀并接受调用方 `*gorm.DB` 以加入其事务）

| 接口 | 方法（语义） | 使用者 |
|---|---|---|
| `ProjectCreator` | `CreateForIntakeOn(ctx, tx, {tenant_id, name, code?, origin_intake_id, primary_contact_id, actor}) (ProjectRef{id, code, channel_id}, error)`（BR-07-01b） | 01 |
| `ProjectReader` | `Get`、`ListByIDs`、`Stage`、`TeamMode`、`LeadAgent`、`DispatchPolicy`、`Repositories`、`Contacts`、`Members(tenant, project)`、`HealthList(tenant, filter) ([]{project_id, code, name, stage, team_mode, health, health_reasons, budget{cap,spent,state,pct}, holds}, error)`（08 总览「项目健康」） | 01、03、04、05、06、08 |
| `RepositoryReader` | `Get(ctx, tenant, repositoryID) (RepositoryView{project_id, url, default_branch, archived}, error)` | 04 |
| `IntakeProgressReader` | `Progress(ctx, tenant, intakeIDs[]) (map[intake_id]{requirement_id?, project_stage?, team_mode?, deliveries[]{id, number, state}, latest_acceptance?{result, received_at}, milestone}, error)`；milestone ∈ RECEIVED/CLARIFYING/IN_PROGRESS/AWAITING_ACCEPTANCE/DELIVERED（PARTIALLY_ACCEPTED 记为 IN_PROGRESS） | 01 |
| `ProjectGate` | `CanStartExecution(tenant, project, kind)`、`CanAutoDispatch`、`CanDelegatedDecide` → `(bool, reasonCode)`（BR-07-12） | 03、05、08 |
| `ProjectModeSwitcher` | `TakeOverOn(ctx, tx, tenant, project, actor, reason, revokedDelegationIDs) error` | 08 |
| `ProjectHoldSetter` | `AddEscalationHoldOn(ctx, tx, tenant, project, ruleID, reason)` | 08 |
| `AcceptanceRecorder` | `ListForIntakes(ctx, tenant, intakeIDs[]) ([]PortalDeliveryView, error)`、`PortalView(ctx, tenant, deliveryID) (PortalDeliveryView{…按 portal_settings 裁剪, source_intake_ids[]}, error)`、`Record(ctx, AcceptanceInput) (acceptance_id, error)`（输入见 BR-07-31） | 01 |
| `ReleaseNotesDrafting` | `ReportUnavailable(ctx, tx, {tenant_id, delivery_id, reason: NO_DRAFTER / DRAFTER_NOT_RUNNING / TIMEOUT})`：幂等；立即触发 BR-07-30b 兜底（仅对处于 BR-07-30b 流程中的交付包生效：兜底同样先执行 BR-07-27 ①–⑤⑦⑧ 校验，失败则只通知负责人而不发确认请求；⑥「人最后编辑发布说明」仍在 DELIVER 时强制）（发布 `delivery.confirmation_requested{release_notes_status:"NEEDS_HUMAN"}`），不再等待 30 分钟 | 05（`agent.release_notes` 不可用或超时） |
| `AcceptanceReader` | `Get(ctx, tenant, acceptanceID) (AcceptanceView{delivery_id, project_id, contact_id, result, items[]{delivery_item_id, work_item_id, result, feedback_category, comment}, signer_name, received_at}, error)`（`project_id`/`contact_id`/`delivery_item_id` 供 01 构建返工来件，复审 #13）（含评论正文，事件中不带） | 01、06 |
| `ProjectProvisioningIssueRecorder` | `Record(tenant, project, step, eventID, errorCode)` | 01、06、08 |
| `DeliveryMetricsReader` | `ProjectKPIs(window)`、`TeamKPIs(window, filter)` | 08 总览 |
| `ToleranceReader`（D112 / D117） | `ForSubject(ctx, tenant, kind, id)`（§6.5）；`WouldBreach(ctx, tenant, requirement_id, {schedule_delta_days, cost_delta_micros, scope_delta_criteria}) (breached[]{dimension TIME\|COST\|SCOPE, subject_kind REQUIREMENT\|PROJECT}, snapshot, error)`——只读，不写库，不改预测（BR-07-62 ①）；需求不存在 / 跨租户 → `120404` | 02、08；01（`WouldBreach`） |
| `ToleranceAdjuster`（D117） | `ApplyApprovedChange(ctx, tx, {tenant_id, requirement_id, source_intake_id, schedule_delta_days, cost_delta_micros, scope_delta_criteria}) (adjusted[]{subject_kind, tolerance_id, baseline_no}, error)`——在调用方事务内执行（BR-07-62 ②）；未决例外 → 409 `127055` | 02（`AppendFromIntake`，经 01 转化甲方已确认的变更） |

本模块使用：01 `IntakeReplySender{SendDelivery, SendAcceptanceReminder}`、01 `IntakeReader{Contacts, ChannelAvailable, AttentionByDay, OpenClarifications}`；02 `WorkItemReader`、`DecisionRecorder`（同模块）；03 `ExecutionReader{ActiveCount, AttentionByDay, Get}`、`EvidenceReader{ExecutionCost, SecretFlags, …}`；05 `ChannelProvisioner.CreateDefaultOn`、`AgentDirectory`；08 `DelegationReader`、`EscalationRuleReader`、`AuthorityChecker.Check`；08 `SystemActions` 登记 `SYSTEM:delivery.autonomous_compose`（`delivery_compose`）；core `EntityProvisioner`、`EntityRoleBindingStore.PutOn`。

### 6.4 错误码与 i18n key（`delivery.errors.<key>`，en-US / zh-CN / ms-MY 三语）

| 码 | HTTP | key | 码 | HTTP | key |
|---|---|---|---|---|---|
| 127001 | 409 | project_code_taken | 127036 | 422 | hold_not_present |
| 127002 | 409 | project_name_taken | 127040 | 422 | contact_not_found |
| 127003 | 422 | parent_not_client | 127041 | 422 | repository_url_invalid |
| 127004 | 503 | provisioning_unavailable | 127042 | 422 | lead_agent_invalid |
| 127005 | 409 | project_read_only | 127043 | 422 | member_role_mismatch |
| 127010 | 409 | stage_transition_illegal | 127044 | 422 | project_limit_reached |
| 127011 | 422 | stage_criteria_unmet | 127045 | 422 | acceptance_policy_invalid |
| 127012 | 422 | stage_reason_required | 127046 | 422 | last_accepting_contact |
| 127013 | 422 | stage_delivered_requires_acceptance | 127047 | 422 | no_such_issue |
| 127014 | 422 | stage_close_open_work | 128001 | 409 | delivery_not_draft |
| 127048 | 422 | member_has_active_assignment | 128002 | 422 | delivery_item_not_accepted |
| 127020 | 409 | mode_same | 128003 | 422 | delivery_item_wrong_project |
| 127021 | 422 | mode_precondition_delegation | 128004 | 409 | delivery_item_in_flight |
| 127022 | 422 | mode_precondition_escalation | 128005 | 409 | delivery_item_already_client_accepted |
| 127023 | 422 | mode_precondition_budget | 128006 | 422 | delivery_empty |
| 127024 | 422 | mode_precondition_executor | 128007 | 422 | delivery_limit_exceeded |
| 127025 | 422 | mode_stage_not_allowed | 128008 | 422 | delivery_bundle_stale |
| 127049 | 422 | last_delivery_lead | 128009 | 422 | delivery_release_notes_required |
| 127027 | 403 | reauthentication_required | 128010 | 409 | delivery_transition_illegal |
| 127030 | 422 | budget_cap_invalid | 128011 | 422 | delivery_send_target_unavailable |
| 128016 | 422 | delivery_item_baseline_stale | 128012 | 409 | delivery_already_sent |
| 127032 | 409 | project_on_hold | 128013 | 422 | delivery_project_stage |
| 127033 | 422 | budget_required_for_autonomous | 128014 | 422 | demo_url_invalid |
| 127034 | 422 | hold_requires_budget_change | 128015 | 422 | delivery_decision_action_invalid |
| 127035 | 422 | budget_below_spent_unconfirmed | 128020 | 409 | bundle_build_in_progress |
| 128021 | 422 | bundle_redaction_failed | 128022 | 422 | evidence_not_in_items |
| 128030 | 409 | acceptance_not_awaited | 128031 | 422 | acceptance_items_mismatch |
| 128032 | 422 | acceptance_comment_required | 128033 | 409 | acceptance_conflict |
| 128034 | 422 | acceptance_manifest_mismatch | 128035 | 422 | acceptance_record_evidence_required |
| 128036 | 422 | acceptance_not_confirmed | 128041 | 422 | no_drafting_agent |
| 129001 | 422 | metric_window_invalid | 129002 | 422 | metric_window_too_large |

通用 `120400 invalid_input`（含缺 If-Match 428）、`120403 forbidden`（`data.reason_code`：`MISSING_PERMISSION`/`HUMAN_ONLY`/`NON_DELEGABLE`/`AGENT_NOT_DRAFTER`）、`120404 not_found`、`120409 version_conflict` 由 02 定义、本规格复用；幂等码为平台 `176425`/`176409`。子段：02 `120000–125999`，12 Part A `126000–126999`，07 `127000–129999`（00 §9，CR-12-01）。

---

### 6.5 D112 / D113 增补接口

| # | 方法 | 路径 | tag | 权限 · 守卫 | 请求 | 响应 / 错误 |
|---|---|---|---|---|---|---|
| 50 | GET | `/entities/{entity_id}/project/constitution-versions` | entities | `project_read` · E | `?state=` | 版本列表（含 ACTIVE） |
| 51 | POST | `/entities/{entity_id}/project/constitution-versions` | entities | `project_read` · E（人或 Agent 提议） | `{body*, change_summary*}` | 201 DRAFT；422 `127050` |
| 52 | POST | `/constitution-versions/{version_id}/submissions` | constitution-versions | 提议人 · O | — | IN_REVIEW；409 `127052` |
| 53 | POST | `/constitution-versions/{version_id}/decision` | constitution-versions | `project_manage` · O；HUMAN | `{result:"APPROVE"\|"REJECT", comment}` + If-Match | ACTIVE / REJECTED；403 `120403` |
| 54 | GET / PUT | `/entities/{entity_id}/project/tolerances`、`/tolerances/{tolerance_id}` | entities / tolerances | `project_read` / `project_manage` · E/O；HUMAN | 预算与容差 + If-Match | 409 `127053`（EXCEPTION 期间修改） |
| 55 | GET | `/entities/{entity_id}/project/exceptions`、`/exceptions/{exception_id}` | entities / exceptions | `project_read` | — | 例外列表 / 详情 |
| 56 | PUT | `/exceptions/{exception_id}/report` | exceptions | `project_manage` · O；HUMAN | `{situation*, options[]*, recommendation*}` + If-Match | REPORTED |
| 57 | POST | `/exceptions/{exception_id}/decision` | exceptions | `exception_decide` · O；HUMAN；KILL 另需 step-up `project_cancel` | `{decision*, payload, reason*}` | DECIDED；403 `120403`（NON_DELEGABLE / HUMAN_ONLY）；409 `127054`（未提交报告） |

**本模块新增提供的接口**：`ConstitutionReader.Active(ctx, tenant, project) (version_no, body, sha256, error)`（03、05 使用）；`ToleranceReader.ForSubject(ctx, tenant, kind, id)`（02、08 使用）。
**新错误码**：`127050` constitution_too_large（422）、`127051` human_only_to_autonomous（409）、`127052` constitution_not_in_review（409）、`127053` tolerance_locked_by_exception（409）、`127054` exception_report_missing（409）、`127055` tolerance_exception_open（409，D117：有未决例外时拒绝按甲方确认的变更调整基线，BR-07-62）。

## 7. 后台任务与事件

### 7.1 任务（09 `JobQueue`；互斥用底座 `dlock`，锁名含租户）

| 任务 | 触发 | 频率 / 时限 | 互斥 | 重试 | 说明 |
|---|---|---|---|---|---|
| `delivery.cost_apply` | 消费 `execution.cost_updated` | 事件驱动，单条 ≤2s | 事务内 cursor 行锁 + 项目行锁 | 指数退避 5 次（1s→16s），后入死信并告警 | BR-07-18；阈值跨越在同事务判定 |
| `delivery.budget_reconcile` | 定时 | 每 15 分钟 | `dlock delivery:budget:{tenant}` 租约 10 分钟 | 下个周期自然重试 | BR-07-19 |
| `delivery.bundle_build` | #34 | 单次 ≤10 分钟；单文件扫描 ≤30s | 交付行 `bundle_state=BUILDING` 作为锁（CAS 自 NONE/STALE/FAILED/BUILT） | 基础设施错误重试 3 次（30s、2m、10m）；REDACTION_FAILED / TOO_LARGE 不重试 | 完成后若指纹已变（期间被编辑）→ 产物丢弃、STALE |
| `delivery.send` | D3 | 单联系人调用 ≤30s | `dlock delivery:send:{delivery_id}` | 5 次：1m、5m、15m、1h、6h；耗尽 `send_failed=true` | BR-07-30 |
| `delivery.acceptance_reminders` | 定时 | 每 15 分钟扫描 `state=SENT AND due_at≤now AND sent_at IS NULL` 的提醒行 | `dlock delivery:reminders:{tenant}`；行级 `UPDATE … SET sent_at=now WHERE sent_at IS NULL` 抢占 | 发送失败不置 sent_at，下周期重试；同联系人 24h 去重 | BR-07-37 |
| `delivery.metrics_snapshot` | 定时 | 今天：每 15 分钟；D-1..D-3：每日 00:20 UTC | `dlock delivery:metrics:{tenant}` 租约 30 分钟 | 失败下周期重算（upsert 幂等） | BR-07-48；每租户单批 ≤500 项目 |
| `delivery.project_health_refresh` | 定时 + 事件（预算、回执、阶段、委托、工作项状态变化） | 每 5 分钟全量；事件触发防抖 10s | `dlock delivery:health:{tenant}` | 下周期 | BR-07-40、BR-07-09 |
| `delivery.autonomous_compose` | 定时 | 每 30 分钟；单项目 ≤60s | `dlock delivery:autocompose:{tenant}`；按项目行锁 | 下周期 | BR-07-30b；以 `SYSTEM:delivery.autonomous_compose` 写审计 |
| `delivery.autonomy_watch` | 事件（`delegation.revoked`、`delegation.expired`、升级规则变更、`agent.state_changed`）+ 定时 | 每小时兜底 | `dlock delivery:autonomy:{tenant}` | 下周期 | BR-07-16 |

所有任务以 `SYSTEM:<任务名>` 作为 `authority_source` 写审计；接 `audit.Do` 记录运行结果。

### 7.2 发布的领域事件（outbox，载荷外层按 00 §7.1）

| 事件 | 时机 | `data` |
|---|---|---|
| `project.created` | BR-07-01 提交 | `{code, name, client_entity_id, origin_intake_id, team_mode, channel_id, member_ids[]}` |
| `project.stage_changed` | 每次阶段迁移 | `{from, to, reason?, blocked_kind?, transition_id}` |
| `project.mode_changed` | 模式切换 / 接管 | `{from, to, trigger, mode_change_id}` |
| `project.hold_changed` | holds 增减 | `{added[], removed[], holds[]}` |
| `project.stage_ready` | BR-07-09 | `{target, criteria_passed[]}` |
| `project.members_changed` | BR-07-02b | `{added[{principal_id, role_key}], removed[principal_id]}`（00 v1.5 已登记；消费者 collab 同步频道成员、governance） |
| `budget.threshold_reached` | 4.3 阈值跨越（每 cap_version 每阈值一次） | `{threshold_pct, cap_micros, spent_micros, cap_version}` |
| `delivery.release_notes_requested` | #37 | `{delivery_id, drafter_agent_id（取自 #37 请求的 requested_agent_id；未指定为 null）, note}` |
| `delivery.confirmation_requested` | BR-07-30b | `{delivery_id, number, requested_by, autonomous, release_notes_status: "DRAFTED"\|"NEEDS_HUMAN"}` |
| `delivery.withdrawn` | D5、DRAFT 归档 | `{delivery_id, number, reason}` |
| `delivery.ready` | D3 | `{delivery_id, number, decision_id, item_ids[]}` |
| `delivery.sent` | D4 | `{delivery_id, number, manifest_sha256, channel_kind, contact_ids[], intake_ids[], work_item_ids[], acceptance_due_at}` |
| `acceptance.received` | D6 | `{delivery_id, acceptance_id, result, source, receipt_sha256, intake_contact_id, intake_ids[], accepted_work_item_ids[], rejected_items[{delivery_item_id, work_item_id, feedback_category, comment_length}]}`（comment 正文不进事件，消费者经 `AcceptanceReader.Get` 读取） |

### 7.3 消费的领域事件

| 事件 | 处理 | 幂等 |
|---|---|---|
| `execution.cost_updated` | `delivery.cost_apply` | `event_id` 唯一 + cursor 单调 |
| `intake.converted` | 若带 `project_id` 且项目 stage=INTAKE → 健康度任务提示 S1 | 只读评估 |
| `intake.received`（`origin_acceptance_id` 非空） | 回填 `acceptances.rework_intake_id` | 已有值则跳过 |
| `work_item.state_changed`、`decision.made` | 触发健康度防抖、刷新交付草稿中 `delivery_item_not_accepted` 提示（ACCEPTED→REWORK 时，READY 交付不受影响因 DELIVER 已冻结；DRAFT 标红） | 只读 |
| `delegation.granted` / `delegation.revoked` / `delegation.expired`、`escalation.triggered` | `delivery.autonomy_watch`；`escalation.triggered` 的 hold 由 08 经 `ProjectHoldSetter` 同步写入，本事件仅刷新健康度 | hold 集合语义 |
| `agent.retired`、`agent.state_changed` | BR-07-07、BR-07-16 | 条件更新 |

### 7.4 审计事件（底座哈希链，业务事务内 `audit.Appender`）

`delivery.project_created`、`delivery.project_updated`（含 portal_settings / acceptance_policy 变更 diff 的键名）、`delivery.project_stage_changed`、`delivery.project_stage_denied`、`delivery.project_mode_changed`、`delivery.project_mode_change_denied`、`delivery.project_budget_cap_changed`、`delivery.budget_threshold_reached`、`delivery.project_paused`、`delivery.project_resumed`、`delivery.delivery_decided`（DELIVER，detail：reason、item_ids、manifest_sha256、authority_source、reauth purpose）、`delivery.delivery_decision_denied`、`delivery.delivery_withdrawn`、`delivery.delivery_sent`、`delivery.acceptance_received`（detail：result、source、receipt_sha256、recorded_by?）。detail 至少含 `reason`、`evidence_refs[]`、`authority_source`（00 §7.2）。

---

### 7.5 D112 / D113 增补任务与事件

- 任务：`delivery.tolerance_forecast`（每小时，dlock `delivery:tolerance-forecast:<tenant>`；另由 `work_item.state_changed`、`execution.cost_updated` 去抖 60 s 触发）；`delivery.metrics_snapshot` 增加 §3.17b 各列。
- 发布：`project.constitution_activated{project_id, version_no, sha256}`；`exception.raised{subject_kind, subject_id, breached_dimensions}`、`exception.decided{decision}`（00 §15.5，subject_kind ∈ PROJECT / REQUIREMENT）；`quality.escaped{stage:CLIENT_ACCEPTANCE, work_item_id, acceptance_id, escape_key}`（`escape_key=(work_item_id, acceptance_id)`；同一 `escape_key` 已发布时不再发布，学习只经 02 `delivery.escape_learning`，D114）；`project.mode_changed` 载荷 `to` 取值增加 `HUMAN_ONLY`。
- 消费：`internal_qa.passed` / `internal_qa.failed`（02 / 03）、`quality.escaped{stage:HUMAN_REVIEW}`（02）——仅用于指标累计。
- 审计：`delivery.constitution_activated` / `_rejected`、`delivery.tolerance_changed`、`delivery.exception_decided`。

## 8. 页面规格

应用 `apps/frontend-web-admin/apps/delivery`；共享组件取自 00 §10（`StateBadge`、`ActorAvatar`、`AuthorityNote`、`CountdownRing`、`DecisionPanel`、`EvidenceCard`）。本规格新增 delivery 应用内组件：`StageStepper`、`BudgetRing`、`ModeBadge`、`HealthDot`、`KpiTile`、`AcceptanceTimeline`（不进 `packages/ui`，只此一处使用）。所有危险操作使用底座 ReauthenticationDialog。

**通用状态**（每页适用，下文不再重复）：加载 = 与布局同形骨架；错误 = 卡片内错误说明 + 〔重试〕；无权限 = "你没有 `<权限码>` 权限，请联系团队所有者"；离线 = 顶部琥珀色"已断开，正在重连…"，所有写按钮禁用并带提示"离线时不可操作"，重连后自动刷新；版本冲突 409 = "内容已被他人更新"〔查看最新〕。

### 8.1 项目列表 `/t/:tenantID/projects`

- **目的**：项目组合一屏扫视，找出不健康项目。
- **布局**：标题栏（〔新建项目〕需 `project_manage`）→ 筛选条（阶段多选、模式、健康度、甲方分组、搜索名称 / 代号、"显示已归档"开关）→ 视图切换（卡片 / 表格，记住在 localStorage）→ 卡片网格（按甲方分组折叠，未分组在最后）。
- **卡片字段**：名称 + 代号；`HealthDot`（GREEN/AMBER/RED，悬停列出 `health_reasons` 文案）；`StageStepper` 迷你版（9 段中显示主线 7 段，BLOCKED 以当前段虚线 + "阻塞" 标签表示，CANCELLED 整条灰显 + 删除线标签）；`ModeBadge`（混编 = 中性 · 人 + 芯片图标；全自治 = 紫色芯片图标 + "全自治"）；`BudgetRing`（spent/cap 百分比，<notify 青、≥notify 琥珀、≥100 红并显示"已暂停"；cap 为空显示"不限"与已花费金额）；计数：待你裁决 n、进行中工作项 n、待甲方验收 n；目标日（过期红色）；主管 Agent 头像。
- **表格列**：名称、甲方、阶段、模式、健康、预算 %、待裁决、待验收、目标日、更新时间；可排序列同接口 #2。
- **空态**："还没有项目。项目把需求、工作项、交付和预算放在一起。"〔新建项目〕（无权限时改为"请联系交付负责人创建项目"）。
- **交互**：卡片点击 → 项目总览；卡片菜单（⋯）：设置、归档（仅终态项目，需 `project_manage`）。
- **实时**：SSE 订阅集合主题 `tenant/{t}/projects`（09 主题文法），健康度 / 阶段 / 预算变化 ≤1s 更新单卡，不整页刷新。
- **移动端**：单列卡片；筛选收入底部 Sheet；表格视图隐藏。
- **文案**：健康原因用人话，如"预算已用 83%"、"甲方验收已逾期 2 天"。

### 8.2 新建项目向导 `/t/:tenantID/projects/new`（也可从来件"转为需求 → 新建项目"带 `origin_intake_id` 进入）

- **步骤**（顶部 4 步进度，可回退，最终一次提交 #1，带 Idempotency-Key = 向导会话 UUID）：
  1. **基本信息**：名称*（≤200，失焦校验同甲方下重名）、代号*（自动从名称生成大写字母建议，正则提示，失焦查重）、甲方分组（下拉选已有 client / "新建分组"内联输入 / 不分组）、简介、目标日（≥今天）。
  2. **甲方联系人**：从 01 联系人搜索选择（来源来件的联系人预填为 PRIMARY）；每人切换"接收交付""可签收"；至少 0 人（允许稍后补），但提示"没有可签收联系人将无法正式交付"。
  3. **团队**：成员选择（人 / Agent 混排，`ActorAvatar` 区分），角色下拉按主体类型过滤（BR-07-02）；"你将成为本项目交付负责人"只读提示；主管 Agent 选择（可空）。
  4. **仓库与预算**：仓库行（名称、远程地址、默认分支、主仓库单选），地址含凭证即时报错；预算上限（美元输入，两位小数，可空 = 不限）、通知阈值滑块 50–95（默认 80）；门户"展示 AI 协作标签"开关（默认开）。
- **提交**：〔创建项目〕；成功 → 跳总览并 toast "项目已创建，已建立 # 频道"；503 `127004` → "暂时无法创建项目（〈缺失能力〉不可用），未产生任何数据"；字段错误回到对应步骤高亮。
- **按钮条件**：下一步需本步必填项有效；创建按钮在提交中禁用并显示进度。
- **离线**：向导内容保存在 sessionStorage（try/catch），恢复连接后可继续。
- **移动端**：步骤条变为"2/4"文本；成员选择用全屏 Sheet。

### 8.3 项目总览 `/t/:tenantID/p/:projectID`

- **目的**：单项目一屏回答"在哪个阶段、能否推进、钱和风险怎样、团队怎么编排"。数据来自 #5，活动来自 08 审计视图（按项目过滤，最近 20 条）。
- **布局**（桌面两栏，左 2/3 右 1/3）：
  - 顶部：名称、代号、`ModeBadge`、`HealthDot`、holds 徽标（如"预算暂停"红、"人工暂停"琥珀）、〔设置〕。
  - **阶段步进条（全宽）**：主线 INTAKE→DISCOVERY→PLANNING→ACTIVE→ACCEPTANCE_PENDING→DELIVERED→CLOSED；当前段高亮；每个合法目标显示为按钮（来自 `stage_readiness`）：条件满足 = 实心按钮〔推进到 规划中〕；不满足 = 描边按钮，点击展开清单（✓/✗ + 原因 + 链接，如"✗ 2 条需求缺验收标准 → 去补"）；`human_only` 且当前用户是 Agent 视角不显示。回退类（S3/S8/S10）与〔标记阻塞〕〔取消项目〕放在"更多"菜单。点击 → 确认对话框：理由（BLOCKED 还需类型）、`AuthorityNote`"你以 交付负责人 身份 · 来源 ROLE:delivery_lead"；取消项目为红色语义 + 后果列表（将取消 n 个工作项、停止 n 个执行、撤销 n 条委托）+ 输入项目代号确认 + step-up。
  - 左栏卡片：**需求基线**（当前版本 vN、需求数、验收标准数、按状态的工作项分布条，点击进工作项看板）；**交付**（草稿 / 待发送 / 待验收计数、最近一次结果，〔新建交付〕需 `delivery_compose` 且阶段允许）；**近期活动**（时间线，主体头像 + 动作 + 对象，裁决类不折叠）。
  - 右栏卡片：**预算**（`BudgetRing` 大号、cap / spent / 分解、〔调整预算〕需 `project_manage` 且主体为人）；**模式与策略**（模式、派单策略、委托数与最早到期 `CountdownRing`、升级规则 4 类勾选状态；〔切换为全自治〕需 `project_mode_change`：先调 #10 展示 P1–P5 清单，全部通过才可提交，提交需理由 + step-up；全自治时显示〔切回混编〕与〔一键接管〕（后者跳 08））；**团队编排**（主管 Agent、派单策略、并行上限、人 / Agent 人数 → 链接到 05 团队编排页）；**风险**（`risks[]` 列表，RED 在前，每条带跳转）；**初始化问题**（仅 provisioning_issues 非空时，〔重试〕）。
- **按钮可用条件汇总**：推进 = `project_stage_change` ∧ allowed ∧ 非只读；暂停 = `decision_pause` ∧ 无 MANUAL hold；恢复 = 存在 MANUAL/ESCALATION hold（ESCALATION 仅人）；终态项目全部写按钮隐藏，顶部显示"项目已关闭（只读）"。
- **空态**：新项目各卡片显示下一步引导（基线卡："还没有需求。〔从来件转入〕〔新建需求〕"）。
- **实时**：SSE `tenant/{t}/project/{id}`：阶段、holds、预算、风险 ≤1s 更新；他人推进阶段时步进条动画过渡并 toast"Morgan 将项目推进到 进行中"。
- **移动端**：单栏，顺序：步进条（横向滚动，当前段居中）→ 风险 → 预算 → 模式 → 基线 → 交付 → 活动；阶段操作收进底部操作栏〔推进〕〔更多〕。
- **文案**：阶段中文名：来件、澄清中、规划中、进行中、阻塞、待验收、已交付、已关闭、已取消。

### 8.4 项目设置 `/t/:tenantID/p/:projectID/settings`

- **分区标签**：常规（名称、简介、甲方分组、目标日、代号只读）｜团队（人与 Agent 混排的项目成员表，`ActorAvatar` 区分，角色下拉按主体类型过滤；〔添加成员〕#46、移除 #47；有活跃分派的成员移除按钮禁用并提示「先改派」；最后一名交付负责人不可移除；委托载体角色不在可选范围，显示「委托请在 授权与策略 中授予」）｜联系人（列表 + 增删 + 角色切换，删除最后可签收联系人且有待验收交付时禁用并说明）｜仓库（列表、新增、归档）｜预算（cap、阈值、预算事件表）｜门户可见性（6 个开关 / 单选，右侧实时"门户预览"卡片显示甲方将看到什么）｜验收策略（验收期限天数、提醒天数标签输入、升级天数；"逾期视同验收"显示为禁用开关 + 说明"MVP 不支持：验收只能来自甲方"）｜危险区（归档、取消项目）。
- **校验**：提醒天数升序且 < 升级天数；阈值 50–95；地址无凭证。每个分区独立保存（PATCH 带 version），未保存离开提示。
- **权限**：无 `project_manage` 时全部只读并显示说明；Agent 视角预算区只读并标注"预算只能由人调整"。
- **移动端**：标签变为纵向列表进入子页。

### 8.5 交付列表 `/t/:tenantID/deliveries`（团队）与 `/t/:tenantID/p/:projectID/deliveries`（项目）

- **列**：编号、标题、项目（团队视图）、状态 `StateBadge`（草稿中性、待发送青、已发送琥珀"待甲方验收"、已接受绿 + 盾、部分接受琥珀 + 盾、已拒收红）、项数、证据包状态、发送时间、验收倒计时（SENT 时 `CountdownRing` 到 `acceptance_due_at`，逾期红"逾期 n 天"）、结果时间。
- **筛选**：状态、项目、时间范围、编号搜索；默认排序：待处理优先（READY 发送失败 > SENT 逾期 > SENT > DRAFT > 终态）。
- **按钮**：〔新建交付〕（项目视图，`delivery_compose` ∧ 阶段允许）。
- **空态**："还没有交付。交付只能包含团队已接受的工作项。"〔新建交付〕；若项目无 ACCEPTED 工作项则按钮禁用并提示"先让工作项通过团队裁决"。
- **实时**：发送状态、回执到达 ≤1s 更新行并高亮 3 秒。
- **移动端**：卡片列表，字段：编号 + 状态 + 倒计时。

### 8.6 交付包编辑器 `/t/:tenantID/p/:projectID/deliveries/:deliveryID/edit`（仅 DRAFT；非 DRAFT 访问自动转详情页）

- **布局**（桌面三栏）：
  - 左 **可选工作项**：项目内 ACCEPTED 工作项列表（搜索、按需求分组），每项显示 `CompletionLadder`（L3）、是否已被甲方接受（已接受且未返工的灰显不可选并说明 128005）、是否在途（灰显 128004）；勾选即 PUT #31。
  - 中 **交付内容**：已选项（拖拽排序）→ 展开每项：验收标准 ↔ 证据映射（`EvidenceCard`，模式选择 SUMMARY/FILE/FULL_LOG，LOG 无 FILE 选项；未覆盖的标准琥珀色）；标题 / 说明 / 演示链接表单；**发布说明**编辑器（Markdown，左编辑右预览）：〔请 Agent 起草〕（#37，无可起草 Agent 时禁用并说明）、草稿列表抽屉（Agent 头像 + 时间 + 〔采纳并编辑〕；采纳后来源标"Agent 草稿 · 待你确认"，人任意编辑一次后标"已由〈你〉定稿"）。
  - 右 **检查与操作**：校验清单实时显示 BR-07-27 ①–⑧（✓/✗ + 修复链接）；证据包卡（状态：未生成 / 生成中进度 / 已生成〈大小、manifest 哈希前 8 位可复制〉/ 已过期"内容已变更，需重新生成" / 失败〈原因：如"发现疑似密钥，已阻止生成：evidence/ACME-42/app.log 第 3 处"〉）〔生成证据包〕；脱敏摘要（剔除文件数、替换处数）；收件人（联系人复选，来自可签收且接收交付者）；〔正式交付〕。
- **〔正式交付〕可用条件**：`delivery_send` ∧ 主体为人 ∧ 清单 ①–⑧ 全过；点击 → 红色语义确认对话框：交付编号、项数、收件人、渠道、manifest 哈希、"交付后不可撤回（发出后）"、`AuthorityNote`、可选"同时把项目推进到 待验收"复选（仅 stage=ACTIVE）、理由 → step-up → 提交 #39。无 `delivery_send` 者看到的是〔请求正式交付〕（#49，清单 ①–⑧ 全过才可点，发 `delivery.confirmation_requested`，08 生成「交付确认」收件箱项），不出现交付按钮；已请求时显示「已于〈时间〉请求 〈人〉 正式交付」。全自治项目由系统自动编排的草稿顶部标「系统已自动编排 · 待你正式交付」。
- **实时**：他人编辑同一交付 → 顶部提示"Morgan 正在编辑"并在保存冲突时给出合并提示；证据包生成进度经 SSE 推送；工作项被重开（ACCEPTED→REWORK）时该项标红"已不再是已接受状态"。
- **移动端**：只读预览 + 发布说明编辑 + 〔正式交付〕；工作项与证据选择提示"请在大屏上编辑"。

### 8.7 交付详情 `/t/:tenantID/p/:projectID/deliveries/:deliveryID`

- **布局**：头部（编号、标题、`StateBadge`、项目链接、manifest / 包哈希可复制、〔下载证据包〕〔下载回执〕）→ **验收时间线 `AcceptanceTimeline`**（纵向：创建 → 证据包生成 → DELIVER 裁决〈人、身份来源、理由〉→ 每次发送尝试〈联系人、渠道、成功 / 失败原因〉→ 提醒〈第 3/7/12 天〉→ 升级 → 回执〈签署人、来源、时间、receipt 哈希〉→ 返工来件链接）→ **逐项结果表**（工作项、甲方结果 ✓/✗、反馈类别、意见、关联返工来件 / 工作项状态）→ 发布说明（只读）→ manifest 结构化查看（折叠树）。
- **按钮**：READY 且无成功发送 → 〔撤回为草稿〕（`delivery_send`）；READY 发送失败 / SENT → 〔重新发送〕；SENT → 〔登记线下验收〕（`delivery_send`，打开逐项表单 + 凭证上传 + step-up）；终态无写按钮。
- **空 / 特殊态**：SENT 未回执显示大号 `CountdownRing`"等待甲方验收 · 还剩 9 天"；PARTIALLY_ACCEPTED 顶部提示"已为被拒的 2 项生成返工来件"〔前往分诊〕。
- **实时**：SSE `tenant/{t}/delivery/{id}`；回执到达即在时间线追加并 toast。
- **移动端**：时间线优先，逐项结果为卡片。

### 8.8 指标页 `/t/:tenantID/p/:projectID/metrics` 与团队 `/t/:tenantID/metrics`

- **KPI 磁贴（`KpiTile`，同一定义供 08 总览复用 `DeliveryMetricsReader`）**：

| 磁贴 | 主值 | 副行 | 趋势 | 颜色规则 |
|---|---|---|---|---|
| 每个已接受交付物成本 | `$27.50` | "$412.50 / 15 项" | 与上一同长窗口比较 ↑↓ | 无阈值，中性（上升以琥珀箭头提示） |
| 首次验收率 | `73.3%` | "11 / 15" | 同上 | ≥80% 绿，60–80% 琥珀，<60% 红（本规格提议） |
| 人工关注分钟 / 交付物 | `10.0 分钟` | "共 150 分钟" | 同上 | 中性 |
| 返工周期 | `均值 9.0 小时` | "P50 ≤4 小时 · P90 ≤48 小时 · 4 次" | 同上 | 中性 |
| 证据覆盖率 | `94.4%` | "17 / 18 · 标准覆盖 88.0%" | 同上 | ≥90% 绿，否则琥珀（PRD 目标） |
| 甲方一次验收率 | `60.0%` | "3 / 5" | 同上 | ≥80% 绿，50–80% 琥珀，<50% 红（本规格提议） |

  分母为 0 显示"—"与"暂无数据"；每个磁贴 ⓘ 显示公式原文（BR-07-42..47）与窗口说明"含今天的未定稿数据"。
- **控件**：窗口（7 / 30 / 90 天 / 自定义日期范围 ≤366 天）；团队视图加项目多选；下方日序列图（#23，按 dataviz 规范）与"按项目"表（团队视图，CPAD 降序）。
- **无权限 / 空**：无可读项目 → "没有你可查看的项目指标"。
- **移动端**：磁贴两列网格，图表横向滚动。

---

### 8.9 D112 / D113 增补页面

- **项目设置 →「宪法」标签**：当前 ACTIVE 版本（只读 Markdown 渲染）、版本历史与差异对比、提议新版本（编辑器 + 变更说明必填）、评审（仅人类：通过 / 驳回 + 意见）；项目未进入 ACTIVE 前若无 ACTIVE 宪法，阶段推进按钮禁用并提示原因。
- **项目总览 →「预算与容差」卡片**：时间 / 成本 / 范围三条，各显示 预算、容差带、当前预测（低置信时虚线）、状态 OK / AT_RISK / EXCEPTION；EXCEPTION 时卡片置顶为红色横幅并链接例外页。
- **例外页** `/t/:tenantID/p/:projectID/exceptions/:exceptionID`：越界维度与预测快照、例外报告（现状 / 选项 / 建议，系统起草可编辑）、四个决定按钮（继续 / 重新塑形 / 暂停 / 终止），仅持 `exception_decide` 的人类可见可用，终止需 step-up；Agent 视图只读。
- **团队模式选择**增加「纯人工 + AI 协助」：说明 Agent 只做助理；从全自治切换时提示将撤销委托。
- **指标页**新增三张磁贴：穿透率、内部一次通过率、平均内部轮次（附趋势与按模板拆分）。
- **甲方门户**：不新增元素；回归要求门户视图与证据包中不出现任何内部质量门信息（BR-07-60）。

## 9. 通知（09 `Notifier`；收件箱由 08 聚合）

| 事件 | 接收人 | 渠道 | 文案模板（zh-CN） | 去重 / 合并 |
|---|---|---|---|---|
| 项目创建 | 被绑定的成员（人） | 站内 | "你已加入项目〈名称〉，角色：〈角色〉" | 每人每项目一次 |
| 可推进阶段建议（`project.stage_ready`） | 项目交付负责人 | 站内（非收件箱） | "〈项目〉已满足进入〈阶段〉的条件" | 同项目同目标 24h 一次 |
| 阶段变化 | 项目成员（人） | 站内 | "〈主体〉将〈项目〉推进到〈阶段〉" | 无 |
| 项目阻塞 > 3 天 | 交付负责人、所有者 | 站内 + 邮件 | "〈项目〉已阻塞 3 天：〈原因〉" | 每次阻塞一次 |
| 预算 80% | 交付负责人、所有者 | 站内 + 邮件 | "〈项目〉预算已用 〈pct〉%（$〈spent〉 / $〈cap〉）" | 每 cap_version 一次（唯一键保证） |
| 预算 100% 暂停 | 同上 + 正在执行的工程师 | 站内 + 邮件，收件箱高优先 | "〈项目〉预算已用尽，自动执行已暂停。调整预算后可恢复" | 同上 |
| 模式切换 / 接管 | 所有者、交付负责人、项目 Agent（频道事件） | 站内 + 邮件 | "〈主体〉已将〈项目〉切换为〈模式〉：〈理由〉" | 无 |
| 自治前置条件失效 / 恢复 | 所有者 | 站内 + 邮件 | "〈项目〉全自治已受限：〈条件〉不再满足，自动派单与委托裁决已暂停" | hold 存在期间不重复 |
| 委托 72h 内到期 | 所有者 | 站内 | "〈Agent〉在〈项目〉的委托将于〈时间〉到期" | 每条委托一次 |
| 交付待决定（`delivery.confirmation_requested`） | 有 `delivery_send` 的项目人类成员；AUTONOMOUS 另含所有者 | 收件箱（08 路由）+ 邮件 | "〈主体 / 系统〉请你正式交付〈编号〉" | 同交付同一次请求一次；D3/D5 关闭 |
| 已接受未交付 >24h（`ACCEPTED_UNDELIVERED`） | 交付负责人；AUTONOMOUS 所有者 | 站内 | "〈项目〉有 n 项已接受工作项尚未交付" | 每项目 24h 一次 |
| 证据包生成失败 | 发起人 | 站内 | "〈编号〉证据包生成失败：〈原因〉" | 每次构建一次 |
| 发送失败（重试耗尽） | DELIVER 决定人、交付负责人 | 站内 + 邮件 | "〈编号〉未能送达〈联系人〉：〈原因〉" | 每交付一次 |
| 甲方验收提醒 | 甲方联系人（经 01 渠道） | 门户 / 邮件 / API 回调 | 01 模板，变量：编号、标题、剩余天数、门户链接 | 同联系人 24h 一次 |
| 验收逾期升级 | 交付负责人 | 收件箱 + 邮件 | "〈编号〉已发出 14 天仍未验收" | 每交付一次 |
| 回执到达 | DELIVER 决定人、交付负责人、项目频道 | 站内 + 频道事件 | "甲方〈签署人〉对〈编号〉的验收结果：〈结果〉" | 无 |
| 拒收 / 部分接受 | 同上 + 所有者（AUTONOMOUS） | 收件箱高优先 | "〈编号〉有 n 项被拒，已生成返工来件" | 无 |

通知正文不含交付内容、评论正文与代码（只给链接）。

---

### 9.x D112 / D113 增补通知

| 事件 | 接收人 | 渠道 | 模板 |
|---|---|---|---|
| `exception.raised` | 项目 `exception_decide` 持有人（人类）+ 所有者 | 站内（强制）+ 邮件 | 「{项目}{维度}预计越界：预测 {forecast}，预算 {appetite}。请提交例外报告并决定。」 |
| 宪法提交评审 | 项目 `project_manage` 人类 | 站内 | 「{提议人}提议项目宪法 v{n}：{摘要}」 |
| 容差 AT_RISK | 交付负责人 | 站内（每主体每日最多 1 次） | 「{主体}预测接近预算上限（{dim}）」 |

内部质量门的失败、返工**不产生任何通知**（D113，统一由 08 路由保证）。

## 10. 验收标准

```
AC-07-01  [integration]  关联 US-07-01, BR-07-01, BR-07-02
Given 租户 T 中 Morgan 具租户级 delivery_lead，联系人 C1 存在，Agent A1(agent_executor) 活跃
When  POST /tenants/T/projects {name:"官网改版", code:"ACME-WEB", contacts:[{C1,PRIMARY}], members:[{A1,agent_executor}], budget_cap_micros:500000000}
Then  201；iam_entities 新增 type=project 实体 E；delivery_projects(E) stage=INTAKE、team_mode=HYBRID；
      角色绑定含 (Morgan, delivery_lead, E) 与 (A1, agent_executor, E)；collab 存在频道 "# 官网改版"；
      审计链含 iam entity_created 与 delivery.project_created；outbox 含 project.created 一条

AC-07-02  [integration]  关联 BR-07-01
Given 注入故障：collab ChannelProvisioner.CreateDefaultOn 返回错误
When  同 AC-07-01 请求
Then  返回 5xx；iam_entities、delivery_projects、role_bindings、files 实体根、outbox、审计均无新增行（逐表计数前后相等）

AC-07-03  [integration]  关联 BR-07-01
Given collab 模块被禁用（ChannelProvisioner 未注册）
When  创建项目
Then  503，业务码 127004；无任何行写入；启动日志含一次 slog.Warn 指明缺失能力

AC-07-04  [integration]  关联 BR-07-03
Given 项目 E 创建成功，06 的 project.created 消费者连续 8 次失败
When  第 8 次失败后
Then  delivery_projects(E).provisioning_issues 含 {step:"knowledge_scope"}；health=AMBER，risk PROVISIONING_ISSUE；
      PUT …/provisioning-retries/knowledge_scope 重新投递，消费成功后该项移除

AC-07-05  [integration]  关联 BR-07-04
Given 项目实体 E
When  通用 DELETE /entities/E 或 PATCH /entities/E {type:"team"}
Then  409 entity_type_reserved；实体不变

AC-07-06  [integration]  关联 US-07-03, 4.1
Given 项目 stage=INTAKE
When  POST …/stage-transitions {to_stage:ACTIVE}
Then  409 127010，meta.allowed_targets=["DISCOVERY","CANCELLED"]；无 transition 行；审计无 stage_changed
（同类参数化用例覆盖 INTAKE→PLANNING、DISCOVERY→ACTIVE、ACTIVE→DELIVERED、BLOCKED→ACCEPTANCE_PENDING、CLOSED→ACTIVE、CANCELLED→INTAKE、ACTIVE→ACTIVE）

AC-07-07  [integration]  关联 BR-07-08
Given stage=ACCEPTANCE_PENDING；可交付工作项 W1、W2、W3（FEATURE）；交付 D1 含 W1、W2 已 ACCEPTED；W3 未交付；另有 CHORE 工作项 W4 未交付
When  人类负责人请求 to_stage=DELIVERED
Then  422 127013，meta.missing_work_item_ids=[W3]（不含 W4）
And   当 W3 经新交付 D2 被甲方 ACCEPTED 后再次请求 → 200，stage=DELIVERED，delivered_at 写入

AC-07-08  [integration]  关联 BR-07-11, CCR-07-06
Given 自定义角色把 project_stage_change 授予 Agent A2
When  A2 请求 S9 / S11 / S12
Then  403 120403（reason_code=HUMAN_ONLY）；审计 delivery.project_stage_denied（outcome=denied）；而 A2 请求 S4（PLANNING→ACTIVE，条件满足）→ 200

AC-07-09  [integration]  关联 4.1 S12
Given stage=ACTIVE，有 RUNNING 执行 X、READY 交付 R、SENT 交付 S、DRAFT 交付 Dr、工作项 W(IN_PROGRESS)
When  人类所有者带有效 step-up 请求 to_stage=CANCELLED，reason="甲方终止合作合同"
Then  stage=CANCELLED；02 Decision(CANCEL, target=PROJECT) 一条；W→CANCELLED；Dr、R 归档（R 先回 DRAFT）；S 仍为 SENT；
      outbox project.stage_changed；03 收到后取消 X；后续 PATCH 档案 → 409 127005；S 的回执仍可登记
And   缺 reauth_proof → 403 127027，状态不变

AC-07-10  [integration]  关联 US-07-05, BR-07-14
Given HYBRID 项目，stage=ACTIVE，有 agent_executor A1；无委托；升级规则缺 CLIENT_REJECTION；budget_cap 为空
When  所有者请求 to_mode=AUTONOMOUS（step-up 有效）
Then  422 127021；meta.preconditions = P1✗ P2✗ P3✗ P4✓ P5✓；team_mode 不变
And   补齐委托（ends_at=now+48h）、启用 4 类规则、设 cap 后重试 → 200，team_mode=AUTONOMOUS，dispatch_policy=AUTO（此前未显式设置），
      mode_changes.preconditions_snapshot 全部 passed，审计 delivery.project_mode_changed
And   委托 ends_at=now+23h 时 → 422 127021

AC-07-11  [integration]  关联 BR-07-15, BR-07-17, US-07-06
Given AUTONOMOUS 项目
When  Agent 评审员 A3 以自身凭证请求 to_mode=HYBRID
Then  403 120403（reason_code=NON_DELEGABLE），审计 outcome=denied
When  所有者经 08 一键接管
Then  同一事务内：项目委托全部 REVOKED、team_mode=HYBRID、dispatch_policy=MANUAL、mode_changes.trigger=TAKEOVER 且 revoked_delegation_ids 非空

AC-07-12  [integration]  关联 BR-07-18, US-07-07
Given cap=$100（100_000_000 micros），notify 80%，spent=$70
When  execution.cost_updated(E1, cumulative=$15) 投递 3 次（同 event_id），再投递乱序旧事件(E1, cumulative=$12, 新 event_id)
Then  spent=$85；cost_entries 仅 1 行 delta=$15；budget_state=NOTIFIED；THRESHOLD_NOTIFY 行 1 条；budget.threshold_reached{threshold_pct:80} 1 条；通知 1 次

AC-07-13  [integration]  关联 4.3, BR-07-12, BR-07-22
Given 同上 spent=$85
When  cost_updated(E2, cumulative=$20)
Then  spent=$105（超支照记）；EXHAUSTED；holds 含 BUDGET；THRESHOLD_EXHAUSTED 1 条；事件 {threshold_pct:100} 1 条
And   ProjectGate.CanStartExecution(AUTONOMOUS) = false(127032)；再来 cost_updated(E2, cumulative=$22) → spent=$107，不新增阈值事件与通知
When  所有者 PUT budget cap=$200，reason="追加二期"
Then  cap_version+1；state=NORMAL；BUDGET hold 移除，HOLD_RELEASED 1 条；已暂停执行不自动恢复

AC-07-14  [integration]  关联 BR-07-18（并发）
Given spent=$79，cap=$100
When  两个不同执行的 cost_updated 各 +$1 并发处理
Then  spent=$81；THRESHOLD_NOTIFY 恰好 1 条（唯一键），通知恰好 1 次

AC-07-15  [integration]  关联 BR-07-21
When  Agent 请求 PUT budget → 403 120403（reason_code=HUMAN_ONLY）；AUTONOMOUS 项目人类置 cap=null → 422 127033；cap 低于 spent 且未确认 → 422 127035

AC-07-16  [integration]  关联 BR-07-24
Given 工作项 W1 ACCEPTED、W2 NEEDS_HUMAN_REVIEW、W3 属其他项目、W4 在 SENT 交付中、W5 最新 APPROVE 已被甲方 ACCEPTED
When  分别 PUT /deliveries/D/items/{W}
Then  W1 200；W2 422 128002；W3 422 128003；W4 409 128004；W5 409 128005

AC-07-17  [integration]  关联 BR-07-27（组装后状态变化）
Given 草稿 D 含 W1，证据包 BUILT；之后 02 将 W1 ACCEPTED→REWORK
When  人类负责人 DELIVER（step-up 有效）
Then  422 128002，meta.work_item_ids=[W1]；D 仍 DRAFT；无 Decision；无 outbox

AC-07-18  [integration]  关联 BR-07-26, BR-07-27
Given D 证据包 BUILT 后编辑了发布说明
Then  bundle_state=STALE；DELIVER → 422 128008；重建后指纹一致，DELIVER 通过

AC-07-19  [integration]  关联 BR-07-27 ⑥, US-07-09
Given 发布说明由 Agent 草稿采纳且无人编辑（release_notes_actor_kind=AGENT）
When  DELIVER → 422 128009
When  人修改一个字后 DELIVER → 通过

AC-07-20  [integration]  关联 BR-07-27 ⑨, 00 §6.4
Given Agent A3 被（错误地）授予含 delivery_send 的自定义角色，且持有有效委托
When  A3 POST /deliveries/D/decisions {action:DELIVER}
Then  403 120403（reason_code=HUMAN_ONLY）；D 仍 DRAFT；审计 delivery.delivery_decision_denied（outcome=denied, authority_source 记录尝试的来源）
And   人类以 PAT（无 Cookie）调用 → 403 127027

AC-07-21  [integration]  关联 BR-07-27（并发）
Given 草稿 D1、D2 都含 W1
When  两位负责人同时 DELIVER D1 与 D2
Then  恰好一个成功（READY）；另一个 409 128004；W1 的 active_lock 仅一行

AC-07-22  [unit]  关联 BR-07-28
Given 证据：app.log 含 "AWS_SECRET=..."、"ghp_" 令牌与 /Users/morgan/ 路径；文件 .env；截图 png
When  构建 FULL_LOG + FILE 证据包
Then  app.log 入包文本中三处被替换为 [REDACTED:*] / 标签，redaction_count=3，bundled_sha256≠original_sha256；
      .env 未入包，excluded_reason=DENY_LISTED_FILE；png 原样入包；同输入构建两次 bundle_sha256 相同

AC-07-23  [unit]  关联 BR-07-28 R7
Given 某文本证据扫描超时
Then  bundle_state=FAILED，error=REDACTION_FAILED，files 中无新 zip

AC-07-24  [integration]  关联 4.4 D3–D4, BR-07-30
Given DELIVER 成功（advance_stage_to_acceptance_pending=true，stage=ACTIVE）
Then  同事务：D READY、Decision(DELIVER)、项目 stage=ACCEPTANCE_PENDING
When  01 对 PRIMARY 联系人 SendDelivery 首次失败、第二次成功
Then  sends 2 行；D=SENT；acceptance_due_at=sent_at+14d；提醒行 4 条（3/7/12 天 REMINDER + 14 天 ESCALATION）；outbox delivery.sent 1 条

AC-07-25  [integration]  关联 4.4 非法迁移
Then  对 SENT 交付 PUT withdrawal → 409 128012；对 READY 交付 PATCH → 409 128001；对 DRAFT 登记回执 → 409 128030；对 ACCEPTED 交付再次 DELIVER → 409 128010

AC-07-26  [integration]  关联 US-07-11, US-07-12, BR-07-31, BR-07-38, BR-07-39
Given SENT 交付 D（W1、W2、W3）
When  甲方经门户提交：W1 ACCEPTED，W2 REJECTED{FUNCTIONAL_DEFECT,"登录后跳转到空白页"}，W3 ACCEPTED，manifest 哈希正确
Then  D=PARTIALLY_ACCEPTED；items.client_result 回填；receipt_sha256 写入且审计链含该哈希；outbox acceptance.received{rejected_items:[W2]}；
      01 创建 1 条返工来件（关联 D 与 acceptance），本模块回填 rework_intake_id；W2 状态仍为 ACCEPTED（不自动重开），L4 显示"甲方拒收"
And   同一回执重放（同 client_request_id）→ 200 返回首份；不同内容 → 409 128033

AC-07-27  [integration]  关联 BR-07-32, BR-07-33, BR-07-31
Then  REJECTED 项无评论 → 422 128032；manifest_sha256_ack 为旧包哈希 → 422 128034；只回执 2/3 项 → 422 128031

AC-07-28  [integration]  关联 BR-07-36, BR-07-37
Given SENT 交付，甲方 30 天无回执（时钟推进）
Then  第 3、7、12 天各 1 次提醒（经 01）；第 14 天升级通知 1 次、risk ACCEPTANCE_OVERDUE、health=RED；交付始终为 SENT，无任何系统生成的回执
And   PATCH acceptance_policy {deemed_acceptance_days:30} → 422 127045

AC-07-29  [integration]  关联 BR-07-35
When  负责人登记线下验收但未附凭证 → 422 128035；附凭证 + step-up → 200，source=RECORDED，actor_kind=CLIENT，recorded_by=负责人

AC-07-30  [integration]  关联 3.1
Given 负责人仅在 client 实体 K 上有 delivery_lead 绑定，项目 E 的父为 K
Then  对 E 的 GET …/project 返回 200（继承生效）；对另一甲方下项目 → 404

AC-07-31  [unit]  关联 BR-07-41..47（公式夹具）
Given 项目 P 在 2026-10-01..07 的日快照合计：cost_micros=412_500_000；accepted_first_count=15；first_pass_count=11；
      attention_ms=9_000_000；rework_cycles=4，时长 2h、5h、26h、3h（rework_ms_sum=129_600_000，hist=[0,2,1,0,1,0,0,0,0]）；
      claimed_items=18，evidenced_items=17，criteria_total=50，criteria_covered=44；first_receipts=5，first_receipts_accepted=3（另 1 PARTIAL、1 REJECTED）
When  GET metrics?window=custom&from=2026-10-01&to=2026-10-07
Then  cpad=$27.50；first_pass_rate=73.3%；attention_min_per_accepted=10.0；rework_mean=9.0h（32_400_000 ms），P50=4h 桶，P90=48h 桶；
      evidence_coverage=94.4%；criteria_coverage=88.0%；client_first_acceptance_rate=60.0%；每项返回 numerator/denominator
And   accepted_first_count=0 的窗口 → cpad、first_pass_rate、attention 均为 null

AC-07-32  [unit]  关联 BR-07-42, BR-07-43
Given W1 首次 ACCEPTED 于 10-02，10-04 被 REQUEST_CHANGES，10-05 再次 ACCEPTED
Then  10-02 accepted_first_count+1、first_pass_count+1；10-05 accepted_first_count 不变；rework_cycles(10-05)+1，时长=10-04→10-05

AC-07-33  [integration]  关联 BR-07-48
Given D-1 快照已算；之后迟到 1 条 D-1 的 cost 事件
When  次日 00:20 任务运行
Then  D-1 行 cost_micros 更新；D-4 行 is_final=true 且不再改写；任务重复运行结果不变

AC-07-34  [integration]  关联 BR-07-54, BR-07-55
Then  相同 Idempotency-Key 重放 POST /entities/E/deliveries → 返回同一交付，sequence_no 不增；同 key 不同 body → 422 176425；
      PATCH 带过期 If-Match → 409 120409，meta.current_version 正确；不带 If-Match → 428 120400

AC-07-35  [integration]  关联 BR-07-50
Then  无 E 项目范围权限的成员 GET /deliveries/{E 的交付} → 404（非 403）；有 project_read 无 delivery_compose 者 PATCH 交付 → 403 120403，meta.missing_permission=delivery_compose

AC-07-36  [integration]  关联 BR-07-16
Given AUTONOMOUS 项目唯一委托被撤销
Then  ≤1 分钟内 holds 含 AUTONOMY_PRECONDITION；CanAutoDispatch=false、CanDelegatedDecide=false、CanStartExecution(INTERACTIVE)=true；所有者收到 1 次通知；重复撤销事件不再通知

AC-07-37  [e2e]  关联 US-07-08..12
Given 浏览器中负责人在交付包编辑器勾选 2 个已接受工作项、生成证据包、采纳并修改 Agent 草稿
When  点〔正式交付〕→ 重新认证 → 确认
Then  页面 ≤1s 显示"待发送"→"待甲方验收"；甲方在门户逐项签收后，交付详情时间线 ≤1s 出现回执节点

AC-07-38  [manual]  关联 §8
Then  375 / 768 / 1024 / 1440 四档下项目列表、总览、交付详情无横向滚动；离线时写按钮全部禁用；触控目标 ≥44px

AC-07-39  [integration]  关联 BR-07-01b, C11
Given 01 在转换事务内调 ProjectCreator.CreateForIntakeOn{name:"票据导出", code 缺省, origin_intake_id=I1, primary_contact_id=C1, actor=人类 H}
Then  同一事务内：实体 + 档案（code 自动生成且租户唯一，team_mode=HYBRID，dispatch_policy=MANUAL，origin_intake_id=I1）+ C1 为 PRIMARY 可签收联系人 + H 的项目 delivery_lead 绑定 + 默认频道；返回 {id, code, channel_id}
And   01 事务随后失败回滚 → 项目、实体、频道均不存在
And   actor 为 Agent（持有 intake_convert 委托）→ 403 120403（reason_code=NON_DELEGABLE），无任何写入

AC-07-40  [integration]  关联 BR-07-30b, C3
Given AUTONOMOUS 项目 P（stage=ACTIVE），工作项 W1、W2 已 ACCEPTED 26 小时且不在任何交付中；P 的发布说明起草 Agent 可用
When  delivery.autonomous_compose 运行
Then  生成 1 个 DRAFT 交付（actor=SYSTEM，authority_source=SYSTEM:delivery.autonomous_compose）含 W1、W2；证据包构建与发布说明起草请求入队；风险 ACCEPTED_UNDELIVERED 出现
And   证据包 BUILT 且 Agent 草稿到达后，系统采纳草稿并发 delivery.confirmation_requested{autonomous:true}；08 为所有者与 delivery_send 持有者各生成 1 条收件箱项
And   人类未修改发布说明直接 DELIVER → 422 128009；修改后 DELIVER（step-up）→ READY，收件箱项关闭；任务再次运行不重复编排
And   以 SYSTEM 身份尝试 DELIVER → 403 120403（reason_code=HUMAN_ONLY）

AC-07-41  [integration]  关联 BR-07-31, C4
Given SENT 交付 D（2 项）
When  01 以 {manifest_sha256_ack 正确, signer_name, confirmed:false, items 2 项} 调 AcceptanceRecorder.Record
Then  422 128036，无回执
When  confirmed:true，item1 ACCEPTED、item2 REJECTED（feedback_category=FUNCTIONAL_DEFECT，comment ≥10 字符）
Then  result=PARTIALLY_ACCEPTED；acceptance.received 载荷含 intake_ids[]（= D.source_intake_ids）、accepted_work_item_ids=[W1]、rejected_items[0].work_item_id=W2
And   01 以 origin_acceptance_id 建返工来件并发 intake.received{origin_acceptance_id} → 本模块回填 rework_intake_id 恰好一次

AC-07-42  [integration]  关联 BR-07-08 ②③, C8
Given stage=ACCEPTANCE_PENDING；W1 被甲方 ACCEPTED；W2 最新结果 REJECTED，其返工来件 R2 被 01 DECLINE；W3 最新结果 REJECTED，在 02 范围变更中被选择「取消」处置（CANCEL 裁决）
When  人类请求 to_stage=DELIVERED
Then  200（W2、W3 不再要求甲方验收）
And   若 R2 仍为 TRIAGED（未终态）→ 422 127013，meta.missing_work_item_ids 含 W2

AC-07-43  [integration]  关联 BR-07-12, C13
Given 项目 stage=PLANNING，无 hold
Then  ProjectGate.CanStartExecution(AUTONOMOUS)=false、CanStartExecution(INTERACTIVE)=true、CanAutoDispatch=false、CanDelegatedDecide=true
And   推进到 ACTIVE 后 CanStartExecution(AUTONOMOUS)=true、CanAutoDispatch=true（AUTO 时）

AC-07-46  [integration]  关联 BR-07-30b, 复审 #3
Given AUTONOMOUS 项目 P 有 ACCEPTED >24h 的可交付项；项目无持 COORDINATOR/EXECUTOR 且可起草的 Agent
When  delivery.autonomous_compose 运行
Then  建 DRAFT 交付、证据包构建；#37 返回 128041；仍发 delivery.confirmation_requested{release_notes_status:"NEEDS_HUMAN"}；收件箱项标「需人工撰写」
And   人直接 DELIVER（发布说明为空）→ 422（BR-07-27 ⑥）；人填写发布说明后 DELIVER（step-up）成功

AC-07-47  [integration]  关联 BR-07-30b, 复审 #3
Given 起草请求已发出（05 已下发 agent.release_notes），30 分钟内无 #36 回写
When  delivery.autonomous_compose 下一周期
Then  发 delivery.confirmation_requested{release_notes_status:"NEEDS_HUMAN"}；之后迟到的草稿仍可经 #36 写入，不重复发确认请求

AC-07-48  [integration]  关联 BR-07-12, 复审 #9
Given AUTONOMOUS 项目 stage=ACCEPTANCE_PENDING（部分验收后有返工），无 hold
Then  CanStartExecution(AUTONOMOUS)=true、CanAutoDispatch=true

AC-07-49  [integration]  关联 BR-07-30, 复审 #12/#13
Given 交付 D source_intake_ids=[I1, I2]，第 2 次发送
When  delivery.send 调 SendDelivery
Then  入参含 attempt_no=2、intake_ids=[I1,I2]；AcceptanceReader.Get 返回 project_id、contact_id、items[].delivery_item_id

AC-07-44  [integration]  关联 BR-07-02b, C16
Given 项目 P 已创建；Agent A1 为 ACTIVE executor，有 1 条 ACTIVE 分派；A2 为 lead_agent
When  PUT /entities/P/project/members/A3 {role_key:agent_executor}
Then  200，core 项目范围绑定写入，project.members_changed{added:[A3]}
When  DELETE …/members/A1 → 422 127048；DELETE …/members/A2 → 204，同事务 lead_agent_id=NULL、dispatch_policy=MANUAL
When  PUT …/members/A3 {role_key:delegated_reviewer} → 422 127043

AC-07-45  [integration]  关联 BR-07-27 ③b, C50
Given 草稿 D 含 W1（ACCEPTED），之后 02 范围变更使 W1 baseline_stale=true
When  人类 DELIVER → 422 128016，meta.work_item_ids=[W1]；D 仍 DRAFT
```

---

### 10.x D112 / D113 增补验收标准

**AC-07-50**（integration）HUMAN_ONLY 阻止 Agent 执行 — Given 项目 HYBRID，Agent A 持有 W 的未开始 EXECUTOR 分派 When 所有者切换到 HUMAN_ONLY Then A 的分派释放、W → READY；随后对 A 派发返回 403 `120403`（reason `AGENT_PROHIBITED`）；对人派发成功。
**AC-07-51**（integration）全自治 → 纯人工 — Given AUTONOMOUS，3 条有效委托 When 切换 HUMAN_ONLY（含 step-up）Then 3 条委托撤销、派单策略 MANUAL、`project.mode_changed{to:HUMAN_ONLY}`；HUMAN_ONLY → AUTONOMOUS 返回 409 `127051`。
**AC-07-52**（integration）宪法激活与执行版本 — Given v1 ACTIVE When v2 由人类评审通过 Then v1 SUPERSEDED、v2 ACTIVE（唯一）；此后开始的执行记录 `constitution_version_no=2`，之前已开始的仍为 1。
**AC-07-53**（unit）宪法 > 64 KB → 422 `127050`；Agent 评审 → 403 `120403`（HUMAN_ONLY）。
**AC-07-54**（integration）预测越界 — Given 项目预算截止 D、时间容差 20%，预测完成 D + 30% When 预测任务运行 Then 状态 EXCEPTION、创建例外记录、`exception.raised`；此时修改截止时间 → 409 `127053`。
**AC-07-55**（integration）例外决定 — Agent 调用决定 → 403（NON_DELEGABLE）；人类未提交报告即决定 → 409 `127054`；提交报告后 CONTINUE → baseline_no=2、状态 OK；KILL 无 step-up → 401/403 step-up 失败。
**AC-07-56**（integration）内部记录不外泄 — Given 工作项 W 内部质量门失败 2 轮后通过并被接受 When 生成证据包并在门户查看交付 Then manifest 与门户响应中不含 AGENT_TEST / AGENT_ACCEPTANCE 结果、内部轮次与反馈（逐字段断言）。
**AC-07-57**（unit）质量指标夹具 — 某周 l3_entered=20、escapes_human=1、escapes_client=1、internal_qa_completed=20、internal_first_pass=14、internal_rounds_sum=31 → 穿透率 10.0%、内部一次通过率 70.0%、平均内部轮次 1.55。
**AC-07-58**（integration）甲方拒收穿透 — Given W 曾通过内部质量门并被接受 When 甲方逐项拒收 W Then 发布 `quality.escaped{stage:CLIENT_ACCEPTANCE}` 且 escapes_client +1；未经过内部质量门的历史工作项（迁移数据）不计入。
**AC-07-59**（integration）变更越界检查（D117，BR-07-62 ①）— Given 需求 R 容差：appetite_end_at=D、时间容差 20%，最近预测完成 D − 1 天；项目 P 无越界 When 01 调 `WouldBreach(R, {schedule_delta_days:10, cost_delta_micros:0, scope_delta_criteria:0})` Then 返回 `[{TIME, REQUIREMENT}]` 与含 forecast_after 的快照，库中无任何写入；增量为 `{0, 0, -1}` → 空集；R 与 P 都无容差行 → 空集。
**AC-07-60**（integration）按甲方确认调整基线（BR-07-62 ②）— Given R 的 REQUIREMENT 行 baseline_no=1、P 的 PROJECT 行按增量只在 TIME 维越界 When 02 在事务内调 `ApplyApprovedChange(R, IN-9, {4, 0, 1})` Then R 行 appetite_end_at+4 天、appetite_scope_criteria+1、baseline_no=2；P 行只调整 appetite_end_at、baseline_no+1；审计 `delivery.tolerance_changed{source:CLIENT_APPROVED_CHANGE, source_intake_id:IN-9}`；外层事务回滚后两行不变。
**AC-07-61**（integration）例外期间拒绝调整 — Given R 的容差 EXCEPTION（open_exception_id 非空）When 调 `ApplyApprovedChange` Then 409 `127055`，R 与 P 两行均不变；人类以 CONTINUE 决定该例外后再调用 → 成功。

## 11. 度量与埋点（不含代码、评论与消息正文）

| 事件 | 属性 |
|---|---|
| `project_created` | `team_mode`, `has_client_group`, `members_human`, `members_agent`, `has_budget`, `from_intake` |
| `project_stage_changed` | `from`, `to`, `actor_kind`, `criteria_failed_before`（之前被拒次数） |
| `project_stage_rejected` | `to`, `error_code` |
| `project_mode_changed` | `from`, `to`, `trigger`, `preconditions_failed[]`（尝试时） |
| `budget_threshold_reached` | `pct`, `team_mode` |
| `project_hold_changed` | `kind`, `added` |
| `delivery_bundle_built` | `items`, `files`, `bytes_bucket`, `redactions`, `excluded`, `duration_ms`, `result` |
| `delivery_decided` | `items`, `advance_stage`, `attention_ms`（打开编辑器到确认） |
| `delivery_sent` | `channel_kind`, `attempts` |
| `acceptance_received` | `result`, `source`, `items`, `rejected`, `days_since_sent` |
| `acceptance_reminder_sent` | `seq`, `kind` |
| `release_notes_draft_adopted` | `edit_distance_bucket` |
| 指标（Prometheus） | `delivery_budget_drift_total`、`delivery_bundle_build_seconds`、`delivery_send_failures_total{channel}`、`delivery_metrics_snapshot_seconds` |

---

### 11.x D112 / D113 增补埋点

`team_mode_changed{to:HUMAN_ONLY}`、`constitution_activated{version_no}`、`tolerance_state_changed{from,to,dims}`、`exception_decided{decision, hours_open}`、`quality_metric_snapshot{escape_rate, internal_first_pass_rate, avg_internal_rounds}`。

## 12. 依赖、风险与待决

### 12.1 依赖

| 依赖方 | 需要的能力 | 状态 |
|---|---|---|
| 底座 `iam/core` | `EntityProvisioner`（`CreateOn` / `UpdateOn` / `PutRoleBindingOn`，在调用方事务内执行，内含实体文件根开通与策略版本递增）；保留实体类型机制（core 根包声明接口，delivery 提供 `{"project"}`，方向同 `identity.PrincipalCredentialStore`） | **需新增**（CCR-07-08） |
| 底座 `iam/authn` | `ReauthenticationConsumer` 新 purpose：`delivery_send`、`team_mode_change`、`project_cancel`、`acceptance_record`（当前白名单仅 5 个） | **需新增** |
| 底座 `files` | ResourceBinding purpose `delivery_bundle`、`delivery_manifest`、`acceptance_record`（resource_kind=`delivery`，owner scope=entity，pinned）；实体文件根下写文件 | 已有机制，注册 purpose |
| 底座 `iam/audit` | `audit.Appender` 同事务追加；denied 结果记录 | 已有 |
| 01 | `IntakeReplySender{SendDelivery, SendAcceptanceReminder}`（同步、幂等；唯一的甲方通知方）；`IntakeReader{Contacts, ChannelAvailable, AttentionByDay, OpenClarifications}`；转换时调 `ProjectCreator.CreateForIntakeOn`；门户 / API 调 `AcceptanceRecorder{ListForIntakes, PortalView, Record}`、`IntakeProgressReader.Progress`；消费 `acceptance.received` 以 `origin_acceptance_id` 建返工来件；转换模式含 `REOPEN_WORK_ITEMS` | 需 01 对齐 |
| 02 | Decision 表支持 target_kind ∈ {WORK_ITEM, DELIVERY, PROJECT}；内部 `recordDecision`；工作项字段：最新 APPROVE 裁决 id、首次 ACCEPTED 时间、质量返工次数；**T26 ACCEPTED→REWORK（甲方拒收触发，经裁决，`source_acceptance_id`）**；CANCEL 级联；工作项字段 `baseline_stale`；ChangeRecord 标记 superseded 工作项 | 需 02 对齐 |
| 03 | `execution.cost_updated` 携带**累计值**与分解；`ExecutionReader{ActiveCount, AttentionByDay, Get}`；`EvidenceReader{ExecutionCost, SecretFlags, 日志摘要}`；消费 `project.stage_changed`/`budget.threshold_reached` 暂停 / 取消执行；调用 `ProjectGate` | 需 03 对齐 |
| 04 | 按机器存 `(project_id, repository_id) → local_path` | 需 04 对齐 |
| 05 | `ChannelProvisioner.CreateDefaultOn`（collab）、频道归档；`AgentDirectory`（主体类型、角色、状态）；团队编排页读写本模块 `lead_agent_id/dispatch_policy/ensemble_max_branches`（经 #4），不另存副本；Lead 自动派单前调 `ProjectGate.CanAutoDispatch`；Agent 起草发布说明 | 需 05 对齐 |
| 06 | 消费 `project.created` 初始化项目 scope；交付 ACCEPTED 入知识库 | 需 06 对齐 |
| 08 | `DelegationReader`、`EscalationRuleReader`（规则 kind 枚举含 4 类）、`AuthorityChecker`、`SystemActions`（登记 `SYSTEM:delivery.autonomous_compose`）；消费 `delivery.confirmation_requested` / `delivery.withdrawn` 生成与关闭「交付确认」收件箱项；`project.stage_ready` 站内通知；接管时调 `ProjectModeSwitcher.TakeOverOn`；规则触发调 `ProjectHoldSetter`；消费 `budget.threshold_reached`、`acceptance.received`；总览读 `DeliveryMetricsReader` | 需 08 对齐 |
| 09 | `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`（主题 `tenant/{t}/projects`、`tenant/{t}/project/{id}`、`tenant/{t}/delivery/{id}`）、`Notifier`、`dlock` | 平台建设中 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 跨模块同事务依赖所有模块共享同一写库 | MVP 单库部署是前提；若未来拆库，BR-07-01 改为带补偿的编排，接口签名（`…On(tx)`）届时需评审 |
| 脱敏规则漏报导致密钥外泄 | fail-closed；默认 SUMMARY；LOG 不放原文；规则版本化 + 单元夹具；03 的秘密标记优先 |
| 成本事件只给增量的实现会破坏幂等 | 契约明确要求累计值（CCR-07-05），对账任务兜底 |
| 甲方长期不回执拖住 DELIVERED | 提醒、升级、线下登记；阶段不因超时自动推进 |
| 日快照按 UTC 切日与团队感知不一致 | 文案标注"按 UTC"；v1.1 租户时区 |

### 12.3 待决（附建议）

| # | 议题 | 建议 |
|---|---|---|
| Q-07-1 | 逾期视同验收 | MVP 不做（BR-07-36）；v1.1 以合同设置 + 不计入一次验收率的方式提供 |
| Q-07-2 | BLOCKED 是否允许从 DISCOVERY / PLANNING 进入 | 维持 PRD 仅 ACTIVE⇄BLOCKED；前期等待用 01 澄清状态表达 |
| Q-07-3 | 预算是否按月周期重置 | MVP 仅项目生命周期总额；按月需求出现后加 `budget_period` |
| Q-07-4 | 首次 / 一次验收率的颜色阈值 | 先用本规格提议值（80/60、80/50），试点后以数据校准 |
| Q-07-5 | 团队汇总中删除权限后的历史项目 | 按当前数据范围过滤（看不到就不计），不做历史快照授权 |
| Q-07-6 | 交付发送是否要求证据包下载链接永久有效 | 建议门户内永久可下（受访问码保护），API 签名链接 72h，可在门户重新生成 |

---

## 13. 契约变更请求

> 状态：CCR-07-01..09 已在 00 v1.1–v1.3 合并或按 00 规则处置（模块内枚举留在本规格；tag 改为路径首段；幂等与错误码按 00 §9）。下表保留原文作记录，新增 CCR-07-10。

| 编号 | 对 `00` 的变更 | 理由 |
|---|---|---|
| CCR-07-01 | §3 审计列类型改为 PostgreSQL `TIMESTAMPTZ(3)` / MySQL `DATETIME(3)` / SQLite `DATETIME` | 现文 `TIMESTAMP`/`DATETIME(6)`/`TEXT ISO8601` 与 `backend-module` 门禁冲突（全仓禁 MySQL `TIMESTAMP`，2038 上限） |
| CCR-07-02 | §9 delivery 错误码段内划分：02 使用 `120000–126999`，07 使用 `127000–129999`；通用 `120403/120404/120409/120422` 由 02 定义、07 复用 | 两份规格共用一个模块段，需避免撞码 |
| CCR-07-03 | §5 新增枚举 `AcceptanceSource`：`PORTAL`, `EMAIL`, `API`, `WEBHOOK`, `RECORDED`（所有者 07） | 回执来源需区分甲方自助与团队线下登记，`IntakeChannelKind` 无 RECORDED |
| CCR-07-04 | §5 登记本规格局部枚举（所有者 07）：`BudgetState`(`NORMAL`,`NOTIFIED`,`EXHAUSTED`)、`ProjectHoldKind`(`BUDGET`,`MANUAL`,`ESCALATION`,`AUTONOMY_PRECONDITION`)、`BlockedKind`(`CLIENT_CLARIFICATION`,`DEPENDENCY`,`EXTERNAL`,`OTHER`)、`BundleState`(`NONE`,`BUILDING`,`BUILT`,`FAILED`,`STALE`)、`EvidenceIncludeMode`(`SUMMARY`,`FILE`,`FULL_LOG`)、`ProjectHealth`(`GREEN`,`AMBER`,`RED`,`NONE`)、`ProjectContactRole`(`PRIMARY`,`CC`)、`ModeChangeTrigger`(`MANUAL`,`TAKEOVER`) | §3 要求枚举值来自 §5；或明确允许"模块私有枚举在本模块规格定义" |
| CCR-07-05 | §7.1：新增 `project.created`、`project.stage_changed`、`project.mode_changed`、`project.hold_changed`、`delivery.ready`、`delivery.release_notes_requested`（发布者 delivery）；`budget.threshold_reached` 发布者由 governance 改为 **delivery**（governance 改为消费者）；`execution.cost_updated.data` 必须含 `cumulative_cost_micros` 与累计分解 | 预算事实（上限、已花费、阈值去重）由项目档案持有，governance 无成本数据；累计值是幂等记账前提 |
| CCR-07-06 | §6.4 不可委托 / 仅人类动作增补：`project_stage_change` 中迁往 `DELIVERED`/`CLOSED`/`CANCELLED`、预算上限变更（`project_manage` 的 budget 子操作）、交付回执线下登记 | 终止与对外承诺类动作、资金上限不应由 Agent 决定（PRD §2.3 精神） |
| CCR-07-07 | §8 新增接口：`IntakeReplySender`、`IntakeReader`（01 提供）；`ProjectReader`、`ProjectGate`、`ProjectModeSwitcher`、`ProjectHoldSetter`、`AcceptanceRecorder`、`ProjectProvisioningIssueRecorder`、`DeliveryMetricsReader`（delivery 提供）；`ChannelProvisioner`（collab 提供）；`ExecutionCostReader`、`ExecutionReader`（execution 提供）；`DelegationReader`、`EscalationRuleReader`（governance 提供） | 本规格的跨模块调用需冻结窄接口 |
| CCR-07-08 | §3"项目"行补充：`project` 为底座保留实体类型，只能经 delivery 创建与改名 / 改父；底座 core 提供在调用方事务内执行的 `EntityProvisioner` | 防止 IAM 通用实体接口绕过项目档案与审计；实现原子创建 |
| CCR-07-09 | §9 补充 OpenAPI tag 词汇：`projects`、`deliveries`（以及其他规格的业务 tag 统一登记） | 路由规范要求 tag 来自批准词汇表，当前词汇表无业务域 |
| CCR-07-10 | §7.1 登记 `project.stage_ready`、`project.members_changed`、`delivery.confirmation_requested`（已列）、`delivery.withdrawn`、`project.hold_changed`、`delivery.ready`、`delivery.release_notes_requested`（发布者 delivery 07）；§8 登记 `ProjectCreator.CreateForIntakeOn`、`RepositoryReader.Get`、`IntakeProgressReader.Progress`、`AcceptanceRecorder{ListForIntakes, PortalView, Record}`、`AcceptanceReader.Get`、`ProjectReader.HealthList/Members`；01 `IntakeReader` 增 `OpenClarifications`；08 `SystemActions` 登记 `SYSTEM:delivery.autonomous_compose` | 审计 C3/C4/C8/C11/C16/C18/C19/C42/C48/C65 修复所需 |

> **v2.0（D112 / D113，00 §15）已并入本规格**：`HUMAN_ONLY` 模式（§4.5、BR-07-56）、项目宪法（§3.19、§4.6、BR-07-57）、项目 / 需求级预算容差与例外（§3.20–3.21、§4.7、BR-07-58/59）、交付不外泄内部记录（BR-07-60）、质量指标（§3.17b、BR-07-61）、接口 #50–#57、错误码 127050–127054、AC-07-50…58。**CR-07-11**（对 00）：§8 登记 `ConstitutionReader`、`ToleranceReader`（delivery 07 提供）；`quality.escaped` 发布者为 02（HUMAN_REVIEW）、07（CLIENT_ACCEPTANCE）与 12 Part A（带 `escaped_from_stage` 的缺陷），统一 `escape_key`，学习只经 02 `delivery.escape_learning`（D114，已并入 00 §7.1）；`exception.*` 的 subject_kind 含 PROJECT / REQUIREMENT（07）与 WORK_ITEM（02）。

> **D117（2026-10-12，甲方变更申请）已并入本规格**：`ToleranceReader.WouldBreach`、`ToleranceAdjuster.ApplyApprovedChange`（§6.3）、BR-07-62、错误码 `127055`、AC-07-59…61。**CR-07-12**（对 00，已合入 00 v2.0 D117 补充）：§8 登记 `ToleranceReader`（ForSubject / WouldBreach）与 `ToleranceAdjuster`（delivery 07 提供）。
