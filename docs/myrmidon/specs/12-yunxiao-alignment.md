# 12 · 云效对齐与集成（D112）

> 规格层级：`PRD.md` → `00-conventions.md`（v2.0，§15 可靠性闭环与内部质量门为权威）→ 本规格。
> 两部分：**A. 云效对齐**（模块 `delivery` 内的工作流配置与协作对象，MVP / P1）；**B. 云效集成**（新模块 `integration`，v1.1 / v2）。
> 依据：`research/2026-10-collaboration-research.md` §1.4、§3 第 11–15、26 项；云效调研结果（官方帮助中心抓取 5 页，其余检索）。**标注「待核实」的事实只来自搜索摘要，落地前必须在客户真实的云效组织中核对（导出一份工作流配置与 OpenAPI 调用实测）。**
> 设计目标：让习惯云效的团队「打开就会用」——工作项类型、缺陷模型、状态流转与评审单的心智模型照搬；在此之上加入 Myrmidon 的验收标准、证据条件、内部质量门与 Agent 协作。

---

## 1. 范围与目标

### 1.1 本规格负责

| 部分 | 内容 | 发布 |
|---|---|---|
| A1 | 工作项类型与字段对齐：需求（= 本系统 Requirement，**必须带验收标准**）、任务 `TASK`、缺陷 `BUG`、风险 `RISK`、线上故障 `INCIDENT`；缺陷详情字段与生命周期 | MVP |
| A2 | 迭代、版本（可锁定）、里程碑 | MVP |
| A3 | 状态组 `StateGroup` + 组内自定义状态（映射到 `WorkItemState`）；按工作项类型的**流转矩阵**与四类流转条件（必填字段、操作人范围、关系对象、**证据条件**） | MVP |
| A4 | 自动化规则：触发 → 条件 → 动作，执行日志 | P1 |
| A5 | 需求评审单：评审类型、评审人、最少人数、通过 / 拒绝 / 建议、以最后一次为准；拒绝 → 逐条验收标准意见 → 返工项 | MVP |
| A6 | 用例即验收标准：用例挂验收标准、测试计划自动同步、执行结果回写验收标准、测试报告导出 PDF | P1 |
| A7 | 工时：人记工时（按类别）、Agent 自动记运行时长与成本、审批、汇总到报告与 KR | P1 |
| A8 | `#ID` 关联：commit / MR / 分支中的 `#WI-123` / `#R-12` 自动关联工作项 | MVP（本地 git）/ v1.1（Codeup） |
| B1 | 云效连接与项目映射、一次性导入（工作项、工作流映射报告、Testhub 用例 csv） | v1.1 |
| B2 | Codeup Webhook（push / MR / note）→ 证据记录 | v1.1 |
| B3 | Flow 结果（自定义步骤回调或轮询）→ 验证证据；人工卡点镜像为只读裁决 | v1.1 |
| B4 | 过渡期双向同步，每个字段单一主控方 | v1.1 |
| B5 | Agent 经云效 MCP Server 读写云效（PAT） | v2 |

### 1.2 不负责（边界）

| 主题 | 归属 |
|---|---|
| 工作项状态机本身、分派、声明、裁决、返工、就绪闸门、内部质量门路由 | 02（本规格的状态定义只是 02 状态之上的**展示与流转约束层**，不新增业务状态） |
| 证据与验证器执行、冻结验收测试、平台重跑 | 03（本规格只把外部来源转成 03 的证据 / 验证结果） |
| 领域模板（软件模板的默认配置由本规格提供数据，模板机制归 11） | 11 |
| 报告与 KR 汇总的计算 | 10（本规格提供工时汇总读取接口） |
| 权限判定、收件箱、通知 | 08 |
| 密钥封装、任务队列、outbox、SSE | 09 |

### 1.3 设计原则

1. **照搬心智，不照搬缺陷**：沿用云效的命名、字段、流转矩阵与评审单；规避调研中的痛点（模板变更不传播、Webhook 触发种类少、不能跨项目搜索、工时无审批、制品可覆盖）。
2. **自定义不破坏语义**：自定义状态必须归属一个 `StateGroup` 并映射到一个 `WorkItemState`；报表、目标、报告按组与业务状态计算，不按显示名。
3. **证据条件优先于人工勾选**：「已验收」类流转默认要求每条验收标准有 PASS 证据（00 §15.4），不能只靠人点按钮。
4. **外部 CI 证据可用但需声明信任**：Flow / Codeup 的结果只有在验证计划中声明为可信验证器、且来源签名有效时，才计入 `AUTOMATED` 阶段；否则只作参考证据（00 §15.4「平台重跑」）。
5. **单一主控**：双向同步期间每个字段只有一个主控方，冲突以主控方为准并记录，绝不来回覆盖。

---

## 2. 用户故事

| ID | 故事 | 发布 |
|---|---|---|
| US-12-01 | 作为从云效迁移的交付负责人，我要看到熟悉的「需求 / 任务 / 缺陷 / 风险 / 线上故障」与迭代、版本，以便团队不需要重新学习 | MVP |
| US-12-02 | 作为测试人员，我要在缺陷上填写严重程度、优先级、复现率、发现 / 修复版本、引入阶段与原因，以便统计缺陷趋势 | MVP |
| US-12-03 | 作为项目管理员，我要按工作项类型配置状态名称与流转矩阵，并为某个流转设置必填字段与操作人范围，以便流程符合团队习惯 | MVP |
| US-12-04 | 作为交付负责人，我要在「→ 已验收」流转上配置证据条件，以便没有证据的工作项无法被标记验收 | MVP |
| US-12-05 | 作为项目管理员，我要配置「子任务全部完成后父需求自动进入验证中」这类自动化规则，并看到执行日志 | P1 |
| US-12-06 | 作为产品负责人，我要发起需求评审，邀请评审人逐条验收标准给出通过 / 拒绝 / 建议，拒绝时自动生成返工项 | MVP |
| US-12-07 | 作为测试人员，我要把用例挂在验收标准上，测试计划自动同步本迭代需求的用例，执行结果回写验收标准状态 | P1 |
| US-12-08 | 作为甲方对接人，我要导出测试报告 PDF 作为验收材料随交付包发给甲方 | P1 |
| US-12-09 | 作为工程师，我要按类别登记工时；作为负责人，我要审批工时；Agent 的运行时长与成本自动记入 | P1 |
| US-12-10 | 作为开发者，我要在 commit 或 MR 标题里写 `#WI-123` 就自动关联到工作项 | MVP |
| US-12-11 | 作为团队所有者，我要连接云效组织并把一个项目一次性导入，先看映射报告（dry run）再正式导入 | v1.1 |
| US-12-12 | 作为交付负责人，我要让 Codeup 的 MR 与 Flow 的红线结果自动成为工作项的证据 | v1.1 |
| US-12-13 | 作为过渡期的项目管理员，我要为每个字段指定主控方（Myrmidon 或云效），双向同步不互相覆盖 | v1.1 |
| US-12-14 | 作为主管 Agent，我要经云效 MCP 读写云效工作项，以便在客户仍用云效时协同 | v2 |
| US-12-15 | 作为评审人或测试人员，我要把产物上的一条批注一键提为缺陷，缺陷自动带上位置、原文快照与所针对的产物版本，以便修复者能回到原处（D120） | MVP |

---

## 3. 领域模型与数据表

### 3.1 通用约定

- 逻辑类型与三方言映射、通用列、外键策略**沿用 02 §3.1**（`ID`、`TS`、`BOOL`、`INT`、`BIGINT`、`STR(n)`、`ENUM`、`LTEXT`、`JSON`、`HASH`）。
- Part A 表属模块 `delivery`（前缀 `delivery_`，迁移目录 `internal/delivery/sql/...`，版本表 `goose_delivery`）；Part B 表属新模块 `integration`（前缀 `integration_`，迁移目录 `internal/integration/sql/...`，版本表 `goose_integration`）。
- 可变配置表均为聚合根（含 `updated_at`、`updated_by`、`version`），响应 `ETag`，写请求 `If-Match`。

### 3.2 模块内枚举（不得出现在其他模块的表或事件载荷）

| 枚举 | 值 |
|---|---|
| `DefectSeverity` | `BLOCKER`（致命）, `CRITICAL`（严重）, `MAJOR`（一般）, `MINOR`（轻微） |
| `DefectPriority` | `URGENT`, `HIGH`, `MEDIUM`, `LOW` |
| `Reproducibility` | `ALWAYS`（必现）, `HIGH`, `MEDIUM`, `LOW`, `UNREPRODUCIBLE` |
| `DefectStage` | `REQUIREMENT`, `DESIGN`, `DEVELOPMENT`, `TEST`, `ACCEPTANCE`, `PRODUCTION`（用于发现阶段 / 引入阶段） |
| `DefectResolution` | `FIXED`, `WONT_FIX`（暂不修复）, `INVALID`（无效）, `DUPLICATE`, `BY_DESIGN` |
| `IterationState` | `PLANNED`, `ACTIVE`, `CLOSED` |
| `VersionState` | `PLANNING`, `LOCKED`, `RELEASED`, `ARCHIVED` |
| `TransitionConditionKind` | `REQUIRED_FIELDS`, `ACTOR_SCOPE`, `RELATED_OBJECTS`, `EVIDENCE` |
| `AutomationOutcome` | `SUCCEEDED`, `FAILED`, `SKIPPED` |
| `ReviewSheetState` | `OPEN`, `PASSED`, `REJECTED`, `CANCELLED` |
| `ReviewVerdict` | `PASS`, `REJECT`, `SUGGEST` |
| `TestCasePriority` | `P0`, `P1`, `P2`, `P3`, `P4`, `P5` |
| `TestCaseType` | `FUNCTIONAL`, `UI`, `API`, `PERFORMANCE`, `SECURITY`, `COMPATIBILITY` |
| `TestRunResult` | `UNEXECUTED`, `PENDING_REVIEW`, `PASSED`, `FAILED`, `BLOCKED` |
| `TestPlanState` | `DRAFT`, `RUNNING`, `DONE` |
| `WorklogCategory` | `DEVELOPMENT`, `TEST`, `REVIEW`, `DESIGN`, `DOC`, `MEETING`, `OPS`, `OTHER`, `AGENT_RUNTIME` |
| `WorklogState` | `DRAFT`, `SUBMITTED`, `APPROVED`, `REJECTED` |
| `CodeLinkKind` | `COMMIT`, `MERGE_REQUEST`, `BRANCH` |
| `IntegrationProvider` | `YUNXIAO` |
| `ConnectionState` | `PENDING_CHECK`, `ACTIVE`, `ERROR`, `DISABLED` |
| `SyncMode` | `IMPORT_ONLY`, `READ_MIRROR`, `BIDIRECTIONAL` |
| `FieldMaster` | `MYRMIDON`, `YUNXIAO` |
| `ImportJobState` | `PLANNED`, `DRY_RUN_RUNNING`, `DRY_RUN_DONE`, `RUNNING`, `SUCCEEDED`, `PARTIAL`, `FAILED`, `CANCELLED` |
| `InboundSource` | `CODEUP`, `FLOW`, `PROJEX` |
| `InboundState` | `RECEIVED`, `PROCESSED`, `IGNORED`, `FAILED` |

### 3.3 `delivery_iterations`（迭代，可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| name | STR(80) | N | | UNIQUE(tenant_id, project_id, name) |
| goal | STR(500) | N | `''` | 迭代目标（可链接 10 的项目目标） |
| goal_id | ID | Y | | 10 目标 id（跨模块，不建 FK） |
| starts_at / ends_at | TS | N | | `ends_at > starts_at`，跨度 1–90 天 |
| state | ENUM `IterationState` | N | `PLANNED` | |
| closed_at | TS | Y | | |

索引：`(tenant_id, project_id, state, starts_at)`。同项目 ACTIVE 迭代可多于一个（云效允许并行迭代），但工作项同一时刻只属于一个迭代。

### 3.4 `delivery_versions`（版本，可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| name | STR(80) | N | | 如 `v1.4.0`；UNIQUE(tenant_id, project_id, name) |
| planned_release_at | TS | Y | | |
| released_at | TS | Y | | |
| state | ENUM `VersionState` | N | `PLANNING` | |
| locked_at / locked_by | TS / ID | Y | | LOCKED 后不能移入 / 移出需求与工作项（BR-12-05） |
| delivery_id | ID | Y | | 07 交付包（版本发布即交付时可关联） |

### 3.5 `delivery_milestones`（里程碑，可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| name | STR(120) | N | | |
| due_at | TS | N | | |
| achieved_at | TS | Y | | 系统在关联工作项全部 DONE 组时提示，人确认后写入 |
| goal_id | ID | Y | | 10 目标 |

### 3.6 工作项扩展列（加到 02 `delivery_work_items`，由本规格迁移新增）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| iteration_id | ID | Y | | FK → 3.3，同项目 |
| version_id | ID | Y | | FK → 3.4；版本 LOCKED 时不可变更（BR-12-05） |
| milestone_id | ID | Y | | FK → 3.5 |
| custom_state_id | ID | Y | | FK → 3.8；为空时按 `state` 取该类型的默认显示状态 |
| external_key | STR(64) | Y | | 导入来源键，如 `YX:PROJ-1234`（展示用，权威映射在 3.21） |

