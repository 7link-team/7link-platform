import { describe, expect, it } from "bun:test"
import { createTranslator } from "use-intl"
import en from "../../i18n/messages/en-US.json"
import zh from "../../i18n/messages/zh-CN.json"
import ms from "../../i18n/messages/ms-MY.json"
import {
  compileRequestContract,
  type OperationRequestContract,
} from "./contract"

const message = (key: string, params?: Record<string, string | number>) =>
  `${key}:${params?.limit ?? ""}`
export function requestContract(
  properties: Record<string, unknown>,
  required: string[] = []
): OperationRequestContract {
  return {
    version: "v1",
    operation_id: "create",
    method: "POST",
    path: "/items",
    schemas: {
      Body: {
        type: "object",
        properties,
        required,
        additionalProperties: false,
      },
    },
    request_body: {
      content: {
        "application/json": { schema: { $ref: "#/components/schemas/Body" } },
      },
    },
  }
}

describe("request contract validation", () => {
  it("uses server templates, node overrides and actual schema limits", () => {
    const item = (template: string) => ({
      code: "rule",
      message_key: "server.rule",
      template,
    })
    const contract = requestContract(
      {
        name: {
          type: "string",
          maxLength: 8,
          "x-field-rules": [{ rule: "utf8-bytes", args: { max: 3 } }],
          "x-field-messages": {
            "custom:maxBytes": item("字段最多 {max} 字节"),
          },
        },
        choice: { type: "string", enum: ["A", "B"] },
      },
      ["name"]
    )
    contract.messages = {
      type: item("类型错误"),
      required: item("缺少 {property}"),
      maxLength: item("最多 {max} 字符"),
      "custom:maxBytes": item("最多 {max} 字节"),
      enum: item("只能选择 {allowed}"),
    }
    const compiled = compileRequestContract(contract, message)
    expect(compiled.unsupported).toEqual([])
    expect(compiled.messageIssues).toEqual([])
    for (const [input, expected] of [
      [{ name: "中文" }, "字段最多 3 字节"],
      [{ name: "123456789" }, "最多 8 字符"],
      [{}, "缺少 name"],
      [{ name: "A", choice: "C" }, '只能选择 ["A","B"]'],
    ] as const) {
      const result = compiled.schema.safeParse(input)
      expect(result.success).toBe(false)
      if (!result.success) expect(result.error.issues[0].message).toBe(expected)
    }
  })
  it("records missing or invalid templates without dropping validation", () => {
    const contract = requestContract({ name: { type: "string", maxLength: 1 } })
    contract.messages = {
      maxLength: {
        code: "max",
        message_key: "server.max",
        template: "Limit {unknown}",
      },
    }
    const compiled = compileRequestContract(contract, message)
    const result = compiled.schema.safeParse({ name: "AB" })
    expect(result.success).toBe(false)
    if (!result.success) expect(result.error.issues[0].message).toBe("invalid:")
    expect(compiled.messageIssues).toContainEqual({
      path: "name",
      rule: "message:maxLength",
    })
    expect(compiled.schema.parse({ name: "A" })).toEqual({ name: "A" })
  })

  it("checks UTF8 bytes using Go whitespace without mutating raw values", () => {
    const compiled = compileRequestContract(
      requestContract(
        {
          name: {
            type: "string",
            maxLength: 128,
            "x-field-rules": [
              {
                rule: "utf8-bytes",
                args: { min: 1, max: 128, normalize: "trim-space" },
              },
            ],
          },
        },
        ["name"]
      ),
      message
    )
    expect(compiled.unsupported).toEqual([])
    for (const name of [
      "中".repeat(42) + "ab",
      "😀".repeat(32),
      "\u0085 A\u3000",
      "\uFEFF",
    ]) {
      const input = { name }
      expect(compiled.schema.parse(input)).toEqual(input)
      expect(input.name).toBe(name)
    }
    for (const [name, error] of [
      ["中".repeat(43), "maxBytes:128"],
      ["😀".repeat(33), "maxBytes:128"],
      ["\u0085\u3000", "minBytes:1"],
      [" " + "a".repeat(128) + " ", "maxLength:128"],
    ]) {
      const parsed = compiled.schema.safeParse({ name })
      expect(parsed.success).toBe(false)
      if (!parsed.success) expect(parsed.error.issues[0].message).toBe(error)
    }
  })
  it("preserves optional nullable untrimmed fields and JSON surrogate byte semantics", () => {
    const compiled = compileRequestContract(
      requestContract({
        description: {
          type: ["string", "null"],
          "x-field-rules": [{ rule: "utf8-bytes", args: { max: 3 } }],
        },
      }),
      message
    )
    expect(compiled.unsupported).toEqual([])
    for (const input of [
      {},
      { description: null },
      { description: "中" },
      { description: "" },
      { description: "\uD800" },
    ])
      expect(compiled.schema.parse(input)).toEqual(input)
    for (const description of [" 中 ", "\uD800a"])
      expect(compiled.schema.safeParse({ description }).success).toBe(false)
  })
  it("rejects malformed byte rules rather than silently dropping constraints", () => {
    for (const extension of [
      null,
      {},
      [null],
      [{ rule: "unknown", args: {} }],
      ...[-1, 1.5, "3", Number.MAX_SAFE_INTEGER + 1].map((max) => [
        { rule: "utf8-bytes", args: { max } },
      ]),
      [{ rule: "utf8-bytes", args: { min: 4, max: 3 } }],
      [{ rule: "utf8-bytes", args: { normalize: "trim" } }],
      [{ rule: "utf8-bytes", args: { max: 3, extra: true } }],
      [{ rule: "utf8-bytes", args: {}, script: "execute" }],
    ]) {
      const compiled = compileRequestContract(
        requestContract({
          name: { type: "string", "x-field-rules": extension },
        }),
        message
      )
      expect(compiled.unsupported.length).toBeGreaterThan(0)
      expect(compiled.schema.safeParse({}).success).toBe(false)
    }
    const compiled = compileRequestContract(
      requestContract({ value: { type: "number", "x-field-rules": [] } }),
      message
    )
    expect(compiled.unsupported.length).toBeGreaterThan(0)
  })
  it("formats rule parameters in all three deployment languages", () => {
    for (const [locale, messages] of [
      ["en-US", en],
      ["zh-CN", zh],
      ["ms-MY", ms],
    ] as const) {
      const translate = createTranslator({
        locale,
        messages,
        namespace: "form.contract",
      })
      const compiled = compileRequestContract(
        requestContract({ name: { type: "string", maxLength: 1 } }),
        (key, params) =>
          translate(key as "maxLength", params as { limit: number })
      )
      const checked = compiled.schema.safeParse({ name: "AB" })
      expect(checked.success).toBe(false)
      if (!checked.success)
        expect(checked.error.issues[0].message).toBe(
          translate("maxLength", { limit: 1 })
        )
    }
  })
  it("does not let nullable bypass an enum constraint", () => {
    const compiled = compileRequestContract(
      requestContract({ value: { type: ["string", "null"], enum: ["A"] } }),
      message
    )
    expect(compiled.schema.safeParse({ value: null }).success).toBe(false)
    expect(compiled.schema.safeParse({ value: "A" }).success).toBe(true)
  })
  it("uses server constraints, counts Unicode code points and enforces strict Go end anchors", () => {
    const compiled = compileRequestContract(
      requestContract(
        {
          name: { type: "string", minLength: 1, maxLength: 2 },
          code: { type: "string", pattern: "^[A-Z][A-Z0-9-]{1,15}$" },
        },
        ["name", "code"]
      ),
      message
    )
    expect(compiled.unsupported).toEqual([])
    expect(
      compiled.schema.safeParse({ name: "😀中", code: "AA" }).success
    ).toBe(true)
    expect(
      compiled.schema.safeParse({ name: "😀中文", code: "AA" }).success
    ).toBe(false)
    expect(compiled.schema.safeParse({ name: "A", code: "AA\n" }).success).toBe(
      false
    )
    expect(compiled.schema.safeParse({ name: "A" }).success).toBe(false)
    expect([...compiled.requiredFields]).toEqual(["name", "code"])
  })
  it("preserves null, omission and false and does not apply defaults", () => {
    const compiled = compileRequestContract(
      requestContract({
        deadline: { type: ["string", "null"], format: "date" },
        enabled: { type: "boolean", default: true },
      }),
      message
    )
    expect(compiled.schema.parse({})).toEqual({})
    expect(compiled.schema.parse({ deadline: null, enabled: false })).toEqual({
      deadline: null,
      enabled: false,
    })
    expect(compiled.schema.safeParse({ deadline: "2024-02-29" }).success).toBe(
      true
    )
    expect(compiled.schema.safeParse({ deadline: "2025-02-29" }).success).toBe(
      false
    )
    expect(compiled.schema.safeParse({ deadline: "" }).success).toBe(false)
  })
  it("converts numeric input only when parsing and rejects lossy integers and incomplete input", () => {
    const compiled = compileRequestContract(
      requestContract({
        amount: { type: "integer", format: "int64", minimum: 1 },
      }),
      message
    )
    const input = { amount: "2500" }
    expect(compiled.schema.parse(input)).toEqual({ amount: 2500 })
    expect(input.amount).toBe("2500")
    expect(compiled.inputLimits.amount).toEqual({ min: 1, step: 1 })
    for (const amount of [
      "9007199254740993",
      "1.0000000000000001",
      9007199254740992,
      "1.5",
      "-",
      "",
      " ",
      "Infinity",
    ])
      expect(compiled.schema.safeParse({ amount }).success).toBe(false)
  })
  it("checks nested arrays and refuses unknown rules, incompatible regex and missing refs", () => {
    const compiled = compileRequestContract(
      requestContract({
        rows: {
          type: "array",
          maxItems: 1,
          items: {
            type: "object",
            properties: { code: { type: "string", enum: ["A"] } },
            required: ["code"],
            additionalProperties: false,
          },
        },
      }),
      message
    )
    expect(compiled.schema.safeParse({ rows: [{ code: "A" }] }).success).toBe(
      true
    )
    expect(compiled.schema.safeParse({ rows: [{ code: "B" }] }).success).toBe(
      false
    )
    expect(
      compiled.schema.safeParse({ rows: [{ code: "A" }, { code: "A" }] })
        .success
    ).toBe(false)
    for (const schema of [
      { type: "string", pattern: "(?i)hello" },
      { type: "string", format: "unknown" },
      { type: "number", multipleOf: 0.01 },
      { $ref: "#/components/schemas/Missing" },
    ]) {
      const unsupported = compileRequestContract(
        requestContract({ value: schema }),
        message
      )
      expect(unsupported.unsupported.length).toBeGreaterThan(0)
      expect(unsupported.schema.safeParse({}).success).toBe(false)
    }
  })
})
