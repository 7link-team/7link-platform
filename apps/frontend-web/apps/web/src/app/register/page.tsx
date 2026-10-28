import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useState } from "react"
import {
  ArrowLeft,
  Eye,
  EyeOff,
  MailCheck,
  RefreshCw,
  ShieldCheck,
} from "lucide-react"
import { Link, useLocation } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { ThemeModeButton } from "../../components/theme-mode-button"
import {
  ApiError,
  businessCode,
  iamApi,
  type AuthnCapabilities,
  type Captcha,
} from "../../lib/iam-api"
import { issueRegistrationCaptcha } from "../../lib/captcha"
import { Turnstile } from "../../components/turnstile"
import { useBranding, copyrightLine } from "../../lib/branding"
import { entranceLoginPath, publicAuthEntrance } from "../../lib/auth-entrance"

export default function RegisterPage() {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")
  const location = useLocation()
  const entry = publicAuthEntrance(location.pathname.split("/")[1])

  const [busy, setBusy] = useState(false)
  const [complete, setComplete] = useState(false)
  const [verificationRequired, setVerificationRequired] = useState(true)
  const [visible, setVisible] = useState(false)
  const [error, setError] = useState("")
  const [captcha, setCaptcha] = useState<Captcha | null>(null)
  const [answer, setAnswer] = useState("")
  const [capabilities, setCapabilities] = useState<AuthnCapabilities | null>(
    null
  )
  const [widget, setWidget] = useState(0)
  const [capabilitiesFailed, setCapabilitiesFailed] = useState(false)
  const [capabilitiesRevision, setCapabilitiesRevision] = useState(0)
  // Which challenge exists is the service's to say. Asking a deployment that
  // delegates to Cloudflare for a drawn one gets nothing, repeatedly.
  const delegated = capabilities?.captcha.provider === "turnstile"
  const verifyEmail = capabilities?.registration_email_verification ?? true

  useEffect(() => {
    let active = true
    void iamApi
      .capabilities()
      .then(next => { if (active) setCapabilities(next) }, () => { if (active) setCapabilitiesFailed(true) })
    return () => { active = false }
  }, [capabilitiesRevision])

  const loadCaptcha = () => {
    void issueRegistrationCaptcha().then(setCaptcha, () =>
      setError(t("captchaUnavailable"))
    )
  }

  const refreshCaptcha = () => {
    setAnswer("")
    if (delegated) {
      // The widget issues its own token; remounting is what asks for another.
      setWidget((generation) => generation + 1)
      return
    }
    setCaptcha(null)
    loadCaptcha()
  }

  useEffect(() => {
    if (!capabilities || capabilities.captcha.provider !== "image") return
    loadCaptcha()
    // loadCaptcha closes over the translator only, which the dependency below
    // already tracks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [capabilities, t])

  const submit = async (values: Record<string, unknown>) => {
    if (!delegated && !captcha) return
    setBusy(true)
    setError("")
    const password = String(values.password ?? "")
    if (password !== String(values.confirm_password ?? "")) {
      setError(t("passwordMismatch"))
      setBusy(false)
      return
    }
    try {
      const result = await iamApi.register({
        ...(entry ? { entry } : {}),
        ...(verifyEmail ? {} : { login_name: String(values.login_name ?? "").trim() }),
        ...(String(values.email ?? "").trim() ? { email: String(values.email).trim() } : {}),
        password,
        display_name: String(values.display_name ?? "").trim(),
        ...(delegated
          ? { captcha_type: "turnstile", captcha_id: "widget" }
          : { captcha_type: captcha!.type, captcha_id: captcha!.captcha_id }),
        captcha_answer: answer.trim(),
      })
      setVerificationRequired(result.verification_required ?? true)
      setComplete(true)
    } catch (cause) {
      setError(t(registrationError(cause)))
      // Each challenge is consumed by the attempt that used it, so a retry
      // needs a new one whether or not the captcha was what failed.
      refreshCaptcha()
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
              {t(
                entry === "tenant" ? "registerTenantAccount" : "createAccount"
              )}
            </h1>
            <p className="text-sm text-muted-foreground">
              {t(verifyEmail ? "verifyEmailToSignIn" : "registerWithAccount")}
            </p>
          </header>

          {complete ? (
            <div
              className="mt-5 flex items-start gap-3 rounded-md border border-primary/25 bg-primary/5 px-4 py-3 text-sm"
              role="status"
            >
              <MailCheck className="mt-0.5 size-5 shrink-0 text-primary" />
              <div>
                <p className="font-medium">{t(verificationRequired ? "checkEmail" : "registrationComplete")}</p>
                <p className="mt-1 text-muted-foreground">
                  {t(verificationRequired ? "registrationEmailQueued" : "registrationReadyToSignIn")}
                </p>
              </div>
            </div>
          ) : !capabilities ? (
            <div className="mt-5 space-y-3">
              <p className="text-sm" role="status">{t(capabilitiesFailed ? "identityUnavailable" : "loading")}</p>
              {capabilitiesFailed && <Button variant="outline" onClick={() => { setCapabilitiesFailed(false); setCapabilitiesRevision(value => value + 1) }}>{t("retry")}</Button>}
            </div>
          ) : !capabilities.registration ? (
            <p className="mt-5 text-sm" role="status">{t("registrationClosed")}</p>
          ) : (
            <AutoForm
              className="mt-5 md:grid-cols-1 [&>*]:col-span-1"
              onSubmit={submit}
              fields={{
                ...(!verifyEmail ? {
                  login_name: {
                    name: t("loginName"),
                    type: "text" as const,
                    required: true,
                    config: { id: "registration-login", autoComplete: "username", autoFocus: true, maxLength: 200 },
                  },
                } : {}),
                email: {
                  name: t("email"),
                  type: "email",
                  required: verifyEmail,
                  config: {
                    id: "registration-email",
                    autoComplete: "email",
                    autoFocus: verifyEmail,
                    maxLength: 320,
                  },
                },
                display_name: {
                  name: t("displayName"),
                  type: "text",
                  config: {
                    id: "registration-name",
                    autoComplete: "name",
                    maxLength: 128,
                  },
                },
                password: {
                  name: t("password"),
                  type: "password",
                  required: true,
                  config: {
                    id: "registration-password",
                    type: visible ? "text" : "password",
                    autoComplete: "new-password",
                    minLength: 12,
                    maxLength: 1024,
                    suffix: (
                      <button
                        type="button"
                        className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                        onClick={() => setVisible((v) => !v)}
                        aria-label={
                          visible ? t("hidePassword") : t("showPassword")
                        }
                      >
                        {visible ? (
                          <EyeOff className="size-4" />
                        ) : (
                          <Eye className="size-4" />
                        )}
                      </button>
                    ),
                  },
                },
                confirm_password: {
                  name: t("confirmPassword"),
                  type: "password",
                  required: true,
                  config: {
                    id: "registration-confirm",
                    type: visible ? "text" : "password",
                    autoComplete: "new-password",
                    minLength: 12,
                    maxLength: 1024,
                  },
                },
              }}
            >
              {delegated ? (
                <div className="space-y-2">
                  <Label>{t("captcha")}</Label>
                  <Turnstile
                    key={widget}
                    siteKey={capabilities?.captcha.site_key ?? ""}
                    onToken={setAnswer}
                    onUnavailable={() => setError(t("captchaUnavailable"))}
                  />
                </div>
              ) : (
                <div className="space-y-2">
                  <Label htmlFor="registration-captcha">{t("captcha")}</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="registration-captcha"
                      name="captcha_answer"
                      value={answer}
                      onChange={(event) => setAnswer(event.target.value)}
                      autoComplete="off"
                      required
                      maxLength={16}
                      className="flex-1"
                    />
                    <button
                      type="button"
                      className="grid h-9 w-[108px] shrink-0 place-items-center overflow-hidden rounded-md border border-border/70 bg-muted/40"
                      onClick={refreshCaptcha}
                      aria-label={t("refreshCaptcha")}
                      title={t("anotherCaptcha")}
                    >
                      {captcha ? (
                        <img
                          src={captcha.image_base64}
                          alt={t("captcha")}
                          className="h-full w-full object-contain"
                        />
                      ) : (
                        <RefreshCw className="size-4 animate-spin text-muted-foreground" />
                      )}
                    </button>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {t("captchaHint")}
                  </p>
                </div>
              )}
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button
                type="submit"
                className="w-full"
                disabled={busy || (delegated ? !answer.trim() : !captcha)}
              >
                {busy ? t("submitting") : t("createAccount")}
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

function registrationError(cause: unknown): string {
  if (!(cause instanceof ApiError)) return "identityUnavailable"
  if (cause.status === 403) return "registrationOrigin"
  // A rejected captcha and a rejected email share one status, so tell them
  // apart by code; otherwise a wrong captcha reads as a bad password forever.
  if (cause.businessCode === businessCode.captchaInvalid)
    return "invalidCaptcha"
  if (cause.status === 422) return "registrationInvalid"
  if (cause.status === 503) return "registrationClosed"
  return "identityUnavailable"
}
