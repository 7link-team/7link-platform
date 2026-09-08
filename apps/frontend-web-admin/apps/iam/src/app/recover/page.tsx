import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useState, type FormEvent } from "react"
import { ArrowLeft, KeyRound, Mail, ShieldCheck } from "lucide-react"
import { Link, useLocation } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { ApiError, iamApi } from "../../lib/iam-api"
import { appName, copyrightLine } from "../../lib/branding"

export default function RecoverPage() {
  const t = useTranslations("iam")

  const location = useLocation()
  const token = new URLSearchParams(location.search).get("token") ?? ""
  const [busy, setBusy] = useState(false)
  const [complete, setComplete] = useState(false)
  const [error, setError] = useState("")

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      if (!token) {
        await iamApi.startPasswordRecovery(
          String(data.get("identifier") ?? "").trim()
        )
      } else {
        const password = String(data.get("new_password") ?? "")
        if (password !== String(data.get("confirm_password") ?? "")) {
          setError(t("passwordMismatch"))
          return
        }
        await iamApi.completePasswordRecovery({
          token,
          new_password: password,
        })
      }
      setComplete(true)
    } catch (cause) {
      setError(t(recoveryError(cause, Boolean(token))))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-svh flex-col items-center justify-center bg-muted/30 px-4 py-10">
      <div className="absolute top-4 right-4">
        <ThemeModeButton />
      </div>
      <main className="w-full max-w-[400px]">
        <div className="rounded-xl border border-border/70 bg-card p-8 shadow-lg">
          <header className="space-y-2.5">
            <div className="flex items-center gap-2">
              <span className="grid size-7 place-items-center rounded-md bg-primary text-primary-foreground">
                <ShieldCheck className="size-4" />
              </span>
              <span className="text-sm font-medium text-muted-foreground">
                {appName}
              </span>
            </div>
            <h1 className="text-xl font-semibold tracking-tight">
              {token ? t("setNewPassword") : t("recoverAccount")}
            </h1>
            <p className="text-sm text-muted-foreground">
              {token
                ? t("recoveryResetDescription")
                : t("recoveryDescriptionLogin")}
            </p>
          </header>

          {complete ? (
            <RecoveryComplete reset={Boolean(token)} />
          ) : (
            <form className="mt-5 space-y-5" onSubmit={submit}>
              {token ? <ResetFields /> : <IdentifierField />}
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button type="submit" className="w-full" disabled={busy}>
                {token ? <KeyRound /> : <Mail />}
                {busy
                  ? t("submitting")
                  : token
                    ? t("updatePassword")
                    : t("sendRecoveryEmail")}
              </Button>
            </form>
          )}

          <Button variant="ghost" className="mt-4 w-full" asChild>
            <Link to="/login">
              <ArrowLeft />
              {t("backSignIn")}
            </Link>
          </Button>
        </div>
        <p className="mt-6 text-center text-xs text-muted-foreground">
          {copyrightLine()}
        </p>
      </main>
    </div>
  )
}

function IdentifierField() {
  const t = useTranslations("iam")

  return (
    <div className="space-y-2">
      <Label htmlFor="recovery-identifier">{t("accountOrEmail")}</Label>
      <Input
        id="recovery-identifier"
        name="identifier"
        autoComplete="username"
        autoFocus
        required
        maxLength={320}
      />
    </div>
  )
}

function ResetFields() {
  const t = useTranslations("iam")

  return (
    <>
      <div className="space-y-2">
        <Label htmlFor="recovery-password">{t("newPassword")}</Label>
        <Input
          id="recovery-password"
          name="new_password"
          type="password"
          autoComplete="new-password"
          autoFocus
          required
          minLength={12}
          maxLength={1024}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="recovery-confirm">{t("confirmNewPassword")}</Label>
        <Input
          id="recovery-confirm"
          name="confirm_password"
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          maxLength={1024}
        />
      </div>
    </>
  )
}

function RecoveryComplete({ reset }: { reset: boolean }) {
  const t = useTranslations("iam")

  return (
    <div
      className="mt-5 rounded-md border border-primary/25 bg-primary/5 px-4 py-3 text-sm"
      role="status"
    >
      {reset ? t("recoveryPasswordUpdated") : t("recoveryEmailQueued")}
    </div>
  )
}

function recoveryError(cause: unknown, completing: boolean): string {
  if (!(cause instanceof ApiError)) return "identityUnavailable"
  if (cause.status === 409) return "passwordRecentlyUsed"
  if (completing && cause.status === 400) return "recoveryLinkInvalid"
  if (cause.status === 422) return "accountEmailRequired"
  return "identityUnavailable"
}
