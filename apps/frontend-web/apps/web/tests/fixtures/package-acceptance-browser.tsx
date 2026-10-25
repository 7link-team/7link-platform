// Isolated rendered contract fixture; every response is explicit test data.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import ClientWorkflowPage from "../../src/app/project-views/client"
import type { ClientRecords } from "../../src/lib/clientwork-api"
import "../../src/index.css"

const flags = new URLSearchParams(location.search)
const first = "a".repeat(64), second = "b".repeat(64), hash = "c".repeat(64)
const requests: string[] = []
const writes: unknown[] = []
Object.assign(window, { fixtureRequests: requests, fixtureWrites: writes })
let created = !flags.has("unavailable")
const row: ClientRecords["packages"] = {
  id: "9007199254740993", state: "PUBLISHED", version: 4, archived_at: null, content_hash: hash,
  content: { title: "Fixture exact package", release_notes: "Frozen published release", artifacts: [] },
  items: [first, second].map((item_id, index) => ({ item_id, artifact: { name: `File ${index + 1}`, version_id: String(index + 8), sha256: "d".repeat(64), coverage: "verified", verification_overrides: null } })),
}
if (flags.has("internal")) {
  row.state = "PARTIALLY_ACCEPTED"
  row.decision = { content_hash: hash, reason: "Exact recorded feedback", decision: "PARTIALLY_ACCEPTED", items: [{ item_id: first, result: "ACCEPTED" }, { item_id: second, result: "REJECTED", feedback_category: "SECURITY", comment: "Fixture rejected source details" }] }
}
const nativeFetch = window.fetch.bind(window)
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.origin)
  if (!url.pathname.includes("/api/")) return nativeFetch(input, init)
  const method = init?.method ?? "GET"
  requests.push(`${method} ${url.pathname}${url.search}`)
  const ok = (data: unknown) => Response.json({ code: 0, data })
  if (url.pathname.endsWith("/client-access")) return flags.has("unknown") ? Response.json({ code: 142503, message: "Fixture unavailable" }, { status: 503 }) : ok({ external: !flags.has("internal"), principal_id: flags.has("internal") ? "2" : "4" })
  if (method === "POST") writes.push({ path: url.pathname, body: JSON.parse(String(init?.body)), headers: Object.fromEntries(new Headers(init?.headers)) })
  if (url.pathname.endsWith("/rework")) {
    if (flags.has("denied")) return Response.json({ code: 142403, message: "Fixture forbidden" }, { status: 403 })
    if (method === "POST") { created = true; return ok({ id: "9007199254740995", version: 1, state: "TRIAGED" }) }
    if (!created) return Response.json({ code: 142404, message: "Fixture no source" }, { status: 404 })
    return ok({ id: "9007199254740995", project_id: flags.has("wrong") ? "5" : "3", package_id: row.id, receipt_id: "9007199254740997", content_hash: hash, version: 1, state: "TRIAGED", rejection: { package_id: row.id, receipt_id: "9007199254740997", content_hash: hash, recipient_id: "4", items: [{ item_id: second, work_item_id: "9007199254740999", result_id: "9007199254741001", evidence_id: "9007199254741003", requirement_id: "9007199254741005", source_intake_id: "9007199254741007", feedback_category: "SECURITY", comment: "Fixture rejected source details" }] } })
  }
  if (url.pathname.endsWith("/receipts")) {
    const body = JSON.parse(String(init?.body))
    row.state = body.items ? "PARTIALLY_ACCEPTED" : body.decision === "ACCEPT" ? "ACCEPTED" : "REJECTED"
    row.decision = { ...body, decision: body.items ? "PARTIALLY_ACCEPTED" : body.decision }
    row.version++
    return ok({ id: row.id, state: row.state, version: row.version })
  }
  if (url.pathname.endsWith("/delivery-packages")) return ok({ items: [row], has_more: false })
  if (url.pathname.endsWith(`/delivery-packages/${row.id}`)) return ok({ ...row, ...(row.state !== "PUBLISHED" && row.state !== "ACCEPTED" ? created ? { rework_intake_id: "9007199254740995" } : { rework_unavailable: true, rework_unavailable_reason: "REWORK_SOURCE_UNAVAILABLE" } : {}) })
  return Response.json({ code: 142404, message: "Unconfigured fixture response" }, { status: 404 })
}
const locale = flags.get("locale") ?? "en-US"
const messages = await loadMessages(locale)
const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(flags.has("noauthority") ? ["channel_read"] : ["channel_read", "scope_change"]), permissionsResolved: true }}/>, children: [{ path: "/admin/:tenantID/entities/:entityID/deliveries", element: <ClientWorkflowPage surface="deliveries"/> }] }], { initialEntries: [`/admin/1/entities/3/deliveries?id=${row.id}`] })
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
