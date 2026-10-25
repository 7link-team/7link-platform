import { iamApi } from "../../lib/iam-api"

export type Message = { id: string | null; author_id: string | null; body: string | null; version: number; created_at: string; retracted?: boolean; edited?: boolean }
export type Revision = { revision: number; body: string; edited_by: string | null; edited_at: string }
export function messageActionAvailability(message: Message, actor: string | undefined, manage: boolean, now = Date.now()) {
  const age = now - Date.parse(message.created_at)
  const own = !!actor && actor === message.author_id
  const active = !!message.id && !message.retracted && Number.isFinite(age) && age >= 0
  return { edit: active && own && age <= 15 * 60_000 && message.version <= 10, retract: active && ((own && age <= 24 * 60 * 60_000) || manage) }
}


export function createMessageActionsApi(api: Pick<typeof iamApi, "deliveryResultRequest">) {
  const base = (tenant: string, id: string) => `/tenants/${encodeURIComponent(tenant)}/messages/${encodeURIComponent(id)}`
  return {
    edit: (tenant: string, id: string, version: number, body: string, key: string, signal?: AbortSignal) => api.deliveryResultRequest<Message>({ method: "PATCH", url: base(tenant,id) }, { headers: { "If-Match": `"${version}"`, "Idempotency-Key": key }, body: JSON.stringify({body}), signal }),
    retract: (tenant: string, id: string, version: number, key: string, signal?: AbortSignal) => api.deliveryResultRequest<void>({ method: "DELETE", url: base(tenant,id) }, { headers: { "If-Match": `"${version}"`, "Idempotency-Key": key }, signal }),
    revisions: (tenant: string, id: string, signal?: AbortSignal) => api.deliveryResultRequest<Revision[]>({ method: "GET", url: `${base(tenant,id)}/revisions` }, { signal }),
  }
}
export const messageActionsApi = createMessageActionsApi(iamApi)
