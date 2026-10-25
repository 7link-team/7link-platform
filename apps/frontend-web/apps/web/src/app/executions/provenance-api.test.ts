import {expect, test} from "bun:test"
import {createProvenanceApi} from "./provenance-api"
test("provenance request preserves big IDs, cursor, comparison and abort; invalid IDs never issue requests", async () => {
  const calls: unknown[][] = []
  const api = createProvenanceApi({deliveryResultRequest: async (...args: unknown[]) => {calls.push(args); return {} as never}})
  const signal = new AbortController().signal
  await api.list("9007199254740993", "9007199254740995", "9007199254740997", signal)
  expect((calls[0]![0] as {url: string}).url).toBe("/executions/9007199254740993/evidence-provenance?limit=50&sort=-id&cursor=9007199254740995&compare_execution_id=9007199254740997")
  expect((calls[0]![1] as {signal: AbortSignal}).signal).toBe(signal)
  expect(() => api.list("0")).toThrow(); expect(() => api.list("1", "bad")).toThrow(); expect(() => api.list("1", "", "bad")).toThrow(); expect(calls).toHaveLength(1)
})
