import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) {
  const data = new Map();
  globalThis.localStorage = { getItem: key => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) };
}
const { read, reset, update, beginWorkDraft, saveWorkDraft, confirmWorkDraft, switchTenant } = await import("../src/store.js?work-draft-tests");
beforeEach(reset);

test("conversation draft remains outside formal work until Human confirms goal and criteria", () => {
  const originalCount = read().work.length;
  const draftId = beginWorkDraft({ projectId: "launch", channel: "launch" });
  expect(read().work).toHaveLength(originalCount);
  expect(read().drafts[draftId]).toMatchObject({ project: "launch", channel: "launch", status: "讨论中", tenantId: read().activeTenant });
  expect(() => confirmWorkDraft(draftId, 0)).toThrow("补充");
  saveWorkDraft(draftId, { title: "目标", criteria: "可验证条款", type: "文档", project: "launch" }, 0);
  expect(read().work).toHaveLength(originalCount);
  update(s => { s.drafts[draftId].discussion.push({ messageId: "discussion", text: "确认范围" }); });
  const work = confirmWorkDraft(draftId, 1);
  expect(work).toMatchObject({ title: "目标", plan: true, flow: false, status: "待开始", project: "launch", draftId, requirements: ["可验证条款"], agent: "未分派", agentId: null });
  expect(work.planSnapshot.discussion[0].text).toBe("确认范围");
  expect(confirmWorkDraft(draftId, 1).id).toBe(work.id);
  expect(read().work).toHaveLength(originalCount + 1);
  expect(() => saveWorkDraft(draftId, { title: "覆盖" }, 1)).toThrow("已生成");
});

test("message conversion preserves original source, thread and explicit project independent of global scope", () => {
  update(s => { s.project = "novel"; s.messages.push({id:"root",channel:"launch",text:"Original thread"},{ id: "source", channel: "launch", thread: "root", text: "原目标" }); });
  const draftId = beginWorkDraft({ sourceId: "source", projectId: "launch", channel: "launch", threadId: "root" });
  expect(beginWorkDraft({ sourceId: "source", projectId: "launch" })).toBe(draftId);
  const draft = read().drafts[draftId];
  expect(draft).toMatchObject({ project: "launch", channel: "launch", threadId: "root", title: "原目标" });
  update(s => { s.messages.find(m => m.id === "source").text = "后来编辑"; });
  saveWorkDraft(draftId, { title: "确认目标", criteria: "验收条款", type: "软件", project: "launch" }, 0);
  const work = confirmWorkDraft(draftId, 1);
  expect(work.source).toBe("source");
  expect(work.sourceContext).toMatchObject({ text: "原目标", messageId: "source", channel: "launch", threadId: "root", project: "launch", tenantId: "studio" });
});

test("work drafts reject stale edits, unavailable context, offline and unauthorized confirmation", () => {
  expect(() => beginWorkDraft({ sourceId: "absent" })).toThrow("来源消息");
  expect(() => beginWorkDraft({ executionId: "absent" })).toThrow("来源执行");
  expect(() => beginWorkDraft({ projectId: "absent" })).toThrow("归属项目");
  const draftId = beginWorkDraft({ executionId: "EX-demo-W-104" });
  const fields = { title: "目标", criteria: "条款", type: "软件", project: "launch" };
  saveWorkDraft(draftId, fields, 0);
  expect(() => saveWorkDraft(draftId, fields, 0)).toThrow("已变化");
  expect(() => confirmWorkDraft(draftId, 0)).toThrow("已变化");
  update(s => { s.role = "executor"; });
  expect(() => confirmWorkDraft(draftId, 1)).toThrow("Human");
  update(s => { s.role = "owner"; s.offline = true; });
  expect(() => beginWorkDraft()).toThrow();
});

test("tenant switching isolates drafts and rejects foreign draft confirmation", () => {
  const draftId = beginWorkDraft({ projectId: "launch" });
  update(s => { s.tenants.push({ id: "empty", name: "Empty", role: "owner" }); });
  switchTenant("empty");
  expect(read().drafts[draftId]).toBeUndefined();
  expect(() => confirmWorkDraft(draftId, 0)).toThrow("当前租户");
  expect(() => beginWorkDraft({ sourceId: "m1" })).toThrow("来源消息");
  switchTenant("studio");
  expect(read().drafts[draftId].project).toBe("launch");
});


