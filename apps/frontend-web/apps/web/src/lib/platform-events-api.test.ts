import { afterEach, expect, it, spyOn } from "bun:test"
import { iamApi } from "./iam-api"
import {
  platformEventsApi,
  replayRange,
  type ParkedEvent,
} from "./platform-events-api"

let restore: (() => void) | undefined
afterEach(() => {
  restore?.()
  restore = undefined
})
it("rejects ambiguous, inverted and unsafe replay boundaries", () => {
  expect(
    replayRange({ from_seq: "1", to_seq: "4", ignore_receipts: true })
  ).toEqual({ from_seq: 1, to_seq: 4, ignore_receipts: true })
  expect(replayRange({ from_seq: "1" })).toEqual({
    from_seq: 1,
    ignore_receipts: false,
  })
  for (const values of [
    { from_seq: "" },
    { from_seq: "0" },
    { from_seq: "1.1" },
    { from_seq: "1e3" },
    { from_seq: "2", to_seq: "1" },
    { from_seq: "9007199254740993" },
    { from_seq: "1", to_seq: "9007199254740993" },
  ])
    expect(replayRange(values)).toBeNull()
})
it("uses exact CAS, proof purpose target path and a stable replay key", async () => {
  const mock = spyOn(iamApi, "deliveryResultRequest").mockResolvedValue(
    {} as never
  )
  restore = () => mock.mockRestore()
  const parked = { id: "123", version: 7 } as ParkedEvent
  await platformEventsApi.mutateParked(parked, true, "Reviewed")
  expect(mock).toHaveBeenLastCalledWith(
    { method: "POST", url: "/platform/parked-events/123/attempts" },
    {
      headers: { "If-Match": "7" },
      body: JSON.stringify({ reason: "Reviewed" }),
    }
  )
  await platformEventsApi.mutateParked(parked, false, "No longer relevant")
  expect(mock).toHaveBeenLastCalledWith(
    { method: "PATCH", url: "/platform/parked-events/123" },
    {
      headers: { "If-Match": "7" },
      body: JSON.stringify({
        reason: "No longer relevant",
        state: "DISCARDED",
      }),
    }
  )
  const range = { from_seq: 4, to_seq: 6, ignore_receipts: false }
  await platformEventsApi.replay(
    "notification.projector",
    range,
    "fresh-proof",
    "operation-key"
  )
  expect(mock).toHaveBeenLastCalledWith(
    {
      method: "POST",
      url: "/platform/domain-event-subscribers/notification.projector/replays",
    },
    {
      headers: { "Idempotency-Key": "operation-key" },
      body: JSON.stringify({ ...range, reauth_proof: "fresh-proof" }),
    }
  )
  expect(() => platformEventsApi.event(null)).toThrow()
  expect(() => platformEventsApi.event("../123")).toThrow()
})
