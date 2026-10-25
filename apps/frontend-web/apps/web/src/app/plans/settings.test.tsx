import {describe,expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter,Outlet,RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {PlanningSettingsButton} from "./settings"
import {settingsLabels} from "./settings-labels"

describe("planning settings entry",()=>{
 it("offers settings only with its real read permission in all supported locales",async()=>{
  for(const locale of ["en-US","zh-CN","ms-MY"]){
   for(const permissions of [new Set<string>(),new Set(["goal_read"])]){
    const router=createMemoryRouter([{path:"/admin/:tenantID",element:<Outlet context={{permissions,permissionsResolved:true}}/>,children:[{path:"reports",element:<PlanningSettingsButton/>}]}],{initialEntries:["/admin/1/reports"]})
    const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><RouterProvider router={router}/></IntlAppProvider>)
    expect(html.includes(settingsLabels(locale).title)).toBe(permissions.has("goal_read"))
    router.dispose()
   }
  }
 })
})
