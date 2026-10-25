import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import type { VerifierResult } from "./api"
import { VerifierResultRows } from "./verifier-results"

for (const locale of ["en-US", "zh-CN", "ms-MY"])
  test(`verifier receipts preserve limitations and advisory calibration in ${locale}`, async () => {
    const messages = await loadMessages(locale)
    const row: VerifierResult = {
      task_id: "10",
      result_id: "20",
      criterion_id: "30",
      verifier_key: "unit-tests",
      kind: "MODEL_SCORED",
      runner: "AGENT_JUDGE",
      stage: "AGENT_TEST",
      verdict: "PASSED",
      reason: "REVIEW_COMPLETED",
      params_resolved: { threshold: 70 },
      metrics: { exit_code: 0 },
      evidence_refs: ["verification-receipt:10:actual-hash"],
      limitations: "Only this exact artifact was checked.",
      tool_version: "platform-verification-v1",
      calibration_state: "UNCALIBRATED",
      template_pin: {
        template_version_id: "77",
        definition_hash: "original-published-hash",
        verifier: { verifier_key: "unit-tests", version: 1, plugin_key: "http-v1", required_tools: ["browser"] },
      },
    }
    const html = renderToStaticMarkup(
      <IntlAppProvider initialLocale={locale} initialMessages={messages}>
        <VerifierResultRows rows={[row]} />
      </IntlAppProvider>
    )
    const strings = messages.iam as Record<string, string>
    expect(html).toContain(strings.bindingVerifierVerdict_PASSED!)
    expect(html).toContain(strings.bindingVerifierAdvisory!)
    expect(html).toContain("original-published-hash")
    expect(html).toContain("verification-receipt:10:actual-hash")
    expect(html).toContain(row.limitations)
    expect(html).toContain("http-v1")
    expect(html).toContain("browser")
    expect(html).toContain(strings.templateVerifierProtocol!)
    expect(html).not.toContain("INCONCLUSIVE")
  })
