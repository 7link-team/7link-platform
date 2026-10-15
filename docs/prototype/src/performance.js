import { reportSubjects, reportPeriod, periodicReportFacts, localDayStart } from "./reporting";

import { planConfirmedSnapshotProblem } from "./plans";
import { reportOperationFactValid } from "./report-content";

export const performanceCycles=["周报","月报","季报","年报"];
export const performanceMetrics={COMPLETED_WORK:"协作进度 · 已接受的独立工作",QUALITY:"交付质量 · 固定接受版本检查通过",REVIEW:"专业评估 · 有依据的人工校准"};
export const performanceTemplate="{{subject}} · {{period}} 绩效报告\n评分：{{score}}\n{{details}}\n数据截止：{{cutoff}}";
const number=(value,label)=>{if(value===""||value==null||!Number.isFinite(Number(value)))throw Error(`${label}须为有效数值。`);return Number(value);};
export function performanceOwnerProblem(s) {
  return s.role!=="owner" || !s.organization.members.some(p=>p.id==="H-lin"&&p.kind==="Human") ? "考核方案与报告由当前租户 owner 管理。" : s.offline ? "离线中，保留阅读；恢复后再配置或确认。" : "";
}
export function normalizePerformancePolicy(s,input) {
  if(!input.name?.trim()||!input.template?.trim())throw Error("请填写考核方案名称与报告模板。");
  if(input.template.length>12000)throw Error("报告模板超过 12000 字符。");
  const subjects=reportSubjects(s).filter(subject => ["Human", "Agent"].includes(subject.kind));
  if(!Array.isArray(input.subjectKeys)||!input.subjectKeys.length||new Set(input.subjectKeys).size!==input.subjectKeys.length||input.subjectKeys.some(key=>!subjects.some(p=>p.key===key)))throw Error("请选择当前租户中的 Human 或 Agent；租户与项目只汇总，不参与评分。");
  if(!Array.isArray(input.cycles)||!input.cycles.length||new Set(input.cycles).size!==input.cycles.length||input.cycles.some(c=>!performanceCycles.includes(c)))throw Error("请选择有效、非重复的考核周期。");
  try {new Intl.DateTimeFormat("en",{timeZone:input.timezone}).format();}catch {throw Error("请选择有效 IANA 时区。");}
  if(!input.timezone)throw Error("请明确考核时区。");
  const baseScore=number(input.baseScore,"基础分"),maxScore=number(input.maxScore,"总分上限");
  if(maxScore<=0||maxScore>10000||baseScore<0||baseScore>maxScore)throw Error("基础分须在 0 至总分上限之间，上限不超过 10000。");
  if(!Array.isArray(input.rules)||!input.rules.length||input.rules.length>30||new Set(input.rules.map(r=>r.id)).size!==input.rules.length)throw Error("请配置 1 至 30 条不重复的考核细则。");
  const rules=input.rules.map(r=>{
    if(!r.id||!r.name?.trim()||!r.dimension?.trim()||!Object.hasOwn(performanceMetrics,r.metric)||!r.description?.trim())throw Error("每条细则须明确维度、条件 / 依据、名称与证据来源。");
    const points=number(r.points,"单次加减分"),limit=number(r.limit,"计分次数上限");
    if(Math.abs(points)>maxScore||!Number.isInteger(limit)||limit<1||limit>10000)throw Error("加减分绝对值不能超过总分上限；次数上限须为 1 至 10000 整数。");
    return {id:r.id,name:r.name.trim(),dimension:r.dimension.trim(),metric:r.metric,description:r.description.trim(),points,limit};
  });
  return {name:input.name.trim(),subjectKeys:[...input.subjectKeys],cycles:[...input.cycles],timezone:input.timezone,baseScore,maxScore,rules,template:input.template.trim(),enabled:input.enabled!==false};
}
export function performancePeriod(policy,cycle,date) {
  if(!policy.cycles.includes(cycle))throw Error("该周期未在方案中启用。");
  const period=reportPeriod(cycle,date),cutoff=localDayStart(period.periodEnd,policy.timezone);
  return {...period,cutoffAt:new Date(cutoff).toISOString(),triggerLabel:`${period.periodEnd} 00:00 · ${policy.timezone}`,uncovered:`${period.periodEnd} 全天尚未覆盖；补录截点前的证据后可有理由重新计算，但不会扩展截点。`};
}

