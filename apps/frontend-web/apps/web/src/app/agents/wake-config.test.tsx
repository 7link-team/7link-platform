import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { AgentWakeConfig } from "./wake-config"
import { loadMessages } from "../../lib/i18n"

describe("Agent wake configuration", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    it(`explains receiver verification and shows no fabricated secret in ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><AgentWakeConfig tenant="1" agent="7" /></IntlAppProvider>)
      const strings = messages.iam as Record<string, string>
      expect(html).toContain(strings.agentWakeConfig!)
      expect(html).toContain("300")
      expect(html).toContain(strings.agentRefresh!)
      expect(html).not.toContain("select-all")
      expect(strings.agentWakeConfig).not.toMatch(/\?{2,}/)
    })
  }
})
