import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import type {ProjectDetail} from "../../lib/iam-api"
import {ModeFacts, ProjectModes} from "./modes"
import type {ModeHistory} from "./modes-api"

test("mode facts expose current failed prerequisites and immutable human history in all locales", async () => {
  const row: ModeHistory = {id:"9", project_id:"3", actor_id:"2", from_mode:"HYBRID", to_mode:"HUMAN_ONLY", trigger:"TAKEOVER", reason:"Take over after review", authority_source:"permission:project_takeover", version_after:4, occurred_at:"2026-10-22T00:00:00Z", revoked_delegation_ids:["7"], revoked_spawn_grant_ids:["8"], preconditions_snapshot:[]}
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ModeFacts criteria={[{key:"P1",passed:false,detail:"raw backend detail"}]} history={[row]}/></IntlAppProvider>)
    expect(html).toContain("✗")
    expect(html).toContain("Take over after review")
    expect(html).toContain("permission:project_takeover")
    expect(html).toContain("1 / 1")
    expect(html).not.toContain("raw backend detail")
  }
})

test("mode actions remain permission gated and archived projects have no command form", async () => {
  const messages = await loadMessages("en-US")
  const project = {project_id:"3", version:4, stage:"ACTIVE", team_mode:"HYBRID", archived_at:null} as ProjectDetail
  for (const permission of ["", "project_mode_change", "project_takeover"]) for (const stage of ["ACTIVE", "CLOSED", "CANCELLED"]) {
    const router = createMemoryRouter([{element:<Outlet context={{permissions:new Set([permission]), permissionsResolved:true}}/>, children:[{path:"/",element:<ProjectModes project={{...project,stage}} onChanged={()=>{}}/>}]}])
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
    expect(html.includes("Change mode")).toBe(permission === "project_mode_change" && stage === "ACTIVE")
    expect(html.includes("Take over project")).toBe(permission === "project_takeover" && stage === "ACTIVE")
    expect(html).toContain("Running processes retain their execution state.")
    router.dispose()
  }
})
