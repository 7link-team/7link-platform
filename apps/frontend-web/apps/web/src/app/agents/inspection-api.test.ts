import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createAgentInspectionApi } from "./inspection-api"

it("reads private workspace paths without persisting content, with bounded offset and cancellation", async () => {
  const requests: Request[] = []
  const server = Bun.serve({ port: 0, fetch(request) {
    requests.push(request)
    const url = new URL(request.url)
    if (url.searchParams.get("path") === "denied") return Response.json({ code: 142403, message: "Denied" }, { status: 403 })
    return Response.json({ code: 0, data: url.pathname.endsWith("/content") ? { path: "notes/a & b.txt", content: "private" } : { path: "notes", items: [{ name: "a & b.txt", kind: "FILE", size: 7 }], total: 51, has_more: true } }, { headers: { "Cache-Control": "no-store" } })
  } })
  try {
    const api = createAgentInspectionApi(createIamApi(`http://localhost:${server.port}`))
    const page = await api.workspace("7", "9007199254740995", "notes", 50, "a & b")
    expect(page.items[0]?.name).toBe("a & b.txt")
    const listing = new URL(requests[0]!.url)
    expect(listing.pathname).toBe("/tenants/7/agents/9007199254740995/workspace-entries")
    expect(listing.searchParams.get("offset")).toBe("50")
    expect(listing.searchParams.get("limit")).toBe("50")
    expect(listing.searchParams.get("search")).toBe("a & b")
    const content = await api.content("7", "9007199254740995", "notes/a & b.txt")
    expect(content.content).toBe("private")
    expect(new URL(requests[1]!.url).searchParams.get("path")).toBe("notes/a & b.txt")
    expect(requests.every(request => request.method === "GET")).toBe(true)
    await expect(api.workspace("7", "9007199254740995", "denied", 0, "")).rejects.toThrow()
    const abort = new AbortController(); abort.abort()
    await expect(api.content("7", "9007199254740995", "notes/a", abort.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})

it("browses the selected tenant directory with opaque pagination and propagates denial and cancellation", async () => {
  const seen: Request[] = []
  const server = Bun.serve({ port: 0, fetch(request) {
    seen.push(request)
    if (new URL(request.url).searchParams.get("search") === "denied") return Response.json({ code: 142403, message: "Forbidden" }, { status: 403 })
    return Response.json({ code: 0, data: { items: [{ id: "9007199254740993", principal_id: "9007199254740995", name: "Actual Agent" }], has_more: true, next_cursor: "opaque+/=" } })
  } })
  try {
    const api = createAgentInspectionApi(createIamApi(`http://localhost:${server.port}`))
    const result = await api.directory("7", "Agent & model", "opaque+/=")
    expect(result.items?.[0]?.principal_id).toBe("9007199254740995")
    const url = new URL(seen[0]!.url)
    expect(url.pathname).toBe("/tenants/7/agents")
    expect(url.searchParams.get("search")).toBe("Agent & model")
    expect(url.searchParams.get("cursor")).toBe("opaque+/=")
    expect(url.searchParams.get("limit")).toBe("30")
    expect(seen[0]!.method).toBe("GET")
    await expect(api.directory("7", "denied", "")).rejects.toThrow()
    const controller = new AbortController(); controller.abort()
    await expect(api.directory("7", "", "", controller.signal)).rejects.toThrow()
    expect(seen).toHaveLength(2)
  } finally { await server.stop(true) }
})

it("reads only the selected Agent with explicit tenant, channel and cursor and propagates denial", async () => {
  const seen: URL[] = []
  const server = Bun.serve({ port: 0, fetch(request) {
    const url = new URL(request.url)
    seen.push(url)
    return url.searchParams.get("cursor") === "denied"
      ? Response.json({ code: 142403, message: "Forbidden" }, { status: 403 })
      : Response.json({ code: 0, data: { items: [], has_more: false } })
  } })
  try {
    const api = createAgentInspectionApi(createIamApi(`http://localhost:${server.port}`))
    await api.work("1", "7", "")
    await api.channels("1", "7", "20")
    await api.messages("1", "7", "20", "30")
    expect(seen.map(url => url.pathname)).toEqual([
      "/tenants/1/agents/7/work", "/tenants/1/agents/7/direct-channels", "/tenants/1/agents/7/direct-channels/20/messages",
    ])
    expect(seen[0]!.searchParams.has("cursor")).toBe(false)
    expect(seen[1]!.searchParams.get("cursor")).toBe("20")
    expect(seen[2]!.searchParams.get("cursor")).toBe("30")
    expect(seen.every(url => url.searchParams.get("limit") === "30")).toBe(true)
    await expect(api.channels("1", "7", "denied")).rejects.toThrow()
  } finally { await server.stop(true) }
})
