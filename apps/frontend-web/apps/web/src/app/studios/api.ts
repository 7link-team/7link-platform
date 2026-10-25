import { iamApi } from "../../lib/iam-api"

export type StudioDomain = "VIDEO" | "NOVEL" | "TEAM_TASKS"
export type StudioContent = { title: string; text: string; source: string; draft_type?: "ACTION" | "TRIAGE" | "STATUS"; reported_state?: string; owner_id?: string; due_at?: string; duration_ms: number; provider: string; model: string; seed: string; team?: TeamTaskData; novel?: NovelData; video?: VideoData }
export type TeamTaskData = {
 work_item_id: string; signer_id: string
 checklist: {text: string; done: boolean; evidence_index: number}[]
 evidence: {url: string; version: string; modified_at: string}[]
 risks: {title: string; owner_id: string; due_at: string; disposition: string; resolved: boolean}[]
 sign_offs: {actor_id: string; at: string; scope: string; content_hash: string; work_version: number}[]
 validation?: {at: string; content_hash: string; work_version: number}
}
export type TeamTaskView = {record: StudioRecord; work: {id: string; title: string; state: string; risk: string; owner_id: string; author_id?: string; version: number; started_at: string}; complete: number; signed: boolean}
export type TeamBoardPage = {items: TeamTaskView[]; drafts: StudioRecord[]; next_cursor?: string; has_more: boolean}
export type TeamEdit = {version: number; action: "SAVE" | "SIGN_OFF" | "CHECK_EVIDENCE" | "EDIT_DRAFT" | "ADOPT_DRAFT"; reason: string; content: StudioContent}
export type StudioDraft = { domain: StudioDomain; kind: string; parent_id?: string; content: StudioContent }
export type StudioRecord = StudioDraft & { id: string; binding_id: string; selected_id?: string; state: string; version: number; created_by: string; created_at: string }
export type StudioPage = { items: StudioRecord[]; next_cursor?: string; has_more: boolean }
export function canConfirm(record: StudioRecord) { return record.kind === "ASSISTANT_DRAFT" && record.state === "OPEN" && /^[1-9][0-9]*$/.test(record.content.owner_id ?? "") && !!record.content.due_at && Number.isFinite(Date.parse(record.content.due_at)) }
export function studioSource(source: string) { try {const url = new URL(source);return ["https:", "http:"].includes(url.protocol) ? url.href : null} catch {return null} }
const root = (tenant: string, project: string) => `/tenants/${encodeURIComponent(tenant)}/projects/${encodeURIComponent(project)}/studio-records`
export const studiosApi = {
 team: (tenant: string, project: string, cursor: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<TeamBoardPage>({method: "GET", url: `${root(tenant, project).replace("studio-records", "team-task-board")}?limit=50&sort=id${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`}, {signal}),
 teamEdit: (tenant: string, project: string, id: string, input: TeamEdit, key: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<TeamTaskView>({method: "POST", url: `${root(tenant, project).replace("studio-records", "team-task-board")}${id ? `/${encodeURIComponent(id)}/decisions` : ""}`}, {signal, headers: {"Idempotency-Key": key}, body: JSON.stringify(input)}),
 list: (tenant: string, project: string, domain: StudioDomain, cursor: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<StudioPage>({method: "GET", url: `${root(tenant, project)}?domain=${domain}&limit=50&sort=id${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`}, {signal}),
 create: (tenant: string, project: string, draft: StudioDraft, key: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<StudioRecord>({method: "POST", url: root(tenant, project)}, {signal, headers: {"Idempotency-Key": key}, body: JSON.stringify(draft)}),
 decide: (tenant: string, project: string, record: StudioRecord, action: string, reason: string, key: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<StudioRecord>({method: "POST", url: `${root(tenant, project)}/${encodeURIComponent(record.id)}/decisions`}, {signal, headers: {"Idempotency-Key": key}, body: JSON.stringify({version: record.version, action, reason})}),
}

export function teamCanSave(content: StudioContent) {
 const team = content.team
 return !!team && !!content.title.trim() && /^[1-9][0-9]*$/.test(content.owner_id ?? "") && !!content.due_at && (team.work_item_id === "" || team.work_item_id === "0" || /^[1-9][0-9]*$/.test(team.work_item_id)) && /^[1-9][0-9]*$/.test(team.signer_id) && team.checklist.length > 0 && team.checklist.every(item => !!item.text.trim() && (!item.done || !!team.evidence[item.evidence_index]))
}
export function teamCanSign(task: TeamTaskView) {
 const team = task.record.content.team
 return !!team && team.checklist.length > 0 && task.complete === team.checklist.length && team.evidence.length > 0 && team.risks.every(risk => risk.resolved && !!risk.disposition.trim()) && !["CANCELLED", "STOPPED"].includes(task.work.state)
}


export type NovelEntry = {key: string; category: "WORLD" | "LOCATION" | "TERM"; entity: string; attribute: string; value: string; text: string; invariant: boolean}
export type NovelFact = {entity: string; attribute: string; value: string; tick: number; start: number; quote: string}
export type NovelData = {
 entries: NovelEntry[]; characters: {name: string; motivation: string; arc: string}[]
 beats: {name: string; target_percent: number; chapter_id: string | null}[]
 chapter?: {number: number; target_words: number; pov: string; plan: string; facts: NovelFact[]}
 foreshadow?: {planted: string | null; planned: string | null; resolved: string | null; retained: boolean; reason: string}
 report?: NovelReport
}
export type NovelCitation = {record_id: string; version: number; start: number; quote: string; sha256: string; entry_key?: string}
export type NovelReport = {engine: string; at: string; input_sha256: string; coverage: {id: string; version: number; sha256: string; words: number}[]; words: number; facts: number; issues: {code: string; category: string; a: NovelCitation; b: NovelCitation}[]; incomplete: string[]; open_foreshadows: string[]; ced: string; semantic_availability: string}
export type NovelWorkbench = {items: StudioRecord[]; report?: StudioRecord; input_sha256: string; stale: boolean}
export type NovelEdit = {version: number; action: "SAVE" | "RUN"; kind: string; reason: string; content: StudioContent}
// Editors use empty/zero options; the platform ID wire contract uses null.
const optionalID = (value: string | null) => value === "" || value === "0" ? null : value
function novelCommand(input: NovelEdit) {
 const n = input.content.novel
 return {...input, content: {...input.content, ...(n ? {novel: {...n, beats: n.beats.map(b => ({...b, chapter_id: optionalID(b.chapter_id)})), ...(n.foreshadow ? {foreshadow: {...n.foreshadow, planted: optionalID(n.foreshadow.planted), planned: optionalID(n.foreshadow.planned), resolved: optionalID(n.foreshadow.resolved)}} : {})}} : {})}}
}
const novelRoot = (tenant: string, project: string) => root(tenant, project).replace("studio-records", "novel-workbench")
export const novelApi = {
 read: (tenant: string, project: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<NovelWorkbench>({method: "GET", url: novelRoot(tenant, project)}, {signal}),
 edit: (tenant: string, project: string, id: string, input: NovelEdit, key: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<StudioRecord>({method: "POST", url: `${novelRoot(tenant, project)}${id ? `/${encodeURIComponent(id)}/decisions` : ""}`}, {signal, headers: {"Idempotency-Key": key}, body: JSON.stringify(novelCommand(input))}),
}
export function novelEvidenceText(record: StudioRecord, cite: NovelCitation) {
 if (record.id !== cite.record_id || record.version !== cite.version) return null
 if (cite.entry_key) return record.content.novel?.entries.find(entry => entry.key === cite.entry_key)?.text === cite.quote ? {before: "", quote: cite.quote, after: ""} : null
 const chars = Array.from(record.content.text)
 if (cite.start < 0 || chars.slice(cite.start, cite.start + Array.from(cite.quote).length).join("") !== cite.quote) return null
 return {before: chars.slice(0, cite.start).join(""), quote: cite.quote, after: chars.slice(cite.start + Array.from(cite.quote).length).join("")}
}

export type VideoArtifact = {evidence_id: string; binding_id: string; file_id: string; version_id: string; sha256: string}
export type VideoGeneration = {id: string; project_id: string; execution_id: string; provider_id: string; provider_version: number; adapter_key: string; state: string; availability: string; job_ref: string; request_sha256: string; output_sha256: string; created_at: string; output: {outputs: {file_ref: string; sha256: string; model: string; model_version: string; seed: string}[]; reported_cost: string; currency: string}}
export type VideoData = {role: "SHOT" | "REFERENCE" | "MASTER" | "CANDIDATE"; character: string; framing: string; execution_id: string | null; generation_id: string | null; evidence_id: string | null; output_index: number; artifact?: VideoArtifact; generation?: VideoGeneration}
export type VideoExecution = {execution_id: string; work_item_id: string; state: string; artifacts: VideoArtifact[]; qc: {task_id: string; result_id: string; result_version: number; verifier: string; method: string; stage: string; verdict: string; reason: string; receipt_sha256: string; head_commit: string}[]}
export type VideoWorkbench = {items: StudioRecord[]; executions: VideoExecution[]; generations: VideoGeneration[]; providers: {id: string; adapter_key: string; display_name: string; state: string; health_checked_at?: string; limitations: string; license_notice: string; region_notice: string; version: number}[]; bindings: {version: number; providers: {kind: string; provider_id: string; provider_version: number}[]}; availability: string}
export type VideoEdit = {action: "SAVE" | "GENERATE" | "IMPORT" | "SELECT"; version: number; parent_id: string | null; reason: string; content: StudioContent}
const videoRoot = (tenant: string, project: string) => root(tenant, project).replace("studio-records", "video-workbench")
export const videoApi = {
 read: (tenant: string, project: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<VideoWorkbench>({method: "GET", url: videoRoot(tenant, project)}, {signal}),
 edit: (tenant: string, project: string, id: string, input: VideoEdit, key: string, signal?: AbortSignal) => iamApi.deliveryResultRequest<StudioRecord>({method: "POST", url: `${videoRoot(tenant, project)}${id ? `/${encodeURIComponent(id)}/decisions` : ""}`}, {signal, headers: {"Idempotency-Key": key}, body: JSON.stringify({...input, parent_id: optionalID(input.parent_id), content: {...input.content, ...(input.content.video ? {video: {...input.content.video, execution_id: optionalID(input.content.video.execution_id), generation_id: optionalID(input.content.video.generation_id), evidence_id: optionalID(input.content.video.evidence_id)}} : {})}})}),
 content: (tenant: string, project: string, id: string, signal?: AbortSignal) => iamApi.deliveryResultDownload({method: "GET", url: `${videoRoot(tenant, project)}/${encodeURIComponent(id)}/content`}, {maxBytes: 100 * 1024 * 1024, signal}),
 bind: (tenant: string, project: string, version: number, provider: string, key: string, signal?: AbortSignal) => iamApi.deliveryResultRequest({method: "PUT", url: `/tenants/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}/template-binding/providers`}, {signal, headers: {"Idempotency-Key": key, "If-Match": String(version)}, body: JSON.stringify({providers: {VIDEO_GEN: provider}})}),
}
