import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import type { WorkItemDetail } from "../../lib/iam-api"
import { WorkCalendar, WorkSchedule } from "./schedule"
import { WorkUpstreamArtifacts } from "./upstream"
describe("scheduling and upstream controls", () => {
  const base = { id: "7", state: "CLAIMED", version: 1, scheduled_start: null, scheduled_end: null } as WorkItemDetail
  const render = (codes: string[], messages = en, state = "CLAIMED") => renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: messages }}><MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(codes), permissionsResolved: true }} />}><Route path="/" element={<><WorkCalendar entityID="3" onOpen={() => {}} revision={0} /><WorkSchedule entityID="3" detail={{ ...base, state }} onUpdated={() => {}} /><WorkUpstreamArtifacts entityID="3" detail={base} /></>} /></Route></Routes></MemoryRouter></IntlAppProvider>)
  it("offers schedule changes and consumption only with current write authority", () => {
    const denied = render([])
    expect(denied).not.toContain(en.scheduleSave)
    expect(denied).not.toContain(en.upstreamConsume)
    const allowed = render(["work_item_write"])
    expect(allowed).toContain(en.scheduleSave)
    expect(allowed).toContain(en.upstreamConsume)
    expect(allowed).toContain('type="datetime-local"')
    const buttons = [...allowed.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)]
    for (const label of [en.scheduleSave, en.upstreamConsume]) {
      const submit = buttons.find(button => button[2]!.includes(label))
      expect(submit?.[1]).toContain('type="submit"')
    }
    expect(render(["work_item_write"], en, "ACCEPTED")).not.toContain(en.scheduleSave)
  })
  it("has month/week/agenda and unscheduled filtering in three locales", () => {
    for (const messages of [en, zh, ms]) {
      const markup = render(["work_item_write"], messages)
      for (const key of ["schedule_month", "schedule_week", "schedule_agenda", "scheduleReason", "scheduleUnscheduled", "upstreamTitle", "upstreamConsume"] as const) expect(markup).toContain(messages[key])
      for (const [key, value] of Object.entries(messages)) {
        if (key.startsWith("schedule") || key.startsWith("upstream")) expect(value).not.toContain("?")
      }
    }
  })
})
