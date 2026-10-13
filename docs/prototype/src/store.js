import { planPeriod, planKey, planActor, planCanRead, planCanDecide, planChecks, normalizePlanItems, planCommitment, planDiff, planSnapshot } from "./plans";
import { reportAuthor, reportReviewAuthority, reportEditProblem, reportTransitionIssue, reportSubmissionContent } from "./report-authorization";
export { reportAuthor, reportReviewAuthority, reportEditProblem } from "./report-authorization";
import { structuredGoal, closedGoalStates, normalizeGoal, goalDefinition, krDefinition, goalNumber, goalEvidence, krObservation, goalResult, readGoalMetric, goalRisks, goalPeriod, validateGoalAlignments } from "./goals";
import { useSyncExternalStore } from "react";
import { initial } from "./data";
import { prepareMessageShare } from "./message-sharing";
import { upsertPeriodicReport, periodicReportKey, validatePeriodicReport, reportSubjects, reportCadences, dateInZone, reportOwner, periodicReportFacts, immutableSubjectKeys, worklogSubjectMatch } from "./reporting";
import { workRework, pendingFailedBatches, initializeWorkRework, reworkConfigured, recordFormalRework, recordInternalRepair, recordInternalRound, configureRework, continueRework, recordReworkDecision } from "./rework";
export { workRework, pendingFailedBatches, recordFormalRework } from "./rework";
import { freezeCases, qaCounts, activeBatch, hydrateQA, startBatch, productionSnapshot, assertVerificationMutable, qualityFromPlans, qaStatuses, qaIdentity, unfinishedWorkBatches, independentRun as independentQAIdentity } from "./qa";
import { defectRecords, defectStatus, defectFields, defectQuality, migrateDefects } from "./defects";
export { defectRecords, defectStatus } from "./defects";
const KEY = "collaboration-prototype-v1";
let state, freshInitial = false;
try {
  const saved = JSON.parse(localStorage.getItem(KEY));
  state = saved?.version === 1 ? saved : (freshInitial = true, structuredClone(initial));
} catch {
  freshInitial = true;
  state = structuredClone(initial);
}
function hydrate(value, seedProduction = false) {
  value.activeTenant ??= "studio";
  value.tenants ??= [{ id: "studio", name: "Northstar Studio", role: "owner" }];
  value.tenantData ??= {};
  value.tenantInvitations ??= [{ id: "invite-partner", code: "DEMO-JOIN", tenantId: "partner", tenantName: "合作空间",
    recipient: "林予", inviter: "合作空间管理员", role: "executor", status: "待接受", expires: "2026-12-31T23:59:59+08:00" }];
  for (const [tenantId, tenant] of [[value.activeTenant, value], ...Object.entries(value.tenantData)]) {
    hydrateQA(tenant);
    tenant.resources.plans ??= [];
    tenant.planningSettings ??= {};
    tenant.organization ??= tenantId === "studio"
      ? structuredClone(initial.organization)
      : { nodes: [], members: [], projectRoles: [] };
    migrateDefects(tenant);
    const humanIds = new Set(tenant.organization.members.filter(person => person.kind === "Human").map(person => person.id));
    for (const node of tenant.organization.nodes) {
      const previousIds = node.memberIds || [];
      const removed = previousIds.filter(id => !humanIds.has(id));
      if (removed.length) {
        node.legacyMemberIds = [...new Set([...(node.legacyMemberIds || []), ...removed])];
        node.memberIds = previousIds.filter(id => humanIds.has(id));
      }
    }
    const legacyAgent = name => {
      const matches = (tenant.agents || []).filter(agent => agent.name === name && agent.status !== "待批准");
      return matches.length === 1 ? matches[0].id : null;
    };
    for (const work of tenant.work || []) if (!("agentId" in work)) work.agentId = legacyAgent(work.agent);
    // Only pristine demo fixtures have known authorship. Saved legacy data cannot infer it from today's team.
    if (seedProduction) for (const work of tenant.work || []) work.production = productionSnapshot(work);
    for (const record of tenant.resources?.worklogs || [])
      if (["Agent 墙钟", "模型 / 工具耗时"].includes(record.durationKind) && !("ownerAgentId" in record)) record.ownerAgentId = seedProduction ? legacyAgent(record.owner) : null;
    for (const agent of tenant.agents || []) {
      agent.creatorKind ??= agent.origin?.includes("主管") || agent.origin?.startsWith("Agent") ? "Agent" : agent.origin?.startsWith("Human") ? "Human" : "未知";
      agent.creatorId ??= null; // Historical source text cannot prove a particular creator identity.
    }
  }

  for (const work of value.work || []) if (!work.rework && work.returnFeedback?.length) initializeWorkRework(work);
  value.budget ??= 2400;
  value.notifications ??= { critical: true, grouped: true, memory: true };
  value.taskPreviews ??= {};
  value.flowDraft ??= null;
  value.flowVersion ??= 3;
  [value, ...Object.values(value.tenantData)]
    .flatMap((tenant) => tenant.resources?.connectors || [])
    .forEach((connector) => {
      if (typeof connector.history === "string") {
        connector.historyScope ??= connector.history;
        connector.history = [];
      }
    });
  value.work.forEach((w) => {
    if (!("plannedStart" in w)) {
      const dates = {
        "W-104": ["2026-10-12", "2026-10-16"],
        "W-103": ["2026-10-10", "2026-10-14"],
        "W-201": ["2026-10-14", "2026-10-22"],
        "W-301": ["2026-10-11", "2026-10-20"],
      };
      [w.plannedStart, w.plannedEnd] = dates[w.id] || ["", ""];
      w.scheduleRevision = 0;
      w.scheduleHistory = [];
    }

    w.history ??= [
      {
        version: w.version,
        notes: w.notes,
        requirements: [...w.requirements],
        qa: w.qa,
        accepted: w.accepted,
      },
    ];
  });
  return value;
}
state = hydrate(state, freshInitial);
const listeners = new Set();
export const read = () => state;
export const useStore = () =>
  useSyncExternalStore((fn) => {
    listeners.add(fn);
    return () => listeners.delete(fn);
  }, read);
export const id = (prefix) => `${prefix}-${crypto.randomUUID().slice(0, 8)}`;
export function update(fn) {
  const next = structuredClone(state);
  fn(next);
  localStorage.setItem(KEY, JSON.stringify(next));
  state = next;
  listeners.forEach((fn) => fn());
}
export function reset() {
  localStorage.removeItem(KEY);
  state = hydrate(structuredClone(initial), true);
  listeners.forEach((fn) => fn());
}
export function log(s, text, target = "/governance/audit") {
  s.events.unshift({
    id: id("e"),
    time: new Date().toISOString(),
    actor: s.role === "external" ? "陈曦" : "林予",
    text,
    target,
  });
}
export function assertWrite() {
  if (state.role === "external")
    throw Error("当前为外部客户视角，只能对已共享成果提交反馈与回执。");
  if (state.offline)
    throw Error("当前处于离线状态。草稿已保留，恢复连接后请重新确认操作。");
}
function recordAcceptanceFact(s, work) {
  const key = `${work.id}:v${work.version}:accept`;
  work.acceptanceFacts ??= [];
  if (work.acceptanceFacts.some(fact => fact.id === key)) return;
  const owners = s.organization.members.filter(person => person.kind === "Human" && person.name === work.owner);
  const ownerId = owners.length === 1 ? owners[0].id : null, executorIds = [...(work.production?.executorIds || [])];
  const subjectKeys = immutableSubjectKeys(s, work.project, [ownerId, ...executorIds]);
  const firstKnown = work.acceptanceFacts.length ? work.acceptanceFacts[0].firstKnown : !work.accepted && !work.history?.some(item => item.accepted);
  work.acceptanceFacts.push({ id: key, workId: work.id, version: work.version, project: work.project, time: new Date().toISOString(), ownerId, executorIds, subjectKeys: [...new Set(subjectKeys)], workSnapshot: { id: work.id, title: work.title, version: work.version, project: work.project, plan: Boolean(work.plan), quality: qualityResult(s, work) }, firstKnown, actorId: "H-lin" });
}
// A current-version failed/missing-evidence check always overrides a cached gate flag.
export function qualityResult(s, work) {
  if (work.workKind === "BUG") return defectQuality(work);
  return qualityFromPlans(s, work);
}

