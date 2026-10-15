import { beforeEach, test, expect } from "bun:test";
const values=new Map();Object.defineProperty(globalThis,"localStorage",{configurable:true,writable:true,value:{getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)}});
const {read,reset,update,saveResource,resourceCommand,knowledgeLibraryCommand:command,createTenant,switchTenant}=await import("../src/store.js");
const {libraryRows,libraryView,knowledgeReadProblem,visibleKnowledge}=await import("../src/knowledge.js");
beforeEach(reset);
const row=id=>read().resources.knowledge.find(item=>item.id===id);
const act=(id,action,extra={})=>command({tenantId:read().activeTenant,id,revision:row(id)?.revision,action,reason:"Human reviewed local operation",...(action==="replace"?{file:{name:"replacement.txt",size:10,type:"text/plain"}}:{}),...extra});
const create=(extra={})=>command({tenantId:read().activeTenant,action:"create",reason:"Register local source",title:"Original material",content:"private original text",category:"MATERIAL",origin:"upload",file:{name:"source.txt",size:21,type:"text/plain"},scope:"TENANT",readers:["H-lin"],...extra});
const atomic=fn=>{const before=structuredClone(read());expect(fn).toThrow();expect(read()).toEqual(before);};
test("one canonical source retains fixed original versions; references never copy content or move after replacement",()=>{
 const id=create();act(id,"index");act(id,"index-success");act(id,"reference",{projectId:"launch",version:1});const ref=structuredClone(row(id).library.references[0]);
 expect(Object.hasOwn(ref,"content")).toBe(false);act(id,"replace",{title:"New material",content:"second text"});
 expect(row(id).library.versions[0].content).toBe("private original text");expect(row(id).library.references[0]).toEqual(ref);
 const view=libraryRows(read(),{scope:"PROJECT",projectId:"launch",category:"MATERIAL"})[0];expect(view.content).toBe("private original text");expect(view.version).toBe(1);expect(view.issues.join()).toContain("新版本");
 expect(read().resources.knowledge.filter(item=>item.id===id)).toHaveLength(1);
});
test("scope uses project identity, not labels; legacy unknown remains unbound and binding cannot expose private source",()=>{
 const id=create({scope:"PROJECT",projectId:"launch"});update(s=>{s.projects.find(p=>p.id==="launch").name="Renamed"});expect(libraryRows(read(),{scope:"PROJECT",projectId:"launch",category:"MATERIAL"})[0].sourceId).toBe(id);
 atomic(()=>create({scope:"PROJECT",projectId:"Renamed"}));
 const before=structuredClone(row("K-03"));atomic(()=>act("K-03","bind",{scope:"TENANT",category:"KNOWLEDGE",readers:["H-lin"]}));expect(row("K-03")).toEqual(before);
 expect(read().resources.knowledge.filter(item=>!item.library).length).toBeGreaterThan(0);
});
test("private and cross-tenant originals fail closed; unfamiliar roles cannot mutate",()=>{
 const id=create({scope:"PRIVATE",category:"KNOWLEDGE",origin:"manual"});
 for(const role of ["external","agent","unknown"]){update(s=>{s.role=role});expect(libraryView(read(),id)).toBeNull();atomic(()=>act(id,"revise",{title:"bad",content:"bad"}));}
 reset();const original=create();const tenant=read().activeTenant;createTenant("Another");atomic(()=>command({tenantId:tenant,id:original,revision:1,action:"archive",reason:"wrong tenant"}));switchTenant(tenant);expect(row(original).library.archivedAt).toBeUndefined();
});
test("publication projects only approved fixed redacted version, never private source or its history",()=>{
 const id=create({scope:"PROJECT",projectId:"film",readers:["H-lin","H-zhou"]});
 act(id,"propose",{title:"Public guidance",content:"redacted safe content",readers:["H-lin"],applicability:"Approved team use",redaction:"Removed confidential source",license:"Human owned rights",sourceNote:"Original version 1"});
 const pubId=row(id).library.publications[0].id;expect(libraryView(read(),id,{publicationId:pubId})).toBeNull();
 act(id,"approve",{publicationId:pubId});
 update(s=>{const r=s.resources.knowledge.find(item=>item.id===id);r.library.readers=["H-zhou"];r.ownerId="H-zhou";r.owner="周宁";r.history=[{id,scope:"私人",ownerId:"H-zhou",owner:"周宁",content:"sensitive history"}];s.role="executor"});
 expect(knowledgeReadProblem(read(),row(id))).not.toBe("");expect(libraryView(read(),id)).toBeNull();const view=libraryView(read(),id,{publicationId:pubId});
 expect(view.content).toBe("redacted safe content");expect(JSON.stringify(view)).not.toContain("sensitive history");expect(JSON.stringify(view)).not.toContain("private original text");expect(view.projectId).toBeNull();expect(view).not.toHaveProperty("history");expect(view).not.toHaveProperty("owner");
 update(s=>{s.projects.find(p=>p.id==="film").archivedAt="2026-10-14T00:00:00Z"});expect(libraryView(read(),id,{publicationId:pubId}).content).toBe("redacted safe content");
});
test("stale source blocks publication approval; rejected proposals cannot silently publish",()=>{
 const id=create({scope:"PRIVATE"});const fields={title:"Safe",content:"safe",readers:["H-lin"],applicability:"Team",redaction:"Reviewed",license:"Owned",sourceNote:"Fixed source"};
 act(id,"propose",fields);const pubId=row(id).library.publications[0].id;act(id,"replace",{title:"New",content:"new"});atomic(()=>act(id,"approve",{publicationId:pubId}));act(id,"reject",{publicationId:pubId});atomic(()=>act(id,"approve",{publicationId:pubId}));
});
test("upload archive is terminal, disappears from normal search, and retains prior fixed references",()=>{
 const id=create();act(id,"reference",{projectId:"launch",version:1});const refId=row(id).library.references[0].id;act(id,"archive");
 expect(libraryRows(read(),{scope:"TENANT",category:"MATERIAL"}).some(v=>v.sourceId===id)).toBe(false);expect(visibleKnowledge(read()).some(v=>v.id===id)).toBe(false);
 expect(libraryView(read(),id,{referenceId:refId}).content).toBe("private original text");expect(libraryView(read(),id,{referenceId:refId}).usable).toBe(false);
 for(const action of ["index","replace","approve"])atomic(()=>act(id,action,{title:"No",content:"No"}));
 const manual=create({origin:"manual",category:"KNOWLEDGE"});atomic(()=>act(manual,"archive"));atomic(()=>act(manual,"replace",{title:"No",content:"No"}));
});
test("index failure and retry retain versions and old successful index; generic writes cannot inject authority",()=>{
 const id=create();act(id,"index");act(id,"index-success");act(id,"replace",{title:"new",content:"new text"});act(id,"index");act(id,"index-failure");expect(row(id).library.indexedVersion).toBe(1);expect(row(id).library.indexState).toBe("FAILED");act(id,"index");act(id,"index-success");expect(row(id).library.indexedVersion).toBe(2);
 atomic(()=>saveResource("knowledge",{...row(id),library:{}}));atomic(()=>saveResource("knowledge",{...row("K-01"),library:{scope:"TENANT"}}));atomic(()=>resourceCommand("knowledge",id,"有效","bypass"));
});
test("archived project sources and references are historical read-only, without moving source scope",()=>{
 const id=create({scope:"PROJECT",projectId:"launch"});const shared=create();act(shared,"reference",{projectId:"launch",version:1});
 update(s=>{s.projects.find(p=>p.id==="launch").archivedAt="2026-10-14T00:00:00Z"});
 expect(libraryRows(read(),{scope:"PROJECT",projectId:"launch",category:"MATERIAL"})).toHaveLength(0);expect(libraryRows(read(),{scope:"PROJECT",projectId:"launch",category:"MATERIAL",historical:true})).toHaveLength(2);
 atomic(()=>act(id,"index"));atomic(()=>act(shared,"reference",{projectId:"launch",version:1}));expect(row(id).library.scope).toBe("PROJECT");expect(row(id).library.projectId).toBe("launch");
});
test("explicit publication/reference revocation removes usable body and new adoption",()=>{
 const id=create({scope:"PRIVATE"});act(id,"propose",{title:"Shared",content:"Allowed",readers:["H-lin"],applicability:"Project use",redaction:"Reviewed",license:"Allowed",sourceNote:"v1"});const pubId=row(id).library.publications[0].id;act(id,"approve",{publicationId:pubId});act(id,"reference",{projectId:"launch",publicationId:pubId,version:2});const refId=row(id).library.references[0].id;
 act(id,"revoke-publication",{publicationId:pubId});expect(libraryView(read(),id,{referenceId:refId}).content).toBeNull();atomic(()=>act(id,"reference",{projectId:"film",publicationId:pubId,version:2}));
});

