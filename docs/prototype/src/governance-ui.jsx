import React, { useState } from 'react';
import { Section, Field, Button, Notice, Badge } from './ui';
import { useStore, governanceApprovalCommand, governanceHumanCommand, approvalSourceProblem } from './store';
import { governanceHumans, humanResponsibilities, approvalTally } from './governance';
const choices = items => [{value:'',label:'请选择'},...items.map(p=>({value:p.id,label:p.name}))];
const toggle = (items,id) => items.includes(id)?items.filter(x=>x!==id):[...items,id];
export function ApprovalOrchestration({record}) {
  const s=useStore(), humans=governanceHumans(s), flow=record.orchestration;
  const [form,setForm]=useState({mode:'并行',strategy:'ALL',humanIds:[],roles:[],projectId:'',quorum:1,timeoutSeconds:3600,timeoutAction:'提醒并保持待处理',delegateId:'',escalationId:''});
  const [participantId,setParticipant]=useState(''),[actorId,setActor]=useState('H-lin'),[reason,setReason]=useState(''),[simulate,setSimulate]=useState(false),[error,setError]=useState('');
  const set=(key,value)=>setForm(f=>({...f,[key]:value}));
  const run=(action,extra={})=>{try{governanceApprovalCommand({tenantId:s.activeTenant,id:record.id,revision:record.revision||1,reason,action,...form,participantId,actorId,simulate,...extra});setReason('');setError('');}catch(e){setError(e.message);}};
  const blocked=s.role!=='owner'||s.offline||record.status!=='待审核'||approvalSourceProblem(s,record);
  const tally=approvalTally(record);
  return <Section title="多人审批规则与决定">
    <Notice>冻结参与名单和规则后逐项决定；最终结果仍使用本页的批准、拒绝或退回按钮。其他 Human 的决定由当前负责人显式模拟，未生成真实签名。</Notice>
    {error&&<p role="alert">{error}</p>}
    {!flow?<>
      <Field label="审批阶段" value={form.mode} onChange={v=>set('mode',v)} options={['顺序','并行']} />
      <Field label="通过策略" value={form.strategy} onChange={v=>set('strategy',v)} options={[{value:'ALL',label:'全员通过'},{value:'ANY',label:'任一参与者通过'},{value:'QUORUM',label:'至少指定人数通过'}]} />
      <fieldset><legend>按顺序选择审批 Human</legend>{humans.map(p=><label className="check-label" key={p.id}><input type="checkbox" checked={form.humanIds.includes(p.id)} onChange={()=>set('humanIds',toggle(form.humanIds,p.id))}/>{p.name}{form.humanIds.includes(p.id)?` · 第 ${form.humanIds.indexOf(p.id)+1} 位`:''}</label>)}</fieldset>
      <Field label="角色来源项目" value={form.projectId} onChange={v=>{set('projectId',v);set('roles',[]);}} options={choices(s.projects.filter(p=>!p.archivedAt))}/>
      <fieldset><legend>追加角色的当前 Human，保存时冻结名单</legend>{[...new Set(s.organization.projectRoles.filter(r=>r.projectId===form.projectId).map(r=>r.role))].map(role=><label className="check-label" key={role}><input type="checkbox" checked={form.roles.includes(role)} onChange={()=>set('roles',toggle(form.roles,role))}/>{role}</label>)}</fieldset>
      {form.strategy==='QUORUM'&&<Field label="法定人数" type="number" min="1" value={form.quorum} onChange={v=>set('quorum',v)}/>}
      <Field label="审批时限（秒）" type="number" min="1" value={form.timeoutSeconds} onChange={v=>set('timeoutSeconds',v)}/>
      <Field label="超时处理" value={form.timeoutAction} onChange={v=>set('timeoutAction',v)} options={['提醒并保持待处理','请求负责人升级','到期终止等待']}/>
      <Field label="代理 Human（可不设置）" value={form.delegateId} onChange={v=>set('delegateId',v)} options={choices(humans)}/>
      {form.timeoutAction==='请求负责人升级'&&<Field label="升级接收 Human" value={form.escalationId} onChange={v=>set('escalationId',v)} options={choices(humans)}/>}
    </>:<>
      <p>规则 v{flow.ruleVersion} · {flow.mode} · {flow.strategy} · 需要 {tally.required} 票 · 当前批准 {tally.approved} 票</p>
      <p>固定名单：{flow.participantSnapshot.map(p=>p.name).join(' → ')}；截止 {flow.deadline}</p>
      <p>超时：{flow.timeoutAction}；代理：{flow.delegateId||'无'}；升级接收：{flow.escalationId||'无'}</p>
      {tally.result&&<Notice>规则结果：{tally.result}。请核对依据后使用本页对应按钮完成固定决定。</Notice>}
      {flow.expiredAt&&<Notice tone="warn">等待已超时并处置；旧票不可提交。{flow.timeoutAction==='到期终止等待'?'请退回补充。':'明确确认后可按原时限继续等待。'}</Notice>}
      <ul>{flow.votes.map(v=><li key={v.participantId}>{v.participantId}：{v.choice} · 实际记录人 {v.recordedBy} · 模拟主体 {v.actorId}{v.delegated?'（代理）':''} · {v.reason} · {v.time}</li>)}</ul>
      <Field label="所代表的固定参与者" value={participantId} onChange={setParticipant} options={choices(flow.participantSnapshot)}/>
      <Field label="本地模拟决定主体" value={actorId} onChange={setActor} options={choices(humans)}/>
      <label className="check-label"><input type="checkbox" checked={simulate} onChange={e=>setSimulate(e.target.checked)}/>明确记录本地模拟，不代表其他 Human 已真实签署</label>
      <details><summary>超时与升级记录</summary>{flow.events.map((event,index)=><p key={index}>{event.action} · {event.recipientId||event.actorId||''} · {event.reason} · {event.time}</p>)}</details>
    </>}
    <Field label="审批操作依据" type="textarea" value={reason} onChange={setReason} required/>
    {!flow?<Button disabled={!!blocked||!reason.trim()} onClick={()=>run('configure')}>冻结多人审批规则</Button>:<div className="actions">
      <Button disabled={!!blocked||!reason.trim()||!!tally.result||!!flow.expiredAt} onClick={()=>run('vote',{choice:'已批准'})}>记录参与者批准</Button>
      <Button disabled={!!blocked||!reason.trim()||!!tally.result||!!flow.expiredAt} onClick={()=>run('vote',{choice:'已拒绝'})}>记录参与者拒绝</Button>
      <Button disabled={!!blocked||!reason.trim()||!!flow.expiredAt||!!tally.result} onClick={()=>run('timeout')}>核对并处置超时</Button>
      {flow.expiredAt&&flow.timeoutAction!=='到期终止等待'&&<Button disabled={!!blocked||!reason.trim()} onClick={()=>run('resume')}>明确继续等待</Button>}
    </div>}
  </Section>;
}
export function HumanLifecycle({memberId}) {
  const s=useStore(),humans=governanceHumans(s),[kind,setKind]=useState('离职'),[targetNodeId,setTarget]=useState(''),[reason,setReason]=useState(''),[successorId,setSuccessor]=useState(''),[confirmed,setConfirmed]=useState(false),[error,setError]=useState('');
  const changes=(s.organization.memberChanges||[]).filter(c=>c.memberId===memberId),current=changes.find(c=>c.status==='待接管'),rows=humanResponsibilities(s,memberId);
  const disabled=s.role!=='owner'||s.offline||memberId==='H-lin';
  const run=(action,extra={})=>{try{governanceHumanCommand({tenantId:s.activeTenant,memberId,id:current?.id,revision:current?.revision,action,kind,targetNodeId,reason,successorId,confirmed,...extra});setError('');setReason('');setConfirmed(false);}catch(e){setError(e.message);}};
  return <Section title="离职 / 调岗与责任接管">
    <Notice>开始变更立即停用此成员的本地访问；固定历史不改写。逐项接管后才能完成。会话与凭据撤销只记录本地模拟结果。</Notice>
    {error&&<p role="alert">{error}</p>}
    {memberId==='H-lin'&&<p>当前负责人不能在自己的会话内撤销自身；须由另一位有权负责人处理。</p>}
    {!current?<><Field label="人员变更类型" value={kind} onChange={setKind} options={['离职','调岗']}/>{kind==='调岗'&&<Field label="调入组织" value={targetNodeId} onChange={setTarget} options={choices(s.organization.nodes)}/>}</>:<><Badge>{current.kind} · {current.status}</Badge><p>{current.access.message}</p><p>本地会话撤销：{current.access.sessionRevokedAt}；凭据撤销模拟：{current.access.credentialRevokedAt}</p>
      <Field label="责任接管 Human" value={successorId} onChange={setSuccessor} options={choices(humans.filter(p=>p.id!==memberId))}/>
      <label className="check-label"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>负责人明确确认本地接管记录，不冒充接收人真实签名</label></>}
    <Field label="人员变更 / 接管依据" type="textarea" value={reason} onChange={setReason} required/>
    <ul>{rows.map(row=><li key={row.key}>{row.label}{current&&(['work','projects','nodes','agents'].includes(row.kind)?<Button disabled={disabled||!confirmed||!successorId||!reason.trim()} onClick={()=>run('handoff',{key:row.key,expectedValue:row.value})}>确认接管此责任</Button>:<a className="btn" href={`#/${row.kind}/${row.id}`} target="_blank" rel="noopener noreferrer">在原对象修订负责人并审核</a>)}</li>)}</ul>
    {!rows.length&&<p>没有尚未接管的活动责任。</p>}
    {!current?<Button disabled={disabled||!reason.trim()||!humans.some(p=>p.id===memberId)} onClick={()=>run('begin')}>开始变更并停用访问</Button>:<Button disabled={disabled||!!rows.length||!reason.trim()} onClick={()=>run('complete')}>核对并完成人员变更</Button>}
    <details><summary>人员变更历史 · {changes.length}</summary>{changes.map(c=><article key={c.id}><p>{c.kind} · {c.status} · {c.reason} · {c.time}</p><p>{c.access.message}</p>{c.handoffs.map((h,i)=><p key={i}>{h.label} → {h.successorId} · {h.reason} · 本地模拟</p>)}</article>)}</details>
  </Section>;
}
