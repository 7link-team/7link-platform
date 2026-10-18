import type { FieldRendererProps } from "@workspace/ui/components/auto-form/types"
import { AutoTable } from "@workspace/ui/components/auto-table/auto-table"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState } from "react"
import {
  AppWindow,
  CheckCircle2,
  Copy,
  Download,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button, buttonVariants } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Label } from "@workspace/ui/components/label"
import { Switch } from "@workspace/ui/components/switch"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@workspace/ui/components/tooltip"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  iamApi,
  type OAuthClient,
  type OAuthClientInput,
  type OAuthGrantType,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

const grants: Array<{ value: OAuthGrantType; labelKey: string }> = [
  { value: "authorization_code", labelKey: "grantAuthorizationCode" },
  { value: "refresh_token", labelKey: "grantRefreshToken" },
  { value: "client_credentials", labelKey: "grantClientCredentials" },
]

interface ClientDraft {
  name: string
  redirectUris: string
  grantTypes: OAuthGrantType[]
  scopes: string
  publicClient: boolean
  status: OAuthClient["status"]
}

interface SecretResult {
  clientID: string
  secret: string
}

type Confirmation =
  | { action: "rotate"; client: OAuthClient }
  | { action: "delete"; client: OAuthClient }
  | null

const emptyDraft: ClientDraft = {
  name: "",
  redirectUris: "",
  grantTypes: ["authorization_code", "refresh_token"],
  scopes: "openid profile email",
  publicClient: false,
  status: "active",
}

function draftFrom(client: OAuthClient): ClientDraft {
  return {
    name: client.name,
    redirectUris: client.redirect_uris.join("\n"),
    grantTypes: [...client.grant_types],
    scopes: client.scopes.join(" "),
    publicClient: client.public_client,
    status: client.status,
  }
}

function uniqueWords(value: string): string[] {
  return [
    ...new Set(
      value
        .split(/\s+/)
        .map((item) => item.trim())
        .filter(Boolean)
    ),
  ]
}

function uniqueLines(value: string): string[] {
  return [
    ...new Set(
      value
        .split(/\r?\n/)
        .map((item) => item.trim())
        .filter(Boolean)
    ),
  ]
}

