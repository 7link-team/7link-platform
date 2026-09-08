import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useState } from "react"
import { KeyRound, MonitorSmartphone, Network, Users } from "lucide-react"
import { useOutletContext } from "react-router"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type EffectiveMenu } from "../../lib/iam-api"
import { effectiveMenuPaths } from "../../lib/navigation"

interface Metrics {
  principals?: number
  roles?: number
  menus?: number
  sessions: number
}

export default function DashboardPage() {
  const t = useTranslations("iam")

  const { menus } = useOutletContext<{ menus: EffectiveMenu[] | null }>()
  const [metrics, setMetrics] = useState<Metrics>({
    sessions: 0,
  })
  const [error, setError] = useState("")
  useEffect(() => {
    if (menus === null) return
    const paths = effectiveMenuPaths(menus)
    let active = true
    void Promise.all([
      paths.has("/iam/users")
        ? iamApi.principals().then((result) => result.total)
        : undefined,
      paths.has("/iam/roles")
        ? iamApi.roles().then((result) => result.length)
        : undefined,
      paths.has("/iam/menus")
        ? iamApi.menus().then((result) => result.length)
        : undefined,
      iamApi.sessions().then((result) => result.length),
    ])
      .then(([principals, roles, menuCount, sessions]) => {
        if (active) {
          setError("")
          setMetrics({ principals, roles, menus: menuCount, sessions })
        }
      })
      .catch((cause: Error) => {
        if (active) setError(cause.message)
      })
    return () => {
      active = false
    }
  }, [menus])
  return (
    <>
      <PageHeader
        title={t("identityControl")}
        description={t("identityStatus")}
      />
      {error && <Alert>{error}</Alert>}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {metrics.principals !== undefined && (
          <Metric
            icon={<Users />}
            label={t("localPrincipals")}
            value={metrics.principals}
          />
        )}
        {metrics.roles !== undefined && (
          <Metric
            icon={<KeyRound />}
            label={t("tenantRoles")}
            value={metrics.roles}
          />
        )}
        {metrics.menus !== undefined && (
          <Metric
            icon={<Network />}
            label={t("menuNodes")}
            value={metrics.menus}
          />
        )}
        <Metric
          icon={<MonitorSmartphone />}
          label={t("mySessions")}
          value={metrics.sessions}
        />
      </div>
      <section className="mt-6 flex min-h-24 flex-wrap items-center justify-between gap-4 rounded-md border bg-card px-5 py-4">
        <div>
          <h2 className="text-sm font-semibold">{t("authorizationStatus")}</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("authorizationDescription")}
          </p>
        </div>
        <span className="flex items-center gap-2 text-xs font-semibold text-success">
          <i className="size-2 rounded-full bg-success ring-4 ring-success/10" />
          {t("policyOnline")}
        </span>
      </section>
    </>
  )
}

function Metric({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode
  label: string
  value: number
}) {
  return (
    <article className="flex min-h-28 items-center gap-4 rounded-md border bg-card p-5">
      <span className="grid size-10 place-items-center rounded-md bg-primary/10 text-primary [&>svg]:size-5">
        {icon}
      </span>
      <div>
        <small className="text-muted-foreground">{label}</small>
        <strong className="mt-1 block text-2xl">{value}</strong>
      </div>
    </article>
  )
}
