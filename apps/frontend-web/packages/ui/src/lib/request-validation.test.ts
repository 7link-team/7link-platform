import { describe, expect, it } from "bun:test"
import { ApiError } from "./api-client"
import {
  normalizeValidationIssues,
  renderValidationMessage,
  validationFormErrors,
} from "./request-validation"

describe("structured validation errors", () => {
  const issue = {
    in: "body" as const,
    path: "/name",
    code: "unique",
    message: "Already used",
  }
  it("retranslates matching tokens and preserves unsafe or unknown messages", () => {
    const localized = {
      ...issue,
      message_key: "name.unique",
      params: { limit: 3 },
    }
    const template = {
      code: "unique",
      message_key: "name.unique",
      template: "最多 {limit} 个",
    }
    expect(
      validationFormErrors([localized], new Set(["name"]), {
        "name.unique": template,
      })
    ).toEqual({ name: "最多 3 个" })
    for (const candidate of [
      { ...template, code: "different" },
      { ...template, message_key: "different" },
      { ...template, template: "Missing {unknown}" },
    ])
      expect(
        validationFormErrors([localized], new Set(["name"]), {
          "name.unique": candidate,
        })
      ).toEqual({ name: "Already used" })
    expect(
      validationFormErrors(
        [localized],
        new Set(["name"]),
        Object.create({ "name.unique": template })
      )
    ).toEqual({ name: "Already used" })
  })
  it("retains safe public details without rejected values", () => {
    expect(
      normalizeValidationIssues({
        validation_errors: [
          {
            ...issue,
            message_key: "name.unique",
            params: { max: 3, invalid: {}, infinite: Infinity },
            value: "secret",
            internal: "trace",
          },
          null,
          { ...issue, in: "unknown" },
          { ...issue, message: 123 },
        ],
      })
    ).toEqual([
      {
        ...issue,
        message_key: "name.unique",
        params: { max: 3 },
      },
    ])
    expect(normalizeValidationIssues(null)).toEqual([])
    expect(normalizeValidationIssues({ validation_errors: "wrong" })).toEqual(
      []
    )
  })
  it("binds exact escaped top-level pointers without splitting dotted property names", () => {
    const names = ["a.b", "a/b", "a~b", "0", "__proto__"]
    const paths = ["/a.b", "/a~1b", "/a~0b", "/0", "/__proto__"]
    const result = validationFormErrors(
      paths.map((path) => ({ ...issue, in: "body", path })),
      new Set(names)
    )
    expect(Object.keys(result).sort()).toEqual([...names].sort())
    for (const name of names) expect(result[name]).toBe("Already used")
    expect(Object.getPrototypeOf(result)).toBe(Object.prototype)
  })
  it("keeps nested, unknown, hidden, ambiguous and non-body issues visible globally", () => {
    const issues = [
      { ...issue, path: "/items/0/name", message: "Nested" },
      { ...issue, path: "/hidden", message: "Hidden" },
      { ...issue, path: "/bad~2name", message: "Malformed" },
      {
        ...issue,
        path: undefined,
        location: "body.name",
        message: "Ambiguous",
      },
      { ...issue, in: "query", message: "Query" },
    ]
    expect(
      validationFormErrors(
        normalizeValidationIssues({ validation_errors: issues }),
        new Set(["name"])
      )
    ).toEqual({
      _form: "Nested\nHidden\nMalformed\nAmbiguous\nQuery",
    })
  })
  it("preserves multiple rules for one field and deduplicates repeated messages", () => {
    expect(
      validationFormErrors(
        normalizeValidationIssues({
          validation_errors: [issue, issue, { ...issue, message: "Too short" }],
        }),
        new Set(["name"])
      )
    ).toEqual({ name: "Already used\nToo short" })
  })
  it("shared API errors preserve legacy fields alongside normalized issues", () => {
    const error = new ApiError("Invalid", 422, 422, "legacy", {
      validation_errors: [issue],
    })
    expect(error.field).toBe("legacy")
    expect(error.validationErrors).toEqual([issue])
    expect(new ApiError("Old", 400, 400).validationErrors).toEqual([])
  })
})

describe("validation message templates", () => {
  const template = (text: string) => ({
    code: "limit",
    message_key: "test.limit",
    template: text,
  })
  it("substitutes literal parameters once, including enum values", () => {
    expect(
      renderValidationMessage(template("Limit {max}; allowed {allowed}"), {
        max: 3,
        allowed: ["A", 0, false, null],
      })
    ).toBe('Limit 3; allowed ["A",0,false,null]')
    expect(
      renderValidationMessage(template("Value {value}"), {
        value: "{other} $& <b>text</b>",
      })
    ).toBe("Value {other} $& <b>text</b>")
  })
  it("rejects missing parameters, inherited properties and executable formatting", () => {
    for (const text of [
      "{unknown}",
      "{value.toString()}",
      "{value, number}",
      "{{value}}",
      "{__proto__}",
      "{constructor}",
    ])
      expect(
        renderValidationMessage(template(text), { value: 1 })
      ).toBeUndefined()
    expect(
      renderValidationMessage(
        template("{value}"),
        Object.create({ value: "hidden" })
      )
    ).toBeUndefined()
    for (const value of [{}, undefined, Infinity, [Infinity], [undefined]])
      expect(
        renderValidationMessage(template("{value}"), { value })
      ).toBeUndefined()
    expect(
      renderValidationMessage({ template: "unsafe shape" })
    ).toBeUndefined()
  })
})
