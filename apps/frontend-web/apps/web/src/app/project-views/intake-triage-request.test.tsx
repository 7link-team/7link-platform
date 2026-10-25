import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import IntakeWorkbench from "./intake-workbench"

it("offers a translated narrow request form to triage authority without exposing internal source or conversion", async () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    const labels = messages.iam as Record<string, string>
    for (const key of ["intakeRequestTriage", "intakeRequestID", "intakeRequestNote", "intakeRequestHint", "intakeRequestQueued", "intakeRequestCoalesced"]) {
      expect(labels[key]).not.toMatch(/^\?+$/)
      if (locale === "zh-CN") expect(labels[key]).toMatch(/\p{Script=Han}/u)
    }
    for (const permission of ["intake_triage", "channel_read"]) {
      const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set([permission]), permissionsResolved: true }} />, children: [{ path: "/admin/:tenantID/entities/:entityID/intake", element: <IntakeWorkbench /> }] }], { initialEntries: ["/admin/1/entities/3/intake"] })
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
      if (permission === "intake_triage") {
        expect(html).toContain(labels.intakeRequestTriage!)
        expect(html).toContain(labels.intakeRequestHint!)
        expect(html).toContain('name="intake_id"')
        expect(html).toContain('name="note"')
        expect(html).toContain('maxLength="2000"')
        expect(html).not.toContain("intakeRequestHint")
      } else expect(html).not.toContain('name="note"')
      expect(html).not.toContain('name="proposal_hash"')
      expect(html).not.toContain('name="summary"')
    }
  }
})
