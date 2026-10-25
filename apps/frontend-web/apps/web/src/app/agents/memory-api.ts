import type { components } from "../../lib/api-schema"
import { apiRoute } from "../../lib/api-route"
import { iamApi } from "../../lib/iam-api"

export type Memory = components["schemas"]["EntryView"]
export type Memories = components["schemas"]["MemoryPage"]
export type MemoryHistory = components["schemas"]["MemoryHistoryPage"]
export type MemoryFilter = { kind?: "EXPERIENCE" | "RULE"; state?: "ACTIVE" | "PENDING_REVIEW" | "REJECTED" | "REVOKED" | "EXPIRED" | "SUPERSEDED"; search?: string; project_id?: string; offset: number; limit: number }
export function createMemoryApi(client: Pick<typeof iamApi, "deliveryResultRequest">) {
 return {
  list: (tenant: string, agent: string, query: MemoryFilter, signal?: AbortSignal) => client.deliveryResultRequest<Memories>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/memories", { path: { tenant_id: tenant, agent_principal_id: agent }, query }), { signal }),
  history: (tenant: string, memory: string, offset: number, signal?: AbortSignal) => client.deliveryResultRequest<MemoryHistory>(apiRoute("get", "/tenants/{tenant_id}/memories/{memory_id}/reviews", { path: { tenant_id: tenant, memory_id: memory }, query: { offset, limit: 50 } }), { signal }),
  create: (tenant: string, agent: string, body: { title: string; body: string; kind: "EXPERIENCE" | "RULE"; project_id?: string; ttl_days: number }, key: string) => client.deliveryResultRequest<Memory>(apiRoute("post", "/tenants/{tenant_id}/knowledge", { path: { tenant_id: tenant } }), { headers: { "Idempotency-Key": key }, body: JSON.stringify({ ...body, scope: "AGENT", agent_principal_id: agent, provenance: "Human-authored Agent memory" }) }),
  edit: (tenant: string, memory: string, body: components["schemas"]["MemoryMutation"], key: string) => client.deliveryResultRequest<Memory>(apiRoute("post", "/tenants/{tenant_id}/memories/{memory_id}/edits", { path: { tenant_id: tenant, memory_id: memory } }), { headers: { "Idempotency-Key": key }, body: JSON.stringify(body) }),
  promote: (tenant: string, memory: string, version: number, reason: string, key: string) => client.deliveryResultRequest<Memory>(apiRoute("post", "/tenants/{tenant_id}/memories/{memory_id}/promotions", { path: { tenant_id: tenant, memory_id: memory } }), { headers: { "Idempotency-Key": key }, body: JSON.stringify({ version, reason, target: "TEAM" }) }),
  review: (tenant: string, memory: string, version: number, action: "APPROVED" | "REJECTED" | "REVOKED", reason: string, key: string, conflicts = false) => client.deliveryResultRequest<Memory>(apiRoute("post", "/tenants/{tenant_id}/knowledge/{entry_id}/reviews", { path: { tenant_id: tenant, entry_id: memory } }), { headers: { "Idempotency-Key": key }, body: JSON.stringify({ version, action, reason, conflicts_acknowledged: conflicts }) }),
 }
}
export const memoryApi = createMemoryApi(iamApi)
