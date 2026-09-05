import { describe, expect, it } from "bun:test"
import { appName, copyrightLine, organizationName } from "./branding"

describe("branding", () => {
  it("falls back to a neutral console name when none is configured", () => {
    expect(appName).toBe("IAM")
  })

  it("names no organization when none is configured", () => {
    expect(organizationName).toBe("")
  })

  it("omits the separator when no organization is configured", () => {
    expect(copyrightLine(2026)).toBe("© 2026")
  })

  it("reads the year at render time rather than baking it in", () => {
    expect(copyrightLine()).toBe(`© ${new Date().getFullYear()}`)
  })

  it("carries no brand name anywhere in its output", () => {
    const rendered = `${appName} ${organizationName} ${copyrightLine(2026)}`
    expect(rendered.toLowerCase()).not.toContain("chaosplus")
  })
})
