import {test,expect,beforeEach} from "bun:test";
const storage=new Map();Object.defineProperty(globalThis,"localStorage",{configurable:true,value:{getItem:key=>storage.get(key)||null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)}});
const api=await import("../src/store.js");beforeEach(()=>api.reset());
const project=id=>api.read().projects.find(p=>p.id===id);
const create=()=>api.createProject({tenantId:api.read().activeTenant,name:"Project archive fixture",description:"Lifecycle acceptance",ownerId:"H-lin"});
const args=id=>({tenantId:api.read().activeTenant,projectId:id,revision:project(id).revision??1,reason:"Human 明确归档并保留责任与历史",impactFingerprint:JSON.stringify(api.projectArchiveImpact(api.read(),id).items)});
const archive=id=>api.changeProjectArchive({...args(id),action:"archive"});
const atomic=fn=>{const before=structuredClone(api.read()),saved=[...storage];expect(fn).toThrow();expect(api.read()).toEqual(before);expect([...storage]).toEqual(saved);};
const task=id=>{const draft=api.beginWorkDraft({projectId:id});const d=api.read().drafts[draft];api.saveWorkDraft(draft,{title:"Unfinished explicit work",criteria:"Real result",project:id,type:"软件"},d.revision);return api.confirmWorkDraft(draft,api.read().drafts[draft].revision);};
test("archive is separate from business stage and restore keeps identity/history without restarting",()=>{
 const id=create(),original=structuredClone(project(id)),start=Date.now();api.update(s=>s.project=id);archive(id);
 expect(project(id).id).toBe(id);expect(project(id).name).toBe(original.name);expect(project(id).progress).toBe(original.progress);expect(project(id).status).toBe(original.status);expect(Date.parse(project(id).archivedAt)).toBeGreaterThanOrEqual(start);expect(api.read().project).toBe("all");
 expect(project(id).history.at(-1)).toMatchObject({kind:"ARCHIVE",actorId:"H-lin",source:"LOCAL_SIMULATION"});const before=structuredClone(api.read());api.changeProjectArchive({...args(id),action:"restore"});
 expect(project(id).archivedAt).toBeNull();expect(project(id).history.at(-1).kind).toBe("RESTORE");expect(project(id).revision).toBe(3);expect(api.read().work).toEqual(before.work);expect(api.read().resources.schedules).toEqual(before.resources.schedules);
});
test("unresolved work blocks until explicit pause and restoration never resumes it",()=>{
 const id=create(),work=task(id),before=structuredClone(api.read().work.find(w=>w.id===work.id));atomic(()=>archive(id));
 api.pauseProjectWork({tenantId:api.read().activeTenant,workId:work.id,version:work.version,reason:"明确暂停推进，责任仍保留"});const paused=api.read().work.find(w=>w.id===work.id);
 expect(paused.status).toBe("等待");expect(paused.history).toEqual(before.history);expect(paused.accepted).toBe(before.accepted);expect(paused.archivePause.source).toBe("LOCAL_SIMULATION");archive(id);
 atomic(()=>api.workCommand(work.id,"plan","stale",work.version));atomic(()=>api.scheduleWork(work.id,"2026-10-15","2026-10-16","stale",0));atomic(()=>api.beginWorkDraft({projectId:id}));
 api.changeProjectArchive({...args(id),action:"restore"});expect(api.read().work.find(w=>w.id===work.id).status).toBe("等待");
});
test("pending approvals handoffs deliveries and QA batches remain blockers and are never silently resolved",()=>{
 const id=create();api.update(s=>{s.resources.approvals.push({id:"pending",projectId:id,name:"Pending decision",status:"待审核"});s.resources.deliveries.push({id:"outbound",projectId:id,name:"Pending receipt",status:"待回执"});s.resources.testplans.push({id:"qa",name:"Active QA",batches:[{id:"batch",status:"进行中",workContexts:[{workId:"w"}]}]});s.work.push({id:"w",project:id,title:"Accepted source",status:"已完成",handoffs:[{id:"handoff",status:"待接收"}]});});
 const kinds=api.projectArchiveImpact(api.read(),id).blockers.map(item=>item.kind);for(const kind of ["approval","delivery","test-run","handoff"])expect(kinds).toContain(kind);atomic(()=>archive(id));
});
test("explicit leases require fixed local release; offline failure and unconfirmed state block archive",()=>{
 const id=create(),leaseId=api.requestProjectLease({tenantId:api.read().activeTenant,projectId:id,nodeId:"n1",kind:"terminal"});atomic(()=>archive(id));let lease=api.read().projectLeases.find(item=>item.id===leaseId);
 api.update(s=>s.offline=true);atomic(()=>api.releaseProjectLease({tenantId:api.read().activeTenant,leaseId,revision:lease.revision,reason:"release"}));api.update(s=>{s.offline=false;s.nodes.find(n=>n.id==="n1").status="离线";});
 expect(api.releaseProjectLease({tenantId:api.read().activeTenant,leaseId,revision:lease.revision,reason:"explicit retry"})).toBe("FAILED");atomic(()=>archive(id));
 api.update(s=>s.nodes.find(n=>n.id==="n1").status="在线");lease=api.read().projectLeases.find(item=>item.id===leaseId);expect(api.releaseProjectLease({tenantId:api.read().activeTenant,leaseId,revision:lease.revision,reason:"verified local release"})).toBe("RELEASED");
 const fixed=structuredClone(api.read().projectLeases);archive(id);atomic(()=>api.requestProjectLease({tenantId:api.read().activeTenant,projectId:id,nodeId:"n1",kind:"terminal"}));api.changeProjectArchive({...args(id),action:"restore"});expect(api.read().projectLeases).toEqual(fixed);
});
test("archive uses live impacts and exact tenant/revision, with independent Human project permissions",()=>{
 const id=create(),old=args(id);api.saveProjectMetadata({...old,name:"Edited",description:"Updated",ownerId:"H-lin",deadline:""});atomic(()=>api.changeProjectArchive({...old,action:"archive"}));
 for(const patch of [{tenantId:"other"},{reason:""},{impactFingerprint:"[]bad"},{action:"delete"}])atomic(()=>api.changeProjectArchive({...args(id),action:"archive",...patch}));
 api.update(s=>s.offline=true);atomic(()=>archive(id));api.update(s=>{s.offline=false;s.role="external";});atomic(()=>archive(id));api.update(s=>{s.role="executor";s.projects.find(p=>p.id===id).ownerId="H-xu";});atomic(()=>archive(id));
 api.update(s=>s.organization.projectRoles.push({projectId:id,memberId:"H-lin",role:"项目负责人"}));archive(id);
});
test("archived canonical resource guard rejects relocation and ordinary write paths but unrelated scopes remain editable",()=>{
 const id=create();api.update(s=>{s.resources.knowledge.push({id:"owned",tenantId:s.activeTenant,name:"Scoped document",owner:"林予",ownerId:"H-lin",kind:"知识",scope:"租户",projectId:id,status:"有效",revision:1,history:[]});s.resources.schedules.push({id:"paused",projectId:id,name:"Paused schedule",status:"暂停"});});archive(id);
 const knowledge=api.read().resources.knowledge.find(k=>k.id==="owned");atomic(()=>api.saveResource("knowledge",{...knowledge,projectId:"launch",content:"stale overwrite"}));atomic(()=>api.resourceCommand("schedules","paused","启用","restart"));atomic(()=>api.saveProjectMetadata({...args(id),name:"changed",ownerId:"H-lin",description:"",deadline:""}));atomic(()=>api.generatePeriodicReport({subjectKey:`project:${id}`}));
 expect(api.visibleKnowledge(api.read()).some(k=>k.id==="owned")).toBe(false);expect(api.visibleKnowledge(api.read(),{includeArchived:true}).some(k=>k.id==="owned")).toBe(true);expect(api.projectIsArchived(api.read(),"launch")).toBe(false);
});
test("explicit historical work lookup is preserved while defaults exclude archived project and unknown scopes are not inferred",()=>{
 const id=create(),work=task(id);api.pauseProjectWork({tenantId:api.read().activeTenant,workId:work.id,version:work.version,reason:"Pause"});archive(id);
 expect(api.visibleWork({...api.read(),project:"all"}).some(w=>w.id===work.id)).toBe(false);expect(api.visibleWork({...api.read(),project:"all"},{includeArchived:true}).some(w=>w.id===work.id)).toBe(true);
 expect(api.activeProjects(api.read()).some(p=>p.id===id)).toBe(false);expect(api.recordProjectId(api.read(),{scope:`项目 · ${project(id).name}`})).toBeNull();expect(api.projectArchiveImpact(api.read(),id).disclosure).toContain("未绑定");
});

