# 08 · 治理：权限目录、授权判定、委托、升级规则、审计、通知、我的裁决与总览

> 模块：`governance`（`internal/governance`，业务）+ 底座 IAM（`core` / `governance` / `audit` / `authn`）映射。遵守 `00-conventions.md`；对 00 的修改只在 §13 提出。标「本规格提议」处为 PRD / 00 未定、由本规格补齐的决定，均附理由。
> 已对齐：01（`intake_convert` 仅 AUTONOMOUS 可委托、`intake_decline` 与新建项目不可委托）、02（BR-02-19 Check 输入输出、`120403 + data.reason_code`、`work_item.escalated`、`ListEligible`）、03（交互 / 人工验证进收件箱、`DiffSummary`）、04（`machine_revoke` step-up）、06（`memory_approve` 不可委托）、07（预算与 `budget.threshold_reached` 归 07；`ProjectModeSwitcher.TakeOverOn`、`ProjectHoldSetter`、`ProjectGate`、`DeliveryMetricsReader`；`DelegationReader` / `EscalationRuleReader` 由本模块提供）。

---

## 1. 范围与目标

**解决**

1. **权限目录**：00 §6.1 全部权限码由谁经 `authz.Registry().Contribute` 注册；默认角色按租户幂等播种与目录增长后的升级；环境维度（生产访问单独授权）。
2. **`AuthorityChecker`**：在 IAM 角色授权之上判定领域条件（不可委托、职责分离、委托条件、策略规则），输出 `authority_source` 与可展示的解释；与 `authz.Register*` 路由守卫的分工（不形成第二个真相源）。
3. **授权委托**：数据模型、授予 / 撤销 / 到期 / 接管、在 IAM 实体角色绑定上的落地、使用计数、审计。
4. **升级规则**：规则种类与参数（含敏感路径模式）、触发事件、动作（项目 hold、委托冻结、转人、通知）、去重；升级事件（incident）状态机。
5. **业务审计视图**：在底座 `iam/audit` 哈希链上的事件类型目录与 detail Schema、项目级索引、完整性徽标、导出与保留（复用底座）。
6. **通知中心**：全产品通知类型的**规范目录**（接收人规则、渠道、模板键、合并 / 去重、免打扰）与用户偏好（经 `PreferenceResolver` 供 09 读取）；站内通知存储、收件 HTTP 与投递归 09。
6b. **控制台外壳**（§8.6）：顶栏域标签与当前域侧栏子导航（D118）、项目空间与项目切换器（D118 修订）、顶栏裁决徽标、移动端底栏、`⌘K` 命令面板。
7. **「我的裁决」**：跨模块待办读模型、与源状态的一致性（≤1s）、键盘操作、无批量通过、跨设备移除。
8. **总览**：各磁贴的数据源与刷新策略。
9b. **编制授权（D110，主管 Agent 自建 Agent）**：团队所有者给主管 Agent 的建 Agent 配额、角色 / 机器 / 模型范围、预算与审批方式；配额原子占用与释放、成本累计与耗尽、模式切换 / 接管时的收紧（§3.8.13–3.8.15、§4.5、BR-08-50…59）。Agent 的创建、收编、孤儿处理本身归 05。
9. **危险操作登记表**：step-up 重新认证的用途、目标格式、时效（复用底座 `authn` 重新认证）。

**不解决（边界）**

| 内容 | 归属 |
|---|---|
| 裁决本身（Decision 表、状态迁移、返工计数、原地打转检测、INCONCLUSIVE 暂停） | 02 |
| 交互授权与人工验证的写入、执行暂停 | 03 |
| 机器吊销流程 | 04 |
| Agent 生命周期、主管 Agent 派单策略、`AgentRole` | 05 |
| 记忆审批流程 | 06 |
| 项目模式、holds、预算记账与阈值（`budget.threshold_reached` 由 07 发布）、KPI 计算 | 07 |
| 通知渠道投递（Web Push、邮件发送）、站内通知存储与收件 HTTP、outbox、SSE、JobQueue、平台幂等、模块菜单贡献机制 | 09 |
| 甲方侧通知（回执、澄清、验收提醒，经开口发送） | 01 / 07 |
| 角色编辑器、访问申请 / 评审页面、审计明细 / 完整性 / 导出接口 | 底座 IAM（直接复用） |

---

## 2. 用户故事

| ID | 故事 | 期 |
|---|---|---|
| US-08-01 | 作为团队所有者，我要新团队开箱即有 7 个默认角色且随产品升级自动补齐新权限，以便不用手工配权限 | MVP |
| US-08-02 | 作为团队所有者，我要在一张矩阵里看清「哪个角色能做什么、哪些动作不可委托、哪些 Agent 正持有委托」，以便审查授权 | MVP |
| US-08-03 | 作为交付负责人，我要在裁决前看到「你以何身份裁决、权限来源、为何允许 / 拒绝」，以便理解与申辩 | MVP |
| US-08-04 | 作为团队所有者，我要把 approve / reject / request_changes / merge 在限定项目、类型、风险、成本、时长内委托给 Agent 评审员，以便低风险项无人值守推进 | MVP |
| US-08-05 | 作为团队所有者，我要看到每条委托的剩余时间与已用次数，并能立即撤销，撤销后正在进行的裁决不能再借用该委托 | MVP |
| US-08-06 | 作为团队所有者，我要一键接管全自治项目：撤销全部委托并切回混编 | MVP |
| US-08-07 | 作为团队所有者，我要配置升级规则（含迁移脚本、依赖升级、权限代码等敏感路径），触发时自动冻结委托、转人并通知我，且同一问题只提醒一次 | MVP |
| US-08-08 | 作为团队所有者，我要在全自治项目中无法关闭硬性升级规则，以便护栏不被误关 | MVP |
| US-08-09 | 作为审计者，我要按项目、模块、主体、权限来源筛选业务审计事件，看到哈希链完整性，并导出可校验快照 | MVP |
| US-08-10 | 作为团队成员，我要在「我的裁决」中处理所有需要我决定的事项，并用 J/K/A/R 键高效操作 | MVP |
| US-08-11 | 作为工程师「阿澈」，我在手机上处理了一项后，桌面端 ≤1s 内移除该项 | MVP |
| US-08-12 | 作为团队成员，我要按类型与渠道设置通知偏好与免打扰时段，紧急事项仍能送达 | MVP |
| US-08-13 | 作为交付负责人「Morgan」，我要打开总览一屏看到待我裁决的前 5 项、核心指标、团队与机器负载、项目健康与动态 | MVP |
| US-08-14 | 作为团队所有者，我要所有危险操作统一要求重新认证，且证明 5 分钟内一次性有效 | MVP |
| US-08-15 | 作为团队所有者，我要生产环境访问必须单独申请、他人批准、限时，且 Agent 永远不能获得 | MVP |
| US-08-16 | 作为团队所有者，我要项目级覆盖升级规则（比团队默认更严） | v1.1 |
| US-08-17 | 作为团队所有者，我要预约未来生效的委托 | v1.1 |
| US-08-18 | 作为团队所有者，我要给主管 Agent 一份「编制授权」：最多同时几个 Agent、每天最多建几个、可建哪些角色、用哪些机器和模型、总预算与单 Agent 预算、到期时间，以便它在边界内自己扩编 | MVP |
| US-08-19 | 作为团队所有者，我要选择主管 Agent 建 Agent 是「每次需人批准」还是「授权内自动」，且混编项目永远需要人批准 | MVP |
| US-08-20 | 作为团队所有者，我要在编制授权列表里看到配额占用、今日新建数、预算进度与剩余时间，并能立即撤销；接管项目时这些授权自动撤销 | MVP |
| US-08-21 | 作为审计者，我要按「人创建 / AI 创建」筛选 Agent 相关审计事件，并看到每个 AI 创建的 Agent 来自哪份编制授权 | MVP |

---

### 2.x D112 / D113 增补用户故事（v2.0，00 §15）

| ID | 用户故事 | 版本 |
|---|---|---|
| US-08-22 | 作为交付负责人，我只想在 Agent 团队内部（测试 Agent、产品验收 Agent）都通过之后才收到待裁决项；内部不通过的不应打扰我 | MVP |
| US-08-23 | 作为任何成员（含 Agent），我发现严重问题时可以拉 Andon 停线，由人确认处置后恢复 | MVP |
| US-08-24 | 作为团队所有者，人或甲方发现了内部已放行的问题时，我要看到穿透记录，并让测试 / 验收 Agent 从中学习 | MVP |
| US-08-25 | 作为交付负责人，我要确保 Agent 的执行计划由人或不同模型家族的评审者批准，且主管 Agent 不能批准自己派出的计划 | MVP |

### 2.y D120 增补用户故事（Agent 运行时协议、操作清单、锚定批注、团队工具目录）

| ID | 用户故事 | 版本 |
|---|---|---|
| US-08-26 | 作为团队所有者，我要 Agent 被拒时得到与人一致的原因码，并在「Agent 操作清单」里看到每个操作「可用 / 未授权 / 配额不足」的统一口径，以便解释和修正授权 | MVP |
| US-08-27 | 作为团队所有者，我要团队工具目录（MCP 服务器与技能）的管理与凭据只由人类掌握、不能委托给 Agent，凭据轮换需要重新认证，且每次经平台中继的工具调用都可审计 | MVP |
| US-08-28 | 作为 Agent 的负责人，我要只有我和被明确授权的人能查看该 Agent 的工作目录、收件箱与记忆，完全重置只能由人在重新认证后执行 | MVP |

## 3. 领域模型与数据表

### 3.1 IAM 复用与新建（总表）

| 需求 | 复用底座（不改动） | 本模块新增 |
|---|---|---|
| 权限码登记 | `authz.Registry().Contribute(actions, fields)`（`pkg/extension/authz/catalog.go`），各模块在 `InjectServices` 中贡献自己的码 | 本模块贡献 §3.2 的 6 个码 |
| 角色与角色权限、条件 | `iam_roles`、`iam_role_permissions.condition_json`（`policyx` 条件，只支持 `eq/neq/in` 于 `resource.attr.*`，数值比较仅 `auth.acr`） | 播种映射 `governance_role_seeds` |
| 项目范围授权、拒绝、到期 | `iam_role_bindings`（scope=entity，effect allow/deny，`expires_at`；查询时按 `now` 过滤；变更自增租户策略 revision；`PutEntityRoleBinding` 内含越权防护 `requireGrantableRole`） | 委托的载体（§3.6） |
| 授权判定与解释 | `AuthorizationEvaluator.ExplainResource(tenant, entity, resourceType, resourceID, code, principal)` → `authz.Explanation{allowed, reason, revision, matches[]}`；`policyx.WithResourceContext` 传入服务端事实 | `AuthorityChecker`（§3.5） |
| 临时提权、审批、到期 | `iam/governance` 访问申请（自申请、他人批准、`access_expires_at` ≤365 天、禁止自批 `ErrSelfApproval`、撤销）+ `iam_temporary_role_grants`（租户级，`source_type=access_request`） | 仅用于**生产访问**（§3.4）；委托**不**用它（理由见 D2） |
| 访问评审 | `iam/governance` access reviews（可移除实体角色绑定） | 评审移除委托绑定 → 对账任务撤销委托（BR-08-23） |
| 审计链 | `audit.Appender`（业务事务内追加，租户内 sequence 无间断哈希链）、`EventStreamReader`（不含 detail）、`/audit/events`、`/audit/events/{id}`、`/audit/integrity-verifications`、`/audit/exports`（NDJSON + 完成记录）、保留策略（默认 min 365 天）与归档、WORM 锚定 | 项目索引 `governance_audit_index`（§3.8.11） |
| 重新认证 | `authnext.ReauthenticationConsumer`（Cookie 会话、purpose + target 绑定、TTL 5 分钟、一次性、在调用方事务内先取主体锁）；前端 `ReauthenticationDialog` | 危险操作登记表（§6.12）；新增 purpose（CR-08-09） |
| 通知 | 09 `Notifier`（站内通知存储、收件 HTTP、推送与邮件投递均归 09） | 类型目录、接收人规则、偏好与合并（§9），经 `PreferenceResolver` 提供给 09 |

**D1 单一真相源分工**：IAM 只回答「主体是否持有某权限码（含角色条件与显式拒绝）」；governance 只回答「在领域事实下这一次动作是否被允许、权限来源是什么」。governance 不缓存、不复制角色 → 权限映射；其他模块不读角色。
**D2 委托不用访问申请 / 临时角色授权（本规格提议）**：后者是租户级（无项目范围）、语义为「自己申请」、`source_type` 写死 `access_request`；委托需要项目范围与授予者主动发起。改用实体角色绑定（项目范围 + `expires_at` + 底座越权防护 + revision 自增），PRD §8.3「临时角色授权 + `access_expires_at` 模型」的语义（限时、可撤销、全审计）完整保留。

### 3.2 权限目录登记

每个模块在自己的 `InjectServices` 中 `module.Resolve[*authz.Registrar]` 后调用一次 `Registry().Contribute(actions, nil)`；全部 `Scope:"tenant"`。

| 贡献模块 | 权限码 | DataScoped | Menu |
|---|---|---|---|
| intake | `intake_read` `intake_triage` `intake_convert` `intake_decline` `intake_channel_manage` `intake_submit` | 否 | `intake_read` |
| delivery（02/07） | `project_read` `project_manage` `project_stage_change` `project_mode_change` `requirement_read` `requirement_write` `work_item_read` `work_item_write` `work_item_assign` `work_item_execute` `work_item_cancel`（02 CCR-03） `decision_approve` `decision_reject` `decision_request_changes` `decision_pause` `scope_change` `delivery_read` `delivery_compose` `delivery_send` | 是 | `project_read` `work_item_read` `delivery_read` |
| delivery（02） | `decomposition_template_manage`（团队级模板） | 否 | 否 |
| execution | `execution_read` `execution_start` `execution_stop` `interaction_respond` `evidence_submit` `verification_run` `code_merge` | 是 | `execution_read` |
| execution | `verifier_manage`（团队级验证器定义） | 否 | 否 |
| fleet | `machine_read` `machine_manage` `machine_revoke` `gateway_manage` `agent_read` `agent_manage` `agent_retire` | 否 | `machine_read` `agent_read` |
| collab | `channel_read` `channel_post` `channel_manage` | 是 | `channel_read` |
| planning（10，D111） | `goal_read` `goal_manage` `goal_member_manage` `plan_submit` `plan_confirm` `report_read` `report_submit` `report_confirm` | 是 | `goal_read` `report_read` |
| planning（10） | `planning_settings_manage` | 否 | 否 |
| knowledge | `knowledge_read` `knowledge_write` `memory_approve` `knowledge_manage`（06 CR-06-06） | `knowledge_read/write`、`memory_approve` 是；`knowledge_manage` 否 | `knowledge_read` |
| **governance** | `policy_read` `policy_manage` `delegation_grant` `takeover` `audit_read` `spawn_grant_manage`（D110） | 否 | `policy_read` `audit_read` |
| **governance** | `agent_spawn`（D110，resource=`agent`，verb=`spawn`；**仅策略来源**，见下） | 是 | 否 |
| **governance** | `production_access`（CR-08-01，resource=`production`，verb=`access`） | 是 | 否 |

**常量集合（硬编码于 governance 根包，不可配置）**

- `NonDelegable` = { `scope_change`, `delivery_send`, `project_mode_change`, `policy_manage`, `delegation_grant`, `takeover`, `machine_revoke`, `production_access`, `intake_decline`（01 CR-10）, `memory_approve`（06 CR-06-06）, `project_create`（01 CR-10：以伪动作码表示「新建项目」，见 BR-08-04）, `spawn_grant_manage`（D110）}。
- `PolicyOnly` = { `agent_spawn` }（D110）：任何角色播种都不授予；S4 的角色允许对其**不生效**（显式拒绝仍生效），只能经 S7 策略规则 `spawn_grant` 放行；不进入 `Delegable`。
- `Delegable` = { `decision_approve`, `decision_reject`, `decision_request_changes`, `code_merge`, `intake_convert`（仅 AUTONOMOUS 项目、仅转为已有项目的新需求，01 BR-01-22） }。
- `HumanOnly` = `NonDelegable` ∪ { `interaction_respond`, `plan_confirm`, `report_confirm` }（后两者 D111）：Agent / 机器主体即使经角色持有也一律拒绝（BR-08-05）。00 §6.5 中不以权限码表达的仅人类动作（项目阶段迁移到 DELIVERED/CLOSED/CANCELLED、修改预算上限、登记线下验收、确认机器接入、批准 RULE 记忆）由所属模块以 `ActorKind=HUMAN` 守卫（07/04/06 各自规格），本模块不重复判定。
- 启动校验：`NonDelegable ∪ Delegable ∪ PolicyOnly` 中除 `project_create` 外每个码必须在 Registry 中存在且三集合两两不相交，否则 `Initialize` 失败（防止拼写漂移）。若贡献模块被关闭（`Enabler`），其码缺失只记 WARN 并在播种中跳过（BR-08-03）。

### 3.3 默认角色播种

**清单**（`RoleManifest`，governance 根包常量，`RoleManifestVersion = 1`；展开为显式码表，不在运行时做通配匹配，以保证可重复）。00 §6.2 的 `*_read` 对人类角色展开为全部 `_read`；对 Agent 角色只展开项目内（DataScoped）读权限（本规格提议：Agent 不需要团队级 `intake_read/machine_read/policy_read/audit_read`，最小授权）。

| role_key | 显示名 key | 授予（业务权限码） | 附加底座码 |
|---|---|---|---|
| `team_owner` | `governance.role.team_owner` | §3.2 全部，**除** `production_access`、`agent_spawn`（`PolicyOnly`，D110） | `audit_event_view` `audit_event_export` `access_request_view` `access_request_approve` `role_view` |
| `delivery_lead` | `…delivery_lead` | 全部，除 `machine_revoke` `policy_manage` `takeover` `gateway_manage` `production_access` `project_mode_change` `delegation_grant` `spawn_grant_manage` `agent_spawn`（模式切换、授予编制与授予委托仅限所有者，00 §6.2 / PRD §12.2）；planning：除 `planning_settings_manage` 外全部（D111） | `audit_event_view` `audit_event_export` `access_request_view` |
| `engineer` | `…engineer` | 全部 `_read`、`work_item_execute`、`execution_read/start/stop`、`interaction_respond`、`evidence_submit`、`verification_run`、`decision_request_changes`、`channel_post`、`knowledge_write`；planning：`goal_read` `goal_member_manage`（本人）`plan_submit` `report_read`（本人与所在项目）`report_submit`（D111） | — |
| `agent_executor` | `…agent_executor` | `project_read` `requirement_read` `work_item_read` `delivery_read` `execution_read` `channel_read` `knowledge_read` `work_item_execute` `execution_start` `execution_stop` `evidence_submit` `verification_run` `channel_post`；planning：`goal_read` `plan_submit` `report_submit`（仅本人；不得确认，D111） | — |
| `agent_reviewer` | `…agent_reviewer` | 同上 7 个读 + `agent_read` `verification_run` `channel_post`；planning：`goal_read` `plan_submit` `report_submit`（仅本人，D111） | — |
| `agent_coordinator` | `…agent_coordinator` | `intake_read` `intake_triage` + 7 个读 + `channel_post`；planning：`goal_read` `plan_submit` `report_submit`（仅本人，D111） | — |
| `agent_lead` | `…agent_lead` | 7 个读 + `agent_read` `channel_post`；**不含** `work_item_assign` / `execution_start`（分派经 POLICY `dispatch_auto`，ENSEMBLE 分支经 POLICY `ensemble_branch`，00 §6.2）；planning：`goal_read` `plan_submit` `report_submit`（仅本人，D111） | — |
| `intake_api_client` | `…intake_api_client` | `intake_submit`（仅见本主体提交的来件，01） | — |
| `production_operator` | `…production_operator` | `production_access` | — |
| `delegated_decision_approve` / `delegated_decision_reject` / `delegated_decision_request_changes` / `delegated_code_merge` / `delegated_intake_convert` | `…delegated_carrier` | 各自唯一一个码 | — |

- 后 5 个为**委托载体角色**：`managed=true`，只能由委托流程绑定；IAM 角色页仍可见（建议 IAM 后续加只读标记，非本期）。人工把载体角色直接绑给某人不会产生授权来源以外的效果：`AuthorityChecker` 只认有对应 ACTIVE 委托行的载体匹配（§3.5 S6）。
- `team_owner` 成员：播种时把租户内底座 `Administrator` 角色的现有成员加入 `team_owner`（之后不同步）。

**播种流程**（`seed.ensureTenant(tenant)`，单租户单事务，`dlock governance:seed:{tenant}`）

1. 读 `governance_role_seeds`；对清单中每个 role_key：无行 → 经 IAM `RoleSeeder.EnsureRoleOn(tx, tenant, nameKey, name, desc)`（CR-08-08）创建或按 `name_key` 认领同名角色（同名且非本模块创建 → 不认领，记 `seed_conflict` 并跳过，`160460`）；写行。
2. `to_grant = manifest[role_key] ∩ registry − applied_codes`；逐码 `GrantPermissionOn(tx, role, code)`；`applied_codes ∪= to_grant`；`manifest_version = 当前`。
3. **只增不减**：清单删除的码不自动回收，管理员手工移除的码（在 `applied_codes` 中但 IAM 已无）不再补回；差异显示为「漂移」（GET role-seeds）。理由：回收可能突然断权，补回会推翻管理员决定。
4. `managed=false`（管理员在授权页点「脱离托管」）的角色不再升级。
5. 有变化才追加审计 `governance.role_seed_applied`；无变化不写（幂等）。

**触发**：模块 `Start` 后对全部租户执行一次；`governance.seed_reconcile` 每 1 小时；任一 governance API 首次访问某租户且无种子行时同步执行。底座无「租户已创建」钩子（CR-08-08 可选），最长 1 小时滞后由首次访问兜底。

### 3.3b D112 / D113 权限码登记与默认授予（00 §15.5）

| 权限码 | 注册模块 | DataScoped | NonDelegable | HumanOnly | PolicyOnly | 默认授予 |
|---|---|---|---|---|---|---|
| `andon_pull` | delivery(02) | 是 | — | — | — | 全部成员：`team_owner`、`delivery_lead`、`engineer` 与**全部 Agent 角色**（执行者、评审、协调、主管、测试、验收、助理） |
| `andon_clear` | delivery(02) | 是 | — | ✓ | — | `team_owner`、`delivery_lead`；工作项的 `accountable_principal_id` 隐式持有（S4 关系授权） |
| `exception_decide` | delivery(02/07) | 是 | ✓ | ✓ | — | `team_owner`、`delivery_lead` |
| `plan_approve` | delivery(02) | 是 | — | — | 人：角色；Agent：仅策略 `plan_review` | `team_owner`、`delivery_lead`、`engineer`（条件：风险 ≤ MEDIUM）；Agent 不经角色授予 |
| `acceptance_suite_manage` | execution(03) | 是 | ✓ | ✓ | — | `team_owner`、`delivery_lead` |
| `template_manage` | templates(11) | 否 | — | ✓ | — | `team_owner`、`delivery_lead` |
| `integration_manage` | integration(12) | 否 | ✓ | ✓ | — | `team_owner` |

`HumanOnly` 集合扩为：`NonDelegable` ∪ {`interaction_respond`, `andon_clear`, `acceptance_suite_manage`, `template_manage`}；`NonDelegable` 增加 `exception_decide`、`acceptance_suite_manage`、`integration_manage`；`PolicyOnly` 增加「`plan_approve` 对 AGENT 主体」。播种升级：已有租户在 catalog 升级时按 BR-08-04 追加授予，不回收已有自定义。

### 3.3c D119 权限码登记与默认授予（远程工作区与机器访问）

| 权限码 | 注册模块 | DataScoped | NonDelegable | HumanOnly | PolicyOnly | 默认授予 |
|---|---|---|---|---|---|---|
| `terminal_interact` | execution(03) | 是 | ✓ | ✓ | — | `team_owner`、`delivery_lead`（交互终端，v1.1；另需 step-up `terminal_interactive` 与机器开关，03 BR-03-125） |
| `workspace_file_edit` | execution(03) | 是 | — | ✓ | — | `team_owner`、`delivery_lead`、`engineer`（人工接管后编辑文件，v1.1；另需机器开关，03 BR-03-127） |
| `preview_share_approve` | execution(03) | 是 | ✓ | ✓ | — | `team_owner`、`delivery_lead`（批准甲方分享链接；申请人不得自批，BR-08-66） |
| `machine_access_manage` | fleet(04) | 否 | ✓ | ✓ | — | 仅 `team_owner`（每机远程访问开关，04 A35；`delivery_lead` 的排除清单增加此码） |

查看预览、产物、文件与只读终端**不新增权限码**：沿用项目 `execution_read`（D119：访问预览须有项目上的执行读取权限）；启动预览与申请分享沿用 `execution_start`；接入服务器沿用 `machine_manage`。`HumanOnly` 增加 `terminal_interact`、`workspace_file_edit`、`preview_share_approve`、`machine_access_manage`；`NonDelegable` 增加 `terminal_interact`、`preview_share_approve`、`machine_access_manage`。Agent 角色一律不授予上述 4 码。播种升级按 BR-08-04 追加，不回收。

### 3.3d D120 权限码登记与默认授予（Agent 运行时、锚定批注、团队工具目录）

| 权限码 | 注册模块 | DataScoped | NonDelegable | HumanOnly | PolicyOnly | 默认授予 |
|---|---|---|---|---|---|---|
| `agent_workspace_read` | fleet(05) | 否 | — | ✓ | — | 仅 `team_owner`；Agent 的负责人类（05 `owner_principal_id`）经关系授权隐式持有（同 `andon_clear` 的 S4 关系授权写法，BR-08-69）；`delivery_lead` 的排除清单增加此码。用途：Agent 工作目录 F12 / F13、收件箱 F17 / F19、重置记录 F22、记忆标签（06 A30） |
| `artifact_comment_write` | execution(03) | 是 | — | — | — | `team_owner`、`delivery_lead`、`engineer` 与**全部 Agent 角色**（新建与回复锚定批注，03 BR-03-134；纯人工项目中 Agent 仍被 S2(e) 拒绝） |
| `interaction_request` | execution(03) | 是 | — | — | — | `team_owner`、`delivery_lead`、`engineer` 与**全部 Agent 角色**（同 `andon_pull` 的授予方式）：在自己运行中的执行内发起工具授权 / 输入请求（03 `interaction_requested`，03 BR-03-148）；批准仍需 `interaction_respond`（仅人类）。这是 05 操作清单中 `interaction.request` 的权限码（原型占位「D120 请求权限待定义」由此确定） |
| `tool_catalog_manage` | fleet(05) | 否 | ✓ | ✓ | — | 仅 `team_owner`（`delivery_lead` 的排除清单增加此码）：团队工具目录的条目、版本、授予与凭据（05 T02–T10）；设置 / 轮换凭据另需 step-up `tool_credential_manage`（§6.9）。读取目录沿用 `agent_read` |

`HumanOnly` 增加 `agent_workspace_read`、`tool_catalog_manage`；`NonDelegable` 增加 `tool_catalog_manage`。Agent 完全重置不新增权限码：仍用 `agent_manage`，但要求 HUMAN + step-up `agent_full_reset`（05 BR-05-136，§6.9）。播种升级按 BR-08-04 追加，不回收。

### 3.4 环境维度（生产访问）

- `Environment` 枚举：`DEFAULT`、`PRODUCTION`（CR-08-04）。任何作用于生产环境的动作（如 v1.1 生产部署、生产密钥读取）在 `AuthorityRequest.environment=PRODUCTION` 时**额外**要求 `production_access`。
- 授予只走底座访问申请：申请人（HUMAN）对 `production_operator` 角色发起申请、他人批准（底座禁止自批）、`access_expires_at` 由申请人填写，本规格要求 ≤8 小时（`governance_settings.production_access_max_hours`，底座上限 365 天无法收紧，CR-08-10 请求按角色设上限；在此之前 AuthorityChecker 对 `temporary_role` 来源且 `ends_at − starts_at > max_hours` 的 `production_access` 判拒，reason `PRODUCTION_GRANT_TOO_LONG`）。
- `production_operator` 不得作为长期成员：S2 中 `production_access` 只接受 `temporary_role` 来源的匹配（长期成员或实体绑定来源 → 拒绝，`PRODUCTION_GRANT_NOT_TEMPORARY`）。
- Agent / 机器主体：恒拒（`AGENT_PROHIBITED`）。MVP 无产品功能消费 PRODUCTION 环境，本维度先于功能落地以确保默认拒绝。

### 3.5 AuthorityChecker

**Go 接口**（`internal/governance` 根包冻结，`module.Provide`）

```go
type AuthorityChecker interface {
    // Check 只读，不写库（对齐 02 BR-02-19）。tx 非 nil 时在调用方事务内读取，并对命中的委托行加共享锁，
    // 使并发撤销与本次裁决串行化（BR-08-20）。tx 为 nil 用于预检 / UI 解释。
    Check(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error)
    // ListEligible 返回在 facts 下可做 code 的主体（人类与 Agent，带来源），供 02 N2、通知接收人与收件箱投影。
    ListEligible(ctx context.Context, tenantID, projectID guid.ID, code string, facts *WorkItemFacts) ([]EligiblePrincipal, error)
}
type AuthorityRequest struct {
    TenantID, ProjectID guid.ID          // ProjectID 对团队级码（intake_*）可为 0；intake_convert 委托时为目标项目
    ActorKind   string                   // HUMAN | AGENT | MACHINE | SYSTEM
    PrincipalID guid.ID                  // SYSTEM 时为 0
    SystemJob   string                   // SYSTEM 时必填，须在 SystemActions 登记
    Code        string                   // 00 §6.1 权限码或伪码 project_create
    Resource    ResourceRef              // {Kind: work_item|requirement|delivery|execution|interaction|intake|project|machine|agent|memory, ID}
    Environment string                   // DEFAULT（缺省）| PRODUCTION
    Facts       *WorkItemFacts           // 工作项类动作必填；缺失事实一律按不满足处理（失败即拒绝）
    Spawn       *SpawnRequestFacts       // Code=agent_spawn 时必填（D110）；缺失 → S0 拒绝
}
type SpawnRequestFacts struct { // 05 在建 Agent 事务内填充（D110）
    SupervisorAgentID guid.ID            // 发起的主管 Agent（= PrincipalID 对应的 Agent）
    ProjectIDs []guid.ID                 // 新 Agent 默认参与的项目（≥1）
    Role string                          // EXECUTOR | COORDINATOR
    MachineID guid.ID; Runtime string; Model string; Effort string
    EstimatedCapMicros int64             // 新 Agent 的单 Agent 预算上限申请值（≤ 授权 per_agent_cap_micros）
}
type WorkItemFacts struct { // 02 BR-02-19 输入 + 本规格补充（†，CR-08-06 对 02）
    Type string; Risk string
    CostMicros int64                     // 该工作项全部 DELIVERY 执行的累计成本之和（不含 VERIFICATION 执行），由 02 经 03 EvidenceReader.WorkItemCost 计算
    QualityReworkCount int
    SelfProduced bool                    // 主体是否为任一分支的发起者 / 执行者（02 经 03 Participants 计算）
    AssignmentRole      string           // †ASSIGN 时：EXECUTOR | REVIEWER（02 BR-02-19 填充，供 review_dispatch / dispatch_auto 判定）
    AssigneePrincipalID guid.ID          // †ASSIGN 时：被分派主体
    DispatchedByActor bool               // †主体是否为该工作项 ASSIGN 裁决的作出者（02 经 05 DispatchProvenance.DispatchedBy 计算）
    RequiredVerificationsPassed bool     // 必需验证 ≥1 且全部 PASSED
    RequireHumanDecision bool; Paused bool // 02 的 PauseReason 为模块内枚举，不跨模块传递（00 §3）
    IntakeConvertMode string             // †仅 intake_convert：NEW_REQUIREMENT | APPEND | NEW_PROJECT
    TriageCardAuthorID string            // †仅 intake_convert：所转换分诊卡的起草主体（01 提供），用于 S3(d)
}
type AuthorityDecision struct {
    Allowed bool
    AuthoritySource string               // ROLE:<key> | DELEGATION:<id> | POLICY:<rule_id> | SYSTEM:<job>
    DelegationID guid.ID
    ReasonCode string                    // 见下表
    PolicyRevision int64                 // IAM revision
    Steps []AuthorityStep                // {step, outcome PASS|DENY|SKIP|MATCH|NO_MATCH, detail{}}
}
```

**评估顺序**（任一步 DENY 即终止；全部步骤结果写入 `Steps`）

| 步 | 名称 | 规则 | 拒绝 reason_code |
|---|---|---|---|
| S0 | 默认拒绝与输入 | 初值 `Allowed=false`；未知码、缺租户、工作项类码缺 `Facts` → 拒 | `INVALID_REQUEST` |
| S1 | 主体状态 | HUMAN/AGENT/MACHINE：IAM explain 的 `inactive_membership/inactive_tenant` → 拒；AGENT：05 `AgentDirectory` 状态须 ACTIVE；SYSTEM：`(SystemJob, Code)` 须在 `SystemActions` 登记（各模块启动时登记，governance 校验不含 `NonDelegable`） | `PRINCIPAL_INACTIVE` / `SYSTEM_NOT_REGISTERED` |
| S2 | 不可委托与环境硬约束 | (a) `Code ∈ HumanOnly` 且 ActorKind≠HUMAN → 拒；(b) `Environment=PRODUCTION`：非 HUMAN 拒；HUMAN 须 `production_access` 且来源为 `temporary_role`（§3.4）；(c) `intake_convert` 且 `IntakeConvertMode=NEW_PROJECT` → 视为 `project_create`，按 (a) 处理 | `AGENT_PROHIBITED` / `PRODUCTION_ACCESS_REQUIRED` / `NON_DELEGABLE` |
| S3 | 职责分离 | （`ActorKind=SYSTEM` 时跳过 S3/S4/S6，由 S7 策略规则判定）码 ∈ {`decision_approve`,`decision_reject`,`decision_request_changes`,`code_merge`}：(a) AGENT 且 `SelfProduced` → 拒（任何来源）；(b) HUMAN 且 `SelfProduced` 且码 ∈ {approve, merge} → 设置 `sod_human_self_approval=DENY` 时拒，`ALLOW_WITH_REASON` 时放行并在 detail 标 `sod_override=true`（02 须要求理由 ≥20 字）；(c) AGENT 且 `DispatchedByActor` → 只允许 S6 委托来源（S4 结果中的 ROLE 匹配被忽略）；(d) 码 = `intake_convert` 且主体 = `TriageCardAuthorID` → 拒（不得转换自己起草的分诊卡，复审 #6） | `SELF_REVIEW`（(a)(b)(d)：裁决/合并自己产出的工作、转换自己起草的分诊卡）/ `SEPARATION_OF_DUTIES`（(c) 及其他：如主管 Agent 裁决其派出的工作，detail 给子原因）|
| S4 | IAM 角色授权 | `policyx.WithResourceContext(ctx, {Type, ID, Owner, Attrs})`，Attrs = `risk`、`work_item_type`、`verification`（`ALL_PASSED`/`NOT_PASSED`）、`project_mode`、`environment`（全部服务端事实，供角色权限条件如 `{"version":1,"in":[{"context":"resource.attr.risk"},{"value":["LOW"]}]}` 使用）；调 `ExplainResource`（Resource 为项目时 `ExplainEntity`）。`reason=explicit_deny` → 拒（显式拒绝优先于一切，含委托）。把 matches 分成「载体角色匹配」与「其他匹配」；其他匹配允许 → **允许**，来源 `ROLE:<key>`（§3.5.1） | `EXPLICIT_DENY` |
| S5 | 不可委托门 | `Code ∈ NonDelegable` 且 S4 未允许 → 拒（不进入 S6/S7） | `NON_DELEGABLE` |
| S6 | 委托 | 仅 `Code ∈ Delegable` 且 ActorKind=AGENT。候选 = `governance_delegations` 中 `state=ACTIVE ∧ grantee=主体 ∧ project=ProjectID ∧ Code∈actions ∧ ends_at>now`，且 S4 matches 中存在对应载体角色在本项目的 allow 匹配（证明 IAM 绑定仍在）。对每个候选按序校验（**适用范围**：码为工作项类 `decision_*` / `code_merge` 时校验 ①–⑨ 与 ⑪；码为 `intake_convert` 时只校验 ⑦⑧⑩，其余条件对来件无意义，复审 #6）：①`Type∈work_item_types`（空=全部）；②`Risk ≤ max_risk`（LOW<MEDIUM；HIGH 永不满足）；③`CostMicros ≤ cost_cap_micros`；④`RequiredVerificationsPassed`；⑤`QualityReworkCount < max_rework`；⑥`!RequireHumanDecision`；⑦无覆盖该工作项或项目的 `hold_delegations=true` 未关闭 incident；⑧07 `ProjectGate.CanDelegatedDecide(project)`；⑨`code_merge` 时 `Risk=LOW`；⑩`intake_convert` 时项目 AUTONOMOUS 且 `IntakeConvertMode=NEW_REQUIREMENT`；⑪工作项当前 claim 的 SENSITIVE_PATH / LOW_SELF_CONFIDENCE 评估回执已落库（`governance_claim_evaluations`，§7.3；未落库 = 评估仍在途，防止委托 APPROVE 抢在升级判定之前）。首个全部通过者胜（按 `ends_at` 升序、`id` 升序）；`tx≠nil` 时对其 `SELECT … FOR SHARE`（PG / MySQL 8 语法 `FOR SHARE`；SQLite 无行锁，依赖单写者，见 BR-08-20）并在锁后复核 `state=ACTIVE ∧ ends_at>now`。来源 `DELEGATION:<id>` | 未通过的首个条件：`DELEGATION_RISK`/`_COST`/`_VERIFICATION`/`_REWORK`/`_TYPE`/`_HUMAN_REQUIRED`/`ESCALATION_HOLD`/`PROJECT_GATE`/`_MERGE_RISK`/`_MODE`/`ESCALATION_PENDING`；无候选 `NO_DELEGATION` |
| S7 | 策略规则 | `PolicyRegistry` 中声明覆盖该码的规则依次 `Evaluate(ctx, req)`；首个 true → 允许，来源 `POLICY:<rule_id>`（如 `POLICY:dispatch_auto`；调用方把具体提案 id 写入裁决 `detail.dispatch_proposal_id`，不写进来源串，复审 #26）。登记时校验：规则码 ∉ `NonDelegable` 且 ∉ {decision_*} | — |
| S8 | 默认拒绝 | 以上均未允许 | `NO_AUTHORITY`；若命中覆盖该码的规则提供了 `Explain`，用其返回的 reason_code（如 `SPAWN_QUOTA_EXCEEDED`） |

