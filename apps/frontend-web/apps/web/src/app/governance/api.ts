import { iamApi } from "../../lib/iam-api"

export const inboxKinds = [
  "WORK_ITEM_REVIEW",
  "INTERACTION",
  "HUMAN_VERIFICATION",
  "CLARIFICATION_REPLY",
  "DELIVERY_CONFIRM",
  "ESCALATION_ALERT",
  "MEMORY_RULE_APPROVAL",
  "DISPATCH_PROPOSAL",
  "INTAKE_TRIAGE",
  "EXECUTION_ATTENTION",
  "ENSEMBLE_APPROVAL",
  "ENSEMBLE_SELECTION",
  "AGENT_SPAWN_APPROVAL",
  "PLAN_CONFIRMATION",
  "REPORT_CONFIRMATION",
  "REPORT_BLOCKED",
  "REPORT_OVERDUE",
  "ESCALATION_INTERNAL_EXHAUSTED",
  "REQUIREMENT_CONFLICT",
  "ANDON",
  "EXCEPTION_DECISION",
  "PLAN_APPROVAL",
  "QUALITY_ESCAPE",
  "REVIEW_SHEET",
  "WORKLOG_APPROVAL",
  "PREVIEW_SHARE_APPROVAL",
] as const
export type InboxKind = (typeof inboxKinds)[number]
export type InboxItem = {
  id: string
  kind: InboxKind
  priority: "NORMAL" | "HIGH" | "CRITICAL"
  due_at?: string
  created_at: string
  project_id?: string
  title: string
  reason?: string
  object_type: string
  object_id: string
  source_revision?: string
  source_principal_id?: string
  source_scope?: "MEMBER" | "PROJECT" | "TEAM" | "AGENT"
}
export type InboxPage = {
  items: InboxItem[]
  has_more: boolean
  next_cursor?: string
  capabilities: { kind: InboxKind; status: "AVAILABLE" | "UNAVAILABLE" }[]
}
export type SettingsValue = {
  delegation_max_days: number
  delegation_default_days: number
  delegation_max_cost_cap_micros: number
  delegation_default_cost_cap_micros: number
  sod_human_self_approval: "DENY" | "ALLOW_WITH_REASON"
  default_budget_notify_pct: number
  production_access_max_hours: number
  inbox_review_routing: "REVIEWER_ELSE_ELIGIBLE" | "ALL_ELIGIBLE"
}
export type SettingsView = SettingsValue & { version: number }
export type EscalationRule = {
  kind: string
  version: number
  locked: boolean
  enabled: boolean
  params: Record<string, unknown>
  actions_autonomous: string[]
  actions_hybrid: string[]
}
export type Incident = {
  id: string
  project_id: string
  kind: string
  subject_kind: string
  subject_id: string
  trigger_event_id: string
  reason: string
  facts: Record<string, unknown>
  state: "OPEN" | "ACKNOWLEDGED" | "RESOLVED" | "DISMISSED"
  version: number
  handled_by?: string
  handled_at?: string
  resolution_note?: string
  created_at: string
}
export type IncidentPage = {
  items: Incident[]
  has_more: boolean
  next_cursor?: string
}
export type GovernanceCatalog = {
  policies: { id: string; codes: string[] }[]
  actions: {
    code: string
    non_delegable: boolean
    human_only: boolean
    policy_only: boolean
    delegable: boolean
  }[]
  settings: string
  escalations: string
  verification_override: string
}

export function ruleNeedsStepUp(
  old: EscalationRule,
  next: EscalationRule,
  projectScope = false
): boolean {
  if (old.enabled && !next.enabled) return true
  if (
    old.actions_autonomous.some(
      (value) => !next.actions_autonomous.includes(value)
    ) ||
    old.actions_hybrid.some((value) => !next.actions_hybrid.includes(value))
  )
    return true
  if (old.kind === "LOW_SELF_CONFIDENCE")
    return (
      Number(next.params.min_confidence) < Number(old.params.min_confidence)
    )
  if (old.kind === "EXECUTION_STALLED")
    return Number(next.params.stall_minutes) > Number(old.params.stall_minutes)
  if (old.kind === "CLIENT_REJECTION")
    return (
      old.params.include_partial === true &&
      next.params.include_partial === false
    )
  if (old.kind === "SENSITIVE_PATH" && projectScope) {
    if (Number(next.params.max_paths_checked ?? 2000) > Number(old.params.max_paths_checked ?? 2000)) return true
    const before = old.params.pattern_sets
    const after = next.params.pattern_sets
    if (!Array.isArray(before) || !Array.isArray(after)) return true
    return before.some((set: unknown) => {
      if (!set || typeof set !== "object" || !("enabled" in set) || !set.enabled) return false
      if (!("key" in set) || !("globs" in set) || !Array.isArray(set.globs)) return true
      const replacement = after.find((item: unknown) => item && typeof item === "object" && "key" in item && item.key === set.key && "enabled" in item && item.enabled) as {globs?: unknown} | undefined
      const patterns = replacement?.globs
      return !Array.isArray(patterns) || set.globs.some((glob: unknown) => !patterns.includes(glob))
    })
  }
  if (old.kind === "SENSITIVE_PATH")
    return JSON.stringify(old.params) !== JSON.stringify(next.params)
  return false
}

