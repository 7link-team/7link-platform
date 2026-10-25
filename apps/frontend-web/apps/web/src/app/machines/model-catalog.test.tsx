import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ModelCatalogEditor} from "./model-catalog"
test("manual catalogs explain preservation and provider uncertainty in all locales", async () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><ModelCatalogEditor machineID="1" runtime="CODEX" manage={false}/></IntlAppProvider>)
  expect(html).toContain("CODEX"); expect(html).not.toContain("fleetManualModelsHint"); expect(html).not.toContain("<textarea"); expect(html).not.toContain("<form"); expect(html).not.toContain("????")
 }
})
