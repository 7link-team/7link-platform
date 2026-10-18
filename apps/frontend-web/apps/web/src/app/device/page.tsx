import { AutoForm } from "@workspace/ui/components/auto-form/auto-form"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useCallback, useEffect, useState } from "react"
import { CheckCircle2, MonitorSmartphone, ShieldX, XCircle } from "lucide-react"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type DeviceRequest } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"

type Outcome = "approved" | "denied"

/** The page a television sends somebody to. It names the application asking and
 *  the scopes it asked for, and nothing about the device, because the server
 *  knows nothing about the device -- only that something holding this code is
 *  waiting. */
export default function DevicePage() {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [code, setCode] = useState(
    () => new URLSearchParams(window.location.search).get("user_code") ?? ""
  )
  const [request, setRequest] = useState<DeviceRequest | null>(null)
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const lookup = useCallback((userCode: string) => {
    setBusy(true)
    setError("")
    setRequest(null)
    return iamApi
      .deviceRequest(userCode)
      .then(setRequest, (cause: Error) => {
        // A code that matches nothing and one that expired look the same here,
        // because they look the same to the server: telling them apart would let
        // somebody probe for live codes.
        setError(cause.message)
      })
      .finally(() => setBusy(false))
  }, [])

  // A device shows a complete link, so arriving with the code already in the URL
  // means the person never has to type it.
  useEffect(() => {
    let active = true
    const prefilled = new URLSearchParams(window.location.search).get(
      "user_code"
    )
    if (!prefilled) return
    void iamApi.deviceRequest(prefilled).then(
      (found) => {
        if (active) setRequest(found)
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [])

  const submit = (values: Record<string, unknown>) => {
    const next = String(values.user_code ?? "").trim()
    if (next) return lookup(next)
  }

  const decide = (approve: boolean) =>
    void (async () => {
      setBusy(true)
      setError("")
      try {
        await iamApi.decideDeviceRequest(code.trim(), approve)
        setOutcome(approve ? "approved" : "denied")
        setRequest(null)
      } catch (cause) {
        setError((cause as Error).message)
      } finally {
        setBusy(false)
      }
    })()

  if (!allowed("oauth_device_request_view"))
    return (
      <>
        <PageHeader title={t("deviceApproval")} />
        <Alert>{t("deviceApprovalDenied")}</Alert>
      </>
    )

  return (
    <>
      <PageHeader
        title={t("deviceApproval")}
        description={t("deviceApprovalIntro")}
      />
      {error && <Alert>{error}</Alert>}

      {outcome ? (
        <section className="rounded-md border bg-card p-6 text-center">
          <span
            className={
              outcome === "approved"
                ? "mx-auto grid size-12 place-items-center rounded-full bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                : "mx-auto grid size-12 place-items-center rounded-full bg-destructive/10 text-destructive"
            }
          >
            {outcome === "approved" ? (
              <CheckCircle2 size={24} />
            ) : (
              <ShieldX size={24} />
            )}
          </span>
          <h2 className="mt-4 font-semibold">
            {outcome === "approved" ? t("deviceApproved") : t("deviceDenied")}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {outcome === "approved"
              ? t("deviceApprovedHint")
              : t("deviceDeniedHint")}
          </p>
          <Button
            className="mt-5"
            variant="outline"
            onClick={() => {
              setOutcome(null)
              setCode("")
            }}
          >
            {t("deviceApproveAnother")}
          </Button>
        </section>
      ) : request ? (
        <section className="overflow-hidden rounded-md border bg-card">
          <header className="flex min-h-20 items-start gap-3 border-b px-5 py-4">
            <span className="grid size-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
              <MonitorSmartphone size={20} />
            </span>
            <div className="min-w-0">
              <h2 className="font-semibold break-words">
                {request.client_name}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {t("deviceAsksToSignIn")}
              </p>
            </div>
          </header>
          <dl className="grid gap-4 px-5 py-4 sm:grid-cols-2">
            <div>
              <dt className="text-xs font-semibold text-muted-foreground uppercase">
                {t("deviceScopes")}
              </dt>
              <dd className="mt-2 flex flex-wrap gap-1.5">
                {request.scope
                  ? request.scope.split(" ").map((scope) => (
                      <Badge key={scope} variant="secondary">
                        {scope}
                      </Badge>
                    ))
                  : t("deviceNoScopes")}
              </dd>
            </div>
            <div>
              <dt className="text-xs font-semibold text-muted-foreground uppercase">
                {t("deviceExpiresAt")}
              </dt>
              <dd className="mt-2 text-sm">
                {new Date(request.expires_at).toLocaleString()}
              </dd>
            </div>
          </dl>
          <footer className="flex flex-wrap items-center justify-end gap-2 border-t bg-muted/20 px-5 py-4">
            <Button
              variant="outline"
              disabled={busy || !allowed("oauth_device_request_approve")}
              onClick={() => decide(false)}
            >
              <XCircle />
              {t("deviceDeny")}
            </Button>
            <Button
              disabled={busy || !allowed("oauth_device_request_approve")}
              onClick={() => decide(true)}
            >
              <CheckCircle2 />
              {t("deviceApprove")}
            </Button>
          </footer>
        </section>
      ) : (
        <AutoForm
          className="max-w-md rounded-md border bg-card p-5 md:grid-cols-1 [&>*]:col-span-1"
          onSubmit={submit}
          fields={{
            user_code: {
              name: t("deviceUserCode"),
              type: "text",
              value: code,
              config: {
                id: "user_code",
                autoComplete: "off",
                autoCapitalize: "characters",
                placeholder: "ACDE-FGHJ",
              },
            },
          }}
          onChange={(values) => {
            if (values.user_code !== code)
              setCode(String(values.user_code ?? ""))
          }}
        >
          <p className="text-xs text-muted-foreground">
            {t("deviceUserCodeHint")}
          </p>
          <Button type="submit" disabled={busy || !code.trim()}>
            {t("deviceLookUp")}
          </Button>
        </AutoForm>
      )}
    </>
  )
}
