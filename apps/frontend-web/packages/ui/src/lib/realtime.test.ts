import { afterEach, describe, expect, it } from "bun:test"
import { createRealtimeClient, readEventStream, type RealtimeClient, type RealtimeEvent, type SSEFrame } from "./realtime"

const encoder = new TextEncoder()
const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))
async function until(predicate: () => boolean, timeout = 3000) {
  const end = Date.now() + timeout
  while (!predicate()) { if (Date.now() >= end) throw new Error("Realtime condition timed out"); await delay(10) }
}

describe("SSE stream parser", () => {
  it("keeps UTF-8, CRLF, comments and multiline data across every byte boundary", async () => {
    const bytes = encoder.encode(": hb\r\n\r\nevent: event\r\nid: 9223372036854775807\r\ndata: {\"name\":\"通知\",\r\ndata: \"value\":1}\r\n\r\nretry: 1500\n\nevent: reconnect\ndata: {}\n\n")
    const stream = new ReadableStream<Uint8Array>({ start(controller) { for (const byte of bytes) controller.enqueue(new Uint8Array([byte])); controller.close() } })
    const frames: SSEFrame[] = []
    await readEventStream(stream.getReader(), (frame) => frames.push(frame))
    expect(frames).toHaveLength(3)
    expect(frames[0]).toEqual({ event: "event", id: "9223372036854775807", data: "{\"name\":\"通知\",\n\"value\":1}" })
    expect(frames[1]?.retry).toBe(1500)
    expect(frames[2]?.event).toBe("reconnect")
  })
  it("rejects unbounded frames and ignores malformed IDs and unfinished frames", async () => {
    const parse = async (text: string) => {
      const frames: SSEFrame[] = []
      const stream = new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(encoder.encode(text)); controller.close() } })
      await readEventStream(stream.getReader(), (frame) => frames.push(frame))
      return frames
    }
    expect(await parse("id: bad\0id\ndata: ok\n\ndata: unfinished")).toEqual([{ event: "message", data: "ok" }])
    await expect(parse(`data: ${"a".repeat(129 * 1024)}\n\n`)).rejects.toThrow("too large")
    await expect(parse(`${`data: ${"a".repeat(1024)}\n`.repeat(129)}\n`)).rejects.toThrow("too large")
  })
})

