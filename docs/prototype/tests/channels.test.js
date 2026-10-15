import { test, expect, beforeEach } from "bun:test";
import { channelReadProblem, channelWriteProblem, channelManageProblem, visibleChannels, visibleChannelMessages, channelMembers, channelCandidates, previewChannelHook, hydrateChannels } from "../src/channels";
import { messageProject } from "../src/message-sharing";
const values = new Map();
Object.defineProperty(globalThis,"localStorage",{configurable:true,writable:true,value:{getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)}});
const {read,reset,update,openDirectChannel,createChannel,changeChannel,changeChannelMember,publishChannelAnnouncement,changeChannelHook,postChannelMessage,changeChannelMessage,shareMessage,createTenant,switchTenant,beginWorkDraft,createProject,changeAgentChannelGrant,log} = await import("../src/store");
beforeEach(reset);
const c = id => read().channels.find(c=>c.id===id);
const args = id => ({tenantId:read().activeTenant,channelId:id,revision:c(id).revision});
const create = (extra={}) => createChannel({tenantId:read().activeTenant,projectId:"launch",name:"design_review",topic:"视觉评审",memberIds:["H-zhou"],...extra});
const atomic = fn => {const before=JSON.stringify(read());expect(fn).toThrow();expect(JSON.stringify(read())).toBe(before);};
const post = (id,text="Test",extra={})=>postChannelMessage({tenantId:read().activeTenant,channelId:id,text,...extra});
const hook = id => {changeChannelHook({...args(id),action:"save",targetUrl:"https://example.com/hook",kinds:["TEXT"],mentionsOnly:false,configuredEnabled:true});return c(id).hooks.at(-1).id;};

