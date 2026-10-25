import { useParams, useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { Gateway } from "./gateway"

const sections = ["providers", "models", "budgets", "requests"] as const
const labels = {providers: "fleetGatewayProviders", models: "fleetGatewayModels", budgets: "fleetGatewayBudgets", requests: "fleetGatewayRequests"}
export default function GatewayPage() {
  const {tenantID = ""} = useParams()
  const [search, setSearch] = useSearchParams()
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const section = sections.find(value => value === search.get("tab")) ?? "providers"
  return <div className="flex min-w-0 flex-col gap-5">
    <PageHeader title={t("fleetGatewayTitle")} description={t("fleetGatewayHint")} />
    {allowed("gateway_manage") ? <>
      <nav className="flex flex-wrap gap-2 border-b pb-3" aria-label={t("fleetGatewayTitle")}>{sections.map(value => <Button key={value} className="min-h-11" variant={section === value ? "default" : "outline"} aria-current={section === value ? "page" : undefined} onClick={() => setSearch({tab: value})}>{t(labels[value])}</Button>)}</nav>
      <Gateway key={tenantID} tenantID={tenantID} section={section} />
    </> : <Alert>{t("capabilityPermissionRequired")}</Alert>}
  </div>
}
