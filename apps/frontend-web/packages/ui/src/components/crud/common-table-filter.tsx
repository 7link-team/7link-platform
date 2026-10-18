import { useState, type ReactNode } from "react"
import { useTranslations } from "use-intl"

import type { FilterOp, FilterValue } from "@workspace/ui/lib/api-client"

import { Button } from "../button"
import { RefreshButton } from "../refresh-button"
import { Input } from "../input"
import { DatePicker } from "../pickers/date-picker"
import { DateTimePicker } from "../pickers/date-time-picker"
import { SimpleSelect } from "../select"
import type { FilterFieldOption, FilterState } from "./types"

/**
 * Operator labels as language-neutral math symbols (no per-locale strings to maintain):
 * = ≠ ≈(contains) ≉ > ≥ < ≤ ∈(in) ⟷(between). The full word is kept in `title` for hover.
 */
const OP_LABELS: Record<FilterOp, string> = {
  eq: "=",
  ne: "≠",
  like: "≈",
  notlike: "≉",
  gt: ">",
  gte: "≥",
  lt: "<",
  lte: "≤",
  in: "∈",
  between: "⟷",
}

// Sentinel for the "all / clear" entry prepended to enum filters (empty value = no filter).
const ALL_OPTION = "__all__"

function normalizeOptions(
  options?: Array<{ label: ReactNode; value: string }> | Record<string, string>
): Array<{ label: ReactNode; value: string }> {
  if (!options) return []
  if (Array.isArray(options)) return options
  return Object.entries(options).map(([value, label]) => ({ value, label }))
}

// Filter values are stored as plain strings; DatePicker works with Date objects.
function toDateObj(s?: string): Date | null {
  if (!s) return null
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? null : d
}
function fromDateObj(d: Date | null): string {
  if (!d) return ""
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, "0")
  const day = String(d.getDate()).padStart(2, "0")
  return `${y}-${m}-${day}`
}

export interface CommonTableFilterProps {
  filterOptions: FilterFieldOption[]
  value?: FilterState
  /** Called with the applied filter when the user clicks Search (or Reset). */
  onChange: (filter: FilterState) => void
  /** Reload current page data keeping all params (offset/limit/filter/sort). */
  onRefresh?: () => void
}

/**
 * CommonTableFilter renders the list filter bar (= CommonTableFilter.vue). It keeps a draft
 * locally and only commits to `onChange` on Search/Reset, so typing doesn't refetch.
 */
export function CommonTableFilter({
  filterOptions,
  value,
  onChange,
  onRefresh,
}: CommonTableFilterProps) {
  const t = useTranslations("table.filter")
  const [draft, setDraft] = useState<FilterState>(value ?? {})

  const setField = (field: string, patch: Partial<FilterValue>) => {
    setDraft((prev) => {
      const current = prev[field] ?? { value: "" }
      return { ...prev, [field]: { ...current, ...patch } }
    })
  }

  const search = () => {
    // drop empties before committing
    const cleaned: FilterState = {}
    for (const [field, fv] of Object.entries(draft)) {
      if (fv && fv.value !== "" && fv.value != null) cleaned[field] = fv
    }
    onChange(cleaned)
  }

  const reset = () => {
    setDraft({})
    onChange({})
  }

  return (
    <div className="mb-4 flex flex-wrap items-end gap-3 rounded-md border bg-muted/30 p-3">
      {filterOptions.map((opt) => {
        const fv = draft[opt.field] ?? { value: "" }
        const conditions = opt.conditions ?? ["like"]
        const op = fv.op ?? conditions[0]
        const betweenParts = String(fv.value ?? "").split(",")
        const pickerType =
          opt.type === "number"
            ? "number"
            : opt.type === "date"
              ? "date"
              : opt.type === "datetime"
                ? "datetime-local"
                : "text"
        const setBetween = (idx: 0 | 1, v: string) => {
          const a = idx === 0 ? v : (betweenParts[0] ?? "")
          const b = idx === 1 ? v : (betweenParts[1] ?? "")
          setField(opt.field, { op: op as FilterOp, value: `${a},${b}` })
        }
        // Real DatePicker (calendar icon + popover) for date filters; native input otherwise.
        const renderValueInput = (val: string, set: (v: string) => void) =>
          opt.type === "date" ? (
            <DatePicker
              value={toDateObj(val)}
              onChange={(d) => set(fromDateObj(d))}
              placeholder={t("pickDate")}
              className="h-8 w-[160px]"
            />
          ) : opt.type === "datetime" ? (
            <DateTimePicker
              value={val}
              onChange={set}
              placeholder={t("pickDateTime")}
              className="h-8 w-[190px]"
            />
          ) : (
            <Input
              className="h-8 w-[160px]"
              type={op === "in" ? "text" : pickerType}
              placeholder={op === "in" ? t("inPlaceholder") : undefined}
              value={val}
              onChange={(e) => set(e.target.value)}
            />
          )
        return (
          <div key={opt.field} className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">{opt.name}</span>
            <div className="flex items-center gap-1.5">
              {conditions.length > 1 ? (
                <SimpleSelect
                  options={conditions.map((c) => ({
                    value: c,
                    label: OP_LABELS[c],
                  }))}
                  value={op}
                  onValueChange={(v) =>
                    setField(opt.field, { op: v as FilterOp })
                  }
                  className="h-8 w-[56px] justify-center px-2 [&_svg]:hidden"
                />
              ) : null}
              {opt.type === "select" ? (
                <SimpleSelect
                  options={[
                    { value: ALL_OPTION, label: t("all") },
                    ...normalizeOptions(opt.options),
                  ]}
                  value={(fv.value as string) || ALL_OPTION}
                  onValueChange={(v) =>
                    setField(opt.field, {
                      op: op as FilterOp,
                      value: v === ALL_OPTION ? "" : v,
                    })
                  }
                  placeholder={t("all")}
                  className="h-8 w-[160px]"
                />
              ) : op === "between" ? (
                // Range: two pickers; value stays comma-encoded "from,to" for the backend.
                <div className="flex items-center gap-1">
                  {renderValueInput(betweenParts[0] ?? "", (v) =>
                    setBetween(0, v)
                  )}
                  <span className="text-muted-foreground">~</span>
                  {renderValueInput(betweenParts[1] ?? "", (v) =>
                    setBetween(1, v)
                  )}
                </div>
              ) : (
                renderValueInput(String(fv.value ?? ""), (v) =>
                  setField(opt.field, { op: op as FilterOp, value: v })
                )
              )}
            </div>
          </div>
        )
      })}
      <div className="ml-auto flex flex-col gap-1">
        {/* invisible label spacer so the buttons sit on the input row, not the labels */}
        <span className="text-xs">&nbsp;</span>
        <div className="flex gap-2">
          <Button type="button" size="sm" className="h-8" onClick={search}>
            {t("search")}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-8"
            onClick={reset}
          >
            {t("reset")}
          </Button>
          {onRefresh ? <RefreshButton onRefresh={onRefresh} /> : null}
        </div>
      </div>
    </div>
  )
}
