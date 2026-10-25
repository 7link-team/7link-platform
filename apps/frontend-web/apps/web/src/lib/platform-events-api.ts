import type { components } from "./api-schema"
import { iamApi } from "./iam-api"

export type EventSubscriber = components["schemas"]["AdminSubscriberView"]
export type DomainEvent = components["schemas"]["AdminEventView"]
export type ParkedEvent = components["schemas"]["AdminParkedView"]
export type EventFilters = Partial<
  Record<
    | "event_type"
    | "tenant_id"
    | "subject_kind"
    | "subject_id"
    | "stream_seq"
    | "occurred_at",
    string
  >
>
export type ReplayRange = {
  from_seq: number
  to_seq?: number
  ignore_receipts: boolean
}
const subscriberPath = (name: string) =>
  `/platform/domain-event-subscribers/${encodeURIComponent(name)}`
function eventID(id: string | null) {
  if (!id || !/^[1-9][0-9]*$/.test(id))
    throw new Error("Invalid resource identifier")
  return id
}
export function replayRange(
  values: Record<string, unknown>
): ReplayRange | null {
  const from = String(values.from_seq ?? "")
  const to = String(values.to_seq ?? "")
  if (!/^[1-9][0-9]*$/.test(from) || (to && !/^[1-9][0-9]*$/.test(to)))
    return null
  const start = Number(from),
    end = to ? Number(to) : undefined
  if (
    !Number.isSafeInteger(start) ||
    (end !== undefined && (!Number.isSafeInteger(end) || end < start))
  )
    return null
  return {
    from_seq: start,
    ...(end === undefined ? {} : { to_seq: end }),
    ignore_receipts: values.ignore_receipts === true,
  }
}
export const platformEventsApi = {
  subscribers: () =>
    iamApi.deliveryResultRequest<EventSubscriber[]>({
      method: "GET",
      url: "/platform/domain-event-subscribers",
    }),
  events: (filters: EventFilters, offset: number) =>
    iamApi.deliveryResultPage<DomainEvent>({
      method: "GET",
      url: `/platform/domain-events?${new URLSearchParams({ ...Object.fromEntries(Object.entries(filters).filter(([, value]) => value)), offset: String(offset), limit: "50" })}`,
    }),
  event: (id: string | null) =>
    iamApi.deliveryResultRequest<DomainEvent>({
      method: "GET",
      url: `/platform/domain-events/${eventID(id)}`,
    }),
  parked: (name: string, offset: number, filters: Record<string, string>) =>
    iamApi.deliveryResultPage<ParkedEvent>({
      method: "GET",
      url: `${subscriberPath(name)}/parked-events?${new URLSearchParams({ ...Object.fromEntries(Object.entries(filters).filter(([, value]) => value)), offset: String(offset), limit: "50" })}`,
    }),
  pause: (subscriber: EventSubscriber, paused: boolean) =>
    iamApi.deliveryResultRequest<EventSubscriber>(
      { method: "PATCH", url: subscriberPath(subscriber.name) },
      { body: JSON.stringify({ paused }) }
    ),
  mutateParked: (row: ParkedEvent, retry: boolean, reason: string) =>
    iamApi.deliveryResultRequest<ParkedEvent>(
      {
        method: retry ? "POST" : "PATCH",
        url: `/platform/parked-events/${eventID(row.id)}${retry ? "/attempts" : ""}`,
      },
      {
        headers: { "If-Match": String(row.version) },
        body: JSON.stringify({
          reason,
          ...(retry ? {} : { state: "DISCARDED" }),
        }),
      }
    ),
  replay: (
    subscriber: string,
    range: ReplayRange,
    proof: string,
    key: string
  ) =>
    iamApi.deliveryResultRequest<components["schemas"]["AdminReplayView"]>(
      { method: "POST", url: `${subscriberPath(subscriber)}/replays` },
      {
        headers: { "Idempotency-Key": key },
        body: JSON.stringify({ ...range, reauth_proof: proof }),
      }
    ),
}
