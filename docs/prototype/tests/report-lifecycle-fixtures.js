// Explicit synthetic scenarios for lifecycle tests, not product generation.
// A valid new-format report has real period Plans; no accepted work is invented.
import { emptyReportContent, reportDetailFingerprint } from "../src/report-content";
import { reportNextPlanBinding, reportCurrentPlanBinding, reportCurrentPlan, planPeriods } from "../src/plans";
import { reportPeriod, reportOwner, reportSubjects, periodicReportKey } from "../src/reporting";
export function legacyPeriodFixture(api, options) {
  const descriptor = { subjectKey: options.subjectKey, cadence: options.cadence, timezone: options.timezone, ...reportPeriod(options.cadence, options.date) };
  const found = api.read().resources.reports.find(r => periodicReportKey(api.read(), r) === periodicReportKey(api.read(), descriptor));
  if (found) return found.id;
  const id = `legacy-${api.read().resources.reports.length}-${options.subjectKey}`;
  const owner = reportOwner(api.read(), descriptor.subjectKey), subjectName = reportSubjects(api.read()).find(s => s.key === descriptor.subjectKey).name;
  api.update(s => s.resources.reports.push({ ...descriptor, id, name: "明确导入的旧格式测试报告", owner, subjectName, summary: "旧格式原正文", risk: "", references: [], status: "草稿", revision: 1, history: [] }));
  return id;
}
export function prepareStructuredReport(api, id, adoptedBinding) {
  const get = () => api.read().resources.reports.find(r => r.id === id);
  const report = get(), subjectKey = report.subjectKey, simulateAgent = subjectKey.startsWith("agent:");
  const goalId = `fixture-goal-${subjectKey}`, personId = subjectKey.split(":")[1];
  if (!api.read().resources.goals.some(g => g.id === goalId)) api.update(s => s.resources.goals.push({
    id: goalId, name: "测试场景：核对下游需求", subjectKey, ownerId: personId, ownerKind: simulateAgent ? "Agent" : "Human", status: "进行中", revision: 1, definitionRevision: 1, krs: [], alignments: [] }));
  const item = { id: `pending-${id}`, kind: "FREE_TASK", title: "测试场景：调研仍待完成", goalId, freeCategory: "LEARNING", criteria: "提交调研依据供下游核对", weight: 1 };
  const periodKind = Object.keys(planPeriods).find(k => planPeriods[k] === report.cadence);
  const currentId = api.openPeriodPlan({ subjectKey, periodKind, date: report.periodStart, timezone: report.timezone, simulateAgent });
  let current = api.read().resources.plans.find(p => p.id === currentId);
  if (!current.items.some(i => i.id === item.id)) api.savePeriodPlan(currentId, { simulateAgent, items: [...current.items, item] }, current.revision);
  let binding = adoptedBinding;
  if (!binding) {
    const nextId = api.openReportNextPlan(id, get().revision, simulateAgent);
    let next = api.read().resources.plans.find(p => p.id === nextId);
    // Explicit test author calibrates the automatically drafted work against
    // this fixture's synthetic goal; production never assigns an arbitrary goal.
    api.savePeriodPlan(nextId, { simulateAgent, items: next.items.length
      ? next.items.map(source => source.goalId ? source : { ...source, goalId, krId: "" })
      : [{ ...item, id: `next-${id}` }] }, next.revision);
    next = api.read().resources.plans.find(p => p.id === nextId);
    binding = reportNextPlanBinding(api.read(), get(), nextId, next.revision);
  }
  const content = emptyReportContent(binding);
  content.summary.narrative = "测试场景本期没有接受成果，调研工作仍待完成。";
  content.uncompleted = [{ key: item.id, title: item.title, plan_item_id: item.id, reason: "WAITING_CLIENT", detail: "测试场景等待下游提供调研资料", carry_over: "CARRY_TO_NEXT" }];
  content.retrospective = { went_well: ["已明确调研验收依据"], problems: ["下游资料未就绪"], root_causes: ["资料准备晚于本期计划"], improvements: [{ text: "提前确认资料到位时间", propose_memory: false }], ai_drafted: false };
  current = api.read().resources.plans.find(p => p.id === currentId);
  const currentBinding = reportCurrentPlanBinding(api.read(), get(), currentId, current.revision);
  api.saveResource("reports", { ...get(), content, nextPlanBinding: binding, currentPlanBinding: currentBinding, reviewedDetailKeys: [item.id], ...(get().content == null ? { migrateLegacyContent: true } : {}) });
  return get();
}
// Genuine local Plan/report commands build the browser fixture. Only source
// disappearance is injected; this never certifies production Agent execution.
export function currentPlanBrowserFixture(api) {
 api.reset();
 const ids = {};
 for (const [kind,date] of [["DRAFT","2026-10-14"],["FIXED","2026-09-07"],["CORRUPT","2026-08-03"]]) {
  const rid=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"周报",date,timezone:"UTC"});
  prepareStructuredReport(api,rid);const getReport=()=>api.read().resources.reports.find(r=>r.id===rid);
  const pid=reportCurrentPlan(api.read(),getReport()).id,getPlan=()=>api.read().resources.plans.find(p=>p.id===pid);
  api.submitPeriodPlan(pid,getPlan().revision,true);let p=getPlan();
  api.decidePeriodPlan(pid,{revision:p.revision,submissionRevision:p.submission.revision,contentHash:p.submission.contentHash,action:"confirm",reason:"明确本地测试：Human 核对承诺"});
  api.saveResource("reports",{...getReport(),currentPlanBinding:reportCurrentPlanBinding(api.read(),getReport(),pid,getPlan().revision)});
  if(kind==="FIXED") {
   api.resourceCommand("reports",rid,"已提交","明确本地 Agent 模拟上报测试","",{simulateAgent:true,revision:getReport().revision,status:getReport().status});
   const nextId=getReport().nextPlanBinding.planId;
   api.update(s=>{s.resources.plans=s.resources.plans.filter(plan=>plan.id!==pid&&plan.id!==nextId);});
  } else {
   p=getPlan();api.savePeriodPlan(pid,{simulateAgent:true,reason:"明确本地测试：下游要求变化",items:p.items.map(item=>({...item,title:"待确认的新调研范围"}))},p.revision);
  }
  ids[kind]={reportId:rid,planId:pid};
 }
 const s=structuredClone(api.read());
 // Deliberately corrupted imported local report; never a valid production submission.
 const corrupt=s.resources.reports.find(r=>r.id===ids.CORRUPT.reportId);corrupt.content.uncompleted[0].plan_item_id="NONEXISTENT-PLAN-ITEM";
 corrupt.content.uncompleted[0].detail_confirmation.fingerprint=reportDetailFingerprint(corrupt.content.uncompleted[0]);
 s.testCurrentPlans=ids;return s;
}
// Synthetic accepted event is explicitly injected for UI correspondence testing.
// It proves the local interaction only, never production execution or acceptance.
export function planLinkBrowserFixture(api) {
 api.reset();
 const rid=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"周报",date:"2026-10-14",timezone:"UTC"});
 prepareStructuredReport(api,rid);
 const report=()=>api.read().resources.reports.find(r=>r.id===rid), pid=reportCurrentPlan(api.read(),report()).id;
 const plan=()=>api.read().resources.plans.find(p=>p.id===pid), goalId=plan().items[0].goalId;
 api.savePeriodPlan(pid,{simulateAgent:true,items:[...plan().items,
  {id:"LINK-DONE",kind:"WORK_ITEM",title:"测试固定已接受工作",workId:"W-103",goalId,weight:1},
  {id:"LINK-PENDING",kind:"WORK_ITEM",title:"测试固定未完成工作",workId:"W-104",goalId,weight:1},
 ]},plan().revision);
 api.saveResource("reports",{...report(),currentPlanBinding:reportCurrentPlanBinding(api.read(),report(),pid,plan().revision)});
 const s=structuredClone(api.read()),work=s.work.find(w=>w.id==="W-103");
 work.acceptanceFacts=[{id:"LINK-SYNTHETIC-ACCEPTED",workId:work.id,version:work.version,time:"2026-10-12T02:00:00Z",project:work.project,subjectKeys:["agent:atlas",`project:${work.project}`,"tenant:studio"],executorIds:["agent:atlas"],workSnapshot:{title:work.title,plan:true,quality:"通过"}}];
 s.testPlanLink={reportId:rid,planId:pid};return s;
}
// Genuine local Plan confirmation and evidence commands, explicitly Agent simulation.
export function freeTaskBrowserFixture(api) {
 api.reset();const rid=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"周报",date:new Date().toISOString().slice(0,10),timezone:"UTC"});
 prepareStructuredReport(api,rid);const report=()=>api.read().resources.reports.find(r=>r.id===rid),pid=reportCurrentPlan(api.read(),report()).id,plan=()=>api.read().resources.plans.find(p=>p.id===pid);
 const done={...plan().items[0],id:"FREE-BROWSER-DONE",title:"本地模拟：已完成的下游比较调研"};
 api.savePeriodPlan(pid,{simulateAgent:true,items:[...plan().items,done]},plan().revision);
 api.submitPeriodPlan(pid,plan().revision,true);
 api.decidePeriodPlan(pid,{action:"confirm",revision:plan().revision,submissionRevision:plan().submission.revision,contentHash:plan().submission.contentHash,reason:"明确本地测试：Human 核对调研承诺"});
 api.recordPlanFreeTask(pid,done.id,{revision:plan().revision,simulateAgent:true,evidence:"明确本地模拟：下游方案比较结论与来源已记录"});
 api.saveResource("reports",{...report(),currentPlanBinding:reportCurrentPlanBinding(api.read(),report(),pid,plan().revision)});
 const s=structuredClone(api.read());s.testFreeTask={reportId:rid,planId:pid,itemId:done.id,eventId:plan().freeTaskFacts[0].id};return s;
}
// Genuine local source facts; new-report generation versus preservation/adoption.
export function generatedContentBrowserFixture(api, fresh = true) {
 const fixture=freeTaskBrowserFixture(api),ids=fixture.testFreeTask,old=structuredClone(api.read().resources.reports.find(r=>r.id===ids.reportId));
 const options={subjectKey:old.subjectKey,cadence:old.cadence,date:old.periodStart,timezone:old.timezone};
 if(fresh) api.update(s=>{s.resources.reports=s.resources.reports.filter(r=>r.id!==ids.reportId);});
 const rid=api.generatePeriodicReport(options),report=api.read().resources.reports.find(r=>r.id===rid);
 if(fresh) api.saveResource("reports",{...report,nextPlanBinding:old.nextPlanBinding,content:{...report.content,next_plan:emptyReportContent(old.nextPlanBinding).next_plan}});
 const s=structuredClone(api.read());s.testGeneratedContent={...ids,reportId:rid,fresh};return s;
}

