import {useCallback, useEffect, useState} from "react"
import {AutoTable} from "@workspace/ui/components/auto-table"
import {Button} from "@workspace/ui/components/button"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {modelPricesApi, validPriceAmount, type ModelPrice, type ModelPricePage, type PriceAmounts} from "./model-prices-api"
const zero: PriceAmounts = {input_micros_per_mtok: "0", output_micros_per_mtok: "0", cache_read_micros_per_mtok: "0", cache_write_micros_per_mtok: "0"}
export function ModelPrices({tenantID, manage}: {tenantID: string; manage: boolean}) {
 const t = useTranslations("iam")
 const [page, setPage] = useState<ModelPricePage | null>(null)
 const [cursor, setCursor] = useState<string>()
 const [previous, setPrevious] = useState<(string | undefined)[]>([])
 const [selected, setSelected] = useState<ModelPrice | null>(null)
 const [runtime, setRuntime] = useState<ModelPrice["runtime_kind"]>("CODEX")
 const [model, setModel] = useState("")
 const [amounts, setAmounts] = useState<PriceAmounts>(zero)
 const [error, setError] = useState("")
 const [busy, setBusy] = useState(false)
 const [loading, setLoading] = useState(true)
 const [refresh, setRefresh] = useState(0)
 useEffect(() => {const controller = new AbortController();modelPricesApi.list(tenantID, cursor, controller.signal).then(value => {if (!controller.signal.aborted) {setPage(value); setLoading(false)}}).catch(cause => {if (!controller.signal.aborted) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"));setLoading(false)}});return () => controller.abort()}, [tenantID, cursor, refresh, t])
 const reset = useCallback(() => {setSelected(null);setModel("");setAmounts(zero);setError("")}, [])
 const reload = () => {setLoading(true);setError("");setRefresh(value => value + 1)}
 return <section className="mt-6 min-w-0 space-y-3 border-t pt-4" aria-label={t("fleetPrices")}>
  <h3 className="font-medium">{t("fleetPrices")}</h3><p className="text-sm text-muted-foreground">{t("fleetPricesHint")}</p>
  {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
  <div className="overflow-x-auto"><AutoTable data={page?.items ?? []} rowKey="id" pagination={false} exportable={false} emptyContent={t("fleetNoPrices")} columns={[{field: "runtime_kind", name: t("fleetRuntime")}, {field: "model_id", name: t("fleetModel")}, ...Object.keys(zero).map(field => ({field, name: t(`fleetPrice_${field}`)})), ...(manage ? [{field: "actions", name: t("actions"), render: (_: unknown, row: ModelPrice) => <Button className="min-h-11" variant="outline" disabled={busy} onClick={() => {setSelected(row);setRuntime(row.runtime_kind);setModel(row.model_id);setAmounts({input_micros_per_mtok: row.input_micros_per_mtok, output_micros_per_mtok: row.output_micros_per_mtok, cache_read_micros_per_mtok: row.cache_read_micros_per_mtok, cache_write_micros_per_mtok: row.cache_write_micros_per_mtok});setError("")}}>{t("edit")}</Button>}] : [])]}/></div>
  <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={loading || busy || previous.length === 0} onClick={() => {setLoading(true);setCursor(previous.at(-1));setPrevious(old => old.slice(0, -1))}}>{t("previous")}</Button><Button variant="outline" disabled={loading || busy || !page?.has_more} onClick={() => {setLoading(true);setPrevious(old => [...old, cursor]);setCursor(page?.next_cursor)}}>{t("next")}</Button><Button variant="outline" disabled={loading || busy} onClick={reload}>{t("refresh")}</Button></div>
  {manage && <form className="space-y-3" onSubmit={event => {event.preventDefault();if (busy || !model.trim() || Object.values(amounts).some(value => !validPriceAmount(value))) return;setBusy(true);setError("");void modelPricesApi.save(tenantID, runtime, model.trim(), selected?.version ?? 0, amounts).then(value => {setSelected(value);reload()}).catch(cause => setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))).finally(() => setBusy(false))}}>
   <div className="grid gap-3 sm:grid-cols-2"><label className="flex flex-col gap-1 text-sm">{t("fleetRuntime")}<select className="min-h-11 rounded border bg-background px-3" value={runtime} disabled={busy || !!selected} onChange={event => setRuntime(event.target.value as ModelPrice["runtime_kind"])}><option>CODEX</option><option>CLAUDE_CODE</option></select></label><label className="flex flex-col gap-1 text-sm">{t("fleetModel")}<input className="min-h-11 rounded border bg-background px-3" maxLength={160} required pattern="\S+" value={model} disabled={busy || !!selected} onChange={event => setModel(event.target.value)}/></label>{(Object.keys(zero) as (keyof PriceAmounts)[]).map(field => <label className="flex flex-col gap-1 text-sm" key={field}>{t(`fleetPrice_${field}`)}<input className="min-h-11 rounded border bg-background px-3" required inputMode="numeric" pattern="(0|[1-9][0-9]{0,18})" maxLength={19} value={amounts[field]} disabled={busy} onChange={event => setAmounts(old => ({...old, [field]: event.target.value}))}/></label>)}</div>
   <div className="flex flex-wrap gap-2"><Button type="submit" disabled={busy || Object.values(amounts).some(value => !validPriceAmount(value))}>{t("save")}</Button><Button type="button" variant="outline" disabled={busy} onClick={reset}>{t("fleetNewPrice")}</Button>{selected && <Button type="button" variant="destructive" disabled={busy} onClick={() => {if (!window.confirm(t("fleetDeletePriceConfirm"))) return;setBusy(true);setError("");void modelPricesApi.remove(tenantID, selected).then(() => {reset();reload()}).catch(cause => setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))).finally(() => setBusy(false))}}>{t("delete")}</Button>}</div>
  </form>}
 </section>
}