describe("realtime fetch client", () => {
  let server: ReturnType<typeof Bun.serve> | undefined
  const clients: RealtimeClient[] = []
  afterEach(() => { for (const client of clients.splice(0)) client.close(); server?.stop(true) })
  const client = (request: () => Pick<RequestInit, "headers" | "credentials"> = () => ({ credentials: "omit", headers: { Authorization: "Bearer account-view" } }), onUnauthorized?: () => void) => {
    const value = createRealtimeClient({ url: (tenant, topics) => `http://127.0.0.1:${server!.port}/tenants/${tenant}/event-stream?${topics.map((topic) => `topic=${encodeURIComponent(topic)}`).join("&")}`, request, onUnauthorized })
    clients.push(value)
    return value
  }
  it("aborts page-exit streams and resumes only on a restored page", async () => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, "window")
    const page = new EventTarget()
    Object.defineProperty(globalThis, "window", { configurable: true, value: page })
    let requests = 0, aborted = 0, resyncs = 0
    server = Bun.serve({ port: 0, fetch(request) {
      requests++
      request.signal.addEventListener("abort", () => aborted++)
      return new Response(new ReadableStream({ start(controller) {
        controller.enqueue(encoder.encode('event: ready\ndata: {}\n\n'))
      } }), { headers: { "Content-Type": "text/event-stream" } })
    } })
    const c = client()
    try {
      c.subscribe("1", ["tenant/1/projects"], { onResync: () => resyncs++ })
      for (let navigation = 1; navigation <= 3; navigation++) {
        await until(() => requests === navigation && c.getSnapshot().status === "live")
        page.dispatchEvent(new Event(navigation === 3 ? "unload" : "pagehide"))
        await until(() => aborted === navigation)
        page.dispatchEvent(new Event("online"))
        await delay(350)
        expect(requests).toBe(navigation)
        if (navigation < 3) page.dispatchEvent(new Event("pageshow"))
      }
      expect(resyncs).toBe(2)
      c.close()
      page.dispatchEvent(new Event("pageshow"))
      await delay(350)
      expect(requests).toBe(3)
    } finally {
      c.close()
      if (previous) Object.defineProperty(globalThis, "window", previous)
      else Reflect.deleteProperty(globalThis, "window")
    }
  })
  it("uses one authenticated fetch for the topic union, resumes exact int64 IDs, and filters callbacks", async () => {
    const requests: Request[] = []
    const streams: ReadableStreamDefaultController<Uint8Array>[] = []
    server = Bun.serve({ port: 0, fetch(request) {
      requests.push(request)
      return new Response(new ReadableStream<Uint8Array>({ start(controller) { streams.push(controller); controller.enqueue(encoder.encode('event: ready\ndata: {"denied":[]}\n\n')) } }), { headers: { "Content-Type": "text/event-stream" } })
    } })
    const c = client()
    const inbox: RealtimeEvent[] = [], project: RealtimeEvent[] = []
    const stopInbox = c.subscribe("1", ["tenant/1/inbox/10"], { onEvent: (event) => inbox.push(event) })
    const stopProject = c.subscribe("1", ["tenant/1/project/2"], { onEvent: (event) => project.push(event) })
    await until(() => c.getSnapshot().status === "live")
    expect(requests).toHaveLength(1)
    expect(requests[0]?.headers.get("Authorization")).toBe("Bearer account-view")
    expect(new URL(requests[0]!.url).searchParams.getAll("topic")).toEqual(["tenant/1/inbox/10", "tenant/1/project/2"])
    streams[0]!.enqueue(encoder.encode('id: 9223372036854775807\nevent: event\ndata: {"type":"notification.created","topics":["tenant/1/inbox/10"],"occurred_at":"2026-10-20T00:00:00Z"}\n\n'))
    await until(() => inbox.length === 1)
    expect(project).toHaveLength(0)
    expect(inbox[0]?.id).toBe("9223372036854775807")
    expect(inbox[0]).not.toHaveProperty("payload")
    stopProject()
    await until(() => requests.length === 2)
    expect(requests[1]?.headers.get("Last-Event-ID")).toBe("9223372036854775807")
    stopInbox()
  })
  it("isolates tenant cursors and discards an old connection after switching workspace", async () => {
    const requests: Request[] = []
    let send: ReadableStreamDefaultController<Uint8Array>
    server = Bun.serve({ port: 0, fetch(request) { requests.push(request); return new Response(new ReadableStream<Uint8Array>({ start(controller) { send = controller; controller.enqueue(encoder.encode('event: ready\ndata: {}\n\n')) } }), { headers: { "Content-Type": "text/event-stream" } }) } })
    const c = client();const first = c.subscribe("1", ["tenant/1/projects"], {})
    await until(() => c.getSnapshot().status === "live")
    send!.enqueue(encoder.encode('id: 10\nevent: event\ndata: {"type":"project.changed","topics":["tenant/1/projects"]}\n\n'))
    await until(() => c.getSnapshot().lastEventAt !== undefined)
    first();c.subscribe("2", ["tenant/2/projects"], {})
    await until(() => requests.length === 2)
    expect(requests[1]?.headers.has("Last-Event-ID")).toBe(false)
    expect(requests[1]?.url).toContain("/tenants/2/")
  })
  it("falls back after three failures, then resyncs on recovery", async () => {
    let failed = true, requests = 0, resyncs = 0
    server = Bun.serve({ port: 0, fetch() { requests++;return failed ? new Response("unavailable", { status: 503 }) : new Response(new ReadableStream({ start(controller) { controller.enqueue(encoder.encode('event: ready\ndata: {"denied":[]}\n\n')) } }), { headers: { "Content-Type": "text/event-stream" } }) } })
    const c = client();c.subscribe("1", ["tenant/1/projects"], { onResync: () => resyncs++ })
    await until(() => c.getSnapshot().status === "polling", 7000)
    expect(requests).toBe(3)
    failed = false;c.subscribe("1", ["tenant/1/inbox/10"], {})
    await until(() => c.getSnapshot().status === "live")
    expect(resyncs).toBe(1)
  }, 10000)
  it("resyncs on control frames and routes expired credentials to the host auth flow", async () => {
    let unauthorized = 0, resyncs = 0
    server = Bun.serve({ port: 0, fetch() { return new Response(new ReadableStream({ start(controller) { controller.enqueue(encoder.encode('event: ready\ndata: {}\n\nevent: resync_required\ndata: {"reason":"cursor_too_old"}\n\nevent: reauth_required\ndata: {}\n\n')) } }), { headers: { "Content-Type": "text/event-stream" } }) } })
    const c = client(undefined, () => unauthorized++);c.subscribe("1", ["tenant/1/projects"], { onResync: () => resyncs++ })
    await until(() => unauthorized === 1)
    expect(resyncs).toBe(1)
    expect(c.getSnapshot().status).toBe("offline")
  })
})
