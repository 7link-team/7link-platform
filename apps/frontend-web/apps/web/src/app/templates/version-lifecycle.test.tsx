import { expect,test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { TemplateVersionLifecycle } from "./version-lifecycle"
import { softwareDefinition,type TemplateVersion } from "./workflow"

for(const locale of ["en-US","zh-CN","ms-MY"])test(`template lifecycle preserves provenance and state restrictions in ${locale}`,async()=>{
 const messages=await loadMessages(locale)
 const strings=messages.iam as Record<string,string>
 const version:TemplateVersion={id:"12",key:"team",name:"Team workflow",state:"PUBLISHED",hash:"snapshot-hash",version:2,definition:softwareDefinition(),origin:"TEAM",based_on_version_id:"11",based_on_sha256:"source-published-hash"}
 const render=(state:string,canManage:boolean)=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><TemplateVersionLifecycle tenantID="7" version={{...version,state}} canManage={canManage} onChanged={()=>{}}/></IntlAppProvider>)
 const published=render("PUBLISHED",true)
 expect(published).toContain(strings.templateCopy!)
 expect(published).toContain(strings.templateDeprecate!)
 expect(published).toContain(strings.templateDeprecateConfirm!)
 expect(published).toContain("source-published-hash")
 expect(published).toContain('type="checkbox"')
 const deprecated=render("DEPRECATED",true)
 expect(deprecated).toContain(strings.templateDeprecatedNotice!)
 expect(deprecated).toContain(strings.templateCopy!)
 expect(deprecated).not.toContain(strings.templateDeprecateConfirm!)
 const draft=render("DRAFT",true)
 expect(draft).not.toContain('<form')
 expect(draft).not.toContain('type="checkbox"')
 const readonly=render("PUBLISHED",false)
 expect(readonly).toContain("source-published-hash")
 expect(readonly).not.toContain('<form')
 expect(readonly).not.toContain('type="checkbox"')
})
