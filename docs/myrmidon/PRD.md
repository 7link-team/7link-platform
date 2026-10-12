# Myrmidon 产品需求文档（PRD）

> 乙方交付团队操作系统：人类与 AI Agent 混编（或全 Agent）组成乙方团队，承接甲方需求，以证据和授权裁决推进到交付。

版本：1.0（整合设计稿，待评审）｜日期：2026-10-11｜原型：[`prototype.html`](prototype.html)

---

## 0. 文档信息

### 0.1 输入来源与采信规则

| 来源 | 内容 | 本文如何使用 |
|---|---|---|
| **A. 评审修订稿**（本仓 `docs/01–04_Myrmidon_*.docx`，2026-10-10） | 评审报告、概念论文、立项基线、商业计划书 | **最新方向，优先采信**：控制层定位、领域对象、完成语义、权限、返工分支、ICP、指标、MVP 范围 |
| **B. 旧项目规格**（`MyrmidonAI/PRD.md` v2.0、`chaosplus.md`） | 执行核原则（Artifact 即真相、验证决定完成、有界自治、事件日志）、产品功能树、机器接入、数字人生命周期、频道 | 采纳与 A 不冲突的原则与产品细节；冲突处以 A 为准，列入 §18 |
| **C. 旧项目实现**（`MyrmidonAI` Preview 01 + API 网关） | 远程 Server + 多 Computer、跨机会话、工具审批回传、能力检测、模型目录、Responses 网关 | 作为**执行面**的已验证能力迁入：交互式执行、机器、运行时适配 |
| **D. 底座框架**（本仓 `apps/backend-api`、`apps/frontend-web-admin`） | IAM、多租户、实体树、授权目录、服务账号、OAuth（含设备码）、访问审批、哈希链审计、webhook、文件与资源绑定、主题与组件库 | **全部复用**；新产品只新增领域模块 |

> 旧项目代码与文档只读，不做修改；本文与原型是全新的整合设计。

### 0.2 文档体系

| 文档 | 作用 |
|---|---|
| `PRD.md`（本文） | 总纲：定位、闭环、领域模型、原则、分期、待决 |
| `specs/00-conventions.md` | **共享契约**：模块与表归属、共享枚举、权限码、事件、跨模块接口、接口/错误码/前端约定、底座改动清单 |
| `specs/01-intake-portal.md` | 需求开口、来件、分诊、澄清、甲方门户 |
| `specs/02-work-review.md` | 需求基线、工作项、分派、完成声明、裁决、返工、评审台 |
| `specs/03-execution-evidence.md` | 执行与分支、交互会话、证据、验证、上下文注入 |
| `specs/04-machines-runtime.md` | 机器接入（设备码）、机器通道、运行时与能力、执行槽、API 网关 |
| `specs/05-agents-orchestration.md` | Agent 生命周期、主导 Agent 派单、Heavy 并行求解、频道 |
| `specs/06-knowledge-memory.md` | 知识库、三方言检索、记忆治理、技能库 |
| `specs/07-projects-delivery.md` | 项目、团队模式、预算、交付包、甲方验收、指标 |
| `specs/08-governance.md` | 权限判定、委托、升级规则、审计、通知、我的裁决、总览、控制台外壳 |
| `specs/09-platform-capabilities.md` | 底座通用能力：任务队列、outbox、SSE、对象存储、通知、菜单贡献、幂等 |

本文与规格冲突时，**以规格为准**（规格经过跨模块一致性审校）；规格之间冲突时以 `00` 为准。

### 0.3 一句话方向（产品负责人确认）

> **主体是乙方团队：agents + humans 混编，或全 AI agent 的无人团队。甲方是谁不重要——对系统来说，甲方就是一个"需求开口"，以及交付物回去的地方。**

---

## 1. 背景与整合原则

### 1.1 问题

AI Agent 能写代码、跑测试、开 PR，但"交付一个被客户接受的结果"还依赖需求澄清、责任分配、证据、授权裁决、返工与验收（A-概念论文 §1）。今天的乙方团队把 Agent 散落在若干终端和聊天窗口里：

- **完成语义混乱**：Agent 说"做完了"，不等于测试通过，更不等于客户接受（A-评审 P0）。
- **监督成本不可见**：负责人不知道每个被接受的交付物花了多少 token、多少人工分钟。
- **返工即重跑**：拒绝原因、旧产物和上下文在重试中丢失。
- **算力被锁在一台电脑上**：Agent CLI 只能在眼前那台机器上跑（C 已解决的问题）。

### 1.2 整合原则

| # | 原则 | 来源 |
|---|---|---|
| I1 | **控制层，不是新 runtime**：Claude Code / Codex 等经适配器接入，Myrmidon 只管项目、执行、证据、裁决、交付 | A-概念 §10、B-P9 |
| I2 | **完成分四层**：Agent 声明 → 自动验证 → 团队裁决 → 甲方验收；任何一层都不能被下一层以外的主体替代 | A-评审 P0、B-P1/P3 |
| I3 | **裁决是权限动作，不是按钮**：每个决定记录主体、权限来源、理由、引用证据 | A-概念 §2、§7 |
| I4 | **证据支持声明，不等于正确**：证据带来源、哈希、验证器版本与局限 | A-评审 P0 |
| I5 | **返工分类、保留历史**：质量拒绝 / 范围变更 / 策略变更 / 执行故障 / 阻塞分别处理，旧产物不被覆盖 | A-概念 §5 |
| I6 | **执行与界面解耦**：关闭页面、断网不影响执行；任何设备都能接着处理 | C-Preview 01 |
| I7 | **框架优先**：身份、租户、授权、审计、文件、主题全部复用底座，不另起一套 | D、CLAUDE.md §4 |
| I8 | **不过度设计**：MVP 用关系表 + 领域事件表 + 投影，不上全量事件溯源与消息平台 | A-评审 P1 |

---

## 2. 产品定位

### 2.1 定位

**Myrmidon = 乙方交付团队操作系统（Delivery Team OS）。** 它是跨 Agent runtime 的项目执行与治理控制层，让一支人机混编的乙方团队：

1. 从任意**需求开口**接住甲方需求；
2. 把需求拆成可分配、可验证的**工作项**；
3. 分派给**人或 Agent**，在团队的**机器**上执行；
4. 收集**证据**、运行**验证**；
5. 由**有权限的主体裁决**，必要时**分支返工**；
6. 打包**交付**，由甲方**验收**。

### 2.2 甲方 = 需求开口

系统不建模"甲方组织"的内部结构。甲方在系统中只有三种痕迹：

| 痕迹 | 说明 |
|---|---|
| **来件（Intake）** | 一条需求从某个开口进入：门户表单、邮件、IM、Webhook、API |
| **澄清往来** | 团队（人或 Agent）向来件联系人提问、对方回答 |
| **验收回执** | 交付包发回开口，对方接受 / 拒绝并附理由 |

> 甲方联系人可以是人，也可以是另一个系统（如对方的工单平台通过 API 提交、通过 webhook 接收回执）。

### 2.3 两种团队模式（按项目设置）

| 模式 | 团队构成 | 团队内裁决 | 甲方验收 |
|---|---|---|---|
| **混编 Hybrid**（默认） | 人类 + Agent | 人类按角色裁决；低风险项可委托 Agent | 甲方 |
| **全自治 Autonomous** | 仅 Agent（人类可只做所有者，不参与日常） | **授权委托给 Agent 评审员角色**：有范围、有期限、可撤销、全审计；触及升级规则必须暂停 | 甲方 |

全自治模式的硬约束（不可配置关闭）：

- Agent **不得**裁决自己执行产出的工作项（职责分离）。
- **范围变更、合同变更、生产访问、正式交付**不可委托给 Agent（A-立项 §7）。
- 预算触顶、连续返工、验证结果 INCONCLUSIVE、甲方拒收 → 自动暂停并通知所有者。
- 所有者一键"接管"：撤销全部委托，项目切回混编。

