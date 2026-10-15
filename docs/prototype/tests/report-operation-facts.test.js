import { beforeEach, expect, test } from "bun:test";
import { completeValidation } from "./qa-helpers.js";
import { prepareStructuredReport } from "./report-lifecycle-fixtures.js";
import { reportContentFacts, reportContentCheck, reportGeneratedContent, reportFactReference } from "../src/report-content.js";
if (!globalThis.localStorage) { const entries=new Map();globalThis.localStorage={getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)}; }
const api=await import("../src/store.js?report-operation-facts");
const get=id=>api.read().resources.reports.find(r=>r.id===id);
const ops=(report,now)=>reportContentFacts(api.read(),report,now).filter(f=>["DECISION","DELIVERY"].includes(f.kind));
beforeEach(()=>api.reset());
function fixture(subjectKey="human:H-lin"){
 completeValidation(api,"W-103");api.workCommand("W-103","accept","明确本地模拟接受成果",2);api.workCommand("W-103","deliver","明确本地模拟固定包发送",2);
 const rid=api.generatePeriodicReport({subjectKey,cadence:"日报",date:new Date().toISOString().slice(0,10),timezone:"UTC"});return rid;
}
function prepared(){const rid=fixture();prepareStructuredReport(api,rid);const r=structuredClone(get(rid));const facts=ops(r);r.content.completed=facts.map((f,i)=>({key:`operation-${i}`,title:"本地操作事实",goal_id:"",kr_id:"",plan_item_id:"",fact_ref:reportFactReference(f),note:""}));r.content.summary.completed_count=facts.length;r.content.summary.accepted_work_items=0;api.saveResource("reports",r);return rid;}
function submit(rid){api.resourceCommand("reports",rid,"已提交","本人核对固定裁决与本地发送事实","",{revision:get(rid).revision,status:get(rid).status});}
test("actual actor gets DECISION and DELIVERY while owner/executor do not inherit those operations",()=>{
 const rid=fixture();expect(ops(get(rid)).map(f=>f.kind)).toEqual(["DECISION","DECISION","DELIVERY"]);
 for(const subjectKey of ["human:H-zhou","agent:muse"]){const r={...get(rid),subjectKey};expect(ops(r)).toEqual([]);}
});
test("generation keeps typed operation identities, zero accepted Work and no fabricated Plan completion",()=>{
 const rid=fixture();const generated=reportGeneratedContent(api.read(),get(rid));expect(generated.content.completed.map(i=>i.fact_ref.kind).sort()).toEqual(["DECISION","DECISION","DELIVERY"]);expect(generated.content.summary.completed_count).toBe(3);expect(generated.content.summary.accepted_work_items).toBe(0);expect(generated.content.completed.every(i=>!i.plan_item_id)).toBe(true);
});
test("typed selected operations pass B1 and do not become accepted work totals or work references",()=>{
 const rid=prepared(),r=get(rid),check=reportContentCheck(api.read(),r);expect(check.issues).toEqual([]);expect(check.facts).toHaveLength(3);expect(r.references).toEqual([]);expect(api.reportEvidence(api.read(),r).totals.completed).toBe(0);
});
test("real fixed submission survives deleted operation sources, keeps actor/time and warns N4",()=>{
 const rid=prepared();submit(rid);const fixed=structuredClone(get(rid).submission),before=ops(get(rid));api.update(s=>{s.work=s.work.filter(w=>w.id!=="W-103");s.resources.deliveries=s.resources.deliveries.filter(d=>d.work!=="W-103");});expect(ops(get(rid))).toEqual(before);expect(reportContentCheck(api.read(),get(rid)).warnings.filter(w=>w.startsWith("N4"))).toHaveLength(3);
 const stateBefore=structuredClone(api.read());expect(()=>api.resourceCommand("reports",rid,"已接受","本人不得自审","",{revision:get(rid).revision,status:get(rid).status})).toThrow("本人");expect(api.read()).toEqual(stateBefore);expect(get(rid).submission).toEqual(fixed);expect(reportContentCheck(api.read(),get(rid)).issues).toEqual([]);
});
test("fixed missing/corrupt typed snapshots refuse completion and never use live events",()=>{
 const rid=prepared();submit(rid);const fixed=structuredClone(get(rid));for(const bad of [undefined,null,{},[null],[{kind:"DECISION"}]]){const r=structuredClone(fixed);r.submission.factSnapshot.structuredFacts=bad;expect(()=>reportContentFacts(api.read(),r)).toThrow();expect(reportContentCheck(api.read(),r).issues.some(i=>i.code==="B1")).toBe(true);}
});
test("rejected/partial delivery is excluded from live reports but original fixed SENT remains",()=>{
 const rid=prepared();submit(rid);const fixed=structuredClone(get(rid));for(const status of ["已拒收","部分接受"]){api.update(s=>s.resources.deliveries.find(d=>d.work==="W-103").status=status);expect(ops({...fixed,submission:null}).filter(f=>f.kind==="DELIVERY")).toEqual([]);expect(ops(fixed).filter(f=>f.kind==="DELIVERY")).toHaveLength(1);expect(reportContentCheck(api.read(),fixed).warnings.some(w=>w.startsWith("N4")&&w.includes("交付"))).toBe(true);}
});
test("tenant, actual actor, report timezone and cutoff constrain operation facts",()=>{
 const rid=fixture(),r=get(rid),original=structuredClone(api.read().work.find(w=>w.id==="W-103").decisionFacts[0]);
 const set=fact=>api.update(s=>{s.work.find(w=>w.id==="W-103").decisionFacts=[fact];s.resources.deliveries=s.resources.deliveries.filter(d=>d.work!=="W-103");});
 set({...original,tenantId:"foreign",subjectKeys:original.subjectKeys.map(k=>k==="tenant:studio"?"tenant:foreign":k)});expect(ops(r)).toEqual([]);
 set({...original,actor:{kind:"Human",id:"H-zhou"},subjectKeys:[...original.subjectKeys,"human:H-zhou"]});expect(ops(r)).toEqual([]);
 const time="2026-10-14T23:30:00Z",futureNow=new Date("2026-10-16T00:00:00Z");set({...original,time});const dated={...r,periodStart:"2026-10-14",periodEnd:"2026-10-14",timezone:"UTC"};expect(ops(dated,futureNow)).toHaveLength(1);expect(ops({...dated,timezone:"Asia/Singapore"},futureNow)).toEqual([]);expect(ops(dated,new Date("2026-10-14T23:29:59Z"))).toEqual([]);
});
test("typed collisions preserve distinct facts and duplicate same-type event triggers B2",()=>{
 const rid=prepared();const r=structuredClone(get(rid));r.content.completed.push({...r.content.completed[0],key:"duplicate"});r.content.summary.completed_count=null;expect(reportContentCheck(api.read(),r).issues.some(i=>i.code==="B2")).toBe(true);
});
test("legacy pending delivery without sender/time never becomes a completed send",()=>{
 const rid=api.generatePeriodicReport({subjectKey:"human:H-lin",cadence:"日报",date:new Date().toISOString().slice(0,10),timezone:"UTC"});expect(ops(get(rid))).toEqual([]);
});

