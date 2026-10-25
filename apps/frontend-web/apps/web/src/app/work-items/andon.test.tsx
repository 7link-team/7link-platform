import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import type { WorkItemDetail } from "../../lib/iam-api"
import { WorkAndon, WorkAndonReceipt } from "./andon"

describe("Andon work controls", () => {
  const base = { id: "7", state: "CLAIMED", version: 1, paused_at: null, andon_active: false, current_execution_id: null } as WorkItemDetail
  const render = (codes: string[], detail = base, messages = en) => renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: messages }}><MemoryRouter><Routes>
      <Route element={<Outlet context={{ permissions: new Set(codes), permissionsResolved: true }} />}>
        <Route path="/" element={<WorkAndon entityID="3" detail={detail} onUpdated={() => {}} />} />
      </Route>
    </Routes></MemoryRouter></IntlAppProvider>
  )
  it("only offers authorized pull and explains real runtime limits", () => {
    expect(render([])).not.toContain(en.workAndonPull)
    const markup = render(["andon_pull"], { ...base, current_execution_id: "99" })
    expect(markup).toContain(en.workAndonPull)
    expect(markup).toContain(en.workAndonRuntimePending)
    expect(markup).not.toContain(en.workAndonRuntimeUnavailable)
    expect(markup).toContain("<label")
  })
  it("labels exact command-time controls without inventing a current process acknowledgment", () => {
    for (const messages of [en, zh, ms]) {
      for (const [control, label] of Object.entries({
        PAUSE_REQUESTED: messages.workAndonPauseRequested,
        PAUSED: messages.workAndonPauseAcknowledged,
        RESUME_REQUESTED: messages.workAndonResumeRequested,
        NOT_RUNNING: messages.workAndonNotRunning,
        FUTURE_CONTROL: messages.workAndonRuntimeUnknown,
      })) {
        const markup = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: messages }}>
          <WorkAndonReceipt receipt={{ runner_pause: "REQUESTED", executions: [{ execution_id: "9007199254740993", state: "RUNNING", control }] }} />
        </IntlAppProvider>)
        expect(markup).toContain(label)
        expect(markup).toContain(messages.workAndonReceiptSnapshot)
        expect(markup).toContain("9007199254740993")
        expect(markup).toContain('role="status"')
        if (control !== "PAUSED") expect(markup).not.toContain(messages.workAndonPauseAcknowledged)
      }
      const unavailable = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: messages }}>
        <WorkAndonReceipt receipt={{ runner_pause: "UNAVAILABLE", executions: null }} />
      </IntlAppProvider>)
      expect(unavailable).toContain(messages.workAndonRuntimeUnavailable)
      expect(unavailable).not.toContain(messages.workAndonPauseAcknowledged)
    }
  })
  it("only clears the Andon hold and preserves other pause controls", () => {
    const paused = { ...base, paused_at: "2026-10-21T00:00:00Z", andon_active: true }
    expect(render(["andon_clear"], paused)).toContain(en.workAndonClear)
    expect(render(["andon_pull"], paused)).not.toContain(en.workAndonClear)
    expect(render(["andon_clear"], { ...paused, andon_active: false })).not.toContain(en.workAndonClear)
    expect(render(["andon_pull"], { ...base, state: "ACCEPTED" })).not.toContain(en.workAndonPull)
  })
  it("renders translated reason and action in all supported languages", () => {
    for (const messages of [en, zh, ms]) {
      const markup = render(["andon_pull"], base, messages)
      expect(markup).toContain(messages.workAndonPull)
      expect(markup).toContain(messages.workAndonReason)
      for (const key of Object.keys(en).filter(k => k.startsWith("workAndon") || k.startsWith("workBlock_ANDON"))) {
        expect((messages as Record<string, string>)[key]).toBeTruthy()
        expect((messages as Record<string, string>)[key]).not.toContain("???")
      }
    }
  })
})
