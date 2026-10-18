import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslations } from "use-intl"

import { Download, Upload } from "lucide-react"

import { cn } from "@workspace/ui/lib/utils"
import { downloadTableCsv } from "@workspace/ui/lib/table-export"

import { evaluate } from "../auto-form"
import { AutoForm } from "../auto-form/auto-form"
import { Button } from "../button"
import { Checkbox } from "../checkbox"
import { RefreshButton } from "../refresh-button"
import { PaginationPages } from "../pagination"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../table"
import {
  isStickyColumn,
  stickyCellStyle,
  stickyColWidth,
} from "../table-sticky"
import { Textarea } from "../textarea"
import {
  formatCellValue,
  getRowKey,
  type ColumnConfig,
  type TableAction,
  type TableOptions,
} from "./types"
import { useAutoTable } from "./use-auto-table"

function SortIndicator<T>({
  column,
  query,
}: {
  column: ColumnConfig<T>
  query: { sort?: { field: string; direction: "asc" | "desc" } }
}) {
  if (!column.sortable) return null
  const active = query.sort?.field === (column.field as string)
  return (
    <span className="ml-1 inline-flex flex-col text-[10px] leading-none text-muted-foreground">
      <span
        className={
          active && query.sort?.direction === "asc" ? "text-foreground" : ""
        }
      >
        ▲
      </span>
      <span
        className={
          active && query.sort?.direction === "desc" ? "text-foreground" : ""
        }
      >
        ▼
      </span>
    </span>
  )
}

function ActionButton<T>({ action, row }: { action: TableAction<T>; row: T }) {
  const visible = action.show?.(row) !== false
  const disabled = action.disabled?.(row) === true
  const Icon = action.icon
  const label = evaluate(action.name)

  if (!visible) return null

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      disabled={disabled}
      title={typeof label === "string" ? label : undefined}
      onClick={(e) => {
        e.stopPropagation()
        action.onclick?.(row)
      }}
      aria-label={typeof label === "string" ? label : undefined}
    >
      {Icon ? <Icon className="size-4" /> : label}
    </Button>
  )
}

