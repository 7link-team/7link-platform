import { test, expect } from "bun:test";
import { normalizePerformancePolicy, performancePeriod, performanceCommandOn, performanceWorkFacts, performancePeriods, performancePeriodKey, performanceScores, performanceTemplate } from "../src/performance.js";
const state = () => ({activeTenant:"t",role:"owner",offline:false,organization:{members:[{id:"H-lin",name:"Member",kind:"Human"}],nodes:[]},agents:[{id:"a",name:"Agent A"}],projects:[{id:"p",name:"Project A"},{id:"other",name:"Project B"}],tenants:[{id:"t",name:"Tenant"}],work:[],resources:{goals:[],plans:[],performancePolicies:[],performanceReports:[]}});
const policy = patch => ({name:"Cycle review",subjectKeys:["human:H-lin","agent:a"],cycles:["周报","月报","季报","年报"],timezone:"Asia/Singapore",baseScore:60,maxScore:100,template:performanceTemplate,rules:[{id:"completed",name:"Accepted work",dimension:"Progress",metric:"COMPLETED_WORK",description:"Count fixed events",points:2,limit:10},{id:"review",name:"Professional review",dimension:"Quality",metric:"REVIEW",description:"Evidence-backed calibration",points:5,limit:4}],...patch});
let sequence=0;const makeId=prefix=>`${prefix}-${++sequence}`;
function command(s,input) {const next=structuredClone(s),result=performanceCommandOn(next,{tenantId:s.activeTenant,reason:"Explicit local evidence review",...input},makeId);Object.assign(s,next);return result;}
const acceptance=(workId="w",patch={})=>({id:`${workId}:v1:accept`,workId,version:1,project:"p",time:"2026-10-09T04:00:00Z",subjectKeys:["tenant:t","project:p","human:H-lin","agent:a"],executorIds:["H-lin","agent:a"],workSnapshot:{id:workId,title:`Fixed ${workId}`,project:"p",version:1,quality:"通过"},...patch});
const work=(patch={})=>({id:"w",project:"p",title:"Current renamed title",status:"已完成",accepted:true,owner:"Member",ownerId:"H-lin",team:{executorIds:["H-lin","agent:a"]},acceptanceFacts:[acceptance()],...patch});
const descriptor=(patch={})=>({...performancePeriod(policy(),"周报","2026-10-07"),cycle:"周报",timezone:"Asia/Singapore",subjectKey:"tenant:t",...patch});
const setup=(s,patch={})=>command(s,{action:"create",...policy(patch)});
const generate=(s,policyId,patch={})=>command(s,{action:"generate",policyId,policyRevision:s.resources.performancePolicies.find(item=>item.id===policyId).revision,subjectKey:"human:H-lin",cycle:"周报",date:"2026-10-07",...patch});
const get=(s,id)=>s.resources.performanceReports.find(report=>report.id===id);
const atomic=(s,fn)=>{const original=structuredClone(s);expect(fn).toThrow();expect(s).toEqual(original);};

