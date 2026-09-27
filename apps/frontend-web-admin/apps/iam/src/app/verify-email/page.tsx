import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useRef, useState } from "react"
import {
  ArrowLeft,
  CheckCircle2,
  LoaderCircle,
  MailCheck,
  ShieldAlert,
} from "lucide-react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { ApiError, iamApi } from "../../lib/iam-api"
import { appName, copyrightLine } from "../../lib/branding"
import {
  entranceLoginPath,
  entrancePath,
  publicAuthEntrance,
} from "../../lib/auth-entrance"

type VerificationState = "pending" | "success" | "invalid" | "unavailable"

export default function VerifyEmailPage() {
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
    token ? "pending" : "invalid"
  )
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true
    window.history.replaceState(null, "", entrancePath(entry, "verify-email"))
    if (!token) return
    void iamApi.completeEmailVerification(token).then(
      () => setState("success"),
      (cause: unknown) =>
        setState(
          cause instanceof ApiError && cause.status === 400
            ? "invalid"
            : "unavailable"
        )
    )
  }, [token, entry])

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
          </header>

          <VerificationResult state={state} />

          {state !== "pending" && (
            <Button className="mt-5 w-full" asChild>
              <Link to={entranceLoginPath(entry)}>
                <ArrowLeft />
                {t("backSignIn")}
              </Link>
            </Button>
          )}
        </div>
        <p className="mt-6 text-center text-xs text-muted-foreground">
          {copyrightLine()}
        </p>
      </main>
    </div>
  )
}

function VerificationResult({ state }: { state: VerificationState }) {
  const t = useTranslations("iam")

  const content = {
    pending: {
      icon: <LoaderCircle className="size-5 animate-spin" />,
      title: t("verifying"),
      description: t("verifyingEmail"),
    },
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
    unavailable: {
      icon: <ShieldAlert className="size-5" />,
      title: t("verificationUnavailable"),
      description: t("verificationUnavailableDescription"),
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
