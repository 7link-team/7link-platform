import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { createMemoryRouter, RouterProvider } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import KnowledgePage, { KnowledgeReviewForm } from "./page"
import type { KnowledgeEntry } from "../../lib/iam-api"
import {
  tenantSystemNavigation,
  workspaceArea,
  workspaceScopePath,
} from "../../lib/navigation"

describe("knowledge scope and permission presentation", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    it(`requires another reviewer, while retaining authorized writer revocation: ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const entry: KnowledgeEntry = { id: "30", scope: "TEAM", kind: "RULE", state: "PENDING_REVIEW", origin: "HUMAN", risk_flags: [], ttl_days: 180, use_count: 0, title: "Rule", body: "Check evidence", provenance: "Human notes", content_hash: "hash", version: 1, created_by: "2", created_at: "2026-10-21T00:00:00Z" }
      const render = (subject: string, state = "PENDING_REVIEW") => {
        const router = createMemoryRouter([{ path: "/", element: <KnowledgeReviewForm tenantID="1" entry={{ ...entry, state }} subject={subject} onDone={() => {}} /> }], { initialEntries: ["/"] })
        const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router} /></IntlAppProvider>)
        router.dispose()
        return html
      }
      const words = messages.iam as Record<string, string>
      expect(render("2")).toContain(words.knowledgeSelfReview!)
      expect(render("2")).not.toContain("<form")
      expect(render("")).not.toContain("<form")
      expect(render("3")).toContain("<form")
      expect(render("3")).not.toContain(words.knowledgeSelfReview!)
      expect(render("2", "ACTIVE")).toContain("<form")
      expect(render("2", "ACTIVE")).not.toContain(words.knowledgeSelfReview!)
      const conflictRouter = createMemoryRouter([{path:"/", element:<KnowledgeReviewForm tenantID="1" entry={{...entry, conflict_memory_ids:["20"]}} subject="3" onDone={() => {}} />}],{initialEntries:["/"]})
      const conflictHTML = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={conflictRouter} /></IntlAppProvider>)
      conflictRouter.dispose()
      expect(conflictHTML).toContain(words.knowledgeConflictingRules!)
      expect(conflictHTML).toContain(words.knowledgeConflictAcknowledgement!)
      expect(conflictHTML).toContain('type="checkbox"')
      expect(conflictHTML).toContain("20")
    })
  }
  it("offers tenant knowledge only with its permission and keeps scope switches in the same top area", () => {
    expect(
      tenantSystemNavigation(new Set(), false, false, false, true)
    ).toContainEqual({ path: "/knowledge", label: "knowledge" })
    expect(tenantSystemNavigation(new Set(), false, false)).not.toContainEqual({
      path: "/knowledge",
      label: "knowledge",
    })
    expect(
      workspaceScopePath("/admin/1/entities/3/knowledge", "1", "", [
        "knowledge_read",
      ])
    ).toBe("/admin/1/knowledge")
    expect(
      workspaceScopePath(
        "/admin/1/knowledge",
        "1",
        "3",
        ["knowledge_read"],
        "project"
      )
    ).toBe("/admin/1/entities/3/knowledge")
    expect(workspaceArea("/admin/1/knowledge")).toBe(
      workspaceArea("/admin/1/entities/3/knowledge")
    )
  })
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    it(`renders translated scoped reads without granting mutation controls: ${locale}`, async () => {
      const messages = await loadMessages(locale)
      const router = createMemoryRouter(
        [
          {
            path: "/admin/:tenantID/entities/:entityID/knowledge",
            element: <KnowledgePage />,
          },
        ],
        { initialEntries: ["/admin/1/entities/3/knowledge"] }
      )
      const html = renderToStaticMarkup(
        <IntlAppProvider initialLocale={locale} initialMessages={messages}>
          <RouterProvider router={router} />
        </IntlAppProvider>
      )
      router.dispose()
      const words = messages.iam as Record<string, string>
      expect(html).toContain(words.knowledgeScope_TEAM!)
      expect(html).toContain(words.knowledgeScope_PROJECT!)
      expect(html).toContain(words.knowledgeScope_AGENT!)
      expect(html).not.toContain(words.knowledgeWrite!)
      expect(html).not.toContain("knowledgeState_ACTIVE")
    })
  }
})
