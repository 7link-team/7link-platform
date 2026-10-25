import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import TeamBoard from "./team-board"
import { teamCanSave, teamCanSign, type StudioContent, type TeamTaskView } from "./api"

const content: StudioContent = {title: "Report", text: "", source: "", owner_id: "12", due_at: "2040-01-01T00:00:00Z", duration_ms: 0, provider: "", model: "", seed: "", team: {work_item_id: "42", signer_id: "13", checklist: [{text: "Report exists", done: true, evidence_index: 0}], evidence: [{url: "https://example.invalid/report", version: "v2", modified_at: "2039-12-01T00:00:00Z"}], risks: [], sign_offs: []}}
test("task controls require canonical identity, owner, deadline and per-criterion evidence", () => {
 expect(teamCanSave(content)).toBe(true)
 for (const changed of [{...content, owner_id: ""}, {...content, due_at: ""}, {...content, team: {...content.team!, work_item_id: "invalid"}}, {...content, team: {...content.team!, signer_id: ""}}, {...content, team: {...content.team!, evidence: []}}, {...content, team: {...content.team!, checklist: []}}]) expect(teamCanSave(changed)).toBe(false)
 const task = {record: {content}, work: {state: "IN_PROGRESS"}, complete: 1} as TeamTaskView
 expect(teamCanSign(task)).toBe(true)
 expect(teamCanSign({...task, complete: 0})).toBe(false)
 expect(teamCanSign({...task, work: {...task.work, state: "CANCELLED"}})).toBe(false)
 expect(teamCanSign({...task, record: {...task.record, content: {...content, team: {...content.team!, risks: [{title: "Risk", owner_id: "12", due_at: "2040-01-01", disposition: "", resolved: false}]}}}})).toBe(false)
})
test("team tabs restore from URL and human-only boundaries are translated in three locales", async () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const messages = await loadMessages(locale)
  for (const tab of ["tasks", "risks", "drafts", "signoff"]) {
   const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(["template_read", "work_item_read", "project_manage"]), permissionsResolved: true}}/>, children: [{path: "/", element: <TeamBoard tenant="7" project="99"/>}]}], {initialEntries: [`/?team_tab=${tab}`]})
   const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
   expect(html).toContain('role="tablist"');expect(html).toContain('aria-selected="true"')
   expect(html).not.toContain("iam.team");expect(html).not.toContain("teamTab_")
   if (tab === "tasks") expect(html).toContain('type="datetime-local"')
   router.dispose()
  }
 }
})
