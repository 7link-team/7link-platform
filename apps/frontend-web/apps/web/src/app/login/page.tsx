import { useTranslations, useLocale } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import {
  ArrowLeft,
  Eye,
  EyeOff,
  Fingerprint,
  ShieldCheck,
  Smartphone,
} from "lucide-react"
import { Link, Navigate, useLocation } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { SimpleSelect } from "@workspace/ui/components/select"
import {
  ApiError,
  getTenant,
  iamApi,
  setTenant,
  type AuthnCapabilities,
  type Captcha,
  type CaptchaCapability,
  type LoginResult,
} from "../../lib/iam-api"
import { issueLoginCaptcha } from "../../lib/captcha"
import { Turnstile } from "../../components/turnstile"
import { useBranding, copyrightLine } from "../../lib/branding"
import {
  devAccountsForEntrance,
  devLoginCaptchaAnswer,
  prefillDevLoginAccount,
  type DevLoginAccount,
} from "../../lib/dev-login-accounts"
import { loginError, useAuth } from "../../components/auth"
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

  const {
    status,
    login,
    requestPhoneLoginCode,
    loginWithPhoneCode,
    requestMfaPhoneCode,
    verifyLoginMfa,
    loginWithPasskey,
  } = useAuth()
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
  // mode is which way in is being offered; phone is the number a code was sent
  // to, which is also what says the page has moved to the code step.
  const [mode, setMode] = useState<"password" | "phone">("password")
  const [phone, setPhone] = useState("")
  const [mfaCodeSent, setMfaCodeSent] = useState(false)
  const [capabilities, setCapabilities] = useState<AuthnCapabilities | null>(
    null
  )
  const [captcha, setCaptcha] = useState<Captcha | null>(null)
  const [widget, setWidget] = useState(0)
  // Filled in development so a persona sign-in, and every browser audit that
  // drives this form, does not have to read the image. Empty in any build that
  // is not a development one, and useless against a backend with no configured
  // test answer.
  const [answer, setAnswer] = useState(devLoginCaptchaAnswer)
  const delegatedCaptcha = capabilities?.captcha.provider === "turnstile"
  const issueCaptcha = useCallback(() => {
    void issueLoginCaptcha().then(setCaptcha, () =>
      setError("captchaUnavailable")
    )
  }, [])
  // A challenge is consumed whether or not it was the thing that failed, so a
  // new one replaces it after every attempt, exactly as registration does.
  const refreshCaptcha = useCallback(() => {
    setAnswer(devLoginCaptchaAnswer)
    if (delegatedCaptcha) {
      // The widget issues its own token and replaces it when it expires; the
      // remount is what asks Cloudflare for another.
      setWidget((generation) => generation + 1)
      return
    }
    setCaptcha(null)
    issueCaptcha()
  }, [delegatedCaptcha, issueCaptcha])
  useEffect(() => {
    void iamApi
      .capabilities()
      .then(setCapabilities, () => setCapabilities(null))
  }, [])
  // Wait for the login options before asking: which challenge exists is the
  // service's to say, and a Turnstile deployment draws none.
  useEffect(() => {
    if (!capabilities || capabilities.captcha.provider !== "image") return
    issueCaptcha()
  }, [capabilities, issueCaptcha])
  if (status === "loading" || status === "error") return <SessionVerification />
  if (status === "authenticated") return <Navigate to={requested} replace />

  const submitPassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const devTenantID = String(data.get("dev_tenant_id") ?? "")
    const previousTenantID = devTenantID ? getTenant() : ""
    if (!delegatedCaptcha && !captcha) {
      setBusy(false)
      return
    }
    try {
      if (devTenantID) setTenant(devTenantID)
      const result = await login(
        String(data.get("login_name") ?? ""),
        String(data.get("password") ?? ""),
        `${window.location.origin}${requested}`,
        delegatedCaptcha
          ? { type: "turnstile", id: "widget", answer }
          : { type: captcha!.type, id: captcha!.captcha_id, answer }
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

  // Asking for a code says nothing about whether the number reaches an account,
  // so the page always moves on to the code step. Anything else would tell
  // whoever typed the number whether it has one, and they are not always its
  // owner.
  const requestPhoneCode = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!delegatedCaptcha && !captcha) return
    const typed = String(
      new FormData(event.currentTarget).get("phone") ?? ""
    ).trim()
    if (!typed) return
    setBusy(true)
    setError("")
    try {
      await requestPhoneLoginCode(
        typed,
        delegatedCaptcha
          ? { type: "turnstile", id: "widget", answer }
          : { type: captcha!.type, id: captcha!.captcha_id, answer }
      )
      setPhone(typed)
    } catch (cause) {
      setError(t(loginError(cause)))
    } finally {
      refreshCaptcha()
      setBusy(false)
    }
  }

  const submitPhoneCode = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setBusy(true)
    setError("")
    const code = String(new FormData(event.currentTarget).get("code") ?? "").trim()
    try {
      const result = await loginWithPhoneCode(
        phone,
        code,
        `${window.location.origin}${requested}`
      )
      if (result.status === "mfa_required") setChallenge(result)
    } catch (cause) {
      setError(t(loginError(cause, "invalidPhoneCode")))
    } finally {
      setBusy(false)
    }
  }

  // A challenge that offers sms has a code to ask for. Asking says nothing about
  // the account; it only queues the message the challenge already implies.
  const sendMfaPhoneCode = () =>
    void (async () => {
      if (!challenge?.challenge_id) return
      setBusy(true)
      setError("")
      try {
        await requestMfaPhoneCode(challenge.challenge_id)
        setMfaCodeSent(true)
      } catch (cause) {
        setError(
          cause instanceof ApiError && cause.status === 409
            ? t("smsFactorUnavailable")
            : t("identityUnavailable")
        )
      } finally {
        setBusy(false)
      }
    })()

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
      setError(t(loginError(cause, "invalidChallenge")))
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
              codeSent={mfaCodeSent}
              onSendPhoneCode={sendMfaPhoneCode}
              onSubmit={submitMfa}
              onBack={() => {
                setChallenge(null)
                setMfaCodeSent(false)
                setError("")
              }}
            />
          ) : phone ? (
            <PhoneCodeForm
              phone={phone}
              busy={busy}
              error={error}
              onSubmit={submitPhoneCode}
              onBack={() => {
                setPhone("")
                setError("")
              }}
            />
          ) : mode === "phone" ? (
            <PhoneForm
              busy={busy}
              error={error}
              onSubmit={requestPhoneCode}
              onBack={() => {
                setMode("password")
                setError("")
              }}
              captcha={captcha}
              captchaProvider={capabilities?.captcha}
              captchaWidget={widget}
              captchaAnswer={answer}
              onCaptchaAnswerChange={setAnswer}
              onCaptchaUnavailable={() => setError(t("captchaUnavailable"))}
              onRefreshCaptcha={refreshCaptcha}
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
              captchaProvider={capabilities?.captcha}
              captchaWidget={widget}
              captchaAnswer={answer}
              onCaptchaAnswerChange={setAnswer}
              onCaptchaUnavailable={() => setError(t("captchaUnavailable"))}
              onRefreshCaptcha={refreshCaptcha}
              onPhone={
                capabilities?.phone_login
                  ? () => {
                      setMode("phone")
                      setError("")
                    }
                  : undefined
              }
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

function CaptchaField({
  captcha,
  provider,
  widget,
  answer,
  onAnswerChange,
  onUnavailable,
  onRefresh,
}: {
  captcha: Captcha | null
  provider: CaptchaCapability | undefined
  widget: number
  answer: string
  onAnswerChange: (value: string) => void
  onUnavailable: () => void
  onRefresh: () => void
}) {
  const t = useTranslations("iam")
  return (
    <>
      {provider?.provider === "turnstile" ? (
        <div className="space-y-2">
          <Label>{t("captcha")}</Label>
          {/* The widget carries its own token; the field below only exists so
              an automated run can hand over the configured test answer, and it
              is not rendered for anybody else. */}
          <Turnstile
            key={widget}
            siteKey={provider.site_key ?? ""}
            onToken={onAnswerChange}
            onUnavailable={onUnavailable}
          />
          {devLoginCaptchaAnswer !== "" && (
            <input type="hidden" name="captcha_answer" value={answer} />
          )}
        </div>
      ) : (
        <div className="space-y-2">
          <Label htmlFor="login-captcha">{t("captcha")}</Label>
          <div className="flex items-center gap-2">
            <Input
              id="login-captcha"
              name="captcha_answer"
              value={answer}
              onChange={(event) => onAnswerChange(event.target.value)}
              autoComplete="off"
              required
              maxLength={16}
              className="flex-1"
            />
            {/* The box is the input's height and the image's own proportions, so
                the characters arrive at the size they were drawn rather than
                squeezed. Selecting it asks for another, which is also the only
                way out of a challenge kind this form cannot draw. */}
            <button
              type="button"
              className="grid h-9 w-[108px] shrink-0 place-items-center overflow-hidden rounded-md border border-border/70 bg-muted/40"
              onClick={onRefresh}
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
      )}
    </>
  )
}

function PhoneForm({
  busy,
  error,
  onSubmit,
  onBack,
  captcha,
  captchaProvider,
  captchaWidget,
  captchaAnswer,
  onCaptchaAnswerChange,
  onCaptchaUnavailable,
  onRefreshCaptcha,
}: {
  busy: boolean
  error: string
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onBack: () => void
  captcha: Captcha | null
  captchaProvider: CaptchaCapability | undefined
  captchaWidget: number
  captchaAnswer: string
  onCaptchaAnswerChange: (value: string) => void
  onCaptchaUnavailable: () => void
  onRefreshCaptcha: () => void
}) {
  const t = useTranslations("iam")
  return (
    <form className="space-y-5" onSubmit={onSubmit}>
      <BrandHeader />
      <FormError error={error} />
      <div className="space-y-2">
        <Label htmlFor="phone">{t("phoneNumber")}</Label>
        <Input
          id="phone"
          name="phone"
          type="tel"
          autoComplete="tel"
          placeholder="+8613800138000"
          required
          disabled={busy}
        />
        <p className="text-xs text-muted-foreground">{t("phoneLoginHint")}</p>
      </div>
      <CaptchaField
        captcha={captcha}
        provider={captchaProvider}
        widget={captchaWidget}
        answer={captchaAnswer}
        onAnswerChange={onCaptchaAnswerChange}
        onUnavailable={onCaptchaUnavailable}
        onRefresh={onRefreshCaptcha}
      />
      <Button type="submit" className="w-full" disabled={busy}>
        {t("sendPhoneCode")}
      </Button>
      <Button
        type="button"
        variant="ghost"
        className="w-full"
        onClick={onBack}
        disabled={busy}
      >
        {t("usePasswordInstead")}
      </Button>
    </form>
  )
}

function PhoneCodeForm({
  phone,
  busy,
  error,
  onSubmit,
  onBack,
}: {
  phone: string
  busy: boolean
  error: string
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onBack: () => void
}) {
  const t = useTranslations("iam")
  return (
    <form className="space-y-5" onSubmit={onSubmit}>
      <BrandHeader />
      <FormError error={error} />
      <p className="text-sm text-muted-foreground">
        {t("phoneCodeSent", { phone })}
      </p>
      <div className="space-y-2">
        <Label htmlFor="code">{t("phoneCode")}</Label>
        <Input
          id="code"
          name="code"
          inputMode="numeric"
          autoComplete="one-time-code"
          maxLength={6}
          className="font-mono tracking-[0.3em]"
          required
          disabled={busy}
        />
      </div>
      <Button type="submit" className="w-full" disabled={busy}>
        {t("signIn")}
      </Button>
      <Button
        type="button"
        variant="ghost"
        className="w-full"
        onClick={onBack}
        disabled={busy}
      >
        {t("back")}
      </Button>
    </form>
  )
}

function BrandHeader({ mfa = false }: { mfa?: boolean }) {
  const { name: appName } = useBranding()
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
  onPhone,
  capabilities,
  captcha,
  captchaProvider,
  captchaWidget,
  captchaAnswer,
  onCaptchaAnswerChange,
  onCaptchaUnavailable,
  onRefreshCaptcha,
}: {
  busy: boolean
  error: string
  visible: boolean
  onVisibleChange: () => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onPasskey: () => void
  onPhone?: () => void
  capabilities: AuthnCapabilities | null
  captcha: Captcha | null
  captchaProvider: CaptchaCapability | undefined
  captchaWidget: number
  captchaAnswer: string
  onCaptchaAnswerChange: (value: string) => void
  onCaptchaUnavailable: () => void
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
      <CaptchaField
        captcha={captcha}
        provider={captchaProvider}
        widget={captchaWidget}
        answer={captchaAnswer}
        onAnswerChange={onCaptchaAnswerChange}
        onUnavailable={onCaptchaUnavailable}
        onRefresh={onRefreshCaptcha}
      />
      <FormError error={error} />
      <Button
        type="submit"
        className="w-full"
        disabled={
          busy || (captchaProvider?.provider !== "turnstile" && !captcha)
        }
      >
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
      {/* Offered only where the service says a code signs somebody in. Without
          that check this button walks somebody through typing their number to a
          refusal the deployment had already decided on. */}
      {onPhone && (
        <Button
          type="button"
          variant="outline"
          className="w-full"
          disabled={busy}
          onClick={onPhone}
        >
          <Smartphone />
          {t("usePhoneCode")}
        </Button>
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
  codeSent,
  onSendPhoneCode,
  onSubmit,
  onBack,
}: {
  challenge: LoginResult
  busy: boolean
  error: string
  codeSent: boolean
  onSendPhoneCode: () => void
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
      {challenge.methods?.includes("sms") && (
        <Button
          type="button"
          variant="outline"
          className="w-full"
          disabled={busy}
          onClick={onSendPhoneCode}
        >
          <Smartphone />
          {codeSent ? t("resendPhoneCode") : t("sendPhoneCode")}
        </Button>
      )}
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
