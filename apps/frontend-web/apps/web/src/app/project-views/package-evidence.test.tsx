import { expect, it } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import type { components } from "../../lib/api-schema"
import { ArtifactPicker } from "./client-pickers"
import { ClientPickerScope } from "./client-picker-scope"
import { PackageArtifactDisclosure, PackageEvidenceMapping } from "./package-evidence"
import { packageEvidenceLabels } from "./package-evidence-labels"

const hash = "a".repeat(64)
const result: components["schemas"]["ResultView"] = {
  id: "9007199254740993", work_item_id: "42", project_id: "3", actor_id: "11", sequence: 2, summary: "Frozen source", created_at: "2026-10-23T00:00:00Z",
  actions: [], handoffs: [], verifications: [], unverified: [],
  completion: {human_override:true,internal_qa_required:true,l1_claimed:true,l2_internal_qa_passed:false,l3_team_accepted:true,l4_client_accepted:false},
  criteria_map: [{criterion_id:"9007199254740995",method:"TEST",coverage:"COVERED",verdict:"FAILED",evidence_ids:["9007199254740997","missing"],verification_ids:["9007199254740999"]}],
  evidence: [{id:"9007199254740997",criterion_id:"9007199254740995",binding_id:"1",space_id:"2",file_id:"3",version_id:"9007199254741001",coverage:"COVERED",note:"Association only",name:"report <unsafe>",sha256:hash}],
  overrides:[{id:"8",actor_id:"11",created_at:"2026-10-23T00:00:00Z",evidence_ids:["9007199254740997"],reason:"Known failure <script>",risk:"Manual risk retained",original_verdicts:[{id:"9",verdict:"FAILED",receipt_hash:hash}]}],
}

it("renders frozen criterion/evidence/version/receipt associations and an original failed verdict beside Human override in every locale", () => {
  for (const locale of ["en-US","zh-CN","ms-MY"] as const) {
    const labels = packageEvidenceLabels[locale]
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><PackageEvidenceMapping result={result}/></IntlAppProvider>)
    for (const id of ["9007199254740995","9007199254740997","9007199254740999","9007199254741001",hash]) expect(html).toContain(id)
    for (const value of [labels.title,labels.hint,labels.overrideHint,labels.risk,labels.proof,labels.actor,labels.unknown,"FAILED","COVERED","Manual risk retained"]) expect(html).toContain(value)
    expect(html).not.toContain("PASSED")
    expect(html).toContain("report &lt;unsafe&gt;")
    expect(html).toContain("Known failure &lt;script&gt;")
    const empty = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><PackageEvidenceMapping result={{...result,criteria_map:[],overrides:[]}}/></IntlAppProvider>)
    expect(empty).toContain(labels.missing)
    expect(empty).not.toContain(labels.overrideHint)
  }
})

it("public artifact disclosure whitelists manifest fields and does not leak arbitrary override properties", () => {
  for (const locale of ["en-US","zh-CN","ms-MY"] as const) {
    const artifact = {name:"file",version_id:"9007199254741001",sha256:hash,coverage:"COVERED",verification_overrides:[{actor:{kind:"HUMAN",display:"TEAM",private_id:"NEVER_RENDER_ACTOR_ID"},supporting_evidence:[{name:"review-evidence.txt",version_id:"9007199254741011",sha256:hash,storage_key:"NEVER_RENDER_STORAGE"}],reason:"Accepted risk",risk:"Original failure",created_at:"2026-10-23T00:00:00Z",private_prompt:"NEVER_RENDER_PROMPT",actor_id:"PRIVATE_INTERNAL_ID",original_verdicts:[{verdict:"FAILED",receipt_hash:hash,debug:"NEVER_RENDER_DEBUG"}]}]}
    const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><PackageArtifactDisclosure artifact={artifact}/></IntlAppProvider>)
    expect(html).toContain(packageEvidenceLabels[locale].overrideHint)
    for(const value of ["FAILED", "HUMAN", "review-evidence.txt", "9007199254741011", "Original failure", "9007199254741001", hash]) expect(html).toContain(value)
    for(const value of ["NEVER_RENDER_PROMPT","PRIVATE_INTERNAL_ID","NEVER_RENDER_DEBUG","NEVER_RENDER_ACTOR_ID","NEVER_RENDER_STORAGE","PASSED"]) expect(html).not.toContain(value)
    for(const malformed of [null,{reason:"not an array"},[null,42,{original_verdicts:[null,{}]}]]) {
      expect(() => renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><PackageArtifactDisclosure artifact={{...artifact,verification_overrides:malformed}}/></IntlAppProvider>)).not.toThrow()
    }
  }
})

it("keeps cross-work selected evidence visible and removable without fetching or requiring its work to remain on the page", () => {
  const html=renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={{iam:{clientChooseWork:"Choose work",clientAcceptedWork:"Accepted work",clientSelected:"{count} selected",fleetPrevious:"Previous",fleetNext:"Next"}}}><ClientPickerScope.Provider value={{project:"3",collection:"packages"}}><ArtifactPicker name="artifacts" config={{}} required={false} disabled={false} onChange={()=>{}} labelId="artifact-label" value={[{work_item_id:"9007199254740993",result_id:"9007199254740995",evidence_id:"9007199254740997"}]}/></ClientPickerScope.Provider></IntlAppProvider>)
  for(const id of ["9007199254740993","9007199254740995","9007199254740997"]) expect(html).toContain(id)
  expect(html).toContain('aria-label="Remove 9007199254740997"')
  expect(html).toContain('type="button"')
})