**`agent_spawn` 的评估路径（D110）**：S0 要求 `Spawn` 事实；S1 主体须为 ACTIVE AGENT；S2 不适用（非 HumanOnly）；S3 跳过；S4 只认显式拒绝（`PolicyOnly` 码的角色允许不计入）；S5 不适用；S6 跳过（不可委托给他人建 Agent）；S7 规则 `spawn_grant`（本模块贡献）：主体 Agent 持 `LEAD` 角色 ∧ 存在 `effective_state=ACTIVE` 的编制授权 `grantee_agent_id=主体` ∧ `Spawn.ProjectIDs ⊆ project_ids`（授权为团队级时任意项目）∧ `Role ∈ allowed_roles` ∧ 机器 / 运行时·模型 / effort 在允许清单内 ∧ `active_count < max_active_agents` ∧ `spawned_today < max_spawns_per_day` ∧ `spent_micros < budget_cap_micros` ∧ `EstimatedCapMicros ≤ per_agent_cap_micros`。Check 只读；真正的配额占用在 05 事务内调用 `SpawnGrantUsage.Reserve`（行锁 + CAS 复验，BR-08-53），Check 通过而 Reserve 失败时以 Reserve 的错误为准。

- 理由码到 HTTP：调用方按自身段位返回（02：403 `120403` + `data.reason_code`；04：`140403`；本模块接口：`160403`）。

**`AuthorityReasonCode` 规范清单**（复审 #25；全系统唯一来源，模块不得自造未登记的码；旧名 `NOT_DELEGABLE`→`NON_DELEGABLE`、`PERMISSION_DENIED`→`MISSING_PERMISSION`、`STEP_UP_REQUIRED`→`REAUTH_REQUIRED`）：

| 码 | 产生方 | 含义 |
|---|---|---|
| `INVALID_REQUEST` | 08 S0 | 未知权限码、缺租户、工作项类码缺事实 |
| `PRINCIPAL_INACTIVE` / `SYSTEM_NOT_REGISTERED` | 08 S1 | 主体失效 / 系统动作未登记 |
| `AGENT_PROHIBITED` | 08 S2 | 非人类主体执行仅限人类的动作（权限判定路径） |
| `HUMAN_ONLY` | 01 / 03 / 06 / 07（模块守卫） | 00 §6.5 中非权限码的人类专属动作（如回应工具授权、批准 RULE、登记线下验收） |
| `PRODUCTION_ACCESS_REQUIRED` | 08 S2 | 生产环境需临时授权 |
| `NON_DELEGABLE` | 08 S2 / S5 | 不可委托动作 |
| `SELF_REVIEW` | 08 S3 / 03 / 06 | 裁决、验证、审批自己产出的工作或记忆；转换自己起草的分诊卡 |
| `SEPARATION_OF_DUTIES` | 08 S3 | 其他职责分离（如主管 Agent 裁决其派出的工作；子原因放 detail） |
| `EXPLICIT_DENY` | 08 S4 | IAM 显式拒绝 |
| `DELEGATION_RISK` / `DELEGATION_COST` / `DELEGATION_VERIFICATION` / `DELEGATION_REWORK` / `DELEGATION_TYPE` / `DELEGATION_HUMAN_REQUIRED` / `DELEGATION_MERGE_RISK` / `DELEGATION_MODE` | 08 S6 | 委托条件未满足（`DELEGATION_CONDITION` 为模块无法细分时的通用码） |
| `ESCALATION_HOLD` / `ESCALATION_PENDING` / `PROJECT_GATE` / `NO_DELEGATION` | 08 S6 | 升级冻结 / 升级评估在途 / 项目闸门 / 无可用委托 |
| `NO_AUTHORITY` | 08 S8 | 无任何授权来源；`data.missing_permission` 给出所缺权限码 |
| `MISSING_PERMISSION` | 各模块路由守卫 | 路由层粗粒度权限缺失（附 `data.missing_permission`） |
| `REAUTH_REQUIRED` | 各模块 | 需要 step-up 或凭据已过期 |
| `FULL_AUTO_FORBIDDEN` | 03 / 05 | 全自动授权被策略禁止 |
| `NOT_LEASE_HOLDER` | 02 | 非当前租约持有者 |
| `AGENT_EXECUTOR` | 02 | 执行者 Agent 不得裁决 |
| `ESCALATED_HUMAN_ONLY` | 02 | 已升级的工作项只能由人裁决 |
| `NOT_OWNER` | 03 / 05 / 10 | 非资源所有者（如他人的执行、Agent） |
| `NOT_MEMBER` | 05 | 非频道 / 项目成员 |
| `AGENT_NOT_DRAFTER` | 07 | Agent 不是该交付的指定起草者 |
| `SPAWN_GRANT_MISSING` / `SPAWN_GRANT_SCOPE` / `SPAWN_QUOTA_EXCEEDED` / `SPAWN_DAILY_LIMIT` / `SPAWN_BUDGET_EXHAUSTED` / `SPAWN_NOT_LEAD` | 08 S7（规则 `spawn_grant` 的 Explain）/ `SpawnGrantUsage.Reserve` | 无有效编制授权 / 角色·机器·模型·effort·项目超出范围 / 同时在岗数已满 / 今日新建数已满 / 授权预算已耗尽 / 发起者不是主管 Agent（D110） |
| `ACCOUNTABLE_HUMAN_MISSING` | 08 S2(d) | 需要负责人的码，但工作项未指定负责人或负责人不是 ACTIVE 人类成员（D112） |
| `HUMAN_ONLY_MODE` | 08 S2(e)（05 使用） | 项目为纯人工模式，Agent 只能读与起草（D112） |
| `SAME_MODEL_FAMILY` | 08 S3(f) / 策略 `internal_qa`（02 / 05 使用） | 评审、验收或计划批准主体与执行者 / 计划作者同模型家族（D113） |
| `NO_INTERNAL_QA_AGENT` | 05 IQ3 | 无合格 TESTER / ACCEPTOR；不以 403 返回，仅作 02 `MANDATORY_HUMAN_GATE` 升级的 detail.reason（D113） |
| `SPAWNED_NOT_ADOPTED` | 08 BR-08-09 | AI 创建且未被人收编的 Agent 不能成为任何委托的受托人（D110） |
| `AGENT_CANNOT_ACCEPT` | 12（BR-12-34，delivery 段 `120403` 返回） | Agent（含持 `TESTER` 角色者）不得执行映射到 `ACCEPTED` 的流转（含关闭缺陷）；验证角色 = 持 `TESTER` 的人类成员或工作项负责人（CR-12-09） |
| `MACHINE_ACCESS_DISABLED` | 03 / 04（D119） | 机器的远程访问开关（远程预览 / 远程终端 / 人工接管后编辑文件）关闭 |
| `PORT_NOT_OWNED` | 03 / 04（D119） | 请求预览的端口不属于该执行（或评审预览）的进程树，或不在回环地址 |
| `PATH_OUTSIDE_WORKSPACE` | 03 / 04（D119） | 文件路径越出执行工作区或指向机器程序目录 |
| `TOOL_NOT_GRANTED` | 05（D120） | 团队工具目录条目未授予该 Agent / 其角色 / 该项目，或授予已撤销（MCP 中继与工具集解析） |
- 失败即拒：IAM、05、07 依赖不可用 → 返回 error（调用方 503，本模块 `160461`），绝不放行（对齐 02 BR-02-42）。
- `PolicyRegistry` 初始规则另含本模块贡献的 `spawn_grant`（`agent_spawn`，D110，评估见上）。
- `PolicyRegistry` 初始规则：`dispatch_auto`（05 贡献：主体为项目 LEAD Agent、07 `DispatchPolicy=AUTO`、`CanAutoDispatch` 为真、`Risk≠HIGH`），`ensemble_branch`（05：`execution_start`，ENSEMBLE 并行上限内），`review_dispatch`（05：`work_item_assign`，仅 `role=REVIEWER`、项目 AUTONOMOUS、受派 Agent 持对应有效委托；复审 #5）。另：05 启动时以 `SystemActions.Register("fleet.review_dispatch", "work_item_assign")` 登记评审派单任务，否则 S1 返回 `SYSTEM_NOT_REGISTERED`。

**本模块提供的其他接口**（根包冻结，`module.Provide`；签名为准，使用方规格须一致）

```go
// 07 读取项目有效委托（AUTONOMOUS 前置条件 P1、项目页摘要）
type DelegationReader interface {
    ActiveForProject(ctx context.Context, tenantID, projectID guid.ID) ([]DelegationView, error) // state=ACTIVE ∧ ends_at>now，按 ends_at 升序
}
// 07 读取项目生效的升级规则种类（AUTONOMOUS 前置条件 P2）
type EscalationRuleReader interface {
    EffectiveKinds(ctx context.Context, tenantID, projectID guid.ID) ([]string, error) // EscalationRuleKind，项目覆盖 ∪ 租户默认，enabled=true
}
// 模块在 InjectServices 中贡献策略规则（S7）；登记时校验码 ∉ NonDelegable 且 ∉ decision_*，重复 ID 启动失败
type PolicyRegistry interface {
    Register(rule PolicyRule) error
}
type PolicyRule struct {
    ID       string   // 全局唯一，如 "dispatch_auto"、"ensemble_branch"
    Codes    []string // 覆盖的权限码
    Evaluate func(ctx context.Context, req AuthorityRequest) (bool, error) // 只读；error = 失败即拒
    Explain  func(ctx context.Context, req AuthorityRequest) string        // 可选：不匹配时给出 AuthorityReasonCode（S8 使用）
}
// 模块登记系统作业可执行的码（S1 SYSTEM 主体校验）；码 ∈ NonDelegable 时登记失败
type SystemActions interface {
    Register(job, code string) error // job 形如 "verification"、"delivery.auto_retry"
}
// 05 退役 / 强制退役 Agent 时在其事务内撤销该 Agent 的全部委托
type DelegationRevoker interface {
    RevokeAllForOn(ctx context.Context, tx *gorm.DB, tenantID, granteePrincipalID guid.ID, reason string) ([]guid.ID, error)
}
// D110：05 读取主管 Agent 的有效编制授权（建 Agent 前预检、UI 展示）；tx 可为 nil
type SpawnGrantReader interface {
    Active(ctx context.Context, tx *gorm.DB, tenantID, supervisorAgentID guid.ID) (SpawnGrantView, error) // 无 → ErrNoSpawnGrant（reason SPAWN_GRANT_MISSING）
}
type SpawnGrantView struct {
    GrantID guid.ID; State, EffectiveState string; ApprovalMode string // 已按项目模式折算的 REQUIRE_APPROVAL | AUTO
    ProjectIDs []guid.ID; AllowedRoles []string; AllowedMachineIDs []guid.ID; AllowedModels []RuntimeModel; MaxEffort string
    MaxActiveAgents, ActiveCount, MaxSpawnsPerDay, SpawnedToday int
    BudgetCapMicros, SpentMicros, PerAgentCapMicros int64
    AgentTTLHours, IdleRetireHours int; EndsAt time.Time; Version int
}
// D110：05 在创建 / 退役 / 收编 Agent 的同一事务内占用与释放配额（行锁 + 版本 CAS，BR-08-53）
type SpawnGrantUsage interface {
    Reserve(ctx context.Context, tx *gorm.DB, grantID, agentID guid.ID) error // 幂等：同 agentID 重复调用不重复计数；失败返回 160471 / 160472（带 reason_code）
    Release(ctx context.Context, tx *gorm.DB, grantID, agentID guid.ID, reason string) error // 幂等；reason ∈ RETIRED | ADOPTED | GRANT_ENDED
}
```

本模块另实现 09 定义的 `PreferenceResolver`：`Channels(ctx, tenantID, principalID guid.ID, notificationType string, severity string) (Channels, error)`，语义见 §9.1 与 `governance_notification_preferences`（§3.8.10）。各模块须在规格中列出其 `SystemActions` 登记（如 02 `SYSTEM:verification`、`SYSTEM:lease_reclaim`；07 `SYSTEM:budget`）。

#### 3.5.1 `authority_source` 的 role key

种子角色 → `ROLE:<role_key>`；底座 `Administrator`（经 `tenant_administer` 的 `administrator_grant`）→ `ROLE:tenant_administrator`；其他自定义角色 → `ROLE:custom.<role_id>`（CR-08-02）。多角色同时允许时取优先级 `team_owner > delivery_lead > tenant_administrator > engineer > agent_* > custom.<最小 role_id>`。

**`EXTERNAL:<provider>:<ref>`**（v2.0，CR-12-07，00 §6.3）：外部系统裁决的只读镜像（如 12 BR-12-28 的 `EXTERNAL:YUNXIAO_FLOW:<pipeline>`），只由写入外部镜像记录的模块赋值，`AuthorityChecker` **从不**返回该来源；它不能满足 L3，也不能作为任何 `decision_*` / `delivery_send` 的授权来源。

#### 3.5.2 与路由守卫的组合

| 端点类型 | 路由守卫（`authz.Register*`） | 服务内 |
|---|---|---|
| 多动作入口（02 `POST /work-items/{id}/decisions`、A1 分派、03 合并） | 粗粒度读码（如 `work_item_read`）：证明成员身份与项目可见 | `AuthorityChecker.Check(tx, 精确码)`，结果写入 Decision 与审计 |
| 单一不可委托动作（`scope_change`、`delivery_send`、`machine_revoke`、模式切换、委托、接管） | 精确码 | 仍调用 Check：执行 Agent 硬拒、职责分离、环境与 `authority_source` 计算 |
| 无授权语义的只读 / 自助 | 粗码或 `RegisterPrincipalTenantMember` | 不调用 |

规则：凡产生 Decision 或在审计 detail 中写 `authority_source` 的写操作必须调用 Check（架构测试：对 governance 以外模块中出现 `authority_source` 赋值的函数，检查同函数内调用 `AuthorityChecker.Check`，本规格提议）。

#### 3.5.3 解释输出（UI）

`POST /authority-explanations` 返回 `AuthorityDecision` + 渲染字段：`actor_label`（「你以 交付负责人 身份」）、`source_label`（「来源：角色 delivery_lead」/「委托 #8812，剩余 2 天 3 小时」）、`steps[]`（i18n key `governance.authority.step.<step>.<outcome>` + 参数）、`missing_permission?`、`fix_hint?`（如「请所有者授予委托」「需要申请生产访问」）。前端组件 `AuthorityNote`（00 §10）使用此输出。

### 3.5b D112 / D113 判定规则增补

- **Facts 增补**（`WorkItemFacts`）：`AccountablePrincipalID guid.ID`（02 填充）、`PlanAuthorID guid.ID`（计划作者主体，02 填充）、`ExecutorModelFamily string`、`ActorModelFamily string`（05 `AgentDirectory` 的 `model_family`，如 `anthropic` / `openai` / `google` / `open`）。
- **S2(d) 负责人存在**：码 ∈ {`decision_*`, `code_merge`, `plan_approve`, `exception_decide`, `delivery_send`} 且 `AccountablePrincipalID` 为空或不是 ACTIVE 人类成员 → 拒 `ACCOUNTABLE_HUMAN_MISSING`。
- **S3(f) 计划审批职责分离**：码 = `plan_approve`：主体 = `PlanAuthorID` → `SELF_REVIEW`；主体为主管 Agent 且 `DispatchedByActor=true` → `SEPARATION_OF_DUTIES`（detail `LEAD_DISPATCHED`）；主体为 AGENT 且 `ActorModelFamily == ExecutorModelFamily` → `SAME_MODEL_FAMILY`。
- **S7 策略 `plan_review`**（08 贡献）：允许 AGENT 主体对 `plan_approve`，当且仅当：角色含 REVIEWER、状态 ACTIVE、跨模型家族（S3(f) 已校验）、`Risk ≤ MEDIUM`、未处于升级冻结；HIGH 风险计划只允许人类批准。
- **S7 策略 `internal_qa`**（08 贡献）：允许角色含 TESTER / ACCEPTOR 的 AGENT 对 `verification_run`（内部质量门阶段 AGENT_TEST / AGENT_ACCEPTANCE），要求与执行者不同主体、ACCEPTOR 与执行者跨模型家族（不可用时由 05 降级为不同模型 + 不同主体并在 detail 标 `FAMILY_FALLBACK`）。
- **策略命名**：策略规则名为 `plan_review`，它授权的权限码是 `plan_approve`；05 / 02 文中“08 策略规则 `plan_approve`”均指本策略（`policy_id=plan_review`，`authority_source=POLICY:plan_review`）。不另设名为 `plan_approve` 的策略。
- **新原因码**（并入 §3.5 规范清单）：`ACCOUNTABLE_HUMAN_MISSING`（08，负责人缺失或非 ACTIVE 人类）、`SAME_MODEL_FAMILY`（08，评审 / 验收 / 计划批准主体与执行者或作者同模型家族；02 / 05 共用）、`HUMAN_ONLY_MODE`（08 登记、05 使用，项目 `team_mode=HUMAN_ONLY` 时 Agent 主体对除只读与起草外的任何码请求 → 403）、`NO_INTERNAL_QA_AGENT`（08 登记、05 使用，无合格 TESTER / ACCEPTOR 候选；不作为 403 返回，只作为 02 升级 `MANDATORY_HUMAN_GATE` 的 detail.reason 与收件箱项说明）。
- **S2(e) 纯人工模式**：项目 `team_mode=HUMAN_ONLY` 且主体为 AGENT，码 ∉ {只读码（`*_read`）、`andon_pull`} → 拒 `HUMAN_ONLY_MODE`；助理起草（报告草稿、记忆提议）不经 AuthorityChecker，由 05 / 06 / 10 以草稿态落库并需人确认（在 S7 策略之前判定，策略不可放行）。

### 3.5c D120 操作清单的能力判定映射（`CAPABILITY_NOT_AUTHORIZED`）

- **规则**：Agent 调用 05 操作清单（05 BR-05-130）内的操作被拒时，响应仍为所属模块段 `xx0403` + `data.reason_code`（取值仍为 §3.5 规范清单，不变），另带 `data.result`（结果类别）与 `data.operation`（清单操作名）；05 M03 能力视图中每个操作的 `status` 由本表**唯一**决定。M03 以 `AuthorityChecker.Check(ctx, nil, req)` 的只读路径判定（tx 为 nil，不加锁、不写库、不占配额）。
- **`via` 取值**（清单 `permission.via`）：`ROLE`（角色授予）、`DELEGATION`（委托，S6）、`POLICY`（策略规则，S7，附 `policy_rule`）、`RELATION`（关系授权，如本项目主管 Agent、Agent 的负责人类，附 `relation`）、`SELF`（只要求调用者是该 Agent 本人的服务账号；`permission.code` 为空，不经 `AuthorityChecker`，如收件箱、提醒、能力视图，BR-08-69）。
- 未在本表列出的原因码不可能由清单内操作的权限判定产生；若出现，M03 记为 `CAPABILITY_NOT_AUTHORIZED` 并告警（`governance_unmapped_reason_total`）。

| reason_code | M03 `status` | 403 / 422 响应的 `data.result` | 建议 `meta.next.reason`（05 BR-05-131） |
|---|---|---|---|
| `INVALID_REQUEST` / `PRINCIPAL_INACTIVE` / `SYSTEM_NOT_REGISTERED` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `ESCALATE_TO_OWNER` |
| `MISSING_PERMISSION` / `NO_AUTHORITY` / `EXPLICIT_DENY` / `NOT_MEMBER` / `NOT_OWNER` / `TOOL_NOT_GRANTED` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `ESCALATE_TO_OWNER` |
| `NON_DELEGABLE` / `HUMAN_ONLY` / `AGENT_PROHIBITED` / `HUMAN_ONLY_MODE` / `AGENT_CANNOT_ACCEPT` / `ESCALATED_HUMAN_ONLY` / `AGENT_EXECUTOR` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `WAIT_FOR_HUMAN` |
| `SELF_REVIEW` / `SEPARATION_OF_DUTIES` / `SAME_MODEL_FAMILY` / `ACCOUNTABLE_HUMAN_MISSING` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `WAIT_FOR_HUMAN` |
| `DELEGATION_RISK` / `DELEGATION_COST` / `DELEGATION_VERIFICATION` / `DELEGATION_REWORK` / `DELEGATION_TYPE` / `DELEGATION_HUMAN_REQUIRED` / `DELEGATION_MERGE_RISK` / `DELEGATION_MODE` / `DELEGATION_CONDITION` / `NO_DELEGATION` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `WAIT_FOR_HUMAN` |
| `ESCALATION_HOLD` / `ESCALATION_PENDING` / `PROJECT_GATE` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `WAIT_FOR_HUMAN` |
| `SPAWN_GRANT_MISSING` / `SPAWN_GRANT_SCOPE` / `SPAWN_NOT_LEAD` / `SPAWNED_NOT_ADOPTED` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED`（05 以 422 142031–142038 返回时同样带此值） | `ESCALATE_TO_OWNER` |
| `SPAWN_QUOTA_EXCEEDED` / `SPAWN_DAILY_LIMIT` / `SPAWN_BUDGET_EXHAUSTED` | `QUOTA_EXHAUSTED` | `QUOTA_EXHAUSTED`（05 以 422 142031–142038 返回） | `ESCALATE_TO_OWNER` |
| `REAUTH_REQUIRED` / `FULL_AUTO_FORBIDDEN` / `PRODUCTION_ACCESS_REQUIRED` / `PRODUCTION_GRANT_TOO_LONG` / `PRODUCTION_GRANT_NOT_TEMPORARY` | `CAPABILITY_NOT_AUTHORIZED` | `CAPABILITY_NOT_AUTHORIZED` | `WAIT_FOR_HUMAN` |
| `NOT_LEASE_HOLDER` / `AGENT_NOT_DRAFTER` / `MACHINE_ACCESS_DISABLED` / `PORT_NOT_OWNED` / `PATH_OUTSIDE_WORKSPACE` | `NOT_APPLICABLE` | `CAPABILITY_NOT_AUTHORIZED` | `NONE_AVAILABLE` |
| `NO_INTERNAL_QA_AGENT` | —（不作为拒绝返回，§3.5b） | — | — |

`NOT_APPLICABLE` 表示「当前对象 / 环境不满足操作前提」（不是该 Agent 的租约、不是指定起草者、机器关闭了远程访问等），与授权缺失区分；M03 对未给出具体对象的操作（如未指定工作项的认领）不判定对象类前提，返回其余检查的结果。05 的 M03 其余状态 `AVAILABLE`（判定允许）不在本表。委托缺失的原因码为 `NO_AUTHORITY`（S8，附 `data.missing_permission`），有委托但条件不满足时为 `DELEGATION_*`。

### 3.6 委托在 IAM 上的落地

| 步骤 | 复用 | 新增 |
|---|---|---|
| 授权载体 | 每个委托动作一条实体角色绑定：role=`delegated_<code>`、principal=受托 Agent、scope=项目 entity、effect=allow、`expires_at=ends_at`（底座查询时过滤到期，revision 自增使缓存失效） | `governance_delegations` 行记录条件、窗口、计数 |
| 越权防护 | 绑定时底座 `requireGrantableRole`：授予者须持有载体角色全部权限 | 另校验授予者经 **ROLE 来源**持有每个委托码（不可转委托，`160403(GRANTOR_LACKS_AUTHORITY)`） |
| 事务 | IAM `EntityRoleBindingStore.PutOn/RemoveOn(tx, …)`（CR-08-08，tx 绑定版本）与委托行同事务 | — |
| 撤销 | `RemoveOn` 删除绑定 + revision 自增 | 行置 REVOKED |
| 到期 | 底座按 `expires_at` 即时失效 | 任务把行置 EXPIRED 并删除残留绑定 |
| 使用计数 | — | 消费 `decision.made`（`authority_source=DELEGATION:*`）投影到 `governance_delegation_uses`（02：Check 不写库） |
| 评审 | 访问评审可移除绑定 | 对账任务发现绑定缺失 → REVOKED(`BINDING_REMOVED`) |

### 3.7 逻辑类型与三方言映射

| 逻辑类型 | PostgreSQL | MySQL 8.0.16+ | SQLite |
|---|---|---|---|
| `ID` | `BIGINT` | `BIGINT` | `INTEGER` |
| `ENUM` | `VARCHAR(32)` + CHECK | `VARCHAR(32)` + CHECK | `TEXT` + CHECK |
| `STR(n)` | `VARCHAR(n)` | `VARCHAR(n)`（utf8mb4） | `TEXT` + `CHECK(length(x)<=n)` |
| `TEXT` | `TEXT` | `TEXT` | `TEXT` |
| `INT` / `BIGINT` | `INTEGER` / `BIGINT` | `INT` / `BIGINT` | `INTEGER` |
| `BOOL` | `BOOLEAN` | `TINYINT(1)` + CHECK(0,1) | `INTEGER` + CHECK(0,1) |
| `TS` | `TIMESTAMPTZ(3)` | `DATETIME(3)` | `DATETIME`（UTC 文本） |
| `JSON` | `JSONB` | `JSON`（无库级默认，应用写入） | `TEXT` + `CHECK(json_valid(x))` |

通用：所有表含 `id ID PK`（雪花）、`tenant_id ID NOT NULL`；可变表含 00 §3 审计列与 `version INT NOT NULL DEFAULT 1`（HTTP 以 `ETag`/`If-Match` 承载，与 02/07 一致）。「仅在某状态唯一」统一用**可空键列 + 普通唯一索引**实现（三方言 UNIQUE 均允许多个 NULL），不用部分索引。索引名 `{{.Prefix}}governance_<表>_<列>`。

### 3.8 数据表

#### 3.8.1 `governance_role_seeds`

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| role_key | STR(48) | N | | §3.3 键 |
| role_id | ID | N | | IAM 角色 id |
| manifest_version | INT | N | | 最后应用的清单版本 |
| applied_codes | JSON | N | | 曾经由播种授予过的码（字符串数组，有序去重） |
| skipped_codes | JSON | N | | 因模块未启用而跳过的码 |
| managed | BOOL | N | true | false = 脱离托管 |
| seed_conflict | BOOL | N | false | 同名非托管角色已存在 |

唯一：`(tenant_id, role_key)`、`(tenant_id, role_id)`。

#### 3.8.2 `governance_settings`（每租户 1 行）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| delegation_max_days | INT | N | 7 | 1–30 |
| delegation_default_days | INT | N | 3 | 1–max |
| delegation_max_cost_cap_micros | BIGINT | N | 50000000 | 单项成本上限的上限（$50） |
| delegation_default_cost_cap_micros | BIGINT | N | 5000000 | $5 |
| sod_human_self_approval | ENUM | N | `DENY` | `DENY` / `ALLOW_WITH_REASON`（单人团队用；改为 ALLOW 需 step-up） |
| default_budget_notify_pct | INT | N | 80 | 50–95；07 新建项目读取为 `budget_notify_pct` 默认值（100% 暂停不可调） |
| production_access_max_hours | INT | N | 8 | 1–24 |
| inbox_review_routing | ENUM | N | `REVIEWER_ELSE_ELIGIBLE` | 或 `ALL_ELIGIBLE`（§6.10） |

唯一：`(tenant_id)`。

#### 3.8.3 `governance_delegations`

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| project_id | ID | N | | 项目 entity |
| grantee_principal_id | ID | N | | Agent 服务账号 |
| grantee_agent_id | ID | N | | 05 Agent |
| grantor_principal_id | ID | N | | HUMAN |
| actions | JSON | N | | `Delegable` 子集，1–5 项 |
| work_item_types | JSON | N | | `WorkItemType` 子集；`[]`=全部 |
| max_risk | ENUM | N | `LOW` | `LOW` / `MEDIUM` |
| cost_cap_micros | BIGINT | N | | 1..settings 上限 |
| max_rework | INT | N | 3 | 1–3；条件为 `quality_rework_count < max_rework` |
| starts_at | TS | N | | = 授予时刻（MVP 不支持预约，US-08-17） |
| ends_at | TS | N | | starts_at+15min ≤ ends_at ≤ starts_at+max_days |
| reason | STR(2000) | N | | ≥10 字 |
| state | ENUM `DelegationState` | N | `ACTIVE` | ACTIVE / EXPIRED / REVOKED |
| end_reason | ENUM | Y | | `WINDOW_ENDED` `MANUAL` `TAKEOVER` `MODE_CHANGED` `PROJECT_CLOSED` `GRANTEE_INACTIVE` `GRANTOR_LOST_AUTHORITY` `BINDING_REMOVED` `REPLACED`（CR-08-04 `DelegationEndReason`） |
| ended_at / ended_by | TS / ID | Y | | ended_by 空 = 系统 |
| end_note | STR(2000) | Y | | 手动撤销理由 |
| iam_bindings | JSON | N | | `[{role_key, role_id}]` |
| used_count | INT | N | 0 | 投影计数（§3.6） |
| last_used_at | TS | Y | | |
| expiring_notified_at | TS | Y | | 72h 提醒已发 |
| active_key | STR(48) | Y | | `<grantee>:<project>`，仅 ACTIVE 时非空 |
| replaces_delegation_id | ID | Y | | 替换链 |

唯一：`(tenant_id, active_key)`（每受托人每项目至多 1 条 ACTIVE，BR-08-12）。索引：`(tenant_id, state, ends_at)`（到期任务）、`(tenant_id, project_id, state)`、`(tenant_id, grantee_principal_id, state)`。

#### 3.8.4 `governance_delegation_uses`（只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| delegation_id | ID | N | |
| project_id | ID | N | |
| decision_id | ID | N | 02 裁决 id；唯一 `(tenant_id, decision_id)` 保证投影幂等 |
| code | STR(64) | N | |
| resource_kind / resource_id | ENUM / ID | N | |
| decision_action | ENUM `DecisionAction` | N | |
| used_at | TS | N | 裁决提交时间（事件 occurred_at） |

索引 `(tenant_id, delegation_id, used_at)`。

#### 3.8.5 `governance_escalation_rules`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id | ID | Y | | NULL = 团队默认；非空 = 项目生效规则（07 `project.created` 时由默认复制，BR-07-03） |
| kind | ENUM `EscalationRuleKind` | N | | §3.9.2 |
| enabled | BOOL | N | true | 硬性规则在 AUTONOMOUS 项目不可关（BR-08-30） |
| params | JSON | N | | 按 kind 的 Schema |
| actions_autonomous | JSON | N | | `EscalationAction` 数组 |
| actions_hybrid | JSON | N | | 同上（缺省仅 `NOTIFY` / `HOLD_DELEGATIONS`） |
| scope_key | STR(48) | N | | `<project_id or 0>:<kind>`，唯一 |

唯一：`(tenant_id, scope_key)`。

#### 3.8.6 `governance_escalation_incidents`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| project_id | ID | N | | |
| rule_id | ID | N | | |
| rule_kind | ENUM | N | | |
| subject_kind | ENUM | N | | `PROJECT` / `WORK_ITEM` / `DELIVERY` / `CLAIM` |
| subject_id | ID | N | | |
| severity | ENUM | N | | `WARNING` / `CRITICAL` |
| state | ENUM `EscalationIncidentState` | N | `OPEN` | OPEN / ACKNOWLEDGED / RESOLVED / DISMISSED |
| open_key | STR(160) | Y | | 开启期间去重键（§4.2），关闭置 NULL |
| once_key | STR(160) | Y | | 一次性去重键（预算阈值、拒收），永不清空 |
| trigger_event_id | ID | N | | 首次触发事件 |
| trigger_event_type | STR(64) | N | | |
| facts | JSON | N | | 触发事实（§3.9.3） |
| actions_applied | JSON | N | | `[{action, status: APPLIED|FAILED|SKIPPED, error_code?}]` |
| hold_delegations | BOOL | N | false | 未关闭时 S6 ⑦ 拒绝 |
| occurrence_count | INT | N | 1 | |
| last_triggered_at / last_notified_at | TS | N | | |
| acknowledged_by / acknowledged_at | ID / TS | Y | | |
| resolved_by / resolved_at | ID / TS | Y | | resolved_by 空 = 系统 |
| resolution | ENUM | Y | | `RESUMED` `AUTO_CLEARED` `DISMISSED` `SUPERSEDED` |
| resolution_note | STR(2000) | Y | | DISMISS 必填 |

唯一：`(tenant_id, open_key)`、`(tenant_id, once_key)`。索引 `(tenant_id, project_id, state)`、`(tenant_id, subject_kind, subject_id, state)`。

#### 3.8.7 `governance_takeovers`（只追加）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| project_id | ID | N | |
| actor_principal_id | ID | N | HUMAN |
| reason | STR(2000) | N | ≥10 字 |
| previous_mode | ENUM `TeamMode` | N | |
| revoked_delegation_ids | JSON | N | ID 字符串数组 |
| mode_change_id | ID | Y | 07 返回（已是 HYBRID 时空） |
| created_at | TS | N | |

#### 3.8.8 `governance_inbox_items`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| recipient_principal_id | ID | N | | 仅 HUMAN |
| kind | ENUM `InboxItemKind` | N | | §6.10 |
| subkind | STR(32) | Y | | 如 `TOOL_PERMISSION`、`ANSWERED` |
| project_id | ID | Y | | 团队级（来件）为空 |
| source_kind / source_id | ENUM / ID | N | | 跳转与动作目标 |
| source_key | STR(160) | N | | 轮次级键，如 `work_item:<id>:claim:<claim_id>` |
| priority | ENUM | N | `NORMAL` | `NORMAL` / `HIGH` / `CRITICAL` |
| title_key | STR(128) | N | | i18n |
| title_params | JSON | N | | 仅标识与短字段（编号、标题 ≤200、计数），不含正文 |
| state | ENUM `InboxItemState` | N | `OPEN` | OPEN / RESOLVED / WITHDRAWN / EXPIRED |
| resolution | STR(32) | Y | | `ACTED_BY_ME` `ACTED_BY_OTHER` `ACKNOWLEDGED` `SOURCE_CLOSED` |
| resolved_by / resolved_at | ID / TS | Y | | |
| due_at | TS | Y | | 交互过期时间等 |
| source_event_id | ID | N | | 创建事件 |
| open_key | STR(200) | Y | | `<recipient>:<source_key>`，OPEN 时非空 |

唯一：`(tenant_id, open_key)`。索引：`(tenant_id, recipient_principal_id, state, priority, created_at)`、`(tenant_id, source_kind, source_id, state)`。

#### 3.8.9 站内通知存储（移交 09）

站内通知行、已读状态、推送 / 邮件投递状态与保留期由 09 C5 的通知表与收件 HTTP 拥有；本模块不建通知表。本模块职责：§9 类型目录、接收人解析、偏好（§3.8.10，经 `PreferenceResolver` 供 09 读取）、合并键与去重键（随 `Notifier.Send` 请求传入：`merge_key`、`merge_window_s`、`dedupe_key`、`priority`、`template_key`、`template_params`、`link`）。

#### 3.8.10 `governance_notification_preferences`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| principal_id | ID | N | | |
| type_key | STR(64) | N | | `*` = 全局默认行 |
| in_app | BOOL | N | true | 强制类型不可为 false |
| browser_push | BOOL | N | 按目录 | |
| email | ENUM | N | 按目录 | `OFF` / `IMMEDIATE` / `DAILY_DIGEST` |
| quiet_enabled | BOOL | N | false | 仅 `*` 行有效 |
| quiet_start_min / quiet_end_min | INT | Y | 1320 / 480 | 0–1439，可跨午夜 |
| timezone | STR(64) | Y | | IANA，空 = 浏览器上报值 / UTC |
| digest_hour | INT | N | 9 | 0–23 |

唯一 `(tenant_id, principal_id, type_key)`。

#### 3.8.11 `governance_audit_index`（审计链投影，可重建）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| audit_event_id | ID | N | 底座事件 id，唯一 `(tenant_id, audit_event_id)` |
| sequence | BIGINT | N | 链序号 |
| event_type | STR(96) | N | |
| module | STR(32) | N | event_type 首段 |
| project_id | ID | Y | 来自 Labels（CR-08-07）或按目标反查 |
| actor_kind | ENUM | Y | |
| actor_id | ID | Y | |
| authority_kind | ENUM | Y | `ROLE`/`DELEGATION`/`POLICY`/`CLIENT`/`SYSTEM` |
| authority_ref | STR(96) | Y | 如 `DELEGATION:8812` |
| target_type / target_id | STR(64) / ID | Y | |
| outcome | STR(16) | N | success / denied / failure |
| occurred_at | TS | N | |

索引：`(tenant_id, occurred_at)`、`(tenant_id, project_id, occurred_at)`、`(tenant_id, module, occurred_at)`、`(tenant_id, actor_id, occurred_at)`、`(tenant_id, authority_ref)`。

#### 3.8.12 其余

- `governance_audit_cursors`：`tenant_id`（唯一）、`last_sequence BIGINT`、`updated_at`。
- `governance_claim_evaluations`（S6 ⑪ 的评估回执）：`claim_id ID`（唯一 `(tenant_id, claim_id)`）、`work_item_id ID`、`low_confidence_result ENUM(PASS,TRIGGERED,SKIPPED)`、`sensitive_path_result ENUM(PASS,TRIGGERED,UNAVAILABLE)`、`evaluated_at TS`。由 `escalation_evaluator` 处理 `claim.submitted` 时与 incident 同事务写入；保留 180 天。
- 事件消费去重统一用 09 `domain_event_receipts`（Subscriber 模式 Transactional）；一次性业务去重由 incident `once_key` 唯一索引承担。本模块不建 consumed-events 表。
- 幂等统一用 09 C7 平台幂等表（00 §9）；本模块不建幂等表。


#### 3.8.13 `governance_spawn_grants`（D110，编制授权）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| grantee_agent_id | ID | N | | 05 Agent，须持 `LEAD` 角色且为人创建或已收编 |
| grantee_principal_id | ID | N | | 该 Agent 的服务账号 |
| project_ids | JSON | N | | 项目 entity id 数组；`[]` = 团队级（BR-08-50） |
| max_active_agents | INT | N | 3 | 1–10；同时在岗（非 RETIRED、未被收编）的 AI 创建 Agent 数上限 |
| max_spawns_per_day | INT | N | 5 | 1–20；UTC 自然日 |
| allowed_roles | JSON | N | `["EXECUTOR"]` | ⊆ {`EXECUTOR`, `COORDINATOR`}，≥1；**不得**含 REVIEWER / LEAD |
| allowed_machine_ids | JSON | N | | 04 机器 id 数组，≥1 |
| allowed_models | JSON | N | | `[{runtime, model}]`，1–20 |
| max_effort | STR(16) | N | `medium` | effort 上限（按 04 模型目录的 effort 顺序比较） |
| budget_cap_micros | BIGINT | N | 50000000 | 1_000_000 … 设置上限；授权内全部 AI 创建 Agent 的累计成本上限 |
| per_agent_cap_micros | BIGINT | N | 15000000 | ≤ budget_cap_micros |
| agent_ttl_hours | INT | N | 168 | 24–720；到期由 05 正常退役 |
| idle_retire_hours | INT | N | 48 | 4–336；无工作项达时长由 05 退役 |
| approval_mode | ENUM `SpawnApprovalMode` | N | `REQUIRE_APPROVAL` | `REQUIRE_APPROVAL` / `AUTO`；AUTO 仅当范围内项目全部 AUTONOMOUS（BR-08-51） |
| effective_approval_mode | ENUM | N | 同上 | 任一范围项目为 HYBRID 时为 `REQUIRE_APPROVAL`（不改 `approval_mode` 原值，条件恢复后回到原值） |
| reason | STR(2000) | N | | ≥10 字 |
| granted_by | ID | N | | HUMAN（`team_owner`） |
| starts_at / ends_at | TS | N | | `starts_at+1h < ends_at ≤ starts_at+90d` |
| state | ENUM `SpawnGrantState` | N | `ACTIVE` | `ACTIVE` / `EXHAUSTED` / `EXPIRED` / `REVOKED` |
| end_reason | ENUM | Y | | `MANUAL` `TAKEOVER` `WINDOW_ENDED` `GRANTEE_INACTIVE` `REPLACED` |
| ended_at / ended_by / end_note | TS / ID / STR(2000) | Y | | ended_by 空 = 系统 |
| active_count | INT | N | 0 | 计数器（BR-08-53），对账任务校正 |
| spawned_day | STR(10) | N | | `YYYY-MM-DD`（UTC），与 `spawned_today` 配对，跨日惰性归零 |
| spawned_today | INT | N | 0 | |
| spent_micros | BIGINT | N | 0 | 成本累计（BR-08-55） |
| threshold_notified_pct | INT | N | 0 | 已发阈值提醒（0 / 80 / 100），去重 |
| expiring_notified_at | TS | Y | | 72h 到期提醒已发 |
| active_key | STR(32) | Y | | `= grantee_agent_id`，仅 ACTIVE / EXHAUSTED 时非空 |
| replaces_grant_id | ID | Y | | 替换链 |

唯一：`(tenant_id, active_key)`（每个主管 Agent 至多 1 份未终结授权）。索引：`(tenant_id, state, ends_at)`、`(tenant_id, grantee_agent_id)`。

**计数方式（决定：授权行计数器 + 成员表）**：配额判定读授权行计数器（O(1)、可加行锁）；成员表提供按 Agent 的幂等与按 Agent 成本；对账任务以成员表与 05 `AgentDirectory` 为准校正计数器。不采用「每次对用量事件表求和」：配额判定在建 Agent 的热路径上，必须 O(1) 且可加锁。

#### 3.8.14 `governance_spawn_grant_members`（D110）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| grant_id | ID | N | |
| agent_id | ID | N | AI 创建的 Agent；唯一 `(tenant_id, agent_id)` |
| state | ENUM | N | `ACTIVE` / `RELEASED` |
| release_reason | ENUM | Y | `RETIRED` `ADOPTED` `GRANT_ENDED` |
| per_agent_cap_micros | BIGINT | N | 创建时快照 |
| spent_micros | BIGINT | N | 该 Agent 累计成本（RELEASED 后不再累计） |
| cap_notified | BOOL | N | 单 Agent 预算触顶已处理 |
| reserved_at / released_at | TS | N / Y | |

索引 `(tenant_id, grant_id, state)`。

#### 3.8.15 `governance_spawn_cost_cursors`（D110，成本增量游标）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| execution_id | ID | N | 唯一 `(tenant_id, execution_id)` |
| agent_id / grant_id | ID | N | |
| last_cumulative_micros | BIGINT | N | 已计入的 `cumulative_cost_micros`；增量 = 新值 − 本值（≤0 忽略） |
### 3.9 JSON Schema（draft 2020-12 摘要；实现写入 `internal/governance/schema/` 并单测）

#### 3.9.1 委托

```json
{"actions": {"type":"array","minItems":1,"maxItems":5,"uniqueItems":true,
   "items":{"enum":["decision_approve","decision_reject","decision_request_changes","code_merge","intake_convert"]}},
 "work_item_types": {"type":"array","maxItems":7,"uniqueItems":true,
   "items":{"enum":["FEATURE","BUG","CHORE","DOC","RESEARCH","DESIGN","TEST"]}},
 "iam_bindings": {"type":"array","items":{"type":"object","required":["role_key","role_id"],
   "properties":{"role_key":{"type":"string","pattern":"^delegated_[a-z_]+$"},"role_id":{"type":"string","pattern":"^[1-9][0-9]{0,18}$"}}}}}
