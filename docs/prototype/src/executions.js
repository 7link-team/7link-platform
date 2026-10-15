import { reserveExecutionBudgetOn, closeExecutionBudgetOn } from "./budget";
import { finiteJSON, schemaProblem, sampleMatchesSchema } from './workflow-node-config';
// Execution identities and receipts belong to a tenant-local prototype scenario.
// No command here contacts a runtime, starts a process, or grants real tool permissions.
const terminal = new Set(['SUCCEEDED', 'FAILED', 'CANCELLED']);
const actor = s => s.role === 'external' ? 'external-demo' : 'H-lin';
const now = () => new Date().toISOString();
const uid = () => `EX-${crypto.randomUUID()}`;
export const executionPath = execution => `/chat?execution_id=${encodeURIComponent(execution.id)}`;
export const executionKinds = { AUTONOMOUS: '自主执行', INTERACTIVE: '任务交互执行', ADHOC: '临时探索执行' };
export const executionStates = { CREATED: '已创建 · 等待启动', RUNNING: '运行中', PAUSED: '已暂停', INTERRUPTED: '已中断', SUCCEEDED: '执行结束', FAILED: '执行失败', CANCELLED: '已终止' };
export const executionFailureKinds = { ENVIRONMENT:'环境故障', QUALITY:'质量问题', MISSING_INPUT:'缺资料', PERMISSION:'权限拒绝', SCOPE_CHANGE:'范围变化' };
export const executionFailureActions = { ENVIRONMENT:'核对安全操作后有限重试', QUALITY:'进入工作质量返工', MISSING_INPUT:'阻塞并补充资料', PERMISSION:'阻塞并请求有权Human审批', SCOPE_CHANGE:'起草关联原任务的新范围' };
export const inputApplicationStates = { RECEIVED:'已收到', WAITING_BOUNDARY:'待安全边界应用', APPLIED:'已应用（本地模拟）', UNAPPLICABLE:'无法应用' };
export function executionSafetyBoundaryProblem(e) {
  if((e.operations || []).some(o=>o.started && (!o.completedAt || o.sideEffects==='UNKNOWN'))) return '存在未完成操作或未知副作用，尚未到达可变更模型的安全边界。';
  return '';
}
const uncertainOperations = e => (e.operations || []).filter(o=>o.started && o.sideEffects==='UNKNOWN' && !o.resolution);
export function hydrateExecutions(s) { s.executions ??= []; }
export function executionRecord(s, id) { return s.executions?.find(e => e.id === id && e.tenantId === s.activeTenant); }
export function executionReadProblem(s, id, principalId = actor(s)) {
  const e = executionRecord(s, id);
  if (!e) return '执行编号不存在或不属于当前租户；任务编号不能作为执行编号。';
  const person = s.organization?.members?.find(p => p.id === principalId && p.kind === 'Human');
  if (!person || s.role === 'external') return '当前身份没有内部执行记录的读取权限。';
  const project = s.projects?.find(p => p.id === e.projectId);
  if (!project) return '执行所属项目不存在。';
  if (s.role === 'owner' || project.ownerId === principalId || s.organization.projectRoles.some(r => r.projectId === e.projectId && r.memberId === principalId)) return '';
  return '当前成员未获执行所属项目的访问权限。';
}
export function executionMaintenanceProblem(s, e) {
  const problem = executionReadProblem(s, e?.id); if (problem) return problem;
  if (s.role === 'owner' || e.initiatorId === actor(s) || e.participantIds?.includes(actor(s))) return '';
  return '当前Human不能维护此本地执行场景。';
}
function writable(s, input) {
  if (input.tenantId !== s.activeTenant) throw Error('租户已切换，请重新打开执行记录。');
  if (s.offline) throw Error('当前离线，执行操作未提交。');
  const problem = executionReadProblem(s, input.executionId); if (problem) throw Error(problem);
  const e = executionRecord(s, input.executionId);
  if (s.projects.find(p => p.id === e.projectId)?.archivedAt) throw Error('所属项目已归档，执行记录只读。');
  if (e.revision !== input.revision) throw Error('执行记录已变化，请核对最新状态后重试。');
  const maintenance = executionMaintenanceProblem(s, e); if (maintenance) throw Error(maintenance);
  return e;
}
export function availableExecutionCommands(e) {
  const commands = {CREATED:['start','cancel'], RUNNING:['pause','interrupt','fail','cancel'],
    PAUSED:['resume','finish','fail','cancel'], INTERRUPTED:['resume','fail','cancel']}[e.state] || [];
  return commands.filter(action => action !== 'finish' || (e.kind !== 'AUTONOMOUS' && e.turns.at(-1)?.runState === 'COMPLETED'));
}
export function createExecutionBranchOn(s, input) {
  const parent = writable(s, input);
  if (!parent.workId || parent.kind === 'ADHOC') throw Error('临时探索没有任务尝试树，请先正式关联工作项。');
  const child=createExecutionOn(s, {...input, parentExecutionId:parent.id, kind:parent.kind,
    projectId:parent.projectId, workId:parent.workId, agentId:parent.agentId},
    {systemDispatch:parent.kind === 'AUTONOMOUS'});
  if(parent.dispatchedFromWorkCommand){const w=s.work.find(w=>w.id===parent.workId&&w.project===parent.projectId);if(w){w.dispatchedExecutionId=child.id;child.dispatchedFromWorkCommand=w.status==='待开始';}}
  return child;
}
export function executionInputProblem(s, id) {
  const problem = executionReadProblem(s, id); if (problem) return problem;
  const e = executionRecord(s,id);
  if (s.offline) return '当前离线，输入未提交。';
  if (s.projects.find(p => p.id === e.projectId)?.archivedAt) return '所属项目已归档，执行记录只读。';
  if (e.state !== 'RUNNING') return `执行状态为${executionStates[e.state] || e.state}，不能发送新指令。`;
  if((e.interactions||[]).some(i=>['PENDING','DECIDED'].includes(i.state)))return '此执行正在等待具体交互请求处理；请先回应请求并核对送达。';
  if (!e.capabilities?.interactiveInput) return '此执行没有通用交互输入能力；请在协作频道讨论，或回应具体输入请求。';
  if (e.initiatorId !== actor(s)) return '只有此执行的 Human 发起者可以发送轮次；当前成员仅可观察或按权限维护执行状态。';
  return '';
}
function event(e, kind, text) { e.events.push({ id: crypto.randomUUID(), seq: e.events.length + 1, kind, text, time: now(), mode: 'LOCAL_SIMULATION' }); }
function projectWrite(s, projectId) {
  const project=s.projects.find(p=>p.id===projectId);
  if (!project || s.role==='external' || !s.organization.members.some(p=>p.id===actor(s)&&p.kind==='Human')) throw Error('请选择当前租户的可访问项目。');
  if (project.archivedAt || s.offline) throw Error('归档项目或离线状态不能创建执行。');
  if (s.role!=='owner' && project.ownerId!==actor(s) && !s.organization.projectRoles.some(r=>r.projectId===projectId&&r.memberId===actor(s))) throw Error('当前成员未获项目执行权限。');
  return project;
}
function fixedWork(s, workId, projectId) {
  const w=s.work.find(w=>w.id===workId&&w.project===projectId);
  if (!w || ['已完成','已取消'].includes(w.status)) throw Error('请选择同项目内可执行的工作项。');
  if (!w.plan || !w.flow || !Number.isSafeInteger(w.flowVersion) || w.flowVersion<1) throw Error('工作项需要已确认计划和固定发布工作流版本。');
  return w;
}
export function createExecutionOn(s,input,{systemDispatch=false}={}) {
  if (input.tenantId!==s.activeTenant) throw Error('租户已切换，请重新创建执行。');
  if (!Object.hasOwn(executionKinds,input.kind) || input.kind==='AUTONOMOUS'&&!systemDispatch) throw Error('自主执行只能由工作分派入口创建。');
  projectWrite(s,input.projectId);
  const node=s.nodes.find(n=>n.id===input.nodeId&&n.status==='在线');
  if (!node || !input.runtime || node.runtime!==input.runtime) throw Error('请选择在线节点已登记的Runtime；未检测能力不能当成已安装。');
  const w=input.kind==='ADHOC'?null:fixedWork(s,input.workId,input.projectId);
  if (input.kind==='ADHOC'&&input.workId) throw Error('临时探索执行创建时不关联工作项；请使用正式关联操作。');
  if (input.kind==='AUTONOMOUS') {
    const agent=s.agents.find(a=>a.id===input.agentId&&!['待批准','退役','已退役'].includes(a.status));
    if (!agent || !(w.team?.executorIds || []).includes(`agent:${agent.id}`) || agent.runtime!==node.runtime) throw Error('自主执行必须引用已分派、可用且Runtime匹配的Agent。');
  }
  if(input.kind==='INTERACTIVE' && !w.team?.executorIds?.includes(actor(s))) throw Error('创建任务交互执行需要当前 Human 已被分派为工作执行者。');
  const authorizationMode=input.authorizationMode || 'READ_ONLY';
  if(!['READ_ONLY','ON_REQUEST'].includes(authorizationMode) || authorizationMode==='ON_REQUEST'&&input.runtime!=='Claude Code') throw Error('当前场景仅支持只读或Claude Code逐项授权；Codex CLI不支持Web工具审批。');
  const parent=input.parentExecutionId?executionRecord(s,input.parentExecutionId):null;
  if (input.parentExecutionId && (!parent || parent.workId!==w?.id || parent.projectId!==input.projectId || !terminal.has(parent.state)&&!['INTERRUPTED','PAUSED'].includes(parent.state))) throw Error('分支需要同工作项已结束或已中断的父执行；运行中的副作用不能自动重放。');
  if(parent && uncertainOperations(parent).length) throw Error('原执行存在未核实或补偿的外部副作用，不能建立替代执行。');
  if (parent && input.branchReason==='RETRY_INFRA'&&!['FAILED','INTERRUPTED','CANCELLED','PAUSED'].includes(parent.state)) throw Error('故障重试不能继承成功执行；请采用明确的策略替代。');
  if (parent && (!['RETRY_INFRA','STRATEGY'].includes(input.branchReason)||!input.reason?.trim())) throw Error('请提供执行分支原因与具体依据。');
  if(w && (s.executions||[]).some(e=>e.workId===w.id && !terminal.has(e.state) && !(e.id===parent?.id && input.branchReason==='RETRY_INFRA'))) throw Error('同一工作已有未结束执行；请先终止原执行，或对暂停/中断父执行明确故障重试。');
  if(input.parentExecutionId && !w) throw Error('临时探索执行不建立任务分支。');
  const siblings=(s.executions||[]).filter(e=>e.workId===w?.id&&e.projectId===input.projectId);
  const branchReason=parent?input.branchReason:w?(siblings.length?'STRATEGY':'INITIAL'):null;
  const e={id:uid(),tenantId:s.activeTenant,projectId:input.projectId,workId:w?.id||null,kind:input.kind,nodeId:node.id,runtime:input.runtime,agentId:input.agentId||null,initiatorKind:input.kind==='AUTONOMOUS'?'SYSTEM':'HUMAN',initiatorId:input.kind==='AUTONOMOUS'?null:actor(s),requestedBy:actor(s),participantIds:[actor(s)],title:w?.title||input.title?.trim()||'临时探索执行',parentExecutionId:parent?.id||null,branchReason,seqNo:w?Math.max(0,...siblings.map(e=>e.seqNo||0))+1:null,reasonOrdinal:w?siblings.filter(e=>e.branchReason===branchReason).length+1:null,branchReasonText:parent?input.reason.trim():null,workVersion:w?.version||null,workflowId:w ? w.workflowId||'default' : null,workflowVersion:w?.flowVersion||null,workSnapshot:w?{...structuredClone(w),previewResources:[]}:null,state:'CREATED',revision:1,dispatchEpoch:0,authorizationMode,mode:'LOCAL_SIMULATION',createdAt:now(),capabilities:{interactiveInput:input.kind!=='AUTONOMOUS',runtimeInput:true,toolPermission:input.runtime==='Claude Code'&&authorizationMode==='ON_REQUEST',source:'PROTOTYPE_SCENARIO'},turns:[],events:[],interactions:[]};
  event(e,'execution.created',`已创建本地${executionKinds[e.kind]}场景；尚未联系节点或启动Runtime。`);
  if(parent&&input.branchReason==='RETRY_INFRA'&&['PAUSED','INTERRUPTED'].includes(parent.state)){parent.state='FAILED';parent.endReason='SUPERSEDED_BY_RETRY';closeExecutionInteractions(parent,'FENCED');parent.revision++;for(const t of parent.turns.filter(t=>t.status==='PENDING')){applicationReceipt(s,t,'UNAPPLICABLE','父执行被替代，未应用输入不会重投。');t.status='REJECTED';t.receipt={reason:'父执行被故障重试替代，输入不会重投子执行。',actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};}event(parent,'execution.superseded',`由故障重试执行${e.id}替代；旧输入及证据不复制为新事实。`);}
  s.executions ??= [];s.executions.push(e);return e;
}
export function executionCommandOn(s,input) {
  const e=writable(s,input);
  if (!input.reason?.trim()) throw Error('请填写本次执行操作的依据。');
  if(input.action?.startsWith('operation-')) return executionOperationOn(s,e,input);
  if(input.action==='classify-failure') return classifyExecutionFailure(s,e,input);
  if(input.action==='finish' && executionSafetyBoundaryProblem(e)) throw Error(executionSafetyBoundaryProblem(e));
  if(input.action==='resume' && uncertainOperations(e).length) throw Error('存在未知副作用，请先记录核实或补偿回执。');
  if(input.action==='fail' && input.failureKind && !Object.hasOwn(executionFailureKinds,input.failureKind)) throw Error('请选择原文定义的失败分类。');
  const next={start:[['CREATED'],'RUNNING'],pause:[['RUNNING'],'PAUSED'],resume:[['PAUSED','INTERRUPTED'],'RUNNING'],interrupt:[['RUNNING'],'INTERRUPTED'],finish:[['PAUSED'],'SUCCEEDED'],fail:[['RUNNING','PAUSED','INTERRUPTED'],'FAILED']};
  if(input.action==='resume' && e.state==='INTERRUPTED' && input.acknowledgeSideEffects!==true) throw Error('中断执行必须先核对副作用，并明确确认后才能恢复。');
  if(input.action==='finish' && (e.kind==='AUTONOMOUS' || e.turns.at(-1)?.runState!=='COMPLETED')) throw Error('结束交互执行需要最后一个轮次已完成；自主执行由完成声明流程结束。');
  const dispatchWork=e.dispatchedFromWorkCommand&&input.action==='start'?s.work.find(w=>w.id===e.workId&&w.project===e.projectId):null;
  if(e.dispatchedFromWorkCommand&&input.action==='start'&&(!dispatchWork||dispatchWork.status!=='待开始'||dispatchWork.version!==e.workVersion||!dispatchWork.plan||!dispatchWork.flow||dispatchWork.flowVersion!==e.workflowVersion||(dispatchWork.workflowId||'default')!==e.workflowId||!(dispatchWork.team?.executorIds||[]).includes(`agent:${e.agentId}`)))throw Error('工作基线、分派或状态已变化，请先核对派发，不能按旧输入启动。');
  if (input.action==='cancel') { if(terminal.has(e.state))throw Error('终态执行保留历史，不能再次终止。'); e.state='CANCELLED'; }
  else {
    const change=next[input.action];if(!change||!change[0].includes(e.state))throw Error('此动作不适用于当前执行状态。');
    if (['start','resume'].includes(input.action)&&!s.nodes.some(n=>n.id===e.nodeId&&n.status==='在线'))throw Error('节点不在线，不能模拟接受派发或恢复。');
    if(input.action==='start')reserveExecutionBudgetOn(s,e,input);
    e.state=change[1];
    if(['start','resume'].includes(input.action)){closeExecutionInteractions(e,'FENCED');e.dispatchEpoch=(e.dispatchEpoch||0)+1;}
  }
  if(terminal.has(e.state))closeExecutionBudgetOn(s,e);
  if(dispatchWork){dispatchWork.status='进行中';delete dispatchWork.archivePause;}
  if (terminal.has(e.state)||['PAUSED','INTERRUPTED'].includes(e.state)) for(const t of e.turns.filter(t=>t.status==='PENDING')){t.status='REJECTED';t.receipt={reason:'执行已结束或中断，未处理输入不会转投其他执行。',actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};}
  if(terminal.has(e.state)||['PAUSED','INTERRUPTED'].includes(e.state))closeExecutionInteractions(e,e.state==='INTERRUPTED'?'FENCED':'EXECUTION_ENDED');
  if(input.action==='fail' && input.failureKind) e.failure={kind:input.failureKind,nextAction:executionFailureActions[input.failureKind],reason:input.reason.trim(),actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};
  if(input.action==='interrupt') for(const operation of e.operations || []) if(operation.started && !operation.completedAt && !operation.resolution) operation.sideEffects='UNKNOWN';
  for(const t of e.turns.filter(t=>t.status==='REJECTED' && ['RECEIVED','WAITING_BOUNDARY'].includes(t.applicationState))) applicationReceipt(s,t,'UNAPPLICABLE',t.receipt.reason);
  e.revision++;event(e,`simulation.${input.action}`,`本地模拟：${executionStates[e.state]}。依据：${input.reason.trim()}。不代表真实Runtime回执或工作验收。`);return e;
}
export function submitExecutionInputOn(s,input) {
  const e=writable(s,input),problem=executionInputProblem(s,e.id);if(problem)throw Error(problem);
  if (input.threadId || input.workDraftId)throw Error('执行输入按交互轮次提交，不使用协作线程或工作草稿消息。');
  if (!input.text?.trim() || input.text.length>8000)throw Error('请填写1–8000字的执行指令。');
  if (input.attachments?.length)throw Error('此执行场景未接入Runtime附件传输，请从协作频道提供材料后确认输入版本。');
  const t={id:crypto.randomUUID(),executionId:e.id,seq:e.turns.length+1,text:input.text.trim(),status:'PENDING',applicationState:'RECEIVED',applicationReceipts:[{state:'RECEIVED',time:now(),actorId:actor(s),reason:'输入已保存于本地执行，未发送真实Runtime。',mode:'LOCAL_SIMULATION'}],submittedBy:actor(s),submittedAt:now(),receipt:null};
  e.turns.push(t);e.revision++;event(e,'input.submitted',`轮次${t.seq}输入已保存，等待本地模拟处理；未送达真实Runtime。`);
  const message={id:`input-${t.id}`,channel:`exec-${e.id}`,executionId:e.id,turnId:t.id,authorId:actor(s),author:s.organization.members.find(p=>p.id===actor(s)).name,kind:'human',text:t.text,time:t.submittedAt,mode:'LOCAL_SIMULATION'};
  s.messages.push(message);return t;
}
export function resolveExecutionInputOn(s,input) {
  const e=writable(s,input),t=e.turns.find(t=>t.id===input.turnId);
  if (e.state!=='RUNNING'||!t||t.status!=='PENDING')throw Error('此轮次不在当前运行执行的待处理状态。');
  if(input.outcome==='WAITING_BOUNDARY') { if(!input.reason?.trim()) throw Error('请填写等待安全边界的依据。'); applicationReceipt(s,t,'WAITING_BOUNDARY',input.reason.trim()); e.revision++; event(e,'input.waiting_boundary',`轮次${t.seq}等待安全边界；未应用。`);return t; }
  if (!['ACCEPTED','REJECTED'].includes(input.outcome)||!input.reason?.trim())throw Error('请选择本地模拟采用或拒绝，并填写依据。');
  if(input.outcome==='ACCEPTED' && executionSafetyBoundaryProblem(e)) throw Error(executionSafetyBoundaryProblem(e));
  applicationReceipt(s,t,input.outcome==='ACCEPTED'?'APPLIED':'UNAPPLICABLE',input.reason.trim());
  t.status=input.outcome;t.receipt={reason:input.reason.trim(),actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};e.revision++;
  event(e,'input.simulation_receipt',`轮次${t.seq}本地模拟${t.status==='ACCEPTED'?'采用':'拒绝'}：${t.receipt.reason}。不代表Agent实际读取。`);return t;
}
export function linkAdhocExecutionOn(s,input) {
  const e=writable(s,input);
  if (e.kind!=='ADHOC'||e.workId||terminal.has(e.state))throw Error('只有未绑定工作项且未结束的临时探索执行可以正式关联。');
  const w=fixedWork(s,input.workId,e.projectId);
  if(e.initiatorId!==actor(s)||!w.team?.executorIds?.includes(actor(s))||!['进行中','等待'].includes(w.status))throw Error('正式关联需要执行发起者已被分派为该工作项执行者，且工作已认领或正在执行。');
  if(!input.reason?.trim())throw Error('请填写关联工作项的依据。');
  if(s.executions.some(row=>row.id!==e.id && row.workId===w.id && !terminal.has(row.state)))throw Error('目标工作已有未结束执行，不能再关联一个并发执行。');
  const siblings=s.executions.filter(row=>row.workId===w.id);e.kind='INTERACTIVE';e.branchReason=siblings.length?'STRATEGY':'INITIAL';e.seqNo=Math.max(0,...siblings.map(e=>e.seqNo||0))+1;e.reasonOrdinal=siblings.filter(row=>row.branchReason===e.branchReason).length+1;
  e.workId=w.id;e.workVersion=w.version;e.workflowId=w.workflowId||null;e.workflowVersion=w.flowVersion;e.workSnapshot={...structuredClone(w),previewResources:[]};e.revision++;
  event(e,'work.linked',`正式关联工作项${w.id} / 输入v${w.version}：${input.reason.trim()}。之前探索轮次仍属此执行。`);return e;
}

