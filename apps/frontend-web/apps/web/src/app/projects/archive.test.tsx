import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import type { ProjectDetail } from "../../lib/iam-api"
import { ProjectArchive } from "./archive"

test("archived history has a separately authorized explicit restore flow in all three locales", async () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    for (const canManage of [false, true]) {
      const project = {project_id: "3", tenant_id: "1", version: 7, archived_at: "2026-10-22T00:00:00Z", stage: "CLOSED"} as ProjectDetail
      const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(canManage ? ["project_manage"] : []), permissionsResolved: true}}/>, children: [{path: "/admin/:tenantID/entities/:entityID/project", element: <ProjectArchive project={project} onChanged={() => {}}/>}]}], {initialEntries: ["/admin/1/entities/3/project"]})
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
      expect(html.includes("<form")).toBe(canManage)
      expect(html).not.toContain("archiveRestoreNotice")
      expect(html).not.toContain("archiveReasonRequired")
      expect(html).not.toContain("archiveReadOnly")
      if (canManage) expect(html).toContain('type="checkbox"')
      router.dispose()
    }
  }
})

test("archive action is fenced until current preflight receipts load", async () => {
  const messages = await loadMessages("en-US")
  const project = {project_id: "3", tenant_id: "1", version: 7, archived_at: null, stage: "ACTIVE"} as ProjectDetail
  const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(["project_manage"]), permissionsResolved: true}}/>, children: [{path: "/", element: <ProjectArchive project={project} onChanged={() => {}}/>}]}])
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
  expect(html).toContain("<fieldset disabled")
  expect(html).toContain("Refresh resource receipts")
  router.dispose()
})