test("legacy scope binding cannot silently promote project/private records or change their history",()=>{
 const legacy=read().resources.knowledge.find(item=>item.scope!=="私人"&&item.scope!=="租户"&&item.kind!=="记忆");
 if(legacy)atomic(()=>act(legacy.id,"bind",{scope:"TENANT",category:"KNOWLEDGE",readers:["H-lin"]}));
 const id=create({scope:"PROJECT",projectId:"launch"});const revision=row(id).revision;act(id,"index");atomic(()=>command({tenantId:read().activeTenant,id,revision,action:"archive",reason:"stale modal"}));
});
test("Agent historical use requires current explicit source grant, exact version and purpose; revocation denies new use without erasing history",()=>{
 const id=create({scope:"PROJECT",projectId:"launch"});act(id,"replace",{title:"v2",content:"new content"});
 const probe={agentId:"atlas",version:1,purpose:"Review historical requirement",allowHistorical:true};
 act(id,"check-use",probe);expect(row(id).library.useChecks.at(-1).outcome).toBe("DENIED");
 act(id,"grant-agents",{agentReaders:["atlas"]});act(id,"check-use",{...probe,allowHistorical:false});expect(row(id).library.useChecks.at(-1).outcome).toBe("DENIED");
 act(id,"check-use",probe);const accepted=structuredClone(row(id).library.useChecks.at(-1));expect(accepted).toMatchObject({outcome:"AUTHORIZED",sourceId:id,version:1,purpose:probe.purpose,agentId:"atlas",actorId:"H-lin",mode:"LOCAL_SIMULATION"});
 act(id,"grant-agents",{agentReaders:[]});act(id,"check-use",probe);expect(row(id).library.useChecks.at(-1).outcome).toBe("DENIED");expect(row(id).library.useChecks).toContainEqual(accepted);
 atomic(()=>act(id,"check-use",{...probe,version:999}));atomic(()=>act(id,"check-use",{...probe,purpose:""}));
 act(id,"grant-agents",{agentReaders:["atlas"]});update(s=>{s.projects.find(p=>p.id==="launch").archivedAt="2026-10-14T00:00:00Z"});act(id,"check-use",probe);expect(row(id).library.useChecks.at(-1).outcome).toBe("AUTHORIZED");
 update(s=>{s.agents.find(a=>a.id==="atlas").status="退役"});act(id,"check-use",probe);expect(row(id).library.useChecks.at(-1).outcome).toBe("DENIED");
});

