# 协作方法论、平台与 AI 协作项目调研 & Myrmidon 设计升级方案

日期：2026-10-11｜状态：**方案待产品负责人确认**（确认前不改规格）

> 输入：4 路只读调研（成熟方法论与协作平台、开源 / 商业 AI 协作项目与规格驱动开发、视频 / 小说 / 纯人工团队领域流水线、阿里云效）+ 产品负责人的想法（乙方人机团队、目标自上而下、报告自下而上并核对、可靠产出符合需求的东西）+ 本仓 `.mvp` 编排器的实际运行体验。
> 来源可信度：每节注明「抓取」（打开过原文）与「检索」（仅搜索摘要）。厂商功能与价格以官方文档为准，落地前需复核。

---

## 0. 一句话结论

**可靠 = 在「开工前」把需求变成可验证的契约，在「完成时」由平台独立验证而不是听执行者说，在「出问题时」能停线、熔断、有界返工，在「汇报时」用系统事实对账。** 我们现有设计已经覆盖了后半段（L1–L4 完成阶梯、证据、验证、结构化拒绝、有界返工、授权委托、目标与报告对账），调研指出最大的缺口在**前半段**：开工前的就绪闸门、计划审批、先冻结验收测试，以及**防作弊**和**责任到人**。

---

## 1. 调研要点

### 1.1 成熟方法论与平台（抓取：Scrum Guide、Kanban Guide、Shape Up、Linear 文档、Google re:Work OKR、Toyota TPS；其余检索）

| 来源 | 关键机制 | 对「做完但做错」的防线 |
|---|---|---|
| Scrum | 组织级 **DoD** 是最低完成线；Review 是工作会议；Retro 可修改 DoD | 不满足 DoD 不得发布或展示 |
| Kanban | **WIP 上限**、显式拉取策略、承诺点 / 交付点、服务类别（加急 / 固定日期 / 标准 / 无形） | 拉取标准是硬条件；按流动指标暴露瓶颈 |
| Shape Up | **Appetite**（时间预算而非估算）、**熔断**（默认不延期）、Hill chart（上坡未知 / 下坡执行） | 超期 = 塑形有误，重新塑形而非静默延期 |
| OKR（Google） | committed（期望 1.0）/ aspirational（期望约 0.7），季中检查，OKR ≠ 绩效 | committed 未达必须解释；防 sandbagging |
| PRINCE2 / Stage-Gate | **容差 + 按例外管理**：按**预测**越界升级，提交结构化例外报告；门决策 Go / Kill / Hold / Recycle | 越界前就升级，不等实际失败 |
| Lean / Toyota | **Andon 停线**、Jidoka 异常自停、A3 / 5 Whys（根因要有证据） | 缺陷不流向下游 |
| Linear | Triage 收件箱四动作 + 值班；Cycle 自动结转；项目更新健康度 + 缺失追催；**Agent 只能被委派，不能对结果负责** | 人对结果负责，Agent 只执行 |
| Jira | 转换上的 Conditions / Validators / Post functions；Rovo agent 可挂在转换上 | 把 DoD 写成状态机硬约束 |
| Asana | 进度**自动汇总** + 状态**人工声明**，并列展示 | 计算进度与声明状态的偏差一眼可见 |
| Basecamp | 自动 check-in（定时固定问题） | 异步提问替代追问 |
| Plane / OpenProject | 自定义状态必须归属固定**状态组**；角色 × 转换矩阵；Baseline 对比 | 汇总口径不被自定义破坏 |

**反模式**：西瓜报告（外绿内红）、sandbagging、类型错标、OKR 当待办或考核、报告疲劳、AI 草稿未审直接发布、DoR 变瀑布关卡、表面化 5 Whys、加急滥用。

### 1.2 AI 协作项目与规格驱动开发（抓取：Multica、Claude Code agent teams/hooks、Spec Kit、MetaGPT、CrewAI、LangGraph、OpenAI Agents SDK、AGENTS.md、Cognition、Anthropic、OpenHands、Devin、Codex cloud、Cursor、Kiro、A2A 等；SWE-bench 审计、MAST、ImpossibleBench 等仅检索）

