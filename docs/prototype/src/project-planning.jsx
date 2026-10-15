import React, { useCallback, useState } from "react";
import { useStore, read, update, assertWrite, id, projectRecordWriteProblem } from "./store";
import { DataList } from "./data-list";
import { useViewState } from "./view-state";
import { PageHead, Modal, ActionForm, Field, Button, Link, Notice, Section, Badge, Empty } from "./ui";
import { visibleChannels, visibleChannelMessages, channelPeople, directMessagePeer } from "./channels";
import { planningKinds, planningRows, planningReadProblem, planningGoals, planningSourceProblem, planningCommandOn } from "./project-planning.js";
import { formatTimestamp } from "./formatting";

export function savePlanningRecord(kind,input) {
  assertWrite();let result;
  update(state=>{result=planningCommandOn(state,kind,input,{makeId:id});});
  return result;
}
const path=(kind,record)=>`/${kind}/${encodeURIComponent(record.id)}?tenant=${encodeURIComponent(record.tenantId)}&scope=${encodeURIComponent(record.projectId)}`;
export function ProjectPlanningPage({kind,recordId,toast}) {
  const s=useStore(),label=planningKinds[kind];
  const [viewed,setViewed]=useViewState(`${kind}.detail`,""),[editing,setEditing]=useState(null),[action,setAction]=useState(null),[,refreshView]=useState(0);
  const tenantId=s.activeTenant,role=s.role,projectId=s.project;
  const recheck=useCallback(()=>{const current=read();if(current.activeTenant!==tenantId || current.role!==role || current.project!==projectId || current.role==="external")throw Error("访问范围已变化，请重新打开列表。");return current;},[tenantId,role,projectId]);
  const refresh=useCallback(()=>{recheck();refreshView(v=>v+1);},[recheck]);
  const authorizeExport=useCallback(payload=>{const rows=planningRows(recheck(),kind);if(payload.records.some(item=>!rows.some(row=>row.id===item.id&&row.revision===item.version)))throw Error("记录或权限已变化，请重新导出。");},[recheck,kind]);
  if(!label)return <Empty title="未知项目记录"/>;
  if(role==="external")return <Empty title="当前身份无权访问"/>;
  const rows=planningRows(s,kind),selectedId=recordId || viewed,record=(s.resources[kind] || []).find(r=>r.id===selectedId);
  const close=()=>{setViewed("");if(recordId)location.hash=`/${kind}?tenant=${encodeURIComponent(tenantId)}&scope=${encodeURIComponent(projectId)}`;};
  const projects=s.projects.filter(p=>!p.archivedAt&&!planningReadProblem(s,{projectId:p.id,tenantId}));
  const openEdit=record=>setEditing(record ? structuredClone(record) : {tenantId,projectId:projects.some(p=>p.id===projectId)?projectId:"",name:"",ownerId:"",goalId:"",description:"",acceptance:"",workIds:[],source:null,startDate:"",endDate:"",entryCriteria:"",exitCriteria:""});
  return <>
    <PageHead title={label} subtitle={kind==="requirements"?"记录需求原件、验收标准与确认历史，关联已有目标和任务。":"明确迭代时间、任务范围与准入准出，完成前核对已有任务。"}/>
    <Notice>需求记录约定与验收标准；迭代组织已有任务的排期。归档后可查阅原件与变更历史。{projectId==="private"?"私人范围没有项目需求或迭代，请选择明确项目。":""}</Notice>
    <DataList rows={rows} viewId={kind} scopeKey={`${tenantId}:${projectId}:${role}`} title={`${label}列表`} defaultSort="name"
      searchText={r=>`${r.id} ${r.name} ${r.description} ${r.owner} ${r.status}`}
      onCreate={()=>openEdit(null)} createLabel={`新建${label}`} createDisabledReason={s.offline?"当前离线":!projects.length||projectId==="private"?"请选择可维护项目":""}
      onRefresh={refresh} offline={s.offline} editing={Boolean(editing||action)} onExport={authorizeExport} exportSource={`${kind}:${tenantId}:${projectId}`}
      columns={[
        {key:"name",label:label,value:r=>r.name,exportValue:r=>r.name},
        {key:"project",label:"项目",value:r=>s.projects.find(p=>p.id===r.projectId)?.name || r.projectId,exportValue:r=>r.projectId},
        {key:"owner",label:"Human负责人",value:r=>r.owner,exportValue:r=>r.ownerId},
        {key:"status",label:"状态",value:r=>r.status,exportValue:r=>r.status,render:r=><Badge>{r.status}</Badge>},
        {key:"version",label:"修订",value:r=>r.revision,exportValue:r=>r.revision},
        {key:"tasks",label:"关联任务数",value:r=>r.workIds.length,exportValue:r=>r.workIds.length}
      ]} renderActions={r=><><Button onClick={()=>setViewed(r.id)}>查看详情</Button><Link className="btn" to={path(kind,r)} target="_blank" rel="noopener noreferrer">新标签页打开</Link></>}
      emptyText={`当前范围没有${label}记录，点击“新建${label}”开始。`}/>
    {selectedId&&<Modal title={`${label}详情`} wide onClose={close} headerActions={record&&!planningReadProblem(s,record)&&<Link className="btn" to={path(kind,record)} target="_blank" rel="noopener noreferrer">新标签页打开</Link>}>
      {planningReadProblem(s,record)?<Empty title="记录不可访问" text={planningReadProblem(s,record)}/>:<>
        <h2>{record.name}</h2><p>{s.projects.find(p=>p.id===record.projectId)?.name} · {record.owner} · 修订 {record.revision} · {record.status}</p>
        <Notice>此详情属于上述明确项目，不随列表筛选改写其范围。</Notice>
        <p style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{record.description}</p>
        {kind==="requirements"?<Section title="验收标准"><p style={{whiteSpace:"pre-wrap"}}>{record.acceptance}</p>{record.confirmedAt&&<p>Human {record.confirmedBy} 于 {formatTimestamp(record.confirmedAt)} 确认此修订。</p>}</Section>:<Section title="时间与条件"><p>{record.startDate} — {record.endDate}</p><p>准入：{record.entryCriteria}</p><p>准出：{record.exitCriteria}</p>{record.entryVerification&&<p>准入核对：{record.entryVerification}</p>}{record.exitVerification&&<p>准出核对：{record.exitVerification}</p>}</Section>}
        <Section title="关联目标与任务">
          {record.goalId ? planningGoals(s,record.projectId).some(g=>g.id===record.goalId)?<Link to={`/goals/${encodeURIComponent(record.goalId)}`} target="_blank" rel="noopener noreferrer">查看关联目标 {record.goalId}</Link>:<p>原关联目标当前不可访问。</p>:<p>尚未关联目标。</p>}
          {record.workIds.map(workId=>{const work=s.work.find(w=>w.id===workId&&w.project===record.projectId);return <p key={workId}>{work?<Link to={`/p/${encodeURIComponent(work.project)}/work-items/${encodeURIComponent(work.id)}`} target="_blank" rel="noopener noreferrer">{work.title} · {work.status}</Link>:`${workId} · 原任务已不存在或已移出项目`}</p>;})}
          {!record.workIds.length&&<p>尚未关联已有任务。</p>}
        </Section>
        {record.source?.channelId&&<Section title="来源会话">{planningSourceProblem(s,record.source,record.projectId)?<Notice>来源当前不可访问或已撤回；保留固定引用，不显示旧正文。</Notice>:<Link target="_blank" rel="noopener noreferrer" to={`/chat?channel_id=${encodeURIComponent(record.source.channelId)}${record.source.messageId?`&message_id=${encodeURIComponent(record.source.messageId)}`:""}`}>新标签页查看来源会话</Link>}</Section>}
        <div className="actions">{!record.archivedAt&&<>
          {record.status!=="已完成"&&<Button disabled={s.offline||Boolean(projectRecordWriteProblem(s,record))} onClick={()=>openEdit(record)}>修订{label}</Button>}
          {(kind==="requirements"&&record.status==="草稿"||kind==="iterations"&&["草稿","进行中"].includes(record.status))&&<Button disabled={s.offline||Boolean(projectRecordWriteProblem(s,record))} onClick={()=>setAction({...record,action:kind==="requirements"?"confirm":record.status==="草稿"?"start":"complete"})}>{kind==="requirements"?"确认需求":record.status==="草稿"?"开始迭代":"完成迭代"}</Button>}
          <Button disabled={s.offline||Boolean(projectRecordWriteProblem(s,record))} onClick={()=>setAction({...record,action:"archive"})}>归档{label}</Button>
        </>}</div>
        <Section title="变更历史">{record.history.map((entry,index)=><details key={index}><summary>{formatTimestamp(entry.time)} · {{save:"保存修订",confirm:"确认需求",start:"开始迭代",complete:"完成迭代",archive:"归档"}[entry.action] || entry.action} · Human {entry.actorId}</summary><p>{entry.reason}</p>{entry.before&&<div className="planning-history"><p>原修订 {entry.before.revision} · {entry.before.name} · {entry.before.status}</p><p style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{entry.before.description}</p>{kind==="requirements"?<p style={{whiteSpace:"pre-wrap"}}>原验收标准：{entry.before.acceptance}</p>:<><p>原排期：{entry.before.startDate} — {entry.before.endDate}</p><p>原准入：{entry.before.entryCriteria}</p><p>原准出：{entry.before.exitCriteria}</p></>}<p>原关联任务：{entry.before.workIds?.join("、")||"无"}</p></div>}</details>)}</Section>
      </>}
    </Modal>}
    {editing&&<PlanningEditor key={`${kind}:${editing.tenantId}:${editing.id||"new"}`} kind={kind} initial={editing} onClose={()=>setEditing(null)} onSaved={id=>{setEditing(null);setViewed(id);toast(`${label}修订已保存`);}}/>}
    {action&&<PlanningAction key={`${action.id}:${action.action}`} kind={kind} record={action} onClose={()=>setAction(null)} onSaved={()=>{setAction(null);toast("状态与变更历史已保存");}}/>}
  </>;
}
function PlanningEditor({kind,initial,onClose,onSaved}) {
  const s=useStore(),[form,setForm]=useState({...initial,changeReason:""});
  const set=patch=>setForm(old=>({...old,...patch}));
  const projects=s.projects.filter(p=>!p.archivedAt&&!planningReadProblem(s,{projectId:p.id,tenantId:s.activeTenant}));
  const channels=visibleChannels(s).filter(c=>!c.projectId||c.projectId===form.projectId);
  return <Modal title={`${initial.id?"修订":"新建"}${planningKinds[kind]}`} wide onClose={onClose} draftValue={form}><ActionForm label="保存修订" onCancel={onClose} onSubmit={({dismiss})=>{const id=savePlanningRecord(kind,{...form,action:"save"});dismiss(()=>onSaved(id));}}>
    <Field label="项目" required disabled={Boolean(initial.id)||s.project!=="all"} value={form.projectId} options={[{value:"",label:"请选择明确项目"},...projects.map(p=>({value:p.id,label:p.name}))]} onChange={projectId=>set({projectId,goalId:"",workIds:[],source:null})}/>
    <Field label="名称" required value={form.name} onChange={name=>set({name})}/>
    <Field label="Human负责人" required value={form.ownerId} options={[{value:"",label:"请选择负责人"},...channelPeople(s).filter(p=>p.kind==="Human"&&p.available&&!p.external).map(p=>({value:p.id,label:p.name}))]} onChange={ownerId=>set({ownerId})}/>
    <Field label="关联目标" value={form.goalId||""} options={[{value:"",label:"暂不关联"},...planningGoals(s,form.projectId).map(g=>({value:g.id,label:g.name}))]} onChange={goalId=>set({goalId})}/>
    <Field label="说明" type="textarea" required value={form.description} onChange={description=>set({description})}/>
    {kind==="requirements"?<>
      <Field label="验收标准" type="textarea" required value={form.acceptance} onChange={acceptance=>set({acceptance})}/>
      <Field label="来源会话" value={form.source?.channelId||""} options={[{value:"",label:"手动录入，无会话来源"},...channels.map(c=>({value:c.id,label:c.kind==="DIRECT"?`私信 · ${directMessagePeer(s,c).name}`:c.name}))]} onChange={channelId=>set({source:channelId?{channelId,messageId:null}:null})}/>
      {form.source?.channelId&&<Field label="来源消息" value={form.source.messageId||""} options={[{value:"",label:"会话上下文，未指定消息"},...visibleChannelMessages(s,form.source.channelId).filter(m=>!m.recalled).map(m=>({value:m.id,label:`${m.id} · ${(m.text||"附件消息").slice(0,70)}`}))]} onChange={messageId=>set({source:{...form.source,messageId}})}/>}
      <Notice>修改已确认需求会形成新草稿，需要重新确认；原确认与原件保留在历史。</Notice>
    </>:<><div className="form-grid"><Field label="开始日期" type="date" required value={form.startDate} onChange={startDate=>set({startDate})}/><Field label="结束日期" type="date" required value={form.endDate} onChange={endDate=>set({endDate})}/></div><Field label="准入条件" type="textarea" required value={form.entryCriteria} onChange={entryCriteria=>set({entryCriteria})}/><Field label="准出条件" type="textarea" required value={form.exitCriteria} onChange={exitCriteria=>set({exitCriteria})}/></>}
    <fieldset><legend>关联同项目已有任务</legend>{s.work.filter(w=>w.project===form.projectId).map(w=><label className="check-label" key={w.id}><input type="checkbox" checked={form.workIds.includes(w.id)} onChange={event=>set({workIds:event.target.checked?[...form.workIds,w.id]:form.workIds.filter(id=>id!==w.id)})}/>{w.id} · {w.title} · {w.status}</label>)}</fieldset>
    <Field label="变更理由" type="textarea" required value={form.changeReason} onChange={changeReason=>set({changeReason})}/>
  </ActionForm></Modal>;
}
function PlanningAction({kind,record,onClose,onSaved}) {
  const [reason,setReason]=useState(""),[verification,setVerification]=useState("");
  const label={confirm:"确认需求",start:"开始迭代",complete:"完成迭代",archive:"归档记录"}[record.action];
  return <Modal title={label} onClose={onClose} draftValue={{reason,verification}}><ActionForm label={label} onCancel={onClose} onSubmit={({dismiss})=>{savePlanningRecord(kind,{...record,changeReason:reason,verification});dismiss(onSaved);}}>
    <p>{record.name} · 修订 {record.revision}</p>
    <Notice>{record.action==="archive"?"归档后只读，保留历史及任务引用，不会取消任务。":record.action==="complete"?"所有范围内任务须已完成，并明确核对准出条件；此操作不代替任务验收。":"保存 Human 核对记录，不启动任务或执行。"}</Notice>
    {["start","complete"].includes(record.action)&&<><p>{record.action==="start"?record.entryCriteria:record.exitCriteria}</p><Field label="条件核对结果" type="textarea" required value={verification} onChange={setVerification}/></>}
    <Field label="变更理由" type="textarea" required value={reason} onChange={setReason}/>
  </ActionForm></Modal>;
}
