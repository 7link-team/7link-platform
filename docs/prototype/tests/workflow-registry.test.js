import { test, expect } from "bun:test";
import { initial } from "../src/data.js";
import { workflowRegistryRows, workflowRegistryCommandOn, workflowRegistryProblem, workflowFixedGraph, workflowLinkedWorks } from "../src/workflow-registry.js";
const state = () => ({ ...structuredClone(initial), activeTenant: "tenant-test" });
const defaults = s => ({ flowNodes:s.flowNodes,flowEdges:s.flowEdges,flowVersion:3,flowDraft:{nodes:[{id:"UNPUBLISHED"}],edges:[]},flowVersions:{2:{nodes:[{id:"fixed-v2",data:{label:"old"}}],edges:[]}} });
let sequence = 0;
const act = (s, input = {}, dependencies = {}) => workflowRegistryCommandOn(s,{tenantId:s.activeTenant,projectId:"launch",action:"create",name:`工作流 ${++sequence}`,description:"test definition",reason:"Human explicit definition management",sourceVersion:3,...input},{defaultFlow:defaults(s),makeId:()=>`WF-${sequence}`,now:"2026-10-14T12:00:00Z",...dependencies});
const get = (s,id) => s.projectWorkflows.launch.find(record=>record.id===id);
const atomic = (s, fn) => {const before=structuredClone(s);expect(fn).toThrow();expect(s).toEqual(before);};
test("legacy default is virtual; multiple new workflows start as independent unpublished drafts",()=>{
 const s=state(),before=structuredClone(s),legacy=defaults(s);
 expect(workflowRegistryRows(s,"launch",legacy)[0]).toMatchObject({id:"default",legacy:true});expect(s).toEqual(before);
 const first=act(s,{name:"协作交付"}),second=act(s,{name:"构建与协作"});
 expect(first).not.toBe(second);expect(workflowRegistryRows(s,"launch",legacy)).toHaveLength(3);
 for(const id of [first,second]){const record=get(s,id);expect(record.flow.flowVersion).toBe(0);expect(record.flow.flowNodes).toEqual([]);expect(record.flow.flowDraft.nodes).toEqual(s.flowNodes);expect(record.flow.flowDraft.nodes.some(n=>n.id==="UNPUBLISHED")).toBe(false);expect(record.history[0].origin).toEqual({workflowId:"default",version:3});}
 get(s,first).flow.flowDraft.nodes[0].data.label="changed";expect(get(s,second).flow.flowDraft.nodes[0].data.label).toBe(s.flowNodes[0].data.label);expect(s.flowNodes).toEqual(before.flowNodes);
});
test("clone selects a canonical fixed version, never an arbitrary graph, latest version or source draft",()=>{
 const s=state(),sourceId=act(s,{name:"source"}),source=get(s,sourceId);
 source.flow.flowVersion=4;source.flow.flowNodes=[{id:"v4",data:{}}];source.flow.flowEdges=[];source.flow.flowVersions={2:{nodes:[{id:"v2",data:{label:"old"}}],edges:[]}};
 const cloneId=act(s,{action:"clone",name:"fixed clone",sourceId,sourceVersion:2,graph:{nodes:[{id:"forged"}],edges:[]}});
 expect(get(s,cloneId).flow.flowDraft.nodes[0].id).toBe("v2");expect(get(s,cloneId).flow.flowVersion).toBe(0);
 get(s,cloneId).flow.flowDraft.nodes[0].data.label="edit clone";expect(source.flow.flowVersions[2].nodes[0].data.label).toBe("old");
 for(const sourceVersion of [0,1,99,"2",2.5])atomic(s,()=>act(s,{action:"clone",sourceId,sourceVersion}));
 atomic(s,()=>act(s,{action:"clone",sourceId:"foreign-workflow",sourceVersion:2}));
});
test("registry checks current Human owner, tenant, project, offline and archived project before mutation",()=>{
 for(const change of [s=>s.role="executor",s=>s.role="external",s=>s.organization.members=s.organization.members.filter(p=>p.id!=="H-lin"),s=>s.offline=true,s=>s.projects.find(p=>p.id==="launch").archivedAt="now"]){const s=state();change(s);expect(workflowRegistryProblem(s,"launch")).not.toBe("");atomic(s,()=>act(s));}
 const s=state();atomic(s,()=>act(s,{tenantId:"other"}));atomic(s,()=>act(s,{projectId:"missing"}));
 expect(workflowRegistryRows({...s,role:"external"},"launch",defaults(s))).toEqual([]);
});
test("metadata revisions and unique names reject stale forms atomically including archived names",()=>{
 const s=state(),id=act(s,{name:"one"}),other=act(s,{name:"two"}),record=get(s,id);
 atomic(s,()=>act(s,{name:" ONE "}));atomic(s,()=>act(s,{name:"项目交付流程"}));
 atomic(s,()=>act(s,{action:"rename",id,revision:99,name:"new"}));
 act(s,{action:"rename",id,revision:record.revision,name:"renamed",description:"changed"});expect(record.revision).toBe(2);expect(record.history.at(-1).previous.name).toBe("one");
 atomic(s,()=>act(s,{action:"rename",id,revision:1,name:"stale"}));
 atomic(s,()=>act(s,{action:"rename",id,revision:record.revision,name:"two"}));
 act(s,{action:"archive",id:other,revision:get(s,other).revision});atomic(s,()=>act(s,{name:"two"}));
 for(const action of ["archive","rename"])atomic(s,()=>act(s,{action,id:"default",revision:0}));
});
test("archive preserves all definitions drafts work versions and historical lookup",()=>{
 const s=state(),id=act(s,{name:"retained"}),record=get(s,id);record.flow.flowVersion=1;record.flow.flowNodes=structuredClone(record.flow.flowDraft.nodes);record.flow.flowEdges=structuredClone(record.flow.flowDraft.edges);
 s.work.push({...structuredClone(s.work[0]),id:"linked",project:"launch",workflowId:id,flowVersion:1});
 const frozen=structuredClone(record.flow),works=structuredClone(s.work);
 act(s,{action:"archive",id,revision:record.revision});expect(record.archivedAt).toBe("2026-10-14T12:00:00Z");expect(record.flow).toEqual(frozen);expect(s.work).toEqual(works);expect(workflowFixedGraph(record,1).nodes).toEqual(frozen.flowNodes);
 atomic(s,()=>act(s,{action:"rename",id,revision:record.revision}));atomic(s,()=>act(s,{action:"archive",id,revision:record.revision}));
 const clone=act(s,{action:"clone",name:"explicit restart draft",sourceId:id,sourceVersion:1});expect(get(s,clone).flow.flowVersion).toBe(0);
});
test("linked work filters workflow identity and frozen version without fabricating runs",()=>{
 const s=state();s.work=[{id:"legacy",project:"launch",flowVersion:3},{id:"explicit-default",project:"launch",workflowId:"default",flowVersion:2},{id:"new",project:"launch",workflowId:"WF-other",flowVersion:3},{id:"pending",project:"launch",workflowId:"WF-other"},{id:"foreign",project:"film",workflowId:"WF-other",flowVersion:3}];
 expect(workflowLinkedWorks(s,"launch").map(w=>w.id)).toEqual(["legacy","explicit-default"]);
 expect(workflowLinkedWorks(s,"launch","WF-other",3).map(w=>w.id)).toEqual(["new"]);
 expect(workflowLinkedWorks(s,"launch","WF-other").map(w=>w.id)).toEqual(["new","pending"]);
 expect(workflowLinkedWorks(s,"launch","WF-none")).toEqual([]);expect(workflowLinkedWorks(s,"launch","WF-other",0)).toEqual([]);
});
test("missing source graphs and duplicate generated IDs cannot partially create registry state",()=>{
 const s=state();atomic(s,()=>act(s,{}, {defaultFlow:{flowNodes:[],flowEdges:[],flowVersion:0,flowVersions:{}}}));
 atomic(s,()=>act(s,{}, {makeId:()=>"default"}));expect(s.projectWorkflows).toBeUndefined();
 const id=act(s);atomic(s,()=>act(s,{}, {makeId:()=>id}));
});

test("a new project can create its first workflow from the initial template without claiming publication",()=>{
 const s=state(),initialFlow={...defaults(s),flowVersion:0,flowVersions:{}};
 const id=act(s,{sourceVersion:undefined},{defaultFlow:initialFlow}),record=get(s,id);
 expect(record.flow.flowVersion).toBe(0);expect(record.flow.flowDraft.nodes).toEqual(initialFlow.flowNodes);expect(record.history[0].origin).toEqual({workflowId:"default",version:0,kind:"INITIAL_TEMPLATE"});expect(record.flow.flowDraft.nodes.some(n=>n.id==="UNPUBLISHED")).toBe(false);
 atomic(s,()=>act(s,{action:"clone",sourceId:"default",sourceVersion:0},{defaultFlow:initialFlow}));
 const id2=act(s,{sourceVersion:0},{defaultFlow:initialFlow});expect(get(s,id2).flow.flowVersion).toBe(0);
});
