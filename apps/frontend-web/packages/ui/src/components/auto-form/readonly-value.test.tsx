import { describe, expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"

import { ReadonlyValue } from "./readonly-value"
import type { FieldComponentConfig, FieldType } from "./types"

const render = (
  type: FieldType,
  value: unknown,
  config: FieldComponentConfig = {}
) =>
  renderToStaticMarkup(
    <ReadonlyValue type={type} value={value} config={config} />
  )

describe("read-only field values", () => {
  it("does not reveal a supplied password in text or attributes", () => {
    const html = render("password", "private-secret-value")
    expect(html).not.toContain("private-secret-value")
    expect(html).toContain("••••••••")
  })

  it.each(["select", "radio", "multi-select", "checkbox-group"] as const)(
    "%s resolves both supported option shapes",
    (type) => {
      const value =
        type === "select" || type === "radio" ? "reviewer" : ["reviewer"]
      for (const options of [
        { reviewer: "Reviewer" },
        [{ value: "reviewer", label: "Reviewer" }],
      ]) {
        expect(render(type, value, { options })).toContain("Reviewer")
      }
    }
  )

  it.each(["multi-select", "checkbox-group"] as const)(
    "%s preserves the empty-value placeholder",
    (type) => {
      for (const value of [[], null, undefined, ""]) {
        expect(render(type, value)).toContain(
          'class="text-muted-foreground">—</span>'
        )
      }
    }
  )

  it("keeps rich option labels readable without coercing React nodes to strings", () => {
    expect(
      render("select", "reviewer", {
        options: [{ value: "reviewer", label: <strong>Reviewer</strong> }],
      })
    ).toContain("<strong>Reviewer</strong>")
  })

  it.each([0, 2, 3])(
    "uses the same minor-unit scale as the price editor (%s decimals)",
    (decimals) => {
      const expected = ["1250", "12.50", "1.250"][
        decimals === 0 ? 0 : decimals === 2 ? 1 : 2
      ]
      expect(render("price", 1250, { decimals })).toContain(expected!)
    }
  )

  it.each(["dateRange", "datetimeRange", "timeRange"] as const)(
    "%s shows both endpoints rather than an object representation",
    (type) => {
      const html = render(type, { start: "2026-10-15", end: "2026-10-18" })
      expect(html).toContain("2026-10-15")
      expect(html).toContain("2026-10-18")
      expect(html).not.toContain("[object Object]")
    }
  )
})
