import { apiRoute } from "../../lib/api-route"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"

type Channel = components["schemas"]["ChannelView"]
export const createChannelLifecycleApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  list: (project: string, archived: boolean, cursor?: string, signal?: AbortSignal) => api.deliveryResultRequest<{ items: Channel[] | null; next_cursor?: string }>(apiRoute("get", "/entities/{entity_id}/channels", { path: { entity_id: project }, query: { archived, cursor, limit: 50 } }), { signal }),
  get: (channel: string, signal?: AbortSignal) => api.deliveryResultRequest<Channel>(apiRoute("get", "/channels/{channel_id}", { path: { channel_id: channel } }), { signal }),
  update: (channel: string, input: components["schemas"]["ChannelUpdateInput"], key: string, signal?: AbortSignal) => api.deliveryResultRequest<Channel>(apiRoute("patch", "/channels/{channel_id}", { path: { channel_id: channel } }), { body: JSON.stringify(input), headers: { "Idempotency-Key": key }, signal }),
  remove: (channel: string, principal: string, signal?: AbortSignal) => api.deliveryResultRequest<void>(apiRoute("delete", "/channels/{channel_id}/members/{principal_id}", { path: { channel_id: channel, principal_id: principal } }), { signal }),
})
export const channelLifecycleApi = createChannelLifecycleApi(iamApi)
