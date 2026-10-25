import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createChannelLifecycleApi } from "./channel-controls-api"

it("keeps archive pages and versioned channel commands separate, sends no body on removal, and surfaces revoked authority", async () => {
  const requests: { url: URL; method: string; body: unknown; key: string | null }[] = []
  const channel = "9007199254740993"
  const server = Bun.serve({ port: 0, async fetch(request) {
    const url = new URL(request.url)
    requests.push({ url, method: request.method, body: request.method === "PATCH" ? await request.json() : null, key: request.headers.get("Idempotency-Key") })
    if (request.headers.get("Idempotency-Key") === "denied") return Response.json({ code: 145403, message: "Denied" }, { status: 403 })
    if (request.method === "DELETE") return new Response(null, { status: 204 })
    return Response.json({ code: 0, data: { id: channel, name: "Channel", version: 2, archived: true, kind: "CUSTOM" } })
  } })
  try {
    const api = createChannelLifecycleApi(createIamApi(`http://localhost:${server.port}`))
    await api.list("7", true, "opaque+/=")
    expect((await api.get(channel)).id).toBe(channel)
    expect((await api.update(channel, { version: 1, archived: true }, "archive-key")).archived).toBe(true)
    await api.remove(channel, "9007199254740995")
    expect(requests[0]!.url.searchParams.get("archived")).toBe("true")
    expect(requests[0]!.url.searchParams.get("cursor")).toBe("opaque+/=")
    expect(requests[2]!.body).toEqual({ version: 1, archived: true })
    expect(requests[2]!.key).toBe("archive-key")
    expect(requests[3]!.url.pathname).toBe(`/channels/${channel}/members/9007199254740995`)
    expect(requests[3]!.body).toBeNull()
    await expect(api.update(channel, { version: 2, archived: false }, "denied")).rejects.toThrow()
  } finally { await server.stop(true) }
})