test("active upstream rework crosses archived work without changing its fixed original or silently validating restore",()=>{
 const archivedProject=create();
 api.update(s=>{for(const [id,projectId,inputs] of [["source","launch",[]],["frozen",archivedProject,[{workId:"source",version:1}]],["descendant","launch",[{workId:"frozen",version:1}]]])s.work.push({id,title:id,project:projectId,version:1,status:id==="source"?"待验收":"已完成",owner:"林予",agent:"Atlas",agentId:"atlas",type:"软件",qa:true,accepted:id!=="source",delivered:false,plan:true,flow:true,flowVersion:3,requirements:["固定要求"],notes:"原成果",history:[{version:1,notes:"原成果",accepted:true}],inputs,completionContract:{mode:"internal",recipient:"林予",reason:""}});});
 archive(archivedProject);const get=id=>api.read().work.find(w=>w.id===id),fixed=JSON.stringify(get("frozen"));
 api.workCommand("source","return","Human 明确指出来源成果错误",1);
 expect(JSON.stringify(get("frozen"))).toBe(fixed);expect(api.dependencyProblems(api.read(),get("frozen"))).toHaveLength(1);
 expect(get("descendant")).toMatchObject({status:"等待",qa:false,accepted:false});expect(api.dependencyProblems(api.read(),get("descendant"))).toHaveLength(1);
 atomic(()=>api.workCommand("descendant","start"));
 api.changeProjectArchive({...args(archivedProject),action:"restore"});expect(JSON.stringify(get("frozen"))).toBe(fixed);expect(api.dependencyProblems(api.read(),get("frozen"))).toHaveLength(1);atomic(()=>api.workCommand("frozen","accept"));
});
test("explicit knowledge library project ownership is recognized without tenant or private projection inference",()=>{
 const id=create();archive(id);
 expect(api.projectRecordWriteProblem(api.read(),{library:{scope:"PROJECT",projectId:id}})).toContain("已归档");
 for(const scope of ["TENANT","PRIVATE"])expect(api.projectRecordWriteProblem(api.read(),{library:{scope,projectId:id}})).toBe("");
});

