import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ArrowLeft, Eye, EyeOff, Fingerprint, ShieldCheck } from "lucide-react"
import { Link, Navigate, useLocation } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  ApiError,
  businessCode,
  getTenant,
  iamApi,
  setTenant,
  type AuthnCapabilities,
  type Captcha,
  type LoginResult,
} from "../../lib/iam-api"
import { issueLoginCaptcha } from "../../lib/captcha"
import { appName, copyrightLine } from "../../lib/branding"
import {
  devAccountsForEntrance,
  devLoginCaptchaAnswer,
  prefillDevLoginAccount,
  type DevLoginAccount,
} from "../../lib/dev-login-accounts"
import { useAuth } from "../../components/auth"
import { SessionVerification } from "../../components/session-verification"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { AuthEntranceLinks } from "../../components/auth-entrance-links"
import {
  authEntrance,
  publicAuthEntrance,
  entrancePath,
  loginReturnPath,
} from "../../lib/auth-entrance"

export default function LoginPage() {
  const t = useTranslations("iam")

  const { status, login, verifyLoginMfa, loginWithPasskey } = useAuth()
  const location = useLocation()
  const entry = authEntrance(location.pathname.split("/")[1])
  const requested = loginReturnPath(
    entry,
    (location.state as { from?: string } | null)?.from
  )
  const [visible, setVisible] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(() =>
    new URLSearchParams(location.search).get("federation_error") ===
    "identity_unavailable"
      ? "externalIdentityProofRequired"
      : ""
  )
  const [challenge, setChallenge] = useState<LoginResult | null>(null)
  const [capabilities, setCapabilities] = useState<AuthnCapabilities | null>(
    null
  )
  const [captcha, setCaptcha] = useState<Captcha | null>(null)
  // Filled in development so a persona sign-in, and every browser audit that
  // drives this form, does not have to read the image. Empty in any build that
  // is not a development one, and useless against a backend with no configured
  // test answer.
  const [answer, setAnswer] = useState(devLoginCaptchaAnswer)
  const issueCaptcha = useCallback(() => {
    void issueLoginCaptcha().then(setCaptcha, () =>
      setError("captchaUnavailable")
    )
  }, [])
  // A challenge is consumed whether or not it was the thing that failed, so a
  // new one replaces it after every attempt, exactly as registration does.
  const refreshCaptcha = useCallback(() => {
    setCaptcha(null)
    setAnswer(devLoginCaptchaAnswer)
    issueCaptcha()
  }, [issueCaptcha])
  useEffect(() => {
    void iamApi
      .capabilities()
      .then(setCapabilities, () => setCapabilities(null))
  }, [])
  useEffect(issueCaptcha, [issueCaptcha])
  if (status === "loading" || status === "error") return <SessionVerification />
  if (status === "authenticated") return <Navigate to={requested} replace />

  const submitPassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const devTenantID = String(data.get("dev_tenant_id") ?? "")
    const previousTenantID = devTenantID ? getTenant() : ""
    if (!captcha) {
      setBusy(false)
      return
    }
    try {
      if (devTenantID) setTenant(devTenantID)
      const result = await login(
        String(data.get("login_name") ?? ""),
        String(data.get("password") ?? ""),
        `${window.location.origin}${requested}`,
        {
          type: captcha.type,
          id: captcha.captcha_id,
          answer,
        }
      )
      if (result.status === "mfa_required") {
        if (devTenantID) setTenant(previousTenantID)
        setChallenge(result)
      }
    } catch (cause) {
      if (devTenantID) setTenant(previousTenantID)
      setError(t(loginError(cause)))
      refreshCaptcha()
    } finally {
      setBusy(false)
    }
  }

  const submitMfa = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!challenge?.challenge_id) return
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    try {
      await verifyLoginMfa(
        challenge.challenge_id,
        String(data.get("code") ?? "").trim()
      )
    } catch (cause) {
      setError(
        cause instanceof ApiError && cause.status === 401
          ? t("invalidChallenge")
          : t("identityUnavailable")
      )
    } finally {
      setBusy(false)
    }
  }

  const submitPasskey = async () => {
    setBusy(true)
    setError("")
    try {
      await loginWithPasskey(`${window.location.origin}${requested}`)
    } catch (cause) {
      setError(
        cause instanceof DOMException && cause.name === "NotAllowedError"
          ? t("passkeyIncomplete")
          : cause instanceof ApiError && cause.status === 401
            ? t("passkeyInvalid")
            : (cause as Error).message || t("identityUnavailable")
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
          <AuthEntranceLinks entry={entry} />
          {challenge ? (
            <MfaForm
              challenge={challenge}
              busy={busy}
              error={error}
              onSubmit={submitMfa}
              onBack={() => {
                setChallenge(null)
                setError("")
              }}
            />
          ) : (
            <PasswordForm
              key={entry}
              busy={busy}
              error={error}
              visible={visible}
              onVisibleChange={() => setVisible((value) => !value)}
              onSubmit={submitPassword}
              onPasskey={() => void submitPasskey()}
              capabilities={capabilities}
              captcha={captcha}
              captchaAnswer={answer}
              onCaptchaAnswerChange={setAnswer}
              onRefreshCaptcha={refreshCaptcha}
            />
          )}
        </div>
        <p className="mt-6 text-center text-xs text-muted-foreground">
          {copyrightLine()}
        </p>
      </main>
    </div>
  )
}

function BrandHeader({ mfa = false }: { mfa?: boolean }) {
  const t = useTranslations("iam")
  const location = useLocation()
  const entry = authEntrance(location.pathname.split("/")[1])

  return (
    <div className="space-y-2.5">
      <div className="flex items-center gap-2">
        <span className="grid size-7 place-items-center rounded-md bg-primary text-primary-foreground">
          <ShieldCheck className="size-4" />
        </span>
        <span className="text-sm font-medium text-muted-foreground">
          {appName}
        </span>
      </div>
      <h1 id="login-title" className="text-xl font-semibold tracking-tight">
        {mfa
          ? t("completeVerification")
          : entry
            ? t(`authLogin_${entry}`)
            : t("welcomeBack")}
      </h1>
      <p className="text-sm text-muted-foreground">
        {mfa ? t("enterMfaCode") : t("signInConsole")}
      </p>
    </div>
  )
}

function PasswordForm({
  busy,
  error,
  visible,
  onVisibleChange,
  onSubmit,
  onPasskey,
  capabilities,
  captcha,
  captchaAnswer,
  onCaptchaAnswerChange,
  onRefreshCaptcha,
}: {
  busy: boolean
  error: string
  visible: boolean
  onVisibleChange: () => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onPasskey: () => void
  capabilities: AuthnCapabilities | null
  captcha: Captcha | null
  captchaAnswer: string
  onCaptchaAnswerChange: (value: string) => void
  onRefreshCaptcha: () => void
}) {
  const t = useTranslations("iam")
  const location = useLocation()
  const entry = authEntrance(location.pathname.split("/")[1])
  const selfService = !entry || publicAuthEntrance(entry) !== null
  const accounts = devAccountsForEntrance(entry)
  const defaultAccount = prefillDevLoginAccount ? accounts[0] : undefined
  const [selectedAccount, setSelectedAccount] = useState(
    defaultAccount?.id ?? ""
  )
  const [loginName, setLoginName] = useState(defaultAccount?.loginName ?? "")
  const [password, setPassword] = useState(defaultAccount?.password ?? "")
  const [tenantID, setTenantID] = useState(defaultAccount?.tenantID ?? "")
  const [platformID, setPlatformID] = useState(defaultAccount?.platformID ?? "")

  const selectAccount = (id: string) => {
    const account = accounts.find((candidate) => candidate.id === id)
    if (!account) return
    setSelectedAccount(account.id)
    setLoginName(account.loginName)
    setPassword(account.password)
    setTenantID(account.tenantID ?? "")
    setPlatformID(account.platformID ?? "")
  }

  const editCredential = (setter: (value: string) => void, value: string) => {
    setSelectedAccount("")
    setTenantID("")
    setPlatformID("")
    setter(value)
  }

  return (
    <form
      className="space-y-5"
      onSubmit={onSubmit}
      aria-labelledby="login-title"
    >
      <input type="hidden" name="dev_tenant_id" value={tenantID} />
      <input type="hidden" name="dev_platform_id" value={platformID} />
      <BrandHeader />
      {accounts.length > 0 && (
        <div className="space-y-2 rounded-md border border-dashed bg-muted/40 p-3">
          <Label id="dev-login-account-label">{t("devLoginAccount")}</Label>
          <SimpleSelect
            aria-labelledby="dev-login-account-label"
            value={selectedAccount}
            onValueChange={selectAccount}
            placeholder={t("devLoginCustomAccount")}
            options={accounts.map((account: DevLoginAccount) => ({
              value: account.id,
              label: (
                <span className="block truncate text-start text-xs">
                  {account.role} · {account.loginName}
                </span>
              ),
            }))}
          />
          <p className="text-xs text-muted-foreground">
            {t("devLoginAccountHint")}
          </p>
        </div>
      )}
      <div className="space-y-2">
        <Label htmlFor="login-name">{t("loginName")}</Label>
        <Input
          id="login-name"
          name="login_name"
          autoComplete="username"
          autoFocus
          value={loginName}
          onChange={(event) => editCredential(setLoginName, event.target.value)}
          required
          maxLength={200}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="password">{t("password")}</Label>
        <div className="relative">
          <Input
            id="password"
            name="password"
            type={visible ? "text" : "password"}
            value={password}
            onChange={(event) =>
              editCredential(setPassword, event.target.value)
            }
            className="pr-10"
            autoComplete="current-password"
            required
            maxLength={1024}
          />
          <button
            type="button"
            className="absolute top-1 right-1 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
            onClick={onVisibleChange}
            aria-label={visible ? t("hidePassword") : t("showPassword")}
          >
            {visible ? (
              <EyeOff className="size-4" />
            ) : (
              <Eye className="size-4" />
            )}
          </button>
        </div>
      </div>
      <div className="space-y-2">
        <Label htmlFor="login-captcha">{t("captcha")}</Label>
        <div className="flex items-center gap-2">
          <Input
            id="login-captcha"
            name="captcha_answer"
            value={captchaAnswer}
            onChange={(event) => onCaptchaAnswerChange(event.target.value)}
            autoComplete="off"
            required
            maxLength={16}
            className="flex-1"
          />

          <button
            type="button"
            className="grid h-9 w-[108px] shrink-0 place-items-center overflow-hidden rounded-md border border-border/70 bg-muted/40"
            onClick={onRefreshCaptcha}
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
              <span className="text-xs text-muted-foreground">
                {t("loading")}
              </span>
            )}
          </button>
        </div>
      </div>
      <FormError error={error} />
      <Button type="submit" className="w-full" disabled={busy || !captcha}>
        {busy ? t("verifying") : t("signIn")}
      </Button>
      {capabilities?.passkey && (
        <>
          <div className="relative text-center text-xs text-muted-foreground before:absolute before:top-1/2 before:left-0 before:h-px before:w-full before:bg-border">
            <span className="relative bg-card px-2">{t("or")}</span>
          </div>
          <Button
            type="button"
            variant="outline"
            className="w-full"
            disabled={busy}
            onClick={onPasskey}
          >
            <Fingerprint />
            {t("usePasskey")}
          </Button>
        </>
      )}
      {selfService &&
        (capabilities?.password_recovery || capabilities?.registration) && (
          <div className="space-y-2 text-center text-sm">
            {capabilities?.password_recovery && (
              <p>
                <Link
                  className="font-medium text-primary hover:underline focus-visible:rounded-sm focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                  to={entrancePath(entry, "recover")}
                >
                  {t("forgotPassword")}
                </Link>
              </p>
            )}
            {capabilities?.registration && (
              <p className="text-muted-foreground">
                {t("noAccount")}{" "}
                <Link
                  className="font-medium text-primary hover:underline"
                  to={entrancePath(entry, "register")}
                >
                  {t("createAccount")}
                </Link>
              </p>
            )}
          </div>
        )}
    </form>
  )
}