```

#### 3.9.1b 编制授权（D110）

```json
{"allowed_roles":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,"items":{"enum":["EXECUTOR","COORDINATOR"]}},
 "allowed_machine_ids":{"type":"array","minItems":1,"maxItems":50,"uniqueItems":true,"items":{"type":"string","pattern":"^[1-9][0-9]{0,18}$"}},
 "allowed_models":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","additionalProperties":false,
   "required":["runtime","model"],"properties":{"runtime":{"enum":["CLAUDE_CODE","CODEX"]},"model":{"type":"string","minLength":1,"maxLength":160}}}},
 "project_ids":{"type":"array","maxItems":50,"uniqueItems":true,"items":{"type":"string","pattern":"^[1-9][0-9]{0,18}$"}}}
```

#### 3.9.2 升级规则（`EscalationRuleKind` 与 params）

| kind | 来源事件 | params Schema（`additionalProperties:false`） | 硬性（AUTONOMOUS 不可关） | 动作 AUTONOMOUS / HYBRID | 严重度 |
|---|---|---|---|---|---|
| `BUDGET` | `budget.threshold_reached`（07） | `{}`（阈值由 07 `budget_notify_pct` 与固定 100%） | 是 | NOTIFY（07 自行加 BUDGET hold）/ NOTIFY | 80%：WARNING；100%：CRITICAL |
| `VERIFICATION_INCONCLUSIVE` | `work_item.escalated{INCONCLUSIVE}`（02 已暂停） | `{}` | 是 | NOTIFY, HOLD_DELEGATIONS / HOLD_DELEGATIONS, NOTIFY | CRITICAL |
| `CLIENT_REJECTION` | `acceptance.received{REJECTED|PARTIALLY_ACCEPTED}` | `{"include_partial":{"type":"boolean","default":true}}` | 是 | PROJECT_HOLD, HOLD_DELEGATIONS（仅被拒收的工作项，subject=WORK_ITEM，每项一条 incident）, NOTIFY / HOLD_DELEGATIONS（同上）, NOTIFY | CRITICAL；对应返工来件进入终态（CONVERTED / DECLINED / MERGED）时自动 RESOLVED(`AUTO_CLEARED`) |
| `REWORK_LIMIT` | `work_item.escalated{REWORK_LIMIT}`（02 已暂停） | `{"threshold":{"const":3}}`（MVP 只读，02 Q5） | 是 | NOTIFY / NOTIFY | CRITICAL |
| `SPINNING` | `work_item.escalated{SPINNING}` | `{"ratio_pct":{"const":8}}`（只读） | 否 | NOTIFY / NOTIFY | WARNING |
| `REVIEW_SLA` | `work_item.escalated{REVIEW_SLA}` | `{}` | 否 | NOTIFY / NOTIFY | WARNING |
| `LOW_SELF_CONFIDENCE` | `claim.submitted` + 02 `WorkItemReader` 读 `self_confidence`（0–100） | `{"min_confidence":{"type":"integer","minimum":30,"maximum":90,"default":60}}` | 否 | HOLD_DELEGATIONS, NOTIFY / HOLD_DELEGATIONS | WARNING |
| `SENSITIVE_PATH` | `claim.submitted` + 03 `EvidenceReader.DiffSummary(execution_id)` 的变更路径 | 见下 | 否（默认启用） | HOLD_DELEGATIONS, NOTIFY / HOLD_DELEGATIONS, NOTIFY | CRITICAL |
| `NO_ELIGIBLE_AUTHORITY`（本规格提议） | `work_item.state_changed{to:NEEDS_HUMAN_REVIEW}`，AUTONOMOUS 且 `ListEligible(decision_approve)` 无 AGENT 委托来源；或 05 下发的 Agent 评审超时（REVIEWER 认领 30 min 内无裁决，`work_item.escalated{REVIEW_SLA, reviewer_kind:AGENT}`） | `{"agent_review_timeout_min":{"const":30}}` | 否 | NOTIFY（并为人类建 WORK_ITEM_REVIEW）/ —（HYBRID 不评估） | WARNING |
| `EXECUTION_STALLED`（本规格提议） | `execution.state_changed{to:INTERRUPTED}` 或 `work_item.state_changed{to:EXECUTION_FAILED}` 持续 ≥ `stall_minutes` 未恢复 / 重试 / 失败处理 | `{"stall_minutes":{"type":"integer","minimum":5,"maximum":1440,"default":30}}` | 否 | NOTIFY / NOTIFY | WARNING |
| `SPAWN_BUDGET`（D110） | 本模块成本累计（BR-08-55）：编制授权 `spent_micros` 达 80% / 100% | `{}`（阈值固定 80 / 100） | 是 | NOTIFY / NOTIFY（100% 时授权已 EXHAUSTED，不再建 Agent；已建 Agent 继续受单 Agent 预算与 07 项目预算约束） | 80%：WARNING；100%：CRITICAL |

`EscalationAction`：`NOTIFY`（§9 `governance.escalation_triggered`）、`HOLD_DELEGATIONS`（incident `hold_delegations=true`；含「转人」：收件箱对人类发 `ESCALATION_ALERT` 与 `WORK_ITEM_REVIEW`）、`PROJECT_HOLD`（07 `ProjectHoldSetter.AddEscalationHoldOn(tx, tenant, project, rule_id, reason)`，来源 `POLICY:<rule_id>`）。暂停工作项由 02 自行完成，本模块不重复（理由：02 已在同事务置 `require_human_decision`）。

`SENSITIVE_PATH.params`：

```json
{"type":"object","additionalProperties":false,"required":["pattern_sets"],
 "properties":{"max_paths_checked":{"type":"integer","minimum":100,"maximum":5000,"default":2000},
  "pattern_sets":{"type":"array","maxItems":20,"items":{"type":"object","additionalProperties":false,
   "required":["key","label","enabled","globs"],
   "properties":{"key":{"type":"string","pattern":"^[A-Z][A-Z0-9_]{1,31}$"},"label":{"type":"string","maxLength":64},
    "enabled":{"type":"boolean"},
    "globs":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"string","minLength":1,"maxLength":200}}}}}}}
