import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useState } from "react"
import { ArrowLeft, KeyRound, Mail, ShieldCheck } from "lucide-react"
import { Link, useLocation } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { ApiError, iamApi } from "../../lib/iam-api"
import { useBranding, copyrightLine } from "../../lib/branding"
import { entranceLoginPath, publicAuthEntrance } from "../../lib/auth-entrance"

export default function RecoverPage() {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")

  const location = useLocation()
  const entry =
    publicAuthEntrance(location.pathname.split("/")[1]) ??
    publicAuthEntrance(new URLSearchParams(location.search).get("entry"))
  const token = new URLSearchParams(location.search).get("token") ?? ""
  const [busy, setBusy] = useState(false)
  const [complete, setComplete] = useState(false)
  const [error, setError] = useState("")

  const submit = async (values: Record<string, unknown>) => {
    setBusy(true)
    setError("")
    try {
      if (!token) {
        await iamApi.startPasswordRecovery(
          String(values.identifier ?? "").trim(),
          entry ?? undefined
        )
      } else {
        const password = String(values.new_password ?? "")
        if (password !== String(values.confirm_password ?? "")) {
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
            <AutoForm
              key={token ? "reset" : "request"}
              className="mt-5 md:grid-cols-1 [&>*]:col-span-1"
              onSubmit={submit}
              fields={
                token
                  ? {
                      new_password: {
                        name: t("newPassword"),
                        type: "password",
                        required: true,
                        config: {
                          id: "recovery-password",
                          minLength: 12,
                          maxLength: 1024,
                          autoFocus: true,
                          autoComplete: "new-password",
                        },
                      },
                      confirm_password: {
                        name: t("confirmNewPassword"),
                        type: "password",
                        required: true,
                        config: {
                          id: "recovery-confirm",
                          minLength: 12,
                          maxLength: 1024,
                          autoComplete: "new-password",
                        },
                      },
                    }
                  : {
                      identifier: {
                        name: t("accountOrEmail"),
                        type: "text",
                        required: true,
                        config: {
                          id: "recovery-identifier",
                          maxLength: 320,
                          autoFocus: true,
                          autoComplete: "username",
                        },
                      },
                    }
              }
            >
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
            </AutoForm>
          )}

          <Button variant="ghost" className="mt-4 w-full" asChild>
            <Link to={entranceLoginPath(entry)}>
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