export function inboxObjectHref(
  tenant: string,
  item: InboxItem
): string | null {
  if (!/^[1-9][0-9]*$/.test(tenant) || !/^[1-9][0-9]*$/.test(item.object_id))
    return null
  if (item.object_type === "report" && item.source_scope === "TEAM")
    return `/admin/${tenant}/reports?scope=TEAM&record_id=${item.object_id}`
  if (item.object_type === "spawn_request" && item.kind === "AGENT_SPAWN_APPROVAL")
    return `/admin/${tenant}/governance/agent-operations?request_id=${item.object_id}`
  if (item.object_type === "escalation_incident" && item.kind === "ESCALATION_ALERT")
    return `/admin/${tenant}/governance/policies?tab=incidents`
  if (item.object_type === "memory" && item.kind === "MEMORY_RULE_APPROVAL") {
    if (item.project_id && !/^[1-9][0-9]*$/.test(item.project_id)) return null
    const query = new URLSearchParams({entry_id: item.object_id})
    if (item.source_scope === "AGENT") {
      if (!item.source_principal_id || !/^[1-9][0-9]*$/.test(item.source_principal_id)) return null
      query.set("scope", "AGENT")
      query.set("agent_principal_id", item.source_principal_id)
    }
    return `/admin/${tenant}${item.project_id ? `/entities/${item.project_id}` : ""}/governance/memory-review?${query}`
  }
  const project = item.project_id
  if (!project || !/^[1-9][0-9]*$/.test(project)) return null
  const root = `/admin/${tenant}/entities/${project}`
  if (item.object_type === "intake" && (item.kind === "INTAKE_TRIAGE" || item.kind === "CLARIFICATION_REPLY")) return `${root}/intake?id=${item.object_id}`
  if (item.object_type === "review_plan" && item.kind === "PLAN_APPROVAL")
    return `${root}/plan-review?plan_id=${item.object_id}`
  if (item.object_type === "dispatch_proposal" && item.kind === "DISPATCH_PROPOSAL")
    return `${root}/dispatch?id=${item.object_id}`
  if (item.object_type === "tolerance_exception" && item.kind === "EXCEPTION_DECISION")
    return `${root}/exceptions?collection=exceptions&id=${item.object_id}`
  if (item.object_type === "preview_share" && item.kind === "PREVIEW_SHARE_APPROVAL")
    return `${root}/previews?collection=shares&id=${item.object_id}`
  if (item.object_type === "execution" && item.kind === "EXECUTION_ATTENTION")
    return `${root}/executions/${item.object_id}`
  if (item.object_type === "execution" && item.kind === "INTERACTION")
    return /^[1-9][0-9]*$/.test(item.id) ? `${root}/executions/${item.object_id}?approval_id=${item.id}` : null
  if (item.object_type === "quality_escape" && item.kind === "QUALITY_ESCAPE")
    return `${root}/escapes?escape_id=${item.object_id}`
  if (item.object_type === "worklog" && item.kind === "WORKLOG_APPROVAL")
    return `${root}/worklogs`
  if (item.object_type === "review_sheet" && item.kind === "REVIEW_SHEET")
    return `${root}/review-sheet?sheet=${item.object_id}`
  if (item.object_type === "work_item" && (item.kind==="ENSEMBLE_APPROVAL" || item.kind==="ENSEMBLE_SELECTION"))
    return /^[1-9][0-9]*$/.test(item.id) ? `${root}/heavy?work_item_id=${item.object_id}&plan_id=${item.id}` : null
  if (item.object_type === "work_item") return `${root}/work-items?work=${item.object_id}`
  if (item.object_type === "plan" || item.object_type === "report") {
    const query = new URLSearchParams()
    if (item.source_scope === "PROJECT" && item.object_type === "report")
      query.set("scope", "PROJECT")
    else if (item.source_principal_id) {
      if (!/^[1-9][0-9]*$/.test(item.source_principal_id)) return null
      query.set("principal_id", item.source_principal_id)
    }
    query.set("record_id", item.object_id)
    return `${root}/${item.object_type === "plan" ? "plans" : "reports"}${query.size ? `?${query}` : ""}`
  }
  if (item.object_type === "project") return `${root}/project`
  // Unsupported source pages do not get guessed paths or dead links.
  return null
}

