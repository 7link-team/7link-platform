import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import PerformancePage from "./page"
for(const locale of ["en-US","zh-CN","ms-MY"])test(`assessment owner boundary is explicit in ${locale}`,async()=>{
 const messages=await loadMessages(locale),strings=messages.iam as Record<string,string>
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><PerformancePage/></MemoryRouter></IntlAppProvider>)
 expect(html).toContain(strings.performanceTitle!);expect(html).toContain(strings.performanceOwnerOnly!);expect(html).toContain('role="alert"');expect(html).not.toContain(strings.performanceCreate!)
 for(const[key,value]of Object.entries(strings).filter(([key])=>key.startsWith("performance"))){expect(value).not.toBe(key);expect(value).not.toMatch(/^\?+$/)}
})
