import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"

export interface ReleaseNoteDraft {
  id: string; package_id: string; project_id: string; source_fingerprint: string; source_snapshot: string
  requested_by: string; drafter_principal_id: string; note: string; content?: string; state: string
  adopted_by?: string; version: number; created_at: string; updated_at: string
}
export const createReleaseNotesApi = (api: Pick<typeof iamApi,"deliveryResultRequest">) => {
  const path = (project:string,id:string) => `/entities/${collaborationID(project)}/delivery-packages/${collaborationID(id)}/release-note-drafts`
  return {
    list: (project:string,id:string,signal?:AbortSignal) => api.deliveryResultRequest<ReleaseNoteDraft[]>({method:"GET",url:path(project,id)},{signal}),
    request: (project:string,id:string,version:number,note:string,key:string) => {
      if (!Number.isSafeInteger(version)||version<1||!note.trim()||Array.from(note).length>2000) throw new Error("Invalid draft request")
      return api.deliveryResultRequest({method:"POST",url:path(project,id)},{headers:{"If-Match":`"${version}"`,"Idempotency-Key":key},body:JSON.stringify({note})})
    },
    adopt: (project:string,id:string,packageVersion:number,draft:ReleaseNoteDraft,content:string,key:string) => {
      if (!Number.isSafeInteger(packageVersion)||packageVersion<1||!Number.isSafeInteger(draft.version)||draft.version<1||draft.state!=="DRAFT"||!/^[a-f0-9]{64}$/.test(draft.source_fingerprint)||!content.trim()||content.trim()===draft.content?.trim()||Array.from(content).length>8000) throw new Error("Human final edits required")
      return api.deliveryResultRequest({method:"POST",url:`${path(project,id)}/${collaborationID(draft.id)}/adoption`},{headers:{"If-Match":`"${draft.version}"`,"Idempotency-Key":key},body:JSON.stringify({source_fingerprint:draft.source_fingerprint,package_version:packageVersion,content})})
    },
  }
}
export const releaseNotesApi = createReleaseNotesApi(iamApi)
