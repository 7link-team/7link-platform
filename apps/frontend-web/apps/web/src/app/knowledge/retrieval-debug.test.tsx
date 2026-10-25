import {describe, expect, it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {createMemoryRouter, Outlet, RouterProvider} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {workspaceArea, workspaceScopePath} from "../../lib/navigation"
import RetrievalDebugPage from "./retrieval-debug"
import {iamApi} from "../../lib/iam-api"

describe("retrieval diagnostics scope and request", () => {
  it("keeps diagnostics within the knowledge top area and preserves scope", () => {
    expect(workspaceArea("/admin/1/entities/3/knowledge/debug")).toBe(workspaceArea("/admin/1/knowledge"))
    expect(workspaceScopePath("/admin/1/knowledge/debug", "1", "3", ["knowledge_manage"], "project")).toBe("/admin/1/entities/3/knowledge/debug")
    expect(workspaceScopePath("/admin/1/entities/3/knowledge/debug", "1", "", ["knowledge_manage"])).toBe("/admin/1/knowledge/debug")
    expect(workspaceScopePath("/admin/1/knowledge/debug", "1", "3", ["knowledge_read"], "project")).toBeNull()
  })
  it("sends actual query and exact scope with a stable replay key", async () => {
    const original = globalThis.fetch
    const calls: {url: string; body: unknown; key: string | null}[] = []
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({url: String(input), body: JSON.parse(String(init?.body)), key: new Headers(init?.headers).get("Idempotency-Key")})
      return new Response(JSON.stringify({code: 0, data: {engine: "LEXICAL_ENTRIES", candidates: [], tokens: ["amount"]}}), {headers: {"Content-Type": "application/json"}})
    }) as typeof fetch
    try {
      const query = {scope: "PROJECT" as const, project_id: "3", query: "amount", mode: "ALL" as const, top_k: 10}
      const result = await iamApi.retrievalDebug("1", query, "same-key")
      expect(calls).toEqual([{url: "/api/tenants/1/knowledge/retrieval-debug", body: query, key: "same-key"}])
      expect(result.engine).toBe("LEXICAL_ENTRIES")
    } finally { globalThis.fetch = original }
  })
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    for (const authorized of [false, true]) {
      it(`renders ${locale} with management permission ${authorized}`, async () => {
        const messages = await loadMessages(locale)
        const router = createMemoryRouter([{element: <Outlet context={{permissions: new Set(authorized ? ["knowledge_manage"] : []), permissionsResolved: true}}/>, children: [{path: "/admin/:tenantID/knowledge/debug", element: <RetrievalDebugPage/>}]}], {initialEntries: ["/admin/1/knowledge/debug?q=amount&mode=ANY"]})
        const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>)
        router.dispose()
        const words = messages.iam as Record<string,string>
        expect(html).toContain(authorized ? words.retrievalTitle! : words.retrievalDenied!)
        expect(html).not.toContain("???")
        if (authorized) { expect(html).toContain('value="amount"');expect(html).toContain(words.retrievalBackend!);expect(html).toContain("0.55T") }
        else expect(html).not.toContain("<form")
      })
    }
  }
})