> 02 `WorkItemType` 已由 00 v2.0 扩展为含 `TASK`、`RISK`、`INCIDENT`、`CONTENT`；`BUG` 即云效「缺陷」。需求不是工作项类型，它是 02 的 `delivery_requirements`（必带验收标准）。

### 3.7 `delivery_defect_details`（缺陷详情，`type ∈ {BUG, INCIDENT}` 时必有一行，可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| work_item_id | ID | N | | PK 兼 FK → 02 工作项（本表 `id` = work_item_id） |
| severity | ENUM `DefectSeverity` | N | `MAJOR` | |
| priority | ENUM `DefectPriority` | N | `MEDIUM` | |
| reproducibility | ENUM `Reproducibility` | N | `ALWAYS` | |
| found_version_id | ID | Y | | FK → 3.4 |
| fixed_version_id | ID | Y | | FK → 3.4；进入「已部署」组的流转默认要求填写（默认流转矩阵配置） |
| found_stage | ENUM `DefectStage` | N | `TEST` | 发现阶段 |
| introduced_stage | ENUM `DefectStage` | Y | | 引入阶段（关闭前必填，默认配置） |
| introduced_reason | STR(32) | Y | | 引入原因，团队字典（默认：需求不清 / 设计缺陷 / 编码错误 / 回归 / 环境 / 第三方） |
| module | STR(80) | Y | | 所属模块（团队字典） |
| environment | STR(200) | N | `''` | |
| steps | LTEXT | N | | 重现步骤（必填） |
| expected | LTEXT | N | | 期望结果（必填） |
| actual | LTEXT | N | | 实际结果（必填） |
| root_cause | LTEXT | Y | | 原因定位 |
| fix_suggestion | LTEXT | Y | | 修复建议 |
| resolution | ENUM `DefectResolution` | Y | | 关闭时必填 |
| source_test_run_id | ID | Y | | 由测试执行提交时关联（3.16） |
| escaped_from_stage | ENUM `VerificationStage` | Y | | 若该缺陷是人 / 甲方发现的「穿透」（00 §15.2），记录被哪一级内部质量门放过；非空时同步发布 `quality.escaped`（BR-12-14） |
| reopened_from_work_item_id | ID | Y | | FK → 02 工作项；「再次打开」新建的缺陷指向原缺陷（§4.2.1 F13 / F14） |
| duplicate_of_work_item_id | ID | Y | | FK → 02 工作项；`resolution=DUPLICATE` 时必填，且不得指向自身 |
| comment_thread_id | ID | Y | | D120：来源批注线程（03 §3.29），由 A31 写入，不可改（BR-12-37） |
| anchor_snapshot | JSON | Y | | D120：转换时 03 返回的锚点快照 `{anchor_kind, anchor, version_kind, version_ref, artifact_ref, anchor_excerpt}`，不可变 |

### 3.8 `delivery_state_definitions`（状态定义：组内自定义显示状态，可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | Y | | NULL = 团队级默认；非空 = 项目覆盖（BR-12-08） |
| work_item_type | ENUM `WorkItemType` | N | | |
| key | STR(40) | N | | `[a-z][a-z0-9_]{1,39}`；UNIQUE(tenant_id, project_id, work_item_type, key) |
| display_name | STR(40) | N | | 中文显示名，如「已合入」「待确认」 |
| state_group | ENUM `StateGroup` | N | | 00 §5 |
| maps_to | ENUM `WorkItemState` | N | | 业务状态；必须与组相容（§4.1 表） |
| defect_stage | ENUM（02 `defect_stage`） | Y | | 仅 `BUG` / `INCIDENT`：同一 maps_to 下按 02 `defect_stage` 区分显示状态（§4.2.1） |
| sort_order | SMALLINT | N | | |
| color | STR(16) | N | `default` | 语义色名，非 hex（主题 Token） |
| is_default | BOOL | N | false | 每个 (类型, maps_to) 恰有一个默认（部分唯一，MySQL 用生成列 `default_key`） |
| archived_at | TS | Y | | 归档后不可作为新流转目标；已在此状态的工作项保留 |

### 3.9 `delivery_transition_rules`（流转矩阵单元格，可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | Y | | NULL = 团队默认 |
| work_item_type | ENUM `WorkItemType` | N | | |
| from_state_id | ID | N | | FK → 3.8 |
| to_state_id | ID | N | | FK → 3.8；UNIQUE(tenant_id, project_id, work_item_type, from_state_id, to_state_id) |
| enabled | BOOL | N | true | 矩阵勾选 |
| conditions | JSON | N | `[]` | `TransitionCondition[]`，见 §3.10 Schema |
| manual_allowed | BOOL | N | true | false = 只能由自动化规则或系统驱动 |

### 3.10 流转条件 JSON Schema（`conditions` 列，schema_version=1）

```json
{
  "type": "array", "maxItems": 12,
  "items": { "oneOf": [
    { "properties": { "kind": { "const": "REQUIRED_FIELDS" },
        "fields": { "type": "array", "items": { "enum": ["iteration_id","version_id","fixed_version_id","assignee","estimate_minutes","introduced_stage","introduced_reason","resolution","root_cause","module","worklog_minutes","acceptance_criteria"] }, "minItems": 1 } },
      "required": ["kind","fields"] },
    { "properties": { "kind": { "const": "ACTOR_SCOPE" },
        "principal_ids": { "type": "array", "maxItems": 50 },
        "role_keys": { "type": "array", "maxItems": 20 },
        "work_item_roles": { "type": "array", "items": { "enum": ["ACCOUNTABLE","EXECUTOR","REVIEWER","REPORTER"] } },
        "actor_kinds": { "type": "array", "items": { "enum": ["HUMAN","AGENT","SYSTEM"] } } },
      "required": ["kind"] },
    { "properties": { "kind": { "const": "RELATED_OBJECTS" },
        "rule": { "enum": ["ALL_CHILDREN_IN_GROUP","NO_OPEN_BLOCKING_DEFECTS","HAS_PARENT","ALL_DEPENDENCIES_DONE","LINKED_REVIEW_PASSED"] },
        "group": { "enum": ["TODO","IN_PROGRESS","VERIFYING","DONE","CANCELLED"] },
        "severity_at_least": { "enum": ["BLOCKER","CRITICAL","MAJOR","MINOR"] } },
      "required": ["kind","rule"] },
    { "properties": { "kind": { "const": "EVIDENCE" },
        "rule": { "enum": ["EVERY_AC_HAS_PASS_EVIDENCE","INTERNAL_QA_PASSED","LINKED_MR_MERGED","TEST_PLAN_PASSED","EXTERNAL_CI_PASSED"] },
        "pipeline_ids": { "type": "array", "maxItems": 10 } },
      "required": ["kind","rule"] }
  ] }
}
```

条件语义：同一单元格内**全部条件为真**才允许流转（AND）。`EVIDENCE` 条件的取值由 03 `EvidenceReader` / 02 覆盖映射计算，不接受人工勾选替代；只有持 `workflow_manage` 的人可以在**配置层**移除证据条件，单次流转不能豁免（BR-12-10）。

### 3.11 `delivery_automation_rules`（自动化规则，可变，P1）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | Y | | NULL = 团队级 |
| name | STR(80) | N | | |
| trigger | JSON | N | | `{event_type, filters}`；`event_type` ∈ 白名单：`work_item.state_changed`、`work_item.created`、`work_item.assigned`、`claim.submitted`、`verification.completed`、`internal_qa.passed`、`internal_qa.failed`、`review_sheet.closed`、`test_run.recorded`、`code_link.created`、`schedule`（cron，经 09 JobQueue） |
| condition | JSON | Y | | JSON Logic，变量仅来自事件载荷与目标工作项公开字段（同 02 JSON Logic 作用域约束） |
| actions | JSON | N | | `Action[]`（≤5）：`TRANSITION{to_state_key}`、`SET_FIELD{field, value}`、`ASSIGN{principal_id}`、`DISPATCH_TO_AGENT{role}`（经 05 派单提案，MANUAL 策略下只出提案）、`REQUEST_VERIFICATION{stage}`、`CREATE_LINKED_ITEM{type, title_template}`、`ADD_REPORT_ENTRY{section}`（经 10 接口写入报告草稿）、`NOTIFY{recipients_rule}` |
| enabled | BOOL | N | true | |
| run_as | STR(64) | N | | `SYSTEM:automation:<rule_id>`，启动时经 08 `SystemActions.Register` 登记 |
| max_runs_per_hour | INT | N | 60 | 防风暴；超限后本小时跳过并记 SKIPPED |

### 3.12 `delivery_automation_runs`（执行日志，不可变）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| rule_id | ID | N | | |
| rule_version | INT | N | | |
| trigger_event_id | ID | Y | | 09 事件 id；schedule 触发为空 |
| work_item_id | ID | Y | | |
| outcome | ENUM `AutomationOutcome` | N | | |
| actions_result | JSON | N | | 逐动作 `{action, ok, error_code?, detail?}` |
| error | STR(500) | Y | | 失败原因（如「条件不满足 / 流转条件未通过：缺少 fixed_version_id」） |
| duration_ms | BIGINT | N | | |

索引：`(tenant_id, rule_id, created_at DESC)`、`(tenant_id, work_item_id, created_at DESC)`；保留 180 天（基础设施例外按 00 §3 不适用，此表为业务日志，按项目归档后保留，不物理删除）。

### 3.13 `delivery_review_types`（评审类型，可变，团队级）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| name | STR(60) | N | | 如「需求评审」「设计评审」 |
| applies_to | JSON | N | | `{requirement: bool, work_item_types: WorkItemType[]}` |
| review_nodes | JSON | N | `[]` | 出现「发起评审」入口的状态 key 列表 |
| default_reviewer_ids | JSON | N | `[]` | principal id（人；Agent 只能作为「建议」评审人，见 BR-12-12） |
| min_reviewers | SMALLINT | N | 1 | 1–10 |
| content_fields | JSON | N | `[]` | 评审时要求查看 / 填写的字段 |
| per_ac_required | BOOL | N | true | 拒绝时必须逐条验收标准给意见 |
| propagate | BOOL | N | true | 修改对已有项目生效（规避云效「新增评审类型只对新项目生效」痛点，BR-12-08） |

### 3.14 `delivery_review_sheets` 与 `delivery_review_opinions`

`delivery_review_sheets`（可变）：`project_id`、`target_kind`（`REQUIREMENT` / `WORK_ITEM`）、`target_id`、`target_version_no`（需求版本快照）、`review_type_id`、`initiator_id`、`reviewer_ids JSON`、`min_reviewers`（发起时快照）、`state ENUM ReviewSheetState`、`result_decision_id`（关闭时 02 裁决 id）、`closed_at`、`due_at`（默认 +3 个工作日）。

`delivery_review_opinions`（不可变，每次提交一行）：`sheet_id`、`reviewer_principal_id`、`actor_kind`、`verdict ENUM ReviewVerdict`、`per_ac JSON [{criterion_id, verdict, comment}]`、`comment LTEXT`、`supersedes_id`（同一评审人上一条，「以最后一次为准」）。

### 3.15 `delivery_test_cases`（用例，可变，P1）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| criterion_id | ID | N | | FK → 02 `delivery_acceptance_criteria`；**每条用例必须挂一条验收标准** |
| requirement_id | ID | N | | 冗余，便于计划同步 |
| title | STR(200) | N | | |
| preconditions | LTEXT | N | `''` | |
| steps | JSON | N | | `[{action, expected}]` 1–50 步 |
| priority | ENUM `TestCasePriority` | N | `P2` | |
| case_type | ENUM `TestCaseType` | N | `FUNCTIONAL` | |
| tags | JSON | N | `[]` | |
| automation_ref | STR(300) | Y | | 自动化测试标识（测试文件 + 名称）；非空时执行结果可由 03 验证结果自动回填 |
| is_hidden_acceptance | BOOL | N | false | 属于冻结的隐藏验收集（00 §15.4）：对执行者与其 Agent 不可见（BR-12-17） |
| state | STR(16) | N | `ACTIVE` | `ACTIVE` / `ARCHIVED` |

### 3.16 测试计划、执行与报告（P1）

- `delivery_test_plans`（可变）：`project_id`、`name`、`iteration_id?`、`version_id?`、`sync_mode`（`AUTO` = 自动同步该迭代 / 版本内需求关联的 ACTIVE 用例；`MANUAL`）、`state ENUM TestPlanState`、`owner_id`、`starts_at`、`ends_at`。
- `delivery_test_runs`（可变，每个计划 × 用例一行）：`plan_id`、`case_id`、`executor_principal_id`、`actor_kind`、`result ENUM TestRunResult`、`executed_at`、`evidence_id`（03 证据，截图 / 日志）、`defect_ids JSON`、`note`；UNIQUE(plan_id, case_id)。结果回写：同一验收标准下所有 ACTIVE 用例最新结果全部 PASSED → 该标准的「用例覆盖」为 PASS（作为 03 的 `HUMAN` 或 `TEST` 层证据，按执行者类型）。
- `delivery_test_run_history`（不可变）：每次执行写一行 `{run_id, result, executor_principal_id, actor_kind, evidence_id, note, executed_at}`，用于追溯覆盖。
- `delivery_test_reports`（不可变）：`plan_ids JSON`（1–15）、`summary JSON {total, passed, failed, blocked, pass_rate, defects_by_severity, fix_ratio}`、`file_node_id`（PDF，经底座 files 与 09 C9 写入）、`content_hash HASH`、`generated_by`。报告可作为 07 交付包的证据项（BR-12-20）。

