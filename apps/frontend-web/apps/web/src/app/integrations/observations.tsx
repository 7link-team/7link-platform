import { useEffect, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { integrationsApi, observationsPath, type Page } from "./api"
import { observationMessages } from "./messages"

type Locale = keyof typeof observationMessages
export type Observation = {
  id: string
  outcome: "REFERENCE_ONLY"
  observation: {
    event_id: string
    source: "CODEUP" | "FLOW"
    repository: string
    pipeline_id: string
    run_id: string
    commit: string
    kind: string
    title: string
    references: string[]
    authority_source: string
    trusted_verification: false
    local_acceptance: false
    stage_results: {
      name: string
      status: string
      redlines: {
        metric: string
        op: string
        threshold: string
        value: string
        passed: boolean
      }[]
    }[]
    manual_gates: {
      name: string
      mode: "AND" | "OR"
      approvers: string[]
      result: string
    }[]
  }
}
export function ObservationEvidence({
  rows,
  locale = "en-US",
}: {
  rows: Observation[]
  locale?: Locale
}) {
  const labels = observationMessages[locale]
  const statuses: Record<string, string> = {
    PASSED: labels.passed,
    FAILED: labels.failed,
    PENDING: labels.pending,
    RUNNING: labels.running,
    CANCELLED: labels.cancelled,
    SKIPPED: labels.skipped,
  }
  if (
    rows.some(
      (row) =>
        row.outcome !== "REFERENCE_ONLY" ||
        row.observation.trusted_verification !== false ||
        row.observation.local_acceptance !== false
    )
  )
    return <Alert>{labels.invalid}</Alert>
  return (
    <div className="min-w-0 space-y-4">
      <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">
        {labels.reference}
      </p>
      {rows.length === 0 ? (
        <p>{labels.empty}</p>
      ) : (
        <ul className="divide-y">
          {rows.map(({ id, observation: value }) => (
            <li
              key={id}
              className="min-w-0 space-y-3 py-4 [overflow-wrap:anywhere] break-words"
            >
              <p className="font-medium">
                {value.title || value.pipeline_id || value.repository}{" "}
                {value.run_id}
              </p>
              <p className="text-sm">
                {labels.source}: {value.authority_source}
              </p>
              {value.commit && (
                <p className="text-sm">
                  {labels.commit}: <code>{value.commit}</code>
                </p>
              )}
              {value.references.length > 0 && (
                <p>
                  {labels.references}: {value.references.join(", ")}
                </p>
              )}
              {value.stage_results.length > 0 && (
                <div>
                  <h4 className="font-medium">{labels.stages}</h4>
                  <ul className="space-y-2">
                    {value.stage_results.map((stage, index) => (
                      <li key={index}>
                        {stage.name} · {statuses[stage.status] ?? stage.status}
                        <ul className="pl-4 text-sm">
                          {stage.redlines.map((line, i) => (
                            <li key={i}>
                              {line.metric}: {line.value} {line.op}{" "}
                              {line.threshold} ·{" "}
                              {line.passed ? labels.passed : labels.failed}
                            </li>
                          ))}
                        </ul>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {value.manual_gates.length > 0 && (
                <div>
                  <h4 className="font-medium">{labels.gates}</h4>
                  <ul>
                    {value.manual_gates.map((gate, index) => (
                      <li key={index}>
                        {gate.name} · {gate.mode} ·{" "}
                        {statuses[gate.result] ?? gate.result}
                        <p className="text-sm text-muted-foreground">
                          {gate.approvers.join(", ")}
                        </p>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
export function IntegrationObservations({
  tenant,
  connection,
  mapping,
}: {
  tenant: string
  connection: string
  mapping: string
}) {
  const language = useLocale()
  const locale: Locale =
    language === "zh-CN" || language === "ms-MY" ? language : "en-US"
  const labels = observationMessages[locale]
  const [page, setPage] = useState<Page<Observation>>({
    items: [],
    has_more: false,
  })
  const [cursor, setCursor] = useState("")
  const [history, setHistory] = useState<string[]>([])
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    void integrationsApi
      .list<Observation>(
        observationsPath(tenant, connection, mapping),
        cursor,
        controller.signal
      )
      .then(
        (value) => {
          if (!controller.signal.aborted) {
            setPage(value)
            setError("")
            setLoading(false)
          }
        },
        (cause) => {
          if (!controller.signal.aborted) {
            setPage({ items: [], has_more: false })
            setError(cause instanceof Error ? cause.message : String(cause))
            setLoading(false)
          }
        }
      )
    return () => controller.abort()
  }, [tenant, connection, mapping, cursor, revision])
  return (
    <section className="min-w-0 space-y-3 border-t pt-4">
      <h3 className="font-semibold">{labels.title}</h3>
      <Button
        className="min-h-11"
        variant="outline"
        disabled={loading}
        onClick={() => {
          setLoading(true)
          setRevision((value) => value + 1)
        }}
      >
        {labels.refresh}
      </Button>
      {error && <Alert>{error}</Alert>}
      {loading ? (
        <p role="status">{labels.loading}</p>
      ) : (
        <ObservationEvidence rows={page.items} locale={locale} />
      )}
      <div className="flex flex-wrap gap-2">
        <Button
          className="min-h-11"
          variant="outline"
          disabled={loading || history.length === 0}
          onClick={() => {
            setCursor(history.at(-1) ?? "")
            setHistory(history.slice(0, -1))
            setLoading(true)
          }}
        >
          {labels.previous}
        </Button>
        <Button
          className="min-h-11"
          variant="outline"
          disabled={loading || !page.has_more || !page.next_cursor}
          onClick={() => {
            setHistory([...history, cursor])
            setCursor(page.next_cursor ?? "")
            setLoading(true)
          }}
        >
          {labels.next}
        </Button>
      </div>
    </section>
  )
}
