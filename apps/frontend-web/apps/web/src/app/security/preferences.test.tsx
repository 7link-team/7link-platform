import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { ProfilePreferences } from "./preferences"

it("keeps profile preferences translated and routes per-type settings to the existing center", async () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const messages = await loadMessages(locale)
    const labels = messages.iam as Record<string, string>
    for (const tenant of ["", "0", "9007199254740993"]) {
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><ProfilePreferences principal="9007199254740995" tenant={tenant} /></MemoryRouter></IntlAppProvider>)
      expect(html).toContain(labels.profilePreferencesTitle!)
      expect(html).toContain(labels.profilePreferencesSystem!)
      expect(html).toContain('href="/user/notifications"')
      expect(html).not.toContain("profilePreferencesTitle")
      if (!tenant || tenant === "0") {
        expect(html).toContain(labels.notificationChooseWorkspace!)
        expect(html).not.toContain("<form")
      } else {
        expect(html).toContain(labels.notificationLoading!)
        expect(html).toContain(tenant)
      }
    }
  }
})
