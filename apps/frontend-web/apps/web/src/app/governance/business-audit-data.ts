export const businessAuditLabels = {
  "en-US": {hint: "Business audit shows only currently visible events. Missing historical labels remain blank. Full details, integrity and exports require their own permissions.", authorityKind: "Authority kind", authorityRef: "Authority reference", origin: "Agent origin", grant: "Spawn grant ID", lag: "Index lag (seconds)", supervisor: "Supervisor Agent", human: "Human created", agent: "Agent spawned"},
  "zh-CN": {hint: "业务审计仅显示当前有权查看的事件。缺失的历史标签保持空白。完整明细、完整性校验和导出分别需要对应权限。", authorityKind: "授权类型", authorityRef: "授权引用", origin: "Agent 来源", grant: "创建授权 ID", lag: "索引延迟（秒）", supervisor: "主管 Agent", human: "人工创建", agent: "Agent 创建"},
  "ms-MY": {hint: "Audit perniagaan hanya memaparkan peristiwa yang kini boleh dilihat. Label sejarah yang tiada kekal kosong. Butiran penuh, integriti dan eksport memerlukan kebenaran masing-masing.", authorityKind: "Jenis kuasa", authorityRef: "Rujukan kuasa", origin: "Asal Agent", grant: "ID geran penciptaan", lag: "Kelewatan indeks (saat)", supervisor: "Agent penyelia", human: "Dicipta manusia", agent: "Dicipta Agent"},
} as const

export const businessAuditFilters = ["project_id", "module", "event_type", "actor_id", "actor_kind", "authority_kind", "authority_ref", "target_type", "target_id", "outcome", "occurred_at_gte", "occurred_at_lt", "origin", "spawn_grant_id"] as const
export function businessAuditPage(params: URLSearchParams) {
  const value = Number(params.get("audit_page"))
  return Number.isSafeInteger(value) && value > 0 && Number.isSafeInteger((value - 1) * 50) ? value : 1
}
export function businessAuditQuery(params: URLSearchParams) {
  const q: Record<string, string | number> = {limit: 50, offset: (businessAuditPage(params) - 1) * 50}
  for (const key of businessAuditFilters) {const value = params.get(`audit_${key}`); if (value) q[key] = value}
  return q
}