function syncQuality(s, workId, version) {
  const work = s.work.find((w) => w.id === workId);
  if (
    !work ||
    version !== `v${work.version}` ||
    !["待验收", "待交接"].includes(work.status)
  )
    return;
  work.qa = qualityResult(s, work) === "通过";
  if (!work.qa) {
    work.accepted = false;
    if (work.status === "待交接") work.status = "待验收";
  }
}
function returnFeedback(work, reason, feedback, version = work.version) {
  if (feedback) {
    if (!work.requirements.includes(feedback.requirement))
      throw Error("请选择该固定版本中的未满足条款。");
    if (["expected", "actual", "position"].some((key) => !feedback[key]?.trim()))
      throw Error("退回需填写期望、实际与具体位置。");
  }
  return {
    work: work.id, version, requirement: feedback?.requirement || null,
    expected: feedback?.expected || null, actual: feedback?.actual || reason,
    position: feedback?.position || null, structured: !!feedback,
  };
}
export function dependencyProblems(s, work) {
  const problems = [...(work.dependencyBlocks || [])];
  for (const input of work.inputs || []) {
    const source = s.work.find((item) => item.id === input.workId);
    if (!source || source.invalidatedVersions?.includes(input.version)) {
      if (!problems.some((block) => block.inputWorkId === input.workId && block.inputVersion === input.version))
        problems.push({ inputWorkId: input.workId, inputVersion: input.version, reason: source ? "输入版本已失效" : "输入工作不存在" });
    }
  }
  return problems;
}
function invalidateDependentOutputs(s, source, reason) {
  source.invalidatedVersions ??= [];
  if (!source.invalidatedVersions.includes(source.version)) source.invalidatedVersions.push(source.version);
  const queue = [{ workId: source.id, version: source.version }], visited = new Set();
  while (queue.length) {
    const input = queue.shift(), key = `${input.workId}:${input.version}`;
    if (visited.has(key)) continue;
    visited.add(key);
    for (const work of s.work) {
      if (!(work.inputs || []).some((ref) => ref.workId === input.workId && ref.version === input.version)) continue;
      work.dependencyBlocks ??= [];
      if (!work.dependencyBlocks.some((block) => block.inputWorkId === input.workId && block.inputVersion === input.version)) {
        work.dependencyHistory ??= [];
        work.dependencyHistory.push({ version: work.version, status: work.status, qa: work.qa, accepted: work.accepted,
          inputs: structuredClone(work.inputs), reason, time: new Date().toISOString() });
        work.dependencyBlocks.push({ inputWorkId: input.workId, inputVersion: input.version,
          sourceWorkId: source.id, sourceVersion: source.version, reason });
      }
      work.invalidatedVersions ??= [];
      if (!work.invalidatedVersions.includes(work.version)) work.invalidatedVersions.push(work.version);
      if (work.status !== "已取消") work.status = "等待";
      work.qa = false;
      work.accepted = false;
      queue.push({ workId: work.id, version: work.version });
    }
  }
}
function fixedWorkPackage(s, work, version = work.version) {
  const output = version === work.version ? work : work.history?.find((entry) => entry.version === version);
  if (!output) throw Error("固定成果版本不存在。");
  return {
    requirements: [...(output.requirements || work.requirements)],
    items: [
      { id: `${work.id}:v${version}:artifact`, name: work.title, kind: "成果引用",
        version: `v${version}`, reference: `/p/${work.project}/artifacts/${work.id}?version=${version}`,
        snapshot: { title: work.title, type: work.type, notes: output.notes }, status: "待回执" },
      ...s.resources.tests.filter((test) => test.work === work.id && test.version === `v${version}`)
        .map((test) => ({ id: `check:${test.id}`, name: test.name, kind: "检查证据引用",
          version: test.version, reference: test.evidence, snapshot: structuredClone(test), status: "待回执" })),
    ],
  };
}
export function prepareInputHandoff(sourceId, targetId, version, recipientId) {
  assertWrite();
  const source = state.work.find((work) => work.id === sourceId);
  const target = state.work.find((work) => work.id === targetId);
  if (!source || !target || source === target) throw Error("请选择本租户内的上游与下游工作。");
  if (source.status === "已取消" || target.status === "已取消") throw Error("已取消的工作不能准备新交接包。");
  if (workRework(source).hold) throw Error("上游返工已停线，请先由 Human 处理再准备交接。");
  const selectedRecipient = recipientId || source.team?.receiverId;
  const recipient = organizationPeople(state).find((person) => person.kind === "Human" && (selectedRecipient ? person.id === selectedRecipient : person.name === target.owner));
  if (!recipient) throw Error("请选择当前租户的指定 Human 接收人。");
  if (version !== source.version || qualityResult(state, source) !== "通过" || source.invalidatedVersions?.includes(version) || dependencyProblems(state, source).length)
    throw Error("请先提交并检查当前固定成果版本。");
  const contract = source.completionContract;
  if (!contract || !["internal", "external"].includes(contract.mode) ||
    (contract.mode === "internal" ? !contract.reason?.trim() : !contract.recipient?.trim())) throw Error("请先确认上游接收契约。");
  const existing = source.handoffs?.find((pack) => pack.version === version && pack.targetWorkId === targetId && pack.recipientId === recipient.id);
  if (existing) return existing.id;
  const packageId = id("H");
  update((s) => {
    const work = s.work.find((item) => item.id === sourceId);
    work.handoffs ??= [];
    work.handoffs.push({ id: packageId, version, targetWorkId: targetId, recipient: recipient.name, recipientId: recipient.id,
      status: "待接收", createdAt: new Date().toISOString(), ...fixedWorkPackage(s, work, version), history: [] });
    log(s, `准备内部固定交接包 · ${source.title} → ${target.title}`);
  });
  return packageId;
}
export function saveWorkInputs(workId, inputs, reason, expectedVersion, expectedRevision = 0) {
  assertWrite();
  if (state.role !== "owner") throw Error("输入契约变更需要负责人确认。");
  const work = state.work.find((item) => item.id === workId);
  if (!work) throw Error("工作不存在。");
  if (work.version !== expectedVersion || (work.inputsRevision || 0) !== expectedRevision) throw Error("工作或输入契约已变化，请重新核对。");
  if (work.workKind === "BUG" && ["已关闭", "已取消"].includes(defectStatus(work))) throw Error("终态缺陷不可改写，请创建关联的新缺陷。");
  if (work.status === "已取消") throw Error("工作已取消。");
  if ((work.accepted || work.delivered) && !dependencyProblems(state, work).length)
    throw Error("已接受的成果需先退回修订，再调整输入。");
  if (!reason?.trim()) throw Error("请填写输入确认或采用说明。");
  if (!Array.isArray(inputs) || new Set(inputs.map((input) => input.workId)).size !== inputs.length)
    throw Error("每个上游工作只保留一个固定输入版本。");
  for (const input of inputs) {
    const source = state.work.find((item) => item.id === input.workId);
    if (!source || source.id === workId) throw Error("输入必须是当前租户内的其他工作。");
    if (!Number.isInteger(input.version) || input.version < 1 || input.version > source.version ||
      (input.version !== source.version && !source.history?.some((entry) => entry.version === input.version)))
      throw Error("输入版本不存在。");
    const visited = new Set();
    const reachesTarget = (id) => {
      if (id === workId) return true;
      if (visited.has(id)) return false;
      visited.add(id);
      return (state.work.find((item) => item.id === id)?.inputs || []).some((entry) => reachesTarget(entry.workId));
    };
    if (reachesTarget(source.id)) throw Error("输入依赖不能形成循环。");
    const checks = state.resources.tests.filter((test) => test.work === source.id && test.version === `v${input.version}`);
    if (source.invalidatedVersions?.includes(input.version) || (input.version === source.version && dependencyProblems(state, source).length) ||
      (input.version === source.version ? qualityResult(state, source) !== "通过" :
        !checks.length || checks.some((test) => test.status !== "通过" || !test.evidence?.trim())))
      throw Error("只能采用已通过内部检查且未失效的固定输入版本。");
  }
  if (dependencyProblems(state, work).some((block) => !inputs.some((input) => input.workId === block.inputWorkId && input.version > block.inputVersion)))
    throw Error("失效输入必须显式采用通过检查的新版本，不能移除引用绕过重验。");
  const normalized = inputs.map(({ workId, version, handoffId }) => {
    const source = state.work.find((item) => item.id === workId);
    const pack = source.handoffs?.find((entry) => entry.version === version && entry.targetWorkId === work.id && (handoffId ? entry.id === handoffId : entry.recipient === work.owner));
    if (!pack?.items?.length) throw Error("请先准备面向当前下游的固定交接包，内部检查通过不等于已交接。");
    if (pack.status !== "已接受" && source.version === version && workRework(source).hold) throw Error("上游返工已停线，不能通过接收绕过 Human 决定。");
    if (pack.recipientId !== "H-lin" || !organizationPeople(state).some((person) => person.id === pack.recipientId && person.kind === "Human")) throw Error("仅指定的下游 Human 接收人可以接收固定包。");
    return { workId, version, handoffId: pack.id };
  });
  if (JSON.stringify(work.inputs) === JSON.stringify(normalized)) return;
  update((s) => {
    const target = s.work.find((item) => item.id === workId);
    target.inputHistory ??= [];
    target.inputHistory.push({ inputs: structuredClone(target.inputs || []), configured: Array.isArray(target.inputs),
      version: target.version, revision: target.inputsRevision || 0, reason, time: new Date().toISOString() });
    if (target.inputs !== undefined || target.qa || target.accepted || target.delivered)
      invalidateDependentOutputs(s, target, `输入契约变更：${reason}`);
    for (const input of normalized) {
      const source = s.work.find((item) => item.id === input.workId);
      const pack = source.handoffs.find((entry) => entry.id === input.handoffId);
      if (pack.status !== "已接受") {
        pack.history.push({ status: pack.status, items: structuredClone(pack.items), actor: "林予", reason, time: new Date().toISOString() });
        pack.status = "已接受";
        pack.items = pack.items.map((item) => ({ ...item, status: "已接受" }));
        pack.receipt = { actor: "林予", actorId: "H-lin", reason, time: new Date().toISOString(), level: "L3" };
        if (source.version === input.version && !source.accepted) {
          recordAcceptanceFact(s, source);
          source.accepted = true;
          source.l3 = { ...pack.receipt, version: input.version, handoffId: pack.id };
          source.status = source.completionContract?.mode === "internal" ? "已完成" : "待交接";
          if (source.completionContract?.mode === "internal") source.l4 = { status: "不适用", reason: source.completionContract.reason, version: source.version };
        }
      }
    }
    target.inputs = normalized;
    target.inputsRevision = (target.inputsRevision || 0) + 1;
    target.dependencyBlocks = [];
    target.qa = false; target.accepted = false; target.delivered = false;
    delete target.currentDeliveryId; delete target.l4;
    if (target.status !== "待开始") target.status = workRework(target).hold ? "等待" : "进行中";
    log(s, `确认固定输入 · ${target.title} · ${reason}`, `/p/${target.project}/work-items/${target.id}`);
  });
}
export function beginWorkDraft({ sourceId, projectId, channel, executionId, threadId, workKind, reopenedFrom } = {}) {
  assertWrite();
  const previousDefect = reopenedFrom && state.work.find(work => work.id === reopenedFrom && work.workKind === "BUG");
  if (reopenedFrom && !previousDefect) throw Error("原缺陷不在当前租户。");
  if (previousDefect) { workKind = "BUG"; projectId = previousDefect.project; }
  const source = sourceId ? state.messages.find((message) => message.id === sourceId) : null;
  if (sourceId && !source) throw Error("来源消息不在当前租户中。");
  executionId ||= source?.channel?.startsWith("exec-") ? source.channel.slice(5) : undefined;
  const execution = executionId ? state.work.find((work) => work.id === executionId) : null;
  if (executionId && !execution) throw Error("来源工作不在当前租户中。");
  const sourceProject = state.projects.find((project) => project.id === source?.channel)?.id;
  const selectedProject = execution?.project || projectId || sourceProject || (source?.channel === "private" ? "private" : state.project === "all" ? "private" : state.project);
  if (selectedProject !== "private" && !state.projects.some((project) => project.id === selectedProject)) throw Error("归属项目不在当前租户中。");
  const existing = source && Object.values(state.drafts || {}).find((draft) => draft.kind === "work" && draft.workKind === workKind && draft.source?.messageId === source.id);
  if (existing) return existing.id;
  const draftId = id("draft");
  update((s) => {
    s.drafts ??= {};
    s.drafts[draftId] = { id: draftId, kind: "work", tenantId: s.activeTenant, status: "讨论中", revision: 0,
      project: selectedProject, channel: source?.channel || channel || (selectedProject === "private" ? "private" : "team"),
      executionId: execution?.id || null, threadId: threadId || source?.thread || source?.id || null,
      title: previousDefect ? `回归：${previousDefect.title}` : source?.text || "", type: "软件", criteria: previousDefect?.defect.expected || "", discussion: [], history: [],
      ...(workKind === "BUG" ? { workKind, reopenedFrom: previousDefect?.id || null, defect: previousDefect ? Object.fromEntries(defectFields.map(key => [key, previousDefect.defect[key] || ""])) : {} } : {}),
      source: source ? { messageId: source.id, channel: source.channel, text: source.text, threadId: source.thread || null, tenantId: s.activeTenant, project: selectedProject } : null };
    log(s, "进入会话工作草稿");
  });
  return draftId;
}
export function saveWorkDraft(draftId, fields, expectedRevision) {
  assertWrite();
  const draft = state.drafts?.[draftId];
  if (!draft || draft.kind !== "work" || draft.tenantId !== state.activeTenant) throw Error("当前租户没有此工作草稿。");
  if (draft.workId) throw Error("草稿已生成工作，请在工作中继续处理。");
  if (draft.revision !== expectedRevision) throw Error("草稿已变化，请重新核对。");
  if (fields.project !== "private" && !state.projects.some((project) => project.id === fields.project)) throw Error("请选择当前租户的项目。");
  update((s) => {
    const target = s.drafts[draftId];
    target.history.push({ title: target.title, criteria: target.criteria, project: target.project, type: target.type, revision: target.revision });
    Object.assign(target, { title: fields.title, criteria: fields.criteria, project: fields.project, type: target.workKind === "BUG" ? "软件" : fields.type, revision: target.revision + 1 });
    if (target.workKind === "BUG") target.defect = Object.fromEntries(defectFields.map(key => [key, fields.defect?.[key] ?? target.defect?.[key] ?? ""]));
  });
}
export function confirmWorkDraft(draftId, expectedRevision) {
  assertWrite();
  if (state.role !== "owner") throw Error("请由 Human 负责人确认工作与计划。");
  const draft = state.drafts?.[draftId];
  if (!draft || draft.kind !== "work" || draft.tenantId !== state.activeTenant) throw Error("当前租户没有此工作草稿。");
  if (draft.workId) return state.work.find((work) => work.id === draft.workId);
  if (draft.revision !== expectedRevision) throw Error("草稿已变化，请重新核对。");
  if (!draft.title?.trim() || !draft.criteria?.trim()) throw Error("请在会话中补充目标与完成条件，再确认生成。");
  if (draft.workKind === "BUG") validateDefectDetails(state, draft.defect, draft.project);
  let created;
  update((s) => {
    const target = s.drafts[draftId];
    if (target.project === "private" && !s.projects.some((project) => project.id === "private"))
      s.projects.push({ id: "private", name: "我的私人工作", type: "个人", owner: "林予", progress: 0, budget: 0, spent: 0, deadline: "待确认", color: "violet", description: "会话确认的私人工作。" });
    if (!s.projects.some((project) => project.id === target.project)) throw Error("草稿归属项目已不可用。");
    created = { id: id(target.workKind === "BUG" ? "BUG" : "W"), title: target.title.trim(), project: target.project, type: target.type, status: "待开始",
      owner: "林予", agent: "未分派", agentId: null, due: "待确认", version: 1, plan: true, flow: false, qa: false, accepted: false, delivered: false,
      cost: 0, notes: "由会话草稿经 Human 确认生成；执行前仍需确认 SOP。", requirements: [target.criteria.trim()],
      source: target.source?.messageId, sourceContext: structuredClone(target.source), draftId,
      planSnapshot: { goal: target.title.trim(), criteria: target.criteria.trim(), discussion: structuredClone(target.discussion), confirmedBy: "林予", draftRevision: target.revision } };
    if (target.workKind === "BUG") {
      created.workKind = "BUG"; created.defect = { ...structuredClone(target.defect), stage: null, revision: 1, repairRevision: 0, history: [], reopenedFromWorkItemId: target.reopenedFrom || null };
      created.ownerSource = "会话草案 Human 确认人";
    }
    s.work.unshift(created);
    target.workId = created.id; target.status = "已确认";
    log(s, `会话确认工作与计划 · ${created.title}`, `/p/${created.project}/work-items/${created.id}`);
  });
  return created;
}
export function workCommand(workId, command, reason = "", expectedVersion, feedback) {
  assertWrite();
  const w = state.work.find((x) => x.id === workId);
  if (!w) throw Error("工作不存在");
  if (expectedVersion !== undefined && w.version !== expectedVersion)
    throw Error("成果版本已经变化，请重新核对后再提交。");
  if (["return", "cancel", "takeover"].includes(command) && !reason.trim())
    throw Error("请填写理由，便于下一位参与者继续处理。");
  const returned = command === "return" ? returnFeedback(w, reason, feedback) : null;
  if (returned && w.returnFeedback?.some((entry) =>
    entry.reason === reason && Object.keys(returned).every((key) => entry[key] === returned[key]))) return;
  if (w.workKind === "BUG") {
    if (["accept", "deliver"].includes(command)) throw Error("缺陷须通过专业区选择修复后的独立验证证据并明确关闭，不能绕过缺陷验证门。");
    if (["return", "takeover", "start", "resume", "submit", "repair", "cancel"].includes(command) && ["已关闭", "已取消"].includes(defectStatus(w))) throw Error("终态缺陷保留原记录；内部回归请创建关联的新缺陷。");
  }
  const control = workRework(w);
  const governance = ["rework-config", "rework-continue"].includes(command);
  if ((governance || (["takeover", "cancel"].includes(command) && control.hold)) && state.role !== "owner") throw Error("返工停线决定需要 Human 负责人权限。");
  if ((governance || command === "repair") && !reason?.trim()) throw Error("请填写修复或 Human 决定依据。");
  if (governance && feedback?.revision !== control.revision) throw Error("返工记录已变化，请重新核对当前决定。");
  if (control.hold && ["start", "resume", "submit", "repair", "check", "accept", "deliver", "return"].includes(command)) throw Error("返工已停线，须由 Human 负责人明确继续、接管或取消。");
  if (["repair", "submit", "return"].includes(command) && pendingFailedBatches(state, w).length) throw Error("请先到测试计划结束失败验证批次并形成报告；轮次记录后才可提交新成果版本。");
  const failures = ["repair", "submit"].includes(command) ? state.resources.tests.filter(test => test.work === w.id && test.version === `v${w.version}` && test.status === "失败") : [];
  const formallyReturned = control.events.some(event => event.kind === "formal" && String(event.version).replace(/^v/, "") === String(w.version));
  if (command === "submit" && failures.length && !formallyReturned) throw Error("当前版本存在未处理的失败验证轮，请明确提交内部修复，不能以普通成果提交绕过。");
  if (command === "repair" && (!["待验收", "等待", "进行中"].includes(w.status) || !failures.length || formallyReturned)) throw Error("仅可针对当前版本失败验证轮提交内部修复。");
  if (command === "repair" && !reworkConfigured(w)) throw Error("请先明确配置本专业包的两类返工上限。");
  if (command === "rework-continue" && !control.hold) throw Error("当前没有待处理的返工停线。");
  const labels = {
    contract: "确认接收契约",
    plan: "确认计划",
    flow: "确认 SOP",
    start: "开始执行",
    submit: "提交成果",
    repair: "提交内部修复",
    "rework-config": "配置返工上限",
    "rework-continue": "Human 明确继续",
    check: "开始独立验证",
    accept: "接受成果",
    deliver: "正式交付",
    return: "退回修订",
    cancel: "取消工作",
    takeover: "Human 接管",
    resume: "继续执行",
  };
  if (!labels[command]) throw Error("未知工作动作。");
  if (["start", "resume", "submit", "repair", "check", "accept", "deliver"].includes(command) && dependencyProblems(state, w).length)
    throw Error("输入依赖已失效，请明确采用通过检查的新版本并重新验收。");
  if (w.status === "已取消") throw Error("工作已取消，不能继续推进。");
  if (w.delivered && !["return"].includes(command))
    throw Error("此版本已经交付，请通过返工产生新版本。");
  if (command === "start" && w.status !== "待开始")
    throw Error("只有待开始的工作可以启动。");
  if (command === "submit" && !["进行中", "等待"].includes(w.status))
    throw Error("只有进行中或接管中的工作可以提交新成果。");
  if (
    ["plan", "flow", "contract", "accept", "deliver"].includes(command) &&
    state.role === "executor"
  )
    throw Error("当前执行者没有此项 Human 决定权限，请交给负责人。");
  if (command === "flow" && !w.plan)
    throw Error("先确认当前计划，再确认 SOP。");
  if (command === "start" && (!w.plan || !w.flow))
    throw Error("计划与 SOP 尚未全部确认。");
  if (
    command === "start" &&
    (!Number.isInteger(w.flowVersion) ||
      !flowDefinition(state, w.project, w.flowVersion))
  )
    throw Error("请重新确认已发布工作流，锁定当前执行版本。");
  if (command === "resume" && w.status !== "等待")
    throw Error("只有等待中的工作可以继续执行。");
  if (command === "check" && w.status !== "待验收")
    throw Error("先提交固定版本再进行独立检查。");
  if (command === "accept" && qualityResult(state, w) !== "通过")
    throw Error("必需内部检查尚未通过，Human 批准不能绕过质量门。");
  if (
    command === "deliver" &&
    (qualityResult(state, w) !== "通过" || !w.accepted)
  )
    throw Error("交付前须内部检查通过，并由 Human 接受当前版本。");
  if (command === "accept" && w.status !== "待验收")
    throw Error("只有待验收的固定成果可以接受。");
  if (command === "deliver" && w.status !== "待交接")
    throw Error("只有已接受并待交接的成果可以交付。");
  if (command === "deliver" && w.delivered)
    throw Error("当前版本已经交付，请查看既有交付包。");
  if (command === "contract") {
    if (w.accepted) throw Error("当前版本已接受，请先退回修订再改变接收契约。");
    if (!feedback || !["internal", "external"].includes(feedback.mode)) throw Error("请选择接收方式。");
    if (feedback.mode === "internal" && !feedback.reason?.trim()) throw Error("请说明为何无外部接收方。");
    if (feedback.mode === "external" && !feedback.recipient?.trim()) throw Error("请指定接收人。");
  }
  if (["accept", "deliver"].includes(command)) {
    const contract = w.completionContract;
    if (!contract || !["internal", "external"].includes(contract.mode) ||
      (contract.mode === "internal" ? !contract.reason?.trim() : !contract.recipient?.trim()))
      throw Error("请先确认接收契约，不能推测是否需要外部回执。");
    if (command === "deliver" && contract.mode !== "external") throw Error("无外部接收方的工作在内部接受后结束，公开发布需要独立授权。");
  }
  update((s) => {
    const x = s.work.find((x) => x.id === workId);
    if (control.hold && !x.rework) initializeWorkRework(x);
    if (command === "contract") x.completionContract = {
      mode: feedback.mode, reason: feedback.mode === "internal" ? feedback.reason.trim() : "",
      recipient: feedback.mode === "external" ? feedback.recipient.trim() : "",
    };
    if (command === "plan") x.plan = true;
    if (command === "flow") {
      lockWorkFlow(s, x);
      x.flow = true;
    }
    if (command === "start" || command === "resume") x.status = "进行中";
    if (command === "submit" || command === "repair") {
      x.history ??= [];
      x.history.push({
        version: x.version,
        notes: x.notes,
        requirements: [...x.requirements],
        qa: x.qa,
        accepted: x.accepted,
        ...(x.production ? { production: structuredClone(x.production) } : {}),
      });
      x.status = "待验收";
      x.version++;
      x.production = productionSnapshot(x);
      x.qa = false;
      x.accepted = false;
      x.delivered = false;
      delete x.currentDeliveryId;
      delete x.l4;
      if (command === "repair") recordInternalRepair(x, failures.map(test => ({ caseId: test.id, runId: test.currentRun || null, version: test.version, actual: test.actual, evidence: test.evidence })), reason);
    }
    if (command === "check") {
      startWorkValidation(s, x);
    }
    if (command === "accept") {
      recordAcceptanceFact(s, x);
      x.accepted = true;
      x.status = x.completionContract.mode === "internal" ? "已完成" : "待交接";
      x.l4 = x.completionContract.mode === "internal"
        ? { status: "不适用", reason: x.completionContract.reason, version: x.version }
        : { status: "待交付", recipient: x.completionContract.recipient, version: x.version };
    }
    if (command === "deliver") {
      x.delivered = true;
      x.status = "待交接";
      x.currentDeliveryId = id("D");
      x.l4 = { status: "待接收", recipient: x.completionContract.recipient, version: x.version };
      s.resources.deliveries.unshift({
        id: x.currentDeliveryId,
        name: `${x.title} · v${x.version}`,
        status: "待回执",
        owner: x.owner,
        work: x.id,
        version: `v${x.version}`,
        scope: x.completionContract.recipient,
        recipient: x.completionContract.recipient,
        evidence: "内部检查与 Human 接受记录",
        content: "原型固定成果与检查证据引用；不包含真实源文件或发布结果。",
        ...fixedWorkPackage(s, x),
      });
    }
    if (command === "return") {
      initializeWorkRework(x);
      invalidateDependentOutputs(s, x, reason);
      x.returnFeedback ??= [];
      x.returnFeedback.push({ ...returned, reason, time: new Date().toISOString() });
      x.status = dependencyProblems(s, x).length ? "等待" : "进行中";
      x.qa = false;
      x.accepted = false;
      x.delivered = false;
      x.notes = reason;
      delete x.currentDeliveryId;
      delete x.l4;
    }
    if (command === "return") recordFormalRework(s, x, reason, { key: `work:v${w.version}`, source: "work", version: w.version });
    if (command === "rework-config") configureRework(x, feedback, reason);
    if (command === "rework-continue") {
      continueRework(x, feedback, reason);
      if (dependencyProblems(s, x).length) x.status = "等待";
    }
    if (["takeover", "cancel"].includes(command) && x.rework) recordReworkDecision(x, command, reason);
    if (command === "cancel") x.status = "已取消";
    if (command === "takeover") {
      x.status = "等待";
      x.notes = `Human 已接管，Agent 暂停竞争写入。${reason}`;
    }
    log(
      s,
      `${labels[command]} · ${x.title} v${x.version}${reason ? " · " + reason : ""}`,
      `/p/${x.project}/work-items/${x.id}`,
    );
  });
  if (command === "check") return `/testplans/${read().work.find(item => item.id === workId).qaPlanId}`;
}
export function saveTestPlan(record) {
  assertWrite();
  if (state.role !== "owner") throw Error("固定或变更验收计划需要负责人确认。");
  const previous = state.resources.testplans.find(plan => plan.id === record.id);
  if (previous && (record.revision || 1) !== (previous.revision || 1)) throw Error("测试计划已变化，请重新打开。");
  for (const key of ["name", "scope", "environment", "targetBuild", "assignment", "criterion", "windowStart", "windowEnd"])
    if (!String(record[key] || "").trim()) throw Error("请填写计划范围、目标构建、环境、分派、执行窗口及完成条件。");
  if (record.windowStart > record.windowEnd) throw Error("执行窗口结束不能早于开始。");
  if (previous && !record.changeReason?.trim()) throw Error("计划改版需说明变更影响与重验范围。");
  if (previous && activeBatch(previous)?.status === "进行中") throw Error("请先形成固定测试报告结束当前批次，再修订计划；未执行项会单独保留。");
  const caseIds = record.caseIds || record.caseRefs?.map(ref => ref.caseId);
  const caseRefs = freezeCases(state, caseIds);
  for (const ref of [...caseRefs, ...(previous?.caseRefs || [])])
    assertVerificationMutable(state, ref.snapshot.work, ref.snapshot.version);
  if (caseRefs.some(ref => unfinishedWorkBatches(state, ref.snapshot.work, previous?.id).length)) throw Error("该工作还有未结束的验证批次，请先形成报告再采用新计划。");
  let resultId;
  update(s => {
    const plan = { id: previous?.id || id("TP"), name: record.name.trim(), owner: record.owner || "林予",
      status: "草稿", revision: (previous?.revision || 0) + 1, version: `r${(previous?.revision || 0) + 1}`,
      scope: record.scope, environment: record.environment, targetBuild: record.targetBuild,
      assignment: record.assignment, windowStart: record.windowStart, windowEnd: record.windowEnd,
      criterion: record.criterion, changeReason: record.changeReason || "首次固定",
      retestCaseIds: caseIds.slice(), caseRefs, batches: copyQA(previous?.batches || []),
      history: [...(previous?.history || []), ...(previous ? [{ ...copyQA(previous), history: undefined, batches: undefined }] : [])] };
    if (previous) s.resources.testplans[s.resources.testplans.findIndex(item => item.id === previous.id)] = plan;
    else s.resources.testplans.unshift(plan);
    const nextWorkIds = new Set(caseRefs.map(ref => ref.snapshot.work));
    const affectedWorkIds = new Set([...nextWorkIds, ...(previous?.caseRefs || []).map(ref => ref.snapshot.work)]);
    for (const workId of affectedWorkIds) {
      const work = s.work.find(work => work.id === workId);
      if (!work) continue;
      if (nextWorkIds.has(workId)) work.qaPlanId = plan.id;
      syncQuality(s, workId, `v${work.version}`);
    }
    resultId = plan.id;
    log(s, `固定测试计划修订 · ${plan.name} r${plan.revision}`, `/testplans/${plan.id}`);
  });
  return resultId;
}
const copyQA = value => structuredClone(value);
function startWorkValidation(s, work) {
  let plan = s.resources.testplans.find(plan => plan.id === work.qaPlanId);
  const currentRefs = plan?.caseRefs?.filter(ref => ref.snapshot.work === work.id && ref.snapshot.version === `v${work.version}`);
  if (!currentRefs?.length) {
    if (unfinishedWorkBatches(s, work.id, plan?.id).length) throw Error("该工作还有未结束的验证批次，请先形成报告。");
    if (s.role !== "owner") throw Error("请由负责人先固定当前版本的验收计划。");
    let tests = s.resources.tests.filter(test => test.work === work.id && test.version === `v${work.version}`);
    if (!tests.length) {
      const test = { id: id("TC"), name: `${work.title} · 验收条款验证`, status: "待执行", revision: 1,
        owner: "Verifier", work: work.id, version: `v${work.version}`, sourceKind: "平台独立验证",
        method: "逐条核对固定成果", inputs: `${work.id} / v${work.version}`, expected: work.requirements.join("；"),
        requirementRefs: [...work.requirements], preconditions: "固定成果可访问", steps: "逐条核对固定验收要求并记录实际结果",
        type: "验收", priority: "高", runs: [] };
      s.resources.tests.unshift(test); tests = [test];
    }
    // Missing coverage creates new pending checks; it never expands an old case's claims.
    for (const requirement of work.requirements.filter(requirement => !tests.some(test => test.requirementRefs?.includes(requirement)))) {
      const test = { id: id("TC"), name: requirement, owner: "Verifier", sourceKind: "平台独立验证", work: work.id,
        version: `v${work.version}`, revision: 1, status: "待执行", requirementRefs: [requirement], expected: requirement,
        preconditions: "固定成果可访问", steps: "核对指定条款并记录实际结果", type: "验收", priority: "高", runs: [] };
      s.resources.tests.unshift(test); tests.push(test);
    }
    plan = { id: id("TP"), name: `${work.title} · v${work.version} 独立验证`, owner: "林予", revision: 1,
      version: "r1", status: "草稿", scope: work.requirements.join("；"), targetBuild: `${work.id}/v${work.version}`,
      environment: "本地原型模拟环境", assignment: "Verifier", windowStart: new Date().toISOString().slice(0, 10), windowEnd: new Date().toISOString().slice(0, 10), criterion: "全部必需条款由独立验证通过",
      caseRefs: freezeCases(s, tests.map(test => test.id)), batches: [], history: [], changeReason: "固定当前工作验收范围" };
    s.resources.testplans.unshift(plan); work.qaPlanId = plan.id;
  }
  startAndSyncBatch(s, plan);
}
function startAndSyncBatch(s, plan) {
  // startBatch validates every member before any adoption or state change; update commits atomically.
  const batch = startBatch(s, plan, id);
  for (const workId of new Set(batch.caseRefs.map(ref => ref.snapshot.work))) {
    const work = s.work.find(work => work.id === workId);
    work.qaPlanId = plan.id;
    syncQuality(s, workId, `v${work.version}`);
  }
  return batch;
}
export function startTestBatch(planId) {
  assertWrite();
  if (!state.resources.testplans.some(plan => plan.id === planId)) throw Error("测试计划不存在。");
  let batchId;
  update(s => {
    const plan = s.resources.testplans.find(plan => plan.id === planId);
    batchId = startAndSyncBatch(s, plan).id;
    log(s, `开始测试批次 · ${batchId}`, `/testplans/${planId}`);
  });
  return batchId;
}
export function recordBatchResult(planId, batchId, caseId, result) {
  assertWrite();
  if (!qaStatuses.includes(result.status) || result.status === "未执行") throw Error("请选择通过、失败、跳过或阻塞。");
  if (!result.actual?.trim() || !result.evidence?.trim() || !result.ownerId || !result.ownerKind || !result.toolVersion?.trim())
    throw Error("逐项结果需填写实际结果、证据、运行主体与工具版本。");
  if (!["Human 手测", "Agent 自测", "平台独立验证"].includes(result.sourceKind)) throw Error("请选择明确的验证来源。");
  update(s => {
    const plan = s.resources.testplans.find(plan => plan.id === planId);
    const batch = plan && activeBatch(plan);
    if (!batch || batch.id !== batchId || batch.status !== "进行中") throw Error("执行批次已固定或计划已改版，请启动新批次。");
    const ref = batch.caseRefs.find(ref => ref.caseId === caseId);
    const test = s.resources.tests.find(test => test.id === caseId);
    const work = s.work.find(work => work.id === ref?.snapshot.work);
    if (!ref || !test || !work || ref.snapshot.version !== `v${work.version}`) throw Error("工作版本已变更，旧批次只能查看历史。");
    assertVerificationMutable(s, ref.snapshot.work, ref.snapshot.version);
    assertTestDefect(s, ref.snapshot);
    if ((test.revision || 1) !== ref.caseRevision) throw Error("用例已改版，请由负责人修订计划并明确重验范围。");
    const context = batch.workContexts?.find(context => context.workId === work.id);
    if (!context || context.version !== work.version || context.chain !== workRework(work).chain) throw Error("工作声明链已变化，旧批次只能形成历史报告。");
    const item = batch.items.find(item => item.caseId === caseId);
    if (item.runId) throw Error("此项结果已固定；请先结束当前批次，再开新批次重验。");
    const expectedKind = result.sourceKind === "Human 手测" ? "Human" : "Agent";
    const identity = result.ownerKind === expectedKind && qaIdentity(s, result.ownerKind, result.ownerId);
    if (!identity) throw Error("请选择与验证来源匹配的当前主体身份。");
    preserveTestRun(s, test);
    const run = { ...copyQA(ref.snapshot), id: id("run"), caseId, caseRevision: ref.caseRevision,
      status: result.status, actual: result.actual.trim(), evidence: result.evidence.trim(), ...identity,
      sourceKind: result.sourceKind, source: "mock", actor: "林予", time: new Date().toISOString(),
      capturedAt: new Date().toISOString(), batchId, planId, planRevision: batch.planRevision,
      environment: batch.environment, targetBuild: batch.targetBuild, toolVersion: result.toolVersion.trim(),
      logs: result.logs?.trim() || "", workRequirements: [...work.requirements], reason: result.actual.trim() };
    const defect = defectRecords(s).find(defect => defect.id === run.defect);
    if (defect?.status === "待验证") run.defectRepairRevision = defect.repairRevision;
    test.runs.push(run); test.currentRun = run.id; test.status = run.status; test.actual = run.actual; test.evidence = run.evidence;
    item.runId = run.id; item.status = run.status; item.run = copyQA(run);
    syncQuality(s, test.work, test.version);
    log(s, `记录逐项测试 · ${test.name} · ${run.status}`, `/testplans/${planId}`);
  });
}
export function publishTestReport(planId) {
  assertWrite();
  let reportId;
  update(s => {
    const plan = s.resources.testplans.find(plan => plan.id === planId);
    const batch = plan && activeBatch(plan);
    if (!batch) throw Error("请先启动固定计划的执行批次。");
    if (batch.reportId) { reportId = batch.reportId; return; }
    batch.status = "已提交"; batch.finishedAt = new Date().toISOString();
    for (const workId of new Set(batch.caseRefs.map(ref => ref.snapshot.work))) {
      const work = s.work.find(work => work.id === workId);
      const context = batch.workContexts?.find(context => context.workId === workId);
      const failures = batch.items.filter(item => item.status === "失败" && item.run?.work === workId && item.run.version === `v${work?.version}`).map(item => copyQA(item.run));
      if (work && work.status !== "已取消" && context?.version === work.version && context.chain === workRework(work).chain && failures.length) recordInternalRound(work, { key: `batch:${batch.id}`, version: context.version, chain: context.chain, failures }, `固定验证批次 ${batch.id} 结束，${failures.length} 项失败`);
    }
    reportId = id("TR"); batch.reportId = reportId; plan.status = "已报告";
    const { batches, history, ...planSnapshot } = plan;
    s.resources.testreports.unshift({ id: reportId, name: `${plan.name} · ${batch.id} 报告`, status: "已固定",
      planId, batchId: batch.id, recordedAt: batch.finishedAt, actor: "林予", counts: qaCounts(batch.items),
      planSnapshot: copyQA(planSnapshot), batchSnapshot: copyQA(batch) });
    for (const workId of new Set(batch.caseRefs.map(ref => ref.snapshot.work))) { const work = s.work.find(work => work.id === workId); if (work) syncQuality(s, workId, `v${work.version}`); }
    log(s, `形成固定测试报告 · ${reportId}`, `/testreports/${reportId}`);
  });
  return reportId;
}

