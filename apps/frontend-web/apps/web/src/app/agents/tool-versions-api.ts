import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type CatalogTool = components["schemas"]["ToolView"] & {credential?: {set: boolean; last4: string; rotated_at?: string; upstream_auth_failing: boolean}}
export type CatalogVersion = {id: string; version_no: number; transport: string; endpoint: string; content_hash: string; content_sha256: string; skill_body?: string; state: string; source: string; source_ref: string; change_note: string; published_by: string; published_at: string}
export type CatalogDetail = {entry: CatalogTool; current_version_no: number; versions: CatalogVersion[]; upstream: {id: string; content_hash: string; note: string; reported_by: string; reported_at: string}[]; upstream_changed: boolean}
export type CatalogPreset = {key: string; title: string; transport: string; endpoint: string; content_hash: string}
export type CatalogImport = {entry_key: string; kind: "MCP_SERVER"; title: string; transport: "STREAMABLE_HTTP" | "STDIO_PACKAGE"; endpoint: string; content_hash: string; preset_key?: string; from_local?: {machine_id: string; runtime: "CODEX" | "CLAUDE_CODE"; name: string; scope: string}}
export type CatalogPublish = {version: number; transport: "STREAMABLE_HTTP" | "STDIO_PACKAGE"; endpoint: string; content_hash: string; change_note: string}
export type CatalogState = {version: number; state?: "ACTIVE" | "DISABLED" | "ARCHIVED"; version_no?: number; version_state?: "REVOKED"; reason: string}
export function createToolVersionsApi(client: Pick<typeof iamApi, "deliveryResultRequest">) {return {
  credential: (tenant: string, id: string, body: {version: number; secret: string; reauth_proof: string}, key: string) => client.deliveryResultRequest<{set: boolean; last4: string; rotated_at: string; version: number}>({method: "PUT", url: `/tenants/${encodeURIComponent(tenant)}/agent-tools/${encodeURIComponent(id)}/credential`}, {headers: {"Idempotency-Key": key}, body: JSON.stringify(body), cache: "no-store"}),
  detail: (tenant: string, id: string, signal?: AbortSignal) => client.deliveryResultRequest<CatalogDetail>(apiRoute("get", "/tenants/{tenant_id}/agent-tools/{tool_id}", {path: {tenant_id: tenant, tool_id: id}}), {signal}),
  presets: (tenant: string, signal?: AbortSignal) => client.deliveryResultRequest<CatalogPreset[]>(apiRoute("get", "/tenants/{tenant_id}/agent-tool-presets", {path: {tenant_id: tenant}}), {signal}),
  sources: (tenant: string, machine: string, signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["MachineCapabilities"]>(apiRoute("get", "/tenants/{tenant_id}/agent-tool-sources", {path: {tenant_id: tenant}, query: {machine_id: machine}}), {signal}),
  import: (tenant: string, body: CatalogImport, key: string) => client.deliveryResultRequest<CatalogTool>(apiRoute("post", "/tenants/{tenant_id}/agent-tools", {path: {tenant_id: tenant}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({...body, skill_body: ""})}),
  publish: (tenant: string, id: string, body: CatalogPublish, key: string) => client.deliveryResultRequest<CatalogVersion>(apiRoute("post", "/tenants/{tenant_id}/agent-tools/{tool_id}/versions", {path: {tenant_id: tenant, tool_id: id}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  state: (tenant: string, id: string, body: CatalogState, key: string) => client.deliveryResultRequest<CatalogTool>(apiRoute("post", "/tenants/{tenant_id}/agent-tools/{tool_id}/state", {path: {tenant_id: tenant, tool_id: id}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  upstream: (tenant: string, id: string, body: {version: number; content_hash: string; note: string}, key: string) => client.deliveryResultRequest<CatalogDetail>(apiRoute("post", "/tenants/{tenant_id}/agent-tools/{tool_id}/upstream-changes", {path: {tenant_id: tenant, tool_id: id}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  metadata: (tenant: string, id: string, body: {version: number; title: string}, key: string) => client.deliveryResultRequest<CatalogTool>(apiRoute("post", "/tenants/{tenant_id}/agent-tools/{tool_id}/metadata", {path: {tenant_id: tenant, tool_id: id}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
}}
export const toolVersions = createToolVersionsApi(iamApi)
