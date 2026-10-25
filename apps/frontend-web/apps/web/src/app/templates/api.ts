import { ApiError, iamApi } from "../../lib/iam-api"
import type {
  Definition,
  TemplateVersion,
  WorkflowInstance,
  WorkflowPatch,
  WorkflowDef,
} from "./workflow"
export function bindingLoadError(cause: unknown): string {
  if (cause instanceof ApiError && cause.status === 404 && cause.businessCode === 185404) return ""
  return cause instanceof Error ? cause.message : String(cause)
}
const request = <T>(
  method: "GET" | "POST" | "PUT" | "PATCH",
  url: string,
  body?: unknown,
  signal?: AbortSignal,
  key?: string,
  version?: number
) =>
  iamApi.deliveryResultRequest<T>(
    { method, url },
    {
      signal,
      ...(body === undefined
        ? {}
        : {
            body: JSON.stringify(body),
            headers: { "Idempotency-Key": key ?? crypto.randomUUID(),...(version===undefined?{}:{"If-Match":`"${version}"`}) },
          }),
    }
  )
export {request as templateRequest}
const root = (tenant: string) => `/tenants/${encodeURIComponent(tenant)}`
export type VerifierResult = {
  task_id: string
  result_id: string
  criterion_id: string
  verifier_key: string
  kind: string
  runner: string
  stage: string
  verdict: string
  reason: string
  params_resolved: Record<string, unknown>
  metrics: Record<string, number>
  evidence_refs: string[]
  limitations: string
  tool_version: string
  calibration_state: string
  duration_ms?: number
  template_pin?: {
    template_version_id: string
    definition_hash: string
    verifier: { verifier_key: string; version: number; plugin_key?: string; required_tools?: string[] }
  }
}
export type TemplateFilters = {domain?: string; origin?: string; state?: string; team_mode?: string; key?: string}
export type TemplateFamily = {key: string; name: string; version_count: number; latest: TemplateVersion; latest_published?: TemplateVersion}
function libraryQuery(cursor: string, filters: TemplateFilters, limit: number, sort = false) {
 const query = new URLSearchParams({limit: String(limit)})
 if(sort) query.set("sort","id")
 if(cursor) query.set("cursor",cursor)
 for(const [key,value] of Object.entries(filters)) if(value) query.set(key,value)
 return query.toString()
}
export const templatesApi = {
	copy: (tenant:string, source:string, key:string, name:string, retryKey:string) =>
		request<TemplateVersion>("POST",`${root(tenant)}/templates`,{based_on_version_id:source,key,name},undefined,retryKey),
	deprecate: (tenant:string, version:string, retryKey:string) =>
		request<TemplateVersion>("PUT",`${root(tenant)}/template-versions/${encodeURIComponent(version)}/deprecation`,{},undefined,retryKey),
  verifierResults: (
    tenant: string,
    work: string,
    stage: string,
    cursor: string,
    signal?: AbortSignal
  ) =>
    request<{
      items: VerifierResult[]
      next_cursor?: string
      has_more: boolean
    }>(
      "GET",
      `${root(tenant)}/work-items/${encodeURIComponent(work)}/verifier-results?limit=50${stage ? `&stage=${encodeURIComponent(stage)}` : ""}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      undefined,
      signal
    ),
  families: (tenant: string, cursor: string, filters: TemplateFilters, signal?: AbortSignal) => request<{items: TemplateFamily[]; next_cursor?: string; has_more: boolean}>("GET", `${root(tenant)}/template-families?${libraryQuery(cursor, filters, 30)}`, undefined, signal),
  list: (tenant: string, cursor: string, signal?: AbortSignal, filters: TemplateFilters = {}) =>
    request<{
      items: TemplateVersion[]
      next_cursor?: string
      has_more: boolean
    }>(
      "GET",
      `${root(tenant)}/templates?${libraryQuery(cursor, filters, 30, true)}`,
      undefined,
      signal
    ),
  get: (tenant: string, id: string, signal?: AbortSignal) =>
    request<TemplateVersion>(
      "GET",
      `${root(tenant)}/templates/${encodeURIComponent(id)}`,
      undefined,
      signal
    ),
  draft: (
    tenant: string,
    key: string,
    definition: Definition,
    idempotencyKey: string
  ) =>
    request<TemplateVersion>(
      "POST",
      `${root(tenant)}/templates`,
      { key, definition },
      undefined,
      idempotencyKey
    ),
  patch: (tenant: string, id: string, patch: WorkflowPatch, key: string) =>
    request<TemplateVersion>(
      "POST",
      `${root(tenant)}/templates/${encodeURIComponent(id)}/patches`,
      patch,
      undefined,
      key
    ),
  publish: (tenant: string, id: string, hash: string, key: string) =>
    request<TemplateVersion>(
      "POST",
      `${root(tenant)}/templates/${encodeURIComponent(id)}/publications`,
      { hash },
      undefined,
      key
    ),
  instantiate: (
    tenant: string,
    id: string,
    project: string,
    accountable: string,
    key: string
  ) =>
    request<WorkflowInstance>(
      "POST",
      `${root(tenant)}/templates/${encodeURIComponent(id)}/instances`,
      { project_id: project, accountable_principal_id: accountable },
      undefined,
      key
    ),
  instance: (tenant: string, project: string, signal?: AbortSignal) =>
    request<WorkflowInstance>(
      "GET",
      `${root(tenant)}/projects/${encodeURIComponent(project)}/workflow`,
      undefined,
      signal
    ),
  advance: (
    tenant: string,
    project: string,
    key: string,
    signal?: AbortSignal
  ) =>
    request<WorkflowInstance>(
      "POST",
      `${root(tenant)}/projects/${encodeURIComponent(project)}/workflow/advances`,
      {},
      signal,
      key
    ),
}

export const rubricStages = ["AGENT_ACCEPTANCE", "HUMAN_REVIEW", "CLIENT_ACCEPTANCE"] as const
export type RubricStage = typeof rubricStages[number]
export type RubricValue = { dimension_key: string; score?: number; pass_fail?: boolean; evidence_refs: string[]; note: string; proofs?: {task_id: string; receipt_hash: string; verifier_key: string; passed: boolean}[] }
export type RubricReceipt = { id: string; template_version_id: string; definition_hash: string; stage: RubricStage; scorer_kind: string; scorer_id?: string; result_id?: string; result_version?: number; head_commit?: string; scores: RubricValue[]; calculation: {total: number | null; passed: boolean; weighted_min: number}; created_at: string }
export type RubricBoard = {
 template_version_id: string; definition_hash: string; work_version: number; result_id?: string; result_version?: number; evidence_refs: string[]
 definition: {dimensions: {dimension_key: string; kind: "HARD_GATE" | "WEIGHTED"; weight?: number; verifier_keys?: string[]}[]; pass_rule: {hard_gates: string; weighted_min?: number}} | null
 receipts: RubricReceipt[]; divergences: {dimension: string; left_id: string; right_id: string; left_stage: string; right_stage: string; delta: string}[]
 allowed_stages: RubricStage[]; availability: string; acceptance_effect: string
}
export type RubricCommand = {stage: RubricStage; result_id: string | null; work_version: number; scores: RubricValue[]}
export function rubricRoute(project: string, work?: string, packageID?: string, itemID?: string) {
 const root = `/entities/${encodeURIComponent(project)}`
 return packageID ? `${root}/delivery-packages/${encodeURIComponent(packageID)}/rubric-scores?item_id=${encodeURIComponent(itemID ?? "")}` : `${root}/work-items/${encodeURIComponent(work ?? "")}/rubric-scores`
}
export const rubricApi = {
 read: (url: string, signal?: AbortSignal) => request<RubricBoard>("GET", url, undefined, signal),
 record: (url: string, body: RubricCommand, key: string, signal?: AbortSignal) => request<RubricReceipt>("POST", url, body, signal, key),
}

export type WorkflowValidation={valid:boolean;issues:{path:string;code:"templateIssueGraph"|"templateIssueSubflow"|"templateIssueInherited"}[]}
export type WorkflowModelProposal={patch:WorkflowPatch;workflow:WorkflowDef;validation:WorkflowValidation;provider_id:string;provider_version:number;model:string;request_sha256:string;response_sha256:string}
const path=(tenant:string,id:string)=>`/tenants/${encodeURIComponent(tenant)}/templates/${encodeURIComponent(id)}`
export const workflowAssistantApi={
 validate:(tenant:string,version:TemplateVersion,workflow:WorkflowDef,signal?:AbortSignal)=>request<WorkflowValidation>("POST",`${path(tenant,version.id)}/workflow-validations`,{base_hash:version.hash,workflow},signal),
 generate:(tenant:string,version:TemplateVersion,providerID:string,instruction:string,key:string,signal?:AbortSignal)=>request<WorkflowModelProposal>("POST",`${path(tenant,version.id)}/workflow-proposals`,{base_hash:version.hash,provider_id:providerID,instruction},signal,key),
}
