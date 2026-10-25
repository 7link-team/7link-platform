import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { DirectProgressSourcePicker } from "./client"
import { ClientPickerScope } from "./client-picker-scope"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"

describe("pre-result source selection", () => {
  it("offers work and project files in every locale without requiring accepted evidence", () => {
    for (const [locale, messages] of [["en-US", en], ["zh-CN", zh], ["ms-MY", ms]] as const) {
      const html = renderToStaticMarkup(
        <IntlAppProvider initialLocale={locale} initialMessages={{ iam: messages }}>
          <ClientPickerScope.Provider value={{ project: "3", collection: "previews" }}>
            <DirectProgressSourcePicker name="source" config={{}} required disabled={false} value={undefined} onChange={() => {}} labelId="source-label" />
          </ClientPickerScope.Provider>
        </IntlAppProvider>
      )
      expect(html).toContain(messages.clientChooseProgressWork)
      expect(html).toContain(messages.clientSourceFiles)
      expect(html).not.toContain(messages.clientChooseWork)
      expect(html).toContain('aria-labelledby="source-label"')
    }
  })
})
