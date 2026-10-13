import { expect, test } from "bun:test";
import { initial } from "../src/data.js";
import { periodicReportFacts, upsertPeriodicReport, periodicWorklogFacts, worklogSubjectMatch, dateInZone } from "../src/reporting.js";
import { planReportFacts } from "../src/plans.js";
const descriptor = {subjectKey:"human:H-lin",cadence:"周报",periodStart:"2026-10-12",periodEnd:"2026-10-18",timezone:"Asia/Singapore"};
const now = new Date("2026-10-19T00:00:00Z");
const state=()=>{const s=structuredClone(initial);s.work=s.work.filter(w=>w.id==="W-104");s.activeTenant="studio";s.tenants=[{id:"studio",name:"Test Tenant"}];s.resources.reports=[];s.resources.plans=[];s.work[0].acceptanceFacts=[];return s};
const fact=(patch={})=>({id:"W-104:v3:accept",workId:"W-104",version:3,project:"launch",time:"2026-10-12T02:00:00Z",subjectKeys:["human:H-lin","project:launch","tenant:studio"],executorIds:["H-lin"],...patch});
const generate=(s,patch={})=>{const id=upsertPeriodicReport(s,{...descriptor,date:"2026-10-13",...patch},"test",()=>"RP-new");return s.resources.reports.find(r=>r.id===id)};
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
 const plan={id:"P-history",...descriptor,confirmedSnapshot:{revision:1,items:[{id:"PI-1",kind:"WORK_ITEM",workId:w.id,title:"交付",weight:1}]}};
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
 const previous=globalThis.localStorage;globalThis.localStorage={getItem:()=>JSON.stringify(saved),setItem:()=>{},removeItem:()=>{}};
 try { const restored=await import("../src/store.js?report-legacy-effort-identity");const log=restored.read().resources.worklogs.find(log=>log.id==="WL-03");expect(log.ownerAgentId).toBeNull();expect(worklogSubjectMatch(restored.read(),log,"agent:atlas")).toBeNull(); }
 finally {globalThis.localStorage=previous;api.reset()}
});
