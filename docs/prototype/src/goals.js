import { reportSubjects, reportPeriod, dateInZone } from "./reporting";

export const goalPeriods = { YEAR: "年报", QUARTER: "季报", MONTH: "月报" };
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
  if (!goalPeriods[kind]) throw Error("请选择年、季或月周期。");
  return reportPeriod(goalPeriods[kind], date);
}
export function krDefinition(kr) {
  return Object.fromEntries(["id", "title", "unit", "direction", "baseline", "target", "weight", "source", "metric", "evidenceRequired"].map(key => [key, kr[key]]));
}
export function goalDefinition(goal) {
  return { name: goal.name, subjectKey: goal.subjectKey, ownerId: goal.ownerId, ownerKind: goal.ownerKind,
    periodKind: goal.periodKind, periodStart: goal.periodStart, periodEnd: goal.periodEnd, timezone: goal.timezone,
    krs: goal.krs.map(krDefinition), alignments: goal.alignments || [], riskPolicy: goal.riskPolicy || {} };
}
export function normalizeGoal(state, input) {
  const subject = goalSubjects(state).find(subject => subject.key === input.subjectKey);
  if (!subject) throw Error("请选择当前租户的目标归属。");
  if (!input.name?.trim()) throw Error("目标名称不能为空。");
  if (["tenant", "team"].includes(input.subjectKey.split(":")[0]) && input.periodKind === "MONTH") throw Error("组织与团队目标使用年度或季度。");
  if (["human", "agent", "project"].includes(input.subjectKey.split(":")[0]) && input.periodKind === "YEAR") throw Error("项目与成员目标使用季度或月度。");
  const period = goalPeriod(input.periodKind, input.periodStart, input.timezone);
  if (input.periodStart !== period.periodStart || input.periodEnd !== period.periodEnd) throw Error("目标起止日期须匹配所选日历周期。");
  if (!Array.isArray(input.krs) || input.krs.length < 1 || input.krs.length > 5) throw Error("每个目标需要 1 至 5 个 KR。");
  if (new Set(input.krs.map(kr => kr.id)).size !== input.krs.length || input.krs.some(kr => !kr.id)) throw Error("KR 标识不能为空或重复。");
  const krs = input.krs.map((kr, index) => {
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
  validateGoalAlignments(state, result);
  return result;
}
export function validateGoalAlignments(state, goal, publishing = false) {
  if (publishing && ["project", "human", "agent"].includes(goal.subjectKey.split(":" )[0]) && !goal.alignments?.length) throw Error("发布前请对齐一个有效的上级 KR。");
  const seen = new Set(), goals = state.resources.goals.map(item => item.id === goal.id ? goal : item);
  if (!goals.some(item => item.id === goal.id)) goals.push(goal);
  for (const edge of goal.alignments || []) {
    const target = goals.find(item => item.id === edge.targetGoalId);
    if (!target || !structuredGoal(target) || !target.krs.some(kr => kr.id === edge.targetKrId)) throw Error("对齐目标或 KR 不存在于当前租户。");
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
    const facts = (work.acceptanceFacts || []).filter(fact => fact.workId === work.id && Number.isFinite(Date.parse(fact.time))).sort((a, b) => Date.parse(a.time) - Date.parse(b.time));
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
  const goal = state.resources.goals.find(item => item.id === goalId);
  if (!structuredGoal(goal) || (krId && !goal.krs.some(kr => kr.id === krId))) return [];
  const contributors = new Map();
  const add = (item, source) => { if (item.ownerId && item.ownerKind) { const key = `${item.ownerKind}:${item.ownerId}`; const previous = contributors.get(key); contributors.set(key, { id: item.ownerId, kind: item.ownerKind, sources: [...new Set([...(previous?.sources || []), source])] }); } };
  add(goal, "OWNER");
  for (const child of state.resources.goals) if (["进行中", "存在风险"].includes(child.status) && child.alignments?.some(edge => edge.targetGoalId === goalId && (!krId || edge.targetKrId === krId))) add(child, "CHILD_GOAL");
  return [...contributors.values()];
}
