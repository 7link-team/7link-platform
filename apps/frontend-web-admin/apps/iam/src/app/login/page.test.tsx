import { describe, expect, it } from "bun:test"
import { readFileSync } from "node:fs"

// The sign-in steps run in form handlers that static markup cannot submit, so
// this pins that each one maps its failure through loginError, which has its own
// tests. Flattening a step's failure to one message is how asking for a phone
// code came to report a mistyped captcha as the identity service being down.
describe("login failure messages", () => {
  const source = readFileSync(new URL("./page.tsx", import.meta.url), "utf8")
  const handler = (name: string) => {
    const start = source.indexOf(`const ${name} = `)
    expect(start).toBeGreaterThan(-1)
    return source.slice(start, source.indexOf("\n  }\n", start))
  }
  it("maps every sign-in step's failure through loginError", () => {
    expect(handler("submitPassword")).toContain("t(loginError(cause))")
    expect(handler("requestPhoneCode")).toContain("t(loginError(cause))")
    expect(handler("submitPhoneCode")).toContain(
      't(loginError(cause, "invalidPhoneCode"))'
    )
    expect(handler("submitMfa")).toContain(
      't(loginError(cause, "invalidChallenge"))'
    )
  })
})
