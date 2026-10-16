import { z, type ZodType } from "zod"
import {
  renderValidationMessage,
  type ValidationMessage,
} from "../../lib/request-validation"

/** Wire shape of OPTIONS operation metadata. No executable code is accepted. */
export interface OperationRequestContract {
  locale?: string
  messages?: Record<string, ValidationMessage>
  issue_messages?: Record<string, ValidationMessage>
  version: string
  operation_id: string
  method: string
  path: string
  request_body: { content?: Record<string, { schema?: unknown }> } | null
  schemas: Record<string, unknown>
}
export interface ContractIssue {
  path: string
  rule: string
}
export type ContractMessage = (
  key: string,
  params?: Record<string, string | number>
) => string

const annotations = new Set([
  "title",
  "description",
  "default",
  "examples",
  "example",
  "deprecated",
  "writeOnly",
  "readOnly",
  "$schema",
])
const supported = new Set([
  "$ref",
  "type",
  "nullable",
  "properties",
  "required",
  "additionalProperties",
  "items",
  "minItems",
  "maxItems",
  "minLength",
  "maxLength",
  "pattern",
  "format",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "enum",
  "x-field-rules",
  "x-field-messages",
])
const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)

/** Compile the supported request vocabulary; unknown constraints block submission.
 * Defaults never mutate user input, and numeric conversion occurs only at parse.
 */
