import { test, expect } from 'bun:test';
import { availableExecutionCommands, createExecutionBranchOn, createExecutionOn, executionCommandOn, submitExecutionInputOn, resolveExecutionInputOn, completeExecutionTurnOn, linkAdhocExecutionOn, executionReadProblem, executionInputProblem } from '../src/executions';
const fixture=()=>({activeTenant:'t',role:'executor',organization:{members:[{id:'H-lin',kind:'Human',name:'Lin'},{id:'H-zhou',kind:'Human',name:'Zhou'}],projectRoles:[{projectId:'p',memberId:'H-lin'}]},projects:[{id:'p',ownerId:'H-zhou'}],nodes:[{id:'n',status:'在线',runtime:'Codex CLI'}],agents:[{id:'a',runtime:'Codex CLI',status:'运行中'}],work:[{id:'w',title:'Work',project:'p',version:1,status:'进行中',plan:true,flow:true,flowVersion:1,workflowId:'flow',previewResources:[{id:'old-preview'}],team:{executorIds:['H-lin','agent:a']}}],executions:[],messages:[]});
const input=s=>({tenantId:s.activeTenant,kind:'INTERACTIVE',projectId:'p',workId:'w',nodeId:'n',runtime:'Codex CLI'});
const command=(s,e,action,extra={})=>executionCommandOn(s,{tenantId:s.activeTenant,executionId:e.id,revision:e.revision,action,reason:'explicit simulated action',...extra});
const atomic=(s,fn)=>{const before=structuredClone(s);expect(fn).toThrow();expect(s).toEqual(before);};
const submit=(s,e,text='instruction')=>submitExecutionInputOn(s,{tenantId:s.activeTenant,executionId:e.id,revision:e.revision,text});
const receipt=(s,e,t,outcome='ACCEPTED')=>resolveExecutionInputOn(s,{tenantId:s.activeTenant,executionId:e.id,revision:e.revision,turnId:t.id,outcome,reason:'explicit local receipt'});

