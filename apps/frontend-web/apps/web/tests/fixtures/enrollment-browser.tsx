// Explicit transport fixtures render the production enrollment component.
// Actual IAM, credentials and ONLINE are independently tested with the installed Runner.
import {createRoot} from "react-dom/client"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../src/lib/i18n"
import {Enrollment} from "../../src/app/machines/enrollment"
import type {EnrollmentStatus} from "../../src/app/machines/enrollment-api"
import "../../src/index.css"

const params = new URLSearchParams(location.search)
const locale = params.get("locale") ?? "en-US"
const writes: unknown[] = []
const reads: string[] = []
const now = new Date()
const status: EnrollmentStatus = {
  id: "9007199254740993", name: "Reported machine", os: "WINDOWS", state: "PENDING",
  server_time: now.toISOString(), expires_at: new Date(now.getTime() + (params.has("expiry") ? 2500 : 300000)).toISOString(),
  facts: {hostname: "observed-build-host", arch: "amd64", runtimes: [{kind: "CODEX", available: true, version: "1.2.3"}, {kind: "CLAUDE_CODE", available: false, error: "NOT_FOUND"}]},
}
Object.assign(window, {enrollmentWrites: writes, enrollmentReads: reads, enrollmentStatus: status})
const nativeFetch = window.fetch.bind(window)
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.origin)
  if (!url.pathname.includes("/api/")) return nativeFetch(input, init)
  const ok = (data: unknown) => Response.json({code: 0, data})
  if (url.pathname.endsWith("machine-enrollment-settings")) return ok({available: true, install: "npm install machine-program@1.0.0", login: "machine-program login --profile ./private-profile", start: "machine-program daemon --profile ./private-profile"})
  const method = init?.method ?? "GET"
  if (method === "GET") {
    reads.push(url.pathname + url.search)
    if (url.searchParams.get("user_code") === "XXXX-XXXX") return Response.json({code: 404, message: "Enrollment not found"}, {status: 404})
    if (url.searchParams.get("user_code") === "AAAA-CCCC") await new Promise(resolve => setTimeout(resolve, 250))
    return ok({...status, server_time: new Date().toISOString()})
  }
  const body = JSON.parse(String(init?.body ?? "{}"))
  writes.push({method, path: url.pathname, body})
  if (params.has("decisionFailure") && writes.length === 1) return Response.json({code: 503, message: "Decision temporarily unavailable"}, {status: 503})
  status.state = body.approve ? "APPROVED" : "DENIED"
  if (body.approve) {status.name = body.name; status.machine_id = "9007199254740993"; status.machine_state = "OFFLINE"}
  return ok(true)
}
const messages = await loadMessages(locale)
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><main className="mx-auto max-w-3xl p-4"><Enrollment tenantID="7" initialCode={params.get("user_code") ?? ""} onChanged={async () => {}} /></main></IntlAppProvider>)
