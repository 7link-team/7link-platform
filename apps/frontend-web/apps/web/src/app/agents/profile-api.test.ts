import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createProfileApi } from "./profile-api"

it("reads private profile without cache and preserves exact snowflake IDs, version and retry key on PATCH", async () => {
  const seen: { method: string; path: string; key: string | null; body: unknown }[] = []
  const server = Bun.serve({ port: 0, async fetch(request) {
    const url = new URL(request.url)
    seen.push({ method: request.method, path: url.pathname, key: request.headers.get("Idempotency-Key"), body: request.method === "PATCH" ? await request.json() : null })
    if (url.pathname.endsWith("avatar-candidates")) return Response.json({ code: 0, data: [{ style: "PIXEL", seed: 42, url: "data:image/png;base64,AA==" }] })
    return Response.json({ code: 0, data: { principal_id: "9007199254740995", version: 3, persona: "private" } })
  } })
  try {
    const api = createProfileApi(createIamApi(`http://localhost:${server.port}`))
    expect((await api.read("7", "9007199254740995")).principal_id).toBe("9007199254740995")
    const patch = { version: 2, persona: "private", avatar_seed: 9007199254740991, default_channel_names: ["design"], memory_policy: { profile: "STANDARD" } }
    await api.update("7", "9007199254740995", patch, "stable-retry-key")
    await api.update("7", "9007199254740995", patch, "stable-retry-key")
    expect(seen[1]).toEqual({ method: "PATCH", path: "/tenants/7/agents/9007199254740995/profile", key: "stable-retry-key", body: patch })
    expect(seen[2]).toEqual(seen[1])
    expect((await api.candidates("7", "9007199254740995", "PIXEL", 1))[0]?.seed).toBe(42)
    const abort = new AbortController(); abort.abort()
    await expect(api.read("7", "9007199254740995", abort.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})

it("propagates current profile authority and version conflicts", async () => {
  const server = Bun.serve({ port: 0, fetch(request) { return Response.json({ code: 142409, message: "Conflict" }, { status: request.method === "PATCH" ? 409 : 403 }) } })
  try {
    const api = createProfileApi(createIamApi(`http://localhost:${server.port}`))
    await expect(api.read("7", "8")).rejects.toThrow()
    await expect(api.update("7", "8", { version: 1, name: "Updated" }, "request-key")).rejects.toThrow()
  } finally { await server.stop(true) }
})
