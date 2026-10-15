import { beforeEach, expect, test } from "bun:test";
import {freeTaskBrowserFixture} from "./report-lifecycle-fixtures";
import {prepareReportRegeneration} from "../src/report-content";
const entries=new Map();globalThis.localStorage={getItem:k=>entries.get(k)||null,setItem:(k,v)=>entries.set(k,v),removeItem:k=>entries.delete(k)};
const api=await import("../src/store.js?regeneration-review");
const get=id=>api.read().resources.reports.find(r=>r.id===id);
function fixture(){const s=freeTaskBrowserFixture(api),ids=s.testFreeTask;return {...ids,report:structuredClone(get(ids.reportId))};}
function finish(f){const p=api.read().resources.plans.find(p=>p.id===f.planId);api.recordPlanFreeTask(p.id,f.report.content.uncompleted[0].plan_item_id,{revision:p.revision,simulateAgent:true,evidence:"明确本地模拟：完成原未完成的调研"});}
beforeEach(()=>api.reset());
test("explicit refill moves actual finished Plan item, preserves manual retrospective and saves original history",()=>{
 const f=fixture();finish(f);const before=structuredClone(get(f.reportId));
 api.regenerateReportContent(f.reportId,{revision:before.revision,simulateAgent:true,sections:["completed","uncompleted"]});
 const r=get(f.reportId);expect(r.content.completed).toHaveLength(2);expect(r.content.uncompleted).toHaveLength(0);expect(r.content.summary).toMatchObject({completed_count:2,accepted_work_items:0,uncompleted_count:0});
 expect(r.content.retrospective).toEqual(before.content.retrospective);expect(r.content.next_plan).toEqual(before.content.next_plan);expect(r.nextPlanBinding).toEqual(before.nextPlanBinding);
 expect(r.history.at(-1).content).toEqual(before.content);expect(r.revision).toBe(before.revision+1);expect(r.status).toBe("草稿");expect(r.submission).toBeNull();
});
test("selected next chapter uses latest real Plan revision without changing other chapters",()=>{
 const f=fixture(),pid=f.report.nextPlanBinding.planId,p=api.read().resources.plans.find(p=>p.id===pid);
 api.savePeriodPlan(pid,{simulateAgent:true,items:p.items.map(i=>({...i,title:"新下期真实承诺"}))},p.revision);
 const before=structuredClone(get(f.reportId));api.regenerateReportContent(f.reportId,{revision:before.revision,simulateAgent:true,sections:["next_plan"]});
 const r=get(f.reportId);expect(r.nextPlanBinding.revision).toBe(p.revision+1);expect(r.content.next_plan[0].title).toBe("新下期真实承诺");
 for(const key of ["completed","uncompleted","retrospective","summary"])expect(r.content[key]).toEqual(before.content[key]);
});
test("partial refill conflict rejects without changing any data",()=>{
 const f=fixture();finish(f);const before=structuredClone(api.read());expect(()=>api.regenerateReportContent(f.reportId,{revision:get(f.reportId).revision,simulateAgent:true,sections:["completed"]})).toThrow("未选章节");expect(api.read()).toEqual(before);
});
test("author permission, explicit Agent simulation, revision and section guards are atomic",()=>{
 const f=fixture();
 for(const patch of [{simulateAgent:false},{revision:0},{sections:[]},{sections:["toString"]},{sections:["completed","completed"]}]){
  const before=structuredClone(api.read());expect(()=>api.regenerateReportContent(f.reportId,{revision:get(f.reportId).revision,simulateAgent:true,...patch})).toThrow();expect(api.read()).toEqual(before);
 }
 api.update(s=>s.role="viewer");const before=structuredClone(api.read());expect(()=>api.regenerateReportContent(f.reportId,{revision:get(f.reportId).revision,simulateAgent:true})).toThrow();expect(api.read()).toEqual(before);
});
test("missing next Plan and accepted report refuse without false success or lost source",()=>{
 const f=fixture();api.update(s=>s.resources.plans=s.resources.plans.filter(p=>p.id!==f.report.nextPlanBinding.planId));let before=structuredClone(api.read());
 expect(()=>api.regenerateReportContent(f.reportId,{revision:get(f.reportId).revision,simulateAgent:true,sections:["next_plan"]})).toThrow("尚无真实下期计划");expect(api.read()).toEqual(before);
 api.update(s=>s.resources.reports.find(r=>r.id===f.reportId).status="已接受");before=structuredClone(api.read());
 expect(()=>api.regenerateReportContent(f.reportId,{revision:get(f.reportId).revision,simulateAgent:true})).toThrow("已接受报告不可编辑");expect(api.read()).toEqual(before);
});
test("pure preview preserves storage and caller inputs",()=>{
 const f=fixture(),state=structuredClone(api.read()),report=structuredClone(f.report),before=structuredClone(state),original=structuredClone(report);
 const preview=prepareReportRegeneration(state,report,["completed","uncompleted"]);expect(preview.content.completed).toHaveLength(1);expect(preview.content.uncompleted).toHaveLength(1);expect(preview.content.uncompleted[0].detail_confirmation).toBeNull();
 expect(preview.content.retrospective).toEqual(original.content.retrospective);expect(state).toEqual(before);expect(report).toEqual(original);
});

