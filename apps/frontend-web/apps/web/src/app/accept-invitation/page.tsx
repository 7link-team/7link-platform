import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useState, type FormEvent } from "react"
import { ArrowLeft, CheckCircle2, MailCheck, ShieldCheck } from "lucide-react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Label } from "@workspace/ui/components/label"
import { ThemeModeButton } from "../../components/theme-mode-button"
import { ApiError, iamApi, type InvitationAcceptance } from "../../lib/iam-api"
import { useBranding, copyrightLine } from "../../lib/branding"

export default function AcceptInvitationPage() {
  const { name: appName } = useBranding()
  const t = useTranslations("iam")

  const [token] = useState(() => {
    const value = new URLSearchParams(window.location.search).get("token") ?? ""
    window.history.replaceState(null, "", "/admin/accept-invitation")
    return value
  })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(token ? "" : t("invitationMissingToken"))
  const [accepted, setAccepted] = useState<InvitationAcceptance | null>(null)

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!token) return
    const data = new FormData(event.currentTarget)
    const password = String(data.get("password") ?? "")
    if (password !== String(data.get("confirm_password") ?? "")) {
      setError(t("passwordMismatchSentence"))
      return
    }
    setBusy(true)
    setError("")
    try {
      setAccepted(
        await iamApi.acceptInvitation({
          token,
          login_name: String(data.get("login_name") ?? "").trim(),
          password,
          display_name: String(data.get("display_name") ?? "").trim(),
        })
      )
    } catch (cause) {
      setError(
        cause instanceof ApiError ? cause.message : t("identityRetryLater")
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
      <main className="w-full max-w-[420px]">
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
            <h1 className="text-xl font-semibold">
              {t("acceptMemberInvitation")}
            </h1>
            <p className="text-sm text-muted-foreground">
              {t("acceptInvitationDescription")}
            </p>
          </header>

          {accepted ? (
            <div
              className="mt-5 flex items-start gap-3 rounded-md border border-primary/25 bg-primary/5 px-4 py-3"
              role="status"
            >
              <CheckCircle2 className="mt-0.5 size-5 text-primary" />
              <div>
                <p className="text-sm font-medium">{t("invitationAccepted")}</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  {accepted.email} {t("invitationAcceptedDescription")}
                </p>
              </div>
            </div>
          ) : (
            <form className="mt-5 space-y-4" onSubmit={submit}>
              <InvitationField
                id="invitation-login"
                name="login_name"
                label={t("loginIdentifier")}
                autoComplete="username"
                maxLength={200}
              />
              <InvitationField
                id="invitation-display-name"
                name="display_name"
                label={t("displayName")}
                autoComplete="name"
                maxLength={128}
              />
              <InvitationField
                id="invitation-password"
                name="password"
                label={t("password")}
                type="password"
                autoComplete="new-password"
                minLength={12}
                maxLength={1024}
              />
              <InvitationField
                id="invitation-confirm-password"
                name="confirm_password"
                label={t("confirmPassword")}
                type="password"
                autoComplete="new-password"
                minLength={12}
                maxLength={1024}
              />
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button
                type="submit"
                className="w-full"
                disabled={busy || !token}
              >
                <ShieldCheck />
                {busy ? t("accepting") : t("acceptInvitation")}
              </Button>
            </form>
          )}

          <Button variant="ghost" className="mt-4 w-full" asChild>
            <Link to="/admin/login">
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

function InvitationField({
  id,
  name,
  label,
  type = "text",
  ...input
}: {
  id: string
  name: string
  label: string
  type?: string
  autoComplete: string
  minLength?: number
  maxLength: number
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} name={name} type={type} required {...input} />
    </div>
  )
}
