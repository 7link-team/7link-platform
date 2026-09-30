import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useRef, useState, type FormEvent } from "react"
import {
  ArrowLeft,
  CheckCircle2,
  LoaderCircle,
  MailCheck,
  ShieldAlert,
} from "lucide-react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { ApiError, iamApi } from "../../lib/iam-api"
import { useBranding, copyrightLine } from "../../lib/branding"
import {
  entranceLoginPath,
  entrancePath,
  publicAuthEntrance,
} from "../../lib/auth-entrance"

type VerificationState = "form" | "success" | "invalid"

export default function VerifyEmailPage() {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")
  const [entry] = useState(
    () =>
      publicAuthEntrance(window.location.pathname.split("/")[1]) ??
      publicAuthEntrance(
        new URLSearchParams(window.location.search).get("entry")
      )
  )

  const [token] = useState(
    () => new URLSearchParams(window.location.search).get("token") ?? ""
  )
  const [state, setState] = useState<VerificationState>(() =>
    token ? "form" : "invalid"
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true
    window.history.replaceState(null, "", entrancePath(entry, "verify-email"))
  }, [entry])

  // The link proves the mailbox; activating a new registration also needs the
  // password chosen for it. A refusal leaves the link usable, so the form stays.
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const password = String(
      new FormData(event.currentTarget).get("password") ?? ""
    )
    try {
      await iamApi.completeEmailVerification(token, password || undefined)
      setState("success")
    } catch (cause) {
      setError(
        cause instanceof ApiError && cause.status === 400
          ? t("verificationRejectedDescription")
          : t("verificationUnavailableDescription")
      )
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
                <MailCheck className="size-4" />
              </span>
              <span className="text-sm font-medium text-muted-foreground">
                {appName}
              </span>
            </div>
            <h1 className="text-xl font-semibold">{t("verifyEmail")}</h1>
            {state === "form" && (
              <p className="text-sm text-muted-foreground">
                {t("verifyEmailPasswordDescription")}
              </p>
            )}
          </header>

          {state === "form" ? (
            <form className="mt-5 space-y-5" onSubmit={submit}>
              <div className="space-y-2">
                <Label htmlFor="verification-password">
                  {t("registrationPassword")}
                </Label>
                <Input
                  id="verification-password"
                  name="password"
                  type="password"
                  autoComplete="current-password"
                  maxLength={1024}
                />
              </div>
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button type="submit" className="w-full" disabled={busy}>
                {busy ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <MailCheck />
                )}
                {busy ? t("verifying") : t("verifyEmail")}
              </Button>
            </form>
          ) : (
            <VerificationResult state={state} />
          )}

          <Button
            variant={state === "form" ? "ghost" : "default"}
            className={state === "form" ? "mt-4 w-full" : "mt-5 w-full"}
            asChild
          >
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

function VerificationResult({
  state,
}: {
  state: Exclude<VerificationState, "form">
}) {
  const t = useTranslations("iam")

  const content = {
    success: {
      icon: <CheckCircle2 className="size-5" />,
      title: t("emailVerified"),
      description: t("emailVerifiedDescription"),
    },
    invalid: {
      icon: <ShieldAlert className="size-5" />,
      title: t("verificationLinkInvalid"),
      description: t("verificationLinkInvalidDescription"),
    },
  }[state]

  return (
    <div
      className="mt-5 flex items-start gap-3 rounded-md border border-border bg-muted/40 px-4 py-3"
      role="status"
      aria-live="polite"
    >
      <span className="mt-0.5 text-primary">{content.icon}</span>
      <div>
        <p className="text-sm font-medium">{content.title}</p>
        <p className="mt-1 text-sm text-muted-foreground">
          {content.description}
        </p>
      </div>
    </div>
  )
}