function MfaForm({
  challenge,
  busy,
  error,
  onSubmit,
  onBack,
}: {
  challenge: LoginResult
  busy: boolean
  error: string
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onBack: () => void
}) {
  const t = useTranslations("iam")
  const locale = useLocale()

  return (
    <form
      className="space-y-5"
      onSubmit={onSubmit}
      aria-labelledby="login-title"
    >
      <BrandHeader mfa />
      <div className="space-y-2">
        <Label htmlFor="mfa-code">{t("verificationOrRecovery")}</Label>
        <Input
          id="mfa-code"
          name="code"
          autoComplete="one-time-code"
          autoCapitalize="characters"
          autoFocus
          required
          minLength={6}
          maxLength={64}
          placeholder="000000"
        />
        {challenge.expires_at && (
          <p className="text-xs text-muted-foreground">
            {t("challengeExpiry", {
              time: new Date(challenge.expires_at).toLocaleTimeString(locale),
            })}
          </p>
        )}
      </div>
      <FormError error={error} />
      <div className="grid grid-cols-[auto_1fr] gap-2">
        <Button
          type="button"
          variant="outline"
          onClick={onBack}
          disabled={busy}
        >
          <ArrowLeft />
          {t("back")}
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? t("verifying") : t("continue")}
        </Button>
      </div>
    </form>
  )
}

function FormError({ error }: { error: string }) {
  const t = useTranslations("iam")
  return error ? (
    <p className="text-sm text-destructive" role="alert">
      {t.has(error) ? t(error) : error}
    </p>
  ) : null
}

function loginError(cause: unknown): string {
  if (!(cause instanceof ApiError)) return "identityUnavailable"
  if (cause.status === 401) return "invalidCredentials"
  // A rejected challenge arrives as 422 with its own code. Without this it
  // would read as "identity unavailable" and send the person away from a form
  // that only needed the image typed again.
  if (cause.businessCode === businessCode.captchaInvalid)
    return "invalidCaptcha"
  return "identityUnavailable"
}
