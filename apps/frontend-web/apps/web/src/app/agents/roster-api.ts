import { iamApi } from "../../lib/iam-api"

export type RosterEntry = {
  kind: "HUMAN" | "AGENT"; principal_id: string; name: string; roles: string[]; state: string
  runtime?: string; model?: string; machine_id?: string; origin?: string; lineage_status?: string; activity_status?: string
  load: { active_items: number; max?: number }; current_work_items: { id: string; project_id: string; title: string; state: string }[]
  stats_30d: { first_decisions: number; first_pass_accepts: number }; fpa_30d: number | null; performance_available: boolean
}
export type RosterPage = { items: RosterEntry[]; total: number; has_more: boolean; scope: string }
export type RosterFilters = { search: string; kind: string; role: string; state: string; project_id: string; machine_id: string; runtime_kind: string; model_id: string; origin: string; lineage_status: string; activity_status: string; sort: string }
export const emptyRosterFilters: RosterFilters = { search: "", kind: "", role: "", state: "", project_id: "", machine_id: "", runtime_kind: "", model_id: "", origin: "", lineage_status: "", activity_status: "", sort: "name" }
export const createRosterApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  list: (tenant: string, filters: RosterFilters, offset: number, signal?: AbortSignal) => {
    const query = new URLSearchParams({ limit: "50", offset: String(offset) })
    for (const [key, value] of Object.entries(filters)) if (value) query.set(key, value)
    return api.deliveryResultRequest<RosterPage>({ method: "GET", url: `/tenants/${encodeURIComponent(tenant)}/roster-entries?${query}` }, { signal, cache: "no-store" })
  },
})
export const rosterApi = createRosterApi(iamApi)