function fixedPerformanceFacts(state, report, cutoff) {
  const result=periodicReportFacts(state,report,new Date(cutoff-1));
  result.facts=result.facts.filter(fact=>{
    const fixed=fact.workSnapshot;
    const valid=Number.isSafeInteger(fact.version) && fact.version>0 && typeof fact.project==="string" && fact.project &&
      fixed?.id===fact.workId && fixed.version===fact.version && fixed.project===fact.project && typeof fixed.title==="string" && fixed.title.trim() &&
      fact.subjectKeys.includes(`tenant:${state.activeTenant}`) && fact.subjectKeys.includes(`project:${fact.project}`);
    if(!valid && !result.unknownWorkIds.includes(fact.workId))result.unknownWorkIds.push(fact.workId);
    return valid;
  });
  result.factKeys=result.facts.map(fact=>fact.id);result.completed=new Set(result.facts.map(fact=>fact.workId)).size;
  return result;
}
export const performanceSubjects = state => reportSubjects(state).filter(subject => ["Human", "Agent"].includes(subject.kind));
export const performancePeriodKey = report => JSON.stringify([report.cycle, report.periodStart, report.periodEnd, report.timezone || report.policySnapshot?.timezone]);
export function performancePeriods(state) {
  const groups = new Map();
  for (const report of state.resources.performanceReports || []) {
    if (report.tenantId !== state.activeTenant || !/^(human|agent):/.test(report.subject?.key || "")) continue;
    const key = performancePeriodKey(report);
    if (!groups.has(key)) groups.set(key, { key, cycle: report.cycle, periodStart: report.periodStart, periodEnd: report.periodEnd, timezone: report.timezone || report.policySnapshot.timezone, reports: [] });
    groups.get(key).reports.push(report);
  }
  return [...groups.values()].sort((a,b) => b.periodEnd.localeCompare(a.periodEnd) || b.periodStart.localeCompare(a.periodStart) || a.cycle.localeCompare(b.cycle));
}
// These are period occurrences, not reconstructed end-of-period state. Missing
// timestamps and scope identities stay unknown; present-day status is never evidence.
export function performanceWorkFacts(state, descriptor) {
  const cutoff = Date.parse(descriptor.cutoffAt), start = localDayStart(descriptor.periodStart, descriptor.timezone);
  if (!Number.isFinite(cutoff) || cutoff < start) throw Error("绩效周期截点无效。");
  const inPeriod = time => Number.isFinite(Date.parse(time)) && Date.parse(time) >= start && Date.parse(time) < cutoff;
  const subjectKey = descriptor.subjectKey || `tenant:${state.activeTenant}`;
  const report = { ...descriptor, subjectKey, cadence: descriptor.cycle };
  // The last-day midnight boundary is exclusive: events exactly at the cutoff
  // belong to uncovered time, even though ordinary periodic reports include it.
  const accepted = fixedPerformanceFacts(state, report, cutoff);
  const personalId = subjectKey.startsWith("human:") ? subjectKey.slice(6) : subjectKey.startsWith("agent:") ? subjectKey : null;
  const candidate = work => subjectKey === `tenant:${state.activeTenant}` || subjectKey === `project:${work.project}` ||
    personalId && [...(work.team?.executorIds || []), ...(work.production?.executorIds || []), work.ownerId, ...(state.organization.members || []).filter(person=>person.name===work.owner).map(person=>person.id)].includes(personalId);
  const rows = [];
  for (const work of state.work) {
    if (Number.isFinite(Date.parse(work.createdAt)) && Date.parse(work.createdAt) >= cutoff) continue;
    const facts = accepted.facts.filter(fact=>fact.workId===work.id);
    const rawDecisions = work.decisionFacts;
    const decisions = (Array.isArray(rawDecisions) ? rawDecisions : []).filter(fact=>reportOperationFactValid(fact) && fact.kind==="DECISION" && fact.workId===work.id && fact.tenantId===state.activeTenant && fact.subjectKeys.includes(subjectKey) && inPeriod(fact.time));
    // A decision is the actor's contribution, never evidence that they executed
    // the work. Keep the role on every evidence row for the presentation.
    if (!facts.length && !decisions.length && !candidate(work)) continue;
    const evidence = [...facts.map(fact=>({id:fact.id,time:fact.time,kind:"接受",role:"固定执行编组",projectId:fact.project,title:fact.workSnapshot?.title,version:fact.version,href:`/p/${fact.project}/artifacts/${work.id}?version=${fact.version}`})),
      ...decisions.map(fact=>({id:fact.id,time:fact.time,kind:fact.action,role:"裁决操作人",projectId:fact.project,title:fact.workSnapshot.title,version:fact.version,reason:fact.reason,href:`/p/${fact.project}/work-items/${work.id}`}))].sort((a,b)=>a.time.localeCompare(b.time));
    const latest = evidence.at(-1), projects = [...new Set(evidence.map(entry=>entry.projectId).filter(Boolean))];
    const projectId = projects.length===1 ? projects[0] : null;
    const issues = [];
    if (!latest) issues.push("缺少本周期带时间与归属的固定事实；当前目录信息仅供补证，不证明当期状态。");
    if (latest && !projectId) issues.push("固定事实项目归属缺失或冲突，不能归入当前项目。");
    if (accepted.unknownWorkIds.includes(work.id)) issues.push("接受历史缺失或损坏，完成数量可能不完整。");
    if (rawDecisions != null && (!Array.isArray(rawDecisions) || rawDecisions.some(fact=>!reportOperationFactValid(fact)))) issues.push("裁决历史损坏，未据此推断进行中或变更。");
    const completed = facts.length ? true : null;
    const inProgress = decisions.some(fact=>fact.action==="RESUME" && fact.toState==="进行中") ? true : null;
    const changed = decisions.some(fact=>["REQUEST_CHANGES","CANCEL"].includes(fact.action)) ? true : null;
    let delayed = null;
    const commitments = [];
    for (const plan of state.resources.plans || []) {
      if (plan.tenantId !== state.activeTenant) continue;
      const baselines = [plan.confirmedSnapshot, ...(Array.isArray(plan.history) ? plan.history : []).filter(entry=>entry.decision==="confirm" && entry.submission?.snapshot).map(entry=>({...entry.submission.snapshot,revision:entry.submission.revision,time:entry.time}))].filter(Boolean);
      for (const fixed of baselines) {
        if (!Number.isFinite(Date.parse(fixed.time)) || Date.parse(fixed.time)>=cutoff || planConfirmedSnapshotProblem(state,{...fixed,tenantId:plan.tenantId,confirmedSnapshot:fixed})) continue;
        for (const item of fixed.items) if (item.workId===work.id && /^\d{4}-\d{2}-\d{2}$/.test(item.dueDate || "") && item.dueDate>=descriptor.periodStart && item.dueDate<=descriptor.periodEnd) {
          const nextDay = new Date(`${item.dueDate}T00:00:00Z`); nextDay.setUTCDate(nextDay.getUTCDate()+1);
          const dueEnd = localDayStart(nextDay.toISOString().slice(0,10),fixed.timezone || descriptor.timezone);
          if (Date.parse(fixed.time)<dueEnd) commitments.push({planId:plan.id,itemId:item.id,dueDate:item.dueDate,dueEnd,confirmedAt:fixed.time});
        }
      }
    }
    if (facts.length && commitments.length) {
      // Freeze only a commitment already confirmed when this version was accepted.
      const first = [...facts].sort((a,b)=>a.time.localeCompare(b.time))[0];
      const commitment = commitments.filter(item=>Date.parse(item.confirmedAt)<=Date.parse(first.time)).sort((a,b)=>b.confirmedAt.localeCompare(a.confirmedAt))[0];
      if (commitment) { delayed=Date.parse(first.time)>=commitment.dueEnd; evidence.push({id:`${commitment.planId}:${commitment.itemId}`,time:commitment.confirmedAt,kind:"确认承诺",role:"周期计划基线",dueDate:commitment.dueDate,href:`/plans/${commitment.planId}`}); }
    }
    if (delayed===null) issues.push("缺少可核对的期内承诺日期及后续接受事实，延期未知；不使用当前排期反推。");
    rows.push({workId:work.id,title:latest?.title || work.title,projectId,projectName:state.projects.find(project=>project.id===projectId)?.name || projectId,catalogProjectId:work.project,scopeKnown:!!projectId,completed,inProgress,delayed,changed,evidence,issues});
  }
  const count = field => rows.filter(row=>row[field]===true).length;
  return {rows,completed:count("completed"),inProgress:count("inProgress"),delayed:count("delayed"),changed:count("changed"),unknown:rows.filter(row=>row.issues.length).length,
    basis:"完成按期内固定接受；进行中按期内继续执行裁决；变更按期内退回或取消裁决。类别可重叠，不代表完整期末状态。延期只由固定计划承诺与接受时间核对。",cutoffAt:descriptor.cutoffAt,subjectKey};
}

