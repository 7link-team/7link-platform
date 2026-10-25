import { iamApi } from "../../lib/iam-api"

export type AgentProfile = {
  id: string; principal_id: string; name: string; handle: string; persona: string | null
  avatar_url?: string; avatar_style: string; avatar_seed: number; system_prompt: string; reasoning_effort: string | null
  runtime: string; model: string; authorization_mode: string; max_context_tokens: number
  mcp_allowlist: string[]; capability_tags: string[]; default_channel_names: string[]
  memory_policy: Record<string, unknown>; max_parallel_items: number; max_risk: string
  auto_restart: boolean; config_revision: number; running_config_revision: number | null; version: number
}
export type ProfilePatch = Partial<Pick<AgentProfile, "name" | "persona" | "avatar_style" | "avatar_seed" | "system_prompt" | "reasoning_effort" | "max_context_tokens" | "mcp_allowlist" | "capability_tags" | "default_channel_names" | "memory_policy" | "max_parallel_items" | "max_risk" | "auto_restart">> & { version: number }
export type AvatarCandidate = { style: string; seed: number; url: string }
const route = (tenant: string, agent: string, suffix: string) => `/tenants/${encodeURIComponent(tenant)}/agents/${encodeURIComponent(agent)}/${suffix}`
export const createProfileApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  read: (tenant: string, agent: string, signal?: AbortSignal) => api.deliveryResultRequest<AgentProfile>({ method: "GET", url: route(tenant, agent, "profile") }, { signal, cache: "no-store" }),
  update: (tenant: string, agent: string, patch: ProfilePatch, key: string) => api.deliveryResultRequest<AgentProfile>({ method: "PATCH", url: route(tenant, agent, "profile") }, { body: JSON.stringify(patch), headers: { "Idempotency-Key": key } }),
  candidates: (tenant: string, agent: string, style: string, batch: number, signal?: AbortSignal) => api.deliveryResultRequest<AvatarCandidate[]>({ method: "GET", url: `${route(tenant, agent, "avatar-candidates")}?${new URLSearchParams({ style, batch: String(batch) })}` }, { signal, cache: "no-store" }),
})
export const profileApi = createProfileApi(iamApi)
