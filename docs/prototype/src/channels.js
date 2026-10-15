import { executionReadProblem, executionInputProblem } from "./executions.js";
// Tenant-local collaboration model. Runtime delivery and production credentials are not simulated.
export const channelActor = s => s.role === "external" ? "external-demo" : "H-lin";
export const channelPeople = s => [...s.organization.members.map(p => ({ ...p, kind: "Human", available: !p.disabledAt && !["停用", "已停用", "离职", "已离职", "DISABLED", "REMOVED"].includes(p.status) })), ...s.agents.map(p => ({ ...p, kind: "Agent", available: !["退役", "已退役", "退役中", "待批准", "RETIRING", "RETIRED"].includes(p.status) })), { id: "external-demo", name: "陈曦 · 客户（本地身份）", kind: "Human", external: true, available: true }];
const now = () => new Date().toISOString();
const uid = prefix => `${prefix}-${crypto.randomUUID().slice(0, 8)}`;
const member = (principalId, role = "MEMBER", source = "EXPLICIT") => ({ principalId, role, source, joinedAt: now(), leftAt: null });
function projectChannelOwners(s, projectId) {
  const project = s.projects.find(p => p.id === projectId);
  if (!project) return [];
  const humans = s.organization.members.filter(p => p.kind === "Human");
  const owners = project.ownerId ? humans.filter(p => p.id === project.ownerId) : humans.filter(p => p.name === project.owner);
  return [...new Set([...(owners.length === 1 ? [owners[0].id] : []), ...s.organization.projectRoles.filter(r => r.projectId === projectId && r.role === "项目负责人" && humans.some(p => p.id === r.memberId)).map(r => r.memberId)])];
}
function projectPeople(s, projectId) {
  const project = s.projects.find(p => p.id === projectId);
  if (!project) return [];
  const owners = projectChannelOwners(s, projectId);
  const ids = [...owners, ...s.organization.projectRoles.filter(r => r.projectId === projectId).map(r => r.memberId), ...(s.channelProjectGrants || []).filter(g => g.projectId === projectId && !g.revokedAt && g.permissions.includes("channel_read")).map(g => g.agentId)];
  return [...new Set(ids.filter(id => channelPeople(s).some(p => p.id === id && !p.external && p.available)))];
}
export function hydrateChannels(s) {
  s.channels ??= [];
  s.channelProjectGrants ??= [];
  const add = c => { if (!s.channels.some(row => row.id === c.id)) s.channels.push({ revision: 1, topic: "", archivedAt: null, members: [], hooks: [], announcement: null, history: [], ...c }); };
  add({ id: "team", name: "协作总频道", kind: "TEAM", projectId: null });
  if ((s.agents || []).some(a => a.id === "quill")) add({ id: "private", name: "Quill", kind: "DIRECT", projectId: null, members: [member("H-lin"), member("quill")] });
  if ((s.messages || []).some(m => m.channel === "external")) add({ id: "external", name: "客户共创", kind: "SHARED", shared: true, projectId: null, members: [member("H-lin", "OWNER"), { ...member("external-demo"), historyAccess: "EXISTING_SHARED" }] });
  for (const project of (s.projects || []).filter(p => p.id !== "private")) add({ id: project.id, name: project.name, kind: "PROJECT_DEFAULT", projectId: project.id });
  return s;
}
export function channelMembers(s, c) {
  if (!c) return [];
  const people = channelPeople(s);
  const ids = c.kind === "PROJECT_DEFAULT" ? [...new Set([...projectPeople(s, c.projectId), ...(s.role === "owner" && s.organization.members.some(p => p.id === channelActor(s)) ? [channelActor(s)] : [])])] : c.kind === "TEAM" ? s.organization.members.map(p => p.id) : null;
  return (ids ? ids.map(id => member(id, projectChannelOwners(s, c.projectId).includes(id) ? "OWNER" : "MEMBER", "DERIVED")) : c.members || []).map(m => ({ ...m, person: people.find(p => p.id === m.principalId) })).filter(m => m.person && !m.leftAt);
}
export function channelReadProblem(s, channelId, principalId = channelActor(s)) {
  const person = channelPeople(s).find(p => p.id === principalId);
  if (!person?.available) return "当前身份已不在租户中，或已退役 / 尚未批准。";
  const external = Boolean(person.external), owner = principalId === channelActor(s) && s.role === "owner";
  if (channelId?.startsWith("exec-")) return executionReadProblem(s,channelId.slice(5),principalId);
  const c = s.channels?.find(c => c.id === channelId);
  if (!c) return "频道不存在或不在当前租户。";
  if (external && !c.shared) return "此频道不在共享范围。";
  const participant = channelMembers(s, c).some(m => m.principalId === principalId);
  if (c.kind === "DIRECT") return participant ? "" : "私人会话仅参与者可见。";
  if (c.projectId && !owner && !external && !projectPeople(s,c.projectId).includes(principalId)) return "项目会话访问权限未授予或已撤销。";
  if (c.shared || c.kind === "CUSTOM") return participant ? "" : "你已不是此频道成员。";
  return participant || owner ? "" : "当前没有项目频道访问权限。";
}
export function channelManageProblem(s, c) {
  const problem = channelReadProblem(s, c?.id); if (problem) return problem;
  if (c.kind === "DIRECT") return "私信成员与共享范围固定，不能作为普通频道管理。";
  if (s.role === "external") return "外部成员不能管理频道。";
  return s.role === "owner" || channelMembers(s, c).some(m => m.principalId === channelActor(s) && m.role === "OWNER") ? "" : "需要频道所有者权限。";
}
export function channelWriteProblem(s, channelId, principalId = channelActor(s)) {
  if (s.offline) return "离线中，请恢复连接后重新操作。";
  const problem = channelReadProblem(s, channelId, principalId); if (problem) return problem;
  const c = s.channels?.find(c => c.id === channelId);
  if (!c) return executionInputProblem(s,channelId.slice(5));
  if (c.archivedAt || s.projects.find(p => p.id === c.projectId)?.archivedAt) return "频道已归档，只能查看历史。";
  const person = channelPeople(s).find(p => p.id === principalId);
  if (c.kind === "DIRECT" && (c.members.length !== 2 || c.members.some(m => m.leftAt || !channelPeople(s).some(p => p.id === m.principalId && p.available)))) return "私信参与者已退役、停用或移出租户，历史只读。";
  if (person.kind === "Agent" && c.projectId && !(s.channelProjectGrants || []).some(g => g.agentId === principalId && g.projectId === c.projectId && !g.revokedAt && g.permissions.includes("channel_post"))) return "Agent 未取得此项目的频道发言权限。";
  if (!channelMembers(s, c).some(m => m.principalId === principalId)) return "只有当前频道成员可以发送。";
  return "";
}
export function assertChannelWrite(s, channelId) { const problem = channelWriteProblem(s, channelId); if (problem) throw Error(problem); }
export function visibleChannels(s, { query = "", archived = "active" } = {}) {
  return (s.channels || []).filter(c => !channelReadProblem(s, c.id) && (archived === "all" || Boolean(c.archivedAt) === (archived === "archived")) && `${c.name} ${c.topic}`.toLowerCase().includes(query.toLowerCase()));
}
function current(s, input, writable = true, manage = true) {
  if (input.tenantId !== s.activeTenant) throw Error("租户已切换，请重新打开频道管理。");
  if (s.offline) throw Error("离线中，请恢复连接后操作。");
  const c = s.channels.find(c => c.id === input.channelId);
  const problem = manage ? channelManageProblem(s, c) : channelReadProblem(s, c?.id); if (problem) throw Error(problem);
  if (input.revision !== c.revision) throw Error("频道已变更，请重新打开当前版本。");
  if (writable) assertChannelWrite(s, c.id);
  return c;
}
function snapshot(c, action) {
  c.history.push({ action, time: now(), actorId: "H-lin", revision: c.revision, name: c.name, topic: c.topic, archivedAt: c.archivedAt, members: structuredClone(c.members), announcement: structuredClone(c.announcement), hooks: structuredClone(c.hooks) });
  c.revision += 1;
}
function nameCheck(s, name, projectId, except) {
  if (typeof name !== "string" || !/^[\p{L}\p{N}][\p{L}\p{N}_-]{0,39}$/u.test(name.trim())) throw Error("频道名须为 1–40 个字母、数字、汉字、下划线或连字符，首位不能为符号。");
  if (s.channels.some(c => c.id !== except && c.kind === "CUSTOM" && c.projectId === projectId && !c.archivedAt && c.name.toLowerCase() === name.trim().toLowerCase())) throw Error("此项目已有同名频道。");
}
function topicCheck(topic) { if (typeof topic !== "string" || topic.length > 200) throw Error("主题须为最多 200 字的文本。"); }
export function createChannelOn(s, input) {
  if (input.tenantId !== s.activeTenant || s.role === "external" || s.offline) throw Error("当前不能创建频道，请核对租户、权限与连接。");
  const project = s.projects.find(p => p.id === input.projectId && p.id !== "private");
  if (!project || project.archivedAt || channelReadProblem(s, project.id)) throw Error("请选择有权访问的活动项目。");
  nameCheck(s, input.name, project.id); topicCheck(input.topic || "");
  if (s.channels.filter(c => c.kind === "CUSTOM" && c.projectId === project.id).length >= 50) throw Error("每项目最多 50 个自建频道。");
  if (input.shared != null && typeof input.shared !== "boolean" || input.memberIds != null && !Array.isArray(input.memberIds)) throw Error("频道共享范围或成员列表格式无效。");
  const ids = [...new Set([channelActor(s), ...(input.memberIds || [])])];
  if (input.shared && s.role !== "owner") throw Error("创建对外共享频道需要租户所有者确认。");
  if (ids.length > 200 || ids.some(id => !channelCandidates(s, project.id, input.shared).some(p => p.id === id))) throw Error("请选择当前租户的有效成员，最多 200 位；外部成员须使用独立共享频道。");
  const c = { id: uid("channel"), projectId: project.id, kind: "CUSTOM", shared: Boolean(input.shared), name: input.name.trim(), topic: input.topic || "", revision: 1, archivedAt: null, createdAt: now(), createdBy: channelActor(s), members: ids.map(id => ({ ...member(id, id === channelActor(s) ? "OWNER" : "MEMBER"), ...(id === "external-demo" ? { historyAccess: "FROM_JOIN", excludedHistoryIds: [] } : {}) })), hooks: [], announcement: null, history: [] };
  s.channels.push(c); return c.id;
}
export function changeChannelOn(s, input) {
  const c = current(s, input, input.action !== "restore");
  if (input.action === "edit") {
    topicCheck(input.topic);
    if (c.kind === "CUSTOM") nameCheck(s, input.name, c.projectId, c.id);
    else if (input.name !== c.name) throw Error("默认频道名称跟随原范围，不支持独立改名。");
    snapshot(c, "编辑频道"); c.name = input.name.trim(); c.topic = input.topic;
  } else if (["archive", "restore"].includes(input.action)) {
    if (c.kind !== "CUSTOM") throw Error("只有自建频道可以手动归档与恢复。");
    if (!input.reason?.trim()) throw Error("请填写归档或恢复理由。");
    if (input.action === "restore") {
      if (!c.archivedAt) throw Error("频道尚未归档。");
      if (s.projects.find(p => p.id === c.projectId)?.archivedAt) throw Error("请先恢复所属项目。");
      nameCheck(s, c.name, c.projectId, c.id);
    }
    snapshot(c, `${input.action === "archive" ? "归档" : "恢复"}：${input.reason.trim()}`);
    c.archivedAt = input.action === "archive" ? now() : null;
    if (c.archivedAt) for (const hook of c.hooks) hook.configuredEnabled = false;
  } else throw Error("未知频道操作。");
}
export function changeMemberOn(s, input) {
  const c = current(s, input, true, !(input.action === "remove" && input.principalId === channelActor(s)));
  if (!["CUSTOM", "SHARED"].includes(c.kind)) throw Error("默认成员由项目或组织权限派生，请在原范围管理。");
  if (!["add", "role", "remove"].includes(input.action) || !["OWNER", "MEMBER"].includes(input.role || "MEMBER")) throw Error("未知成员操作或角色。");
  const person = (input.action === "add" ? channelCandidates(s,c.projectId,c.shared) : channelPeople(s)).find(p => p.id === input.principalId && (!p.external || c.shared));
  if (!person) throw Error("成员不存在或属于外部身份；不能公开内部历史。");
  if (person.external && (input.role === "OWNER" || input.action === "add" && input.historyAccess !== "FROM_JOIN")) throw Error("外部成员仅能作为成员读取加入后的消息，请明确确认；不开放旧历史。");
  const existing = c.members.find(m => m.principalId === person.id && !m.leftAt);
  if (input.action !== "add" && !existing) throw Error("该成员已不在频道。");
  if (existing?.role === "OWNER" && (input.action === "remove" || input.role === "MEMBER") && channelMembers(s,c).filter(m => m.role === "OWNER" && m.person.available).length === 1) throw Error("请先指定另一位频道所有者。");
  if (input.action === "add" && existing) throw Error("该成员已加入频道。");
  if (input.action === "add" && channelMembers(s,c).length >= 200) throw Error("频道成员不能超过 200 位。");
  snapshot(c, `${input.action}成员 ${person.id}`);
  if (input.action === "add") c.members.push({ ...member(person.id, input.role || "MEMBER"), ...(person.external ? { historyAccess: "FROM_JOIN", excludedHistoryIds: s.messages.filter(m => m.channel === c.id).map(m => m.id) } : {}) });
  if (input.action === "role") existing.role = input.role;
  if (input.action === "remove") existing.leftAt = now();
}
export function publishAnnouncementOn(s, input) {
  const c = current(s, input);
  if (input.cancel) {
    if (!c.announcement || c.announcement.cancelledAt) throw Error("没有生效的公告。");
    snapshot(c, "取消公告"); c.announcement.cancelledAt = now();
    const previous = s.messages.find(m => m.id === c.announcement.messageId); if (previous) previous.pinned = false;
    return;
  }
  if (!input.body?.trim() || input.body.length > 8000) throw Error("公告正文须为 1–8000 字。");
  snapshot(c, c.announcement ? "修订公告" : "发布公告");
  const previous = s.messages.find(m => m.id === c.announcement?.messageId); if (previous) previous.pinned = false;
  const messageId = uid("announcement");
  s.messages.push({ id: messageId, channel: c.id, text: input.body.trim(), author: "林予", authorId: "H-lin", kind: "human", time: now(), pinned: true, announcement: true });
  c.announcement = { messageId, revision: (c.announcement?.revision || 0) + 1, publishedAt: now(), publishedBy: "H-lin", cancelledAt: null };
}
export function hookTargetProblem(value) {
  try {
    const url = new URL(value); const host = url.hostname.toLowerCase();
    const ip = /^\d+\.\d+\.\d+\.\d+$/.test(host) ? host.split(".").map(Number) : null;
    const local4 = ip && (ip[0] === 0 || ip[0] === 10 || ip[0] === 127 || ip[0] >= 224 || ip[0] === 169 && ip[1] === 254 || ip[0] === 172 && ip[1] >= 16 && ip[1] <= 31 || ip[0] === 192 && ip[1] === 168 || ip[0] === 100 && ip[1] >= 64 && ip[1] <= 127);
    const local6 = host.startsWith("[") && (/^\[(::|::1)\]$/.test(host) || /^\[(fc|fd|fe[89ab]|ff|::ffff:)/.test(host));
    if (typeof value !== "string" || value.length > 512 || url.protocol !== "https:" || url.username || url.password || url.hash || (!host.includes(".") && !host.startsWith("[")) || /(^|\.)(localhost|local|internal)$/.test(host) || local4 || local6) return "仅支持无账户凭据的 HTTPS 公网地址；服务端连接时仍须复核 DNS 与地址范围。";
    for (const [key, content] of url.searchParams) if (/^(token|secret|api[-_]?key|password|credential|signature|access[-_]?token)$/i.test(key) && !content.startsWith("secretref:")) return "秘密查询参数请填 secretref:引用ID，不能把真实凭据保存到本地配置。";
    return "";
  } catch { return "请填写有效 HTTPS 目标地址。"; }
}
export function changeHookOn(s, input) {
  const c = current(s, input);
  const old = c.hooks.find(h => h.id === input.hookId);
  if (input.action !== "save" && !["disable", "enable", "revoke"].includes(input.action)) throw Error("未知钩子操作。");
  if (input.hookId && !old || old?.revokedAt) throw Error("钩子不存在或已撤销。");
  if (input.action !== "save" && !old) throw Error("请选择有效钩子。");
  if (input.action === "save") {
    if (typeof input.configuredEnabled !== "boolean" || typeof input.mentionsOnly !== "boolean") throw Error("钩子配置开关须为明确状态。");
    const problem = hookTargetProblem(input.targetUrl); if (problem) throw Error(problem);
    if (!Array.isArray(input.kinds) || !input.kinds.length || input.kinds.some(k => !["TEXT", "EVENT", "DECISION_CARD", "DISPATCH_CARD"].includes(k))) throw Error("请选择有效消息类型。");
  }
  snapshot(c, `${input.action}钩子配置`);
  if (input.action === "save") {
    const value = { id: old?.id || uid("hook"), kind: "OUTGOING_WEBHOOK", targetUrl: input.targetUrl.trim(), filter: { kinds: [...new Set(input.kinds)], mentionsOnly: Boolean(input.mentionsOnly) }, configuredEnabled: Boolean(input.configuredEnabled), revision: (old?.revision || 0) + 1, revokedAt: null, runtime: "NOT_CONNECTED" };
    if (old) Object.assign(old, value); else c.hooks.push(value);
  } else { old.configuredEnabled = input.action === "enable"; old.revision += 1; if (input.action === "revoke") old.revokedAt = now(); }
}
export function previewChannelHook(s, input) {
  const c = current(s, input); const hook = c.hooks.find(h => h.id === input.hookId);
  if (!hook || hook.revokedAt || !hook.configuredEnabled ) throw Error("钩子已停用、撤销或缺少外发授权，不能预览。");
  return { previewOnly: true, runtime: "NOT_CONNECTED", channel_id: c.id, message_id: "<新消息编号>", kinds: [...hook.filter.kinds], mentions_only: hook.filter.mentionsOnly, body: "<正文已脱敏，不读取历史或附件>", signature: "未生成；尚未连接投递服务" };
}

export function visibleChannelMessages(s, channelId) {
  if (channelReadProblem(s, channelId)) return [];
  const c = s.channels.find(c => c.id === channelId);
  const membership = c && channelMembers(s, c).find(m => m.principalId === channelActor(s));
  return s.messages.filter(m => m.channel === channelId && (s.role !== "external" || membership?.historyAccess === "EXISTING_SHARED" || membership?.historyAccess === "FROM_JOIN" && !(membership.excludedHistoryIds || []).includes(m.id) && Date.parse(m.time) >= Date.parse(membership.joinedAt)));
}
export function channelMessageReadProblem(s, message) {
  return !message || !visibleChannelMessages(s, message.channel).some(m => m.id === message.id) ? "消息不在当前可读范围。" : "";
}

export function channelCandidates(s, projectId, shared = false) {
  const ids = projectId ? projectPeople(s, projectId) : channelPeople(s).map(p => p.id);
  return channelPeople(s).filter(p => p.available && (p.external ? shared : ids.includes(p.id) || p.id === "H-lin" && s.role === "owner"));
}

export function directMessageCandidates(s) {
  const actor = channelPeople(s).find(p => p.id === channelActor(s));
  if (s.role === "external" || !actor?.available || actor.kind !== "Human") return [];
  return channelPeople(s).filter(p => p.id !== actor.id && !p.external && p.available);
}
export function directMessagePeer(s, c) {
  const peerId = c?.members?.find(m => m.principalId !== channelActor(s))?.principalId;
  return channelPeople(s).find(p => p.id === peerId) || { id: peerId, name: "已离开成员", kind: "Human", available: false };
}
export function directMessagePolicy(s, c) {
  const agent = c?.members?.map(m => s.agents.find(a => a.id === m.principalId)).find(Boolean);
  return agent
    ? `托管 Agent 私信政策：${agent.name} 的负责人 ${agent.owner || "未登记"} 与获授权审计员可只读查阅；原型尚未接入该旁观授权，当前仅参与者可读。分享须选择具体消息与接收范围；发消息不会自动启动任务或执行。`
    : "仅两名参与者可见，租户所有者和组织上级没有旁观权。分享须选择具体消息与接收范围。";
}
export function openDirectChannelOn(s, input) {
  if (input.tenantId !== s.activeTenant || s.offline) throw Error("租户已切换或当前离线，请重新发起私信。");
  const peer = directMessageCandidates(s).find(p => p.id === input.peerId);
  if (!peer) throw Error("请选择当前租户内另一名在职 Human 或可用 Agent；不能与自己、外部身份或失效成员发起私信。");
  const pair = [channelActor(s), peer.id].sort();
  const existing = s.channels.find(c => c.kind === "DIRECT" && c.members?.length === 2 && c.members.every(m => !m.leftAt && pair.includes(m.principalId)) && new Set(c.members.map(m => m.principalId)).size === 2);
  if (existing) return existing.id;
  const c = { id: uid("dm"), kind: "DIRECT", projectId: null, name: "dm", directPairKey: pair.join(":"), revision: 1, topic: "", archivedAt: null, createdAt: now(), createdBy: channelActor(s), members: pair.map(id => member(id)), hooks: [], announcement: null, history: [] };
  s.channels.push(c);
  return c.id;
}

export function agentChannelGrantProblem(s, agentId) {
  const agent = s.agents.find(a => a.id === agentId);
  if (!agent) return "Agent 不在当前租户。";
  const owners = s.organization.members.filter(p => p.kind === "Human" && (agent.ownerId ? p.id === agent.ownerId : p.name === agent.owner));
  return s.role !== "external" && owners.length === 1 && owners[0].id === channelActor(s) ? "" : "仅此 Agent 的 Human 负责人可以授予或撤销项目会话权限。";
}
export function changeAgentChannelGrantOn(s, input) {
  if (input.tenantId !== s.activeTenant || s.offline) throw Error("租户已切换或当前离线，请重新打开授权。");
  const problem = agentChannelGrantProblem(s,input.agentId); if (problem) throw Error(problem);
  const agent = s.agents.find(a => a.id === input.agentId);
  if ((agent.revision || 1) !== input.agentRevision) throw Error("Agent 配置已变更，请重新核对负责人和授权。");
  const project = s.projects.find(p => p.id === input.projectId && p.id !== "private");
  if (!project || channelReadProblem(s,project.id)) throw Error("请选择当前有权管理的项目会话范围。");
  if (s.role !== "owner" && project.ownerId !== channelActor(s) && !s.organization.projectRoles.some(r => r.projectId === project.id && r.memberId === channelActor(s) && r.role === "项目负责人")) throw Error("授予项目会话权限还需要该项目负责人权限。");
  if (!input.reason?.trim()) throw Error("请填写授权或撤销说明。");
  const existing = s.channelProjectGrants.find(g => g.agentId === agent.id && g.projectId === project.id);
  if ((existing?.revision || 0) !== input.revision) throw Error("授权已变化，请重新核对当前版本。");
  if (!["grant","revoke"].includes(input.action)) throw Error("未知项目会话授权操作。");
  if (input.action === "grant") {
    if (!channelPeople(s).find(p => p.id === agent.id)?.available || project.archivedAt) throw Error("退役、待批准 Agent 或归档项目不能新增授权。");
    if (!Array.isArray(input.permissions) || !input.permissions.includes("channel_read") || input.permissions.some(p => !["channel_read","channel_post"].includes(p))) throw Error("只允许显式 channel_read/channel_post，发言必须同时可读。");
  } else if (!existing || existing.revokedAt) throw Error("当前没有有效授权。");
  const previous = existing ? structuredClone({...existing,history:undefined}) : null;
  const value = { id: existing?.id || uid("channel-grant"), agentId: agent.id, projectId: project.id, permissions: input.action === "grant" ? [...new Set(input.permissions)] : [...existing.permissions], grantorId: channelActor(s), time: now(), revision: (existing?.revision || 0) + 1, revokedAt: input.action === "revoke" ? now() : null,
    history: [...(existing?.history || []),{ action: input.action, reason: input.reason.trim(), actorId: channelActor(s), time: now(), previous }] };
  if (existing) Object.assign(existing,value); else s.channelProjectGrants.push(value);
}
