import { iamApi } from "../../lib/iam-api"

export type ExecutionFact = {id: string; work_item_id: string; principal_id: string; state: string; result_state: string}
export type Ensemble = {id: string; work_item_id: string; project_id: string; state: string; reason: string; version: number; selected_branch_id: string | null; synthesis_execution_id: string | null; branches: {id: string; execution_id: string; archived_at: string | null}[]; comparison: ExecutionFact[]}
export function executionIDs(value: string): string[] {
  const ids = value.split(/[\s,]+/).filter(Boolean)
  if (ids.length < 2 || ids.length > 5 || ids.some(id => !/^[1-9][0-9]{0,18}$/.test(id) || BigInt(id) > 9223372036854775807n) || new Set(ids).size !== ids.length) throw new Error("invalid")
  return ids
}
export function ensembleBase(tenant: string, project: string) {
  return `/tenants/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}/ensemble-plans`
}
export const heavyAPI = {
  list: (tenant: string, project: string, work: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<Ensemble[]>({method: "GET", url: `${ensembleBase(tenant, project)}?work_item_id=${encodeURIComponent(work)}`}, {signal}),
  create: (tenant: string, project: string, work: string, executions: string[], reason: string, key: string) => iamApi.deliveryResultRequest<Ensemble>({method: "POST", url: ensembleBase(tenant, project)}, {headers: {"Idempotency-Key": key}, body: JSON.stringify({work_item_id: work, execution_ids: executions, reason})}),
  decide: (tenant: string, project: string, plan: Ensemble, action: string, branch: string, synthesis: string, reason: string, key: string) => iamApi.deliveryResultRequest<Ensemble>({method: "POST", url: `${ensembleBase(tenant, project)}/${encodeURIComponent(plan.id)}/decisions`}, {headers: {"Idempotency-Key": key}, body: JSON.stringify({version: plan.version, action, branch_id: branch || "0", synthesis_execution_id: synthesis || "0", reason})}),
}
