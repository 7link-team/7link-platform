import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import { WorkRecovery } from "./recovery"
import { recoveryActions, type RecoveryWork } from "./recovery-policy"
import type { WorkResult } from "./results-api"

const permissions = [
  "work_item_read",
  "work_item_write",
  "requirement_read",
  "decision_pause",
  "decision_approve",
  "decision_request_changes",
]
const all = (code: string) => permissions.includes(code)
const work = {
  id: "7",
  type: "BUG",
  state: "ACCEPTED",
  version: 4,
  current_claim_id: "11",
  current_execution_id: null,
  paused_at: null,
  pause_reason: null,
  baseline_stale: false,
  defect_stage: null,
  accepted_at: "2026-10-20T00:00:00Z",
  accepted_decision_id: "12",
  is_container: false,
  requirement_id: "5",
} as RecoveryWork
const result: WorkResult = {
  id: "11",
  project_id: "3",
  work_item_id: "7",
  sequence: 1,
  summary: "Actual result",
  actor_id: "13",
  created_at: "2026-10-20T00:00:00Z",
  evidence: [],
  handoffs: [],
  actions: [
    {
      id: "14",
      kind: "CHECK",
      verdict: "PASSED",
      actor_id: "17",
      reason: "Actual independent check",
      created_at: "2026-10-20T00:00:00Z",
    },
  ],
}

describe("work recovery authority and state", () => {
  it("keeps Chinese recovery messages readable instead of encoding placeholders", () => {
    const messages = Object.entries(zh).filter(([key]) =>
      key.startsWith("workRecovery")
    )
    expect(messages.length).toBeGreaterThan(20)
    for (const [key, value] of messages) {
      expect(value, key).not.toMatch(/\?{2,}|\uFFFD/)
      expect(value, key).toMatch(/\p{Script=Han}/u)
    }
  })

  it("allows verified then closed only for the independent exact-result checker", () => {
    expect(recoveryActions(work, all, result, "17")).toEqual(["VERIFIED"])
    expect(
      recoveryActions(
        { ...work, type: "INCIDENT", defect_stage: "VERIFIED" },
        all,
        result,
        "17"
      )
    ).toEqual(["CLOSED"])
    for (const patch of [
      { current_claim_id: "99" },
      { type: "TASK" },
      { state: "REWORK" },
      { baseline_stale: true },
      { accepted_at: null },
      { accepted_decision_id: null },
      { defect_stage: "CLOSED" },
    ])
      expect(
        recoveryActions({ ...work, ...patch }, all, result, "17")
      ).not.toContain("VERIFIED")
    expect(recoveryActions(work, all, result, "13")).toEqual([])
    expect(recoveryActions(work, all, result, "19")).toEqual([])
    expect(
      recoveryActions(
        work,
        all,
        {
          ...result,
          actions: [
            ...result.actions!,
            { ...result.actions![0]!, id: "18", verdict: "FAILED" },
          ],
        },
        "17"
      )
    ).toEqual([])
    for (const removed of [
      "work_item_read",
      "work_item_write",
      "decision_approve",
    ])
      expect(
        recoveryActions(work, (c) => all(c) && c !== removed, result, "17")
      ).toEqual([])
  })
  it("never offers fabricated merged or deployed transitions", () => {
    for (const stage of [null, "MERGED", "DEPLOYED", "VERIFIED", "CLOSED"]) {
      const actions = recoveryActions(
        { ...work, defect_stage: stage },
        all,
        result,
        "17"
      )
      expect(actions).not.toContain("MERGED")
      expect(actions).not.toContain("DEPLOYED")
    }
  })
  it("allows stale scope rework only in independent manual current review", () => {
    const stale = { ...work, state: "NEEDS_HUMAN_REVIEW", baseline_stale: true }
    expect(recoveryActions(stale, all, result, "17")).toEqual(["scope"])
    for (const patch of [
      { current_execution_id: "99" },
      { current_execution_id: undefined },
      { baseline_stale: false },
      { current_claim_id: "99" },
      { state: "ACCEPTED" },
    ])
      expect(
        recoveryActions({ ...stale, ...patch }, all, result, "17")
      ).not.toContain("scope")
    expect(recoveryActions(stale, all, result, "13")).not.toContain("scope")
    expect(
      recoveryActions(
        stale,
        (c) => all(c) && c !== "decision_request_changes",
        result,
        "17"
      )
    ).not.toContain("scope")
  })
  it("resumes an identified progress hold with direct Human decision permission", () => {
    const paused = {
      ...work,
      state: "IN_PROGRESS",
      current_execution_id: "99",
      paused_at: "2026-10-23T00:00:00Z",
      pause_reason: "ESCALATION_RULE",
      preparation: {
        ...work.preparation,
        block_reasons: ["AUTONOMY_NO_PROGRESS"],
      },
    }
    expect(recoveryActions(paused, all)).toEqual(["resume"])
    expect(
      recoveryActions(
        {
          ...paused,
          preparation: { ...paused.preparation, block_reasons: [] },
        },
        all
      )
    ).not.toContain("resume")
    expect(
      recoveryActions(paused, (c) => all(c) && c !== "decision_pause")
    ).not.toContain("resume")
  })
  it("resumes only an autonomy-limit manual pause and requires direct decision permission", () => {
    const paused = {
      ...work,
      state: "REWORK",
      paused_at: "2026-10-20T00:00:00Z",
      pause_reason: "AUTONOMY_REWORK_LIMIT",
    }
    expect(recoveryActions(paused, all)).toEqual(["resume"])
    for (const patch of [
      { pause_reason: "OTHER" },
      { paused_at: null },
      { current_execution_id: "99" },
      { current_execution_id: undefined },
      { state: "STOPPED" },
    ])
      expect(recoveryActions({ ...paused, ...patch }, all)).not.toContain(
        "resume"
      )
    expect(
      recoveryActions(paused, (c) => all(c) && c !== "decision_pause")
    ).toEqual([])
  })
  it("offers explicit baseline confirmation only with requirement visibility and editable manual work", () => {
    for (const state of [
      "DRAFT",
      "READY",
      "BLOCKED",
      "CLAIMED",
      "IN_PROGRESS",
      "REWORK",
    ])
      expect(recoveryActions({ ...work, state }, all)).toContain("baseline")
    for (const patch of [
      { state: "ACCEPTED" },
      { state: "NEEDS_HUMAN_REVIEW" },
      { requirement_id: null },
      { paused_at: "2026-10-20T00:00:00Z" },
      { current_execution_id: "99" },
      { current_execution_id: undefined },
      { is_container: true },
    ])
      expect(
        recoveryActions({ ...work, state: "REWORK", ...patch }, all)
      ).not.toContain("baseline")
    for (const removed of [
      "work_item_read",
      "work_item_write",
      "requirement_read",
    ])
      expect(
        recoveryActions(
          { ...work, state: "REWORK" },
          (c) => all(c) && c !== removed
        )
      ).not.toContain("baseline")
  })
})

