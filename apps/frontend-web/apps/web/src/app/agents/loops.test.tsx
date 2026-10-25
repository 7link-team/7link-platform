import { afterEach, describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { createIamApi } from "../../lib/iam-api"
import { loopApi, type LoopCreate, type LoopRun } from "./loops-api"
import { LoopEditor, LoopRunFacts } from "./loops"

let server: ReturnType<typeof Bun.serve> | undefined
afterEach(() => {server?.stop(true); server = undefined})

describe("Agent loop contracts", () => {
  it("sends actual scoped create, quoted CAS patch/state and bounded history requests", async () => {
    const writes: {path: string; method: string; key: string | null; version: string | null; body?: unknown}[] = []
    server = Bun.serve({port: 0, async fetch(req) {
      writes.push({path: new URL(req.url).pathname + new URL(req.url).search, method: req.method, key: req.headers.get("Idempotency-Key"), version: req.headers.get("If-Match"), ...(req.method !== "GET" ? {body: await req.json()} : {})})
      return Response.json({code: 0, data: req.method === "GET" ? {items: [], has_more: false} : {id: "91", version: 4}})
    }})
    const api = loopApi(createIamApi(`http://127.0.0.1:${server.port}`, () => "1"))
    const body: LoopCreate = {activate: false, agent_id: "7", owner_principal_id: null, kind: "CHECK", name: "Evidence review", anchor: {channel_id: "3", thread_root_message_id: "5"}, schedule_kind: "INTERVAL", interval_minutes: 15, self_checks: [], budget: {max_cost_micros_per_run: 0, max_duration_minutes: 30}, escalation: {on_budget_exceeded: false, on_required_check_failed: true, consecutive_failures_to_pause: 3}, output_format: "CHECK_RESULT", allow_overlap: false}
    await api.create("1", body, "create-loop")
    await api.update("91", 4, {name: "Updated"}, "edit-loop")
    await api.update("91", 4, {name: "Updated"}, "edit-loop")
    await api.state("91", 5, "PAUSED", "pause-loop")
    await api.runs("91", "history-token")
    await api.list("1", "7", "list-token")
    expect(writes[0]).toMatchObject({method: "POST", path: "/tenants/1/loop-contracts", key: "create-loop", body})
    expect(writes[1]).toMatchObject({method: "PATCH", path: "/loop-contracts/91", key: "edit-loop", version: '"4"', body: {name: "Updated"}})
    expect(writes[2]).toEqual(writes[1])
    expect(writes[3]).toMatchObject({method: "PUT", version: '"5"', body: {state: "PAUSED"}})
    expect(writes[4]!.path).toContain("cursor=history-token")
    expect(writes[5]!.path).toContain("agent_id=7")
    expect(writes[5]!.path).toContain("limit=50")
  })
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    it(`renders editable limits and preserves unknown cost and exact report provenance in ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const labels = messages.iam as Record<string, string>
      const wrap = (child: React.ReactNode) => <IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter>{child}</MemoryRouter></IntlAppProvider>
      const form = renderToStaticMarkup(wrap(<LoopEditor agent="7" busy={false} onCancel={() => {}} onSave={async () => {}}/>))
      expect(form).toContain(labels.loopDraftHint!)
      expect(form).toContain(labels.loopChannel!)
      expect(form).toContain(labels.loopMaxCost!)
      expect(form).toContain('type="submit"')
      const run: LoopRun = {id: "44", loop_contract_id: "91", fire_no: 2, state: "OPEN", opened_at: "2026-10-21T00:00:00Z", scheduled_for: "2026-10-21T00:00:00Z", self_check_results: [], report_id: "9007199254740993", unavailable_capabilities: ["SETTLED_EXECUTION_COST"]}
      const history = renderToStaticMarkup(wrap(<LoopRunFacts tenant="1" run={run}/>))
      expect(history).toContain(labels.loopUnknown!)
      expect(history).toContain("/admin/1/reports?record_id=9007199254740993")
      expect(history).toContain("SETTLED_EXECUTION_COST")
      expect(history).not.toContain("loopRun_")
      expect(labels.agentInboxKind).not.toContain("?")
      for (const kind of ["DISPATCH", "REVIEW_FEEDBACK"]) {
        expect(labels[`agentInboxKind_${kind}`]).toBeTruthy()
        expect(labels[`agentInboxKind_${kind}`]).not.toContain("?")
      }
    })
  }
})
