import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createProcessApi } from "./process-api"

it("requests bounded observed history and repeats the exact start/stop request key", async () => {
  const seen: { method: string; path: string; key: string | null; body: unknown }[] = []
  const server = Bun.serve({ port: 0, async fetch(request) {
    const url = new URL(request.url)
    seen.push({ method: request.method, path: url.pathname + url.search, key: request.headers.get("Idempotency-Key"), body: request.method === "PUT" ? await request.json() : null })
    return Response.json({ code: 0, data: request.method === "GET" ? { items: [{ id: "9007199254740995", kind: "CRASHED" }], has_more: true } : { state: "STOPPED", process_status: "STOPPING" } })
  } })
  try {
    const api = createProcessApi(createIamApi(`http://localhost:${server.port}`))
    expect((await api.list("7", "9007199254740993", 50, "crash")).items[0]?.id).toBe("9007199254740995")
    expect(seen[0]?.path).toBe("/tenants/7/agents/9007199254740993/process-events?offset=50&limit=50&search=crash")
    await api.change("7", "9007199254740993", "STOPPED", "same-request")
    await api.change("7", "9007199254740993", "STOPPED", "same-request")
    expect(seen[1]).toEqual({ method: "PUT", path: "/tenants/7/agents/9007199254740993/run-state", key: "same-request", body: { desired: "STOPPED" } })
    expect(seen[2]).toEqual(seen[1])
  } finally { await server.stop(true) }
})
