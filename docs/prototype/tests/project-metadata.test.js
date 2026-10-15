import { beforeEach, test, expect } from "bun:test";
const values=new Map();Object.defineProperty(globalThis,"localStorage",{configurable:true,writable:true,value:{getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)}});
const {read,reset,update,createProject,saveProjectMetadata,projectHumanOwner,projectMetadataWriteProblem,createTenant,switchTenant}=await import("../src/store.js");
beforeEach(reset);
const project=id=>read().projects.find(p=>p.id===id);
const input=(id="launch",patch={})=>{const p=project(id);return {tenantId:read().activeTenant,projectId:id,revision:p.revision??1,name:p.name,description:p.description||"",ownerId:projectHumanOwner(read(),p)?.id||"",deadline:p.deadline==="待确认"?"":p.deadline,...patch};};
const atomic=fn=>{const before=structuredClone(read());expect(fn).toThrow();expect(read()).toEqual(before);};

test("project metadata changes preserve stable project identity and every related work, plan, message and ownership fact",()=>{
 const before=structuredClone(read());const p=project("launch"),original=structuredClone(p);const earliest=Date.now();
 expect(saveProjectMetadata(input("launch",{name:"Renamed Project",description:"New project description",ownerId:"H-zhou",deadline:"2028-02-29"}))).toBe("launch");
 const result=project("launch");expect(result).toMatchObject({id:"launch",name:"Renamed Project",description:"New project description",ownerId:"H-zhou",owner:"周宁",deadline:"2028-02-29",revision:2});
 for(const key of ["work","messages","resources","organization","projectFlows","flowVersions","agents"]){expect(read()[key]).toEqual(before[key]);}
 for(const key of ["type","progress","budget","spent","color"]){expect(result[key]).toEqual(original[key]);}
 expect(read().projects.filter(p=>p.id!=="launch")).toEqual(before.projects.filter(p=>p.id!=="launch"));
 expect(result.history).toHaveLength(1);expect(result.history[0].previous).toEqual({name:original.name,description:original.description,owner:original.owner,deadline:original.deadline});
 expect(result.history[0].next.ownerId).toBe("H-zhou");expect(result.history[0].actorId).toBe("H-lin");expect(Date.parse(result.history[0].time)).toBeGreaterThanOrEqual(earliest);expect(read().events[0].target).toBe("/p/launch");
});
test("successive metadata versions retain original owner identity and reject old forms without partially changing history",()=>{
 const first=input();saveProjectMetadata({...first,name:"First name",ownerId:"H-zhou"});const fixed=structuredClone(project("launch").history[0]);
 const second=input();saveProjectMetadata({...second,name:"Second name",ownerId:"H-xu"});
 expect(project("launch").history[0]).toEqual(fixed);expect(project("launch").history[1].previous.ownerId).toBe("H-zhou");expect(project("launch").history[1].next.ownerId).toBe("H-xu");
 atomic(()=>saveProjectMetadata({...first,name:"Old overwrite"}));expect(project("launch").name).toBe("Second name");
});
test("actual dates allow leap days and overdue records, clearing returns existing pending convention",()=>{
 for(const deadline of ["2028-02-29","2020-01-01",""]){saveProjectMetadata(input("launch",{deadline}));expect(project("launch").deadline).toBe(deadline||"待确认");}
 for(const deadline of ["2026-02-29","2026-04-31","2026-13-01","2026-01-00","2026-1-2","2026-01-01T00:00:00Z","0000-01-01",null,22])atomic(()=>saveProjectMetadata(input("launch",{deadline})));
});
test("scope, owner IDs, field boundaries and corruption reject atomically with no generic field overwrite",()=>{
 for(const patch of [{tenantId:"wrong"},{projectId:"foreign"},{ownerId:"atlas"},{ownerId:"agent:atlas"},{ownerId:"周宁"},{ownerId:""},{name:" "},{name:"x".repeat(201)},{description:"x".repeat(2001)},{revision:0},{revision:NaN}])atomic(()=>saveProjectMetadata({...input(),...patch}));
 saveProjectMetadata({...input(),name:"x".repeat(200),description:"x".repeat(2000),budget:999999,spent:-1,id:"replacement",history:[],progress:100});
 expect(project("launch").budget).toBe(2400);expect(project("launch").progress).toBe(68);expect(project("launch").history).toHaveLength(1);
 for(const history of ["corrupt",[null],[{kind:"METADATA",previous:null,next:{}}],[{kind:"METADATA",previous:{},next:[]}]] ){update(s=>{s.projects.find(p=>p.id==="launch").history=history});atomic(()=>saveProjectMetadata(input("launch",{description:"new"})));}
});
test("only current Human project managers can save, with readonly for unrelated internal users and offline/external views",()=>{
 update(s=>{s.role="executor"});expect(projectMetadataWriteProblem(read(),project("launch"))).toBe("");saveProjectMetadata(input("launch",{description:"Project owner edit"}));
 atomic(()=>saveProjectMetadata(input("film",{description:"Unrelated edit"})));
 update(s=>{s.organization.projectRoles.push({id:"PR-explicit",memberId:"H-lin",projectId:"film",role:"项目负责人"})});saveProjectMetadata(input("film",{description:"Explicit project manager edit"}));
 for(const role of ["external","owner"]){update(s=>{s.role=role;s.offline=role==="owner"});atomic(()=>saveProjectMetadata(input("launch",{name:"Forbidden"})));}
 reset();update(s=>{s.organization.members=s.organization.members.filter(p=>p.id!=="H-lin")});atomic(()=>saveProjectMetadata(input("launch",{ownerId:"H-zhou"})));
});
test("legacy display names resolve only when unique while chosen stable IDs survive duplicate and changed names",()=>{
 update(s=>{s.organization.members.push({id:"H-other",name:"林予",kind:"Human"});s.organization.projectRoles=[];s.role="executor"});expect(projectHumanOwner(read(),project("launch"))).toBeNull();atomic(()=>saveProjectMetadata(input("launch",{ownerId:"H-lin"})));
 update(s=>{s.role="owner"});saveProjectMetadata(input("launch",{ownerId:"H-lin"}));update(s=>{s.organization.members.find(p=>p.id==="H-lin").name="New display name"});
 expect(projectHumanOwner(read(),project("launch")).id).toBe("H-lin");saveProjectMetadata(input("launch",{description:"Stable owner"}));expect(project("launch").ownerId).toBe("H-lin");expect(project("launch").owner).toBe("New display name");
});
test("tenant switching invalidates open forms even when a project ID appears in both tenants",()=>{
 const old=input();const original=structuredClone(project("launch"));const tenant=createTenant("Other tenant");
 update(s=>{s.projects.push({...structuredClone(original),name:"Other tenant project",ownerId:"H-lin",owner:"林予"})});atomic(()=>saveProjectMetadata({...old,name:"Cross tenant overwrite"}));
 saveProjectMetadata(input("launch",{name:"Current tenant edit"}));switchTenant("studio");expect(project("launch")).toEqual(original);switchTenant(tenant);expect(project("launch").name).toBe("Current tenant edit");
});
test("new projects can be edited and empty description is valid without creating spurious history for identical saves",()=>{
 const id=createProject({tenantId:read().activeTenant,name:"New project",description:"Goal",ownerId:"H-lin"});
 const before=structuredClone(read());saveProjectMetadata(input(id));expect(read()).toEqual(before);
 saveProjectMetadata(input(id,{description:""}));expect(project(id).description).toBe("");expect(project(id).deadline).toBe("待确认");
 const saved=structuredClone(read());saveProjectMetadata(input(id));expect(read()).toEqual(saved);
});
test("private work container is not transferred through team project metadata settings",()=>{
 update(s=>s.projects.push({id:"private",name:"私人工作",owner:"林予",ownerId:"H-lin",description:"",deadline:"待确认"}));atomic(()=>saveProjectMetadata(input("private",{ownerId:"H-zhou"})));
});