test("legacy default IDs, direct and shared histories hydrate idempotently without copies",()=>{
 const before=structuredClone(read().messages); const state=structuredClone(read()); hydrateChannels(state); hydrateChannels(state);
 expect(state.messages).toEqual(before);expect(state.channels.map(c=>c.id)).toEqual(read().channels.map(c=>c.id));
 for(const p of state.projects.filter(p=>p.id!=="private"))expect(state.channels.find(c=>c.id===p.id)?.projectId).toBe(p.id);
 expect(channelMembers(state,state.channels.find(c=>c.id==="launch")).some(m=>m.principalId==="H-zhou"&&m.source==="DERIVED")).toBe(true);
});
test("create edit query archive restore retains messages, history and disabled hook intention",()=>{
 const id=create(); const message=post(id);const hookId=hook(id);
 expect(messageProject(read(),message)?.id).toBe("launch");
 changeChannel({...args(id),action:"edit",name:"design_final",topic:"最终方案"});
 expect(visibleChannels(read(),{query:"最终"}).map(c=>c.id)).toEqual([id]);
 changeChannel({...args(id),action:"archive",reason:"本轮已收尾"});
 expect(visibleChannels(read()).some(c=>c.id===id)).toBe(false);expect(visibleChannels(read(),{archived:"archived"}).some(c=>c.id===id)).toBe(true);
 expect(visibleChannelMessages(read(),id).map(m=>m.id)).toContain(message.id); expect(channelWriteProblem(read(),id)).toContain("归档");
 for(const fn of [()=>post(id),()=>changeChannelMember({...args(id),action:"add",principalId:"atlas"}),()=>publishChannelAnnouncement({...args(id),body:"bad"}),()=>hook(id),()=>shareMessage({tenantId:read().activeTenant,messageId:message.id,expectedText:message.text,target:"project",projectId:"launch"})]) atomic(fn);
 changeChannel({...args(id),action:"restore",reason:"继续协作"});expect(c(id).hooks.find(h=>h.id===hookId).configuredEnabled).toBe(false);expect(c(id).history.map(h=>h.action)).toContain("归档：本轮已收尾");
 expect(post(id,"再次协作").channel).toBe(id);
});
test("names limits formats tenant and stale version failures are atomic",()=>{
 const id=create();const stale=args(id);changeChannel({...args(id),action:"edit",name:"design_review",topic:"new"});
 for(const fn of [()=>changeChannel({...stale,action:"edit",name:"overwrite",topic:"old"}),()=>create(),()=>create({name:"bad name"}),()=>create({name:"x".repeat(41)}),()=>create({projectId:"private"}),()=>create({memberIds:["foreign"]}),()=>create({memberIds:["external-demo"]}),()=>create({tenantId:"other"}),()=>create({shared:"false"}),()=>changeChannel({...args(id),action:"erase"})])atomic(fn);
 update(s=>{s.offline=true});atomic(()=>create({name:"offline"}));atomic(()=>post(id));
});
test("default names members and archive cannot be managed apart from source project",()=>{
 for(const fn of [()=>changeChannel({...args("launch"),action:"edit",name:"independent_name",topic:"x"}),()=>changeChannel({...args("launch"),action:"archive",reason:"x"}),()=>changeChannelMember({...args("launch"),action:"remove",principalId:"H-zhou"})])atomic(fn);
 update(s=>{s.organization.projectRoles=s.organization.projectRoles.filter(r=>r.memberId!=="H-zhou")});expect(channelMembers(read(),c("launch")).some(m=>m.principalId==="H-zhou")).toBe(false);
 const id=createProject({tenantId:read().activeTenant,name:"New Project",description:"Scope",ownerId:"H-lin"});expect(c(id).kind).toBe("PROJECT_DEFAULT");
});
test("default channel management follows the unique Human project owner and explicit Human project managers",()=>{
 update(s=>{s.role="executor"});
 expect(channelMembers(read(),c("launch")).find(m=>m.principalId==="H-lin").role).toBe("OWNER");
 expect(channelManageProblem(read(),c("launch"))).toBe("");
 changeChannel({...args("launch"),action:"edit",name:c("launch").name,topic:"项目负责人的公告主题"});
 expect(c("launch").topic).toBe("项目负责人的公告主题");
 update(s=>{s.projects.find(p=>p.id==="launch").ownerId="H-zhou";s.organization.projectRoles=s.organization.projectRoles.filter(r=>r.memberId!=="H-lin");});
 expect(channelManageProblem(read(),c("launch"))).not.toBe("");
 update(s=>s.organization.projectRoles.push({id:"explicit-manager",projectId:"launch",memberId:"H-lin",role:"项目负责人"}));
 expect(channelManageProblem(read(),c("launch"))).toBe("");
 expect(channelMembers(read(),c("launch")).find(m=>m.principalId==="H-lin").role).toBe("OWNER");
 update(s=>{delete s.projects.find(p=>p.id==="launch").ownerId;s.organization.projectRoles=[];s.organization.members.push({id:"H-duplicate",name:"林予",kind:"Human"});});
 expect(channelReadProblem(read(),"launch")).not.toBe("");
});
test("last owner cannot leave; removed actor immediately loses read send edit share and management",()=>{
 const id=create();const message=post(id);
 atomic(()=>changeChannelMember({...args(id),action:"remove",principalId:"H-lin"}));atomic(()=>changeChannelMember({...args(id),action:"role",principalId:"H-lin",role:"MEMBER"}));
 changeChannelMember({...args(id),action:"role",principalId:"H-zhou",role:"OWNER"});
 changeChannelMember({...args(id),action:"remove",principalId:"H-lin"});
 expect(visibleChannelMessages(read(),id)).toEqual([]);expect(channelReadProblem(read(),id)).not.toBe("");
 for(const fn of [()=>post(id),()=>changeChannel({...args(id),action:"edit",name:"hidden",topic:"hidden"}),()=>changeChannelMessage({tenantId:read().activeTenant,messageId:message.id,expectedText:message.text,action:"edit",text:"bad"}),()=>shareMessage({tenantId:read().activeTenant,messageId:message.id,expectedText:message.text,target:"project",projectId:"launch"})])atomic(fn);
 expect(read().messages.find(m=>m.id===message.id).text).toBe("Test");
});
test("ordinary custom member can exit while project revocation removes channel access",()=>{
 const id=create();changeChannelMember({...args(id),action:"role",principalId:"H-zhou",role:"OWNER"});changeChannelMember({...args(id),action:"role",principalId:"H-lin",role:"MEMBER"});
 update(s=>{s.role="executor"});expect(channelReadProblem(read(),id)).toBe("");
 changeChannelMember({...args(id),action:"remove",principalId:"H-lin"});expect(channelReadProblem(read(),id)).not.toBe("");
 reset();const other=create();update(s=>{s.role="executor";s.organization.projectRoles=s.organization.projectRoles.filter(r=>r.memberId!=="H-lin");const project=s.projects.find(p=>p.id==="launch");project.owner="周宁";project.ownerId="H-zhou"});
 expect(channelReadProblem(read(),other)).toContain("撤销");atomic(()=>post(other));
});
test("announcement publish revise cancel is visible text with retained immutable prior versions",()=>{
 const id=create();publishChannelAnnouncement({...args(id),body:"Original announcement"});const original=c(id).announcement.messageId;
 publishChannelAnnouncement({...args(id),body:"Revised announcement"});const revised=c(id).announcement.messageId;
 expect(original).not.toBe(revised);expect(c(id).announcement.revision).toBe(2);expect(read().messages.find(m=>m.id===original).text).toBe("Original announcement");expect(read().messages.find(m=>m.id===original).pinned).toBe(false);
 atomic(()=>changeChannelMessage({tenantId:read().activeTenant,messageId:revised,expectedText:"Revised announcement",action:"edit",text:"bypass"}));
 publishChannelAnnouncement({...args(id),cancel:true});expect(c(id).announcement.cancelledAt).not.toBeNull();expect(read().messages.find(m=>m.id===revised).pinned).toBe(false);expect(c(id).history.some(h=>h.announcement?.messageId===original)).toBe(true);
 atomic(()=>publishChannelAnnouncement({...args(id),cancel:true}));
});
test("hooks persist configuration only; private addresses and credentials reject, disabled or revoked preview fails",()=>{
 const id=create();const hookId=hook(id);const preview=previewChannelHook(read(),{...args(id),hookId});
 expect(preview.runtime).toBe("NOT_CONNECTED");expect(preview.signature).toContain("未生成");expect(JSON.stringify(preview)).not.toContain("Test");expect(c(id).hooks[0].secret).toBeUndefined();
 for(const url of ["http://example.com","https://localhost/hook","https://127.0.0.1/hook","https://10.0.0.1/hook","https://[::1]/hook","https://internal.local/hook","https://user:secret@example.com/hook","https://example.com/hook?secret=oops"]){atomic(()=>changeChannelHook({...args(id),action:"save",targetUrl:url,kinds:["TEXT"],mentionsOnly:false,configuredEnabled:true}));}
 changeChannelHook({...args(id),action:"disable",hookId});atomic(()=>previewChannelHook(read(),{...args(id),hookId}));changeChannelHook({...args(id),action:"enable",hookId});expect(previewChannelHook(read(),{...args(id),hookId}).previewOnly).toBe(true);
 changeChannelHook({...args(id),action:"revoke",hookId});expect(c(id).hooks[0].revokedAt).not.toBeNull();atomic(()=>previewChannelHook(read(),{...args(id),hookId}));atomic(()=>changeChannelHook({...args(id),action:"enable",hookId}));expect(c(id).history.filter(h=>h.hooks.length).length).toBeGreaterThan(0);
});
test("private conversations forbid member announcement hook and settings mutations",()=>{
 for(const fn of [()=>changeChannel({...args("private"),action:"edit",name:"leak",topic:"x"}),()=>changeChannelMember({...args("private"),action:"add",principalId:"H-zhou"}),()=>publishChannelAnnouncement({...args("private"),body:"x"}),()=>hook("private")])atomic(fn);
 update(s=>{s.role="external"});expect(visibleChannelMessages(read(),"private")).toEqual([]);atomic(()=>post("private"));
});
test("external shared member does not receive prior history even at identical timestamps and removal is immediate",()=>{
 const id=create({shared:true});const old=post(id,"Internal-before-invite");
 changeChannelMember({...args(id),action:"add",principalId:"external-demo",role:"MEMBER",historyAccess:"FROM_JOIN"});
 update(s=>{const m=s.channels.find(c=>c.id===id).members.find(m=>m.principalId==="external-demo");s.messages.find(m=>m.id===old.id).time=m.joinedAt});
 const fresh=post(id,"Shared-after-invite");update(s=>{s.role="external"});
 expect(visibleChannelMessages(read(),id).map(m=>m.id)).toEqual([fresh.id]);atomic(()=>postChannelMessage({tenantId:read().activeTenant,channelId:id,text:"reply hidden",threadId:old.id}));expect(post(id,"External reply").authorId).toBe("external-demo");
 atomic(()=>create({name:"customer_forbidden"}));update(s=>{s.role="owner"});changeChannelMember({...args(id),action:"remove",principalId:"external-demo"});update(s=>{s.role="external"});expect(visibleChannelMessages(read(),id)).toEqual([]);atomic(()=>post(id));
});
test("selected sharing into custom channels preserves textual preview protection and private external exclusion",()=>{
 const id=create({shared:true,memberIds:["external-demo"]});const privateMessage=read().messages.find(m=>m.channel==="private");
 atomic(()=>shareMessage({tenantId:read().activeTenant,messageId:privateMessage.id,expectedText:privateMessage.text,target:"channel",channelId:id}));
 const source=read().messages.find(m=>m.channel==="launch");const shared=shareMessage({tenantId:read().activeTenant,messageId:source.id,expectedText:source.text,target:"channel",channelId:id});
 expect(shared.channel).toBe(id);expect(shared.file).toBeUndefined();expect(shared.history).toBeUndefined();expect(read().messages.find(m=>m.id===source.id).channel).toBe("launch");
 atomic(()=>shareMessage({tenantId:read().activeTenant,messageId:source.id,expectedText:"stale",target:"channel",channelId:id}));
});
test("custom project mapping feeds work drafts without private or arbitrary project fallback",()=>{
 const id=create();const message=post(id,"Please create a task");const draft=beginWorkDraft({sourceId:message.id,channel:id});
 expect(read().drafts[draft.id || draft]?.project).toBe("launch");
 const noSource = beginWorkDraft({channel:id});expect(read().drafts[noSource.id || noSource]?.project).toBe("launch");
});
test("tenant switching keeps all channel config, membership, announcements and history isolated",()=>{
 const id=create();hook(id);publishChannelAnnouncement({...args(id),body:"Tenant one"});const before=structuredClone(c(id));const stale=args(id);
 const other=createTenant("Another tenant");expect(read().channels.some(c=>c.id===id)).toBe(false);expect(read().channels.some(c=>c.id==="private"||c.id==="external")).toBe(false);
 atomic(()=>changeChannel({...stale,action:"edit",name:"cross",topic:"cross"}));expect(visibleChannelMessages(read(),id)).toEqual([]);
 switchTenant("studio");expect(c(id)).toEqual(before);switchTenant(other);expect(read().messages).toEqual([]);
});