test('execution identity and frozen snapshot never borrow earlier outputs',()=>{
 const s=fixture(),e=createExecutionOn(s,input(s));expect(e.id).not.toBe('w');expect(e.workSnapshot.previewResources).toEqual([]);s.work[0].title='changed';s.work[0].version=2;expect(e.workSnapshot.title).toBe('Work');expect(e.workVersion).toBe(1);expect(executionReadProblem(s,'w')).toContain('任务编号');
});
test('all nonterminal states reject a second execution or strategy branch',()=>{
 for(const state of ['CREATED','RUNNING','PAUSED','INTERRUPTED']){const s=fixture(),e=createExecutionOn(s,input(s));e.state=state;atomic(s,()=>createExecutionOn(s,input(s)));atomic(s,()=>createExecutionOn(s,{...input(s),parentExecutionId:e.id,branchReason:'STRATEGY',reason:'replace'}));}
});
test('project membership and owner role do not substitute for executor assignment',()=>{
 for(const role of ['owner','executor']){const s=fixture();s.role=role;s.work[0].team.executorIds=['H-zhou'];atomic(s,()=>createExecutionOn(s,input(s)));}
});
test('autonomous creation requires system dispatch and an assigned matching Agent',()=>{
 const s=fixture(),args={...input(s),kind:'AUTONOMOUS',agentId:'a'};atomic(s,()=>createExecutionOn(s,args));const e=createExecutionOn(s,args,{systemDispatch:true});expect(e.initiatorKind).toBe('SYSTEM');expect(e.capabilities.interactiveInput).toBe(false);
});
test('retry supersedes paused parent without copying input, output or old snapshot mutations',()=>{
 const s=fixture(),p=createExecutionOn(s,input(s));command(s,p,'start');const t=submit(s,p);command(s,p,'pause');const snapshot=structuredClone(p.workSnapshot);const e=createExecutionOn(s,{...input(s),parentExecutionId:p.id,branchReason:'RETRY_INFRA',reason:'node failure'});expect(p.state).toBe('FAILED');expect(p.endReason).toBe('SUPERSEDED_BY_RETRY');expect(p.workSnapshot).toEqual(snapshot);expect(t.status).toBe('REJECTED');expect(e.turns).toEqual([]);expect(e.seqNo).toBe(2);expect(e.parentExecutionId).toBe(p.id);
});
test('input is initiator-only and project revocation removes reads',()=>{
 const s=fixture(),e=createExecutionOn(s,input(s));command(s,e,'start');e.initiatorId='H-zhou';s.role='owner';expect(executionInputProblem(s,e.id)).toContain('发起者');atomic(s,()=>submit(s,e));s.role='executor';s.organization.projectRoles=[];expect(executionReadProblem(s,e.id)).toContain('访问权限');
});
test('tenant, offline, archive and stale revision failures are atomic',()=>{
 for(const mutate of [s=>s.activeTenant='other',s=>s.offline=true,s=>s.projects[0].archivedAt='2026-10-14']){const s=fixture(),e=createExecutionOn(s,input(s));mutate(s);atomic(s,()=>command(s,e,'start'));}
 const s=fixture(),e=createExecutionOn(s,input(s)),revision=e.revision;command(s,e,'start');atomic(s,()=>executionCommandOn(s,{tenantId:'t',executionId:e.id,revision,action:'pause',reason:'stale'}));
});
test('adoption and completion are separate; execution finish never accepts work',()=>{
 const s=fixture(),e=createExecutionOn(s,input(s));command(s,e,'start');const t=submit(s,e);atomic(s,()=>command(s,e,'finish'));receipt(s,e,t);expect(e.state).toBe('RUNNING');expect(t.runState).toBeUndefined();completeExecutionTurnOn(s,{tenantId:'t',executionId:e.id,revision:e.revision,turnId:t.id,reason:'simulated output complete'});expect(e.state).toBe('PAUSED');expect(t.runState).toBe('COMPLETED');command(s,e,'finish');expect(e.state).toBe('SUCCEEDED');expect(s.work[0].status).toBe('进行中');
});
test('interrupted recovery requires side-effect acknowledgement, failure is also allowed',()=>{
 for(const next of ['resume','fail']){const s=fixture(),e=createExecutionOn(s,input(s));command(s,e,'start');command(s,e,'interrupt');if(next==='resume'){atomic(s,()=>command(s,e,'resume'));command(s,e,'resume',{acknowledgeSideEffects:true});expect(e.state).toBe('RUNNING');}else{command(s,e,'fail');expect(e.state).toBe('FAILED');}}
});
test('ended execution rejects pending inputs; next execution has no inherited turns',()=>{
 const s=fixture(),e=createExecutionOn(s,input(s));command(s,e,'start');const t=submit(s,e);command(s,e,'cancel');expect(t.status).toBe('REJECTED');atomic(s,()=>receipt(s,e,t));const next=createExecutionOn(s,input(s));expect(next.id).not.toBe(e.id);expect(next.turns).toEqual([]);expect(s.messages[0].executionId).toBe(e.id);
});
test('adhoc association checks live competition, converts kind and cannot be repeated',()=>{
 const s=fixture(),existing=createExecutionOn(s,input(s)),a=createExecutionOn(s,{...input(s),kind:'ADHOC',workId:undefined});expect(a.workId).toBeNull();const link=()=>linkAdhocExecutionOn(s,{tenantId:'t',executionId:a.id,revision:a.revision,workId:'w',reason:'confirmed association'});atomic(s,link);command(s,existing,'cancel');link();expect(a.kind).toBe('INTERACTIVE');expect(a.workId).toBe('w');expect(a.branchReason).toBe('STRATEGY');expect(a.seqNo).toBe(2);atomic(s,link);
});

test('every displayed execution command is legal for its kind, state and last-turn completion',()=>{
 for(const kind of ['INTERACTIVE','AUTONOMOUS','ADHOC'])for(const state of ['CREATED','RUNNING','PAUSED','INTERRUPTED','SUCCEEDED','FAILED','CANCELLED'])for(const completed of [false,true]){
  const e={kind,state,turns:completed?[{runState:'COMPLETED',status:'ACCEPTED'}]:[]};
  expect(availableExecutionCommands(e).includes('finish')).toBe(kind!=='AUTONOMOUS'&&state==='PAUSED'&&completed);
  for(const action of availableExecutionCommands(e)){
   const s=fixture(),record=createExecutionOn(s,kind==='AUTONOMOUS'?{...input(s),kind,agentId:'a'}:kind==='ADHOC'?{...input(s),kind,workId:undefined}:input(s),{systemDispatch:kind==='AUTONOMOUS'});
   record.state=state;record.turns=structuredClone(e.turns);
   expect(()=>command(s,record,action,{acknowledgeSideEffects:true})).not.toThrow();
  }
 }
});
test('branch action freezes parent revision and dispatches assigned autonomous agent without input inheritance',()=>{
 for(const kind of ['INTERACTIVE','AUTONOMOUS']){
  const s=fixture(),p=createExecutionOn(s,{...input(s),kind,...(kind==='AUTONOMOUS'?{agentId:'a'}:kind==='ADHOC'?{workId:undefined}:{})},{systemDispatch:kind==='AUTONOMOUS'});command(s,p,'start');command(s,p,'interrupt');
  const args={tenantId:'t',executionId:p.id,revision:p.revision,nodeId:'n',runtime:'Codex CLI',branchReason:'RETRY_INFRA',reason:'explicit side-effect review'};
  atomic(s,()=>createExecutionBranchOn(s,{...args,revision:p.revision-1}));
  const branch=createExecutionBranchOn(s,args);expect(branch.parentExecutionId).toBe(p.id);expect(branch.kind).toBe(kind);expect(branch.initiatorKind).toBe(kind==='AUTONOMOUS'?'SYSTEM':'HUMAN');expect(branch.turns).toEqual([]);expect(branch.workSnapshot.previewResources).toEqual([]);expect(p.endReason).toBe('SUPERSEDED_BY_RETRY');atomic(s,()=>createExecutionBranchOn(s,args));
 }
});

