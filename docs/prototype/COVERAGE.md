# PRD → 原型覆盖索引

本表列出全部 **332 条需求**的关联审阅入口，保证需求有去向。**入口映射不等于逐条功能已实现或验收通过**：同一路由可能只提供该需求的配置/状态展示，底层能力仍按 PRD 实现。不得将本表的行数作为上线完成度。

## 模块与交互深度

| 需求族 | 审阅入口 | 原型提供 / 边界 |
|---|---|---|
| REQ-CORE | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` | 工作生成、计划/SOP确认、门禁、交付与退回 |
| REQ-ORG | `/members、/invitations、/policies` | 组织结构、成员与邀请、权限策略样例 |
| REQ-OKR | `/goals、/reports` | 目标口径、进度、报告关联与审核 |
| REQ-PLAN | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` | 计划确认、项目截止、个人月/周排期 |
| REQ-FLOW | `/p/launch/flow` | 节点库、参数/输入输出/调试、草案连线与发布校验 |
| REQ-TEAM | `/agents、/members` | Human/Agent成员、来源、生命周期 |
| REQ-CHAT | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` | 频道、私信、线程、草稿、转工作、桥接配置 |
| REQ-CLOUD | `/overview、/governance/audit` | 汇总状态与活动；云端同步需后端 |
| REQ-EXEC | `/chat?execution_id=W-104、/p/launch/work-items/W-104` | 模拟执行记录、接管、提交与失败处理 |
| REQ-NODE | `/machines、/machines/n1` | 安装指引、状态与排空；实际进程/升级需原生工程 |
| REQ-PREVIEW | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` | 任务多标签预览、失联恢复、租约与释放的模拟 |
| REQ-REMOTE | `/machines/n1?tab=files、/machines/n1?tab=terminal` | 文件与模拟终端；不是远程文件协议或PTY |
| REQ-REPO | `/repositories` | 仓库事件与同步状态、固定提交 |
| REQ-COST | `/cost、/models` | 用量、预算与调节策略；真实计费需供应商对账 |
| REQ-SCHED | `/schedules` | 定时配置与手动模拟触发草稿 |
| REQ-QA | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` | 测试证据、质量门与返工 |
| REQ-APP | `/approvals、/deliveries、/policies` | 版本审批、Human责任与交付回执 |
| REQ-REPORT | `/reports、/worklogs` | 报告草稿、核对与人类确认 |
| REQ-WATCH | `/overview、/health、/schedules` | 风险、告警与跟进；自动监督模型需执行层 |
| REQ-HEALTH | `/health、/machines` | 告警认领/恢复、节点健康样例 |
| REQ-DATA | `/governance/audit、/knowledge` | 活动、记录与资料；真实永久归档/恢复需后端 |
| REQ-KNOW | `/knowledge` | 资料/记忆候选与审核；检索和向量处理需后端 |
| REQ-RUNTIME | `/agents、/models、/tools` | runtime、模型配置与工具目录；适配器需真实验证 |
| REQ-CLIENT | `/settings、/overview、review.html` | 共享响应式界面；原生客户端能力待真机验收 |
| REQ-UX | `/overview、/work、/settings` | 统一导航、搜索、主题、角色、异常状态 |
| REQ-NFR | `/settings、/health、/policies` | 边界与策略说明；性能/安全/可靠性不是界面验收 |
| REQ-DETAIL | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` | 轻量起步、共享、专业反馈与版本引用样例 |
| REQ-PRO | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` | 专业业务模型和复用入口；完整专业制作链需后续工程 |
| REQ-OPS | `/machines、/models、/tools` | 运维/适配能力展示；Go进程、协议、保活升级不在浏览器执行 |

## 逐项定位

每行保留原要求文字以便对照；验收条件与冲突取舍仍以 [PRD_FINAL](../PRD_FINAL.md) 为准。生产能力特别是权限、数据、原生运行、外部集成和性能必须另行取得实际证据。

| ID | 要求 | 关联审阅入口 |
|---|---|---|
| REQ-CORE-001 | 从自然语言、文件、已有项目或频道讨论发起工作；先识别目标、接收者、结果、范围和约束 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-002 | 计划与 SOP 分别有草稿、确认、版本和责任人；计划定义要做什么，SOP 定义如何协作执行与交接 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-003 | 工作、子任务、Flow 环节、执行、成果、会话相互关联；工作 / 任务沿用同一工作项体系 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-004 | 每个正式运行环节由一个明确的人类负责人负责，支持多 Human、多 Agent 共同执行 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-005 | 环节完成包含执行产物、内部验收和下游接收；最终环节由最终接受者承接 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-006 | 退回必须绑定输入 / 输出版本、未满足条款、期望和归属环节；支持本环节重做与退回上游 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-007 | 上游返工要计算受影响下游；暂停依赖旧结果的步骤，保留无关步骤，重新验证失效成果 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-008 | 工作状态区分待开始、进行中、等待、待验收、待交接、已完成、取消；工程映射统一制定 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-009 | 简单任务允许单环节 Flow，复用团队已确认 SOP；具体计划和执行范围仍须符合确认规则 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-CORE-010 | 最终交付必须满足当前要求和所有必需环节门禁，责任人确认指定版本 | `/chat?channel_id=launch、/work、/p/launch/work-items/W-104` |
| REQ-ORG-001 | 支持组织 → 部门 / 团队 → 子团队的组织树，维护负责人、成员、上下级与有效期 | `/members、/invitations、/policies` |
| REQ-ORG-002 | 组织管理关系与项目协作关系分离；成员可跨部门加入多个项目 | `/members、/invitations、/policies` |
| REQ-ORG-003 | Human 通过电子邮件或已有账号邀请加入组织 / 项目，邀请含角色、范围、有效期与撤销 | `/members、/invitations、/policies` |
| REQ-ORG-004 | 区分内部成员与外部成员；外部账号使用同应用和共享对象投影 | `/members、/invitations、/policies` |
| REQ-ORG-005 | Agent 是可分派、可 @、可审计的服务账号，标明人类负责人、角色与来源 | `/members、/invitations、/policies` |
| REQ-ORG-006 | 支持混编、AI 主执行和人工主执行模式；OPC 可采用一人多 Agent | `/members、/invitations、/policies` |
| REQ-ORG-007 | 基于组织、项目、环节、对象与动作授权；管理树位置不自动授予私人内容访问 | `/members、/invitations、/policies` |
| REQ-ORG-008 | 离职、退役、调岗、邀请撤回触发任务交接和凭据 / 会话撤销 | `/members、/invitations、/policies` |
| REQ-ORG-009 | Agent 创建、主管扩编、收编、退役保留编制、预算、期限、父子关系与审批 | `/members、/invitations、/policies` |
| REQ-ORG-010 | 复用现有租户、身份、实体和成员模型；组织树不得隐式打通租户隔离 | `/members、/invitations、/policies` |
| REQ-OKR-001 | 支持组织、团队、项目、Human、Agent 的 Objective 与 Key Result，以及年 / 季 / 月等周期 | `/goals、/reports` |
| REQ-OKR-002 | 目标自上而下拆解，支持跨项目贡献与多对多对齐；避免同一成果重复计分 | `/goals、/reports` |
| REQ-OKR-003 | KR 定义基线、目标值、单位、方向、计算方式、数据来源、更新时间与证据要求 | `/goals、/reports` |
| REQ-OKR-004 | 支持 AI 起草、成员提交、主管审核、退回修改、发布、变更和期末复核 | `/goals、/reports` |
| REQ-OKR-005 | Agent 可更新授权范围内的事实数据与进展草案，不自行确认自己的 OKR 或人类承诺 | `/goals、/reports` |
| REQ-OKR-006 | 自动关联计划、任务、交付、报告；手工进度必须有说明和可选佐证 | `/goals、/reports` |
| REQ-OKR-007 | 识别偏离、无计划支撑、数据过期、无负责人和临期风险，触发提醒与复盘 | `/goals、/reports` |
| REQ-OKR-008 | 支持期末自评、上级审核与结转；业务目标达成和交付物接受分开记录 | `/goals、/reports` |
| REQ-PLAN-001 | Human 与 Agent 在会话中生成项目、阶段与个人计划，关联目标、要求、期限和已有资源 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-002 | 支持日、周、月、季、年计划及里程碑拆解，关联预计投入、预算与成果 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-003 | 计划任务支持多执行者但有唯一结果负责人；指定接收方和完成标准 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-004 | 计划先确认再进入正式 SOP / 派发；已有授权的模板可简化重复确认 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-005 | 系统按实际执行、成果、依赖和验收维护进展，Human 可补充事实并注明来源 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-006 | 识别逾期、长期无进展、资源冲突、上游延期和等待过久；支持提醒、催办、升级 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-007 | 重规划给出范围、时间、成本、受影响环节与人员的差异，经所需确认后生效 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-008 | 支持计划取消、暂停、结转、替代和版本比较；保留原承诺和未完成原因 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-PLAN-009 | 进度汇总按明确权重和事实计算，阻塞 / 不确定单列；能定位到具体工作 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-FLOW-001 | 使用 React Flow 展示与编辑 SOP；支持拖动、缩放、平移、自动布局、框选、搜索定位和小地图 | `/p/launch/flow` |
| REQ-FLOW-002 | 支持无限画布工作空间、分组、注释、子流程与折叠；可见区域与子流程按需加载 | `/p/launch/flow` |
| REQ-FLOW-003 | AI 可由已确认计划和业务包生成 Flow 草案；Human 与 Agent 可协作修改并比较差异 | `/p/launch/flow` |
| REQ-FLOW-004 | WorkflowDef 是唯一流程定义；画布、列表、JSON、AI 草案与运行实例引用同一版本 | `/p/launch/flow` |
| REQ-FLOW-005 | 节点支持制作、检查、人工审批、条件分支、并行、汇合、等待、交接与有界循环 | `/p/launch/flow` |
| REQ-FLOW-006 | 边定义依赖、输入 / 输出映射、版本、可选条件和交接要求；检查缺输入、不可达和循环错误 | `/p/launch/flow` |
| REQ-FLOW-007 | 每个业务环节绑定 Human 负责的小团队，默认支持 AI 执行，可有多 Agent、多 Human；人工主执行同样有效 | `/p/launch/flow` |
| REQ-FLOW-008 | 支持顺序、并行、候选比较、局部返工、上游退回、人工接管和中途接入已有成果 | `/p/launch/flow` |
| REQ-FLOW-009 | 运行实例锁定 Flow / 工作包版本；动态改流程以补丁展示影响，经过相应审批 | `/p/launch/flow` |
| REQ-FLOW-010 | 循环有次数、时间、成本与退出条件；超限必须升级，不可无限自动重做 | `/p/launch/flow` |
| REQ-FLOW-011 | 支持多人编辑、乐观并发或锁定编辑，防止覆盖；AI 补丁和人工编辑同样校验版本 | `/p/launch/flow` |
| REQ-FLOW-012 | 支持模板保存、版本发布、回滚、导入导出、团队规则叠加和跨业务组合 | `/p/launch/flow` |
| REQ-FLOW-013 | 画布中可下钻环节详情、执行、审批、成果与日志；复用规范对象详情 | `/p/launch/flow` |
| REQ-FLOW-014 | 提供列表和键盘可操作替代；移动端可查看、确认、批注与执行关键控制 | `/p/launch/flow` |
| REQ-TEAM-001 | 每个环节配置 Human 负责人、协调者、多个执行者、检查者和下游接收者 | `/agents、/members` |
| REQ-TEAM-002 | 支持任务拆分、共同执行、接力、候选并行与成果合并；定义 ALL / ANY / 指定汇合等完成条件 | `/agents、/members` |
| REQ-TEAM-003 | 人与 Agent 接受分派后明确认领，防重复领取；可拒绝接手并说明原因 | `/agents、/members` |
| REQ-TEAM-004 | 主管 Agent 根据能力、权限、负载和成本提出分派；在已授自动策略内执行 | `/agents、/members` |
| REQ-TEAM-005 | 多写入者采用独立工作区 / 分支或明确写入所有权，合并前校验基线 | `/agents、/members` |
| REQ-TEAM-006 | 接管时停止竞争写入，保存部分成果与外部动作状态；继续时重新建立基线 | `/agents、/members` |
| REQ-TEAM-007 | 下游接收固定成果包，可接受、拒收或逐项接收；拒收引用具体不满足项 | `/agents、/members` |
| REQ-TEAM-008 | 上游返工形成新的执行 / 成果版本；重新交接前重验受影响条件 | `/agents、/members` |
| REQ-TEAM-009 | 跨环节分歧按指定 Human 决定者升级，涉及需求变化另走变更审批 | `/agents、/members` |
| REQ-TEAM-010 | 多模型比较保留各候选、证据与成本，合成后仍走正式验收 | `/agents、/members` |
| REQ-TEAM-011 | 交接包包含目标、计划、成果版本、决定、未验证项、未决动作和剩余预算 | `/agents、/members` |
| REQ-CHAT-001 | 提供类似 Slack 的组织 / 项目 / 环节频道、共享频道、群聊、Human↔Human、Human↔Agent 与授权 Agent 协作 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-002 | 支持线程、@提及、回复、附件、引用、反应、置顶、未读、搜索和通知偏好 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-003 | 支持文本、图片、文件、音视频资料；语音输入 / 转写标明来源与转写状态 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-004 | 会话中生成并确认计划、任务、SOP、修改与审批，关联真实对象和固定版本 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-005 | 明确唤醒对象、触发原因、回复链和循环上限，系统事件默认不广播唤醒全部 Agent | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-006 | 永久保存消息、编辑版本、引用和必要附件；撤回展示墓碑，历史依权限与审计政策读取 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-007 | 人类消息、Agent 消息、系统事件和代登记行为明确标识；显示发送、落库、失败与重试状态 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-008 | 协作事件和工具输出按步骤折叠，关键成果、阻塞和待审批持续可见 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-009 | 私聊、共享历史、附件、搜索与摘要统一鉴权；托管 Agent 会话显示管理员 / 负责人可见政策 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-010 | 新增外部成员前明确其可见历史与附件，优先创建明确共享范围 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-011 | 会话在线状态、正在输入、已读与任务接手是不同信号 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-012 | 会话引用可入知识 / 记忆，保留来源和权限；普通讨论不自动升级为团队正式结论 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-013 | 支持断线补拉、游标、稳定排序、跨端未读同步，长历史按锚点增量加载 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-014 | 明确同一事件在频道、待办、通知与右栏的归属，处理结果共用一份事实 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-015 | 每个连接器声明单向通知、双向消息、群 / 私聊、线程、附件、编辑撤回、回执及历史导入的实际能力 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-016 | 外部工作区、频道、用户与内部租户、会话、成员建立显式授权映射 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-017 | 外部事件验签、幂等入库；保存来源平台、账号、会话、消息 ID、方向和回复映射 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-018 | 外发有投递记录、限流、重试与失败状态；入站正文和必要附件按授权范围归档 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CHAT-019 | 项目管理员控制绑定、转发方向、共享范围、暂停和撤销；内部消息对外分享重新校验权限 | `/chat?channel_id=launch、/chat?channel_id=private、/connectors` |
| REQ-CLOUD-001 | 云端保存组织、项目、计划、Flow、任务、执行、成果、消息、审批、费用与监控的权威状态 | `/overview、/governance/audit` |
| REQ-CLOUD-002 | 同一工作台汇总多个授权项目，展示目标、里程碑、Flow 进度、负载、阻塞、预算、告警和待审批 | `/overview、/governance/audit` |
| REQ-CLOUD-003 | 管理者按组织 / 部门 / 团队 / 项目 / 时间查看，执行者默认“与我有关” | `/overview、/governance/audit` |
| REQ-CLOUD-004 | 显示状态来源、最近上报、已确认版本和新鲜度，区分在线、离线、失联与未知 | `/overview、/governance/audit` |
| REQ-CLOUD-005 | 通过服务端授权订阅实时更新；客户端按版本与事件序号处理乱序和重连 | `/overview、/governance/audit` |
| REQ-CLOUD-006 | 节点离线时保留已知状态和待核实动作，未授权离线副作用不得继续执行 | `/overview、/governance/audit` |
| REQ-CLOUD-007 | 云端命令持久化、带期限、目标版本和幂等键；执行节点报告收到、开始与结果 | `/overview、/governance/audit` |
| REQ-CLOUD-008 | 支持项目总览与跨项目大盘共存；全局大盘可进入管理协作上下文 | `/overview、/governance/audit` |
| REQ-CLOUD-009 | 云端重启 / 故障切换后恢复持久工作、调度与审批；分区时选择一致性安全边界 | `/overview、/governance/audit` |
| REQ-EXEC-001 | 一次执行绑定工作、环节、参与者、输入版本、上下文快照、runtime / 模型、节点和预算 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-002 | 执行会话支持多轮对话、工具记录、输入、附件、工具授权、暂停、停止、继续和接管 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-003 | 反馈来自真实事件：排队、启动、阶段进度、输出、等待、心跳、错误和结果 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-004 | 普通运行补充在安全边界应用，区分已收到、待应用、已应用与无法应用 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-005 | 同一执行产生的部分成果及时登记，工作稿、提交版、接受版明确区分 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-006 | 关闭页面、换设备不影响后台执行；取消是显式命令并有完成回执 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-007 | runtime 支持时原生恢复；不支持时用交接包新建关联执行，明确恢复方式 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-008 | 失败分为环境故障、质量问题、缺资料、权限拒绝和范围变化 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-009 | 未启动的安全操作可有限重试；已启动且副作用不确定必须核实或补偿 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-010 | 记录进程退出、实际成果、检查与业务完成的不同状态 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-011 | 执行目录、工具和网络按任务隔离，只有所需临时凭据 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-EXEC-012 | 支持人工观察执行与专业工具接续，人类制作的成果也可进入相同验证闭环 | `/chat?execution_id=W-104、/p/launch/work-items/W-104` |
| REQ-NODE-001 | 执行器分布在多个服务器与个人电脑，支持 macOS、Linux、Windows，登记节点身份与负责人 | `/machines、/machines/n1` |
| REQ-NODE-002 | 提供单命令安装 daemon / CLI / 系统服务，安装可重复执行，包与二进制有版本和完整性验证 | `/machines、/machines/n1` |
| REQ-NODE-003 | 提供 install、uninstall、start、stop、restart、status、logs、doctor、update、auth login/logout | `/machines、/machines/n1` |
| REQ-NODE-004 | macOS 使用 launchd、Linux 使用 systemd 或受支持的用户服务、Windows 使用 SCM 服务 | `/machines、/machines/n1` |
| REQ-NODE-005 | 支持令牌登录与设备码登录；首次注册凭据换取节点专属、可轮换和撤销凭据 | `/machines、/machines/n1` |
| REQ-NODE-006 | CLI 支持自动化安全输入令牌，优先标准输入 / 系统凭据存储；兼容参数模式需避免回显与日志收集 | `/machines、/machines/n1` |
| REQ-NODE-007 | 节点主动向云端建立出站连接，提供心跳、能力发现、运行槽、活动执行与版本 | `/machines、/machines/n1` |
| REQ-NODE-008 | 支持任务租约、排空、维护、停机、失联重连与吊销；同一工作目录限制竞争写入 | `/machines、/machines/n1` |
| REQ-NODE-009 | 节点支持安全自更新和兼容检查，运行中任务先排空或明确接续；失败可回滚 | `/machines、/machines/n1` |
| REQ-NODE-010 | 支持受控服务器 / 集群池，按任务隔离目录或临时环境；资源配额与清理策略可配置 | `/machines、/machines/n1` |
| REQ-NODE-011 | 节点离线期间本地缓冲有界，记录待上传事件 / 成果；限额、断电和磁盘不足处理明确 | `/machines、/machines/n1` |
| REQ-NODE-012 | 用户能停用或卸载节点；stop 停止接新任务并执行规定的优雅关闭，强制停止单独显式操作 | `/machines、/machines/n1` |
| REQ-PREVIEW-001 | 使用 frp 式反向隧道，通过节点出站连接将当前执行的本地服务映射到受控云端预览入口 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-002 | 自动分配不可预测的随机子域名，统一 DNS / TLS 管理，绑定租户、项目、执行、服务与期限 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-003 | 预览域与控制台使用隔离来源，按实例隔离；控制台身份 Cookie 不透传给不可信预览应用 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-004 | 支持 Web 和 API，含必要 HTTP、WebSocket、SSE、Range、请求体与响应流 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-005 | 服务登记验证端口归属、执行工作区和实际进程，只转发被授权的本地服务 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-006 | 记录申请、配置、启动、健康、可用、排空、释放、失败状态，所有操作幂等 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-007 | 采用租约、心跳、TTL、空闲回收和最长存活；执行结束、撤权、节点失联触发释放 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-008 | 清理涵盖隧道代理、节点子进程、监听端口、临时文件、凭据、域名映射 / 租约 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-009 | 定期核对云端登记与 frp 服务端、节点实际状态，检查连接、端口、健康、TLS 和资源泄漏 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-010 | 支持评审指定版本的隔离预览，实时工作稿与已提交版本明确区分 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-011 | 外部分享需指定收件范围、只读能力、期限和人类授权，可撤销并审计 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-PREVIEW-012 | 预览数量、并发、连接、带宽和流量有配额，配额异常可定位项目 / 执行 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
| REQ-REMOTE-001 | 支持执行工作区远程文件树、查看、搜索、下载与版本差异；敏感文件按策略遮蔽 | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-002 | 文件上传 / 编辑 / 删除须独立权限、版本校验和必要接管，署名真实操作人 | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-003 | 使用 xterm.js 提供实时终端界面；终端后端由节点创建 PTY，Windows 使用受支持的 ConPTY | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-004 | 区分只读终端观察和可交互控制；多人观察可以并行，交互写入实行明确控制权 | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-005 | 交互终端仅授权 Human，独立权限、必要重新认证、空闲超时和审计；个人电脑默认关闭 | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-006 | 终端有起止、重连、断开、输出回放与敏感信息处理；禁止无边界暴露主机 Shell | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-007 | 执行输出、终端控制和 frp Web 预览可复用连接基础设施但具有独立授权与限额 | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REMOTE-008 | 移动客户端可观察与经授权操作远程节点，输入辅助、断线与后台状态清楚 | `/machines/n1?tab=files、/machines/n1?tab=terminal` |
| REQ-REPO-001 | 项目连接一个或多个授权仓库，登记平台、仓库、分支、路径与负责团队 | `/repositories` |
| REQ-REPO-002 | 监控 push、commit、PR / MR、review、merge、tag / release、CI 结果等提供商支持的事件 | `/repositories` |
| REQ-REPO-003 | 优先受验签 webhook，提供增量轮询补偿；事件去重、乱序处理、丢失对账 | `/repositories` |
| REQ-REPO-004 | 按项目、分支、路径、事件与严重度配置通知到群、私信、待办或邮件 | `/repositories` |
| REQ-REPO-005 | 关联变更到目标、任务、Flow、执行和证据；无法自动关联时保留未归属事件 | `/repositories` |
| REQ-REPO-006 | 固定 commit / 构建输入；新提交、force push、分支删除或 CI 重跑使相关旧检查标记适用性变化 | `/repositories` |
| REQ-REPO-007 | 可按授权策略触发代码检查、生成摘要、建立待处理项或启动指定 SOP | `/repositories` |
| REQ-REPO-008 | AI 解释变更与影响时引用实际 diff / 文件，披露未读范围和失败来源 | `/repositories` |
| REQ-REPO-009 | 连接器权限撤销、同步失败、速率限制、仓库迁移进入异常与恢复流程 | `/repositories` |
| REQ-COST-001 | 按组织、项目、计划、Flow 环节、任务、Human 发起者、Agent、模型、执行统计用量和费用 | `/cost、/models` |
| REQ-COST-002 | 区分输入、输出、缓存、供应商独立报告的推理 Token 与其他计费单位 | `/cost、/models` |
| REQ-COST-003 | 保存供应商实际值、估算值、未知值、价格版本、币种、时间和对账状态 | `/cost、/models` |
| REQ-COST-004 | 支持组织 / 项目 / Agent / 任务预算、每日 / 周期上限与并发限制，执行前预留、完成后结算 | `/cost、/models` |
| REQ-COST-005 | 达到软阈值通知，硬阈值停止新调用并在安全边界暂停；进行中调用按可取消性处理 | `/cost、/models` |
| REQ-COST-006 | 自动调节可缩短无关上下文、使用缓存 / 摘要、限制候选数量、调整并发与输出上限 | `/cost、/models` |
| REQ-COST-007 | 在允许模型集合内，按任务能力、风险、质量要求与预算路由；允许策略性降档或升级 | `/cost、/models` |
| REQ-COST-008 | 原生执行中的换模型能力由适配器声明；不支持时在边界新建关联执行 | `/cost、/models` |
| REQ-COST-009 | 展示每次自动调节的触发原因、原配置、新配置、预计影响和实际节省 / 质量结果 | `/cost、/models` |
| REQ-COST-010 | 提供成本趋势、异常检测、Top 消耗、预算预测与每个已接受成果的总成本 | `/cost、/models` |
| REQ-COST-011 | 用量与账单对账修正追加记录，保留原估算和差异；不能改掉历史计量 | `/cost、/models` |
| REQ-SCHED-001 | 支持一次性、固定间隔、日历 / Cron 和事件触发；可关联任务、SOP、报告、检查与通知 | `/schedules` |
| REQ-SCHED-002 | 保存时区、夏令时规则、起止、节假日 / 工作日策略、负责人和授权来源 | `/schedules` |
| REQ-SCHED-003 | 明确错过触发的跳过 / 合并补跑 / 有界逐次补跑，以及并发、排队和互斥策略 | `/schedules` |
| REQ-SCHED-004 | 调度状态持久化，带幂等触发键、租约与故障转移；区分调度投递与实际执行 | `/schedules` |
| REQ-SCHED-005 | 每次运行重新检查有效权限、预算、资料版本、节点能力和必要审批 | `/schedules` |
| REQ-SCHED-006 | 支持暂停、恢复、修改、终止、手动运行、历史与失败重试 | `/schedules` |
| REQ-SCHED-007 | 连续失败、超时、预算不足、长期未运行触发告警与自动停用策略 | `/schedules` |
| REQ-SCHED-008 | 定时执行仍受内部验收与人工决定约束；生成草稿和正式提交 / 发布分开 | `/schedules` |
| REQ-QA-001 | 每个环节开工前确定编号要求、检查方式、输入 / 输出、指定评审者和完成条件 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-002 | 所有环节交接和最终交付前，当前版本的必需内部验收全部通过 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-003 | 区分确定性检查、AI 辅助判断和 Human 专业判断；主观要求必须有适当评审方法 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-004 | 软件保留独立重跑、冻结验收、测试 Agent、跨模型产品验收与工具校准要求 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-005 | 视频检查规格、素材 / 工程完整性、声画与人工审片；小说检查设定 / 连续性与编辑判断 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-006 | 支持定期、里程碑、提交、上游变化、交付前触发检查和评审 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-007 | 相同版本的可复用检查结果按验证器 / 配置 / 输入版本缓存，失效后重检 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-008 | 失败进入本环节修复或上游退回，反馈明确位置、期望、实际与阻塞范围 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-009 | 误检可申请复核；新复核结论保留理由、方法、授权与旧结论，不直接篡改失败结果 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-010 | 检查者与执行者遵守职责分离；适用的例外需明确规则，不能把自评标成独立评审 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-011 | 最终交付打包固定成果版本、源文件 / 导出件、证据、要求映射、局限与使用说明 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-012 | 对外交付和必要发布由 Human 负责并批准，客户回执区分接受、部分接受、拒收 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-QA-013 | 已交付后发现问题，保留原包、定位影响、发起修复 / 变更与重新验收 | `/tests、/testplans、/defects、/p/launch/work-items/W-104?tab=review` |
| REQ-APP-001 | 审批覆盖目标 / OKR、计划、SOP 发布 / 变更、必要分派、工具与生产权限、预算例外、验收争议、正式交付 | `/approvals、/deliveries、/policies` |
| REQ-APP-002 | 审批流支持顺序、并行、指定角色、ALL / ANY / 法定人数策略，明确超时、代理和升级 | `/approvals、/deliveries、/policies` |
| REQ-APP-003 | 每项审批绑定对象版本、请求人、审批人、授权来源、理由、证据、时限和结果 | `/approvals、/deliveries、/policies` |
| REQ-APP-004 | 同一审批可在会话、工作台、Flow、成果详情处理，执行相同服务端校验 | `/approvals、/deliveries、/policies` |
| REQ-APP-005 | 支持批准、拒绝、退回补充、撤回、过期和重新提交；拒绝 / 退回包含具体原因 | `/approvals、/deliveries、/policies` |
| REQ-APP-006 | 人类责任人与 Agent 委托明确；Agent 不批准自产、不给自己授权、不确认人类报告 | `/approvals、/deliveries、/policies` |
| REQ-APP-007 | 关键外部副作用、正式交付、合同 / 范围承诺、生产访问授权保留 Human 关口 | `/approvals、/deliveries、/policies` |
| REQ-APP-008 | 低风险自动决定依据预先确认策略，记录系统 / Agent 主体、策略版本、输入事实和理由 | `/approvals、/deliveries、/policies` |
| REQ-APP-009 | 讨论中的意见或推断可自动提取为决定草案，有权确认后成为业务决定 | `/approvals、/deliveries、/policies` |
| REQ-APP-010 | 审批前重新核验版本、权限、验收与预算，必要动作重新认证；撤权即时阻止后续动作 | `/approvals、/deliveries、/policies` |
| REQ-APP-011 | 审批流与内部验收相互独立，审批同意不能替代未通过的必需技术 / 专业门禁 | `/approvals、/deliveries、/policies` |
| REQ-APP-012 | 一键人工接管可暂停自治、撤销相关委托、指定负责人并保留恢复方案 | `/approvals、/deliveries、/policies` |
| REQ-REPORT-001 | Agent 自动生成日 / 周 / 月 / 季 / 年报，支持成员、环节、项目、团队、组织层级 | `/reports、/worklogs` |
| REQ-REPORT-002 | Human 可由 AI 生成草稿，但必须本人确认后上报；Agent 报告可按策略自动提交，仍由指定 Human 审核 | `/reports、/worklogs` |
| REQ-REPORT-003 | 报告包含完成工作、未完成与原因、风险阻塞、成本质量、复盘和下期计划 | `/reports、/worklogs` |
| REQ-REPORT-004 | 时间上日→周→月→季→年、组织上成员→项目 / 团队→组织汇总；避免多重归属重复计数 | `/reports、/worklogs` |
| REQ-REPORT-005 | 提交前核对计划、OKR、工作状态、验收和下级报告；阻断错误禁止提交，非阻断缺失显式标记 | `/reports、/worklogs` |
| REQ-REPORT-006 | 主管可确认、退回和评论，逾期提醒与升级，历史修订可比较 | `/reports、/worklogs` |
| REQ-REPORT-007 | 并列展示系统事实和作者主观判断；明显冲突要求解释与纠偏 | `/reports、/worklogs` |
| REQ-REPORT-008 | 从确认的复盘提出改进任务、SOP 调整或经验记忆，分别走对应确认 | `/reports、/worklogs` |
| REQ-WATCH-001 | 监督 Agent 在授权范围观察 Human / Agent 的目标、计划、任务、Flow、进展、成本和交接 | `/overview、/health、/schedules` |
| REQ-WATCH-002 | 结合确定性规则与 AI 分析识别延期、卡住、进度冲突、循环返工、交接失败、预算风险和无人负责 | `/overview、/health、/schedules` |
| REQ-WATCH-003 | 检测跨环节影响，提示哪个上游问题将影响哪些下游与里程碑 | `/overview、/health、/schedules` |
| REQ-WATCH-004 | 在群、私信、工作台和适用推送中提醒，包含责任人、影响、推荐动作与处理期限 | `/overview、/health、/schedules` |
| REQ-WATCH-005 | 提醒去重、聚合、节流、严重级别和静默窗口，关键停线使用独立升级规则 | `/overview、/health、/schedules` |
| REQ-WATCH-006 | 监督 Agent 可提出重规划、调人、改模型和重新检查建议；自动处理必须有明确策略授权 | `/overview、/health、/schedules` |
| REQ-WATCH-007 | 监测监督机制自身的心跳、积压、漏检窗口和调用预算 | `/overview、/health、/schedules` |
| REQ-WATCH-008 | 人可纠正误报、说明例外，形成可审核规则改进；保留原风险记录与反馈 | `/overview、/health、/schedules` |
| REQ-HEALTH-001 | 监控节点 CPU、内存、磁盘容量 / IO、网络、进程、运行槽和可选 GPU 指标 | `/health、/machines` |
| REQ-HEALTH-002 | 监控 daemon、模型 / 工具适配器、预览 / frp、队列、调度器、API、数据库、存储和索引作业健康 | `/health、/machines` |
| REQ-HEALTH-003 | 支持受控 HTTP / TCP / 进程探测、延迟、错误率、超时、依赖状态、TLS 证书到期和存储风险 | `/health、/machines` |
| REQ-HEALTH-004 | 节点主动上报与云端探测结合，设置窗口、阈值、连续失败与恢复条件 | `/health、/machines` |
| REQ-HEALTH-005 | 告警按组织 / 项目 / 服务路由到指定协作群，必要时 @责任人、App 推送或邮件 | `/health、/machines` |
| REQ-HEALTH-006 | 告警有唯一身份与触发、确认、处理中、恢复、关闭记录；支持指派和升级 | `/health、/machines` |
| REQ-HEALTH-007 | 支持聚合、去重、抑制、维护窗口、分级升级与恢复通知，保留被抑制记录 | `/health、/machines` |
| REQ-HEALTH-008 | 告警关联正在执行的任务、Flow、预览和项目；可创建处置工作 | `/health、/machines` |
| REQ-HEALTH-009 | 仅对预先允许的低风险动作自动修复；重启、杀进程、释放资源按授权及副作用规则执行 | `/health、/machines` |
| REQ-HEALTH-010 | 监控采集、保留、聚合与采样可配置，采集开销单独计量 | `/health、/machines` |
| REQ-HEALTH-011 | 告警投递失败持久重试，频道不可用时按策略使用后备通知渠道 | `/health、/machines` |
| REQ-HEALTH-012 | 使用已授权真实信息生成 AI 故障摘要和建议，保留原始指标 / 日志链接并脱敏 | `/health、/machines` |
| REQ-DATA-001 | 消息、决定、审批、操作、执行、交接、报告与相关材料均持久化并关联主体、时间、对象、版本和来源 | `/governance/audit、/knowledge` |
| REQ-DATA-002 | 消息默认永久保存，支持分区 / 冷归档；附件与引用保持完整性，普通用户归档不物理删除 | `/governance/audit、/knowledge` |
| REQ-DATA-003 | 所有已生效决策自动记账：Human 决策、策略自动决定、Agent 获授权决定均保留理由与证据 | `/governance/audit、/knowledge` |
| REQ-DATA-004 | 原始消息修订、撤回、审批撤销和决定被取代以追加历史表达；明确当前有效版本 | `/governance/audit、/knowledge` |
| REQ-DATA-005 | 关键状态、审批与审计具有一致的事务边界；大文件采用可核对的上传 / 登记协议 | `/governance/audit、/knowledge` |
| REQ-DATA-006 | 全局搜索覆盖消息、线程、目标、计划、任务、Flow、成果、文件内容、决定、审批、报告与授权日志 / 告警 | `/governance/audit、/knowledge` |
| REQ-DATA-007 | 关键词与结构化检索为基础，语义搜索可增强；结果显示来源、命中位置、新鲜度与版本 | `/governance/audit、/knowledge` |
| REQ-DATA-008 | 索引、搜索摘要、计数、缓存、导出、附件与引用逐层遵守租户 / 对象权限 | `/governance/audit、/knowledge` |
| REQ-DATA-009 | 撤权优先阻止读取，随后处理索引 / 缓存；搜索点击重新鉴权 | `/governance/audit、/knowledge` |
| REQ-DATA-010 | 提供增量索引、失败重试、重建、水位、源一致性校验和损坏告警 | `/governance/audit、/knowledge` |
| REQ-DATA-011 | 提供按权限导出工作证据包、消息历史、文件清单和决定 / 审批记录，含稳定引用与校验信息 | `/governance/audit、/knowledge` |
| REQ-DATA-012 | 备份、异地或隔离副本、恢复演练、存储容量预测和保留政策可管理 | `/governance/audit、/knowledge` |
| REQ-DATA-013 | 法律要求、误传秘密和有效删除请求作为独立治理流程，明确保留、遮蔽与清除范围 | `/governance/audit、/knowledge` |
| REQ-DATA-014 | 原始高频指标、缓存与可重建索引可按各自政策压缩 / 清理，永久保存范围明确 | `/governance/audit、/knowledge` |
| REQ-DATA-015 | 全文解析、OCR、字幕 / 转写与专业格式提取异步处理，保留原件及提取版本 | `/governance/audit、/knowledge` |
| REQ-KNOW-001 | 内置文件资产、知识、记忆和外部连接能力，支持个人、工作、项目、团队和组织范围 | `/knowledge` |
| REQ-KNOW-002 | 自动从消息、确认决定、接受成果、复盘与纠错提取知识 / 记忆候选 | `/knowledge` |
| REQ-KNOW-003 | 区分偏好、工作状态、事实 / 决策引用与经验；状态从真实领域记录恢复 | `/knowledge` |
| REQ-KNOW-004 | 自动提取可配置；规则型 / 高影响记忆需审核，未经证实的推断不得成为团队事实 | `/knowledge` |
| REQ-KNOW-005 | 用户 / 有权管理员可纠正、禁用、删除派生记忆；原消息保留按独立政策处理 | `/knowledge` |
| REQ-KNOW-006 | 来源更新、被取代、撤权或删除时传播失效，处理派生摘要、索引和缓存 | `/knowledge` |
| REQ-KNOW-007 | Context Builder 按目标、阶段、角色、权限、模型能力和 Token 预算选择资料 | `/knowledge` |
| REQ-KNOW-008 | 每次执行保存上下文来源清单、版本、组装器版本、选择理由、缺失与截断信息 | `/knowledge` |
| REQ-KNOW-009 | 支持混合检索、相关性排序、去重与受控摘要；外部文档中的指令不得升级系统权限 | `/knowledge` |
| REQ-KNOW-010 | 连接器声明源系统、权限、同步方式、版本与健康；读取和写回分开授权 | `/knowledge` |
| REQ-KNOW-011 | 跨 Agent、模型与专业工具复用可迁移工作事实；交接只共享获授权资料 | `/knowledge` |
| REQ-KNOW-012 | 小说人物 / 世界观、品牌规范、产品约束、架构决策与运营口径使用同一来源治理 | `/knowledge` |
| REQ-RUNTIME-001 | Agent 身份与 runtime、模型、节点解耦，同一团队可混用 CLI 与直接 API | `/agents、/models、/tools` |
| REQ-RUNTIME-002 | 统一适配器定义启动、事件、等待、工具授权、取消、结果、部分成果、错误和用量 | `/agents、/models、/tools` |
| REQ-RUNTIME-003 | 发布能力清单，区分支持、受限、不支持、未经验证；调度按必要能力匹配 | `/agents、/models、/tools` |
| REQ-RUNTIME-004 | 同一请求保留平台 ID、供应商 ID、模型版本、配置版本、工具版本和关联链 | `/agents、/models、/tools` |
| REQ-RUNTIME-005 | 提供类似 cc-switch 的模型配置档案：提供商、协议、Base URL、模型别名、参数、凭据引用、作用域 | `/agents、/models、/tools` |
| REQ-RUNTIME-006 | 远程下发配置由授权管理者或策略发起，绑定目标节点 / Agent / 工作区、版本和有效期 | `/agents、/models、/tools` |
| REQ-RUNTIME-007 | 配置原子更新、保留备份、连通性检查和失败回滚；支持灰度与批量结果 | `/agents、/models、/tools` |
| REQ-RUNTIME-008 | 默认使用执行级配置 / 环境与本地代理，不覆盖用户个人全局配置；全局写入须明确授权 | `/agents、/models、/tools` |
| REQ-RUNTIME-009 | 凭据优先保存在云端秘密服务 / 安全代理，节点拿短期受限能力；必须直连时按独立策略安全分发 | `/agents、/models、/tools` |
| REQ-RUNTIME-010 | 新配置默认用于后续执行；运行中切换依能力和安全边界处理，必要时新建关联执行 | `/agents、/models、/tools` |
| REQ-RUNTIME-011 | 模型探测、连通性测试、Token 测算标记测试用途并计费归属 | `/agents、/models、/tools` |
| REQ-RUNTIME-012 | 支持每 runtime 的版本兼容矩阵、升级告警与隔离回退，保留有差异的错误和授权语义 | `/agents、/models、/tools` |
| REQ-RUNTIME-013 | 团队工具目录管理 Skills、MCP 与专业工具版本、内容哈希、授予范围和操作 Schema | `/agents、/models、/tools` |
| REQ-CLIENT-001 | Web 和 Tauri 客户端共用产品对象、API、权限与实时协议；目标包含 Windows、macOS、Linux、iOS、Android | `/settings、/overview、review.html` |
| REQ-CLIENT-002 | 前端业务层共用，原生能力通过明确适配层接入；Web 不依赖 Tauri API 才能工作 | `/settings、/overview、review.html` |
| REQ-CLIENT-003 | 移动端支持协作、计划 / Flow 确认、审批、任务跟进、预览、批注、告警处理与远程节点操作 | `/settings、/overview、review.html` |
| REQ-CLIENT-004 | 桌面端支持通知、深链接、文件选择 / 拖放与经授权本地能力，安装升级有签名与版本策略 | `/settings、/overview、review.html` |
| REQ-CLIENT-005 | 使用系统安全存储保护会话和凭据，支持撤销设备、退出、重新认证与身份切换 | `/settings、/overview、review.html` |
| REQ-CLIENT-006 | 前台使用实时连接，移动后台按 OS 能力使用推送和恢复同步；不依赖永久后台 WebSocket | `/settings、/overview、review.html` |
| REQ-CLIENT-007 | 跨端同步未读、待处理和工作状态；本地草稿 / 上传重试按用户与对象隔离 | `/settings、/overview、review.html` |
| REQ-CLIENT-008 | 深链接打开同一对象 / 固定版本，外部链接、Web 与 App 导航遵守相同授权 | `/settings、/overview、review.html` |
| REQ-CLIENT-009 | 明确平台能力矩阵、最低系统版本、WebView / 插件要求、更新与分发方式 | `/settings、/overview、review.html` |
| REQ-CLIENT-010 | 桌面 / 手机客户端与执行 daemon 分离；移动端主要远程操作执行节点 | `/settings、/overview、review.html` |
| REQ-CLIENT-011 | 支持国际化、键盘、读屏、触控、减少动画、响应式及长内容阅读 | `/settings、/overview、review.html` |
| REQ-CLIENT-012 | 缓存只作加速，明确离线范围与失效；敏感资料下载 / 缓存遵守策略 | `/settings、/overview、review.html` |
| REQ-UX-001 | 工作台汇总多个项目与“与我有关”，协作区管理具体项目，会话区统一沟通与执行交互 | `/overview、/work、/settings` |
| REQ-UX-002 | 管理会话右栏是授权全局摘要，执行会话右栏是当前任务预览，可展开同一规范详情 | `/overview、/work、/settings` |
| REQ-UX-003 | 列表、看板、Flow、评审布局共用工作事实；用过滤与视图状态切换 | `/overview、/work、/settings` |
| REQ-UX-004 | 一个对象有一个规范详情地址；会话 / 通知 / 画布中的面板是同对象的嵌入上下文 | `/overview、/work、/settings` |
| REQ-UX-005 | 执行交互统一到会话，保留执行身份、历史、工具权限与关联工作 | `/overview、/work、/settings` |
| REQ-UX-006 | 文件、图片、文档、代码、视频、小说复用成果查看 / 版本 / 批注机制，专业编辑仍用合适工具 | `/overview、/work、/settings` |
| REQ-UX-007 | 所有审批 / 告警 / 交接在统一待处理体系聚合，专业页面提供同一动作的快捷入口 | `/overview、/work、/settings` |
| REQ-UX-008 | 目标、报告、交付包、确认单等独立生命周期对象保留详情；高级能力按需展开可搜索 | `/overview、/work、/settings` |
| REQ-UX-009 | 浏览器与 App 返回恢复筛选、阅读位置、选中版本和未提交草稿；跳转显示所属范围 | `/overview、/work、/settings` |
| REQ-UX-010 | 客户与内部成员使用同路由权限投影，管理者与执行者使用同对象的角色视角 | `/overview、/work、/settings` |
| REQ-UX-011 | 新路由仅在独立对象、权限或编辑意图确有需要时注册；废弃路由统一迁移入口与测试 | `/overview、/work、/settings` |
| REQ-UX-012 | 页面清单附能力去向，包括高级报告、工具、主管编排、远程终端、治理与专业工作包 | `/overview、/work、/settings` |
| REQ-NFR-001 | 核心新增技术不得依赖 JVM / Java 类重型常驻栈；优先复用现有 Go 后端和轻量独立进程 | `/settings、/health、/policies` |
| REQ-NFR-002 | “轻量”按真实占用与负载衡量，不能只依据语言名；模型推理、渲染与第三方 runtime 单独核算 | `/settings、/health、/policies` |
| REQ-NFR-003 | 核心模块化单体、独立 daemon / Worker；按连接和吞吐需要增加中间件，不先拆大量服务 | `/settings、/health、/policies` |
| REQ-NFR-004 | 数据库兼容既有三方言约束，生产推荐 PostgreSQL；高级索引按部署能力启用 | `/settings、/health、/policies` |
| REQ-NFR-005 | 状态 / 审批事务、outbox、幂等、租约、重连和备份恢复作为基础保证 | `/settings、/health、/policies` |
| REQ-NFR-006 | 复用已有分页、授权与模块边界；搜索投影和关联目录不成为中央万能业务表 | `/settings、/health、/policies` |
| REQ-NFR-007 | 大消息历史、媒体、Flow 与多项目聚合按服务端过滤、增量、分页、虚拟化和缓存处理 | `/settings、/health、/policies` |
| REQ-NFR-008 | 检查依赖许可证、维护状态、供应链、平台支持与升级路径，锁定具体版本 | `/settings、/health、/policies` |
| REQ-NFR-009 | 租户、个人、项目、对象权限贯穿消息、搜索、模型、文件、终端、预览、导出与监控 | `/settings、/health、/policies` |
| REQ-NFR-010 | 运行日志、指标和 trace 关联到工作 / 环节 / 执行，按需要开启深度采集与脱敏 | `/settings、/health、/policies` |
| REQ-NFR-011 | 云端故障、节点断网、磁盘不足、队列积压、索引失败、隧道失联均有恢复演练 | `/settings、/health、/policies` |
| REQ-NFR-012 | 原型、模拟运行、适配器集成、平台端到端、生产规模测试分别记录 | `/settings、/health、/policies` |
| REQ-DETAIL-001 | 简单问答或一次草稿不强迫用户建组织、团队、机器、SOP；形成正式工作时必须有私人归属、负责人和必要契约。自动私人集合是推荐方案，实体复用与建立时机待冻结 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-002 | 个人转团队 / 外部共享前预览选定消息、文件、成果版本、依赖引用、接收者与访问范围；发现未授权依赖时提示补授权或排除 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-003 | 动态澄清表单预填已知值和来源 / 版本，只要求影响下一步的缺项；推断与已确认信息区别展示，修改同步到关联草稿 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-004 | 实时预览新增批注时固定可恢复快照 / 修订与定位信息；无法稳定捕获时明确要求固定版本再批注 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-005 | 图片 / 设计支持候选并排、局部区域标注、选择依据与指定区域修改；保留落选候选、来源、成本和新旧关系 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-006 | 视频 / 音频反馈绑定媒体版本、时间点 / 时间区间、适用音轨 / 字幕；代理与原件保存映射 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-007 | 文档 / 小说按章节或段落增量呈现，保留阅读锚点；批注引用版本与片段，人工修订记录作者和基线 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-008 | 报告批注关联数据时窗、来源版本、指标口径、图表 / 数据行及筛选条件 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-009 | 对外 Agent 消息默认草稿；仅在明确授权范围自动发送事实性答复，范围 / 合同 / 价格等承诺须 Human 确认 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-010 | 自动分派默认选择满足要求的最简单执行方式；新增主管、多候选、跨模型协作说明能力、独立性或并行收益与成本 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-011 | 软件内部修复轮次默认 5 与正式质量返工达到 3 次升级分别计数、配置并展示；环境重试、质量返工、需求变更不混计 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-DETAIL-012 | 无外部接收人时 L4 标为不适用并记录原因；L2 必需内部检查与 L3 有权接受继续成立；公开发布独立授权 | `/chat、/p/launch/artifacts/W-103、/p/film/artifacts/W-201、/reports` |
| REQ-PRO-001 | 保留 Human 工时记录：主体、日期 / 时区、时长、工作类别、项目 / 工作关联、说明、草稿 / 提交 / 审核 / 退回 / 更正历史；有权代理登记标记实际作者 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-002 | Human 工时、Agent 墙钟运行时长、实际模型 / 工具耗时、Token 与费用分列；汇总按唯一来源与分摊口径去重 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-003 | 工时支持作为周期报告 / KR 证据引用，记录核对状态、缺失和冲突；报告确认不等同于每条工时已审核 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-004 | 软件需求、任务、缺陷复用 WorkItem，保留类型字段、严重度 / 优先级、复现步骤、期望 / 实际、环境、影响版本、负责人、关联提交、验证与关闭依据 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-005 | 保留迭代、里程碑、发布与工作关联；支持范围基线、目标日期、依赖、准入 / 准出条件和变更记录，专业状态迁移校验必需字段和责任 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-006 | 独立测试用例保存版本、要求引用、前置、输入、步骤、期望、类型 / 优先级与负责人；用例可复用但不能修改已执行快照 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-007 | 测试计划保存范围、用例固定版本、目标提交 / 构建、环境、分派、执行窗口、完成条件；变更追加版本并明确未执行与重验范围 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-008 | 测试执行与报告保存实际结果、证据、运行主体、工具版本、时间、日志和失败关联缺陷；区分 Human 手测、Agent 自测、平台独立验证 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-009 | 软件要求确认 / 评审支持结构化意见、固定版本、分歧与变更影响；冻结验收与平台独立重跑、测试 Agent、跨模型产品验收继续保留 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-010 | 云效 / Codeup / Flow / Testhub 等专业集成保留明确外部 ID、来源系统、仓库 / 提交 / 构建、测试对象和工作映射；按具体 API 能力核实导入与写回 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-011 | 工作包版本至少声明输入与必需程度、步骤 / 输入输出契约、工具能力、成果格式、专业预览与批注、客观验证器、主观评审准则 / 人员、预算与返工上限 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-012 | 运营包保留指标口径 / 时窗与观察期；产品包保留研究依据 / 场景 / 原型；UI 包保留组件 / 品牌 / 交互反馈；广告包保留渠道规格 / 受众 / 素材授权 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-013 | 小说包保留人物、世界观、大纲、章节及连续性；音视频包保留素材、工程、字幕、音轨、代理和审片；图片包保留候选、局部修改与导出 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-PRO-014 | API、邮件、webhook、消息桥接与实验网关保留为受控来源 / 操作连接器，声明协议、权限、配额、能力、来源和健康；默认不启用写出副作用 | `/worklogs、/tests、/testplans、/defects、/templates、/connectors` |
| REQ-OPS-001 | Runner 的 CLI 与 daemon 采用纯 Go，同一发行物提供服务管理；不建设必需的 Go + Node 常驻桥接双栈 | `/machines、/models、/tools` |
| REQ-OPS-002 | 原生签名安装 / 升级路径必须独立可用；npm / npx 仅可选分发。安装 / 注册服务与启动任务分离，认证失败不能显示节点可执行 | `/machines、/models、/tools` |
| REQ-OPS-003 | Go 直接调用模型 SDK 或 HTTP；SDK 缺功能可按官方协议补足，但协议直连只提供模型能力，Agent 工具循环、预算、权限与结果归档由平台执行层负责 | `/machines、/models、/tools` |
| REQ-OPS-004 | 原生 CLI / 服务协议适配与 SDK 分别登记能力，不能假定等价；结构化事件优先，终端文本仅作为明确受限适配 | `/machines、/models、/tools` |
| REQ-OPS-005 | MCP、Skills、专业工具声明来源、版本 / 哈希、依赖、授权范围、输入输出与操作能力；按工具实际协议运行，Skill 内容不能自动取得进程权限 | `/machines、/models、/tools` |
| REQ-OPS-006 | 进程生命周期覆盖启动失败、事件解析失败、stdin 等待、输出阻塞、取消、子进程回收、退出码与结果对账；外部 runtime 升级与 Runner 升级分开 | `/machines、/models、/tools` |
| REQ-OPS-007 | 保活由 launchd / systemd 或支持的用户服务 / Windows SCM 承担；daemon 提供就绪、心跳、崩溃与诊断状态，异常重启避免快速循环占满资源 | `/machines、/models、/tools` |
| REQ-OPS-008 | 升级前验证包、配置与协议兼容，节点进入排空、不接新任务；运行完成或到明确可接续边界才切换，失败有回滚与原因 | `/machines、/models、/tools` |
| REQ-OPS-009 | 若承诺“活跃任务不中断升级”，worker / 原生 runtime 生命周期必须独立于控制 daemon，具备持久事件 / 检查点、所有权交接与版本兼容；不具备能力的任务等待完成再升级 | `/machines、/models、/tools` |
| REQ-OPS-010 | 云端与节点滚动升级维持协议兼容窗口，先升级可兼容组件再收敛；状态 / 数据迁移有备份与回退边界 | `/machines、/models、/tools` |
| REQ-UX-013 | 左侧区域菜单、频道列表与项目实例选择在当前工作区原位切换内容，不执行路由跳转；保留侧栏位置、滚动、展开与顶部当前栏目 | `/overview、/work、/settings` |
| REQ-ORG-011 | 租户创建、通过邀请加入和租户切换统一放在左上角 Logo 右侧；租户下管理多个项目实例并支持租户内汇总 | `/members、/invitations、/policies` |
| REQ-PLAN-010 | 提供项目计划与个人任务的月 / 周排期日历，按项目范围与 Human / Agent 查看，展示跨日任务、项目截止与未排期工作；起止日期调整记录理由、作者、旧新值与版本 | `/work?view=calendar、/p/launch/work-items/W-104?tab=plan` |
| REQ-FLOW-015 | 工作流提供类似 n8n 的节点编排体验：节点库、画布连线、节点参数与输入输出映射、右侧查看 / 编辑面板、复制删除、配置检查与节点调试；触发、AI、工具、人工审批、检查、条件、汇合、等待、连接与数据节点有对应配置 | `/p/launch/flow` |
| REQ-PREVIEW-013 | 任务实时预览支持多个可切换标签，同类可多开，Web / API / 文件 / 终端 / 文稿等按适配能力展示；可新增关闭、显示连接状态、保留各标签交互状态，预览绑定任务、执行与权限范围 | `/machines/n1?tab=preview、/p/launch/artifacts/W-104` |
