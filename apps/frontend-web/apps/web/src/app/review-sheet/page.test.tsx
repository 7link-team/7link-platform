import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { RequirementReviewFacts } from "./page"
import { reviewSheetMessages } from "./messages"
import en from "../../messages/en-US.json"
import ui from "@workspace/ui/i18n/messages/en-US.json"

it("renders the actual requirement verdict separately from business status in all locales", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    const l = reviewSheetMessages[locale]
    const html = renderToStaticMarkup(
      <IntlAppProvider
        initialLocale={locale}
        initialMessages={{ ...ui, iam: en }}
      >
        <RequirementReviewFacts
          requirement={{
            status: "DRAFT",
            version: 4,
            review: { state: "PASSED", sheet_id: "9007199254740993" },
          }}
          frozenVersion={4}
        />
      </IntlAppProvider>
    )
    expect(html).toContain(l.businessStatus)
    expect(html).toContain("DRAFT")
    expect(html).toContain(l.currentVerdict)
    expect(html).toContain("PASSED")
    expect(html).toContain("9007199254740993")
    expect(html).toContain(l.verdictStatusPolicy)
    expect(html).not.toContain(l.outdatedSnapshot)
  }
})

it("does not substitute an old sheet verdict when the current requirement version has no review", () => {
  const l = reviewSheetMessages["en-US"]
  const html = renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ ...ui, iam: en }}>
      <RequirementReviewFacts
        requirement={{ status: "BASELINED", version: 5 }}
        frozenVersion={4}
      />
    </IntlAppProvider>
  )
  expect(html).toContain("BASELINED")
  expect(html).toContain(l.noCurrentReview)
  expect(html).toContain(l.outdatedSnapshot)
  expect(html).not.toContain("PASSED")
})

it("renders persisted lifecycle labels for approval, pending review and requested changes in every locale", () => {
 for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
  const l = reviewSheetMessages[locale]
  for (const [state, label] of [["APPROVED", l.approved], ["REVIEW_PENDING", l.reviewPending], ["NEEDS_WORK", l.needsWork]] as const) {
   const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{ ...ui, iam: en }}><RequirementReviewFacts requirement={{status: "BASELINED", version: 4, review_state: state}} frozenVersion={4} /></IntlAppProvider>)
   expect(html).toContain(l.lifecycle)
   expect(html).toContain(label)
  }
 }
})
