import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { ExecutionProgress } from "./progress"

for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) test(`progress distinguishes measured counters, pending ACK and unconfigured in ${locale}`, async () => {
 const messages = await loadMessages(locale)
 const render = (state: string, configured: boolean) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ExecutionProgress state={state} policy={configured ? {no_progress_limit: 3, dependency_miss_limit: 2} : undefined} progress={{no_progress_count: 3, dependency_miss_count: 0, stop_reason: "AUTONOMY_NO_PROGRESS", similarity_union: 5, similarity_ratio_ppm: 40000}} templateVersion="9007199254740993"/></IntlAppProvider>)
 const pending = render("RUNNING", true), paused = render("PAUSED", true), unconfigured = render("RUNNING", false)
 expect(pending).toContain("3 / 3"); expect(pending).toContain("4%"); expect(pending).toContain("9007199254740993"); expect(pending).toContain('role="status"'); expect(pending).not.toBe(paused); expect(unconfigured).not.toContain("3 / 3"); expect(pending).not.toContain("executionProgressPausePending")
})
