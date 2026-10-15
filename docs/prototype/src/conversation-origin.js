import { channelReadProblem, channelMessageReadProblem, visibleChannelMessages } from "./channels";

const textId = value => typeof value === "string" && value.length > 0 ? value : null;
const own = (value, key) => Object.prototype.hasOwnProperty.call(value || {}, key);
const denied = (index, status, reason) => ({ index, status, reason, reference: null, link: null, message: null, thread: [], creation: null });
function pick(raw) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  return {
    tenantId: textId(raw.tenantId), projectId: textId(raw.projectId ?? raw.project),
    channelId: textId(raw.channelId ?? raw.channel), executionId: textId(raw.executionId),
    contextOnly: raw.contextOnly === true,
    messageId: textId(raw.messageId), threadId: textId(raw.threadId), draftId: textId(raw.draftId),
    objectVersion: typeof raw.objectVersion === "number" || typeof raw.objectVersion === "string" ? raw.objectVersion : null,
    draftRevision: Number.isInteger(raw.draftRevision) ? raw.draftRevision : null,
    createdBy: textId(raw.createdBy ?? raw.actorId), createdAt: textId(raw.createdAt ?? raw.time),
    actorKind: ["Human", "Agent", "System"].includes(raw.actorKind) ? raw.actorKind : null,
    confirmation: raw.confirmation && typeof raw.confirmation === "object" ? { actorId: textId(raw.confirmation.actorId), status: textId(raw.confirmation.status), time: textId(raw.confirmation.time), revision: Number.isInteger(raw.confirmation.revision) ? raw.confirmation.revision : null } : null,
    confirmedBy: textId(raw.confirmedBy), confirmedAt: textId(raw.confirmedAt),
  };
}
function references(s, record) {
  if (own(record, "conversationOrigins")) return Array.isArray(record.conversationOrigins) ? record.conversationOrigins.map(pick) : [null];
  const context = record?.sourceContext, draftId = textId(record?.draftId), draft = draftId ? s.drafts?.[draftId] : null;
  if (!context && !draftId) return [];
  const source = context && typeof context === "object" ? context : draft?.source || {};
  const executionId = textId(source.executionId) || textId(draft?.executionId);
  return [pick({
    ...source, tenantId: source.tenantId ?? draft?.tenantId, projectId: source.projectId ?? source.project ?? draft?.project,
    // Legacy execution drafts store their authoritative execution ID alongside a generic channel field.
    channelId: source.channelId ?? source.channel ?? (executionId ? null : draft?.channel), executionId,
    threadId: source.threadId ?? draft?.threadId, draftId,
    draftRevision: record?.planSnapshot?.draftRevision ?? null,
    confirmedBy: record?.planSnapshot?.confirmedBy,
  })];
}
function creation(ref) {
  return { tenantId: ref.tenantId, projectId: ref.projectId, channelId: ref.channelId, executionId: ref.executionId, contextOnly:ref.contextOnly, messageId: ref.messageId, threadId: ref.threadId, draftId: ref.draftId, objectVersion: ref.objectVersion, draftRevision: ref.draftRevision, createdBy: ref.createdBy, createdAt: ref.createdAt, confirmedBy: ref.confirmedBy, confirmedAt: ref.confirmedAt, actorKind: ref.actorKind, confirmation: ref.confirmation };
}
function safeMessage(message) {
  // Never project cached sourceContext text, earlier edit histories, or unrelated object payloads.
  return structuredClone(Object.fromEntries(["id", "channel", "author", "authorId", "kind", "text", "time", "attachments", "file", "thread", "recalled"].filter(key => own(message, key)).map(key => [key, message[key]])));
}
function conversationLink(ref, channelId, threadId, draft) {
  const params = new URLSearchParams({tenant:ref.tenantId});
  if (ref.projectId) params.set("scope", ref.projectId);
  if (ref.executionId) params.set("execution_id", ref.executionId);
  else params.set("channel_id", channelId);
  if (threadId) params.set("thread_id", threadId);
  if (ref.messageId) params.set("message_id", ref.messageId);
  if (draft?.kind === "work") params.set("work_draft", draft.id);
  return `/chat?${params}`;
}
function resolve(s, ref, index) {
  if (!ref?.tenantId) return denied(index, "UNRECORDED", "来源租户或稳定引用未记录，不能推断来源会话。");
  if (ref.tenantId !== s.activeTenant) return denied(index, "FOREIGN_TENANT", "来源属于其他租户；当前会话不展示其内容。");
  const channelId = ref.executionId ? `exec-${ref.executionId}` : ref.channelId;
  if (!channelId) return denied(index, "UNRECORDED", "来源频道 / 执行会话未记录，不能代用公共频道。");
  if (ref.executionId && ref.channelId && ref.channelId !== channelId) return denied(index, "MISMATCH", "来源频道与执行会话标识不一致。");
  const read = channelReadProblem(s, channelId);
  if (read) return denied(index, "UNAVAILABLE", "来源会话不存在或当前无权读取；未展示来源正文。");
  if (ref.contextOnly && ref.executionId && !ref.messageId && !ref.threadId && !ref.draftId) {
    return { index, status:"CONTEXT", reason:"明确记录的来源执行上下文；未指定来源消息，不推断为某次输入或输出。", reference:creation(ref), creation:creation(ref), message:null, thread:[], link:conversationLink(ref,channelId,null,null), draftStatus:null, warning:null };
  }
  const draft = ref.draftId ? s.drafts?.[ref.draftId] : null;
  const draftChannel = draft?.executionId ? `exec-${draft.executionId}` : draft?.channel;
  let draftUsable = draft && draft.id === ref.draftId && draft.tenantId === ref.tenantId && draftChannel === channelId && (!ref.threadId || draft.threadId === ref.threadId);
  if (!ref.messageId && !draftUsable) return denied(index, "MISSING_DRAFT", "尚无来源消息，原讨论草稿也已缺失或不在此会话。");
  const message = ref.messageId ? s.messages.find(item => item.id === ref.messageId && item.channel === channelId) : null;
  if (ref.messageId && !message) return denied(index, "MISSING_MESSAGE", "来源消息已缺失，不能以缓存文字代替原消息。");
  if (message && (message.tenantId && message.tenantId !== ref.tenantId || channelMessageReadProblem(s, message))) return denied(index, "UNAVAILABLE", "当前无权读取这条来源消息；未展示来源正文。");
  if (message?.recalled) return denied(index, "RECALLED", "来源消息已撤回；不展示原正文或历史缓存。");
  const threadId = ref.threadId || message?.thread || (message ? message.id : draft?.threadId) || null;
  if (message && threadId !== message.id && message.thread !== threadId || message?.thread && threadId === message.id && message.thread !== message.id) return denied(index, "MISMATCH", "来源消息与所记录线程不一致，不能跳转到其他讨论。");
  if (message && draft?.threadId && draft.threadId !== threadId) draftUsable = false;
  const root = threadId ? s.messages.find(item => item.id === threadId && item.channel === channelId) : null;
  if (threadId && (!root || root.thread && root.thread !== root.id || root.tenantId && root.tenantId !== ref.tenantId || channelMessageReadProblem(s, root))) return denied(index, "MISSING_THREAD", "来源线程已缺失或不可读，未补造线程历史。");
  if (root?.recalled) return denied(index, "RECALLED", "来源线程已撤回；不展示原正文或历史缓存。");
  const thread = threadId ? visibleChannelMessages(s, channelId).filter(item => (!item.tenantId || item.tenantId === ref.tenantId) && (item.id === threadId || item.thread === threadId)).map(item => item.recalled ? { id: item.id, channel: item.channel, thread: item.thread, recalled: true, text: "", time: item.time } : safeMessage(item)) : [];
  return { index, status: message ? "READY" : "DRAFT", reason: message ? "当前可读来源消息；正文反映当前记录，不替代创建时的固定引用。" : "来源仍处于讨论草稿阶段，尚无固定来源消息。", reference: creation(ref), creation: creation(ref), message: message ? safeMessage(message) : null, thread, link: conversationLink(ref, channelId, threadId, draftUsable ? draft : null), draftStatus: draftUsable ? draft.status : null, warning: ref.draftId && !draftUsable ? "原讨论草稿已缺失或不匹配；仍可查看授权的来源消息。" : null };
}
export function resolveConversationOrigins(s, record) {
  const refs = references(s, record);
  if (refs.length) return { category: "CONVERSATION", label: "会话来源", sources: refs.map((ref, index) => resolve(s, ref, index)) };
  const categories = { MANUAL: "手动创建，无会话来源", IMPORT: "导入对象，无会话来源", API: "API 创建，无会话来源", SCHEDULE: "定时生成，无会话来源" };
  const sourceKind = record?.creationSource ?? record?.sourceKind;
  return { category: Object.hasOwn(categories, sourceKind || "") ? sourceKind : "UNRECORDED", label: categories[sourceKind] || "未记录会话来源；不推断为公共频道或同名会话。", sources: [] };
}