test("same event ID in different typed sources remains distinct in generation and fixed checking",()=>{
 const rid=fixture();api.update(s=>{const decision=s.work.find(w=>w.id==="W-103").decisionFacts[0];s.resources.deliveries.find(d=>d.work==="W-103").sendFact.id=decision.id;});const facts=ops(get(rid));expect(facts).toHaveLength(3);expect(facts.filter(f=>f.id===facts[0].id)).toHaveLength(2);const generated=reportGeneratedContent(api.read(),get(rid));expect(generated.content.completed).toHaveLength(3);expect(generated.content.summary.accepted_work_items).toBe(0);
});

test("a known unrelated actor's damaged evidence does not invalidate another member's empty operation list",()=>{
 const rid=fixture();api.update(s=>s.work.find(w=>w.id==="W-103").decisionFacts[0].reason=null);expect(ops({...get(rid),subjectKey:"agent:atlas"})).toEqual([]);expect(()=>ops(get(rid))).toThrow("原始事实损坏");
});

test("malformed live history produces N4 without breaking a valid fixed report",()=>{
 const rid=prepared();submit(rid);const fixed=structuredClone(get(rid)),expected=ops(fixed);for(const history of [{find:1},{},"corrupt",null,[null]]){api.update(s=>s.work.find(w=>w.id==="W-103").decisionFacts=history);expect(ops(fixed)).toEqual(expected);const check=reportContentCheck(api.read(),fixed);expect(check.issues).toEqual([]);expect(check.warnings.filter(w=>w.startsWith("N4"))).toHaveLength(2);}
});