test("source channel and execution determine context when no explicit project is supplied", () => {
  update(s => { s.project = "novel"; s.messages.push({ id: "execution-source", channel: "exec-EX-demo-W-104", text: "执行讨论" }); });
  const channelDraft = beginWorkDraft({ sourceId: "m1" });
  const privateDraft = beginWorkDraft({ sourceId: "m6" });
  expect(read().drafts[channelDraft].project).toBe("launch");
  expect(read().drafts[privateDraft].project).toBe("private");
  const draftId = beginWorkDraft({ sourceId: "execution-source" });
  expect(read().drafts[draftId]).toMatchObject({ sourceExecutionId: "EX-demo-W-104", executionId: null, channel: "launch", project: "launch" });
});

test("confirmed source metadata stays fixed across retries and later task changes", () => {
  const draftId=beginWorkDraft({sourceId:'m1',projectId:'launch'});
  const initialDraft=structuredClone(read().drafts[draftId]);
  saveWorkDraft(draftId,{title:'Fixed origin',criteria:'Explicit confirmation',type:'文档',project:'launch'},0);
  const work=confirmWorkDraft(draftId,1),origin=structuredClone(work.conversationOrigins[0]);
  expect(origin).toMatchObject({tenantId:'studio',projectId:'launch',channelId:'launch',messageId:'m1',threadId:'m1',createdBy:'H-lin',actorKind:'Human',draftId,draftRevision:1,objectVersion:1,confirmedBy:'H-lin'});
  expect(origin.createdAt).toBe(initialDraft.createdAt);expect(Number.isFinite(Date.parse(origin.confirmedAt))).toBe(true);
  expect(origin.confirmation).toMatchObject({actorId:'H-lin',status:'CONFIRMED',revision:1,time:origin.confirmedAt});
  expect(confirmWorkDraft(draftId,1).conversationOrigins[0]).toEqual(origin);
  update(s=>{const target=s.work.find(w=>w.id===work.id);target.version++;target.title='Changed work';});
  expect(read().work.find(w=>w.id===work.id).conversationOrigins[0]).toEqual(origin);
});
test("manual draft confirmation does not invent a source from the fallback channel", () => {
  const draftId=beginWorkDraft({projectId:'launch'});
  saveWorkDraft(draftId,{title:'Manual work',criteria:'Confirm intent',type:'文档',project:'launch'},0);
  const work=confirmWorkDraft(draftId,1);
  expect(work.creationSource).toBe('MANUAL');expect(work.conversationOrigins).toEqual([]);
});

