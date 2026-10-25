import { afterEach, expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createMemoryApi } from "./memory-api"

let server: ReturnType<typeof Bun.serve> | undefined
afterEach(() => { server?.stop(true); server = undefined })

it("uses independent offset pages and versioned, idempotent memory commands", async () => {
  const requests: { url: URL; key: string | null; body: Record<string, unknown> }[] = []
  server = Bun.serve({ port: 0, async fetch(request) {
    requests.push({ url: new URL(request.url), key: request.headers.get("Idempotency-Key"), body: request.method === "GET" ? {} : await request.json() as Record<string, unknown> })
    return Response.json({ code: 0, data: { items: [], total: 0, stats: {} } })
  } })
  const api = createMemoryApi(createIamApi(`http://127.0.0.1:${server.port}`, () => "1"))
  await api.list("1", "7", { offset: 50, limit: 50, search: "evidence", kind: "EXPERIENCE", project_id: "3" })
  await api.history("1", "9", 100)
  await api.edit("1", "9", { version: 2, title: "Corrected", body: "Retain verified test evidence.", ttl_days: 30, reason: "Correct the original guidance" }, "edit-attempt")
  await api.promote("1", "9", 2, "Share reviewed guidance", "promote-attempt")
  expect(requests[0]!.url.pathname).toBe("/tenants/1/agents/7/memories")
  expect(requests[0]!.url.searchParams.get("offset")).toBe("50")
  expect(requests[0]!.url.searchParams.get("search")).toBe("evidence")
  expect(requests[0]!.url.searchParams.has("cursor")).toBe(false)
  expect(requests[1]!.url.searchParams.get("offset")).toBe("100")
  expect(requests[2]!.body.version).toBe(2)
  expect(requests[2]!.key).toBe("edit-attempt")
  expect(requests[3]!.body.target).toBe("TEAM")
  expect(requests[3]!.key).toBe("promote-attempt")
})
