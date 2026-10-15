import { prepareStructuredReport } from "./report-lifecycle-fixtures";
import { reportNextPlanItems } from "../src/report-content";
import { beforeEach, expect, test } from "bun:test";
import { reportNextPlanDescriptor, reportNextPlan, reportNextPlanBinding, reportNextPlanCheck } from "../src/plans.js";
import { reportSubmissionContent } from "../src/report-authorization.js";
if (!globalThis.localStorage) { const entries = new Map(); globalThis.localStorage = { getItem: key => entries.get(key) || null, setItem: (key,value) => entries.set(key,value), removeItem: key => entries.delete(key) }; }
const api = await import("../src/store.js?report-next-plan");
const getReport = id => api.read().resources.reports.find(r => r.id === id);
const getPlan = id => api.read().resources.plans.find(p => p.id === id);
const generate = (patch = {}) => api.generatePeriodicReport({ subjectKey: "agent:atlas", cadence: "周报", date: "2026-10-14", timezone: "Asia/Singapore", ...patch });
const item = (patch = {}) => ({ id: "PI-next", kind: "FREE_TASK", title: "研究下游需求", goalId: "G-next", freeCategory: "LEARNING", criteria: "交付有证据的调研结论", weight: 1, ...patch });
const savePlan = (id, items = [item()]) => api.savePeriodPlan(id, { items, simulateAgent: true }, getPlan(id).revision);
const bind = id => { const report = getReport(id), plan = reportNextPlan(api.read(),report); const binding=reportNextPlanBinding(api.read(),report,plan.id,plan.revision); api.saveResource("reports", { ...report, nextPlanBinding:binding,content:{...report.content,next_plan:reportNextPlanItems(binding)} }); };
const submitReport = id => api.resourceCommand("reports",id,"已提交","核对固定计划，未批准计划","",{simulateAgent:true,revision:getReport(id).revision,status:getReport(id).status});
beforeEach(() => { api.reset(); api.update(s => s.resources.goals.push({ id: "G-next", subjectKey: "agent:atlas", ownerId: "atlas", ownerKind: "Agent", name: "研究目标", status: "进行中", revision: 1, definitionRevision: 1, krs: [], alignments: [] })); });
test("every next cadence uses actual calendar boundaries across leap months, DST and year ends", () => {
  const s = api.read();
  for (const [cadence,start,end,nextStart,nextEnd] of [
    ["日报","2026-11-01","2026-11-01","2026-11-02","2026-11-02"],
    ["周报","2026-12-28","2027-01-03","2027-01-04","2027-01-10"],
    ["月报","2024-01-01","2024-01-31","2024-02-01","2024-02-29"],
    ["季报","2026-10-01","2026-12-31","2027-01-01","2027-03-31"],
    ["年报","2026-01-01","2026-12-31","2027-01-01","2027-12-31"],
  ]) for (const timezone of ["UTC","America/New_York","Pacific/Kiritimati"]) {
    expect(reportNextPlanDescriptor(s,{subjectKey:"agent:atlas",cadence,periodStart:start,periodEnd:end,timezone})).toMatchObject({periodStart:nextStart,periodEnd:nextEnd,timezone});
  }
  expect(() => reportNextPlanDescriptor(s,{})).toThrow("旧报告");
  expect(() => reportNextPlanDescriptor(s,{subjectKey:"project:launch",cadence:"周报",periodStart:"2026-10-12",periodEnd:"2026-10-18",timezone:"UTC"})).toThrow("汇总成员计划");
});
test("open is authorized and idempotent, preserves existing original timezone and all authored items", () => {
  const pid = api.openPeriodPlan({ subjectKey:"agent:atlas",periodKind:"WEEK",date:"2026-10-19",timezone:"UTC",simulateAgent:true });
  savePlan(pid); const before = structuredClone(getPlan(pid)), count = api.read().resources.plans.length;
  const rid = generate(), report = getReport(rid);
  expect(() => api.openReportNextPlan(rid,report.revision,false)).toThrow("本地模拟");
  expect(api.openReportNextPlan(rid,report.revision,true)).toBe(pid);
  expect(api.openReportNextPlan(rid,report.revision,true)).toBe(pid);
  expect(getPlan(pid)).toEqual(before); expect(api.read().resources.plans).toHaveLength(count);
  bind(rid); expect(getReport(rid).nextPlanBinding.snapshot.timezone).toBe("UTC");
  expect(reportNextPlanCheck(api.read(),getReport(rid)).warnings.some(w=>w.includes("原时区 UTC"))).toBe(true);
  api.update(s=>{s.role="external";}); const denied=structuredClone(api.read());
  expect(()=>api.openReportNextPlan(rid,report.revision,true)).toThrow();expect(api.read()).toEqual(denied);
});
test("foreign subject, other period, forged snapshot, stale revision and changed dependencies reject atomically", () => {
  const rid=generate(), report=getReport(rid), pid=api.openReportNextPlan(rid,report.revision,true);savePlan(pid);
  const other=api.openPeriodPlan({subjectKey:"human:H-lin",periodKind:"WEEK",date:"2026-10-19",timezone:"UTC"});
  const later=api.openPeriodPlan({subjectKey:"agent:atlas",periodKind:"WEEK",date:"2026-10-26",timezone:"UTC",simulateAgent:true});
  for (const wrong of [other,later]) expect(()=>reportNextPlanBinding(api.read(),report,wrong,getPlan(wrong).revision)).toThrow("同一主体");
  const good=reportNextPlanBinding(api.read(),report,pid,getPlan(pid).revision);
  const forged=structuredClone(good);forged.snapshot.items[0].title="伪造承诺";
  let before=structuredClone(api.read());expect(()=>api.saveResource("reports",{...report,nextPlanBinding:forged,content:{...report.content,next_plan:reportNextPlanItems(forged)}})).toThrow("固定内容");expect(api.read()).toEqual(before);
  savePlan(pid,[item({note:"后来备注"})]);before=structuredClone(api.read());
  expect(()=>api.saveResource("reports",{...report,nextPlanBinding:good,content:{...report.content,next_plan:reportNextPlanItems(good)}})).toThrow("修订已变化");expect(api.read()).toEqual(before);
  const fresh=reportNextPlanBinding(api.read(),report,pid,getPlan(pid).revision);
  api.update(s=>{s.resources.goals.find(g=>g.id==="G-next").definitionRevision++;});before=structuredClone(api.read());
  expect(()=>api.saveResource("reports",{...report,nextPlanBinding:fresh,content:{...report.content,next_plan:reportNextPlanItems(fresh)}})).toThrow("固定内容");expect(api.read()).toEqual(before);
  bind(rid);savePlan(pid,[item({title:"新承诺"})]);before=structuredClone(api.read());
  expect(()=>submitReport(rid)).toThrow("下周期计划已变化");expect(api.read()).toEqual(before);
});
test("empty and ineligible next Plan cannot pass report checks; unconfirmed valid commitments are explicit N2", () => {
  const rid=generate(),pid=api.openReportNextPlan(rid,getReport(rid).revision,true);savePlan(pid,[]);bind(rid);
  expect(reportNextPlanCheck(api.read(),getReport(rid)).errors.join()).toContain("至少添加一项");
  savePlan(pid,[item({goalId:"missing"})]);bind(rid);
  expect(reportNextPlanCheck(api.read(),getReport(rid)).errors.join()).toContain("B3");
  savePlan(pid);bind(rid);const check=reportNextPlanCheck(api.read(),getReport(rid));expect(check.errors).toEqual([]);expect(check.warnings.join()).toContain("N2");
});
test("report submission and Human acceptance freeze Plan content without approving or rewriting it", () => {
  const rid=generate(),pid=api.openReportNextPlan(rid,getReport(rid).revision,true);savePlan(pid);bind(rid);
  api.update(s=>{s.resources.reports.find(r=>r.id===rid).references=[];});
  prepareStructuredReport(api,rid,getReport(rid).nextPlanBinding);const before=structuredClone(getPlan(pid));submitReport(rid);expect(getPlan(pid)).toEqual(before);const fixed=structuredClone(getReport(rid).submission);
  savePlan(pid,[item({title:"后续草稿承诺"})]);const changed=structuredClone(getPlan(pid));
  api.update(s=>{s.resources.goals.find(g=>g.id==="G-next").status="已取消";s.resources.plans.push({...structuredClone(getPlan(pid)),id:"conflicting-next"});});
  expect(getReport(rid).submission).toEqual(fixed);expect(api.reportEvidence(api.read(),getReport(rid)).warnings.join()).toContain("保留固定版本");
  api.resourceCommand("reports",rid,"已接受","核对原固定计划，当前改动另行确认");
  expect(getPlan(pid)).toEqual(changed);expect(before.status).toBe("草稿");expect(getPlan(pid).confirmedSnapshot).toBeUndefined();
  expect(getReport(rid).approval.snapshot.content.nextPlanBinding.snapshot.items[0].title).toBe("研究下游需求");
});
test("binding is part of fingerprint, mutation invalidates approval; legacy no-binding fingerprints stay exact", () => {
  const legacy={subjectKey:"agent:atlas",cadence:"周报",periodStart:"2026-10-12",periodEnd:"2026-10-18",timezone:"UTC",name:"旧报告",summary:"原正文",references:[]};
  expect(Object.keys(reportSubmissionContent(legacy))).toEqual(["subjectKey","cadence","periodStart","periodEnd","timezone","name","summary","risk","claimedCompleted","references"]);
  expect(reportSubmissionContent({...legacy,nextPlanBinding:null})).toEqual(reportSubmissionContent(legacy));
  const rid=generate(),pid=api.openReportNextPlan(rid,getReport(rid).revision,true);savePlan(pid);bind(rid);
  api.update(s=>{s.resources.reports.find(r=>r.id===rid).references=[];});prepareStructuredReport(api,rid,getReport(rid).nextPlanBinding);submitReport(rid);
  api.update(s=>{s.resources.reports.find(r=>r.id===rid).nextPlanBinding.snapshot.items[0].title="篡改";});const before=structuredClone(api.read());
  expect(()=>api.resourceCommand("reports",rid,"已接受","企图批准")).toThrow("固定提交");expect(api.read()).toEqual(before);
});
test("opening ignores mixed foreign-tenant Plans and rejects ambiguous duplicates without mutation", () => {
  const rid=generate(),report=getReport(rid), descriptor=reportNextPlanDescriptor(api.read(),report);
  api.update(s=>{s.resources.plans=s.resources.plans.filter(plan=>plan.id!==report.nextPlanBinding.planId);s.resources.plans.push({...descriptor,id:"foreign-next",tenantId:"foreign",status:"草稿",revision:1,items:[]});});
  expect(reportNextPlan(api.read(),report)).toBeNull();
  const pid=api.openReportNextPlan(rid,report.revision,true);expect(pid).not.toBe("foreign-next");expect(getPlan(pid).tenantId).toBe(api.read().activeTenant);
  api.update(s=>{s.resources.plans.push({...structuredClone(getPlan(pid)),id:"duplicate-next"});});const before=structuredClone(api.read());
  expect(()=>api.openReportNextPlan(rid,report.revision,true)).toThrow("重复计划");
  expect(()=>reportNextPlan(api.read(),report)).toThrow("重复计划");expect(api.read()).toEqual(before);
});

