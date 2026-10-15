import { beforeEach, expect, test } from "bun:test";
import { prepareStructuredReport } from "./report-lifecycle-fixtures";
import { reportCurrentPlan, reportCurrentPlanBinding, planReportFacts } from "../src/plans";
import { reportContentFacts, reportContentCheck } from "../src/report-content";
if (!globalThis.localStorage) { const entries=new Map();globalThis.localStorage={getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)}; }
const api=await import("../src/store.js?free-task-reports");
const get=id=>api.read().resources.reports.find(r=>r.id===id);
const save=(rid,patch)=>api.saveResource("reports",{...get(rid),...patch});
function fixture(){
 const rid=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"周报",date:new Date().toISOString().slice(0,10),timezone:"UTC"});
 prepareStructuredReport(api,rid);const pid=reportCurrentPlan(api.read(),get(rid)).id,plan=()=>api.read().resources.plans.find(p=>p.id===pid);
 const done={...plan().items[0],id:"FREE-DONE",title:"完成比较调研并保留结论"};
 api.savePeriodPlan(pid,{simulateAgent:true,items:[...plan().items,done]},plan().revision);
 api.submitPeriodPlan(pid,plan().revision,true);
 api.decidePeriodPlan(pid,{action:"confirm",revision:plan().revision,submissionRevision:plan().submission.revision,contentHash:plan().submission.contentHash,reason:"明确本地模拟：核对固定计划"});
 api.recordPlanFreeTask(pid,done.id,{revision:plan().revision,simulateAgent:true,evidence:"明确本地模拟：比较结论与来源已保存"});
 save(rid,{currentPlanBinding:reportCurrentPlanBinding(api.read(),get(rid),pid,plan().revision)});
 const fact=structuredClone(plan().freeTaskFacts[0]),content=structuredClone(get(rid).content);
 content.completed=[{key:"free-completed",title:done.title,goal_id:done.goalId,kr_id:"",plan_item_id:done.id,fact_ref:{kind:"FREE_TASK_EVIDENCE",id:done.id,event_id:fact.id,version:null},note:""}];
 content.summary.completed_count=1;content.summary.accepted_work_items=0;
 save(rid,{content});return {rid,pid,fact,done};
}
const submit=rid=>api.resourceCommand("reports",rid,"已提交","明确本地 Agent 模拟上报","",{simulateAgent:true,revision:get(rid).revision,status:get(rid).status});
beforeEach(()=>api.reset());
test("genuine confirmed Plan completion becomes typed report evidence without invented work acceptance",()=>{
 const {rid,pid,fact}=fixture(),before=structuredClone(api.read());
 expect(planReportFacts(api.read(),api.read().resources.plans.find(p=>p.id===pid)).factKeys).toContain(fact.id);
 expect(reportContentFacts(api.read(),get(rid))).toEqual([fact]);expect(reportContentCheck(api.read(),get(rid))).toMatchObject({issues:[],facts:[fact]});
 const evidence=api.reportEvidence(api.read(),get(rid));expect(evidence.errors).toEqual([]);expect(evidence.structuredFacts).toEqual([fact]);expect(evidence.totals.completed).toBe(0);expect(api.read()).toEqual(before);
});
test("typed Plan fact rejects malformed provenance, changed definition, calendar and cutoff without mutation",()=>{
 const {rid,pid,fact}=fixture(),base=structuredClone(api.read()),report=get(rid);
 for(const patch of [{kind:"WORK_ITEM"},{actor:null},{actor:{...fact.actor,id:"other"}},{actor:{...fact.actor,kind:"Human"}},{actor:{...fact.actor,recordedBy:""}},{evidence:""},{subjectKey:"agent:other"},{itemDefinition:"different commitment"},{time:"invalid"},{time:"2099-01-01T00:00:00Z"}]){
  const state=structuredClone(base);state.resources.plans.find(p=>p.id===pid).freeTaskFacts=[{...fact,...patch}];
  const before=structuredClone(state);expect(reportContentCheck(state,report).issues.some(i=>i.code==="B1")).toBe(true);expect(state).toEqual(before);
 }
 for(const patch of [{plan_item_id:""},{fact_ref:{...report.content.completed[0].fact_ref,version:1}},{fact_ref:{...report.content.completed[0].fact_ref,id:"other"}},{fact_ref:{...report.content.completed[0].fact_ref,event_id:"other"}}]){
  const candidate={...report,content:{...report.content,completed:[{...report.content.completed[0],...patch}]}};
  expect(reportContentCheck(base,candidate).issues.some(i=>i.code==="B1")).toBe(true);
 }
 expect(reportContentCheck(base,{...report,currentPlanBinding:{...report.currentPlanBinding,confirmed:false}}).issues.some(i=>i.code==="B1")).toBe(true);
 expect(reportContentCheck(base,{...report,content:{...report.content,summary:{...report.content.summary,accepted_work_items:1}}}).issues.some(i=>i.code==="B2")).toBe(true);
});
test("same calendar labels cannot substitute for both Plan and report timezone checks",()=>{
 const {rid,pid,fact}=fixture(),base=structuredClone(api.read()),report=structuredClone(get(rid));
 for(const [planZone,reportZone,expected] of [["UTC","UTC",true],["UTC","America/Los_Angeles",false],["America/Los_Angeles","UTC",false]]){
  const state=structuredClone(base),candidate=structuredClone(report);candidate.timezone=reportZone;candidate.currentPlanBinding.snapshot.timezone=planZone;
  state.resources.plans.find(p=>p.id===pid).freeTaskFacts=[{...fact,time:`${report.periodStart}T00:30:00Z`}];
  expect(reportContentFacts(state,candidate).some(f=>f.id===fact.id)).toBe(expected);
 }
});
test("typed identities preserve work and FREE events sharing an ID, while rejecting duplicates",()=>{
 const {rid,fact}=fixture(),state=structuredClone(api.read()),report=structuredClone(get(rid)),work=state.work.find(w=>w.id==="W-103");
 work.acceptanceFacts=[{id:fact.id,workId:work.id,version:work.version,time:fact.time,subjectKeys:[report.subjectKey],executorIds:["agent:atlas"],workSnapshot:{title:work.title}}];
 report.references=[{kind:"work",id:work.id,version:work.version,acceptanceFactIds:[fact.id]}];
 report.content.completed.push({key:"accepted-work",title:work.title,fact_ref:{kind:"WORK_ITEM",id:work.id,event_id:fact.id,version:work.version}});
 report.content.summary.completed_count=2;report.content.summary.accepted_work_items=1;
 expect(reportContentFacts(state,report)).toHaveLength(2);expect(reportContentCheck(state,report).issues).toEqual([]);
 report.content.completed.push({...report.content.completed[0],key:"duplicate"});expect(reportContentCheck(state,report).issues.some(i=>i.code==="B2")).toBe(true);
});
test("fixed report keeps original FREE evidence after Plan deletion and roster changes",()=>{
 const {rid,pid,fact}=fixture();submit(rid);expect(get(rid).submission.factSnapshot.structuredFacts).toEqual([fact]);
 const original=structuredClone(get(rid).submission),state=structuredClone(api.read());state.resources.plans=state.resources.plans.filter(p=>p.id!==pid);state.agents=[];
 expect(reportContentFacts(state,get(rid))).toEqual([fact]);expect(reportContentCheck(state,get(rid)).issues).toEqual([]);expect(get(rid).submission).toEqual(original);
});
test("missing corrupt or post-submission frozen evidence never falls back to valid live Plan",()=>{
 const {rid,fact}=fixture();submit(rid);const base=structuredClone(get(rid));
 for(const structuredFacts of [undefined,null,{},[null],[],[{...fact,evidence:""}],[{...fact,actor:{...fact.actor,id:"other"}}],[{...fact,time:"2099-01-01T00:00:00Z"}]]){
  const report=structuredClone(base);report.submission.factSnapshot.structuredFacts=structuredFacts;const before=structuredClone(report);
  expect(reportContentCheck(api.read(),report).issues.some(i=>i.code==="B1")).toBe(true);expect(report).toEqual(before);
 }
 const report=structuredClone(base);report.submission.time="invalid";expect(reportContentCheck(api.read(),report).issues.some(i=>i.code==="B1")).toBe(true);
});
test("as-of cutoff rejects later evidence even inside the same valid calendar period",()=>{
 const {rid,pid,fact}=fixture();
 const live=structuredClone(api.read());live.resources.plans.find(p=>p.id===pid).freeTaskFacts=[{...fact,time:new Date(Date.now()+60000).toISOString()}];
 expect(reportContentFacts(live,get(rid))).toEqual([]);
 submit(rid);const report=structuredClone(get(rid));
 report.submission.factSnapshot.structuredFacts=[{...fact,time:new Date(Date.parse(report.submission.time)+1000).toISOString()}];
 expect(reportContentFacts(api.read(),report)).toEqual([]);expect(reportContentCheck(api.read(),report).issues.some(i=>i.code==="B1")).toBe(true);
});
