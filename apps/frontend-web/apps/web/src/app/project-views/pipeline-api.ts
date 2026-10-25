import { iamApi } from "../../lib/iam-api"
import { collaborationID } from "../../lib/clientwork-api"

export type PipelineRun = { id: string; project_id: string; work_item_id: string; result_id: string; stage: string; internal_round: number | null; attempt: number; state: string; created_at: string; started_at: string | null; ended_at: string | null; execution_id: string | null; verification_id: string | null; decision_id: string | null; delivery_id: string | null; evidence_refs: string[] }
export type PipelinePage = { items: PipelineRun[]; total: number }
export const pipelineStages = ["PLATFORM_RERUN", "HIDDEN_SUITE", "TESTER", "ACCEPTOR", "HUMAN_REVIEW", "DELIVERY"] as const
export const createPipelineApi = (api: Pick<typeof iamApi, "deliveryResultPage">) => ({
  list: (project: string, work: string, offset: number, signal?: AbortSignal) => {
    if (!Number.isSafeInteger(offset) || offset < 0) throw new Error("Invalid offset")
    return api.deliveryResultPage<PipelineRun>({ method: "GET", url: `/entities/${collaborationID(project)}/pipeline-runs?${new URLSearchParams({ offset: String(offset), limit: "50", ...(work ? { work_item_id: collaborationID(work) } : {}) })}` }, signal)
  },
})
export const pipelineApi = createPipelineApi(iamApi)