### 3.17 `delivery_worklogs`（工时，可变，P1）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| work_item_id | ID | Y | | 可为空（项目级杂项工时） |
| principal_id | ID | N | | |
| actor_kind | STR(8) | N | | `HUMAN` / `AGENT` |
| category | ENUM `WorklogCategory` | N | | Agent 自动记录固定为 `AGENT_RUNTIME` |
| work_date | STR(10) | N | | `YYYY-MM-DD`，团队时区（10 settings） |
| minutes | INT | N | | 1–1440；同一人同一天合计 ≤ 1440 |
| cost_micros | BIGINT | N | 0 | Agent 记录取自 03 执行成本；人类为 0 |
| execution_id | ID | Y | | Agent 自动记录来源；UNIQUE(tenant_id, execution_id)（幂等） |
| note | STR(500) | N | `''` | |
| state | ENUM `WorklogState` | N | `DRAFT` | Agent 自动记录直接 `APPROVED`（系统事实，BR-12-22） |
| approved_by / approved_at | ID / TS | Y | | |

### 3.18 `delivery_code_links`（代码关联，不可变 + 状态列）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| work_item_id | ID | N | | |
| kind | ENUM `CodeLinkKind` | N | | |
| repository | STR(300) | N | | 仓库标识（本地 workdir key 或 Codeup 仓库全名） |
| ref | STR(200) | N | | commit sha / MR iid / 分支名 |
| title | STR(300) | N | `''` | |
| url | STR(500) | Y | | |
| source | STR(16) | N | | `LOCAL_GIT` / `CODEUP` |
| mr_state | STR(16) | Y | | `OPENED` / `MERGED` / `CLOSED`（MR） |
| evidence_id | ID | Y | | 生成的 03 证据（BR-12-24） |

UNIQUE(tenant_id, work_item_id, kind, repository, ref)。

### 3.19 `integration_connections`（云效连接，可变，v1.1）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| provider | ENUM `IntegrationProvider` | N | `YUNXIAO` | |
| name | STR(80) | N | | |
| organization_id | STR(64) | N | | 云效组织 ID |
| api_endpoint | STR(200) | N | | 新版 OpenAPI 域名（**待核实**：调研称为 `openapi-rdc.aliyuncs.com`，路径前缀 `/oapi/v1/...`） |
| token_sealed | LTEXT | N | | 个人访问令牌，经 09 `SecretSealer` 封装（请求头 `x-yunxiao-token`，**待核实**）；永不出现在响应与日志 |
| webhook_secret_sealed | LTEXT | N | | 入站 Webhook 校验密钥（Codeup 头 `X-Codeup-Token`，**待核实**） |
| state | ENUM `ConnectionState` | N | `PENDING_CHECK` | |
| last_checked_at | TS | Y | | |
| last_error | STR(500) | Y | | |
| token_scopes | JSON | N | `[]` | 连接检测时实际可用的能力（项目读 / 写、工作项读 / 写、代码库读、流水线读） |

### 3.20 `integration_project_mappings`（项目映射，可变）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| connection_id | ID | N | | FK |
| external_project_id | STR(64) | N | | UNIQUE(connection_id, external_project_id) |
| external_project_name | STR(200) | N | | |
| project_id | ID | N | | 本系统项目（Entity）；UNIQUE(tenant_id, project_id)：一个项目只映射一个云效项目 |
| sync_mode | ENUM `SyncMode` | N | `IMPORT_ONLY` | |
| field_masters | JSON | N | `{}` | `{field: FieldMaster}`，未列出的字段默认 `MYRMIDON`；`state`、`assignee`、`title`、`description`、`iteration`、`version`、`severity`、`priority`、`worklog` 可配置 |
| codeup_repositories | JSON | N | `[]` | 关联的 Codeup 仓库全名 |
| trusted_pipelines | JSON | N | `[]` | 可信 Flow 流水线 id（只有这些的红线结果计入 `AUTOMATED` 阶段，BR-12-27） |
| paused | BOOL | N | false | 暂停同步 |

### 3.21 `integration_external_refs`（外部对象映射，可变）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| mapping_id | ID | N | | |
| external_kind | STR(32) | N | | `WORKITEM` / `SPRINT` / `VERSION` / `TEST_CASE` / `MR` / `PIPELINE_RUN` |
| external_id | STR(64) | N | | UNIQUE(mapping_id, external_kind, external_id) |
| local_kind / local_id | STR(32) / ID | N | | |
| external_updated_at | TS | Y | | 最近一次看到的外部修改时间（冲突判定） |
| local_version | INT | Y | | 最近一次同步时的本地版本 |
| last_synced_at | TS | Y | | |

### 3.22 `integration_state_mappings`（状态映射，可变）

`mapping_id`、`external_type`（云效工作项类型名，如「需求」「任务」「缺陷」）、`external_status_id`、`external_status_name`、`local_work_item_type ENUM WorkItemType`（「需求」映射到 Requirement 时为空，并设 `maps_to_requirement=true`）、`local_state_id`（FK → 3.8）、`confidence SMALLINT`（自动建议的置信度 0–100）、`confirmed_by`、`confirmed_at`。导入前所有外部状态必须有确认过的映射（BR-12-25）。

### 3.23 `integration_import_jobs`（导入任务，可变）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| mapping_id | ID | N | | |
| state | ENUM `ImportJobState` | N | `PLANNED` | |
| scope | JSON | N | | `{work_items: bool, sprints: bool, versions: bool, test_cases_csv_file_id?, since?}` |
| dry_run_report_file_id | ID | Y | | 映射报告（csv + 摘要 JSON）：每类数量、未映射状态、无验收标准的需求、超长字段、无法解析的人员 |
| counts | JSON | N | `{}` | `{created, updated, skipped, failed}` 按对象类型 |
| failures_file_id | ID | Y | | 失败明细 |
| started_at / finished_at | TS | Y | | |
| job_id | ID | Y | | 09 JobQueue 任务 id |

### 3.24 `integration_inbound_events` 与 `integration_sync_log`

- `integration_inbound_events`（不可变 + 处理状态）：`connection_id`、`source ENUM InboundSource`、`event_kind`（如 `push`、`merge_request`、`note`、`pipeline_run`）、`delivery_key`（来源提供的投递 id；无则为载荷 SHA-256）、`signature_ok BOOL`、`payload_hash HASH`、`payload_file_id`（原始载荷存文件，便于审计与重放）、`state ENUM InboundState`、`error`、`processed_at`。UNIQUE(connection_id, source, delivery_key)（入站去重；这不是领域事件消费去重，09 receipts 不适用于外部入站）。
- `integration_sync_log`（不可变）：`mapping_id`、`direction`（`IN` / `OUT`）、`external_kind`、`external_id`、`local_id`、`field?`、`outcome`（`APPLIED` / `SKIPPED_NOT_MASTER` / `CONFLICT_MASTER_WINS` / `FAILED`）、`detail`。保留 180 天后归档到文件。

---

## 4. 状态机

### 4.1 状态组与业务状态的相容关系（`delivery_state_definitions.maps_to` 守卫）

| StateGroup | 允许映射的 WorkItemState |
|---|---|
| `TODO` | `DRAFT`, `READY`, `BLOCKED` |
| `IN_PROGRESS` | `CLAIMED`, `PLAN_REVIEW`, `IN_PROGRESS`, `REWORK`, `EXECUTION_FAILED`, `STOPPED`, `EXCEPTION` |
| `VERIFYING` | `AGENT_CLAIMED_DONE`, `VERIFYING`, `INTERNAL_QA`, `NEEDS_HUMAN_REVIEW` |
| `DONE` | `ACCEPTED` |
| `CANCELLED` | `CANCELLED` |

违反 → 422 `126001`。显示状态之间的流转最终都转换为 02 的业务迁移调用：**流转矩阵只能收紧，不能放宽** 02 状态机（矩阵允许但 02 不允许的迁移 → 02 返回 `120420`，配置保存时即预检并拒绝 422 `126002`）。系统驱动的迁移（如验证完成 → `INTERNAL_QA`）不受 `manual_allowed` 与 `ACTOR_SCOPE` 约束，但仍受 `EVIDENCE` 条件约束。

### 4.2 默认缺陷生命周期（软件模板默认数据，类型 `BUG`）

| 显示状态（key） | 组 | maps_to | 说明 |
|---|---|---|---|
| 待确认 `triage` | TODO | `DRAFT` | 新提缺陷；就绪闸门要求重现步骤 / 期望 / 实际 |
| 待处理 `ready` | TODO | `READY` | |
| 处理中 `fixing` | IN_PROGRESS | `IN_PROGRESS` | |
| 已合入 `merged` | VERIFYING | `VERIFYING`（`defect_stage=MERGED`；瞬态 AGENT_CLAIMED_DONE 同样显示为此） | 代码合入（`EVIDENCE: LINKED_MR_MERGED`）；云效的「已修复」被拆为 已合入 → 已部署 → 已验证 |
| 已部署 `deployed` | VERIFYING | `VERIFYING`（`defect_stage=DEPLOYED`） | 要求 `fixed_version_id` 与部署证据 |
| 内部验证 `internal_qa` | VERIFYING | `INTERNAL_QA` | 测试 Agent + 产品验收 Agent（00 §15.2） |
| 待确认关闭 `verify` | VERIFYING | `NEEDS_HUMAN_REVIEW` | |
| 已关闭 `closed` | DONE | `ACCEPTED` | 要求 `resolution`、`introduced_stage`；`ACTOR_SCOPE` 默认限定测试角色或负责人 |
| 暂不修复 `wont_fix` | CANCELLED | `CANCELLED` | `resolution=WONT_FIX`，理由必填 |
| 无效 `invalid` | CANCELLED | `CANCELLED` | `resolution ∈ {INVALID, DUPLICATE, BY_DESIGN}` |

「再次打开」的语义与执行者见 §4.2.1（02 中 ACCEPTED / CANCELLED 为终态，只有 T26 例外）。需求、任务、风险、线上故障的默认状态与矩阵见 11 软件模板的数据文件（本规格 §7.3 的种子任务负责写入）。

#### 4.2.1 默认缺陷流转矩阵与权责（02 BR-02-59 委托）

**两层推进。** 缺陷的推进分两层：① 02 业务状态，只经 02 的 T 迁移改变；② 02 工作项字段 `defect_stage`（`MERGED → DEPLOYED → VERIFIED → CLOSED`），在 02 状态不变的前提下由证据推进。`merged` / `deployed` 两个显示状态同属 VERIFYING 组，由 `WorkflowResolver` 按 `defect_stage` 区分（§3.8 `defect_stage` 判别列）：02 状态 ∈ {AGENT_CLAIMED_DONE, VERIFYING} 且 `defect_stage=MERGED` 时显示 `merged`，`=DEPLOYED` 时显示 `deployed`。

**执行者记号。** 「人」= 人类成员；「Agent」= 执行 / 主管 Agent；「系统」= 平台事件驱动，不受 `manual_allowed` / `ACTOR_SCOPE` 约束，但受 `EVIDENCE` 约束（§4.1）。**验证角色** = 项目内持 `TESTER` 角色的**人类**成员，或该工作项的责任人（00 §15 accountable human）。团队可在矩阵上继续收紧（如把 F1 限定为测试角色），不能放宽本表的「仅人」格与证据条件（BR-12-33）。

