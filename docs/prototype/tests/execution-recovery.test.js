import { test, expect } from 'bun:test';
import { createExecutionOn, createExecutionBranchOn, executionCommandOn, submitExecutionInputOn, resolveExecutionInputOn, executionSafetyBoundaryProblem, executionFailureKinds, executionFailureActions } from '../src/executions';
const fixture=()=>({activeTenant:'t',role:'executor',organization:{members:[{id:'H-lin',kind:'Human',name:'Lin'}],projectRoles:[{projectId:'p',memberId:'H-lin'}]},projects:[{id:'p',ownerId:'other'}],nodes:[{id:'n',status:'在线',runtime:'Codex CLI'}],work:[{id:'w',project:'p',title:'Original',version:1,status:'进行中',plan:true,flow:true,flowVersion:1,team:{executorIds:['H-lin']}}],executions:[],messages:[]});
const args={tenantId:'t',kind:'INTERACTIVE',projectId:'p',workId:'w',nodeId:'n',runtime:'Codex CLI'};
const context=e=>({tenantId:'t',executionId:e.id,revision:e.revision});
const command=(s,e,action,extra={})=>executionCommandOn(s,{...context(e),action,reason:'核对本地事实',...extra});
const setup=()=>{const s=fixture(),e=createExecutionOn(s,args);command(s,e,'start');return {s,e};};
const atomic=(s,fn)=>{const before=structuredClone(s);expect(fn).toThrow();expect(s).toEqual(before);};
const register=(s,e,extra={})=>command(s,e,'operation-register',{title:'发布动作',safe:true,retryLimit:2,...extra});
test('supplement receipt records received, boundary wait and applied independently of turn completion',()=>{
 const {s,e}=setup(),t=submitExecutionInputOn(s,{...context(e),text:'调整约束'});
 expect(t.applicationState).toBe('RECEIVED');
 resolveExecutionInputOn(s,{...context(e),turnId:t.id,outcome:'WAITING_BOUNDARY',reason:'等待当前工具返回'});
 expect(t.status).toBe('PENDING');expect(t.applicationState).toBe('WAITING_BOUNDARY');
 resolveExecutionInputOn(s,{...context(e),turnId:t.id,outcome:'ACCEPTED',reason:'当前工具已结束，新约束已应用'});
 expect(t.status).toBe('ACCEPTED');expect(t.runState).toBeUndefined();
 expect(t.applicationReceipts.map(r=>r.state)).toEqual(['RECEIVED','WAITING_BOUNDARY','APPLIED']);
 expect(t.applicationReceipts.every(r=>r.mode==='LOCAL_SIMULATION')).toBe(true);
});
test('pending supplements cannot silently move into another dispatch and retain unapplicable receipt',()=>{
 for(const action of ['interrupt','pause','cancel','fail']){const {s,e}=setup(),t=submitExecutionInputOn(s,{...context(e),text:'补充'});command(s,e,action);expect(t.applicationState).toBe('UNAPPLICABLE');expect(t.status).toBe('REJECTED');expect(t.applicationReceipts.at(-1).reason).not.toBe('');}
});
test('five failure classes have explicit different next actions without changing work or auto dispatch',()=>{
 expect(Object.keys(executionFailureKinds)).toHaveLength(5);
 for(const kind of Object.keys(executionFailureKinds)){const {s,e}=setup(),before=structuredClone(s.work);command(s,e,'fail',{failureKind:kind});expect(e.failure.kind).toBe(kind);expect(e.failure.nextAction).toBe(executionFailureActions[kind]);expect(e.state).toBe('FAILED');expect(s.executions).toHaveLength(1);expect(s.work).toEqual(before);}
 const {s,e}=setup();atomic(s,()=>command(s,e,'fail',{failureKind:'OTHER'}));command(s,e,'fail');expect(e.failure).toBeUndefined();command(s,e,'classify-failure',{failureKind:'QUALITY'});command(s,e,'classify-failure',{failureKind:'SCOPE_CHANGE'});expect(e.failureHistory[0].kind).toBe('QUALITY');
});
test('only unstarted safe operations get finite retries; invalid limits and started replays are atomic',()=>{
 const {s,e}=setup();for(const retryLimit of [-1,Infinity,1.5,NaN])atomic(s,()=>register(s,e,{retryLimit}));
 const unsafe=register(s,e,{safe:false});atomic(s,()=>command(s,e,'operation-retry',{operationId:unsafe.id}));
 const o=register(s,e);for(let i=0;i<2;i++)command(s,e,'operation-retry',{operationId:o.id});expect(o.retryCount).toBe(2);atomic(s,()=>command(s,e,'operation-retry',{operationId:o.id}));
 command(s,e,'operation-start',{operationId:o.id});atomic(s,()=>command(s,e,'operation-retry',{operationId:o.id}));expect(executionSafetyBoundaryProblem(e)).not.toBe('');
 const started=register(s,e);command(s,e,'operation-start',{operationId:started.id});expect(started.retryCount).toBe(0);atomic(s,()=>command(s,e,'operation-retry',{operationId:started.id}));
});
test('interruption fences unknown effects until evidenced verification or compensation; no external replay',()=>{
 for(const action of ['operation-verify','operation-compensate']){const {s,e}=setup(),o=register(s,e);command(s,e,'operation-start',{operationId:o.id});command(s,e,'interrupt');expect(o.sideEffects).toBe('UNKNOWN');atomic(s,()=>command(s,e,'resume',{acknowledgeSideEffects:true}));atomic(s,()=>createExecutionBranchOn(s,{...args,...context(e),branchReason:'RETRY_INFRA',reason:'换节点'}));atomic(s,()=>command(s,e,action,{operationId:o.id,sideEffects:'UNKNOWN',evidence:'不能确认'}));atomic(s,()=>command(s,e,action,{operationId:o.id,sideEffects:'NONE'}));command(s,e,action,{operationId:o.id,sideEffects:'KNOWN',evidence:'外部查询结果已核对（本地场景）'});expect(executionSafetyBoundaryProblem(e)).toBe('');command(s,e,'resume',{acknowledgeSideEffects:true});expect(o.retryCount).toBe(0);atomic(s,()=>command(s,e,'operation-start',{operationId:o.id}));}
});
test('operation and application writes reuse tenant, archive, offline, role and revision guards',()=>{
 for(const mutate of [s=>s.activeTenant='other',s=>s.offline=true,s=>s.projects[0].archivedAt='now',s=>s.role='external',s=>s.organization.projectRoles=[]]){const {s,e}=setup();mutate(s);atomic(s,()=>register(s,e));}
 const {s,e}=setup(),old=context(e);register(s,e);atomic(s,()=>executionCommandOn(s,{...old,action:'operation-register',reason:'stale',title:'x',safe:true,retryLimit:1}));
});

