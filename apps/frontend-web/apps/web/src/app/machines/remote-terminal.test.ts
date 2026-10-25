import { describe, expect, it } from "bun:test"
import { createIamApi, type RemoteTerminalFrame } from "../../lib/iam-api"
import { encodeTerminalLine, terminalPlainText } from "./remote-terminal-codec"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"

describe("remote terminal", () => {
  it("reuses the intent key across renewed proofs and reads lifecycle separately from cached create receipt", async () => {
    const calls: {method: string; key: string | null; body: unknown}[] = []
    const server = Bun.serve({port: 0, async fetch(request) {
      const raw = await request.text()
      calls.push({method: request.method, key: request.headers.get("Idempotency-Key"), body: raw ? JSON.parse(raw) : null})
      return Response.json({code: 0, data: {id: "31", state: request.method === "POST" ? "OPENING" : "CLOSED"}}, {status: request.method === "POST" ? 201 : 200})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`)
      const intent = crypto.randomUUID()
      expect(intent.length).toBeLessThanOrEqual(64)
      expect(intent).toMatch(/^[\x21-\x7e]+$/u)
      expect((await api.createTerminalSession("11", "21", "first-proof", intent)).state).toBe("OPENING")
      expect((await api.createTerminalSession("11", "21", "renewed-proof", intent)).state).toBe("OPENING")
      expect((await api.terminalSession("31")).state).toBe("CLOSED")
      expect(calls).toEqual([
        {method: "POST", key: intent, body: {execution_id: "21", cols: 80, rows: 24, reauth_proof: "first-proof"}},
        {method: "POST", key: intent, body: {execution_id: "21", cols: 80, rows: 24, reauth_proof: "renewed-proof"}},
        {method: "GET", key: null, body: null},
      ])
    } finally {await server.stop(true)}
  })
  it("marks a valid output channel connected without requiring shell output", async () => {
    const original = globalThis.fetch
    let connected = 0
    globalThis.fetch = (async () => new Response(JSON.stringify({kind: "ping"}) + "\n", {headers: {"Content-Type": "application/x-ndjson"}})) as unknown as typeof fetch
    try {
      await createIamApi("http://localhost").streamTerminalSession("31", () => {}, new AbortController().signal, () => connected++)
      expect(connected).toBe(1)
      globalThis.fetch = (async () => Response.json({message: "denied"}, {status: 403})) as unknown as typeof fetch
      await expect(createIamApi("http://localhost").streamTerminalSession("31", () => {}, new AbortController().signal, () => connected++)).rejects.toMatchObject({status: 403})
      expect(connected).toBe(1)
    } finally {globalThis.fetch = original}
  })
  it("encodes UTF-8 commands and the platform Enter key without double submission", () => {
    const decode = (value: string) => new TextDecoder().decode(Uint8Array.from(atob(value), (char) => char.charCodeAt(0)))
    expect(decode(encodeTerminalLine("echo 你好", "WINDOWS"))).toBe("echo 你好\r")
    expect(decode(encodeTerminalLine("pwd", "LINUX"))).toBe("pwd\n")
    expect(decode(encodeTerminalLine("", "MACOS"))).toBe("\n")
  })
  it("strips terminal formatting and hyperlink controls while preserving inert HTML text", () => {
    expect(terminalPlainText("\x1b[31mred\x1b[0m\n\t<script>alert(1)</script>\x00\x07")).toBe("red\n\t<script>alert(1)</script>")
    expect(terminalPlainText("\x1b]8;;https://example.test\x07link\x1b]8;;\x1b\\")).toBe("link")
    expect(terminalPlainText("prefix\x1b]8;;incomplete")).toBe("prefix")
    expect(terminalPlainText("prefix\x1b[31")).toBe("prefix")
  })
  it("sends exact versioned policy and proof-bound execution commands, never a cwd", async () => {
    const calls: {path: string; method: string; version: string | null; body: unknown}[] = []
    const server = Bun.serve({port: 0, async fetch(request) {
      const raw = await request.text()
      calls.push({path: new URL(request.url).pathname, method: request.method, version: request.headers.get("If-Match"), body: raw ? JSON.parse(raw) : null})
      return Response.json({code: 0, data: {}})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`)
      await api.machineRemoteAccess("11")
      await api.setMachineRemoteAccess("11", 8, true, "enable-proof")
      await api.setMachineRemoteAccess("11", 9, false)
      await api.createTerminalSession("11", "21", "session-proof")
      await api.terminalInput("31", 1, "cHdkCg==")
      await api.revokeTerminalSession("31")
      expect(calls).toEqual([
        {path: "/machines/11/remote-access", method: "GET", version: null, body: null},
        {path: "/machines/11/remote-access", method: "PUT", version: "8", body: {allow_remote_terminal: true, reauth_proof: "enable-proof"}},
        {path: "/machines/11/remote-access", method: "PUT", version: "9", body: {allow_remote_terminal: false}},
        {path: "/machines/11/terminal-sessions", method: "POST", version: null, body: {execution_id: "21", cols: 80, rows: 24, reauth_proof: "session-proof"}},
        {path: "/terminal-sessions/31/input", method: "POST", version: null, body: {seq: 1, data: "cHdkCg=="}},
        {path: "/terminal-sessions/31/revoke", method: "POST", version: null, body: null},
      ])
    } finally {await server.stop(true)}
  })
  it("consumes split NDJSON frames and cancels immediately on closed", async () => {
    const frames: RemoteTerminalFrame[] = []
    const output: RemoteTerminalFrame = {kind: "output", session_id: "31", data: "cHdkCg==", seq: 1, sandboxed: false}
    // Match Go terminalFrame's zero-valued seq and omitempty data on CLOSED.
    const closed: RemoteTerminalFrame = {kind: "closed", session_id: "31", seq: 0, sandboxed: false}
    let cancelled = false
    const original = globalThis.fetch
    globalThis.fetch = (async (url, init) => {
      expect(String(url)).toEndWith("/terminal-sessions/31/stream")
      expect(String(url)).not.toContain("?")
      expect(new Headers(init?.headers).get("Accept")).toBe("application/x-ndjson")
      return new Response(new ReadableStream({start(controller) {
        const encoded = new TextEncoder().encode(JSON.stringify({kind: "ping", session_id: "", seq: 0, sandboxed: false}) + "\n" + JSON.stringify(output) + "\n" + JSON.stringify({kind: "ping"}) + "\n" + JSON.stringify(closed) + "\n" + JSON.stringify(output) + "\n")
        controller.enqueue(encoded.slice(0, 7)); controller.enqueue(encoded.slice(7))
      }, cancel() {cancelled = true}}), {headers: {"Content-Type": "application/x-ndjson"}})
    }) as typeof fetch
    try {
      await createIamApi("http://localhost").streamTerminalSession("31", (frame) => frames.push(frame), new AbortController().signal)
      expect(frames).toEqual([output, closed])
      expect(cancelled).toBe(true)
    } finally {globalThis.fetch = original}
  })
  it("rejects invalid output payloads, JSON shapes and oversized newline-terminated frames", async () => {
    const output = {kind: "output", session_id: "31", seq: 1, sandboxed: false}
    const invalid = [
      JSON.stringify(output),
      JSON.stringify({...output, data: "plain text"}),
      JSON.stringify({...output, data: "AB=="}), // Valid alphabet, noncanonical pad bits.
      JSON.stringify({...output, data: btoa("x".repeat(16385))}),
      JSON.stringify({...output, data: "eA==", reason: {html: "invalid"}}),
      JSON.stringify({...output, data: "eA==", seq: 0.5}),
      JSON.stringify({...output, data: "eA==", padding: "x".repeat(65536)}),
      "{", "null", "[]",
    ]
    const original = globalThis.fetch
    try {
      for (const line of invalid) {
        globalThis.fetch = (async () => new Response(line + "\n", {headers: {"Content-Type": "application/x-ndjson"}})) as unknown as typeof fetch
        await expect(createIamApi("http://localhost").streamTerminalSession("31", () => {throw new Error("invalid frame rendered")}, new AbortController().signal)).rejects.toMatchObject({code: "invalid_response"})
      }
    } finally {globalThis.fetch = original}
  })
  it("rejects cross-session frames and HTTP errors without auto replay", async () => {
    let attempts = 0
    const server = Bun.serve({port: 0, fetch(request) {
      attempts++
      if (new URL(request.url).pathname.includes("forbidden")) return Response.json({message: "forbidden"}, {status: 403})
      return new Response(JSON.stringify({kind: "output", session_id: "other", data: "", sandboxed: false}) + "\n", {headers: {"Content-Type": "application/x-ndjson"}})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`)
      await expect(api.streamTerminalSession("31", () => {throw new Error("must not render")}, new AbortController().signal)).rejects.toMatchObject({code: "invalid_response"})
      await expect(api.streamTerminalSession("forbidden", () => {}, new AbortController().signal)).rejects.toThrow()
      expect(attempts).toBe(2)
    } finally {await server.stop(true)}
  })
  it("provides all terminal messages and permanent filesystem warnings in every locale", () => {
    const keys = Object.keys(en).filter((key) => key.startsWith("terminal"))
    expect(keys.length).toBeGreaterThan(10)
    for (const messages of [en, zh, ms]) {
      expect(messages.terminalFilesystemWarning).toContain("sandbox:false")
      for (const key of keys) {
        const value = (messages as Record<string, string>)[key]
        expect(value?.trim().length).toBeGreaterThan(0)
        expect(value).not.toContain("??")
        if (messages === zh) expect(value).toMatch(/[\u4e00-\u9fff]/u)
      }
    }
  })
})
