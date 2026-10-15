import { test, expect } from "bun:test";
import { initial } from "../src/data";
import { hydrateChannels } from "../src/channels";
import { resolveConversationOrigins } from "../src/conversation-origin.js";
const fixture = () => {
  const s = structuredClone(initial); s.activeTenant = "studio"; hydrateChannels(s); s.drafts = {};
  s.executions=[{id:"EX-review",tenantId:"studio",projectId:"launch",initiatorId:"H-lin",participantIds:["H-lin"]}];
  s.messages = [
    {id:"root",channel:"team",author:"Human",text:"CURRENT_ROOT",time:"2026-10-14T10:00:00Z",history:[{text:"OLD_EDIT_SECRET"}]},
    {id:"reply",channel:"team",thread:"root",author:"Agent",text:"CURRENT_REPLY",time:"2026-10-14T10:01:00Z"},
    {id:"unrelated",channel:"team",author:"Human",text:"OTHER_THREAD_SECRET"},
    {id:"private-root",channel:"private",text:"PRIVATE_SECRET"},
    {id:"external-root",channel:"external",text:"EXTERNAL_SECRET",time:"2026-10-14T10:00:00Z"},
    {id:"execution-root",channel:"exec-EX-review",text:"EXECUTION_SOURCE"},
  ];
  return s;
};
const origin = extra => ({tenantId:"studio",channelId:"team",messageId:"reply",threadId:"root",...extra});
const object = extra => ({id:"OBJECT-1",title:"Current object",version:7,status:"待确认",conversationOrigins:[origin()],...extra});
const first = (s, record = object()) => resolveConversationOrigins(s,record).sources[0];
const secretless = result => { for(const text of ["CACHED_SECRET","OLD_EDIT_SECRET","PRIVATE_SECRET","OTHER_THREAD_SECRET","FOREIGN_SECRET"]) expect(JSON.stringify(result)).not.toContain(text); };

test("stable references resolve exact authorized message/thread links without editing fixed origins",()=>{
 const s=fixture(),record=object({conversationOrigins:[origin({createdBy:"atlas",createdAt:"2026-10-12",objectVersion:1,confirmedBy:"H-lin"})]});const before=structuredClone(record);
 const result=first(s,record);expect(result.status).toBe("READY");expect(result.message.text).toBe("CURRENT_REPLY");expect(result.thread.map(m=>m.id)).toEqual(["root","reply"]);
 expect(result.creation.objectVersion).toBe(1);expect(record.version).toBe(7);expect(record).toEqual(before);
 const q=new URLSearchParams(result.link.split("?")[1]);expect(q.get("tenant")).toBe("studio");expect(q.get("channel_id")).toBe("team");expect(q.get("message_id")).toBe("reply");expect(q.get("thread_id")).toBe("root");expect(q.has("execution_id")).toBe(false);secretless(result);
 s.messages.find(m=>m.id==="reply").text="EDITED_CURRENT_REPLY";expect(first(s,record).message.text).toBe("EDITED_CURRENT_REPLY");expect(first(s,record).creation).toEqual(result.creation);
});

test("foreign tenant or missing tenant never falls back to matching local IDs or cached text",()=>{
 const s=fixture();for(const tenantId of ["foreign",null,undefined]) {
  const result=first(s,object({conversationOrigins:[origin({tenantId,text:"CACHED_SECRET",createdBy:"FOREIGN_SECRET"})]}));
  expect(result.link).toBeNull();expect(result.message).toBeNull();expect(result.thread).toEqual([]);expect(result.creation).toBeNull();secretless(result);
 }
 const legacy={sourceContext:{channel:"team",messageId:"reply",text:"CACHED_SECRET"}};expect(first(s,legacy).status).toBe("UNRECORDED");secretless(first(s,legacy));
});

