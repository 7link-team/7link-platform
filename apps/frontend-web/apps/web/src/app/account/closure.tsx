import { useCallback, useEffect, useState } from "react"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAuth } from "../../components/auth"
import { iamApi, type ClosureRequest } from "../../lib/iam-api"

const pendingKey = "iam.closure-request"
export default function ClosurePage() {
  const t = useTranslations("iam")
  const locale = useLocale()
  const { session } = useAuth()
  const [request, setRequest] = useState<ClosureRequest | null>(null)
  const [reload, setReload] = useState(0)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [version, setVersion] = useState<number | null>(() => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(pendingKey) ?? "null")
      if (
        saved?.subject === session?.subject &&
        Number.isInteger(saved.version)
      )
        return saved.version
    } catch {
      /* Ignore invalid continuation data. */
    }
    sessionStorage.removeItem(pendingKey)
    return null
  })
  useEffect(() => {
    let active = true
    void iamApi.closureRequest().then(
      (next) => {
        if (active) {
          setRequest(next)
          setError("")
        }
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [reload])
  const submit = useCallback(
    async (proof: string) => {
      if (version === null) return
      await iamApi.submitClosureRequest(proof, version)
      sessionStorage.removeItem(pendingKey)
      setVersion(null)
      setRequest(null)
      setReload((value) => value + 1)
    },
    [version]
  )
  const cancel = async () => {
    if (!request) return
    setBusy(true)
    setError("")
    try {
      await iamApi.cancelClosureRequest(request.version)
      setRequest(null)
      setReload((value) => value + 1)
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <PageHeader
        title={t("accountClosure")}
        description={t("accountClosureDescription")}
      />
      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {!request ? (
        !error && <p role="status">{t("loading")}</p>
      ) : (
        <section className="grid gap-4 rounded-md border p-4">
          <h2 className="font-medium">
            {t(`closureStatus_${request.status}`)}
          </h2>
          {request.requested_at && (
            <p className="text-sm">
              {new Date(request.requested_at).toLocaleString(locale)}
            </p>
          )}
          {request.status === "pending" ? (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => void cancel()}
            >
              {t("cancelClosureRequest")}
            </Button>
          ) : (
            !["completed", "processing"].includes(request.status) && (
              <Button
                variant="destructive"
                onClick={() => {
                  sessionStorage.setItem(
                    pendingKey,
                    JSON.stringify({
                      subject: session?.subject,
                      version: request.version,
                    })
                  )
                  setVersion(request.version)
                }}
              >
                {t("submitClosureRequest")}
              </Button>
            )
          )}
        </section>
      )}
      {version !== null && session && (
        <ReauthenticationDialog
          purpose="principal_close"
          target={session.subject}
          onVerified={submit}
          onClose={() => {
            sessionStorage.removeItem(pendingKey)
            setVersion(null)
          }}
        />
      )}
    </>
  )
}
