import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { AutomationRules } from "./automation"
import { loadMessages } from "../../lib/i18n"
describe("Automation rule capability disclosure", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) it(`keeps truthful preview and executor messages in ${locale}`, async () => {
    const messages = await loadMessages(locale)
    const strings = messages.iam as Record<string, string>
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(["work_item_read", "workflow_manage"]), permissionsResolved: true }} />}><Route index element={<AutomationRules tenant="1" project="99" />} /></Route></Routes></MemoryRouter></IntlAppProvider>)
    expect(html).toContain(strings.automationExecutorHint!)
    expect(html).toContain(strings.automationPreview!)
    expect(html).toContain(strings.automationSave!)
    expect(html).toContain(strings.automationScheduleHint!)
    expect(strings.automationScheduleHint).toContain("cron")
    expect(strings.automationScheduleHint).toContain("timezone")
    expect(strings.automationExecutorHint).toContain("NOTIFY")
	 expect(strings.automationExecutorHint).toContain("ADD_REPORT_ENTRY")
	 expect(strings.automationExecutorHint).toContain("SET_FIELD")
	 expect(strings.automationExecutorHint).toContain("TRANSITION")
	 expect(strings.automationExecutorHint).toContain("to_state_key")
	 expect(strings.automationExecutorHint).toContain("review_sheet.closed")
	 expect(strings.automationExecutorHint).toContain("REQUEST_VERIFICATION")
	 expect(strings.automationExecutorHint).toContain("verification_run")
	 for (const field of ["iteration", "version", "milestone", "risk", "autonomy_level", "estimate_minutes", "test_run.recorded", "ASSIGN", "principal_id", "DISPATCH_TO_AGENT", "EXECUTOR", "CREATE_LINKED_ITEM", "title_template", "{title}", "{number}"]) expect(strings.automationExecutorHint).toContain(field)
	 expect(strings.automationExecutorHint).not.toMatch(/\?{2,}/)
    expect(html.match(/type="submit"/g)?.length).toBe(2)
    expect(strings.automationExecutorHint!.length).toBeGreaterThan(30)
    expect(strings.automationNoExecution!.length).toBeGreaterThan(15)
    expect(strings.automationLogicHint).toContain("60")
    expect(strings.automationTitle).not.toMatch(/\?{2,}/)
  })
})