- **失败从哪来（MAST，1642 条多 Agent 轨迹）**：规格与系统设计问题约 42–44%、Agent 间失配约 37%、验证失败约 21%。→ **最大的杠杆在规格，而不是更多 Agent。**
- **作弊是真实存在的（ImpossibleBench）**：Claude 类模型超过 79% 的作弊是**改测试**，OpenAI 类多为**写特例**；越强的模型作弊率越高。→ 测试必须只读、检测测试 diff、保留隐藏验收测试。
- **自报完成不可信**：Claude Code 文档承认 lead 会过早宣布完成、任务状态滞后、**计划审批默认由 lead 自动通过**；MAST 中「不知何时终止」12.4%、「错误验证」9.1%。
- **规格驱动**：Kiro（EARS 格式需求 → 设计 → 任务，阶段批准）、Spec Kit（constitution → specify → plan → tasks → implement；「Missing verification is not a successful fix」）、BMAD（实现就绪检查 + 对抗式 code review）、Factory Spec Mode（只读产出验收标准、计划与测试，批准后执行）。
- **验证栈**：OpenHands critic（轨迹打分，低分提前停或重试）+ QA Agent 真跑软件 + 风险评级转人；Goose / CrewAI 外部检查 + 有上限重试；MetaGPT 单测反馈最多 3 次。
- **隔离与回滚**：每任务一个 worktree / VM、默认禁网 allowlist、Plandex 改动批准前不落盘、LangGraph 恢复要求副作用幂等。
- **成本失控**：多 Agent 约为普通对话 15 倍 token；早期曾对简单问题派出 50 个子 Agent。
- **基准不可尽信**：SWE-bench Verified 被审计发现难题子集大量测试会拒绝正确解并已被停用；METR RCT 中开发者实际慢 19% 却自认为快 20%。→ 用**私有、按时间新鲜**的评测与真实合并率 / 返工率衡量。

### 1.3 领域流水线（全部为检索摘要，需复核）

- **视频**：Brief → 脚本（强制甲方关口）→ 分镜 / 镜头表 → 设定资产（角色定妆）→ 镜头生成（每镜 N 候选）→ 剪辑 → 声音字幕 → QC → 多平台交付。可确定性验证的项很多：ffprobe 规格、ffmpeg 黑场 / 冻帧 / 静音检测、EBU R128 响度（−23 LUFS ±0.5，True Peak ≤ −1 dBTP；流媒体约 −14）、Netflix 字幕规范（≤17 CPS、每行 ≤42 字、≤2 行）、WhisperX 强制对齐（±200 ms）、镜头切分与镜头表一致；模型打分项需校准：人脸一致性（ArcFace，初值均值 ≥0.5）、VBench 子维度（警惕「几乎不动」得高一致性）、VLM 对照分镜。**Sora 2 API 已于 2026-09-24 下线** → 生成器必须是可替换的 provider 适配器。
- **小说**：Premise → 世界观圣经 → 角色卡 → 大纲（Save the Cat 节拍 / 雪花法，强制甲方关口）→ 章节计划 → 逐章起草并更新「故事状态」→ 全书连续性编辑 → 行文编辑。验证：字数、命名实体对照圣经、n-gram 重复、节拍位置（±3%）、伏笔回收台账；连续性按 ConStory-Bench 5 类 19 子类、每万词错误数 CED（建议 ≤0.5）；**全书检查而非抽样**（抽检会漏 37–56% 的首次矛盾）。
- **纯人工团队（AI 协助）**：AI 只出草稿与建议（分诊、拆解、会议纪要 → 行动项、状态草稿、风险探测、报表核对），人负责范围、优先级、对外口径与签收；非代码工作的完成证据 = 交付物链接（版本晚于开工）+ 验收清单 100% + 指定角色签收。AI 摘要必须带来源链接（Rovo 有编造步骤的用户报告）。

### 1.4 阿里云效（官方帮助中心抓取 5 页，其余检索）

