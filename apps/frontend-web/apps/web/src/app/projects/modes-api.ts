import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"
import { collaborationID } from "../../lib/clientwork-api"

export type ModeCriterion = components["schemas"]["StageCriterion"]
export type ModeHistory = components["schemas"]["ProjectModeChangeView"]
export type TeamMode = "HUMAN_ONLY" | "HYBRID" | "AUTONOMOUS"
export const modeNeedsProof = (current: string, target: TeamMode, takeover: boolean) => takeover || target === "AUTONOMOUS" || (current === "AUTONOMOUS" && target === "HUMAN_ONLY")
export const modeTargets = (current: string): TeamMode[] => (["HUMAN_ONLY", "HYBRID", "AUTONOMOUS"] as TeamMode[]).filter(value => value !== current && !(current === "HUMAN_ONLY" && value === "AUTONOMOUS"))
export const createProjectModesApi = (api: Pick<typeof iamApi, "deliveryResultRequest" | "deliveryResultPage">) => ({
  criteria: (project: string, signal?: AbortSignal) => api.deliveryResultRequest<ModeCriterion[]>({method: "GET", url: `/entities/${collaborationID(project)}/project/mode-preconditions`}, {signal}),
  history: (project: string, offset: number, signal?: AbortSignal) => api.deliveryResultPage<ModeHistory>({method: "GET", url: `/entities/${collaborationID(project)}/project/mode-changes?${new URLSearchParams({offset: String(offset), limit: "10"})}`}, signal),
  change: (project: string, version: number, target: TeamMode, reason: string, takeover: boolean, key: string, proof?: string) => {
    if (!Number.isSafeInteger(version) || version < 1 || Array.from(reason.trim()).length < 10 || Array.from(reason).length > 2000) throw new Error("Invalid mode change")
    return api.deliveryResultRequest<components["schemas"]["ProjectModeReceipt"]>({method: "POST", url: `/entities/${collaborationID(project)}/project/${takeover ? "takeovers" : "mode-changes"}`}, {headers: {"If-Match": `"${version}"`, "Idempotency-Key": key}, body: JSON.stringify({...(takeover ? {} : {to_mode: target}), reason, ...(proof ? {reauth_proof: proof} : {})})})
  },
})
export const projectModesApi = createProjectModesApi(iamApi)
