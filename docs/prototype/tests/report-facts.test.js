import { expect, test } from "bun:test";
import { initial } from "../src/data.js";
import { periodicReportFacts, upsertPeriodicReport, periodicWorklogFacts, worklogSubjectMatch, dateInZone } from "../src/reporting.js";
import { planReportFacts } from "../src/plans.js";
import { reportSubmissionContent } from "../src/report-authorization.js";
const descriptor = {subjectKey:"human:H-lin",cadence:"周报",periodStart:"2026-10-12",periodEnd:"2026-10-18",timezone:"Asia/Singapore"};
const now = new Date("2026-10-19T00:00:00Z");
const state=()=>{const s=structuredClone(initial);s.work=s.work.filter(w=>w.id==="W-104");s.activeTenant="studio";s.tenants=[{id:"studio",name:"Test Tenant"}];s.resources.reports=[];s.resources.plans=[];s.work[0].acceptanceFacts=[];return s};
const fact=(patch={})=>({id:"W-104:v3:accept",workId:"W-104",version:3,project:"launch",time:"2026-10-12T02:00:00Z",subjectKeys:["human:H-lin","project:launch","tenant:studio"],executorIds:["H-lin"],...patch});
const generate=(s,patch={})=>{const id=upsertPeriodicReport(s,{...descriptor,date:"2026-10-13",...patch},"test",()=>"RP-new");return s.resources.reports.find(r=>r.id===id)};
test("personal completion requires fixed executor identity across every role association",()=>{
 const s=state();
 for (const subjectKey of ["human:H-lin","agent:atlas"]) {
  const identity=subjectKey.startsWith("human:")?subjectKey.slice(6):subjectKey;
  const other=identity==="H-lin"?"agent:atlas":"H-lin";
  for (const association of [{ownerId:identity},{actorId:identity},{ownerId:identity,actorId:identity}]) {
   s.work[0].acceptanceFacts=[fact({subjectKeys:[subjectKey,"project:launch","tenant:studio"],executorIds:[other],...association})];
   const report={...descriptor,subjectKey};
   expect(periodicReportFacts(s,report,now).completed).toBe(0);
   expect(periodicReportFacts(s,{...report,subjectKey:"project:launch"},now).completed).toBe(1);
   expect(periodicReportFacts(s,{...report,subjectKey:"tenant:studio"},now).completed).toBe(1);
   expect(generate(s,{subjectKey}).generatedFacts.completed).toBe(0);
   expect(reportEvidence(s,{...report,references:[{kind:"work",id:"W-104",version:3}],claimedCompleted:1}).errors.some(e=>e.includes("不一致"))).toBe(true);
   s.work[0].acceptanceFacts[0].executorIds=[identity,other];
   expect(periodicReportFacts(s,report,now).completed).toBe(1);
   delete s.work[0].acceptanceFacts[0].executorIds;
   expect(periodicReportFacts(s,report,now)).toMatchObject({completed:0,unknownWorkIds:["W-104"]});
  }
 }
});
test("period completion follows immutable acceptance event dates and subjects, never today's work status",()=>{
 const s=state(),w=s.work[0];w.status="已完成";w.accepted=true;
 const old=generate(s,{date:"2000-01-03"});expect(old.generatedFacts.completed).toBe(0);expect(old.generatedFacts.unknownWorkIds).toEqual([w.id]);expect(old.generatedSummary).toContain("完整完成数不可确定");expect(old.suggestedReferences).toEqual([]);
 w.acceptanceFacts=[fact()];expect(periodicReportFacts(s,descriptor,now).completed).toBe(1);
 w.status="进行中";w.accepted=false;w.version=4;w.project="other";w.owner="Other";w.team={executorIds:["H-other"]};w.production={executorIds:["H-other"]};
 expect(periodicReportFacts(s,descriptor,now).completed).toBe(1);expect(periodicReportFacts(s,{...descriptor,subjectKey:"human:H-other"},now).completed).toBe(0);
 expect(periodicReportFacts(s,{...descriptor,periodStart:"2000-01-03",periodEnd:"2000-01-09"},now).completed).toBe(0);
});
test("all versioned acceptance events survive dedupe while distinct-work count remains one",()=>{
 const s=state();s.work[0].acceptanceFacts=[fact(),fact(),fact({id:"W-104:v4:accept",version:4})];
 const result=periodicReportFacts(s,descriptor,now);expect(result.completed).toBe(1);expect(result.factKeys).toEqual(["W-104:v3:accept","W-104:v4:accept"]);
 expect(generate(s).suggestedReferences.filter(r=>r.kind==="work")).toEqual([{kind:"work",id:"W-104",version:3,acceptanceFactIds:["W-104:v3:accept"]},{kind:"work",id:"W-104",version:4,acceptanceFactIds:["W-104:v4:accept"]}]);
});
test("period timezone boundaries, future facts and invalid historical data never fabricate completion",()=>{
 const s=state();s.work[0].acceptanceFacts=[fact({time:"2026-10-11T20:00:00Z"})];
 expect(periodicReportFacts(s,descriptor,now).completed).toBe(1);expect(periodicReportFacts(s,{...descriptor,timezone:"UTC"},now).completed).toBe(0);
 expect(periodicReportFacts(s,descriptor,new Date("2026-10-11T19:00:00Z")).completed).toBe(0);
 s.work[0].acceptanceFacts=[fact({time:"unknown"})];expect(periodicReportFacts(s,descriptor,now)).toMatchObject({completed:0,unknownWorkIds:["W-104"]});
 s.work[0].acceptanceFacts=[fact({subjectKeys:undefined})];expect(periodicReportFacts(s,descriptor,now).unknownWorkIds).toEqual(["W-104"]);
});
test("historical report Plan facts survive rework while live Plan facts retain current-state contract",()=>{
 const s=state(),w=s.work[0];w.acceptanceFacts=[fact()];w.version=4;w.accepted=false;
 const plan={id:"P-history",tenantId:s.activeTenant,periodKind:"WEEK",...descriptor,confirmedSnapshot:{...descriptor,periodKind:"WEEK",revision:1,items:[{id:"PI-1",kind:"WORK_ITEM",workId:w.id,title:"交付",weight:1}]}};
 expect(planReportFacts(s,plan,now).progress).toBe(0);expect(planReportFacts(s,plan,now,{historical:true}).progress).toBe(1);
 s.resources.plans=[plan];expect(periodicReportFacts(s,descriptor,now).plans[0].factKeys).toEqual(["W-104:v3:accept"]);
 w.acceptanceFacts[0].executorIds=["H-other"];expect(planReportFacts(s,plan,now,{historical:true}).progress).toBe(0);
 w.acceptanceFacts=[];w.history=[{accepted:true}];expect(planReportFacts(s,plan,now,{historical:true}).progress).toBeNull();
});
test("regeneration updates only generated suggestions and preserves submitted revisions",()=>{
 const s=state();s.work[0].acceptanceFacts=[fact()];let r=generate(s);r.summary="人工判断";r.references=[];r.risk="人工风险";
 s.work[0].acceptanceFacts.push(fact({id:"W-104:v4:accept",version:4}));r=generate(s);expect(r.summary).toBe("人工判断");expect(r.references).toEqual([]);expect(r.risk).toBe("人工风险");expect(r.generatedFacts.factKeys).toHaveLength(2);expect(r.generationHistory[0].generatedFacts.factKeys).toHaveLength(1);
 r.status="已提交";r.factSnapshot={fixed:"evidence"};const before=structuredClone(r);s.work[0].acceptanceFacts=[];expect(generate(s)).toEqual(before);
});

