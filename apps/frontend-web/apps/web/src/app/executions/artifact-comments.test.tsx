import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, Outlet, Route, Routes } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { ArtifactCommentThreads, FrozenArtifactAnchor, type ArtifactAnchor } from "./artifact-comments"
import { loadMessages } from "../../lib/i18n"

export const frozenTestAnchor: ArtifactAnchor = { kind: "CODE_LINES", path: "source.go", version_ref: "file-version:3 sha256:abc", file_id: "2", version_id: "3", sha256: "abc", start_line: 4, end_line: 5, excerpt: "return false\n<script>alert(1)</script>" }
describe("Artifact comments", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) it(`renders labeled file/line controls and immutable source in ${locale}`, async () => {
    const messages = await loadMessages(locale), strings = messages.iam as Record<string, string>
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(["execution_read", "artifact_comment_write", "work_item_write"]), permissionsResolved: true }} />}><Route index element={<ArtifactCommentThreads tenantID="1" projectID="99" executionID="7" evidence={[{ id: "10", kind: "FILE", title: "Actual evidence" }]} />} /></Route></Routes></MemoryRouter></IntlAppProvider>)
    for (const key of ["artifactCommentsTitle", "artifactCommentsHint", "artifactCommentFile", "artifactCommentAnchorKind", "artifactCommentStart", "artifactCommentEnd", "artifactCommentBody", "artifactCommentAdd"]) expect(html).toContain(strings[key]!)
    expect(html).toContain('value="10"'); expect(html).toContain("Actual evidence"); expect(html).toContain('max="100"'); expect(html.match(/<label/g)?.length).toBe(5)
    const anchor = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><FrozenArtifactAnchor anchor={frozenTestAnchor} /></IntlAppProvider>)
    expect(anchor).toContain("source.go:4-5"); expect(anchor).toContain("file-version:3 sha256:abc"); expect(anchor).toContain("&lt;script&gt;"); expect(anchor).not.toContain("<script>")
    expect(strings.artifactCommentsTitle).not.toMatch(/\?{2,}/)
  })
  it("keeps comment creation unavailable without captured FILE evidence", async () => {
    const messages = await loadMessages("en-US")
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><MemoryRouter><Routes><Route element={<Outlet context={{ permissions: new Set(["execution_read", "artifact_comment_write", "work_item_write"]), permissionsResolved: true }} />}><Route index element={<ArtifactCommentThreads tenantID="1" projectID="99" executionID="7" evidence={[{ id: "10", kind: "COMMIT" }]} />} /></Route></Routes></MemoryRouter></IntlAppProvider>)
    expect(html).toContain((messages.iam as Record<string, string>).artifactCommentsNoFiles!); expect(html).not.toContain("<form")
  })
})
