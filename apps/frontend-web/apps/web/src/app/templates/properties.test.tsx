import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { WorkflowNodeProperties } from "./properties"
import { newWorkflowNode } from "./workflow"

for(const locale of ["en-US","zh-CN","ms-MY"]) test(`rich node fields have translated labels in ${locale}`, async()=>{
 const messages=await loadMessages(locale), strings=messages.iam as Record<string,string>
 const render=(kind:Parameters<typeof newWorkflowNode>[0])=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><WorkflowNodeProperties node={newWorkflowNode(kind,kind,0)} onChange={()=>{}}/></IntlAppProvider>)
 const loop=render("LOOP")
 for(const key of ["templateLogic","templateLoopBudget","templateLoopIterations","templateEscalateTo"]) {expect(loop).toContain(strings[key]!);expect(strings[key]).not.toContain("??")}
 expect(render("FAN_OUT")).toContain(strings.templateInputPath!)
 expect(render("SUBFLOW")).toContain(strings.templateSubflowVersion!)
 expect(render("TEAM")).toContain(strings.templateMandatory!)
 expect(loop).not.toContain("TRIGGER")
})