if (!globalThis.localStorage) globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const { reportEvidence } = await import("../src/store.js");
test("current Plan selection uses one exact calendar cadence and reports missing, draft and duplicate baselines",()=>{
 const s=state(), r={...descriptor,references:[]};
 const p={id:"WEEK-plan",tenantId:s.activeTenant,subjectKey:r.subjectKey,periodKind:"WEEK",periodStart:r.periodStart,periodEnd:r.periodEnd,timezone:r.timezone,status:"已确认"};
 p.confirmedSnapshot={...p,revision:1,items:[]};
 const day={...structuredClone(p),id:"DAY-plan",periodKind:"DAY",periodStart:"2026-10-14",periodEnd:"2026-10-14"};day.confirmedSnapshot={...day,revision:1,items:[]};
 s.resources.plans=[day,p];
 expect(periodicReportFacts(s,r,now).plans.map(plan=>plan.id)).toEqual([p.id]);
 expect(periodicReportFacts(s,r,now).planIssues).toEqual([]);
 for(const plans of [[],[day],[{...p,tenantId:"foreign"}],[{...p,subjectKey:"agent:atlas"}]]) {
  s.resources.plans=plans;const result=reportEvidence(s,r);expect(result.warnings.join()).toContain("N2：本期尚无");expect(result.errors).toEqual([]);
 }
 s.resources.plans=[{...p,status:"草稿",confirmedSnapshot:null}];
 expect(periodicReportFacts(s,r,now).plans[0]).toMatchObject({basisRevision:null,progress:null});
 expect(reportEvidence(s,r).warnings.join()).toContain("不能使用草稿承诺");
 expect(generate(s).generatedSummary).toContain("0 份可读取的已确认计划基线");
 s.resources.plans=[p,{...structuredClone(p),id:"duplicate"}];
 expect(periodicReportFacts(s,r,now).plans).toEqual([]);expect(reportEvidence(s,r).warnings.join()).toContain("重复计划");
 s.resources.plans=[{...p,periodEnd:"2026-10-17"}];expect(reportEvidence(s,r).warnings.join()).toContain("结束日期");
 s.resources.plans=[{...p,status:"已提交"}];expect(reportEvidence(s,r).warnings.join()).toContain("原已确认基线");
});
test("fixed report preserves current Plan diagnostics after sources change without reading live replacements",()=>{
 const s=state(),r={...descriptor,id:"R-fixed-plans",references:[]};
 const evidence=reportEvidence(s,r);expect(evidence.planIssues.join()).toContain("本期尚无");
 r.submission={content:reportSubmissionContent(r),factSnapshot:structuredClone(evidence)};
 const fixed=structuredClone(r);
 s.resources.plans=[{id:"later",tenantId:s.activeTenant,subjectKey:r.subjectKey,periodKind:"WEEK",periodStart:r.periodStart,periodEnd:r.periodEnd,timezone:r.timezone,status:"草稿"}];
 expect(reportEvidence(s,r).planIssues).toEqual(evidence.planIssues);expect(r).toEqual(fixed);
 r.submission.factSnapshot.warnings=[];
 expect(reportEvidence(s,r).planIssues).toEqual(evidence.planIssues);
 r.submission.factSnapshot.warnings=structuredClone(evidence.warnings);
 // Previously stored warning-only snapshots retain their original diagnostic.
 delete r.submission.factSnapshot.planIssues;
 expect(reportEvidence(s,r).warnings).toContain("N2：本期尚无同主体、同日历周期的计划；承诺与进度基线未确认。");
 for(const corrupt of [{},"bad",[{}]]) {
  r.submission.factSnapshot.planIssues=corrupt;expect(reportEvidence(s,r).errors.join()).toContain("固定本期计划核对提示损坏");
 }
});
test("report evidence counts selected historical events and preserves every exact snapshot across version dedupe",()=>{
 const s=state(),w=s.work[0];w.acceptanceFacts=[fact(),fact({id:"W-104:v4:accept",version:4})];w.version=5;w.accepted=false;w.title="TODAY TITLE";
 const r={...descriptor,id:"R",references:[{kind:"work",id:w.id,version:3,acceptanceFactIds:["W-104:v3:accept"]},{kind:"work",id:w.id,version:4,acceptanceFactIds:["W-104:v4:accept"]}],claimedCompleted:1};
 const result=reportEvidence(s,r);expect(result.errors).toEqual([]);expect(result.totals.completed).toBe(1);expect(result.totals.acceptanceFactKeys).toHaveLength(2);expect(result.sources[0].acceptanceFacts).toHaveLength(2);expect(result.sources[0].name).not.toContain("TODAY TITLE");expect(result.sources[0].name).toContain("历史成果内容不可用");
 r.references=[{kind:"work",id:w.id,version:3}];expect(reportEvidence(s,r).errors.some(error=>error.includes("引用版本已变化"))).toBe(true);
 r.references=[{kind:"work",id:w.id,version:5}];expect(reportEvidence(s,r).totals.completed).toBe(0);expect(reportEvidence(s,r).errors.some(error=>error.includes("不一致"))).toBe(true);
 r.references=[{kind:"work",id:w.id,version:3,acceptanceFactIds:["forged"]}];expect(reportEvidence(s,r).errors.some(error=>error.includes("接受事实不属于"))).toBe(true);
});
test("explicit empty calendar reports are allowed while missing legacy references and unknown zero claims are not",()=>{
 const s=state();expect(reportEvidence(s,{...descriptor,references:[]}).errors).toEqual([]);
 expect(reportEvidence(s,{periodStart:descriptor.periodStart,periodEnd:descriptor.periodEnd,references:[]}).errors.length).toBeGreaterThan(0);
 expect(reportEvidence(s,{...descriptor}).errors.length).toBeGreaterThan(0);
 s.work[0].accepted=true;const result=reportEvidence(s,{...descriptor,references:[],claimedCompleted:0});expect(result.errors.some(error=>error.includes("接受历史未知"))).toBe(true);expect(result.warnings.some(warning=>warning.includes("不代表"))).toBe(true);
});

