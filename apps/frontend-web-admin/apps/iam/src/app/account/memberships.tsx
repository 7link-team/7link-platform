import { useCallback, useEffect, useState } from "react"
import { Link } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { useAuth } from "../../components/auth"
import {
  getTenant,
  iamApi,
  setTenant,
  type PersonalMembership,
} from "../../lib/iam-api"

const pendingKey = "iam.membership-leave"
export default function MembershipsPage() {
  const t = useTranslations("iam")
  const { session } = useAuth()
  const [items, setItems] = useState<PersonalMembership[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [reload, setReload] = useState(0)
  const [error, setError] = useState("")
  const [registration, setRegistration] = useState(false)
  useEffect(() => {
    let active = true
    void iamApi.tenantRegistrationPolicy().then(
      (policy) => {
        if (active) setRegistration(policy.enabled)
      },
      () => {}
    )
    return () => {
      active = false
    }
  }, [])
  const [selected, setSelected] = useState<string | null>(() => {
    try {
      const saved = JSON.parse(sessionStorage.getItem(pendingKey) ?? "null")
      if (
        saved?.subject === session?.subject &&
        typeof saved.tenant === "string"
      )
        return saved.tenant
    } catch {
      /* Ignore invalid continuation data. */
    }
    sessionStorage.removeItem(pendingKey)
    return null
  })
  useEffect(() => {
    let active = true
    void iamApi.personalMemberships(offset).then(
      (result) => {
        if (active) {
          setItems(result.items)
          setTotal(result.total)
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
  }, [offset, reload])
  const leave = useCallback(
    async (proof: string) => {
      if (!selected) return
      await iamApi.leaveMembership(selected, proof)
      if (getTenant() === selected) setTenant("")
      window.dispatchEvent(new Event("tenant-catalog-change"))
      sessionStorage.removeItem(pendingKey)
      setSelected(null)
      setItems(null)
      setOffset(0)
      setReload((value) => value + 1)
    },
    [selected]
  )
  return (
    <>
      <PageHeader
        title={t("myMemberships")}
        description={t("myMembershipsDescription")}
      />
      {registration && (
        <Button variant="outline" asChild>
          <Link to="/workspaces/tenant?create=1">{t("registerTenant")}</Link>
        </Button>
      )}
      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      {!items ? (
        !error && <p role="status">{t("loading")}</p>
      ) : items.length === 0 ? (
        <p>{t("noMemberships")}</p>
      ) : (
        <ul className="space-y-3">
          {items.map((item) => (
            <li
              key={item.tenant_id}
              className="flex flex-wrap items-center justify-between gap-3 rounded-md border p-4"
            >
              <div className="min-w-0">
                <h2 className="font-medium break-all">{item.tenant_name}</h2>
                <p className="text-sm break-all text-muted-foreground">
                  {item.tenant_id}
                </p>
              </div>
              <Button
                variant="outline"
                onClick={() => {
                  sessionStorage.setItem(
                    pendingKey,
                    JSON.stringify({
                      subject: session?.subject,
                      tenant: item.tenant_id,
                    })
                  )
                  setSelected(item.tenant_id)
                }}
              >
                {t("leaveTenant")}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {items && (
        <div className="mt-4 flex items-center justify-between gap-3">
          <Button
            variant="outline"
            disabled={offset === 0}
            onClick={() => {
              setItems(null)
              setOffset(Math.max(0, offset - 50))
            }}
          >
            {t("previous")}
          </Button>
          <span>
            {Math.min(offset + 50, total)} / {total}
          </span>
          <Button
            variant="outline"
            disabled={offset + 50 >= total}
            onClick={() => {
              setItems(null)
              setOffset(offset + 50)
            }}
          >
            {t("next")}
          </Button>
        </div>
      )}
      {selected && (
        <ReauthenticationDialog
          purpose="tenant_leave"
          target={selected}
          onVerified={leave}
          onClose={() => {
            sessionStorage.removeItem(pendingKey)
            setSelected(null)
          }}
        />
      )}
    </>
  )
}
