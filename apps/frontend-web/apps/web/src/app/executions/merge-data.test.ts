import { expect, it } from "bun:test"
import { createMergeApi, mergeLabels } from "./merge-data"
import type { iamApi } from "../../lib/iam-api"

it("preserves exact IDs, abort, merge inputs and retry identity", async () => {
  const calls: unknown[][] = []
  const api = createMergeApi({deliveryResultRequest: async (...args: unknown[]) => {calls.push(args); return []}} as Pick<typeof iamApi, "deliveryResultRequest">)
  const controller = new AbortController(), id = "9007199254740993"
  await api.list(id, controller.signal)
  expect(calls[0]).toEqual([{method: "GET", url: `/executions/${id}/merges`}, {signal: controller.signal}])
  const body = {target_branch: "release/stable", push: false, reason: "Integrate accepted evidence"}
  await api.request(id, body, "exact-request")
  await api.request(id, body, "exact-request")
  expect(calls[1]).toEqual(calls[2])
  expect(calls[1]).toEqual([{method: "POST", url: `/executions/${id}/merges`}, {headers: {"Idempotency-Key": "exact-request"}, body: JSON.stringify(body)}])
  await api.request(id, {push: true, reason: "Use configured target"}, "other-request")
  expect(JSON.parse((calls[3]![1] as {body: string}).body)).toEqual({push: true, reason: "Use configured target"})
  for (const invalid of ["../secret", "1e3", "0", "9007199254740993/merges"]) {
    expect(() => api.list(invalid)).toThrow()
    expect(() => api.request(invalid, body, "exact-request")).toThrow()
  }
  expect(calls).toHaveLength(4)
})

it("provides complete merge and outcome disclosure in three locales", () => {
  for (const labels of Object.values(mergeLabels)) {
    expect(Object.keys(labels).sort()).toEqual(Object.keys(mergeLabels["en-US"]).sort())
    expect(Object.keys(labels.states).sort()).toEqual(["CONFLICT", "FAILED", "LEASED", "MERGED", "PENDING"])
    for (const value of [...Object.values(labels).filter(value => typeof value === "string"), ...Object.values(labels.states)]) {
      expect(value.length).toBeGreaterThan(0)
      expect(value).not.toContain("\uFFFD")
    }
  }
})
