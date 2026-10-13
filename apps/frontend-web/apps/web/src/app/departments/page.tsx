import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react"
import {
  Building2,
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
import { operatingPlatformID } from "../../lib/organization-scope"
import {
  iamApi,
  type Department,
  type DepartmentInput,
  type Entity,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

const rootDepartment = "__root__"

export function SystemDepartmentsPage() {
  return <DepartmentsPage system />
}

export default function DepartmentsPage({
  system = false,
}: {
  system?: boolean
}) {
  const t = useTranslations("iam")
  const scopeAllowed = useAllowed()
  const allowed = useCallback(
    (code: string) =>
      scopeAllowed(
        system ? code.replace(/^dept_/, "system_department_") : code
      ),
    [scopeAllowed, system]
  )
  const api = useMemo(
    () =>
      system
        ? {
            ...iamApi,
            departments: iamApi.systemDepartments,
            createDepartment: (body: DepartmentInput) => {
              const { name, parent_id, sort_order, status } = body
              return iamApi.createSystemDepartment({
                name,
                parent_id,
                sort_order,
                status,
              })
            },
            updateDepartment: iamApi.updateSystemDepartment,
            deleteDepartment: iamApi.deleteSystemDepartment,
          }
        : iamApi,
    [system]
  )

  const [departments, setDepartments] = useState<Department[]>([])
  const [entities, setEntities] = useState<Entity[]>([])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Department | null>(null)
  const [entityID, setEntityID] = useState("")
  const [parentID, setParentID] = useState(rootDepartment)
  const [status, setStatus] = useState<DepartmentInput["status"]>("active")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const canViewEntities = scopeAllowed("entity_view")
  const loadGeneration = useRef(0)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoading(true)
    setLoadError("")
    try {
      const [nextDepartments, nextEntities] = await Promise.all([
        api.departments(),
        // The entity catalogue only labels a column, and reading it is
        // tenant-wide entity administration. Somebody whose dept_view is
        // narrowed to a few departments does not have that, and a refused label
        // lookup used to turn this whole page into one refusal -- so it is asked
        // for only where the account holds it.
        system || operatingPlatformID() || !canViewEntities
          ? Promise.resolve([])
          : iamApi.entities(),
      ])
      if (generation === loadGeneration.current) {
        setDepartments(nextDepartments)
        setEntities(nextEntities)
      }
    } catch (cause) {
      if (generation === loadGeneration.current) {
        setDepartments([])
        setEntities([])
        setLoadError((cause as Error).message)
      }
    } finally {
      if (generation === loadGeneration.current) setLoading(false)
    }
  }, [api, system, canViewEntities])

  useEffect(() => {
    let active = true
    void Promise.resolve().then(() => {
      if (active) void refresh()
    })
    return () => {
      active = false
    }
  }, [refresh])

  const childrenByParent = useMemo(() => {
    const result = new Map<string, number>()
    for (const department of departments) {
      const parent = department.parent_id ?? ""
      result.set(parent, (result.get(parent) ?? 0) + 1)
    }
    return result
  }, [departments])

  const departmentByID = useMemo(
    () => new Map(departments.map((department) => [department.id, department])),
    [departments]
  )

  const entityByID = useMemo(
    () => new Map(entities.map((entity) => [entity.id, entity])),
    [entities]
  )

  const visibleDepartments = useMemo(
    () =>
      loading || loadError
        ? []
        : filterCollapsedDepartments(departments, collapsed),
    [collapsed, departments, loading, loadError]
  )

  const parentOptions = useMemo(
    () => [
      { value: rootDepartment, label: t("topDepartment") },
      ...departments
        .filter(
          (candidate) =>
            (system ||
              Boolean(operatingPlatformID()) ||
              candidate.entity_id === entityID) &&
            candidate.id !== editing?.id &&
            !isDescendantOf(candidate, editing?.id, departmentByID)
        )
        .map((department) => ({
          value: department.id,
          label: `${"　".repeat(department.depth)}${department.name}`,
        })),
    ],
    [departmentByID, departments, editing?.id, entityID, system, t]
  )

  const openCreate = () => {
    setEditing(null)
    setEntityID(
      entities.find((entity) => entity.status === "active")?.id ??
        entities[0]?.id ??
        ""
    )
    setParentID(rootDepartment)
    setStatus("active")
    setOpen(true)
  }

  const openEdit = (department: Department) => {
    setEditing(department)
    setEntityID(department.entity_id)
    setParentID(department.parent_id || rootDepartment)
    setStatus(department.status)
    setOpen(true)
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const values = {
      parent_id: parentID === rootDepartment ? "" : parentID,
      name: String(data.get("name")),
      status,
      sort_order: Number(data.get("sort_order") || 0),
    }
    try {
      if (editing)
        await api.updateDepartment(editing.id, {
          ...values,
          version: editing.version,
        })
      else
        await api.createDepartment({
          ...values,
          entity_id: entityID,
        })
      setOpen(false)
      setEditing(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async (department: Department) => {
    setBusy(true)
    setError("")
    try {
      await api.deleteDepartment(department.id, department.version)
      setCollapsed((current) => {
        const next = new Set(current)
        next.delete(department.id)
        return next
      })
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const toggleCollapsed = (id: string) => {
    setCollapsed((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  return (
    <>
      <PageHeader
        title={t("departmentManagement")}
        description={t(
          system
            ? "systemDepartmentsDescription"
            : operatingPlatformID()
              ? "platformDepartmentsDescription"
              : "departmentsDescription"
        )}
        action={
          allowed("dept_create") && (
            <Button
              onClick={openCreate}
              disabled={
                !system && !operatingPlatformID() && entities.length === 0
              }
            >
              <Plus />
              {t("createDepartment")}
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
      <div className="overflow-x-auto rounded-md border bg-card">
        <Table className="min-w-[840px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("department")}</TableHead>
              {!system && <TableHead>{t("entity")}</TableHead>}
              <TableHead>{t("parentDepartment")}</TableHead>
              <TableHead>{t("sortOrder")}</TableHead>
              <TableHead>{t("status")}</TableHead>
              <TableHead>{t("version")}</TableHead>
              <TableHead className="w-24 text-right">{t("actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visibleDepartments.map((department) => {
              const hasChildren = (childrenByParent.get(department.id) ?? 0) > 0
              const parent = department.parent_id
                ? departmentByID.get(department.parent_id)
                : undefined
              return (
                <TableRow
                  key={department.id}
                  data-department-id={department.id}
                >
                  <TableCell>
                    <div
                      className="flex min-w-0 items-center gap-1"
                      style={{
                        paddingInlineStart: `${department.depth * 24}px`,
                      }}
                    >
                      {hasChildren ? (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8 shrink-0"
                          onClick={() => toggleCollapsed(department.id)}
                          aria-label={`${collapsed.has(department.id) ? t("expand") : t("collapseShort")}${department.name}`}
                          title={
                            collapsed.has(department.id)
                              ? t("expandDepartments")
                              : t("collapseDepartments")
                          }
                        >
                          {collapsed.has(department.id) ? (
                            <ChevronRight />
                          ) : (
                            <ChevronDown />
                          )}
                        </Button>
                      ) : (
                        <span className="block size-8 shrink-0" />
                      )}
                      <Building2 className="size-4 shrink-0 text-primary" />
                      <strong className="truncate text-sm">
                        {department.name}
                      </strong>
                    </div>
                  </TableCell>
                  {!system && (
                    <TableCell className="text-sm">
                      {entityByID.get(department.entity_id)?.name ?? "-"}
                    </TableCell>
                  )}
                  <TableCell className="text-sm text-muted-foreground">
                    {parent?.name ?? "-"}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {department.sort_order}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        department.status === "active"
                          ? "secondary"
                          : "destructive"
                      }
                    >
                      {department.status === "active"
                        ? t("active")
                        : t("disable")}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    v{department.version}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      {allowed("dept_update") && (
                        <Button
                          variant="ghost"
                          size="icon"
                          disabled={busy}
                          onClick={() => openEdit(department)}
                          aria-label={t("editNamed", { name: department.name })}
                          title={t("editDepartment")}
                        >
                          <Pencil />
                        </Button>
                      )}
                      {allowed("dept_delete") && (
                        <Button
                          variant="ghost"
                          size="icon"
                          disabled={busy}
                          onClick={() => void remove(department)}
                          aria-label={t("deleteNamed", {
                            name: department.name,
                          })}
                          title={t("deleteEmptyDepartment")}
                        >
                          <Trash2 />
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
        {!loading && !loadError && departments.length === 0 && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            <span className="text-center">
              <Building2 className="mx-auto mb-2 size-7" />
              {t("noDepartments")}
            </span>
          </div>
        )}
      </div>

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
              {editing ? t("editDepartment") : t("createDepartment")}
            </DialogTitle>
            <DialogDescription>
              {t("departmentFormDescription")}
            </DialogDescription>
          </DialogHeader>
          <form
            key={editing?.id ?? "create"}
            id="department-form"
            className="grid gap-4"
            onSubmit={submit}
          >
            {!system && !operatingPlatformID() && (
              <div className="grid gap-2">
                <Label htmlFor="department-entity">{t("entity")}</Label>
                <SimpleSelect
                  id="department-entity"
                  options={entities.map((entity) => ({
                    value: entity.id,
                    label: entity.name,
                  }))}
                  value={entityID}
                  onValueChange={(value) => {
                    setEntityID(value)
                    setParentID(rootDepartment)
                  }}
                  disabled={editing !== null}
                />
              </div>
            )}
            <div className="grid gap-2">
              <Label htmlFor="department-name">{t("departmentName")}</Label>
              <Input
                id="department-name"
                name="name"
                defaultValue={editing?.name}
                required
                maxLength={128}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="department-parent">{t("parentDepartment")}</Label>
              <SimpleSelect
                id="department-parent"
                options={parentOptions}
                value={parentID}
                onValueChange={setParentID}
              />
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="grid gap-2">
                <Label htmlFor="department-sort-order">{t("sortOrder")}</Label>
                <Input
                  id="department-sort-order"
                  name="sort_order"
                  type="number"
                  min={0}
                  max={1000000}
                  defaultValue={editing?.sort_order ?? 0}
                  required
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="department-status">{t("status")}</Label>
                <SimpleSelect
                  id="department-status"
                  options={{ active: t("active"), disabled: t("disable") }}
                  value={status}
                  onValueChange={(value) =>
                    setStatus(value as DepartmentInput["status"])
                  }
                />
              </div>
            </div>
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="department-form"
              disabled={
                busy || (!system && !operatingPlatformID() && entityID === "")
              }
            >
              {busy ? t("saving") : editing ? t("save") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function filterCollapsedDepartments(
  departments: Department[],
  collapsed: Set<string>
): Department[] {
  const byID = new Map(
    departments.map((department) => [department.id, department])
  )
  return departments.filter((department) => {
    let parentID = department.parent_id
    while (parentID) {
      if (collapsed.has(parentID)) return false
      parentID = byID.get(parentID)?.parent_id
    }
    return true
  })
}

function isDescendantOf(
  candidate: Department,
  ancestorID: string | undefined,
  departments: Map<string, Department>
): boolean {
  if (!ancestorID) return false
  let parentID = candidate.parent_id
  while (parentID) {
    if (parentID === ancestorID) return true
    parentID = departments.get(parentID)?.parent_id
  }
  return false
}
