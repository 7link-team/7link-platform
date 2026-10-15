import { executionBudgetPolicies } from "./budget";
import { useViewState } from "./view-state";
import React, { useState, useEffect } from 'react';
import { useStore, beginWorkDraft, workCommand, requestExecutionInteraction, decideExecutionInteraction, deliverExecutionInteraction, expireExecutionInteraction, completeExecutionTurn, createExecution, createExecutionBranch, executionCommand, resolveExecutionInput, linkAdhocExecution } from './store';
import { executionFailureKinds, executionFailureActions, inputApplicationStates, interactionStates, interactionResponseProblem, executionMaintenanceProblem, availableExecutionCommands, executionPath, executionRecord, executionReadProblem, executionKinds, executionStates } from './executions';
import { Button, Badge, Section, Modal, Field, ActionForm, Notice, Empty, Link } from './ui';
import { formatTimestamp } from './formatting';

export function ExecutionCreate({ work, parent, branchReason, toast }) {
  const s=useStore(),[creating,setCreating]=useViewState(parent?`execution-create.branch.${parent.id}.${branchReason}`:"execution-create.work",false),[nodeId,setNodeId]=useState(''),[reason,setReason]=useState(''),[created,setCreated]=useState(null),[parentRevision,setParentRevision]=useState(parent?.revision),[authorizationMode,setAuthorizationMode]=useState('READ_ONLY');
  const node=s.nodes.find(n=>n.id===nodeId),label=parent ? branchReason==='RETRY_INFRA'?'创建故障重试分支':'创建策略替代分支' : '创建任务交互执行';
  if (!work) return null;
  return <>
    <Button disabled={Boolean(work && s.projects.find(p=>p.id===work.project)?.archivedAt) || s.offline || s.role==='external' || Boolean(parent && executionMaintenanceProblem(s,parent))} onClick={()=>{setNodeId('');setReason('');setParentRevision(parent?.revision);setAuthorizationMode('READ_ONLY');setCreating(true);}}>{label}</Button>
    {creating && <Modal title={label} draftValue={{nodeId,reason}} onClose={()=>setCreating(false)}><ActionForm label="创建等待启动的执行" onCancel={()=>setCreating(false)} onSubmit={({dismiss})=>{const args={tenantId:s.activeTenant,kind:'INTERACTIVE',projectId:work.project,workId:work.id,nodeId,runtime:node?.runtime,authorizationMode:node?.runtime==='Claude Code'?authorizationMode:'READ_ONLY'};const e=parent?createExecutionBranch({...args,executionId:parent.id,revision:parentRevision,branchReason,reason}):createExecution(args);setCreated(e);dismiss();toast(`已创建 ${e.id}，尚未启动；从执行会话列表查看`);}}>
      <p>{work.id} · {work.title} · 固定工作输入 v{work.version}</p>
      <Field label="执行节点与已登记 Runtime" value={nodeId} onChange={setNodeId} required options={[{value:'',label:'请选择在线节点'},...s.nodes.filter(n=>n.status==='在线').map(n=>({value:n.id,label:`${n.name} · ${n.runtime}`}))]} />
      {node?.runtime==='Claude Code' && <Field label="授权场景（本地模拟）" value={authorizationMode} onChange={setAuthorizationMode} options={[{value:'READ_ONLY',label:'只读场景'},{value:'ON_REQUEST',label:'逐项请求Human授权'}]} />}
      {parent && <><p>父执行 {parent.id} · {executionStates[parent.state]} · {executionKinds[parent.kind]}</p><Field label="分支原因与副作用核对" type="textarea" value={reason} onChange={setReason} required /><Notice>{branchReason==='RETRY_INFRA'?'对暂停或中断父执行建立故障重试，会将父执行标为被替代；原输入与旧成果不会复制到新执行。':'策略替代只允许父执行已经结束；不会撤销此前的工具或外部副作用。'}新分支使用当前已确认工作输入，请核对版本差异。</Notice></>}
      <Notice>创建只产生等待启动的本地场景。交互执行要求你已被分派为此工作的执行者；已有未结束的任务执行须先处置。此原型不连接节点。</Notice>
    </ActionForm></Modal>}
    {created && <Link className="btn" to={executionPath(created)} target="_blank" rel="noopener noreferrer">新标签页查看 {created.id}</Link>}
  </>;
}

