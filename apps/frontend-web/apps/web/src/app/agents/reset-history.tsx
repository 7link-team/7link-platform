import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { resetHistoryApi as api, type ResetHistory as History } from "./reset-history-api"

export function AgentResetHistory({ tenant, agent, revision }: { tenant: string; agent: string; revision: number }) {
  const t = useTranslations("iam")
  const [cursors, setCursors] = useState([""])
  const cursor = cursors[cursors.length - 1]!
  const [refresh, setRefresh] = useState(0)
  const identity = `${tenant}:${agent}:${cursor}:${revision}:${refresh}`
  const [result, setResult] = useState<{ identity: string; page: History | null; error: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void api.list(tenant, agent, cursor, controller.signal).then(page => {
      if (!controller.signal.aborted) setResult({ identity, page, error: "" })
    }).catch((cause: Error) => {
      if (!controller.signal.aborted) setResult({ identity, page: null, error: cause.message })
    })
    return () => controller.abort()
  }, [tenant, agent, cursor, identity])
  const current = result?.identity === identity ? result : null
  const page = current?.page
  return <section className="min-w-0 space-y-3" aria-label={t("agentResetHistory")}>
    <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="font-medium">{t("agentResetHistory")}</h2><Button variant="outline" onClick={() => { setCursors([""]); setRefresh(value => value + 1) }}>{t("refresh")}</Button></div>
    <p className="text-sm text-muted-foreground">{t("agentResetHistoryHint")}</p>
    {!current && <p role="status">{t("loading")}</p>}
    {current?.error && <Alert>{current.error}</Alert>}
    {page && <><ol className="space-y-3">{page.items.map(row => <li key={row.id} className="space-y-1 border-b pb-3 text-sm"><p className="break-words font-medium">{row.kind} · {row.state}</p><p className="break-words">{row.reason}</p><p className="break-words text-muted-foreground">{row.created_at} · {row.requested_by}</p></li>)}</ol>{!page.items.length && <p>{t("agentResetHistoryEmpty")}</p>}</>}
    <div className="flex gap-2"><Button variant="outline" disabled={!current || cursors.length < 2} onClick={() => setCursors(values => values.slice(0, -1))}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={!page?.has_more || !page.next_cursor} onClick={() => setCursors(values => [...values, page!.next_cursor!])}>{t("fleetNext")}</Button></div>
  </section>
}
