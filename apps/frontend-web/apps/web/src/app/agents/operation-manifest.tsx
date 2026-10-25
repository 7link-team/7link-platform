import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { manifestApi as api, type Publication, type Manifest, type Capability } from "./operation-manifest-api"

export function CapabilityFacts({ value }: { value: Capability }) {
  const t = useTranslations("iam")
  return <div className="space-y-1 text-sm"><p>{t(`agentCapability_${value.status}`)}</p>{value.reason_code && <p className="break-words text-muted-foreground">{value.reason_code}</p>}<p>{t("agentManifestConfirmation")}: {value.human_confirmation}</p><p>{t("agentManifestQuota")}: {value.quota_state.kind} / {value.quota_state.state}{value.quota_state.remaining !== undefined ? ` / ${value.quota_state.remaining}` : ""}</p></div>
}
export function AgentOperationManifest({ tenant, agent = "" }: { tenant: string; agent?: string }) {
  const t = useTranslations("iam")
  const [version, setVersion] = useState("")
  const [project, setProject] = useState("")
  const [projectDraft, setProjectDraft] = useState("")
  const [cursors, setCursors] = useState([""])
  const [revision, setRevision] = useState(0)
  const cursor = cursors[cursors.length - 1]!
  const identity = `${tenant}:${agent}:${version}:${project}:${cursor}:${revision}`
  const [result, setResult] = useState<{ identity: string; publications: Publication[]; manifest: Manifest | null; capabilities: Capability[]; nextCursor: string; error: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void Promise.all([api.versions(tenant, cursor, controller.signal), api.manifest(tenant, version, controller.signal), agent ? api.capabilities(tenant, agent, project, version, controller.signal) : Promise.resolve(null)]).then(([publications, manifest, capabilities]) => {
      if (!controller.signal.aborted) setResult({ identity, publications: publications.items, manifest, capabilities: capabilities?.operations ?? [], nextCursor: publications.has_more ? publications.next_cursor ?? "" : "", error: "" })
    }).catch((error: Error) => { if (!controller.signal.aborted) setResult({ identity, publications: [], manifest: null, capabilities: [], nextCursor: "", error: error.message }) })
    return () => controller.abort()
  }, [tenant, agent, version, project, cursor, identity])
  const current = result?.identity === identity ? result : null
  return <section className="space-y-4" aria-label={t("agentManifestTitle")}>
    <div className="flex items-center justify-between gap-3"><h2 className="font-medium">{t("agentManifestTitle")}</h2><Button variant="outline" onClick={() => setRevision(value => value + 1)}>{t("refresh")}</Button></div><p className="text-sm text-muted-foreground">{t("agentManifestHint")}</p>
    <label className="grid gap-2 text-sm">{t("agentManifestVersion")}<select className="h-9 rounded-md border bg-background px-3" value={version} onChange={event => setVersion(event.target.value)}><option value="">{t("agentManifestLatest")}</option>{version && !current?.publications.some(row => row.manifest_version === version) && <option value={version}>{version}</option>}{current?.publications.map(row => <option key={row.manifest_version} value={row.manifest_version}>{row.manifest_version} / {row.operation_count} / {row.published_at}</option>)}</select></label>
    <div className="flex gap-2"><Button variant="outline" disabled={!current || cursors.length < 2} onClick={() => setCursors(values => values.slice(0, -1))}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={!current?.nextCursor} onClick={() => setCursors(values => [...values, current!.nextCursor])}>{t("fleetNext")}</Button></div>
    {agent && <form className="flex items-end gap-2" onSubmit={event => { event.preventDefault(); setProject(projectDraft) }}><label className="grid gap-2 text-sm">{t("agentManifestProject")}<Input value={projectDraft} onChange={event => setProjectDraft(event.target.value)} pattern="[1-9][0-9]{0,18}" inputMode="numeric" /></label><Button type="submit">{t("agentManifestEvaluate")}</Button></form>}
    {!current && <p role="status">{t("loading")}</p>}{current?.error && <Alert>{current.error}</Alert>}
    {current?.manifest && <><p className="break-all font-mono text-xs">{current.manifest.manifest_version} / {current.manifest.sha256}</p><ul className="divide-y">{(current.manifest.operations ?? []).map(op => { const capability = current.capabilities.find(value => value.name === op.name); return <li key={op.name} className="space-y-2 py-3"><p className="font-medium">{op.name}</p><p className="text-sm">{op.summary}</p><p className="break-all font-mono text-xs">{op.method} {op.path}</p><p className="text-sm">{op.required_permission || op.via}</p>{capability && <CapabilityFacts value={capability} />}<details><summary className="cursor-pointer text-sm">{t("agentManifestSchemas")}</summary><pre className="overflow-x-auto text-xs">{JSON.stringify({ input: op.input_schema, output: op.output_schema }, null, 2)}</pre></details></li> })}</ul></>}
  </section>
}
