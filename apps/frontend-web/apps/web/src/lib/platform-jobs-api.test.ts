import { afterEach, describe, expect, it, spyOn } from "bun:test"
import { iamApi } from "./iam-api"
import {
  jobCanCancel,
  jobCanRetry,
  jobFailures,
  platformJobsApi,
  type PlatformJob,
  type PlatformSchedule,
} from "./platform-jobs-api"

let restore: (() => void) | undefined
afterEach(() => {
  restore?.()
  restore = undefined
})

describe("platform job command contract", () => {
  it("queries only the exact recorded correlation with bounded pagination", async () => {
    const calls: unknown[][] = []
    const mock = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (...args) => { calls.push(args); return [] as never })
    restore = () => mock.mockRestore()
    await platformJobsApi.operations("recorded-trace", 50)
    expect(calls).toEqual([[{method: "GET", url: "/system/operation-logs?correlation_id=recorded-trace&offset=50&limit=50"}]])
    expect(() => platformJobsApi.operations("%" )).toThrow()
    expect(calls).toHaveLength(1)
  })
  it("shows only the newest five valid recorded failures in reverse chronological order", () => {
    const history = Array.from({ length: 7 }, (_, index) => ({
      attempt: index + 1,
      at: `2026-10-20T00:00:0${index}Z`,
      code: "TIMEOUT",
      message: "Execution timed out.",
    }))
    expect(jobFailures(history).map((failure) => failure.attempt)).toEqual([
      7, 6, 5, 4, 3,
    ])
    expect(history[0].attempt).toBe(1)
    expect(
      jobFailures([null, {}, { ...history[0], code: 1 }, history[0]])
    ).toEqual([history[0]])
    expect(jobFailures(undefined)).toEqual([])
  })
  it("sends current CAS versions, bounded filters and the retained schedule replay key", async () => {
    const calls: unknown[][] = []
    const mock = spyOn(iamApi, "deliveryResultRequest").mockImplementation(
      async (...args) => {
        calls.push(args)
        return {} as never
      }
    )
    restore = () => mock.mockRestore()
    const job = { id: "123", version: 7 } as PlatformJob
    const schedule = { id: "456", version: 9 } as PlatformSchedule
    await platformJobsApi.list(
      { state: "DEAD", tenant_id: "eq:42", queue: "" },
      50
    )
    await platformJobsApi.retry(job, "Reviewed failure")
    await platformJobsApi.cancel(job, "No longer needed")
    await platformJobsApi.enable(schedule, false)
    await platformJobsApi.run(schedule, "retained-key")
    await platformJobsApi.run(schedule, "retained-key")
    const url = new URL(
      (calls[0]![0] as { url: string }).url,
      "https://api.invalid"
    )
    expect(Object.fromEntries(url.searchParams)).toEqual({
      state: "DEAD",
      tenant_id: "eq:42",
      offset: "50",
      limit: "50",
    })
    expect(calls[1]).toEqual([
      { method: "POST", url: "/platform/jobs/123/attempts" },
      {
        headers: { "If-Match": "7" },
        body: JSON.stringify({ reason: "Reviewed failure" }),
      },
    ])
    expect(calls[2]).toEqual([
      { method: "PATCH", url: "/platform/jobs/123" },
      {
        headers: { "If-Match": "7" },
        body: JSON.stringify({
          state: "CANCELLED",
          reason: "No longer needed",
        }),
      },
    ])
    expect(calls[3]).toEqual([
      { method: "PATCH", url: "/platform/job-schedules/456" },
      {
        headers: { "If-Match": "9" },
        body: JSON.stringify({ enabled: false }),
      },
    ])
    expect(calls[4]).toEqual(calls[5])
    expect(calls[4]).toEqual([
      { method: "POST", url: "/platform/job-schedules/456/runs" },
      { headers: { "Idempotency-Key": "retained-key" } },
    ])
  })
  it("never offers retry without a handler or cancellation of running/finished work", () => {
    for (const state of [
      "PENDING",
      "RUNNING",
      "SUCCEEDED",
      "DEAD",
      "CANCELLED",
    ]) {
      for (const has_handler of [false, true]) {
        const job = { state, has_handler } as PlatformJob
        expect(jobCanRetry(job)).toBe(state === "DEAD" && has_handler)
        expect(jobCanCancel(job)).toBe(state === "PENDING" || state === "DEAD")
      }
    }
    expect(() => platformJobsApi.get(null)).toThrow(
      "Invalid resource identifier"
    )
    expect(() => platformJobsApi.get("../other")).toThrow(
      "Invalid resource identifier"
    )
  })
  it("sends bounded batch identities and preserves partial outcome details", async () => {
    const outcome = { retried: ["123"], skipped: [{ id: "124", code: 170409 }] }
    const mock = spyOn(iamApi, "deliveryResultRequest").mockResolvedValue(
      outcome as never
    )
    restore = () => mock.mockRestore()
    expect(
      await platformJobsApi.retryBatch(["123", "124"], "Reviewed")
    ).toEqual(outcome)
    expect(mock).toHaveBeenCalledWith(
      { method: "POST", url: "/platform/job-attempts" },
      { body: JSON.stringify({ job_ids: ["123", "124"], reason: "Reviewed" }) }
    )
    expect(() => platformJobsApi.retryBatch([], "Reviewed")).toThrow()
    expect(() =>
      platformJobsApi.retryBatch(Array(101).fill("123"), "Reviewed")
    ).toThrow()
    expect(() => platformJobsApi.retryBatch(["0"], "Reviewed")).toThrow()
    expect(mock).toHaveBeenCalledTimes(1)
  })
})
