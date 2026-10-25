import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createResetHistoryApi } from "./reset-history-api"

it("reads bounded reset history in the selected tenant and principal, preserves outcomes, and surfaces denial", async () => {
  const requests: URL[] = []
  const server = Bun.serve({ port: 0, fetch(request) {
    const url = new URL(request.url); requests.push(url)
    if (url.searchParams.get("cursor") === "denied") return Response.json({ code: 142403, message: "Denied" }, { status: 403 })
    return Response.json({ code: 0, data: { items: [{ id: "9007199254740997", kind: "FULL", state: "REQUESTED", reason: "Pending runner", requested_by: "4" }], has_more: true, next_cursor: "9007199254740997" } })
  } })
  try {
    const api = createResetHistoryApi(createIamApi(`http://localhost:${server.port}`))
    const page = await api.list("7", "9007199254740995", "9007199254740999")
    expect(page.items[0]!.state).toBe("REQUESTED")
    expect(page.items[0]!.id).toBe("9007199254740997")
    expect(requests[0]!.pathname).toBe("/tenants/7/agents/9007199254740995/resets")
    expect(requests[0]!.searchParams.get("cursor")).toBe("9007199254740999")
    expect(requests[0]!.searchParams.get("limit")).toBe("20")
    await expect(api.list("7", "9007199254740995", "denied")).rejects.toThrow()
    const controller = new AbortController(); controller.abort()
    await expect(api.list("7", "9007199254740995", "", controller.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})