test("only Human and Agent may be assessed; multiple cycles and explicit rules stay configurable",()=>{
 const s=state();expect(normalizePerformancePolicy(s,policy()).cycles).toHaveLength(4);
 for(const key of ["tenant:t","project:p","team:missing","human:unknown"])atomic(s,()=>setup(s,{subjectKeys:[key]}));
 for(const patch of [{cycles:[]},{cycles:["日报"]},{cycles:["周报","周报"]},{timezone:"Invalid/Zone"},{baseScore:101},{rules:[{...policy().rules[0],limit:0}]}])atomic(s,()=>setup(s,patch));
});
test("cutoff is last calendar day midnight in policy timezone, never full final day",()=>{
 expect(performancePeriod(policy(),"周报","2026-10-07")).toMatchObject({periodStart:"2026-10-05",periodEnd:"2026-10-11",cutoffAt:"2026-10-10T16:00:00.000Z"});
 for(const [cycle,end] of [["月报","2026-02-28"],["季报","2026-03-31"],["年报","2026-12-31"]])expect(performancePeriod(policy(),cycle,"2026-02-10").periodEnd).toBe(end);
 const s=state();s.work=[work({acceptanceFacts:[acceptance("w",{time:"2026-10-10T16:00:00Z"})]})];expect(performanceWorkFacts(s,descriptor()).completed).toBe(0);s.work[0].acceptanceFacts[0].time="2026-10-10T15:59:59.999Z";expect(performanceWorkFacts(s,descriptor()).completed).toBe(1);
});
test("current completed, ongoing, delayed and schedule history never masquerade as historical facts",()=>{
 const s=state();s.work=[work({acceptanceFacts:[],status:"已完成",plannedEnd:"2020-01-01",scheduleHistory:[{start:"2020-01-01",end:"2020-01-02"}]}),work({id:"ongoing",accepted:false,status:"进行中",acceptanceFacts:[]})];
 const facts=performanceWorkFacts(s,descriptor());expect(facts.completed).toBe(0);expect(facts.inProgress).toBe(0);expect(facts.delayed).toBe(0);expect(facts.rows).toHaveLength(2);for(const row of facts.rows)expect(row).toMatchObject({completed:null,inProgress:null,delayed:null,changed:null,projectId:null,scopeKnown:false});
 const policyId=setup(s),id=generate(s,policyId);expect(get(s,id).scoring.score).toBeNull();expect(get(s,id).facts.unknownWorkIds.sort()).toEqual(["ongoing","w"]);
});
test("fixed project and title win over current reassignment and future-created work stays outside historical period",()=>{
 const s=state();s.work=[work({project:"other"}),work({id:"future",createdAt:"2026-10-12T00:00:00Z",acceptanceFacts:[]})];
 const facts=performanceWorkFacts(s,descriptor());expect(facts.rows).toHaveLength(1);expect(facts.rows[0]).toMatchObject({title:"Fixed w",projectId:"p",completed:true});
 const human=performanceWorkFacts(s,descriptor({subjectKey:"human:H-lin"}));expect(human.completed).toBe(1);
 s.work[0].acceptanceFacts[0].executorIds=["agent:a"];expect(performanceWorkFacts(s,descriptor({subjectKey:"human:H-lin"})).completed).toBe(0);
});
const decision=(patch={})=>({id:"decision",kind:"DECISION",tenantId:"t",project:"p",workId:"w",version:1,actor:{kind:"Human",id:"H-lin"},time:"2026-10-08T02:00:00Z",source:"LOCAL_SIMULATION",subjectKeys:["tenant:t","project:p","human:H-lin"],workSnapshot:{id:"w",title:"At decision time",version:1,project:"p"},originalCommand:"resume",action:"RESUME",fromState:"等待",toState:"进行中",reason:"Explicit continuation",...patch});
test("period progress and changes retain dated decision evidence and never grant decision actor execution credit",()=>{
 const s=state();s.work=[work({accepted:false,status:"已取消",acceptanceFacts:[],decisionFacts:[decision(),decision({id:"cancel",time:"2026-10-09T03:00:00Z",originalCommand:"cancel",action:"CANCEL",fromState:"进行中",toState:"已取消"})]})];
 const facts=performanceWorkFacts(s,descriptor());expect(facts).toMatchObject({completed:0,inProgress:1,changed:1});expect(facts.rows[0].evidence.every(entry=>entry.role==="裁决操作人")).toBe(true);
 expect(performanceWorkFacts(s,descriptor({subjectKey:"agent:a"})).inProgress).toBe(0);
 s.work[0].decisionFacts[0].time="invalid";expect(performanceWorkFacts(s,descriptor()).inProgress).toBe(0);
});
function fixedPlan(dueDate="2026-10-08",patch={}) {
 const snapshot={subjectKey:"human:H-lin",periodKind:"WEEK",periodStart:"2026-10-05",periodEnd:"2026-10-11",timezone:"Asia/Singapore",revision:1,time:"2026-10-05T01:00:00Z",items:[{id:"item",kind:"WORK_ITEM",workId:"w",title:"Committed work",weight:1,dueDate}],...patch};
 return {id:"plan",tenantId:"t",status:"已确认",...snapshot,confirmedSnapshot:snapshot};
}
test("delay needs a valid prior fixed commitment and actual subsequent acceptance, not present-day due date",()=>{
 const s=state();s.work=[work()];s.resources.plans=[fixedPlan()];expect(performanceWorkFacts(s,descriptor()).rows[0].delayed).toBe(true);
 s.resources.plans=[fixedPlan("2026-10-09")];expect(performanceWorkFacts(s,descriptor()).rows[0].delayed).toBe(false);
 s.resources.plans=[fixedPlan("2026-10-08",{time:"2026-10-09T05:00:00Z"})];expect(performanceWorkFacts(s,descriptor()).rows[0].delayed).toBeNull();
 s.resources.plans=[fixedPlan("2026-02-30")];expect(performanceWorkFacts(s,descriptor()).rows[0].delayed).toBeNull();
});
test("local-day deadline honors daylight saving rather than a fixed 24-hour offset",()=>{
 const s=state();s.work=[work({acceptanceFacts:[acceptance("w",{time:"2025-11-03T04:30:00Z"})]})];s.resources.plans=[fixedPlan("2025-11-02",{periodKind:"MONTH",periodStart:"2025-11-01",periodEnd:"2025-11-30",timezone:"America/New_York",time:"2025-11-01T05:00:00Z"})];
 const period=performancePeriod(policy({timezone:"America/New_York"}),"月报","2025-11-10");expect(performanceWorkFacts(s,{...period,cycle:"月报",timezone:"America/New_York",subjectKey:"tenant:t"}).rows[0].delayed).toBe(false);
});
test("batch generation is explicit, idempotent and periods sort newest first without tenant/project scores",()=>{
 const s=state();s.work=[work()];const policyId=setup(s);command(s,{action:"generate-period",policyId,policyRevision:1,cycle:"周报",date:"2026-10-07"});expect(s.resources.performanceReports).toHaveLength(2);
 const original=structuredClone(s.resources.performanceReports);command(s,{action:"generate-period",policyId,policyRevision:1,cycle:"周报",date:"2026-10-07"});expect(s.resources.performanceReports).toEqual(original);
 generate(s,policyId,{cycle:"月报",date:"2026-09-12"});const periods=performancePeriods(s);expect(periods.map(item=>item.periodEnd)).toEqual(["2026-10-11","2026-09-30"]);expect(periods[0].reports).toHaveLength(2);expect(periods[0].key).toBe(performancePeriodKey(periods[0].reports[0]));
 for(const report of s.resources.performanceReports){expect(report.subject.kind).not.toBe("租户");expect(report.subject.kind).not.toBe("项目");expect(report.overviewSnapshot).not.toHaveProperty("score");}
});
test("calibration, confirmation and regeneration preserve old policy and fixed report revisions",()=>{
 const s=state();s.work=[work()];const policyId=setup(s),id=generate(s,policyId);expect(get(s,id).scoring.score).toBeNull();
 command(s,{action:"calibrate",reportId:id,revision:1,ruleId:"review",value:3,evidence:"https://example.test/evidence"});expect(get(s,id).scoring.score).toBe(77);
 command(s,{action:"confirm",reportId:id,revision:2});const confirmed=structuredClone(get(s,id));expect(confirmed.status).toBe("已确认");expect(confirmed.history.at(-1).action).toBe("CONFIRM");
 command(s,{action:"calibrate",reportId:id,revision:3,ruleId:"review",value:2,evidence:"https://example.test/revised"});expect(get(s,id).history.at(-1).before.confirmation).toEqual(confirmed.confirmation);expect(get(s,id).confirmation).toBeUndefined();expect(get(s,id).status).toBe("待复核");
 const frozen=structuredClone(get(s,id));command(s,{action:"save",...policy({name:"Revised policy",baseScore:50}),id:policyId,revision:1});expect(get(s,id)).toEqual(frozen);
 generate(s,policyId,{regenerate:true,revision:4});expect(get(s,id).policySnapshot.revision).toBe(2);expect(get(s,id).history.at(-1).before.body).toBe(frozen.body);expect(get(s,id).assessments).toEqual({});
});
test("owner, tenant, revision, evidence and future-cutoff gates reject atomically",()=>{
 const s=state();s.work=[work()];const policyId=setup(s),id=generate(s,policyId);
 for(const patch of [{role:"executor"},{role:"external"},{offline:true}]){const original={role:s.role,offline:s.offline};Object.assign(s,patch);atomic(s,()=>generate(s,policyId));Object.assign(s,original);}
 for(const input of [{action:"generate",policyId,policyRevision:1,subjectKey:"human:H-lin",cycle:"周报",date:"2099-10-07"},{action:"confirm",reportId:id,revision:1},{action:"confirm",reportId:id,revision:999},{action:"calibrate",reportId:id,revision:1,ruleId:"completed",value:2,evidence:"https://example.test/evidence"},{action:"calibrate",reportId:id,revision:1,ruleId:"review",value:5,evidence:"https://example.test/evidence"},{action:"calibrate",reportId:id,revision:1,ruleId:"review",value:1,evidence:"https://user:secret@example.test/evidence"},{action:"calibrate",reportId:id,revision:1,ruleId:"review",value:1,evidence:"javascript:alert(1)"},{action:"generate",tenantId:"foreign",policyId,policyRevision:1,subjectKey:"human:H-lin",cycle:"周报",date:"2026-10-07"}])atomic(s,()=>command(s,input));
});
test("frozen work summary and report body stay independent of later current source mutation",()=>{
 const s=state();s.work=[work()];const policyId=setup(s),id=generate(s,policyId),report=structuredClone(get(s,id));s.work[0].acceptanceFacts[0].workSnapshot.title="Later changed";s.work[0].acceptanceFacts=[];s.work[0].status="进行中";s.work[0].project="other";
 expect(get(s,id)).toEqual(report);expect(get(s,id).workOverview.rows[0].title).toBe("Fixed w");
});
test("work duplicates do not inflate count and unknown quality never becomes a fabricated score",()=>{
 const p=policy({rules:[{...policy().rules[0],metric:"QUALITY"}]});const facts={facts:[acceptance(),acceptance("w",{id:"w:v2:accept",version:2,workSnapshot:{quality:"通过"}})],unknownWorkIds:[]};expect(performanceScores(p,facts).lines[0].count).toBe(1);
 facts.facts[1].workSnapshot.quality=null;expect(performanceScores(p,facts).score).toBeNull();
});