test("publication Agent permission is separate from root and channel grants; unauthorized raw data never enters projection",()=>{
 const id=create({scope:"PRIVATE",agentReaders:["atlas"]});act(id,"propose",{title:"Safe title",content:"safe",readers:["H-lin"],agentReaders:[],applicability:"Allowed",redaction:"Reviewed",license:"Owned",sourceNote:"v1"});const pubId=row(id).library.publications[0].id;act(id,"approve",{publicationId:pubId});
 const input={publicationId:pubId,version:2,agentId:"atlas",purpose:"Use shared guidance"};act(id,"check-use",input);expect(row(id).library.useChecks.at(-1).outcome).toBe("DENIED");act(id,"grant-agents",{publicationId:pubId,agentReaders:["atlas"]});act(id,"check-use",input);expect(row(id).library.useChecks.at(-1).outcome).toBe("AUTHORIZED");
 expect(JSON.stringify(libraryView(read(),id,{publicationId:pubId}))).not.toContain("private original text");
 act(id,"replace",{title:"Updated private source",content:"unpublished secret"});const fixed=libraryView(read(),id,{publicationId:pubId});expect(fixed.content).toBe("safe");expect(fixed.issues.join()).toContain("已有更新");expect(JSON.stringify(fixed)).not.toContain("unpublished secret");
});
test("upload metadata is required; unsupported knowledge extraction is not reported successful",()=>{
 atomic(()=>create({file:null}));atomic(()=>create({category:"KNOWLEDGE",file:{name:"scan.pdf",size:12,type:"application/pdf"}}));
 const id=create();atomic(()=>act(id,"replace",{title:"new",content:"new",file:null}));
});

test("membership withdrawal closes original and independently published reads",()=>{
 const id=create({scope:"PROJECT",projectId:"film"});act(id,"propose",{title:"Shared",content:"Safe fixed content",readers:["H-lin"],applicability:"Tenant guidance",redaction:"Reviewed",license:"Owned",sourceNote:"Version 1"});
 const publicationId=row(id).library.publications[0].id;act(id,"approve",{publicationId});
 expect(libraryView(read(),id)).not.toBeNull();expect(libraryView(read(),id,{publicationId})).not.toBeNull();
 update(s=>{s.organization.members=s.organization.members.filter(person=>person.id!=="H-lin");});
 expect(libraryView(read(),id)).toBeNull();expect(libraryView(read(),id,{publicationId})).toBeNull();
 expect(knowledgeReadProblem(read(),row(id))).not.toBe("");
});

test("file versions keep explicit content provenance; historical metadata is never inferred available",()=>{
 const old=create();expect(libraryView(read(),old).file.textContentAvailable).toBe(false);
 const actual=create({file:{name:"server.log",size:16,type:"text/plain",textContentAvailable:true},content:"actual log bytes"});expect(libraryView(read(),actual).file.textContentAvailable).toBe(true);expect(libraryView(read(),actual).content).toBe("actual log bytes");
 act(actual,"replace",{title:"Metadata only",content:"not uploaded",file:{name:"server.log",size:16,type:"text/plain"}});expect(libraryView(read(),actual).file.textContentAvailable).toBe(false);expect(libraryView(read(),actual,{version:1}).file.textContentAvailable).toBe(true);
 atomic(()=>create({file:{name:"bad.txt",size:1,type:"text/plain",textContentAvailable:"true"}}));
});
