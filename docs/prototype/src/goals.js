import { reportSubjects, reportPeriod, dateInZone } from "./reporting";
import { channelReadProblem } from "./channels";

export const goalPeriods = { YEAR: "年报", QUARTER: "季报", MONTH: "月报", WEEK: "周报" };
export const goalMetrics = {
  ACCEPTED_WORK_ITEMS: "首次接受工作数", FIRST_PASS_RATE: "首次验收率", COST_PER_ACCEPTED: "每项接受成本",
  EVIDENCE_COVERAGE: "证据覆盖率", REWORK_CYCLE_P50: "返工周期 P50", ON_TIME_REPORT_RATE: "报告及时率", CLIENT_FIRST_ACCEPTANCE: "甲方首次接受率",
};
export const closedGoalStates = ["已达成", "未达成", "已取消"];
export const structuredGoal = goal => Array.isArray(goal?.krs);
export function goalNumber(value, label = "数值") {
  if (!["number", "string"].includes(typeof value) || !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(String(value).trim()) || !Number.isFinite(Number(value)))
    throw Error(`${label}必须是完整的有限数值，单位请单独填写。`);
  return Number(value);
}
export const goalSubjects = reportSubjects;
export function goalPeriod(kind, date, timezone) {
  dateInZone(timezone);
  if (!goalPeriods[kind]) throw Error("请选择年、季、月或周周期。");
  return reportPeriod(goalPeriods[kind], date);
}
export function krDefinition(kr) {
  return Object.fromEntries(["id", "title", "unit", "direction", "baseline", "target", "weight", "source", "metric", "evidenceRequired", "kind", "delivery", "use", "criteria", "evidencePlan", "dueDate"].map(key => [key, kr[key]]));
}
export function goalDefinition(goal) {
  return { ...(goal.collaborationVersion ? Object.fromEntries(["collaborationVersion", "tenantId", "goalType", "timeMode", "stageName", "beneficiaries", "value", "resources", "reviewerId", "visibility", "collaborators"].map(key => [key, goal[key]])) : {}), name: goal.name, subjectKey: goal.subjectKey, ownerId: goal.ownerId, ownerKind: goal.ownerKind,
    periodKind: goal.periodKind, periodStart: goal.periodStart, periodEnd: goal.periodEnd, timezone: goal.timezone,
    krs: goal.krs.map(krDefinition), alignments: goal.alignments || [], riskPolicy: goal.riskPolicy || {} };
}
export function normalizeGoal(state, input) {
  const subject = goalSubjects(state).find(subject => subject.key === input.subjectKey);
  if (!subject) throw Error("请选择当前租户的目标归属。");
  if (!input.name?.trim()) throw Error("目标名称不能为空。");
  const period = input.timeMode === "STAGE" ? stageGoalPeriod(input) : goalPeriod(input.periodKind, input.periodStart, input.timezone);
  if (input.periodStart !== period.periodStart || input.periodEnd !== period.periodEnd) throw Error("目标起止日期须匹配所选日历周期。");
  if (!Array.isArray(input.krs) || input.krs.length < 1 || input.krs.length > 5) throw Error("每个目标需要 1 至 5 个 KR。");
  if (new Set(input.krs.map(kr => kr.id)).size !== input.krs.length || input.krs.some(kr => !kr.id)) throw Error("KR 标识不能为空或重复。");
  const krs = input.krs.map((kr, index) => {
    if (kr.kind === "QUALITATIVE") {
      const weight = goalNumber(kr.weight, "KR 权重");
      if (weight <= 0 || weight > 10 || ["title", "delivery", "use", "criteria", "evidencePlan"].some(key => !kr[key]?.trim())) throw Error("定性 KR 须明确结果、交付物、作用、评审标准、证据约定和有效权重。");
      if (!validGoalDate(kr.dueDate) || kr.dueDate < period.periodStart || kr.dueDate > period.periodEnd) throw Error("定性 KR 期限须在目标范围内。");
      return { ...krDefinition(kr), kind: "QUALITATIVE", source: "MANUAL", metric: "", weight, unit: "证据评审", baseline: null, target: null, direction: "INCREASE", evidenceRequired: true };
    }
    if (!kr.title?.trim() || !kr.unit?.trim()) throw Error(`KR ${index + 1} 请填写标题与单位。`);
    const baseline = goalNumber(kr.baseline, `KR ${index + 1} 基线`), target = goalNumber(kr.target, `KR ${index + 1} 目标值`), weight = goalNumber(kr.weight, `KR ${index + 1} 权重`);
    if (weight <= 0 || weight > 10) throw Error("KR 权重须大于 0 且不超过 10。");
    if (!["INCREASE", "DECREASE"].includes(kr.direction) || (kr.direction === "INCREASE" ? target <= baseline : target >= baseline)) throw Error("KR 目标值须与基线不同，并符合所选增减方向。");
    if (!["MANUAL", "AUTO"].includes(kr.source) || (kr.source === "AUTO" && !goalMetrics[kr.metric])) throw Error("请选择有效的指标计算来源。");
    return { ...krDefinition(kr), title: kr.title.trim(), unit: kr.unit.trim(), baseline, target, weight,
      metric: kr.source === "AUTO" ? kr.metric : "", evidenceRequired: Boolean(kr.evidenceRequired) };
  });
  const riskPolicy = {};
  for (const key of ["lagThreshold", "freshnessDays", "nearDueDays"]) {
    const value = input.riskPolicy?.[key];
    if (value === "" || value === undefined || value === null) { riskPolicy[key] = null; continue; }
    const number = goalNumber(value, "风险规则");
    if (number < 0 || (key === "lagThreshold" ? number > 1 : !Number.isInteger(number))) throw Error("滞后阈值为 0–1，其余风险天数为非负整数。");
    riskPolicy[key] = number;
  }
  const weekdays = input.riskPolicy?.workingWeekdays || [];
  if (!Array.isArray(weekdays) || weekdays.some(day => !Number.isInteger(day) || day < 0 || day > 6)) throw Error("请选择有效的每周工作日。");
  riskPolicy.workingWeekdays = [...new Set(weekdays)].sort();
  const result = { ...input, ...period, name: input.name.trim(), krs, riskPolicy, alignments: input.alignments || [] };
  if (input.collaborationVersion) Object.assign(result, normalizeGoalCollaboration(state, result));
  validateGoalAlignments(state, result);
  return result;
}
export function validateGoalAlignments(state, goal, publishing = false) {
  if (publishing && ["project", "human", "agent"].includes(goal.subjectKey.split(":" )[0]) && !goal.alignments?.length) throw Error("发布前请对齐一个有效的上级 KR。");
  const seen = new Set(), goals = state.resources.goals.map(item => item.id === goal.id ? goal : item);
  if (!goals.some(item => item.id === goal.id)) goals.push(goal);
  for (const edge of goal.alignments || []) {
    const target = effectiveGoal(goals.find(item => item.id === edge.targetGoalId));
    if (!target || goalReadProblem(state, target) || !structuredGoal(target) || !target.krs.some(kr => kr.id === edge.targetKrId)) throw Error("对齐目标或 KR 不存在于当前租户。");
    const key = `${edge.targetGoalId}:${edge.targetKrId}`;
    if (seen.has(key) || target.id === goal.id) throw Error("不允许重复对齐或对齐自身。");
    seen.add(key);
    if (!["breakdown", "contribution"].includes(edge.relation) || !edge.reason?.trim()) throw Error("对齐须明确拆解或贡献关系及依据。");
    if (goal.periodStart < target.periodStart || goal.periodEnd > target.periodEnd) throw Error("子目标周期须包含在对齐目标周期内。");
    if (closedGoalStates.includes(target.status)) throw Error("不能对齐已关闭目标，请选择当前周期的开放目标。");
    if (publishing && !["进行中", "存在风险"].includes(target.status)) throw Error("对齐的上级目标须先批准发布。");
    if (edge.relation === "breakdown") {
      const rank = key => ({ tenant: 0, team: 1, project: 2, human: 3, agent: 3 })[key.split(":")[0]];
      if (rank(target.subjectKey) >= rank(goal.subjectKey)) throw Error("拆解关系必须从上级归属指向下级；跨项目请使用贡献关系。");
    }
  }
  const visiting = new Set(), visited = new Set();
  const walk = id => {
    if (visiting.has(id)) throw Error("对齐关系不能形成循环。");
    if (visited.has(id)) return;
    visiting.add(id);
    for (const edge of goals.find(item => item.id === id)?.alignments || []) walk(edge.targetGoalId);
    visiting.delete(id); visited.add(id);
  };
  walk(goal.id);
}
export function goalEvidence(state, reference) {
  if (["URL", "FILE"].includes(reference.kind)) {
    if (!reference.ref?.trim() || (reference.kind === "URL" && !/^https?:\/\//i.test(reference.ref))) throw Error("请填写有效证据引用，外链须使用 http 或 https。");
    return { kind: reference.kind, ref: reference.ref.trim(), label: reference.ref.trim(), verified: false };
  }
  const rows = reference.kind === "work" ? state.work : state.resources[{ delivery: "deliveries", report: "reports", testreport: "testreports" }[reference.kind]];
  const item = rows?.find(item => item.id === reference.id);
  if (!item) throw Error("证据不存在于当前租户。");
  const currentVersion = reference.kind === "work" ? item.version : reference.kind === "delivery" ? item.version : item.revision || 1;
  if (String(reference.version) !== String(currentVersion)) throw Error("证据版本已变化，请重新选择当前固定记录。");
  return { kind: reference.kind, id: item.id, version: currentVersion, label: item.title || item.name,
    snapshot: structuredClone(item), verified: false };
}
export function evidenceHref(ref) {
  if (ref.kind === "work") return `/p/${ref.snapshot?.project}/artifacts/${ref.id}?version=${encodeURIComponent(ref.version)}`;
  return { delivery: `/deliveries/${ref.id}`, report: `/reports/${ref.id}`, testreport: `/testreports/${ref.id}` }[ref.kind] || null;
}
export function krObservation(goal, kr) {
  const observation = kr.observations?.find(item => item.id === kr.currentObservationId);
  return observation?.definitionRevision === kr.definitionRevision ? observation : null;
}
export function krResult(goal, kr, now = new Date()) {
  const observation = krObservation(goal, kr);
  let issue = !observation ? "尚无当前指标观察" : observation.availability !== "available" ? observation.issue || "数据源不可用" : null;
  if (!issue && kr.evidenceRequired && !observation.evidence?.length) issue = "缺少必需佐证";
  if (!issue && goal.riskPolicy?.freshnessDays != null && now.getTime() - Date.parse(observation.observedAt) > goal.riskPolicy.freshnessDays * 86400000) issue = "观察数据已过期";
  if (kr.kind === "QUALITATIVE") {
    if (!issue && !["MET", "NOT_MET", "UNKNOWN"].includes(observation.judgment)) issue = "缺少约定的证据评审结论";
    if (!issue && observation.judgment === "UNKNOWN") issue = "证据不足，结果未知";
    if (!issue && (!observation.delivery?.trim() || !observation.use?.trim())) issue = "尚未记录交付与实际作用";
    return { value: issue ? null : observation.judgment, progress: issue ? null : observation.judgment === "MET" ? 1 : 0, issue, observation };
  }
  if (!issue && !Number.isFinite(observation.value)) issue = "观察数值不可用";
  const value = issue ? null : observation.value;
  const progress = value === null ? null : Math.max(0, Math.min(1, kr.direction === "DECREASE" ? (kr.baseline - value) / (kr.baseline - kr.target) : (value - kr.baseline) / (kr.target - kr.baseline)));
  return { value, progress, issue, observation };
}
export function goalResult(goal, now = new Date()) {
  if (!structuredGoal(goal)) return { progress: null, coverage: 0, issues: ["旧目标尚未转换为结构化 KR"], results: [] };
  const results = goal.krs.map(kr => ({ kr, ...krResult(goal, kr, now) }));
  const total = results.reduce((sum, item) => sum + item.kr.weight, 0), known = results.filter(item => item.progress !== null);
  return { progress: !total || known.length !== results.length ? null : Math.round(known.reduce((sum, item) => sum + item.kr.weight * item.progress, 0) / total * 10000) / 10000,
    coverage: total ? known.reduce((sum, item) => sum + item.kr.weight, 0) / total : 0, results, issues: results.flatMap(item => item.issue ? [`${item.kr.title}：${item.issue}`] : []) };
}
export function goalScopeWorks(state, goal) {
  const [kind, identity] = goal.subjectKey.split(":");
  if (kind === "tenant") return state.work;
  if (kind === "project") return state.work.filter(work => work.project === identity);
  const members = kind === "team" ? state.organization.nodes.find(node => node.id === identity)?.memberIds || [] : [kind === "agent" ? `agent:${identity}` : identity];
  return state.work.filter(work => [...(work.production?.executorIds || []), ...(work.team?.executorIds || []), work.team?.receiverId,
    ...state.organization.members.filter(person => person.name === work.owner).map(person => person.id)].some(id => members.includes(id)));
}
export function readGoalMetric(state, goal, kr, now = new Date()) {
  const base = { value: null, availability: "unavailable", issue: "当前本地事实不足以计算此指标；未以零代替。", evidence: [], factKeys: [], observedAt: now.toISOString() };
  if (kr.metric !== "ACCEPTED_WORK_ITEMS") return base;
  const works = state.work, scoped = new Set(goalScopeWorks(state, goal).map(work => work.id)), keys = new Set(), evidence = [];
  for (const work of works) {
    const entries = Array.isArray(work.acceptanceFacts) ? work.acceptanceFacts : [];
    const malformed = work.acceptanceFacts != null && (!Array.isArray(work.acceptanceFacts) || entries.some(fact => !fact || typeof fact !== "object" || Array.isArray(fact) || !fact.id || !Array.isArray(fact.subjectKeys) || !Number.isFinite(Date.parse(fact.time))));
    const historicalScope = entries.some(fact => Array.isArray(fact?.subjectKeys) && fact.subjectKeys.includes(goal.subjectKey));
    if (malformed && (scoped.has(work.id) || historicalScope)) return { ...base, issue: `${work.id} 接受历史格式损坏，不能推断首次接受或认证为零。` };
    const facts = entries.filter(fact => fact?.id && fact.workId === work.id && Array.isArray(fact.subjectKeys) && Number.isFinite(Date.parse(fact.time))).sort((a, b) => Date.parse(a.time) - Date.parse(b.time));
    if (!facts.length && scoped.has(work.id) && (work.accepted || work.history?.some(item => item.accepted))) return { ...base, issue: `${work.id} 缺少首次接受时间，周期指标不能推测。` };
    const first = facts[0];
    if (!first || !first.subjectKeys?.includes(goal.subjectKey)) continue;
    if (first.firstKnown !== true) return { ...base, issue: `${work.id} 更早的接受历史不完整，不能推断首次接受时间。` };
    const day = dateInZone(goal.timezone, new Date(first.time));
    if (day < goal.periodStart || day > goal.periodEnd || keys.has(work.id)) continue;
    keys.add(work.id); evidence.push({ kind: "work", id: work.id, version: first.version, label: work.title, snapshot: structuredClone(first) });
  }
  return { ...base, value: keys.size, numerator: keys.size, denominator: null, availability: "available", issue: null, factKeys: [...keys].map(id => `first-acceptance:${id}`), evidence };
}
export function goalRisks(state, goal, now = new Date()) {
  if (!structuredGoal(goal) || closedGoalStates.includes(goal.status)) return [];
  const result = goalResult(goal, now), risks = [], today = dateInZone(goal.timezone, now);
  const add = (type, message) => risks.push({ type, message, ownerId: goal.ownerId, ownerKind: goal.ownerKind });
  if (!goal.ownerId) add("owner", "未配置负责人：请补充稳定身份。");
  if (!goal.alignments?.length && !goal.subjectKey.startsWith("tenant:")) add("alignment", "未对齐上级 KR：请明确贡献关系。");
  if (!(state.resources.plans || []).some(plan => plan.confirmedSnapshot?.items?.some(item => item.goalId === goal.id))) add("plan", "无已确认计划支撑：请在周期计划中关联本目标。");
  if (result.progress === null) add("data", `数据不完整：${result.issues.join("；")}`);
  const remaining = (Date.parse(goal.periodEnd) - Date.parse(today)) / 86400000;
  if (goal.riskPolicy?.nearDueDays != null && remaining >= 0 && remaining <= goal.riskPolicy.nearDueDays && result.progress !== 1) add("near-due", "已临近期末：请复核进度、阻碍与下一步。");
  if (remaining < -7) add("closure", "周期结束超过 7 天：请提交期末复核。");
  // Only an explicitly configured working-week calendar may drive the lag rule.
  if (goal.riskPolicy?.lagThreshold != null) {
    const weekdays = goal.riskPolicy.workingWeekdays || [];
    if (!weekdays.length) add("calendar", "滞后规则已配置，但尚无工作日历，暂不计算进度偏离。");
    else if (result.progress !== null) {
      let total = 0, elapsed = 0;
      for (let time = Date.parse(goal.periodStart); time <= Date.parse(goal.periodEnd); time += 86400000) if (weekdays.includes(new Date(time).getUTCDay())) {
        total++; if (time < Date.parse(today)) elapsed++;
      }
      if (total && elapsed / total - result.progress > goal.riskPolicy.lagThreshold) add("lag", `进度偏离：已过 ${elapsed}/${total} 个配置工作日，指标完成 ${Math.round(result.progress * 100)}%。请说明阻碍与调整计划。`);
    }
  }
  return risks;
}
export function conversionDraft(state, goal, makeId) {
  const candidates = [...state.organization.members, ...state.agents.map(agent => ({ ...agent, kind: "Agent" }))];
  const named = candidates.filter(person => person.name === goal.owner);
  const person = goal.ownerId ? candidates.find(person => person.id === goal.ownerId && person.kind === goal.ownerKind) : named.length === 1 ? named[0] : null;
  return { id: goal.id, revision: goal.revision || 1, name: goal.name, owner: goal.owner, ownerId: person?.id || "", ownerKind: person?.kind || "Human",
    subjectKey: "", periodKind: "QUARTER", periodStart: "", periodEnd: "", timezone: "", alignments: [], riskPolicy: {},
    krs: [{ id: makeId("KR"), title: goal.metric || "", unit: "", baseline: String(goal.baseline ?? ""), target: String(goal.target ?? ""), direction: "INCREASE", weight: 1, source: "MANUAL", metric: "", evidenceRequired: false }] };
}

export function goalContributors(state, goalId, krId) {
  const goal = effectiveGoal(state.resources.goals.find(item => item.id === goalId));
  if (!structuredGoal(goal) || (krId && !goal.krs.some(kr => kr.id === krId))) return [];
  const contributors = new Map();
  const add = (item, source) => { if (item.ownerId && item.ownerKind) { const key = `${item.ownerKind}:${item.ownerId}`; const previous = contributors.get(key); contributors.set(key, { id: item.ownerId, kind: item.ownerKind, sources: [...new Set([...(previous?.sources || []), source])] }); } };
  add(goal, "OWNER");
  for (const child of state.resources.goals) if (["进行中", "存在风险"].includes(child.status) && child.alignments?.some(edge => edge.targetGoalId === goalId && (!krId || edge.targetKrId === krId))) add(child, "CHILD_GOAL");
  return [...contributors.values()];
}

// Co-maintenance extends the same goal record; old records are never assigned invented approvals.
export const goalTypes = { ORGANIZATION: "组织", TEAM: "团队", PROJECT_STAGE: "项目阶段", HUMAN: "Human", AGENT: "Agent" };
export const valueReviewChecks = { reasonable: "合理性", feasible: "可实现性", effective: "有效性", contribution: "项目 / 下游贡献", reuse: "重复投入", resources: "资源匹配" };
export const initialGoalGuidance = { revision: 1, objective: "O 描述希望改变的结果与受益对象，不用活动清单代替结果。", keyResult: "KR 明确基线、目标、期限、证据与上下游价值；研究、风险降低、基础建设可约定定性证据评审。", goodExample: "让客服准确处理退款：以抽样审核证明指引被正确采用；研究结论应支撑明确决策。", badExample: "只写完成二十份文档，未说明读者、用途、采用方式与结果证据。" };
export const goalGuidance = s => s.goalGuidance?.current || initialGoalGuidance;
export const validGoalDate = date => /^\d{4}-\d{2}-\d{2}$/.test(date || "") && Number.isFinite(Date.parse(date)) && new Date(date).toISOString().slice(0,10) === date;
export function stageGoalPeriod(input) {
  dateInZone(input.timezone);
  if (!goalPeriods[input.periodKind] || input.goalType !== "PROJECT_STAGE" || !input.stageName?.trim() || !validGoalDate(input.periodStart) || !validGoalDate(input.periodEnd) || input.periodEnd < input.periodStart) throw Error("阶段目标须明确阶段名称、有效起止日期、周期维度和时区。");
  return { periodStart: input.periodStart, periodEnd: input.periodEnd };
}
export function goalManagers(s, humanId) {
  const ids = new Set(), visited = new Set();
  const visit = node => { if (!node || visited.has(node.id)) return; visited.add(node.id); if (node.ownerId && node.ownerId !== humanId) ids.add(node.ownerId); visit(s.organization.nodes.find(p => p.id === node.parentId)); };
  s.organization.nodes.filter(n => n.memberIds?.includes(humanId) || n.ownerId === humanId).forEach(visit);
  return [...ids].filter(id => s.organization.members.some(p => p.id === id && p.kind === "Human"));
}
export function goalProjectOwners(s, projectId) {
  const p = s.projects.find(p => p.id === projectId); if (!p || projectId === "private") return [];
  const people = s.organization.members.filter(p => p.kind === "Human"), matches = people.filter(h => p.ownerId ? h.id === p.ownerId : h.name === p.owner);
  return [...new Set([...(matches.length === 1 ? [matches[0].id] : []), ...s.organization.projectRoles.filter(r => r.projectId === projectId && r.role === "项目负责人" && people.some(p => p.id === r.memberId)).map(r => r.memberId)])];
}
export function goalReviewers(s, goal) {
  const [kind, key] = (goal.subjectKey || "").split(":");
  if (kind === "project") return goalProjectOwners(s, key);
  if (kind === "human") return [...new Set([key, ...goalManagers(s, key)])];
  if (kind === "agent") { const a = s.agents.find(a => a.id === key), owners = s.organization.members.filter(p => p.kind === "Human" && p.name === a?.owner); return owners.length === 1 ? [owners[0].id] : []; }
  if (kind === "team") { const n = s.organization.nodes.find(n => n.id === key); return n?.ownerId ? [n.ownerId] : []; }
  return kind === "tenant" && key === s.activeTenant ? s.organization.nodes.filter(n => !n.parentId).map(n => n.ownerId).filter(Boolean) : [];
}
export function goalReadProblem(s, goal, actorId = "H-lin") {
  if (!goal || !["owner","executor"].includes(s.role) || goal.tenantId && goal.tenantId !== s.activeTenant) return "目标不在当前授权范围。";
  if (!goal.collaborationVersion) return actorId === "H-lin" ? "" : "旧目标未记录 Agent 读取授权。";
  const human = s.organization.members.some(p => p.id === actorId && p.kind === "Human"), agent = s.agents.find(a => a.id === actorId);
  if (!human && (!agent || ["退役", "已退役", "待批准"].includes(agent.status))) return "目标主体不可用。";
  const explicit = goal.ownerId === actorId || goal.reviewerId === actorId || goal.collaborators?.some(c => c.id === actorId);
  const [kind, key] = goal.subjectKey.split(":");
  if (kind === "project" && human && channelReadProblem(s, key, actorId)) return "未获当前项目访问权限；组织上级不授予跨项目访问。";
  if (["human", "agent"].includes(kind) && goal.visibility === "PRIVATE") return explicit ? "" : "个人私密目标仅本人和明确协作者可见。";
  if (!human) return explicit ? "" : "Agent 未获此目标明确授权；任务编组不等于目标权限。";
  if (explicit || kind === "project" || kind === "tenant" || kind === "team" && s.organization.nodes.some(n => n.id === key && (n.ownerId === actorId || n.memberIds?.includes(actorId))) || kind === "human" && goalManagers(s, key).includes(actorId)) return "";
  return "目标不在当前管理或协作范围。";
}
export function goalActionProblem(s, goal, action, actorId = "H-lin") {
  const read = goalReadProblem(s, goal, actorId); if (read) return read;
  if (s.offline && action !== "read") return "离线中，请恢复连接后操作。";
  if (action === "read" || action === "suggest") return "";
  if (action === "approve") return actorId === goal.reviewerId && s.organization.members.some(p => p.id === actorId && p.kind === "Human") && goalReviewers(s, goal).includes(actorId) ? "" : "须由明确且仍有责任范围的 Human 审核；Agent 不自行批准。";
  if (action === "manage") return (actorId === goal.ownerId && goal.ownerKind === "Human") || actorId === goal.reviewerId && goalReviewers(s, goal).includes(actorId) ? "" : "协作者授权须由本人或明确责任 Human 维护。";
  if (actorId === goal.ownerId || actorId === goal.reviewerId && goalReviewers(s, goal).includes(actorId) || goal.collaborators?.some(c => c.id === actorId && c.permissions?.includes(action))) return "";
  return "此身份没有该目标的草稿 / check-in 权限。";
}
export const visibleGoals = s => s.resources.goals.filter(g => !goalReadProblem(s, g));
export function normalizeGoalCollaboration(s, input) {
  const [kind] = input.subjectKey.split(":"), expected = { tenant: "ORGANIZATION", team: "TEAM", project: "PROJECT_STAGE", human: "HUMAN", agent: "AGENT" }[kind];
  if (input.goalType !== expected || input.tenantId && input.tenantId !== s.activeTenant) throw Error("目标类型、归属与当前租户必须一致，负责人类型不能代替目标类型。");
  if (!["NATURAL", "STAGE"].includes(input.timeMode) || input.timeMode === "STAGE" && expected !== "PROJECT_STAGE") throw Error("独立阶段起止仅适用于项目阶段目标。");
  if (expected === "PROJECT_STAGE" && !input.stageName?.trim()) throw Error("请填写项目阶段名称。");
  if (!goalReviewers(s, input).includes(input.reviewerId)) throw Error("请选择来自现有责任范围的 Human 审核者。");
  if (!["PRIVATE", "SCOPE"].includes(input.visibility) || (!["HUMAN", "AGENT"].includes(expected) && input.visibility !== "SCOPE")) throw Error("请明确目标可见范围。");
  const collaborators = input.collaborators || [];
  if (!Array.isArray(collaborators) || new Set(collaborators.map(c=>c.id)).size !== collaborators.length) throw Error("目标协作者重复或格式无效。");
  for (const c of collaborators) {
    const person = c.kind === "Human" ? s.organization.members.find(p=>p.id===c.id&&p.kind==="Human") : c.kind === "Agent" ? s.agents.find(a=>a.id===c.id&&!["退役","已退役","待批准"].includes(a.status)) : null;
    if (!person || !Array.isArray(c.permissions) || c.permissions.some(p=>!["suggest","draft","checkin"].includes(p))) throw Error("协作者必须是当前有效主体，权限仅建议 / 草稿 / check-in。");
    if (c.kind === "Human" && c.id !== input.ownerId && !goalReviewers(s,input).includes(c.id)) throw Error("Human 共同维护者须为本人或责任范围中的指定上级 / 项目负责人。");
  }
  if (input.quadrant) validateGoalQuadrantPoint(s, input, input.quadrant);
  return { collaborationVersion: 1, tenantId: s.activeTenant, goalType: expected, timeMode: input.timeMode, stageName: input.stageName?.trim() || "", beneficiaries: input.beneficiaries?.trim() || "", value: input.value?.trim() || "", resources: input.resources?.trim() || "", reviewerId: input.reviewerId, visibility: input.visibility, collaborators: structuredClone(collaborators), quadrant: input.quadrant || null };
}
export function goalValueReviewProblem(s, goal) {
  if (["beneficiaries","value","resources"].some(key=>!goal[key]?.trim())) return "请先明确受益对象、下游价值与资源假设，再作价值审查。";
  const review = goal.valueReview;
  if (!review || review.source !== "LOCAL_SIMULATION" || review.definitionRevision !== goal.definitionRevision || review.guidanceRevision !== goalGuidance(s).revision || JSON.stringify(review.definition) !== JSON.stringify(goalDefinition(goal))) return "请对当前目标定义运行明确标识的 Agent 价值审查；旧定义或旧指导版本结论不可复用。";
  if (goalActionProblem(s, goal, "draft", review.agentId)) return "价值审查 Agent 已失效或不再获授权。";
  if (Object.keys(valueReviewChecks).some(key => review.checks?.[key]?.result !== "PASS" || !review.checks[key].basis?.trim())) return "价值审查尚有未通过 / 未知项，不能正式发布。";
  return "";
}
export function validateGoalQuadrantConfig(input) {
  const axes = {};
  for (const key of ["x", "y"]) { const a = input[key]; if (!a?.label?.trim() || !a.lowLabel?.trim() || !a.highLabel?.trim()) throw Error("请填写两条轴及各区间的明确口径。"); const min=goalNumber(a.min),split=goalNumber(a.split),max=goalNumber(a.max); if (!(min<split&&split<max)) throw Error("轴须满足最小值 < 分界 < 最大值。"); axes[key]={label:a.label.trim(),lowLabel:a.lowLabel.trim(),highLabel:a.highLabel.trim(),min,split,max}; }
  return axes;
}
export function validateGoalQuadrantPoint(s, goal, point) {
  if (["human", "agent"].includes(goal.subjectKey.split(":")[0])) throw Error("个人与 Agent 目标不进入非个人四象限。");
  const config = s.goalQuadrant?.current;
  if (!config || point.configRevision !== config.revision) throw Error("请先配置并核对当前四象限轴版本。");
  for (const key of ["x","y"]) { const v=goalNumber(point[key]); if(v<config[key].min||v>config[key].max)throw Error("象限数值超出已定义区间。"); }
  if (!point.reason?.trim()) throw Error("请记录象限位置的判断依据。");
}
export function goalQuadrantBucket(s, goal) {
  if (["human","agent"].includes(goal.subjectKey?.split(":")[0])) return null;
  try {validateGoalQuadrantPoint(s,goal,goal.quadrant);}catch{return "unknown";}
  const c=s.goalQuadrant.current;return `${Number(goal.quadrant.x)>=c.x.split?"high":"low"}-${Number(goal.quadrant.y)>=c.y.split?"high":"low"}`;
}

export function effectiveGoal(goal) { return goal?.activeBaseline && goal.definitionRevision !== goal.activeBaseline.definitionRevision ? {...goal,...goal.activeBaseline.definition,krs:goal.activeBaseline.krs,definitionRevision:goal.activeBaseline.definitionRevision,status:"进行中"} : goal; }