test("unknown and Agent roles fail closed even for the current Human project owner",()=>{
 for(const role of ["agent","unknown",null,undefined]){
  reset();update(s=>{s.role=role});expect(projectHumanOwner(read(),project("launch")).id).toBe("H-lin");
  expect(projectMetadataWriteProblem(read(),project("launch"))).not.toBe("");
  atomic(()=>saveProjectMetadata(input("launch",{description:"Forbidden role edit"})));
 }
});
test("archived project metadata is readonly for tenant and project owners",()=>{
 for(const role of ["owner","executor"]){
  reset();update(s=>{s.role=role});expect(projectMetadataWriteProblem(read(),project("launch"))).toBe("");
  saveProjectMetadata(input("launch",{description:"Before archive"}));
  update(s=>{s.projects.find(p=>p.id==="launch").archivedAt="2026-10-14T00:00:00Z"});
  expect(projectMetadataWriteProblem(read(),project("launch"))).toContain("已归档");
  atomic(()=>saveProjectMetadata(input("launch",{description:"Forbidden archive edit"})));
 }
});
test("duplicate project names remain supported by creation and metadata edits with stable distinct IDs",()=>{
 const name=project("film").name;const film=structuredClone(project("film"));
 const createdId=createProject({tenantId:read().activeTenant,name,description:"Duplicate name",ownerId:"H-lin"});
 expect(createdId).not.toBe("film");expect(createdId).not.toBe("launch");expect(project(createdId).name).toBe(name);
 expect(saveProjectMetadata(input("launch",{name}))).toBe("launch");
 expect(project("launch").name).toBe(name);expect(project("film")).toEqual(film);
 expect(new Set(read().projects.filter(p=>p.name===name).map(p=>p.id)).size).toBe(3);
});
test("project rename updates only its default channel, preserves messages and rejects a stale channel editor",async()=>{
 const {createChannel,changeChannel}=await import("../src/store.js");
 const customId=createChannel({tenantId:read().activeTenant,projectId:"launch",name:"separate_channel",topic:"Independent"});
 const before=structuredClone(read()),old=before.channels.find(c=>c.id==="launch");
 saveProjectMetadata(input("launch",{name:"Renamed project channel"}));
 const current=read().channels.find(c=>c.id==="launch");
 expect(current.id).toBe(old.id);expect(current.projectId).toBe("launch");expect(current.name).toBe("Renamed project channel");expect(current.revision).toBe(old.revision+1);
 expect(current.history.at(-1).name).toBe(old.name);expect(current.history.at(-1).action).toBe("项目改名同步默认频道");
 expect(read().messages).toEqual(before.messages);
 expect(read().channels.find(c=>c.id===customId)).toEqual(before.channels.find(c=>c.id===customId));
 atomic(()=>changeChannel({tenantId:read().activeTenant,channelId:old.id,revision:old.revision,action:"edit",name:old.name,topic:"Stale overwrite"}));
});
