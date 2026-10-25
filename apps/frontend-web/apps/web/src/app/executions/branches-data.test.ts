import { expect, it } from "bun:test"
import { createBranchApi, branchMessages } from "./branches-data"
import type { iamApi } from "../../lib/iam-api"
it("uses exact large IDs, bounded cursor, abort, CAS and stable request identity", async () => {
  const calls: unknown[][] = []
  const api = createBranchApi({deliveryResultRequest: async (...args: unknown[]) => {calls.push(args); return {items: [], has_more: false}}} as Pick<typeof iamApi, "deliveryResultRequest">)
  const controller = new AbortController(), id = "9007199254740993"
  await api.list(id, "9007199254740994", controller.signal)
  expect(calls[0]).toEqual([{method: "GET", url: `/executions/${id}/branches?limit=50&cursor=9007199254740994`}, {signal: controller.signal}])
  const input = {reason: "Try another strategy", objective: "Use the root baseline", machine_id: "9007199254740995"}
  await api.create(id, 7, input, "same-request"); await api.create(id, 7, input, "same-request")
  expect(calls[1]).toEqual(calls[2]); expect(calls[1]).toEqual([{method: "POST", url: `/executions/${id}/branches`}, {headers: {"If-Match": '"7"', "Idempotency-Key": "same-request"}, body: JSON.stringify(input)}])
  expect(() => api.list("../secret")).toThrow(); expect(() => api.list(id, "1e3")).toThrow()
})
it("provides complete disclosure in three languages", () => {
  for (const messages of Object.values(branchMessages)) {
    expect(Object.keys(messages).sort()).toEqual(Object.keys(branchMessages["en-US"]).sort())
    for (const value of Object.values(messages)) {expect(value.length).toBeGreaterThan(0); expect(value).not.toContain("\uFFFD")}
  }
})
