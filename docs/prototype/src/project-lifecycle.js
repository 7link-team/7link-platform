// Explicit identities only. Legacy names/scope strings never establish project ownership.
export function recordProjectId(state, record) {
  if (!record) return null;
  if (record.library?.scope === "PROJECT" && typeof record.library.projectId === "string" && record.library.projectId) return record.library.projectId;
  if (typeof record.projectId === "string" && record.projectId) return record.projectId;
  if (typeof record.project === "string" && record.project) return record.project;
  if (typeof record.scope?.projectId === "string" && record.scope.projectId) return record.scope.projectId;
  if (typeof record.subjectKey === "string" && record.subjectKey.startsWith("project:")) return record.subjectKey.slice(8);
  if (["model","agent"].includes(record.targetKind)) { const target = record.targetKind === "model" ? state.resources.models.find(item=>item.id===record.target) : state.agents.find(item=>item.id===record.target); if (target?.projectId) return target.projectId; }
  const workId = record.workId || record.work;
  if (typeof workId === "string") return state.work.find(work => work.id === workId)?.project || null;
  const goal = record.goalId && state.resources.goals.find(goal=>goal.id===record.goalId);
  if (goal) return recordProjectId(state,{projectId:goal.projectId,scope:goal.scope,subjectKey:goal.subjectKey});
  return null;
}
export const projectIsArchived = (state, projectId) => Boolean(projectId && state.projects.find(project => project.id === projectId)?.archivedAt);
export const activeProjects = state => state.projects.filter(project => !project.archivedAt);
export function projectRecordWriteProblem(state, record) {
  return projectIsArchived(state, recordProjectId(state, record)) ? "所属项目已归档，业务记录只读；请由有权 Human 明确恢复项目。" : "";
}
export function projectArchiveImpact(state, projectId) {
  const items = [], add = (kind, id, title, status, blocked, href, action) => items.push({kind,id,title,status,blocked,href,action});
  const works = state.work.filter(work => work.project === projectId);
  for (const work of works) {
    const terminal = ["已完成", "已取消"].includes(work.status);
    const paused = work.status === "等待" && work.archivePause?.source === "LOCAL_SIMULATION" && work.archivePause.workVersion === work.version;
    if (!terminal) add("work",work.id,work.title,paused ? "已明确暂停推进（本地模拟）" : work.status,!paused,`/p/${projectId}/work-items/${work.id}`, ["进行中","待开始","等待"].includes(work.status) ? "pause-work" : null);
  }
  for (const source of state.work) for (const handoff of source.handoffs || []) {
    const target = state.work.find(work => work.id === handoff.targetWorkId);
    if ((source.project === projectId || target?.project === projectId) && !["已接收","已接受","已拒绝","已取消"].includes(handoff.status))
      add("handoff",handoff.id,`${source.title} → ${target?.title || "接收工作不可核对"} · 内部交接`,handoff.status || "状态未核实",true,`/p/${source.project}/work-items/${source.id}`);
  }
  for (const plan of state.resources.testplans || []) for (const batch of plan.batches || []) if (batch.status === "进行中" && batch.workContexts?.some(context=>works.some(work=>work.id===context.workId))) add("test-run",batch.id,`${plan.name} · 检查批次`,batch.status,true,`/testplans/${plan.id}`);
  for (const approval of state.resources.approvals || []) if (recordProjectId(state,approval) === projectId && approval.status === "待审核") add("approval",approval.id,approval.name,approval.status,true,`/approvals/${approval.id}`);
  for (const delivery of state.resources.deliveries || []) if (recordProjectId(state,delivery) === projectId && !["已接受","部分接受","已拒收"].includes(delivery.status)) add("delivery",delivery.id,delivery.name,delivery.status,true,`/deliveries/${delivery.id}`);
  for (const schedule of state.resources.schedules || []) if (recordProjectId(state,schedule) === projectId) add("schedule",schedule.id,schedule.name,schedule.status,schedule.status !== "暂停",`/schedules/${schedule.id}`,"pause-schedule");
  for (const plan of state.resources.plans || []) {
    const linked = (plan.items || []).filter(item => recordProjectId(state,item) === projectId);
    if (recordProjectId(state,plan) === projectId || linked.length) add("plan",plan.id,`${plan.owner || plan.subjectKey} · 周期计划`,plan.status,plan.status === "已提交",`/plans/${plan.id}`);
  }
  for (const lease of state.projectLeases || []) if (lease.projectId === projectId) {
    const released = lease.state === "RELEASED" && lease.disposition?.source === "LOCAL_SIMULATION" && lease.disposition.leaseId === lease.id && lease.disposition.result === "RELEASED";
    add("lease",lease.id,lease.kind === "terminal" ? "本地模拟终端租约" : "本地模拟预览租约",released ? "已记录本地模拟释放" : lease.state,!released,lease.workId ? `/p/${projectId}/work-items/${lease.workId}` : `/machines/${lease.nodeId}?tab=${lease.kind === "terminal" ? "terminal" : "preview"}`,"release-lease");
  }
  return { items, blockers: items.filter(item => item.blocked), unknownKnowledge: (state.resources.knowledge || []).filter(record => !recordProjectId(state,record) && typeof record.scope === "string" && record.scope !== "租户" && record.scope !== "私人").length,
    disclosure: "仅核对具有明确项目 ID 的本地记录。旧节点会话、未绑定项目的资料与审批不纳入该项目覆盖；不声明真实进程、端口或隧道已释放。" };
}