test("malformed fixed acceptance snapshots stay unknown and cannot earn completion points",()=>{
 const s=state();s.work=[work({acceptanceFacts:[acceptance("w",{workSnapshot:{title:"unverifiable title",quality:"通过"}})]})];
 const summary=performanceWorkFacts(s,descriptor());expect(summary.completed).toBe(0);expect(summary.rows[0].completed).toBeNull();
 const policyId=setup(s),id=generate(s,policyId);expect(get(s,id).facts.facts).toHaveLength(0);expect(get(s,id).facts.unknownWorkIds).toContain("w");expect(get(s,id).scoring.score).toBeNull();
});
test("the actual store wrapper commits batch generation once and keeps runtime and storage unchanged on rejection",async()=>{
 const previous=Object.getOwnPropertyDescriptor(globalThis,"localStorage"),storage=new Map();let fail=false;
 Object.defineProperty(globalThis,"localStorage",{configurable:true,writable:true,value:{getItem:key=>storage.get(key)||null,setItem:(key,value)=>{if(fail){const error=Error("full");error.name="QuotaExceededError";throw error;}storage.set(key,value);},removeItem:key=>storage.delete(key)}});
 try {
  const api=await import("../src/store.js");api.reset();const tenantId=api.read().activeTenant;
  const input={...policy({subjectKeys:["human:H-lin","agent:atlas"],rules:[policy().rules[1]]}),tenantId,reason:"Actual wrapper evidence",action:"create"};const policyId=api.performanceCommand(input);
  const reportId=api.performanceCommand({action:"generate-period",tenantId,policyId,policyRevision:1,cycle:"周报",date:"2026-10-07",reason:"Local batch generation"});expect(api.read().resources.performanceReports).toHaveLength(2);expect(api.read().resources.performanceReports.some(report=>report.id===reportId)).toBe(true);
  const before=structuredClone(api.read()),saved=[...storage];expect(()=>api.performanceCommand({action:"generate",tenantId:"wrong",policyId,policyRevision:1,subjectKey:"human:H-lin",cycle:"周报",date:"2026-10-07",reason:"wrong tenant"})).toThrow("租户");expect(api.read()).toEqual(before);expect([...storage]).toEqual(saved);
  fail=true;expect(()=>api.performanceCommand({...input,name:"Will not persist"})).toThrow("空间不足");expect(api.read()).toEqual(before);expect([...storage]).toEqual(saved);
 } finally {if(previous)Object.defineProperty(globalThis,"localStorage",previous);else delete globalThis.localStorage;}
});

test("tenant work overview includes other members and projects outside policy scoring subjects",()=>{
 const s=state();s.organization.members.push({id:"H-other",name:"Other member",kind:"Human"});
 s.work=[work(),work({id:"other-work",project:"other",owner:"Other member",ownerId:"H-other",team:{executorIds:["H-other"]},acceptanceFacts:[acceptance("other-work",{project:"other",subjectKeys:["tenant:t","project:other","human:H-other"],executorIds:["H-other"],workSnapshot:{id:"other-work",version:1,project:"other",title:"Outside policy work",quality:"通过"}})]})];
 const policyId=setup(s,{subjectKeys:["human:H-lin"]}),id=generate(s,policyId),report=get(s,id);
 expect(report.workOverview.completed).toBe(1);expect(report.overviewSnapshot.completed).toBe(2);expect(report.overviewSnapshot.rows.find(row=>row.workId==="other-work").projectId).toBe("other");expect(report.subject.key).toBe("human:H-lin");
});
