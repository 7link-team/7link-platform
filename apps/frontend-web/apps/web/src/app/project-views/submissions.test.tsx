import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { entityNavigation, workspaceScopePath } from "../../lib/navigation"
import IntakeWorkbench from "./intake-workbench"

it("offers only owned submissions to intake_submit authority in the existing project navigation", async () => {
  expect(entityNavigation(["intake_submit"], "project").filter(row => row.path === "/intake")).toHaveLength(1)
  expect(workspaceScopePath("/admin/1/entities/7/intake", "1", "8", ["intake_submit"], "project")).toBe("/admin/1/entities/8/intake")
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    for (const allowed of [false, true]) {
      const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(allowed ? ["intake_submit"] : []), permissionsResolved: true }} />, children: [{ path: "/admin/:tenantID/entities/:entityID/intake", element: <IntakeWorkbench /> }] }], { initialEntries: ["/admin/1/entities/7/intake"] })
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
      const labels = messages.iam as Record<string, string>
      if (allowed) expect(html).toContain(labels.intakeMySubmissions!)
      else expect(html).not.toContain(labels.intakeMySubmissions!)
      expect(html).not.toContain("raw_content")
      expect(html).not.toContain('name="intake_id"')
      expect(html).not.toContain('name="proposal_hash"')
    }
  }
})
