import { ExternalChannelBridge } from "./external-channel-bridge";
import React, { useState } from "react";
import { useStore, read, createChannel, openDirectChannel, changeChannel, changeChannelMember, publishChannelAnnouncement, changeChannelHook, changeAgentChannelGrant } from "./store";
import { channelActor, channelPeople, channelCandidates, channelMembers, channelManageProblem, channelReadProblem, channelWriteProblem, previewChannelHook, visibleChannelMessages, agentChannelGrantProblem, directMessageCandidates, directMessagePeer, directMessagePolicy } from "./channels";
import { useViewState } from "./view-state";
import { Modal, Field, Button, Badge, Notice, Tabs, Section, Form, Link, FormError } from "./ui";
import { formatTimestamp } from "./formatting";

export function DirectMessageStart({ initial, onClose, onCreated }) {
  const s = useStore(), [peerId, setPeerId] = useState(initial.peerId || "");
  const [error, setError] = useState(null);
  const people = directMessageCandidates(s), peer = people.find(p => p.id === peerId);
  const c = initial.channelId && s.channels.find(c => c.id === initial.channelId && c.kind === "DIRECT");
  const stale = initial.tenantId !== s.activeTenant;
  const path = c && `/chat?channel_id=${encodeURIComponent(c.id)}&tenant=${encodeURIComponent(s.activeTenant)}&scope=all`;
  return <Modal title="发起私信" onClose={onClose}>
    {stale ? <Notice>租户已切换，请关闭后在当前租户重新选择联系人。</Notice> : c && !channelReadProblem(s,c.id) ? <>
      <Notice>已准备与 {directMessagePeer(s,c).name} 的私信；没有发送消息，也没有启动任务。</Notice>
      <Notice>{directMessagePolicy(s,c)}</Notice>
      <div className="actions"><Link className="btn primary" to={path} target="_blank" rel="noopener noreferrer">在新标签页打开私信</Link><Link className="btn" to={path}>在当前页打开私信</Link></div>
    </> : <Form onSubmit={() => { try { const channelId = openDirectChannel({tenantId:initial.tenantId,peerId}); setError(null); onCreated(channelId,peerId); } catch(e) { setError({message:e.message}); } }}>
      <p>选择当前租户的 Human 或 Agent。同一对参与者复用已有私信，成员固定为两位；不属于任何项目。</p>
      <Field label="私信联系人" value={peerId} onChange={setPeerId} required searchable options={[{value:"",label:"请选择 Human 或 Agent"},...people.map(p => ({value:p.id,label:`${p.name} · ${p.kind}${p.role ? ` · ${p.role}` : ""}`}))]} help="展开后按姓名、身份或岗位搜索；不包含自己、外部客户、停用成员和退役 Agent。" />
      {peer?.kind === "Agent" && <Notice>{directMessagePolicy(s,{members:[{principalId:peer.id}]})}</Notice>}
      {peer?.kind === "Human" && <Notice>仅你和对方可见；组织上级与租户所有者没有旁观权。</Notice>}
      {!people.length && <Notice>当前没有可发起私信的其他成员。</Notice>}
      <FormError error={error} />
      <div className="actions"><Button onClick={onClose}>取消</Button><Button type="submit" variant="primary" disabled={!peer || s.offline}>打开或创建私信</Button></div>
    </Form>}
  </Modal>;
}
export function DirectMessageButton({ peerId }) {
  const s = useStore(), [initial, setInitial] = useViewState(`direct-start.${peerId || "picker"}`,null);
  return <>
    <Button icon="MessageSquarePlus" disabled={s.offline || !directMessageCandidates(s).some(p => p.id === peerId)} onClick={() => setInitial({tenantId:s.activeTenant,peerId})}>发私信</Button>
    {initial && <DirectMessageStart key={`${initial.tenantId}:${initial.peerId}`} initial={initial} onClose={() => setInitial(null)} onCreated={(channelId,selectedPeer) => setInitial({...initial,channelId,peerId:selectedPeer})} />}
  </>;
}

