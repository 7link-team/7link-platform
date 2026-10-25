import type { components } from "../../lib/api-schema"
import { apiRoute } from "../../lib/api-route"
import { collaborationID } from "../../lib/clientwork-api"
import { iamApi } from "../../lib/iam-api"

export type IntakeChangeSource = components["schemas"]["ChangeSourceView"]
export type IntakeLedgerSource = components["schemas"]["IntakeLedgerSource"]
export type IntakeLedgerPage = components["schemas"]["PageIntakeLedgerEntry"]
export type DelegatedIntakeCard = components["schemas"]["TriageSource"]
export type TriageDraft = components["schemas"]["TriageDraft"]
export function intakeThreadReply(body: string, work: string, version: string) {
  if (!work && !version) return { body }
  const current = Number(version)
  if (!/^[1-9][0-9]*$/.test(version) || !Number.isSafeInteger(current)) throw new Error("Invalid work version")
  return { body, work_item_id: collaborationID(work), work_version: current }
}
export function intakeConversion(source: IntakeChangeSource, proof: string) {
  if (source.state !== "TRIAGED" || !Number.isSafeInteger(source.version) || source.version < 1 || !Number.isSafeInteger(source.card_revision) || source.card_revision < 1 || !/^[a-f0-9]{64}$/.test(source.proposal_hash) || !proof) throw new Error("Invalid Intake conversion")
  collaborationID(source.id)
  collaborationID(source.requirement_id)
  return { triage_card_revision: source.card_revision, proposal_hash: source.proposal_hash, reauth_proof: proof }
}
function newIntakeCard(project: string, source: IntakeLedgerSource) {
    if (source.project_id !== project || source.kind !== "NEW" || source.state !== "TRIAGED" || !Number.isSafeInteger(source.version) || source.version < 1 || !Number.isSafeInteger(source.card_revision) || source.card_revision < 1 || !source.triage || source.triage.intake_id !== source.id || source.triage.project_id !== project || source.triage.revision !== source.card_revision || source.triage.version !== source.version || !/^[a-f0-9]{64}$/.test(source.triage.hash)) throw new Error("Invalid Intake conversion")
 return {triage_card_revision: source.triage!.revision, proposal_hash: source.triage!.hash}
}
function delegatedCardBody(project: string, card: DelegatedIntakeCard) {
 collaborationID(project); collaborationID(card.intake_id); collaborationID(card.actor_id)
 if (card.project_id !== project || !Number.isSafeInteger(card.version) || card.version < 1 || !Number.isSafeInteger(card.revision) || card.revision < 1 || !/^[a-f0-9]{64}$/.test(card.hash)) throw new Error("Invalid delegated Intake card")
 return {triage_card_revision: card.revision, proposal_hash: card.hash}
}
export type ReworkDisposition = Omit<components["schemas"]["ReworkDispositionBody"], "items"> & {items: NonNullable<components["schemas"]["ReworkDispositionBody"]["items"]>}
export const createIntakeChangeApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
 requestTriage: (project: string, id: string, note: string, key: string, signal?: AbortSignal) => {
 if (Array.from(note).length > 2000) throw new Error("Invalid triage note")
 return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method:"POST",url:`/entities/${collaborationID(project)}/intakes/${collaborationID(id)}/triage-requests`},{headers:{"Idempotency-Key":key},body:JSON.stringify({note}),signal})
 },
 delegatedCard: async (project: string, id: string, signal?: AbortSignal) => {
 const card = await api.deliveryResultRequest<DelegatedIntakeCard>({method:"GET",url:`/entities/${collaborationID(project)}/intakes/${collaborationID(id)}/delegated-requirement`},{signal})
 delegatedCardBody(project,card)
 if(card.intake_id !== id) throw new Error("Invalid delegated Intake card")
 return card
 },
 delegatedRequirement: (project: string, card: DelegatedIntakeCard, key: string) => {
 const body=delegatedCardBody(project,card)
 return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method:"POST",url:`/entities/${collaborationID(project)}/intakes/${collaborationID(card.intake_id)}/delegated-requirement`},{headers:{"Idempotency-Key":key,"If-Match":`"${card.version}"`},body:JSON.stringify(body)})
 },

  dispose: (project: string, source: IntakeLedgerSource, body: ReworkDisposition, key: string) => {
    if (source.project_id !== project || source.kind !== "REWORK" || !source.rework || source.rework.id !== source.id || source.rework.version !== source.version || source.rework.project_id !== project || source.rework.receipt_id !== body.receipt_id || source.raw_content_hash !== body.source_hash || !["RECEIVED", "TRIAGED", "CONVERTED"].includes(source.state) || !Number.isSafeInteger(source.version) || source.version < 1 || !body.items.length || body.items.some(item => !source.rework!.rejection.items?.some(fact => fact.item_id === item.item_id) || !item.reason.trim() || !item.client_message.trim() || (item.mode === "REOPEN_WORK_ITEMS" && (!Number.isSafeInteger(item.work_version) || item.work_version! < 1)))) throw new Error("Invalid rejected subset")
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method: "POST", url: `/entities/${collaborationID(project)}/intakes/${collaborationID(source.id)}/dispositions`}, {headers: {"Idempotency-Key": key, "If-Match": `"${source.version}"`}, body: JSON.stringify(body)})
  },
  newProject: (project: string, source: IntakeLedgerSource, name: string, code: string, proof: string, key: string) => {
    const card = newIntakeCard(project, source)
    if (!name.trim() || Array.from(name).length > 200 || !/^[A-Z][A-Z0-9-]{1,15}$/.test(code) || !proof) throw new Error("Invalid Intake project")
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method:"POST",url:`/entities/${collaborationID(project)}/intakes/${collaborationID(source.id)}/project`}, {headers:{"Idempotency-Key":key,"If-Match":`"${source.version}"`},body:JSON.stringify({...card,project:{name,code},reauth_proof:proof})})
  },
  newRequirement: (project: string, source: IntakeLedgerSource, key: string) => {
    const card = newIntakeCard(project, source)
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>({method: "POST", url: `/entities/${collaborationID(project)}/intakes/${collaborationID(source.id)}/requirement`}, {headers: {"Idempotency-Key": key, "If-Match": `"${source.version}"`}, body: JSON.stringify(card)})
  },
  list: async (project: string, cursor: string, signal?: AbortSignal) => {
    const result = await api.deliveryResultRequest<IntakeLedgerPage>(apiRoute("get", "/entities/{entity_id}/intakes", { path: { entity_id: collaborationID(project) }, query: { cursor, limit: 50 } }), { signal })
    if ((result.items ?? []).some(row => row.project_id !== project)) throw new Error("Invalid Intake project source")
    return result
  },
  source: async (project: string, id: string, signal?: AbortSignal) => {
    const result = await api.deliveryResultRequest<IntakeLedgerSource>(apiRoute("get", "/entities/{entity_id}/intakes/{id}", { path: { entity_id: collaborationID(project), id: collaborationID(id) } }), { signal })
    if (result.id !== id || result.project_id !== project || (result.card && (result.card.id !== id || result.card.project_id !== project || result.card.card_revision !== result.card_revision)) || (result.triage && (result.triage.intake_id !== id || result.triage.project_id !== project || result.triage.revision !== result.card_revision || result.triage.version !== result.version))) throw new Error("Invalid Intake project source")
    return result
  },
  triage: (project: string, source: IntakeLedgerSource, body: TriageDraft, key: string, signal?: AbortSignal) => {
    if (source.project_id !== project || !["NEW", "REWORK"].includes(source.kind) || !["RECEIVED", "TRIAGED"].includes(source.state) || !Number.isSafeInteger(source.version) || source.version < 1) throw new Error("Invalid Intake triage source")
    return api.deliveryResultRequest<components["schemas"]["Receipt"]>(apiRoute("post", "/entities/{entity_id}/intakes/{id}/triage-cards", { path: { entity_id: collaborationID(project), id: collaborationID(source.id) } }), {
      signal, headers: { "Idempotency-Key": key, "If-Match": `"${source.version}"` }, body: JSON.stringify(body),
    })
  },
  get: (project: string, id: string, signal?: AbortSignal) => api.deliveryResultRequest<IntakeChangeSource>(apiRoute("get", "/entities/{entity_id}/intake-changes/{id}", { path: { entity_id: collaborationID(project), id: collaborationID(id) } }), { signal }),
  draft: (project: string, original: string, change: string, key: string) => api.deliveryResultRequest<components["schemas"]["Receipt"]>(apiRoute("post", "/entities/{entity_id}/intake-changes", { path: { entity_id: collaborationID(project) } }), {
    headers: { "Idempotency-Key": key }, body: JSON.stringify({ original_intake_id: collaborationID(original), change_id: collaborationID(change) }),
  }),
  convert: (project: string, source: IntakeChangeSource, proof: string, key: string) => api.deliveryResultRequest<components["schemas"]["Receipt"]>(apiRoute("post", "/entities/{entity_id}/intake-changes/{id}/conversion", { path: { entity_id: collaborationID(project), id: collaborationID(source.id) } }), {
    headers: { "Idempotency-Key": key, "If-Match": `"${source.version}"` }, body: JSON.stringify(intakeConversion(source, proof)),
  }),
})
export const intakeChangeApi = createIntakeChangeApi(iamApi)