export default function OAuthClientsPage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()

  const [clients, setClients] = useState<OAuthClient[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [loadError, setLoadError] = useState("")
  const [notice, setNotice] = useState("")
  const [editing, setEditing] = useState<OAuthClient | null | undefined>()
  const [draft, setDraft] = useState<ClientDraft>(emptyDraft)
  const [confirmation, setConfirmation] = useState<Confirmation>(null)
  const [secret, setSecret] = useState<SecretResult | null>(null)

  const refresh = useCallback(async () => {
    try {
      setClients(await iamApi.oauthClients())
      setLoadError("")
    } catch (cause) {
      setClients([])
      setLoadError((cause as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let active = true
    void iamApi
      .oauthClients()
      .then(
        (next) => {
          if (active) setClients(next)
        },
        (cause: Error) => {
          if (active) setLoadError(cause.message)
        }
      )
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  const openCreate = () => {
    setError("")
    setDraft({ ...emptyDraft, grantTypes: [...emptyDraft.grantTypes] })
    setEditing(null)
  }

  const openEdit = (client: OAuthClient) => {
    setError("")
    setDraft(draftFrom(client))
    setEditing(client)
  }

  const closeEditor = (open: boolean) => {
    if (!open && !busy) setEditing(undefined)
  }

  const save = async () => {
    const body: OAuthClientInput = {
      name: draft.name.trim(),
      redirect_uris: uniqueLines(draft.redirectUris),
      grant_types: draft.grantTypes,
      scopes: uniqueWords(draft.scopes),
      public_client: draft.publicClient,
    }
    if (body.grant_types.length === 0 || body.scopes.length === 0) {
      setError(t("grantAndScopeRequired"))
      return
    }
    if (
      body.grant_types.includes("authorization_code") &&
      body.redirect_uris.length === 0
    ) {
      setError(t("redirectRequired"))
      return
    }
    setBusy(true)
    setError("")
    setNotice("")
    try {
      if (editing) {
        await iamApi.updateOAuthClient(editing.id, {
          ...body,
          status: draft.status,
        })
        setNotice(t("oauthUpdated"))
      } else {
        const result = await iamApi.createOAuthClient(body)
        if (result.client_secret) {
          setSecret({
            clientID: result.client.id,
            secret: result.client_secret,
          })
        } else {
          setNotice(t("publicOAuthCreated"))
        }
      }
      setEditing(undefined)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const confirm = async () => {
    if (!confirmation) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      if (confirmation.action === "rotate") {
        const result = await iamApi.rotateOAuthClientSecret(
          confirmation.client.id
        )
        setSecret({
          clientID: confirmation.client.id,
          secret: result.client_secret,
        })
      } else {
        await iamApi.deleteOAuthClient(confirmation.client.id)
        setNotice(t("oauthDeleted"))
        await refresh()
      }
      setConfirmation(null)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader
        title={t("oauthClients")}
        description={t("oauthDescription")}
      />
      {error && <Alert>{error}</Alert>}
      {loadError && !loading && (
        <Alert>
          <p>{t.has(loadError) ? t(loadError) : loadError}</p>
          <Button
            variant="outline"
            onClick={() => {
              setLoading(true)
              void refresh()
            }}
          >
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {notice && <SuccessNotice>{notice}</SuccessNotice>}

      <AutoTable
        fill
        toolbar={
          <>
            {allowed("oauth_client_create") && (
              <Button onClick={openCreate}>
                <Plus />
                {t("createClient")}
              </Button>
            )}
          </>
        }
        onRefresh={refresh}
        searchable

        emptyContent={loadError ? null : <EmptyClients />}
        loading={loading}
        data={loading || loadError ? [] : clients}
        rowKey="id"
        columns={[
          {
            field: "name",
            name: <>{t("client")}</>,
            render: (_, client) => (
              <>
                <strong className="block text-sm">{client.name}</strong>
                <code className="mt-1 block max-w-72 truncate font-mono text-xs text-muted-foreground">
                  {client.id}
                </code>
              </>
            ),
          },
          {
            field: "public_client",
            name: <>{t("type")}</>,
            render: (_, client) => (
              <>
                {client.public_client
                  ? t("publicClient")
                  : t("confidentialClient")}
              </>
            ),
          },
          {
            field: "grant_types",
            name: <>{t("grantTypes")}</>,
            render: (_, client) => (
              <>
                <GrantBadges client={client} />
              </>
            ),
          },
          {
            field: "scopes",
            name: <>{t("scopes")}</>,
            render: (_, client) => (
              <>
                <span className="block max-w-72 truncate text-xs text-muted-foreground">
                  {client.scopes.join(" ")}
                </span>
              </>
            ),
          },
          {
            field: "status",
            name: <>{t("status")}</>,
            render: (_, client) => (
              <>
                <StatusBadge status={client.status} />
              </>
            ),
          },
          {
            field: "actions",
            type: "actions",
            name: <>{t("actions")}</>,
            render: (_, client) => (
              <>
                <ClientActions
                  client={client}
                  busy={busy}
                  onEdit={() => openEdit(client)}
                  onRotate={() => setConfirmation({ action: "rotate", client })}
                  onDelete={() => setConfirmation({ action: "delete", client })}
                />
              </>
            ),
          },
        ]}
      />

      <ClientEditor
        open={editing !== undefined}
        client={editing ?? null}
        draft={draft}
        busy={busy}
        onDraft={setDraft}
        onOpenChange={closeEditor}
        onSubmit={save}
      />
      <ConfirmationDialog
        value={confirmation}
        busy={busy}
        onOpenChange={(open) => !open && !busy && setConfirmation(null)}
        onConfirm={() => void confirm()}
      />
      <SecretDialog value={secret} onClose={() => setSecret(null)} />
    </div>
  )
}

function ClientActions({
  client,
  busy,
  onEdit,
  onRotate,
  onDelete,
}: {
  client: OAuthClient
  busy: boolean
  onEdit: () => void
  onRotate: () => void
  onDelete: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()

  return (
    <TooltipProvider>
      <div className="flex justify-end gap-1">
        {allowed("oauth_client_update") && (
          <ActionButton
            label={t("editNamedSpaced", { name: client.name })}
            onClick={onEdit}
            disabled={busy}
          >
            <Pencil />
          </ActionButton>
        )}
        {!client.public_client && allowed("oauth_client_update") && (
          <ActionButton
            label={t("rotateSecretNamed", { name: client.name })}
            onClick={onRotate}
            disabled={busy}
          >
            <RefreshCw />
          </ActionButton>
        )}
        {allowed("oauth_client_delete") && (
          <ActionButton
            label={t("deleteNamedSpaced", { name: client.name })}
            onClick={onDelete}
            disabled={busy}
            destructive
          >
            <Trash2 />
          </ActionButton>
        )}
      </div>
    </TooltipProvider>
  )
}

function GrantBadges({ client }: { client: OAuthClient }) {
  return (
    <div className="flex flex-wrap gap-1">
      {client.grant_types.map((grant) => (
        <Badge key={grant} variant="outline">
          {grant}
        </Badge>
      ))}
    </div>
  )
}

function StatusBadge({ status }: { status: OAuthClient["status"] }) {
  const t = useTranslations("iam")

  return (
    <Badge
      variant={status === "active" ? "default" : "outline"}
      className="shrink-0 whitespace-nowrap"
    >
      {status === "active" ? t("active") : t("disable")}
    </Badge>
  )
}

function EmptyClients() {
  const t = useTranslations("iam")

  return (
    <div>
      <AppWindow className="mx-auto mb-2 size-7 text-muted-foreground" />
      <p className="text-sm font-medium">{t("noOAuthClients")}</p>
    </div>
  )
}

function ActionButton({
  label,
  destructive = false,
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  label: string
  destructive?: boolean
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        {...props}
        aria-label={label}
        className={buttonVariants({
          size: "icon",
          variant: "ghost",
          className: destructive ? "size-10 text-destructive" : "size-10",
        })}
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

function ClientEditor({
  open,
  client,
  draft,
  busy,
  onDraft,
  onOpenChange,
  onSubmit,
}: {
  open: boolean
  client: OAuthClient | null
  draft: ClientDraft
  busy: boolean
  onDraft: (value: ClientDraft) => void
  onOpenChange: (open: boolean) => void
  onSubmit: () => void
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {client ? t("editOAuthClient") : t("createOAuthClient")}
          </DialogTitle>
          <DialogDescription>{t("oauthFormDescription")}</DialogDescription>
        </DialogHeader>
        <AutoForm
          key={client?.id ?? "new"}
          id="oauth-client-form"
          mode="edit"
          initialData={{ ...draft }}
          fields={{
            name: {
              name: t("applicationName"),
              type: "text",
              required: true,
              disabled: false,
              col: 2,
              config: {
                id: "oauth-client-name",
                minLength: 1,
                maxLength: 128,
                autoFocus: true,
              },
            },
            publicClient: {
              name: t("publicClient"),
              type: "switch",
              col: 2,
              config: { id: "oauth-public-client", label: t("publicPkce") },
            },
            status: {
              name: t("enableClient"),
              type: "custom",
              show: Boolean(client),
              col: 2,
              component: ClientStatusField,
            },
            grantTypes: {
              name: t("grantTypes"),
              type: "custom",
              col: 2,
              component: GrantTypesField,
            },
            redirectUris: {
              name: t("redirectUris"),
              type: "textarea",
              required: false,
              disabled: false,
              col: 2,
              config: {
                id: "oauth-redirect-uris",
                placeholder: "https://app.example.test/oauth/callback",
                rows: 3,
                maxLength: 8192,
                suffix: t("redirectUriHint"),
              },
            },
            scopes: {
              name: t("scopes"),
              type: "text",
              required: true,
              disabled: false,
              col: 2,
              config: {
                id: "oauth-scopes",
                maxLength: 2048,
                placeholder: "openid profile email",
                suffix: t("scopeHint"),
              },
            },
          }}
          onChange={(values) => {
            const next = { ...values } as unknown as ClientDraft
            if (
              next.publicClient &&
              next.grantTypes.includes("client_credentials")
            )
              next.grantTypes = next.grantTypes.filter(
                (grant) => grant !== "client_credentials"
              )
            if (JSON.stringify(next) !== JSON.stringify(draft)) onDraft(next)
          }}
          onSubmit={() => onSubmit()}
        >
          <p className="text-xs text-muted-foreground md:col-span-2">
            {t("redirectUriHint")}
          </p>
        </AutoForm>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            {t("cancel")}
          </Button>
          <Button type="submit" form="oauth-client-form" disabled={busy}>
            {busy ? t("saving") : client ? t("saveChanges") : t("createClient")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function BinarySetting({
  id,
  label,
  description,
  checked,
  onCheckedChange,
}: {
  id: string
  label: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex min-h-16 items-center justify-between gap-3 rounded-md border px-3">
      <div>
        <Label htmlFor={id}>{label}</Label>
        <p className="mt-1 text-xs text-muted-foreground">{description}</p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  )
}

function ConfirmationDialog({
  value,
  busy,
  onOpenChange,
  onConfirm,
}: {
  value: Confirmation
  busy: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}) {
  const t = useTranslations("iam")

  const rotating = value?.action === "rotate"
  return (
    <Dialog open={value !== null} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {rotating ? t("rotateClientSecret") : t("deleteOAuthClient")}
          </DialogTitle>
          <DialogDescription>
            {rotating
              ? t("rotateSecretConfirm", {
                  name: value?.client.name ?? t("thisClient"),
                })
              : t("deleteClientConfirm", {
                  name: value?.client.name ?? t("thisClient"),
                })}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            {t("cancel")}
          </Button>
          <Button
            type="button"
            variant={rotating ? "default" : "destructive"}
            disabled={busy}
            onClick={onConfirm}
          >
            {busy
              ? t("processing")
              : rotating
                ? t("confirmRotation")
                : t("confirmDelete")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SecretDialog({
  value,
  onClose,
}: {
  value: SecretResult | null
  onClose: () => void
}) {
  const t = useTranslations("iam")

  const content = value
    ? `client_id=${value.clientID}\nclient_secret=${value.secret}\n`
    : ""
  const copy = () => void navigator.clipboard.writeText(content)
  const download = () => {
    if (!value) return
    const url = URL.createObjectURL(
      new Blob([content], { type: "text/plain;charset=utf-8" })
    )
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = `oauth-client-${value.clientID}.txt`
    anchor.click()
    URL.revokeObjectURL(url)
  }
  return (
    <Dialog open={value !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("saveClientSecret")}</DialogTitle>
          <DialogDescription>{t("secretShownOnce")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="rounded-md border bg-muted/40 p-3">
            <span className="block text-xs text-muted-foreground">
              {t("clientId")}
            </span>
            <code className="mt-1 block font-mono text-xs break-all">
              {value?.clientID}
            </code>
          </div>
          <div className="rounded-md border bg-muted/40 p-3">
            <span className="block text-xs text-muted-foreground">
              {t("clientSecret")}
            </span>
            <code className="mt-1 block font-mono text-xs break-all">
              {value?.secret}
            </code>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" onClick={copy}>
              <Copy />
              {t("copy")}
            </Button>
            <Button type="button" variant="outline" onClick={download}>
              <Download />
              {t("download")}
            </Button>
          </div>
        </div>
        <DialogFooter>
          <Button type="button" onClick={onClose}>
            <CheckCircle2 />
            {t("safelySaved")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SuccessNotice({ children }: { children: string }) {
  return (
    <div
      className="mb-4 rounded-md border border-success/30 bg-success/5 px-3 py-2 text-sm text-success"
      role="status"
    >
      {children}
    </div>
  )
}

function GrantTypesField({ value, onChange, values }: FieldRendererProps) {
  const t = useTranslations("iam")
  const selected = value as OAuthGrantType[]
  const publicClient = Boolean(values?.publicClient)
  useEffect(() => {
    if (publicClient && selected.includes("client_credentials"))
      onChange(selected.filter((grant) => grant !== "client_credentials"))
  }, [publicClient, selected, onChange])
  return (
    <fieldset className="grid gap-2">
      <legend className="mb-1 text-sm font-medium">{t("grantTypes")}</legend>
      <div className="grid gap-2 sm:grid-cols-3">
        {grants.map((grant) => {
          const disabled =
            Boolean(values?.publicClient) &&
            grant.value === "client_credentials"
          return (
            <label
              key={grant.value}
              className="flex min-h-12 items-center gap-2 rounded-md border px-3 text-sm"
            >
              <Checkbox
                checked={
                  !disabled &&
                  ((value as OAuthGrantType[]) ?? []).includes(grant.value)
                }
                disabled={disabled}
                onCheckedChange={(value) =>
                  onChange(
                    value === true
                      ? [
                          ...((values?.grantTypes as OAuthGrantType[]) ?? []),
                          grant.value,
                        ]
                      : ((values?.grantTypes as OAuthGrantType[]) ?? []).filter(
                          (item) => item !== grant.value
                        )
                  )
                }
              />
              <span className={disabled ? "text-muted-foreground" : ""}>
                {t(grant.labelKey)}
              </span>
            </label>
          )
        })}
      </div>
    </fieldset>
  )
}

function ClientStatusField({ value, onChange }: FieldRendererProps) {
  const t = useTranslations("iam")
  return (
    <BinarySetting
      id="oauth-client-status"
      label={t("enableClient")}
      description={t("disabledClientHint")}
      checked={value === "active"}
      onCheckedChange={(checked) => onChange(checked ? "active" : "disabled")}
    />
  )
}
