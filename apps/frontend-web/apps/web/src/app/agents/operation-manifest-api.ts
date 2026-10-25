import { apiRoute } from "../../lib/api-route"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
export type Publication = components["schemas"]["ManifestVersionView"]
export type Manifest = components["schemas"]["ManifestDocument"]
export type Capability = components["schemas"]["EffectiveOperation"]
export const operationManifestApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  versions: (tenant: string, cursor = "", signal?: AbortSignal) => api.deliveryResultRequest<{ items: Publication[]; has_more: boolean; next_cursor?: string }>(apiRoute("get", "/tenants/{tenant_id}/agent-operation-manifest-versions", { path: { tenant_id: tenant }, query: { cursor: cursor || undefined } }), { signal }),
  manifest: (tenant: string, version: string, signal?: AbortSignal) => api.deliveryResultRequest<Manifest>(apiRoute("get", "/tenants/{tenant_id}/agent-operation-manifest", { path: { tenant_id: tenant }, query: { version: version || undefined } }), { signal }),
  capabilities: (tenant: string, agent: string, project: string, version: string, signal?: AbortSignal) => api.deliveryResultRequest<{ manifest_version: string; operations: Capability[] }>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/capabilities", { path: { tenant_id: tenant, agent_principal_id: agent }, query: { project_id: project || undefined, manifest_version: version || undefined } }), { signal }),
})
export const manifestApi = operationManifestApi(iamApi)