export function AutoTable<T>(options: TableOptions<T>) {
  const t = useTranslations("table")
  const [search, setSearch] = useState("")
  const [toolBusy, setToolBusy] = useState(false)
  const [toolError, setToolError] = useState("")
  const tableRef = useRef<HTMLTableElement>(null)
  const { data: sourceData, searchable, columns } = options
  const data = useMemo(() => {
    if (!sourceData || !searchable || !search.trim()) return sourceData
    const needle = search.trim().toLocaleLowerCase()
    return sourceData.filter((row) => {
      const text =
        typeof searchable === "function"
          ? searchable(row)
          : columns
              .filter((column) => column.type !== "actions")
              .map((column) => {
                const value = (row as Record<string, unknown>)[
                  column.field as string
                ]
                return typeof value === "string" || typeof value === "number"
                  ? String(value)
                  : ""
              })
              .join(" ")
      return text.toLocaleLowerCase().includes(needle)
    })
  }, [sourceData, searchable, columns, search])
  const table = useAutoTable({ ...options, data })
  const {
    rows,
    total,
    loading: fetching,
    error,
    query,
    selected,
    setPage,
    setPageSize,
    setSort,
    toggleSelect,
    selectAll,
  } = table
  const loading = fetching || !!options.loading

  const displayRows = rows
  const totalPages = Math.max(1, Math.ceil(total / query.pageSize))
  useEffect(() => {
    const viewport = tableRef.current?.parentElement
    if (viewport) viewport.scrollTop = 0
  }, [query.page, query.pageSize, search, options.rowOffset])

  // Column order is [sequence?, selection?, ...configured columns, actions?].
  const numbered = options.rowNumbers !== false
  const numberOffset = numbered ? 1 : 0
  const selOffset = numberOffset + (options.selectable ? 1 : 0)
  const hasActions = !!options.actions?.length
  const totalCols = selOffset + options.columns.length + (hasActions ? 1 : 0)
  let rightColumns = hasActions ? 1 : 0
  for (let index = options.columns.length - 1; index >= 0; index--) {
    if (options.columns[index]?.type !== "actions") break
    rightColumns++
  }
  const sticky = {
    header: true,
    left: numberOffset,
    right: rightColumns,
    maxHeight: options.fill ? undefined : "60svh",
    ...options.sticky,
  }
  const stickyWidths = [
    ...(numbered ? [56] : []),
    ...(options.selectable ? [40] : []),
    ...options.columns.map((c) => stickyColWidth(c.width)),
    ...(hasActions ? [40] : []),
  ]
  const headSticky = (i: number) => ({
    width: stickyWidths[i],
    minWidth: stickyWidths[i],
    ...stickyCellStyle(i, totalCols, stickyWidths, sticky, true),
  })
  const cellSticky = (i: number) => ({
    minWidth: stickyWidths[i],
    ...stickyCellStyle(i, totalCols, stickyWidths, sticky, false),
  })
  const stickyBg = (i: number, header = false) =>
    (header && sticky?.header) || isStickyColumn(i, totalCols, sticky)
      ? "bg-background"
      : ""

  const allPageSelected =
    displayRows.length > 0 &&
    displayRows.every((row) =>
      selected.some(
        (r) => getRowKey(r, options.rowKey) === getRowKey(row, options.rowKey)
      )
    )

  const anyPageSelected = displayRows.some((row) =>
    selected.some(
      (r) => getRowKey(r, options.rowKey) === getRowKey(row, options.rowKey)
    )
  )

  const handleHeaderCheckbox = () => {
    if (!options.selectable) return
    selectAll(displayRows)
  }

  const handleRowClick = (row: T) => {
    options.onRowClick?.(row)
  }

  const handleRowKeyDown = (
    event: React.KeyboardEvent<HTMLTableRowElement>,
    row: T
  ) => {
    if (!options.onRowClick) return
    if (event.key !== "Enter" && event.key !== " ") return
    event.preventDefault()
    handleRowClick(row)
  }

  const runTool = async (action: () => void | Promise<void>) => {
    if (toolBusy) return
    setToolBusy(true)
    setToolError("")
    try {
      await action()
    } catch {
      setToolError(t("actionError"))
    } finally {
      setToolBusy(false)
    }
  }
  const exportPage = () => {
    const element = tableRef.current
    if (!element) return
    // Export rendered, authorized cell text only, never hidden record properties.
    const lines = Array.from(element.querySelectorAll("tr")).map((row) =>
      Array.from(row.querySelectorAll<HTMLTableCellElement>("th,td"))
        .filter((cell) => cell.dataset.tableExport !== "false")
        .map((cell) => cell.innerText.trim())
    )
    downloadTableCsv(lines)
  }
  const toolsDisabled = loading || !!error || toolBusy

  return (
    <div
      data-slot="auto-table"
      data-layout={options.fill ? "fill" : "content"}
      className={cn(
        "flex min-w-0 flex-col rounded-md border bg-card",
        options.fill && "min-h-0 flex-1",
        options.className
      )}
    >
      {options.title ? (
        <div className="flex shrink-0 items-center justify-between px-3 pt-3">
          {options.title}
        </div>
      ) : null}

      {options.toolbar !== false && (
        <div
          data-slot="table-toolbar"
          className="flex shrink-0 flex-wrap items-end gap-3 border-b p-3"
        >
          {options.searchable && options.data !== undefined && (
            <AutoForm
              hideLabel
              className="w-full sm:w-60 md:grid-cols-1"
              fields={{
                search: {
                  name: t("search"),
                  type: "text",
                  config: { type: "search", placeholder: t("search") },
                },
              }}
              onChange={(values) => {
                const next = String(values.search ?? "")
                if (next !== search) {
                  setSearch(next)
                  setPage(1)
                }
              }}
            />
          )}
          {options.toolbar && (
            <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
              {options.toolbar}
            </div>
          )}
          <div className="ms-auto flex shrink-0 flex-wrap items-center gap-2">
            {(options.onRefresh || options.fetch) && (
              <RefreshButton
                disabled={toolsDisabled}
                loading={loading}
                onRefresh={() =>
                  runTool(
                    options.onRefresh ??
                      (() => {
                        table.refresh()
                      })
                  )
                }
              />
            )}
            {options.onImport && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={toolsDisabled}
                onClick={() => void runTool(options.onImport!)}
              >
                <Upload className="size-4" />
                {t("import")}
              </Button>
            )}
            {options.exportable !== false && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={toolsDisabled || displayRows.length === 0}
                onClick={() => void runTool(options.onExport ?? exportPage)}
              >
                <Download className="size-4" />
                {t(options.onExport ? "export" : "exportPage")}
              </Button>
            )}
          </div>
        </div>
      )}
      {toolError && (
        <p role="alert" className="shrink-0 px-3 py-2 text-sm text-destructive">
          {toolError}
        </p>
      )}

      <Table
        ref={tableRef}
        data-slot="auto-table-data"
        wrapperClassName={cn(
          "min-h-0 min-w-0",
          options.fill && "flex flex-1 flex-col"
        )}
        containerClassName={cn(
          "min-h-0 overscroll-contain",
          options.fill && "max-h-[60svh] flex-1 lg:max-h-none"
        )}
        containerStyle={{ maxHeight: sticky?.maxHeight }}
      >
        <TableHeader>
          <TableRow className="bg-muted/50">
            {numbered && (
              <TableHead
                data-table-export="false"
                data-column="sequence"
                className={cn("text-center", stickyBg(0, true))}
                style={headSticky(0)}
              >
                {t("rowNumber")}
              </TableHead>
            )}
            {options.selectable ? (
              <TableHead
                data-table-export="false"
                className={cn("w-10", stickyBg(numberOffset, true))}
                style={headSticky(numberOffset)}
              >
                <Checkbox
                  disabled={loading || !!error}
                  checked={allPageSelected}
                  indeterminate={!allPageSelected && anyPageSelected}
                  onCheckedChange={handleHeaderCheckbox}
                  aria-label={t("selectAll")}
                />
              </TableHead>
            ) : null}
            {options.columns.map((column, ci) => {
              const field = column.field as string
              const sorted =
                query.sort?.field === field
                  ? query.sort.direction === "asc"
                    ? "ascending"
                    : "descending"
                  : undefined

              return (
                <TableHead
                  key={String(column.field)}
                  data-column={
                    column.type === "actions" ? "actions" : undefined
                  }
                  data-table-export={
                    column.type === "actions" ? "false" : undefined
                  }
                  className={cn(
                    column.sortable ? "select-none" : undefined,
                    stickyBg(selOffset + ci, true)
                  )}
                  style={headSticky(selOffset + ci)}
                  aria-sort={sorted}
                >
                  {column.sortable ? (
                    <button
                      type="button"
                      className="flex items-center text-left whitespace-nowrap hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                      onClick={() => setSort(field)}
                    >
                      <span>{evaluate(column.name)}</span>
                      <SortIndicator column={column} query={query} />
                    </button>
                  ) : (
                    <span className="whitespace-nowrap">
                      {evaluate(column.name)}
                    </span>
                  )}
                </TableHead>
              )
            })}
            {hasActions ? (
              <TableHead
                data-column="actions"
                data-table-export="false"
                className={cn(
                  "w-10 text-right",
                  stickyBg(selOffset + options.columns.length, true)
                )}
                style={headSticky(selOffset + options.columns.length)}
              >
                {t("actions")}
              </TableHead>
            ) : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableRow>
              <TableCell colSpan={totalCols}>
                <div className="space-y-2 py-4">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <div
                      key={i}
                      className="h-8 w-full animate-pulse rounded bg-muted"
                    />
                  ))}
                </div>
              </TableCell>
            </TableRow>
          ) : error ? (
            <TableRow>
              <TableCell colSpan={totalCols} className="h-24 text-center">
                <div role="alert" className="space-y-2">
                  <p>{t("loadError")}</p>
                  <Button
                    type="button"
                    variant="outline"
                    onClick={table.refresh}
                  >
                    {t("retry")}
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ) : displayRows.length === 0 ? (
            options.emptyContent === null ? null : (
              <TableRow>
                <TableCell
                  colSpan={totalCols}
                  className="h-24 text-center text-muted-foreground"
                >
                  {options.emptyContent !== undefined
                    ? options.emptyContent
                    : t("empty")}
                </TableCell>
              </TableRow>
            )
          ) : (
            displayRows.map((row, rowIndex) => {
              const rowKey = getRowKey(row, options.rowKey)
              const rowProps = options.rowProps?.(row)
              const isSelected = selected.some(
                (r) => getRowKey(r, options.rowKey) === rowKey
              )
              return (
                <TableRow
                  {...rowProps}
                  key={rowKey}
                  data-selected={isSelected}
                  onClick={(event) => {
                    rowProps?.onClick?.(event)
                    if (!event.defaultPrevented && options.onRowClick)
                      handleRowClick(row)
                  }}
                  onKeyDown={(event) => {
                    rowProps?.onKeyDown?.(event)
                    if (!event.defaultPrevented) handleRowKeyDown(event, row)
                  }}
                  tabIndex={
                    rowProps?.tabIndex ?? (options.onRowClick ? 0 : undefined)
                  }
                  role={
                    rowProps?.role ??
                    (options.onRowClick ? "button" : undefined)
                  }
                  className={cn(
                    rowProps?.className,
                    options.onRowClick
                      ? "cursor-pointer focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:outline-none"
                      : ""
                  )}
                >
                  {numbered && (
                    <TableCell
                      data-table-export="false"
                      data-column="sequence"
                      className={cn(
                        "text-center text-muted-foreground tabular-nums",
                        stickyBg(0)
                      )}
                      style={cellSticky(0)}
                    >
                      {(options.rowOffset ??
                        (options.pagination === false
                          ? 0
                          : (query.page - 1) * query.pageSize)) +
                        rowIndex +
                        1}
                    </TableCell>
                  )}
                  {options.selectable ? (
                    <TableCell
                      data-table-export="false"
                      className={cn("w-10", stickyBg(numberOffset))}
                      style={cellSticky(numberOffset)}
                    >
                      <Checkbox
                        checked={isSelected}
                        onCheckedChange={() => toggleSelect(row)}
                        onClick={(e) => e.stopPropagation()}
                        aria-label={t("selectRow")}
                      />
                    </TableCell>
                  ) : null}
                  {options.columns.map((column, ci) => (
                    <TableCell
                      key={`${rowKey}-${String(column.field)}`}
                      data-column={
                        column.type === "actions" ? "actions" : undefined
                      }
                      data-table-export={
                        column.type === "actions" ? "false" : undefined
                      }
                      align={column.align}
                      className={stickyBg(selOffset + ci)}
                      style={cellSticky(selOffset + ci)}
                      onClick={(e) => {
                        if (column.onclick?.(row, e) === false) {
                          e.stopPropagation()
                        }
                      }}
                    >
                      <CellContent row={row} column={column} />
                    </TableCell>
                  ))}
                  {hasActions ? (
                    <TableCell
                      data-column="actions"
                      data-table-export="false"
                      className={cn(
                        "text-right",
                        stickyBg(selOffset + options.columns.length)
                      )}
                      style={cellSticky(selOffset + options.columns.length)}
                    >
                      <div className="flex items-center justify-end gap-1">
                        {options.actions!.map((action, idx) => (
                          <ActionButton key={idx} action={action} row={row} />
                        ))}
                      </div>
                    </TableCell>
                  ) : null}
                </TableRow>
              )
            })
          )}
        </TableBody>
      </Table>

      {(options.footer != null || options.pagination !== false) && (
        <div
          data-slot="table-footer"
          className={cn(
            "flex shrink-0 flex-col items-center justify-between gap-3 border-t bg-background p-3 sm:flex-row",
            sticky?.pagination &&
              "sticky bottom-0 z-10 border-t bg-background py-2"
          )}
        >
          {options.footer ?? (
            <>
              <div className="text-sm text-muted-foreground">
                {!loading && !error ? (
                  <>
                    {t("total", { total })} /{" "}
                    {t("page", { page: query.page, totalPages })}
                  </>
                ) : null}
              </div>
              <div className="flex flex-wrap items-center justify-center gap-2">
                <select
                  value={query.pageSize}
                  onChange={(e) => setPageSize(Number(e.target.value))}
                  className="h-8 rounded-md border border-input bg-transparent px-2 text-sm"
                  aria-label={t("pageSize")}
                >
                  {(options.pageSizeOptions ?? [10, 20, 50, 100]).map(
                    (size) => (
                      <option key={size} value={size}>
                        {size} {t("pageSize")}
                      </option>
                    )
                  )}
                </select>
                <PaginationPages
                  page={query.page}
                  totalPages={totalPages}
                  onPageChange={setPage}
                  disabled={loading || !!error}
                />
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}

function CellContent<T>({ row, column }: { row: T; column: ColumnConfig<T> }) {
  if (column.render) {
    return (
      <>
        {column.render(
          (row as Record<string, unknown>)[column.field as string],
          row,
          { column }
        )}
      </>
    )
  }

  if (column.type === "image") {
    const src = (row as Record<string, unknown>)[
      column.field as string
    ] as string
    if (!src) return null
    return <img src={src} alt="" className="h-11 w-11 rounded object-cover" />
  }

  if (column.type === "icon") {
    const value = (row as Record<string, unknown>)[column.field as string]
    const color =
      typeof column.color === "function"
        ? column.color(value, row)
        : column.color
    const Icon = column.format?.(value, row) as unknown as React.ComponentType<{
      className?: string
      style?: React.CSSProperties
    }>
    if (!Icon) return null
    return <Icon className="size-5" style={{ color }} />
  }

  if (column.type === "area") {
    const value = String(
      (row as Record<string, unknown>)[column.field as string] ?? ""
    )
    return (
      <Textarea
        readOnly
        value={value}
        rows={2}
        className="min-h-[60px] resize-none"
      />
    )
  }

  const color =
    typeof column.color === "function"
      ? column.color(
          (row as Record<string, unknown>)[column.field as string],
          row
        )
      : column.color

  return <span style={{ color }}>{formatCellValue(row, column)}</span>
}

export { useAutoTable }
