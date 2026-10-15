export function workflowRecordForProject(state, projectId, workflowId) {
  if (!state.projects.some(project => project.id === projectId)) return null;
  return state.projectWorkflows?.[projectId]?.find(record => record.id === workflowId && (!record.tenantId || record.tenantId === state.activeTenant)) || null;
}
// Project workflow identities are independent of their immutable definition versions.
// The legacy singleton is projected as "default"; it is never copied into the registry.
export function workflowRegistryReadProblem(state, projectId) {
  if (!["owner", "executor"].includes(state.role) || !state.organization?.members?.some(person => person.id === "H-lin" && person.kind === "Human")) return "当前 Human 无权访问工作流。";
  if (!state.projects.some(project => project.id === projectId)) return "项目不存在或不属于当前租户。";
  return "";
}
export function workflowRegistryProblem(state, projectId) {
  const readProblem = workflowRegistryReadProblem(state, projectId);
  if (readProblem) return readProblem;
  if (state.offline) return "离线中，工作流只读。";
  if (state.role !== "owner") return "管理工作流需要 Human 负责人权限。";
  if (state.projects.find(project => project.id === projectId).archivedAt) return "项目已归档，工作流只读。";
  return "";
}
export function workflowRegistryRows(state, projectId, defaultFlow) {
  if (workflowRegistryReadProblem(state, projectId)) return [];
  const entry = defaultFlow?.flowNodes?.find(node => node.id === "brief");
  return [{ id: "default", name: entry?.data.workflowSettings?.name?.trim() || "项目交付流程", description: "保留原项目工作流及既有工作的固定版本引用。", revision: 0, history: [], legacy: true, ownerId: entry?.data.owner || "", flow: defaultFlow }, ...(state.projectWorkflows?.[projectId] || []).filter(record => !record.tenantId || record.tenantId === state.activeTenant)];
}
export function workflowFixedGraph(record, version) {
  if (!Number.isSafeInteger(version) || version < 1 || !record?.flow) return null;
  const flow = record.flow;
  const definition = version === flow.flowVersion ? { nodes: flow.flowNodes, edges: flow.flowEdges } : flow.flowVersions?.[version];
  return definition && Array.isArray(definition.nodes) && Array.isArray(definition.edges) ? definition : null;
}
export function workflowLinkedWorks(state, projectId, workflowId = "default", version) {
  if (workflowRegistryReadProblem(state, projectId)) return [];
  if (version !== undefined && (!Number.isSafeInteger(version) || version < 1)) return [];
  return state.work.filter(work => work.project === projectId && (work.workflowId || "default") === workflowId && (version === undefined || work.flowVersion === version));
}
function text(value, label, max) {
  if (typeof value !== "string" || !value.trim() || value.trim().length > max) throw Error(`${label}须为 1–${max} 字。`);
  return value.trim();
}
export function workflowRegistryCommandOn(state, input, { defaultFlow, makeId, now = new Date().toISOString() }) {
  if (input.tenantId !== state.activeTenant) throw Error("租户已切换，请重新打开工作流列表。");
  const problem = workflowRegistryProblem(state, input.projectId);
  if (problem) throw Error(problem);
  const rows = workflowRegistryRows(state, input.projectId, defaultFlow);
  if (!["create", "clone", "rename", "archive"].includes(input.action)) throw Error("未知工作流操作。");
  const reason = text(input.reason, "操作理由", 2000);
  const previous = rows.find(record => record.id === input.id);
  if (["rename", "archive"].includes(input.action)) {
    if (!previous) throw Error("工作流不存在或不属于当前项目。");
    if (previous.id === "default") throw Error("默认旧工作流保留原身份，不在列表中改名或归档。");
    if (previous.revision !== input.revision) throw Error("工作流已变化，请重新打开当前记录。");
    if (!Array.isArray(previous.history)) throw Error("工作流管理历史格式无效，不能覆盖历史。");
    if (previous.archivedAt) throw Error("工作流已归档，旧版本与关联工作只读保留。");
  }
  let name, description;
  if (input.action !== "archive") {
    name = text(input.name, "工作流名称", 120);
    if (rows.some(record => record.id !== previous?.id && record.name.trim().toLowerCase() === name.toLowerCase())) throw Error("当前项目已有同名工作流，请使用可区分的名称。");
    if (input.description != null && (typeof input.description !== "string" || input.description.length > 4000)) throw Error("工作流说明最多 4000 字。");
    description = (input.description || "").trim();
  }
  const event = { action: input.action, reason, actorId: "H-lin", actorKind: "Human", time: now, source: "LOCAL_SIMULATION" };
  if (input.action === "create" || input.action === "clone") {
    if (input.id) throw Error("创建或复制不能覆盖已有工作流。");
    const sourceId = input.action === "create" ? "default" : input.sourceId;
    const source = rows.find(record => record.id === sourceId);
    const initialTemplate = input.action === "create" && source?.flow?.flowVersion === 0 && (input.sourceVersion == null || input.sourceVersion === 0);
    const fixed = initialTemplate ? { nodes: source.flow.flowNodes, edges: source.flow.flowEdges } : workflowFixedGraph(source, input.sourceVersion);
    if (!fixed || !Array.isArray(fixed.nodes) || !fixed.nodes.length || !Array.isArray(fixed.edges)) throw Error("来源固定版本不存在；请先发布来源工作流，不能复制未发布草案或代用最新版本。");
    const graph = structuredClone({ nodes: fixed.nodes, edges: fixed.edges });
    const entry = graph.nodes.find(node => node.id === "brief");
    if (entry?.data.workflowSettings) entry.data.workflowSettings = { ...entry.data.workflowSettings, name, description };
    const workflowId = makeId("WF");
    if (typeof workflowId !== "string" || !workflowId || workflowId === "default" || rows.some(record => record.id === workflowId)) throw Error("工作流标识重复，请重试。");
    const record = { id: workflowId, name, description, tenantId: state.activeTenant, ownerId: "H-lin", revision: 1,
      history: [{ ...event, origin: { workflowId: sourceId, version: initialTemplate ? 0 : input.sourceVersion, ...(initialTemplate ? { kind: "INITIAL_TEMPLATE" } : {}) } }],
      flow: { flowNodes: [], flowEdges: [], flowVersion: 0, flowDraft: graph, flowVersions: {} } };
    state.projectWorkflows ??= {};
    state.projectWorkflows[input.projectId] ??= [];
    state.projectWorkflows[input.projectId].push(record);
    return workflowId;
  }
  const before = { name: previous.name, description: previous.description, archivedAt: previous.archivedAt || null, revision: previous.revision };
  if (input.action === "rename") { previous.name = name; previous.description = description; }
  else { previous.archivedAt = now; previous.archivedBy = "H-lin"; }
  previous.revision++;
  previous.history.push({ ...event, previous: before, next: { name: previous.name, description: previous.description, archivedAt: previous.archivedAt || null, revision: previous.revision } });
  return previous.id;
}
