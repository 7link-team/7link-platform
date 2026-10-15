import { test, expect, beforeEach } from "bun:test";
const entries = new Map(); globalThis.localStorage = { getItem: k => entries.get(k) || null, setItem: (k,v) => entries.set(k,v), removeItem: k => entries.delete(k) };
const api = await import("../src/store.js");
beforeEach(() => api.reset());
const get = id => api.read().resources.approvals.find(a => a.id === id);
const request = () => api.requestModelApproval("MD-02", api.read().resources.models.find(m => m.id === "MD-02").revision || 1);
const decide = (id,status="已批准",reason="Human 明确核对本地固定配置",receipt) => api.resourceCommand("approvals",id,status,reason,"",receipt);
const atomic = fn => { const before=structuredClone(api.read()), stored=[...entries]; expect(fn).toThrow(); expect(api.read()).toEqual(before); expect([...entries]).toEqual(stored); };
test("approval projections cannot be created or edited and legacy requests remain unapproved",()=>{
 const legacy=structuredClone(get("AP-01"));
 atomic(()=>api.saveResource("approvals",{...legacy,id:undefined}));atomic(()=>api.saveResource("approvals",{...legacy,target:"MD-01"}));
 atomic(()=>decide("AP-01"));expect(get("AP-01")).toEqual(legacy);
});
test("source request fixes current configuration and repeated requests are idempotent",()=>{
 const started=Date.now(); const id=request(); expect(Date.parse(get(id).requestedAt)).toBeGreaterThanOrEqual(started); expect(Date.parse(get(id).requestedAt)).toBeLessThanOrEqual(Date.now()); const before=structuredClone(api.read());expect(request()).toBe(id);expect(api.read()).toEqual(before);
 expect(get(id)).toMatchObject({targetKind:"model",target:"MD-02",tenantId:api.read().activeTenant,targetRevision:1,requestedBy:{source:"LOCAL_SIMULATION"}});
 expect(get(id).sourceSnapshot.model).toBe("writing-model");
});
test("every outcome rejects stale source revisions, altered content, missing sources and foreign tenant",()=>{
 for(const status of ["已批准","已拒绝","待补充"]) for(const change of ["revision","content","missing","tenant"]){
  api.reset();const id=request();api.update(s=>{const m=s.resources.models.find(m=>m.id==="MD-02");if(change==="revision")m.revision=2;if(change==="content")m.baseURL="https://changed.invalid";if(change==="missing")s.resources.models=s.resources.models.filter(m=>m.id!=="MD-02");if(change==="tenant")s.activeTenant="foreign";});atomic(()=>decide(id,status));
 }
});
test("terminal decisions are immutable; exact retries have no effects and old dialog revisions reject",()=>{
 for(const result of ["已批准","已拒绝","待补充"]){api.reset();const id=request();atomic(()=>decide(id,result,"reason",{revision:99,status:"待审核"}));decide(id,result);const fixed=structuredClone(api.read());decide(id,result);expect(api.read()).toEqual(fixed);for(const next of ["已批准","已拒绝","待补充"])atomic(()=>decide(id,next,"different decision"));atomic(()=>api.saveResource("approvals",{...get(id),target:"MD-01"}));}
});
test("known Human owner only; unsupported source types never inherit model approval behavior",()=>{
 for(const role of ["executor","external"]){api.reset();const id=request();api.update(s=>s.role=role);for(const result of ["已批准","已拒绝","待补充"])atomic(()=>decide(id,result));}
 api.reset();const id=request();api.update(s=>s.resources.approvals.find(a=>a.id===id).targetKind="unknown");atomic(()=>decide(id));
});
test("configuration application requires the exact approved source and editing invalidates it",()=>{
 const id=request();decide(id);api.resourceCommand("models","MD-02","生效","local rollout");expect(api.read().resources.models.find(m=>m.id==="MD-02").status).toBe("部分生效");
 const model=api.read().resources.models.find(m=>m.id==="MD-02");api.saveResource("models",{...model,scope:"new scope"});atomic(()=>api.resourceCommand("models","MD-02","生效","old approval cannot apply"));
 const next=request();expect(next).not.toBe(id);decide(next);api.resourceCommand("models","MD-02","生效","new revision rollout");expect(get(id).targetRevision).toBe(1);expect(get(next).targetRevision).toBe(2);
});

test("missing Human identity and damaged approved snapshot cannot authorize a rollout",()=>{
 const id=request();decide(id);api.update(s=>s.resources.approvals.find(a=>a.id===id).decision.snapshot.scope="forged");atomic(()=>api.resourceCommand("models","MD-02","生效","damaged approval"));
 api.reset();api.update(s=>s.organization.members=s.organization.members.filter(p=>p.id!=="H-lin"));atomic(request);
});