function assertTestDefect(s, test) {
  if (!test.defect) return;
  const defect = defectRecords(s).find(item => item.id === test.defect);
  if (!defect || defect.work !== test.work || defect.version !== test.version)
    throw Error("关联缺陷必须属于当前租户、同一工作与同一版本，请重新选择或清除缺陷关联。");
}

// Runs are copied at creation; case edits and later results never rewrite them.
function testRunSnapshot(s, test, source, reason = "") {
  const { runs, history, currentRun, ...snapshot } = test;
  const work = s.work.find((item) => item.id === test.work);
  const workVersion = work && test.version === `v${work.version}`
    ? work : work?.history?.find((item) => `v${item.version}` === test.version);
  const defect = defectRecords(s).find((item) => item.id === test.defect);
  return structuredClone({
    ...snapshot,
    ...(qaIdentity(s, test.sourceKind === "Human 手测" ? "Human" : ["平台独立验证", "Agent 自测"].includes(test.sourceKind) ? "Agent" : null, test.ownerId, test.owner) || {}),
    id: id("run"), caseId: test.id, caseRevision: test.revision || 1,
    workRequirements: workVersion?.requirements || [],
    source, reason,
    actor: source === "mock" ? "林予" : test.actor || null,
    time: source === "mock" ? new Date().toISOString() : test.time || null,
    capturedAt: new Date().toISOString(),
    ...(source === "mock" && defect?.status === "待验证"
      ? { defectRepairRevision: defect.repairRevision } : {}),
  });
}
function preserveTestRun(s, test) {
  test.runs ??= [];
  if (["通过", "失败"].includes(test.status) && !test.currentRun) {
    const run = testRunSnapshot(s, test, "legacy");
    test.runs.push(run);
    test.currentRun = run.id;
  }
}
function recordTestRun(s, test, status, reason) {
  assertVerificationMutable(s, test.work, test.version);
  assertTestDefect(s, test);
  preserveTestRun(s, test);
  test.status = status;
  test.actual = status === "通过" ? `模拟重验通过：${reason}` : `模拟检查失败：${reason}`;
  // Every attempt has its own evidence reference, including failed attempts.
  test.evidence = `mock-run/${test.work}/${test.version}/${id("evidence")}`;
  const run = testRunSnapshot(s, test, "mock", reason);
  test.runs.push(run);
  test.currentRun = run.id;
}
export function defectVerificationRuns(s, defect) {
  const work = s.work.find((item) => item.id === defect.work);
  const defectWork = s.work.find(item => item.id === defect.id && item.workKind === "BUG");
  if (defect.repairWorkVersion && defectWork?.version !== defect.repairWorkVersion) return [];
  if (!work || defect.repairVersion !== `v${work.version}` || defect.version !== defect.repairVersion) return [];
  return s.resources.tests.flatMap((test) => (test.runs || []).filter((run) =>
    run.id === test.currentRun && run.caseRevision === (test.revision || 1) &&
    run.defect === defect.id && run.defectRepairRevision === defect.repairRevision &&
    run.work === defect.work && run.version === defect.repairVersion &&
    run.source === "mock" && ["Human 手测", "平台独立验证"].includes(run.sourceKind) &&
    run.owner?.trim() && run.owner !== defect.owner && independentQAIdentity(s, work, run) &&
    (!defect.repairExecutorIds || !defect.repairExecutorIds.includes(run.ownerKind === "Agent" ? `agent:${run.ownerId}` : run.ownerId)) &&
    run.evidence?.trim(),
  ));
}

export function openPeriodPlan(input) {
  assertWrite();
  const period = planPeriod(input.periodKind, input.date, input.timezone);
  const descriptor = { subjectKey: input.subjectKey, periodKind: input.periodKind, timezone: input.timezone, ...period };
  const actor = planActor(state, descriptor, input.simulateAgent);
  const previous = (state.resources.plans || []).find(plan => planKey(state, plan) === planKey(state, descriptor));
  if (previous) return previous.id;
  const planId = id("PLAN");
  update(s => {
    s.resources.plans ??= [];
    s.resources.plans.unshift({ ...descriptor, id: planId, tenantId: s.activeTenant, status: "草稿", revision: 1, items: [],
      draftedBy: actor, history: [], freeTaskFacts: [], createdAt: new Date().toISOString() });
    log(s, "生成成员周期计划草稿", `/plans/${planId}`);
  });
  return planId;
}
function currentPlan(s, planId, revision) {
  const plan = s.resources.plans?.find(plan => plan.id === planId);
  if (!plan || plan.tenantId !== s.activeTenant || !planCanRead(s, plan)) throw Error("计划不存在或当前主体无权访问。");
  if (revision !== plan.revision) throw Error("计划修订已变化，请重新打开当前版本。");
  return plan;
}
function freezePlanSubmission(s, plan, actor) {
  const checks = planChecks(s, plan);
  if (checks.blocking.length) throw Error(checks.blocking.map(issue => `${issue.code}：${issue.message}`).join("；"));
  if (!checks.confirmer.id) throw Error(checks.confirmer.issue);
  const snapshot = planSnapshot(s, plan);
  plan.status = "已提交";
  plan.submission = { revision: plan.revision, snapshot, contentHash: JSON.stringify(snapshot), checks: structuredClone(checks),
    confirmerId: checks.confirmer.id, confirmerBasis: checks.confirmer.basis, actor, time: new Date().toISOString() };
}
export function savePeriodPlan(planId, input, expectedRevision) {
  assertWrite();
  const plan = currentPlan(state, planId, expectedRevision), actor = planActor(state, plan, input.simulateAgent);
  const items = normalizePlanItems(input.items, id);
  const changed = JSON.stringify(planCommitment(items)) !== JSON.stringify(planCommitment(plan.items));
  if (plan.confirmedSnapshot && changed && !input.reason?.trim()) throw Error("已确认承诺变化须说明范围、时间或责任变更原因。");
  const previousIds = new Set(plan.items.map(item => item.id));
  if (items.some(item => item.carriedFromItemId && !previousIds.has(item.id) && !(state.resources.plans || []).some(source => source.subjectKey === plan.subjectKey && source.confirmedSnapshot?.items.some(previous => previous.id === item.carriedFromItemId)))) throw Error("结转来源不是同一主体的已确认计划项。");
  update(s => {
    const target = currentPlan(s, planId, expectedRevision);
    target.history.push({ revision: target.revision, status: target.status, items: structuredClone(target.items), submission: structuredClone(target.submission || null), actor, reason: input.reason?.trim() || "保存草稿", time: new Date().toISOString() });
    target.items = items; target.revision++;
    target.pendingDiff = planDiff(target.confirmedSnapshot?.items || [], items);
    if (target.confirmedSnapshot && changed) freezePlanSubmission(s, target, actor);
    else if (target.status !== "已确认") { target.status = "草稿"; delete target.submission; }
    log(s, "保存周期计划修订", `/plans/${planId}`);
  });
}
export function submitPeriodPlan(planId, expectedRevision, simulateAgent = false) {
  assertWrite();
  const plan = currentPlan(state, planId, expectedRevision), actor = planActor(state, plan, simulateAgent);
  if (!["草稿", "已退回"].includes(plan.status)) throw Error("只有草稿或退回后的计划可以提交。");
  update(s => {
    const target = currentPlan(s, planId, expectedRevision); target.revision++;
    freezePlanSubmission(s, target, actor);
    log(s, "提交周期计划，请 Human 上级确认", `/plans/${planId}`);
  });
}
export function decidePeriodPlan(planId, input) {
  assertWrite();
  const plan = currentPlan(state, planId, input.revision);
  if (plan.status !== "已提交" || !plan.submission) throw Error("只有固定提交的计划可确认或退回。");
  const problem = planCanDecide(state, plan); if (problem) throw Error(problem);
  if (!["confirm", "return"].includes(input.action)) throw Error("请选择确认或退回。");
  if (input.submissionRevision !== plan.submission.revision || input.contentHash !== plan.submission.contentHash) throw Error("待审计划已变化，请重新核对差异。");
  if (input.action === "return" && (!input.reason?.trim() || input.reason.trim().length < 4)) throw Error("退回理由至少四个字。");
  const checks = planChecks(state, plan);
  if (input.action === "confirm" && checks.blocking.length) throw Error(checks.blocking.map(issue => issue.message).join("；"));
  if (input.action === "confirm" && JSON.stringify(planSnapshot(state, plan)) !== plan.submission.contentHash) throw Error("目标或工作依据已变化，请作者重新核对并提交。");
  update(s => {
    const target = currentPlan(s, planId, input.revision), time = new Date().toISOString();
    target.history.push({ revision: target.revision, status: target.status, decision: input.action, submission: structuredClone(target.submission), reason: input.reason?.trim() || "确认计划范围", actor: { kind: "Human", id: "H-lin" }, time });
    target.revision++; target.status = input.action === "confirm" ? "已确认" : "已退回";
    if (input.action === "confirm") {
      target.confirmedSnapshot = { ...structuredClone(target.submission.snapshot), revision: target.submission.revision, actor: { kind: "Human", id: "H-lin" }, time };
      target.pendingDiff = [];
    }
    log(s, `${input.action === "confirm" ? "确认" : "退回"}周期计划`, `/plans/${planId}`);
  });
}
export function recordPlanFreeTask(planId, itemId, input) {
  assertWrite();
  const plan = currentPlan(state, planId, input.revision), actor = planActor(state, plan, input.simulateAgent);
  const item = plan.confirmedSnapshot?.items.find(item => item.id === itemId && item.kind === "FREE_TASK");
  if (!item) throw Error("只有已确认基线中的非交付任务可以记录完成证据。");
  if (!input.evidence?.trim()) throw Error("请填写至少一项 URL、文件引用或完成说明。");
  const today = dateInZone(plan.timezone);
  if (today < plan.periodStart || today > plan.periodEnd) throw Error("完成事实须记录在实际发生的周期内，不能回填本期以外的完成时间。");
  const definition = JSON.stringify(planCommitment([item])[0]);
  if (plan.freeTaskFacts.some(fact => fact.itemId === itemId && fact.itemDefinition === definition)) return;
  update(s => {
    const target = currentPlan(s, planId, input.revision);
    target.freeTaskFacts.push({ id: id("PFACT"), kind: "FREE_TASK_EVIDENCE", itemId, itemDefinition: definition, subjectKey: plan.subjectKey, evidence: input.evidence.trim(), actor, time: new Date().toISOString() });
    target.revision++; log(s, "记录周期计划非交付任务完成证据", `/plans/${planId}`);
  });
}

