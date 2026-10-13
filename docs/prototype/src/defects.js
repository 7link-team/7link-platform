// Defects are work items. This projection adapts their professional fields to QA evidence.
export const defectFields = ["severity", "priority", "steps", "expected", "actual", "environment", "commit", "affectedWorkId", "affectedVersion"];
export function defectStatus(work) {
  if (work.status === "已取消") return "已取消";
  if (work.defect?.stage === "CLOSED") return "已关闭";
  if (work.defect?.repairVersion && (!work.defect.repairWorkVersion || work.defect.repairWorkVersion === work.version) && work.status === "待验收") return "待验证";
  return "待修复";
}
export function defectRecords(s) {
  return (s.work || []).filter(work => work.workKind === "BUG").map(work => ({
    ...structuredClone(work.defect), id: work.id, name: work.title, owner: work.agent === "未分派" ? work.owner : work.agent,
    status: defectStatus(work), work: work.defect?.affectedWorkId, version: work.defect?.affectedVersion,
    revision: work.defect?.revision || 1,
  }));
}
export function migrateDefects(s) {
  if (!s.resources?.defects) return;
  s.defectMigrationIssues ??= [];
  for (const old of s.resources.defects) {
    const affected = s.work.find(work => work.id === old.work);
    const collision = s.work.some(work => work.id === old.id);
    const versionExists = /^v[1-9]\d*$/.test(old.version || "") && affected &&
      (old.version === `v${affected.version}` || affected.history?.some(entry => old.version === `v${entry.version}`));
    if (collision || !affected || !old.id || !versionExists || !["草稿", "待修复", "待验证", "已关闭", "已取消"].includes(old.status)) {
      s.defectMigrationIssues.push({ status: "待修复迁移", reason: collision ? "工作编号冲突" : "缺少有效关联工作、编号、版本或状态", legacy: structuredClone(old) });
      continue;
    }
    const unique = matches => matches?.length === 1 ? matches[0] : null;
    const human = unique(s.organization?.members.filter(person => person.kind === "Human" && person.name === old.owner));
    const accountable = human || unique(s.organization?.members.filter(person => person.kind === "Human" && person.name === affected.owner));
    const agent = unique(s.agents?.filter(agent => agent.name === old.owner));
    s.work.push({ id: old.id, workKind: "BUG", title: old.name, project: affected.project, type: "软件",
      status: old.status === "已取消" ? "已取消" : old.status === "已关闭" ? "历史已关闭" : old.status === "待验证" ? "待验收" : "待开始",
      owner: accountable?.name || "待补负责人", ownerSource: human ? "原缺陷 Human 负责人" : accountable ? `关联工作 ${affected.id} 的 Human 负责人` : "旧记录未声明有效 Human 负责人",
      agent: agent?.name || "未分派", agentId: agent?.id || null, version: 1, due: "待确认", cost: 0,
      plan: false, flow: false, qa: false, accepted: false, delivered: false,
      notes: old.actual || old.name, requirements: [old.expected || "待补缺陷验收条件"],
      defect: { ...structuredClone(old), affectedWorkId: old.work, affectedVersion: old.version,
        stage: old.status === "已关闭" ? "CLOSED" : null, legacy: structuredClone(old), history: structuredClone(old.history || []) },
    });
    const details = s.work.at(-1).defect;
    for (const key of ["id", "name", "owner", "status", "work", "version"]) delete details[key];
  }
  delete s.resources.defects;
}

export function defectQuality(work) {
  const details = work.defect, run = details?.verification, closure = details?.closure;
  const verified = details?.stage === "CLOSED" && work.accepted && closure?.actorKind === "Human" && closure.actorId && closure.time &&
    closure.workVersion === work.version && details.repairWorkVersion === work.version && closure.repairRevision === details.repairRevision &&
    closure.runId === run?.id && run?.status === "通过" && run.source === "mock" &&
    ["Human 手测", "平台独立验证"].includes(run.sourceKind) && run.ownerId && run.evidence?.trim() &&
    run.defect === work.id && run.defectRepairRevision === details.repairRevision &&
    run.work === details.affectedWorkId && run.version === details.repairVersion && details.affectedVersion === details.repairVersion;
  return verified ? "通过" : defectStatus(work) === "待验证" ? "待验证" : "未执行";
}
