import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"

describe("delegation translations", () => {
  const source = readFileSync(new URL("./delegations.tsx", import.meta.url), "utf8")
  const keys = [...source.matchAll(/\bt\("(delegation\w+)"\)/g)].map(match => match[1]!)
  it("provides every delegation label and lifecycle state in all three locales", () => {
    for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
      const messages = JSON.parse(readFileSync(new URL(`../../messages/${locale}.json`, import.meta.url), "utf8"))
      for (const key of [...keys, ...["ACTIVE", "REVOKED", "EXPIRED"].map(state => `delegationState_${state}`)]) expect(typeof messages[key]).toBe("string")
    }
  })
})
