import { iamApi } from "../../lib/iam-api"

export type SearchRequest = {include_archived_projects?: boolean; query: string; match_mode: "ALL" | "ANY"; scopes?: string[]; project_ids?: string[]; agent_ids?: string[]; source_kinds?: string[]; tags?: string[]; updated_after?: string; updated_before?: string; offset: number; limit: number; explain: boolean}
export type SearchHit = {project_archived?: boolean; chunk_id: string; source_id: string; entry_id: string; title: string; heading_path: string; snippet: string; highlights: {start: number; end: number}[]; final_score: number; updated_at: string; scope: string}
export type SearchResult = {native_sql_available: boolean; retrieval_log_id: string; mode: string; fallback_reason?: string; total_candidates: number; offset: number; limit: number; results: SearchHit[]}
export type EmbeddingConfig = {base_url: string; model: string; dims: number; has_api_key: boolean; batch_size: number; rpm: number; tpm: number; timeout_ms: number; state: string; active_model_key: string; pending_model_key: string; coverage_ppm: number; version: number}
export type EmbeddingWrite = {base_url: string; model: string; dims: number; api_key?: string; reauth_proof?: string; batch_size: number; rpm: number; tpm: number; timeout_ms: number; enabled: boolean; version: number}

export function nativeKnowledgeApi(client: Pick<typeof iamApi, "deliveryResultRequest">) {
  const route = (tenant: string, path: string, method: "GET" | "POST" | "PUT" = "GET") => ({method, url: `/tenants/${encodeURIComponent(tenant)}/${path}`})
  return {
    search: (tenant: string, body: SearchRequest, key: string, signal?: AbortSignal) => client.deliveryResultRequest<SearchResult>(route(tenant, "knowledge-searches", "POST"), {body: JSON.stringify(body), headers: {"Idempotency-Key": key}, signal}),
    config: (tenant: string, signal?: AbortSignal) => client.deliveryResultRequest<EmbeddingConfig>(route(tenant, "knowledge-embedding-config"), {signal}),
    save: (tenant: string, body: EmbeddingWrite) => client.deliveryResultRequest<EmbeddingConfig>(route(tenant, "knowledge-embedding-config", "PUT"), {body: JSON.stringify(body)}),
    probe: (tenant: string, body?: EmbeddingWrite) => client.deliveryResultRequest<{ok: boolean; latency_ms: number; dims_returned: number; error_code?: string}>(route(tenant, "knowledge-embedding-probes", "POST"), {body: JSON.stringify(body ?? {})}),
    backfill: (tenant: string) => client.deliveryResultRequest<{processed: number; remaining: number; coverage_ppm: number; error_code?: string}>(route(tenant, "knowledge-embedding-backfills", "POST"), {body: JSON.stringify({limit: 64})}),
  }
}
export const nativeKnowledge = nativeKnowledgeApi(iamApi)

// Offsets from the server count Unicode code points. React renders text safely;
// provider-controlled HTML never becomes markup.
export function highlightedParts(text: string, ranges: {start: number; end: number}[]) {
  const runes = Array.from(text)
  const parts: {text: string; marked: boolean}[] = []
  let after = 0
  for (const {start, end} of ranges) {
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < after || end <= start || end > runes.length) continue
    if (start > after) parts.push({text: runes.slice(after, start).join(""), marked: false})
    parts.push({text: runes.slice(start, end).join(""), marked: true})
    after = end
  }
  if (after < runes.length) parts.push({text: runes.slice(after).join(""), marked: false})
  return parts
}
