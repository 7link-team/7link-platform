import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useState, type FormEvent } from "react"
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
import { ApiError, businessCode, iamApi, type Captcha } from "../../lib/iam-api"
import { issueRegistrationCaptcha } from "../../lib/captcha"
import { useBranding, copyrightLine } from "../../lib/branding"
import { entranceLoginPath, publicAuthEntrance } from "../../lib/auth-entrance"

export default function RegisterPage() {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")
  const location = useLocation()
  const entry = publicAuthEntrance(location.pathname.split("/")[1])

  const [busy, setBusy] = useState(false)
  const [complete, setComplete] = useState(false)
  const [visible, setVisible] = useState(false)
  const [error, setError] = useState("")
  const [captcha, setCaptcha] = useState<Captcha | null>(null)
  const [answer, setAnswer] = useState("")

  const loadCaptcha = () => {
    void issueRegistrationCaptcha().then(setCaptcha, () =>
      setError(t("captchaUnavailable"))
    )
  }

  const refreshCaptcha = () => {
    setCaptcha(null)
    setAnswer("")
    loadCaptcha()
  }

  useEffect(loadCaptcha, [t])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!captcha) return
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const password = String(data.get("password") ?? "")
    if (password !== String(data.get("confirm_password") ?? "")) {
      setError(t("passwordMismatch"))
      setBusy(false)
      return
    }
    try {
      await iamApi.register({
        ...(entry ? { entry } : {}),
        email: String(data.get("email") ?? "").trim(),
        password,
        display_name: String(data.get("display_name") ?? "").trim(),
        captcha_type: captcha.type,
        captcha_id: captcha.captcha_id,
        captcha_answer: answer.trim(),
      })
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
              {t("verifyEmailToSignIn")}
            </p>
          </header>

          {complete ? (
            <div
              className="mt-5 flex items-start gap-3 rounded-md border border-primary/25 bg-primary/5 px-4 py-3 text-sm"
              role="status"
            >
              <MailCheck className="mt-0.5 size-5 shrink-0 text-primary" />
              <div>
                <p className="font-medium">{t("checkEmail")}</p>
                <p className="mt-1 text-muted-foreground">
                  {t("registrationEmailQueued")}
                </p>
              </div>
            </div>
          ) : (
            <form className="mt-5 space-y-5" onSubmit={submit}>
              <div className="space-y-2">
                <Label htmlFor="registration-email">{t("email")}</Label>
                <Input
                  id="registration-email"
                  name="email"
                  type="email"
                  autoComplete="email"
                  autoFocus
                  required
                  maxLength={320}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="registration-name">{t("displayName")}</Label>
                <Input
                  id="registration-name"
                  name="display_name"
                  autoComplete="name"
                  maxLength={128}
                />
              </div>
              <PasswordField
                id="registration-password"
                name="password"
                label={t("password")}
                visible={visible}
                onVisible={() => setVisible((value) => !value)}
              />
              <PasswordField
                id="registration-confirm"
                name="confirm_password"
                label={t("confirmPassword")}
                visible={visible}
              />
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
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button
                type="submit"
                className="w-full"
                disabled={busy || !captcha}
              >
                {busy ? t("submitting") : t("createAccount")}
              </Button>
            </form>
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

function PasswordField({
  id,
  name,
  label,
  visible,
  onVisible,
}: {
  id: string
  name: string
  label: string
  visible: boolean
  onVisible?: () => void
}) {
  const t = useTranslations("iam")

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <div className="relative">
        <Input
          id={id}
          name={name}
          type={visible ? "text" : "password"}
          className={onVisible ? "pr-10" : undefined}
          autoComplete="new-password"
          required
          minLength={12}
          maxLength={1024}
        />
        {onVisible && (
          <button
            type="button"
            className="absolute top-1 right-1 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
            onClick={onVisible}
            aria-label={visible ? t("hidePassword") : t("showPassword")}
          >
            {visible ? (
              <EyeOff className="size-4" />
            ) : (
              <Eye className="size-4" />
            )}
          </button>
        )}
      </div>
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
