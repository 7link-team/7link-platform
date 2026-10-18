import { PaginationPages } from "@workspace/ui/components/pagination"
import { AutoTable } from "@workspace/ui/components/auto-table"
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
  const [readLoading, setReadLoading] = useState(true)
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
    void iamApi
      .personalMemberships(offset)
      .then(
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
      .finally(() => {
        if (active) setReadLoading(false)
      })
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
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <PageHeader
        title={t("myMemberships")}
        description={t("myMembershipsDescription")}
      />

      {error && (
        <Alert>
          <p>{t.has(error) ? t(error) : error}</p>
          <Button onClick={() => setReload((value) => value + 1)}>
            {t("retryNow")}
          </Button>
        </Alert>
      )}
      <AutoTable
        fill
        toolbar={
          <>
            {registration && (
              <Button variant="outline" asChild>
                <Link to="/admin/workspaces?create=1">
                  {t("registerTenant")}
                </Link>
              </Button>
            )}
          </>
        }
        data={items ?? []}
        loading={readLoading || (!items && !error)}
        emptyContent={t("noMemberships")}
        rowKey="tenant_id"
        pagination={false}
        rowOffset={offset}
        onRefresh={() => {
          setReadLoading(true)
          setReload((value) => value + 1)
        }}
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            <span className="text-sm text-muted-foreground">
              {Math.min(offset + 50, total)} / {total}
            </span>
            <PaginationPages
              page={Math.floor(offset / 50) + 1}
              totalPages={Math.max(1, Math.ceil(total / 50))}
              disabled={!items || !!error}
              onPageChange={(page) => {
                setItems(null)
                setOffset((page - 1) * 50)
              }}
            />
          </div>
        }
        columns={[
          {
            field: "tenant_name",
            name: t("tenant"),
            render: (_, item) => (
              <h2 className="font-medium break-all">{item.tenant_name}</h2>
            ),
          },
          {
            field: "tenant_id",
            name: t("tenant"),
            render: (_, item) => (
              <span className="break-all">{item.tenant_id}</span>
            ),
          },
          {
            field: "actions",
            type: "actions",
            name: t("actions"),
            render: (_, item) => (
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
            ),
          },
        ]}
      />

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
    </div>
  )
}
