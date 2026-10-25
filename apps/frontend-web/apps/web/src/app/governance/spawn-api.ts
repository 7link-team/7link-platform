import { iamApi } from "../../lib/iam-api"

export type SpawnScope = {
  project_ids: string[]; max_active_agents: number; max_spawns_per_day: number; allowed_roles: string[]
  allowed_machine_ids: string[]; allowed_models: {runtime: string; model: string}[]; max_effort: string
  budget_cap_micros: number; per_agent_cap_micros: number; agent_ttl_hours: number; idle_retire_hours: number; approval_mode: string
}
export type SpawnGrant = SpawnScope & {
  id: string; grantee_agent_id: string; grantee_principal_id: string; granted_by: string
  state: string; effective_state: string; effective_approval_mode: string; active_count: number; spawned_today: number
  spent_micros: string; reason: string; starts_at: string; ends_at: string; end_reason?: string; version: number
}
export type SpawnGrantInput = SpawnScope & {grantee_agent_id: string; duration_hours: number; reason: string; team_scope_confirmed: boolean; reauth_proof?: string; replaces_grant_id?: string}
export type SpawnMember = {agent_id: string; state: string; release_reason?: string; spent_micros: string; per_agent_cap_micros: string; reserved_at: string; released_at?: string}

export function spawnApi(tenant: string) {
  const root = `/tenants/${encodeURIComponent(tenant)}/spawn-grants`
  const request = <T>(method: "GET" | "POST" | "PATCH", url: string, body?: unknown, headers?: Record<string, string>, signal?: AbortSignal) => iamApi.deliveryResultRequest<T>({method, url}, {body: body === undefined ? undefined : JSON.stringify(body), headers, signal})
  return {
    list: (state: string, offset: number, signal?: AbortSignal) => iamApi.deliveryResultPage<SpawnGrant>({method: "GET", url: `${root}?${new URLSearchParams({limit: "50", offset: String(offset), ...(state ? {state} : {})})}`}, signal),
    get: (id: string, signal?: AbortSignal) => request<SpawnGrant>("GET", `${root}/${encodeURIComponent(id)}`, undefined, undefined, signal),
    members: (id: string, offset = 0, signal?: AbortSignal) => iamApi.deliveryResultPage<SpawnMember>({method: "GET", url: `${root}/${encodeURIComponent(id)}/members?${new URLSearchParams({limit: "50", offset: String(offset)})}`}, signal),
    grant: (body: SpawnGrantInput, key: string) => request<SpawnGrant>("POST", root, body, {"Idempotency-Key": key}),
    update: (value: SpawnGrant, body: SpawnScope & {reason: string; reauth_proof?: string; team_scope_confirmed: boolean}) => request<SpawnGrant>("PATCH", `${root}/${encodeURIComponent(value.id)}`, body, {"If-Match": `"${value.version}"`}),
    revoke: (value: SpawnGrant, reason: string, key: string) => request<SpawnGrant>("POST", `${root}/${encodeURIComponent(value.id)}/revocations`, {reason}, {"If-Match": `"${value.version}"`, "Idempotency-Key": key}),
  }
}

const values = (value: unknown) => String(value ?? "").split(/[,\n]/).map(value => value.trim()).filter(Boolean)
export function spawnFormInput(form: Record<string, unknown>): SpawnGrantInput {
  const project_ids = values(form.projects), allowed_machine_ids = values(form.machines)
  if ([...project_ids, ...allowed_machine_ids, String(form.supervisor)].some(value => !/^[1-9][0-9]{0,18}$/.test(value))) throw new Error("invalid")
  const integer = (key: string, min: number, max: number) => {const value = Number(form[key]); if (!Number.isSafeInteger(value) || value < min || value > max) throw new Error("invalid"); return value}
  const budget_cap_micros = integer("budget", 1000000, Number.MAX_SAFE_INTEGER)
  const allowed_models = [...values(form.codex_models).map(model => ({runtime: "CODEX", model})), ...values(form.claude_models).map(model => ({runtime: "CLAUDE_CODE", model}))]
  const allowed_roles = Array.isArray(form.roles) ? form.roles.map(String) : []
  if (!allowed_machine_ids.length || !allowed_models.length || allowed_models.length > 20 || !allowed_roles.length || allowed_roles.some(role => !["EXECUTOR", "COORDINATOR"].includes(role)) || (project_ids.length === 0 && form.approval === "AUTO")) throw new Error("invalid")
  return {project_ids, allowed_machine_ids, allowed_models, allowed_roles, grantee_agent_id: String(form.supervisor), max_active_agents: integer("active", 1, 10), max_spawns_per_day: integer("daily", 1, 20), budget_cap_micros, per_agent_cap_micros: integer("per_agent", 1, budget_cap_micros), agent_ttl_hours: integer("ttl", 24, 720), idle_retire_hours: integer("idle", 4, 336), max_effort: String(form.effort), approval_mode: String(form.approval), duration_hours: integer("duration", 2, 2160), reason: String(form.reason), team_scope_confirmed: form.team_confirm === true}
}
