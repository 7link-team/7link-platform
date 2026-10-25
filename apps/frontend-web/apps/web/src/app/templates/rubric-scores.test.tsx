import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { loadMessages } from "../../lib/i18n"
import { RubricComparison } from "./rubric-scores"
import { rubricRoute, rubricStages, type RubricBoard, type RubricReceipt } from "./api"

test("rubric comparison retains all three stages, frozen version, missing scores and exact divergence in three locales", async () => {
 const receipt = (id: string, stage: RubricReceipt["stage"], score: number): RubricReceipt => ({id, template_version_id: "90", definition_hash: "frozen-pin", stage, scorer_kind: stage === "CLIENT_ACCEPTANCE" ? "CLIENT" : "HUMAN", scores: [{dimension_key: "quality", score, evidence_refs: [], note: "Actual assessment"}], calculation: {total: score, passed: score >= 3.5, weighted_min: 3.5}, created_at: "2026-10-23T00:00:00Z"})
 const board: RubricBoard = {template_version_id: "90", definition_hash: "frozen-pin", work_version: 3, evidence_refs: [], definition: {dimensions: [{dimension_key: "quality", kind: "WEIGHTED", weight: 1}, {dimension_key: "tests", kind: "HARD_GATE", verifier_keys: ["unit-tests"]}], pass_rule: {hard_gates: "all_pass", weighted_min: 3.5}}, receipts: [receipt("3", "CLIENT_ACCEPTANCE", 2), receipt("2", "AGENT_ACCEPTANCE", 4)], divergences: [{dimension: "quality", left_id: "2", right_id: "3", left_stage: "AGENT_ACCEPTANCE", right_stage: "CLIENT_ACCEPTANCE", delta: "2"}], allowed_stages: [], availability: "AVAILABLE", acceptance_effect: "NONE"}
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const messages = await loadMessages(locale)
  const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RubricComparison board={board}/></IntlAppProvider>)
  expect(html).toContain("frozen-pin");expect(html).toContain("\u22651.0");expect(html).toContain("Actual assessment");expect(html).toContain("<caption");expect(html.match(/scope="col"/g)).toHaveLength(4)
  for (const stage of rubricStages) expect(html).toContain(String((messages.iam as Record<string, string> | undefined)?.[`rubricStage_${stage}`] ?? ""))
  expect(html).not.toContain("????");expect(html).not.toContain("iam.rubric");expect(html).not.toContain("NaN")
  expect(html).not.toContain(" / CLIENT");expect(html).not.toContain(" / HUMAN");expect(html).not.toContain(" / AGENT");
  expect(html).toContain("3.5");expect(html).not.toContain("data-verdict");expect(html).toContain('role="status"')
 }
})
test("rubric routes pin entity and public package item without private result IDs", () => {
 expect(rubricRoute("3", "11")).toBe("/entities/3/work-items/11/rubric-scores")
 expect(rubricRoute("3", undefined, "99", "a".repeat(64))).toBe(`/entities/3/delivery-packages/99/rubric-scores?item_id=${"a".repeat(64)}`)
 expect(rubricRoute("3/x", "11/y")).toContain("3%2Fx/work-items/11%2Fy")
})
