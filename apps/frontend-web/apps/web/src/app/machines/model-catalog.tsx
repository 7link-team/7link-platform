import {useEffect, useRef, useState} from "react"
import {Button} from "@workspace/ui/components/button"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {modelCatalogApi, type ModelCatalog} from "./model-catalog-api"

export function ModelCatalogEditor({machineID, runtime, manage}: {machineID: string; runtime: ModelCatalog["runtime"]; manage: boolean}) {
  const t = useTranslations("iam")
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null)
  const [models, setModels] = useState("")
  const [families, setFamilies] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [saved, setSaved] = useState(false)
  const pending = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController(); pending.current = controller
    modelCatalogApi.get(machineID, runtime, controller.signal).then(value => {if (!controller.signal.aborted) {setCatalog(value); setModels(value.manual_ids.join("\n")); setFamilies(value.families)}}).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))})
    return () => pending.current?.abort()
  }, [machineID, runtime, t])
  const ids = [...new Set([...catalog?.observed_ids ?? [], ...models.split("\n").map(id => id.trim()).filter(Boolean)])].sort()
  return <section className="my-4 min-w-0 border-t pt-4" aria-label={`${runtime} ${t("fleetManualModels")}`}>
    <h3 className="font-medium">{runtime} · {t("fleetManualModels")}</h3>
    <p className="my-2 text-sm text-muted-foreground">{t("fleetManualModelsHint")}</p>
    {error && <Alert>{error}</Alert>}{saved && <p role="status">{t("fleetManualModelsSaved")}</p>}
    {!catalog ? <p role="status">{error ? t("fleetUnavailableLabel") : t("loading")}</p> : manage ? <form className="space-y-3" onSubmit={event => {
      event.preventDefault(); if (busy) return
      const controller = new AbortController(); pending.current = controller
      setBusy(true); setError(""); setSaved(false)
      const modelIDs = models.split("\n").map(id => id.trim()).filter(Boolean)
      const overrides = Object.fromEntries(Object.entries(families).filter(([id, family]) => family && ids.includes(id)))
      void modelCatalogApi.replace(machineID, runtime, {expected_version: catalog.version, model_ids: modelIDs, families: overrides}, controller.signal).then(value => {if (!controller.signal.aborted) {setCatalog(value); setModels(value.manual_ids.join("\n")); setFamilies(value.families); setSaved(true)}}).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}).finally(() => {if (!controller.signal.aborted) setBusy(false)})
    }}>
      <label className="flex flex-col gap-1 text-sm">{t("fleetManualModelIDs")}<textarea className="min-h-28 rounded border bg-background p-3" value={models} disabled={busy} onChange={event => {setModels(event.target.value); setSaved(false)}} /></label>
      <fieldset className="space-y-2" disabled={busy}><legend className="mb-2 text-sm font-medium">{t("fleetModelFamilies")}</legend>{ids.map(id => <label key={id} className="flex flex-wrap items-center justify-between gap-2 text-sm"><span className="min-w-0 break-all">{id}{catalog.resolved_families?.[id] && `: ${catalog.resolved_families[id]}`}</span><select className="min-h-11 rounded border bg-background px-3" value={families[id] ?? ""} onChange={event => {setFamilies(old => ({...old, [id]: event.target.value})); setSaved(false)}}><option value="">{t("fleetModelFamilyAutomatic")}</option>{["openai", "anthropic", "google"].map(value => <option key={value}>{value}</option>)}</select></label>)}</fieldset>
      <Button type="submit" disabled={busy}>{t("save")}</Button>
    </form> : <><p className="text-sm">{t("fleetManualModelIDs")}</p><ul className="list-inside list-disc break-all text-sm">{catalog.manual_ids.map(id => <li key={id}>{id}</li>)}</ul>{Object.entries(catalog.resolved_families ?? catalog.families).map(([id, family]) => <p className="break-all text-sm" key={id}>{id} · {family}</p>)}</>}
  </section>
}