### 2.4 非目标（A-立项 §4 + 本次补充）

自建模型；Agent 市场；跨组织联邦与结算；完整 Matrix 服务器；通用数据库分支与 token 级 checkpoint；自动报价与签约；**甲方组织管理与甲方侧多角色**；可视化工作流编辑器（v2 以后）。

---

## 3. 用户与角色

### 3.1 首发 ICP

**AI 软件工作室 / 软件外包团队**（A-商业 §2 推荐，最贴近"乙方入口"设想）：3–20 人，同时承接 3+ 个客户项目，已日常使用 Claude Code / Codex，客户要求可追溯验收。

### 3.2 乙方团队成员（系统内主体）

| 角色 | 主体类型 | 典型职责 | 默认权限要点 |
|---|---|---|---|
| **团队所有者** | 人 | 管理团队、机器、预算、策略；最终兜底 | 全部 |
| **交付负责人** | 人 | 接件、拆解、分派、裁决、交付 | approve / reject / scope_change / deliver |
| **工程师** | 人 | 执行工作项、审阅 Agent 产出 | execute / verify / request_changes |
| **Agent 执行者** | Agent | 执行被分配的工作项、提交证据与完成声明 | execute / submit_evidence；**默认禁止裁决** |
| **Agent 评审员** | Agent | 低风险预检；全自治模式下经委托做团队裁决 | verify；approve/reject **仅经委托** |
| **Agent 协调员** | Agent | 来件分诊、拆解草案、频道答疑、催办 | triage / draft；不可改范围 |
| **主导 Agent**（Lead，UI 称「主管 Agent」） | Agent | 拆解工作项、按能力与负载**分派给其他 Agent**、发起多模型并行求解并择优、汇总进展（§10.6）；**在人授予的编制授权内创建子 Agent**（§10.8） | assign（策略内）、agent_spawn（仅经编制授权）；不可裁决自己团队产出，除非另有委托 |

### 3.3 Persona

| Persona | 场景 | 首要页面 |
|---|---|---|
| **交付负责人「Morgan」**（AI 工作室合伙人，they/them） | 早上打开总览：3 个裁决待办、1 个甲方澄清待回、Agent 夜里推进了 11 个工作项 | 总览、评审台 |
| **工程师「阿澈」**（they/them） | 通勤路上在手机上批准一次文件写入；回到工位接手一个被拒绝的工作项分支 | 执行、工作项 |
| **团队所有者「Sam」**（they/them） | 新接一台 Mac Studio 作为机器；给周末项目开启全自治模式，设置预算与升级规则 | 机器、项目策略 |
| **甲方联系人「Lee」**（they/them，系统外） | 在门户提交需求，回答两个澄清问题，两周后验收交付包 | 甲方门户 |

---

## 4. 核心闭环与完成语义

### 4.1 端到端闭环

```
 甲方开口                       乙方团队（人 + Agent）                               甲方开口
┌────────┐   ┌──────────┐   ┌──────────┐   ┌────────────┐   ┌──────────┐   ┌──────────┐   ┌────────┐
│ 来件   │──▶│ 分诊澄清 │──▶│ 需求基线 │──▶│ 工作项拆解 │──▶│ 执行     │──▶│ 证据验证 │──▶│ 团队裁决│
│Intake  │   │Triage    │   │Requirement│  │WorkItem    │   │Execution │   │Evidence  │   │Decision │
└────────┘   └──────────┘   └──────────┘   └────────────┘   └──────────┘   │Verification│ └───┬────┘
                                                                ▲          └──────────┘       │
                                                                └──── 返工分支 Rework ◀── 拒绝 ┤
                                                                                              ▼ 通过
                                                    ┌──────────┐   ┌──────────────┐   ┌──────────┐
                                                    │ 甲方验收 │◀──│ 交付包       │◀──│ 已接受   │
                                                    │Acceptance│   │Delivery      │   │ACCEPTED  │
                                                    └──────────┘   └──────────────┘   └──────────┘
```

### 4.2 完成阶梯（全产品统一组件）

| 层级 | 名称 | 由谁产生 | UI 文案 | 颜色语义 |
|---|---|---|---|---|
| L1 | **Agent 声明完成** | 执行者（人或 Agent） | 「已声明完成（未验证）」 | 中性 |
| L2 | **验证通过** | 验证器（自动 / AI 辅助，记版本） | 「验证通过 n/n」 | 青 |
| L3 | **团队已接受** | 有 `decision_approve` 权限的主体 | 「已接受 · Morgan」 | 绿 |
| L4 | **甲方已验收** | 甲方开口回执 | 「甲方已验收」 | 绿 + 盾形 |

规则：L3 不能跳过 L2 中的必需验证；L4 只对交付包中的工作项生效；**UI 永远不把 L1 显示成"完成 ✓"**。

---

## 5. 领域模型

### 5.1 对象与关系

```
Team(=Tenant) ─┬─ Member(Human Principal | Agent ServiceAccount)
               ├─ Machine ─ Runtime ─ Capability(Skills/MCP) ─ ModelCatalog
               ├─ IntakeChannel ─ Intake ─ ClarificationThread
               ├─ Policy(Role, Delegation, EscalationRule, Budget)
               └─ Project(=Entity type "project")
                    ├─ Requirement(versioned baseline, AcceptanceCriteria[])
                    │     └─ WorkItem ─┬─ Assignment(member, role, window)
                    │                  ├─ Execution(attempt tree: parent_execution_id, branch_reason)
                    │                  │     ├─ InteractiveSession(Turn, Interaction)   ← Preview 01 迁入
                    │                  │     └─ Artifact(code/PR/doc/build)
                    │                  ├─ Claim(L1) ─ Evidence[] ─ Verification[]
                    │                  └─ Decision[](approve/reject/request_changes/pause/resume/scope_change)
                    ├─ Channel ─ Message
                    ├─ Delivery(package of accepted WorkItems + evidence bundle) ─ Acceptance(甲方回执)
                    └─ ChangeRecord(scope/strategy change, requirement version bump)
```

### 5.2 对象定义（字段为产品语义，非最终 schema）

| 对象 | 关键字段 | 说明 |
|---|---|---|
| **Intake 来件** | channel、raw_content、attachments、contact_ref、received_at、triage_summary、status | 甲方需求的原始形态；保留原文不可改 |
| **Requirement 需求** | project、title、description、acceptance_criteria[]、version、source_intake、priority | 基线可版本化；范围变更产生新版本 |
| **WorkItem 工作项** | requirement、title、type（feature/bug/doc/research）、estimate、risk（low/med/high）、state、parent | 可分配、可执行、可验证的最小单元 |
| **Assignment 分派** | work_item、member、role（executor/reviewer）、starts/ends、lease | 一个工作项可有多个分派；租约防重复认领 |
| **Execution 执行** | work_item、assignee、machine、runtime、model、parent_execution、branch_reason、state、cost、started/ended | 一次具体运行；返工产生**子执行**，旧执行保留 |
| **Interactive Session 交互会话** | execution、turns[]、interactions[] | 人与 runtime 的多轮对话（Preview 01 能力）；也可独立存在于"临时执行" |
| **Claim 完成声明** | work_item、execution、summary、claimed_by、claimed_at | L1 |
| **Evidence 证据** | claim、type（commit/pr/test_report/log/screenshot/observation）、producer、content_hash、uri、scope、limitations | 附件经文件模块 ResourceBinding 绑定 |
| **Verification 验证** | target（claim/artifact）、verifier、verifier_version、layer（automated/ai_assisted/human）、inputs、result（PENDING/PASSED/FAILED/INCONCLUSIVE）、output | AI 辅助结果只作参考（A-评审 P1） |
| **Decision 裁决** | target、action、actor、authority_source（role/delegation id）、reason、evidence_refs[]、failure_category? | 必须先通过授权检查；写入审计链 |
| **Delivery 交付包** | project、work_items[]、evidence_bundle、release_notes、sent_via、state | 发往需求开口 |
| **Acceptance 验收** | delivery、result（accepted/rejected/partial）、comments、received_via、at | 甲方回执；拒收生成新的来件或范围变更 |
| **Machine 机器** | name、os、address、runtimes[]、slots、state、last_seen、credential | 执行面；Agent 绑定其上 |
| **Agent** | name、persona、system_prompt、machine、runtime×model、skills[]、memory_policy、roles[]、delegations[]、state | 一个 Agent = 一个服务账号主体 |

