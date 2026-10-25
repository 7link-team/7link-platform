import { expect, test } from "bun:test"
import { archiveResourceHref, createProjectArchiveApi, type ArchiveResource } from "./archive-api"

test("archive commands pin the observed version and receipt hash and preserve retry identity", async () => {
  const calls: unknown[][] = []
  const api = createProjectArchiveApi({deliveryResultRequest: async (...args: unknown[]) => {calls.push(args); return {} as never}})
  const hash = "a".repeat(64)
  await api.change("9007199254740993", 7, false, "Preserve actual project history", "stable-attempt", hash)
  await api.change("9007199254740993", 7, false, "Preserve actual project history", "stable-attempt", hash)
  expect(calls[0]).toEqual(calls[1])
  const [route, options] = calls[0] as [{url: string}, {headers: Record<string, string>; body: string}]
  expect(route.url).toBe("/entities/9007199254740993/project/archive")
  expect(options.headers).toEqual({"If-Match": '"7"', "Idempotency-Key": "stable-attempt"})
  expect(JSON.parse(options.body).preflight_hash).toBe(hash)
  await api.change("3", 8, true, "Explicit restore without restarting", "restore-attempt")
  expect((calls[2]![0] as {url: string}).url).toEndWith("/restore")
  expect(JSON.parse((calls[2]![1] as {body: string}).body)).toEqual({reason: "Explicit restore without restarting"})
  for (const [version, reason, key, receipt] of [[0, "Preserve history", "key", hash], [1, "short", "key", hash], [1, "Preserve history", "", hash], [1, "Preserve history", "key", "stale"]] as const) {
    expect(() => api.change("3", version, false, reason, key, receipt)).toThrow()
  }
  expect(calls).toHaveLength(3)
  const controller = new AbortController()
  await api.preflight("3", controller.signal)
  expect(calls[3]![1]).toEqual({signal: controller.signal})
})

test("resource disposition goes to existing owner surfaces without claiming a receipt", () => {
  const resource: ArchiveResource = {kind: "WORK_ITEM", id: "9007199254740993", state: "BLOCKED", version: 4, ready: false}
  expect(archiveResourceHref("1", "3", resource)).toBe("/admin/1/entities/3/work-items?work=9007199254740993")
  expect(archiveResourceHref("1", "3", {...resource, kind: "MEMORY_APPROVAL"})).toEndWith("/governance/memory-review")
  expect(archiveResourceHref("1", "3", {...resource, kind: "UNKNOWN_OWNER"})).toBeNull()
  expect(() => archiveResourceHref("1", "../3", resource)).toThrow()
})
