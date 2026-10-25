import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { WorkCommentSourceSnapshot, type CommentSource } from "./comment-source"
import { loadMessages } from "../../lib/i18n"

describe("Frozen comment provenance", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) it(`preserves first comment and links to exact original thread in ${locale}`, async () => {
    const messages = await loadMessages(locale), strings = messages.iam as Record<string, string>
    const source: CommentSource = { source_comment_thread_id: "80", execution_id: "71", source_first_body: "Original finding", found_in_work_item_id: "60", source_anchor_snapshot: { kind: "CODE_LINES", path: "source.go", version_ref: "file-version:3 sha256:abc", file_id: "2", version_id: "3", sha256: "abc", start_line: 4, end_line: 4, excerpt: "return false" }, steps: "Reproduce", expected: "Return true", actual: "Overridden report", found_stage: "INTERNAL_QA" }
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><MemoryRouter><WorkCommentSourceSnapshot source={source} tenantID="1" entityID="99" /></MemoryRouter></IntlAppProvider>)
    expect(html).toContain("Original finding"); expect(html).not.toContain("Overridden report"); expect(html).toContain("executions/71#artifact-comment-80"); expect(html).toContain("work-items?work=60"); expect(html).toContain("file-version:3 sha256:abc"); expect(html).toContain(strings.artifactCommentOpenSource!)
  })
})