export function ChannelManagement({ channelId, onClose, onCreated, toast, initialTab, onTabChange }) {
  const s = useStore(); const c = s.channels.find(c => c.id === channelId);
  const creating = !channelId;
  const [localTab, setLocalTab] = useState("details");
  const tab=initialTab || localTab;
  const setTab=value=>{setLocalTab(value);onTabChange?.(value);};
  const [name, setName] = useState(c?.name || ""); const [topic, setTopic] = useState(c?.topic || "");
  const [projectId, setProjectId] = useState(s.project !== "all" && s.project !== "private" ? s.project : "");
  const [shared, setShared] = useState(false); const [memberIds, setMemberIds] = useState([]);
  const [revision, setRevision] = useState(c?.revision); const [tenantId] = useState(s.activeTenant);
  const [memberId, setMemberId] = useState(""); const [memberRole, setMemberRole] = useState("MEMBER");
  const [reason, setReason] = useState(""); const [body, setBody] = useState("");
  const [hook, setHook] = useState({ targetUrl: "", kinds: ["TEXT"], mentionsOnly: false, configuredEnabled: false });
  const [preview, setPreview] = useState(null); const [error, setError] = useState("");
  const readable = creating || !channelReadProblem(s, channelId);
  const managementProblem = creating ? "" : channelManageProblem(s, c);
  const writable = creating || !channelWriteProblem(s, channelId) && !managementProblem;
  const args = { tenantId, channelId, revision };
  const run = (fn, success) => { try { fn(); setRevision(read().channels.find(c => c.id === channelId)?.revision); setError(""); setPreview(null); toast(success); } catch(e) { setError({message:e.message}); } };
  const announcement = c?.announcement && visibleChannelMessages(s, channelId).find(m => m.id === c.announcement.messageId);
  const people = channelCandidates(s,creating ? projectId : c?.projectId,creating ? shared : c?.shared);
  const members = readable && c ? channelMembers(s,c) : [];
  const locked = !writable || revision !== c?.revision || tenantId !== s.activeTenant;
  if (c?.kind === "DIRECT") return <Modal title="私信详情" onClose={onClose}>
    {!readable || tenantId !== s.activeTenant ? <Notice>私信权限或租户已变化，请关闭后重新进入。</Notice> : <>
      <Notice>{directMessagePolicy(s,c)}</Notice>
      <Section title="固定参与者"><ul className="channel-member-list">{c.members.map(m => {const person=channelPeople(s).find(p => p.id === m.principalId);return <li key={m.principalId}><div><b>{person?.name || "已离开成员"}</b><small>{person?.kind || "历史主体"} · {m.principalId}{!person?.available ? " · 历史只读" : ""}</small></div></li>;})}</ul></Section>
      <p>私信不属于项目，不支持改名、加人、退出、公告或钩子。消息与线程保留各自的历史。</p>
    </>}
  </Modal>;
  return <Modal wide className="channel-management-dialog" title={creating ? "创建频道" : "频道设置"} onClose={onClose} draftValue={creating ? {name,topic,projectId,shared,memberIds} : {nameChanged:name !== c?.name,topicChanged:topic !== c?.topic,body,hook,reason,memberId}}>
    {({ dismiss }) => <div className="channel-management">
      <FormError error={error} />
      {!readable || tenantId !== s.activeTenant ? <Notice tone="warn">频道权限或租户已变化，请关闭此窗口重新进入。</Notice> : <>
      <p className="muted">当前本地身份：{s.role === "external" ? "陈曦 · 客户" : "林予"} · <span translate="no">{channelActor(s)}</span></p>
      {creating ? <Form onSubmit={event => { event.preventDefault(); run(() => { const id = createChannel({tenantId,projectId,name,topic,shared,memberIds}); dismiss(); onCreated(id); }, "频道已创建"); }}>
        <Field label="所属项目" value={projectId} onChange={setProjectId} required options={[{value:"",label:"请选择项目"}, ...s.projects.filter(p => p.id !== "private" && !p.archivedAt && !channelReadProblem(s,p.id)).map(p => ({value:p.id,label:p.name}))]} />
        <Field label="频道名称" value={name} onChange={setName} required maxLength={40} help="1–40 字；字母、数字、汉字、下划线或连字符，首位不能为符号。" />
        <Field label="频道主题" value={topic} onChange={setTopic} maxLength={200} />
        {s.role === "owner" && <label className="channel-check"><input type="checkbox" checked={shared} onChange={e => { setShared(e.target.checked); setMemberIds(ids => ids.filter(id => id !== "external-demo")); }} />新建明确对外共享的频道</label>}
        {shared && <Notice tone="warn">这是全新的共享频道，不开放任何已有内部历史。外部成员仅能读取加入后的消息；附件仍须单独授权。当前外部身份为本地演示账号。</Notice>}
        <fieldset><legend>初始成员 · 创建者自动成为所有者</legend>{people.filter(p => p.id !== "H-lin").map(person => <label className="channel-check" key={person.id}><input type="checkbox" checked={memberIds.includes(person.id)} onChange={e => setMemberIds(ids => e.target.checked ? [...ids,person.id] : ids.filter(id => id !== person.id))} />{person.name} · {person.kind}{person.external ? " · 外部" : ""}</label>)}</fieldset>
        <Button type="submit" variant="primary">创建频道</Button>
      </Form> : <>
        <p><Badge>{c.archivedAt ? "已归档 · 只读" : "活动频道"}</Badge> {c.shared && <Badge>外部成员共享范围</Badge>} <span translate="no">{c.id}</span></p>
        {c.kind === "DIRECT" && <Notice>私信仅参与者可见；不能变更成员、公告、钩子或共享范围。当前为本地身份演示，不代表生产审计权限。</Notice>}
        {managementProblem && c.kind !== "DIRECT" && <Notice>{managementProblem}；可以查看已授权的频道详情。</Notice>}
        {revision !== c.revision && <Notice tone="warn">频道已变更；当前表单不会覆盖新版本。<Button onClick={() => { setRevision(c.revision); setName(c.name); setTopic(c.topic); setBody(""); setHook({targetUrl:"",kinds:["TEXT"],mentionsOnly:false,configuredEnabled:false}); setError(""); }}>载入当前版本并放弃旧表单</Button></Notice>}
        <Tabs label="频道管理栏目" value={tab} onChange={setTab} items={[["details","详情"],["members","成员"],["announcements","公告"],["hooks","钩子"],["external","外部会话"],["history","历史"]]} />
        {tab === "details" && <Section title="频道资料">
          <p>范围：{s.projects.find(p => p.id === c.projectId)?.name || (c.kind === "DIRECT" ? "私人会话" : "当前租户")} · 版本 {c.revision}</p>
          <Field label="频道名称" value={name} onChange={setName} disabled={locked || c.kind !== "CUSTOM"} maxLength={40} />
          <Field label="频道主题" value={topic} onChange={setTopic} disabled={locked} maxLength={200} />
          <Button disabled={locked} onClick={() => run(() => changeChannel({...args,action:"edit",name,topic}), "频道资料已保存")}>保存频道资料</Button>
          {c.kind === "CUSTOM" && !managementProblem && <Section title="关闭与恢复">
            <Notice>关闭意味着归档：消息和成员历史保留；归档后禁止发言及配置写入。恢复不会启用已停用钩子。</Notice>
            <Field label={c.archivedAt ? "恢复理由" : "归档理由"} value={reason} onChange={setReason} type="textarea" required />
            <Button disabled={revision !== c.revision || s.offline} onClick={() => run(() => {changeChannel({...args,action:c.archivedAt ? "restore" : "archive",reason});setReason("");}, c.archivedAt ? "频道已恢复，钩子保持停用" : "频道已归档，历史保留")}>{c.archivedAt ? "恢复频道" : "归档关闭频道"}</Button>
          </Section>}
        </Section>}
        {tab === "members" && <Section title="当前成员">
          {["PROJECT_DEFAULT","TEAM"].includes(c.kind) && <Notice>成员由项目或组织权限实时派生。请在原范围变更权限，频道不能单独移除默认成员。</Notice>}
          {c.shared && <Notice tone="warn">新增外部成员仅可见加入后的消息，不开放已有历史。移除立即停止其后续访问；既有消息保留。</Notice>}
          <ul className="channel-member-list">{members.map(m => <li key={m.principalId}><div><b>{m.person.name}</b><small>{m.person.kind}{!m.person.available ? ` · ${m.person.status}（历史成员，不可新提及）` : ""} · {m.role === "OWNER" ? "所有者" : "成员"} · {m.source === "DERIVED" ? "权限派生" : "显式加入"}{m.person.external ? " · 外部" : ""}</small>{m.person.kind === "Agent" && <Link className="text-link" to={`/agents/${m.person.id}`}>查看 Agent 项目会话授权</Link>}</div>{["CUSTOM","SHARED"].includes(c.kind) && !managementProblem && <div className="actions"><Button disabled={locked || m.person.external} onClick={() => run(() => changeChannelMember({...args,action:"role",principalId:m.principalId,role:m.role === "OWNER" ? "MEMBER" : "OWNER"}), "成员角色已更新")}>{m.role === "OWNER" ? "设为成员" : "设为所有者"}</Button><Button disabled={locked} onClick={() => run(() => changeChannelMember({...args,action:"remove",principalId:m.principalId}), "成员已移除，历史保留")}>移除</Button></div>}</li>)}</ul>
          {c.kind === "CUSTOM" && managementProblem && members.some(m => m.principalId === channelActor(s)) && <Button disabled={!!channelWriteProblem(s,c.id)} onClick={() => run(() => {changeChannelMember({...args,action:"remove",principalId:channelActor(s)});dismiss();},"已退出频道，历史保留")}>退出频道</Button>}
          {["CUSTOM","SHARED"].includes(c.kind) && !managementProblem && <>
            <Notice>成员候选来自当前项目权限与已授权 Agent；若不在列表，请先在项目中授权。</Notice>
            <Field label="添加成员" value={memberId} onChange={setMemberId} disabled={locked} options={[{value:"",label:"请选择当前租户成员"},...people.filter(p => !members.some(m => m.principalId === p.id)).map(p => ({value:p.id,label:p.name}))]} />
            <Field label="加入角色" value={memberRole} onChange={setMemberRole} disabled={locked || memberId === "external-demo"} options={[{value:"MEMBER",label:"成员"},{value:"OWNER",label:"频道所有者"}]} />
            <Button disabled={locked || !memberId} onClick={() => run(() => { changeChannelMember({...args,action:"add",principalId:memberId,role:memberId === "external-demo" ? "MEMBER" : memberRole,historyAccess:"FROM_JOIN"}); setMemberId(""); }, "成员已添加")}>{memberId === "external-demo" ? "确认仅开放加入后消息并添加" : "添加成员"}</Button>
          </>}
        </Section>}
        {tab === "announcements" && <Section title="频道公告">
          {announcement && !c.announcement.cancelledAt ? <><Badge>公告 v{c.announcement.revision}</Badge><p className="channel-announcement-text">{announcement.text}</p><small>{formatTimestamp(c.announcement.publishedAt)} · 发布人 {c.announcement.publishedBy}</small><div className="actions"><Button disabled={locked} onClick={() => setBody(announcement.text)}>载入公告修订</Button><Button disabled={locked} onClick={() => run(() => publishChannelAnnouncement({...args,cancel:true}), "公告已取消，原消息与版本保留")}>取消公告</Button></div></> : <p>当前没有生效公告。</p>}
          <Field label="公告正文" value={body} onChange={setBody} disabled={locked} type="textarea" maxLength={8000} help="发布生成频道消息；修订另存发布版本，取消不会删除历史。" />
          <Button disabled={locked || !body.trim()} variant="primary" onClick={() => run(() => { publishChannelAnnouncement({...args,body}); setBody(""); }, "公告已发布，历史版本保留")}>{announcement && !c.announcement.cancelledAt ? "发布修订公告" : "发布公告"}</Button>
        </Section>}
        {tab === "hooks" && <Section title="向外推送钩子">
          <Notice>仅保存 OUTGOING_WEBHOOK 配置。尚未连接投递服务；没有真实密钥、签名、发送、重试或回执。启用表示配置意图，不表示已在运行。</Notice>
          {managementProblem && <p>钩子配置仅频道管理者可见。</p>}
          <ul className="channel-member-list">{(!managementProblem ? c.hooks : []).map(h => <li key={h.id}><div><b>{h.targetUrl}</b><small>配置 v{h.revision} · {h.revokedAt ? "已撤销" : h.configuredEnabled ? "配置启用" : "配置停用"} · 尚未连接投递服务</small></div><div className="actions"><Button disabled={locked || !!h.revokedAt} onClick={() => {setHook({hookId:h.id,targetUrl:h.targetUrl,kinds:h.filter.kinds,mentionsOnly:h.filter.mentionsOnly,configuredEnabled:h.configuredEnabled});setPreview(null);}}>编辑配置</Button><Button disabled={locked || !!h.revokedAt} onClick={() => run(() => changeChannelHook({...args,action:h.configuredEnabled ? "disable" : "enable",hookId:h.id}), "配置状态已更新，不代表运行状态")}>{h.configuredEnabled ? "停用" : "启用配置"}</Button><Button disabled={locked || !!h.revokedAt} onClick={() => run(() => changeChannelHook({...args,action:"revoke",hookId:h.id}), "钩子已撤销，历史保留")}>撤销</Button><Button disabled={locked || !!h.revokedAt || !h.configuredEnabled} onClick={() => {try {setPreview(previewChannelHook(read(),{...args,hookId:h.id}));setError("");} catch(e){setError({message:e.message});}}}>脱敏载荷预览</Button></div></li>)}</ul>
          {!managementProblem && <>
            <Field label="HTTPS 公网目标地址（支持普通查询参数）" value={hook.targetUrl} onChange={value => setHook(h => ({...h,targetUrl:value}))} disabled={locked} help="普通查询参数会保留；秘密参数只填 secretref:引用ID，不保存实际秘密。连接真实服务时再次校验 DNS 和目标地址。" />
            <fieldset disabled={locked}><legend>消息类型筛选</legend>{["TEXT","EVENT","DECISION_CARD","DISPATCH_CARD"].map(kind => <label className="channel-check" key={kind}><input type="checkbox" checked={hook.kinds.includes(kind)} onChange={e => setHook(h => ({...h,kinds:e.target.checked ? [...h.kinds,kind] : h.kinds.filter(k => k !== kind)}))} />{kind}</label>)}</fieldset>
            <label className="channel-check"><input disabled={locked} type="checkbox" checked={hook.mentionsOnly} onChange={e => setHook(h => ({...h,mentionsOnly:e.target.checked}))} />仅包含提及的消息</label>
            <label className="channel-check"><input disabled={locked} type="checkbox" checked={hook.configuredEnabled} onChange={e => setHook(h => ({...h,configuredEnabled:e.target.checked}))} />保存为配置启用（尚未连接投递服务）</label>
            <div className="actions"><Button disabled={locked} onClick={() => run(() => {changeChannelHook({...args,...hook,action:"save"});setHook({targetUrl:"",kinds:["TEXT"],mentionsOnly:false,configuredEnabled:false});}, "钩子配置已保存，未发送任何消息")}>{hook.hookId ? "保存钩子修改" : "新增钩子配置"}</Button>{hook.hookId && <Button onClick={() => setHook({targetUrl:"",kinds:["TEXT"],mentionsOnly:false,configuredEnabled:false})}>改为新增</Button>}</div>
          </>}
          {preview && !locked && <pre className="channel-payload" aria-label="脱敏载荷预览">{JSON.stringify(preview,null,2)}</pre>}
        </Section>}
        {tab === "external" && <ExternalChannelBridge key={`${s.activeTenant}:${channelId}`} channelId={channelId} toast={toast} embedded/>}
        {tab === "history" && <Section title="管理与发布历史">
          <Notice>历史保留原修订与成员身份；取消公告、移除成员或撤销钩子均不会物理删除记录。</Notice>
          {managementProblem ? <p>管理历史仅频道管理者可见。</p> : c.history.length ? <ol>{[...c.history].reverse().map((h,index) => <li key={index}><b>v{h.revision} · {h.action}</b><p>{formatTimestamp(h.time)} · {h.actorId}</p>{h.announcement && <p>当时公告 v{h.announcement.revision} · {s.messages.find(m => m.id === h.announcement.messageId)?.text || "来源不可用"}</p>}<details><summary>查看该版本配置</summary><p>名称：{h.name} · 主题：{h.topic || "未填写"} · {h.archivedAt ? "已归档" : "活动"}</p><ul>{h.members.map((m,i)=><li key={i}><span translate="no">{m.principalId}</span> · {m.role} · {m.leftAt ? "已移除" : "当时有效"}</li>)}</ul><ul>{h.hooks.map(hook=><li key={hook.id}>{hook.targetUrl} · {hook.filter.kinds.join("、")} · {hook.revokedAt ? "已撤销" : hook.configuredEnabled ? "配置启用" : "配置停用"}</li>)}</ul></details></li>)}</ol> : <p>尚无管理变更记录。</p>}
        </Section>}
      </>}
      </>}
    </div>}
  </Modal>;
}

