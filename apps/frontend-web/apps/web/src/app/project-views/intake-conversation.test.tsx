import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import { IntakeConversation, IntakeSuggestions } from "./intake-conversation"
import type { IntakeLedgerSource } from "./intake-api"
import { intakeThreadReply } from "./intake-api"

describe("original Intake thread and internal suggestions", () => {
  const source = { id: "7", project_id: "3", kind: "NEW", state: "RECEIVED", version: 1, raw_content: "Private original source", raw_content_hash: "a".repeat(64) } as IntakeLedgerSource
  const render = (suggestions: boolean, messages = en) => renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: messages }}>
      {suggestions ? <IntakeSuggestions project="3" source={source} /> : <IntakeConversation project="3" source={source} />}
    </IntlAppProvider>,
  )

  it("requires exact positive channel/root/contact IDs and bounds client replies", () => {
    const html = render(false)
    expect(html.match(/pattern="\[1-9\]\[0-9\]\{0,18\}"/g)).toHaveLength(3)
    expect(html).toContain('maxLength="2000"')
    expect(html).toContain('disabled=""')
    expect(html).toContain(en.intakeConversationHint)
    expect(html).not.toContain(source.raw_content!)
  })

  it("shows a pending source read without inventing suggestions or completion", () => {
    const html = render(true)
    expect(html).toContain('role="status"')
    expect(html).toContain(en.intakeSuggestionsHint)
    expect(html).not.toContain("<article")
    expect(html).not.toContain(en.intakeSuggestionsEmpty)
    expect(html).not.toContain(source.raw_content!)
  })

  it("preserves opaque work IDs and requires an exact positive work version", () => {
    expect(intakeThreadReply("Question", "", "")).toEqual({ body: "Question" })
    expect(intakeThreadReply("Question", "9007199254740993", "3")).toEqual({ body: "Question", work_item_id: "9007199254740993", work_version: 3 })
    for (const [work, version] of [["90", ""], ["", "3"], ["90", "0"], ["90", "1.5"], ["90", "9007199254740993"], ["bad", "3"]]) {
      expect(() => intakeThreadReply("Question", work!, version!)).toThrow()
    }
  })

  it("offers original-thread work linkage only for a converted requirement", () => {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: en }}><IntakeConversation project="3" source={{ ...source, state: "CONVERTED", requirement_id: "100" }} /></IntlAppProvider>)
    expect(html).toContain(en.intakeConversationWorkHint)
    expect(html).toContain(en.intakeConversationWorkVersion)
    expect(render(false)).not.toContain(en.intakeConversationWorkHint)
  })

  it("forwards stored client replies for merged sources instead of composing replacement content", () => {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: en }}><IntakeConversation project="3" source={{ ...source, state: "MERGED" }} /></IntlAppProvider>)
    expect(html).toContain(en.intakeConversationForwardHint)
    expect(html).toContain(en.intakeConversationMessage)
    expect(html).toContain(en.intakeConversationForward)
    expect(html).not.toContain("textarea")
    expect(html).not.toContain(source.raw_content!)
  })

  it("provides thread, provenance and recommendation language in all supported locales", () => {
    for (const messages of [en, zh, ms]) {
      expect(render(false, messages)).toContain(messages.intakeConversationTitle)
      expect(render(true, messages)).toContain(messages.intakeSuggestionsTitle)
      for (const key of Object.keys(en).filter(key => key.startsWith("intakeConversation") || key.startsWith("intakeSuggestion"))) {
        const value = (messages as Record<string, string>)[key]
        expect(value).toBeTruthy()
        expect(value).not.toContain("???")
      }
    }
  })
})
