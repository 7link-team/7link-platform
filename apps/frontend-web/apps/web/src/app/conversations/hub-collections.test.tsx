import { expect, it } from "bun:test"
import { isActiveHubExecution } from "./hub-api"

it("includes only the dedicated live session states without presenting completed runs as active", () => {
  for (const state of ["RUNNING", "PAUSED"]) expect(isActiveHubExecution(state)).toBe(true)
  for (const state of ["CREATED", "INTERRUPTED", "COMPLETED", "FAILED", "CANCELLED", "SUCCEEDED", "PENDING", "UNKNOWN"]) expect(isActiveHubExecution(state)).toBe(false)
})

import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { HubToolApproval } from "./hub-collections"
import type { components } from "../../lib/api-schema"

it("shows real tool approval input and requires a reason while disabling duplicate decisions", async () => {
  const messages = await loadMessages("en-US")
  const labels = messages.iam as Record<string, string>
  const approval: components["schemas"]["ApprovalView"] = {id: "91", execution_id: "81", request_id: "request", tool: "ReadFile", input: {path: "/repo/file"}, state: "PENDING", reason: "", expires_at: "2026-10-23T00:00:00Z"}
  const render = (reason: string, busy: boolean) => renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><HubToolApproval approval={approval} reason={reason} busy={busy} onReason={() => {}} onDecide={() => {}} /></IntlAppProvider>)
  const empty = render("", false)
  expect(empty).toContain("ReadFile")
  expect(empty).toContain("/repo/file")
  expect(empty).toContain(labels.executionPermissionReason!)
  expect(empty).toContain(labels.executionPermissionAllow!)
  expect(empty).toContain(labels.executionPermissionDeny!)
  expect((empty.match(/<button[^>]+>/g) ?? []).filter(tag => tag.includes(' disabled=""')).length).toBe(2)
  expect((render("Checked the requested path", false).match(/<button[^>]+>/g) ?? []).filter(tag => tag.includes(' disabled=""')).length).toBe(0)
  expect((render("Checked the requested path", true).match(/<button[^>]+>/g) ?? []).filter(tag => tag.includes(' disabled=""')).length).toBe(2)
})
