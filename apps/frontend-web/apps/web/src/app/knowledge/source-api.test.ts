import { describe, expect, it } from "bun:test"
import type { iamApi } from "../../lib/iam-api"
import { knowledgeSourceApi } from "./source-api"

describe("knowledge source command contracts", () => {
  it("keeps multipart boundaries, opaque cursors and metadata versions intact", async () => {
    const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}
    const api = knowledgeSourceApi({deliveryResultRequest: request})
    const file = new File(["# Evidence"], "notes.md", {type: "text/markdown"})
    await api.upload("19", "9007199254740993", [file], "upload-retry")
    expect(calls[0]?.route.method).toBe("POST")
    expect(calls[0]?.route.url).toContain("project_id=9007199254740993")
    expect(calls[0]?.init?.body).toBeInstanceOf(FormData)
    expect((calls[0]?.init?.body as FormData).getAll("files")).toHaveLength(1)
    expect(new Headers(calls[0]?.init?.headers).has("Content-Type")).toBe(false)
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("upload-retry")
    await api.chunkPage("19", "9007199254740995", "opaque+/=")
    const chunkURL = new URL(calls[1]!.route.url, "http://test")
    expect(chunkURL.searchParams.get("cursor")).toBe("opaque+/=")
    expect(chunkURL.searchParams.has("offset")).toBe(false)
    await api.metadata("19", "9007199254740995", {version: 7, title: "Revised", tags: ["audit"]}, "metadata-retry")
    expect(calls[2]?.route.method).toBe("PATCH")
    expect(JSON.parse(String(calls[2]?.init?.body))).toEqual({version: 7, title: "Revised", tags: ["audit"]})
    await api.archive("19", "9007199254740995", 8, "archive-retry")
    expect(calls[3]?.route.url).toEndWith("/archives")
    expect(JSON.parse(String(calls[3]?.init?.body))).toEqual({version: 8})
    await api.saveSettings("19", {version: 2, candidate_limit: 30, index_strategy: "LEXICAL", source_permission: "MANAGERS", memory_approval: "ALL_MEMORIES"}, "policy-retry")
    expect(JSON.parse(String(calls[4]?.init?.body)).memory_approval).toBe("ALL_MEMORIES")
  })
  it("queues durable ingestion and reads factual usage with opaque IDs", async () => {
    const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}
    const api = knowledgeSourceApi({deliveryResultRequest: request})
    await api.ingest("19", "9007199254740995", {version: 4}, "queue-retry")
    expect(calls[0]?.route.method).toBe("POST")
    expect(calls[0]?.route.url).toBe("/tenants/19/knowledge/sources/9007199254740995/ingestions")
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({version: 4})
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("queue-retry")
    const controller = new AbortController()
    await api.usage("19", "9007199254740995", controller.signal)
    expect(calls[1]?.route.method).toBe("GET")
    expect(calls[1]?.route.url).toBe("/tenants/19/knowledge/sources/9007199254740995/usage")
    expect(calls[1]?.init?.signal).toBe(controller.signal)
  })
  it("preserves opaque source/project IDs, current version and retry identity", async () => {
    const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}
    const api = knowledgeSourceApi({deliveryResultRequest: request})
    await api.list("19", {scope: "PROJECT", project_id: "9007199254740993", state: "FAILED", limit: 50})
    expect(calls[0]?.route.url).toContain("project_id=9007199254740993")
    await api.sync("19", "9007199254740995", {version: 7, content: "# Actual source\n金额"}, "retry-same-content")
    expect(calls[1]?.route.method).toBe("POST")
    expect(calls[1]?.route.url).toBe("/tenants/19/knowledge/sources/9007199254740995/syncs")
    expect(JSON.parse(String(calls[1]?.init?.body))).toEqual({version: 7, content: "# Actual source\n金额"})
    expect(new Headers(calls[1]?.init?.headers).get("Idempotency-Key")).toBe("retry-same-content")
    await api.reindex("19", "9007199254740995", 8, "rebuild-once")
    await api.invalidate("19", "9007199254740995", 9, "invalidate-once")
    expect(JSON.parse(String(calls[2]?.init?.body))).toEqual({version: 8})
    expect(JSON.parse(String(calls[3]?.init?.body))).toEqual({version: 9})
    expect(calls[3]?.route.url).toEndWith("/invalidations")
  })
  it("uses the saved setting version and paginates real chunk offsets", async () => {
    const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}
    const api = knowledgeSourceApi({deliveryResultRequest: request})
    await api.saveSettings("8", {version: 4, candidate_limit: 23}, "settings-retry")
    expect(calls[0]?.route.method).toBe("PUT")
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({version: 4, candidate_limit: 23})
    await api.chunks("8", "7", 100)
    expect(calls[1]?.route.url).toContain("offset=100")
    expect(calls[1]?.route.url).toContain("limit=50")
  })
})