test('application is blocked while an operation has not reached its safety boundary',()=>{
 const {s,e}=setup(),o=register(s,e);command(s,e,'operation-start',{operationId:o.id});const t=submitExecutionInputOn(s,{...context(e),text:'替换参数'});
 atomic(s,()=>resolveExecutionInputOn(s,{...context(e),turnId:t.id,outcome:'ACCEPTED',reason:'文字声称完成'}));
 command(s,e,'operation-verify',{operationId:o.id,sideEffects:'NONE',evidence:'当前调用结束且无副作用'});resolveExecutionInputOn(s,{...context(e),turnId:t.id,outcome:'ACCEPTED',reason:'已到安全边界'});expect(t.applicationState).toBe('APPLIED');
});

test('actual execution panel exposes local receipt, failure route and operation state without pretending real effects',async()=>{
 if(!globalThis.localStorage){const data=new Map();globalThis.localStorage={getItem:k=>data.get(k)||null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)};}
 const React=await import('react'),{renderToStaticMarkup}=await import('react-dom/server'),api=await import('../src/store'),{ExecutionProcess}=await import('../src/execution-ui');
 api.reset();const {s,e}=setup(),t=submitExecutionInputOn(s,{...context(e),text:'新参数'});register(s,e);command(s,e,'fail',{failureKind:'SCOPE_CHANGE'});
 api.update(current=>current.executions.push({...structuredClone(e),tenantId:current.activeTenant,projectId:'launch',workId:'W-104'}));
 let dispatcher,previous,html;
 function Harness(){dispatcher=React.default.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE.H;previous=dispatcher.useSyncExternalStore;dispatcher.useSyncExternalStore=(_subscribe,getSnapshot)=>getSnapshot();return React.createElement(ExecutionProcess,{executionId:e.id,toast:()=>{},onWork:()=>{}});}
 try{html=renderToStaticMarkup(React.createElement(Harness));}finally{if(dispatcher)dispatcher.useSyncExternalStore=previous;}
 for(const text of ['失败处置与操作副作用','起草关联原任务的新范围','无法应用','应用回执历史','未启动','安全重试 0 / 2','不执行真实外部操作'])expect(html).toContain(text);
});
