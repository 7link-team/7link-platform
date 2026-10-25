import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { AuthContext, type AuthContextValue } from "../../components/auth"
import { devLoginAccounts } from "../../lib/dev-login-accounts"
import { loadMessages } from "../../lib/i18n"
import Workspaces from "./workspaces"

const auth: AuthContextValue = {
  status: "authenticated",
  session: {subject: "9"} as AuthContextValue["session"],
  login: async () => ({}) as never,
  requestPhoneLoginCode: async () => ({}) as never,
  loginWithPhoneCode: async () => ({}) as never,
  requestMfaPhoneCode: async () => ({}) as never,
  verifyLoginMfa: async () => ({}) as never,
  loginWithPasskey: async () => ({}) as never,
  logout: async () => {}, refresh: async () => {},
}

it("keeps sign-in entrance tabs off the authenticated picker even with development personas enabled", async () => {
  const messages = await loadMessages("en-US")
  const count = devLoginAccounts.length
  devLoginAccounts.push({id: "fixture", role: "fixture", loginName: "fixture", password: "fixture"})
  const router = createMemoryRouter([{path: "/admin/workspaces", element: <Workspaces />}], {initialEntries: ["/admin/workspaces"]})
  try {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><AuthContext.Provider value={auth}><RouterProvider router={router} /></AuthContext.Provider></IntlAppProvider>)
    expect(html).toContain("Select workspace")
    for (const entry of ["system", "platform", "admin", "user"]) expect(html).not.toContain(`href="/${entry}/login"`)
  } finally {
    devLoginAccounts.splice(count)
    router.dispose()
  }
})

it("shows pending navigation while the selected tenant loader waits", async () => {
  const messages = await loadMessages("en-US")
  let complete: (() => void) | undefined
  const router = createMemoryRouter([
    {path: "/admin/workspaces", element: <Workspaces />},
    {path: "/admin/7", element: <p>Destination</p>, loader: () => new Promise<null>(resolve => {complete = () => resolve(null)})},
  ], {initialEntries: ["/admin/workspaces"]})
  const navigation = router.navigate("/admin/7")
  try {
    expect(router.state.navigation.state).toBe("loading")
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><AuthContext.Provider value={auth}><RouterProvider router={router} /></AuthContext.Provider></IntlAppProvider>)
    expect(html).toContain('aria-busy="true"')
    expect(html).not.toContain("Select workspace")
  } finally {
    complete?.()
    await navigation
    router.dispose()
  }
})
