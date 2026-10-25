import {describe, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {UsedKnowledgeContent, UsedKnowledgePanel} from "./used-knowledge"
import {readUsedKnowledge, type UsedKnowledge} from "./source-api"

const data: UsedKnowledge = {available: true, retrieval: {retrieval_log_id: "9007199254740997", mode: "LEXICAL_ENTRIES", rules_omitted: 1, tokens_total: 8, token_budget: 4000, rules: [{entry_id:"1",label:"[R1]",title:"Always verify",body:"Keep review evidence.",content_hash:"a".repeat(64),source_kind:"MEMORY",scope:"TEAM",tokens:4,score_ppm:0,inclusion:"FULL",cited:false,truncated:false}],items:[{entry_id:"2",label:"[K1]",title:"Source <script>",body:"Frozen reference",content_hash:"b".repeat(64),source_id:"9007199254740995",source_kind:"REQUIREMENT",scope:"PROJECT",tokens:4,score_ppm:765432,inclusion:"REFERENCE",cited:true,citation_outcome:"ACCEPTED",truncated:true,source_changed:true}]}}

describe("used knowledge",()=>{
 it("loads exact snowflake execution identity without browser caching",async()=>{
  const original=globalThis.fetch;let requested="";let cache:RequestCache|undefined
  globalThis.fetch=(async(input:RequestInfo|URL,init?:RequestInit)=>{requested=String(input);cache=init?.cache;return new Response(JSON.stringify({code:0,data}),{headers:{"Content-Type":"application/json"}})}) as typeof fetch
  try{expect((await readUsedKnowledge("1","9007199254740993")).available).toBe(true);expect(new URL(requested,"https://local.invalid").pathname).toBe("/api/tenants/1/knowledge/executions/9007199254740993");expect(cache).toBe("no-store")}finally{globalThis.fetch=original}
 })
 for(const locale of ["en-US","zh-CN","ms-MY"] as const){
  it(`shows frozen rules, scores and verified citation outcomes: ${locale}`,async()=>{
   const messages=await loadMessages(locale),words=messages.iam as Record<string,string>
   const router=createMemoryRouter([{path:"/",element:<UsedKnowledgeContent data={data}/>}],{initialEntries:["/"]})
   const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>);router.dispose()
   for(const key of ["usedKnowledgeRules","usedKnowledgeReferences","usedKnowledgeOutcome_ACCEPTED","usedKnowledgeTruncated","usedKnowledgeRulesOmitted","usedKnowledgeSourceChanged"])expect(html).toContain(words[key]!)
   expect(html).toContain("77%");expect(html).toContain("9007199254740995");expect(html).toContain("Source &lt;script&gt;");expect(html).not.toContain("<script>");expect(html).not.toContain("open=")
  })
  it(`hides private retrieval requests without current knowledge read: ${locale}`,async()=>{
   const messages=await loadMessages(locale)
   const router=createMemoryRouter([{element:<Outlet context={{permissions:new Set(),permissionsResolved:true}}/>,children:[{path:"/",element:<UsedKnowledgePanel executionID="3"/>}]}],{initialEntries:["/"]})
   expect(renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)).toBe("");router.dispose()
  })
 }
})

it("prioritizes real structured feedback in compact review and hides absent records",async()=>{
 const messages=await loadMessages("en-US"),words=messages.iam as Record<string,string>
 const reference=data.retrieval!.items[0]!
 const compact:UsedKnowledge={...data,scores_available:false,execution_budget:12000,feedback:[{label:"[F1]",category:"FUNCTIONAL_DEFECT",location:"api",expected:"PASSED",detail:"Keep evidence",source_decision_id:"901",actor_id:"3",decided_at:"2026-10-22T00:00:00Z",cited:true}],retrieval:{...data.retrieval!,items:Array.from({length:7},(_,i)=>({...reference,label:`[K${i+1}]`,title:`Reference ${i+1}`}))}}
 const router=createMemoryRouter([{path:"/",element:<UsedKnowledgeContent data={compact} compact/>}],{initialEntries:["/"]})
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>);router.dispose()
 expect(html).toContain("[F1]");expect(html.indexOf("[F1]")).toBeLessThan(html.indexOf("[K1]"));expect(html).toContain("Reference 3");expect(html).not.toContain("Reference 4");expect(html).toContain(words.usedKnowledgeShowAll!);expect(html).toContain(words.usedKnowledgeScoresUnavailable!);expect(html).not.toContain("77%")
 const emptyRouter=createMemoryRouter([{path:"/",element:<UsedKnowledgeContent data={{available:false}}/>}],{initialEntries:["/"]});expect(renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={emptyRouter}/></IntlAppProvider>)).toBe("");emptyRouter.dispose()
})
