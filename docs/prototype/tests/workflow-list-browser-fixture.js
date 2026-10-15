globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const api=await import('../src/store.js');
const {workflowRegistryCommandOn}=await import('../src/workflow-registry.js');
api.update(s=>{
  for(let index=1;index<=25;index++){
    const workflowId=workflowRegistryCommandOn(s,{tenantId:s.activeTenant,projectId:'launch',action:'create',sourceId:'default',sourceVersion:3,name:`LIST_FLOW_${String(index).padStart(2,'0')}`,description:`Workflow query fixture ${index}`,reason:'Explicit local browser fixture'},{defaultFlow:api.projectFlow(s,'launch'),makeId:()=>`FLOW-LIST-${index}`});
    const record=s.projectWorkflows.launch.find(r=>r.id===workflowId);
    record.flow.flowDraft.nodes[0].data.privateTestMarker='DO_NOT_EXPORT_NODE_CONFIGURATION';
    if(index>23)workflowRegistryCommandOn(s,{tenantId:s.activeTenant,projectId:'launch',action:'archive',id:workflowId,revision:record.revision,reason:'Archived fixture remains traceable'},{defaultFlow:api.projectFlow(s,'launch')});
  }
});
console.log(JSON.stringify(api.read()));
