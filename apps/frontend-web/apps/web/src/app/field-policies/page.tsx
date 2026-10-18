import type { FieldRendererProps } from "@workspace/ui/components/auto-form/types"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table/auto-table"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useMemo, useState } from "react"
import {
  LoaderCircle,
  LockKeyhole,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
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
  ApiError,
  createIamApi,
  getTenant,
  type FieldDefinition,
  type FieldPolicy,
  type ManagedFieldPolicy,
  type Role,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

const mutabilityLabels = {
  readOnly: "readOnly",
  readWrite: "readWrite",
  immutable: "createOnlyWrite",
  writeOnly: "writeOnly",
}
const defaultOptions = { allow: "defaultAllow", deny: "defaultDeny" }

export default function FieldPoliciesPage() {
  const [tenant, setTenantValue] = useState(getTenant)
  useEffect(() => {
    const sync = () => setTenantValue(getTenant())
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])
  return <TenantFieldPolicies key={tenant} tenant={tenant} />
}

function TenantFieldPolicies({ tenant }: { tenant: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()

  // Pin requests to the editor's tenant, including refreshes after mutations.
  const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])
  const [catalog, setCatalog] = useState<FieldDefinition[]>([])
  const [policies, setPolicies] = useState<ManagedFieldPolicy[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [resource, setResource] = useState("all")
  const [search, setSearch] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [roleError, setRoleError] = useState("")
  const [selected, setSelected] = useState<FieldDefinition | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      const [fields, rules] = await Promise.all([
        api.fieldCatalog(),
        api.fieldPolicies(),
      ])
      setCatalog(fields)
      setPolicies(rules)
    } catch (cause) {
      setCatalog([])
      setPolicies([])
      setError((cause as Error).message)
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    let active = true
    void Promise.all([api.fieldCatalog(), api.fieldPolicies()]).then(
      ([fields, rules]) => {
        if (!active) return
        setCatalog(fields)
        setPolicies(rules)
        setLoading(false)
      },
      (cause: Error) => {
        if (!active) return
        setError(cause.message)
        setLoading(false)
      }
    )
    void api
      .roles()
      .then((next) => {
        if (active) setRoles(next)
      })
      .catch((cause: Error) => {
        if (active) setRoleError(cause.message)
      })
    return () => {
      active = false
    }
  }, [api])

  const resources = [...new Set(catalog.map((field) => field.resource))]
  const visible = catalog.filter(
    (field) =>
      (resource === "all" || field.resource === resource) &&
      `${field.resource}.${field.name}`
        .toLowerCase()
        .includes(search.toLowerCase())
  )
  const findPolicy = (field: FieldDefinition) =>
    policies.find(
      (rule) =>
        rule.version > 0 &&
        rule.resource === field.resource &&
        rule.field === field.name
    )

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader title={t("fieldPermissions")} />
      {error && <Alert>{error}</Alert>}

      <AutoTable
        fill
        toolbar={
          <AutoForm
            mode="edit"
            className="flex-1 md:grid-cols-2"
            initialData={{ resource, search }}
            fields={{
              resource: {
                name: t("resource"),
                type: "select",
                config: {
                  id: "field-resource",
                  options: [
                    { value: "all", label: t("allResources") },
                    ...resources.map((value) => ({ value, label: value })),
                  ],
                },
              },
              search: {
                name: t("field"),
                type: "text",
                config: { id: "field-search", type: "search" },
              },
            }}
            onChange={(values) => {
              if (values.resource !== resource)
                setResource(String(values.resource))
              if (values.search !== search) setSearch(String(values.search))
            }}
          />
        }
        onRefresh={refresh}

        data={loading ? [] : visible}
        loading={loading}
        emptyContent={
          error ? t("fieldPoliciesUnavailable") : t("noMatchingFields")
        }
        rowKey={(field) => `${field.resource}.${field.name}`}
        rowProps={(field) => ({
          "data-field": `${field.resource}.${field.name}`,
        })}
        columns={[
          {
            field: "name",
            name: <>{t("field")}</>,
            render: (_, field) => {
              return (
                <>
                  <span className="text-xs text-muted-foreground">
                    {field.resource}
                  </span>
                  <div className="font-mono text-sm">{field.name}</div>
                </>
              )
            },
          },
          {
            field: "mutability",
            name: <>{t("properties")}</>,
            render: (_, field) => {
              return (
                <>
                  <Badge variant="outline">
                    {t(mutabilityLabels[field.mutability])}
                  </Badge>
                </>
              )
            },
          },
          {
            field: "read",
            name: <>{t("read")}</>,
            render: (_, field) => {
              const rule = findPolicy(field)
              return (
                <>
                  {rule
                    ? t(defaultOptions[rule.policy.read.default])
                    : field.returned === "never"
                      ? t("omit")
                      : t("declaredDefault")}
                  {rule && (
                    <div className="text-xs text-muted-foreground">
                      {t("allow")}
                      {rule.policy.read.allow_roles.length} {t("denySuffix")}{" "}
                      {rule.policy.read.deny_roles.length}
                    </div>
                  )}
                </>
              )
            },
          },
          {
            field: "write",
            name: <>{t("write")}</>,
            render: (_, field) => {
              const rule = findPolicy(field)
              return (
                <>
                  {field.mutability === "readOnly"
                    ? t("forbidden")
                    : rule
                      ? t(defaultOptions[rule.policy.write.default])
                      : t("declaredDefault")}
                  {rule && field.mutability !== "readOnly" && (
                    <div className="text-xs text-muted-foreground">
                      {t("allow")}
                      {rule.policy.write.allow_roles.length} {t("denySuffix")}{" "}
                      {rule.policy.write.deny_roles.length}
                    </div>
                  )}
                </>
              )
            },
          },
          {
            field: "mask",
            name: <>{t("whenUnreadable")}</>,
            render: (_, field) => {
              const rule = findPolicy(field)
              return (
                <>
                  {rule?.policy.mask === "redact"
                    ? t("fixedMask")
                    : t("hidden")}
                </>
              )
            },
          },
          {
            field: "version",
            name: <>{t("version")}</>,
            render: (_, field) => {
              const rule = findPolicy(field)
              return <>{rule?.version ?? "-"}</>
            },
          },
          {
            field: "actions",
            type: "actions",
            name: (
              <>
                <span className="sr-only">{t("actions")}</span>
              </>
            ),
            render: (_, field) => {
              return (
                <>
                  {/* A field nobody may configure keeps the lock. A field this
                        caller may not configure shows nothing, because calling
                        it fixed would be a different claim. */}
                  {!field.configurable ? (
                    <span
                      title={t("fixedField")}
                      className="inline-flex size-9 items-center justify-center text-muted-foreground"
                    >
                      <LockKeyhole
                        className="size-4"
                        aria-label={t("fixedField")}
                      />
                    </span>
                  ) : allowed("field_policy_update") ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("editNamedSpaced", { name: field.name })}
                      title={t("editNamedSpaced", { name: field.name })}
                      onClick={() => setSelected(field)}
                    >
                      <Pencil />
                    </Button>
                  ) : null}
                </>
              )
            },
          },
        ]}
      />
      {selected && (
        <FieldPolicyEditor
          key={`${selected.resource}.${selected.name}`}
          field={selected}
          current={findPolicy(selected)}
          roles={roles}
          roleError={roleError}
          api={api}
          onClose={() => setSelected(null)}
          onSaved={async () => {
            setSelected(null)
            await refresh()
          }}
        />
      )}
    </div>
  )
}

