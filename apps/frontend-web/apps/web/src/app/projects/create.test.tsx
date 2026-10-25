import {expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ProjectCreateAction} from "./create"

it("offers project creation only with the current tenant and manage permission", async () => {
  const messages = await loadMessages("en-US")
  for (const tenant of ["", "1"]) for (const allowed of [false, true]) {
    const router = createMemoryRouter([{element:<Outlet context={{permissions:new Set(allowed ? ["project_manage"] : []),permissionsResolved:true}}/>,children:[{path:"/",element:<ProjectCreateAction tenantID={tenant}/> }]}])
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
    expect(html.includes("Create project")).toBe(Boolean(tenant) && allowed)
    router.dispose()
  }
})