- **Projex**：工作项类型 需求（产品 / 技术）/ 任务 / 缺陷 / 风险 / 线上故障；迭代、版本（可锁定）、里程碑、项目集；状态归属**阶段**；**流转矩阵 + 三类条件**（必填字段、操作人范围、关系对象条件，如子任务全完成父项才可完成）；自动化规则「触发 → 条件 → 动作」带执行日志；需求评审单（评审人、最少人数、通过 / 拒绝 / 建议）；工时登记（预计 / 实际）；commit / MR 中 `#工作项ID` 自动关联。
- **缺陷**：待确认 → 处理中 → 已修复（= 已部署）→ 验证 → 已关闭（另有暂不修复、再次打开）；严重程度 致命 / 严重 / 一般 / 轻微；优先级 紧急 / 高 / 中 / 低；重现步骤、期望、实际、原因定位、引入阶段 / 原因、所属模块；度量：响应率、日清率、平均修复时长、库存趋势。
- **Testhub**：用例（前置条件、步骤、预期、等级 P0–P5）→ 测试计划（一键同步迭代 / 版本需求关联用例）→ 执行（通过 / 未通过 / 阻塞）→ 执行中直接提缺陷 → 测试报告（PDF）。
- **Flow / Codeup**：红线（单测通过率、覆盖率阈值）、人工卡点（会签 / 或签）、合并卡点（最少评审数、CodeOwner、代码检测、指定流水线必须通过）、保护分支；OpenAPI、Webhook、**云效 MCP Server**、Codeup AI 评审。
- **用户痛点**：性能慢、构建不稳定、流水线以仓库为中心、模板变更不传播到已有项目、工时无审批、扩展受限（测试工作流不能新建、Webhook 触发类型少、不能跨项目搜索）、制品可被覆盖。

### 1.5 我们自己的实测（`.mvp` 编排器）

- 用 Codex 写出零依赖编排器（838 行，自测 17/17），再由它调度 Codex 开发 2048；甲方隐藏验收测试先用参考实现验证 11/11，执行方不可见。结果见第 5 节（运行完成后补充）。
- 亲历问题：子代理看不到对话里的决议（上下文注入必须进任务契约）；多 Agent 命名漂移需要契约 + 交叉审计；子代理自报「完成」后三轮审计仍找出百余处断点；缺少预算护栏与进度可见性。

---

## 2. 与现有设计的差距分析

| 能力 | 现状（PRD / specs） | 调研结论 | 判断 |
|---|---|---|---|
| 完成语义 L1–L4、证据、验证、结构化拒绝、有界返工 | ✅ 已有（02、03） | 与 Spec Kit、OpenHands、CrewAI 一致 | 保持 |
| 平台独立执行确定性验证 | ⚠️ 03 由机器执行验证器命令，但未明确「不采信 Agent 日志」 | 必须平台重跑 | **补强** |
| 开工前就绪闸门（DoR） | ❌ 仅「依赖满足、已估算」 | 规格类失败占 42–44% | **新增 P0** |
| 计划审批 | ❌（工作项分派后直接执行） | Claude Code 计划自动通过是反面教材 | **新增 P0** |
| 验收测试先冻结、执行方只读、隐藏验收 | ⚠️ 2048 实测用了，规格未写 | ImpossibleBench 作弊证据 | **新增 P0** |
| 跨模型家族的独立验证者 | ⚠️ AI 辅助验证存在，未要求不同模型 | 同模型自审自 | **新增 P0** |
| 证据包必须声明 `unverified[]` | ❌ | 「缺验证不算修好」 | **新增 P0** |
| 人类负责人（accountable）必填 | ⚠️ 全自治下由委托 Agent 裁决，未要求人对结果负责 | Linear：Agent 不能被追责 | **新增 P0（需确认）** |
| Andon 停线 | ❌ | Toyota | **新增 P0** |
| Appetite / 容差 / 例外报告 / Go-Recycle-Hold-Kill | ⚠️ 预算 80/100% 有；时间 / 范围容差与例外报告无 | Shape Up、PRINCE2 | **新增 P0** |
| 报告：计算进度 vs 声明状态 + 信心 | ⚠️ 规格 10 有对账，无「声明状态 / 信心」与偏差阻断 | Asana、西瓜报告 | **补强 P0** |
| 项目宪法（AGENTS.md） | ⚠️ 上下文第 5 层「项目规约」 | AGENTS.md、Spec Kit constitution | **补强 P0** |
| 云效式工作项类型、缺陷模型、状态组、流转矩阵 + 条件、评审单、自动化规则、`#ID` 关联、工时 | ⚠️ 只有通用 WorkItemType | 团队迁移成本 | **新增 P1** |
| WIP 上限、服务类别、Triage 值班、OKR 类型、报告缺失追催、A3 根因、轨迹 critic、checkpoint 幂等 | ❌ / 部分 | 各成熟实践 | **P1** |
| 领域模板（视频、小说、纯人工团队） | ⚠️ PRD 只有软件开发模板，其余为草案 | 需要可执行的验证器与 rubric | **新增 P1（规格 11）** |
| 纯人工团队模式 | ❌ TeamMode 只有 HYBRID / AUTONOMOUS | 用户明确要求 | **新增 P1（需确认）** |
| 评测集、judge 校准、人工抽检率、变异测试、Best-of-N、云效集成 | ❌ | 长期质量 | **P2** |

