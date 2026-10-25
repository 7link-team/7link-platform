import { useLocale } from "@workspace/ui/i18n/intl-provider"
import type { components } from "../../lib/api-schema"
import { packageEvidenceLabels } from "./package-evidence-labels"

type Result = components["schemas"]["ResultView"]
type Artifact = components["schemas"]["ClientArtifact"]


function useLabels() {
  const locale = useLocale()
  return packageEvidenceLabels[locale as keyof typeof packageEvidenceLabels] ?? packageEvidenceLabels["en-US"]
}

export function PackageEvidenceMapping({ result }: { result: Result }) {
  const l = useLabels()
  return <section className="grid min-w-0 gap-3" aria-label={l.title}>
    <h3 className="font-medium">{l.title}</h3><p className="text-sm text-muted-foreground">{l.hint}</p>
    {!(result.criteria_map?.length) && <p role="status">{l.missing}</p>}
    {result.criteria_map?.map((criterion, index) => <article key={`${criterion.criterion_id}:${criterion.method}:${index}`} className="grid min-w-0 gap-2 border-l-2 pl-3">
      <p className="break-words">{l.criterion}: {criterion.criterion_id ?? l.unknown} · {l.method}: {criterion.method}</p>
      <p>{l.coverage}: {criterion.coverage} · {l.verdict}: {criterion.verdict}</p>
      <p className="break-all text-sm">{l.verification}: {criterion.verification_ids?.filter(Boolean).join(", ") || l.unknown}</p>
      {criterion.evidence_ids?.map(id => {
        const item = result.evidence?.find(e => id != null && e.id === id)
        return <div key={id ?? "unknown"} className="grid min-w-0 gap-1 text-sm">
          <p>{l.evidence}: {item?.name ?? l.unknown} ({id ?? l.unknown})</p>
          <p className="break-all">{l.version}: {item?.version_id ?? l.unknown}</p>
          <p className="break-all">{l.hash}: {item?.sha256 || l.unknown}</p>
        </div>
      })}
    </article>)}
    {!!result.overrides?.length && <section className="grid min-w-0 gap-3 border p-3" aria-label={l.override}>
      <h4 className="font-medium">{l.override}</h4><p className="text-sm">{l.overrideHint}</p>
      {result.overrides.map((override, index) => <dl key={override.id ?? index} className="grid min-w-0 gap-1 text-sm">
        <dt>{l.actor}</dt><dd className="break-all">{override.actor_id ?? l.unknown}</dd>
        <dt>{l.reason}</dt><dd className="whitespace-pre-wrap break-words">{override.reason}</dd>
        <dt>{l.risk}</dt><dd className="whitespace-pre-wrap break-words">{override.risk}</dd>
        <dt>{l.proof}</dt><dd className="break-all">{override.evidence_ids?.filter(Boolean).join(", ") || l.unknown}</dd>
        <dt>{l.at}</dt><dd><time dateTime={override.created_at}>{override.created_at}</time></dd>
        {override.original_verdicts?.map((original, n) => <div key={n}><dt>{l.verdict}</dt><dd>{original.verdict}</dd><dt>{l.receipt}</dt><dd className="break-all">{original.receipt_hash || l.unknown}</dd></div>)}
      </dl>)}
    </section>}
  </section>
}

// Client-facing projection accepts only the public manifest's named fields.
// Do not render arbitrary JSON: private additions must not silently appear here.
export function PackageArtifactDisclosure({ artifact }: { artifact: Artifact }) {
  const l = useLabels()
  const locale=useLocale()
  const localeLabel=(key:"team"|"engineer")=>({"en-US":{team:"Team",engineer:"Engineer"},"zh-CN":{team:"??",engineer:"???"},"ms-MY":{team:"Pasukan",engineer:"Jurutera"}}[locale as "en-US"|"zh-CN"|"ms-MY"]??{team:"Team",engineer:"Engineer"})[key]
  const overrides = Array.isArray(artifact.verification_overrides) ? artifact.verification_overrides : []
  return <div className="grid min-w-0 gap-2 text-sm">
    <p className="break-all">{l.version}: {artifact.version_id ?? l.unknown}</p>
    <p className="break-all">{l.hash}: {artifact.sha256}</p>
    <p>{l.coverage}: {artifact.coverage}</p><p className="text-muted-foreground">{l.hint}</p>
    {!!overrides.length && <section className="grid min-w-0 gap-2 border-l-2 pl-3" aria-label={l.override}>
      <h4 className="font-medium">{l.override}</h4><p>{l.overrideHint}</p>
      {overrides.map((raw, index) => {
        const entry = raw && typeof raw === "object" ? raw as Record<string, unknown> : {}
        const actor = entry.actor && typeof entry.actor === "object" ? entry.actor as Record<string,unknown> : {}
        const support = Array.isArray(entry.supporting_evidence) ? entry.supporting_evidence : []
        const originals = Array.isArray(entry.original_verdicts) ? entry.original_verdicts : []
        const text = (v: unknown) => typeof v === "string" ? v : l.unknown
        return <dl key={index} className="grid min-w-0 gap-1">
          <dt>{l.actor}</dt><dd>{text(actor.kind)} ? {actor.display==="TEAM"?(localeLabel("team")):actor.display==="ENGINEER"?(localeLabel("engineer")):l.unknown}</dd>
          <dt>{l.proof}</dt><dd>{support.map((raw,n)=>{const item=raw&&typeof raw==="object"?raw as Record<string,unknown>:{};return <p key={n} className="break-all">{text(item.name)} ? {l.version}: {text(item.version_id)} ? {l.hash}: {text(item.sha256)}</p>})}</dd>
          <dt>{l.reason}</dt><dd className="whitespace-pre-wrap break-words">{text(entry.reason)}</dd>
          <dt>{l.risk}</dt><dd className="whitespace-pre-wrap break-words">{text(entry.risk)}</dd>
          <dt>{l.at}</dt><dd>{text(entry.created_at)}</dd>
          {originals.map((original, n) => {const item = original && typeof original === "object" ? original as Record<string, unknown> : {}; return <div key={n}><dt>{l.verdict}</dt><dd>{text(item.verdict)}</dd><dt>{l.receipt}</dt><dd className="break-all">{text(item.receipt_hash)}</dd></div>})}
        </dl>
      })}
    </section>}
  </div>
}
