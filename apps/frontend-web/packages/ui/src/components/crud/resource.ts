import type { ReactNode } from "react"
import { Copy, Pencil, Trash2 } from "lucide-react"

import type {
  ApiClient,
  FilterSyntax,
  FilterValue,
  SortDir,
} from "@workspace/ui/lib/api-client"
import type { FormConfig, FormMode } from "../auto-form/types"
import type { StickyConfig } from "../table-sticky"
import type {
  ActionDef,
  ColumnDef,
  FilterFieldOption,
  FormFactory,
  Row,
  TableOptions,
} from "./types"

/**
 * CrudResource is the single config object that fully describes a CRUD resource — its form,
 * its table, its API and its behavior. crudFactory and crudTableOptions derive the
 * inputs for the existing CrudForm and CommonTable components. Operation callbacks
 * bind an existing application client when its routes differ from the REST defaults.
 */
export interface CrudResource {
  /** Route base for navigation, e.g. '/goods'. */
  basePath: string
  /** API base; defaults to basePath. */
  apiPath?: string
  /** Override the POST path for create only (list/get/update/delete stay on apiPath).
   *  Used when create targets a distinct endpoint, e.g. HQ operators POST /operators/hq
   *  while the list reads GET /operators. Defaults to apiPath. */
  createApiPath?: string
  /** List page heading. */
  title?: ReactNode
  /** Row identity key (default 'id'). */
  idKey?: string

  // --- form ---
  /** Bind the existing application client; omitted operations use the REST defaults. */
  getInfo?: FormFactory["getInfo"]
  commitAdd?: FormFactory["commitAdd"]
  commitEdit?: FormFactory["commitEdit"]
  /** Metadata for this mode's write operation. Return null while unavailable. */
  getContract?: (mode: FormMode) => FormFactory["contract"]
  /** Field definition; receives the form mode (add/edit/detail/copy) for mode-aware forms. */
  getFormDef: (mode: FormMode) => FormConfig
  /** Server → form mapping (parse JSON, scale prices, …). */
  toLocalData?: (server: Row) => Row | Promise<Row>
  /** Form → server mapping. */
  toServerData?: (local: Row) => Row
  commitCallback?: {
    onSuccess?: () => void
    onError?: (error: unknown) => void
  }
  /** When false, the form has no submit/edit button (read-only resource). */
  editable?: boolean
  successMessage?: string

  // --- table ---
  fetch?: TableOptions["fetch"]
  /** Bind deletion to the existing client, retaining its scope/version handling. */
  deleteRecord?: (row: Row) => Promise<unknown>
  /** Project existing permission/state decisions onto standard actions. */
  actionRules?: Partial<
    Record<
      "detail" | "edit" | "copy" | "delete",
      Pick<ActionDef, "show" | "disabled">
    >
  >
  columns: ColumnDef[]
  filters?: FilterFieldOption[]
  order?: Record<string, SortDir>
  limit?: number
  /**
   * Filter dialect sent to the backend. `simple` (default) emits `filter[field]=op:value`;
   * `rsql` serializes the filter to a single `filter=<expr>`, unlocking OR/IN/BETWEEN/grouping.
   * Both are accepted by the backend; behavior is identical for basic filters.
   */
  filterMode?: FilterSyntax
  /** Static filter merged into every list request, e.g. `{ source: 'local' }`. */
  staticFilter?: Record<string, FilterValue | unknown>
  /** Sticky columns/header/pagination for the list table. */
  sticky?: StickyConfig
  /** Extra row actions inserted before the delete action. */
  extraActions?: ActionDef[]
  /** Custom delete confirm message builder. */
  deleteConfirm?: (row: Row) => string

  /**
   * Backend export path (e.g. '/goods/export'). When set, the list page shows a split button:
   * primary action downloads Excel via the backend; dropdown also offers client-side CSV.
   * When omitted, only a client-side CSV button is shown (backwards-compatible).
   */
  exportPath?: string

  /**
   * Backend import path (e.g. '/sys/users/import'). When set, the list page shows an
   * Import button that opens a dialog; the template download is derived from exportPath
   * ('<exportPath>?template=1&format=xlsx').
   */
  importPath?: string

  // --- behavior toggles (all default on) ---
  features?: {
    add?: boolean
    edit?: boolean
    copy?: boolean
    detail?: boolean
    delete?: boolean
    export?: boolean
  }
}

export const resourceApiPath = (r: CrudResource): string =>
  r.apiPath ?? r.basePath
