import {describe, expect, it} from "bun:test"
import type {iamApi} from "../../lib/iam-api"
import {highlightedParts, nativeKnowledgeApi} from "./native-api"

describe("native retrieval contracts", () => {
  it("keeps opaque IDs, paging, abort, credential proof and CAS intact", async () => {
    const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}
    const api = nativeKnowledgeApi({deliveryResultRequest: request})
    const controller = new AbortController()
    await api.search("9007199254740993", {query: "预算 安全", match_mode: "ALL", project_ids: ["9007199254740995"], offset: 50, limit: 50, explain: false}, "same-search", controller.signal)
    expect(calls[0]?.route).toEqual({method: "POST", url: "/tenants/9007199254740993/knowledge-searches"})
    expect(calls[0]?.init?.signal).toBe(controller.signal)
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("same-search")
    expect(JSON.parse(String(calls[0]?.init?.body))).toMatchObject({project_ids: ["9007199254740995"], offset: 50, limit: 50, explain: false})
    await api.save("19", {base_url: "https://embed.example.net", model: "configured-model", dims: 768, api_key: "ephemeral-test-key", reauth_proof: "verified-proof", batch_size: 64, rpm: 60, tpm: 150000, timeout_ms: 1500, enabled: true, version: 7})
    expect(calls[1]?.route.method).toBe("PUT")
    expect(JSON.parse(String(calls[1]?.init?.body))).toMatchObject({version: 7, reauth_proof: "verified-proof"})
    await api.probe("19")
    await api.backfill("19")
    expect(calls[2]?.route.url).toEndWith("/knowledge-embedding-probes")
    expect(JSON.parse(String(calls[2]?.init?.body))).toEqual({})
    expect(JSON.parse(String(calls[3]?.init?.body))).toEqual({limit: 64})
    await api.probe("19", {base_url: "https://embed.example.net", model: "unsaved-model", dims: 384, api_key: "ephemeral-test-key", reauth_proof: "single-use-probe-proof", batch_size: 16, rpm: 60, tpm: 1000, timeout_ms: 1500, enabled: true, version: 7})
    expect(calls[4]?.route.method).toBe("POST")
    expect(JSON.parse(String(calls[4]?.init?.body))).toMatchObject({model: "unsaved-model", reauth_proof: "single-use-probe-proof"})
  })
  it("preserves Unicode text and rejects invalid highlight ranges", () => {
    expect(highlightedParts("A🙂预算<script>", [{start: 2, end: 4}])).toEqual([{text: "A🙂", marked: false}, {text: "预算", marked: true}, {text: "<script>", marked: false}])
    expect(highlightedParts("安全", [{start: -1, end: 1}, {start: 0, end: 999}, {start: 1.5, end: 2}])).toEqual([{text: "安全", marked: false}])
  })
})
