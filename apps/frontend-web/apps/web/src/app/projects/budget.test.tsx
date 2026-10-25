import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter,Outlet,RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import type {ProjectDetail} from "../../lib/iam-api"
import {BudgetHistory,ProjectBudget} from "./budget"

test("budget history shows actual cap/version/spend/reason in each locale",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=await loadMessages(locale)
  const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><BudgetHistory rows={[{id:"1",kind:"CAP_CHANGED",cap_micros:null,spent_micros:37,notify_pct:80,cap_version:4,state:"NORMAL",reason:"Reviewed actual spend",actor_id:"9",occurred_at:"2026-10-22T00:00:00Z"}]}/></IntlAppProvider>)
  expect(html).toContain("Reviewed actual spend");expect(html).toContain("37");expect(html).toContain("v4");expect(html).not.toContain("budgetUnlimited")
 }
})
test("budget mutation uses current project_manage and is absent on terminal projects",async()=>{
 const messages=await loadMessages("en-US")
 for(const permission of ["","project_manage"])for(const stage of ["ACTIVE","CLOSED","CANCELLED"]){
  const project={project_id:"3",version:1,stage,archived_at:null,budget_spent_micros:70,budget_notify_pct:80,budget_cap_micros:100,budget_state:"NORMAL"}as ProjectDetail
  const router=createMemoryRouter([{element:<Outlet context={{permissions:new Set([permission]),permissionsResolved:true}}/>,children:[{path:"/",element:<ProjectBudget project={project} onChanged={()=>{}}/>}]}])
  const html=renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
  expect(html.includes("<form")).toBe(permission==="project_manage"&&stage==="ACTIVE");expect(html).toContain("does not resume paused executions");router.dispose()
 }
})
