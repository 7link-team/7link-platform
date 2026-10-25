export type RealtimeStatus = "connecting" | "live" | "polling" | "offline"
export interface RealtimeEvent {
  id: string
  type: string
  topics: string[]
  subject?: { kind: string; id: string }
  subjectVersion?: number
  payload?: unknown
  occurredAt: string
}
export interface RealtimeClientOptions {
  url: (tenantID: string, topics: string[]) => string
  request: () => Pick<RequestInit, "headers" | "credentials">
  onUnauthorized?: () => void
}
export interface RealtimeSnapshot { status: RealtimeStatus; lastEventAt?: number }
export interface RealtimeListener {
  onEvent?: (event: RealtimeEvent) => void
  onResync?: () => void
}
export interface RealtimeClient {
  subscribe: (tenantID: string, topics: string[], listener: RealtimeListener) => () => void
  subscribeStatus: (listener: () => void) => () => void
  getSnapshot: () => RealtimeSnapshot
  close: () => void
}
export interface SSEFrame { event: string; data: string; id?: string; retry?: number }

// SSE is line based, not chunk based. Preserve split UTF-8, CRLF and multiline
// data across network reads; comment heartbeats never become application data.
export async function readEventStream(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  onFrame: (frame: SSEFrame) => void,
): Promise<void> {
  const decoder = new TextDecoder()
  let pending = ""
  let frame: SSEFrame = { event: "message", data: "" }
  let data: string[] = []
  let dataLength = 0
  const line = (value: string) => {
    if (value === "") {
      if (data.length || frame.event !== "message" || frame.retry !== undefined) {
        onFrame({ ...frame, data: data.join("\n") })
      }
      frame = { event: "message", data: "" }
      data = []
      dataLength = 0
      return
    }
    if (value.startsWith(":")) return
    const colon = value.indexOf(":")
    const field = colon < 0 ? value : value.slice(0, colon)
    const content = colon < 0 ? "" : value.slice(colon + 1).replace(/^ /, "")
    if (field === "event") frame.event = content
    if (field === "data") {
      dataLength += content.length
      if (dataLength > 128 * 1024) throw new Error("Event stream frame is too large")
      data.push(content)
    }
    if (field === "id" && !content.includes("\0")) frame.id = content
    if (field === "retry" && /^\d+$/.test(content)) frame.retry = Number(content)
  }
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      pending += decoder.decode(value, { stream: true })
      if (pending.length > 128 * 1024) throw new Error("Event stream frame is too large")
      for (;;) {
        const index = pending.search(/[\r\n]/)
        if (index < 0 || (pending[index] === "\r" && index === pending.length - 1)) break
        const width = pending[index] === "\r" && pending[index + 1] === "\n" ? 2 : 1
        line(pending.slice(0, index))
        pending = pending.slice(index + width)
      }
    }
  } finally {
    reader.releaseLock()
  }
}

