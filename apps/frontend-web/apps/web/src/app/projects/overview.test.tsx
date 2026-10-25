import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter,Outlet,Route,Routes} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import type {ProjectDetail} from "../../lib/iam-api"
import {ProjectOverviewFacts,ProjectBudgetTrend,ProjectWorkAreas} from "./overview"
test("overview preserves stored risks, unresolved provisioning and missing health timestamp in all locales",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=await loadMessages(locale)
 const project={health:"RED",health_refreshed_at:null,health_reasons:[{code:"ACCEPTANCE_OVERDUE",package_id:"9007199254740993"}],holds:[{kind:"BUDGET",reason:"Spend limit"}],provisioning_issues:[{code:"CHANNEL_PENDING"}],budget_cap_micros:100,budget_spent_micros:110,budget_state:"EXHAUSTED",team_mode:"HYBRID",dispatch_policy:"MANUAL",lead_agent_id:"9007199254740995"}as ProjectDetail
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ProjectOverviewFacts project={project}/></IntlAppProvider>)
 for(const value of ["ACCEPTANCE_OVERDUE","9007199254740993","CHANNEL_PENDING","9007199254740995","EXHAUSTED","Spend limit","110"])expect(html).toContain(value)
 expect(html).not.toContain("overviewUnknown");expect(html).not.toContain("Invalid Date")
 }
})

test("budget trend retains exact micros and distinguishes unavailable, empty and truncated observations",async()=>{
 const messages=await loadMessages("en-US")
 const render=(trend?:Parameters<typeof ProjectBudgetTrend>[0]["trend"])=>renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><ProjectBudgetTrend trend={trend}/></IntlAppProvider>)
 expect(render()).toContain("unavailable")
 expect(render({available:true,truncated:false,points:[]})).toContain("No recorded settlements")
 const html=render({available:true,truncated:true,points:[{id:"1",at:"2026-10-21T00:00:00Z",spent_micros:"9007199254740993"},{id:"2",at:"2026-10-22T00:00:00Z",spent_micros:"9223372036854775807"}]})
 expect(html).toContain("9007199254740993");expect(html).toContain("9223372036854775807");expect(html).toContain("latest 100");expect(html).toContain("polyline");expect(html).not.toContain("NaN")
})

test("project work areas link to the current project and honor current permissions in all locales",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  const messages=await loadMessages(locale),strings=messages.iam as Record<string,string>
  const render=(permissions:string[])=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><Routes><Route element={<Outlet context={{permissions:new Set(permissions),permissionsResolved:true}}/>}><Route index element={<ProjectWorkAreas tenantID="7" projectID="9007199254740993"/>}/></Route></Routes></MemoryRouter></IntlAppProvider>)
  const html=render(["work_item_read","requirement_read","channel_read"])
  for(const path of ["work-items","requirements","conversations","deliveries"])expect(html).toContain(`/admin/7/entities/9007199254740993/${path}`)
  expect(html).toContain(strings.projectWorkAreas!)
  expect(html).toContain(strings.projectWorkAreasHint!.replaceAll("'", "&#x27;"))
  expect(strings.projectWorkAreas!).not.toContain("?")
  expect(strings.projectWorkAreasHint!).not.toContain("?")
  expect(render([])).toBe("")
  const limited=render(["work_item_read"])
  expect(limited).toContain('/work-items')
  for(const path of ["requirements","conversations","deliveries"])expect(limited).not.toContain(`/${path}`)
 }
})
