# PRD → 原型逐项证据与缺口

审计范围仅为 `docs/prototype`，对照 [PRD_FINAL.md](../PRD_FINAL.md) 的 **343 个 REQ** 和 **70 个验收场景**（JNY 18、AC 27、EXT 25）。保留原 ID、要求及关联入口；入口只用于定位，不是行为证据。**没有任何一行据此被判定为整条需求或整场景验收通过，也没有宣称完成两轮无问题评审。**

## 判定口径

- **D — 设计部分**：存在相关 UI、数据字段或本地模拟（可仅为通用表单），但缺口列尚未落实；通用 CRUD 不代表专业流程已设计。
- **M — 缺失设计**：在已检查源码中未见该行为的专用状态/流程；证据列只能指向相邻能力，不计覆盖。
- **P — 生产能力未验证**：需后端、原生系统、真实外部服务或性能/故障实验；页面演示不能证明。P 不表示仅差测试，也可能尚无实现；本审计不推断原型之外代码。
- **Tnn**：保留的初次证据快照中的本地域测试。**Q / F / B 编号**：后续经执行者报告的领域、工作流或浏览器测试。各编号仅证明所列断言子集，不代表整条需求、真实基础设施或整轮评审通过。

源码引用相对本目录，符号名用于稳定定位。每行“局部证据”与“尚缺行为”共同阅读；同族共享组件只证明该组件存在，不证明每一行的业务。前端角色切换、localStorage、mock-run、模拟API/终端、静态成功文案不等同 IAM、永久保存、独立测试、实际服务或运行成功。

## 实际执行记录

2026-10-13，证据审计子代理初次在本目录运行 `bun test`：**16 pass、0 fail、59 expect() calls**。以下 T01–T16 保留当时测试名称与证据，不冒充最新源码总数。最新执行见下一节；后续源码变化须重新验证。

| 证据号 | 实际运行测试名称 |
|---|---|
| T01 | `a Human cannot deliver or accept a failed internal gate` |
| T02 | `recheck, acceptance and delivery form one traceable fixed-version package` |
| T03 | `rework and a new artifact invalidate the old accepted gate` |
| T04 | `unconfirmed plan or SOP never starts execution` |
| T05 | `cancelled work is terminal and rejected transitions leave no mutation` |
| T06 | `offline and external views cannot mutate internal work` |
| T07 | `executor cannot grant final Human authorization` |
| T08 | `editing a model invalidates its old approval target revision` |
| T09 | `partial configuration rollout does not falsely report every node applied` |
| T10 | `scheduled report generation produces a draft without Human submission` |
| T11 | `external rejection retains delivered package and starts a traceable rework` |
| T12 | `rejected packages retain their shared version without exposing new revisions` |
| T13 | `project SOP drafts and publications never change another project` |
| T14 | `tenant creation and switching isolate projects, messages and domain records` |
| T15 | `calendar schedule validates real dates, versions and preserves history` |
| T16 | `workflow validation rejects missing contracts, cycles and incomplete branches` |

## 当前执行证据（2026-10-13 后续修复）

前次证据快照 `bun test`：**30 pass、0 fail、384 expect() calls**，覆盖 `tests/domain.test.js` 与 `tests/flow.test.js`。本节由证据编辑子代理根据主代理已执行结果及当前脚本断言整理；不是该子代理再次运行的记录。

| 编号 | 已执行命名测试 / 脚本 | 通过范围与限制 |
|---|---|---|
| Q1 | `failed current-version checks revoke acceptance and block delivery`; `every nonpassing or evidence-free current check blocks a stale accepted flag`; `missing current checks cannot be replaced by a cached quality flag` | 当前版本失败、非通过、缺证据/缺检查不能由旧 qa 或接受标志放行 |
| Q2 | `rechecks retain failed evidence and new versions are unexecuted`; `quality display distinguishes failed, pending, unexecuted and passed evidence` | 原失败证据保留、新版本未执行及四类质量状态展示；复核仍是显式模拟 |
| Q3 | `executor cannot approve goals or tool authorization regardless of status label` | 目标/工具授权状态不能绕过本地执行者限制 |
| Q4 | `a newly created work item submits without requiring a page reload` | 新工作首次提交建立版本历史，无须靠刷新补字段 |
| Q5 | `all resource editors restrict forms to configuration and preserve runtime receipts`; `connector history scope and audit history remain separate for every connector`; `legacy connector history text migrates without losing its sharing meaning` | 配置编辑字段与运行回执分开，连接器共享历史范围与审计历史不混用 |
| F1 | `every delivery path requires independent checking followed by Human approval` | 图中每条交付路径及检查/批准顺序校验；不是实际执行引擎 |
| F2 | `publication retains fixed definitions and existing work version references`; `project publications and work locks reject unpublished templates and invalid graphs`; `legacy work without a recorded workflow version must reconfirm before start` | 发布定义快照、工作锁定版本、项目隔离、未发布/非法图拒绝；运行补丁审批尚缺 |
| B1 | `tests/browser-workflow.js`，1440px PASS | 失败门禁、模拟复核→接受→固定交付、列表同步、消息保存、版本批注、非法图阻止发布 |
| B2 | `tests/browser-navigation.js`，1440px PASS | 侧栏 URL/history/外壳稳定、项目选择、租户创建切回、主题、确认框、频道原位切换 |
| B3 | `tests/browser-features.js`，1440px PASS | 节点新增/编辑/复制/删除/调试、未连接发布拒绝、个人周排期保存、基础API预览切换与重连 |
| B4 | `tests/browser-previews.js`，1440px 与 375px PASS | 同类多开、任务切换/重挂载保留路径/结果/终端草稿历史/文件选择、断线重连、键盘关闭焦点、离线切换、全部关闭再打开；已验证终端实际scrollTop80在断线重连和重挂载后恢复，并验证恢复选中标签可见；未验证真实服务资源回收 |
| B5 | `tests/browser-collab.js`，1440px 与375px主代理PASS | 执行线程进出/回复、仅共享选中文字不带未选元数据、私人/空租户上下文；375px另覆盖抽屉关闭与上下文弹窗焦点 |
| B6 | `tests/browser-routes.js`，1440px PASS | 路由可打开的冒烟证据；不计任一业务需求完整覆盖 |

`tests/browser-domain.js` 已由主代理在1440px复跑PASS：新工作提交、未执行/失败/重验状态、运行回执不被配置编辑、发布版本选择及历史引用。

变异证据：主代理报告 7 个领域变异均被测试杀死，质量状态展示变异是在补充状态展示测试后杀死。预览“重挂载丢状态”与“离线阻止标签操作”两个源码回退变异均令 B4 失败，但旧 B3 仍通过，证明 B4 补上该回归盲区；不能据此推断其余未测行为正确。这里没有全 PRD PASS 或两轮无问题结论。

## 统计

原 332 条初始分类：D 164、M 67、P 101；本次新增 11 条列入 M 待实现/验证（共 343 条；此为证据快照分类，不是当前功能实时盘点）；本表不宣称任何整条生产 PRD 已验收。状态描述剩余工作性质，不是原型完成百分比；已验证的本地交互见对应证据，不能解释为“原型未实现”。

## 逐项需求矩阵

