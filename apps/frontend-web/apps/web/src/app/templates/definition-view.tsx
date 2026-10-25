import { useState, type ReactNode } from "react"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import type { TemplateVersion } from "./workflow"

const metadataLabels: Record<string,string> = {
 domain:"templateLibrary_domain",origin:"templateLibrary_origin",state:"templateLibrary_state",semver:"templateLatestVersion",team_modes:"templateLibrary_team_mode",constitution_seed:"templateConstitutionSeed",metadata:"templateMetadata",artifacts:"templateArtifacts",artifact_schemas:"templateArtifacts",verifiers:"templateVerifiers",rubric:"templateRubric",internal_quality_gate:"templateQualityGates",quality_gates:"templateQualityGates",description:"description",name:"name",version:"templateMetadataVersion",schema:"templateMetadataSchema",required:"templateMetadataRequired",limitations:"templateMetadataLimitations",dimensions:"templateMetadataDimensions",weight:"templateMetadataWeight",threshold:"templateMetadataThreshold",verifier_key:"templateMetadataVerifier",required_tools:"templateMetadataTools",calibration_id:"templateMetadataCalibration",evidence_refs:"templateMetadataEvidence",hard_gate:"templateMetadataHardGate",
}
// Render declarations without treating unexecuted verifier or calibration
// metadata as runtime facts. Arbitrary extension fields remain inspectable.
export function MetadataValue({value, label}: {value: unknown; label: (key:string)=>string}): ReactNode {
 if(value===undefined || value===null)return <span className="text-muted-foreground">{label("templateMetadataAbsent")}</span>
 if(typeof value==="boolean")return <span>{label(value ? "templateValueTrue" : "templateValueFalse")}</span>
 if(Array.isArray(value))return value.length ? <ol className="list-inside list-decimal space-y-2">{value.map((item,index)=><li key={index} className="break-words"><MetadataValue value={item} label={label}/></li>)}</ol> : <span className="text-muted-foreground">{label("templateMetadataEmpty")}</span>
 if(typeof value==="object")return Object.keys(value).length ? <dl className="divide-y">{Object.entries(value).map(([key,entry])=><div key={key} className="grid gap-2 py-2 sm:grid-cols-[minmax(8rem,1fr)_3fr]"><dt className="break-words text-sm text-muted-foreground">{metadataLabels[key] ? label(metadataLabels[key]) : key.replaceAll("_"," ")}</dt><dd className="min-w-0 break-words whitespace-pre-wrap text-sm"><MetadataValue value={entry} label={label}/></dd></div>)}</dl> : <span className="text-muted-foreground">{label("templateMetadataEmpty")}</span>
 return <span className="whitespace-pre-wrap break-words">{String(value)}</span>
}

export function TemplateDefinitionView({version,calibration,evaluation,providers}: {version: TemplateVersion;calibration?:ReactNode;evaluation?:ReactNode;providers?:ReactNode}) {
 const t = useTranslations("iam")
 const [section, setSection] = useState("overview")
 const metadata = version.definition.metadata
 const sections: Record<string, {label: string; value: unknown}> = {
  overview: {label: t("templateOverview"), value: {domain: version.definition.domain, origin: version.origin, state: version.state, semver: version.definition.semver, team_modes: metadata.team_modes, constitution_seed: version.definition.constitution_seed, metadata}},
  artifacts: {label: t("templateArtifacts"), value: metadata.artifacts ?? metadata.artifact_schemas},
  verifiers: {label: t("templateVerifiers"), value: metadata.verifiers},
  rubric: {label: t("templateRubric"), value: metadata.rubric},
  gates: {label: t("templateQualityGates"), value: metadata.internal_quality_gate ?? metadata.quality_gates},
  ...(calibration?{calibration:{label:t("templateCalibrationTitle"),value:null}}:{}),
  ...(evaluation?{evaluation:{label:t("templateEvalTitle"),value:null}}:{}),
  ...(providers?{providers:{label:t("templateProviderTitle"),value:null}}:{}),
 }
 const current = sections[section]!
 return <section className="space-y-3 rounded-md border p-4"><nav aria-label={t("templateDefinitionSections")} className="flex flex-wrap gap-2">{Object.entries(sections).map(([key, value]) => <Button key={key} variant={section === key ? "default" : "outline"} aria-pressed={section === key} onClick={() => setSection(key)}>{value.label}</Button>)}</nav>{section==="calibration"?calibration:section==="evaluation"?evaluation:section==="providers"?providers:<><h2 className="font-medium">{current.label}</h2><p className="text-sm text-muted-foreground">{t("templateDefinitionNotice")}</p>{current.value === undefined || current.value === null ? <p>{t("templateMetadataAbsent")}</p> : <div className="max-h-[36rem] overflow-auto"><MetadataValue value={current.value} label={key=>t(key)}/></div>}</>}</section>
}
