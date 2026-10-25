import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import { SourceDisposition } from "./source-disposition"
import type { IntakeLedgerSource } from "./intake-api"

describe("source disposition controls", () => {
  const base = { id: "7", project_id: "3", kind: "NEW", state: "RECEIVED", version: 1 } as IntakeLedgerSource
  const render = (codes: string[], source = base, messages = en) => renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: messages }}><MemoryRouter><Routes>
      <Route element={<Outlet context={{ permissions: new Set(codes), permissionsResolved: true }} />}>
        <Route path="/" element={<SourceDisposition project="3" source={source} onSaved={() => {}} />} />
      </Route>
    </Routes></MemoryRouter></IntlAppProvider>
  )
  it("uses separate merge and Human decline permissions", () => {
    expect(render([])).toBe("")
    expect(render(["intake_convert"])).toContain(en.sourceDispositionMerge)
    expect(render(["intake_convert"])).not.toContain(`<option value="DECLINE">`)
    expect(render(["intake_decline"])).toContain(en.sourceDispositionDecline)
    expect(render(["intake_decline"])).not.toContain(`<option value="MERGE">`)
  })
  it("preserves terminal and structured rework flows", () => {
    for (const patch of [{ kind: "REWORK" }, { state: "CONVERTED" }, { state: "MERGED" }, { state: "DECLINED" }])
      expect(render(["intake_convert", "intake_decline"], { ...base, ...patch })).toBe("")
  })
  it("allows disposition of active clarification through the canonical round owner", () => {
    expect(render(["intake_convert"], { ...base, state: "CLARIFYING" })).toContain(en.sourceDispositionMerge)
  })
  it("keeps recorded dispositions readable without reopening mutation controls", () => {
    const declined = render([], { ...base, state: "DECLINED", decline_reason_code: "OTHER", decline_reason: "Outside the confirmed scope", client_message: "Recorded explanation" })
    expect(declined).toContain("Outside the confirmed scope")
    expect(declined).toContain("Recorded explanation")
    expect(declined).not.toContain("<form")
    expect(declined).toContain(en.sourceDispositionHint)
    const merged = render([], { ...base, state: "MERGED", merged_into_intake_id: "19" })
    expect(merged).toContain(en.sourceDispositionTarget)
    expect(merged).toContain("19")
    expect(merged).not.toContain("<form")
  })
  it("discloses the recorded-message delivery limit in three languages", () => {
    for (const messages of [en, zh, ms]) {
      expect(render(["intake_convert"], base, messages)).toContain(messages.sourceDispositionHint)
      for (const key of Object.keys(en).filter(k => k.startsWith("sourceDisposition"))) {
        expect((messages as Record<string, string>)[key]).toBeTruthy()
        expect((messages as Record<string, string>)[key]).not.toContain("???")
      }
    }
  })
  it("shows actual delivery state and offers a current read receipt only for posted messages", () => {
    for (const messages of [en, zh, ms]) {
      const posted = render(["scope_change"], {...base, state:"MERGED", merged_into_intake_id:"19", disposition_delivery:{state:"POSTED", message_id:"88"}}, messages)
      expect(posted).toContain(messages.sourceDispositionReceipt_POSTED)
      expect(posted).toContain(messages.sourceDispositionReceiptRefresh)
      expect(posted).toContain("88")
      const unavailable = render(["scope_change"], {...base, state:"DECLINED", decline_reason:"Outside scope", disposition_delivery:{state:"UNAVAILABLE"}}, messages)
      expect(unavailable).toContain(messages.sourceDispositionReceipt_UNAVAILABLE)
      expect(unavailable).not.toContain(messages.sourceDispositionReceiptRefresh)
    }
  })
})
