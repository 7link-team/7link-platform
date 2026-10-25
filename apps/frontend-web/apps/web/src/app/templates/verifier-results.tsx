import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { templatesApi, type VerifierResult } from "./api"

export function VerifierResultRows({ rows }: { rows: VerifierResult[] }) {
  const t = useTranslations("iam")
  return (
    <div className="space-y-3">
      {rows.map((row) => (
        <article
          key={row.task_id}
          className="space-y-2 rounded-md border p-3 text-sm"
        >
          <div className="flex flex-wrap justify-between gap-2">
            <h4 className="font-medium">{row.verifier_key}</h4>
            <span>{t(`bindingVerifierVerdict_${row.verdict}`)}</span>
          </div>
          <p>
            {row.kind} · {row.runner} · {row.stage} · {row.reason}
          </p>
          <p>
            {t("bindingVerifierCalibration")}: {row.calibration_state}
          </p>
          {row.calibration_state !== "CALIBRATED" && (
            <p className="font-medium">{t("bindingVerifierAdvisory")}</p>
          )}
          <p>{row.limitations}</p>
          {row.template_pin && <dl className="space-y-2">
            <div><dt>{t("templateVerifierProtocol")}</dt><dd>{row.template_pin.verifier.plugin_key || "runner-v1"} · v{row.template_pin.verifier.version}</dd></div>
            <div><dt>{t("templateMetadataTools")}</dt><dd>{row.template_pin.verifier.required_tools?.join(", ") || t("templateCalibrationNotApplicable")}</dd></div>
          </dl>}
          <dl className="grid gap-2 sm:grid-cols-2">
            <div>
              <dt>{t("bindingVerifierVersion")}</dt>
              <dd>{row.tool_version}</dd>
            </div>
            {row.duration_ms !== undefined && (
              <div>
                <dt>{t("bindingVerifierDuration")}</dt>
                <dd>{row.duration_ms} ms</dd>
              </div>
            )}
          </dl>
          {row.template_pin && (
            <p className="break-all">
              {t("bindingCurrent")}: {row.template_pin.template_version_id} ·{" "}
              {row.template_pin.definition_hash}
            </p>
          )}
          <details>
            <summary>{t("bindingVerifierEvidence")}</summary>
            <pre className="text-xs break-all whitespace-pre-wrap">
              {JSON.stringify(
                {
                  result_id: row.result_id,
                  criterion_id: row.criterion_id,
                  params_resolved: row.params_resolved,
                  metrics: row.metrics,
                  evidence_refs: row.evidence_refs,
                },
                null,
                2
              )}
            </pre>
          </details>
        </article>
      ))}
    </div>
  )
}

export function TemplateVerifierResults({
  tenant,
  works,
}: {
  tenant: string
  works: string[]
}) {
  const t = useTranslations("iam")
  const [work, setWork] = useState(works[0] ?? ""),
    [stage, setStage] = useState("")
  const [cursor, setCursor] = useState(""),
    [next, setNext] = useState("")
  const [rows, setRows] = useState<VerifierResult[]>([]),
    [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [settledRequest, setSettledRequest] = useState("")
  const selected = works.includes(work) ? work : (works[0] ?? "")
  const requestIdentity = JSON.stringify([
    tenant,
    selected,
    stage,
    cursor,
    revision,
  ])
  const loading = Boolean(selected) && settledRequest !== requestIdentity
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    void templatesApi
      .verifierResults(tenant, selected, stage, cursor, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return
        setRows((prior) =>
          cursor
            ? [
                ...prior,
                ...page.items.filter(
                  (row) => !prior.some((old) => old.task_id === row.task_id)
                ),
              ]
            : page.items
        )
        setNext(page.has_more ? (page.next_cursor ?? "") : "")
        setError("")
      })
      .catch((cause) => {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : String(cause))
      })
      .finally(() => {
        if (!controller.signal.aborted) setSettledRequest(requestIdentity)
      })
    return () => controller.abort()
  }, [tenant, selected, stage, cursor, requestIdentity])
  return (
    <section
      className="space-y-3 border-t pt-4"
      aria-label={t("bindingVerifierResults")}
    >
      <h3 className="font-semibold">{t("bindingVerifierResults")}</h3>
      <p className="text-sm text-muted-foreground">
        {t("bindingVerifierHelp")}
      </p>
      {error && <Alert>{error}</Alert>}
      {!works.length ? (
        <p>{t("bindingVerifierEmpty")}</p>
      ) : (
        <>
          <div className="flex flex-wrap gap-3">
            <label className="grid gap-1 text-sm">
              {t("bindingVerifierWork")}
              <select
                className="h-11 rounded-md border bg-background px-3"
                value={selected}
                onChange={(event) => {
                  setWork(event.target.value)
                  setCursor("")
                  setRows([])
                  setNext("")
                }}
              >
                {works.map((id) => (
                  <option key={id} value={id}>
                    {id}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-1 text-sm">
              {t("bindingVerifierStage")}
              <select
                className="h-11 rounded-md border bg-background px-3"
                value={stage}
                onChange={(event) => {
                  setStage(event.target.value)
                  setCursor("")
                  setRows([])
                  setNext("")
                }}
              >
                <option value="">{t("all")}</option>
                {["AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE"].map(
                  (value) => (
                    <option key={value} value={value}>
                      {value}
                    </option>
                  )
                )}
              </select>
            </label>
            <Button
              type="button"
              variant="outline"
              disabled={loading}
              onClick={() => {
                setCursor("")
                setRevision((value) => value + 1)
              }}
            >
              {t("refresh")}
            </Button>
          </div>
          {loading && <p role="status">{t("loading")}</p>}
          {!loading && !rows.length && !error && (
            <p>{t("bindingVerifierEmpty")}</p>
          )}
          <VerifierResultRows rows={rows} />
          {next && (
            <Button
              type="button"
              variant="outline"
              disabled={loading}
              onClick={() => setCursor(next)}
            >
              {t("bindingVerifierMore")}
            </Button>
          )}
        </>
      )}
    </section>
  )
}
