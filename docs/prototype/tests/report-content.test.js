import { beforeEach, expect, test } from "bun:test";
import { emptyReportContent, reportNextPlanItems, reportContentCheck, reportContentFacts } from "../src/report-content.js";
import { reportNextPlanBinding, reportCurrentPlanBinding, planReportFacts } from "../src/plans.js";
import { readGoalMetric } from "../src/goals.js";
import { reportSubmissionContent } from "../src/report-authorization.js";
import { periodicReportFacts, reportAcceptanceMatch } from "../src/reporting.js";
if (!globalThis.localStorage) { const entries=new Map();globalThis.localStorage={getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)}; }
const api=await import("../src/store.js?report-content");
const get=id=>api.read().resources.reports.find(r=>r.id===id);
const save=(id,patch={})=>api.saveResource("reports",{...get(id),...patch});
const command=(id,status,patch={})=>api.resourceCommand("reports",id,status,"核对当前完整报告内容","",{revision:get(id).revision,status:get(id).status,simulateAgent:true,...patch});
function fixture(){
 const rid=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"周报",date:"2026-10-13",timezone:"UTC"});
 const pid=api.openReportNextPlan(rid,get(rid).revision,true),plan=()=>api.read().resources.plans.find(p=>p.id===pid);
 api.savePeriodPlan(pid,{simulateAgent:true,items:[{id:"next",kind:"FREE_TASK",title:"完成下游需求研究",goalId:"G-content",freeCategory:"LEARNING",criteria:"保留调研结论",weight:1}]},plan().revision);
 const binding=reportNextPlanBinding(api.read(),get(rid),pid,plan().revision),content=emptyReportContent(binding),work=api.read().work.find(w=>w.id==="W-103");
 const fact={id:"content-accepted",workId:work.id,version:work.version,time:"2026-10-12T02:00:00Z",project:work.project,subjectKeys:["agent:atlas",`project:${work.project}`,"tenant:studio"],executorIds:["agent:atlas"],workSnapshot:{title:work.title,plan:true,quality:"通过"}};
 api.update(s=>{s.work.find(w=>w.id===work.id).acceptanceFacts=[fact];});
 content.completed=[{key:"done",title:"固定版本交付完成",goal_id:"G-content",kr_id:"KR-content",fact_ref:{kind:"WORK_ITEM",id:work.id,event_id:fact.id,version:fact.version},note:"接受事实保留"}];
 content.uncompleted=[{key:"unfinished",title:"下游方案仍待检查",work_item_id:"W-104",reason:"QUALITY_REWORK",detail:"质量检查尚未完成",carry_over:"CARRY_TO_NEXT"}];
 content.retrospective={went_well:["固定版本可追溯"],problems:["下游检查延迟"],root_causes:["检查资源未及时到位"],improvements:[{text:"提前预约检查资源",propose_memory:true}],ai_drafted:false};
 save(rid,{references:[{kind:"work",id:work.id,version:work.version,acceptanceFactIds:[fact.id]}],nextPlanBinding:binding,content,reviewedDetailKeys:["unfinished"]});return {rid,pid,workId:work.id,fact};
}
beforeEach(()=>{api.reset();api.update(s=>s.resources.goals.push({id:"G-content",name:"交付研究目标",subjectKey:"agent:atlas",ownerId:"atlas",ownerKind:"Agent",status:"进行中",revision:1,definitionRevision:1,krs:[{id:"KR-content",title:"完成研究",weight:1}],alignments:[]}));});
test("new periodic reports require honest four-section drafts with real next-Plan projection for every subject and cadence",()=>{
 for(const subjectKey of ["human:H-lin","agent:atlas","project:launch","team:ORG-engineering","tenant:studio"])for(const cadence of ["日报","周报","月报","季报","年报"]){
  const options={subjectKey,cadence,date:"2027-02-17",timezone:"UTC"},id=api.generatePeriodicReport(options),report=structuredClone(get(id));
  expect(report.contentRequired).toBe(true);expect(report.content).toEqual(emptyReportContent(report.nextPlanBinding));
  expect(api.generatePeriodicReport(options)).toBe(id);expect(get(id)).toEqual(report);
  const before=structuredClone(api.read());expect(()=>command(id,"已提交")).toThrow("B4");expect(api.read()).toEqual(before);
 }
});
test("new report chapter requirement cannot be removed through save or missing stored content",()=>{
 const id=api.generatePeriodicReport({subjectKey:"agent:atlas",cadence:"日报",date:"2027-02-17",timezone:"UTC"});
 const before=structuredClone(api.read());expect(()=>save(id,{content:null,contentRequired:false})).toThrow("必须保留");expect(api.read()).toEqual(before);
 save(id,{contentRequired:false});expect(get(id).contentRequired).toBe(true);
 api.update(s=>{delete s.resources.reports.find(r=>r.id===id).content;});
 expect(reportContentCheck(api.read(),get(id)).issues[0].code).toBe("B4");const broken=structuredClone(api.read());expect(()=>command(id,"已提交")).toThrow("必须保留");expect(api.read()).toEqual(broken);
});
test("new records saved directly initialize required chapters instead of inheriting legacy bypass",()=>{
 const id=api.generatePeriodicReport({subjectKey:"human:H-lin",cadence:"月报",date:"2027-02-17",timezone:"UTC"}),record=structuredClone(get(id));
 api.update(s=>{s.resources.reports=s.resources.reports.filter(r=>r.id!==id);});api.saveResource("reports",{...record,content:null,contentRequired:false,status:"已接受"});
 expect(get(id).contentRequired).toBe(true);expect(get(id).content).toEqual(emptyReportContent());expect(get(id).status).toBe("草稿");
});
test("explicit legacy migration preserves same ID, original prose and original revision",()=>{
 const id="RP-01",original=structuredClone(get(id));expect(original.content).toBeUndefined();
 const options={subjectKey:original.subjectKey,cadence:original.cadence,date:original.periodStart,timezone:original.timezone};
 expect(api.generatePeriodicReport(options)).toBe(id);expect(get(id).content).toBeUndefined();expect(get(id).summary).toBe(original.summary);
 const before=structuredClone(api.read()),content=emptyReportContent();expect(()=>save(id,{references:[],content})).toThrow("显式建立");expect(api.read()).toEqual(before);
 const prior=structuredClone(get(id));save(id,{references:[],content,migrateLegacyContent:true,contentMigration:{actorId:"forged"}});
 const migrated=get(id);expect(migrated.id).toBe(id);expect(migrated.summary).toBe(prior.summary);expect(migrated.contentRequired).toBe(true);expect(migrated.content).toEqual(content);
 expect(migrated.contentMigration).toMatchObject({sourceRevision:prior.revision,actorId:"H-lin",actorKind:"Human"});expect(migrated.history.at(-1)).toMatchObject({...prior,history:undefined});expect(migrated.migrateLegacyContent).toBeUndefined();
 expect(()=>command(id,"已提交")).toThrow("B4");
});
test("corrupt adopted Plan shapes reject projection and save without erasing fixed data",()=>{
 const {rid}=fixture(),base=structuredClone(get(rid));
 const variants=[{}, {snapshot:null}, {snapshot:{}}, {snapshot:{items:{}}}, {snapshot:{items:[null]}}, {snapshot:{items:[{}]}}, {snapshot:{items:[{id:"item",title:null}]}}, {revision:{bad:true},snapshot:{items:[]}}, {planId:{bad:true},snapshot:{items:[]}}, {status:{bad:true},snapshot:{items:[]}}, {snapshot:{periodStart:{bad:true},items:[]}}, ...["goalId","krId","dueDate","workId","freeCategory"].map(key=>({snapshot:{items:[{id:"item",title:"标题",[key]:{bad:true}}]}}))];
 for(const binding of variants){
  const before=structuredClone(api.read()),original=structuredClone(binding);
  expect(()=>reportNextPlanItems(binding)).toThrow("固定下期计划快照损坏");
  expect(()=>emptyReportContent(binding)).toThrow("固定下期计划快照损坏");
  expect(()=>save(rid,{nextPlanBinding:binding})).toThrow("固定下期计划快照损坏");expect(api.read()).toEqual(before);expect(binding).toEqual(original);
  expect(reportContentCheck(api.read(),{...base,nextPlanBinding:binding}).issues.some(issue=>issue.message.includes("固定下期计划快照损坏"))).toBe(true);
 }
 expect(reportNextPlanItems(null)).toEqual([]);
 expect(reportNextPlanItems({snapshot:{items:[]}})).toEqual([]);
});
test("four sections round-trip into fixed submission, approval and REOPEN history without approving Plan",()=>{
 const {rid,pid}=fixture(),before=structuredClone(get(rid).content),plans=structuredClone(api.read().resources.plans);
 expect(before.summary.completed_count).toBeNull();expect(before.summary.cost_micros).toBeNull();expect(reportContentCheck(api.read(),get(rid)).issues).toEqual([]);expect(get(rid).reviewedDetailKeys).toBeUndefined();
 command(rid,"已提交");expect(get(rid).submission.content.content).toEqual(before);command(rid,"已接受");expect(get(rid).approval.snapshot.content.content).toEqual(before);expect(api.read().resources.plans).toEqual(plans);expect(api.read().resources.plans.find(p=>p.id===pid).status).toBe("草稿");
 command(rid,"草稿",{action:"REOPEN"});save(rid,{content:{...get(rid).content,summary:{...get(rid).content.summary,narrative:"修订说明"}}});expect(get(rid).history.some(h=>h.snapshot?.approval?.snapshot.content.content?.retrospective.improvements[0].text==="提前预约检查资源")).toBe(true);
});
test("whole content fingerprint catches changed text, legacy no-content keys remain exact",()=>{
 const {rid}=fixture();command(rid,"已提交");const fixed=structuredClone(get(rid).submission);
 api.update(s=>{s.resources.reports.find(r=>r.id===rid).content.retrospective.root_causes=["被篡改的根因"];});const before=structuredClone(api.read());expect(()=>command(rid,"已接受")).toThrow("固定提交");expect(api.read()).toEqual(before);expect(fixed.content.content.retrospective.root_causes).toEqual(["检查资源未及时到位"]);
 const legacy={name:"原正文",summary:"原内容",references:[]};expect(Object.keys(reportSubmissionContent(legacy))).toHaveLength(10);expect(reportSubmissionContent({...legacy,content:null})).toEqual(reportSubmissionContent(legacy));
});
test("required-chapter policy is fixed in submissions and cannot be downgraded before acceptance",()=>{
 const {rid}=fixture();command(rid,"已提交");expect(get(rid).submission.content.contentRequired).toBe(true);
 api.update(s=>{s.resources.reports.find(r=>r.id===rid).contentRequired=false;});const before=structuredClone(api.read());expect(()=>command(rid,"已接受")).toThrow("固定提交");expect(api.read()).toEqual(before);
});
test("legacy migration keeps original submitted body and its old fingerprint in history",()=>{
 const id="RP-01";api.update(s=>{const r=s.resources.reports.find(r=>r.id===id);r.references=[];r.status="已提交";r.submission={revision:r.revision||1,authorId:"H-lin",authorKind:"Human",confirmerId:"H-zhou",content:reportSubmissionContent(r),factSnapshot:api.reportEvidence(s,r)};});
 const legacy=structuredClone(get(id));expect(Object.keys(legacy.submission.content)).toHaveLength(10);
 save(id,{content:emptyReportContent(),migrateLegacyContent:true});
 expect(get(id).status).toBe("草稿");expect(get(id).submission).toBeNull();expect(get(id).history.at(-1).submission).toEqual(legacy.submission);
 expect(get(id).history.at(-1).summary).toBe(legacy.summary);expect(get(id).contentRequired).toBe(true);
});
test("invalid schema, all field boundaries, duplicate keys and inherited enums reject atomically",()=>{
 const {rid}=fixture(),base=structuredClone(get(rid).content);
 const variants=[{...base,completed:null},{...base,schema_version:9},{...base,retrospective:{...base.retrospective,problems:null}}, {...base,completed:[{...base.completed[0],title:"x".repeat(201)}]}, {...base,completed:[{...base.completed[0],note:"x".repeat(2001)}]}, {...base,completed:[base.completed[0],base.completed[0]]}, {...base,uncompleted:[{...base.uncompleted[0],reason:"toString"}]}, {...base,uncompleted:[{...base.uncompleted[0],carry_over:"constructor"}]}, {...base,retrospective:{...base.retrospective,problems:["x".repeat(501)]}}, {...base,completed:Array.from({length:501},(_,i)=>({...base.completed[0],key:String(i)}))}];
 for(const content of variants){const before=structuredClone(api.read());expect(()=>save(rid,{content})).toThrow();expect(api.read()).toEqual(before);}expect(()=>save(rid,{content:null})).toThrow("隐式移除");
 for(const broken of [{...base,completed:[null]},{...base,retrospective:{problems:[]}},{...base,summary:null}])expect(()=>reportContentCheck(api.read(),{...get(rid),content:broken})).not.toThrow();
});
test("unavailable kinds, owner-only events, wrong dates/versions, duplicates and fake counts cannot pass",()=>{
 const {rid,workId,fact}=fixture(),base=structuredClone(get(rid).content);
 for(const kind of ["DECISION","DELIVERY","ACCEPTANCE","FREE_TASK_EVIDENCE","CHILD_REPORT"]){save(rid,{content:{...base,completed:[{...base.completed[0],fact_ref:{kind,id:"source",event_id:"event",version:1}}]}});expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.code==="B1")).toBe(true);}
 save(rid,{content:base});for(const patch of [{executorIds:[],actorId:"atlas"},{time:"2026-09-01T00:00:00Z"},{version:fact.version+1},{subjectKeys:["human:H-lin"]}]){api.update(s=>{s.work.find(w=>w.id===workId).acceptanceFacts=[{...fact,...patch}];});expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.code==="B1")).toBe(true);}
 api.update(s=>{s.work.find(w=>w.id===workId).acceptanceFacts=[fact];});save(rid,{content:{...base,completed:[base.completed[0],{...base.completed[0],key:"copy"}]}});expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.code==="B2")).toBe(true);
 for(const patch of [{completed_count:2},{accepted_work_items:2},{uncompleted_count:2},{cost_micros:0}]){save(rid,{content:{...base,summary:{...base.summary,...patch}}});expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.code==="B2")).toBe(true);}
 save(rid,{content:{...base,completed:[{...base.completed[0],kr_id:"foreign-KR"}]}});expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.code==="B5")).toBe(true);
});
test("author acknowledgments are store-owned, content-bound and invalidated on edits or explicit withdrawal",()=>{
 const {rid}=fixture(),original=structuredClone(get(rid).content.uncompleted[0].detail_confirmation);expect(original.actorId).toBe("atlas");let content=structuredClone(get(rid).content);content.uncompleted[0].detail="更改后的阻塞说明";content.uncompleted[0].detail_confirmation={...original,fingerprint:"forged",actorId:"H-lin"};
 save(rid,{content});expect(get(rid).content.uncompleted[0].detail_confirmation).toBeNull();const before=structuredClone(api.read());expect(()=>command(rid,"已提交")).toThrow("尚未确认");expect(api.read()).toEqual(before);
 save(rid,{reviewedDetailKeys:["unfinished"]});expect(get(rid).content.uncompleted[0].detail_confirmation.actorId).toBe("atlas");content=structuredClone(get(rid).content);content.uncompleted[0].detail_confirmation=null;save(rid,{content});expect(get(rid).content.uncompleted[0].detail_confirmation).toBeNull();
});
test("AI provenance cannot be erased by false flags, forged stamps or edits after confirmation",()=>{
 const {rid}=fixture();let content=structuredClone(get(rid).content);content.retrospective.ai_drafted=true;save(rid,{content});expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.message.includes("AI/模板"))).toBe(true);
 content=structuredClone(get(rid).content);content.retrospective.ai_drafted=false;content.retrospective.origin="MANUAL";content.retrospective.confirmation={actorId:"atlas",actorKind:"Agent",fingerprint:"forged"};save(rid,{content});expect(get(rid).content.retrospective.ai_drafted).toBe(true);
 save(rid,{reviewedRetrospective:true});expect(get(rid).content.retrospective.ai_drafted).toBe(false);content=structuredClone(get(rid).content);content.retrospective.problems=["更改后的问题描述"];save(rid,{content});expect(get(rid).content.retrospective.origin).toBe("AI");expect(get(rid).content.retrospective.ai_drafted).toBe(true);
});
test("real Plan projection is exclusive; incomplete drafts save but structured submission blocks",()=>{
 const {rid}=fixture(),base=structuredClone(get(rid).content);expect(base.next_plan).toEqual(reportNextPlanItems(get(rid).nextPlanBinding));const before=structuredClone(api.read());expect(()=>save(rid,{content:{...base,next_plan:[{key:"fake",title:"伪清单",goal_id:"G-content"}]}})).toThrow("不可另写");expect(api.read()).toEqual(before);
 save(rid,{content:emptyReportContent(get(rid).nextPlanBinding)});const check=reportContentCheck(api.read(),get(rid));for(const section of ["COMPLETED","UNCOMPLETED","RETRO"])expect(check.issues.some(i=>i.section===section)).toBe(true);expect(()=>command(rid,"已提交")).toThrow("B4");
 expect(reportContentCheck(api.read(),{...get(rid),nextPlanBinding:null,content:emptyReportContent()}).issues.some(i=>i.section==="NEXT_PLAN")).toBe(true);
});
test("fixed structured completion reader retains exact events when current work disappears",()=>{
 const {rid,workId}=fixture();command(rid,"已提交");const before=reportContentFacts(api.read(),get(rid));api.update(s=>{s.work=s.work.filter(w=>w.id!==workId);});expect(reportContentFacts(api.read(),get(rid))).toEqual(before);expect(reportContentCheck(api.read(),get(rid)).issues).toEqual([]);command(rid,"已接受");expect(get(rid).status).toBe("已接受");
});
test("fixed reports survive current event removal while corrupt fixed evidence blocks atomically",()=>{
 const {rid,workId}=fixture();command(rid,"已提交");const original=structuredClone(api.read());
 for(const corrupt of [s=>{delete s.resources.reports.find(r=>r.id===rid).submission.factSnapshot;},s=>s.resources.reports.find(r=>r.id===rid).submission.factSnapshot.sources.push(null),s=>s.resources.reports.find(r=>r.id===rid).submission.factSnapshot.sources.find(x=>x.kind==="work").acceptanceFacts.push(null)]){
  api.update(s=>{Object.assign(s,structuredClone(original));corrupt(s);});const before=structuredClone(api.read());
  expect(reportContentCheck(api.read(),get(rid)).issues.some(i=>i.code==="B1")).toBe(true);expect(()=>command(rid,"已接受")).toThrow("固定报告来源快照");expect(api.read()).toEqual(before);
 }
 for(const events of [[],{},[null]]){
  api.update(s=>{Object.assign(s,structuredClone(original));s.work.find(w=>w.id===workId).acceptanceFacts=events;});
  const fixed=structuredClone(get(rid).submission);command(rid,"已接受");expect(get(rid).status).toBe("已接受");expect(get(rid).submission).toEqual(fixed);
 }
});
test("empty reference entries reject atomically and malformed live events remain unknown",()=>{
 const {rid,workId}=fixture(),before=structuredClone(api.read());
 expect(()=>save(rid,{references:[null,...get(rid).references]})).toThrow("空来源");expect(api.read()).toEqual(before);
 for(const patch of [{reviewedDetailKeys:{}},{reviewedDetailKeys:[null]},{reviewedRetrospective:"true"}]){expect(()=>save(rid,patch)).toThrow("核对");expect(api.read()).toEqual(before);}
 for(const malformed of [null,undefined,"invalid",{},[null]])expect(reportAcceptanceMatch(get(rid),malformed)).toBeNull();
 for(const malformed of [[null],"invalid",{},[null,{id:"broken",subjectKeys:"agent:atlas"}]]){
  api.update(s=>{const work=s.work.find(w=>w.id===workId);work.acceptanceFacts=malformed;work.production={...work.production,executorIds:["agent:atlas"]};});
  const facts=periodicReportFacts(api.read(),get(rid));expect(facts.unknownWorkIds).toContain(workId);expect(facts.facts.some(f=>f.workId===workId)).toBe(false);
  expect(()=>reportContentCheck(api.read(),get(rid))).not.toThrow();
 }
});
test("corrupt event histories stay unknown across Plan, report-with-Plan and OKR readers",()=>{
 const {rid,workId}=fixture(),base=structuredClone(api.read());
 const plan={id:"current-plan",tenantId:base.activeTenant,periodKind:"WEEK",subjectKey:"agent:atlas",timezone:"UTC",periodStart:"2026-10-12",periodEnd:"2026-10-18",confirmedSnapshot:{subjectKey:"agent:atlas",periodKind:"WEEK",timezone:"UTC",periodStart:"2026-10-12",periodEnd:"2026-10-18",revision:1,items:[{id:"current-item",kind:"WORK_ITEM",workId,title:"计划成果",weight:1}]}};
 for(const events of [[null],[{}],[{executorIds:[]}],{},"invalid",[{id:"bad",workId,time:"2026-10-12T02:00:00Z",executorIds:"agent:atlas",subjectKeys:"agent:atlas"}]]){
  const state=structuredClone(base),work=state.work.find(w=>w.id===workId);work.acceptanceFacts=events;work.accepted=true;work.production={...work.production,executorIds:["agent:atlas"]};state.resources.plans=[plan];
  const outcome=planReportFacts(state,plan);expect(outcome.progress).toBeNull();expect(outcome.outcomes[0].status).toBe("未知");
  const facts=periodicReportFacts(state,get(rid));expect(facts.plans[0].progress).toBeNull();
  const metric=readGoalMetric(state,{...get(rid)},{metric:"ACCEPTED_WORK_ITEMS"});expect(metric.availability).toBe("unavailable");expect(metric.value).toBeNull();
 }
 const free={...plan,confirmedSnapshot:{...plan.confirmedSnapshot,items:[{id:"free",kind:"FREE_TASK",title:"研究",freeCategory:"LEARNING",criteria:"保留研究记录",weight:1}]}};
 for(const freeTaskFacts of [[null],[{}],{},"invalid"]){const outcome=planReportFacts(base,{...free,freeTaskFacts});expect(outcome.progress).toBeNull();expect(outcome.outcomes[0].status).toBe("未知");}
 const reassigned=structuredClone(base),work=reassigned.work.find(w=>w.id===workId);work.accepted=false;work.history=[];work.owner="林予";work.production={...work.production,executorIds:["H-lin"]};work.team={...work.team,executorIds:["H-lin"],receiverId:"H-lin"};work.acceptanceFacts=[null,{...work.acceptanceFacts[0],firstKnown:true}];
 const metric=readGoalMetric(reassigned,{...get(rid)},{metric:"ACCEPTED_WORK_ITEMS"});expect(metric.availability).toBe("unavailable");expect(metric.value).toBeNull();
 work.production.executorIds=["agent:atlas"];work.acceptanceFacts=[{executorIds:[]}];expect(planReportFacts(reassigned,plan).progress).toBeNull();
});