### 5.3 旧概念到新模型

| 旧概念（B/C） | 新模型 |
|---|---|
| Instance / Account | Team = 底座 Tenant；账号 = 底座 Principal |
| Project / Workspace | Project = 底座 Entity（type=project），workspace_uri 存为实体元数据 |
| Machine Runner / Computer / 执行节点 | **Machine** |
| 数字人 Agent | **Agent**（长期成员，服务账号身份） |
| 执行器 Agent（瞬时） | Execution 中的 runtime 进程，不是成员，不可 @ |
| WorkflowDef / Run / Node | **工作项拆解模板**（如 software-dev-agile）：生成 WorkItem 树与依赖；运行期以工作项状态推进，不再暴露独立 DAG 引擎（见 §18 Q3） |
| Artifact / validation_result | Artifact + Evidence + Verification |
| human_approval / review.approve | Decision |
| Session / Turn / Interaction（Preview 01） | Interactive Session（挂在 Execution 下，或作为"临时执行"） |
| Channel `# ALL`、@mention | 保留：项目频道 |
| API 网关（实验） | 机器能力的对外复用，保留为实验功能 |

---

## 6. 状态机

### 6.1 Intake 来件

```
RECEIVED ─分诊→ TRIAGED ─需澄清→ CLARIFYING ⇄(甲方回复) ─→ ACCEPTED_AS_REQUIREMENT
    │                │                                       （生成/追加 Requirement）
    └──────────────┴─拒绝(理由必填)→ DECLINED      重复→ MERGED(指向已有来件)
```

### 6.2 Project 项目（A-立项 §6）

`INTAKE → DISCOVERY → PLANNING → ACTIVE ⇄ BLOCKED → ACCEPTANCE_PENDING → DELIVERED → CLOSED`；任意非终态 → `CANCELLED`。项目状态由**授权决策**推进，不由工作项状态机械推导。

### 6.3 WorkItem 工作项（A-立项 §6，所有者标注）

| 状态 | 进入条件 | 允许动作 | 状态所有者 |
|---|---|---|---|
| READY | 依赖满足、已估算 | 认领、分派 | 交付负责人 |
| CLAIMED | 分派生效、租约获得 | 开始、释放 | 执行者 |
| IN_PROGRESS | 执行已启动 | 暂停、提交声明 | 执行者 |
| AGENT_CLAIMED_DONE | 提交 Claim + ≥1 证据 | — | 系统 |
| VERIFYING | 必需验证器排队/运行中 | — | 系统 |
| NEEDS_HUMAN_REVIEW | 必需验证 PASSED（或 INCONCLUSIVE 需人判） | 裁决 | 有裁决权主体 |
| ACCEPTED | approve | 纳入交付包 | — |
| REWORK | reject / request_changes（质量） | 创建子执行 | 交付负责人 |
| BLOCKED | 依赖/澄清缺失 | 解除 | 交付负责人 |
| EXECUTION_FAILED | runtime/环境故障 | 重试、转交 | 执行者 |
| CANCELLED | 取消裁决 | — | — |

硬规则：执行者（尤其 Agent）**只能**把工作项推进到 AGENT_CLAIMED_DONE；→ ACCEPTED 必须是 Decision。

### 6.4 Execution 执行

`CREATED → RUNNING ⇄ PAUSED → SUCCEEDED | FAILED | INTERRUPTED | CANCELLED`。SUCCEEDED 仅表示执行器层结果。交互会话内部沿用 Preview 01 状态（queued / running / waiting / completed / failed / cancelled / interrupted），映射：waiting → RUNNING（附"待你决定"标记）。

### 6.5 Verification

`PENDING → PASSED | FAILED | INCONCLUSIVE`；记录规则、工具版本、输入证据、输出摘要。

### 6.6 Decision

动作：`APPROVE / REJECT / REQUEST_CHANGES / PAUSE / RESUME / SCOPE_CHANGE / DELIVER`。每次：授权检查 → 记录理由（REJECT / SCOPE_CHANGE 理由必填）→ 写审计链 → 驱动目标状态迁移。

### 6.7 Delivery 与 Acceptance

`DRAFT → READY(内部 deliver 裁决) → SENT → ACCEPTED | REJECTED | PARTIALLY_ACCEPTED`。REJECTED / PARTIAL → 自动生成新来件（标记"返工来件"）关联原交付。

### 6.8 机器接入（OAuth 设备码，B §5.3.1 的 300 秒流程落在底座设备码授权上）

```
机器运行 `machine-runner login --server <url>`（命令名品牌中立，显示名取部署配置）─▶ 显示 8 位用户码 + 链接（300s 有效）
控制台「接入机器」输入或扫码确认 ─▶ 接入待确认（显示 hostname/OS/检测到的 runtime，可改名）
   ├─ 确认 ─▶ OFFLINE → 机器建立会话后 ONLINE（签发机器专属服务账号凭证，长期有效，可轮换/吊销）
   ├─ 取消 ─▶ DENIED（设备码作废）
   └─ 超时 ─▶ EXPIRED（需重新发起）
ONLINE ⇄ OFFLINE（心跳）；ONLINE → DRAINING → OFFLINE；任意 → REVOKED
```

### 6.9 Agent 生命周期（B §6.2.1）

> 各状态机的完整迁移表、守卫与错误码见 `specs/`（本节为摘要）。

`DRAFT → ACTIVE(running) ⇄ STOPPED；ACTIVE → CRASHED → (restart)；→ RETIRED`。正常退役：生成交接文档（作为 Artifact 入库）→ 选择接手人 → 清理机器上的 Agent 文件 → 吊销凭证 → 标记退役；强制退役：红色确认 + 输入名称。

---

## 7. 失败分类与返工分支（A-概念 §5）

| 类别 | 触发 | 系统处理 | UI |
|---|---|---|---|
| **执行故障** Execution failure | runtime 崩溃、机器离线、超时 | EXECUTION_FAILED；**仅当 runtime 尚未启动**（如派发失败、启动失败）才按策略自动重试 ≤N 次（不消耗质量重试次数）；已开始执行后被中断（可能已有副作用）一律转人确认，不自动重放（§16） | 执行卡片红色 + 「重试 / 换机器 / 转交」 |
| **质量拒绝** Quality rejection | 裁决 REJECT/REQUEST_CHANGES | 保留原执行与证据，创建**子执行**（branch_reason=quality），结构化反馈注入上下文 | 执行分支树新增一枝 |
| **范围变更** Scope change | 甲方新要求 / 负责人调整 | 生成 ChangeRecord，Requirement 版本 +1，受影响工作项标"基线过期" | 需求页版本对比 |
| **策略变更** Strategy change | 换方案（如换库、换架构） | 保留旧分支，创建并行**替代分支**，完成后对比证据择一 | 分支对比视图 |
| **阻塞** Blocked | 依赖未完成、需澄清 | BLOCKED；自动向来件联系人或负责人发起澄清 | 阻塞原因 + 「发起澄清」 |

**结构化反馈**（拒绝必填）：`category*`（功能缺陷 / 需求偏差 / 样式 / 性能 / 安全 / 其他）、`location`、`expected`、`detail*`（B §13）。

**有界自治**（B §13，保留）：同一工作项质量返工 ≥3 次、或相邻两次执行产物变化文件比例 < 8%（原地打转）→ 暂停并升级。

