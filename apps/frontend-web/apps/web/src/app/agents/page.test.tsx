import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { AuthContext, type AuthContextValue } from "../../components/auth"
import { loadMessages } from "../../lib/i18n"
import {
  tenantSystemNavigation,
  workspaceArea,
  workspaceScopePath,
} from "../../lib/navigation"
import AgentsPage from "./page"
import { AgentInspection } from "./inspection"

describe("agent workspace boundaries", () => {
  it("places agents under collaboration, gates navigation, and rejects project scope", () => {
    expect(workspaceArea("/agents")).toBe("collaboration")
    expect(
      tenantSystemNavigation(new Set(), false, false).some(
        (row) => row.path === "/agents"
      )
    ).toBe(false)
    expect(
      tenantSystemNavigation(
        new Set(),
        false,
        false,
        false,
        false,
        false,
        true
      ).filter((row) => row.path === "/agents")
    ).toHaveLength(1)
    expect(
      tenantSystemNavigation(
        new Set(["/agents"]),
        false,
        false,
        false,
        false,
        false,
        true
      ).some((row) => row.path === "/agents")
    ).toBe(false)
    expect(workspaceScopePath("/admin/1/agents", "2", "", ["agent_read"])).toBe(
      "/admin/2/agents"
    )
    expect(
      workspaceScopePath("/admin/1/agents", "2", "3", ["agent_read"], "project")
    ).toBeNull()
    expect(workspaceScopePath("/admin/1/agents", "2", "", [])).toBeNull()
  })
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    it(`makes retirement reachable only on the selected Agent lifecycle: ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const render = (url: string, permissions: string[]) => renderToStaticMarkup(
        <IntlAppProvider initialLocale={locale} initialMessages={messages}>
          <AuthContext.Provider value={{ session: { subject: "2" }, status: "authenticated" } as AuthContextValue}>
            <MemoryRouter initialEntries={[url]}>
              <Routes><Route element={<Outlet context={{ permissions: new Set(permissions), permissionsResolved: true }} />}>
                <Route path="/admin/:tenantID/agents" element={<AgentsPage />} />
              </Route></Routes>
            </MemoryRouter>
          </AuthContext.Provider>
        </IntlAppProvider>
      )
      const title = (messages.iam as Record<string, string>).agentRetirementTitle!
      expect(render("/admin/1/agents?agent=7", ["agent_read", "agent_retire"])).toContain(title)
      expect(render("/admin/1/agents?agent=7", ["agent_read"])).toContain(title)
      expect(render("/admin/1/agents?agent=7&tab=inbox", ["agent_read", "agent_retire"])).not.toContain(title)
      expect(render("/admin/1/agents", ["agent_read", "agent_retire"])).not.toContain(title)
      expect(render("/admin/1/agents?agent=7", [])).not.toContain(title)
    })
    it(`renders bounded read-only workspace controls and no files before machine evidence: ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><AgentInspection tenant="1" agent="7" tab="workspace" /></IntlAppProvider>)
      expect(html).toContain((messages.iam as Record<string, string>).agentWorkspaceReadOnly!)
      expect(html).toContain((messages.iam as Record<string, string>).agentWorkspacePath!)
      expect(html).toContain('aria-busy="true"')
      expect(html).toContain('maxLength="200"')
      expect(html).toContain("<button")
      expect(html).toContain("<input")
      expect(html).not.toContain("<li")
      expect(html).not.toContain("<pre")
    })
    it(`renders a translated agent selector without fabricated runtime facts: ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const auth = {
        session: { subject: "2" },
        status: "authenticated",
      } as AuthContextValue
      const html = renderToStaticMarkup(
        <IntlAppProvider initialLocale={locale} initialMessages={messages}>
          <AuthContext.Provider value={auth}>
            <MemoryRouter>
              <AgentsPage />
            </MemoryRouter>
          </AuthContext.Provider>
        </IntlAppProvider>
      )
      expect(html).toContain('id="agent-principal"')
      expect(html).toMatch(/inputmode="numeric"/i)
      expect(html).toContain(
        (messages.iam as Record<string, string>).agentChoose!
      )
      expect(html).not.toContain("agentTab_")
      expect(html).not.toContain("agentActivity_")
      expect(html).not.toContain('role="alert"')
    })
  }
})