export function generatePeriodicReport(options) {
  assertWrite();
  let reportId;
  update((s) => { reportId = upsertPeriodicReport(s, options, "手动生成", id); log(s, "生成或打开周期报告", `/reports/${reportId}`); });
  return reportId;
}
export function saveReportSchedule(options) {
  assertWrite();
  if (state.role !== "owner") throw Error("定时生成配置需要负责人权限。");
  if (!reportSubjects(state).some((subject) => subject.key === options.subjectKey)) throw Error("请选择当前租户的报告归属。");
  if (!reportCadences.includes(options.cadence)) throw Error("请选择报告周期。");
  dateInZone(options.timezone);
  if (!options.cron?.trim() || options.cron.trim().split(/\s+/).length !== 5) throw Error("请填写五段 Cron 表达式；仅保存本地模拟配置。");
  const existing = options.id ? state.resources.schedules.find((schedule) => schedule.id === options.id) : state.resources.schedules.find((schedule) => schedule.subjectKey === options.subjectKey && schedule.cadence === options.cadence && schedule.timezone === options.timezone && schedule.action?.includes("报告"));
  if (options.id && (!existing || (existing.revision || 1) !== (options.revision || 1))) throw Error("定时配置已变化或不属于当前租户，请重新打开。");
  const subject = reportSubjects(state).find((subject) => subject.key === options.subjectKey);
  const scheduleId = existing?.id || id("SCH");
  saveResource("schedules", { ...(existing || {}), id: scheduleId, name: `${subject.name} · ${options.cadence}生成`, owner: subject.name,
    subjectKey: options.subjectKey, cadence: options.cadence, timezone: options.timezone, cron: options.cron.trim(),
    missed: options.missed || "合并补跑一次", action: "生成周期报告草稿", status: existing?.status || "暂停" });
  return scheduleId;
}

export function humanConfirmedHours(record) {
  if (record.durationKind && record.durationKind !== "Human 工时") return null;
  const value = record.effortState === "confirmed" ? record.confirmedHours
    : !record.effortState && ["已提交", "已接受"].includes(record.status) ? record.hours : null;
  if (!["number", "string"].includes(typeof value) || String(value).trim() === "") return null;
  const hours = Number(value);
  return Number.isFinite(hours) && hours >= 0 && hours <= 24 ? hours : null;
}
export function myEffortTasks(s) {
  return s.work.filter((work) => work.owner === "林予" ||
    [work.team?.coordinatorId, work.team?.receiverId, ...(work.team?.executorIds || []), ...(work.team?.checkerIds || [])].includes("H-lin"));
}
export function saveHumanEffort(input) {
  assertWrite();
  if (!["estimate", "calibrate", "confirm"].includes(input.action)) throw Error("请选择评估、校准或本人确认。");
  const previous = input.id ? state.resources.worklogs.find((record) => record.id === input.id) : null;
  if (input.id && !previous) throw Error("工作量记录不存在于当前租户。");
  if (previous && previous.durationKind && previous.durationKind !== "Human 工时") throw Error("Agent 运行指标只读，不能作为 Human 工作量修改。");
  if (previous && (previous.ownerId ? previous.ownerId !== "H-lin" : previous.owner !== "林予")) throw Error("只能校准与确认本人的工作量，不能替他人确认。");
  if (previous && (previous.revision || 1) !== input.revision) throw Error("工作量修订已变化，请重新打开当前记录。");
  if (!previous && input.action !== "estimate") throw Error("请先从本人任务建立待校准评估。");
  const workId = previous?.work || input.workId, date = previous?.date || input.date;
  if (!(previous ? state.work : myEffortTasks(state)).some((work) => work.id === workId)) throw Error("请选择当前租户内本人参与的任务。");
  if (previous && ((input.workId && input.workId !== previous.work) || (input.date && input.date !== previous.date))) throw Error("已有记录的任务与日期不可改写，请打开对应任务日期的评估。");
  if (typeof date !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(date) || Number.isNaN(Date.parse(date)) || new Date(date).toISOString().slice(0, 10) !== date) throw Error("请选择有效的评估日期。");
  if (!previous && state.resources.worklogs.some((record) => (!record.durationKind || record.durationKind === "Human 工时") &&
    (record.ownerId ? record.ownerId === "H-lin" : record.owner === "林予") && record.work === workId && record.date === date))
    throw Error("该任务与日期已有本人记录，请打开原条目校准。");
  if (input.action === "confirm" && previous.effortState !== "calibrated") throw Error("请先保存校准，再由本人确认。");
  const timezone = input.action === "confirm" ? previous.timezone : (input.timezone ?? previous?.timezone);
  const category = input.action === "confirm" ? previous.category : (input.category ?? previous?.category);
  if (!timezone?.trim()) throw Error("请填写投入日期所属时区。");
  dateInZone(timezone);
  if (!category?.trim()) throw Error("请填写工作量类别。");
  const value = input.action === "estimate" ? input.estimateHours : input.action === "calibrate" ? input.calibratedHours : previous?.calibratedHours;
  if (!["number", "string"].includes(typeof value) || String(value).trim() === "" || !Number.isFinite(Number(value)) || Number(value) < 0 || Number(value) > 24)
    throw Error("小时数必须是 0 至 24 之间的有限数值。");
  if (input.action !== "confirm" && !input.note?.trim()) throw Error("请填写评估或校准依据，不从聊天时长推断投入。");
  if (input.action === "confirm") {
    const confirmed = state.resources.worklogs.filter((record) => record.id !== previous.id && record.date === date &&
      (record.ownerId ? record.ownerId === "H-lin" : record.owner === "林予"))
      .reduce((total, record) => total + (humanConfirmedHours(record) ?? 0), 0);
    if (confirmed + Number(value) > 24) throw Error("本人同一天确认投入合计不能超过 24 小时，请校准重复或重叠投入。");
  }
  const recordId = previous?.id || id("WL");
  update((s) => {
    const index = s.resources.worklogs.findIndex((record) => record.id === recordId);
    const old = index < 0 ? null : s.resources.worklogs[index];
    const record = old ? structuredClone(old) : { id: recordId, name: s.work.find((work) => work.id === workId).title,
      work: workId, date, owner: "林予", ownerId: "H-lin", durationKind: "Human 工时", suggestedHours: null, history: [] };
    record.history ??= [];
    if (old) record.history.push({ ...structuredClone(old), history: undefined, changedBy: "H-lin", changedAt: new Date().toISOString(), action: input.action });
    if (!old) {
      const currentDay = dateInZone(timezone) === date, work = s.work.find(item => item.id === workId);
      record.scopeSnapshot = { recordedAt: new Date().toISOString(), date, timezone,
        subjectKeys: currentDay ? immutableSubjectKeys(s, work.project, ["H-lin"]) : [`tenant:${s.activeTenant}`, "human:H-lin"],
        unknownScopes: currentDay ? [] : ["project", "team"],
        basis: currentDay ? "新建当日投入时固定归属" : "补录历史日期；项目与组织归属未从当前状态推断" };
    }
    record.ownerId = "H-lin";
    record.durationKind = "Human 工时";
    record.timezone = timezone.trim(); record.category = category.trim();
    delete record.reviewedBy; delete record.reviewedAt; delete record.reviewReason;
    record.reviewState = "pending";
    record.revision = old ? (old.revision || 1) + 1 : 1;
    if (!old?.effortState && old?.hours !== undefined) record.legacyHours = old.hours;
    if (input.action === "confirm") {
      record.effortState = "confirmed"; record.status = "已确认";
      record.confirmedHours = Number(value); record.hours = Number(value);
      record.confirmedBy = "H-lin"; record.confirmedAt = new Date().toISOString();
    } else {
      record.effortState = input.action === "estimate" ? "estimate" : "calibrated";
      record.status = "待校准";
      if (input.action === "estimate") { record.estimateHours = Number(value); delete record.calibratedHours; }
      else record.calibratedHours = Number(value);
      record.note = input.note.trim();
      delete record.hours; delete record.confirmedHours; delete record.confirmedBy; delete record.confirmedAt;
    }
    if (index < 0) s.resources.worklogs.unshift(record); else s.resources.worklogs[index] = record;
    log(s, `${input.action === "confirm" ? "本人确认" : input.action === "calibrate" ? "校准" : "评估"}工作量 · ${record.name} · ${record.date}`, `/worklogs/${recordId}`);
  });
  return recordId;
}

export function reviewHumanEffort(recordId, action, reason, revision) {
  assertWrite();
  if (state.role !== "owner") throw Error("工作量审核需要负责人权限。");
  const record = state.resources.worklogs.find((item) => item.id === recordId);
  if (!record) throw Error("工作量记录不存在于当前租户。");
  if ((record.revision || 1) !== revision) throw Error("工作量修订已变化，请重新打开当前记录。");
  if (!["accept", "return"].includes(action)) throw Error("请选择审核通过或退回。");
  if (humanConfirmedHours(record) === null || record.reviewState === "accepted" || record.status === "已接受") throw Error("仅可审核本人已确认或历史已提交的待审核记录。");
  if (!reason?.trim()) throw Error("请填写审核或退回原因。");
  update((s) => {
    const current = s.resources.worklogs.find((item) => item.id === recordId);
    current.history ??= [];
    current.history.push({ ...structuredClone(current), history: undefined, changedBy: "H-lin", changedAt: new Date().toISOString(), action, reason: reason.trim() });
    current.revision = (current.revision || 1) + 1;
    current.reviewState = action === "accept" ? "accepted" : "returned";
    current.reviewedBy = "H-lin"; current.reviewedAt = new Date().toISOString(); current.reviewReason = reason.trim();
    current.status = action === "accept" ? "已接受" : "已退回";
    if (action === "return") {
      if (!current.effortState) current.legacyHours = current.hours;
      current.effortState = "returned";
      delete current.hours; delete current.confirmedHours; delete current.confirmedBy; delete current.confirmedAt;
    }
    log(s, `${action === "accept" ? "审核通过" : "退回"}工作量 · ${current.name}`, `/worklogs/${recordId}`);
  });
}

const goalMetricFields = ["metric", "baseline", "actual", "target"];
function goalOwner(s, goal) {
  const people = [...(s.organization?.members || []).filter(person => person.kind === "Human").map(person => ({ id: person.id, kind: "Human", name: person.name })),
    ...(s.agents || []).map(agent => ({ id: agent.id, kind: "Agent", name: agent.name }))];
  const matches = goal.ownerId && goal.ownerKind ? people.filter(person => person.id === goal.ownerId && person.kind === goal.ownerKind) : people.filter(person => person.name === goal.owner);
  return matches.length === 1 ? matches[0] : null;
}
export function goalWriteProblem(s, goal, editing = false) {
  if (!["owner", "executor"].includes(s.role) || !s.organization?.members.some(person => person.id === "H-lin" && person.kind === "Human")) return "当前身份无权修改目标。";
  if (s.offline) return "离线中，请恢复连接后操作。";
  if (s.role === "owner") return null;
  const owner = goalOwner(s, goal);
  if (owner?.kind !== "Human" || owner.id !== "H-lin") return "只能修改本人负责的目标。";
  if (editing && goal.status !== "草稿") return "成员只能编辑目标草稿，请由负责人发起正式变更。";
  return null;
}
export function goalTransitionProblem(s, goal, status) {
  if (!goal) return "目标不存在。";
  if (closedGoalStates.includes(goal.status)) return "已关闭目标只读。";
  if (s.offline) return "离线中，请恢复连接后重新确认。";
  if (!["owner", "executor"].includes(s.role)) return "当前身份无权操作目标。";
  if (!["待审核", "进行中", "草稿"].includes(status)) return "不支持的目标动作。";
  if (status === "待审核") {
    if (goal.status !== "草稿") return "仅草稿目标可以提交审核。";
    const permission = goalWriteProblem(s, goal, true);
    if (permission) return permission;
    if (!goalOwner(s, goal)) return "请选择当前租户中可识别的目标负责人。";
    if (structuredGoal(goal)) {
      try { normalizeGoal(s, goal); validateGoalAlignments(s, goal, true); } catch (error) { return error.message; }
      return null;
    }
    if (["name", "owner", "cycle", "metric", "baseline", "target"].some(key => !String(goal[key] ?? "").trim())) return "请补齐名称、负责人、周期、指标、基线和目标值后提交。";
    if (String(goal.baseline).trim() === String(goal.target).trim()) return "目标值必须与基线不同。";
    return null;
  }
  if (s.role !== "owner" || !s.organization?.members.some(person => person.id === "H-lin" && person.kind === "Human")) return "目标审核需要有管理权限的 Human。";
  if (goal.status !== "待审核") return "请先提交当前目标修订，再执行审核。";
  if (status === "进行中" && (goal.submission?.revision !== (goal.revision || 1) || !goal.submission?.snapshot)) return "缺少当前修订的固定提交记录，请退回修改后重新提交。";
  if (status === "进行中" && structuredGoal(goal)) {
    try { normalizeGoal(s, goal); validateGoalAlignments(s, goal, true); } catch (error) { return error.message; }
  }
  return null;
}
export function goalProgress(goal) {
  if (structuredGoal(goal)) { const value = goalResult(goal).progress; return value === null ? null : value * 100; }
  const calibration = goal.progressCalibration;
  if (calibration && (calibration.metricRevision !== (goal.metricRevision || 1) || goalMetricFields.some(key => calibration.metric?.[key] !== String(goal[key] ?? "")))) return null;
  const value = calibration ? calibration.progress : goal.progress;
  if (value === null || value === undefined || value === "") return null;
  const progress = Number(value);
  return Number.isFinite(progress) && progress >= 0 && progress <= 100 ? progress : null;
}
export function calibrateGoalProgress(goalId, input) {
  assertWrite();
  if (structuredGoal(state.resources.goals.find(item => item.id === goalId))) {
    const goal = mutableStructuredGoal(goalId, input);
    if (input.definitionRevision !== goal.definitionRevision) throw Error("指标定义已变更，请重新校准。");
    const progress = goalNumber(input.progress, "人工判断");
    if (progress < 0 || progress > 100 || !input.reason?.trim()) throw Error("人工判断须在 0–100 之间并填写依据。");
    update(s => {
      const current = s.resources.goals.find(item => item.id === goalId); goalAudit(current, input.reason.trim());
      current.manualAssessment = { progress, reason: input.reason.trim(), definitionRevision: current.definitionRevision, actorId: "H-lin", time: new Date().toISOString() };
      current.review = null;
      if (current.status === "待审核") { current.status = "草稿"; current.submission = null; current.approval = null; }
      log(s, `记录人工进度判断 · ${current.name}`, `/goals/${goalId}`);
    });
    return;
  }
  const goal = state.resources.goals.find(item => item.id === goalId);
  if (!goal) throw Error("目标不存在。");
  const permission = goalWriteProblem(state, goal);
  if (permission) throw Error(permission);
  if (input.metricRevision !== (goal.metricRevision || 1)) throw Error("指标已变更，请重新打开当前指标修订后校准。");
  if (input.revision !== (goal.revision || 1)) throw Error("记录已变更，请重新打开后校准。");
  if (!String(input.reason || "").trim()) throw Error("请填写手工校准依据。");
  const progress = Number(input.progress);
  if (String(input.progress ?? "").trim() === "" || !Number.isFinite(progress) || progress < 0 || progress > 100) throw Error("进度必须是 0 到 100 之间的数字。");
  update(s => {
    const current = s.resources.goals.find(item => item.id === goalId);
    current.history ??= [];
    current.history.push({ ...structuredClone(current), history: undefined });
    current.progress = progress;
    current.progressCalibration = { progress, reason: input.reason.trim(), metricRevision: current.metricRevision || 1,
      metric: Object.fromEntries(goalMetricFields.map(key => [key, String(current[key] ?? "")])), actor: "林予", time: new Date().toISOString() };
    current.revision = (current.revision || 1) + 1;
    if (current.status === "待审核") {
      current.status = "草稿";
      current.submission = null;
      current.approval = null;
    }
    log(s, `手工校准目标进度 ${current.name}：${progress}%`, `/goals/${goalId}`);
  });
}

