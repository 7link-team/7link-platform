import {describe,expect,it} from "bun:test"
import {calendarPeriods,calendarEnd} from "./calendar"
describe("report calendar boundaries",()=>{
 it("bounds fetches to the final visible period",()=>{expect(calendarEnd("DAY","2026-12-31")).toBe("2027-01-01");expect(calendarEnd("WEEK","2026-12-28")).toBe("2027-01-04");expect(calendarEnd("MONTH","2026-12-01")).toBe("2027-01-01")})
 it("uses the tenant timezone at the week boundary",()=>{const now=new Date("2026-10-19T00:30:00Z");expect(calendarPeriods("DAY",now,"America/Los_Angeles")[0]).toBe("2026-10-12");expect(calendarPeriods("DAY",now,"Asia/Kuala_Lumpur")[0]).toBe("2026-10-19")})
 it("uses Monday for this week and spans calendar year boundaries",()=>{expect(calendarPeriods("DAY",new Date(2027,0,3))).toEqual(["2026-12-28","2026-12-29","2026-12-30","2026-12-31","2027-01-01","2027-01-02","2027-01-03"])})
 it("shows eight Monday anchored weeks",()=>{const periods=calendarPeriods("WEEK",new Date(2026,9,21));expect(periods).toHaveLength(8);expect(periods[0]).toBe("2026-08-31");expect(periods.at(-1)).toBe("2026-10-19")})
 it("includes twelve whole month anchors",()=>{const periods=calendarPeriods("MONTH",new Date(2026,9,31));expect(periods).toHaveLength(12);expect(periods[0]).toBe("2025-11-01");expect(periods.at(-1)).toBe("2026-10-01")})
})
