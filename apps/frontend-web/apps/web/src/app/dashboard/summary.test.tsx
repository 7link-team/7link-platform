import {afterEach, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {iamApi} from "../../lib/iam-api"
import {WorkbenchSummary} from "./summary"

const nativeFetch = globalThis.fetch
afterEach(() => {globalThis.fetch = nativeFetch})

for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) it(`shows translated loading without invented home metrics: ${locale}`, async () => {
  const messages = await loadMessages(locale)
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><WorkbenchSummary tenantID="1"/></MemoryRouter></IntlAppProvider>)
  expect(html).toContain((messages.iam as Record<string, string>).workbenchSummaryTitle!)
  expect(html).toContain('role="status"')
  expect(html).not.toContain("<dd")
  expect(html).not.toContain("50%")
})

it("requests the generated tenant-scoped read route with dates and cancellation", async () => {
  let url = ""
  let init: RequestInit | undefined
  const controller = new AbortController()
  globalThis.fetch = (async (input: RequestInfo | URL, options?: RequestInit) => {
    url = String(input); init = options
    return Response.json({code: 0, data: {projects: 2, execution_cost_micros: null, missing_sources: ["EXECUTION_COST"]}})
  }) as typeof fetch
  const result = await iamApi.workbenchSummary("17", {from: "2026-10-01", to: "2026-10-21"}, controller.signal)
  expect(url).toContain("/tenants/17/workbench-summary?")
  expect(url).toContain("from=2026-10-01")
  expect(url).toContain("to=2026-10-21")
  expect(init?.signal).toBe(controller.signal)
  expect(init?.method).toBe("GET")
  expect(init?.body).toBeUndefined()
  expect(result.projects).toBe(2)
  expect(result.execution_cost_micros).toBeNull()
})
