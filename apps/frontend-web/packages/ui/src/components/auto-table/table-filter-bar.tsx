import { useMemo, useState } from "react"
import { useTranslations } from "use-intl"

import { Button } from "../button"
import { Input } from "../input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../select"
import { evaluate, normalizeOptions, type SelectOption } from "../auto-form"
import { DateTimePicker } from "../pickers/date-time-picker"
import { cn } from "@workspace/ui/lib/utils"

export interface FilterOption {
  field: string
  name: React.ReactNode | (() => React.ReactNode)
  type:
    "string" | "number" | "select" | "boolean" | "date" | "datetime" | "time"
  conditions?: string[]
  options?: SelectOption[]
  defaultCondition?: string
  maxLength?: number
}

export interface TableFilterBarProps {
  className?: string
  filters: FilterOption[]
  value?: Record<string, string>
  defaultValue?: Record<string, string>
  onChange?: (filters: Record<string, string>) => void
  onSearch?: (filters: Record<string, string>) => void
  onReset?: () => void
}

const DEFAULT_CONDITIONS = ["like", "eq", "ne", "gt", "lt"]

type FilterState = { condition: string; value: string; instant?: string }

function getTextLabel(value: React.ReactNode): string | undefined {
  if (typeof value === "string" || typeof value === "number")
    return String(value)
  return undefined
}

function parseFilterValue(raw?: string): { condition: string; value: string } {
  if (!raw) return { condition: "", value: "" }
  const idx = raw.indexOf(":")
  if (idx > 0) {
    return {
      condition: raw.substring(0, idx),
      value: raw.substring(idx + 1),
    }
  }
  return { condition: "", value: raw }
}

function encodeFilterValue(condition: string, value: string): string {
  if (!value) return ""
  if (condition) return `${condition}:${value}`
  return value
}

function buildFilterState(
  filters: FilterOption[],
  value?: Record<string, string>
) {
  const initial: Record<string, FilterState> = {}
  for (const filter of filters) {
    const raw = value?.[filter.field]
    const parsed = parseFilterValue(raw)
    const conditions = filter.conditions ?? DEFAULT_CONDITIONS
    const encoded = conditions.includes(parsed.condition)
    let input = encoded ? parsed.value : (raw ?? "")
    let instant: string | undefined
    if (filter.type === "datetime" && /(?:Z|[+-]\d{2}:\d{2})$/.test(input)) {
      const date = new Date(input)
      if (!Number.isNaN(date.getTime())) {
        // Keep the original instant until the user changes this field. Local
        // clock time alone is ambiguous during the repeated hour at DST end.
        instant = input
        const local = new Date(
          date.getTime() - date.getTimezoneOffset() * 60_000
        )
        input = local.toISOString().slice(0, -1)
      }
    }
    initial[filter.field] = {
      condition:
        (encoded ? parsed.condition : "") ||
        filter.defaultCondition ||
        conditions[0] ||
        "",
      value: input,
      instant,
    }
  }
  return initial
}

