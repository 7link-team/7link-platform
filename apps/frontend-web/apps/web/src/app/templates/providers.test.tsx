import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ProviderDetails,ProviderHTTPSetup} from "./providers"
import type {Provider} from "./provider-api"
for(const locale of ["en-US","zh-CN","ms-MY"])test(`provider protocol setup and honest availability ${locale}`,async()=>{
 const messages=await loadMessages(locale)
 const row={id:"9007199254740993",kind:"VIDEO_GEN",adapter_key:"video_gen_http_v1",display_name:"Configured service",config:{endpoint:"https://example.invalid",model:"selected-model"},has_secret:true,machine_id:null,state:"UNAVAILABLE",limitations:"Service offline",license_notice:"Actual service licence",region_notice:"Configured region",license_accepted_by:"11",license_accepted_at:"2026-10-23",version:3} as Provider
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ProviderDetails row={row}/></IntlAppProvider>)
 for(const value of ["provider-v1","verifier-v1","endpoint","model","Service offline","9007199254740993","Actual service licence"])expect(html).toContain(value)
 expect(html).not.toContain("templateProviderHTTPSetup")
 expect(renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ProviderHTTPSetup adapterKey="another-adapter"/></IntlAppProvider>)).toBe("")
})