test("own known Human local lease can be released without project management; stale and other owners remain atomic",()=>{
 const id=create();api.update(s=>{s.projects.find(p=>p.id===id).ownerId="H-zhou";s.role="executor";});
 const leaseId=api.requestProjectLease({tenantId:api.read().activeTenant,projectId:id,nodeId:"n1",kind:"preview"});
 atomic(()=>api.releaseProjectLease({tenantId:api.read().activeTenant,leaseId,revision:9,reason:"stale"}));
 api.update(s=>s.projectLeases.find(l=>l.id===leaseId).actorId="H-zhou");atomic(()=>api.releaseProjectLease({tenantId:api.read().activeTenant,leaseId,revision:1,reason:"other owner"}));
 api.update(s=>s.projectLeases.find(l=>l.id===leaseId).actorId="H-lin");expect(api.releaseProjectLease({tenantId:api.read().activeTenant,leaseId,revision:1,reason:"Own local release"})).toBe("RELEASED");
 atomic(()=>archive(id));
});

test("cross-project pending handoff blocks receiver archive and explicit cancellation retains fixed materials",()=>{
 const id=create(),receiver=task(id),sourceId="W-104";
 api.update(s=>{s.work.find(w=>w.id===sourceId).handoffs=[{id:"cross-pack",version:1,targetWorkId:receiver.id,recipientId:"H-lin",status:"待接收",items:[{id:"frozen",name:"Fixed material",reference:"original:v1"}],history:[]}];});
 api.pauseProjectWork({tenantId:api.read().activeTenant,workId:receiver.id,version:receiver.version,reason:"Pause receiver"});
 expect(api.projectArchiveImpact(api.read(),id).blockers.some(item=>item.id==="cross-pack")).toBe(true);atomic(()=>archive(id));
 api.workCommand(receiver.id,"cancel","Receiver no longer needed",receiver.version);
 const fixed=structuredClone(api.read().work.find(w=>w.id===sourceId).handoffs[0].items),input={tenantId:api.read().activeTenant,sourceId,handoffId:"cross-pack",version:1,fingerprint:JSON.stringify(fixed),reason:"Explicitly terminate pending package"};
 atomic(()=>api.cancelInputHandoff({...input,fingerprint:"stale"}));api.cancelInputHandoff(input);
 const pack=api.read().work.find(w=>w.id===sourceId).handoffs[0];expect(pack.status).toBe("已取消");expect(pack.items).toEqual(fixed);expect(pack.history.at(-1)).toMatchObject({actorId:"H-lin",source:"LOCAL_SIMULATION",previousStatus:"待接收"});archive(id);atomic(()=>api.cancelInputHandoff(input));
});

