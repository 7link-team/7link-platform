import { iamApi } from "../../lib/iam-api"
import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"

export type ResetHistory = { items: components["schemas"]["ResetHistoryView"][]; has_more: boolean; next_cursor?: string }
export const createResetHistoryApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  list: (tenant: string, agent: string, cursor: string, signal?: AbortSignal) => api.deliveryResultRequest<ResetHistory>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/resets", { path: { tenant_id: tenant, agent_principal_id: agent }, query: { cursor: cursor || undefined, limit: 20 } }), { signal }),
})
export const resetHistoryApi = createResetHistoryApi(iamApi)
