import {afterEach,expect,spyOn,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {workflowAssistantApi} from "./api"
import {iamApi} from "../../lib/iam-api"
import {WorkflowAssistant,WorkflowServerIssues} from "./workflow-assistant"
import {softwareDefinition,type TemplateVersion} from "./workflow"

let restore:(()=>void)|undefined
afterEach(()=>{restore?.();restore=undefined})
const version={id:"9007199254740993",key:"workflow",state:"DRAFT",hash:"a".repeat(64),version:3,definition:softwareDefinition()} as TemplateVersion
test("workflow generation is a proposal request with exact base, model provider and stable retry key",async()=>{
 const calls:{route:{method:string;url:string};init?:RequestInit}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never});restore=()=>spy.mockRestore()
 const signal=new AbortController().signal
 await workflowAssistantApi.generate("7",version,"9007199254740995","Add explicit test work","retry-key",signal)
 await workflowAssistantApi.generate("7",version,"9007199254740995","Add explicit test work","retry-key",signal)
 expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method:"POST",url:"/tenants/7/templates/9007199254740993/workflow-proposals"})
 expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({base_hash:version.hash,provider_id:"9007199254740995",instruction:"Add explicit test work"})
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key":"retry-key"})
 await workflowAssistantApi.validate("7",version,version.definition.workflow,signal)
 expect(calls[2]?.route.url).toBe("/tenants/7/templates/9007199254740993/workflow-validations")
 expect(JSON.parse(calls[2]?.init?.body as string)).toEqual({base_hash:version.hash,workflow:version.definition.workflow})
 expect(calls.every(call=>call.init?.signal===signal)).toBe(true)
 expect(calls.some(call=>call.route.url.endsWith("/patches")||call.route.url.endsWith("/publications"))).toBe(false)
})
for(const locale of ["en-US","zh-CN","ms-MY"])test(`workflow model review and server diagnostics ${locale}`,async()=>{
 const messages=await loadMessages(locale)
 const wrap=(child:React.ReactNode)=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}>{child}</IntlAppProvider>)
 const html=wrap(<WorkflowAssistant tenantID="7" version={version} workflow={version.definition.workflow} canEdit={false} onProposal={()=>{throw new Error("render must not apply")}}/>)
 expect(html).toContain("<fieldset disabled");expect(html).toContain("LLM_JUDGE");expect(html).toContain("workflow-proposals");expect(html).not.toContain("templateAssistantNotice")
 const issues=wrap(<WorkflowServerIssues validation={{valid:false,issues:[{path:"workflow.nodes[3].subflow",code:"templateIssueSubflow"},{path:"workflow.inherited_gates[team]",code:"templateIssueInherited"}]}}/>)
 expect(issues).toContain("workflow.nodes[3].subflow");expect(issues).toContain("workflow.inherited_gates[team]");expect(issues).not.toContain("templateIssueInherited")
 expect(wrap(<WorkflowServerIssues validation={{valid:true,issues:[]}}/>)).not.toContain("templateServerValid")
})
