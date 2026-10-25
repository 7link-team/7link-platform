import { describe, expect, it } from "bun:test"
import { historicalRate, metricsMessages } from "./messages"

describe("historical metrics", () => {
  it("distinguishes an absent denominator from a factual zero rate", () => {
    expect(historicalRate(null, "Unavailable")).toBe("Unavailable")
    expect(historicalRate(undefined, "Unavailable")).toBe("Unavailable")
    expect(historicalRate(0, "Unavailable")).toBe("0.0%")
    expect(historicalRate(2 / 3, "Unavailable")).toBe("66.7%")
  })
  it("has complete concrete labels for all three locales", () => {
    const keys = Object.keys(metricsMessages["en-US"]).sort()
    for (const messages of Object.values(metricsMessages)) {
      expect(Object.keys(messages).sort()).toEqual(keys)
      for (const value of Object.values(messages)) expect(value.trim().length).toBeGreaterThan(0)
    }
    expect(metricsMessages["zh-CN"].hint).toMatch(/[\u4e00-\u9fff]/)
  })
})
