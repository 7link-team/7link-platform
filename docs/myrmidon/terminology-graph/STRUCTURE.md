# 关系结构核对

这是已登记关系的结构检查，不是产品缺陷数量或业务闭环验收。

总网：421 个术语，930 条关系，0 个断线节点，1 个连通分组。

## 仅租户归属或枚举连接

这些节点虽不完全断线，但还没有登记业务对象间关系，优先核对其真实上下游。不能为了清空清单虚构关系。


## 单邻居节点

单邻居不等于缺陷：枚举值、入口和视图可能合理地只关联一个对象。需结合定义及来源核对，不能要求所有节点都有两个业务对象。

- L1完成层级（acceptance_L1）→ 完成声明 Claim
- L2完成层级（acceptance_L2）→ 验证 Verification
- L3完成层级（acceptance_L3）→ 业务裁决 Decision
- L4完成层级（acceptance_L4）→ 最终验收 Acceptance
- AGENT主体（actor_AGENT）→ 主体账号 Principal
- CLIENT主体（actor_CLIENT）→ 外部联系人
- MACHINE主体（actor_MACHINE）→ 主体账号 Principal
- SYSTEM主体（actor_SYSTEM）→ 记忆治理记录
- 创建执行分支（branch_execution）→ 执行 Execution
- OPC / 一人公司经营者（business_role_58）→ Human负责人
- 组织 / 团队管理者（business_role_59）→ Human负责人
- 项目负责人（business_role_60）→ Human负责人
- 环节人类负责人（business_role_61）→ Human负责人
- 专业执行者 Human（business_role_62）→ Human负责人
- Agent 执行者（business_role_63）→ Agent成员
- 协调 / 监督 Agent（business_role_64）→ Agent成员
- 验收 / 审核者（business_role_65）→ Human负责人
- 客户 / 外部成员（business_role_66）→ 协作频道
- 平台 / 运维管理员（business_role_67）→ Human负责人
- 任务拆解模板（delivery_decomposition_templates）→ 拆解模板固定版本
- AgentOrigin（enum_AgentOrigin）→ Agent成员
- AgentRole（enum_AgentRole）→ Agent角色绑定
- AgentState（enum_AgentState）→ Agent成员
- AnchorKind（enum_AnchorKind）→ 锚定批注线程
- BranchReason（enum_BranchReason）→ 执行 Execution
- CheckSeverity（enum_CheckSeverity）→ 核对问题
- CommentVisibility（enum_CommentVisibility）→ 锚定批注线程
- DelegationState（enum_DelegationState）→ 授权委托
- DeliveryState（enum_DeliveryState）→ 交付包 Delivery
- EscalationReason（enum_EscalationReason）→ 任务／工作项
- ExceptionDecision（enum_ExceptionDecision）→ 例外报告
- ExecutionKind（enum_ExecutionKind）→ 执行 Execution
- ExecutionState（enum_ExecutionState）→ 执行 Execution
- FailureCategory（enum_FailureCategory）→ 业务裁决 Decision
- GoalLevel（enum_GoalLevel）→ 目标 Goal
- GoalStatus（enum_GoalStatus）→ 目标 Goal
- IntakeStatus（enum_IntakeStatus）→ 来件
- InteractionKind（enum_InteractionKind）→ 执行交互请求
- InteractionState（enum_InteractionState）→ 执行交互请求
- KRSource（enum_KRSource）→ 关键结果 KR
- KnowledgeScope（enum_KnowledgeScope）→ 知识来源
- MachineState（enum_MachineState）→ 执行节点 Machine
- MachineType（enum_MachineType）→ 执行节点 Machine
- ReportLevel（enum_ReportLevel）→ 周期报告
- ReportState（enum_ReportState）→ 周期报告
- SpawnGrantState（enum_SpawnGrantState）→ Agent编制授权
- StateGroup（enum_StateGroup）→ 显示状态定义
- StatusConfidence（enum_StatusConfidence）→ 周期报告
- TemplateDomain（enum_TemplateDomain）→ 专业工作包
- CANCELLED（execution_state_CANCELLED）→ 执行 Execution
- CREATED（execution_state_CREATED）→ 执行 Execution
- FAILED（execution_state_FAILED）→ 执行 Execution
- INTERRUPTED（execution_state_INTERRUPTED）→ 执行 Execution
- SUCCEEDED（execution_state_SUCCEEDED）→ 执行 Execution
- 模块菜单贡献（iam_menu_contributions）→ IAM权限底座
- Matrix（当前未采用）（matrix）→ Centrifugo
- NATS / JetStream（当前未部署）（nats）→ Centrifugo
- 目标结转（okr_carryover）→ 目标 Goal
- OKR四象限视图（okr_quadrants）→ 目标 Goal
- 导入半成品（product_import_partial_work）→ 专业工作包
- 广告工作包（product_package_advertising）→ 专业工作包
- 服务标准（product_service_standard）→ 专业工作包
- React Flow（react_flow）→ 工作流定义 WorkflowDef
- 文件资源绑定（resource_binding）→ 文件 File
- agent_coordinator（role_agent_coordinator）→ Agent角色绑定
- agent_executor（role_agent_executor）→ Agent角色绑定
- agent_lead（role_agent_lead）→ Agent角色绑定
- agent_reviewer（role_agent_reviewer）→ Agent角色绑定
- delivery_lead（role_delivery_lead）→ IAM权限底座
- engineer（role_engineer）→ IAM权限底座
- team_owner（role_team_owner）→ IAM权限底座
- Runner命令入口（runner_cli）→ Runner

