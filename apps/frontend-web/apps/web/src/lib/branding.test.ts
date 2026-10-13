import { describe, expect, it } from "bun:test"
import { branding, copyrightLine, loadBranding } from "./branding"

describe("branding", () => {
  it("falls back to a neutral console name when none is configured", () => {
    // Neutral wording, not whichever brand happened to be first. A build that
    // has not been told a name does not invent one.
    expect(branding().name).toBe("IAM")
  })

  it("names no organization when none is configured", () => {
    expect(branding().organization).toBe("")
  })

  it("omits the separator when no organization is configured", () => {
    expect(copyrightLine("", 2026)).toBe("© 2026")
  })

  it("names the organization when one is configured", () => {
    expect(copyrightLine("Acme", 2026)).toBe("Acme · © 2026")
  })

  it("reads the year at render time rather than baking it in", () => {
    expect(copyrightLine()).toBe(`© ${new Date().getFullYear()}`)
  })

  // The console has to render when the service cannot be reached: a display
  // name nobody could fetch is not a reason to show nobody the login form.
  it("keeps the last known identity when the service cannot be reached", async () => {
    const before = branding()
    await loadBranding()
    expect(branding()).toEqual(before)
  })
})