test("period uniqueness excludes timezone and regeneration retains the original temporal interpretation",()=>{
 const s=state();s.work[0].acceptanceFacts=[fact({time:"2026-10-11T20:00:00Z"})];const first=generate(s);expect(first.generatedFacts.completed).toBe(1);const changed=generate(s,{timezone:"UTC"});expect(changed.id).toBe(first.id);expect(changed.timezone).toBe("Asia/Singapore");expect(changed.generatedFacts.completed).toBe(1);expect(s.resources.reports).toHaveLength(1);
});
test("legacy effort scope is unknown instead of being reassigned by today's work or organization",()=>{
 const s=state();s.resources.worklogs=[{id:"old-effort",work:"W-104",date:"2026-10-13",ownerId:"H-lin",owner:"旧名",hours:2,status:"已提交"}];
 const project={...descriptor,subjectKey:"project:launch"};expect(periodicWorklogFacts(s,project)).toMatchObject({logs:[],unknownLogIds:["old-effort"]});
 s.work[0].project="other";s.organization.nodes.forEach(node=>node.memberIds=[]);expect(periodicWorklogFacts(s,project)).toMatchObject({logs:[],unknownLogIds:["old-effort"]});
 expect(periodicWorklogFacts(s,descriptor).logs.map(log=>log.id)).toEqual(["old-effort"]);
 delete s.resources.worklogs[0].ownerId;expect(periodicWorklogFacts(s,descriptor).unknownLogIds).toEqual(["old-effort"]);
});
test("new effort snapshots survive reassignment, org changes, calibration and attempted injected scope",async()=>{
 const api=await import("../src/store.js");api.reset();api.update(s=>s.resources.worklogs=[]);const date=dateInZone("UTC"),options={action:"estimate",workId:"W-104",date,timezone:"UTC",category:"验收",estimateHours:2,note:"本人当日参与"};
 const id=api.saveHumanEffort({...options,scopeSnapshot:{subjectKeys:["project:forged"]}}),get=()=>api.read().resources.worklogs.find(log=>log.id===id),snapshot=structuredClone(get().scopeSnapshot);
 expect(snapshot.subjectKeys).toContain("project:launch");expect(snapshot.subjectKeys).toContain("team:ORG-engineering");expect(snapshot.subjectKeys).not.toContain("project:forged");
 api.update(s=>{s.work.find(work=>work.id==="W-104").project="other";s.organization.nodes.forEach(node=>node.memberIds=[])});
 api.saveHumanEffort({id,revision:get().revision,action:"calibrate",calibratedHours:1.5,note:"实际核对"});expect(get().scopeSnapshot).toEqual(snapshot);api.saveHumanEffort({id,revision:get().revision,action:"confirm"});expect(get().scopeSnapshot).toEqual(snapshot);
 expect(worklogSubjectMatch(api.read(),get(),"project:launch")).toBe(true);expect(worklogSubjectMatch(api.read(),get(),"project:other")).toBe(false);expect(worklogSubjectMatch(api.read(),get(),"team:ORG-engineering")).toBe(true);
 const historical=api.saveHumanEffort({...options,date:"2000-01-03"}),old=api.read().resources.worklogs.find(log=>log.id===historical);expect(worklogSubjectMatch(api.read(),old,"human:H-lin")).toBe(true);expect(worklogSubjectMatch(api.read(),old,"project:other")).toBeNull();expect(old.scopeSnapshot.unknownScopes).toEqual(["project","team"]);
 api.reset();
});

