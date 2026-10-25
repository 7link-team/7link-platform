import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createConversationHubApi } from "./hub-api"

it("separates mention and reply cursors and propagates current access errors", async () => {
  const seen: URL[] = []
  const server = Bun.serve({port: 0, fetch(request) {
    const url = new URL(request.url); seen.push(url)
    if (url.searchParams.get("cursor") === "revoked") return Response.json({code: 145403, message: "Denied"}, {status: 403})
    return Response.json({code: 0, data: {items: [], has_more: true, next_cursor: "next+/="}})
  }})
  try {
    const api = createConversationHubApi(createIamApi(`http://localhost:${server.port}`))
    expect((await api.activity("7", "MENTIONS", "opaque+/=")).next_cursor).toBe("next+/=")
    await api.activity("7", "REPLIES", "")
    expect(seen[0]!.pathname).toBe("/tenants/7/conversations/activity")
    expect(seen[0]!.searchParams.get("kind")).toBe("MENTIONS")
    expect(seen[0]!.searchParams.get("cursor")).toBe("opaque+/=")
    expect(seen[1]!.searchParams.get("kind")).toBe("REPLIES")
    expect(seen[1]!.searchParams.has("cursor")).toBe(false)
    await expect(api.activity("7", "REPLIES", "revoked")).rejects.toThrow()
  } finally {await server.stop(true)}
})

it("keeps private conversations tenant scoped, preserves IDs and idempotency keys, and rejects mismatched message sources", async () => {
  const seen: { url: URL; method: string; body: unknown; key: string | null }[] = []
  const channel = "9007199254740993"
  const peer = "9007199254740995"
  const server = Bun.serve({ port: 0, async fetch(request) {
    const url = new URL(request.url)
    seen.push({ url, method: request.method, body: request.method === "POST" || request.method === "PUT" ? await request.json() : null, key: request.headers.get("Idempotency-Key") })
    if (url.pathname.endsWith("/read-marker")) return Response.json({ code: 0, data: 2 })
    if (url.searchParams.get("cursor") === "denied") return Response.json({ code: 403, message: "Forbidden" }, { status: 403 })
    if (url.pathname.endsWith("/messages")) return Response.json({ code: 0, data: request.method === "POST" ? { id: peer, channel_id: channel, body: "Hi" } : { items: [{ id: peer, channel_id: url.searchParams.get("cursor") === "foreign" ? "999" : channel, body: "Hi" }], has_more: false } })
    if (url.pathname.endsWith("/direct-channels")) return Response.json({ code: 0, data: { id: channel, kind: "DIRECT", created: true } }, { status: 201 })
    return Response.json({ code: 0, data: { items: [], has_more: false } })
  } })
  try {
    const api = createConversationHubApi(createIamApi(`http://localhost:${server.port}`))
    await api.list("7", "opaque+/=")
    await api.contacts("7", "Alice & Bob", peer)
    expect((await api.open("7", peer, "create-key")).id).toBe(channel)
    expect((await api.messages("7", channel, "")).items?.[0]?.id).toBe(peer)
    await api.send("7", channel, "Hi", "send-key")
    expect(await api.markRead("7", channel, 2)).toBe(2)
    expect(seen.map(value => value.url.pathname)).toEqual([
      "/tenants/7/conversations", "/tenants/7/human-contacts", "/tenants/7/direct-channels",
      `/tenants/7/direct-channels/${channel}/messages`, `/tenants/7/direct-channels/${channel}/messages`,
      `/tenants/7/direct-channels/${channel}/read-marker`,
    ])
    expect(seen[0]!.url.searchParams.get("cursor")).toBe("opaque+/=")
    expect(seen[1]!.url.searchParams.get("search")).toBe("Alice & Bob")
    expect(seen[1]!.url.searchParams.get("after")).toBe(peer)
    expect(seen[2]!.body).toEqual({ peer_principal_id: peer })
    expect(seen[2]!.key).toBe("create-key")
    expect(seen[4]!.body).toEqual({ body: "Hi" })
    expect(seen[4]!.key).toBe("send-key")
    expect(seen[5]!.method).toBe("PUT")
    expect(seen[5]!.body).toEqual({ seq: 2 })
    await expect(api.messages("7", channel, "foreign")).rejects.toThrow("Conversation source mismatch")
    await expect(api.list("7", "denied")).rejects.toThrow()
    const controller = new AbortController(); controller.abort()
    await expect(api.contacts("7", "", "", controller.signal)).rejects.toThrow()
    expect(seen).toHaveLength(8)
  } finally { await server.stop(true) }
})

