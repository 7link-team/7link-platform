import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { WorkCodeLinks } from "./code-links"
import { loadMessages } from "../../lib/i18n"
import type { WorkItemDetail } from "../../lib/iam-api"

describe("Code references", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    it(`explains reference limits in ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const strings = messages.iam as Record<string, string>
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><WorkCodeLinks entityID="99" detail={{ id: "139", version: 1 } as WorkItemDetail} /></IntlAppProvider>)
      expect(html).toContain(strings.codeLinksTitle!)
      expect(html).toContain(strings.codeLinksHint!)
      expect(html).not.toContain("LOCAL_GIT_OBSERVED")
      expect(strings.codeLinksTitle).not.toMatch(/\?{2,}/)
    })
  }
})