test("unselected changed next Plan keeps exact original binding and remains blocked at submission",()=>{
 const f=fixture(),pid=f.report.nextPlanBinding.planId,p=api.read().resources.plans.find(p=>p.id===pid);
 api.savePeriodPlan(pid,{simulateAgent:true,items:p.items.map(i=>({...i,title:"下期独立升版"}))},p.revision);
 const before=structuredClone(get(f.reportId));api.regenerateReportContent(f.reportId,{revision:before.revision,simulateAgent:true,sections:["completed","uncompleted"]});
 const r=get(f.reportId);expect(r.nextPlanBinding).toEqual(before.nextPlanBinding);expect(r.content.next_plan).toEqual(before.content.next_plan);
 expect(()=>api.resourceCommand("reports",r.id,"已提交","明确本地模拟提交","",{simulateAgent:true,revision:r.revision,status:r.status})).toThrow();expect(get(r.id).status).toBe("草稿");
});
test("independent decisions refill without inventing missing Plan commitments",async()=>{
 const {completeValidation}=await import("./qa-helpers.js");api.reset();completeValidation(api,"W-103");api.workCommand("W-103","accept","明确本地接受裁决",2);
 const rid=api.generatePeriodicReport({subjectKey:"human:H-lin",cadence:"日报",date:new Date().toISOString().slice(0,10),timezone:"UTC"}),before=structuredClone(get(rid));
 api.regenerateReportContent(rid,{revision:before.revision,sections:["completed"]});const r=get(rid);
 expect(r.content.completed.some(item=>item.fact_ref.kind==="DECISION")).toBe(true);expect(r.content.uncompleted).toEqual(before.content.uncompleted);expect(r.currentPlanBinding).toBeUndefined();expect(r.content.summary.uncompleted_count).toBeNull();
});

test("form-only refill clears replaced confirmations and save rejects changed facts atomically",()=>{
 const f=fixture(),stateBefore=structuredClone(api.read()),form=prepareReportRegeneration(api.read(),{...get(f.reportId),reviewedDetailKeys:["generated-uncompleted-1"]},["completed","uncompleted"]);
 expect(form.reviewedDetailKeys).toEqual([]);expect(api.read()).toEqual(stateBefore);
 api.update(s=>s.resources.plans.find(p=>p.id===f.planId).freeTaskFacts[0].evidence="故障注入：源证据改变");
 const before=structuredClone(api.read());expect(()=>api.saveResource("reports",form)).toThrow("重填来源已变化");expect(api.read()).toEqual(before);
});
test("manual calibration after form refill can save while preserving unselected old next binding",()=>{
 const f=fixture(),pid=f.report.nextPlanBinding.planId,p=api.read().resources.plans.find(p=>p.id===pid);api.savePeriodPlan(pid,{simulateAgent:true,items:p.items.map(i=>({...i,title:"下期源升版"}))},p.revision);
 const form=prepareReportRegeneration(api.read(),get(f.reportId),["completed","uncompleted"]);form.content.completed[0].note="作者补充的来源说明";
 api.saveResource("reports",form);const r=get(f.reportId);expect(r.content.completed[0].note).toBe("作者补充的来源说明");expect(r.nextPlanBinding).toEqual(f.report.nextPlanBinding);expect(r.regenerationDraft).toBeUndefined();
});

test("successive section refills retain each unselected chapter original source fingerprint",()=>{
 const f=fixture(),first=prepareReportRegeneration(api.read(),get(f.reportId),["completed","uncompleted"]);
 const second=prepareReportRegeneration(api.read(),first,["next_plan"]);
 expect(second.regenerationDraft.sections).toEqual(["completed","uncompleted","next_plan"]);expect(second.regenerationDraft.sources.completed).toEqual(first.regenerationDraft.sources.completed);
 api.update(s=>s.resources.plans.find(p=>p.id===f.planId).freeTaskFacts[0].evidence="故障注入：先前完成来源改变");
 const third=prepareReportRegeneration(api.read(),second,["uncompleted"]);expect(third.regenerationDraft.sources.completed).toEqual(first.regenerationDraft.sources.completed);
 const before=structuredClone(api.read());for(const form of [second,third]){expect(()=>api.saveResource("reports",form)).toThrow("重填来源已变化");expect(api.read()).toEqual(before);}
 const refreshed=prepareReportRegeneration(api.read(),third,["completed"]);api.saveResource("reports",refreshed);expect(get(f.reportId).revision).toBe(f.report.revision+1);
});

test("chapter source validation does not depend on the order of successive selections",()=>{
 for(const order of [[["next_plan"],["completed","uncompleted"]],[["uncompleted"],["next_plan"],["completed"]],[["completed","uncompleted"],["next_plan"]]]){
  const f=fixture();let form=get(f.reportId);for(const selection of order)form=prepareReportRegeneration(api.read(),form,selection);
  expect(new Set(form.regenerationDraft.sections)).toEqual(new Set(["completed","uncompleted","next_plan"]));api.saveResource("reports",form);
  expect(get(f.reportId).content.completed).toHaveLength(1);expect(get(f.reportId).content.uncompleted).toHaveLength(1);expect(get(f.reportId).content.retrospective).toEqual(f.report.content.retrospective);
 }
});
