import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type HubChannel = Omit<components["schemas"]["ChannelView"], "id"> & { id: string }
export type HumanContact = Omit<components["schemas"]["HumanContact"], "id"> & { id: string }
export type DirectMessage = components["schemas"]["MessageView"]
type Page<T> = { items: T[] | null; has_more: boolean; next_cursor?: string }
function requiredID(id: string | null) {
  if (!id || !/^[1-9][0-9]*$/.test(id)) throw new Error("Conversation source mismatch")
  return id
}

export function isActiveHubExecution(state: string) {
  return ["RUNNING", "PAUSED"].includes(state)
}

export const directCreateRoute = (tenant: string) => apiRoute("post", "/tenants/{tenant_id}/direct-channels", { path: { tenant_id: tenant } })
export const directSendRoute = (tenant: string, channel: string) => apiRoute("post", "/tenants/{tenant_id}/direct-channels/{channel_id}/messages", { path: { tenant_id: tenant, channel_id: channel } })

export const createConversationHubApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  sessions: (tenant: string, project: string, cursor: string, signal?: AbortSignal) => api.deliveryResultRequest<components["schemas"]["HubExecutionSessionPage"]>(apiRoute("get", "/tenants/{tenant_id}/execution-sessions", {path: {tenant_id: tenant}, query: {project_id: project, cursor: cursor || undefined, limit: 50}}), {signal}),
  activity: (tenant: string, kind: "MENTIONS" | "REPLIES", cursor: string, signal?: AbortSignal) => api.deliveryResultRequest<components["schemas"]["CursorPageHubActivity"]>(apiRoute("get", "/tenants/{tenant_id}/conversations/activity", {path: {tenant_id: tenant}, query: {kind, cursor: cursor || undefined, limit: 30}}), {signal}),
  list: async (tenant: string, cursor: string, signal?: AbortSignal) => {
    const page = await api.deliveryResultRequest<Page<components["schemas"]["ChannelView"]>>(apiRoute("get", "/tenants/{tenant_id}/conversations", { path: { tenant_id: tenant }, query: { cursor: cursor || undefined, limit: 30 } }), { signal })
    return { ...page, items: (page.items ?? []).map(channel => ({ ...channel, id: requiredID(channel.id) })) }
  },
  contacts: async (tenant: string, search: string, after: string, signal?: AbortSignal, kind: "HUMAN" | "AGENT" = "HUMAN") => {
    if (kind === "AGENT") {
      const page = await api.deliveryResultRequest<components["schemas"]["AgentDirectoryPage"]>(apiRoute("get", "/tenants/{tenant_id}/agents", { path: { tenant_id: tenant }, query: { search: search || undefined, cursor: after || undefined, limit: 30 } }), { signal })
      return { items: (page.items ?? []).filter(agent => agent.state === "ACTIVE").map(agent => ({ id: requiredID(agent.principal_id), display_name: agent.name ?? agent.principal_id ?? "", avatar: "" })), has_more: page.has_more, next_after: page.next_cursor }
    }
    const page = await api.deliveryResultRequest<components["schemas"]["HumanContactsPage"]>(apiRoute("get", "/tenants/{tenant_id}/human-contacts", { path: { tenant_id: tenant }, query: { search: search || undefined, after: after || undefined, limit: 30 } }), { signal })
    return { ...page, items: (page.items ?? []).map(contact => ({ ...contact, id: requiredID(contact.id) })) }
  },
  open: async (tenant: string, peer: string, key: string, signal?: AbortSignal) => {
    const channel = await api.deliveryResultRequest<components["schemas"]["DirectChannelResult"]>(directCreateRoute(tenant), { headers: { "Idempotency-Key": key }, body: JSON.stringify({ peer_principal_id: peer }), signal })
    if (channel.kind !== "DIRECT" || channel.project_id) throw new Error("Conversation source mismatch")
    return { ...channel, id: requiredID(channel.id) }
  },
  messages: async (tenant: string, channel: string, cursor: string, signal?: AbortSignal) => {
    const page = await api.deliveryResultRequest<Page<DirectMessage>>(apiRoute("get", "/tenants/{tenant_id}/direct-channels/{channel_id}/messages", { path: { tenant_id: tenant, channel_id: channel }, query: { cursor: cursor || undefined, limit: 30 } }), { signal })
    if (page.items?.some(message => message.channel_id !== channel)) throw new Error("Conversation source mismatch")
    return page
  },
  markRead: (tenant: string, channel: string, seq: number, signal?: AbortSignal) => api.deliveryResultRequest<number>(apiRoute("put", "/tenants/{tenant_id}/direct-channels/{channel_id}/read-marker", { path: { tenant_id: tenant, channel_id: channel } }), { body: JSON.stringify({ seq }), signal }),
  send: async (tenant: string, channel: string, body: string, key: string, signal?: AbortSignal) => {
    const message = await api.deliveryResultRequest<DirectMessage>(directSendRoute(tenant, channel), { headers: { "Idempotency-Key": key }, body: JSON.stringify({ body }), signal })
    if (message.channel_id !== channel) throw new Error("Conversation source mismatch")
    return message
  },
})
export const conversationHubApi = createConversationHubApi(iamApi)