---

## 8. 权限与授权委托

### 8.1 动作集合（A-概念 §7）

`observe · execute · assign · submit_evidence · verify · approve · reject · request_changes · change_scope · pause · stop · merge · deliver · administer`

### 8.2 默认权限矩阵（拒绝默认优先）

| 动作 | 团队所有者 | 交付负责人 | 工程师 | Agent 执行者 | Agent 评审员 | Agent 协调员 |
|---|---|---|---|---|---|---|
| observe 读取项目与工作项 | ✅ | ✅ | ✅ | 按分派范围 | 按项目 | 按项目 |
| execute 执行已分派工作项 | ✅ | ✅ | ✅ | ✅ 受沙箱约束 | — | — |
| assign 分派 | ✅ | ✅ | — | — | — | 草案（需确认） |
| submit_evidence | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| verify 运行验证 | ✅ | ✅ | ✅ | 自动化规则 | ✅ | — |
| approve / reject | ✅ | ✅ | 低风险（策略） | ❌ | **仅经委托** | ❌ |
| request_changes | ✅ | ✅ | ✅ | ❌ | **仅经委托** | ❌ |
| change_scope | ✅ | ✅ | ❌ | ❌ | ❌（不可委托） | ❌ |
| pause / stop 执行 | ✅ | ✅ | 自己的 | 自己的 | ✅ | — |
| merge 合并到主分支 | ✅ | ✅ | 策略 | ❌ | **仅经委托** | ❌ |
| deliver 正式交付 | ✅ | ✅ | ❌ | ❌ | ❌（不可委托） | ❌ |
| 生产密钥 / 生产环境 | 单独授权 | 单独授权 | ❌ | ❌ | ❌ | ❌ |
| administer 团队/机器/策略 | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| 授予编制授权（名额 / 预算） | ✅（不可委托） | ❌ | ❌ | ❌ | ❌ | ❌ |
| 创建 Agent | ✅ | ✅ | ❌ | ❌ | ❌ | ❌（仅主管 Agent 在编制授权内） |

### 8.3 授权委托（Delegation）

委托 = 把某个裁决动作在**限定范围**内临时授予 Agent 评审员：

| 字段 | 说明 |
|---|---|
| grantee | Agent（服务账号） |
| actions | approve / reject / request_changes / merge 的子集 |
| scope | 项目、工作项类型、风险等级 ≤ low/med、单项成本上限 |
| window | 生效与失效时间 |
| conditions | 必需验证全部 PASSED；非自产工作项；同一工作项返工 <3 |
| granted_by / reason | 授予人与理由 |

委托在底座上落为**临时角色授权**（访问申请审批模块的 `access_expires_at` 模型 + 条件策略），撤销即时生效，全部动作入审计链。每个经委托做出的 Decision，`authority_source` 指向委托 id。

### 8.4 升级规则（全自治模式）

预算 ≥ 80% 通知、100% 暂停；INCONCLUSIVE 验证；甲方拒收；返工超限；Agent 置信度自评低于阈值；涉及迁移脚本、依赖升级、权限相关代码 → 转交所有者。

---

## 9. 需求开口（Intake）

### 9.1 开口类型

| 开口 | 接入方式 | 回执方式 | MVP |
|---|---|---|---|
| **甲方门户** | 团队专属链接 + 一次性访问码；甲方无需注册账号 | 门户内 | ✅ |
| **邮件** | 专属收件地址，正文 + 附件解析 | 邮件回复 | ✅ |
| **API** | `POST /intakes`（PAT / OAuth 客户端） | Webhook 回调 | ✅ |
| **Webhook 接入** | 外部工单系统推送 | 回调 URL | v1.1 |
| **IM（企业微信 / Slack / Telegram）** | 机器人绑定 | 同一会话 | v2 |

### 9.2 分诊（Agent 协调员 + 人确认）

来件到达后，Agent 协调员产出**分诊卡**：需求摘要、建议归属项目（或新建）、需求草案（标题 / 描述 / 验收标准）、风险与不确定点、澄清问题草案、粗估规模。分诊卡是**草案**：转为需求需要交付负责人确认（全自治模式下可委托，但"新建项目"不可委托）。

### 9.3 甲方门户（极简，面向外部）

- 首页：提交新需求（标题、描述、附件、期望日期）。
- 我的需求：每条显示里程碑进度条（已接收 → 澄清中 → 进行中 → 待验收 → 已交付），**不暴露内部工作项与 Agent 细节**（可在团队设置中选择是否展示"由 AI 与工程师协作完成"标签）。
- 澄清：问题列表，逐条回答。
- 验收：交付包内容（说明、演示链接、交付物清单、证据摘要），按钮〔接受〕〔部分接受〕〔拒绝〕，拒绝需填写原因。
- 品牌：门户显示**团队**的品牌（来自部署 / 租户配置），不出现 Myrmidon 字样（可配置）。

---

## 10. 执行面

### 10.1 机器

- 接入：OAuth 设备码（§6.8）；一台机器 = 一个机器服务账号；凭证可轮换、吊销。
- 详情：概览（OS、地址、版本、执行槽、最近心跳）、运行时（检测到的 Claude Code / Codex 版本与路径）、模型目录、能力（Skills / MCP，来自检测，不调用模型）、Agent（托管于此）、执行记录、排空。
- 规则（继承 C）：每台机器 MVP 1–N 个执行槽；同一工作目录同时只允许一个写执行；离线时可把执行迁到支持同一 runtime 的机器（同目录续接原生会话，不同目录带入上下文）。

### 10.2 Agent

- 创建：名称、头像（像素人生成）、简介、系统提示词、绑定机器、runtime × 模型 × 推理强度、技能（SKILL.md）、MCP 白名单、角色（执行者 / 评审员 / 协调员）、默认频道、记忆策略。
- 详情：概览与实时状态、工作项（进行中 / 历史）、授权（角色与委托，含剩余有效期）、技能、记忆审计（每次写入记录）、私信（Agent 间对话）、工作目录（只读）。
- 身份：服务账号；执行环境**不注入**控制面令牌（B §17.2）。

### 10.3 执行与交互会话

- 执行由工作项发起（分派给 Agent 自动启动；分派给人时，人可选择"在机器上开启交互会话"）。
- **交互会话**完整继承 Preview 01：多轮对话、工具调用折叠行、工具授权卡片（允许一次 / 拒绝）、输入表单、Skills `/` `$` 候选、MCP 清单、附件、重试、换机器、授权模式（只读 / 全自动，全自动为红色语义）。
- 执行头部常驻：所属工作项、分支序号（如 `#3 · 质量返工 2`）、机器、runtime、成本、〔提交完成声明〕。
- 提交完成声明：填写摘要 + 选择证据（自动收集 commit / PR / 测试报告 / 日志，可补人工观察）→ 工作项进入 AGENT_CLAIMED_DONE → 自动排队验证。

### 10.4 工作项拆解模板

继承 B 的 `software-dev-agile`：需求 → PRD → [评审] → 设计 → [评审] → 迭代计划 → 并行编码 ×N → QA → [交付评审]。模板只用于**生成工作项树与依赖**，运行期按工作项状态推进（不暴露独立 DAG 引擎，简化心智）。

### 10.5 API 网关（实验）

把某台机器上已登录的 Codex 暴露为 OpenAI 兼容 Responses API（C 现状：仅文本、单密钥、≤8 并发请求）。放在「机器 → 能力复用」下，常驻"实验性"标识。

### 10.6 主导 Agent 与多模型编排

**目标**：支持"一个主导 / 决策 Agent 负责把任务分给不同 Agent"的团队形态（类似 Grok Heavy 的多 Agent 协作），且团队中各 Agent 可使用不同运行时与模型。

