import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { PerformanceFacts } from "./performance"
import type { AgentPerformance, PerformanceMeasures } from "./performance-api"

for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  it(`shows genuine null, small sample, >100% rework and live load in ${locale}`, async () => {
    const messages = await loadMessages(locale)
    const labels = messages.iam as Record<string, string>
    const row: PerformanceMeasures = { work_item_type: "BUG", fpa_n: 4, first_pass_accepts: 3, fpa: null, claims: 2, rework_decisions: 3, rework_rate: 1.5, accepted_items: 1, cost_on_accepted_micros: null, cpa_micros: null }
    const value: AgentPerformance = { ...row, window_days: 30, computed_at: "2026-10-23T10:00:00Z", scope: "CURRENT_READER_VISIBLE_WORK_AND_EXECUTIONS", by_type: [row], load: { active_items: 1, max_parallel_items: 4, running_executions: 2 } }
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><PerformanceFacts value={value}/></IntlAppProvider>)
    expect(html).toContain(labels.agentStatsScope!)
    expect(html).toContain(labels.agentStatsCostHint!)
    expect(html).toContain(labels.agentStatsSmallSample!.replace("{n}", "4"))
    expect(html).toContain("150.0%")
    expect(html).toContain("1/4")
    expect(html).toContain("—")
    expect(html).toContain('scope="col"')
    expect(html).toContain('dateTime="2026-10-23T10:00:00Z"')
    expect(html).not.toContain("agentStats")
    for (const [key, text] of Object.entries(labels)) if (key.startsWith("agentStats") || key === "agentTab_performance") expect(text).not.toContain("?")
  })
}
