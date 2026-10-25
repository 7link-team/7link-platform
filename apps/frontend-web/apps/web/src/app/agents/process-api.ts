import { iamApi } from "../../lib/iam-api"

export type ProcessEvent = { id: string; agent_principal_id: string; kind: string; machine_id?: string; pid: number; restart_count: number; reason: string; occurred_at: string }
export type ProcessPage = { items: ProcessEvent[]; has_more: boolean }
export type RunState = { state: string; process_status: string; restart_count: number; restart_exhausted: boolean }
const route = (tenant: string, agent: string, suffix: string) => `/tenants/${encodeURIComponent(tenant)}/agents/${encodeURIComponent(agent)}/${suffix}`
export const createProcessApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  list: (tenant: string, agent: string, offset: number, search: string, signal?: AbortSignal) => api.deliveryResultRequest<ProcessPage>({ method: "GET", url: `${route(tenant, agent, "process-events")}?${new URLSearchParams({ offset: String(offset), limit: "50", search })}` }, { signal, cache: "no-store" }),
  change: (tenant: string, agent: string, desired: "ACTIVE" | "STOPPED", key: string) => api.deliveryResultRequest<RunState>({ method: "PUT", url: route(tenant, agent, "run-state") }, { body: JSON.stringify({ desired }), headers: { "Idempotency-Key": key } }),
})
export const processApi = createProcessApi(iamApi)
