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
import {
  iamApi,
  type Department,
  type DepartmentInput,
} from "../../lib/iam-api"

const rootDepartment = "__root__"

export default function DepartmentsPage() {
  const t = useTranslations("iam")

  const [departments, setDepartments] = useState<Department[]>([])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Department | null>(null)
  const [parentID, setParentID] = useState(rootDepartment)
  const [status, setStatus] = useState<DepartmentInput["status"]>("active")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const loadGeneration = useRef(0)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState("")
  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoading(true)
    setLoadError("")
    try {
      const next = await iamApi.departments()
      if (generation === loadGeneration.current) setDepartments(next)
    } catch (cause) {
      if (generation === loadGeneration.current) {
        setDepartments([])
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
            candidate.id !== editing?.id &&
            !isDescendantOf(candidate, editing?.id, departmentByID)
        )
        .map((department) => ({
          value: department.id,
          label: `${"　".repeat(department.depth)}${department.name}`,
        })),
    ],
    [departmentByID, departments, editing?.id, t]
  )

  const openCreate = () => {
    setEditing(null)
    setParentID(rootDepartment)
    setStatus("active")
    setOpen(true)
  }

  const openEdit = (department: Department) => {
    setEditing(department)
    setParentID(department.parent_id || rootDepartment)
    setStatus(department.status)
    setOpen(true)
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const input: DepartmentInput = {
      parent_id: parentID === rootDepartment ? "" : parentID,
      name: String(data.get("name")),
      status,
      sort_order: Number(data.get("sort_order") || 0),
    }
    try {
      if (editing)
        await iamApi.updateDepartment(editing.id, {
          ...input,
          version: editing.version,
        })
      else await iamApi.createDepartment(input)
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
      await iamApi.deleteDepartment(department.id, department.version)
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
        description={t("departmentsDescription")}
        action={
          <Button onClick={openCreate}>
            <Plus />
            {t("createDepartment")}
          </Button>
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
        <Table className="min-w-[720px]">
          <TableHeader>
            <TableRow>
              <TableHead>{t("department")}</TableHead>
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
                <TableRow key={department.id}>
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
            <Button type="submit" form="department-form" disabled={busy}>
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
