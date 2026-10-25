import {iamApi} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"
export type ModelCatalog = {runtime: "CODEX" | "CLAUDE_CODE"; observed_ids: string[]; manual_ids: string[]; families: Record<string, string>; resolved_families?: Record<string, string>; version: number}
export type ManualModelsUpdate = {expected_version: number; model_ids: string[]; families: Record<string, string>}
export const createModelCatalogApi = (api: Pick<typeof iamApi, "deliveryResultRequest">) => {
  const path = (machine: string, runtime: ModelCatalog["runtime"]) => `/machines/${collaborationID(machine)}/model-catalogs/${encodeURIComponent(runtime)}`
  return {
    get: (machine: string, runtime: ModelCatalog["runtime"], signal?: AbortSignal) => api.deliveryResultRequest<ModelCatalog>({method: "GET", url: path(machine, runtime)}, {signal}),
    replace: (machine: string, runtime: ModelCatalog["runtime"], data: ManualModelsUpdate, signal?: AbortSignal) => api.deliveryResultRequest<ModelCatalog>({method: "PUT", url: `${path(machine, runtime)}/manual-models`}, {signal, body: JSON.stringify(data)}),
  }
}
export const modelCatalogApi = createModelCatalogApi(iamApi)
