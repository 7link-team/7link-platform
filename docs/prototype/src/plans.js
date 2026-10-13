import { reportSubjects, reportPeriod, dateInZone } from "./reporting";
import { goalContributors, structuredGoal } from "./goals";

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
  return state.role !== "external" && (state.role === "owner" || plan.subjectKey === "human:H-lin" || planConfirmer(state, plan.subjectKey).id === "H-lin");
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
  return state.resources.goals.filter(goal => structuredGoal(goal) && goal.status !== "已取消" &&
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
export function planChecks(state, plan) {
  const blocking = [], warnings = [], goals = planEligibleGoals(state, plan.subjectKey), identities = new Set();
  const add = (code, message, item) => blocking.push({ code, message, itemId: item?.id });
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
  return { subjectKey: plan.subjectKey, periodKind: plan.periodKind, periodStart: plan.periodStart, periodEnd: plan.periodEnd, timezone: plan.timezone,
    items: clone(plan.items), references: plan.items.map(item => {
      const goal = state.resources.goals.find(goal => goal.id === item.goalId), work = state.work.find(work => work.id === item.workId);
      return { itemId: item.id, goal: goal ? { id: goal.id, definitionRevision: goal.definitionRevision || goal.revision, status: goal.status } : null,
        work: work ? { id: work.id, version: work.version, requirements: clone(work.requirements), inputs: clone(work.inputs || []), team: clone(work.team || null), owner: work.owner, agentId: work.agentId } : null };
    }) };
}
export function planReportFacts(state, plan, now = new Date(), { historical = false } = {}) {
  const items = plan.confirmedSnapshot?.items || [], facts = new Map(), outcomes = [];
  const identity = plan.subjectKey.startsWith("agent:") ? plan.subjectKey : plan.subjectKey.slice(6);
  for (const item of items) {
    let fact, unknown = false, blocked = false;
    if (item.kind === "WORK_ITEM") {
      const work = state.work.find(work => work.id === item.workId);
      blocked = !work || ["等待", "已取消"].includes(work.status);
      unknown = !work || ((historical ? work.accepted || work.history?.some(entry => entry.accepted) : work.accepted) && !(work.acceptanceFacts || []).some(entry => entry.id && entry.workId === work.id && (historical || entry.version === work.version) && Number.isFinite(Date.parse(entry.time)) && Array.isArray(entry.executorIds)));
      fact = (work?.acceptanceFacts || []).find(entry => {
        if (!entry.id || entry.workId !== work.id || (!historical && (entry.version !== work.version || !work.accepted)) || !Number.isFinite(Date.parse(entry.time)) || !entry.executorIds?.includes(identity)) return false;
        const day = dateInZone(plan.timezone, new Date(entry.time));
        return day >= plan.periodStart && day <= plan.periodEnd && Date.parse(entry.time) <= now.getTime();
      });
    } else fact = (plan.freeTaskFacts || []).find(entry => entry.itemId === item.id && entry.subjectKey === plan.subjectKey && entry.evidence?.trim() &&
      entry.itemDefinition === JSON.stringify(planCommitment([item])[0]) && Number.isFinite(Date.parse(entry.time)) && Date.parse(entry.time) <= now.getTime() &&
      dateInZone(plan.timezone, new Date(entry.time)) >= plan.periodStart && dateInZone(plan.timezone, new Date(entry.time)) <= plan.periodEnd);
    if (fact) facts.set(fact.id, clone(fact));
    outcomes.push({ itemId: item.id, title: item.title, status: fact ? "完成" : unknown ? "未知" : blocked ? "阻塞" : "未完成", factId: fact?.id || null, weight: item.weight });
  }
  const total = outcomes.reduce((sum, item) => sum + item.weight, 0), complete = outcomes.filter(item => item.status === "完成").reduce((sum, item) => sum + item.weight, 0);
  const today = dateInZone(plan.timezone, now);
  return { basisRevision: plan.confirmedSnapshot?.revision || null, facts: [...facts.values()], factKeys: [...facts.keys()], outcomes,
    progress: !total || outcomes.some(item => item.status === "未知") ? null : complete / total,
    expected: total && items.every(item => item.dueDate) ? items.filter(item => item.dueDate <= today).reduce((sum, item) => sum + item.weight, 0) / total : null,
    unknown: outcomes.filter(item => item.status === "未知").length, blocked: outcomes.filter(item => item.status === "阻塞").length };
}
