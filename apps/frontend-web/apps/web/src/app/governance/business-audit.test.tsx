import { expect, it, afterEach } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createIamApi } from "../../lib/iam-api"
import { BusinessAuditFacts } from "./business-audit"
import { businessAuditLabels, businessAuditQuery } from "./business-audit-data"
import type { components } from "../../lib/api-schema"

const event: components["schemas"]["BusinessEvent"] = {audit_event_id: "9007199254740993", sequence: 8, event_type: "fleet.agent_spawned", module: "fleet", project_id: "3", actor: {kind: "AGENT", id: "9007199254740995", name: "Recorded <script>"}, authority: {kind: "POLICY", ref: "spawn_grant", label: "Bounded grant"}, target: {type: "agent", id: "9007199254740997"}, outcome: "success", occurred_at: "2026-10-23T00:00:00Z", origin: "AGENT_SPAWNED", spawn_grant_id: "9007199254740999", supervisor_agent_id: "9007199254741001"}
it("renders recorded labels and exact spawn references in three locales without inventing absent history", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    const html = renderToStaticMarkup(<BusinessAuditFacts event={event} locale={locale}/>)
    expect(html).toContain("9007199254740995")
    expect(html).toContain("9007199254740999")
    expect(html).toContain("9007199254741001")
    expect(html).toContain(businessAuditLabels[locale].agent)
    expect(html).toContain("Bounded grant")
    expect(html).toContain("&lt;script&gt;")
    expect(html).not.toContain("<script>")
    const missing = renderToStaticMarkup(<BusinessAuditFacts locale={locale} event={{...event, actor: {kind: "", name: ""}, authority: {kind: "", ref: "", label: ""}, origin: undefined, spawn_grant_id: undefined, supervisor_agent_id: undefined}}/>)
    expect(missing).not.toContain(businessAuditLabels[locale].agent)
    expect(missing).not.toContain(businessAuditLabels[locale].human)
  }
})
it("restores only audit filters and paging from URL without mixing governance tab state", () => {
  expect(businessAuditQuery(new URLSearchParams("tab=audit&audit_page=3&audit_project_id=9007199254740993&audit_actor_kind=AGENT&audit_event_type=fleet.agent_spawned,fleet.agent_adopted&policy_id=private"))).toEqual({limit: 50, offset: 100, project_id: "9007199254740993", actor_kind: "AGENT", event_type: "fleet.agent_spawned,fleet.agent_adopted"})
  for (const value of ["Infinity", "-1", "1.5", "1e300", "bad"]) expect(businessAuditQuery(new URLSearchParams({audit_page: value})).offset).toBe(0)
})
let server: ReturnType<typeof Bun.serve> | undefined
afterEach(() => server?.stop(true))
it("uses the business projection route and preserves metadata, exact filters and abort signal", async () => {
  let requested = ""
  server = Bun.serve({port: 0, fetch(request) {requested = request.url; return Response.json({code: 0, message: "ok", data: [event], meta: {page: {total: 17}, index_lag_seconds: 0}})}})
  const client = createIamApi(server.url.toString().replace(/\/$/, ""))
  const result = await client.businessAuditEvents("7", {actor_id: "9007199254740995", event_type: "fleet.agent_spawned,fleet.agent_adopted", offset: 50, limit: 50, origin: "AGENT_SPAWNED"})
  const url = new URL(requested)
  expect(url.pathname).toBe("/tenants/7/business-audit-events")
  expect(url.searchParams.get("actor_id")).toBe("9007199254740995")
  expect(url.searchParams.get("event_type")).toBe("fleet.agent_spawned,fleet.agent_adopted")
  expect(result.total).toBe(17)
  expect(result.indexLagSeconds).toBe(0)
  expect(result.items[0]?.spawn_grant_id).toBe("9007199254740999")
  const controller = new AbortController(); controller.abort()
  await expect(client.businessAuditEvents("7", {}, controller.signal)).rejects.toThrow()
})
