import { beforeEach, expect, test } from "bun:test";
import { reportCurrentPlan, reportCurrentPlanBinding, reportCurrentPlanCheck, reportCurrentPlanBindingProblem } from "../src/plans";
import { reportSubmissionContent } from "../src/report-authorization";
import { prepareStructuredReport, currentPlanBrowserFixture } from "./report-lifecycle-fixtures";
if (!globalThis.localStorage) { const entries = new Map(); globalThis.localStorage = { getItem: key => entries.get(key) || null, setItem: (key,value) => entries.set(key,value), removeItem: key => entries.delete(key) }; }
const api = await import("../src/store.js?report-current-plan");
const report = id => api.read().resources.reports.find(r => r.id === id);
const plan = id => api.read().resources.plans.find(p => p.id === id);
const item = { id: "current-item", kind: "FREE_TASK", title: "本期调研承诺", goalId: "G-current", freeCategory: "LEARNING", criteria: "提供可复核的调研依据", weight: 1 };
const create = () => {
 const rid=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"周报",date:"2026-10-14",timezone:"Asia/Singapore"});
 const pid=api.openPeriodPlan({subjectKey:"agent:atlas",periodKind:"WEEK",date:"2026-10-14",timezone:"UTC",simulateAgent:true});
 api.savePeriodPlan(pid,{simulateAgent:true,items:[item]},plan(pid).revision);return {rid,pid};
};
const confirm = pid => {
 if(plan(pid).status!=="已提交")api.submitPeriodPlan(pid,plan(pid).revision,true);const p=plan(pid);
 api.decidePeriodPlan(pid,{revision:p.revision,submissionRevision:p.submission.revision,contentHash:p.submission.contentHash,action:"confirm",reason:"核对本期承诺基线"});
};
const adopt = (rid,pid) => { const binding=reportCurrentPlanBinding(api.read(),report(rid),pid,plan(pid).revision);api.saveResource("reports",{...report(rid),currentPlanBinding:binding});return binding; };
beforeEach(()=>{api.reset();api.update(s=>s.resources.goals.push({id:"G-current",name:"本期研究目标",subjectKey:"agent:atlas",ownerId:"atlas",ownerKind:"Agent",status:"进行中",revision:1,definitionRevision:1,krs:[],alignments:[]}));});
test("current binding records genuine baseline and original timezone; report save never confirms Plan",()=>{
 const {rid,pid}=create(), before=structuredClone(plan(pid));const binding=adopt(rid,pid);
 expect(binding.confirmed).toBe(false);expect(binding.snapshot.timezone).toBe("UTC");expect(plan(pid)).toEqual(before);
 expect(reportCurrentPlanCheck(api.read(),report(rid)).warnings.join()).toContain("N2");
 confirm(pid);expect(reportCurrentPlanCheck(api.read(),report(rid)).errors.join()).toContain("基线已变化");
 const confirmed=adopt(rid,pid);expect(confirmed.confirmed).toBe(true);expect(confirmed.revision).toBe(plan(pid).confirmedSnapshot.revision);
 expect(confirmed.snapshot).toEqual(plan(pid).confirmedSnapshot);
 expect(reportSubmissionContent(report(rid)).currentPlanBinding).toEqual(confirmed);
});
test("confirmed notes and pending replans retain original commitment; new Human baseline requires explicit adoption",()=>{
 const {rid,pid}=create();confirm(pid);const binding=adopt(rid,pid);
 api.savePeriodPlan(pid,{simulateAgent:true,items:[{...item,note:"后来补充备注",estimateHours:2,estimateCostMicros:2000000}]},plan(pid).revision);
 expect(plan(pid).items[0].estimateHours).toBe(2);expect(plan(pid).items[0].estimateCostMicros).toBe(2000000);
 expect(reportCurrentPlanCheck(api.read(),report(rid)).errors).toEqual([]);
 api.saveResource("reports",{...report(rid),summary:"补充本期说明"});expect(report(rid).currentPlanBinding).toEqual(binding);
 api.savePeriodPlan(pid,{simulateAgent:true,reason:"下游需求变化，需重核调研范围",items:[{...item,title:"待确认的新承诺"}]},plan(pid).revision);
 expect(plan(pid).status).toBe("已提交");expect(reportCurrentPlanBinding(api.read(),report(rid),pid,plan(pid).revision).snapshot.items[0].title).toBe(item.title);
 expect(reportCurrentPlanCheck(api.read(),report(rid)).errors).toEqual([]);
 expect(api.reportEvidence(api.read(),report(rid)).warnings.join()).toContain("原已确认基线");
 confirm(pid);const before=structuredClone(api.read());
 expect(()=>api.saveResource("reports",{...report(rid),summary:"不能默默换承诺"})).toThrow("基线已变化");expect(api.read()).toEqual(before);
 expect(adopt(rid,pid).snapshot.items[0].title).toBe("待确认的新承诺");
});
test("all forged or removed current bindings reject atomically, including fake confirmation and rendered object fields",()=>{
 const {rid,pid}=create();const genuine=reportCurrentPlanBinding(api.read(),report(rid),pid,plan(pid).revision);
 const variants=[{...genuine,confirmed:true},{...genuine,status:"已确认"},{...genuine,sourceRevision:1},{...genuine,tenantId:"foreign"},{...genuine,planId:{}},
  {...genuine,snapshot:{...genuine.snapshot,subjectKey:"human:H-lin"}}, {...genuine,snapshot:{...genuine.snapshot,periodEnd:"2026-10-17"}}, {...genuine,snapshot:{...genuine.snapshot,items:[null]}},
  ...["goalId","krId"].map(key=>({...genuine,snapshot:{...genuine.snapshot,items:[{...item,[key]:{invalid:true}}]}})),
  {...genuine,snapshot:{...genuine.snapshot,items:[{...item,title:"伪造承诺"}]}}];
 for(const binding of variants){const before=structuredClone(api.read());expect(()=>api.saveResource("reports",{...report(rid),currentPlanBinding:binding})).toThrow();expect(api.read()).toEqual(before);}
 adopt(rid,pid);const before=structuredClone(api.read());expect(()=>api.saveResource("reports",{...report(rid),currentPlanBinding:null})).toThrow("不可隐式移除");expect(api.read()).toEqual(before);
 expect(()=>reportCurrentPlanBinding(api.read(),report(rid),pid,1)).toThrow("修订已变化");
});
test("fixed current binding remains readable after source deletion and is part of actual Human acceptance",()=>{
 const {rid,pid}=create();prepareStructuredReport(api,rid);confirm(pid);adopt(rid,pid);const binding=structuredClone(report(rid).currentPlanBinding);
 api.resourceCommand("reports",rid,"已提交","明确本地模拟 Agent 核对上报","",{simulateAgent:true,revision:report(rid).revision,status:report(rid).status});
 const submitted=structuredClone(report(rid).submission);expect(submitted.content.currentPlanBinding).toEqual(binding);
 api.update(s=>{s.resources.reports.find(r=>r.id===rid).currentPlanBinding.snapshot.items[0].title="篡改固定承诺";});const forged=structuredClone(api.read());
 expect(()=>api.resourceCommand("reports",rid,"已接受","不能接受被篡改的固定报告")).toThrow("固定提交记录");expect(api.read()).toEqual(forged);
 api.update(s=>{s.resources.reports.find(r=>r.id===rid).currentPlanBinding=structuredClone(binding);});
 api.update(s=>{s.resources.plans=s.resources.plans.filter(p=>p.id!==pid);});
 expect(reportCurrentPlanCheck(api.read(),report(rid)).errors).toEqual([]);expect(api.reportEvidence(api.read(),report(rid)).warnings.join()).toContain("原固定引用保留");
 api.resourceCommand("reports",rid,"已接受","由指定 Human 接受固定报告");
 expect(report(rid).approval.snapshot.content.currentPlanBinding).toEqual(binding);expect(report(rid).submission).toEqual(submitted);
 for(const patch of [{tenantId:"foreign"},{snapshot:{}},{revision:0},{snapshot:{...binding.snapshot,subjectKey:"human:H-lin"}},
  {snapshot:{...binding.snapshot,items:[null]}},{snapshot:{...binding.snapshot,items:[{...binding.snapshot.items[0],weight:-1}]}}]){
  const corrupt={...binding,...patch};expect(reportCurrentPlanBindingProblem(api.read(),report(rid),corrupt)).toBeTruthy();
  expect(reportCurrentPlanCheck(api.read(),{...report(rid),currentPlanBinding:corrupt}).errors.length).toBeGreaterThan(0);
 }
});
test("missing, foreign, duplicate and wrong cadence Plans cannot be silently adopted; absent legacy fingerprint is unchanged",()=>{
 const {rid,pid}=create();const r=report(rid);expect(Object.keys(reportSubmissionContent({...r,currentPlanBinding:null}))).not.toContain("currentPlanBinding");
 for(const patch of [{tenantId:"foreign"},{subjectKey:"human:H-lin"},{periodKind:"DAY"},{periodStart:"2026-10-19"}]){
  const s=structuredClone(api.read());Object.assign(s.resources.plans.find(p=>p.id===pid),patch);expect(reportCurrentPlan(s,r)).toBeNull();expect(()=>reportCurrentPlanBinding(s,r,pid,plan(pid).revision)).toThrow();
 }
 api.update(s=>s.resources.plans.push({...structuredClone(plan(pid)),id:"duplicate"}));const before=structuredClone(api.read());
 expect(()=>reportCurrentPlanBinding(api.read(),r,pid,plan(pid).revision)).toThrow("重复计划");expect(api.read()).toEqual(before);
});

test("current-reference browser corruption fixture uses a distinct real period without duplicate reports",()=>{
 const s=currentPlanBrowserFixture(api), rows=Object.values(s.testCurrentPlans).map(ids=>s.resources.reports.find(r=>r.id===ids.reportId));
 expect(rows).toHaveLength(3);expect(new Set(rows.map(r=>JSON.stringify([r.subjectKey,r.cadence,r.periodStart]))).size).toBe(3);
 const corrupt=rows.find(r=>r.id===s.testCurrentPlans.CORRUPT.reportId);
 expect(corrupt.currentPlanBinding.snapshot.items.some(item=>item.id===corrupt.content.uncompleted[0].plan_item_id)).toBe(false);
 expect(rows.filter(r=>r!==corrupt).every(r=>r.currentPlanBinding.snapshot.items.some(item=>item.id===r.content.uncompleted[0].plan_item_id))).toBe(true);
});
