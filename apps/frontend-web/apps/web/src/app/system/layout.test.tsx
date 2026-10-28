import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import type { EffectiveMenu } from "../../lib/iam-api"
import { AdministrationWorkspaceHome } from "./layout"

it("shows accessible administration actions and their purpose instead of choosing an arbitrary page", async () => {
  const menus: EffectiveMenu[] = [
    { id: "accounts", label: "Accounts", path: "/system/accounts", permission_code: "system_account_view", sort_order: 1 },
    { id: "tenants", label: "Tenants", path: "/system/tenants", permission_code: "tenant_view", sort_order: 2 },
  ]
  const router = createMemoryRouter([{
    element: <Outlet context={{ menus, permissionsResolved: true }} />,
    children: [{ path: "/system", element: <AdministrationWorkspaceHome /> }],
  }], { initialEntries: ["/system"] })
  try {
    const messages = await loadMessages("en-US")
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
    expect(html).toContain('href="/system/accounts"')
    expect(html).toContain('href="/system/tenants"')
    expect(html).toContain("Create and find accounts")
    expect(html).toContain("assign administrators")
    expect(html).not.toContain('href="/system/jobs"')
    expect(html).not.toContain('href="/system/settings"')
    expect(router.state.location.pathname).toBe("/system")
  } finally {
    router.dispose()
  }
})
