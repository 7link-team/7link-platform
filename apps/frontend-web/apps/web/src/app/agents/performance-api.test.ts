import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createPerformanceApi } from "./performance-api"

it("preserves principal IDs, exact stats filters, current authority and cancellation", async () => {
  const observed: string[] = []
  const server = Bun.serve({ port: 0, fetch(request) {
    const url = new URL(request.url)
    observed.push(url.pathname + url.search)
    if (url.searchParams.get("work_item_type") === "BUG") return Response.json({ message: "Forbidden" }, { status: 403 })
    return Response.json({ code: 0, data: { fpa: null, cpa_micros: null, by_type: [] } })
  } })
  try {
    const api = createPerformanceApi(createIamApi(`http://localhost:${server.port}`))
    expect((await api.read("9007199254740993", "9007199254740995", 90, "")).fpa).toBeNull()
    expect(observed[0]).toBe("/tenants/9007199254740993/agents/9007199254740995/stats?window_days=90")
    await expect(api.read("1", "7", 30, "BUG")).rejects.toThrow()
    expect(observed[1]).toContain("window_days=30&work_item_type=BUG")
    const abort = new AbortController(); abort.abort()
    await expect(api.read("1", "7", 30, "", abort.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})