| ID | 要求 | 关联审阅入口（非证据） | 状态 | 源码与局部证据 | 尚缺行为 / 验收证据 | 已运行测试子集 |
|---|---|---|---|---|---|---|
| REQ-CORE-001 | 从自然语言、文件、已有项目或频道讨论发起工作；先识别目标、接收者、结果、范围和约束 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 文件解析与目标识别 | 未执行对应测试 |
| REQ-CORE-002 | 计划与 SOP 分别有草稿、确认、版本和责任人；计划定义要做什么，SOP 定义如何协作执行与交接 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/store.js: workCommand / lockWorkFlow; src/pages-work.jsx: WorkDetail`：计划独立确认标志，SOP确认锁定已发布定义版本 | 计划自身版本/责任记录和计划/SOP变更确认差异 | T04、F2 |
| REQ-CORE-003 | 工作、子任务、Flow 环节、执行、成果、会话相互关联；工作 / 任务沿用同一工作项体系 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 子任务/执行/会话完整关联 | 未执行对应测试 |
| REQ-CORE-004 | 每个正式运行环节由一个明确的人类负责人负责，支持多 Human、多 Agent 共同执行 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 实际多参与者执行与唯一责任约束 | 未执行对应测试 |
| REQ-CORE-005 | 环节完成包含执行产物、内部验收和下游接收；最终环节由最终接受者承接 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 逐环节内部验收及下游接收 | 未执行对应测试 |
| REQ-CORE-006 | 退回必须绑定输入 / 输出版本、未满足条款、期望和归属环节；支持本环节重做与退回上游 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 结构化退回条款/输入输出版本/归属 | T11 |
| REQ-CORE-007 | 上游返工要计算受影响下游；暂停依赖旧结果的步骤，保留无关步骤，重新验证失效成果 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | M | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：仅相邻能力：工作表单与本地计划/SOP、门禁、交付状态；未见本条专用流程 | 影响图计算与选择性暂停 | 未执行对应测试 |
| REQ-CORE-008 | 工作状态区分待开始、进行中、等待、待验收、待交接、已完成、取消；工程映射统一制定 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 各状态真实事件与全部转换 | T05 |
| REQ-CORE-009 | 简单任务允许单环节 Flow，复用团队已确认 SOP；具体计划和执行范围仍须符合确认规则 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 已授权单环节模板复用 | 未执行对应测试 |
| REQ-CORE-010 | 最终交付必须满足当前要求和所有必需环节门禁，责任人确认指定版本 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | D | `src/pages-work.jsx: WorkDetail / NewWork; src/store.js: workCommand`：工作表单与本地计划/SOP、门禁、交付状态 | 所有环节门禁与当前要求映射 | T01、T02、T07、Q1、F1、B1 |
| REQ-ORG-001 | 支持 Human 组织 → 部门 / 团队 → 子团队的组织树，维护人类负责人、Human 成员、上下级与有效期；Agent 独立名册，不作为组织树成员 | `/members、/invitations、/policies` | D | Team / saveOrganization：Human-only组织树，编辑上下级/负责人/有效期/成员，循环与旧版本拒绝 | 真实组织IAM及历史有效期权限投影 | organization.test.js；browser-organization.js |
| REQ-ORG-002 | 组织管理关系与项目协作关系分离；成员可跨部门加入多个项目 | `/members、/invitations、/policies` | D | Human正式组织与projectRoles独立保存；Agent身份在独立名册；任务编组引用身份ID | 真实跨部门授权与大型名册分页 | organization.test.js；work-team.test.js |
| REQ-ORG-003 | Human 通过电子邮件或已有账号邀请加入组织 / 项目，邀请含角色、范围、有效期与撤销 | `/members、/invitations、/policies` | D | Logo右侧邀请收件箱接受/拒绝/幂等加入；Team创建范围化本地邀请并撤销，登记人与邀请负责人分开 | 真实邮件/账号邀请投递及身份核验 | tenants.test.js；organization.test.js；browser-tenants.js |
| REQ-ORG-004 | 区分内部成员与外部成员；外部账号使用同应用和共享对象投影 | `/members、/invitations、/policies` | D | `src/pages-admin.jsx: Team / Agents / ResourcePage; src/store.js: tenant functions`：成员/Agent样例、邀请记录和本地租户快照 | 按共享对象逐字段授权 | 未执行对应测试 |
| REQ-ORG-005 | Agent 是可分派、可 @、可审计的服务账号，独立于 Human 组织名册；标明人类负责人、角色、Human / Agent 创建来源与准确创建者，Agent 创建的 Agent 单列管理 | `/members、/invitations、/policies` | D | Agents：Human创建/Agent创建分列，创建者ID、父子链接、岗位/prompt/model/Skills/MCP、稳定任务身份关联 | 真实服务账号/@分派与授权事件 | agents-templates.test.js；agent-lineage.test.js；browser-agent-lineage.js |
| REQ-ORG-006 | 支持混编、AI 主执行和人工主执行模式；OPC 可采用一人多 Agent | `/members、/invitations、/policies` | D | WorkTeamEditor：任务内Agent优先的池与Human混编，多执行者和唯一Human负责人，聊天/日历联动 | 实际运行器多人执行与并发协调 | work-team.test.js；browser-work-team.js |
| REQ-ORG-007 | 基于组织、项目、环节、对象与动作授权；管理树位置不自动授予私人内容访问 | `/members、/invitations、/policies` | D | `src/pages-admin.jsx: Team / Agents / ResourcePage; src/store.js: tenant functions`：成员/Agent样例、邀请记录和本地租户快照 | 服务端对象动作授权 | 未执行对应测试 |
| REQ-ORG-008 | 离职、退役、调岗、邀请撤回触发任务交接和凭据 / 会话撤销 | `/members、/invitations、/policies` | M | `src/pages-admin.jsx: Team / Agents / ResourcePage; src/store.js: tenant functions`：仅相邻能力：成员/Agent样例、邀请记录和本地租户快照；未见本条专用流程 | 离职交接与凭据即时撤销 | 未执行对应测试 |
| REQ-ORG-009 | Agent 创建、主管扩编、收编、退役保留编制、预算、期限、父子关系与审批 | `/members、/invitations、/policies` | D | 保存准确创建来源与创建者，独立配置提案审批、保留子Agent、禁止退役/待批准新分派 | 真实扩编编制/预算/期限及接管策略 | agents-templates.test.js；agent-lineage.test.js；work-team.test.js |
| REQ-ORG-010 | 复用现有租户、身份、实体和成员模型；组织树不得隐式打通租户隔离 | `/members、/invitations、/policies` | D | `src/pages-admin.jsx: Team / Agents / ResourcePage; src/store.js: tenant functions`：成员/Agent样例、邀请记录和本地租户快照 | 复用生产IAM模型及跨租户测试 | 未执行对应测试 |
| REQ-OKR-001 | 支持组织、团队、项目、Human、Agent 的 Objective 与 Key Result，以及年 / 季 / 月等周期 | `/goals、/reports` | D | `src/pages-admin.jsx: ResourcePage(goals); src/data.js: resources.goals`：通用目标记录、指标字段与手工状态按钮 | 完整层级/周期与KR编辑 | 未执行对应测试 |
| REQ-OKR-002 | 目标自上而下拆解，支持跨项目贡献与多对多对齐；避免同一成果重复计分 | `/goals、/reports` | M | `src/pages-admin.jsx: ResourcePage(goals); src/data.js: resources.goals`：仅相邻能力：通用目标记录、指标字段与手工状态按钮；未见本条专用流程 | 多对多对齐/贡献去重 | 未执行对应测试 |
| REQ-OKR-003 | KR 定义基线、目标值、单位、方向、计算方式、数据来源、更新时间与证据要求 | `/goals、/reports` | D | goalProgress / calibrateGoalProgress：手工进度绑定指标修订和依据，指标变化显示待校准 | 指标方向/计算定义、自动来源与新鲜度完整闭环 | goals.test.js；browser-goals.js |
| REQ-OKR-004 | 支持 AI 起草、成员提交、主管审核、退回修改、发布、变更和期末复核 | `/goals、/reports` | D | `src/pages-admin.jsx: ResourcePage(goals); src/data.js: resources.goals`：通用目标记录、指标字段与手工状态按钮 | 期末复核/变更版本闭环 | 未执行对应测试 |
| REQ-OKR-005 | Agent 可更新授权范围内的事实数据与进展草案，不自行确认自己的 OKR 或人类承诺 | `/goals、/reports` | D | `src/pages-admin.jsx: ResourcePage(goals); src/data.js: resources.goals`：通用目标记录、指标字段与手工状态按钮 | Agent与Human身份权限约束 | 未执行对应测试 |
| REQ-OKR-006 | 自动关联计划、任务、交付、报告；手工进度必须有说明和可选佐证 | `/goals、/reports` | D | 当前手工校准记录依据与历史，不改变工作完成/成果接受状态 | 计划任务交付报告的自动事实关联 | goals.test.js；browser-goals.js |
| REQ-OKR-007 | 识别偏离、无计划支撑、数据过期、无负责人和临期风险，触发提醒与复盘 | `/goals、/reports` | M | `src/pages-admin.jsx: ResourcePage(goals); src/data.js: resources.goals`：仅相邻能力：通用目标记录、指标字段与手工状态按钮；未见本条专用流程 | 偏离风险规则及提醒 | 未执行对应测试 |
| REQ-OKR-008 | 支持期末自评、上级审核与结转；业务目标达成和交付物接受分开记录 | `/goals、/reports` | M | `src/pages-admin.jsx: ResourcePage(goals); src/data.js: resources.goals`：仅相邻能力：通用目标记录、指标字段与手工状态按钮；未见本条专用流程 | 自评/主管审核/结转及接受分离 | 未执行对应测试 |
| REQ-PLAN-001 | Human 与 Agent 在会话中生成项目、阶段与个人计划，关联目标、要求、期限和已有资源 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 会话生成多层计划与资源匹配 | 未执行对应测试 |
| REQ-PLAN-002 | 支持日、周、月、季、年计划及里程碑拆解，关联预计投入、预算与成果 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 日周月季年/里程碑投入预算 | 未执行对应测试 |
| REQ-PLAN-003 | 计划任务支持多执行者但有唯一结果负责人；指定接收方和完成标准 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 多执行者及唯一责任/接收人约束 | 未执行对应测试 |
| REQ-PLAN-004 | 计划先确认再进入正式 SOP / 派发；已有授权的模板可简化重复确认 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 版本化计划确认及模板授权 | T04 |
| REQ-PLAN-005 | 系统按实际执行、成果、依赖和验收维护进展，Human 可补充事实并注明来源 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 真实执行进展/补充事实来源 | 未执行对应测试 |
| REQ-PLAN-006 | 识别逾期、长期无进展、资源冲突、上游延期和等待过久；支持提醒、催办、升级 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 资源冲突/等待风险/升级 | 未执行对应测试 |
| REQ-PLAN-007 | 重规划给出范围、时间、成本、受影响环节与人员的差异，经所需确认后生效 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | M | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：仅相邻能力：计划确认布尔值和月/周本地排期；未见本条专用流程 | 重规划差异与逐项确认 | 未执行对应测试 |
| REQ-PLAN-008 | 支持计划取消、暂停、结转、替代和版本比较；保留原承诺和未完成原因 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | M | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：仅相邻能力：计划确认布尔值和月/周本地排期；未见本条专用流程 | 暂停/结转/替代/版本比较 | 未执行对应测试 |
| REQ-PLAN-009 | 进度汇总按明确权重和事实计算，阻塞 / 不确定单列；能定位到具体工作 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | M | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：仅相邻能力：计划确认布尔值和月/周本地排期；未见本条专用流程 | 权重进度及不确定性来源 | 未执行对应测试 |
| REQ-FLOW-001 | 使用 React Flow 展示与编辑 SOP；支持拖动、缩放、平移、自动布局、框选、搜索定位和小地图 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 自动布局/框选/搜索全交互验收 | 未执行对应测试 |
| REQ-FLOW-002 | 支持无限画布工作空间、分组、注释、子流程与折叠；可见区域与子流程按需加载 | `/p/launch/flow` | M | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：仅相邻能力：React Flow草案、节点面板和本地发布校验；未见本条专用流程 | 分组/注释/子流程折叠/按需加载 | 未执行对应测试 |
| REQ-FLOW-003 | AI 可由已确认计划和业务包生成 Flow 草案；Human 与 Agent 可协作修改并比较差异 | `/p/launch/flow` | M | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：仅相邻能力：React Flow草案、节点面板和本地发布校验；未见本条专用流程 | AI草案来源与多人差异比较 | 未执行对应测试 |
| REQ-FLOW-004 | WorkflowDef 是唯一流程定义；画布、列表、JSON、AI 草案与运行实例引用同一版本 | `/p/launch/flow` | D | `src/store.js: flowDefinition / publishProjectFlow; src/pages-flow.jsx: Flow`：画布和列表引用同一发布定义/草案，工作保存定义版本 | JSON/AI草案/实际运行统一WorkflowDef契约 | F2 |
| REQ-FLOW-005 | 节点支持制作、检查、人工审批、条件分支、并行、汇合、等待、交接与有界循环 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 全部节点类型真实语义及有界循环 | 未执行对应测试 |
| REQ-FLOW-006 | 边定义依赖、输入 / 输出映射、版本、可选条件和交接要求；检查缺输入、不可达和循环错误 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 输入输出版本映射与交接契约 | T16、F1、B3 |
| REQ-FLOW-007 | 每个业务环节绑定 Human 负责的小团队，默认支持 AI 执行，可有多 Agent、多 Human；人工主执行同样有效 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 小团队多角色及人工主执行 | 未执行对应测试 |
| REQ-FLOW-008 | 支持顺序、并行、候选比较、局部返工、上游退回、人工接管和中途接入已有成果 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 候选比较/局部返工/已有成果接入 | 未执行对应测试 |
| REQ-FLOW-009 | 运行实例锁定 Flow / 工作包版本；动态改流程以补丁展示影响，经过相应审批 | `/p/launch/flow` | D | `src/store.js: flowDefinition / lockWorkFlow / publishProjectFlow; src/pages-flow.jsx: Flow`：发布前定义保留快照，工作锁定 flowVersion，旧版可只读查看 | 运行中补丁影响展示/相应审批；真实执行与工作包版本锁定另验 | F2 |
| REQ-FLOW-010 | 循环有次数、时间、成本与退出条件；超限必须升级，不可无限自动重做 | `/p/launch/flow` | M | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：仅相邻能力：React Flow草案、节点面板和本地发布校验；未见本条专用流程 | 次数/时间/成本上限升级 | 未执行对应测试 |
| REQ-FLOW-011 | 支持多人编辑、乐观并发或锁定编辑，防止覆盖；AI 补丁和人工编辑同样校验版本 | `/p/launch/flow` | M | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：仅相邻能力：React Flow草案、节点面板和本地发布校验；未见本条专用流程 | 多人并发冲突与AI补丁版本 | 未执行对应测试 |
| REQ-FLOW-012 | 支持模板保存、版本发布、回滚、导入导出、团队规则叠加和跨业务组合 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 回滚/导入/跨包组合/规则叠加 | T13 |
| REQ-FLOW-013 | 画布中可下钻环节详情、执行、审批、成果与日志；复用规范对象详情 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 节点至所有规范对象下钻 | 未执行对应测试 |
| REQ-FLOW-014 | 提供列表和键盘可操作替代；移动端可查看、确认、批注与执行关键控制 | `/p/launch/flow` | D | `src/pages-flow.jsx: Flow; src/store.js: validateWorkflow / projectFlow`：React Flow草案、节点面板和本地发布校验 | 全部键盘与移动控制验收 | 未执行对应测试 |
| REQ-TEAM-001 | 每个环节配置 Human 负责人、协调者、多个执行者、检查者和下游接收者 | `/agents、/members` | D | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：成员/Agent列表及工作负责人和接管状态 | 环节团队角色编辑 | 未执行对应测试 |
| REQ-TEAM-002 | 支持任务拆分、共同执行、接力、候选并行与成果合并；定义 ALL / ANY / 指定汇合等完成条件 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 任务拆分/候选合并/ALL ANY汇合 | 未执行对应测试 |
| REQ-TEAM-003 | 人与 Agent 接受分派后明确认领，防重复领取；可拒绝接手并说明原因 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 认领去重/拒绝接手理由 | 未执行对应测试 |
| REQ-TEAM-004 | 主管 Agent 根据能力、权限、负载和成本提出分派；在已授自动策略内执行 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 能力/负载/成本分派策略 | 未执行对应测试 |
| REQ-TEAM-005 | 多写入者采用独立工作区 / 分支或明确写入所有权，合并前校验基线 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 独立工作区与合并基线校验 | 未执行对应测试 |
| REQ-TEAM-006 | 接管时停止竞争写入，保存部分成果与外部动作状态；继续时重新建立基线 | `/agents、/members` | D | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：成员/Agent列表及工作负责人和接管状态 | 真实停止竞争写入与恢复基线 | 未执行对应测试 |
| REQ-TEAM-007 | 下游接收固定成果包，可接受、拒收或逐项接收；拒收引用具体不满足项 | `/agents、/members` | D | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：成员/Agent列表及工作负责人和接管状态 | 逐项固定版本接收 | 未执行对应测试 |
| REQ-TEAM-008 | 上游返工形成新的执行 / 成果版本；重新交接前重验受影响条件 | `/agents、/members` | D | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：成员/Agent列表及工作负责人和接管状态 | 跨环节重新交接与影响重验 | T03 |
| REQ-TEAM-009 | 跨环节分歧按指定 Human 决定者升级，涉及需求变化另走变更审批 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 指定Human争议升级/需求变更 | 未执行对应测试 |
| REQ-TEAM-010 | 多模型比较保留各候选、证据与成本，合成后仍走正式验收 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 候选证据/成本/合成验收 | 未执行对应测试 |
| REQ-TEAM-011 | 交接包包含目标、计划、成果版本、决定、未验证项、未决动作和剩余预算 | `/agents、/members` | M | `src/pages-admin.jsx: Agents / Team; src/pages-work.jsx: WorkDetail`：仅相邻能力：成员/Agent列表及工作负责人和接管状态；未见本条专用流程 | 结构化完整交接包 | 未执行对应测试 |
| REQ-CHAT-001 | 提供类似 Slack 的组织 / 项目 / 环节频道、共享频道、群聊、Human↔Human、Human↔Agent 与授权 Agent 协作 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 动态频道/群聊及授权协作 | 未执行对应测试 |
| REQ-CHAT-002 | 支持线程、@提及、回复、附件、引用、反应、置顶、未读、搜索和通知偏好 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 全部提及/附件/置顶/未读/通知交互 | B5执行线程进出/回复子集 |
| REQ-CHAT-003 | 支持文本、图片、文件、音视频资料；语音输入 / 转写标明来源与转写状态 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 真实媒体上传/语音转写 | 未执行对应测试 |
| REQ-CHAT-004 | 会话中生成并确认计划、任务、SOP、修改与审批，关联真实对象和固定版本 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 固定版本计划/SOP/审批会话卡片 | 未执行对应测试 |
| REQ-CHAT-005 | 明确唤醒对象、触发原因、回复链和循环上限，系统事件默认不广播唤醒全部 Agent | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | M | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：仅相邻能力：本地消息/草稿/编辑/分享UI和连接器记录；未见本条专用流程 | 唤醒对象/原因/循环预算 | 未执行对应测试 |
| REQ-CHAT-006 | 永久保存消息、编辑版本、引用和必要附件；撤回展示墓碑，历史依权限与审计政策读取 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | P | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录；不含本条真实生产证明 | 永久消息/附件存储与政策 | 未执行对应测试 |
| REQ-CHAT-007 | 人类消息、Agent 消息、系统事件和代登记行为明确标识；显示发送、落库、失败与重试状态 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 真实发送/落库/失败/重试状态 | 未执行对应测试 |
| REQ-CHAT-008 | 协作事件和工具输出按步骤折叠，关键成果、阻塞和待审批持续可见 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 真实事件折叠与持续关键状态 | 未执行对应测试 |
| REQ-CHAT-009 | 私聊、共享历史、附件、搜索与摘要统一鉴权；托管 Agent 会话显示管理员 / 负责人可见政策 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | P | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录；不含本条真实生产证明 | 私聊/搜索/摘要统一服务端鉴权 | 未执行对应测试 |
| REQ-CHAT-010 | 新增外部成员前明确其可见历史与附件，优先创建明确共享范围 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 外部成员可见历史与附件授权预览 | B5仅选定文本分享子集 |
| REQ-CHAT-011 | 会话在线状态、正在输入、已读与任务接手是不同信号 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | M | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：仅相邻能力：本地消息/草稿/编辑/分享UI和连接器记录；未见本条专用流程 | 在线/输入/已读/认领独立状态 | 未执行对应测试 |
| REQ-CHAT-012 | 会话引用可入知识 / 记忆，保留来源和权限；普通讨论不自动升级为团队正式结论 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat`：选定消息可保存带来源ID、私人/项目范围的待确认记忆候选 | 候选完整来源权限校验及确认后上下文使用规则 | 未执行对应测试 |
| REQ-CHAT-013 | 支持断线补拉、游标、稳定排序、跨端未读同步，长历史按锚点增量加载 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | P | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录；不含本条真实生产证明 | 游标补拉/排序/跨端未读 | 未执行对应测试 |
| REQ-CHAT-014 | 明确同一事件在频道、待办、通知与右栏的归属，处理结果共用一份事实 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 事件归属与各出口同事实 | 未执行对应测试 |
| REQ-CHAT-015 | 每个连接器声明单向通知、双向消息、群 / 私聊、线程、附件、编辑撤回、回执及历史导入的实际能力 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 各平台实际能力验证 | 未执行对应测试 |
| REQ-CHAT-016 | 外部工作区、频道、用户与内部租户、会话、成员建立显式授权映射 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | D | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录 | 真实外部身份/频道授权映射 | 未执行对应测试 |
| REQ-CHAT-017 | 外部事件验签、幂等入库；保存来源平台、账号、会话、消息 ID、方向和回复映射 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | P | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录；不含本条真实生产证明 | 验签/幂等/来源ID/回复映射 | 未执行对应测试 |
| REQ-CHAT-018 | 外发有投递记录、限流、重试与失败状态；入站正文和必要附件按授权范围归档 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | P | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录；不含本条真实生产证明 | 限流重试/投递与附件归档 | 未执行对应测试 |
| REQ-CHAT-019 | 项目管理员控制绑定、转发方向、共享范围、暂停和撤销；内部消息对外分享重新校验权限 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | P | `src/pages-collab.jsx: Chat; src/pages-admin.jsx: ResourcePage(connectors)`：本地消息/草稿/编辑/分享UI和连接器记录；不含本条真实生产证明 | 转发范围改变及撤权再鉴权 | 未执行对应测试 |
| REQ-CLOUD-001 | 云端保存组织、项目、计划、Flow、任务、执行、成果、消息、审批、费用与监控的权威状态 | `/overview、/governance/audit` | P | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage；不含本条真实生产证明 | 云端权威状态及持久化 | 未执行对应测试 |
| REQ-CLOUD-002 | 同一工作台汇总多个授权项目，展示目标、里程碑、Flow 进度、负载、阻塞、预算、告警和待审批 | `/overview、/governance/audit` | D | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage | 全量授权项目与负载/预算来源 | 未执行对应测试 |
| REQ-CLOUD-003 | 管理者按组织 / 部门 / 团队 / 项目 / 时间查看，执行者默认“与我有关” | `/overview、/governance/audit` | D | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage | 组织层级筛选与真实主体范围 | 未执行对应测试 |
| REQ-CLOUD-004 | 显示状态来源、最近上报、已确认版本和新鲜度，区分在线、离线、失联与未知 | `/overview、/governance/audit` | D | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage | 真实上报新鲜度/未知状态 | 未执行对应测试 |
| REQ-CLOUD-005 | 通过服务端授权订阅实时更新；客户端按版本与事件序号处理乱序和重连 | `/overview、/governance/audit` | P | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage；不含本条真实生产证明 | 授权实时订阅/乱序重连 | 未执行对应测试 |
| REQ-CLOUD-006 | 节点离线时保留已知状态和待核实动作，未授权离线副作用不得继续执行 | `/overview、/governance/audit` | P | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage；不含本条真实生产证明 | 节点离线副作用控制 | 未执行对应测试 |
| REQ-CLOUD-007 | 云端命令持久化、带期限、目标版本和幂等键；执行节点报告收到、开始与结果 | `/overview、/governance/audit` | P | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage；不含本条真实生产证明 | 持久命令/期限/幂等回执 | 未执行对应测试 |
| REQ-CLOUD-008 | 支持项目总览与跨项目大盘共存；全局大盘可进入管理协作上下文 | `/overview、/governance/audit` | D | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage | 跨项目及实例上下文全部返回路径 | 未执行对应测试 |
| REQ-CLOUD-009 | 云端重启 / 故障切换后恢复持久工作、调度与审批；分区时选择一致性安全边界 | `/overview、/governance/audit` | P | `src/pages-work.jsx: Dashboard; src/store.js: update`：本地工作台汇总；权威状态实际在localStorage；不含本条真实生产证明 | 云端重启/故障切换/分区安全 | 未执行对应测试 |
| REQ-EXEC-001 | 一次执行绑定工作、环节、参与者、输入版本、上下文快照、runtime / 模型、节点和预算 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | D | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作 | 独立执行ID及完整上下文快照 | 未执行对应测试 |
| REQ-EXEC-002 | 执行会话支持多轮对话、工具记录、输入、附件、工具授权、暂停、停止、继续和接管 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | D | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作 | 真实工具授权/暂停/停止事件 | 未执行对应测试 |
| REQ-EXEC-003 | 反馈来自真实事件：排队、启动、阶段进度、输出、等待、心跳、错误和结果 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | P | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作；不含本条真实生产证明 | 真实执行反馈/心跳与阶段成果 | 未执行对应测试 |
| REQ-EXEC-004 | 普通运行补充在安全边界应用，区分已收到、待应用、已应用与无法应用 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | M | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：仅相邻能力：以workId充当执行入口的模拟记录和动作；未见本条专用流程 | 补充要求已收到/待应用/应用状态 | 未执行对应测试 |
| REQ-EXEC-005 | 同一执行产生的部分成果及时登记，工作稿、提交版、接受版明确区分 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | D | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作 | 部分成果登记与三类版本 | 未执行对应测试 |
| REQ-EXEC-006 | 关闭页面、换设备不影响后台执行；取消是显式命令并有完成回执 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | P | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作；不含本条真实生产证明 | 后台执行独立于页面/取消回执 | 未执行对应测试 |
| REQ-EXEC-007 | runtime 支持时原生恢复；不支持时用交接包新建关联执行，明确恢复方式 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | M | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：仅相邻能力：以workId充当执行入口的模拟记录和动作；未见本条专用流程 | 原生恢复与交接新执行区分 | 未执行对应测试 |
| REQ-EXEC-008 | 失败分为环境故障、质量问题、缺资料、权限拒绝和范围变化 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | M | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：仅相邻能力：以workId充当执行入口的模拟记录和动作；未见本条专用流程 | 错误分类与恢复动作 | 未执行对应测试 |
| REQ-EXEC-009 | 未启动的安全操作可有限重试；已启动且副作用不确定必须核实或补偿 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | M | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：仅相邻能力：以workId充当执行入口的模拟记录和动作；未见本条专用流程 | 副作用核实/补偿/有限重试 | 未执行对应测试 |
| REQ-EXEC-010 | 记录进程退出、实际成果、检查与业务完成的不同状态 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | D | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作 | 进程/成果/检查/业务状态分离 | 未执行对应测试 |
| REQ-EXEC-011 | 执行目录、工具和网络按任务隔离，只有所需临时凭据 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | P | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作；不含本条真实生产证明 | 进程/目录/工具/网络隔离 | 未执行对应测试 |
| REQ-EXEC-012 | 支持人工观察执行与专业工具接续，人类制作的成果也可进入相同验证闭环 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | D | `src/pages-collab.jsx: Chat; src/pages-work.jsx: WorkDetail; src/store.js: workCommand`：以workId充当执行入口的模拟记录和动作 | 专业工具接续及人工成果导入 | 未执行对应测试 |
| REQ-NODE-001 | 执行器分布在多个服务器与个人电脑，支持 macOS、Linux、Windows，登记节点身份与负责人 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 三OS真实节点注册 | 未执行对应测试 |
| REQ-NODE-002 | 提供单命令安装 daemon / CLI / 系统服务，安装可重复执行，包与二进制有版本和完整性验证 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 原生签名/幂等安装 | 未执行对应测试 |
| REQ-NODE-003 | 提供 install、uninstall、start、stop、restart、status、logs、doctor、update、auth login/logout | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 完整CLI命令实际行为 | 未执行对应测试 |
| REQ-NODE-004 | macOS 使用 launchd、Linux 使用 systemd 或受支持的用户服务、Windows 使用 SCM 服务 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | launchd/systemd/SCM服务安装 | 未执行对应测试 |
| REQ-NODE-005 | 支持令牌登录与设备码登录；首次注册凭据换取节点专属、可轮换和撤销凭据 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 设备码/节点凭据轮换 | 未执行对应测试 |
| REQ-NODE-006 | CLI 支持自动化安全输入令牌，优先标准输入 / 系统凭据存储；兼容参数模式需避免回显与日志收集 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 安全令牌输入与日志脱敏 | 未执行对应测试 |
| REQ-NODE-007 | 节点主动向云端建立出站连接，提供心跳、能力发现、运行槽、活动执行与版本 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 真实出站心跳及能力发现 | 未执行对应测试 |
| REQ-NODE-008 | 支持任务租约、排空、维护、停机、失联重连与吊销；同一工作目录限制竞争写入 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 任务租约/写入所有权 | 未执行对应测试 |
| REQ-NODE-009 | 节点支持安全自更新和兼容检查，运行中任务先排空或明确接续；失败可回滚 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 兼容升级/排空/回滚 | 未执行对应测试 |
| REQ-NODE-010 | 支持受控服务器 / 集群池，按任务隔离目录或临时环境；资源配额与清理策略可配置 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 真实集群配额/隔离清理 | 未执行对应测试 |
| REQ-NODE-011 | 节点离线期间本地缓冲有界，记录待上传事件 / 成果；限额、断电和磁盘不足处理明确 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 有界缓冲/断电磁盘故障 | 未执行对应测试 |
| REQ-NODE-012 | 用户能停用或卸载节点；stop 停止接新任务并执行规定的优雅关闭，强制停止单独显式操作 | `/machines、/machines/n1` | P | `src/pages-admin.jsx: Machines; src/data.js: nodes`：节点样例、安装说明和本地状态按钮；不含本条真实生产证明 | 优雅关闭/强停/卸载残留检查 | 未执行对应测试 |
| REQ-PREVIEW-001 | 使用 frp 式反向隧道，通过节点出站连接将当前执行的本地服务映射到受控云端预览入口 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 真实frp出站隧道 | 未执行对应测试 |
| REQ-PREVIEW-002 | 自动分配不可预测的随机子域名，统一 DNS / TLS 管理，绑定租户、项目、执行、服务与期限 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 随机域名/DNS/TLS/租期绑定 | 未执行对应测试 |
| REQ-PREVIEW-003 | 预览域与控制台使用隔离来源，按实例隔离；控制台身份 Cookie 不透传给不可信预览应用 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 独立来源与Cookie隔离 | 未执行对应测试 |
| REQ-PREVIEW-004 | 支持 Web 和 API，含必要 HTTP、WebSocket、SSE、Range、请求体与响应流 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | HTTP/WebSocket/SSE/Range协议 | 未执行对应测试 |
| REQ-PREVIEW-005 | 服务登记验证端口归属、执行工作区和实际进程，只转发被授权的本地服务 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 端口进程/工作区归属核验 | 未执行对应测试 |
| REQ-PREVIEW-006 | 记录申请、配置、启动、健康、可用、排空、释放、失败状态，所有操作幂等 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 真实生命周期幂等状态 | 未执行对应测试 |
| REQ-PREVIEW-007 | 采用租约、心跳、TTL、空闲回收和最长存活；执行结束、撤权、节点失联触发释放 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 租约TTL/撤权失联回收 | 未执行对应测试 |
| REQ-PREVIEW-008 | 清理涵盖隧道代理、节点子进程、监听端口、临时文件、凭据、域名映射 / 租约 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 资源逐项实际清理证明 | 未执行对应测试 |
| REQ-PREVIEW-009 | 定期核对云端登记与 frp 服务端、节点实际状态，检查连接、端口、健康、TLS 和资源泄漏 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 云端/节点/frp对账扫描 | 未执行对应测试 |
| REQ-PREVIEW-010 | 支持评审指定版本的隔离预览，实时工作稿与已提交版本明确区分 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | D | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态 | 固定版本可恢复隔离预览 | T12 |
| REQ-PREVIEW-011 | 外部分享需指定收件范围、只读能力、期限和人类授权，可撤销并审计 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 外部共享期限/人类授权/撤销 | 未执行对应测试 |
| REQ-PREVIEW-012 | 预览数量、并发、连接、带宽和流量有配额，配额异常可定位项目 / 执行 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | P | `src/task-previews.jsx: TaskPreviews / PreviewContent; src/pages-admin.jsx: Machines`：模拟Web/API/文件/终端标签及连接状态；不含本条真实生产证明 | 并发带宽配额与异常定位 | 未执行对应测试 |
| REQ-REMOTE-001 | 支持执行工作区远程文件树、查看、搜索、下载与版本差异；敏感文件按策略遮蔽 | `/machines/n1?tab=files、/machines/n1?tab=terminal` | D | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令 | 实际文件协议/搜索下载/敏感遮蔽 | 未执行对应测试 |
| REQ-REMOTE-002 | 文件上传 / 编辑 / 删除须独立权限、版本校验和必要接管，署名真实操作人 | `/machines/n1?tab=files、/machines/n1?tab=terminal` | D | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令 | 文件写入权限/基线冲突 | 未执行对应测试 |
| REQ-REMOTE-003 | 使用 xterm.js 提供实时终端界面；终端后端由节点创建 PTY，Windows 使用受支持的 ConPTY | `/machines/n1?tab=files、/machines/n1?tab=terminal` | P | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令；不含本条真实生产证明 | xterm.js及PTY/ConPTY | 未执行对应测试 |
| REQ-REMOTE-004 | 区分只读终端观察和可交互控制；多人观察可以并行，交互写入实行明确控制权 | `/machines/n1?tab=files、/machines/n1?tab=terminal` | P | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令；不含本条真实生产证明 | 服务端单写控制权 | 未执行对应测试 |
| REQ-REMOTE-005 | 交互终端仅授权 Human，独立权限、必要重新认证、空闲超时和审计；个人电脑默认关闭 | `/machines/n1?tab=files、/machines/n1?tab=terminal` | P | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令；不含本条真实生产证明 | Human再认证/空闲超时 | 未执行对应测试 |
| REQ-REMOTE-006 | 终端有起止、重连、断开、输出回放与敏感信息处理；禁止无边界暴露主机 Shell | `/machines/n1?tab=files、/machines/n1?tab=terminal` | P | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令；不含本条真实生产证明 | 终端输出回放及秘密处理 | 未执行对应测试 |
| REQ-REMOTE-007 | 执行输出、终端控制和 frp Web 预览可复用连接基础设施但具有独立授权与限额 | `/machines/n1?tab=files、/machines/n1?tab=terminal` | P | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令；不含本条真实生产证明 | 独立通道权限与限额 | 未执行对应测试 |
| REQ-REMOTE-008 | 移动客户端可观察与经授权操作远程节点，输入辅助、断线与后台状态清楚 | `/machines/n1?tab=files、/machines/n1?tab=terminal` | D | `src/pages-admin.jsx: Machines; src/task-previews.jsx: PreviewContent`：示例文件内容与白名单模拟终端命令 | 真机输入/后台/断线行为 | 未执行对应测试 |
| REQ-REPO-001 | 项目连接一个或多个授权仓库，登记平台、仓库、分支、路径与负责团队 | `/repositories` | D | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仓库记录及只更新同步时间的模拟动作 | 真实仓库授权/多仓库配置 | 未执行对应测试 |
| REQ-REPO-002 | 监控 push、commit、PR / MR、review、merge、tag / release、CI 结果等提供商支持的事件 | `/repositories` | P | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仓库记录及只更新同步时间的模拟动作；不含本条真实生产证明 | 供应商事件实际摄取 | 未执行对应测试 |
| REQ-REPO-003 | 优先受验签 webhook，提供增量轮询补偿；事件去重、乱序处理、丢失对账 | `/repositories` | P | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仓库记录及只更新同步时间的模拟动作；不含本条真实生产证明 | 验签/去重/乱序/漏发对账 | 未执行对应测试 |
| REQ-REPO-004 | 按项目、分支、路径、事件与严重度配置通知到群、私信、待办或邮件 | `/repositories` | M | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仅相邻能力：仓库记录及只更新同步时间的模拟动作；未见本条专用流程 | 多维通知路由配置 | 未执行对应测试 |
| REQ-REPO-005 | 关联变更到目标、任务、Flow、执行和证据；无法自动关联时保留未归属事件 | `/repositories` | M | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仅相邻能力：仓库记录及只更新同步时间的模拟动作；未见本条专用流程 | 真实变更关联/未归属事件 | 未执行对应测试 |
| REQ-REPO-006 | 固定 commit / 构建输入；新提交、force push、分支删除或 CI 重跑使相关旧检查标记适用性变化 | `/repositories` | P | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仓库记录及只更新同步时间的模拟动作；不含本条真实生产证明 | force push/重跑检查失效 | 未执行对应测试 |
| REQ-REPO-007 | 可按授权策略触发代码检查、生成摘要、建立待处理项或启动指定 SOP | `/repositories` | M | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仅相邻能力：仓库记录及只更新同步时间的模拟动作；未见本条专用流程 | 授权事件触发SOP | 未执行对应测试 |
| REQ-REPO-008 | AI 解释变更与影响时引用实际 diff / 文件，披露未读范围和失败来源 | `/repositories` | M | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仅相邻能力：仓库记录及只更新同步时间的模拟动作；未见本条专用流程 | 真实diff引用及未读披露 | 未执行对应测试 |
| REQ-REPO-009 | 连接器权限撤销、同步失败、速率限制、仓库迁移进入异常与恢复流程 | `/repositories` | P | `src/pages-admin.jsx: ResourcePage(repositories); src/store.js: resourceCommand`：仓库记录及只更新同步时间的模拟动作；不含本条真实生产证明 | 限流/迁移/撤权恢复 | 未执行对应测试 |
| REQ-COST-001 | 按组织、项目、计划、Flow 环节、任务、Human 发起者、Agent、模型、执行统计用量和费用 | `/cost、/models` | D | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：样例用量预算与配置/策略记录 | 全部维度计量来源与汇总 | 未执行对应测试 |
| REQ-COST-002 | 区分输入、输出、缓存、供应商独立报告的推理 Token 与其他计费单位 | `/cost、/models` | P | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：样例用量预算与配置/策略记录；不含本条真实生产证明 | 真实各类Token/计费单位 | 未执行对应测试 |
| REQ-COST-003 | 保存供应商实际值、估算值、未知值、价格版本、币种、时间和对账状态 | `/cost、/models` | P | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：样例用量预算与配置/策略记录；不含本条真实生产证明 | 价格版本/未知值/币种对账 | 未执行对应测试 |
| REQ-COST-004 | 支持组织 / 项目 / Agent / 任务预算、每日 / 周期上限与并发限制，执行前预留、完成后结算 | `/cost、/models` | P | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：样例用量预算与配置/策略记录；不含本条真实生产证明 | 并发预算预留结算 | 未执行对应测试 |
| REQ-COST-005 | 达到软阈值通知，硬阈值停止新调用并在安全边界暂停；进行中调用按可取消性处理 | `/cost、/models` | P | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：样例用量预算与配置/策略记录；不含本条真实生产证明 | 真实硬软限额阻止调用 | 未执行对应测试 |
| REQ-COST-006 | 自动调节可缩短无关上下文、使用缓存 / 摘要、限制候选数量、调整并发与输出上限 | `/cost、/models` | M | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：仅相邻能力：样例用量预算与配置/策略记录；未见本条专用流程 | 自动上下文/并发/输出调节 | 未执行对应测试 |
| REQ-COST-007 | 在允许模型集合内，按任务能力、风险、质量要求与预算路由；允许策略性降档或升级 | `/cost、/models` | M | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：仅相邻能力：样例用量预算与配置/策略记录；未见本条专用流程 | 能力风险质量模型路由 | 未执行对应测试 |
| REQ-COST-008 | 原生执行中的换模型能力由适配器声明；不支持时在边界新建关联执行 | `/cost、/models` | M | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：仅相邻能力：样例用量预算与配置/策略记录；未见本条专用流程 | 适配器切换边界 | 未执行对应测试 |
| REQ-COST-009 | 展示每次自动调节的触发原因、原配置、新配置、预计影响和实际节省 / 质量结果 | `/cost、/models` | M | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：仅相邻能力：样例用量预算与配置/策略记录；未见本条专用流程 | 逐次调节因果/实际节省 | 未执行对应测试 |
| REQ-COST-010 | 提供成本趋势、异常检测、Top 消耗、预算预测与每个已接受成果的总成本 | `/cost、/models` | D | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：样例用量预算与配置/策略记录 | 趋势预测/异常/接受成果归集 | 未执行对应测试 |
| REQ-COST-011 | 用量与账单对账修正追加记录，保留原估算和差异；不能改掉历史计量 | `/cost、/models` | M | `src/pages-admin.jsx: Cost / ResourcePage(models); src/data.js`：仅相邻能力：样例用量预算与配置/策略记录；未见本条专用流程 | 追加对账差异不改历史 | 未执行对应测试 |
| REQ-SCHED-001 | 支持一次性、固定间隔、日历 / Cron 和事件触发；可关联任务、SOP、报告、检查与通知 | `/schedules` | D | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：定时记录和手动生成报告草稿/待执行检查 | 真实一次性/间隔/Cron/事件触发 | 未执行对应测试 |
| REQ-SCHED-002 | 保存时区、夏令时规则、起止、节假日 / 工作日策略、负责人和授权来源 | `/schedules` | P | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：定时记录和手动生成报告草稿/待执行检查；不含本条真实生产证明 | DST/节假日策略 | 未执行对应测试 |
| REQ-SCHED-003 | 明确错过触发的跳过 / 合并补跑 / 有界逐次补跑，以及并发、排队和互斥策略 | `/schedules` | M | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：仅相邻能力：定时记录和手动生成报告草稿/待执行检查；未见本条专用流程 | 漏跑补偿/互斥排队 | 未执行对应测试 |
| REQ-SCHED-004 | 调度状态持久化，带幂等触发键、租约与故障转移；区分调度投递与实际执行 | `/schedules` | P | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：定时记录和手动生成报告草稿/待执行检查；不含本条真实生产证明 | 持久租约/幂等/故障转移 | 未执行对应测试 |
| REQ-SCHED-005 | 每次运行重新检查有效权限、预算、资料版本、节点能力和必要审批 | `/schedules` | P | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：定时记录和手动生成报告草稿/待执行检查；不含本条真实生产证明 | 每次权限预算版本重新核验 | 未执行对应测试 |
| REQ-SCHED-006 | 支持暂停、恢复、修改、终止、手动运行、历史与失败重试 | `/schedules` | D | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：定时记录和手动生成报告草稿/待执行检查 | 真实定时历史/失败重试 | 未执行对应测试 |
| REQ-SCHED-007 | 连续失败、超时、预算不足、长期未运行触发告警与自动停用策略 | `/schedules` | M | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：仅相邻能力：定时记录和手动生成报告草稿/待执行检查；未见本条专用流程 | 失败阈值告警及自动停用 | 未执行对应测试 |
| REQ-SCHED-008 | 定时执行仍受内部验收与人工决定约束；生成草稿和正式提交 / 发布分开 | `/schedules` | D | `src/pages-admin.jsx: ResourcePage(schedules); src/store.js: resourceCommand`：定时记录和手动生成报告草稿/待执行检查 | 真实调度仍遵守全部质量及发布门禁 | T10 |
| REQ-QA-001 | 每个环节开工前确定编号要求、检查方式、输入 / 输出、指定评审者和完成条件 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 开工前结构化编号契约 | 未执行对应测试 |
| REQ-QA-002 | 所有环节交接和最终交付前，当前版本的必需内部验收全部通过 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 全环节必需检查集合 | T01、Q1、F1、B1 |
| REQ-QA-003 | 区分确定性检查、AI 辅助判断和 Human 专业判断；主观要求必须有适当评审方法 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 真实确定性/AI/Human检查职责 | 未执行对应测试 |
| REQ-QA-004 | 软件保留独立重跑、冻结验收、测试 Agent、跨模型产品验收与工具校准要求 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | qa.js / 固定计划和批次、报告、稳定身份独立性；check仅开始待执行验证 | 真实工具独立重跑、测试Agent编排、跨模型产品验收和校准 | qa-plans.test.js；browser-qa-plans.js；6类变异 |
| REQ-QA-005 | 视频检查规格、素材 / 工程完整性、声画与人工审片；小说检查设定 / 连续性与编辑判断 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | M | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：仅相邻能力：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run；未见本条专用流程 | 媒体/小说专业验证与审片 | 未执行对应测试 |
| REQ-QA-006 | 支持定期、里程碑、提交、上游变化、交付前触发检查和评审 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 全部真实检查触发器 | 未执行对应测试 |
| REQ-QA-007 | 相同版本的可复用检查结果按验证器 / 配置 / 输入版本缓存，失效后重检 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | M | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：仅相邻能力：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run；未见本条专用流程 | 配置及输入指纹缓存/失效 | 未执行对应测试 |
| REQ-QA-008 | 失败进入本环节修复或上游退回，反馈明确位置、期望、实际与阻塞范围 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 位置/实际/期望/阻塞的结构化反馈 | 未执行对应测试 |
| REQ-QA-009 | 误检可申请复核；新复核结论保留理由、方法、授权与旧结论，不直接篡改失败结果 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 复核授权与结构化理由/方法；旧失败证据已有本地历史保留 | T02、Q2 |
| REQ-QA-010 | 检查者与执行者遵守职责分离；适用的例外需明确规则，不能把自评标成独立评审 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | M | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：仅相邻能力：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run；未见本条专用流程 | 执行者与检查者身份分离 | 未执行对应测试 |
| REQ-QA-011 | 最终交付打包固定成果版本、源文件 / 导出件、证据、要求映射、局限与使用说明 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 真实源文件/证据清单与哈希 | 未执行对应测试 |
| REQ-QA-012 | 对外交付和必要发布由 Human 负责并批准，客户回执区分接受、部分接受、拒收 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 逐项回执与独立发布授权 | T11 |
| REQ-QA-013 | 已交付后发现问题，保留原包、定位影响、发起修复 / 变更与重新验收 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | D | `src/pages-work.jsx: WorkDetail; src/store.js: workCommand; src/pages-admin.jsx: ResourcePage(tests)`：`qualityResult / syncQuality` 根据当前版本检查与证据同步门禁/接受状态，复核仍为显式 mock-run | 交付后影响定位/新包重验 | T03、T11、T12 |
| REQ-APP-001 | 审批覆盖目标 / OKR、计划、SOP 发布 / 变更、必要分派、工具与生产权限、预算例外、验收争议、正式交付 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 全部审批类型与动作覆盖 | 未执行对应测试 |
| REQ-APP-002 | 审批流支持顺序、并行、指定角色、ALL / ANY / 法定人数策略，明确超时、代理和升级 | `/approvals、/deliveries、/policies` | M | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：仅相邻能力：本地审批/回执状态和部分角色版本校验；未见本条专用流程 | 顺序并行/法定人数/代理超时 | 未执行对应测试 |
| REQ-APP-003 | 每项审批绑定对象版本、请求人、审批人、授权来源、理由、证据、时限和结果 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 完整授权来源/版本/证据期限 | T08 |
| REQ-APP-004 | 同一审批可在会话、工作台、Flow、成果详情处理，执行相同服务端校验 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 全部入口共同服务端校验 | 未执行对应测试 |
| REQ-APP-005 | 支持批准、拒绝、退回补充、撤回、过期和重新提交；拒绝 / 退回包含具体原因 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 撤回/过期/重提/原因约束 | 未执行对应测试 |
| REQ-APP-006 | 人类责任人与 Agent 委托明确；Agent 不批准自产、不给自己授权、不确认人类报告 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 真实Agent委托及自批隔离 | T07、T10 |
| REQ-APP-007 | 关键外部副作用、正式交付、合同 / 范围承诺、生产访问授权保留 Human 关口 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 合同/价格/生产权限Human关口 | T07 |
| REQ-APP-008 | 低风险自动决定依据预先确认策略，记录系统 / Agent 主体、策略版本、输入事实和理由 | `/approvals、/deliveries、/policies` | M | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：仅相邻能力：本地审批/回执状态和部分角色版本校验；未见本条专用流程 | 低风险策略版本与自动决定记录 | 未执行对应测试 |
| REQ-APP-009 | 讨论中的意见或推断可自动提取为决定草案，有权确认后成为业务决定 | `/approvals、/deliveries、/policies` | M | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：仅相邻能力：本地审批/回执状态和部分角色版本校验；未见本条专用流程 | 讨论提取草案再确认 | 未执行对应测试 |
| REQ-APP-010 | 审批前重新核验版本、权限、验收与预算，必要动作重新认证；撤权即时阻止后续动作 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 再认证/撤权/预算复核 | 未执行对应测试 |
| REQ-APP-011 | 审批流与内部验收相互独立，审批同意不能替代未通过的必需技术 / 专业门禁 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 真实门禁结果与审批不可互代 | T01、Q1、B1 |
| REQ-APP-012 | 一键人工接管可暂停自治、撤销相关委托、指定负责人并保留恢复方案 | `/approvals、/deliveries、/policies` | D | `src/pages-admin.jsx: ResourcePage(approvals/deliveries); src/store.js: resourceCommand / workCommand`：本地审批/回执状态和部分角色版本校验 | 真实暂停自治/撤销委托/恢复方案 | 未执行对应测试 |
| REQ-REPORT-001 | 支持定时自动生成及手动生成 / 打开日、周、月、季、年报，支持成员、环节、项目、团队、组织层级；同一租户、主体、周期类型、起止日期和时区只有一份逻辑报告 | `/reports、/worklogs` | D | reporting.js / ReportGeneration：手动与模拟定时共享周期唯一键，归属与Human提交负责人分开，日周月季年边界 | 真实AI及持久调度/并发唯一约束，环节与完整组织汇总 | reports.test.js；browser-reports.js |
| REQ-REPORT-002 | Human 可由 AI 生成草稿，但必须本人确认后上报；Agent 报告可按策略自动提交，仍由指定 Human 审核 | `/reports、/worklogs` | D | Human报告本人确认；重复生成保留人工内容及已提交快照，修订不更换逻辑ID | Agent策略自动提交与真实审核通知 | reports.test.js；browser-reports.js |
| REQ-REPORT-003 | 报告包含完成工作、未完成与原因、风险阻塞、成本质量、复盘和下期计划 | `/reports、/worklogs` | D | `src/pages-admin.jsx: ResourcePage(reports); src/store.js: resourceCommand`：报告字段、静态核对提示与本人提交限制 | 完成/未完成/成本质量/下期结构 | 未执行对应测试 |
| REQ-REPORT-004 | 时间上日→周→月→季→年、组织上成员→项目 / 团队→组织汇总；避免多重归属重复计数 | `/reports、/worklogs` | D | typed引用树去重/周期核对；Agent稳定ID归属，Human确认投入与遥测分账 | 完整组织层级自动汇总与生产跨时区调度 | reports.test.js；browser-reports.js |
| REQ-REPORT-005 | 提交前核对计划、OKR、工作状态、验收和下级报告；阻断错误禁止提交，非阻断缺失显式标记 | `/reports、/worklogs` | D | ReportFacts / reportEvidence：可选择固定版本工作、工时、下级报告；来源/日期/版本/完成数核对及提交阻断 | OKR与完整计划事实联动尚缺 | reports.test.js；browser-reports.js主代理1440 PASS |
| REQ-REPORT-006 | 主管可确认、退回和评论，逾期提醒与升级，历史修订可比较 | `/reports、/worklogs` | D | `src/pages-admin.jsx: ResourcePage(reports); src/store.js: resourceCommand`：报告字段、静态核对提示与本人提交限制 | 逾期升级/历史修订比较 | 未执行对应测试 |
| REQ-REPORT-007 | 并列展示系统事实和作者主观判断；明显冲突要求解释与纠偏 | `/reports、/worklogs` | D | ReportFacts：系统事实与作者摘要分开显示；声明完成数与实际不符时阻断 | 主观判断解释与纠偏流程尚缺；不对自由文本自动定性 | reports.test.js；browser-reports.js主代理1440 PASS |
| REQ-REPORT-008 | 从确认的复盘提出改进任务、SOP 调整或经验记忆，分别走对应确认 | `/reports、/worklogs` | M | `src/pages-admin.jsx: ResourcePage(reports); src/store.js: resourceCommand`：仅相邻能力：报告字段、静态核对提示与本人提交限制；未见本条专用流程 | 复盘转任务/SOP/记忆审批 | 未执行对应测试 |
| REQ-WATCH-001 | 监督 Agent 在授权范围观察 Human / Agent 的目标、计划、任务、Flow、进展、成本和交接 | `/overview、/health、/schedules` | D | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：风险/告警样例和手动状态动作 | 监督Agent授权观察 | 未执行对应测试 |
| REQ-WATCH-002 | 结合确定性规则与 AI 分析识别延期、卡住、进度冲突、循环返工、交接失败、预算风险和无人负责 | `/overview、/health、/schedules` | M | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：仅相邻能力：风险/告警样例和手动状态动作；未见本条专用流程 | 真实风险规则及模型分析 | 未执行对应测试 |
| REQ-WATCH-003 | 检测跨环节影响，提示哪个上游问题将影响哪些下游与里程碑 | `/overview、/health、/schedules` | M | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：仅相邻能力：风险/告警样例和手动状态动作；未见本条专用流程 | 跨环节影响图 | 未执行对应测试 |
| REQ-WATCH-004 | 在群、私信、工作台和适用推送中提醒，包含责任人、影响、推荐动作与处理期限 | `/overview、/health、/schedules` | D | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：风险/告警样例和手动状态动作 | 多渠道含期限可行动提醒 | 未执行对应测试 |
| REQ-WATCH-005 | 提醒去重、聚合、节流、严重级别和静默窗口，关键停线使用独立升级规则 | `/overview、/health、/schedules` | M | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：仅相邻能力：风险/告警样例和手动状态动作；未见本条专用流程 | 去重节流/静默/停线升级 | 未执行对应测试 |
| REQ-WATCH-006 | 监督 Agent 可提出重规划、调人、改模型和重新检查建议；自动处理必须有明确策略授权 | `/overview、/health、/schedules` | M | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：仅相邻能力：风险/告警样例和手动状态动作；未见本条专用流程 | 授权建议与自动处理 | 未执行对应测试 |
| REQ-WATCH-007 | 监测监督机制自身的心跳、积压、漏检窗口和调用预算 | `/overview、/health、/schedules` | M | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：仅相邻能力：风险/告警样例和手动状态动作；未见本条专用流程 | 监督自身心跳/积压/预算 | 未执行对应测试 |
| REQ-WATCH-008 | 人可纠正误报、说明例外，形成可审核规则改进；保留原风险记录与反馈 | `/overview、/health、/schedules` | M | `src/pages-work.jsx: Dashboard; src/pages-admin.jsx: ResourcePage(health/schedules)`：仅相邻能力：风险/告警样例和手动状态动作；未见本条专用流程 | 误报纠正/例外/规则审批 | 未执行对应测试 |
| REQ-HEALTH-001 | 监控节点 CPU、内存、磁盘容量 / IO、网络、进程、运行槽和可选 GPU 指标 | `/health、/machines` | P | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态；不含本条真实生产证明 | 真实全量主机指标 | 未执行对应测试 |
| REQ-HEALTH-002 | 监控 daemon、模型 / 工具适配器、预览 / frp、队列、调度器、API、数据库、存储和索引作业健康 | `/health、/machines` | P | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态；不含本条真实生产证明 | 所有组件采集/健康来源 | 未执行对应测试 |
| REQ-HEALTH-003 | 支持受控 HTTP / TCP / 进程探测、延迟、错误率、超时、依赖状态、TLS 证书到期和存储风险 | `/health、/machines` | P | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态；不含本条真实生产证明 | 实际HTTP/TCP/TLS探测 | 未执行对应测试 |
| REQ-HEALTH-004 | 节点主动上报与云端探测结合，设置窗口、阈值、连续失败与恢复条件 | `/health、/machines` | P | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态；不含本条真实生产证明 | 上报探测窗口阈值 | 未执行对应测试 |
| REQ-HEALTH-005 | 告警按组织 / 项目 / 服务路由到指定协作群，必要时 @责任人、App 推送或邮件 | `/health、/machines` | D | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态 | 频道/推送/邮件路由 | 未执行对应测试 |
| REQ-HEALTH-006 | 告警有唯一身份与触发、确认、处理中、恢复、关闭记录；支持指派和升级 | `/health、/machines` | D | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态 | 告警身份/指派/升级完整状态 | 未执行对应测试 |
| REQ-HEALTH-007 | 支持聚合、去重、抑制、维护窗口、分级升级与恢复通知，保留被抑制记录 | `/health、/machines` | M | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：仅相邻能力：静态节点指标与告警认领/恢复/关闭状态；未见本条专用流程 | 聚合抑制/维护窗口/恢复通知 | 未执行对应测试 |
| REQ-HEALTH-008 | 告警关联正在执行的任务、Flow、预览和项目；可创建处置工作 | `/health、/machines` | D | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态 | 任务Flow影响关联及处置任务 | 未执行对应测试 |
| REQ-HEALTH-009 | 仅对预先允许的低风险动作自动修复；重启、杀进程、释放资源按授权及副作用规则执行 | `/health、/machines` | M | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：仅相邻能力：静态节点指标与告警认领/恢复/关闭状态；未见本条专用流程 | 授权自动修复副作用控制 | 未执行对应测试 |
| REQ-HEALTH-010 | 监控采集、保留、聚合与采样可配置，采集开销单独计量 | `/health、/machines` | P | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态；不含本条真实生产证明 | 采样保留/开销计量 | 未执行对应测试 |
| REQ-HEALTH-011 | 告警投递失败持久重试，频道不可用时按策略使用后备通知渠道 | `/health、/machines` | P | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：静态节点指标与告警认领/恢复/关闭状态；不含本条真实生产证明 | 投递持久重试/后备渠道 | 未执行对应测试 |
| REQ-HEALTH-012 | 使用已授权真实信息生成 AI 故障摘要和建议，保留原始指标 / 日志链接并脱敏 | `/health、/machines` | M | `src/pages-admin.jsx: Machines / ResourcePage(health); src/data.js`：仅相邻能力：静态节点指标与告警认领/恢复/关闭状态；未见本条专用流程 | 真实原始指标引用及脱敏 | 未执行对应测试 |
| REQ-DATA-001 | 消息、决定、审批、操作、执行、交接、报告与相关材料均持久化并关联主体、时间、对象、版本和来源 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 服务端持久记录/完整来源 | 未执行对应测试 |
| REQ-DATA-002 | 消息默认永久保存，支持分区 / 冷归档；附件与引用保持完整性，普通用户归档不物理删除 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 永久存储/冷归档/附件完整性 | 未执行对应测试 |
| REQ-DATA-003 | 所有已生效决策自动记账：Human 决策、策略自动决定、Agent 获授权决定均保留理由与证据 | `/governance/audit、/knowledge` | D | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索 | 全部决定授权输入审计 | 未执行对应测试 |
| REQ-DATA-004 | 原始消息修订、撤回、审批撤销和决定被取代以追加历史表达；明确当前有效版本 | `/governance/audit、/knowledge` | D | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索 | 消息修订/撤回/审批撤销/决定替代的追加历史与搜索有效版本 | 未执行对应测试 |
| REQ-DATA-005 | 关键状态、审批与审计具有一致的事务边界；大文件采用可核对的上传 / 登记协议 | `/governance/audit、/knowledge` | D | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索 | 状态审批审计事务、大文件上传登记、孤儿对象核对 | 未执行对应测试 |
| REQ-DATA-006 | 全局搜索覆盖消息、线程、目标、计划、任务、Flow、成果、文件内容、决定、审批、报告与授权日志 / 告警 | `/governance/audit、/knowledge` | D | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索 | 全类型全文搜索与授权日志 | 未执行对应测试 |
| REQ-DATA-007 | 关键词与结构化检索为基础，语义搜索可增强；结果显示来源、命中位置、新鲜度与版本 | `/governance/audit、/knowledge` | D | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索 | 命中定位/来源新鲜度/版本 | 未执行对应测试 |
| REQ-DATA-008 | 索引、搜索摘要、计数、缓存、导出、附件与引用逐层遵守租户 / 对象权限 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 每层读取计数导出鉴权 | 未执行对应测试 |
| REQ-DATA-009 | 撤权优先阻止读取，随后处理索引 / 缓存；搜索点击重新鉴权 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 撤权立即阻断/点击再鉴权 | 未执行对应测试 |
| REQ-DATA-010 | 提供增量索引、失败重试、重建、水位、源一致性校验和损坏告警 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 索引水位/重建/失败告警 | 未执行对应测试 |
| REQ-DATA-011 | 提供按权限导出工作证据包、消息历史、文件清单和决定 / 审批记录，含稳定引用与校验信息 | `/governance/audit、/knowledge` | D | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索 | 完整证据包校验与权限 | 未执行对应测试 |
| REQ-DATA-012 | 备份、异地或隔离副本、恢复演练、存储容量预测和保留政策可管理 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 备份/隔离副本/恢复演练 | 未执行对应测试 |
| REQ-DATA-013 | 法律要求、误传秘密和有效删除请求作为独立治理流程，明确保留、遮蔽与清除范围 | `/governance/audit、/knowledge` | M | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：仅相邻能力：localStorage、简化事件日志、JSON导出和本地搜索；未见本条专用流程 | 治理案件/保全/秘密清除 | 未执行对应测试 |
| REQ-DATA-014 | 原始高频指标、缓存与可重建索引可按各自政策压缩 / 清理，永久保存范围明确 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | 保留范围及独立清理策略 | 未执行对应测试 |
| REQ-DATA-015 | 全文解析、OCR、字幕 / 转写与专业格式提取异步处理，保留原件及提取版本 | `/governance/audit、/knowledge` | P | `src/store.js: update / log / exportData; src/main.jsx: Search; src/pages-admin.jsx: Audit`：localStorage、简化事件日志、JSON导出和本地搜索；不含本条真实生产证明 | OCR/专业解析异步版本 | 未执行对应测试 |
| REQ-KNOW-001 | 内置文件资产、知识、记忆和外部连接能力，支持个人、工作、项目、团队和组织范围 | `/knowledge` | D | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：资料/记忆样例、编辑和确认/禁用状态 | 全范围资产/外部来源治理 | 未执行对应测试 |
| REQ-KNOW-002 | 自动从消息、确认决定、接受成果、复盘与纠错提取知识 / 记忆候选 | `/knowledge` | D | `src/pages-collab.jsx: Chat`：从选定消息手动保存来源关联、私人/项目范围及待确认记忆候选；`ResourcePage(knowledge)`编辑/确认 | 从消息/决定/成果/复盘/纠错自动提取候选 | 源码已核对；未执行自动提取测试 |
| REQ-KNOW-003 | 区分偏好、工作状态、事实 / 决策引用与经验；状态从真实领域记录恢复 | `/knowledge` | D | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：资料/记忆样例、编辑和确认/禁用状态 | 事实类型/状态来源恢复 | 未执行对应测试 |
| REQ-KNOW-004 | 自动提取可配置；规则型 / 高影响记忆需审核，未经证实的推断不得成为团队事实 | `/knowledge` | D | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：资料/记忆样例、编辑和确认/禁用状态 | 提取配置/高风险审核 | 未执行对应测试 |
| REQ-KNOW-005 | 用户 / 有权管理员可纠正、禁用、删除派生记忆；原消息保留按独立政策处理 | `/knowledge` | D | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：资料/记忆样例、编辑和确认/禁用状态 | 修订作者/原件政策及删除 | 未执行对应测试 |
| REQ-KNOW-006 | 来源更新、被取代、撤权或删除时传播失效，处理派生摘要、索引和缓存 | `/knowledge` | M | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：仅相邻能力：资料/记忆样例、编辑和确认/禁用状态；未见本条专用流程 | 来源失效传播/索引缓存 | 未执行对应测试 |
| REQ-KNOW-007 | Context Builder 按目标、阶段、角色、权限、模型能力和 Token 预算选择资料 | `/knowledge` | M | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：仅相邻能力：资料/记忆样例、编辑和确认/禁用状态；未见本条专用流程 | Context Builder权限预算选择 | 未执行对应测试 |
| REQ-KNOW-008 | 每次执行保存上下文来源清单、版本、组装器版本、选择理由、缺失与截断信息 | `/knowledge` | M | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：仅相邻能力：资料/记忆样例、编辑和确认/禁用状态；未见本条专用流程 | 执行上下文快照及截断清单 | 未执行对应测试 |
| REQ-KNOW-009 | 支持混合检索、相关性排序、去重与受控摘要；外部文档中的指令不得升级系统权限 | `/knowledge` | M | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：仅相邻能力：资料/记忆样例、编辑和确认/禁用状态；未见本条专用流程 | 混合检索/防提示注入 | 未执行对应测试 |
| REQ-KNOW-010 | 连接器声明源系统、权限、同步方式、版本与健康；读取和写回分开授权 | `/knowledge` | D | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：资料/记忆样例、编辑和确认/禁用状态 | 实际源授权/同步/独立写回 | 未执行对应测试 |
| REQ-KNOW-011 | 跨 Agent、模型与专业工具复用可迁移工作事实；交接只共享获授权资料 | `/knowledge` | M | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：仅相邻能力：资料/记忆样例、编辑和确认/禁用状态；未见本条专用流程 | 跨模型授权事实交接 | 未执行对应测试 |
| REQ-KNOW-012 | 小说人物 / 世界观、品牌规范、产品约束、架构决策与运营口径使用同一来源治理 | `/knowledge` | D | `src/pages-admin.jsx: ResourcePage(knowledge); src/data.js: resources.knowledge`：资料/记忆样例、编辑和确认/禁用状态 | 专业来源版本及权限治理 | 未执行对应测试 |
| REQ-RUNTIME-001 | Agent 身份与 runtime、模型、节点解耦，同一团队可混用 CLI 与直接 API | `/agents、/models、/tools` | D | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发 | 真实runtime/模型/节点解耦 | 未执行对应测试 |
| REQ-RUNTIME-002 | 统一适配器定义启动、事件、等待、工具授权、取消、结果、部分成果、错误和用量 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 适配器全生命周期契约 | 未执行对应测试 |
| REQ-RUNTIME-003 | 发布能力清单，区分支持、受限、不支持、未经验证；调度按必要能力匹配 | `/agents、/models、/tools` | M | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：仅相邻能力：runtime名称、模型工具记录及模拟部分下发；未见本条专用流程 | 实际验证能力矩阵/调度匹配 | 未执行对应测试 |
| REQ-RUNTIME-004 | 同一请求保留平台 ID、供应商 ID、模型版本、配置版本、工具版本和关联链 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 供应商ID与配置工具版本链 | 未执行对应测试 |
| REQ-RUNTIME-005 | 提供类似 cc-switch 的模型配置档案：提供商、协议、Base URL、模型别名、参数、凭据引用、作用域 | `/agents、/models、/tools` | D | Agent引用模型版本及runtime适配声明，配置提案版本校验；原模型配置档案保留 | 真实凭据引用/参数校验/远程原子应用 | agents-templates.test.js；browser-agents-templates.js |
| REQ-RUNTIME-006 | 远程下发配置由授权管理者或策略发起，绑定目标节点 / Agent / 工作区、版本和有效期 | `/agents、/models、/tools` | D | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发 | 实际目标作用域/有效期下发 | 未执行对应测试 |
| REQ-RUNTIME-007 | 配置原子更新、保留备份、连通性检查和失败回滚；支持灰度与批量结果 | `/agents、/models、/tools` | D | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发 | 真实原子备份/连接检查/回滚 | T09 |
| REQ-RUNTIME-008 | 默认使用执行级配置 / 环境与本地代理，不覆盖用户个人全局配置；全局写入须明确授权 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 执行级配置与个人全局保护 | 未执行对应测试 |
| REQ-RUNTIME-009 | 凭据优先保存在云端秘密服务 / 安全代理，节点拿短期受限能力；必须直连时按独立策略安全分发 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 秘密服务/短期能力安全分发 | 未执行对应测试 |
| REQ-RUNTIME-010 | 新配置默认用于后续执行；运行中切换依能力和安全边界处理，必要时新建关联执行 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 在跑任务能力边界切换 | 未执行对应测试 |
| REQ-RUNTIME-011 | 模型探测、连通性测试、Token 测算标记测试用途并计费归属 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 实际探测计费归属 | 未执行对应测试 |
| REQ-RUNTIME-012 | 支持每 runtime 的版本兼容矩阵、升级告警与隔离回退，保留有差异的错误和授权语义 | `/agents、/models、/tools` | P | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发；不含本条真实生产证明 | 具体版本兼容矩阵/回退 | 未执行对应测试 |
| REQ-RUNTIME-013 | 团队工具目录管理 Skills、MCP 与专业工具版本、内容哈希、授予范围和操作 Schema | `/agents、/models、/tools` | D | `src/pages-admin.jsx: Agents / ResourcePage(models/tools); src/store.js: resourceCommand`：runtime名称、模型工具记录及模拟部分下发 | 工具哈希/授权/操作Schema | 未执行对应测试 |
| REQ-CLIENT-001 | Web 和 Tauri 客户端共用产品对象、API、权限与实时协议；目标包含 Windows、macOS、Linux、iOS、Android | `/settings、/overview、review.html` | P | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置；不含本条真实生产证明 | 五平台Tauri构建及协议共用 | 未执行对应测试 |
| REQ-CLIENT-002 | 前端业务层共用，原生能力通过明确适配层接入；Web 不依赖 Tauri API 才能工作 | `/settings、/overview、review.html` | D | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置 | 真实共享业务层/原生适配 | 未执行对应测试 |
| REQ-CLIENT-003 | 移动端支持协作、计划 / Flow 确认、审批、任务跟进、预览、批注、告警处理与远程节点操作 | `/settings、/overview、review.html` | D | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置 | 手机全部关键动作实机验收 | 未执行对应测试 |
| REQ-CLIENT-004 | 桌面端支持通知、深链接、文件选择 / 拖放与经授权本地能力，安装升级有签名与版本策略 | `/settings、/overview、review.html` | P | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置；不含本条真实生产证明 | 系统通知/深链/签名分发 | 未执行对应测试 |
| REQ-CLIENT-005 | 使用系统安全存储保护会话和凭据，支持撤销设备、退出、重新认证与身份切换 | `/settings、/overview、review.html` | P | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置；不含本条真实生产证明 | OS安全存储/撤销再认证 | 未执行对应测试 |
| REQ-CLIENT-006 | 前台使用实时连接，移动后台按 OS 能力使用推送和恢复同步；不依赖永久后台 WebSocket | `/settings、/overview、review.html` | P | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置；不含本条真实生产证明 | 推送后台恢复同步 | 未执行对应测试 |
| REQ-CLIENT-007 | 跨端同步未读、待处理和工作状态；本地草稿 / 上传重试按用户与对象隔离 | `/settings、/overview、review.html` | D | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置 | 跨端未读/上传/草稿隔离 | 未执行对应测试 |
| REQ-CLIENT-008 | 深链接打开同一对象 / 固定版本，外部链接、Web 与 App 导航遵守相同授权 | `/settings、/overview、review.html` | D | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置 | App深链及共同服务端鉴权 | 未执行对应测试 |
| REQ-CLIENT-009 | 明确平台能力矩阵、最低系统版本、WebView / 插件要求、更新与分发方式 | `/settings、/overview、review.html` | P | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置；不含本条真实生产证明 | 最低OS/WebView/插件矩阵 | 未执行对应测试 |
| REQ-CLIENT-010 | 桌面 / 手机客户端与执行 daemon 分离；移动端主要远程操作执行节点 | `/settings、/overview、review.html` | P | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置；不含本条真实生产证明 | 真机客户端与daemon分离 | 未执行对应测试 |
| REQ-CLIENT-011 | 支持国际化、键盘、读屏、触控、减少动画、响应式及长内容阅读 | `/settings、/overview、review.html` | D | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置 | 国际化/读屏/全部键盘触控 | 未执行对应测试 |
| REQ-CLIENT-012 | 缓存只作加速，明确离线范围与失效；敏感资料下载 / 缓存遵守策略 | `/settings、/overview、review.html` | D | `src/main.jsx: App; src/styles.css; src/pages-admin.jsx: Settings`：独立浏览器UI、响应式样式和设置 | 敏感缓存/下载/失效策略 | 未执行对应测试 |
| REQ-UX-001 | 工作台汇总多个项目与“与我有关”，协作区管理具体项目，会话区统一沟通与执行交互 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 各角色所有项目范围一致 | 未执行对应测试 |
| REQ-UX-002 | 管理会话右栏是授权全局摘要，执行会话右栏是当前任务预览，可展开同一规范详情 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 真实授权摘要与执行身份 | 未执行对应测试 |
| REQ-UX-003 | 列表、看板、Flow、评审布局共用工作事实；用过滤与视图状态切换 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 全部视图筛选同事实 | 未执行对应测试 |
| REQ-UX-004 | 一个对象有一个规范详情地址；会话 / 通知 / 画布中的面板是同对象的嵌入上下文 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 全部对象规范地址/返回上下文 | 未执行对应测试 |
| REQ-UX-005 | 执行交互统一到会话，保留执行身份、历史、工具权限与关联工作 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 独立执行身份/权限历史 | 未执行对应测试 |
| REQ-UX-006 | 文件、图片、文档、代码、视频、小说复用成果查看 / 版本 / 批注机制，专业编辑仍用合适工具 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 专业格式真实读取/批注 | 未执行对应测试 |
| REQ-UX-007 | 所有审批 / 告警 / 交接在统一待处理体系聚合，专业页面提供同一动作的快捷入口 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 所有待处理类型统一聚合 | 未执行对应测试 |
| REQ-UX-008 | 目标、报告、交付包、确认单等独立生命周期对象保留详情；高级能力按需展开可搜索 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 高级生命周期与搜索完整性 | 未执行对应测试 |
| REQ-UX-009 | 浏览器与 App 返回恢复筛选、阅读位置、选中版本和未提交草稿；跳转显示所属范围 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 返回恢复全部筛选/版本/阅读 | 未执行对应测试 |
| REQ-UX-010 | 客户与内部成员使用同路由权限投影，管理者与执行者使用同对象的角色视角 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 服务端角色对象字段投影 | T06、T12 |
| REQ-UX-011 | 新路由仅在独立对象、权限或编辑意图确有需要时注册；废弃路由统一迁移入口与测试 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 路由迁移策略与历史兼容 | 未执行对应测试 |
| REQ-UX-012 | 页面清单附能力去向，包括高级报告、工具、主管编排、远程终端、治理与专业工作包 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 高级能力完整业务而非仅入口 | 未执行对应测试 |
| REQ-NFR-001 | 核心新增技术不得依赖 JVM / Java 类重型常驻栈；优先复用现有 Go 后端和轻量独立进程 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 生产组件技术栈验证 | 未执行对应测试 |
| REQ-NFR-002 | “轻量”按真实占用与负载衡量，不能只依据语言名；模型推理、渲染与第三方 runtime 单独核算 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 真实资源占用及负载报告 | 未执行对应测试 |
| REQ-NFR-003 | 核心模块化单体、独立 daemon / Worker；按连接和吞吐需要增加中间件，不先拆大量服务 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 后端模块/Worker部署边界 | 未执行对应测试 |
| REQ-NFR-004 | 数据库兼容既有三方言约束，生产推荐 PostgreSQL；高级索引按部署能力启用 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 三数据库方言实测 | 未执行对应测试 |
| REQ-NFR-005 | 状态 / 审批事务、outbox、幂等、租约、重连和备份恢复作为基础保证 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 事务/outbox/幂等/恢复 | 未执行对应测试 |
| REQ-NFR-006 | 复用已有分页、授权与模块边界；搜索投影和关联目录不成为中央万能业务表 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 真实模块授权/分页边界 | 未执行对应测试 |
| REQ-NFR-007 | 大消息历史、媒体、Flow 与多项目聚合按服务端过滤、增量、分页、虚拟化和缓存处理 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 大规模服务端增量/虚拟化 | 未执行对应测试 |
| REQ-NFR-008 | 检查依赖许可证、维护状态、供应链、平台支持与升级路径，锁定具体版本 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 依赖许可供应链及版本审查 | 未执行对应测试 |
| REQ-NFR-009 | 租户、个人、项目、对象权限贯穿消息、搜索、模型、文件、终端、预览、导出与监控 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 所有出口真实租户对象隔离 | 未执行对应测试 |
| REQ-NFR-010 | 运行日志、指标和 trace 关联到工作 / 环节 / 执行，按需要开启深度采集与脱敏 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 关联trace/采集及脱敏 | 未执行对应测试 |
| REQ-NFR-011 | 云端故障、节点断网、磁盘不足、队列积压、索引失败、隧道失联均有恢复演练 | `/settings、/health、/policies` | P | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施；不含本条真实生产证明 | 真实故障注入恢复 | 未执行对应测试 |
| REQ-NFR-012 | 原型、模拟运行、适配器集成、平台端到端、生产规模测试分别记录 | `/settings、/health、/policies` | D | `src/store.js; package.json; tests/domain.test.js`：浏览器原型和本地域测试，不含生产基础设施 | 适配器/端到端/规模证据分层 | 未执行对应测试 |
| REQ-DETAIL-001 | 简单问答或一次草稿不强迫用户建组织、团队、机器、SOP；形成正式工作时必须有私人归属、负责人和必要契约。自动私人集合是推荐方案，实体复用与建立时机待冻结 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | `src/pages-work.jsx: NewWork`：默认私人归属，首次创建本地私人项目，记录负责人/要求；`Chat`可先对话 | 空白用户完整轻量旅程及私人实体政策冻结；永久消息与真实修订另验 | Q4仅证明新建后可提交；私人起步完整旅程未执行 |
| REQ-DETAIL-002 | 个人转团队 / 外部共享前预览选定消息、文件、成果版本、依赖引用、接收者与访问范围；发现未授权依赖时提示补授权或排除 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | message-sharing：文本预览、明确目标项目/频道，拒绝陈旧/撤回及租户切换，外部参数不打开执行记录 | 文件/成果版本/依赖授权共享清单与真实ACL | message-sharing.test.js；browser-message-sharing.js |
| REQ-DETAIL-003 | 动态澄清表单预填已知值和来源 / 版本，只要求影响下一步的缺项；推断与已确认信息区别展示，修改同步到关联草稿 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | M | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：仅相邻能力：静态专业成果示例、文本定位批注和新工作表单；未见本条专用流程 | 动态缺项/来源/推断标识 | 未执行对应测试 |
| REQ-DETAIL-004 | 实时预览新增批注时固定可恢复快照 / 修订与定位信息；无法稳定捕获时明确要求固定版本再批注 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：静态专业成果示例、文本定位批注和新工作表单 | 真实可恢复预览快照与定位 | 未执行对应测试 |
| REQ-DETAIL-005 | 图片 / 设计支持候选并排、局部区域标注、选择依据与指定区域修改；保留落选候选、来源、成本和新旧关系 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：静态专业成果示例、文本定位批注和新工作表单 | 区域标注/候选来源成本关系 | 未执行对应测试 |
| REQ-DETAIL-006 | 视频 / 音频反馈绑定媒体版本、时间点 / 时间区间、适用音轨 / 字幕；代理与原件保存映射 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：静态专业成果示例、文本定位批注和新工作表单 | 时间区间/音轨/代理原件映射 | 未执行对应测试 |
| REQ-DETAIL-007 | 文档 / 小说按章节或段落增量呈现，保留阅读锚点；批注引用版本与片段，人工修订记录作者和基线 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：静态专业成果示例、文本定位批注和新工作表单 | 章节增量/阅读锚点/修订作者基线 | 未执行对应测试 |
| REQ-DETAIL-008 | 报告批注关联数据时窗、来源版本、指标口径、图表 / 数据行及筛选条件 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | M | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：仅相邻能力：静态专业成果示例、文本定位批注和新工作表单；未见本条专用流程 | 数据时窗/指标/图表行定位 | 未执行对应测试 |
| REQ-DETAIL-009 | 对外 Agent 消息默认草稿；仅在明确授权范围自动发送事实性答复，范围 / 合同 / 价格等承诺须 Human 确认 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | M | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：仅相邻能力：静态专业成果示例、文本定位批注和新工作表单；未见本条专用流程 | 外发草稿/事实授权/承诺Human确认 | 未执行对应测试 |
| REQ-DETAIL-010 | 自动分派默认选择满足要求的最简单执行方式；新增主管、多候选、跨模型协作说明能力、独立性或并行收益与成本 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | M | `src/pages-collab.jsx: Chat / Artifact; src/pages-work.jsx: NewWork`：仅相邻能力：静态专业成果示例、文本定位批注和新工作表单；未见本条专用流程 | 最简分派及新增协作收益依据 | 未执行对应测试 |
| REQ-DETAIL-011 | 软件内部修复轮次默认 5 与正式质量返工达到 3 次升级分别计数、配置并展示；环境重试、质量返工、需求变更不混计 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | rework：固定失败批次/正式工作或包事件独立累计，阈值停线、有限Human续行、历史/改派不清零 | 环境重试与范围变更专属分类入口；实际运行计数接入 | rework.test.js；browser-rework.js；6类变异 |
| REQ-DETAIL-012 | 无外部接收人时 L4 标为不适用并记录原因；L2 必需内部检查与 L3 有权接受继续成立；公开发布独立授权 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | D | WorkDetail接收契约 / workCommand：内部理由明确，L2/L3后内部完成；外部待回执，当前包全部接受才完成 | 独立公开发布授权流程尚缺；内部完成不授予发布权 | completion.test.js；browser-completion.js主代理1440 PASS |
| REQ-PRO-001 | 保留 Human 工时记录：主体、日期 / 时区、时长、工作类别、项目 / 工作关联、说明、草稿 / 提交 / 审核 / 退回 / 更正历史；有权代理登记标记实际作者 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | pages-effort / saveHumanEffort / reviewHumanEffort：日期、时区、类别，评估→本人校准确认→主管审核/退回；更正与原审核值留痕 | 授权代理登记、时区容量政策完整流程 | effort.test.js；browser-effort.js |
| REQ-PRO-002 | Human 工时、Agent 墙钟运行时长、实际模型 / 工具耗时、Token 与费用分列；汇总按唯一来源与分摊口径去重 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | Human确认投入与Agent墙钟/工具/token/费用分列；未知遥测不伪造零；按来源ID去重 | 实际Usage采集/价格对账与跨项目分摊 | effort.test.js；reports.test.js；browser-effort.js |
| REQ-PRO-003 | 工时支持作为周期报告 / KR 证据引用，记录核对状态、缺失和冲突；报告确认不等同于每条工时已审核 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | ReportFacts：工时作为固定版本引用，核对来源/周期；报告提交保存未审核标记且不修改工时 | KR证据引用与核对尚缺 | reports.test.js；browser-reports.js主代理1440 PASS |
| REQ-PRO-004 | 软件需求、任务、缺陷复用 WorkItem，保留类型字段、严重度 / 优先级、复现步骤、期望 / 实际、环境、影响版本、负责人、关联提交、验证与关闭依据 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：工时/测试/缺陷/模板/连接器通用记录与状态 | 缺陷复用WorkItem/结构化环境提交/验证关闭依据 | 未执行对应测试 |
| REQ-PRO-005 | 保留迭代、里程碑、发布与工作关联；支持范围基线、目标日期、依赖、准入 / 准出条件和变更记录，专业状态迁移校验必需字段和责任 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：工时/测试/缺陷/模板/连接器通用记录与状态 | 迭代里程碑发布范围基线/准入准出/固定版本集合 | 未执行对应测试 |
| REQ-PRO-006 | 独立测试用例保存版本、要求引用、前置、输入、步骤、期望、类型 / 优先级与负责人；用例可复用但不能修改已执行快照 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | 用例修订、显式验收条款、前置/步骤/期望与固定执行快照；旧缺失映射不推断全覆盖 | 完整专业用例类型/优先级与真实运行适配验收 | qa-plans.test.js；browser-qa-plans.js |
| REQ-PRO-007 | 测试计划保存范围、用例固定版本、目标提交 / 构建、环境、分派、执行窗口、完成条件；变更追加版本并明确未执行与重验范围 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | 固定用例修订与计划/批次/报告，构建环境窗口及变更说明；五状态统计；改计划先结束当前批次 | 逐项角色分派和大规模计划/外部构建集成验收 | qa-plans.test.js；browser-qa-plans.js |
| REQ-PRO-008 | 测试执行与报告保存实际结果、证据、运行主体、工具版本、时间、日志和失败关联缺陷；区分 Human 手测、Agent 自测、平台独立验证 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | 逐项实际/证据/稳定运行身份/工具版本/日志；报告不可变，自测不能满足独立门禁 | 真实采集、可信运行回执及缺陷WorkItem统一关联 | qa-plans.test.js；qa.test.js；browser-qa-plans.js |
| REQ-PRO-009 | 软件要求确认 / 评审支持结构化意见、固定版本、分歧与变更影响；冻结验收与平台独立重跑、测试 Agent、跨模型产品验收继续保留 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | 固定验收条款/用例版本/独立身份/批次报告门禁；缺失和自测保持待验证 | 结构化要求评审分歧/变更影响、测试Agent/跨模型完整流程 | qa-plans.test.js；browser-qa-plans.js |
| REQ-PRO-010 | 云效 / Codeup / Flow / Testhub 等专业集成保留明确外部 ID、来源系统、仓库 / 提交 / 构建、测试对象和工作映射；按具体 API 能力核实导入与写回 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | D | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：工时/测试/缺陷/模板/连接器通用记录与状态 | 真实Codeup/Flow/Testhub同步去重 | 未执行对应测试 |
| REQ-PRO-011 | 工作包版本至少声明输入与必需程度、步骤 / 输入输出契约、工具能力、成果格式、专业预览与批注、客观验证器、主观评审准则 / 人员、预算与返工上限 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | M | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：仅相邻能力：工时/测试/缺陷/模板/连接器通用记录与状态；未见本条专用流程 | 各业务包实际工具/专业验收 | 未执行对应测试 |
| REQ-PRO-012 | 运营包保留指标口径 / 时窗与观察期；产品包保留研究依据 / 场景 / 原型；UI 包保留组件 / 品牌 / 交互反馈；广告包保留渠道规格 / 受众 / 素材授权 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | M | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：仅相邻能力：工时/测试/缺陷/模板/连接器通用记录与状态；未见本条专用流程 | 运营观察期/产品研究/组件交互/广告授权与效果分离 | 未执行对应测试 |
| REQ-PRO-013 | 小说包保留人物、世界观、大纲、章节及连续性；音视频包保留素材、工程、字幕、音轨、代理和审片；图片包保留候选、局部修改与导出 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | M | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：仅相邻能力：工时/测试/缺陷/模板/连接器通用记录与状态；未见本条专用流程 | 小说视频图片专业资源完整性 | 未执行对应测试 |
| REQ-PRO-014 | API、邮件、webhook、消息桥接与实验网关保留为受控来源 / 操作连接器，声明协议、权限、配额、能力、来源和健康；默认不启用写出副作用 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | M | `src/pages-admin.jsx: ResourcePage; src/data.js: resources`：仅相邻能力：工时/测试/缺陷/模板/连接器通用记录与状态；未见本条专用流程 | 输入指令隔离/独立写出权限 | 未执行对应测试 |
| REQ-OPS-001 | Runner 的 CLI 与 daemon 采用纯 Go，同一发行物提供服务管理；不建设必需的 Go + Node 常驻桥接双栈 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 纯Go发行物服务管理验证 | 未执行对应测试 |
| REQ-OPS-002 | 原生签名安装 / 升级路径必须独立可用；npm / npx 仅可选分发。安装 / 注册服务与启动任务分离，认证失败不能显示节点可执行 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 无Node原生安装签名认证 | 未执行对应测试 |
| REQ-OPS-003 | Go 直接调用模型 SDK 或 HTTP；SDK 缺功能可按官方协议补足，但协议直连只提供模型能力，Agent 工具循环、预算、权限与结果归档由平台执行层负责 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | Go直接模型API与Agent执行层 | 未执行对应测试 |
| REQ-OPS-004 | 原生 CLI / 服务协议适配与 SDK 分别登记能力，不能假定等价；结构化事件优先，终端文本仅作为明确受限适配 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 原生CLI/SDK逐项能力验证 | 未执行对应测试 |
| REQ-OPS-005 | MCP、Skills、专业工具声明来源、版本 / 哈希、依赖、授权范围、输入输出与操作能力；按工具实际协议运行，Skill 内容不能自动取得进程权限 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 真实MCP/Skill哈希/依赖/权限 | 未执行对应测试 |
| REQ-OPS-006 | 进程生命周期覆盖启动失败、事件解析失败、stdin 等待、输出阻塞、取消、子进程回收、退出码与结果对账；外部 runtime 升级与 Runner 升级分开 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 子进程/畸形输出/取消/副作用对账 | 未执行对应测试 |
| REQ-OPS-007 | 保活由 launchd / systemd 或支持的用户服务 / Windows SCM 承担；daemon 提供就绪、心跳、崩溃与诊断状态，异常重启避免快速循环占满资源 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | OS保活与退避/就绪状态 | 未执行对应测试 |
| REQ-OPS-008 | 升级前验证包、配置与协议兼容，节点进入排空、不接新任务；运行完成或到明确可接续边界才切换，失败有回滚与原因 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 实际排空升级/失败回滚 | 未执行对应测试 |
| REQ-OPS-009 | 若承诺“活跃任务不中断升级”，worker / 原生 runtime 生命周期必须独立于控制 daemon，具备持久事件 / 检查点、所有权交接与版本兼容；不具备能力的任务等待完成再升级 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 独立worker连续性/单写交接 | 未执行对应测试 |
| REQ-OPS-010 | 云端与节点滚动升级维持协议兼容窗口，先升级可兼容组件再收敛；状态 / 数据迁移有备份与回退边界 | `/machines、/models、/tools` | P | `src/pages-admin.jsx: Machines / Agents / ResourcePage(models/tools)`：安装说明与runtime/工具目录；无Go进程执行；不含本条真实生产证明 | 新旧协议窗口/迁移回退限制 | 未执行对应测试 |
| REQ-UX-013 | 左侧区域菜单、频道列表与项目实例选择在当前工作区原位切换内容，不执行路由跳转；保留侧栏位置、滚动、展开与顶部当前栏目 | `/overview、/work、/settings` | D | `src/main.jsx: App / Search; src/ui.jsx: PanelButton; src/pages-work.jsx`：共享外壳、视角/项目选择、本地导航与对象链接 | 全部侧栏滚动/展开恢复及完整键盘路径；现有 URL/history/外壳和频道原位切换已有证据 | B2、B5的375px抽屉子集 |
| REQ-ORG-011 | 租户创建、通过邀请加入和租户切换统一放在左上角 Logo 右侧；租户下管理多个项目实例并支持租户内汇总 | `/members、/invitations、/policies` | D | `src/main.jsx: App; src/store.js: createTenant / switchTenant / joinTenant`：Logo旁租户入口、本地空白租户和快照切换；无效邀请码本地拒绝 | 建项目及有效/无效邀请组合旅程；服务端邀请核验及全出口租户隔离另验 | T14、B2、B5空租户上下文子集 |
| REQ-PLAN-010 | 提供项目计划与个人任务的月 / 周排期日历，按项目范围与 Human / Agent 查看，展示跨日任务、项目截止与未排期工作；起止日期调整记录理由、作者、旧新值与版本 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | D | `src/pages-work.jsx: WorkDetail; src/pages-calendar.jsx: WorkCalendar; src/store.js: scheduleWork`：计划确认布尔值和月/周本地排期 | 排期历史作者/旧新值完整性与浏览器跨月验收 | T15、B3个人周排期保存子集 |
| REQ-FLOW-015 | 工作流提供类似 n8n 的节点编排体验：节点库、画布连线、节点参数与输入输出映射、右侧查看 / 编辑面板、复制删除、配置检查与节点调试；触发、AI、工具、人工审批、检查、条件、汇合、等待、连接与数据节点有对应配置 | `/p/launch/flow` | D | 项目与工作流模板复用Flow编辑器；AI模拟草案/手改/图检查/发布/历史只读、节点面板 | 所有节点专业配置与真实调试运行证据 | flow.test.js；agents-templates.test.js；browser-agents-templates.js |
| REQ-PREVIEW-013 | 任务实时预览支持多个可切换标签，同类可多开，Web / API / 文件 / 终端 / 文稿等按适配能力展示；资源自动提供标签、不可手动增删、显示连接状态、保留各标签交互状态，预览绑定任务、执行与权限范围 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | D | `src/task-previews.jsx: TaskPreviews / PreviewPanel / PreviewContent`：按工作及标签保存 view、输入/结果/终端历史/文件选择；含滚动记录、断线恢复、自动资源标签、展开预览和独立模拟导航历史 | 滚动恢复完整交互断言、执行ID/权限真实绑定；真实服务租约与终端回收独立验证 | B3、B4 |

