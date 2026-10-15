import {test,expect,beforeEach} from 'bun:test';
const entries=new Map();globalThis.localStorage={getItem:k=>entries.get(k)||null,setItem:(k,v)=>entries.set(k,v),removeItem:k=>entries.delete(k)};
const {read,reset,update,workCommand,executionCommand,createExecutionBranch}=await import('../src/store');
beforeEach(()=>{reset();update(s=>{s.executions=[];const w=s.work.find(w=>w.id==='W-105');w.team={executorIds:['agent:atlas'],checkerIds:[]};w.agentId='atlas';});workCommand('W-105','plan');workCommand('W-105','flow');});
const feedback=()=>({tenantId:read().activeTenant,agentId:'atlas',nodeId:'n1',runtime:'Codex CLI'});
const start=extra=>workCommand('W-105','start','',read().work.find(w=>w.id==='W-105').version,{...feedback(),...extra});
test('work dispatch atomically creates one autonomous fixed execution without inventing node acceptance',()=>{
 const before=read().work.find(w=>w.id==='W-105');start();const s=read(),w=s.work.find(w=>w.id==='W-105'),e=s.executions[0];
 expect(e.kind).toBe('AUTONOMOUS');expect(e.initiatorKind).toBe('SYSTEM');expect(e.state).toBe('CREATED');expect(e.workId).toBe(w.id);expect(e.workVersion).toBe(before.version);expect(e.workflowVersion).toBe(before.flowVersion);expect(e.workSnapshot.previewResources).toEqual([]);expect(w.status).toBe('待开始');expect(w.dispatchedExecutionId).toBe(e.id);
 const frozen=structuredClone(s);expect(()=>start()).toThrow();expect(read()).toEqual(frozen);
 executionCommand({tenantId:s.activeTenant,executionId:e.id,revision:e.revision,action:'start',reason:'explicit local node receipt'});expect(read().work.find(w=>w.id===before.id).status).toBe('进行中');expect(read().executions[0].state).toBe('RUNNING');expect(read().work.find(w=>w.id===before.id).accepted).toBe(false);
});
test('wrong agent, offline node, stale tenant and incompatible runtime roll back work and execution together',()=>{
 for(const extra of [{agentId:'quill'},{nodeId:'n3'},{runtime:'Claude Code'},{tenantId:'other'}]){const before=structuredClone(read());expect(()=>start(extra)).toThrow();expect(read()).toEqual(before);}
});
test('a work revision or assignment changed before node acceptance refuses the old execution atomically',()=>{
 for(const change of [w=>w.version++,w=>w.team.executorIds=[],w=>w.workflowId='another-flow']){
  reset();update(s=>{s.executions=[];const w=s.work.find(w=>w.id==='W-105');w.team={executorIds:['agent:atlas'],checkerIds:[]};});workCommand('W-105','plan');workCommand('W-105','flow');start();
  update(s=>change(s.work.find(w=>w.id==='W-105')));const e=read().executions[0],before=structuredClone(read());expect(()=>executionCommand({tenantId:read().activeTenant,executionId:e.id,revision:e.revision,action:'start',reason:'old input'})).toThrow();expect(read()).toEqual(before);
 }
});
test('Human work start preserves its own path and creates no fictional Agent execution',()=>{
 update(s=>{s.work.find(w=>w.id==='W-105').team.executorIds=['H-lin'];});workCommand('W-105','start');expect(read().work.find(w=>w.id==='W-105').status).toBe('进行中');expect(read().executions).toEqual([]);
});

test('retry of cancelled waiting dispatch transfers task link and advances only upon child acceptance',()=>{
 start();let e=read().executions[0];executionCommand({tenantId:read().activeTenant,executionId:e.id,revision:e.revision,action:'cancel',reason:'before acceptance'});e=read().executions[0];const child=createExecutionBranch({tenantId:read().activeTenant,executionId:e.id,revision:e.revision,branchReason:'RETRY_INFRA',reason:'new explicit dispatch',nodeId:'n1',runtime:'Codex CLI'});
 expect(read().work.find(w=>w.id==='W-105').dispatchedExecutionId).toBe(child.id);expect(child.dispatchedFromWorkCommand).toBe(true);expect(read().work.find(w=>w.id==='W-105').status).toBe('待开始');executionCommand({tenantId:read().activeTenant,executionId:child.id,revision:child.revision,action:'start',reason:'child local node acceptance'});expect(read().work.find(w=>w.id==='W-105').status).toBe('进行中');
});