| # | 显示流转 | 中文 | 谁可执行 | 条件（默认） | 02 落点 | `defect_stage` |
|---|---|---|---|---|---|---|
| F1 | triage → ready | 确认缺陷 | 人；主管 Agent（持 `work_item_write`） | REQUIRED_FIELDS：severity、priority、reproducibility、steps、expected、actual；就绪闸门通过 | T27 DRAFT → READY | — |
| F2 | triage → invalid | 判为无效 | **仅人**（`work_item_cancel`）；`resolution=DUPLICATE` 时主管 Agent 也可，须填 `duplicate_of_work_item_id` | 理由必填；`resolution ∈ {INVALID, DUPLICATE, BY_DESIGN}` | T23 → CANCELLED | — |
| F3 | triage / ready → wont_fix | 暂不修复 | **仅人**（`work_item_cancel`；产品取舍，不可委托 Agent） | 理由必填；`resolution=WONT_FIX`；建议填 `found_version_id` | T23 → CANCELLED | — |
| F4 | ready → fixing | 开始处理 | 受派执行者（人或 Agent）；派单经 T03 | 计划需评审时先经 PLAN_REVIEW（T30–T31） | T02 / T03 → T04 | — |
| F5 | fixing → merged | 已合入 | 执行者提交声明（人或 Agent） | EVIDENCE `LINKED_MR_MERGED`：关联 MR 已合入目标分支，合入提交 = 声明 `head_commit` | T08 → AGENT_CLAIMED_DONE（随后系统 T09） | `MERGED` |
| F6 | merged → deployed | 已部署 | 系统（可信流水线部署事件 / 07 交付证据）；执行者（人或 Agent）可手工登记部署证据，平台复核后推进 | EVIDENCE：部署记录指向含合入提交的制品；REQUIRED_FIELDS `fixed_version_id` | 02 状态不变 | `DEPLOYED` |
| F7 | deployed → internal_qa | 进入内部验证 | 系统 | 必需验证全部 PASSED / 无 FAILED（T10 / T11）；**若矩阵含 F6，`defect_stage` 须已为 `DEPLOYED`**，否则停在 VERIFYING 并显示「等待部署」（02 守卫钩子 BR-02-62） | T10 / T11 → INTERNAL_QA | — |
| F8 | internal_qa → fixing | 内部打回 | 系统 | 测试 Agent 或产品验收 Agent 任一不通过（T34）；**不通知人、不进收件箱**（BR-02-54） | T34 → REWORK | 回 `MERGED` 之前：清空为 NULL |
| F9 | internal_qa → verify | 内部验证通过 | 系统 | AGENT_TEST 与 AGENT_ACCEPTANCE 均 PASSED（T33）；轮次用尽时为 T35（带升级原因） | T33 / T35 → NEEDS_HUMAN_REVIEW | `VERIFIED`（T35 不置） |
| F10 | verify → closed | 关闭（已验证通过） | **仅验证角色（人）**；Agent 一律不可（BR-12-34） | REQUIRED_FIELDS：resolution（= FIXED）、introduced_stage；02 BR-02-19 ~ 25 | T14 APPROVE → ACCEPTED | `CLOSED` |
| F11 | verify → fixing | 人工打回 | **仅人**（`decision_request_changes`） | 反馈 ≥1、理由必填；被打回的声明曾内部通过 → 记 `escaped_from_stage`，`quality.escaped` 由 02 在 T15 发布，本规格不重复发布（BR-12-14，D114） | T15 → REWORK | 清空为 NULL |
| F12 | closed → fixing | 再次打开（甲方拒收） | **仅人**，且只能经 01 返工来件 `REOPEN_WORK_ITEMS` 或 02 D1 | 07 报告该缺陷在 `source_acceptance_id` 中被拒；同一验收单只能重开一次 | T26 → REWORK | 清空为 NULL |
| F13 | closed → （新缺陷） | 再次打开（内部 / 回归） | 人；测试 Agent（发现回归时） | 原缺陷不动；新建 `BUG`，`reopened_from_work_item_id` 指向原缺陷，继承 severity / module / steps（BR-12-07） | T01 新工作项 | 新项为 NULL |
| F14 | wont_fix / invalid → （新缺陷） | 再次打开（取消后） | 人 | 同 F13；原缺陷保持 CANCELLED | T01 新工作项 | 新项为 NULL |

矩阵中未列出的显示流转默认不勾选；勾选 02 不允许的格在保存时 422 `126002`。`STOPPED`（Andon，T36 / T37）、`EXCEPTION`（T38 / T39）、`BLOCKED`（T21 / T22）、PAUSE 覆盖层对缺陷与其他类型一致，不进入本矩阵。

### 4.3 需求评审单 `ReviewSheetState`

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| RS1 | — | 发起评审 | 目标处于该评审类型的 `review_nodes` 之一；同一目标无 OPEN 评审单；评审人 ≥ `min_reviewers` 且至少 `min_reviewers` 名人类 | OPEN | 通知评审人（08 收件箱 `REVIEW_SHEET`）；事件 `review_sheet.opened` |
| RS2 | OPEN | 提交意见 | 调用者在 `reviewer_ids`；`per_ac_required` 时 REJECT 必须给每条被拒标准的意见 | OPEN | 写入意见（覆盖同人上一条） |
| RS3 | OPEN | 发起人结束评审 | 有效（最新）人类意见数 ≥ `min_reviewers` | PASSED（全部 PASS 或 SUGGEST）/ REJECTED（任一 REJECT） | 记录 02 裁决（PASSED → `APPROVE`，REJECTED → `REQUEST_CHANGES` + 逐条反馈 → 返工项，BR-12-13）；`review_sheet.closed` |
| RS4 | OPEN | 发起人取消 | 理由必填 | CANCELLED | |
| RS5 | OPEN | 目标需求升版本 | — | CANCELLED | 系统取消（评审对象已变），提示重新发起 |

非法：对非 OPEN 的评审单提交意见或结束 → 409 `126010`；意见不足即结束 → 409 `126011`。

### 4.4 测试执行 `TestRunResult`

`UNEXECUTED → PENDING_REVIEW | PASSED | FAILED | BLOCKED`；任意结果可被再次执行覆盖（保留历史行到 `delivery_test_run_history`，不可变）。`FAILED` 时可「提交缺陷」（创建 `BUG` + 3.7 并预填步骤、关联计划 / 迭代 / 用例）。`automation_ref` 用例由 03 验证结果自动写入（执行者 `SYSTEM`），人可覆盖但须理由。

### 4.5 工时 `WorklogState`

`DRAFT →（提交）SUBMITTED →（审批）APPROVED | REJECTED →（修改）DRAFT`。APPROVED 后只能由审批人撤回为 SUBMITTED（理由必填）。Agent 自动记录创建即 APPROVED，且不可编辑（只能由人加备注）。

### 4.6 连接 `ConnectionState` 与导入 `ImportJobState`

- 连接：`PENDING_CHECK →（检测通过）ACTIVE`；`ACTIVE →（调用连续失败 5 次或 401 / 403）ERROR`；`ERROR →（重新检测通过）ACTIVE`；任意 →（停用）DISABLED。ERROR 期间同步暂停，不丢入站事件（入站继续落表，恢复后处理）。
- 导入：`PLANNED → DRY_RUN_RUNNING → DRY_RUN_DONE →（确认）RUNNING → SUCCEEDED | PARTIAL | FAILED`；`PLANNED / DRY_RUN_DONE → CANCELLED`。DRY_RUN_DONE 有未确认的状态映射时不能进入 RUNNING（409 `190010`）。同一映射同时只能有一个非终态导入任务（409 `190011`）。

---

## 5. 业务规则与边界情况

### 5.1 工作项类型与字段

- **BR-12-01** 需求不是工作项类型：云效「需求」导入为 02 Requirement；没有验收标准的需求进入 `DRAFT`（需求层面为草稿，02 RequirementStatus），在导入报告中列出，必须补齐验收标准后才能拆出可派发的工作项（就绪闸门，00 §15.3）。
- **BR-12-02** `BUG` / `INCIDENT` 创建时必须同时写 3.7 行，且 `steps`、`expected`、`actual` 非空（422 `126020`）；`INCIDENT` 默认 `priority=URGENT`、`severity ≥ CRITICAL`、服务类别「加急」。
- **BR-12-03** 严重程度与优先级相互独立；看板默认排序：优先级 → 严重程度 → 创建时间。
- **BR-12-04** 团队字典（`introduced_reason`、`module`）由 `workflow_manage` 维护，最多 100 项；停用项不可新选但保留历史值。

### 5.2 迭代、版本、里程碑

- **BR-12-05** 版本 `LOCKED` 后：不可移入 / 移出工作项与需求、不可修改 `found_version_id` / `fixed_version_id` 指向该版本的值（409 `126030`）；只有版本负责人或持 `workflow_manage` 者可解锁（理由必填，写审计）。
- **BR-12-06** 工作项同一时刻只属于一个迭代；迭代 CLOSED 时未完成（非 DONE / CANCELLED 组）的工作项按项目设置**自动结转**到下一个 PLANNED/ACTIVE 迭代或移回待办池，并在 10 的报告「未完成」中带出（原因 `CARRIED_OVER`）。
- **BR-12-07** 「再次打开」：02 中 ACCEPTED 与 CANCELLED 为终态（仅 T26 例外），因此除甲方拒收重开（§4.2.1 F12）外，已关闭 / 暂不修复 / 无效的缺陷一律以**新建 `BUG`** 方式重开（F13 / F14）：新缺陷的 `reopened_from_work_item_id` 指向原缺陷，原缺陷保持终态——审计不被改写。

### 5.3 状态定义与流转矩阵

- **BR-12-08** 配置层级：团队默认 → 项目覆盖。团队默认的修改**立即对所有未覆盖该单元格的项目生效**，保存前展示影响预览（受影响项目数、处于将被归档状态的工作项数）——规避云效「模板变更不传播」痛点。项目覆盖可随时「恢复为团队默认」。
- **BR-12-09** 归档一个显示状态时，若仍有工作项处于该状态：必须指定迁移目标（同组、同 `maps_to`）后才能归档（409 `126003`）。
- **BR-12-10** 流转条件评估顺序：`ACTOR_SCOPE` → `REQUIRED_FIELDS` → `RELATED_OBJECTS` → `EVIDENCE`。前两类失败返回可修正提示（弹窗只要求补填缺失字段，不覆盖已有值，沿用云效体验）；`EVIDENCE` 失败返回缺失项清单（哪条验收标准没有 PASS 证据 / 哪条内部验收未通过），**单次流转不能豁免**。
- **BR-12-11** `ACTOR_SCOPE.actor_kinds` 不含 `AGENT` 时，Agent 不能手动触发该流转；Agent 只能触发 `manual_allowed=true` 且 actor_kinds 含 AGENT 的流转，且永远不能触发映射到 `ACCEPTED` 的流转（00 §15.1，L3 由人或经委托的评审者通过 02 裁决产生）。

### 5.4 评审单

- **BR-12-12** Agent 可以被邀请提交 `SUGGEST` 意见（标「AI 建议」），但不计入 `min_reviewers`，也不能提交 `PASS` / `REJECT`（403 `120403`，`reason_code=AGENT_PROHIBITED`）。
- **BR-12-13** 评审 REJECTED：按逐条验收标准意见生成返工项（每条被拒标准一条，类型 `TASK`，挂原需求，反馈作为结构化反馈 `REQUIREMENT_MISMATCH` 注入）；需求本身回到「待完善」显示状态（maps_to `DRAFT`）。评审 PASSED：需求可进入拆解；若评审类型配置了「通过后冻结验收测试」，触发 03 `acceptance_suite.frozen`。
- **BR-12-14** 穿透标记：L3 评审或甲方验收（07 回执）中发现的缺陷，创建时 `escaped_from_stage` 默认 `AGENT_ACCEPTANCE`（可改为 `AGENT_TEST` / `AUTOMATED`）；保存时发布 `quality.escaped`（00 §15.2；`escape_key` 见 00 §7.1），由 02 `delivery.escape_learning` 经 06 写入经验记忆草稿并经 11 `EvalSetWriter.ProposeCase` 加入模板评测集（D114）。若该缺陷源自已发布过穿透的同一拒收（同一 `escape_key`，如 07 已就该甲方回执发布、或 02 已在 T15 发布），缺陷只记录对该穿透的引用，**不再发布**。

### 5.5 用例与测试计划

- **BR-12-15** 计划 `sync_mode=AUTO`：每次迭代 / 版本内需求的用例新增或归档时（事件驱动 + 每 10 分钟对账），自动增删 `delivery_test_runs`（已执行的用例不删除，只标「已移出范围」）。
- **BR-12-16** 验收标准的用例覆盖：同一标准下全部 ACTIVE 用例的最新结果为 PASSED → 写入 03 证据（`EvidenceType=TEST_REPORT`，来源 `TEST_PLAN:<plan_id>`），作为 `verification_plan` 中该标准对应方式为 `TEST` / `HUMAN` 时的满足证据；任一 FAILED → 该标准 FAIL，并阻止「已验收」类流转（`EVIDENCE` 条件）。
- **BR-12-17** 隐藏验收用例（`is_hidden_acceptance=true`）：只对持 `acceptance_suite_manage` 的人类可见；对执行者、执行 Agent、测试 Agent 不可见（API 过滤，404 而非 403 以免泄露存在性）；产品验收 Agent 只拿到执行结果，不拿到用例内容（00 §15.4）。
- **BR-12-18** 测试执行者为 Agent 时（测试 Agent 跑用例），结果属于 `AGENT_TEST` 阶段证据；人类执行属于人工证据。二者在报告中分开统计。
- **BR-12-19** 导入的 Testhub csv 用例若无法确定所属验收标准：挂到需求的「未归类」占位标准并标黄，不计入覆盖率，提示人工归类。
- **BR-12-20** 测试报告生成后不可修改（内容哈希）；可选择加入 07 交付包证据包，甲方门户只显示汇总（通过率、缺陷分布），不显示用例步骤。最多关联 15 个计划（沿用云效限制，便于迁移习惯）。

### 5.6 工时

- **BR-12-21** 人类工时：同一人同一天合计 ≤ 1440 分钟（422 `126040`）；工作项进入 DONE 组前，若流转配置了 `REQUIRED_FIELDS: worklog_minutes`，则该工作项该人工时 > 0。
- **BR-12-22** Agent 工时：03 执行进入终态时自动生成一条（`AGENT_RUNTIME`、`minutes = ceil(wall_clock/60s)`、`cost_micros` 取执行累计成本），幂等于 `execution_id`；不可编辑。
- **BR-12-23** 汇总：按人 / Agent、类别、工作项、迭代、项目汇总，供 10 报告与 KR 读取（`WorklogReader`）；只统计 APPROVED。

### 5.7 代码关联

