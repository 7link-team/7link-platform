import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useState, type FormEvent } from "react"
import { useLocation } from "react-router"
import {
  CheckCircle2,
  Copy,
  Download,
  Fingerprint,
  KeyRound,
  Laptop,
  LogOut,
  Mail,
  Pencil,
  Plus,
  RefreshCw,
  ShieldCheck,
  ShieldOff,
  Smartphone,
  Trash2,
} from "lucide-react"
import { QRCodeSVG } from "qrcode.react"
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
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { PageHeader } from "../../components/page-header"
import { AvatarEditor } from "../../components/avatar-editor"
import {
  iamApi,
  setTenant,
  type BrowserSession,
  type MfaEnrollment,
  type MfaStatus,
  type Passkey,
  type Session,
} from "../../lib/iam-api"
import { useBranding } from "../../lib/branding"
import { createPasskeyCredential } from "../../lib/webauthn"

type MfaDialog =
  | "enroll-password"
  | "enroll-confirm"
  | "recovery"
  | "regenerate"
  | "disable"
  | null

type PasskeyDialog =
  | { type: "register" }
  | { type: "rename"; passkey: Passkey }
  | { type: "delete"; passkey: Passkey }
  | null

export default function SecurityPage() {
  const t = useTranslations("iam")

  const { pathname } = useLocation()
  const section =
    pathname === "/user/profile"
      ? "profile"
      : pathname === "/user/mfa"
        ? "mfa"
        : "security"
  const { session } = useAuth()
  const [sessions, setSessions] = useState<BrowserSession[]>([])
  const [mfa, setMfa] = useState<MfaStatus | null>(null)
  const [passkeys, setPasskeys] = useState<Passkey[]>([])
  const [passkeyError, setPasskeyError] = useState("")
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [mfaDialog, setMfaDialog] = useState<MfaDialog>(null)
  const [passkeyDialog, setPasskeyDialog] = useState<PasskeyDialog>(null)
  const [enrollment, setEnrollment] = useState<MfaEnrollment | null>(null)
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")

  const refresh = async () => {
    if (section === "mfa") setMfa(await iamApi.mfaStatus())
    if (section === "security") {
      await Promise.all([
        iamApi.sessions().then(setSessions),
        iamApi.passkeys().then(setPasskeys),
      ])
    }
  }

  useEffect(() => {
    let active = true
    const failed = (cause: Error) => {
      if (active) setError(cause.message)
    }
    if (section === "security") {
      void iamApi.sessions().then((value) => {
        if (active) setSessions(value)
      }, failed)
      void iamApi.passkeys().then(
        (value) => {
          if (active) setPasskeys(value)
        },
        (cause: Error) => {
          if (active) setPasskeyError(cause.message)
        }
      )
    }
    if (section === "mfa") {
      void iamApi.mfaStatus().then((value) => {
        if (active) setMfa(value)
      }, failed)
    }
    return () => {
      active = false
    }
  }, [section])

  const run = async (operation: () => Promise<void>) => {
    setBusy(true)
    setError("")
    setNotice("")
    try {
      await operation()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const revoke = (session: BrowserSession) =>
    run(async () => {
      await iamApi.revokeSession(session.id)
      if (session.current) window.location.assign("/user/login")
      else {
        await refresh()
        setNotice(t("sessionRevoked"))
      }
    })

  const logoutAll = () =>
    run(async () => {
      const result = await iamApi.logoutAll()
      setTenant("")
      window.location.assign(result?.logout_url || "/user/login")
    })

  const sendEmailVerification = () =>
    run(async () => {
      await iamApi.startEmailVerification()
      setNotice(t("emailQueued"))
    })

  const changePassword = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    void run(async () => {
      await iamApi.changePassword({
        current_password: String(data.get("current_password") ?? ""),
        new_password: String(data.get("new_password") ?? ""),
      })
      form.reset()
      setPasswordOpen(false)
      setNotice(t("passwordUpdated"))
      await refresh()
    })
  }

  const beginEnrollment = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    void run(async () => {
      const next = await iamApi.beginTotpEnrollment(
        String(data.get("current_password") ?? "")
      )
      form.reset()
      setEnrollment(next)
      setMfaDialog("enroll-confirm")
    })
  }

  const confirmEnrollment = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    void run(async () => {
      const result = await iamApi.confirmTotpEnrollment(
        String(data.get("code") ?? "").trim()
      )
      form.reset()
      setEnrollment(null)
      setRecoveryCodes(result.recovery_codes)
      setMfaDialog("recovery")
      await refresh()
    })
  }

  const verifyMfaChange = (
    event: FormEvent<HTMLFormElement>,
    action: "regenerate" | "disable"
  ) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    const input = {
      current_password: String(data.get("current_password") ?? ""),
      code: String(data.get("code") ?? "").trim(),
    }
    void run(async () => {
      if (action === "regenerate") {
        const result = await iamApi.regenerateRecoveryCodes(input)
        setRecoveryCodes(result.recovery_codes)
        setMfaDialog("recovery")
      } else {
        await iamApi.disableTotp(input)
        setMfaDialog(null)
        setNotice(t("mfaDisabled"))
      }
      form.reset()
      await refresh()
    })
  }

  const closeMfaDialog = (open: boolean) => {
    if (open) return
    if (mfaDialog === "recovery") setRecoveryCodes([])
    if (mfaDialog === "enroll-confirm") setEnrollment(null)
    setMfaDialog(null)
  }

  const registerPasskey = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    void run(async () => {
      const options = await iamApi.beginPasskeyRegistration(
        String(data.get("current_password") ?? "")
      )
      const credential = await createPasskeyCredential(options.options)
      await iamApi.finishPasskeyRegistration({
        challenge_id: options.challenge_id,
        name: String(data.get("name") ?? "").trim(),
        credential,
      })
      form.reset()
      setPasskeyDialog(null)
      setNotice(t("passkeyAdded"))
      await refresh()
    })
  }

  const renamePasskey = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (passkeyDialog?.type !== "rename") return
    const form = event.currentTarget
    const data = new FormData(form)
    const id = passkeyDialog.passkey.id
    void run(async () => {
      await iamApi.renamePasskey(id, String(data.get("name") ?? "").trim())
      setPasskeyDialog(null)
      setNotice(t("passkeyRenamed"))
      await refresh()
    })
  }

  const deletePasskey = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (passkeyDialog?.type !== "delete") return
    const form = event.currentTarget
    const data = new FormData(form)
    const id = passkeyDialog.passkey.id
    void run(async () => {
      await iamApi.deletePasskey(id, String(data.get("current_password") ?? ""))
      form.reset()
      setPasskeyDialog(null)
      setNotice(t("passkeyDeleted"))
      await refresh()
    })
  }

  return (
    <>
      <PageHeader
        title={
          section === "profile"
            ? t("profile")
            : section === "mfa"
              ? t("twoFactor")
              : t("security")
        }
        action={
          section === "security" ? (
            <Button onClick={() => setPasswordOpen(true)}>
              <KeyRound />
              {t("changePassword")}
            </Button>
          ) : undefined
        }
      />
      {error && <Alert>{error}</Alert>}
      {notice && <SuccessNotice>{notice}</SuccessNotice>}

      {section === "profile" && (
        <>
          {session && <AvatarEditor id={session.subject} />}
          <dl className="mb-5 grid gap-4 border-b py-5 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-muted-foreground">{t("username")}</dt>
              <dd className="mt-1 break-all">
                {session?.preferred_username ?? t("notVisible")}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("accountId")}</dt>
              <dd className="mt-1 font-mono break-all">{session?.subject}</dd>
            </div>
          </dl>
          <EmailSection
            session={session}
            busy={busy}
            onSend={sendEmailVerification}
          />
        </>
      )}

      {section === "mfa" && (
        <MfaSection
          status={mfa}
          busy={busy}
          onEnable={() => setMfaDialog("enroll-password")}
          onRegenerate={() => setMfaDialog("regenerate")}
          onDisable={() => setMfaDialog("disable")}
        />
      )}

      {section === "security" && (
        <>
          <PasskeySection
            passkeys={passkeys}
            error={passkeyError}
            busy={busy}
            onRegister={() => setPasskeyDialog({ type: "register" })}
            onRename={(passkey) =>
              setPasskeyDialog({ type: "rename", passkey })
            }
            onDelete={(passkey) =>
              setPasskeyDialog({ type: "delete", passkey })
            }
          />

          <SessionSection
            sessions={sessions}
            busy={busy}
            onLogoutAll={logoutAll}
            onRevoke={revoke}
          />
        </>
      )}

      <PasswordDialog
        open={passwordOpen}
        busy={busy}
        onOpenChange={setPasswordOpen}
        onSubmit={changePassword}
      />
      <MfaFlowDialog
        state={mfaDialog}
        enrollment={enrollment}
        recoveryCodes={recoveryCodes}
        busy={busy}
        onOpenChange={closeMfaDialog}
        onBegin={beginEnrollment}
        onConfirm={confirmEnrollment}
        onRegenerate={(event) => verifyMfaChange(event, "regenerate")}
        onDisable={(event) => verifyMfaChange(event, "disable")}
      />
      <PasskeyFlowDialog
        state={passkeyDialog}
        busy={busy}
        onOpenChange={(open) => !open && setPasskeyDialog(null)}
        onRegister={registerPasskey}
        onRename={renamePasskey}
        onDelete={deletePasskey}
      />
    </>
  )
}

