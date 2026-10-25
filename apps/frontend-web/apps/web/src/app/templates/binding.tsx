import { useEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { bindingLoadError } from "./api"
import { useAllowed } from "../../lib/permissions"
import type { WorkflowInstance } from "./workflow"
import { TemplateVerifierResults } from "./verifier-results"
import { ProjectProviderBinding } from "./providers"

type Binding = WorkflowInstance & {
	 template_state?:string
  version: number
  brief_overrides: Record<string, unknown>
  effective_parameters: Record<string, unknown>
  overrides_approved_by?: string
}
type Diff = { added: unknown[]; removed: unknown[]; changed: unknown[] }
export function TemplateBinding({
  tenant,
  project,
}: {
  tenant: string
  project: string
}) {
  const t = useTranslations("iam"),
    allowed = useAllowed()
  const canRead = allowed("template_read") && allowed("work_item_read"),
    canManage = allowed("project_manage")
  const [binding, setBinding] = useState<Binding | null>(null)
  const [overrides, setOverrides] = useState("{}"),
    [target, setTarget] = useState("")
  const [ack, setAck] = useState(false),
    [diff, setDiff] = useState<Diff | null>(null)
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false)
  const pending = useRef(false),
    retry = useRef<{ identity: string; key: string } | null>(null)
  const root = `/tenants/${encodeURIComponent(tenant)}`,
    base = `${root}/entities/${encodeURIComponent(project)}/template-binding`
  useEffect(() => {
    const controller = new AbortController()
    if (canRead)
      void iamApi
        .deliveryResultRequest<Binding>(
          { method: "GET", url: base },
          { signal: controller.signal }
        )
        .then((value) => {
          if (!controller.signal.aborted) {
            setBinding(value)
            setOverrides(JSON.stringify(value.brief_overrides, null, 2))
          }
        })
        .catch((e) => {
          if (!controller.signal.aborted) setError(bindingLoadError(e))
        })
    return () => controller.abort()
  }, [base, canRead])
  const perform = async (kind: "DIFF" | "UPGRADE" | "OVERRIDES") => {
    if (!binding || pending.current) return
    pending.current = true
    setBusy(true)
    setError("")
    try {
      if (kind === "DIFF") {
        setDiff(
          await iamApi.deliveryResultRequest<Diff>({
            method: "GET",
            url: `${root}/template-versions/${encodeURIComponent(target)}/diff?against=${encodeURIComponent(binding.template_version_id)}`,
          })
        )
      } else {
        const parsed: unknown =
          kind === "OVERRIDES" ? JSON.parse(overrides) : null
        if (
          kind === "OVERRIDES" &&
          (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
        )
          throw new Error(t("bindingObjectRequired"))
        const body = JSON.stringify(
          kind === "OVERRIDES"
            ? { overrides: parsed }
            : { target_version_id: target, acknowledge_breaking: ack }
        )
        const identity = `${kind}:${binding.version}:${body}`
        if (retry.current?.identity !== identity)
          retry.current = { identity, key: crypto.randomUUID() }
        const value = await iamApi.deliveryResultRequest<Binding>(
          {
            method: kind === "OVERRIDES" ? "PATCH" : "PUT",
            url: `${base}/${kind === "OVERRIDES" ? "brief-overrides" : "upgrade"}`,
          },
          {
            body,
            headers: {
              "If-Match": `"${binding.version}"`,
              "Idempotency-Key": retry.current.key,
            },
          }
        )
        setBinding(value)
        setOverrides(JSON.stringify(value.brief_overrides, null, 2))
        retry.current = null
        setDiff(null)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      pending.current = false
      setBusy(false)
    }
  }
  if (!canRead) return null
  return (
    <section className="space-y-4 border-t p-4" aria-label={t("bindingTitle")}>
      <h2 className="font-semibold">{t("bindingTitle")}</h2>
      <p className="text-sm text-muted-foreground">{t("bindingHistoryHint")}</p>
      {error && <Alert>{error}</Alert>}
      {!binding ? (
        <div className="space-y-2"><p>{t("bindingLoadHint")}</p>{canManage && <Link className="underline underline-offset-4" to={`/admin/${encodeURIComponent(tenant)}/templates`}>{t("templateLibrary")}</Link>}</div>
      ) : (
        <>
          <p>
            {t("bindingCurrent")}: {binding.template_version_id} · v
            {binding.version}
          </p>
          <code className="block text-xs break-all">{binding.hash}</code>
          {binding.template_state === "DEPRECATED" && <p role="status" className="rounded-md border p-3 text-sm">{t("templateDeprecatedNotice")}</p>}
          <details>
            <summary>{t("bindingEffective")}</summary>
            <pre className="text-xs break-all whitespace-pre-wrap">
              {JSON.stringify(binding.effective_parameters, null, 2)}
            </pre>
          </details>
          <form
            className="space-y-2"
            onSubmit={(e) => {
              e.preventDefault()
              void perform("DIFF")
            }}
          >
            <label className="block">
              <span>{t("bindingTarget")}</span>
              <Input
                required
                inputMode="numeric"
                pattern="[1-9][0-9]*"
                value={target}
                disabled={busy}
                onChange={(e) => {
                  setTarget(e.target.value)
                  setDiff(null)
                  setAck(false)
                }}
              />
            </label>
            <Button
              type="submit"
              variant="outline"
              disabled={busy || !/^[1-9][0-9]*$/.test(target)}
            >
              {t("bindingCompare")}
            </Button>
          </form>
          {diff && (
            <div className="space-y-2">
              <pre className="max-h-80 overflow-auto text-xs break-all whitespace-pre-wrap">
                {JSON.stringify(diff, null, 2)}
              </pre>
              {canManage && (
                <>
                  <label className="flex gap-2">
                    <input
                      type="checkbox"
                      checked={ack}
                      disabled={busy}
                      onChange={(e) => setAck(e.target.checked)}
                    />
                    {t("bindingMajorAck")}
                  </label>
                  <Button
                    disabled={busy}
                    onClick={() => void perform("UPGRADE")}
                  >
                    {t("bindingUpgrade")}
                  </Button>
                </>
              )}
            </div>
          )}
          {canManage && (
            <form
              className="space-y-2"
              onSubmit={(e) => {
                e.preventDefault()
                void perform("OVERRIDES")
              }}
            >
              <label className="block">
                <span>{t("bindingOverrides")}</span>
                <Textarea
                  required
                  maxLength={12000}
                  rows={5}
                  value={overrides}
                  disabled={busy}
                  onChange={(e) => setOverrides(e.target.value)}
                />
              </label>
              <p className="text-sm text-muted-foreground">
                {t("bindingOverrideHint")}
              </p>
              <Button type="submit" disabled={busy}>
                {t("bindingApprove")}
              </Button>
            </form>
          )}
          <ProjectProviderBinding key={binding.id} tenantID={tenant} projectID={project} canRead={allowed("template_read")} canManage={canManage}/>
          <TemplateVerifierResults
            key={`${binding.id}:${binding.version}`}
            tenant={tenant}
            works={binding.runs.flatMap((run) =>
              run.work_item_id ? [run.work_item_id] : []
            )}
          />
        </>
      )}
    </section>
  )
}
