import { useMemo, type ReactNode } from "react"
import { createRealtimeClient } from "@workspace/ui/lib/realtime"
import { RealtimeProvider, useRealtime } from "@workspace/ui/hooks/use-realtime"
import { RealtimeStatus } from "@workspace/ui/components/realtime-status"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { accountViewRequest, accountViewResponse, getAccountView, getAccountViewRevision } from "../lib/account-view"
import { useAuth } from "./auth"

export function WorkspaceRealtime({ tenantID, principal, children }: { tenantID: string; principal: string; children: ReactNode }) {
  const { refresh } = useAuth()
  const revision = getAccountViewRevision()
  const client = useMemo(() => createRealtimeClient({
    url: (tenant, topics) => {
      const query = new URLSearchParams()
      for (const topic of topics) query.append("topic", topic)
	  const path = tenant === "0"
	    ? `/principals/${encodeURIComponent(principal)}/platform-event-stream`
	    : `/tenants/${encodeURIComponent(tenant)}/event-stream`
      return `${(import.meta.env.VITE_API_URL ?? "/api").replace(/\/$/, "")}${path}?${query}`
    },
    request: () => {
      if (!principal) throw new Error("Realtime requires an authenticated principal")
      return accountViewRequest("GET")
    },
    onUnauthorized: () => {
      accountViewResponse(401, revision)
      if (revision === getAccountViewRevision() && !getAccountView()) void refresh().catch(() => {})
    },
  }), [principal, refresh, revision])
  return <RealtimeProvider client={client} tenantID={tenantID}>{children}</RealtimeProvider>
}

export function WorkspaceRealtimeStatus() {
  const t = useTranslations("iam")
  const { status } = useRealtime(null)
  return <RealtimeStatus status={status} labels={{ connecting: t("realtimeConnecting"), polling: t("realtimePolling"), offline: t("realtimeOffline") }} />
}
