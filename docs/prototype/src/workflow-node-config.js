import { validateLoopBoundary } from "./workflow-definition";
// Definition metadata only. These fields never execute commands, requests or deployments.
export const workflowNodeLibrary = [
  ["AI 任务", "Bot", "模型与 Agent", "Runtime、模型与明确的提示词 / 工具引用"],
  ["工具调用", "Wrench", "模型与 Agent", "引用已授权 MCP / Skill，声明调用参数"],
  ["源码检出", "GitBranch", "构建与发布", "仓库、分支与固定 commit 输入"],
  ["构建", "Hammer", "构建与发布", "运行环境、构建命令与产物路径"],
  ["测试", "FlaskConical", "构建与发布", "测试命令、报告与通过标准"],
  ["制品归档", "Package", "构建与发布", "固定制品版本与校验值"],
  ["部署 / 回滚", "Rocket", "构建与发布", "目标环境、固定制品与 Human 确认"],
  ["容器镜像", "Container", "构建与发布", "镜像构建配置、固定标签与仓库凭据引用"],
  ["安全扫描", "ShieldCheck", "构建与发布", "声明扫描对象、严重度门禁与报告"],
  ["通知", "Bell", "协作与质量", "配置明确接收范围；不发送真实消息"],
  ["子工作流", "Workflow", "流程控制", "固定模板版本与输入输出映射；不启动执行"],
  ["人工审批", "Stamp", "协作与质量", "指定 Human、决策依据与驳回处理"],
  ["独立检查", "ShieldCheck", "协作与质量", "独立核验固定成果与验收条款"],
  ["条件分支", "GitBranch", "流程控制", "声明条件及是 / 否分支"],
  ["并行汇合", "GitCompareArrows", "流程控制", "等待必需上游输出齐备"],
  ["等待", "Clock3", "流程控制", "等待事件或指定时长"],
  ["HTTP 请求", "Globe2", "连接与数据", "请求映射与凭据引用，不保存明文密钥"],
  ["数据转换", "FileJson", "连接与数据", "转换方式、映射规则与输出结构"],
];
const f = (key, label, options = {}) => ({ key, label, group: "业务参数", ...options });
export const workflowNodeFields = {
  "触发": [f("triggerMode", "触发方式", { options: ["手动", "Git 事件", "定时", "Webhook", "会话事件"] }), f("gitEvent", "Git 事件", { options: ["push", "pull_request", "tag"], when: d => d.triggerMode === "Git 事件" }), f("branchFilter", "分支 / 标签过滤", { required: true, when: d => d.triggerMode === "Git 事件" }), f("cron", "Cron（五段）", { required: true, when: d => d.triggerMode === "定时" }), f("timezone", "时区", { required: true, when: d => d.triggerMode === "定时" }), f("triggerPayload", "触发输入契约", { type: "textarea" })],
  "AI 任务": [f("runtime", "Runtime", { options: ["Codex CLI", "Claude Code", "OpenCode", "模型 API"] }), f("modelId", "模型配置引用", { resource: "models", empty: "继承 Runtime 已批准的模型配置" }), f("promptRef", "固定提示词引用（可选）", { help: "填写已确认的提示词标识及版本；下方仍需声明本节点指令。" }), f("prompt", "节点指令 / 提示词", { required: true, type: "textarea" }), f("skillId", "Skill 引用", { resource: "skills", empty: "不使用 Skill" }), f("mcpId", "MCP 引用", { resource: "mcps", empty: "不使用 MCP" }), f("contextBoundary", "上下文 / 数据范围", { type: "textarea", help: "说明可读资料范围；节点配置不自动授予访问权。" })],
  "工具调用": [f("tool", "已授权工具引用", { required: true, resource: "tools" }), f("toolOperation", "操作名称", { required: true }), f("toolArguments", "参数映射（JSON）", { required: true, type: "textarea", json: true }), f("credential", "凭据引用（可选）", { help: "仅 vault/…、credential/… 或 secret/… 引用；不填写密钥。" })],
  "源码检出": [
    f("sourceMode", "源码输入方式", {options:["远程地址", "执行节点工作区", "关联仓库"], group:"源码输入"}),
    f("repositoryId", "已有仓库引用", {required:true, resource:"repositories", when:d => !d.sourceMode || d.sourceMode === "关联仓库", group:"源码输入"}),
    f("sourceURL", "远程 Git 地址", {required:true, when:d => d.sourceMode === "远程地址", group:"源码输入", help:"使用已知源码地址即可；不创建或维护代码仓库。凭据单独引用。"}),
    f("workspaceRef", "节点工作区路径", {required:true, when:d => d.sourceMode === "执行节点工作区", group:"源码输入"}),
    f("workspaceVersion", "工作区固定快照 / commit 引用", {required:true, when:d => d.sourceMode === "执行节点工作区", group:"源码输入"}),
    f("gitRef", "分支 / 标签", {required:true, when:d => d.sourceMode !== "执行节点工作区", group:"固定版本"}),
    f("commit", "固定 commit / 上游 commit 引用", {required:true, when:d => d.sourceMode !== "执行节点工作区", group:"固定版本", help:"固定 SHA 或 {{trigger.commit}}；不隐式采用最新提交。"}),
    f("checkoutPath", "检出目录", {required:true, group:"执行环境"}),
    f("credential", "源码凭据引用（可选）", {group:"执行环境"}),
  ],
  "构建": [f("environment", "运行环境 / 镜像引用", { required: true }), f("workingDirectory", "工作目录", { required: true }), f("command", "构建命令", { required: true, type: "textarea" }), f("artifactPath", "构建产物路径", { required: true })],
  "测试": [f("environment", "测试环境 / 镜像引用", { required: true }), f("workingDirectory", "工作目录", { required: true }), f("command", "测试命令", { required: true, type: "textarea" }), f("reportPath", "测试报告路径", { required: true }), f("passCriteria", "测试通过标准", { required: true, type: "textarea" })],
  "制品归档": [f("artifactPath", "制品来源路径 / 引用", { required: true }), f("artifactName", "制品名称", { required: true }), f("artifactVersion", "固定制品版本", { required: true }), f("checksum", "校验值 / 上游校验值引用", { required: true }), f("retentionDays", "保留天数", { type: "number", required: true })],
  "部署 / 回滚": [f("deployMode", "操作类型", { options: ["部署", "回滚"] }), f("environment", "目标环境", { required: true }), f("artifactRef", "固定制品引用（含版本）", { required: true }), f("approver", "部署确认 Human", { required: true, resource: "humans" }), f("command", "部署 / 回滚命令", { required: true, type: "textarea" }), f("healthCheck", "健康检查 / 成功条件", { required: true, type: "textarea" }), f("rollbackRef", "可回滚的固定版本", { required: true, when: d => d.deployMode === "部署" }), f("credential", "部署凭据引用（可选）")],
  "人工审批": [f("approver", "批准 Human", { required: true, resource: "humans" }), f("approvalBasis", "审批依据 / 固定成果", { required: true, type: "textarea" }), f("rejectionPolicy", "驳回处理", { options: ["暂停并退回负责人", "终止本次流程"] })],
  "独立检查": [f("reviewer", "独立检查 Human", { required: true, resource: "humans" }), f("checklist", "独立检查清单", { required: true, type: "textarea" }), f("evidence", "必需证据 / 报告", { required: true, type: "textarea" })],
  "条件分支": [f("condition", "条件表达式（仅配置，不求值）", { required: true, type: "textarea" }), f("missingInput", "输入缺失时", { options: ["暂停并通知负责人", "走否分支"] })],
  "并行汇合": [f("join", "汇合策略", { options: ["全部必需输入到齐", "任一可选输入到齐"] }), f("joinKeys", "必需输入键 / 去重键", { required: true, type: "textarea" })],
  "等待": [f("waitMode", "等待方式", { options: ["事件", "时长"] }), f("waitFor", "事件名称 / 匹配条件", { required: true, when: d => d.waitMode === "事件" }), f("waitSeconds", "等待秒数", { required: true, type: "number", when: d => d.waitMode === "时长" })],
  "HTTP 请求": [f("method", "请求方法", { options: ["GET", "POST", "PUT", "PATCH", "DELETE"] }), f("url", "HTTP 地址", { required: true }), f("credential", "凭据引用（可选）"), f("headers", "非机密请求头（JSON）", { type: "textarea", json: true }), f("body", "请求体映射（JSON）", { type: "textarea", json: true, when: d => !["GET", "DELETE"].includes(d.method) }), f("expectedStatus", "预期 HTTP 状态码", { required: true, type: "number" })],
  "数据转换": [f("transformMode", "转换方式", { options: ["字段映射", "JSON 模板", "过滤与整理"] }), f("mapping", "转换规则 / 映射", { required: true, type: "textarea" }), f("outputSchema", "输出结构（JSON）", { required: true, type: "textarea", json: true })],
  "交付": [f("approver", "最终接收 Human", { required: true, resource: "humans" }), f("deliveryPackage", "固定交付包 / 验收证据", { required: true, type: "textarea" }), f("receiptPolicy", "接收方式", { options: ["Human 明确接收", "Human 验收后接收"] })],
};
// Optional extensions preserve the required fields and defaults of existing definitions.
const extra = (kind, fields) => workflowNodeFields[kind].push(...fields);
const json = (key, label, options = {}) => f(key, label, { type: "textarea", json: true, ...options });
const numeric = (key, label, min, max, options = {}) => f(key, label, { type: "number", min, max, ...options });
const credential = (key, label, options = {}) => f(key, label, { credential: true, help: "只保存 vault/…、credential/… 或 secret/… 引用；不会读取或授予凭据。", ...options });
extra("触发", [
  f("webhookPath", "Webhook 路径定义", { group: "事件来源", when: d => d.triggerMode === "Webhook", help: "只定义本地路径，不创建真实监听地址。" }),
  credential("webhookCredential", "Webhook 验签凭据引用", { group: "事件来源", when: d => d.triggerMode === "Webhook" }),
  f("conversationEvent", "会话事件类型", { group: "事件来源", options: ["消息明确转为工作", "Human 确认草稿", "指定提及"], when: d => d.triggerMode === "会话事件" }),
  f("conversationScope", "会话事件范围", { group: "事件来源", when: d => d.triggerMode === "会话事件", help: "指定频道 / 工作引用；事件不自动取得会话访问权。" }),
  json("eventFilter", "事件过滤条件（JSON）", { group: "事件筛选" }),
  f("eventIdKey", "事件去重键映射", { group: "事件筛选" }),
]);
extra("AI 任务", [
  f("systemPrompt", "System 指令", { type: "textarea", group: "提示词与上下文" }),
  f("userPrompt", "User 输入模板", { type: "textarea", group: "提示词与上下文", help: "仅声明变量映射；不解析表达式或调用模型。" }),
  numeric("temperature", "温度", 0, 2, { group: "生成约束", step: "0.1" }),
  numeric("maxOutputTokens", "输出 Token 上限", 1, Number.MAX_SAFE_INTEGER, { integer: true, group: "生成约束" }),
  f("responseFormat", "输出格式", { group: "生成约束", options: ["文本", "JSON 对象", "结构化 JSON"] }),
  json("responseSchema", "模型输出 Schema（受支持子集）", { group: "生成约束", schema: true, when: d => d.responseFormat === "结构化 JSON", help: "仅校验 type / properties / required / items / additionalProperties / enum；不声称兼容完整 JSON Schema。" }),
]);
for (const kind of ["源码检出", "构建", "测试", "制品归档", "部署 / 回滚"]) extra(kind, [
  f("runnerRef", "Runner / 执行节点引用", { resource:"nodes", empty:"由任务运行时选择执行节点", group: "执行环境", help: "定义目标引用；不表示节点在线、已授权或已接入。" }),
  json("environmentVariables", "非机密环境变量（JSON）", { group: "执行环境", stringMap: true }),
  json("environmentCredentials", "环境变量凭据引用（JSON）", { group: "执行环境", credentialMap: true, help: "例如 {\"API_TOKEN\":\"vault/ci/api\"}；值只能是凭据引用。" }),
]);
for (const kind of ["构建", "测试"]) extra(kind, [
  f("cacheKey", "缓存键 / 固定依赖映射", { group: "缓存" }),
  f("cachePaths", "缓存路径（每行一个）", { group: "缓存", type: "textarea" }),
  f("cachePolicy", "缓存策略", { group: "缓存", options: ["禁用", "只读恢复", "成功后写入"] }),
]);
extra("部署 / 回滚", [
  f("deploymentStrategy", "部署策略定义", { group: "发布策略", options: ["滚动", "蓝绿", "金丝雀", "手动切换"] }),
  numeric("canaryPercent", "金丝雀流量百分比", 1, 100, { group: "发布策略", integer: true, when: d => d.deploymentStrategy === "金丝雀" }),
  numeric("healthTimeout", "健康检查超时（秒）", 1, Number.MAX_SAFE_INTEGER, { group: "发布策略", integer: true }),
  f("rollbackPolicy", "异常回滚策略定义", { group: "发布策略", options: ["暂停并由 Human 核实", "按已批准回滚方案请求确认"] }),
]);
extra("人工审批", [
  f("approvalMode", "审批阶段定义", { group: "审批规则草案", options: ["顺序", "并行"], help: "仅工作流定义；不创建正式审批、不记录投票或授予审批权。" }),
  f("approvalStrategy", "通过策略定义", { group: "审批规则草案", options: ["ALL", "ANY", "QUORUM"] }),
  json("approvalParticipants", "审批参与者 / 角色定义（JSON）", { group: "审批规则草案", help: "例如 {\"humanIds\":[\"H-lin\"],\"roles\":[\"项目负责人\"]}；正式审批须另行冻结实际名单。" }),
  numeric("approvalQuorum", "法定人数定义", 1, Number.MAX_SAFE_INTEGER, { integer: true, group: "审批规则草案", when: d => d.approvalStrategy === "QUORUM" }),
  numeric("approvalTimeout", "审批超时（秒）", 1, Number.MAX_SAFE_INTEGER, { integer: true, group: "超时与升级" }),
  f("approvalTimeoutAction", "超时处理定义", { group: "超时与升级", options: ["提醒并保持待处理", "请求负责人升级", "到期终止等待"] }),
  f("approvalDelegate", "代理 Human 定义", { group: "超时与升级", resource: "humans" }),
  f("approvalEscalation", "升级接收 Human 定义", { group: "超时与升级", resource: "humans" }),
]);
workflowNodeFields["容器镜像"] = [
  f("imageName", "镜像仓库 / 名称", { required: true }), f("imageTag", "固定镜像标签 / commit 映射", { required: true }),
  f("dockerfile", "Dockerfile 路径", { required: true }), f("buildContext", "构建上下文", { required: true }),
  json("buildArguments", "非机密构建参数（JSON）", { stringMap: true, group: "构建参数" }),
  credential("registryCredential", "镜像仓库凭据引用", { group: "镜像仓库" }),
  f("imagePushPolicy", "推送策略定义", { options: ["仅构建", "请求 Human 确认后推送"], group: "镜像仓库" }),
];
workflowNodeFields["安全扫描"] = [
  f("scanTarget", "固定扫描对象 / 版本引用", { required: true }),
  f("scanMode", "扫描类型", { required: true, options: ["源码", "依赖", "镜像", "机密泄漏"] }),
  f("scannerRef", "扫描器 / 配置版本引用", { required: true }),
  f("severityThreshold", "阻断严重度", { required: true, options: ["严重", "高", "中", "低"], group: "扫描门禁" }),
  f("reportPath", "扫描报告路径", { required: true, group: "扫描门禁" }),
];
workflowNodeFields["通知"] = [
  f("notificationChannel", "通知渠道定义", { required: true, options: ["站内", "会话", "邮件连接器", "Webhook 连接器"] }),
  f("notificationRecipients", "明确接收范围 / 收件人引用", { required: true }),
  f("notificationTemplate", "通知正文模板", { required: true, type: "textarea", help: "只保存模板，不发送消息；对外承诺仍需要 Human 确认。" }),
  f("notificationWhen", "发送条件定义", { options: ["成功", "失败", "等待 Human", "总是"], group: "触发条件" }),
  credential("notificationCredential", "通知连接器凭据引用", { group: "连接器" }),
];
workflowNodeFields["子工作流"] = [
  f("childTemplateId", "子工作流模板", { required: true, resource: "templates" }),
  numeric("childVersion", "固定模板版本", 1, Number.MAX_SAFE_INTEGER, { required: true, integer: true }),
  json("childInputs", "子工作流输入映射（JSON）", { required: true, group: "子流程契约" }),
  json("childOutputs", "子工作流输出映射（JSON）", { required: true, group: "子流程契约" }),
  f("childWaitPolicy", "子流程等待策略", { options: ["等待完成并接收固定输出", "等待 Human 确认接续"], group: "子流程契约" }),
];
for (const kind of ["容器镜像", "安全扫描"]) extra(kind, workflowNodeFields["构建"].filter(field => field.group === "执行环境").map(field => ({ ...field })));
export const workflowInputFields = [
  json("inputSchema", "输入 Schema（受支持子集）", { group: "输入校验", schema: true, help: "支持 type / properties / required / items / additionalProperties / enum；不解析 $ref 或执行表达式。" }),
  json("pinnedInput", "固定输入样例（JSON）", { group: "固定样例", help: "样例仅用于本地结构校验；不替代真实上游数据。" }),
  f("pinnedInputVersion", "样例来源固定版本", { group: "固定样例" }),
];
export const workflowErrorFields = [
  f("retrySafety", "重试安全约束定义", { group: "幂等与副作用", options: ["仅未启动的安全操作", "按已核实的幂等结果处理", "禁止自动重试"] }),
  f("idempotencyKey", "幂等键映射", { group: "幂等与副作用", help: "填写稳定工作 / 动作 / 版本映射；不在原型中发起重放。" }),
  f("unknownEffectPolicy", "未知副作用处置定义", { group: "幂等与副作用", options: ["暂停并核实", "暂停并请求补偿确认"], help: "已启动且结果未知时不得自动重放；此处仅定义处置方式。" }),
  f("compensationRef", "补偿方案固定引用", { group: "幂等与副作用" }),
];
export function groupsForWorkflowNode(node) {
  const groups = [];
  for (const field of fieldsForWorkflowNode(node)) {
    let group = groups.find(item => item.label === field.group);
    if (!group) { group = { label: field.group, fields: [] }; groups.push(group); }
    group.fields.push(field);
  }
  return groups;
}
// Collaboration and construction share the same workflow definition. These nodes
// describe contracts only; they do not create tasks, approvals or acceptance facts.
workflowNodeLibrary.push(
  ["任务分派", "Users", "协作与交接", "固定任务契约、Human 责任与既有执行小团队分工"],
  ["材料交接", "PackageCheck", "协作与交接", "固定工作包与证据，沿用一次 Human 接收（L3）"],
  ["退回重做", "Undo2", "协作与交接", "指定上游、返工依据与重验要求，保留累计次数"],
  ["需求变更", "FilePenLine", "协作与交接", "固定变更依据、影响声明与 Human 确认后的同步安排"],
);
workflowNodeFields["任务分派"] = [
  f("taskContractRef", "任务契约引用", { required: true, group: "固定任务契约", help: "填写现有工作 / 任务契约引用；节点不创建新的工作事实。" }),
  f("taskContractVersion", "任务契约固定版本", { required: true, group: "固定任务契约" }),
  f("assignmentMode", "分派方式定义", { required: true, group: "责任与分工", options: ["负责人执行", "按已选小团队分工"], help: "Human 负责人及执行身份沿用本节点 owner / executorIds，不额外授予权限。" }),
  f("assignmentPlan", "分工与责任边界", { required: true, type: "textarea", group: "责任与分工", help: "说明谁制作、谁协助；必需内部验收继续在输出契约中声明。" }),
];
workflowNodeFields["材料交接"] = [
  f("handoffPackageRef", "固定工作包引用", { required: true, group: "交接依据" }),
  f("handoffPackageVersion", "工作包固定版本", { required: true, group: "交接依据" }),
  f("handoffEvidence", "上游固定证据引用", { required: true, type: "textarea", group: "交接依据", help: "逐项注明检查报告、输入和成果版本；不复制未授权材料。" }),
  f("receiver", "下游 Human 接收人", { required: true, resource: "humans", group: "Human 接收" }),
  f("handoffAcceptance", "接收定义", { required: true, options: ["沿用固定包的一次 Human 接收（L3）"], group: "Human 接收", help: "复用同一工作包、版本与接收人的接收决定，不增加二次接受或伪造接收成功。" }),
];
workflowNodeFields["退回重做"] = [
  f("reworkTargetNode", "目标上游节点 ID", { required: true, group: "返工范围", help: "须为通过现有连接可追溯的上游节点；退回是业务定义，不创建画布循环。" }),
  f("reworkSourceRef", "固定返工来源 / 成果引用", { required: true, group: "返工范围" }),
  f("reworkSourceVersion", "返工来源固定版本", { required: true, group: "返工范围" }),
  f("reworkReason", "返工原因与未满足条款", { required: true, type: "textarea", group: "重做与重验" }),
  f("revalidationRequirements", "重验要求与必需证据", { required: true, type: "textarea", group: "重做与重验" }),
  f("reworkAccounting", "轮次处理定义", { required: true, options: ["沿用原工作轮次与上限，达到阈值由 Human 决定"], group: "重做与重验", help: "不新增计数器，不因换人、换节点或本节点配置清零内部修复 / 正式返工累计。" }),
];
workflowNodeFields["需求变更"] = [
  f("changeRef", "已提出的需求变更引用", { required: true, group: "固定变更依据" }),
  f("changeVersion", "变更固定版本", { required: true, group: "固定变更依据" }),
  f("changeImpact", "影响对象与旧新基线声明", { required: true, type: "textarea", group: "影响与同步", help: "注明受影响工作、要求、成果、依赖及计划；声明本身不修改这些对象。" }),
  f("changeConfirmer", "变更确认 Human 定义", { required: true, resource: "humans", group: "影响与同步", help: "指定需要确认的 Human；工作流发布不等于业务变更已批准。" }),
  f("changeSyncPlan", "确认后的同步与重新验收安排", { required: true, type: "textarea", group: "影响与同步", help: "说明哪些步骤须暂停、计划如何同步、哪些检查失效及如何重验；保留旧新版本关系。" }),
];

