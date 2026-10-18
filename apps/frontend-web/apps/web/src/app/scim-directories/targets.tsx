import { AutoTable } from "@workspace/ui/components/auto-table/auto-table"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ArrowUpFromLine,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  UserMinus,
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
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  ApiError,
  createIamApi,
  type SCIMTarget,
  type SCIMPushResult,
  type SCIMResourceInput,
  type ServiceAccount,
} from "../../lib/iam-api"

interface TargetDraft {
  name: string
  url: string
  token: string
  account: string
  enabled: boolean
}
const emptyDraft: TargetDraft = {
  name: "",
  url: "",
  token: "",
  account: "",
  enabled: true,
}

export function SCIMTargets({ tenant }: { tenant: string }) {
  const t = useTranslations("iam")

  const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])
  const [targets, setTargets] = useState<SCIMTarget[]>([])
  const [loading, setLoading] = useState(true)
  const [available, setAvailable] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [editing, setEditing] = useState<SCIMTarget | null | undefined>()
  const [draft, setDraft] = useState<TargetDraft>({ ...emptyDraft })
  const [conflict, setConflict] = useState(false)
  const [deleting, setDeleting] = useState<SCIMTarget | null>(null)
  const [operation, setOperation] = useState<{
    target: SCIMTarget
    remove: boolean
  } | null>(null)
  const [resource, setResource] = useState<SCIMResourceInput>({
    resource_type: "User",
    resource_id: "",
  })
  const [result, setResult] = useState<SCIMPushResult | null>(null)
  const [accounts, setAccounts] = useState<ServiceAccount[]>([])
  const [accountError, setAccountError] = useState("")
  const [choices, setChoices] = useState<{ id: string; name: string }[]>([])
  const [choiceError, setChoiceError] = useState("")

  const refresh = useCallback(async () => {
    try {
      setTargets(await api.scimTargets())
      setAvailable(true)
    } catch (cause) {
      setTargets([])
      setAvailable(false)
      throw cause
    }
  }, [api])
  useEffect(() => {
    let active = true
    void api
      .scimTargets()
      .then(
        (items) => {
          if (active) {
            setTargets(items)
            setAvailable(true)
          }
        },
        (cause: Error) => active && setError(cause.message)
      )
      .finally(() => active && setLoading(false))
    return () => {
      active = false
    }
  }, [api])
  useEffect(() => {
    if (editing === undefined) return
    let active = true
    void api.serviceAccounts(draft.account).then(
      (data) => {
        if (active) {
          setAccounts(data.items)
          setAccountError("")
        }
      },
      (cause: Error) => {
        if (active) {
          setAccounts([])
          setAccountError(cause.message)
        }
      }
    )
    return () => {
      active = false
    }
  }, [api, editing, draft.account])
  useEffect(() => {
    if (!operation) return
    let active = true
    const load =
      resource.resource_type === "User"
        ? api.principals().then((data) =>
            data.items.map((item) => ({
              id: item.id,
              name: item.display_name || item.login_name || item.id,
            }))
          )
        : api
            .groups()
            .then((data) =>
              data.map((item) => ({ id: item.id, name: item.name }))
            )
    void load.then(
      (items) => active && setChoices(items),
      (cause: Error) => active && setChoiceError(cause.message)
    )
    return () => {
      active = false
    }
  }, [api, operation, resource.resource_type])

  const edit = (target: SCIMTarget | null) => {
    setError("")
    setAccountError("")
    setAccounts([])
    setConflict(false)
    setDraft(
      target
        ? {
            name: target.name,
            url: target.base_url,
            token: "",
            account: target.service_account_id ?? "",
            enabled: target.status === "active",
          }
        : { ...emptyDraft }
    )
    setEditing(target)
  }
  const closeEditor = () => {
    setEditing(undefined)
    setDraft({ ...emptyDraft })
    setAccounts([])
    setConflict(false)
  }
  const reload = async () => {
    if (!editing) return
    setBusy(true)
    setError("")
    try {
      const items = await api.scimTargets()
      setTargets(items)
      const current = items.find((item) => item.id === editing.id)
      if (current) edit(current)
      else closeEditor()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const save = async () => {
    if (conflict) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const body = {
        name: draft.name.trim(),
        base_url: draft.url.trim(),
        service_account_id: draft.account.trim() || null,
      }
      if (editing) {
        await api.updateSCIMTarget(editing.id, {
          ...body,
          status: draft.enabled ? "active" : "disabled",
          version: editing.version,
          ...(draft.token ? { bearer_token: draft.token } : {}),
        })
      } else {
        await api.createSCIMTarget({ ...body, bearer_token: draft.token })
      }
      setNotice(editing ? t("outboundUpdated") : t("outboundCreated"))
      closeEditor()
      await refresh()
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409 && editing)
        setConflict(true)
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const removeTarget = async () => {
    if (!deleting) return
    setBusy(true)
    setError("")
    try {
      await api.deleteSCIMTarget(deleting.id)
      setDeleting(null)
      setNotice(t("outboundDeleted"))
      setResult(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  // Retiring removes the remote accounts before the mapping and token that
  // reach them. A remote that refuses leaves everything in place, so the
  // operator can come back to it rather than losing track of what is out there.
  const retireTarget = async () => {
    if (!deleting) return
    setBusy(true)
    setError("")
    try {
      const outcome = await api.retireSCIMTarget(deleting.id)
      setDeleting(null)
      setNotice(t("deprovisionedCount", { count: outcome.deprovisioned }))
      setResult(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const openOperation = (target: SCIMTarget, remove: boolean) => {
    setError("")
    setChoices([])
    setChoiceError("")
    setResult(null)
    setResource({ resource_type: "User", resource_id: "" })
    setOperation({ target, remove })
  }
  const runOperation = async () => {
    if (!operation) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const body = { ...resource, resource_id: resource.resource_id.trim() }
      if (operation.remove) {
        await api.deprovisionSCIMResource(operation.target.id, body)
        setNotice(t("remoteRemoved"))
      } else {
        setResult(await api.pushSCIMResource(operation.target.id, body))
        setNotice(t("resourcePushed"))
      }
      setOperation(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader title={t("scimOutboundTargets")} />
      {error && editing === undefined && !operation && !deleting && (
        <Alert>{error}</Alert>
      )}
      {notice && (
        <div role="status" className="mb-4 text-sm">
          {notice}
          {result && (
            <span className="ml-2 font-mono text-xs break-all">
              {t("remoteId")}
              {result.remote_id || result.external_id}
            </span>
          )}
        </div>
      )}
      <AutoTable
        className="max-h-[min(65svh,44rem)]"
        toolbar={
          <Button disabled={!available || busy} onClick={() => edit(null)}>
            <Plus />
            {t("createTarget")}
          </Button>
        }
        onRefresh={async () => {
          setError("")
          setLoading(true)
          try {
            await refresh()
          } catch (cause) {
            setError((cause as Error).message)
          } finally {
            setLoading(false)
          }
        }}
        loading={loading}
        searchable

        emptyContent={
          available ? t("noOutboundTargets") : t("outboundUnavailable")
        }
        data={targets}
        rowKey="id"
        rowProps={(target) => ({ "data-target-id": target.id })}
        columns={[
          {
            field: "name",
            name: <>{t("target")}</>,
            render: (_, target) => (
              <>
                <strong className="block text-sm">{target.name}</strong>
                <span className="block max-w-lg font-mono text-xs break-all text-muted-foreground">
                  {target.base_url}
                </span>
              </>
            ),
          },
          {
            field: "service_account_id",
            name: <>{t("serviceAccounts")}</>,
            render: (_, target) => (
              <>{target.service_account_id ?? t("unbound")}</>
            ),
          },
          {
            field: "status",
            name: <>{t("status")}</>,
            render: (_, target) => (
              <>
                <Badge
                  variant={target.status === "active" ? "secondary" : "outline"}
                >
                  {target.status === "active" ? t("active") : t("disable")}
                </Badge>
              </>
            ),
          },
          {
            field: "version",
            name: <>{t("version")}</>,
            render: (_, target) => <>{target.version}</>,
          },
          {
            field: "actions",
            type: "actions",
            name: <>{t("actions")}</>,
            render: (_, target) => (
              <>
                <div className="flex justify-end gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={busy}
                    title={t("editTarget")}
                    aria-label={t("editNamedSpaced", { name: target.name })}
                    onClick={() => edit(target)}
                  >
                    <Pencil />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={
                      busy ||
                      target.status !== "active" ||
                      !target.service_account_id
                    }
                    title={t("pushResource")}
                    aria-label={t("pushToNamed", { name: target.name })}
                    onClick={() => openOperation(target, false)}
                  >
                    <ArrowUpFromLine />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={
                      busy ||
                      target.status !== "active" ||
                      !target.service_account_id
                    }
                    title={t("removeRemote")}
                    aria-label={t("removeFromNamed", { name: target.name })}
                    onClick={() => openOperation(target, true)}
                  >
                    <UserMinus />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={busy}
                    title={t("deleteOutbound")}
                    aria-label={t("deleteNamedSpaced", { name: target.name })}
                    onClick={() => {
                      setError("")
                      setDeleting(target)
                    }}
                  >
                    <Trash2 />
                  </Button>
                </div>
              </>
            ),
          },
        ]}
      />

      <Dialog
        open={editing !== undefined}
        onOpenChange={(open) => {
          if (!open && !busy) closeEditor()
        }}
      >
        <DialogContent className="grid max-h-[calc(100svh-2rem)] gap-5 overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {editing ? t("editOutboundTarget") : t("createOutboundTarget")}
            </DialogTitle>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <AutoForm
            key={`${editing?.id ?? "new"}:${editing?.version ?? 0}`}
            id="scim-target-form"
            mode="edit"
            initialData={{ ...draft }}
            fields={{
              name: {
                name: t("name"),
                type: "text",
                required: true,
                disabled: busy || conflict,
                col: 2,
                config: { id: "scim-target-name", maxLength: 128 },
              },
              url: {
                name: t("scimBaseUrl"),
                type: "text",
                required: true,
                disabled: busy || conflict,
                col: 2,
                config: { id: "scim-target-url", type: "url", maxLength: 1024 },
              },
              token: {
                name: editing ? t("optionalBearer") : t("bearerToken"),
                type: "password",
                required: !editing,
                disabled: busy || conflict,
                col: 2,
                config: {
                  id: "scim-target-token",
                  type: "password",
                  maxLength: 4096,
                  autoComplete: "new-password",
                },
              },
              account: {
                name: t("serviceAccounts"),
                type: "text",
                required: false,
                disabled: busy || conflict,
                col: 2,
                config: {
                  id: "scim-target-account",
                  list: "scim-target-accounts",
                  pattern: "[1-9][0-9]*",
                  maxLength: 64,
                  autoComplete: "off",
                  placeholder: t("unbound"),
                },
              },
              enabled: {
                name: t("enableTarget"),
                type: "switch",
                show: Boolean(editing),
                disabled: busy || conflict,
                col: 2,
                config: { id: "scim-target-enabled" },
              },
            }}
            onChange={(values) => {
              if (JSON.stringify(values) !== JSON.stringify(draft))
                setDraft(values as unknown as TargetDraft)
            }}
            onSubmit={() => save()}
          >
            <datalist id="scim-target-accounts">
              {accounts.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.display_name || account.login_name}
                </option>
              ))}
            </datalist>
            {accountError && <Alert>{accountError}</Alert>}
          </AutoForm>
          <DialogFooter className="gap-2">
            <Button variant="outline" disabled={busy} onClick={closeEditor}>
              {t("cancel")}
            </Button>
            {conflict ? (
              <Button disabled={busy} onClick={() => void reload()}>
                <RefreshCw />
                {t("reload")}
              </Button>
            ) : (
              <Button type="submit" form="scim-target-form" disabled={busy}>
                {busy ? t("saving") : t("save")}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(operation)}
        onOpenChange={(open) => {
          if (!open && !busy) setOperation(null)
        }}
      >
        <DialogContent className="grid max-h-[calc(100svh-2rem)] gap-5 overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {operation?.remove ? t("removeRemote") : t("pushResource")}
            </DialogTitle>
            <DialogDescription className="break-all">
              {operation?.target.name} · {operation?.target.base_url}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <AutoForm
            key={resource.resource_type}
            id="scim-resource-form"
            mode="edit"
            initialData={{ ...resource }}
            fields={{
              resource_type: {
                name: t("resourceType"),
                type: "select",
                disabled: busy,
                col: 2,
                config: {
                  id: "scim-resource-type",
                  options: [
                    { value: "User", label: t("user") },
                    { value: "Group", label: t("groups") },
                  ],
                },
              },
              resource_id: {
                name:
                  resource.resource_type === "User"
                    ? t("principalId")
                    : t("groupId"),
                type: "text",
                required: true,
                disabled: busy,
                col: 2,
                config: {
                  id: "scim-resource-id",
                  list: "scim-resource-options",
                  pattern: "[1-9][0-9]*",
                  maxLength: 64,
                  autoComplete: "off",
                },
              },
            }}
            onChange={(values) => {
              if (values.resource_type !== resource.resource_type) {
                setChoices([])
                setChoiceError("")
                setResource({
                  resource_type:
                    values.resource_type as SCIMResourceInput["resource_type"],
                  resource_id: "",
                })
              } else if (values.resource_id !== resource.resource_id)
                setResource(values as unknown as SCIMResourceInput)
            }}
            onSubmit={() => runOperation()}
          >
            <datalist id="scim-resource-options">
              {choices.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </datalist>
            {choiceError && <Alert>{choiceError}</Alert>}
          </AutoForm>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setOperation(null)}
            >
              {t("cancel")}
            </Button>
            <Button
              type="submit"
              form="scim-resource-form"
              disabled={busy}
              variant={operation?.remove ? "destructive" : "default"}
            >
              {operation?.remove ? <UserMinus /> : <ArrowUpFromLine />}
              {busy
                ? t("processing")
                : operation?.remove
                  ? t("confirmRemove")
                  : t("push")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(deleting)}
        onOpenChange={(open) => {
          if (!open && !busy) setDeleting(null)
        }}
      >
        <DialogContent className="grid gap-5">
          <DialogHeader>
            <DialogTitle>{t("deleteOutbound")}</DialogTitle>
            <DialogDescription>
              {deleting?.name} {t("outboundDeleteDescription")}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setDeleting(null)}
            >
              {t("cancel")}
            </Button>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void removeTarget()}
            >
              <Trash2 />
              {busy ? t("processing") : t("deleteConfigOnly")}
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void retireTarget()}
            >
              <Trash2 />
              {busy ? t("deprovisioning") : t("deprovisionAndDelete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
