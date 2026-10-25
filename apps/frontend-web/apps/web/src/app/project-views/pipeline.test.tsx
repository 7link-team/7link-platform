import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {PipelineFacts} from "./pipeline"
import type {PipelineRun} from "./pipeline-api"

test("pipeline list and graph preserve exact attempts, failures and source refs in all locales",async()=>{
 const items:PipelineRun[]=[{id:"verification:99",project_id:"3",work_item_id:"9007199254740993",result_id:"21",stage:"TESTER",internal_round:2,attempt:3,state:"FAILED",created_at:"2026-10-22T00:00:00Z",started_at:null,ended_at:null,execution_id:"40",verification_id:"99",decision_id:null,delivery_id:null,evidence_refs:["receipt-sha256:abc"]}]
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  const messages=await loadMessages(locale)
  for(const view of ["list","graph"] as const){
   const html=renderToStaticMarkup(<MemoryRouter><IntlAppProvider initialLocale={locale} initialMessages={messages}><PipelineFacts items={items} view={view} tenant="1" project="3"/></IntlAppProvider></MemoryRouter>)
   expect(html).toContain("9007199254740993");expect(html).toContain("receipt-sha256:abc");expect(html).toContain("/entities/3/executions/40");expect(html).not.toContain("pipelineState_");expect(html).not.toContain("pipelineStage_");expect(html).not.toContain("????")
  }
 }
})