| 能力 | 规格 |
|---|---|
| **多模型团队** | 每个 Agent 独立绑定 runtime × 模型 × 推理强度（如 Claude Code·Opus 写架构、Codex·gpt 写实现、轻量模型做分诊）；同一项目可混用 |
| **主导 Agent** | 项目级唯一（可空）；输入：需求基线、工作项树、团队名册（技能、模型、负载、历史首次验收率、单位成本）、知识库检索结果；输出：**分派提案** |
| **分派即裁决** | 每次分派记录为 Decision（action=`ASSIGN`，authority_source=策略），含理由（"选 Ada：前端技能、首次验收率 82%、当前空闲"）；人可在频道或工作项上一键改派 |
| **派单策略** | `manual`（只出提案，人确认）／`auto`（策略内自动生效，超出预算或高风险转人）；全自治模式默认 `auto` |
| **并行求解（Heavy 模式）** | 对高风险或高不确定的工作项，主导 Agent 可启动 N 个**并行分支**（不同 Agent / 模型同解一题，branch_reason=`ensemble`）；全部分支提交证据后，由验证器 + 主导 Agent 出**对比报告**（证据覆盖、测试结果、diff 规模、成本）→ 择优或合成 → 仍须走正常裁决 |
| **成本护栏** | 并行分支数上限（默认 3）、单工作项预算上限；超限需人批准 |
| **职责分离** | 主导 Agent 可分派、可推荐，但**不能**以自身身份 approve 它所分派的工作项产出（除非另有委托且满足 §8.3 条件） |
| **可观察** | 主导 Agent 的每次规划、分派、对比都写入频道事件（折叠）与审计；"为什么派给它"永远可追溯 |

UI：项目页「团队编排」卡片——主导 Agent、派单策略、并行上限；工作项详情的执行分支树中，ensemble 分支以并列紫色枝呈现，附对比报告。

### 10.7 记忆与知识库（数据库检索）

**目标**：Agent 内置记忆、团队共享知识库；**基于数据库检索**，不引入独立向量数据库作为前置依赖。

**知识范围（scope）**：`team`（团队规范、交付标准）→ `project`（需求、决策、ADR、项目文档）→ `agent`（个人记忆）。检索按「agent → project → team」合并，权限按底座授权过滤（Agent 只能检索其被授权范围）。

**知识来源（自动入库，均带出处链接）**：

| 来源 | 内容 | 价值 |
|---|---|---|
| 需求与验收标准 | 版本化基线 | 执行时对齐需求 |
| 裁决与拒绝反馈 | 理由 + 结构化反馈 | **避免重复犯错**（最高价值） |
| 已接受交付 | 摘要、关键 diff、证据 | 可复用解法 |
| ADR / 项目文档 | 文件模块中的 md/pdf/文本 | 架构约束 |
| 技能 SKILL.md | 团队技能库 | 方法沉淀 |
| Agent 记忆 | Agent 主动写入的经验条目 | 个体经验 |

**检索技术分期（跟随底座三方言）**：

| 阶段 | 技术 | 支持方言 | 说明 |
|---|---|---|---|
| **K1（MVP）** | 关系表 + **全文检索**（PostgreSQL `tsvector`+GIN；MySQL `FULLTEXT` + ngram 解析器以支持中文；SQLite FTS5）+ 结构化过滤（scope、来源类型、时间、标签）+ 时间衰减排序 | 三种都支持 | 零新基础设施；中文分词用 ngram / 二元切分保证可用 |
| **K2** | **混合检索**：PostgreSQL `pgvector` 向量相似度 + 全文 BM25，按 RRF 融合 | PostgreSQL（推荐生产） | 嵌入模型可配置（团队自带 key）；MySQL/SQLite 部署降级为 K1 |
| K3（可选） | 重排序模型、知识图谱关系 | — | 视效果再议 |

**入库流程**：来源对象 → 切块（按标题 / 段落，≤800 字）→ 写入 `knowledge_chunks`（scope、source_kind、source_id、content、tokens、hash、embedding?）→ 来源更新时按 hash 增量重建，删除来源即级联失效。

**记忆治理（继承 B §6.3 防记忆投毒）**：

- 每次写入记审计（谁、何时、摘要、来源执行）。
- 「规则型」记忆（"以后都要这样做"）需人审后生效；普通经验条目直接生效但可撤销。
- 记忆永远**不是真相**：只影响 Agent 倾向；真实变更仍走工作项 → 证据 → 验证 → 裁决。
- 注入执行上下文时附**引用**（chunk id + 来源），并作为执行记录的一部分可回看"这次用了哪些知识"。

**UI**：
- 「知识库」页：按 scope 浏览、全文检索、来源筛选、命中预览；手动上传文档；规则型记忆待审队列。
- Agent 详情「记忆」标签：条目列表、写入审计、撤销。
- 执行详情「使用的知识」折叠区：本次注入的条目与得分。

---

### 10.8 主管 Agent 自建 Agent（编制授权，D110）

**原则：AI 可以招人，人给编制和预算。** 团队不必每个 Agent 都由人创建；主管 Agent 可按工作负载自行扩编，但边界由人设定，且来源在任何视图中都可识别。

| 维度 | 规则 |
|---|---|
| 谁能创建 | 仅主管 Agent（LEAD）且持有 ACTIVE 的**编制授权**；子 Agent 不能是主管、不能再创建（层级 ≤ 1） |
| 编制授权（人设定） | 仅团队所有者授予，不可委托，需 step-up：项目范围、同时在岗名额（默认 3，≤10）、每日创建数（5）、可选角色（执行者 / 协调员）、可用机器与模型、推理强度上限、子 Agent 总预算（默认 $50）与单个上限（$15）、存活期（7 天）与闲置退役（48 小时）、审批方式、有效期 |
| 审批方式 | 默认「逐个人工批准」（收件箱「创建审批」）；仅全自治项目可设「编制内自动创建」，项目切回混编时自动恢复人工批准 |
| 子 Agent 约束 | 权限、技能、MCP ⊆ 主管；不能开全自动授权；不能成为裁决类委托的受托方（除非人「收编」） |
| 预算 | 子 Agent 的执行成本计入编制授权（同时计入项目预算）；80% 通知，100% 授权「耗尽」，子 Agent 停止接新执行 |
| 兜底 | 主管退役 / 长时间崩溃 / 授权撤销或到期 → 子 Agent 停机并标记「孤儿」，由人收编或退役（7 天未处理自动退役）；一键接管同时撤销编制授权 |
| 可识别 | Agent 带来源标记：**人工创建 / 主管创建·〈主管名〉/ 已收编 / 孤儿**；成员列表可按来源筛选、按主管分组为树；详情显示血缘（谁创建、依据哪份授权、理由、到期）与授权用量；审计可按来源过滤 |

详细规格：`specs/05-agents-orchestration.md`（创建请求、来源字段、孤儿与收编）、`specs/08-governance.md`（编制授权、名额与预算、策略规则 `spawn_grant`、创建审批收件箱）。

## 11. 证据与验证

### 11.1 证据类型（A-立项 §3）

| 类型 | 来源 | 自动采集 | 可验证内容 |
|---|---|---|---|
| Git commit | 执行工作目录 | ✅ | 哈希、diff 统计、签名 |
| Pull Request | Git 平台连接器 | ✅（v1.1） | 链接、CI 检查结果 |
| 测试报告 | 验证器输出 | ✅ | 通过 / 失败 / 跳过数、覆盖率 |
| 执行日志 | runtime 适配器 | ✅ | 摘要 + 完整日志（文件模块） |
| 截图 / 录屏 | Agent 或人 | 半自动 | 对应验收标准条目 |
| 人工观察 | 人 | 手填 | 观察者、时间、范围 |

每条证据展示**来源卡**：producer、timestamp、content hash（前 8 位，可复制全值）、verifier / version、result、**局限**（例如"截图仅证明界面渲染，不证明数据正确"）。

### 11.2 验证层级

