import { AutoTable } from "@workspace/ui/components/auto-table/auto-table"
import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useRef, useState } from "react"
import {
  Bot,
  CheckCircle2,
  Copy,
  KeyRound,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react"
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
  type ServiceAccount,
  type ServiceAccountCredential,
  type ServiceAccountInput,
  type ServiceAccountUpdate,
} from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

interface AccountDraft {
  loginName: string
  displayName: string
  description: string
  expiresAt: string
  enabled: boolean
}

interface CredentialDraft {
  name: string
  scopes: string
  expiresAt: string
}

interface SecretResult {
  clientID: string
  clientSecret: string
}

const emptyAccount: AccountDraft = {
  loginName: "",
  displayName: "",
  description: "",
  expiresAt: "",
  enabled: true,
}

const emptyCredential: CredentialDraft = {
  name: "",
  scopes: "iam-api",
  expiresAt: "",
}

function accountDraft(account: ServiceAccount): AccountDraft {
  return {
    loginName: account.login_name,
    displayName: account.display_name,
    description: account.description ?? "",
    expiresAt: localDateTime(account.expires_at),
    enabled: account.status === "active",
  }
}

export default function ServiceAccountsPage() {
  const locale = useLocale()

  const t = useTranslations("iam")
  const allowed = useAllowed()

  const [accounts, setAccounts] = useState<ServiceAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [editing, setEditing] = useState<ServiceAccount | null | undefined>()
  const [draft, setDraft] = useState<AccountDraft>(emptyAccount)
  const [deleting, setDeleting] = useState<ServiceAccount | null>(null)
  const [credentialAccount, setCredentialAccount] =
    useState<ServiceAccount | null>(null)
  const [credentials, setCredentials] = useState<ServiceAccountCredential[]>([])
  const [credentialsLoading, setCredentialsLoading] = useState(false)
  const [credentialDraft, setCredentialDraft] =
    useState<CredentialDraft>(emptyCredential)
  const [revokeCredential, setRevokeCredential] =
    useState<ServiceAccountCredential | null>(null)
  const [secret, setSecret] = useState<SecretResult | null>(null)

  const loadGeneration = useRef(0)
  const [loadError, setLoadError] = useState("")
  const refresh = useCallback(async () => {
    const generation = ++loadGeneration.current
    setLoading(true)
    setLoadError("")
    try {
      const next = await iamApi.serviceAccounts()
      if (generation === loadGeneration.current) setAccounts(next.items)
    } catch (cause) {
      if (generation === loadGeneration.current) {
        setAccounts([])
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
    setError("")
    setDraft({ ...emptyAccount })
    setEditing(null)
  }

  const openEdit = (account: ServiceAccount) => {
    setError("")
    setDraft(accountDraft(account))
    setEditing(account)
  }

  const saveAccount = async () => {
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const expiresAt = isoDateTime(draft.expiresAt)
      if (editing) {
        const body: ServiceAccountUpdate = {
          display_name: draft.displayName.trim(),
          description: draft.description.trim() || undefined,
          status: draft.enabled ? "active" : "disabled",
          expires_at: expiresAt,
          version: editing.version,
        }
        await iamApi.updateServiceAccount(editing.id, body)
        setNotice(t("serviceAccountUpdated"))
      } else {
        const body: ServiceAccountInput = {
          login_name: draft.loginName.trim(),
          display_name: draft.displayName.trim() || undefined,
          description: draft.description.trim() || undefined,
          expires_at: expiresAt,
        }
        await iamApi.createServiceAccount(body)
        setNotice(t("serviceAccountCreated"))
      }
      setEditing(undefined)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const removeAccount = async () => {
    if (!deleting) return
    setBusy(true)
    setError("")
    try {
      await iamApi.deleteServiceAccount(deleting.id, deleting.version)
      setDeleting(null)
      setNotice(t("serviceAccountDeleted"))
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const openCredentials = async (
    account: ServiceAccount,
    resetDraft = true
  ) => {
    setCredentialAccount(account)
    if (resetDraft) setCredentialDraft({ ...emptyCredential })
    setCredentials([])
    setCredentialsLoading(true)
    setBusy(true)
    setError("")
    try {
      setCredentials(await iamApi.serviceAccountCredentials(account.id))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setCredentialsLoading(false)
      setBusy(false)
    }
  }

  const createCredential = async () => {
    if (!credentialAccount) return
    const scopes = uniqueWords(credentialDraft.scopes)
    if (scopes.length === 0) {
      setError(t("credentialScopeRequired"))
      return
    }
    setBusy(true)
    setError("")
    try {
      const result = await iamApi.createServiceAccountCredential(
        credentialAccount.id,
        {
          name: credentialDraft.name.trim(),
          scopes,
          expires_at: isoDateTime(credentialDraft.expiresAt),
        }
      )
      setCredentialAccount(null)
      setSecret({
        clientID: result.credential.id,
        clientSecret: result.client_secret,
      })
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const revoke = async () => {
    if (!credentialAccount || !revokeCredential) return
    setBusy(true)
    setError("")
    try {
      await iamApi.revokeServiceAccountCredential(
        credentialAccount.id,
        revokeCredential.id
      )
      setCredentials(
        await iamApi.serviceAccountCredentials(credentialAccount.id)
      )
      setRevokeCredential(null)
      setNotice(t("credentialRevoked"))
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader
        title={t("serviceAccounts")}
        description={t("serviceAccountsDescription")}
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

      <AutoTable
        fill
        toolbar={
          allowed("service_account_create") ? (
            <Button onClick={openCreate}>
              <Plus />
              {t("createServiceAccount")}
            </Button>
          ) : null
        }
        onRefresh={refresh}
        searchable

        emptyContent={loadError ? null : <EmptyAccounts />}
        loading={loading}
        data={loading || loadError ? [] : accounts}
        rowKey="id"
        columns={[
          {
            field: "login_name",
            name: <>{t("loginName")}</>,
            render: (_, account) => (
              <>
                <strong className="block text-sm">
                  {account.display_name}
                </strong>
                <span className="block text-xs text-muted-foreground">
                  {account.login_name}
                </span>
                <code className="mt-1 block max-w-72 truncate font-mono text-xs text-muted-foreground">
                  {account.id}
                </code>
              </>
            ),
          },
          {
            field: "description",
            name: <>{t("notes")}</>,
            render: (_, account) => <>{account.description || "-"}</>,
          },
          {
            field: "expires_at",
            name: <>{t("validity")}</>,
            render: (_, account) => (
              <>{formatDate(account.expires_at, locale, t("neverExpires"))}</>
            ),
          },
          {
            field: "status",
            name: <>{t("status")}</>,
            render: (_, account) => (
              <>
                <StatusBadge status={account.status} />
              </>
            ),
          },
          {
            field: "actions",
            type: "actions",
            name: <>{t("actions")}</>,
            render: (_, account) => (
              <>
                <AccountActions
                  account={account}
                  busy={busy}
                  onCredentials={() => void openCredentials(account)}
                  onEdit={() => openEdit(account)}
                  onDelete={() => setDeleting(account)}
                />
              </>
            ),
          },
        ]}
      />

      <AccountEditor
        open={editing !== undefined}
        account={editing ?? null}
        draft={draft}
        busy={busy}
        onDraft={setDraft}
        onOpenChange={(open) => !open && !busy && setEditing(undefined)}
        onSubmit={saveAccount}
      />
      <CredentialsDialog
        account={credentialAccount}
        credentials={credentials}
        loading={credentialsLoading}
        draft={credentialDraft}
        busy={busy}
        onDraft={setCredentialDraft}
        onClose={() => !busy && setCredentialAccount(null)}
        onRefresh={() =>
          credentialAccount
            ? openCredentials(credentialAccount, false)
            : Promise.resolve()
        }
        onSubmit={createCredential}
        onRevoke={setRevokeCredential}
      />
      <ConfirmDialog
        open={deleting !== null}
        title={t("deleteServiceAccount")}
        description={t("deleteServiceAccountConfirm", {
          name: deleting?.display_name ?? t("thisServiceAccount"),
        })}
        busy={busy}
        onClose={() => !busy && setDeleting(null)}
        onConfirm={() => void removeAccount()}
      />
      <ConfirmDialog
        open={revokeCredential !== null}
        title={t("revokeCredential")}
        description={t("revokeCredentialConfirm", {
          name: revokeCredential?.name ?? t("thisCredential"),
        })}
        busy={busy}
        onClose={() => !busy && setRevokeCredential(null)}
        onConfirm={() => void revoke()}
      />
      <SecretDialog value={secret} onClose={() => setSecret(null)} />
    </div>
  )
}

function AccountActions({
  account,
  busy,
  onCredentials,
  onEdit,
  onDelete,
}: {
  account: ServiceAccount
  busy: boolean
  onCredentials: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()

  return (
    <TooltipProvider>
      <div className="flex justify-end gap-1">
        {allowed("service_account_manage_credential") && (
          <ActionButton
            label={t("manageCredentialsNamed", { name: account.display_name })}
            onClick={onCredentials}
            disabled={busy}
          >
            <KeyRound />
          </ActionButton>
        )}
        {allowed("service_account_update") && (
          <ActionButton
            label={t("editNamedSpaced", { name: account.display_name })}
            onClick={onEdit}
            disabled={busy}
          >
            <Pencil />
          </ActionButton>
        )}
        {allowed("service_account_delete") && (
          <ActionButton
            label={t("deleteNamedSpaced", { name: account.display_name })}
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

function AccountEditor({
  open,
  account,
  draft,
  busy,
  onDraft,
  onOpenChange,
  onSubmit,
}: {
  open: boolean
  account: ServiceAccount | null
  draft: AccountDraft
  busy: boolean
  onDraft: (value: AccountDraft) => void
  onOpenChange: (open: boolean) => void
  onSubmit: () => void
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {account ? t("editServiceAccount") : t("createServiceAccount")}
          </DialogTitle>
          <DialogDescription>
            {account ? account.login_name : t("serviceAccountNoLogin")}
          </DialogDescription>
        </DialogHeader>
        <AutoForm
          key={account?.id ?? "new"}
          id="service-account-form"
          mode="edit"
          initialData={{ ...draft }}
          fields={{
            loginName: {
              name: t("loginIdentifier"),
              type: "text",
              required: true,
              disabled: Boolean(account) || busy,
              col: 2,
              config: { id: "service-account-login", maxLength: 200 },
            },
            displayName: {
              name: t("displayName"),
              type: "text",
              required: Boolean(account),
              disabled: busy,
              col: 2,
              config: { id: "service-account-display", maxLength: 128 },
            },
            description: {
              name: t("notes"),
              type: "textarea",
              required: false,
              disabled: busy,
              col: 2,
              config: {
                id: "service-account-description",
                maxLength: 1000,
                rows: 3,
              },
            },
            expiresAt: {
              name: t("validity"),
              type: "text",
              required: false,
              disabled: busy,
              col: 2,
              config: { id: "service-account-expiry", type: "datetime-local" },
            },
            enabled: {
              name: t("enableAccount"),
              type: "switch",
              show: Boolean(account),
              disabled: busy,
              col: 2,
              config: { id: "service-account-enabled" },
            },
          }}
          onChange={(values) => {
            if (JSON.stringify(values) !== JSON.stringify(draft))
              onDraft(values as unknown as AccountDraft)
          }}
          onSubmit={() => onSubmit()}
        ></AutoForm>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            {t("cancel")}
          </Button>
          <Button type="submit" form="service-account-form" disabled={busy}>
            {busy ? t("savingProgress") : t("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CredentialsDialog({
  account,
  credentials,
  loading,
  draft,
  busy,
  onDraft,
  onClose,
  onSubmit,
  onRefresh,
  onRevoke,
}: {
  account: ServiceAccount | null
  credentials: ServiceAccountCredential[]
  loading: boolean
  draft: CredentialDraft
  busy: boolean
  onDraft: (value: CredentialDraft) => void
  onClose: () => void
  onSubmit: () => void
  onRefresh: () => Promise<void>
  onRevoke: (credential: ServiceAccountCredential) => void
}) {
  const locale = useLocale()

  const t = useTranslations("iam")

  return (
    <Dialog open={account !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("serviceAccountCredentials")}</DialogTitle>
          <DialogDescription>{account?.display_name}</DialogDescription>
        </DialogHeader>
        <AutoForm
          key={account?.id ?? "new"}
          id="service-credential-form"
          mode="edit"
          initialData={{ ...draft }}
          fields={{
            name: {
              name: t("credentialName"),
              type: "text",
              required: true,
              disabled: busy,
              col: 2,
              config: { id: "service-credential-name", maxLength: 128 },
            },
            scopes: {
              name: t("scopes"),
              type: "text",
              required: true,
              disabled: busy,
              col: 2,
              config: { id: "service-credential-scopes" },
            },
            expiresAt: {
              name: t("validity"),
              type: "text",
              required: false,
              disabled: busy,
              col: 2,
              config: {
                id: "service-credential-expiry",
                type: "datetime-local",
              },
            },
          }}
          onChange={(values) => {
            if (JSON.stringify(values) !== JSON.stringify(draft))
              onDraft(values as unknown as CredentialDraft)
          }}
          onSubmit={() => onSubmit()}
        ></AutoForm>
        <div className="min-w-0">
          <AutoTable
            className="max-h-[min(55svh,32rem)]"
            searchable
            onRefresh={onRefresh}
            toolbar={
              <Button
                type="submit"
                form="service-credential-form"
                disabled={busy}
              >
                <Plus />
                {busy ? t("creating") : t("createCredential")}
              </Button>
            }
            loading={loading}
            emptyContent={t("noCredentials")}
            data={credentials}
            rowKey="id"
            columns={[
              {
                field: "name",
                name: <>{t("name")}</>,
                render: (_, credential) => <>{credential.name}</>,
              },
              {
                field: "id",
                name: <>{t("clientId")}</>,
                render: (_, credential) => (
                  <>
                    <code className="font-mono text-xs">{credential.id}</code>
                  </>
                ),
              },
              {
                field: "scopes",
                name: <>{t("scopes")}</>,
                render: (_, credential) => <>{credential.scopes.join(" ")}</>,
              },
              {
                field: "expires_at",
                name: <>{t("validity")}</>,
                render: (_, credential) => (
                  <>
                    {formatDate(
                      credential.expires_at,
                      locale,
                      t("neverExpires")
                    )}
                  </>
                ),
              },
              {
                field: "last_used_at",
                name: <>{t("lastUsedLabel")}</>,
                render: (_, credential) => (
                  <>
                    {formatDate(
                      credential.last_used_at,
                      locale,
                      t("neverExpires")
                    )}
                  </>
                ),
              },
              {
                field: "revoked_at",
                name: <>{t("status")}</>,
                render: (_, credential) => (
                  <>
                    <CredentialStatus credential={credential} />
                  </>
                ),
              },
              {
                field: "actions",
                type: "actions",
                name: <>{t("actions")}</>,
                render: (_, credential) => (
                  <>
                    {!credential.revoked_at && (
                      <ActionButton
                        label={t("revokeNamed", { name: credential.name })}
                        onClick={() => onRevoke(credential)}
                        disabled={busy}
                        destructive
                      >
                        <Trash2 />
                      </ActionButton>
                    )}
                  </>
                ),
              },
            ]}
          />
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={busy}
          >
            {t("close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ConfirmDialog({
  open,
  title,
  description,
  busy,
  onClose,
  onConfirm,
}: {
  open: boolean
  title: string
  description: string
  busy: boolean
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
            {busy ? t("processingProgress") : t("confirm")}
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

  const copy = async (text: string) => navigator.clipboard.writeText(text)
  return (
    <Dialog open={value !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("saveClientCredentials")}</DialogTitle>
          <DialogDescription>{t("secretOnce")}</DialogDescription>
        </DialogHeader>
        <SecretField
          label={t("clientId")}
          value={value?.clientID ?? ""}
          onCopy={copy}
        />
        <SecretField
          label={t("clientSecret")}
          value={value?.clientSecret ?? ""}
          onCopy={copy}
        />
        <DialogFooter>
          <Button onClick={onClose}>{t("done")}</Button>
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
  onCopy: (value: string) => Promise<void>
}) {
  const t = useTranslations("iam")

  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      <div className="flex gap-2">
        <Input value={value} readOnly className="font-mono" />
        <Button
          type="button"
          size="icon"
          variant="outline"
          aria-label={t("copyNamed", { label: label })}
          onClick={() => void onCopy(value)}
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

function StatusBadge({ status }: { status: ServiceAccount["status"] }) {
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

function CredentialStatus({
  credential,
}: {
  credential: ServiceAccountCredential
}) {
  const t = useTranslations("iam")

  const label = credential.revoked_at ? t("revoked") : t("notRevoked")
  return (
    <Badge variant={label === t("notRevoked") ? "default" : "outline"}>
      {label}
    </Badge>
  )
}

function EmptyAccounts() {
  const t = useTranslations("iam")

  return (
    <div>
      <Bot className="mx-auto mb-2 size-7 text-muted-foreground" />
      <p className="text-sm font-medium">{t("noServiceAccounts")}</p>
    </div>
  )
}

function SuccessNotice({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-4 flex items-start gap-2 rounded-md border border-emerald-600/30 bg-emerald-600/10 px-4 py-3 text-sm text-emerald-800 dark:text-emerald-300">
      <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
      {children}
    </div>
  )
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
