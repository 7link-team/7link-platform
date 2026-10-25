import {expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter,RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {GoalAlignmentPanel} from "./alignment"
import type {PlanningRecord} from "./api"

for(const locale of ["en-US","zh-CN","ms-MY"] as const)it(`localizes alignment loading without inventing contributor facts: ${locale}`,async()=>{
 const messages=await loadMessages(locale)
 const record={id:"42",version:1,state:"ACTIVE",content:{level:"PROJECT",key_results:[{key:"quality",title:"Quality"}]}} as PlanningRecord
 const router=createMemoryRouter([{path:"/admin/:tenantID/goals",element:<GoalAlignmentPanel record={record} busy={false} save={async()=>{}}/>}],{initialEntries:["/admin/1/goals"]})
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
 router.dispose()
 expect(html).toContain((messages.iam as Record<string,string>).goalAlignmentTitle!)
 expect(html).toContain('role="status"')
 expect(html).not.toContain("50%")
 expect(html).not.toContain("<form")
})