## 业务单邻居复核队列

排除状态、主体及角色分类后的单邻居概念。仍不是已确认缺陷；逐项核对来源、权限、动作、结果与异常，合理终点无需强行补线。

- 创建执行分支（branch_execution）：动作入口作用于已有Execution并产出子Execution；父与子属于同一种对象，因此单邻居数量不能表现尝试树，需看Execution的父子关系。
- 任务拆解模板（delivery_decomposition_templates）：模板身份关联不可变版本；拆解实例引用版本而非可变模板本体，单邻居符合固定版本契约。
- 模块菜单贡献（iam_menu_contributions）：IAM内部菜单播种账本，合理实现端点；不独立增加用户业务菜单。
- Matrix（当前未采用）（matrix）：当前未采用的备选技术，仅说明与已选聊天方案的取舍；不是当前部署的功能孤岛。
- NATS / JetStream（当前未部署）（nats）：当前未部署的可选传输能力，仅登记与Centrifugo的能力边界；不是必须创建的服务。
- 目标结转（okr_carryover）：旧目标与新目标属于同一种Goal对象，单邻居不表达前后周期；结转不能改写旧期结果，原型历史追溯仍须验收。
- OKR四象限视图（okr_quadrants）：同一目标数据的展示视图，合理视图端点；四象限轴和分界口径仍待确认，不据此声称交互已验收。
- 导入半成品（product_import_partial_work）：接手半成品的入口场景；已知状态和缺失步骤须登记，原型入口的完整性仍待验收。
- 广告工作包（product_package_advertising）：广告专业场景挂接专业工作包；通用成果和检查契约复用工作包关系，不强行增加广告专属实体。
- 服务标准（product_service_standard）：专业工作包内保障工作的验收依据，合理规则端点；不能直接换算成OKR或绩效。
- React Flow（react_flow）：第三方画布工具，合理工具端点；执行与审批语义归工作流领域，不要求工具直接连接所有业务对象。
- 文件资源绑定（resource_binding）：通用文件绑定机制，目标为多态resource_kind/resource_id；当前图尚未逐项映射全部purpose，保留待核对。
- Runner命令入口（runner_cli）：同一Go Runner的命令入口，合理入口端点；不是第二个执行节点或子节点。
