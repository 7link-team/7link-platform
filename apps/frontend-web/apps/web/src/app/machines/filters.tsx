import {useSearchParams} from "react-router"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {machineListFilters} from "./filters-state"

export function MachineFilters() {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const filters = machineListFilters(params)
  const change = (name: string, value: string) => setParams(previous => {
    const next = new URLSearchParams(previous)
    if (value) next.set(name, value)
    else next.delete(name)
    next.delete("cursor")
    return next
  })
  return <div className="flex shrink-0 flex-wrap items-end gap-3">
    <label className="flex min-w-40 flex-col gap-1 text-sm">{t("fleetState")}<select className="min-h-11 rounded-md border bg-background px-3" value={filters.state ?? ""} onChange={event => change("state", event.target.value)}><option value="">{t("runtimeAllStates")}</option>{["ONLINE", "OFFLINE", "DRAINING", "REVOKED"].map(state => <option key={state} value={state}>{t(`fleetState_${state}`)}</option>)}</select></label>
    <label className="flex min-w-40 flex-col gap-1 text-sm">{t("fleetRuntime")}<select className="min-h-11 rounded-md border bg-background px-3" value={filters.runtime ?? ""} onChange={event => change("runtime", event.target.value)}><option value="">{t("fleetAllRuntimes")}</option><option value="CODEX">Codex</option><option value="CLAUDE_CODE">Claude Code</option></select></label>
    <label className="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input type="checkbox" className="size-5" checked={filters.free_slots === true} onChange={event => change("free_slots", event.target.checked ? "true" : "")}/>{t("fleetFreeSlotsOnly")}</label>
  </div>
}
