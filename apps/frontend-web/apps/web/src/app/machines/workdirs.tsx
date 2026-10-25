import {useRef, useState} from "react"
import {Button} from "@workspace/ui/components/button"
import {AutoForm} from "@workspace/ui/components/auto-form"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {workdirsApi, type Workdir, type WorkdirChange} from "./workdirs-api"

export function WorkdirList({machineID, rows, manage, probe, online, reload, api = workdirsApi}: {machineID: string; rows: Workdir[]; manage: boolean; probe: boolean; online: boolean; reload: () => Promise<void>; api?: typeof workdirsApi}) {
  const t = useTranslations("iam")
  return <section className="mt-4 min-w-0 space-y-4" aria-label={t("fleetTab_workdirs")}>
    <p className="text-sm text-muted-foreground">{t("workdirLifecycleHint")}</p>
    {!rows.length && <p>{t("fleetNoWorkdirs")}</p>}
    {rows.map(row => <WorkdirRow key={`${row.id}:${row.version}`} {...{machineID, row, manage, probe, online, reload, api}}/>)}
  </section>
}

function WorkdirRow({machineID, row, manage, probe, online, reload, api}: {machineID: string; row: Workdir; manage: boolean; probe: boolean; online: boolean; reload: () => Promise<void>; api: typeof workdirsApi}) {
  const t = useTranslations("iam")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [status, setStatus] = useState("")
  const intent = useRef<{signature: string; key: string} | null>(null)
  const submit = async (body: WorkdirChange) => {
    if (busy) return
    const signature = JSON.stringify(body)
    if (intent.current?.signature !== signature) intent.current = {signature, key: crypto.randomUUID()}
    setBusy(true); setError(""); setStatus("")
    try {await api.update(machineID, row, body, intent.current.key); await reload()}
    catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {setBusy(false)}
  }
  return <article className="min-w-0 space-y-3 rounded-md border p-4">
    <h3 className="break-words font-medium">{row.path}</h3>
    <p className="break-words text-sm">{t("fleetProject")}: {row.project_id} · {t("fleetRepository")}: {row.repository_id} · {t("fleetVersion")}: {row.version}</p>
    <p className="text-sm">{t(row.archived_at ? "workdirArchived" : "workdirActive")}{row.archived_at && <time className="ml-2" dateTime={row.archived_at}>{row.archived_at}</time>}</p>
    {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
    {status && <p role="status" className="text-sm">{status}</p>}
    {manage && !row.archived_at && <AutoForm mode="edit" initialData={{path: row.path}} fields={{path: {name: t("fleetPath"), type: "text"}}} onSubmit={values => submit({path: String(values.path ?? ""), archived: false})}>
      <Button type="submit" className="min-h-11" disabled={busy}>{t("save")}</Button>
    </AutoForm>}
    <div className="flex flex-wrap gap-2">
      {manage && <Button type="button" variant="outline" className="min-h-11" disabled={busy} onClick={() => void submit({path: row.path, archived: !row.archived_at})}>{t(row.archived_at ? "workdirRestore" : "workdirArchive")}</Button>}
      {probe && !row.archived_at && <Button type="button" variant="outline" className="min-h-11" disabled={busy || !online} onClick={async () => {
        setBusy(true); setError(""); setStatus("")
        try {await api.probe(machineID, row.id); setStatus(t("workdirProbeQueued"))}
        catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
        finally {setBusy(false)}
      }}>{t("workdirProbe")}</Button>}
    </div>
  </article>
}
