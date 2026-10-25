import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type DirectPage<T> = { items: T[] | null; has_more: boolean; next_cursor?: string }
export type AgentDirectChannel = components["schemas"]["ChannelView"]
export type AgentDirectMessage = components["schemas"]["MessageView"]
export type AgentDirectoryPage = components["schemas"]["AgentDirectoryPage"]
export type WorkspacePage = { path: string; items: { name: string; kind: "FILE" | "DIRECTORY"; size: number }[]; total: number; has_more: boolean }
export type WorkspaceContent = { path: string; content: string }
const workspaceRoute = (tenant: string, agent: string, path: string, content: boolean, offset = 0, search = "") => ({
  method: "GET" as const,
  url: `/tenants/${encodeURIComponent(tenant)}/agents/${encodeURIComponent(agent)}/workspace-entries${content ? "/content" : ""}?${new URLSearchParams({ path, offset: String(offset), limit: "50", search })}`,
})
export const createAgentInspectionApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  workspaceAccess: (tenant: string, agent: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<boolean>({ method: "GET", url: `/tenants/${encodeURIComponent(tenant)}/agents/${encodeURIComponent(agent)}/workspace-access` }, { signal, cache: "no-store" }),
  workspace: (tenant: string, agent: string, path: string, offset: number, search: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<WorkspacePage>(workspaceRoute(tenant, agent, path, false, offset, search), { signal, cache: "no-store" }),
  content: (tenant: string, agent: string, path: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<WorkspaceContent>(workspaceRoute(tenant, agent, path, true), { signal, cache: "no-store" }),
  directory: (tenant: string, search: string, cursor: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<AgentDirectoryPage>(apiRoute("get", "/tenants/{tenant_id}/agents", { path: { tenant_id: tenant }, query: { search: search || undefined, cursor: cursor || undefined, limit: 30 } }), { signal }),
  work: (tenant: string, agent: string, cursor: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<components["schemas"]["PersonalWorkPage"]>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/work", { path: { tenant_id: tenant, agent_principal_id: agent }, query: { cursor: cursor || undefined, limit: 30 } }), { signal }),
  channels: (tenant: string, agent: string, cursor: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<DirectPage<AgentDirectChannel>>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/direct-channels", { path: { tenant_id: tenant, agent_principal_id: agent }, query: { cursor: cursor || undefined, limit: 30 } }), { signal }),
  messages: (tenant: string, agent: string, channel: string, cursor: string, signal?: AbortSignal) =>
    api.deliveryResultRequest<DirectPage<AgentDirectMessage>>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/direct-channels/{channel_id}/messages", { path: { tenant_id: tenant, agent_principal_id: agent, channel_id: channel }, query: { cursor: cursor || undefined, limit: 30 } }), { signal }),
})
export const agentInspectionApi = createAgentInspectionApi(iamApi)
