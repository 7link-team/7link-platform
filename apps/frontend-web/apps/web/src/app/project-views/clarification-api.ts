import type { components } from "../../lib/api-schema"
import { collaborationID } from "../../lib/clientwork-api"
import { iamApi } from "../../lib/iam-api"

export type Clarification = components["schemas"]["ClarificationView"]
type Request = components["schemas"]["ClarificationRequest"]
type Answer = components["schemas"]["ClarificationAnswer"]
const path = (project: string, id?: string) => `/entities/${collaborationID(project)}/client-clarifications${id ? `/${collaborationID(id)}` : ""}`
export const createClarificationApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  list: (project: string, cursor: string, signal?: AbortSignal) => api.deliveryResultRequest<components["schemas"]["PageClarificationView"]>({ method: "GET", url: `${path(project)}?${new URLSearchParams({limit: "20", ...(cursor ? {cursor} : {})})}` }, {signal}),
  request: (project: string, body: Request, key: string) => {
    collaborationID(body.intake_id); collaborationID(body.recipient_id)
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method: "POST", url: path(project)}, {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)})
  },
  answer: (project: string, round: Clarification, body: Answer, key: string) => {
    if (round.state !== "OPEN" || !Number.isSafeInteger(round.version) || round.version < 1) throw new Error("Invalid clarification version")
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method: "POST", url: `${path(project, collaborationID(round.id))}/answers`}, {headers: {"Idempotency-Key": key, "If-Match": `"${round.version}"`}, body: JSON.stringify(body)})
  },
  cancel: (project: string, round: Clarification, reason: string, key: string) => {
    if (round.state !== "OPEN" || !Number.isSafeInteger(round.version) || round.version < 1) throw new Error("Invalid clarification version")
    if (!reason.trim() || Array.from(reason).length > 2000) throw new Error("Invalid cancellation reason")
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method: "POST", url: `${path(project, collaborationID(round.id))}/cancellation`}, {headers: {"Idempotency-Key": key, "If-Match": `"${round.version}"`}, body: JSON.stringify({reason})})
  },
})
export const clarificationApi = createClarificationApi(iamApi)
