import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { AccountNavigation } from "./layout"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import zh from "../../messages/zh-CN.json"

describe("AccountNavigation", () => {
  it("has personal deep links and no business scope controls", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter initialEntries={["/account/mfa"]}>
        <IntlAppProvider initialLocale="zh-CN" initialMessages={{ iam: zh }}>
          <AccountNavigation />
        </IntlAppProvider>
      </MemoryRouter>
    )
    for (const path of [
      "profile",
      "security",
      "mfa",
      "applications",
      "tokens",
      "memberships",
      "identities",
      "closure",
    ])
      expect(html).toContain(`href="/account/${path}"`)
    expect(html).toContain('aria-current="page"')
    expect(html).not.toContain("/iam/")
    expect(html).not.toContain("tenant")
  })
})
