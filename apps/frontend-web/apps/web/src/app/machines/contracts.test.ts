import { describe, expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { entityNavigation, workspaceArea, workspaceScopePath } from "../../lib/navigation"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"

describe("machine and execution contracts", () => {
  it("sends explicit tenant/project cursors and versioned command intent", async () => {
    const calls: {path: string; method: string; version: string | null; body: unknown}[] = []
    const server = Bun.serve({port: 0, async fetch(request) {
      const url = new URL(request.url)
      const rawBody = request.method === "GET" ? "" : await request.text()
      calls.push({path: url.pathname + url.search, method: request.method, version: request.headers.get("If-Match"), body: rawBody ? JSON.parse(rawBody) : null})
      return Response.json({code: 0, data: {items: [], has_more: false}})
    }})
    try {
      const api = createIamApi(`http://127.0.0.1:${server.port}`, () => "wrong-tenant")
      await api.machines("7", "100")
      await api.executions("7", "20", "101")
      await api.drainMachine("11", 3, true)
      await api.cancelExecution("21", 5)
      await api.recoverExecution("21", 6, true)
      await api.registerMachineWorkdir("11", {project_id: "20", repository_id: "30", path: "C:/repo"})
      await api.machineCredentials("11")
      await api.machineInspections("11", "99")
      await api.requestMachineInspection("11", "RUNTIMES")
      await api.rotateMachineCredential("11")
      expect(calls).toEqual([
        {path: "/tenants/7/machines?cursor=100&limit=30", method: "GET", version: null, body: null},
        {path: "/tenants/7/executions?project_id=20&cursor=101&limit=30", method: "GET", version: null, body: null},
        {path: "/machines/11/drain", method: "PUT", version: "3", body: {drain: true}},
        {path: "/executions/21/cancellation", method: "PUT", version: "5", body: {acknowledge_unknown_result: false}},
        {path: "/executions/21/recovery", method: "PUT", version: "6", body: {acknowledge_unknown_result: true}},
        {path: "/machines/11/workdirs", method: "POST", version: null, body: {project_id: "20", repository_id: "30", path: "C:/repo"}},
        {path: "/machines/11/credentials", method: "GET", version: null, body: null},
        {path: "/machines/11/inspections?cursor=99&limit=30", method: "GET", version: null, body: null},
        {path: "/machines/11/inspections", method: "POST", version: null, body: {kind: "RUNTIMES"}},
        {path: "/machines/11/credential-rotations", method: "POST", version: null, body: null},
      ])
    } finally {await server.stop(true)}
  })
  it("never carries a machine/execution object into another scope", () => {
    expect(workspaceArea("/machines/11")).toBe("team")
    expect(workspaceArea("/executions/21")).toBe("collaboration")
    expect(workspaceScopePath("/admin/7/machines/11", "8", "", ["machine_read"])).toBe("/admin/8/machines")
    expect(workspaceScopePath("/admin/7/machines/11", "8", "20", ["machine_read"], "project")).toBeNull()
    expect(workspaceScopePath("/admin/7/entities/20/executions/21", "8", "30", ["execution_read"], "project")).toBe("/admin/8/entities/30/executions")
    expect(workspaceScopePath("/admin/7/entities/20/executions/21", "8", "30", [], "project")).toBeNull()
    expect(entityNavigation(["execution_read"], "project").some((item) => item.path === "/executions")).toBe(true)
    expect(entityNavigation(["execution_read"], "client").some((item) => item.path === "/executions")).toBe(false)
  })
  it("has non-placeholder translations for every fleet/execution message", () => {
    const keys = Object.keys(en).filter((key) => /^(fleet|execution)/.test(key))
    expect(keys.length).toBeGreaterThan(50)
    for (const key of keys) {
      for (const messages of [en, zh, ms]) {
        const value = (messages as Record<string, string>)[key]
        expect(typeof value).toBe("string")
        expect(value?.trim().length).toBeGreaterThan(0)
        expect(value).not.toContain("??")
      }
    }
    expect(zh.fleetRemoteUnavailable).toMatch(/[\u4e00-\u9fff]/)
  })
})
