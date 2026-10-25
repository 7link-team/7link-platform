import { useEffect, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { intakeChangeApi, type IntakeLedgerSource, type TriageDraft } from "./intake-api"

export function IntakeTriage({ source, project, onSaved }: { source: IntakeLedgerSource; project: string; onSaved: () => void }) {
  const t = useTranslations("iam")
  const prior = source.triage?.draft
  const [summary, setSummary] = useState(prior?.summary ?? "")
  const [draft, setDraft] = useState(prior?.requirement_draft ?? "")
  const [criteria, setCriteria] = useState(prior?.criteria?.join("\n") ?? "")
  const [risks, setRisks] = useState(prior?.risks?.join("\n") ?? "")
  const [questions, setQuestions] = useState(prior?.questions?.join("\n") ?? "")
  const [size, setSize] = useState<TriageDraft["size"]>(prior?.size ?? "UNKNOWN")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const pending = useRef<{ identity: string; key: string } | null>(null)
  const request = useRef<AbortController | null>(null)
  useEffect(() => () => request.current?.abort(), [])
  const lines = (text: string) => text.split(/\r?\n/).map(value => value.trim()).filter(Boolean)
  const fields = [
    { key: "summary", label: "intakeTriageSummary", value: summary, update: setSummary, max: 2000, required: true },
    { key: "draft", label: "intakeTriageDraft", value: draft, update: setDraft, max: 8000, required: true },
    { key: "criteria", label: "intakeTriageCriteria", value: criteria, update: setCriteria, max: 20000, required: true },
    { key: "risks", label: "intakeTriageRisks", value: risks, update: setRisks, max: 20000, required: false },
    { key: "questions", label: "intakeTriageQuestions", value: questions, update: setQuestions, max: 20000, required: false },
  ]
  return <form className="space-y-4 border-t pt-4" aria-label={t("intakeTriageTitle")} onSubmit={event => {
    event.preventDefault()
    if (busy) return
    const body: TriageDraft = { summary, requirement_draft: draft, criteria: lines(criteria), risks: lines(risks), questions: lines(questions), size }
    const identity = JSON.stringify({ project, id: source.id, version: source.version, body })
    if (pending.current?.identity !== identity) pending.current = { identity, key: crypto.randomUUID() }
    const controller = new AbortController()
    request.current = controller
    setBusy(true); setError("")
    void intakeChangeApi.triage(project, source, body, pending.current.key, controller.signal).then(() => {
      if (!controller.signal.aborted) onSaved()
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("clientSaveFailed"))
    }).finally(() => { if (!controller.signal.aborted) setBusy(false) })
  }}>
    <h3 className="font-medium">{t("intakeTriageTitle")}</h3>
    <p className="text-sm text-muted-foreground">{t("intakeTriageDraftOnly")}</p>
    <fieldset disabled={busy} className="grid gap-4 sm:grid-cols-2">
      {fields.map(field => <label key={field.key} className={field.key === "draft" ? "space-y-1 sm:col-span-2" : "space-y-1"}>
        <span className="text-sm font-medium">{t(field.label)}</span>
        <Textarea name={field.key} value={field.value} onChange={event => field.update(event.target.value)} maxLength={field.max} required={field.required} rows={3} />
      </label>)}
      <label className="space-y-1"><span className="text-sm font-medium">{t("intakeTriageSize")}</span>
        <select name="size" className="min-h-11 w-full rounded-md border bg-background px-3" value={size} onChange={event => setSize(event.target.value as TriageDraft["size"])}>
          {(["UNKNOWN", "XS", "S", "M", "L", "XL"] as const).map(value => <option key={value} value={value}>{value === "UNKNOWN" ? t("intakeTriageUnknown") : value}</option>)}
        </select>
      </label>
    </fieldset>
    {error && <Alert>{error} {t("intakeTriageRetry")}</Alert>}
    <Button type="submit" disabled={busy || !summary.trim() || !draft.trim() || !lines(criteria).length}>{busy ? t("loading") : t("intakeTriageSave")}</Button>
  </form>
}
