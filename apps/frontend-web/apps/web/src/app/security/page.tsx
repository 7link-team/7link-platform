import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState } from "react"
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
import {
  AutoForm,
  type FieldConfig,
  type FieldRendererProps,
} from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
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

  const sendPhoneCode = () =>
    run(async () => {
      await iamApi.startPhoneVerification()
      setNotice(t("phoneQueued"))
    })

  const submitPhoneCode = (code: string) =>
    run(async () => {
      await iamApi.completePhoneVerification(code)
      setNotice(t("phoneVerified"))
      await refresh()
    })

  const changePassword = (data: Record<string, unknown>) => {
    return run(async () => {
      await iamApi.changePassword({
        current_password: String(data.current_password ?? ""),
        new_password: String(data.new_password ?? ""),
      })
      setPasswordOpen(false)
      setNotice(t("passwordUpdated"))
      await refresh()
    })
  }

  const beginEnrollment = (data: Record<string, unknown>) => {
    return run(async () => {
      const next = await iamApi.beginTotpEnrollment(
        String(data.current_password ?? "")
      )
      setEnrollment(next)
      setMfaDialog("enroll-confirm")
    })
  }

  const confirmEnrollment = (data: Record<string, unknown>) => {
    return run(async () => {
      const result = await iamApi.confirmTotpEnrollment(
        String(data.code ?? "").trim()
      )
      setEnrollment(null)
      setRecoveryCodes(result.recovery_codes)
      setMfaDialog("recovery")
      await refresh()
    })
  }

  const verifyMfaChange = (
    data: Record<string, unknown>,
    action: "regenerate" | "disable"
  ) => {
    const input = {
      current_password: String(data.current_password ?? ""),
      code: String(data.code ?? "").trim(),
    }
    return run(async () => {
      if (action === "regenerate") {
        const result = await iamApi.regenerateRecoveryCodes(input)
        setRecoveryCodes(result.recovery_codes)
        setMfaDialog("recovery")
      } else {
        await iamApi.disableTotp(input)
        setMfaDialog(null)
        setNotice(t("mfaDisabled"))
      }
      await refresh()
    })
  }

  const closeMfaDialog = (open: boolean) => {
    if (open) return
    if (mfaDialog === "recovery") setRecoveryCodes([])
    if (mfaDialog === "enroll-confirm") setEnrollment(null)
    setMfaDialog(null)
  }

  const registerPasskey = (data: Record<string, unknown>) => {
    return run(async () => {
      const options = await iamApi.beginPasskeyRegistration(
        String(data.current_password ?? "")
      )
      const credential = await createPasskeyCredential(options.options)
      await iamApi.finishPasskeyRegistration({
        challenge_id: options.challenge_id,
        name: String(data.name ?? "").trim(),
        credential,
      })
      setPasskeyDialog(null)
      setNotice(t("passkeyAdded"))
      await refresh()
    })
  }

  const renamePasskey = (data: Record<string, unknown>) => {
    if (passkeyDialog?.type !== "rename") return
    const id = passkeyDialog.passkey.id
    return run(async () => {
      await iamApi.renamePasskey(id, String(data.name ?? "").trim())
      setPasskeyDialog(null)
      setNotice(t("passkeyRenamed"))
      await refresh()
    })
  }

  const deletePasskey = (data: Record<string, unknown>) => {
    if (passkeyDialog?.type !== "delete") return
    const id = passkeyDialog.passkey.id
    return run(async () => {
      await iamApi.deletePasskey(id, String(data.current_password ?? ""))
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
          <PhoneSection
            session={session}
            busy={busy}
            onSend={sendPhoneCode}
            onSubmit={submitPhoneCode}
          />
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
            onRefresh={refresh}
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
            onRefresh={refresh}
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

/** Proving a number is two steps, unlike proving an address: a message arrives
 *  with a code and the code is typed back here, in the session that asked. */
function PhoneSection({
  session,
  busy,
  onSend,
  onSubmit,
}: {
  session: Session | null
  busy: boolean
  onSend: () => void
  onSubmit: (code: string) => void | Promise<void>
}) {
  const t = useTranslations("iam")
  const [code, setCode] = useState("")
  const updateCode = useCallback(
    (values: Record<string, unknown>) => setCode(String(values.code ?? "")),
    []
  )
  const [entering, setEntering] = useState(false)

  const phone = session?.phone ?? ""
  const verified = Boolean(session?.phone_verified)
  const visible = session?.phone_verified !== undefined
  return (
    <section className="mb-5 overflow-hidden rounded-md border bg-card">
      <header className="flex min-h-20 flex-wrap items-center justify-between gap-4 border-b px-5 py-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
            <Smartphone size={20} />
          </span>
          <div className="min-w-0">
            <h2 className="font-semibold">{t("phoneNumber")}</h2>
            <p className="mt-1 truncate text-sm text-muted-foreground">
              {phone || (visible ? t("noPhone") : t("notVisible"))}
            </p>
          </div>
        </div>
        {!visible ? (
          <span className="text-sm text-muted-foreground">
            {t("notVisible")}
          </span>
        ) : verified ? (
          <span className="inline-flex min-h-9 items-center gap-2 text-sm font-medium text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 size={17} />
            {t("verified")}
          </span>
        ) : phone ? (
          <Button
            disabled={busy}
            onClick={() => {
              setEntering(true)
              onSend()
            }}
          >
            <Smartphone />
            {t("sendPhoneCode")}
          </Button>
        ) : (
          <span className="text-sm text-muted-foreground">
            {t("notConfigured")}
          </span>
        )}
      </header>
      {entering && !verified && phone && (
        <AutoForm
          className="px-5 py-4"
          onChange={updateCode}
          fields={{
            code: {
              name: t("phoneCode"),
              type: "custom",
              component: PhoneCodeField,
              value: "",
              config: { id: "phone_code" },
            },
          }}
          onSubmit={(values) => {
            const nextCode = String(values.code ?? "")
            if (nextCode.length === 6) return onSubmit(nextCode)
          }}
        >
          <Button type="submit" disabled={busy || code.length !== 6}>
            {t("phoneCodeSubmit")}
          </Button>
        </AutoForm>
      )}
    </section>
  )
}

function PasskeySection({
  passkeys,
  error,
  busy,
  onRefresh,
  onRegister,
  onRename,
  onDelete,
}: {
  passkeys: Passkey[]
  error: string
  busy: boolean
  onRefresh: () => Promise<void>
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
      </header>
      <div className="flex h-80 min-h-0 flex-col">
        <AutoTable
          toolbar={
            <Button disabled={busy || Boolean(error)} onClick={onRegister}>
              <Plus />
              {t("addPasskey")}
            </Button>
          }
          emptyContent={error ? t("passkeyUnavailable") : t("noPasskeys")}
          data={passkeys}
          rowKey="id"
          searchable
          fill
          onRefresh={onRefresh}
          columns={[
            {
              field: "name",
              name: t("passkeys"),
              render: (_, passkey) => (
                <div className="flex items-center gap-3">
                  <KeyRound size={20} />{" "}
                  <div className="min-w-0">
                    <strong className="block truncate text-sm">
                      {passkey.name}
                    </strong>
                    <small className="mt-1 block text-xs text-muted-foreground">
                      {passkey.last_used_at
                        ? t("lastUsed", {
                            date: new Date(passkey.last_used_at).toLocaleString(
                              locale
                            ),
                          })
                        : t("addedAt", {
                            date: new Date(passkey.created_at).toLocaleString(
                              locale
                            ),
                          })}
                    </small>
                  </div>
                </div>
              ),
            },
            {
              field: "actions",
              type: "actions",
              name: t("actions"),
              render: (_, passkey) => (
                <div className="flex justify-end">
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
                </div>
              ),
            },
          ]}
        />
      </div>
      {error && (
        <div className="px-5 py-4">
          <Alert>{t("passkeyUnavailable")}</Alert>
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
  onRegister: (data: Record<string, unknown>) => void | Promise<void>
  onRename: (data: Record<string, unknown>) => void | Promise<void>
  onDelete: (data: Record<string, unknown>) => void | Promise<void>
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
        <AutoForm
          key={formID}
          id={formID}
          fields={{
            name: {
              name: t("name"),
              type: "text",
              show: state?.type !== "delete",
              value: state?.type === "rename" ? state.passkey.name : "",
              required: true,
              config: {
                id: `${formID}-name`,
                autoComplete: "off",
                maxLength: 200,
              },
            },
            current_password: {
              ...passwordField(`${formID}-password`, t("currentPassword")),
              show: state?.type !== "rename",
            },
          }}
          onSubmit={
            state?.type === "register"
              ? onRegister
              : state?.type === "rename"
                ? onRename
                : onDelete
          }
        />
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
            {/* A proven number can answer a challenge with no enrolment of its
                own: the number above is the enrolment. Said here so somebody
                does not think they have no second factor at all. */}
            {status?.sms_available && (
              <p className="mt-1 text-xs text-muted-foreground">
                {t("smsFactorAvailable")}
              </p>
            )}
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
  onRefresh,
  onLogoutAll,
  onRevoke,
}: {
  sessions: BrowserSession[]
  busy: boolean
  onRefresh: () => Promise<void>
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
      </header>
      <div className="flex h-80 min-h-0 flex-col">
        <AutoTable
          toolbar={
            <Button
              variant="destructive"
              disabled={busy || sessions.length === 0}
              onClick={onLogoutAll}
            >
              <LogOut />
              {t("logoutAll")}
            </Button>
          }
          data={sessions}
          rowKey="id"
          searchable
          fill
          onRefresh={onRefresh}
          emptyContent={
            <div className="py-6">
              <ShieldCheck className="mx-auto mb-2 size-7" />
              {t("noSessions")}
            </div>
          }
          columns={[
            {
              field: "user_agent",
              name: t("sessions"),
              render: (_, session) => (
                <div className="flex items-center gap-3">
                  <span className="text-primary">
                    {/android|iphone|ipad|mobile/i.test(
                      session.user_agent ?? ""
                    ) ? (
                      <Smartphone size={20} />
                    ) : (
                      <Laptop size={20} />
                    )}
                  </span>
                  <SessionDetails session={session} />
                </div>
              ),
            },
            {
              field: "current",
              name: t("currentDevice"),
              render: (_, session) =>
                session.current ? (
                  <span className="rounded bg-success/10 px-2 py-1 text-xs font-semibold text-success">
                    {t("currentDevice")}
                  </span>
                ) : null,
            },
            {
              field: "actions",
              type: "actions",
              name: t("actions"),
              render: (_, session) => (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  disabled={busy}
                  onClick={() => onRevoke(session)}
                  aria-label={t(
                    session.current ? "revokeCurrentSession" : "revokeSession"
                  )}
                >
                  <Trash2 className="text-destructive" />
                </Button>
              ),
            },
          ]}
        />
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
  onSubmit: (data: Record<string, unknown>) => void | Promise<void>
}) {
  const t = useTranslations("iam")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("changePassword")}</DialogTitle>
          <DialogDescription>{t("passwordDescription")}</DialogDescription>
        </DialogHeader>
        <AutoForm
          id="password-form"
          onSubmit={onSubmit}
          fields={{
            current_password: passwordField(
              "current-password",
              t("currentPassword")
            ),
            new_password: {
              name: t("newPassword"),
              type: "password",
              required: true,
              config: {
                id: "new-password",
                type: "password",
                autoComplete: "new-password",
                minLength: 12,
                maxLength: 1024,
              },
            },
          }}
        />
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
  onBegin: (data: Record<string, unknown>) => void | Promise<void>
  onConfirm: (data: Record<string, unknown>) => void | Promise<void>
  onRegenerate: (data: Record<string, unknown>) => void | Promise<void>
  onDisable: (data: Record<string, unknown>) => void | Promise<void>
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
  onSubmit: (data: Record<string, unknown>) => void | Promise<void>
}) {
  const t = useTranslations("iam")

  return (
    <>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
      </DialogHeader>
      <AutoForm
        id={id}
        onSubmit={onSubmit}
        fields={{
          current_password: passwordField(
            `${id}-password`,
            t("currentPassword")
          ),
          code: {
            ...codeField(
              `${id}-code`,
              t("authOrRecoveryCode"),
              t("codePlaceholder")
            ),
            show: !passwordOnly,
          },
        }}
      />
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
  onSubmit: (data: Record<string, unknown>) => void | Promise<void>
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
      <AutoForm
        id="mfa-confirm-form"
        onSubmit={onSubmit}
        fields={{
          code: codeField(
            "mfa-confirm-code",
            t("sixDigitCode"),
            "000000",
            true
          ),
        }}
      />
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

function passwordField(id: string, label: string): FieldConfig {
  return {
    name: label,
    type: "password",
    required: true,
    config: {
      id,
      type: "password",
      autoComplete: "current-password",
      maxLength: 1024,
    },
  }
}

function codeField(
  id: string,
  label: string,
  placeholder: string,
  totpOnly = false
): FieldConfig {
  return {
    name: label,
    type: "text",
    required: true,
    config: {
      id,
      inputMode: totpOnly ? "numeric" : "text",
      autoComplete: "one-time-code",
      autoCapitalize: "characters",
      minLength: 6,
      maxLength: 64,
      placeholder,
    },
  }
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

function SessionDetails({ session }: { session: BrowserSession }) {
  const t = useTranslations("iam")
  const locale = useLocale()
  return (
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
  )
}

function PhoneCodeField({
  value,
  onChange,
  config,
  disabled,
  labelId,
  describedBy,
}: FieldRendererProps) {
  return (
    <Input
      id={config.id}
      name="code"
      inputMode="numeric"
      autoComplete="one-time-code"
      maxLength={6}
      className="w-32 font-mono tracking-[0.3em]"
      value={String(value ?? "")}
      disabled={disabled}
      aria-labelledby={labelId}
      aria-describedby={describedBy}
      onChange={(event) =>
        onChange(event.target.value.replace(/\D/g, "").slice(0, 6))
      }
    />
  )
}