export function AgentChannelGrants({ agentId, toast }) {
  const s = useStore(), agent = s.agents.find(a => a.id === agentId);
  const [projectId,setProjectId] = useState(""); const [post,setPost] = useState(false); const [reason,setReason] = useState(""); const [error,setError] = useState("");
  const [expected,setExpected] = useState(null);
  const problem = agentChannelGrantProblem(s,agentId), grants = s.channelProjectGrants.filter(g => g.agentId === agentId);
  const choose = id => {const g=grants.find(g=>g.projectId===id);setProjectId(id);setPost(g?.permissions.includes("channel_post") || false);setExpected({tenantId:s.activeTenant,agentId,agentRevision:agent.revision || 1,projectId:id,revision:g?.revision || 0});};
  const act = action => {try {if(!expected)throw Error("请选择项目并核对授权版本。");changeAgentChannelGrant({...expected,action,reason,permissions:post?["channel_read","channel_post"]:["channel_read"]});setReason("");setExpected(null);setProjectId("");setError("");toast(action === "revoke" ? "项目会话授权已撤销，历史保留" : "项目会话授权已保存，不授予任务或文件权限");}catch(e){setError({message:e.message});}};
  return <Section title="项目会话授权">
    <Notice>由本地 Human 负责人显式授权选定项目的频道读取 / 发言；任务编组不等于频道成员。此授权不包括任务、文件、工具或其他项目。当前本地身份：林予（H-lin）。</Notice>
    {problem && <Notice tone="warn">{problem}</Notice>}
    <FormError error={error} />
    <ul className="channel-member-list">{grants.map(g => <li key={g.id}><div><b>{s.projects.find(p=>p.id===g.projectId)?.name || "原项目已不可用"}</b><small>{g.permissions.join(" · ")} · v{g.revision} · {g.revokedAt ? "已撤销" : ["退役","已退役","待批准"].includes(agent.status) ? "主体失效，授权不生效" : "有效"}</small><small>授权者 {g.grantorId} · {formatTimestamp(g.time)}</small><details><summary>授权历史（{g.history.length}）</summary>{g.history.map((h,i)=><p key={i}>{h.action} · {h.reason} · {h.actorId} · {formatTimestamp(h.time)}<br />此前权限：{h.previous?.permissions.join(" · ") || "无授权"}{h.previous?.revokedAt ? "（已撤销）" : ""}</p>)}</details></div>{!problem && <Button onClick={()=>choose(g.projectId)}>核对 / 修改 / 撤销</Button>}</li>)}</ul>
    {!problem && <>
      <Field label="授权项目" value={projectId} onChange={choose} options={[{value:"",label:"请选择项目"},...s.projects.filter(p=>p.id!=="private"&&!channelReadProblem(s,p.id)).map(p=>({value:p.id,label:p.name}))]} />
      <p>读取：channel_read（必选）</p><label className="channel-check"><input type="checkbox" checked={post} onChange={e=>setPost(e.target.checked)} />同时授权频道发言 channel_post</label>
      <Field label="授权或撤销说明" value={reason} onChange={setReason} type="textarea" required />
      <div className="actions"><Button disabled={!expected || s.offline || ["退役","已退役","待批准"].includes(agent.status)} onClick={()=>act("grant")}>保存明确授权</Button><Button disabled={!expected || s.offline || !grants.some(g=>g.projectId===projectId&&!g.revokedAt)} onClick={()=>act("revoke")}>撤销此项目会话授权</Button></div>
    </>}
  </Section>;
}
