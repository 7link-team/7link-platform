import { workRework } from "./rework";
// QA plans pin definitions; batches collect outcomes; reports retain immutable copies.
export const qaStatuses = ["未执行", "通过", "失败", "跳过", "阻塞"];
const copy = value => structuredClone(value);
const now = () => new Date().toISOString();
export function caseSnapshot(state, test) {
  const { runs, history, currentRun, ...definition } = test;
  return copy({ ...definition, revision: test.revision || 1,
    requirementRefs: test.requirementRefs || [] });
}
export function freezeCases(state, caseIds) {
  if (!Array.isArray(caseIds) || !caseIds.length || new Set(caseIds).size !== caseIds.length) throw Error("请选择不重复的测试用例。");
  return caseIds.map(caseId => {
    const test = state.resources.tests.find(item => item.id === caseId);
    const work = state.work.find(item => item.id === test?.work);
    if (!test || !work || test.version !== `v${work.version}`) throw Error("计划只能固定当前工作版本的有效用例。");
    const snapshot = caseSnapshot(state, test);
    if (!snapshot.requirementRefs.length || snapshot.requirementRefs.some(ref => !work.requirements.includes(ref))) throw Error("用例须关联当前版本的验收条款。");
    return { caseId, caseRevision: snapshot.revision, required: true, snapshot };
  });
}
export function qaCounts(items) {
  return Object.fromEntries(qaStatuses.map(status => [status, items.filter(item => item.status === status).length]));
}
export function activeBatch(plan) {
  return plan.batches?.find(batch => batch.id === plan.currentBatchId && batch.planRevision === (plan.revision || 1));
}
export function unfinishedWorkBatches(state, workId, exceptPlanId) {
  const work = state.work.find(work => work.id === workId);
  return (state.resources.testplans || []).filter(plan => plan.id !== exceptPlanId).flatMap(plan => {
    const batch = activeBatch(plan), context = batch?.workContexts?.find(context => context.workId === workId);
    return batch?.status === "进行中" && context?.version === work?.version && context.chain === workRework(work).chain ? [batch] : [];
  });
}
export function hydrateQA(state) {
  state.resources.testreports ??= [];
  for (const plan of state.resources.testplans || []) {
    plan.revision ??= 1;
    plan.batches ??= [];
    if (plan.caseRefs) continue;
    // Migrate only explicit local case IDs; preserve unknown references for correction.
    const caseIds = String(plan.cases || "").split(/[、,，\s]+/).filter(Boolean);
    plan.caseRefs = caseIds.flatMap(caseId => {
      const test = state.resources.tests.find(item => item.id === caseId);
      return test ? [{ caseId, caseRevision: test.revision || 1, required: true, snapshot: caseSnapshot(state, test) }] : [];
    });
    plan.legacyUnresolvedCases = caseIds.filter(caseId => !state.resources.tests.some(test => test.id === caseId));
    plan.status = "草稿";
    plan.migrationNote = "原计划的用例引用已保留；旧计划没有固定执行批次，请核对后开始。";
  }
}
export function productionSnapshot(work) {
  return { version: work.version, executorIds: [...new Set(work.team?.executorIds || (work.agentId ? [`agent:${work.agentId}`] : []))] };
}
export function assertVerificationMutable(state, workId, version) {
  const work = state.work.find(item => item.id === workId);
  if (work && version === `v${work.version}` && (work.accepted || work.delivered || work.status === "已完成"))
    throw Error("当前版本已接受，请先正式退回并提交新成果版本，再开始验证。");
}
export function startBatch(state, plan, makeId) {
  if (!plan.environment?.trim() || !plan.targetBuild?.trim() || !plan.assignment?.trim() || !plan.windowStart || !plan.windowEnd)
    throw Error("请先修订计划，明确目标构建、环境、分派和执行窗口。");
  if (!plan.caseRefs?.length || plan.legacyUnresolvedCases?.length) throw Error("请先固定完整的用例修订。");
  for (const ref of plan.caseRefs) {
    if (!ref.snapshot.requirementRefs?.length) throw Error("旧用例未关联验收条款，请明确修订后开始。");
    const test = state.resources.tests.find(item => item.id === ref.caseId);
    const work = state.work.find(item => item.id === ref.snapshot.work);
    if (work && workRework(work).hold) throw Error("返工已停线，不能启动新的验证批次。");
    if (work?.status === "已取消") throw Error("已取消的工作不能启动验证批次。");
    assertVerificationMutable(state, ref.snapshot.work, ref.snapshot.version);
    if (work?.status !== "待验收") throw Error("先提交固定版本再进行独立检查。");
    if (unfinishedWorkBatches(state, work.id, plan.id).length) throw Error("该工作还有未结束的验证批次，请先形成报告再采用此计划。");
    if (work.qaPlanId !== plan.id && state.role !== "owner") throw Error("采用新的验收计划需要负责人确认。");
    if (!test || (test.revision || 1) !== ref.caseRevision || ref.snapshot.version !== `v${work?.version}`) throw Error("计划用例或工作已改版，请建立计划新修订并明确重验范围。");
  }
  const existing = activeBatch(plan);
  if (existing?.status === "进行中") return existing;
  const batch = { id: makeId("QB"), planId: plan.id, planRevision: plan.revision || 1,
    workContexts: [...new Set(plan.caseRefs.map(ref => ref.snapshot.work))].map(workId => {
      const work = state.work.find(work => work.id === workId);
      return { workId, version: work.version, chain: workRework(work).chain, production: copy(work.production || null) };
    }),
    environment: plan.environment, targetBuild: plan.targetBuild || "未记录", assignment: plan.assignment || plan.owner,
    windowStart: plan.windowStart || "", windowEnd: plan.windowEnd || "", caseRefs: copy(plan.caseRefs),
    items: plan.caseRefs.map(ref => ({ caseId: ref.caseId, caseRevision: ref.caseRevision, status: "未执行", runId: null })),
    status: "进行中", startedAt: now(), startedBy: "林予" };
  plan.batches ??= [];
  plan.batches.push(batch);
  plan.currentBatchId = batch.id;
  plan.status = "进行中";
  return batch;
}
export function qaIdentity(state, kind, identityId, legacyName) {
  if (!["Human", "Agent"].includes(kind)) return null;
  const people = kind === "Human" ? (state.organization?.members || []).filter(person => person.kind === "Human") : state.agents || [];
  const matches = identityId ? people.filter(person => person.id === identityId) : people.filter(person => person.name === legacyName);
  return matches.length === 1 ? { ownerKind: kind, ownerId: matches[0].id, owner: matches[0].name } : null;
}
export function independentRun(state, work, run) {
  const requiredKind = run?.sourceKind === "Human 手测" ? "Human" : run?.sourceKind === "平台独立验证" ? "Agent" : null;
  if (!requiredKind || run.ownerKind !== requiredKind || !run.ownerId || !qaIdentity(state, requiredKind, run.ownerId)) return false;
  // Historical producer identity belongs to the output version, never the mutable team.
  const production = work.production;
  if (production?.version !== work.version || !production.executorIds?.length) return false;
  const executorIds = production.executorIds;
  const identity = run.ownerKind === "Agent" ? `agent:${run.ownerId}` : run.ownerId;
  return !executorIds.includes(identity);
}
export function qualityFromPlans(state, work) {
  const refs = [];
  for (const plan of (state.resources.testplans || []).filter(plan => plan.id === work.qaPlanId)) {
    const batch = activeBatch(plan);
    for (const ref of plan.caseRefs || []) {
      if (!ref.required || ref.snapshot.work !== work.id || ref.snapshot.version !== `v${work.version}`) continue;
      const test = state.resources.tests.find(item => item.id === ref.caseId);
      const item = batch?.items.find(item => item.caseId === ref.caseId);
      const run = test?.runs?.find(run => run.id === item?.runId);
      const context = batch?.workContexts?.find(context => context.workId === work.id);
      const reportExists = batch?.status === "已提交" && state.resources.testreports?.some(report => report.id === batch.reportId && report.batchId === batch.id);
      const valid = reportExists && context?.version === work.version && context.chain === workRework(work).chain && test && (test.revision || 1) === ref.caseRevision && run?.caseRevision === ref.caseRevision &&
        test.currentRun === run.id && test.status === "通过" && test.evidence?.trim() && run.batchId === batch?.id && run.version === `v${work.version}` &&
        run.requirementRefs?.length && run.evidence?.trim() && independentRun(state, work, run);
      refs.push({ ref, item, valid });
    }
  }
  if (refs.some(({ item }) => item?.status === "失败")) return "失败";
  const currentTests = state.resources.tests.filter(test => test.work === work.id && test.version === `v${work.version}`);
  if (currentTests.some(test => test.status === "失败")) return "失败";
  if (!refs.length) return "未执行";
  if (currentTests.some(test => !refs.some(({ ref }) => ref.caseId === test.id))) return "待验证";
  if (!work.requirements.every(requirement => refs.some(({ ref }) => ref.snapshot.requirementRefs?.includes(requirement)))) return "待验证";
  return refs.every(({ item, valid }) => item?.status === "通过" && valid) ? "通过" : "待验证";
}