test("source scope stays independent of work project after draft reassignment", async () => {
  const {resolveConversationOrigins}=await import('../src/conversation-origin.js');
  const draftId=beginWorkDraft({sourceId:'m1',projectId:'launch'});
  saveWorkDraft(draftId,{title:'Cross-project reuse',criteria:'Retain source scope',type:'文档',project:'film'},0);
  const work=confirmWorkDraft(draftId,1),source=resolveConversationOrigins(read(),work).sources[0];
  expect(work.project).toBe('film');expect(work.conversationOrigins[0].projectId).toBe('launch');
  expect(source.status).toBe('READY');const params=new URLSearchParams(source.link.split('?')[1]);
  expect(params.get('scope')).toBe('launch');expect(params.get('channel_id')).toBe('launch');
});
test("execution context and subsequent collaboration draft have separate exact origins", async () => {
  const {resolveConversationOrigins}=await import('../src/conversation-origin.js');
  for(const sourceId of [undefined,'runtime-input-source']){
    if(sourceId)update(s=>{s.messages.push({id:sourceId,channel:'exec-EX-demo-W-104',executionId:'EX-demo-W-104',text:'Local execution input',authorId:'H-lin',kind:'human'});});
    const draftId=beginWorkDraft({executionId:'EX-demo-W-104',sourceId});
    saveWorkDraft(draftId,{title:'Follow-up collaboration',criteria:'Distinct source and draft',type:'文档',project:'launch'},0);
    const work=confirmWorkDraft(draftId,1),sources=resolveConversationOrigins(read(),work).sources;
    expect(read().drafts[draftId].threadId).toBeNull();expect(sources).toHaveLength(2);
    expect(sources[0].status).toBe(sourceId?'READY':'CONTEXT');
    const executionParams=new URLSearchParams(sources[0].link.split('?')[1]);
    expect(executionParams.get('execution_id')).toBe('EX-demo-W-104');expect(executionParams.has('work_draft')).toBe(false);
    const draftParams=new URLSearchParams(sources[1].link.split('?')[1]);
    expect(sources[1].status).toBe('DRAFT');expect(draftParams.get('channel_id')).toBe('launch');expect(draftParams.get('work_draft')).toBe(draftId);expect(draftParams.has('execution_id')).toBe(false);
  }
});
test("source thread contradictions and unreadable roots are rejected before any draft write",()=>{
  update(s=>{s.messages.push({id:'source-root',channel:'launch',text:'root'},{id:'source-reply',channel:'launch',thread:'source-root',text:'reply'});});
  for(const threadId of ['other-thread','source-reply']){
    const before=structuredClone(read());expect(()=>beginWorkDraft({sourceId:'source-reply',threadId})).toThrow('来源线程');expect(read()).toEqual(before);
  }
  update(s=>{s.messages.find(m=>m.id==='source-root').recalled=true;});
  const before=structuredClone(read());expect(()=>beginWorkDraft({sourceId:'source-reply'})).toThrow('来源线程');expect(read()).toEqual(before);
});
test("every conflicting execution origin is rejected without memory or storage writes", () => {
  update(s => {
    s.executions.push({...structuredClone(s.executions.find(e => e.id === 'EX-demo-W-104')),id:'EX-review-other'});
    s.messages.push({id:'execution-origin',channel:'exec-EX-demo-W-104',executionId:'EX-demo-W-104',text:'Execution source'});
    s.messages.push({id:'contradictory-origin',channel:'exec-EX-demo-W-104',executionId:'EX-review-other',text:'Invalid metadata'});
    s.messages.push({id:'channel-with-execution',channel:'launch',executionId:'EX-demo-W-104',text:'Invalid collaboration metadata'});
  });
  for (const input of [
    {sourceId:'m1',executionId:'EX-demo-W-104'},
    {sourceId:'execution-origin',executionId:'EX-review-other'},
    {sourceId:'contradictory-origin'},
    {sourceId:'channel-with-execution'}
  ]) {
    const before=structuredClone(read()), stored=localStorage.getItem('collaboration-prototype-v1');
    expect(stored).not.toBeNull();expect(()=>beginWorkDraft(input)).toThrow('标识不一致');
    expect(read()).toEqual(before);expect(localStorage.getItem('collaboration-prototype-v1')).toBe(stored);
  }
});
test("explicit threads resolve real roots and reject missing, nested, recalled or foreign-channel roots atomically", async () => {
  const {resolveConversationOrigins}=await import('../src/conversation-origin.js');
  update(s=>s.messages.push({id:'nested-root',channel:'launch',thread:'m1',text:'Not a root'},
    {id:'nested-reply',channel:'launch',thread:'nested-root',text:'Reply'},
    {id:'recalled-root',channel:'launch',recalled:true,text:'Withdrawn'}));
  for (const input of [
    {channel:'launch',threadId:'absent-thread'},
    {sourceId:'nested-reply'},
    {channel:'launch',threadId:'nested-root'},
    {channel:'launch',threadId:'recalled-root'},
    {channel:'novel',threadId:'m1'}
  ]) {
    const before=structuredClone(read()),stored=localStorage.getItem('collaboration-prototype-v1');
    expect(stored).not.toBeNull();expect(()=>beginWorkDraft(input)).toThrow();expect(read()).toEqual(before);
    expect(localStorage.getItem('collaboration-prototype-v1')).toBe(stored);
  }
  const draftId=beginWorkDraft({channel:'launch',threadId:'m1'});
  saveWorkDraft(draftId,{title:'From an explicit thread',criteria:'Traceable source',type:'文档',project:'launch'},0);
  const source=resolveConversationOrigins(read(),confirmWorkDraft(draftId,1)).sources[0];
  expect(source.status).toBe('READY');expect(source.reference.messageId).toBe('m1');
  expect(source.reference.threadId).toBe('m1');
});