export function performanceScores(policy,facts,assessments={}) {
  const accepted=[...new Map([...facts.facts].sort((a,b)=>Date.parse(a.time)-Date.parse(b.time) || a.version-b.version).map(f=>[f.workId,f])).values()];
  const lines=policy.rules.map(rule=>{
    const assessment=assessments[rule.id];let count=null,evidence=[],issue="";
    if(rule.metric==="REVIEW") {
      if(assessment) {count=assessment.value;evidence=[{note:assessment.reason,url:assessment.evidence}];}
      else issue="需要有依据的专业评估，不从消息量或工时推断。";
    } else if(facts.unknownWorkIds.length)issue=`${facts.unknownWorkIds.length} 项工作缺少可核对的接受事实。`;
    else if(rule.metric==="COMPLETED_WORK") {count=accepted.length;evidence=accepted;}
    else if(accepted.some(f=>!["通过","失败","未检查","待检查"].includes(f.workSnapshot?.quality)))issue="固定版本未记录可解释的质量结论。";
    else {count=accepted.filter(f=>f.workSnapshot.quality==="通过").length;evidence=accepted.filter(f=>f.workSnapshot.quality==="通过");}
    const delta=count===null?null:Math.min(rule.limit,count)*rule.points;
    return {...structuredClone(rule),count,delta,evidence:structuredClone(evidence),issue};
  });
  const knownScore=Math.max(0,Math.min(policy.maxScore,policy.baseScore+lines.reduce((sum,l)=>sum+(l.delta??0),0)));
  return {lines,knownScore,score:lines.some(l=>l.delta===null)?null:knownScore};
}
export function performanceBody(report) {
  const details=report.scoring.lines.map(l=>`${l.dimension} / ${l.name}：${l.count===null?"待评":`${l.count} 次 × ${l.points}（最多 ${l.limit} 次） = ${l.delta>0?"+":""}${l.delta}`}；${l.issue||l.description}`).join("\n");
  const tokens={subject:report.subject.name,period:`${report.periodStart} — ${report.periodEnd}`,score:report.scoring.score===null?"待补证 / 校准":String(report.scoring.score),details,cutoff:report.cutoffAt};
  return report.policySnapshot.template.replace(/\{\{(subject|period|score|details|cutoff)\}\}/g,(_,key)=>tokens[key]);
}
export function performanceCommandOn(s,input,makeId) {
  const problem=performanceOwnerProblem(s);if(problem)throw Error(problem);
  if(input.tenantId!==s.activeTenant)throw Error("租户已切换，请重新打开考核操作。");
  if(!input.reason?.trim())throw Error("请记录配置、生成或校准理由。");
  s.resources.performancePolicies??=[];s.resources.performanceReports??=[];
  const policies=s.resources.performancePolicies,reports=s.resources.performanceReports;
  if(["save","create"].includes(input.action)) {
    const old=input.id&&policies.find(p=>p.id===input.id);
    if(input.id&&(!old || old.tenantId!==s.activeTenant))throw Error("考核方案不存在于当前租户。");
    if(old&&input.revision!==old.revision)throw Error("方案已变化，请重新核对。");
    const policy={...normalizePerformancePolicy(s,input),id:old?.id||makeId("PERF"),tenantId:s.activeTenant,revision:(old?.revision||0)+1,history:[...(old?.history||[]),{time:new Date().toISOString(),actorId:"H-lin",reason:input.reason.trim(),before:old?{...structuredClone(old),history:undefined}:null}],source:"LOCAL_SIMULATION"};
    if(old)policies.splice(policies.indexOf(old),1,policy);else policies.push(policy);return policy.id;
  }
  if(input.action==="generate-period") {
    const policy=policies.find(item=>item.id===input.policyId);
    if(!policy || policy.tenantId!==s.activeTenant || !policy.enabled || policy.revision!==input.policyRevision)throw Error("请选择当前已启用的考核方案版本。");
    const checked=normalizePerformancePolicy(s,policy);
    return checked.subjectKeys.map(subjectKey=>performanceCommandOn(s,{...input,action:"generate",subjectKey},makeId))[0];
  }
  if(input.action==="generate") {
    const policy=policies.find(p=>p.id===input.policyId);if(!policy||policy.tenantId!==s.activeTenant||!policy.enabled||input.policyRevision!==policy.revision)throw Error("请选择当前已启用的考核方案版本。");
    const checked=normalizePerformancePolicy(s,policy);if(!checked.subjectKeys.includes(input.subjectKey))throw Error("该主体不在方案范围。");
    const period={...performancePeriod(checked,input.cycle,input.date),timezone:policy.timezone};
    if(Date.parse(period.cutoffAt)>Date.now())throw Error("该周期最后一天 00:00 截点尚未到达，不能提前生成未来事实；请选择已到截点的周期。");
    const key=JSON.stringify([s.activeTenant,policy.id,input.subjectKey,input.cycle,period.periodStart,period.periodEnd]);
    const old=reports.find(r=>r.key===key);if(old&&!input.regenerate)return old.id;
    if(old&&input.revision!==old.revision)throw Error("考核报告已变化，不能覆盖新修订。");
    const facts=fixedPerformanceFacts(s,{subjectKey:input.subjectKey,cadence:input.cycle,timezone:policy.timezone,...period},Date.parse(period.cutoffAt));
    const workOverview=performanceWorkFacts(s,{...period,cycle:input.cycle,subjectKey:input.subjectKey});
    const overviewSnapshot=performanceWorkFacts(s,{...period,cycle:input.cycle,subjectKey:`tenant:${s.activeTenant}`});
    facts.unknownWorkIds=[...new Set([...facts.unknownWorkIds,...workOverview.rows.filter(row=>row.completed===null).map(row=>row.workId)])];
    const report={id:old?.id||makeId("PR"),key,tenantId:s.activeTenant,policyId:policy.id,policySnapshot:structuredClone(policy),subject:structuredClone(reportSubjects(s).find(p=>p.key===input.subjectKey)),cycle:input.cycle,...period,facts:structuredClone(facts),workOverview,overviewSnapshot,assessments:{},scoring:performanceScores(policy,facts),status:"待复核",revision:(old?.revision||0)+1,history:[...(old?.history||[]),...(old?[{time:new Date().toISOString(),actorId:"H-lin",reason:input.reason.trim(),before:{...structuredClone(old),history:undefined}}]:[])],generatedAt:new Date().toISOString(),source:"LOCAL_SIMULATION",generationReason:input.reason.trim()};
    report.body=performanceBody(report);if(old)reports.splice(reports.indexOf(old),1,report);else reports.push(report);return report.id;
  }
  const report=reports.find(r=>r.id===input.reportId);if(!report||report.tenantId!==s.activeTenant||report.revision!==input.revision)throw Error("考核报告不存在或修订已变化。");
  if(input.action==="calibrate") {
    const rule=report.policySnapshot.rules.find(r=>r.id===input.ruleId);if(!rule||rule.metric!=="REVIEW")throw Error("自动事实不能手改；请仅校准专业评估维度。");
    const value=number(input.value,"评估次数");if(!Number.isInteger(value)||value<0||value>rule.limit)throw Error("评估次数须为该细则上限范围内的非负整数。");
    try {const url=new URL(input.evidence);if(!["http:","https:"].includes(url.protocol)||url.username||url.password)throw Error();}catch {throw Error("请提供可核对且不含凭据的 HTTP(S) 证据地址。");}
    report.history.push({time:new Date().toISOString(),actorId:"H-lin",reason:input.reason.trim(),before:{...structuredClone(report),history:undefined}});
    report.assessments[input.ruleId]={value,evidence:input.evidence,reason:input.reason.trim(),actorId:"H-lin",time:new Date().toISOString()};report.scoring=performanceScores(report.policySnapshot,report.facts,report.assessments);report.status="待复核";delete report.confirmation;report.revision++;report.body=performanceBody(report);return report.id;
  }
  if(input.action==="confirm") {
    if(report.status==="已确认")throw Error("本修订已经确认。");if(report.scoring.score===null)throw Error("仍有缺证据或待评维度，不能确认本期评分。");
    report.history.push({time:new Date().toISOString(),actorId:"H-lin",reason:input.reason.trim(),action:"CONFIRM",before:{...structuredClone(report),history:undefined}});
    report.confirmation={actorId:"H-lin",time:new Date().toISOString(),reason:input.reason.trim(),revision:report.revision,score:report.scoring.score,policyRevision:report.policySnapshot.revision};report.status="已确认";report.revision++;return report.id;
  }
  throw Error("不支持的绩效操作。");
}
