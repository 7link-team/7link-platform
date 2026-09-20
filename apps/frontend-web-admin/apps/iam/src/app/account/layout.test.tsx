import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { AccountContent, AccountNavigation } from "./layout"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import zh from "../../messages/zh-CN.json"

describe("AccountNavigation", () => {
  it("has personal deep links and no business scope controls", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter initialEntries={["/me/mfa"]}>
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
      "files",
      "memberships",
      "identities",
      "closure",
    ])
      expect(html).toContain(`href="/me/${path}"`)
    expect(html).toContain('aria-current="page"')
    expect(html).not.toContain('href="/tenants"')
    expect(html).not.toContain('href="/entities"')
    expect(html).not.toContain("tenant")
    expect(html).toContain("我的文件")
    expect(html).not.toContain("文件管理")
  })

  it("gives the file manager the full workspace without widening account forms", () => {
    const content = (pathname: string) =>
      renderToStaticMarkup(
        <AccountContent pathname={pathname}>content</AccountContent>
      )
    expect(content("/me/files")).toContain("max-w-none")
    expect(content("/me/files/")).toContain("max-w-none")
    expect(content("/me/profile")).toContain("max-w-5xl")
    expect(content("/me/profile")).not.toContain("max-w-none")
  })
})