## 70 个验收场景逐项证据

以下列出复合场景的原型子集与剩余生产/设计证据，未宣称整场景验收。T/Q/F/B 编号只证明对应断言；D/M/P 含义同上。

| 场景 ID | 原场景 | 原验收条件 | 状态 | 当前局部证据 | 尚缺 / 未验证 |
|---|---|---|---|---|---|
| JNY-01 | OPC 第一次提交一份待改文稿 | 不先配置组织、机器或流程；收到可用修订，原稿保留，接受者明确 | D | NewWork / Chat | 空白账户私人归属、真实修订和原稿存储 |
| JNY-02 | 同一工作由负责人、设计师、开发者和客户参与 | 各自能找到责任与待处理内容；内部状态唯一，外部投影不泄露内部资料 | D | App角色视角 / WorkDetail | 各参与者责任待办及外部字段权限 |
| JNY-03 | 文案、图片、软件使用不同专业步骤 | 共用工作、版本、反馈与接受；软件保留严格检查，主观质量由指定人判断 | D | Artifact / WorkDetail | 不同专业真实步骤、检查器与指定评审者 |
| JNY-04 | 导入半成品小说，只要求改第三章 | 从实际缺失步骤开始；不强制重走创作全流程，也不伪造前序接受记录 | M | NewWork只有通用新建 | 半成品导入、缺失步骤识别、保留前序事实 |
| JNY-05 | Agent 草稿交给人修改，再换模型继续 | 无并发覆盖；明确新基线、人工改动和未完事项；原执行与版本可追溯 | D | workCommand(takeover/resume) | 独立执行、写入所有权、新模型基线 |
| JNY-06 | 两个人同时决定，或拿旧版本批准 | 服务端识别重复与版本冲突；不能把旧审批用于新成果 | P | T08仅覆盖模型配置旧版本拒绝 | 服务端并发决定与成果版本冲突 |
| JNY-07 | 频道中提出意见、@Agent 并转为工作 | 只唤醒必要参与者；行动符合授权；重复转换不建重复任务；已读不代表接受分派 | D | `Chat / NewWork`转工作，按source.id复用已有工作；B5执行线程与消息归属子集 | Agent唤醒授权、认领语义及完整转换去重浏览器验收 |
| JNY-08 | 执行长时间运行并不断产生结果 | 用户知道当前步骤、最近有效事件、阶段成果与等待原因；不显示伪造进度 | P | Chat执行事件为样例 | 真实长任务事件、部分成果与等待原因 |
| JNY-09 | 在实时预览中批注，随后内容发生变化 | 意见固定到当时修订 / 快照；新版本能追查处理情况，不错误漂移 | D | Artifact版本与文本定位批注 | 可恢复快照、定位漂移与新版处理追踪 |
| JNY-10 | 运行中补充要求、撤销工具权限或接管 | 显示已收到 / 已应用 / 等待暂停等真实状态；后续执行符合新边界 | D | workCommand接管状态 / 工具记录 | 真实补充应用状态、即时撤权和停止竞争写 |
| JNY-11 | 断网、关闭页面、换设备、重复事件 | 执行继续；恢复正确位置和事实；消息、任务与外部动作不重复 | P | 本地草稿/offline开关 | 真实离线恢复、后台继续、跨设备幂等 |
| JNY-12 | 共享选定成果给客户，再移除其访问 | 客户只见共享范围；搜索、通知、附件与后续实时推送同步收回权限 | D | T12固定共享版本辅助函数 | 搜索/通知/附件/推送实时撤权 |
| JNY-13 | 私人资料用于工作、记忆被修正或删除 | 共享清单明确；无权 Agent 无法检索；新上下文不再使用失效内容 | D | Chat分享 / knowledge记录 | 依赖共享清单、Agent检索权限、上下文失效 |
| JNY-14 | 由通知、会话、项目分别打开同一工作与成果 | 规范对象地址一致，权限与版本一致，处理结果同步，浏览器返回可恢复来源 | D | 规范对象链接 / workPath | 所有入口权限/版本/返回来源完整验证 |
| JNY-15 | 从工作台连续处理任务、评审、工具批准和交付 | 不必进入多套重复页面；全部已登记待处理类型仍有入口与动作 | D | B1工作质量门→接受→交付→交付列表；其他类型ResourcePage状态动作 | 所有待处理类型完整连续旅程 |
| JNY-16 | 手机使用、长会话、大项目与专业成果 | 核心动作完整，输入和阅读位置不丢，按需加载；键盘与触控均可完成 | D | 响应式样式 / 移动议程 | 长内容加载/阅读输入保持/键盘触控真机 |
| JNY-17 | 预算或返工达到上限，外部动作状态不确定 | 停止不合规继续执行，保留部分成果；负责人收到可行动建议；不自动重放 | M | Cost预算输入与静态风险 | 预算/双返工阈值停止、未知副作用核实 |
| JNY-18 | 部分成果接受、部分拒绝，稍后再次交付 | 每项绑定明确版本与回执；接受部分保留，拒绝部分进入修订，不误标整包完成 | D | deliveries部分接受状态 | 逐项版本回执和已接受项保留 |
| AC-01 | 多 Human 与多 Agent 从协作频道生成计划、确认、生成 SOP、发布 | 版本与权限正确，实际运行使用已确认定义，所有步骤可追溯 | D | T04、T13局部计划/SOP隔离 | 会话生成、多参与者确认和真实运行定义 |
| AC-02 | 一个环节由两个 Agent 和一个 Human 配合执行 | 唯一人类负责人、分工与写入权明确，部分结果不误算全部完成 | M | Agents列表 / WorkDetail负责人 | 环节多人分工/写入权/部分完成 |
| AC-03 | 下游因输入不合格退回上游 | 固定退回版本与依据，受影响下游暂停，上游新版本重验后重新交接 | D | T03、T11本地重做 | 结构化上游退回、下游影响暂停/重验 |
| AC-04 | 文档、图片、软件三类业务使用同一协作内核 | 专业步骤和验收不同，工作、版本、审批、上下文与交接一致 | D | Artifact专业样例 | 真实专业工具、共同内核全部交接 |
| AC-05 | 小说 / 视频定时检测与交付前评审 | 每次检查对应当时版本，主观判断有负责人，新版不得继承过期通过结果 | D | schedules手动待执行检查 | 真实定时专业检查、快照和主观评审 |
| AC-06 | OKR 拆到成员计划，报告逐级汇总 | 进度有佐证、不重复计分，Human 报告未经本人确认不能上报 | D | goals/reports记录；T10草稿 | 多层目标计划、来源去重和汇总 |
| AC-07 | 三台不同 OS 节点安装、认证、接任务、重启、升级与卸载 | 命令与服务行为可验证，权限及执行记录保留，无失控残留进程 | P | Machines安装文字 | 三OS实际安装、服务升级卸载 |
| AC-08 | 自动分配域名预览 Web / API，节点突然崩溃或撤权 | 未授权不能访问；租约失效关闭代理；恢复扫描清理资源并发告警 | P | TaskPreviews模拟状态 | 真实域名隧道鉴权、租约崩溃回收 |
| AC-09 | 跨平台浏览文件与实时终端接管 | 工作区隔离、PTY / ConPTY、单写控制、审计、超时和撤权全部通过 | P | 模拟文件/终端 | 真实PTY/ConPTY/单写/撤权/审计 |
| AC-10 | 管理者看到多项目全局，执行者看当前任务预览 | 范围清楚，摘要下钻原对象，实时状态一致，没有另一套任务副本 | D | Dashboard / Chat / TaskPreviews | 真实多项目授权、摘要来源和实时同步 |
| AC-11 | 仓库 webhook 重复、乱序、漏发和 force push | 事件正确对账、通知去重，证据绑定提交，新版本须重验 | P | repositories模拟同步时间 | 真实webhook重复/乱序/漏发/force push |
| AC-12 | 多 Agent 并发接近预算上限 | 预留与结算防超发，自动调节可解释，必要要求和质量门未被削弱 | P | Cost本地预算 | 并发预留结算、防超发及质量不降级 |
| AC-13 | 调度器重启、DST 切换、错过周期 | 按策略补跑且有界，命令不重复产生外部副作用 | P | schedules通用记录 | DST/重启/漏跑补偿/副作用幂等 |
| AC-14 | daemon 失联、磁盘不足、API 故障与隧道泄漏 | 群内告警可认领、升级、解决与恢复，关联受影响项目 | D | health认领/恢复按钮 | 真实故障触发/通知升级/项目影响 |
| AC-15 | 服务重启、客户端重装、归档恢复、索引全量重建 | 消息 / 决定 / 审批 / 文件仍可追溯，索引不是唯一原件 | P | localStorage / exportData | 服务重启、重装、备份归档/索引恢复 |
| AC-16 | 私聊资料、外部客户、组织树和跨项目搜索 | 所有读取与计数遵守权限，撤权后缓存和实时订阅不能继续泄露 | P | T06、T14仅本地角色/租户 | 私聊组织树全出口服务端鉴权/撤权 |
| AC-17 | 自动记忆误提取并被人纠正 | 新上下文不再使用错误条目，原消息与纠正记录仍可追溯 | D | knowledge编辑/禁用 | 自动提取、上下文传播失效、原件保留 |
| AC-18 | 六类 runtime 目标与直接 API 分别接入 | 每个冻结版本通过能力、授权、事件、停止、错误、成本与成果契约测试 | P | Agent runtime名称 / 模型目录 | 六类runtime及API逐版本实际契约 |
| AC-19 | 远程批量下发模型配置，部分节点失败 | 可见期望 / 实际版本，成功与失败准确，回滚不破坏个人配置和在跑任务 | D | T08、T09模拟配置版本/部分下发 | 真实原子下发、失败回滚、个人/在跑保护 |
| AC-20 | Web、桌面与手机接续同一审批和执行 | 深链接、未读、版本和权限一致，重复点击只产生一次业务决定 | P | 共享浏览器视图 | Web/Tauri桌面手机跨端续接/幂等审批 |
| AC-21 | 必需内部验收失败，Human 或 Agent 尝试直接交付 | 所有入口拒绝放行；复核或修复产生有效通过后才能进入正式批准 | D | T01、T02、T07、Q1、Q2及B1：本地当前版本检查/证据、角色与批准交付门禁 | 所有入口完整授权、真实验证器与服务端门禁 |
| AC-22 | 任一审批 / 自动决定 / 模型调节被要求解释 | 能查主体、授权、输入、版本、理由、结果、影响与来源 | D | log及resource history | 完整主体/授权/输入/版本/理由因果审计 |
| AC-23 | 页面合并后完成高级业务 | OKR、报告、主管扩编、Heavy、工具、终端、专业检查等有明确入口，功能不丢失 | D | ResourcePage / Agents / Machines | Heavy/扩编/高级工具等完整业务，非路由存在 |
| AC-24 | 轻量部署与高负载验证 | 记录实际常驻资源、延迟、恢复和存储成本；不依赖 Java / JVM 新核心组件 | P | package.json仅浏览器依赖 | 实际生产资源/延迟/恢复/成本报告 |
| AC-25 | 外部聊天连接器接收、回复、重复回调、断网重试与平台限流 | 每个接入平台按能力矩阵验证；消息保留来源与身份映射，重试不重复，双向转发不形成回声循环，不将已接收误报为已读 | P | connectors目录及模拟连接 | 真实平台账号、回调去重/限流/回声防护 |
| AC-26 | 外部群绑定内部频道后修改共享范围、撤销权限或移除连接器 | 未授权历史、私信和附件不外泄；外部回复不能绕过审批权限；撤销后停止后续转发，已有记录按既定保留政策可追溯 | P | connectors暂停/撤销状态 | 真实转发停止、共享范围与附件再鉴权 |
| AC-27 | Centrifugo 重启、恢复窗口过期及后续可选 NATS 接入 | 当前部署从业务数据库补取缺失消息，永久记录不丢；若启用 NATS broker，明确不支持内置历史恢复；若启用 JetStream consumer，验证重投去重和乱序处理，不宣称二者能力等价 | P | 无Centrifugo/NATS客户端 | 重启补拉/业务DB原件/可选NATS能力边界 |
| EXT-01 | 新用户不建业务项目，先问答，再提交正式改稿 | 问答可进行且消息保留；正式工作有私人归属、要求和负责人；不强迫配机器 / 组织 | D | Chat / NewWork已有私人默认归属及首次创建私人项目；Q4新工作无需刷新即可提交 | 完整空白用户轻量问答→改稿旅程；永久消息与真实改稿另验 |
| EXT-02 | 选两条私聊与一版成果分享给团队，其中一个附件无权分享 | 显示确切共享清单与权限缺口，拒绝未授权部分；其他历史仍私有 | M | 单条Chat分享弹窗 | 多消息+固定成果+依赖附件逐项授权清单 |
| EXT-03 | 对持续变化的图片 / 文稿批注，随后生成新版 | 固定原快照、原定位和作者；新版本可追查处理情况，定位失败如实显示 | D | Artifact评论version/location | 真实快照/区域定位/作者/新版处理状态 |
| EXT-04 | 视频代理剪辑后处理旧时间段意见；报表数据刷新后处理旧图表意见 | 原媒体 / 数据快照与来源可还原；不把旧定位直接套新版 | M | 视频静态位置 / reports字段 | 代理原件映射、数据快照和旧定位不可漂移 |
| EXT-05 | 外部客户问进度后提出价格和交付日期承诺 | 事实回复依授权发送；承诺进入 Human 草稿确认；对外材料重新鉴权 | M | connectors记录 / Chat | 事实自动回复授权与价格日期承诺Human草稿 |
| EXT-06 | 单 Agent 任务需要独立验证，同时到达内部修复 / 正式返工阈值 | 必需检查不减少，两个计数分别升级；换 Agent 不清零 | D | 固定失败批次/正式事件两计数，Human有限继续与改派历史；rework和QA专项 | 真实独立执行/其他专业配置及完整场景集成 |
| EXT-07 | 个人成果无客户，经内部检查和本人接受后准备公开发布 | L4 有依据标不适用；发布仍需要单独授权，不继承默认公开权 | M | workCommand交付 | L4不适用理由及独立公开发布授权 |
| EXT-08 | Human 工时退回更正，三个 Agent 并行；报告跨两项目汇总 | 原记录保留，实际作者正确，人类与 Agent 时间分列，不重复汇总或自动算薪酬 | D | worklogs/report通用记录 | 代理实际作者、Agent并行时长分列去重 |
| EXT-09 | 测试计划执行一半后用例改版，并修复一个缺陷 | 已执行结果保留原版本；新计划明确重验；失败 / 未执行分列，缺陷需验证才能关闭 | D | tests/testplans/defects状态 | 计划用例版本锁定/失败未执行分列/关闭验证 |
| EXT-10 | 同一 Codeup / Flow / Testhub 事件重复导入，随后仓库改提交 | 来源映射去重、权限有效，证据固定旧提交；新提交重验，不将外部 CI 当平台独立验证 | P | repositories/connector样例 | 真实Codeup/Flow/Testhub去重和旧提交证据 |
| EXT-11 | 小说、视频、运营、设计分别使用工作包并跨包交接 | 有专业输入、实际工具、反馈和验收；半成品可接续，数据效果不由文件生成替代 | M | templates通用记录 / Artifact | 四专业包真实工具/半成品跨包/数据效果验收 |
| EXT-12 | 邮件 / API 来源带操作指令，导入后要求对外写回 | 输入作为材料且保留来源；未授权写回被阻止，实验通道不豁免门禁 | M | connectors通用记录 | 材料指令隔离与独立写回授权 |
| EXT-13 | 无 Node 的三种 OS 原生安装 Runner，调用直接模型 API，再选择依赖 Node 的 MCP | 核心安装 / 执行成功；外部依赖单独提示与授权，不偷装第二个常驻桥接服务 | P | Machines安装说明 | 三OS无Node安装、真实模型/MCP授权依赖 |
| EXT-14 | CLI runtime 输出畸形事件、阻塞、崩溃或取消时残留子进程 | daemon 仍健康；错误归属准确、子进程受控、部分成果保留，未知副作用待核实 | P | 无runtime进程 | 畸形输出/阻塞/崩溃/取消子进程故障注入 |
| EXT-15 | 长任务运行时升级 Runner，分别测试排空和具备连续能力的 worker | 排空不杀任务；连续模式控制进程切换后单一所有权、日志连续；失败可回滚或清楚阻塞 | P | 节点排空文字状态 | 真实升级任务连续/单写/日志/失败回滚 |
| EXT-16 | 云端滚动升级时新旧节点并存，旧版本不兼容新的配置 | 按矩阵拒绝不兼容操作，实际配置准确；不可逆迁移标明回滚限制 | P | T09仅本地模拟下发 | 新旧节点兼容矩阵/不可逆迁移回退边界 |
| EXT-17 | 上游返工与下游部分接收并发，负责人离职 | 保留接受事实，受影响输出标失效 / 待重验；安全撤权立即生效，接管待办明确 | M | T11仅单工作退回 | 上游下游并发部分接受/离职撤权交接 |
| EXT-18 | 频道撤权后索引尚未刷新，旧 App 离线草稿恢复发送 | 搜索、计数和补拉不泄密；旧高风险动作不自动重放；草稿重新鉴权 | P | 本地offline开关与草稿 | 真实索引撤权/旧App离线重鉴权/不重放 |
| EXT-19 | 两名评审者从会话和画布同时批准，其中一个绑定旧成果版 | 仅有效版本决定生效；界面说明冲突，所有视图同步同一记录 | P | T08模型版本拒绝 | 两评审入口服务端并发与成果版本冲突 |
| EXT-20 | 冷归档恢复后执行合法秘密清除，相关记忆与索引仍在 | 原件按政策恢复；治理流程明确保全例外 / 清除范围；后续上下文不再用失效正文 | P | localStorage和knowledge禁用 | 冷归档、法律治理、清除范围、上下文失效 |
| EXT-21 | 从工作台侧栏依次切换我的工作、OKR、报告，再在会话左栏切换频道 | 无 URL / history 跳转、无顶部栏目误切、无外壳重挂载；菜单与选中状态稳定，消息草稿保留 | D | B2验证URL/history/外壳/频道原位切换；B5在375px验证抽屉关闭且URL/history不变 | 指定我的工作→OKR→报告连续组合、频道草稿和侧栏滚动/展开完整恢复 |
| EXT-22 | 在 Logo 右侧创建租户，建立项目实例，切回原租户，再用有效 / 无效邀请加入另一租户 | 每个租户只显示自身资料与实例；汇总不跨租户，切回恢复原资料，无效邀请无副作用，加入权限由邀请与服务端核验 | D | T14租户快照；B2 Logo入口创建空白租户/切回恢复；B5空租户不继承项目上下文 | 创建项目再切换、有效/无效邀请完整组合与服务端授权 |
| EXT-23 | 查看某项目月历，切周历与个人排期，调整任务起止日期，再在详情查看并移回未排期 | 同一任务同步更新，跨日和跨月边界正确，项目截止可见，未排期不丢失；错误日期与过期更新不落库，变更历史可追溯，原内部质量门不受日期编辑绕过 | D | T15日期/版本/历史；B3个人周排期保存及共享工作状态 | 跨月边界/项目截止/清空的完整浏览器场景、历史作者旧新值 |
| EXT-24 | 打开实例工作流，添加节点、编辑参数、复制、删除、连接、检查及发布 | 右侧显示当前节点对应配置与输入输出，错误定位到节点；未连接节点阻止发布；旧执行版本不被覆盖；调试示例不产生正式验收记录 | D | T13、T16、F1、F2局部定义/门禁测试；B3节点编辑/复制/删除/调试/拒绝未连接发布 | 全部节点类型/实际画布连线/成功发布与旧版本回看组合旅程；真实运行另验 |
| EXT-25 | 在任务执行会话中同时打开两个 Web、一个 API、文件和终端预览，切换、断线、恢复与资源撤销 | 每个标签状态独立且当前任务范围明确；资源清单决定标签，无手动加减；资源撤销后回退到剩余资源；不会访问其他任务内容，资源释放和断线回收有独立证据 | D | B4在1440px/375px验证多Web/API/文件/终端/文稿、切换任务重挂载、断线重连、系统标签与键盘焦点 | 滚动位置恢复断言、独立执行/权限服务绑定；实际资源释放和断线回收 |

