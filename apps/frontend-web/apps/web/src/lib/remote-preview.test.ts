import { describe, expect, it } from "bun:test"
import { createIamApi } from "./iam-api"
import { loginReturnPath } from "./auth-entrance"
import { previewAccessURL, previewLabel, previewPort } from "./remote-preview"
import en from "../messages/en-US.json"
import zh from "../messages/zh-CN.json"
import ms from "../messages/ms-MY.json"

describe("isolated service previews", () => {
  it("accepts only a separate HTTPS gateway URL and never console or credential URLs", () => {
    const consoleOrigin = "https://console.example.test"
    expect(previewAccessURL("https://abc.preview.example.test/__access?code=one-time", consoleOrigin)).toBe("https://abc.preview.example.test/__access?code=one-time")
    expect(previewAccessURL("https://preview.example.test/p/abc/__access?code=x", consoleOrigin)).toBe("https://preview.example.test/p/abc/__access?code=x")
    for (const value of ["/api/app", "//other.test/x", "http://other.test/x", "javascript:alert(1)", "data:text/html,app", "https://console.example.test/app", "https://user:password@other.test/x"]) {
      expect(() => previewAccessURL(value, consoleOrigin)).toThrow()
    }
  })
  it("validates gateway labels and numeric port bounds", () => {
    const label = "abcdefghijklmnopqrstuv2345"
    expect(previewLabel(`?label=${label}`)).toBe(label)
    for (const query of ["", "?label=../admin", `?label=${label}&label=${label}`, "?label=ABCDEFGHIJKLMNOPQRSTUV2345", "?label=<script>"]) expect(previewLabel(query)).toBeNull()
    for (const port of ["1", "65535", "8080"]) expect(previewPort(port)).toBe(Number(port))
    for (const port of ["", "0", "65536", "-1", "80.5", "1e3", " 80", "80 ", "999999"]) expect(previewPort(port)).toBeNull()
  })
  it("resumes only the exact local preview exchange after login", () => {
    expect(loginReturnPath("tenant", "/preview-access?label=abc")).toBe("/preview-access?label=abc")
    expect(loginReturnPath("user", "/preview-access")).toBe("/preview-access")
    for (const path of ["https://other.test/preview-access", "//other.test/preview-access", "/preview-access-other?label=x", "/preview-access/../system"]) expect(loginReturnPath("tenant", path)).toBe("/admin/workspaces")
  })
  it("uses authenticated domain commands with exact execution/port and preserves terminal policy", async () => {
    const calls: {path: string; method: string; version: string | null; body: unknown}[] = []
    const server = Bun.serve({port: 0, async fetch(request) {
      const raw = await request.text()
      calls.push({path: new URL(request.url).pathname, method: request.method, version: request.headers.get("If-Match"), body: raw ? JSON.parse(raw) : null})
      if (new URL(request.url).pathname === "/preview-sessions/denied/termination") return Response.json({message: "forbidden"}, {status: 403})
      return Response.json({code: 0, data: []}, {status: new URL(request.url).pathname.includes("access-grants") ? 201 : 200})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`, () => "wrong-tenant")
      await api.setMachinePreviewAccess("11", 9, true, false)
      await api.setMachinePreviewAccess("11", 10, false, true)
      await api.previewSessions("21")
      await api.createPreviewSession("21", 8080)
      await api.previewSessionAccess("31")
      await api.stopPreviewSession("31")
      await api.previewLabelAccess("abcdefghijklmnopqrstuv2345")
      expect(calls).toEqual([
        {path: "/machines/11/remote-access", method: "PUT", version: "9", body: {allow_remote_terminal: true, allow_remote_preview: false}},
        {path: "/machines/11/remote-access", method: "PUT", version: "10", body: {allow_remote_terminal: false, allow_remote_preview: true}},
        {path: "/executions/21/preview-sessions", method: "GET", version: null, body: null},
        {path: "/executions/21/preview-sessions", method: "POST", version: null, body: {kind: "SERVICE", port: 8080}},
        {path: "/preview-sessions/31/access-grants", method: "POST", version: null, body: {return_path: "/"}},
        {path: "/preview-sessions/31/termination", method: "PUT", version: null, body: {}},
        {path: "/preview-access-grants", method: "POST", version: null, body: {label: "abcdefghijklmnopqrstuv2345", return_path: "/"}},
      ])
      await expect(api.stopPreviewSession("denied")).rejects.toMatchObject({status: 403})
      expect(calls.length).toBe(8)
    } finally {await server.stop(true)}
  })
  it("provides readable preview messages for every supported locale", () => {
    const keys = Object.keys(en).filter((key) => key.startsWith("remotePreview"))
    expect(keys.length).toBeGreaterThan(15)
    for (const key of keys) for (const messages of [en, zh, ms]) {
      const value = (messages as Record<string, string>)[key]
      expect(value?.trim().length).toBeGreaterThan(0)
      expect(value).not.toContain("??")
      if (messages === zh) expect(value).toMatch(/[\u4e00-\u9fff]/u)
    }
  })
  it("normalizes an empty nullable list and surfaces denied grants without retry", async () => {
    let requests = 0
    const server = Bun.serve({port: 0, fetch(request) {
      requests++
      return new URL(request.url).pathname.includes("access-grants")
        ? Response.json({message: "denied"}, {status: 403})
        : Response.json({code: 0, data: null})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`)
      expect(await api.previewSessions("21")).toEqual([])
      await expect(api.previewSessionAccess("31")).rejects.toMatchObject({status: 403})
      expect(requests).toBe(2)
    } finally {await server.stop(true)}
  })
})