function EmailSection({
  session,
  busy,
  onSend,
}: {
  session: Session | null
  busy: boolean
  onSend: () => void
}) {
  const t = useTranslations("iam")

  const email = session?.email ?? ""
  const verified = Boolean(session?.email_verified)
  const verificationVisible = session?.email_verified !== undefined
  return (
    <section className="mb-5 overflow-hidden rounded-md border bg-card">
      <header className="flex min-h-20 flex-wrap items-center justify-between gap-4 border-b px-5 py-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
            <Mail size={20} />
          </span>
          <div className="min-w-0">
            <h2 className="font-semibold">{t("primaryEmail")}</h2>
            <p className="mt-1 truncate text-sm text-muted-foreground">
              {email || (verificationVisible ? t("noEmail") : t("notVisible"))}
            </p>
          </div>
        </div>
        {!verificationVisible ? (
          <span className="text-sm text-muted-foreground">
            {t("notVisible")}
          </span>
        ) : verified ? (
          <span className="inline-flex min-h-9 items-center gap-2 text-sm font-medium text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 size={17} />
            {t("verified")}
          </span>
        ) : email ? (
          <Button disabled={busy} onClick={onSend}>
            <Mail />
            {t("sendVerification")}
          </Button>
        ) : (
          <span className="text-sm text-muted-foreground">
            {t("notConfigured")}
          </span>
        )}
      </header>
    </section>
  )
}

