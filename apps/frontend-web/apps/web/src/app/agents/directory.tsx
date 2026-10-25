import { useEffect, useState } from "react"
import { Link } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { emptyRosterFilters, rosterApi, type RosterFilters, type RosterPage } from "./roster-api"

export function AgentDirectory({ tenant, onSelect }: { tenant: string; onSelect: (principal: string) => void }) {
  const t = useTranslations("iam")
  const [draft, setDraft] = useState<RosterFilters>(emptyRosterFilters)
  const [filters, setFilters] = useState<RosterFilters>(emptyRosterFilters)
  const [offset, setOffset] = useState(0)
  const [revision, setRevision] = useState(0)
  const identity = JSON.stringify([filters, offset, revision])
  const [result, setResult] = useState<{ identity: string; page: RosterPage | null; error: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void rosterApi.list(tenant, filters, offset, controller.signal).then(page => {
      if (!controller.signal.aborted) setResult({ identity, page, error: "" })
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setResult({ identity, page: null, error: cause instanceof Error ? cause.message : t("clientSaveFailed") })
    })
    return () => controller.abort()
  }, [tenant, filters, offset, revision, identity, t])
  const current = result?.identity === identity ? result : null
  const page = current?.page
  const loading = !current
  const options: Partial<Record<keyof RosterFilters, string[]>> = {
    kind: ["HUMAN", "AGENT"], runtime_kind: ["CODEX", "CLAUDE_CODE"], origin: ["HUMAN_CREATED", "AGENT_SPAWNED"],
    lineage_status: ["HUMAN_CREATED", "SPAWNED", "ADOPTED", "ORPHANED"], activity_status: ["ONLINE", "THINKING", "WORKING", "OFFLINE", "ERROR"],
    sort: ["name", "-name", "created_at", "-created_at", "state", "-state", "fpa_30d", "-fpa_30d", "load_ratio", "-load_ratio", "kind", "-kind", "origin", "-origin"],
  }
  return <section className="min-w-0 space-y-3" aria-label={t("teamRosterTitle")}>
    <h2 className="font-medium">{t("teamRosterTitle")}</h2>
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => { event.preventDefault(); setFilters({ ...draft, search: draft.search.trim() }); setOffset(0); setRevision(value => value + 1) }}>
      {(Object.keys(draft) as (keyof RosterFilters)[]).map(key => <label key={key} className="grid gap-2 text-sm" htmlFor={`roster-${key}`}>{t(`rosterFilter_${key}`)}
        {options[key] ? <select id={`roster-${key}`} className="min-h-11 rounded-md border bg-background px-3" value={draft[key]} onChange={event => setDraft(value => ({ ...value, [key]: event.target.value }))}>
          {key !== "sort" && <option value="">{t("rosterAll")}</option>}{options[key]!.map(value => <option key={value} value={value}>{value}</option>)}
        </select> : <Input id={`roster-${key}`} value={draft[key]} maxLength={200} onChange={event => setDraft(value => ({ ...value, [key]: event.target.value }))} />}
      </label>)}
      <Button type="submit">{t("agentBrowseApply")}</Button>
    </form>
    <p className="text-sm text-muted-foreground">{t("rosterStatisticsScope")}</p>
    {current?.error && <Alert>{current.error}</Alert>}
    <AutoTable data={page?.items ?? []} loading={loading} rowKey="principal_id" pagination={false} exportable={false}
      emptyContent={current?.error ? t("agentBrowseFailed") : t("agentBrowseEmpty")} onRefresh={() => setRevision(value => value + 1)} columns={[
        { field: "name", name: t("agentName"), render: (_, row) => row.kind === "AGENT" ? <button className="text-left underline underline-offset-4" type="button" onClick={() => onSelect(row.principal_id)}>{row.name || row.principal_id}</button> : <span>{row.name || row.principal_id}</span> },
        { field: "kind", name: t("rosterFilter_kind") },
        { field: "roles", name: t("rosterFilter_role"), render: (_, row) => row.roles.join(", ") || t("rosterUnavailable") },
        { field: "state", name: t("status") }, { field: "activity_status", name: t("rosterFilter_activity_status") },
        { field: "runtime", name: t("agentRuntimeKind"), render: (_, row) => [row.runtime, row.model].filter(Boolean).join(" / ") || t("rosterUnavailable") },
        { field: "load", name: t("rosterLoad"), render: (_, row) => `${row.load.active_items}${row.load.max === undefined ? "" : ` / ${row.load.max}`}` },
        { field: "current_work_items", name: t("rosterCurrentWork"), render: (_, row) => <div className="grid gap-1">{row.current_work_items.map(work => <Link key={work.id} className="underline underline-offset-4" to={`/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(work.project_id)}/work-items?work=${encodeURIComponent(work.id)}`}>{work.title || work.id}</Link>)}</div> },
        { field: "fpa_30d", name: t("rosterFirstPass"), render: (_, row) => row.performance_available ? `${row.fpa_30d === null ? t("rosterUnavailable") : `${Math.round(row.fpa_30d * 100)}%`} (n=${row.stats_30d.first_decisions})` : t("rosterUnavailable") },
        { field: "origin", name: t("rosterFilter_origin"), render: (_, row) => [row.origin, row.lineage_status].filter(Boolean).join(" / ") || t("rosterUnavailable") },
      ]} footer={<div className="flex w-full items-center justify-end gap-2"><span className="text-sm">{page ? `${Math.min(offset + 1, page.total)} - ${offset + page.items.length} / ${page.total}` : ""}</span><Button variant="outline" disabled={loading || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 50))}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={loading || !page?.has_more} onClick={() => setOffset(value => value + 50)}>{t("fleetNext")}</Button></div>} />
  </section>
}
