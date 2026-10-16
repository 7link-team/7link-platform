import { Fragment, type ReactNode } from "react"
import { ChevronDown } from "lucide-react"

import {
  evaluate,
  normalizeOptions,
  type FieldComponentConfig,
  type FieldType,
} from "./types"

// Select-style types keep a (decorative) chevron in read-only mode so the box still reads
// as "this was a dropdown"; plain inputs (text/number/date…) render without one.
const CHEVRON_TYPES = new Set<FieldType>([
  "select",
  "multi-select",
  "bool-select",
])

// Field types rendered as a read-only value block in detail mode (per 3.DESIGN.md
// "detail = 只读块、非输入框"). Complex types (remote-select / image / rich-text / code /
// custom / relation-list / location / tabs) keep their own renderer instead.
const BLOCK_TYPES = new Set<FieldType>([
  "text",
  "textarea",
  "number",
  "password",
  "email",
  "select",
  "multi-select",
  "checkbox",
  "checkbox-group",
  "switch",
  "radio",
  "date",
  "dateRange",
  "datetime",
  "datetimeRange",
  "time",
  "timeRange",
  "price",
  "bool-select",
])

export function isReadonlyBlockType(type: FieldType): boolean {
  return BLOCK_TYPES.has(type)
}

function optionLabel(config: FieldComponentConfig, value: unknown): ReactNode {
  return (
    normalizeOptions(config.options).find(
      (option) => option.value === String(value)
    )?.label ?? String(value)
  )
}

/** Renders a field's value as a plain read-only block (normal contrast, selectable). */
export function ReadonlyValue({
  type,
  config,
  value,
  values,
}: {
  type: FieldType
  config: FieldComponentConfig
  value: unknown
  values?: Record<string, unknown>
}) {
  let text: ReactNode
  switch (type) {
    case "password":
      text = value == null || value === "" ? "" : "••••••••"
      break
    case "price": {
      const n = Number(value)
      const decimals = config.decimals ?? 2
      text =
        Number.isFinite(n) && value != null && value !== ""
          ? (n / 10 ** decimals).toFixed(decimals)
          : ""
      break
    }
    case "switch":
    case "checkbox":
      text = value ? "✓" : "—"
      break
    case "bool-select":
      text =
        value === true
          ? evaluate(config.trueLabel ?? "✓", values)
          : value === false
            ? evaluate(config.falseLabel ?? "—", values)
            : ""
      break
    case "select":
    case "radio":
      text = value == null || value === "" ? "" : optionLabel(config, value)
      break
    case "multi-select":
    case "checkbox-group":
      text =
        !Array.isArray(value) || value.length === 0
          ? ""
          : value.map((v, index) => (
              <Fragment key={index}>
                {index > 0 ? "、" : ""}
                {optionLabel(config, v)}
              </Fragment>
            ))
      break
    case "dateRange":
    case "datetimeRange":
    case "timeRange": {
      const range =
        value && typeof value === "object" && !Array.isArray(value)
          ? (value as { start?: unknown; end?: unknown })
          : {}
      const start = typeof range.start === "string" ? range.start : ""
      const end = typeof range.end === "string" ? range.end : ""
      text = start || end ? `${start || "—"} – ${end || "—"}` : ""
      break
    }
    default:
      text = value == null ? "" : String(value)
  }
  return <ReadonlyBox text={text} chevron={CHEVRON_TYPES.has(type)} />
}

/** Boxed read-only display: keeps the input's box shape (so detail rows align with edit
 * rows) but uses a dashed border + muted fill to signal read-only, text at normal contrast.
 * `chevron` adds a decorative ▼ for select-style fields. */
export function ReadonlyBox({
  text,
  chevron,
}: {
  text: ReactNode
  chevron?: boolean
}) {
  return (
    <div className="flex min-h-9 w-full items-center rounded-md border border-dashed bg-muted/30 px-3 py-2 text-sm break-words whitespace-pre-wrap">
      <span className="min-w-0 flex-1">
        {text !== "" && text != null ? (
          text
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </span>
      {chevron ? (
        <ChevronDown className="ms-2 size-4 shrink-0 text-muted-foreground" />
      ) : null}
    </div>
  )
}
