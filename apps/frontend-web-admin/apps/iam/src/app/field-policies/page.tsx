import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
} from "react"
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
  ApiError,
  createIamApi,
  getTenant,
  type FieldDefinition,
  type FieldPolicy,
  type ManagedFieldPolicy,
  type Role,
} from "../../lib/iam-api"

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
    <>
      <PageHeader
        title={t("fieldPermissions")}
        action={
          <Button
            variant="outline"
            size="icon"
            aria-label={t("refreshFieldPolicies")}
            title={t("refreshFieldPolicies")}
            disabled={loading}
            onClick={() => void refresh()}
          >
            <RefreshCw className={loading ? "animate-spin" : ""} />
          </Button>
        }
      />
      {error && <Alert>{error}</Alert>}
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="grid w-48 gap-2">
          <Label htmlFor="field-resource">{t("resource")}</Label>
          <SimpleSelect
            id="field-resource"
            value={resource}
            onValueChange={setResource}
            options={[
              { value: "all", label: t("allResources") },
              ...resources.map((value) => ({ value, label: value })),
            ]}
          />
        </div>
        <div className="grid min-w-0 flex-1 gap-2">
          <Label htmlFor="field-search">{t("field")}</Label>
          <Input
            id="field-search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            type="search"
          />
        </div>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("field")}</TableHead>
            <TableHead>{t("properties")}</TableHead>
            <TableHead>{t("read")}</TableHead>
            <TableHead>{t("write")}</TableHead>
            <TableHead>{t("whenUnreadable")}</TableHead>
            <TableHead>{t("version")}</TableHead>
            <TableHead className="w-14">
              <span className="sr-only">{t("actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableRow>
              <TableCell colSpan={7}>
                <div
                  className="flex h-24 items-center justify-center gap-2"
                  role="status"
                >
                  <LoaderCircle className="size-4 animate-spin" />
                  {t("loadingShort")}
                </div>
              </TableCell>
            </TableRow>
          ) : (
            visible.map((field) => {
              const rule = findPolicy(field)
              return (
                <TableRow
                  key={`${field.resource}.${field.name}`}
                  data-field={`${field.resource}.${field.name}`}
                >
                  <TableCell>
                    <span className="text-xs text-muted-foreground">
                      {field.resource}
                    </span>
                    <div className="font-mono text-sm">{field.name}</div>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">
                      {t(mutabilityLabels[field.mutability])}
                    </Badge>
                  </TableCell>
                  <TableCell>
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
                  </TableCell>
                  <TableCell>
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
                  </TableCell>
                  <TableCell>
                    {rule?.policy.mask === "redact"
                      ? t("fixedMask")
                      : t("hidden")}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {rule?.version ?? "-"}
                  </TableCell>
                  <TableCell>
                    {field.configurable ? (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("editNamedSpaced", { name: field.name })}
                        title={t("editNamedSpaced", { name: field.name })}
                        onClick={() => setSelected(field)}
                      >
                        <Pencil />
                      </Button>
                    ) : (
                      <span
                        title={t("fixedField")}
                        className="inline-flex size-9 items-center justify-center text-muted-foreground"
                      >
                        <LockKeyhole
                          className="size-4"
                          aria-label={t("fixedField")}
                        />
                      </span>
                    )}
                  </TableCell>
                </TableRow>
              )
            })
          )}
          {!loading && visible.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={7}
                className="h-24 text-center text-muted-foreground"
              >
                {error ? t("fieldPoliciesUnavailable") : t("noMatchingFields")}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
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
    </>
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

  const toggleRole = (
    side: "read" | "write",
    effect: "allow_roles" | "deny_roles",
    id: string,
    checked: boolean
  ) => {
    const selected = draft[side][effect]
    if (checked && selected.length >= 64) {
      setError(t("maxRolesPerRule"))
      return
    }
    setDraft({
      ...draft,
      [side]: {
        ...draft[side],
        [effect]: checked
          ? [...selected, id]
          : selected.filter((value) => value !== id),
      },
    })
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
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
        <form className="mt-3" onSubmit={(event) => void submit(event)}>
          {restore ? (
            <p className="mb-6 text-sm">{t("restoreFieldPolicyConfirm")}</p>
          ) : (
            <fieldset
              disabled={busy || conflict}
              className="grid min-w-0 gap-4"
            >
              <div className="grid gap-4 sm:grid-cols-3">
                <div className="grid gap-2">
                  <Label htmlFor="field-read">{t("read")}</Label>
                  <SimpleSelect
                    id="field-read"
                    options={{
                      allow: t("defaultAllow"),
                      deny: t("defaultDeny"),
                    }}
                    value={draft.read.default}
                    disabled={neverRead || busy || conflict}
                    onValueChange={(value) =>
                      setDraft({
                        ...draft,
                        read: {
                          ...draft.read,
                          default: value as "allow" | "deny",
                        },
                      })
                    }
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="field-write">{t("write")}</Label>
                  <SimpleSelect
                    id="field-write"
                    options={{
                      allow: t("defaultAllow"),
                      deny: t("defaultDeny"),
                    }}
                    value={draft.write.default}
                    disabled={readOnly || busy || conflict}
                    onValueChange={(value) =>
                      setDraft({
                        ...draft,
                        write: {
                          ...draft.write,
                          default: value as "allow" | "deny",
                        },
                      })
                    }
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="field-mask">{t("whenUnreadable")}</Label>
                  <SimpleSelect
                    id="field-mask"
                    options={
                      field.maskable && !neverRead
                        ? { omit: t("hidden"), redact: t("fixedMask") }
                        : { omit: t("hidden") }
                    }
                    value={draft.mask}
                    disabled={busy || conflict}
                    onValueChange={(value) =>
                      setDraft({ ...draft, mask: value as "omit" | "redact" })
                    }
                  />
                </div>
              </div>
              {roleError && (
                <Alert>
                  {t("roleCatalogUnavailable")}
                  {roleError}
                </Alert>
              )}
              <div className="grid gap-2">
                <Label htmlFor="field-role-search">{t("role")}</Label>
                <Input
                  id="field-role-search"
                  type="search"
                  value={roleSearch}
                  onChange={(event) => setRoleSearch(event.target.value)}
                />
              </div>
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
                      {[
                        "read-allow",
                        "read-deny",
                        "write-allow",
                        "write-deny",
                      ].map((key) => (
                        <TableHead
                          key={key}
                          className="px-1 text-center whitespace-nowrap"
                        >
                          {key.endsWith("allow") ? t("allow") : t("deny")}
                        </TableHead>
                      ))}
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
                          (["allow_roles", "deny_roles"] as const).map(
                            (effect) => (
                              <TableCell
                                key={`${side}.${effect}`}
                                className="px-1 text-center"
                              >
                                <Checkbox
                                  className="inline-flex size-5 items-center justify-center align-middle"
                                  aria-label={`${name} ${side === "read" ? t("read") : t("write")}${effect === "allow_roles" ? t("allow") : t("deny")}`}
                                  checked={draft[side][effect].includes(id)}
                                  disabled={
                                    busy ||
                                    conflict ||
                                    (side === "read" ? neverRead : readOnly)
                                  }
                                  onCheckedChange={(checked) =>
                                    toggleRole(
                                      side,
                                      effect,
                                      id,
                                      checked === true
                                    )
                                  }
                                />
                              </TableCell>
                            )
                          )
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
              <div className="flex items-end gap-2">
                <div className="grid min-w-0 flex-1 gap-2">
                  <Label htmlFor="field-role-id">{t("roleId")}</Label>
                  <Input
                    id="field-role-id"
                    inputMode="numeric"
                    value={roleID}
                    onChange={(event) => setRoleID(event.target.value)}
                  />
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  aria-label={t("addRoleId")}
                  title={t("addRoleId")}
                  disabled={
                    !/^[1-9][0-9]{0,18}$/.test(roleID) ||
                    BigInt(roleID || "0") > 9223372036854775807n
                  }
                  onClick={() => {
                    setExtraRoles([...new Set([...extraRoles, roleID])])
                    setRoleID("")
                    setRoleSearch("")
                  }}
                >
                  <Plus />
                </Button>
              </div>
            </fieldset>
          )}
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
        </form>
      </DialogContent>
    </Dialog>
  )
}
