import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"

export type Workdir = {id: string; project_id: string; repository_id: string; path: string; version: number; archived_at?: string}
export type WorkdirChange = {path: string; archived: boolean}
export const createWorkdirsApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => ({
  update: (machine: string, row: Workdir, body: WorkdirChange, key: string) => api.deliveryResultRequest<Workdir>({method: "PUT", url: `/machines/${collaborationID(machine)}/workdirs/${collaborationID(row.id)}`}, {headers: {"If-Match": String(row.version), "Idempotency-Key": key}, body: JSON.stringify(body)}),
  probe: (machine: string, id: string) => api.deliveryResultRequest<unknown>({method: "POST", url: `/machines/${collaborationID(machine)}/inspections`}, {body: JSON.stringify({kind: "CAPABILITIES", workdir_id: collaborationID(id)})}),
})
export const workdirsApi = createWorkdirsApi(iamApi)