// Compose with validateWorkflowNodeConfig; existing field validation owns required
// contracts and current Human identity checks. This helper only validates graph/team relations.
export function validateWorkflowCollaboration(graph) {
  const issues = [];
  for (const node of graph.nodes) {
    const d = node.data;
    const fail = (field, message) => issues.push({ node: node.id, field, message: `${d.label || node.id}：${message}` });
    if (d.kind === "任务分派" && d.assignmentMode === "按已选小团队分工" && (!Array.isArray(d.executorIds) || !d.executorIds.length)) fail("executorIds", "小团队分工须明确选择至少一位执行身份。");
    if (d.kind !== "退回重做" || typeof d.reworkTargetNode !== "string" || !d.reworkTargetNode.trim()) continue;
    const upstream = new Set(), pending = [node.id];
    while (pending.length) {
      const target = pending.pop();
      for (const edge of graph.edges.filter(edge => edge.target === target)) {
        if (!upstream.has(edge.source)) { upstream.add(edge.source); pending.push(edge.source); }
      }
    }
    if (d.reworkTargetNode === node.id || !graph.nodes.some(candidate => candidate.id === d.reworkTargetNode) || !upstream.has(d.reworkTargetNode)) fail("reworkTargetNode", "返工目标须为现有上游节点，不能指向自己、下游或无关节点。");
  }
  return issues;
}

