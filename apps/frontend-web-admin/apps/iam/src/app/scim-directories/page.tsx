import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react"
import {
  CheckCircle2,
  Copy,
  FolderSync,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react"
import { useSearchParams } from "react-router"
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@workspace/ui/components/tabs"
import { Badge } from "@workspace/ui/components/badge"
import { Button, buttonVariants } from "@workspace/ui/components/button"
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
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@workspace/ui/components/tooltip"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import {
  ApiError,
  clientErrorMessage,
  createIamApi,
  getTenant,
  type SCIMCredential,
  type SCIMCredentialSecret,
  type SCIMDirectory,
  type ServiceAccount,
} from "../../lib/iam-api"
import { SCIMTargets } from "./targets"

interface DirectoryDraft {
  name: string
  enabled: boolean
  serviceAccountId: string
}

interface CredentialDraft {
  name: string
  expiresAt: string
}

const emptyDirectory: DirectoryDraft = {
  name: "",
  enabled: true,
  serviceAccountId: "",
}
const emptyCredential: CredentialDraft = { name: "", expiresAt: "" }

export default function SCIMDirectoriesPage() {
  const t = useTranslations("iam")

  const [tenant, setTenantValue] = useState(getTenant)
  const [params, setParams] = useSearchParams()
  const view = params.get("view") === "targets" ? "targets" : "directories"
  useEffect(() => {
    const sync = () => setTenantValue(getTenant())
    window.addEventListener("tenant-change", sync)
    return () => window.removeEventListener("tenant-change", sync)
  }, [])
  return (
    <Tabs
      value={view}
      onValueChange={(value) => {
        const next = new URLSearchParams(params)
        if (value === "targets") next.set("view", "targets")
        else next.delete("view")
        setParams(next)
      }}
    >
      <TabsList variant="line" className="mb-4" aria-label={t("scimDirection")}>
        <TabsTrigger variant="line" value="directories">
          {t("inboundDirectories")}
        </TabsTrigger>
        <TabsTrigger variant="line" value="targets">
          {t("outboundTargets")}
        </TabsTrigger>
      </TabsList>
      <TabsContent value="directories">
        {view === "directories" && (
          <TenantSCIMDirectories key={tenant} tenant={tenant} />
        )}
      </TabsContent>
      <TabsContent value="targets">
        {view === "targets" && <SCIMTargets key={tenant} tenant={tenant} />}
      </TabsContent>
    </Tabs>
  )
}

