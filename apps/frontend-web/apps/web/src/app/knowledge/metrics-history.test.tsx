import {describe, expect, it} from "bun:test"
import {qualityRate} from "./metrics-format"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {MetricsHistory} from "./metrics-history"
import {iamApi} from "../../lib/iam-api"

describe("knowledge evidence metrics",()=>{
 it("distinguishes missing samples from a measured zero",()=>{
  expect(qualityRate({denominator:0},"unavailable")).toBe("unavailable")
  expect(qualityRate({denominator:4,value:0},"unavailable")).toBe("0.0% (4)")
  expect(qualityRate({denominator:2,value:0.5},"unavailable")).toBe("50.0% (2)")
 })
 it("requests the real scoped metrics and bounded history endpoints",async()=>{
  const original=globalThis.fetch;const urls:string[]=[]
  globalThis.fetch=(async(input:RequestInfo|URL)=>{urls.push(String(input));return new Response(JSON.stringify({code:0,data:{items:[],total:0}}),{headers:{"Content-Type":"application/json"}})}) as typeof fetch
  try{
   await iamApi.knowledgeMetrics("1",{from:"2026-10-01T00:00:00Z",to:"2026-10-22T00:00:00Z",project_id:"3"})
   await iamApi.retrievalHistory("1",{scope:"PROJECT",project_id:"3",search:"amount%",offset:50,limit:50})
   const metrics=new URL(urls[0]!,"https://local.invalid");expect(metrics.pathname).toBe("/api/tenants/1/knowledge-metrics");expect(metrics.searchParams.get("project_id")).toBe("3")
   const history=new URL(urls[1]!,"https://local.invalid");expect(history.pathname).toBe("/api/tenants/1/knowledge/retrieval-debug/history");expect(history.searchParams.get("offset")).toBe("50");expect(history.searchParams.get("search")).toBe("amount%")
  }finally{globalThis.fetch=original}
 })
})

for(const locale of ["en-US","zh-CN","ms-MY"] as const){
 it(`offers metrics only to current knowledge readers and history only to managers: ${locale}`,async()=>{
  const messages=await loadMessages(locale);const words=messages.iam as Record<string,string>
  for(const permissions of [[],["knowledge_read"],["knowledge_read","knowledge_manage"]]){
   const router=createMemoryRouter([{element:<Outlet context={{permissions:new Set(permissions),permissionsResolved:true}}/>,children:[{path:"/",element:<MetricsHistory tenant="1" project="3" onSelect={()=>{}}/>}]}],{initialEntries:["/"]})
   const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>);router.dispose()
   expect(html.includes(words.knowledgeQualityTitle!)).toBe(permissions.includes("knowledge_read"))
   expect(html.includes(words.knowledgeHistoryTitle!)).toBe(permissions.includes("knowledge_manage"))
  }
 })
}
