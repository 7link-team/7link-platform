import {
  AutoTable,
  type ColumnConfig,
} from "@workspace/ui/components/auto-table"
import {
  AutoForm,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useRef,
  useMemo,
  useState,
  type DragEvent,
} from "react"
import {
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  FolderTree,
  GripVertical,
  Boxes,
  ChevronDown,
  ChevronRight,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Input } from "@workspace/ui/components/input"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type Menu, type MenuInput } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { operatingPlatformID } from "../../lib/organization-scope"
import {
  menuLabel,
  menuRows,
  adjacentMenuMove,
  droppedMenuMove,
  type MenuMove,
} from "../../lib/navigation"
import { defaultLocale, locales, localeNames } from "@workspace/ui/i18n/config"

const rootMenu = "__root__"

export function SystemMenusPage() {
  return <MenusPage scope="system" />
}

export default function MenusPage({
  scope = "tenant",
}: {
  scope?: "system" | "tenant"
}) {
  const t = useTranslations("iam")
  const scopeAllowed = useAllowed()
  const system = scope === "system"
  const allowed = useCallback(
    (code: string) => scopeAllowed(system ? `system_${code}` : code),
    [scopeAllowed, system]
  )
  const menuApi = useMemo(
    () =>
      system
        ? {
            list: iamApi.systemMenus,
            create: iamApi.createSystemMenu,
            update: iamApi.updateSystemMenu,
            remove: iamApi.deleteSystemMenu,
          }
        : {
            list: iamApi.menus,
            create: iamApi.createMenu,
            update: iamApi.updateMenu,
            remove: iamApi.deleteMenu,
          },
    [system]
  )
  const locale = useLocale()

  const [menus, setMenus] = useState<Menu[]>([])
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Menu | null>(null)
  const [parentID, setParentID] = useState(rootMenu)
  const [status, setStatus] = useState<MenuInput["status"]>("active")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [translations, setTranslations] = useState<
    { locale: string; label: string }[]
  >([])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [listCollapsed, setListCollapsed] = useState<Set<string>>(new Set())
  const [selectedID, setSelectedID] = useState("")
  const [dragging, setDragging] = useState("")
  const [dropTarget, setDropTarget] = useState<{
    id: string
    position: "before" | "inside" | "after"
  } | null>(null)
  const [previewLanguage, setPreviewLanguage] = useState<string | null>(null)
  const loadGeneration = useRef(0)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoading(true)
    setLoadError("")
    try {
      const next = await menuApi.list()
      if (generation === loadGeneration.current) {
        setMenus(next)
        setSelectedID((id) =>
          next.some((menu) => menu.parent_id === id) ? id : ""
        )
      }
    } catch (cause) {
      if (generation === loadGeneration.current) {
        setMenus([])
        setLoadError((cause as Error).message)
      }
    } finally {
      if (generation === loadGeneration.current) setLoading(false)
    }
  }, [menuApi])

  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    return () => {
      active = false
    }
  }, [refresh])

  const openCreate = () => {
    setEditing(null)
    setParentID(selectedID || rootMenu)
    setStatus("active")
    setTranslations(locales.map((locale) => ({ locale, label: "" })))
    setOpen(true)
  }

  const openEdit = (menu: Menu) => {
    setEditing(menu)
    setParentID(menu.parent_id || rootMenu)
    setStatus(menu.status)
    setTranslations(
      Object.entries(menu.translations ?? {}).map(([locale, label]) => ({
        locale,
        label,
      }))
    )
    setOpen(true)
  }

  const submit = async (data: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    const translated = (
      data.translations as { locale: string; label: string }[]
    ).filter((item) => item.label.trim())
    if (
      new Set(translated.map((item) => item.locale.trim().toLowerCase()))
        .size !== translated.length
    ) {
      setError(t("duplicateLanguage"))
      setBusy(false)
      return
    }
    const body: MenuInput = {
      parent_id: data.parent_id === rootMenu ? "" : String(data.parent_id),
      label: String(data.label),
      translations: Object.fromEntries(
        translated.map((item) => [item.locale.trim(), item.label.trim()])
      ),
      route: String(data.route),
      icon: String(data.icon),
      permission_code:
        system && !allowed("menu_bind_permission")
          ? (editing?.permission_code ?? "")
          : String(data.permission_code ?? ""),
      sort_order: Number(data.sort_order || 0),
      status: data.status as MenuInput["status"],
    }
    try {
      if (editing) await menuApi.update(editing.id, body)
      else await menuApi.create(body)
      setOpen(false)
      setEditing(null)
      await refresh()
      window.dispatchEvent(new Event("menu-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async (id: string) => {
    setBusy(true)
    setError("")
    try {
      await menuApi.remove(id)
      await refresh()
      window.dispatchEvent(new Event("menu-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const rows = menuRows(loading || loadError ? [] : menus)
  const branchIDs = new Set(
    menus.flatMap((menu) => (menu.parent_id ? [menu.parent_id] : []))
  )
  const selected = rows.find(({ menu }) => menu.id === selectedID)
  const visibleRows = rows.filter(({ ancestors }) => {
    if (selectedID && !ancestors.includes(selectedID)) return false
    const visibleAncestors = selectedID
      ? ancestors.slice(ancestors.indexOf(selectedID) + 1)
      : ancestors
    return !visibleAncestors.some((id) => listCollapsed.has(id))
  })
  const selectBranch = (id: string) => {
    setSelectedID(id)
    setListCollapsed(id ? new Set(branchIDs) : new Set())
  }
  const toggleListBranch = (id: string) => {
    setListCollapsed((previous) => {
      const next = new Set(previous)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }
  const displayLanguage = previewLanguage ?? locale
  const move = async (id: string, destination: MenuMove | null) => {
    if (!destination || busy || !allowed("menu_update")) return
    setBusy(true)
    setError("")
    try {
      await menuApi.update(id, destination)
      await refresh()
      window.dispatchEvent(new Event("menu-catalog-change"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const dragProps = (id: string) => ({
    draggable: Boolean(id && allowed("menu_update") && !busy),
    onDragStart: (event: DragEvent<HTMLElement>) => {
      if (!id || busy || !allowed("menu_update")) {
        event.preventDefault()
        return
      }
      event.dataTransfer.setData("application/x-menu-id", id)
      event.dataTransfer.effectAllowed = "move"
      setDragging(id)
    },
    onDragOver: (event: DragEvent<HTMLElement>) => {
      if (!dragging || busy || !allowed("menu_update")) return
      const rect = event.currentTarget.getBoundingClientRect()
      const ratio = (event.clientY - rect.top) / rect.height
      const position =
        !id || (ratio >= 0.3 && ratio <= 0.7)
          ? "inside"
          : ratio < 0.3
            ? "before"
            : "after"
      if (!droppedMenuMove(menus, dragging, id, position)) return
      event.preventDefault()
      event.dataTransfer.dropEffect = "move"
      setDropTarget({ id, position })
    },
    onDrop: (event: DragEvent<HTMLElement>) => {
      event.preventDefault()
      const source =
        dragging || event.dataTransfer.getData("application/x-menu-id")
      const rect = event.currentTarget.getBoundingClientRect()
      const ratio = (event.clientY - rect.top) / rect.height
      const position =
        !id || (ratio >= 0.3 && ratio <= 0.7)
          ? "inside"
          : ratio < 0.3
            ? "before"
            : "after"
      setDragging("")
      setDropTarget(null)
      void move(source, droppedMenuMove(menus, source, id, position))
    },
    onDragEnd: () => {
      setDragging("")
      setDropTarget(null)
    },
  })
  const dropClass = (id: string) =>
    dropTarget?.id !== id
      ? ""
      : dropTarget.position === "inside"
        ? "bg-accent outline outline-2 outline-primary"
        : dropTarget.position === "before"
          ? "border-t-2 border-t-primary"
          : "border-b-2 border-b-primary"
  const parentOptions = [
    { value: rootMenu, label: t("rootMenu") },
    ...rows
      .filter(
        ({ menu, ancestors }) =>
          menu.id !== editing?.id && !ancestors.includes(editing?.id ?? "")
      )
      .map(({ menu, depth }) => ({
        value: menu.id,
        label: `${"  ".repeat(depth)}${menuLabel(menu, locale)}`,
      })),
  ]

  const menuActionColumn: ColumnConfig<(typeof visibleRows)[number]> = {
    field: "actions",
    name: t("actions"),
    width: "160px",
    align: "right",
    type: "actions",
    render: (_value, { menu }) => (
      <div className="text-right">
        <div className="flex justify-end gap-1">
          {allowed("menu_update") && (
            <>
              <Button
                variant="ghost"
                size="icon"
                disabled={busy || !adjacentMenuMove(menus, menu.id, "up")}
                onClick={() =>
                  void move(menu.id, adjacentMenuMove(menus, menu.id, "up"))
                }
                aria-label={t("moveMenuUp", {
                  name: menu.label,
                })}
                title={t("moveMenuUp", { name: menu.label })}
              >
                <ArrowUp />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                disabled={busy || !adjacentMenuMove(menus, menu.id, "down")}
                onClick={() =>
                  void move(menu.id, adjacentMenuMove(menus, menu.id, "down"))
                }
                aria-label={t("moveMenuDown", {
                  name: menu.label,
                })}
                title={t("moveMenuDown", {
                  name: menu.label,
                })}
              >
                <ArrowDown />
              </Button>
            </>
          )}
          {allowed("menu_update") && (
            <Button
              variant="ghost"
              size="icon"
              disabled={busy}
              onClick={() => openEdit(menu)}
              aria-label={t("editNamed", {
                name: menu.label,
              })}
              title={t("editMenu")}
            >
              <Pencil />
            </Button>
          )}
          {allowed("menu_delete") && (
            <Button
              variant="ghost"
              size="icon"
              disabled={busy}
              onClick={() => void remove(menu.id)}
              aria-label={t("deleteNamed", {
                name: menu.label,
              })}
              title={t("deleteMenu")}
            >
              <Trash2 className="text-destructive" />
            </Button>
          )}
        </div>
      </div>
    ),
  }
  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
      data-menu-workspace
    >
      <PageHeader
        title={t(
          system
            ? "systemMenuManagement"
            : operatingPlatformID()
              ? "platformMenuManagement"
              : "tenantMenuManagement"
        )}
      />
      {error && <Alert>{error}</Alert>}
      {loadError && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button variant="outline" onClick={() => void refresh()}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {loading && <p role="status">{t("loading")}</p>}

      {!loadError && (
        <div className="grid min-h-0 min-w-0 flex-1 grid-rows-[auto_minmax(0,1fr)] gap-4 lg:grid-cols-[240px_minmax(0,1fr)] lg:grid-rows-1">
          <nav
            aria-label={t("menuTree")}
            className="flex min-h-0 min-w-0 flex-col border-b pb-4 lg:border-e lg:border-b-0 lg:pe-4"
            data-menu-navigation
          >
            <button
              type="button"
              className={`mb-2 flex min-h-10 w-full shrink-0 items-center gap-2 rounded px-2 text-start text-sm font-medium ${selectedID ? "hover:bg-accent" : "bg-accent"} ${dropClass("")}`}
              onClick={() => selectBranch("")}
              {...dragProps("")}
              data-menu-root
            >
              <FolderTree className="size-4 shrink-0" />
              {t("allMenus")}
            </button>
            <div
              role="tree"
              aria-label={t("menuTree")}
              className="max-h-64 min-h-0 overflow-auto overscroll-contain lg:max-h-none lg:flex-1"
              tabIndex={0}
            >
              {rows
                .filter(
                  (row) =>
                    branchIDs.has(row.menu.id) &&
                    !row.ancestors.some((id) => collapsed.has(id))
                )
                .map(({ menu, depth }) => {
                  const children = menus.some(
                    (child) =>
                      child.parent_id === menu.id && branchIDs.has(child.id)
                  )
                  return (
                    <div
                      key={menu.id}
                      role="treeitem"
                      aria-level={depth + 1}
                      aria-selected={selectedID === menu.id}
                      aria-expanded={
                        children ? !collapsed.has(menu.id) : undefined
                      }
                      className={`flex min-h-10 min-w-0 items-center rounded text-sm ${selectedID === menu.id ? "bg-accent" : "hover:bg-accent"} ${dropClass(menu.id)}`}
                      style={{ paddingInlineStart: Math.min(depth, 12) * 14 }}
                      data-menu-tree-id={menu.id}
                      {...dragProps(menu.id)}
                    >
                      {children ? (
                        <button
                          type="button"
                          className="grid size-8 shrink-0 place-items-center"
                          aria-label={t(
                            collapsed.has(menu.id)
                              ? "expandNamed"
                              : "collapseNamed",
                            { name: menuLabel(menu, displayLanguage) }
                          )}
                          onClick={() =>
                            setCollapsed((previous) => {
                              const next = new Set(previous)
                              if (next.has(menu.id)) next.delete(menu.id)
                              else next.add(menu.id)
                              return next
                            })
                          }
                        >
                          {collapsed.has(menu.id) ? (
                            <ChevronRight className="size-4" />
                          ) : (
                            <ChevronDown className="size-4" />
                          )}
                        </button>
                      ) : (
                        <span className="w-8 shrink-0" />
                      )}
                      <button
                        type="button"
                        className="flex min-w-0 flex-1 items-center gap-2 py-2 pe-2 text-start"
                        onClick={() => selectBranch(menu.id)}
                      >
                        <Boxes className="size-4 shrink-0 text-muted-foreground" />
                        <span className="break-words">
                          {menuLabel(menu, displayLanguage)}
                        </span>
                      </button>
                    </div>
                  )
                })}
            </div>
          </nav>
          <section
            className="flex min-h-0 min-w-0 flex-col"
            aria-label={t("menuChildren")}
            data-menu-content
          >
            <div className="mb-3 flex min-h-10 shrink-0 flex-wrap items-center justify-between gap-2 border-b pb-2">
              <h2 className="min-w-0 text-sm font-semibold break-words">
                {selected
                  ? [
                      ...selected.ancestors.map((id) =>
                        menuLabel(
                          menus.find((menu) => menu.id === id)!,
                          displayLanguage
                        )
                      ),
                      menuLabel(selected.menu, displayLanguage),
                    ].join(" / ")
                  : t("allMenus")}
              </h2>
              <span className="text-xs text-muted-foreground">
                {visibleRows.length}
              </span>
            </div>
            <div className="flex min-h-0 min-w-0 flex-1 flex-col border">
              <AutoTable
                searchable={({ menu }) =>
                  [
                    menuLabel(menu, displayLanguage),
                    menu.id,
                    menu.route,
                    menu.permission_code,
                  ].join(" ")
                }
                fill
                onRefresh={refresh}
                toolbar={
                  <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
                    {allowed("menu_create") && (
                      <Button onClick={openCreate}>
                        <Plus />
                        {t("createMenu")}
                      </Button>
                    )}
                    <AutoForm
                      key={locale}
                      className="md:grid-cols-1"
                      fields={{
                        preview_language: {
                          name: t("displayLanguage"),
                          type: "select",
                          value: previewLanguage ?? locale,
                          config: {
                            options: locales.map((language) => ({
                              value: language,
                              label: localeNames[language],
                            })),
                          },
                        },
                      }}
                      onChange={(values) => {
                        if (
                          values.preview_language !==
                          (previewLanguage ?? locale)
                        )
                          setPreviewLanguage(String(values.preview_language))
                      }}
                    />
                  </div>
                }
                footer={
                  <>
                    <span className="text-sm text-muted-foreground">
                      {t("menuCounts", {
                        roots: menus.filter((menu) => !menu.parent_id).length,
                        total: menus.length,
                      })}
                    </span>
                  </>
                }
                pagination={false}
                className="[&_table]:min-w-[1100px]"
                sticky={{
                  right:
                    allowed("menu_update") || allowed("menu_delete") ? 1 : 0,
                }}
                data={visibleRows}
                rowKey={(row) => row.menu.id}
                rowProps={({ menu }) => ({
                  ...dragProps(menu.id),
                  "data-menu-row-id": menu.id,
                  className: dropClass(menu.id),
                })}
                emptyContent={
                  !loading && !loadError ? (
                    <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
                      <Boxes className="mb-2 size-7" />
                      {t("noMenus")}
                    </div>
                  ) : null
                }
                columns={[
                  {
                    field: "menu",
                    name: t("menu"),
                    type: "custom",
                    render: (_value, { menu, depth }) => (
                      <div>
                        <span
                          className="flex items-center gap-2 font-medium"
                          style={{
                            paddingInlineStart:
                              Math.min(
                                depth - (selected ? selected.depth + 1 : 0),
                                8
                              ) * 20,
                          }}
                        >
                          {menus.some(
                            (child) => child.parent_id === menu.id
                          ) ? (
                            <button
                              type="button"
                              className="grid size-8 shrink-0 place-items-center rounded hover:bg-muted"
                              aria-expanded={!listCollapsed.has(menu.id)}
                              aria-label={t(
                                listCollapsed.has(menu.id)
                                  ? "expandNamed"
                                  : "collapseNamed",
                                { name: menuLabel(menu, displayLanguage) }
                              )}
                              title={t(
                                listCollapsed.has(menu.id)
                                  ? "expandNamed"
                                  : "collapseNamed",
                                { name: menuLabel(menu, displayLanguage) }
                              )}
                              onClick={() => toggleListBranch(menu.id)}
                            >
                              {listCollapsed.has(menu.id) ? (
                                <ChevronRight className="size-4" />
                              ) : (
                                <ChevronDown className="size-4" />
                              )}
                            </button>
                          ) : (
                            <span className="w-8 shrink-0" />
                          )}
                          {allowed("menu_update") ? (
                            <GripVertical className="size-4 shrink-0 text-muted-foreground" />
                          ) : (
                            <Boxes className="size-4 text-primary" />
                          )}
                          <span className="min-w-36 break-words">
                            {menuLabel(menu, previewLanguage ?? locale)}
                            <span className="mt-1 block text-xs font-normal text-muted-foreground">
                              {Object.keys(menu.translations ?? {}).join(
                                " · "
                              ) || t("defaultLabel")}
                            </span>
                          </span>
                        </span>
                      </div>
                    ),
                  },
                  {
                    field: "route",
                    name: t("route"),
                    type: "custom",
                    render: (_value, { menu }) => (
                      <div className="font-mono text-xs">
                        {menu.route || "-"}
                      </div>
                    ),
                  },
                  {
                    field: "permissionCode",
                    name: t("permissionCode"),
                    type: "custom",
                    render: (_value, { menu }) => (
                      <div className="font-mono text-xs">
                        {menu.permission_code || "-"}
                      </div>
                    ),
                  },
                  {
                    field: "sortOrder",
                    name: t("sortOrder"),
                    type: "custom",
                    render: (_value, { menu }) => (
                      <div>
                        <span className="flex items-center gap-1 text-xs">
                          <ArrowUpDown size={13} />
                          {menu.sort_order}
                        </span>
                      </div>
                    ),
                  },
                  {
                    field: "status",
                    name: t("status"),
                    type: "custom",
                    render: (_value, { menu }) => (
                      <div>
                        <Badge
                          className="whitespace-nowrap"
                          variant={
                            menu.status === "active"
                              ? "secondary"
                              : "destructive"
                          }
                        >
                          {menu.status === "active"
                            ? t("active")
                            : t("disable")}
                        </Badge>
                      </div>
                    ),
                  },
                  ...(allowed("menu_update") || allowed("menu_delete")
                    ? [menuActionColumn]
                    : []),
                ]}
              />
            </div>
          </section>
        </div>
      )}
      <Dialog
        open={open}
        onOpenChange={(nextOpen) => {
          setOpen(nextOpen)
          if (!nextOpen) setEditing(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing ? t("editMenu") : t("createMenu")}
            </DialogTitle>
            <DialogDescription>
              {t("menuContractDescription")}
            </DialogDescription>
          </DialogHeader>
          <AutoForm
            key={editing?.id ?? "create"}
            mode={editing ? "edit" : "add"}
            id="menu-form"
            fields={{
              label: {
                name: t("defaultLabel"),
                type: "text",
                value: editing?.label ?? "",
                required: true,
                config: { id: "menu-label", maxLength: 128 },
              },
              translations: {
                name: t("translatedLabels"),
                type: "custom",
                component: MenuTranslationsField,
                value: translations,
              },
              route: {
                name: t("frontendRoute"),
                type: "text",
                value: editing?.route ?? "",
                config: { id: "menu-route", maxLength: 512 },
              },
              icon: {
                name: t("iconIdentifier"),
                type: "text",
                value: editing?.icon ?? "",
                config: { id: "menu-icon", maxLength: 64 },
              },
              permission_code: {
                name: t("permissionCode"),
                type: "text",
                value: editing?.permission_code ?? "",
                disabled: system && !allowed("menu_bind_permission"),
                config: { id: "menu-permission_code", maxLength: 128 },
              },
              sort_order: {
                name: t("sortOrder"),
                type: "number",
                value: editing?.sort_order ?? 0,
                config: { id: "menu-sort_order", min: -100000, max: 100000 },
              },
              parent_id: {
                name: t("parentMenu"),
                type: "select",
                value: parentID,
                config: { id: "menu-parent", options: parentOptions },
              },
              status: {
                name: t("status"),
                type: "select",
                value: status,
                config: {
                  id: "menu-status",
                  options: { active: t("active"), disabled: t("disable") },
                },
              },
            }}
            onSubmit={submit}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button type="submit" form="menu-form" disabled={busy}>
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function MenuTranslationsField({
  value,
  onChange,
  disabled,
  readonly,
  labelId,
  error,
  errorId,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const translations = value as { locale: string; label: string }[]
  const setTranslations = (
    update: (current: typeof translations) => typeof translations
  ) => {
    if (!disabled && !readonly) onChange(update(translations))
  }
  return (
    <fieldset
      disabled={disabled || readonly}
      aria-labelledby={labelId}
      aria-describedby={error ? errorId : undefined}
    >
      <fieldset className="grid gap-3 border-y py-4 sm:col-span-2">
        {translations.map((item, index) => (
          <div className="flex items-end gap-2" key={index}>
            <label className="grid w-28 shrink-0 gap-1 text-xs">
              {t("languageTag")}
              <select
                aria-label={t("languageTagNumber", { number: index + 1 })}
                value={item.locale}
                className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                onChange={(event) =>
                  setTranslations((previous) =>
                    previous.map((row, i) =>
                      i === index ? { ...row, locale: event.target.value } : row
                    )
                  )
                }
              >
                {locales.map((locale) => (
                  <option
                    key={locale}
                    value={locale}
                    disabled={translations.some(
                      (row, i) => i !== index && row.locale === locale
                    )}
                  >
                    {localeNames[locale]}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid min-w-0 flex-1 gap-1 text-xs">
              {t("translation")}
              <Input
                aria-label={t("translationNumber", { number: index + 1 })}
                value={item.label}
                maxLength={128}
                onChange={(event) =>
                  setTranslations((previous) =>
                    previous.map((row, i) =>
                      i === index ? { ...row, label: event.target.value } : row
                    )
                  )
                }
              />
            </label>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t("removeLanguageNumber", {
                number: index + 1,
              })}
              title={t("removeLanguage")}
              onClick={() =>
                setTranslations((previous) =>
                  previous.filter((_, i) => i !== index)
                )
              }
            >
              <Trash2 />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="outline"
          className="justify-self-start"
          disabled={translations.length >= locales.length}
          onClick={() =>
            setTranslations((previous) => [
              ...previous,
              {
                locale:
                  locales.find(
                    (locale) => !previous.some((row) => row.locale === locale)
                  ) ?? defaultLocale,
                label: "",
              },
            ])
          }
        >
          <Plus />
          {t("addLanguage")}
        </Button>
      </fieldset>
      {error && (
        <p id={errorId} className="mt-1 text-xs text-destructive">
          {error}
        </p>
      )}
    </fieldset>
  )
}