export function prototypeExecutionExamples(initial) {
  // Explicit demonstration fixtures; no legacy task messages are promoted into events.
  const scenario={...initial,work:structuredClone(initial.work),activeTenant:'studio',executions:[],messages:[]};
  const w=scenario.work.find(w=>w.id==='W-104');
  if(w?.plan && w.flow && w.flowVersion){
    w.team={executorIds:['agent:atlas']};
    const e=createExecutionOn(scenario,{tenantId:'studio',kind:'AUTONOMOUS',projectId:w.project,workId:w.id,nodeId:'n1',runtime:'Codex CLI',agentId:'atlas'},{systemDispatch:true});
    e.id='EX-demo-W-104';e.state='RUNNING';e.dispatchEpoch=1;
    e.workSnapshot.previewResources=[['primary','Web · 主预览','Web'],['secondary','Web · 管理端','Web'],['api','API','API'],['terminal','终端','终端'],['files','仓库代码','仓库'],['readme','README.md','文稿']].map(([id,name,kind])=>({id,name,kind,status:'就绪',mode:'LOCAL_SIMULATION'}));
    event(e,'simulation.preview_examples','预置本地成果查看器样例清单；仅演示查看交互，不代表Runtime已产生这些成果。');
    event(e,'simulation.example','预置本地自主运行场景；不代表真实节点启动、输入校验或成果验证。');
  }
  return scenario.executions;
}

