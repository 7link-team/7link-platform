// Temporary isolated browser boundary fixture, removed after verification.
import { createRoot } from "react-dom/client"
import { createMemoryRouter, Outlet, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { AuthContext } from "../../components/auth"
import { iamApi } from "../../lib/iam-api"
import { loadMessages } from "../../lib/i18n"
import AgentsPage from "./page"
import "../../index.css"
const events: unknown[] = []
Object.assign(window, { stepupEvents: events })
iamApi.agentLive = async () => ({ agent_principal_id: "7", name: "Test agent", identity_state: "ACTIVE", activity: "OFFLINE", session_epoch: 1, workspace_epoch: 1, head: 0, ack: 0, next: [] }) as never
iamApi.agentTools = async () => ({ items: [], has_more: false, next: [] })
iamApi.requestContract = async (route) => ({ version: "test", operation_id: "boundary", method: "POST", path: route.url, schemas: {}, request_body: { content: { "application/json": { schema: { type: "object", properties: Object.fromEntries(["kind", "reason", "confirm_name", "entry_key", "title", "transport", "endpoint", "content_hash", "skill_body", "credential"].map(key => [key, { type: "string" }])) } } } } }) as never
iamApi.reauthenticationMethods = async () => ({ methods: ["password"], mfa_required: false })
iamApi.identityLinks = async () => []
iamApi.identityLinkProviders = async () => []
iamApi.capabilities = async () => ({ password_recovery: false }) as never
iamApi.createReauthenticationChallenge = async (body) => { events.push({ challenge: body }); return { proof: "fresh-proof", method: "password", expires_at: "2099-01-01T00:00:00Z" } }
iamApi.verifyReauthentication = async () => { events.push({ verified: true }); return { verified: true } }
iamApi.agentReset = async (_tenant, _agent, body, key) => { events.push({ reset: body, key }); throw new Error("Runtime dispatch unavailable (boundary fixture)") }
iamApi.createAgentTool = async (_tenant, body, key) => { events.push({ tool: body, key }); return { id: "81", has_credential: true, next: [] } as never }
const router = createMemoryRouter([{ element: <Outlet context={{ permissions: new Set(["agent_manage", "agent_read", "tool_catalog_manage"]), permissionsResolved: true }} />, children: [{ path: "/admin/:tenantID/agents", element: <AgentsPage /> }] }], { initialEntries: ["/admin/1/agents"] })
void loadMessages(new URLSearchParams(location.search).get("locale") ?? "en-US").then(messages => createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><AuthContext.Provider value={{ session: { subject: "2" }, status: "authenticated" } as never}><RouterProvider router={router}/></AuthContext.Provider></IntlAppProvider>))