export function workflowKind(node) { return node.data.kind || (node.id === "brief" ? "触发" : node.id === "delivery" ? "交付" : "AI 任务"); }
export function normalizeWorkflowNode(node) {
  const kind = workflowKind(node);
  const defaults = { timeout: "300", retries: "2", onError: "暂停并通知负责人", retryDelay: "10", inputs: "", inputVersion: "", output: "固定成果与检查证据", gate: "必需内部检查通过", executorIds: [], triggerMode: "手动", gitEvent: "push", timezone: "UTC", runtime: "Codex CLI", method: "GET", expectedStatus: "200", headers: "{}", toolArguments: "{}", deployMode: "部署", checkoutPath: ".", workingDirectory: ".", retentionDays: "30", waitMode: "事件", join: "全部必需输入到齐", transformMode: "字段映射", missingInput: "暂停并通知负责人", rejectionPolicy: "暂停并退回负责人", receiptPolicy: "Human 明确接收" };
  const kindDefaults = { "容器镜像": { dockerfile: "Dockerfile", buildContext: ".", imagePushPolicy: "仅构建" }, "安全扫描": { scanMode: "源码", severityThreshold: "高" }, "通知": { notificationChannel: "站内", notificationWhen: "失败" }, "子工作流": { childWaitPolicy: "等待完成并接收固定输出" } };
  return { ...node, type: "task", data: { ...defaults, ...(kindDefaults[kind] || {}), ...node.data, ...(kind === "源码检出" && !node.data.sourceMode ? {sourceMode:"关联仓库"} : {}), kind } };
}
export function fieldsForWorkflowNode(node) { return (workflowNodeFields[workflowKind(node)] || []).filter(field => !field.when || field.when(node.data)); }
function workflowHuman(state, value) {
  const humans = (state.organization?.members || []).filter(person => person.kind === "Human");
  const byId = humans.find(person => person.id === value);
  if (byId) return byId;
  const named = humans.filter(person => person.name === value);
  return named.length === 1 ? named[0] : null;
}
export function workflowHumanValue(state, value) { return workflowHuman(state, value)?.id || value || ""; }
export function workflowHumanLabel(state, value) { return workflowHuman(state, value)?.name || (value ? `${value} · 身份待确认` : "未指定 Human"); }
export function workflowResourceOptions(state, resource) {
  const humans = (state.organization?.members || []).filter(person => person.kind === "Human");
  if (resource === "nodes") return (state.nodes || []).map(item => ({value:item.id,label:`${item.name} · ${item.os} · ${item.status}`}));
  if (resource === "humans") return humans.map(person => ({ value: person.id, label: `${person.name} · ${person.id}` }));
  let rows = resource === "skills" || resource === "mcps" ? (state.resources?.tools || []).filter(item => item.kind === (resource === "skills" ? "Skill" : "MCP")) : state.resources?.[resource] || [];
  if (["tools", "skills", "mcps"].includes(resource)) rows = rows.filter(item => item.status === "已授权");
  if (resource === "models") rows = rows.filter(item => item.status === "生效");
  if (resource === "repositories") rows = rows.filter(item => !item.archivedAt);
  return rows.map(item => ({ value: item.id, label: `${item.name} · ${item.version || item.id}` }));
}
const credentialReference = value => typeof value === "string" && /^(vault|credential|secret)\/[\w./-]+$/.test(value);
// A deliberately bounded local Schema subset; unsupported keywords are rejected, never silently accepted.
export function schemaProblem(schema, depth = 0) {
  if (depth > 16) return "Schema 嵌套超过 16 层。";
  if (!schema || typeof schema !== "object" || Array.isArray(schema)) return "Schema 须为对象。";
  if (Object.keys(schema).some(key => !["type", "properties", "required", "items", "additionalProperties", "enum"].includes(key))) return "Schema 包含当前原型不支持的关键字。";
  if (!["object", "array", "string", "number", "integer", "boolean", "null"].includes(schema.type)) return "Schema 须声明受支持的 type。";
  if ("enum" in schema && (!Array.isArray(schema.enum) || !schema.enum.length)) return "Schema enum 须为非空数组。";
  if (schema.type !== "object" && ["properties", "required", "additionalProperties"].some(key => key in schema)) return "只有 object Schema 可声明 properties / required / additionalProperties。";
  if (schema.type !== "array" && "items" in schema) return "只有 array Schema 可声明 items。";
  if ("additionalProperties" in schema && typeof schema.additionalProperties !== "boolean") return "additionalProperties 仅支持布尔值。";
  if ("properties" in schema && (!schema.properties || typeof schema.properties !== "object" || Array.isArray(schema.properties))) return "Schema properties 须为对象。";
  if ("required" in schema && (!Array.isArray(schema.required) || schema.required.some(key => typeof key !== "string" || !Object.hasOwn(schema.properties || {}, key)) || new Set(schema.required).size !== schema.required.length)) return "Schema required 须唯一引用已声明字段。";
  for (const child of Object.values(schema.properties || {})) { const problem = schemaProblem(child, depth + 1); if (problem) return problem; }
  if ("items" in schema) return schemaProblem(schema.items, depth + 1);
  return "";
}
export function sampleMatchesSchema(value, schema) {
  if(typeof value === "number" && !Number.isFinite(value)) return false;
  const actual = value === null ? "null" : Array.isArray(value) ? "array" : typeof value;
  if (schema.type === "integer" ? !Number.isSafeInteger(value) : actual !== schema.type) return false;
  if (schema.enum && !schema.enum.some(entry => JSON.stringify(entry) === JSON.stringify(value))) return false;
  if (schema.type === "object") {
    if ((schema.required || []).some(key => !Object.hasOwn(value, key))) return false;
    if (schema.additionalProperties === false && Object.keys(value).some(key => !Object.hasOwn(schema.properties || {}, key))) return false;
    return Object.entries(schema.properties || {}).every(([key, child]) => !Object.hasOwn(value, key) || sampleMatchesSchema(value[key], child));
  }
  return schema.type !== "array" || !schema.items || value.every(item => sampleMatchesSchema(item, schema.items));
}
export const finiteJSON = value => typeof value === "number" ? Number.isFinite(value) : value && typeof value === "object" ? Object.values(value).every(finiteJSON) : true;
const nonempty = value => typeof value === "string" && value.trim().length > 0;
export function validateWorkflowNodeConfig(graph, state) {
  const issues = [];
  const humans = state.organization?.members?.filter(person => person.kind === "Human") || [];
  for (const raw of graph.nodes) {
    const node = normalizeWorkflowNode(raw), d = node.data;
    const fail = (field, message) => issues.push({ node: node.id, field, message: `${d.label || node.id}：${message}` });
    if (!workflowNodeFields[d.kind]) { fail("kind", "不支持的节点类型。"); continue; }
    if (!workflowHuman(state, d.owner)) fail("owner", "请选择当前租户的 Human 负责人。");
    if (!Array.isArray(d.executorIds) || d.executorIds.some(memberId => !humans.some(person => person.id === memberId) && !state.agents?.some(agent => `agent:${agent.id}` === memberId && !["退役", "已退役", "待批准"].includes(agent.status)))) fail("executorIds", "执行小团队包含不可用身份。");
    for (const field of [...fieldsForWorkflowNode(node), ...workflowInputFields, ...workflowErrorFields, ...(!fieldsForWorkflowNode(node).some(field => field.key === "outputSchema") ? [json("outputSchema", "输出结构（JSON）")] : [])]) {
      const value = d[field.key];
      if (field.required && !nonempty(value)) fail(field.key, `请填写${field.label}。`);
      if (nonempty(value) && field.resource && !workflowResourceOptions(state, field.resource).some(option => option.value === (field.resource === "humans" ? workflowHumanValue(state, value) : value))) fail(field.key, `${field.label}已不可用，请重新选择。`);
      if (value != null && value !== "" && typeof value !== "string") fail(field.key, `${field.label}须为文本配置。`);
      if (field.options && (field.required || value != null && value !== "") && !field.options.includes(value)) fail(field.key, `${field.label}选项无效。`);
      if (field.type === "number" && nonempty(value) && (!/^-?(?:0|[1-9]\d*)(?:\.\d+)?$/.test(value) || !Number.isFinite(Number(value)) || (field.integer && !Number.isSafeInteger(Number(value))) || (field.min != null && Number(value) < field.min) || (field.max != null && Number(value) > field.max))) fail(field.key, `${field.label}数值或范围无效。`);
      if (field.credential && nonempty(value) && !credentialReference(value)) fail(field.key, `${field.label}只能保存凭据引用。`);
      if (field.json && nonempty(value)) { try { const parsed = JSON.parse(value); if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) || !finiteJSON(parsed)) throw Error(); } catch { fail(field.key, `${field.label}须为 JSON 对象。`); } }
    }
    for (const field of [...fieldsForWorkflowNode(node), ...workflowInputFields].filter(field => field.json && nonempty(d[field.key]))) {
      let value; try { value = JSON.parse(d[field.key]); } catch { continue; }
      if (!value || typeof value !== "object" || Array.isArray(value)) continue;
      if (field.credentialMap && Object.entries(value).some(([key, ref]) => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) || !credentialReference(ref))) fail(field.key, "环境凭据须为变量名到凭据引用的映射。");
      if (field.stringMap && Object.entries(value).some(([key, entry]) => typeof entry !== "string" || /(?:token|secret|password|api[_-]?key|authorization)/i.test(key))) fail(field.key, "非机密变量须为字符串映射；机密变量改用凭据引用。");
      if (field.schema) { const problem = schemaProblem(value); if (problem) fail(field.key, problem); }
    }
    if (nonempty(d.pinnedInput)) {
      if (!nonempty(d.pinnedInputVersion)) fail("pinnedInputVersion", "固定样例须记录来源版本。");
      if (nonempty(d.inputSchema)) try { const schema = JSON.parse(d.inputSchema), sample = JSON.parse(d.pinnedInput); if (!schemaProblem(schema) && !sampleMatchesSchema(sample, schema)) fail("pinnedInput", "固定样例与输入 Schema 不匹配。"); } catch { /* JSON issue already reported. */ }
    }
    if (d.retrySafety === "按已核实的幂等结果处理" && !nonempty(d.idempotencyKey)) fail("idempotencyKey", "按幂等结果处理须声明稳定幂等键。");
    if (d.retrySafety === "禁止自动重试" && d.onError === "有限重试后暂停") fail("retrySafety", "禁止自动重试与有限重试策略冲突。");
    if (d.unknownEffectPolicy === "暂停并请求补偿确认" && !nonempty(d.compensationRef)) fail("compensationRef", "请求补偿须声明固定补偿方案。");
    if (d.webhookPath && (typeof d.webhookPath !== "string" || !/^\/(?!\/)[A-Za-z0-9/_-]*$/.test(d.webhookPath))) fail("webhookPath", "Webhook 定义须为本地路径，不填写远程 URL 或凭据。");
    if (d.kind === "子工作流" && nonempty(d.childVersion)) {
      const template = state.resources?.templates?.find(item => item.id === d.childTemplateId), flow = template?.flow, version = Number(d.childVersion);
      if (!flow || !Number.isSafeInteger(version) || !(flow.flowVersion === version && version > 0 || flow.flowVersions?.[version])) fail("childVersion", "子工作流固定版本不存在，不能代用最新定义。");
    }
    if (nonempty(d.approvalParticipants)) try {
      const participants = JSON.parse(d.approvalParticipants);
      if (!participants || typeof participants !== "object" || Array.isArray(participants) || Object.keys(participants).some(key => !["humanIds", "roles"].includes(key)) || ["humanIds", "roles"].some(key => participants[key] != null && (!Array.isArray(participants[key]) || participants[key].some(item => !nonempty(item)) || new Set(participants[key]).size !== participants[key].length)) || (participants.humanIds || []).some(id => !humans.some(human => human.id === id))) fail("approvalParticipants", "参与者定义只接受当前 Human ID 数组与明确角色名称数组。");
      else if (d.approvalStrategy === "QUORUM" && nonempty(d.approvalQuorum) && !(participants.roles || []).length && Number(d.approvalQuorum) > (participants.humanIds || []).length) fail("approvalQuorum", "法定人数不能超过已明确的参与 Human 数量。");
    } catch { /* JSON issue already reported. */ }
    if (d.inputs?.trim() && !nonempty(d.inputVersion)) fail("inputVersion", "填写输入引用时须明确固定版本 / 固定版本映射。");
    if (d.receiver && !workflowHuman(state, d.receiver)) fail("receiver", "下游接收人须是当前租户 Human。");
    if (d.credential && !/^(vault|credential|secret)\/[\w./-]+$/.test(d.credential)) fail("credential", "仅填写凭据引用，不能保存明文密钥。");
    if (d.kind === "源码检出" && d.sourceMode === "远程地址" && nonempty(d.sourceURL)) {
      try {const url=new URL(d.sourceURL);if (!["https:","http:","ssh:"].includes(url.protocol) || !url.hostname || url.password || url.search || (url.protocol !== "ssh:" && url.username)) throw Error();} catch {fail("sourceURL","Git 地址须为 HTTP(S) / SSH 地址，凭据使用单独引用。");}
    }
    if (d.kind === "HTTP 请求") {
      try { const url = new URL(d.url); if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || [...url.searchParams.keys()].some(key => /^(token|api[_-]?key|secret|password|access_token)$/i.test(key))) throw Error(); } catch { fail("url", "HTTP 地址须有效，且不得内嵌凭据。"); }
      try { if (Object.keys(JSON.parse(d.headers || "{}")).some(key => /^(authorization|proxy-authorization|cookie|x-api-key)$/i.test(key))) fail("headers", "认证信息请使用凭据引用，不放入请求头。"); } catch { /* JSON error reported above. */ }
      if (!/^\d{3}$/.test(d.expectedStatus) || Number(d.expectedStatus) < 100 || Number(d.expectedStatus) > 599) fail("expectedStatus", "HTTP 状态码须为 100–599。");
    }
    if (d.kind === "触发" && d.triggerMode === "定时") {
      if (!validCron(d.cron)) fail("cron", "请使用有效的五段 Cron（分 时 日 月 周）。");
      try { new Intl.DateTimeFormat("en", { timeZone: d.timezone }).format(); } catch { fail("timezone", "时区无效。"); }
    }
    for (const key of ["retryDelay", ...(d.kind === "制品归档" ? ["retentionDays"] : []), ...(d.kind === "等待" && d.waitMode === "时长" ? ["waitSeconds"] : [])]) if (!/^\d+$/.test(d[key] || "") || Number(d[key]) <= 0) fail(key, "时长 / 保留天数须为正整数。");
    if (!["暂停并通知负责人", "有限重试后暂停", "终止本次流程"].includes(d.onError)) fail("onError", "失败策略无效。");
    if (d.onError === "有限重试后暂停" && Number(d.retries) < 1) fail("retries", "有限重试至少需要一次重试。");
  }
  return issues;
}
// Adapt only kinds for the existing graph validator, so its reachability and quality gates stay authoritative.
export function workflowGraphForValidation(graph) {
  const producers = new Set(["源码检出", "构建", "测试", "制品归档", "部署 / 回滚", "容器镜像", "安全扫描", "通知", "子工作流", "任务分派", "材料交接", "退回重做", "需求变更"]);
  return { ...graph, nodes: graph.nodes.map(node => producers.has(workflowKind(node)) ? { ...node, data: { ...node.data, kind: "数据转换" } } : node) };
}
function validCron(value) {
  const parts = String(value || "").trim().split(/\s+/), ranges = [[0,59],[0,23],[1,31],[1,12],[0,7]];
  return parts.length === 5 && parts.every((part, index) => part.split(",").every(term => {
    const match = /^(\*|\d+(?:-\d+)?)(?:\/(\d+))?$/.exec(term); if (!match || (match[2] && Number(match[2]) < 1)) return false;
    if (match[1] === "*") return true;
    const [low, high = low] = match[1].split("-").map(Number), [min, max] = ranges[index];
    return low >= min && high <= max && high >= low;
  }));
}
export function simulateWorkflowNode(node, graph, state, now = new Date().toISOString(), structuralIssues = []) {
  const issues = [...structuralIssues, ...validateWorkflowNodeConfig(graph, state), ...validateWorkflowSettings(graph), ...validateWorkflowCollaboration(graph)].filter(issue => issue.node === node.id);
  const configuration = structuredClone(node.data);
  for (const issue of issues) {
    if (issue.field?.startsWith("workflowSettings.")) {
      if (configuration.workflowSettings) configuration.workflowSettings[issue.field.split(".")[1]] = "[无效配置不加入预览记录]";
    } else if (issue.field && Object.hasOwn(configuration, issue.field)) configuration[issue.field] = "[无效配置不加入预览记录]";
  }
  return { time: now, nodeId: node.id, kind: workflowKind(node), mode: "LOCAL_SIMULATION", status: issues.length ? "配置未通过" : "配置预览完成 · 未执行", configuration, input: { upstream: graph.edges.filter(edge => edge.target === node.id).map(edge => edge.source), mapping: node.data.inputs || "尚未声明输入映射", pinnedSample: issues.some(issue => issue.field === "pinnedInput") ? null : node.data.pinnedInput || null, pinnedVersion: node.data.pinnedInputVersion || null, fixedVersion: node.data.inputVersion || "未指定" }, output: { contract: node.data.output, acceptance: node.data.gate, artifact: null, note: "未创建真实制品、测试报告、部署或验收证据。" }, issues: issues.map(issue => issue.message) };
}