export function ExecutionRegistry({ work, toast }) {
  const s = useStore();
  const records=(s.executions || []).filter(e=>e.workId===work.id && !executionReadProblem(s,e.id));
  return <Section title="执行记录" extra={<ExecutionCreate work={work} toast={toast} />}>
    <Notice>一次任务可以保留多次执行历史，每次有独立编号、固定输入和成果清单。创建不会启动真实节点。</Notice>
    {!records.length && <Empty title="尚无执行记录" text="确认计划与工作流版本后，可选择节点创建任务交互执行。" />}
    {records.map(e=><article key={e.id} className="execution-record" data-segment={`execution:${e.id}`}><div className="split"><b>{e.id}</b><Badge>{executionStates[e.state]}</Badge></div><p>{executionKinds[e.kind]} · {e.nodeId} · {e.runtime} · 固定工作输入 v{e.workVersion} · 工作流 v{e.workflowVersion}</p><p className="muted">{e.parentExecutionId ? `父执行 ${e.parentExecutionId}` : '无父执行'} · {e.turns.length} 个输入轮次 · 本地模拟</p><Link className="btn" to={executionPath(e)} target="_blank" rel="noopener noreferrer">新标签页查看此执行</Link></article>)}
  </Section>;
}

export function ExecutionProcess({ executionId, focusedTurnId, toast, onWork }) {
  const s=useStore(), e=executionRecord(s,executionId);
  const [operation,setOperation]=useViewState(`execution-action.${executionId}`,null),[reason,setReason]=useState(''),[workId,setWorkId]=useState(''),[acknowledgeSideEffects,setAcknowledgeSideEffects]=useState(false),[failureKind,setFailureKind]=useState(''),[maxEstimatedCost,setMaxEstimatedCost]=useState('');
  if (!e) return <Empty title="执行不存在" />;
  const mutable=!executionMaintenanceProblem(s,e)&&!s.offline&&!s.projects.find(p=>p.id===e.projectId)?.archivedAt;
  const commands={CREATED:[['start','模拟节点接受并启动'],['cancel','模拟终止执行']],RUNNING:[['pause','模拟暂停'],['interrupt','模拟连接中断'],['fail','模拟执行失败'],['cancel','模拟终止执行']],PAUSED:[['resume','模拟恢复'],['finish','模拟结束交互会话'],['fail','模拟标记失败'],['cancel','模拟终止执行']],INTERRUPTED:[['resume','核对副作用并模拟恢复'],['fail','模拟标记失败'],['cancel','模拟终止执行']]};
  const open=op=>{setReason('');setWorkId('');setAcknowledgeSideEffects(false);setFailureKind('');setOperation({...op,tenantId:s.activeTenant,executionId:e.id,revision:e.revision});};
  return <>
    <Section title={`${executionKinds[e.kind]} · ${e.id}`} extra={<Badge>{executionStates[e.state]}</Badge>}>
      <Notice>本地执行场景。事件、输入及处理回执均未发送到真实 Runtime；执行结束不等于成果验收通过，关闭视图不终止执行。</Notice>
      <p>项目 {s.projects.find(p=>p.id===e.projectId)?.name} · 节点 {s.nodes.find(n=>n.id===e.nodeId)?.name || e.nodeId} · {e.runtime}</p>
      <p>{e.workId ? <>工作 {e.workId} · 固定输入 v{e.workVersion} · 工作流 {e.workflowId} / v{e.workflowVersion} <Button onClick={onWork}>查看工作</Button></> : '未关联工作项 · 临时探索输入仍归属于此执行'}</p>
      {e.parentExecutionId && <p>父执行 <Link to={`/chat?execution_id=${encodeURIComponent(e.parentExecutionId)}`} target="_blank" rel="noopener noreferrer">{e.parentExecutionId}</Link> · {e.branchReason} · {e.branchReasonText}</p>}
      <div className="actions">{(mutable?commands[e.state]||[]:[]).filter(([action])=>availableExecutionCommands(e).includes(action)).map(([action,label])=><Button key={action} onClick={()=>open({kind:'command',action,label})}>{label}</Button>)}
        {mutable && e.kind==='ADHOC' && !e.workId && !['SUCCEEDED','FAILED','CANCELLED'].includes(e.state) && <Button onClick={()=>open({kind:'link',label:'正式关联工作项'})}>正式关联工作项</Button>}
        {mutable && e.workId && ['FAILED','CANCELLED','PAUSED','INTERRUPTED'].includes(e.state) && <ExecutionCreate work={s.work.find(w=>w.id===e.workId)} parent={e} branchReason="RETRY_INFRA" toast={toast} />}
        {mutable && e.workId && ['SUCCEEDED','FAILED','CANCELLED'].includes(e.state) && <ExecutionCreate work={s.work.find(w=>w.id===e.workId)} parent={e} branchReason="STRATEGY" toast={toast} />}
      </div>
    </Section>
    <ExecutionRecovery execution={e} toast={toast} onWork={onWork} />
    <Section title={`输入轮次与处理回执 · ${e.turns.length}`}>
      {!e.turns.length && <p className="muted">尚无输入轮次。只有运行中且具备输入能力的执行可以接收新指令。</p>}
      {e.turns.map(t=><article key={t.id} data-segment={`turn:${t.id}`} tabIndex={focusedTurnId===t.id ? -1 : undefined} className={`execution-record${focusedTurnId===t.id ? " message-location-highlight" : ""}`}><div className="split"><b>轮次 {t.seq}</b><Badge>{t.status==='PENDING'?'已提交 · 待处理':t.status==='ACCEPTED'?'本地模拟已采用':'本地模拟拒绝'}</Badge></div><p style={{whiteSpace:'pre-wrap'}}>{t.text}</p><small>{formatTimestamp(t.submittedAt)} · {t.id} · {t.runState==='COMPLETED'?'本地模拟轮次完成':'尚无轮次完成记录'}</small>{mutable && t.status==='ACCEPTED' && !t.runState && e.state==='RUNNING' && <Button onClick={()=>open({kind:'turn',turnId:t.id,label:'模拟轮次完成并暂停'})}>模拟轮次完成并暂停</Button>}<p>补充应用：{inputApplicationStates[t.applicationState] || '历史记录未区分应用状态'}</p>{t.applicationReceipts?.length>0&&<details><summary>应用回执历史</summary>{t.applicationReceipts.map((r,i)=><p key={i}>{inputApplicationStates[r.state]} · {r.reason} · {formatTimestamp(r.time)}</p>)}</details>}{t.receipt && <p>处理依据：{t.receipt.reason} · {formatTimestamp(t.receipt.time)}</p>}{mutable && t.status==='PENDING' && e.state==='RUNNING' && <div className="actions">{t.applicationState==='RECEIVED'&&<Button onClick={()=>open({kind:'receipt',turnId:t.id,outcome:'WAITING_BOUNDARY',label:'记录等待安全边界'})}>等待安全边界</Button>}<Button onClick={()=>open({kind:'receipt',turnId:t.id,outcome:'ACCEPTED',label:'模拟采用此轮次'})}>模拟采用</Button><Button onClick={()=>open({kind:'receipt',turnId:t.id,outcome:'REJECTED',label:'模拟拒绝此轮次'})}>模拟拒绝</Button></div>}</article>)}
    </Section>
    <ExecutionInteractions execution={e} toast={toast} />
    <Section title={`过程事件 · ${e.events.length}`}>
      {e.events.map(event=><p className="execution-event" key={event.id} data-segment={`event:${event.id}`}><b>#{event.seq} · {event.kind}</b><br/>{event.text}<br/><small>{formatTimestamp(event.time)}</small></p>)}
    </Section>
    {operation && <Modal title={operation.label} draftValue={{reason,workId,failureKind,maxEstimatedCost}} onClose={()=>setOperation(null)}><ActionForm label={operation.label} onCancel={()=>setOperation(null)} onSubmit={({dismiss})=>{const input={...operation,reason,workId,acknowledgeSideEffects,failureKind,maxEstimatedCost};if(operation.action==='fail'&&!failureKind)throw Error('请选择失败分类。');if(operation.kind==='receipt')resolveExecutionInput(input);else if(operation.kind==='turn')completeExecutionTurn(input);else if(operation.kind==='link')linkAdhocExecution(input);else executionCommand(input);dismiss();toast('本地场景已更新，未联系真实节点');}}>
      <p>执行 {e.id} · 当前 {executionStates[e.state]}</p>{operation.kind==='link' && <Field label="本项目工作项" value={workId} onChange={setWorkId} required options={[{value:'',label:'请选择已分派给自己的工作'},...s.work.filter(w=>w.project===e.projectId && w.team?.executorIds?.includes('H-lin') && ['进行中','等待'].includes(w.status)).map(w=>({value:w.id,label:`${w.id} · ${w.title}`}))]} />}
      {operation.action==='start'&&executionBudgetPolicies(s,e).length>0&&<Field label="本次执行最大预留费用（USD）" type="number" min="0.000001" step="any" required value={maxEstimatedCost} onChange={setMaxEstimatedCost} help="启动前同时检查所有预算与并发上限；未知实际费用会继续占额等待对账。"/>}
      {operation.action==='fail'&&<Field label="失败分类" required value={failureKind} onChange={setFailureKind} options={[{value:'',label:'请选择失败原因'},...Object.entries(executionFailureKinds).map(([value,label])=>({value,label}))]}/>}
      {operation.kind==='receipt'&&operation.outcome==='ACCEPTED'&&<Notice>请在操作依据记录本次已到达的安全边界及应用结果；这是明确的本地应用回执，Agent文字回复不代替此记录。</Notice>}
      {operation.action==='resume' && e.state==='INTERRUPTED' && <label><input type="checkbox" name="acknowledge-side-effects" autoComplete="off" checked={acknowledgeSideEffects} onChange={event=>setAcknowledgeSideEffects(event.target.checked)} />已核对中断前的工具和外部副作用，明确恢复此执行</label>}<Field label="操作依据" type="textarea" value={reason} onChange={setReason} required /><Notice>仅保存本地模拟状态与记录。未执行真实工具，不授予外部访问权限，不改变工作验收结果。</Notice>
    </ActionForm></Modal>}
  </>;
}

function ExecutionInteractions({execution:e,toast}){
 const s=useStore(),[operation,setOperation]=useViewState(`execution-interaction.${e.id}`,null),[title,setTitle]=useState(''),[message,setMessage]=useState(''),[details,setDetails]=useState(''),[response,setResponse]=useState(''),[clock,setClock]=useState(Date.now());
 const denied=interactionResponseProblem(s,e),turn=e.turns.filter(t=>t.status==='ACCEPTED'&&!t.runState).at(-1),active=e.state==='RUNNING',canRequest=active&&(e.kind==='AUTONOMOUS'||turn),mutable=!s.offline&&!s.projects.find(p=>p.id===e.projectId)?.archivedAt,maintenanceDenied=executionMaintenanceProblem(s,e),canMaintain=mutable&&!maintenanceDenied;
 const context=()=>({tenantId:s.activeTenant,executionId:e.id,revision:e.revision,dispatchEpoch:e.dispatchEpoch});
 useEffect(()=>{const timer=setInterval(()=>setClock(Date.now()),1000);return()=>clearInterval(timer);},[]);
 useEffect(()=>{if(!canMaintain||!active)return;const due=(e.interactions||[]).find(i=>i.state==='PENDING'&&i.dispatchEpoch===e.dispatchEpoch&&Date.parse(i.expiresAt)<=clock);if(due)try{expireExecutionInteraction({...context(),interactionId:due.id});}catch{/* A concurrent change is rechecked on the next clock tick. */}},[clock,e.id,e.revision,canMaintain,active]);
 const open=op=>{setTitle('');setMessage('');setDetails('');setResponse('{"answer":""}');setOperation({...context(),...op});};
 const simulate=(i,outcome)=>{try{if(outcome==='EXPIRED')expireExecutionInteraction({...context(),interactionId:i.id,simulateDeadline:true});else deliverExecutionInteraction({...context(),interactionId:i.id,outcome});toast('本地交互状态已更新，未联系真实Runtime');}catch(error){toast(error.message,true);}};
 return <Section title={`工具授权与输入请求 · ${(e.interactions||[]).length}`} extra={canMaintain&&canRequest&&<div className="actions">{e.capabilities?.runtimeInput&&<Button onClick={()=>open({kind:'request',interactionKind:'RUNTIME_INPUT',label:'模拟Runtime输入请求'})}>模拟Runtime输入请求</Button>}{e.capabilities?.toolPermission&&<Button onClick={()=>open({kind:'request',interactionKind:'TOOL_PERMISSION',label:'模拟工具授权请求'})}>模拟工具授权请求</Button>}</div>}>
  <Notice>请求来自此执行的派发 #{e.dispatchEpoch ?? '未记录'}，决定与送达分别记录。此处演示本地请求，允许一次不授予真实工具权限。{e.runtime==='Codex CLI'?'Codex CLI不支持Web工具审批。':''}</Notice>
  {(!Number.isSafeInteger(e.dispatchEpoch)||typeof e.capabilities?.runtimeInput!=='boolean'||typeof e.capabilities?.toolPermission!=='boolean')&&<Notice>此历史执行未记录派发纪元或交互能力，当前仅保留已有过程记录；不能推断它支持输入或工具授权，请创建明确的新执行场景。</Notice>}
  {!(e.interactions||[]).length&&<p className="muted">尚无交互请求。任务交互执行需先采用一个输入轮次；自主执行请求直接归属于执行。</p>}
  {(e.interactions||[]).map(i=>{const expired=i.state==='PENDING'&&Date.parse(i.expiresAt)<=clock;return <article className="execution-record" data-segment={`interaction:${i.id}`} key={i.id}>
   <div className="split"><b>{i.title}</b><Badge>{expired?'已到期 · 等待记录':i.state==='CANCELLED'&&i.closedReason==='DELIVERY_FAILED'?'未送达 · 旧决定已关闭':interactionStates[i.state]}</Badge></div>
   <p className="muted">{i.id} · {i.kind==='TOOL_PERMISSION'?'工具授权':'结构化输入'} · 派发 #{i.dispatchEpoch} · {i.turnId?`轮次 ${e.turns.find(t=>t.id===i.turnId)?.seq || i.turnId}`:'自主执行请求'} · 截止 {formatTimestamp(i.expiresAt)}</p>
   {i.message&&<p style={{whiteSpace:'pre-wrap'}}>{i.message}</p>}{i.details&&<pre className="codeblock" style={{maxHeight:180,overflow:'auto',whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{i.details}</pre>}
   {i.schemaJson&&<details><summary>查看固定输入结构（受支持的JSON Schema子集）</summary><pre className="codeblock">{JSON.stringify(i.schemaJson,null,2)}</pre></details>}
   {i.responseAction&&<p>本地决定：{i.responseAction==='ACCEPT'?i.kind==='TOOL_PERMISSION'?'允许一次':'提交输入':'拒绝'} · Human {i.responderId} · {formatTimestamp(i.decidedAt)}</p>}
   {i.responseContent&&<details><summary>查看本地已提交输入</summary><pre className="codeblock">{JSON.stringify(i.responseContent,null,2)}</pre></details>}
   {i.closedReason&&<p>关闭原因：{i.closedReason}；不会转投下一派发。</p>}
   {i.state==='PENDING'&&!expired&&active&&mutable&&i.dispatchEpoch===e.dispatchEpoch&&<><p className="muted">{denied||'仅有权Human可回应；保存决定后仍需Runtime送达回执。'}</p><div className="actions"><Button disabled={Boolean(denied)} onClick={()=>open({kind:'decision',interactionId:i.id,action:'DECLINE',label:'拒绝此请求'})}>拒绝此请求</Button><Button disabled={Boolean(denied)} onClick={()=>open({kind:'decision',interactionId:i.id,action:'ACCEPT',interactionKind:i.kind,label:i.kind==='TOOL_PERMISSION'?'允许一次':'提交结构化输入'})}>{i.kind==='TOOL_PERMISSION'?'允许一次':'提交结构化输入'}</Button>{canMaintain&&<Button onClick={()=>simulate(i,'EXPIRED')}>模拟时钟推进至到期</Button>}</div></>}
   {i.state==='DECIDED'&&active&&canMaintain&&i.dispatchEpoch===e.dispatchEpoch&&<div className="actions"><Button onClick={()=>simulate(i,'DELIVERED')}>模拟决定送达</Button><Button onClick={()=>simulate(i,'FAILED')}>模拟送达失败</Button></div>}
  </article>})}
  {operation&&<Modal title={operation.label} draftValue={{title,message,details,response}} onClose={()=>setOperation(null)}><ActionForm label={operation.kind==='request'?'创建本地模拟请求':'保存本地决定'} onCancel={()=>setOperation(null)} onSubmit={({dismiss})=>{
   if(operation.kind==='request')requestExecutionInteraction({...operation,nativeRequestId:crypto.randomUUID(),turnId:turn?.id,title,message,details,schemaJson:operation.interactionKind==='RUNTIME_INPUT'?{type:'object',properties:{answer:{type:'string'}},required:['answer'],additionalProperties:false}:undefined});
   else{let content;if(operation.action==='ACCEPT'&&operation.interactionKind==='RUNTIME_INPUT'){try{content=JSON.parse(response);}catch{throw Error('请输入有效JSON对象。');}}decideExecutionInteraction({...operation,content});}
   dismiss();toast(operation.kind==='request'?'已保存本地模拟请求':'本地决定已保存，尚未送达Runtime');
  }}>
   <p>{e.id} · 派发 #{operation.dispatchEpoch} · {operation.interactionId||'新请求'}</p>
   {operation.kind==='request'?<><Field label="请求标题" value={title} onChange={setTitle} required />{operation.interactionKind==='RUNTIME_INPUT'&&<Field label="需要Human补充的信息" type="textarea" value={message} onChange={setMessage} required />}<Field label="已脱敏的请求详情" type="textarea" value={details} onChange={setDetails} /><Notice>输入请求使用固定结构：answer为字符串的JSON对象。原型只支持已声明的Schema子集，不冒称兼容全部JSON Schema。</Notice></>:operation.action==='ACCEPT'&&operation.interactionKind==='RUNTIME_INPUT'?<Field label="回应JSON对象" type="textarea" value={response} onChange={setResponse} required />:<Notice>{operation.action==='ACCEPT'?'仅保存对此请求允许一次的Human决定；不会扩大其他工具或后续派发权限。':'拒绝此具体请求；拒绝不等同终止整个执行。'}</Notice>}
  </ActionForm></Modal>}
 </Section>;
}

export function WorkExecutionStart({work,archived,toast,onHumanStart}){
 const s=useStore(),[form,setForm]=useViewState(`work-dispatch.${work.id}`,null),[agentId,setAgentId]=useState(''),[nodeId,setNodeId]=useState('');
 const ids=(work.team?.executorIds||[]).filter(id=>id.startsWith('agent:')).map(id=>id.slice(6)),agent=s.agents.find(a=>a.id===agentId),pending=(s.executions||[]).find(e=>e.workId===work.id&&!['SUCCEEDED','FAILED','CANCELLED'].includes(e.state));
 if(!ids.length)return <Button disabled={archived} variant="primary" onClick={onHumanStart}>开始执行</Button>;
 return <><Button disabled={archived||s.offline||Boolean(pending)} variant="primary" onClick={()=>{setAgentId('');setNodeId('');setForm({tenantId:s.activeTenant,version:work.version});}}>分派Agent执行</Button>{pending&&<p>已分派 {pending.id} · {executionStates[pending.state]}。<Link to={executionPath(pending)} target="_blank" rel="noopener noreferrer">新标签页查看执行</Link></p>}
 {form&&<Modal title="分派Agent执行" draftValue={{agentId,nodeId}} onClose={()=>setForm(null)}><ActionForm label="创建派发记录，等待节点接受" onCancel={()=>setForm(null)} onSubmit={({dismiss})=>{const node=s.nodes.find(n=>n.id===nodeId);workCommand(work.id,'start','',form.version,{tenantId:form.tenantId,agentId,nodeId,runtime:node?.runtime});dismiss();toast('本地派发记录已创建，任务等待节点接受；未运行真实Runtime');}}>
 <p>{work.id} · 固定工作输入 v{form.version} · 工作流 {work.workflowId||'default'} / v{work.flowVersion}</p>
 <Field label="已分派的Agent" value={agentId} onChange={value=>{setAgentId(value);setNodeId('');}} required options={[{value:'',label:'请选择Agent'},...s.agents.filter(a=>ids.includes(a.id)&&!['待批准','退役','已退役'].includes(a.status)).map(a=>({value:a.id,label:`${a.name} · ${a.runtime}`}))]} />
 <Field label="匹配Runtime的在线节点" value={nodeId} onChange={setNodeId} required options={[{value:'',label:'请选择执行节点'},...s.nodes.filter(n=>n.status==='在线'&&n.runtime===agent?.runtime).map(n=>({value:n.id,label:`${n.name} · ${n.runtime}`}))]} />
 <Notice>只为这位已分派Agent建立独立自主执行记录，不复制旧成果。节点接受前工作保持待开始；选择多个Agent的协作团队不等于同时启动所有Agent。真实调度、槽位预留与Runtime尚未接入。</Notice>
 </ActionForm></Modal>}</>;
}

function ExecutionRecovery({execution:e,toast,onWork}) {
  const s=useStore(),[form,setForm]=useState(null);
  const mutable=!executionMaintenanceProblem(s,e)&&!s.offline&&!s.projects.find(p=>p.id===e.projectId)?.archivedAt;
  const open=(action,operationId)=>setForm({action,operationId,tenantId:s.activeTenant,executionId:e.id,revision:e.revision,title:'',safe:'',retryLimit:'',sideEffects:'',evidence:'',reason:'',failureKind:''});
  const set=patch=>setForm(value=>({...value,...patch}));
  const scopeDraft=()=>{try{const id=beginWorkDraft({scopeChangeFrom:e.workId,scopeChangeReason:e.failure.reason});const q=new URLSearchParams(location.hash.split('?')[1]||'');q.set('quick_work',id);location.hash=`${location.hash.slice(1).split('?')[0]}?${q}`;}catch(error){toast(error.message,true);}};
  return <Section title="失败处置与操作副作用">
    <Notice>仅记录本地模拟。登记、重试、核实与补偿均不执行真实外部操作；断线不会自动重放发信、发布、扣费或资源创建。</Notice>
    {e.failure?<><p>{executionFailureKinds[e.failure.kind]} · {e.failure.reason}</p><p>下一步：{executionFailureActions[e.failure.kind]}</p>{e.failure.kind==='QUALITY'&&e.workId&&<Button onClick={onWork}>打开工作质量返工</Button>}{e.failure.kind==='SCOPE_CHANGE'&&e.workId&&<Button disabled={!mutable} onClick={scopeDraft}>起草关联原任务的新范围</Button>}{['MISSING_INPUT','PERMISSION'].includes(e.failure.kind)&&<Notice>当前执行保持失败阻塞；请在原任务补充资料或提交现有审批，核准前不重跑。</Notice>}</>:e.state==='FAILED'&&<p>历史失败尚未分类，不推定为环境故障。</p>}
    {mutable&&e.state==='FAILED'&&<Button onClick={()=>open('classify-failure')}>记录失败分类</Button>}
    {mutable&&['CREATED','RUNNING','PAUSED'].includes(e.state)&&<Button onClick={()=>open('operation-register')}>登记待执行操作</Button>}
    {(e.operations||[]).map(o=><article className="execution-record" key={o.id}><b>{o.title}</b><p>{o.started?'已启动':'未启动'} · {o.safe?'已声明安全操作':'非安全操作'} · 副作用：{{NONE:'无',KNOWN:'已知',UNKNOWN:'未知'}[o.sideEffects]} · 安全重试 {o.retryCount} / {o.retryLimit}</p>{o.resolution&&<p>{o.resolution.kind==='VERIFIED'?'核实回执':'补偿回执'}：{o.resolution.evidence} · {formatTimestamp(o.resolution.time)}</p>}
      {mutable&&<div className="actions">{!o.started&&e.state==='RUNNING'&&<Button onClick={()=>open('operation-start',o.id)}>模拟操作已启动</Button>}{!o.started&&o.safe&&o.retryCount<o.retryLimit&&['CREATED','RUNNING','PAUSED','INTERRUPTED','FAILED'].includes(e.state)&&<Button onClick={()=>open('operation-retry',o.id)}>记录一次安全重试</Button>}{o.started&&<><Button onClick={()=>open('operation-verify',o.id)}>记录核实回执</Button><Button onClick={()=>open('operation-compensate',o.id)}>记录补偿回执</Button></>}</div>}
      <details><summary>操作记录 · {o.history.length}</summary>{o.history.map((h,i)=><p key={i}>{h.action} · {h.reason} · {formatTimestamp(h.time)}</p>)}</details>
    </article>)}
    {form&&<Modal title="记录本地处置" draftValue={form} onClose={()=>setForm(null)}><ActionForm label="保存本地处置" onCancel={()=>setForm(null)} onSubmit={({dismiss})=>{if(form.action==='operation-register'&&(form.safe===''||form.retryLimit===''))throw Error('请明确安全性和有限重试上限。');executionCommand({...form,safe:form.safe==='yes',retryLimit:Number(form.retryLimit)});dismiss();toast('已保存本地处置，未调用外部服务');}}>
      {form.action==='classify-failure'&&<Field label="失败分类" required value={form.failureKind} onChange={failureKind=>set({failureKind})} options={[{value:'',label:'请选择分类'},...Object.entries(executionFailureKinds).map(([value,label])=>({value,label}))]}/>}
      {form.action==='operation-register'&&<><Field label="操作名称" required value={form.title} onChange={title=>set({title})}/><Field label="是否为可安全重试操作" required value={form.safe} onChange={safe=>set({safe})} options={[{value:'',label:'请明确核对'},{value:'yes',label:'是，已核对安全性'},{value:'no',label:'否，不允许安全重试'}]}/><Field label="最多额外重试次数" type="number" min="0" step="1" required value={form.retryLimit} onChange={retryLimit=>set({retryLimit})}/></>}
      {['operation-verify','operation-compensate'].includes(form.action)&&<><Field label="核实后的副作用" required value={form.sideEffects} onChange={sideEffects=>set({sideEffects})} options={[{value:'',label:'请选择已核实结果'},{value:'NONE',label:'已核实无副作用'},{value:'KNOWN',label:'副作用已明确，保留事实'}]}/><Field label="核实或补偿回执依据" required type="textarea" value={form.evidence} onChange={evidence=>set({evidence})}/></>}
      <Field label="处置理由" type="textarea" required value={form.reason} onChange={reason=>set({reason})}/><Notice>已启动操作不会通过重试按钮重新执行。未知副作用须核实或补偿后才能恢复或建立替代执行。</Notice>
    </ActionForm></Modal>}
  </Section>;
}
