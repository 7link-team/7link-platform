import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createRetirementApi } from "./retirement-api"

it("fences retirement commands separately from current machine version, preserves IDs, propagates denial and cancellation", async () => {
  const seen: { path: string; method: string; version: string | null; key: string | null; body: unknown }[] = []
  const server = Bun.serve({ port: 0, async fetch(request) {
    seen.push({ path: new URL(request.url).pathname, method: request.method, version: request.headers.get("If-Match"), key: request.headers.get("Idempotency-Key"), body: request.method === "GET" ? null : await request.json() })
    if (request.headers.get("Idempotency-Key") === "denied") return Response.json({ message: "Forbidden" }, { status: 403 })
    return Response.json({ code: 0, data: { id: "9007199254740997", version: 4 } })
  } })
  try {
    const api = createRetirementApi(createIamApi(`http://localhost:${server.port}`))
    await api.start("9007199254740993", "9007199254740995", 12, { mode: "FORCED", reason: "Machine unrecoverable", confirm_name: "Current name", reauth_proof: "fresh-proof" }, "start")
    expect(seen[0]).toEqual({ path: "/tenants/9007199254740993/agents/9007199254740995/retirements", method: "POST", version: "12", key: "start", body: { mode: "FORCED", reason: "Machine unrecoverable", confirm_name: "Current name", reauth_proof: "fresh-proof" } })
    for (const action of [{ successor_kind: "HUMAN" as const, successor_principal_id: "9007199254740999" }, { successor_kind: "LATER" as const }, { handover_retry: true as const }, { advance: true as const }, { state: "CANCELLED" as const }]) {
      await api.patch("1", "9007199254740997", 4, { agent_version: 15, ...action }, "same-attempt")
      expect(seen.at(-1)).toEqual({ path: "/tenants/1/agent-retirements/9007199254740997", method: "PATCH", version: "4", key: "same-attempt", body: { agent_version: 15, ...action } })
    }
    await api.read("1", "9007199254740997")
    expect(seen.at(-1)?.method).toBe("GET")
    await api.handover("1", "9007199254740997")
    expect(seen.at(-1)?.path).toBe("/tenants/1/agent-retirements/9007199254740997/handover")
    await api.binding("1", "7")
    expect(seen.at(-1)?.path).toBe("/tenants/1/agents/7/machine-binding")
    await expect(api.patch("1", "7", 4, { agent_version: 15, advance: true }, "denied")).rejects.toThrow()
    const abort = new AbortController(); abort.abort()
    await expect(api.read("1", "7", abort.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})