test("current private-channel and per-message historical access are rechecked on every resolution",()=>{
 const s=fixture(),privateRecord=object({conversationOrigins:[origin({channelId:"private",messageId:"private-root",threadId:null})]});expect(first(s,privateRecord).status).toBe("READY");
 s.channels.find(c=>c.id==="private").members=s.channels.find(c=>c.id==="private").members.filter(m=>m.principalId!=="H-lin");
 expect(first(s,privateRecord).status).toBe("UNAVAILABLE");secretless(first(s,privateRecord));
 s.role="external";const c=s.channels.find(c=>c.id==="external"),membership=c.members.find(m=>m.principalId==="external-demo");membership.historyAccess="FROM_JOIN";membership.joinedAt="2026-10-13T00:00:00Z";membership.excludedHistoryIds=["external-root"];
 const record=object({conversationOrigins:[origin({channelId:"external",messageId:"external-root",threadId:null})]});expect(first(s,record).message).toBeNull();expect(JSON.stringify(first(s,record))).not.toContain("EXTERNAL_SECRET");
});

test("recalled missing wrong-channel and mismatched-thread sources expose no cached history",()=>{
 for(const change of [s=>{s.messages.find(m=>m.id==="reply").recalled=true;},s=>{s.messages=s.messages.filter(m=>m.id!=="reply");},s=>{s.messages.find(m=>m.id==="reply").channel="private";},s=>{s.messages.find(m=>m.id==="reply").thread="unrelated";},s=>{s.messages=s.messages.filter(m=>m.id!=="root");},s=>{s.messages.find(m=>m.id==="root").recalled=true;}]){
  const s=fixture();change(s);const result=first(s,object({sourceContext:{text:"CACHED_SECRET"}}));expect(result.message).toBeNull();expect(result.link).toBeNull();expect(result.thread).toEqual([]);secretless(result);
 }
 const s=fixture();s.messages.push({id:"old-reply",channel:"team",thread:"root",text:"CACHED_SECRET",recalled:true,history:[{text:"OLD_EDIT_SECRET"}]});const result=first(s);expect(result.thread.find(m=>m.id==="old-reply").recalled).toBe(true);secretless(result);
 const bad=object({conversationOrigins:[origin({messageId:"root",threadId:"unrelated"})]});expect(first(s,bad).status).toBe("MISMATCH");
});

test("legacy task references use actual drafts and never guess a team channel",()=>{
 const s=fixture();s.drafts.d={id:"d",kind:"work",tenantId:"studio",channel:"private",status:"讨论中",threadId:null};
 const record={id:"work",draftId:"d",planSnapshot:{draftRevision:2,confirmedBy:"H-lin"}};const result=first(s,record);
 expect(result.status).toBe("DRAFT");expect(result.message).toBeNull();expect(result.link).toBe("/chat?tenant=studio&channel_id=private&work_draft=d");expect(result.creation.draftRevision).toBe(2);expect(result.creation.objectVersion).toBeNull();
 s.drafts.d.channel=undefined;expect(first(s,record).status).toBe("UNRECORDED");
 s.drafts.d.channel="private";s.drafts.d.tenantId="foreign";expect(first(s,record).status).toBe("FOREIGN_TENANT");
 delete s.drafts.d;expect(first(s,record).link).toBeNull();
 const sourceRecord={sourceContext:{tenantId:"studio",channel:"team",messageId:"reply",threadId:"root",text:"CACHED_SECRET"},draftId:"missing"};const readable=first(s,sourceRecord);expect(readable.status).toBe("READY");expect(readable.link).not.toContain("work_draft");expect(readable.warning).toBeTruthy();secretless(readable);
});

test("execution drafts produce execution routes and contradictory explicit channel IDs are rejected",()=>{
 const s=fixture();s.drafts.d={id:"d",kind:"work",tenantId:"studio",channel:"team",executionId:"EX-review",status:"讨论中",threadId:null};
 let result=first(s,{draftId:"d"});expect(result.status).toBe("DRAFT");expect(result.link).toBe("/chat?tenant=studio&execution_id=EX-review&work_draft=d");
 result=first(s,object({conversationOrigins:[origin({channelId:"exec-EX-review",executionId:"EX-review",messageId:"execution-root",threadId:null})]}));expect(result.status).toBe("READY");expect(result.link).toContain("execution_id=EX-review");expect(result.link).not.toContain("channel_id");
 expect(first(s,object({conversationOrigins:[origin({executionId:"EX-review"})]})).status).toBe("MISMATCH");
 s.role="external";expect(first(s,{draftId:"d"}).link).toBeNull();
});