export function compileRequestContract(
  contract: OperationRequestContract,
  message: ContractMessage
) {
  const unsupported: ContractIssue[] = []
  const messageIssues: ContractIssue[] = []
  const requiredFields = new Set<string>()
  const fields: Record<string, ZodType> = Object.create(null)
  const inputLimits: Record<
    string,
    { min?: number; max?: number; step?: number }
  > = Object.create(null)
  const fail = (path: string, rule: string): ZodType => {
    unsupported.push({ path, rule })
    return z.never({ error: message("unsupported") })
  }
  const compile = (raw: unknown, path: string, refs: Set<string>): ZodType => {
    if (!record(raw)) return fail(path, "schema")
    for (const key of Object.keys(raw)) {
      if (!supported.has(key) && !annotations.has(key)) fail(path, key)
    }
    if (typeof raw.$ref === "string") {
      const prefix = "#/components/schemas/"
      if (!raw.$ref.startsWith(prefix) || refs.has(raw.$ref))
        return fail(path, "$ref")
      const next = new Set(refs).add(raw.$ref)
      const name = raw.$ref
        .slice(prefix.length)
        .replace(/~1/g, "/")
        .replace(/~0/g, "~")
      // Ref siblings carry constraints in modern JSON Schema; never ignore them.
      if (
        Object.keys(raw).some((key) => key !== "$ref" && !annotations.has(key))
      )
        return fail(path, "$ref siblings")
      return compile(contract.schemas[name], path, next)
    }
    const types = Array.isArray(raw.type) ? raw.type : [raw.type]
    const nullable = raw.nullable === true || types.includes("null")
    const concrete = types.filter((type) => type !== "null")
    if (concrete.length !== 1) return fail(path, "type")
    const type = concrete[0]
    if (raw["x-field-rules"] !== undefined && type !== "string")
      return fail(path, "x-field-rules:type")
    let result: ZodType
    const msg = (
      key: string,
      limit?: number,
      extra: Record<string, unknown> = {}
    ) => {
      if (
        contract.messages === undefined &&
        raw["x-field-messages"] === undefined
      )
        return message(key, limit === undefined ? undefined : { limit })
      const id =
        key === "minBytes" || key === "maxBytes"
          ? `custom:${key}`
          : key === "date"
            ? `format:${String(raw.format)}`
            : key
      const overrides = raw["x-field-messages"]
      const template =
        record(overrides) && Object.hasOwn(overrides, id)
          ? overrides[id]
          : contract.messages?.[id]
      const params: Record<string, unknown> = { ...extra }
      if (limit !== undefined) {
        const parameter = ["minLength", "minItems", "minBytes"].includes(key)
          ? "min"
          : ["maxLength", "maxItems", "maxBytes"].includes(key)
            ? "max"
            : key === "exclusiveMinimum"
              ? "minimum"
              : key === "exclusiveMaximum"
                ? "maximum"
                : key
        params[parameter] = limit
      }
      if (key === "pattern") params.pattern = raw.pattern
      if (key === "enum") params.allowed = raw.enum
      const rendered = renderValidationMessage(template, params)
      if (rendered !== undefined) return rendered
      messageIssues.push({ path, rule: `message:${id}` })
      return message("invalid")
    }
    if (type === "string") {
      let text = z.string({ error: msg("type") })
      if (raw.format !== undefined) {
        if (raw.format === "date")
          text = text.check(z.iso.date({ error: msg("date") }))
        else if (raw.format === "date-time")
          text = text.check(
            z.iso.datetime({ offset: true, error: msg("date") })
          )
        else if (raw.format !== "")
          return fail(path, `format:${String(raw.format)}`)
      }
      result = text
      for (const [key, lower] of [
        ["minLength", true],
        ["maxLength", false],
      ] as const) {
        if (typeof raw[key] === "number") {
          const limit = raw[key]
          result = result.refine(
            (value) =>
              lower
                ? [...String(value)].length >= limit
                : [...String(value)].length <= limit,
            msg(key, limit)
          )
        }
      }
      if (raw.pattern !== undefined) {
        // This deliberately small Go/JS intersection includes Huma ID and
        // project-code patterns. Other dialect constructs need explicit support.
        if (
          typeof raw.pattern !== "string" ||
          !/^\^(?:\[[A-Za-z0-9^-]+\](?:[+*?]|\{\d+(?:,\d*)?\})?)+\$$/.test(
            raw.pattern
          )
        )
          return fail(path, "pattern")
        const regex = new RegExp(
          raw.pattern.slice(0, -1) + "$(?![\\s\\S])",
          "u"
        )
        result = result.refine(
          (value) => regex.test(String(value)),
          msg("pattern")
        )
      }
      if (raw["x-field-rules"] !== undefined) {
        const rules = raw["x-field-rules"]
        if (!Array.isArray(rules)) return fail(path, "x-field-rules")
        for (const rule of rules) {
          if (
            !record(rule) ||
            rule.rule !== "utf8-bytes" ||
            Object.keys(rule).some((key) => !["rule", "args"].includes(key)) ||
            !record(rule.args)
          )
            return fail(path, "x-field-rules")
          const args = rule.args
          if (
            Object.keys(args).some(
              (key) => !["min", "max", "normalize"].includes(key)
            ) ||
            (args.normalize !== undefined && args.normalize !== "trim-space") ||
            [args.min, args.max].some(
              (bound) =>
                bound !== undefined &&
                (typeof bound !== "number" ||
                  !Number.isSafeInteger(bound) ||
                  bound < 0)
            ) ||
            (typeof args.min === "number" &&
              typeof args.max === "number" &&
              args.min > args.max)
          )
            return fail(path, "utf8-bytes:args")
          for (const [key, lower] of [
            ["min", true],
            ["max", false],
          ] as const) {
            const limit = args[key]
            if (typeof limit !== "number") continue
            result = result.refine(
              (value) => {
                // Go strings.TrimSpace uses Unicode White_Space (including NEL,
                // excluding BOM). JS trim() has different semantics. Validate a
                // copy: raw schema limits and the submitted value stay unchanged.
                const normalized =
                  args.normalize === "trim-space"
                    ? String(value).replace(
                        /^\p{White_Space}+|\p{White_Space}+$/gu,
                        ""
                      )
                    : String(value)
                const bytes = new TextEncoder().encode(normalized).byteLength
                return lower ? bytes >= limit : bytes <= limit
              },
              msg(lower ? "minBytes" : "maxBytes", limit)
            )
          }
        }
      }
    } else if (type === "number" || type === "integer") {
      if (
        raw.format !== undefined &&
        !["int32", "int64", "float", "double"].includes(String(raw.format))
      )
        return fail(path, `format:${String(raw.format)}`)
      let number = z.number({ error: msg("number") })
      if (type === "integer") number = number.int({ error: msg("integer") })
      for (const key of [
        "minimum",
        "maximum",
        "exclusiveMinimum",
        "exclusiveMaximum",
      ] as const) {
        const limit = raw[key]
        if (typeof limit !== "number") continue
        if (
          !Number.isFinite(limit) ||
          (Number.isInteger(limit) && !Number.isSafeInteger(limit))
        )
          return fail(path, `${key}:precision`)
        if (key === "minimum") number = number.min(limit, msg(key, limit))
        if (key === "maximum") number = number.max(limit, msg(key, limit))
        if (key === "exclusiveMinimum")
          number = number.gt(limit, msg(key, limit))
        if (key === "exclusiveMaximum")
          number = number.lt(limit, msg(key, limit))
      }
      if (!path.includes(".") && !path.includes("[]"))
        inputLimits[path] = {
          ...(typeof raw.minimum === "number" ? { min: raw.minimum } : {}),
          ...(typeof raw.maximum === "number" ? { max: raw.maximum } : {}),
          ...(type === "integer" ? { step: 1 } : {}),
        }
      result = z.preprocess(
        (value) =>
          typeof value === "string" &&
          (type === "integer"
            ? /^-?(?:0|[1-9]\d*)$/
            : /^-?(?:0|[1-9]\d*)(?:\.\d+)?$/
          ).test(value)
            ? Number(value)
            : value,
        number
      )
    } else if (type === "boolean") result = z.boolean({ error: msg("type") })
    else if (type === "object") {
      if (!record(raw.properties)) return fail(path, "properties")
      const required = Array.isArray(raw.required) ? raw.required : []
      const shape: Record<string, ZodType> = Object.create(null)
      for (const [key, property] of Object.entries(raw.properties)) {
        const child = compile(property, path ? `${path}.${key}` : key, refs)
        shape[key] = required.includes(key)
          ? z
              .custom<unknown>((value) => value !== undefined, {
                error: msg("required", undefined, { property: key }),
              })
              .pipe(child)
          : child.optional()
        if (!path) {
          fields[key] = shape[key]
          if (required.includes(key)) requiredFields.add(key)
        }
      }
      if (raw.additionalProperties === false)
        result = z.strictObject(shape, { error: msg("type") })
      else if (
        raw.additionalProperties === undefined ||
        raw.additionalProperties === true
      )
        result = z.looseObject(shape)
      else return fail(path, "additionalProperties")
    } else if (type === "array") {
      let array = z.array(compile(raw.items, `${path}[]`, refs), {
        error: msg("type"),
      })
      if (typeof raw.minItems === "number")
        array = array.min(raw.minItems, msg("minItems", raw.minItems))
      if (typeof raw.maxItems === "number")
        array = array.max(raw.maxItems, msg("maxItems", raw.maxItems))
      result = array
    } else return fail(path, "type")
    if (nullable) result = result.nullable()
    if (Array.isArray(raw.enum)) {
      const options = raw.enum
      result = result.refine(
        (value) => options.some((option) => Object.is(option, value)),
        msg("enum")
      )
    }
    return result
  }
  let schema: ZodType
  try {
    schema = compile(
      contract.request_body?.content?.["application/json"]?.schema,
      "",
      new Set()
    )
  } catch {
    schema = fail("", "schema")
  }
  if (unsupported.length) schema = z.never({ error: message("unsupported") })
  return {
    schema,
    fields,
    requiredFields,
    inputLimits,
    unsupported,
    messageIssues,
  }
}
