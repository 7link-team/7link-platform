import { projectRecordWriteProblem } from "./project-lifecycle";
import { planConfirmer } from "./plans";

const human = (s, id) => s.organization.members.find(person => person.id === id && person.kind === "Human");
const byName = (people, name) => {
  const matches = people.filter(person => person.name === name);
  return matches.length === 1 ? matches[0] : null;
};
const identity = person => person ? { id: person.id, kind: person.kind, name: person.name } : null;
export function reportTenantOwner(s) {
  const tenant = s.tenants.find(item => item.id === s.activeTenant);
  const id = s.planningSettings?.tenantOwnerId || tenant?.ownerId || (tenant?.role === "owner" ? "H-lin" : null);
  return identity(human(s, id));
}
export function reportAuthor(s, report) {
  const members = s.organization.members.filter(person => person.kind === "Human");
  const agents = s.agents.map(agent => ({ ...agent, kind: "Agent" }));
  if (report.subjectKey) {
    const [kind, id] = report.subjectKey.split(":");
    if (kind === "human") return identity(human(s, id));
    if (kind === "agent") return identity(agents.find(agent => agent.id === id));
    if (kind === "project") {
      const project = s.projects.find(item => item.id === id);
      return identity(project?.ownerId ? human(s, project.ownerId) : byName(members, project?.owner));
    }
    if (kind === "team") return identity(human(s, s.organization.nodes.find(node => node.id === id)?.ownerId));
    if (kind === "tenant" && id === s.activeTenant) return reportTenantOwner(s);
    return null;
  }
  if (report.authorId && report.authorKind) return identity([...members, ...agents].find(person => person.id === report.authorId && person.kind === report.authorKind));
  return identity(byName([...members, ...agents], report.owner));
}
export function reportReviewAuthority(s, report) {
  const author = reportAuthor(s, report);
  if (!author) return { id: null, issue: "报告作者身份不明确，请先修复归属记录。" };
  const scope = report.subjectKey?.split(":")[0];
  // BR-10-20's TEAM is the one tenant-wide report (BR-10-24), not
  // every organization node exposed as a team subject in the prototype.
  if (scope === "tenant") return { id: "SYSTEM:team_top", system: true, basis: "团队所有者本人提交后系统确认" };
  if (["project", "team"].includes(scope)) {
    const owner = reportTenantOwner(s);
    const label = scope === "team" ? "组织团队" : "项目";
    if (!owner || owner.id === author.id) return { id: null, issue: `${label}报告需要独立的 Human 确认人；未配置有效上级，不能本人自审。` };
    return { id: owner.id, basis: `团队所有者确认${label}报告` };
  }
  const key = `${author.kind === "Agent" ? "agent" : "human"}:${author.id}`;
  // Reuse member hierarchy resolution; an explicitly invalid Agent owner never
  // grants an unrelated tenant manager permission as a fallback.
  return planConfirmer({ ...s, planningSettings: { ...s.planningSettings,
    tenantOwnerId: s.planningSettings?.tenantOwnerId || reportTenantOwner(s)?.id } }, key);
}
export function reportEditProblem(s, report, { simulateAgent = false } = {}) {
  const archived = projectRecordWriteProblem(s,report); if (archived) return archived;
  if (s.offline) return "离线中，请恢复连接后操作。";
  if (report.status === "已接受") return "已接受报告不可编辑，请由确认人或租户所有者撤销确认并填写理由后更正。";
  if (!["owner", "executor"].includes(s.role) || !human(s, "H-lin")) return "当前身份无权修改或提交报告。";
  const author = reportAuthor(s, report);
  if (!author) return "报告作者身份不明确，请先修复归属记录。";
  if (author.kind === "Human") return author.id === "H-lin" ? null : "Human 报告只能由本人确认，不能替其他成员修改或提交。";
  const agent = s.agents.find(item => item.id === author.id);
  if (["退役", "待批准", "已禁用"].includes(agent?.status)) return "Agent 当前不可提交新报告；历史报告保留，代提交策略尚未配置。";
  if (reportReviewAuthority(s, report).id !== "H-lin") return "仅 Agent 的负责人类可操作本地报告建议。";
  return simulateAgent ? null : "Agent 报告须明确选择本地模拟上报，不冒充 Human 本人确认。";
}
// Definition/body fingerprint excludes changing source data and display-only names.
export function reportSubmissionContent(report) {
  const keys = ["subjectKey", "cadence", "periodStart", "periodEnd", "timezone", "name", "summary", "risk", "claimedCompleted", "references"];
  // Preserve the fingerprint of legacy fixed submissions with no Plan binding.
  if (report.nextPlanBinding != null) keys.push("nextPlanBinding");
  if (report.currentPlanBinding != null) keys.push("currentPlanBinding");
  if (report.content != null) keys.push("content");
  if (report.contentRequired === true) keys.push("contentRequired");
  return Object.fromEntries(keys.map(key => [key, structuredClone(report[key] ?? null)]));
}
export function reportTransitionIssue(s, report, status, options = {}) {
  const archived = projectRecordWriteProblem(s,report); if (archived) return archived;
  if (!["已提交", "已接受", "草稿"].includes(status)) return "未知报告动作。";
  if (options.action === "REOPEN" && status !== "草稿") return "撤销确认只能将已接受报告退回更正。";
  if (s.offline) return "离线中，请恢复连接后操作。";
  if (status === "已提交") {
    if (reportReviewAuthority(s, report).system && s.role !== "owner") return "团队顶层报告须由具有所有者权限的 Human 本人提交。";
    const permission = reportEditProblem(s, report, options);
    if (permission) return permission;
    return report.status === "草稿" ? null : "只有草稿可以本人确认上报，请先更正为新修订。";
  }
  if (s.role !== "owner" || !human(s, "H-lin")) return "报告审核与退回需要负责人权限。";
  if (status === "草稿" && options.action === "REOPEN") {
    if (report.status !== "已接受") return "只有已接受报告可以撤销确认。";
    const confirmedBy = report.approval?.actorKind === "Human" ? report.approval.actorId : null;
    return confirmedBy === "H-lin" || reportTenantOwner(s)?.id === "H-lin" ? null : "只有原确认人或租户所有者可以撤销确认。";
  }
  if (!["已提交", "待审核"].includes(report.status)) return "只有已提交或待审核报告可以审核或退回，不能跳过本人确认。";
  const author = reportAuthor(s, report);
  if (author?.kind === "Human" && author.id === "H-lin") return "不能本人审核或退回本人报告；请由有效 Human 上级确认。";
  const authority = reportReviewAuthority(s, report);
  if (!authority.id) return authority.issue;
  if (authority.system) return "团队顶层报告由所有者本人提交后系统确认，无需重复人工审核。";
  if (authority.id !== "H-lin") return "当前 Human 不是这份报告的指定确认人。";
  if (report.submission?.confirmerId && report.submission.confirmerId !== authority.id) return "报告确认人已变化，请重新核对并提交。";
  if (status === "已接受") {
    const submitted = report.submission;
    if (!submitted || !submitted.confirmerId || submitted.confirmerId !== authority.id || submitted.revision !== (report.revision || 1) || submitted.authorId !== author?.id || submitted.authorKind !== author?.kind ||
      JSON.stringify(submitted.content) !== JSON.stringify(reportSubmissionContent(report))) return "缺少当前作者与修订的固定提交记录，请退回更正后重新提交。";
  }
  return null;
}
