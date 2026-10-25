// Isolated UI contract fixture. All API responses below are explicit test data.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import { AgentInspection } from "../../src/app/agents/inspection"
import { IntakeChange } from "../../src/app/project-views/intake"
import WorkItemsPage from "../../src/app/work-items/page"
import "../../src/index.css"

const query = new URLSearchParams(location.search)
const requests: string[] = []
const writes: { path: string; body: unknown; headers: Record<string, string> }[] = []
Object.assign(window, { fixtureRequests: requests, fixtureWrites: writes })
let converted = false
const nativeFetch = window.fetch.bind(window)
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.origin)
  if (!url.pathname.includes("/api/")) return nativeFetch(input, init)
  requests.push(`${init?.method ?? "GET"} ${url.pathname}${url.search}`)
  if (init?.method === "POST") writes.push({ path: url.pathname, body: JSON.parse(String(init.body)), headers: Object.fromEntries(new Headers(init.headers)) })
  if (query.has("denied")) return Response.json({ code: 142403, message: "Fixture denied" }, { status: 403 })
  if (url.pathname.includes("/intake-changes")) {
    if (url.pathname.endsWith("/conversion")) { converted = true; return Response.json({ code: 0, data: { id: "9007199254740993", state: "CONVERTED", version: 5 } }) }
    return Response.json({ code: 0, data: init?.method === "POST" ? { id: "9007199254740993", version: 4, state: "TRIAGED" } : { id: "9007199254740993", project_id: "3", change_id: query.has("wrong") ? "5" : "4", requirement_id: "9007199254740995", original_intake_id: "9007199254740997", state: converted ? "CONVERTED" : "TRIAGED", version: converted ? 5 : 4, card_revision: 2, proposal_hash: "a".repeat(64), proposal: "Fixture frozen proposal", impact: "Fixture frozen impact", ...(converted ? { result_version_no: 6 } : {}) } })
  }
  if (url.pathname.endsWith("/me")) return Response.json({ code: 0, data: { subject: "2" } })
  if (url.pathname.endsWith("/capabilities")) return Response.json({ code: 0, data: { password_recovery: false } })
  if (url.pathname.endsWith("/reauthentication/methods")) return Response.json({ code: 0, data: { methods: ["password"], mfa_required: false } })
  if (url.pathname.endsWith("/identity-links") || url.pathname.endsWith("/identity-link-providers")) return Response.json({ code: 0, data: [] })
  if (url.pathname.endsWith("/reauthentication/challenges")) return Response.json({ code: 0, data: { proof: "fixture-requirement-proof" } })
  if (url.pathname.endsWith("/reauthentication/verifications")) return Response.json({ code: 0, data: { verified: true } })
  if (url.pathname.includes("/work-items")) {
    const work = { id: "9007199254740993", project_id: "3", title: "Fixture exact defect", number: 1, type: "BUG", state: "ACCEPTED", objective: "Fixture objective", description: "Exact opaque identifier detail", severity: "CRITICAL", priority: "HIGH", reproducibility: "ALWAYS", found_version: "v1", accountable_principal_id: "2", current_execution_id: null, current_claim_id: null, requirement_id: null, version: 1, is_container: false, baseline_stale: false, paused_at: null, defect_stage: "VERIFIED", preparation: {}, created_at: "2026-10-20T00:00:00Z", updated_at: "2026-10-20T00:00:00Z" }
    return Response.json({ code: 0, data: url.pathname.endsWith("/work-items") || /\/(results|dependencies)$/.test(url.pathname) ? [] : work, meta: { page: { total: 0 } } })
  }
  const cursor = url.searchParams.get("cursor")
  const data = url.pathname.endsWith("/work") ? {
    items: [{ id: "31", project_id: "3", title: "Fixture assigned work", number: 1, state: "IN_PROGRESS", responsibility: "executor" }], has_more: false,
  } : url.pathname.endsWith("/messages") ? {
    items: [{ id: cursor ? "29" : "30", channel_id: "20", seq: cursor ? 1 : 2, author_id: "7", body: cursor ? "Fixture older message" : "Fixture current message", created_at: "2026-10-20T00:00:00Z", attachments: [] }], has_more: !cursor, next_cursor: cursor ? "" : "30",
  } : { items: [{ id: "20", name: "Fixture Agent direct", kind: "DIRECT", visibility: "INTERNAL", version: 1 }], has_more: false }
  return Response.json({ code: 0, data })
}
const locale = query.get("locale") ?? "en-US"
const tab = query.get("tab") === "work" ? "work" : query.get("tab") === "workspace" ? "workspace" : "direct"
const messages = await loadMessages(locale)
const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(query.has("readonly") ? [] : ["scope_change"]), permissionsResolved: true }}/>, children: [{ path: "/", element: <main className="p-4">{query.get("tab") === "intake" ? <IntakeChange project="3" change="4" canFreeze confirmed={!query.has("unconfirmed")} onUpdated={() => {}}/> : <AgentInspection tenant="1" agent="7" tab={tab}/>}</main> }, { path: "/admin/:tenantID/entities/:entityID/work-items", element: <main className="p-4"><WorkItemsPage/></main> }] }], { initialEntries: [query.get("tab") === "defect" ? "/admin/1/entities/3/work-items?work=9007199254740993&type=BUG&title=preserved-filter" : "/"] })
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
