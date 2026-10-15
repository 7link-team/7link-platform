// Explicit local scenarios; no runtime execution or authority is inferred from these fixtures.
globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const {read}=await import('../src/store');
const {createExecutionOn,executionCommandOn,submitExecutionInputOn,resolveExecutionInputOn,requestExecutionInteractionOn}=await import('../src/executions');
const s=structuredClone(read());s.role='executor';s.projects.find(p=>p.id==='launch').ownerId='H-zhou';s.executions=[];
s.organization.projectRoles=s.organization.projectRoles.filter(r=>!(r.projectId==='launch'&&r.memberId==='H-lin'));
s.organization.projectRoles.push({projectId:'launch',memberId:'H-lin',role:'executor',permissions:[]});
const make=title=>{
 const e=createExecutionOn(s,{tenantId:s.activeTenant,kind:'ADHOC',projectId:'launch',nodeId:'n1',runtime:'Codex CLI',title});
 const context=()=>({tenantId:s.activeTenant,executionId:e.id,revision:e.revision,dispatchEpoch:e.dispatchEpoch});
 executionCommandOn(s,{...context(),action:'start',reason:'Explicit local fixture acceptance'});
 const t=submitExecutionInputOn(s,{...context(),text:'A local input requiring clarification'});
 resolveExecutionInputOn(s,{...context(),turnId:t.id,outcome:'ACCEPTED',reason:'Local fixture turn receipt'});
 return {e,context,t};
};
const own=make('Own ADHOC maintenance without response privilege');
const expired=requestExecutionInteractionOn(s,{...own.context(),interactionKind:'RUNTIME_INPUT',nativeRequestId:'own-deadline',turnId:own.t.id,title:'Elapsed local deadline'});
// Represent an elapsed deadline, leaving the real component effect to record expiry.
expired.expiresAt=new Date(Date.now()-1000).toISOString();
const observer=make('Other Human execution visible to an observer');
requestExecutionInteractionOn(s,{...observer.context(),interactionKind:'RUNTIME_INPUT',nativeRequestId:'observer-pending',turnId:observer.t.id,title:'Other Human request'});
observer.e.initiatorId='H-zhou';observer.e.participantIds=[];
const legacy=make('Legacy record without explicit Interaction capabilities');legacy.e.capabilities={interactiveInput:true,source:'PROTOTYPE_SCENARIO'};
s.testExecutionMaintenance={own:own.e.id,observer:observer.e.id,legacy:legacy.e.id};
console.log(JSON.stringify(s));
