import { expect,test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { TemplateLibrary } from "./library"

for(const locale of ["en-US","zh-CN","ms-MY"])test(`template library restores filter controls from URL in ${locale}`,async()=>{
 const messages=await loadMessages(locale),strings=messages.iam as Record<string,string>
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter initialEntries={["/?library_domain=VIDEO&library_origin=FIRST_PARTY&library_state=PUBLISHED&library_team_mode=HUMAN_ONLY&library_key=video"]}><TemplateLibrary tenantID="7" onSelect={()=>{throw new Error("unexpected select")}}/></MemoryRouter></IntlAppProvider>)
 for(const key of ["templateLibrary","templateLibrary_domain","templateLibrary_origin","templateLibrary_state","templateLibrary_team_mode","templateVersionHistory"])expect(html).toContain(strings[key]!)
 for(const value of ["VIDEO","FIRST_PARTY","PUBLISHED","HUMAN_ONLY"])expect(html).toContain(`value="${value}" selected=""`)
 expect(html).toContain('role="status"')
 expect(html).not.toContain("templateLibrary_domain_")
})
