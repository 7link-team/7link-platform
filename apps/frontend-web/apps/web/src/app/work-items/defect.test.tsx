import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { DefectReportFields } from "./defect"
import { defectMessages, type DefectReport } from "./defect-data"
import { loadMessages } from "../../lib/i18n"

const report: DefectReport = { work_item_id: "9007199254740993", version: 7, type: "BUG", severity: "CRITICAL", priority: "HIGH", reproducibility: "ALWAYS", found_version: "v1", fixed_version: null, introduced_stage: null, introduced_reason: null, module: null, details: { environment: "staging", steps: "Open report", expected: "Loads", actual: "Fails", root_cause: "", fix_suggestion: "", resolution: "DUPLICATE", duplicate_of_work_item_id: "9007199254740995" } }
describe("structured defect report", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) it(`renders every fact with required constraints and readonly controls in ${locale}`, async () => {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><DefectReportFields report={report} onChange={() => {}} disabled locale={locale} /></IntlAppProvider>)
    for (const key of ["environment", "steps", "expected", "actual", "root_cause", "fix_suggestion", "resolution", "duplicate_of_work_item_id", "found_version", "fixed_version", "introduced_stage", "introduced_reason", "module"] as const) expect(html).toContain(defectMessages[locale][key])
    expect(html).toContain("disabled"); expect(html).toContain("required"); expect(html).toContain("maxLength=\"20000\""); expect(html).toContain("9007199254740995"); expect(html).toContain("staging"); expect(html).toContain("Open report")
  })
})