export function createRealtimeClient(opts: RealtimeClientOptions): RealtimeClient {
  type Subscription = { tenant: string; topics: Set<string>; listener: RealtimeListener }
  const subscriptions = new Set<Subscription>()
  const statusListeners = new Set<() => void>()
  let snapshot: RealtimeSnapshot = { status: "connecting" }
  let tenant = ""
  let cursor = ""
  let controller: AbortController | undefined
  let reconnect: ReturnType<typeof setTimeout> | undefined
  let hiddenTimer: ReturnType<typeof setTimeout> | undefined
  let generation = 0
  let failures: number[] = []
  let attempts = 0
  let suspended = false
  let pageHidden = false
  let closed = false
  let watching = false
  const online = () => typeof navigator === "undefined" || navigator.onLine !== false
  const update = (status: RealtimeStatus, eventAt?: number) => {
    if (snapshot.status === status && eventAt === undefined) return
    snapshot = { status, lastEventAt: eventAt ?? snapshot.lastEventAt }
    for (const listener of statusListeners) listener()
  }
  const current = () => [...subscriptions].filter((sub) => sub.tenant === tenant)
  const resync = () => { for (const sub of current()) sub.listener.onResync?.() }
  const stop = () => {
    generation++
    controller?.abort()
    controller = undefined
    clearTimeout(reconnect)
  }
  const schedule = (delay: number) => {
    clearTimeout(reconnect)
    reconnect = setTimeout(() => { void connect() }, delay)
  }
  const connect = async () => {
    stop()
    if (closed || suspended || !tenant) return
    const topics = [...new Set(current().flatMap((sub) => [...sub.topics]))].sort()
    if (!topics.length) return
    if (!online()) { update("offline"); return }
    const mine = generation
    controller = new AbortController()
    const signal = controller.signal
    if (snapshot.status !== "polling") update("connecting")
    let serverRetry: number | undefined
    let endedForReauth = false
    try {
      const request = opts.request()
      const headers = new Headers(request.headers)
      headers.set("Accept", "text/event-stream")
      if (cursor) headers.set("Last-Event-ID", cursor)
      const response = await fetch(opts.url(tenant, topics), { ...request, method: "GET", headers, signal })
      if (mine !== generation) { await response.body?.cancel(); return }
      if (response.status === 401) {
        await response.body?.cancel()
        opts.onUnauthorized?.()
        update("offline")
        return
      }
      if (!response.ok || !response.body || !response.headers.get("Content-Type")?.startsWith("text/event-stream")) {
        throw new Error("Event stream unavailable")
      }
      await readEventStream(response.body.getReader(), (frame) => {
        if (mine !== generation) return
        if (frame.retry !== undefined) serverRetry = Math.min(30_000, Math.max(1000, frame.retry))
        if (frame.id && /^\d+$/.test(frame.id)) cursor = frame.id
        if (frame.event === "reconnect") return
        if (frame.event === "reauth_required") {
          endedForReauth = true
          opts.onUnauthorized?.()
          update("offline")
          controller?.abort()
          return
        }
        let body: Record<string, unknown>
        try { body = JSON.parse(frame.data) as Record<string, unknown> } catch { return }
        if (!body || typeof body !== "object") return
        if (frame.event === "ready") {
          const wasPolling = snapshot.status === "polling"
          const denied = Array.isArray(body.denied) && body.denied.length > 0
          update(denied ? "polling" : "live")
          attempts = 0
          if (wasPolling && !denied) resync()
          return
        }
        if (frame.event === "resync_required" || frame.event === "topic_revoked") {
          if (frame.event === "topic_revoked") update("polling")
          resync()
          return
        }
        if (typeof body.type !== "string" || !Array.isArray(body.topics)) return
        const event: RealtimeEvent = {
          id: frame.id ?? cursor,
          type: body.type,
          topics: body.topics.filter((topic): topic is string => typeof topic === "string"),
          occurredAt: typeof body.occurred_at === "string" ? body.occurred_at : "",
          ...("data" in body ? { payload: body.data } : {}),
        }
        update(snapshot.status, Date.now())
        for (const sub of current()) {
          if (event.topics.some((topic) => sub.topics.has(topic))) sub.listener.onEvent?.(event)
        }
      })
      if (signal.aborted || mine !== generation || endedForReauth) return
      if (serverRetry !== undefined) { schedule(serverRetry); return }
      throw new Error("Event stream disconnected")
    } catch {
      if (signal.aborted || mine !== generation || endedForReauth) return
      if (!online()) { update("offline"); return }
      const now = Date.now()
      failures = [...failures.filter((at) => now - at < 60_000), now]
      const polling = failures.length >= 3 || snapshot.status === "polling"
      update(polling ? "polling" : "connecting")
      const delay = polling ? 30_000 : Math.min(30_000, 1000 * 2 ** attempts++) * (0.8 + Math.random() * 0.4)
      schedule(delay)
    }
  }
  const network = () => {
    if (!online()) { stop(); update("offline") }
    else { schedule(0) }
  }
  const visibility = () => {
    clearTimeout(hiddenTimer)
    if (pageHidden) return
    if (document.hidden) {
      hiddenTimer = setTimeout(() => { suspended = true; stop() }, 5 * 60_000)
    } else if (suspended) {
      suspended = false
      resync()
      schedule(0)
    }
  }
  const pagehide = () => {
    pageHidden = true
    suspended = true
    clearTimeout(hiddenTimer)
    stop()
  }
  const pageshow = () => {
    if (!pageHidden) return
    pageHidden = false
    suspended = false
    resync()
    schedule(0)
  }
  const watch = () => {
    if (watching) return
    watching = true
    if (typeof window !== "undefined") {
      window.addEventListener("online", network); window.addEventListener("offline", network)
      window.addEventListener("pagehide", pagehide); window.addEventListener("unload", pagehide)
      window.addEventListener("pageshow", pageshow)
    }
    if (typeof document !== "undefined") document.addEventListener("visibilitychange", visibility)
  }
  const unwatch = () => {
    watching = false
    clearTimeout(hiddenTimer)
    if (typeof window !== "undefined") {
      window.removeEventListener("online", network); window.removeEventListener("offline", network)
      window.removeEventListener("pagehide", pagehide); window.removeEventListener("unload", pagehide)
      window.removeEventListener("pageshow", pageshow)
    }
    if (typeof document !== "undefined") document.removeEventListener("visibilitychange", visibility)
  }
  return {
    subscribe(nextTenant, topics, listener) {
      closed = false
      watch()
      const sub = { tenant: nextTenant, topics: new Set(topics), listener }
      subscriptions.add(sub)
      if (tenant !== nextTenant) { stop(); tenant = nextTenant; cursor = ""; failures = []; attempts = 0 }
      schedule(300)
      return () => {
        subscriptions.delete(sub)
        if (!subscriptions.size) { stop(); unwatch() }
        else schedule(300)
      }
    },
    subscribeStatus(listener) { statusListeners.add(listener); return () => { statusListeners.delete(listener) } },
    getSnapshot: () => snapshot,
    close() {
      closed = true
      stop()
      clearTimeout(hiddenTimer)
      subscriptions.clear()
      statusListeners.clear()
      unwatch()
    },
  }
}
