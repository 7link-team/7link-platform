// Browser interaction fixture with explicit API doubles; not backend acceptance.
import { createRoot } from "react-dom/client"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "@workspace/ui/i18n/messages/en-US.json"
import iam from "../../src/messages/en-US.json"
import PlatformJobsPage, {
  PlatformSchedulesPage,
} from "../../src/app/system/jobs"
import {
  platformJobsApi,
  type PlatformJob,
  type PlatformSchedule,
} from "../../src/lib/platform-jobs-api"
import "../../src/index.css"

const params = new URLSearchParams(location.search)
const permissions = new Set(
  params.has("denied")
    ? []
    : [
        "system_job_view",
        ...(params.has("readonly") ? [] : ["system_job_manage"]),
      ]
)
const jobs = [
  {
    id: "123",
    tenant_id: "42",
    kind: "delivery.rebuild",
    queue: "default",
    state: "DEAD",
    priority: 3,
    attempts: 5,
    max_attempts: 5,
    manual_retries: 0,
    version: 7,
    has_handler: true,
    run_at: "2026-10-20T18:00:00Z",
    created_at: "2026-10-20T17:00:00Z",
    last_error_code: "UPSTREAM_UNAVAILABLE",
    payload: { token: "[REDACTED]" },
    error_history: [],
  },
  {
    id: "124",
    tenant_id: "42",
    kind: "removed.handler",
    queue: "default",
    state: "DEAD",
    priority: 3,
    attempts: 5,
    max_attempts: 5,
    manual_retries: 0,
    version: 1,
    has_handler: false,
    run_at: "2026-10-20T18:00:00Z",
    created_at: "2026-10-20T17:00:00Z",
    last_error_code: "NO_HANDLER",
  },
] as PlatformJob[]
const schedules = [
  {
    id: "456",
    name: "delivery.cleanup",
    kind: "delivery.cleanup",
    queue: "default",
    enabled: true,
    orphaned: false,
    tenant_scope: "all",
    interval_ms: 60000,
    timezone: "UTC",
    next_run_at: "2026-10-20T19:00:00Z",
    version: 3,
  },
] as PlatformSchedule[]
platformJobsApi.list = async () => ({
  items: jobs,
  has_more: false,
  summary: {
    running: 0,
    pending: 0,
    dead_24h: 2,
    oldest_pending_age_seconds: 0,
  },
})
platformJobsApi.get = async (id) => jobs.find((job) => job.id === id)!
platformJobsApi.retry = async (job, reason) => {
  if (!reason.trim()) throw new Error("Reason required")
  const next = { ...job, state: "PENDING", version: job.version + 1 }
  jobs[jobs.findIndex((row) => row.id === job.id)] = next
  return next
}
platformJobsApi.retryBatch = async (ids, reason) => {
  if (!reason.trim()) throw new Error("Reason required")
  const retried: string[] = []
  for (const id of ids) {
    const job = jobs.find((row) => row.id === id)
    if (job?.state === "DEAD" && job.has_handler) {
      await platformJobsApi.retry(job, reason)
      retried.push(id)
    }
  }
  return { retried, skipped: [] }
}
platformJobsApi.cancel = async (job) => ({
  ...job,
  state: "CANCELLED",
  version: job.version + 1,
})
platformJobsApi.schedules = async () => schedules
platformJobsApi.enable = async (schedule, enabled) => {
  const next = { ...schedule, enabled, version: schedule.version + 1 }
  schedules[0] = next
  return next
}
platformJobsApi.run = async () => ({ job_id: "789" })
createRoot(document.getElementById("root")!).render(
  <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam }}>
    <MemoryRouter
      initialEntries={[params.has("schedules") ? "/schedules" : "/jobs"]}
    >
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
          <Route path="/jobs" element={<PlatformJobsPage />} />
          <Route path="/schedules" element={<PlatformSchedulesPage />} />
        </Route>
      </Routes>
    </MemoryRouter>
  </IntlAppProvider>
)