- **BR-12-24** 解析规则：commit message、MR 标题 / 描述、分支名中的 `#WI-<n>`、`#R-<n>`（项目内编号）以及云效原编号（导入后的 `external_key`）；同一提交引用多个工作项则分别关联；引用不存在或跨项目 → 忽略并在同步日志记录。MR 合并时生成 03 证据（`PULL_REQUEST`）；commit 生成 `COMMIT` 证据（仅当仓库属于本项目映射或本地工作目录）。

### 5.8 集成（Part B）

- **BR-12-25** 导入前置：连接 ACTIVE；项目映射存在；所有外部状态已确认映射；dry run 已完成且报告被确认。导入幂等：以 `integration_external_refs` 唯一键判定新建或更新；重复导入同一范围只更新外部修改时间晚于上次的对象。
- **BR-12-26** 人员映射：按邮箱匹配本租户成员；匹配不到 → 指派为空并在报告列出（不自动创建账号）。
- **BR-12-27** 外部 CI 证据可信度：Flow 运行结果只有当流水线 id 在 `trusted_pipelines` 中、入站签名有效、且运行关联的 commit 等于工作项声明的 `head_commit` 时，才作为 `AUTOMATED` 阶段证据；否则记为参考证据（不推进状态）。平台重跑原则（00 §15.4）对本地可重跑的检查仍然优先：可信外部 CI 只替代平台无法在本机重跑的部分（如需要特定环境的集成测试）。
- **BR-12-28** 人工卡点镜像：Flow 人工卡点（会签 / 或签）的结果镜像为 02 的只读外部裁决记录（`authority_source=EXTERNAL:YUNXIAO_FLOW:<pipeline>`），**不能**替代本系统的 L3 裁决；它只作为证据显示。
- **BR-12-29** 双向同步：出站只同步主控方为 `MYRMIDON` 的字段；入站只应用主控方为 `YUNXIAO` 的字段；同一字段两端在同一同步周期内都有修改 → 以主控方为准，另一端被覆盖并记 `CONFLICT_MASTER_WINS`。状态同步经状态映射表双向转换；目标状态在本系统不可达（违反 02 状态机或流转条件）→ 不应用，记 `FAILED` 并在映射页提示。
- **BR-12-30** 入站 Webhook：签名不符 → 401，不落库；同一 `delivery_key` 重复 → 200 幂等忽略；处理失败 → 记 FAILED，指数退避重试 5 次后停在 FAILED 并通知 `integration_manage` 持有者。原始载荷经 09 C9 存为文件，保留 90 天。
- **BR-12-31** 速率：出站调用每连接 ≤ 10 次 / 秒（**待核实**云效限流值后调整），遇 429 按 `Retry-After` 退避。
- **BR-12-32** MCP（v2）：Agent 经云效 MCP Server 读写云效时，使用连接上配置的服务账号 PAT（不是用户个人 PAT）；Agent 的写操作仍须满足本系统的授权（08）——写入云效的状态变更视为对映射工作项的变更，经同一流转条件与主控方规则。
- **BR-12-33** 缺陷矩阵的下限：§4.2.1 中标为「仅人」的格（F2 非重复、F3、F10、F11、F12）与所有 EVIDENCE 条件不可被项目 / 团队配置放宽；保存试图放宽的矩阵 → 422 `126002`（`reason_key=defect_floor`）。
- **BR-12-34** Agent 不得执行任何 `maps_to=ACCEPTED` 的流转（含 F10）；即使 Agent 持 `TESTER` 角色。违反 → 403，`reason_code` 由 08 定义（`AGENT_CANNOT_ACCEPT`）。
- **BR-12-35** `defect_stage` 只前进不后退，唯一例外是 02 回到 REWORK（F8 / F11 / F12）时清空为 NULL；`DEPLOYED` 需部署证据指向含 `MERGED` 合入提交的制品，证据缺失或提交不符 → 409 `126006`。`VERIFIED` 只由 T33 写入；`CLOSED` 只由 T14 写入。
- **BR-12-36** 新建「再次打开」缺陷（F13 / F14）时：原缺陷须为 ACCEPTED 或 CANCELLED，否则 409 `126007`；原缺陷为 ACCEPTED 且新缺陷由人类或甲方发现时，按 BR-12-14 记录 `escaped_from_stage` 并发布 `quality.escaped`（同一 `escape_key` 已发布时只引用、不再发布）；由测试 Agent 发现的回归不计穿透。

### 5.9 D120：批注转缺陷与过期上下文写入

- **BR-12-37** 批注转缺陷：A31 `POST /comment-threads/{thread_id}/defects`（守卫 `RegisterOwned`，按线程所属项目判定 `work_item_write`）。请求 `{title?, severity*, priority?, steps?, expected?, actual?, found_in_work_item_id?}`；服务端经 03 `ArtifactComments.GetOn(ctx, tx, thread_id)` 读取线程并按锚点预填缺省值：`title` = 首条批注前 80 字；`steps` = 位置（CODE_LINES `src/app.ts:40-44`、HTML_REGION「页面 /orders · 区域 (120,340) 390×844 · PHONE」、TABLE_ROW `orders.csv 行 id=42`、TEXT_PARAGRAPH「第 3 段」）+ 版本（commit 短哈希或内容哈希前 8 位）+ `anchor_excerpt`；`actual` = 首条批注正文（≤2000 字）；`expected` 无可推断值时须由调用方提供（否则按 BR-12-02 返回 422 `126020`）；`found_in_work_item_id` 缺省 = 线程的 `work_item_id`（为空时不关联）。同一事务内：① 在线程所属项目创建 `type=BUG` 工作项与 §3.7 行（BR-12-02 字段齐全，`found_stage=TEST`，可随后经 A9 修改）；② 写 `comment_thread_id` 与 `anchor_snapshot`；③ 调 03 `ArtifactComments.MarkConvertedOn(ctx, tx, thread_id, {target_kind:DEFECT, target_id:新缺陷工作项 id, actor})`；④ 审计 `delivery.defect_created_from_comment`（detail 只含线程 ID、缺陷 ID、锚点种类与版本引用，不含正文）。只允许 INTERNAL 线程：CLIENT 线程 → 422 **126063**（`data.reason=CLIENT_THREAD`；甲方意见走 07 拒收项）；该线程已转为缺陷 → 422 **126063**（`data.reason=ALREADY_CONVERTED`、`data.target_id` = 已有缺陷，调用方可直接跳转），整体回滚；03 的 `ErrTargetNotAllowed` 同样映射为 126063。转换为 FEEDBACK（02）与 DEFECT 可并存。Agent 调用时适用 BR-12-38 同一 `seen_seq` 规则（线程有关联工作项时取其 `work_item:<id>` 流）。本操作登记到 05 `OperationRegistry`（`defect_from_comment.create`，权限 `work_item_write` · ROLE，副作用 `WRITES_STATE`），响应带 `meta.next`。
- **BR-12-38** 流转的过期上下文写入（05 BR-05-127 / 128 / 131，00 §9）：A13 调用方为 AGENT 时请求须带 `seen_seq`（02 `work_item:<id>` 流的序号；缺失 → 422 **126062**）；人类可选（传了即同样判定）。事务顺序：取得工作项行锁 → 经 02 `WorkItemActivityReader.HeadOn(ctx, tx, work_item_id)` 读取当前序号 → 若 `activity_seq > seen_seq` 且其间存在主体 ≠ 调用者的活动 → 409 **126061**，`data={state:"interrupted", stream:"work_item:<id>", seen_seq, current_seq, newer_events[≤50], truncated}`（`newer_events` 取自 `WorkItemActivityReader.Since`），**不写任何数据**、不发事件、不审计业务动作，`interrupted` 不入 09 C7 幂等表，Agent 读完更新事件后可用同一 `Idempotency-Key`、新的 `seen_seq` 重试 → 否则按原顺序继续流转条件判定（§3.10）。成功流转在同一事务内调 `WorkItemActivityRecorder.RecordOn(ctx, tx, work_item_id, {kind:TRANSITIONED, actor, ref:{kind:"transition", id}, title})`。调用者自己产生的活动不构成打断；自动化规则（SYSTEM）触发的流转不要求 `seen_seq`。本操作登记为 `work_item_transition.apply`，成功与失败响应都带 `meta.next`。

---

## 6. 接口契约

### 6.1 通用

- Part A 沿用 02 §6.1（huma v2 + `respx`、i18n namespace `delivery`、`RegisterOwned` 项目守卫、ETag / If-Match、平台幂等 `176425` / `176409`、权限拒绝 403 `120403` + `reason_code`、OpenAPI tag = 最终发布路径第一段）。通用码复用 02：`120400`（输入 / 缺 If-Match 428）、`120403`、`120404`、`120409`（版本冲突）、`120420`（02 状态机非法迁移）。
- Part B 模块 `integration`：i18n namespace `integration`；团队级资源用 `authz.Register` + 权限码；入站 Webhook 用 `RegisterPublic` + 签名校验；错误码段 `190000–194999`；通用码 `190400` / `190403` / `190404` / `190409`。
- 列表接口支持 `pkg/query`（`field=op:value`，offset 分页，`capabilities`）。

### 6.2 错误码

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 126001 | 422 | `delivery.error.state_group_incompatible` | 显示状态的组与业务状态不相容（§4.1） |
| 126002 | 422 | `delivery.error.transition_not_allowed_by_core` | 矩阵勾选了 02 不允许的迁移 |
| 126003 | 409 | `delivery.error.state_in_use` | 归档的状态仍有工作项且未指定迁移目标 |
| 126004 | 409 | `delivery.error.transition_condition_failed` | 流转条件未通过；`data.failed[]` 逐条给出 `{kind, detail, missing_fields?, missing_criteria?}` |
| 126005 | 409 | `delivery.error.transition_disabled` | 矩阵未勾选或 `manual_allowed=false` |
| 126006 | 409 | `delivery.error.defect_stage_evidence_missing` | 推进 `defect_stage` 缺少证据或证据提交与合入提交不符（BR-12-35） |
| 126007 | 409 | `delivery.error.defect_reopen_source_not_terminal` | 「再次打开」新建缺陷时原缺陷尚未终态（BR-12-36） |
| 126010 | 409 | `delivery.error.review_sheet_closed` | 评审单非 OPEN |
| 126011 | 409 | `delivery.error.review_insufficient` | 有效人类意见不足 `min_reviewers` |
| 126012 | 409 | `delivery.error.review_already_open` | 目标已有 OPEN 评审单 |
| 126013 | 422 | `delivery.error.review_per_ac_required` | 拒绝未逐条给验收标准意见 |
| 126020 | 422 | `delivery.error.defect_fields_required` | 缺陷缺重现步骤 / 期望 / 实际 |
| 126030 | 409 | `delivery.error.version_locked` | 版本已锁定 |
| 126031 | 409 | `delivery.error.iteration_closed` | 迭代已关闭 |
| 126040 | 422 | `delivery.error.worklog_daily_limit` | 单人单日工时超 1440 分钟 |
| 126041 | 409 | `delivery.error.worklog_immutable` | Agent 自动工时不可编辑 / 已审批工时只能撤回 |
| 126050 | 422 | `delivery.error.test_case_criterion_required` | 用例未挂验收标准 |
| 126051 | 422 | `delivery.error.test_report_plan_limit` | 报告关联计划超过 15 |
| 126060 | 422 | `delivery.error.automation_invalid` | 规则触发 / 条件 / 动作不合法 |
| 126061 | 409 | `delivery.error.write_interrupted` | 工作项已有他人的更新活动，流转 / 转缺陷未写入；`data.state="interrupted"` + `newer_events`（BR-12-38，D120） |
| 126062 | 422 | `delivery.error.seen_seq_required` | Agent 流转未携带 `seen_seq`（BR-12-38，D120） |
| 126063 | 422 | `delivery.error.comment_not_convertible` | 批注不可转为缺陷：`data.reason ∈ {CLIENT_THREAD, ALREADY_CONVERTED}`，已转换时带 `data.target_id`（BR-12-37，D120） |
| 190010 | 409 | `integration.error.unmapped_states` | 存在未确认的状态映射 |
| 190011 | 409 | `integration.error.import_in_progress` | 已有非终态导入任务 |
| 190012 | 409 | `integration.error.dry_run_required` | 未完成或未确认 dry run |
| 190020 | 502 | `integration.error.upstream_failed` | 云效接口失败（`data.upstream_status`） |
| 190021 | 401 | `integration.error.webhook_signature` | 入站签名不符 |
| 190022 | 409 | `integration.error.connection_not_active` | 连接非 ACTIVE |
| 190023 | 422 | `integration.error.project_already_mapped` | 项目已映射到其他云效项目 |

### 6.3 Part A 接口

