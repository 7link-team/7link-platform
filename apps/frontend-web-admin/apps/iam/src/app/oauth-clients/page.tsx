import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState, type FormEvent } from "react"
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
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { Switch } from "@workspace/ui/components/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { Textarea } from "@workspace/ui/components/textarea"
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

const grants: Array<{ value: OAuthGrantType; label: string }> = [
  { value: "authorization_code", label: "Authorization Code + PKCE" },
  { value: "refresh_token", label: "Refresh Token" },
  { value: "client_credentials", label: "Client Credentials" },
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

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
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
    <>
      <PageHeader
        title={t("oauthClients")}
        description={t("oauthDescription")}
        action={
          <Button onClick={openCreate}>
            <Plus />
            {t("createClient")}
          </Button>
        }
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

      <section className="overflow-hidden rounded-md border bg-card">
        <header className="flex min-h-14 items-center justify-between gap-4 border-b bg-muted/20 px-4">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <AppWindow className="size-4 text-muted-foreground" />
            {t("registeredApplications")}
          </div>
          <span className="text-xs text-muted-foreground">
            {loading
              ? t("loading")
              : !loadError && t("clientCount", { count: clients.length })}
          </span>
        </header>
        <div className="hidden md:block">
          <Table containerClassName="min-h-56">
            <TableHeader>
              <TableRow>
                <TableHead className="min-w-56">{t("client")}</TableHead>
                <TableHead className="min-w-40">{t("type")}</TableHead>
                <TableHead className="min-w-64">{t("grantTypes")}</TableHead>
                <TableHead className="min-w-52">Scopes</TableHead>
                <TableHead className="w-24">{t("status")}</TableHead>
                <TableHead className="w-36 text-right">
                  {t("actions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(loading || loadError ? [] : clients).map((client) => (
                <TableRow key={client.id}>
                  <TableCell>
                    <strong className="block text-sm">{client.name}</strong>
                    <code className="mt-1 block max-w-72 truncate font-mono text-xs text-muted-foreground">
                      {client.id}
                    </code>
                  </TableCell>
                  <TableCell>
                    {client.public_client
                      ? t("publicClient")
                      : t("confidentialClient")}
                  </TableCell>
                  <TableCell>
                    <GrantBadges client={client} />
                  </TableCell>
                  <TableCell>
                    <span className="block max-w-72 truncate text-xs text-muted-foreground">
                      {client.scopes.join(" ")}
                    </span>
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={client.status} />
                  </TableCell>
                  <TableCell className="text-right">
                    <ClientActions
                      client={client}
                      busy={busy}
                      onEdit={() => openEdit(client)}
                      onRotate={() =>
                        setConfirmation({ action: "rotate", client })
                      }
                      onDelete={() =>
                        setConfirmation({ action: "delete", client })
                      }
                    />
                  </TableCell>
                </TableRow>
              ))}
              {!loading && !loadError && clients.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="h-52 text-center">
                    <EmptyClients />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className="md:hidden">
          {(loading || loadError ? [] : clients).map((client) => (
            <article
              key={client.id}
              className="space-y-3 border-b p-4 last:border-0"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <strong className="block text-sm break-words">
                    {client.name}
                  </strong>
                  <code className="mt-1 block truncate font-mono text-xs text-muted-foreground">
                    {client.id}
                  </code>
                </div>
                <StatusBadge status={client.status} />
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <Badge variant="secondary">
                  {client.public_client
                    ? t("publicClient")
                    : t("confidentialClient")}
                </Badge>
                <GrantBadges client={client} />
              </div>
              <p className="font-mono text-xs break-words text-muted-foreground">
                {client.scopes.join(" ")}
              </p>
              <ClientActions
                client={client}
                busy={busy}
                onEdit={() => openEdit(client)}
                onRotate={() => setConfirmation({ action: "rotate", client })}
                onDelete={() => setConfirmation({ action: "delete", client })}
              />
            </article>
          ))}
          {!loading && !loadError && clients.length === 0 && (
            <div className="grid h-52 place-items-center text-center">
              <EmptyClients />
            </div>
          )}
        </div>
      </section>

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
    </>
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

  return (
    <TooltipProvider>
      <div className="flex justify-end gap-1">
        <ActionButton
          label={t("editNamedSpaced", { name: client.name })}
          onClick={onEdit}
          disabled={busy}
        >
          <Pencil />
        </ActionButton>
        {!client.public_client && (
          <ActionButton
            label={t("rotateSecretNamed", { name: client.name })}
            onClick={onRotate}
            disabled={busy}
          >
            <RefreshCw />
          </ActionButton>
        )}
        <ActionButton
          label={t("deleteNamedSpaced", { name: client.name })}
          onClick={onDelete}
          disabled={busy}
          destructive
        >
          <Trash2 />
        </ActionButton>
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
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const t = useTranslations("iam")

  const update = <K extends keyof ClientDraft>(key: K, value: ClientDraft[K]) =>
    onDraft({ ...draft, [key]: value })
  const toggleGrant = (grant: OAuthGrantType, checked: boolean) =>
    update(
      "grantTypes",
      checked
        ? [...draft.grantTypes, grant]
        : draft.grantTypes.filter((value) => value !== grant)
    )
  const setPublic = (checked: boolean) =>
    onDraft({
      ...draft,
      publicClient: checked,
      grantTypes: checked
        ? draft.grantTypes.filter((grant) => grant !== "client_credentials")
        : draft.grantTypes,
    })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {client ? t("editOAuthClient") : t("createOAuthClient")}
          </DialogTitle>
          <DialogDescription>{t("oauthFormDescription")}</DialogDescription>
        </DialogHeader>
        <form id="oauth-client-form" className="grid gap-5" onSubmit={onSubmit}>
          <div className="grid gap-2">
            <Label htmlFor="oauth-client-name">{t("applicationName")}</Label>
            <Input
              id="oauth-client-name"
              value={draft.name}
              onChange={(event) => update("name", event.target.value)}
              required
              minLength={1}
              maxLength={128}
              autoFocus
            />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <BinarySetting
              id="oauth-public-client"
              label={t("publicClient")}
              description={t("publicPkce")}
              checked={draft.publicClient}
              onCheckedChange={setPublic}
            />
            {client && (
              <BinarySetting
                id="oauth-client-status"
                label={t("enableClient")}
                description={t("disabledClientHint")}
                checked={draft.status === "active"}
                onCheckedChange={(checked) =>
                  update("status", checked ? "active" : "disabled")
                }
              />
            )}
          </div>
          <fieldset className="grid gap-2">
            <legend className="mb-1 text-sm font-medium">
              {t("grantTypes")}
            </legend>
            <div className="grid gap-2 sm:grid-cols-3">
              {grants.map((grant) => {
                const disabled =
                  draft.publicClient && grant.value === "client_credentials"
                return (
                  <label
                    key={grant.value}
                    className="flex min-h-12 items-center gap-2 rounded-md border px-3 text-sm"
                  >
                    <Checkbox
                      checked={draft.grantTypes.includes(grant.value)}
                      disabled={disabled}
                      onCheckedChange={(value) =>
                        toggleGrant(grant.value, value === true)
                      }
                    />
                    <span className={disabled ? "text-muted-foreground" : ""}>
                      {grant.label}
                    </span>
                  </label>
                )
              })}
            </div>
          </fieldset>
          <div className="grid gap-2">
            <Label htmlFor="oauth-redirect-uris">{t("redirectUris")}</Label>
            <Textarea
              id="oauth-redirect-uris"
              value={draft.redirectUris}
              onChange={(event) => update("redirectUris", event.target.value)}
              placeholder="https://app.example.test/oauth/callback"
              rows={3}
              maxLength={8192}
              spellCheck={false}
              className="font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">
              {t("redirectUriHint")}
            </p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="oauth-scopes">Scopes</Label>
            <Input
              id="oauth-scopes"
              value={draft.scopes}
              onChange={(event) => update("scopes", event.target.value)}
              placeholder="openid profile email"
              required
              maxLength={2048}
              className="font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">{t("scopeHint")}</p>
          </div>
        </form>
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
              Client ID
            </span>
            <code className="mt-1 block font-mono text-xs break-all">
              {value?.clientID}
            </code>
          </div>
          <div className="rounded-md border bg-muted/40 p-3">
            <span className="block text-xs text-muted-foreground">
              Client Secret
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
