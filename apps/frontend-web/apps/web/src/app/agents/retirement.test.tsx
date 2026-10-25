import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { RetirementProgress } from "./retirement"
import type { Retirement } from "./retirement-api"

for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  it(`renders genuine retirement facts, loss disclosure and unavailable evidence in ${locale}`, async () => {
    const messages = await loadMessages(locale)
    const labels = messages.iam as Record<string, string>
    const value: Retirement = { id: "81", agent_principal_id: "7", version: 4, agent_version: 15, mode: "FORCED", state: "COMPLETED", step: "DONE", reason: "Machine unrecoverable", precheck: { work_items: [{ assignment_id: "10", work_item_id: "11", project_id: "12", title: "Open task", state: "ACTIVE" }], executions: [], lost: { open_items: 1, running_executions: 0 }, progress: { handover_artifact_id: "9007199254740997", handover_source: "SYSTEM", cleanup_status: "PENDING_RECONNECT", successor_kind: "LATER", reassign_result: [{ work_item_id: "11", status: "RELEASED" }] } } }
    const render = (value: Retirement) => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RetirementProgress value={value} /></IntlAppProvider>)
    const html = render(value)
    expect(html).toContain(labels.agentRetirementForcedWarning!)
    expect(html).toContain(labels.agentRetirementSource_SYSTEM!)
    expect(html).toContain(labels.agentRetirementCleanup_PENDING_RECONNECT!)
    expect(html).toContain("9007199254740997")
    expect(html).toContain("RELEASED")
    expect(html).toContain('aria-current="step"')
    expect(html).toContain('aria-live="polite"')
    const pending = render({ ...value, mode: "NORMAL", state: "IN_PROGRESS", step: "HANDOVER", precheck: { work_items: [], executions: [] } })
    expect(pending).toContain(labels.agentRetirementPending!)
    expect(pending).not.toContain("9007199254740997")
    expect(pending).not.toContain(labels.agentRetirementSource_SYSTEM!)
    expect(pending).not.toContain(labels.agentRetirementForcedWarning!)
    const observed = render({ ...value, mode: "NORMAL", state: "IN_PROGRESS", step: "HANDOVER", step_started_at: "2000-01-01T00:00:00Z", precheck: { work_items: [], executions: [], machine: { machine_id: "9007199254740998", machine_state: "ONLINE", process_status: "STOPPING" } } })
    expect(observed).toContain(labels.agentRetirementMachine!)
    expect(observed).toContain("9007199254740998")
    expect(observed).toContain("ONLINE")
    expect(observed).toContain("STOPPING")
    expect(observed).toContain('role="timer"')
    expect(observed).toContain(labels.agentRetirementHandoverRemaining!.replace("{seconds}", "0"))
    expect(pending).not.toContain('role="timer"')
    expect(html).not.toContain("agentRetirement")
    for (const [key, text] of Object.entries(labels)) if (key.startsWith("agentRetirement")) expect(text).not.toContain("?")
  })
}