test("selected worklog totals disclose unknown historical scope instead of attributing by current project",()=>{
 const s=state();s.resources.worklogs=[{id:"WL-history",name:"历史投入",work:"W-104",date:"2026-10-13",ownerId:"H-lin",hours:2,status:"已提交"}];
 const r={...descriptor,subjectKey:"project:launch",references:[{kind:"worklog",id:"WL-history",version:1}]};let check=reportEvidence(s,r);expect(check.totals.human).toBe(0);expect(check.totals.unknownEffortIds).toEqual(["WL-history"]);expect(check.warnings.some(w=>w.includes("历史投入归属未知"))).toBe(true);
 s.work[0].project="other";expect(reportEvidence(s,r).totals).toEqual(check.totals);
 r.subjectKey="human:H-lin";expect(reportEvidence(s,r).totals.human).toBe(2);r.subjectKey="human:H-zhou";expect(reportEvidence(s,r).errors.some(e=>e.includes("不属于"))).toBe(true);
});

test("saved legacy Agent worklogs never infer stable authorship from today's matching display name",async()=>{
 const api=await import("../src/store.js");api.reset();const saved=structuredClone(api.read());delete saved.resources.worklogs.find(log=>log.id==="WL-03").ownerAgentId;
 const previous=Object.getOwnPropertyDescriptor(globalThis,"localStorage");Object.defineProperty(globalThis,"localStorage",{configurable:true,value:{getItem:()=>JSON.stringify(saved),setItem:()=>{},removeItem:()=>{}}});
 try { const restored=await import("../src/store.js?report-legacy-effort-identity");const log=restored.read().resources.worklogs.find(log=>log.id==="WL-03");expect(log.ownerAgentId).toBeNull();expect(worklogSubjectMatch(restored.read(),log,"agent:atlas")).toBeNull(); }
 finally {if(previous)Object.defineProperty(globalThis,"localStorage",previous);else delete globalThis.localStorage;api.reset()}
});