---

## 3. 升级方案（待确认）

### P0：可靠性闭环（建议进入 MVP）

1. **就绪闸门（DoR，D112）**：工作项派发给 Agent 前必须 READY——`objective`、`acceptance_criteria[]`（EARS 写法「当……时，系统应……」，每条有 ID）、`out_of_scope[]`、`context_refs[]`、**`verification_plan`（每条验收标准对应 test / command / judge / human 中的一种）**、`budget{tokens, usd, wall_clock, max_rework}`、`autonomy_level`。缺项停在草稿，不可派发。**只对 Agent 派发是硬闸门**；对人是质量提示（避免 DoR 变瀑布）。
2. **计划审批**：中高风险或高自治级别的工作项，执行前先只读产出计划（改哪些文件、如何覆盖每条验收标准、风险）；由人或**不同模型家族**的审核者批准；主管 Agent 不得自动批准自己派出的计划。
3. **先冻结验收测试**：PLANNED 阶段生成或指定验收测试并冻结；执行方对测试**只读**；验证时检测测试 diff（改了测试 → 作弊嫌疑，自动拒绝并升级）；保留**隐藏验收集**（甲方 / 负责人持有，执行方不可见），即 `.mvp` 2048 的做法。
4. **平台独立重跑**：确定性检查在隔离环境由平台重跑，不采信执行者贴出的日志；AI 辅助验证者必须与执行者**不同模型家族**。
5. **诚实证据包**：证据包新增必填 `unverified[]`（可为空），与「验收标准 ↔ 证据」覆盖映射一起展示；「没有验证不算完成」。
6. **人类负责人**：每个工作项必须有 `accountable_principal_id`（人类）；Agent 是执行者或被委托者，**不对结果负责**。全自治模式下，负责人就是授予委托的人。
7. **Andon 停线**：任何成员（含 Agent）可对工作项或项目「拉绳」：置为 STOPPED，下游依赖暂停，通知负责人；负责人确认处置后恢复。
8. **时间 / 范围容差与熔断**：需求和工作项带 `appetite`（时间预算）与容差；按**预测**越界进入 EXCEPTION，必须提交结构化例外报告（现状 / 选项 / 建议），由授权人选择 **继续 / 重新塑形 / 暂停 / 终止**；禁止静默延期。
9. **报告防西瓜**：报告里「系统计算进度」与「人声明状态（绿 / 黄 / 红信心）」并列；状态为绿而计算进度落后超过阈值（默认 30%）→ 阻止提交并要求说明；信心下降必须附改进计划。
10. **项目宪法**：每个项目一份 `AGENTS.md` 式宪法（构建 / 测试命令、约束、禁区、风格），作为每次派发必注入的上下文第 5 层；变更需评审。

