import { reportNextPlan, reportNextPlanBinding, reportCurrentPlan, reportCurrentPlanBinding, reportCurrentPlanBindingProblem, planCommitment, planFreeTaskFactValid, planReportFacts } from "./plans";
import { periodicReportFacts, reportAcceptanceMatch, dateInZone } from "./reporting";
import { goalContributors, structuredGoal, effectiveGoal, goalReadProblem } from "./goals";
import { qualityFromPlans } from "./qa";

export const reportReasons = { BLOCKED_DEPENDENCY: "依赖阻塞", SCOPE_CHANGED: "范围变更", UNDERESTIMATED: "低估工作量", REPRIORITIZED: "优先级调整", WAITING_CLIENT: "等待客户", QUALITY_REWORK: "质量返工", RESOURCE: "资源不足", OTHER: "其他" };
export const reportDispositions = { CARRY_TO_NEXT: "结转下期", DROP: "放弃并保留原因", ESCALATE: "升级处理" };
const clone = value => structuredClone(value);
const object = value => value && typeof value === "object" && !Array.isArray(value);
export function reportNextPlanItems(binding) {
  if (binding == null) return [];
  const snapshot = binding.snapshot;
  const strings = (record, keys) => keys.every(key => record[key] == null || typeof record[key] === "string");
  if (!object(binding) || !strings(binding, ["planId", "status"]) || (binding.revision != null && (!Number.isSafeInteger(binding.revision) || binding.revision < 1)) ||
    !object(snapshot) || !strings(snapshot, ["periodStart", "periodEnd", "timezone"]) || !Array.isArray(snapshot.items) ||
    snapshot.items.some(item => !object(item) || typeof item.id !== "string" || !item.id.trim() || typeof item.title !== "string" || !strings(item, ["goalId", "krId", "workId", "freeCategory", "dueDate"])))
    throw Error("固定下期计划快照损坏，原始内容仍保留；请核对计划来源，不会自动清空或补造计划项。");
  return snapshot.items.map(item => ({ key: item.id, title: item.title, goal_id: item.goalId, kr_id: item.krId || "", plan_item_id: item.id,
    work_item_id: item.workId || "", free_category: item.freeCategory || "" }));
}
export function emptyReportContent(binding) {
  return { schema_version: 1, summary: { narrative: "", completed_count: null, accepted_work_items: null, uncompleted_count: null, cost_micros: null, kr_progress: [] },
    completed: [], uncompleted: [], retrospective: { went_well: [], problems: [], root_causes: [], improvements: [], origin: "MANUAL", confirmation: null, ai_drafted: false }, next_plan: reportNextPlanItems(binding) };
}
export const reportDetailFingerprint = item => JSON.stringify([item.key, item.title, item.plan_item_id || "", item.work_item_id || "", item.reason, item.detail, item.carry_over]);
export const reportRetroFingerprint = retro => JSON.stringify([retro.went_well, retro.problems, retro.root_causes, retro.improvements]);
// Normalize storage shape only. Incomplete drafts remain editable; semantic
// acceptance checks run separately. Never synthesize facts from narrative text.
export function normalizeReportContent(value, binding, previous, review = {}, actor) {
  if (!object(value) || (value.schema_version != null && value.schema_version !== 1)) throw Error("结构化报告版本无效，请使用四章节编辑器。");
  for (const key of ["completed", "uncompleted", "next_plan"]) if (!Array.isArray(value[key])) throw Error(`结构化报告缺少 ${key} 章节。`);
  if (!object(value.summary) || !object(value.retrospective)) throw Error("结构化报告缺少统计或复盘章节。");
  const text = (v, label, max) => { if (typeof v !== "string" || v.length > max) throw Error(`${label}须为文本且不超过 ${max} 字。`); return v; };
  const list = (v, label) => { if (!Array.isArray(v) || v.length > 500) throw Error(`${label}须为列表，最多 500 项。`); return v; };
  const keys = (items, label) => { const seen = new Set(); for (const item of list(items, label)) {
    if (!object(item) || typeof item.key !== "string" || !item.key.trim() || item.key.length > 64 || seen.has(item.key)) throw Error(`${label}条目编号须唯一且不超过 64 字。`); seen.add(item.key);
  } };
  keys(value.completed, "完成章节"); keys(value.uncompleted, "未完成章节");
  const identity = (v, label) => text(v ?? "", label, 200);
  const completed = value.completed.map(item => {
    if (!object(item.fact_ref)) throw Error("完成条目必须保留类型化事实引用。");
    const fact = item.fact_ref;
    if (fact.version != null && (!Number.isSafeInteger(fact.version) || fact.version < 1)) throw Error("事实版本须为正整数。");
    if (!["WORK_ITEM", "DECISION", "DELIVERY", "ACCEPTANCE", "FREE_TASK_EVIDENCE", "CHILD_REPORT"].includes(fact.kind)) throw Error("完成事实类型无效。");
    return { key: item.key, title: text(item.title, "完成标题", 200), goal_id: identity(item.goal_id, "目标编号"), kr_id: identity(item.kr_id, "KR编号"), plan_item_id: identity(item.plan_item_id, "计划项编号"),
      fact_ref: { kind: fact.kind, id: identity(fact.id, "事实编号"), event_id: identity(fact.event_id, "事件编号"), version: fact.version ?? null }, note: text(item.note ?? "", "完成说明", 2000) };
  });
  const acknowledgment = (fingerprint, old, requested) => {
    if (old?.fingerprint === fingerprint && old.actorId === actor?.id && old.actorKind === actor?.kind) return clone(old);
    return requested && actor ? { fingerprint, actorId: actor.id, actorKind: actor.kind, recordedBy: "H-lin", time: new Date().toISOString() } : null;
  };
  const uncompleted = value.uncompleted.map(item => {
    if (item.reason && !Object.hasOwn(reportReasons, item.reason)) throw Error("未完成原因类型无效。");
    if (item.carry_over && !Object.hasOwn(reportDispositions, item.carry_over)) throw Error("未完成处置类型无效。");
    const normalized = { key: item.key, title: text(item.title, "未完成标题", 200), plan_item_id: identity(item.plan_item_id, "计划项编号"), work_item_id: identity(item.work_item_id, "工作编号"),
      reason: item.reason || "", detail: text(item.detail ?? "", "未完成原因说明", 2000), carry_over: item.carry_over || "" };
    normalized.detail_confirmation = acknowledgment(reportDetailFingerprint(normalized), item.detail_confirmation === null ? null : previous?.uncompleted?.find(old => old.key === item.key)?.detail_confirmation, review.detailKeys?.includes(item.key));
    return normalized;
  });
  const retro = value.retrospective, retrospective = {};
  for (const key of ["went_well", "problems", "root_causes"]) retrospective[key] = list(retro[key], "复盘条目").map(entry => text(entry, "复盘条目", 500));
  retrospective.improvements = list(retro.improvements, "改进建议").map(entry => {
    if (!object(entry)) throw Error("改进建议必须为独立条目。");
    return { text: text(entry.text, "改进建议", 500), propose_memory: entry.propose_memory === true };
  });
  // Existing AI provenance cannot be erased by submitting ai_drafted=false.
  retrospective.origin = previous?.retrospective?.origin === "AI" || previous?.retrospective?.ai_drafted === true || retro.ai_drafted === true ? "AI" : "MANUAL";
  retrospective.confirmation = acknowledgment(reportRetroFingerprint(retrospective), retro.confirmation === null ? null : previous?.retrospective?.confirmation, review.retrospective === true);
  retrospective.ai_drafted = retrospective.origin === "AI" && !retrospective.confirmation;
  const summary = { narrative: text(value.summary.narrative ?? "", "统计说明", 2000) };
  for (const key of ["completed_count", "accepted_work_items", "uncompleted_count", "cost_micros"]) {
    const number = value.summary[key] ?? null;
    if (number !== null && (!Number.isSafeInteger(number) || number < 0)) throw Error("报告统计须为非负安全整数，缺少事实时保持未知。");
    summary[key] = number;
  }
  summary.kr_progress = clone(list(value.summary.kr_progress ?? [], "KR观察"));
  if (summary.kr_progress.length) throw Error("尚无固定期初/期末KR观察读取器，不能写入未经证实的KR变化。");
  const next_plan = reportNextPlanItems(binding);
  if (JSON.stringify(value.next_plan) !== JSON.stringify(next_plan)) throw Error("下期计划章节须来自已采用的真实计划固定版本，不可另写一套清单。");
  return { schema_version: 1, summary, completed, uncompleted, retrospective, next_plan };
}
export const reportFactKind = fact => fact.kind || "WORK_ITEM";
export function reportFactReference(fact) {
  const kind = reportFactKind(fact);
  return { kind, id: kind === "FREE_TASK_EVIDENCE" ? fact.itemId : kind === "DECISION" ? fact.id : kind === "DELIVERY" ? fact.deliveryId : fact.workId,
    event_id: fact.id, version: kind === "FREE_TASK_EVIDENCE" ? null : fact.version };
}
export function reportFactTitle(fact, planned) {
  const kind = reportFactKind(fact);
  if (kind === "FREE_TASK_EVIDENCE") return planned?.title || "非交付任务完成证据";
  if (kind === "DECISION") return `${({ APPROVE: "接受成果", REQUEST_CHANGES: "退回修订", CANCEL: "取消工作", PAUSE: "Human 接管", RESUME: "继续执行", DELIVER: "交付决定" })[fact.action]} · ${fact.workSnapshot.title}`;
  if (kind === "DELIVERY") return `本地模拟交付 · ${fact.deliverySnapshot.name}`;
  return fact.workSnapshot?.title || `已接受工作 ${fact.workId}`;
}
export function reportOperationFactValid(fact) {
  const string = value => typeof value === "string" && value.trim().length > 0;
  if (!object(fact) || !["DECISION", "DELIVERY"].includes(fact.kind) ||
    ![fact.id, fact.tenantId, fact.project, fact.workId].every(string) ||
    !Number.isSafeInteger(fact.version) || fact.version < 1 || !object(fact.actor) ||
    !["Human", "Agent"].includes(fact.actor.kind) || !string(fact.actor.id) ||
    !string(fact.time) || !/T.*(?:Z|[+-]\d{2}:\d{2})$/.test(fact.time) || !Number.isFinite(Date.parse(fact.time)) ||
    fact.source !== "LOCAL_SIMULATION" || !Array.isArray(fact.subjectKeys) || fact.subjectKeys.some(key => !string(key)) ||
    !fact.subjectKeys.includes(`tenant:${fact.tenantId}`) || !fact.subjectKeys.includes(`project:${fact.project}`) ||
    !fact.subjectKeys.includes(`${fact.actor.kind === "Human" ? "human" : "agent"}:${fact.actor.id}`)) return false;
  const snapshot = fact.kind === "DECISION" ? fact.workSnapshot : fact.deliverySnapshot;
  if (!object(snapshot) || snapshot.version !== fact.version || snapshot.project !== fact.project) return false;
  if (fact.kind === "DECISION") {
    const actions = { accept: "APPROVE", return: "REQUEST_CHANGES", cancel: "CANCEL", takeover: "PAUSE", resume: "RESUME", deliver: "DELIVER" };
    return Object.hasOwn(actions, fact.originalCommand) && actions[fact.originalCommand] === fact.action && snapshot.id === fact.workId &&
      string(snapshot.title) && typeof fact.reason === "string" && string(fact.fromState) && string(fact.toState);
  }
  return fact.status === "SENT" && string(fact.deliveryId) && snapshot.id === fact.deliveryId && snapshot.workId === fact.workId &&
    string(snapshot.name) && string(snapshot.recipient) && string(snapshot.content);
}
function reportOperationFacts(state, report, frozen, now) {
  let entries;
  if (frozen) {
    const fixed = report.submission.factSnapshot.structuredFacts;
    if (fixed == null && report.content?.completed?.some(item => ["DECISION", "DELIVERY"].includes(item.fact_ref?.kind)))
      throw Error("固定报告裁决/交付证据快照缺失，不能使用当前来源补造历史。");
    if (fixed != null && (!Array.isArray(fixed) || fixed.some(fact => !object(fact))))
      throw Error("固定报告结构化证据快照损坏，不能使用当前来源补造历史。");
    entries = (fixed || []).filter(fact => ["DECISION", "DELIVERY"].includes(fact.kind));
  } else {
    // A known different operation actor is unrelated to this member report.
    // Unknown scope remains an error; do not infer it from the current owner.
    const relevant = fact => {
      if (typeof fact?.tenantId === "string" && fact.tenantId !== state.activeTenant) return false;
      if (/^(human|agent):/.test(report.subjectKey || "") && object(fact?.actor) &&
        ["Human", "Agent"].includes(fact.actor.kind) && typeof fact.actor.id === "string" && fact.actor.id.trim())
        return report.subjectKey === `${fact.actor.kind === "Human" ? "human" : "agent"}:${fact.actor.id}`;
      return true;
    };
    entries = state.work.flatMap(work => {
      if (work.decisionFacts == null) return [];
      if (!Array.isArray(work.decisionFacts)) throw Error("工作裁决历史损坏，不能认证报告完成事实。");
      const selected = work.decisionFacts.filter(relevant);
      if (selected.some(fact => fact?.kind !== "DECISION" || fact.workId !== work.id)) throw Error("工作裁决历史损坏，不能认证报告完成事实。");
      return selected;
    });
    for (const delivery of state.resources.deliveries) {
      if (delivery.sendFact == null || !relevant(delivery.sendFact)) continue;
      if (delivery.sendFact.kind !== "DELIVERY" || !reportOperationFactValid(delivery.sendFact)) throw Error("交付发送证据损坏，不能从负责人或当前状态补造发送事实。");
      if (delivery.id === delivery.sendFact.deliveryId && ["待回执", "已接受"].includes(delivery.status)) entries.push(delivery.sendFact);
    }
  }
  if (entries.some(fact => !reportOperationFactValid(fact))) throw Error("裁决/交付原始事实损坏，不能认证报告完成事实。");
  const cutoff = frozen ? Date.parse(report.submission.time) : now.getTime();
  if (entries.length && !Number.isFinite(cutoff)) throw Error("报告缺少有效截止时间，不能核对裁决/交付历史证据。");
  return entries.filter(fact => {
    if (fact.tenantId !== state.activeTenant || !fact.subjectKeys.includes(report.subjectKey) || Date.parse(fact.time) > cutoff) return false;
    if (/^(human|agent):/.test(report.subjectKey || "") && report.subjectKey !== `${fact.actor.kind === "Human" ? "human" : "agent"}:${fact.actor.id}`) return false;
    const day = dateInZone(report.timezone, new Date(fact.time));
    return day >= report.periodStart && day <= report.periodEnd;
  });
}
export function reportContentFacts(state, report, now = new Date()) {
  const frozen = fixedReportSources(report);
  const facts = frozen ? frozen.filter(source => source.kind === "work").flatMap(source => source.acceptanceFacts || []).filter(fact => reportAcceptanceMatch(report, fact) === true)
    : periodicReportFacts(state, report, now).facts;
  const binding = report.currentPlanBinding;
  let freeFacts = [];
  if (binding?.confirmed && !reportCurrentPlanBindingProblem(state, report, binding)) {
    let entries;
    if (frozen) {
      entries = report.submission.factSnapshot.structuredFacts;
      if (entries == null && report.content?.completed?.some(item => item.fact_ref?.kind === "FREE_TASK_EVIDENCE"))
        throw Error("固定报告非交付任务证据快照缺失，不能使用当前计划补造历史。");
      if (entries != null && (!Array.isArray(entries) || entries.some(entry => !object(entry))))
        throw Error("固定报告结构化证据快照损坏，不能使用当前计划补造历史。");
      entries = (entries || []).filter(entry => entry.kind === "FREE_TASK_EVIDENCE");
    } else {
      const plan = state.resources.plans.find(plan => plan.id === binding.planId && plan.tenantId === binding.tenantId);
      entries = plan?.freeTaskFacts ?? [];
    }
    if (!Array.isArray(entries) || entries.some(entry => !planFreeTaskFactValid(entry)))
      throw Error("非交付任务完成证据损坏，请核对原始记录；不会补造主体或完成事实。");
    const cutoff = frozen ? Date.parse(report.submission.time) : now.getTime();
    if (entries.length && !Number.isFinite(cutoff)) throw Error("固定报告缺少有效提交时间，不能核对非交付任务历史证据。");
    const snapshot = binding.snapshot;
    freeFacts = entries.filter(entry => {
      const item = snapshot.items.find(item => item.kind === "FREE_TASK" && item.id === entry.itemId);
      if (!item || entry.subjectKey !== report.subjectKey || entry.itemDefinition !== JSON.stringify(planCommitment([item])[0]) || Date.parse(entry.time) > cutoff) return false;
      const planDay = dateInZone(snapshot.timezone, new Date(entry.time)), reportDay = dateInZone(report.timezone, new Date(entry.time));
      return planDay >= snapshot.periodStart && planDay <= snapshot.periodEnd && reportDay >= report.periodStart && reportDay <= report.periodEnd;
    });
  }
  return [...new Map([...facts, ...freeFacts, ...reportOperationFacts(state, report, frozen, now)].map(fact => [JSON.stringify([reportFactKind(fact), fact.id]), clone(fact)])).values()];
}
// A new draft can project verified sources. Regenerating an existing draft only
// refreshes this proposal; author text and commitments require explicit adoption.
export function reportGeneratedContent(state, report, now = new Date(), { includeNextPlan = false } = {}) {
  const content = emptyReportContent(), issues = [];
  let binding = null, plan;
  if (/^(human|agent):[^:]+$/.test(report.subjectKey || "")) {
    try {
      plan = reportCurrentPlan(state, report);
      if (plan?.confirmedSnapshot) binding = reportCurrentPlanBinding(state, report, plan.id, plan.revision);
      else issues.push("本期没有已确认计划基线，未补造承诺或未完成清单。");
    } catch (error) { issues.push(error.message); }
  }
  let facts;
  try { facts = reportContentFacts(state, { ...report, currentPlanBinding: binding, submission: null }, now); }
  catch (error) { issues.push(error.message); return { content, currentPlanBinding: binding, facts: [], issues }; }
  facts = facts.filter(fact => {
    const valid = typeof fact.id === "string" && fact.id.trim() && (fact.kind === "FREE_TASK_EVIDENCE" ||
      (typeof fact.workId === "string" && fact.workId.trim() && Number.isSafeInteger(fact.version) && fact.version > 0));
    if (!valid) issues.push("接受事件缺少有效身份或固定版本，未作为完成条目生成。");
    return valid;
  });
  const items = binding?.snapshot.items || [], linked = new Set();
  // Latest eligible acceptance gets the sole Plan-item association. All other
  // genuine events retain their own fixed version and stay explicitly off-plan.
  facts.sort((a, b) => Date.parse(b.time) - Date.parse(a.time) || a.id.localeCompare(b.id));
  content.completed = facts.map((fact, index) => {
    const free = fact.kind === "FREE_TASK_EVIDENCE";
    const candidates = items.filter(item => free ? item.kind === "FREE_TASK" && item.id === fact.itemId : reportFactKind(fact) === "WORK_ITEM" && item.kind === "WORK_ITEM" && item.workId === fact.workId);
    const item = candidates.length === 1 && !linked.has(candidates[0].id) ? candidates[0] : null;
    if (item) linked.add(item.id);
    return { key: `generated-completed-${index + 1}`, title: reportFactTitle(fact, item),
      goal_id: item?.goalId || "", kr_id: item?.krId || "", plan_item_id: item?.id || "",
      fact_ref: reportFactReference(fact), note: "" };
  });
  const outcomes = binding ? planReportFacts(state, { ...plan, confirmedSnapshot: binding.snapshot }, now, { historical: true }).outcomes : [];
  const currentPeriod = dateInZone(report.timezone, now) >= report.periodStart && dateInZone(report.timezone, now) <= report.periodEnd;
  content.uncompleted = items.filter(item => !linked.has(item.id)).map((item, index) => {
    const outcome = outcomes.find(outcome => outcome.itemId === item.id), work = item.kind === "WORK_ITEM" && state.work.find(work => work.id === item.workId);
    let reason = "OTHER", detail = "本期尚无可核对的完成证据；请作者核对原因与处置。";
    if (outcome?.status === "未知" || outcome?.status === "完成") detail = "完成证据未知或不满足报告主体及周期；请核对原始来源，不视为已完成。";
    else if (currentPeriod && work?.rework?.hold && ["formal", "internal"].includes(work.rework.hold.kind)) { reason = "QUALITY_REWORK"; detail = "当前工作因质量返工停线，原因与后续处置须作者核对。"; }
    else if (currentPeriod && work?.rework?.hold) detail = "当前工作已停线，停线依据与后续处置须作者核对，不推断为依赖或质量原因。";
    else if (currentPeriod && work?.status === "等待") { reason = "BLOCKED_DEPENDENCY"; detail = "当前工作处于等待 / 阻塞状态；具体依赖与原因须作者核对。"; }
    else if (currentPeriod && work && qualityFromPlans(state, work) === "通过" && !work.accepted) detail = "已验证，待裁决；尚无本周期接受事实，请作者核对。";
    return { key: `generated-uncompleted-${index + 1}`, title: item.title, plan_item_id: item.id, work_item_id: item.workId || "", reason, detail, carry_over: "CARRY_TO_NEXT", detail_confirmation: null };
  });
  if (content.completed.length > 500 || content.uncompleted.length > 500) {
    issues.push("来源条目超过报告章节可编辑上限，未截断或伪造汇总；请核对报告范围。");
    return { content: emptyReportContent(), currentPlanBinding: binding, facts: [], issues };
  }
  if (content.completed.length || content.uncompleted.length) {
    content.summary.narrative = "系统按可核对来源预填本期条目；覆盖已接入的工作接受、非交付任务及本地模拟裁决/交付证据，原因与复盘仍需作者核对。";
    content.summary.completed_count = content.completed.length;
    content.summary.accepted_work_items = new Set(content.completed.filter(item => item.fact_ref.kind === "WORK_ITEM").map(item => item.fact_ref.id)).size;
    if (binding) content.summary.uncompleted_count = content.uncompleted.length;
  }
  let nextPlanBinding = null;
  if (includeNextPlan && /^(human|agent):[^:]+$/.test(report.subjectKey || "")) {
    try {
      const nextPlan = reportNextPlan(state, report);
      if (nextPlan) {
        nextPlanBinding = reportNextPlanBinding(state, report, nextPlan.id, nextPlan.revision);
        content.next_plan = reportNextPlanItems(nextPlanBinding);
      }
    } catch (error) { issues.push(`下期计划无法采用：${error.message}`); }
  }
  try { normalizeReportContent(content, nextPlanBinding, content); }
  catch (error) { issues.push(`来源不能生成有效章节：${error.message}`); return { content: emptyReportContent(), currentPlanBinding: binding, facts: [], issues }; }
  return { content, currentPlanBinding: binding, nextPlanBinding, facts: clone(facts), issues };
}
export function adoptReportGeneratedContent(state, form) {
  const stored = state.resources.reports.find(report => report.id === form.id);
  if (!stored || stored.revision !== form.revision) throw Error("报告已变化，请重新打开并刷新生成建议。");
  if (!form.content || !stored.suggestedContent) throw Error("请先显式建立四章节草稿并刷新生成建议，旧正文不会自动迁移。");
  const proposal = reportGeneratedContent(state, form);
  if (JSON.stringify(stored.suggestedContent) !== JSON.stringify(proposal.content) || JSON.stringify(stored.suggestedCurrentPlanBinding) !== JSON.stringify(proposal.currentPlanBinding) || JSON.stringify(stored.suggestedContentFacts) !== JSON.stringify(proposal.facts))
    throw Error("生成建议或来源已变化，请重新生成 / 打开本周期报告后核对；原草稿保留。");
  const actor = { id: form.authorId || form.subjectKey?.split(":")[1], kind: form.authorKind || (form.subjectKey?.startsWith("agent:") ? "Agent" : "Human") };
  const content = normalizeReportContent(form.content, form.nextPlanBinding, form.content, {}, actor);
  const binding = proposal.currentPlanBinding;
  if (form.currentPlanBinding && JSON.stringify(form.currentPlanBinding.snapshot) !== JSON.stringify(binding?.snapshot) && [...content.completed, ...content.uncompleted].some(item => item.plan_item_id))
    throw Error("现有条目引用不同的固定计划基线，请先核对原计划关联；不会改写承诺或说明。");
  const next = clone(content), refs = clone(form.references || []);
  const identity = item => JSON.stringify([item.fact_ref.kind, item.fact_ref.id, item.fact_ref.event_id, item.fact_ref.version]);
  const key = (section, original) => {
    let candidate = original, index = 1;
    while (next[section].some(item => item.key === candidate)) candidate = `adopted-${section}-${index++}`;
    return candidate;
  };
  for (const item of proposal.content.completed) {
    if (next.uncompleted.some(old => (item.plan_item_id && old.plan_item_id === item.plan_item_id) ||
      (item.fact_ref.kind === "WORK_ITEM" && old.work_item_id === item.fact_ref.id)))
      throw Error(`${item.title}：原未完成条目已有完成证据，请先核对并处理原条目；说明与确认不会被静默删除。`);
    if (next.completed.some(old => identity(old) === identity(item))) continue;
    if (item.plan_item_id && next.completed.some(old => old.plan_item_id === item.plan_item_id))
      throw Error(`${item.title}：计划项已关联另一完成事件，请先核对关联，不能重复采用。`);
    next.completed.push({ ...clone(item), key: key("completed", item.key) });
    if (item.fact_ref.kind === "WORK_ITEM") {
      const ref = refs.find(ref => ref.kind === "work" && ref.id === item.fact_ref.id && ref.version === item.fact_ref.version);
      if (ref) ref.acceptanceFactIds = [...new Set([...(ref.acceptanceFactIds || []), item.fact_ref.event_id])];
      else refs.push({ kind: "work", id: item.fact_ref.id, version: item.fact_ref.version, acceptanceFactIds: [item.fact_ref.event_id] });
    }
  }
  for (const item of proposal.content.uncompleted) {
    if (next.uncompleted.some(old => old.plan_item_id === item.plan_item_id)) continue;
    if (next.completed.some(old => old.plan_item_id === item.plan_item_id || (item.work_item_id && old.fact_ref.kind === "WORK_ITEM" && old.fact_ref.id === item.work_item_id))) throw Error(`${item.title}：所选完成事实与最新计划结果冲突，请核对来源；原记录保留。`);
    next.uncompleted.push({ ...clone(item), key: key("uncompleted", item.key) });
  }
  next.summary.completed_count = next.completed.length;
  next.summary.accepted_work_items = new Set(next.completed.filter(item => item.fact_ref.kind === "WORK_ITEM").map(item => item.fact_ref.id)).size;
  next.summary.uncompleted_count = next.uncompleted.length;
  if (!next.summary.narrative) next.summary.narrative = proposal.content.summary.narrative;
  // Validate limits and shape before replacing any form field. Adoption does not
  // confirm reasons or retrospective text, and it never writes to storage.
  normalizeReportContent(next, form.nextPlanBinding, content, {}, actor);
  return { ...form, content: next, references: refs, ...(binding ? { currentPlanBinding: form.currentPlanBinding && JSON.stringify(form.currentPlanBinding.snapshot) === JSON.stringify(binding.snapshot) ? form.currentPlanBinding : binding } : {}) };
}
// Explicit author-selected chapter refill. This is separate from additive
// suggestions: callers must authorize and save using the report revision.
export function prepareReportRegeneration(state, report, sections = ["completed", "uncompleted", "next_plan"]) {
  if (!Array.isArray(sections) || !sections.length || new Set(sections).size !== sections.length ||
    sections.some(section => !["completed", "uncompleted", "next_plan"].includes(section)))
    throw Error("请选择有效且不重复的报告重填章节。");
  if (!report.content) throw Error("请先显式建立四章节草稿，旧正文不会自动迁移。");
  const actor = { id: report.authorId || report.subjectKey?.split(":")[1], kind: report.authorKind || (report.subjectKey?.startsWith("agent:") ? "Agent" : "Human") };
  const content = normalizeReportContent(report.content, report.nextPlanBinding, report.content, {}, actor);
  const next = clone(content);
  let currentPlanBinding = report.currentPlanBinding, nextPlanBinding = report.nextPlanBinding;
  if (sections.includes("completed") || sections.includes("uncompleted")) {
    const proposal = reportGeneratedContent(state, report);
    const blockingIssues = proposal.issues.filter(issue => !(issue === "本期没有已确认计划基线，未补造承诺或未完成清单。" &&
      !sections.includes("uncompleted") && !report.currentPlanBinding));
    if (blockingIssues.length) throw Error(`本期来源无法完整重填：${blockingIssues.join("；")}`);
    if (JSON.stringify(currentPlanBinding?.snapshot) !== JSON.stringify(proposal.currentPlanBinding?.snapshot) &&
      (!sections.includes("completed") && content.completed.some(item => item.plan_item_id) ||
       !sections.includes("uncompleted") && content.uncompleted.some(item => item.plan_item_id)))
      throw Error("本期计划基线已变化，请同时选择完成与未完成章节；不能改写未选章节的固定关联。");
    currentPlanBinding = proposal.currentPlanBinding || currentPlanBinding;
    for (const section of ["completed", "uncompleted"]) if (sections.includes(section)) next[section] = clone(proposal.content[section]);
    if (next.completed.some(done => next.uncompleted.some(pending =>
      (done.plan_item_id && done.plan_item_id === pending.plan_item_id) ||
      (done.fact_ref.kind === "WORK_ITEM" && done.fact_ref.id === pending.work_item_id))))
      throw Error("重填内容与未选章节的完成 / 未完成记录冲突，请同时选择相关章节核对。");
    next.summary.completed_count = next.completed.length;
    next.summary.accepted_work_items = new Set(next.completed.filter(item => item.fact_ref.kind === "WORK_ITEM").map(item => item.fact_ref.id)).size;
    next.summary.uncompleted_count = currentPlanBinding ? next.uncompleted.length : null;
  }
  if (sections.includes("next_plan")) {
    const plan = reportNextPlan(state, report);
    if (!plan) throw Error("尚无真实下期计划，请先建立计划草案；不会编造下一周期承诺。");
    nextPlanBinding = reportNextPlanBinding(state, report, plan.id, plan.revision);
    next.next_plan = reportNextPlanItems(nextPlanBinding);
  }
  // New reasons are unconfirmed. Retrospective and unselected chapters retain
  // their exact author confirmations; no regeneration acts as acceptance.
  normalizeReportContent(next, nextPlanBinding, content, {}, actor);
  let references = clone(report.references || []);
  if (sections.includes("completed")) {
    const oldWorkKeys = new Set(content.completed.filter(item => item.fact_ref.kind === "WORK_ITEM").map(item => JSON.stringify([item.fact_ref.id, item.fact_ref.version])));
    references = references.filter(ref => ref.kind !== "work" || !oldWorkKeys.has(JSON.stringify([ref.id, ref.version])));
    for (const item of next.completed.filter(item => item.fact_ref.kind === "WORK_ITEM")) {
      const existing = references.find(ref => ref.kind === "work" && ref.id === item.fact_ref.id && ref.version === item.fact_ref.version);
      if (existing) existing.acceptanceFactIds = [...new Set([...(existing.acceptanceFactIds || []), item.fact_ref.event_id])];
      else references.push({ kind: "work", id: item.fact_ref.id, version: item.fact_ref.version, acceptanceFactIds: [item.fact_ref.event_id] });
    }
  }
  return { ...clone(report), content: next, references, currentPlanBinding, nextPlanBinding,
    ...(sections.includes("uncompleted") ? { reviewedDetailKeys: [] } : {}),
    regenerationDraft: {
      sections: [...new Set([...(report.regenerationDraft?.sections || []), ...sections])],
      sources: { ...clone(report.regenerationDraft?.sources || {}), ...reportRegenerationSources(state, report, sections) },
    } };
}
export function reportRegenerationSources(state, report, sections) {
  const sources = {};
  if (sections.includes("completed") || sections.includes("uncompleted")) {
    const proposal = reportGeneratedContent(state, report);
    for (const section of ["completed", "uncompleted"]) if (sections.includes(section))
      sources[section] = { content: proposal.content[section], facts: proposal.facts, binding: proposal.currentPlanBinding, issues: proposal.issues };
  }
  if (sections.includes("next_plan")) {
    const plan = reportNextPlan(state, report);
    if (!plan) throw Error("尚无真实下期计划，请先建立计划草案；不会编造下一周期承诺。");
    sources.next_plan = reportNextPlanBinding(state, report, plan.id, plan.revision);
  }
  return sources;
}
export function fixedReportSources(report) {
  const snapshot = report.submission?.factSnapshot;
  if (!snapshot) {
    if (report.submission && (report.content != null || report.submission.content?.content != null)) throw Error("固定报告来源快照缺失，不能使用当前来源补造历史，请更正固定证据。");
    return null;
  }
  if (!Array.isArray(snapshot.sources) || snapshot.sources.some(source => !object(source) ||
    (source.acceptanceFacts != null && (!Array.isArray(source.acceptanceFacts) || source.acceptanceFacts.some(fact => !object(fact))))))
    throw Error("固定报告来源快照损坏，不能使用当前来源补造历史，请更正固定证据。");
  return snapshot.sources;
}
export function reportContentCheck(state, report) {
  const issues = []; let content = report.content;
  const add = (code, section, message, itemKey) => issues.push({ code, section, message, itemKey });
  if (content == null) return report.contentRequired === true
    ? { issues: [{ code: "B4", section: "SUMMARY", message: "此报告必须保留结构化四章节，请恢复章节草稿后核对。" }], warnings: [], facts: [] }
    : { issues, warnings: ["旧报告尚无结构化四章节；本次来源核对不代表报告结构验收通过。"], facts: [] };
  if (content.schema_version !== 1 || !object(content.summary) || !object(content.retrospective) || ["completed", "uncompleted", "next_plan"].some(key => !Array.isArray(content[key])))
    return { issues: [{ code: "B4", section: "SUMMARY", message: "报告缺少结构化四章节。" }], warnings: [], facts: [] };
  const actor = { id: report.authorId || report.subjectKey?.split(":")[1], kind: report.authorKind || (report.subjectKey?.startsWith("agent:") ? "Agent" : "Human") };
  try { content = normalizeReportContent(content, report.nextPlanBinding, content, {}, actor); }
  catch (error) { return { issues: [{ code: "B4", section: "SUMMARY", message: error.message }], warnings: [], facts: [] }; }
  let facts;
  try { facts = reportContentFacts(state, report); }
  catch (error) { return { issues: [{ code: "B1", section: "COMPLETED", message: error.message }], warnings: [], facts: [] }; }
  const selected = [], seen = new Set(), workIds = new Set();
  const referencedPlanItems = new Set();
  const bindingProblem = report.currentPlanBinding && reportCurrentPlanBindingProblem(state, report, report.currentPlanBinding);
  const planReference = (item, section) => {
    if (!item.plan_item_id) return;
    if (!report.currentPlanBinding || bindingProblem) {
      add("B1", section, `${item.title}：计划项引用缺少可核对的固定本期计划，请明确采用真实计划基线。`, item.key); return;
    }
    const planned = report.currentPlanBinding.snapshot.items.find(candidate => candidate.id === item.plan_item_id);
    if (!planned) { add("B1", section, `${item.title}：计划项不存在于采用的固定本期计划。`, item.key); return; }
    if (referencedPlanItems.has(planned.id)) add("B2", section, `${item.title}：同一计划项重复列出或同时列为已完成与未完成。`, item.key);
    referencedPlanItems.add(planned.id);
    if (section === "COMPLETED") {
      const compatible = planned.kind === "WORK_ITEM"
        ? item.fact_ref.kind === "WORK_ITEM" && item.fact_ref.id === planned.workId
        : item.fact_ref.kind === "FREE_TASK_EVIDENCE" && item.fact_ref.id === planned.id;
      if (!compatible) add("B1", section, `${item.title}：完成事实类型或工作编号与固定计划项不一致。`, item.key);
      if ((item.goal_id && item.goal_id !== planned.goalId) || (item.kr_id && item.kr_id !== planned.krId))
        add("B5", section, `${item.title}：目标或 KR 与固定计划项不一致。`, item.key);
    } else if ((planned.kind === "WORK_ITEM" && item.work_item_id !== planned.workId) || (planned.kind === "FREE_TASK" && item.work_item_id))
      add("B1", section, `${item.title}：未完成工作编号与固定计划项不一致。`, item.key);
  };

  const contribution = (item, section) => {
    if (!item.goal_id && !item.kr_id) return;
    const goal = effectiveGoal(state.resources.goals.find(goal => goal.id === item.goal_id));
    if (!structuredGoal(goal) || goalReadProblem(state,goal) || goal.status === "已取消") { add("B3", section, `${item.title}：目标不存在或不可用。`, item.key); return; }
    const personal = /^(human|agent):/.test(report.subjectKey || ""), eligible = goalContributors(state, goal.id, item.kr_id || undefined).some(person => `${person.kind === "Agent" ? "agent" : "human"}:${person.id}` === report.subjectKey);
    if ((personal && !eligible) || (item.kr_id && !goal.krs.some(kr => kr.id === item.kr_id))) add("B5", section, `${item.title}：目标/KR不属于报告主体贡献范围。`, item.key);
    if (!personal && goal.subjectKey !== report.subjectKey) add("B5", section, `${item.title}：汇总目标归属未经证实。`, item.key);
  };
  for (const item of content.completed) {
    planReference(item, "COMPLETED");
    if (!item.title?.trim()) add("B4", "COMPLETED", "完成条目缺少标题。", item.key);
    const ref = item.fact_ref;
    if (!["WORK_ITEM", "FREE_TASK_EVIDENCE", "DECISION", "DELIVERY"].includes(ref?.kind)) { add("B1", "COMPLETED", `${item.title}：${ref?.kind || "未知"}事实读取器尚未提供，不能认证完成。`, item.key); continue; }
    const free = ref.kind === "FREE_TASK_EVIDENCE", operation = ["DECISION", "DELIVERY"].includes(ref.kind);
    const fact = facts.find(fact => JSON.stringify(reportFactReference(fact)) === JSON.stringify(ref));
    const selectedRef = free ? item.plan_item_id === ref.id && report.currentPlanBinding?.confirmed === true && !bindingProblem
      : operation || Array.isArray(report.references) && report.references.some(source => source?.kind === "work" && source.id === ref.id && source.version === ref.version && (!source.acceptanceFactIds?.length || source.acceptanceFactIds.includes(ref.event_id)));
    if (!fact || !selectedRef) { add("B1", "COMPLETED", `${item.title}：缺少匹配主体、周期、固定版本及对应执行者/操作者的已选事实。`, item.key); continue; }
    const identity = JSON.stringify([ref.kind, fact.id]);
    if (seen.has(identity)) add("B2", "COMPLETED", `${item.title}：同一完成事件不能重复列为完成。`, item.key);
    else { seen.add(identity); selected.push(clone(fact)); if (ref.kind === "WORK_ITEM") workIds.add(fact.workId); }
    if (!report.submission) contribution(item, "COMPLETED");
  }
  const summary = content.summary;
  if (!content.completed.length && (!summary.narrative?.trim() || (!content.uncompleted.length && !content.retrospective.problems?.some(text => text.trim())))) add("B4", "COMPLETED", "没有完成事实时须说明原因，并记录未完成项或复盘问题。");
  for (const [key, expected] of [["completed_count", seen.size], ["accepted_work_items", workIds.size], ["uncompleted_count", content.uncompleted.length]])
    if (summary[key] != null && summary[key] !== expected) add("B2", "SUMMARY", `${key} 与结构化条目事实数不一致。`);
  if (summary.cost_micros != null) add("B2", "SUMMARY", "执行成本读取器尚未提供，成本须保持未知，不能认证为零。");
  if (!content.uncompleted.length) add("B4", "UNCOMPLETED", "未完成章节为空；明确无事项的声明规则仍待确认，不要创建虚构任务。");
  for (const item of content.uncompleted) {
    planReference(item, "UNCOMPLETED");
    if (!item.title?.trim() || !Object.hasOwn(reportReasons, item.reason) || !Object.hasOwn(reportDispositions, item.carry_over) || item.detail?.trim().length < 4) add("B4", "UNCOMPLETED", `${item.title || "未完成项"}：填写原因、至少四字说明和处置。`, item.key);
    const authorId = report.authorId || report.subjectKey?.split(":")[1], authorKind = report.authorKind || (report.subjectKey?.startsWith("agent:") ? "Agent" : "Human");
    if (!item.detail_confirmation || item.detail_confirmation.fingerprint !== reportDetailFingerprint(item) || item.detail_confirmation.actorId !== authorId || item.detail_confirmation.actorKind !== authorKind) add("B4", "UNCOMPLETED", `${item.title || "未完成项"}：作者尚未确认当前原因和处置。`, item.key);
    if (item.work_item_id && workIds.has(item.work_item_id)) add("B2", "UNCOMPLETED", `${item.title}：同一工作同时列为已完成与未完成，须解释不同版本或拆分。`, item.key);
  }
  const retro = content.retrospective;
  for (const key of ["went_well", "problems", "root_causes", "improvements"]) if (!Array.isArray(retro[key]) || !retro[key].length || retro[key].some(entry => !(typeof entry === "string" ? entry : entry.text)?.trim())) add("B4", "RETRO", `复盘 ${key} 须有明确内容。`);
  if (retro.ai_drafted && (!retro.confirmation || retro.confirmation.fingerprint !== reportRetroFingerprint(retro))) add("B4", "RETRO", "AI/模板复盘须由作者确认当前内容。");
  if (!report.nextPlanBinding || !content.next_plan.length) add("B3", "NEXT_PLAN", "须采用真实下周期计划；明确无下期承诺的声明规则仍待确认。");
  else if (JSON.stringify(content.next_plan) !== JSON.stringify(reportNextPlanItems(report.nextPlanBinding))) add("B3", "NEXT_PLAN", "下期计划与固定计划版本不一致。");
  const warnings = [];
  if (report.submission) for (const fact of selected.filter(fact => ["DECISION", "DELIVERY"].includes(fact.kind))) {
    const delivery = fact.kind === "DELIVERY" ? state.resources.deliveries.find(item => item.id === fact.deliveryId) : null;
    const decisions = fact.kind === "DECISION" ? state.work.find(work => work.id === fact.workId)?.decisionFacts : null;
    const current = delivery?.sendFact || (Array.isArray(decisions) ? decisions.find(item => item?.id === fact.id) : null);
    if (JSON.stringify(current) !== JSON.stringify(fact) || (delivery && !["待回执", "已接受"].includes(delivery.status)))
      warnings.push(`N4：${reportFactTitle(fact)} 当前来源已变化或不可用，保留报告固定证据，需重新核对。`);
  }
  return { issues, warnings, facts: selected };
}