| # | 方法 路径 | 权限码 · 守卫 | 请求 / 响应要点 | 幂等 |
|---|---|---|---|---|
| A1 | GET `/entities/{entity_id}/iterations` | `work_item_read` · Owned | 过滤 `state`；排序 `starts_at` | — |
| A2 | POST `/entities/{entity_id}/iterations` | `workflow_manage` | `{name*, goal, goal_id?, starts_at*, ends_at*}` | ✔ |
| A3 | PATCH `/iterations/{iteration_id}` | `workflow_manage` · If-Match | 改名、日期；`state` 经 A4 | — |
| A4 | PUT `/iterations/{iteration_id}/state` | `workflow_manage` · If-Match | `{state: ACTIVE\|CLOSED, carry_over_to?}`；关闭时结转（BR-12-06） | ✔ |
| A5 | GET/POST `/entities/{entity_id}/versions`、PATCH `/versions/{version_id}` | 读 `work_item_read`；写 `workflow_manage` | | ✔ |
| A6 | PUT `/versions/{version_id}/lock` | `workflow_manage` · If-Match | `{locked: bool, reason?}`；解锁理由必填 | ✔ |
| A7 | GET/POST `/entities/{entity_id}/milestones`、PATCH `/milestones/{milestone_id}` | 同 A5 | | ✔ |
| A8 | PUT `/work-items/{work_item_id}/planning` | `work_item_write` · If-Match | `{iteration_id?, version_id?, milestone_id?}`；版本锁定 → 126030 | — |
| A9 | GET/PUT `/work-items/{work_item_id}/defect` | 读 `work_item_read`；写 `work_item_write` · If-Match | 3.7 全部字段 | — |
| A10 | GET `/tenants/{tenant_id}/workflows?project_id=&type=` | `work_item_read` | 返回生效配置（团队默认 + 项目覆盖合并）：状态列表 + 矩阵 + 条件 | — |
| A11 | PUT `/tenants/{tenant_id}/state-definitions/{key}?type=&project_id=` | `workflow_manage` · If-Match | 新建 / 修改显示状态；`archive` 时需 `migrate_to` | ✔ |
| A12 | PUT `/tenants/{tenant_id}/transition-rules?type=&project_id=` | `workflow_manage` · If-Match（矩阵整体版本） | 整张矩阵 `{cells:[{from, to, enabled, manual_allowed, conditions}]}`；保存前预检 02 合法性；返回影响预览 `{projects_affected, items_in_archived_states}`；`?dry_run=true` 只返回预览 | ✔ |
| A13 | POST `/work-items/{work_item_id}/transitions` | `work_item_write`（再经条件 ACTOR_SCOPE 与 02 授权） | `{to_state_key*, fields?:{...补填字段}, comment?}`；条件失败 409 126004（含缺失项）；成功返回新状态 | ✔ |
| A14 | GET/POST `/tenants/{tenant_id}/automation-rules`、PATCH/DELETE→`/automation-rules/{rule_id}`（DELETE = 归档） | 读 `work_item_read`；写 `workflow_manage` | 规则 JSON；`?test_event_id=` 试运行（不执行动作，返回将执行的动作） | ✔ |
| A15 | GET `/automation-rules/{rule_id}/runs`、GET `/work-items/{work_item_id}/automation-runs` | `work_item_read` | 执行日志 | — |
| A16 | GET/POST `/tenants/{tenant_id}/review-types`、PATCH `/review-types/{review_type_id}` | 读 `work_item_read`；写 `workflow_manage` | | ✔ |
| A17 | POST `/requirements/{requirement_id}/review-sheets`、POST `/work-items/{work_item_id}/review-sheets` | `review_initiate` | `{review_type_id*, reviewer_ids?, due_at?}` | ✔ |
| A18 | GET `/review-sheets/{review_sheet_id}` | `work_item_read` | 含意见（每人最新 + 历史） | — |
| A19 | POST `/review-sheets/{review_sheet_id}/opinions` | `review_participate`（被邀请者） | `{verdict*, per_ac?[], comment?}` | ✔ |
| A20 | PUT `/review-sheets/{review_sheet_id}/state` | 发起人 · If-Match | `{state: PASSED\|REJECTED\|CANCELLED, reason?}`（PASSED / REJECTED 由系统按意见计算，调用方只能请求「结束」，见 RS3） | ✔ |
| A21 | GET/POST `/entities/{entity_id}/test-cases`、PATCH `/test-cases/{test_case_id}` | 读 `work_item_read`；写 `test_case_manage` | 隐藏验收用例按 BR-12-17 过滤 | ✔ |
| A22 | POST `/entities/{entity_id}/test-case-imports` | `test_case_manage` | `multipart` csv（云效 Testhub 列格式）≤500 行 / 次；返回导入报告 | ✔ |
| A23 | GET/POST `/entities/{entity_id}/test-plans`、PATCH `/test-plans/{test_plan_id}` | 读 `work_item_read`；写 `test_plan_manage` | | ✔ |
| A24 | PUT `/test-plans/{test_plan_id}/runs/{test_case_id}` | `test_run_execute` | `{result*, evidence_id?, note?, defect?:{title, severity, priority}}`（附 defect 时同步创建缺陷） | ✔ |
| A25 | POST `/tenants/{tenant_id}/test-reports` | `test_plan_manage` | `{plan_ids* ≤15, title}` → 异步生成 PDF（09 JobQueue），返回报告 id | ✔ |
| A26 | GET/POST `/principals/{principal_id}/worklogs`、PATCH `/worklogs/{worklog_id}` | 本人 `worklog_submit`；他人只读需 `worklog_approve` | | ✔ |
| A27 | PUT `/worklogs/{worklog_id}/state` | 提交：本人；审批 / 撤回：`worklog_approve`（仅人类，非本人） | `{state, reason?}` | ✔ |
| A28 | GET `/entities/{entity_id}/worklog-summary?from=&to=&group_by=` | `work_item_read` | 汇总（BR-12-23） | — |
| A29 | GET `/work-items/{work_item_id}/code-links` | `work_item_read` | | — |
| A30 | POST `/work-items/{work_item_id}/code-links` | `work_item_write` | 手动关联 `{kind, repository, ref, url?}`（本地 git 由 03 机器通道上报自动关联） | ✔ |
| A31 | POST `/comment-threads/{thread_id}/defects` | `work_item_write`（线程所属项目）· Owned；AGENT 另带 `seen_seq`（BR-12-38） | `{title?, severity*, priority?, steps?, expected?, actual?, found_in_work_item_id?}`，缺省按锚点预填（BR-12-37）→ 201 工作项（BUG）+ 缺陷详情（含 `comment_thread_id`、`anchor_snapshot`）；422 `126063` / `126020`；409 `126061` | ✔ |

**A13 请求增补（D120）**：`seen_seq?`（AGENT 必填，BR-12-38）；409 `126061`、422 `126062`；响应带 `meta.next`。

### 6.4 Part B 接口（v1.1）

| # | 方法 路径 | 权限码 · 守卫 | 要点 | 幂等 |
|---|---|---|---|---|
| B1 | GET/POST `/tenants/{tenant_id}/integration-connections`、PATCH `/integration-connections/{connection_id}` | `integration_manage`（仅人类，step-up 用途 `integration_credentials`） | 令牌写入后不可读，只回显末 4 位 | ✔ |
| B2 | POST `/integration-connections/{connection_id}/checks` | `integration_manage` | 检测连通性与可用能力，更新 state / token_scopes | ✔ |
| B3 | GET `/integration-connections/{connection_id}/external-projects` | `integration_manage` | 列出云效项目（分页透传） | — |
| B4 | GET/POST `/integration-connections/{connection_id}/project-mappings`、PATCH `/project-mappings/{mapping_id}` | `integration_manage` · If-Match | `{external_project_id, project_id, sync_mode, field_masters, codeup_repositories, trusted_pipelines}` | ✔ |
| B5 | GET/PUT `/project-mappings/{mapping_id}/state-mappings` | `integration_manage` | 自动建议 + 人工确认 | ✔ |
| B6 | POST `/project-mappings/{mapping_id}/import-jobs` | `integration_manage` | `{scope, dry_run: true}` 先 dry run | ✔ |
| B7 | PUT `/import-jobs/{import_job_id}/state` | `integration_manage` · If-Match | `{state: RUNNING\|CANCELLED}`（确认 dry run 后开跑） | ✔ |
| B8 | GET `/import-jobs/{import_job_id}`、`/project-mappings/{mapping_id}/sync-log` | `integration_manage` | 进度、计数、报告文件 | — |
| B9 | POST `/integration-webhooks/{connection_id}/codeup` | 公开 + 签名（`X-Codeup-Token` 常量时间比较） | push / merge_request / note；202 快速返回，异步处理 | 入站去重 |
| B10 | POST `/integration-webhooks/{connection_id}/flow` | 公开 + 签名（自定义步骤回调携带 HMAC） | `{pipeline_id, run_id, commit, stage_results[{name, status, redlines[{metric, op, threshold, value, passed}]}], manual_gates[{name, mode: AND\|OR, approvers[], result}]}` | 入站去重 |
| B11 | POST `/integration-webhooks/{connection_id}/projex` | 公开 + 签名 | 云效自动化规则出站（字段 / 状态变更，载荷为工作项 JSON，**待核实**格式） | 入站去重 |

---

## 7. 后台任务与事件

### 7.1 任务（经 09 JobQueue）

| 任务 | 触发 / 频率 | 互斥 | 说明 |
|---|---|---|---|
| `delivery.automation_dispatch` | 消费白名单事件（09 订阅，Transactional） | 按规则 + 工作项串行 | 评估条件、执行动作、写执行日志；动作失败不重试（写 FAILED），避免风暴 |
| `delivery.automation_schedule` | cron（规则配置） | dlock `delivery:automation:<rule_id>` | 定时类规则 |
| `delivery.test_plan_sync` | 事件 + 每 10 分钟 | dlock 每计划 | BR-12-15 |
| `delivery.test_report_render` | 按需 | — | 生成 PDF，写 files，计算哈希 |
| `delivery.iteration_close_carry_over` | 迭代关闭时 | 每迭代 | BR-12-06 |
| `delivery.workflow_seed` | 租户创建 / 模板安装 | 每租户 | 写入软件模板默认状态、矩阵、评审类型、字典（数据来自 11） |
| `delivery.agent_worklog` | 消费 `execution.state_changed{terminal}` | 幂等于 execution_id | BR-12-22 |
| `integration.import` | B7 | 每映射一个 | 分页拉取，批量写入（每批 200），可续跑 |
| `integration.inbound_process` | 入站落表后 | 每连接串行 | 解析、生成证据 / 镜像裁决 / 应用字段 |
| `integration.outbound_sync` | 消费 02 / 12 变更事件（BIDIRECTIONAL） | 每对象串行 | 只推主控字段 |
| `integration.reconcile` | 每 15 分钟 | dlock 每映射 | 对比外部修改时间，补拉遗漏（Webhook 丢失兜底） |
| `integration.connection_check` | 每 6 小时 + ERROR 时每 10 分钟 | 每连接 | 更新 state |

### 7.2 发布事件

`iteration.state_changed`、`version.locked`、`version.released`、`workflow.config_changed`、`review_sheet.opened`、`review_sheet.closed`、`test_run.recorded`、`test_report.generated`、`worklog.submitted`、`worklog.approved`、`worklog.returned`、`worklog.withdrawn`、`code_link.created`、`automation.run_failed`、`integration.connection_state_changed`、`integration.import_finished`、`integration.sync_conflict`；以及按 00 §15.5 发布 `quality.escaped`（BR-12-14）。SSE 主题：`tenant/{t}/review_sheet/{id}`、`tenant/{t}/test_plan/{id}`、`tenant/{t}/import_job/{id}`（TopicAuthorizer：读权限码同 6.3 / 6.4）。

### 7.3 消费事件

02 `work_item.state_changed`、`work_item.created`、`claim.submitted`；03 `verification.completed`、`internal_qa.passed` / `failed`、`execution.state_changed`；`requirement.versioned`（RS5）；07 `acceptance.received`（客户拒收的缺陷穿透标记提示）；11 `template.installed`（触发种子任务）。

### 7.4 审计

`delivery.workflow_config_changed`（含 diff）、`delivery.version_lock_changed`、`delivery.review_sheet_closed`、`delivery.worklog_approved`、`integration.credentials_changed`、`integration.mapping_changed`、`integration.import_started` / `finished`、`integration.webhook_signature_failed`（限频合并）；D120：`delivery.defect_created_from_comment`（BR-12-37，detail 不含正文）。

---

## 8. 页面规格

### 8.1 流转配置 `/t/:tenantID/settings/workflows`（与项目内 `/p/:projectID/settings/workflows`）

- **布局**：左侧工作项类型列表（需求、任务、缺陷、风险、线上故障、…）；中间「状态」面板：按 5 个状态组分栏，拖拽排序，新建状态需选组与业务状态（不相容的选项置灰并说明）；右侧「流转矩阵」：行 = 从，列 = 到，勾选格；点击格子打开条件编辑抽屉（四类条件分区，证据条件有醒目说明「不可在单次流转中豁免」）。
- **保存**：先显示影响预览（受影响项目、处于将归档状态的工作项数及迁移目标选择）；项目页显示「继承团队默认 / 已覆盖」，可一键恢复。
- **状态**：加载骨架；无权限（缺 `workflow_manage`）只读；冲突（他人已改）提示重新加载并保留本地未保存修改对比。
- **云效迁移提示**：顶部提示条「习惯云效？状态 = 云效状态，状态组 = 云效阶段，矩阵 = 云效流转规则」。

### 8.2 需求评审单 `/p/:projectID/requirements/:requirementID/reviews/:sheetID`

