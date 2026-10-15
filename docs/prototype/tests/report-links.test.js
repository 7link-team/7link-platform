import { beforeEach, expect, test } from "bun:test";
import { reportChildProblem, submittedReportBody } from "../src/reporting.js";
import { reportSubmissionContent } from "../src/report-authorization.js";
if (!globalThis.localStorage) {
 const entries=new Map();globalThis.localStorage={getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)};
}
const api=await import("../src/store.js?report-links");
beforeEach(api.reset);
const descriptor=(patch={})=>({id:"parent",name:"十月",subjectKey:"human:H-lin",cadence:"月报",timezone:"UTC",periodStart:"2026-10-01",periodEnd:"2026-10-31",status:"草稿",revision:1,references:[],...patch});
const state=()=>{const s=structuredClone(api.read());s.resources.reports=[];s.resources.worklogs=[];for(const w of s.work)w.acceptanceFacts=[];return s;};
const fixed=(s,r)=>{const record={...r,status:"已提交"};record.submission={revision:record.revision,authorId:"H-lin",authorKind:"Human",content:reportSubmissionContent(record),factSnapshot:api.reportEvidence(s,record)};s.resources.reports.push(record);return record;};
const event=(w,time,subject="human:H-lin",project=w.project)=>({id:`${w.id}:v${w.version}:accept`,workId:w.id,version:w.version,time,project,executorIds:[subject.startsWith("human:")?subject.slice(6):subject],subjectKeys:[subject,`project:${project}`,"tenant:studio"],workSnapshot:{title:w.title,plan:true,quality:"通过"}});
test("every personal subject rejects foreign reports, drafts, legacy accepted labels and cadence shortcuts",()=>{
 const s=state(),parent=descriptor();const child=fixed(s,descriptor({id:"child",cadence:"周报",periodStart:"2026-09-28",periodEnd:"2026-10-04"}));
 expect(reportChildProblem(s,parent,child)).toBeNull();
 for(const subjectKey of ["human:H-zhou","agent:atlas","project:launch"]){const other=fixed(s,{...child,id:subjectKey,subjectKey});expect(reportChildProblem(s,parent,other)).toContain("个人报告不能");}
 for(const status of ["草稿","退回"]){expect(reportChildProblem(s,parent,{...child,status})).toContain("固定提交");}
 expect(reportChildProblem(s,parent,{...child,status:"已接受",submission:null})).toContain("固定提交");
 expect(reportChildProblem(s,parent,fixed(s,descriptor({id:"day",cadence:"日报",periodStart:"2026-10-02",periodEnd:"2026-10-02"})))).toContain("相邻");
 expect(reportChildProblem(s,parent,fixed(s,descriptor({id:"outside",cadence:"周报",periodStart:"2026-09-21",periodEnd:"2026-09-27"})))).toContain("没有交集");
});
test("cross-month reports clip actual work and effort dates and deduplicate direct and child paths",()=>{
 const s=state(),[sept,oct]=s.work;
 sept.acceptanceFacts=[event(sept,"2026-09-30T08:00:00Z")];oct.acceptanceFacts=[event(oct,"2026-10-02T08:00:00Z")];
 s.resources.worklogs=[{id:"sept",name:"九月投入",work:sept.id,date:"2026-09-30",timezone:"UTC",ownerId:"H-lin",hours:3,status:"已接受"},{id:"oct",name:"十月投入",work:oct.id,date:"2026-10-02",timezone:"UTC",ownerId:"H-lin",hours:1,status:"已接受"}];
 const refs=s.work.slice(0,2).map(w=>({kind:"work",id:w.id,version:w.version,acceptanceFactIds:[w.acceptanceFacts[0].id]}));
 const child=fixed(s,descriptor({id:"child",cadence:"周报",periodStart:"2026-09-28",periodEnd:"2026-10-04",claimedCompleted:2,references:[...refs,{kind:"worklog",id:"sept",version:1},{kind:"worklog",id:"oct",version:1}]}));
 const parent=descriptor({claimedCompleted:1,references:[refs[1],{kind:"worklog",id:"oct",version:1},{kind:"report",id:child.id,version:1},{kind:"report",id:child.id,version:1}]});
 const result=api.reportEvidence(s,parent);expect(result.errors).toEqual([]);expect(result.totals).toMatchObject({completed:1,works:1,human:1,acceptanceFactKeys:[oct.acceptanceFacts[0].id]});
 expect(result.sources.some(source=>source.id===sept.id||source.id==="sept")).toBe(false);
 child.references=[];child.claimedCompleted=0;child.summary="后来被修改的正文";
 expect(api.reportEvidence(s,parent).totals).toEqual(result.totals);expect(submittedReportBody(child).claimedCompleted).toBe(2);
});
test("project hierarchy requires fixed period scope and excludes unrelated project contributions",()=>{
 const s=state(),[a,b]=s.work;a.acceptanceFacts=[event(a,"2026-10-02T08:00:00Z","human:H-lin","launch")];b.acceptanceFacts=[event(b,"2026-10-03T08:00:00Z","human:H-lin","film")];
 const child=fixed(s,descriptor({id:"member",references:[a,b].map(w=>({kind:"work",id:w.id,version:w.version,acceptanceFactIds:[w.acceptanceFacts[0].id]}))}));
 const parent=descriptor({subjectKey:"project:launch",references:[{kind:"report",id:child.id,version:1}],claimedCompleted:1});
 expect(reportChildProblem(s,parent,child)).toBeNull();expect(api.reportEvidence(s,parent).totals).toMatchObject({completed:1,works:1});
 const unrelated=descriptor({subjectKey:"project:unproven"});expect(reportChildProblem(s,unrelated,child)).not.toBeNull();
 child.submission.factSnapshot.sources=[];expect(reportChildProblem(s,parent,child)).toContain("固定归属证据");
});
test("cross-zone period overlap uses actual local midnight boundaries including DST",()=>{
 const s=state(),parent=descriptor({cadence:"周报",periodStart:"2026-10-12",periodEnd:"2026-10-18"});
 const child=fixed(s,descriptor({id:"zone",cadence:"日报",periodStart:"2026-10-19",periodEnd:"2026-10-19",timezone:"Pacific/Kiritimati"}));
 expect(reportChildProblem(s,parent,child)).toBeNull();
 const noOverlap=fixed(s,descriptor({id:"zone2",cadence:"日报",periodStart:"2026-10-20",periodEnd:"2026-10-20",timezone:"Pacific/Kiritimati"}));
 expect(reportChildProblem(s,parent,noOverlap)).toContain("没有交集");
 const dstParent=descriptor({cadence:"周报",periodStart:"2026-11-02",periodEnd:"2026-11-08",timezone:"UTC"});
 const dst=fixed(s,descriptor({id:"dst",cadence:"日报",periodStart:"2026-11-01",periodEnd:"2026-11-01",timezone:"America/New_York"}));
 expect(reportChildProblem(s,dstParent,dst)).toBeNull();
});
test("invalid child links are rejected during save and submission without mutation",()=>{
 const id=api.generatePeriodicReport({subjectKey:"human:H-lin",cadence:"周报",date:"2026-10-14",timezone:"UTC"});
 const child=api.generatePeriodicReport({subjectKey:"human:H-zhou",cadence:"日报",date:"2026-10-14",timezone:"UTC"});
 api.update(s=>{const r=s.resources.reports.find(r=>r.id===child);r.status="已提交";r.submission={content:reportSubmissionContent(r)};});
 const get=()=>api.read().resources.reports.find(r=>r.id===id);const references=[{kind:"report",id:child,version:1}],before=structuredClone(api.read());
 expect(()=>api.saveResource("reports",{...get(),references})).toThrow("个人报告不能");expect(api.read()).toEqual(before);
 api.update(s=>{s.resources.reports.find(r=>r.id===id).references=references;});const injected=structuredClone(api.read());
 expect(()=>api.resourceCommand("reports",id,"已提交","核对事实")).toThrow("个人报告不能");expect(api.read()).toEqual(injected);
});
test("every intermediate hierarchy scope constrains work and effort, not only the tenant root",()=>{
 const s=state(),[a,b]=s.work;a.acceptanceFacts=[event(a,"2026-10-02T08:00:00Z","human:H-lin","launch")];b.acceptanceFacts=[event(b,"2026-10-03T08:00:00Z","human:H-lin","film")];
 s.resources.worklogs=[{id:"launch-hours",name:"本项目",work:a.id,date:"2026-10-02",timezone:"UTC",ownerId:"H-lin",hours:2,status:"已接受",scopeSnapshot:{subjectKeys:["human:H-lin","project:launch","tenant:studio"]}},{id:"film-hours",name:"其他项目",work:b.id,date:"2026-10-03",timezone:"UTC",ownerId:"H-lin",hours:3,status:"已接受",scopeSnapshot:{subjectKeys:["human:H-lin","project:film","tenant:studio"]}}];
 const member=fixed(s,descriptor({id:"member",claimedCompleted:2,references:[... [a,b].map(w=>({kind:"work",id:w.id,version:w.version})),...s.resources.worklogs.map(log=>({kind:"worklog",id:log.id,version:1}))]}));
 const project=fixed(s,descriptor({id:"project",subjectKey:"project:launch",claimedCompleted:1,references:[{kind:"report",id:member.id,version:1}]}));
 const parent=descriptor({subjectKey:"tenant:studio",claimedCompleted:1,references:[{kind:"report",id:project.id,version:1}]});
 const result=api.reportEvidence(s,parent);expect(result.errors).toEqual([]);expect(result.totals).toMatchObject({completed:1,human:2,works:1});
 expect(result.sources.some(source=>source.id===b.id||source.id==="film-hours")).toBe(false);
});
test("fixed work and effort survive changed or deleted live sources and preserve omitted completion claims",()=>{
 const s=state(),w=s.work[0];w.acceptanceFacts=[event(w,"2026-10-02T08:00:00Z")];
 s.resources.worklogs=[{id:"hours",name:"固定投入",work:w.id,date:"2026-10-02",timezone:"UTC",ownerId:"H-lin",hours:2,status:"已接受"}];
 const child=fixed(s,descriptor({id:"child",cadence:"周报",periodStart:"2026-09-28",periodEnd:"2026-10-04",references:[{kind:"work",id:w.id,version:w.version},{kind:"worklog",id:"hours",version:1}]}));
 expect(child.submission.content.claimedCompleted).toBeNull();
 const parent=descriptor({claimedCompleted:1,references:[{kind:"report",id:child.id,version:1}]});
 const before=structuredClone(child.submission);s.resources.worklogs[0].hours=7;s.resources.worklogs[0].revision=2;
 let result=api.reportEvidence(s,parent);expect(result.errors).toEqual([]);expect(result.totals).toMatchObject({human:2,completed:1});
 s.resources.worklogs=[];s.work=s.work.filter(item=>item.id!==w.id);result=api.reportEvidence(s,parent);
 expect(result.errors).toEqual([]);expect(result.totals).toMatchObject({human:2,completed:1});expect(child.submission).toEqual(before);expect(result.warnings.some(warning=>warning.includes("不可用"))).toBe(true);
});
test("partially overlapping cross-zone daily effort is explicitly unknown and never silently zero",()=>{
 const s=state(),w=s.work[0];s.resources.worklogs=[{id:"K",name:"跨区投入",work:w.id,date:"2026-10-19",timezone:"Pacific/Kiritimati",ownerId:"H-lin",hours:2,status:"已接受"}];
 const child=fixed(s,descriptor({id:"child",cadence:"日报",periodStart:"2026-10-19",periodEnd:"2026-10-19",timezone:"Pacific/Kiritimati",references:[{kind:"worklog",id:"K",version:1}]}));
 const parent=descriptor({cadence:"周报",periodStart:"2026-10-12",periodEnd:"2026-10-18",references:[{kind:"report",id:child.id,version:1}]});
 const result=api.reportEvidence(s,parent);expect(result.errors).toEqual([]);expect(result.totals.unknownEffortIds).toEqual(["K"]);expect(result.totals.human).toBe(0);expect(result.warnings.some(warning=>warning.includes("时区边界"))).toBe(true);
});
test("duplicate child references remain bounded through ten hierarchy levels",()=>{
 const s=state(),keys=Array.from({length:11},(_,i)=>`team:chain-${i}`);s.organization.nodes.push(...keys.map(key=>({id:key.slice(5),name:key,memberIds:[]})));
 const w=s.work[0];s.resources.worklogs=[{id:"hours",name:"投入",work:w.id,date:"2026-10-13",timezone:"UTC",hours:1,status:"已接受",scopeSnapshot:{subjectKeys:keys}}];
 let child=fixed(s,descriptor({id:"leaf",subjectKey:keys[10],cadence:"日报",periodStart:"2026-10-13",periodEnd:"2026-10-13",references:[{kind:"worklog",id:"hours",version:1}]}));
 for(let i=9;i>=1;i--)child=fixed(s,descriptor({id:`child-${i}`,subjectKey:keys[i],cadence:"日报",periodStart:"2026-10-13",periodEnd:"2026-10-13",references:[{kind:"report",id:child.id,version:1},{kind:"report",id:child.id,version:1}]}));
 let reads=0;for(const r of s.resources.reports){const content=r.submission.content;Object.defineProperty(r.submission,"content",{get(){reads++;return content}});}
 const root=descriptor({subjectKey:keys[0],cadence:"日报",periodStart:"2026-10-13",periodEnd:"2026-10-13",references:[{kind:"report",id:child.id,version:1},{kind:"report",id:child.id,version:1}]});
 const result=api.reportEvidence(s,root);expect(result.errors).toEqual([]);expect(result.totals.human).toBe(1);expect(reads).toBeLessThan(1000);
});
