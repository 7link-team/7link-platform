import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ProvenanceFacts} from "./provenance"
import type {ProvenancePage} from "./provenance-api"

test("provenance renders partial pins and uncertainty without implying verified results in three locales", async () => {
  const page: ProvenancePage = {items: [{id: "9007199254740993", execution_id: "21", producer_id: "9007199254740995", machine_id: "31", kind: "FILE", title: "<untrusted>", content_hash: "exact-bytes-hash", created_at: "2026-10-22T00:00:00Z", plan_hash: "plan-hash", base_commit: "a".repeat(40), output_state: "PARTIAL", comparison: "UNKNOWN", artifact: {binding_id: "11", space_id: "12", file_id: "13", version_id: "14", file_version: 3, criterion_id: "15", coverage: "PARTIAL", note: "incomplete bytes", sha256: "exact-bytes-hash"}}], has_more: false, comparison_truncated: true, verifiers_truncated: false, limitations: [], verifiers: []}
  for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><ProvenanceFacts page={page}/></IntlAppProvider>)
    for (const fact of ["9007199254740995", "exact-bytes-hash", "plan-hash", "incomplete bytes", "13 / 14 / 3"]) expect(html).toContain(fact)
    expect(html).toContain("&lt;untrusted&gt;"); expect(html).not.toContain("<untrusted>"); expect(html).not.toContain("provenance_"); expect(html).not.toContain("????")
  }
})