- 头部：评审类型、发起人、截止、状态、需求版本（与当前版本不同时提示「需求已更新，评审将被取消」）。
- 主体：验收标准列表，每条右侧为各评审人的逐条意见（PASS / REJECT / SUGGEST 徽章 + 评论）；底部评审人汇总（有效意见 n / 最少 m，AI 建议单列不计数）。
- 操作：评审人「提交意见」（拒绝时每条被拒标准必填意见）；发起人「结束评审」（不足人数时禁用并说明）、「取消」。
- 结束后：显示结论与生成的返工项链接。

### 8.3 用例与测试计划 `/p/:projectID/test-cases`、`/p/:projectID/test-plans/:planID`

- 用例库：按需求 → 验收标准树组织，显示每条标准的用例数与最新覆盖结果；隐藏验收用例对无权限者完全不显示；导入 csv 向导（列映射预览、失败行下载）。
- 测试计划：范围（迭代 / 版本，自动同步开关）、执行进度条（通过 / 失败 / 阻塞 / 未执行）、执行面板（逐步勾选、截图上传、失败一键提缺陷预填）、按执行者类型（人 / 测试 Agent）分组统计；「生成测试报告」（选择 ≤15 个计划）→ PDF 预览、加入交付包。

### 8.4 工时 `/t/:tenantID/worklogs`

- 我的工时：周视图日历格（按工作项 × 日），快速填报，按类别着色，单日合计提示；提交本周。
- 审批：待审批列表（按人），批量审批限同一人同一周；Agent 运行时长单独一栏（只读）。
- 汇总：按项目 / 迭代 / 类别 / 人与 Agent 的柱状汇总，导出 csv。

### 8.5 集成设置 `/t/:tenantID/settings/integrations/yunxiao`

- **连接**：组织 ID、端点、令牌（只写）、Webhook 地址与密钥（复制）、「检测连接」→ 能力清单；状态徽章与最近错误。
- **项目映射**：云效项目 ↔ 本系统项目、同步模式、字段主控方表（每个字段一行，单选主控方，双向模式下高亮冲突风险）、关联 Codeup 仓库、可信流水线。
- **状态映射**：左云效状态（按类型分组），右本系统显示状态下拉，自动建议带置信度，必须全部确认才能导入。
- **导入向导**：范围 → dry run → 报告（数量、未映射、无验收标准的需求、无法匹配的人员）→ 确认导入 → 进度（实时）→ 结果与失败明细下载。
- **同步日志**：方向、对象、字段、结果（冲突以主控方为准的记录高亮）、筛选与导出。
- 顶部常驻提示：「集成功能基于云效公开文档设计，部分接口与格式需按你组织的实际配置核对」。

### 8.6 工作项详情的新增区块（嵌入 02 §8.3）

- 规划：迭代 / 版本（锁定图标）/ 里程碑。
- 缺陷详情（BUG / INCIDENT）：字段卡 + 「穿透」标记（如有）。
- 代码：关联的 commit / MR 列表（状态、来源、证据链接）。
- 评审：评审单历史。
- 用例：本工作项相关验收标准的用例与最新结果。
- 工时：按人与 Agent 的汇总。
- 自动化：本工作项上的规则执行日志（失败红色可见）。
- 来源批注（D120）：由批注转来的缺陷显示锚点快照（位置、原文、版本「基于 commit c1」）与〔打开原批注〕（03 远程工作区查看器定位到锚点）；原锚点已过期时标「锚点版本已过期」。

---

## 9. 通知（路由与偏好由 08 管理）

| 事件 | 接收人 | 默认渠道 | 文案要点 | 合并 |
|---|---|---|---|---|
| `review_sheet.opened` | 被邀请的人类评审人 | 站内（收件箱 `REVIEW_SHEET`）+ 邮件摘要 | 「{发起人} 邀请你评审 {需求}，截止 {due}」 | 同一评审单不重复 |
| 评审截止前 24 小时未提交 | 未提交的评审人 | 站内 | 「{需求} 的评审将于明天截止」 | 每评审单一次 |
| `review_sheet.closed`（REJECTED） | 发起人、需求负责人 | 站内 | 「评审未通过，已生成 {n} 个返工项」 | |
| `automation.run_failed` | 规则创建者、`workflow_manage` 持有者 | 站内 | 「规则 {name} 在 {工作项} 上执行失败：{原因}」 | 同规则 1 小时内合并 |
| 工时待审批 | 审批人 | 站内（每周一汇总） | 「{n} 条工时待你审批」 | 每周 |
| `integration.connection_state_changed{ERROR}` | `integration_manage` 持有者 | 站内 + 邮件 | 「云效连接异常：{错误}，同步已暂停」 | 6 小时内合并 |
| `integration.import_finished` | 发起人 | 站内 | 「导入完成：新建 {c}，更新 {u}，失败 {f}」 | |
| `integration.sync_conflict` | 映射负责人 | 站内（每日汇总） | 「{n} 个字段冲突已按主控方处理」 | 每日 |

---

## 10. 验收标准

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-12-01 | integration | US-12-01, BR-12-01 | **G** 软件模板已安装 **W** 新建项目 **T** 存在需求（Requirement）与任务 / 缺陷 / 风险 / 线上故障四种工作项类型的默认状态与矩阵，状态组齐全 |
| AC-12-02 | integration | BR-12-02 | **G** 创建 BUG 未填重现步骤 **W** 保存 **T** 422 `126020`，`data.missing=["steps"]` |
| AC-12-03 | unit | §4.1 | **G** 新建显示状态组 `DONE`、maps_to `REWORK` **W** 保存 **T** 422 `126001` |
| AC-12-04 | integration | §4.1 | **G** 矩阵勾选 `ready → closed`（02 不允许 READY → ACCEPTED）**W** 保存矩阵 **T** 422 `126002`，指出该格 |
| AC-12-05 | integration | BR-12-10 | **G** `deployed → internal_qa` 无条件；`verify → closed` 有 `EVIDENCE: EVERY_AC_HAS_PASS_EVIDENCE`，工作项 W 的标准 AC2 无 PASS 证据 **W** 人请求 `verify → closed` **T** 409 `126004`，`data.failed[0]={kind:"EVIDENCE", missing_criteria:["AC2"]}`，W 状态不变 |
| AC-12-06 | integration | BR-12-10 | **G** 流转要求 `fixed_version_id` 且 W 未填 **W** 请求流转并在 `fields` 中提供该字段 **T** 一次成功，字段写入，其他已有字段不被覆盖 |
| AC-12-07 | integration | BR-12-11 | **G** Agent A 持 `work_item_write` **W** A 请求映射到 `ACCEPTED` 的流转 **T** 403 `120403`（`reason_code=AGENT_PROHIBITED`） |
| AC-12-08 | integration | §3.10 | **G** `ACTOR_SCOPE.work_item_roles=["REVIEWER"]` 用于 `verify → closed`，调用者是执行者 **W** 请求 **T** 409 `126004`（`kind=ACTOR_SCOPE`） |
| AC-12-09 | integration | BR-12-08 | **G** 团队默认矩阵被修改，项目 P1 未覆盖、P2 已覆盖 **W** 保存（dry_run）**T** 预览 `projects_affected=1`；正式保存后 P1 立即生效，P2 不变 |
| AC-12-10 | integration | BR-12-09 | **G** 显示状态 `merged` 上有 3 个工作项 **W** 归档且未给 `migrate_to` **T** 409 `126003`；给出同组同 maps_to 的目标后成功，3 项迁移并写审计 |
| AC-12-11 | integration | BR-12-05 | **G** 版本 V 已锁定 **W** 把工作项移入 V **T** 409 `126030`；版本负责人解锁（带理由）后成功 |
| AC-12-12 | integration | BR-12-06 | **G** 迭代 I 有 2 个未完成工作项，项目设置「结转到下一迭代」**W** 关闭 I **T** 2 项进入下一迭代，10 报告草稿的「未完成」出现原因 `CARRIED_OVER` |
| AC-12-13 | integration | RS1–RS3, BR-12-13 | **G** 评审类型 min=2，评审人 H1、H2（人）与 Agent G **W** H1 PASS、G SUGGEST、H2 REJECT（AC3 意见）后发起人结束 **T** 评审单 REJECTED；为 AC3 生成 1 个返工项；需求回到「待完善」；G 的意见显示为 AI 建议不计数 |
| AC-12-14 | integration | RS3 | **G** min=2，只有 H1 与 G 提交 **W** 结束 **T** 409 `126011` |
| AC-12-15 | integration | RS2「以最后一次为准」 | **G** H1 先 REJECT 后 PASS **W** 结束 **T** 以 PASS 计；两条意见均保留，第一条标「已被替代」 |
| AC-12-16 | integration | BR-12-12 | **G** Agent G 是评审人 **W** G 提交 `PASS` **T** 403 `120403`（`AGENT_PROHIBITED`） |
| AC-12-17 | integration | RS5 | **G** OPEN 评审单针对需求 v3 **W** 需求升 v4 **T** 评审单 CANCELLED，发起人收到「需求已更新，请重新发起」 |
| AC-12-18 | integration | BR-12-15 | **G** 计划 AUTO 同步迭代 I；I 内需求新增 1 条用例 **W** 等待同步 **T** 计划新增 1 个执行行（UNEXECUTED） |
| AC-12-19 | integration | BR-12-16 | **G** 标准 AC1 下 2 条用例，最新结果 PASSED / FAILED **W** 请求含 `EVIDENCE` 条件的验收流转 **T** 拒绝，AC1 显示 FAIL；第二条改为 PASSED 后 AC1 写入 PASS 证据，流转成功 |
| AC-12-20 | e2e | BR-12-17 | **G** 用例 C 为隐藏验收；执行 Agent E、测试 Agent T **W** E 与 T 列出 / 读取 C **T** 列表不含 C；按 id 读取 404；人类（持 `acceptance_suite_manage`）可见 |
| AC-12-21 | integration | §4.4 | **G** 执行 FAILED 时附 defect **W** 提交 **T** 创建 BUG（预填步骤、关联计划 / 迭代 / 用例），执行行 `defect_ids` 含新缺陷 |
| AC-12-22 | integration | BR-12-20 | **G** 选择 16 个计划生成报告 **W** 提交 **T** 422 `126051`；15 个成功生成 PDF，内容哈希固定，再次读取一致 |
| AC-12-23 | integration | BR-12-21 | **G** H 当天已记 1400 分钟 **W** 再记 60 分钟 **T** 422 `126040` |
| AC-12-24 | integration | BR-12-22 | **G** Agent 执行 X 终态（12 分 30 秒，$0.84）**W** 事件重复投递两次 **T** 只有 1 条 `AGENT_RUNTIME` 工时：13 分钟、840000 micros、APPROVED；尝试编辑 → 409 `126041` |
| AC-12-25 | integration | §4.5 | **G** H 提交工时 **W** H 自己审批 **T** 403 `120403`（`SELF_REVIEW`）；他人审批成功 |
| AC-12-26 | integration | BR-12-24 | **G** commit message「修复舍入 #WI-139 #WI-999」，WI-999 不存在 **W** 本地 git 上报 **T** WI-139 新增 COMMIT 关联与证据；WI-999 忽略并记同步日志 |
| AC-12-27 | integration | A14, A15 | **G** 规则「子任务全部完成 → 父需求的任务进入 VERIFYING」**W** 最后一个子任务完成 **T** 父项流转成功，执行日志 SUCCEEDED；若父项流转条件不满足 → 日志 FAILED 含原因，且规则创建者收到通知 |
| AC-12-28 | unit | §3.11 | **G** 同一规则 1 小时内触发 61 次 **W** 第 61 次 **T** 记 SKIPPED（速率限制），不执行动作 |
| AC-12-29 | integration | BR-12-14 | **G** 甲方验收拒收发现缺陷 D（内部质量门已放行）**W** 交付负责人创建 D 并标 `escaped_from_stage=AGENT_ACCEPTANCE` **T** 发布 `quality.escaped`（07 已就同一回执发布时只引用、不重复发布）；02 `delivery.escape_learning` 经 06 生成经验记忆草稿、经 11 生成评测用例 |
| AC-12-30 | integration | B1, B2 | **G** 新连接，令牌有效但无流水线读权限 **W** 检测 **T** ACTIVE，`token_scopes` 不含 pipeline_read；映射页「可信流水线」禁用并说明 |
| AC-12-31 | integration | BR-12-25 | **G** dry run 报告含 2 个未映射状态 **W** 确认导入 **T** 409 `190010`；全部确认后导入成功，计数与报告一致 |
| AC-12-32 | integration | BR-12-25 | **G** 同一范围导入两次，第二次之间云效有 3 个工作项被修改 **W** 第二次导入 **T** 只更新 3 项，其余 skipped；无重复工作项 |
| AC-12-33 | integration | BR-12-01 | **G** 云效需求 Q 无验收标准 **W** 导入 **T** Q 作为需求草稿导入，报告列出，Q 下无法拆出可派发工作项（就绪闸门） |
| AC-12-34 | integration | BR-12-30 | **G** Codeup 入站 `X-Codeup-Token` 错误 **W** POST B9 **T** 401 `190021`，不落库；正确签名的同一 delivery_key 重复投递两次 → 第二次 200 幂等忽略 |
| AC-12-35 | integration | BR-12-24, B9 | **G** MR 标题含 `#WI-142`，状态 merged **W** 入站处理 **T** WI-142 新增 MR 关联与 `PULL_REQUEST` 证据；流转条件 `LINKED_MR_MERGED` 变为满足 |
| AC-12-36 | integration | BR-12-27 | **G** Flow 回调流水线 P 不在可信列表 **W** 处理 **T** 证据记为参考，不推进 VERIFYING；P 加入可信列表且 commit 等于 claim 的 `head_commit` → 记为 `AUTOMATED` 证据 |
| AC-12-37 | integration | BR-12-28 | **G** Flow 人工卡点会签通过 **W** 处理 **T** 生成只读外部裁决记录（`EXTERNAL:YUNXIAO_FLOW:P`），工作项仍需本系统 L3 裁决 |
| AC-12-38 | integration | BR-12-29 | **G** 字段 `assignee` 主控 YUNXIAO、`state` 主控 MYRMIDON；同周期两端都改了 state 与 assignee **W** 同步 **T** state 以本系统为准推到云效、assignee 以云效为准写入本地；两条 `CONFLICT_MASTER_WINS` 日志 |
| AC-12-39 | integration | BR-12-29 | **G** 云效状态变更映射到本系统不可达状态 **W** 入站同步 **T** 不应用，记 FAILED，映射页提示 |
| AC-12-40 | integration | §4.6 | **G** 云效连续 5 次 401 **W** 第 5 次后 **T** 连接 ERROR，同步暂停，入站继续落表；令牌更新并检测通过后恢复并处理积压 |
| AC-12-41 | integration | §4.2.1 F5–F7, BR-12-35 | **G** 缺陷 MR 已合入、必需验证全部通过，但无部署证据 **W** 验证完成 **T** 02 停在 VERIFYING，显示「已合入 · 等待部署」；可信流水线部署事件到达后 `defect_stage=DEPLOYED` 且进入 INTERNAL_QA |
| AC-12-42 | integration | BR-12-35 | **G** 手工登记的部署证据指向不含合入提交的制品 **W** 提交 **T** 409 `126006`，`defect_stage` 仍为 `MERGED` |
| AC-12-43 | integration | §4.2.1 F8, BR-02-54 | **G** 缺陷处于 internal_qa **W** 测试 Agent 判 FAILED **T** 回到 fixing，`defect_stage=NULL`；无 N2 / N3、无收件箱项 |
| AC-12-44 | integration | BR-12-34 | **G** 持 TESTER 角色的 Agent **W** 对 verify 状态缺陷执行关闭 **T** 403，`reason_code=AGENT_CANNOT_ACCEPT`；人类测试成员执行成功，`defect_stage=CLOSED` |
| AC-12-45 | integration | BR-12-33 | **G** 项目管理员 **W** 保存把 F3「暂不修复」开放给 Agent 的矩阵 **T** 422 `126002` `reason_key=defect_floor` |
| AC-12-46 | integration | BR-12-07, BR-12-36 | **G** 已关闭缺陷 D **W** 人类发起「再次打开」 **T** 新建 BUG，`reopened_from_work_item_id=D`；D 仍为 ACCEPTED；发布 `quality.escaped`；对处理中的缺陷发起 → 409 `126007` |
| AC-12-47 | integration | US-12-15, BR-12-37 | **G** 执行 E（关联工作项 W）上的 INTERNAL 线程 T：CODE_LINES `src/app.ts` 40–44（commit c1），首条批注「金额四舍五入错误」**W** 测试人员 A31 `{severity:MAJOR, expected:"保留两位小数"}` **T** 201 新 BUG：`steps` 含 `src/app.ts:40-44`、`c1` 与第 40–44 行原文，`actual` 为首条批注，`found_in_work_item_id=W`；`comment_thread_id=T`、`anchor_snapshot.version_ref=c1`；03 记 DEFECT 转换、T 变 RESOLVED；审计 `delivery.defect_created_from_comment` 不含正文 |
| AC-12-48 | integration | BR-12-37 | **G** T 已转为缺陷 D1；CLIENT 线程 T2 **W** 再对 T 调 A31；对 T2 调 A31 **T** 422 `126063`（`reason=ALREADY_CONVERTED`、`target_id=D1`），无新工作项；422 `126063`（`reason=CLIENT_THREAD`）；T 经 02 D1 转为结构化反馈仍成功（两种转换并存） |
| AC-12-49 | integration | BR-12-38 | **G** 缺陷 W（`activity_seq=20`），Agent A 持 `work_item_write`，读到 20 后人类评审人在 W 上新建批注（流到 21）**W** A 以 `seen_seq=20`、键 K 调 A13 **T** 409 `126061`，`data.state="interrupted"`、`current_seq=21`、`newer_events[0].kind=COMMENT_ADDED`；W 状态与流转日志不变；A 以同一键 K、`seen_seq=21` 重试 → 200 流转成功（无 176425），W 流到 22（TRANSITIONED） |
| AC-12-50 | integration | BR-12-38 | **G** 同上 **W** A 不带 `seen_seq` 调 A13；人类不带 `seen_seq` 调 A13；自动化规则触发流转 **T** 422 `126062`；人类 200；自动化 200（SYSTEM 不要求 `seen_seq`）；A 自己产生的活动在其后不构成打断 |