export function completeExecutionTurnOn(s,input){
  const e=writable(s,input),t=e.turns.find(t=>t.id===input.turnId);
  if(e.state!=='RUNNING' || !t || t.status!=='ACCEPTED' || t.runState)throw Error('仅可模拟当前运行中已采用且未结束的轮次。');
  if(!input.reason?.trim())throw Error('请记录本轮完成依据。');
  if(executionSafetyBoundaryProblem(e))throw Error(executionSafetyBoundaryProblem(e));
  if((e.interactions||[]).some(i=>['PENDING','DECIDED'].includes(i.state)))throw Error('仍有未完成的交互请求，不能模拟轮次完成。');
  closeExecutionInteractions(e,'TURN_ENDED');
  t.runState='COMPLETED';t.completedAt=now();t.completionReason=input.reason.trim();e.state='PAUSED';e.revision++;
  for(const pending of e.turns.filter(row=>row.id!==t.id&&row.status==='PENDING')){applicationReceipt(s,pending,'UNAPPLICABLE','当前轮次结束，未应用输入不自动重投。');pending.status='REJECTED';pending.receipt={reason:'当前轮次结束，执行暂停；此输入未采用，不自动重投。',actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};}
  event(e,'simulation.turn_completed',`轮次${t.seq}本地模拟完成，执行暂停等待下一轮或明确结束。依据：${t.completionReason}。不代表真实输出或成果验收。`);return t;
}