test("corrupt confirmed next-Plan baselines cannot be adopted, saved or certified through reports", () => {
 const rid=generate(),pid=api.openReportNextPlan(rid,getReport(rid).revision,true);savePlan(pid);
 api.submitPeriodPlan(pid,getPlan(pid).revision,true);
 const submitted=getPlan(pid);api.decidePeriodPlan(pid,{revision:submitted.revision,submissionRevision:submitted.submission.revision,contentHash:submitted.submission.contentHash,action:"confirm"});
 const original=structuredClone(getPlan(pid)),validBinding=reportNextPlanBinding(api.read(),getReport(rid),pid,original.revision);
 for(const confirmedSnapshot of [null,{}, {...original.confirmedSnapshot,subjectKey:"human:H-zhou"},{...original.confirmedSnapshot,timezone:"Invalid/Zone"},{...original.confirmedSnapshot,items:[null]}]) {
  api.update(s=>s.resources.plans.find(p=>p.id===pid).confirmedSnapshot=confirmedSnapshot);
  const before=structuredClone(api.read()),invalidBinding={...validBinding,confirmedSnapshot};
  expect(()=>reportNextPlanBinding(api.read(),getReport(rid),pid,getPlan(pid).revision)).toThrow();
  for(const submission of [null,{content:{}}])expect(reportNextPlanCheck(api.read(),{...getReport(rid),submission,nextPlanBinding:invalidBinding}).errors.join()).toContain("已确认计划");
  expect(()=>api.saveResource("reports",{...getReport(rid),nextPlanBinding:invalidBinding,content:{...getReport(rid).content,next_plan:reportNextPlanItems(invalidBinding)}})).toThrow();
  expect(api.read()).toEqual(before);
  const historical=reportNextPlanCheck(api.read(),{...getReport(rid),submission:{content:{}},nextPlanBinding:validBinding});
  expect(historical.errors).toEqual([]);expect(historical.warnings.join()).toContain("保留固定版本");
 }
});