import { requestExecutionInteractionOn, decideExecutionInteractionOn, deliverExecutionInteractionOn, expireExecutionInteractionOn, interactionResponseProblem, executionMaintenanceProblem } from '../src/executions';
const dispatchInput=(s,e,extra={})=>({tenantId:s.activeTenant,executionId:e.id,revision:e.revision,dispatchEpoch:e.dispatchEpoch,...extra});
const runningInteraction=(kind='INTERACTIVE',runtime='Claude Code')=>{
 const s=fixture();s.nodes[0].runtime=runtime;s.agents[0].runtime=runtime;
 const e=createExecutionOn(s,{...input(s),kind,runtime,authorizationMode:runtime==='Claude Code'?'ON_REQUEST':'READ_ONLY',...(kind==='AUTONOMOUS'?{agentId:'a'}:kind==='ADHOC'?{workId:undefined}:{})},{systemDispatch:kind==='AUTONOMOUS'});command(s,e,'start');
 let turn;if(kind!=='AUTONOMOUS'){turn=submit(s,e);receipt(s,e,turn);}return {s,e,turn};
};
const request=({s,e,turn},extra={})=>requestExecutionInteractionOn(s,dispatchInput(s,e,{interactionKind:'TOOL_PERMISSION',nativeRequestId:'native-1',title:'Run a bounded check',turnId:turn?.id,...extra}));
const decide=({s,e},i,extra={})=>decideExecutionInteractionOn(s,dispatchInput(s,e,{interactionId:i.id,action:'ACCEPT',...extra}));

