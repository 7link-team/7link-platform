import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"

export function ProgressPolicyEditor({value, disabled, onChange}: {value: unknown; disabled: boolean; onChange: (value: unknown) => void}) {
 const t = useTranslations("iam")
 const policy = value && typeof value === "object" ? value as Record<string, unknown> : null
 const change = (key: string, next: unknown) => onChange({...policy, [key]: next})
 return <section className="space-y-3 rounded-lg border p-3"><h2 className="font-medium">{t("executionProgressTitle")}</h2><p className="text-sm text-muted-foreground">{t("templateProgressApproval")}</p>
  <label className="flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" checked={Boolean(policy)} disabled={disabled} onChange={e => onChange(e.target.checked ? {no_progress_limit: 0, dependency_miss_limit: 0, observable_paths: [], required_dependency_paths: []} : undefined)}/>{t("templateProgressEnable")}</label>
  {policy && <div className="grid gap-3 sm:grid-cols-2">{([ ["no_progress_limit", "executionNoProgressCount"], ["dependency_miss_limit", "executionDependencyMissCount"] ] as const).map(([key, label]) => <label key={key} className="space-y-1 text-sm">{t(label)}<Input type="number" min={0} step={1} disabled={disabled} value={typeof policy[key] === "number" ? policy[key] : ""} onChange={e => change(key, e.target.value === "" ? 0 : Number(e.target.value))}/></label>)}{([ ["observable_paths", "templateProgressPaths"], ["required_dependency_paths", "templateDependencyPaths"] ] as const).map(([key, label]) => <label key={key} className="space-y-1 text-sm">{t(label)}<Textarea disabled={disabled} value={Array.isArray(policy[key]) ? (policy[key] as string[]).join("\n") : ""} onChange={e => change(key, e.target.value.split("\n").filter(Boolean))}/></label>)}</div>}
 </section>
}