test("explicit report Plan references require original fixed items and matching work/type/goal", () => {
 const {rid}=fixture(), r=get(rid);
 const pid=api.openPeriodPlan({subjectKey:r.subjectKey,periodKind:"WEEK",date:r.periodStart,timezone:r.timezone,simulateAgent:true});
 const plan=()=>api.read().resources.plans.find(p=>p.id===pid);
 api.savePeriodPlan(pid,{simulateAgent:true,items:[
  {id:"done-plan",kind:"WORK_ITEM",title:"交付完成",workId:"W-103",goalId:"G-content",krId:"KR-content",weight:1},
  {id:"pending-plan",kind:"WORK_ITEM",title:"下游检查",workId:"W-104",goalId:"G-content",weight:1},
  {id:"free-plan",kind:"FREE_TASK",title:"调研",goalId:"G-content",freeCategory:"LEARNING",criteria:"保留结论",weight:1},
 ]},plan().revision);
 const binding=reportCurrentPlanBinding(api.read(),r,pid,plan().revision), content=structuredClone(r.content);
 content.completed[0].plan_item_id="done-plan"; content.uncompleted[0].plan_item_id="pending-plan";
 save(rid,{currentPlanBinding:binding,content,reviewedDetailKeys:["unfinished"]});
 const valid=structuredClone(get(rid));expect(reportContentCheck(api.read(),valid).issues).toEqual([]);
 const variants=[
  ["COMPLETED","B1",r=>r.content.completed[0].plan_item_id="missing"],
  ["UNCOMPLETED","B1",r=>r.content.uncompleted[0].plan_item_id="missing"],
  ["COMPLETED","B1",r=>r.content.completed[0].plan_item_id="pending-plan"],
  ["COMPLETED","B1",r=>r.content.completed[0].plan_item_id="free-plan"],
  ["UNCOMPLETED","B1",r=>r.content.uncompleted[0].work_item_id="W-103"],
  ["UNCOMPLETED","B1",r=>r.content.uncompleted[0].plan_item_id="free-plan"],
  ["COMPLETED","B5",r=>r.content.completed[0].kr_id="other"],
  ["COMPLETED","B1",r=>delete r.currentPlanBinding],
  ["UNCOMPLETED","B2",r=>r.content.uncompleted[0].plan_item_id="done-plan"],
 ];
 for(const [section,code,alter] of variants){const candidate=structuredClone(valid);alter(candidate);const before=structuredClone(candidate);expect(reportContentCheck(api.read(),candidate).issues.some(i=>i.section===section&&i.code===code&&/计划/.test(i.message))).toBe(true);expect(candidate).toEqual(before);}
 const forged=structuredClone(valid.content);forged.uncompleted[0].plan_item_id="missing";
 save(rid,{content:forged,reviewedDetailKeys:["unfinished"]});
 const rejected=structuredClone(api.read()), persisted=localStorage.getItem("collaboration-prototype-v1");
 expect(()=>command(rid,"已提交")).toThrow("计划项不存在");expect(api.read()).toEqual(rejected);expect(localStorage.getItem("collaboration-prototype-v1")).toBe(persisted);
 api.update(s=>s.resources.plans=s.resources.plans.filter(p=>p.id!==pid));
 expect(reportContentCheck(api.read(),{...valid,submission:{content:{},factSnapshot:api.reportEvidence(api.read(),valid)}}).issues).toEqual([]);
 const offPlan=structuredClone(valid);delete offPlan.currentPlanBinding;offPlan.content.completed[0].plan_item_id="";offPlan.content.uncompleted[0].plan_item_id="";
 expect(reportContentCheck(api.read(),offPlan).issues.some(i=>i.code==="B1"&&/计划/.test(i.message))).toBe(false);
});