export function saveStructuredGoal(input) {
  assertWrite();
  const previous = state.resources.goals.find(goal => goal.id === input.id);
  if (previous && (input.revision || 1) !== (previous.revision || 1)) throw Error("目标已变更，请重新打开当前版本后编辑。");
  const permission = goalWriteProblem(state, previous || input, true);
  if (permission) throw Error(permission);
  if (previous && closedGoalStates.includes(previous.status)) throw Error("已关闭目标只读，请使用结转创建新周期草稿。");
  const owner = goalOwner(state, input);
  if (!owner) throw Error("请选择当前租户可识别的负责人。");
  if (state.role !== "owner" && (owner.kind !== "Human" || owner.id !== "H-lin" || input.subjectKey !== "human:H-lin")) throw Error("成员只能创建或编辑本人归属的目标，不能转移负责人。");
  const normalized = normalizeGoal(state, { ...input, id: previous?.id || id("G"), ownerId: owner.id, ownerKind: owner.kind, owner: owner.name });
  if (["human", "agent"].includes(normalized.subjectKey.split(":")[0]) && normalized.subjectKey !== `${owner.kind === "Human" ? "human" : "agent"}:${owner.id}`) throw Error("成员目标的归属必须与负责人一致。");
  if (structuredGoal(previous) && state.resources.goals.some(child => child.id !== previous.id && !closedGoalStates.includes(child.status) && child.alignments?.some(edge => edge.targetGoalId === previous.id && !normalized.krs.some(kr => kr.id === edge.targetKrId)))) throw Error("仍有开放目标引用被移除的 KR，请先调整对齐关系。");
  const scopeChanged = !structuredGoal(previous) || ["subjectKey", "periodStart", "periodEnd", "timezone"].some(key => normalized[key] !== previous[key]);
  const definitionRevision = (previous?.definitionRevision || 0) + 1;
  const krs = normalized.krs.map(kr => {
    const old = previous?.krs?.find(item => item.id === kr.id);
    const changed = scopeChanged || !old || JSON.stringify(krDefinition(old)) !== JSON.stringify(krDefinition(kr));
    return { ...kr, definitionRevision: (old?.definitionRevision || 0) + (changed ? 1 : 0), observations: structuredClone(old?.observations || []), currentObservationId: changed ? null : old?.currentObservationId || null };
  });
  const record = { ...goalDefinition({ ...normalized, krs }), id: normalized.id, owner: owner.name,
    definitionRevision, revision: (previous?.revision || 0) + 1, status: "草稿", krs, submission: null, approval: null,
    cycle: `${normalized.periodStart} — ${normalized.periodEnd}`, review: null,
    history: [...(previous?.history || []), ...(previous ? [{ ...structuredClone(previous), history: undefined }] : [])],
    legacy: previous?.legacy || (previous && !structuredGoal(previous) ? { scalar: { ...structuredClone(previous), history: undefined }, note: "旧数值仅供核对，尚未形成带观察时间的 KR 事实。" } : null),
    draftOrigin: previous?.draftOrigin || (input.draftOrigin?.simulated === true ? { kind: "Agent", simulated: true, agentId: state.agents.some(agent => agent.id === input.draftOrigin.agentId) ? input.draftOrigin.agentId : null, recordedBy: "H-lin" } : null),
    manualAssessment: null, carriedFrom: previous?.carriedFrom || null, reminders: structuredClone(previous?.reminders || []) };
  update(s => {
    const index = s.resources.goals.findIndex(goal => goal.id === record.id);
    if (index < 0) s.resources.goals.unshift(record); else s.resources.goals[index] = record;
    log(s, `保存目标定义 · ${record.name}`, `/goals/${record.id}`);
  });
  return record.id;
}
function mutableStructuredGoal(goalId, input, manage = false) {
  const goal = state.resources.goals.find(item => item.id === goalId);
  if (!structuredGoal(goal)) throw Error("请先将旧目标转换为结构化 KR。");
  const problem = goalWriteProblem(state, goal);
  if (problem) throw Error(problem);
  if (manage && state.role !== "owner") throw Error("此决定需要有管理权限的 Human。");
  if (input.revision !== goal.revision) throw Error("目标已变更，请重新打开当前修订。");
  if (closedGoalStates.includes(goal.status)) throw Error("已关闭目标只读。");
  return goal;
}
function goalAudit(goal, reason) {
  goal.history ??= [];
  goal.history.push({ ...structuredClone(goal), history: undefined, reason, actor: "林予", time: new Date().toISOString() });
  goal.revision++;
}
export function recordGoalObservation(goalId, krId, input) {
  assertWrite();
  const goal = mutableStructuredGoal(goalId, input), kr = goal.krs.find(item => item.id === krId);
  if (!kr) throw Error("KR 不存在。");
  if (kr.source !== "MANUAL") throw Error("自动指标请刷新事实，不能手填覆盖。");
  const value = goalNumber(input.value, "观察值");
  if (!input.reason?.trim()) throw Error("请填写观察依据。");
  if (!input.observedAt || !Number.isFinite(Date.parse(input.observedAt))) throw Error("请填写有效观察时间。");
  const observedAt = new Date(input.observedAt).toISOString(), day = dateInZone(goal.timezone, new Date(observedAt));
  if (day < goal.periodStart || day > goal.periodEnd || Date.parse(observedAt) > Date.now()) throw Error("观察时间须处于本周期内且不能晚于当前时间。");
  const evidence = (input.evidence || []).map(ref => goalEvidence(state, ref));
  if ((kr.evidenceRequired || input.agentDraft) && !evidence.length) throw Error("此观察必须提供佐证；Agent 事实不能没有证据。");
  if (input.agentDraft && (goal.ownerKind !== "Agent" || state.role !== "owner")) throw Error("只能由负责人模拟该 Agent 的授权事实草案。");
  update(s => {
    const current = s.resources.goals.find(item => item.id === goalId), target = current.krs.find(item => item.id === krId);
    goalAudit(current, input.reason.trim());
    const observation = { id: id("OBS"), value, observedAt, recordedAt: new Date().toISOString(), definitionRevision: target.definitionRevision,
      availability: "available", reason: input.reason.trim(), evidence, actorId: input.agentDraft ? goal.ownerId : "H-lin", actorKind: input.agentDraft ? "Agent" : "Human", recordedBy: "H-lin", source: input.agentDraft ? "本地 Agent 草案模拟" : "人工观察" };
    target.observations.push(observation); target.currentObservationId = observation.id;
    current.review = null;
    if (current.status === "待审核") { current.status = "草稿"; current.submission = null; current.approval = null; }
    log(s, `记录 KR 观察 · ${target.title}`, `/goals/${goalId}`);
  });
}
export function refreshGoalMetrics(goalId, input) {
  assertWrite(); const goal = mutableStructuredGoal(goalId, input);
  const automatic = goal.krs.filter(kr => kr.source === "AUTO");
  if (!automatic.length) throw Error("此目标没有自动指标。");
  const observations = automatic.map(kr => ({ krId: kr.id, ...readGoalMetric(state, goal, kr) }));
  update(s => {
    const current = s.resources.goals.find(item => item.id === goalId); goalAudit(current, "刷新本地可核对事实");
    for (const result of observations) {
      const kr = current.krs.find(item => item.id === result.krId);
      const observation = { ...result, id: id("OBS"), definitionRevision: kr.definitionRevision, recordedAt: new Date().toISOString(), actorKind: "Human", actorId: "H-lin", source: "本地事实读取", reason: "刷新固定事实" };
      kr.observations.push(observation); kr.currentObservationId = observation.id;
    }
    current.review = null;
    if (current.status === "待审核") { current.status = "草稿"; current.submission = null; current.approval = null; }
    log(s, `刷新目标指标 · ${current.name}`, `/goals/${goalId}`);
  });
}
export function checkGoalRisks(goalId, input) {
  assertWrite(); const goal = mutableStructuredGoal(goalId, input), risks = goalRisks(state, goal);
  update(s => {
    const current = s.resources.goals.find(item => item.id === goalId);
    current.reminders ??= [];
    const active = new Set(risks.map(risk => `${current.definitionRevision}:${risk.type}`));
    for (const reminder of current.reminders) if (!active.has(reminder.key)) reminder.status = "已解除";
    for (const risk of risks) {
      const key = `${current.definitionRevision}:${risk.type}`, existing = current.reminders.find(item => item.key === key);
      if (existing) { existing.message = risk.message; existing.status = "待处理"; }
      else current.reminders.push({ ...risk, id: id("RISK"), key, status: "待处理", time: new Date().toISOString(), target: `/goals/${goalId}`, delivery: "本地提醒，未发送外部消息" });
    }
    current.revision++;
    log(s, `检查目标风险 · ${current.name}`, `/goals/${goalId}`);
  });
}
export function goalPeriodCommand(goalId, action, input) {
  assertWrite();
  const existing = state.resources.goals.find(item => item.id === goalId);
  if (action === "carry" && existing?.carryover?.goalId) {
    const permission = goalWriteProblem(state, existing);
    if (permission || state.role !== "owner") throw Error(permission || "结转需要有权 Human 确认。");
    return existing.carryover.goalId;
  }
  const goal = action === "carry" ? existing : mutableStructuredGoal(goalId, input, ["confirm", "return", "cancel"].includes(action));
  if (!structuredGoal(goal)) throw Error("请先转换结构化目标。");
  if (action === "carry") {
    const permission = goalWriteProblem(state, goal);
    if (permission) throw Error(permission);
    if (state.role !== "owner" || input.revision !== goal.revision || !["已达成", "未达成"].includes(goal.status) || !goal.review?.confirmedAt) throw Error("结转需要有权 Human 确认的期末结果与当前修订。");
    const period = goalPeriod(goal.periodKind, input.periodStart, goal.timezone);
    if (period.periodStart <= goal.periodEnd) throw Error("结转必须选择后续周期。");
    let nextId;
    update(s => {
      const current = s.resources.goals.find(item => item.id === goalId); nextId = id("G");
      const next = { ...goalDefinition(current), id: nextId, owner: current.owner, name: input.name?.trim() || current.name, ...period,
        revision: 1, definitionRevision: 1, status: "草稿", submission: null, approval: null, review: null, history: [], alignments: [], reminders: [],
        carriedFrom: { goalId, definitionRevision: current.definitionRevision, reviewId: current.review.id },
        krs: current.krs.map(kr => ({ ...krDefinition(kr), id: id("KR"), carriedFromKrId: kr.id, baseline: null, target: null, definitionRevision: 1, observations: [], currentObservationId: null })) };
      current.carryover = { goalId: nextId, time: new Date().toISOString(), actorId: "H-lin" }; current.revision++;
      s.resources.goals.unshift(next); log(s, `结转目标为新周期草稿 · ${current.name}`, `/goals/${nextId}`);
    });
    return nextId;
  }
  if (action === "cancel") {
    if (!input.reason?.trim()) throw Error("取消须填写理由。");
  } else if (!["进行中", "存在风险"].includes(goal.status)) throw Error("请先批准发布当前目标定义，再进行期末复核。");
  if (action === "submit") {
    if (!input.summary?.trim() || !input.reason?.trim() || !["结转", "停止", "升级"].includes(input.disposition)) throw Error("请填写自评、偏差依据和未完成处置。");
    if (dateInZone(goal.timezone) <= goal.periodEnd && goalResult(goal).progress !== 1) throw Error("周期结束或有效指标全部达成后才能提交期末复核。");
    if (goal.review?.status === "待审核") throw Error("已有待审核的期末自评。");
  } else if (["confirm", "return"].includes(action)) {
    if (goal.review?.status !== "待审核" || !input.reason?.trim()) throw Error("请先提交期末自评，并填写复核依据。");
    if (action === "confirm" && !["已达成", "未达成"].includes(input.result)) throw Error("请选择期末结果。");
    if (action === "confirm" && input.result === "已达成" && goalResult(goal).progress !== 1) throw Error("缺少有效达成观察，不能因交付完成而确认业务目标达成。");
  } else if (action !== "cancel") throw Error("不支持的期末动作。");
  update(s => {
    const current = s.resources.goals.find(item => item.id === goalId); goalAudit(current, input.reason.trim());
    if (action === "submit") current.review = { id: id("GR"), status: "待审核", definitionRevision: current.definitionRevision,
      snapshot: structuredClone(goalDefinition(current)), observations: current.krs.map(kr => ({ krId: kr.id, observation: structuredClone(krObservation(current, kr)) })),
      summary: input.summary.trim(), reason: input.reason.trim(), disposition: input.disposition, submittedBy: "H-lin", submittedAt: new Date().toISOString() };
    if (action === "return") current.review = { ...current.review, status: "已退回", reviewReason: input.reason.trim(), reviewerId: "H-lin" };
    if (action === "confirm") { current.review = { ...current.review, status: "已确认", reviewReason: input.reason.trim(), reviewerId: "H-lin", confirmedAt: new Date().toISOString(), result: input.result }; current.status = input.result; current.closedAt = new Date().toISOString(); }
    if (action === "cancel") { current.status = "已取消"; current.closedAt = new Date().toISOString(); }
    log(s, `目标期末动作 · ${action} · ${current.name}`, `/goals/${goalId}`);
  });
}

function validateDefectDetails(s, details, project) {
  const affected = s.work.find(work => work.id === details.affectedWorkId && work.workKind !== "BUG");
  if (!affected || affected.project !== project) throw Error("请选择同一项目的受影响工作。");
  if (!details.affectedVersion?.trim()) throw Error("请填写准确的影响版本。");
  for (const field of ["severity", "priority", "steps", "expected", "actual"]) if (!details[field]?.trim()) throw Error("请补全严重度、优先级、复现步骤、期望与实际结果。");
}
export function saveDefect(record) {
  assertWrite();
  const work = state.work.find(work => work.id === record.id && work.workKind === "BUG");
  if (!work) throw Error("请通过会话草案确认创建缺陷工作。");
  if (!work.defect) throw Error("缺陷专业数据缺失，请先修复迁移记录。");
  if (["已关闭", "已取消"].includes(defectStatus(work))) throw Error("终态缺陷不可改写；请创建关联的新缺陷。");
  if ((record.revision || 1) !== (work.defect.revision || 1)) throw Error("缺陷已变化，请重新打开当前修订。");
  const details = { ...work.defect, ...Object.fromEntries(defectFields.map(key => [key, record[key] ?? work.defect[key] ?? ""])),
    affectedWorkId: record.work ?? record.affectedWorkId ?? work.defect.affectedWorkId,
    affectedVersion: record.version ?? record.affectedVersion ?? work.defect.affectedVersion };
  validateDefectDetails(state, details, work.project);
  update(s => {
    const target = s.work.find(item => item.id === work.id);
    const { history, ...previous } = target.defect;
    target.defect = { ...details, revision: (target.defect.revision || 1) + 1, history: [...(history || []), { ...structuredClone(previous), reason: "修订缺陷专业信息", time: new Date().toISOString(), actor: "林予" }], stage: null };
    delete target.defect.repairVersion; delete target.defect.verification;
    target.title = record.name?.trim() || target.title;
    target.requirements = [details.expected]; target.status = workRework(target).hold ? "等待" : "进行中"; target.qa = false; target.accepted = false;
    log(s, `修订缺陷工作 · ${target.title}`, `/p/${target.project}/work-items/${target.id}`);
  });
}
export function defectCommand(workId, status, reason, actionLabel = "", receipt) {
  assertWrite();
  const work = state.work.find(item => item.id === workId && item.workKind === "BUG");
  if (!work) throw Error("缺陷工作不存在。");
  if (!work.defect) throw Error("缺陷专业数据缺失，请先修复迁移记录。");
  const record = defectRecords(state).find(item => item.id === workId);
  if (!reason?.trim()) throw Error("请填写修复或验证依据。");
  if (receipt?.revision !== undefined && receipt.revision !== (work.defect.revision || 1)) throw Error("缺陷已变化，请重新核对。");
  if (["已关闭", "已取消"].includes(record.status)) throw Error("终态缺陷不可改写；内部回归请创建关联的新缺陷。");
  if (workRework(work).hold) throw Error("返工已停线，请先由 Human 作出决定。");
  if (dependencyProblems(state, work).length) throw Error("请先处理失效输入依赖，再推进缺陷修复或验证。");
  let verification;
  if (status === "待验证") {
    if (record.status !== "待修复") throw Error("只有待修复缺陷可以提交修复。");
    const affected = state.work.find(item => item.id === record.work);
    if (!affected || record.version !== `v${affected.version}`) throw Error("请先将缺陷关联到当前待验证的准确工作版本。");
    if (!state.organization.members.some(person => person.kind === "Human" && person.name === work.owner)) throw Error("请先在工作团队补充有效 Human 负责人。");
  } else if (status === "已关闭" || actionLabel === "验证失败并重开") {
    if (status === "已关闭" && state.role !== "owner") throw Error("关闭缺陷需要 Human 负责人决定。");
    if (record.status !== "待验证") throw Error("请先提交修复，再选择独立验证运行。");
    verification = defectVerificationRuns(state, record).find(run => run.id === receipt?.runId);
    if (!verification) throw Error("请选择修复提交后、当前版本且关联此缺陷的独立验证证据；自测、旧版和已失效运行不可用。");
    if (verification.status !== (status === "已关闭" ? "通过" : "失败")) throw Error("验证结论与缺陷动作不一致。");
  } else throw Error("不支持的缺陷动作。");
  update(s => {
    const target = s.work.find(item => item.id === workId), details = target.defect;
    const { history, ...previous } = details;
    details.history = [...(history || []), { ...structuredClone(previous), status: record.status, reason, actor: "林予", time: new Date().toISOString() }];
    details.revision = (details.revision || 1) + 1;
    if (status === "待验证") {
      details.repairRevision = (details.repairRevision || 0) + 1;
      details.repairWorkVersion = target.version;
      details.repairVersion = details.affectedVersion; details.repairReason = reason; details.stage = null;
      details.repairExecutorIds = [...(target.team?.executorIds || (target.agentId ? [`agent:${target.agentId}`] : []))];
      delete details.verification; target.status = "待验收"; target.qa = false; target.accepted = false;
    } else {
      details.verification = structuredClone(verification); details.closeReason = status === "已关闭" ? reason : "";
      details.stage = status === "已关闭" ? "CLOSED" : null;
      if (status === "已关闭") details.closure = { actorKind: "Human", actorId: "H-lin", time: new Date().toISOString(), workVersion: target.version, repairRevision: details.repairRevision, runId: verification.id, reason };
      target.status = status === "已关闭" ? "已完成" : "进行中";
      if (status === "已关闭") recordAcceptanceFact(s, target);
      target.qa = status === "已关闭"; target.accepted = status === "已关闭";
      if (status !== "已关闭") delete details.repairVersion;
    }
    log(s, `${actionLabel || status} · ${target.title} · ${reason}`, `/p/${target.project}/work-items/${target.id}`);
  });
}

