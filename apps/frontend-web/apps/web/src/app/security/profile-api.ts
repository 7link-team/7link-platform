import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type SelfProfile = components["schemas"]["SelfProfileView"]
export type SelfProfilePatch = components["schemas"]["SelfProfilePatch"]
export function selfProfileRoute(principal: string) {
  if (!/^[1-9][0-9]{0,18}$/.test(principal)) throw new Error("Invalid principal identifier")
  return apiRoute("patch", "/principals/{principal_id}/profile", { path: { principal_id: principal } })
}
export const createSelfProfileApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  get: async (principal: string, signal?: AbortSignal) => {
    selfProfileRoute(principal)
    const profile = await api.deliveryResultRequest<SelfProfile>(apiRoute("get", "/principals/{principal_id}/profile", { path: { principal_id: principal } }), { signal })
    if (profile.id !== principal) throw new Error("Invalid self profile response")
    return profile
  },
  update: async (principal: string, body: SelfProfilePatch) => {
    const profile = await api.deliveryResultRequest<SelfProfile>(selfProfileRoute(principal), { body: JSON.stringify(body) })
    if (profile.id !== principal) throw new Error("Invalid self profile response")
    return profile
  },
})
export const selfProfileApi = createSelfProfileApi(iamApi)
