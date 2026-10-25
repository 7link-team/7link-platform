import {expect, it} from "bun:test"
import {homeMessages, homeMetricValue} from "./messages"

it("formats exact large micros and actual ratios without inventing unavailable metrics", () => {
  expect(homeMetricValue("COST_PER_ACCEPTED", "9007199254740993.000000", "unknown")).toBe("$9007199254.74")
  expect(homeMetricValue("FIRST_PASS_RATE", "0.733333", "unknown")).toBe("73.3%")
  expect(homeMetricValue("ATTENTION_MIN_PER_ACCEPTED", null, "unknown")).toBe("unknown")
  expect(homeMetricValue("REWORK_P50_HOURS", ">336", "unknown")).toBe(">336")
  for (const labels of Object.values(homeMessages)) {
    expect(Object.keys(labels).sort()).toEqual(Object.keys(homeMessages["en-US"]).sort())
    for (const value of Object.values(labels)) expect(value).not.toContain("???")
  }
})