场景统计：D 35、M 10、P 25；70 个复合场景没有整体生产验收结论，已验证原型子集保留各自证据。

## 后续证据门槛

1. 补齐 M 的可审查交互设计和状态契约，D 按每行缺口补齐行为；在相关专用流程不存在时，不用更多通用字段替代。
2. 逐场景运行浏览器交互，记录实际断言/环境/失败与修复；导航遍历只证明可打开，不证明需求完成。
3. P 单独通过后端集成、真实账号、原生系统、故障与规模测试提供证据，不能混入原型通过数。
4. 修复后重新进行完整评审；两轮无问题须有两次实际逐项复查结果，当前表不提供这一证明。

## 主代理增量证据（2026-10-13，未构成完整验收）

- 本轮亲自运行领域/工作流测试：35 pass、414 assertions；构建PASS。这一结果早于正在修改的报告核对与接收契约，不能当作其验收。
- `browser-previews.js` 在1440与375通过，包括实际终端滚动恢复及选中标签完整可见；手机执行预览弹层axe：0 violations/0 incomplete。
- `browser-receipts.js` 主代理复跑通过：固定逐项清单、不可通用编辑的交付包、部分接收、结构化绑定版本反馈与历史。
- `browser-controls.js` 主代理复跑通过：12主题亮暗组合的指定控件对比度、自定义滚动条、无原生校验弹窗、创建/加入租户关闭焦点返回。
- 以上仍是具体断言子集；本表M/D/P分类及未完成闭环不因测试数量增加自动升级。连续完整无新增问题轮数仍为0。

