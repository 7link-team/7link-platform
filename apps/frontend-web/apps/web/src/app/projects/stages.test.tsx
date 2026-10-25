import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter,RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import type {ProjectDetail} from "../../lib/iam-api"
import {ProjectStages} from "./stages"
test("stage history and terminal read-only surface render without translation leaks in three locales",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){const messages=await loadMessages(locale);const project={project_id:"99",stage:"CLOSED",version:4,archived_at:null} as ProjectDetail
 const router=createMemoryRouter([{path:"/",element:<IntlAppProvider initialLocale={locale} initialMessages={messages}><ProjectStages project={project} onChanged={()=>{}}/></IntlAppProvider>}])
 const html=renderToStaticMarkup(<RouterProvider router={router}/>);expect(html).not.toContain("iam.");expect(html).toContain("section");expect(html).not.toContain('form="project-stage-form"');router.dispose()
 }
})
