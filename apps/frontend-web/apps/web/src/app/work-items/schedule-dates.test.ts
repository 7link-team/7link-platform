import { describe, expect, it } from "bun:test"
import { calendarRange, overlapsDay } from "./schedule-dates"
describe("work scheduling dates", () => {
  it("uses calendar boundaries including leap days and weekly alignment", () => {
    const leap = calendarRange("2028-02-17", "month")
    expect(leap.days).toHaveLength(29)
    expect(leap.start.getDate()).toBe(1)
    expect(leap.end.getMonth()).toBe(2)
    const week = calendarRange("2026-11-01", "week")
    expect(week.days).toHaveLength(7)
    expect(week.start.getDay()).toBe(1)
    expect(week.days[6]!.getDay()).toBe(0)
  })
  it("uses half-open intervals and includes tasks spanning the day", () => {
    const day = new Date("2026-10-20T00:00:00")
    expect(overlapsDay(null, null, day)).toBe(false)
    expect(overlapsDay("2026-10-19T00:00:00", "2026-10-20T00:00:00", day)).toBe(false)
    expect(overlapsDay("2026-10-21T00:00:00", "2026-10-22T00:00:00", day)).toBe(false)
    expect(overlapsDay("2026-10-19T00:00:00", "2026-10-22T00:00:00", day)).toBe(true)
  })
})