describe("work recovery rendering", () => {
  function render(codes: string[], detail = work, withResult = true) {
    return renderToStaticMarkup(
      <IntlAppProvider
        initialLocale="en-US"
        initialMessages={{ ...ui, iam: en }}
      >
        <MemoryRouter>
          <Routes>
            <Route
              element={
                <Outlet
                  context={{
                    permissions: new Set(codes),
                    permissionsResolved: true,
                  }}
                />
              }
            >
              <Route
                path="/"
                element={
                  <WorkRecovery
                    entityID="3"
                    detail={detail}
                    result={withResult ? result : undefined}
                    subject="17"
                    onUpdated={() => {}}
                  />
                }
              />
            </Route>
          </Routes>
        </MemoryRouter>
      </IntlAppProvider>
    )
  }
  it("renders the concrete verified action and hides it without approval", () => {
    expect(render(permissions)).toContain("Record verified defect")
    expect(
      render(permissions.filter((c) => c !== "decision_approve"))
    ).not.toContain("Record verified defect")
    expect(
      render(permissions, { ...work, defect_stage: "VERIFIED" })
    ).toContain("Close verified defect")
    const closed = render(permissions, { ...work, defect_stage: "CLOSED" })
    expect(closed).toContain("Defect closed")
    expect(closed).not.toContain("Close verified defect")
  })
  it("translates every recovery label in all three locales", () => {
    for (const key of Object.keys(en).filter((k) =>
      k.startsWith("workRecovery")
    ))
      for (const locale of [en, zh, ms])
        expect((locale as Record<string, string>)[key]?.length).toBeGreaterThan(
          0
        )
  })
  it("offers new BUG reopening only for terminal defects with current read/write authority", () => {
    expect(render(permissions, work, false)).toContain(en.workRecovery_reopen)
    expect(
      render(permissions, { ...work, state: "CANCELLED" }, false)
    ).toContain(en.workRecovery_reopen)
    for (const detail of [
      { ...work, state: "REWORK" },
      { ...work, type: "TASK" },
      { ...work, is_container: true },
    ])
      expect(render(permissions, detail, false)).not.toContain(
        en.workRecovery_reopen
      )
    for (const removed of ["work_item_read", "work_item_write"])
      expect(
        render(
          permissions.filter((code) => code !== removed),
          work,
          false
        )
      ).not.toContain(en.workRecovery_reopen)
    expect(render(permissions, work)).not.toContain(en.workRecovery_reopen)
  })
})
