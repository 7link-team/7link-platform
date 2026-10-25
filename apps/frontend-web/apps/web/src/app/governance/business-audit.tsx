import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { PaginationPages } from "@workspace/ui/components/pagination"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import type { components } from "../../lib/api-schema"
import { businessAuditLabels, businessAuditQuery, businessAuditPage as auditPage, businessAuditFilters as filters } from "./business-audit-data"

type BusinessEvent = components["schemas"]["BusinessEvent"]

export function BusinessAuditFacts({event, locale}: {event: BusinessEvent; locale: string}) {
  const l = businessAuditLabels[locale as keyof typeof businessAuditLabels] ?? businessAuditLabels["en-US"]
  return <div className="space-y-1 break-words text-xs">
    <div>{[event.actor.kind, event.actor.name, event.actor.id].filter(Boolean).join(" · ") || "—"}</div>
    <div>{[event.authority.kind, event.authority.ref, event.authority.label].filter(Boolean).join(" · ") || "—"}</div>
    {event.origin && <div>{l.origin}: {event.origin === "AGENT_SPAWNED" ? l.agent : event.origin === "HUMAN_CREATED" ? l.human : event.origin}</div>}
    {event.spawn_grant_id && <div>{l.grant}: {event.spawn_grant_id}</div>}
    {event.supervisor_agent_id && <div>{l.supervisor}: {event.supervisor_agent_id}</div>}
  </div>
}

export function BusinessAudit({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const allowed = useAllowed()
  const l = businessAuditLabels[locale as keyof typeof businessAuditLabels] ?? businessAuditLabels["en-US"]
  const [params, setParams] = useSearchParams()
  const query = JSON.stringify(businessAuditQuery(params))
  const [data, setData] = useState<Awaited<ReturnType<typeof iamApi.businessAuditEvents>> | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.businessAuditEvents(tenantID, JSON.parse(query), controller.signal).then(value => {
      if (!controller.signal.aborted) {setData(value); setError(""); setLoading(false)}
    }, cause => {if (!controller.signal.aborted) {setData(null); setError(cause instanceof Error ? cause.message : t("govUnavailable")); setLoading(false)}})
    return () => controller.abort()
  }, [tenantID, query, refresh, t])
  const names = {project_id: t("auditProjectID"), module: t("auditModule"), event_type: t("eventType"), actor_id: t("actorId"), actor_kind: t("auditActorKind"), authority_kind: l.authorityKind, authority_ref: l.authorityRef, target_type: t("targetType"), target_id: t("targetId"), outcome: t("outcome"), occurred_at_gte: t("startTime"), occurred_at_lt: t("endTime"), origin: l.origin, spawn_grant_id: l.grant}
  const page = auditPage(params)
  return <section className="flex min-h-0 flex-1 flex-col gap-3">
    <p className="text-sm text-muted-foreground">{l.hint}</p>
    {allowed("audit_event_view") && <Button variant="outline" asChild><Link to={`/admin/${encodeURIComponent(tenantID)}/audit/events`}>{t("auditLog")}</Link></Button>}
    {error && <Alert>{error}</Alert>}
    <details><summary className="cursor-pointer text-sm">{t("query")}</summary><AutoForm key={query} mode="edit" initialData={JSON.parse(query)} className="md:grid-cols-3" fields={Object.fromEntries(filters.map(key => [key, {name: names[key], type: "text" as const}]))} onSubmit={values => {
      setLoading(true)
      setParams(previous => {const next = new URLSearchParams(previous); next.delete("audit_page"); for (const key of filters) {const value = String(values[key] ?? "").trim(); if (value) next.set(`audit_${key}`, value); else next.delete(`audit_${key}`)} return next})
    }}><Button type="submit">{t("search")}</Button></AutoForm></details>
    {data?.indexLagSeconds !== null && data?.indexLagSeconds !== undefined && <p role="status" className="text-xs text-muted-foreground">{l.lag}: {data.indexLagSeconds}</p>}
    <AutoTable fill data={loading ? [] : data?.items ?? []} rowKey="audit_event_id" loading={loading} pagination={false} exportable={false} onRefresh={() => {setLoading(true); setRefresh(value => value + 1)}} columns={[
      {field: "occurred_at", name: t("occurredAt"), width: "190px", format: (_, row) => new Date(row.occurred_at).toLocaleString(locale)},
      {field: "event_type", name: t("event"), width: "230px"},
      {field: "project_id", name: t("auditProjectID"), width: "160px"},
      {field: "actor", name: t("auditActor"), width: "290px", format: (_, row) => <BusinessAuditFacts event={row} locale={locale} />},
      {field: "target", name: t("target"), width: "220px", format: (_, row) => [row.target.type, row.target.label, row.target.id].filter(Boolean).join(" · ")},
      {field: "outcome", name: t("outcome"), width: "120px", format: (_,row) => t(row.outcome === "denied" ? "deny" : row.outcome)},
      {field: "sequence", name: t("chainSequence"), width: "130px"},
    ]}/>
    <PaginationPages page={page} totalPages={Math.max(1, Math.ceil((data?.total ?? 0) / 50))} disabled={loading} onPageChange={value => {setLoading(true); setParams(previous => {const next = new URLSearchParams(previous); next.set("audit_page", String(value)); return next})}} />
  </section>
}