export const interactionStates={PENDING:'需要确认',DECIDED:'本地决定已保存 · 待送达',DELIVERED:'本地模拟已送达',EXPIRED:'已过期',CANCELLED:'已取消'};
export function interactionResponseProblem(s,e){
  const problem=executionReadProblem(s,e.id);if(problem)return problem;
  const principalId=actor(s),project=s.projects.find(p=>p.id===e.projectId);
  if(s.role==='owner'||project?.ownerId===principalId||e.kind==='INTERACTIVE'&&e.initiatorId===principalId)return '';
  if(s.organization.projectRoles.some(r=>r.projectId===e.projectId&&r.memberId===principalId&&r.permissions?.includes('interaction_respond')))return '';
  return '当前Human没有此执行的交互回应权限；项目读取权限不等于工具批准权限。';
}
function interactionExecution(s,input,{response=false,revision=true}={}){
  if(input.tenantId!==s.activeTenant)throw Error('租户已切换，请重新核对交互请求。');
  const problem=executionReadProblem(s,input.executionId);if(problem)throw Error(problem);
  const e=executionRecord(s,input.executionId);
  if(s.offline||s.projects.find(p=>p.id===e.projectId)?.archivedAt)throw Error('离线或归档状态不能修改交互请求。');
  if(response){const denial=interactionResponseProblem(s,e);if(denial)throw Error(denial);}
  else {const maintenance=executionMaintenanceProblem(s,e);if(maintenance)throw Error(maintenance);}
  if(input.dispatchEpoch!==e.dispatchEpoch||!Number.isSafeInteger(e.dispatchEpoch)||e.dispatchEpoch<1)throw Error('交互请求属于旧派发，不能回送或套用到当前执行。');
  if(revision&&input.revision!==e.revision)throw Error('执行记录已变化，请核对最新交互请求。');
  return e;
}
function closeExecutionInteractions(e,reason){
  for(const i of e.interactions||[])if(['PENDING','DECIDED'].includes(i.state)){
    i.closedReason=i.state==='DECIDED'?'DELIVERY_FAILED':reason;i.state='CANCELLED';i.closedAt=now();
    event(e,'interaction.closed',`交互 ${i.id} 已取消：${i.closedReason}。旧决定不转投新派发。`);
  }
}
const canonical=value=>JSON.stringify(value===null||typeof value!=='object'?value:Array.isArray(value)?value.map(v=>JSON.parse(canonical(v))):Object.fromEntries(Object.keys(value).sort().map(k=>[k,JSON.parse(canonical(value[k]))])));
export function requestExecutionInteractionOn(s,input){
  const e=interactionExecution(s,input,{revision:false}),kind=input.interactionKind;
  if(e.state!=='RUNNING')throw Error('仅可模拟当前运行派发的交互请求。');
  if(!['TOOL_PERMISSION','RUNTIME_INPUT'].includes(kind))throw Error('未知交互请求类型。');
  if(!e.capabilities?.[kind==='TOOL_PERMISSION'?'toolPermission':'runtimeInput'] || kind==='TOOL_PERMISSION'&&(e.runtime!=='Claude Code'||e.authorizationMode!=='ON_REQUEST'))throw Error('当前Runtime或授权场景不支持此交互；Codex CLI不支持Web工具审批。');
  if(!input.title?.trim()||input.title.length>200||!input.nativeRequestId?.trim()||input.nativeRequestId.length>128)throw Error('请提供1–200字标题及有效请求关联编号。');
  if((input.details||'').length>65536||(input.message||'').length>2000)throw Error('请求详情或问题超出允许长度。');
  const schemaJson=kind==='RUNTIME_INPUT'?(input.schemaJson||{type:'object'}):null;
  if(schemaJson&&(new TextEncoder().encode(JSON.stringify(schemaJson)).length>32768||schemaProblem(schemaJson)||schemaJson.type!=='object'))throw Error('输入Schema须为受支持的对象结构，最大32KiB；不支持的关键字不会被忽略。');
  const turn=e.kind==='AUTONOMOUS'?null:e.turns.find(t=>t.id===input.turnId&&t.status==='ACCEPTED'&&!t.runState);
  if(e.kind!=='AUTONOMOUS'&&!turn)throw Error('请求必须关联当前执行已采用且未结束的轮次。');
  const prior=(e.interactions||[]).find(i=>i.dispatchEpoch===e.dispatchEpoch&&i.nativeRequestId===input.nativeRequestId);
  const payload={kind,title:input.title.trim(),details:input.details||'',message:input.message||'',schemaJson,turnId:turn?.id||null};
  if(prior){if(canonical(prior.requestSnapshot)!==canonical(payload))throw Error('同一请求编号已有不同内容，不能覆盖。');return prior;}
  if((e.interactions||[]).filter(i=>i.dispatchEpoch===e.dispatchEpoch).length>=100)throw Error('同一派发最多100项交互请求。');
  const createdAt=now(),runtimeExpiry=input.expiresAt?Date.parse(input.expiresAt):Infinity;
  if(input.expiresAt&&!Number.isFinite(runtimeExpiry))throw Error('请求截止时间格式无效。');
  const expiresAt=new Date(Math.min(Date.parse(createdAt)+30*60000,runtimeExpiry)).toISOString();
  const i={id:`IX-${crypto.randomUUID()}`,executionId:e.id,tenantId:e.tenantId,projectId:e.projectId,dispatchEpoch:e.dispatchEpoch,nativeRequestId:input.nativeRequestId,...payload,requestSnapshot:structuredClone(payload),state:Date.parse(expiresAt)<=Date.parse(createdAt)?'EXPIRED':'PENDING',createdAt,expiresAt,mode:'LOCAL_SIMULATION'};
  if(i.state==='EXPIRED'){i.closedReason='TIMEOUT';i.closedAt=createdAt;}
  e.interactions??=[];e.interactions.push(i);e.revision++;
  event(e,'interaction.simulation_requested',`本地模拟请求 ${i.id}：${kind} · ${i.title}。未接收真实Runtime事件。`);return i;
}
export function decideExecutionInteractionOn(s,input){
  // Authority is checked before idempotent reads; unrelated execution revisions do not invalidate an identical decision retry.
  const e=interactionExecution(s,input,{response:true,revision:false}),i=e.interactions.find(i=>i.id===input.interactionId);
  if(!i||i.dispatchEpoch!==input.dispatchEpoch)throw Error('交互请求不存在或派发已变化。');
  if(!['ACCEPT','DECLINE'].includes(input.action))throw Error('请选择允许/提交或拒绝。');
  let content=null;
  if(input.action==='ACCEPT'&&i.kind==='RUNTIME_INPUT'){
    content=input.content;
    if(!content||typeof content!=='object'||Array.isArray(content)||!finiteJSON(content)||new TextEncoder().encode(JSON.stringify(content)).length>32768)throw Error('请输入最大32KiB的JSON对象。');
    if(!sampleMatchesSchema(content,i.schemaJson))throw Error('输入不符合此请求固定的Schema。');
  }
  if(i.state==='DECIDED'){
    if(i.responseAction!==input.action||canonical(i.responseContent)!==canonical(content))throw Error('已有不同决定，不能覆盖；请查看原决定。');
    return i;
  }
  if(i.state!=='PENDING'||e.state!=='RUNNING'||Date.parse(i.expiresAt)<=Date.now())throw Error('此请求已结束或到期，不能再决定；请核对过期状态。');
  if(input.revision!==e.revision)throw Error('执行记录已变化，请核对最新交互请求。');
  i.state='DECIDED';i.responseAction=input.action;i.responseContent=structuredClone(content);i.responderId=actor(s);i.responderKind='HUMAN';i.responderAuthority=e.initiatorId===actor(s)&&e.kind==='INTERACTIVE'?'INTERACTIVE_INITIATOR':'PROJECT_INTERACTION_RESPOND';i.decidedAt=now();e.revision++;
  event(e,'interaction.simulation_decided',`交互 ${i.id} 本地决定已保存：${input.action}。决定者 ${i.responderId}；尚未送达Runtime，不记录回答正文到审计。`);return i;
}
export function deliverExecutionInteractionOn(s,input){
  const e=interactionExecution(s,input),i=e.interactions.find(i=>i.id===input.interactionId);
  if(!i||i.dispatchEpoch!==e.dispatchEpoch||i.state!=='DECIDED'||e.state!=='RUNNING')throw Error('仅可为当前运行派发中待送达的决定模拟回执。');
  if(!['DELIVERED','FAILED'].includes(input.outcome))throw Error('请选择模拟送达或送达失败。');
  if(input.outcome==='DELIVERED'){i.state='DELIVERED';i.deliveredAt=now();}else{i.state='CANCELLED';i.closedReason='DELIVERY_FAILED';i.closedAt=now();}
  e.revision++;event(e,'interaction.simulation_delivery',`交互 ${i.id}：本地模拟${input.outcome==='DELIVERED'?'送达':'送达失败'}。不代表真实Runtime读取或工具执行。`);return i;
}
export function expireExecutionInteractionOn(s,input){
  const e=interactionExecution(s,input),i=e.interactions.find(i=>i.id===input.interactionId);
  if(!i||i.state!=='PENDING'||i.dispatchEpoch!==e.dispatchEpoch)throw Error('只有当前派发的待决定请求可以到期。');
  const observedAt=input.simulateDeadline?i.expiresAt:now();
  if(Date.parse(observedAt)<Date.parse(i.expiresAt))throw Error('尚未达到请求截止时间。');
  i.state='EXPIRED';i.closedReason='TIMEOUT';i.closedAt=observedAt;i.expiryMode=input.simulateDeadline?'SIMULATED_CLOCK':'OBSERVED_DEADLINE';e.revision++;
  event(e,'interaction.simulation_expired',`交互 ${i.id} 已过期${input.simulateDeadline?'（本地模拟时钟推进至截止）':''}；未允许工具，不转投下一派发。`);return i;
}