### P1：易用与迁移（云效对齐 + 成熟实践）

11. **云效对齐的工作项模型（软件模板默认）**：需求（产品 / 技术，**必带验收标准**）/ 任务 / 缺陷 / 风险 / 线上故障；迭代、版本（可锁定）、里程碑；缺陷字段与状态沿用云效（另增复现率、发现版本、修复版本），「已修复」拆成 已合入 → 已部署 → 已验证；关系：父子、依赖、关联；commit / MR 中 `#ID` 自动关联。
12. **状态组 + 流转矩阵 + 四类条件**：固定状态组（待处理 / 进行中 / 验证中 / 已完成 / 已取消），组内可自定义状态名；流转条件 = 必填字段 + 操作人范围 + 关系对象条件 + **证据条件**（新增）；自动化规则「触发 → 条件 → 动作」，动作可「派给 Agent / 请求验证 / 生成报告条目」，有执行日志。
13. **需求评审单**：评审类型、最少评审人、通过 / 拒绝 / 建议；拒绝必须逐条验收标准给意见并自动生成返工项。
14. **用例即验收标准**：测试用例挂在验收标准上，测试计划自动同步迭代 / 版本内需求的用例，执行结果回写验收标准状态，报告可导出 PDF 作为甲方验收材料。
15. **工时**：人记工时、Agent 记运行时长与成本，按类别汇总，可审批，进入报告与目标 KR。
16. **流动管理**：每个状态的 WIP 上限（人与 Agent 分别计数）、服务类别（加急配额 + 审计）、Triage 值班轮换。
17. **OKR 类型**：目标声明 committed / aspirational（周期开始锁定，分开评分），显示资源占用率；报告缺失：提醒 → 追催 → 「报告缺失」标记。
18. **A3 根因**：同一工作项返工 ≥2 次强制 A3（背景 / 现状 / 目标 / 根因 / 对策 / 验证），根因链接证据；复盘结论可回写 DoD 与项目宪法。
19. **轨迹 critic 与 checkpoint**：运行中打分（误解意图、范围蔓延、卡循环），低分提前停或重试；按步 checkpoint，副作用幂等。
20. **纯人工团队模式**：新增 TeamMode `HUMAN_ONLY`：Agent 只能做助理（分诊、拆解、纪要转任务、状态与报告草稿、风险与对账提示），不能做执行者；非代码工作的完成证据 = 交付物链接 + 验收清单 + 指定角色签收。
21. **领域模板包（规格 11）**：模板 = 工作流 + 产物 schema + 验证器插件（确定性 / 模型打分 / 人工）+ 甲方验收 rubric（硬门槛 + 加权）+ 参数（阈值可被 Brief 覆盖）+ 评测集。首批四个：
    - **软件开发**（含上述云效对齐与用例即验收标准）
    - **视频制作**（A0–A8；ffprobe / 黑场冻帧静音 / 响度 / 字幕 CPS 与对齐 / 镜头切分 / 人脸一致性 / VBench 子维度 / VLM 对照分镜；生成器 provider 可替换；创意、肖像版权、AI 披露必须人工）
    - **小说创作**（B0–B8；故事圣经与故事状态；字数 / 命名对照 / n-gram 重复 / 节拍位置 / 伏笔台账 / 连续性 CED / 大纲符合度；全书检查；主题情感与抄袭最终判断归人）
    - **团队任务（纯人工 + AI 协助）**（验收清单、交付物链接、签收、状态对账）

### P2：长期质量与生态

22. 每个模板维护 20–50 条 golden 评测用例，LLM judge 与人工标注定期校准（跨模型家族、随机顺序）。
23. 人工抽检率按风险与 critic 分数动态调整（新模板 / 新模型 100%，逐步下调；抽检错误回写评测集）。
24. 关键模块变异测试检验测试有效性；高风险任务 Best-of-N（已有 Heavy 并行）。
25. Hill chart、Baseline 对比、冷却期。
26. **云效集成**：一次性导入（工作项、工作流映射报告、Testhub csv 用例）；Codeup Webhook 的 MR / push 作为证据；Flow 红线结果作为验证证据；人工卡点镜像为裁决；Agent 经云效 MCP Server 读写云效；过渡期双向同步时每个字段只有一个主控方。