export function TableFilterBar({
  className,
  filters,
  value,
  defaultValue,
  onChange,
  onSearch,
  onReset,
}: TableFilterBarProps) {
  const t = useTranslations("table.filter")
  const [internal, setInternal] = useState<Record<string, FilterState>>(() =>
    buildFilterState(filters, value ?? defaultValue)
  )
  const isControlled = value !== undefined
  const currentState = isControlled
    ? buildFilterState(filters, value)
    : internal

  const conditionsByField = useMemo(() => {
    const map: Record<string, string[]> = {}
    for (const filter of filters) {
      map[filter.field] = filter.conditions ?? DEFAULT_CONDITIONS
    }
    return map
  }, [filters])

  const toEncoded = (
    state: Record<string, FilterState>
  ): Record<string, string> => {
    const encoded: Record<string, string> = {}
    for (const [field, { condition, value, instant }] of Object.entries(
      state
    )) {
      let input = instant ?? value
      if (
        input &&
        filters.find((filter) => filter.field === field)?.type === "datetime"
      ) {
        const date = new Date(input)
        if (!Number.isNaN(date.getTime())) input = date.toISOString()
      }
      const encodedValue = encodeFilterValue(condition, input)
      if (encodedValue) encoded[field] = encodedValue
    }
    return encoded
  }

  const update = (
    field: string,
    patch: Partial<{ condition: string; value: string }>
  ) => {
    setInternal((prev) => {
      const source = isControlled ? currentState : prev
      const state = source[field] ?? { condition: "", value: "" }
      const next: Record<string, FilterState> = {
        ...source,
        [field]: {
          ...state,
          ...patch,
          ...(patch.value !== undefined ? { instant: undefined } : {}),
        },
      }
      onChange?.(toEncoded(next))
      return next
    })
  }

  const handleSearch = () => {
    onSearch?.(toEncoded(currentState))
  }

  const handleReset = () => {
    const reset: Record<string, { condition: string; value: string }> = {}
    for (const filter of filters) {
      reset[filter.field] = {
        condition:
          filter.defaultCondition ||
          (filter.conditions ?? DEFAULT_CONDITIONS)[0] ||
          "",
        value: "",
      }
    }
    setInternal(reset)
    onChange?.(toEncoded(reset))
    onReset?.()
  }

  return (
    <div className={cn("space-y-3 rounded-lg border p-4", className)}>
      <div className="flex flex-wrap gap-3">
        {filters.map((filter) => {
          const state = currentState[filter.field] ?? {
            condition: "",
            value: "",
          }
          const conditions = conditionsByField[filter.field] ?? []
          const label = evaluate(filter.name)
          const labelText = getTextLabel(label)
          const conditionLabel = labelText
            ? `${labelText} ${t("condition")}`
            : t("condition")
          const valueLabel = labelText
            ? `${labelText} ${t("value")}`
            : t("value")
          const conditionItems = conditions.map((condition) => ({
            value: condition,
            label: t.has(`op_${condition}`) ? t(`op_${condition}`) : condition,
          }))
          const filterOptions = normalizeOptions(filter.options)
          const booleanOptions = [
            { value: "true", label: t("yes") },
            { value: "false", label: t("no") },
          ]
          return (
            <div
              key={filter.field}
              className="flex min-w-[220px] flex-1 flex-wrap items-center gap-2"
            >
              <span className="min-w-0 shrink text-sm font-medium break-words">
                {label}
              </span>
              {conditions.length > 0 ? (
                <Select
                  items={conditionItems}
                  value={state.condition}
                  onValueChange={(nextCondition) =>
                    update(filter.field, { condition: nextCondition ?? "" })
                  }
                >
                  <SelectTrigger
                    className="w-[140px]"
                    aria-label={conditionLabel}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {conditionItems.map((condition) => (
                      <SelectItem key={condition.value} value={condition.value}>
                        {condition.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : null}
              {filter.type === "select" ? (
                <Select
                  items={filterOptions}
                  value={state.value}
                  onValueChange={(nextValue) =>
                    update(filter.field, { value: nextValue ?? "" })
                  }
                >
                  <SelectTrigger className="flex-1" aria-label={valueLabel}>
                    <SelectValue placeholder={t("selectPlaceholder")} />
                  </SelectTrigger>
                  <SelectContent>
                    {filterOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : filter.type === "boolean" ? (
                <Select
                  items={booleanOptions}
                  value={state.value}
                  onValueChange={(nextValue) =>
                    update(filter.field, { value: nextValue ?? "" })
                  }
                >
                  <SelectTrigger className="flex-1" aria-label={valueLabel}>
                    <SelectValue placeholder={t("selectPlaceholder")} />
                  </SelectTrigger>
                  <SelectContent>
                    {booleanOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : filter.type === "datetime" ? (
                <DateTimePicker
                  editableTime
                  showSeconds
                  value={state.value}
                  onChange={(value) => update(filter.field, { value })}
                  aria-label={valueLabel}
                  placeholder={t("selectPlaceholder")}
                  className="min-w-0 flex-1"
                />
              ) : (
                <Input
                  maxLength={filter.maxLength}
                  type={
                    filter.type === "date"
                      ? "date"
                      : filter.type === "number"
                        ? "number"
                        : filter.type === "time"
                          ? "time"
                          : "text"
                  }
                  value={state.value}
                  onChange={(event) =>
                    update(filter.field, { value: event.target.value })
                  }
                  aria-label={valueLabel}
                  className="flex-1"
                  placeholder={t("inputPlaceholder")}
                />
              )}
            </div>
          )
        })}
      </div>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={handleReset}>
          {t("reset")}
        </Button>
        <Button type="button" onClick={handleSearch}>
          {t("search")}
        </Button>
      </div>
    </div>
  )
}