test("damaged current next-Plan fields cannot enter newly adopted or embedded report snapshots", () => {
  const rid = generate(), pid = api.openReportNextPlan(rid, getReport(rid).revision, true); savePlan(pid);
  const original = structuredClone(getPlan(pid)), binding = reportNextPlanBinding(api.read(), getReport(rid), pid, original.revision);
  for (const field of ["krId", "note", "estimateHours", "estimateCostMicros"]) {
    api.update(s => { const plan = s.resources.plans.find(p => p.id === pid); Object.assign(plan, structuredClone(original)); plan.items[0][field] = { malformed: true }; });
    const before = structuredClone(api.read());
    expect(() => reportNextPlanBinding(api.read(), getReport(rid), pid, original.revision)).toThrow("当前修订计划项");
    const corruptBinding = structuredClone(binding); corruptBinding.snapshot.items[0][field] = { malformed: true };
    for (const submission of [null, { content: {} }]) expect(reportNextPlanCheck(api.read(), { ...getReport(rid), submission, nextPlanBinding: corruptBinding }).errors.join()).toContain("当前修订计划项");
    const historical = reportNextPlanCheck(api.read(), { ...getReport(rid), submission: { content: {} }, nextPlanBinding: binding });
    expect(historical.errors).toEqual([]); expect(historical.warnings.join()).toContain("保留固定版本");
    expect(api.read()).toEqual(before);
  }
});
