import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {MachineHistory} from "./machine-history"
test("machine history has localized permission, loading and bounded navigation states", async () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const messages = await loadMessages(locale)
  for (const kind of ["agents", "history"] as const) {
   const render = (allowed: boolean) => renderToStaticMarkup(<MemoryRouter><IntlAppProvider initialLocale={locale} initialMessages={messages}><MachineHistory tenantID="7" machineID="1" kind={kind} allowed={allowed}/></IntlAppProvider></MemoryRouter>)
   const denied = render(false);expect(denied).not.toContain("<table");expect(denied).not.toContain("fleetHistoryNoAccess")
   const html = render(true);expect(html).toContain('role="status"');expect(html).not.toContain("fleetHostedAgentsHint");expect(html).not.toContain("fleetExecutionHistoryHint");expect(html).toContain("disabled")
  }
 }
})