// Workflow-level definition settings live on the entry node, so existing fixed-version
// snapshots cover them without introducing a second publishing contract.
export const workflowSettingsDefaults = {
  name: "", scenario: "协作交付", description: "", stages: "", concurrency: "1",
  duplicatePolicy: "排队等待", concurrencyKey: "", timeout: "3600",
  versionPolicy: "保留原固定版本", unknownEffect: "暂停并核实，禁止自动重放",
  retentionDays: "30", variables: "{}", credentialRefs: "{}",
  notificationEvents: "失败 / 阻塞 / 待审批", notificationTarget: "",
};
export function validateWorkflowSettings(graph) {
  const entry = graph.nodes.find(node => node.id === "brief");
  const settings = entry?.data.workflowSettings;
  if (!settings) return [];
  const d = { ...workflowSettingsDefaults, ...settings }, issues = [];
  const fail = (key, message) => issues.push({node:"brief", field:`workflowSettings.${key}`, message:`流程设置：${message}`});
  for (const key of ["concurrency", "timeout", "retentionDays"]) {
    if (!/^\d+$/.test(String(d[key])) || Number(d[key]) < 1 || !Number.isSafeInteger(Number(d[key]))) fail(key, `${key === "concurrency" ? "并行运行数" : key === "timeout" ? "流程超时秒数" : "保留天数"}须为正整数。`);
  }
  for (const message of validateLoopBoundary(d.loopBoundary)) fail("loopBoundary", message);
  const choices = {
    scenario: ["协作交付", "构建与发布", "综合协作", "业务自动化", "Git CI/CD", "混合流程"],
    duplicatePolicy: ["排队等待", "拒绝重复触发", "请求 Human 决定"],
    versionPolicy: ["保留原固定版本", "暂停后申请迁移"],
    unknownEffect: ["暂停并核实，禁止自动重放", "暂停并申请补偿"],
    notificationEvents: ["失败 / 阻塞 / 待审批", "开始 / 完成 / 失败 / 阻塞 / 待审批", "仅失败 / 阻塞"],
  };
  for (const [key, values] of Object.entries(choices)) if (!values.includes(d[key])) fail(key, "请选择有效策略。");
  for (const key of ["variables", "credentialRefs"]) {
    try {
      const value = JSON.parse(d[key]);
      if (!value || typeof value !== "object" || Array.isArray(value) || !finiteJSON(value)) throw Error();
      if (key === "credentialRefs" && Object.values(value).some(ref => typeof ref !== "string" || !/^(vault|credential|secret)\/[\w./-]+$/.test(ref))) fail(key, "凭据映射只能保存引用，不能保存密钥。");
      if (key === "variables" && containsCredential(value)) fail(key, "机密字段须移入凭据引用映射。");
    } catch {fail(key, `${key === "variables" ? "流程变量" : "凭据引用"}须为 JSON 对象。`);}
  }
  return issues;
}
function containsCredential(value) {
  if (!value || typeof value !== "object") return false;
  return Object.entries(value).some(([key, child]) => /(?:token|api[_-]?key|secret|password|authorization)/i.test(key) || containsCredential(child));
}
