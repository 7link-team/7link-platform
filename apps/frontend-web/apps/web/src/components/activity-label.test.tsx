import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { ActivityLabel } from "./activity-label"
import en from "../messages/en-US.json"
import zh from "../messages/zh-CN.json"
import ms from "../messages/ms-MY.json"

it("provides readable activity labels and explanations in every deployment locale", () => {
  for (const messages of [en, zh, ms]) {
    for (const key of [
      "jobsDescription",
      "jobsScheduleDescription",
      "eventsDescription",
      "activityUnregistered",
      "activityModule",
      "activityEverySeconds",
      "activityScope",
      "activityEachTenant",
      "activityPlatform",
      "activityTriggers",
    ] as const) {
      expect(messages[key].trim()).not.toBe("")
      expect(messages[key]).not.toMatch(/[?\uFFFD]/)
    }
  }
  const html = renderToStaticMarkup(
    <IntlAppProvider initialLocale="zh-CN" initialMessages={{ iam: zh }}>
      <ActivityLabel code="removed.handler" />
    </IntlAppProvider>
  )
  expect(html).toContain("未注册的活动")
})

it("puts the readable activity and purpose before its stable technical identity", () => {
  const html = renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ iam: en }}>
      <ActivityLabel
        code="governance.seed_reconcile"
        display_name="Synchronize roles"
        description="Governance: apply declared roles."
        module="business_governance"
      />
    </IntlAppProvider>
  )
  expect(html.indexOf("Synchronize roles")).toBeLessThan(
    html.indexOf("governance.seed_reconcile")
  )
  expect(html).toContain("Governance: apply declared roles.")
  expect(html).toContain("business_governance")
})

it("keeps historical undeclared activities inspectable without inventing their purpose", () => {
  const html = renderToStaticMarkup(
    <IntlAppProvider initialLocale="en-US" initialMessages={{ iam: en }}>
      <ActivityLabel code="removed.handler" />
    </IntlAppProvider>
  )
  expect(html).toContain(en.activityUnregistered)
  expect(html).toContain("removed.handler")
  expect(html).not.toContain("Module:")
})