test('interaction request/decision/delivery are separate, same-decision retries canonical and audit excludes content',()=>{
 const f=runningInteraction(),i=request(f,{interactionKind:'RUNTIME_INPUT',schemaJson:{type:'object',properties:{a:{type:'string'},b:{type:'integer'}},required:['a','b'],additionalProperties:false}});
 expect(i.state).toBe('PENDING');expect(i.dispatchEpoch).toBe(1);const revision=f.e.revision;
 decide(f,i,{content:{b:2,a:'private answer'}});expect(i.state).toBe('DECIDED');expect(i.deliveredAt).toBeUndefined();const before=structuredClone(f.s);
 decide(f,i,{revision,content:{a:'private answer',b:2}});expect(f.s).toEqual(before);
 atomic(f.s,()=>decide(f,i,{content:{a:'different',b:2}}));
 expect(f.e.events.some(e=>e.text.includes('private answer'))).toBe(false);
 deliverExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id,outcome:'DELIVERED'}));expect(i.state).toBe('DELIVERED');expect(i.deliveredAt).toBeTruthy();expect(f.s.work[0].status).toBe('进行中');atomic(f.s,()=>decide(f,i,{content:{a:'private answer',b:2}}));
});
test('decline ignores response content, stays separate from termination, and failed delivery preserves decision',()=>{
 const f=runningInteraction(),i=request(f);decide(f,i,{action:'DECLINE'});expect(i.responseAction).toBe('DECLINE');expect(f.e.state).toBe('RUNNING');
 deliverExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id,outcome:'FAILED'}));expect(i.closedReason).toBe('DELIVERY_FAILED');expect(i.state).toBe('CANCELLED');expect(i.decidedAt).toBeTruthy();expect(i.responseAction).toBe('DECLINE');
});
test('all interrupt/pause/fail/cancel paths close pending and undelivered requests; resume increments epoch',()=>{
 for(const action of ['pause','interrupt','fail','cancel'])for(const decided of [false,true]){
  const f=runningInteraction(),i=request(f);if(decided)decide(f,i);const epoch=f.e.dispatchEpoch;command(f.s,f.e,action);expect(i.state).toBe('CANCELLED');expect(i.closedReason).toBe(decided?'DELIVERY_FAILED':action==='interrupt'?'FENCED':'EXECUTION_ENDED');
  if(['pause','interrupt'].includes(action)){command(f.s,f.e,'resume',{acknowledgeSideEffects:true});expect(f.e.dispatchEpoch).toBe(epoch+1);atomic(f.s,()=>deliverExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{dispatchEpoch:epoch,interactionId:i.id,outcome:'DELIVERED'})));}
 }
});
test('request repeats deduplicate within the same epoch and never overwrite changed payloads',()=>{
 const f=runningInteraction(),i=request(f),before=structuredClone(f.s);expect(request(f).id).toBe(i.id);expect(f.s).toEqual(before);atomic(f.s,()=>request(f,{title:'different'}));
 command(f.s,f.e,'pause');command(f.s,f.e,'resume');const nextTurn=submit(f.s,f.e);receipt(f.s,f.e,nextTurn);f.turn=nextTurn;const next=request(f);expect(next.id).not.toBe(i.id);expect(next.dispatchEpoch).toBe(2);
});
test('requests expire no later than thirty minutes and cannot be answered after deadline',()=>{
 const f=runningInteraction(),i=request(f,{expiresAt:new Date(Date.now()+1000).toISOString()});expect(Date.parse(i.expiresAt)-Date.parse(i.createdAt)).toBeLessThanOrEqual(1000);
 atomic(f.s,()=>expireExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id})));
 expireExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id,simulateDeadline:true}));expect(i.state).toBe('EXPIRED');expect(i.expiryMode).toBe('SIMULATED_CLOCK');atomic(f.s,()=>decide(f,i));
 const past=runningInteraction(),expired=request(past,{expiresAt:'2020-01-01T00:00:00Z'});expect(expired.state).toBe('EXPIRED');atomic(past.s,()=>decide(past,expired));
});
test('Human response authority is distinct from reading, membership, and scenario maintenance, including retries',()=>{
 const f=runningInteraction('AUTONOMOUS'),i=request(f);expect(interactionResponseProblem(f.s,f.e)).toContain('权限');atomic(f.s,()=>decide(f,i));
 f.s.organization.projectRoles[0].permissions=['interaction_respond'];expect(interactionResponseProblem(f.s,f.e)).toBe('');decide(f,i);const args=dispatchInput(f.s,f.e,{interactionId:i.id,action:'ACCEPT'});
 f.s.organization.projectRoles[0].permissions=[];atomic(f.s,()=>decideExecutionInteractionOn(f.s,args));
 f.s.role='external';atomic(f.s,()=>decideExecutionInteractionOn(f.s,args));
});
test('Codex cannot request Web tool approval, while explicit runtime-input scenario remains available',()=>{
 const f=runningInteraction('INTERACTIVE','Codex CLI');expect(f.e.capabilities.toolPermission).toBe(false);atomic(f.s,()=>request(f));const i=request(f,{interactionKind:'RUNTIME_INPUT'});expect(i.state).toBe('PENDING');
 const s=fixture();atomic(s,()=>createExecutionOn(s,{...input(s),authorizationMode:'ON_REQUEST'}));
});
test('JSON request schema and response preserve validation and byte boundaries',()=>{
 const f=runningInteraction(),i=request(f,{interactionKind:'RUNTIME_INPUT',schemaJson:{type:'object',properties:{answer:{type:'string'}},required:['answer'],additionalProperties:false}});
 for(const content of [null,[],42,{}, {answer:2},{answer:'ok',extra:true},{answer:'中'.repeat(11000)}])atomic(f.s,()=>decide(f,i,{content}));
 atomic(f.s,()=>request(f,{nativeRequestId:'unsupported',interactionKind:'RUNTIME_INPUT',schemaJson:{type:'object',format:'unknown'}}));
 decide(f,i,{content:{answer:'valid'}});expect(i.responseContent).toEqual({answer:'valid'});
});
test('waiting interaction blocks new generic input and turn completion, not cancellation',()=>{
 const f=runningInteraction(),i=request(f);expect(executionInputProblem(f.s,f.e.id)).toContain('等待');atomic(f.s,()=>submit(f.s,f.e));atomic(f.s,()=>completeExecutionTurnOn(f.s,dispatchInput(f.s,f.e,{turnId:f.turn.id,reason:'not finished'})));decide(f,i);atomic(f.s,()=>submit(f.s,f.e));command(f.s,f.e,'cancel');expect(i.state).toBe('CANCELLED');
});
test('request and response tenant/archive/offline/epoch/revision failures are atomic',()=>{
 for(const change of [f=>f.s.activeTenant='other',f=>f.s.offline=true,f=>f.s.projects[0].archivedAt='2026-10-14',f=>f.e.dispatchEpoch++]){
  const f=runningInteraction(),i=request(f),args=dispatchInput(f.s,f.e,{interactionId:i.id,action:'ACCEPT'});change(f);atomic(f.s,()=>decideExecutionInteractionOn(f.s,args));
 }
 const f=runningInteraction(),i=request(f);atomic(f.s,()=>decide(f,i,{revision:f.e.revision-1}));
});