export function saveResource(kind, record) {
  assertWrite();
  if (kind === "plans") throw Error("周期计划请通过专用草稿、提交与确认流程更新。");
  if (kind === "goals" && (structuredGoal(record) || structuredGoal(state.resources.goals.find(item => item.id === record.id)))) return saveStructuredGoal(record);
  if (kind === "goals") {
    const previous = state.resources.goals.find(goal => goal.id === record.id);
    const permission = goalWriteProblem(state, previous || { owner: record.owner, status: "草稿" }, true);
    if (permission) throw Error(permission);
    const owner = goalOwner(state, previous && previous.owner === record.owner ? previous : { owner: record.owner });
    if (!owner) throw Error("请选择当前租户中唯一可识别的 Human 或 Agent 负责人。");
    if (state.role !== "owner" && (owner.kind !== "Human" || owner.id !== "H-lin")) throw Error("成员不能转移目标负责人，请由负责人处理。");
    record = { ...record, ownerId: owner.id, ownerKind: owner.kind, owner: owner.name };
  }
  if (kind === "defects") return saveDefect(record);
  if (kind === "testplans") return saveTestPlan(record);
  if (kind === "testreports") throw Error("测试报告是固定快照，请创建新的执行批次。");
  if (kind === "invitations") throw Error("邀请请通过组织与成员中的范围化邀请流程创建或撤销。");
  if (kind === "worklogs") throw Error("Human 工作量请从本人任务评估与校准；Agent 运行指标只读。");
  if (kind === "reports") {
    const previous = state.resources.reports.find((item) => item.id === record.id);
    const permission = reportEditProblem(state, previous || record, { simulateAgent: true });
    if (permission) throw Error(permission);
    const key = validatePeriodicReport(state, record);
    if (!previous && !key) throw Error("请通过生成 / 打开周期报告选择明确归属与周期。");
    if (previous && (previous.revision || 1) !== (record.revision || 1)) throw Error("报告已变化，请重新打开当前修订。");
    const previousKey = previous && periodicReportKey(state, previous);
    if (previousKey && (previousKey !== key || previous.timezone !== record.timezone || previous.periodEnd !== record.periodEnd)) throw Error("已有报告的归属、周期与时区不可改写，请生成或打开对应周期。");
    if (key && state.resources.reports.some((item) => item.id !== record.id && periodicReportKey(state, item) === key)) throw Error("此归属与周期已有报告，请打开原报告修订。");
    if (key) record = { ...record, owner: reportOwner(state, record.subjectKey), subjectName: reportSubjects(state).find((subject) => subject.key === record.subjectKey).name };
    const author = reportAuthor(state, previous || record);
    const editable = ["id", "name", "summary", "risk", "claimedCompleted", "references", "subjectKey", "cadence", "periodStart", "periodEnd", "timezone", "cycle"];
    record = { ...(previous || {}), ...Object.fromEntries(editable.filter(key => key in record).map(key => [key, record[key]])),
      owner: author.name, authorId: author.id, authorKind: author.kind,
      status: "草稿", submission: null, approval: null };
    if (!previous) { record.revision = 1; record.history = []; }
  }
  if (kind === "schedules" && (record.action?.includes("报告") || record.action?.includes("日报"))) {
    if (state.role !== "owner") throw Error("报告定时配置需要负责人权限。");
    if (!reportSubjects(state).some((subject) => subject.key === record.subjectKey)) throw Error("请选择当前租户的报告归属。");
    if (!reportCadences.includes(record.cadence)) throw Error("请选择报告周期。");
    dateInZone(record.timezone);
  }
  if (kind === "tests") {
    assertVerificationMutable(state, record.work, record.version);
    const previous = state.resources.tests.find(test => test.id === record.id);
    if (previous) assertVerificationMutable(state, previous.work, previous.version);
    assertTestDefect(state, record);
    const work = state.work.find(work => work.id === record.work);
    if (!work) throw Error("请选择当前租户的关联工作。");
    const requirementRefs = record.requirementRefs || [];
    if (!requirementRefs.length || requirementRefs.some(ref => !work.requirements.includes(ref))) throw Error("请选择当前工作的有效验收条款。");
    record = { ...record, requirementRefs };
  }
  if (kind === "templates") throw Error("工作流模板请通过画布草案编辑与发布。");
  if (kind === "deliveries") throw Error("固定交付包不能编辑，请从工作形成新版本交付。");
  update((s) => {
    const index = s.resources[kind].findIndex((x) => x.id === record.id);
    const previousGoal = kind === "goals" && index >= 0 ? structuredClone(s.resources.goals[index]) : null;
    if (previousGoal && (record.revision || 1) !== (previousGoal.revision || 1)) throw Error("目标已变更，请重新打开当前版本后编辑。");
    if (kind === "tests" && index >= 0) preserveTestRun(s, s.resources.tests[index]);
    const retainedRuns = kind === "tests" && index >= 0 ? structuredClone(s.resources.tests[index].runs || []) : [];
    if (index < 0)
      s.resources[kind].unshift({
        ...record,
        id: record.id || id(kind.toUpperCase()),
      });
    else {
      const previous = s.resources[kind][index];
      s.resources[kind][index] = {
        ...record,
        revision: (previous.revision || 1) + 1,
        history: [
          ...(previous.history || []),
          { ...previous, history: undefined },
        ],
      };
      if (["goals", "reports", "templates", "models"].includes(kind))
        s.resources[kind][index].status = "草稿";
    }
    if (kind === "goals") {
      const saved = index < 0 ? s.resources.goals[0] : s.resources.goals[index];
      saved.status = "草稿";
      saved.revision = previousGoal ? (previousGoal.revision || 1) + 1 : 1;
      saved.submission = null;
      saved.approval = null;
      if (!previousGoal) saved.history = [];
      const changed = previousGoal && goalMetricFields.some(key => String(previousGoal[key] ?? "") !== String(saved[key] ?? ""));
      saved.metricRevision = previousGoal ? (previousGoal.metricRevision || 1) + (changed ? 1 : 0) : 1;
      saved.progress = previousGoal && !changed ? previousGoal.progress ?? null : null;
      saved.progressCalibration = previousGoal && !changed ? structuredClone(previousGoal.progressCalibration || null) : null;
    }
    if (kind === "tests") {
      const saved =
        s.resources.tests.find((t) => t.id === record.id) ||
        s.resources.tests[0];
      saved.runs = retainedRuns;
      delete saved.currentRun;
      saved.status = "待执行";
      saved.actual = "";
      saved.evidence = "";
      syncQuality(s, saved.work, saved.version);
    }
    log(s, `${index < 0 ? "创建" : "更新"}${record.name}`, `/${kind}`);
  });
}
// Reports resolve local facts by typed identity. Shared leaves are counted once,
// while every selected revision is checked (including duplicate references).
export function reportEvidence(s, report) {
  const errors = [], warnings = [], sources = [], seen = new Set();
  const totals = { human: 0, agent: 0, tool: 0, completed: 0, works: 0, unknownEffortIds: [] };
  const validDate = (v) => typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v)
    && !Number.isNaN(Date.parse(v)) && new Date(v).toISOString().slice(0, 10) === v;
  const issue = (list, text) => { if (!list.includes(text)) list.push(text); };
  const period = (r) => {
    const valid = validDate(r.periodStart) && validDate(r.periodEnd) && r.periodStart <= r.periodEnd;
    if (!valid) issue(errors, `${r.name || r.id || "报告"}：请填写有效且顺序正确的报告起止日期。`);
    return valid;
  };
  const rootPeriod = period(report);
  const rootFacts = periodicReportFacts(s, report);
  const selectedFactIds = new Set();
  const visit = (current, ancestors) => {
    const localWorks = new Set(), localFactIds = new Set();
    const currentFacts = periodicReportFacts(s, current);
    const currentPeriod = current === report ? rootPeriod : period(current);
    const references = current.references;
    let explicitPeriod = false;
    try { explicitPeriod = Boolean(validatePeriodicReport(s, current)); } catch { /* Invalid identity is never proof of an empty period. */ }
    if (!Array.isArray(references) || (!references.length && !explicitPeriod)) {
      issue(errors, `${current.name || current.id || "报告"}：尚未选择可核对的事实引用。`);
      return { works: localWorks, factIds: localFactIds };
    }
    if (!references.length && currentFacts.unknownWorkIds.length) issue(warnings, "周期历史接受数据不完整；无引用不代表实际完成数为零。");
    for (const ref of references) {
      const kind = ref?.kind;
      const collection = kind === "work" ? s.work : kind === "worklog" ? s.resources.worklogs
        : kind === "report" ? s.resources.reports : [];
      const value = collection.find((item) => item.id === ref?.id);
      const key = `${kind}:${ref?.id}`;
      if (!value) {
        issue(errors, `来源 ${ref?.id || "未知"} 不存在于当前租户，或类型无效。`);
        continue;
      }
      const version = kind === "work" ? value.version : value.revision || 1;
      const acceptanceFacts = kind === "work" ? currentFacts.facts.filter(fact => fact.workId === value.id && fact.version === ref.version) : [];
      const fixedAcceptance = acceptanceFacts.length > 0 && (ref.acceptanceFactIds?.length > 0 ? ref.acceptanceFactIds.every(id => acceptanceFacts.some(fact => fact.id === id)) : ref.version === version);
      if (ref.acceptanceFactIds?.length && !fixedAcceptance) issue(errors, `${value.id}：接受事实不属于所选版本、主体或周期。`);
      if (ref.version !== version && !fixedAcceptance) issue(errors, `${value.title || value.name}：引用版本已变化，请重新选择当前版本。`);
      if (kind === "report" && ancestors.has(value.id)) {
        issue(errors, `${value.name}：报告引用形成循环。`);
        continue;
      }
      if (kind === "work") { localWorks.add(value.id); for (const fact of acceptanceFacts) { localFactIds.add(fact.id); selectedFactIds.add(fact.id); } }
      if (kind === "worklog" && currentPeriod && validDate(value.date)
        && (value.date < current.periodStart || value.date > current.periodEnd))
        issue(errors, `${value.name}：记录日期不在${current.name || "报告"}周期内。`);
      if (kind === "report") {
        if (period(value) && currentPeriod && (value.periodStart < current.periodStart || value.periodEnd > current.periodEnd))
          issue(errors, `${value.name}：下级报告周期超出当前报告。`);
        const child = visit(value, new Set([...ancestors, value.id]));
        for (const workId of child.works) localWorks.add(workId);
        for (const factId of child.factIds) localFactIds.add(factId);
      }
      if (seen.has(key)) {
        if (kind === "work") { const prior = sources.find(source => source.kind === kind && source.id === value.id); prior.acceptanceFacts = [...new Map([...(prior.acceptanceFacts || []), ...structuredClone(acceptanceFacts)].map(fact => [fact.id, fact])).values()]; }
        continue;
      }
      seen.add(key);
      const source = { kind, id: value.id, name: value.title || value.name, version,
        selectedVersion: ref.version, status: value.status,
        href: kind === "work" ? `/p/${value.project}/work-items/${value.id}`
          : `/${kind === "worklog" ? "worklogs" : "reports"}/${value.id}` };
      sources.push(source);
      if (kind === "work") {
        totals.works++;
        source.acceptanceFacts = structuredClone(acceptanceFacts);
        source.completionUnknown = currentFacts.unknownWorkIds.includes(value.id);
        if (source.completionUnknown) issue(warnings, `${value.id}：历史接受时间或归属未知，完整周期完成数不可确定。`);
        source.quality = qualityResult(s, value);
        source.plan = Boolean(value.plan);
        source.accepted = Boolean(value.accepted);
        if (!acceptanceFacts.length) issue(warnings, `${source.name}：所选版本没有本周期接受事实，不计为周期完成。`);
        if (fixedAcceptance) {
          const snapshot = acceptanceFacts.find(fact => fact.workSnapshot)?.workSnapshot;
          source.name = snapshot?.title || `${value.id} · 历史成果内容不可用`;
          source.plan = snapshot?.plan ?? null; source.quality = snapshot?.quality || "历史检查内容不可用";
          source.accepted = true; source.status = "本周期有固定接受裁决"; source.currentVersion = version; source.version = ref.version;
          source.href = `/p/${acceptanceFacts[0].project}/artifacts/${value.id}?version=${ref.version}`;
        }
        if (source.plan === false) issue(warnings, `${source.name}：计划未确认。`);
        if (source.quality !== "通过") issue(warnings, `${source.name}：内部检查${source.quality}。`);
      } else if (kind === "worklog") {
        const scopeMatch = current.subjectKey ? worklogSubjectMatch(s, value, current.subjectKey) : true;
        const rootScopeMatch = report.subjectKey ? worklogSubjectMatch(s, value, report.subjectKey) : true;
        source.scopeSnapshot = structuredClone(value.scopeSnapshot || null);
        source.scopeStatus = scopeMatch === null || rootScopeMatch === null ? "历史归属未知" : scopeMatch && rootScopeMatch ? "归属可核对" : "不属于所选报告归属";
        if (scopeMatch === false || rootScopeMatch === false) issue(errors, `${source.name}：投入不属于所选报告归属。`);
        if (scopeMatch === null || rootScopeMatch === null) { totals.unknownEffortIds.push(value.id); issue(warnings, `${source.name}：历史投入归属未知，不计入该主体的确定投入合计。`); }
        const scopeConfirmed = scopeMatch === true && rootScopeMatch === true;
        const hours = Number(value.hours);
        const durationKind = value.durationKind || "Human 工时";
        const bucket = { "Human 工时": "human", "Agent 墙钟": "agent", "模型 / 工具耗时": "tool" }[durationKind];
        source.durationKind = durationKind;
        const humanHours = bucket === "human" ? humanConfirmedHours(value) : null;
        const pendingHuman = bucket === "human" && value.effortState !== "confirmed"
          && !(!value.effortState && ["已提交", "已接受"].includes(value.status));
        source.hours = pendingHuman ? 0 : bucket === "human" ? humanHours : hours;
        source.pendingConfirmation = pendingHuman;
        if (pendingHuman) issue(warnings, `${source.name}：尚未本人确认，不计入 Human 实际投入。`);
        else if (!bucket || !Number.isFinite(source.hours) || source.hours < 0 || (source.hours === 0 && bucket !== "human") || source.hours > 24)
          issue(errors, `${source.name}：耗时类型或小时数无效。`);
        else if (scopeConfirmed) totals[bucket] += source.hours;
        if (!validDate(value.date)) issue(errors, `${source.name}：记录日期无效。`);
        if (!s.work.some((w) => w.id === value.work)) issue(errors, `${source.name}：关联工作不存在。`);
        if (value.status !== "已接受") issue(warnings, `${source.name}：${value.status}，尚未审核；上报不会改变工时审核状态。`);
      } else {
        if (value.status !== "已接受") issue(warnings, `${source.name}：${value.status}，尚未审核。`);
      }
    }
    if (current.claimedCompleted !== undefined && current.claimedCompleted !== "") {
      const claimed = Number(current.claimedCompleted);
      const completed = new Set(currentFacts.facts.filter(fact => localFactIds.has(fact.id)).map(fact => fact.workId)).size;
      if (currentFacts.unknownWorkIds.some(id => localWorks.has(id) || !references.length)) issue(errors, `${current.name || "报告"}：接受历史未知，不能确认声明完成数，请留空。`);
      if (!Number.isInteger(claimed) || claimed < 0) issue(errors, `${current.name || "报告"}：声明完成工作数须为非负整数，或留空。`);
      else if (claimed !== completed) issue(errors, `${current.name || "报告"}：声明完成 ${claimed} 项，与引用事实完成 ${completed} 项不一致。`);
    }
    return { works: localWorks, factIds: localFactIds };
  };
  visit(report, new Set(report.id ? [report.id] : []));
  totals.completed = new Set(rootFacts.facts.filter(fact => selectedFactIds.has(fact.id)).map(fact => fact.workId)).size;
  totals.completionUnknown = rootFacts.unknownWorkIds.filter(id => seen.has(`work:${id}`));
  totals.acceptanceFactKeys = rootFacts.factKeys.filter(id => selectedFactIds.has(id));
  for (const bucket of ["human", "agent", "tool"]) totals[bucket] = Math.round(totals[bucket] * 10000) / 10000;
  return { errors, warnings, sources, totals };
}

