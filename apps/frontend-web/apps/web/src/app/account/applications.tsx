import { useCallback, useEffect, useState } from "react"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type GrantedConsent } from "../../lib/iam-api"

export default function AuthorizedApplicationsPage() {
  const t = useTranslations("iam")
  const locale = useLocale()
  const [consents, setConsents] = useState<GrantedConsent[] | null>(null)
  const [error, setError] = useState("")
  const [selected, setSelected] = useState<GrantedConsent | null>(null)
  const [busy, setBusy] = useState(false)
  const refresh = useCallback(async () => {
    const items = await iamApi.oauthConsents()
    setConsents(items)
    setError("")
  }, [])
  useEffect(() => {
    let active = true
    void iamApi.oauthConsents().then(
      (items) => {
        if (active) setConsents(items)
      },
      (cause: Error) => {
        if (active) setError(cause.message)
      }
    )
    return () => {
      active = false
    }
  }, [])
  const revoke = async () => {
    if (!selected) return
    setBusy(true)
    setError("")
    try {
      await iamApi.revokeOAuthConsent(selected.client_id)
      setSelected(null)
      // A failed readback must not leave a revoked application actionable.
      setConsents(null)
      await refresh()
    } catch (cause) {
      setError((cause as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <PageHeader
        title={t("authorizedApplications")}
        description={t("authorizedApplicationsDescription")}
      />
      {error && !selected && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() =>
              void refresh().catch((cause: Error) => setError(cause.message))
            }
          >
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {!consents ? (
        !error && <p role="status">{t("loading")}</p>
      ) : consents.length === 0 ? (
        <p>{t("noAuthorizedApplications")}</p>
      ) : (
        <ul className="space-y-4">
          {consents.map((consent) => (
            <li key={consent.client_id} className="rounded-md border p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 className="min-w-0 font-medium break-words">
                  {consent.client_name}
                </h2>
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => {
                    setError("")
                    setSelected(consent)
                  }}
                >
                  {t("revokeNamed", { name: consent.client_name })}
                </Button>
              </div>
              <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm [&>dd]:break-all">
                <dt>{t("tenant")}</dt>
                <dd>{consent.tenant_id}</dd>
                <dt>{t("authorizedScopes")}</dt>
                <dd>{consent.scopes?.join(" ") || "—"}</dd>
                <dt>{t("grantedAt")}</dt>
                <dd>{new Date(consent.granted_at).toLocaleString(locale)}</dd>
              </dl>
              <p className="mt-2 text-sm text-muted-foreground">
                {t("lastUsed", {
                  date: new Date(consent.last_used_at).toLocaleString(locale),
                })}
              </p>
            </li>
          ))}
        </ul>
      )}
      <Dialog
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setSelected(null)
            setError("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("revoke")}</DialogTitle>
            <DialogDescription>
              {t("revokeApplicationConfirm", {
                name: selected?.client_name ?? "",
              })}
            </DialogDescription>
          </DialogHeader>
          {error && <Alert>{error}</Alert>}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => {
                setSelected(null)
                setError("")
              }}
            >
              {t("cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => void revoke()}
            >
              {busy ? t("loading") : t("confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
