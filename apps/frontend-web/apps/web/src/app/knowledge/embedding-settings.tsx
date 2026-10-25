import { useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { nativeKnowledge, type EmbeddingConfig, type EmbeddingWrite } from "./native-api"

export function EmbeddingSettings({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const [config, setConfig] = useState<EmbeddingConfig | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [status, setStatus] = useState("")
  const [pending, setPending] = useState<{body: EmbeddingWrite; probe: boolean} | null>(null)
  const probeSubmission = useRef(false)
  async function action(run: () => Promise<void>) {
    if (busy) return
    setBusy(true); setError(""); setStatus("")
    try {await run()} catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))} finally {setBusy(false)}
  }
  async function save(body: EmbeddingWrite) {
    await action(async () => {setConfig(await nativeKnowledge.save(tenant, body)); setStatus(t("fleetGatewaySaved")); setPending(null)})
  }
  async function probe(body: EmbeddingWrite) {
    await action(async () => {
      const result = await nativeKnowledge.probe(tenant, body)
      setStatus(result.ok ? `${t("knowledgeEmbeddingProbeOK")} · ${result.dims_returned} · ${result.latency_ms.toFixed(0)} ms` : `${t("knowledgeEmbeddingProbeFailed")} · ${result.error_code ?? ""}`)
      setPending(null)
    })
  }
  return <details className="min-w-0 space-y-3 border-t pt-4">
    <summary className="min-h-11 cursor-pointer font-medium">{t("knowledgeEmbeddingTitle")}</summary>
    <p className="text-sm text-muted-foreground">{t("knowledgeEmbeddingHint")}</p>
    <Button variant="outline" disabled={busy} onClick={() => void action(async () => setConfig(await nativeKnowledge.config(tenant)))}>{t("refresh")}</Button>
    {error && <Alert>{error}</Alert>}{busy && <p role="status">{t("loading")}</p>}{status && <p role="status">{status}</p>}
    {config && <p className="break-words text-sm">{t("knowledgeEmbeddingCoverage")} {(config.coverage_ppm / 10000).toFixed(1)}% · {config.state}{config.pending_model_key && <> · {t("knowledgeEmbeddingPending")}</>}</p>}
    <AutoForm key={config?.version ?? 0} mode="edit" initialData={{base_url: config?.base_url ?? "", model: config?.model ?? "", dims: config?.dims ?? 1536, api_key: "", batch_size: config?.batch_size ?? 64, rpm: config?.rpm ?? 60, tpm: config?.tpm ?? 150000, timeout_ms: config?.timeout_ms ?? 1500, enabled: config?.state === "ACTIVE"}} fields={{
      base_url: {name: t("knowledgeEmbeddingURL"), type: "text", required: true},
      model: {name: t("knowledgeEmbeddingModel"), type: "text", required: true},
      dims: {name: t("knowledgeEmbeddingDimensions"), type: "select", config: {options: [384, 768, 1024, 1536].map(value => ({value: String(value), label: String(value)}))}},
      api_key: {name: t("knowledgeEmbeddingKey"), type: "password"},
      batch_size: {name: t("knowledgeEmbeddingBatch"), type: "number"},
      rpm: {name: t("knowledgeEmbeddingRPM"), type: "number"},
      tpm: {name: t("knowledgeEmbeddingTPM"), type: "number"},
      timeout_ms: {name: t("knowledgeEmbeddingTimeout"), type: "number"},
      enabled: {name: t("knowledgeEmbeddingEnabled"), type: "checkbox"},
    }} onSubmit={async values => {
      const testing = probeSubmission.current
      probeSubmission.current = false
      const body: EmbeddingWrite = {base_url: String(values.base_url), model: String(values.model), dims: Number(values.dims), api_key: String(values.api_key || "") || undefined, batch_size: Number(values.batch_size), rpm: Number(values.rpm), tpm: Number(values.tpm), timeout_ms: Number(values.timeout_ms), enabled: Boolean(values.enabled), version: config?.version ?? 0}
      if (body.api_key) setPending({body, probe: testing})
      else if (testing) await probe(body)
      else await save(body)
    }}><div className="flex flex-wrap gap-3"><Button type="submit" disabled={busy} onClick={() => {probeSubmission.current = false}}>{t("save")}</Button><Button type="submit" variant="outline" disabled={busy} onClick={() => {probeSubmission.current = true}}>{t("knowledgeEmbeddingProbe")}</Button></div></AutoForm>
    <Button variant="outline" disabled={busy || !config} onClick={() => void action(async () => {const batch = await nativeKnowledge.backfill(tenant); if (batch.error_code) throw new Error(batch.error_code); setStatus(`${t("knowledgeEmbeddingBackfillStatus")} ${batch.processed} / ${batch.remaining} · ${(batch.coverage_ppm / 10000).toFixed(1)}%`)})}>{t("knowledgeEmbeddingBackfill")}</Button>
    {pending && <ReauthenticationDialog purpose="knowledge_embedding_write" target={`tenant:${tenant}:embedding-config`} onClose={() => setPending(null)} onVerified={async proof => pending.probe ? probe({...pending.body, reauth_proof: proof}) : save({...pending.body, reauth_proof: proof})}/>}
  </details>
}