export function reportTransitionProblem(s, report, status, options) {
  return reportTransitionIssue(s, report, status, options);
}
export function resourceCommand(
  kind,
  recordId,
  status,
  reason,
  actionLabel = "",
  receipt,
) {
  if (kind === "plans") throw Error("周期计划须核对固定提交版本，再由 Human 上级确认。");
  if (kind === "defects") return defectCommand(recordId, status, reason, actionLabel, receipt);
  if (state.role !== "external" || kind !== "deliveries") assertWrite();
  if (kind === "worklogs") throw Error("请通过本人工作量校准与确认；Agent 运行指标只读。");
  if (kind === "testreports") throw Error("测试报告不可改写。");
  if (kind === "testplans") {
    if (status === "进行中") return startTestBatch(recordId);
    if (status === "待审核") return publishTestReport(recordId);
    throw Error("未知测试计划动作。");
  }
  if (state.offline) throw Error("离线中，请恢复连接后重新确认。");
  if (
    state.role === "external" &&
    !["已接受", "部分接受", "已拒收"].includes(status)
  )
    throw Error("当前客户仅可提交交付回执。");
  if (
    state.role === "executor" &&
    (["已批准", "已接受", "已发布", "生效", "已授权"].includes(status) ||
      (kind === "goals" && status === "进行中"))
  )
    throw Error("此决定需要负责人权限。");
  if (kind === "invitations") throw Error("邀请请通过组织与成员中的范围化邀请流程创建或撤销。");
  if (kind === "templates") throw Error("工作流模板请在画布中检查并发布新版本。");
  const record = state.resources[kind].find((x) => x.id === recordId);
  if (!record) throw Error("记录不存在。");
  if (kind === "goals") {
    if (receipt?.revision !== (record.revision || 1) || receipt?.status !== record.status) throw Error("目标修订或状态已变化，请重新打开当前版本后操作。");
    const problem = goalTransitionProblem(state, record, status);
    if (problem) throw Error(problem);
    if (!String(reason || "").trim()) throw Error("请填写提交或审核依据。");
  }
  if (kind === "tests" && (!["通过", "失败"].includes(status) || !reason?.trim()))
    throw Error("请选择测试结论并填写实际结果与依据。");
  let receiptItems;
  if (kind === "deliveries") {
    if (!["已接受", "部分接受", "已拒收"].includes(status))
      throw Error("请选择接受、部分接受或拒收回执。");
    if (!record.items?.length) throw Error("旧交付包没有逐项清单，请重新形成固定版本交付包。");
    if (receipt && receipt.version !== record.version) throw Error("交付版本已变化，请重新核对。");
    if (status === "部分接受" && !receipt) throw Error("部分接受必须逐项选择结果。");
    const choices = receipt?.items || record.items.map((item) => ({ id: item.id, status }));
    if (choices.length !== record.items.length || new Set(choices.map((c) => c.id)).size !== record.items.length)
      throw Error("请为交付清单中的每一项记录唯一结果。");
    receiptItems = record.items.map((item) => {
      const choice = choices.find((c) => c.id === item.id);
      if (!choice || !["已接受", "已拒收"].includes(choice.status)) throw Error("每项必须选择接受或拒收。");
      if (item.status === "已接受" && choice.status !== "已接受") throw Error("已接受项保留原决定，请通过新版本处理变更。");
      const feedback = choice.status === "已拒收"
        ? returnFeedback({ id: record.work, requirements: record.requirements || [] }, reason, choice.feedback, record.version)
        : undefined;
      if (receipt && choice.status === "已拒收" && !choice.feedback) throw Error("拒收项需补充结构化反馈。");
      return { ...item, status: choice.status, ...(feedback ? { feedback } : {}) };
    });
    const accepted = receiptItems.filter((item) => item.status === "已接受").length;
    const derived = accepted === receiptItems.length ? "已接受" : accepted ? "部分接受" : "已拒收";
    if (status !== derived) throw Error("回执类型与逐项结果不一致。");
    if (record.status === status && JSON.stringify(record.items) === JSON.stringify(receiptItems)) return;
  }
  if (kind === "reports" && receipt && ((record.revision || 1) !== receipt.revision || record.status !== receipt.status))
    throw Error("报告修订或状态已变化，请重新打开核对。");
  if (kind === "reports") {
    const problem = reportTransitionProblem(state, record, status, receipt);
    if (problem) throw Error(problem);
    if (!reason?.trim()) throw Error("报告决定需填写说明；退回需给出更正原因。");
  }
  let reportCheck;
  if (kind === "reports" && ["已提交", "已接受"].includes(status)) {
    reportCheck = reportEvidence(state, record);
    if (reportCheck.errors.length) throw Error(`报告核对未通过：${reportCheck.errors.join("；")}`);
  }
  if (kind === "approvals" && status === "已批准") {
    const target = record.targetKind === "agent" ? state.agents.find(a => a.id === record.target) : state.resources.models.find((m) => m.id === record.target);
    if (record.targetKind === "agent" && !target) throw Error("审批 Agent 不存在。");
    if (record.targetKind === "agent" && target) {
      const model = state.resources.models.find(model => model.id === target.modelId);
      if (!model || (model.revision || 1) !== target.modelRevision) throw Error("模型配置已变更，请重新提交 Agent 配置。");
    }
    if (target && (target.revision || 1) !== (record.targetRevision || 1))
      throw Error("审批目标已变更，请重新提交当前配置版本。");
  }
  update((s) => {
    const r = s.resources[kind].find((x) => x.id === recordId);
    if (kind === "tests") preserveTestRun(s, r);
    r.history ??= [];
    r.history.push({
      status: r.status,
      ...(kind === "reports" ? { snapshot: { ...structuredClone(r), history: undefined }, revision: r.revision || 1 } : {}),
      ...(kind === "goals" ? { snapshot: { ...structuredClone(r), history: undefined }, revision: r.revision || 1, actorId: "H-lin" } : {}),
      ...(kind === "deliveries" ? { items: structuredClone(r.items) } : {}),
      ...(kind === "tests" ? { snapshot: structuredClone(r.runs?.find((run) => run.id === r.currentRun) || testRunSnapshot(s, r, "legacy")) } : {}),
      actor: state.role === "external" ? "陈曦" : "林予",
      reason,
      time: new Date().toISOString(),
    });
    r.status = status;
    if (kind === "goals") {
      const previousRevision = r.revision || 1;
      r.revision = previousRevision + 1;
      const actor = { actorId: "H-lin", actorKind: "Human", actor: "林予", reason: reason.trim(), time: new Date().toISOString() };
      if (status === "待审核") {
        r.submission = { ...actor, revision: r.revision, snapshot: { ...structuredClone(r), history: undefined, submission: undefined, approval: undefined } };
        r.approval = null;
      } else if (status === "进行中") {
        r.approval = { ...actor, revision: previousRevision, permission: "tenant-owner", snapshot: structuredClone(r.submission.snapshot) };
      } else {
        r.submission = null;
        r.approval = null;
      }
    }
    if (reportCheck) r.factSnapshot = { ...reportCheck, recordedAt: new Date().toISOString() };
    if (kind === "reports") {
      const author = reportAuthor(s, r), authority = reportReviewAuthority(s, r), time = new Date().toISOString();
      const previousRevision = r.revision || 1;
      r.revision = previousRevision + 1;
      r.authorId = author.id; r.authorKind = author.kind; r.owner = author.name;
      if (status === "已提交") {
        r.submission = { revision: r.revision, authorId: author.id, authorKind: author.kind,
          confirmerId: authority.system ? "SYSTEM:team_top" : authority.id || null,
          confirmerBasis: authority.basis || authority.issue, content: reportSubmissionContent(r),
          factSnapshot: structuredClone(r.factSnapshot), reason: reason.trim(), time,
          actorId: author.id, actorKind: author.kind, recordedBy: "H-lin",
          source: author.kind === "Agent" ? "本地 Agent 模拟上报" : "Human 本人确认上报" };
        r.approval = null;
        if (authority.system) {
          r.status = "已接受";
          r.approval = { actorId: "SYSTEM:team_top", actorKind: "System", submissionRevision: r.submission.revision,
            reason: authority.basis, time, snapshot: structuredClone(r.submission) };
        }
      } else if (status === "已接受") {
        r.approval = { actorId: "H-lin", actorKind: "Human", submissionRevision: previousRevision,
          reason: reason.trim(), time, snapshot: structuredClone(r.submission) };
      } else { r.submission = null; r.approval = null; }
    }
    if (kind === "tests") {
      // The previous status is restored for legacy capture before the new attempt.
      r.status = r.history.at(-1).status;
      recordTestRun(s, r, status, reason);
      syncQuality(s, r.work, r.version);
    }

    if (kind === "approvals" && status === "已批准") {
      const agent = s.agents.find(
        (a) => (r.targetKind === "agent" ? a.id === r.target : a.name === r.target) && a.status === "待批准",
      );
      if (agent) { agent.status = "空闲"; agent.approvedRevision = agent.revision || 1; }
    }
    if (kind === "deliveries") {
      const newlyRejected = receiptItems.filter((item) => item.status === "已拒收" && r.items.find((old) => old.id === item.id)?.status !== "已拒收");
      r.items = receiptItems;
      const work = s.work.find((w) => w.id === r.work);
      if (work && work.currentDeliveryId === r.id && `v${work.version}` === r.version) {
        work.l4 = { status, recipient: r.recipient, version: work.version };
        if (status === "已接受" && work.delivered && work.accepted && qualityResult(s, work) === "通过")
          work.status = "已完成";
      }
      if (work && newlyRejected.length && `v${work.version}` === r.version) {
        invalidateDependentOutputs(s, work, reason);
        work.returnFeedback ??= [];
        work.returnFeedback.push(...newlyRejected.map((item) => ({ ...item.feedback, item: item.id, delivery: r.id, reason, time: new Date().toISOString() })));
        work.status = dependencyProblems(s, work).length ? "等待" : "进行中";
        work.qa = false;
        work.accepted = false;
        work.notes = reason;
        work.delivered = false;
        recordFormalRework(s, work, reason, { key: `delivery:${r.id}`, source: "delivery", version: r.version });
      }
    }
    if (kind === "schedules" && status === "启用")
      r.next = "按已保存时区等待下一次触发";
    if (kind === "models" && status === "生效") {
      const approved = s.resources.approvals.some(
        (a) =>
          a.target === r.id &&
          a.status === "已批准" &&
          (a.targetRevision || 1) === (r.revision || 1),
      );
      if (!approved && record.status !== "生效")
        throw Error("当前配置需要先批准下发范围。");
      r.nodeResults = s.nodes.map((n) => ({
        node: n.id,
        status: n.status === "在线" ? "生效" : "待应用",
        version: r.version,
      }));
      if (r.nodeResults.some((n) => n.status !== "生效")) r.status = "部分生效";
    }
    if (kind === "schedules" && actionLabel === "模拟运行一次") {
      if (r.action.includes("报告") || r.action.includes("日报")) {
        r.lastReportId = upsertPeriodicReport(s, { subjectKey: r.subjectKey, cadence: r.cadence, timezone: r.timezone }, r.id, id);
      }
      else
        s.resources.tests.unshift({
          id: id("TC"),
          name: `${r.name} · 定期检查`,
          status: "待执行",
          owner: r.owner,
          version: "当前固定输入",
          work: "W-301",
          method: "待独立执行",
          expected: "按已确认专业包检查",
          actual: "未执行，不冒充通过",
          evidence: "暂无",
        });
      r.lastRun = new Date().toISOString();
    }
    if (kind === "repositories" && actionLabel === "模拟增量同步")
      r.sync = new Intl.DateTimeFormat("zh-CN", {
        hour: "2-digit",
        minute: "2-digit",
      }).format(new Date());
    if (kind === "approvals" && status === "已批准") {
      const model = s.resources.models.find((x) => x.id === r.target);
      if (model) model.status = "待应用";
    }
    log(
      s,
      `${r.name} → ${status}${reason ? " · " + reason : ""}`,
      `/${kind}/${recordId}`,
    );
  });
}
export function shareMessage(input) {
  assertWrite();
  const selection = prepareMessageShare(state, input);
  let shared;
  update(s => {
    shared = { id: id("m"), author: "林予", kind: "human",
      time: new Date().toISOString(), channel: selection.channel,
      text: `共享自 ${selection.source.author}：${selection.source.text}` };
    s.messages.push(shared);
    log(s, `共享选定消息 ${selection.source.id} 到 ${selection.targetName}`, `/chat?channel_id=${encodeURIComponent(selection.channel)}`);
  });
  return shared;
}

