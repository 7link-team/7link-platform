import { test, expect, beforeEach } from "bun:test";
import { visibleKnowledge, memoryRevocationProblem } from "../src/knowledge";
const stored = new Map();
globalThis.localStorage = { getItem: key => stored.get(key) || null, setItem: (key,value) => stored.set(key,value), removeItem: key => stored.delete(key) };
const api = await import("../src/store");
beforeEach(api.reset);
const get = id => api.read().resources.knowledge.find(item => item.id === id);
function own() {
  api.saveResource("knowledge", { name:"自己的派生经验", kind:"记忆", status:"待确认", owner:"林予", scope:"私人", content:"仅用于改进后续执行", source:"m6" });
  const record = api.read().resources.knowledge[0];
  api.resourceCommand("knowledge",record.id,"有效","Human 确认自己的派生经验");
  return get(record.id);
}
const request = record => ({tenantId:api.read().activeTenant,id:record.id,revision:record.revision || 1,status:record.status,reason:"经验内容已有错误，停止继续使用"});
function atomic(fn) { const before=structuredClone(api.read()),disk=[...stored];expect(fn).toThrow();expect(api.read()).toEqual(before);expect([...stored]).toEqual(disk); }
test("own active memory revocation retains canonical history and source messages, excludes usable projection",()=>{
  const record=structuredClone(own()),messages=structuredClone(api.read().messages),started=Date.now();
  api.revokeKnowledgeMemory(request(record));const revoked=get(record.id);
  expect(revoked.id).toBe(record.id);expect(revoked.content).toBe(record.content);expect(revoked.source).toBe("m6");expect(revoked.status).toBe("已撤销");expect(revoked.revision).toBe((record.revision || 1)+1);
  expect(revoked.revocation).toMatchObject({actorId:"H-lin",actorKind:"Human",source:"LOCAL_SIMULATION"});expect(Date.parse(revoked.revocation.time)).toBeGreaterThanOrEqual(started);
  expect(api.read().messages).toEqual(messages);expect(revoked.history.some(item=>item.id===record.id&&item.content===record.content&&item.status==="有效")).toBe(true);
  expect(visibleKnowledge(api.read()).some(item=>item.id===record.id)).toBe(false);expect(visibleKnowledge(api.read(),{includeRevoked:true}).some(item=>item.id===record.id)).toBe(true);
  for(const role of ["owner","executor"]){api.update(s=>s.role=role);atomic(()=>api.saveResource("knowledge",record));atomic(()=>api.saveResource("knowledge",{...revoked,status:"有效",revocation:undefined}));for(const status of ["有效","已禁用","已撤销"])atomic(()=>api.resourceCommand("knowledge",record.id,status,"不能复活已撤销原件"));atomic(()=>api.revokeKnowledgeMemory(request(revoked)));}
});
test("wrong type, pending candidate, non-owner, stale tenant/revision/status and invalid reason deny atomically",()=>{
  const record=structuredClone(own());
  for(const patch of [{tenantId:"foreign"},{revision:99},{status:"待确认"},{reason:"短"},{reason:"x".repeat(2001)},{id:"missing"}])atomic(()=>api.revokeKnowledgeMemory({...request(record),...patch}));
  for(const id of ["K-01","K-02","K-03"])atomic(()=>api.revokeKnowledgeMemory(request(get(id))));
  api.saveResource("knowledge",{name:"他人的共享经验",owner:"周宁",kind:"记忆",scope:"租户",status:"有效",content:"共享可读不等于自己的记忆"});
  atomic(()=>api.revokeKnowledgeMemory(request(api.read().resources.knowledge[0])));
  api.update(s=>{s.resources.knowledge.find(item=>item.id===record.id).owner="许言";s.resources.knowledge.find(item=>item.id===record.id).ownerId="H-xu";});atomic(()=>api.revokeKnowledgeMemory(request(record)));
  api.reset();const ownRecord=own();api.saveResource("knowledge",{...ownRecord,content:"已编辑新版本"});atomic(()=>api.revokeKnowledgeMemory(request(ownRecord)));
  api.update(s=>s.offline=true);atomic(()=>api.revokeKnowledgeMemory(request(get(ownRecord.id))));
});
test("revocation metadata cannot be injected through ordinary editors; historical exports retain revoked status",async()=>{
  const record=structuredClone(own());for(const patch of [{revocation:{actorId:"H-lin"}},{status:"已撤销"}])atomic(()=>api.saveResource("knowledge",{...record,...patch}));
  api.revokeKnowledgeMemory(request(record));const revoked=get(record.id);expect(memoryRevocationProblem(api.read(),revoked)).toContain("终态");
  const savedDocument=globalThis.document,savedCreate=URL.createObjectURL,savedRevoke=URL.revokeObjectURL;let blob;
  try {globalThis.document={createElement:()=>({click(){}})};URL.createObjectURL=value=>(blob=value,"blob:revocation");URL.revokeObjectURL=()=>{};api.exportData("knowledge",revoked);expect(JSON.parse(await blob.text())).toEqual(JSON.parse(JSON.stringify(revoked)));atomic(()=>api.exportData("knowledge",record));}
  finally {globalThis.document=savedDocument;URL.createObjectURL=savedCreate;URL.revokeObjectURL=savedRevoke;}
});