export const resourceIdKey = (r: CrudResource): string => r.idKey ?? "id"

/**
 * Labels for the standard row actions. Since {@link crudTableOptions} runs outside React
 * (it builds plain config), the translated strings are passed in by the caller (e.g. the
 * list page, via `useTranslations('crud')`). English defaults keep the builder usable
 * without a translator.
 */
export interface CrudActionLabels {
  edit: string
  copy: string
  delete: string
  /** Receives the row label (e.g. row.name) and returns the confirm message. */
  deleteConfirm: (name: string) => string
}

const DEFAULT_ACTION_LABELS: CrudActionLabels = {
  edit: "Edit",
  copy: "Copy",
  delete: "Delete",
  deleteConfirm: (name: string) => `Delete "${name}"?`,
}

/** Builds the {@link FormFactory} for a resource from a configured API client + mode. */
export function crudFactory(
  resource: CrudResource,
  api: ApiClient,
  mode: FormMode
): FormFactory {
  const apiPath = resourceApiPath(resource)
  return {
    getFormDef: () => resource.getFormDef(mode),
    getInfo:
      resource.getInfo ??
      (async (id) => (await api.get<Row>(`${apiPath}/${id}`)).data),
    contract: resource.getContract?.(mode),
    toLocalData: resource.toLocalData,
    toServerData: resource.toServerData,
    commitAdd:
      resource.commitAdd ??
      ((body) => api.post(resource.createApiPath ?? apiPath, body)),
    commitEdit:
      resource.commitEdit ?? ((id, body) => api.put(`${apiPath}/${id}`, body)),
    commitCallback: resource.commitCallback,
    editable: resource.editable,
  }
}

export interface CrudNav {
  detail: (row: Row) => void
  edit: (row: Row) => void
  copy: (row: Row) => void
}

/** Builds the list {@link TableOptions} (columns + standard actions + row-click) for a resource. */
export function crudTableOptions(
  resource: CrudResource,
  opts: {
    api: ApiClient
    nav: CrudNav
    refresh: () => void
    filter?: Record<string, unknown>
    /** Translated action labels; English defaults are used when omitted. */
    labels?: CrudActionLabels
    /** Custom confirm dialog; falls back to window.confirm when omitted. */
    confirm?: (msg: string) => Promise<boolean>
  }
): TableOptions {
  const f = resource.features ?? {}
  const apiPath = resourceApiPath(resource)
  const idKey = resourceIdKey(resource)
  const labels = opts.labels ?? DEFAULT_ACTION_LABELS

  const rules = resource.actionRules ?? {}
  const allowed = (action: keyof typeof rules, row: Row) =>
    rules[action]?.show?.(row) !== false &&
    rules[action]?.disabled?.(row) !== true
  const actions: ActionDef[] = []
  if (f.edit !== false)
    actions.push({
      name: labels.edit,
      icon: Pencil,
      ...rules.edit,
      onclick: (row) => {
        if (allowed("edit", row)) opts.nav.edit(row)
      },
    })
  if (f.copy !== false)
    actions.push({
      name: labels.copy,
      icon: Copy,
      ...rules.copy,
      onclick: (row) => {
        if (allowed("copy", row)) opts.nav.copy(row)
      },
    })
  if (resource.extraActions) actions.push(...resource.extraActions)
  if (f.delete !== false) {
    actions.push({
      name: labels.delete,
      icon: Trash2,
      color: "var(--destructive)",
      ...rules.delete,
      onclick: async (row) => {
        if (!allowed("delete", row)) return
        const msg =
          resource.deleteConfirm?.(row) ??
          labels.deleteConfirm(String(row.name ?? ""))
        const ok = opts.confirm ? await opts.confirm(msg) : window.confirm(msg)
        if (!ok || !allowed("delete", row)) return
        if (resource.deleteRecord) await resource.deleteRecord(row)
        else await opts.api.delete(`${apiPath}/${row[idKey]}`)
        opts.refresh()
      },
    })
  }

  return {
    url: apiPath,
    fetch: resource.fetch,
    filter: { ...(resource.staticFilter ?? {}), ...(opts.filter ?? {}) },
    order: resource.order,
    limit: resource.limit,
    filterMode: resource.filterMode,
    sticky: resource.sticky,
    rowClickable: (row) => f.detail !== false && allowed("detail", row),
    onclick:
      f.detail !== false
        ? (row) => {
            if (allowed("detail", row)) opts.nav.detail(row)
          }
        : undefined,
    actions,
    columns: resource.columns,
  }
}