test("multiple explicit origins remain distinct; manual and malformed records never synthesize chats",()=>{
 const s=fixture();const result=resolveConversationOrigins(s,object({conversationOrigins:[origin(),origin({tenantId:"foreign"}),origin({channelId:"private",messageId:"private-root",threadId:null})]}));
 expect(result.sources.map(x=>x.status)).toEqual(["READY","FOREIGN_TENANT","READY"]);
 for(const kind of ["MANUAL","IMPORT","API","SCHEDULE"]) {const resolved=resolveConversationOrigins(s,{sourceKind:kind});expect(resolved.category).toBe(kind);expect(resolved.sources).toEqual([]);}
 expect(resolveConversationOrigins(s,{title:"team",source:"same-name"}).category).toBe("UNRECORDED");
 expect(resolveConversationOrigins(s,{conversationOrigins:[],sourceContext:{tenantId:"studio",channel:"team",messageId:"root"}}).sources).toEqual([]);
 expect(first(s,{conversationOrigins:{text:"CACHED_SECRET"}}).status).toBe("UNRECORDED");
});

test("links encode stable opaque IDs and unrelated draft scopes do not appear in links",()=>{
 const s=fixture(),c=structuredClone(s.channels.find(c=>c.id==="external"));c.id="shared &/雪";s.channels.push(c);
 s.messages.push({id:"message &/雪",channel:c.id,text:"safe"});s.drafts.foreign={id:"foreign",kind:"work",tenantId:"foreign",channel:c.id,status:"PRIVATE_SECRET"};
 const result=first(s,object({conversationOrigins:[origin({channelId:c.id,messageId:"message &/雪",threadId:null,draftId:"foreign"})]}));
 const params=new URLSearchParams(result.link.split("?")[1]);expect(params.get("channel_id")).toBe(c.id);expect(params.get("message_id")).toBe("message &/雪");expect(params.has("work_draft")).toBe(false);expect(result.draftStatus).toBeNull();secretless(result);
});


test("draft thread mismatch never attaches the wrong discussion; fixed historical records do not borrow current origins",()=>{
 const s=fixture();s.drafts.d={id:"d",kind:"work",tenantId:"studio",channel:"team",threadId:"root",status:"讨论中"};
 const draftRecord=object({conversationOrigins:[origin({messageId:null,threadId:"unrelated",draftId:"d"})]});expect(first(s,draftRecord).link).toBeNull();
 const sourceRecord=object({conversationOrigins:[origin({messageId:"unrelated",threadId:"unrelated",draftId:"d"})]});expect(first(s,sourceRecord).status).toBe("READY");expect(first(s,sourceRecord).link).not.toContain("work_draft");
 const current=object({history:[{conversationOrigins:[origin({messageId:"missing-old",threadId:null})]}]});expect(first(s,current).message.text).toBe("CURRENT_REPLY");expect(first(s,current.history[0]).status).toBe("MISSING_MESSAGE");
});


test("readable thread root never authorizes a reply excluded by historical message access",()=>{
 const s=fixture();s.role="external";const membership=s.channels.find(c=>c.id==="external").members.find(m=>m.principalId==="external-demo");
 membership.historyAccess="FROM_JOIN";membership.joinedAt="2026-10-13T00:00:00Z";membership.excludedHistoryIds=["excluded-reply"];
 s.messages.push({id:"excluded-reply",channel:"external",thread:"external-root",text:"EXCLUDED_REPLY_SECRET",time:"2026-10-14T11:00:00Z"});
 const result=first(s,object({conversationOrigins:[origin({channelId:"external",messageId:"excluded-reply",threadId:"external-root"})]}));
 expect(result.status).toBe("UNAVAILABLE");expect(result.message).toBeNull();expect(result.link).toBeNull();expect(JSON.stringify(result)).not.toContain("EXCLUDED_REPLY_SECRET");
});