// Genuine local prototype commands; QA evidence remains an explicit test simulation.
export async function operationReportBrowserFixture(api) {
 const { completeValidation } = await import("./qa-helpers.js");
 api.reset();completeValidation(api,"W-103");api.workCommand("W-103","accept","明确本地模拟：固定成果接受",2);api.workCommand("W-103","deliver","明确本地模拟：固定包发送",2);
 const rid=api.generatePeriodicReport({subjectKey:"human:H-lin",cadence:"日报",date:new Date().toISOString().slice(0,10),timezone:"UTC"});prepareStructuredReport(api,rid);
 const state=structuredClone(api.read());state.testOperationFactsReportId=rid;return state;
}

export function regenerationBrowserFixture(api) {
 const fixture=freeTaskBrowserFixture(api),ids=fixture.testFreeTask,report=()=>api.read().resources.reports.find(r=>r.id===ids.reportId);
 const original=structuredClone(report()),p=api.read().resources.plans.find(p=>p.id===ids.planId);
 api.recordPlanFreeTask(ids.planId,original.content.uncompleted[0].plan_item_id,{revision:p.revision,simulateAgent:true,evidence:"明确本地模拟：原未完成调研已完成"});
 const next=api.read().resources.plans.find(p=>p.id===original.nextPlanBinding.planId);
 api.savePeriodPlan(next.id,{simulateAgent:true,items:next.items.map(item=>({...item,title:"本地模拟：更新后的下期承诺"}))},next.revision);
 const state=structuredClone(api.read());state.testRegeneration={...ids,original,nextRevision:next.revision+1};return state;
}
