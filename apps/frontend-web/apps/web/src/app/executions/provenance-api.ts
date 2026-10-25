import { iamApi } from "../../lib/iam-api"
import { collaborationID } from "../../lib/clientwork-api"

export type ProvenanceFact = {
  id: string; execution_id: string; producer_id: string; machine_id: string; kind: string; title: string; content_hash: string; created_at: string; plan_hash: string; base_commit: string; commit?: string;
  output_state: "RECORDED" | "PARTIAL"; comparison: "NOT_REQUESTED" | "SAME" | "CHANGED" | "ONLY_CURRENT" | "UNKNOWN"; compared_evidence_id?: string;
  artifact?: {binding_id: string; space_id: string; file_id: string; version_id: string; file_version: number; criterion_id?: string; coverage: string; note: string; sha256: string}
}
export type ProvenancePage = {items: ProvenanceFact[]; has_more: boolean; next_before_id?: string; compared_execution_id?: string; comparison_truncated: boolean; verifiers_truncated: boolean; limitations: string[]; verifiers: {id: string; criterion_id: string; result_id: string; result_version: number; state: string; plan_hash: string; receipt_hash: string}[]}
export const createProvenanceApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  list: (id: string, before = "", compare = "", signal?: AbortSignal) => api.deliveryResultRequest<ProvenancePage>({method: "GET", url: `/executions/${collaborationID(id)}/evidence-provenance?${new URLSearchParams({limit: "50", sort: "-id", ...(before ? {cursor: collaborationID(before)} : {}), ...(compare ? {compare_execution_id: collaborationID(compare)} : {})})}`}, {signal}),
})
export const provenanceApi = createProvenanceApi(iamApi)
