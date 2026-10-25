import { afterEach, describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { createIamApi } from "../../lib/iam-api"
import { CapabilityFacts } from "./operation-manifest"
import { operationManifestApi } from "./operation-manifest-api"
let server: ReturnType<typeof Bun.serve> | undefined
afterEach(() => { server?.stop(true); server = undefined })
describe("immutable operation manifest", () => {
 it("reads exact historical publication and current subject project capabilities without commands", async () => {
  const requests: { method: string; path: string }[] = []
  server = Bun.serve({ port: 0, fetch(request) { requests.push({ method: request.method, path: new URL(request.url).pathname + new URL(request.url).search }); return Response.json({ code: 0, data: { items: [], operations: [] } }) } })
  const api = operationManifestApi(createIamApi(`http://127.0.0.1:${server.port}`, () => "1"))
  await api.versions("1")
  await api.manifest("1", "1.2.3")
  await api.capabilities("1", "7", "3", "1.2.3")
  expect(requests.every(value => value.method === "GET")).toBe(true)
  expect(requests[0]!.path).toBe("/tenants/1/agent-operation-manifest-versions")
  expect(requests[1]!.path).toContain("version=1.2.3")
  expect(requests[2]!.path).toContain("/agents/7/capabilities")
  expect(requests[2]!.path).toContain("project_id=3")
  expect(requests[2]!.path).toContain("manifest_version=1.2.3")
 })
 it("renders explicit exhausted quota and unavailable resource facts", async () => {
  const messages = await loadMessages("en-US")
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><CapabilityFacts value={{ name: "agent_reminders.create", status: "QUOTA_EXHAUSTED", reason_code: "REMINDER_LIMIT", human_confirmation: "NONE", quota_state: { kind: "RATE_LIMIT", state: "EXHAUSTED", remaining: 0 } }} /></IntlAppProvider>)
  expect(html).toContain((messages.iam as Record<string, string>).agentCapability_QUOTA_EXHAUSTED!)
  expect(html).toContain("REMINDER_LIMIT")
  expect(html).toContain("EXHAUSTED")
  expect(html).toContain("0")
  expect(html).not.toContain("AVAILABLE")
 })
})