const grant = (agentId="atlas",projectId="launch",permissions=["channel_read","channel_post"],extra={}) => changeAgentChannelGrant({tenantId:read().activeTenant,agentId,projectId,agentRevision:read().agents.find(a=>a.id===agentId).revision || 1,revision:read().channelProjectGrants.find(g=>g.agentId===agentId&&g.projectId===projectId)?.revision || 0,action:"grant",permissions,reason:"明确频道范围",...extra});
test("task assignment never grants channel access; explicit Agent grants drive default membership and read/post",()=>{
 expect(read().work.some(w=>w.agentId==="atlas")).toBe(true);
 expect(channelMembers(read(),c("launch")).some(m=>m.principalId==="atlas")).toBe(false);
 expect(channelCandidates(read(),"launch").some(p=>p.id==="atlas")).toBe(false);
 expect(channelReadProblem(read(),"launch","atlas")).not.toBe("");
 grant("atlas","launch",["channel_read"]);
 expect(channelMembers(read(),c("launch")).some(m=>m.principalId==="atlas"&&m.source==="DERIVED")).toBe(true);
 expect(channelReadProblem(read(),"launch","atlas")).toBe("");expect(channelWriteProblem(read(),"launch","atlas")).toContain("发言");
 expect(channelReadProblem(read(),"film","atlas")).not.toBe("");
 grant();expect(channelWriteProblem(read(),"launch","atlas")).toBe("");
 const id=create({memberIds:["atlas"]});expect(channelReadProblem(read(),id,"atlas")).toBe("");
 grant("atlas","launch",[],{action:"revoke"});expect(channelReadProblem(read(),id,"atlas")).not.toBe("");expect(channelMembers(read(),c("launch")).some(m=>m.principalId==="atlas")).toBe(false);
 expect(c(id).members.some(m=>m.principalId==="atlas")).toBe(true);expect(read().channelProjectGrants[0].history.length).toBe(3);
});
test("actual Agent retirement preserves old channel identity but rejects new grant invitation and mentions",()=>{
 grant();const id=create({memberIds:["atlas"]});const old=post(id,"<@atlas> Please review");expect(old.mentions).toEqual(["atlas"]);
 // Exact existing Agent detail retirement operation: status='退役' plus lifecycle audit.
 update(s=>{s.agents.find(a=>a.id==="atlas").status="退役";log(s,"Atlas → 退役","/agents/atlas");});
 expect(channelMembers(read(),c(id)).find(m=>m.principalId==="atlas").person.status).toBe("退役");
 expect(channelCandidates(read(),"launch").some(p=>p.id==="atlas")).toBe(false);
 for(const fn of [()=>grant(),()=>create({name:"retired",memberIds:["atlas"]}),()=>post(id,"<@atlas> again")])atomic(fn);
 expect(channelReadProblem(read(),id,"atlas")).not.toBe("");expect(read().messages.find(m=>m.id===old.id).text).toContain("<@atlas>");
 grant("atlas","launch",[],{action:"revoke"});expect(read().channelProjectGrants[0].revokedAt).not.toBeNull();
});
test("Agent project grants enforce Human owner, scope, stale version, permissions and tenant isolation atomically",()=>{
 for(const fn of [()=>grant("quill"),()=>grant("atlas","private"),()=>grant("atlas","launch",["file_read"]),()=>grant("atlas","launch",["channel_post"]),()=>grant("atlas","launch",["channel_read"],{tenantId:"other"})])atomic(fn);
 grant();const existing=structuredClone(read().channelProjectGrants[0]);atomic(()=>grant("atlas","launch",["channel_read"],{revision:0}));
 createTenant("Grant isolation");expect(read().channelProjectGrants).toEqual([]);switchTenant("studio");expect(read().channelProjectGrants[0]).toEqual(existing);
});
test("legitimate webhook queries and public IPs are preserved without allowing plaintext credentials",()=>{
 const id=create();for(const targetUrl of ["https://example.com/hook?team=review&format=json","https://8.8.8.8/hook","https://[2606:4700:4700::1111]/hook","https://example.com/hook?token=secretref:delivery-signing"]){changeChannelHook({...args(id),action:"save",targetUrl,kinds:["TEXT"],mentionsOnly:false,configuredEnabled:false});expect(c(id).hooks.at(-1).targetUrl).toBe(targetUrl);}
});
test("thread roots must belong to the currently authorized channel and explicit external history scope",()=>{
 const privateRoot=read().messages.find(m=>m.channel==="private");const internal=read().messages.find(m=>m.channel==="launch");
 update(s=>{s.role="external"});for(const root of [privateRoot,internal]){expect(visibleChannelMessages(read(),"external").some(m=>m.id===root.id)).toBe(false);atomic(()=>post("external","Reply",{threadId:root.id}));}
 update(s=>{s.role="owner"});const id=create();atomic(()=>post(id,"Reply",{threadId:internal.id}));atomic(()=>post(id,"Reply",{threadId:"unknown"}));
});