自动化（编译、单测、lint、构建，高可信）→ AI 辅助（代码审阅、需求比对，**仅参考**）→ 人工（最终权威）。必需验证由工作项类型 + 风险等级决定；AI 辅助验证不能单独让工作项进入 ACCEPTED。

### 11.3 证据覆盖

工作项提交声明时，按验收标准逐条映射证据；未覆盖的标准在评审台以琥珀色标出（指标：证据覆盖率 ≥90%，A-评审 §7）。

---

## 12. 信息架构与页面规格

### 12.1 控制台结构（新应用 `apps/delivery`，路由 `/t/:tenantID/...`，复用底座认证、外壳与工作区切换；外壳规格见 `specs/08-governance.md` §8.6）

```
顶栏：工作区切换（团队/项目） · 全局搜索 ⌘K · 裁决徽标 · 语言 · 主题 · 用户菜单
侧栏：
  工作台   总览 · 我的裁决
  交付     来件 · 项目 · 工作项 · 评审台 · 执行 · 交付
  协作     频道
  团队     成员与 Agent · 机器 · 授权与策略 · 知识库
  治理     审计 · API 网关(实验)
外部     甲方门户（独立入口 /portal#t=<token>，令牌在 fragment 中，不在控制台内）
```

手机端：侧栏收入抽屉（底座 Sheet），底部常驻四项快捷栏：总览、裁决、执行、频道。

### 12.2 页面清单与规格摘要

| 页面 | 目标 | 关键组件 | 关键交互 / 验收 |
|---|---|---|---|
| **总览** | 一屏回答：要我决定什么？进展如何？哪里不健康？ | 裁决队列（前 5）、五项核心指标、项目健康表、团队负载（人 / Agent）、机器状态、近期活动 | 卡片内可直接"通过"；拒绝弹结构化反馈；裁决队列与机器状态 ≤1s 刷新，指标磁贴 ≤60s |
| **我的裁决** | 所有需要我处理的事项 | 分类：工作项评审 / 工具授权 / 澄清回复 / 交付确认 / 升级告警；键盘 J/K/A/R | 他端处理后 ≤1s 移除；不提供批量通过 |
| **来件** | 接住并分诊甲方需求 | 来件列表（开口图标、联系人、状态）、原文、分诊卡、澄清线程 | 〔转为需求〕〔请求澄清〕〔合并〕〔拒绝(理由)〕 |
| **项目** | 项目组合与单项目总览 | 阶段步进条、模式徽章（混编 / 全自治）、预算环、需求基线与版本、团队、风险 | 阶段推进需裁决；模式切换需所有者 |
| **工作项** | 看板 + 列表 | 按状态分列，卡片显示完成阶梯、负责人头像（人 / Agent 区分）、风险、分支数 | 拖拽受状态机约束（非法迁移拒绝并提示原因） |
| **工作项详情** | 单项全貌 | 完成阶梯、验收标准 ↔ 证据映射、执行分支树、验证列表、裁决与审计时间线 | 分支树可对比两枝证据 |
| **评审台**（核心） | 高质量、低成本地裁决 | 左：队列；中：声明 + 证据来源卡 + 验证结果 + diff 摘要 + 验收标准覆盖；右：裁决面板（显示"你以何身份裁决、权限来源"） | 拒绝 → 失败分类 + 结构化反馈 → 自动生成返工分支；记录人工关注时长 |
| **执行** | 所有执行与交互会话 | 列表（状态、机器、成本、所属工作项）；交互会话工作台（Preview 01） | 提交完成声明；换机器；停止 |
| **交付** | 打包并发送给甲方 | 交付包编辑（勾选已接受工作项、生成说明、证据包）、发送记录、甲方回执 | 〔交付〕需 deliver 权限；拒收自动生成返工来件 |
| **频道** | 项目内人机协作 | `# 项目名` 默认频道、@成员（人 + Agent，不含 runtime 进程）、事件折叠、裁决卡片永不折叠 | 在频道里 @Agent 分派工作项 |
| **成员与 Agent** | 一张表管理人和 Agent | 类型标签、角色、负载、当前工作项、状态；Agent 详情抽屉 | 新建 Agent；退役向导 |
| **机器** | 接入与管理 | 机器卡片（OS、runtime、槽位、心跳）、设备码接入向导、详情标签页 | 设备码 300s 倒计时；吊销红色确认 |
| **知识库** | 团队 / 项目 / Agent 记忆的检索与治理 | scope 树、全文检索、来源筛选、命中预览、规则型记忆待审 | 上传文档自动切块入库；撤销记忆即时生效 |
| **授权与策略** | 看清"谁能做什么" | 权限矩阵（角色 × 动作）、委托列表（剩余有效期、使用次数）、升级规则、预算 | 授予委托需理由；一键接管 |
| **审计** | 可证明的历史 | 审计事件时间线（主体、动作、对象、理由、证据引用）、哈希链完整性徽章、导出 | 复用底座审计导出与校验 |
| **API 网关** | 机器能力复用（实验） | 状态、路由机器、Base URL / Key（遮罩、轮换）、测试、最近请求 | 轮换红色确认 |
| **甲方门户** | 外部极简入口 | 提交需求、进度里程碑、澄清问答、交付验收 | 无需注册；不暴露内部细节 |

### 12.3 全局交互

- **⌘K 命令面板**（底座 Command 组件）：跳转、创建来件 / 工作项 / Agent、接入机器、切换主题。
- **实时性**：状态变化 ≤1s 呈现（MVP 可用 SSE；底座暂无实时推送，需新建，见 §13.3）。
- **危险操作**：全自动授权、吊销、强制退役、授予委托、交付、范围变更 → 红色语义 + 后果说明 + 底座**重新认证**（step-up）。

---

## 13. 框架落地映射

### 13.1 直接复用（零新建）

| 需求 | 底座能力 |
|---|---|
| 团队、成员、登录、MFA、Passkey、SSO | `iam/organization`（Tenant）、`iam/identity`、`iam/authn`、`iam/federation` |
| 项目作为组织单元 | `iam/core` Entity（type=`project`；可选 type=`client` 作为分组） |
| Agent 与机器身份 | `iam/identity` 服务账号（client credentials，可设过期） |
| 机器接入 | `iam/oauth` 设备码授权（需扩展：签发给机器专属服务账号、通用设备页拒绝机器客户端，见 specs/00 §13 F2） |
| 权限动作与矩阵 | `pkg/extension/authz` 权限目录：新模块 `Registry().Contribute(...)` 注册 §8.1 动作 |
| 授权委托 | `iam/governance` 访问申请 + 临时角色授权（到期、撤销）+ `policyx` 条件策略 |
| 裁决审计、导出、完整性 | `iam/audit` 哈希链（`audit.Appender` 在业务事务内追加） |
| 甲方回执推送、外部系统联动 | `iam/webhook`（订阅审计事件流，HMAC 签名） |
| API 开口凭证 | PAT / OAuth 客户端 |
| 证据附件、交付包文件 | `files` + `file_resource_bindings`（purpose=`evidence`/`delivery`，resource_kind=`work_item`/`delivery`） |
| 敏感操作二次确认 | `authn` step-up / ReauthenticationDialog |
| 列表筛选排序 | `pkg/query`（`field=op:value` + capabilities） |
| ID、i18n、配置、观测 | `pkg/guid`（雪花 ID）、`pkg/i18n`（en-US / zh-CN / ms-MY）、`pkg/config`、metrics / telemetry |
| 品牌 | `GET /deployment` 部署显示名（不写死 Myrmidon） |
| 前端外壳、主题、组件 | `@workspace/ui`：layout、sidebar、auto-table、auto-form、crud、files、theme（modern / business / eyeCare / pinkKawaii / neoBrutalism） |

### 13.2 新建领域模块（遵循 `backend-module` 规约：三方言迁移、权限目录、respx、query、覆盖率 ≥90%）

> 下表路由为示意；**权威接口契约见各模块规格 §6**。