export function governanceApi(tenant: string) {
  const root = `/tenants/${encodeURIComponent(tenant)}`
  const get = <T>(path: string, signal?: AbortSignal) =>
    iamApi.deliveryResultRequest<T>(
      { method: "GET", url: root + path },
      { signal }
    )
  const put = <T>(path: string, version: number, body: unknown) =>
    iamApi.deliveryResultRequest<T>(
      { method: "PUT", url: root + path },
      { headers: { "If-Match": `"${version}"` }, body: JSON.stringify(body) }
    )
  return {
    inbox: (
      filters: { kind?: string; project_id?: string; cursor?: string },
      signal?: AbortSignal
    ) => {
      const query = new URLSearchParams({ limit: "50" })
      for (const [key, value] of Object.entries(filters))
        if (value) query.set(key, value)
      return get<InboxPage>(`/inbox-items?${query}`, signal)
    },
    item: (kind: InboxKind, id: string, signal?: AbortSignal) => get<InboxItem>(`/inbox-items/${encodeURIComponent(`${kind}:${id}`)}`, signal),
    counts: (filters: {kind?: string; project_id?: string}, signal?: AbortSignal) => {
      const query = new URLSearchParams()
      for (const [key,value] of Object.entries(filters)) if (value) query.set(key,value)
      return get<{total:number;by_kind:Partial<Record<InboxKind,number>>;by_priority:Record<string,number>;revision:string;capabilities:InboxPage["capabilities"]}>(`/inbox-counts?${query}`,signal)
    },
    acknowledge: (item: InboxItem) => iamApi.deliveryResultRequest<InboxItem>({method:"POST",url:`${root}/inbox-items/${encodeURIComponent(`${item.kind}:${item.id}`)}/acknowledgements`},{body:JSON.stringify({revision:item.source_revision})}),
    catalog: (signal?: AbortSignal) =>
      get<GovernanceCatalog>("/governance-catalog", signal),
    settings: (signal?: AbortSignal) =>
      get<SettingsView>("/governance-settings", signal),
    saveSettings: (version: number, value: SettingsValue, proof?: string) =>
      put<SettingsView>("/governance-settings", version, {
        ...value,
        reauth_proof: proof,
      }),
    rules: (signal?: AbortSignal, projectID?: string) =>
      get<EscalationRule[]>(escalationRulePath(undefined, projectID), signal),
    saveRule: (rule: EscalationRule, proof?: string, projectID?: string) =>
      put<EscalationRule>(
        escalationRulePath(rule.kind, projectID),
        rule.version,
        {
          enabled: rule.enabled,
          params: rule.params,
          actions_autonomous: rule.actions_autonomous,
          actions_hybrid: rule.actions_hybrid,
          reauth_proof: proof,
        }
      ),
    incidents: (cursor?: string, signal?: AbortSignal) =>
      get<IncidentPage>(
        `/escalation-incidents?${new URLSearchParams({ limit: "50", ...(cursor ? { cursor } : {}) })}`,
        signal
      ),
    decideIncident: (
      incident: Incident,
      action: "acknowledgements" | "dismissals",
      reason?: string
    ) =>
      iamApi.deliveryResultRequest<Incident>(
        {
          method: "POST",
          url: `${root}/escalation-incidents/${encodeURIComponent(incident.id)}/${action}`,
        },
        {
          headers: { "If-Match": `"${incident.version}"` },
          body: JSON.stringify({ reason }),
        }
      ),
  }
}

export function escalationRulePath(kind?: string, projectID?: string): string {
  if (projectID && !/^[1-9][0-9]{0,18}$/.test(projectID))
    throw new Error("Invalid project identifier")
  return `/escalation-rules${kind ? `/${encodeURIComponent(kind)}` : ""}${projectID ? `?${new URLSearchParams({project_id: projectID})}` : ""}`
}
