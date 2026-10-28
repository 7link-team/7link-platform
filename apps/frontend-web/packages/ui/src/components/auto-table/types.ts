import type { ComponentType, HTMLAttributes, ReactNode } from "react"

import type { Evaluable } from "../auto-form/types"
import type { StickyConfig } from "../table-sticky"

export type { Evaluable } from "../auto-form/types"
export type { StickyConfig } from "../table-sticky"

export interface ColumnConfig<T = unknown> {
  field: keyof T | string
  name: Evaluable<ReactNode>
  type?: "text" | "image" | "icon" | "qrcode" | "area" | "actions" | "custom"
  format?: (value: unknown, row: T) => ReactNode
  color?: string | ((value: unknown, row: T) => string | undefined)
  sortable?: boolean
  width?: string
  align?: "left" | "center" | "right"
  onclick?: (row: T, event: React.MouseEvent) => false | void
  render?: (
    value: unknown,
    row: T,
    ctx: { column: ColumnConfig<T> }
  ) => ReactNode
}

export interface TableAction<T = unknown> {
  name: Evaluable<ReactNode>
  icon?: ComponentType<{ className?: string }>
  color?: string
  onclick?: (row: T) => void
  disabled?: (row: T) => boolean
  show?: (row: T) => boolean
}

export interface TableQuery {
  page: number
  pageSize: number
  sort?: { field: string; direction: "asc" | "desc" }
  filter?: Record<string, unknown>
}

export interface TableOptions<T = unknown> {
  data?: T[]
  fetch?: (query: TableQuery) => Promise<{ rows: T[]; total: number }>
  columns: ColumnConfig<T>[]
  actions?: TableAction<T>[]
  rowKey?: keyof T | ((row: T) => string)
  selectable?: boolean
  selection?: "single" | "multiple"
  pageSize?: number
  pageSizeOptions?: number[]
  defaultSort?: { field: string; direction: "asc" | "desc" }
  filter?: Record<string, unknown>
  title?: ReactNode
  onRowClick?: (row: T) => void
  onSelectionChange?: (rows: T[]) => void
  onQueryChange?: (query: TableQuery) => void
  className?: string
  loading?: boolean
  /** Fill a bounded flex workspace; only the table viewport scrolls. */
  fill?: boolean
  /** Show a fixed sequence column. Enabled unless explicitly disabled. */
  rowNumbers?: boolean
  /** Offset for rows supplied by an external/server pager. */
  rowOffset?: number
  /** Existing page filters and permission-aware create/domain buttons. */
  toolbar?: ReactNode | false
  /** Primary page actions beside refresh/export, after the search and filters. */
  toolbarActions?: ReactNode
  onRefresh?: () => void | Promise<void>
  onExport?: () => void | Promise<void>
  onImport?: () => void | Promise<void>
  exportable?: boolean
  /** Local search over supplied rows; use a callback for computed columns. */
  searchable?: boolean | ((row: T) => string)
  /** Existing server pager or tree summary; never paginates rows a second time. */
  footer?: ReactNode
  /** Disable slicing and the built-in pager for trees or externally paged data. */
  pagination?: boolean
  emptyContent?: ReactNode
  rowProps?: (row: T) => HTMLAttributes<HTMLTableRowElement> & {
    [key: `data-${string}`]: unknown
  }
  /** Sticky columns/header/pagination config. */
  sticky?: StickyConfig
}

export interface UseAutoTableReturn<T> {
  rows: T[]
  total: number
  loading: boolean
  error: Error | null
  query: TableQuery
  selected: T[]
  setPage: (page: number) => void
  setPageSize: (size: number) => void
  setSort: (field: string) => void
  setFilter: (filter: Record<string, unknown>) => void
  toggleSelect: (row: T) => void
  selectAll: (rows: T[]) => void
  refresh: () => void
}

export function getRowKey<T>(
  row: T,
  rowKey?: keyof T | ((row: T) => string)
): string {
  if (!rowKey) return (row as { id?: string })?.id ?? String(row)
  if (typeof rowKey === "function") return rowKey(row)
  return String(row[rowKey])
}

export function formatCellValue<T>(row: T, column: ColumnConfig<T>): ReactNode {
  const raw = (row as Record<string, unknown>)[column.field as string]
  if (column.format) return column.format(raw, row)
  return raw as ReactNode
}
