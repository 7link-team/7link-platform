import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { WorkWorkflowTransition } from "./workflow-transition"
import { transitionMessages } from "./workflow-transition-data"
import type { WorkItemDetail } from "../../lib/iam-api"

test("workflow loading preserves the exact current alias and denies unresolved writes in three locales", () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
   const detail = { id: "9007199254740993", state: "READY", state_key: "scheduled", type: "TASK", version: 2 } as WorkItemDetail
   const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><MemoryRouter><WorkWorkflowTransition entityID="99" detail={detail} onUpdated={() => {}} /></MemoryRouter></IntlAppProvider>)
   expect(html).toContain("scheduled")
   expect(html).toContain(transitionMessages[locale].title)
   expect(html).toContain(transitionMessages[locale].loading)
   expect(html).not.toContain('type="submit"')
 }
})
