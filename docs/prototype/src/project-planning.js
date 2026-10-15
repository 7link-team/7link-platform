import { projectRecordWriteProblem, recordProjectId } from "./project-lifecycle";
import { goalReadProblem } from "./goals";
import { channelMessageReadProblem, channelReadProblem, channelPeople } from "./channels";

export const planningKinds = { requirements:"需求", iterations:"迭代" };
export function planningReadProblem(s, record) {
  if (!record || record.tenantId!==s.activeTenant) return "记录不存在于当前租户。";
  const project=s.projects.find(p=>p.id===record.projectId && p.id!=="private");
  const actor=channelPeople(s).find(p=>p.id==="H-lin" && p.kind==="Human" && p.available && !p.external);
  if(s.role==="external" || !actor || !project) return "当前身份无权读取此项目记录。";
  return s.role==="owner" || project.ownerId===actor.id || s.organization.projectRoles.some(r=>r.projectId===project.id&&r.memberId===actor.id) ? "" : "当前成员未获项目访问权限。";
}
export function planningRows(s,kind) {
  return (s.resources[kind] || []).filter(record=>!planningReadProblem(s,record) && (s.project==="all" || record.projectId===s.project));
}
export function planningGoals(s,projectId) {
  return s.resources.goals.filter(goal=>!goalReadProblem(s,goal) && (!recordProjectId(s,goal) || recordProjectId(s,goal)===projectId));
}
export function planningSourceProblem(s,source,projectId) {
  if(!source?.channelId)return "";
  const problem=channelReadProblem(s,source.channelId);if(problem)return problem;
  const channel=s.channels.find(c=>c.id===source.channelId);
  if(channel?.projectId && channel.projectId!==projectId)return "来源会话属于其他项目。";
  if(source.messageId) {
    const message=s.messages.find(m=>m.id===source.messageId && m.channel===source.channelId);
    if(!message || message.recalled)return "来源消息不存在或已撤回。";
    return channelMessageReadProblem(s,message);
  }
  return "";
}
const text=(value,label)=>{if(typeof value!=="string" || !value.trim())throw Error(`请填写${label}。`);return value.trim();};
const date=value=>typeof value==="string" && /^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(Date.parse(value)) && new Date(value).toISOString().slice(0,10)===value;
// The caller commits this command against an isolated store copy. No execution is created.
export function planningCommandOn(s,kind,input,{makeId=()=>crypto.randomUUID(),now=()=>new Date().toISOString()}={}) {
  if(!Object.hasOwn(planningKinds,kind))throw Error("未知项目记录类型。");
  if(input.tenantId!==s.activeTenant || s.offline || s.role==="external")throw Error("租户、身份或在线状态已变化，请重新打开。");
  const previous=input.id && (s.resources[kind] || []).find(r=>r.id===input.id);
  if(input.id && !previous)throw Error("记录不存在，不能从旧表单重新创建。");
  if(previous && previous.revision!==input.revision)throw Error("记录已变化，请重新打开最新修订。");
  const projectId=previous?.projectId || input.projectId;
  if(previous && input.projectId && input.projectId!==projectId)throw Error("已有记录的项目不能改写。");
  const problem=planningReadProblem(s,{tenantId:s.activeTenant,projectId}) || projectRecordWriteProblem(s,{projectId});if(problem)throw Error(problem);
  if(previous?.archivedAt)throw Error("归档记录保留历史，只读。");
  const action=input.action || "save", reason=text(input.changeReason,"变更理由");
  if(!["save","confirm","start","complete","archive"].includes(action))throw Error("未知操作。");
  if(!previous && action!=="save")throw Error("请先保存草稿。");
  let next;
  if(action==="save") {
    if(previous?.status==="已完成")throw Error("已完成迭代保留固定范围，不能改写。");
    const owner=channelPeople(s).find(p=>p.id===input.ownerId && p.kind==="Human" && p.available && !p.external);
    if(!owner)throw Error("请选择当前在职 Human 负责人。");
    if(input.goalId && !planningGoals(s,projectId).some(g=>g.id===input.goalId))throw Error("关联目标不可访问或属于其他项目。");
    if(!Array.isArray(input.workIds) || input.workIds.some(id=>typeof id!=="string" || !s.work.some(w=>w.id===id && w.project===projectId)))throw Error("任务范围必须引用同项目的已有任务。");
    next={...(previous || {}),name:text(input.name,"名称"),projectId,ownerId:owner.id,owner:owner.name,goalId:input.goalId || null,workIds:[...new Set(input.workIds)],description:text(input.description,"说明")};
    if(kind==="requirements") {
      const source=input.source?.channelId ? {channelId:input.source.channelId,messageId:input.source.messageId || null} : null;
      const sourceProblem=planningSourceProblem(s,source,projectId);if(sourceProblem)throw Error(sourceProblem);
      next={...next,acceptance:text(input.acceptance,"验收标准"),source,status:"草稿",confirmedAt:null,confirmedBy:null};
    } else {
      if(!date(input.startDate)||!date(input.endDate)||input.startDate>input.endDate)throw Error("请填写有效且先后有序的起止日期。");
      next={...next,startDate:input.startDate,endDate:input.endDate,entryCriteria:text(input.entryCriteria,"准入条件"),exitCriteria:text(input.exitCriteria,"准出条件"),status:previous?.status || "草稿"};
    }
  } else {
    next={...previous};
    if(["confirm","start"].includes(action)) {
      if(!channelPeople(s).some(p=>p.id===previous.ownerId && p.kind==="Human" && p.available && !p.external))throw Error("负责人已不可用，请先修订负责人。");
      if(previous.goalId && !planningGoals(s,projectId).some(g=>g.id===previous.goalId))throw Error("原关联目标已不可访问，请先修订。");
      if(previous.workIds.some(id=>!s.work.some(w=>w.id===id&&w.project===projectId)))throw Error("任务范围已变化，请先修订。");
    }
    if(action==="confirm") {
      if(kind!=="requirements" || previous.status!=="草稿")throw Error("只能确认需求草稿。");
      const sourceProblem=planningSourceProblem(s,previous.source,projectId);if(sourceProblem)throw Error(sourceProblem);
      next.status="已确认";next.confirmedAt=now();next.confirmedBy="H-lin";
    } else if(action==="start") {
      if(kind!=="iterations" || previous.status!=="草稿")throw Error("只能开始草稿迭代。");
      next.status="进行中";next.entryVerification=text(input.verification,"准入核对结果");
    } else if(action==="complete") {
      if(kind!=="iterations" || previous.status!=="进行中")throw Error("只能完成进行中的迭代。");
      if(previous.workIds.some(id=>!s.work.some(w=>w.id===id && w.project===projectId && w.status==="已完成")))throw Error("范围内仍有未完成、已移出或不存在的任务，不能标记迭代完成。");
      next.status="已完成";next.exitVerification=text(input.verification,"准出核对结果");next.completedAt=now();
    } else {next.archivedAt=now();next.status="已归档";}
  }
  const time=now(), before=previous ? structuredClone(Object.fromEntries(Object.entries(previous).filter(([key])=>key!=="history"))) : null;
  next={...next,id:previous?.id || makeId(kind==="requirements"?"REQ":"ITER"),tenantId:s.activeTenant,revision:(previous?.revision || 0)+1,changeReason:reason,updatedAt:time,history:[...(previous?.history || []),{action,reason,time,actorId:"H-lin",before}]};
  if(!next.id || (s.resources[kind] || []).some(r=>r.id===next.id && r!==previous))throw Error("记录编号冲突。");
  s.resources[kind] ??= [];
  const index=s.resources[kind].findIndex(r=>r.id===next.id);
  if(index<0)s.resources[kind].push(next);else s.resources[kind][index]=next;
  return next.id;
}
