import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { WorkPreparation } from "./preparation"
import { loadMessages } from "../../lib/i18n"
import type { WorkItemDetail } from "../../lib/iam-api"

describe("automated draft contract confirmation", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    it(`requires an explicit unchecked confirmation in ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const detail = {
        id: "7", state: "DRAFT", version: 1, criteria: [], contract: null,
        preparation: { risk: "MEDIUM", autonomy_level: "MEDIUM", out_of_scope: [], out_of_scope_confirmed: false, context_refs: [], verification_plan: [], ok: false, issues: [], block_reasons: [] },
      } as unknown as WorkItemDetail
      const html = renderToStaticMarkup(
        <IntlAppProvider initialLocale={locale} initialMessages={messages}>
          <MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(["work_item_read", "work_item_write"]), permissionsResolved: true }} />}>
            <Route index element={<WorkPreparation entityID="3" detail={detail} onUpdated={() => {}} />} />
          </Route></Routes></MemoryRouter>
        </IntlAppProvider>
      )
      const label = (messages.iam as Record<string, string>).workConfirmDraftContract!
      expect(html).toContain(renderToStaticMarkup(<>{label}</>))
      expect(html).toContain('aria-checked="false"')
      expect(html).not.toContain('aria-checked="true"')
      expect(html).not.toMatch(/<input[^>]*\schecked(?:=|\s|>)/)
    })
  }
})