function TenantSCIMDirectories({ tenant }: { tenant: string }) {
  const locale = useLocale()

  const t = useTranslations("iam")

  const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])
  const [directories, setDirectories] = useState<SCIMDirectory[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [editing, setEditing] = useState<SCIMDirectory | null | undefined>()
  const [directoryConflict, setDirectoryConflict] = useState(false)
  const [directoryDraft, setDirectoryDraft] =
    useState<DirectoryDraft>(emptyDirectory)
  const [credentialDirectory, setCredentialDirectory] =
    useState<SCIMDirectory | null>(null)
  const [credentials, setCredentials] = useState<SCIMCredential[]>([])
  const [credentialsLoading, setCredentialsLoading] = useState(false)
  const [credentialDraft, setCredentialDraft] =
    useState<CredentialDraft>(emptyCredential)
  const [revoking, setRevoking] = useState<SCIMCredential | null>(null)
  const [secret, setSecret] = useState<SCIMCredentialSecret | null>(null)
  const [endpoint] = useState(() => scimEndpoint())

  const loadGeneration = useRef(0)
  const [loadError, setLoadError] = useState("")
  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoading(true)
    setLoadError("")
    try {
      const next = await api.scimDirectories()
      if (generation === loadGeneration.current) setDirectories(next)
    } catch (cause) {
      if (generation === loadGeneration.current) {
        setDirectories([])
        setLoadError((cause as Error).message)
      }
    } finally {
      if (generation === loadGeneration.current) setLoading(false)
    }
  }, [api])

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
    setError("")
    setDirectoryConflict(false)
    setDirectoryDraft({ ...emptyDirectory })
    setEditing(null)
  }

  const openEdit = (directory: SCIMDirectory) => {
    setError("")
    setDirectoryConflict(false)
    setDirectoryDraft({
      name: directory.name,
      enabled: directory.status === "active",
      serviceAccountId: directory.service_account_id ?? "",
    })
    setEditing(directory)
  }

  const reloadDirectory = async () => {
    if (!editing) return
    setBusy(true)
    setError("")
    try {
      const items = await api.scimDirectories()
      setDirectories(items)
      const current = items.find((item) => item.id === editing.id)
      if (current) openEdit(current)
      else setEditing(undefined)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const saveDirectory = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    setNotice("")
    try {
      if (editing) {
        await api.updateSCIMDirectory(editing.id, {
          name: directoryDraft.name.trim(),
          status: directoryDraft.enabled ? "active" : "disabled",
          version: editing.version,
          service_account_id: directoryDraft.serviceAccountId.trim() || null,
        })
        setNotice(t("scimUpdated"))
      } else {
        await api.createSCIMDirectory({
          name: directoryDraft.name.trim(),
          service_account_id: directoryDraft.serviceAccountId.trim() || null,
        })
        setNotice(t("scimCreated"))
      }
      setEditing(undefined)
      await refresh()
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409 && editing)
        setDirectoryConflict(true)
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const openCredentials = async (directory: SCIMDirectory) => {
    setCredentialDirectory(directory)
    setCredentialDraft({ ...emptyCredential })
    setCredentials([])
    setCredentialsLoading(true)
    setError("")
    try {
      setCredentials(await api.scimCredentials(directory.id))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setCredentialsLoading(false)
    }
  }

  const createCredential = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!credentialDirectory) return
    setBusy(true)
    setError("")
    try {
      const result = await api.createSCIMCredential(credentialDirectory.id, {
        name: credentialDraft.name.trim(),
        expires_at: isoDateTime(credentialDraft.expiresAt),
      })
      setCredentialDirectory(null)
      setSecret(result)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const revokeCredential = async () => {
    if (!credentialDirectory || !revoking) return
    setBusy(true)
    setError("")
    try {
      await api.revokeSCIMCredential(credentialDirectory.id, revoking.id)
      setCredentials(await api.scimCredentials(credentialDirectory.id))
      setRevoking(null)
      setNotice(t("scimCredentialRevoked"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const copy = async (value: string, message: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setNotice(message)
    } catch {
      setError(clientErrorMessage("clipboard_access_denied"))
    }
  }

  return (
    <>
      <PageHeader
        title={t("scimDirectories")}
        description={t("scimDescription")}
        action={
          <Button onClick={openCreate}>
            <Plus />
            {t("createDirectory")}
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
      {notice && <SuccessNotice>{notice}</SuccessNotice>}

      <section className="mb-4 flex min-h-14 flex-wrap items-center justify-between gap-3 border-y bg-muted/20 px-4 py-3">
        <div className="min-w-0">
          <span className="block text-xs font-medium text-muted-foreground">
            SCIM Base URL
          </span>
          <code className="mt-1 block truncate font-mono text-xs">
            {endpoint}
          </code>
        </div>
        <Button
          type="button"
          size="icon"
          variant="outline"
          aria-label={t("copyScimUrl")}
          onClick={() => void copy(endpoint, t("scimUrlCopied"))}
        >
          <Copy />
        </Button>
      </section>

      <section className="overflow-hidden rounded-md border bg-card">
        <header className="flex min-h-14 items-center justify-between gap-4 border-b bg-muted/20 px-4">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <FolderSync className="size-4 text-muted-foreground" />
            {t("provisioningDirectories")}
          </div>
          <span className="text-xs text-muted-foreground">
            {loading
              ? t("loading")
              : !loadError &&
                t("directoryCount", { count: directories.length })}
          </span>
        </header>
        <div className="hidden md:block">
          <Table containerClassName="min-h-56">
            <TableHeader>
              <TableRow>
                <TableHead className="min-w-64">{t("directory")}</TableHead>
                <TableHead className="min-w-44">{t("updatedAt")}</TableHead>
                <TableHead className="w-24">{t("version")}</TableHead>
                <TableHead className="w-24">{t("status")}</TableHead>
                <TableHead className="w-32 text-right">
                  {t("actions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(loading || loadError ? [] : directories).map((directory) => (
                <TableRow key={directory.id}>
                  <TableCell>
                    <strong className="block text-sm">{directory.name}</strong>
                    <code className="mt-1 block max-w-72 truncate font-mono text-xs text-muted-foreground">
                      {directory.id}
                    </code>
                  </TableCell>
                  <TableCell>
                    {formatDate(
                      directory.updated_at,
                      locale,
                      t("neverExpires")
                    )}
                  </TableCell>
                  <TableCell>v{directory.version}</TableCell>
                  <TableCell>
                    <DirectoryStatus status={directory.status} />
                  </TableCell>
                  <TableCell className="text-right">
                    <DirectoryActions
                      directory={directory}
                      busy={busy}
                      onCredentials={() => void openCredentials(directory)}
                      onEdit={() => openEdit(directory)}
                    />
                  </TableCell>
                </TableRow>
              ))}
              {!loading && !loadError && directories.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="h-52 text-center">
                    <EmptyDirectories />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className="md:hidden">
          {(loading || loadError ? [] : directories).map((directory) => (
            <article
              key={directory.id}
              className="space-y-3 border-b p-4 last:border-0"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <strong className="block text-sm break-words">
                    {directory.name}
                  </strong>
                  <code className="mt-1 block truncate font-mono text-xs text-muted-foreground">
                    {directory.id}
                  </code>
                </div>
                <DirectoryStatus status={directory.status} />
              </div>
              <p className="text-xs text-muted-foreground">
                v{directory.version} ·{" "}
                {formatDate(directory.updated_at, locale, t("neverExpires"))}
              </p>
              <DirectoryActions
                directory={directory}
                busy={busy}
                onCredentials={() => void openCredentials(directory)}
                onEdit={() => openEdit(directory)}
              />
            </article>
          ))}
          {!loading && !loadError && directories.length === 0 && (
            <div className="grid h-52 place-items-center text-center">
              <EmptyDirectories />
            </div>
          )}
        </div>
      </section>

      <DirectoryEditor
        api={api}
        error={error}
        conflict={directoryConflict}
        onReload={() => void reloadDirectory()}
        open={editing !== undefined}
        directory={editing ?? null}
        draft={directoryDraft}
        busy={busy}
        onDraft={setDirectoryDraft}
        onClose={() => !busy && setEditing(undefined)}
        onSubmit={saveDirectory}
      />
      <CredentialsDialog
        directory={credentialDirectory}
        credentials={credentials}
        loading={credentialsLoading}
        busy={busy}
        draft={credentialDraft}
        onDraft={setCredentialDraft}
        onClose={() => !busy && setCredentialDirectory(null)}
        onSubmit={createCredential}
        onRevoke={setRevoking}
      />
      <ConfirmDialog
        open={revoking !== null}
        busy={busy}
        title={t("revokeScimCredential")}
        description={t("revokeScimConfirm", {
          name: revoking?.name ?? t("thisCredential"),
        })}
        onClose={() => !busy && setRevoking(null)}
        onConfirm={() => void revokeCredential()}
      />
      <SecretDialog
        value={secret}
        endpoint={endpoint}
        onCopy={copy}
        onClose={() => setSecret(null)}
      />
    </>
  )
}

function DirectoryActions({
  directory,
  busy,
  onCredentials,
  onEdit,
}: {
  directory: SCIMDirectory
  busy: boolean
  onCredentials: () => void
  onEdit: () => void
}) {
  const t = useTranslations("iam")

  return (
    <TooltipProvider>
      <div className="flex justify-end gap-1">
        <ActionButton
          label={t("manageCredentialsNamed", { name: directory.name })}
          onClick={onCredentials}
          disabled={busy}
        >
          <KeyRound />
        </ActionButton>
        <ActionButton
          label={t("editNamedSpaced", { name: directory.name })}
          onClick={onEdit}
          disabled={busy}
        >
          <Pencil />
        </ActionButton>
      </div>
    </TooltipProvider>
  )
}

function DirectoryEditor({
  api,
  error,
  conflict,
  onReload,
  open,
  directory,
  draft,
  busy,
  onDraft,
  onClose,
  onSubmit,
}: {
  api: ReturnType<typeof createIamApi>
  error: string
  conflict: boolean
  onReload: () => void
  open: boolean
  directory: SCIMDirectory | null
  draft: DirectoryDraft
  busy: boolean
  onDraft: (value: DirectoryDraft) => void
  onClose: () => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const t = useTranslations("iam")

  const [accounts, setAccounts] = useState<ServiceAccount[]>([])
  const [accountError, setAccountError] = useState("")
  useEffect(() => {
    if (!open) return
    let active = true
    const timer = window.setTimeout(() => {
      void api.serviceAccounts(draft.serviceAccountId).then(
        ({ items }) => {
          if (!active) return
          setAccounts(
            items.filter(
              (account) =>
                account.status === "active" &&
                (!account.expires_at ||
                  Date.parse(account.expires_at) > Date.now())
            )
          )
          setAccountError("")
        },
        (cause: Error) => {
          if (!active) return
          setAccounts([])
          setAccountError(cause.message)
        }
      )
    }, 200)
    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [api, open, draft.serviceAccountId])
  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="grid max-h-[calc(100svh-2rem)] w-[calc(100vw-2rem)] gap-5 overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {directory ? t("editScimDirectory") : t("createScimDirectory")}
          </DialogTitle>
          <DialogDescription>{t("scimFormDescription")}</DialogDescription>
        </DialogHeader>
        <form
          id="scim-directory-form"
          className="grid gap-5"
          onSubmit={onSubmit}
        >
          {error && <Alert>{error}</Alert>}
          <div className="grid gap-2">
            <Label htmlFor="scim-directory-name">{t("directoryName")}</Label>
            <Input
              id="scim-directory-name"
              value={draft.name}
              onChange={(event) =>
                onDraft({ ...draft, name: event.target.value })
              }
              required
              minLength={1}
              maxLength={128}
              autoFocus
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="scim-directory-service-account">
              {t("serviceAccounts")}
            </Label>
            <Input
              id="scim-directory-service-account"
              list="scim-service-account-options"
              value={draft.serviceAccountId}
              onChange={(event) =>
                onDraft({ ...draft, serviceAccountId: event.target.value })
              }
              pattern="[1-9][0-9]*"
              maxLength={64}
              placeholder={t("unbound")}
              autoComplete="off"
              disabled={busy}
            />
            <datalist id="scim-service-account-options">
              {accounts.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.display_name || account.login_name}
                </option>
              ))}
            </datalist>
            {accountError && <Alert>{accountError}</Alert>}
          </div>
          {directory && (
            <div className="flex min-h-16 items-center justify-between gap-3 rounded-md border px-3">
              <div>
                <Label htmlFor="scim-directory-enabled">
                  {t("enableDirectory")}
                </Label>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t("disabledDirectoryHint")}
                </p>
              </div>
              <Switch
                id="scim-directory-enabled"
                checked={draft.enabled}
                onCheckedChange={(enabled) => onDraft({ ...draft, enabled })}
              />
            </div>
          )}
        </form>
        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onClose} disabled={busy}>
            {t("cancel")}
          </Button>
          {conflict ? (
            <Button onClick={onReload} disabled={busy}>
              <RefreshCw />
              {t("reload")}
            </Button>
          ) : (
            <Button type="submit" form="scim-directory-form" disabled={busy}>
              {busy ? t("savingProgress") : t("save")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CredentialsDialog({
  directory,
  credentials,
  loading,
  busy,
  draft,
  onDraft,
  onClose,
  onSubmit,
  onRevoke,
}: {
  directory: SCIMDirectory | null
  credentials: SCIMCredential[]
  loading: boolean
  busy: boolean
  draft: CredentialDraft
  onDraft: (value: CredentialDraft) => void
  onClose: () => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onRevoke: (credential: SCIMCredential) => void
}) {
  const locale = useLocale()

  const t = useTranslations("iam")

  const disabled = directory?.status !== "active"
  return (
    <Dialog
      open={directory !== null}
      onOpenChange={(value) => !value && onClose()}
    >
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>
            {directory?.name ?? "SCIM"} {t("credentialsSuffix")}
          </DialogTitle>
          <DialogDescription>{t("newBearerOnce")}</DialogDescription>
        </DialogHeader>
        <form
          id="scim-credential-form"
          className="grid gap-4 border-y py-4 sm:grid-cols-2"
          onSubmit={onSubmit}
        >
          <div className="grid gap-2">
            <Label htmlFor="scim-credential-name">{t("credentialName")}</Label>
            <Input
              id="scim-credential-name"
              value={draft.name}
              onChange={(event) =>
                onDraft({ ...draft, name: event.target.value })
              }
              required
              minLength={1}
              maxLength={128}
              disabled={disabled}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="scim-credential-expiry">
              {t("expirationTime")}
            </Label>
            <Input
              id="scim-credential-expiry"
              type="datetime-local"
              value={draft.expiresAt}
              onChange={(event) =>
                onDraft({ ...draft, expiresAt: event.target.value })
              }
              min={localDateTime(new Date().toISOString())}
              disabled={disabled}
            />
          </div>
        </form>
        <div className="hidden sm:block">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("credential")}</TableHead>
                <TableHead>{t("expirationTime")}</TableHead>
                <TableHead>{t("lastUsage")}</TableHead>
                <TableHead>{t("status")}</TableHead>
                <TableHead className="w-16 text-right">
                  {t("actions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {credentials.map((credential) => (
                <TableRow key={credential.id}>
                  <TableCell>
                    <strong className="block text-sm">{credential.name}</strong>
                    <code className="block max-w-48 truncate font-mono text-xs text-muted-foreground">
                      {credential.id}
                    </code>
                  </TableCell>
                  <TableCell>
                    {formatDate(
                      credential.expires_at,
                      locale,
                      t("neverExpires")
                    )}
                  </TableCell>
                  <TableCell>
                    {formatDate(
                      credential.last_used_at,
                      locale,
                      t("neverExpires")
                    )}
                  </TableCell>
                  <TableCell>
                    <CredentialStatus credential={credential} />
                  </TableCell>
                  <TableCell className="text-right">
                    {!credential.revoked_at && (
                      <ActionButton
                        label={t("revokeNamed", { name: credential.name })}
                        destructive
                        disabled={busy}
                        onClick={() => onRevoke(credential)}
                      >
                        <Trash2 />
                      </ActionButton>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <div className="sm:hidden">
          {credentials.map((credential) => (
            <article key={credential.id} className="space-y-2 border-b py-3">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <strong className="block text-sm">{credential.name}</strong>
                  <code className="block truncate font-mono text-xs text-muted-foreground">
                    {credential.id}
                  </code>
                </div>
                <CredentialStatus credential={credential} />
              </div>
              <p className="text-xs text-muted-foreground">
                {t("expiry")}
                {formatDate(
                  credential.expires_at,
                  locale,
                  t("neverExpires")
                )}{" "}
                {t("lastUsageSuffix")}{" "}
                {formatDate(credential.last_used_at, locale, t("neverExpires"))}
              </p>
              {!credential.revoked_at && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  onClick={() => onRevoke(credential)}
                >
                  <Trash2 />
                  {t("revoke")}
                </Button>
              )}
            </article>
          ))}
        </div>
        {(loading || credentials.length === 0) && (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {loading ? t("loadingCredentials") : t("noCredentials")}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            {t("close")}
          </Button>
          <Button
            type="submit"
            form="scim-credential-form"
            disabled={busy || disabled}
          >
            <Plus />
            {busy ? t("creating") : t("createCredential")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConfirmDialog({
  open,
  busy,
  title,
  description,
  onClose,
  onConfirm,
}: {
  open: boolean
  busy: boolean
  title: string
  description: string
  onClose: () => void
  onConfirm: () => void
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            {t("cancel")}
          </Button>
          <Button variant="destructive" onClick={onConfirm} disabled={busy}>
            {busy ? t("processingProgress") : t("confirmRevoke")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SecretDialog({
  value,
  endpoint,
  onCopy,
  onClose,
}: {
  value: SCIMCredentialSecret | null
  endpoint: string
  onCopy: (value: string, message: string) => Promise<void>
  onClose: () => void
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={value !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("saveScimToken")}</DialogTitle>
          <DialogDescription>{t("bearerOnce")}</DialogDescription>
        </DialogHeader>
        <SecretField
          label="SCIM Base URL"
          value={endpoint}
          onCopy={() => onCopy(endpoint, t("scimUrlCopied"))}
        />
        <SecretField
          label="Bearer token"
          value={value?.bearer_token ?? ""}
          onCopy={() => onCopy(value?.bearer_token ?? "", t("scimTokenCopied"))}
        />
        <DialogFooter>
          <Button onClick={onClose}>
            <CheckCircle2 />
            {t("safelySaved")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SecretField({
  label,
  value,
  onCopy,
}: {
  label: string
  value: string
  onCopy: () => Promise<void>
}) {
  const t = useTranslations("iam")

  return (
    <div className="grid gap-2">
      <Label>{label}</Label>
      <div className="flex gap-2">
        <Input value={value} readOnly className="font-mono text-xs" />
        <Button
          type="button"
          size="icon"
          variant="outline"
          aria-label={t("copyNamed", { label: label })}
          onClick={() => void onCopy()}
        >
          <Copy />
        </Button>
      </div>
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

function DirectoryStatus({ status }: { status: SCIMDirectory["status"] }) {
  const t = useTranslations("iam")

  return (
    <Badge variant={status === "active" ? "default" : "outline"}>
      {status === "active" ? t("active") : t("disable")}
    </Badge>
  )
}

function CredentialStatus({ credential }: { credential: SCIMCredential }) {
  const t = useTranslations("iam")

  const label = credential.revoked_at ? t("revoked") : t("notRevoked")
  return (
    <Badge variant={label === t("notRevoked") ? "default" : "outline"}>
      {label}
    </Badge>
  )
}

function EmptyDirectories() {
  const t = useTranslations("iam")

  return (
    <div>
      <FolderSync className="mx-auto mb-2 size-7 text-muted-foreground" />
      <p className="text-sm font-medium">{t("noScimDirectories")}</p>
    </div>
  )
}

function SuccessNotice({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="mb-4 flex items-start gap-2 rounded-md border border-emerald-600/30 bg-emerald-600/10 px-4 py-3 text-sm text-emerald-800 dark:text-emerald-300"
      role="status"
    >
      <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
      {children}
    </div>
  )
}

function scimEndpoint(): string {
  const api = (import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")
  if (typeof window === "undefined") return `${api}/scim/v2`
  return new URL(`${api}/scim/v2`, window.location.origin)
    .toString()
    .replace(/\/$/, "")
}

function localDateTime(value?: string): string {
  if (!value) return ""
  const date = new Date(value)
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

function isoDateTime(value: string): string | undefined {
  return value ? new Date(value).toISOString() : undefined
}

function formatDate(
  value: string | undefined,
  locale: string,
  emptyLabel: string
): string {
  return value
    ? new Intl.DateTimeFormat(locale, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(value))
    : emptyLabel
}