function PasskeySection({
  passkeys,
  error,
  busy,
  onRegister,
  onRename,
  onDelete,
}: {
  passkeys: Passkey[]
  error: string
  busy: boolean
  onRegister: () => void
  onRename: (passkey: Passkey) => void
  onDelete: (passkey: Passkey) => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()

  return (
    <section className="mb-5 overflow-hidden rounded-md border bg-card">
      <header className="flex min-h-20 flex-wrap items-center justify-between gap-4 border-b px-5 py-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
            <Fingerprint size={20} />
          </span>
          <div>
            <h2 className="font-semibold">{t("passkeys")}</h2>
            <p className="mt-1 text-xs text-muted-foreground">
              {t("passkeyDescription")}
            </p>
          </div>
        </div>
        <Button disabled={busy || Boolean(error)} onClick={onRegister}>
          <Plus />
          {t("addPasskey")}
        </Button>
      </header>
      {passkeys.map((passkey) => (
        <article
          key={passkey.id}
          className="grid min-h-20 grid-cols-[40px_minmax(0,1fr)_36px_36px] items-center gap-3 border-b px-5 py-3 last:border-0"
        >
          <span className="grid size-10 place-items-center rounded-md bg-primary/10 text-primary">
            <KeyRound size={20} />
          </span>
          <div className="min-w-0">
            <strong className="block truncate text-sm">{passkey.name}</strong>
            <small className="mt-1 block text-xs text-muted-foreground">
              {passkey.last_used_at
                ? t("lastUsed", {
                    date: new Date(passkey.last_used_at).toLocaleString(locale),
                  })
                : t("addedAt", {
                    date: new Date(passkey.created_at).toLocaleString(locale),
                  })}
            </small>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={busy}
            onClick={() => onRename(passkey)}
            aria-label={t("renameNamedPasskey", { name: passkey.name })}
            title={t("rename")}
          >
            <Pencil />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={busy}
            onClick={() => onDelete(passkey)}
            aria-label={t("deleteNamedPasskey", { name: passkey.name })}
            title={t("delete")}
          >
            <Trash2 className="text-destructive" />
          </Button>
        </article>
      ))}
      {error && (
        <div className="px-5 py-4">
          <Alert>{t("passkeyUnavailable")}</Alert>
        </div>
      )}
      {!error && passkeys.length === 0 && (
        <div className="grid min-h-32 place-items-center px-5 text-sm text-muted-foreground">
          {t("noPasskeys")}
        </div>
      )}
    </section>
  )
}

function PasskeyFlowDialog({
  state,
  busy,
  onOpenChange,
  onRegister,
  onRename,
  onDelete,
}: {
  state: PasskeyDialog
  busy: boolean
  onOpenChange: (open: boolean) => void
  onRegister: (event: FormEvent<HTMLFormElement>) => void
  onRename: (event: FormEvent<HTMLFormElement>) => void
  onDelete: (event: FormEvent<HTMLFormElement>) => void
}) {
  const t = useTranslations("iam")

  const formID = state ? `passkey-${state.type}-form` : "passkey-form"
  return (
    <Dialog open={state !== null} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {state?.type === "register"
              ? t("addPasskey")
              : state?.type === "rename"
                ? t("renamePasskey")
                : t("deletePasskey")}
          </DialogTitle>
          <DialogDescription>
            {state?.type === "register"
              ? t("registerPasskeyDescription")
              : state?.type === "rename"
                ? t("renamePasskeyDescription")
                : t("deletePasskeyDescription")}
          </DialogDescription>
        </DialogHeader>
        <form
          id={formID}
          className="grid gap-4"
          onSubmit={
            state?.type === "register"
              ? onRegister
              : state?.type === "rename"
                ? onRename
                : onDelete
          }
        >
          {state?.type !== "delete" && (
            <div className="grid gap-2">
              <Label htmlFor={`${formID}-name`}>{t("name")}</Label>
              <Input
                id={`${formID}-name`}
                name="name"
                defaultValue={
                  state?.type === "rename" ? state.passkey.name : ""
                }
                autoComplete="off"
                required
                maxLength={200}
              />
            </div>
          )}
          {state?.type !== "rename" && (
            <PasswordField
              id={`${formID}-password`}
              label={t("currentPassword")}
            />
          )}
        </form>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            {t("cancel")}
          </Button>
          <Button
            type="submit"
            form={formID}
            variant={state?.type === "delete" ? "destructive" : "default"}
            disabled={busy}
          >
            {busy
              ? t("processing")
              : state?.type === "delete"
                ? t("confirmDelete")
                : t("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MfaSection({
  status,
  busy,
  onEnable,
  onRegenerate,
  onDisable,
}: {
  status: MfaStatus | null
  busy: boolean
  onEnable: () => void
  onRegenerate: () => void
  onDisable: () => void
}) {
  const t = useTranslations("iam")

  return (
    <section className="mb-5 overflow-hidden rounded-md border bg-card">
      <header className="flex min-h-20 flex-wrap items-center justify-between gap-4 px-5 py-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
            <ShieldCheck size={20} />
          </span>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="font-semibold">{t("mfa")}</h2>
              {status?.totp_enabled && (
                <span className="rounded bg-success/10 px-2 py-0.5 text-xs font-semibold text-success">
                  {t("enabled")}
                </span>
              )}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {status?.totp_enabled
                ? t("remainingCodes", {
                    count: status.recovery_codes_remaining,
                  })
                : t("mfaDescription")}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {status?.totp_enabled ? (
            <>
              <Button variant="outline" disabled={busy} onClick={onRegenerate}>
                <RefreshCw />
                {t("renewRecoveryCodes")}
              </Button>
              <Button variant="destructive" disabled={busy} onClick={onDisable}>
                <ShieldOff />
                {t("disable")}
              </Button>
            </>
          ) : (
            <Button disabled={busy || status === null} onClick={onEnable}>
              <Smartphone />
              {t("enableAuthenticator")}
            </Button>
          )}
        </div>
      </header>
    </section>
  )
}

function SessionSection({
  sessions,
  busy,
  onLogoutAll,
  onRevoke,
}: {
  sessions: BrowserSession[]
  busy: boolean
  onLogoutAll: () => void
  onRevoke: (session: BrowserSession) => void
}) {
  const t = useTranslations("iam")

  return (
    <section className="overflow-hidden rounded-md border bg-card">
      <header className="flex min-h-20 flex-wrap items-center justify-between gap-4 border-b px-5 py-4">
        <div>
          <h2 className="font-semibold">{t("sessions")}</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("sessionsDescription")}
          </p>
        </div>
        <Button
          variant="destructive"
          disabled={busy || sessions.length === 0}
          onClick={onLogoutAll}
        >
          <LogOut />
          {t("logoutAll")}
        </Button>
      </header>
      <div>
        {sessions.map((session) => (
          <SessionRow
            key={session.id}
            session={session}
            busy={busy}
            onRevoke={() => onRevoke(session)}
          />
        ))}
        {sessions.length === 0 && (
          <div className="grid min-h-40 place-items-center text-sm text-muted-foreground">
            <div className="text-center">
              <ShieldCheck className="mx-auto mb-2 size-7" />
              {t("noSessions")}
            </div>
          </div>
        )}
      </div>
    </section>
  )
}

function PasswordDialog({
  open,
  busy,
  onOpenChange,
  onSubmit,
}: {
  open: boolean
  busy: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("changePassword")}</DialogTitle>
          <DialogDescription>{t("passwordDescription")}</DialogDescription>
        </DialogHeader>
        <form id="password-form" className="grid gap-4" onSubmit={onSubmit}>
          <PasswordField id="current-password" label={t("currentPassword")} />
          <div className="grid gap-2">
            <Label htmlFor="new-password">{t("newPassword")}</Label>
            <Input
              id="new-password"
              name="new_password"
              type="password"
              autoComplete="new-password"
              required
              minLength={12}
              maxLength={1024}
            />
          </div>
        </form>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            {t("cancel")}
          </Button>
          <Button type="submit" form="password-form" disabled={busy}>
            {busy ? t("saving") : t("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MfaFlowDialog({
  state,
  enrollment,
  recoveryCodes,
  busy,
  onOpenChange,
  onBegin,
  onConfirm,
  onRegenerate,
  onDisable,
}: {
  state: MfaDialog
  enrollment: MfaEnrollment | null
  recoveryCodes: string[]
  busy: boolean
  onOpenChange: (open: boolean) => void
  onBegin: (event: FormEvent<HTMLFormElement>) => void
  onConfirm: (event: FormEvent<HTMLFormElement>) => void
  onRegenerate: (event: FormEvent<HTMLFormElement>) => void
  onDisable: (event: FormEvent<HTMLFormElement>) => void
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={state !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-xl">
        {state === "enroll-password" && (
          <VerificationForm
            id="mfa-enroll-password-form"
            title={t("enableAuthenticator")}
            description={t("enrollDescription")}
            busy={busy}
            submitLabel={t("continue")}
            passwordOnly
            onSubmit={onBegin}
          />
        )}
        {state === "enroll-confirm" && enrollment && (
          <EnrollmentForm
            enrollment={enrollment}
            busy={busy}
            onSubmit={onConfirm}
          />
        )}
        {state === "recovery" && (
          <RecoveryCodes
            codes={recoveryCodes}
            onDone={() => onOpenChange(false)}
          />
        )}
        {state === "regenerate" && (
          <VerificationForm
            id="mfa-regenerate-form"
            title={t("renewRecoveryCodes")}
            description={t("renewCodesDescription")}
            busy={busy}
            submitLabel={t("generateCodes")}
            onSubmit={onRegenerate}
          />
        )}
        {state === "disable" && (
          <VerificationForm
            id="mfa-disable-form"
            title={t("disableMfa")}
            description={t("disableMfaDescription")}
            busy={busy}
            submitLabel={t("confirmDisable")}
            destructive
            onSubmit={onDisable}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function VerificationForm({
  id,
  title,
  description,
  busy,
  submitLabel,
  passwordOnly = false,
  destructive = false,
  onSubmit,
}: {
  id: string
  title: string
  description: string
  busy: boolean
  submitLabel: string
  passwordOnly?: boolean
  destructive?: boolean
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const t = useTranslations("iam")

  return (
    <>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
      </DialogHeader>
      <form id={id} className="grid gap-4" onSubmit={onSubmit}>
        <PasswordField id={`${id}-password`} label={t("currentPassword")} />
        {!passwordOnly && <CodeField id={`${id}-code`} />}
      </form>
      <DialogFooter>
        <Button
          type="submit"
          form={id}
          variant={destructive ? "destructive" : "default"}
          disabled={busy}
        >
          {busy ? t("processing") : submitLabel}
        </Button>
      </DialogFooter>
    </>
  )
}

function EnrollmentForm({
  enrollment,
  busy,
  onSubmit,
}: {
  enrollment: MfaEnrollment
  busy: boolean
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")
  const locale = useLocale()

  return (
    <>
      <DialogHeader>
        <DialogTitle>{t("linkAuthenticator")}</DialogTitle>
        <DialogDescription>{t("scanAuthenticator")}</DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 sm:grid-cols-[176px_minmax(0,1fr)] sm:items-center">
        <div className="mx-auto grid size-44 place-items-center rounded-md border bg-white p-3">
          <QRCodeSVG
            value={enrollment.provisioning_uri}
            title={t("totpQr", { app: appName })}
            size={152}
            level="M"
          />
        </div>
        <div className="min-w-0 space-y-2 text-sm">
          <p className="text-muted-foreground">{t("manualSecret")}</p>
          <code className="block rounded-md border bg-muted px-3 py-2 font-mono text-xs break-all">
            {enrollment.secret}
          </code>
          <p className="text-xs text-muted-foreground">
            {t("enrollmentExpiry", {
              time: new Date(enrollment.expires_at).toLocaleTimeString(locale),
            })}
          </p>
        </div>
      </div>
      <form id="mfa-confirm-form" onSubmit={onSubmit}>
        <CodeField id="mfa-confirm-code" totpOnly />
      </form>
      <DialogFooter>
        <Button type="submit" form="mfa-confirm-form" disabled={busy}>
          {busy ? t("confirming") : t("confirmEnable")}
        </Button>
      </DialogFooter>
    </>
  )
}

function RecoveryCodes({
  codes,
  onDone,
}: {
  codes: string[]
  onDone: () => void
}) {
  const t = useTranslations("iam")

  const content = `${codes.join("\n")}\n`
  const copy = () => void navigator.clipboard.writeText(content)
  const download = () => {
    const url = URL.createObjectURL(
      new Blob([content], { type: "text/plain;charset=utf-8" })
    )
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = "recovery-codes.txt"
    anchor.click()
    URL.revokeObjectURL(url)
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>{t("saveRecoveryCodes")}</DialogTitle>
        <DialogDescription>{t("recoveryDescription")}</DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-1 gap-2 rounded-md border bg-muted/40 p-3 font-mono text-sm sm:grid-cols-2">
        {codes.map((code) => (
          <code
            key={code}
            className="rounded bg-background px-2 py-1.5 text-center text-xs break-all"
          >
            {code}
          </code>
        ))}
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
      <DialogFooter>
        <Button type="button" onClick={onDone}>
          <CheckCircle2 />
          {t("safelySaved")}
        </Button>
      </DialogFooter>
    </>
  )
}

function PasswordField({ id, label }: { id: string; label: string }) {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        name="current_password"
        type="password"
        autoComplete="current-password"
        required
        maxLength={1024}
      />
    </div>
  )
}

function CodeField({
  id,
  totpOnly = false,
}: {
  id: string
  totpOnly?: boolean
}) {
  const t = useTranslations("iam")

  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>
        {totpOnly ? t("sixDigitCode") : t("authOrRecoveryCode")}
      </Label>
      <Input
        id={id}
        name="code"
        inputMode={totpOnly ? "numeric" : "text"}
        autoComplete="one-time-code"
        autoCapitalize="characters"
        required
        minLength={6}
        maxLength={64}
        placeholder={totpOnly ? "000000" : t("codePlaceholder")}
      />
    </div>
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

function SessionRow({
  session,
  busy,
  onRevoke,
}: {
  session: BrowserSession
  busy: boolean
  onRevoke: () => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()

  const mobile = /android|iphone|ipad|mobile/i.test(session.user_agent ?? "")
  return (
    <article className="grid min-h-20 grid-cols-[40px_minmax(0,1fr)_auto_36px] items-center gap-3 border-b px-5 py-3 last:border-0 max-sm:grid-cols-[40px_minmax(0,1fr)_36px]">
      <span className="grid size-10 place-items-center rounded-md bg-primary/10 text-primary">
        {mobile ? <Smartphone size={20} /> : <Laptop size={20} />}
      </span>
      <div className="min-w-0">
        <strong className="block truncate text-sm">
          {session.user_agent || t("unknownBrowser")}
        </strong>
        <small className="mt-1 block text-xs text-muted-foreground">
          {t("sessionActivity", {
            ip: session.ip_address || t("noIp"),
            date: new Date(session.last_seen_at).toLocaleString(locale),
          })}
        </small>
      </div>
      {session.current && (
        <span className="rounded bg-success/10 px-2 py-1 text-xs font-semibold text-success max-sm:col-start-2">
          {t("currentDevice")}
        </span>
      )}
      <Button
        type="button"
        variant="ghost"
        size="icon"
        disabled={busy}
        onClick={onRevoke}
        aria-label={t(
          session.current ? "revokeCurrentSession" : "revokeSession"
        )}
      >
        <Trash2 className="text-destructive" />
      </Button>
    </article>
  )
}