| 模块 | 职责 | 主要资源路由（示意，按 http-resource-routing 规范） |
|---|---|---|
| `intake` | 开口、来件、分诊、澄清 | `/tenants/{t}/intake-channels`、`/tenants/{t}/intakes`、`/intakes/{id}/clarifications`、`/intakes/{id}/conversion` |
| `delivery`（核心领域） | 需求、工作项、分派、声明、裁决、交付包、验收 | `/entities/{project}/requirements`、`/work-items`、`/work-items/{id}/assignments`、`/work-items/{id}/claims`、`/work-items/{id}/decisions`、`/entities/{project}/deliveries`、`/deliveries/{id}/acceptance` |
| `execution` | 执行、分支、交互会话、证据、验证 | `/work-items/{id}/executions`、`/executions/{id}/turns`、`/executions/{id}/interactions/{i}/decision`、`/executions/{id}/evidence`、`/verifications` |
| `fleet` | 机器、运行时检测、能力、Agent、调度、网关 | `/tenants/{t}/machines`、`/machines/{id}/capabilities`、`/tenants/{t}/agents`、`/agents/{id}/retirement`；机器通道 `/runner/*`（服务账号认证） |
| `collab` | 项目频道、消息、@mention、事件折叠、裁决卡片 | `/entities/{project}/channels`、`/channels/{id}/messages` |
| `governance` | 授权委托、升级规则、预算阈值、业务审计视图、我的裁决聚合 | `/tenants/{t}/delegations`、`/tenants/{t}/escalation-rules`、`/principals/{p}/inbox-items` |
| `knowledge` | 知识库与记忆：切块、全文 / 混合检索、记忆治理 | `/tenants/{t}/knowledge/chunks`、`/knowledge/search`、`/agents/{id}/memories`、`/memories/{id}/approval` |
| `portal`（属 intake 模块） | 甲方门户的公开入口（每联系人链接 + 邮箱验证码 + 会话 Cookie） | `/portal-sessions`、`/portal-intakes` 等（令牌不进 API 路径，见 specs/01） |

命名遵循 CLAUDE.md §4：模块与表按**所含内容**命名，不嵌入品牌。

### 13.3 底座缺口（需新增的平台能力，建议作为平台通用能力而非业务模块私有）

| 缺口 | 用途 | 建议 |
|---|---|---|
| 通用后台任务 / 重试队列 | 验证排队、执行调度、开口轮询 | 平台级 job 能力（`dlock` + 表队列），接 `audit.Do` |
| 领域事件 + Transactional Outbox | 状态迁移通知、频道事件、webhook | 落实 `plugin-architecture.md` 的提案 |
| 实时推送（SSE） | ≤1s 状态呈现 | 平台级订阅端点，按资源授权过滤 |
| 机器长连接通道 | 下发执行、心跳、事件回传 | MVP 沿用 Preview 01 的 HTTP 轮询协议（已验证），v1.1 升级 WebSocket |
| 对象存储驱动 | 证据与日志体量 | `files` 新增 S3 兼容 Driver |
| 通知渠道 | 裁决提醒、甲方邮件 | 在 `iam_notification_outbox` 基础上扩展业务通知 |
| 模块贡献菜单 | 新模块菜单项 | `core.DefaultMenus` 增加模块贡献接口 |

### 13.4 前端

新建 `apps/frontend-web-admin/apps/delivery`（Vite + React，依赖 `@workspace/ui`），复用 IAM 应用的认证、外壳与工作区切换；甲方门户为同应用内的独立布局。API 类型从 OpenAPI 生成（与 IAM 应用同一机制）。

---

## 14. 设计系统

### 14.1 来源与取舍

- **基座**：`@workspace/ui` 的 shadcn `base-nova` 组件、语义 Token（`--background / --foreground / --primary / --muted / --border / --radius / --sidebar-*`）、5 套主题与 `[data-theme]` 切换机制、lucide 图标。
- **新增主题「Aurora」**（Myrmidon 默认）：依据 `.agents/skills/ui-ux-pro-max` 检索（AI-Native UI，亮 / 暗双模式）：主色紫罗兰 `#7C3AED`、交互青 `#0891B2`；标题 Space Grotesk、正文 DM Sans、代码 JetBrains Mono。按 `.agents/skills/high-end-visual-design` 取舍：发丝线代替灰边、极淡环境阴影、关键卡片双层外壳、自定义缓动曲线。图标沿用底座 lucide，统一 1.5 线宽以求精致（一致性优先于个人偏好）。
- 其余底座主题全部可用；所有组件只引用语义 Token，**不写死颜色**。

### 14.2 状态语义（不只依赖颜色，均配图标 + 文字）

| 语义 | Token | 用于 |
|---|---|---|
| 进行中 | `--info`（青） | RUNNING、VERIFYING、IN_PROGRESS |
| 待你决定 | `--warning`（琥珀） | NEEDS_HUMAN_REVIEW、工具授权、澄清待回、INCONCLUSIVE |
| 已接受 / 通过 | `--success`（绿） | ACCEPTED、PASSED、在线 |
| 已声明（未验证） | `--muted-foreground` | AGENT_CLAIMED_DONE |
| 失败 / 拒绝 | `--destructive` | FAILED、REJECT、EXECUTION_FAILED |
| 返工 | `--primary`（紫） | REWORK、分支 |
| 阻塞 / 离线 | 虚线 + 灰 | BLOCKED、OFFLINE、INTERRUPTED |

### 14.3 主体标识

人类头像为圆形照片 / 首字母；**Agent 头像为圆角方形像素人 + 右下角小芯片标记**；机器为设备图标。任何列表中人与 Agent 一眼可分。

### 14.4 可访问性

文本对比 ≥4.5:1；焦点环永不移除；触控目标 ≥44px；实时计数用单一 `role="status"` 播报语义句（"3 项待你裁决"）；`prefers-reduced-motion` 关闭非必要动画。

---

## 15. 指标（A-商业 §7）

| 指标 | 定义 | MVP 目标 / 用途 |
|---|---|---|
| **每个已接受交付物成本** | (AI + 算力 + 平台成本) / ACCEPTED 工作项数 | 交付经济性；总览首位 |
| **首次验收率** | 首次提交即 ACCEPTED 的工作项比例 | 初次质量 |
| **人工关注分钟** | 每个已接受交付物消耗的人工评审 / 授权 / 澄清时长 | 监督负担；评审台自动计时 |
| **返工周期** | 拒绝 → 再次 ACCEPTED 的时长 | 返工效率 |
| **证据覆盖率** | 具备 ≥1 条可审查证据的已提交工作项比例 | ≥90% |
| **闭环完整率** | 终态工作项中具备终态与审计记录的比例 | 100% |
| **甲方一次验收率** | 交付包首次即被甲方接受的比例 | 客户价值 |
| **试点转付费率** | 试点团队转付费比例 | ≥1/3 |

埋点：`intake_received{channel}`、`intake_converted`、`work_item_transition{from,to,actor_kind}`、`claim_submitted{actor_kind}`、`verification_result{layer,result}`、`decision_made{action,authority_kind,attention_ms}`、`rework_branch_created{category}`、`delivery_sent`、`acceptance_received{result}`、`delegation_granted/revoked`、`machine_register_{begun,confirmed,expired}`、`lead_assignment{mode,overridden}`、`ensemble_branches{n,chosen}`、`knowledge_search{hits,used}`、`memory_written{kind,approved}`。**不采集代码与消息正文。**

---

## 16. 非功能需求

