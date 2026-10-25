import { useTranslations } from "@workspace/ui/i18n/intl-provider"

type Progress = {no_progress_count: number; dependency_miss_count: number; stop_reason?: string; similarity_ratio_ppm?: number; similarity_union: number}
type Policy = {no_progress_limit: number; dependency_miss_limit: number}
export function ExecutionProgress({state, progress, policy, templateVersion}: {state: string; progress?: Progress | null; policy?: Policy | null; templateVersion?: string}) {
 const t = useTranslations("iam")
 return <section className="space-y-2 rounded-lg border p-4" aria-label={t("executionProgressTitle")}>
  <h2 className="font-medium">{t("executionProgressTitle")}</h2>
  <p className="text-sm">{policy ? t("executionProgressConfigured") : t("executionProgressUnconfigured")}{templateVersion && <> / {templateVersion}</>}</p>
  {progress && <dl className="grid gap-2 text-sm sm:grid-cols-2"><div><dt>{t("executionNoProgressCount")}</dt><dd>{progress.no_progress_count} / {policy?.no_progress_limit || "—"}</dd></div><div><dt>{t("executionDependencyMissCount")}</dt><dd>{progress.dependency_miss_count} / {policy?.dependency_miss_limit || "—"}</dd></div><div><dt>{t("executionSimilarity")}</dt><dd>{progress.similarity_ratio_ppm == null ? "—" : `${progress.similarity_ratio_ppm / 10000}%`} / {progress.similarity_union}</dd></div></dl>}
  {progress?.stop_reason && <p role="status" className="text-sm">{t(progress.stop_reason === "AUTONOMY_NO_PROGRESS" ? "executionStopNoProgress" : progress.stop_reason === "AUTONOMY_DEPENDENCY_MISSING" ? "executionStopDependency" : "executionStopSpinning")} · {t(state === "PAUSED" ? "executionProgressPaused" : ["SUCCEEDED", "FAILED", "CANCELLED", "INTERRUPTED"].includes(state) ? "executionProgressEnded" : "executionProgressPausePending")}</p>}
 </section>
}
