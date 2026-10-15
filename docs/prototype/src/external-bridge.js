import { channelActor, channelManageProblem, channelReadProblem, channelWriteProblem, channelPeople, channelMessageReadProblem } from "./channels";

export const externalBridgeDirections = ["双向", "只接收", "通知"];
const uid = prefix => `${prefix}-${crypto.randomUUID()}`;
const stamp = () => new Date().toISOString();
const clone = value => structuredClone(value);
const required = (value, label, max = 200) => {
  if (typeof value !== "string" || !value.trim() || value.length > max) throw Error(`${label}须为 1–${max} 字。`);
  return value.trim();
};
export function externalBridgeProblem(s, channelId) {
  const c = s.channels?.find(channel => channel.id === channelId);
  const read = channelReadProblem(s, channelId);
  if (read) return read;
  if (c?.kind !== "SHARED" || !c.shared) return "仅独立外部共享频道可绑定外部会话；内部、私聊与执行频道不开放。";
  return channelManageProblem(s, c);
}
export function externalBridgeConnectorDirections(connector) {
  return connector?.direction === "双向" ? externalBridgeDirections : connector?.direction === "通知" ? ["通知"] : ["只接收", "接收", "入站"].includes(connector?.direction) ? ["只接收"] : [];
}
export function visibleExternalBridgeBindings(s, connectorId) {
  return (s.channels || []).filter(c => c.externalBridge?.connectorId === connectorId && !externalBridgeProblem(s, c.id)).map(c => ({ channelId: c.id, channelName: c.name, ...clone(c.externalBridge) }));
}
const connectorSnapshot = c => ({ id: c.id, platform: c.platform, direction: c.direction, status: c.status, ...(c.controls ? {controls:clone(c.controls)} : {}) });
export const connectorProtocols = ["消息桥接", "HTTP API", "邮件", "Webhook", "实验网关"];
function usage(connector) {
  const day = stamp().slice(0,10);
  return connector.controlUsage?.day === day ? connector.controlUsage : {day,received:0,sent:0};
}
function controlProblem(connector, direction) {
  const controls=connector.controls;if(!controls)return '';
  if(controls.health==='不可用')return '连接器健康不可用，等待恢复后重试。';
  if(direction==='send'&&!controls.allowWrite)return '连接器写回未授权；输入材料不授予发送权限。';
  const amount=usage(connector)[direction==='send'?'sent':'received'];
  if(amount>=controls[direction==='send'?'dailyOutbound':'dailyInbound'])return '连接器本日配额已用尽；未产生外部副作用。';
  return '';
}
function consume(connector,direction) {
  if(!connector.controls)return;
  connector.controlUsage={...usage(connector)};
  connector.controlUsage[direction==='send'?'sent':'received']++;
}
function active(s, c, operation) {
  const b = c.externalBridge;
  if (!b || b.status !== "ACTIVE") throw Error("外部会话绑定已暂停或撤销，不能收发。");
  const connector = s.resources.connectors.find(item => item.id === b.connectorId);
  if (!connector || connector.status !== "已连接" || connector.platform !== b.platform) throw Error("连接器未连接、已变更或不存在，请重新核对绑定。");
  if (!externalBridgeConnectorDirections(connector).includes(b.direction)) throw Error("同步方向超出连接器当前能力。");
  if (operation === "receive" && b.direction === "通知" || operation === "send" && b.direction === "只接收") throw Error("绑定方向不允许此操作。");
  return connector;
}
function record(c, action, actorId, previous) {
  c.externalBridgeHistory ??= [];
  c.externalBridgeHistory.push({ id: uid("bridge-history"), action, actorId, time: stamp(), previous: clone(previous || null), mode: "LOCAL_SIMULATION" });
}
export function externalBridgeCommandOn(s, input) {
  if (input.tenantId !== s.activeTenant) throw Error("租户已切换，请重新打开外部会话。");
  const problem = externalBridgeProblem(s, input.channelId); if (problem) throw Error(problem);
  const c = s.channels.find(channel => channel.id === input.channelId);
  const write = channelWriteProblem(s, c.id); if (write) throw Error(write);
  if (input.revision !== (c.externalBridge?.revision || 0)) throw Error("绑定已变更，请重新核对当前修订。");
  const actorId = input.actorId || channelActor(s), person = channelPeople(s).find(p => p.id === actorId);
  if (!person?.available || person.external || s.role === "external") throw Error("当前身份不能管理外部会话。");
  if (person.kind === "Agent") {
    if (!["draft","autoReply"].includes(input.action) || channelWriteProblem(s, c.id, actorId)) throw Error("Agent 只能在已授权频道提出回复草稿，不能授权或发送。");
  } else if (actorId !== channelActor(s)) throw Error("不能替其他 Human 作出决定。");
  const b = c.externalBridge;
  if (input.action === "controls") {
    if(person.kind!=="Human"||s.role!=="owner")throw Error("只有当前 Human 负责人能授权连接器操作范围。");
    const connector=s.resources.connectors.find(item=>item.id===input.connectorId);
    if(!connector || input.controlsRevision!==(connector.controls?.revision||0))throw Error("连接器控制已变化，请重新核对。");
    if(!connectorProtocols.includes(input.protocol)||!["可用","降级","不可用"].includes(input.health))throw Error("请选择明确协议和健康状态。");
    for(const key of ['dailyInbound','dailyOutbound'])if(!Number.isSafeInteger(Number(input[key]))||Number(input[key])<0||Number(input[key])>100000)throw Error('每日配额须为 0–100000 的整数。');
    const scope=required(input.scope,'权限与来源范围',1000),capability=required(input.capability,'声明能力',1000),reason=required(input.reason,'授权依据',1000);
    if(input.allowWrite===true&&input.confirmed!==true)throw Error('启用写回须 Human 单独明确确认。');
    connector.controlHistory??=[];connector.controlHistory.push({previous:clone(connector.controls||null),actorId,time:stamp(),reason,mode:'LOCAL_SIMULATION'});
    connector.controls={revision:(connector.controls?.revision||0)+1,protocol:input.protocol,health:input.health,scope,capability,dailyInbound:Number(input.dailyInbound),dailyOutbound:Number(input.dailyOutbound),allowWrite:input.allowWrite===true,actorId,reason,time:stamp(),mode:'LOCAL_SIMULATION'};
    return clone(connector.controls);
  }
  if (input.action === "save") {
    const connector = s.resources.connectors.find(item => item.id === input.connectorId);
    if (!connector || !connector.platform || !externalBridgeConnectorDirections(connector).includes(input.direction)) throw Error("请选择现有连接器及其支持的同步方向。");
    const externalChannelId = required(input.externalChannelId, "外部频道 / 会话 ID"), name = required(input.name, "外部会话名称");
    if (s.channels.some(other => other.id !== c.id && other.externalBridge?.status !== "REVOKED" && other.externalBridge?.connectorId === connector.id && other.externalBridge?.externalChannelId === externalChannelId)) throw Error("此连接器会话已有绑定，请先撤销原绑定。");
    record(c, "保存绑定", actorId, b);
    c.externalBridge = { connectorId: connector.id, platform: connector.platform, externalChannelId, name, direction: input.direction, status: b?.status === "PAUSED" ? "PAUSED" : "ACTIVE", revision: (b?.revision || 0) + 1, time: stamp(), actorId, mode: "LOCAL_SIMULATION", historyScope: "仅显式模拟接收，不导入历史" };
    return clone(c.externalBridge);
  }
  if (["pause", "resume", "revoke"].includes(input.action)) {
    if (!b || b.status === "REVOKED") throw Error("绑定不存在或已撤销，请新建绑定。");
    if (input.action === "resume" && b.status !== "PAUSED" || input.action === "pause" && b.status !== "ACTIVE") throw Error("绑定状态已变化。");
    record(c, input.action, actorId, b);
    b.status = { pause: "PAUSED", resume: "ACTIVE", revoke: "REVOKED" }[input.action]; b.revision += 1;
    if (input.action === "revoke") b.revokedAt = stamp();
    return clone(b);
  }
  const connector = active(s, c, input.action === "receive" ? "receive" : "send");
  if(input.action==='factPolicy') {
    if(person.kind!=='Human'||s.role!=='owner')throw Error('事实自动回复策略须当前 Human 负责人授权。');
    if(input.policyRevision!==(c.factReplyPolicy?.revision||0))throw Error('事实策略已变化。');
    if(input.enabled!==false && input.confirmed!==true)throw Error('请明确确认事实范围、固定正文、依据和有效期。');
    const basis=s.messages.find(m=>m.id===input.basisMessageId&&m.channel===c.id&&!m.recalled&&!channelMessageReadProblem(s,m));
    if(input.enabled!==false&&(!basis||!basis.text?.trim()))throw Error('依据须来自当前外部共享频道中可见的固定消息。');
    if(input.enabled!==false&&(typeof input.expiresAt!=='string'||!Number.isFinite(Date.parse(input.expiresAt))||Date.parse(input.expiresAt)<=Date.now()))throw Error('请选择未来的策略失效时间。');
    const policy={revision:(c.factReplyPolicy?.revision||0)+1,enabled:input.enabled!==false,bindingRevision:b.revision,connectorSnapshot:connectorSnapshot(connector),topic:required(input.topic,'事实主题'),text:required(input.text,'固定事实答复',8000),basis:input.enabled===false?null:{id:basis.id,text:basis.text},expiresAt:input.expiresAt,actorId,time:stamp(),reason:required(input.reason,'授权依据',1000),mode:'LOCAL_SIMULATION'};
    record(c,'Human 授权事实回复策略',actorId,c.factReplyPolicy);c.factReplyPolicy=policy;return clone(policy);
  }
  if(input.action==='autoReply') {
    const request=s.messages.find(m=>m.id===input.requestId&&m.channel===c.id&&!m.recalled&&!channelMessageReadProblem(s,m)&&m.source?.connectorId===connector.id&&m.source?.externalChannelId===b.externalChannelId);
    if(!request)throw Error('请选择当前绑定收到的外部来源消息。');
    const existing=c.externalOutbox?.find(d=>d.replyToId===request.id);if(existing)return clone(existing);
    const policy=c.factReplyPolicy,basis=policy&&s.messages.find(m=>m.id===policy.basis?.id&&m.channel===c.id&&!m.recalled&&!channelMessageReadProblem(s,m));
    let pendingReason='';
    if(input.category!=='FACT')pendingReason='承诺或范围不明确，须由 Human 核对后逐条授权。';
    else if(!policy?.enabled||policy.bindingRevision!==b.revision||Date.parse(policy.expiresAt)<=Date.now()||JSON.stringify(policy.connectorSnapshot)!==JSON.stringify(connectorSnapshot(connector)))pendingReason='事实策略未授权、已失效或绑定 / 连接器已变化。';
    else if(input.topic!==policy.topic||input.text!==policy.text)pendingReason='主题或正文超出已授权固定事实范围。';
    else if(!basis||basis.text!==policy.basis.text)pendingReason='固定事实依据已变化或撤回，请 Human 重新核对。';
    else pendingReason=controlProblem(connector,'send');
    const text=required(input.text,'回复草稿',8000);
    const draft={id:uid('external-reply'),text,bindingRevision:b.revision,target:clone(b),connectorSnapshot:connectorSnapshot(connector),createdBy:actorId,authorKind:person.kind,time:stamp(),status:'DRAFT',mode:'LOCAL_SIMULATION',replyToId:request.id,replyCategory:input.category==='FACT'?'FACT':'HUMAN_REVIEW',pendingReason};
    if(!pendingReason){draft.authorization={actorId:policy.actorId,time:stamp(),bindingRevision:b.revision,text,target:clone(b),policyRevision:policy.revision,basis:clone(policy.basis),mode:'LOCAL_SIMULATION'};draft.status='AUTHORIZED';draft.policySnapshot=clone(policy);}
    c.externalOutbox??=[];c.externalOutbox.push(draft);
    if(!pendingReason)return externalBridgeCommandOn(s,{tenantId:s.activeTenant,channelId:c.id,revision:b.revision,action:'send',actorId:channelActor(s),draftId:draft.id,expectedText:text});
    record(c,'超范围回复转 Human 待处理',actorId,{draftId:draft.id,reason:pendingReason});return clone(draft);
  }
  if (input.action === "receive") {
    const externalMessageId = required(input.externalMessageId, "外部消息 ID"), text = required(input.text, "消息正文", 8000);
    const previous = s.messages.find(m => m.channel === c.id && m.source?.connectorId === b.connectorId && m.source?.externalChannelId === b.externalChannelId && m.source?.externalMessageId === externalMessageId);
    if (previous) { if (previous.text !== text) throw Error("相同外部消息 ID 的内容不一致，原消息保留。"); return { messageId: previous.id, duplicate: true }; }
    const denied=controlProblem(connector,'receive');if(denied)throw Error(denied);
    const message = { id: uid("external-message"), channel: c.id, author: `${b.platform} · 外部消息（本地模拟）`, authorId: "external-demo", kind: "human", text, time: stamp(), thread: null, attachments: [], mentions: [], source: { platform: b.platform, connectorId: b.connectorId, externalChannelId: b.externalChannelId, externalMessageId, mode: "LOCAL_SIMULATION" } };
    if(connector.controls)message.source={...message.source,protocol:connector.controls.protocol,scope:connector.controls.scope,controlsRevision:connector.controls.revision,contentRole:'UNTRUSTED_MATERIAL'};
    consume(connector,'receive');
    s.messages.push(message);
    return { messageId: message.id, duplicate: false };
  }
  if (input.action === "draft") {
    const text = required(input.text, "回复正文", 8000);
    const draft = { id: uid("external-reply"), text, bindingRevision: b.revision, target: clone(b), connectorSnapshot: connectorSnapshot(connector), createdBy: actorId, authorKind: person.kind, time: stamp(), status: "DRAFT", mode: "LOCAL_SIMULATION" };
    c.externalOutbox ??= []; c.externalOutbox.push(draft); return clone(draft);
  }
  const draft = c.externalOutbox?.find(d => d.id === input.draftId);
  if (!draft || draft.bindingRevision !== b.revision || draft.text !== input.expectedText || JSON.stringify(draft.connectorSnapshot) !== JSON.stringify(connectorSnapshot(connector))) throw Error("回复草稿、绑定或连接器已变化，请重新创建草稿并授权。");
  if (input.action === "authorize") {
    if (draft.status !== "DRAFT" || input.confirmed !== true) throw Error("请 Human 明确确认当前目标和完整回复正文。");
    const denied=controlProblem(connector,'send');if(denied)throw Error(denied);
    draft.authorization = { actorId, time: stamp(), bindingRevision: b.revision, text: draft.text, target: clone(draft.target), mode: "LOCAL_SIMULATION" }; draft.status = "AUTHORIZED";
    record(c, "Human 授权模拟发送", actorId, { draftId: draft.id, authorization: draft.authorization }); return clone(draft);
  }
  if (input.action === "send") {
    if (draft.status === "SIMULATED") return clone(draft);
    if (draft.status !== "AUTHORIZED" || !draft.authorization || draft.authorization.text !== draft.text) throw Error("回复尚未获得 Human 明确授权。");
    const denied=controlProblem(connector,'send');if(denied)throw Error(denied);
    consume(connector,'send');
    draft.status = "SIMULATED"; draft.receipt = { id: uid("simulation-receipt"), time: stamp(), actorId, mode: "LOCAL_SIMULATION", delivered: false, message: "本地发送流程模拟完成；未调用外部平台，未证明真实送达。" };
    record(c, "模拟发送回执", actorId, { draftId: draft.id, receipt: draft.receipt }); return clone(draft);
  }
  throw Error("未知外部会话操作。");
}