test('runtime request redelivery ignores stale UI revision but does not duplicate the request',()=>{
 const f=runningInteraction(),revision=f.e.revision,i=request(f),before=structuredClone(f.s);expect(request(f,{revision}).id).toBe(i.id);expect(f.s).toEqual(before);
});
test('number input schema rejects every nonfinite value rather than persist it as null',()=>{
 const f=runningInteraction(),i=request(f,{interactionKind:'RUNTIME_INPUT',schemaJson:{type:'object',properties:{value:{type:'number'}},required:['value']}});for(const value of [Infinity,-Infinity,NaN,JSON.parse('1e400')])atomic(f.s,()=>decide(f,i,{content:{value}}));decide(f,i,{content:{value:1.5}});expect(i.responseContent.value).toBe(1.5);
});
test('interaction cap counts unique requests within a dispatch, retries do not consume quota',()=>{
 const f=runningInteraction();for(let n=0;n<100;n++)request(f,{nativeRequestId:'r-'+n});const before=structuredClone(f.s);expect(request(f,{nativeRequestId:'r-0'}).id).toBe(f.e.interactions[0].id);expect(f.s).toEqual(before);atomic(f.s,()=>request(f,{nativeRequestId:'r-100'}));
});


test('maintenance authority remains distinct from response for every execution kind and participant case',()=>{
 for(const kind of ['INTERACTIVE','ADHOC','AUTONOMOUS']){
  const f=runningInteraction(kind),i=request(f,{interactionKind:'RUNTIME_INPUT'});
  expect(executionMaintenanceProblem(f.s,f.e)).toBe('');
  expect(Boolean(interactionResponseProblem(f.s,f.e))).toBe(kind!=='INTERACTIVE');
  f.e.initiatorId='H-zhou';f.e.participantIds=[];
  f.s.organization.projectRoles[0].permissions=['interaction_respond'];
  expect(interactionResponseProblem(f.s,f.e)).toBe('');
  expect(executionMaintenanceProblem(f.s,f.e)).toContain('不能维护');
  atomic(f.s,()=>expireExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id,simulateDeadline:true})));
  atomic(f.s,()=>command(f.s,f.e,'pause'));
  f.s.organization.projectRoles[0].permissions=[];
  expect(interactionResponseProblem(f.s,f.e)).toContain('权限');
  f.e.participantIds=['H-lin'];expect(executionMaintenanceProblem(f.s,f.e)).toBe('');
  expireExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id,simulateDeadline:true}));
  expect(i.state).toBe('EXPIRED');
  f.s.organization.projectRoles=[];expect(executionMaintenanceProblem(f.s,f.e)).toContain('访问权限');
 }
});
test('own ADHOC can maintain expiry and unblock generic input without acquiring response authority',()=>{
 const f=runningInteraction('ADHOC'),i=request(f,{interactionKind:'RUNTIME_INPUT'});
 expect(interactionResponseProblem(f.s,f.e)).toContain('权限');
 expect(executionInputProblem(f.s,f.e.id)).toContain('等待');
 i.expiresAt=new Date(Date.now()-1).toISOString();
 expireExecutionInteractionOn(f.s,dispatchInput(f.s,f.e,{interactionId:i.id}));
 expect(i.state).toBe('EXPIRED');expect(i.expiryMode).toBe('OBSERVED_DEADLINE');
 expect(executionInputProblem(f.s,f.e.id)).toBe('');
 expect(interactionResponseProblem(f.s,f.e)).toContain('权限');
});
