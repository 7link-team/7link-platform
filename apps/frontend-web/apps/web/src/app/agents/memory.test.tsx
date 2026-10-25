import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { MemoryFacts } from "./memory"
import type { Memory } from "./memory-api"

it("shows persisted usage, expiry and risk without fabricating verification", async () => {
  const messages = await loadMessages("en-US")
  const memory: Memory = { id: "9", scope: "AGENT", agent_principal_id: "7", kind: "EXPERIENCE", state: "ACTIVE", title: "Keep evidence", body: "Retain verified test evidence.", provenance: "Execution output", content_hash: "a".repeat(64), version: 1, created_by: "7", created_at: "2026-10-22T00:00:00Z", ttl_days: 30, use_count: 4, expires_at: "2026-11-21T00:00:00Z", last_used_at: "2026-10-22T00:00:00Z", origin: "EXECUTION", source_execution_id: "90", risk_flags: ["AUTHORITY_CLAIM"] }
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><MemoryFacts memory={memory}/></IntlAppProvider>)
  expect(html).toContain("AUTHORITY_CLAIM")
  expect(html).toContain("2026-11-21T00:00:00Z")
  expect(html).toContain("90")
  expect(html).toContain("4")
  expect(html).not.toContain("VERIFIED")
})
