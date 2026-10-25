import { useEffect, useMemo, useRef, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { apiRoute } from "../../lib/api-route"
import type { components } from "../../lib/api-schema"
import { iamApi } from "../../lib/iam-api"
import { useRequestContract } from "../../lib/use-request-contract"

type WakeConfig = components["schemas"]["WakeConfigView"]

export function AgentWakeConfig({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const route = useMemo(() => apiRoute("put", "/tenants/{tenant_id}/agents/{agent_principal_id}/wake-config", { path: { tenant_id: tenant, agent_principal_id: agent } }), [tenant, agent])
  const metadata = useRequestContract(route)
  const [config, setConfig] = useState<WakeConfig | null>(null)
  const [secret, setSecret] = useState("")
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [busy, setBusy] = useState(false)
  const attempt = useRef<{ body: string; key: string } | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    const load = async () => {
      try {
        const result = await iamApi.deliveryResultRequest<WakeConfig>(apiRoute("get", "/tenants/{tenant_id}/agents/{agent_principal_id}/wake-config", { path: { tenant_id: tenant, agent_principal_id: agent } }), { signal: controller.signal })
        if (!controller.signal.aborted) { setConfig(result); setError("") }
      } catch (e) { if (!controller.signal.aborted) setError((e as Error).message) }
    }
    void load()
    return () => controller.abort()
  }, [tenant, agent, revision])
  return <section className="space-y-4 border-t pt-5" aria-label={t("agentWakeConfig")}>
    <h3 className="font-semibold">{t("agentWakeConfig")}</h3>
    <p className="text-sm text-muted-foreground">{t("agentWakeHelp")}</p>
    {error && <Alert>{error}</Alert>}
    {metadata.failed && <Button variant="outline" onClick={metadata.retry}>{t("agentRefresh")}</Button>}
    {!config && <Button variant="outline" onClick={() => setRevision(value => value + 1)}>{t("agentRefresh")}</Button>}
    {config && <>
      <p className="text-sm">{t("agentWakeFailures", { count: config.consecutive_failures })}</p>
      <AutoForm key={`${tenant}:${agent}:${config.version}`} contract={metadata.contract} fields={{
        wake_mode: { name: t("agentWakeMode"), type: "select", value: config.wake_mode, config: { options: ["CHANNEL", "POLL", "WEBHOOK"].map(value => ({ value, label: t(`agentWakeMode_${value}`) })) } },
        webhook_url: { name: t("agentWakeURL"), type: "text", value: config.webhook_url ?? "", config: { type: "url" } },
        rotate_secret: { name: t("agentWakeRotate"), type: "switch", value: false },
      }} onSubmit={async values => {
        setBusy(true)
        setError("")
        const body = { wake_mode: String(values.wake_mode), webhook_url: values.wake_mode === "WEBHOOK" ? String(values.webhook_url ?? "") : "", rotate_secret: values.wake_mode === "WEBHOOK" && Boolean(values.rotate_secret) }
        const identity = JSON.stringify({ version: config.version, body })
        const key = attempt.current?.body === identity ? attempt.current.key : crypto.randomUUID()
        attempt.current = { body: identity, key }
        try {
          const result = await iamApi.deliveryResultRequest<WakeConfig>(route, { headers: { "Idempotency-Key": key, "If-Match": String(config.version) }, body: JSON.stringify(body) })
          setSecret(result.secret ?? "")
          setConfig(result)
          attempt.current = null
        } catch (e) { setError((e as Error).message); throw e } finally { setBusy(false) }
      }}>
        <Button type="submit" disabled={busy || !metadata.contract}>{t("agentSave")}</Button>
      </AutoForm>
    </>}
    {secret && <div className="space-y-3 border p-4" role="status">
      <p>{t("agentWakeSecretOnce")}</p>
      <code className="block break-all select-all">{secret}</code>
      <Button variant="outline" onClick={() => setSecret("")}>{t("agentWakeSecretDismiss")}</Button>
    </div>}
  </section>
}
