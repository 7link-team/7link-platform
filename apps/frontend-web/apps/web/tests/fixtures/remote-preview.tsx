// Isolated UI verification: all API data is explicit fixture data.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, Outlet, RouterProvider, useLocation } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../src/lib/i18n"
import { AuthContext } from "../../src/components/auth"
import { RemoteTerminal } from "../../src/app/machines/remote-terminal"
import PreviewAccessPage from "../../src/app/preview-access/page"
import "../../src/index.css"

const query = new URLSearchParams(location.search)
const writes: {path: string; body: unknown; key?: string | null}[] = []
const requests: string[] = []
Object.assign(window, {fixtureWrites: writes, fixtureRequests: requests})
let policy = {allow_remote_terminal: query.has("terminal"), allow_remote_preview: !query.has("disabled"), tunnel_available: true, version: 1}
let terminalCreates = 0
let terminalOpen = false
let terminalClosed = false
let proofNumber = 0
let terminalOutput: ReadableStreamDefaultController<Uint8Array> | undefined
Object.assign(window, {fixtureOpenTerminal: () => {terminalOpen = true}})
const readySession = {id: "31", execution_id: "21", machine_id: "11", started_by: "12", kind: "SERVICE", origin: "AGENT_OFFERED", port: 8080, state: "READY", url_mode: query.has("path") ? "PATH" : "SUBDOMAIN", path_mode_warning: query.has("path"), last_access_at: "2026-10-22T00:00:00Z", expires_at: "2026-10-22T08:00:00Z", version: 1}
let rows: unknown[] = query.has("seeded") ? [readySession] : []
const nativeFetch = window.fetch.bind(window)
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.origin)
  if (!url.pathname.startsWith("/api/")) return nativeFetch(input, init)
  const path = url.pathname.slice(4)
  requests.push(path)
  const body = init?.body ? JSON.parse(String(init.body)) : null
  if (init?.method && init.method !== "GET") writes.push({path, body, ...(path.endsWith("terminal-sessions") ? {key: new Headers(init.headers).get("Idempotency-Key")} : {})})
  if (path === "/me") return Response.json({code: 0, data: {subject: "12"}})
  if (path === "/login/options") return Response.json({code: 0, data: {password_recovery: false}})
  if (path.endsWith("/reauthentication/methods")) return Response.json({code: 0, data: {methods: ["password"], mfa_required: false}})
  if (path.endsWith("/identity-links") || path.endsWith("/identity-link-providers")) return Response.json({code: 0, data: []})
  if (path.endsWith("/reauthentication/challenges")) return Response.json({code: 0, data: {proof: `fixture-proof-${++proofNumber}`}})
  if (path.endsWith("/reauthentication/verifications")) return Response.json({code: 0, data: {verified: true}})
  const terminal = {id: "41", state: terminalClosed ? "CLOSED" : terminalOpen ? "OPEN" : "OPENING", expires_at: "2040-01-02T04:04:05Z"}
  if (path.endsWith("/terminal-sessions")) {
    terminalCreates++
    if (query.has("terminal-retry") && terminalCreates === 1) return Response.json({message: "Fixture uncertain create"}, {status: 503})
    return Response.json({code: 0, data: {...terminal, state: "OPENING"}}, {status: 201})
  }
  if (path === "/terminal-sessions/41") return Response.json({code: 0, data: terminal})
  if (path.endsWith("/41/stream")) return new Response(new ReadableStream({start(controller) {terminalOutput = controller; controller.enqueue(new TextEncoder().encode('{"kind":"ping"}\n'))}}), {headers: {"Content-Type": "application/x-ndjson"}})
  if (path.endsWith("/41/input")) return Response.json({code: 0, data: {accepted: true}})
  if (path.endsWith("/41/revoke")) {
    terminalClosed = true
    terminalOutput?.close(); terminalOutput = undefined
    return Response.json({code: 0, data: {...terminal, state: "CLOSED"}})
  }
  if (query.has("denied") && path.includes("access-grants")) return Response.json({message: "Fixture preview access denied"}, {status: 403})
  if (path.endsWith("remote-access")) {
    if (init?.method === "PUT") policy = {...policy, ...body, version: policy.version + 1}
    return Response.json({code: 0, data: policy})
  }
  if (path.endsWith("preview-sessions")) {
    if (init?.method === "POST") rows = [{...readySession, port: body.port}]
    return Response.json({code: 0, data: path.includes("/22/") ? [] : rows})
  }
  if (path.endsWith("termination")) {
    rows = rows.map((row) => ({...(row as object), state: "STOPPED", version: 2}))
    return Response.json({code: 0, data: rows[0]})
  }
  if (path.includes("access-grants")) return Response.json({code: 0, data: {url: query.has("unsafe") ? location.origin + "/app-bytes" : "https://abcdefghijklmnopqrstuv2345.preview.example.test/__access?code=fixture-only", expires_in: 60}}, {status: 201})
  return Response.json({code: 0, data: []})
}
const permissions = new Set(query.has("readonly") ? ["execution_read"] : ["machine_access_manage", "terminal_interact", "execution_read", "execution_start", "execution_stop"])
export function LoginFixture() {
  const at = useLocation()
  return <p role="status">Fixture login: {(at.state as {from: string}).from}</p>
}
const router = createMemoryRouter([{element: <Outlet context={{permissions, permissionsResolved: true}}/>, children: [
  {path: "/", element: <main className="mx-auto max-w-4xl p-4"><RemoteTerminal machineID="11" os="LINUX" online={!query.has("offline")} executionIDs={["21", "22"]}/></main>},
  {path: "/preview-access", element: <PreviewAccessPage/>},
  {path: "/admin/login", element: <LoginFixture/>},
]}], {initialEntries: [query.has("exchange") ? "/preview-access?label=" + (query.has("invalid") ? "bad" : "abcdefghijklmnopqrstuv2345") : "/"]})
const locale = query.get("locale") ?? "en-US"
createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><AuthContext.Provider value={{status: query.has("anonymous") ? "anonymous" : "authenticated", session: {subject: query.has("other") ? "13" : "12"}} as never}><RouterProvider router={router}/></AuthContext.Provider></IntlAppProvider>)