| 类别 | 要求 |
|---|---|
| 一致性 | 关键命令幂等（`client_request_id`）；工作项认领租约 + 过期回收；状态迁移与审计同事务 |
| 恢复 | 机器中断 → 执行进入 INTERRUPTED，可恢复或明确失败；服务重启后不自动重放 |
| 实时 | 状态呈现 ≤1s；裁决事项到达所有在线端 ≤1s |
| 性能 | 控制台首屏 <2s；工作项看板 1000 项流畅；会话 >500 条消息虚拟滚动 |
| 安全 | 拒绝默认；执行环境不注入控制面令牌；密钥脱敏；危险操作 step-up；越权负向测试 100% 通过 |
| 隔离 | 租户数据隔离（底座）；甲方门户只读其来件与交付 |
| 国际化 | zh-CN / en-US / ms-MY（底座）全量文案 key 化 |
| 响应式 | 375 / 768 / 1024 / 1440 四档 |

---

## 17. 分期（以 A-立项 8 周计划为骨架，对齐底座与旧资产）

| 周 | 交付 | 退出标准 | 复用 |
|---|---|---|---|
| W1 | 领域模型、状态机、权限矩阵、委托模型 ADR | 评审通过（A-立项 §12 前置条件） | 本文 §5–§8 |
| W2 | `delivery` 模块 schema / API / 审计事件；权限目录注册 | 状态可持久化、每次迁移有审计 | 底座 module、authz、audit |
| W3 | `fleet`：机器设备码接入、运行时检测、Agent（服务账号） | 两台机器接入，Agent 可创建 | 底座 oauth、identity；旧 C 检测逻辑 |
| W4 | `execution`：执行、交互会话、证据自动采集 | 工作项可分派给 Agent 并跑完一次执行 | 旧 C 桥接与会话协议 |
| W5 | 验证器与评审台；结构化拒绝与返工分支 | reject 生成可追溯子执行 | 底座 files |
| W6 | `intake` + 甲方门户 + 交付包 / 验收 | 甲方提交 → 交付 → 验收闭环 | 底座 webhook、PAT |
| W7 | 全自治模式（委托、升级规则）；主导 Agent 派单（manual/auto）；知识库 K1（全文检索 + 记忆治理）；故障与越权测试 | 中断、重复命令、越权全部通过；派单与检索命中可追溯 | 底座 governance、files |
| W8 | 真实项目试点与评估 | 产出指标数据，决定下一阶段 | — |

v1.1+：Heavy 并行求解与对比报告、知识库 K2（pgvector 混合检索）、PR / CI 连接器、Webhook 开口、WebSocket 通道、S3、IM 开口、看板自定义、分支对比视图增强、移动端优化。v2+：可视化模板编辑、跨团队协作、Agent 技能沉淀闭环、Marketplace（冻结）。

---

## 18. 风险与待决事项

### 18.1 待决（附建议）

| # | 议题 | 冲突来源 | 建议 | 理由 |
|---|---|---|---|---|
| Q1 | 产品名 | B 决议 chaos.plus；A 使用 Myrmidon | 工作名 **Myrmidon**；对外显示名走部署配置 | A 为最新；CLAUDE.md 要求品牌不写死 |
| Q2 | 首发 ICP | B：独立开发者 / 小团队；A：AI 软件工作室 | **AI 软件工作室**，独立开发者作为单人团队自然覆盖 | 与"乙方团队"方向一致 |
| Q3 | 是否保留独立 DAG 工作流引擎 | B 的核心；A 不在 MVP | **MVP 不暴露 DAG**，模板只生成工作项树 | 降低心智与工期；模板 JSON 保留，后续可升级 |
| Q4 | 存储 | B：SQLite-first；A：PostgreSQL；底座三方言 | **跟随底座三方言**，生产推荐 PostgreSQL | 框架已支持，无需选边 |
| Q5 | 机器接入协议 | B：自定义 300s token；C：预共享 bootstrap | **OAuth 设备码** | 底座已有，安全模型成熟，体验等价 |
| Q6 | 全自治模式下 Agent 可否 merge | — | 可委托，但必须"非自产 + 验证全过 + 风险 ≤ low" | 兼顾效率与职责分离 |
| Q7 | 甲方门户是否展示 AI 参与 | — | 团队可配置，默认显示"AI 与工程师协作完成" | 透明度利于信任；保留团队选择 |
| Q8 | 交互会话是否必须挂工作项 | C 的会话独立存在 | 允许"临时执行"，但提交完成声明前必须关联工作项 | 不打断探索式使用，又保证闭环 |
| Q10 | 主导 Agent 派单默认策略 | — | 混编默认 `manual`（出提案人确认），全自治默认 `auto` | 先建立信任再放权 |
| Q11 | 知识检索是否首发向量 | 底座三方言，MySQL 社区版无可用向量检索 | **K1 全文检索首发**，K2 pgvector 作为 PostgreSQL 部署增强 | 不引入新基础设施即可上线；效果数据驱动 K2 |
| Q9 | Matrix / A2A | A 要求不做强依赖 | 不进 MVP；频道为内建 | A-评审 P1 |

> **决议（2026-10-11，产品负责人：「该有的就有」）**：Q1–Q11 全部按上表「建议」列采纳；各模块规格 §12「待决（附建议）」中的问题同样按其建议执行，视为已决。如需推翻某一项，在本表与 `.claude/decisions.md` D109 中同时修订。

### 18.2 风险（A-商业 §10 + 实施）

| 风险 | 缓解 |
|---|---|
| 平台厂商内置类似能力 | 聚焦跨 runtime + 甲方闭环 + 审计，这是单一厂商不做的 |
| 人工评审成为瓶颈 | 评审台计时、证据前置、低风险委托；量化后逐步自动化 |
| runtime API 变化 | 适配器隔离 + 契约测试；首发仅 Claude Code + Codex |
| 底座缺实时 / 队列 / outbox | 作为平台能力建设（§13.3），W2–W4 并行 |
| 全自治失控 | 不可委托动作清单 + 升级规则 + 一键接管 |

---

## 附录 A：术语表

| 术语 | 定义 |
|---|---|
| 乙方团队 | 系统主体；= 底座 Tenant；成员为人或 Agent |
| 需求开口 / 来件 | 甲方需求进入系统的通道 / 一条进入的需求 |
| 工作项 | 可分配、可执行、可验证的最小交付单元 |
| 执行 / 分支 | 工作项的一次运行；返工产生子执行，形成分支树 |
| 完成声明 | 执行者声称完成（L1），不代表接受 |
| 证据 | 支持声明的可审查材料，带来源与局限 |
| 验证 | 用特定规则 / 工具 / 版本检查声明或产物的结果 |
| 裁决 | 有权限主体对目标做出的决定，含权限来源与理由 |
| 委托 | 在限定范围与期限内把裁决权临时授予 Agent |
| 交付包 / 验收 | 发往甲方的已接受工作项集合 / 甲方的回执 |
| 机器 | 运行执行的电脑，通过设备码接入 |
| Agent | 长期在岗的 AI 成员，身份为服务账号 |
| 交互会话 | 人与 runtime 的多轮对话（Preview 01 能力） |

## 附录 B：旧资产处置

| 旧资产 | 处置 |
|---|---|
| Preview 01 会话 / 审批回传 / 能力检测 / 模型目录 / 换机续接 | **迁入** `execution` + `fleet`（逻辑与协议复用，代码按底座规约重写） |
| Preview 01 预共享 bootstrap、单 client token | **替换**为设备码 + 底座 IAM |
| Desktop 壳（Electron，托盘） | 保留为可选客户端，加载新控制台；托盘中文化 |
| TUI | 保留为执行者入口（A-立项：CLI/TUI 为主要操作入口之一） |
| API 网关 | 保留为实验功能 |
| v2 执行核原则（Artifact 即真相、验证决定完成、有界自治、stale 只标记） | **保留**为原则，落在工作项 / 证据 / 验证模型上 |
| v2 DAG 引擎、七层上下文、事件溯源全量 | 七层上下文用于 Agent 执行注入；DAG 与全量事件溯源**推迟**（Q3、A-评审 P1） |
| chaosplus 功能树：看板、预览区、协作区、在线文件、定时任务、消息桥接 | 看板 = 工作项看板（MVP）；在线文件 = 底座 files；其余 v2+ |
