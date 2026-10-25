import {describe, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {WorkSkillDistillation} from "./distillation"

describe("accepted-work distillation entry", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) for (const state of ["ACCEPTED", "IN_PROGRESS"]) for (const writable of [false, true]) {
    it(`${locale} ${state} permission ${writable}`, async () => {
      const messages = await loadMessages(locale)
      const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(writable ? ["knowledge_write"] : []), permissionsResolved: true}} />, children: [{path: "/admin/:tenantID/work", element: <WorkSkillDistillation work="9007199254740993" state={state} />}]}], {initialEntries: ["/admin/1/work"]})
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
      router.dispose()
      const visible = state === "ACCEPTED" && writable
      expect(html.includes("<form")).toBe(visible)
      if (visible) {
        const words = messages.iam as Record<string, string>
        expect(html).toContain(words.knowledgeSkillDistill!)
        expect(html).toContain(words.knowledgeSkillDistillHint!)
        expect(html).toContain('inputMode="numeric"')
        expect(html).toContain("<label")
      }
    })
  }
})