export function exportData(name, value) {
  const blob = new Blob([JSON.stringify(value, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function visibleWork(s) {
  return s.work.filter(
    (w) =>
      (s.project === "all" || w.project === s.project) &&
      (s.role !== "external" || w.delivered),
  );
}

// Shared packages retain their fixed version even when current work is reopened.
export function sharedVersions(s, workId) {
  return [
    ...new Set(
      s.resources.deliveries
        .filter((d) => d.work === workId)
        .map((d) => Number(String(d.version).replace(/^v/, "")))
        .filter(Number.isInteger),
    ),
  ].sort((a, b) => a - b);
}
export function sharedArtifactPath(s, w) {
  return `/p/${w.project}/artifacts/${w.id}?version=${sharedVersions(s, w.id).at(-1)}`;
}

export const agentRuntimes = [
  "Codex CLI",
  "Claude Code",
  "OpenCode",
  "直接 API",
];
export function agentModelCompatible(runtime, model) {
  return Boolean(
    model &&
    (runtime === "Codex CLI"
      ? model.protocol === "OpenAI compatible"
      : runtime === "Claude Code"
        ? model.protocol === "Anthropic Messages"
        : ["OpenCode", "直接 API"].includes(runtime) &&
          ["OpenAI compatible", "Anthropic Messages"].includes(model.protocol)),
  );
}
export function agentMessages(config, task) {
  return [
    { role: "system", content: config.systemPrompt || "" },
    {
      role: "user",
      content: (config.taskTemplate || "").replaceAll("{{task}}", task),
    },
  ];
}
export function saveAgent(config, agentId, expectedRevision) {
  assertWrite();
  const clean = Object.fromEntries(
    [
      "name",
      "role",
      "responsibilities",
      "systemPrompt",
      "taskTemplate",
      "runtime",
      "modelId",
      "owner",
    ].map((key) => [key, String(config[key] || "").trim()]),
  );
  if (Object.values(clean).some((value) => !value))
    throw Error(
      "名称、职责、提示词、任务模板、负责人、Runtime 和模型均需填写。",
    );
  if (!(state.organization?.members || []).some(member => member.kind === "Human" && member.name === clean.owner))
    throw Error("负责人必须是当前租户内的 Human 成员。");
  if (state.agents.some(agent => agent.id !== agentId && agent.name === clean.name))
    throw Error("当前租户已有同名 Agent，请使用可区分的名称。");
  if (!clean.taskTemplate.includes("{{task}}"))
    throw Error("用户任务模板必须包含 {{task}}，用于注入本次任务。");
  const model = state.resources.models.find(
    (model) => model.id === clean.modelId,
  );
  if (!agentModelCompatible(clean.runtime, model))
    throw Error("所选 Runtime 与模型协议不匹配。");
  const skillIds = [...new Set(config.skillIds || [])],
    mcpIds = [...new Set(config.mcpIds || [])];
  for (const [ids, kind] of [
    [skillIds, "Skill"],
    [mcpIds, "MCP"],
  ])
    if (
      ids.some(
        (id) =>
          !state.resources.tools.some(
            (tool) => tool.id === id && tool.kind === kind,
          ),
      )
    )
      throw Error("工具引用不存在或类型不匹配。");
  const existing =
    agentId && state.agents.find((agent) => agent.id === agentId);
  if (agentId && !existing) throw Error("Agent 不存在。");
  if (existing && expectedRevision !== (existing.revision || 1))
    throw Error("Agent 配置已变更，请重新打开当前版本后编辑。");
  if (existing?.status === "退役") throw Error("已退役 Agent 不能修改配置。");
  const creatorKind = existing?.creatorKind || (existing ? "未知" : config.creatorKind || "Human");
  const creatorId = existing ? existing.creatorId : creatorKind === "Human" ? "H-lin" : config.creatorId;
  if (!existing && !["Human", "Agent"].includes(creatorKind)) throw Error("请选择有效的 Agent 创建来源。");
  if (!existing && creatorKind === "Agent" && !state.agents.some(agent => agent.id === creatorId && !["退役", "待批准"].includes(agent.status)))
    throw Error("创建者必须是当前租户中可用的 Agent。");
  const targetId = agentId || id("agent");
  update((s) => {
    const previous = s.agents.find((agent) => agent.id === targetId);
    const revision = (previous?.revision || (previous ? 1 : 0)) + 1;
    const agent = {
      ...clean,
      id: targetId,
      model: model.name,
      modelRevision: model.revision || 1,
      skillIds,
      mcpIds,
      revision,
      status: "待批准",
      owner: clean.owner,
      origin: previous?.origin || `${creatorKind} 创建`,
      creatorKind, creatorId,
      creationMode: previous?.creationMode || (creatorKind === "Agent" ? "本地模拟创建提案" : "Human 配置提案"),
      history: [
        ...(previous?.history || []),
        ...(previous
          ? [{ ...structuredClone(previous), history: undefined }]
          : []),
      ],
    };
    if (previous) Object.assign(previous, agent);
    else s.agents.push(agent);
    s.resources.approvals.unshift({
      id: id("AP"),
      name: `Agent 配置 ${agent.name} v${revision}`,
      status: "待审核",
      owner: agent.owner,
      target: targetId,
      targetKind: "agent",
      targetRevision: revision,
      scope: "Agent 职责 / 提示词 / 模型 / 工具引用",
      reason: agent.responsibilities,
      version: `v${revision}`,
    });
    log(s, `提交 Agent 配置 ${agent.name} v${revision}`, `/agents/${targetId}`);
  });
  return targetId;
}
export function templateFlow(s, templateId) {
  const template = s.resources.templates.find((item) => item.id === templateId);
  if (!template) throw Error("工作流模板不存在。");
  return (
    template.flow || {
      flowNodes: structuredClone(initial.flowNodes),
      flowEdges: structuredClone(initial.flowEdges),
      flowVersion: 0,
      flowDraft: null,
    }
  );
}
export function mutableTemplateFlow(s, templateId) {
  const template = s.resources.templates.find((item) => item.id === templateId);
  if (!template) throw Error("工作流模板不存在。");
  template.flow ??= structuredClone(templateFlow(s, templateId));
  return template.flow;
}
export function templateDefinition(s, templateId, version) {
  const flow = templateFlow(s, templateId);
  if (!Number.isInteger(version) || version <= 0) return null;
  return version === flow.flowVersion
    ? { version, nodes: flow.flowNodes, edges: flow.flowEdges }
    : flow.flowVersions?.[version] || null;
}
export function createWorkflowTemplate(name) {
  assertWrite();
  if (!name.trim()) throw Error("请填写工作流模板名称。");
  const templateId = id("WF");
  update((s) =>
    s.resources.templates.unshift({
      id: templateId,
      name: name.trim(),
      owner: "林予",
      status: "草稿",
      version: "未发布",
      flow: {
        flowNodes: structuredClone(initial.flowNodes),
        flowEdges: structuredClone(initial.flowEdges),
        flowVersion: 0,
        flowDraft: null,
      },
    }),
  );
  return templateId;
}
export function draftWorkflowTemplate(templateId, brief) {
  assertWrite();
  if (!brief.trim()) throw Error("请描述模板目标。");
  update((s) => {
    const flow = mutableTemplateFlow(s, templateId);
    if (flow.flowDraft)
      throw Error("已有草案，请先完成当前草案再生成，避免覆盖手动修改。");
    flow.flowDraft = {
      nodes: structuredClone(flow.flowNodes),
      edges: structuredClone(flow.flowEdges),
    };
    flow.flowDraft.source = "AI 模拟草案";
    flow.flowDraft.brief = brief.trim();
    for (const node of flow.flowDraft.nodes)
      if (node.data.kind === "AI 任务")
        node.data.prompt = `【本地模拟草案，请人工审阅】目标：${brief.trim()}。节点：${node.data.label}。依据已确认输入产出成果并提交独立检查。`;
    log(s, "生成工作流模板 AI 模拟草案", `/templates/${templateId}`);
  });
}
export function publishWorkflowTemplate(s, templateId, graph) {
  if (s.role !== "owner") throw Error("工作流模板发布需要负责人确认。");
  const flow = mutableTemplateFlow(s, templateId);
  const version = publishFlowGraph(flow, graph);
  const template = s.resources.templates.find((item) => item.id === templateId);
  template.status = "已发布";
  template.version = `v${version}`;
  return version;
}
export function projectFlow(s, project) {
  if (project === "launch") return s;
  return (
    s.projectFlows?.[project] || {
      flowNodes: initial.flowNodes,
      flowEdges: initial.flowEdges,
      flowDraft: null,
      flowVersion: 0,
    }
  );
}
export function mutableProjectFlow(s, project) {
  if (project === "launch") return s;
  s.projectFlows ??= {};
  s.projectFlows[project] ??= structuredClone(projectFlow(s, project));
  return s.projectFlows[project];
}
export function flowDefinition(s, project, version) {
  const flow = projectFlow(s, project);
  const wanted = version ?? flow.flowVersion;
  if (!Number.isInteger(wanted) || wanted <= 0) return null;
  if (wanted === flow.flowVersion)
    return { version: wanted, nodes: flow.flowNodes, edges: flow.flowEdges };
  return flow.flowVersions?.[wanted] || null;
}
export function lockWorkFlow(s, work) {
  const definition = flowDefinition(s, work.project);
  if (!definition) throw Error("请先发布项目工作流，再确认 SOP。");
  work.flowVersion = definition.version;
  return definition.version;
}
function publishFlowGraph(flow, graph) {
  const issues = validateWorkflow(graph);
  if (issues.length) throw Error(issues[0].message);
  flow.flowVersions ??= {};
  if (flow.flowVersion > 0) flow.flowVersions[flow.flowVersion] = structuredClone({ version: flow.flowVersion, nodes: flow.flowNodes, edges: flow.flowEdges });
  flow.flowVersion = (flow.flowVersion || 0) + 1;
  flow.flowNodes = structuredClone(graph.nodes);
  flow.flowEdges = structuredClone(graph.edges);
  flow.flowDraft = null;
  return flow.flowVersion;
}
export function publishProjectFlow(s, project, graph) {
  const issues = validateWorkflow(graph);
  if (issues.length) throw Error(issues[0].message);
  return publishFlowGraph(mutableProjectFlow(s, project), graph);
}

export function organizationPeople(s) {
  return [
    ...(s.organization?.members || []),
    ...s.agents.map((agent) => ({ id: `agent:${agent.id}`, name: agent.name, kind: "Agent" })),
  ];
}
export function createProject(input) {
  assertWrite();
  if (input.tenantId !== state.activeTenant) throw Error("租户已切换，请在当前租户重新创建项目。");
  if (typeof input.name !== "string" || !input.name.trim()) throw Error("请填写项目名称。");
  if (typeof input.description !== "string" || !input.description.trim()) throw Error("请填写目标与预期结果。");
  const owner = state.organization.members.find(person => person.id === input.ownerId && person.kind === "Human");
  if (!owner) throw Error("请选择当前租户的 Human 负责人。");
  const projectId = id("project");
  update(s => {
    s.projects.push({ id: projectId, name: input.name.trim(), description: input.description.trim(),
      owner: owner.name, ownerId: owner.id, type: "通用协作", progress: 0, budget: 0, spent: 0,
      deadline: "待确认", color: "violet" });
    log(s, `创建项目 ${input.name.trim()}`, `/p/${projectId}`);
  });
  return projectId;
}
export function workExecutors(s, work) {
  if (!work.team) {
    if (work.agentId) {
      const agent = s.agents.find((item) => item.id === work.agentId);
      return [{ id: `agent:${work.agentId}`, name: agent?.name || "成员已不可用", kind: agent ? "Agent" : "未知" }];
    }
    return work.agent && work.agent !== "未分派" ? [{ id: "legacy", name: work.agent, kind: "未绑定身份" }] : [];
  }
  const people = organizationPeople(s);
  return work.team.executorIds.map((id) => people.find((person) => person.id === id) || { id, name: "成员已不可用", kind: "未知" });
}
export function workTeamMemberAvailable(s, memberId) {
  if (memberId?.startsWith("agent:")) {
    const agent = s.agents.find((item) => `agent:${item.id}` === memberId);
    return Boolean(agent && !["退役", "待批准"].includes(agent.status));
  }
  return s.organization.members.some((person) => person.id === memberId && person.kind === "Human");
}
function assertOrganizationWrite() {
  assertWrite();
  if (state.role !== "owner") throw Error("请由负责人视角配置组织与团队责任。");
}
export function saveOrganization(kind, input, reason) {
  assertOrganizationWrite();
  if (!["nodes", "members", "projectRoles"].includes(kind)) throw Error("未知组织记录类型。");
  if (!reason?.trim()) throw Error("请填写变更说明。");
  const org = state.organization;
  const previous = input.id ? org[kind].find((item) => item.id === input.id) : null;
  if (input.id && !previous) throw Error("记录不存在于当前租户。");
  if (previous && previous.revision !== input.revision) throw Error("记录已变化，请重新打开核对。");
  const people = state.organization.members.filter(person => person.kind === "Human");
  const known = (id) => people.some((person) => person.id === id);
  let record;
  if (kind === "members") {
    if (!input.name?.trim()) throw Error("请填写成员名称。");
    if (org.members.some((member) => member.id !== input.id && member.name === input.name.trim())) throw Error("当前租户已有同名 Human 成员。");
    record = { name: input.name.trim(), kind: "Human" };
    if (previous && previous.name !== record.name) throw Error("此原型暂不支持重命名已有成员，请保留其身份引用。");
  } else if (kind === "nodes") {
    if (!input.name?.trim()) throw Error("请填写部门或团队名称。");
    if (!people.some((person) => person.id === input.ownerId && person.kind === "Human")) throw Error("组织节点须指定当前租户的 Human 负责人。");
    if (input.parentId && !org.nodes.some((node) => node.id === input.parentId)) throw Error("上级节点不存在。");
    let parent = input.parentId;
    const visited = new Set(input.id ? [input.id] : []);
    while (parent) {
      if (visited.has(parent)) throw Error("不能把节点移动到自身或下级，组织层级不能形成循环。");
      visited.add(parent);
      parent = org.nodes.find((node) => node.id === parent)?.parentId;
    }
    const validDate = (date) => typeof date === "string" && /^\d{4}-\d{2}-\d{2}$/.test(date)
      && !Number.isNaN(Date.parse(date)) && new Date(date).toISOString().slice(0, 10) === date;
    if (!validDate(input.start) || (input.end && (!validDate(input.end) || input.end < input.start)))
      throw Error("请填写有效起始日期；结束日期可留空，但不能早于开始。");
    if (!Array.isArray(input.memberIds) || input.memberIds.some((id) => !known(id))) throw Error("成员必须来自当前租户。");
    record = { name: input.name.trim(), parentId: input.parentId || "", ownerId: input.ownerId,
      memberIds: [...new Set(input.memberIds)], start: input.start, end: input.end || "" };
  } else {
    if (!known(input.memberId)) throw Error("请选择当前租户的成员。");
    if (!state.projects.some((project) => project.id === input.projectId)) throw Error("项目不存在于当前租户。");
    if (!["项目成员", "项目负责人", "评审者"].includes(input.role)) throw Error("请选择有效项目角色。");
    if (org.projectRoles.some((role) => role.id !== input.id && role.memberId === input.memberId
      && role.projectId === input.projectId && role.role === input.role)) throw Error("此成员已具有该项目角色。");
    record = { memberId: input.memberId, projectId: input.projectId, role: input.role };
  }
  const recordId = previous?.id || id(kind === "nodes" ? "ORG" : kind === "members" ? "H" : "PR");
  update((s) => {
    const entry = { ...record, id: recordId, revision: (previous?.revision || 0) + 1,
      history: [...(previous?.history || []), { previous: previous ? { ...previous, history: undefined } : null,
        next: structuredClone(record), actor: "当前负责人", reason: reason.trim(), time: new Date().toISOString() }] };
    if (previous) s.organization[kind][s.organization[kind].findIndex((item) => item.id === recordId)] = entry;
    else s.organization[kind].push(entry);
    log(s, `更新组织记录 ${record.name || recordId} · ${reason.trim()}`, "/members");
  });
  return recordId;
}
export function saveMemberInvitation(input, reason) {
  assertOrganizationWrite();
  if (!reason?.trim()) throw Error("请填写邀请说明。");
  if (!input.name?.trim() || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(input.email || "")) throw Error("请填写姓名与有效电子邮件。");
  const project = state.projects.find((item) => item.id === input.projectId);
  if (!project) throw Error("请选择当前租户的项目。");
  const owner = organizationPeople(state).find((item) => item.id === input.ownerId && item.kind === "Human");
  if (!owner) throw Error("请选择当前租户的 Human 邀请人。");
  if (!["项目成员", "评审者", "外部客户"].includes(input.role)) throw Error("请选择有效角色。");
  const scopes = { project: "所选项目协作内容", shared: "所选项目已共享成果" };
  if (!scopes[input.scopeKind] || (input.role === "外部客户" && input.scopeKind !== "shared")) throw Error("外部客户仅可访问所选项目已共享成果。");
  const today = new Date().toISOString().slice(0, 10);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(input.expires || "") || Number.isNaN(Date.parse(input.expires))
    || new Date(input.expires).toISOString().slice(0, 10) !== input.expires || input.expires < today) throw Error("请选择今天或之后的有效截止日期。");
  const invitationId = id("INV");
  update((s) => {
    s.resources.invitations.unshift({ id: invitationId, name: input.name.trim(), email: input.email.trim(),
      owner: owner.name, ownerId: owner.id, createdBy: "林予", projectId: project.id, role: input.role, scopeKind: input.scopeKind,
      scope: `${project.name} · ${scopes[input.scopeKind]}`, expires: input.expires, status: "待接受", revision: 1,
      history: [{ action: "创建本地邀请", reason: reason.trim(), actor: "林予", time: new Date().toISOString() }] });
    log(s, `创建本地邀请 ${input.name.trim()} · ${reason.trim()}`, "/members");
  });
  return invitationId;
}
export function revokeMemberInvitation(invitationId, reason, revision) {
  assertOrganizationWrite();
  const invitation = state.resources.invitations.find((item) => item.id === invitationId);
  if (!invitation) throw Error("邀请不存在于当前租户。");
  if (invitation.status !== "待接受") throw Error("只能撤销待接受的邀请；已加入成员需单独管理。");
  if ((invitation.revision || 1) !== revision) throw Error("邀请已变化，请重新核对。");
  if (!reason?.trim()) throw Error("请填写撤销原因。");
  update((s) => {
    const item = s.resources.invitations.find((entry) => entry.id === invitationId);
    item.status = "已撤销"; item.revision = (item.revision || 1) + 1;
    item.history = [...(item.history || []), { action: "撤销本地邀请", actor: "当前负责人", reason: reason.trim(), time: new Date().toISOString() }];
    log(s, `撤销本地邀请 ${item.name} · ${reason.trim()}`, "/members");
  });
}

export function saveWorkTeam(workId, input, reason) {
  assertOrganizationWrite();
  const work = state.work.find((item) => item.id === workId);
  if (!work) throw Error("工作不存在于当前租户。");
  if ((work.team?.revision || 0) !== input.revision || work.owner !== input.previousOwner)
    throw Error("团队责任已变化，请重新打开核对。");
  if (!reason?.trim()) throw Error("请填写变更说明。");
  const people = organizationPeople(state);
  const owner = people.find((person) => person.id === input.ownerId && person.kind === "Human");
  if (!owner) throw Error("必须指定当前租户的 Human 结果负责人。");
  const known = (id) => people.some((person) => person.id === id);
  if (!known(input.coordinatorId)) throw Error("请指定当前租户的协调者。");
  if (!people.some((person) => person.id === input.receiverId && person.kind === "Human")) throw Error("下游接收决定须指定当前租户的 Human。");
  for (const field of ["executorIds", "checkerIds"])
    if (!Array.isArray(input[field]) || !input[field].length || input[field].some((id) => !known(id)))
      throw Error("执行者和检查者须从当前租户成员中选择，且不能为空。");
  for (const field of ["coordinatorId", "executorIds", "checkerIds"]) {
    const nextIds = Array.isArray(input[field]) ? input[field] : [input[field]];
    const previousIds = Array.isArray(work.team?.[field]) ? work.team[field] : [work.team?.[field]];
    if (nextIds.some((id) => !previousIds.includes(id) && !workTeamMemberAvailable(state, id)))
      throw Error("退役或待批准 Agent 不能新分派到任务角色；旧编组引用保留，可移除后更换。");
  }
  update((s) => {
    const item = s.work.find((item) => item.id === workId);
    const next = { coordinatorId: input.coordinatorId, receiverId: input.receiverId,
      executorIds: [...new Set(input.executorIds)], checkerIds: [...new Set(input.checkerIds)] };
    const previous = { owner: item.owner, ...(item.team || {}), history: undefined };
    item.team = { ...next, revision: (item.team?.revision || 0) + 1,
      history: [...(item.team?.history || []), { previous, next: { owner: owner.name, ...next },
        actor: "当前负责人", reason: reason.trim(), time: new Date().toISOString() }] };
    // work.owner remains the single responsibility field used by work details and commands.
    item.owner = owner.name;
    log(s, `更新 ${item.title} 小团队 · ${reason.trim()}`, `/p/${item.project}/work-items/${item.id}`);
  });
}

const tenantFields = [
  "planningSettings",
  "organization",
  "role",
  "project",
  "projects",
  "work",
  "agents",
  "nodes",
  "messages",
  "comments",
  "resources",
  "events",
  "flowNodes",
  "flowEdges",
  "flowDraft",
  "flowVersion",
  "flowVersions",
  "projectFlows",
  "drafts",
  "taskPreviews",
  "defectMigrationIssues",
  "budget",
];
function tenantSnapshot(s) {
  return Object.fromEntries(
    tenantFields.map((k) => [k, structuredClone(s[k] ?? null)]),
  );
}
function emptyTenant(role = "owner") {
  const fresh = hydrate(structuredClone(initial));
  for (const k of [
    "projects",
    "work",
    "agents",
    "nodes",
    "messages",
    "comments",
    "events",
  ])
    fresh[k] = [];
  fresh.organization = { nodes: [], members: [{ id: "H-lin", name: "林予", kind: "Human" }], projectRoles: [] };
  fresh.resources = Object.fromEntries(
    Object.keys(fresh.resources).map((k) => [k, []]),
  );
  fresh.role = role;
  fresh.project = "all";
  fresh.budget = 0;
  fresh.flowNodes = [];
  fresh.flowEdges = [];
  fresh.flowVersion = 0;
  fresh.projectFlows = {};
  fresh.drafts = {};
  return tenantSnapshot(fresh);
}
export function switchTenant(tenantId) {
  const target = state.tenants.find((t) => t.id === tenantId);
  if (!target) throw Error("只能切换到已加入的租户。");
  if (state.offline) throw Error("离线时不能切换租户，请先恢复连接。");
  if (tenantId === state.activeTenant) return;
  update((s) => {
    s.tenantData[s.activeTenant] = tenantSnapshot(s);
    Object.assign(
      s,
      structuredClone(s.tenantData[tenantId] || emptyTenant(target.role)),
    );
    s.activeTenant = tenantId;
  });
}
export function createTenant(name) {
  if (state.offline) throw Error("离线时不能创建租户。");
  if (!name.trim()) throw Error("请填写租户名称。");
  const tenantId = id("tenant");
  update((s) =>
    s.tenants.push({ id: tenantId, name: name.trim(), role: "owner" }),
  );
  switchTenant(tenantId);
  return tenantId;
}
export function joinTenant(code) {
  if (state.offline) throw Error("离线时不能加入租户。");
  const invitation = state.tenantInvitations.find((item) => item.code === code.trim());
  if (!invitation || invitation.recipient !== "林予") throw Error("邀请码无效或不属于当前账号，请核对后重试。");
  if (invitation.status !== "已接受" && (invitation.status !== "待接受" || !Number.isFinite(Date.parse(invitation.expires)) || Date.parse(invitation.expires) <= Date.now()))
    throw Error("邀请已失效、拒绝或撤销，请联系邀请人重新邀请。");
  update((s) => {
    const current = s.tenantInvitations.find((item) => item.id === invitation.id);
    if (current.status !== "已接受") {
      current.status = "已接受";
      current.respondedAt = new Date().toISOString();
    }
    if (!s.tenants.some((tenant) => tenant.id === invitation.tenantId))
      s.tenants.push({ id: invitation.tenantId, name: invitation.tenantName, role: invitation.role });
  });
  switchTenant(invitation.tenantId);
  return invitation.tenantId;
}
export function declineTenantInvitation(invitationId) {
  if (state.offline) throw Error("离线时不能处理邀请。");
  const invitation = state.tenantInvitations.find((item) => item.id === invitationId && item.recipient === "林予");
  if (!invitation || invitation.status !== "待接受") throw Error("邀请已变化，请刷新后核对。");
  update((s) => {
    const item = s.tenantInvitations.find((item) => item.id === invitationId);
    item.status = "已拒绝";
    item.respondedAt = new Date().toISOString();
  });
}

export function scheduleWork(workId, start, end, reason, revision) {
  assertWrite();
  const w = state.work.find((w) => w.id === workId);
  if (!w) throw Error("工作不存在。");
  if (state.role === "executor" && w.owner !== "林予")
    throw Error("只能调整本人负责的排期。");
  if ((w.scheduleRevision || 0) !== revision)
    throw Error("排期已变化，请重新打开核对。");
  if (!reason.trim()) throw Error("请填写调整说明。");
  const valid = (v) =>
    /^\d{4}-\d{2}-\d{2}$/.test(v) &&
    !Number.isNaN(Date.parse(v)) &&
    new Date(v).toISOString().slice(0, 10) === v;
  if ((start || end) && (!valid(start) || !valid(end)))
    throw Error("请填写真实有效的起止日期，或同时清空。");
  if (start > end) throw Error("结束日期不能早于开始日期。");
  update((s) => {
    const task = s.work.find((v) => v.id === workId);
    task.scheduleHistory ??= [];
    task.scheduleHistory.push({
      start: task.plannedStart || "",
      end: task.plannedEnd || "",
      reason,
      revision: task.scheduleRevision || 0,
    });
    task.plannedStart = start;
    task.plannedEnd = end;
    task.scheduleRevision = (task.scheduleRevision || 0) + 1;
    log(
      s,
      `调整 ${task.title} 排期：${start || "未排期"}${end ? " 至 " + end : ""} · ${reason}`,
      `/p/${task.project}/work-items/${task.id}`,
    );
  });
}

export function validateWorkflow(graph) {
  const issues = [],
    nodes = graph.nodes,
    edges = graph.edges,
    ids = new Set(nodes.map((n) => n.id));
  const fail = (node, message) => issues.push({ node, message });
  if (ids.size !== nodes.length) fail(nodes[0]?.id, "节点标识重复。");
  if (!ids.has("brief")) fail(nodes[0]?.id, "缺少触发入口。");
  if (!ids.has("delivery")) fail(nodes[0]?.id, "缺少最终 Human 交付环节。");
  for (const n of nodes) {
    const prefix = n.data.label || n.id;
    if (!n.data.label?.trim()) fail(n.id, `${prefix}：请填写节点名称。`);
    if (n.id !== "brief" && !edges.some((e) => e.target === n.id))
      fail(n.id, `${prefix}：缺少上游输入，请先连接。`);
    if (n.id !== "delivery" && !edges.some((e) => e.source === n.id))
      fail(n.id, `${prefix}：缺少下游接收方。`);
    if (!n.data.owner?.trim()) fail(n.id, `${prefix}：缺少 Human 负责人。`);
    if (!n.data.output?.trim() || !n.data.gate?.trim())
      fail(n.id, `${prefix}：输出契约与内部验收必须明确。`);
    if (n.data.kind === "AI 任务" && !n.data.prompt?.trim())
      fail(n.id, `${prefix}：请配置任务指令。`);
    if (n.data.kind === "工具调用" && !n.data.tool?.trim())
      fail(n.id, `${prefix}：请选择工具引用。`);
    if (n.data.kind === "HTTP 请求" && !/^https?:\/\//.test(n.data.url || ""))
      fail(n.id, `${prefix}：请配置有效的 HTTP 地址。`);
    if (n.data.kind === "条件分支") {
      if (!n.data.condition?.trim()) fail(n.id, `${prefix}：请填写分支条件。`);
      if (
        !["true", "false"].every((handle) =>
          edges.some((e) => e.source === n.id && e.sourceHandle === handle),
        )
      )
        fail(n.id, `${prefix}：是 / 否分支都需要下游。`);
    }
    if (!Number.isFinite(Number(n.data.timeout)) || Number(n.data.timeout) <= 0)
      fail(n.id, `${prefix}：超时必须大于零。`);
    if (!Number.isInteger(Number(n.data.retries)) || Number(n.data.retries) < 0)
      fail(n.id, `${prefix}：重试次数必须是非负整数。`);
  }
  for (const e of edges)
    if (!ids.has(e.source) || !ids.has(e.target))
      fail(e.source, "连接引用了不存在的节点。");
  const seen = new Set(),
    active = new Set();
  function visit(node) {
    if (active.has(node)) {
      fail(node, "检测到未定义退出条件的循环。");
      return;
    }
    if (seen.has(node)) return;
    active.add(node);
    seen.add(node);
    edges.filter((e) => e.source === node).forEach((e) => visit(e.target));
    active.delete(node);
  }
  if (ids.has("brief")) visit("brief");
  for (const n of nodes)
    if (!seen.has(n.id)) fail(n.id, `${n.data.label}：无法从触发入口到达。`);
  const paths = new Set();
  function checkGates(nodeId, checked, approved) {
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) return;
    if (["AI 任务", "工具调用", "HTTP 请求", "数据转换"].includes(node.data.kind)) {
      checked = false;
      approved = false;
    }
    if (node.data.kind === "独立检查") {
      checked = true;
      approved = false;
    }
    if (node.data.kind === "人工审批" && checked) approved = true;
    const key = `${nodeId}:${checked}:${approved}`;
    if (paths.has(key)) return;
    paths.add(key);
    if (nodeId === "delivery" && (!checked || !approved))
      fail(nodeId, "每条交付路径必须先经过独立检查，再经过 Human 审批。");
    edges
      .filter((e) => e.source === nodeId)
      .forEach((e) => checkGates(e.target, checked, approved));
  }
  checkGates("brief", false, false);
  return issues;
}