function applicationReceipt(s,t,state,reason) {
  t.applicationState=state;t.applicationReceipts ??= [];
  t.applicationReceipts.push({state,reason,actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'});
}
function classifyExecutionFailure(s,e,input) {
  if(e.state!=='FAILED' || !Object.hasOwn(executionFailureKinds,input.failureKind)) throw Error('仅可为失败执行记录五类失败中的一种。');
  e.failureHistory ??= [];if(e.failure)e.failureHistory.push(structuredClone(e.failure));
  e.failure={kind:input.failureKind,nextAction:executionFailureActions[input.failureKind],reason:input.reason.trim(),actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};
  e.revision++;event(e,'failure.classified',`${executionFailureKinds[input.failureKind]}：${e.failure.nextAction}。分类不自动重跑，也不代替工作返工或Human审批。`);return e;
}
function executionOperationOn(s,e,input) {
  const stamp={reason:input.reason.trim(),actorId:actor(s),time:now(),mode:'LOCAL_SIMULATION'};
  if(input.action==='operation-register') {
    if(!['CREATED','RUNNING','PAUSED'].includes(e.state)) throw Error('此执行状态不能登记新的操作。');
    if(!input.title?.trim() || typeof input.safe!=='boolean' || !Number.isSafeInteger(input.retryLimit) || input.retryLimit<0) throw Error('请明确操作名称、安全性及非负整数重试上限。');
    const o={id:crypto.randomUUID(),title:input.title.trim(),safe:input.safe,retryLimit:input.retryLimit,retryCount:0,started:false,sideEffects:'NONE',history:[{action:'REGISTER',...stamp}],mode:'LOCAL_SIMULATION'};
    e.operations ??= [];e.operations.push(o);e.revision++;event(e,'operation.registered',`登记本地操作 ${o.id}；不会调用外部服务。`);return o;
  }
  const o=(e.operations || []).find(o=>o.id===input.operationId);if(!o)throw Error('操作不属于当前执行。');
  if(input.action==='operation-start') {
    if(e.state!=='RUNNING'||o.started||o.resolution)throw Error('仅运行中执行的未启动操作可以明确启动。');
    o.started=true;o.sideEffects='UNKNOWN';o.startedAt=now();
  } else if(input.action==='operation-retry') {
    if(!['CREATED','RUNNING','PAUSED','INTERRUPTED','FAILED'].includes(e.state)||o.started||!o.safe||o.retryCount>=o.retryLimit)throw Error('仅未启动的安全操作可在已配置上限内有限重试；已启动操作不得重放。');
    o.retryCount++;
  } else if(['operation-verify','operation-compensate'].includes(input.action)) {
    if(!o.started || !['NONE','KNOWN'].includes(input.sideEffects))throw Error('核实或补偿须明确已启动操作的副作用结果；未知不能当作成功。');
    if(input.action==='operation-compensate'&&!input.evidence?.trim())throw Error('请记录本地补偿回执依据。');
    if(input.action==='operation-verify'&&!input.evidence?.trim())throw Error('请记录本地核实回执依据。');
    o.sideEffects=input.sideEffects;o.resolution={kind:input.action==='operation-verify'?'VERIFIED':'COMPENSATED',sideEffects:input.sideEffects,evidence:input.evidence.trim(),...stamp};o.completedAt=now();
  } else throw Error('未知操作命令。');
  o.history.push({action:input.action,...stamp,...o.resolution?{resolution:structuredClone(o.resolution)}:{}});e.revision++;
  event(e,'operation.simulation_receipt',`${o.id} · ${input.action}。仅本地回执，不自动重放外部动作。`);return o;
}
