import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import NovelBoard from "./novel-board"
import { novelEvidenceText, type StudioRecord } from "./api"

test("novel original reader anchors Unicode quotations to the exact recorded version", () => {
 const record = {id: "42", version: 2, content: {text: "她🙂 opened the gate.", novel: {entries: [{key: "gate", text: "Full moon only"}]}}} as StudioRecord
 const cite = {record_id: "42", version: 2, start: 3, quote: "opened", sha256: "hash"}
 expect(novelEvidenceText(record, cite)).toEqual({before: "她🙂 ", quote: "opened", after: " the gate."})
 for (const changed of [{...cite, record_id: "43"}, {...cite, version: 1}, {...cite, start: 2}, {...cite, start: -1}, {...cite, quote: "invented"}]) expect(novelEvidenceText(record, changed)).toBeNull()
 expect(novelEvidenceText(record, {...cite, entry_key: "gate", quote: "Full moon only"})?.quote).toBe("Full moon only")
 expect(novelEvidenceText(record, {...cite, entry_key: "gate", quote: "invented"})).toBeNull()
})
test("all five novel tabs restore from the URL in three locales and expose explicit model unavailability", async () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const messages = await loadMessages(locale)
  for (const tab of ["bible", "outline", "chapters", "continuity", "foreshadow"]) {
   const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(["template_read", "work_item_read", "project_manage"]), permissionsResolved: true}}/>, children: [{path: "/", element: <NovelBoard tenant="7" project="99"/>}]}], {initialEntries: [`/?novel_tab=${tab}`]})
   const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
   expect(html).toContain(`aria-labelledby="novel-tab-${tab}"`); expect(html.match(/role="tab"/g)).toHaveLength(5)
   expect(html).not.toContain("iam.novel"); expect(html).not.toContain("novelTab_"); expect(html).not.toContain("????")
   if (tab === "continuity") {expect(html).toContain("CED"); expect(html).toContain("LLM")}
   router.dispose()
  }
 }
})
