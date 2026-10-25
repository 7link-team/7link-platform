import { describe, expect, it } from "bun:test"
import { createChannelMediaApi, readSearchCard } from "./media-api"
import type { iamApi } from "../../lib/iam-api"
describe("channel media API contract", () => {
  it("reads durable cited cards and rejects malformed history or unsafe links", () => {
    const card = { query: "saved query", sources: [{ title: "Source", url: "https://example.com", summary: "Original" }], conclusions: [{ text: "Finding", sources: [1] }] }
    expect(readSearchCard(card)).toEqual(card)
    expect(readSearchCard({ ...card, sources: [{ ...card.sources[0], url: "javascript:alert(1)" }] })).toBeNull()
    expect(readSearchCard({ ...card, conclusions: [{ text: "uncited", sources: [2] }] })).toBeNull()
    expect(readSearchCard({ ...card, sources: [null] })).toBeNull()
    expect(readSearchCard(undefined)).toBeNull()
  })
  it("retains string identifiers, explicit approval and stable configuration replay keys", async () => {
    const calls: { route: { method: string; url: string }; init?: Omit<RequestInit, "method"> }[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T,>(route: { method: string; url: string }, init?: Omit<RequestInit, "method">) => { calls.push({ route, init }); return {} as T }
    const api = createChannelMediaApi({ deliveryResultRequest: request })
    const id = "9223372036854775807"
    await api.list(id)
    expect(calls[0].route.url).toBe(`/channels/${id}/connectors?offset=0&limit=50`)
    await api.configure(id, "HOOK", { version: 1, endpoint: "https://receiver.example/hook", enabled: false }, "same-key")
    expect(calls[1].init?.headers).toEqual({ "Idempotency-Key": "same-key" })
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ version: 1, endpoint: "https://receiver.example/hook", enabled: false })
    await api.search(id, "query", false, "search-attempt")
    expect(JSON.parse(String(calls[2].init?.body))).toEqual({ query: "query", approved: false })
    expect(calls[2].init?.headers).toEqual({ "Idempotency-Key": "search-attempt" })
    await api.transcribe(id, "123", "456")
    expect(calls[3].route.method).toBe("POST")
    expect(calls[3].route.url).toBe(`/channels/${id}/messages/123/attachments/456/transcription`)
    expect(() => api.list("../admin")).toThrow()
  })
})
