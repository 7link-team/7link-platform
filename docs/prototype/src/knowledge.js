import { normalizeChatAttachments } from "./chat-content";
import { projectIsArchived, recordProjectId, projectRecordWriteProblem } from "./project-lifecycle";
import { goalReadProblem } from "./goals";
// Local prototype identity mirrors the existing domain actor; this is not server IAM.
const privateScope = record => typeof record?.scope === "string" && record.scope.trim() === "私人";
export function knowledgeOwner(state, record) {
  const people = state.organization?.members || [];
  if (record?.ownerId != null) {
    const person = people.find(person => person.id === record.ownerId && person.kind === "Human");
    const named = people.filter(member => member.kind === "Human" && member.name === record.owner);
    return person && (record.ownerKind == null || record.ownerKind === "Human") &&
      (!named.length || named.some(member => member.id === person.id)) ? person : null;
  }
  const owners = people.filter(person => person.kind === "Human" && person.name === record?.owner);
  return owners.length === 1 ? owners[0] : null;
}
export function knowledgeReadProblem(state, record) {
  if (!["owner", "executor"].includes(state.role)) return "当前身份无权访问资料。";
  if (!record || typeof record !== "object" || Array.isArray(record)) return "资料不存在或无权访问。";
  const originalProblem = libraryOriginalProblem(state, record);
  if (originalProblem) return originalProblem;
  const pending = [record], inspected = new Set();
  while (pending.length) {
    const snapshot = pending.pop();
    if (!snapshot || typeof snapshot !== "object" || Array.isArray(snapshot)) return "资料历史格式无效，不能确认读取权限。";
    if (inspected.has(snapshot)) continue;
    inspected.add(snapshot);
    // resourceCommand records a status event, while saveResource records a full snapshot.
    // Status-only history inherits the already authorized parent's scope; it cannot contain a document.
    if (snapshot !== record && typeof snapshot.status === "string" &&
      Object.keys(snapshot).every(key => ["status", "actor", "reason", "time"].includes(key)) &&
      [snapshot.actor, snapshot.reason, snapshot.time].every(value => value == null || typeof value === "string")) continue;
    if (snapshot !== record && (typeof record.id !== "string" || !record.id || snapshot.id !== record.id))
      return "资料完整历史必须属于同一已保存原件，来源缺失或不一致。";
    const scope = typeof snapshot.scope === "string" ? snapshot.scope.trim() : "";
    if (!["私人", "租户", "当前项目"].includes(scope) && !/^项目 · \S/.test(scope))
      return "资料范围缺失或无法识别，不能按公开资料读取。";
    if (snapshot.tenantId != null && snapshot.tenantId !== state.activeTenant) return "资料不属于当前租户。";
    if (privateScope(snapshot)) {
      const owner = knowledgeOwner(state, snapshot);
      if (!owner || owner.id !== "H-lin") return "私人资料仅限其本人访问；租户负责人身份不自动授予权限。";
    }
    if (snapshot.history != null) {
      if (!Array.isArray(snapshot.history)) return "资料历史格式无效，不能确认读取权限。";
      pending.push(...snapshot.history);
    }
  }
  return "";
}
export function visibleKnowledge(state, { includeRevoked = false, includeArchived = false } = {}) {
  return (state.resources.knowledge || []).filter(record => !knowledgeReadProblem(state, record) && (includeArchived || (!projectIsArchived(state,recordProjectId(state,record)) && !record.library?.archivedAt)) && (includeRevoked || !record.revocation));
}
export function memoryRevocationProblem(state, record) {
  const archived = projectRecordWriteProblem(state,record); if (archived) return archived;
  const problem = knowledgeReadProblem(state, record);
  if (problem) return problem;
  if (state.offline) return "离线中，请恢复连接后操作。";
  if (record.kind !== "记忆") return "此操作只撤销派生记忆，不删除原文件、知识或消息。";
  if (record.revocation) return "记忆已终态撤销，历史保留，不能重新启用。";
  if (record.status !== "有效") return "当前只能撤销已生效记忆；待确认候选请使用现有审核流程。";
  if (knowledgeOwner(state, record)?.id !== "H-lin") return "此入口仅管理当前 Human 自己的记忆；不扩大他人私人资料权限。";
  return "";
}
export function visibleActivityEvents(state) {
  if (!["owner", "executor"].includes(state.role)) return [];
  return (state.events || []).filter(event => {
    const target = typeof event?.target === "string" ? event.target : "";
    if (target.startsWith("/goals/")) { const id=/^\/goals\/([^/?#]+)/.exec(target)?.[1]; return !goalReadProblem(state,state.resources.goals.find(g=>g.id===id)); }
    if (!/^\/(knowledge|materials)(?:[/?#]|$)/.test(target)) return true;
    const sourceId = /^\/(?:knowledge|materials)\/([^/?#]+)(?:[?#].*)?$/.exec(target)?.[1];
    // Legacy collection-only links do not identify an authorized source.
    // Retain original events in storage, but do not expose their private titles/reasons.
    if (!sourceId) return false;
    const source = (state.resources.knowledge || []).find(record => record.id === sourceId);
    return !knowledgeReadProblem(state, source);
  });
}
export function knowledgeWriteProblem(state, record, previous) {
  const archived = projectRecordWriteProblem(state,previous) || projectRecordWriteProblem(state,record); if (archived) return archived;
  if (previous) {
    const problem = knowledgeReadProblem(state, previous);
    if (problem) return problem;
    if (previous.revocation) return "记忆已终态撤销，历史只读；旧表单不能复活原件。";
    if (privateScope(previous) && (!privateScope(record) || knowledgeOwner(state, record)?.id !== knowledgeOwner(state, previous)?.id))
      return "私人资料不能通过普通编辑转移或扩大共享范围；须走授权共享流程。";
  }
  return knowledgeReadProblem(state, record);
}

// Scoped library records remain in resources.knowledge. References never own content.
export function libraryProjectProblem(state, projectId, write = false) {
  const project = state.projects.find(item => item.id === projectId);
  if (!project || project.id === "private") return "项目不存在或不可作为项目库。";
  if (!["owner", "executor"].includes(state.role)) return "当前身份无权访问项目库。";
  const member = state.role === "owner" || project.ownerId === "H-lin" ||
    (!project.ownerId && project.owner === state.organization.members.find(person => person.id === "H-lin")?.name) ||
    state.organization.projectRoles.some(role => role.projectId === projectId && role.memberId === "H-lin");
  if (!member) return "当前 Human 未获此项目访问范围。";
  if (write && project.archivedAt) return "项目已归档，资料库只读；不会移动资料或重启索引。";
  return "";
}
export function libraryOriginalProblem(state, record) {
  const library = record?.library;
  if (!library) return "";
  if (record.tenantId !== state.activeTenant || !state.organization.members.some(person => person.id === "H-lin" && person.kind === "Human") || !["owner", "executor"].includes(state.role)) return "资料不存在或无权访问。";
  if (!Array.isArray(library.readers) || !library.readers.includes("H-lin")) return "未获原件读取授权。";
  if (library.scope === "PROJECT") return libraryProjectProblem(state, library.projectId);
  if (library.scope === "PRIVATE" && knowledgeOwner(state, record)?.id !== "H-lin") return "私人资料仅本人可读。";
  return ["TENANT", "PRIVATE"].includes(library.scope) ? "" : "资料待明确绑定范围。";
}
export function libraryView(state, sourceId, { version, publicationId, referenceId } = {}) {
  if (!["owner", "executor"].includes(state.role) || !state.organization.members.some(person => person.id === "H-lin" && person.kind === "Human")) return null;
  const record = state.resources.knowledge.find(item => item.id === sourceId);
  if (!record?.library || record.tenantId !== state.activeTenant) return null;
  const lib = record.library;
  const ref = referenceId && lib.references.find(item => item.id === referenceId);
  if (referenceId && (!ref || libraryProjectProblem(state, ref.projectId))) return null;
  publicationId = ref?.publicationId || publicationId;
  const pub = publicationId && lib.publications.find(item => item.id === publicationId);
  if (publicationId) {
    if (!pub || !["APPROVED", "REVOKED"].includes(pub.status) || !pub.readers.includes("H-lin")) return null;
    version = pub.version;
  } else if (knowledgeReadProblem(state, record)) return null;
  version = ref?.version || version || lib.currentVersion;
  const fixed = lib.versions.find(item => item.number === Number(version));
  if (!fixed || (pub && pub.version !== fixed.number) || (!pub && fixed.purpose === "PUBLICATION")) return null;
  const issues = [];
  if (lib.archivedAt) issues.push("来源已归档失效；固定历史仅供有权者回看，不能继续采用。");
  if (pub?.status === "REVOKED" || ref?.revokedAt) issues.push("授权已撤销；保留固定引用标识，正文不再可读。");
  if (!pub && lib.scope === "PROJECT" && state.projects.find(item => item.id === lib.projectId)?.archivedAt) issues.push("来源项目已归档；历史只读，使用历史须明确用途。");
  if (!pub && fixed.number !== lib.currentVersion) issues.push("来源已有新版本；此固定引用未自动更新。");
  if (pub && pub.sourceVersion !== lib.currentVersion) issues.push("原件已有更新；此授权发布版本仍固定，未自动采用新内容。");
  // Publication projection deliberately contains neither root history nor original metadata.
  return { sourceId, title: fixed.title, category: lib.category, version: fixed.number,
    content: pub?.status === "REVOKED" || ref?.revokedAt ? null : fixed.content,
    file: fixed.file ? { ...fixed.file } : null, scope: pub ? "TENANT" : lib.scope,
    projectId: pub ? null : lib.projectId || null, publicationId: pub?.id || null,
    referenceId: ref?.id || null, referencedProjectId: ref?.projectId || null,
    state: lib.indexState, indexedVersion: lib.indexedVersion, issues,
    authorization: pub ? { sourceNote: pub.sourceNote, sourceVersion: pub.sourceVersion, applicability: pub.applicability, license: pub.license, redaction: pub.redaction, approvedAt: pub.approvedAt } : { basis: "原件明确 Human 读取范围" },
    useChecks: (lib.useChecks || []).filter(check => check.version === fixed.number && (check.publicationId || null) === (pub?.id || null)).map(check => ({ id: check.id, sourceId: record.id, version: check.version, agentId: check.agentId, purpose: check.purpose, outcome: check.outcome, reason: check.reason, actorId: check.actorId, time: check.time, mode: "LOCAL_SIMULATION" })),
    usable: !lib.archivedAt && !ref?.revokedAt && (!pub || pub.status === "APPROVED") };
}
export function libraryRows(state, { scope = state.project === "all" ? "TENANT" : "PROJECT", projectId = state.project, category = "KNOWLEDGE", historical = false, query = "" } = {}) {
  const rows = [];
  for (const record of state.resources.knowledge) {
    const lib = record.library;
    if (!lib) continue;
    if (lib.category !== category) continue;
    if (lib.scope === scope && (scope !== "PROJECT" || lib.projectId === projectId)) {
      const view = libraryView(state, record.id);
      const archivedProject = scope === "PROJECT" && state.projects.find(item => item.id === projectId)?.archivedAt;
      if (view && (historical || (!lib.archivedAt && !archivedProject))) rows.push(view);
    }
    if (scope === "TENANT") for (const pub of lib.publications) {
      const view = libraryView(state, record.id, { publicationId: pub.id });
      if (view && (historical || (pub.status === "APPROVED" && !lib.archivedAt))) rows.push(view);
    }
    if (scope === "PROJECT") for (const ref of lib.references.filter(item => item.projectId === projectId)) {
      const view = libraryView(state, record.id, { referenceId: ref.id });
      if (view && (historical || (!ref.revokedAt && view.usable && !state.projects.find(project => project.id === projectId)?.archivedAt))) rows.push(view);
    }
  }
  const needle = query.trim().toLowerCase();
  return rows.filter(row => `${row.title} ${row.content || ""}`.toLowerCase().includes(needle));
}
export function libraryWriteProblem(state, record) {
  if (state.offline) return "离线中，请恢复连接后重新核对。";
  if (!["owner", "executor"].includes(state.role) || !state.organization.members.some(person => person.id === "H-lin" && person.kind === "Human")) return "仅当前租户 Human 可操作。";
  if (record) {
    const problem = knowledgeReadProblem(state, record);
    if (problem) return problem;
    if (record.revocation || record.library?.archivedAt) return "来源已终态撤销或归档，只能查看历史。";
    if (knowledgeOwner(state, record)?.id !== "H-lin" && state.role !== "owner") return "资料修改需要原件负责人。";
    if (record.library?.scope === "PROJECT") return libraryProjectProblem(state, record.library.projectId, true);
  }
  return "";
}
const libraryText = (value, label, max = 2000) => {
  if (typeof value !== "string" || !value.trim() || value.trim().length > max) throw Error(`${label}须为 1–${max} 字。`);
  return value.trim();
};
function libraryReaders(state, values) {
  if (!Array.isArray(values) || !values.length || values.some(value => !state.organization.members.some(person => person.id === value && person.kind === "Human"))) throw Error("请明确选择当前租户有权读取的 Human；租户共享不是全员公开。");
  return [...new Set(values)];
}
function libraryAgents(state, values = []) {
  if (!Array.isArray(values) || values.some(id => !state.agents.some(agent => agent.id === id && !["退役", "已退役", "待批准"].includes(agent.status)))) throw Error("Agent 授权须选择当前租户可用的明确身份，不能按岗位或频道推断。");
  return [...new Set(values)];
}
function libraryScope(state, scope, projectId) {
  if (!["TENANT", "PROJECT", "PRIVATE"].includes(scope)) throw Error("请选择明确的资料范围。");
  if (scope === "TENANT" && state.role !== "owner") throw Error("租户库写入需要负责人权限。");
  if (scope === "PROJECT") { const problem = libraryProjectProblem(state, projectId, true); if (problem) throw Error(problem); }
  return scope === "PROJECT" ? projectId : null;
}
function libraryVersion(input, number, actor, purpose = "ORIGINAL") {
  const title = libraryText(input.title, "标题", 200), content = libraryText(input.content, "正文 / 本地预览", 100000);
  let file = null;
  if (input.file) {
    if (typeof input.file.name !== "string" || !Number.isSafeInteger(input.file.size) || input.file.size < 0 || input.file.size > 10 * 1024 * 1024) throw Error("单文件最多 10 MB，须有真实文件名和字节数。");
    if (input.file.textContentAvailable !== undefined && typeof input.file.textContentAvailable !== "boolean") throw Error("文件正文可用标记无效。");
    const original = input.file.data ? normalizeChatAttachments([{id:"material-original",name:input.file.name,size:input.file.size,mime:input.file.type||"application/octet-stream",data:input.file.data}])[0] : null;
    file = { ...(original ? {data:original.data,kind:original.kind,mime:original.mime} : {}), name: input.file.name, size: input.file.size, type: typeof input.file.type === "string" ? input.file.type : "", textContentAvailable: input.file.textContentAvailable === true };
  }
  return { number, title, content, file, purpose, createdBy: actor.id, createdAt: actor.time };
}
export function libraryCommandOn(state, input, makeId, now = new Date().toISOString()) {
  if (input.tenantId !== state.activeTenant) throw Error("租户已切换，请重新打开资料库。");
  const record = input.id && state.resources.knowledge.find(item => item.id === input.id);
  if (input.id && !record) throw Error("资料不存在或无权访问。");
  let problem = libraryWriteProblem(state, ["reference", "revoke-reference", "check-use"].includes(input.action) ? null : record);
  if (problem) throw Error(problem);
  if (record && input.revision !== (record.revision || 1)) throw Error("资料修订已变化，请重新核对当前版本。");
  const actor = { id: "H-lin", kind: "Human", time: now, source: "LOCAL_SIMULATION" };
  const reason = libraryText(input.reason, "操作理由");
  if (["create", "bind"].includes(input.action)) {
    if (input.action === "bind" && (!record || record.library || record.kind === "记忆")) throw Error("只绑定未管理的知识/资料原件；记忆沿用既有治理。");
    if (input.action === "create" && record) throw Error("创建不能覆盖已有来源。");
    const projectId = libraryScope(state, input.scope, input.projectId);
    if (record) {
      const expectedScope = record.scope === "私人" ? "PRIVATE" : record.scope === "租户" ? "TENANT" : "PROJECT";
      if (input.scope !== expectedScope) throw Error("绑定只能明确原有范围；扩大共享须走固定发布候选，不能借绑定改动原件权限。");
    }
    if (!["KNOWLEDGE", "MATERIAL"].includes(input.category)) throw Error("请选择知识或素材与资料。");
    const readers = libraryReaders(state, input.readers);
    if (!readers.includes("H-lin") || (input.scope === "PRIVATE" && (readers.length !== 1 || readers[0] !== "H-lin"))) throw Error("原件须保留当前负责人的读取权；私人资料仅本人可读。");
    if (!record && input.origin === "upload" && !input.file) throw Error("请选择本地文件并保留原件元数据；不会伪造上传。");
    if (!record && input.origin === "upload" && input.category === "KNOWLEDGE" && !/\.(md|txt|js|jsx|ts|tsx|json|css|html|go|py|csv|yaml|yml)$/i.test(input.file.name)) throw Error("知识文本上传暂支持文本/代码；PDF 抽取与二进制解析未连接，可作为素材元数据登记。");
    const version = libraryVersion({ ...input, ...(record ? { title: record.name, content: record.content || record.source || "旧原件未记录正文" } : {}) }, 1, actor);
    const source = record || { id: makeId("K"), name: version.title, owner: state.organization.members.find(person => person.id === "H-lin").name, ownerId: "H-lin", ownerKind: "Human", tenantId: state.activeTenant, scope: input.scope === "PRIVATE" ? "私人" : input.scope === "TENANT" ? "租户" : "当前项目", kind: input.category === "MATERIAL" ? "资料" : "知识", status: "有效", revision: 0, history: [] };
    source.library = { category: input.category, scope: input.scope, projectId, readers, origin: input.action === "bind" ? "legacy" : input.origin === "upload" ? "upload" : "manual", agentReaders: libraryAgents(state, input.agentReaders), useChecks: [], versions: [version], currentVersion: 1, indexedVersion: null, indexState: "PENDING", attempts: 0, references: [], publications: [], events: [] };
    if (!record) state.resources.knowledge.unshift(source);
    source.revision = (source.revision || 0) + 1; source.library.events.push({ ...actor, action: input.action, reason });
    return source.id;
  }
  const lib = record?.library;
  if (!lib) throw Error("请先明确绑定原件范围。");
  const current = lib.versions.find(version => version.number === lib.currentVersion);
  const nextNumber = Math.max(...lib.versions.map(version => version.number)) + 1;
  if (input.action === "grant-agents") {
    if (state.role !== "owner" && knowledgeOwner(state, record)?.id !== "H-lin") throw Error("Agent 资料授权需要原件负责人。");
    const target = input.publicationId ? lib.publications.find(pub => pub.id === input.publicationId && pub.status === "APPROVED") : lib;
    if (!target) throw Error("授权发布版本不存在或已撤销。");
    target.agentReaders = libraryAgents(state, input.agentReaders);
  } else if (input.action === "check-use") {
    const view = libraryView(state, record.id, { publicationId: input.publicationId, version: input.version, referenceId: input.referenceId });
    if (!view || view.version !== input.version || view.content === null) throw Error("当前 Human 无权核对该固定来源版本。");
    const agent = state.agents.find(agent => agent.id === input.agentId);
    if (!agent) throw Error("Agent 不在当前租户中。");
    const purpose = libraryText(input.purpose, "本次历史取用目的");
    const publication = view.publicationId && lib.publications.find(pub => pub.id === view.publicationId);
    const grants = publication ? publication.agentReaders : lib.agentReaders;
    const historical = !publication && (view.version !== lib.currentVersion || lib.scope === "PROJECT" && state.projects.find(project => project.id === lib.projectId)?.archivedAt);
    const denied = !view.usable ? "来源已失效，拒绝新取用" : ["退役", "已退役", "待批准"].includes(agent.status) ? "Agent 当前不可用" : !grants?.includes(agent.id) ? "此固定来源没有该 Agent 的当前明确授权" : historical && input.allowHistorical !== true ? "历史版本或归档项目需要显式确认用途与历史取用" : "";
    lib.useChecks ??= [];
    lib.useChecks.push({ id: makeId("KUSE"), sourceId: record.id, version: view.version, publicationId: view.publicationId, agentId: agent.id, purpose, outcome: denied ? "DENIED" : "AUTHORIZED", reason: denied || reason, actorId: "H-lin", time: now, authorization: { sourceRevision: record.revision, publicationId: view.publicationId, agentId: agent.id }, mode: "LOCAL_SIMULATION" });
  } else if (input.action === "replace" || input.action === "revise") {
    if (input.action === "revise" && lib.origin !== "manual") throw Error("只可修订手工知识；来源内容通过其专用流程更新。");
    if (input.action === "replace" && lib.origin !== "upload") throw Error("仅 upload 来源可替换内容；派生/记忆来源不能普通覆盖。");
    if (!["INDEXED", "FAILED", "PENDING"].includes(lib.indexState)) throw Error("索引中不可替换内容。");
    if (input.action === "replace" && !input.file) throw Error("请选择替换文件，保留新版本元数据。");
    const version = libraryVersion(input, nextNumber, actor); lib.versions.push(version); lib.currentVersion = version.number; record.name = version.title; lib.indexState = "PENDING"; lib.attempts = 0;
  } else if (input.action === "index") {
    if (!["PENDING", "FAILED", "INDEXED"].includes(lib.indexState)) throw Error("当前状态不能开始索引。");
    lib.indexState = "INDEXING"; lib.attempts++; lib.indexReason = "";
  } else if (input.action === "index-success" || input.action === "index-failure") {
    if (lib.indexState !== "INDEXING") throw Error("请先启动本地模拟索引。");
    lib.indexState = input.action === "index-success" ? "INDEXED" : "FAILED"; lib.indexReason = reason;
    if (lib.indexState === "INDEXED") { lib.indexedVersion = current.number; lib.indexedAt = now; }
  } else if (input.action === "archive") {
    if (lib.origin !== "upload") throw Error("仅 upload 来源可归档失效；记忆与派生来源使用各自治理。");
    lib.archivedAt = now; lib.archivedBy = "H-lin"; lib.archiveReason = reason; lib.indexState = "INVALIDATED";
  } else if (input.action === "reference") {
    problem = libraryProjectProblem(state, input.projectId, true); if (problem) throw Error(problem);
    const view = libraryView(state, record.id, { publicationId: input.publicationId, version: input.version });
    if (!view || view.scope !== "TENANT" || !view.usable || (!view.publicationId && input.version !== lib.currentVersion && !lib.versions.some(item => item.number === input.version && item.purpose === "ORIGINAL"))) throw Error("请选择当前有权采用的租户固定版本。");
    if (lib.references.some(ref => !ref.revokedAt && ref.projectId === input.projectId && ref.version === view.version && ref.publicationId === view.publicationId)) return record.id;
    lib.references.push({ id: makeId("KREF"), projectId: input.projectId, version: view.version, publicationId: view.publicationId, authorization: { humanId: "H-lin", sourceRevision: record.revision, kind: view.publicationId ? "PUBLICATION" : "ORIGINAL" }, actor, reason });
  } else if (input.action === "revoke-reference") {
    const ref = lib.references.find(item => item.id === input.referenceId);
    if (!ref || ref.revokedAt) throw Error("引用不存在或已撤销。");
    problem = libraryProjectProblem(state, ref.projectId, true); if (problem) throw Error(problem);
    ref.revokedAt = now; ref.revokedBy = "H-lin"; ref.revokeReason = reason;
  } else if (input.action === "propose") {
    if (lib.scope === "TENANT") throw Error("已是租户原件，无需推广。");
    const readers = libraryReaders(state, input.readers);
    const version = libraryVersion(input, nextNumber, actor, "PUBLICATION");
    const proposal = { id: makeId("KPUB"), sourceVersion: current.number, version: version.number, status: "PENDING", readers, agentReaders: libraryAgents(state, input.agentReaders), applicability: libraryText(input.applicability, "适用范围"), redaction: libraryText(input.redaction, "脱敏核对"), license: libraryText(input.license, "许可依据"), sourceNote: libraryText(input.sourceNote, "来源依据"), reason, proposedBy: actor };
    lib.versions.push(version); lib.publications.push(proposal);
  } else if (["approve", "reject", "revoke-publication"].includes(input.action)) {
    if (state.role !== "owner") throw Error("租户发布决定需要 Human 负责人。");
    const pub = lib.publications.find(item => item.id === input.publicationId);
    if (!pub || (input.action === "revoke-publication" ? pub.status !== "APPROVED" : pub.status !== "PENDING")) throw Error("候选已有决定或发布记录不可用。");
    if (input.action === "approve" && pub.sourceVersion !== lib.currentVersion) throw Error("来源已更新，请重新提出当前版本候选。");
    pub.status = { approve: "APPROVED", reject: "REJECTED", "revoke-publication": "REVOKED" }[input.action]; pub.decidedBy = actor; pub.decisionReason = reason;
    if (input.action === "approve") pub.approvedAt = now;
  } else throw Error("未知资料库操作。");
  record.revision = (record.revision || 1) + 1; lib.events.push({ ...actor, action: input.action, reason, ...(input.action === "grant-agents" ? { publicationId: input.publicationId || null, agentReaders: [...input.agentReaders] } : {}) });
  return record.id;
}
