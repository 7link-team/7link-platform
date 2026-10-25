import { iamApi } from "../../lib/iam-api"

export const delegationActions = ["decision_approve", "decision_reject", "decision_request_changes", "code_merge", "intake_convert"] as const
export type Delegation = {
  id: string; project_id: string; grantee_principal_id: string; grantee_agent_id: string; grantor_principal_id: string
  actions: string[]; work_item_types: string[]; max_risk: "LOW" | "MEDIUM"; cost_cap_micros: number; max_rework: number
  starts_at: string; ends_at: string; use_count?: number; uses?: {work_item_id: string; decision_id: string; reason: string; at: string}[]; reason: string; state: "SCHEDULED" | "ACTIVE" | "REVOKED" | "EXPIRED"; end_reason?: string; ended_at?: string; end_note?: string; version: number
}
export type DelegationGrant = Pick<Delegation, "project_id" | "grantee_principal_id" | "actions" | "work_item_types" | "max_risk" | "cost_cap_micros" | "max_rework" | "reason"> & {duration_minutes: number; starts_at?: string; replaces_delegation_id?: string; reauth_proof?: string}

export function delegationApi(tenant: string) {
  const root = `/tenants/${encodeURIComponent(tenant)}/delegations`
  return {
    list: async (filters: {project_id?: string; state?: string; before_id?: string}, signal?: AbortSignal) => {
      const query = new URLSearchParams({limit: "51"})
      if (filters.before_id) query.set("before_id", filters.before_id)
      if (filters.project_id) query.set("project_id", filters.project_id)
      if (filters.state) query.set("state", filters.state)
      const page = await iamApi.deliveryResultPage<Delegation>({method: "GET", url: `${root}?${query}`}, signal)
      return {...page, items: page.items.slice(0, 50), hasMore: page.items.length > 50}
    },
    get: (id: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<Delegation>({method: "GET", url: `${root}/${encodeURIComponent(id)}`}, {signal}),
    grant: (body: DelegationGrant, key: string) => iamApi.deliveryResultRequest<Delegation>({method: "POST", url: root}, {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
    revoke: (id: string, reason: string, key: string) => iamApi.deliveryResultRequest<Delegation>({method: "POST", url: `${root}/${encodeURIComponent(id)}/revocations`}, {headers: {"Idempotency-Key": key}, body: JSON.stringify({reason})}),
  }
}

// Repeated boundaries preserve previous pages across refresh and browser history.
export function delegationPageSearch(search: URLSearchParams, nextBoundary?: string): URLSearchParams {
  const next = new URLSearchParams(search)
  const boundaries = next.getAll("before_id")
  next.delete("offset")
  next.delete("delegation_id")
  if (nextBoundary) boundaries.push(nextBoundary)
  else boundaries.pop()
  next.delete("before_id")
  for (const boundary of boundaries) next.append("before_id", boundary)
  return next
}

export function delegationIsCurrent(value: Pick<Delegation, "state" | "starts_at" | "ends_at">, now = Date.now()): boolean {
  return value.state === "ACTIVE" && Date.parse(value.starts_at) <= now && now < Date.parse(value.ends_at)
}

export function scheduledDelegationStart(value: unknown, now = Date.now()): string | undefined {
  const input = String(value ?? "").trim()
  if (!input) return undefined
  const at = new Date(input)
  if (!Number.isFinite(at.getTime()) || at.getTime() <= now) throw new Error("Invalid future activation time")
  return at.toISOString()
}
