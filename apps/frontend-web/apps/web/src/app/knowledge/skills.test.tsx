import {describe, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import KnowledgeSkillsPage from "./skills"
describe("skills library permission and language presentation", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) for (const writable of [false, true]) {
    it(`renders ${locale} creation permission ${writable}`, async () => {
      const messages = await loadMessages(locale)
      const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(writable ? ["knowledge_read", "knowledge_write"] : ["knowledge_read"]), permissionsResolved: true}}/>, children: [{path: "/admin/:tenantID/knowledge/skills", element: <KnowledgeSkillsPage/>}]}], {initialEntries: ["/admin/1/knowledge/skills"]})
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>); router.dispose()
      const words = messages.iam as Record<string,string>
      expect(html).toContain(words.knowledgeSkills!); expect(html).not.toContain("???"); expect(html.includes("<textarea")).toBe(writable)
      if (writable) {expect(html).toContain(words.knowledgeSkillFormat!); expect(html).toContain(words.knowledgeSkillSaveDraft!); expect(html).toContain("aria-describedby")}
    })
  }
})