test("Human starts tenant-level private conversations with Human or Agent and reuses the fixed pair",()=>{
 const tenantId=read().activeTenant;
 for(const peerId of ["H-zhou","atlas","quill"]) {
  const id=openDirectChannel({tenantId,peerId});const first=structuredClone(c(id));
  expect(first.kind).toBe("DIRECT");expect(first.projectId).toBeNull();
  expect(first.members.map(m=>m.principalId).sort()).toEqual(["H-lin",peerId].sort());
  expect(openDirectChannel({tenantId,peerId})).toBe(id);expect(c(id)).toEqual(first);
  expect(visibleChannels(read()).filter(row=>row.id===id)).toHaveLength(1);
  expect(read().messages.filter(m=>m.channel===id)).toHaveLength(peerId==="quill"?1:0);
 }
 expect(openDirectChannel({tenantId,peerId:"quill"})).toBe("private");
 expect(read().executions.every(e=>!e.id.startsWith("dm-"))).toBe(true);
});
test("private creation rejects stale tenant self external unknown and inactive targets atomically",()=>{
 const tenantId=read().activeTenant;
 for(const input of [{tenantId:"other",peerId:"H-zhou"},{tenantId,peerId:"H-lin"},{tenantId,peerId:"external-demo"},{tenantId,peerId:"foreign"}]) atomic(()=>openDirectChannel(input));
 update(s=>{s.agents.find(a=>a.id==="atlas").status="退役中";s.organization.members.find(p=>p.id==="H-zhou").status="停用"});
 for(const peerId of ["H-zhou","atlas"]) atomic(()=>openDirectChannel({tenantId,peerId}));
 update(s=>{s.role="external"});atomic(()=>openDirectChannel({tenantId,peerId:"quill"}));
 update(s=>{s.role="owner";s.offline=true});atomic(()=>openDirectChannel({tenantId,peerId:"quill"}));
});
test("dynamic private conversations preserve ownership sharing and draft boundaries",()=>{
 const tenantId=read().activeTenant,id=openDirectChannel({tenantId,peerId:"H-zhou"}),message=post(id,"Private plan");
 update(s=>{s.project="film"});
 const draft=beginWorkDraft({channel:id,sourceId:message.id});expect(read().drafts[draft].project).toBe("private");
 for(const target of [{target:"external"},{target:"channel",channelId:"external"}]) atomic(()=>shareMessage({tenantId,messageId:message.id,expectedText:message.text,...target}));
 const copied=shareMessage({tenantId,messageId:message.id,expectedText:message.text,target:"project",projectId:"launch"});
 expect(read().messages.filter(m=>m.channel===id)).toHaveLength(1);expect(copied.channel).toBe("launch");
 for(const action of ["archive","edit"]) atomic(()=>changeChannel({...args(id),action,name:"other",topic:"new",reason:"no"}));
 atomic(()=>changeChannelMember({...args(id),action:"add",principalId:"H-xu"}));
 const other=structuredClone(read());other.channels.push({id:"dm-others",kind:"DIRECT",projectId:null,members:[{principalId:"H-zhou"},{principalId:"H-xu"}]});
 expect(channelReadProblem(other,"dm-others")).toContain("仅参与者");
 expect(visibleChannels(other).some(row=>row.id==="dm-others")).toBe(false);
});
test("departed or retired private peers leave readable history but block all further writes",()=>{
 for(const peerId of ["H-zhou","atlas"]) {
  reset();const tenantId=read().activeTenant,id=openDirectChannel({tenantId,peerId}),message=post(id,"Retained history");
  update(s=>{if(peerId==="atlas")s.agents=s.agents.filter(a=>a.id!==peerId);else s.organization.members=s.organization.members.filter(p=>p.id!==peerId)});
  expect(channelReadProblem(read(),id)).toBe("");expect(visibleChannelMessages(read(),id).map(m=>m.id)).toContain(message.id);
  expect(channelWriteProblem(read(),id)).toContain("历史只读");atomic(()=>post(id,"must fail"));
  atomic(()=>openDirectChannel({tenantId,peerId}));
 }
});
