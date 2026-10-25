import { iamApi } from "../../lib/iam-api"

export type Retirement = {
  id: string; agent_principal_id: string; mode: "NORMAL" | "FORCED"; state: string; step: string
  version: number; agent_version: number; reason: string; step_started_at?: string
  precheck: {
    machine?: { machine_id?: string; machine_state: string; process_status: string }; lead_project_ids?: string[]
    work_items: { assignment_id: string; project_id: string; work_item_id: string; title: string; state: string }[]
    executions: { id: string; project_id: string; work_item_id: string; state: string }[]
    lost?: { open_items: number; running_executions: number }
    progress?: {
      handover_execution_id?: string; handover_artifact_id?: string; handover_source?: "AGENT" | "SYSTEM"
      handover_retry_count?: number; successor_kind?: string; successor_principal_id?: string
      cleanup_status?: string; reassign_result?: { work_item_id: string; [key: string]: unknown }[]
      handover_error_code?: string; reconciliation_status?: string
    }
  }
}
export type RetirementStart = { mode: "NORMAL" | "FORCED"; reason: string; confirm_name?: string; reauth_proof?: string }
export type HandoverDocument = { id: string; execution_id?: string; source: "AGENT" | "SYSTEM"; content: string; sha256: string }
export type RetirementPatch = { agent_version: number } & ({ state: "CANCELLED" } | { advance: true } | { handover_retry: true } | { successor_kind: "HUMAN" | "AGENT" | "LATER"; successor_principal_id?: string })
const root = (tenant: string) => `/tenants/${encodeURIComponent(tenant)}`
export const createRetirementApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  handover: (tenant: string, id: string, signal?: AbortSignal) => api.deliveryResultRequest<HandoverDocument>({ method: "GET", url: `${root(tenant)}/agent-retirements/${encodeURIComponent(id)}/handover` }, { signal, cache: "no-store" }),
  read: (tenant: string, id: string, signal?: AbortSignal) => api.deliveryResultRequest<Retirement>({ method: "GET", url: `${root(tenant)}/agent-retirements/${encodeURIComponent(id)}` }, { signal, cache: "no-store" }),
  start: (tenant: string, agent: string, version: number, body: RetirementStart, key: string) => api.deliveryResultRequest<Retirement>({ method: "POST", url: `${root(tenant)}/agents/${encodeURIComponent(agent)}/retirements` }, { body: JSON.stringify(body), headers: { "If-Match": String(version), "Idempotency-Key": key } }),
  patch: (tenant: string, id: string, version: number, body: RetirementPatch, key: string) => api.deliveryResultRequest<Retirement>({ method: "PATCH", url: `${root(tenant)}/agent-retirements/${encodeURIComponent(id)}` }, { body: JSON.stringify(body), headers: { "If-Match": String(version), "Idempotency-Key": key } }),
  binding: (tenant: string, agent: string, signal?: AbortSignal) => api.deliveryResultRequest<{ version: number; state: string; retirement_id?: string }>({ method: "GET", url: `${root(tenant)}/agents/${encodeURIComponent(agent)}/machine-binding` }, { signal, cache: "no-store" }),
})
export const retirementApi = createRetirementApi(iamApi)