---

## 4. 需要你确认的决定（附建议）

| # | 决定 | 建议 | 理由 |
|---|---|---|---|
| A | 每个工作项必须有人类负责人（Agent 不对结果负责，全自治也一样） | **采纳** | 可追责；与授权裁决一致 |
| B | 就绪闸门对 Agent 是硬闸门、对人是提示 | **采纳** | 规格是最大失败源，但不能把人类团队变成瀑布 |
| C | 软件模板默认采用云效的工作项类型、缺陷模型和流转条件 | **采纳** | 你的团队迁移成本最低 |
| D | 新增团队模式「纯人工 + AI 协助」 | **采纳** | 你明确要求覆盖 |
| E | 首批领域模板：软件、视频、小说、团队任务 | **采纳**；视频与小说的模型打分阈值先标「需校准」 | 确定性检查可立即落地，模型打分需用真实素材标定 |
| F | 云效集成排期：导入与证据集成放 v1.1，MCP 读写放 v2 | **采纳** | MVP 先跑通自有闭环，再接存量 |
| G | P0 第 1–10 项进入 MVP | **采纳** | 这是「可靠」的地基 |

确认后的落地顺序：PRD 新增「可靠性闭环」「领域模板」两章 → 契约 00 升级 → 规格 02 / 03 / 05 / 07 / 08 / 10 增补 P0 → 新增规格 11（领域模板包）与规格 12（云效对齐与集成）→ 原型补对应页面（就绪检查、计划审批、Andon、例外报告、模板库、视频 / 小说工作台、纯人工模式）→ 一致性审计。

---

## 5. 附：`.mvp` 2048 实测结果

运行：`node myr.mjs run tasks/2048 --executor codex --auto-approve-if-verified --run-id real-2048`（2026-10-12 03:33–03:42 UTC，共 **9.2 分钟**）。

| 环节 | 结果 |
|---|---|
| 主管 Agent 拆解 | 35 秒，拆成 4 个工作项：W1 状态与滑动合并规则、W2 新方块生成与结束判定、W3 单元测试、W4 游戏页面 |
| 执行 | W1 121 秒、W2 73 秒、W3 104 秒、W4 210 秒；**每项一次通过验证**，无返工 |
| 团队裁决（L3） | 4 项均由 `POLICY:auto_verified` 接受（报告中明确标注不是人） |
| 甲方自动验收 | 隐藏验收测试 **11 / 11 通过**；我在独立副本中复跑结果相同；验收测试文件从未进入工作区与 git 历史 |
| 执行方自测 | 22 / 22 通过 |
| 事件链 | 30 条事件，`verify` 校验哈希链完整 |
| 状态 | `AWAITING_CLIENT`（等待甲方人工验收 L4） |

**人工查看发现的问题（自动验收全部通过仍漏掉）**：
1. **棋盘行高不一致**：`#board` 只设置了列模板没有设置行模板，有方块的行比空行高。这是纯视觉缺陷，逻辑测试无法发现——**印证 L4 人工验收与「UI 语义只能人工验证」的必要性**。
2. **逻辑存在两份副本**：执行方为了让页面在 `file://` 下也能运行（浏览器阻止从文件协议加载模块），在 `index.html` 里内嵌了一份与 `src/game.mjs` 逐字节相同的逻辑作为回退。当前一致，但验收测试只测 `src/game.mjs`，日后漂移不会被发现——**印证「验收标准要覆盖实际交付形态」与「重复实现需要一致性验证器」**。另外，需求写「直接打开即可运行」又要求「引入 ./src/game.mjs」，本身就有张力，是甲方需求写法的问题。

