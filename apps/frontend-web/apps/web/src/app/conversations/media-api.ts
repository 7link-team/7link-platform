import { iamApi } from "../../lib/iam-api"
export type ConnectorKind = "HOOK" | "HTTP_BRIDGE" | "TRANSCRIPTION" | "SEARCH"
export type ChannelConnector = { id: string; kind: ConnectorKind; endpoint: string; enabled: boolean; version: number }
export type SearchCard = { message_id?: string; query: string; sources: { title: string; url: string; summary: string }[]; conclusions: { text: string; sources: number[] }[] }

export function readSearchCard(payload: Record<string, unknown> | undefined): SearchCard | null {
  if (!payload || typeof payload.query !== "string" || !Array.isArray(payload.sources) || !Array.isArray(payload.conclusions)) return null
  const sources: SearchCard["sources"] = []
  for (const source of payload.sources) {
    if (!source || typeof source !== "object" || typeof source.title !== "string" || typeof source.url !== "string" || typeof source.summary !== "string") return null
    try { if (!["http:", "https:"].includes(new URL(source.url).protocol)) return null } catch { return null }
    sources.push({ title: source.title, url: source.url, summary: source.summary })
  }
  const conclusions: SearchCard["conclusions"] = []
  for (const conclusion of payload.conclusions) {
    if (!conclusion || typeof conclusion !== "object" || typeof conclusion.text !== "string" || !Array.isArray(conclusion.sources) || conclusion.sources.some((ref: unknown) => typeof ref !== "number" || !Number.isInteger(ref) || ref < 1 || ref > sources.length)) return null
    conclusions.push({ text: conclusion.text, sources: conclusion.sources })
  }
  return { query: payload.query, sources, conclusions }
}
function channelPath(channel: string) {
  if (!/^[1-9][0-9]{0,18}$/.test(channel)) throw new Error("Invalid channel identifier")
  return `/channels/${channel}`
}
export function createChannelMediaApi(api: Pick<typeof iamApi, "deliveryResultRequest">) {
  return {
    list: (channel: string, signal?: AbortSignal) => api.deliveryResultRequest<{ items: ChannelConnector[] }>({ method: "GET", url: `${channelPath(channel)}/connectors?offset=0&limit=50` }, { signal }),
    configure: (channel: string, kind: ConnectorKind, input: { version: number; endpoint: string; secret?: string; enabled: boolean }, key: string, signal?: AbortSignal) => api.deliveryResultRequest<ChannelConnector>({ method: "PUT", url: `${channelPath(channel)}/connectors/${kind}` }, { body: JSON.stringify(input), headers: { "Idempotency-Key": key }, signal }),
    transcribe: (channel: string, message: string, attachment: string, signal?: AbortSignal) => api.deliveryResultRequest<{ text: string }>({ method: "POST", url: `${channelPath(channel)}/messages/${encodeURIComponent(message)}/attachments/${encodeURIComponent(attachment)}/transcription` }, { signal }),
    search: (channel: string, query: string, approved: boolean, key: string, signal?: AbortSignal) => api.deliveryResultRequest<SearchCard>({ method: "POST", url: `${channelPath(channel)}/search-cards` }, { body: JSON.stringify({ query, approved }), headers: { "Idempotency-Key": key }, signal }),
  }
}
export const channelMediaApi = createChannelMediaApi(iamApi)
