import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { IntakeChange } from "./intake"
import IntakeWorkbench from "./intake-workbench"
import { IntakeTriage } from "./intake-triage"

it("hides Intake controls without current scope authority and translates the frozen-source workflow", async () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    for (const authorized of [false, true]) {
      const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(authorized ? ["scope_change"] : []), permissionsResolved: true }}/>, children: [{ path: "/", element: <IntakeChange project="3" change="4" canFreeze confirmed={false} onUpdated={() => {}}/> }] }])
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
      if (!authorized) { expect(html).not.toContain("<input"); expect(html).not.toContain("<button") }
      else {
        expect(html).toContain((messages.iam as Record<string, string>).intakeFreeze!)
        expect(html).toContain((messages.iam as Record<string, string>).intakeOriginalID!)
        expect(html).toContain('pattern="[1-9][0-9]{0,18}"')
        expect(html).not.toContain("intakeChangeTitle")
      }
    }
  }
})

it("keeps the standalone Intake workbench internal and renders translated processing limits", async () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    const labels = messages.iam as Record<string, string>
    for (const authorized of [false, true]) {
      const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(authorized ? ["scope_change"] : ["channel_read"]), permissionsResolved: true }} />, children: [{ path: "/admin/:tenantID/entities/:entityID/intake", element: <IntakeWorkbench /> }] }], { initialEntries: ["/admin/1/entities/3/intake"] })
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
      expect(html).toContain(labels.intakeWorkbenchTitle!)
      expect(html).not.toContain("intakeWorkbenchTitle")
      if (authorized) {
        expect(html).toContain(labels.intakeWorkbenchUnavailable!)
        expect(html).toContain(labels.intakeWorkbenchKind!)
        expect(html).toContain(labels.intakeWorkbenchReceived!)
      } else {
        expect(html).toContain(labels.projectSurfaceAccessUnavailable!)
        expect(html).not.toContain("<table")
        expect(html).not.toContain("<button")
      }
    }
  }
})

it("labels all triage inputs and preserves exact stored assessment text in each locale", async () => {
  for (const locale of ["en-US","zh-CN","ms-MY"]) {
    const messages=await loadMessages(locale), labels=messages.iam as Record<string,string>
    const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><IntakeTriage project="3" source={{disposition_delivery:{state:"NOT_REQUESTED"},id:"9007199254740993",project_id:"3",ref_no:1,title:"Source",kind:"NEW",state:"TRIAGED",version:2,card_revision:1,received_at:"2026-10-21T00:00:00Z",raw_content_hash:"a".repeat(64),raw_content_format:"PLAIN",triage:{id:"9007199254740995",intake_id:"9007199254740993",project_id:"3",revision:1,version:2,hash:"b".repeat(64),actor_id:"11",created_at:"2026-10-21T00:00:00Z",draft:{summary:"<script>unsafe()</script>",requirement_draft:"Export",criteria:["Exact rows"],risks:[],questions:[],size:"S"}}}} onSaved={()=>{}} /></IntlAppProvider>)
    expect(html).toContain(labels.intakeTriageTitle!)
    expect(html).toContain(labels.intakeTriageDraftOnly!)
    expect(html).toContain('name="summary"')
    expect(html).toContain('name="criteria"')
    expect(html).toContain('name="size"')
    expect(html).toContain("&lt;script&gt;unsafe()&lt;/script&gt;")
    expect(html).not.toContain("<script>")
    expect(html).not.toContain("intakeTriageTitle")
    expect(html.match(/<label/g)?.length).toBe(6)
  }
})
