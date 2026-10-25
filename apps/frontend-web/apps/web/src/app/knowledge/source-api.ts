import { apiRoute, type RouteQuery } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"

export type Source = components["schemas"]["SourceView"]
export type Chunk = components["schemas"]["ChunkView"]
export type Settings = components["schemas"]["KnowledgeSettings"]
export type SourceUsage = components["schemas"]["SourceUsage"]
export type SourceQuery = RouteQuery<"/tenants/{tenant_id}/knowledge/sources">
export function knowledgeSourceApi(client: Pick<typeof iamApi, "deliveryResultRequest">) { return {
  settings: (tenant: string, signal?: AbortSignal) => client.deliveryResultRequest<Settings>(apiRoute("get", "/tenants/{tenant_id}/knowledge/settings", {path: {tenant_id: tenant}}), {signal}),
  saveSettings: (tenant: string, body: components["schemas"]["KnowledgeSettingsInput"], key: string) => client.deliveryResultRequest<Settings>(apiRoute("put", "/tenants/{tenant_id}/knowledge/settings", {path: {tenant_id: tenant}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  list: (tenant: string, query: SourceQuery, signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["SourcePage"]>(apiRoute("get", "/tenants/{tenant_id}/knowledge/sources", {path: {tenant_id: tenant}, query}), {signal}),
  get: (tenant: string, source: string, signal?: AbortSignal) => client.deliveryResultRequest<Source>(apiRoute("get", "/tenants/{tenant_id}/knowledge/sources/{source_id}", {path: {tenant_id: tenant, source_id: source}}), {signal}),
  usage: (tenant: string, source: string, signal?: AbortSignal) => client.deliveryResultRequest<SourceUsage>(apiRoute("get", "/tenants/{tenant_id}/knowledge/sources/{source_id}/usage", {path: {tenant_id: tenant, source_id: source}}), {signal}),
  chunks: (tenant: string, source: string, offset = 0, signal?: AbortSignal) => {
    const route = apiRoute("get", "/tenants/{tenant_id}/knowledge/sources/{source_id}/chunks", {path: {tenant_id: tenant, source_id: source}, query: {limit: 50}})
    return client.deliveryResultRequest<components["schemas"]["SourceChunkPage"]>({...route, url: `${route.url}&offset=${encodeURIComponent(offset)}`}, {signal})
  },
  chunkPage: (tenant: string, source: string, cursor = "", signal?: AbortSignal) => client.deliveryResultRequest<components["schemas"]["SourceChunkPage"]>(apiRoute("get", "/tenants/{tenant_id}/knowledge/sources/{source_id}/chunks", {path: {tenant_id: tenant, source_id: source}, query: {cursor: cursor || undefined, limit: 50}}), {signal}),
  upload: (tenant: string, project: string, files: File[], key: string) => {
    const body = new FormData()
    for (const file of files) body.append("files", file)
    return client.deliveryResultRequest<Source[]>(apiRoute("post", "/tenants/{tenant_id}/knowledge/source-uploads", {path: {tenant_id: tenant}, query: {scope: project ? "PROJECT" : "TEAM", project_id: project || undefined}}), {headers: {"Idempotency-Key": key}, body})
  },
  metadata: (tenant: string, source: string, body: components["schemas"]["SourceMetadataInput"], key: string) => client.deliveryResultRequest<Source>(apiRoute("patch", "/tenants/{tenant_id}/knowledge/sources/{source_id}", {path: {tenant_id: tenant, source_id: source}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  archive: (tenant: string, source: string, version: number, key: string) => client.deliveryResultRequest<Source>(apiRoute("post", "/tenants/{tenant_id}/knowledge/sources/{source_id}/archives", {path: {tenant_id: tenant, source_id: source}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({version})}),
  create: (tenant: string, body: components["schemas"]["SourceInput"], key: string) => client.deliveryResultRequest<Source>(apiRoute("post", "/tenants/{tenant_id}/knowledge/sources", {path: {tenant_id: tenant}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  sync: (tenant: string, source: string, body: components["schemas"]["SourceSyncInput"], key: string) => client.deliveryResultRequest<Source>(apiRoute("post", "/tenants/{tenant_id}/knowledge/sources/{source_id}/syncs", {path: {tenant_id: tenant, source_id: source}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  ingest: (tenant: string, source: string, body: components["schemas"]["SourceSyncInput"], key: string) => client.deliveryResultRequest<Source>(apiRoute("post", "/tenants/{tenant_id}/knowledge/sources/{source_id}/ingestions", {path: {tenant_id: tenant, source_id: source}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify(body)}),
  reindex: (tenant: string, source: string, version: number, key: string) => client.deliveryResultRequest<Source>(apiRoute("post", "/tenants/{tenant_id}/knowledge/sources/{source_id}/reindexes", {path: {tenant_id: tenant, source_id: source}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({version})}),
  invalidate: (tenant: string, source: string, version: number, key: string) => client.deliveryResultRequest<Source>(apiRoute("post", "/tenants/{tenant_id}/knowledge/sources/{source_id}/invalidations", {path: {tenant_id: tenant, source_id: source}}), {headers: {"Idempotency-Key": key}, body: JSON.stringify({version})}),
}
}
export const knowledgeSources = knowledgeSourceApi(iamApi)

export type UsedKnowledgeItem = {
  entry_id: string; label: string; title: string; body: string; content_hash: string
  source_id?: string; source_kind: string; scope: string; tokens: number; score_ppm: number
  inclusion: "PENDING" | "FULL" | "REFERENCE" | "DROPPED"; cited: boolean
  citation_outcome?: "ACCEPTED" | "REJECTED"; truncated: boolean; source_changed?: boolean
}
export type UsedKnowledge = {
  available: boolean; reason?: string; scores_available?: boolean; execution_budget?: number
  feedback?: {label: string; category: string; location: string; expected: string; detail: string; source_decision_id: string; actor_id?: string; decided_at?: string; cited: boolean}[]
  retrieval?: { retrieval_log_id: string; mode: string; items: UsedKnowledgeItem[]; rules: UsedKnowledgeItem[]; rules_omitted: number; tokens_total: number; token_budget: number }
}


export function readUsedKnowledge(tenantID: string, executionID: string, signal?: AbortSignal) {
  return iamApi.deliveryResultRequest<UsedKnowledge>({method: "GET", url: `/tenants/${encodeURIComponent(tenantID)}/knowledge/executions/${encodeURIComponent(executionID)}`}, {signal, cache: "no-store"})
}