test("cached work drafts recheck original message access before render, save and confirmation", async () => {
  const {workDraftReadProblem}=await import("../src/store.js?work-draft-tests");
  for (const boundary of ["external","recalled","missing","membership"]) {
    reset();
    update(s=>{s.messages.push({id:"draft-private-source",channel:"launch",text:"private cached source",author:"林予",time:new Date().toISOString()});});
    const draftId=beginWorkDraft({sourceId:"draft-private-source"});
    const fields={title:"private derived title",criteria:"private derived acceptance",type:"软件",project:"launch"};
    saveWorkDraft(draftId,fields,0);
    update(s=>{
      if(boundary==="external")s.role="external";
      if(boundary==="recalled")s.messages.find(m=>m.id==="draft-private-source").recalled=true;
      if(boundary==="missing")s.messages=s.messages.filter(m=>m.id!=="draft-private-source");
      if(boundary==="membership"){s.role="executor";s.organization.projectRoles=s.organization.projectRoles.filter(r=>!(r.memberId==="H-lin"&&r.projectId==="launch"));s.channels.find(c=>c.id==="launch").members=[];s.projects.find(p=>p.id==="launch").ownerId="H-zhou";}
    });
    expect(workDraftReadProblem(read(),read().drafts[draftId])).not.toBe("");
    const before=JSON.stringify(read());
    expect(()=>saveWorkDraft(draftId,fields,1)).toThrow();
    expect(()=>confirmWorkDraft(draftId,1)).toThrow();
    expect(JSON.stringify(read())).toBe(before);
  }
});

for (const boundary of ["recalled", "missing", "unreadable", "nested"]) {
  test(`saved reply draft rejects ${boundary} thread root before reads or writes`, async () => {
    const { workDraftReadProblem } = await import("../src/store.js?work-draft-tests");
    const rootId = "lifecycle-root", replyId = "lifecycle-reply";
    update(s => s.messages.push(
      {id:rootId, channel:"launch", text:"Original root", authorId:"H-lin"},
      {id:replyId, channel:"launch", thread:rootId, text:"Original reply", authorId:"H-lin"}
    ));
    const draftId = beginWorkDraft({sourceId:replyId});
    const fields = {title:"Reply-derived task", criteria:"Retain valid source thread", type:"文档", project:"film"};
    saveWorkDraft(draftId, fields, 0);
    update(s => {
      const root = s.messages.find(message => message.id === rootId);
      if (boundary === "recalled") root.recalled = true;
      if (boundary === "missing") s.messages = s.messages.filter(message => message.id !== rootId);
      if (boundary === "unreadable") {
        // Owner status does not bypass membership in a custom source channel.
        const channel = s.channels.find(channel => channel.id === "launch");
        channel.kind = "CUSTOM"; channel.members = [];
      }
      if (boundary === "nested") root.thread = "m1";
    });
    const before = structuredClone(read()), stored = localStorage.getItem("collaboration-prototype-v1");
    expect(stored).not.toBeNull();
    expect(workDraftReadProblem(read(), read().drafts[draftId])).not.toBe("");
    expect(read()).toEqual(before);
    expect(() => saveWorkDraft(draftId, {...fields,title:"Must not overwrite"}, 1)).toThrow();
    expect(read()).toEqual(before);
    expect(localStorage.getItem("collaboration-prototype-v1")).toBe(stored);
    expect(() => confirmWorkDraft(draftId, 1)).toThrow();
    expect(read()).toEqual(before);
    expect(localStorage.getItem("collaboration-prototype-v1")).toBe(stored);
  });
}

test("readable reply draft still confirms across project reassignment with its original valid thread", async () => {
  const {workDraftReadProblem} = await import("../src/store.js?work-draft-tests");
  const {resolveConversationOrigins} = await import("../src/conversation-origin.js");
  update(s => s.messages.push(
    {id:"valid-root",channel:"launch",text:"Root",authorId:"H-lin"},
    {id:"valid-reply",channel:"launch",thread:"valid-root",text:"Reply",authorId:"H-lin"}
  ));
  const draftId = beginWorkDraft({sourceId:"valid-reply"});
  saveWorkDraft(draftId,{title:"Valid follow-up",criteria:"Original thread remains readable",type:"文档",project:"film"},0);
  expect(workDraftReadProblem(read(),read().drafts[draftId])).toBe("");
  const beforeCount = read().work.length, work = confirmWorkDraft(draftId,1);
  expect(read().work).toHaveLength(beforeCount+1);
  expect(work.project).toBe("film");
  expect(work.conversationOrigins[0]).toMatchObject({projectId:"launch",channelId:"launch",messageId:"valid-reply",threadId:"valid-root"});
  expect(resolveConversationOrigins(read(),work).sources[0].status).toBe("READY");
});