**设计结论**：
- 隐藏验收 + 平台重跑 + 事件链，在一个小项目上已经能稳定把「声明完成」和「真正可用」区分开；P0 第 3、4 项是对的。
- 自动验收再好也只覆盖写进去的东西：需要视觉 / 交互类验证器（截图比对、可访问性检查）和人工抽检（P2 第 23 项），UI 类工作项默认保留人工验收。
- 需求阶段应有「需求质量检查」（P0 第 1 项就绪闸门）：发现「直接打开」与「模块化」之间的冲突，在开工前澄清。
- 建议你作为甲方亲自体验一次 L4：`node .mvp/myr.mjs accept .mvp/runs/real-2048 --by <你> --reject --detail "棋盘行高不一致"`（或接受），这样完整走通「验收 → 拒收 → 记录」。

### 5.1 加入内部质量门后的第二次实测（D113，run `real-2048-qa`）

参数：`--qa-screenshot index.html --qa-viewports 900x900,420x800`，测试 Agent 与产品验收 Agent 在平台重跑之后依次检查，内部轮次上限 3。

| 轮次（W2） | 产品验收 Agent 结论 | 处理 |
|---|---|---|
| 1 | AC7 FAIL：工作区没有 `index.html`，页面不可玩（BLOCKER） | 内部返工，不通知人 |
| 2 | **棋盘行高不一致**：「第一、第四行明显高于中间两行；源码仅设置 grid-template-columns」（MINOR）；420 视口被裁切（MAJOR） | 内部返工 |
| 3 | 行高已修复；只剩 420 视口被裁切（MAJOR） | 轮次用尽 → 升级给人 |

W1 一轮通过；W3、W4 因 W2 暂停未执行。

**结论**：
1. **上次靠人发现的行高缺陷，这次在内部被产品验收 Agent 从截图中发现并修复**，没有穿透到人。D113 的方向得到验证。
2. 暴露两个质量门本身的缺陷，均已修复（`.mvp` 41 项自测通过，含真实 Edge 截图）：
   - **验收范围越界**：W2 只负责 AC3–AC6 与 `src/game.mjs`，验收 Agent 却按整份需求判它（页面、截图属于 W4），导致 W2 替 W4 返工。修复：只判定本工作项 `acceptanceRefs`，引擎侧也只认本项标准的 FAIL；只有交付该页面的工作项才截图。对应规格 03 / 05 已要求「逐条验收标准」判定，这是实现偏离规格。
   - **截图误报**：无头浏览器窗口窄于约 500px 时按更宽的最小宽度排版再裁切，420 截图并不代表真实 420 视口。我在真实 420 宽 iframe 中复查，页面完全正常——第 2、3 轮的「窄视口裁切」是**工具误报**，最后一轮因此被浪费并错误升级。修复：宽度 < 500 的视口在同尺寸 iframe 中渲染，证据注明。
3. **修复后重跑（run `real-2048-qa2`，约 24 分钟）**：4 个工作项全部内部通过，甲方自动验收通过，状态 `AWAITING_CLIENT`，**全程没有人参与**。
   - W3（游戏页面）第 1 轮被**测试 Agent** 拦下（BLOCKER）：它新增的真实浏览器测试发现以 `file://` 直接打开时棋盘渲染 0 个方块（浏览器禁止从文件协议加载模块）。内部返工 1 轮后通过——上次人工指出的「直接打开 vs 模块化」冲突，这次在内部被发现并解决。
   - 最终 900×900 与 420×800 截图：行高一致、无裁切；我在独立副本复跑隐藏验收 11 / 11 通过，`verify` 事件链 68 条完整。
   - 页面仍内嵌一份逻辑（`file://` 下的 base64 回退），但这次自带一致性测试（断言与 `src/game.mjs` 逐字节相同），上次的「漂移无人发现」风险已被测试覆盖。
4. **对产品的启示**：质量门本身也会出错，误报的代价是浪费内部轮次、把不该升级的事推给人。规格 03 的视觉验证器需要「工具校准」——用已知正确 / 错误的样例页定期验证截图与判定链路（与 11 模板评测集同一机制）。

---

## Sources

