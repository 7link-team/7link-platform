// Isolated rendered UI contract fixture; never imported by production.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, RouterProvider, Outlet } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import { AuthContext } from "../../src/components/auth"
import Goals from "../../src/app/goals/page"
import Plans, { MyPlansPage } from "../../src/app/plans/page"
import Reports from "../../src/app/reports/page"
import Templates from "../../src/app/templates/page"
import {softwareDefinition} from "../../src/app/templates/workflow"
import Inbox from "../../src/app/governance/page"
import Policies from "../../src/app/governance/policies"
import Integrations from "../../src/app/integrations/page"
import "../../src/index.css"
const query = new URLSearchParams(location.search)
const locale = query.get("locale") ?? "en-US"
const page = query.get("page") ?? "integrations"
const unavailable = query.has("unavailable")
const denied = query.has("denied")
const requests: string[] = []
Object.assign(window, { fixtureRequests: requests })
const nativeFetch = window.fetch.bind(window)
window.fetch = async (input, init) => {
  const url = String(input)
  if (!url.includes("/api/")) return nativeFetch(input, init)
  requests.push(`${init?.method ?? "GET"} ${url}`)
  let data: unknown = { items: [], has_more: false }
  if (query.has("editor") && url.includes("/templates")) data = {items:[{id:"1",key:"fixture",state:"DRAFT",hash:"a".repeat(64),version:1,definition:softwareDefinition()}],has_more:false}
  if (unavailable || init?.method === "OPTIONS")
    return new Response(
      JSON.stringify({
        code: 503,
        message: "UNAVAILABLE: isolated fixture service unavailable",
      }),
      { status: 503, headers: { "Content-Type": "application/json" } }
    )
  if (url.includes("/inbox-items"))
    data = {
      items: [],
      has_more: false,
      capabilities: [{ kind: "PLAN_APPROVAL", status: "UNAVAILABLE" }],
    }
  if (url.includes("/governance-catalog"))
    data = {
      policies: [],
      actions: [],
      settings: "AVAILABLE",
      escalations: "UNAVAILABLE",
      verification_override: "UNAVAILABLE",
    }
  if (url.includes("/escalation-rules")) data = []
  return new Response(JSON.stringify({ code: 0, message: "ok", data }), {
    headers: { "Content-Type": "application/json" },
  })
}
const permissions = new Set(
  denied
    ? []
    : [
        "integration_manage",
        "policy_read",
        "policy_manage",
        "goal_create",
        "plan_create",
        "report_create",
      ]
)
const auth = {
  session: { subject: "11", organization_id: "1" },
  status: "authenticated",
}
export function Shell() {
  return (
    <AuthContext.Provider value={auth as never}>
      <main className="h-screen p-4">
        <aside className="mb-3 border-b text-xs">
          Isolated UI contract fixture: empty/unavailable service responses
        </aside>
        <Outlet
          context={{ permissions, permissionsResolved: true, tenantID: "1" }}
        />
      </main>
    </AuthContext.Provider>
  )
}
const entries = [
  ["goals", Goals],
  ["plans/me", MyPlansPage],
  ["entities/2/plans", Plans],
  ["reports", Reports],
  ["templates", Templates],
  ["governance/inbox", Inbox],
  ["governance/policies", Policies],
  ["integrations", Integrations],
] as const
const router = createMemoryRouter(
  [
    {
      element: <Shell />,
      children: entries.map(([path, Component]) => ({
        path: `/admin/:tenantID/${path.replace("entities/2/", "entities/:entityID/")}`,
        element: <Component />,
      })),
    },
  ],
  { initialEntries: [`/admin/1/${page}`] }
)
void loadMessages(locale).then((messages) =>
  createRoot(document.getElementById("root")!).render(
    <IntlAppProvider initialLocale={locale} initialMessages={messages}>
      <RouterProvider router={router} />
    </IntlAppProvider>
  )
)