test("archive guards canonical project and execution messages, including edits through stale UI selections",()=>{
 const id=create(),work=task(id),executionId=`EX-archive-${work.id}`;
 api.update(s=>{s.executions.push({id:executionId,tenantId:s.activeTenant,projectId:id,workId:work.id,state:"RUNNING",revision:1,initiatorId:"H-lin",capabilities:{interactiveInput:true},turns:[],events:[]});});
 api.submitExecutionInput({tenantId:api.read().activeTenant,executionId,revision:1,text:"Preserved execution"});
 const message=api.read().messages.at(-1);api.pauseProjectWork({tenantId:api.read().activeTenant,workId:work.id,version:work.version,reason:"Pause"});archive(id);
 atomic(()=>api.submitExecutionInput({tenantId:api.read().activeTenant,executionId,revision:2,text:"blocked"}));
 for(const channelId of [id,`exec-${executionId}`])atomic(()=>api.postChannelMessage({tenantId:api.read().activeTenant,channelId,text:"blocked"}));
 for(const action of ["edit","recall","react","pin"])atomic(()=>api.changeChannelMessage({tenantId:api.read().activeTenant,messageId:message.id,expectedText:message.text,action,text:"blocked"}));
 expect(api.visibleChannelMessages(api.read(),`exec-${executionId}`).some(item=>item.id===message.id)).toBe(true);
});

test("preview leases use execution identity and reject another execution's manifest or node",()=>{
 const first=api.read().executions.find(e=>e.id==='EX-demo-W-104');
 api.update(s=>{const copy=structuredClone(first);copy.id='EX-preview-second';copy.state='SUCCEEDED';s.executions.push(copy);});
 const args=executionId=>({tenantId:api.read().activeTenant,projectId:'launch',workId:'W-104',executionId,resourceId:'primary',kind:'preview'});
 const one=api.requestProjectLease(args(first.id)),two=api.requestProjectLease(args('EX-preview-second'));
 expect(one).not.toBe(two);expect(api.read().projectLeases.find(l=>l.id===one).executionId).toBe(first.id);expect(api.requestProjectLease(args(first.id))).toBe(one);
 for(const patch of [{executionId:'W-104'},{executionId:'missing'},{projectId:'film'},{workId:'W-103'},{resourceId:'unregistered'},{nodeId:'n2'}])atomic(()=>api.requestProjectLease({...args(first.id),...patch}));
});
