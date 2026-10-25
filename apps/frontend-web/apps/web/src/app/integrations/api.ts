import { iamApi } from "../../lib/iam-api"
import type { OperationRequestContract } from "@workspace/ui/components/auto-form/contract"
export type Connection = {
  id: string
  name: string
  organization_id: string
  api_endpoint: string
  token_last4: string
  provider: string
  state: string
  version: number
  availability: "UNAVAILABLE"
  token_scopes: string[]
}
export type Mapping = {
  id: string
  connection_id: string
  external_project_id: string
  external_project_name: string
  project_id: string
  sync_mode: "IMPORT_ONLY" | "READ_MIRROR" | "BIDIRECTIONAL"
  field_masters: Record<string, "MYRMIDON" | "YUNXIAO">
  codeup_repositories: string[]
  trusted_pipelines: string[]
  paused: boolean
  version: number
}
export type Page<T> = { items: T[]; next_cursor?: string; has_more: boolean }
export const connectionPath = (tenant: string) =>
  `/tenants/${encodeURIComponent(tenant)}/integration-connections`
export const mappingPath = (tenant: string, connection: string) =>
  `${connectionPath(tenant)}/${encodeURIComponent(connection)}/project-mappings`
export function mappingInput(values: Record<string, unknown>) {
  const parsed: unknown = JSON.parse(String(values.field_masters ?? "{}"))
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object")
    throw new Error("Invalid field masters")
  return {
    ...values,
    field_masters: parsed,
    codeup_repositories: values.codeup_repositories ?? [],
    trusted_pipelines: values.trusted_pipelines ?? [],
  }
}
export const integrationsApi = {
  list: <T>(url: string, cursor: string, signal?: AbortSignal) =>
    iamApi.deliveryResultRequest<Page<T>>(
      {
        method: "GET",
        url: `${url}?limit=25${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      },
      { signal }
    ),
  contract: (url: string, method: "POST" | "PATCH", signal?: AbortSignal) =>
    iamApi.deliveryResultRequest<OperationRequestContract>(
      { method: "OPTIONS", url: `${url}?method=${method}` },
      { signal, cache: "no-store" }
    ),
  save: <T>(
    url: string,
    method: "POST" | "PATCH",
    body: Record<string, unknown>,
    key: string,
    version?: number
  ) =>
    iamApi.deliveryResultRequest<T>(
      { method, url },
      {
        headers: {
          "Idempotency-Key": key,
          ...(version ? { "If-Match": `"${version}"` } : {}),
        },
        body: JSON.stringify(body),
      }
    ),
  check: (tenant: string, connection: string) =>
    iamApi.deliveryResultRequest<unknown>(
      {
        method: "POST",
        url: `${connectionPath(tenant)}/${encodeURIComponent(connection)}/checks`,
      },
      { body: "{}" }
    ),
}

export function observationsPath(
  tenant: string,
  connection: string,
  mapping: string
) {
  return `${mappingPath(tenant, connection)}/${encodeURIComponent(mapping)}/observations`
}
