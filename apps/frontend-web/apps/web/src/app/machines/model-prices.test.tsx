import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ModelPrices} from "./model-prices"
test("model price controls disclose units and unknown prices in three locales and honor read-only authority", async () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const messages = await loadMessages(locale)
  const render = (manage: boolean) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ModelPrices tenantID="9007199254740993" manage={manage}/></IntlAppProvider>)
  const readonly = render(false);expect(readonly).not.toContain("<form");expect(readonly).not.toContain("fleetPricesHint");expect(readonly).toContain('role="status"')
  const managed = render(true);expect(managed).toContain("<form");expect(managed).toContain('inputMode="numeric"');expect(managed).not.toContain("fleetPrice_input_micros_per_mtok")
 }
})
