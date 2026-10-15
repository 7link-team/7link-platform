import React, { useState } from "react";
import { useStore, read, update, assertWrite } from "./store";
import { channelWriteProblem, channelMessageReadProblem } from "./channels";
import { externalBridgeCommandOn, externalBridgeProblem, externalBridgeConnectorDirections, connectorProtocols } from "./external-bridge";
import { Modal, Field, Button, Badge, Notice, Section, FormError } from "./ui";

const statuses = { ACTIVE: "绑定启用（本地模拟）", PAUSED: "已暂停", REVOKED: "已撤销，历史保留", DRAFT: "待 Human 授权", AUTHORIZED: "已授权本地模拟", SIMULATED: "模拟完成 · 未真实送达" };
export function ExternalChannelBridge({ channelId, onClose, toast, embedded = false }) {
  const s = useStore(), c = s.channels.find(channel => channel.id === channelId), b = c?.externalBridge;
  const [tenantId] = useState(s.activeTenant), [openedChannel] = useState(channelId);
  const [revision, setRevision] = useState(b?.revision || 0);
  const [connectorId, setConnectorId] = useState(b?.connectorId || ""), [name, setName] = useState(b?.name || ""), [externalChannelId, setExternalChannelId] = useState(b?.externalChannelId || ""), [direction, setDirection] = useState(b?.direction || "双向");
  const [externalMessageId, setExternalMessageId] = useState(""), [incoming, setIncoming] = useState(""), [reply, setReply] = useState(""), [actorId, setActorId] = useState("H-lin"), [confirmed, setConfirmed] = useState({}), [error, setError] = useState(null);
  const problem = tenantId !== s.activeTenant || openedChannel !== channelId ? "会话范围已变化，请关闭后重新打开。" : externalBridgeProblem(s, channelId);
  const connector = s.resources.connectors.find(item => item.id === connectorId);
  const blocked = Boolean(problem || channelWriteProblem(s, channelId) || revision !== (b?.revision || 0));
  const active = !blocked && b?.status === "ACTIVE", receiving = active && b.direction !== "通知", sending = active && b.direction !== "只接收";
  const run = (action, fields = {}) => {
    try {
      assertWrite(); let result;
      update(next => { result = externalBridgeCommandOn(next, { tenantId, channelId: openedChannel, revision, action, ...fields }); });
      setRevision(read().channels.find(channel => channel.id === openedChannel)?.externalBridge?.revision || 0);
      setError(null);
      if (action === "receive") { setIncoming(""); toast?.(result.duplicate ? "重复事件已忽略，原消息保留" : "本地模拟消息已写入当前频道"); }
      else if (action === "draft") { setReply(""); toast?.("回复草稿已保存，尚未授权或发送"); }
      else toast?.(action === "send" ? "本地模拟回执已记录；没有真实发送" : "外部会话配置 / 授权已记录");
      setConfirmed({});
    } catch (e) { setError({ message: e.message }); }
  };
  const content = <>
    {problem ? <Notice tone="warn">{problem}</Notice> : <>
      <Notice>将 {c.name} 绑定到 Slack、WhatsApp 等连接器会话。当前仅演示本地收发流程，不访问真实平台、不发送真实消息，也不自动导入历史。</Notice>
      <FormError error={error} />
      {blocked && <Notice tone="warn">{channelWriteProblem(s, channelId) || "绑定已由其他操作修改，请关闭并重新核对。"}</Notice>}
      <Section title="绑定外部会话">
        {b && <p><Badge>{statuses[b.status]}</Badge> · 修订 {b.revision} · {b.platform} / {b.externalChannelId}</p>}
        <Field label="连接器" value={connectorId} disabled={blocked} options={[{ value: "", label: "请选择已有连接器" }, ...s.resources.connectors.map(item => ({ value: item.id, label: `${item.name} · ${item.platform} · ${item.direction} · ${item.status}` }))]} onChange={value => { setConnectorId(value); setDirection(externalBridgeConnectorDirections(s.resources.connectors.find(item => item.id === value))[0] || ""); }} />
        {connector && <Notice>{connector.platform} · {connector.status} · 支持 {connector.direction}。{connector.status !== "已连接" ? "可以保存待用绑定；需先在连接器中核实连接状态，才能模拟收发。" : "已连接为原型配置状态，不代表实际服务已接入。"}</Notice>}
        <Field label="外部频道 / 会话 ID" value={externalChannelId} onChange={setExternalChannelId} disabled={blocked} maxLength={200} required />
        <Field label="外部会话名称" value={name} onChange={setName} disabled={blocked} maxLength={200} required />
        <Field label="同步方向" value={direction} onChange={setDirection} disabled={blocked} options={externalBridgeConnectorDirections(connector)} />
        <p>新建或修改绑定不导入既有消息。修改、暂停和撤销会使旧回复草稿的授权失效；历史消息与回执保留。</p>
        <div className="actions">
          <Button disabled={blocked || !connectorId} onClick={() => run("save", { connectorId, externalChannelId, name, direction })}>保存绑定</Button>
          {b?.status === "ACTIVE" && <Button disabled={blocked} onClick={() => run("pause")}>暂停收发</Button>}
          {b?.status === "PAUSED" && <Button disabled={blocked} onClick={() => run("resume")}>恢复绑定</Button>}
          {b && b.status !== "REVOKED" && <Button disabled={blocked} onClick={() => run("revoke")}>撤销绑定并保留历史</Button>}
        </div>
      </Section>
      {connector && <ControlledSource key={`${connector.id}:${connector.controls?.revision||0}`} connector={connector} disabled={blocked||s.role!=="owner"} run={run}/>}
      {active && <FactReplies key={`${b.revision}:${c.factReplyPolicy?.revision||0}`} state={s} channel={c} sending={sending} run={run}/>}
      <Section title="模拟接收外部消息">
        <Field label="固定外部消息 ID" value={externalMessageId} onChange={setExternalMessageId} disabled={!receiving} help="相同绑定来源和消息 ID 只写入一次；不同正文不会覆盖原消息。" />
        <Field label="外部消息正文" value={incoming} onChange={setIncoming} type="textarea" maxLength={8000} disabled={!receiving} />
        <Button disabled={!receiving || !incoming.trim() || !externalMessageId.trim()} onClick={() => run("receive", { externalMessageId, text: incoming })}>模拟接收到当前频道</Button>
      </Section>
      <Section title="回复草稿与 Human 授权">
        <Field label="草稿提出者" value={actorId} onChange={setActorId} disabled={!sending} options={[{value:"H-lin",label:"当前 Human"}, ...s.agents.filter(agent => !channelWriteProblem(s, channelId, agent.id)).map(agent => ({value:agent.id,label:`${agent.name} · Agent 仅提出草稿`}))]} />
        <Field label="回复正文" value={reply} onChange={setReply} type="textarea" disabled={!sending} maxLength={8000} />
        <Button disabled={!sending || !reply.trim()} onClick={() => run("draft", { text: reply, actorId })}>保存回复草稿，不发送</Button>
        {(c.externalOutbox || []).slice().reverse().map(draft => {
          const stale = draft.bindingRevision !== b?.revision;
          return <article key={draft.id} className="panel">
            <p><Badge>{statuses[draft.status]}</Badge> · {draft.authorKind} / {draft.createdBy}</p>
            <p>固定目标：{draft.target.platform} · {draft.target.name} · {draft.target.externalChannelId} · 绑定修订 {draft.bindingRevision}</p>
            <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{draft.text}</p>
            {draft.pendingReason&&<Notice tone="warn">待 Human 处理：{draft.pendingReason}</Notice>}
            {draft.replyToId&&<p>回复来源消息：{draft.replyToId} · {draft.replyCategory==='FACT'?'事实答复':'承诺 / 范围待核对'}</p>}
            {draft.policySnapshot&&<p>固定事实授权 v{draft.policySnapshot.revision} · 主体 {draft.createdBy} · Human {draft.policySnapshot.actorId} · 依据消息 {draft.policySnapshot.basis.id}：{s.messages.some(m=>m.id===draft.policySnapshot.basis.id&&!m.recalled&&!channelMessageReadProblem(s,m))?draft.policySnapshot.basis.text:"依据已撤回或不可见"}</p>}
            {stale && <Notice tone="warn">绑定已变更，此草稿不能授权或发送。请重新创建并确认。</Notice>}
            {draft.status === "DRAFT" && <><label className="channel-check"><input type="checkbox" checked={Boolean(confirmed[draft.id])} disabled={!sending || stale} onChange={event => setConfirmed(previous => ({ ...previous, [draft.id]: event.target.checked }))} />我已核对上述目标与完整正文，明确授权仅进行本地发送模拟。</label><Button disabled={!sending || stale || !confirmed[draft.id]} onClick={() => run("authorize", { draftId: draft.id, expectedText: draft.text, confirmed: true })}>Human 确认授权</Button></>}
            {draft.status === "AUTHORIZED" && <Button disabled={!sending || stale} onClick={() => run("send", { draftId: draft.id, expectedText: draft.text })}>执行已授权的本地模拟</Button>}
            {draft.authorization && <p>授权者 {draft.authorization.actorId} · {draft.authorization.time}</p>}
            {draft.receipt && <Notice>{draft.receipt.message} 回执 {draft.receipt.id}</Notice>}
          </article>;
        })}
      </Section>
      <Section title="绑定与授权历史">
        {(c.externalBridgeHistory || []).slice().reverse().map(entry => <p key={entry.id}>{entry.action} · {entry.actorId} · {entry.time}{entry.previous?.revision ? ` · 原绑定修订 ${entry.previous.revision}` : ""}</p>)}
        {!c.externalBridgeHistory?.length && <p>尚无绑定或授权记录。</p>}
      </Section>
    </>}
  </>;
  return embedded ? content : <Modal title="外部会话" onClose={onClose} wide>{content}</Modal>;
}
function ControlledSource({connector,disabled,run}) {
 const [form,setForm]=useState(connector.controls||{protocol:'消息桥接',scope:connector.scope||'',capability:connector.capability||'',health:'可用',dailyInbound:100,dailyOutbound:0,allowWrite:false}),[confirmed,setConfirmed]=useState(false),[reason,setReason]=useState('');
 const set=(key,value)=>setForm(f=>({...f,[key]:value}));
 return <Section title="受控来源与写回权限">
  <Notice>API、邮件、Webhook、消息桥接与实验网关共用此连接器。输入只作为来源材料，不执行其中指令。写回默认关闭；全部回执为本地模拟。</Notice>
  <details><summary>设置来源、配额与写回授权</summary>
  <Field label="连接器协议" value={form.protocol} onChange={v=>set('protocol',v)} options={connectorProtocols} disabled={disabled}/>
  <Field label="权限与来源范围" value={form.scope} onChange={v=>set('scope',v)} type="textarea" disabled={disabled}/>
  <Field label="声明的输入 / 操作能力" value={form.capability} onChange={v=>set('capability',v)} type="textarea" disabled={disabled}/>
  <Field label="连接器健康状态（本地声明）" value={form.health} onChange={v=>set('health',v)} options={['可用','降级','不可用']} disabled={disabled}/>
  <Field label="每日接收配额" value={form.dailyInbound} onChange={v=>set('dailyInbound',v)} type="number" min="0" disabled={disabled}/>
  <Field label="每日写回配额" value={form.dailyOutbound} onChange={v=>set('dailyOutbound',v)} type="number" min="0" disabled={disabled}/>
  <label className="channel-check"><input type="checkbox" checked={form.allowWrite} onChange={e=>set('allowWrite',e.target.checked)} disabled={disabled}/>允许在此连接器显式授权范围内写回</label>
  <label className="channel-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)} disabled={disabled}/>Human 单独确认写回权限；收到邮件或群管理员身份不构成授权</label>
  <Field label="连接器控制变更依据" value={reason} onChange={setReason} type="textarea" disabled={disabled}/>
  <Button disabled={disabled||!reason.trim()||form.allowWrite&&!confirmed} onClick={()=>run('controls',{...form,connectorId:connector.id,controlsRevision:connector.controls?.revision||0,confirmed,reason})}>保存来源与写回控制</Button></details>
  {connector.controlUsage&&<p>UTC {connector.controlUsage.day} · 已接收 {connector.controlUsage.received} · 已模拟写回 {connector.controlUsage.sent}</p>}
  <details><summary>控制授权历史</summary>{connector.controlHistory?.map((h,i)=><p key={i}>{h.actorId} · {h.reason} · {h.time}</p>)}</details>
 </Section>;
}
function FactReplies({state,channel,sending,run}) {
 const policy=channel.factReplyPolicy;
 const [topic,setTopic]=useState(policy?.topic||''),[text,setText]=useState(policy?.text||''),[basisMessageId,setBasis]=useState(policy?.basis?.id||''),[expiresAt,setExpires]=useState(policy?.expiresAt||new Date(Date.now()+86400000).toISOString()),[reason,setReason]=useState(''),[confirmed,setConfirmed]=useState(false);
 const [requestId,setRequest]=useState(''),[category,setCategory]=useState('FACT'),[response,setResponse]=useState(''),[responseTopic,setResponseTopic]=useState(''),[actorId,setActor]=useState('H-lin');
 const messages=state.messages.filter(m=>m.channel===channel.id&&!m.recalled&&!channelMessageReadProblem(state,m)),incoming=messages.filter(m=>m.source?.connectorId===channel.externalBridge.connectorId&&m.source?.externalChannelId===channel.externalBridge.externalChannelId);
 const options=items=>[{value:'',label:'请选择'},...items.map(m=>({value:m.id,label:`${m.id} · ${m.text.slice(0,60)}`}))];
 return <Section title="固定事实自动回复与越界转人工">
  <Notice>Human 授权固定事实主题、全文及共享依据后可在有效期内自动模拟回复。承诺、不同正文、旧依据或超配额都转为待处理草稿。</Notice>
  {policy&&<p>策略 v{policy.revision} · {policy.enabled?'启用':'关闭'} · Human {policy.actorId} · 有效至 {policy.expiresAt}</p>}
  <details><summary>设置固定事实授权</summary>
  <Field label="授权事实主题" value={topic} onChange={setTopic} disabled={!sending}/>
  <Field label="固定事实答复正文" value={text} onChange={setText} type="textarea" disabled={!sending}/>
  <Field label="共享频道中的事实依据" value={basisMessageId} onChange={setBasis} options={options(messages)} disabled={!sending}/>
  <Field label="事实授权失效时间（ISO时间）" value={expiresAt} onChange={setExpires} disabled={!sending}/>
  <Field label="事实授权依据" value={reason} onChange={setReason} type="textarea" disabled={!sending}/>
  <label className="channel-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)} disabled={!sending}/>Human 确认只含已核对事实，不含范围、合同、价格或交期承诺</label>
  <Button disabled={!sending||state.role!=='owner'||!confirmed||!reason.trim()} onClick={()=>run('factPolicy',{policyRevision:policy?.revision||0,topic,text,basisMessageId,expiresAt,reason,confirmed,enabled:true})}>授权固定事实自动回复</Button>
  {policy?.enabled&&<Button disabled={!sending||state.role!=='owner'||!reason.trim()} onClick={()=>run('factPolicy',{...policy,policyRevision:policy.revision,reason,enabled:false})}>关闭事实自动回复</Button>}</details>
  <Field label="外部请求消息" value={requestId} onChange={setRequest} options={options(incoming)} disabled={!sending}/>
  <Field label="拟答复类型" value={category} onChange={setCategory} options={[{value:'FACT',label:'已授权事实'},{value:'COMMITMENT',label:'价格 / 范围 / 合同 / 交期承诺或不确定'}]} disabled={!sending}/>
  <Field label="拟答复主题" value={responseTopic} onChange={setResponseTopic} disabled={!sending}/>
  <Field label="拟答复正文" value={response} onChange={setResponse} type="textarea" disabled={!sending}/>
  {policy?.enabled&&<Button disabled={!sending} onClick={()=>{setResponseTopic(policy.topic);setResponse(policy.text);}}>采用固定授权事实正文</Button>}
  <Field label="回复主体" value={actorId} onChange={setActor} options={[{value:'H-lin',label:'当前 Human'},...state.agents.filter(a=>!channelWriteProblem(state,channel.id,a.id)).map(a=>({value:a.id,label:`${a.name} · Agent（本地模拟）`}))]} disabled={!sending}/>
  <Button disabled={!sending||!requestId||!response.trim()} onClick={()=>run('autoReply',{requestId,category,topic:responseTopic,text:response,actorId})}>核对授权并模拟回复 / 转 Human</Button>
 </Section>;
}