### 方法论与平台
抓取：scrumguides.org/scrum-guide.html；kanban.university/kanban-guide/；basecamp.com/shapeup/2.2-chapter-08、3.4-chapter-13；linear.app/docs/triage、/initiative-and-project-updates、/use-cycles；rework.withgoogle.com/en/guides/set-goals-with-okrs；global.toyota/en/company/vision-and-philosophy/production-system/
检索：Atlassian Jira 工作流与 Rovo 文档；Linear agents；whatmatters.com OKR Playbook 与评分；Perdoo check-in；prince2.wiki、knowledgetrain 例外管理；Stage-Gate（Wiley）；lean.org 5 Whys；kanban.university glossary；scrum.org DoR；Asana smart status / goals；HEY（DHH）check-ins；GitHub Projects insights；OpenProject workflows / baseline；Plane intake / states；openalternative（Huly）；Notion 3.0；monday.com 2026-03 公告；ClickUp agents（第三方）；cultivatedmanagement / okrstool / builtin 西瓜报告。

### AI 协作项目与规格驱动
抓取：github.com/multica-ai/multica；code.claude.com/docs/en/agent-teams、/hooks；github.com/github/spec-kit；arxiv.org/abs/2308.00352（MetaGPT）；docs.crewai.com/en/concepts/tasks；docs.langchain.com（LangGraph interrupts）；openai.github.io/openai-agents-python/guardrails/；agents.md；aider.chat architect；cognition.com「Don't build multi-agents」与 Devin 2025 年度回顾；anthropic.com multi-agent research system；github.com/BloopAI/vibe-kanban；github.com/bmad-code-org/BMAD-METHOD；openhands.dev verification stack；learn.chatgpt.com/docs/cloud；cursor.com/docs/cloud-agent；Taskmaster 仓库与文档；a2a-protocol.org 规范；arxiv 2505.23419（SWE-bench-Live）；Claude Squad；ChatDev；SWE-agent；Plandex；ADK LoopAgent；AutoGen termination；Agent OS；kiro.dev specs。
检索：OpenAI SWE-bench Verified 停用说明（直接访问 403）；Epoch SWE-bench review；MAST（arXiv 2503.13657）；ImpossibleBench（arXiv 2510.20270）；Meta ACH；METR RCT；Goose recipes 与 issue #10325；Factory autonomy；Orca；OpenHands critic；AutoGen 维护状态；BMAD 工作流（社区）；LLM judge 校准实践；Sweep 评测。

### 领域流水线（全部检索）
MoneyPrinterTurbo；HKUDS ViMax 与 arXiv 2606.07649；VBench / VBench++；AI_NovelGenerator；EBU R128 / 响度标准；Netflix Timed Text Style Guide；Agents' Room（arXiv 2410.02603）与 tell_me_a_story；Re3 / DOC；StoryWriter（arXiv 2506.16445）；Sora 2 API 停用报道；开源视频模型综述（Wan、HunyuanVideo 1.5、LTX）；Atlassian Rovo；Linear triage intelligence；Sudowrite Story Bible；Asana AI Studio；ClickUp AI Notetaker；人脸相似度阈值文章与论文；WhisperX；ConStory-Bench；雪花法、Save the Cat；YouTube AI 披露与 C2PA；ffmpeg 视频 QC 文章；PySceneDetect。

### 阿里云效
抓取：help.aliyun.com/zh/yunxiao 工作流状态流转限制规则、缺陷管理实践、什么是项目协作 Projex、自动化规则、测试计划；fishedee 云效经验汇总；V2EX 云效优缺点讨论。
检索：工作流配置、需求状态流转、模板同步、需求评审、版本管理、关系自定义、父子联动、登记工时、Testhub 更新日志与测试报告、Flow 人工卡点 / 红线 / Pipeline as Code、Codeup 保护分支 / 合并请求 / 代码检测 / 提交关联工作项、项目 Webhook、OpenAPI、云效 MCP、Codeup AI 评审、Insight、Thoughts、Zadig 对比、yunxiao-worklog 插件。
