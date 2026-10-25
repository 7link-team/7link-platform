import { expect, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { EffortFields } from "./effort"
import { effortMessages } from "./effort-data"
test("task calibration exposes date, timezone, category, note and reason with readonly boundaries in three locales", () => {
  for (const locale of ["en-US", "zh-CN", "ms-MY"] as const) {
    const html = renderToStaticMarkup(<EffortFields value={{ work_item_id: "9007199254740993", work_date: "2026-10-20", timezone: "Asia/Kuala_Lumpur", category: "REVIEW", minutes: 45, note: "Reviewed", reason: "Actual participation" }} onChange={() => {}} disabled locale={locale} />)
    for (const value of ["9007199254740993", "Asia/Kuala_Lumpur", "2026-10-20", "Actual participation", "Reviewed", effortMessages[locale].timezone, effortMessages[locale].reason]) expect(html).toContain(value)
    expect(html).toContain("readOnly"); expect(html).toContain("disabled"); expect(html).toContain('max="1440"'); expect(html).toContain('maxLength="500"')
    expect(html).not.toContain("AGENT_RUNTIME")
  }
})
