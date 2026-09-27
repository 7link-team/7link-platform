import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
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
  Languages,
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
import { Label } from "@workspace/ui/components/label"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
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

export default function MenusPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
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
      const next = await iamApi.menus()
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
  }, [])

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

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const translated = translations.filter((item) => item.label.trim())
    if (
      new Set(translated.map((item) => item.locale.trim().toLowerCase()))
        .size !== translated.length
    ) {
      setError(t("duplicateLanguage"))
      setBusy(false)
      return
    }
    const body: MenuInput = {
      parent_id: parentID === rootMenu ? "" : parentID,
      label: String(data.get("label")),
      translations: Object.fromEntries(
        translated.map((item) => [item.locale.trim(), item.label.trim()])
      ),
      route: String(data.get("route")),
      icon: String(data.get("icon")),
      permission_code: String(data.get("permission_code")),
      sort_order: Number(data.get("sort_order") || 0),
      status,
    }
    try {
      if (editing) await iamApi.updateMenu(editing.id, body)
      else await iamApi.createMenu(body)
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
      await iamApi.deleteMenu(id)
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
      await iamApi.updateMenu(id, destination)
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

  return (
    <>
      <PageHeader
        title={t(
          operatingPlatformID()
            ? "platformMenuManagement"
            : "tenantMenuManagement"
        )}
        action={
          allowed("menu_create") && (
            <Button onClick={openCreate}>
              <Plus />
              {t("createMenu")}
            </Button>
          )
        }
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
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b pb-3">
          <span className="text-sm text-muted-foreground">
            {!loading &&
              !loadError &&
              t("menuCounts", {
                roots: menus.filter((menu) => !menu.parent_id).length,
                total: menus.length,
              })}
          </span>
          <label className="flex items-center gap-2 text-sm">
            <Languages className="size-4" />
            {t("displayLanguage")}
            <select
              aria-label={t("menuPreviewLanguage")}
              className="h-9 rounded-md border bg-background px-2"
              value={previewLanguage ?? locale}
              onChange={(event) => setPreviewLanguage(event.target.value)}
            >
              {locales.map((locale) => (
                <option key={locale} value={locale}>
                  {localeNames[locale]}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
      {!loadError && (
        <div className="grid min-w-0 gap-4 md:grid-cols-[240px_minmax(0,1fr)]">
          <nav
            aria-label={t("menuTree")}
            className="min-w-0 border-b pb-4 md:border-e md:border-b-0 md:pe-4"
          >
            <button
              type="button"
              className={`mb-2 flex min-h-10 w-full items-center gap-2 rounded px-2 text-start text-sm font-medium ${selectedID ? "hover:bg-accent" : "bg-accent"} ${dropClass("")}`}
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
              className="max-h-64 overflow-auto md:max-h-[calc(100vh-260px)]"
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
          <section className="min-w-0" aria-label={t("menuChildren")}>
            <div className="mb-3 flex min-h-10 flex-wrap items-center justify-between gap-2 border-b pb-2">
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
            <div className="max-h-[calc(100dvh-320px)] min-h-40 overflow-auto border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("menu")}</TableHead>
                    <TableHead>{t("route")}</TableHead>
                    <TableHead>{t("permissionCode")}</TableHead>
                    <TableHead>{t("sortOrder")}</TableHead>
                    <TableHead>{t("status")}</TableHead>
                    <TableHead className="w-24 text-right">
                      {t("actions")}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleRows.map(({ menu, depth }) => (
                    <TableRow
                      key={menu.id}
                      data-menu-row-id={menu.id}
                      className={dropClass(menu.id)}
                      {...dragProps(menu.id)}
                    >
                      <TableCell>
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
                          <span>
                            {menuLabel(menu, previewLanguage ?? locale)}
                            <span className="mt-1 block text-xs font-normal text-muted-foreground">
                              {Object.keys(menu.translations ?? {}).join(
                                " · "
                              ) || t("defaultLabel")}
                            </span>
                          </span>
                        </span>
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {menu.route || "-"}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {menu.permission_code || "-"}
                      </TableCell>
                      <TableCell>
                        <span className="flex items-center gap-1 text-xs">
                          <ArrowUpDown size={13} />
                          {menu.sort_order}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Badge
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
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          {allowed("menu_update") && (
                            <>
                              <Button
                                variant="ghost"
                                size="icon"
                                disabled={
                                  busy ||
                                  !adjacentMenuMove(menus, menu.id, "up")
                                }
                                onClick={() =>
                                  void move(
                                    menu.id,
                                    adjacentMenuMove(menus, menu.id, "up")
                                  )
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
                                disabled={
                                  busy ||
                                  !adjacentMenuMove(menus, menu.id, "down")
                                }
                                onClick={() =>
                                  void move(
                                    menu.id,
                                    adjacentMenuMove(menus, menu.id, "down")
                                  )
                                }
                                aria-label={t("moveMenuDown", {
                                  name: menu.label,
                                })}
                                title={t("moveMenuDown", { name: menu.label })}
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
                              aria-label={t("editNamed", { name: menu.label })}
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
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {!loading && !loadError && visibleRows.length === 0 && (
                <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
                  <Boxes className="mb-2 size-7" />
                  {t("noMenus")}
                </div>
              )}
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
          <form
            key={editing?.id ?? "create"}
            id="menu-form"
            className="grid gap-4 sm:grid-cols-2"
            onSubmit={submit}
          >
            <MenuField
              label={t("defaultLabel")}
              name="label"
              defaultValue={editing?.label}
              maxLength={128}
              required
            />
            <fieldset className="grid gap-3 border-y py-4 sm:col-span-2">
              <legend className="px-1 text-sm font-medium">
                {t("translatedLabels")}
              </legend>
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
                            i === index
                              ? { ...row, locale: event.target.value }
                              : row
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
                            i === index
                              ? { ...row, label: event.target.value }
                              : row
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
                          (locale) =>
                            !previous.some((row) => row.locale === locale)
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
            <MenuField
              label={t("frontendRoute")}
              name="route"
              defaultValue={editing?.route}
              maxLength={512}
            />
            <MenuField
              label={t("iconIdentifier")}
              name="icon"
              defaultValue={editing?.icon}
              maxLength={64}
            />
            <MenuField
              label={t("permissionCode")}
              name="permission_code"
              defaultValue={editing?.permission_code}
              maxLength={128}
            />
            <MenuField
              label={t("sortOrder")}
              name="sort_order"
              type="number"
              defaultValue={String(editing?.sort_order ?? 0)}
              min={-100000}
              max={100000}
            />
            <div className="grid gap-2">
              <Label htmlFor="menu-parent">{t("parentMenu")}</Label>
              <SimpleSelect
                id="menu-parent"
                options={parentOptions}
                value={parentID}
                onValueChange={setParentID}
              />
            </div>
            <div className="grid gap-2 sm:col-span-2">
              <Label htmlFor="menu-status">{t("status")}</Label>
              <SimpleSelect
                id="menu-status"
                options={[
                  { value: "active", label: t("active") },
                  { value: "disabled", label: t("disable") },
                ]}
                value={status}
                onValueChange={(value) =>
                  setStatus(value as MenuInput["status"])
                }
              />
            </div>
          </form>
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
    </>
  )
}

function MenuField({
  label,
  name,
  type = "text",
  required = false,
  defaultValue,
  maxLength,
  min,
  max,
}: {
  label: string
  name: string
  type?: string
  required?: boolean
  defaultValue?: string
  maxLength?: number
  min?: number
  max?: number
}) {
  return (
    <div className="grid gap-2">
      <Label htmlFor={`menu-${name}`}>{label}</Label>
      <Input
        id={`menu-${name}`}
        name={name}
        type={type}
        required={required}
        defaultValue={defaultValue}
        maxLength={maxLength}
        min={min}
        max={max}
      />
    </div>
  )
}
