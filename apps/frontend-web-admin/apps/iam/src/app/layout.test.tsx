import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../lib/i18n"
import { AuthContext, type AuthContextValue } from "../components/auth"
import type { Session } from "../lib/iam-api"
import AppLayout from "./layout"

const session = {
  subject: "1000000000000000001",
  organization_id: "1000000000000000002",
  tenant_id: "1000000000000000003",
  login_name: "operator",
} as unknown as Session

const auth = {
  session,
  status: "authenticated",
  login: async () => ({}) as never,
  requestPhoneLoginCode: () => Promise.reject(new Error("unused")),
  loginWithPhoneCode: () => Promise.reject(new Error("unused")),
  requestMfaPhoneCode: () => Promise.reject(new Error("unused")),
  verifyLoginMfa: async () => ({}) as never,
  loginWithPasskey: async () => ({}) as never,
  logout: async () => {},
  refresh: async () => {},
} satisfies AuthContextValue

async function renderShell(
  path = "/"
): Promise<{ html: string; openMainNav: string }> {
  const messages = await loadMessages("zh-CN")
  const html = renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <IntlAppProvider initialLocale="zh-CN" initialMessages={messages}>
        <AuthContext.Provider value={auth}>
          <Routes>
            <Route path="/" element={<AppLayout />} />
            <Route path="/admin/:tenantID/users" element={<AppLayout />} />
          </Routes>
        </AuthContext.Provider>
      </IntlAppProvider>
    </MemoryRouter>
  )
  return { html, openMainNav: (messages.iam as { openMainNav: string }).openMainNav }
}

describe("AppLayout shell", () => {
  it("opens the main navigation from a touch-sized button, not a bare trigger", async () => {
    const { html, openMainNav } = await renderShell()
    // The coarse-pointer rule in globals.css only grows a real Button, so the
    // trigger has to render with data-slot="button" to reach 44 px on touch.
    expect(html).toContain(`aria-label="${openMainNav}"`)
    expect(html).toMatch(
      new RegExp(
        `<button[^>]*data-slot="button"[^>]*aria-label="${openMainNav}"` +
          `|<button[^>]*aria-label="${openMainNav}"[^>]*data-slot="button"`
      )
    )
    expect(html).toContain("md:hidden")
  })

  it("never shows a route tenant that requests cannot address", async () => {
    // A non-numeric tenant segment names no tenant, so the shell hands over to
    // the workspace chooser instead of displaying it over another tenant.
    const { html: rejected } = await renderShell("/admin/acme/users")
    expect(rejected).not.toContain("acme")
    expect(rejected).not.toContain("<header")
    const { html: addressed } = await renderShell("/admin/7/users")
    expect(addressed).toContain("<header")
  })
})

// The switcher's tenant list is a membership list. A privileged caller reaches
// a tenant it does not belong to through the account view, not by finding it
// here among its own, so the shell must not widen the list with the platform
// listing -- which returns every tenant in the deployment, cannot be told apart
// from the caller's own once merged, and has no paging or search in a dropdown.
//
// This reads the source because the list is assembled in an effect that calls
// the service, and the shell renders here without one. What it pins is the
// mechanism: re-introducing the platform listing in this file fails it.
describe("workspace switcher tenant source", () => {
  const source = readFileSync(
    new URL("./layout.tsx", import.meta.url),
    "utf8"
  )
  it("offers the caller's own tenants and not the whole platform listing", () => {
    expect(source).toContain("iamApi.myTenants()")
    expect(source).not.toContain("iamApi.tenants(")
  })
  it("decides platform tenant access by permission, not by that listing", () => {
    expect(source).toContain('permissions?.has("tenant_view")')
    expect(source).not.toContain("platformAccess")
  })
  // The switcher offered the tenant perspective to anybody in the tenant area,
  // including an account whose grants live inside one entity. The decision is a
  // function with its own tests; what this pins is that the shell actually hands
  // it over, because the switcher cannot be opened in static markup.
  it("hands the switcher a tenant perspective decision instead of letting it assume one", () => {
    expect(source).toContain("tenantPerspectiveOffered(")
    expect(source).toContain("tenantPerspective={tenantPerspective}")
  })
  // Picking a tenant went straight to that tenant's perspective, which is the
  // same leak as the perspective button: choosing a tenant says nothing about
  // having standing in it. The workspace home is where that is decided.
  it("lets the workspace home decide where a chosen tenant lands", () => {
    expect(source).not.toContain("navigate(`/admin/${encodeURIComponent(next)}`)")
    expect(source).toContain('navigate("/")')
  })
  // Entering an entity used to go to its overview, which reads the entity record
  // itself -- tenant-wide entity administration an entity's own member lacks.
  it("enters an entity on the view the account has rather than always the overview", () => {
    expect(source).toContain("entityWorkspaceView(permissions ?? [])")
    expect(source).not.toContain("/entities/${encodeURIComponent(id)}/overview")
  })
})

// The workspace switcher cannot be opened in static markup, so these pin the
// shape of the decision the shell hands it and the one branch it renders.
describe("workspace switcher tenant selectability", () => {
  const source = readFileSync(
    new URL("../components/workspace-switcher.tsx", import.meta.url),
    "utf8"
  )
  // Not selectable is not the same as not shown: somebody still has to see which
  // tenant their entities belong to. The row stays and stops being a button.
  it("keeps the tenant listed and makes it a heading without tenant standing", () => {
    expect(source).toContain("shownTenants.map((tenant) =>")
    expect(source).toContain("tenantPerspective ? (")
    const rows = source.slice(source.indexOf("shownTenants.map((tenant) =>"))
    const heading = rows.slice(rows.indexOf("                    ) : ("))
    expect(heading).toContain("<h3")
    expect(heading.slice(0, heading.indexOf("</h3>"))).not.toContain("onTenant(")
    expect(heading.slice(0, heading.indexOf("</h3>"))).not.toContain("<button")
  })
  // The perspective button is the other way into the tenant's own shell.
  it("offers the tenant perspective button only with that standing", () => {
    expect(source).toContain("{tenantPerspective && (")
  })
})