最新主代理整合：51 tests /484 assertions、build PASS；静态构建桌面11套browser脚本PASS；375px previews/collab/completion/reports PASS。完成契约和报告引用6行从M转为D，仍明确列出公开发布、OKR/计划/组织汇总等剩余范围。整合测试不改变两轮完整审查尚未完成的结论。

当前续审主代理整合快照：164 tests / 1407 assertions与build PASS；完整冻结浏览器26套×桌面/手机及真实键盘/主题门禁仍在执行。新增缺陷已复现并修正，但完整干净轮数仍0；规则矩阵见[GUIDELINES_REVIEW](GUIDELINES_REVIEW.md)。

最终本轮冻结构建：主代理26套浏览器脚本在1440/375全部PASS（52次），真实键盘/12主题手机导航回归PASS但Pink浅色渐变1项自动判定不完整；预览调宽/移除底部动作已落实。源码hash在两套矩阵期间一致。仍无完整332REQ/70场景验收结论。


## 2026-10-14 用户补充：目标共同维护与非研发岗位

以下为新增需求登记，未据此宣称原型已通过；四象限轴等待用户确认。

| 编号 | 新增要求 | 状态 | 尚需验收 |
|---|---|---|---|
| REQ-OKR-009 | 区分组织、团队、项目阶段、Human 与 Agent 目标；一个项目允许多个并行或先后阶段目标。归属、责任人、协作者与对齐关系分别记录，不能用负责人类型代替目标类型 | M | 项目成员及获授权的 Agent 能查看本项目阶段目标；不因此暴露其他租户、未授权项目或个人私密资料；尚无本次新增要求的完整执行证据 |
| REQ-OKR-010 | 各归属支持周、月、季、年周期；阶段目标可按明确起止日期跨自然周期，阶段与周期是不同维度；保留全部历期版本及结转关系 | M | 不能以主体类型禁止合理周期；切换周期可查看历史，结转生成关联的新目标而不改写旧期结果；尚无本次新增要求的完整执行证据 |
| REQ-OKR-011 | 上级按组织管理范围查看下级 OKR；下级 OKR 由本人、指定上级与获授权 Agent 共同制定维护；项目目标由负责人及协作 Agent 维护，Agent 可以起草自身目标 | M | 查看、建议、修改草案、批准分别鉴权；组织树上级不自动获得跨项目权限；Agent 不自行批准自身承诺；尚无本次新增要求的完整执行证据 |
| REQ-OKR-012 | 目标调整必须声明理由，如请假、临时任务、依赖变化或原假设失效；保存调整前后定义、提出者、理由、时间、影响及审核决定，批准绑定固定修订 | M | 修改目标值、期限、范围或负责人不能覆盖历史；待批准修订与当前生效基线分开呈现，旧审批不可复用；尚无本次新增要求的完整执行证据 |
| REQ-OKR-013 | 内置可版本化的 O/KR 写作指导：O 描述要改变的结果和受益对象；KR 说明可核验结果、基线、目标、期限、证据与上下游价值；活动清单与结果指标区分 | M | 提供合格与不合格示例；有价值的研究、风险降低和基础建设可用事先约定的证据评审，不强迫虚构收入或数值；尚无本次新增要求的完整执行证据 |
| REQ-OKR-014 | 正式发布前 Agent 必须校验目标合理性、可实现性、有效性、项目或下游贡献、重复投入及资源匹配，并给出逐项依据；Human 按既有责任审批 | M | 结构校验不冒充价值审查；审查固定规则和目标版本，定义变更使旧审查失效；失败或未运行不能标为通过，本地模拟须明确标识；尚无本次新增要求的完整执行证据 |
| REQ-OKR-015 | 共同维护流程覆盖对齐起草、讨论修订、Agent 校验、Human 确认、周期 check-in、带理由变更、期末复核和结转；进度、信心、风险与求助分开记录 | M | 本人和上级可查看建议、分歧和处理记录；check-in 更新事实不暗改目标，未观测结果保持未知；尚无本次新增要求的完整执行证据 |
| REQ-OKR-016 | 组织、团队、项目等非个人 OKR 提供四象限视图，与列表和目标详情共用同一数据及筛选；个人和 Agent 目标保留独立视角 | M | 四象限轴与分界口径待用户确认，不擅自等同重要/紧急；缺数据不被默认为低价值或已达成，移动端可完整查看；尚无本次新增要求的完整执行证据 |
| REQ-PRO-015 | 非研发岗位提供可编辑的职责/工作包起点，覆盖运营、行政、HR、财务支持、研究与管理支持，声明服务对象、输入、交付、下游接收者、验收证据与 Human 责任 | M | 新手从业务问题生成建议并由负责人确认；复用统一工作流，不只换模板标题；遵循 P6.4；尚无本次新增要求的完整执行证据 |
| REQ-PRO-016 | 分别记录成果质量、下游采用和观察期业务效果；支持文档、表格、配置及服务完成证据；定量和定性评价均需事先约定依据 | M | 方案接受不自动完成效果 KR；未知保持未知，负面研究结果可以被接受；不得按文档字数、Agent token 或 Human 在线时长替代价值；尚无本次新增要求的完整执行证据 |
| REQ-PRO-017 | 区分日常保障、改善目标与探索研究；评价保留来源、成本、观察窗口、归因限制和多人贡献，复盘可形成后续工作 | M | 行政保障可按服务质量评价；探索失败不自动判定无效劳动；不重复计功，不自动生成薪酬绩效结论；敏感材料不会随目标公开；尚无本次新增要求的完整执行证据 |
