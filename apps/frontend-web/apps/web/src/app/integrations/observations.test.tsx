import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import ui from "../../../../../packages/ui/src/i18n/messages/en-US.json"
import {
  ObservationEvidence,
  type Observation,
} from "./observations"
import { observationsPath } from "./api"
import { observationMessages } from "./messages"

const observation: Observation = {
  id: "9007199254740993",
  outcome: "REFERENCE_ONLY",
  observation: {
    event_id: "9007199254740994",
    source: "FLOW",
    repository: "",
    pipeline_id: "release",
    run_id: "run-1",
    commit: "a".repeat(40),
    kind: "pipeline",
    title: "",
    references: ["#WI-142"],
    authority_source: "EXTERNAL:YUNXIAO_FLOW:release",
    trusted_verification: false,
    local_acceptance: false,
    stage_results: [
      {
        name: "integration",
        status: "PASSED",
        redlines: [
          {
            metric: "escape_rate",
            op: "<=",
            threshold: "0.01",
            value: "0.000000000000000001",
            passed: true,
          },
        ],
      },
    ],
    manual_gates: [
      {
        name: "release approval",
        mode: "AND",
        approvers: ["external-user"],
        result: "PASSED",
      },
    ],
  },
}

test("signed evidence preserves source identity, exact decimal and readonly acceptance limits in three locales", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    const html = renderToStaticMarkup(
      <ObservationEvidence rows={[observation]} locale={locale} />
    )
    for (const value of [
      "EXTERNAL:YUNXIAO_FLOW:release",
      "0.000000000000000001",
      "0.01",
      "external-user",
      "AND",
      "#WI-142",
      observationMessages[locale].gates,
      observationMessages[locale].reference,
      observationMessages[locale].passed,
    ])
      expect(html).toContain(value)
    expect(html).not.toContain("<button")
    expect(html).not.toContain("<input")
    expect(html).not.toContain("<form")
    expect(html).toContain("overflow-wrap:anywhere")
  }
})
test("source content is escaped and an invalid verification or local-acceptance claim fails closed", () => {
  const hostile = structuredClone(observation)
  hostile.observation.title = '<script>alert("source")</script>'
  expect(
    renderToStaticMarkup(<ObservationEvidence rows={[hostile]} />)
  ).not.toContain("<script>")
  for (const field of ["trusted_verification", "local_acceptance"] as const) {
    const invalid = structuredClone(observation)
    Object.assign(invalid.observation, { [field]: true })
    const html = renderToStaticMarkup(
      <IntlAppProvider initialLocale="en-US" initialMessages={ui}>
        <ObservationEvidence rows={[invalid]} />
      </IntlAppProvider>
    )
    expect(html).toContain(observationMessages["en-US"].invalid)
    expect(html).not.toContain("external-user")
  }
})
test("observation paths encode scope independently and empty states remain translated", () => {
  expect(observationsPath("1/x", "2/y", "3/z")).toBe(
    "/tenants/1%2Fx/integration-connections/2%2Fy/project-mappings/3%2Fz/observations"
  )
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    expect(
      renderToStaticMarkup(<ObservationEvidence rows={[]} locale={locale} />)
    ).toContain(observationMessages[locale].empty)
    expect(Object.keys(observationMessages[locale])).toEqual(
      Object.keys(observationMessages["en-US"])
    )
  }
})
