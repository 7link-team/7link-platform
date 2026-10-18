import { AutoTable } from "@workspace/ui/components/auto-table"
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
    <div className="flex min-h-0 flex-1 flex-col gap-4">
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
      <AutoTable
        fill
        data={consents ?? []}
        loading={!consents && !error}
        emptyContent={t("noAuthorizedApplications")}
        rowKey="client_id"
        searchable
        onRefresh={() =>
          refresh().catch((cause: Error) => setError(cause.message))
        }
        columns={[
          {
            field: "client_name",
            name: t("name"),
            render: (_, consent) => (
              <h2 className="font-medium break-words">{consent.client_name}</h2>
            ),
          },
          {
            field: "tenant_id",
            name: t("tenant"),
            render: (_, consent) => (
              <span className="break-all">{consent.tenant_id}</span>
            ),
          },
          {
            field: "scopes",
            name: t("authorizedScopes"),
            render: (_, consent) => consent.scopes?.join(" ") || "—",
          },
          {
            field: "granted_at",
            name: t("grantedAt"),
            render: (_, consent) =>
              new Date(consent.granted_at).toLocaleString(locale),
          },
          {
            field: "last_used_at",
            name: t("lastUsed", { date: "" }),
            render: (_, consent) =>
              t("lastUsed", {
                date: new Date(consent.last_used_at).toLocaleString(locale),
              }),
          },
          {
            field: "actions",
            type: "actions",
            name: t("actions"),
            render: (_, consent) => (
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
            ),
          },
        ]}
      />
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
    </div>
  )
}
