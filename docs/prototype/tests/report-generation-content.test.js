import { beforeEach, expect, test } from "bun:test";
import { freeTaskBrowserFixture } from "./report-lifecycle-fixtures";
import { reportGeneratedContent, adoptReportGeneratedContent, reportContentCheck, reportNextPlanItems } from "../src/report-content";
if (!globalThis.localStorage) { const entries=new Map();globalThis.localStorage={getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)}; }
const api=await import("../src/store.js?generated-report-content");
const get=rid=>api.read().resources.reports.find(r=>r.id===rid);
const save=(rid,patch)=>api.saveResource("reports",{...get(rid),...patch});
function fixture(){const s=freeTaskBrowserFixture(api),ids=s.testFreeTask,original=structuredClone(get(ids.reportId));return {...ids,original,options:{subjectKey:original.subjectKey,cadence:original.cadence,date:original.periodStart,timezone:original.timezone}};}
function fresh(f){api.update(s=>s.resources.reports=s.resources.reports.filter(r=>r.id!==f.reportId));return api.generatePeriodicReport(f.options);}
beforeEach(()=>api.reset());
test("new report fills real done and unfinished Plan items without inventing acceptance or confirmations",()=>{
 const f=fixture(),rid=fresh(f),report=get(rid);
 expect(report.content.completed).toHaveLength(1);expect(report.content.completed[0]).toMatchObject({plan_item_id:f.itemId,fact_ref:{kind:"FREE_TASK_EVIDENCE",id:f.itemId,event_id:f.eventId,version:null}});
 expect(report.content.uncompleted).toHaveLength(1);expect(report.content.uncompleted[0].detail_confirmation).toBeNull();expect(report.currentPlanBinding.confirmed).toBe(true);
 expect(report.content.summary).toMatchObject({completed_count:1,accepted_work_items:0,uncompleted_count:1,cost_micros:null,kr_progress:[]});
 expect(report.content.retrospective.problems).toEqual([]);expect(report.content.next_plan).toEqual(reportNextPlanItems(report.nextPlanBinding));
 expect(reportContentCheck(api.read(),report).issues.some(i=>i.code==="B1"||i.code==="B2")).toBe(false);
 const before=structuredClone(report);expect(api.generatePeriodicReport(f.options)).toBe(rid);expect(get(rid)).toEqual(before);
});
test("pending replan uses old confirmed commitment while missing corrupt or foreign Plan never invents a list",()=>{
 const f=fixture(),pid=f.planId,plan=()=>api.read().resources.plans.find(p=>p.id===pid);
 api.savePeriodPlan(pid,{simulateAgent:true,reason:"明确本地测试：下游需求变化",items:plan().items.map(i=>({...i,title:"尚未确认的新标题"}))},plan().revision);
 const rid=fresh(f);expect(get(rid).content.completed[0].title).not.toBe("尚未确认的新标题");expect(get(rid).currentPlanBinding.snapshot.items[0].title).toBe(f.original.currentPlanBinding.snapshot.items[0].title);
 for(const mutate of [s=>s.resources.plans=[],s=>s.resources.plans.find(p=>p.id===pid).confirmedSnapshot={},s=>s.resources.plans.find(p=>p.id===pid).tenantId="foreign"]){
  const state=structuredClone(api.read());mutate(state);const before=structuredClone(state),p=reportGeneratedContent(state,get(rid));expect(p.content.uncompleted).toEqual([]);expect(p.content.completed).toEqual([]);expect(p.issues.length).toBeGreaterThan(0);expect(state).toEqual(before);
 }
});
test("off-plan accepted work retains each fixed event version even without a confirmed Plan",()=>{
 const f=fixture(),state=structuredClone(api.read()),work=state.work.find(w=>w.id==="W-103"),time=new Date(Date.now()-10000).toISOString();
 state.resources.plans=[];work.acceptanceFacts=[{id:"old-event",workId:work.id,version:1,time,subjectKeys:[f.original.subjectKey],executorIds:["agent:atlas"],workSnapshot:{title:"原版已接受标题"}},{id:"new-event",workId:work.id,version:2,time,subjectKeys:[f.original.subjectKey],executorIds:["agent:atlas"],workSnapshot:{title:"新版已接受标题"}}];
 const p=reportGeneratedContent(state,f.original);expect(p.content.completed.map(i=>i.fact_ref.version).sort()).toEqual([1,2]);expect(p.content.completed.map(i=>i.title).sort()).toEqual(["原版已接受标题","新版已接受标题"].sort());expect(p.content.completed.every(i=>!i.plan_item_id)).toBe(true);expect(p.content.summary.accepted_work_items).toBe(1);expect(p.content.summary.completed_count).toBe(2);expect(p.content.summary.uncompleted_count).toBeNull();
 for(const patch of [{id:3},{version:null},{version:-1}]){work.acceptanceFacts=[{...work.acceptanceFacts[0],...patch}];const result=reportGeneratedContent(state,f.original);expect(result.content.completed).toEqual([]);expect(result.issues.length).toBeGreaterThan(0);}
});
test("multiple accepted versions associate one Plan item once and keep other events explicitly off-plan",()=>{
 const f=fixture(),plan=()=>api.read().resources.plans.find(p=>p.id===f.planId),goalId=plan().items[0].goalId;
 api.savePeriodPlan(f.planId,{simulateAgent:true,reason:"明确本地测试：加入真实工作承诺",items:[...plan().items,{id:"GEN-WORK",kind:"WORK_ITEM",workId:"W-104",title:"本期交付工作",goalId,weight:1}]},plan().revision);
 api.decidePeriodPlan(f.planId,{action:"confirm",revision:plan().revision,submissionRevision:plan().submission.revision,contentHash:plan().submission.contentHash,reason:"明确本地测试：Human 确认工作承诺"});
 api.update(s=>{const w=s.work.find(w=>w.id==="W-104");w.acceptanceFacts=[1,2].map(version=>({id:`synthetic-generation-event-${version}`,workId:w.id,version,time:new Date(Date.now()-(3-version)*10000).toISOString(),subjectKeys:[f.original.subjectKey],executorIds:["agent:atlas"],workSnapshot:{title:`明确合成测试固定版本 ${version}`}}));});
 const rid=fresh(f),report=get(rid),work=report.content.completed.filter(i=>i.fact_ref.kind==="WORK_ITEM");
 expect(work).toHaveLength(2);expect(work.filter(i=>i.plan_item_id==="GEN-WORK")).toHaveLength(1);expect(work.find(i=>i.plan_item_id==="GEN-WORK").fact_ref.version).toBe(2);
 expect(reportContentCheck(api.read(),report).issues.some(i=>i.code==="B1"||i.code==="B2")).toBe(false);
});
test("refresh preserves authored content and selected refs; explicit adoption adds facts with no reason confirmation",()=>{
 const f=fixture(),rid=f.reportId;save(rid,{content:{...get(rid).content,summary:{...get(rid).content.summary,narrative:"人工保留的周期解释"}}});
 const before=structuredClone(get(rid));api.generatePeriodicReport(f.options);const report=get(rid);
 for(const key of ["content","references","currentPlanBinding","nextPlanBinding"])expect(report[key]).toEqual(before[key]);
 expect(report.suggestedContent.completed).toHaveLength(1);expect(report.generationHistory.at(-1).suggestedContent).toEqual(before.suggestedContent);
 const stateBefore=structuredClone(api.read()),form=structuredClone(report),adopted=adoptReportGeneratedContent(api.read(),form);
 expect(api.read()).toEqual(stateBefore);expect(form).toEqual(report);expect(adopted.content.completed).toHaveLength(1);expect(adopted.content.summary.narrative).toBe("人工保留的周期解释");expect(adopted.content.uncompleted[0]).toEqual(before.content.uncompleted[0]);expect(adopted.content.retrospective).toEqual(before.content.retrospective);
 save(rid,adopted);expect(reportContentCheck(api.read(),get(rid)).issues).toEqual([]);
 const stable=structuredClone(get(rid));expect(adoptReportGeneratedContent(api.read(),stable).content).toEqual(stable.content);
});
test("adoption rejects changed evidence and report revision atomically",()=>{
 const f=fixture();api.generatePeriodicReport(f.options);const form=structuredClone(get(f.reportId));
 api.update(s=>s.resources.plans.find(p=>p.id===f.planId).freeTaskFacts[0].evidence="测试故障注入：原证据被修改");
 const before=structuredClone(api.read()),original=structuredClone(form);expect(()=>adoptReportGeneratedContent(api.read(),form)).toThrow("来源已变化");expect(api.read()).toEqual(before);expect(form).toEqual(original);
 api.generatePeriodicReport(f.options);expect(()=>adoptReportGeneratedContent(api.read(),form)).toThrow("报告已变化");
});
test("old unfinished item becoming complete is a visible atomic conflict, never silently removed",()=>{
 const f=fixture(),rid=f.reportId,content=structuredClone(get(rid).content);
 content.uncompleted.push({key:"author-old-pending",title:"作者之前尚未完成的调研",plan_item_id:f.itemId,reason:"OTHER",detail:"作者此前解释不可丢弃",carry_over:"CARRY_TO_NEXT"});save(rid,{content,reviewedDetailKeys:["author-old-pending"]});api.generatePeriodicReport(f.options);
 const form=structuredClone(get(rid)),before=structuredClone(api.read());expect(()=>adoptReportGeneratedContent(api.read(),form)).toThrow("原未完成条目已有完成证据");expect(api.read()).toEqual(before);expect(form).toEqual(get(rid));
 const edited={...form,content:{...form.content,uncompleted:form.content.uncompleted.filter(i=>i.key!=="author-old-pending")}};
 const adopted=adoptReportGeneratedContent(api.read(),edited);expect(adopted.content.completed).toHaveLength(1);save(rid,adopted);expect(get(rid).history.some(h=>h.content?.uncompleted.some(i=>i.detail==="作者此前解释不可丢弃"))).toBe(true);
});
test("off-plan unfinished work also conflicts with newly accepted work and leaves every field unchanged",()=>{
 const f=fixture(),rid=f.reportId,content=structuredClone(get(rid).content);
 content.uncompleted.push({key:"offplan",title:"作者声明未完成",plan_item_id:"",work_item_id:"W-103",reason:"OTHER",detail:"保留作者原始解释",carry_over:"CARRY_TO_NEXT"});save(rid,{content,reviewedDetailKeys:["offplan"]});
 api.update(s=>{const w=s.work.find(w=>w.id==="W-103");w.acceptanceFacts=[{id:"synthetic-offplan-accepted",workId:w.id,version:w.version,time:new Date(Date.now()-1000).toISOString(),subjectKeys:[f.original.subjectKey],executorIds:["agent:atlas"],workSnapshot:{title:"明确合成测试接受事实"}}];});
 api.generatePeriodicReport(f.options);const form=structuredClone(get(rid)),before=structuredClone(api.read());expect(()=>adoptReportGeneratedContent(api.read(),form)).toThrow("原未完成条目已有完成证据");expect(api.read()).toEqual(before);expect(form).toEqual(get(rid));
});
test("prefilled reasons use current evidence without turning a QA boolean or historical status into proof",()=>{
 const f=fixture(),state=structuredClone(api.read()),plan=state.resources.plans.find(p=>p.id===f.planId),work=state.work.find(w=>w.id==="W-104");
 plan.confirmedSnapshot.items.push({id:"test-work",kind:"WORK_ITEM",title:"明确合成测试工作承诺",workId:work.id,weight:1});work.accepted=false;work.acceptanceFacts=[];work.qa=true;work.qaPlanId=null;work.status="待验收";
 const item=()=>reportGeneratedContent(state,f.original).content.uncompleted.find(i=>i.plan_item_id==="test-work");
 expect(item().detail).not.toContain("已验证，待裁决");expect(item().detail_confirmation).toBeNull();work.status="等待";expect(item().reason).toBe("BLOCKED_DEPENDENCY");
 work.rework={hold:{kind:"formal",reason:"质量返工停线"}};expect(item().reason).toBe("QUALITY_REWORK");
 const historical={...f.original,periodStart:"2026-09-07",periodEnd:"2026-09-13"};plan.periodStart=historical.periodStart;plan.periodEnd=historical.periodEnd;plan.confirmedSnapshot.periodStart=historical.periodStart;plan.confirmedSnapshot.periodEnd=historical.periodEnd;
 const old=reportGeneratedContent(state,historical).content.uncompleted.find(i=>i.plan_item_id==="test-work");expect(old.reason).toBe("OTHER");expect(old.detail).not.toContain("返工停线");
});
test("generation never refreshes fixed submission or its suggestions",()=>{
 const f=fixture();api.generatePeriodicReport(f.options);save(f.reportId,adoptReportGeneratedContent(api.read(),get(f.reportId)));
 const report=get(f.reportId);api.resourceCommand("reports",f.reportId,"已提交","明确本地测试：模拟 Agent 上报","",{simulateAgent:true,revision:report.revision,status:report.status});
 const fixed=structuredClone(get(f.reportId));api.update(s=>s.resources.plans.find(p=>p.id===f.planId).freeTaskFacts[0].evidence="测试故障注入：源证据后来变化");expect(api.generatePeriodicReport(f.options)).toBe(f.reportId);expect(get(f.reportId)).toEqual(fixed);
});
