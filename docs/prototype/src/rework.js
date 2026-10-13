// Work-level business events survive executor and workflow reassignment.
const positiveInteger = value => ["number", "string"].includes(typeof value) && Number.isSafeInteger(Number(value)) && Number(value) > 0;
const formalKey = entry => entry.delivery ? `delivery:${entry.delivery}` : `work:v${String(entry.version).replace(/^v/, "")}`;
export function workRework(work) {
  if (work.rework) return work.rework;
  const legacy = [...new Map((work.returnFeedback || []).map(entry => [formalKey(entry), entry])).entries()]
    .map(([key, entry]) => ({ key, kind: "formal", version: entry.version, reason: entry.reason, source: entry.delivery ? "delivery" : "work", legacy: true }));
  const record = { limits: work.type === "软件" ? { internal: 5, formal: 3 } : { internal: null, formal: null },
    internal: 0, internalTotal: 0, formalTotal: legacy.length, chain: 1, events: legacy, decisions: [], revision: 0, hold: null };
  if (legacy.length && !["已完成", "已取消"].includes(work.status)) {
    const unknown = !positiveInteger(record.limits.formal);
    if (unknown || record.formalTotal >= record.limits.formal) record.hold = {
      kind: unknown ? "configuration" : "formal", reason: unknown ? "历史返工的专业包上限未配置" : "历史正式返工累计已达到上限，尚无 Human 续行决定",
      version: work.version, resumeStatus: legacy.some(event => String(event.version).replace(/^v/, "") === String(work.version)) ? "进行中" : work.status, legacy: true,
    };
  }
  return record;
}
export function reworkConfigured(work) {
  const { limits } = workRework(work);
  return positiveInteger(limits.internal) && positiveInteger(limits.formal);
}
export function initializeWorkRework(work) {
  work.rework ??= structuredClone(workRework(work));
  if (work.rework.hold && work.status !== "已取消") work.status = "等待";
  return work.rework;
}
const mutable = initializeWorkRework;
function stamp(actor = "H-lin") { return { actor, time: new Date().toISOString() }; }
function pause(work, kind, reason, resumeStatus) {
  const record = mutable(work);
  record.hold = { kind, reason, resumeStatus, version: work.version, ...stamp() };
  work.status = "等待";
}
export function recordFormalRework(s, work, reason, event) {
  const initialized = Boolean(work.rework), record = mutable(work);
  const actor = s.role === "external" ? "外部接收人" : "H-lin";
  const existing = record.events.some(item => item.kind === "formal" && item.key === event.key);
  if (existing && initialized) {
    if (record.hold) work.status = "等待";
    return;
  }
  if (existing && !initialized) {
    const index = record.events.findIndex(item => item.kind === "formal" && item.key === event.key);
    record.events[index] = { ...event, kind: "formal", reason, ...stamp(actor) };
  }
  if (!existing) {
    record.events.push({ ...event, kind: "formal", reason, ...stamp(actor) });
    record.formalTotal++;
  }
  // BR-02-54: only a formal Human rejection / external reopening starts a new declaration chain.
  record.events.push({ kind: "chain", previousInternal: record.internal, previousChain: record.chain, reason, ...stamp(actor) });
  record.internal = 0; record.chain++; delete record.internalCeiling;
  record.revision++;
  if (!reworkConfigured(work)) pause(work, "configuration", "专业包返工上限未配置", "进行中");
  else if (record.formalTotal >= record.limits.formal) pause(work, "formal", "正式质量返工已达上限，需 Human 逐次决定", "进行中");
  if (record.hold) work.status = "等待";
}
export function pendingFailedBatches(state, work) {
  const control = workRework(work), events = control.events;
  return state.resources.testplans.flatMap(plan => (plan.batches || []).flatMap(batch => {
    const context = batch.workContexts?.find(item => item.workId === work.id);
    if (!context || context.version !== work.version || context.chain !== control.chain) return [];
    const failures = batch.items.filter(item => item.status === "失败" && item.run?.work === work.id && item.run.version === `v${work.version}`);
    if (!failures.length) return [];
    if (batch.status === "已提交" && !failures.some(item => state.resources.tests.some(test => test.id === item.caseId && test.currentRun === item.runId))) return [];
    if (batch.status === "已提交" && events.some(event => event.kind === "internal" && event.key === `batch:${batch.id}`)) return [];
    return [{ planId: plan.id, batchId: batch.id, needsNewBatch: batch.status === "已提交" }];
  }));
}
export function recordInternalRound(work, event, reason) {
  if (event.version !== work.version || (event.chain !== undefined && event.chain !== workRework(work).chain) || !event.failures?.length) return;
  const record = mutable(work);
  if (record.events.some(item => item.kind === "internal" && item.key === event.key)) return;
  record.internal++; record.internalTotal++; record.revision++;
  record.events.push({ ...structuredClone(event), kind: "internal", countedBy: "failed-batch", chain: record.chain,
    round: record.internal, total: record.internalTotal, reason, ...stamp() });
  if (!reworkConfigured(work)) pause(work, "configuration", "失败验证轮已记录，专业包返工上限未配置", "待验收");
  else if (record.internal >= (record.internalCeiling ?? record.limits.internal))
    pause(work, "internal", "内部验证修复轮次已用尽，需 Human 决定", "待验收");
}
export function recordInternalRepair(work, failures, reason) {
  const record = mutable(work);
  record.revision++;
  record.events.push({ kind: "repair", version: work.version - 1, submittedVersion: work.version,
    failures: structuredClone(failures), reason, ...stamp() });
}
export function configureRework(work, input, reason) {
  if (!positiveInteger(input?.internal) || !positiveInteger(input?.formal)) throw Error("两类上限必须分别填写正整数。");
  const previousStatus = work.status;
  const record = mutable(work), previous = structuredClone(record.limits);
  record.limits = { internal: Number(input.internal), formal: Number(input.formal) };
  record.decisions.push({ action: "configure", previous, limits: { ...record.limits }, reason, ...stamp() });
  record.revision++;
  // Configuration never silently clears a stop or extends an approved continuation window.
  if (!record.hold && (record.internal >= (record.internalCeiling ?? record.limits.internal) || record.formalTotal >= record.limits.formal))
    pause(work, record.internal >= (record.internalCeiling ?? record.limits.internal) ? "internal" : "formal", "已达到配置后的返工上限", previousStatus);
}
export function continueRework(work, input, reason) {
  const record = mutable(work);
  if (!record.hold) throw Error("当前没有待处理的返工停线。");
  if (!reworkConfigured(work)) throw Error("请先明确配置本专业包的两类返工上限。");
  if (record.hold.kind === "internal" && !positiveInteger(input?.additionalRounds)) throw Error("请明确本次继续允许的内部修复轮数。");
  if (record.hold.kind === "internal" && !Number.isSafeInteger(record.internal + Number(input.additionalRounds))) throw Error("本次修复窗口超出可记录范围。");
  const hold = structuredClone(record.hold);
  if (hold.kind === "internal") record.internalCeiling = record.internal + Number(input.additionalRounds);
  record.decisions.push({ action: "CONTINUE", hold, internal: record.internal, formalTotal: record.formalTotal,
    ...(hold.kind === "internal" ? { additionalRounds: Number(input.additionalRounds), internalCeiling: record.internalCeiling } : { formalWindow: 1 }), reason, ...stamp() });
  record.revision++;
  work.status = hold.resumeStatus;
  record.hold = null;
}
export function recordReworkDecision(work, action, reason) {
  const record = mutable(work);
  record.decisions.push({ action, hold: structuredClone(record.hold), internal: record.internal, formalTotal: record.formalTotal, reason, ...stamp() });
  record.revision++;
}
