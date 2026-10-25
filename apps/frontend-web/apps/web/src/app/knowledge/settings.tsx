import { useEffect, useState } from "react"
import { Link, useParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { knowledgeSources, type Settings } from "./source-api"
import { EmbeddingSettings } from "./embedding-settings"

export default function KnowledgeSettingsPage() {
  const {tenantID = ""} = useParams()
  return <SettingsPanel key={tenantID} tenant={tenantID} />
}
function SettingsPanel({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [settings, setSettings] = useState<Settings | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    if (!allowed("knowledge_manage")) return
    const controller = new AbortController()
    void knowledgeSources.settings(tenant, controller.signal).then(row => {if (!controller.signal.aborted) {setSettings(row); setError("")}}, cause => {if (!controller.signal.aborted) {setSettings(null); setError(cause.message)}})
    return () => controller.abort()
  }, [tenant, revision, allowed])
  return <div className="flex min-w-0 flex-col gap-5">
    <PageHeader title={t("knowledgeSettings")} description={t("knowledgeSettingsHint")} />
    <Link className="inline-flex min-h-11 items-center underline" to={`/admin/${encodeURIComponent(tenant)}/knowledge`}>{t("knowledge")}</Link>
    {!allowed("knowledge_manage") ? <Alert>{t("capabilityPermissionRequired")}</Alert> : <>
      <EmbeddingSettings key={tenant} tenant={tenant}/>
      <Button variant="outline" className="self-start" disabled={busy} onClick={() => setRevision(value => value + 1)}>{t("refresh")}</Button>
      {error && <Alert>{error}</Alert>}{saved && <p role="status">{t("fleetGatewaySaved")}</p>}
      {!settings && !error && <p role="status">{t("loading")}</p>}
      {settings && <>
        <AutoForm mode="detail" initialData={{engine: settings.engine, dialect: settings.dialect, full_text: settings.full_text_available, hybrid: settings.hybrid_available, remote: settings.remote_sync_available, version: settings.version}} fields={{engine: {name: t("knowledgeRetrievalEngine"), type: "text"}, dialect: {name: t("knowledgeDatabaseDialect"), type: "text"}, full_text: {name: t("knowledgeFullText"), type: "checkbox"}, hybrid: {name: t("knowledgeHybrid"), type: "checkbox"}, remote: {name: t("knowledgeRemoteSync"), type: "checkbox"}, version: {name: t("fleetVersion"), type: "number"}}} />
        <dl className="grid gap-2 text-sm"><div><dt>{t("knowledgeURLSync")}</dt><dd>{settings.url_sync_available ? t("knowledgeConnectorAvailable") : t("knowledgeConnectorUnavailable")}</dd></div><div><dt>{t("knowledgeRepositorySync")}</dt><dd>{settings.repository_sync_available ? t("knowledgeConnectorAvailable") : t("knowledgeConnectorUnavailable")}</dd></div></dl>
        <p className="text-sm text-muted-foreground">{t("knowledgeRemoteCapability")}</p>
        <p className="text-sm text-muted-foreground">{t("knowledgeEmbeddingRequirements")}</p>
        <AutoForm key={settings.version} mode="edit" initialData={{candidate_limit: settings.candidate_limit, index_strategy: settings.index_strategy, source_permission: settings.source_permission, memory_approval: settings.memory_approval}} fields={{candidate_limit: {name: t("knowledgeCandidateLimit"), type: "number", required: true}, index_strategy: {name: t("knowledgeIndexStrategy"), type: "select", config: {options: [{value: "LEXICAL", label: t("knowledgeIndexLexical")}, {value: "FULL_TEXT", label: t("knowledgeIndexFullText")}, {value: "HYBRID", label: t("knowledgeIndexHybrid")}] }}, source_permission: {name: t("knowledgeSourcePermission"), type: "select", config: {options: [{value: "WRITERS", label: t("knowledgeSourceWriters")}, {value: "MANAGERS", label: t("knowledgeSourceManagers")}] }}, memory_approval: {name: t("knowledgeMemoryApproval"), type: "select", config: {options: [{value: "RULES_ONLY", label: t("knowledgeApprovalRules")}, {value: "ALL_MEMORIES", label: t("knowledgeApprovalAll")}] }}}} onSubmit={async values => {
          if (busy) return
          setBusy(true); setError(""); setSaved(false)
          try {setSettings(await knowledgeSources.saveSettings(tenant, {candidate_limit: Number(values.candidate_limit), version: settings.version, index_strategy: String(values.index_strategy) as "LEXICAL", source_permission: String(values.source_permission) as "WRITERS", memory_approval: String(values.memory_approval) as "RULES_ONLY"}, crypto.randomUUID())); setSaved(true)}
          catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
          finally {setBusy(false)}
        }}><Button type="submit" disabled={busy}>{t("save")}</Button></AutoForm>
      </>}
    </>}
  </div>
}
