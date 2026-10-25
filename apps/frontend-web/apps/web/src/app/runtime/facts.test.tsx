import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import type { components } from "../../lib/api-schema"
import en from "../../messages/en-US.json"
import { MetricsValues } from "../metrics/page"
import { executionWorkdirs, metricPeriod } from "./facts"

describe("runtime facts", () => {
  it("matches a workdir only by both the frozen ID and project, never by a similar path", () => {
    const execution = {project_id: "20", plan: {workdir_id: "30", workdir: "/repo"}} as components["schemas"]["ExecutionView"]
    const facts: components["schemas"]["MachineCapabilities"] = {remote_access: "UNAVAILABLE", active_slots: [], runtimes: [], tools: [], discoveries: [], workdirs: [
      {id: "30", project_id: "20", repository_id: "40", path: "/repo", version: 1},
      {id: "31", project_id: "20", repository_id: "40", path: "/repo", version: 1},
      {id: "30", project_id: "21", repository_id: "40", path: "/repo", version: 1},
    ]}
    expect(executionWorkdirs(execution, facts)).toEqual([facts.workdirs![0]!])
    expect(executionWorkdirs({...execution, plan: {...execution.plan, workdir_id: null}}, facts)).toEqual([])
    expect(executionWorkdirs(execution, {...facts, workdirs: null})).toEqual([])
  })
  it("rejects reversed, equal, malformed and calendar-overflow periods", () => {
    expect(metricPeriod("2026-10-01", "2026-10-21")).toEqual({from: "2026-10-01", to: "2026-10-21"})
    for (const [from, to] of [["2026-10-21", "2026-10-01"], ["2026-10-21", "2026-10-21"], ["2026-02-30", "2026-10-21"], ["", "2026-10-21"], ["2026-1-01", "2026-10-21"]]) expect(metricPeriod(from!, to!)).toBeNull()
    expect(metricPeriod("2028-02-29", "2028-03-01")).not.toBeNull()
  })
  it("preserves exact decimal cost and distinguishes an unavailable metric from zero", () => {
    const data: components["schemas"]["WorkbenchSummary"] = {from: "2026-10-01T00:00:00Z", to: "2026-10-21T00:00:00Z", projects: 0, work_items: 0, open: 0, blocked: 0, accepted: 0, review: 0, acceptances: [], activity: [], load: [], machines: [], missing_sources: ["execution"], execution_cost_micros: "9007199254740993"}
    const render = (summary: typeof data) => renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{iam: en}}><MetricsValues data={summary}/></IntlAppProvider>)
    const html = render(data)
    expect(html).toContain("9007199254740993")
    expect(html).toContain(en.workbenchSummaryUnknown)
    expect(html).toMatch(/>0<\/dd>/)
    const complete = render({...data, execution_created: 0, execution_cost_micros: "0", missing_sources: []})
    expect(complete).not.toContain(en.workbenchSummaryUnknown)
  })
})
