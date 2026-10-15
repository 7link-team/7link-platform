import { projectIsArchived, recordProjectId } from "./project-lifecycle";
import { reportSubjects, reportPeriod, dateInZone, validatePeriodicReport, submittedReportBody } from "./reporting";
import { goalContributors, structuredGoal, krResult, effectiveGoal, goalReadProblem } from "./goals";
import { workRework } from "./rework";
import { reportDetailFingerprint } from "./report-content";

export const planPeriods = { DAY: "日报", WEEK: "周报", MONTH: "月报", QUARTER: "季报", YEAR: "年报" };
export const planCategories = ["LEARNING", "OPS", "REVIEW", "SUPPORT", "OTHER"];
const clone = value => structuredClone(value);
export function planSubject(state, key) {
  return reportSubjects(state).find(subject => subject.key === key && ["Human", "Agent"].includes(subject.kind));
}
export function planKey(state, plan) {
  return JSON.stringify([state.activeTenant, plan.subjectKey, plan.periodKind, plan.periodStart]);
}
export function planPeriod(kind, date, timezone) {
  dateInZone(timezone);
  if (!planPeriods[kind]) throw Error("请选择日、周、月、季或年计划。");
  return reportPeriod(planPeriods[kind], date);
}
const uniqueHuman = (state, name) => {
  const matches = state.organization.members.filter(person => person.kind === "Human" && person.name === name);
  return matches.length === 1 ? matches[0].id : null;
};
export function planConfirmer(state, subjectKey) {
  const subject = planSubject(state, subjectKey);
  if (!subject) return { id: null, issue: "计划主体已不可用。" };
  const principalId = subjectKey.slice(subjectKey.indexOf(":") + 1);
  let id, basis;
  if (subject.kind === "Agent") {
    const agent = state.agents.find(agent => agent.id === principalId);
    id = agent.ownerPrincipalId || uniqueHuman(state, agent.owner);
    basis = agent.ownerPrincipalId ? "Agent 固定负责人类" : "Agent 原有负责人唯一匹配 Human";
  }
  if (!id) {
    const memberships = state.organization.projectRoles.filter(role => role.memberId === principalId && state.projects.some(project => project.id === role.projectId && ["ACTIVE", "进行中"].includes(project.status)));
    const projects = [...new Set(memberships.map(role => role.projectId))];
    if (projects.length === 1) {
      const owners = state.organization.projectRoles.filter(role => role.projectId === projects[0] && role.role === "项目负责人");
      if (owners.length === 1) { id = owners[0].memberId; basis = `唯一进行中项目 ${projects[0]} 的负责人`; }
    }
  }
  if (!id && subject.kind === "Human") { id = state.planningSettings?.tenantOwnerId; basis = "已配置团队所有者"; }
  if (subject.kind === "Human" && id === principalId) {
    const superior = state.planningSettings?.tenantOwnerId;
    id = superior && superior !== principalId ? superior : null; basis = "确认人的上级";
  }
  if (!id || !state.organization.members.some(person => person.id === id && person.kind === "Human")) return { id: null, issue: "未配置可独立确认的 Human 上级；不能本人自审。" };
  return { id, basis, issue: null };
}
export function planActor(state, plan, simulateAgent = false) {
  if (state.role === "external") throw Error("外部成员无权访问内部周期计划。");
  const subject = planSubject(state, plan.subjectKey);
  if (!subject) throw Error("计划主体不存在于当前租户。");
  if (subject.kind === "Human") {
    if (plan.subjectKey !== "human:H-lin") throw Error("Human 计划只能由本人编辑与提交。");
    return { kind: "Human", id: "H-lin", source: "本人操作" };
  }
  const agent = state.agents.find(agent => `agent:${agent.id}` === plan.subjectKey);
  if (!simulateAgent || planConfirmer(state, plan.subjectKey).id !== "H-lin") throw Error("Agent 计划仅支持由其负责人类明确发起的本地模拟，不冒充真实 Agent 提交。");
  if (["退役", "待批准", "已禁用"].includes(agent.status)) throw Error("当前 Agent 不可用，不能新增计划承诺。");
  return { kind: "Agent", id: agent.id, source: "本地 Agent 模拟", recordedBy: "H-lin" };
}
export function planCanRead(state, plan) {
  return plan.tenantId === state.activeTenant && state.role !== "external" && (state.role === "owner" || plan.subjectKey === "human:H-lin" || planConfirmer(state, plan.subjectKey).id === "H-lin");
}
export function planCanDecide(state, plan) {
  if (state.role === "external") return "外部成员无权确认计划。";
  if (plan.subjectKey === "human:H-lin") return "禁止本人确认或退回本人计划。";
  const confirmer = planConfirmer(state, plan.subjectKey);
  if (!confirmer.id) return confirmer.issue;
  if (plan.submission?.confirmerId !== confirmer.id) return "确认人已变化，请作者重新核对提交。";
  if (confirmer.id !== "H-lin") return "当前 Human 不是这份计划的指定 Human 确认人，且未建立有效上级授权。";
  return null;
}
export function planEligibleGoals(state, subjectKey) {
  const subject = planSubject(state, subjectKey), principalId = subjectKey.split(":")[1];
  if (!subject) return [];
  return state.resources.goals.filter(goal => !goalReadProblem(state, goal)).map(effectiveGoal).filter(goal => structuredGoal(goal) && goal.status !== "已取消" &&
    (goal.subjectKey === subjectKey || (["project", "team", "tenant"].includes(goal.subjectKey?.split(":")[0]) && goalContributors(state, goal.id).some(person => person.id === principalId && person.kind === subject.kind))));
}
export function planWorkEligible(state, plan, work) {
  const identity = plan.subjectKey.startsWith("agent:") ? plan.subjectKey : plan.subjectKey.slice(6);
  if ((work.team?.executorIds || []).includes(identity) || (work.team?.checkerIds || []).includes(identity)) return true;
  if (!work.team && work.agentId && `agent:${work.agentId}` === identity) return true;
  return (work.team?.history || []).some(entry => {
    if (!entry.time || !Number.isFinite(Date.parse(entry.time))) return false;
    const day = dateInZone(plan.timezone, new Date(entry.time));
    return day >= plan.periodStart && day <= plan.periodEnd && [...(entry.previous?.executorIds || []), ...(entry.previous?.checkerIds || []), ...(entry.next?.executorIds || []), ...(entry.next?.checkerIds || [])].includes(identity);
  });
}
// Diagnose persisted current items without normalizing the original evidence.
export function planCurrentItemsProblem(plan) {
  if (!Array.isArray(plan.items)) return "当前修订计划项格式损坏。";
  const ids = new Set();
  for (const [index, item] of plan.items.entries()) {
    if (!item || typeof item !== "object" || Array.isArray(item) ||
      typeof item.id !== "string" || !item.id.trim() || ids.has(item.id) ||
      !["WORK_ITEM", "FREE_TASK"].includes(item.kind) ||
      ["title", "goalId", "krId", "workId", "freeCategory", "criteria", "dueDate", "note", "carriedFromItemId"].some(key => item[key] != null && typeof item[key] !== "string") ||
      typeof item.title !== "string" || !item.title.trim() ||
      typeof item.weight !== "number" || !Number.isFinite(item.weight) || item.weight <= 0 ||
      ["estimateHours", "estimateCostMicros"].some(key => item[key] != null && (typeof item[key] !== "number" || !Number.isFinite(item[key]) || item[key] < 0)) ||
      (item.estimateCostMicros != null && !Number.isSafeInteger(item.estimateCostMicros)))
      return `当前修订计划项 ${index + 1} 的字段损坏，请核对原始记录；不会自动转换或补造。`;
    ids.add(item.id);
  }
  return null;
}
export function planChecks(state, plan) {
  const blocking = [], warnings = [], goals = planEligibleGoals(state, plan.subjectKey), identities = new Set();
  const add = (code, message, item) => blocking.push({ code, message, itemId: item?.id });
  const currentProblem = planCurrentItemsProblem(plan);
  if (currentProblem) return { blocking: [{ code: "SHAPE", message: currentProblem }], warnings, confirmer: planConfirmer(state, plan.subjectKey) };
  if (!plan.items.length) add("EMPTY", "至少添加一项计划承诺。");
  if (plan.items.length > (plan.periodKind === "DAY" ? 30 : 200)) add("LIMIT", "日计划最多 30 项，其他周期最多 200 项。");
  for (const item of plan.items) {
    const goal = goals.find(goal => goal.id === item.goalId);
    if (!goal) add("B3", `${item.title || "计划项"}：请选择有贡献资格且未取消的目标。`, item);
    if (item.krId && (!goal?.krs.some(kr => kr.id === item.krId) || !goalContributors(state, goal.id, item.krId).some(person => `${person.kind === "Agent" ? "agent" : "human"}:${person.id}` === plan.subjectKey))) add("B5", `${item.title}：KR 不属于当前目标或计划主体贡献范围。`, item);
    if (item.kind === "WORK_ITEM") {
      const work = state.work.find(work => work.id === item.workId);
      if (!work || !planWorkEligible(state, plan, work)) add("WORK", `${item.title}：工作未分派给计划主体，历史分派也未得到证实。`, item);
      if (identities.has(item.workId)) add("DUPLICATE", `${item.title}：同一工作在本计划重复引用。`, item);
      identities.add(item.workId);
      if (work?.status === "已取消") warnings.push({ message: `${item.title}：关联工作已取消，请明确结转或移除原因。`, itemId: item.id });
    }
  }
  const confirmer = planConfirmer(state, plan.subjectKey);
  if (confirmer.issue) warnings.push({ message: confirmer.issue });
  if (!plan.confirmedSnapshot) warnings.push({ code: "N2", message: "本期计划尚未经过上级确认。" });
  return { blocking, warnings, confirmer };
}
export function normalizePlanItems(items, makeId) {
  if (!Array.isArray(items)) throw Error("计划项格式无效。");
  const ids = new Set();
  return items.map((item, index) => {
    const id = item.id || makeId("PI");
    if (ids.has(id)) throw Error("计划项编号不能重复。"); ids.add(id);
    if (!["WORK_ITEM", "FREE_TASK"].includes(item.kind) || !item.title?.trim()) throw Error("请填写计划项类型与标题。");
    const number = (value, label, integer = false) => {
      if (value === "" || value === null || value === undefined) return null;
      if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(String(value)) || !Number.isFinite(Number(value)) || Number(value) < 0 || (integer && !Number.isSafeInteger(Number(value)))) throw Error(`${label}必须是有效非负数。`);
      return Number(value);
    };
    const weight = number(item.weight, "权重"); if (!(weight > 0)) throw Error("计划项权重必须明确且大于零。");
    if (item.dueDate) planPeriod("DAY", item.dueDate, "UTC");
    if (item.kind === "FREE_TASK" && (!planCategories.includes(item.freeCategory) || !item.criteria?.trim())) throw Error("非交付任务需要类别与明确完成标准。");
    return { id, seq: index + 1, kind: item.kind, title: item.title.trim(), goalId: item.goalId || "", krId: item.krId || "",
      workId: item.kind === "WORK_ITEM" ? item.workId || "" : null, freeCategory: item.kind === "FREE_TASK" ? item.freeCategory : null,
      criteria: item.criteria?.trim() || "", weight, dueDate: item.dueDate || "", note: item.note?.trim() || "",
      estimateHours: number(item.estimateHours, "预计小时"), estimateCostMicros: number(item.estimateCostMicros, "预计成本 micros", true),
      carriedFromItemId: item.carriedFromItemId || null };
  });
}
// BR-10-15: draft suggestions from recorded sources; never invent a goal,
// assignment, completion or Human commitment. The caller owns the transaction.
export function periodPlanDraftProposal(state, descriptor, previousReport, makeId, now = new Date()) {
  if (!planSubject(state, descriptor.subjectKey)) throw Error("系统起草仅适用于真实成员计划，不创建项目或团队的虚构成员计划。");
  const period = planPeriod(descriptor.periodKind, descriptor.periodStart, descriptor.timezone);
  if (period.periodStart !== descriptor.periodStart || period.periodEnd !== descriptor.periodEnd) throw Error("起草计划的日历周期不一致。");
  const items = [], sources = [], issues = [], workIds = new Set();
  const eligibleGoals = planEligibleGoals(state, descriptor.subjectKey);
  const add = (item, source) => {
    if (projectIsArchived(state,recordProjectId(state,item))) return;
    if (item.kind === "WORK_ITEM" && workIds.has(item.workId)) return;
    const normalized = normalizePlanItems([{ ...item, id: makeId("PI") }], makeId)[0];
    normalized.seq = items.length + 1;
    items.push(normalized); sources.push({ itemId: normalized.id, ...source });
    if (item.kind === "WORK_ITEM") workIds.add(item.workId);
  };
  if (previousReport) {
    const next = reportNextPlanDescriptor(state, previousReport);
    if (["subjectKey", "periodKind", "periodStart", "periodEnd"].some(key => next[key] !== descriptor[key])) throw Error("结转报告不是同一主体紧邻上周期。");
    const fixedBody = submittedReportBody(previousReport);
    if (["已提交", "待审核", "已接受"].includes(previousReport.status) && !fixedBody) throw Error("固定报告正文无法读取，不能降级使用当前草稿结转。");
    const body = fixedBody || previousReport;
    if (body) {
      if (["subjectKey", "cadence", "periodStart", "periodEnd", "timezone"].some(key => body[key] !== previousReport[key])) throw Error("固定结转报告的主体或周期与原记录冲突，不能采用他人的承诺。");
      const binding = body.currentPlanBinding;
      const problem = binding && reportCurrentPlanBindingProblem(state, body, binding);
      if (problem) throw Error(problem);
      for (const entry of body.content?.uncompleted || []) {
        if (entry.carry_over !== "CARRY_TO_NEXT") continue;
        const confirmation = entry.detail_confirmation;
        if (!fixedBody && (confirmation?.fingerprint !== reportDetailFingerprint(entry) || confirmation.actorId !== descriptor.subjectKey.split(":")[1] || confirmation.actorKind !== (descriptor.subjectKey.startsWith("agent:") ? "Agent" : "Human"))) {
          issues.push(`结转项 ${entry.title || entry.key} 的原因与处置尚未由作者确认，未把系统预填当作决定。`); continue;
        }
        const item = binding?.confirmed && binding.snapshot.items.find(item => item.id === entry.plan_item_id);
        if (!item) { issues.push(`结转项 ${entry.title || entry.key} 缺少已确认原计划依据，未补造。`); continue; }
        add({ ...item, carriedFromItemId: item.id, note: `${item.note || ""}${item.note ? "；" : ""}系统结转草稿，待成员核对。` },
          { kind: "REPORT_CARRY", reportId: previousReport.id, reportRevision: fixedBody ? previousReport.submission.revision : previousReport.revision, fixedReport: Boolean(fixedBody), planId: binding.planId, planRevision: binding.revision, originalItemId: item.id });
      }
      if (!fixedBody && !body.content?.uncompleted?.length) issues.push("上期报告未固定提交，且没有已核对的结转项；未补造成员决定。");
    }
  }
  const principal = descriptor.subjectKey.startsWith("agent:") ? descriptor.subjectKey : descriptor.subjectKey.slice(6);
  for (const work of state.work) {
    const currentAssignment = work.team ? [...(work.team.executorIds || []), ...(work.team.checkerIds || [])].includes(principal)
      : descriptor.subjectKey.startsWith("agent:") && work.agentId === descriptor.subjectKey.slice(6);
    const qualityHold = !["已完成", "已取消", "ACCEPTED", "CANCELLED", "COMPLETED"].includes(work.status) && ["formal", "internal"].includes(workRework(work).hold?.kind);
    if (!currentAssignment || (!qualityHold && !["READY", "CLAIMED", "IN_PROGRESS", "REWORK", "待开始", "待认领", "进行中", "返工"].includes(work.status))) continue;
    const goal = eligibleGoals.find(goal => goal.id === work.goalId);
    const krId = goal?.krs.some(kr => kr.id === work.krId) && goalContributors(state, goal.id, work.krId).some(person => `${person.kind === "Agent" ? "agent" : "human"}:${person.id}` === descriptor.subjectKey) ? work.krId : "";
    add({ kind: "WORK_ITEM", title: work.title, workId: work.id, goalId: goal?.id || "", krId, weight: 1,
      note: "系统按当前分派起草，待成员核对目标关联、期限与投入。" }, { kind: "CURRENT_ASSIGNMENT", workId: work.id, workVersion: work.version });
  }
  for (const goal of eligibleGoals.filter(goal => ["进行中", "存在风险"].includes(goal.status))) {
    for (const kr of goal.krs) {
      const result = krResult(goal, kr, now), observedAt = Date.parse(result.observation?.observedAt);
      const provenAchieved = result.progress === 1 && Number.isFinite(observedAt) && observedAt <= now.getTime();
      if (!goalContributors(state, goal.id, kr.id).some(person => `${person.kind === "Agent" ? "agent" : "human"}:${person.id}` === descriptor.subjectKey) || provenAchieved) continue;
      add({ kind: "FREE_TASK", title: `建议：推进 ${kr.title}`, goalId: goal.id, krId: kr.id, freeCategory: "OTHER", weight: 1,
        criteria: "核对本 KR 的下期推进方案、下游价值和可核验完成标准；成员确认前不视为正式承诺。",
        note: "系统建议，尚未确认；指标未知不等于已达成。" },
        { kind: "KR_SUGGESTION", goalId: goal.id, definitionRevision: goal.definitionRevision || goal.revision, krId: kr.id });
    }
  }
  if (items.length > (descriptor.periodKind === "DAY" ? 30 : 200)) throw Error("起草来源超过计划项上限，未截断；请先明确范围。");
  return { items, sources, issues, checks: planChecks(state, { ...descriptor, items }) };
}
export function planCommitment(items) {
  return items.map(({ note, estimateHours, estimateCostMicros, ...item }) => item);
}
export function planDiff(before = [], after = []) {
  const result = [];
  for (const item of before) if (!after.some(next => next.id === item.id)) result.push({ itemId: item.id, label: item.title, kind: "移除", before: clone(item), after: null });
  for (const item of after) {
    const old = before.find(previous => previous.id === item.id);
    if (!old || JSON.stringify(old) !== JSON.stringify(item)) result.push({ itemId: item.id, label: item.title, kind: old ? "变更" : "新增", before: old ? clone(old) : null, after: clone(item) });
  }
  return result;
}
export function planSnapshot(state, plan) {
  const problem = planCurrentItemsProblem(plan);
  if (problem) throw Error(problem);
  return { subjectKey: plan.subjectKey, periodKind: plan.periodKind, periodStart: plan.periodStart, periodEnd: plan.periodEnd, timezone: plan.timezone,
    items: clone(plan.items), references: plan.items.map(item => {
      const goal = effectiveGoal(state.resources.goals.find(goal => goal.id === item.goalId)), work = state.work.find(work => work.id === item.workId);
      return { itemId: item.id, goal: goal ? { id: goal.id, definitionRevision: goal.definitionRevision || goal.revision, status: goal.status } : null,
        work: work ? { id: work.id, version: work.version, requirements: clone(work.requirements), inputs: clone(work.inputs || []), team: clone(work.team || null), owner: work.owner, agentId: work.agentId } : null };
    }) };
}
// A report adopts a real member Plan revision; it never owns another item list.
export function reportCurrentPlan(state, report) {
  if (!/^(human|agent):[^:]+$/.test(report.subjectKey || "")) throw Error("项目与团队报告的本期计划需汇总成员计划，不能绑定虚构的成员计划。");
  const kind = Object.keys(planPeriods).find(key => planPeriods[key] === report.cadence);
  if (!kind) throw Error("本期计划周期未声明，不能推断计划基线。");
  const matches = (state.resources.plans || []).filter(plan => plan && plan.tenantId === state.activeTenant &&
    plan.subjectKey === report.subjectKey && plan.periodKind === kind && plan.periodStart === report.periodStart);
  if (matches.length > 1) throw Error("本期存在重复计划，不能任意选取或合并为进度基线，请先处理冲突。");
  const plan = matches[0];
  if (plan && plan.periodEnd !== report.periodEnd) throw Error("本期计划的结束日期与报告日历周期冲突，不能作为进度基线。");
  if (plan && !planCanRead(state, plan)) throw Error("当前主体无权读取本期计划，进度基线不可核对。");
  return plan || null;
}
export function reportCurrentPlanBinding(state, report, planId, sourceRevision) {
  const plan = reportCurrentPlan(state, report);
  if (!plan || plan.id !== planId) throw Error("请选择同一主体、本期日历周期的真实计划。");
  if (plan.revision !== sourceRevision) throw Error("本期计划修订已变化，请重新采用当前基线。");
  const problem = planConfirmedSnapshotProblem(state, plan);
  if (problem) throw Error(problem);
  const snapshot = clone(plan.confirmedSnapshot || { ...planSnapshot(state, plan), revision: plan.revision });
  const binding = { planId: plan.id, tenantId: plan.tenantId, sourceRevision: plan.revision, revision: snapshot.revision,
    status: plan.status, confirmed: Boolean(plan.confirmedSnapshot), snapshot };
  const invalid = reportCurrentPlanBindingProblem(state, report, binding);
  if (invalid) throw Error(invalid);
  return binding;
}
export function reportCurrentPlanBindingProblem(state, report, binding) {
  const object = value => value && typeof value === "object" && !Array.isArray(value);
  if (!object(binding) || typeof binding.planId !== "string" || !binding.planId.trim() || binding.tenantId !== state.activeTenant ||
    !Number.isSafeInteger(binding.sourceRevision) || binding.sourceRevision < 1 || !Number.isSafeInteger(binding.revision) || binding.revision < 1 ||
    typeof binding.status !== "string" || typeof binding.confirmed !== "boolean" || !object(binding.snapshot)) return "固定本期计划的身份或版本损坏，请核对原始引用。";
  const snapshot = binding.snapshot;
  if (snapshot.revision !== binding.revision || snapshot.revision > binding.sourceRevision || snapshot.subjectKey !== report.subjectKey ||
    planPeriods[snapshot.periodKind] !== report.cadence || snapshot.periodStart !== report.periodStart || snapshot.periodEnd !== report.periodEnd)
    return "固定本期计划不属于当前报告的主体、周期或基线版本。";
  if (Array.isArray(snapshot.items) && snapshot.items.some(item => item && ["goalId", "krId"].some(key => item[key] != null && typeof item[key] !== "string")))
    return "固定本期计划的目标或 KR 引用损坏，请核对原始引用。";
  // Reuse structural baseline validation; this does not grant confirmation.
  return planConfirmedSnapshotProblem(state, { ...snapshot, tenantId: binding.tenantId, confirmedSnapshot: snapshot });
}
export function reportCurrentPlanCheck(state, report) {
  const errors = [], warnings = [], binding = report.currentPlanBinding;
  if (!binding) return { errors, warnings };
  const invalid = reportCurrentPlanBindingProblem(state, report, binding);
  if (invalid) return { errors: [invalid], warnings };
  if (!binding.confirmed) warnings.push("N2：固定本期计划尚未由 Human 上级确认；报告确认不会批准计划。");
  if (binding.snapshot.timezone !== report.timezone) warnings.push(`N2：固定本期计划沿用原时区 ${binding.snapshot.timezone}，未改写原计划。`);
  try {
    const live = reportCurrentPlan(state, report);
    const current = live && reportCurrentPlanBinding(state, report, live.id, live.revision);
    const same = current && current.planId === binding.planId && current.confirmed === binding.confirmed && JSON.stringify(current.snapshot) === JSON.stringify(binding.snapshot);
    if (!same) (report.submission ? warnings : errors).push("本期计划基线已变化或不可用；原固定引用保留，请明确重新采用当前基线。");
  } catch (error) {
    (report.submission ? warnings : errors).push(`当前本期计划无法核对：${error.message}；原固定引用保留。`);
  }
  return { errors, warnings };
}
export function reportCurrentPlanFacts(state, report, now = new Date()) {
  if (!/^(human|agent):[^:]+$/.test(report.subjectKey || "")) return { plans: [], issues: [] };
  const kind = Object.keys(planPeriods).find(key => planPeriods[key] === report.cadence);
  if (!kind) return { plans: [], issues: ["本期计划周期未声明，不能推断计划基线。"] };
  let plan;
  try { plan = reportCurrentPlan(state, report); } catch (error) { return { plans: [], issues: [error.message] }; }
  if (!plan) return { plans: [], issues: ["本期尚无同主体、同日历周期的计划；承诺与进度基线未确认。"] };
  const facts = planReportFacts(state, plan, now, { historical: true });
  const issues = [...facts.issues];
  if (!plan.confirmedSnapshot) issues.push("本期计划尚未由 Human 上级确认，不能使用草稿承诺计算进度。");
  else if (plan.status !== "已确认") issues.push("本期计划有待确认修订；继续以原已确认基线核对，未采用新草稿承诺。");
  if (plan.timezone !== report.timezone) issues.push(`本期计划沿用原时区 ${plan.timezone}；报告时区为 ${report.timezone}，未改写原计划。`);
  return { plans: [{ id: plan.id, ...facts }], issues };
}
export function reportNextPlanDescriptor(state, report) {
  if (!validatePeriodicReport(state, report)) throw Error("旧报告未声明有效周期，不能推断下周期计划。");
  if (!planSubject(state, report.subjectKey)) throw Error("项目与团队报告的下期计划需汇总成员计划，不能创建虚构的成员计划。");
  const periodKind = Object.keys(planPeriods).find(kind => planPeriods[kind] === report.cadence);
  const next = new Date(`${report.periodEnd}T00:00:00Z`);
  next.setUTCDate(next.getUTCDate() + 1);
  const date = next.toISOString().slice(0, 10);
  return { subjectKey: report.subjectKey, periodKind, timezone: report.timezone, ...planPeriod(periodKind, date, report.timezone) };
}
export function reportNextPlan(state, report) {
  const descriptor = reportNextPlanDescriptor(state, report);
  const matches = (state.resources.plans || []).filter(plan => plan.tenantId === state.activeTenant && planKey(state, plan) === planKey(state, descriptor));
  if (matches.length > 1) throw Error("下周期存在重复计划，请先处理冲突，不能任意采用其中一份。");
  const plan = matches[0];
  if (plan && !planCanRead(state, plan)) throw Error("当前主体无权读取下周期计划。");
  return plan || null;
}
// Called inside the report generation transaction. System drafting is distinct
// from editing/submitting as a member; all existing author guards remain intact.
export function ensureReportNextPlanDraft(state, report, makeId, now = new Date()) {
  const existing = reportNextPlan(state, report);
  if (existing) return existing;
  const descriptor = reportNextPlanDescriptor(state, report);
  const proposal = periodPlanDraftProposal(state, descriptor, report, makeId, now);
  const plan = { ...descriptor, id: makeId("PLAN"), tenantId: state.activeTenant, status: "草稿", revision: 1,
    items: proposal.items, draftSources: proposal.sources, draftIssues: proposal.issues,
    draftedBy: { kind: "System", id: "SYSTEM:local-plan-draft", source: "本地系统规则起草，未调用 Agent runtime" },
    history: [], freeTaskFacts: [], createdAt: now.toISOString() };
  if (!planCanRead(state, plan)) throw Error("当前主体无权读取该成员下期计划，未创建不可访问的草稿。");
  state.resources.plans ??= [];
  state.resources.plans.unshift(plan);
  return plan;
}
export function reportNextPlanBinding(state, report, planId, revision) {
  const plan = reportNextPlan(state, report);
  if (!plan || plan.id !== planId) throw Error("请选择同一主体紧邻下周期的真实计划。");
  if (plan.revision !== revision) throw Error("下周期计划修订已变化，请重新选择当前版本。");
  const problem = planConfirmedSnapshotProblem(state, plan);
  if (problem) throw Error(problem);
  return { planId: plan.id, revision: plan.revision, tenantId: plan.tenantId, status: plan.status,
    snapshot: planSnapshot(state, plan), confirmedSnapshot: clone(plan.confirmedSnapshot || null), checks: clone(planChecks(state, plan)) };
}
export function reportNextPlanCheck(state, report) {
  const errors = [], warnings = [], binding = report.nextPlanBinding;
  if (!binding) return { errors, warnings };
  try {
    const descriptor = reportNextPlanDescriptor(state, report), saved = binding.snapshot;
    if (binding.tenantId !== state.activeTenant || !saved || ["subjectKey", "periodKind", "periodStart", "periodEnd"].some(key => saved[key] !== descriptor[key]))
      throw Error("固定下期计划不属于当前租户、主体或紧邻下周期。");
    const currentProblem = planCurrentItemsProblem(saved);
    if (currentProblem) throw Error(currentProblem);
    dateInZone(saved.timezone);
    const problem = planConfirmedSnapshotProblem(state, { ...saved, tenantId: binding.tenantId, status: binding.status, confirmedSnapshot: binding.confirmedSnapshot });
    if (problem) throw Error(problem);
    let changed = true;
    try {
      const live = reportNextPlan(state, report);
      changed = !live || live.id !== binding.planId || live.revision !== binding.revision ||
        JSON.stringify(reportNextPlanBinding(state, report, live.id, live.revision)) !== JSON.stringify(binding);
    } catch (error) {
      if (!report.submission) throw error;
      warnings.push(`当前下期计划无法唯一读取：${error.message}；原固定提交保留。`);
    }
    // Fixed submissions retain the adopted commitment, even after the live Plan
    // changes. Drafts must explicitly adopt the new revision before submission.
    if (changed) (report.submission ? warnings : errors).push("下周期计划已变化或不可用；保留固定版本，需重新核对并明确采用新版本。");
    const checks = report.submission ? binding.checks : planChecks(state, { ...saved, tenantId: binding.tenantId, confirmedSnapshot: binding.confirmedSnapshot });
    if (!checks || !Array.isArray(checks.blocking)) throw Error("固定下期计划缺少采用时的核对依据。");
    errors.push(...checks.blocking.map(issue => `${issue.code}：下期计划 · ${issue.message}`));
    if (!binding.confirmedSnapshot || JSON.stringify(planCommitment(binding.confirmedSnapshot.items)) !== JSON.stringify(planCommitment(saved.items)) ||
      JSON.stringify(binding.confirmedSnapshot.references) !== JSON.stringify(saved.references))
      warnings.push("N2：下周期计划承诺尚未由 Human 上级确认；报告确认不会批准计划。");
    if (saved.timezone !== report.timezone) warnings.push(`下期计划沿用原时区 ${saved.timezone}；报告时区为 ${report.timezone}，未改写原计划。`);
  } catch (error) { errors.push(error.message); }
  return { errors, warnings };
}
// A fixed baseline is not replaceable by today's draft or guessed metadata.
export function planConfirmedSnapshotProblem(state, plan) {
  const object = value => value && typeof value === "object" && !Array.isArray(value);
  if (!object(plan) || plan.tenantId !== state.activeTenant) return "计划归属未证实为当前租户，不能核对已确认基线。";
  if (plan.confirmedSnapshot == null) return plan.status === "已确认" ? "已确认计划缺少固定基线，请核对原始确认记录；不会使用当前草稿补造。" : null;
  const snapshot = plan.confirmedSnapshot;
  if (!object(snapshot) || !Number.isSafeInteger(snapshot.revision) || snapshot.revision < 1 || !Array.isArray(snapshot.items)) return "已确认计划快照损坏，请核对原始基线；不会使用当前草稿补造。";
  for (const key of ["subjectKey", "periodKind", "periodStart", "periodEnd", "timezone"]) {
    if (typeof snapshot[key] !== "string" || !snapshot[key] || snapshot[key] !== plan[key]) return "已确认计划的主体、周期或时区缺失或与当前记录冲突，请核对原始基线。";
  }
  if (!/^(human|agent):[^:]+$/.test(snapshot.subjectKey)) return "已确认计划主体格式无效。";
  try {
    const period = planPeriod(snapshot.periodKind, snapshot.periodStart, snapshot.timezone);
    if (period.periodStart !== snapshot.periodStart || period.periodEnd !== snapshot.periodEnd) return "已确认计划的起止日期与日历周期不一致。";
  } catch (error) { return `已确认计划的周期或时区无效：${error.message}`; }
  const ids = new Set();
  for (const item of snapshot.items) {
    if (!object(item) || typeof item.id !== "string" || !item.id.trim() || ids.has(item.id) || typeof item.title !== "string" || !item.title.trim() ||
      !["WORK_ITEM", "FREE_TASK"].includes(item.kind) || typeof item.weight !== "number" || !Number.isFinite(item.weight) || item.weight <= 0 ||
      (item.kind === "WORK_ITEM" && (typeof item.workId !== "string" || !item.workId.trim())) ||
      (item.kind === "FREE_TASK" && (!planCategories.includes(item.freeCategory) || typeof item.criteria !== "string" || !item.criteria.trim()))) return "已确认计划项的编号、类型、标题、完成标准、工作引用或正数权重损坏，请核对原始基线。";
    ids.add(item.id);
    if (item.dueDate) {
      try { planPeriod("DAY", item.dueDate, snapshot.timezone); }
      catch { return "已确认计划项的承诺日期无效，请核对原始基线。"; }
    }
  }
  if (!Number.isFinite(snapshot.items.reduce((sum, item) => sum + item.weight, 0))) return "已确认计划的权重合计超出可核对范围。";
  return null;
}
export function planFreeTaskFactValid(entry) {
  return entry && typeof entry === "object" && !Array.isArray(entry) &&
    entry.kind === "FREE_TASK_EVIDENCE" &&
    ["id", "itemId", "subjectKey", "evidence", "itemDefinition", "time"].every(key => typeof entry[key] === "string" && entry[key].trim()) && Number.isFinite(Date.parse(entry.time)) &&
    /^(human|agent):[^:]+$/.test(entry.subjectKey) && entry.actor &&
    entry.actor.kind === (entry.subjectKey.startsWith("agent:") ? "Agent" : "Human") && entry.actor.id === entry.subjectKey.split(":")[1] &&
    (entry.actor.kind !== "Agent" || (typeof entry.actor.recordedBy === "string" && entry.actor.recordedBy.trim()));
}
export function planReportFacts(state, plan, now = new Date(), { historical = false } = {}) {
  const problem = planConfirmedSnapshotProblem(state, plan);
  if (problem) return { basisRevision: null, facts: [], factKeys: [], outcomes: [], progress: null, expected: null, unknown: null, blocked: null, issues: [problem] };
  const items = plan.confirmedSnapshot?.items || [], facts = new Map(), outcomes = [];
  const identity = plan.subjectKey.startsWith("agent:") ? plan.subjectKey : plan.subjectKey.slice(6);
  for (const item of items) {
    let fact, unknown = false, blocked = false;
    if (item.kind === "WORK_ITEM") {
      const work = state.work.find(work => work.id === item.workId);
      blocked = !work || ["等待", "已取消"].includes(work.status);
      const entries = Array.isArray(work?.acceptanceFacts) ? work.acceptanceFacts : [];
      const malformed = work?.acceptanceFacts != null && (!Array.isArray(work.acceptanceFacts) || entries.some(entry => !entry || typeof entry !== "object" || Array.isArray(entry) || !entry.id || entry.workId !== work.id || !Number.isFinite(Date.parse(entry.time)) || !Array.isArray(entry.executorIds)));
      unknown = !work || malformed || ((historical ? work.accepted || work.history?.some(entry => entry.accepted) : work.accepted) && !entries.some(entry => entry?.id && entry.workId === work.id && (historical || entry.version === work.version) && Number.isFinite(Date.parse(entry.time)) && Array.isArray(entry.executorIds)));
      fact = !malformed && entries.find(entry => {
        if (!entry?.id || entry.workId !== work.id || (!historical && (entry.version !== work.version || !work.accepted)) || !Number.isFinite(Date.parse(entry.time)) || !Array.isArray(entry.executorIds) || !entry.executorIds.includes(identity)) return false;
        const day = dateInZone(plan.timezone, new Date(entry.time));
        return day >= plan.periodStart && day <= plan.periodEnd && Date.parse(entry.time) <= now.getTime();
      });
    } else {
      const entries = Array.isArray(plan.freeTaskFacts) ? plan.freeTaskFacts : [];
      unknown = plan.freeTaskFacts != null && (!Array.isArray(plan.freeTaskFacts) || entries.some(entry => !planFreeTaskFactValid(entry)));
      fact = !unknown && entries.find(entry => entry?.itemId === item.id && entry.subjectKey === plan.subjectKey && typeof entry.evidence === "string" && entry.evidence.trim() &&
        entry.itemDefinition === JSON.stringify(planCommitment([item])[0]) && Number.isFinite(Date.parse(entry.time)) && Date.parse(entry.time) <= now.getTime() &&
        dateInZone(plan.timezone, new Date(entry.time)) >= plan.periodStart && dateInZone(plan.timezone, new Date(entry.time)) <= plan.periodEnd);
    }
    if (fact) facts.set(fact.id, clone(fact));
    outcomes.push({ itemId: item.id, title: item.title, status: fact ? "完成" : unknown ? "未知" : blocked ? "阻塞" : "未完成", factId: fact?.id || null, weight: item.weight });
  }
  const total = outcomes.reduce((sum, item) => sum + item.weight, 0), complete = outcomes.filter(item => item.status === "完成").reduce((sum, item) => sum + item.weight, 0);
  const today = dateInZone(plan.timezone, now);
  return { basisRevision: plan.confirmedSnapshot?.revision || null, facts: [...facts.values()], factKeys: [...facts.keys()], outcomes,
    progress: !total || outcomes.some(item => item.status === "未知") ? null : complete / total,
    expected: total && items.every(item => item.dueDate) ? items.filter(item => item.dueDate <= today).reduce((sum, item) => sum + item.weight, 0) / total : null,
    unknown: outcomes.filter(item => item.status === "未知").length, blocked: outcomes.filter(item => item.status === "阻塞").length, issues: [] };
}