```

glob 语法：仓库相对 POSIX 路径、区分大小写；`**` 跨目录、`*` 不跨 `/`、`?`、`{a,b}`；不支持否定。实现优先复用仓内已有依赖，无则自写匹配（≤80 行，表驱动单测）。默认集合（新租户种子）：

| key | globs |
|---|---|
| `MIGRATIONS` | `**/migrations/**` `**/migrate/**` `**/sql/**/*.sql` `**/*.migration.*` `**/db/schema.*` `**/prisma/schema.prisma` |
| `DEPENDENCIES` | `**/go.mod` `**/go.sum` `**/package.json` `**/package-lock.json` `**/bun.lock` `**/bun.lockb` `**/pnpm-lock.yaml` `**/yarn.lock` `**/Cargo.toml` `**/Cargo.lock` `**/requirements*.txt` `**/pyproject.toml` `**/poetry.lock` `**/uv.lock` `**/Gemfile.lock` `**/pom.xml` `**/build.gradle*` |
| `AUTH` | `**/auth/**` `**/authn/**` `**/authz/**` `**/iam/**` `**/*permission*` `**/*rbac*` `**/*acl*` `**/security/**` `**/*oauth*` `**/*session*` |
| `SECRETS_CI` | `**/.env*` `**/*.pem` `**/*.key` `**/secrets/**` `.github/workflows/**` `.gitlab-ci.yml` `**/Dockerfile*` `**/deploy/**` `**/k8s/**` `**/terraform/**` |

#### 3.9.3 incident `facts`

```json
{"type":"object","properties":{
 "threshold_pct":{"type":"integer"},"cap_version":{"type":"integer"},"spent_micros":{"type":"integer"},"cap_micros":{"type":"integer"},
 "work_item_id":{"type":"string"},"claim_id":{"type":"string"},"execution_id":{"type":"string"},"delivery_id":{"type":"string"},
 "acceptance_result":{"enum":["REJECTED","PARTIALLY_ACCEPTED"]},"rework_count":{"type":"integer"},"ratio_bp":{"type":"integer"},
 "self_confidence":{"type":"integer"},"matched":{"type":"array","maxItems":20,"items":{"type":"object",
   "properties":{"set":{"type":"string"},"glob":{"type":"string"},"path":{"type":"string","maxLength":512}}}},
 "matched_total":{"type":"integer"},"paths_unavailable":{"type":"boolean"}},"additionalProperties":false}
```

---

### 3.8.16 `governance_quality_escapes`（穿透记录，D113）

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| id / tenant_id / project_id | ID | N | | |
| work_item_id | ID | N | | |
| stage | ENUM `VerificationStage` | N | | `HUMAN_REVIEW` / `CLIENT_ACCEPTANCE` |
| source_event_id | ID | N | | `quality.escaped` 事件 id（唯一，幂等） |
| found_by_kind / found_by | STR(16) / ID | N | | 人或甲方联系人 |
| decision_ref / acceptance_ref | ID | Y | NULL | |
| passed_by | JSON | N | | `[{stage, principal_id, agent_id?, model_family, verifier_version}]` 曾放行该工作项的内部关口 |
| category | ENUM `FeedbackCategory` | N | | |
| summary | STR(2000) | N | | 结构化反馈摘要（不含代码正文） |
| learning_state | STR(16) | N | `PENDING` | `PENDING` / `RECORDED` / `DISMISSED`（模块内枚举 `EscapeLearningState`） |
| memory_ids | JSON | N | `[]` | 02 `delivery.escape_learning` 经 06 提出的经验记忆草稿 id（只读展示，由 02 `quality.escape_learning_proposed` 回填） |
| eval_case_ref | STR(200) | Y | NULL | 02 `delivery.escape_learning` 提交的 11 模板评测集用例引用（只读展示，由 02 `quality.escape_learning_proposed` 回填） |
| decided_by / decided_at | ID / 时间 | Y | NULL | RECORDED / DISMISSED 的人类 |
| created_at / version | 时间 / INT | N | | |

唯一：`(source_event_id)`。

## 4. 状态机

### 4.1 委托 `DelegationState`

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | `POST …/delegations` | BR-08-08…14；step-up | ACTIVE | IAM 绑定 ×N；审计 `governance.delegation_granted`；事件 `delegation.granted`；通知 |
| ACTIVE | 投影 `decision.made`（DELEGATION 来源） | 未投影过该 decision_id | ACTIVE | uses 行；`used_count+1` |
| ACTIVE | 到期任务 | `ends_at ≤ now` | EXPIRED(`WINDOW_ENDED`) | 删除残留绑定；审计 `governance.delegation_expired`；事件 `delegation.expired`（不再发 `delegation.revoked`） |
| ACTIVE | `POST /delegations/{id}/revocations` | `delegation_grant`；HUMAN | REVOKED(`MANUAL`) | 删除绑定 + revision 自增；审计 `governance.delegation_revoked`；事件 `delegation.revoked`；撤回受托相关收件箱项 |
| ACTIVE | 接管 / `project.mode_changed{to:HYBRID}` / 项目 CLOSED·CANCELLED | — | REVOKED(`TAKEOVER`/`MODE_CHANGED`/`PROJECT_CLOSED`) | 同上 |
| ACTIVE | 对账任务 | Agent 非 ACTIVE / 授予者不再经 ROLE 持有任一委托码 / 绑定缺失 | REVOKED(`GRANTEE_INACTIVE`/`GRANTOR_LOST_AUTHORITY`/`BINDING_REMOVED`) | 同上 |
| ACTIVE | 替换授予（`replaces_delegation_id`） | 同事务新委托通过校验 | REVOKED(`REPLACED`) | 同上 |
| EXPIRED / REVOKED | 任意命令 | — | — | 409 `160414`；重复撤销同一已撤销委托 → 200 返回当前（幂等，BR-08-17） |

`effective_state`（API 计算字段）：ACTIVE 且 `ends_at ≤ now` 时显示 EXPIRED（到期任务尚未运行的 ≤30s 窗口内，判定已经拒绝）。

### 4.2 升级事件 `EscalationIncidentState`

去重：`open_key = <kind>:<subject_kind>:<subject_id>`（开启期间同主体同规则只一条）；`once_key` 仅用于 BUDGET（`BUDGET:<project>:<cap_version>:<pct>`）与 CLIENT_REJECTION（`CLIENT_REJECTION:<acceptance_id>`）、LOW_SELF_CONFIDENCE / SENSITIVE_PATH（`<kind>:<claim_id>`）。

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | 规则命中 | 规则启用且适用模式；`open_key`/`once_key` 无冲突 | OPEN | 执行动作（同事务）；审计 `governance.escalation_triggered`；事件 `escalation.triggered`；收件箱 `ESCALATION_ALERT`；通知 |
| OPEN / ACKNOWLEDGED | 同 `open_key` 再命中 | — | 不变 | `occurrence_count+1`、`last_triggered_at`；距 `last_notified_at` ≥24h 才再通知 |
| — | `once_key` 冲突 | — | 不创建 | incident `once_key` 唯一索引拒绝插入（冲突即视为已处理），无副作用 |
| OPEN | 确认 | `policy_read` 且为接收人之一 | ACKNOWLEDGED | 审计；全部接收人的 ESCALATION_ALERT 收件箱项 → RESOLVED(`ACKNOWLEDGED`/`ACTED_BY_OTHER`) |
| ACKNOWLEDGED | 确认 | — | 不变 | 200 幂等 |
| OPEN / ACKNOWLEDGED | 07 `project.hold_changed` 移除对应 ESCALATION hold / 02 工作项人工放行（`work_item.state_changed` 离开暂停）/ 工作项进入 ACCEPTED·CANCELLED / 新 cap_version 低于阈值 | — | RESOLVED(`RESUMED` 或 `AUTO_CLEARED`) | 解除 hold_delegations；审计 `governance.escalation_resolved`；事件 `escalation.resolved`（CR-08-05） |
| OPEN / ACKNOWLEDGED | `POST …/dismissals` | `policy_manage`；HUMAN；理由 ≥10；CRITICAL 且来源 PROJECT_HOLD 时不可（须经 07 恢复） | DISMISSED | 解除 hold_delegations；审计 `governance.escalation_dismissed` |
| OPEN / ACKNOWLEDGED | 同主体同 kind 的更新触发（如新 claim） | — | RESOLVED(`SUPERSEDED`) 后新建 | — |
| RESOLVED / DISMISSED | 任意命令 | — | — | 409 `160432` |

### 4.3 收件箱项 `InboxItemState`

| 当前 | 触发 | 目标 | resolution |
|---|---|---|---|
| — | 源事件（§6.10 表） | OPEN | — |
| OPEN | 源动作由本人完成 | RESOLVED | `ACTED_BY_ME` |
| OPEN | 源动作由他人完成 | RESOLVED | `ACTED_BY_OTHER` |
| OPEN | 仅信息类（CLARIFICATION_REPLY、ESCALATION_ALERT）本人确认 | RESOLVED | `ACKNOWLEDGED` |
| OPEN | 接收人失去资格（权限、改派、委托撤销）/ 源被取消 | WITHDRAWN | `SOURCE_CLOSED` |
| OPEN | 源过期（交互 EXPIRED） | EXPIRED | `SOURCE_CLOSED` |
| RESOLVED / WITHDRAWN / EXPIRED | 任意 | — | 409 `160441`（终态不可重开；同源新一轮产生新项，`source_key` 不同） |

### 4.4 错误码（governance 段，i18n namespace `governance`，`governance.errors.<key>`，三语）

| 码 | HTTP | key | 场景 |
|---|---|---|---|
| 160400 | 422 | invalid_argument | 形状 / 范围校验 |
| 160403 | 403 | authority_denied | 一切权限拒绝（00 §9）；`data.reason_code` ∈ §3.5 表 ∪ {`AGENT_PROHIBITED`（非人类调用人类专属操作）, `REAUTH_REQUIRED`（step-up 证明缺失 / 过期 / 已用 / purpose 或 target 不符，附 `data.purpose`、`data.target`）, `GRANTOR_LACKS_AUTHORITY`（授予者未经角色持有委托码）}；`data.missing_permission?` |
| 160404 | 404 | not_found | |
| 160409 | 409 | version_conflict | If-Match 不匹配 |
| 160410 | 422 | non_delegable_action | 委托动作含不可委托码 |
| 160411 | 422 | invalid_grantee | 受托人非 ACTIVE Agent 或无 REVIEWER/LEAD 角色 |
| 160412 | 422 | invalid_window | 窗口越界 |
| 160413 | 409 | delegation_active_exists | 受托人×项目已有 ACTIVE（`data.delegation_id`） |
| 160414 | 409 | delegation_not_active | 非法迁移 |
| 160416 | 422 | delegation_scope_exceeds_limits | `max_risk=HIGH`、成本上限越界、merge 配 MEDIUM、intake_convert 用于 HYBRID 项目 |
| 160430 | 422 | escalation_rule_invalid | params / glob 非法 |
| 160431 | 409 | escalation_rule_locked | 关闭硬性规则（`data.autonomous_project_ids[]`） |
| 160432 | 409 | incident_illegal_transition | |
| 160433 | 409 | incident_dismiss_requires_resume | PROJECT_HOLD 类须经 07 恢复 |
| 160441 | 409 | inbox_item_closed | |
| 160450 | 422 | notification_preference_mandatory | 关闭强制类型站内通知 |
| 160460 | 409 | seed_conflict | 同名非托管角色 |
| 160461 | 503 | authority_unavailable | 依赖不可用，拒绝默认 |
| 160429 | 429 | rate_limited | 解释接口 ≤60 次/分/主体 |
| 160428 | 428 | precondition_required | 更新型请求缺少 `If-Match` |
| 160470 | 422 | spawn_grant_invalid | 编制授权字段越界（角色含 REVIEWER / LEAD、per_agent > budget、窗口越界、机器 / 模型不在 04 目录、受托人非 LEAD 或为未收编的 AI 创建 Agent） |
| 160471 | 409 | spawn_quota_exceeded | `Reserve`：同时在岗或今日新建数已满（`data.reason_code` = `SPAWN_QUOTA_EXCEEDED` / `SPAWN_DAILY_LIMIT`） |
| 160472 | 409 | spawn_budget_exhausted | `Reserve`：授权非 ACTIVE（EXHAUSTED / 已终结）或预算已用尽（`SPAWN_BUDGET_EXHAUSTED` / `SPAWN_GRANT_MISSING`） |
| 160473 | 409 | spawn_grant_active_exists | 该主管 Agent 已有未终结授权（`data.spawn_grant_id`；可用 `replaces_grant_id` 替换） |
| 160474 | 409 | spawn_grant_not_active | 非法迁移 |
| 160475 | 422 | spawn_grant_auto_requires_autonomous | `approval_mode=AUTO` 但范围含 HYBRID 项目或为团队级 |
| 160476 | 422 | spawn_grant_below_usage | 收紧后的上限低于当前用量（`data.usage{}`） |

### 4.5 编制授权 `SpawnGrantState`（D110）

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | `POST /spawn-grants` | BR-08-50…52；HUMAN ∧ `spawn_grant_manage`；step-up `spawn_grant_manage` | ACTIVE | 审计 `governance.spawn_grant_granted`；事件 `spawn_grant.granted`；SSE |
| ACTIVE / EXHAUSTED | `PATCH /spawn-grants/{id}` | 同上；收紧不需 step-up，放宽（任一上限调高、范围扩大、`approval_mode`→AUTO、延长 `ends_at`）需 step-up；新上限 ≥ 当前用量（`160476`） | 不变；EXHAUSTED 且新预算 > 已用 → ACTIVE | 审计 `governance.spawn_grant_updated`（before / after）；事件 `spawn_grant.updated` |
| ACTIVE | 成本累计达 `budget_cap_micros` | — | EXHAUSTED | 审计 `governance.spawn_grant_exhausted`；事件 `spawn_grant.exhausted`；升级规则 `SPAWN_BUDGET`（CRITICAL）；未决 AGENT_SPAWN_APPROVAL 项撤回 |
| ACTIVE / EXHAUSTED | `POST /spawn-grants/{id}/revocations` | HUMAN ∧ `spawn_grant_manage`（无 step-up） | REVOKED(`MANUAL`) | 审计 `governance.spawn_grant_revoked`；事件 `spawn_grant.revoked`；未决 AGENT_SPAWN_APPROVAL 项撤回 |
| ACTIVE / EXHAUSTED | 接管（BR-08-22 ⑥）/ 对账发现主管 Agent 非 ACTIVE 或失去 LEAD | — | REVOKED(`TAKEOVER` / `GRANTEE_INACTIVE`) | 同上 |
| ACTIVE / EXHAUSTED | 到期任务 `ends_at ≤ now` | — | EXPIRED(`WINDOW_ENDED`) | 事件 `spawn_grant.expired`；审计 `governance.spawn_grant_revoked`（end_reason=WINDOW_ENDED） |
| ACTIVE / EXHAUSTED | 替换（`replaces_grant_id`） | 同事务新授权通过校验 | REVOKED(`REPLACED`) | ACTIVE 成员行与计数器迁移到新授权 |
| EXPIRED / REVOKED | 任意命令 | — | — | 409 `160474`；重复撤销同一已撤销授权 → 200 返回当前（幂等） |

授权终结（EXPIRED / REVOKED，`REPLACED` 除外）**不自动退役**已建 Agent：成员行 `RELEASED(GRANT_ENDED)`，05 发布 `agent.orphaned` 并按其规则处理（停止接新工作、提示人收编或退役）。`effective_state`：ACTIVE 且 `ends_at ≤ now` 时显示 EXPIRED（到期任务运行前的 ≤30s 窗口内 Check 已按 EXPIRED 拒绝）。

---

### 4.6 D112 / D113 错误码

`160480` escape_not_found（404）、`160481` escape_learning_already_decided（409）、`160482` andon_clear_requires_human（403 + reason `HUMAN_ONLY`，由 02 端点返回时用 120403）、`160483` plan_review_policy_denied（仅 explain 用）。

## 5. 业务规则与边界情况

**目录与角色**
- **BR-08-01** 每个模块只贡献自己的码；重复码使 `Contribute` 整体失败（底座原子语义），启动失败。
- **BR-08-02** 播种幂等：同一租户执行 N 次，IAM 角色数、权限行、审计条数与执行 1 次相同。并发播种由 `dlock governance:seed:{tenant}`（TTL 60s）串行，锁失败的实例跳过本轮。
- **BR-08-03** 清单码在 Registry 缺失（模块关闭）→ 记入 `skipped_codes`；模块启用后的下一次播种补授。
- **BR-08-04** 伪码 `project_create` 不注册到 Registry，只出现在 `NonDelegable` 与 Check 请求中；01 / 07 新建项目路径以 `Code=project_create` 调用 Check（S4 映射为 IAM 码 `project_manage`）。
- **BR-08-05** `HumanOnly`：AGENT / MACHINE 主体请求其中任一码，无论角色如何 → `AGENT_PROHIBITED`。防止管理员把 `delivery_lead` 误绑给 Agent 后产生交付 / 范围变更能力。

**AuthorityChecker**
- **BR-08-06** 显式拒绝优先：IAM `explicit_deny` 对角色、委托、策略三条路径一律生效。
- **BR-08-07** 委托只对 AGENT 生效；HUMAN 匹配到载体角色（误绑）被忽略并在 explain 中标 `ignored_carrier_match`。

**委托授予**
- **BR-08-08** 授予人：HUMAN；路由 `delegation_grant`；Check(`delegation_grant`)；step-up purpose `delegation_grant`，target `project:<project_id>:grantee:<principal_id>`。
- **BR-08-09** 受托人：05 `AgentDirectory` 中 ACTIVE、为本租户活跃成员；角色要求按动作：`actions` 含任一 `decision_*` 或 `code_merge` → 受托人必须持有 `REVIEWER`（仅 LEAD 不足：主管 Agent 负责分派，裁决须由评审角色承担，职责分离）；仅含 `intake_convert` → 受托人须持有 `COORDINATOR` 或 `REVIEWER`；否则 `160411`。受托人在项目无读权限 → 允许授予但响应 `warnings:["GRANTEE_NO_PROJECT_READ"]`（UI 提示先在项目团队中加入该 Agent）。受托人为 AI 创建且未被人收编的 Agent → `160411`，`data.reason_code=SPAWNED_NOT_ADOPTED`（BR-08-56，D110）。
- **BR-08-10** 动作：`actions ⊆ Delegable`；含任何 `NonDelegable` → `160410`；含其他码 → `160400`。`code_merge` 要求 `max_risk=LOW`；`intake_convert` 要求项目当前 AUTONOMOUS；否则 `160416`。由于 07 P1 要求切换 AUTONOMOUS 前已有委托，`intake_convert` 只能在切换之后经 `replaces_delegation_id` 以「原动作 ∪ {intake_convert}」替换授予（同事务撤销旧委托为 REPLACED）。委托的 `intake_convert` 覆盖 01 在同事务内调用 02 `RequirementCreator` 创建需求（不再单独检查 `requirement_write`）；NEW_PROJECT / APPEND 仍不可委托，进入人类收件箱 `INTAKE_TRIAGE`。
- **BR-08-11** 授予者对每个委托码须经 ROLE 来源持有（以 Check 计算，忽略其自身委托）→ 否则 `160403(GRANTOR_LACKS_AUTHORITY)`；另经底座绑定越权防护。
- **BR-08-12** 每（受托人, 项目）至多 1 条 ACTIVE；冲突 → `160413`；请求带 `replaces_delegation_id` 时同事务先撤销旧委托（REPLACED）再授予。
- **BR-08-13** 窗口：`duration_hours` 0.25–`max_days×24`；成本上限 1..`delegation_max_cost_cap_micros`；`max_rework` 1–3；理由 10–2000 字。
- **BR-08-14** 项目阶段为 CLOSED / CANCELLED → `160416`（`data.reason=PROJECT_TERMINAL`）。
- **BR-08-15** 幂等：`Idempotency-Key` 必填；重放返回首次响应；同键不同请求 → 平台码 `176425`，同键进行中 → `176409`（09 C7，00 §9）。

**委托撤销、到期、接管**
- **BR-08-16** 撤销不需要 step-up（本规格提议：收回权力必须最快，与 07 BR-07-17「切回混编无需 step-up」一致）；理由 ≥5 字。
- **BR-08-17** 撤销幂等：对已 REVOKED 的委托重复撤销 → 200 返回当前状态；对 EXPIRED → 409 `160414`。
- **BR-08-18** 撤销事务内顺序：① `UPDATE … SET state='REVOKED' WHERE id=? AND state='ACTIVE' AND version=?`（行锁）→ ② IAM `RemoveOn` 每个绑定（取租户 revision 锁）→ ③ 审计 → ④ outbox。锁顺序固定为「委托行 → IAM revision」；Check 只读 IAM、只对委托行加共享锁，因此无环。
- **BR-08-19** 到期：IAM 绑定 `expires_at=ends_at`，在 `ends_at` 时刻判定即刻拒绝（与任务无关）；任务 ≤30s 内补状态。
- **BR-08-20 撤销与在途裁决**：02 在裁决事务内调用 `Check(tx)`。PG/MySQL：Check 对命中委托行 `FOR SHARE`。(a) 撤销先提交 → 裁决读到 REVOKED → `NO_DELEGATION` 拒绝；(b) 裁决先取得共享锁 → 撤销的 UPDATE 等待裁决事务结束 → 裁决以 `DELEGATION:<id>` 提交，`decided_at < revoked_at`，合法。SQLite：单写者；裁决事务读后升级写锁时若撤销已提交，得到 `SQLITE_BUSY_SNAPSHOT`，02 重试时读到 REVOKED。已提交的裁决不追溯撤回（审计可查，人可另行 REQUEST_CHANGES）。
- **BR-08-21** 撤销后：受托 Agent 的收件箱无项（Agent 无收件箱）；`delegation.revoked` 被 02 消费以释放该 Agent 的 REVIEWER 认领（02 §7.3）；07 据此评估自治前置条件（BR-07-16）。
- **BR-08-22 接管**：HUMAN；`takeover`；step-up purpose `project_takeover` target `project:<id>`；单事务：① 撤销项目全部 ACTIVE 委托（`TAKEOVER`）→ ② 07 `ProjectModeSwitcher.TakeOverOn(tx, tenant, project, actor, reason, revokedIDs)`（项目已 HYBRID 时 07 返回 no-op，本模块仍完成撤销）→ ③ 写 `governance_takeovers` → ④ 审计 `governance.takeover_executed` → ⑤ outbox `delegation.revoked`×N、`takeover.executed`（CR-08-05）→ ⑥ 撤销覆盖该项目的编制授权（BR-08-57，D110；outbox `spawn_grant.revoked`×M）。任一步失败整体回滚。未关闭 incident 不变（问题未消失），`resolved_by` 仍需人。
- **BR-08-49 接管后补投影**：`takeover.executed` 或 `project.mode_changed{to:HYBRID}` 到达时，`inbox_projector` 读取 02 `WorkItemReader` 中该项目全部 NEEDS_HUMAN_REVIEW 工作项，按 `work_item:<id>:claim:<claim_id>` 幂等建 WORK_ITEM_REVIEW（已存在 OPEN 项不重复），保证切回混编后已在等待评审的工作项都有人类接收人。
- **BR-08-23** 对账（每 5 分钟）：IAM 绑定缺失、Agent 非 ACTIVE、授予者失去 ROLE 来源 → 撤销（理由码见 §3.8.3）。授予者失权撤销为本规格提议：委托的正当性来自授予者的权限。

**升级规则**
- **BR-08-24** 新租户种子：10 条团队默认规则（§3.9.2 默认值）；07 `project.created` → 复制为项目规则（幂等键 `project.created:<event_id>`）。
- **BR-08-25** 编辑：`policy_manage`；If-Match；params 经 Schema 与 glob 编译校验（`160430`）。
- **BR-08-26** 放宽类修改（关闭规则、提高 `min_confidence` 以外的放宽、删除 pattern set 或禁用之）需 step-up purpose `escalation_rule_relax` target `rule:<id>`；收紧类不需要。
- **BR-08-27** 评估在消费事件的事务中执行：去重键冲突 → 不产生副作用；动作 `PROJECT_HOLD` 失败（07 拒绝）→ incident 仍创建，`actions_applied[].status=FAILED` 并以 CRITICAL 通知（失败不吞，失败即可见）。
- **BR-08-28** `SENSITIVE_PATH`：`DiffSummary` 不可用、超时（3s）或路径截断（> `max_paths_checked`）→ 视为命中（`paths_unavailable=true`），失败即冻结委托。facts 只存前 20 条命中与总数。
- **BR-08-29** `LOW_SELF_CONFIDENCE`：`self_confidence` 为 NULL（人类执行或 Agent 未自评）不触发；`< min_confidence` 触发（60 触发阈值下 59 触发、60 不触发）。
- **BR-08-30** 硬性规则（BUDGET、VERIFICATION_INCONCLUSIVE、CLIENT_REJECTION、REWORK_LIMIT）：项目为 AUTONOMOUS 时不可 `enabled=false`、不可删除动作 → `160431`；团队默认行关闭时，对 AUTONOMOUS 项目的项目行不传播（07 P2 依赖项目行）。
- **BR-08-31** HYBRID 模式按 `actions_hybrid` 执行（缺省不加项目 hold）。理由：PRD §8.4 的暂停护栏针对无人值守；混编有人在场，冻结委托 + 通知即可。

**收件箱与通知**
- **BR-08-32** 收件箱只为 HUMAN 建项；Agent 的待办经 03 / 05 下发。
- **BR-08-33** 收件箱不提供任何动作接口（除信息类确认）；A/R 调用源模块接口，源模块的授权与状态守卫是唯一判定。源已终态 → 源返回 409，前端移除该项并刷新计数。
- **BR-08-34** 不提供批量：API 无批量裁决端点，前端无多选（PRD §12.2）。
- **BR-08-35** 一致性：源事件到达 → 收件箱行提交 → SSE 推送，p95 ≤1s（预算：outbox 投递 ≤300ms〔09〕+ 投影 ≤200ms + SSE ≤300ms）；对账任务每 60s 用源读取接口复核 OPEN 项（按 `source_key` 批量），差异修正并推送。
- **BR-08-36** 资格复核：读取列表时按 IAM revision 缓存（60s）复核接收人对项目的 `project_read`；无权的 OPEN 项不返回并异步撤回。
- **BR-08-37** 通知只发给 HUMAN；正文模板只含标识、编号、标题（≤80 字截断）、计数，不含代码 / 消息 / 反馈正文。
- **BR-08-38** 合并窗口内同 `merge_key` 只保留一条（count 累加，推送与邮件在窗口结束时发一次）；`dedupe_key` 冲突直接丢弃（跨类型去重，如 07 与本模块的委托到期提醒共用 `delegation_expiring:<id>`）。
- **BR-08-39** 免打扰：开启时推送与邮件延后到时段结束（合并为一条），CRITICAL 绕过；站内永不抑制。
- **BR-08-40** 强制类型（§9 标 ●）：站内不可关；其中 CRITICAL 的邮件不可设 OFF（可选 IMMEDIATE / DAILY_DIGEST 之外强制 IMMEDIATE）→ `160450`。

**审计**
- **BR-08-41** 业务事件由所属模块在业务事务内经 `audit.Appender` 追加；拒绝类（outcome=denied）由调用方在**独立事务**追加（回滚的事务带不走审计，对齐 02 D1 ⑧⑨）。governance 不重复写拒绝审计。
- **BR-08-42** 审计 detail 必含 `reason`、`evidence_refs[]`、`authority_source`、`actor_kind`、`project_id`（团队级动作为 null）；`delegation_id` 在委托来源时必含。
- **BR-08-43** 导出、明细、完整性校验直接调用底座接口（需 `audit_event_view` / `audit_event_export`，已在种子角色中）；底座导出不支持按项目过滤，导出的是租户链区间（保证可校验）；页面按项目筛选只影响列表。
- **BR-08-44** 保留：沿用底座保留策略（默认 min 365 天）；本规格建议团队设 ≥730 天以覆盖合同质保期（提示，不强制）。索引表随底座归档：底座归档删除的 sequence 区间，索引同步标记 `archived`（v1.1；MVP 不删除索引）。

**通用**
- **BR-08-45** 所有写接口：`Idempotency-Key`（创建 / 命令型）或 `If-Match`（更新型）；缺少 If-Match → 428 `160428`；不匹配 → 409 `160409`。
- **BR-08-46** 依赖不可用（IAM、02/03/05/07 读取接口、DB）→ 503 `160461`，判定类一律拒绝；读模型类（总览各节）按节降级返回 `status:"error"`。
- **BR-08-47** 上限：每租户 ACTIVE 委托 ≤200；每项目升级规则 ≤20；pattern set ≤20×50 glob；收件箱列表单页 ≤100；通知偏好行 ≤100/人。
- **BR-08-48** 时间：所有判定使用服务端 UTC；客户端时间只用于显示。

**编制授权（D110，主管 Agent 自建 Agent）**
- **BR-08-50** 授予：仅 HUMAN ∧ `spawn_grant_manage`（种子仅 `team_owner`；该码 ∈ `NonDelegable` ⊆ `HumanOnly`，S2 对一切非人类主体拒绝 `AGENT_PROHIBITED`）；step-up purpose `spawn_grant_manage` target `agent:<grantee_agent_id>`；受托主管 Agent 须 ACTIVE、持 `LEAD`、且为人创建或已收编（否则 `160470`）；`project_ids=[]`（团队级）需二次确认并只能 `REQUIRE_APPROVAL`；`allowed_machine_ids` / `allowed_models` 须存在于 04 机器与模型目录；每主管 Agent 至多 1 份未终结授权（`160473`，可替换）；每租户未终结授权 ≤20。
- **BR-08-51** 审批模式：`AUTO` 仅当范围内**全部**项目为 AUTONOMOUS（否则 `160475`）；消费 07 `project.mode_changed{to:HYBRID}` 时，所有覆盖该项目的授权（含团队级）`effective_approval_mode` 立即降为 `REQUIRE_APPROVAL`（`approval_mode` 原值不变；范围内全部恢复 AUTONOMOUS 时回到原值），发 `spawn_grant.updated`。混编项目中 AI 建 Agent **永远**需人批准。
- **BR-08-52** 范围不可越权：`allowed_roles` ⊆ {EXECUTOR, COORDINATOR}——AI 不能创建评审员或主管 Agent（防自建自审与自我繁殖链）；AI 创建且未收编的 Agent 不得持有编制授权（`160470`）。
- **BR-08-53** 配额原子性：`Reserve` 在 05 建 Agent 的同一事务内执行：PG / MySQL `SELECT … FOR UPDATE` 锁授权行，SQLite 依赖 `_txlock=immediate` 写锁；跨日先把 `spawned_today` 归零并更新 `spawned_day`；校验 `effective_state=ACTIVE ∧ active_count < max_active_agents ∧ spawned_today < max_spawns_per_day ∧ spent_micros < budget_cap_micros`；通过则 `active_count+1`、`spawned_today+1`、`version+1`（`UPDATE … WHERE id=? AND version=?`，影响 0 行 → 重读重试 ≤3 次，仍失败 → `160471`），并插入成员行（唯一 `(tenant_id, agent_id)`；同 agent 重复调用视为已占用、不重复计数）。并发两个建 Agent 只剩 1 个名额时恰一个成功，失败方整个建 Agent 事务回滚。
- **BR-08-54** 释放：`Release` 把成员行置 `RELEASED` 并 `active_count−1`（不低于 0）；`spawned_today` 不回退（日配额按发起次数计，防止建了即退刷配额）。触发：05 退役（`RETIRED`）、人收编（`ADOPTED`：收编后不再占编制、成本不再计入授权）、授权终结（`GRANT_ENDED`）。建 Agent 事务回滚时 `Reserve` 随之回滚，无需显式释放。
- **BR-08-55** 成本累计：消费 03 `execution.cost_updated`（09 Transactional 订阅，receipts 去重）；按 `agent_id` 查 ACTIVE 成员行（RELEASED 或非成员不计）；增量 = `cumulative_cost_micros − cursor.last_cumulative_micros`（≤0 忽略，游标单调）；同事务更新游标、成员 `spent_micros`、授权 `spent_micros`。成员 `spent_micros ≥ per_agent_cap_micros` 且 `cap_notified=false` → 发 `spawn_grant.updated{member_cap_reached:agent_id}`（05 消费后停止该 Agent 接新工作）并通知授予人；授权达 80% → 通知（`threshold_notified_pct` 去重）；达 100% → EXHAUSTED（§4.5）。07 项目预算独立约束，两者先到者生效。
- **BR-08-56** 委托排除：AI 创建且未收编的 Agent（05 `AgentDirectory.origin=AGENT_SPAWNED ∧ adopted_at IS NULL`）不能作为任何委托的受托人（BR-08-09 → `160411`，`data.reason_code=SPAWNED_NOT_ADOPTED`）。人收编后按普通 Agent 对待。
- **BR-08-57** 接管：BR-08-22 单事务增加 ⑥：撤销 `project_ids` 包含该项目的全部未终结编制授权（`TAKEOVER`）；团队级授权不因单项目接管撤销，但其 `effective_approval_mode` 按 BR-08-51 降级。
- **BR-08-58** 审批：`effective_approval_mode=REQUIRE_APPROVAL` 时 05 先发布 `agent.spawn_requested`（含 `spawn_request_id`、`spawn_grant_id`、请求事实），本模块投影 `AGENT_SPAWN_APPROVAL`；人〔批准〕/〔驳回〕调用 05 建 Agent 请求接口（05 在批准事务内 `Reserve`；期间授权被撤销 / 耗尽 → 批准失败并提示）；请求 24h 未处理由 05 置过期（`agent.spawn_expired`）。`AUTO` 时 05 直接在建 Agent 事务内 `Reserve`，不建收件箱项。
- **BR-08-59** 审计与上限：授予、修改、撤销、耗尽、到期均写审计（§7.5，detail 含授权快照）；AI 创建 Agent 的审计（05 `fleet.agent_spawned`）必须带 `spawn_grant_id` 与 `supervisor_agent_id`，供审计页按来源过滤。

---

### 5.x D112 / D113 增补规则

- **BR-08-60 内部失败不进收件箱（D113）**：以下事件**不创建任何收件箱项、不发任何通知**，只计入指标：`internal_qa.failed`、`verification.completed` 中 stage ∈ {AUTOMATED, AGENT_TEST, AGENT_ACCEPTANCE} 的 FAILED / INCONCLUSIVE、`work_item.state_changed{to:REWORK}` 且 `cause ∈ INTERNAL_QA`、内部返工分支的 `execution.*`。投影器以 allowlist 实现（只有 §6.10 列出的事件会产生收件箱项），并有架构测试防止新事件误入。另加**可见性过滤**：任何事件信封或载荷带 `visibility=INTERNAL`（02 BR-02-54 定义）时，即使事件类型在 allowlist 内，投影器与通知发件箱也**一律丢弃**——不建收件箱项、不发通知、不在频道发人类可见卡片；只计入 07 指标与审计。过滤先于类型匹配执行。
- **BR-08-61 升级路由**：02 发布 `work_item.escalated{reason}` 时按 `EscalationReason` 路由：`INTERNAL_ROUNDS_EXHAUSTED` → `ESCALATION_INTERNAL_EXHAUSTED`；`REQUIREMENT_CONFLICT` → `REQUIREMENT_CONFLICT`（只给交付负责人，**不**直达甲方）；`ANDON` → `ANDON`；`TOLERANCE_EXCEPTION` → `EXCEPTION_DECISION`；`BUDGET` → 既有 `ESCALATION_ALERT`；`MANDATORY_HUMAN_GATE` → `WORK_ITEM_REVIEW`。
- **BR-08-62 Andon**：任何持 `andon_pull` 的主体（含 Agent）可停线（理由必填，≥10 字）；`andon_clear` 仅人类；停线期间对该工作项 / 项目的派发、裁决、交付在 AuthorityChecker 前由 02 守卫拒绝（reason `ANDON_ACTIVE`，02 登记）；Agent 一小时内拉线超过 3 次 → 自动升级 `ESCALATION_ALERT`（防滥用）。
- **BR-08-63 穿透处理**（D114）：消费 `quality.escaped`（任一发布者：02 / 07 / 12 Part A）→ 建 `governance_quality_escapes`（幂等于事件 id）→ 通知所有者与项目交付负责人 → `learning_state=PENDING` 进入「质量穿透」页待人确认。学习条目**不由本模块生成**：EXPERIENCE 记忆草稿（「漏检：{category}：{summary}」，经 06 `MemoryWriter.Propose`）与评测用例（11 `EvalSetWriter.ProposeCase`）只由 02 `delivery.escape_learning` 提出；本模块不调用 `MemoryWriter`、不向 11 提交评测用例。人工确认：RECORDED → 02 已提出的记忆草稿生效、评测用例保留；DISMISSED → 理由必填，撤回这些草稿与用例。确认结果经 `quality.escape_learning_decided` 发布，由 02 `delivery.escape_learning` 执行生效 / 撤回。
- **BR-08-64 负责人检查**：每日任务扫描处于 READY 及之后状态但 `accountable_principal_id` 为空或指向已停用成员的工作项 → 给交付负责人建 `ESCALATION_ALERT`（subkind `ACCOUNTABLE_MISSING`）；负责人离职时其工作项自动转给项目交付负责人并通知。
- **BR-08-65 总览质量磁贴**：总览增加「穿透率」与「内部一次通过率」磁贴（数据源 07 `DeliveryMetricsReader`，刷新 ≤60 s），穿透率 > 10% 显示琥珀、> 20% 显示红色（模板可覆盖）。
- **BR-08-66 远程工作区授权（D119）**：① 预览、产物、文件与只读终端的访问只看项目 `execution_read`，每次访问由 03 判定（不缓存超过 30 s），Agent 与服务账号不能换取预览访问；② `terminal_interact`、`preview_share_approve`、`machine_access_manage` 不可委托、仅限人类，经 `AuthorityChecker` 判定（委托来源 → `NON_DELEGABLE`，Agent → `AGENT_PROHIBITED`）；③ `preview_share_approve` 适用职责分离：批准人 = 申请人 → `SELF_REVIEW`；④ 机器开关不是权限码而是机器属性，关闭时由 03 / 04 以 `MACHINE_ACCESS_DISABLED` 拒绝，即使主体持有对应权限码。

### 5.y D120 增补规则（操作清单、团队工具目录、Agent 运行时）

- **BR-08-67 操作清单登记一致性**：05 `OperationRegistry.Register` 登记的每个操作，其 `permission.code` 必须是 §3.2 目录中的码（`via=SELF` 时为空，`via=RELATION` 时附 `relation`），且与该接口路由守卫（`authz.Register*`）及服务内 `AuthorityChecker.Check` 使用的码一致；`human_only` 必须等于该码 ∈ `HumanOnly`，`delegable` 必须等于该码 ∉ `NonDelegable`。本模块在 `Start` 时向 05 提供校验器，逐项比对，任一不一致 → 启动失败并在日志指明操作名与差异（禁止静默降级）；架构测试另对路由表与清单做静态比对（本规格提议）。被拒响应的 `data.result` 与 M03 状态按 §3.5c。
- **BR-08-68 团队工具目录治理**：① 目录条目（MCP 服务器、技能）的授予默认**无**：未被授予给某项目 / 角色 / Agent 的条目对任何 Agent 都不可用。② `tool_catalog_manage` 仅限人类、不可委托（§3.3d）；Agent（含持自定义角色者）调用目录管理接口 → S2 拒 `AGENT_PROHIBITED`；Agent 不能管理目录，也不能给自己或其他 Agent 授予条目。③ 设置 / 轮换 MCP 凭据需 step-up `tool_credential_manage`（§6.9，target `tool_entry:<id>`），凭据只写不回显。④ 05 MCP 中继拒绝未授予的调用时 `reason_code=TOOL_NOT_GRANTED`。⑤ 每次经中继的工具调用追加审计 `fleet.tool_called`（detail 只含 `entry_key`、`version_no`、`tool_name`、`execution_id`、`agent_id`、`args_sha256`、`outcome`、`duration_ms`，不含参数与结果原文），拒绝写 `fleet.tool_call_denied`（outcome=denied）；审计页（§8.2）增加「工具调用」分类筛选（`event_type ∈ {fleet.tool_called, fleet.tool_call_denied}`）。
- **BR-08-69 Agent 运行时的授权口径**：① `agent_workspace_read` 为关系授权：Agent 的负责人类（05 `owner_principal_id`）隐式持有，S4 判定时把 `owner_principal_id = 主体` 视为匹配（同 `andon_clear` 的负责人关系）；其他人须显式授予（默认仅 `team_owner`），Agent 主体一律拒（`AGENT_PROHIBITED`）。② Agent 完全重置仅限人类：`agent_manage` + HUMAN + step-up `agent_full_reset`（§6.9）；Agent 或服务账号发起 → `AGENT_PROHIBITED`。③ `interaction_request` 的判定主体是该执行的**执行者**（Agent 执行为该 Agent，人类交互会话为发起者），资源为该执行；批准仍为 `interaction_respond`（仅人类）。④ 收件箱读取与确认、自设提醒、读取本人能力视图与有效工具集等 `via=SELF` 操作不经权限码，只校验调用者是该 Agent 本人的服务账号（05 BR-05-130）。

## 6. 接口契约

### 6.1 通用

- huma v2 + `respx` 信封；错误 `respx.Err(ctx, code, "governance.errors.<key>")`。守卫写法：`T` = `authz.Register`（实际路径 `/tenants/{tenant_id}` + 路径）；`P` = `authz.RegisterEntity`（`/tenants/{tenant_id}/entities/{entity_id}` + 路径，entity = 项目）；`O` = `authz.RegisterOwned`（全局短地址，owner 解析器由本模块提供，解析到租户）；`M` = `authz.RegisterPrincipalTenantMember`（`/tenants/{tenant_id}/principals/{principal_id}` + 路径，仅本人）；`TM` = `authz.RegisterTenantMember`。
- OpenAPI tag = 最终发布路径（含 `/tenants/{tenant_id}`、`/entities/{entity_id}`、`/principals/{principal_id}` 等挂载前缀）的第一段，由底座 `OnAddOperation` 自动设置（00 §9）。因此 T / P / M / TM 守卫的操作 tag 均为 `tenants`，O 守卫的全局短地址取其首段。
- 列表：`pkg/query`（`field=op:value`、offset 分页、`capabilities`）；默认 limit 50，最大 100。
- 乐观锁：响应带 `ETag: "<version>"`；更新须 `If-Match`。幂等：创建 / 命令型 POST 必带 `Idempotency-Key`（≤64）。
- step-up：请求体字段 `reauth_proof`（≤256）；服务在事务**第一步**调用 `ReauthenticationConsumer(ctx, tx, principal, cookieHeader, proof, purpose, target)`；Bearer 调用方无 Cookie → `160403(REAUTH_REQUIRED)`（结构上排除 PAT / 服务账号）。

### 6.2 授权与解释

| # | 方法 路径 | tag | 守卫 · 权限 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| G1 | GET `/permission-matrix` | tenants | T · `policy_read` | query `project_id?` | `{roles[{role_id, role_key?, name, managed, seeded, member_count}], actions[{code, prd_action, module, non_delegable, delegable, human_only, data_scoped}], cells[{role_id, code, state: GRANTED\|CONDITIONAL\|NONE, condition_summary?}], delegations[{delegation_id, grantee{principal_id, agent_id, name}, project_id, codes[], ends_at, used_count}], production_grants[{principal_id, ends_at}], revision}` | 160404（项目） | — |
| G2 | POST `/authority-explanations` | tenants | P · `project_read`；`principal_id≠本人` 时服务内要求 `policy_read` | `{principal_id?, code*, resource{kind*, id*}, environment?}`（facts 由服务经 01/02 读取接口自行组装，客户端不能伪造） | §3.5.3 | 160400, 160403, 160429, 160461 | 只读 |
| G3 | GET `/role-seeds` | tenants | T · `policy_read` | — | `{manifest_version, roles[{role_key, role_id, managed, seed_conflict, applied_codes[], skipped_codes[], drift{missing[], extra[]}}]}` | — | — |
| G4 | POST `/role-seed-applications` | tenants | T · `policy_manage`；HUMAN | `{}` | `{applied, roles[{role_key, added_codes[]}]}` | 160460 | Idempotency-Key；天然幂等 |
| G5 | PATCH `/role-seeds/{role_key}` | tenants | T · `policy_manage` | `{managed:false}`（只允许脱离托管，不可重新托管：本规格提议，防止自动授权覆盖手工裁剪） | 角色种子 | 160404, 160409 | If-Match |
| G6 | GET `/dangerous-operations` | tenants | TM | — | §6.9 登记表 | — | — |

`prd_action` 映射（矩阵行分组）：observe→`*_read`；execute→`work_item_execute`/`execution_start`；assign→`work_item_assign`；submit_evidence→`evidence_submit`；verify→`verification_run`；approve/reject/request_changes→`decision_*`；change_scope→`scope_change`；pause/stop→`decision_pause`/`execution_stop`；merge→`code_merge`；deliver→`delivery_send`；administer→`policy_manage`/`machine_manage`/`agent_manage`/`intake_channel_manage`/`gateway_manage`/`knowledge_manage`；生产→`production_access`。

### 6.3 委托与接管

| # | 方法 路径 | tag | 守卫 · 权限 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| D1 | GET `/delegations` | tenants | T · `policy_read` | query：`state` `project_id` `grantee_principal_id` `grantor_principal_id` `actions`(contains) `ends_at`(lt/gt)；排序 `ends_at` `created_at` | `Delegation[]` | — | — |
| D2 | POST `/delegations` | tenants | P · `delegation_grant`；HUMAN；step-up `delegation_grant` | `{grantee_principal_id*, actions*[], work_item_types[], max_risk*, cost_cap_micros*, max_rework?, duration_hours*, reason*, replaces_delegation_id?, reauth_proof*}` | `{delegation, revoked_delegation?, warnings[]}`（201） | 160400, 160410, 160411, 160412, 160413, 160416, 160403(AGENT_PROHIBITED), 160403(REAUTH_REQUIRED), 160403(GRANTOR_LACKS_AUTHORITY), 160461 | Idempotency-Key |
| D3 | GET `/delegations/{delegation_id}` | delegations | O · `policy_read` | — | `Delegation` + `ETag` | 160404 | — |
| D4 | POST `/delegations/{delegation_id}/revocations` | delegations | O · `delegation_grant`；HUMAN | `{reason*}` | `Delegation`（REVOKED） | 160404, 160409, 160414, 160403(AGENT_PROHIBITED) | Idempotency-Key；If-Match |
| D5 | GET `/delegations/{delegation_id}/uses` | delegations | O · `policy_read` | query `used_at` 范围 | `[{decision_id, code, decision_action, resource_kind, resource_id, used_at}]` | 160404 | — |
| D6 | GET `/takeovers` | tenants | P · `policy_read` | — | `Takeover[]` | — | — |
| D7 | POST `/takeovers` | tenants | P · `takeover`；HUMAN；step-up `project_takeover` | `{reason*, reauth_proof*}` | `{takeover, revoked_delegation_ids[], mode_change_id?, previous_mode}`（201） | 160403, 160403(AGENT_PROHIBITED), 160403(REAUTH_REQUIRED), 160461；07 错误透传于 `data.upstream_code` | Idempotency-Key |

`Delegation` 表示：§3.8.3 全部列（`active_key` 除外）+ `grantee{name, avatar, agent_roles[]}`、`grantor{name}`、`project{name, code, team_mode}`、`effective_state`、`remaining_seconds`。

### 6.3b 编制授权（D110）

| # | 方法 路径 | tag | 守卫 · 权限 | 请求 | 响应 `data` | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| SG1 | GET `/spawn-grants` | tenants | T · `policy_read` | query：`state` `grantee_agent_id` `project_id`(contains) `approval_mode` `ends_at`(lt/gt)；排序 `ends_at` `created_at` | `SpawnGrant[]` | — | — |
| SG2 | POST `/spawn-grants` | tenants | T · `spawn_grant_manage`；HUMAN；step-up `spawn_grant_manage` | `{grantee_agent_id*, project_ids*[], max_active_agents*, max_spawns_per_day*, allowed_roles*[], allowed_machine_ids*[], allowed_models*[], max_effort*, budget_cap_micros*, per_agent_cap_micros*, agent_ttl_hours*, idle_retire_hours*, approval_mode*, duration_hours*, reason*, replaces_grant_id?, reauth_proof*}` | `SpawnGrant`（201）+ `ETag` | 160400, 160403(AGENT_PROHIBITED / REAUTH_REQUIRED / MISSING_PERMISSION), 160470, 160473, 160475 | Idempotency-Key（176425 / 176409） |
| SG3 | GET `/spawn-grants/{spawn_grant_id}` | spawn-grants | O · `policy_read` | — | `SpawnGrant` + `ETag` | 160404 | — |
| SG4 | PATCH `/spawn-grants/{spawn_grant_id}` | spawn-grants | O · `spawn_grant_manage`；HUMAN；放宽时 step-up | 可选：上述上限 / 范围字段、`approval_mode`、`ends_at`、`reauth_proof?` | `SpawnGrant` | 160403, 160409, 160428, 160470, 160474, 160475, 160476 | If-Match |
| SG5 | POST `/spawn-grants/{spawn_grant_id}/revocations` | spawn-grants | O · `spawn_grant_manage`；HUMAN | `{reason*}` | `SpawnGrant`（REVOKED） | 160403, 160404, 160409, 160474 | Idempotency-Key；If-Match |
| SG6 | GET `/spawn-grants/{spawn_grant_id}/members` | spawn-grants | O · `policy_read` | query `state` | `[{agent{id, name, avatar, roles[], state}, state, release_reason?, spent_micros, per_agent_cap_micros, reserved_at, released_at?}]` | 160404 | — |

`SpawnGrant` 表示：§3.8.13 全部列（`active_key` 除外）+ `grantee{agent_id, name, avatar}`、`granted_by{name}`、`projects[{id, name, team_mode}]`、`effective_state`、`effective_approval_mode`、`remaining_seconds`、`budget_pct`、`quota{active:"x/y", today:"x/y"}`。

### 6.4 升级规则与事件

| # | 方法 路径 | tag | 守卫 · 权限 | 请求 | 响应 | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|---|---|
| E1 | GET `/escalation-rules` | tenants | T · `policy_read` | query `project_id`（缺省 = 团队默认）、`kind` | `Rule[]`（含 `locked`、`locked_reason`、`dependent_autonomous_project_ids[]`） | — | — |
| E2 | PUT `/escalation-rules/{rule_id}` | escalation-rules | O · `policy_manage`；HUMAN；放宽类 step-up `escalation_rule_relax` | `{enabled*, params*, actions_autonomous*, actions_hybrid*, reauth_proof?}` | `Rule` | 160400, 160430, 160431, 160409, 160403(REAUTH_REQUIRED) | If-Match |
| E3 | GET `/escalation-incidents` | tenants | T · `policy_read` | query `project_id` `state` `rule_kind` `severity` `subject_id` `created_at`；排序 `severity desc, last_triggered_at desc` | `Incident[]` | — | — |
| E4 | GET `/escalation-incidents/{incident_id}` | escalation-incidents | O · `policy_read` | — | `Incident` + `ETag` | 160404 | — |
| E5 | POST `/escalation-incidents/{incident_id}/acknowledgements` | escalation-incidents | O · `policy_read`；HUMAN | `{}` | `Incident` | 160404, 160432 | Idempotency-Key（天然幂等） |
| E6 | POST `/escalation-incidents/{incident_id}/dismissals` | escalation-incidents | O · `policy_manage`；HUMAN | `{reason*}` | `Incident` | 160432, 160433, 160409 | Idempotency-Key；If-Match |

恢复被 hold 的项目不在本模块：前端调用 07 `POST /entities/{entity_id}/project/decisions {action:RESUME, hold_kind:ESCALATION}`；本模块消费 `project.hold_changed` 自动关闭 incident。

### 6.5 设置

| # | 方法 路径 | tag | 守卫 | 请求 | 响应 | 错误 | 锁 |
|---|---|---|---|---|---|---|---|
| S1 | GET `/governance-settings` | tenants | T · `policy_read` | — | §3.8.2 + `ETag` | — | — |
| S2 | PUT `/governance-settings` | tenants | T · `policy_manage`；HUMAN；`sod_human_self_approval→ALLOW_WITH_REASON` 时 step-up `governance_sod_relax` | 全部可写字段 + `reauth_proof?` | 同 S1 | 160400, 160409, 160403(REAUTH_REQUIRED) | If-Match |

### 6.6 业务审计

| # | 方法 路径 | tag | 守卫 | 请求 | 响应 | 错误 |
|---|---|---|---|---|---|---|
| A1 | GET `/business-audit-events` | tenants | T · `audit_read`；行过滤：无 `audit_event_view` 者只见其 `project_read` 可见项目的事件 + 自己作为主体的团队级事件 | query：`project_id` `module` `event_type`(in) `actor_id` `actor_kind` `authority_kind` `authority_ref` `target_type` `target_id` `outcome` `occurred_at`(gte/lt)；排序固定 `occurred_at desc, sequence desc` | `[{audit_event_id, sequence, event_type, module, project_id, actor{kind,id,name}, authority{kind, ref, label}, target{type,id,label?}, outcome, occurred_at}]` + `meta.index_lag_seconds` | 160400 |

明细、完整性、导出：底座 `GET /tenants/{t}/audit/events/{event_id}`、`GET /tenants/{t}/audit/integrity-verifications`（`audit_event_view`）、`GET /tenants/{t}/audit/exports`（`audit_event_export`，NDJSON：manifest → events → completion；链无效 409）。本模块不重写。

### 6.7 我的裁决（收件箱）

| # | 方法 路径 | tag | 守卫 | 请求 | 响应 | 错误 |
|---|---|---|---|---|---|---|
| I1 | GET `/inbox-items` | tenants | M | query `state`（缺省 OPEN）`kind`(in) `project_id` `priority`；排序固定 `priority desc, due_at asc nulls last, created_at asc` | `InboxItem[]`：§3.8.8 + `actions[{key: A\|R\|OPEN\|ACK, label_key, method, href, requires[STRUCTURED_FEEDBACK\|REASON\|STEP_UP], disabled_reason?}]`、`project{name}`、`preview{…}`（按 kind：验证 n/n、覆盖、风险、成本、工具名、过期倒计时） | — |
| I2 | GET `/inbox-counts` | tenants | M | — | `{total, by_kind{}, by_priority{}, revision}` | — |
| I3 | GET `/inbox-items/{inbox_item_id}` | tenants | M（服务内校验 recipient=本人，否则 404） | — | `InboxItem` | 160404 |
| I4 | POST `/inbox-items/{inbox_item_id}/acknowledgements` | tenants | M | `{}` | `InboxItem`（仅 CLARIFICATION_REPLY / ESCALATION_ALERT；后者同时执行 E5） | 160404, 160441, 160400（类型不支持） |

`actions[].href` 指向源模块接口（02 `POST /work-items/{id}/decisions`、03 `PUT /interactions/{id}/decision`、03 `PUT /verifications/{id}/result`、06 记忆审批、05 派单提案、07 交付决定）；收件箱只做导航、预览与预判（`disabled_reason` 来自 Check，提交时源模块再判）。

### 6.8 通知

| # | 方法 路径 | tag | 守卫 | 请求 | 响应 | 错误 | 锁 |
|---|---|---|---|---|---|---|---|
| N1 | —（移交 09） | — | — | 站内通知列表由 09 C5 收件 HTTP 提供 | — | — | — |
| N2 | —（移交 09） | — | — | 已读标记由 09 C5 提供 | — | — | — |
| N3 | GET `/notification-preferences` | tenants | M | — | `{defaults, types[{type_key, mandatory, priority, in_app, browser_push, email, catalog_defaults}]}` + `ETag` | — | — |
| N4 | PUT `/notification-preferences` | tenants | M | 同 N3（只传修改行） | 同 N3 | 160450, 160400, 160409 | If-Match |
| N5 | GET `/notification-types` | tenants | TM | — | §9.2 目录（只读） | — | — |

浏览器推送订阅的登记与投递属 09；本模块只决定是否推送。

### 6.9 总览与危险操作登记

| # | 方法 路径 | tag | 守卫 | 请求 | 响应 |
|---|---|---|---|---|---|
| O1 | GET `/overview` | tenants | TM（各节按 IAM 数据范围过滤） | query `sections`（逗号分隔，缺省全部）、`window`（7d/30d，缺省 30d） | `{sections:{kpis, closure, decision_queue, team_load, machines, projects, activity}}`，每节 `{status: OK\|ERROR\|FORBIDDEN, generated_at, data?, error_code?, missing_permission?}` |

**危险操作登记表**（G6 返回同一数据）。step-up 一律用底座证明：发起后 300s 内有效、一次性、purpose + target 绑定、仅 Cookie 会话。`max_age=300s`，不另设更短值（本规格提议：底座以发起时刻计 TTL，业务侧另建计时会形成第二套时钟）。

| op_key | 所有者 | 接口 | purpose | target | 附加确认 |
|---|---|---|---|---|---|
| delegation.grant | 08 | D2 | `delegation_grant` | `project:<id>:grantee:<principal>` | 理由 |
| spawn_grant.manage | 08 | SG2 / SG4（放宽） | `spawn_grant_manage` | `agent:<grantee_agent_id>` | 配额 / 预算 / 审批方式摘要；团队级范围二次确认（D110） |
| project.takeover | 08 | D7 | `project_takeover` | `project:<id>` | 理由 |
| escalation_rule.relax | 08 | E2 | `escalation_rule_relax` | `rule:<id>` | 后果说明 |
| governance.sod_relax | 08 | S2 | `governance_sod_relax` | `tenant:<id>` | 后果说明 |
| project.mode_autonomous | 07 | mode-changes | `team_mode_change` | `project:<id>` | P1–P5 清单 |
| project.cancel | 07 | stage S12 | `project_cancel` | `project:<id>` | 输入项目代号 |
| delivery.send | 07 | DELIVER | `delivery_send` | `delivery:<id>` | 清单 ①–⑧ |
| requirement.scope_change | 02 | R10 | `scope_change` | `requirement:<id>` | 影响列表 |
| intake.convert_privileged | 01 | 转换（NEW_PROJECT / APPEND） | `intake_convert_privileged` | `intake:<id>` | 「不可委托」说明；01 校验的该凭证同时覆盖 APPEND 所含的范围变更（02 `AppendFromIntake` 不再要求第二次 step-up，复审 #11） |
| machine.revoke | 04 | A11 | `machine_revoke` | `machine:<id>` | 输入名称 |
| agent.forced_retire | 05 | 强制退役 | `agent_forced_retire` | `agent:<id>` | 输入名称 |
| agent.full_auto | 05 | Agent 授权模式设为全自动 | `agent_full_auto` | `agent:<id>` | 红色语义 |
| acceptance.record | 07 | 登记线下验收 | `acceptance_record` | `delivery:<id>` | 附证明文件 |
| system_event.replay | 09 | 事件重放 | `system_event_replay` | `subscriber:<key>` | 范围说明 |
| execution.full_auto | 03 | 授权模式 | `execution_full_auto` | `execution:<id>` | 红色语义 |
| gateway.key_reveal | 04 | A28 查看网关密钥 | `gateway_key_reveal` | `gateway:<id>` | 红色确认（轮换为内联确认，不需 step-up，04） |
| integration.credentials | 12 | B1 新建 / 修改云效连接 | `integration_credentials` | `tenant:<id>`（新建）/ `integration_connection:<id>` | 令牌只写不回显（CR-12-07） |
| execution.terminal_interactive | 03 | A67 打开交互终端（v1.1，D119） | `terminal_interactive` | `execution:<id>` | 红色说明「全程录制并写入审计」；无沙箱时追加目录风险说明 |
| machine.terminal_enable | 04 | A35 把「允许远程终端」由关改开（D119） | `machine_terminal_enable` | `machine:<id>` | 后果说明；关闭不需 step-up |
| machine.credential_issue | 04 | A32 预签发 SERVER / K8S_POOL 凭证；SERVER 的 A13 轮换（D119） | `machine_credential_issue` | `tenant:<id>`（新建）/ `machine:<id>`（轮换） | 「凭证只显示这一次」 |
| agent.full_reset | 05 | F21 `{kind:FULL}` 完全重置 Agent（D120） | `agent_full_reset` | `agent:<id>` | 输入 Agent 名称；后果清单（私有目录清空、会话丢失、长期记忆不受影响） |
| tool.credential_manage | 05 | T08 设置 / 轮换团队工具目录 MCP 凭据（D120） | `tool_credential_manage` | `tool_entry:<id>` | 「凭据只写不回显」 |

上述 purpose 由各所属模块在启动时经底座 `authn.RegisterReauthenticationPurpose` 登记（00 §13 F4）。**本表是唯一登记源**：各模块规格的 purpose / target 必须与本表一致，冲突时以本表为准回推修订。step-up 失败统一返回调用方段 `xx0403` + `data.reason_code=REAUTH_REQUIRED`。

### 6.10 收件箱投影映射（`InboxItemKind`）

| kind | 创建事件 | 接收人 | 优先级 | 关闭事件 | A / R |
|---|---|---|---|---|---|
| `WORK_ITEM_REVIEW` | `work_item.state_changed{to:NEEDS_HUMAN_REVIEW}`（source_key 含 claim_id）；**补投影**：`project.mode_changed{to:HYBRID}` / `takeover.executed` 时对项目内全部 NEEDS_HUMAN_REVIEW 工作项按 `source_key` 幂等建项（BR-08-49） | 设置 `REVIEWER_ELSE_ELIGIBLE`：HUMAN REVIEWER 分派人；否则 `ELIGIBLE(decision_approve)` 人类。AUTONOMOUS 项目仅在 `require_human_decision`、有 hold 类 incident、NO_ELIGIBLE（含 Agent 评审超时）时为人类建项 | 风险 HIGH → HIGH，否则 NORMAL | `decision.made`（该工作项评审类 / CANCEL）、状态离开 NEEDS_HUMAN_REVIEW | A 通过 / R 拒绝（结构化反馈） |
| `INTERACTION`（subkind TOOL_PERMISSION / RUNTIME_INPUT） | `interaction.requested` | `assignee_principal_id`（03 已解析） | HIGH | `interaction.decided` / `interaction.closed` → EXPIRED | A 允许一次 / R 拒绝；RUNTIME_INPUT 的 A = 打开输入表单 |
| `HUMAN_VERIFICATION` | `verification.human_requested` | 03 eligible 非执行者 | NORMAL | `verification.completed` | A 打开记录表单 / R — |
| `CLARIFICATION_REPLY`（subkind ANSWERED / NEEDS_MAPPING / CLIENT_SILENT） | `intake.clarification_answered` / `intake.clarification_expired` | 发起该轮者 + 最近处理人 | HIGH | `intake.triaged`(新 rev) / `converted` / `declined` / `merged`、或本人确认 | A 打开来件 / R —；ACK 确认 |
| `DELIVERY_CONFIRM` | `delivery.confirmation_requested`（`release_notes_status=NEEDS_HUMAN` 时标「需人工撰写」） | `HOLDERS(delivery_send)` 项目内人类 | HIGH | `delivery.ready`、`delivery.withdrawn`、`delivery.sent`（复审 #19） | A 打开交付确认（step-up）/ —（无退回动作；撤回在 07 交付页操作） |
| `ESCALATION_ALERT` | `escalation.triggered` | `OWNERS` + `LEADS(p)` | 按严重度 | incident 任一人 ACK / RESOLVED / DISMISSED | A 确认 / R —；附〔接管〕〔恢复项目〕 |
| `MEMORY_RULE_APPROVAL` | `memory.written{kind:RULE, state:PENDING_REVIEW}` | `ELIGIBLE(memory_approve)` 人类，**排除该记忆的写入者**（06 BR：写入者不可自批） | NORMAL | `memory.approved` / `memory.rejected` / `memory.revoked`（含过期、被取代） | A 批准 / R 驳回（理由） |
| `DISPATCH_PROPOSAL` | `dispatch.proposed{requires_human:true}`（MANUAL 策略，或 AUTO 下被护栏 G1–G9 拦截的提案；05 在载荷中给出 `policy`、`requires_human`） | `HOLDERS(work_item_assign)` 项目内人类 | NORMAL | `dispatch.applied` / `dispatch.rejected` / `dispatch.expired`（任何结局）/ `work_item.assigned` | A 采纳 / R 驳回（理由；调用 05 提案接口） |
| `INTAKE_TRIAGE` | `intake.received` 为返工来件（`origin_acceptance_id` 非空）或变更申请（`kind=CHANGE`，D117）；`intake.triaged` 且分诊卡建议 NEW_PROJECT / APPEND（不可委托）；AUTONOMOUS 下协调员无有效委托；`intake.client_approval_decided{decision:APPROVED\|REJECTED}`（甲方已决定，source_key 含 round，D117） | `HOLDERS(intake_convert)` 人类（团队级，`project_id` 可空） | 返工来件与甲方已决定 HIGH，否则 NORMAL | `intake.converted` / `intake.merged` / `intake.declined`；`intake.client_approval_requested`（转为等待甲方，关闭当前项） | A 打开来件转换 / R 拒绝（理由） |
| `EXECUTION_ATTENTION`（subkind INTERRUPTED / EXECUTION_FAILED） | `execution.state_changed{to:INTERRUPTED}`；`work_item.state_changed{to:EXECUTION_FAILED}` | 人类执行者（交互执行）；Agent 执行 → `LEADS(p)` 人类 | HIGH | `execution.state_changed` 离开 INTERRUPTED（恢复 / 失败）、`work_item.state_changed` 离开 EXECUTION_FAILED | A 打开执行（恢复 / 重试）/ R 标记失败 |
| `ENSEMBLE_APPROVAL`（v1.1） | `ensemble.plan_created{requires_approval:true}`（超出预算上限需人批准；复审 #22） | `HOLDERS(work_item_assign)` 项目内人类 | NORMAL | `ensemble.started` / `ensemble.cancelled`（含 24h 过期） | A 批准 / R 拒绝 |
| `ENSEMBLE_SELECTION`（v1.1） | `ensemble.compared{requires_human:true}` | `LEADS(p)` 人类 | HIGH | `ensemble.selected` / `ensemble.cancelled` | A 打开对比报告择优 / R — |
| `AGENT_SPAWN_APPROVAL`（D110） | 05 `agent.spawn_requested`（`effective_approval_mode=REQUIRE_APPROVAL`；载荷含 `spawn_request_id`、`spawn_grant_id`、`supervisor_agent_id`、请求事实） | `HOLDERS(agent_manage)` 人类（授权有项目范围时另须对任一范围项目 `project_read`） | NORMAL | 05 `agent.spawned`（同 `spawn_request_id`）/ `agent.spawn_rejected` / `agent.spawn_expired`；授权撤销 / 耗尽 → WITHDRAWN | A 批准 / R 驳回（理由必填）；均调用 05 建 Agent 请求接口 |
| `PLAN_CONFIRMATION`（D111） | 10 `plan.submitted` | 该计划的确认人（成员上级；Agent 成员 = 其 `owner_principal_id`），仅人类 | NORMAL | 10 `plan.confirmed` / `plan.returned` | A 确认 / R 退回（意见必填）；调用 10 计划确认接口 |
| `REPORT_CONFIRMATION`（D111） | 10 `report.submitted` | 该报告的确认人（同上），仅人类；载荷含 flags（未核对 n/m、未确认、待佐证） | NORMAL | 10 `report.confirmed` / `report.returned` | A 确认 / R 退回（意见必填）；调用 10 报告确认接口 |
| `REPORT_BLOCKED`（D111） | 10 `report.checked{result:BLOCKED}` | 报告作者（人类作者；Agent 作者时同时给其 `owner_principal_id`） | HIGH | 后续 `report.checked{result:PASS}` 或 `report.submitted` | 打开报告编辑器（定位首个阻断项） |
| `REPORT_OVERDUE`（D111） | 10 `report.overdue` | 作者 + 其上级（人类） | HIGH | 10 `report.submitted` | 打开报告编辑器 |

---

### 6.10b D112 / D113 收件箱类型（追加到 §6.10 表）

| kind | 创建事件 | 接收人 | 优先级 | 关闭事件 | A / R |
|---|---|---|---|---|---|
| `ESCALATION_INTERNAL_EXHAUSTED` | 02 `work_item.escalated{reason:INTERNAL_ROUNDS_EXHAUSTED}` | 工作项负责人（`accountable_principal_id`）+ `LEADS(p)` 人类 | HIGH | 工作项离开 REWORK / 进入 NEEDS_HUMAN_REVIEW / 取消 | 打开工作项（显示各轮内部反馈） |
| `REQUIREMENT_CONFLICT` | 02 `work_item.escalated{reason:REQUIREMENT_CONFLICT}` | `LEADS(p)` 人类 | HIGH | `requirement.versioned` 或升级被 DISMISSED | A 打开需求 / R 驳回冲突判定（理由） |
| `ANDON` | 02 `andon.pulled` | 负责人 + `LEADS(p)` 人类 | CRITICAL | 02 `andon.cleared` | A 打开处置 |
| `EXCEPTION_DECISION` | 02 / 07 `exception.raised` | `HOLDERS(exception_decide)` 人类 | HIGH | `exception.decided` | A 打开例外页 |
| `PLAN_APPROVAL` | 02 `plan.proposed{requires_human:true}`（HIGH 风险或无可用跨家族评审 Agent） | `ELIGIBLE(plan_approve)` 人类，排除计划作者与派出它的主管的负责人以外者不限 | NORMAL | `plan.approved` / `plan.rejected` / 工作项取消 | A 批准 / R 驳回（理由） |
| `QUALITY_ESCAPE` | 本模块 BR-08-63 记录建立 | 所有者 + `LEADS(p)` | NORMAL | `learning_state` ≠ PENDING | A 确认学习 / R 驳回 |
| `REVIEW_SHEET`（12，CR-12-07） | 12 `review_sheet.opened` | 被邀请的人类评审人 | NORMAL | 本人已提交意见、`review_sheet.closed` | A 打开评审单 / R — |
| `WORKLOG_APPROVAL`（12，CR-12-07） | 12 `worklog.submitted` | 持 `worklog_approve` 的人类审批人（不含提交人本人） | NORMAL | `worklog.approved`、`worklog.returned`、`worklog.withdrawn` | A 打开工时审批 / R 退回（理由必填） |


**D119 收件箱类型**（追加到 §6.10 表；共计数随之 +1）：

| kind | 创建事件 | 接收人 | 优先级 | 关闭事件 | A / R |
|---|---|---|---|---|---|
| `PREVIEW_SHARE_APPROVAL`（03，分期待定） | 03 `share_link.requested` | `HOLDERS(preview_share_approve)` 项目内人类，**排除申请人** | NORMAL | 03 `share_link.decided`、`share_link.ended` | A 批准（调用 03 A72 APPROVE，成功后在收件箱内一次性显示链接）/ R 驳回（理由必填） |

明确**不投影**：`internal_qa.failed`、内部阶段的 `verification.completed`、`work_item.state_changed{to:REWORK, cause:INTERNAL_QA}`（BR-08-60）。

### 6.11 D112 / D113 增补接口

| # | 方法 | 路径 | tag | 权限 · 守卫 | 说明 |
|---|---|---|---|---|---|
| Q1 | GET | `/tenants/{tenant_id}/quality-escapes` | tenants | `audit_read` · T | 过滤 `project_id`、`stage`、`learning_state`、`category`；排序 `created_at` |
| Q2 | GET | `/quality-escapes/{escape_id}` | quality-escapes | `audit_read` · O | 详情含 `passed_by` 与记忆 / 评测草稿 |
| Q3 | PUT | `/quality-escapes/{escape_id}/learning` | quality-escapes | `policy_manage` 或项目 `delivery_lead` · O；HUMAN | `{state:"RECORDED"\|"DISMISSED", reason?}` + If-Match；409 `160481` |

## 7. 后台任务与事件

### 7.1 任务（09 `JobQueue`；周期任务互斥 `dlock`，锁名 `governance:<job>[:tenant]`，TTL = 2×周期；每批 ≤500 行，单行失败不影响其他行）

| 任务 | 触发 / 频率 | 处理 | 重试 / 失败 |
|---|---|---|---|
| `governance.delegation_expiry` | 每 30s | `state=ACTIVE AND ends_at<=now` → 逐行条件更新为 EXPIRED、删除残留绑定、审计、outbox | 下周期重做（条件更新幂等） |
| `governance.delegation_expiring_notice` | 每 15 min | `ends_at` 在 72h 内且 `expiring_notified_at IS NULL` → 通知，置标记 | — |
| `governance.delegation_reconcile` | 每 5 min（每租户一锁）；`agent.state_changed` 即时触发 | BR-08-23 | — |
| `governance.escalation_evaluator` | outbox 消费（§7.3）；每 60s 扫描「NEEDS_HUMAN_REVIEW ∧ AUTONOMOUS ∧ 无 incident」补评 NO_ELIGIBLE，并扫描 INTERRUPTED / EXECUTION_FAILED 超 `stall_minutes` 者评估 EXECUTION_STALLED | §3.9.2，与动作同事务；处理 `claim.submitted` 时同事务写 `governance_claim_evaluations`；消费去重经 09 `domain_event_receipts`（Transactional） | 09 退避与停放（09 C2），停放后 `job.dead_lettered` + 本模块 CRITICAL 通知 |
| `governance.inbox_projector` | outbox 消费，每租户有序 | §6.10；写行后推 `tenant/{t}/inbox/{principal}` | 同上 |
| `governance.inbox_reconcile` | 每 60s | 对 `created_at < now−30s` 的 OPEN 项按源批量读取（01/02/03/05/06/07 读取接口）；源已关闭 → RESOLVED/WITHDRAWN/EXPIRED 并推送；资格复核（BR-08-36） | — |
| `governance.notification_router` | outbox 消费 | §9：解析接收人与类型，调用 09 `Notifier.Send`（携带 `merge_key`、`dedupe_key`、`priority`、模板与链接）；偏好、免打扰、合并窗口与摘要由 09 在投递时经 `PreferenceResolver` 读取本模块偏好执行 | 同上 |
| `governance.audit_indexer` | 每 2s（每租户游标） | `EventStreamReader(tenant, last_sequence, 500)` → 写索引（project / authority 取 Labels，缺省按 target 反查 02/07）→ 同事务更新游标 → 推 `tenant/{t}/activity` | 游标保证不漏不重 |
| `governance.seed_reconcile` | 启动 + 每 1h | §3.3 | — |
| `governance.spawn_grant_expiry` | 每 30s | `state∈{ACTIVE,EXHAUSTED} AND ends_at<=now` → EXPIRED、ACTIVE 成员 RELEASED(GRANT_ENDED)、审计、outbox | 条件更新幂等，下周期重做 |
| `governance.spawn_grant_day_reset` | 每日 00:00:30 UTC（`Reserve` 另做惰性归零） | `spawned_day <> today` → `spawned_today=0`、`spawned_day=today` | — |
| `governance.spawn_grant_expiring_notice` | 每 15 min | `ends_at` 在 72h 内且 `expiring_notified_at IS NULL` → 通知授予人，置标记 | — |
| `governance.spawn_cost_accrual` | outbox 消费 `execution.cost_updated`（Transactional） | BR-08-55 | 09 退避；停放后告警 |
| `governance.spawn_grant_reconcile` | 每 5 min（每租户一锁）；`agent.*` 事件即时 | 以成员表 + 05 `AgentDirectory` 重算 `active_count`；主管 Agent 非 ACTIVE / 失去 LEAD → REVOKED(`GRANTEE_INACTIVE`) | — |

闭环完整率（07 BR-07-49）：总览请求时实时计算最近 30 天（终态工作项数来自 02 `WorkItemReader` 计数，审计侧来自索引中 `delivery.decision_approved` / `delivery.decision_cancelled` 去重 target 数），服务端缓存 5 min；不建表（本规格提议：数据量小，避免第二份快照）。

### 7.2 发布的领域事件（outbox，与业务同事务；外壳 00 §7.1）

| 事件 | 时机 | `data` |
|---|---|---|
| `delegation.granted` | D2 | `{delegation_id, project_id, grantee_principal_id, grantee_agent_id, actions[], max_risk, cost_cap_micros, ends_at}` |
| `delegation.revoked` | 手工撤销 / 接管 / 模式切换 / 项目终态 / 对账 / 替换 | `{delegation_id, project_id, grantee_principal_id, end_reason, ended_at}` |
| `delegation.expired` | 到期任务 | `{delegation_id, project_id, grantee_principal_id, ended_at}`（消费方 02 / 05 / 07 对 `delegation.expired` 与 `delegation.revoked` 做相同处理） |
| `escalation.triggered` | incident 创建 | `{incident_id, project_id, rule_kind, severity, subject{kind,id}, actions_applied[], hold_delegations}` |
| `escalation.resolved` | RESOLVED / DISMISSED | `{incident_id, project_id, rule_kind, resolution}` |
| `takeover.executed` | D7 | `{takeover_id, project_id, revoked_delegation_ids[], previous_mode, revoked_spawn_grant_ids[]}` |
| `spawn_grant.granted` | SG2 | `{spawn_grant_id, grantee_agent_id, project_ids[], max_active_agents, max_spawns_per_day, allowed_roles[], budget_cap_micros, per_agent_cap_micros, approval_mode, ends_at}` |
| `spawn_grant.updated` | SG4 / 审批方式折算（BR-08-51）/ 单 Agent 预算触顶（BR-08-55） | `{spawn_grant_id, changed_fields[], effective_approval_mode, state, member_cap_reached?:agent_id}` |
| `spawn_grant.exhausted` | 成本达上限 | `{spawn_grant_id, grantee_agent_id, spent_micros, budget_cap_micros}` |
| `spawn_grant.revoked` | SG5 / 接管 / 对账 / 替换 | `{spawn_grant_id, grantee_agent_id, end_reason, ended_at}` |
| `spawn_grant.expired` | 到期任务 | `{spawn_grant_id, grantee_agent_id, ended_at}` |

### 7.3 消费的事件

| 事件 | 来源 | 用途 |
|---|---|---|
| `work_item.state_changed` | 02 | WORK_ITEM_REVIEW 建 / 关；NO_ELIGIBLE；incident 自动关闭 |
| `work_item.escalated` | 02 | REWORK_LIMIT / SPINNING / INCONCLUSIVE / REVIEW_SLA incident |
| `decision.made` | 02 | 委托使用投影；收件箱关闭；通知 |
| `claim.submitted` | 02 | LOW_SELF_CONFIDENCE、SENSITIVE_PATH |
| `work_item.assigned`、`requirement.versioned` | 02 | 通知；DISPATCH_PROPOSAL 关闭 |
| `work_item.state_changed{to:EXECUTION_FAILED}`、`execution.state_changed{to:INTERRUPTED}` | 02 / 03 | EXECUTION_ATTENTION；EXECUTION_STALLED |
| `interaction.requested` / `.decided` / `.closed` | 03 | INTERACTION |
| `verification.completed`、`verification.human_requested`、`execution.state_changed` | 03 | HUMAN_VERIFICATION；EXECUTION_ATTENTION；通知 |
| `intake.*`（received、triaged、clarification_answered、clarification_expired、converted、declined、merged、client_approval_requested、client_approval_decided） | 01 | CLARIFICATION_REPLY；INTAKE_TRIAGE；CLIENT_REJECTION incident 自动关闭（返工来件终态）；通知 |
| `machine.state_changed` | 04 | 通知；总览 |
| `agent.state_changed`、`agent.retired`、`dispatch.proposed` / `.applied` / `.rejected` / `.expired` | 05 | 委托对账；DISPATCH_PROPOSAL；通知 |
| `ensemble.plan_created` / `.started` / `.compared` / `.selected` / `.cancelled`（v1.1） | 05 | ENSEMBLE_APPROVAL / ENSEMBLE_SELECTION |
| `memory.written` / `.approved` / `.rejected` / `.revoked` | 06 | MEMORY_RULE_APPROVAL |
| `project.created` | 07 | 复制项目升级规则 |
| `project.mode_changed{to:HYBRID}`、`project.stage_changed{to:CLOSED\|CANCELLED}` | 07 | 撤销项目委托（MODE_CHANGED / PROJECT_CLOSED）；切回 HYBRID 时补投影 WORK_ITEM_REVIEW（BR-08-49） |
| `project.hold_changed` | 07 | incident 自动关闭 |
| `budget.threshold_reached` | 07 | BUDGET incident（`once_key` 按 cap_version） |
| `acceptance.received` | 07 | CLIENT_REJECTION；通知 |
| `delivery.confirmation_requested`、`delivery.ready`、`delivery.withdrawn`、`delivery.sent` | 07 | DELIVERY_CONFIRM（开 / 关） |
| `execution.cost_updated` | 03 | 编制授权成本累计（BR-08-55；载荷须含 `agent_id`、`cumulative_cost_micros`） |
| `agent.spawn_requested` / `agent.spawned` / `agent.spawn_rejected` / `agent.spawn_expired` / `agent.adopted` / `agent.orphaned` | 05 | AGENT_SPAWN_APPROVAL 开 / 关；收编释放核对；孤儿通知（D110） |

### 7.4 SSE（09 机制）

| 主题 | 事件 | 载荷 |
|---|---|---|
| `tenant/{t}/inbox/{principal}` | `inbox.item_added` / `inbox.item_removed` / `inbox.counts` / `notification.added` | `{item_id?, kind?, state?, resolution?, actor_name?, counts{total, by_kind}, revision}` |
| `tenant/{t}/governance/delegations` | `delegation.changed` | `{delegation_id, state, effective_state, used_count}` |
| `tenant/{t}/governance/spawn_grants` | `spawn_grant.changed` | `{spawn_grant_id, state, effective_state, effective_approval_mode, active_count, spawned_today, spent_micros}`（`policy_read`） |
| `tenant/{t}/governance/incidents` | `incident.changed` | `{incident_id, state, project_id}` |
| `tenant/{t}/activity` | `activity.appended` | 索引行（按订阅者 `project_read` / `audit_read` 过滤） |

### 7.5 业务审计事件目录（00 §7.2；底座哈希链，所属模块在业务事务内追加）

**公共 detail Schema**（全部事件必含，BR-08-42）：

```json
{"type":"object","required":["reason","evidence_refs","authority_source","actor_kind","project_id"],
 "properties":{
  "reason":{"type":["string","null"],"maxLength":2000},
  "evidence_refs":{"type":"array","maxItems":100,"items":{"type":"string","pattern":"^[a-z_]+:[1-9][0-9]{0,18}$"}},
  "authority_source":{"type":"string","pattern":"^(ROLE|DELEGATION|POLICY|CLIENT|SYSTEM):[A-Za-z0-9_.:-]{1,80}$"},
  "actor_kind":{"enum":["HUMAN","AGENT","MACHINE","CLIENT","SYSTEM"]},
  "project_id":{"type":["string","null"],"pattern":"^[1-9][0-9]{0,18}$"},
  "delegation_id":{"type":"string"},"reauth_purpose":{"type":"string"}}}
```

| event_type | 发出 | target_type | 额外必填 detail 键 |
|---|---|---|---|
| `delivery.decision_approved` / `_rejected` / `_changes_requested` / `_paused` / `_resumed` / `_cancelled` / `_assigned` | 02 | work_item / project | `decision_id, from_state, to_state, claim_id?, failure_category?, feedback_count?, attention_ms?` |
| `delivery.decision_denied`（outcome=denied，独立事务） | 02 | work_item | `attempted_action, reason_code, error_code` |
| `delivery.scope_changed` | 02 | requirement | `change_record_id, from_version, to_version, stale_count, cancelled_count` |
| `delivery.delivery_decided` / `delivery.delivery_sent` | 07 | delivery | `delivery_id, item_count, manifest_sha256, channel_kind?` |
| `delivery.acceptance_received` | 07 | delivery | `result, receipt_sha256, source`（actor_kind=CLIENT，authority `CLIENT:<contact>`） |
| `delivery.project_mode_changed` / `_mode_change_denied` | 07 | project | `from_mode, to_mode, trigger(MANUAL\|TAKEOVER)` |
| `intake.intake_converted` / `intake.intake_declined` / `intake.conversion_denied` | 01 | intake | `mode, requirement_id?, decline_reason_code?` |
| `execution.interaction_responded` | 03 | interaction | `interaction_id, execution_id, kind, action` |
| `execution.full_auto_enabled` | 03 | execution | `execution_id, mode` |
| `execution.merged` | 03 | execution | `merge_id, target_branch, merge_commit` |
| `fleet.machine_confirmed` / `fleet.machine_revoked` | 04 | machine | `machine_id, name, interrupted_count?` |
| `fleet.agent_created` / `fleet.agent_retired` | 05 | agent | `agent_id, roles[], retire_mode?, successor_id?` |
| `knowledge.memory_rule_approved` / `_rejected` | 06 | memory | `memory_id, scope, agent_id` |
| `governance.delegation_granted` | 08 | delegation | `delegation_id, grantee_principal_id, actions[], max_risk, cost_cap_micros, max_rework, ends_at` |
| `governance.delegation_revoked` / `governance.delegation_expired` | 08 | delegation | `delegation_id, end_reason, used_count` |
| `governance.takeover_executed` | 08 | project | `takeover_id, revoked_delegation_ids[], previous_mode, revoked_spawn_grant_ids[]` |
| `governance.spawn_grant_granted` / `governance.spawn_grant_updated` | 08 | spawn_grant | `spawn_grant_id, grantee_agent_id, snapshot{max_active_agents, max_spawns_per_day, allowed_roles[], budget_cap_micros, per_agent_cap_micros, approval_mode, project_ids[], ends_at}, changed_fields[]?` |
| `governance.spawn_grant_revoked` / `governance.spawn_grant_exhausted` | 08 | spawn_grant | `spawn_grant_id, end_reason?, spent_micros, active_count` |
| `fleet.agent_spawned` / `fleet.agent_adopted`（05 发出，本表登记） | 05 | agent | `agent_id, origin(AGENT_SPAWNED), supervisor_agent_id, spawn_grant_id, spawn_request_id?, approved_by?`；收编时 `adopted_by`（审计页「AI 创建」筛选依据） |
| `governance.escalation_triggered` / `_acknowledged` / `_resolved` / `_dismissed` | 08 | escalation_incident | `incident_id, rule_kind, severity, actions_applied[]?, resolution?` |
| `governance.escalation_rule_changed` / `governance.settings_changed` | 08 | escalation_rule / tenant | `changed_fields[], before{}, after{}` |
| `governance.role_seed_applied` | 08 | tenant | `manifest_version, roles[{role_key, added_codes[]}]` |
| `execution.preview_started` / `execution.preview_stopped` / `execution.preview_access_denied`（D119） | 03 | preview_session | `session_id, execution_id, kind, origin, port?, commit?, stop_reason?` |
| `execution.workspace_file_downloaded` / `execution.workspace_path_denied` / `execution.workspace_files_edited`（D119） | 03 | execution | `execution_id, path_sha256[]（不含路径原文）, size?, commit?` |
| `execution.terminal_opened` / `execution.terminal_closed`（D119） | 03 | terminal_session | `session_id, execution_id, mode, sandboxed?, recording_sha256?（SHELL 关闭时必填）, close_reason?, reauth_purpose?` |
| `execution.human_takeover_started` / `_released`（D119） | 03 | execution | `execution_id, previous_state` |
| `execution.share_link_requested` / `_approved` / `_rejected` / `_revoked` / `_expired` / `_accessed`（D119） | 03 | share_link | `link_id, preview_session_id, ttl_hours?, ip_prefix?（accessed）` |
| `fleet.machine_created` / `fleet.machine_credential_issued` / `fleet.machine_fingerprint_mismatch`（D119） | 04 | machine | `machine_id, machine_type, credential_origin`（不含秘密） |
| `fleet.machine_access_changed` / `fleet.pool_config_changed`（D119） | 04 | machine | `machine_id, before{}, after{}` |
| `fleet.tunnel_stream_rejected`（D119，安全事件） | 04 | machine | `machine_id, holder_kind, holder_id, kind, code` |
| `fleet.agent_session_reset` / `fleet.agent_full_reset` / `fleet.agent_reset_failed`（D120） | 05 | agent | `agent_id, reset_id, kind, removed_bytes?, failure_code?`；FULL 另含 `reauth_purpose=agent_full_reset` |
| `fleet.agent_workspace_read`（D120） | 05 | agent | `agent_id, path_sha256`（不含路径原文与内容）、`relation(OWNER\|GRANTED)` |
| `fleet.agent_reminder_changed` / `fleet.agent_wake_config_changed`（D120） | 05 | agent | `agent_id, reminder_id?, change, created_by_kind?, wake_mode?`（不含秘密） |
| `fleet.operation_manifest_published`（D120） | 05 | tenant（`tenant_id=0` 系统级） | `manifest_version, sha256, operation_count, supersedes_version?` |
| `fleet.tool_entry_changed` / `fleet.tool_version_published` / `fleet.tool_grant_changed` / `fleet.tool_credential_rotated`（D120） | 05 | tool_entry | `entry_id, entry_key, kind, version_no?, content_sha256?, grant{subject_kind, subject_id?, project_id?}?, before{}?, after{}?`（凭据只记 `key_id` 与末 4 位；`tool_credential_rotated` 含 `reauth_purpose=tool_credential_manage`） |
| `fleet.tool_called` / `fleet.tool_call_denied`（D120，BR-08-68） | 05 | execution | `entry_key, version_no, tool_name, execution_id, agent_id, args_sha256, outcome, duration_ms`；denied 另含 `reason_code` |
| `execution.comment_thread_created` / `execution.comment_thread_resolved` / `execution.comment_thread_reopened` / `execution.comment_converted` / `execution.comment_retracted`（D120） | 03 | comment_thread | `thread_id, execution_id, anchor_kind, visibility, version_ref, target_kind?, target_id?`（不含正文与引文） |
| `planning.loop_contract_changed` / `planning.loop_run_escalated`（D120） | 10 | loop_contract | `loop_contract_id, agent_id, from_state?, to_state?, loop_run_id?, escalation_reason?` |
| `delivery.defect_created_from_comment`（D120） | 12 Part A | work_item | `work_item_id, comment_thread_id, execution_id, anchor_kind, version_ref` |

委托「使用」不单独写事件：使用该委托的裁决事件本身带 `authority_source=DELEGATION:<id>` 与 `delegation_id`，避免同一动作两条记录（与 02「Check 不写库」一致）。工作项事件 `project_id` 取工作项所属项目；团队级事件为 null。

---

### 7.6 D112 / D113 增补任务与事件

- 任务：`governance.accountable_check`（每日 02:00 团队时区，dlock）；`governance.escape_learning_reminder`（每日，PENDING > 3 天提醒一次）。
- 消费：`work_item.escalated`、`andon.pulled` / `andon.cleared`、`exception.raised` / `exception.decided`、`plan.proposed` / `plan.approved` / `plan.rejected`、`quality.escaped`、`quality.escape_learning_proposed`（回填 §3.8.16 展示列）、`internal_qa.passed` / `internal_qa.failed`（仅指标，不投影）；12 `review_sheet.opened` / `review_sheet.closed`、`worklog.submitted` / `worklog.approved` / `worklog.returned` / `worklog.withdrawn`（收件箱 `REVIEW_SHEET` / `WORKLOG_APPROVAL`，CR-12-07）。
- 发布：`quality.escape_recorded{escape_id, work_item_id, stage}`、`quality.escape_learning_decided{state}`。
- 审计：`governance.escape_learning_recorded` / `_dismissed`；`andon_*`、`exception_*`、`plan_*` 的审计由 02 / 07 在各自事务内追加。

## 8. 页面规格

共通：路由前缀 `/t/:tenantID`；骨架屏加载；错误态（可重试，显示 `meta.request_id`）；无权限态（「缺少权限 `<code>`」+「请联系团队所有者」）；离线态（顶部条「已离线，正在重连…」，写按钮禁用，重连后整页重取）；状态色均配图标 + 文字；触控目标 ≥44px；实时计数用单一 `role="status"`；组件 `AuthorityNote`、`ActorAvatar`、`CountdownRing`、`StateBadge` 取自 `packages/ui`（00 §10），`ReauthenticationDialog` 需从 IAM 应用迁入 `packages/ui`（CR-08-09）。

### 8.1 授权与策略 `/t/:tenantID/governance/policy?tab=matrix|delegations|rules|incidents|settings&project_id=`

- **目的**：看清「谁能做什么」，管理委托、升级规则与默认值，一键接管。入口：「治理 › 策略 › 授权与策略」（`policy_read`）；07 项目页「模式与策略」卡〔管理委托〕〔一键接管〕带 `project_id`。
- **页头**：标题、项目选择器（「全部项目」/ 单项目）、选中 AUTONOMOUS 项目时显示 `ModeBadge` 与红色〔一键接管〕。
- **权限矩阵 tab**：行 = 动作（按 PRD 动作分组，组内权限码）；列 = 7 个种子角色 + 自定义角色（首列冻结，横向滚动）。单元格：✅「已授予」/ ⚙「有条件」（hover 显示条件摘要「风险 ∈ LOW」）/ —「无」。行首标：🔒「不可委托」、👤「仅人类」、🤝「可委托」。选中项目时 `agent_reviewer`、`agent_lead` 列下追加「委托中」子行：受托 Agent 头像 + 动作 + `CountdownRing`。列头 → 新标签打开 IAM `/admin/:tenantID/roles`（定位该角色）。顶部「生产访问」条：当前持有人与剩余时间、〔申请生产访问〕跳 IAM `/admin/:tenantID/access-requests`。种子漂移提示（G3 drift 非空）：「engineer 角色比默认少 2 项权限」〔查看〕〔按默认补齐〕（G4，`policy_manage`）。
- **委托 tab**：表列：受托 Agent、项目、动作 chips、条件（类型 / 风险 ≤ / 成本 ≤ $ / 返工 <）、剩余时间（`CountdownRing`；<24h 琥珀、<1h 红）、已用次数、最近使用、授予人、状态。筛选：状态、项目、Agent。行操作：〔撤销〕（`delegation_grant` ∧ 人 ∧ `effective_state=ACTIVE`；对话框只要理由，无 step-up）、〔替换〕（同条件；打开授予对话框预填并带 `replaces_delegation_id`）、〔使用记录〕（抽屉 D5，每条链到裁决）。空态：「还没有委托。委托可让 Agent 评审员在你设定的范围内裁决低风险工作项」〔授予委托〕。
- **授予委托对话框**（红色语义头部）：项目*（可见且非终态）；受托 Agent*（ACTIVE；勾选任一裁决类动作或 `code_merge` 时仅列 REVIEWER，仅 `intake_convert` 时列 COORDINATOR / REVIEWER（BR-08-09）；显示其项目读权限状态）；动作*（复选；不可委托动作以锁形图标列出且不可选，tooltip「此动作不可委托（团队治理硬约束）」；选 `code_merge` 时风险锁定 LOW；`intake_convert` 仅 AUTONOMOUS 项目可选）；工作项类型（多选，空=全部）；最高风险*（LOW / MEDIUM）；单项成本上限*（$，默认值，≤上限）；返工上限（1–3，默认 3）；有效期*（4h / 24h / 3d / 7d / 自定义小时，≤max_days）；理由*（≥10 字，计数器）；固定条件只读说明「必需验证全部通过 · 不裁决自己产出或自己分派的工作项 · 触发升级规则时对该工作项失效」；`AuthorityNote`「你以 团队所有者 身份授予 · 来源 ROLE:team_owner」。〔授予〕：必填有效时可用 → `ReauthenticationDialog(purpose=delegation_grant, target)` → D2。`160413` 内联「该 Agent 在此项目已有委托」〔替换它〕。
- **编制授权 tab（`?tab=spawn-grants`，D110）**：
  - **列表**：主管 Agent（`ActorAvatar` + 名称）、范围（项目 chips / 红色「团队级」标记）、在岗配额「2 / 3」进度条、今日新建「1 / 5」、预算条（`spent / cap`，≥80% 琥珀、100% 红「已耗尽」）、审批方式（「需人批准」/「授权内自动」；被项目模式降级时显示「已降为需人批准 · 项目 X 为混编」）、剩余时间（同委托 tab）、状态。行展开：成员列表（SG6：AI 创建的 Agent、单 Agent 预算条、状态、〔去收编〕链到 05 Agent 详情）。筛选：状态、主管 Agent、项目。空态：「还没有编制授权。授予后，主管 Agent 可在边界内自己创建执行者」+〔授予编制〕（仅可授予者可见）。
  - **授予 / 编辑对话框**（红色语义头部；仅 `spawn_grant_manage` ∧ 人可见）：主管 Agent*（只列 ACTIVE、持 LEAD、人创建或已收编的 Agent）；范围*（项目多选；〔团队级〕开关需二次勾选「我了解它可在任何项目为自己扩编」，并锁定审批方式为需人批准）；同时在岗上限*（1–10，默认 3）；每日新建上限*（1–20，默认 5）；可建角色*（执行者 / 协调员复选；评审员、主管以锁形图标列出不可选，tooltip「AI 不能创建评审或主管 Agent」）；可用机器*（多选，来自 04）；可用模型*（按运行时分组多选，来自模型目录）；effort 上限*；总预算 $*（默认 50）与单 Agent 预算 $*（默认 15，≤ 总预算，即时校验）；Agent 有效期*（小时，默认 168）与闲置退役*（默认 48）；审批方式*（范围含混编项目时「授权内自动」禁用并说明原因）；授权有效期*；理由*（≥10 字）。提交 → step-up → SG2；编辑时仅放宽项需要 step-up，对话框底部列出「本次放宽：…」；收紧低于用量时即时报错（160476）。
  - **撤销**：`spawn_grant_manage` ∧ 人；只要理由，无 step-up；后果说明「已建的 n 个 Agent 不会被退役，将变为待收编状态」。
  - **实时**：订阅 `tenant/{t}/governance/spawn_grants`，配额与预算条 ≤1s 更新。**移动端**：列表变卡片，对话框全屏 Sheet。
- **升级规则 tab**：每 kind 一卡：名称、说明、来源事件、启用开关（锁定时灰显 +「全自治项目 X、Y 依赖此规则」）、参数（`LOW_SELF_CONFIDENCE` 滑块 30–90；`SENSITIVE_PATH` pattern set 列表：key、名称、开关、glob 多行框逐行即时校验 + 测试框「输入路径 → 命中 MIGRATIONS: `**/migrations/**`」）、动作（混编 / 全自治两列复选，硬性动作锁定）。〔保存〕：有修改且校验通过；放宽类先 step-up。项目选择器为单项目时编辑项目规则，否则编辑团队默认（说明「新项目创建时复制此默认」）。
- **升级事件 tab**：列：严重度、规则、项目、主体（链接）、状态、次数、最近触发、处理人；详情抽屉：facts（敏感路径命中、预算数值）、动作执行结果（FAILED 红）、时间线；按钮：〔确认〕（OPEN）、〔恢复项目〕（PROJECT_HOLD 类，调 07，需 `decision_pause` ∧ 人）、〔驳回〕（`policy_manage`；非 PROJECT_HOLD；理由*）、〔一键接管〕。
- **设置 tab**：S1/S2 表单：默认预算提醒阈值（50–95%，注「100% 暂停不可调」）、委托默认 / 最长天数、默认 / 最高成本上限、人类自批策略（ALLOW_WITH_REASON 红色说明 + step-up）、生产访问最长小时、评审收件箱路由。
- **一键接管**：可用：`takeover` ∧ 人 ∧（AUTONOMOUS ∨ 有 ACTIVE 委托）。对话框红色语义；后果清单「将撤销 n 条委托（列名）；项目切回混编；派单改为手动；进行中的执行继续；待裁决项回到人类队列」；理由*；step-up → D7；成功 toast，委托表实时清空。
- **实时**：订阅 delegations / incidents 主题 ≤1s；他人撤销时行淡出 + toast。
- **移动端**：Tabs → 下拉；矩阵改「按角色」卡片（选角色 → 动作清单）；表格变卡片；对话框全屏 Sheet。
- **文案**：写「Agent 评审员」，不写 Runner / Daemon；剩余时间「2 天 3 小时」。

### 8.2 审计 `/t/:tenantID/governance/audit`

- **目的**：可证明的历史。入口：「治理 › 记录 › 审计」（`audit_read`）。
- **布局**：顶部完整性徽标（底座完整性接口：绿「链完整 · 已校验 n 条 · 头 #seq」/ 红「链校验失败」/ 灰「校验中」；客户端缓存 60s，〔重新校验〕；无 `audit_event_view` 时显示「需要审计查看权限」）+ 索引滞后提示（`index_lag_seconds>10` 琥珀「最新事件同步中」）；筛选：时间（今天 / 7 天 / 30 天 / 自定义）、项目、模块、事件类型、主体（人 / Agent）、权限来源类型、结果；〔导出〕。
- **来源筛选（D110）**：「Agent 来源：全部 / 人创建 / AI 创建」；选「AI 创建」时显示 `fleet.agent_spawned`、`fleet.agent_adopted`、`governance.spawn_grant_*` 以及这些 Agent 作为主体的事件，可再按编制授权与主管 Agent 过滤；AI 创建的 Agent 在主体列带「AI 创建 · 由 {supervisor}」角标，已收编者带「已收编」。
- **列表**：时间、`ActorAvatar` + 名称、动作（事件类型 i18n）、对象（链接）、权限来源 chip（「角色 交付负责人」「委托 #8812」「策略 dispatch_auto」「甲方」「系统」）、结果（denied 红）。行展开：底座明细（reason、evidence_refs 链接、`event_hash` 前 8 位可复制、`previous_hash`、sequence）。
- **导出**：可用：`audit_event_export` ∧ 完整性有效；对话框说明「导出租户审计链区间（含完整性完成记录），项目筛选不影响导出范围」→ 流式下载 `.ndjson`；链无效禁用并提示联系所有者。
- **状态**：空态「该范围内没有事件」；错误；无权限。**实时**：`activity.appended` → 顶部「n 条新事件」条，点击插入。**移动端**：筛选进抽屉；卡片列表；导出仅桌面（本规格提议：大文件下载）。

### 8.3 通知中心与通知设置 `/t/:tenantID/settings/notifications`

- **通知中心**：顶栏铃铛（未读徽标）→ 右侧抽屉：按日期分组（类型图标、文案、项目、时间、未读点），点击跳 `link` 并标已读；〔全部已读〕（09 已读接口 up_to）；筛选全部 / 未读 / 项目；底部〔通知设置〕。手机为全屏页。
- **设置页**：全局：免打扰开关 + 起止 + 时区（默认浏览器）、摘要邮件时刻；浏览器推送状态（未授权〔启用浏览器通知〕调 09 订阅；被拒 → 说明如何在浏览器设置开启）。按模块分组的类型表：名称、示例文案、优先级、站内（强制显示锁）、推送、邮件（关闭 / 即时 / 每日摘要；CRITICAL 锁为即时）。〔恢复默认〕。保存 If-Match；冲突提示「设置已在其他设备更改」并重载。移动端单列卡片。

### 8.4 我的裁决 `/t/:tenantID/inbox`

- **目的**：所有需要我处理的事项。入口：侧栏「工作台 › 我的裁决」、顶栏裁决徽标、手机底栏「裁决」。
- **布局（≥1024 双栏）**：左：分类 Tabs（全部 / 工作项评审 / 工具授权与输入 / 人工验证 / 执行异常 / 来件分诊 / 澄清回复 / 交付确认 / 升级告警 / 记忆规则 / 派单提案 / 并行求解〔v1.1〕，带计数）+ 项目筛选 + 列表（优先级标、类型图标、标题、项目、等待时长或过期倒计时）；右：预览（评审 = 完成阶梯、验证 n/n、证据覆盖、风险、成本、diff 摘要、〔在评审台打开〕；交互 = 工具名、已脱敏参数摘要、倒计时；澄清 = 问答摘要；交付 = 编号、项数、收件人；升级 = facts；记忆 = 规则摘要；派单 = 提案理由与候选）+ 动作栏 + `AuthorityNote`。
- **键盘**：`J`/`K` 下 / 上一项；`A` 主动作，**两步**：按 A 后动作栏进入「按 Enter 确认 · Esc 取消」（本规格提议：防误触，仍为单项操作）；`R` 次动作（评审 = 打开 `StructuredFeedbackForm`；交互 = 拒绝；记忆 / 派单 = 驳回需理由）；`Enter`（非确认态）打开源页面；`?` 帮助。输入框聚焦时快捷键失效；不可用时按键弹出 `disabled_reason`（如「你是该工作项的执行者，不能通过」）。
- **无批量**：无复选框、无「全部通过」、无「全部已读」。
- **关注计时**：打开评审 / 交互预览即调用 02 关注会话接口（surface=`INBOX`），提交时携带 `attention_session_ids`。
- **一致性**：订阅 `tenant/{t}/inbox/{me}`；`inbox.item_removed` → 淡出移除（若为焦点项，焦点移到下一项并 toast「已由 Morgan 在其他设备处理」）；本端提交成功乐观移除；源 409 → 移除并 toast「该事项已被处理」；SSE 断开 >5s → 每 10s 拉 I2 修正。
- **状态**：空态「没有待你决定的事项」〔去看总览〕；加载；错误；离线（动作禁用）。
- **移动端**：单栏列表 → 详情页；底部固定动作栏（通过 / 拒绝，高 ≥44px）；不支持滑动手势动作（本规格提议：防误触）。

### 8.5 总览 `/t/:tenantID/overview`

- **目的**：一屏回答：要我决定什么？进展如何？哪里不健康？
- **布局**：1440：第一行 KPI ×5；第二行左 2/3「待你裁决（前 5）」、右 1/3「团队负载」；第三行「项目健康」/「机器」；第四行「近期活动」。1024 两列；768 单列；375 单列且 KPI 横向滑动（每屏 2 个）。

| 磁贴 | 数据源 | 内容 | 刷新 |
|---|---|---|---|
| KPI ×5 | 07 `DeliveryMetricsReader.TeamKPIs(window, filter{可见项目})`（公式以 07 §5.8 为唯一定义） | 每个已接受交付物成本、首次验收率、人工关注分钟、返工周期、证据覆盖率（≥90% 目标线）；值、对上一窗口 Δ、14 点 sparkline；点击 → 07 指标页 | **≤60s**（轮询，服务端缓存 30s）；`KpiTile` 与 07 共用 |
| 闭环完整率 | 本模块 §7.1 | 「闭环完整率 100%」；<100% 红并可展开缺失列表 | 5 min |
| 待你裁决 | I1 `limit=5` | 卡片；评审卡〔通过〕（两步确认）/〔拒绝〕（结构化反馈）；〔查看全部 n 项〕 | SSE `tenant/{t}/inbox/{me}` **≤1s** |
| 团队负载 | 05 `AgentDirectory`（Agent 状态、负载）+ 02 `WorkItemReader.LoadByPrincipal(tenant, principalIDs[])`（人类的进行中 / 待评审计数） | 人 / Agent 两组：进行中、待评审、空闲；Agent 状态点 | SSE 主题 `tenant/{t}/agents` 与 `tenant/{t}/work-items`（2s 防抖），兜底 30s |
| 机器 | 04 `MachineDirectory.Summary(tenant)` | 在线 / 离线 / 排空数、槽位 已用/总、离线前 3 | SSE 主题 `tenant/{t}/machines` **≤1s**，兜底 30s |
| 项目健康 | 07 `ProjectReader.HealthList(tenant, visibleProjectIDs[])`（阶段、模式、预算、健康、风险数）+ 本模块未关闭 incident 数 | 项目、阶段、模式、预算环、健康点、风险数、升级数 | SSE 主题 `tenant/{t}/projects`（≤1s 呈现阶段 / 暂停变化），兜底 30s |
| 近期活动 | 审计索引最近 20 条（可见范围） | 主体、动作、对象、权限来源、相对时间 | SSE `activity.appended` |

- 各节独立状态：`ERROR` 只在该卡显示「暂时无法加载」〔重试〕；`FORBIDDEN` 显示缺少的权限码。
- 顶部横幅：本人可见的未关闭 CRITICAL incident（「项目 X 因甲方拒收已暂停」〔查看〕）。
- 「3 项待你裁决」经 `role="status"` 播报。
- **刷新目标**（对 PRD §12.2「≤1s」的细化）：待你裁决、机器、项目阶段 / 暂停变化 ≤1s（SSE）；KPI 与闭环完整率为聚合指标，≤60s / 5 min。

### 8.6 控制台外壳（delivery 应用全局，本模块拥有）

- **目的**：统一导航、全局入口与裁决徽标；各模块页面只贡献菜单项，不各自实现外壳。路由前缀 `/t/:tenantID`（00 §10）；复用 IAM 应用的认证、`WorkspaceSwitcher`、`LanguageSwitcher`、主题切换与 `UserMenu`。导航为「顶栏域标签 + 当前域的侧栏子导航」两级（D118）；项目是主容器，项目内的工作在项目空间中进行（D118 修订）。
- **顶栏**（左 → 右）：移动端菜单按钮；品牌区（含 `WorkspaceSwitcher`）；**域标签** 工作台 · 会话 · 项目 · 团队 · 治理（顺序固定；`aria-current` 标当前域；会话标签带未读数，数据为 05 C14 合计）；全局搜索框（点击或 `⌘K` / `Ctrl+K` 打开命令面板）；裁决徽标（订阅 `tenant/{t}/inbox/{me}`，数字 = I2 `total`，`role="status"` 播报「n 项待你决定」，点击 → `/inbox`）；通知铃铛（09 未读数）；连接状态点（SSE 已连接 / 重连中 / 断开）；语言 / 主题；用户菜单。
- **侧栏**：只显示当前域的子导航，按 `Group` 分节（无 Group 的项置顶不加标题）；**会话域没有侧栏**，内容区全宽（05 §8.8 三栏）。项目域有两种侧栏（D118 修订，项目是主容器）：**项目外**显示范围为团队的项目域项（项目组合 · 来件 · 交付总览 / 跨项目 需求 · 任务 · 缺陷 · 流水线）；**项目内**（路由带 `:projectID`）显示**项目空间**——范围为项目的全部项，侧栏顶部为项目切换器与〔返回项目组合〕。侧栏与域标签都由 09 C6 模块菜单贡献机制生成（`MenuPage{Key, Domain, Space, Group, Route, PermissionCode, Icon, SortOrder}`）：先按 `Domain` 归入域标签，再按 `Space`（TEAM / PROJECT，即下表「范围」）决定出现在项目外还是项目空间，再在域内按 `Group`、`SortOrder` 排列；按权限码过滤，无权限的项不显示（范围为项目的项按当前项目判定）；**一个域的项全部被过滤时，该域标签也不显示**（菜单可见性不是授权机制，页面自身仍做守卫；直接打开 URL 仍按页面守卫给出无权限态）。

  表中「范围」列：**团队** = 团队级页面，路由不含项目；**项目** = 只在项目空间内渲染，路由形如 `/t/:tenantID/p/:projectID/...`（00 §10 项目内路由），`:projectID` 取当前项目。同一页面既有团队级列表又有项目内版本时，两行分别登记（Key 不同，项目内 Key 加 `p-` 前缀）。

| 域 | 范围 | 分组 | Key | 标签 | 路由 | 权限码 | 贡献模块 |
|---|---|---|---|---|---|---|---|
| 工作台 | 团队 | — | overview | 总览 | `/t/:tenantID/overview` | （租户成员） | 08 |
| 工作台 | 团队 | — | inbox | 我的裁决 | `/t/:tenantID/inbox` | （租户成员） | 08 |
| 工作台 | 团队 | — | my-work | 我的任务 | `/t/:tenantID/my-work`（D118 新页；数据 = 02 W14 `executor_principal_id=eq:<me>`） | `work_item_read` | 02 |
| 工作台 | 团队 | — | my-plans | 我的计划 | `/t/:tenantID/plans/me` | `plan_submit` | 10 |
| 会话 | 团队 | — | chat | 会话 | `/t/:tenantID/chat`（05 §8.8；原 channels 项迁入，旧路由 `/t/:tenantID/channels` 重定向至此） | `channel_read` | 05 |
| 项目 | 团队 | — | projects | 项目组合 | `/t/:tenantID/projects` | `project_read` | 07 |
| 项目 | 团队 | — | intakes | 来件 | `/t/:tenantID/intakes` | `intake_read` | 01 |
| 项目 | 团队 | — | deliveries | 交付总览 | `/t/:tenantID/deliveries`（07 §8.5 团队版） | `delivery_read` | 07 |
| 项目 | 团队 | 跨项目 | requirements | 需求 | `/t/:tenantID/requirements`（D118 新页，团队级列表，页内项目筛选） | `requirement_read` | 02 |
| 项目 | 团队 | 跨项目 | work-items | 任务 | `/t/:tenantID/work-items`（02 W14 团队级，页内项目筛选） | `work_item_read` | 02 |
| 项目 | 团队 | 跨项目 | defects | 缺陷 | `/t/:tenantID/defects`（D118 新页；数据 = 02 W14 `type=in:BUG,INCIDENT` + 12 A9 缺陷详情） | `work_item_read` | 12 Part A |
| 项目 | 团队 | 跨项目 | pipelines | 流水线 | `/t/:tenantID/pipelines`（D118 新页：每个工作项的阶段运行 平台重跑 → 隐藏验收 → 测试 Agent → 产品验收 Agent → 人工评审 → 交付，外加外部 Flow 运行） | `execution_read` | 03 |
| 项目 | 项目 | — | p-overview | 概览 | `/t/:tenantID/p/:projectID`（07 §8.3 项目总览） | `project_read` | 07 |
| 项目 | 项目 | — | p-goals | 目标 | `/t/:tenantID/p/:projectID/goals`（10 §8.1 目标树，固定为本项目的 PROJECT 目标及其子树；团队全树见团队 › 目标树） | `goal_read` | 10 |
| 项目 | 项目 | — | p-channel | 项目频道 | `/t/:tenantID/p/:projectID/channel`（替换式重定向到该项目默认频道 `/t/:tenantID/chat/:channelID`，进入会话域；05 §8.8） | `channel_read` | 05 |
| 项目 | 项目 | 需求 | p-requirements | 需求 | `/t/:tenantID/p/:projectID/requirements`（02 §8.5；团队级列表见上） | `requirement_read` | 02 |
| 项目 | 项目 | 开发 | p-work-items | 任务 | `/t/:tenantID/p/:projectID/work-items`（02 §8.1 / §8.2；团队级列表见上） | `work_item_read` | 02 |
| 项目 | 项目 | 开发 | p-review | 评审台 | `/t/:tenantID/p/:projectID/review`（02 §8.4；团队级 `/t/:tenantID/review`（D5）保留为「我的裁决」评审类项的直达入口，不进菜单） | `work_item_read` | 02 |
| 项目 | 项目 | 测试 | p-test-cases | 用例与测试计划 | `/t/:tenantID/p/:projectID/test-cases`（12 §8.3） | `work_item_read` | 12 Part A |
| 项目 | 项目 | 测试 | p-defects | 缺陷 | `/t/:tenantID/p/:projectID/defects`（同缺陷页，固定本项目；团队级列表见上） | `work_item_read` | 12 Part A |
| 项目 | 项目 | 测试 | p-escapes | 穿透记录 | `/t/:tenantID/p/:projectID/escapes`（§8.7，固定本项目；团队级 `/t/:tenantID/governance/escapes` 保留，供审计员跨项目查看，不进菜单） | `audit_read` | 08 |
| 项目 | 项目 | 流水线 | p-pipelines | 流水线 | `/t/:tenantID/p/:projectID/pipelines`（固定本项目；团队级列表见上） | `execution_read` | 03 |
| 项目 | 项目 | 流水线 | p-executions | 执行 | `/t/:tenantID/p/:projectID/executions`（03 §8.1；「我的执行」`/t/:tenantID/executions?mine=1` 为团队级，不进菜单） | `execution_read` | 03 |
| 项目 | 项目 | 交付 | p-deliveries | 交付 | `/t/:tenantID/p/:projectID/deliveries`（07 §8.5 项目版；团队级「交付总览」见上） | `delivery_read` | 07 |
| 项目 | 项目 | 交付 | p-worklogs | 工时 | `/t/:tenantID/p/:projectID/worklogs`（12 §8.4 汇总，固定本项目；团队级 `/t/:tenantID/worklogs` 保留为个人填报与审批入口，经通知与我的任务到达，不进菜单） | `worklog_submit` | 12 Part A |
| 项目 | 项目 | 领域工作台 | p-video | 视频 | `/t/:tenantID/p/:projectID/video`（11 §8.4；仅当项目模板领域为 VIDEO） | `work_item_read` | 11 |
| 项目 | 项目 | 领域工作台 | p-novel | 小说 | `/t/:tenantID/p/:projectID/novel`（11 §8.5；仅当项目模板领域为 NOVEL） | `work_item_read` | 11 |
| 项目 | 项目 | 领域工作台 | p-team-tasks | 团队任务 | `/t/:tenantID/p/:projectID/team-tasks`（11 §8.6；仅当项目模板领域为 TEAM_TASKS） | `work_item_read` | 11 |
| 项目 | 项目 | 设置 | p-settings | 项目设置 | `/t/:tenantID/p/:projectID/settings`（07 §8.4，含 11 §8.3 模板与 12 项目流转 `/t/:tenantID/p/:projectID/settings/workflows`） | `project_read` | 07 |
| 团队 | 团队 | 目标与报告 | goals | 目标树 | `/t/:tenantID/goals`（10 §8.1，团队 → 项目 → 成员全树） | `goal_read` | 10 |
| 团队 | 团队 | 目标与报告 | reports | 报告中心 | `/t/:tenantID/reports`（10 §8.5 / §8.6） | `report_read` | 10 |
| 团队 | 团队 | 团队资产 | members | 成员与 Agent | `/t/:tenantID/members` | `agent_read` | 05 |
| 团队 | 团队 | 团队资产 | machines | 机器 | `/t/:tenantID/machines` | `machine_read` | 04 |
| 团队 | 团队 | 团队资产 | knowledge | 知识库 | `/t/:tenantID/knowledge` | `knowledge_read` | 06 |
| 团队 | 团队 | 团队资产 | tool-catalog | 工具与技能 | `/t/:tenantID/tools`（05 §8.z：MCP 服务器 / 技能 两个标签与条目抽屉；无 `tool_catalog_manage` 时只读，D120） | `agent_read` | 05 |
| 团队 | 团队 | 团队资产 | templates | 模板库 | `/t/:tenantID/templates` | `project_read` | 11 |
| 治理 | 团队 | 策略 | policy | 授权与策略 | `/t/:tenantID/governance/policy` | `policy_read` | 08 |
| 治理 | 团队 | 策略 | workflow-config | 流转配置 | `/t/:tenantID/settings/workflows`（无 `workflow_manage` 时只读） | `work_item_read` | 12 Part A |
| 治理 | 团队 | 策略 | agent-operations | Agent 操作清单 | `/t/:tenantID/governance/agent-operations`（05 §8.z：操作清单与「Agent 能力」两个标签，D120） | `agent_read` | 05 |
| 治理 | 团队 | 连接 | integration | 集成 | `/t/:tenantID/settings/integrations/yunxiao`（v1.1） | `integration_manage` | 12 Part B |
| 治理 | 团队 | 连接 | intake-channels | 需求开口 | `/t/:tenantID/settings/intake-channels`（无 `intake_channel_manage` 时只读） | `intake_read` | 01 |
| 治理 | 团队 | 连接 | gateway | API 网关 | `/t/:tenantID/gateway`（实验） | `gateway_manage` | 04 |
| 治理 | 团队 | 记录 | audit | 审计 | `/t/:tenantID/governance/audit` | `audit_read` | 08 |

  表注：① 权限码均取自所属规格的读接口（01 §8.3、02 W14 / R2、03 A01、10 §8、11 L1 / W1–W3、12 A9 / A10 / A21 / A26、B1、08 Q1）；范围为项目的项按当前项目判定（项目范围权限与项目可见性，00 §6）。② 以下数据接口在所属规格中**尚不存在**，本表只登记菜单与路由，不代为定义接口：团队级需求列表（02 只有项目级 R2，跨项目需 02 新增或由前端按可见项目逐个调用 R2）；流水线的按工作项阶段聚合（03 现有 A01 / A04 / A53 与 12 Flow 镜像需组合，聚合读接口待 03 定义）；我的任务、缺陷直接复用 02 W14 过滤，无缺口。③ 标「固定本项目」的项目内路由复用同一页面组件，以路由中的 `:projectID` 作为不可清除的项目过滤（列表接口带 `project_id=eq:`），不新增接口。④ 领域工作台在一个项目空间内至多出现一项，按项目的模板领域（11 模板绑定）决定。
- **当前域与当前项目判定**：二者**只由路由决定**，不另存状态。外壳以「最长路由前缀匹配」把当前路由归到菜单项与域：`/t/:tenantID/p/:projectID/**` → 项目域的项目空间，当前项目 = 路由中的 `:projectID`（如 `/p/:projectID/work-items/:id` → 项目 › 开发 › 任务；`/p/:projectID/executions/:executionID/session` → 项目 › 流水线 › 执行）。**详情页**（工作项、缺陷、执行与交互会话、评审单、内部验收、例外、交付等，路由均带 `/p/:projectID/`）一律在所属项目的项目空间内渲染，从跨项目列表、我的裁决、会话卡片或通知打开时也是如此。`/t/:tenantID/agents/:agentID` → 团队 › 成员与 Agent；`/chat/**` 与旧频道路由归会话域；`/settings/notifications` 归工作台；未进菜单的团队级路由（如 `/t/:tenantID/review`、`/t/:tenantID/governance/escapes`、`/t/:tenantID/worklogs`）归项目域的项目外侧栏，不高亮任何项。无法匹配的路由不改变当前域。路由中的项目本人不可见（无 `project_read` 或已被移出）→ 页面 404 态「项目不存在或你无权访问」并提供〔返回项目组合〕，不泄露项目名。
- **项目空间**：侧栏顶部为**项目切换器**（只列本人 `project_read` 可见的项目，非终态在前，含最近访问 5 个与搜索）与〔返回项目组合〕（→ `/t/:tenantID/projects`）。**切换项目保持子页面**：切换器把路由中的 `:projectID` 换成目标项目并保留当前菜单项（列表页保留与项目无关的查询参数，如视图与排序；详情页 → 目标项目中同一菜单项的列表页，因为详情对象属于原项目）；目标项目没有该项（权限不足，或领域工作台与其模板领域不符）→ 目标项目概览，并以 toast 说明「该项目没有『视频』，已打开概览」。`WorkspaceSwitcher` 的项目选择等同于项目切换器：在项目外选择项目 → 进入该项目概览；在项目空间内选择 → 同上保持子页面；团队切换仍在品牌区，切团队回到工作台总览。
- **跨项目列表**：项目外「跨项目」各页在页内提供项目筛选（只列本人可见项目，默认全部，筛选值写入 URL `project_id=`，属于页面状态而非外壳状态）；〔新建〕类操作要求先选项目，或在项目空间内进行。
- **记住子页面**：每个域记住本人最近访问的子页面（含路由与查询参数；项目域记住的可以是项目外页面，也可以是某个项目空间内的页面），点击域标签回到该页；从未访问、该页已不可见或其项目已不可见 → 该域第一个可见项（项目域 = 项目组合）。记忆保存在浏览器本地存储，读写失败时退回第一个可见项（不影响导航）。
- **移动端**（<768px）：域标签与当前域子导航收入左侧抽屉（底座 Sheet；抽屉顶部为域列表，下方为当前域子导航，项目空间内子导航顶部为项目切换器与〔返回项目组合〕）。**底栏**常驻 4 项（触控 ≥44px）：工作台（回到工作台记住的子页，默认总览）/ 会话（带未读数）/ 项目（回到项目域记住的子页，默认项目组合）/ 裁决（带徽标，→ `/inbox`）。会话在移动端为单栏堆叠（列表 → 消息 → 线程，05 §8.8）。
- **命令面板（`⌘K`）**：底座 `Command` 组件；分组「跳转」（上表全部菜单，按权限过滤，显示为「域 · 标签」，如「项目 · 缺陷」；范围为项目的项对最近 5 个项目展开，显示为「项目名 · 标签」；另列最近 5 个会话，数据 05 C17）、「搜索」（来件、需求、项目、工作项、执行、Agent、机器：各模块列表接口 `q` 参数，每组前 5 条，150ms 防抖）、「动作」：

| 命令 | 权限码 | 去向 |
|---|---|---|
| 新建来件（手工录入） | `intake_triage` | 01 来件新建对话框 |
| 新建项目 | `project_manage` | 07 新建项目向导 |
| 新建工作项 | `work_item_write`（当前项目） | 02 工作项新建 |
| 新建交互会话 | `execution_start` | 03 新建会话对话框 |
| 新建 Agent | `agent_manage` | 05 新建 Agent |
| 新建私信 | （成员） | 05 §8.8 新建私信对话框（C18） |
| 接入机器 | `machine_manage` | 04 接入向导 |
| 授予委托 | `delegation_grant` | §8.1 授予对话框 |
| 打开我的裁决 | （成员） | `/inbox` |
| 切换主题 / 语言 | （成员） | 外壳 |

- **状态**：外壳加载骨架（顶栏域标签与侧栏占位）；租户无权限 → 跳工作区选择；SSE 断开 >5s 显示琥珀连接点并降级轮询（I2 每 10s）。

---

### 8.7 D112 / D113 增补页面

- **总览**：新增「穿透率」「内部一次通过率」磁贴（BR-08-65）；「待你裁决」不显示任何内部质量门失败。
- **质量穿透** `/t/:tenantID/governance/escapes`（入口：项目空间「测试 › 穿透记录」`/t/:tenantID/p/:projectID/escapes`；团队级路由供审计员跨项目查看；`audit_read`，D118 修订）：列表（项目、工作项、发现阶段 人 / 甲方、类别、放行它的内部关口与模型家族、学习状态），详情含记忆草稿与评测用例草稿，人类可「确认学习」或「驳回（理由）」。
- **授权与策略 → 矩阵**新增列：停线、清除停线、例外决定、计划批准、验收集管理、模板、集成；在「计划批准」格对 Agent 显示「策略：跨模型家族、风险 ≤ 中」。
- **我的裁决**分类筛选增加：内部升级、需求冲突、停线、例外决定、计划批准、质量穿透。

## 9. 通知

### 9.1 接收人规则

`ELIGIBLE(code)` = `ListEligible(project, code, facts)` 中的 HUMAN（已排除职责分离命中者）；`HOLDERS(code)` = 对项目（DataScoped）或团队持有该码的 HUMAN（经 IAM `TenantMemberLister` 枚举 ≤500 人 × `CheckBulk`，按 revision 缓存 60s）；`OWNERS` = `HOLDERS(takeover)`；`LEADS(p)` = 在 p 或团队范围持有 `delivery_lead` 角色的 HUMAN；`ACTOR` / `ASSIGNEE` / `GRANTOR` = 事件中对应 HUMAN。Agent 永不接收。各模块规格中的「收件箱」指本模块通知中心，仅 §6.10 列出的 kind 进入「我的裁决」。

### 9.2 规范目录（● = 站内强制；渠道默认 站 / 推 / 邮：即 = 即时，摘 = 每日摘要，— = 关闭）

| type_key | 来源（规格） | 接收人 | 优先级 | 站 推 邮 | 模板键 | 合并 / 去重 |
|---|---|---|---|---|---|---|
| `intake.received` | 01 N1 | 开口 notify 对象 | NORMAL | ✓ — 摘 | `intake.notify.received` | merge `intake_recv:<channel>` 5 min |
| `intake.triaged` | 01 N2 | 同上 | NORMAL | ✓ — — | `intake.notify.triaged` | dedupe 每来件 |
| `intake.triage_overdue` | 01 N3 | 同上 | HIGH | ✓ ✓ 即 | `intake.notify.triage_overdue` | dedupe 每来件 |
| `intake.clarification_answered` ● | 01 N4/N4b | 发起轮次者 + 最近处理人 | HIGH | ✓ ✓ — | `intake.notify.answered` | merge `answered:<intake>` 10 min |
| `intake.client_silent` ● | 01 N5 | 同上 + notify 对象 | HIGH | ✓ ✓ 即 | `intake.notify.clarification_expired` | dedupe 每轮 |
| `intake.closed_message` | 01 N6 | 终态执行人 | NORMAL | ✓ — — | `intake.notify.closed_message` | merge 1h |
| `intake.channel_fault` | 01 N7 | `HOLDERS(intake_channel_manage)` | HIGH | ✓ — 即 | `intake.notify.channel_fault` | dedupe 每开口每原因 24h |
| `intake.link_expiring` | 01 N9 | 签发人 | NORMAL | ✓ — — | `intake.notify.link_expiring` | dedupe 每链接 |
| `intake.client_approval_decided`（D117） | 01 N16 | T15 请求人 + 最近处理人 + `HOLDERS(intake_convert)` | HIGH | ✓ ✓ 即 | `intake.notify.client_approval_decided` | dedupe 每（来件, round） |
| `intake.client_approval_overdue`（D117） | 01 N17 | T15 请求人 + `HOLDERS(intake_convert)` | NORMAL | ✓ — 即 | `intake.notify.client_approval_overdue` | dedupe 每（来件, round, 序号） |
| `work_item.assigned` | 02 N1 | `ASSIGNEE` | NORMAL | ✓ ✓ 摘 | `delivery.notify.assigned` | dedupe `assigned:<assignment>` |
| `work_item.review_ready` ● | 02 N2 | §6.10 WORK_ITEM_REVIEW 接收人 | NORMAL（HIGH 风险 → HIGH） | ✓ ✓ — | `delivery.notify.review_ready` | merge `review:<project>` 5 min |
| `work_item.rework` | 02 N3 | 执行者（人）+ `LEADS` | NORMAL | ✓ — — | `delivery.notify.rework` | dedupe `rework:<decision>` |
| `work_item.lease_expired` | 02 N4 | 原受派人 + `LEADS` | NORMAL | ✓ — — | `delivery.notify.lease_expired` | 每项每小时 ≤1 |
| `work_item.review_overdue` | 02 N6 | 同 review_ready | NORMAL | ✓ ✓ — | `delivery.notify.review_overdue` | 每项每 24h ≤1 |
| `execution.failed_exhausted` | 02 N7 / 03 N3 | 执行者 + `LEADS` | HIGH | ✓ — 即 | `delivery.notify.execution_failed` | dedupe `fail:<execution>`；同工作项 10 min 合并 |
| `requirement.versioned` | 02 N8 | `LEADS` + 受影响执行者 | NORMAL | ✓ — 摘 | `delivery.notify.requirement_versioned` | dedupe `ver:<req>:<v>` |
| `work_item.accepted` | 02 N9 | 执行者（人）或分派人 | NORMAL | ✓ — — | `delivery.notify.accepted` | 同接收人 10 min 合并 |
| `work_item.dependency_cancelled` | 02 N10 | 后继执行者 + `LEADS` | NORMAL | ✓ — — | `delivery.notify.dependency_cancelled` | dedupe |
| `interaction.requested` ● | 03 N1 | `assignee_principal_id` | HIGH | ✓ ✓ — | `execution.notify.interaction_requested` | 每交互 1；同执行 60s 合并 |
| `interaction.expiring` | 03 N1b | 同上 | HIGH | ✓ ✓ — | `execution.notify.interaction_expiring` | 每交互 ≤1 |
| `execution.interrupted` | 03 N2 | 发起者 / 执行者；AUTONOMOUS 加 `LEADS` | HIGH | ✓ — 即（工作时间外仅站内） | `execution.notify.interrupted` | 同机器 10 min 合并 |
| `execution.agent_paused` | 03 N4 | `LEADS` | NORMAL | ✓ — — | `execution.notify.agent_paused` | 每执行 1 |
| `execution.cost_paused` | 03 N5 | `LEADS` + `OWNERS` | HIGH | ✓ — 即 | `execution.notify.cost_paused` | 按项目 30 min 合并 |
| `verification.not_passed` | 03 N6 | 执行者 + REVIEWER | NORMAL | ✓ — — | `execution.notify.verification` | 每 claim 汇总 1 |
| `verification.human_requested` ● | 03 N7 | eligible 非执行者 | NORMAL | ✓ ✓ — | `execution.notify.human_verification` | 每验证 1 |
| `merge.result` | 03 N9 | 发起合并者 | NORMAL | ✓ — — | `execution.notify.merge` | 每合并 1 |
| `machine.offline` | 04 | `HOLDERS(machine_manage)` | HIGH | ✓ ✓ 摘 | `fleet.notify.machine_offline` | 同机器 30 min 合并 |
| `machine.revoked` | 04 N-04-04 | `OWNERS` | HIGH | ✓ — 即 | `fleet.notify.machine_revoked` | 每机器 1 |
| `agent.crashed` | 05 | `HOLDERS(agent_manage)` | HIGH | ✓ ✓ — | `fleet.notify.agent_crashed` | 同 Agent 30 min 合并 |
| `dispatch.proposed` ● | 05（`requires_human=true`：MANUAL 或 AUTO 被护栏拦截） | `HOLDERS(work_item_assign)` 项目内 | NORMAL | ✓ — — | `fleet.notify.dispatch_proposed` | merge 每项目 5 min |
| `dispatch.applied` | 05（AUTO） | `LEADS` | NORMAL | ✓ — 摘 | `fleet.notify.dispatch_applied` | merge 每项目 15 min |
| `channel.mentioned` | 05 collab | 被 @ 的人 | NORMAL | ✓ ✓ — | `collab.notify.mentioned` | 同频道 2 min 合并 |
| `memory.rule_pending` ● | 06 N1 | `ELIGIBLE(memory_approve)` | NORMAL | ✓ — 摘 | `knowledge.notify.rule_pending` | merge 每 scope 30 min |
| `project.joined` | 07 | 被绑定成员 | NORMAL | ✓ — — | `delivery.notify.project_joined` | 每人每项目 1 |
| `project.stage_suggestion` | 07 BR-07-09 | `LEADS` | NORMAL | ✓ — — | `delivery.notify.stage_suggestion` | 同目标 24h 1 |
| `project.stage_changed` | 07 | 项目成员（人） | NORMAL | ✓ — — | `delivery.notify.stage_changed` | — |
| `project.blocked_long` | 07 | `LEADS` + `OWNERS` | HIGH | ✓ — 即 | `delivery.notify.blocked_long` | 每次阻塞 1 |
| `project.mode_changed` | 07 | `OWNERS` + `LEADS` | HIGH | ✓ — 即 | `delivery.notify.mode_changed` | — |
| `project.autonomy_limited` ● | 07 BR-07-16 | `OWNERS` | CRITICAL | ✓ ✓ 即 | `delivery.notify.autonomy_limited` | hold 期间 1 |
| `delivery.confirmation_requested` ● | 07 | `HOLDERS(delivery_send)` 项目内 | HIGH | ✓ ✓ — | `delivery.notify.delivery_confirm` | 每交付 1 |
| `delivery.bundle_failed` / `.send_failed` | 07 | 发起人 / 决定人 + `LEADS` | HIGH | ✓ — 即 | `delivery.notify.bundle_failed` / `.send_failed` | 每次 1 |
| `acceptance.overdue` | 07 | `LEADS` | HIGH | ✓ — 即 | `delivery.notify.acceptance_overdue` | 每交付 1 |
| `acceptance.received` | 07 | 决定人 + `LEADS`；拒收且 AUTONOMOUS 加 `OWNERS` | HIGH（拒收 CRITICAL） | ✓ ✓ 即 | `delivery.notify.acceptance_received` | — |
| `budget.threshold` ●（100%） | 07 → BUDGET incident | `LEADS` + `OWNERS`（100% 加正在执行的工程师） | 80% HIGH / 100% CRITICAL | ✓ ✓ 即 | `delivery.notify.budget_80` / `.budget_100` | dedupe `budget:<project>:<cap_version>:<pct>` |
| `governance.delegation_granted` | D2 | 授予人以外的 `OWNERS` | NORMAL | ✓ — 摘 | `governance.notify.delegation_granted` | — |
| `governance.delegation_expiring` | 本模块任务（取代 07「委托 72h 内到期」行） | `GRANTOR` + `OWNERS` | HIGH | ✓ — 即 | `governance.notify.delegation_expiring` | dedupe `delegation_expiring:<id>` |
| `governance.delegation_ended` | `delegation.revoked`（非手动） | `GRANTOR` | NORMAL | ✓ — — | `governance.notify.delegation_ended` | dedupe `delegation_ended:<id>` |
| `governance.escalation_triggered` ● | incident 创建 / ≥24h 重提醒（取代 02 N5 单发） | `OWNERS` + `LEADS(p)` | 按严重度（CRITICAL 绕过免打扰） | ✓ ✓ 即 | `governance.notify.escalation.<kind>` | dedupe `incident:<id>:<yyyymmdd>` |
| `governance.takeover_executed` | D7 | 项目全部人类成员 | HIGH | ✓ ✓ 即 | `governance.notify.takeover` | — |
| `execution.attention` | `EXECUTION_ATTENTION` 建项 | 同 §6.10 接收人 | HIGH | ✓ ✓ — | `governance.notify.execution_attention` | 同执行 1 |
| `intake.triage_needed` | `INTAKE_TRIAGE` 建项 | `HOLDERS(intake_convert)` | 返工来件 HIGH | ✓ ✓ — | `governance.notify.intake_triage` | dedupe 每来件 |
| `ensemble.approval_requested` / `.selection_requested`（v1.1） | 05 | 同 §6.10 | NORMAL / HIGH | ✓ — — | `fleet.notify.ensemble_*` | 每计划 1 |
| `governance.spawn_grant_threshold`（D110） | 编制授权预算 80% / 100% | `GRANTOR` + `OWNERS` | 80% NORMAL / 100% HIGH | ✓ ✓ 即 | `governance.notify.spawn_grant_threshold` | dedupe `spawn_grant:<id>:<pct>` |
| `governance.spawn_grant_expiring`（D110） | 72h 内到期 | `GRANTOR` | NORMAL | ✓ — 摘 | `governance.notify.spawn_grant_expiring` | 每授权 1 |
| `agent.spawn_requested`（D110） | `AGENT_SPAWN_APPROVAL` 建项 | 同 §6.10 | NORMAL | ✓ ✓ — | `governance.notify.agent_spawn_requested` | merge `spawn_req:<grant>` 10 min |
| `agent.orphaned`（D110） | 05 `agent.orphaned`（授权终结后遗留的 AI 创建 Agent） | `GRANTOR` + `OWNERS` | HIGH | ✓ ✓ 即 | `governance.notify.agent_orphaned` | merge `orphan:<grant>` 1h |
| `governance.consumer_dead_letter` ● | 消费死信 | `OWNERS` | CRITICAL | ✓ — 即 | `governance.notify.dead_letter` | 同消费者 1h 1 |
| `fleet.agent_error_persisting`（D120） | 05 N-05-16 | Agent 负责人类 | HIGH | ✓ — 即 | `fleet.notify.agent_error_persisting` | 每次进入 ERROR 1 |
| `fleet.agent_inbox_backlog`（D120） | 05 N-05-17（积压 / 丢弃 / webhook 改轮询） | Agent 负责人类 | NORMAL | ✓ — — | `fleet.notify.agent_inbox_backlog` | dedupe 每 Agent 24h |
| `fleet.tool_credential_failing`（D120） | 05 N-05-18（中继连续收到上游 401 / 403） | `HOLDERS(tool_catalog_manage)` | HIGH | ✓ — 即 | `fleet.notify.tool_credential_failing` | dedupe 每条目 24h |
| `execution.comment_mentioned`（D120） | 03 N13 | 被提及的人类 | NORMAL | ✓ ✓ 摘 | `execution.notify.comment_mentioned` | merge 同线程 5 min |
| `execution.comment_on_my_execution`（D120） | 03 N14（本人执行产物上的新批注；甲方批注另给交付负责人） | 执行的人类执行者 / 发起者；CLIENT 线程另加交付负责人 | NORMAL | ✓ — — | `execution.notify.comment_on_my_execution` | merge 同执行 10 min |
| `planning.loop_run_escalated`（D120） | 10 N-10-11 | 循环契约负责人类 | HIGH | ✓ ✓ 即 | `planning.notify.loop_run_escalated` | dedupe 每运行 |
| `planning.loop_contract_auto_paused`（D120） | 10 N-10-12 | 循环契约负责人类 + Agent 负责人类 | HIGH | ✓ — 即 | `planning.notify.loop_contract_auto_paused` | dedupe 每契约每次暂停 |

甲方侧通知（01 N0、N8、N10–N13；07 验收提醒）经来源开口发送，不进入本目录与偏好。模板键属于各发出模块的 i18n 命名空间，渲染由 09 `Notifier` 按接收人语言完成；本模块拥有类型目录、接收人路由与偏好（经 `PreferenceResolver`），站内存储、合并窗口执行与投递归 09。

### 9.3 本模块模板（zh-CN；en-US / ms-MY 同 key）

- `governance.notify.delegation_expiring`：「{agent} 在 {project} 的委托将于 {ends_at_local} 到期（剩余 {remaining}），已用 {used_count} 次。」
- `governance.notify.escalation.SENSITIVE_PATH`：「{project} · {work_item_key} 修改了敏感路径（{set_labels}，共 {count} 处），该项委托裁决已冻结，需人工评审。」
- `governance.notify.escalation.CLIENT_REJECTION`：「甲方拒收了 {delivery_key}，{project} 已暂停。」
- `governance.notify.escalation.LOW_SELF_CONFIDENCE`：「{agent} 对 {work_item_key} 的自评置信度 {confidence}（低于 {threshold}），已转人工评审。」
- `governance.notify.escalation.NO_ELIGIBLE_AUTHORITY`：「{project} 的 {work_item_key} 待裁决，但没有可用的委托，请裁决或授予委托。」
- `governance.notify.escalation.EXECUTION_STALLED`：「{project} · {work_item_key} 的执行已中断 {minutes} 分钟未处理，请恢复、重试或标记失败。」
- `governance.notify.execution_attention`：「{work_item_key} 的执行{state_label}，需要你处理。」
- `governance.notify.intake_triage`：「新来件 {intake_key} 需要转换（{reason_label}）。」
- `governance.notify.spawn_grant_threshold`：「{supervisor} 的编制授权预算已用 {pct}%（${spent} / ${cap}）{exhausted_suffix}。」
- `governance.notify.spawn_grant_expiring`：「{supervisor} 的编制授权将于 {ends_at_local} 到期，当前在岗 {active}/{max}。」
- `governance.notify.agent_spawn_requested`：「{supervisor} 申请新建{role_label} Agent（{model} · {machine}），用于 {project}。」
- `governance.notify.agent_orphaned`：「{n} 个由 {supervisor} 创建的 Agent 已失去编制授权，请收编或退役。」
- `governance.notify.takeover`：「{actor} 接管了 {project}：撤销 {n} 条委托，已切回混编。理由：{reason_80}」

---

### 9.4 D112 / D113 增补通知

| 类型 | 触发 | 接收人 | 渠道 | 模板 key |
|---|---|---|---|---|
| ANDON | `andon.pulled` | 负责人 + 交付负责人 | 站内 + 推送 + 邮件（立即，CRITICAL） | `governance.notify.andon_pulled` |
| ESCALATION_INTERNAL_EXHAUSTED | 见 §6.10b | 负责人 + 交付负责人 | 站内 + 推送 | `governance.notify.internal_exhausted` |
| REQUIREMENT_CONFLICT | 见 §6.10b | 交付负责人 | 站内 + 推送 | `governance.notify.requirement_conflict` |
| EXCEPTION_DECISION | 见 §6.10b | 例外决定人 | 站内 + 邮件 | `governance.notify.exception_raised` |
| PLAN_APPROVAL | 见 §6.10b | 计划批准人 | 站内（15 分钟合并） | `governance.notify.plan_approval` |
| QUALITY_ESCAPE | BR-08-63 | 所有者 + 交付负责人 | 站内 + 每日摘要 | `governance.notify.quality_escape` |


**D119 增补通知**：

| 类型 | 触发 | 接收人 | 渠道 | 模板 key |
|---|---|---|---|---|
| PREVIEW_SHARE_APPROVAL | 见 §6.10b D119 | 分享批准人（不含申请人） | 站内 + 推送 | `governance.notify.preview_share_approval`：「{requester} 申请把 {execution} 的预览分享给甲方（有效 {ttl} 小时）：{note_80}」 |

**与 02 N11–N14 的对应**（均为升级类，08 负责收件箱投影与投递；02 定义触发与文案 key，08 不重复发送，同一去重键只投递一次）：

| 02 通知 | 触发 | 08 收件箱类型 | 优先级 | 去重 |
|---|---|---|---|---|
| N11 | `work_item.escalated{INTERNAL_ROUNDS_EXHAUSTED}` | ESCALATION_INTERNAL_EXHAUSTED | HIGH | 02 键 `esc:{wi}:…` |
| N12 | 需求质量冲突 / 歧义（BR-02-50，`work_item.escalated{REQUIREMENT_CONFLICT}`） | REQUIREMENT_CONFLICT（只给交付负责人，不给甲方） | NORMAL | 02 键 `rq:{r}:…` |
| N13 | `andon.pulled` | ANDON | CRITICAL（+推送） | 02 键 `andon:{id}` |
| N14 | `exception.raised` | EXCEPTION_DECISION | HIGH | 02 键 `exc:…` |

02 的 N1–N10 以外、N11–N14 以外的内部事件（T12 / T32 Agent 拒绝 / T34 等，BR-02-54）不在本表，按 BR-08-60 丢弃。

**内部质量门失败不通知任何人**（BR-08-60）；在通知偏好页不可开启此类通知。

## 10. 验收标准

> 测试层：unit（服务层、假时钟）/ integration（PostgreSQL、MySQL 8、SQLite 三方言真实库，IAM 真实模块，跨模块读取接口用夹具）/ e2e（Playwright，1440 与 390 视口）/ manual。时间相关一律假时钟。

### 10.1 目录与播种

```
AC-08-01  [integration]  US-08-01, BR-08-02
Given 新租户 T（底座 Administrator 成员 Sam）
When  种子流程连续执行 2 次（含两个实例并发）
Then  IAM 中恰好新增 12 个角色（§3.3），各角色权限与清单一致，Sam 是 team_owner 成员；
      审计 governance.role_seed_applied 仅 1 条；governance_role_seeds 12 行 manifest_version=1

AC-08-02  [integration]  BR-08-02, §3.3 步骤 3
Given T 已播种 v1，管理员从 engineer 移除了 knowledge_write
When  清单升级到 v2（engineer 新增码 X）并执行种子
Then  engineer 获得 X；knowledge_write 不被补回；G3 drift.missing=[knowledge_write]

AC-08-03  [integration]  BR-08-03
Given knowledge 模块关闭
When  播种
Then  knowledge_* 记入 skipped_codes，其余授予成功；开启模块后下一次播种补授且 skipped_codes 清空

AC-08-04  [unit]  §3.2
Given 某模块误把 decision_approve 同时登记为 NonDelegable，或 Delegable 中的码未注册
Then  Initialize 失败，错误消息列出冲突码
```

### 10.2 不可委托与仅人类（全量夹具）

```
AC-08-05  [integration]  BR-08-05, S2, S5
Given 夹具：Agent Rev（agent_reviewer + 本项目对每个被测码的 ACTIVE 委托行与载体绑定已被强行写入）、
      Agent Bad（被管理员误绑 delivery_lead + team_owner）、人类 Sam（team_owner）
When  对下表每个码以三者分别调用 Check（工作项事实全部满足委托条件）
Then  结果如下，且 Rev 与 Bad 的拒绝 reason_code 均为 AGENT_PROHIBITED（S2 先于 S6），Sam 均 ROLE:team_owner 允许
      （production_access 除外：Sam 无临时授权 → PRODUCTION_ACCESS_REQUIRED）
| 码 | Rev | Bad | Sam |
| scope_change | 拒 | 拒 | 允许 |
| delivery_send | 拒 | 拒 | 允许 |
| project_mode_change | 拒 | 拒 | 允许 |
| policy_manage | 拒 | 拒 | 允许 |
| delegation_grant | 拒 | 拒 | 允许 |
| takeover | 拒 | 拒 | 允许 |
| machine_revoke | 拒 | 拒 | 允许 |
| production_access | 拒 | 拒 | 拒（PRODUCTION_ACCESS_REQUIRED） |
| intake_decline | 拒 | 拒 | 允许 |
| memory_approve | 拒 | 拒 | 允许 |
| project_create（伪码） | 拒 | 拒 | 允许 |

AC-08-06  [integration]  BR-08-10
When  Sam 调 D2，actions 分别含上表 11 个码之一
Then  每次 422 160410，data.codes 列出该码；无委托行、无 IAM 绑定、无审计

AC-08-07  [integration]  §3.4
Given 人类阿澈申请 production_operator 4 小时，Morgan 批准
When  阿澈以 environment=PRODUCTION 调 Check
Then  允许，来源 ROLE:production_operator；4 小时后拒绝（PRODUCTION_ACCESS_REQUIRED）；
      若管理员把阿澈设为 production_operator 长期成员 → 拒绝 PRODUCTION_GRANT_NOT_TEMPORARY
```

### 10.3 职责分离

```
AC-08-08  [integration]  S3(a)
Given Rev 持有满足全部条件的委托，工作项 W 的 SelfProduced=true（Rev 曾执行其某个分支）
When  Rev 以 decision_approve / decision_reject / decision_request_changes / code_merge 调 Check
Then  全部拒绝 SEPARATION_OF_DUTIES；02 D1 返回 403 120403 data.reason_code=SEPARATION_OF_DUTIES

AC-08-09  [integration]  S3(b)
Given 设置 sod_human_self_approval=DENY；人类工程师 E（被授 decision_approve）是 W 的执行者
When  E 调 Check(decision_approve)        Then 拒绝 SEPARATION_OF_DUTIES；Check(decision_request_changes) 允许
When  所有者经 step-up 设为 ALLOW_WITH_REASON 后 E 再调 Check(decision_approve)
Then  允许，Steps 中 sod_override=true

AC-08-10  [integration]  S3(c)
Given 主管 Agent L 分派了 W（DispatchedByActor=true），L 被管理员授予 decision_approve 角色
When  L 调 Check(decision_approve)                    Then 拒绝 SEPARATION_OF_DUTIES（ROLE 来源被忽略）
When  所有者给 L 授予满足条件的委托后再调            Then 允许 DELEGATION:<id>
```

### 10.4 委托条件夹具（基线：Rev 委托 D = {actions:[approve, merge 另测], types:[FEATURE,BUG], max_risk:MEDIUM, cost_cap:5_000_000, max_rework:3, ends_at=now+24h}；工作项基线 = FEATURE, LOW, 1_000_000, 已验证全部通过, rework 0, 非自产, 无人工要求, 无 hold, ProjectGate 允许）

```
AC-08-11  [unit + integration]  S6
每行只改一个事实，调用 Check(decision_approve)，断言 Allowed 与 reason_code：
| # | 改动 | 期望 |
| 1 | 基线 | 允许 DELEGATION:D |
| 2 | Risk=MEDIUM | 允许 |
| 3 | Risk=HIGH | 拒 DELEGATION_RISK |
| 4 | CostMicros=5_000_000（等于上限） | 允许 |
| 5 | CostMicros=5_000_001 | 拒 DELEGATION_COST |
| 6 | RequiredVerificationsPassed=false（任一 INCONCLUSIVE / PENDING / 无必需验证） | 拒 DELEGATION_VERIFICATION |
| 7 | QualityReworkCount=2 | 允许 |
| 8 | QualityReworkCount=3 | 拒 DELEGATION_REWORK |
| 9 | Type=DOC | 拒 DELEGATION_TYPE |
| 10 | RequireHumanDecision=true | 拒 DELEGATION_HUMAN_REQUIRED |
| 11 | 存在该工作项 hold_delegations=true 的 OPEN incident | 拒 ESCALATION_HOLD |
| 12 | 07 CanDelegatedDecide=false（AUTONOMY_PRECONDITION） | 拒 PROJECT_GATE |
| 13 | 假时钟到 ends_at（任务未运行） | 拒 NO_DELEGATION |
| 14 | 行 ACTIVE 但 IAM 绑定已被删除 | 拒 NO_DELEGATION（并在 ≤5 min 内对账为 BINDING_REMOVED） |
| 15 | 码=code_merge，D.max_risk=LOW 的另一委托，Risk=LOW | 允许；Risk=MEDIUM → 拒 DELEGATION_MERGE_RISK |
| 16 | 主体为人类且被误绑载体角色 | 拒 NO_AUTHORITY，Steps 含 ignored_carrier_match |
| 17 | Facts 缺失 | 拒 INVALID_REQUEST |

AC-08-12  [integration]  BR-08-06
Given 基线可允许，另对 Rev 在本项目存在 decision_approve 的 deny 实体绑定
Then  拒 EXPLICIT_DENY

AC-08-13  [integration]  S4
Given engineer 角色被授 decision_approve，条件 {"version":1,"in":[{"context":"resource.attr.risk"},{"value":["LOW"]}]}
When  工程师 E（非执行者）对 LOW / MEDIUM 工作项调 Check
Then  LOW 允许 ROLE:engineer；MEDIUM 拒 NO_AUTHORITY；矩阵 G1 该单元格 state=CONDITIONAL
```

### 10.5 授予、撤销、在途裁决、到期、接管

```
AC-08-14  [integration]  BR-08-08, BR-08-11, BR-08-12
When  Sam（team_owner）带有效 step-up 授予 Rev 委托 {approve, request_changes} → 201，IAM 绑定 2 条 expires_at=ends_at，审计与 delegation.granted 各 1
When  自定义角色 R（有 delegation_grant、无 decision_approve）的成员授予含 approve 的委托 → 403 160403(GRANTOR_LACKS_AUTHORITY)
When  对同一 Rev×项目再授予 → 409 160413；带 replaces_delegation_id 再授予 → 201，旧行 REVOKED(REPLACED)

AC-08-15  [integration]  BR-08-15
When  同一 Idempotency-Key 重放 D2 两次 → 第二次返回首次响应，仅 1 行；同键不同体 → 422 160400

AC-08-16  [integration]  BR-08-20（PostgreSQL 与 MySQL 各一遍；SQLite 一遍）
Given Rev 持有委托 D，W 满足条件
Case A：事务 T1（02 裁决）执行 Check(tx) 取得 D 的共享锁后暂停；T2 撤销 D
Then  T2 阻塞直到 T1 提交；T1 的 Decision authority_source=DELEGATION:D，decided_at < D.ended_at；T2 随后成功
Case B：T2 先提交撤销；T1 再执行 Check(tx)
Then  T1 拒绝 NO_DELEGATION，02 返回 403 120403，审计 delivery.decision_denied（独立事务）
Case C（SQLite）：T1 读取后 T2 提交，T1 写入
Then  T1 得到 busy-snapshot，02 重试后同 Case B

AC-08-17  [integration]  BR-08-16, BR-08-17, BR-08-21
When  Sam 撤销 D（无 step-up）
Then  同请求返回后下一次 Check 即拒绝；IAM 绑定已删除且 revision+1；02 释放 Rev 的 REVIEWER 认领；
      重复撤销 200 同结果；对 EXPIRED 委托撤销 → 409 160414

AC-08-18  [integration]  BR-08-19
Given D.ends_at = T0
Then  T0−1ms Check 允许；T0 Check 拒绝；任务在 T0+30s 内把 D 置 EXPIRED(WINDOW_ENDED)，发 delegation.revoked{end_reason:WINDOW_ENDED}；
      T0−72h 前后 15 min 内授予人收到 1 条 governance.delegation_expiring（重复执行任务不重复）

AC-08-19  [integration]  BR-08-22
Given AUTONOMOUS 项目 P 有 3 条 ACTIVE 委托
When  Sam 带有效 step-up 调 D7
Then  单事务：3 条 REVOKED(TAKEOVER)；07 TakeOverOn 被调用且 mode=HYBRID、revoked_delegation_ids 回填；
      governance_takeovers 1 行；审计 governance.takeover_executed；通知全部人类成员
When  注入 07 TakeOverOn 返回错误
Then  整体回滚：3 条仍 ACTIVE，无审计，响应 data.upstream_code
When  重放同 Idempotency-Key → 返回首次结果；对已 HYBRID 且无委托的项目再接管 → 201，revoked_delegation_ids=[]，mode_change_id=null

AC-08-20  [integration]  D7 权限
When  delivery_lead（无 takeover）调 D7 → 403 160403；Agent 以 Bearer 调用 → 403 160403(AGENT_PROHIBITED)（且无 Cookie 无法 step-up）

AC-08-21  [integration]  BR-08-23, §7.3
When  访问评审移除 D 的 IAM 绑定 → ≤5 min 内 D 为 REVOKED(BINDING_REMOVED)
When  07 发 project.mode_changed{to:HYBRID} → 该项目全部委托 REVOKED(MODE_CHANGED)
When  Rev 被 05 置 STOPPED → agent.state_changed 触发对账，D 为 REVOKED(GRANTEE_INACTIVE)

AC-08-22  [integration]  §3.6
When  decision.made（authority_source=DELEGATION:D）被投递 2 次
Then  governance_delegation_uses 1 行，used_count=1，last_used_at=事件时间
```

### 10.6 升级规则

```
AC-08-23  [integration]  §4.2 去重
Given AUTONOMOUS 项目 P，W 触发 work_item.escalated{REWORK_LIMIT}
When  同一事件重复投递，以及 2 小时后同 W 再次 escalated{REWORK_LIMIT}
Then  只有 1 个 OPEN incident，occurrence_count=2；通知仅 1 条；24h 后第三次触发再通知 1 次

AC-08-24  [integration]  BUDGET
When  07 对 P 发 budget.threshold_reached{80, cap_version 1} 两次，再发 {100, 1}，再改上限后发 {80, 2}
Then  incident 3 个（once_key 分别为 …:1:80、…:1:100、…:2:80）；100% 为 CRITICAL；本模块不加 hold（07 已加）

AC-08-25  [integration]  SENSITIVE_PATH
Given 默认 pattern sets；W 的 claim 对应执行 DiffSummary 含 db/migrations/0007_add_index.sql
When  claim.submitted
Then  incident CRITICAL，facts.matched=[{set:MIGRATIONS, glob:"**/migrations/**", path:"db/migrations/0007_add_index.sql"}]，hold_delegations=true；
      Rev 对 W 的 Check 拒 ESCALATION_HOLD；人类 ELIGIBLE 收到 WORK_ITEM_REVIEW 与 ESCALATION_ALERT
When  所有者驳回（理由 ≥10）
Then  DISMISSED，Rev 的 Check 恢复允许
Given 路径仅 README.md → 不触发

AC-08-26  [integration]  BR-08-28
When  DiffSummary 超时 3s / 返回 truncated
Then  触发 incident，facts.paths_unavailable=true，委托冻结

AC-08-27  [unit]  BR-08-29
min_confidence=60：self_confidence=59 触发、60 不触发、NULL 不触发

AC-08-28  [integration]  CLIENT_REJECTION, BR-08-31
When  AUTONOMOUS 项目收到 acceptance.received{REJECTED}
Then  incident CRITICAL，07 AddEscalationHoldOn 被调用（authority POLICY:<rule_id>），通知所有者
When  HYBRID 项目收到同样事件 → incident 仅 NOTIFY，未调用 07
When  07 拒绝加 hold → incident 仍创建，actions_applied[0].status=FAILED，CRITICAL 通知
When  07 发 project.hold_changed 移除该 ESCALATION hold → incident RESOLVED(RESUMED)

AC-08-29  [integration]  BR-08-30
Given 项目 P 为 AUTONOMOUS
When  PUT E2 将 P 的 CLIENT_REJECTION enabled=false → 409 160431
When  关闭 SPINNING（非硬性，放宽）无 step-up → 403 160403(REAUTH_REQUIRED)；带有效证明 → 200

AC-08-30  [integration]  §4.2 非法迁移
When  对 RESOLVED incident 调 E5 / E6 → 409 160432；对 PROJECT_HOLD 类调 E6 → 409 160433

AC-08-31  [integration]  NO_ELIGIBLE_AUTHORITY
Given AUTONOMOUS 项目无 ACTIVE 委托（holds 阻止前不会发生，夹具直接构造）
When  W 进入 NEEDS_HUMAN_REVIEW → WARNING incident，人类收到 WORK_ITEM_REVIEW；HYBRID 项目不评估
```

### 10.7 我的裁决、通知

```
AC-08-32  [e2e]  US-08-11, BR-08-35
Given Morgan 在桌面（1440）与手机（390）同时打开「我的裁决」
When  W 进入 NEEDS_HUMAN_REVIEW
Then  两端 ≤1s 出现该项（p95，50 次）；Morgan 在手机按〔通过〕成功
Then  桌面 ≤1s 移除该项并 toast「已在其他设备处理」，计数同步减 1

AC-08-33  [integration]  BR-08-33
Given 收件箱项 X 对应的 W 已被他人裁决但 decision.made 投递延迟
When  Morgan 按 A + Enter
Then  02 返回 409；前端移除 X；60s 内对账把 X 置 RESOLVED(ACTED_BY_OTHER)

AC-08-34  [unit + e2e]  BR-08-34, §8.4
Then  OpenAPI 中不存在任何对多个收件箱项或多个工作项同时裁决的操作；页面无复选框；
      J/K 移动焦点；A 进入确认态，Esc 取消不发请求，Enter 发出 1 个请求；R 打开结构化反馈；输入框聚焦时 J 输入字符

AC-08-35  [integration]  BR-08-36
When  Morgan 在项目 P 的权限被撤销
Then  I1 不再返回 P 的项；≤60s 内这些项 WITHDRAWN 并推送 item_removed

AC-08-36  [integration]  §4.3
When  inbox_projector 重放同一事件 → 不新增项（open_key 唯一）；对 RESOLVED 项调 I4 → 409 160441

AC-08-37  [integration]  BR-08-38
When  5 min 内项目 P 有 5 个工作项进入 NEEDS_HUMAN_REVIEW
Then  Morgan 的站内通知 1 条 count=5，推送 1 次（窗口结束时）

AC-08-38  [integration]  BR-08-39, BR-08-40
Given Morgan 免打扰 22:00–08:00（Asia/Kuala_Lumpur）
When  23:00 产生 HIGH 通知与 CRITICAL incident
Then  HIGH 的推送在 08:00 合并发送 1 次；CRITICAL 立即推送与邮件；站内两者立即可见
When  PUT N4 关闭 governance.escalation_triggered 的站内 → 422 160450

AC-08-39  [integration]  §9.2 跨类型去重
When  07 与本模块对同一委托各产生到期提醒
Then  接收人只收到 1 条（dedupe_key delegation_expiring:<id>）
```

### 10.8 审计、总览、step-up、可用性

```
AC-08-40  [integration]  §6.6, BR-08-43
Given 项目 P 与 Q 各有裁决事件
When  A1 project_id=P → 只返回 P 的事件，authority chip 正确（ROLE / DELEGATION / POLICY）
When  调底座完整性接口 → valid=true；导出 → NDJSON 末行为完成记录，离线校验哈希链通过
When  测试库直接篡改一条事件 detail
Then  完整性 valid=false，页面徽标红，〔导出〕禁用，导出接口 409

AC-08-41  [integration]  §7.1 audit_indexer
When  1,200 条事件写入后启动索引任务（批 500），期间进程重启一次
Then  索引恰好 1,200 行，sequence 连续，无重复；index_lag_seconds 在追平后 <5

AC-08-42  [integration]  §6.9 O1
When  07 DeliveryMetricsReader 返回错误
Then  overview.kpis.status=ERROR，其余节 OK；对无 machine_read 的成员 machines.status=FORBIDDEN、missing_permission=machine_read

AC-08-43  [integration]  §6.9 登记表, BR-08-08
对登记表中本模块的 4 个操作（D2、D7、E2 放宽、S2 放宽）逐一：
  无 reauth_proof → 403 160403(REAUTH_REQUIRED) data.purpose 正确；
  证明在发起后 301s 使用 → 160403(REAUTH_REQUIRED)（过期）；
  同一证明第二次使用 → 160403(REAUTH_REQUIRED)（已用）；
  purpose 正确但 target 为其他项目 → 160403(REAUTH_REQUIRED)；
  有效证明 → 成功，且证明消费与业务写入同事务（注入业务失败时证明未被消费，可重试）
对 01/02/03/04/05/07 的登记操作：各规格的 AC 引用本表 purpose / target（契约测试校验 purpose 字符串一致）

AC-08-44  [integration]  BR-08-46, §3.5
When  IAM 授权评估或 07 ProjectGate 不可用
Then  Check 返回 error；D2 / D7 返回 503 160461；02 裁决 503 120460；无任何放行

AC-08-45  [e2e]  §8.1
Given 选中项目 P，Rev 持有委托（剩余 23h）
Then  矩阵 agent_reviewer 列显示「委托中 · Rev · 23 小时」琥珀环；scope_change 行带 🔒「不可委托」；
      授予对话框中 scope_change 等不可勾选；手机视口矩阵为「按角色」卡片

AC-08-46  [integration]  §3.5.3
When  Rev 对 HIGH 风险工作项调 G2
Then  steps 中 S2 PASS、S3 PASS、S4 NO_MATCH、S6 DENY(DELEGATION_RISK)、fix_hint=「该工作项风险高于委托上限，需要人类裁决」；第 61 次/分钟 → 429 160429
```

### 10.9 审校补充（v1.1 合入）

```
AC-08-47  [integration]  §3.3, BR-08-02
Given 新租户首次播种
When  读取 delivery_lead、agent_lead、intake_api_client 的种子码表
Then  delivery_lead 不含 project_mode_change、delegation_grant、production_access；agent_lead 不含 work_item_assign、execution_start；intake_api_client 仅含 intake_submit；Registry 含 intake_submit、decomposition_template_manage、verifier_manage

AC-08-48  [unit]  §3.2 HumanOnly, BR-08-05
Given Agent A 经误绑角色持有 interaction_respond
When  Check{actor:AGENT, code:interaction_respond}
Then  Allowed=false，reason_code=AGENT_PROHIBITED

AC-08-49  [integration]  §4.1, §7.2
Given 委托 D ACTIVE，ends_at 已过
When  governance.delegation_expiry 运行
Then  D → EXPIRED(WINDOW_ENDED)，outbox 只发 delegation.expired（不发 delegation.revoked）；02 释放该 Agent 的 REVIEWER 认领

AC-08-50  [integration]  BR-08-09
Given Agent L 仅有 LEAD 角色
When  授予 actions=[decision_approve]
Then  422 160411；L 增加 REVIEWER 后重试 → 201；actions=[intake_convert] 授予仅 COORDINATOR 的 Agent C → 201

AC-08-51  [integration]  BR-08-10
Given 项目 P 已 AUTONOMOUS，Agent R 有委托 D1{decision_approve}
When  所有者以 replaces_delegation_id=D1、actions=[decision_approve,intake_convert] 授予
Then  同事务 D1 → REVOKED(REPLACED)、新委托 ACTIVE；R 以 intake_convert（NEW_REQUIREMENT）转换来件通过 Check，01 在同事务创建需求且不再检查 requirement_write；IntakeConvertMode=NEW_PROJECT → NON_DELEGABLE

AC-08-52  [integration]  §3.5 S6 ⑪
Given claim C 刚提交，governance_claim_evaluations 尚无 C 的行
When  受托 Agent 对该工作项 APPROVE
Then  403，reason_code=ESCALATION_PENDING；评估回执落库且无命中后重试 → 允许，来源 DELEGATION:<id>

AC-08-53  [integration]  §3.5 WorkItemFacts.CostMicros
Given 工作项有 2 条 DELIVERY 执行（累计 3_000_000、2_500_000）与 1 条 VERIFICATION 执行（900_000），委托 cost_cap=5_000_000
When  Check(decision_approve)
Then  CostMicros=5_500_000，S6 DENY(DELEGATION_COST)

AC-08-54  [integration]  §6.10 DISPATCH_PROPOSAL
Given AUTO 项目中提案因护栏 G1（HIGH 风险）被拦截，dispatch.proposed{requires_human:true}
When  投影
Then  HOLDERS(work_item_assign) 人类各得 1 项 OPEN；dispatch.expired 到达 → 全部 RESOLVED(SOURCE_CLOSED)；requires_human=false 的提案不建项

AC-08-55  [integration]  §6.10 MEMORY_RULE_APPROVAL
Given 人类 H 写入 RULE 记忆 M（PENDING_REVIEW），H 与 K 都持有 memory_approve
When  投影；随后 M 过期产生 memory.revoked
Then  只为 K 建项（H 被排除）；memory.revoked 后该项 RESOLVED(SOURCE_CLOSED)

AC-08-56  [integration]  §6.10 INTAKE_TRIAGE / EXECUTION_ATTENTION
Given 返工来件 I（origin_acceptance_id 非空）到达；执行 X（Agent）进入 INTERRUPTED
When  投影
Then  HOLDERS(intake_convert) 人类得 HIGH 项 INTAKE_TRIAGE，intake.converted 后关闭；LEADS(p) 得 EXECUTION_ATTENTION，X 恢复或标记失败后关闭

AC-08-57  [integration]  BR-08-49
Given AUTONOMOUS 项目有 3 个 NEEDS_HUMAN_REVIEW 工作项（其中 1 个已有人类 OPEN 项）
When  一键接管成功
Then  ≤1s 内另 2 个工作项为评审接收人建 WORK_ITEM_REVIEW；已存在的不重复

AC-08-58  [integration]  §3.9.2 CLIENT_REJECTION
Given AUTONOMOUS 项目交付被 PARTIALLY_ACCEPTED（工作项 W1 拒收、W2 接受）
When  acceptance.received
Then  项目 hold；仅 W1 有 hold_delegations=true 的 incident（受托 Agent 对 W1 裁决 → ESCALATION_HOLD）；对应返工来件转为 CONVERTED 后该 incident AUTO_CLEARED

AC-08-59  [integration]  §3.9.2 EXECUTION_STALLED / NO_ELIGIBLE
Given stall_minutes=30；执行 X 于 T 进入 INTERRUPTED；Agent 评审认领 30 min 未裁决
When  T+31min 扫描
Then  创建 EXECUTION_STALLED incident（WARNING，NOTIFY）；评审超时触发 NO_ELIGIBLE_AUTHORITY 并为人类建 WORK_ITEM_REVIEW

AC-08-60  [unit]  §6.1 tag, BR-08-45
Given 生成 OpenAPI
When  检查 08 操作
Then  T/P/M/TM 守卫操作 tag=tenants；D3–D5=delegations、E2=escalation-rules、E4–E6=escalation-incidents；PUT 缺 If-Match → 428 160428

AC-08-61  [e2e]  §8.6
Given engineer 登录（无 machine_read、policy_read 以外权限按种子）
When  打开外壳与 ⌘K
Then  团队域侧栏不含「机器」等无权限项；顶栏域标签为 工作台 · 会话 · 项目 · 团队 · 治理（无「协作」），底栏为 工作台/会话/项目/裁决（D118 修订）；⌘K「接入机器」不出现、「跳转」项显示为「域 · 标签」；裁决徽标随 inbox SSE ≤1s 更新并以 role=status 播报

AC-08-62  [unit]  S6 适用范围, 复审 #6
Given AUTONOMOUS 项目 P；协调员 Agent C 持 intake_convert 委托（NEW_REQUIREMENT），无 hold，ProjectGate 允许
When  C 调 Check(intake_convert, IntakeConvertMode=NEW_REQUIREMENT, TriageCardAuthorID=Iris)
Then  允许，来源 DELEGATION:<id>（不校验 ④ 验证、⑪ claim 评估回执）
And   TriageCardAuthorID=C 时 → 拒绝 SELF_REVIEW

AC-08-63  [integration]  §6.10 DELIVERY_CONFIRM, 复审 #19
Given 交付 D 的 DELIVERY_CONFIRM 收件箱项 OPEN
When  分别发生 delivery.ready / delivery.withdrawn / delivery.sent
Then  每种情况该项均关闭（≤1s 他端移除）；收件箱项无「退回」动作

AC-08-64  [integration]  §6.10 DISPATCH_PROPOSAL, 复审 #21
Given DISPATCH_PROPOSAL 项 OPEN
When  05 发布 dispatch.rejected
Then  该项关闭；重复投递同一 event_id 不产生副作用（09 receipts）

AC-08-65  [unit]  S7 / S1, 复审 #5 / #26
Given 05 已登记 SystemActions fleet.review_dispatch 与规则 review_dispatch
When  SYSTEM:fleet.review_dispatch 调 Check(work_item_assign, role=REVIEWER)；另一次 LEAD Agent 调 Check(work_item_assign) 命中 dispatch_auto
Then  前者允许、来源 POLICY:review_dispatch；后者来源 POLICY:dispatch_auto（不含提案 id），提案 id 仅出现在裁决 detail.dispatch_proposal_id
And   未登记 SystemActions 时前者拒绝 SYSTEM_NOT_REGISTERED

AC-08-66  [unit]  AuthorityReasonCode, 复审 #25
Given 规范清单
When  架构测试扫描各模块规格声明的 reason_code 常量
Then  均在清单内；出现 NOT_DELEGABLE / PERMISSION_DENIED / STEP_UP_REQUIRED 即失败
```

### 10.10 编制授权（D110）

```
AC-08-67  [integration]  BR-08-50, US-08-18
Given 交付负责人 M（种子不含 spawn_grant_manage）
When  M 调 SG2 为主管 Agent L 授予编制授权
Then  403 160403 data.reason_code=MISSING_PERMISSION；无授权行；审计追加 outcome=denied

AC-08-68  [integration]  BR-08-50, S2
Given 主管 Agent L（即使被人工绑定了 team_owner 角色）
When  L 调 SG2 为自己或其他 Agent 授予编制授权
Then  403 160403 data.reason_code=AGENT_PROHIBITED（spawn_grant_manage ∈ NonDelegable ⊆ HumanOnly）

AC-08-69  [integration]  BR-08-50
Given 团队所有者 O；step-up 证明缺失或 purpose ≠ spawn_grant_manage
When  O 调 SG2
Then  403 160403 REAUTH_REQUIRED；带有效证明 → 201，state=ACTIVE，事件 spawn_grant.granted，审计 governance.spawn_grant_granted 含快照
And   allowed_roles 含 REVIEWER → 422 160470

AC-08-70  [integration]  BR-08-51
Given 项目 P1 AUTONOMOUS、P2 HYBRID
When  O 以 project_ids=[P1,P2]、approval_mode=AUTO 调 SG2；另以 project_ids=[]（团队级）、AUTO 调 SG2
Then  均 422 160475
When  O 以 project_ids=[P1]、AUTO 授予 G；之后 07 发布 project.mode_changed{P1, to:HYBRID}
Then  G.effective_approval_mode=REQUIRE_APPROVAL、approval_mode 仍为 AUTO，发 spawn_grant.updated；P1 切回 AUTONOMOUS 后恢复 AUTO

AC-08-71  [integration]  BR-08-53（并发，三方言）
Given 授权 G：max_active_agents=3、active_count=2、spawned_today=0
When  05 的两个建 Agent 事务同时 Reserve(G, A1) 与 Reserve(G, A2)
Then  恰一个成功（active_count=3）；另一个 409 160471 SPAWN_QUOTA_EXCEEDED，其建 Agent 事务整体回滚（无服务账号、无 Agent 行）
And   成功方重复调用 Reserve(G, A1) → 成功且 active_count 仍为 3

AC-08-72  [integration]  BR-08-53 / BR-08-54
Given max_spawns_per_day=2，今日已建 A1、A2，随后 A1 退役（Release RETIRED）
When  Reserve(G, A3)
Then  409 160471 SPAWN_DAILY_LIMIT（日配额不因释放回退）；次日 00:00:30 UTC 后 Reserve 成功

AC-08-73  [integration]  BR-08-55
Given G：budget_cap=$50、per_agent_cap=$15；成员 A1；03 对 A1 的执行 E 依次发布 cumulative_cost_micros = 10_000_000、16_000_000、16_000_000（重复）、15_000_000（回退）
When  成本累计消费
Then  G.spent_micros=16_000_000（重复与回退不计）；A1 达 $15 → spawn_grant.updated{member_cap_reached:A1} 且授予人收到通知（仅一次）
When  其余成员累计使 G.spent 达 $40 与 $50
Then  $40 时发 governance.spawn_grant_threshold(80%) 一次；$50 时 → EXHAUSTED、事件 spawn_grant.exhausted、升级 SPAWN_BUDGET（CRITICAL）；此后 Reserve → 409 160472；O 以 SG4 把预算调到 $80（step-up）→ ACTIVE

AC-08-74  [integration]  BR-08-57
Given AUTONOMOUS 项目 P；G1 project_ids=[P]、G2 project_ids=[P,Q]、G3 团队级；G1 下已建 A1
When  O 接管 P（D7）
Then  同一事务内 G1、G2 → REVOKED(TAKEOVER)；G3 不撤销但 effective_approval_mode=REQUIRE_APPROVAL；takeover.executed.revoked_spawn_grant_ids=[G1,G2]
And   A1 未被退役，成员 RELEASED(GRANT_ENDED)；05 发布 agent.orphaned 后授予人收到 agent.orphaned 通知

AC-08-75  [integration]  BR-08-56
Given A1 为 AI 创建（origin=AGENT_SPAWNED）且未收编，持 REVIEWER 角色（由人追加）
When  O 调 D2 把 decision_approve 委托给 A1
Then  422 160411 data.reason_code=SPAWNED_NOT_ADOPTED
When  人收编 A1（05 agent.adopted，05 同事务调用 Release(G, A1, ADOPTED)）后重试
Then  201；G.active_count 减 1，A1 之后的成本不再计入 G

AC-08-76  [unit]  agent_spawn 评估路径（S4 PolicyOnly / S7）
Given 主管 Agent L 持 ACTIVE 授权 G（allowed_roles=[EXECUTOR]、allowed_models=[{CLAUDE_CODE, sonnet}]）
When  依次：L 以 Spawn{Role:REVIEWER}；L 以 Spawn{Role:EXECUTOR, Model:opus}；L 以合法事实；人类 O（team_owner）以合法事实 调 Check(agent_spawn)
Then  依次：拒绝 SPAWN_GRANT_SCOPE；拒绝 SPAWN_GRANT_SCOPE；允许，来源 POLICY:spawn_grant；拒绝 NO_AUTHORITY（PolicyOnly：角色授予不生效）
And   非 LEAD 的执行者 Agent 调 Check(agent_spawn) → 拒绝 SPAWN_NOT_LEAD

AC-08-77  [integration]  §6.10 AGENT_SPAWN_APPROVAL, BR-08-58
Given G.effective_approval_mode=REQUIRE_APPROVAL；05 发布 agent.spawn_requested{R}
When  投影
Then  agent_manage 持有人的收件箱出现 AGENT_SPAWN_APPROVAL；任一人批准后 05 发布 agent.spawned{R} → 所有人的该项 RESOLVED（他端 ≤1s 移除）
And   若批准前 O 撤销 G → 该项 WITHDRAWN，05 的批准接口返回授权不可用
```

---

### 10.11 D112 / D113 增补验收标准

**AC-08-78**（integration）内部失败不打扰人 — Given W 处于 INTERNAL_QA When 产品验收 Agent 判定不通过、W → REWORK Then 无任何收件箱项、无任何通知记录；指标 internal_rounds +1。
**AC-08-79**（integration）内部轮次用尽 — Given max_internal_rounds=5，W 第 5 轮内部失败 When 02 发布 `work_item.escalated{INTERNAL_ROUNDS_EXHAUSTED}` Then 负责人与交付负责人各得 1 条 `ESCALATION_INTERNAL_EXHAUSTED`；W 进入 NEEDS_HUMAN_REVIEW 后该项关闭。
**AC-08-80**（integration）需求冲突不直达甲方 — `REQUIREMENT_CONFLICT` 只投影给交付负责人；甲方门户、来件联系人无任何新消息。
**AC-08-81**（integration）Andon — Agent A 拉线成功（`andon_pull`）；A 清除停线 → 403 `120403`（reason `HUMAN_ONLY`）；负责人清除成功并关闭 ANDON 项；A 一小时内第 4 次拉线 → 生成 `ESCALATION_ALERT`。
**AC-08-82**（unit）例外决定 — Agent 调 Check(`exception_decide`) → 拒 `NON_DELEGABLE`；人类 `delivery_lead` → 允许（ROLE）。
**AC-08-83**（unit）计划批准 — 执行者模型家族 `anthropic`：评审 Agent 家族 `anthropic` → `SAME_MODEL_FAMILY`；家族 `openai` 且风险 MEDIUM → 允许（`POLICY:plan_review`）；风险 HIGH → 拒 `NO_AUTHORITY`；主管 Agent 批准自己派出工作项的计划 → `SEPARATION_OF_DUTIES`（`LEAD_DISPATCHED`）；计划作者自批 → `SELF_REVIEW`。
**AC-08-84**（unit）负责人缺失 — `AccountablePrincipalID` 为空时对 `decision_approve` → `ACCOUNTABLE_HUMAN_MISSING`；指向停用成员 → 同上。
**AC-08-85**（integration）穿透学习 — Given W 曾由 TESTER T（openai）与 ACCEPTOR P（google）放行 When 人类 REJECT 触发 `quality.escaped{HUMAN_REVIEW}` Then 建穿透记录（重复事件不重复建）、通知所有者与交付负责人；本模块**不**调用 06 `MemoryWriter` 或 11 `EvalSetWriter`（T 与 P 的 EXPERIENCE 草稿与评测用例由 02 `delivery.escape_learning` 提出，详情页只读展示）；人类「确认学习」后 `learning_state=RECORDED`，发布 `quality.escape_learning_decided`，02 据此使草稿生效；「驳回」须填理由，草稿与用例被撤回；Agent 调 Q3 → 403。
**AC-08-86**（integration）总览磁贴 — 07 指标穿透率 12% → 磁贴琥珀；25% → 红色。
**AC-08-87**（architecture）收件箱 allowlist — 投影器只订阅 §6.10 / §6.10b 列出的事件；新增未登记事件类型的投影 → 架构测试失败。
**AC-08-88**（integration）可见性过滤 — 发布 `work_item.state_changed`（allowlist 内类型）且 `visibility=INTERNAL` → 收件箱项 0、通知发件箱 0、频道人类可见消息 0；同事件 `visibility` 缺省 → 正常投影。
**AC-08-89**（unit）纯人工模式 — `team_mode=HUMAN_ONLY` 项目的 ASSISTANT Agent 请求 `decision_approve` → 403 `HUMAN_ONLY_MODE`；请求 `report_read` → 允许；即使存在放行 `decision_approve` 的策略仍拒。
**AC-08-90**（integration）N11–N14 路由 — 依次触发四类升级 → 各产生一条对应类型收件箱项、接收人如 §9.4 对应表；重复事件按去重键只产生一条。

### 10.12 D118 控制台外壳增补验收标准（含 D118 修订：项目是主容器）

**AC-08-91**（e2e）域内记住子页面 — Given Morgan 在项目 P 的项目空间打开「测试 › 缺陷」（`/t/:tenantID/p/P/defects?sort=-severity`），再切到团队域打开「机器」 When 点击「项目」域标签 Then 回到 `/t/:tenantID/p/P/defects?sort=-severity`，侧栏为 P 的项目空间且项目切换器显示 P；点击「团队」回到机器页；Morgan 被移出 P 后点击「项目」→ 项目组合（记住的页面已不可见），不出现 P 的名称；清空浏览器本地存储后点击「项目」→ 项目组合，导航不报错；项目外侧栏只含 项目组合 · 来件 · 交付总览 与「跨项目」分组 需求 · 任务 · 缺陷 · 流水线。
**AC-08-92**（e2e）按权限隐藏菜单项与整个域 — Given 成员 X 的角色只含 `work_item_read`、`execution_read` When 打开外壳 Then 顶栏域标签只有 工作台 · 项目 · 治理（会话与团队的项全部被过滤，标签不显示；任何情况下都没有「协作」）；项目域项目外侧栏只含「跨项目 › 任务 · 缺陷 · 流水线」（无项目组合、来件、交付总览、需求）；治理域只含「流转配置」；再撤销 `work_item_read` 后刷新 → 治理域标签也不显示，项目域只剩「跨项目 › 流水线」，工作台不含「我的任务」；直接访问 `/t/:tenantID/governance/audit` → 页面无权限态「缺少权限 `audit_read`」（菜单隐藏不替代页面守卫）。
**AC-08-93**（e2e）会话全宽 — Given 视口 1440 When 点击「会话」域标签 Then 不渲染侧栏，内容区占满顶栏以下全宽并呈现 05 §8.8 三栏（左：提及与线程 / 频道 / 私信 / 执行会话；中：消息；右：线程 / 详情）；访问旧路由 `/t/:tenantID/p/:projectID/channels/:channelID` → 重定向到 `/t/:tenantID/chat/:channelID` 且当前域为会话；在项目 P 的项目空间点击「项目频道」→ 替换式重定向到 P 默认频道 `/t/:tenantID/chat/:channelID`，当前域为会话；视口 375 时为单栏堆叠，底栏为 工作台 · 会话 · 项目 · 裁决，「会话」显示未读数。
**AC-08-94**（e2e）项目空间 — Given Morgan 可见项目 P（软件模板）与 Q（VIDEO 模板），不可见项目 R When 从「跨项目 › 缺陷」点击 P 的缺陷 D Then 打开 `/t/:tenantID/p/P/work-items/D`，当前域为项目、侧栏为 P 的项目空间且高亮「开发 › 任务」，侧栏顶部为项目切换器（P）与〔返回项目组合〕；When 在 `/t/:tenantID/p/P/executions?status=RUNNING` 用切换器选 Q Then 到 `/t/:tenantID/p/Q/executions?status=RUNNING`（保持子页面与无关查询参数）；When 在 P 的工作项详情切到 Q Then 到 `/t/:tenantID/p/Q/work-items`（详情 → 同项列表）；When 在 Q 的「领域工作台 › 视频」切到 P Then 到 P 概览并 toast「该项目没有『视频』，已打开概览」，P 的项目空间不显示任何领域工作台项；When 点击〔返回项目组合〕 Then 到 `/t/:tenantID/projects` 且侧栏为项目外；When 直接访问 `/t/:tenantID/p/R/defects` Then 404 态「项目不存在或你无权访问」+〔返回项目组合〕，页面与切换器中均不出现 R 的名称；`⌘K` 输入「缺陷」列出「项目 · 缺陷」与「P · 缺陷」「Q · 缺陷」。

### 10.13 D119 远程工作区增补验收标准

**AC-08-95**（integration）D119 权限播种 — Given 已有租户升级到含 D119 的 catalog When `governance.seed_reconcile` 运行 Then `team_owner` 增加 `terminal_interact`、`workspace_file_edit`、`preview_share_approve`、`machine_access_manage`；`delivery_lead` 增加前三者、**不含** `machine_access_manage`；`engineer` 只增加 `workspace_file_edit`；全部 Agent 角色不增加任何一码；管理员此前手工移除的码不补回；审计 `governance.role_seed_applied` 一条。
**AC-08-96**（unit）不可委托与仅人类 — 对 `terminal_interact`、`preview_share_approve`、`machine_access_manage` 分别以「委托来源的人类」「持角色的 Agent」「服务账号」请求 `AuthorityChecker.Check` → 依次拒 `NON_DELEGABLE`、`AGENT_PROHIBITED`、`AGENT_PROHIBITED`；`workspace_file_edit` 对 Agent → `AGENT_PROHIBITED`，对持角色的人类 → 允许；`preview_share_approve` 主体 = 申请人 → `SELF_REVIEW`。
**AC-08-97**（integration）分享审批收件箱 — Given R 申请分享（03 `share_link.requested`），项目内持 `preview_share_approve` 的人类为 R、Q、O When 投影 Then 只有 Q、O 收到 `PREVIEW_SHARE_APPROVAL`（R 不收到）；Q 在收件箱批准 → 03 A72 成功，链接只在 Q 的界面显示一次；`share_link.decided` 到达后 O 的项 ≤1 s 移除；会话结束触发 `share_link.ended` → 未处理的项关闭。
**AC-08-98**（integration）危险操作登记 — G6 返回的登记表含 `terminal_interactive`、`machine_terminal_enable`、`machine_credential_issue` 三行，purpose 与 03 A67、04 A35 / A32 / A13 使用值一致；缺少任一 purpose 登记时模块启动失败。

### 10.14 D120 增补验收标准（操作清单、团队工具目录、Agent 运行时）

**AC-08-99**（integration）D120 权限播种 — Given 已有租户升级到含 D120 的 catalog When `governance.seed_reconcile` 运行 Then `team_owner` 增加 `agent_workspace_read`、`artifact_comment_write`、`interaction_request`、`tool_catalog_manage`；`delivery_lead`、`engineer` 与全部 Agent 角色增加 `artifact_comment_write`、`interaction_request`；`delivery_lead` **不含** `agent_workspace_read`、`tool_catalog_manage`；`HumanOnly` 含 `agent_workspace_read`、`tool_catalog_manage`，`NonDelegable` 含 `tool_catalog_manage`；再次运行无变化、不写审计。
**AC-08-100**（unit）仅人类与不可委托 — Given 自定义角色含 `tool_catalog_manage` 并绑给 Agent A When A 请求 `Check(tool_catalog_manage)` Then 拒 `AGENT_PROHIBITED`；授予委托 `actions:[tool_catalog_manage]` → 422（不可委托），Check 对委托来源返回 `NON_DELEGABLE`；A 请求 `agent_workspace_read` → `AGENT_PROHIBITED`；服务账号请求 F21 `{kind:FULL}` 所需的 `agent_manage` + step-up → `AGENT_PROHIBITED`。
**AC-08-101**（integration）负责人关系授权 — Given Agent Ada 的 `owner_principal_id`=Sam（角色 `engineer`，无 `agent_workspace_read`）；Morgan 为 `delivery_lead` When 二者请求 `Check(agent_workspace_read, resource=agent:Ada)` Then Sam 允许（来源为负责人关系，`Steps` 中 S4 记 `RELATION:owner`）；Morgan 拒 `MISSING_PERMISSION`；Ada 换负责人为 Lee 后 Sam 立即被拒。
**AC-08-102**（unit）§3.5c 映射夹具 — 对 §3.5 规范清单中的每个码调用映射函数 Then 与 §3.5c 表一致：`NO_AUTHORITY`、`TOOL_NOT_GRANTED` → `CAPABILITY_NOT_AUTHORIZED` / `ESCALATE_TO_OWNER`；`HUMAN_ONLY_MODE` → `CAPABILITY_NOT_AUTHORIZED` / `WAIT_FOR_HUMAN`；`SPAWN_QUOTA_EXCEEDED` → `QUOTA_EXHAUSTED`；`NOT_LEASE_HOLDER` → `NOT_APPLICABLE`；清单中不存在未映射的码（`NO_INTERNAL_QA_AGENT` 除外）；M03 判定期间 `Check` 以 tx=nil 调用，`SpawnGrantUsage.Reserve` 未被调用。
**AC-08-103**（integration）BR-08-67 启动校验 — Given 某模块向 `OperationRegistry` 登记 `claim.submit` 的 `permission.code=work_item_write`，而路由守卫与 Check 使用 `work_item_execute` When 服务启动 Then 启动失败，日志含操作名与两个码；登记 `via=SELF` 且 code 非空 → 同样失败；`human_only` 与 `HumanOnly` 集合不一致 → 失败。
**AC-08-104**（integration）危险操作登记与工具审计 — G6 登记表含 `agent_full_reset`（target `agent:<id>`）与 `tool_credential_manage`（target `tool_entry:<id>`）两行，purpose 与 05 F21 / T08 一致；无 step-up 调 T08 → 403 `140403`（`REAUTH_REQUIRED`）；经 05 中继的一次成功调用与一次未授予调用分别追加 `fleet.tool_called` 与 `fleet.tool_call_denied`（`reason_code=TOOL_NOT_GRANTED`），两者 detail 中不出现工具参数或结果原文（只有 `args_sha256`）；审计页「工具调用」筛选只返回这两类事件。
**AC-08-105**（e2e）菜单项 — Given 成员 X 有 `agent_read`、无 `tool_catalog_manage`；成员 Y 无 `agent_read` When 打开外壳 Then X 在「团队 › 团队资产」看到「工具与技能」（`/t/:tenantID/tools`，只读）、在「治理 › 策略」看到「Agent 操作清单」；Y 两项都不显示，直接访问显示无权限态「缺少权限 `agent_read`」。

## 11. 度量与埋点（不含代码、消息、反馈正文）

| 事件 | 属性 |
|---|---|
| `authority_check` | `code`, `actor_kind`, `allowed`, `source_kind`(ROLE/DELEGATION/POLICY/SYSTEM/none), `reason_code`, `latency_ms` |
| `delegation_granted` / `delegation_revoked`（PRD §15） | `actions_count`, `max_risk`, `duration_hours`, `end_reason`, `used_count`, `team_mode` |
| `delegation_used` | `code`, `decision_action`, `risk` |
| `takeover_executed` | `revoked_count`, `previous_mode`, `open_incidents` |
| `escalation_triggered` | `rule_kind`, `severity`, `team_mode`, `actions`, `deduped`(bool) |
| `escalation_closed` | `rule_kind`, `resolution`, `open_minutes` |
| `inbox_item_latency_ms` | `kind`（源事件 occurred_at → 收件箱行提交；SLO p95 ≤700ms） |
| `inbox_item_resolved` | `kind`, `resolution`, `via`(inbox/source/other_device), `open_seconds`, `keyboard`(bool) |
| `notification_sent` | `type_key`, `channel`, `merged_count`, `suppressed_by_quiet`(bool) |
| `audit_export_requested` | `range_days`, `valid` |
| `audit_index_lag_seconds`（gauge） | — |
| `role_seed_applied` | `manifest_version`, `added_codes_count` |
| `spawn_grant_granted` / `spawn_grant_ended`（D110） | `max_active_agents`, `budget_cap_usd`, `approval_mode`, `scope`(project/team), `end_reason`, `spawned_total`, `spent_pct` |
| `spawn_reserve`（D110） | `outcome`(ok/quota/daily/budget/inactive), `latency_ms` |

Prometheus：`governance_authority_checks_total{allowed,source_kind}`、`governance_inbox_projection_latency_seconds`（histogram）、`governance_consumer_dead_letters_total{consumer}`、`governance_active_delegations`（gauge）。

---

### 11.x D112 / D113 增补埋点

`andon_pulled{actor_kind}`、`andon_cleared{minutes_open}`、`escape_recorded{stage, category}`、`escape_learning_decided{state, days_pending}`、`plan_approval_decided{approver_kind, cross_family}`、`inbox_suppressed_internal{count}`（每日聚合，证明内部失败被拦截）。

**D120 增补埋点**：`agent_capability_denied{operation, reason_code, via, result}`（与 05 §11.z 同名事件共用口径，result 取 §3.5c）、`tool_call_audited{outcome, entry_kind}`（每日聚合，不含工具名以外的参数）、`agent_workspace_read{relation}`、`agent_full_reset_requested{reauth_ok}`；Prometheus：`governance_unmapped_reason_total{reason_code}`（§3.5c 告警）、`governance_manifest_permission_mismatch_total`（BR-08-67，启动期）。

## 12. 依赖、风险与待决

### 12.1 依赖

| 依赖 | 需要什么 | 状态 |
|---|---|---|
| 底座 IAM core | `AuthorizationEvaluator`（已有）；**新增** `EntityRoleBindingStore{PutOn, RemoveOn}`、`RoleSeeder{EnsureRoleOn, GrantPermissionOn}`、`TenantMemberLister`（CR-08-08） | 需底座改动 |
| 底座 authn | `ReauthenticationConsumer`（已有）；purpose 可由模块登记（CR-08-09） | 需底座改动 |
| 底座 audit | `Appender`、`EventStreamReader`（已有）；StreamEvent `Labels`（CR-08-07，缺省降级为按目标反查） | 可选改动 |
| 底座 governance | 访问申请（生产访问，已有）；按角色上限时长（CR-08-10，缺省由 Check 兜底） | 可选 |
| 01 | 事件（§7.3）；转换路径以 `project_create` / `intake_convert` 调 Check；step-up purpose `intake_convert_privileged` | 对齐 |
| 02 | `WorkItemReader`（含 `self_confidence`、计数、`LoadByPrincipal`）；Check 输入补 `DispatchedByActor`（经 05 `DispatchProvenance`）、`IntakeConvertMode`、`Paused`、`CostMicros`；02 N5 改由本模块发送；`work_item.escalated` | 对齐（00 v1.3） |
| 03 | `EvidenceReader.DiffSummary`（路径列表、truncated 标志）、`EvidenceReader.WorkItemCost`；事件 `verification.human_requested`、`execution.state_changed`；03 请求的 `BudgetGuard` 改用 07 `ProjectGate.CanStartExecution`（预算归 07，本模块不提供 BudgetGuard） | CR-08-06 |
| 04 | `MachineDirectory.Summary`；`machine_revoke`、`gateway_key_reveal` step-up | 对齐 |
| 05 | `AgentDirectory`（状态、AgentRole、负载）；向 `PolicyRegistry` 贡献 `dispatch_auto` / `ensemble_branch`；调用 `DelegationRevoker`；事件 `dispatch.proposed{policy, requires_human}`、`dispatch.expired`、`ensemble.*`（v1.1）；Agent 评审超时由 05 检测并调用 02 `WorkItemEscalator.Escalate(ctx, tx, {work_item_id, kind:REVIEW_SLA, reviewer_kind:AGENT})`，事件 `work_item.escalated` 由 02 发布（复审 #27）；05 登记 `SystemActions` `fleet.review_dispatch`、贡献 `review_dispatch` 规则 | 对齐（00 v1.3） |
| 06 | `memory.rejected` 事件；`memory_approve` 不可委托 | 对齐 |
| 05（D110） | `AgentDirectory` 增 `origin`(HUMAN_CREATED / AGENT_SPAWNED)、`supervisor_agent_id`、`spawn_grant_id`、`adopted_at`；建 Agent 事务内 Check(`agent_spawn`, `SpawnRequestFacts`) 后调用 `SpawnGrantUsage.Reserve`，退役 / 收编调用 `Release`；消费 `spawn_grant.updated{member_cap_reached}` 停止该 Agent 接新工作；事件 `agent.spawn_requested` / `agent.spawned` / `agent.spawn_rejected` / `agent.spawn_expired` / `agent.adopted` / `agent.orphaned`；审计 `fleet.agent_spawned` / `fleet.agent_adopted`；建 Agent 请求接口（供收件箱批准 / 驳回） | 需 05 落实 |
| 03（D110） | `execution.cost_updated` 载荷含 `agent_id` | 需确认 |
| 07 | `ProjectReader`（含 `HealthList`）、`ProjectGate.CanDelegatedDecide`、`ProjectModeSwitcher.TakeOverOn`、`ProjectHoldSetter.AddEscalationHoldOn`、`DeliveryMetricsReader`；事件 `project.hold_changed`、`project.mode_changed`、`budget.threshold_reached`、`delivery.confirmation_requested`；07 的「委托 72h 内到期」通知由本模块统一发送 | CR-08-06 |
| 09 | outbox 消费（`domain_event_receipts` 去重、每聚合有序、退避与停放）、SSE 主题授权、`Notifier`（站内存储、推送 / 邮件、渲染、合并窗口执行；读取本模块 `PreferenceResolver`）、平台幂等表、模块菜单贡献（§8.6）、`JobQueue`、`dlock` | 对齐（00 v1.3） |
| 05（D120） | `OperationRegistry` 与 M03 能力视图使用 §3.5c 映射与 BR-08-67 启动校验；`agent_workspace_read`（负责人关系授权）、`agent_full_reset` step-up；团队工具目录 T02–T10 以 `tool_catalog_manage` 守卫、T08 用 step-up `tool_credential_manage`；MCP 中继的 `TOOL_NOT_GRANTED` 与 `fleet.tool_called` / `fleet.tool_call_denied` 审计；菜单项 `tool-catalog`、`agent-operations`；通知 N-05-16…18 | 已在 05 D120 增补中定义 |
| 03（D120） | `artifact_comment_write`（锚定批注）、`interaction_request`（发起工具授权 / 输入请求，判定主体 = 执行者）；`execution.comment_*` 审计；通知 N13 / N14 | 已在 03 BR-03-134 / BR-03-148 定义 |
| 10（D120） | 循环契约审计 `planning.loop_contract_changed` / `planning.loop_run_escalated`；通知 N-10-11 / N-10-12 | 10 D120 增补 |
| 12（D120） | 批注转缺陷审计 `delivery.defect_created_from_comment`；A13 流转的 `interrupted` 原因不经本模块 | 12 D120 增补 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 收件箱 ≤1s 依赖 outbox 投递延迟 | 延迟埋点 + SLO 告警；对账任务 60s 兜底；前端乐观移除 |
| Check 成本（每次 IAM explain） | 单请求内缓存；IAM 已有 revision 快照；`ListEligible` 按 revision 缓存 60s |
| 敏感路径误报导致自治停摆 | 驳回路径 + 规则可调；默认集合偏保守，指标 `escalation_closed{resolution=DISMISSED}` 观测误报率 |
| 载体角色在 IAM 页被手工绑定 | Check 只认有委托行的载体匹配（BR-08-07）；建议 IAM 增加托管角色只读标记 |
| 播种与管理员编辑冲突 | 只增不减 + 漂移可见 + 可脱离托管 |

### 12.3 待决（附建议）

| # | 议题 | 建议 | 理由 |
|---|---|---|---|
| Q1 | ~~`delivery_lead` 是否持有 `project_mode_change` 与 `delegation_grant`~~ | **已决（00 v1.3 §6.2）**：两码移出 `delivery_lead` | — |
| Q2 | 00 §6.2 `*_read` 是否包含 `policy_read`/`audit_read` 给工程师 | 人类角色保留（透明度），Agent 角色排除（已按此播种） | 工程师需要看懂「谁能裁决」；Agent 最小授权 |
| Q3 | 人类自批默认 | `DENY`，单人团队由所有者显式切换 `ALLOW_WITH_REASON`（step-up + 每次理由） | 职责分离默认安全，同时不阻断独立开发者 |
| Q4 | 生产访问是否项目级 | MVP 租户级临时授予（底座现状）；v1.1 改为项目实体绑定 + 时长上限 | MVP 无生产功能消费，先保证默认拒绝 |
| Q5 | 授予者失权是否自动撤销委托 | 是（BR-08-23） | 委托的正当性来自授予者 |
| Q6 | 审计保留期 | 沿用底座默认 365 天，页面提示建议 ≥730 天 | 合同质保期常见 1–2 年；是否强制属所有者决定 |
| Q7 | `intake_convert` 委托（01 要求） | 已纳入 `Delegable`，仅 AUTONOMOUS、仅 NEW_REQUIREMENT | 与 01 BR-01-22、PRD §9.2 一致 |

---

## 13. 契约变更请求

> D110（主管 Agent 自建 Agent 的编制授权）已并入 00 v1.8：新增权限码 `spawn_grant_manage`（不可委托、仅人类）与 `agent_spawn`（仅策略来源，`PolicyOnly`）；事件 `spawn_grant.granted / updated / exhausted / revoked / expired`；接口 `SpawnGrantReader` / `SpawnGrantUsage`；`AuthorityReasonCode` 增 `SPAWN_*`、`SPAWNED_NOT_ADOPTED`；`InboxItemKind` 增 `AGENT_SPAWN_APPROVAL`（共 13 个）；`EscalationRuleKind` 增 `SPAWN_BUDGET`；step-up purpose `spawn_grant_manage`。

> 状态：CR-08-01…12 已于 00 v1.3 合入（CR-08-04 中仅 `EscalationRuleKind`、`AuthorityReasonCode` 跨模块共享，其余按 00 §3 作模块内枚举；`EscalationRuleKind` 另增 `EXECUTION_STALLED`；`InboxItemKind` 现为 §6.10 的 12 个；CR-08-06 中 `dispatch.rejected` 保留（00 v1.4 决议 R7））。

| # | 对 00 的位置 | 变更 | 理由 |
|---|---|---|---|
| CR-08-01 | §6.1 | 新增 `production_access`（governance 注册，DataScoped）；把 §6.4「生产环境访问」写为该码 | 环境维度需要可判定的码 |
| CR-08-02 | §6.3 | `ROLE:<role_key>` 的 key 定义：种子角色键；底座管理员 `tenant_administrator`；其他 `custom.<role_id>` | 自定义角色可改名，需稳定标识 |
| CR-08-03 | §6.2 | 显式列出各角色码表（以本规格 §3.3 为准）；新增 `production_operator` 与 5 个 `delegated_*` 载体角色；`agent_lead` 去掉 `work_item_assign`（改由 `POLICY:dispatch_auto`，与 §6.3 一致）；`agent_coordinator` 不授 `work_item_assign`；`team_owner`/`delivery_lead` 附加底座审计码；Q1 的两码移出 `delivery_lead` | 去除通配歧义、对齐 POLICY 来源语义 |
| CR-08-04 | §5 | 新增枚举（所有者 08）：`Environment`(DEFAULT, PRODUCTION)、`DelegationEndReason`、`EscalationRuleKind`(BUDGET, VERIFICATION_INCONCLUSIVE, CLIENT_REJECTION, REWORK_LIMIT, SPINNING, REVIEW_SLA, LOW_SELF_CONFIDENCE, SENSITIVE_PATH, NO_ELIGIBLE_AUTHORITY)、`EscalationAction`(NOTIFY, HOLD_DELEGATIONS, PROJECT_HOLD)、`EscalationIncidentState`、`IncidentSeverity`(WARNING, CRITICAL)、`InboxItemKind`(§6.10 共 8 个)、`InboxItemState`、`NotificationPriority`(NORMAL, HIGH, CRITICAL)、`NotificationEmailMode`(OFF, IMMEDIATE, DAILY_DIGEST)、`AuthorityReasonCode`(§3.5 表) | 跨模块共享（07 读 RuleKind，02/04 透传 reason_code） |
| CR-08-05 | §7.1 | 新增 `delegation.expired`、`escalation.resolved`、`takeover.executed`（governance）；`budget.threshold_reached` 发布者改为 delivery（同 07 CCR-07-05） | 消费方需区分到期与撤销；incident 关闭需信号 |
| CR-08-06 | §7.1 / §8 | 新增事件 `verification.human_requested`（03）、`delivery.confirmation_requested`（07）、`dispatch.rejected`（05，人工驳回提案；保留，作为派单收件箱项关闭条件之一）、`memory.rejected`（06）；`AuthorityChecker` 签名按 §3.5（`Check(ctx, tx, req)`、`ListEligible`）；新增 `DelegationReader`、`EscalationRuleReader`、`PolicyRegistry`、`SystemActions`（governance 提供）；删除 03 请求的 governance `BudgetGuard`，改用 07 `ProjectGate` | 冻结窄接口；预算归 07 |
| CR-08-07 | §7.2 | 业务审计 detail 必含 `project_id`、`actor_kind`、`authority_source`（`delegation_id` 于委托来源）；请求底座 `StreamEvent` 增加白名单 `Labels{project_id, authority_source}` | 项目级审计视图的索引来源 |
| CR-08-08 | 底座（经 09 / IAM 负责人） | IAM core 发布 tx 绑定的 `EntityRoleBindingStore`、`RoleSeeder`、`TenantMemberLister`；可选「租户已创建」钩子 | 委托与播种须与业务同事务；接收人解析需成员枚举 |
| CR-08-09 | 底座 authn + 前端 | 重新认证 purpose 改为模块登记（如 `authn.RegisterReauthenticationPurpose`），登记 §6.9 全部 purpose；`ReauthenticationDialog` 迁入 `packages/ui` | 现白名单固定 5 个；delivery 应用需复用对话框 |
| CR-08-10 | 底座 governance | 访问申请支持按角色设最长授权时长 | 生产访问 ≤8h |
| CR-08-11 | §6.4 | 不可委托清单补入 `intake_decline`、新建项目（伪码 `project_create`）、`memory_approve`（同 01 CR-10、06 CR-06-06）；`Delegable` 写明 `intake_convert` 的条件 | 与 01 / 06 对齐，单一清单 |
| CR-08-12 | §3 | 时间类型改为 `TIMESTAMPTZ(3)` / `DATETIME(3)` / `DATETIME`（同 01 CR-01、02 CCR-01）；§9 tag 规则改为发布路径首段（同 02 CCR-08） | 与仓库规约及已实施的路由规则一致 |
| CR-08-14 | §6.1 / §6.4 / §6.5 / §9 / 08 §6.9 / §8.6（D120） | §6.1 新增 `agent_workspace_read`（fleet，团队级，仅人类，负责人关系授权）、`artifact_comment_write`（execution，DataScoped）、`interaction_request`（execution，DataScoped）、`tool_catalog_manage`（fleet，团队级，不可委托、仅人类）；§6.4 增 `tool_catalog_manage`；§6.5 增「Agent 完全重置」「团队工具目录管理与凭据」「查看 Agent 工作目录 / 收件箱 / 记忆」；原因码 `TOOL_NOT_GRANTED` 与 §3.5c 结果类别 `CAPABILITY_NOT_AUTHORIZED` / `QUOTA_EXHAUSTED` / `NOT_APPLICABLE`；step-up `agent_full_reset`、`tool_credential_manage`；菜单项 `tool-catalog`、`agent-operations` | D120：操作清单需可判定的权限码与统一拒绝口径；工具凭据与 Agent 内部材料只给人 |

> **v2.0（D112 / D113，00 §15）已并入本规格**：权限码登记与授予（§3.3b）、判定规则 S2(d) / S3(f) / 策略 `plan_review` / `internal_qa` 与新原因码（§3.5b）、穿透记录表（§3.8.16）、错误码 160480–160483、BR-08-60…65、接口 Q1–Q3、收件箱类型（§6.10b）、任务与事件（§7.6）、页面（§8.7）、通知（§9.4）、AC-08-78…87。**CR-08-13**（对 00）：§15.5 增补原因码 `ACCOUNTABLE_HUMAN_MISSING`、`SAME_MODEL_FAMILY`、`ANDON_ACTIVE`（02）；事件 `quality.escape_recorded`、`quality.escape_learning_decided`；05 `AgentDirectory` 需暴露 `model_family`；02 需在 `WorkItemFacts` 填 `AccountablePrincipalID`、`PlanAuthorID`，并在 `work_item.state_changed{to:REWORK}` 载荷携带 `cause`。

> **D118（控制台信息架构，2026-10-12）已并入本规格**：§8.6 改为顶栏域标签（工作台 · 会话 · 协作 · 项目 · 团队 · 治理）+ 当前域侧栏子导航，菜单表增「域」列并按 D118 重新归位，补登 01 / 11 / 12 / 08 已有页面与新页面（我的任务、需求、缺陷、流水线、会话）；协作域项目筛选、记住子页面、移动端抽屉与底栏 工作台 · 会话 · 协作 · 裁决；AC-08-61 更新，新增 AC-08-91…93。依赖 09 C6 `MenuPage.Domain`（09 BR-09-80）与 05 §8.8 会话页；对 00 无新增变更请求。 **D118 修订（项目是主容器，2026-10-12）已并入**：删除协作域，域标签为 工作台 · 会话 · 项目 · 团队 · 治理；菜单表增「范围」列（团队 / 项目），全部行重新归位：项目域分项目外（项目组合 · 来件 · 交付总览 / 跨项目 需求 · 任务 · 缺陷 · 流水线）与项目空间（路由带 `:projectID`），团队域增目标树 · 报告中心；当前域与当前项目只由路由决定、详情页在所属项目空间内渲染、切换项目保持子页面、〔返回项目组合〕、项目切换器；移动端底栏 工作台 · 会话 · 项目 · 裁决；AC-08-61、AC-08-91…93 原地更新，新增 AC-08-94。依赖 09 C6 `MenuPage.Space`（09 BR-09-80）与 05 §8.8 项目频道重定向；对 00 无新增变更请求。

> **D119（远程预览、终端与文件；服务器与集群执行机器，2026-10-12）已并入本规格**：权限码登记与默认授予（§3.3c：`terminal_interact`、`workspace_file_edit`、`preview_share_approve`、`machine_access_manage`）、原因码 `MACHINE_ACCESS_DISABLED` / `PORT_NOT_OWNED` / `PATH_OUTSIDE_WORKSPACE`（§3.5）、BR-08-66、step-up 用途 `terminal_interactive` / `machine_terminal_enable` / `machine_credential_issue`（§6.9）、收件箱 `PREVIEW_SHARE_APPROVAL`（§6.10b，分期待定）、审计事件（§7.5）、通知（§9.4）、AC-08-95…98。来源 03 CR-03-09、04 CR-04-12；已并入 00 v2.0 D119 补充。


> **D120（Agent 运行时协议、操作清单、锚定批注、生命周期、循环契约与团队工具目录，2026-10-12）已并入本规格**：US-08-26…28；权限码登记与默认授予（§3.3d：`agent_workspace_read`、`artifact_comment_write`、`interaction_request`、`tool_catalog_manage`）；原因码 `TOOL_NOT_GRANTED`（§3.5）；操作清单能力判定映射与 `via` 取值（§3.5c）；BR-08-67…69（§5.y）；step-up 用途 `agent_full_reset`、`tool_credential_manage`（§6.9）；审计事件（§7.5）；菜单项 `tool-catalog`（`/t/:tenantID/tools`）与 `agent-operations`（§8.6）；通知类型（§9.2）；AC-08-99…105；D120 埋点（§11.x）；依赖（§12.1）；CR-08-14（对 00）。
