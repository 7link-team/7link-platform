import {describe, expect, it} from "bun:test"
import type {iamApi} from "../../lib/iam-api"
import {knowledgeSkillsApi} from "./skills-api"
describe("versioned skill contracts", () => {
  it("preserves accepted-work distillation IDs and idempotency without supplying source or draft bytes", async () => {
    const calls: {route: Parameters<typeof iamApi.deliveryResultRequest>[0]; init?: Parameters<typeof iamApi.deliveryResultRequest>[1]}[] = []
    const api = knowledgeSkillsApi({deliveryResultRequest: async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}})
    await api.distill("9007199254740993", "9007199254740995", "9007199254740997", "retry-same")
    await api.distill("9007199254740993", "9007199254740995", "9007199254740997", "retry-same")
    expect(calls[0]).toEqual(calls[1])
    expect(calls[0]?.route.url).toBe("/tenants/9007199254740993/work-items/9007199254740995/skill-distillations")
    expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({agent_id: "9007199254740997"})
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("retry-same")
    const controller = new AbortController()
    await api.distillation("9007199254740993", "9007199254740999", controller.signal)
    expect(calls[2]?.route.url).toBe("/tenants/9007199254740993/skill-distillations/9007199254740999")
    expect(calls[2]?.init?.signal).toBe(controller.signal)
  })
  it("preserves opaque IDs, retry identities, revision fences and pin semantics", async () => {
    const calls: {route: {method: string; url: string}; init?: Omit<RequestInit, "method">}[] = []
    const request: typeof iamApi.deliveryResultRequest = async <T>(route: Parameters<typeof iamApi.deliveryResultRequest>[0], init?: Parameters<typeof iamApi.deliveryResultRequest>[1]) => {calls.push({route, init}); return {} as T}
    const api = knowledgeSkillsApi({deliveryResultRequest: request})
    const tenant = "9007199254740993", skill = "9007199254740995", version = "9007199254740997", agent = "9007199254740999"
    const content = "---\nname: review-code\ndescription: Review exact execution evidence\n---\nUse evidence."
    await api.create(tenant, content, "create-once"); await api.create(tenant, content, "create-once")
    expect(calls[0]).toEqual(calls[1]); expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({content})
    expect(new Headers(calls[0]?.init?.headers).get("Idempotency-Key")).toBe("create-once")
    await api.list(tenant, 50, "review code", "ARCHIVED")
    const url = new URL(calls[2]!.route.url, "http://test")
    expect(url.pathname).toBe(`/tenants/${tenant}/skills`); expect(url.searchParams.get("offset")).toBe("50"); expect(url.searchParams.get("limit")).toBe("50"); expect(url.searchParams.get("search")).toBe("review code"); expect(url.searchParams.has("cursor")).toBe(false)
    await api.state(tenant, skill, 4, "ARCHIVED", "archive-once")
    expect(calls[3]?.route.method).toBe("PATCH"); expect(JSON.parse(String(calls[3]?.init?.body))).toEqual({version: 4, state: "ARCHIVED"})
    await api.addVersion(tenant, skill, content, "version-once")
    expect(calls[4]?.route.url).toBe(`/tenants/${tenant}/skills/${skill}/versions`)
    await api.review(tenant, version, "APPROVED", "Human-reviewed content", "review-once")
    expect(calls[5]?.route.url).toBe(`/tenants/${tenant}/skill-versions/${version}/reviews`); expect(JSON.parse(String(calls[5]?.init?.body))).toEqual({decision: "APPROVED", reason: "Human-reviewed content"})
    await api.mount(tenant, agent, skill, version, "pin-once")
    expect(calls[6]?.route.method).toBe("PUT"); expect(calls[6]?.route.url).toBe(`/tenants/${tenant}/agents/${agent}/skills/${skill}`); expect(JSON.parse(String(calls[6]?.init?.body))).toEqual({pinned_version_id: version})
    await api.mount(tenant, agent, skill, undefined, "follow-once"); expect(JSON.parse(String(calls[7]?.init?.body))).toEqual({})
    await api.unmount(tenant, agent, skill, "remove-once"); expect(calls[8]?.route.method).toBe("DELETE"); expect(new Headers(calls[8]?.init?.headers).get("Idempotency-Key")).toBe("remove-once")
    const controller = new AbortController(); await api.versions(tenant, skill, 50, "evidence", controller.signal); expect(calls[9]?.route.url).toContain("offset=50"); expect(calls[9]?.init?.signal).toBe(controller.signal)
  })
})
