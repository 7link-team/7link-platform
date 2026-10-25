import { iamApi } from "../../lib/iam-api"

export type ResultEvidenceInput = {
  space_id: string
  file_id: string
  file_version: number
  version_id: string
  criterion_id?: string
  coverage: "COVERED" | "PARTIAL" | "NOT_APPLICABLE"
  note?: string
}
export type ResultFeedbackInput = {
  comment_thread_id?: string
  location?: string
  expected?: string
  kind: "COMMENT" | "FEEDBACK"
  category?: "QUALITY_REJECTION" | "STRATEGY_CHANGE" | "SCOPE_CHANGE"
  detail: string
}
export type ResultFeedback = ResultFeedbackInput & {
  id: string
  result_version_id: string
  anchor_snapshot?: import("../executions/artifact-comments").ArtifactAnchor | null
  actor_id: string
  created_at: string
}
export type WorkResult = {
  id: string
  work_item_id: string
  project_id: string
  sequence: number
  summary: string
  actor_id: string
  created_at: string
  previous_result_id?: string | null
  rework_action_id?: string | null
  evidence:
    | (Omit<ResultEvidenceInput, "file_version"> & {
        id: string
        binding_id: string
      })[]
    | null
  actions:
    | {
        id: string
        kind: "CHECK" | "ACCEPT" | "RETURN" | "RECEIPT"
        actor_id: string
        reason: string
        verdict?: "PASSED" | "FAILED" | "INCONCLUSIVE" | null
        created_at: string
        handoff_id?: string | null
      }[]
    | null
  handoffs:
    | {
        id: string
        actor_id: string
        delivery_id: string
        result_id: string
        summary: string
        manifest: string
        sha256: string
        created_at: string
      }[]
    | null
}

export function resultPath(entityID: string, workID: string) {
  return `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(workID)}/results`
}

export const resultsApi = {
  feedback: (
    entityID: string,
    workID: string,
    resultID: string,
    offset: number,
    signal?: AbortSignal
  ) =>
    iamApi.deliveryResultPage<ResultFeedback>(
      {
        method: "GET",
        url: `${resultPath(entityID, workID)}/${encodeURIComponent(resultID)}/feedback?offset=${offset}&limit=20`,
      },
      signal
    ),
  postFeedback: (
    entityID: string,
    workID: string,
    resultID: string,
    key: string,
    body: ResultFeedbackInput
  ) =>
    iamApi.deliveryResultRequest<ResultFeedback>(
      {
        method: "POST",
        url: `${resultPath(entityID, workID)}/${encodeURIComponent(resultID)}/feedback`,
      },
      { headers: { "Idempotency-Key": key }, body: JSON.stringify(body) }
    ),
  candidate: (entityID: string, workID: string, fileID: string) =>
    iamApi.deliveryResultRequest<
      Pick<
        ResultEvidenceInput,
        "space_id" | "file_id" | "file_version" | "version_id"
      > & {
        name: string
        sha256: string
        mime: string
        size: number
      }
    >({
      method: "GET",
      url: `/entities/${encodeURIComponent(entityID)}/work-items/${encodeURIComponent(workID)}/evidence-candidates/${encodeURIComponent(fileID)}`,
    }),
  list: (
    entityID: string,
    workID: string,
    offset: number,
    signal?: AbortSignal
  ) =>
    iamApi.deliveryResultPage<WorkResult>(
      {
        method: "GET",
        url: `${resultPath(entityID, workID)}?offset=${offset}&limit=50`,
      },
      signal
    ),
  mutate: (
    entityID: string,
    workID: string,
    suffix: string,
    version: number,
    key: string,
    body: unknown
  ) =>
    iamApi.deliveryResultRequest<
      Pick<WorkResult, "id" | "project_id" | "work_item_id" | "sequence">
    >(
      { method: "POST", url: resultPath(entityID, workID) + suffix },
      {
        headers: { "If-Match": `"${version}"`, "Idempotency-Key": key },
        body: JSON.stringify(body),
      }
    ),
  evidence: (
    entityID: string,
    workID: string,
    resultID: string,
    evidenceID: string
  ) =>
    iamApi.deliveryResultDownload({
      method: "GET",
      url: `${resultPath(entityID, workID)}/${encodeURIComponent(resultID)}/evidence/${encodeURIComponent(evidenceID)}/content`,
    }),
}

// Only the current result can carry the work's current acceptance. Historical
// ACCEPT actions remain visible without implying acceptance of a newer result.
export function isAcceptedResult(
  result: WorkResult,
  work: { current_claim_id?: string | null; accepted_at?: string | null }
) {
  return work.current_claim_id === result.id && Boolean(work.accepted_at)
}
