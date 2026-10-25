import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import IntegrationsPage from "./page"
import { integrationMessages } from "./messages"
function render(
  locale: keyof typeof integrationMessages,
  permissions: string[],
  resolved = true
) {
  return renderToStaticMarkup(
    <IntlAppProvider initialLocale={locale} initialMessages={ui}>
      <MemoryRouter initialEntries={["/1/integrations"]}>
        <Routes>
          <Route
            element={
              <Outlet
                context={{
                  permissions: new Set(permissions),
                  permissionsResolved: resolved,
                }}
              />
            }
          >
            <Route
              path="/:tenantID/integrations"
              element={<IntegrationsPage />}
            />
          </Route>
        </Routes>
      </MemoryRouter>
    </IntlAppProvider>
  )
}
describe("integration configuration boundaries", () => {
  it("shows explicit unavailable and loading, never remote success", () => {
    for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
      const html = render(locale, ["integration_manage"])
      expect(html).toContain(integrationMessages[locale].unavailable)
      expect(html).toContain(integrationMessages[locale].loading)
      expect(html).not.toContain("ACTIVE")
      expect(html).not.toContain("SUCCEEDED")
    }
  })
  it("hides administration until permission resolution and denies unauthorized viewers", () => {
    const denied = render("en-US", [])
    expect(denied).toContain(integrationMessages["en-US"].forbidden)
    expect(denied).not.toContain(integrationMessages["en-US"].add)
    const unresolved = render("en-US", ["integration_manage"], false)
    expect(unresolved).toContain(integrationMessages["en-US"].loading)
    expect(unresolved).not.toContain(integrationMessages["en-US"].add)
  })
  it("ships matching locale keys with explicit unavailable language", () => {
    const keys = Object.keys(integrationMessages["en-US"])
    expect(Object.keys(integrationMessages["zh-CN"])).toEqual(keys)
    expect(Object.keys(integrationMessages["ms-MY"])).toEqual(keys)
  })
  it("renders readable Chinese labels instead of encoding placeholders", () => {
    const html = render("zh-CN", ["integration_manage"])
    expect(html).toContain("云效集成")
    expect(html).toContain("添加连接")
    expect(html).toContain("不可用：远程 API")
    for (const messages of Object.values(integrationMessages)) {
      for (const value of Object.values(messages)) {
        expect(value).not.toMatch(/\?{2,}/)
      }
    }
  })
})