function FieldPolicyEditor({
  field,
  current,
  roles,
  roleError,
  api,
  onClose,
  onSaved,
}: {
  field: FieldDefinition
  current?: ManagedFieldPolicy
  roles: Role[]
  roleError: string
  api: ReturnType<typeof createIamApi>
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const t = useTranslations("iam")

  const [draft, setDraft] = useState<FieldPolicy>(() =>
    current
      ? structuredClone(current.policy)
      : {
          read: {
            default: field.returned === "never" ? "deny" : "allow",
            allow_roles: [],
            deny_roles: [],
          },
          write: {
            default: field.mutability === "readOnly" ? "deny" : "allow",
            allow_roles: [],
            deny_roles: [],
          },
          mask: "omit",
        }
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [conflict, setConflict] = useState(false)
  const [restore, setRestore] = useState(false)
  const [roleSearch, setRoleSearch] = useState("")
  const [roleID, setRoleID] = useState("")
  const [roleCatalogueRevision, setRoleCatalogueRevision] = useState(0)
  const [extraRoles, setExtraRoles] = useState<string[]>([])
  const known = new Map(roles.map((role) => [role.id, role.name]))
  for (const id of [
    ...draft.read.allow_roles,
    ...draft.read.deny_roles,
    ...draft.write.allow_roles,
    ...draft.write.deny_roles,
    ...extraRoles,
  ]) {
    if (!known.has(id)) known.set(id, id)
  }
  const visibleRoles = [...known].filter(([id, name]) =>
    `${id} ${name}`.toLowerCase().includes(roleSearch.toLowerCase())
  )
  const readOnly = field.mutability === "readOnly"
  const neverRead =
    field.returned === "never" || field.mutability === "writeOnly"

  const submit = async () => {
    if (busy || conflict) return
    setBusy(true)
    setError("")
    try {
      if (restore && current)
        await api.deleteFieldPolicy(field.resource, field.name, current.version)
      else
        await api.setFieldPolicy(
          field.resource,
          field.name,
          draft,
          current?.version ?? 0
        )
      await onSaved()
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        setConflict(true)
        setError(t("policyVersionConflict"))
      } else setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent
        className="max-h-[90svh] overflow-y-auto sm:max-w-2xl"
        aria-describedby={undefined}
      >
        <DialogHeader>
          <DialogTitle className="text-base break-all">
            {restore ? t("restoreDefaultPolicy") : t("editFieldPolicy")} ·{" "}
            {field.resource}.{field.name}
          </DialogTitle>
        </DialogHeader>
        {error && <Alert>{error}</Alert>}
        <AutoForm
          key={roleCatalogueRevision}
          className="mt-3"
          mode="edit"
          initialData={{
            readDefault: draft.read.default,
            writeDefault: draft.write.default,
            mask: draft.mask,
            roleRules: draft,
            roleSearch,
            roleID,
          }}
          fields={{
            readDefault: {
              name: t("read"),
              type: "select",
              show: !restore,
              disabled: neverRead || busy || conflict,
              config: {
                id: "field-read",
                options: { allow: t("defaultAllow"), deny: t("defaultDeny") },
              },
            },
            writeDefault: {
              name: t("write"),
              type: "select",
              show: !restore,
              disabled: readOnly || busy || conflict,
              config: {
                id: "field-write",
                options: { allow: t("defaultAllow"), deny: t("defaultDeny") },
              },
            },
            mask: {
              name: t("whenUnreadable"),
              type: "select",
              show: !restore,
              disabled: busy || conflict,
              config: {
                id: "field-mask",
                options:
                  field.maskable && !neverRead
                    ? { omit: t("hidden"), redact: t("fixedMask") }
                    : { omit: t("hidden") },
              },
            },
            roleSearch: {
              name: t("role"),
              type: "text",
              show: !restore,
              disabled: busy || conflict,
              config: { id: "field-role-search", type: "search" },
            },
            roleID: {
              name: t("roleId"),
              type: "text",
              show: !restore,
              disabled: busy || conflict,
              config: { id: "field-role-id", inputMode: "numeric" },
            },
            roleRules: {
              name: t("role"),
              type: "custom",
              value: draft,
              show: !restore,
              disabled: busy || conflict,
              col: 2,
              component: FieldPolicyRoleMatrix,
              config: {
                visibleRoles,
                busy,
                conflict,
                neverRead,
                readOnly,
                setError,
              },
            },
          }}
          onChange={(values) => {
            if (values.roleID !== roleID) setRoleID(String(values.roleID))
            const rules = values.roleRules as FieldPolicy
            const next = {
              ...rules,
              read: {
                ...rules.read,
                default: values.readDefault as "allow" | "deny",
              },
              write: {
                ...rules.write,
                default: values.writeDefault as "allow" | "deny",
              },
              mask: values.mask as FieldPolicy["mask"],
            }
            if (JSON.stringify(next) !== JSON.stringify(draft)) setDraft(next)
            if (values.roleSearch !== roleSearch)
              setRoleSearch(String(values.roleSearch))
          }}
          onSubmit={() => submit()}
        >
          {restore && (
            <p className="mb-6 text-sm">{t("restoreFieldPolicyConfirm")}</p>
          )}{" "}
          {!restore && (
            <div className="flex items-end gap-2">
              <Button
                type="button"
                variant="outline"
                size="icon"
                aria-label={t("addRoleId")}
                title={t("addRoleId")}
                disabled={
                  busy ||
                  conflict ||
                  !/^[1-9][0-9]{0,18}$/.test(roleID) ||
                  BigInt(roleID || "0") > 9223372036854775807n
                }
                onClick={() => {
                  setExtraRoles([...new Set([...extraRoles, roleID])])
                  setRoleID("")
                  setRoleSearch("")
                  setRoleCatalogueRevision((value) => value + 1)
                }}
              >
                <Plus />
              </Button>
            </div>
          )}
          {roleError && (
            <Alert>
              {t("roleCatalogUnavailable")}
              {roleError}
            </Alert>
          )}
          <div className="md:col-span-2">
            {" "}
            <DialogFooter className="mt-6 flex-wrap gap-2">
              {!restore && current && !conflict && (
                <Button
                  type="button"
                  variant="outline"
                  disabled={busy}
                  onClick={() => setRestore(true)}
                >
                  <RotateCcw />
                  {t("restoreDefault")}
                </Button>
              )}
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() =>
                  restore && !conflict ? setRestore(false) : onClose()
                }
              >
                {t("cancel")}
              </Button>
              {conflict ? (
                <Button type="button" onClick={() => void onSaved()}>
                  <RefreshCw />
                  {t("reload")}
                </Button>
              ) : (
                <Button type="submit" disabled={busy}>
                  {busy ? (
                    <LoaderCircle className="animate-spin" />
                  ) : restore ? (
                    <RotateCcw />
                  ) : (
                    <Save />
                  )}
                  {restore ? t("confirmRestore") : t("save")}
                </Button>
              )}
            </DialogFooter>
          </div>
        </AutoForm>
      </DialogContent>
    </Dialog>
  )
}

function FieldPolicyRoleMatrix({
  value,
  onChange,
  config,
}: FieldRendererProps) {
  const t = useTranslations("iam")
  const { visibleRoles, busy, conflict, neverRead, readOnly, setError } =
    config as {
      visibleRoles: [string, string][]
      busy: boolean
      conflict: boolean
      neverRead: boolean
      readOnly: boolean
      setError: (value: string) => void
    }
  return (
    <>
      <div className="max-h-60 overflow-auto">
        <Table className="min-w-72 table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead rowSpan={2} className="w-[40%]">
                {t("role")}
              </TableHead>
              <TableHead colSpan={2} className="text-center">
                {t("read")}
              </TableHead>
              <TableHead colSpan={2} className="text-center">
                {t("write")}
              </TableHead>
            </TableRow>
            <TableRow>
              {["read-allow", "read-deny", "write-allow", "write-deny"].map(
                (key) => (
                  <TableHead
                    key={key}
                    className="px-1 text-center whitespace-nowrap"
                  >
                    {key.endsWith("allow") ? t("allow") : t("deny")}
                  </TableHead>
                )
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {visibleRoles.map(([id, name]) => (
              <TableRow key={id}>
                <TableCell className="max-w-48 break-all">
                  {name}
                  {name !== id && (
                    <div className="font-mono text-xs text-muted-foreground">
                      {id}
                    </div>
                  )}
                </TableCell>
                {(["read", "write"] as const).flatMap((side) =>
                  (["allow_roles", "deny_roles"] as const).map((effect) => (
                    <TableCell
                      key={`${side}.${effect}`}
                      className="px-1 text-center"
                    >
                      <Checkbox
                        className="inline-flex size-5 items-center justify-center align-middle"
                        aria-label={`${name} ${side === "read" ? t("read") : t("write")}${effect === "allow_roles" ? t("allow") : t("deny")}`}
                        checked={(value as FieldPolicy)[side][effect].includes(
                          id
                        )}
                        disabled={
                          busy ||
                          conflict ||
                          (side === "read" ? neverRead : readOnly)
                        }
                        onCheckedChange={(checked) =>
                          (() => {
                            const policy = value as FieldPolicy
                            const selected = policy[side][effect]
                            if (checked && selected.length >= 64) {
                              setError(t("maxRolesPerRule"))
                              return
                            }
                            onChange({
                              ...policy,
                              [side]: {
                                ...policy[side],
                                [effect]: checked
                                  ? [...selected, id]
                                  : selected.filter((item) => item !== id),
                              },
                            })
                          })()
                        }
                      />
                    </TableCell>
                  ))
                )}
              </TableRow>
            ))}
            {visibleRoles.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="text-center text-muted-foreground"
                >
                  {t("noMatchingRoles")}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </>
  )
}
