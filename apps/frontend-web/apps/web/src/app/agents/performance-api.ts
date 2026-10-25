import { iamApi } from "../../lib/iam-api"

export const performanceTypes = ["FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"] as const
export type PerformanceMeasures = {
  work_item_type: string; fpa_n: number; first_pass_accepts: number; fpa: number | null
  claims: number; rework_decisions: number; rework_rate: number | null
  accepted_items: number; cost_on_accepted_micros: number | null; cpa_micros: number | null
}
export type AgentPerformance = PerformanceMeasures & {
  window_days: number; computed_at: string; scope: string; by_type: PerformanceMeasures[]
  load: { active_items: number; max_parallel_items: number; running_executions: number }
}
export const createPerformanceApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  read: (tenant: string, agent: string, days: 30 | 90, kind: string, signal?: AbortSignal) => {
    const query = new URLSearchParams({ window_days: String(days) })
    if (kind) query.set("work_item_type", kind)
    return api.deliveryResultRequest<AgentPerformance>({ method: "GET", url: `/tenants/${encodeURIComponent(tenant)}/agents/${encodeURIComponent(agent)}/stats?${query}` }, { signal, cache: "no-store" })
  },
})
export const performanceApi = createPerformanceApi(iamApi)
