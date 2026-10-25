// Isolated production-component interaction fixture; domain behavior is tested in Go.
import { useState } from "react"
import { createRoot } from "react-dom/client"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import { TemplateVersionLifecycle } from "../../src/app/templates/version-lifecycle"
import { templatesApi } from "../../src/app/templates/api"
import { softwareDefinition,type TemplateVersion } from "../../src/app/templates/workflow"
import "../../src/index.css"
const query=new URLSearchParams(location.search)
const locale=query.get("locale")??"en-US"
const calls:unknown[]=[]
Object.assign(window,{lifecycleCalls:calls})
const source:TemplateVersion={id:"11",key:"source",state:"PUBLISHED",hash:"original-published-hash",version:2,definition:softwareDefinition(),origin:"FIRST_PARTY"}
let attempts=0
templatesApi.copy=async(tenant,version,key,name,retryKey)=>{
 calls.push({action:"copy",tenant,version,key,name,retryKey})
 if(query.has("retry")&&attempts++===0)throw new Error("Temporary fixture failure; retry the same request")
 return {...source,id:"12",key,name,state:"DRAFT",version:1,origin:"TEAM",based_on_version_id:version,based_on_sha256:source.hash}
}
templatesApi.deprecate=async(tenant,version,retryKey)=>{
 calls.push({action:"deprecate",tenant,version,retryKey})
 return {...source,state:"DEPRECATED",version:3}
}
export function Fixture(){
 const [version,setVersion]=useState(source)
 return <main className="mx-auto max-w-3xl p-6"><h1 className="mb-4 text-xl">Template lifecycle interaction fixture</h1><TemplateVersionLifecycle key={version.id} tenantID="7" version={version} canManage={!query.has("denied")} onChanged={setVersion}/><output id="current-state">{version.id}:{version.state}:{version.hash}</output></main>
}
const messages=await loadMessages(locale)
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><Fixture/></IntlAppProvider>)
