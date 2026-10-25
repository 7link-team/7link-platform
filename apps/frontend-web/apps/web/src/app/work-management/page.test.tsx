import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { WorkflowPreviewResult, WorklogApproval } from "./page"
import { messages } from "./messages"
import type { WorkflowPreview, Worklog } from "./api"

test("configuration preview distinguishes changed cells from runtime acceptance in every locale", () => {
  const workflow = { version: 1, states: [], transitions: [] }
  const value: WorkflowPreview = {
    before: workflow,
    after: workflow,
    changed_states: ["ready"],
    changed_transitions: ["draft:ready"],
    runtime_impact: "NOT_EVALUATED",
  }
  for (const labels of Object.values(messages)) {
    const html = renderToStaticMarkup(
      <WorkflowPreviewResult value={value} labels={labels} />
    )
    expect(html).toContain('role="status"')
    expect(html).toContain(labels.previewNotice)
    expect(html).toContain("draft:ready")
    expect(html).toContain(labels.changedStates)
    expect(html).toContain(labels.changedTransitions)
  }
})

test("effort renders persisted Human approval without manufacturing one for runtime receipts", () => {
  const row: Worklog = {
    id: "1",
    principal_id: "11",
    actor_kind: "HUMAN",
    category: "TEST",
    work_date: "2026-10-20",
    minutes: 30,
    cost_micros: null,
    note: "",
    state: "APPROVED",
    version: 3,
    approved_by: "12",
    approved_at: "2026-10-20T12:00:00Z",
  }
  for (const labels of Object.values(messages)) {
    const html = renderToStaticMarkup(
      <WorklogApproval row={row} labels={labels} />
    )
    expect(html).toContain(labels.approvedBy)
    expect(html).toContain("12")
    expect(html).toContain('dateTime="2026-10-20T12:00:00Z"')
    expect(
      renderToStaticMarkup(
        <WorklogApproval
          row={{
            ...row,
            actor_kind: "AGENT",
            execution_id: "77",
            approved_by: null,
            approved_at: null,
          }}
          labels={labels}
        />
      )
    ).toBe("")
  }
})

test("evaluated workflow preview shows actual impact without claiming business transitions", () => {
  const workflow = { version: 2, states: [], transitions: [] }
  for (const labels of Object.values(messages)) {
    const html = renderToStaticMarkup(<WorkflowPreviewResult value={{ before: workflow, after: workflow, changed_states: ["ready"], changed_transitions: [], runtime_impact: "EVALUATED", projects_affected: 2, items_in_archived_states: 3 }} labels={labels} />)
    expect(html).toContain(labels.impactNotice)
    expect(html).toContain(`${labels.projectsAffected}: 2`)
    expect(html).toContain(`${labels.itemsInArchivedStates}: 3`)
    expect(html).not.toContain(labels.previewNotice)
  }
})