test("report Plan projection excludes foreign tenants and diagnoses damaged baselines without certifying them", () => {
 const s=state(),w=s.work[0];w.acceptanceFacts=[fact()];
 const snapshot={...descriptor,periodKind:"WEEK",revision:1,items:[{id:"PI-1",kind:"WORK_ITEM",workId:w.id,title:"本期交付",weight:1}]};
 const plan={...snapshot,id:"P-projection",tenantId:s.activeTenant,status:"已确认",confirmedSnapshot:snapshot};
 s.resources.plans=[{...plan,tenantId:"foreign"}];expect(periodicReportFacts(s,descriptor,now).plans).toEqual([]);expect(generate(s).generatedSummary).toContain("0 份可读取");
 s.resources.plans=[plan];expect(periodicReportFacts(s,descriptor,now).plans[0]).toMatchObject({progress:1,issues:[]});
 for(const confirmedSnapshot of [null,{}, {...snapshot,items:{}},{...snapshot,items:[null]},{...snapshot,subjectKey:"human:H-zhou"}]) {
  s.resources.plans=[{...plan,confirmedSnapshot}];const before=structuredClone(s.resources.plans);
  const projection=periodicReportFacts(s,descriptor,now).plans;expect(projection).toHaveLength(1);expect(projection[0]).toMatchObject({progress:null,unknown:null,facts:[],outcomes:[]});expect(projection[0].issues).toHaveLength(1);
  const r=generate(s);expect(r.generatedSummary).toContain("0 份可读取的已确认计划基线");expect(r.generatedSummary).toContain("1 份计划基线无法核对");expect(s.resources.plans.filter(item=>item.id===plan.id)).toEqual(before);expect(s.resources.plans).toHaveLength(2);expect(s.resources.plans.find(item=>item.id!==plan.id).status).toBe("草稿");expect(reportEvidence(s,r).warnings.some(w=>w.includes("N2：本期计划"))).toBe(true);
 }
});