---

## 11. 度量与埋点

指标：流转条件拦截次数（按条件类型，证据条件拦截率是质量门有效性的信号）、评审一次通过率、评审平均时长、缺陷库存趋势（新增 / 解决 / 库存）、缺陷平均修复时长、日清率、按引入阶段 / 原因 / 模块分布、**穿透缺陷数与穿透率**（与 00 §15.5 一致）、用例覆盖率（有 PASS 用例的验收标准占比）、工时按类别占比（人 vs Agent）、自动化规则失败率、集成：入站延迟、同步冲突数、导入成功率。

埋点事件（不含正文与代码）：`workflow_config_saved{type, cells_changed, projects_affected}`、`transition_blocked{condition_kind}`、`review_sheet_closed{result, reviewers, rounds}`、`defect_created{severity, found_stage, escaped}`、`test_run_recorded{result, actor_kind}`、`worklog_submitted{category, actor_kind}`、`automation_run{outcome}`、`integration_import{outcome, counts}`、`integration_inbound{source, outcome}`、`integration_conflict{field}`。

---

## 12. 依赖、风险与待决

### 12.1 依赖

| 规格 | 需要 |
|---|---|
| 00 | v2.0 已提供 `StateGroup`、扩展的 `WorkItemType` / `WorkItemState`、`VerificationStage`、§15；本规格 §13 的 CR |
| 02 | 业务状态机与迁移接口（本规格的流转经 02 执行）；裁决（评审单结果）；工作项新增列迁移由本规格提供；Requirement 草稿语义 |
| 03 | `EvidenceReader`（证据条件、内部验收结果）、证据写入（代码关联、用例覆盖、外部 CI）、冻结 / 隐藏验收集可见性、执行终态事件 |
| 05 | 自动化动作「派给 Agent」经派单提案接口 |
| 06 | 穿透学习条目（经验记忆） |
| 07 | 交付包加入测试报告；甲方拒收回执 |
| 08 | 新权限码与默认角色、收件箱 `REVIEW_SHEET` / `WORKLOG_APPROVAL`、step-up 用途 `integration_credentials`、`SystemActions` 登记自动化规则身份 |
| 09 | JobQueue、outbox、SSE、`SecretSealer`、files 业务写入（PDF、入站载荷） |
| 10 | 报告草稿写入接口（自动化动作 `ADD_REPORT_ENTRY`）、读取工时汇总 |
| 11 | 软件模板的默认状态 / 矩阵 / 评审类型 / 字典数据文件；穿透评测集 |
| 02（D120） | `WorkItemActivityRecorder.RecordOn` / `WorkItemActivityReader.HeadOn` / `Since`（02 BR-02-64），用于 A13 与 A31 的 `seen_seq` 判定与 TRANSITIONED 活动 |
| 03（D120） | `ArtifactComments.GetOn` / `MarkConvertedOn`（03 BR-03-139），A31 读取线程与写 DEFECT 转换 |
| 05（D120） | `OperationRegistry` 登记 `work_item_transition.apply`、`defect_from_comment.create`；`interrupted` / `meta.next` 规则（05 BR-05-127 / 128 / 131） |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 云效接口与格式与公开资料不一致（多项仅检索摘要） | 集成全部放 v1.1；先用客户真实组织导出工作流配置与 OpenAPI 实测；适配层隔离；入站原始载荷留存便于修正后重放 |
| 配置过度灵活导致工作流膨胀（Jira 反模式） | 状态必须归组且映射业务状态；矩阵只能收紧不能放宽 02；每类型最多 20 个显示状态 |
| 双向同步回环或互相覆盖 | 单一主控方；同步日志；出站带来源标记，入站识别自身回声并忽略 |
| 外部 CI 结果被伪造或与提交不符 | 签名 + 可信流水线白名单 + commit 必须等于声明 `head_commit`；本地可重跑的检查仍由平台重跑 |
| 迁移期间团队两边都改 | 映射页强制选择同步模式；默认 `IMPORT_ONLY`，双向需显式开启并确认主控表 |

### 12.3 待决（附建议）

| # | 问题 | 建议 |
|---|---|---|
| Q-12-1 | 是否支持跨项目的流转配置模板（云效「项目模板」） | 用团队默认 + 项目覆盖即可满足；模板机制归 11（领域模板安装时写入团队默认） |
| Q-12-2 | 工时是否计入成本（人类人天单价） | MVP 不计；v2 可在 07 预算中加入人类成本（需定价策略） |
| Q-12-3 | 云效「需求」的产品类 / 技术类之分 | 作为需求标签导入（`kind:product` / `kind:tech`），不新增类型 |
| Q-12-4 | MCP 写云效时 Agent 身份 | 用连接级服务账号 PAT，云效侧显示为服务账号；本系统审计记录真实 Agent 主体 |

---

## 13. 契约变更请求（对 00）

> **合并状态（2026-10-12）**：CR-12-01..09 已并入 00（v2.0 §14 记录）、02（错误码段、BR-02-62 缺陷部署守卫）与 08（`AGENT_CANNOT_ACCEPT`、收件箱 `REVIEW_SHEET` / `WORKLOG_APPROVAL`、step-up `integration_credentials`、`EXTERNAL:<provider>:<ref>`）；`quality.escaped` 归属按 D114。

| # | 位置 | 变更 | 理由 |
|---|---|---|---|
| CR-12-01 | §9 错误码 | delivery 段细分：02 `120000–125999`、**12 Part A `126000–126999`**、07 `127000–129999`；新增 integration `190000–194999` | 本规格 Part A 位于 delivery 模块 |
| CR-12-02 | §1 / §3 | 新模块 `integration`（表前缀 `integration_`、版本表 `goose_integration`） | Part B |
| CR-12-03 | §6.1 | 新权限码：`workflow_manage`（流转配置、字典、迭代 / 版本 / 里程碑管理）、`review_initiate`、`review_participate`、`test_case_manage`、`test_plan_manage`、`test_run_execute`、`worklog_submit`、`worklog_approve`（仅人类，非本人）；`integration_manage` 已在 §15.5，补注「仅人类、需 step-up」 | Part A / B 操作 |
| CR-12-04 | §6.5 | 仅人类：评审单 `PASS` / `REJECT` 意见、工时审批、集成凭据管理 | BR-12-12、§4.5、B1 |
| CR-12-05 | §7.1 | 登记 §7.2 事件；`quality.escaped` 本规格为发布者之一 | 消费方 06 / 08 / 10 / 11 |
| CR-12-06 | §8 | 新接口：`WorklogReader`（12 → 10）、`WorkflowResolver`（12 → 02：解析显示状态与流转条件）、`TestCoverageReader`（12 → 03 / 07） | 跨模块读取 |
| CR-12-07 | §6 / 08 | 收件箱新类型 `REVIEW_SHEET`、`WORKLOG_APPROVAL`；step-up 用途 `integration_credentials`；`authority_source` 新前缀 `EXTERNAL:<provider>:<ref>`（只读外部裁决，不能满足 L3） | BR-12-28 |
| CR-12-08 | §3 | 入站去重表 `integration_inbound_events` 作为「外部入站」例外：09 receipts 只用于内部领域事件 | BR-12-30 |
| CR-12-09 | §15.2 / §6.5（reason_code 由 08 登记） | `reason_code` 新值 `AGENT_CANNOT_ACCEPT`（Agent 不得执行映射到 ACCEPTED 的流转，含持 TESTER 角色者）；「验证角色」= 持 TESTER 的**人类**成员或该工作项责任人 | BR-12-34、§4.2.1 F10 |
| CR-12-10 | §8 / §9（D120） | 使用 02 `WorkItemActivityRecorder` / `WorkItemActivityReader`、03 `ArtifactComments`、05 `OperationRegistry`；A13 适用 §9 过期上下文写入约定（`seen_seq`、`interrupted`）；不新增共享枚举、权限码与事件（批注转缺陷沿用 `work_item_write`） | BR-12-37 / 38 |

> **D120（2026-10-12，批注转缺陷与过期上下文写入）已并入本规格**：US-12-15、§3.7 增列 `comment_thread_id` / `anchor_snapshot`、§5.9（BR-12-37 / 38）、接口 A31 与 A13 的 `seen_seq`、错误码 126061–126063、审计 `delivery.defect_created_from_comment`、§8.6 来源批注区块、AC-12-47…50、CR-12-10。