it("uses current registered Agent principal IDs and skips retiring peers", async () => {
  const requests: URL[] = []
  const server = Bun.serve({ port: 0, fetch(request) {
    const url = new URL(request.url); requests.push(url)
    return Response.json({ code: 0, data: { items: [
      { id: "9007199254740993", principal_id: "9007199254740995", name: "Available", state: "ACTIVE" },
      { id: "9007199254740997", principal_id: "9007199254740999", name: "Retiring", state: "RETIRING" },
    ], has_more: true, next_cursor: "opaque" } })
  } })
  try {
    const api = createConversationHubApi(createIamApi(`http://localhost:${server.port}`))
    const page = await api.contacts("7", "Agent", "previous", undefined, "AGENT")
    expect(page.items).toEqual([{ id: "9007199254740995", display_name: "Available", avatar: "" }])
    expect(page.next_after).toBe("opaque")
    expect(requests[0]!.pathname).toBe("/tenants/7/agents")
    expect(requests[0]!.searchParams.get("cursor")).toBe("previous")
    expect(requests[0]!.searchParams.get("search")).toBe("Agent")
  } finally { await server.stop(true) }
})

it("uses the viewer-filtered session feed with selected project, limit50 and opaque cursor, and actual approval decisions", async () => {
 const seen: {url: URL; method: string; body: unknown}[] = []
 const server = Bun.serve({port: 0, async fetch(request) {
  const url = new URL(request.url);seen.push({url,method: request.method,body: request.method === "PUT" ? await request.json() : null})
  if (url.searchParams.get("cursor") === "revoked") return Response.json({code: 130403,message: "Denied"},{status: 403})
  if (request.method === "PUT") return Response.json({code: 0,data: {id: "91",state: "ALLOW"}})
  return Response.json({code: 0,data: {items: [{id: "81",state: "PAUSED",viewer_owned: false,current_responder: true,pending_interaction_count: 1}],has_more: true,next_cursor: "next+/="}})
 }})
 try {
  const transport = createIamApi(`http://localhost:${server.port}`)
  const api = createConversationHubApi(transport)
  const page = await api.sessions("7", "9", "opaque+/=")
  expect(page.items?.[0]).toMatchObject({id: "81",viewer_owned: false,current_responder: true,pending_interaction_count: 1})
  expect(seen[0]!.url.pathname).toBe("/tenants/7/execution-sessions")
  expect(seen[0]!.url.searchParams.get("project_id")).toBe("9")
  expect(seen[0]!.url.searchParams.get("limit")).toBe("50")
  expect(seen[0]!.url.searchParams.get("cursor")).toBe("opaque+/=")
  await transport.decideExecutionPermission("81", "91", "ALLOW", "Checked path")
  await transport.decideExecutionPermission("81", "91", "DENY", "Unsafe path")
  expect(seen[1]!.url.pathname).toBe("/executions/81/approval-requests/91/decision")
  expect(seen[1]!.method).toBe("PUT")
  expect(seen[1]!.body).toEqual({state: "ALLOW",reason: "Checked path"})
  expect(seen[2]!.body).toEqual({state: "DENY",reason: "Unsafe path"})
  await expect(api.sessions("7", "9", "revoked")).rejects.toThrow()
  const controller = new AbortController();controller.abort()
  await expect(api.sessions("7", "9", "", controller.signal)).rejects.toThrow()
 } finally {await server.stop(true)}
})
