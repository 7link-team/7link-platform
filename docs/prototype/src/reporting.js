import { planReportFacts } from "./plans";

export const reportCadences = ["日报", "周报", "月报", "季报", "年报"];
const iso = (date) => date.toISOString().slice(0, 10);
export function reportPeriod(cadence, date) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date || "") || !Number.isFinite(Date.parse(date)) || iso(new Date(date)) !== date)
    throw Error("请选择有效的报告日期。");
  if (!reportCadences.includes(cadence)) throw Error("请选择报告周期。");
  const start = new Date(`${date}T00:00:00Z`), end = new Date(start);
  if (cadence === "周报") {
    start.setUTCDate(start.getUTCDate() - (start.getUTCDay() + 6) % 7);
    end.setTime(start.getTime()); end.setUTCDate(end.getUTCDate() + 6);
  } else if (cadence !== "日报") {
    const month = cadence === "年报" ? 0 : cadence === "季报" ? Math.floor(start.getUTCMonth() / 3) * 3 : start.getUTCMonth();
    start.setUTCMonth(month, 1);
    end.setUTCFullYear(start.getUTCFullYear(), month + (cadence === "年报" ? 12 : cadence === "季报" ? 3 : 1), 0);
  }
  return { periodStart: iso(start), periodEnd: iso(end) };
}
export function dateInZone(timezone, now = new Date()) {
  if (typeof timezone !== "string" || !timezone.trim()) throw Error("请明确报告时区。");
  try {
    const parts = Object.fromEntries(new Intl.DateTimeFormat("en-CA", { timeZone: timezone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(now).map((part) => [part.type, part.value]));
    return `${parts.year}-${parts.month}-${parts.day}`;
  }
  catch { throw Error("请选择有效的 IANA 时区，例如 Asia/Singapore。"); }
}
export function reportSubjects(s) {
  return [
    ...(s.organization?.members || []).filter((p) => p.kind === "Human").map((p) => ({ key: `human:${p.id}`, name: p.name, kind: "Human" })),
    ...s.agents.map((p) => ({ key: `agent:${p.id}`, name: p.name, kind: "Agent" })),
    ...s.projects.map((p) => ({ key: `project:${p.id}`, name: p.name, kind: "项目" })),
    ...(s.organization?.nodes || []).map((p) => ({ key: `team:${p.id}`, name: p.name, kind: "团队" })),
    { key: `tenant:${s.activeTenant}`, name: s.tenants.find((p) => p.id === s.activeTenant)?.name || "当前租户", kind: "租户" },
  ];
}
export function reportOwner(s, subjectKey) {
  const subject = reportSubjects(s).find((item) => item.key === subjectKey);
  if (!subject) throw Error("报告归属对象不存在于当前租户。");
  if (["Human", "Agent"].includes(subject.kind)) return subject.name;
  const members = s.organization?.members || [];
  const owner = subject.kind === "项目" ? s.projects.find((item) => `project:${item.id}` === subjectKey)?.owner
    : subject.kind === "团队" ? members.find((person) => person.id === s.organization.nodes.find((node) => `team:${node.id}` === subjectKey)?.ownerId)?.name
      : members.find((person) => person.id === "H-lin")?.name;
  if (!owner || !members.some((person) => person.kind === "Human" && person.name === owner)) throw Error("请先为报告归属对象指定当前租户的 Human 负责人。");
  return owner;
}
export function periodicReportKey(s, report) {
  const subject = report.subjectKey;
  if (!subject || !report.cadence || !report.periodStart || !report.periodEnd || !report.timezone) return null;
  return JSON.stringify([s.activeTenant, subject, report.cadence, report.periodStart]);
}
export function upsertPeriodicReport(s, options, source, makeId) {
  const subject = reportSubjects(s).find((p) => p.key === options.subjectKey);
  if (!subject) throw Error("报告归属对象不存在于当前租户，请重新选择。");
  const owner = reportOwner(s, subject.key);
  let timezone = options.timezone || "Asia/Singapore";
  const date = options.date || dateInZone(timezone);
  dateInZone(timezone); // Validate the timezone even when the caller supplies a date.
  let period = reportPeriod(options.cadence, date);
  const descriptor = { subjectKey: subject.key, cadence: options.cadence, timezone, ...period };
  const key = periodicReportKey(s, descriptor);
  const existing = s.resources.reports.find((r) => periodicReportKey(s, r) === key);
  // A submitted report is a fixed revision; a scheduled rerun cannot overwrite it.
  if (existing && existing.status !== "草稿") return existing.id;
  if (existing) { timezone = existing.timezone; period = { periodStart: existing.periodStart, periodEnd: existing.periodEnd }; Object.assign(descriptor, { timezone, ...period }); }
  const team = subject.kind === "团队" ? s.organization.nodes.find((node) => `team:${node.id}` === subject.key) : null;
  const humanIds = new Set(subject.kind === "Human" ? [subject.key.slice("human:".length)] : team?.memberIds || []);
  const humanNames = new Set((s.organization?.members || []).filter((person) => person.kind === "Human" && humanIds.has(person.id)).map((person) => person.name));
  const agentId = subject.kind === "Agent" ? subject.key.slice("agent:".length) : null;
  const participantIds = new Set(agentId ? [`agent:${agentId}`] : humanIds);
  const works = s.work.filter((work) => {
    if (subject.kind === "租户") return true;
    if (subject.kind === "项目") return `project:${work.project}` === subject.key;
    const teamIds = work.team ? [work.team.coordinatorId, work.team.receiverId, ...(work.team.executorIds || []), ...(work.team.checkerIds || [])] : [];
    if (teamIds.some((id) => participantIds.has(id))) return true;
    if (agentId) return !work.team && work.agentId === agentId;
    return work.ownerId ? humanIds.has(work.ownerId) : humanNames.has(work.owner);
  });
  const effort = periodicWorklogFacts(s, descriptor);
  const logs = effort.logs;
  const facts = { ...periodicReportFacts(s, descriptor), effort: { logIds: logs.map(log => log.id), unknownLogIds: effort.unknownLogIds } };
  const references = [...facts.facts.map(fact => ({ kind: "work", id: fact.workId, version: fact.version, acceptanceFactIds: [fact.id] })),
    ...logs.map(log => ({ kind: "worklog", id: log.id, version: log.revision || 1 }))];
  const generatedSummary = `当前关联 ${works.length} 项工作（当前上下文，不代表周期完成）；本周期有固定接受事实的工作 ${facts.completed} 项、接受裁决 ${facts.facts.length} 次；${facts.unknownWorkIds.length ? `另有 ${facts.unknownWorkIds.length} 项历史接受日期或归属未知，完整完成数不可确定；` : ""}本周期有 ${logs.length} 条明确归属投入记录${effort.unknownLogIds.length ? `，另有 ${effort.unknownLogIds.length} 条日期或归属未知` : ""}、${facts.plans.length} 份已确认计划事实。请核对进展、风险及未审核项。`;
  if (existing) {
    if (existing.owner !== owner || existing.generatedSummary !== generatedSummary || JSON.stringify(existing.generatedFacts) !== JSON.stringify(facts) || JSON.stringify(existing.suggestedReferences) !== JSON.stringify(references)) {
      existing.generationHistory ??= [];
      existing.generationHistory.push({ revision: existing.revision || 1, generatedSummary: existing.generatedSummary, generatedFacts: existing.generatedFacts, suggestedReferences: existing.suggestedReferences, source: existing.generatedBy, time: existing.generatedAt });
      existing.owner = owner; existing.subjectName = subject.name;
      existing.revision = (existing.revision || 1) + 1;
      existing.generatedSummary = generatedSummary; existing.suggestedReferences = references; existing.generatedFacts = facts;
      existing.generatedAt = new Date().toISOString(); existing.generatedBy = source;
    }
    return existing.id;
  }
  const record = { id: makeId("RP"), ...descriptor, name: `${subject.name} · ${period.periodStart} ${options.cadence}`,
    owner, subjectName: subject.name, status: "草稿", cycle: `${period.periodStart} — ${period.periodEnd}`,
    summary: "", risk: "", references, generatedSummary, generatedFacts: facts, suggestedReferences: structuredClone(references),
    generatedAt: new Date().toISOString(), generatedBy: source, source, revision: 1 };
  s.resources.reports.unshift(record);
  return record.id;
}

export function validatePeriodicReport(s, report) {
  if (!report.subjectKey && !report.cadence && !report.timezone) return null; // Unknown legacy records stay unknown.
  if (!reportSubjects(s).some((subject) => subject.key === report.subjectKey)) throw Error("报告归属对象不存在于当前租户。");
  if (!report.timezone) throw Error("请明确报告时区。");
  dateInZone(report.timezone);
  const period = reportPeriod(report.cadence, report.periodStart);
  if (period.periodStart !== report.periodStart || period.periodEnd !== report.periodEnd) throw Error("报告起止日期必须匹配所选日历周期。");
  return periodicReportKey(s, report);
}

// Acceptance events retain their original subject and time even after reassignment or rework.
export function periodicReportFacts(state, report, now = new Date()) {
  const facts = new Map(), unknownWorkIds = new Set();
  const scoped = work => {
    if (!report.subjectKey || report.subjectKey.startsWith("tenant:")) return true;
    if (report.subjectKey.startsWith("project:")) return report.subjectKey === `project:${work.project}`;
    const members = report.subjectKey.startsWith("team:") ? state.organization?.nodes.find(node => `team:${node.id}` === report.subjectKey)?.memberIds || [] : [report.subjectKey.startsWith("human:") ? report.subjectKey.slice(6) : report.subjectKey];
    return [...(work.production?.executorIds || []), ...(work.team?.executorIds || []), work.ownerId, ...(state.organization?.members || []).filter(person => person.kind === "Human" && person.name === work.owner).map(person => person.id)].some(id => members.includes(id));
  };
  for (const work of state.work) {
    const entries = work.acceptanceFacts || [];
    if (scoped(work) && (work.accepted || work.status === "已完成" || work.history?.some(item => item.accepted)) && !entries.length) unknownWorkIds.add(work.id);
    for (const fact of entries) {
      const belongs = report.subjectKey && fact.subjectKeys?.includes(report.subjectKey);
      if (!fact.id || fact.workId !== work.id || !Number.isFinite(Date.parse(fact.time)) || !Array.isArray(fact.subjectKeys) || !report.subjectKey || !report.timezone) {
        if (belongs || scoped(work)) unknownWorkIds.add(work.id);
        continue;
      }
      if (!belongs || Date.parse(fact.time) > now.getTime()) continue;
      const day = dateInZone(report.timezone, new Date(fact.time));
      if (day >= report.periodStart && day <= report.periodEnd) facts.set(fact.id, structuredClone(fact));
    }
  }
  const plans = (state.resources.plans || []).filter(plan => plan.subjectKey === report.subjectKey && plan.confirmedSnapshot && plan.periodStart >= report.periodStart && plan.periodEnd <= report.periodEnd).map(plan => ({ id: plan.id, ...planReportFacts(state, plan, now, { historical: true }) }));
  return { facts: [...facts.values()], factKeys: [...facts.keys()], completed: new Set([...facts.values()].map(fact => fact.workId)).size, unknownWorkIds: [...unknownWorkIds], plans };
}

export function immutableSubjectKeys(state, project, memberIds) {
  const identities = [...new Set(memberIds.filter(Boolean))];
  const keys = [`tenant:${state.activeTenant}`, ...(project ? [`project:${project}`] : []), ...identities.map(id => id.startsWith("agent:") ? id : `human:${id}`)];
  for (const node of state.organization.nodes) if (node.memberIds?.some(id => identities.includes(id))) {
    const visited = new Set(); let current = node;
    while (current && !visited.has(current.id)) { visited.add(current.id); keys.push(`team:${current.id}`); current = state.organization.nodes.find(item => item.id === current.parentId); }
  }
  return [...new Set(keys)];
}
export function worklogSubjectMatch(state, log, subjectKey) {
  const [kind, identity] = (subjectKey || "").split(":");
  if (!subjectKey) return null;
  if (log.scopeSnapshot) {
    if (log.scopeSnapshot.subjectKeys?.includes(subjectKey)) return true;
    return log.scopeSnapshot.unknownScopes?.includes(kind) ? null : false;
  }
  // Existing stable personal IDs remain usable; legacy names never certify historical membership.
  if (kind === "tenant") return identity === state.activeTenant;
  if (kind === "human" && log.ownerId && (!log.durationKind || log.durationKind === "Human 工时")) return log.ownerId === identity;
  if (kind === "agent" && log.ownerAgentId) return log.ownerAgentId === identity;
  if (kind === "human" && log.ownerAgentId) return false;
  if (kind === "agent" && log.ownerId) return false;
  return null;
}
export function periodicWorklogFacts(state, report) {
  const logs = [], unknownLogIds = [];
  for (const log of state.resources.worklogs) {
    const match = worklogSubjectMatch(state, log, report.subjectKey);
    if (match === false) continue;
    const dateValid = /^\d{4}-\d{2}-\d{2}$/.test(log.date || "") && Number.isFinite(Date.parse(log.date)) && new Date(log.date).toISOString().slice(0,10) === log.date;
    if (!dateValid) { unknownLogIds.push(log.id); continue; }
    if (log.date < report.periodStart || log.date > report.periodEnd) continue;
    if (match === null) unknownLogIds.push(log.id); else logs.push(log);
  }
  return { logs, unknownLogIds };
}
