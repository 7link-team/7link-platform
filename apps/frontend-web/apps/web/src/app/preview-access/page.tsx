import { useEffect, useState } from "react"
import { Navigate, useLocation } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { useAuth } from "../../components/auth"
import { SessionVerification } from "../../components/session-verification"
import { iamApi } from "../../lib/iam-api"
import { previewAccessURL, previewLabel } from "../../lib/remote-preview"

export default function PreviewAccessPage() {
  const t = useTranslations("iam")
  const {status} = useAuth()
  const location = useLocation()
  const label = previewLabel(location.search)
  const [error, setError] = useState("")
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    if (status !== "authenticated" || !label) return
    const controller = new AbortController()
    void iamApi.previewLabelAccess(label, controller.signal).then((grant) => {
      if (!controller.signal.aborted) window.location.replace(previewAccessURL(grant.url, window.location.origin))
    }).catch((cause: Error) => {if (!controller.signal.aborted) setError(cause.message)})
    return () => controller.abort()
  }, [status, label, attempt])
  if (status === "loading" || status === "error") return <SessionVerification />
  if (status === "anonymous") return <Navigate to="/admin/login" state={{from: location.pathname + location.search}} replace />
  return <main className="grid min-h-svh place-items-center bg-background p-4">
    <section className="flex w-full max-w-lg flex-col gap-4">
      <h1 className="text-xl font-semibold">{t("remotePreviewTitle")}</h1>
      <p className="text-sm text-muted-foreground">{t("remotePreviewExchangeHint")}</p>
      {!label ? <Alert>{t("remotePreviewInvalidLabel")}</Alert> : error ? <>
        <Alert>{error}</Alert>
        <Button className="min-h-11" onClick={() => {setError(""); setAttempt((value) => value + 1)}}>{t("retryNow")}</Button>
      </> : <p role="status">{t("remotePreviewExchanging")}</p>}
    </section>
  </main>
}
