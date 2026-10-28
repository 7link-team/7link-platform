// Explicit HTTP doubles: this fixture verifies UI interaction, not backend delivery.
import { createRoot } from "react-dom/client"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "@workspace/ui/i18n/messages/en-US.json"
import iam from "../../src/messages/en-US.json"
import PlatformDomainEventsPage from "../../src/app/system/domain-events"
import {
  platformEventsApi,
  type DomainEvent,
  type EventSubscriber,
  type ParkedEvent,
} from "../../src/lib/platform-events-api"
import { iamApi } from "../../src/lib/iam-api"
import "../../src/index.css"

const params = new URLSearchParams(location.search)
const calls: unknown[] = []
Object.assign(window, { platformEventCalls: calls })
const permissions = new Set(
  params.has("denied")
    ? []
    : [
        "system_event_view",
        ...(params.has("readonly") ? [] : ["system_event_manage"]),
      ]
)
const subscribers: EventSubscriber[] = [
  {
    name: "notification.projector",
    display_name: "Prepare business notifications",
    description: "Notifications: prepare messages from business events.",
    module: "notification",
    event_types: ["work_item.*"],
    mode: "Transactional",
    partitions: 1,
    cursors: [{ p: 0, last_seq: 5, lag: 2 }],
    parked: 1,
    blocked: 1,
    paused: false,
  },
]
const events = [
  {
    event_id: "123",
    event_type: "work_item.state_changed",
    display_name: "Work item state changed",
    description: "Project delivery: records a work item state change.",
    module: "delivery",
    stream_seq: 7,
    tenant_id: "42",
    schema_version: 1,
    subject: { kind: "work_item", id: "321" },
    subject_version: 2,
    actor: { kind: "HUMAN", id: "11", authority_source: "" },
    occurred_at: "2026-10-20T18:00:00Z",
    topics: ["tenant/42/work_item/321"],
    correlation_id: "fixture-trace",
  },
] as DomainEvent[]
const parked = [
  {
    id: "456",
    consumer: "notification.projector",
    partition_no: 0,
    event_id: "123",
    stream_seq: 7,
    tenant_id: "42",
    subject_kind: "work_item",
    subject_id: "321",
    reason: "FAILED",
    state: "PARKED",
    attempts: 5,
    last_error_code: "UNAVAILABLE",
    last_error: "Unavailable",
    resolution_note: "",
    created_at: "2026-10-20T18:00:00Z",
    updated_at: "2026-10-20T18:00:00Z",
    version: 3,
  },
] as ParkedEvent[]
platformEventsApi.subscribers = async () => subscribers
platformEventsApi.events = async () => ({ items: events, total: events.length })
platformEventsApi.event = async () => ({
  ...events[0]!,
  data: { state: "READY" },
})
platformEventsApi.parked = async () => ({ items: parked, total: parked.length })
platformEventsApi.pause = async (row, paused) => {
  calls.push({ pause: paused })
  subscribers[0] = { ...row, paused }
  return subscribers[0]
}
platformEventsApi.mutateParked = async (row, retry, reason) => {
  calls.push({ retry, reason, version: row.version })
  const next = {
    ...row,
    state: retry ? "PARKED" : "DISCARDED",
    version: row.version + 1,
  }
  parked[0] = next
  return next
}
platformEventsApi.replay = async (name, range, proof, key) => {
  calls.push({ name, range, proof, key })
  return {
    replay_cursor: "replay:fixture",
    from_seq: range.from_seq,
    to_seq: range.to_seq ?? 7,
  }
}
iamApi.capabilities = async () => ({ password_recovery: false }) as never
iamApi.reauthenticationMethods = async () => ({
  methods: ["password"],
  mfa_required: false,
})
iamApi.identityLinks = async () => []
iamApi.identityLinkProviders = async () => []
iamApi.createReauthenticationChallenge = async (body) => {
  calls.push({ challenge: body })
  return {
    proof: "fresh-proof",
    method: "password",
    expires_at: "2099-01-01T00:00:00Z",
  }
}
iamApi.verifyReauthentication = async () => ({ verified: true })
createRoot(document.getElementById("root")!).render(
  <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam }}>
    <MemoryRouter>
      <Routes>
        <Route
          element={
            <main className="p-4">
              <Outlet
                context={{ permissions, permissionsResolved: true, menus: [] }}
              />
            </main>
          }
        >
          <Route path="/" element={<PlatformDomainEventsPage />} />
        </Route>
      </Routes>
    </MemoryRouter>
  </IntlAppProvider>
)
